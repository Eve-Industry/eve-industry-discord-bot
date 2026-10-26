// Package bot wires the Discord client to the feature modules.
package bot

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
)

// Module is one feature of the bot. It declares its slash commands and
// registers handlers for its commands and components on the router.
type Module interface {
	Commands() []discord.ApplicationCommandCreate
	Register(r handler.Router)
}

// Bot owns the Discord client and the modules it serves.
type Bot struct {
	cfg     config.Config
	logger  *slog.Logger
	modules []Module
}

// New returns a Bot that serves the given modules.
func New(cfg config.Config, logger *slog.Logger, modules ...Module) *Bot {
	return &Bot{cfg: cfg, logger: logger, modules: modules}
}

// Run connects to Discord, syncs slash commands and blocks until ctx is done.
func (b *Bot) Run(ctx context.Context) error {
	r := handler.New()
	var commands []discord.ApplicationCommandCreate
	for _, m := range b.modules {
		commands = append(commands, m.Commands()...)
		m.Register(r)
	}

	client, err := disgo.New(b.cfg.Token,
		bot.WithGatewayConfigOpts(gateway.WithIntents(
			gateway.IntentGuilds,
			gateway.IntentGuildMembers,
		)),
		bot.WithEventListeners(r),
	)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer client.Close(context.Background())

	if err := handler.SyncCommands(client, commands, []snowflake.ID{b.cfg.GuildID}); err != nil {
		return fmt.Errorf("sync commands: %w", err)
	}

	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}
	b.logger.Info("bot is running", "guild", b.cfg.GuildID, "commands", len(commands))

	<-ctx.Done()
	b.logger.Info("shutting down")
	return nil
}
