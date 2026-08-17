# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Postfach is an MCP (Model Context Protocol) server that exposes tools for working with email. The end goal is a remote MCP service; the current stage is a **PoC: a local stdio MCP server** that:

- reads mailbox connection settings from environment variables (connection-string style),
- provides tools for reading email and saving attachments,
- screens all email-derived content for prompt injection before returning it to the client.

The repository currently has no code — this file records the decided stack, intended design, and org conventions.

## Stack (decided)

- **Go** (parent `../go.work` pins `go 1.26.1`), single binary, no sidecars.
- **MCP:** `github.com/mark3labs/mcp-go` — same library and pattern as sibling `hub` (`../hub/cmd/*-mcp/main.go`): `server.NewMCPServer(name, version, ...)` + `server.ServeStdio(s)`, one binary per MCP server under `cmd/`.
- **Mail:** `github.com/emersion/go-imap/v2` for IMAP, `github.com/emersion/go-message` for MIME parsing and attachment extraction.
- **Prompt-injection screening: in-process ONNX Runtime** (e.g. `github.com/yalue/onnxruntime_go`, cgo) with the **CoreML execution provider** on macOS for Apple GPU/ANE, running **Llama Prompt Guard 2 86M** (ONNX export exists on Hugging Face, e.g. `gravitee-io/Llama-Prompt-Guard-2-86M-onnx`; tokenizer.json ships with it — use HF-tokenizers Go bindings such as `github.com/daulet/tokenizers`).
- Model files are **not committed**; they are downloaded to a local cache dir whose path comes from an env var.

### Known limits of Prompt Guard 2 86M (design around them)

- **512-token inspection window** — long email bodies must be scanned in overlapping chunks; the message is flagged if *any* chunk classifies as injection/jailbreak.
- **8 languages, no Russian** — keep the screener behind a `Screener` interface so the model/runtime can be swapped (e.g. Ollama + Qwen3Guard-Gen 0.6B for multilingual) without touching tool code.

### go.work gotcha (important)

This repo sits inside `~/projects/hugr-lab/`, which has a `go.work` that does **not** list `postfach`. Once `go.mod` exists here, plain `go build`/`go test` will fail with a workspace error. Either:

- add `./postfach` to `../go.work` (`go work use .` from this directory), or
- run Go commands with `GOWORK=off`.

## Commands

Standard Go workflow (after the module is created):

```sh
go build ./...
go vet ./...
go test ./...
go test -run TestName ./path/to/pkg/   # single test
```

## Architecture Constraints

- **Transport-agnostic tool layer.** The stdio server is a PoC; the service will later become a remote MCP (streamable HTTP). Keep tool handlers (mail access, attachment saving, injection screening) decoupled from the transport so only the `cmd/` entrypoint changes.
- **stdio discipline.** With stdio transport, stdout is the JSON-RPC channel — all logging must go to stderr.
- **Email content is untrusted input.** Every string originating from a mailbox (subjects, bodies, sender names, attachment filenames) passes through the screening layer before it is placed in a tool result. Screening is layered: cheap heuristics in Go first, then the Prompt Guard 2 classifier. This layer sits between the mail client and the tool response and is not optional per-tool.
- **Attachment safety.** Attachments are written only under a configured output directory; sanitize filenames against path traversal.
- **Configuration via env only.** Mailbox credentials/connection strings come from environment variables; never log them or echo them back through tool results.
