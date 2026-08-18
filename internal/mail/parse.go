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

// Attachment describes one attachment without its content. SHA256 is
// computed on the fly while the message is parsed, so read_message can
// report it before anything is saved.
type Attachment struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size_bytes"`
	SHA256      string `json:"sha256,omitempty"`
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

// Parse decodes an RFC 5322 message: headers, best-effort text body and the
// attachment list (metadata only).
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
	attIdx := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Tolerate malformed sub-parts: return what we decoded so far.
			break
		}
		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			ctype, _, _ := h.ContentType()
			switch {
			case ctype == "text/plain" && p.TextBody == "":
				b, _ := io.ReadAll(part.Body)
				p.TextBody = string(b)
			case ctype == "text/html" && htmlBody == "":
				b, _ := io.ReadAll(part.Body)
				htmlBody = string(b)
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			ctype, _, _ := h.ContentType()
			hasher := sha256.New()
			size, _ := io.Copy(hasher, part.Body)
			p.Attachments = append(p.Attachments, Attachment{
				Index:       attIdx,
				Filename:    name,
				ContentType: ctype,
				Size:        size,
				SHA256:      hex.EncodeToString(hasher.Sum(nil)),
			})
			attIdx++
		}
	}
	if p.TextBody == "" && htmlBody != "" {
		p.TextBody = htmlToText(htmlBody)
	}
	return p, nil
}

// ExtractAttachment re-parses raw and returns the content of the attachment
// with the given index (as reported by Parse).
func ExtractAttachment(raw []byte, index int) (Attachment, []byte, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("parse message: %w", err)
	}
	attIdx := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		h, ok := part.Header.(*mail.AttachmentHeader)
		if !ok {
			continue
		}
		if attIdx != index {
			attIdx++
			continue
		}
		name, _ := h.Filename()
		ctype, _, _ := h.ContentType()
		data, err := io.ReadAll(part.Body)
		if err != nil {
			return Attachment{}, nil, fmt.Errorf("read attachment %d: %w", index, err)
		}
		sum := sha256.Sum256(data)
		return Attachment{
			Index:       index,
			Filename:    name,
			ContentType: ctype,
			Size:        int64(len(data)),
			SHA256:      hex.EncodeToString(sum[:]),
		}, data, nil
	}
	return Attachment{}, nil, fmt.Errorf("attachment with index %d not found", index)
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
