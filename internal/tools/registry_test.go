package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryAppendAndFind(t *testing.T) {
	dir := t.TempDir()

	if rec, err := findInRegistry(dir, "deadbeef"); err != nil || rec != nil {
		t.Fatalf("empty registry: rec=%v err=%v", rec, err)
	}

	first := registryRecord{
		Mailbox: "INBOX", UID: 7, AttachmentIndex: 0,
		OriginalFilename: "rechnung.pdf", SavedPath: "/x/rechnung.pdf",
		SizeBytes: 123, SHA256: "aaa", ContentType: "application/pdf",
	}
	if err := appendToRegistry(dir, first); err != nil {
		t.Fatal(err)
	}
	if err := appendToRegistry(dir, registryRecord{UID: 8, SHA256: "bbb", SavedPath: "/x/other.pdf"}); err != nil {
		t.Fatal(err)
	}

	rec, err := findInRegistry(dir, "aaa")
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.UID != 7 || rec.SavedPath != "/x/rechnung.pdf" || rec.SavedAt == "" {
		t.Errorf("unexpected record: %+v", rec)
	}
	if rec, _ := findInRegistry(dir, "ccc"); rec != nil {
		t.Errorf("phantom record: %+v", rec)
	}
}

func TestRegistryToleratesCorruptLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, registryFile),
		[]byte("not-json\n{\"sha256\":\"aaa\",\"saved_path\":\"/x/a\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := findInRegistry(dir, "aaa")
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.SavedPath != "/x/a" {
		t.Errorf("record after corrupt line not found: %+v", rec)
	}
}
