// Package config layers configuration from defaults, an optional TOML file,
// environment variables (CALDAV_*), and command-line flags, in that order of
// increasing precedence.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds every runtime setting. Field comments name the matching
// CALDAV_* environment variable.
type Config struct {
	// Core
	ListenAddr        string        `toml:"listen_addr"`         // CALDAV_LISTEN_ADDR
	ExternalURL       string        `toml:"external_url"`        // CALDAV_EXTERNAL_URL
	DBPath            string        `toml:"db_path"`             // CALDAV_DB_PATH
	SessionKey        string        `toml:"session_key"`         // CALDAV_SESSION_KEY (base64 or raw)
	DataKey           string        `toml:"data_key"`            // CALDAV_DATA_KEY
	LogLevel          string        `toml:"log_level"`           // CALDAV_LOG_LEVEL
	TrustProxyHeaders bool          `toml:"trust_proxy_headers"` // CALDAV_TRUST_PROXY_HEADERS
	DefaultTZ         string        `toml:"default_tz"`          // CALDAV_DEFAULT_TZ
	AutoMigrate       bool          `toml:"auto_migrate"`        // CALDAV_AUTO_MIGRATE

	// OIDC
	OIDCIssuer       string `toml:"oidc_issuer"`        // CALDAV_OIDC_ISSUER
	OIDCClientID     string `toml:"oidc_client_id"`     // CALDAV_OIDC_CLIENT_ID
	OIDCClientSecret string `toml:"oidc_client_secret"` // CALDAV_OIDC_CLIENT_SECRET
	OIDCRedirectURL  string `toml:"oidc_redirect_url"`  // CALDAV_OIDC_REDIRECT_URL
	OIDCScopes       string `toml:"oidc_scopes"`        // CALDAV_OIDC_SCOPES
	OIDCAdminEmails  string `toml:"oidc_admin_emails"`  // CALDAV_OIDC_ADMIN_EMAILS

	// External ICS sources
	ICSDefaultPollInterval time.Duration `toml:"ics_default_poll_interval"` // CALDAV_ICS_DEFAULT_POLL_INTERVAL

	// Scheduling and iMIP
	SchedulingEnabled bool          `toml:"scheduling_enabled"`  // CALDAV_SCHEDULING_ENABLED
	SMTPHost          string        `toml:"smtp_host"`           // CALDAV_SMTP_HOST
	SMTPPort          int           `toml:"smtp_port"`           // CALDAV_SMTP_PORT
	SMTPUser          string        `toml:"smtp_user"`           // CALDAV_SMTP_USER
	SMTPPass          string        `toml:"smtp_pass"`           // CALDAV_SMTP_PASS
	SMTPTLS           string        `toml:"smtp_tls"`            // CALDAV_SMTP_TLS
	IMIPFromOverride  string        `toml:"imip_from_override"`  // CALDAV_IMIP_FROM_OVERRIDE
	IMIPReplyAddress  string        `toml:"imip_reply_address"`  // CALDAV_IMIP_REPLY_ADDRESS
	IMAPHost          string        `toml:"imap_host"`           // CALDAV_IMAP_HOST
	IMAPPort          int           `toml:"imap_port"`           // CALDAV_IMAP_PORT
	IMAPUser          string        `toml:"imap_user"`           // CALDAV_IMAP_USER
	IMAPPass          string        `toml:"imap_pass"`           // CALDAV_IMAP_PASS
	IMAPTLS           string        `toml:"imap_tls"`            // CALDAV_IMAP_TLS
	IMAPFolder        string        `toml:"imap_folder"`         // CALDAV_IMAP_FOLDER
	IMAPPollInterval  time.Duration `toml:"imap_poll_interval"`  // CALDAV_IMAP_POLL_INTERVAL
	IMAPProcessedDir  string        `toml:"imap_processed_folder"` // CALDAV_IMAP_PROCESSED_FOLDER
}

func defaults() *Config {
	return &Config{
		ListenAddr:             ":8080",
		DBPath:                 "/var/lib/caldav-share/db.sqlite",
		LogLevel:               "info",
		TrustProxyHeaders:      true,
		DefaultTZ:              "UTC",
		AutoMigrate:            true,
		OIDCScopes:             "openid profile email",
		ICSDefaultPollInterval: 15 * time.Minute,
		SchedulingEnabled:      true,
		SMTPPort:               587,
		SMTPTLS:                "starttls",
		IMAPPort:               993,
		IMAPTLS:                "implicit",
		IMAPFolder:             "INBOX",
		IMAPPollInterval:       60 * time.Second,
	}
}

// Load builds a Config from defaults, an optional TOML file, environment
// variables, and the given flag map (kebab-case keys), in increasing
// precedence. A nil flags map is treated as empty.
func Load(flags map[string]string) (*Config, error) {
	c := defaults()

	configPath := flags["config"]
	if configPath == "" {
		configPath = os.Getenv("CALDAV_CONFIG")
	}
	if configPath != "" {
		if _, err := toml.DecodeFile(configPath, c); err != nil {
			return nil, fmt.Errorf("read config %s: %w", configPath, err)
		}
	}

	if err := c.applyEnv(); err != nil {
		return nil, err
	}
	if err := c.applyFlags(flags); err != nil {
		return nil, err
	}

	if c.OIDCRedirectURL == "" && c.ExternalURL != "" {
		c.OIDCRedirectURL = strings.TrimRight(c.ExternalURL, "/") + "/oidc/callback"
	}
	return c, nil
}

