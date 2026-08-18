# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Postfach is an MCP (Model Context Protocol) server that exposes tools for working with email. The end goal is a remote MCP service; the current stage is a **PoC: a local stdio MCP server** that:

- reads mailbox connection settings from environment variables (connection-string style),
- provides tools for reading email and saving attachments,
- screens all email-derived content for prompt injection before returning it to the client.

## Stack

- **Go** (parent `../go.work` pins `go 1.26.1`; this module is listed in it), single binary, no sidecars.
- **MCP:** `github.com/mark3labs/mcp-go` — same library and pattern as sibling `hub`: `server.NewMCPServer` + `server.ServeStdio`, entrypoint `cmd/postfach-mcp`.
- **Mail:** `github.com/emersion/go-imap/v2` (IMAP) + `github.com/emersion/go-message` (MIME/attachments) in `internal/mail`.
- **Screening** (`internal/screen`): `Screener` interface, chain of two layers — regex heuristics (EN/RU/DE, always on) and **Llama Prompt Guard 2 86M** int8 ONNX via `github.com/yalue/onnxruntime_go` + `github.com/daulet/tokenizers`, compiled only with **build tag `promptguard`** (the default build has a stub and needs no native deps).
- Model and native libs are **not committed**: `make fetch-model` / `make deps-guard` download them into `models/` and `third_party/` (gitignored).

### Prompt Guard 2 facts (measured, design around them)

- **Score dilution:** the classifier scores a whole window, so a 16-token injection scores 0.99 alone but 0.04 inside ~100 benign tokens. Hence two-scale scanning in `promptguard.go`: coarse 510-token windows + fine 64-token windows (stride 32), max score wins. Don't "optimize" the fine scan away.
- **CoreML EP is opt-in** (`POSTFACH_PG2_COREML=1`) and currently slower than CPU: the export has unbounded dims, CoreML rejects nodes and recompiles per shape. Fixed-shape re-export is the path to GPU/ANE.
- **ORT version coupling:** the `onnxruntime` release downloaded by the Makefile must match the ORT API version `yalue/onnxruntime_go` expects (v1.33 → ORT 1.29). A mismatch fails at init with "requested API version".
- **Language coverage decides the variant.** 86M (mDeBERTa, default) catches DE injections at 0.99 and — beyond its official 8 languages — RU at 0.96 (measured). The 22M variant (DeBERTa-xsmall, `make fetch-model PG2_VARIANT=22m`) is 4× smaller, ~2× faster and more dilution-resistant, but **blind to non-English injections** (DE scores 0.003) — never use it for the German invoice mailbox. Heuristics carry RU/DE regardless; the `Screener` interface allows swapping in another model (e.g. Qwen3Guard) later.
- Tokenizer is loaded with truncation/padding stripped from `tokenizer.json` (`loadTokenizerNoTruncation`) so long texts tokenize fully — keep it that way, silent truncation defeats chunking.

### go.work gotcha (important)

This repo sits inside `~/projects/hugr-lab/`, which has a `go.work` that does **not** list `postfach`. Once `go.mod` exists here, plain `go build`/`go test` will fail with a workspace error. Either:

- add `./postfach` to `../go.work` (`go work use .` from this directory), or
- run Go commands with `GOWORK=off`.

## Commands

```sh
go build ./... && go vet ./... && go test ./...   # default build, no native deps
go test -run TestName ./internal/screen/          # single test

make deps-guard fetch-model   # one-time: native libs + model (~280 MB)
make build-guard              # binary with -tags promptguard (needs CGO_LDFLAGS, see Makefile)
make test-guard               # integration tests against the real model
```

## Architecture Constraints

- **Transport-agnostic tool layer.** The stdio server is a PoC; the service will later become a remote MCP (streamable HTTP). Keep tool handlers (mail access, attachment saving, injection screening) decoupled from the transport so only the `cmd/` entrypoint changes.
- **stdio discipline.** With stdio transport, stdout is the JSON-RPC channel — all logging must go to stderr.
- **Email content is untrusted input.** Every string originating from a mailbox (subjects, bodies, sender names, attachment filenames) passes through the screening layer before it is placed in a tool result. Screening is layered: cheap heuristics, the language allowlist gate (`POSTFACH_ALLOWED_LANGS`, default en,de,ru — text the stack cannot vet is flagged, script check catches embedded foreign-script fragments), then the Prompt Guard 2 classifier. This layer sits between the mail client and the tool response and is not optional per-tool.
- **Pagination never weakens screening.** `read_message`/`read_attachment` page long text by characters, but the FULL text is screened on every call before the page is cut. Keep that invariant.
- **Reads are non-destructive.** Mailboxes are opened read-only (`SelectOptions{ReadOnly: true}` = EXAMINE) and body fetches use `Peek` — `\Seen` is never set. Incremental ingestion uses `since_uid` + `uid_validity` from `list_messages`, not read/unread flags.
- **Attachment ledger.** `save_attachment` appends to `registry.jsonl` in the attachments dir (one JSON line per save, `sha256` is the dedup key) and skips writing content whose hash is already recorded.
- **Attachment safety.** Attachments are written only under a configured output directory; sanitize filenames against path traversal.
- **Configuration via env only.** Mailbox credentials/connection strings come from environment variables; never log them or echo them back through tool results.

## Roadmap notes

- First real deployment: an invoice mailbox (PDF + e-Rechnung XML). Planned tool `get_attached_erechnung`: parse XRechnung (UBL/CII) and ZUGFeRD/Factur-X (XML embedded in PDF/A-3) on the Go side into structured JSON instead of dumping raw XML into context.
- `read_attachment` returns content inline (screened text / image / base64 blob) capped by `POSTFACH_MAX_INLINE_MB`; larger files go through `save_attachment`.
