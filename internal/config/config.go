package config

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

type Role string

const (
	RoleSuperAdmin   Role = "SUPER_ADMIN"
	RoleBarrierAdmin Role = "BARRIER_ADMIN"
	RoleUser         Role = "USER"
)

type AccessType string

const (
	AccessTypeOwner AccessType = "OWNER"
	AccessTypeUser  AccessType = "USER"
	AccessTypeGuest AccessType = "GUEST"
)

type BarrierStatus string

const (
	StatusOnline  BarrierStatus = "ONLINE"
	StatusOffline BarrierStatus = "OFFLINE"
	StatusOpening BarrierStatus = "OPENING"
	StatusOpened  BarrierStatus = "OPENED"
	StatusError   BarrierStatus = "ERROR"
)

type SIPConfig struct {
	Host          string `toml:"host"`
	OutboundProxy string `toml:"outbound_proxy"`
	Port          int    `toml:"port"`
	User          string `toml:"user"`
	Password      string `toml:"password"`
}

type Barrier struct {
	Phone string `toml:"phone"`
	Name  string `toml:"name"`
}

type User struct {
	TelegramID int64     `toml:"telegram_id"`
	Username   string    `toml:"username"`
	FullName   string    `toml:"full_name"`
	CreatedAt  time.Time `toml:"created_at"`
}

type Access struct {
	ID        string     `toml:"id"`
	UserID    int64      `toml:"user_id"`
	BarrierID string     `toml:"barrier_id"` // Phone number used as ID
	Type      AccessType `toml:"type"`
	ExpiresAt time.Time  `toml:"expires_at"`
	CreatedBy int64      `toml:"created_by"`
	CreatedAt time.Time  `toml:"created_at"`
}

type Admin struct {
	UserID    int64     `toml:"user_id"`
	BarrierID string    `toml:"barrier_id"` // Empty for SUPER_ADMIN
	Role      Role      `toml:"role"`
	CreatedBy int64     `toml:"created_by"`
	CreatedAt time.Time `toml:"created_at"`
}

type LogEntry struct {
	UserID    int64     `toml:"user_id"`
	Username  string    `toml:"username"`
	Timestamp time.Time `toml:"timestamp"`
	Status    string    `toml:"status"`
}

type AdminLog struct {
	Timestamp time.Time `toml:"timestamp"`
	AdminID   int64     `toml:"admin_id"`
	AdminName string    `toml:"admin_name"`
	Action    string    `toml:"action"`
	Target    string    `toml:"target"`
	Barrier   string    `toml:"barrier"`
	Details   string    `toml:"details"`
}

type Config struct {
	TelegramToken string `toml:"telegram_token"`
	MasterAdminID int64  `toml:"master_admin_id"`
	Debug         bool   `toml:"debug"`
	ForceIPv6     bool   `toml:"force_ipv6"`

	SIP SIPConfig `toml:"sip"`

	Barriers []Barrier `toml:"barriers"`
	Users    []User    `toml:"users"`
	Accesses []Access  `toml:"accesses"`
	Admins   []Admin   `toml:"administrators"`

	// AuditLogs maps Barrier Phone -> List of LogEntry (max 10)
	AuditLogs map[string][]LogEntry `toml:"audit_logs"`
	AdminLogs []AdminLog            `toml:"admin_logs"`

	// Migration helpers (not persisted if omitted in write)
	OldAdminPermissions map[string][]string `toml:"admin_permissions"`
	OldBarrierUsers     map[string][]int64  `toml:"barrier_users"`
}

type Manager struct {
	mu   sync.RWMutex
	path string
	cfg  *Config
}

func NewManager(path string) (*Manager, error) {
	m := &Manager{path: path}
	if err := m.Reload(); err != nil {
		if os.IsNotExist(err) {
			m.cfg = &Config{
				AuditLogs: make(map[string][]LogEntry),
			}
			return m, nil
		}
		return nil, err
	}
	m.migrate()
	return m, nil
}

func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var cfg Config
	if _, err := toml.DecodeFile(m.path, &cfg); err != nil {
		return err
	}

	if cfg.AuditLogs == nil {
		cfg.AuditLogs = make(map[string][]LogEntry)
	}

	m.cfg = &cfg
	return nil
}

