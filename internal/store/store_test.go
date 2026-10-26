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
