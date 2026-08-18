# postfach

MCP server exposing email tools: list and read messages over IMAP, save
attachments — with prompt-injection screening of all mailbox-derived text.

PoC stage: local stdio transport. The end goal is a remote MCP service.

## Configuration (env)

| Variable | Required | Description |
|---|---|---|
| `POSTFACH_IMAP_URL` | yes | `imaps://user:password@imap.example.com:993` (`imap://` uses STARTTLS on 143). URL-escape special characters in the password. |
| `POSTFACH_ATTACHMENTS_DIR` | no | Where `save_attachment` writes files. Default `./attachments`. |
| `POSTFACH_MAX_INLINE_MB` | no | Size cap for `read_attachment`, default 5. |
| `POSTFACH_PG2_MODEL` | no | Path to Prompt Guard 2 `model.quant.onnx`; enables the classifier (needs a `make build-guard` binary). |
| `POSTFACH_PG2_TOKENIZER` | no | Path to `tokenizer.json`, default: next to the model. |
| `POSTFACH_PG2_THRESHOLD` | no | Malicious-score threshold, default 0.5. |
| `POSTFACH_PG2_COREML` | no | `1` to try the CoreML execution provider (default CPU; see below). |
| `POSTFACH_ORT_LIB` | no | Path to `libonnxruntime.dylib`, default: `third_party/`, then Homebrew paths. |

## Build & run

```sh
go build ./cmd/postfach-mcp        # heuristics-only screening
POSTFACH_IMAP_URL='imaps://user:pass@imap.example.com:993' ./postfach-mcp
```

With the Prompt Guard 2 classifier (macOS arm64):

```sh
make deps-guard    # downloads libtokenizers.a and onnxruntime into third_party/
make fetch-model   # downloads model.quant.onnx (~280 MB) + tokenizer.json into models/pg2-86m/
make build-guard   # go build -tags promptguard
POSTFACH_IMAP_URL='imaps://...' \
POSTFACH_PG2_MODEL="$PWD/models/pg2-86m/model.quant.onnx" \
POSTFACH_ORT_LIB="$PWD/third_party/onnxruntime/lib/libonnxruntime.dylib" \
./postfach-mcp
```

`make fetch-model PG2_VARIANT=22m` fetches the 22M variant (~70 MB, ~2×
faster) instead — English-only: it does not detect German or Russian
injections (measured DE score 0.003 vs 0.991 on 86M), so keep 86M for
multilingual mailboxes.

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
- `read_attachment(uid, attachment_index, mailbox="INBOX")` — returns the attachment inline with its MIME type: text/XML (incl. e-invoice XML) as screened text, images as image content, PDFs and other binaries as base64 blobs. Capped by `POSTFACH_MAX_INLINE_MB`.
- `save_attachment(uid, attachment_index, mailbox="INBOX")` — saves one attachment under `POSTFACH_ATTACHMENTS_DIR` and returns the path.

Planned for the invoice-mailbox use case: `get_attached_erechnung` — parse
e-Rechnung XML (XRechnung UBL/CII, ZUGFeRD/Factur-X embedded in PDF/A-3) on
the Go side into structured JSON instead of dumping raw XML into context.

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

The second layer is a local classifier: **Llama Prompt Guard 2 86M**
(int8 ONNX) via ONNX Runtime, enabled by `POSTFACH_PG2_MODEL` in a
`make build-guard` binary. Because the classifier scores a whole window,
a short injection dissolves in benign text (measured: 0.99 alone, 0.87 with
~50 surrounding benign tokens, 0.04 with ~100), so texts are scanned at two
scales: coarse 510-token windows for wholesale jailbreak content and fine
64-token windows (stride 32) for embedded injections; the maximum score
decides. ~2.3 s for a 2400-token email on an Apple M-series CPU.

CoreML/Apple GPU is opt-in (`POSTFACH_PG2_COREML=1`) and currently not
recommended: this ONNX export has unbounded dimensions, which the CoreML
compiler rejects per node and re-attempts per input shape — CPU is faster.
A fixed-shape re-export is the path to real GPU/ANE use.
