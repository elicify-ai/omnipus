> Back to [Channels](../channels.md)

# OneBot

OneBot v11 channel for Omnipus. Connects over WebSocket to any OneBot-compatible bot framework (e.g. NapCat, go-cqhttp) and provides full bidirectional messaging for private and group QQ chats.

## Configuration

```json
{
  "channels": {
    "onebot": {
      "enabled": true,
      "ws_url": "ws://localhost:8080",
      "access_token_ref": "",
      "reconnect_interval": 10,
      "allow_from": [],
      "group_trigger": {
        "mention_only": false,
        "prefixes": []
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

The `access_token_ref` field holds a credential store key name, not the token value directly. Store the token with `omnipus credentials set <key> <token>`.

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | bool | Yes | Enable the OneBot channel. |
| `ws_url` | string | Yes | WebSocket URL of the OneBot server, e.g. `ws://localhost:8080`. |
| `access_token_ref` | string | No | Credential store key for the access token. Leave empty if the OneBot server requires no token. |
| `reconnect_interval` | int | No | Reconnect interval in seconds after a disconnect. `0` disables automatic reconnect (fails hard on initial connection failure). |
| `allow_from` | []string | No | QQ user ID allowlist. Empty means all senders are allowed. |
| `group_trigger` | object | No | Group-chat trigger rules — `mention_only` (bool) and/or `prefixes` ([]string). |
| `typing` | object | No | Typing indicator configuration (reserved; not actively used by this channel). |
| `placeholder` | object | No | Reserved; the OneBot channel does not implement `PlaceholderCapable` today, so this block has no effect. |
| `reasoning_channel_id` | string | No | Chat ID to route reasoning/thinking output to a separate conversation. |

Note: `group_trigger_prefix` (legacy flat array) is still accepted and migrated automatically to `group_trigger.prefixes` on load.

## Setup

1. Deploy a OneBot v11-compatible implementation. [NapCat](https://github.com/NapNeko/NapCatQQ) is the recommended modern choice; go-cqhttp also works.
2. In the bot framework's settings, enable the reverse WebSocket server (or forward WebSocket) and optionally set an access token.
3. If using an access token, store it in the Omnipus credential store:
   ```bash
   omnipus credentials set onebot_token <token>
   ```
   Then set `access_token_ref: "onebot_token"` in `config.json`.
4. Set `ws_url` to the WebSocket endpoint exposed by the bot framework.
5. Start the gateway. Omnipus connects on startup and reconnects automatically if `reconnect_interval` is set.

## Capabilities

- Private and group message send/receive
- Outgoing image, video, audio (record), and file media via the media store
- Group trigger rules: `mention_only` and/or `prefixes`
- Emoji reaction on group messages (`ReactToMessage` — uses emoji ID 289)
- Reply threading: replies quote the last received message in that chat
- Duplicate message suppression (ring buffer, last 1024 message IDs)
- Automatic reconnect with configurable interval

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
