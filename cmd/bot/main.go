// Command bot runs the EVE Industry Discord bot.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/bot"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/faq"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/feedback"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/modlog"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/ping"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/roles"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/tickets"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/welcome"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/settings"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*configPath, logger); err != nil {
		logger.Error("bot stopped", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := store.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	live, err := settings.Load(ctx, db, cfg.Settings, logger)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	b := bot.New(cfg, logger,
		ping.New(),
		welcome.New(cfg.GuildID, live, logger),
		roles.New(cfg.GuildID, live, logger),
		tickets.New(live, logger),
		feedback.New(live, logger),
		modlog.New(cfg.GuildID, live, logger),
		faq.New(db, logger),
	)
	return b.Run(ctx)
}
