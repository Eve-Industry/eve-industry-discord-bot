package admin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/auth"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

const recoveryCodeCount = 10

// stateJSON tells the dashboard where the sign-in stands.
type stateJSON struct {
	State string `json:"state"`
	CSRF  string `json:"csrf"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	now := s.now()
	ip := s.clientIP(r)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if len(email) > 254 {
		email = email[:254]
	}

	for _, check := range []struct {
		l   *limiter
		key string
	}{{s.loginByIP, ip}, {s.loginByEmail, email}} {
		if blocked, wait := check.l.blocked(check.key, now); blocked {
			tooMany(w, wait)
			return
		}
	}

	a, err := s.store.GetAdminByEmail(ctx, email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, "load admin", err)
		return
	}
	// A missing admin is checked against a dummy hash, so both failures take
	// the same time and get the same message.
	if !auth.CheckPassword(a.PasswordHash, req.Password) {
		s.loginByIP.fail(ip, now)
		s.loginByEmail.fail(email, now)
		s.record(ctx, email, "auth.login_failed", "from "+ip)
		writeError(w, http.StatusUnauthorized, "bad_credentials", "Wrong email or password.")
		return
	}
	s.loginByEmail.reset(email)
	if a.Locked {
		writeError(w, http.StatusForbidden, "locked", "This account is locked after too many wrong codes. Ask another admin to reset it.")
		return
	}

	state := store.SessionPending2FA
	if a.TOTPSecret == nil {
		state = store.SessionSetup2FA
	}
	sess, err := s.startSession(w, r, a.ID, state)
	if err != nil {
		s.internalError(w, "start session", err)
		return
	}
	s.record(ctx, a.Email, "auth.password_ok", "from "+ip)
	writeJSON(w, http.StatusOK, stateJSON{State: sess.State, CSRF: sess.CSRF})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSession(r.Context(), sessionFrom(r).IDHash); err != nil {
		s.internalError(w, "end session", err)
		return
	}
	clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// verify checks an authenticator or recovery code. It finishes sign-in, and
// for a signed-in admin it refreshes the step-up window.
func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	a := adminFrom(r)

	var ok bool
	var method string
	if req.RecoveryCode != "" {
		method = "recovery code"
		used, err := s.store.UseRecoveryCode(ctx, a.ID, auth.HashRecoveryCode(req.RecoveryCode))
		if err != nil {
			s.internalError(w, "use recovery code", err)
			return
		}
		ok = used
	} else {
		method = "authenticator"
		secret, err := s.box.Open(a.TOTPSecret)
		if err != nil {
			s.internalError(w, "decrypt authenticator secret", err)
			return
		}
		if step, match := auth.MatchTOTP(string(secret), req.Code, s.now()); match {
			fresh, err := s.store.UseTOTPStep(ctx, a.ID, step)
			if err != nil {
				s.internalError(w, "record authenticator code", err)
				return
			}
			ok = fresh
		}
	}
	if !ok {
		s.codeFailed(w, r)
		return
	}

	s.codeByAdmin.reset(strconv.FormatInt(a.ID, 10))
	sess, err := s.rotate(w, r)
	if err != nil {
		s.internalError(w, "rotate session", err)
		return
	}
	left, err := s.store.RecoveryCodesLeft(ctx, a.ID)
	if err != nil {
		s.internalError(w, "count recovery codes", err)
		return
	}
	s.record(ctx, a.Email, "auth.second_factor", "with "+method)
	writeJSON(w, http.StatusOK, struct {
		stateJSON
		RecoveryCodesLeft int `json:"recovery_codes_left"`
	}{stateJSON{sess.State, sess.CSRF}, left})
}

// codeFailed counts a wrong code. Too many on one session ends it; too many
// for one admin within an hour locks the account until someone resets it.
func (s *Server) codeFailed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := adminFrom(r)
	sess := sessionFrom(r)
	s.record(ctx, a.Email, "auth.second_factor_failed", "from "+s.clientIP(r))

	if s.codeByAdmin.fail(strconv.FormatInt(a.ID, 10), s.now()) >= maxAdminFailures {
		if err := s.store.LockAdmin(ctx, a.ID); err != nil {
			s.internalError(w, "lock admin", err)
			return
		}
		s.record(ctx, a.Email, "auth.locked", fmt.Sprintf("%d wrong codes within an hour", maxAdminFailures))
		clearCookie(w)
		writeError(w, http.StatusForbidden, "locked", "Too many wrong codes. This account is locked; ask another admin to reset it.")
		return
	}
	n, err := s.store.AddSessionFailure(ctx, sess.IDHash)
	if err != nil {
		s.internalError(w, "count failed code", err)
		return
	}
	if n >= maxSessionFailures {
		_ = s.store.DeleteSession(ctx, sess.IDHash)
		clearCookie(w)
		writeError(w, http.StatusUnauthorized, "unauthorized", "Too many wrong codes. Sign in again.")
		return
	}
	writeError(w, http.StatusUnauthorized, "wrong_code", "That code didn't work. Check your authenticator app and try again.")
}

func (s *Server) checkInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	inv, err := s.store.GetInvite(r.Context(), auth.HashToken(req.Token), s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "invite_invalid", "This invite link is invalid or has expired. Ask for a new one.")
		return
	}
	if err != nil {
		s.internalError(w, "load invite", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": inv.Email, "expires_at": inv.ExpiresAt.UTC()})
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := auth.CheckPasswordPolicy(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", passwordAdvice(err))
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internalError(w, "hash password", err)
		return
	}
	token, sess := s.newSession(0, store.SessionSetup2FA)
	a, err := s.store.AcceptInvite(r.Context(), auth.HashToken(req.Token), hash, sess, s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "invite_invalid", "This invite link is invalid or has expired. Ask for a new one.")
		return
	}
	if err != nil {
		s.internalError(w, "accept invite", err)
		return
	}
	setCookie(w, token)
	s.record(r.Context(), a.Email, "auth.invite_accepted", "password set")
	writeJSON(w, http.StatusOK, stateJSON{State: sess.State, CSRF: sess.CSRF})
}

func (s *Server) setupTOTP(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	setup, err := auth.NewTOTP(a.Email)
	if err != nil {
		s.internalError(w, "create authenticator secret", err)
		return
	}
	if err := s.store.SetPendingTOTP(r.Context(), a.ID, s.box.Seal([]byte(setup.Secret))); err != nil {
		s.internalError(w, "store authenticator secret", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret":  setup.Secret,
		"uri":     setup.URI,
		"qr_code": "data:image/png;base64," + base64.StdEncoding.EncodeToString(setup.QRCode),
	})
}

func (s *Server) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	a := adminFrom(r)
	if a.TOTPPending == nil {
		writeError(w, http.StatusConflict, "totp_setup_required", "Start authenticator setup first.")
		return
	}
	secret, err := s.box.Open(a.TOTPPending)
	if err != nil {
		s.internalError(w, "decrypt authenticator secret", err)
		return
	}
	step, ok := auth.MatchTOTP(string(secret), req.Code, s.now())
	if !ok {
		s.codeFailed(w, r)
		return
	}
	codes := auth.NewRecoveryCodes(recoveryCodeCount)
	if err := s.store.ConfirmTOTP(ctx, a.ID, step, hashCodes(codes)); err != nil {
		s.internalError(w, "confirm authenticator", err)
		return
	}
	sess, err := s.rotate(w, r)
	if err != nil {
		s.internalError(w, "rotate session", err)
		return
	}
	s.record(ctx, a.Email, "auth.totp_enabled", "")
	writeJSON(w, http.StatusOK, struct {
		stateJSON
		RecoveryCodes []string `json:"recovery_codes"`
	}{stateJSON{sess.State, sess.CSRF}, codes})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	sess := sessionFrom(r)
	out := map[string]any{
		"id":    a.ID,
		"email": a.Email,
		"state": sess.State,
		"csrf":  sess.CSRF,
	}
	if sess.State == store.SessionFull {
		left, err := s.store.RecoveryCodesLeft(r.Context(), a.ID)
		if err != nil {
			s.internalError(w, "count recovery codes", err)
			return
		}
		out["recovery_codes_left"] = left
		if !sess.VerifiedAt.IsZero() {
			out["step_up_until"] = sess.VerifiedAt.Add(stepUpWindow).UTC()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	a := adminFrom(r)
	if !auth.CheckPassword(a.PasswordHash, req.CurrentPassword) {
		writeError(w, http.StatusBadRequest, "wrong_password", "Your current password is wrong.")
		return
	}
	if err := auth.CheckPasswordPolicy(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", passwordAdvice(err))
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.internalError(w, "hash password", err)
		return
	}
	if err := s.store.SetPassword(ctx, a.ID, hash); err != nil {
		s.internalError(w, "set password", err)
		return
	}
	if err := s.store.DeleteOtherSessions(ctx, a.ID, sessionFrom(r).IDHash); err != nil {
		s.internalError(w, "end other sessions", err)
		return
	}
	s.record(ctx, a.Email, "admin.password_changed", "other sessions signed out")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) newRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	codes := auth.NewRecoveryCodes(recoveryCodeCount)
	if err := s.store.ReplaceRecoveryCodes(r.Context(), a.ID, hashCodes(codes)); err != nil {
		s.internalError(w, "replace recovery codes", err)
		return
	}
	s.record(r.Context(), a.Email, "admin.recovery_codes_replaced", "")
	writeJSON(w, http.StatusOK, map[string][]string{"recovery_codes": codes})
}

func hashCodes(codes []string) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = auth.HashRecoveryCode(c)
	}
	return out
}

// passwordAdvice turns a policy error into a sentence for the form.
func passwordAdvice(err error) string {
	msg := strings.TrimPrefix(err.Error(), auth.ErrWeakPassword.Error()+": ")
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}

func tooMany(w http.ResponseWriter, wait time.Duration) {
	secs := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate_limited",
		fmt.Sprintf("Too many failed attempts. Try again in %d minutes.", int(math.Ceil(wait.Minutes()))))
}
