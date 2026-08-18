package mail

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset" // register non-UTF-8 charsets
	"github.com/emersion/go-message/mail"
)

// maxNestingDepth bounds recursion into message/rfc822 attachments so a
// crafted eml-in-eml chain cannot blow the stack ("eml bomb").
const maxNestingDepth = 3

// Attachment describes one attachment without its content. SHA256 is
// computed on the fly while the message is parsed, so read_message can
// report it before anything is saved. Attachments found inside nested
// message/rfc822 attachments are flattened into the same index space,
// with Via naming the .eml they came from.
type Attachment struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size_bytes"`
	SHA256      string `json:"sha256,omitempty"`
	Via         string `json:"via,omitempty"`
}

// Parsed is the decoded view of a message.
type Parsed struct {
	Subject     string
	From        string
	To          string
	Date        time.Time
	TextBody    string // text/plain if present, otherwise text/html converted to text
	Attachments []Attachment
}

// IsNestedMessage reports whether the attachment is an embedded email.
func IsNestedMessage(contentType, filename string) bool {
	switch contentType {
	case "message/rfc822", "message/global":
		return true
	}
	return strings.HasSuffix(strings.ToLower(filename), ".eml")
}

// part is one collected attachment with its content.
type part struct {
	meta Attachment
	data []byte
}

// collectParts walks the message and returns all attachments depth-first,
// recursing into nested message/rfc822 attachments. The traversal order is
// deterministic and defines the attachment index space used by all tools.
func collectParts(raw []byte, via string, depth int) ([]part, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}
	var out []part
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break // tolerate malformed sub-parts: return what we have
		}
		h, ok := p.Header.(*mail.AttachmentHeader)
		if !ok {
			continue
		}
		name, _ := h.Filename()
		ctype, _, _ := h.ContentType()
		data, err := io.ReadAll(p.Body)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out = append(out, part{
			meta: Attachment{
				Filename:    name,
				ContentType: ctype,
				Size:        int64(len(data)),
				SHA256:      hex.EncodeToString(sum[:]),
				Via:         via,
			},
			data: data,
		})
		if IsNestedMessage(ctype, name) && depth < maxNestingDepth {
			nestedVia := name
			if nestedVia == "" {
				nestedVia = "nested message"
			}
			if via != "" {
				nestedVia = via + " > " + nestedVia
			}
			nested, err := collectParts(data, nestedVia, depth+1)
			if err == nil {
				out = append(out, nested...)
			}
		}
	}
	return out, nil
}

// Parse decodes an RFC 5322 message: headers, best-effort text body and the
// flattened attachment list (metadata only, nested .eml included).
func Parse(raw []byte) (*Parsed, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}

	p := &Parsed{}
	p.Subject, _ = mr.Header.Subject()
	p.Date, _ = mr.Header.Date()
	if addrs, err := mr.Header.AddressList("From"); err == nil {
		p.From = formatAddrs(addrs)
	}
	if addrs, err := mr.Header.AddressList("To"); err == nil {
		p.To = formatAddrs(addrs)
	}

	var htmlBody string
	for {
		pt, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if h, ok := pt.Header.(*mail.InlineHeader); ok {
			ctype, _, _ := h.ContentType()
			switch {
			case ctype == "text/plain" && p.TextBody == "":
				b, _ := io.ReadAll(pt.Body)
				p.TextBody = string(b)
			case ctype == "text/html" && htmlBody == "":
				b, _ := io.ReadAll(pt.Body)
				htmlBody = string(b)
			}
		}
	}
	if p.TextBody == "" && htmlBody != "" {
		p.TextBody = htmlToText(htmlBody)
	}

	parts, err := collectParts(raw, "", 0)
	if err != nil {
		return nil, err
	}
	p.Attachments = make([]Attachment, len(parts))
	for i, pt := range parts {
		pt.meta.Index = i
		p.Attachments[i] = pt.meta
	}
	return p, nil
}

// ExtractAttachment returns the content of the attachment with the given
// index in the flattened index space reported by Parse.
func ExtractAttachment(raw []byte, index int) (Attachment, []byte, error) {
	parts, err := collectParts(raw, "", 0)
	if err != nil {
		return Attachment{}, nil, err
	}
	if index < 0 || index >= len(parts) {
		return Attachment{}, nil, fmt.Errorf("attachment with index %d not found (message has %d)", index, len(parts))
	}
	meta := parts[index].meta
	meta.Index = index
	return meta, parts[index].data, nil
}

func formatAddrs(addrs []*mail.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Name != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", a.Name, a.Address))
		} else {
			parts = append(parts, a.Address)
		}
	}
	return strings.Join(parts, ", ")
}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style|head)\b.*?</(script|style|head)>`)
	reBlockTags   = regexp.MustCompile(`(?i)<(/?(p|div|tr|li|h[1-6]|blockquote|table)|br\s*/?)>`)
	reTags        = regexp.MustCompile(`(?s)<[^>]*>`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
)

// htmlToText is a crude HTML→text conversion, good enough for the PoC:
// the goal is to give the model readable body text, not perfect rendering.
func htmlToText(s string) string {
	s = reScriptStyle.ReplaceAllString(s, "")
	s = reBlockTags.ReplaceAllString(s, "\n")
	s = reTags.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
