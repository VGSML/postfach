# postfach

MCP server exposing email tools: list and read messages over IMAP, save
attachments — with prompt-injection screening of all mailbox-derived text.

PoC stage: local stdio transport. The end goal is a remote MCP service.

## Configuration (env)

| Variable | Required | Description |
|---|---|---|
| `POSTFACH_IMAP_URL` | yes | `imaps://user:password@imap.example.com:993` (`imap://` uses STARTTLS on 143). URL-escape special characters in the password. |
| `POSTFACH_ATTACHMENTS_DIR` | no | Where `save_attachment` writes files. Default `./attachments`. |

## Build & run

```sh
go build ./cmd/postfach-mcp
POSTFACH_IMAP_URL='imaps://user:pass@imap.example.com:993' ./postfach-mcp
```

Add to Claude Code:

```sh
claude mcp add postfach \
  -e POSTFACH_IMAP_URL='imaps://user:pass@imap.example.com:993' \
  -e POSTFACH_ATTACHMENTS_DIR="$HOME/Downloads/postfach" \
  -- /path/to/postfach-mcp
```

## Tools

- `list_messages(mailbox="INBOX", limit=20, unseen_only=false)` — newest messages first: uid, date, from, subject, flags, size.
- `read_message(uid, mailbox="INBOX")` — headers, text body (text/plain, or text/html converted), attachment list.
- `save_attachment(uid, attachment_index, mailbox="INBOX")` — saves one attachment under `POSTFACH_ATTACHMENTS_DIR` and returns the path.

## Security model

Email is untrusted input. Every string coming from the mailbox (subjects,
bodies, sender names, attachment filenames) is screened before it enters a
tool result:

- zero-width/bidi control characters are always stripped;
- heuristic rules (EN/RU/DE injection phrasings, system-prompt markers,
  exfiltration patterns) flag suspicious content;
- flagged text is **redacted by default**; pass `include_flagged_content=true`
  to read it anyway — the result then carries a `screening` verdict so the
  client can treat it accordingly;
- attachment filenames are sanitized against path traversal; flagged names
  are replaced with generated ones; files are written with mode 0600 and
  never interpreted.

Next layer (planned): local classifier — Llama Prompt Guard 2 86M via ONNX
Runtime with the CoreML execution provider (Apple GPU/ANE) — behind the same
`Screener` interface.
