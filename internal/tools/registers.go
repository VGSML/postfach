package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/hugr-lab/postfach/internal/ledger"
	"github.com/hugr-lab/postfach/internal/screen"
)

// Registry tools: the client model owns the whole lifecycle. It decides
// which registries exist (invoices, Mahnungen, requests, ...), documents
// their fields, records entries while reading mail, and can remove or
// drop. The server only stores and renders Register.xlsx (one sheet per
// registry) for viewing in Google Sheets via Drive folder sync.
func (t *Tools) registerRegistryTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("add_registry",
		mcp.WithDescription("Create a named registry (or update its documentation): a description of what it "+
			"tracks and the accepted fields with their meanings. The declared field order defines the spreadsheet "+
			"columns. Idempotent: calling again merges description and appends new fields."),
		mcp.WithString("registry", mcp.Required(), mcp.Description("Registry slug: lowercase letters/digits/-/_, e.g. rechnungen, mahnungen, anfragen")),
		mcp.WithString("description", mcp.Required(), mcp.Description("What this registry tracks and when to add entries")),
		mcp.WithArray("fields", mcp.Description("Accepted fields in column order: [{name, description}, ...]")),
	), t.handleAddRegistry)

	s.AddTool(mcp.NewTool("registries",
		mcp.WithDescription("List all registries with their descriptions, documented fields and entry counts. "+
			"Call this first to learn which registries this mailbox maintains."),
	), t.handleRegistries)

	s.AddTool(mcp.NewTool("record_entry",
		mcp.WithDescription("Insert or update one record in an existing registry (create registries with "+
			"add_registry). Upsert by key: reuse the same key to update; empty field values never erase "+
			"previously recorded ones. Fields not declared for the registry are stored anyway and reported back "+
			"as undeclared_fields — prefer declared names."),
		mcp.WithString("registry", mcp.Required(), mcp.Description("Registry slug")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Stable unique key of the record within the registry, e.g. 'RE-2026-4711|Berlin Recycling'")),
		mcp.WithObject("fields", mcp.Required(), mcp.Description("Record fields as a flat JSON object using the registry's declared field names")),
		mcp.WithNumber("uid", mcp.Description("Source message UID")),
		mcp.WithNumber("attachment_index", mcp.Description("Source attachment index")),
		mcp.WithString("sha256", mcp.Description("sha256 of the source file")),
		mcp.WithString("saved_path", mcp.Description("Path returned by save_attachment")),
		mcp.WithString("mailbox", mcp.Description("Source mailbox (default INBOX)")),
	), t.handleRecordEntry)

	s.AddTool(mcp.NewTool("list_entries",
		mcp.WithDescription("List records of one registry, newest first."),
		mcp.WithString("registry", mcp.Required(), mcp.Description("Registry slug")),
		mcp.WithNumber("limit", mcp.Description("Maximum entries to return (default 50)")),
	), t.handleListEntries)

	s.AddTool(mcp.NewTool("remove_entry",
		mcp.WithDescription("Remove one record from a registry by key (tombstoned in the journal, gone from the spreadsheet)."),
		mcp.WithString("registry", mcp.Required(), mcp.Description("Registry slug")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Key of the record to remove")),
	), t.handleRemoveEntry)

	s.AddTool(mcp.NewTool("drop_registry",
		mcp.WithDescription("Permanently delete a whole registry: its journal, documentation and spreadsheet "+
			"sheet. Irreversible — confirm with the user before dropping a registry that has entries."),
		mcp.WithString("registry", mcp.Required(), mcp.Description("Registry slug")),
	), t.handleDropRegistry)
}

func (t *Tools) handleAddRegistry(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	registry := argString(args, "registry", "")
	description := screen.StripInvisible(argString(args, "description", ""))

	var fields []ledger.FieldDef
	if raw, ok := args["fields"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			desc, _ := m["description"].(string)
			fields = append(fields, ledger.FieldDef{
				Name:        screen.StripInvisible(name),
				Description: screen.StripInvisible(desc),
			})
		}
	}

	created, err := t.ledger.Create(registry, description, fields)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{
		"registry":    registry,
		"created":     created,
		"updated":     !created,
		"spreadsheet": t.ledger.XLSXPath(),
	})
}

func (t *Tools) handleRegistries(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	infos, err := t.ledger.Registries()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{
		"registries":  infos,
		"spreadsheet": t.ledger.XLSXPath(),
	})
}

func (t *Tools) handleRecordEntry(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	registry := argString(args, "registry", "")
	fields, _ := args["fields"].(map[string]any)

	// Field values are model-produced but ultimately email-derived: strip
	// invisible characters from every string.
	cleanFields := make(map[string]any, len(fields))
	for k, v := range fields {
		if s, ok := v.(string); ok {
			cleanFields[screen.StripInvisible(k)] = screen.StripInvisible(s)
		} else {
			cleanFields[screen.StripInvisible(k)] = v
		}
	}

	e := ledger.Entry{
		Key:    screen.StripInvisible(argString(args, "key", "")),
		Fields: cleanFields,
		Source: ledger.Source{
			Mailbox:   argString(args, "mailbox", ""),
			SHA256:    argString(args, "sha256", ""),
			SavedPath: argString(args, "saved_path", ""),
		},
	}
	if uid := argInt(args, "uid", 0); uid > 0 {
		e.Source.UID = uint32(uid)
	}
	if idx := argInt(args, "attachment_index", -1); idx >= 0 {
		e.Source.AttachmentIndex = &idx
	}

	stored, updated, undeclared, total, err := t.ledger.Upsert(registry, e)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result := map[string]any{
		"recorded":      true,
		"updated":       updated,
		"registry":      registry,
		"entry":         stored,
		"spreadsheet":   t.ledger.XLSXPath(),
		"total_entries": total,
	}
	if len(undeclared) > 0 {
		result["undeclared_fields"] = undeclared
		result["note"] = "these fields were not declared via add_registry; they are stored and added as new columns"
	}
	return jsonResult(result)
}

func (t *Tools) handleListEntries(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	registry := argString(args, "registry", "")
	limit := min(max(argInt(args, "limit", 50), 1), 500)

	if !t.ledger.Exists(registry) {
		return mcp.NewToolResultError("registry does not exist; see registries for the list"), nil
	}
	entries, err := t.ledger.Load(registry)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	ledger.SortNewestFirst(entries)
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return jsonResult(map[string]any{
		"registry":    registry,
		"count":       len(entries),
		"entries":     entries,
		"spreadsheet": t.ledger.XLSXPath(),
	})
}

func (t *Tools) handleRemoveEntry(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	registry := argString(args, "registry", "")
	key := argString(args, "key", "")
	if key == "" {
		return mcp.NewToolResultError("key is required"), nil
	}
	removed, err := t.ledger.Remove(registry, key)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{
		"registry": registry,
		"key":      key,
		"removed":  removed,
	})
}

func (t *Tools) handleDropRegistry(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	registry := argString(args, "registry", "")
	if err := t.ledger.Drop(registry); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{
		"registry": registry,
		"dropped":  true,
	})
}
