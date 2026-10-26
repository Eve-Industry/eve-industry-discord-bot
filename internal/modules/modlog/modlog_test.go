package modlog

import (
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestRoleEntry(t *testing.T) {
	user := discord.User{ID: 42, Username: "alice"}

	embed, ok := roleEntry(user, []snowflake.ID{1, 2}, []snowflake.ID{2, 3})
	if !ok {
		t.Fatal("roleEntry reported no change")
	}
	for _, want := range []string{"Added: <@&3>", "Removed: <@&1>"} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("description %q missing %q", embed.Description, want)
		}
	}

	if _, ok := roleEntry(user, []snowflake.ID{1, 2}, []snowflake.ID{2, 1}); ok {
		t.Error("roleEntry reported a change when only the order differed")
	}
}

func TestJoinEntryFlagsNewAccounts(t *testing.T) {
	created := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	user := discord.User{ID: snowflake.New(created), Username: "newbie"}

	fresh := joinEntry(user, created.Add(2*24*time.Hour))
	if !strings.Contains(fresh.Description, "New account") {
		t.Errorf("2-day-old account not flagged: %q", fresh.Description)
	}
	old := joinEntry(user, created.Add(30*24*time.Hour))
	if strings.Contains(old.Description, "New account") {
		t.Errorf("30-day-old account flagged: %q", old.Description)
	}
}