func (m *Manager) migrate() {
	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	// Migrate users
	for phone, userIDs := range m.cfg.OldBarrierUsers {
		for _, uid := range userIDs {
			found := false
			for _, a := range m.cfg.Accesses {
				if a.UserID == uid && a.BarrierID == phone {
					found = true
					break
				}
			}
			if !found {
				m.cfg.Accesses = append(m.cfg.Accesses, Access{
					ID:        fmt.Sprintf("migrated_%d_%s", uid, phone),
					UserID:    uid,
					BarrierID: phone,
					Type:      AccessTypeUser,
					CreatedAt: time.Now(),
				})
				changed = true
			}
		}
	}

	// Migrate admins
	for uidStr, phones := range m.cfg.OldAdminPermissions {
		uid, err := strconv.ParseInt(uidStr, 10, 64)
		if err != nil {
			continue
		}
		for _, phone := range phones {
			found := false
			for _, a := range m.cfg.Admins {
				if a.UserID == uid && a.BarrierID == phone {
					found = true
					break
				}
			}
			if !found {
				m.cfg.Admins = append(m.cfg.Admins, Admin{
					UserID:    uid,
					BarrierID: phone,
					Role:      RoleBarrierAdmin,
					CreatedAt: time.Now(),
				})
				changed = true
			}
		}
	}

	if changed {
		m.cfg.OldBarrierUsers = nil
		m.cfg.OldAdminPermissions = nil
		m.saveLocked()
	}
}

func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.cfg
}

// Update executes a function that modifies the config and then saves it atomically.
func (m *Manager) Update(fn func(*Config)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fn(m.cfg)
	return m.saveLocked()
}

func (m *Manager) cleanupLocked() {
	now := time.Now()

	// 1. Remove expired accesses
	var activeAccesses []Access
	for _, a := range m.cfg.Accesses {
		if !a.ExpiresAt.IsZero() && a.ExpiresAt.Before(now) {
			continue
		}
		activeAccesses = append(activeAccesses, a)
	}
	m.cfg.Accesses = activeAccesses

	// 2. Remove orphaned users
	referencedUsers := make(map[int64]bool)
	referencedUsers[m.cfg.MasterAdminID] = true
	for _, a := range m.cfg.Accesses {
		referencedUsers[a.UserID] = true
		referencedUsers[a.CreatedBy] = true
	}
	for _, adm := range m.cfg.Admins {
		referencedUsers[adm.UserID] = true
		referencedUsers[adm.CreatedBy] = true
	}

	var activeUsers []User
	for _, u := range m.cfg.Users {
		if referencedUsers[u.TelegramID] {
			activeUsers = append(activeUsers, u)
		}
	}
	m.cfg.Users = activeUsers

	// 3. Enforce ring buffer size 10 for audit_logs
	for phone, logs := range m.cfg.AuditLogs {
		if len(logs) > 10 {
			m.cfg.AuditLogs[phone] = logs[len(logs)-10:]
		}
	}

	// 4. Enforce ring buffer size 10 for admin_logs
	if len(m.cfg.AdminLogs) > 10 {
		m.cfg.AdminLogs = m.cfg.AdminLogs[len(m.cfg.AdminLogs)-10:]
	}
}

func (m *Manager) saveLocked() error {
	// Call cleanup at the BEGINNING of saveLocked()
	m.cleanupLocked()

	// Atomic write: write to temp file then rename
	tmpPath := m.path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer f.Close()

	if err := toml.NewEncoder(f).Encode(m.cfg); err != nil {
		return fmt.Errorf("failed to encode toml: %w", err)
	}
	f.Sync()
	f.Close()

	if err := os.Rename(tmpPath, m.path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (m *Manager) AddLog(phone string, entry LogEntry) error {
	return m.Update(func(cfg *Config) {
		logs := cfg.AuditLogs[phone]
		logs = append(logs, entry)
		if len(logs) > 10 {
			logs = logs[len(logs)-10:]
		}
		cfg.AuditLogs[phone] = logs
	})
}

func (m *Manager) AddAdminLog(log AdminLog) error {
	return m.Update(func(cfg *Config) {
		cfg.AdminLogs = append(cfg.AdminLogs, log)
		if len(cfg.AdminLogs) > 10 {
			cfg.AdminLogs = cfg.AdminLogs[len(cfg.AdminLogs)-100:]
		}
	})
}
