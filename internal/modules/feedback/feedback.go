// Package feedback turns short forms into tidy posts in the bugs and
// feature-requests forums.
package feedback

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
)

// Custom IDs of the buttons that open each form.
const (
	BugButtonID     = "/feedback/bug"
	FeatureButtonID = "/feedback/feature"
)

const (
	bugModalID     = "/feedback/bug/submit"
	featureModalID = "/feedback/feature/submit"

	titleID    = "title"
	summaryID  = "summary"
	stepsID    = "steps"
	setupID    = "setup"
	problemID  = "problem"
	proposalID = "proposal"

	bugColor     = 0xE5534B
	featureColor = 0x57AB5A

	maxPostTitle = 100 // Discord's limit for a thread name
)

// field is one labelled section of a post.
type field struct{ name, value string }

// Module serves the bug report and feature request forms.
type Module struct {
	settings config.Source
	logger   *slog.Logger
}

// New returns the feedback module.
func New(settings config.Source, logger *slog.Logger) Module {
	return Module{settings: settings, logger: logger}
}

// Commands declares no slash commands; the forms open from panel buttons.
func (Module) Commands() []discord.ApplicationCommandCreate { return nil }

// Register adds the form buttons and submit handlers.
func (m Module) Register(r handler.Router) {
	r.ButtonComponent(BugButtonID, m.openBugForm)
	r.ButtonComponent(FeatureButtonID, m.openFeatureForm)
	r.Modal(bugModalID, m.submitBug)
	r.Modal(featureModalID, m.submitFeature)
}

func (m Module) openBugForm(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	return e.Modal(discord.NewModalCreate(bugModalID, "Report a bug",
		discord.NewLabel("Short title", discord.NewTextInput(titleID, discord.TextInputStyleShort).
			WithRequired(true).WithMaxLength(80).
			WithPlaceholder("e.g. Blueprint costs show as zero")),
		discord.NewLabel("What happened?", discord.NewTextInput(summaryID, discord.TextInputStyleParagraph).
			WithRequired(true).WithMaxLength(1000).
			WithPlaceholder("What you expected, and what you saw instead")),
		discord.NewLabel("Steps to reproduce", discord.NewTextInput(stepsID, discord.TextInputStyleParagraph).
			WithRequired(false).WithMaxLength(1000)),
		discord.NewLabel("Version and setup", discord.NewTextInput(setupID, discord.TextInputStyleShort).
			WithRequired(false).WithMaxLength(200).
			WithPlaceholder("e.g. v1.2, Docker on Linux")),
	))
}

func (m Module) openFeatureForm(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
	return e.Modal(discord.NewModalCreate(featureModalID, "Request a feature",
		discord.NewLabel("Short title", discord.NewTextInput(titleID, discord.TextInputStyleShort).
			WithRequired(true).WithMaxLength(80).
			WithPlaceholder("e.g. Export build costs to CSV")),
		discord.NewLabel("What problem would it solve?", discord.NewTextInput(problemID, discord.TextInputStyleParagraph).
			WithRequired(true).WithMaxLength(1000)),
		discord.NewLabel("What would you like to see?", discord.NewTextInput(proposalID, discord.TextInputStyleParagraph).
			WithRequired(false).WithMaxLength(1000)),
	))
}

func (m Module) submitBug(e *handler.ModalEvent) error {
	return m.post(e, m.settings.Settings().Channels.Bugs, "bug report", bugColor, []field{
		{"What happened", e.Data.Text(summaryID)},
		{"Steps to reproduce", e.Data.Text(stepsID)},
		{"Version and setup", e.Data.Text(setupID)},
	})
}

func (m Module) submitFeature(e *handler.ModalEvent) error {
	return m.post(e, m.settings.Settings().Channels.FeatureRequests, "feature request", featureColor, []field{
		{"Problem", e.Data.Text(problemID)},
		{"Idea", e.Data.Text(proposalID)},
	})
}

func (m Module) post(e *handler.ModalEvent, forumID snowflake.ID, kind string, color int, fields []field) error {
	if forumID == 0 {
		return e.CreateMessage(ephemeral(fmt.Sprintf("The %s forum isn't set up yet. Please tell a staff member.", kind)))
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}

	user := e.User()
	post, err := e.Client().Rest.CreatePostInThreadChannel(forumID, discord.ThreadChannelPostCreate{
		Name:    postTitle(e.Data.Text(titleID)),
		Message: postMessage(user.ID, color, fields),
	})
	if err != nil {
		m.logger.Error("create forum post", "forum", forumID, "user", user.ID, "err", err)
		return m.followup(e, fmt.Sprintf("Sorry, I couldn't post your %s. You can post it in %s yourself.", kind, discord.ChannelMention(forumID)))
	}
	return m.followup(e, fmt.Sprintf("Thanks! Your %s is posted: %s", kind, discord.ChannelMention(post.ID())))
}

func (m Module) followup(e *handler.ModalEvent, content string) error {
	_, err := e.CreateFollowupMessage(ephemeral(content))
	return err
}

// postMessage is the first message of a forum post: who sent it and the
// filled-in sections, skipping any left blank.
func postMessage(userID snowflake.ID, color int, fields []field) discord.MessageCreate {
	var b strings.Builder
	for _, f := range fields {
		v := strings.TrimSpace(f.value)
		if v == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "**%s**\n%s", f.name, v)
	}
	return discord.MessageCreate{
		Content:         fmt.Sprintf("Submitted by %s", discord.UserMention(userID)),
		Embeds:          []discord.Embed{discord.NewEmbed().WithDescription(b.String()).WithColor(color)},
		AllowedMentions: &discord.AllowedMentions{Users: []snowflake.ID{userID}},
	}
}

// postTitle cleans up a title for use as a forum post name.
func postTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return "Untitled"
	}
	if r := []rune(title); len(r) > maxPostTitle {
		title = strings.TrimSpace(string(r[:maxPostTitle]))
	}
	return title
}

func ephemeral(content string) discord.MessageCreate {
	return discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral}
}
