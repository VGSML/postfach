package screen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// LLMGuard screens text with a local guard LLM (e.g. Qwen3Guard-Gen) served
// over an OpenAI-compatible chat API — LM Studio (http://localhost:1234/v1)
// and Ollama (http://localhost:11434/v1) both qualify. It complements
// Prompt Guard 2 for languages PG2 does not cover reliably (Eastern
// Europe, Scandinavia, ...).
//
// Configuration (all via env):
//
//	POSTFACH_GUARD_LLM_MODEL model id as served (enables the screener),
//	                         e.g. qwen3guard-gen-0.6b
//	POSTFACH_GUARD_LLM_URL   base URL, default http://localhost:1234/v1
//
// The guard model classifies a whole prompt, so like PG2 it is scanned in
// windows; guard LLMs are markedly more dilution-resistant than the
// classifier, so a single window scale suffices.
type LLMGuard struct {
	url    string
	model  string
	client *http.Client
}

// Two-scale scanning, like PG2: the guard model judges a whole window, so
// a one-sentence injection at the tail of a long benign email dilutes below
// "unsafe" in a large window (measured with Qwen3Guard-Gen 0.6B: a Danish
// tail injection is missed in 900+-rune windows, caught when it makes up
// >=40% of the window). Fine blocks follow sentence boundaries so a
// injected sentence mostly gets judged on its own.
const (
	llmGuardWindow    = 1200 // runes per coarse window
	llmGuardOverlap   = 200
	llmGuardBlock     = 200 // target runes per fine block
	llmGuardBlockMax  = 400 // single segments longer than this get rune-chunked
	llmGuardBlockOver = 80
)

var segmentSplit = regexp.MustCompile(`(?s).*?(?:[.!?…]+[\s"')\]]|\n+)`)

// segments splits text at sentence enders and newlines, then packs
// consecutive pieces into blocks of at most target runes. A single piece
// longer than hardMax is rune-chunked so nothing exceeds the fine scale.
func segments(text string, target, hardMax int) []string {
	var parts []string
	rest := text
	for len(rest) > 0 {
		loc := segmentSplit.FindStringIndex(rest)
		if loc == nil || loc[1] == 0 {
			parts = append(parts, rest)
			break
		}
		parts = append(parts, rest[:loc[1]])
		rest = rest[loc[1]:]
	}

	var blocks []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			blocks = append(blocks, s)
		}
		cur.Reset()
		curLen = 0
	}
	for _, p := range parts {
		n := len([]rune(p))
		if n > hardMax {
			flush()
			blocks = append(blocks, chunkRunes(p, hardMax, llmGuardBlockOver)...)
			continue
		}
		if curLen+n > target && curLen > 0 {
			flush()
		}
		cur.WriteString(p)
		curLen += n
	}
	flush()
	return blocks
}

// NewLLMGuardFromEnv builds the screener if POSTFACH_GUARD_LLM_MODEL is
// set; returns (nil, nil) when it is not configured.
func NewLLMGuardFromEnv() (Screener, error) {
	model := os.Getenv("POSTFACH_GUARD_LLM_MODEL")
	if model == "" {
		return nil, nil
	}
	url := os.Getenv("POSTFACH_GUARD_LLM_URL")
	if url == "" {
		url = "http://localhost:1234/v1"
	}
	g := &LLMGuard{
		url:    strings.TrimRight(url, "/"),
		model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
	// Fail fast on a dead endpoint instead of erroring per tool call.
	if err := g.ping(); err != nil {
		return nil, fmt.Errorf("guard LLM endpoint %s: %w", g.url, err)
	}
	return g, nil
}

func (g *LLMGuard) ping() error {
	req, err := http.NewRequest(http.MethodGet, g.url+"/models", nil)
	if err != nil {
		return err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func (g *LLMGuard) Name() string { return "llmguard" }

var (
	reSafety = regexp.MustCompile(`(?i)safety\s*:\s*(safe|unsafe|controversial)`)
	// Attack-shaped categories. Content categories (PII, adult, politics…)
	// are normal in email and must not flag on their own: benign invoices
	// routinely come back "Controversial | Categories: PII".
	reAttackCat = regexp.MustCompile(`(?i)categor(?:y|ies)\s*:.*?(jailbreak|injection|illegal|crime)`)
)

func (g *LLMGuard) Screen(ctx context.Context, text string) (Verdict, error) {
	var v Verdict
	if strings.TrimSpace(text) == "" {
		return v, nil
	}
	chunks := chunkRunes(text, llmGuardWindow, llmGuardOverlap)
	if len([]rune(text)) > llmGuardBlock {
		chunks = append(chunks, segments(text, llmGuardBlock, llmGuardBlockMax)...)
	}
	for i, chunk := range chunks {
		verdictLine, err := g.classify(ctx, chunk)
		if err != nil {
			return v, fmt.Errorf("llmguard (window %d/%d): %w", i+1, len(chunks), err)
		}
		safety := ""
		if m := reSafety.FindStringSubmatch(verdictLine); m != nil {
			safety = strings.ToLower(m[1])
		}
		// Unsafe always flags. Controversial flags only with an
		// attack-shaped category — measured: injection fragments diluted by
		// benign prose come back as "Controversial | Non-violent Illegal
		// Acts", while benign invoices are "Controversial | PII".
		attack := reAttackCat.MatchString(verdictLine)
		if safety == "unsafe" || (safety == "controversial" && attack) || (safety == "" && attack) {
			v.Flagged = true
			v.Reasons = append(v.Reasons, fmt.Sprintf(
				"llmguard:%s in window %d/%d", strings.TrimSpace(verdictLine), i+1, len(chunks)))
			return v, nil
		}
	}
	return v, nil
}

func (g *LLMGuard) classify(ctx context.Context, text string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":       g.model,
		"messages":    []map[string]string{{"role": "user", "content": text}},
		"temperature": 0,
		"max_tokens":  64,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("guard LLM status %s", resp.Status)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("guard LLM returned no choices")
	}
	// Qwen3Guard-Gen replies like "Safety: Unsafe\nCategories: Jailbreak".
	content := out.Choices[0].Message.Content
	// Some runtimes keep <think> blocks; the verdict lines are what matter.
	content = strings.TrimSpace(content)
	if len(content) > 200 {
		content = content[len(content)-200:]
	}
	return content, nil
}
