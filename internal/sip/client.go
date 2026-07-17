package sip

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

type Client struct {
	ua            gosip.Server
	host          string // Domain/Realm (e.g., multifon.ru)
	outboundProxy string // Registrar (e.g., sbc.megafon.ru)
	port          int
	user          string
	password      string

	publicIP   string
	publicPort int
	mu         sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	debug  bool

	// Callback for critical system errors (SBC failures, registration loss)
	OnError func(err error)

	// Auth state for RFC 2617/7616 compliance
	authMu    sync.Mutex
	ncCounter uint32
	lastNonce string
}

func NewClient(host, outboundProxy string, port int, user, password string, debug bool) (*Client, error) {
	logger := log.NewDefaultLogrusLogger()
	if !debug {
		logger.SetLevel(uint32(log.ErrorLevel))
	}
	ua := gosip.NewServer(gosip.ServerConfig{
		UserAgent: "Asterisk PBX",
	}, nil, nil, logger)

	if err := ua.Listen("udp", "0.0.0.0:5060"); err != nil {
		return nil, fmt.Errorf("sip listen error: %w", err)
	}

	// Register handler for incoming BYE (remote hangup)
	ua.OnRequest(sip.BYE, func(req sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest("", req, 200, "OK", "")
		tx.Respond(res)
	})

	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		ua:            ua,
		host:          host,
		outboundProxy: outboundProxy,
		port:          port,
		user:          user,
		password:      password,
		ctx:           ctx,
		cancel:        cancel,
		debug:         debug,
	}

	return c, nil
}

func (c *Client) Start() {
	go func() {
		// Use exponential backoff for registration attempts
		// Default interval increased to 240s per requirement to reduce SBC load
		delay := 120 * time.Second
		for {
			select {
			case <-time.After(delay):
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				if err := c.Register(ctx); err != nil {
					fmt.Printf("Periodic registration failed: %v\n", err)
					if c.OnError != nil {
						c.OnError(fmt.Errorf("SIP Registration failed: %w", err))
					}

					// RFC-compliant backoff: double the delay on failure to avoid flooding SBC during IP bans
					delay *= 2
					if delay > 30*time.Minute {
						delay = 30 * time.Minute
					}
					fmt.Printf("Retrying registration in %v to recover from potential temporary ban\n", delay)
				} else {
					fmt.Println("Periodic registration successful")
					// Reset delay to standard 240s on success
					delay = 120 * time.Second
				}
				cancel()
			case <-c.ctx.Done():
				return
			}
		}
	}()
}

func (c *Client) Close() {
	c.cancel()
	c.ua.Shutdown()
}

func (c *Client) Register(ctx context.Context) error {
	userUri := &sip.SipUri{}
	userUri.SetUser(sip.String{Str: c.user})
	userUri.SetHost(c.host)

	domainUri := &sip.SipUri{}
	domainUri.SetHost(c.host)

	req := c.createBaseRequest(sip.REGISTER, userUri, userUri, domainUri)
	expires := sip.Expires(180)
	req.AppendHeader(&expires)

	return c.doRequestWithAuth(ctx, req)
}

func (c *Client) OpenBarrier(ctx context.Context, phoneNumber string) error {
	err := c.openBarrierInternal(ctx, phoneNumber)
	if err != nil && strings.Contains(err.Error(), "500") {
		// Megafon Multifon occasionally glitches with 500 error when session state is stale.
		// Attempting a re-registration to refresh SBC session and then retrying once.
		fmt.Printf("[SIP] Received 500 error, attempting re-registration to refresh session...\n")
		regCtx, regCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = c.Register(regCtx)
		regCancel()

		// Small delay before retry
		time.Sleep(1 * time.Second)
		return c.openBarrierInternal(ctx, phoneNumber)
	}
	return err
}

