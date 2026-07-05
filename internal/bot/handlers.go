package bot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/khaliullov/barrier-bot/internal/config"
)

func (b *Bot) handleCallback(query *tgbotapi.CallbackQuery) {
	data := query.Data
	userID := query.From.ID
	chatID := query.Message.Chat.ID
	messageID := query.Message.MessageID

	b.api.Send(tgbotapi.NewCallback(query.ID, ""))

	// Очищаем состояние сессии при навигации через кнопки, если это не кнопки ввода данных (срок или подтверждение)
	if !strings.HasPrefix(data, "exp_") && !strings.HasPrefix(data, "confirm_") {
		b.sessions.Delete(userID)
	}

	switch {
	// --- НАВИГАЦИЯ ---
	case data == "main_menu":
		b.sendMainMenu(chatID, userID, messageID)

	case data == "barrier_list":
		b.showBarrierList(chatID, userID, messageID)

	case strings.HasPrefix(data, "ctrl_"):
		phone := strings.TrimPrefix(data, "ctrl_")
		b.showBarrierControl(chatID, userID, messageID, phone)

	case data == "guest_access_menu":
		b.startGuestAccess(chatID, userID, messageID)

	case data == "admin_menu":
		b.sendAdminMenu(chatID, userID, messageID)

	case data == "manage_users":
		b.showBarrierSelection(chatID, userID, messageID, "users")

	case data == "manage_admins":
		b.showBarrierSelection(chatID, userID, messageID, "admins")

	case data == "manage_barriers":
		b.showManageBarriers(chatID, userID, messageID)

	// --- УПРАВЛЕНИЕ ШЛАГБАУМОМ ---
	case strings.HasPrefix(data, "open_"):
		phone := strings.TrimPrefix(data, "open_")
		b.handleOpenBarrier(chatID, messageID, userID, query.From.UserName, phone)

	// --- АДМИН-ПАНЕЛЬ ---
	case strings.HasPrefix(data, "sel_users_"):
		barrierID := strings.TrimPrefix(data, "sel_users_")
		b.showUserManagement(chatID, userID, messageID, barrierID)

	case strings.HasPrefix(data, "sel_admins_"):
		barrierID := strings.TrimPrefix(data, "sel_admins_")
		b.showAdminManagement(chatID, userID, messageID, barrierID)

	case strings.HasPrefix(data, "add_user_"):
		barrierID := strings.TrimPrefix(data, "add_user_")
		// RBAC Guard
		if !b.store.IsBarrierAdmin(userID, barrierID) {
			b.showError(chatID, messageID, "❌ Ошибка доступа: требуются права администратора шлагбаума.")
			return
		}
		b.sessions.Store(userID, Session{State: StateWaitingUserID, BarrierID: barrierID, LastMenuID: messageID})
		b.editMessageWithBack(chatID, messageID, "👤 Введите Telegram username (с @) или user_id пользователя:", "sel_users_"+barrierID)

	case strings.HasPrefix(data, "add_admin_"):
		barrierID := strings.TrimPrefix(data, "add_admin_")
		// RBAC Guard
		if !b.store.IsBarrierAdmin(userID, barrierID) {
			b.showError(chatID, messageID, "❌ Ошибка доступа: требуются права администратора шлагбаума.")
			return
		}
		b.sessions.Store(userID, Session{State: StateWaitingAdminID, BarrierID: barrierID, LastMenuID: messageID})
		b.editMessageWithBack(chatID, messageID, "👮 Введите Telegram username или user_id для нового администратора:", "sel_admins_"+barrierID)

	case strings.HasPrefix(data, "sel_guest_"):
		barrierID := strings.TrimPrefix(data, "sel_guest_")
		// Проверка прав на выдачу гостевого доступа
		if !b.store.CanGrantGuestAccess(userID, barrierID) {
			b.showError(chatID, messageID, "❌ Вы не можете выдавать гостевой доступ к этому шлагбауму.")
			return
		}
		b.sessions.Store(userID, Session{State: StateWaitingGuestID, BarrierID: barrierID, LastMenuID: messageID})
		b.editMessageWithBack(chatID, messageID, "👤 Введите Telegram username (с @) или user_id гостя:", "guest_access_menu")

	case strings.HasPrefix(data, "exp_"):
		parts := strings.Split(data, "_")
		if len(parts) == 2 {
			durationStr := parts[1]
			b.handleExpirationSelection(chatID, userID, messageID, durationStr)
		}

	case strings.HasPrefix(data, "rem_user_"):
		parts := strings.Split(data, "_")
		if len(parts) == 4 {
			barrierID := parts[2]
			targetID, _ := strconv.ParseInt(parts[3], 10, 64)
			b.confirmRemoveUser(chatID, userID, messageID, barrierID, targetID)
		}

	case strings.HasPrefix(data, "confirm_rem_user_"):
		parts := strings.Split(data, "_")
		if len(parts) == 5 {
			barrierID := parts[3]
			targetID, _ := strconv.ParseInt(parts[4], 10, 64)
			b.executeRemoveUser(chatID, userID, messageID, barrierID, targetID)
		}

	case strings.HasPrefix(data, "rem_admin_"):
		parts := strings.Split(data, "_")
		if len(parts) == 4 {
			barrierID := parts[2]
			targetID, _ := strconv.ParseInt(parts[3], 10, 64)
			b.confirmRemoveAdmin(chatID, userID, messageID, barrierID, targetID)
		}

	case strings.HasPrefix(data, "confirm_rem_admin_"):
		parts := strings.Split(data, "_")
		if len(parts) == 5 {
			barrierID := parts[3]
			targetID, _ := strconv.ParseInt(parts[4], 10, 64)
			b.executeRemoveAdmin(chatID, userID, messageID, barrierID, targetID)
		}

	case data == "add_barrier_btn":
		if !b.store.IsSuperAdmin(userID) {
			b.showError(chatID, messageID, "❌ Только супер-администратор может добавлять шлагбаумы.")
			return
		}
		b.sessions.Store(userID, Session{State: StateWaitingBarrierName, LastMenuID: messageID})
		b.editMessageWithBack(chatID, messageID, "🚧 Введите название шлагбаума:", "manage_barriers")

	case data == "view_audit_log":
		b.showAuditLogs(chatID, messageID)
	}
}

