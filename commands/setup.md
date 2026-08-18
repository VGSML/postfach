---
description: Install and configure the postfach mail MCP server (build, models, mailbox, screening, registration)
---

Set up the postfach MCP server from this plugin's directory
(`${CLAUDE_PLUGIN_ROOT}`).

1. Ask the user (AskUserQuestion / plain questions) for:
   - IMAP URL (`imaps://user%40domain:password@host:993`) — remind them to
     URL-escape special characters in the login and password;
   - documents folder (default `~/Downloads/postfach`; recommend a folder
     synced with Google Drive so registries are viewable in Sheets);
   - whether a local guard LLM runtime is available (LM Studio/Ollama) —
     if unsure, the installer auto-detects;
   - Prompt Guard 2 variant: 86m multilingual (default) or 22m
     English-only.
2. Run the installer non-interactively with the collected values:
   `cd ${CLAUDE_PLUGIN_ROOT} && POSTFACH_IMAP_URL='...' POSTFACH_ATTACHMENTS_DIR='...' ./install.sh --yes`
   (add `--skip-model` if the user declined the Prompt Guard 2 download).
3. The installer builds the binary, downloads models, writes `.env`,
   smoke-tests the server and registers it with Claude Code. Relay any
   warnings (e.g. wide language allowlist without a guard LLM) verbatim.
4. Tell the user to reconnect via `/mcp` and suggest the first run:
   "разбери почту" — the `postfach-mail` skill guides the workflow.
