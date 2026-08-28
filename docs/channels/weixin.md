> Back to [Channels](../channels.md)

# Weixin (WeChat Personal)

Omnipus connects to a personal WeChat account using the Tencent iLink REST API via long polling. The channel is personal-account only — it does not use WeChat Official Accounts or Work WeChat.

## Quick Setup

Open **Connectors → Weixin → Configure** in the web UI (`omnipus start`, then visit
`http://localhost:5000`). A QR code appears in the Configure panel — scan it with the
WeChat mobile app. On approval the token is saved to the encrypted credential store and
the channel config is written to `~/.omnipus/config.json` automatically.

After authenticating, click **Save & Enable** in the Configure panel to activate the
channel.

## Configuration

```json
{
  "channels": {
    "weixin": {
      "enabled": true,
      "token_ref": "weixin_token",
      "account_id": "",
      "base_url": "",
      "cdn_base_url": "",
      "proxy": "",
      "allow_from": [],
      "reasoning_channel_id": ""
    }
  }
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Enable the Weixin channel at startup |
| `token_ref` | string | Yes | Credential name whose value is the iLink bot token (written automatically by the web UI Configure flow) |
| `account_id` | string | No | iLink account ID (written automatically by the web UI Configure flow; rarely set manually) |
| `base_url` | string | No | iLink API base URL; defaults to `https://ilinkai.weixin.qq.com/` |
| `cdn_base_url` | string | No | iLink CDN base URL for media downloads; defaults to the standard iLink CDN |
| `proxy` | string | No | HTTP proxy URL for environments where `ilinkai.weixin.qq.com` is not directly reachable (e.g. `http://localhost:7890`) |
| `allow_from` | array | No | Allowlist of WeChat user IDs; empty allows all senders who can message the connected account |
| `reasoning_channel_id` | string | No | User ID that receives reasoning/thought output from a secondary agent |

## Notes

**Session binding.** The iLink token is bound to a single session. A new QR login on another device will invalidate the existing token. To refresh, open **Connectors → Weixin → Configure** in the web UI and complete the QR flow again.

**Long-poll loop.** The channel uses a `getUpdates`-style long poll (35-second server timeout). On consecutive failures it backs off for 30 seconds. Persisted `get_updates_buf` and context tokens survive gateway restarts (stored under `~/.omnipus/`).

**Capabilities implemented:** `TypingCapable` (typing status via iLink API), `MediaSender` (image and file upload/download via CDN). Supports inbound images, voice, and file attachments.

**Rate limits.** Avoid high-frequency automated broadcasts; WeChat anti-spam systems may restrict accounts that send messages at excessive rates.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
