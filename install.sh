#!/usr/bin/env bash
# postfach installer: collects configuration (mailbox, folders, screening
# models, languages), builds the MCP server, downloads model/native deps,
# smoke-tests it and (optionally) registers it with Claude Code.
#
# Usage:
#   ./install.sh                 interactive
#   ./install.sh --yes           non-interactive (config from env)
#   ./install.sh --no-register   skip `claude mcp add`
#   ./install.sh --skip-model    no Prompt Guard 2 (heuristics + guard LLM only)
#
# Non-interactive config via env: POSTFACH_IMAP_URL (required); optional:
# POSTFACH_ATTACHMENTS_DIR, POSTFACH_ALLOWED_LANGS, POSTFACH_GUARD_LLM_MODEL,
# POSTFACH_GUARD_LLM_URL, POSTFACH_MAX_INLINE_MB, POSTFACH_DOC_LINK_TEMPLATE,
# PG2_VARIANT (86m|22m).
set -euo pipefail

cd "$(dirname "$0")"
ROOT="$(pwd)"

YES=0 REGISTER=1 SKIP_MODEL=0
for arg in "$@"; do
  case "$arg" in
    --yes) YES=1 ;;
    --no-register) REGISTER=0 ;;
    --skip-model) SKIP_MODEL=1 ;;
    --help|-h) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown flag: $arg (see --help)"; exit 1 ;;
  esac
done

say()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

ask() { # ask VAR "prompt" "default"
  local var="$1" prompt="$2" def="${3-}"
  local cur="${!var-}"
  if [ -n "$cur" ]; then return 0; fi
  if [ "$YES" = 1 ]; then
    printf -v "$var" '%s' "$def"
    [ -n "${!var}" ] || fail "$var not set (non-interactive mode)"
    return 0
  fi
  local answer
  read -r -p "$prompt${def:+ [$def]}: " answer
  printf -v "$var" '%s' "${answer:-$def}"
}

# --- prerequisites -----------------------------------------------------------
[ "$(uname -s)" = "Darwin" ] && [ "$(uname -m)" = "arm64" ] \
  || fail "postfach is currently distributed for macOS on Apple Silicon only"
command -v curl >/dev/null || fail "curl is required"

# Release packages ship a prebuilt binary (no Go needed); a repo checkout
# (go.mod present) builds from source.
PREBUILT=0
if [ -x ./postfach-mcp ] && [ ! -f go.mod ]; then
  PREBUILT=1
elif [ -f go.mod ]; then
  command -v go   >/dev/null || fail "Go is required to build from source (brew install go)"
  command -v make >/dev/null || fail "make is required (xcode-select --install)"
else
  fail "neither a prebuilt ./postfach-mcp nor go.mod found — broken package?"
fi

# Keep in sync with the Makefile (ORT_VERSION).
ORT_VERSION=1.29.0
HF_BASE="https://huggingface.co/gravitee-io"

fetch_model() { # fetch_model <variant>
  local variant="$1" dir="models/pg2-$1"
  local repo="$HF_BASE/Llama-Prompt-Guard-2-$(printf '%s' "$variant" | tr a-z A-Z)-onnx/resolve/main"
  mkdir -p "$dir"
  [ -f "$dir/model.quant.onnx" ] || { say "downloading Prompt Guard 2 $variant model"; curl -fSL -o "$dir/model.quant.onnx" "$repo/model.quant.onnx"; }
  [ -f "$dir/tokenizer.json" ]  || curl -fsSL -o "$dir/tokenizer.json" "$repo/tokenizer.json"
}

fetch_ort() {
  [ -e third_party/onnxruntime/lib ] && return 0
  say "downloading ONNX Runtime $ORT_VERSION"
  rm -rf third_party/ort-tmp && mkdir -p third_party/ort-tmp
  curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v$ORT_VERSION/onnxruntime-osx-arm64-$ORT_VERSION.tgz" \
    | tar -xz -C third_party/ort-tmp
  mv "$(find third_party/ort-tmp -maxdepth 2 -type d -name 'onnxruntime-osx-*' | head -1)" third_party/onnxruntime
  rm -rf third_party/ort-tmp
}

