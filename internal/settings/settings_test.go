package settings

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func fileSettings() config.Settings {
	return config.Settings{
		Channels: config.Channels{General: 11, Tickets: 15},
		Roles: config.Roles{
			Staff:      20,
			SelfAssign: []config.SelfAssignRole{{ID: 21, Name: "Industrialist", Description: "Builds things"}},
		},
		Welcome: config.Welcome{Message: "Hi"},
	}
}

func TestLoadSeedsFromFile(t *testing.T) {
	st, _ := openStore(t)
	s, err := Load(context.Background(), st, fileSettings(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Settings(); got.Channels.General != 11 || got.Roles.SelfAssign[0].Name != "Industrialist" {
		t.Errorf("Settings = %+v", got)
	}
	if s.Version() != 1 {
		t.Errorf("Version = %d, want 1", s.Version())
	}
}

func TestFileEditsApplyUntilAdminSaves(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	if _, err := Load(ctx, st, fileSettings(), quiet); err != nil {
		t.Fatal(err)
	}

	// The operator edits config.yaml and restarts: the file still wins.
	edited := fileSettings()
	edited.Channels.General = 99
	s, err := Load(ctx, st, edited, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Settings().Channels.General != 99 {
		t.Fatalf("General = %d, want the edited 99", s.Settings().Channels.General)
	}

	// An admin saves from the dashboard: from now on the database wins.
	fromAdmin := s.Settings()
	fromAdmin.Welcome.Message = "Welcome aboard"
	if _, err := s.Update(ctx, fromAdmin, s.Version(), "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	s, err = Load(ctx, st, fileSettings(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Settings(); got.Welcome.Message != "Welcome aboard" || got.Channels.General != 99 {
		t.Errorf("after restart Settings = %+v, want the admin's copy", got)
	}
}

func TestUpdateIsLiveAndRejectsStaleVersion(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	s, err := Load(ctx, st, fileSettings(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	var src config.Source = s // modules read through this

	next := s.Settings()
	next.Channels.ModLog = 30
	v, err := s.Update(ctx, next, s.Version(), "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if src.Settings().Channels.ModLog != 30 || v != 2 {
		t.Errorf("after Update: ModLog = %d, version %d", src.Settings().Channels.ModLog, v)
	}

	next.Channels.ModLog = 31
	if _, err := s.Update(ctx, next, 1, "someone-else"); !errors.Is(err, ErrConflict) {
		t.Errorf("stale Update: err = %v, want ErrConflict", err)
	}
	if src.Settings().Channels.ModLog != 30 {
		t.Error("a rejected Update changed the live settings")
	}
}

func TestUpdateValidates(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	s, err := Load(ctx, st, fileSettings(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	bad := s.Settings()
	bad.Roles.SelfAssign = []config.SelfAssignRole{{ID: 5}}
	if _, err := s.Update(ctx, bad, s.Version(), "admin@example.com"); err == nil {
		t.Fatal("Update accepted a self-assign role with no name")
	}
}

func TestAuditRecordsChanges(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	s, err := Load(ctx, st, fileSettings(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, s.Settings(), s.Version(), "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	log, err := st.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 || log[0].Actor != "admin@example.com" || log[1].Actor != FileActor {
		t.Errorf("audit log = %+v", log)
	}
}