func (b *Bot) handleOpenBarrier(chatID int64, messageID int, userID int64, username string, phone string) {
	if !b.store.CanOpen(userID, phone) {
		b.showError(chatID, messageID, "❌ Доступ запрещен или срок действия истек.")
		return
	}

	mRaw, _ := b.openingMu.LoadOrStore(phone, &sync.Mutex{})
	mu := mRaw.(*sync.Mutex)
	if !mu.TryLock() {
		b.editMessage(chatID, messageID, "⏳ Шлагбаум уже открывается другим пользователем...")
		return
	}
	defer mu.Unlock()

	if lastOpen, ok := b.cooldowns.Load(phone); ok {
		if time.Since(lastOpen.(time.Time)) < 5*time.Second {
			remaining := 5 - int(time.Since(lastOpen.(time.Time)).Seconds())
			b.editMessage(chatID, messageID, fmt.Sprintf("⏳ Подождите %d сек...", remaining))
			return
		}
	}
	b.cooldowns.Store(phone, time.Now())

	b.barrierStatuses.Store(phone, config.StatusOpening)
	b.showBarrierControl(chatID, userID, messageID, phone)

	// Structured logging for journald visibility
	log.Printf("BARRIER_OPEN_REQUEST user_id=%d username=%s barrier=%s", userID, username, phone)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err := b.sipClient.OpenBarrier(ctx, phone)

	if err != nil {
		log.Printf("BARRIER_OPEN_ERROR user_id=%d username=%s barrier=%s error=%q", userID, username, phone, err.Error())
		b.barrierStatuses.Store(phone, config.StatusError)
		b.barrierLogs.Store(phone, fmt.Sprintf("❌ Ошибка: %v", err))

		b.store.AddLog(phone, config.LogEntry{
			UserID:    userID,
			Username:  username,
			Timestamp: time.Now(),
			Status:    fmt.Sprintf("Error: %v", err),
		})
	} else {
		log.Printf("BARRIER_OPEN_SUCCESS user_id=%d username=%s barrier=%s", userID, username, phone)
		b.barrierStatuses.Store(phone, config.StatusOpened)
		b.barrierLogs.Store(phone, fmt.Sprintf("✅ Последний раз открыто %s в %s", username, time.Now().Format("02.01 15:04")))

		b.store.AddLog(phone, config.LogEntry{
			UserID:    userID,
			Username:  username,
			Timestamp: time.Now(),
			Status:    "Opened",
		})
	}

	b.showBarrierControl(chatID, userID, messageID, phone)

	go func() {
		time.Sleep(5 * time.Second)
		b.barrierStatuses.Store(phone, config.StatusOnline)
		b.showBarrierControl(chatID, userID, messageID, phone)
	}()
}