# --- configuration -----------------------------------------------------------
say "mailbox"
ask POSTFACH_IMAP_URL "IMAP URL (imaps://user%40domain:password@imap.example.com:993)"
case "$POSTFACH_IMAP_URL" in
  imaps://*|imap://*) ;;
  *) fail "POSTFACH_IMAP_URL must start with imaps:// (or imap:// for STARTTLS)" ;;
esac

say "folders"
ask POSTFACH_ATTACHMENTS_DIR "Documents/registry folder (sync it with Google Drive for Sheets access)" "$HOME/Downloads/postfach"
mkdir -p "$POSTFACH_ATTACHMENTS_DIR"
ask POSTFACH_MAX_INLINE_MB "Max attachment size returned inline, MB" "5"
ask POSTFACH_DOC_LINK_TEMPLATE "Document link template ({filename} is substituted)" \
  'https://drive.google.com/drive/search?q=%22{filename}%22'

say "screening"
# Prompt Guard 2 classifier variant.
if [ "$SKIP_MODEL" = 0 ]; then
  ask PG2_VARIANT "Prompt Guard 2 variant: 86m (multilingual) or 22m (English-only, 4x smaller)" "86m"
  case "$PG2_VARIANT" in
    86m) ;;
    22m) warn "22m is blind to non-English injections (measured DE score 0.003) — only for English-only mailboxes" ;;
    *) fail "PG2_VARIANT must be 86m or 22m" ;;
  esac
fi

# Guard LLM: suggest whatever local runtime answers (LM Studio, then Ollama).
GUARD_URL_DEFAULT=""
if curl -sf --max-time 2 http://localhost:1234/v1/models >/dev/null 2>&1; then
  GUARD_URL_DEFAULT="http://localhost:1234/v1"
elif curl -sf --max-time 2 http://localhost:11434/v1/models >/dev/null 2>&1; then
  GUARD_URL_DEFAULT="http://localhost:11434/v1"
fi
if [ -n "$GUARD_URL_DEFAULT" ]; then
  say "local LLM runtime detected at $GUARD_URL_DEFAULT"
  ask POSTFACH_GUARD_LLM_MODEL "Guard LLM model id ('' disables; EU-wide language coverage needs it)" "qwen3guard-gen-0.6b"
  POSTFACH_GUARD_LLM_URL="${POSTFACH_GUARD_LLM_URL:-$GUARD_URL_DEFAULT}"
else
  POSTFACH_GUARD_LLM_MODEL="${POSTFACH_GUARD_LLM_MODEL-}"
  [ -n "$POSTFACH_GUARD_LLM_MODEL" ] || warn "no LM Studio/Ollama detected — guard LLM disabled, language allowlist stays narrow"
fi

# Language allowlist: what the configured stack can actually vet.
if [ -n "${POSTFACH_GUARD_LLM_MODEL-}" ]; then
  DEFAULT_LANGS="en,de,fr,it,es,pt,nl,pl,cs,sk,da,sv,no,fi,hu,ro,bg,el,hr,sl,lt,lv,et,ru"
else
  DEFAULT_LANGS="en,de,fr,it,es,ru"   # PG2-86m's measured-reliable set
fi
ask POSTFACH_ALLOWED_LANGS "Language allowlist (emails in other languages are quarantined)" "$DEFAULT_LANGS"
if [ -z "${POSTFACH_GUARD_LLM_MODEL-}" ] && [ "$(printf '%s' "$POSTFACH_ALLOWED_LANGS" | tr -cd , | wc -c)" -gt 6 ]; then
  warn "wide language list without a guard LLM: injections in languages beyond en/de/fr/it/es/ru are NOT reliably screened"
fi

