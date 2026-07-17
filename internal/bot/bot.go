package bot

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/khaliullov/barrier-bot/internal/config"
	"github.com/khaliullov/barrier-bot/internal/sip"
	"github.com/khaliullov/barrier-bot/internal/storage"
	"github.com/khaliullov/barrier-bot/internal/web"
)

type UserState string

const (
	StateNone                UserState = ""
	StateWaitingUserID       UserState = "WAITING_USER_ID"
	StateWaitingFullName     UserState = "WAITING_FULL_NAME"
	StateWaitingExpiration   UserState = "WAITING_EXPIRATION"
	StateWaitingAdminID      UserState = "WAITING_ADMIN_ID"
	StateWaitingAdminName    UserState = "WAITING_ADMIN_NAME"
	StateWaitingBarrierName  UserState = "WAITING_BARRIER_NAME"
	StateWaitingBarrierPhone UserState = "WAITING_BARRIER_PHONE"
	StateWaitingGuestID      UserState = "WAITING_GUEST_ID"
	StateWaitingGuestName    UserState = "WAITING_GUEST_NAME"
	StateWaitingRequestAdmin UserState = "WAITING_REQUEST_ADMIN"
)

type Session struct {
	State      UserState
	BarrierID  string
	TargetID   int64
	TargetUser string
	TargetName string
	RequestID  string
	Role       config.Role
	LastMenuID int // ID сообщения, которое редактируется
}

type Bot struct {
	api       *tgbotapi.BotAPI
	store     *storage.Store
	sipClient *sip.Client
	webServer *web.Server

	cooldowns       sync.Map // barrierPhone -> time.Time
	sessions        sync.Map // userID -> Session
	barrierStatuses sync.Map // barrierPhone -> config.BarrierStatus
	barrierLogs     sync.Map // barrierPhone -> last action details (string)
	openingMu       sync.Map // barrierPhone -> *sync.Mutex (для предотвращения одновременных открытий)
}

func NewBot(token string, store *storage.Store, sipClient *sip.Client, forceIPv6 bool, webServer *web.Server) (*Bot, error) {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if forceIPv6 {
				return dialer.DialContext(ctx, "tcp6", addr)
			}
			return dialer.DialContext(ctx, network, addr)
		},
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// Robust HTTP Client Configuration
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Minute * 2, // Slightly more than the long-polling timeout
	}

	api, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, client)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания bot api: %w", err)
	}

	b := &Bot{
		api:       api,
		store:     store,
		sipClient: sipClient,
		webServer: webServer,
	}

	// Инициализация статусов
	for _, br := range b.store.GetBarriers() {
		b.barrierStatuses.Store(br.Phone, config.StatusOnline)

		// Restore last status from logs
		logs := b.store.GetLogs(br.Phone)
		if len(logs) > 0 {
			lastEntry := logs[len(logs)-1]
			if lastEntry.Status == "Opened" {
				b.barrierLogs.Store(br.Phone, fmt.Sprintf("✅ Последний раз открыто %s в %s", lastEntry.Username, lastEntry.Timestamp.Format("02.01 15:04")))
			} else if strings.HasPrefix(lastEntry.Status, "Error: ") {
				errMsg := strings.TrimPrefix(lastEntry.Status, "Error: ")
				b.barrierLogs.Store(br.Phone, fmt.Sprintf("❌ Ошибка: %s", errMsg))
			}
		}
	}

	if webServer != nil {
		webServer.Statuses = &b.barrierStatuses
		webServer.OpenFunc = func(barrierID string, userID int64, source string) error {
			return b.OpenBarrierInternal(barrierID, userID, source)
		}
	}

	// Set SIP error handler to notify Super Admin
	if sipClient != nil {
		sipClient.OnError = func(err error) {
			cfg := b.store.GetConfig()
			if cfg.MasterAdminID != 0 {
				msg := tgbotapi.NewMessage(cfg.MasterAdminID, fmt.Sprintf("⚠️ КРИТИЧЕСКАЯ ОШИБКА SIP:\n%v", err))
				b.api.Send(msg)
			}
		}
	}

	return b, nil
}

