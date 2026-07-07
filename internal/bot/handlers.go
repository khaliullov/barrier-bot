package bot

import (
	"fmt"
	"strconv"
	"strings"
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

	case data == "request_access_list":
		b.showRequestBarrierList(chatID, userID, messageID)

	case strings.HasPrefix(data, "req_bar_"):
		barrierID := strings.TrimPrefix(data, "req_bar_")
		b.showRequestAdminList(chatID, userID, messageID, barrierID)

	case strings.HasPrefix(data, "req_send_"):
		parts := strings.Split(data, "_")
		if len(parts) == 4 {
			barrierID := parts[2]
			adminID, _ := strconv.ParseInt(parts[3], 10, 64)
			b.handleSendAccessRequest(chatID, userID, messageID, barrierID, adminID)
		}

	case strings.HasPrefix(data, "appr_guest_"):
		requestID := strings.TrimPrefix(data, "appr_guest_")
		b.handleApproveRequest(chatID, userID, messageID, requestID, config.AccessTypeGuest)

	case strings.HasPrefix(data, "appr_user_"):
		requestID := strings.TrimPrefix(data, "appr_user_")
		b.handleApproveUserRequest(chatID, userID, messageID, requestID)

	case strings.HasPrefix(data, "rej_req_"):
		requestID := strings.TrimPrefix(data, "rej_req_")
		b.handleRejectRequest(chatID, userID, messageID, requestID)

	case strings.HasPrefix(data, "gen_web_link_"):
		barrierID := strings.TrimPrefix(data, "gen_web_link_")
		b.handleGenerateAnonymousLink(chatID, userID, messageID, barrierID)

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

	case data == "show_web_link":
		b.showWebLink(chatID, userID, messageID)

	case data == "gen_anon_link":
		b.showBarrierSelection(chatID, userID, messageID, "anon")

	case strings.HasPrefix(data, "sel_anon_"):
		barrierID := strings.TrimPrefix(data, "sel_anon_")
		b.handleGenerateAnonymousLink(chatID, userID, messageID, barrierID)

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

	err := b.OpenBarrierInternal(phone, userID, username)
	if err != nil {
		// If error is from TryLock or cooldown, we might want to show it.
		// OpenBarrierInternal currently returns errors for those.
		if err.Error() == "шлагбаум уже открывается" {
			b.editMessage(chatID, messageID, "⏳ Шлагбаум уже открывается другим пользователем...")
			return
		}
		if err.Error() == "подождите немного" {
			// Calculate remaining time? For now keep it simple.
			b.editMessage(chatID, messageID, "⏳ Подождите немного...")
			return
		}
	}

	// Update Telegram UI based on current status (which was updated by OpenBarrierInternal)
	b.showBarrierControl(chatID, userID, messageID, phone)

	// Since OpenBarrierInternal is synchronous for the SIP call, we can update UI after it returns.
	// But it also has a goroutine to return to Online status.
	// We might need to listen to status changes to update TG UI in real-time if we want it perfect,
	// but for now, updating after the call and then once more (maybe via a callback) is fine.
}

func (b *Bot) showBarrierList(chatID int64, userID int64, messageID int) {
	barriers := b.store.GetUserBarriers(userID)

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
		tgbotapi.NewInlineKeyboardButtonData("❓ Запросить доступ", "request_access_list"),
	))

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "main_menu"),
	))

	msg := tgbotapi.NewEditMessageText(chatID, messageID, "🚧 Выберите шлагбаум:")
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg.ReplyMarkup = &kb
	b.api.Send(msg)
}

func (b *Bot) showRequestBarrierList(chatID int64, userID int64, messageID int) {
	barriers := b.store.GetBarriers()
	var rows [][]tgbotapi.InlineKeyboardButton

	// Show barriers user DOES NOT have access to
	hasAccess := make(map[string]bool)
	for _, b := range b.store.GetUserBarriers(userID) {
		hasAccess[b.Phone] = true
	}

	for _, br := range barriers {
		if !hasAccess[br.Phone] {
			btn := tgbotapi.NewInlineKeyboardButtonData(br.Name, "req_bar_"+br.Phone)
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
		}
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "barrier_list"),
	))

	edit := tgbotapi.NewEditMessageText(chatID, messageID, "❓ Выберите шлагбаум для запроса доступа:")
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit.ReplyMarkup = &kb
	b.api.Send(edit)
}

