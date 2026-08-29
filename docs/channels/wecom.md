> Back to [Channels](../channels.md)

# WeCom

Omnipus exposes WeCom as a single `channels.wecom` channel built on the official WeCom AI Bot WebSocket API.
This replaces the legacy `wecom`, `wecom_app`, and `wecom_aibot` split with one unified configuration model.

> No public webhook callback URL is required. Omnipus opens an outbound WebSocket connection to WeCom.

## What This Channel Supports

- Direct chat and group chat delivery
- Channel-side streaming replies over WeCom's AI Bot protocol
- Incoming text, voice, image, file, video, and mixed messages
- Outbound text and media replies (`image`, `file`, `voice`, `video`)
- QR-based onboarding via Web UI or CLI
- Shared allowlist and `reasoning_channel_id` routing

---

## Quick Start

### Option 1: Web UI QR Binding (Recommended)

Open the Web UI, navigate to **Connectors -> WeCom**, and click the QR binding button. Scan the QR code with WeCom and confirm in the app — credentials are saved automatically.

### Option 2: Manual credential setup

If you already have a `bot_id` and secret from the WeCom AI Bot platform, store the
secret and configure the channel as shown in Option 3 below.

### Option 3: Configure manually

If you already have a `bot_id` and secret from the WeCom AI Bot platform:

1. Store the secret in the Omnipus credential store:
   ```bash
   omnipus credentials set wecom_secret <your-secret>
   ```
2. Add the following to `config.json`:

```json
{
  "channels": {
    "wecom": {
      "enabled": true,
      "bot_id": "YOUR_BOT_ID",
      "secret_ref": "wecom_secret",
      "websocket_url": "wss://openws.work.weixin.qq.com",
      "send_thinking_message": true,
      "allow_from": [],
      "reasoning_channel_id": ""
    }
  }
}
```

---

## Configuration

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `enabled` | bool | `false` | Enable the WeCom channel. |
| `bot_id` | string | — | WeCom AI Bot identifier. Required when enabled. |
| `secret_ref` | string | — | Credential store key for the WeCom AI Bot secret. Required when enabled. |
| `websocket_url` | string | `wss://openws.work.weixin.qq.com` | WeCom WebSocket endpoint. |
| `send_thinking_message` | bool | `true` | Send a `Processing...` message before the streamed reply begins. |
| `allow_from` | []string | `[]` | Sender allowlist. Empty means allow all senders. |
| `reasoning_channel_id` | string | `""` | Optional chat ID to route reasoning/thinking output to a separate conversation. |

### Environment Variables

Fields can be overridden via environment variables. The parent block uses the prefix `OMNIPUS_CHANNELS_WECOM_`:

| Environment Variable | Corresponding Field |
| -------------------- | ------------------- |
| `OMNIPUS_CHANNELS_WECOM_ENABLED` | `enabled` |
| `OMNIPUS_CHANNELS_WECOM_BOT_ID` | `bot_id` |
| `OMNIPUS_CHANNELS_WECOM_SECRET_REF` | `secret_ref` |
| `OMNIPUS_CHANNELS_WECOM_WEBSOCKET_URL` | `websocket_url` |
| `OMNIPUS_CHANNELS_WECOM_SEND_THINKING_MESSAGE` | `send_thinking_message` |
| `OMNIPUS_CHANNELS_WECOM_ALLOW_FROM` | `allow_from` |
| `OMNIPUS_CHANNELS_WECOM_REASONING_CHANNEL_ID` | `reasoning_channel_id` |

---

## Runtime Behavior

- Omnipus maintains an active WeCom turn so streaming replies can continue on the same stream when possible.
- Streaming replies have a maximum duration of **5.5 minutes** and a minimum send interval of **500 ms**.
- If streaming is no longer available, replies fall back to active push delivery.
- Chat route associations expire after **30 minutes** of inactivity.
- Incoming media is downloaded into the local media store before being passed to the agent.
- Outbound media is uploaded to WeCom as a temporary file and then sent as a media message.
- Duplicate messages are detected and suppressed (ring buffer of last 1000 message IDs).

---

## Migration from Legacy WeCom Config

| Previous config | Migration |
| --------------- | --------- |
| `channels.wecom` (webhook bot) | Replace with `channels.wecom` using `bot_id` + `secret_ref`. |
| `channels.wecom_app` | Remove. Use `channels.wecom` instead. |
| `channels.wecom_aibot` | Move `bot_id` to `channels.wecom.bot_id`; store the secret in the credential store and set `secret_ref`. |
| `token`, `encoding_aes_key`, `webhook_url`, `webhook_path` | No longer used. Remove from config. |
| `corp_id`, `corp_secret`, `agent_id` | No longer used. Remove from config. |
| `welcome_message`, `processing_message`, `max_steps` | No longer part of the WeCom channel config. |
| `secret` (plain text) | Move value to credential store: `omnipus credentials set wecom_secret <value>`. Set `secret_ref: "wecom_secret"`. |

---

## Troubleshooting

### QR binding times out

- After scanning the QR code, you must also **confirm the login inside the WeCom app**. Scanning alone is not enough.
- If the QR code in the Configure panel is hard to scan, use the **QR Code Link** shown below it to open in a browser.

### QR code expired

- The QR code has a limited validity. Open **Connectors → WeCom → Configure** again to get a fresh QR code.

### WebSocket connection fails

- Verify `bot_id` and the secret in the credential store are correct.
- Confirm the host can reach `wss://openws.work.weixin.qq.com` (outbound WebSocket, no inbound port needed).

### Replies do not arrive

- Check whether `allow_from` is blocking the sender.
- Check that `channels.wecom.bot_id` and `channels.wecom.secret_ref` are set and non-empty, and that the referenced credential exists in the store.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
