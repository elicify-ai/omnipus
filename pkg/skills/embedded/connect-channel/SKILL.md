---
name: connect-channel
description: Use when the person asks to connect a channel platform to an agent's workspace; one call configures, binds, enables and live-tests, and the result always separates proven facts from not-tested ones.
metadata:
  display_name: Connect Channel (W13)
---
# Connect a channel (W13)

## Prerequisites
`tool:list_channels` for the instance keys (the bare type is the default instance, `type.suffix` an additional one — for example `telegram`, `telegram.eu`) and each instance's revision; `tool:list_workspaces` and `tool:get_workspace` to validate the target pair; credentials only through `tool:request_secret`. Landing 1 binds one main agent per connection in a workspace; per-person rules arrive in landing 2.

## Steps
1. Ask which channel type, then which agent and which workspace — the agent must already be a member of that workspace; a valid token with a non-member target fails and nothing is saved.
2. Read the instance's `expected_revision` from `tool:list_channels` (`absent` when it is not configured yet).
3. Present one card: instance key, platform, the bound agent and workspace, what the live test will and will not prove.
4. One Go / Change / Cancel question. Never call a change tool before Go.
5. After Go, save the instance's non-secret settings with `tool:configure_channel` (`id`, `expected_revision`, the settings — `"absent"` when the instance is not configured yet). On a fresh instance this provisional entry is the recipient the credential cards name; an instance that already exists skips this step unless its settings change.
6. Need credentials: `tool:request_secret` with purpose and the exact instance recipient (instance + field) per field; WhatsApp needs none — it pairs by QR. The person enters each value on the secret card; you pass only the returned references (`token_ref`, Slack's `bot_token_ref` + `app_token_ref`, …); a reference must match that exact instance and field, so a new recipient needs a new card. Never a plaintext value.
7. `tool:connect_channel` with `id`, `workspace_id`, `agent_id`, the instance's current `expected_revision` and the `<field>_ref` references — one call configures, binds to the agent in the workspace, enables and live-tests.

## Expected output
From the tool's own result, two lists: the facts proven by real platform calls (for example "the channel started after the reload with the saved configuration and credentials") and everything **not tested** (inbound message delivery and routing to the agent; outbound replies). "working" is claimed only when the channel is actually running; a channel that did not start is "saved but not working", with the tool result's cause and repair. Some start failures identify a local session-store problem (check the data directory's read/write access; re-entering credentials will not fix it) or an unreachable platform (check the network, then connect again). The fallback reports "the platform rejected the credentials or the start failed" (check stored credentials and the server log, then connect again); some status paths still report a combined credential-or-reachability reason. A validation failure before saving is "failed: nothing saved" — never "working". WhatsApp reports "pairing not finished: scan the WhatsApp QR code" with the connection working only after the person scans the QR — the code appears in this chat while pairing runs (and on Connectors > WhatsApp); a pairing timeout is "saved but not working".

## Stop and handoff
Stop in this chat ends the pairing with a "stopped" state; running connect_channel again starts a fresh pairing. Cancel after a submitted secret leaves it stored, unused, listed and removable. Never claim routing "working" from stored credentials or a saved configuration alone.
