// Package welcome greets new members and posts the routing panel that points
// them to the right channel.
package welcome

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/feedback"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/roles"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/tickets"
)

const (
	startButtonID  = "/welcome/start"
	acceptButtonID = "/welcome/accept"

	defaultMessage = "Glad to have you here. Say hello in %s, or use the buttons below to find help, suggest a feature or report a bug."

	accentColor = 0xF5A623
)

// Module serves the welcome message and routing panel.
type Module struct {
	cfg    config.Config
	logger *slog.Logger
}

// New returns the welcome module.
func New(cfg config.Config, logger *slog.Logger) Module {
	return Module{cfg: cfg, logger: logger}
}

// Commands declares /setup panel and /setup rules, limited to members who
// can manage the server.
func (Module) Commands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:                     "setup",
			Description:              "Admin tools for the bot",
			DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageGuild),
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{
					Name:        "panel",
					Description: "Post the routing panel in this channel",
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "rules",
					Description: "Post the Accept rules button in this channel",
				},
			},
		},
	}
}

// Register adds the /setup, Get started and Accept rules handlers.
func (m Module) Register(r handler.Router) {
	r.SlashCommand("/setup/panel", m.postPanel)
	r.SlashCommand("/setup/rules", m.postRules)
	r.ButtonComponent(startButtonID, m.showPanel)
	r.ButtonComponent(acceptButtonID, m.acceptRules)
}

// gated reports whether new members must accept the rules first. When they
// must, the welcome is posted on accepting rather than on joining, so they
// can see it.
func (m Module) gated() bool { return m.cfg.Roles.Member != 0 }

// Listeners greets members when they join.
func (m Module) Listeners() []bot.EventListener {
	return []bot.EventListener{bot.NewListenerFunc(m.onJoin)}
}

func (m Module) onJoin(e *events.GuildMemberJoin) {
	if e.GuildID != m.cfg.GuildID || e.Member.User.Bot || m.gated() {
		return
	}
	m.greet(e.Client(), e.Member)
}

func (m Module) greet(client *bot.Client, member discord.Member) {
	if m.cfg.Channels.General == 0 {
		return
	}
	if _, err := client.Rest.CreateMessage(m.cfg.Channels.General, greeting(m.cfg, member)); err != nil {
		m.logger.Error("post welcome", "user", member.User.ID, "err", err)
	}
}

func (m Module) postRules(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	if !m.gated() {
		return e.CreateMessage(ephemeral("Set `roles.member` in the config first, then run this again."))
	}
	if _, err := e.Client().Rest.CreateMessage(e.Channel().ID(), rulesGate()); err != nil {
		m.logger.Error("post rules gate", "channel", e.Channel().ID(), "err", err)
		return e.CreateMessage(ephemeral("I couldn't post here. Check that I can send messages in this channel."))
	}
	return e.CreateMessage(ephemeral("Accept button posted. Hide your other channels from @everyone and show them to the member role."))
}

func (m Module) acceptRules(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	member := e.Member()
	if member == nil || !m.gated() {
		return e.CreateMessage(ephemeral("There's nothing to accept here."))
	}
	for _, id := range member.RoleIDs {
		if id == m.cfg.Roles.Member {
			return e.CreateMessage(ephemeral("You've already accepted the rules."))
		}
	}
	if err := e.Client().Rest.AddMemberRole(m.cfg.GuildID, member.User.ID, m.cfg.Roles.Member); err != nil {
		m.logger.Error("give member role", "user", member.User.ID, "err", err)
		return e.CreateMessage(ephemeral("Sorry, I couldn't unlock the server for you. Please tell a staff member."))
	}
	m.greet(e.Client(), member.Member)

	p := panel(m.cfg)
	p.Content = "Thanks! The server is unlocked."
	p.Flags = discord.MessageFlagEphemeral
	return e.CreateMessage(p)
}

