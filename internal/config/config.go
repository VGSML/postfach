// Package config loads server configuration from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

// Config holds everything the MCP server needs to run.
type Config struct {
	// IMAP connection, parsed from POSTFACH_IMAP_URL.
	Host     string
	Port     string
	UseTLS   bool // true for imaps://, false for imap:// (STARTTLS)
	Username string
	Password string

	// Directory where save_attachment writes files.
	AttachmentsDir string

	// Maximum attachment size read_attachment returns inline, in bytes.
	MaxInlineAttachment int64
}

// Load reads configuration from the environment.
//
//	POSTFACH_IMAP_URL        required, e.g. imaps://user:pass@imap.example.com:993
//	                         (imap:// connects to port 143 and upgrades via STARTTLS;
//	                         URL-escape special characters in the password)
//	POSTFACH_ATTACHMENTS_DIR optional, default ./attachments
//	POSTFACH_MAX_INLINE_MB   optional, default 5: size cap for read_attachment
func Load() (*Config, error) {
	raw := os.Getenv("POSTFACH_IMAP_URL")
	if raw == "" {
		return nil, fmt.Errorf("POSTFACH_IMAP_URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse POSTFACH_IMAP_URL: %w", err)
	}

	cfg := &Config{}
	switch u.Scheme {
	case "imaps":
		cfg.UseTLS = true
		cfg.Port = "993"
	case "imap":
		cfg.UseTLS = false
		cfg.Port = "143"
	default:
		return nil, fmt.Errorf("POSTFACH_IMAP_URL: unsupported scheme %q (want imap:// or imaps://)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("POSTFACH_IMAP_URL: missing host")
	}
	cfg.Host = u.Hostname()
	if p := u.Port(); p != "" {
		cfg.Port = p
	}
	if u.User == nil {
		return nil, fmt.Errorf("POSTFACH_IMAP_URL: missing user info (user:password@)")
	}
	cfg.Username = u.User.Username()
	pass, ok := u.User.Password()
	if !ok || pass == "" {
		return nil, fmt.Errorf("POSTFACH_IMAP_URL: missing password")
	}
	cfg.Password = pass

	dir := os.Getenv("POSTFACH_ATTACHMENTS_DIR")
	if dir == "" {
		dir = "attachments"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve attachments dir: %w", err)
	}
	cfg.AttachmentsDir = abs

	cfg.MaxInlineAttachment = 5 << 20
	if s := os.Getenv("POSTFACH_MAX_INLINE_MB"); s != "" {
		mb, err := strconv.ParseInt(s, 10, 64)
		if err != nil || mb <= 0 {
			return nil, fmt.Errorf("POSTFACH_MAX_INLINE_MB: invalid value %q", s)
		}
		cfg.MaxInlineAttachment = mb << 20
	}
	return cfg, nil
}

// Addr returns the host:port dial address.
func (c *Config) Addr() string {
	return c.Host + ":" + c.Port
}
