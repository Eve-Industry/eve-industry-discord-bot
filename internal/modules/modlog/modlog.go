// Package modlog records joins, leaves and role changes in a staff channel.
package modlog

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
)

const (
	joinColor  = 0x57AB5A
	leaveColor = 0xE5534B
	roleColor  = 0x539BF5
)

// Module posts member events to the mod log channel.
type Module struct {
	guildID   snowflake.ID
	channelID snowflake.ID
	logger    *slog.Logger
}

// New returns the mod log module.
func New(cfg config.Config, logger *slog.Logger) Module {
	return Module{guildID: cfg.GuildID, channelID: cfg.Channels.ModLog, logger: logger}
}

// Commands declares no slash commands.
func (Module) Commands() []discord.ApplicationCommandCreate { return nil }

// Register adds no interaction handlers.
func (Module) Register(handler.Router) {}

// Listeners watches joins, leaves and member updates.
func (m Module) Listeners() []bot.EventListener {
	if m.channelID == 0 {
		return nil
	}
	return []bot.EventListener{
		bot.NewListenerFunc(m.onJoin),
		bot.NewListenerFunc(m.onLeave),
		bot.NewListenerFunc(m.onUpdate),
	}
}

func (m Module) onJoin(e *events.GuildMemberJoin) {
	if e.GuildID != m.guildID {
		return
	}
	m.send(e.Client(), joinEntry(e.Member.User, time.Now()))
}

func (m Module) onLeave(e *events.GuildMemberLeave) {
	if e.GuildID != m.guildID {
		return
	}
	m.send(e.Client(), leaveEntry(e.User))
}

func (m Module) onUpdate(e *events.GuildMemberUpdate) {
	if e.GuildID != m.guildID {
		return
	}
	// Without the old member from the cache there is nothing to compare.
	if e.OldMember.User.ID == 0 {
		return
	}
	if entry, ok := roleEntry(e.Member.User, e.OldMember.RoleIDs, e.Member.RoleIDs); ok {
		m.send(e.Client(), entry)
	}
}

func (m Module) send(client *bot.Client, embed discord.Embed) {
	_, err := client.Rest.CreateMessage(m.channelID, discord.MessageCreate{
		Embeds:          []discord.Embed{embed},
		AllowedMentions: &discord.AllowedMentions{},
	})
	if err != nil {
		m.logger.Error("post mod log", "err", err)
	}
}

func joinEntry(user discord.User, now time.Time) discord.Embed {
	age := now.Sub(user.CreatedAt())
	desc := fmt.Sprintf("%s joined. Account created <t:%d:R>.", userLabel(user), user.CreatedAt().Unix())
	if age < 7*24*time.Hour {
		desc += " **New account.**"
	}
	return discord.NewEmbed().WithTitle("Member joined").WithDescription(desc).WithColor(joinColor)
}

func leaveEntry(user discord.User) discord.Embed {
	return discord.NewEmbed().
		WithTitle("Member left").
		WithDescription(userLabel(user) + " left.").
		WithColor(leaveColor)
}

// roleEntry describes roles added and removed, or reports false when the
// roles did not change.
func roleEntry(user discord.User, before, after []snowflake.ID) (discord.Embed, bool) {
	var added, removed []string
	for _, id := range after {
		if !slices.Contains(before, id) {
			added = append(added, discord.RoleMention(id))
		}
	}
	for _, id := range before {
		if !slices.Contains(after, id) {
			removed = append(removed, discord.RoleMention(id))
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return discord.Embed{}, false
	}
	lines := []string{userLabel(user)}
	if len(added) > 0 {
		lines = append(lines, "Added: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		lines = append(lines, "Removed: "+strings.Join(removed, ", "))
	}
	return discord.NewEmbed().
		WithTitle("Roles changed").
		WithDescription(strings.Join(lines, "\n")).
		WithColor(roleColor), true
}

func userLabel(user discord.User) string {
	return fmt.Sprintf("%s (`%s`, %s)", user.Mention(), user.Username, user.ID)
}
