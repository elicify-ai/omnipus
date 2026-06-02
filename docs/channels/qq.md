> Back to [Channels](../channels.md)

# QQ

Omnipus connects to QQ via the official QQ Bot Open Platform API using a persistent WebSocket session (botgo library). It handles both direct (C2C) messages and group @-mention messages.

## Configuration

```json
{
  "channels": {
    "qq": {
      "enabled": true,
      "app_id": "YOUR_APP_ID",
      "app_secret_ref": "qq_app_secret",
      "allow_from": [],
      "group_trigger": {
        "mention_only": false,
        "prefixes": []
      },
      "max_message_length": 0,
      "max_base64_file_size_mib": 0,
      "send_markdown": false,
      "reasoning_channel_id": ""
    }
  }
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Enable the QQ channel at startup |
| `app_id` | string | Yes | App ID of the QQ bot application |
| `app_secret_ref` | string | Yes | Credential name whose value is the App Secret |
| `allow_from` | array | No | Allowlist of user IDs; empty allows all users |
| `group_trigger.mention_only` | bool | No | In group chats, only respond when the bot is @-mentioned |
| `group_trigger.prefixes` | array | No | In group chats, also respond when the message starts with one of these prefixes |
| `max_message_length` | int | No | Maximum outbound message length in characters; `0` uses the channel default |
| `max_base64_file_size_mib` | int | No | Maximum local file size (MiB) to send as base64 inline media; `0` uses the channel default |
| `send_markdown` | bool | No | Send outbound text using QQ markdown message format instead of plain text |
| `reasoning_channel_id` | string | No | Group ID that receives reasoning/thought output from a secondary agent |

## Setup

1. Log in to the [QQ Open Platform](https://q.qq.com/) with your QQ account and register as a developer.
2. Create a QQ bot and set its avatar and display name.
3. Store the **App Secret** in the Omnipus credential store (e.g. with key `qq_app_secret`), then set `app_secret_ref` to that key name in `config.json`.
4. Set `app_id` to the App ID shown on the bot settings page.
5. Run `omnipus start` to start the service.
6. Search for your bot in QQ and start chatting.

> During development, enable sandbox mode on the QQ Open Platform and add test users and groups to the sandbox before going live.

## Notes

**Capabilities implemented:** `TypingCapable` (typing status via QQ API), `MediaSender` (image and file upload via base64 inline or URL). The channel registers handlers for both C2C (`handleC2CMessage`) and group @-mention (`handleGroupATMessage`) events. Message deduplication is performed with a time-bounded map (5-minute TTL) to suppress duplicates from QQ's at-least-once delivery.

**Group chats.** The QQ Bot API requires an @-mention in group chats for the bot to receive the message. `group_trigger.mention_only` is therefore the effective default for group interactions regardless of its value; `group_trigger.prefixes` applies to the content after the @-mention is stripped.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
