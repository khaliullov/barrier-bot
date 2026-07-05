package bot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/khaliullov/barrier-bot/internal/config"
	"github.com/khaliullov/barrier-bot/internal/sip"
	"github.com/khaliullov/barrier-bot/internal/storage"
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
)

type Session struct {
	State      UserState
	BarrierID  string
	TargetID   int64
	TargetUser string
	TargetName string
	Role       config.Role
	LastMenuID int // ID сообщения, которое редактируется
}

type Bot struct {
	api       *tgbotapi.BotAPI
	store     *storage.Store
	sipClient *sip.Client

	cooldowns       sync.Map // barrierPhone -> time.Time
	sessions        sync.Map // userID -> Session
	barrierStatuses sync.Map // barrierPhone -> config.BarrierStatus
	barrierLogs     sync.Map // barrierPhone -> last action details (string)
	openingMu       sync.Map // barrierPhone -> *sync.Mutex (для предотвращения одновременных открытий)
}

func NewBot(token string, store *storage.Store, sipClient *sip.Client, forceIPv6 bool) (*Bot, error) {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
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

	client := &http.Client{
		Transport: transport,
	}

	api, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, client)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания bot api: %w", err)
	}

	b := &Bot{
		api:       api,
		store:     store,
		sipClient: sipClient,
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

	return b, nil
}

func (b *Bot) Run() {
	// Регистрация глобальных команд
	b.registerGlobalCommands()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			b.handleMessage(update.Message)
		} else if update.CallbackQuery != nil {
			b.handleCallback(update.CallbackQuery)
		}
	}
}

func (b *Bot) registerGlobalCommands() {
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Главное меню"},
		{Command: "help", Description: "Справка"},
	}
	b.api.Send(tgbotapi.NewSetMyCommands(commands...))
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
	b.api.Send(tgbotapi.NewSetMyCommandsWithScope(scope, commands...))
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

	// 3. Админ-панель
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
		b.api.Send(edit)
	} else {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = kb
		b.api.Send(msg)
	}
}

func (b *Bot) reply(msg *tgbotapi.Message, text string) {
	newMsg := tgbotapi.NewMessage(msg.Chat.ID, text)
	b.api.Send(newMsg)
}
