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
2. Have the person enter each credential field on the secret card (purpose and the exact instance recipient); WhatsApp needs none — it pairs by QR. You pass only the returned references (`token_ref`, Slack's `bot_token_ref` + `app_token_ref`, …); a reference must match that exact instance and field, so a new recipient needs a new card. Never a plaintext value.
3. Read the instance's `expected_revision` from `tool:list_channels` (`absent` when it is not configured yet).
4. Present one card: instance key, platform, the bound agent and workspace, what the live test will and will not prove.
5. One Go / Change / Cancel question. Never call a change tool before Go.
6. After Go: `tool:connect_channel` with `id`, `workspace_id`, `agent_id`, `expected_revision` and the `<field>_ref` references — one call configures, binds to the agent in the workspace, enables and live-tests.

## Expected output
From the tool's own result, two lists: the facts proven by real platform calls (for example "the channel started after the reload with the saved configuration and credentials") and everything **not tested** (inbound message delivery and routing to the agent; outbound replies). "working" is claimed only when the channel is actually running; a channel that did not start is "saved but not working: the channel did not start: the platform rejected the credentials or could not be reached" (the product does not tell a rejected credential from an unreachable platform), or "failed: nothing saved" when validation stopped the save — never "working". WhatsApp reports "pairing not finished: scan the WhatsApp QR code" with the connection working only after the person scans the code on the Connectors page (WhatsApp) — the QR does not render in the chat; a pairing timeout is "saved but not working".

## Stop and handoff
Stop ends a pairing attempt; the person can restart it from Connectors. Cancel after a submitted secret leaves it stored, unused, listed and removable. Never claim routing "working" from stored credentials or a saved configuration alone.
