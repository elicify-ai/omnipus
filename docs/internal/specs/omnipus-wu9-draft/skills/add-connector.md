---
name: add-connector
description: Use when the user wants to connect an MCP server (connector) for agents (W10).
metadata:
  display_name: Add Connector (W10)
---
# Add a connector (MCP) (W10)

## Prerequisites
`tool:list_mcp_servers` for what is already connected; `dependency-check` when the server needs a runtime (Node, Python — from the server's own guidance, never invented); secrets only through `tool:request_secret`.

## Steps
1. Ask which server or app to connect, and which agents get it.
2. Need credentials, have the user enter them on the secret card — the value never appears in chat or tool arguments; you receive reference names.
3. Load `dependency-check` for the runtime the server needs and list the install in the summary.
4. Present one card: server name, that connector tools leave the sandbox for their server's scope, which agents get it, the exact launch command, needed installs.
5. One Go / Change / Cancel question. Never call a change tool before Go.
6. After Go: `tool:add_mcp_server` with references — its live readback returns status and tool count — then assign the server to the named agents.

## Expected output
The connector's own check: working (with tool count) or saved but not working: reason — and the assignment outcome. A failed connect offers fix or remove; it is never reported as done.

## Stop and handoff
Cancel stores nothing; a submitted secret stays stored, unused, listed and removable. An install is reported "installed; readiness confirmed at the agent's first use" unless proven in a consuming agent's sandbox.
