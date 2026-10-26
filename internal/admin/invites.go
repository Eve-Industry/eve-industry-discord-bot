package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/auth"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

// ErrAlreadyAdmin is returned when inviting an email that already belongs
// to an admin. Reset them instead.
var ErrAlreadyAdmin = errors.New("already an admin")

// NormalizeEmail trims email and checks it looks like an address.
func NormalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", fmt.Errorf("%q is not an email address", email)
	}
	return email, nil
}

// Invite stores a 24-hour invite for email and returns the link to send
// them. The token is in the link's fragment, so it never reaches server or
// proxy logs.
func Invite(ctx context.Context, st *store.Store, baseURL, email, by string, now time.Time) (string, time.Time, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err := st.GetAdminByEmail(ctx, email); err == nil {
		return "", time.Time{}, ErrAlreadyAdmin
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, err
	}
	token, inv := newInvite(email, by, now)
	if err := st.PutInvite(ctx, inv); err != nil {
		return "", time.Time{}, err
	}
	if err := st.AddAudit(ctx, store.AuditEntry{At: now, Actor: by, Action: "admin.invited", Detail: email}); err != nil {
		return "", time.Time{}, err
	}
	return inviteLink(baseURL, token), inv.ExpiresAt, nil
}

// Reset clears the admin's password, authenticator and sessions and returns
// a new invite link for them to set both up again.
func Reset(ctx context.Context, st *store.Store, baseURL string, a store.Admin, by string, now time.Time) (string, time.Time, error) {
	token, inv := newInvite(a.Email, by, now)
	if err := st.ResetAdmin(ctx, a.ID, inv); err != nil {
		return "", time.Time{}, err
	}
	if err := st.AddAudit(ctx, store.AuditEntry{At: now, Actor: by, Action: "admin.reset", Detail: a.Email}); err != nil {
		return "", time.Time{}, err
	}
	return inviteLink(baseURL, token), inv.ExpiresAt, nil
}

// Bootstrap invites cfg.BootstrapEmail and logs the link, but only while
// nobody is an admin or invited yet.
func Bootstrap(ctx context.Context, st *store.Store, cfg Config, logger *slog.Logger) error {
	if cfg.BootstrapEmail == "" {
		return nil
	}
	now := time.Now()
	admins, err := st.CountAdmins(ctx)
	if err != nil {
		return err
	}
	invites, err := st.CountInvites(ctx, now)
	if err != nil {
		return err
	}
	if admins > 0 || invites > 0 {
		return nil
	}
	link, expires, err := Invite(ctx, st, cfg.BaseURL(), cfg.BootstrapEmail, "ADMIN_BOOTSTRAP_EMAIL", now)
	if err != nil {
		return fmt.Errorf("invite first admin: %w", err)
	}
	logger.Info("first admin invited; open this link to set a password and authenticator",
		"email", cfg.BootstrapEmail, "link", link, "expires", expires.Format(time.RFC3339))
	return nil
}

func newInvite(email, by string, now time.Time) (string, store.Invite) {
	token := auth.NewToken()
	return token, store.Invite{
		TokenHash: auth.HashToken(token),
		Email:     email,
		CreatedBy: by,
		ExpiresAt: now.Add(inviteLifetime),
	}
}

func inviteLink(baseURL, token string) string {
	return strings.TrimRight(baseURL, "/") + "/invite#" + token
}
