> Back to [Channels](../channels.md)

# Google Chat

Omnipus supports Google Chat via two modes: **webhook** (outbound only, simple setup) and **bot** (full interactive — receives and sends messages).

## Configuration

### Webhook mode

```json
{
  "channels": {
    "google-chat": {
      "enabled": true,
      "mode": "webhook",
      "webhook_url": "https://chat.googleapis.com/v1/spaces/SPACE_ID/messages?key=...",
      "allow_from": []
    }
  }
}
```

### Bot mode

```json
{
  "channels": {
    "google-chat": {
      "enabled": true,
      "mode": "bot",
      "service_account_json": "{...}",
      "space": "spaces/abc123",
      "bot_user": "bot@your-project.iam.gserviceaccount.com",
      "allow_from": [],
      "group_trigger": {
        "mention_only": false,
        "prefixes": ["/ask"]
      },
      "typing": {},
      "placeholder": {
        "enabled": false,
        "text": "Thinking..."
      },
      "reasoning_channel_id": ""
    }
  }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | bool | Yes | Enable the Google Chat channel. |
| `mode` | string | Yes | `"webhook"` (outbound only) or `"bot"` (full interactive). If omitted, defaults to `"webhook"` when `webhook_url` is set, otherwise `"bot"`. |
| `webhook_url` | string | Yes (webhook) | Incoming webhook URL from the Google Chat space. |
| `service_account_file` | string | Yes (bot, if no JSON) | Path to the service account JSON key file on disk. |
| `service_account_json` | string | Yes (bot, if no file) | Inline service account JSON. When both are provided, the file takes precedence. |
| `space` | string | No | Google Chat space name (e.g. `spaces/abc123`) for display purposes. |
| `bot_user` | string | No | Service account email address. Used for mention detection in group spaces. |
| `allow_from` | []string | No | Sender allowlist (user `name` fields from Google Chat events). Empty means all senders are allowed. |
| `group_trigger` | object | No | Group-chat trigger rules — `mention_only` (bool) and/or `prefixes` ([]string). |
| `typing` | object | No | Typing indicator configuration (bot mode only). |
| `placeholder` | object | No | Placeholder message configuration — `enabled` (bool) and `text` (string or []string). |
| `reasoning_channel_id` | string | No | Space name to route reasoning/thinking output to a separate conversation. |

## Setup

### Webhook mode

1. Go to the Google Chat space where you want to post messages.
2. Click **...** > **Manage webhooks** (or **Configure webhooks** on older clients).
3. Give the webhook a name and copy the webhook URL.
4. Set `webhook_url` in `config.json` and set `mode: "webhook"`.

Webhook mode is outbound only — Omnipus can post messages, but cannot receive them.

### Bot mode

1. Create a Google Cloud project and enable the **Google Chat API**.
2. Create a service account under **IAM & Admin** > **Service Accounts** and download the JSON key file.
3. In the Google Chat API settings:
   - Set the bot name and avatar.
   - Configure the connection settings to point at the Omnipus webhook endpoint:
     `https://<your-host>/webhook/google-chat`
   - Enable bot interactions.
4. Add the service account email to the Google Chat space.
5. Set either `service_account_file` (path to the JSON key file) or `service_account_json` (inline JSON) in `config.json`.
6. Set `mode: "bot"` and optionally set `bot_user` to the service account email for mention detection.

## Group trigger

The `group_trigger` section controls when the bot responds in group spaces:

```json
{
  "group_trigger": {
    "mention_only": false,
    "prefixes": ["/ask", "/omnipus"]
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `mention_only` | bool | `false` | Only respond when @mentioned (by `bot_user` name or the string `"OmnipusBot"`). |
| `prefixes` | []string | `[]` | Respond to messages starting with any of these prefixes. |

When neither is set, the bot responds to all messages in group spaces (permissive default).

## Capabilities

- **Webhook mode**: Post messages to Google Chat spaces via incoming webhooks (send only).
- **Bot mode**: Full bidirectional messaging with JWT authentication via service account.
- **Signature verification**: Bot mode verifies inbound request signatures using RSA keys fetched from Google's JWKS endpoint (`https://www.googleapis.com/service_accounts/jwks`). Requests without a valid `Google-Signature` header are rejected.
- **Typing indicators**: Bot mode sends typing indicators while composing responses (`TypingCapable`).
- **Thread support**: Replies use the thread key from the inbound message when present.
- **Retry with backoff**: API calls retry up to 3 times on transient errors (5xx, 429) with exponential backoff and `Retry-After` header support.
- **Health endpoint**: `GET /webhook/google-chat/health` returns 200 when the channel is running.

## Webhook security and secrets

- **Webhook-mode `webhook_url` is a bearer secret.** The URL contains a `?key=…` query parameter that grants posting rights to the space. Do not commit it to `config.json` in plain text — store the URL itself in the credential store and reference it via `_ref` patterns (the field name follows the convention used elsewhere; see your config for the exact key).
- **Bot-mode signature verification runs inside the channel**, not in the shared gateway mux. The implementation parses the `Google-Signature` header, fetches Google's JWKS (with caching), verifies RSA-SHA256 over the request body, and rejects on mismatch. If you front the bot endpoint with a reverse proxy, **do not strip the `Google-Signature` header** — that breaks verification. The shared gateway mux does no HMAC/signature enforcement of its own; see `pkg/channels/README.md` §12.4.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