func (b *Bot) showBarrierList(chatID int64, userID int64, messageID int) {
	barriers := b.store.GetUserBarriers(userID)
	if len(barriers) == 0 {
		b.showError(chatID, messageID, "У вас нет доступа ни к одному шлагбауму.")
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, br := range barriers {
		status, _ := b.barrierStatuses.LoadOrStore(br.Phone, config.StatusOnline)
		statusEmoji := "🟢"
		if status == config.StatusOffline {
			statusEmoji = "🔴"
		} else if status == config.StatusOpening {
			statusEmoji = "⏳"
		} else if status == config.StatusError {
			statusEmoji = "❌"
		}

		btn := tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s %s", statusEmoji, br.Name), "ctrl_"+br.Phone)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "main_menu"),
	))

	msg := tgbotapi.NewEditMessageText(chatID, messageID, "🚧 Выберите шлагбаум:")
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg.ReplyMarkup = &kb
	b.api.Send(msg)
}

func (b *Bot) showBarrierControl(chatID int64, userID int64, messageID int, phone string) {
	barriers := b.store.GetBarriers()
	var br *config.Barrier
	for _, b := range barriers {
		if b.Phone == phone {
			br = &b
			break
		}
	}

	if br == nil {
		b.showError(chatID, messageID, "Шлагбаум не найден.")
		return
	}

	status, _ := b.barrierStatuses.Load(phone)
	log, _ := b.barrierLogs.Load(phone)

	statusText := "🟢 Готов"
	if status == config.StatusOpening {
		statusText = "⏳ Открывается..."
	} else if status == config.StatusOpened {
		statusText = "✅ Открыто"
	} else if status == config.StatusError {
		statusText = "❌ Ошибка"
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("🚧 Шлагбаум: %s\n", br.Name))
	text.WriteString(fmt.Sprintf("Статус: %s\n", statusText))
	if log != nil {
		text.WriteString(fmt.Sprintf("\n%s", log))
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	if status != config.StatusOpening {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔓 Открыть", "open_"+phone),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "barrier_list"),
	))

	edit := tgbotapi.NewEditMessageText(chatID, messageID, text.String())
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit.ReplyMarkup = &kb
	b.api.Send(edit)
}

func (b *Bot) sendAdminMenu(chatID int64, userID int64, messageID int) {
	if !b.store.IsSuperAdmin(userID) {
		isAdmin := false
		for _, br := range b.store.GetBarriers() {
			if b.store.IsBarrierAdmin(userID, br.Phone) {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			b.showError(chatID, messageID, "❌ Доступ запрещен.")
			return
		}
	}

	text := "🛠 Администрирование\nВыберите действие:"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👥 Пользователи", "manage_users"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👮 Админы", "manage_admins"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📜 Лог действий", "view_audit_log"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "main_menu"),
		),
	)

	if messageID > 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
		edit.ReplyMarkup = &keyboard
		b.api.Send(edit)
	} else {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = keyboard
		b.api.Send(msg)
	}
}

func (b *Bot) showBarrierSelection(chatID int64, userID int64, messageID int, mode string) {
	barriers := b.store.GetBarriers()
	var rows [][]tgbotapi.InlineKeyboardButton

	for _, br := range barriers {
		if b.store.IsBarrierAdmin(userID, br.Phone) {
			btn := tgbotapi.NewInlineKeyboardButtonData(br.Name, "sel_"+mode+"_"+br.Phone)
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
		}
	}

	if len(rows) == 0 {
		b.showError(chatID, messageID, "Вы не управляете шлагбаумами.")
		return
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "admin_menu"),
	))

	msg := tgbotapi.NewEditMessageText(chatID, messageID, "Выберите шлагбаум:")
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg.ReplyMarkup = &kb
	b.api.Send(msg)
}

