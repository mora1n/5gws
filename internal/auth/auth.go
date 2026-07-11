package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var ErrUnauthorized = errors.New("invalid credentials or session")

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Manager struct {
	db  *sql.DB
	ttl time.Duration
}

func New(db *sql.DB, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Manager{db: db, ttl: ttl}
}

func (m *Manager) NeedsBootstrap(ctx context.Context) (bool, error) {
	var count int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM panel_users`).Scan(&count)
	return count == 0, err
}

func (m *Manager) Bootstrap(ctx context.Context, username, password string) (User, error) {
	if len(username) < 3 || len(username) > 64 {
		return User{}, errors.New("username must contain 3 to 64 characters")
	}
	if len(password) < 12 {
		return User{}, errors.New("password must contain at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return User{}, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM panel_users`).Scan(&count); err != nil {
		return User{}, err
	}
	if count != 0 {
		return User{}, errors.New("bootstrap has already been claimed")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO panel_users(username, password_hash) VALUES(?, ?)`, username, hash)
	if err != nil {
		return User{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username}, tx.Commit()
}

func (m *Manager) Login(ctx context.Context, username, password string) (User, string, time.Time, error) {
	var user User
	var hash string
	err := m.db.QueryRowContext(ctx, `SELECT id, username, password_hash FROM panel_users WHERE username = ?`, username).Scan(&user.ID, &user.Username, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, "", time.Time{}, ErrUnauthorized
	}
	token, tokenHash, err := newToken()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	expires := time.Now().UTC().Add(m.ttl)
	if _, err := m.db.ExecContext(ctx, `INSERT INTO panel_sessions(token_hash, user_id, expires_at) VALUES(?, ?, ?)`, tokenHash, user.ID, expires.Format(time.RFC3339)); err != nil {
		return User{}, "", time.Time{}, err
	}
	_, _ = m.db.ExecContext(ctx, `UPDATE panel_users SET last_login = CURRENT_TIMESTAMP WHERE id = ?`, user.ID)
	return user, token, expires, nil
}

func (m *Manager) Verify(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrUnauthorized
	}
	hash := hashToken(token)
	var user User
	var expires string
	err := m.db.QueryRowContext(ctx, `SELECT u.id, u.username, s.expires_at
		FROM panel_sessions s JOIN panel_users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.revoked_at IS NULL`, hash).Scan(&user.ID, &user.Username, &expires)
	if err != nil {
		return User{}, ErrUnauthorized
	}
	deadline, err := time.Parse(time.RFC3339, expires)
	if err != nil || !deadline.After(time.Now().UTC()) {
		return User{}, ErrUnauthorized
	}
	return user, nil
}

func (m *Manager) Logout(ctx context.Context, token string) error {
	result, err := m.db.ExecContext(ctx, `UPDATE panel_sessions SET revoked_at = CURRENT_TIMESTAMP WHERE token_hash = ? AND revoked_at IS NULL`, hashToken(token))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrUnauthorized
	}
	return nil
}

func (m *Manager) ChangePassword(ctx context.Context, userID int64, current, next string) error {
	if len(next) < 12 {
		return errors.New("new password must contain at least 12 characters")
	}
	var hash string
	if err := m.db.QueryRowContext(ctx, `SELECT password_hash FROM panel_users WHERE id = ?`, userID).Scan(&hash); err != nil {
		return ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return ErrUnauthorized
	}
	nextHash, err := bcrypt.GenerateFromPassword([]byte(next), 12)
	if err != nil {
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE panel_users SET password_hash = ? WHERE id = ?`, nextHash, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE panel_sessions SET revoked_at = CURRENT_TIMESTAMP WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func newToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