func (c *Client) openBarrierInternal(ctx context.Context, phoneNumber string) error {
	phoneNumber = strings.TrimPrefix(phoneNumber, "+")

	userUri := &sip.SipUri{}
	userUri.SetUser(sip.String{Str: c.user})
	userUri.SetHost(c.host)

	toUri := &sip.SipUri{}
	toUri.SetUser(sip.String{Str: phoneNumber})
	toUri.SetHost(c.host)

	ip := getLocalIP()
	c.mu.RLock()
	if c.publicIP != "" {
		ip = c.publicIP
	}
	c.mu.RUnlock()

	// Megafon Multifon requires a non-zero RTP port even if media is inactive.
	// We use a deterministic port based on the last digits of the barrier's phone.
	// This makes traffic patterns predictable for the SBC (e.g., ...258 -> 40258).
	portOffset := 0
	cleanPhone := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phoneNumber)
	if len(cleanPhone) >= 3 {
		fmt.Sscanf(cleanPhone[len(cleanPhone)-3:], "%d", &portOffset)
	}
	dummyRtpPort := 40000 + (portOffset % 1000)

	sdp := fmt.Sprintf("v=0\r\n"+
		"o=- %d %d IN IP4 %s\r\n"+
		"s=-\r\n"+
		"c=IN IP4 %s\r\n"+
		"t=0 0\r\n"+
		"m=audio %d RTP/AVP 8 0\r\n"+
		"a=rtpmap:8 PCMA/8000\r\n"+
		"a=rtpmap:0 PCMU/8000\r\n"+
		"a=sendonly\r\n"+
		"a=ptime:20\r\n", time.Now().Unix(), time.Now().Unix(), ip, ip, dummyRtpPort)

	req := c.createBaseRequest(sip.INVITE, userUri, toUri, toUri)
	req.AppendHeader(&sip.GenericHeader{HeaderName: "Allow", Contents: "INVITE, ACK, CANCEL, BYE, NOTIFY, REFER, MESSAGE, INFO, SUBSCRIBE"})
	contentType := sip.ContentType("application/sdp")
	req.AppendHeader(&contentType)
	req.SetBody(sdp, true)

	if c.debug {
		fmt.Printf("[SIP] Outgoing INVITE:\n%s\n", req.String())
	}

	tx, err := c.ua.Request(req)
	if err != nil {
		return err
	}

	var response sip.Response
	var lastReq sip.Request = req
	// Handle provisional responses (like 100 Trying, 180 Ringing)
	for {
		select {
		case res := <-tx.Responses():
			if c.debug {
				fmt.Printf("[SIP] Incoming response to INVITE: %d %s\n%s\n", res.StatusCode(), res.Reason(), res.String())
			}
			if res.StatusCode() < 200 {
				continue // Skip provisional responses
			}
			if res.StatusCode() == 401 || res.StatusCode() == 407 {
				authHeader := c.buildAuthHeader(res, string(sip.INVITE), req.Recipient().String())

				newReq := req.Clone().(sip.Request)
				newReq.AppendHeader(authHeader)
				if cseq, ok := newReq.CSeq(); ok {
					cseq.SeqNo++
				}

				if c.debug {
					fmt.Printf("[SIP] Outgoing Auth-INVITE:\n%s\n", newReq.String())
				}

				lastReq = newReq
				tx2, err := c.ua.Request(newReq)
				if err != nil {
					return err
				}
				// Handle responses for the second request
				for {
					res2 := <-tx2.Responses()
					if c.debug {
						fmt.Printf("[SIP] Incoming response to Auth-INVITE: %d %s\n%s\n", res2.StatusCode(), res2.Reason(), res2.String())
					}
					if res2.StatusCode() < 200 {
						continue
					}
					response = res2
					break
				}
			} else {
				response = res
			}
			goto handled
		case <-ctx.Done():
			return ctx.Err()
		}
	}

