package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/khaliullov/barrier-bot/internal/config"
)

type Store struct {
	manager *config.Manager
}

func NewStore(manager *config.Manager) *Store {
	return &Store{manager: manager}
}

// Permission Checks

func (s *Store) IsSuperAdmin(userID int64) bool {
	cfg := s.manager.Config()
	if cfg.MasterAdminID == userID {
		return true
	}
	for _, admin := range cfg.Admins {
		if admin.UserID == userID && admin.Role == config.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func (s *Store) IsBarrierAdmin(userID int64, barrierID string) bool {
	// Супер-админ является админом всех шлагбаумов
	if s.IsSuperAdmin(userID) {
		return true
	}

	cfg := s.manager.Config()
	// Проверяем ТОЛЬКО список явных администраторов (cfg.Admins)
	// Это исключает обычных пользователей и гостей из cfg.Accesses
	for _, admin := range cfg.Admins {
		if admin.UserID == userID && admin.BarrierID == barrierID && admin.Role == config.RoleBarrierAdmin {
			return true
		}
	}
	return false
}

func (s *Store) CanOpen(userID int64, barrierPhone string) bool {
	if s.IsSuperAdmin(userID) {
		return true
	}
	if s.IsBarrierAdmin(userID, barrierPhone) {
		return true
	}

	cfg := s.manager.Config()
	for _, access := range cfg.Accesses {
		if access.UserID == userID && access.BarrierID == barrierPhone {
			// Проверка срока действия
			if access.ExpiresAt.IsZero() || access.ExpiresAt.After(time.Now()) {
				return true
			}
		}
	}
	return false
}

func (s *Store) CanGrantGuestAccess(userID int64, barrierID string) bool {
	if s.IsSuperAdmin(userID) {
		return true
	}
	if s.IsBarrierAdmin(userID, barrierID) {
		return true
	}

	cfg := s.manager.Config()
	for _, access := range cfg.Accesses {
		if access.UserID == userID && access.BarrierID == barrierID {
			// Только USER или OWNER могут давать гостевой доступ
			// GUEST не может плодить других гостей
			if access.Type == config.AccessTypeUser || access.Type == config.AccessTypeOwner {
				if access.ExpiresAt.IsZero() || access.ExpiresAt.After(time.Now()) {
					return true
				}
			}
		}
	}
	return false
}

// User Management

func (s *Store) GetUser(userID int64) (config.User, bool) {
	cfg := s.manager.Config()
	for _, u := range cfg.Users {
		if u.TelegramID == userID {
			return u, true
		}
	}
	return config.User{}, false
}

func (s *Store) GetUserByToken(token string) (config.User, bool) {
	if token == "" {
		return config.User{}, false
	}
	cfg := s.manager.Config()
	for _, u := range cfg.Users {
		if u.WebToken == token {
			return u, true
		}
	}
	return config.User{}, false
}

func (s *Store) GetAnonymousAccess(token string) (config.AnonymousAccess, bool) {
	if token == "" {
		return config.AnonymousAccess{}, false
	}
	cfg := s.manager.Config()
	for _, a := range cfg.AnonymousAccesses {
		if a.Token == token {
			if a.ExpiresAt.After(time.Now()) {
				return a, true
			}
		}
	}
	return config.AnonymousAccess{}, false
}

func (s *Store) AddAnonymousAccess(adminID int64, barrierID string) (string, error) {
	token := generateToken()
	err := s.manager.Update(func(cfg *config.Config) {
		cfg.AnonymousAccesses = append(cfg.AnonymousAccesses, config.AnonymousAccess{
			Token:     token,
			BarrierID: barrierID,
			ExpiresAt: time.Now().Add(24 * time.Hour),
			CreatedBy: adminID,
		})
	})
	return token, err
}

func (s *Store) IsGuestOnly(userID int64) bool {
	if s.IsSuperAdmin(userID) {
		return false
	}
	cfg := s.manager.Config()
	// Check if admin
	for _, adm := range cfg.Admins {
		if adm.UserID == userID {
			return false
		}
	}
	// Check accesses
	isGuest := false
	for _, a := range cfg.Accesses {
		if a.UserID == userID {
			if a.Type != config.AccessTypeGuest {
				return false // Has regular access
			}
			if a.ExpiresAt.IsZero() || a.ExpiresAt.After(time.Now()) {
				isGuest = true
			}
		}
	}
	return isGuest
}

func (s *Store) NextPendingUserID() int64 {
	cfg := s.manager.Config()
	minID := int64(0)
	for _, u := range cfg.Users {
		if u.TelegramID < minID {
			minID = u.TelegramID
		}
	}
	for _, a := range cfg.Accesses {
		if a.UserID < minID {
			minID = a.UserID
		}
	}
	for _, adm := range cfg.Admins {
		if adm.UserID < minID {
			minID = adm.UserID
		}
	}
	return minID - 1
}

func (s *Store) GetUserByUsername(username string) (config.User, bool) {
	if username == "" {
		return config.User{}, false
	}
	cleanUsername := strings.TrimPrefix(username, "@")

	cfg := s.manager.Config()
	for _, u := range cfg.Users {
		uClean := strings.TrimPrefix(u.Username, "@")
		if strings.EqualFold(uClean, cleanUsername) {
			return u, true
		}
	}
	return config.User{}, false
}

func (s *Store) UpsertUser(user config.User) error {
	return s.manager.Update(func(cfg *config.Config) {
		s.upsertUserLocked(cfg, user)
	})
}

func (s *Store) upsertUserLocked(cfg *config.Config, user config.User) {
	cleanUsername := strings.TrimPrefix(user.Username, "@")
	if cleanUsername != "" {
		user.Username = cleanUsername
	}

	var userByIDIndex = -1
	var pendingUserIndex = -1

	for i, u := range cfg.Users {
		if user.TelegramID != 0 && u.TelegramID == user.TelegramID {
			userByIDIndex = i
		}
		uClean := strings.TrimPrefix(u.Username, "@")
		if cleanUsername != "" && strings.EqualFold(uClean, cleanUsername) && u.TelegramID <= 0 {
			pendingUserIndex = i
		}
	}

	// Case 1: Real telegram user logged in, and there was a pending user record with their username
	if user.TelegramID > 0 && pendingUserIndex != -1 {
		oldID := cfg.Users[pendingUserIndex].TelegramID
		newID := user.TelegramID

		// Migrate accesses
		for j, a := range cfg.Accesses {
			if a.UserID == oldID {
				cfg.Accesses[j].UserID = newID
			}
		}

		// Migrate admins
		for j, adm := range cfg.Admins {
			if adm.UserID == oldID {
				cfg.Admins[j].UserID = newID
			}
		}

		if userByIDIndex != -1 && userByIDIndex != pendingUserIndex {
			// Real user record already existed separately, merge and remove pending
			if user.Username != "" {
				cfg.Users[userByIDIndex].Username = user.Username
			}
			if user.FullName != "" {
				cfg.Users[userByIDIndex].FullName = user.FullName
			}
			cfg.Users = append(cfg.Users[:pendingUserIndex], cfg.Users[pendingUserIndex+1:]...)
			return
		}

		// Update pending record to real ID
		cfg.Users[pendingUserIndex].TelegramID = newID
		if user.Username != "" {
			cfg.Users[pendingUserIndex].Username = user.Username
		}
		if user.FullName != "" {
			cfg.Users[pendingUserIndex].FullName = user.FullName
		}
		if cfg.Users[pendingUserIndex].WebToken == "" {
			cfg.Users[pendingUserIndex].WebToken = generateToken()
		}
		return
	}

	// Case 2: Matching by TelegramID
	if userByIDIndex != -1 {
		existing := &cfg.Users[userByIDIndex]
		if user.Username != "" {
			existing.Username = user.Username
		}
		if user.FullName != "" {
			existing.FullName = user.FullName
		}
		if existing.WebToken == "" {
			existing.WebToken = generateToken()
		}
		return
	}

	// Case 3: Matching pending user by username (when user.TelegramID <= 0)
	if pendingUserIndex != -1 {
		existing := &cfg.Users[pendingUserIndex]
		if user.TelegramID != 0 {
			existing.TelegramID = user.TelegramID
		}
		if user.Username != "" {
			existing.Username = user.Username
		}
		if user.FullName != "" {
			existing.FullName = user.FullName
		}
		if existing.WebToken == "" {
			existing.WebToken = generateToken()
		}
		return
	}

	// Case 4: Brand new user
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now()
	}
	if user.WebToken == "" {
		user.WebToken = generateToken()
	}
	cfg.Users = append(cfg.Users, user)
}

func (s *Store) GrantUserAccess(user config.User, adminID int64, barrierID string, expiresAt time.Time, accType config.AccessType) error {
	return s.manager.Update(func(cfg *config.Config) {
		s.upsertUserLocked(cfg, user)
		found := false
		for i, a := range cfg.Accesses {
			if a.UserID == user.TelegramID && a.BarrierID == barrierID && a.Type == accType {
				cfg.Accesses[i].ExpiresAt = expiresAt
				cfg.Accesses[i].CreatedBy = adminID
				found = true
				break
			}
		}
		if !found {
			cfg.Accesses = append(cfg.Accesses, config.Access{
				ID:        fmt.Sprintf("%d_%s_%d", user.TelegramID, barrierID, time.Now().Unix()),
				UserID:    user.TelegramID,
				BarrierID: barrierID,
				Type:      accType,
				ExpiresAt: expiresAt,
				CreatedBy: adminID,
				CreatedAt: time.Now(),
			})
		}
	})
}

func (s *Store) AddAdminUser(user config.User, createdBy int64, barrierID string, role config.Role) error {
	return s.manager.Update(func(cfg *config.Config) {
		s.upsertUserLocked(cfg, user)
		found := false
		for i, a := range cfg.Admins {
			if a.UserID == user.TelegramID && a.BarrierID == barrierID {
				cfg.Admins[i].Role = role
				cfg.Admins[i].CreatedBy = createdBy
				found = true
				break
			}
		}
		if !found {
			cfg.Admins = append(cfg.Admins, config.Admin{
				UserID:    user.TelegramID,
				BarrierID: barrierID,
				Role:      role,
				CreatedBy: createdBy,
				CreatedAt: time.Now(),
			})
		}
	})
}

func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// Access Management

func (s *Store) GrantAccess(adminID int64, userID int64, barrierID string, expiresAt time.Time, accType config.AccessType) error {
	return s.manager.Update(func(cfg *config.Config) {
		found := false
		for i, a := range cfg.Accesses {
			if a.UserID == userID && a.BarrierID == barrierID && a.Type == accType {
				cfg.Accesses[i].ExpiresAt = expiresAt
				cfg.Accesses[i].CreatedBy = adminID
				found = true
				break
			}
		}
		if !found {
			cfg.Accesses = append(cfg.Accesses, config.Access{
				ID:        fmt.Sprintf("%d_%s_%d", userID, barrierID, time.Now().Unix()),
				UserID:    userID,
				BarrierID: barrierID,
				Type:      accType,
				ExpiresAt: expiresAt,
				CreatedBy: adminID,
				CreatedAt: time.Now(),
			})
		}
	})
}

func (s *Store) RevokeAccess(userID int64, barrierID string) error {
	return s.manager.Update(func(cfg *config.Config) {
		var newAccesses []config.Access
		for _, a := range cfg.Accesses {
			if a.UserID == userID && a.BarrierID == barrierID {
				continue
			}
			newAccesses = append(newAccesses, a)
		}
		cfg.Accesses = newAccesses
	})
}

func (s *Store) GetBarrierUsers(barrierID string) []config.Access {
	var accesses []config.Access
	cfg := s.manager.Config()
	for _, a := range cfg.Accesses {
		if a.BarrierID == barrierID {
			accesses = append(accesses, a)
		}
	}
	return accesses
}

func (s *Store) GetUserBarriers(userID int64) []config.Barrier {
	cfg := s.manager.Config()
	var barriers []config.Barrier
	seen := make(map[string]bool)

	// Super admins see all
	if s.IsSuperAdmin(userID) {
		return cfg.Barriers
	}

	// Barriers where user has explicit access
	for _, a := range cfg.Accesses {
		if a.UserID == userID && (a.ExpiresAt.IsZero() || a.ExpiresAt.After(time.Now())) {
			if !seen[a.BarrierID] {
				for _, b := range cfg.Barriers {
					if b.Phone == a.BarrierID {
						barriers = append(barriers, b)
						seen[a.BarrierID] = true
						break
					}
				}
			}
		}
	}

	// Barriers where user is admin
	for _, adm := range cfg.Admins {
		if adm.UserID == userID {
			if !seen[adm.BarrierID] {
				for _, b := range cfg.Barriers {
					if b.Phone == adm.BarrierID {
						barriers = append(barriers, b)
						seen[adm.BarrierID] = true
						break
					}
				}
			}
		}
	}

	return barriers
}

// Admin Management

func (s *Store) AddAdmin(createdBy int64, userID int64, barrierID string, role config.Role) error {
	return s.manager.Update(func(cfg *config.Config) {
		found := false
		for i, a := range cfg.Admins {
			if a.UserID == userID && a.BarrierID == barrierID {
				cfg.Admins[i].Role = role
				cfg.Admins[i].CreatedBy = createdBy
				found = true
				break
			}
		}
		if !found {
			cfg.Admins = append(cfg.Admins, config.Admin{
				UserID:    userID,
				BarrierID: barrierID,
				Role:      role,
				CreatedBy: createdBy,
				CreatedAt: time.Now(),
			})
		}
	})
}

func (s *Store) RemoveAdmin(userID int64, barrierID string) error {
	return s.manager.Update(func(cfg *config.Config) {
		var newAdmins []config.Admin
		for _, a := range cfg.Admins {
			if a.UserID == userID && a.BarrierID == barrierID {
				continue
			}
			newAdmins = append(newAdmins, a)
		}
		cfg.Admins = newAdmins
	})
}

func (s *Store) GetBarrierAdmins(barrierID string) []config.Admin {
	cfg := s.manager.Config()
	var admins []config.Admin
	seen := make(map[int64]bool)

	// 1. Всегда добавляем Master Admin
	admins = append(admins, config.Admin{
		UserID:    cfg.MasterAdminID,
		Role:      config.RoleSuperAdmin,
		BarrierID: "", // Глобальный админ
	})
	seen[cfg.MasterAdminID] = true

	// 2. Добавляем всех Super Admins
	for _, a := range cfg.Admins {
		if a.Role == config.RoleSuperAdmin {
			if !seen[a.UserID] {
				admins = append(admins, a)
				seen[a.UserID] = true
			}
		}
	}

	// 3. Добавляем администраторов конкретного шлагбаума
	for _, a := range cfg.Admins {
		if a.BarrierID == barrierID && a.Role == config.RoleBarrierAdmin {
			if !seen[a.UserID] {
				admins = append(admins, a)
				seen[a.UserID] = true
			}
		}
	}

	return admins
}

func (s *Store) AddAccessRequest(req *config.AccessRequest) error {
	return s.manager.Update(func(cfg *config.Config) {
		if req.CreatedAt.IsZero() {
			req.CreatedAt = time.Now()
		}
		if req.ID == "" {
			req.ID = fmt.Sprintf("req_%d_%s_%d", req.UserID, req.BarrierID, time.Now().UnixNano())
		}
		cfg.AccessRequests = append(cfg.AccessRequests, *req)
	})
}

func (s *Store) GetAccessRequest(id string) (config.AccessRequest, bool) {
	cfg := s.manager.Config()
	for _, r := range cfg.AccessRequests {
		if r.ID == id {
			return r, true
		}
	}
	return config.AccessRequest{}, false
}

func (s *Store) UpdateAccessRequestStatus(id string, status string) error {
	return s.manager.Update(func(cfg *config.Config) {
		for i, r := range cfg.AccessRequests {
			if r.ID == id {
				cfg.AccessRequests[i].Status = status
				break
			}
		}
	})
}

// Barrier Management

func (s *Store) GetBarriers() []config.Barrier {
	return s.manager.Config().Barriers
}

func (s *Store) AddBarrier(phone, name string) error {
	return s.manager.Update(func(cfg *config.Config) {
		for i, b := range cfg.Barriers {
			if b.Phone == phone {
				cfg.Barriers[i].Name = name
				return
			}
		}
		cfg.Barriers = append(cfg.Barriers, config.Barrier{Phone: phone, Name: name})
	})
}

// Logging

func (s *Store) AddLog(phone string, entry config.LogEntry) error {
	return s.manager.AddLog(phone, entry)
}

func (s *Store) AddAdminLog(log config.AdminLog) error {
	return s.manager.Update(func(cfg *config.Config) {
		cfg.AdminLogs = append(cfg.AdminLogs, log)
		// Keep last 100 admin logs?
		if len(cfg.AdminLogs) > 100 {
			cfg.AdminLogs = cfg.AdminLogs[len(cfg.AdminLogs)-100:]
		}
	})
}

func (s *Store) GetLogs(phone string) []config.LogEntry {
	return s.manager.Config().AuditLogs[phone]
}

func (s *Store) GetAdminLogs() []config.AdminLog {
	return s.manager.Config().AdminLogs
}

func (s *Store) GetConfig() config.Config {
	return s.manager.Config()
}

func (s *Store) GetLastOpenTime(barrierID string) time.Time {
	logs := s.GetLogs(barrierID)
	for i := len(logs) - 1; i >= 0; i-- {
		if logs[i].Status == "Opened" {
			return logs[i].Timestamp
		}
	}
	return time.Time{}
}
