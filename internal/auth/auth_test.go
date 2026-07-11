package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/morain/5gws/internal/store"
)

func TestSessionLifecycle(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	m := New(state.DB(), time.Hour)
	ctx := context.Background()
	user, err := m.ResetAdmin(ctx, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	logged, token, _, err := m.Login(ctx, "admin", "correct-horse-battery")
	if err != nil || logged.ID != user.ID {
		t.Fatalf("login: %+v %v", logged, err)
	}
	if _, err := m.Verify(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err := m.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(ctx, token); err == nil {
		t.Fatal("revoked token verified")
	}
}

func TestResetAdminCreatesAndRevokesSessions(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	m := New(state.DB(), time.Hour)
	ctx := context.Background()
	user, err := m.ResetAdmin(ctx, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" {
		t.Fatalf("username = %q", user.Username)
	}
	_, token, _, err := m.Login(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResetAdmin(ctx, "new-correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(ctx, token); err == nil {
		t.Fatal("old admin session survived reset")
	}
	if _, _, _, err := m.Login(ctx, "admin", "new-correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratePasswordMeetsMinimumLength(t *testing.T) {
	password, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(password) < 12 {
		t.Fatalf("password length = %d", len(password))
	}
}
