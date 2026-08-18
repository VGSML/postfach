---
description: Uninstall the postfach MCP server (registrations, generated config; optionally artifacts and data)
---

Uninstall postfach from this machine using `${CLAUDE_PLUGIN_ROOT}/uninstall.sh`.

1. Ask the user what to remove:
   - registrations + generated config only (default),
   - also downloaded artifacts (`--artifacts`: models, native libs, binary),
   - also the documents/registry data folder (`--purge-data`) — warn that
     this deletes saved invoices, registry journals and Register.xlsx, and
     that a Drive-synced copy may survive in the cloud trash.
2. Run `cd ${CLAUDE_PLUGIN_ROOT} && ./uninstall.sh --yes [flags]` with the
   chosen flags. Never pass `--purge-data` without the user's explicit
   confirmation in this conversation.
3. Relay the script output. If postfach was installed as a plugin, remind
   the user to also run `/plugin uninstall postfach@hugr-lab`.
