package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/auth"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/settings"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

const (
	cookieName = "__Host-admin_session"
	csrfHeader = "X-CSRF-Token"

	idleTimeout     = 30 * time.Minute
	absoluteTimeout = 12 * time.Hour
	stepUpWindow    = 5 * time.Minute
	inviteLifetime  = 24 * time.Hour

	maxSessionFailures = 5 // wrong codes before a session is ended
	maxAdminFailures   = 10
	adminFailureWindow = time.Hour

	maxBody = 64 << 10
)

// Server is the dashboard's HTTP API.
type Server struct {
	cfg      Config
	store    *store.Store
	settings *settings.Service
	box      *auth.Box
	logger   *slog.Logger
	now      func() time.Time

	loginByIP    *limiter
	loginByEmail *limiter
	codeByAdmin  *limiter

	mux      *http.ServeMux
	patterns []string
}

// New returns a Server. cfg.Key must be set.
func New(cfg Config, st *store.Store, live *settings.Service, logger *slog.Logger) (*Server, error) {
	if cfg.Key == nil {
		return nil, errors.New("ADMIN_ENCRYPTION_KEY is not set (make one with: openssl rand -base64 32)")
	}
	box, err := auth.NewBox(cfg.Key)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:          cfg,
		store:        st,
		settings:     live,
		box:          box,
		logger:       logger,
		now:          time.Now,
		loginByIP:    newLimiter(10, 15*time.Minute),
		loginByEmail: newLimiter(5, 15*time.Minute),
		codeByAdmin:  newLimiter(maxAdminFailures, adminFailureWindow),
		mux:          http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

// Handler returns the server's routes wrapped in its security middleware.
func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.checkOrigin(s.mux))
}

// Run serves until ctx is done.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr(),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go s.sweepSessions(ctx)

	errc := make(chan error, 1)
	go func() {
		s.logger.Info("admin dashboard listening", "addr", s.cfg.Addr(), "url", s.cfg.BaseURL())
		if s.cfg.TLSCert != "" {
			errc <- srv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	select {
	case err := <-errc:
		return fmt.Errorf("admin dashboard: %w", err)
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

func (s *Server) sweepSessions(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := s.now()
			if err := s.store.DeleteExpiredSessions(ctx, now.Add(-idleTimeout), now.Add(-absoluteTimeout)); err != nil {
				s.logger.Error("sweep admin sessions", "err", err)
			}
		}
	}
}

func (s *Server) routes() {
	s.handle("GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	s.handle("POST /auth/login", http.HandlerFunc(s.login))
	s.handle("POST /auth/invite/check", http.HandlerFunc(s.checkInvite))
	s.handle("POST /auth/invite/accept", http.HandlerFunc(s.acceptInvite))
	s.handle("POST /auth/logout", s.session(s.logout, anyState...))
	s.handle("POST /auth/verify", s.session(s.verify, store.SessionPending2FA, store.SessionFull))
	s.handle("POST /auth/totp/setup", s.session(s.setupTOTP, store.SessionSetup2FA))
	s.handle("POST /auth/totp/confirm", s.session(s.confirmTOTP, store.SessionSetup2FA))

	s.handle("GET /api/v1/me", s.session(s.me, anyState...))
	s.handle("POST /api/v1/me/password", s.session(s.stepUp(s.changePassword), store.SessionFull))
	s.handle("POST /api/v1/me/recovery-codes", s.session(s.stepUp(s.newRecoveryCodes), store.SessionFull))
	s.handle("GET /api/v1/settings", s.session(s.getSettings, store.SessionFull))
	s.handle("PUT /api/v1/settings", s.session(s.putSettings, store.SessionFull))
	s.handle("GET /api/v1/audit", s.session(s.audit, store.SessionFull))
	s.handle("GET /api/v1/admins", s.session(s.listAdmins, store.SessionFull))
	s.handle("POST /api/v1/admins/invites", s.session(s.stepUp(s.inviteAdmin), store.SessionFull))
	s.handle("POST /api/v1/admins/{id}/reset", s.session(s.stepUp(s.resetAdmin), store.SessionFull))
	s.handle("DELETE /api/v1/admins/{id}", s.session(s.stepUp(s.deleteAdmin), store.SessionFull))
}

// handle registers h and remembers the pattern, so a test can check every
// route is described in api/openapi.yaml.
func (s *Server) handle(pattern string, h http.Handler) {
	s.patterns = append(s.patterns, pattern)
	s.mux.Handle(pattern, h)
}

var anyState = []string{store.SessionPending2FA, store.SessionSetup2FA, store.SessionFull}

// securityHeaders sets headers that keep the dashboard out of frames and
// stop the browser loading anything from elsewhere.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		if s.cfg.secure() {
			h.Set("Strict-Transport-Security", "max-age=63072000")
		}
		next.ServeHTTP(w, r)
	})
}

