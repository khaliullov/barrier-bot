package storage_test

import (
	"os"
	"testing"
	"time"

	"github.com/khaliullov/barrier-bot/internal/config"
	"github.com/khaliullov/barrier-bot/internal/storage"
)

func setupTestStore(t *testing.T) (*storage.Store, *config.Manager, func()) {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "store_test_*.toml")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	mgr, err := config.NewManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	store := storage.NewStore(mgr)
	cleanup := func() {
		os.Remove(tmpFile.Name())
	}
	return store, mgr, cleanup
}

func TestAddPendingUserByUsernameAndGrantAccess(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	barrierPhone := "+79278094207"
	adminID := int64(475925115)

	// 1. Check NextPendingUserID
	pendingID := store.NextPendingUserID()
	if pendingID >= 0 {
		t.Fatalf("expected negative pending ID, got %d", pendingID)
	}

	// 2. Grant access for pending user @Ilias_IR, "Ильяс", Бессрочно
	err := store.GrantUserAccess(config.User{
		TelegramID: pendingID,
		Username:   "Ilias_IR",
		FullName:   "Ильяс",
	}, adminID, barrierPhone, time.Time{}, config.AccessTypeUser)
	if err != nil {
		t.Fatalf("GrantUserAccess failed: %v", err)
	}

	// 3. Verify user is saved and retrievable
	u, ok := store.GetUser(pendingID)
	if !ok {
		t.Fatalf("expected user with ID %d to exist", pendingID)
	}
	if u.FullName != "Ильяс" || u.Username != "Ilias_IR" {
		t.Fatalf("unexpected user data: %+v", u)
	}

	// 4. Case-insensitive lookup by username with and without @
	uByAt, ok := store.GetUserByUsername("@ilias_ir")
	if !ok || uByAt.TelegramID != pendingID {
		t.Fatalf("failed to find user by @ilias_ir: %+v", uByAt)
	}

	uNoAt, ok := store.GetUserByUsername("ILIAS_IR")
	if !ok || uNoAt.TelegramID != pendingID {
		t.Fatalf("failed to find user by ILIAS_IR: %+v", uNoAt)
	}

	// 5. Verify barrier users list
	barrierUsers := store.GetBarrierUsers(barrierPhone)
	if len(barrierUsers) != 1 {
		t.Fatalf("expected 1 barrier user, got %d", len(barrierUsers))
	}
	if barrierUsers[0].UserID != pendingID {
		t.Fatalf("expected access for user ID %d, got %d", pendingID, barrierUsers[0].UserID)
	}

	// 6. Add second pending user @another_user to verify no collision
	secondPendingID := store.NextPendingUserID()
	if secondPendingID >= pendingID {
		t.Fatalf("expected second pending ID (%d) to be less than first (%d)", secondPendingID, pendingID)
	}

	err = store.GrantUserAccess(config.User{
		TelegramID: secondPendingID,
		Username:   "another_user",
		FullName:   "Второй Пользователь",
	}, adminID, barrierPhone, time.Time{}, config.AccessTypeUser)
	if err != nil {
		t.Fatalf("GrantUserAccess for second user failed: %v", err)
	}

	barrierUsers = store.GetBarrierUsers(barrierPhone)
	if len(barrierUsers) != 2 {
		t.Fatalf("expected 2 barrier users, got %d", len(barrierUsers))
	}

	// 7. Now simulate user @Ilias_IR sending /start with real Telegram ID 537350675
	realTelegramID := int64(537350675)
	err = store.UpsertUser(config.User{
		TelegramID: realTelegramID,
		Username:   "Ilias_IR",
		FullName:   "Ильяс",
	})
	if err != nil {
		t.Fatalf("UpsertUser with real ID failed: %v", err)
	}

	// 8. Verify access migrated to realTelegramID
	if !store.CanOpen(realTelegramID, barrierPhone) {
		t.Fatalf("expected realTelegramID %d to have CanOpen == true", realTelegramID)
	}

	// Old pending ID should no longer have access
	if store.CanOpen(pendingID, barrierPhone) {
		t.Fatalf("expected old pendingID %d to not have CanOpen", pendingID)
	}

	// Check barrier users list has the real ID
	barrierUsers = store.GetBarrierUsers(barrierPhone)
	foundReal := false
	for _, a := range barrierUsers {
		if a.UserID == realTelegramID {
			foundReal = true
		}
	}
	if !foundReal {
		t.Fatalf("real user ID %d not found in barrier accesses", realTelegramID)
	}
}

func TestCleanupDoesNotDeleteUsers(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	testID := int64(999888)
	err := store.UpsertUser(config.User{
		TelegramID: testID,
		Username:   "standalone_user",
		FullName:   "Standalone",
	})
	if err != nil {
		t.Fatalf("UpsertUser failed: %v", err)
	}

	// User should not be purged by cleanupLocked
	u, ok := store.GetUser(testID)
	if !ok {
		t.Fatalf("user was prematurely deleted by cleanup")
	}
	if u.Username != "standalone_user" {
		t.Fatalf("unexpected username: %s", u.Username)
	}
}
