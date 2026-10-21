package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/access"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	hash, err := access.HashPassword("bootstrap-owner-password")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "control")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(dir, "control.db"), "owner", hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestBootstrapOwnerAndAuthentication(t *testing.T) {
	store := testStore(t)
	owner, err := store.Authenticate(context.Background(), "", "bootstrap-owner-password")
	if err != nil {
		t.Fatal(err)
	}
	if owner.Role != RoleOwner || owner.Username != "owner" || owner.DisplayName != "owner" || !owner.Active() {
		t.Fatalf("unexpected owner: %+v", owner)
	}
	if _, err := store.Authenticate(context.Background(), "owner", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error=%v", err)
	}
}

func TestBootstrapOwnerRenameAndPasswordRotation(t *testing.T) {
	hash, err := access.HashPassword("bootstrap-owner-password")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "control")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "control.db")
	store, err := Open(path, "owner", hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	nextHash, err := access.HashPassword("rotated-owner-password")
	if err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, "delicious233", nextHash)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Authenticate(context.Background(), "delicious233", "rotated-owner-password")
	if err != nil {
		t.Fatal(err)
	}
	if owner.Username != "delicious233" || owner.DisplayName != "delicious233" || owner.Role != RoleOwner {
		t.Fatalf("unexpected owner after rotation: %+v", owner)
	}
	if _, err := store.Authenticate(context.Background(), "owner", "rotated-owner-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old owner username survived rotation: %v", err)
	}
}

func TestControlDatabaseFilesStayPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not meaningful on Windows")
	}
	store := testStore(t)
	if _, err := store.Authenticate(context.Background(), "owner", "bootstrap-owner-password"); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := store.path + suffix
		info, err := os.Stat(path)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("%s mode=%04o, want 0600", filepath.Base(path), got)
		}
	}
}