func (b *Bot) showRequestAdminList(chatID int64, userID int64, messageID int, barrierID string) {
	admins := b.store.GetBarrierAdmins(barrierID)
	var rows [][]tgbotapi.InlineKeyboardButton

	for _, a := range admins {
		u, ok := b.store.GetUser(a.UserID)
		name := fmt.Sprintf("ID: %d", a.UserID)
		if ok {
			name = u.FullName
		}
		btn := tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("👮 %s", name), fmt.Sprintf("req_send_%s_%d", barrierID, a.UserID))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "request_access_list"),
	))

	edit := tgbotapi.NewEditMessageText(chatID, messageID, "Выберите администратора для отправки запроса:")
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit.ReplyMarkup = &kb
	b.api.Send(edit)
}

func (b *Bot) handleSendAccessRequest(chatID int64, userID int64, messageID int, barrierID string, adminID int64) {
	user, _ := b.store.GetUser(userID)
	request := config.AccessRequest{
		UserID:    userID,
		BarrierID: barrierID,
		AdminID:   adminID,
		Status:    "PENDING",
	}

	err := b.store.AddAccessRequest(&request)
	if err != nil {
		b.showError(chatID, messageID, "❌ Ошибка отправки запроса.")
		return
	}

	// Notify Admin
	barrierName := barrierID
	for _, br := range b.store.GetBarriers() {
		if br.Phone == barrierID {
			barrierName = br.Name
			break
		}
	}

	adminMsg := tgbotapi.NewMessage(adminID, fmt.Sprintf("🔔 Новый запрос доступа!\nПользователь: %s (@%s)\nШлагбаум: %s", user.FullName, user.Username, barrierName))
	adminMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Гость (24ч)", "appr_guest_"+request.ID),
			tgbotapi.NewInlineKeyboardButtonData("✅ Пользователь", "appr_user_"+request.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отклонить", "rej_req_"+request.ID),
		),
	)
	b.api.Send(adminMsg)

	b.editMessageWithBack(chatID, messageID, "✅ Запрос успешно отправлен администратору.", "barrier_list")
}

func (b *Bot) handleApproveUserRequest(chatID int64, userID int64, messageID int, requestID string) {
	req, ok := b.store.GetAccessRequest(requestID)
	if !ok {
		b.showError(chatID, messageID, "Запрос не найден.")
		return
	}

	// Fetch user info to keep session complete
	user, _ := b.store.GetUser(req.UserID)

	// Set session to handle duration selection
	b.sessions.Store(userID, Session{
		State:      StateWaitingExpiration,
		BarrierID:  req.BarrierID,
		TargetID:   req.UserID,
		TargetUser: user.Username,
		TargetName: user.FullName,
		RequestID:  requestID,
		LastMenuID: messageID,
	})

	b.showExpirationMenuSPA(chatID, messageID, req.BarrierID)
}

