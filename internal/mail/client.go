// Package mail wraps IMAP access and MIME parsing for the MCP tools.
package mail

import (
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/hugr-lab/postfach/internal/config"
)

// Summary is a lightweight view of one message for list results.
type Summary struct {
	UID          uint32          `json:"uid"`
	Date         string          `json:"date,omitempty"`
	InternalDate string          `json:"internal_date,omitempty"`
	From         string          `json:"from,omitempty"`
	Subject      string          `json:"subject"`
	Flags        []string        `json:"flags,omitempty"`
	Size         int64           `json:"size_bytes,omitempty"`
	Attachments  []AttachmentRef `json:"attachments,omitempty"`
}

// AttachmentRef is attachment metadata taken from BODYSTRUCTURE — no
// content is downloaded for a listing. Size is the encoded transfer size
// (base64 parts are ~33% larger than the decoded file). Indices for
// read_attachment/save_attachment come from read_message, not from here.
type AttachmentRef struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Size        uint32 `json:"size_bytes_encoded,omitempty"`
}

// ListOptions filters a mailbox listing. All reads are non-destructive:
// the mailbox is opened read-only (EXAMINE), so \Seen is never set.
type ListOptions struct {
	Limit      int
	UnseenOnly bool
	// SinceUID keeps only messages with UID > SinceUID. Together with the
	// mailbox UIDVALIDITY this is the incremental-sync cursor (UIDs are
	// ascending within one UIDVALIDITY generation).
	SinceUID uint32
	// Since keeps only messages received after this time. IMAP SINCE is
	// day-granular; the exact cut-off is applied client-side on the
	// server's INTERNALDATE.
	Since time.Time
}

// ListResult is a mailbox listing plus the UIDVALIDITY needed to know when
// a SinceUID cursor must be reset. TotalMessages is the mailbox size;
// Matched counts messages satisfying the filters BEFORE the limit was
// applied, so callers can tell "hit the limit" from "that was all".
type ListResult struct {
	UIDValidity   uint32
	TotalMessages uint32
	Matched       int
	Messages      []Summary
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
	switch {
	case cfg.Insecure:
		c, err = imapclient.DialInsecure(cfg.Addr(), nil)
	case cfg.UseTLS:
		c, err = imapclient.DialTLS(cfg.Addr(), nil)
	default:
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

// List returns summaries of the newest matching messages, newest first.
func (cl *Client) List(mailbox string, o ListOptions) (*ListResult, error) {
	sel, err := cl.c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", mailbox, err)
	}
	res := &ListResult{UIDValidity: sel.UIDValidity, TotalMessages: sel.NumMessages, Messages: []Summary{}}
	if sel.NumMessages == 0 {
		return res, nil
	}

	var numSet imap.NumSet
	if o.UnseenOnly || o.SinceUID > 0 || !o.Since.IsZero() {
		crit := &imap.SearchCriteria{}
		if o.UnseenOnly {
			crit.NotFlag = []imap.Flag{imap.FlagSeen}
		}
		if o.SinceUID > 0 {
			crit.UID = []imap.UIDSet{{imap.UIDRange{Start: imap.UID(o.SinceUID + 1), Stop: 0}}}
		}
		if !o.Since.IsZero() {
			crit.Since = o.Since
		}
		data, err := cl.c.UIDSearch(crit, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("uid search: %w", err)
		}
		uids := data.AllUIDs()
		res.Matched = len(uids)
		if len(uids) == 0 {
			return res, nil
		}
		if len(uids) > o.Limit {
			uids = uids[len(uids)-o.Limit:] // keep the newest
		}
		var us imap.UIDSet
		us.AddNum(uids...)
		numSet = us
	} else {
		res.Matched = int(sel.NumMessages)
		from := int64(1)
		if n := int64(sel.NumMessages) - int64(o.Limit) + 1; n > 1 {
			from = n
		}
		var ss imap.SeqSet
		ss.AddRange(uint32(from), sel.NumMessages)
		numSet = ss
	}

	msgs, err := cl.c.Fetch(numSet, &imap.FetchOptions{
		UID:           true,
		Envelope:      true,
		Flags:         true,
		RFC822Size:    true,
		InternalDate:  true,
		BodyStructure: &imap.FetchItemBodyStructure{},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch summaries: %w", err)
	}

	for i := len(msgs) - 1; i >= 0; i-- { // newest first
		m := msgs[i]
		// IMAP SINCE is day-granular; enforce the exact cut-off here.
		if !o.Since.IsZero() && !m.InternalDate.IsZero() && !m.InternalDate.After(o.Since) {
			continue
		}
		s := Summary{
			UID:         uint32(m.UID),
			Size:        m.RFC822Size,
			Attachments: attachmentRefs(m.BodyStructure),
		}
		if !m.InternalDate.IsZero() {
			s.InternalDate = m.InternalDate.Format(time.RFC3339)
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
		res.Messages = append(res.Messages, s)
	}
	return res, nil
}

// attachmentRefs walks a BODYSTRUCTURE tree and collects attachment
// metadata (named parts or parts with an attachment disposition).
func attachmentRefs(bs imap.BodyStructure) []AttachmentRef {
	if bs == nil {
		return nil
	}
	var refs []AttachmentRef
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		sp, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		name := sp.Filename()
		disp := sp.Disposition()
		if name == "" && (disp == nil || !strings.EqualFold(disp.Value, "attachment")) {
			return true
		}
		refs = append(refs, AttachmentRef{
			Filename:    name,
			ContentType: strings.ToLower(sp.MediaType()),
			Size:        sp.Size,
		})
		return true
	})
	return refs
}

// FetchRaw downloads the full RFC 5322 source of one message by UID.
func (cl *Client) FetchRaw(mailbox string, uid uint32) ([]byte, error) {
	if _, err := cl.c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("select %q: %w", mailbox, err)
	}
	var us imap.UIDSet
	us.AddNum(imap.UID(uid))
	section := &imap.FetchItemBodySection{Peek: true}
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
