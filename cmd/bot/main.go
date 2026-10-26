// Command bot runs the EVE Industry Discord bot.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/admin"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/bot"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/esi"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/faq"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/feedback"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/modules/market"
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
	flag.Usage = usage
	flag.Parse()

	if args := flag.Args(); len(args) > 0 {
		if err := runCommand(*configPath, args, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

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
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	live, err := settings.Load(ctx, db, cfg.Settings, logger)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	adminCfg, adminOn, err := admin.ConfigFromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("admin dashboard: %w", err)
	}
	if adminOn {
		srv, err := admin.New(adminCfg, db, live, logger)
		if err != nil {
			return fmt.Errorf("admin dashboard: %w", err)
		}
		if err := admin.Bootstrap(ctx, db, adminCfg, logger); err != nil {
			return err
		}
		go func() {
			if err := srv.Run(ctx); err != nil {
				cancel(err)
			}
		}()
	}

	b := bot.New(cfg, logger,
		ping.New(),
		welcome.New(cfg.GuildID, live, logger),
		roles.New(cfg.GuildID, live, logger),
		tickets.New(live, logger),
		feedback.New(live, logger),
		modlog.New(cfg.GuildID, live, logger),
		faq.New(db, logger),
		market.New(esi.New(esi.DefaultURL, userAgent(cfg)), logger),
	)
	err = b.Run(ctx)
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

// userAgent identifies the bot to ESI, as CCP asks, so they can reach the
// operator if it misbehaves.
func userAgent(cfg config.Config) string {
	ua := "eve-industry-discord-bot (+https://github.com/Eve-Industry/eve-industry-discord-bot)"
	if cfg.ESI.Contact != "" {
		ua += " contact: " + cfg.ESI.Contact
	}
	return ua
}
