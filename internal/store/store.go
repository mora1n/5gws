package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

var ErrNotInitialized = errors.New("5gws database is not initialized")

type Bundle struct {
	Config        config.Config `json:"config" toml:"config"`
	Rules         rules.File    `json:"rules" toml:"rules"`
	ResolvedRules []rules.Rule  `json:"resolved_rules,omitempty" toml:"-"`
}

func (b Bundle) Normalized() rules.Normalized {
	return rules.Normalized{Rules: b.ResolvedRules}
}

type Revision struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Bundle    Bundle    `json:"bundle"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ActiveAt  time.Time `json:"active_at,omitempty"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) GetImportCache(ctx context.Context, url string) (rules.CacheEntry, bool, error) {
	var entry rules.CacheEntry
	err := s.db.QueryRowContext(ctx, `SELECT url, COALESCE(etag, ''), COALESCE(last_modified, ''), sha256, content FROM import_cache WHERE url = ?`, url).
		Scan(&entry.URL, &entry.ETag, &entry.LastModified, &entry.SHA256, &entry.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return rules.CacheEntry{}, false, nil
	}
	return entry, err == nil, err
}

func (s *Store) PutImportCache(ctx context.Context, entry rules.CacheEntry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO import_cache(url, etag, last_modified, sha256, content, fetched_at)
		VALUES(?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(url) DO UPDATE SET etag=excluded.etag, last_modified=excluded.last_modified,
		sha256=excluded.sha256, content=excluded.content, fetched_at=CURRENT_TIMESTAMP`,
		entry.URL, entry.ETag, entry.LastModified, entry.SHA256, entry.Content)
	return err
}

func (s *Store) PutMetric(ctx context.Context, timestamp int64, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO metrics(recorded_at, payload_json) VALUES(?, ?)`, timestamp, payload); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM metrics WHERE recorded_at < ?`, time.Now().Add(-24*time.Hour).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Metrics(ctx context.Context, limit int) ([]json.RawMessage, error) {
	if limit <= 0 || limit > 1000 {
		limit = 360
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM metrics ORDER BY recorded_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var payload json.RawMessage
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		out = append(out, payload)
	}
	return out, rows.Err()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) Initialize(ctx context.Context, bundle Bundle) (Revision, error) {
	bundle.Config.ApplyDefaults()
	if err := bundle.Config.Validate(); err != nil {
		return Revision{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM revisions").Scan(&count); err != nil {
		return Revision{}, err
	}
	if count != 0 {
		return Revision{}, errors.New("5gws database is already initialized")
	}
	rev, err := insertRevision(ctx, tx, "active", bundle, "")
	if err != nil {
		return Revision{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_state(id, active_revision_id, draft_revision_id) VALUES(1, ?, ?)`, rev.ID, rev.ID); err != nil {
		return Revision{}, err
	}
	if err := tx.Commit(); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

func (s *Store) Draft(ctx context.Context) (Revision, error) {
	return s.revisionFor(ctx, "draft_revision_id")
}

func (s *Store) Active(ctx context.Context) (Revision, error) {
	return s.revisionFor(ctx, "active_revision_id")
}

func (s *Store) revisionFor(ctx context.Context, column string) (Revision, error) {
	query := `SELECT r.id, r.status, r.payload_json, COALESCE(r.error, ''), r.created_at,
	                 COALESCE(r.active_at, '')
	          FROM app_state s JOIN revisions r ON r.id = s.` + column + ` WHERE s.id = 1`
	return scanRevision(s.db.QueryRowContext(ctx, query))
}

func (s *Store) SaveDraft(ctx context.Context, bundle Bundle) (Revision, error) {
	bundle.Config.ApplyDefaults()
	if err := bundle.Config.Validate(); err != nil {
		return Revision{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback()
	rev, err := insertRevision(ctx, tx, "draft", bundle, "")
	if err != nil {
		return Revision{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE app_state SET draft_revision_id = ? WHERE id = 1`, rev.ID)
	if err != nil {
		return Revision{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return Revision{}, ErrNotInitialized
	}
	return rev, tx.Commit()
}

func (s *Store) Activate(ctx context.Context, revisionID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE revisions SET status = 'superseded' WHERE status = 'active'`); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE revisions SET status = 'active', active_at = CURRENT_TIMESTAMP, error = NULL WHERE id = ?`, revisionID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("revision %d not found", revisionID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_state SET active_revision_id = ?, draft_revision_id = ? WHERE id = 1`, revisionID, revisionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Fail(ctx context.Context, revisionID int64, cause error) error {
	_, err := s.db.ExecContext(ctx, `UPDATE revisions SET status = 'failed', error = ? WHERE id = ?`, cause.Error(), revisionID)
	return err
}

func (s *Store) DraftFromRevision(ctx context.Context, revisionID int64) (Revision, error) {
	rev, err := scanRevision(s.db.QueryRowContext(ctx, revisionSelect+` WHERE id = ?`, revisionID))
	if err != nil {
		return Revision{}, err
	}
	return s.SaveDraft(ctx, rev.Bundle)
}

func (s *Store) Revisions(ctx context.Context, limit int) ([]Revision, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, revisionSelect+` ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		rev, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(...any) error
}

func insertRevision(ctx context.Context, tx *sql.Tx, status string, bundle Bundle, message string) (Revision, error) {
	payload, err := json.Marshal(bundle)
	if err != nil {
		return Revision{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO revisions(status, payload_json, error) VALUES(?, ?, NULLIF(?, ''))`, status, payload, message)
	if err != nil {
		return Revision{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Revision{}, err
	}
	return scanRevision(tx.QueryRowContext(ctx, revisionSelect+` WHERE id = ?`, id))
}

func scanRevision(row scanner) (Revision, error) {
	var rev Revision
	var payload []byte
	var created, active string
	if err := row.Scan(&rev.ID, &rev.Status, &payload, &rev.Error, &created, &active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Revision{}, ErrNotInitialized
		}
		return Revision{}, err
	}
	if err := json.Unmarshal(payload, &rev.Bundle); err != nil {
		return Revision{}, fmt.Errorf("decode revision %d: %w", rev.ID, err)
	}
	rev.Bundle.Config.ApplyDefaults()
	rev.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
	rev.ActiveAt, _ = time.Parse("2006-01-02 15:04:05", active)
	return rev, nil
}

const revisionSelect = `SELECT id, status, payload_json, COALESCE(error, ''), created_at, COALESCE(active_at, '') FROM revisions`

const schema = `
CREATE TABLE IF NOT EXISTS revisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  status TEXT NOT NULL CHECK(status IN ('draft','active','superseded','failed')),
  payload_json BLOB NOT NULL,
  error TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  active_at TEXT
);
CREATE TABLE IF NOT EXISTS app_state (
  id INTEGER PRIMARY KEY CHECK(id = 1),
  active_revision_id INTEGER NOT NULL REFERENCES revisions(id),
  draft_revision_id INTEGER NOT NULL REFERENCES revisions(id)
);
CREATE TABLE IF NOT EXISTS panel_users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_login TEXT
);
CREATE TABLE IF NOT EXISTS panel_sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES panel_users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at TEXT
);
CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  result TEXT NOT NULL,
  detail TEXT
);
CREATE TABLE IF NOT EXISTS metrics (
  recorded_at INTEGER PRIMARY KEY,
  payload_json BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS import_cache (
  url TEXT PRIMARY KEY,
  etag TEXT,
  last_modified TEXT,
  sha256 TEXT NOT NULL,
  content BLOB NOT NULL,
  fetched_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`
