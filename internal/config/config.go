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
}

// Roles names the roles the bot gives access to.
type Roles struct {
	// Staff can see private support tickets. Admins for now.
	Staff snowflake.ID `yaml:"staff"`
}

// Channels names the channels the bot welcomes into and routes to.
type Channels struct {
	General            snowflake.ID `yaml:"general"`
	FeatureRequests    snowflake.ID `yaml:"feature_requests"`
	Bugs               snowflake.ID `yaml:"bugs"`
	SelfHostingSupport snowflake.ID `yaml:"self_hosting_support"`
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
	return errors.Join(errs...)
}
