package screen

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cache memoizes screening verdicts by content hash, so the same subject,
// body or attachment is never screened twice — repeated reads and paging
// through a long message cost model inference only once. Persisted as an
// append-only JSONL journal.
//
// The fingerprint must encode everything that changes verdicts (screener
// set, model paths, thresholds, language allowlist): a different
// fingerprint simply misses all old entries.
type Cache struct {
	inner       Screener
	fingerprint string
	path        string // "" = in-memory only

	mu  sync.Mutex
	mem map[string]Verdict
}

type cacheEntry struct {
	Key     string   `json:"key"`
	Flagged bool     `json:"flagged"`
	Reasons []string `json:"reasons,omitempty"`
	At      string   `json:"at"`
}

// NewCache wraps inner. If path is non-empty, existing entries are loaded
// and new ones appended there.
func NewCache(inner Screener, fingerprint, path string) (*Cache, error) {
	c := &Cache{inner: inner, fingerprint: fingerprint, path: path, mem: map[string]Verdict{}}
	if path == "" {
		return c, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var e cacheEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // tolerate corrupt lines
		}
		c.mem[e.Key] = Verdict{Flagged: e.Flagged, Reasons: e.Reasons}
	}
	return c, sc.Err()
}

func (c *Cache) Name() string { return "cache(" + c.inner.Name() + ")" }

func (c *Cache) Screen(ctx context.Context, text string) (Verdict, error) {
	sum := sha256.Sum256([]byte(c.fingerprint + "\x00" + text))
	key := hex.EncodeToString(sum[:])

	c.mu.Lock()
	if v, ok := c.mem[key]; ok {
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	v, err := c.inner.Screen(ctx, text)
	if err != nil {
		return v, err // never cache errors
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.mem[key]; ok {
		return v, nil // concurrent call beat us; entry already journaled
	}
	c.mem[key] = v
	if c.path != "" {
		line, err := json.Marshal(cacheEntry{
			Key: key, Flagged: v.Flagged, Reasons: v.Reasons,
			At: time.Now().UTC().Format(time.RFC3339),
		})
		if err == nil {
			if f, err := os.OpenFile(c.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = f.Write(append(line, '\n'))
				_ = f.Close()
			}
		}
	}
	return v, nil
}
