package screen

import (
	"context"
	"path/filepath"
	"testing"
)

type countingScreener struct {
	calls int
	v     Verdict
}

func (c *countingScreener) Name() string { return "counting" }
func (c *countingScreener) Screen(context.Context, string) (Verdict, error) {
	c.calls++
	return c.v, nil
}

func TestCacheMemoizes(t *testing.T) {
	inner := &countingScreener{v: Verdict{Flagged: true, Reasons: []string{"x"}}}
	c, err := NewCache(inner, "fp1", "")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		v, err := c.Screen(context.Background(), "same text")
		if err != nil {
			t.Fatal(err)
		}
		if !v.Flagged || len(v.Reasons) != 1 {
			t.Fatalf("verdict lost: %+v", v)
		}
	}
	if inner.calls != 1 {
		t.Errorf("inner called %d times, want 1", inner.calls)
	}
	if _, err := c.Screen(context.Background(), "other text"); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Errorf("inner called %d times, want 2", inner.calls)
	}
}

func TestCachePersistsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.jsonl")

	inner1 := &countingScreener{v: Verdict{Flagged: true, Reasons: []string{"r"}}}
	c1, err := NewCache(inner1, "fp", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c1.Screen(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}

	inner2 := &countingScreener{}
	c2, err := NewCache(inner2, "fp", path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := c2.Screen(context.Background(), "text")
	if err != nil {
		t.Fatal(err)
	}
	if inner2.calls != 0 {
		t.Errorf("persisted entry not used: %d inner calls", inner2.calls)
	}
	if !v.Flagged || len(v.Reasons) != 1 || v.Reasons[0] != "r" {
		t.Errorf("verdict not restored: %+v", v)
	}
}

func TestCacheFingerprintSeparation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.jsonl")
	c1, _ := NewCache(&countingScreener{}, "fp-a", path)
	if _, err := c1.Screen(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}

	inner := &countingScreener{}
	c2, _ := NewCache(inner, "fp-b", path)
	if _, err := c2.Screen(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Errorf("different fingerprint must miss: %d calls", inner.calls)
	}
}
