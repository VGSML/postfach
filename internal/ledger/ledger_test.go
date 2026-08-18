package ledger

import (
	"os"
	"testing"

	"github.com/xuri/excelize/v2"
)

func mustCreate(t *testing.T, l *Ledger, name, desc string, fields ...FieldDef) {
	t.Helper()
	if _, err := l.Create(name, desc, fields); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycle(t *testing.T) {
	l := New(t.TempDir())
	mustCreate(t, l, "rechnungen", "Eingehende Rechnungen",
		FieldDef{Name: "betrag", Description: "Bruttobetrag"},
		FieldDef{Name: "status", Description: "new/geprüft/bezahlt"})

	// Insert.
	_, updated, undeclared, err := l.Upsert("rechnungen", Entry{
		Key:    "RE-1|Berlin Recycling",
		Fields: map[string]any{"betrag": "1190.00", "status": "new"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated || len(undeclared) != 0 {
		t.Errorf("insert: updated=%v undeclared=%v", updated, undeclared)
	}

	// Update by same key: status changes, betrag survives; empty ignored;
	// a new field is reported as undeclared but stored.
	stored, updated, undeclared, err := l.Upsert("rechnungen", Entry{
		Key:    "RE-1|Berlin Recycling",
		Fields: map[string]any{"status": "bezahlt", "betrag": "", "kategorie": "Entsorgung"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated || stored.Fields["betrag"] != "1190.00" || stored.Fields["status"] != "bezahlt" {
		t.Errorf("merge failed: %+v", stored)
	}
	if len(undeclared) != 1 || undeclared[0] != "kategorie" {
		t.Errorf("undeclared = %v", undeclared)
	}

	// Second registry; enumeration carries docs and counts.
	mustCreate(t, l, "mahnungen", "Zahlungserinnerungen", FieldDef{Name: "stufe"})
	infos, err := l.Registries()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].Registry != "mahnungen" || infos[1].Entries != 1 {
		t.Errorf("infos: %+v", infos)
	}
	if infos[1].Description != "Eingehende Rechnungen" || infos[1].Fields[0].Description != "Bruttobetrag" {
		t.Errorf("docs lost: %+v", infos[1])
	}

	// Remove entry (tombstone), then drop the registry.
	if ok, err := l.Remove("rechnungen", "RE-1|Berlin Recycling"); err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	if ok, _ := l.Remove("rechnungen", "nope"); ok {
		t.Error("removed nonexistent key")
	}
	entries, _ := l.Load("rechnungen")
	if len(entries) != 0 {
		t.Errorf("tombstone ignored: %+v", entries)
	}
	if err := l.Drop("mahnungen"); err != nil {
		t.Fatal(err)
	}
	infos, _ = l.Registries()
	if len(infos) != 1 {
		t.Errorf("after drop: %+v", infos)
	}
}

func TestUpsertValidation(t *testing.T) {
	l := New(t.TempDir())
	if _, _, _, err := l.Upsert("nope", Entry{Key: "k", Fields: map[string]any{"a": 1}}); err == nil {
		t.Error("upsert into nonexistent registry accepted")
	}
	mustCreate(t, l, "ok", "")
	if _, _, _, err := l.Upsert("Bad Name!", Entry{Key: "k", Fields: map[string]any{"a": 1}}); err == nil {
		t.Error("invalid registry name accepted")
	}
	if _, _, _, err := l.Upsert("ok", Entry{Fields: map[string]any{"a": 1}}); err == nil {
		t.Error("missing key accepted")
	}
	if _, _, _, err := l.Upsert("ok", Entry{Key: "k"}); err == nil {
		t.Error("empty fields accepted")
	}
}

func TestXLSXWorkbook(t *testing.T) {
	l := New(t.TempDir())
	mustCreate(t, l, "rechnungen", "", FieldDef{Name: "verkaeufer"}, FieldDef{Name: "brutto"})
	mustCreate(t, l, "anfragen", "", FieldDef{Name: "thema"})
	if _, _, _, err := l.Upsert("rechnungen", Entry{
		Key:    "RE-9",
		Fields: map[string]any{"verkaeufer": "ACME", "brutto": "10.00"},
		Source: Source{UID: 42, SavedPath: "/x/re9.pdf"},
	}); err != nil {
		t.Fatal(err)
	}

	f, err := excelize.OpenFile(l.XLSXPath())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if sheets := f.GetSheetList(); len(sheets) != 2 {
		t.Fatalf("sheets: %v", sheets)
	}
	rows, err := f.GetRows("rechnungen")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][0] != "RE-9" || rows[1][1] != "ACME" {
		t.Errorf("rechnungen rows: %v", rows)
	}
	if header := rows[0]; header[0] != "Key" || header[1] != "verkaeufer" || header[2] != "brutto" {
		t.Errorf("declared column order lost: %v", header)
	}

	if err := os.Remove(l.XLSXPath()); err != nil {
		t.Fatal(err)
	}
	if err := l.WriteXLSX(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.XLSXPath()); err != nil {
		t.Error("workbook not regenerated")
	}
}
