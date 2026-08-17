// postfach-mcp is a local stdio MCP server exposing email tools.
// Mailbox credentials come from the environment (see internal/config).
// All logging goes to stderr: stdout is the JSON-RPC channel.
package main

import (
	"fmt"
	"log"
	"os"

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

	// Screening chain: heuristics now; the Prompt Guard 2 (ONNX/CoreML)
	// screener will be appended here once wired up.
	screener := screen.Chain{screen.NewHeuristic()}

	s := server.NewMCPServer("postfach", version,
		server.WithToolCapabilities(true),
	)
	tools.New(cfg, screener).Register(s)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "postfach-mcp error: %v\n", err)
		os.Exit(1)
	}
}
