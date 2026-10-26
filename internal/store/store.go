// Package store keeps the bot's data in a single SQLite file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound is returned when a lookup matches nothing.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a write was based on data that has since
// changed.
var ErrConflict = errors.New("changed since it was read")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// FAQ is one stored answer.
type FAQ struct {
	Name      string
	Answer    string
	UpdatedBy string
	UpdatedAt time.Time
}

// Settings is the stored copy of the bot's settings, as JSON. Version goes
// up by one on every save.
type Settings struct {
	Data      []byte
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
}

// AuditEntry records one change made to the bot.
type AuditEntry struct {
	ID     int64
	At     time.Time
	Actor  string
	Action string
	Detail string
}

const schema = `
CREATE TABLE IF NOT EXISTS faqs (
	name       TEXT PRIMARY KEY COLLATE NOCASE,
	answer     TEXT NOT NULL,
	updated_by TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	data       TEXT NOT NULL,
	version    INTEGER NOT NULL,
	updated_by TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	at     INTEGER NOT NULL,
	actor  TEXT NOT NULL,
	action TEXT NOT NULL,
	detail TEXT NOT NULL
);`

// Open opens (or creates) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// SetFAQ creates or replaces the answer for name.
func (s *Store) SetFAQ(ctx context.Context, f FAQ) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO faqs (name, answer, updated_by, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			name = excluded.name, answer = excluded.answer,
			updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		f.Name, f.Answer, f.UpdatedBy, f.UpdatedAt.Unix())
	return err
}

// GetFAQ returns the answer for name, ignoring case.
func (s *Store) GetFAQ(ctx context.Context, name string) (FAQ, error) {
	var f FAQ
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT name, answer, updated_by, updated_at FROM faqs WHERE name = ?`, name).
		Scan(&f.Name, &f.Answer, &f.UpdatedBy, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	f.UpdatedAt = time.Unix(updated, 0)
	return f, err
}

// DeleteFAQ removes the answer for name.
func (s *Store) DeleteFAQ(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM faqs WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SearchFAQNames returns up to limit names containing query, alphabetically.
func (s *Store) SearchFAQNames(ctx context.Context, query string, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM faqs WHERE name LIKE '%' || ? || '%' ESCAPE '\' ORDER BY name LIMIT ?`,
		escapeLike(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// GetSettings returns the stored settings, or ErrNotFound before the first
// save.
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	var st Settings
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT data, version, updated_by, updated_at FROM settings WHERE id = 1`).
		Scan(&st.Data, &st.Version, &st.UpdatedBy, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return st, ErrNotFound
	}
	st.UpdatedAt = time.Unix(updated, 0)
	return st, err
}

// SaveSettings stores data if the saved version is still version (0 for the
// first save) and records entry in the audit log, both or neither. It
// returns the new version, or ErrConflict if someone saved in between.
func (s *Store) SaveSettings(ctx context.Context, data []byte, version int64, entry AuditEntry) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var res sql.Result
	if version == 0 {
		res, err = tx.ExecContext(ctx, `
			INSERT INTO settings (id, data, version, updated_by, updated_at) VALUES (1, ?, 1, ?, ?)
			ON CONFLICT(id) DO NOTHING`,
			data, entry.Actor, entry.At.Unix())
	} else {
		res, err = tx.ExecContext(ctx, `
			UPDATE settings SET data = ?, version = version + 1, updated_by = ?, updated_at = ?
			WHERE id = 1 AND version = ?`,
			data, entry.Actor, entry.At.Unix(), version)
	}
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrConflict
	}
	if err := addAudit(ctx, tx, entry); err != nil {
		return 0, err
	}
	return version + 1, tx.Commit()
}

// AddAudit records entry in the audit log.
func (s *Store) AddAudit(ctx context.Context, entry AuditEntry) error {
	return addAudit(ctx, s.db, entry)
}

func addAudit(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, e AuditEntry) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO audit_log (at, actor, action, detail) VALUES (?, ?, ?, ?)`,
		e.At.Unix(), e.Actor, e.Action, e.Detail)
	return err
}

// ListAudit returns up to limit audit entries, newest first.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, actor, action, detail FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '%' || r == '_' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
