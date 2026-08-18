// Package ledger maintains named registries of arbitrary email-derived
// records. The client model owns the lifecycle: it creates registries
// (with a description and documented fields), records/updates entries,
// removes them, drops whole registries. The server only stores.
//
// Per registry: registry-<name>.jsonl is the append-only journal (source
// of truth; last write per key wins, deletions are tombstone lines) and
// registry-<name>.meta.json documents the registry (description, field
// docs) and pins the column order. One workbook Register.xlsx with a
// sheet per registry is regenerated on every change, so the registries
// are readable in Google Sheets once the folder is synced to Drive.
package ledger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"
)

const XLSXName = "Register.xlsx"

// maxEntryLine caps one journal line: Load's scanner buffer must always be
// able to read back what Upsert wrote, or one oversized entry would brick
// the registry forever.
const (
	maxEntryLine  = 1 << 20
	scanBufferCap = 16 << 20
)

var registryNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// Entry is one record in a registry. Fields is model-defined; Source ties
// the record back to the mailbox. Deleted marks a tombstone line.
type Entry struct {
	Key        string         `json:"key"`
	RecordedAt string         `json:"recorded_at,omitempty"`
	UpdatedAt  string         `json:"updated_at,omitempty"`
	Fields     map[string]any `json:"fields,omitempty"`
	Source     Source         `json:"source,omitempty"`
	Deleted    bool           `json:"deleted,omitempty"`
}

