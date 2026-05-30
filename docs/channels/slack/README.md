> Back to [README](../../../README.md)

# Slack

Omnipus connects to Slack using [Socket Mode](https://api.slack.com/apis/socket-mode) via the [slack-go](https://github.com/slack-go/slack) library. Socket Mode maintains a persistent WebSocket connection — no public inbound endpoint or webhook URL is required.

## Configuration

```json
{
  "channels": {
    "slack": {
      "enabled": true,
      "bot_token_ref": "slack_bot_token",
      "app_token_ref": "slack_app_token",
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
| `enabled` | bool | Yes | Activate the Slack channel. |
| `bot_token_ref` | string | Yes | Key name in the Omnipus credential store holding the Bot User OAuth Token (`xoxb-…`). |
| `app_token_ref` | string | Yes | Key name in the Omnipus credential store holding the App Level Token for Socket Mode (`xapp-…`). |
| `allow_from` | array | No | Slack user ID allowlist. Empty means all users are accepted. |
| `group_trigger` | object | No | Controls when the bot responds in channels/group DMs. `mention_only: true` restricts responses to @-mentions; `prefixes` lists trigger prefixes. |
| `typing` | object | No | Reserved; not actively read by the Slack channel. Typing indicators always run through `TypingCapable` when the agent loop calls for them. |
| `placeholder` | object | No | Reserved; the Slack channel does not implement `PlaceholderCapable` today, so this block has no effect. |
| `reasoning_channel_id` | string | No | Channel ID where extended reasoning traces are sent separately. |

## Setup

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and create a new app **from scratch**.
2. Under **Socket Mode**, enable Socket Mode and generate an **App Level Token** with the `connections:write` scope. This is your `app_token_ref` value (`xapp-…`).
3. Under **OAuth & Permissions > Bot Token Scopes**, add the scopes your use case needs. Typical minimum:
   - `chat:write` — send messages
   - `im:history` — read direct messages
   - `channels:history` — read messages in public channels the bot is added to
   - `app_mentions:read` — receive @-mention events (needed with `mention_only: true`)
4. Under **Event Subscriptions**, enable events and subscribe to:
   - `message.im` (direct messages)
   - `message.channels` or `message.groups` (channel/group messages, as needed)
   - `app_mention` (if using `mention_only`)
5. Under **Install App**, install the app to your workspace. Copy the **Bot User OAuth Token** (`xoxb-…`).
6. Store both tokens in the Omnipus credential store under the key names you chose (e.g. `omnipus credentials set slack_bot_token` and `omnipus credentials set slack_app_token`).
7. Set `bot_token_ref` and `app_token_ref` in `config.json` to the key names used in step 6.

## Notes

- **Socket Mode.** Because Slack delivers events over the Socket Mode WebSocket, no public URL or firewall rule is required. The connection is outbound from the Omnipus process.
- **Capabilities.** The Slack channel implements `TypingCapable`, `ReactionCapable`, `MediaSender`, and `CommandRegistrarCapable`. Typing indicators are driven by the agent loop via `TypingCapable`; the `typing` and `placeholder` config blocks are reserved fields and have no effect today.
- **Message length.** Slack caps individual messages at 40 000 characters; longer responses are automatically split.
- **Group chats.** Use `group_trigger.mention_only: true` so the bot only responds in channels when directly @-mentioned.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../../pkg/channels/README.md).
