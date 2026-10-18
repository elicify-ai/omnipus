---
name: remove-connector
description: Use when the user wants to disconnect an MCP server (W11).
metadata:
  display_name: Remove Connector (W11)
---
# Remove a connector (W11)

## Prerequisites
`tool:list_mcp_servers` for the exact server entry.

## Steps
1. List the servers and confirm which one to remove.
2. Present one card: the server, and every agent that loses its tools.
3. One Go / Change / Cancel question. Never call a change tool before Go.
4. After Go: `tool:remove_mcp_server` — it raises its own approval after Go; that is expected.

## Expected output
Removal outcome — working / saved but not working: reason / failed: nothing saved — naming the agents that lost access.

## Stop and handoff
Cancel removes nothing. Never name the wrong server; re-read the list if the user's naming is ambiguous.
