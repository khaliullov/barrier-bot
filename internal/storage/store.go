package storage

import (
	"fmt"
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

func (s *Store) GetUserByUsername(username string) (config.User, bool) {
	if username == "" {
		return config.User{}, false
	}
	cleanUsername := username
	if cleanUsername[0] == '@' {
		cleanUsername = cleanUsername[1:]
	}

	cfg := s.manager.Config()
	for _, u := range cfg.Users {
		uClean := u.Username
		if len(uClean) > 0 && uClean[0] == '@' {
			uClean = uClean[1:]
		}
		if uClean == cleanUsername {
			return u, true
		}
	}
	return config.User{}, false
}

func (s *Store) UpsertUser(user config.User) error {
	return s.manager.Update(func(cfg *config.Config) {
		var existingUserIndex = -1

		// 1. Пытаемся найти пользователя по ID или по Username
		for i, u := range cfg.Users {
			if u.TelegramID != 0 && u.TelegramID == user.TelegramID {
				existingUserIndex = i
				break
			}
			if u.Username != "" && u.Username == user.Username {
				existingUserIndex = i
				break
			}
		}

		if existingUserIndex != -1 {
			existingUser := &cfg.Users[existingUserIndex]

			// Если у найденного пользователя был ID 0, а теперь мы получили реальный ID
			if existingUser.TelegramID == 0 && user.TelegramID != 0 {
				oldID := int64(0)
				newID := user.TelegramID

				// Обновляем самого пользователя
				existingUser.TelegramID = newID

				// 2. Обновляем все записи в Accesses, где был ID 0
				for j, a := range cfg.Accesses {
					if a.UserID == oldID {
						cfg.Accesses[j].UserID = newID
					}
				}

				// 3. Обновляем все записи в Administrators
				for j, adm := range cfg.Admins {
					if adm.UserID == oldID {
						cfg.Admins[j].UserID = newID
					}
				}
			}

			// Обновляем остальные данные
			if user.Username != "" {
				existingUser.Username = user.Username
			}
			if user.FullName != "" {
				existingUser.FullName = user.FullName
			}
		} else {
			// Новый пользователь
			if user.CreatedAt.IsZero() {
				user.CreatedAt = time.Now()
			}
			cfg.Users = append(cfg.Users, user)
		}
	})
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
	var admins []config.Admin
	cfg := s.manager.Config()
	for _, a := range cfg.Admins {
		if a.BarrierID == barrierID {
			admins = append(admins, a)
		}
	}
	return admins
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
