package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/settings"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

type settingsJSON struct {
	Version  int64           `json:"version"`
	Settings config.Settings `json:"settings"`
}

func (s *Server) currentSettings() settingsJSON {
	return settingsJSON{Version: s.settings.Version(), Settings: s.settings.Settings()}
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.currentSettings())
}

// putSettings saves new settings. The request carries the version it
// edited; if someone saved since, it gets 409 and the newer settings.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsJSON
	if !readJSON(w, r, &req) {
		return
	}
	if err := req.Settings.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	_, err := s.settings.Update(r.Context(), req.Settings, req.Version, adminFrom(r).Email)
	if errors.Is(err, settings.ErrConflict) {
		writeJSON(w, http.StatusConflict, struct {
			Code    string       `json:"code"`
			Error   string       `json:"error"`
			Current settingsJSON `json:"current"`
		}{"conflict", "Someone else saved changes first. Review theirs and try again.", s.currentSettings()})
		return
	}
	if err != nil {
		s.internalError(w, "save settings", err)
		return
	}
	writeJSON(w, http.StatusOK, s.currentSettings())
}

type adminJSON struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Active    bool      `json:"active"` // has a password and an authenticator
	Locked    bool      `json:"locked"`
	CreatedAt time.Time `json:"created_at"`
	You       bool      `json:"you"`
}

func (s *Server) listAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := s.store.ListAdmins(r.Context())
	if err != nil {
		s.internalError(w, "list admins", err)
		return
	}
	me := adminFrom(r).ID
	out := make([]adminJSON, len(admins))
	for i, a := range admins {
		out[i] = adminJSON{
			ID:        a.ID,
			Email:     a.Email,
			Active:    a.PasswordHash != "" && a.TOTPSecret != nil,
			Locked:    a.Locked,
			CreatedAt: a.CreatedAt.UTC(),
			You:       a.ID == me,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": out})
}

type inviteJSON struct {
	Link      string    `json:"link"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) inviteAdmin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	email, err := NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "Enter a valid email address.")
		return
	}
	link, expires, err := Invite(r.Context(), s.store, s.cfg.BaseURL(), email, adminFrom(r).Email, s.now())
	if errors.Is(err, ErrAlreadyAdmin) {
		writeError(w, http.StatusConflict, "already_admin", "That person is already an admin. Reset them instead if they're locked out.")
		return
	}
	if err != nil {
		s.internalError(w, "invite admin", err)
		return
	}
	writeJSON(w, http.StatusCreated, inviteJSON{Link: link, ExpiresAt: expires.UTC()})
}

func (s *Server) resetAdmin(w http.ResponseWriter, r *http.Request) {
	target, ok := s.otherAdmin(w, r, "Use Change password to change your own.")
	if !ok {
		return
	}
	link, expires, err := Reset(r.Context(), s.store, s.cfg.BaseURL(), target, adminFrom(r).Email, s.now())
	if err != nil {
		s.internalError(w, "reset admin", err)
		return
	}
	s.codeByAdmin.reset(strconv.FormatInt(target.ID, 10))
	writeJSON(w, http.StatusOK, inviteJSON{Link: link, ExpiresAt: expires.UTC()})
}

func (s *Server) deleteAdmin(w http.ResponseWriter, r *http.Request) {
	target, ok := s.otherAdmin(w, r, "You can't remove yourself. Ask another admin.")
	if !ok {
		return
	}
	if err := s.store.DeleteAdmin(r.Context(), target.ID); err != nil {
		s.internalError(w, "remove admin", err)
		return
	}
	s.record(r.Context(), adminFrom(r).Email, "admin.removed", target.Email)
	w.WriteHeader(http.StatusNoContent)
}

// otherAdmin loads the admin named in the path, refusing the caller
// themself with selfMsg.
func (s *Server) otherAdmin(w http.ResponseWriter, r *http.Request, selfMsg string) (store.Admin, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such admin.")
		return store.Admin{}, false
	}
	if id == adminFrom(r).ID {
		writeError(w, http.StatusBadRequest, "self", selfMsg)
		return store.Admin{}, false
	}
	target, err := s.store.GetAdmin(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such admin.")
		return store.Admin{}, false
	}
	if err != nil {
		s.internalError(w, "load admin", err)
		return store.Admin{}, false
	}
	return target, true
}