func TestCreateUpdateAndAccountGrants(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "alice", DisplayName: "Alice", Password: "alice-password-123",
		Role: RoleOperator, Accounts: []string{"root", "tenant-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(user.Accounts) != 2 {
		t.Fatalf("accounts=%v", user.Accounts)
	}
	authenticated, err := store.Authenticate(ctx, "ALICE", "alice-password-123")
	if err != nil || authenticated.ID != user.ID {
		t.Fatalf("auth=%+v err=%v", authenticated, err)
	}
	role := RoleViewer
	password := "alice-password-456"
	accounts := []string{"tenant-b"}
	updated, err := store.UpdateUser(ctx, user.ID, UpdateUserInput{Role: &role, Password: &password, Accounts: &accounts, ActorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Role != RoleViewer || updated.AuthVersion <= user.AuthVersion || len(updated.Accounts) != 1 || updated.Accounts[0] != "tenant-b" {
		t.Fatalf("updated=%+v", updated)
	}
	if _, err := store.Authenticate(ctx, "alice", "alice-password-123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("old password survived update")
	}
	if _, err := store.Authenticate(ctx, "alice", "alice-password-456"); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayNameUpdateKeepsSessionVersion(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "alice", DisplayName: "Alice", Password: "alice-password-123", Role: RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	display := "Alice Chen"
	updated, err := store.UpdateUser(ctx, user.ID, UpdateUserInput{DisplayName: &display, ActorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AuthVersion != user.AuthVersion {
		t.Fatalf("display-name-only update incremented auth version: %d -> %d", user.AuthVersion, updated.AuthVersion)
	}
}

func TestSessionValidTracksVersionAndStatus(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "alice", DisplayName: "Alice", Password: "alice-password-123", Role: RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := store.SessionValid(ctx, user.ID, user.AuthVersion); err != nil || !valid {
		t.Fatalf("fresh session valid=%v err=%v", valid, err)
	}
	status := StatusDisabled
	updated, err := store.UpdateUser(ctx, user.ID, UpdateUserInput{Status: &status, ActorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := store.SessionValid(ctx, user.ID, user.AuthVersion); err != nil || valid {
		t.Fatalf("old session survived valid=%v err=%v", valid, err)
	}
	if valid, err := store.SessionValid(ctx, user.ID, updated.AuthVersion); err != nil || valid {
		t.Fatalf("disabled user session valid=%v err=%v", valid, err)
	}
}

func TestOwnerCannotBeDemotedOrDisabled(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner, err := store.UserByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	role, status := RoleAdmin, StatusDisabled
	if _, err := store.UpdateUser(ctx, owner.ID, UpdateUserInput{Role: &role, ActorID: 1}); !errors.Is(err, ErrInvalidUser) {
		t.Fatal("owner demotion accepted")
	}
	if _, err := store.UpdateUser(ctx, owner.ID, UpdateUserInput{Status: &status, ActorID: 1}); !errors.Is(err, ErrInvalidUser) {
		t.Fatal("owner disable accepted")
	}
}

func TestUpdateUserRechecksActorPermission(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	admin, err := store.CreateUser(ctx, CreateUserInput{
		Username: "admin1", DisplayName: "Admin", Password: "admin-password-123", Role: RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherAdmin, err := store.CreateUser(ctx, CreateUserInput{
		Username: "admin2", DisplayName: "Admin Two", Password: "admin-password-456", Role: RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := store.CreateUser(ctx, CreateUserInput{
		Username: "operator1", DisplayName: "Operator", Password: "operator-password-123", Role: RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer := RoleViewer
	if _, err := store.UpdateUser(ctx, operator.ID, UpdateUserInput{Role: &viewer, ActorID: admin.ID}); err != nil {
		t.Fatalf("admin could not modify operator: %v", err)
	}
	if _, err := store.UpdateUser(ctx, otherAdmin.ID, UpdateUserInput{DisplayName: ptr("blocked"), ActorID: admin.ID}); !errors.Is(err, ErrPermission) {
		t.Fatalf("admin modified another admin: %v", err)
	}
	if _, err := store.UpdateUser(ctx, admin.ID, UpdateUserInput{Role: ptr(RoleOperator), ActorID: admin.ID}); !errors.Is(err, ErrPermission) {
		t.Fatalf("admin changed own role: %v", err)
	}
}

func ptr[T any](value T) *T {
	return &value
}

func TestRejectsUnsafeAccountKeysAndOwnerCreation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	for _, input := range []CreateUserInput{
		{Username: "bad1", DisplayName: "Bad", Password: "password-123456", Role: RoleViewer, Accounts: []string{"../escape"}},
		{Username: "bad2", DisplayName: "Bad", Password: "password-123456", Role: RoleOwner},
		{Username: "Bad Name", DisplayName: "Bad", Password: "password-123456", Role: RoleViewer},
	} {
		if _, err := store.CreateUser(ctx, input); !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("unsafe input accepted: %+v err=%v", input, err)
		}
	}
}

func TestAuditRoundTripAndPrune(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	details, _ := json.Marshal(map[string]any{"tool": "list_courses", "success": true})
	if err := store.RecordAudit(ctx, AuditEvent{
		ActorID: 1, ActorUsername: "owner", ActorRole: RoleOwner,
		AccountKey: "root", Category: "tool", Action: "tool.call", Target: "list_courses",
		Success: true, IP: "203.0.113.7", RequestID: "req-1", Details: details,
	}); err != nil {
		t.Fatal(err)
	}
	items, total, err := store.ListAudit(ctx, AuditFilter{Category: "tool", Limit: 20})
	if err != nil || total != 1 || len(items) != 1 || items[0].Action != "tool.call" {
		t.Fatalf("items=%+v total=%d err=%v", items, total, err)
	}
	if err := store.PruneAudit(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	items, total, err = store.ListAudit(ctx, AuditFilter{})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("after prune items=%+v total=%d err=%v", items, total, err)
	}
}
