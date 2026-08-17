// Package mail wraps IMAP access and MIME parsing for the MCP tools.
package mail

import (
	"fmt"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/hugr-lab/postfach/internal/config"
)

// Summary is a lightweight view of one message for list results.
type Summary struct {
	UID     uint32   `json:"uid"`
	Date    string   `json:"date,omitempty"`
	From    string   `json:"from,omitempty"`
	Subject string   `json:"subject"`
	Flags   []string `json:"flags,omitempty"`
	Size    int64    `json:"size_bytes,omitempty"`
}

// Client is a thin wrapper over an authenticated IMAP connection.
// PoC scope: one connection per tool call, no pooling.
type Client struct {
	c *imapclient.Client
}

// Dial connects and authenticates using the configured credentials.
func Dial(cfg *config.Config) (*Client, error) {
	var (
		c   *imapclient.Client
		err error
	)
	if cfg.UseTLS {
		c, err = imapclient.DialTLS(cfg.Addr(), nil)
	} else {
		c, err = imapclient.DialStartTLS(cfg.Addr(), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", cfg.Addr(), err)
	}
	if err := c.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return &Client{c: c}, nil
}

func (cl *Client) Close() {
	if cl.c != nil {
		_ = cl.c.Logout().Wait()
		_ = cl.c.Close()
	}
}

// List returns summaries of the newest messages in mailbox, newest first.
func (cl *Client) List(mailbox string, limit int, unseenOnly bool) ([]Summary, error) {
	sel, err := cl.c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", mailbox, err)
	}
	if sel.NumMessages == 0 {
		return []Summary{}, nil
	}

	var numSet imap.NumSet
	if unseenOnly {
		data, err := cl.c.UIDSearch(&imap.SearchCriteria{
			NotFlag: []imap.Flag{imap.FlagSeen},
		}, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("search unseen: %w", err)
		}
		uids := data.AllUIDs()
		if len(uids) == 0 {
			return []Summary{}, nil
		}
		if len(uids) > limit {
			uids = uids[len(uids)-limit:] // keep the newest
		}
		var us imap.UIDSet
		us.AddNum(uids...)
		numSet = us
	} else {
		from := int64(1)
		if n := int64(sel.NumMessages) - int64(limit) + 1; n > 1 {
			from = n
		}
		var ss imap.SeqSet
		ss.AddRange(uint32(from), sel.NumMessages)
		numSet = ss
	}

	msgs, err := cl.c.Fetch(numSet, &imap.FetchOptions{
		UID:        true,
		Envelope:   true,
		Flags:      true,
		RFC822Size: true,
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch summaries: %w", err)
	}

	out := make([]Summary, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- { // newest first
		m := msgs[i]
		s := Summary{
			UID:  uint32(m.UID),
			Size: m.RFC822Size,
		}
		for _, f := range m.Flags {
			s.Flags = append(s.Flags, string(f))
		}
		if env := m.Envelope; env != nil {
			s.Subject = env.Subject
			if !env.Date.IsZero() {
				s.Date = env.Date.Format(time.RFC3339)
			}
			if len(env.From) > 0 {
				a := env.From[0]
				if a.Name != "" {
					s.From = fmt.Sprintf("%s <%s>", a.Name, a.Addr())
				} else {
					s.From = a.Addr()
				}
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// FetchRaw downloads the full RFC 5322 source of one message by UID.
func (cl *Client) FetchRaw(mailbox string, uid uint32) ([]byte, error) {
	if _, err := cl.c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("select %q: %w", mailbox, err)
	}
	var us imap.UIDSet
	us.AddNum(imap.UID(uid))
	section := &imap.FetchItemBodySection{}
	msgs, err := cl.c.Fetch(us, &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{section},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch uid %d: %w", uid, err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("message with uid %d not found in %q", uid, mailbox)
	}
	body := msgs[0].FindBodySection(section)
	if body == nil {
		return nil, fmt.Errorf("server returned no body for uid %d", uid)
	}
	return body, nil
}