# --- build / prebuilt ----------------------------------------------------
if [ "$PREBUILT" = 1 ]; then
  say "using prebuilt postfach-mcp (release package)"
  # Downloads via curl carry no quarantine attribute; clear it just in case
  # the package was fetched through a browser.
  xattr -dr com.apple.quarantine . 2>/dev/null || true
  if [ "$SKIP_MODEL" = 0 ]; then
    fetch_ort
    fetch_model "$PG2_VARIANT"
  fi
elif [ "$SKIP_MODEL" = 1 ]; then
  say "building postfach-mcp (default build, no Prompt Guard 2)"
  GOWORK=off go build -o postfach-mcp ./cmd/postfach-mcp
else
  say "fetching native libs and the Prompt Guard 2 model (one-time download)"
  make deps-guard
  fetch_model "$PG2_VARIANT"
  say "building postfach-mcp (with Prompt Guard 2)"
  make build-guard
fi

# --- env assembly --------------------------------------------------------
ENV_KV=(
  "POSTFACH_IMAP_URL=$POSTFACH_IMAP_URL"
  "POSTFACH_ATTACHMENTS_DIR=$POSTFACH_ATTACHMENTS_DIR"
  "POSTFACH_ALLOWED_LANGS=$POSTFACH_ALLOWED_LANGS"
  "POSTFACH_MAX_INLINE_MB=$POSTFACH_MAX_INLINE_MB"
  "POSTFACH_DOC_LINK_TEMPLATE=$POSTFACH_DOC_LINK_TEMPLATE"
)
if [ "$SKIP_MODEL" = 0 ]; then
  ENV_KV+=( "POSTFACH_PG2_MODEL=$ROOT/models/pg2-$PG2_VARIANT/model.quant.onnx"
            "POSTFACH_ORT_LIB=$ROOT/third_party/onnxruntime/lib/libonnxruntime.dylib" )
fi
if [ -n "${POSTFACH_GUARD_LLM_MODEL-}" ]; then
  ENV_KV+=( "POSTFACH_GUARD_LLM_MODEL=$POSTFACH_GUARD_LLM_MODEL"
            "POSTFACH_GUARD_LLM_URL=${POSTFACH_GUARD_LLM_URL:-http://localhost:1234/v1}" )
fi

: > .env
for kv in "${ENV_KV[@]}"; do printf "%s='%s'\n" "${kv%%=*}" "${kv#*=}" >> .env; done
say "wrote .env (gitignored) for manual runs: set -a; source .env; ./postfach-mcp"

# ChatGPT/Codex plugin: generate the local stdio MCP config the manifest
# (.codex-plugin/plugin.json) points at — absolute paths, full env.
if command -v python3 >/dev/null; then
  printf '%s\0' "${ENV_KV[@]}" | python3 -c '
import json, sys
pairs = [s for s in sys.stdin.buffer.read().decode().split("\0") if s]
env = dict(s.split("=", 1) for s in pairs)
print(json.dumps({"postfach": {"command": sys.argv[1], "env": env}}, indent=2))
' "$ROOT/postfach-mcp" > .codex-plugin/mcp.json
  say "wrote .codex-plugin/mcp.json (gitignored) — add this directory as a local marketplace in ChatGPT/Codex developer mode"
else
  warn "python3 not found; skipped .codex-plugin/mcp.json (see .codex-plugin/mcp.json.example)"
fi

