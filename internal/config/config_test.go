package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "guild_id: 123\nchannels:\n  general: 456\n  bugs: 789\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISCORD_TOKEN", "test-token")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "test-token" {
		t.Errorf("Token = %q, want test-token", cfg.Token)
	}
	if cfg.GuildID != 123 {
		t.Errorf("GuildID = %d, want 123", cfg.GuildID)
	}
	if cfg.Channels.General != 456 || cfg.Channels.Bugs != 789 {
		t.Errorf("Channels = %+v", cfg.Channels)
	}
}

func TestLoadMissingToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("guild_id: 123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISCORD_TOKEN", "")

	if _, err := Load(path); err == nil {
		t.Fatal("Load succeeded without DISCORD_TOKEN")
	}
}
