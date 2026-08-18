// postfach-mcp is a local stdio MCP server exposing email tools.
// Mailbox credentials come from the environment (see internal/config).
// All logging goes to stderr: stdout is the JSON-RPC channel.
package main

import (
	"fmt"
	"log"
	"os"
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
		log.Printf("prompt guard 2 classifier disabled (POSTFACH_PG2_MODEL not set); heuristics only")
	}

	s := server.NewMCPServer("postfach", version,
		server.WithToolCapabilities(true),
	)
	tools.New(cfg, screener).Register(s)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "postfach-mcp error: %v\n", err)
		os.Exit(1)
	}
}
