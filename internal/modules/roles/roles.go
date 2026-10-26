// Package roles lets members pick their own roles from a menu.
package roles

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
)

// PickButtonID is the custom ID of a button that opens the role picker.
const PickButtonID = "/roles/pick"

const selectID = "/roles/select"

// Module serves the role picker.
type Module struct {
	guildID snowflake.ID
	roles   []config.SelfAssignRole
	logger  *slog.Logger
}

// New returns the roles module.
func New(cfg config.Config, logger *slog.Logger) Module {
	return Module{guildID: cfg.GuildID, roles: cfg.Roles.SelfAssign, logger: logger}
}

// Commands declares no slash commands; the picker opens from a button.
func (Module) Commands() []discord.ApplicationCommandCreate { return nil }

// Register adds the picker's button and menu handlers.
func (m Module) Register(r handler.Router) {
	r.ButtonComponent(PickButtonID, m.openPicker)
	r.SelectMenuComponent(selectID, m.applyPicks)
}

func (m Module) openPicker(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	if len(m.roles) == 0 {
		return e.CreateMessage(ephemeral("No roles are set up to pick yet."))
	}
	var have []snowflake.ID
	if member := e.Member(); member != nil {
		have = member.RoleIDs
	}
	return e.CreateMessage(discord.MessageCreate{
		Content:    "Pick the roles you want. Untick one to remove it.",
		Components: []discord.LayoutComponent{discord.NewActionRow(pickerMenu(m.roles, have))},
		Flags:      discord.MessageFlagEphemeral,
	})
}

func (m Module) applyPicks(data discord.SelectMenuInteractionData, e *handler.ComponentEvent) error {
	member := e.Member()
	if member == nil {
		return e.CreateMessage(ephemeral("Roles can only be picked inside the server."))
	}
	picked := data.(discord.StringSelectMenuInteractionData).Values
	add, remove := diff(m.roles, member.RoleIDs, picked)

	rest := e.Client().Rest
	userID := member.User.ID
	var added, removed int
	var failed []string
	for _, id := range add {
		if err := rest.AddMemberRole(m.guildID, userID, id); err != nil {
			m.logger.Error("add role", "user", userID, "role", id, "err", err)
			failed = append(failed, discord.RoleMention(id))
			continue
		}
		added++
	}
	for _, id := range remove {
		if err := rest.RemoveMemberRole(m.guildID, userID, id); err != nil {
			m.logger.Error("remove role", "user", userID, "role", id, "err", err)
			failed = append(failed, discord.RoleMention(id))
			continue
		}
		removed++
	}

	msg := fmt.Sprintf("Done: %d added, %d removed.", added, removed)
	if len(failed) > 0 {
		msg += " I couldn't change " + strings.Join(failed, ", ") + ". Please tell a staff member."
	}
	return e.UpdateMessage(discord.MessageUpdate{
		Content:    &msg,
		Components: &[]discord.LayoutComponent{},
	})
}

// pickerMenu builds the select menu, pre-selecting the roles the member has.
func pickerMenu(roles []config.SelfAssignRole, have []snowflake.ID) discord.StringSelectMenuComponent {
	options := make([]discord.StringSelectMenuOption, 0, len(roles))
	for _, r := range roles {
		opt := discord.NewStringSelectMenuOption(r.Name, r.ID.String()).
			WithDefault(slices.Contains(have, r.ID))
		if r.Description != "" {
			opt = opt.WithDescription(r.Description)
		}
		options = append(options, opt)
	}
	return discord.NewStringSelectMenu(selectID, "Choose your roles", options...).
		WithMinValues(0).
		WithMaxValues(len(roles))
}

// diff works out which self-assignable roles to add and remove so that the
// member ends up with exactly the picked ones. Roles outside the picker are
// never touched.
func diff(roles []config.SelfAssignRole, have []snowflake.ID, picked []string) (add, remove []snowflake.ID) {
	for _, r := range roles {
		want := slices.Contains(picked, r.ID.String())
		has := slices.Contains(have, r.ID)
		switch {
		case want && !has:
			add = append(add, r.ID)
		case !want && has:
			remove = append(remove, r.ID)
		}
	}
	return add, remove
}

func ephemeral(content string) discord.MessageCreate {
	return discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral}
}