type Source struct {
	Mailbox         string `json:"mailbox,omitempty"`
	UID             uint32 `json:"uid,omitempty"`
	AttachmentIndex *int   `json:"attachment_index,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	SavedPath       string `json:"saved_path,omitempty"`
}

// FieldDef documents one accepted field of a registry.
type FieldDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Info describes a registry.
type Info struct {
	Registry    string     `json:"registry"`
	Description string     `json:"description,omitempty"`
	Fields      []FieldDef `json:"fields,omitempty"`
	Entries     int        `json:"entries"`
}

// registryMeta is the persisted registry documentation.
type registryMeta struct {
	Description string            `json:"description,omitempty"`
	Columns     []string          `json:"columns"`
	FieldDocs   map[string]string `json:"field_docs,omitempty"`
}

// Ledger is bound to one directory. docLink (optional) renders a web URL
// for a saved document's file name; it becomes a hyperlink in the
// workbook so registry rows can open their document from any machine.
// The mutex serializes all registry operations: MCP servers dispatch tool
// calls on a worker pool, so concurrent record_entry calls are normal.
type Ledger struct {
	dir     string
	docLink func(filename string) string
	mu      sync.Mutex
}

func New(dir string, docLink func(string) string) *Ledger {
	return &Ledger{dir: dir, docLink: docLink}
}

// XLSXPath is where the workbook is written.
func (l *Ledger) XLSXPath() string { return filepath.Join(l.dir, XLSXName) }

func (l *Ledger) jsonlPath(registry string) string {
	return filepath.Join(l.dir, "registry-"+registry+".jsonl")
}

func (l *Ledger) metaPath(registry string) string {
	return filepath.Join(l.dir, "registry-"+registry+".meta.json")
}

// ValidateName checks a registry name (lowercase slug).
func ValidateName(registry string) error {
	if !registryNameRe.MatchString(registry) {
		return fmt.Errorf("invalid registry name %q: use a lowercase slug like rechnungen, mahnungen, anfragen", registry)
	}
	return nil
}

// Exists reports whether the registry has been created.
func (l *Ledger) Exists(registry string) bool {
	_, err := os.Stat(l.metaPath(registry))
	return err == nil
}

// Create makes a registry (or updates its documentation): description and
// the documented fields, whose order defines the initial spreadsheet
// columns. Returns false if the registry already existed.
func (l *Ledger) Create(registry, description string, fields []FieldDef) (bool, error) {
	if err := ValidateName(registry); err != nil {
		return false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	existed := l.Exists(registry)
	m, err := l.loadMeta(registry)
	if err != nil {
		return false, err
	}
	if description != "" {
		m.Description = description
	}
	if m.FieldDocs == nil {
		m.FieldDocs = map[string]string{}
	}
	known := map[string]bool{}
	for _, c := range m.Columns {
		known[c] = true
	}
	for _, fd := range fields {
		name := strings.TrimSpace(fd.Name)
		if name == "" {
			continue
		}
		if !known[name] {
			m.Columns = append(m.Columns, name)
			known[name] = true
		}
		if fd.Description != "" {
			m.FieldDocs[name] = fd.Description
		}
	}
	if err := l.saveMeta(registry, m); err != nil {
		return false, err
	}
	if err := l.writeXLSX(); err != nil {
		return !existed, fmt.Errorf("registry saved, but workbook update failed: %w", err)
	}
	return !existed, nil
}

// Drop permanently removes a registry (journal + meta) and regenerates
// the workbook.
func (l *Ledger) Drop(registry string) error {
	if err := ValidateName(registry); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.Exists(registry) {
		return fmt.Errorf("registry %q does not exist", registry)
	}
	if err := os.Remove(l.metaPath(registry)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(l.jsonlPath(registry)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return l.writeXLSX()
}

// Registries lists all registries with their documentation and counts.
func (l *Ledger) Registries() ([]Info, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	names, err := l.registryNames()
	if err != nil {
		return nil, err
	}
	infos := make([]Info, 0, len(names))
	for _, name := range names {
		m, err := l.loadMeta(name)
		if err != nil {
			return nil, err
		}
		entries, err := l.load(name)
		if err != nil {
			return nil, err
		}
		info := Info{Registry: name, Description: m.Description, Entries: len(entries)}
		for _, c := range m.Columns {
			info.Fields = append(info.Fields, FieldDef{Name: c, Description: m.FieldDocs[c]})
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// registryNames enumerates registries from their meta files.
func (l *Ledger) registryNames() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(l.dir, "registry-*.meta.json"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		base := strings.TrimSuffix(filepath.Base(m), ".meta.json")
		names = append(names, strings.TrimPrefix(base, "registry-"))
	}
	sort.Strings(names)
	return names, nil
}

// Load reads live entries of one registry (last write wins per key,
// tombstones drop the key, stable order of first appearance).
func (l *Ledger) Load(registry string) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.load(registry)
}

func (l *Ledger) load(registry string) ([]Entry, error) {
	f, err := os.Open(l.jsonlPath(registry))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var order []string
	byKey := map[string]Entry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), scanBufferCap)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Key == "" {
			continue // tolerate corrupt lines
		}
		if _, seen := byKey[e.Key]; !seen {
			order = append(order, e.Key)
		}
		byKey[e.Key] = e
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(order))
	for _, k := range order {
		if e := byKey[k]; !e.Deleted {
			out = append(out, e)
		}
	}
	return out, nil
}

func (l *Ledger) loadMeta(registry string) (registryMeta, error) {
	var m registryMeta
	data, err := os.ReadFile(l.metaPath(registry))
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return registryMeta{}, nil // corrupt meta: rebuild from scratch
	}
	return m, nil
}

func (l *Ledger) saveMeta(registry string, m registryMeta) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(l.metaPath(registry), data, 0o600)
}

func (l *Ledger) appendLine(registry string, e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(line) > maxEntryLine {
		return fmt.Errorf("entry too large (%d bytes, max %d): keep large content in files, not registry fields", len(line), maxEntryLine)
	}
	f, err := os.OpenFile(l.jsonlPath(registry), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Upsert inserts or updates an entry by Key in an existing registry and
// regenerates the workbook. Empty/nil field values of an update do not
// erase previously recorded values. Field names not documented at
// add_registry time are still stored (and become new columns); they are
// returned so the caller notices schema drift.
func (l *Ledger) Upsert(registry string, e Entry) (stored Entry, updated bool, undeclared []string, total int, err error) {
	if err := ValidateName(registry); err != nil {
		return Entry{}, false, nil, 0, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.Exists(registry) {
		return Entry{}, false, nil, 0, fmt.Errorf("registry %q does not exist; create it first with add_registry", registry)
	}
	if strings.TrimSpace(e.Key) == "" {
		return Entry{}, false, nil, 0, fmt.Errorf("key is required")
	}
	if len(e.Fields) == 0 {
		return Entry{}, false, nil, 0, fmt.Errorf("fields must not be empty")
	}

	entries, err := l.load(registry)
	if err != nil {
		return Entry{}, false, nil, 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, old := range entries {
		if old.Key != e.Key {
			continue
		}
		merged := old
		mergeEntry(&merged, e)
		merged.UpdatedAt = now
		e = merged
		updated = true
		break
	}
	if !updated {
		e.RecordedAt = now
		clean := make(map[string]any, len(e.Fields))
		for k, v := range e.Fields {
			if !emptyValue(v) {
				clean[k] = v
			}
		}
		e.Fields = clean
	}

	total = len(entries)
	if !updated {
		total++
	}

	// Track schema drift: append undeclared field names as new columns.
	m, err := l.loadMeta(registry)
	if err != nil {
		return Entry{}, false, nil, 0, err
	}
	known := map[string]bool{}
	for _, c := range m.Columns {
		known[c] = true
	}
	for k := range e.Fields {
		if !known[k] {
			undeclared = append(undeclared, k)
		}
	}
	sort.Strings(undeclared) // deterministic within one call
	if len(undeclared) > 0 {
		m.Columns = append(m.Columns, undeclared...)
		if err := l.saveMeta(registry, m); err != nil {
			return Entry{}, false, nil, 0, err
		}
	}

	if err := l.appendLine(registry, e); err != nil {
		return Entry{}, false, nil, 0, err
	}
	if err := l.writeXLSX(); err != nil {
		return e, updated, undeclared, total, fmt.Errorf("entry recorded, but workbook update failed: %w", err)
	}
	return e, updated, undeclared, total, nil
}

// Remove tombstones one entry. Returns whether it existed.
func (l *Ledger) Remove(registry, key string) (bool, error) {
	if err := ValidateName(registry); err != nil {
		return false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := l.load(registry)
	if err != nil {
		return false, err
	}
	found := false
	for _, e := range entries {
		if e.Key == key {
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	if err := l.appendLine(registry, Entry{Key: key, Deleted: true, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		return false, err
	}
	return true, l.writeXLSX()
}

func emptyValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// mergeEntry copies non-empty fields of src over dst.
func mergeEntry(dst *Entry, src Entry) {
	if dst.Fields == nil {
		dst.Fields = map[string]any{}
	}
	for k, v := range src.Fields {
		if !emptyValue(v) {
			dst.Fields[k] = v
		}
	}
	if src.Source.Mailbox != "" {
		dst.Source.Mailbox = src.Source.Mailbox
	}
	if src.Source.UID != 0 {
		dst.Source.UID = src.Source.UID
	}
	if src.Source.AttachmentIndex != nil {
		dst.Source.AttachmentIndex = src.Source.AttachmentIndex
	}
	if src.Source.SHA256 != "" {
		dst.Source.SHA256 = src.Source.SHA256
	}
	if src.Source.SavedPath != "" {
		dst.Source.SavedPath = src.Source.SavedPath
	}
}

// SortNewestFirst orders entries by RecordedAt descending — the one
// definition of "newest first" shared by the workbook and list_entries.
func SortNewestFirst(entries []Entry) {
	sort.SliceStable(entries, func(a, b int) bool {
		return entries[a].RecordedAt > entries[b].RecordedAt
	})
}

// WriteXLSX regenerates the workbook: one sheet per registry, dynamic
// columns in persisted order, newest entries first. With no registries
// the workbook is removed.
func (l *Ledger) WriteXLSX() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writeXLSX()
}

func (l *Ledger) writeXLSX() error {
	names, err := l.registryNames()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		if err := os.Remove(l.XLSXPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	f := excelize.NewFile()
	defer f.Close()
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})

	for i, reg := range names {
		sheet := sheetName(reg)
		if i == 0 {
			if err := f.SetSheetName("Sheet1", sheet); err != nil {
				return err
			}
		} else if _, err := f.NewSheet(sheet); err != nil {
			return err
		}

		entries, err := l.load(reg)
		if err != nil {
			return err
		}
		SortNewestFirst(entries)
		m, err := l.loadMeta(reg)
		if err != nil {
			return err
		}
		cols := m.Columns

		header := append([]string{"Key"}, cols...)
		header = append(header, "Quelle", "Mail-UID", "Erfasst", "Aktualisiert")
		for col, h := range header {
			cell, _ := excelize.CoordinatesToCellName(col+1, 1)
			if err := f.SetCellValue(sheet, cell, h); err != nil {
				return err
			}
		}
		_ = f.SetRowStyle(sheet, 1, 1, bold)

		for row, e := range entries {
			values := make([]any, 0, len(header))
			values = append(values, e.Key)
			for _, c := range cols {
				values = append(values, cellValue(e.Fields[c]))
			}
			file := e.Source.SavedPath
			if file != "" {
				file = filepath.Base(file)
			}
			var uid any
			if e.Source.UID != 0 {
				uid = e.Source.UID
			}
			values = append(values, file, uid, e.RecordedAt, e.UpdatedAt)
			for col, v := range values {
				cell, _ := excelize.CoordinatesToCellName(col+1, row+2)
				if err := f.SetCellValue(sheet, cell, v); err != nil {
					return err
				}
				// Any URL-valued field becomes clickable — this is how a
				// direct document link (e.g. the real Drive file URL the
				// model fetched after sync) lands in the sheet.
				if s, ok := v.(string); ok && (strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")) {
					if err := f.SetCellHyperLink(sheet, cell, s, "External"); err != nil {
						return err
					}
				}
			}
			// Fallback link on the source-file cell (e.g. a Drive by-name
			// search) so the document opens from any machine even before a
			// direct link was recorded.
			if file != "" && l.docLink != nil {
				if link := l.docLink(file); link != "" {
					cell, _ := excelize.CoordinatesToCellName(len(header)-3, row+2)
					if err := f.SetCellHyperLink(sheet, cell, link, "External"); err != nil {
						return err
					}
				}
			}
		}
		last, _ := excelize.ColumnNumberToName(len(header))
		_ = f.SetColWidth(sheet, "A", last, 18)
		_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	}

	// excelize validates the extension, so the temp file must be .xlsx too.
	tmp := filepath.Join(l.dir, ".tmp-"+XLSXName)
	if err := f.SaveAs(tmp); err != nil {
		return err
	}
	return os.Rename(tmp, l.XLSXPath())
}

// cellValue flattens non-scalar field values to JSON for the sheet.
func cellValue(v any) any {
	switch v.(type) {
	case nil:
		return ""
	case string, float64, int, int64, bool:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// sheetName makes a registry name safe for Excel (<=31 chars, no
// forbidden characters — our slugs already exclude them).
func sheetName(registry string) string {
	if len(registry) > 31 {
		registry = registry[:31]
	}
	return registry
}
