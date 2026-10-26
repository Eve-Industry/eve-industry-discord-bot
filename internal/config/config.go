// Package config loads bot settings. Secrets come from environment
// variables; channel and role IDs come from a YAML file.
package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/disgoorg/snowflake/v2"
	"gopkg.in/yaml.v3"
)

// Config holds everything the bot needs to start.
type Config struct {
	// Token is the Discord bot token, read from DISCORD_TOKEN.
	Token string `yaml:"-"`

	// GuildID is the server the bot serves. Slash commands are registered
	// to this guild so changes show up instantly.
	GuildID snowflake.ID `yaml:"guild_id"`

	Channels Channels `yaml:"channels"`
	Roles    Roles    `yaml:"roles"`
	Welcome  Welcome  `yaml:"welcome"`

	// Database is the path of the SQLite file. Defaults to bot.db.
	Database string `yaml:"database"`

	ESI ESI `yaml:"esi"`
}

// ESI holds settings for EVE Online's API.
type ESI struct {
	// Contact (an email or Discord name) is sent in the User-Agent so CCP
	// can reach whoever runs this bot.
	Contact string `yaml:"contact"`
}

// Welcome controls the message posted when someone joins.
type Welcome struct {
	// Message is shown under the greeting. Empty uses the default text.
	Message string `yaml:"message"`
}

// Roles names the roles the bot gives access to.
type Roles struct {
	// Staff can see private support tickets. Admins for now.
	Staff snowflake.ID `yaml:"staff"`

	// Member is given when someone accepts the rules. Leave it unset to
	// turn the rules gate off.
	Member snowflake.ID `yaml:"member"`

	// SelfAssign lists the roles members can pick for themselves.
	SelfAssign []SelfAssignRole `yaml:"self_assign"`
}

// SelfAssignRole is one entry in the role picker.
type SelfAssignRole struct {
	ID          snowflake.ID `yaml:"id"`
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
}

// Channels names the channels the bot welcomes into and routes to.
type Channels struct {
	General            snowflake.ID `yaml:"general"`
	FeatureRequests    snowflake.ID `yaml:"feature_requests"`
	Bugs               snowflake.ID `yaml:"bugs"`
	SelfHostingSupport snowflake.ID `yaml:"self_hosting_support"`

	// Tickets is a text channel. Each staff ticket is a private thread in it.
	Tickets snowflake.ID `yaml:"tickets"`

	// ModLog is a staff-only text channel for joins, leaves and role changes.
	ModLog snowflake.ID `yaml:"mod_log"`

	// Rules holds the rules and the Accept button for the rules gate.
	Rules snowflake.ID `yaml:"rules"`
}

// Load reads the YAML file at path and the DISCORD_TOKEN environment variable.
func Load(path string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}

	cfg.Token = os.Getenv("DISCORD_TOKEN")
	if cfg.Database == "" {
		cfg.Database = "bot.db"
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []error
	if c.Token == "" {
		errs = append(errs, errors.New("DISCORD_TOKEN is not set"))
	}
	if c.GuildID == 0 {
		errs = append(errs, errors.New("guild_id is not set"))
	}
	if len(c.Roles.SelfAssign) > 25 {
		errs = append(errs, errors.New("roles.self_assign has more than 25 roles, the most a Discord menu can show"))
	}
	for i, r := range c.Roles.SelfAssign {
		if r.ID == 0 || r.Name == "" {
			errs = append(errs, fmt.Errorf("roles.self_assign[%d] needs an id and a name", i))
		}
	}
	return errors.Join(errs...)
}
