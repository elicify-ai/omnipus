---
name: mcp-install
metadata:
  display_name: MCP Install
description: Install and probe an MCP server through Admin without automatically granting any agent execution access.
---
# MCP Install
## Prerequisites
Know the server, transport, command or endpoint, and approved credential reference.
## Steps
1. Review that MCP runs outside the sandbox and confirm requested setup.
2. Call `tool:add_mcp_server` with secrets referenced through supported credential storage.
3. Use `tool:list_mcp_servers` to verify the controlled server connects.
4. State that no agent connector assignment was created automatically.
## Expected output
A connected server and probe result, or an explicit incomplete setup.
## Stop and handoff
Do not expose secrets. A failed connection is not installed successfully.
