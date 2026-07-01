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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := sipClient.Register(ctx); err != nil {
		log.Printf("Initial SIP registration failed (NAT detection might be limited): %v", err)
	} else {
		log.Println("SIP registration successful")
	}
	cancel()

	// 3. Initialize Storage/Store
	store := storage.NewStore(cfgManager)

	// 4. Initialize Bot
	telegramBot, err := bot.NewBot(cfg.TelegramToken, store, sipClient, cfg.ForceIPv6)
	if err != nil {
		log.Fatalf("Failed to initialize Telegram bot: %v", err)
	}

	// 5. Run
	log.Println("Barrier Bot is starting...")
	go telegramBot.Run()

	// Wait for termination
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down...")
}