func (b *Bot) showUserManagement(chatID int64, userID int64, messageID int, barrierID string) {
	// RBAC Guard
	if !b.store.IsBarrierAdmin(userID, barrierID) {
		b.showError(chatID, messageID, "❌ У вас нет прав администратора для этого шлагбаума.")
		return
	}

	accesses := b.store.GetBarrierUsers(barrierID)
	text := fmt.Sprintf("👥 Пользователи для шлагбаума: %s", barrierID)

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, a := range accesses {
		u, ok := b.store.GetUser(a.UserID)
		name := fmt.Sprintf("ID: %d", a.UserID)
		if ok {
			name = fmt.Sprintf("%s (@%s)", u.FullName, u.Username)
		}
		expiresStr := "Бессрочно"
		if !a.ExpiresAt.IsZero() {
			if a.ExpiresAt.Before(time.Now()) {
				expiresStr = "Истек"
			} else {
				expiresStr = time.Until(a.ExpiresAt).Round(time.Minute).String()
			}
		}
		btn := tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s (%s) 🗑", name, expiresStr), fmt.Sprintf("rem_user_%s_%d", barrierID, a.UserID))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ Добавить пользователя", "add_user_"+barrierID),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "manage_users"),
	))

	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg.ReplyMarkup = &kb
	b.api.Send(msg)
}

func (b *Bot) showAdminManagement(chatID int64, userID int64, messageID int, barrierID string) {
	// RBAC Guard
	if !b.store.IsBarrierAdmin(userID, barrierID) {
		b.showError(chatID, messageID, "❌ У вас нет прав администратора для этого шлагбаума.")
		return
	}

	admins := b.store.GetBarrierAdmins(barrierID)
	text := fmt.Sprintf("👮 Администраторы для шлагбаума: %s", barrierID)

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, a := range admins {
		u, ok := b.store.GetUser(a.UserID)
		name := fmt.Sprintf("ID: %d", a.UserID)
		if ok {
			name = fmt.Sprintf("%s (@%s)", u.FullName, u.Username)
		}
		btn := tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s (%s) 🗑", name, a.Role), fmt.Sprintf("rem_admin_%s_%d", barrierID, a.UserID))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ Добавить админа", "add_admin_"+barrierID),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "manage_admins"),
	))

	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg.ReplyMarkup = &kb
	b.api.Send(msg)
}

func (b *Bot) showManageBarriers(chatID int64, userID int64, messageID int) {
	if !b.store.IsSuperAdmin(userID) {
		b.showError(chatID, messageID, "❌ Доступ запрещен.")
		return
	}

	barriers := b.store.GetBarriers()
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, br := range barriers {
		btn := tgbotapi.NewInlineKeyboardButtonData(br.Name+" ("+br.Phone+")", "ctrl_"+br.Phone)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ Добавить шлагбаум", "add_barrier_btn"),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "main_menu"),
	))

	msg := tgbotapi.NewEditMessageText(chatID, messageID, "🚧 Управление шлагбаумами:")
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg.ReplyMarkup = &kb
	b.api.Send(msg)
}

func (b *Bot) startAddBarrier(chatID int64, userID int64) {
	if !b.store.IsSuperAdmin(userID) {
		b.api.Send(tgbotapi.NewMessage(chatID, "❌ Только Super Admins могут добавлять шлагбаумы."))
		return
	}
	msg, _ := b.api.Send(tgbotapi.NewMessage(chatID, "🚧 Введите название шлагбаума:"))
	b.editMessageWithBack(chatID, msg.MessageID, "🚧 Введите название шлагбаума:", "manage_barriers")
	b.sessions.Store(userID, Session{State: StateWaitingBarrierName, LastMenuID: msg.MessageID})
}

func (b *Bot) startAddUser(chatID int64, userID int64) {
	b.showBarrierSelection(chatID, userID, 0, "users")
}

