package faq

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
)

func TestCleanName(t *testing.T) {
	if got := cleanName("  self   hosting\n"); got != "self hosting" {
		t.Errorf("cleanName = %q", got)
	}
	if got := cleanName(strings.Repeat("x", 80)); len([]rune(got)) != maxNameLen {
		t.Errorf("cleanName length = %d, want %d", len([]rune(got)), maxNameLen)
	}
}

func TestEditorPrefills(t *testing.T) {
	m := editor("docker", "Use the image.")
	values := map[string]string{}
	for _, c := range m.Components {
		input := c.(discord.LabelComponent).Component.(discord.TextInputComponent)
		values[input.CustomID] = input.Value
	}
	if values[nameInputID] != "docker" || values[textInputID] != "Use the image." {
		t.Errorf("editor values = %v", values)
	}
}

func TestCommandsRestrictAdmin(t *testing.T) {
	for _, c := range (Module{}).Commands() {
		cmd := c.(discord.SlashCommandCreate)
		restricted := cmd.DefaultMemberPermissions.Value != nil
		if cmd.Name == "faq-admin" && !restricted {
			t.Error("/faq-admin is open to everyone")
		}
		if cmd.Name == "faq" && restricted {
			t.Error("/faq is restricted")
		}
	}
}
