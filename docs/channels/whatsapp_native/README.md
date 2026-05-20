> Back to [README](../../../README.md)

# WhatsApp Native (whatsmeow)

This document covers **native mode** — the transport used when `use_native: true`. Omnipus connects directly to WhatsApp servers using the [whatsmeow](https://github.com/tulir/whatsmeow) library in-process. No external bridge process is required. Session state is stored in a local SQLite database via `modernc.org/sqlite` (pure Go, no CGo).

For the external bridge mode, see [docs/channels/whatsapp/README.md](../whatsapp/README.md). Set `use_native: false` (or omit the field) to select that mode instead.

## Build requirement

Native mode requires the `whatsapp_native` build tag:

```bash
go build -tags whatsapp_native ./cmd/...
```

Without the tag the `NewWhatsAppNativeChannel` constructor returns an error at startup. The default binary does not include this tag. `modernc.org/sqlite` is pure Go and does not require CGo, so `CGO_ENABLED=0` builds are supported.

## Configuration

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "use_native": true,
      "session_store_path": "",
      "allow_from": [],
      "reasoning_channel_id": "",
      "group_trigger": {
        "mention_only": false,
        "prefixes": []
      },
      "bridge_url": ""
    }
  }
}
```

Both bridge mode and native mode share the `WhatsAppConfig` struct. Fields relevant to native mode are listed below.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Activate the WhatsApp channel. The manager also checks `use_native` to select the factory. |
| `use_native` | bool | Yes | Must be `true` to select the whatsmeow factory. |
| `session_store_path` | string | No | Directory for the SQLite session database (`store.db`). Defaults to `<workspace>/whatsapp` when empty (typically `~/.omnipus/workspace/whatsapp`). |
| `allow_from` | []string | No | Allowlist of WhatsApp JIDs (sender phone numbers or group IDs). Empty means all senders are accepted. |
| `reasoning_channel_id` | string | No | Chat ID where extended reasoning traces are sent separately. |
| `group_trigger` | object | No | Controls when the bot responds in group chats. `mention_only: true` restricts to @-mentions (reads `ContextInfo.MentionedJID`); `prefixes` lists additional trigger strings. |
| `bridge_url` | string | No | Unused in native mode; leave empty. |

## Setup

1. Build Omnipus with the `whatsapp_native` tag (see Build requirement above).
2. Start the gateway. On first run, no session exists and the bot prints a QR code to stdout in half-block Unicode form:
   ```
   Starting WhatsApp native channel (whatsmeow)
   Scan this QR code with WhatsApp (Linked Devices):
   <QR code>
   ```
   The QR code data is also emitted as a structured log entry with `"event": "whatsapp.qr_code"` for programmatic consumption.
3. Open WhatsApp on your phone → **Linked Devices** → **Link a Device** and scan the QR code.
4. Once paired, the session is persisted in `store.db` under `session_store_path`. Subsequent gateway starts connect without re-pairing.
5. If the session is logged out by the server (e.g. from the phone), the channel automatically clears the stored session, reconnects, and emits a fresh QR code for re-pairing.

## Notes

- **Capabilities.** The native channel implements `TypingCapable` using `whatsmeow`'s `SendChatPresence`. It sends `composing` immediately on `StartTyping`, refreshes the indicator every 10 seconds, and sends `paused` when the stop function is called. The maximum typing duration is 5 minutes.
- **Session storage.** The SQLite database is opened with `_foreign_keys=on` and `max_open_conns=1`. The store is created at `session_store_path/store.db`. Back this file up to preserve the paired session across reinstalls.
- **Reconnect behavior.** On a `Disconnected` event the channel enters an exponential-backoff reconnect loop (initial: 5 s, multiplier: 2×, ceiling: 5 min). On `LoggedOut` it clears the session and re-enters the QR pairing flow. Both reconnect paths are tracked by a `sync.WaitGroup` and respect the lifecycle context so `Stop()` exits cleanly without goroutine leaks.
- **Group message filtering.** In WhatsApp groups (JID server = `g.us`) the bot checks whether its own JID appears in `ContextInfo.MentionedJID` to detect @-mentions. The `group_trigger` config then decides whether to respond.
- **Chat ID format.** For outbound messages the `chat_id` field accepts either a bare phone number (e.g. `15551234567`) or a full JID (e.g. `15551234567@s.whatsapp.net`). For groups, use the group JID (e.g. `12345678-1234567890@g.us`).
- **No credential ref.** Session credentials are managed entirely by whatsmeow in `store.db`. The Omnipus credential store is not used for WhatsApp authentication.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../../pkg/channels/README.md).
