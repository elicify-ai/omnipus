> Back to [README](../../../README.md)

# LINE

Omnipus connects to LINE Official Accounts via the [LINE Messaging API](https://developers.line.biz/en/docs/messaging-api/). Incoming messages arrive over an HTTPS webhook registered on the shared Omnipus gateway; replies are sent via the LINE REST API.

## Configuration

```json
{
  "channels": {
    "line": {
      "enabled": true,
      "channel_secret_ref": "line_channel_secret",
      "channel_access_token_ref": "line_channel_access_token",
      "webhook_path": "/webhook/line",
      "allow_from": [],
      "group_trigger": {
        "mention_only": false,
        "prefixes": []
      },
      "typing": {
        "enabled": false
      },
      "placeholder": {
        "enabled": false,
        "text": []
      },
      "reasoning_channel_id": ""
    }
  }
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Activate the LINE channel. |
| `channel_secret_ref` | string | Yes | Key name in the Omnipus credential store holding the Channel Secret. Used to verify webhook signatures. |
| `channel_access_token_ref` | string | Yes | Key name in the Omnipus credential store holding the Channel Access Token. Used to send messages. |
| `webhook_path` | string | No | Path on the Omnipus gateway where LINE delivers webhook events. Defaults to `/webhook/line`. |
| `allow_from` | array | No | LINE user ID allowlist. Empty means all users are accepted. |
| `group_trigger` | object | No | Controls when the bot responds in group/multi-person chats. `mention_only: true` restricts responses to @-mentions; `prefixes` lists trigger prefixes. |
| `typing` | object | No | `enabled: true` sends a LINE loading animation while the bot is processing. |
| `placeholder` | object | No | `enabled: true` posts a placeholder message immediately; the bot edits it with the final reply. `text` is a list of placeholder strings (chosen randomly). |
| `reasoning_channel_id` | string | No | Chat ID where extended reasoning traces are sent separately. |

## Setup

1. Go to the [LINE Developers Console](https://developers.line.biz/console/) and create a **Provider** and a **Messaging API channel** under it.
2. On the channel's **Basic settings** tab, copy the **Channel Secret**.
3. On the **Messaging API** tab, issue a **Channel access token** (long-lived).
4. Store both secrets in the Omnipus credential store under the key names you chose for `channel_secret_ref` and `channel_access_token_ref` (e.g. `omnipus credentials set line_channel_secret` and `omnipus credentials set line_channel_access_token`).
5. Configure the Webhook URL on LINE:
   - LINE requires the webhook endpoint to be reachable over HTTPS. Use a reverse proxy (nginx, Caddy) or a tunneling tool (ngrok) to expose the Omnipus gateway — which listens on port 5000 by default — with a public HTTPS URL.
   - Set the Webhook URL to `https://your-domain.com/webhook/line` (or the custom path set in `webhook_path`).
   - Click **Verify** in the LINE Developers Console to confirm the endpoint responds correctly.
   - Enable **Use webhook**.
6. Set `channel_secret_ref` and `channel_access_token_ref` in `config.json` to the key names used in steps 2–4.

## Notes

- **Webhook routing.** The LINE channel registers its handler on the shared Omnipus gateway mux at startup (path returned by `WebhookPath()`). No separate HTTP listener is started. The `webhook_host` and `webhook_port` fields exist in the config schema but are not consumed by the channel implementation — they have no effect.
- **Capabilities.** The LINE channel implements `TypingCapable` (LINE loading animation) — enable it via `typing.enabled: true`.
- **Message length.** LINE caps individual text messages at 5 000 characters; longer responses are automatically split.
- **HTTPS required.** LINE will not deliver webhooks to plain HTTP endpoints.
- **Webhook security.** The LINE channel itself signature-verifies every inbound request — HMAC-SHA256 of the raw body using your Channel Secret, compared against the `X-Line-Signature` header. The shared gateway mux does no HMAC enforcement (see `pkg/channels/README.md` §12.4). If you reverse-proxy the webhook path with nginx / Caddy / Cloudflare, **do not strip the `X-Line-Signature` header** — that breaks verification and inbound delivery will fail.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../../pkg/channels/README.md).
