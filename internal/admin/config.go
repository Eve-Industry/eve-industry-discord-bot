// Package admin serves the admin dashboard's HTTP API: sign-in with a
// password and an authenticator code, and the settings and audit endpoints
// behind it.
package admin

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/auth"
)

// Config says where and how the dashboard listens. It comes from ADMIN_*
// environment variables; the dashboard is off unless ADMIN_PORT is set.
type Config struct {
	// Port is the TCP port to listen on.
	Port int
	// Host is the interface to listen on; empty means all of them.
	Host string
	// TLSCert and TLSKey are PEM files. When set, the dashboard serves HTTPS
	// itself.
	TLSCert, TLSKey string
	// PublicURL is the https address people open when a reverse proxy or
	// tunnel terminates TLS in front of the dashboard.
	PublicURL *url.URL
	// Key encrypts authenticator secrets at rest. Nil when unset.
	Key []byte
	// BootstrapEmail gets an invite on first start, when there are no admins.
	BootstrapEmail string
}

// ConfigFromEnv reads the ADMIN_* variables through getenv. It reports
// false when ADMIN_PORT is unset, which leaves the dashboard off.
func ConfigFromEnv(getenv func(string) string) (Config, bool, error) {
	portText := strings.TrimSpace(getenv("ADMIN_PORT"))
	if portText == "" {
		return Config{}, false, nil
	}
	var errs []error
	cfg := Config{
		Host:           strings.TrimSpace(getenv("ADMIN_HOST")),
		TLSCert:        strings.TrimSpace(getenv("ADMIN_TLS_CERT")),
		TLSKey:         strings.TrimSpace(getenv("ADMIN_TLS_KEY")),
		BootstrapEmail: strings.TrimSpace(getenv("ADMIN_BOOTSTRAP_EMAIL")),
	}

	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, fmt.Errorf("ADMIN_PORT %q is not a port number", portText))
	}
	cfg.Port = port

	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		errs = append(errs, errors.New("set both ADMIN_TLS_CERT and ADMIN_TLS_KEY, or neither"))
	}

	if raw := strings.TrimSpace(getenv("ADMIN_PUBLIC_URL")); raw != "" {
		u, err := url.Parse(strings.TrimRight(raw, "/"))
		switch {
		case err != nil || u.Host == "":
			errs = append(errs, fmt.Errorf("ADMIN_PUBLIC_URL %q is not a URL", raw))
		case u.Scheme != "https" && !isLoopback(u.Hostname()):
			errs = append(errs, errors.New("ADMIN_PUBLIC_URL must start with https://"))
		default:
			cfg.PublicURL = u
		}
	}

	// Without HTTPS in front, passwords would cross the network in the
	// clear, so plain HTTP is only allowed on this machine.
	if !cfg.secure() {
		if cfg.Host == "" {
			cfg.Host = "127.0.0.1"
		} else if !isLoopback(cfg.Host) {
			errs = append(errs, errors.New("the dashboard needs HTTPS to listen beyond this machine: set ADMIN_TLS_CERT and ADMIN_TLS_KEY, or put a proxy in front and set ADMIN_PUBLIC_URL"))
		}
	}

	if raw := getenv("ADMIN_ENCRYPTION_KEY"); raw != "" {
		key, err := auth.ParseKey(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("ADMIN_ENCRYPTION_KEY: %w (make one with: openssl rand -base64 32)", err))
		}
		cfg.Key = key
	}
	return cfg, true, errors.Join(errs...)
}

// secure reports whether browsers reach the dashboard over HTTPS.
func (c Config) secure() bool {
	return c.TLSCert != "" || (c.PublicURL != nil && c.PublicURL.Scheme == "https")
}

// Addr is the address to listen on.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// BaseURL is the address people open, used in invite links.
func (c Config) BaseURL() string {
	if c.PublicURL != nil {
		return c.PublicURL.String()
	}
	scheme := "http"
	if c.TLSCert != "" {
		scheme = "https"
	}
	host := c.Host
	if host == "" || isLoopback(host) {
		host = "localhost"
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, strconv.Itoa(c.Port)))
}

// proxied reports whether a reverse proxy sits in front, so the client's
// address arrives in X-Forwarded-For.
func (c Config) proxied() bool {
	return c.PublicURL != nil && c.TLSCert == ""
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
