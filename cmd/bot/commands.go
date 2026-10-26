package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/admin"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `Usage:
  bot [-config config.yaml]                          run the bot
  bot [-config config.yaml] admin invite <email>     invite a dashboard admin
  bot [-config config.yaml] admin reset <email>      reset a locked-out admin

Flags:
`)
	flag.PrintDefaults()
}

// runCommand runs a one-off command instead of the bot.
func runCommand(configPath string, args []string, out io.Writer) error {
	if len(args) != 3 || args[0] != "admin" || (args[1] != "invite" && args[1] != "reset") {
		usage()
		return errors.New("unknown command")
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return err
	}
	adminCfg, on, err := admin.ConfigFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	if !on {
		return errors.New("set ADMIN_PORT (and the other ADMIN_* settings the bot runs with) so the link points at the dashboard")
	}
	db, err := store.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now()
	email := args[2]
	var link string
	var expires time.Time
	switch args[1] {
	case "invite":
		link, expires, err = admin.Invite(ctx, db, adminCfg.BaseURL(), email, "command line", now)
		if errors.Is(err, admin.ErrAlreadyAdmin) {
			return fmt.Errorf("%s is already an admin; use: bot admin reset %s", email, email)
		}
	case "reset":
		var a store.Admin
		a, err = db.GetAdminByEmail(ctx, email)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no admin has the email %s; use: bot admin invite %s", email, email)
		}
		if err == nil {
			link, expires, err = admin.Reset(ctx, db, adminCfg.BaseURL(), a, "command line", now)
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Send this link to %s. It works once, until %s:\n\n  %s\n",
		email, expires.Local().Format("2 Jan 15:04 MST"), link)
	return nil
}