func (b *Bot) startAddAdmin(chatID int64, userID int64) {
	b.showBarrierSelection(chatID, userID, 0, "admins")
}

func (b *Bot) startRemoveUser(chatID int64, userID int64) {
	b.showBarrierSelection(chatID, userID, 0, "users")
}

func (b *Bot) startRemoveAdmin(chatID int64, userID int64) {
	b.showBarrierSelection(chatID, userID, 0, "admins")
}

func (b *Bot) startGuestAccess(chatID int64, userID int64, messageID int) {
	barriers := b.store.GetBarriers()
	var eligibleBarriers []config.Barrier
	for _, br := range barriers {
		if b.store.CanGrantGuestAccess(userID, br.Phone) {
			eligibleBarriers = append(eligibleBarriers, br)
		}
	}

	if len(eligibleBarriers) == 0 {
		b.showError(chatID, messageID, "У вас нет прав для выдачи гостевого доступа ни к одному шлагбауму.")
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, br := range eligibleBarriers {
		btn := tgbotapi.NewInlineKeyboardButtonData(br.Name, "sel_guest_"+br.Phone)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "main_menu"),
	))

	text := "Выберите шлагбаум для гостевого доступа (24ч):"
	if messageID > 0 {
		msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
		msg.ReplyMarkup = &kb
		b.api.Send(msg)
	} else {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
		b.api.Send(msg)
	}
}

func (b *Bot) handleStateInput(msg *tgbotapi.Message, sess Session) {
	userID := msg.From.ID
	text := msg.Text

	deleteMsg := tgbotapi.NewDeleteMessage(msg.Chat.ID, msg.MessageID)
	b.api.Send(deleteMsg)

	switch sess.State {
	case StateWaitingBarrierName:
		sess.TargetName = text
		sess.State = StateWaitingBarrierPhone
		b.sessions.Store(userID, sess)
		b.editMessageWithBack(msg.Chat.ID, sess.LastMenuID, "📞 Введите номер телефона шлагбаума:", "manage_barriers")

	case StateWaitingBarrierPhone:
		err := b.store.AddBarrier(text, sess.TargetName)
		if err != nil {
			b.showError(msg.Chat.ID, sess.LastMenuID, "❌ Ошибка добавления шлагбаума.")
		} else {
			b.editMessage(msg.Chat.ID, sess.LastMenuID, fmt.Sprintf("✅ Шлагбаум создан!\nНазвание: %s\nТелефон: %s", sess.TargetName, text))
			b.store.AddAdminLog(config.AdminLog{
				Timestamp: time.Now(),
				AdminID:   userID,
				Action:    "Создал шлагбаум",
				Target:    sess.TargetName,
				Barrier:   text,
			})
			go func() {
				time.Sleep(2 * time.Second)
				b.showManageBarriers(msg.Chat.ID, userID, sess.LastMenuID)
			}()
		}
		b.sessions.Delete(userID)

	case StateWaitingUserID, StateWaitingAdminID, StateWaitingGuestID:
		var targetID int64
		var targetUser string

		if strings.HasPrefix(text, "@") {
			targetUser = text[1:]
			u, ok := b.store.GetUserByUsername(targetUser)
			if ok {
				targetID = u.TelegramID
			}
		} else {
			id, err := strconv.ParseInt(text, 10, 64)
			if err == nil {
				targetID = id
				u, ok := b.store.GetUser(id)
				if ok {
					targetUser = u.Username
				}
			}
		}

		if targetID == 0 && targetUser == "" {
			var back string
			if sess.State == StateWaitingGuestID {
				back = "guest_access_menu"
			} else if sess.State == StateWaitingUserID {
				back = "sel_users_" + sess.BarrierID
			} else {
				back = "sel_admins_" + sess.BarrierID
			}
			b.editMessageWithBack(msg.Chat.ID, sess.LastMenuID, "Неверный формат. Введите @username или user_id:", back)
			return
		}

		sess.TargetID = targetID
		sess.TargetUser = targetUser

		if sess.State == StateWaitingGuestID {
			sess.State = StateWaitingGuestName
			b.editMessageWithBack(msg.Chat.ID, sess.LastMenuID, "👤 Введите ФИО гостя:", "guest_access_menu")
		} else if sess.State == StateWaitingUserID {
			sess.State = StateWaitingFullName
			b.editMessageWithBack(msg.Chat.ID, sess.LastMenuID, "👤 Введите ФИО пользователя:", "sel_users_"+sess.BarrierID)
		} else {
			sess.State = StateWaitingAdminName
			b.editMessageWithBack(msg.Chat.ID, sess.LastMenuID, "👮 Введите ФИО админа:", "sel_admins_"+sess.BarrierID)
		}
		b.sessions.Store(userID, sess)

	case StateWaitingGuestName:
		sess.TargetName = text
		b.confirmAddGuest(msg.Chat.ID, userID, sess)
		b.sessions.Delete(userID)

	case StateWaitingFullName, StateWaitingAdminName:
		sess.TargetName = text
		if sess.State == StateWaitingFullName {
			sess.State = StateWaitingExpiration
			b.showExpirationMenuSPA(msg.Chat.ID, sess.LastMenuID, sess.BarrierID)
		} else {
			b.confirmAddAdminSPA(msg.Chat.ID, userID, sess)
		}
		b.sessions.Store(userID, sess)
	}
}

