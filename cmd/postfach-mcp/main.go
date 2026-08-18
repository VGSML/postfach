// postfach-mcp is a local stdio MCP server exposing email tools.
// Mailbox credentials come from the environment (see internal/config).
// All logging goes to stderr: stdout is the JSON-RPC channel.
package main

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/server"

	"github.com/hugr-lab/postfach/internal/config"
	"github.com/hugr-lab/postfach/internal/screen"
	"github.com/hugr-lab/postfach/internal/tools"
)

const version = "0.1.0"

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("postfach-mcp: ")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("imap %s as %s, attachments dir %s", cfg.Addr(), cfg.Username, cfg.AttachmentsDir)

	// Screening chain: heuristics, then the language allowlist gate, then
	// the Prompt Guard 2 classifier when configured (POSTFACH_PG2_MODEL +
	// build tag promptguard).
	screener := screen.Chain{screen.NewHeuristic()}
	// Languages the screening stack can actually vet. Text in any other
	// language/script is flagged: an injection we cannot screen is an
	// injection we must not trust. The default is the set where PG2-86M
	// (quantized) scored reliably in our probes — see README for the
	// measured table. POSTFACH_ALLOWED_LANGS overrides ("any" disables).
	langs := os.Getenv("POSTFACH_ALLOWED_LANGS")
	if langs == "" {
		langs = "en,de,fr,it,es,ru"
	}
	if langs == "any" || langs == "*" {
		log.Printf("language gate disabled (POSTFACH_ALLOWED_LANGS=%s)", langs)
	} else {
		gate, err := screen.NewLanguageGate(strings.Split(langs, ","))
		if err != nil {
			log.Fatalf("language gate: %v", err)
		}
		screener = append(screener, gate)
		log.Printf("language allowlist: %s", langs)
	}
	pg, err := screen.NewPromptGuardFromEnv()
	if err != nil {
		log.Fatalf("promptguard: %v", err)
	}
	if pg != nil {
		screener = append(screener, pg)
		log.Printf("prompt guard 2 classifier enabled")
	} else {
		log.Printf("prompt guard 2 classifier disabled (POSTFACH_PG2_MODEL not set)")
	}
	// Guard LLM (e.g. Qwen3Guard via LM Studio/Ollama, OpenAI-compatible
	// API) — multilingual coverage beyond PG2's reliable set.
	llm, err := screen.NewLLMGuardFromEnv()
	if err != nil {
		log.Fatalf("guard LLM: %v", err)
	}
	if llm != nil {
		screener = append(screener, llm)
		log.Printf("guard LLM enabled: %s", os.Getenv("POSTFACH_GUARD_LLM_MODEL"))
	} else {
		log.Printf("guard LLM disabled (POSTFACH_GUARD_LLM_MODEL not set)")
	}

	// Verdict cache: identical content is screened once. The fingerprint
	// covers everything that changes verdicts.
	fingerprint := strings.Join([]string{
		"v1", langs,
		os.Getenv("POSTFACH_PG2_MODEL"), os.Getenv("POSTFACH_PG2_THRESHOLD"),
		os.Getenv("POSTFACH_GUARD_LLM_MODEL"),
	}, "|")
	cached, err := screen.NewCache(screener, fingerprint,
		filepath.Join(cfg.AttachmentsDir, "screening_cache.jsonl"))
	if err != nil {
		log.Fatalf("screening cache: %v", err)
	}

	s := server.NewMCPServer("postfach", version,
		server.WithToolCapabilities(true),
	)
	tools.New(cfg, cached).Register(s)

	// Transport: stdio by default. POSTFACH_HTTP_ADDR switches to
	// streamable HTTP — the remote-MCP mode used by ChatGPT developer-mode
	// connectors and remote Claude setups. Serving a mailbox over HTTP
	// without auth is not allowed: a bearer token is mandatory.
	if addr := os.Getenv("POSTFACH_HTTP_ADDR"); addr != "" {
		token := os.Getenv("POSTFACH_HTTP_TOKEN")
		if len(token) < 16 {
			log.Fatalf("POSTFACH_HTTP_ADDR is set, but POSTFACH_HTTP_TOKEN is missing or shorter than 16 characters — refusing to serve the mailbox unauthenticated")
		}
		httpSrv := server.NewStreamableHTTPServer(s)
		mux := http.NewServeMux()
		mux.Handle("/mcp", requireBearer(token, httpSrv))
		log.Printf("serving streamable HTTP MCP on %s (endpoint /mcp)", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("http: %v", err)
		}
		return
	}

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "postfach-mcp error: %v\n", err)
		os.Exit(1)
	}
}

// requireBearer enforces `Authorization: Bearer <token>` with a
// constant-time comparison.
func requireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