func (b *Bot) OpenBarrierInternal(phone string, userID int64, source string) error {
	// Race condition prevention
	mRaw, _ := b.openingMu.LoadOrStore(phone, &sync.Mutex{})
	mu := mRaw.(*sync.Mutex)
	if !mu.TryLock() {
		return fmt.Errorf("шлагбаум уже открывается")
	}
	defer mu.Unlock()

	if lastOpen, ok := b.cooldowns.Load(phone); ok {
		if time.Since(lastOpen.(time.Time)) < 5*time.Second {
			return fmt.Errorf("подождите немного")
		}
	}
	b.cooldowns.Store(phone, time.Now())

	b.barrierStatuses.Store(phone, config.StatusOpening)
	if b.webServer != nil {
		b.webServer.NotifyStatusChange(phone, config.StatusOpening)
	}

	// Structured logging for journald visibility
	log.Printf("BARRIER_OPEN_REQUEST user_id=%d source=%s barrier=%s", userID, source, phone)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err := b.sipClient.OpenBarrier(ctx, phone)

	if err != nil {
		log.Printf("BARRIER_OPEN_ERROR user_id=%d source=%s barrier=%s error=%q", userID, source, phone, err.Error())

		// Notify Master Admin about specific server errors (500)
		if strings.Contains(err.Error(), "500") {
			cfg := b.store.GetConfig()
			if cfg.MasterAdminID != 0 {
				adminMsg := tgbotapi.NewMessage(cfg.MasterAdminID, fmt.Sprintf("🚨 Сбой SIP (500 Error) при открытии шлагбаума %s пользователем %s", phone, source))
				b.api.Send(adminMsg)
			}
		}

		b.barrierStatuses.Store(phone, config.StatusError)
		b.barrierLogs.Store(phone, fmt.Sprintf("❌ Ошибка: %v", err))
		if b.webServer != nil {
			b.webServer.NotifyStatusChange(phone, config.StatusError)
		}

		b.store.AddLog(phone, config.LogEntry{
			UserID:    userID,
			Username:  source,
			Timestamp: time.Now(),
			Status:    fmt.Sprintf("Error: %v", err),
		})
	} else {
		log.Printf("BARRIER_OPEN_SUCCESS user_id=%d source=%s barrier=%s", userID, source, phone)
		b.barrierStatuses.Store(phone, config.StatusOpened)
		b.barrierLogs.Store(phone, fmt.Sprintf("✅ Последний раз открыто через %s в %s", source, time.Now().Format("02.01 15:04")))
		if b.webServer != nil {
			b.webServer.NotifyStatusChange(phone, config.StatusOpened)
		}

		b.store.AddLog(phone, config.LogEntry{
			UserID:    userID,
			Username:  source,
			Timestamp: time.Now(),
			Status:    "Opened",
		})
	}

	// Return to online after 5 seconds
	go func() {
		time.Sleep(5 * time.Second)
		b.barrierStatuses.Store(phone, config.StatusOnline)
		if b.webServer != nil {
			b.webServer.NotifyStatusChange(phone, config.StatusOnline)
		}
	}()

	return err
}

func (b *Bot) Run(ctx context.Context) {
	// Регистрация глобальных команд
	b.registerGlobalCommands()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping Telegram bot...")
			return
		default:
			// Improved Update Fetching Loop
			updates := b.api.GetUpdatesChan(u)

			log.Println("Telegram update listener started")
			if err := b.processUpdates(ctx, updates); err != nil {
				log.Printf("Update processing stopped: %v. Re-initializing in 5 seconds...", err)
				select {
				case <-time.After(5 * time.Second):
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (b *Bot) processUpdates(ctx context.Context, updates tgbotapi.UpdatesChannel) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case update, ok := <-updates:
			if !ok {
				return fmt.Errorf("updates channel closed")
			}
			b.safeHandleUpdate(update)
		}
	}
}

func (b *Bot) safeHandleUpdate(update tgbotapi.Update) {
	// Panic Recovery in Main Loop
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in Telegram update handler: %v", r)
		}
	}()

	if update.Message != nil {
		b.handleMessage(update.Message)
	} else if update.CallbackQuery != nil {
		b.handleCallback(update.CallbackQuery)
	}
}

