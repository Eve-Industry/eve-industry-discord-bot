// Package tickets lets members contact staff privately. Each ticket is a
// private thread that only the member and the staff role can see.
package tickets

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
)

// OpenButtonID is the custom ID of a button that starts a staff ticket.
const OpenButtonID = "/tickets/open"

const (
	modalID  = "/tickets/submit"
	claimID  = "/tickets/claim"
	closeID  = "/tickets/close"
	topicID  = "topic"
	detailID = "details"

	maxThreadName = 100 // Discord's limit
)

// Module serves staff tickets.
type Module struct {
	channelID snowflake.ID
	staffRole snowflake.ID
	logger    *slog.Logger
}

// New returns the tickets module.
func New(cfg config.Config, logger *slog.Logger) Module {
	return Module{channelID: cfg.Channels.Tickets, staffRole: cfg.Roles.Staff, logger: logger}
}

// Commands declares no slash commands; tickets open from a button.
func (Module) Commands() []discord.ApplicationCommandCreate { return nil }

// Register adds the ticket button, modal and thread button handlers.
func (m Module) Register(r handler.Router) {
	r.ButtonComponent(OpenButtonID, m.openModal)
	r.Modal(modalID, m.createTicket)
	r.ButtonComponent(claimID, m.claim)
	r.ButtonComponent(closeID, m.close)
}

func (m Module) openModal(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	if m.channelID == 0 || m.staffRole == 0 {
		return e.CreateMessage(ephemeral("Staff tickets aren't set up yet. Please message an admin directly."))
	}
	return e.Modal(discord.NewModalCreate(modalID, "Contact staff",
		discord.NewLabel("What is it about?", discord.NewTextInput(topicID, discord.TextInputStyleShort).
			WithRequired(true).
			WithMaxLength(80).
			WithPlaceholder("e.g. Report a member, account problem")),
		discord.NewLabel("Details", discord.NewTextInput(detailID, discord.TextInputStyleParagraph).
			WithRequired(true).
			WithMaxLength(1500)),
	))
}

func (m Module) createTicket(e *handler.ModalEvent) error {
	user := e.User()
	topic := e.Data.Text(topicID)
	details := e.Data.Text(detailID)

	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	rest := e.Client().Rest

	invitable := false
	thread, err := rest.CreateThread(m.channelID, discord.GuildPrivateThreadCreate{
		Name:                threadName(user.Username, topic),
		AutoArchiveDuration: discord.AutoArchiveDuration1w,
		Invitable:           &invitable,
	})
	if err != nil {
		m.logger.Error("create ticket thread", "user", user.ID, "err", err)
		return m.followup(e, "Sorry, I couldn't open a ticket. Please message an admin directly.")
	}

	if err := rest.AddThreadMember(thread.ID(), user.ID); err != nil {
		m.logger.Error("add ticket member", "thread", thread.ID(), "user", user.ID, "err", err)
	}

	// Mentioning the staff role adds its members to the private thread.
	_, err = rest.CreateMessage(thread.ID(), discord.MessageCreate{
		Content: fmt.Sprintf("%s opened a ticket for %s.", user.Mention(), discord.RoleMention(m.staffRole)),
		Embeds: []discord.Embed{discord.NewEmbed().
			WithTitle(topic).
			WithDescription(details)},
		Components: []discord.LayoutComponent{discord.NewActionRow(
			discord.NewPrimaryButton("Claim", claimID),
			discord.NewDangerButton("Close", closeID),
		)},
		AllowedMentions: &discord.AllowedMentions{
			Users: []snowflake.ID{user.ID},
			Roles: []snowflake.ID{m.staffRole},
		},
	})
	if err != nil {
		m.logger.Error("post ticket message", "thread", thread.ID(), "err", err)
	}

	return m.followup(e, fmt.Sprintf("Your ticket is open: %s. Only you and staff can see it.", discord.ChannelMention(thread.ID())))
}

func (m Module) claim(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	if !m.isStaff(e) {
		return e.CreateMessage(ephemeral("Only staff can claim tickets."))
	}
	return e.CreateMessage(discord.MessageCreate{
		Content:         fmt.Sprintf("%s is handling this ticket.", e.User().Mention()),
		AllowedMentions: &discord.AllowedMentions{},
	})
}

func (m Module) close(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	threadID := e.Channel().ID()
	if err := e.CreateMessage(discord.MessageCreate{
		Content:         fmt.Sprintf("Ticket closed by %s.", e.User().Mention()),
		AllowedMentions: &discord.AllowedMentions{},
	}); err != nil {
		return err
	}

	archived, locked := true, true
	if _, err := e.Client().Rest.UpdateChannel(threadID, discord.GuildThreadUpdate{
		Archived: &archived,
		Locked:   &locked,
	}); err != nil {
		m.logger.Error("close ticket", "thread", threadID, "err", err)
	}
	return nil
}

func (m Module) isStaff(e *handler.ComponentEvent) bool {
	member := e.Member()
	if member == nil {
		return false
	}
	for _, id := range member.RoleIDs {
		if id == m.staffRole {
			return true
		}
	}
	return member.Permissions.Has(discord.PermissionAdministrator)
}

func (m Module) followup(e *handler.ModalEvent, content string) error {
	_, err := e.CreateFollowupMessage(discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral})
	return err
}

// threadName builds a readable thread name like "ticket-alice-account-problem",
// kept within Discord's length limit.
func threadName(username, topic string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return unicode.ToLower(r)
		case unicode.IsSpace(r) || r == '-' || r == '_':
			return '-'
		default:
			return -1
		}
	}, username+" "+topic)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	name := "ticket-" + strings.Trim(slug, "-")
	if r := []rune(name); len(r) > maxThreadName {
		name = strings.TrimRight(string(r[:maxThreadName]), "-")
	}
	return name
}

func ephemeral(content string) discord.MessageCreate {
	return discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral}
}
