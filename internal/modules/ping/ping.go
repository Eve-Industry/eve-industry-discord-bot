// Package ping provides /ping, a health check for the bot.
package ping

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
)

// Module answers /ping.
type Module struct{}

// New returns the ping module.
func New() Module { return Module{} }

// Commands declares /ping.
func (Module) Commands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "ping",
			Description: "Check that the bot is online",
		},
	}
}

// Register adds the /ping handler.
func (Module) Register(r handler.Router) {
	r.SlashCommand("/ping", func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		return e.CreateMessage(discord.MessageCreate{
			Content: "Pong! The bot is online.",
			Flags:   discord.MessageFlagEphemeral,
		})
	})
}
