---
name: connect-channel
description: Use when the user wants to connect a chat channel — Telegram, WhatsApp, Slack, Discord, Matrix and the like — for an agent in a workspace (W13).
metadata:
  display_name: Connect Channel (W13)
---
# Connect a channel (W13)

## Prerequisites
`tool:list_channels` for existing instances; `tool:list_workspaces` and `tool:get_workspace` to validate the target pair; credentials only through `tool:request_secret`. Landing 1 binds one main agent per connection in a workspace; per-person rules arrive in landing 2.

## Steps
1. Ask which channel type, then which agent and which workspace — the agent must be a member of that workspace; anything else is refused before saving.
2. Have the user enter the token or credentials on the secret card; WhatsApp pairs by QR — the code and progress render right here in this chat, correlated by the pairing request.
3. Present one card: platform, instance name, the bound agent and workspace, what the live test will and will not prove.
4. One Go / Change / Cancel question. Never call a change tool before Go.
5. After Go: `tool:connect_channel` — configure, bind to the agent in the workspace, enable and live-test in one call.

## Expected output
Two lists from the tool's result: the facts proven per platform (for example token authenticated, bot identity read, binding saved, inbound route reaches the bound agent) and what was **not tested** (for example webhook delivery from the platform). Routing is never reported "working" from stored credentials alone. A revoked token is "saved but not working: token rejected" or "failed: nothing saved"; a valid token with a non-member target agent is "failed: nothing saved"; a QR timeout is "saved but not working: pairing timed out".

## Stop and handoff
Stop during pairing ends it with "stopped"; a reconnect re-subscribes and shows current progress. Cancel after a submitted secret leaves it stored, unused, listed and removable.
