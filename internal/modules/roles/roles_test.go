package roles

import (
	"slices"
	"testing"

	"github.com/disgoorg/snowflake/v2"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
)

func TestDiff(t *testing.T) {
	roles := []config.SelfAssignRole{{ID: 1, Name: "Industrialist"}, {ID: 2, Name: "Trader"}, {ID: 3, Name: "Miner"}}
	// Member has Industrialist, Miner and an unrelated role 99.
	have := []snowflake.ID{1, 3, 99}
	// They untick Miner and tick Trader.
	add, remove := diff(roles, have, []string{"1", "2"})

	if !slices.Equal(add, []snowflake.ID{2}) {
		t.Errorf("add = %v, want [2]", add)
	}
	if !slices.Equal(remove, []snowflake.ID{3}) {
		t.Errorf("remove = %v, want [3]", remove)
	}
}

func TestPickerMenuPreselects(t *testing.T) {
	roles := []config.SelfAssignRole{{ID: 1, Name: "Industrialist"}, {ID: 2, Name: "Trader"}}
	menu := pickerMenu(roles, []snowflake.ID{2})

	if len(menu.Options) != 2 {
		t.Fatalf("got %d options, want 2", len(menu.Options))
	}
	if menu.Options[0].Default || !menu.Options[1].Default {
		t.Errorf("defaults = %v, %v; want false, true", menu.Options[0].Default, menu.Options[1].Default)
	}
	if menu.MaxValues != 2 {
		t.Errorf("MaxValues = %d, want 2", menu.MaxValues)
	}
}