// rulesGate is the message under the rules with the Accept button.
func rulesGate() discord.MessageCreate {
	return discord.MessageCreate{
		Content: "Read the rules above, then press **Accept** to unlock the rest of the server.",
		Components: []discord.LayoutComponent{discord.NewActionRow(
			discord.NewSuccessButton("Accept", acceptButtonID),
		)},
	}
}

func (m Module) postPanel(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	if _, err := e.Client().Rest.CreateMessage(e.Channel().ID(), panel(m.cfg)); err != nil {
		m.logger.Error("post panel", "channel", e.Channel().ID(), "err", err)
		return e.CreateMessage(ephemeral("I couldn't post here. Check that I can send messages in this channel."))
	}
	return e.CreateMessage(ephemeral("Panel posted. Pin it so new members find it."))
}

func (m Module) showPanel(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	p := panel(m.cfg)
	p.Flags = discord.MessageFlagEphemeral
	return e.CreateMessage(p)
}

// greeting is the message posted in #general when someone joins.
func greeting(cfg config.Config, member discord.Member) discord.MessageCreate {
	text := cfg.Welcome.Message
	if text == "" {
		text = fmt.Sprintf(defaultMessage, discord.ChannelMention(cfg.Channels.General))
	}
	return discord.MessageCreate{
		Content: fmt.Sprintf("Welcome %s!", member.Mention()),
		Embeds: []discord.Embed{discord.NewEmbed().
			WithDescription(text).
			WithThumbnail(member.EffectiveAvatarURL()).
			WithColor(accentColor)},
		Components: []discord.LayoutComponent{discord.NewActionRow(
			discord.NewPrimaryButton("Get started", startButtonID),
		)},
		AllowedMentions: &discord.AllowedMentions{Users: []snowflake.ID{member.User.ID}},
	}
}

// panel is the routing message: where to go for what, with buttons.
func panel(cfg config.Config) discord.MessageCreate {
	ch := cfg.Channels
	var lines []string
	var links []discord.InteractiveComponent
	add := func(id snowflake.ID, label, line string) {
		if id == 0 {
			return
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", discord.ChannelMention(id), line))
		links = append(links, discord.NewLinkButton(label, channelURL(cfg.GuildID, id)))
	}
	add(ch.General, "General", "chat with everyone")
	add(ch.SelfHostingSupport, "Self-hosting help", "help running it yourself")

	var actions []discord.InteractiveComponent
	form := func(id snowflake.ID, label, buttonID, line string) {
		if id == 0 {
			return
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", discord.ChannelMention(id), line))
		actions = append(actions, discord.NewSecondaryButton(label, buttonID))
	}
	form(ch.FeatureRequests, "Request a feature", feedback.FeatureButtonID, "suggest something new")
	form(ch.Bugs, "Report a bug", feedback.BugButtonID, "something broken? tell us here")

	if ch.Tickets != 0 && cfg.Roles.Staff != 0 {
		lines = append(lines, "- **Contact staff**: a private ticket only you and staff can see")
		actions = append(actions, discord.NewSecondaryButton("Contact staff", tickets.OpenButtonID))
	}
	if len(cfg.Roles.SelfAssign) > 0 {
		lines = append(lines, "- **Pick roles**: choose what you're into")
		actions = append(actions, discord.NewSecondaryButton("Pick roles", roles.PickButtonID))
	}

	var rows []discord.LayoutComponent
	if len(links) > 0 {
		rows = append(rows, discord.NewActionRow(links...))
	}
	if len(actions) > 0 {
		rows = append(rows, discord.NewActionRow(actions...))
	}

	return discord.MessageCreate{
		Embeds: []discord.Embed{discord.NewEmbed().
			WithTitle("Where to go").
			WithDescription(strings.Join(lines, "\n")).
			WithColor(accentColor)},
		Components: rows,
	}
}

func channelURL(guildID, channelID snowflake.ID) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s", guildID, channelID)
}

func ephemeral(content string) discord.MessageCreate {
	return discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral}
}
