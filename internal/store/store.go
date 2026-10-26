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

const schema = `
CREATE TABLE IF NOT EXISTS faqs (
	name       TEXT PRIMARY KEY COLLATE NOCASE,
	answer     TEXT NOT NULL,
	updated_by TEXT NOT NULL,
	updated_at INTEGER NOT NULL
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
