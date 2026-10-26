package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Session states. A session starts in SessionPending2FA (or SessionSetup2FA
// when the admin has no authenticator yet) and only reaches SessionFull after
// a second factor.
const (
	SessionPending2FA = "pending_2fa"
	SessionSetup2FA   = "setup_2fa"
	SessionFull       = "full"
)

// Admin is someone who can sign in to the dashboard.
type Admin struct {
	ID           int64
	Email        string
	PasswordHash string // empty until an invite is accepted
	TOTPSecret   []byte // encrypted; nil until the authenticator is set up
	TOTPPending  []byte // encrypted; set during setup, before the first code
	TOTPLastStep int64  // last 30-second step used, so a code works once
	Locked       bool
	CreatedAt    time.Time
}

// Invite lets someone set a password and authenticator for Email. Only a
// hash of its token is stored.
type Invite struct {
	TokenHash string
	Email     string
	CreatedBy string
	ExpiresAt time.Time
}

// Session is a signed-in browser. Only a hash of its cookie is stored.
type Session struct {
	IDHash     string
	AdminID    int64
	State      string
	CSRF       string
	CreatedAt  time.Time
	LastSeen   time.Time
	VerifiedAt time.Time // last time a second factor was passed
	Failed2FA  int
}

const adminSchema = `
CREATE TABLE IF NOT EXISTS admins (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	email          TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash  TEXT NOT NULL DEFAULT '',
	totp_secret    BLOB,
	totp_pending   BLOB,
	totp_last_step INTEGER NOT NULL DEFAULT 0,
	locked         INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_recovery_codes (
	admin_id  INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
	code_hash TEXT NOT NULL,
	PRIMARY KEY (admin_id, code_hash)
);

CREATE TABLE IF NOT EXISTS admin_invites (
	token_hash TEXT PRIMARY KEY,
	email      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	created_by TEXT NOT NULL,
	expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_sessions (
	id_hash     TEXT PRIMARY KEY,
	admin_id    INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
	state       TEXT NOT NULL,
	csrf        TEXT NOT NULL,
	created_at  INTEGER NOT NULL,
	last_seen   INTEGER NOT NULL,
	verified_at INTEGER NOT NULL DEFAULT 0,
	failed_2fa  INTEGER NOT NULL DEFAULT 0
);`

const adminColumns = `id, email, password_hash, totp_secret, totp_pending, totp_last_step, locked, created_at`

func scanAdmin(row interface{ Scan(...any) error }) (Admin, error) {
	var a Admin
	var created int64
	err := row.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.TOTPSecret, &a.TOTPPending, &a.TOTPLastStep, &a.Locked, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.CreatedAt = time.Unix(created, 0)
	return a, err
}

// CreateAdmin adds an admin with no password or authenticator yet. It
// returns ErrConflict if the email is taken.
func (s *Store) CreateAdmin(ctx context.Context, email string, at time.Time) (Admin, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO admins (email, created_at) VALUES (?, ?) ON CONFLICT(email) DO NOTHING`, email, at.Unix())
	if err != nil {
		return Admin{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Admin{}, ErrConflict
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Admin{}, err
	}
	return s.GetAdmin(ctx, id)
}

// GetAdmin returns the admin with id.
func (s *Store) GetAdmin(ctx context.Context, id int64) (Admin, error) {
	return scanAdmin(s.db.QueryRowContext(ctx, `SELECT `+adminColumns+` FROM admins WHERE id = ?`, id))
}

// GetAdminByEmail returns the admin with email, ignoring case.
func (s *Store) GetAdminByEmail(ctx context.Context, email string) (Admin, error) {
	return scanAdmin(s.db.QueryRowContext(ctx, `SELECT `+adminColumns+` FROM admins WHERE email = ?`, email))
}

// ListAdmins returns every admin, oldest first.
func (s *Store) ListAdmins(ctx context.Context) ([]Admin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+adminColumns+` FROM admins ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Admin
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAdmins returns how many admins exist.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

// SetPassword stores a new password hash for the admin.
func (s *Store) SetPassword(ctx context.Context, id int64, hash string) error {
	return s.updateAdmin(ctx, `UPDATE admins SET password_hash = ? WHERE id = ?`, hash, id)
}

// SetPendingTOTP stores an encrypted authenticator secret that is waiting
// for its first code.
func (s *Store) SetPendingTOTP(ctx context.Context, id int64, secret []byte) error {
	return s.updateAdmin(ctx, `UPDATE admins SET totp_pending = ? WHERE id = ?`, secret, id)
}

// ConfirmTOTP makes the pending secret the admin's authenticator, records
// step as used, and replaces their recovery codes with codeHashes.
func (s *Store) ConfirmTOTP(ctx context.Context, id, step int64, codeHashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE admins SET totp_secret = totp_pending, totp_pending = NULL, totp_last_step = ?
		WHERE id = ? AND totp_pending IS NOT NULL`, step, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	if err := replaceRecoveryCodes(ctx, tx, id, codeHashes); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceRecoveryCodes swaps the admin's recovery codes for codeHashes.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, id int64, codeHashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceRecoveryCodes(ctx, tx, id, codeHashes); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceRecoveryCodes(ctx context.Context, tx *sql.Tx, id int64, codeHashes []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_recovery_codes WHERE admin_id = ?`, id); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO admin_recovery_codes (admin_id, code_hash) VALUES (?, ?)`, id, h); err != nil {
			return err
		}
	}
	return nil
}

// UseTOTPStep marks step as used for the admin. It reports false if that
// step or a later one was already used, so each code works only once.
func (s *Store) UseTOTPStep(ctx context.Context, id, step int64) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE admins SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, id, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// UseRecoveryCode deletes the code if the admin has it, and reports whether
// they did.
func (s *Store) UseRecoveryCode(ctx context.Context, id int64, codeHash string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_recovery_codes WHERE admin_id = ? AND code_hash = ?`, id, codeHash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RecoveryCodesLeft returns how many unused recovery codes the admin has.
func (s *Store) RecoveryCodesLeft(ctx context.Context, id int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM admin_recovery_codes WHERE admin_id = ?`, id).Scan(&n)
	return n, err
}

// LockAdmin stops the admin signing in and ends their sessions. Only a reset
// undoes it.
func (s *Store) LockAdmin(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE admins SET locked = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE admin_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetAdmin clears the admin's password, authenticator, recovery codes,
// lock and sessions, and stores inv so they can set them up again.
func (s *Store) ResetAdmin(ctx context.Context, id int64, inv Invite) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`UPDATE admins SET password_hash = '', totp_secret = NULL, totp_pending = NULL, locked = 0 WHERE id = ?`,
		`DELETE FROM admin_recovery_codes WHERE admin_id = ?`,
		`DELETE FROM admin_sessions WHERE admin_id = ?`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	if err := putInvite(ctx, tx, inv); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteAdmin removes the admin, their sessions, codes and any invite.
func (s *Store) DeleteAdmin(ctx context.Context, id int64) error {
	a, err := s.GetAdmin(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_invites WHERE email = ?`, a.Email); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admins WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) updateAdmin(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PutInvite stores inv, replacing any earlier invite for the same email.
func (s *Store) PutInvite(ctx context.Context, inv Invite) error {
	return putInvite(ctx, s.db, inv)
}

func putInvite(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, inv Invite) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO admin_invites (token_hash, email, created_by, expires_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET
			token_hash = excluded.token_hash, created_by = excluded.created_by, expires_at = excluded.expires_at`,
		inv.TokenHash, strings.TrimSpace(inv.Email), inv.CreatedBy, inv.ExpiresAt.Unix())
	return err
}

// GetInvite returns the invite whose token hashes to tokenHash. Expired
// invites count as not found.
func (s *Store) GetInvite(ctx context.Context, tokenHash string, now time.Time) (Invite, error) {
	var inv Invite
	var expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT token_hash, email, created_by, expires_at FROM admin_invites WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, now.Unix()).Scan(&inv.TokenHash, &inv.Email, &inv.CreatedBy, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return inv, ErrNotFound
	}
	inv.ExpiresAt = time.Unix(expires, 0)
	return inv, err
}

// CountInvites returns how many unexpired invites exist.
func (s *Store) CountInvites(ctx context.Context, now time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_invites WHERE expires_at > ?`, now.Unix()).Scan(&n)
	return n, err
}

// AcceptInvite uses up the invite: it creates the admin if needed, sets
// their password and opens session sess for them, all or nothing. It returns
// ErrNotFound if the invite was already used or has expired.
func (s *Store) AcceptInvite(ctx context.Context, tokenHash, passwordHash string, sess Session, now time.Time) (Admin, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Admin{}, err
	}
	defer tx.Rollback()

	var email string
	err = tx.QueryRowContext(ctx,
		`DELETE FROM admin_invites WHERE token_hash = ? AND expires_at > ? RETURNING email`,
		tokenHash, now.Unix()).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, ErrNotFound
	}
	if err != nil {
		return Admin{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO admins (email, created_at) VALUES (?, ?) ON CONFLICT(email) DO NOTHING`, email, now.Unix()); err != nil {
		return Admin{}, err
	}
	a, err := scanAdmin(tx.QueryRowContext(ctx, `SELECT `+adminColumns+` FROM admins WHERE email = ?`, email))
	if err != nil {
		return Admin{}, err
	}
	// A new password means a new authenticator too, as with a reset.
	if _, err := tx.ExecContext(ctx, `
		UPDATE admins SET password_hash = ?, totp_secret = NULL, totp_pending = NULL, locked = 0 WHERE id = ?`,
		passwordHash, a.ID); err != nil {
		return Admin{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE admin_id = ?`, a.ID); err != nil {
		return Admin{}, err
	}
	sess.AdminID = a.ID
	if err := insertSession(ctx, tx, sess); err != nil {
		return Admin{}, err
	}
	if err := tx.Commit(); err != nil {
		return Admin{}, err
	}
	return s.GetAdmin(ctx, a.ID)
}

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	return insertSession(ctx, s.db, sess)
}

