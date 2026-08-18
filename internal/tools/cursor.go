package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Incremental-sync cursor, persisted per mailbox in the attachments
// directory. The MODEL commits the cursor explicitly after it finished
// processing a batch — the server never advances it on its own, so a run
// that dies halfway loses nothing.
const cursorsFile = "cursors.json"

type cursor struct {
	UIDValidity uint32 `json:"uid_validity"`
	LastUID     uint32 `json:"last_uid"`
	UpdatedAt   string `json:"updated_at"`
}

func (t *Tools) cursorsPath() string {
	return filepath.Join(t.cfg.AttachmentsDir, cursorsFile)
}

func (t *Tools) loadCursors() (map[string]cursor, error) {
	data, err := os.ReadFile(t.cursorsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]cursor{}, nil
		}
		return nil, err
	}
	out := map[string]cursor{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", cursorsFile, err)
	}
	return out, nil
}

func (t *Tools) registerCursorTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("get_cursor",
		mcp.WithDescription("Read the persisted incremental-sync cursor for a mailbox: the last processed UID and "+
			"the uid_validity it belongs to. Start a processing run with this, list messages with "+
			"since_uid=last_uid, and compare uid_validity with the one list_messages returns — if it changed, the "+
			"UID space was reset and a full rescan is needed."),
		mcp.WithString("mailbox", mcp.Description("IMAP mailbox name (default INBOX)")),
	), t.handleGetCursor)

	s.AddTool(mcp.NewTool("set_cursor",
		mcp.WithDescription("Persist the incremental-sync cursor for a mailbox. Call this AFTER a batch of "+
			"messages has been fully processed (registries updated, attachments saved) with the highest processed "+
			"UID and the uid_validity from list_messages. Never advance the cursor past messages you have not "+
			"handled."),
		mcp.WithString("mailbox", mcp.Description("IMAP mailbox name (default INBOX)")),
		mcp.WithNumber("last_uid", mcp.Required(), mcp.Description("Highest fully processed UID")),
		mcp.WithNumber("uid_validity", mcp.Required(), mcp.Description("uid_validity reported by list_messages for this mailbox")),
	), t.handleSetCursor)
}

func (t *Tools) handleGetCursor(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mailbox := argString(req.GetArguments(), "mailbox", "INBOX")
	cursors, err := t.loadCursors()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	c, ok := cursors[mailbox]
	if !ok {
		return jsonResult(map[string]any{
			"mailbox": mailbox,
			"exists":  false,
			"note":    "no cursor yet: process the mailbox from scratch, then set_cursor",
		})
	}
	return jsonResult(map[string]any{
		"mailbox":      mailbox,
		"exists":       true,
		"last_uid":     c.LastUID,
		"uid_validity": c.UIDValidity,
		"updated_at":   c.UpdatedAt,
	})
}

func (t *Tools) handleSetCursor(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	mailbox := argString(args, "mailbox", "INBOX")
	lastUID := argInt(args, "last_uid", 0)
	uidValidity := argInt(args, "uid_validity", 0)
	if lastUID <= 0 || uidValidity <= 0 {
		return mcp.NewToolResultError("last_uid and uid_validity are required and must be positive"), nil
	}

	cursors, err := t.loadCursors()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	prev, existed := cursors[mailbox]
	if existed && prev.UIDValidity == uint32(uidValidity) && uint32(lastUID) < prev.LastUID {
		return mcp.NewToolResultError(fmt.Sprintf(
			"refusing to move the cursor backwards (stored last_uid %d > %d); pass the higher value or a new uid_validity",
			prev.LastUID, lastUID)), nil
	}
	cursors[mailbox] = cursor{
		UIDValidity: uint32(uidValidity),
		LastUID:     uint32(lastUID),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := os.MkdirAll(t.cfg.AttachmentsDir, 0o755); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := json.MarshalIndent(cursors, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := os.WriteFile(t.cursorsPath(), data, 0o600); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{
		"mailbox":      mailbox,
		"last_uid":     lastUID,
		"uid_validity": uidValidity,
		"saved":        true,
	})
}
