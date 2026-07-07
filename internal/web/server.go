package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/khaliullov/barrier-bot/internal/config"
	"github.com/khaliullov/barrier-bot/internal/storage"
)

type StatusUpdate struct {
	BarrierID    string `json:"barrier_id"`
	Status       string `json:"status"`
	LastOpenTime string `json:"last_open_time"`
}

type Server struct {
	config   config.WebConfig
	store    *storage.Store
	clients  map[*websocket.Conn]bool
	mu       sync.Mutex
	upgrader websocket.Upgrader

	// Callback to trigger barrier opening from bot package
	OpenFunc func(barrierID string, userID int64, source string) error

	// Access to current statuses (shared with Bot)
	Statuses *sync.Map // barrierPhone -> config.BarrierStatus
}

func NewServer(cfg config.WebConfig, store *storage.Store, statuses *sync.Map) *Server {
	return &Server{
		config:  cfg,
		store:   store,
		clients: make(map[*websocket.Conn]bool),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		Statuses: statuses,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	prefix := s.config.Prefix
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	mux.HandleFunc(prefix, s.handleIndex)
	mux.HandleFunc(prefix+"index.html", s.handleIndex)
	mux.HandleFunc(prefix+"ws.php", s.handleWS)
	mux.HandleFunc(prefix+"api/open/", s.handleOpenAPI)

	addr := fmt.Sprintf(":%d", s.config.Port)
	fmt.Printf("Web server starting on %s with prefix %s\n", addr, prefix)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	prefix := s.config.Prefix
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// Allow both the prefix root and index.html
	if r.URL.Path != prefix && r.URL.Path != prefix+"index.html" {
		http.NotFound(w, r)
		return
	}

	token := r.URL.Query().Get("token")
	user, ok := s.store.GetUserByToken(token)
	var barriers []config.Barrier
	var name string

	if ok {
		// User with token exists
		if !s.store.IsGuestOnly(user.TelegramID) {
			http.Error(w, "Веб-интерфейс доступен только гостям.", http.StatusForbidden)
			return
		}
		barriers = s.store.GetUserBarriers(user.TelegramID)
		name = user.FullName
	} else {
		// Try anonymous access
		anon, ok := s.store.GetAnonymousAccess(token)
		if !ok {
			http.Error(w, "У вас нет доступа к этой странице. Получите ссылку у бота.", http.StatusUnauthorized)
			return
		}
		// Show only the specific barrier
		allBarriers := s.store.GetBarriers()
		for _, b := range allBarriers {
			if b.Phone == anon.BarrierID {
				barriers = append(barriers, b)
				break
			}
		}
		name = "Анонимный гость"
	}

	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <title>Barrier Guest Access</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body { font-family: sans-serif; padding: 20px; background: #f0f2f5; }
        .barrier { background: white; padding: 15px; margin-bottom: 10px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .status { font-weight: bold; }
        button { padding: 10px 20px; font-size: 16px; cursor: pointer; background: #007bff; color: white; border: none; border-radius: 4px; }
        button:disabled { background: #ccc; }
        .StatusONLINE { color: green; }
        .StatusOPENING { color: orange; }
        .StatusERROR { color: red; }
        .StatusOPENED { color: blue; }
        .user-info { margin-bottom: 20px; font-size: 14px; color: #666; }
    </style>
</head>
<body>
    <h1>Управление шлагбаумами</h1>
    <div class="user-info">Пользователь: {{.UserName}}</div>
    <div id="barriers">
        {{range .Barriers}}
        <div class="barrier" id="barrier-{{.Phone}}">
            <h3>{{.Name}}</h3>
            <p>Статус: <span class="status" id="status-{{.Phone}}">Загрузка...</span></p>
            <p>Последнее открытие: <span id="time-{{.Phone}}">...</span></p>
            <button onclick="openBarrier('{{.Phone}}')" id="btn-{{.Phone}}">Открыть</button>
        </div>
        {{end}}
    </div>

    <script>
        const prefix = "{{.Prefix}}";
        const token = "{{.Token}}";

        function updateUI(data) {
            const statusEl = document.getElementById("status-" + data.barrier_id);
            const timeEl = document.getElementById("time-" + data.barrier_id);
            const btn = document.getElementById("btn-" + data.barrier_id);

            if (statusEl) {
                statusEl.innerText = data.status;
                statusEl.className = "status Status" + data.status;
            }
            if (timeEl && data.last_open_time) timeEl.innerText = data.last_open_time;
            if (btn) btn.disabled = (data.status === "OPENING");
        }

        function openBarrier(id) {
            const btn = document.getElementById("btn-" + id);
            if (btn) btn.disabled = true;

            fetch(prefix + "/api/open/" + id + ".php?token=" + token, { method: "POST" })
                .then(r => r.json())
                .then(data => {
                    if (!data.success) {
                        alert("Ошибка: " + data.error);
                        if (btn) btn.disabled = false;
                    }
                })
                .catch(err => {
                    alert("Network error");
                    if (btn) btn.disabled = false;
                });
        }

        function connectWS() {
            const protocol = window.location.protocol === "https:" ? "wss://" : "ws://";
            const ws = new WebSocket(protocol + window.location.host + prefix + "/ws.php?token=" + token);
            ws.onmessage = (event) => {
                const data = JSON.parse(event.data);
                updateUI(data);
            };
            ws.onclose = () => {
                setTimeout(connectWS, 2000);
            };
        }
        connectWS();
    </script>
</body>
</html>`

	// Filter barriers based on user permissions
	data := struct {
		Barriers []config.Barrier
		Prefix   string
		Token    string
		UserName string
	}{
		Barriers: barriers,
		Prefix:   strings.TrimSuffix(prefix, "/"),
		Token:    token,
		UserName: name,
	}

	t := template.Must(template.New("index").Parse(tmpl))
	t.Execute(w, data)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	user, userOk := s.store.GetUserByToken(token)
	anon, anonOk := s.store.GetAnonymousAccess(token)

	if !userOk && !anonOk {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var barriers []config.Barrier
	if userOk {
		barriers = s.store.GetUserBarriers(user.TelegramID)
	} else {
		allBarriers := s.store.GetBarriers()
		for _, b := range allBarriers {
			if b.Phone == anon.BarrierID {
				barriers = append(barriers, b)
				break
			}
		}
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()

	// Send initial states for barriers this client can see
	for _, b := range barriers {
		status, _ := s.Statuses.Load(b.Phone)
		lastTime := s.store.GetLastOpenTime(b.Phone)
		update := StatusUpdate{
			BarrierID:    b.Phone,
			Status:       fmt.Sprintf("%v", status),
			LastOpenTime: lastTime.Format("02.01 15:04"),
		}
		if lastTime.IsZero() {
			update.LastOpenTime = "Никогда"
		}
		conn.WriteJSON(update)
	}

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.clients, conn)
			s.mu.Unlock()
			conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("X-Guest-Token")
	}

	user, userOk := s.store.GetUserByToken(token)
	anon, anonOk := s.store.GetAnonymousAccess(token)

	if !userOk && !anonOk {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	prefix := s.config.Prefix
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	apiPrefix := prefix + "api/open/"
	barrierID := strings.TrimPrefix(r.URL.Path, apiPrefix)

	if barrierID == "" {
		http.Error(w, "Barrier ID required", http.StatusBadRequest)
		return
	}

	barrierID = strings.TrimSuffix(barrierID, ".php")

	var openerID int64
	var openerName string

	if userOk {
		if !s.store.CanOpen(user.TelegramID, barrierID) {
			http.Error(w, "Permission denied for this barrier", http.StatusForbidden)
			return
		}
		openerID = user.TelegramID
		openerName = user.FullName + " (Web)"
	} else {
		if anon.BarrierID != barrierID {
			http.Error(w, "Permission denied for this barrier", http.StatusForbidden)
			return
		}
		openerID = anon.CreatedBy // Attribution to the admin who created the link
		openerName = "Анонимный гость"
	}

	err := s.OpenFunc(barrierID, openerID, openerName)

	resp := struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}{
		Success: err == nil,
	}
	if err != nil {
		resp.Error = err.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) NotifyStatusChange(barrierID string, status config.BarrierStatus) {
	lastTime := s.store.GetLastOpenTime(barrierID)
	update := StatusUpdate{
		BarrierID:    barrierID,
		Status:       string(status),
		LastOpenTime: lastTime.Format("02.01 15:04"),
	}
	if lastTime.IsZero() {
		update.LastOpenTime = "Никогда"
	}

	msg, _ := json.Marshal(update)

	s.mu.Lock()
	defer s.mu.Unlock()
	for client := range s.clients {
		err := client.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			client.Close()
			delete(s.clients, client)
		}
	}
}