func insertSession(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, sess Session) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO admin_sessions (id_hash, admin_id, state, csrf, created_at, last_seen, verified_at, failed_2fa)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		sess.IDHash, sess.AdminID, sess.State, sess.CSRF,
		sess.CreatedAt.Unix(), sess.LastSeen.Unix(), unixOrZero(sess.VerifiedAt))
	return err
}

// GetSession returns the session whose cookie hashes to idHash.
func (s *Store) GetSession(ctx context.Context, idHash string) (Session, error) {
	var sess Session
	var created, seen, verified int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id_hash, admin_id, state, csrf, created_at, last_seen, verified_at, failed_2fa
		FROM admin_sessions WHERE id_hash = ?`, idHash).
		Scan(&sess.IDHash, &sess.AdminID, &sess.State, &sess.CSRF, &created, &seen, &verified, &sess.Failed2FA)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, ErrNotFound
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.LastSeen = time.Unix(seen, 0)
	if verified != 0 {
		sess.VerifiedAt = time.Unix(verified, 0)
	}
	return sess, err
}

// TouchSession records activity on the session.
func (s *Store) TouchSession(ctx context.Context, idHash string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE admin_sessions SET last_seen = ? WHERE id_hash = ?`, at.Unix(), idHash)
	return err
}

// RotateSession replaces the session oldHash with next, keeping its creation
// time, so a cookie captured before a second factor is useless after it.
func (s *Store) RotateSession(ctx context.Context, oldHash string, next Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE id_hash = ?`, oldHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := insertSession(ctx, tx, next); err != nil {
		return err
	}
	return tx.Commit()
}

// AddSessionFailure counts a failed second factor on the session and returns
// the new count.
func (s *Store) AddSessionFailure(ctx context.Context, idHash string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`UPDATE admin_sessions SET failed_2fa = failed_2fa + 1 WHERE id_hash = ? RETURNING failed_2fa`, idHash).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return n, err
}

// DeleteSession ends one session.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteOtherSessions ends every session of the admin except keepHash.
func (s *Store) DeleteOtherSessions(ctx context.Context, adminID int64, keepHash string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_sessions WHERE admin_id = ? AND id_hash != ?`, adminID, keepHash)
	return err
}

// DeleteExpiredSessions removes sessions idle since before idle or created
// before created.
func (s *Store) DeleteExpiredSessions(ctx context.Context, idle, created time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_sessions WHERE last_seen < ? OR created_at < ?`, idle.Unix(), created.Unix())
	return err
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
