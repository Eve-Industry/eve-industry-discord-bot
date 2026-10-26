package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFAQRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	if err := s.SetFAQ(ctx, FAQ{Name: "Docker", Answer: "Use the image.", UpdatedBy: "1", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFAQ(ctx, "docker")
	if err != nil {
		t.Fatalf("GetFAQ is not case-insensitive: %v", err)
	}
	if got.Answer != "Use the image." || !got.UpdatedAt.Equal(now) {
		t.Errorf("got %+v", got)
	}

	// Setting again with different case replaces, not duplicates.
	if err := s.SetFAQ(ctx, FAQ{Name: "docker", Answer: "New answer.", UpdatedBy: "2", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	names, err := s.SearchFAQNames(ctx, "", 25)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"docker"}) {
		t.Errorf("names = %v, want [docker]", names)
	}

	if err := s.DeleteFAQ(ctx, "DOCKER"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFAQ(ctx, "docker"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete, err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteFAQ(ctx, "docker"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestSearchFAQNamesEscapesWildcards(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, n := range []string{"install", "100% cpu", "update"} {
		if err := s.SetFAQ(ctx, FAQ{Name: n, Answer: "x", UpdatedBy: "1", UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	names, err := s.SearchFAQNames(ctx, "%", 25)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"100% cpu"}) {
		t.Errorf("search %% = %v, want [100%% cpu]", names)
	}
}

func TestSettingsVersioning(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	if _, err := s.GetSettings(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSettings before first save: err = %v, want ErrNotFound", err)
	}

	v, err := s.SaveSettings(ctx, []byte(`{"a":1}`), 0, AuditEntry{At: now, Actor: "seed", Action: "settings.seed"})
	if err != nil || v != 1 {
		t.Fatalf("first save = %d, %v; want 1, nil", v, err)
	}
	if _, err := s.SaveSettings(ctx, []byte(`{"a":9}`), 0, AuditEntry{At: now, Actor: "other"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second first-save: err = %v, want ErrConflict", err)
	}

	v, err = s.SaveSettings(ctx, []byte(`{"a":2}`), 1, AuditEntry{At: now, Actor: "admin", Action: "settings.update"})
	if err != nil || v != 2 {
		t.Fatalf("update = %d, %v; want 2, nil", v, err)
	}
	if _, err := s.SaveSettings(ctx, []byte(`{"a":3}`), 1, AuditEntry{At: now, Actor: "stale"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: err = %v, want ErrConflict", err)
	}

	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != `{"a":2}` || got.Version != 2 || got.UpdatedBy != "admin" {
		t.Errorf("GetSettings = %s v%d by %s", got.Data, got.Version, got.UpdatedBy)
	}

	log, err := s.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range log {
		actions = append(actions, e.Action)
	}
	if want := []string{"settings.update", "settings.seed"}; !slices.Equal(actions, want) {
		t.Errorf("audit actions = %v, want %v (failed saves must not be logged)", actions, want)
	}
}
