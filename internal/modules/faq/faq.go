// Package faq answers common questions with /faq. Staff add and edit the
// answers with /faq-admin; they are kept in the bot's database.
package faq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

const (
	saveModalID = "/faq-admin/save"
	nameInputID = "name"
	textInputID = "answer"

	maxNameLen   = 50
	maxAnswerLen = 4000 // stays under Discord's 4096 embed limit
	maxChoices   = 25   // Discord's autocomplete limit

	answerColor = 0x539BF5
)

// Store is the part of the database the FAQ needs.
type Store interface {
	SetFAQ(ctx context.Context, f store.FAQ) error
	GetFAQ(ctx context.Context, name string) (store.FAQ, error)
	DeleteFAQ(ctx context.Context, name string) error
	SearchFAQNames(ctx context.Context, query string, limit int) ([]string, error)
}

// Module serves /faq and /faq-admin.
type Module struct {
	store  Store
	logger *slog.Logger
}

// New returns the FAQ module.
func New(s Store, logger *slog.Logger) Module {
	return Module{store: s, logger: logger}
}

// Commands declares /faq for everyone and /faq-admin for server managers.
func (Module) Commands() []discord.ApplicationCommandCreate {
	topic := func(desc string) discord.ApplicationCommandOptionString {
		return discord.ApplicationCommandOptionString{
			Name: "topic", Description: desc, Required: true, Autocomplete: true,
		}
	}
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "faq",
			Description: "Post the answer to a common question",
			Options: []discord.ApplicationCommandOption{
				topic("Which question"),
				discord.ApplicationCommandOptionUser{Name: "for", Description: "Mention someone with the answer"},
			},
		},
		discord.SlashCommandCreate{
			Name:                     "faq-admin",
			Description:              "Add, edit or remove FAQ answers",
			DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageGuild),
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{
					Name:        "set",
					Description: "Add an answer, or edit an existing one",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionString{
							Name: "topic", Description: "Short name, e.g. docker", Required: true, Autocomplete: true,
						},
					},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "delete",
					Description: "Remove an answer",
					Options:     []discord.ApplicationCommandOption{topic("Which answer to remove")},
				},
			},
		},
	}
}

// Register adds the FAQ handlers.
func (m Module) Register(r handler.Router) {
	r.SlashCommand("/faq", m.show)
	r.Autocomplete("/faq", m.complete)
	r.SlashCommand("/faq-admin/set", m.openEditor)
	r.Autocomplete("/faq-admin/set", m.complete)
	r.SlashCommand("/faq-admin/delete", m.delete)
	r.Autocomplete("/faq-admin/delete", m.complete)
	r.Modal(saveModalID, m.save)
}

func (m Module) show(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	f, err := m.store.GetFAQ(e.Ctx, data.String("topic"))
	if errors.Is(err, store.ErrNotFound) {
		return e.CreateMessage(ephemeral("I don't have an answer for that yet."))
	}
	if err != nil {
		m.logger.Error("get faq", "err", err)
		return e.CreateMessage(ephemeral("Something went wrong looking that up."))
	}

	msg := discord.MessageCreate{
		Embeds:          []discord.Embed{answerEmbed(f)},
		AllowedMentions: &discord.AllowedMentions{},
	}
	if user, ok := data.OptUser("for"); ok {
		msg.Content = user.Mention()
		msg.AllowedMentions.Users = []snowflake.ID{user.ID}
	}
	return e.CreateMessage(msg)
}

func (m Module) complete(e *handler.AutocompleteEvent) error {
	names, err := m.store.SearchFAQNames(e.Ctx, e.Data.String("topic"), maxChoices)
	if err != nil {
		m.logger.Error("search faq", "err", err)
	}
	choices := make([]discord.AutocompleteChoice, 0, len(names))
	for _, n := range names {
		choices = append(choices, discord.AutocompleteChoiceString{Name: n, Value: n})
	}
	return e.AutocompleteResult(choices)
}

func (m Module) openEditor(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	name := cleanName(data.String("topic"))
	answer := ""
	if f, err := m.store.GetFAQ(e.Ctx, name); err == nil {
		name, answer = f.Name, f.Answer
	}
	return e.Modal(editor(name, answer))
}

func (m Module) save(e *handler.ModalEvent) error {
	name := cleanName(e.Data.Text(nameInputID))
	answer := strings.TrimSpace(e.Data.Text(textInputID))
	if name == "" || answer == "" {
		return e.CreateMessage(ephemeral("The topic and answer can't be empty."))
	}
	err := m.store.SetFAQ(e.Ctx, store.FAQ{
		Name:      name,
		Answer:    answer,
		UpdatedBy: e.User().ID.String(),
		UpdatedAt: time.Now(),
	})
	if err != nil {
		m.logger.Error("save faq", "name", name, "err", err)
		return e.CreateMessage(ephemeral("Something went wrong saving that answer."))
	}
	return e.CreateMessage(ephemeral(fmt.Sprintf("Saved. Anyone can now use `/faq topic:%s`.", name)))
}

func (m Module) delete(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	name := data.String("topic")
	err := m.store.DeleteFAQ(e.Ctx, name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return e.CreateMessage(ephemeral("There's no answer with that name."))
	case err != nil:
		m.logger.Error("delete faq", "name", name, "err", err)
		return e.CreateMessage(ephemeral("Something went wrong removing that answer."))
	}
	return e.CreateMessage(ephemeral(fmt.Sprintf("Removed **%s**.", name)))
}

// editor is the modal for adding or editing an answer, pre-filled when the
// answer already exists.
func editor(name, answer string) discord.ModalCreate {
	nameInput := discord.NewTextInput(nameInputID, discord.TextInputStyleShort).
		WithRequired(true).WithMaxLength(maxNameLen)
	if name != "" {
		nameInput = nameInput.WithValue(name)
	}
	answerInput := discord.NewTextInput(textInputID, discord.TextInputStyleParagraph).
		WithRequired(true).WithMaxLength(maxAnswerLen)
	if answer != "" {
		answerInput = answerInput.WithValue(answer)
	}
	return discord.NewModalCreate(saveModalID, "FAQ answer",
		discord.NewLabel("Topic", nameInput),
		discord.NewLabel("Answer (Markdown works)", answerInput),
	)
}

func answerEmbed(f store.FAQ) discord.Embed {
	return discord.NewEmbed().
		WithTitle(f.Name).
		WithDescription(f.Answer).
		WithColor(answerColor)
}

// cleanName trims and collapses spaces and enforces the length limit.
func cleanName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxNameLen {
		s = strings.TrimSpace(string(r[:maxNameLen]))
	}
	return s
}

func ephemeral(content string) discord.MessageCreate {
	return discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral}
}
