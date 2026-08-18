package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// registryFile is the append-only JSONL ledger of saved attachments, kept
// inside the attachments directory. One line per saved file; SHA256 is the
// dedup key.
const registryFile = "registry.jsonl"

type registryRecord struct {
	SavedAt          string `json:"saved_at"`
	Mailbox          string `json:"mailbox"`
	UID              uint32 `json:"uid"`
	AttachmentIndex  int    `json:"attachment_index"`
	OriginalFilename string `json:"original_filename,omitempty"`
	SavedPath        string `json:"saved_path"`
	SizeBytes        int64  `json:"size_bytes"`
	SHA256           string `json:"sha256"`
	ContentType      string `json:"content_type,omitempty"`
	Subject          string `json:"message_subject,omitempty"`
	From             string `json:"message_from,omitempty"`
	MessageDate      string `json:"message_date,omitempty"`
	ScreeningFlagged bool   `json:"screening_flagged,omitempty"`
}

// findInRegistry returns the first record with the given SHA256, or nil.
// A missing registry file is not an error.
func findInRegistry(dir, sha string) (*registryRecord, error) {
	f, err := os.Open(filepath.Join(dir, registryFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var rec registryRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue // tolerate a corrupt line rather than blocking saves
		}
		if rec.SHA256 == sha {
			return &rec, nil
		}
	}
	return nil, sc.Err()
}

// appendToRegistry stamps and appends one record.
func appendToRegistry(dir string, rec registryRecord) error {
	rec.SavedAt = time.Now().UTC().Format(time.RFC3339)
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, registryFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open registry: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append registry: %w", err)
	}
	return nil
}