func (b *Bot) registerGlobalCommands() {
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Главное меню"},
		{Command: "help", Description: "Справка"},
	}
	if _, err := b.api.Request(tgbotapi.NewSetMyCommands(commands...)); err != nil {
		log.Printf("Failed to register global commands: %v", err)
	}
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	userID := msg.From.ID

	// Автоматически сохраняем или обновляем данные пользователя
	fullName := strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
	if fullName == "" {
		fullName = msg.From.UserName
	}
	b.store.UpsertUser(config.User{
		TelegramID: userID,
		Username:   msg.From.UserName,
		FullName:   fullName,
	})

	if msg.IsCommand() {
		// Регистрация команд для конкретного пользователя в зависимости от прав
		b.registerUserCommands(userID)

		b.sessions.Delete(userID) // Сброс состояния при новой команде
		switch msg.Command() {
		case "start":
			b.sendMainMenu(msg.Chat.ID, userID, 0)
		case "help":
			b.reply(msg, "Справка Barrier Bot:\n/start - Главное меню\n/admin - Панель управления (если есть доступ)")
		case "admin":
			b.sendAdminMenu(msg.Chat.ID, userID, 0)
		default:
			b.reply(msg, "Неизвестная команда. Используйте /start, чтобы открыть меню.")
		}
		return
	}

	// Обработка текстового ввода на основе состояния
	if sessRaw, ok := b.sessions.Load(userID); ok {
		sess := sessRaw.(Session)
		b.handleStateInput(msg, sess)
	}
}

func (b *Bot) registerUserCommands(userID int64) {
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Главное меню"},
		{Command: "help", Description: "Справка"},
	}

	isAdmin := b.store.IsSuperAdmin(userID)
	if !isAdmin {
		for _, br := range b.store.GetBarriers() {
			if b.store.IsBarrierAdmin(userID, br.Phone) {
				isAdmin = true
				break
			}
		}
	}

	if isAdmin {
		commands = append(commands, tgbotapi.BotCommand{Command: "admin", Description: "Панель администратора"})
	}

	scope := tgbotapi.NewBotCommandScopeChat(userID)
	if _, err := b.api.Request(tgbotapi.NewSetMyCommandsWithScope(scope, commands...)); err != nil {
		log.Printf("Failed to register user commands for user %d: %v", userID, err)
	}
}

func (b *Bot) getMainMenuKeyboard(userID int64) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton

	// 1. Список шлагбаумов
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🚧 Открыть шлагбаум", "barrier_list"),
	))

	// 2. Гостевой доступ
	canGrantAny := false
	for _, br := range b.store.GetBarriers() {
		if b.store.CanGrantGuestAccess(userID, br.Phone) {
			canGrantAny = true
			break
		}
	}

	if canGrantAny {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👤 Гостевой доступ", "guest_access_menu"),
		))
	}

	// 3. Личный веб-интерфейс (только для гостей)
	if b.store.IsGuestOnly(userID) {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌐 Веб-интерфейс", "show_web_link"),
		))
	}

	// 4. Админ-панель
	isAdmin := b.store.IsSuperAdmin(userID)
	if !isAdmin {
		for _, br := range b.store.GetBarriers() {
			if b.store.IsBarrierAdmin(userID, br.Phone) {
				isAdmin = true
				break
			}
		}
	}

	if isAdmin {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛠 Админ-панель", "admin_menu"),
		))
	}

	if b.store.IsSuperAdmin(userID) {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚧 Управление шлагбаумами", "manage_barriers"),
		))
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) sendMainMenu(chatID int64, userID int64, messageID int) {
	user, _ := b.store.GetUser(userID)
	name := user.FullName
	if name == "" {
		name = "Гость"
	}

	text := fmt.Sprintf("🚧 Управление шлагбаумом\nЗдравствуйте, %s", name)
	kb := b.getMainMenuKeyboard(userID)

	if messageID > 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
		edit.ReplyMarkup = &kb
		if _, err := b.api.Request(edit); err != nil {
			log.Printf("Failed to edit main menu for user %d: %v", userID, err)
		}
	} else {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = kb
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send main menu to user %d: %v", userID, err)
		}
	}
}

func (b *Bot) reply(msg *tgbotapi.Message, text string) {
	newMsg := tgbotapi.NewMessage(msg.Chat.ID, text)
	if _, err := b.api.Send(newMsg); err != nil {
		log.Printf("Failed to send reply to user %d: %v", msg.From.ID, err)
	}
}
