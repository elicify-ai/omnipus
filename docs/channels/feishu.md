> Back to [Channels](../channels.md)

# Feishu

Feishu (international name: Lark) is a ByteDance enterprise collaboration platform. The channel connects via the Lark Open Platform WebSocket SDK — no public webhook URL is required.

## Configuration

```json
{
  "channels": {
    "feishu": {
      "enabled": true,
      "app_id": "cli_xxx",
      "app_secret_ref": "feishu_app_secret",
      "encrypt_key_ref": "",
      "verification_token_ref": "",
      "is_lark": false,
      "allow_from": [],
      "group_trigger": {
        "mention_only": false,
        "prefixes": []
      },
      "placeholder": {
        "enabled": false,
        "text": []
      },
      "random_reaction_emoji": [],
      "reasoning_channel_id": ""
    }
  }
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Enable the Feishu channel at startup |
| `app_id` | string | Yes | App ID of the Feishu/Lark application (starts with `cli_`) |
| `app_secret_ref` | string | Yes | Credential name whose value is the App Secret |
| `encrypt_key_ref` | string | No | Credential name for the event-encryption key (optional; only needed if webhook encryption is configured on the platform) |
| `verification_token_ref` | string | No | Credential name for the webhook verification token (same caveat as `encrypt_key_ref`) |
| `is_lark` | bool | No | Set to `true` to use Lark international endpoints instead of Feishu China endpoints |
| `allow_from` | array | No | Allowlist of user IDs; empty allows all users |
| `group_trigger.mention_only` | bool | No | In group chats, only respond when the bot is @-mentioned |
| `group_trigger.prefixes` | array | No | In group chats, also respond when the message starts with one of these prefixes |
| `placeholder.enabled` | bool | No | Send a placeholder card while the agent is thinking |
| `placeholder.text` | array | No | Candidate placeholder texts (chosen randomly); default is `"Thinking..."` |
| `random_reaction_emoji` | array | No | Emoji types to add as reactions while processing; default is `"Pin"` when empty |
| `reasoning_channel_id` | string | No | Chat ID that receives reasoning/thought output from a secondary agent |

## Setup

1. Go to the [Feishu Open Platform](https://open.feishu.cn/) (or [Lark Developer Portal](https://open.larksuite.com/) for international) and create an application.
2. Enable the **Bot** capability in the application settings.
3. Store the **App Secret** in the Omnipus credential store (e.g. with key `feishu_app_secret`), then set `app_secret_ref` to that key name in `config.json`.
4. Create a version and publish the application (settings take effect only after publishing).
5. Run `omnipus start` to start the service.
6. Search for the bot name in Feishu/Lark and start a conversation.

## Notes

**32-bit architecture stub.** The Feishu SDK ships 64-bit libraries only. On 32-bit architectures (armv6, armv7, mipsle, 386, etc.) the package compiles via `feishu_32.go`, which returns an error at construction time. Use Telegram, Discord, or OneBot on 32-bit devices.

**Capabilities implemented:** `MessageEditor` (card patch), `PlaceholderCapable` (interactive card placeholder), `ReactionCapable` (emoji reaction + undo), `MediaSender` (image upload and file upload). Outbound messages are sent as interactive Lark cards with markdown rendering; plain-text fallback is used when the card table limit (error code 11310) is exceeded.

**Token cache invalidation.** The channel detects Feishu error code `99991663` (invalid tenant token) and invalidates the SDK token cache immediately, avoiding the default ~2-hour stale-token window.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
