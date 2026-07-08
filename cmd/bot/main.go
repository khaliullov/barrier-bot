package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/khaliullov/barrier-bot/internal/bot"
	"github.com/khaliullov/barrier-bot/internal/config"
	"github.com/khaliullov/barrier-bot/internal/sip"
	"github.com/khaliullov/barrier-bot/internal/storage"
	"github.com/khaliullov/barrier-bot/internal/web"
)

func main() {
	configPath := flag.String("config", "/etc/barrier-bot/config.toml", "path to config file")
	flag.Parse()

	// 1. Load Config
	cfgManager, err := config.NewManager(*configPath)
	if err != nil {
		// Fallback to local config if default /etc path doesn't exist
		if *configPath == "/etc/barrier-bot/config.toml" {
			if _, errLocal := os.Stat("config.toml"); errLocal == nil {
				log.Println("Config not found in /etc, falling back to ./config.toml")
				cfgManager, err = config.NewManager("config.toml")
			}
		}

		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	}
	cfg := cfgManager.Config()

	// 2. Initialize SIP Client
	sipClient, err := sip.NewClient(cfg.SIP.Host, cfg.SIP.OutboundProxy, cfg.SIP.Port, cfg.SIP.User, cfg.SIP.Password, cfg.Debug)
	if err != nil {
		log.Fatalf("Failed to initialize SIP client: %v", err)
	}
	defer sipClient.Close()
	sipClient.Start()

	// Perform initial registration to detect NAT and verify credentials
	ctxReg, cancelReg := context.WithTimeout(context.Background(), 10*time.Second)
	if err := sipClient.Register(ctxReg); err != nil {
		log.Printf("Initial SIP registration failed (NAT detection might be limited): %v", err)
	} else {
		log.Println("SIP registration successful")
	}
	cancelReg()

	// 3. Initialize Storage/Store
	store := storage.NewStore(cfgManager)

	// 4. Initialize Web Server (if enabled)
	var webServer *web.Server
	if cfg.Web.Enabled {
		webServer = web.NewServer(cfg.Web, store, nil) // status sync.Map will be passed by Bot
		go func() {
			if err := webServer.Start(); err != nil {
				log.Printf("Web server error: %v", err)
			}
		}()
	}

	// 5. Initialize Bot
	telegramBot, err := bot.NewBot(cfg.TelegramToken, store, sipClient, cfg.ForceIPv6, webServer)
	if err != nil {
		log.Fatalf("Failed to initialize Telegram bot: %v", err)
	}

	// 6. Run with Graceful Shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("Barrier Bot is starting...")
	go telegramBot.Run(ctx)

	// Wait for termination
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down...")
	cancel() // Signal context cancellation

	// Give some time for components to stop cleanly
	time.Sleep(time.Second)
	log.Println("Process finished.")
}