handled:

	if response.StatusCode() != 200 {
		return fmt.Errorf("call failed: %d %s", response.StatusCode(), response.Reason())
	}

	ack := sip.NewAckRequest("", lastReq, response, "", nil)
	c.ua.Send(ack)

	select {
	case <-time.After(1 * time.Second):
	case <-ctx.Done():
	}

	// Create BYE request within the same dialog
	bye := lastReq.Clone().(sip.Request)
	bye.SetMethod(sip.BYE)
	// Must use 'To' header from the response as it contains the required 'tag'
	if to, ok := response.To(); ok {
		bye.ReplaceHeaders(to.Name(), []sip.Header{to})
	}
	if cseq, ok := bye.CSeq(); ok {
		cseq.SeqNo++
		cseq.MethodName = sip.BYE
	}
	// Remove media headers and body
	bye.RemoveHeader("Content-Type")
	bye.RemoveHeader("Content-Length")
	bye.SetBody("", true)

	if c.debug {
		fmt.Printf("[SIP] Outgoing BYE:\n%s\n", bye.String())
	}
	c.ua.Send(bye)

	return nil
}

func (c *Client) doRequestWithAuth(ctx context.Context, req sip.Request) error {
	if c.debug {
		fmt.Printf("[SIP] Outgoing request (%s):\n%s\n", req.Method(), req.String())
	}
	tx, err := c.ua.Request(req)
	if err != nil {
		return err
	}

	var response sip.Response
	select {
	case res := <-tx.Responses():
		if c.debug {
			fmt.Printf("[SIP] Incoming response to %s: %d %s\n%s\n", req.Method(), res.StatusCode(), res.Reason(), res.String())
		}
		if res.StatusCode() < 200 {
			// Long-polling for final response if first was provisional
			for {
				select {
				case r := <-tx.Responses():
					if r.StatusCode() >= 200 {
						res = r
						goto auth_check
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}

	auth_check:
		if res.StatusCode() == 401 || res.StatusCode() == 407 {
			authUri := req.Recipient().String()
			authHeader := c.buildAuthHeader(res, string(req.Method()), authUri)

			newReq := req.Clone().(sip.Request)
			newReq.AppendHeader(authHeader)
			if cseq, ok := newReq.CSeq(); ok {
				cseq.SeqNo++
			}

			if c.debug {
				fmt.Printf("[SIP] Outgoing auth request (%s):\n%s\n", newReq.Method(), newReq.String())
			}

			tx2, err := c.ua.Request(newReq)
			if err != nil {
				return err
			}

			// Wait for final response to the auth request
			for {
				select {
				case res2 := <-tx2.Responses():
					if c.debug {
						fmt.Printf("[SIP] Incoming auth response to %s: %d %s\n%s\n", newReq.Method(), res2.StatusCode(), res2.Reason(), res2.String())
					}
					if res2.StatusCode() >= 200 {
						response = res2
						goto finalized
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		} else {
			response = res
		}

	case <-ctx.Done():
		return ctx.Err()
	}

finalized:
	if response.StatusCode() == 200 {
		c.updateNAT(response)
		return nil
	}
	return fmt.Errorf("request failed: %d %s", response.StatusCode(), response.Reason())
}

func (c *Client) createBaseRequest(method sip.RequestMethod, fromUri, toUri, recipientUri sip.Uri) sip.Request {
	callIDStr := fmt.Sprintf("%d@%s", time.Now().UnixNano(), getLocalIP())
	if c.publicIP != "" {
		callIDStr = fmt.Sprintf("%d@%s", time.Now().UnixNano(), c.publicIP)
	}
	callID := sip.CallID(callIDStr)

	uaHeader := sip.UserAgentHeader("Asterisk PBX")
	maxForwards := sip.MaxForwards(70)

	req := sip.NewRequest(
		"",
		method,
		recipientUri,
		"SIP/2.0",
		[]sip.Header{
			(&sip.Address{Uri: fromUri}).AsFromHeader(),
			(&sip.Address{Uri: toUri}).AsToHeader(),
			&sip.ContactHeader{Address: c.getContact()},
			&callID,
			&sip.CSeq{SeqNo: 1, MethodName: method},
			&uaHeader,
			&maxForwards,
		},
		"",
		nil,
	)

	req.SetDestination(fmt.Sprintf("%s:%d", c.outboundProxy, c.port))

	if via, ok := req.Via(); ok && len(via) > 0 {
		via[0].Params.Add("rport", nil)
		via[0].Host = getLocalIP()
		if c.publicIP != "" {
			via[0].Host = c.publicIP
		}
	}

	return req
}

func (c *Client) updateNAT(res sip.Response) {
	if via, ok := res.Via(); ok && len(via) > 0 {
		c.mu.Lock()
		defer c.mu.Unlock()
		if rec, ok := via[0].Params.Get("received"); ok {
			c.publicIP = rec.String()
		}
		if rp, ok := via[0].Params.Get("rport"); ok {
			fmt.Sscanf(rp.String(), "%d", &c.publicPort)
		}
	}
}

func (c *Client) buildAuthHeader(res sip.Response, method, uri string) sip.Header {
	var challenges []sip.Header
	challenges = append(challenges, res.GetHeaders("WWW-Authenticate")...)
	challenges = append(challenges, res.GetHeaders("Proxy-Authenticate")...)

	if len(challenges) == 0 {
		return nil
	}

	challenge := challenges[0].Value()
	parts := parseChallenge(challenge)

	realm := parts["realm"]
	nonce := parts["nonce"]
	opaque, hasOpaque := parts["opaque"]
	qopList := parts["qop"]

	// Digest state management for RFC 2617/7616 compliance
	// Megafon SBC detects duplicate nc values as replay attacks.
	c.authMu.Lock()
	if c.lastNonce != nonce {
		// Server rotated nonce, reset the monotonic counter
		c.lastNonce = nonce
		c.ncCounter = 0
	}
	c.ncCounter++
	nc := fmt.Sprintf("%08x", c.ncCounter)
	c.authMu.Unlock()

	// Unique cnonce per request to satisfy security requirements and Multifon's strict parser
	cnonce := fmt.Sprintf("%016x", time.Now().UnixNano())

	// Multifon typically expects the 11-digit number as the username.
	digestUser := c.user
	if idx := strings.Index(digestUser, "@"); idx != -1 {
		digestUser = digestUser[:idx]
	}

	ha1 := md5Hash(fmt.Sprintf("%s:%s:%s", digestUser, realm, c.password))
	ha2 := md5Hash(fmt.Sprintf("%s:%s", method, uri))

	var authRes string
	var contents string

	useQop := false
	if qopList != "" {
		for _, v := range strings.Split(qopList, ",") {
			if strings.TrimSpace(v) == "auth" {
				useQop = true
				break
			}
		}
	}

	if useQop {
		qop := "auth"
		// Response = MD5(HA1:nonce:nc:cnonce:qop:HA2)
		authRes = md5Hash(fmt.Sprintf("%s:%s:%s:%s:%s:%s", ha1, nonce, nc, cnonce, qop, ha2))
		contents = fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", qop=%s, nc=%s, cnonce="%s"`, digestUser, realm, nonce, uri, authRes, qop, nc, cnonce)
	} else {
		// Fallback for non-qop path: Response = MD5(HA1:nonce:HA2)
		authRes = md5Hash(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
		contents = fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, digestUser, realm, nonce, uri, authRes)
	}

	if hasOpaque {
		contents = fmt.Sprintf(`%s, opaque="%s"`, contents, opaque)
	}

	return &sip.GenericHeader{
		HeaderName: "Authorization",
		Contents:   contents,
	}
}

func (c *Client) getContact() sip.Uri {
	c.mu.RLock()
	defer c.mu.RUnlock()
	host := getLocalIP()
	if c.publicIP != "" {
		host = c.publicIP
	}
	portVal := sip.Port(5060)
	if c.publicPort != 0 {
		portVal = sip.Port(c.publicPort)
	}

	uri := &sip.SipUri{}
	uri.SetUser(sip.String{Str: c.user})
	uri.SetHost(host)
	uri.SetPort(&portVal)
	return uri
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

func md5Hash(text string) string {
	h := md5.New()
	io.WriteString(h, text)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func parseChallenge(challenge string) map[string]string {
	result := make(map[string]string)

	idx := strings.Index(challenge, "Digest ")
	if idx != -1 {
		challenge = challenge[idx+7:]
	}

	parts := strings.Split(challenge, ",")
	for _, part := range parts {
		subparts := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(subparts) == 2 {
			key := subparts[0]
			val := strings.Trim(subparts[1], `"`)
			result[key] = val
		}
	}
	return result
}