# --- smoke test ----------------------------------------------------------
say "smoke test: starting the server and listing tools"
SMOKE=$(printf '%s\n%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"install","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | env "${ENV_KV[@]}" ./postfach-mcp 2>/tmp/postfach-install-smoke.log) \
  || { cat /tmp/postfach-install-smoke.log >&2; fail "server failed to start (log above)"; }
echo "$SMOKE" | grep -q '"list_messages"' || fail "tools/list did not include list_messages"
TOOLS=$(echo "$SMOKE" | grep -o '"name":"[a-z_]*"' | sort -u | wc -l | tr -d ' ')
say "server OK: $TOOLS tools registered"
grep -E 'language allowlist|prompt guard|guard LLM' /tmp/postfach-install-smoke.log | sed 's/^/    /' || true

# --- register --------------------------------------------------------------
confirm() { # confirm "question"
  if [ "$YES" = 1 ]; then return 0; fi
  local reply
  read -r -p "$1 [y/N] " reply
  [ "$reply" = "y" ] || [ "$reply" = "Y" ]
}

# Claude Code (user scope: visible in every Claude Code session) + plugin
# (the postfach-mail skill and /postfach:* commands).
if [ "$REGISTER" = 1 ] && command -v claude >/dev/null; then
  if confirm "Register with Claude Code (all projects) and install the skill plugin?"; then
    ENV_ARGS=(); for kv in "${ENV_KV[@]}"; do ENV_ARGS+=( -e "$kv" ); done
    claude mcp remove -s user postfach >/dev/null 2>&1 || true
    claude mcp remove -s local postfach >/dev/null 2>&1 || true
    claude mcp add --scope user postfach "${ENV_ARGS[@]}" -- "$ROOT/postfach-mcp"
    say "registered with Claude Code (user scope)"
    if claude plugin marketplace add "$ROOT" >/dev/null 2>&1 || claude plugin marketplace update hugr-lab >/dev/null 2>&1; then
      if claude plugin install postfach@hugr-lab >/dev/null 2>&1; then
        say "installed the postfach plugin (postfach-mail skill, /postfach:setup, /postfach:remove)"
      else
        warn "plugin install failed; skill unavailable (try /plugin install postfach@hugr-lab in a session)"
      fi
    else
      warn "could not add the local plugin marketplace; skill unavailable"
    fi
  fi
fi

# Claude Desktop (separate config file; restart the app afterwards).
DESKTOP_CFG="$HOME/Library/Application Support/Claude/claude_desktop_config.json"
if [ "$REGISTER" = 1 ] && [ -e "$(dirname "$DESKTOP_CFG")" ] && command -v python3 >/dev/null; then
  if confirm "Register with Claude Desktop ($DESKTOP_CFG)?"; then
    printf '%s\0' "${ENV_KV[@]}" | python3 -c '
import json, os, sys
path = sys.argv[1]
pairs = [s for s in sys.stdin.buffer.read().decode().split("\0") if s]
env = dict(s.split("=", 1) for s in pairs)
cfg = {}
if os.path.exists(path):
    with open(path) as f:
        cfg = json.load(f)
cfg.setdefault("mcpServers", {})["postfach"] = {"command": sys.argv[2], "env": env}
with open(path, "w") as f:
    json.dump(cfg, f, indent=2)
' "$DESKTOP_CFG" "$ROOT/postfach-mcp"
    say "registered with Claude Desktop — restart the Claude app to pick it up"
  fi
fi

# ChatGPT/Codex (global config shared by the desktop app, CLI and IDE).
if [ "$REGISTER" = 1 ] && command -v codex >/dev/null; then
  if confirm "Register with ChatGPT/Codex (codex mcp add postfach)?"; then
    ENV_ARGS=(); for kv in "${ENV_KV[@]}"; do ENV_ARGS+=( --env "$kv" ); done
    codex mcp remove postfach >/dev/null 2>&1 || true
    codex mcp add postfach "${ENV_ARGS[@]}" -- "$ROOT/postfach-mcp"
    say "registered with ChatGPT/Codex — restart the ChatGPT app (developer mode must be enabled)"
  fi
fi

say "done. Next steps:"
cat <<EOF
  1. Sync '$POSTFACH_ATTACHMENTS_DIR' with Google Drive (Drive for Desktop):
     documents and Register.xlsx become viewable in Google Sheets.
  2. In Claude Code: /mcp -> postfach; the postfach plugin skill teaches the
     model the processing workflow ("разбери почту").
  3. Optional: connect the Google Drive connector in Claude for direct
     document links in the registries.
EOF
