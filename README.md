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
| `POSTFACH_ALLOWED_LANGS` | no | Language allowlist for the screening gate, default `en,de,fr,it,es,ru`. Comma-separated ISO codes; `any` disables the gate. |
| `POSTFACH_GUARD_LLM_MODEL` | no | Model id of a guard LLM served over an OpenAI-compatible API (enables the multilingual screener), e.g. `qwen3guard-gen-0.6b`. |
| `POSTFACH_GUARD_LLM_URL` | no | Base URL of that API. Default `http://localhost:1234/v1` (LM Studio); Ollama is `http://localhost:11434/v1`. |
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

All reads are strictly non-destructive: mailboxes are opened read-only and
messages are never marked as seen.

- `list_messages(mailbox="INBOX", limit=20, unseen_only=false, since_uid=0, since_date="")` — newest messages first: uid, dates (envelope + server internal), from, subject, flags, size, plus the mailbox `uid_validity`. `since_uid` + `uid_validity` form the incremental-sync cursor for ingestion loops; `since_date` filters by server receive time.
- `read_message(uid, mailbox="INBOX", body_offset=0, body_limit=4000)` — headers, one page of the text body (character-based pagination with `body_total_chars` / `body_next_offset`), and the attachment list with per-attachment `sha256`. The full body is always screened regardless of the requested page.
- `read_attachment(uid, attachment_index, mailbox="INBOX", offset=0, limit=4000)` — returns the attachment inline with its MIME type and `sha256`: text/XML (incl. e-invoice XML) as screened, paginated text; images as image content; PDFs and other binaries as base64 blobs. Capped by `POSTFACH_MAX_INLINE_MB`.
- `read_quarantined(uid, attachment_index=-1, mailbox="INBOX", offset=0, limit=4000)` — the escape hatch for blocked content (e.g. language outside the allowlist): returns the body or a text attachment in **defused** form — whitespace replaced with `ˆ` markers, long unbroken runs (CJK) split, every line prefixed with `❯` (spotlighting/datamarking), so embedded instructions read as data. The screening verdict is always attached.
- `save_attachment(uid, attachment_index, mailbox="INBOX")` — saves one attachment under `POSTFACH_ATTACHMENTS_DIR`, appends a record (uid, filename, path, `sha256`, sender, screening verdict) to `registry.jsonl` there, and deduplicates by hash: identical content is not written twice.

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
- a **language allowlist gate** (`POSTFACH_ALLOWED_LANGS`) flags text the
  screening stack cannot vet: letters from foreign scripts (e.g. a Chinese
  fragment inside a German email — a few stray glyphs are tolerated) and,
  for longer texts, statistically detected disallowed languages. An
  injection in a language the classifier was not trained on must not be
  trusted just because the classifier is silent;

  Measured PG2-86M (quantized) injection detection per language — the
  default allowlist is the reliable set:

  | Reliable (in default) | Inconsistent (opt-in with care) |
  |---|---|
  | en 0.99, de 0.99, fr 0.90–0.99, it 0.99, es 0.99, ru 0.96, (cs 0.98–0.99*) | pt 0.05–0.98, pl 0.47–0.99, nl 0.002–0.99 |

  \* cs measured reliable but not officially trained; add it via env if
  needed. Officially trained set: en, fr, de, hi, it, pt, es, th.

### Full-EU coverage with a guard LLM

For languages beyond PG2's reliable set (Scandinavia, Eastern Europe, the
Baltics), a second screener runs a local guard LLM over an OpenAI-compatible
API — LM Studio or Ollama, same `Screener` interface, no extra build deps.
Measured with **Qwen3Guard-Gen 0.6B** (Q8 GGUF in LM Studio, ~70 ms/call):
injections in da, sv, no, fi, nl, pl, cs, hu, ro, bg, el, lt, lv, et, pt and
zh — **16/16 caught, 0 false positives** on benign invoices in 15 languages,
including the exact pt/nl phrasings PG2 misses. Scanning is two-scale
(coarse 1200-rune windows + sentence-packed ~200-rune blocks), which catches
tail and mid-text injections in long benign emails.

Setup (LM Studio): download `Qwen3Guard-Gen-0.6B.Q8_0.gguf` from
`QuantFactory/Qwen3Guard-Gen-0.6B-GGUF` into
`~/.lmstudio/models/QuantFactory/Qwen3Guard-Gen-0.6B-GGUF/`, then run with:

```sh
POSTFACH_GUARD_LLM_MODEL=qwen3guard-gen-0.6b \
POSTFACH_ALLOWED_LANGS=en,de,fr,it,es,pt,nl,pl,cs,sk,da,sv,no,fi,hu,ro,bg,el,hr,sl,lt,lv,et,ru \
./postfach-mcp
```

Widen `POSTFACH_ALLOWED_LANGS` only together with the guard LLM — the
allowlist must always match what the screening stack can actually vet.
- flagged text is **redacted by default**; the recommended way to inspect it
  is `read_quarantined` (defused form); `include_flagged_content=true`
  returns the raw text and should be a last resort;
- screening verdicts are **cached by content hash**
  (`screening_cache.jsonl` in the attachments dir), so a message is
  screened by the models once, no matter how often it is listed, read or
  paged through; the cache key includes the screener configuration;
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