func (b *Bot) confirmAddGuest(chatID int64, userID int64, sess Session) {
	b.store.UpsertUser(config.User{
		TelegramID: sess.TargetID,
		Username:   sess.TargetUser,
		FullName:   sess.TargetName,
	})

	expiresAt := time.Now().Add(24 * time.Hour)
	err := b.store.GrantAccess(userID, sess.TargetID, sess.BarrierID, expiresAt, config.AccessTypeGuest)
	if err != nil {
		b.showError(chatID, sess.LastMenuID, "❌ Ошибка добавления гостя.")
	} else {
		text := fmt.Sprintf("✅ Гость добавлен!\nИмя: %s\nTelegram: @%s\nСрок: 24 часа", sess.TargetName, sess.TargetUser)
		b.editMessage(chatID, sess.LastMenuID, text)
		b.store.AddAdminLog(config.AdminLog{
			Timestamp: time.Now(),
			AdminID:   userID,
			Action:    "Добавил гостя",
			Target:    sess.TargetName,
			Barrier:   sess.BarrierID,
			Details:   "Срок 24ч",
		})
		go func() {
			time.Sleep(2 * time.Second)
			b.sendMainMenu(chatID, userID, sess.LastMenuID)
		}()
	}
}

func (b *Bot) showExpirationMenuSPA(chatID int64, messageID int, barrierID string) {
	msg := tgbotapi.NewEditMessageText(chatID, messageID, "🗓 Выберите срок действия доступа:")
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("1 День", "exp_1d"),
			tgbotapi.NewInlineKeyboardButtonData("3 Дня", "exp_3d"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("1 Неделя", "exp_1w"),
			tgbotapi.NewInlineKeyboardButtonData("2 Недели", "exp_2w"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("1 Месяц", "exp_1m"),
			tgbotapi.NewInlineKeyboardButtonData("Бессрочно", "exp_perm"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "sel_users_"+barrierID),
		),
	)
	msg.ReplyMarkup = &keyboard
	b.api.Send(msg)
}

