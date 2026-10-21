> Back to [Connectors](../connectors.md)

# WhatsApp connector (native, whatsmeow)

Omnipus connects to WhatsApp directly using the [whatsmeow](https://github.com/tulir/whatsmeow) library in-process. No external bridge process is required. Session state is stored in a local SQLite database via `modernc.org/sqlite` (pure Go, no CGo).

## Build requirement

**Native mode ships in the default build — no build tag needed.** A standard build (and every official release binary) includes the whatsmeow stack:

```bash
make build                # canonical build; native WhatsApp included
```

Build through the make targets — they inject the required `goolm,stdjson` build tags (a bare `go build ./cmd/...` fails with a "build constraints exclude all Go files" error).

`modernc.org/sqlite` is pure Go and does not require CGo, so `CGO_ENABLED=0` builds are supported. The whatsmeow + SQLite stack adds roughly 58 MB to the binary.

> **Unsupported architectures.** There is no separate lite build — native WhatsApp ships in every build on every supported platform (Linux, macOS, Windows). On a few unsupported targets — `mipsle`, `netbsd`, and `freebsd/arm` — `modernc.org/sqlite`, the SQLite driver behind the session store, cannot build, so those binaries get a stub instead and the WhatsApp channel fails to start. There, the WhatsApp **Configure** panel on the Connectors screen shows a note that linked-device pairing is unavailable on this server build instead of a QR code.

## Configuration

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
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
| `enabled` | bool | Yes | Activate the WhatsApp channel. |
| `session_store_path` | string | No | Directory for the SQLite session database (`store.db`). Defaults to `<workspace>/whatsapp` when empty (typically `~/.omnipus/workspace/whatsapp`). |
| `allow_from` | []string | No | Allowlist of WhatsApp JIDs (sender phone numbers or group IDs). Empty means all senders are accepted. |
| `reasoning_channel_id` | string | No | Chat ID where extended reasoning traces are sent separately. |
| `group_trigger` | object | No | Controls when the bot responds in group chats. `mention_only: true` restricts responses to @-mentions (reads `ContextInfo.MentionedJID`); `prefixes` lists additional trigger strings. |

## Setup

Native mode is included in the default build (see Build requirement above), so no special build step is needed — just run the gateway, then pair a device. There are two ways to scan the pairing QR — in the app (recommended) or from the gateway logs (headless).

### Pair in the app (recommended)

The pairing QR renders live inside the SPA — no log scraping required.

1. Open **Connectors** in the sidebar and click **Configure** on the WhatsApp card.
2. Under **Routing**, choose a **Workspace** and a **Main agent** (a chat agent from that workspace), then select **Save & Enable**. WhatsApp uses the built-in native connection. The QR appears in the panel and refreshes automatically as whatsmeow rotates it.
3. On your phone, open WhatsApp → **Linked Devices** → **Link a Device** and scan the QR.

The QR reaches the Configure panel over a live connection and refreshes on its own, so you never need to reload to get a fresh code.

If the gateway restarts before pairing finishes, the panel shows "A server restart interrupted this pairing. Press Retry to get a new code." Select **Retry** to request a fresh QR code. The same message and action are available in the live chat card when pairing was started there.

If the connection was deleted while its pairing card was open, Retry shows "This WhatsApp connection was deleted. Add it again from Connectors." with no Retry button. Add the connection again from **Connectors**. Retry from this card never recreates a deleted connection. The `enable_channel` agent tool can create a missing entry when called with revision `absent`; see [Connecting from chat](../connectors.md#connecting-from-chat) for that distinction.

The panel also says when a pairing could not start, was interrupted by a settings change, is still being stopped, or found the phone already linked — each with its exact message and next step. See [the pairing states in Connecting from chat](../connectors.md#connecting-from-chat).

### Pair from the gateway logs (headless)

When you run the gateway without the SPA in front of you, the same QR is also printed to stdout in half-block Unicode form whenever a pairing starts — when the WhatsApp connection is turned on, or turned off and on again:

```
Starting WhatsApp native channel (whatsmeow)
Scan this QR code with WhatsApp (Linked Devices):
<QR code>
```

The QR code data is also emitted as a structured log entry with `"event": "whatsapp.qr_code"` for programmatic consumption. Scan it the same way (WhatsApp → **Linked Devices** → **Link a Device**).

### After pairing

- Once paired, the session is persisted in `store.db` under `session_store_path`. Subsequent gateway starts connect without re-pairing.
- A gateway start never resumes or begins a pairing on its own. If the connection is turned on but not yet linked to a phone, a start (or restart) of the gateway shows "A server restart interrupted this pairing. Press Retry to get a new code." in the Configure panel and in a chat card that is showing the pairing live, and logs the same text as a warning. A chat reopened after a full page reload shows its recorded result and a pointer to **Connectors** instead. Press **Retry** in the panel or the live chat card, or turn the connection off and on again, to get a new code.
- Changing the connection's settings, or restarting after a pairing was stopped, does not show a new code by itself. A running pairing cut off by such a change shows "This pairing was interrupted because the WhatsApp connection was changed or restarted. Press Retry to get a new code." Automatic re-pairing after a server logout is the separate case described below.
- When the phone is already linked, turning the connection on (or a Retry) connects it without a QR code and shows "This WhatsApp device is already linked."
- If the session is logged out by the server (e.g. from the phone), the channel automatically clears the stored session, reconnects, and emits a fresh QR — both in the Configure panel and to the logs — for re-pairing.

## Notes

- **Capabilities.** The native channel implements `TypingCapable` using `whatsmeow`'s `SendChatPresence`. It sends `composing` immediately on `StartTyping`, refreshes the indicator every 10 seconds, and sends `paused` when the stop function is called. The maximum typing duration is 5 minutes.
- **Session storage.** The SQLite database is opened with `_foreign_keys=on` and `max_open_conns=1`. The store is created at `session_store_path/store.db`. Back this file up to preserve the paired session across reinstalls.
- **Reconnect behavior.** On a `Disconnected` event the channel enters an exponential-backoff reconnect loop (initial: 5 s, multiplier: 2×, ceiling: 5 min). On `LoggedOut` it clears the session and re-enters the QR pairing flow. Both reconnect paths are tracked by a `sync.WaitGroup` and respect the lifecycle context so `Stop()` exits cleanly without goroutine leaks.
- **Group message filtering.** In WhatsApp groups (JID server = `g.us`) the bot checks whether its own JID appears in `ContextInfo.MentionedJID` to detect @-mentions. The `group_trigger` config then decides whether to respond.
- **Chat ID format.** For outbound messages the `chat_id` field accepts either a bare phone number (e.g. `15551234567`) or a full JID (e.g. `15551234567@s.whatsapp.net`). For groups, use the group JID (e.g. `12345678-1234567890@g.us`).
- **No credential ref.** Session credentials are managed entirely by whatsmeow in `store.db`. The Omnipus credential store is not used for WhatsApp authentication.