// checkOrigin refuses state-changing requests that didn't come from the
// dashboard's own pages.
func (s *Server) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			writeError(w, http.StatusForbidden, "origin", "Request came from another site.")
			return
		}
		if r.Header.Get("Origin") != s.origin(r) {
			writeError(w, http.StatusForbidden, "origin", "Request came from another site.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// origin is the scheme and host browsers use for the dashboard.
func (s *Server) origin(r *http.Request) string {
	if s.cfg.PublicURL != nil {
		return s.cfg.PublicURL.Scheme + "://" + s.cfg.PublicURL.Host
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.proxied() {
		// The proxy appends the address it saw; earlier entries can be forged.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type ctxKey int

const (
	sessionKey ctxKey = iota
	adminKey
	tokenKey
)

// session loads the signed-in admin and only lets through sessions in one
// of states. Writes must carry the session's CSRF token.
func (s *Server) session(next http.HandlerFunc, states ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Sign in first.")
			return
		}
		ctx := r.Context()
		hash := auth.HashToken(c.Value)
		sess, err := s.store.GetSession(ctx, hash)
		if errors.Is(err, store.ErrNotFound) {
			clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "Your session has ended. Sign in again.")
			return
		}
		if err != nil {
			s.internalError(w, "load session", err)
			return
		}
		now := s.now()
		if now.Sub(sess.LastSeen) > idleTimeout || now.Sub(sess.CreatedAt) > absoluteTimeout {
			_ = s.store.DeleteSession(ctx, hash)
			clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "Your session has ended. Sign in again.")
			return
		}
		a, err := s.store.GetAdmin(ctx, sess.AdminID)
		if err != nil || a.Locked {
			_ = s.store.DeleteSession(ctx, hash)
			clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "Your session has ended. Sign in again.")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(sess.CSRF)) != 1 {
			writeError(w, http.StatusForbidden, "csrf", "Missing or wrong CSRF token. Reload the page.")
			return
		}
		allowed := false
		for _, st := range states {
			allowed = allowed || st == sess.State
		}
		if !allowed {
			code := "second_factor_required"
			if sess.State == store.SessionSetup2FA {
				code = "totp_setup_required"
			}
			writeJSON(w, http.StatusForbidden, map[string]string{
				"code": code, "error": "Finish signing in first.", "state": sess.State,
			})
			return
		}
		if now.Sub(sess.LastSeen) > time.Minute {
			if err := s.store.TouchSession(ctx, hash, now); err != nil {
				s.logger.Error("touch admin session", "err", err)
			}
		}
		ctx = context.WithValue(ctx, sessionKey, sess)
		ctx = context.WithValue(ctx, adminKey, a)
		ctx = context.WithValue(ctx, tokenKey, c.Value)
		next(w, r.WithContext(ctx))
	})
}

// stepUp requires a second factor within the last few minutes, for changes
// that would let a stolen session take over the dashboard.
func (s *Server) stepUp(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r)
		if sess.VerifiedAt.IsZero() || s.now().Sub(sess.VerifiedAt) > stepUpWindow {
			writeError(w, http.StatusForbidden, "step_up_required", "Enter a fresh authenticator code first.")
			return
		}
		next(w, r)
	}
}

func sessionFrom(r *http.Request) store.Session { return r.Context().Value(sessionKey).(store.Session) }
func adminFrom(r *http.Request) store.Admin     { return r.Context().Value(adminKey).(store.Admin) }
func tokenFrom(r *http.Request) string          { return r.Context().Value(tokenKey).(string) }

// startSession opens a session for adminID and sets its cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, adminID int64, state string) (store.Session, error) {
	token, sess := s.newSession(adminID, state)
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		return sess, err
	}
	setCookie(w, token)
	return sess, nil
}

func (s *Server) newSession(adminID int64, state string) (string, store.Session) {
	token := auth.NewToken()
	now := s.now()
	sess := store.Session{
		IDHash:    auth.HashToken(token),
		AdminID:   adminID,
		State:     state,
		CSRF:      auth.NewToken(),
		CreatedAt: now,
		LastSeen:  now,
	}
	return token, sess
}

// rotate replaces the current session with a fully signed-in one under a new
// cookie, and records the second factor just passed.
func (s *Server) rotate(w http.ResponseWriter, r *http.Request) (store.Session, error) {
	old := sessionFrom(r)
	token, next := s.newSession(old.AdminID, store.SessionFull)
	next.CreatedAt = old.CreatedAt
	next.VerifiedAt = s.now()
	if err := s.store.RotateSession(r.Context(), old.IDHash, next); err != nil {
		return next, err
	}
	setCookie(w, token)
	return next, nil
}

func setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(absoluteTimeout.Seconds()),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListAudit(r.Context(), 200)
	if err != nil {
		s.internalError(w, "list audit", err)
		return
	}
	out := make([]auditJSON, len(entries))
	for i, e := range entries {
		out[i] = auditJSON{ID: e.ID, At: e.At.UTC(), Actor: e.Actor, Action: e.Action, Detail: e.Detail}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

type auditJSON struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
}

// record writes an audit entry, logging rather than failing the request if
// it can't.
func (s *Server) record(ctx context.Context, actor, action, detail string) {
	err := s.store.AddAudit(ctx, store.AuditEntry{At: s.now(), Actor: actor, Action: action, Detail: detail})
	if err != nil {
		s.logger.Error("write audit log", "action", action, "err", err)
	}
}

func (s *Server) internalError(w http.ResponseWriter, what string, err error) {
	s.logger.Error("admin api: "+what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. Check the bot's logs.")
}

// readJSON decodes a small JSON body into v, rejecting unknown fields.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "invalid", "Send JSON.")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "The request body isn't valid.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "error": msg})
}
