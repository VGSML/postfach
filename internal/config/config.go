// Package config loads server configuration from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds everything the MCP server needs to run.
type Config struct {
	// IMAP connection, parsed from POSTFACH_IMAP_URL.
	Host     string
	Port     string
	UseTLS   bool // true for imaps://, false for imap:// (STARTTLS)
	Insecure bool // imap+insecure://: plaintext, no STARTTLS — tests/local only
	Username string
	Password string

	// Directory where save_attachment writes files.
	AttachmentsDir string

	// Maximum attachment size read_attachment returns inline, in bytes.
	MaxInlineAttachment int64

	// URL template for opening a saved document from the registry
	// spreadsheet on another machine; {filename} is replaced with the
	// URL-escaped file name. Default: Google Drive exact-name search,
	// which works in the web UI once the attachments folder is synced.
	DocLinkTemplate string
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
	case "imap+insecure": // plaintext without STARTTLS — tests/local only
		cfg.Insecure = true
		cfg.Port = "143"
	default:
		return nil, fmt.Errorf("POSTFACH_IMAP_URL: unsupported scheme %q (want imaps://, imap:// or imap+insecure://)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("POSTFACH_IMAP_URL: missing host")
	}
	cfg.Host = u.Hostname()
	if cfg.Insecure {
		switch cfg.Host {
		case "localhost", "127.0.0.1", "::1":
		default:
			return nil, fmt.Errorf("imap+insecure:// is restricted to loopback hosts (tests/local dev); use imaps:// for %q", cfg.Host)
		}
	}
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

	cfg.DocLinkTemplate = os.Getenv("POSTFACH_DOC_LINK_TEMPLATE")
	if cfg.DocLinkTemplate == "" {
		cfg.DocLinkTemplate = `https://drive.google.com/drive/search?q=%22{filename}%22`
	}
	return cfg, nil
}

// DocLink renders the document link for a saved file name.
func (c *Config) DocLink(filename string) string {
	if filename == "" || c.DocLinkTemplate == "" {
		return ""
	}
	return strings.ReplaceAll(c.DocLinkTemplate, "{filename}", url.QueryEscape(filename))
}

// Addr returns the host:port dial address.
func (c *Config) Addr() string {
	return c.Host + ":" + c.Port
}
