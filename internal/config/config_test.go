package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/khaliullov/barrier-bot/internal/config"
)

func TestMigrateTelegramIDZero(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config_migrate_*.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// Write a config with user_id = 0
	initialTOML := `
telegram_token = "dummy"
master_admin_id = 12345

[[users]]
  telegram_id = 0
  username = "Ravil_Gafurov"
  full_name = "Равиль"

[[accesses]]
  id = "0_+79278087258_12345"
  user_id = 0
  barrier_id = "+79278087258"
  type = "USER"

[[administrators]]
  user_id = 0
  barrier_id = "+79278087258"
  role = "BARRIER_ADMIN"
`
	if err := os.WriteFile(tmpFile.Name(), []byte(initialTOML), 0644); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	mgr, err := config.NewManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	cfg := mgr.Config()
	if len(cfg.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(cfg.Users))
	}
	if cfg.Users[0].TelegramID >= 0 {
		t.Fatalf("expected user with TelegramID 0 to be migrated to a negative ID, got %d", cfg.Users[0].TelegramID)
	}

	migratedID := cfg.Users[0].TelegramID
	if len(cfg.Accesses) != 1 || cfg.Accesses[0].UserID != migratedID {
		t.Fatalf("expected access user_id to be migrated to %d, got %+v", migratedID, cfg.Accesses)
	}
	if len(cfg.Admins) != 1 || cfg.Admins[0].UserID != migratedID {
		t.Fatalf("expected admin user_id to be migrated to %d, got %+v", migratedID, cfg.Admins)
	}
}

func TestCleanupExpiredAccesses(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config_cleanup_*.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	mgr, err := config.NewManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Add an active access, an expired access, and a perpetual access
	err = mgr.Update(func(cfg *config.Config) {
		cfg.Accesses = []config.Access{
			{
				ID:        "acc_active",
				UserID:    101,
				BarrierID: "+79991112233",
				ExpiresAt: time.Now().Add(24 * time.Hour),
			},
			{
				ID:        "acc_expired",
				UserID:    102,
				BarrierID: "+79991112233",
				ExpiresAt: time.Now().Add(-1 * time.Hour),
			},
			{
				ID:        "acc_perm",
				UserID:    103,
				BarrierID: "+79991112233",
				ExpiresAt: time.Time{},
			},
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := mgr.Config()
	if len(cfg.Accesses) != 2 {
		t.Fatalf("expected 2 active accesses after cleanup, got %d", len(cfg.Accesses))
	}

	for _, a := range cfg.Accesses {
		if a.ID == "acc_expired" {
			t.Fatalf("expired access was not cleaned up")
		}
	}
}