func (c *Config) applyEnv() error {
	str := func(env string, dst *string) {
		if v, ok := os.LookupEnv(env); ok && v != "" {
			*dst = v
		}
	}
	boolean := func(env string, dst *bool) error {
		if v, ok := os.LookupEnv(env); ok && v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("%s: %w", env, err)
			}
			*dst = b
		}
		return nil
	}
	integer := func(env string, dst *int) error {
		if v, ok := os.LookupEnv(env); ok && v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s: %w", env, err)
			}
			*dst = n
		}
		return nil
	}
	dur := func(env string, dst *time.Duration) error {
		if v, ok := os.LookupEnv(env); ok && v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("%s: %w", env, err)
			}
			*dst = d
		}
		return nil
	}

	str("CALDAV_LISTEN_ADDR", &c.ListenAddr)
	str("CALDAV_EXTERNAL_URL", &c.ExternalURL)
	str("CALDAV_DB_PATH", &c.DBPath)
	str("CALDAV_SESSION_KEY", &c.SessionKey)
	str("CALDAV_DATA_KEY", &c.DataKey)
	str("CALDAV_LOG_LEVEL", &c.LogLevel)
	str("CALDAV_DEFAULT_TZ", &c.DefaultTZ)
	str("CALDAV_OIDC_ISSUER", &c.OIDCIssuer)
	str("CALDAV_OIDC_CLIENT_ID", &c.OIDCClientID)
	str("CALDAV_OIDC_CLIENT_SECRET", &c.OIDCClientSecret)
	str("CALDAV_OIDC_REDIRECT_URL", &c.OIDCRedirectURL)
	str("CALDAV_OIDC_SCOPES", &c.OIDCScopes)
	str("CALDAV_OIDC_ADMIN_EMAILS", &c.OIDCAdminEmails)
	str("CALDAV_SMTP_HOST", &c.SMTPHost)
	str("CALDAV_SMTP_USER", &c.SMTPUser)
	str("CALDAV_SMTP_PASS", &c.SMTPPass)
	str("CALDAV_SMTP_TLS", &c.SMTPTLS)
	str("CALDAV_IMIP_FROM_OVERRIDE", &c.IMIPFromOverride)
	str("CALDAV_IMIP_REPLY_ADDRESS", &c.IMIPReplyAddress)
	str("CALDAV_IMAP_HOST", &c.IMAPHost)
	str("CALDAV_IMAP_USER", &c.IMAPUser)
	str("CALDAV_IMAP_PASS", &c.IMAPPass)
	str("CALDAV_IMAP_TLS", &c.IMAPTLS)
	str("CALDAV_IMAP_FOLDER", &c.IMAPFolder)
	str("CALDAV_IMAP_PROCESSED_FOLDER", &c.IMAPProcessedDir)

	for _, fn := range []func() error{
		func() error { return boolean("CALDAV_TRUST_PROXY_HEADERS", &c.TrustProxyHeaders) },
		func() error { return boolean("CALDAV_AUTO_MIGRATE", &c.AutoMigrate) },
		func() error { return boolean("CALDAV_SCHEDULING_ENABLED", &c.SchedulingEnabled) },
		func() error { return integer("CALDAV_SMTP_PORT", &c.SMTPPort) },
		func() error { return integer("CALDAV_IMAP_PORT", &c.IMAPPort) },
		func() error { return dur("CALDAV_ICS_DEFAULT_POLL_INTERVAL", &c.ICSDefaultPollInterval) },
		func() error { return dur("CALDAV_IMAP_POLL_INTERVAL", &c.IMAPPollInterval) },
	} {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) applyFlags(flags map[string]string) error {
	if flags == nil {
		return nil
	}
	if v := flags["listen-addr"]; v != "" {
		c.ListenAddr = v
	}
	if v := flags["external-url"]; v != "" {
		c.ExternalURL = v
	}
	if v := flags["db-path"]; v != "" {
		c.DBPath = v
	}
	if v := flags["log-level"]; v != "" {
		c.LogLevel = v
	}
	return nil
}

// Validate checks that required settings are present and internally
// consistent. It returns the first problem found.
func (c *Config) Validate() error {
	var missing []string
	if c.ExternalURL == "" {
		missing = append(missing, "CALDAV_EXTERNAL_URL")
	}
	if c.OIDCIssuer == "" {
		missing = append(missing, "CALDAV_OIDC_ISSUER")
	}
	if c.OIDCClientID == "" {
		missing = append(missing, "CALDAV_OIDC_CLIENT_ID")
	}
	if c.OIDCClientSecret == "" {
		missing = append(missing, "CALDAV_OIDC_CLIENT_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	if c.SMTPHost != "" && c.IMIPReplyAddress == "" {
		return fmt.Errorf("CALDAV_IMIP_REPLY_ADDRESS is required when CALDAV_SMTP_HOST is set")
	}
	switch c.SMTPTLS {
	case "starttls", "implicit", "none":
	default:
		return fmt.Errorf("CALDAV_SMTP_TLS must be one of starttls, implicit, none")
	}
	switch c.IMAPTLS {
	case "starttls", "implicit", "none":
	default:
		return fmt.Errorf("CALDAV_IMAP_TLS must be one of starttls, implicit, none")
	}
	return nil
}

// AdminEmailSet returns the configured admin emails, lower-cased, as a set.
func (c *Config) AdminEmailSet() map[string]struct{} {
	set := map[string]struct{}{}
	for _, e := range strings.Split(c.OIDCAdminEmails, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			set[e] = struct{}{}
		}
	}
	return set
}
