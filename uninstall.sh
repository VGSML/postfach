#!/usr/bin/env bash
# postfach uninstaller.
#
# Default: remove the MCP registrations (Claude Code and ChatGPT/Codex) and
# the generated configuration (.env, .codex-plugin/mcp.json). Downloaded
# artifacts and your data are kept unless asked for explicitly:
#
#   ./uninstall.sh                interactive
#   ./uninstall.sh --yes          non-interactive
#   ./uninstall.sh --artifacts    also delete models/, third_party/ and the binary
#   ./uninstall.sh --purge-data   ALSO DELETE the documents/registry folder
#                                 (saved invoices, registry-*.jsonl, Register.xlsx)
#
# Installed as a Claude Code plugin? Additionally run: /plugin uninstall postfach@hugr-lab
set -euo pipefail

cd "$(dirname "$0")"

YES=0 ARTIFACTS=0 PURGE=0
for arg in "$@"; do
  case "$arg" in
    --yes) YES=1 ;;
    --artifacts) ARTIFACTS=1 ;;
    --purge-data) PURGE=1 ;;
    --help|-h) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown flag: $arg (see --help)"; exit 1 ;;
  esac
done

say()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*"; }

# Remember the data folder before we delete .env.
DATA_DIR=""
if [ -f .env ]; then
  DATA_DIR=$(sed -n "s/^POSTFACH_ATTACHMENTS_DIR='\(.*\)'$/\1/p" .env)
fi

# --- registrations ---------------------------------------------------------
if command -v claude >/dev/null; then
  if claude mcp remove postfach >/dev/null 2>&1; then
    say "removed the Claude Code registration (claude mcp remove postfach)"
  else
    say "no Claude Code registration found"
  fi
fi
if command -v codex >/dev/null; then
  if codex mcp remove postfach >/dev/null 2>&1; then
    say "removed the ChatGPT/Codex registration (codex mcp remove postfach)"
  else
    say "no ChatGPT/Codex registration found"
  fi
fi

# --- generated configuration ----------------------------------------------
removed=""
for f in .env .codex-plugin/mcp.json; do
  if [ -f "$f" ]; then
    rm -f "$f"
    removed="$removed $f"
  fi
done
[ -n "$removed" ] && say "removed generated config:$removed"

# --- downloaded artifacts ---------------------------------------------------
if [ "$ARTIFACTS" = 1 ]; then
  rm -rf models third_party postfach-mcp
  say "removed downloaded artifacts (models/, third_party/, postfach-mcp)"
else
  say "kept models/, third_party/ and the binary (delete with --artifacts)"
fi

# --- user data (documents + registries) ------------------------------------
if [ "$PURGE" = 1 ]; then
  if [ -z "$DATA_DIR" ]; then
    warn "--purge-data: data folder unknown (.env was already gone); delete it manually"
  elif [ ! -d "$DATA_DIR" ]; then
    say "data folder $DATA_DIR does not exist"
  else
    warn "about to DELETE $DATA_DIR — saved documents, registry journals and Register.xlsx"
    if [ "$YES" != 1 ]; then
      read -r -p "Type the folder path to confirm: " CONFIRM
      [ "$CONFIRM" = "$DATA_DIR" ] || { warn "confirmation mismatch; data kept"; exit 1; }
    fi
    rm -rf "$DATA_DIR"
    say "deleted $DATA_DIR"
  fi
elif [ -n "$DATA_DIR" ]; then
  say "kept your data in $DATA_DIR (documents, registries; delete with --purge-data)"
fi

say "done. If postfach was installed as a plugin, also run:"
cat <<'EOF'
  Claude Code:    /plugin uninstall postfach@hugr-lab
  ChatGPT/Codex:  remove the plugin/local marketplace in developer mode
EOF
