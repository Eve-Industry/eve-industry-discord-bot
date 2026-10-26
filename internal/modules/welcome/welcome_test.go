package welcome

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/feedback"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/roles"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/tickets"
)

const guildID = 10

func fullSettings() config.Settings {
	return config.Settings{
		Channels: config.Channels{
			General:            11,
			FeatureRequests:    12,
			Bugs:               13,
			SelfHostingSupport: 14,
			Tickets:            15,
		},
		Roles: config.Roles{
			Staff:      20,
			SelfAssign: []config.SelfAssignRole{{ID: 21, Name: "Industrialist"}},
		},
	}
}

func buttons(msg discord.MessageCreate) []discord.ButtonComponent {
	var out []discord.ButtonComponent
	for _, row := range msg.Components {
		for _, c := range row.(discord.ActionRowComponent).Components {
			out = append(out, c.(discord.ButtonComponent))
		}
	}
	return out
}

func TestPanelFull(t *testing.T) {
	got := buttons(panel(guildID, fullSettings()))
	want := []struct{ label, target string }{
		{"General", "https://discord.com/channels/10/11"},
		{"Self-hosting help", "https://discord.com/channels/10/14"},
		{"Request a feature", feedback.FeatureButtonID},
		{"Report a bug", feedback.BugButtonID},
		{"Contact staff", tickets.OpenButtonID},
		{"Pick roles", roles.PickButtonID},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d buttons, want %d", len(got), len(want))
	}
	for i, w := range want {
		target := got[i].URL
		if target == "" {
			target = got[i].CustomID
		}
		if got[i].Label != w.label || target != w.target {
			t.Errorf("button %d = %q -> %q, want %q -> %q", i, got[i].Label, target, w.label, w.target)
		}
	}
}

func TestPanelSkipsUnsetFeatures(t *testing.T) {
	s := fullSettings()
	s.Channels.Bugs = 0
	s.Roles.Staff = 0
	s.Roles.SelfAssign = nil

	msg := panel(guildID, s)
	for _, b := range buttons(msg) {
		switch b.Label {
		case "Report a bug", "Contact staff", "Pick roles":
			t.Errorf("panel has %q button although it isn't configured", b.Label)
		}
	}
	if strings.Contains(msg.Embeds[0].Description, "Contact staff") {
		t.Error("panel text mentions Contact staff although tickets aren't configured")
	}
}

func TestGreetingDefaultsMessage(t *testing.T) {
	member := discord.Member{User: discord.User{ID: 42, Username: "alice"}}
	msg := greeting(fullSettings(), member)
	if !strings.Contains(msg.Embeds[0].Description, "Say hello in <#11>") {
		t.Errorf("description = %q, want the default message", msg.Embeds[0].Description)
	}
	if !strings.Contains(msg.Content, "<@42>") {
		t.Errorf("content %q doesn't mention the member", msg.Content)
	}
}

func TestRulesGateHasAcceptButton(t *testing.T) {
	got := buttons(rulesGate())
	if len(got) != 1 || got[0].CustomID != acceptButtonID {
		t.Fatalf("rules gate buttons = %+v, want one Accept button", got)
	}
}

func TestGatedOnlyWithMemberRole(t *testing.T) {
	s := fullSettings()
	if gated(s) {
		t.Error("gated without roles.member")
	}
	s.Roles.Member = 30
	if !gated(s) {
		t.Error("not gated with roles.member set")
	}
}