func (b *Bot) handleApproveRequest(chatID int64, userID int64, messageID int, requestID string, accType config.AccessType) {
	req, ok := b.store.GetAccessRequest(requestID)
	if !ok {
		b.showError(chatID, messageID, "Запрос не найден.")
		return
	}

	if !b.store.IsBarrierAdmin(userID, req.BarrierID) {
		b.showError(chatID, messageID, "❌ Нет прав.")
		return
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	err := b.store.GrantAccess(userID, req.UserID, req.BarrierID, expiresAt, accType)
	if err != nil {
		b.showError(chatID, messageID, "❌ Ошибка при одобрении.")
		return
	}

	b.store.UpdateAccessRequestStatus(requestID, "APPROVED")
	b.editMessage(chatID, messageID, "✅ Запрос одобрен (Гость 24ч).")

	// Notify User
	b.api.Send(tgbotapi.NewMessage(req.UserID, "✅ Ваш запрос доступа одобрен (на 24 часа)!"))
}

func (b *Bot) handleRejectRequest(chatID int64, userID int64, messageID int, requestID string) {
	req, ok := b.store.GetAccessRequest(requestID)
	if !ok {
		return
	}
	b.store.UpdateAccessRequestStatus(requestID, "REJECTED")
	b.editMessage(chatID, messageID, "❌ Запрос отклонен.")
	b.api.Send(tgbotapi.NewMessage(req.UserID, "❌ Ваш запрос доступа был отклонен администратором."))
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

		// Any user with USER or OWNER access type can generate 24h web link
		// We already checked CanOpen before showing this screen.
		// Guests are excluded from link generation logic in store or here.
		if b.canGenerateLink(userID, phone) {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔗 Создать гостевую ссылку", "gen_web_link_"+phone),
			))
		}
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
			tgbotapi.NewInlineKeyboardButtonData("🔗 Гостевая ссылка (24ч)", "gen_anon_link"),
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

	// Handle Contact
	if msg.Contact != nil {
		contact := msg.Contact
		firstName := contact.FirstName
		lastName := contact.LastName
		fullName := strings.TrimSpace(firstName + " " + lastName)

		// Update target info in session
		sess.TargetID = contact.UserID
		sess.TargetUser = "" // Contact doesn't have username
		sess.TargetName = fullName

		// If we are waiting for ID, we can proceed to next step
		if sess.State == StateWaitingUserID || sess.State == StateWaitingAdminID || sess.State == StateWaitingGuestID {
			if sess.State == StateWaitingGuestID {
				sess.State = StateWaitingGuestName // Or skip to confirm if we already have name
				// Let's keep it simple and skip to confirmation since we have name from contact
				b.confirmAddGuest(msg.Chat.ID, userID, sess)
				b.sessions.Delete(userID)
				return
			} else if sess.State == StateWaitingUserID {
				sess.State = StateWaitingExpiration
				b.showExpirationMenuSPA(msg.Chat.ID, sess.LastMenuID, sess.BarrierID)
			} else {
				b.confirmAddAdminSPA(msg.Chat.ID, userID, sess)
			}
			b.sessions.Store(userID, sess)
			return
		}
	}

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

		if sess.RequestID != "" {
			b.store.UpdateAccessRequestStatus(sess.RequestID, "APPROVED")
			b.api.Send(tgbotapi.NewMessage(sess.TargetID, "✅ Ваш запрос доступа одобрен! Теперь вы можете открывать шлагбаум."))
		}

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

func (b *Bot) canGenerateLink(userID int64, barrierID string) bool {
	if b.store.IsBarrierAdmin(userID, barrierID) {
		return true
	}

	cfg := b.store.GetConfig()
	for _, a := range cfg.Accesses {
		if a.UserID == userID && a.BarrierID == barrierID {
			// Only USER or OWNER can generate links
			if a.Type == config.AccessTypeUser || a.Type == config.AccessTypeOwner {
				if a.ExpiresAt.IsZero() || a.ExpiresAt.After(time.Now()) {
					return true
				}
			}
		}
	}
	return false
}

func (b *Bot) handleGenerateAnonymousLink(chatID int64, userID int64, messageID int, barrierID string) {
	if !b.canGenerateLink(userID, barrierID) {
		b.showError(chatID, messageID, "❌ Ошибка доступа.")
		return
	}

	token, err := b.store.AddAnonymousAccess(userID, barrierID)
	if err != nil {
		b.showError(chatID, messageID, "❌ Ошибка генерации ссылки.")
		return
	}

	cfg := b.store.GetConfig()
	prefix := cfg.Web.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	link := fmt.Sprintf("%s%sindex.html?token=%s", cfg.Web.BaseURL, prefix, token)

	text := fmt.Sprintf("🔗 Гостевая ссылка создана!\n\nОна будет действовать 24 часа и позволяет открыть только выбранный шлагбаум.\n\n[Открыть в браузере](%s)", link)

	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅ Назад", "ctrl_"+barrierID),
		),
	)
	edit.ReplyMarkup = &kb
	b.api.Send(edit)
}

func (b *Bot) showWebLink(chatID int64, userID int64, messageID int) {
	user, ok := b.store.GetUser(userID)
	if !ok || user.WebToken == "" {
		b.showError(chatID, messageID, "❌ Ошибка: пользователь не найден или токен не сгенерирован.")
		return
	}

	cfg := b.store.GetConfig()
	if !cfg.Web.Enabled {
		b.showError(chatID, messageID, "❌ Веб-интерфейс отключен в настройках.")
		return
	}

	prefix := cfg.Web.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	link := fmt.Sprintf("%s%sindex.html?token=%s", cfg.Web.BaseURL, prefix, user.WebToken)
	text := fmt.Sprintf("🌐 Ваш личный веб-интерфейс:\n\nЭтот интерфейс позволяет открывать доступные вам шлагбаумы через браузер.\n\n[Открыть в браузере](%s)\n\n⚠️ Не передавайте эту ссылку посторонним!", link)

	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
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
