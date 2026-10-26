// Package settings keeps the live copy of the settings admins can change
// while the bot runs. The database holds them; config.yaml only seeds them.
package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

// FileActor is the actor recorded when settings come from config.yaml.
// While it is the last writer, the file stays in charge: edits to it are
// picked up on the next start. Once an admin saves from the dashboard, the
// database copy wins.
const FileActor = "config.yaml"

// ErrConflict is returned by Update when the settings changed since they
// were read.
var ErrConflict = store.ErrConflict

// Service serves the current settings and saves changes.
type Service struct {
	store *store.Store
	now   func() time.Time
	cur   atomic.Pointer[snapshot]
}

type snapshot struct {
	settings config.Settings
	version  int64
}

// Load reads the saved settings, seeding them from file on first start, and
// returns a Service that serves them.
func Load(ctx context.Context, st *store.Store, file config.Settings, logger *slog.Logger) (*Service, error) {
	s := &Service{store: st, now: time.Now}
	fileData, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}

	saved, err := st.GetSettings(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		v, err := st.SaveSettings(ctx, fileData, 0, s.entry(FileActor, "settings.seed", fileData))
		if err != nil {
			return nil, fmt.Errorf("seed settings: %w", err)
		}
		logger.Info("settings seeded from config.yaml")
		s.cur.Store(&snapshot{settings: file, version: v})
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read settings: %w", err)
	}

	if bytes.Equal(saved.Data, fileData) {
		s.cur.Store(&snapshot{settings: file, version: saved.Version})
		return s, nil
	}
	if saved.UpdatedBy == FileActor {
		v, err := st.SaveSettings(ctx, fileData, saved.Version, s.entry(FileActor, "settings.reload", fileData))
		if err != nil {
			return nil, fmt.Errorf("update settings from config.yaml: %w", err)
		}
		logger.Info("settings updated from config.yaml")
		s.cur.Store(&snapshot{settings: file, version: v})
		return s, nil
	}

	var cur config.Settings
	if err := json.Unmarshal(saved.Data, &cur); err != nil {
		return nil, fmt.Errorf("decode saved settings: %w", err)
	}
	logger.Warn("channels, roles and welcome in config.yaml are ignored because an admin changed them in the dashboard",
		"by", saved.UpdatedBy, "at", saved.UpdatedAt)
	s.cur.Store(&snapshot{settings: cur, version: saved.Version})
	return s, nil
}

// Settings returns the current settings.
func (s *Service) Settings() config.Settings { return s.cur.Load().settings }

// Version returns the version of the current settings. Pass it to Update.
func (s *Service) Version() int64 { return s.cur.Load().version }

// Update saves next if the settings are still at version, records actor in
// the audit log and makes next live at once. It returns the new version, or
// ErrConflict if someone else saved first.
func (s *Service) Update(ctx context.Context, next config.Settings, version int64, actor string) (int64, error) {
	if err := next.Validate(); err != nil {
		return 0, err
	}
	next.Roles.SelfAssign = slices.Clone(next.Roles.SelfAssign)
	data, err := json.Marshal(next)
	if err != nil {
		return 0, err
	}
	v, err := s.store.SaveSettings(ctx, data, version, s.entry(actor, "settings.update", data))
	if err != nil {
		return 0, err
	}
	s.cur.Store(&snapshot{settings: next, version: v})
	return v, nil
}

func (s *Service) entry(actor, action string, data []byte) store.AuditEntry {
	return store.AuditEntry{At: s.now(), Actor: actor, Action: action, Detail: string(data)}
}
