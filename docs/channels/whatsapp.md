> Back to [Channels](../channels.md)

# WhatsApp (bridge mode)

This document covers **bridge mode** — the transport used when `use_native: false` (the default). Omnipus connects to an external WhatsApp bridge process over a persistent WebSocket connection (`BridgeURL`). The bridge is responsible for maintaining the WhatsApp session; Omnipus only sends and receives JSON-encoded message frames over that WebSocket.

For the in-process whatsmeow mode, see [docs/channels/whatsapp_native.md](whatsapp_native.md). Set `use_native: true` to select that mode instead.

## Configuration

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "bridge_url": "ws://localhost:8765",
      "use_native": false,
      "session_store_path": "",
      "allow_from": [],
      "reasoning_channel_id": "",
      "group_trigger": {
        "mention_only": false,
        "prefixes": []
      }
    }
  }
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Activate the WhatsApp channel. Must be `true`; the manager also checks `use_native` to select the factory. |
| `bridge_url` | string | Yes (bridge mode) | WebSocket URL of the external WhatsApp bridge, e.g. `ws://localhost:8765`. Required when `use_native: false`. |
| `use_native` | bool | No | Set to `false` (or omit) to use bridge mode. Set to `true` to use the whatsmeow in-process mode (see the native doc). |
| `session_store_path` | string | No | Not used by the bridge factory; applies only in native mode. |
| `allow_from` | []string | No | Allowlist of WhatsApp JIDs (sender phone numbers or group IDs). Empty means all senders are accepted. |
| `reasoning_channel_id` | string | No | Chat ID where extended reasoning traces are sent separately. |
| `group_trigger` | object | No | Controls when the bot responds in group chats. `mention_only: true` restricts to @-mentions; `prefixes` lists additional trigger strings. |

## Setup

1. Deploy a WhatsApp bridge that exposes a WebSocket API. The bridge must accept connections at `bridge_url`, authenticate sessions on its own, and exchange JSON frames in the format:
   - **Inbound** (bridge → Omnipus): `{"type":"message","from":"<jid>","chat":"<jid>","content":"<text>","id":"<id>","from_name":"<name>","media":[]}`
   - **Outbound** (Omnipus → bridge): `{"type":"message","to":"<jid>","content":"<text>"}`
2. Pair your WhatsApp account via the bridge's own pairing mechanism (QR code or linking code — bridge-specific).
3. Set `bridge_url` in `config.json` to the WebSocket URL of the running bridge.
4. Set `enabled: true` and `use_native: false`.

## Notes

- **Capabilities.** The bridge-mode channel does not implement `TypingCapable` or any other optional capability interface. It sends and receives plain text only.
- **Reconnect behavior.** On a read error the channel backs off linearly (2 s per attempt) up to a maximum of 10 consecutive failures, then exits the listen loop. Restart the gateway or the bridge to re-establish the connection after the cap is hit.
- **No credential ref.** Bridge mode does not use the Omnipus credential store — authentication to the bridge is handled entirely within the bridge process. `credentials.SecretBundle` is accepted by the factory but not read.
- **Message length.** Outbound messages are capped at 65 536 characters by the base channel layer. WhatsApp itself imposes its own limits on the bridge side.
- **Group chat trigger.** Group trigger and `allow_from` filtering applies to inbound messages before they reach the agent. The bridge must populate the `chat` field with the group JID and `from` with the sender JID for group routing to work correctly.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
