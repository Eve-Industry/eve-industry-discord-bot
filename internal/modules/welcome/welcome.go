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
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/roles"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/tickets"
)

const (
	startButtonID = "/welcome/start"

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

// Commands declares /setup panel, limited to members who can manage the server.
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
			},
		},
	}
}

// Register adds the /setup panel and Get started handlers.
func (m Module) Register(r handler.Router) {
	r.SlashCommand("/setup/panel", m.postPanel)
	r.ButtonComponent(startButtonID, m.showPanel)
}

// Listeners greets members when they join.
func (m Module) Listeners() []bot.EventListener {
	return []bot.EventListener{bot.NewListenerFunc(m.onJoin)}
}

func (m Module) onJoin(e *events.GuildMemberJoin) {
	if e.GuildID != m.cfg.GuildID || m.cfg.Channels.General == 0 || e.Member.User.Bot {
		return
	}
	if _, err := e.Client().Rest.CreateMessage(m.cfg.Channels.General, greeting(m.cfg, e.Member)); err != nil {
		m.logger.Error("post welcome", "user", e.Member.User.ID, "err", err)
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
	add(ch.FeatureRequests, "Request a feature", "suggest something new")
	add(ch.Bugs, "Report a bug", "something broken? tell us here")

	var actions []discord.InteractiveComponent
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