func (b *Bot) handleExpirationSelection(chatID int64, userID int64, messageID int, durationStr string) {
	sessRaw, ok := b.sessions.Load(userID)
	if !ok {
		return
	}
	sess := sessRaw.(Session)

	// RBAC Guard
	if !b.store.IsBarrierAdmin(userID, sess.BarrierID) {
		b.showError(chatID, messageID, "❌ Ошибка доступа: требуются права администратора шлагбаума.")
		return
	}

	var expiresAt time.Time
	switch durationStr {
	case "1d":
		expiresAt = time.Now().AddDate(0, 0, 1)
	case "3d":
		expiresAt = time.Now().AddDate(0, 0, 3)
	case "1w":
		expiresAt = time.Now().AddDate(0, 0, 7)
	case "2w":
		expiresAt = time.Now().AddDate(0, 0, 14)
	case "1m":
		expiresAt = time.Now().AddDate(0, 1, 0)
	case "perm":
		expiresAt = time.Time{}
	}

	b.store.UpsertUser(config.User{
		TelegramID: sess.TargetID,
		Username:   sess.TargetUser,
		FullName:   sess.TargetName,
	})

	err := b.store.GrantAccess(userID, sess.TargetID, sess.BarrierID, expiresAt, config.AccessTypeUser)
	if err != nil {
		b.showError(chatID, messageID, "❌ Ошибка добавления пользователя.")
	} else {
		expireText := durationStr
		if durationStr == "perm" {
			expireText = "Бессрочно"
		}
		text := fmt.Sprintf("✅ Пользователь добавлен!\nИмя: %s\nTelegram: @%s\nСрок: %s\nШлагбаум: %s", sess.TargetName, sess.TargetUser, expireText, sess.BarrierID)
		b.editMessage(chatID, messageID, text)
		b.store.AddAdminLog(config.AdminLog{
			Timestamp: time.Now(),
			AdminID:   userID,
			Action:    "Добавил пользователя",
			Target:    sess.TargetName,
			Barrier:   sess.BarrierID,
			Details:   fmt.Sprintf("До: %v", expiresAt),
		})
		go func() {
			time.Sleep(2 * time.Second)
			b.showUserManagement(chatID, userID, messageID, sess.BarrierID)
		}()
	}
	b.sessions.Delete(userID)
}

func (b *Bot) confirmAddAdminSPA(chatID int64, userID int64, sess Session) {
	// RBAC Guard
	if !b.store.IsBarrierAdmin(userID, sess.BarrierID) {
		b.showError(chatID, sess.LastMenuID, "❌ Ошибка доступа: требуются права администратора шлагбаума.")
		return
	}

	b.store.UpsertUser(config.User{
		TelegramID: sess.TargetID,
		Username:   sess.TargetUser,
		FullName:   sess.TargetName,
	})

	err := b.store.AddAdmin(userID, sess.TargetID, sess.BarrierID, config.RoleBarrierAdmin)
	if err != nil {
		b.showError(chatID, sess.LastMenuID, "❌ Ошибка добавления администратора.")
	} else {
		b.editMessage(chatID, sess.LastMenuID, fmt.Sprintf("✅ Администратор добавлен!\nИмя: %s\nTelegram: @%s\nШлагбаум: %s\nРоль: Админ шлагбаума", sess.TargetName, sess.TargetUser, sess.BarrierID))
		b.store.AddAdminLog(config.AdminLog{
			Timestamp: time.Now(),
			AdminID:   userID,
			Action:    "Добавил админа",
			Target:    sess.TargetName,
			Barrier:   sess.BarrierID,
			Details:   "Роль: Админ шлагбаума",
		})
		go func() {
			time.Sleep(2 * time.Second)
			b.showAdminManagement(chatID, userID, sess.LastMenuID, sess.BarrierID)
		}()
	}
	b.sessions.Delete(userID)
}

func (b *Bot) confirmRemoveUser(chatID int64, userID int64, messageID int, barrierID string, targetID int64) {
	// RBAC Guard
	if !b.store.IsBarrierAdmin(userID, barrierID) {
		b.showError(chatID, messageID, "❌ Ошибка доступа: требуются права администратора шлагбаума.")
		return
	}

	u, ok := b.store.GetUser(targetID)
	name := fmt.Sprintf("ID: %d", targetID)
	if ok {
		name = u.FullName
	}
	msg := tgbotapi.NewEditMessageText(chatID, messageID, fmt.Sprintf("❓ Подтвердите удаление пользователя %s?", name))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 Подтвердить удаление", fmt.Sprintf("confirm_rem_user_%s_%d", barrierID, targetID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "sel_users_"+barrierID),
		),
	)
	msg.ReplyMarkup = &keyboard
	b.api.Send(msg)
}

func (b *Bot) executeRemoveUser(chatID int64, userID int64, messageID int, barrierID string, targetID int64) {
	// RBAC Guard
	if !b.store.IsBarrierAdmin(userID, barrierID) {
		b.showError(chatID, messageID, "❌ Ошибка доступа: требуются права администратора шлагбаума.")
		return
	}

	u, _ := b.store.GetUser(targetID)
	err := b.store.RevokeAccess(targetID, barrierID)
	if err != nil {
		b.showError(chatID, messageID, "❌ Ошибка удаления пользователя.")
	} else {
		b.editMessage(chatID, messageID, "✅ Доступ пользователя отозван.")
		b.store.AddAdminLog(config.AdminLog{
			Timestamp: time.Now(),
			AdminID:   userID,
			Action:    "Удалил пользователя",
			Target:    u.FullName,
			Barrier:   barrierID,
		})
		go func() {
			time.Sleep(2 * time.Second)
			b.showUserManagement(chatID, userID, messageID, barrierID)
		}()
	}
}

func (b *Bot) confirmRemoveAdmin(chatID int64, userID int64, messageID int, barrierID string, targetID int64) {
	// RBAC Guard
	if !b.store.IsBarrierAdmin(userID, barrierID) {
		b.showError(chatID, messageID, "❌ Ошибка доступа: требуются права администратора шлагбаума.")
		return
	}

	u, ok := b.store.GetUser(targetID)
	name := fmt.Sprintf("ID: %d", targetID)
	if ok {
		name = u.FullName
	}
	msg := tgbotapi.NewEditMessageText(chatID, messageID, fmt.Sprintf("❓ Подтвердите удаление админа %s?", name))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 Подтвердить удаление", fmt.Sprintf("confirm_rem_admin_%s_%d", barrierID, targetID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "sel_admins_"+barrierID),
		),
	)
	msg.ReplyMarkup = &keyboard
	b.api.Send(msg)
}

func (b *Bot) executeRemoveAdmin(chatID int64, userID int64, messageID int, barrierID string, targetID int64) {
	// RBAC Guard
	if !b.store.IsBarrierAdmin(userID, barrierID) {
		b.showError(chatID, messageID, "❌ Ошибка доступа: требуются права администратора шлагбаума.")
		return
	}

	u, _ := b.store.GetUser(targetID)
	err := b.store.RemoveAdmin(targetID, barrierID)
	if err != nil {
		b.showError(chatID, messageID, "❌ Ошибка удаления админа.")
	} else {
		b.editMessage(chatID, messageID, "✅ Права администратора отозваны.")
		b.store.AddAdminLog(config.AdminLog{
			Timestamp: time.Now(),
			AdminID:   userID,
			Action:    "Удалил админа",
			Target:    u.FullName,
			Barrier:   barrierID,
		})
		go func() {
			time.Sleep(2 * time.Second)
			b.showAdminManagement(chatID, userID, messageID, barrierID)
		}()
	}
}

func (b *Bot) showAuditLogs(chatID int64, messageID int) {
	logs := b.store.GetAdminLogs()
	if len(logs) == 0 {
		b.showError(chatID, messageID, "Логи администратора не найдены.")
		return
	}

	text := "📜 Последние действия админов:\n\n"
	start := len(logs) - 10
	if start < 0 {
		start = 0
	}
	for i := len(logs) - 1; i >= start; i-- {
		l := logs[i]
		text += fmt.Sprintf("🕒 %s\nАдмин ID: %d\nДействие: %s\nЦель: %s\nШлагбаум: %s\n\n",
			l.Timestamp.Format("02.01 15:04"), l.AdminID, l.Action, l.Target, l.Barrier)
	}

	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "admin_menu"),
		),
	)
	edit.ReplyMarkup = &kb
	b.api.Send(edit)
}

func (b *Bot) showError(chatID int64, messageID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "main_menu"),
		),
	)
	edit.ReplyMarkup = &kb
	b.api.Send(edit)
}

func (b *Bot) editMessageWithBack(chatID int64, messageID int, text string, backData string) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", backData),
		),
	)
	if messageID == 0 {
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = kb
		b.api.Send(msg)
		return
	}
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ReplyMarkup = &kb
	b.api.Send(edit)
}

func (b *Bot) editMessage(chatID int64, messageID int, text string) {
	if messageID == 0 {
		msg := tgbotapi.NewMessage(chatID, text)
		b.api.Send(msg)
		return
	}
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	b.api.Send(edit)
}
