> Back to [README](../../../README.md)

# Discord

Omnipus connects to Discord as a bot using the [discordgo](https://github.com/bwmarrin/discordgo) library. The bot receives messages over a persistent WebSocket gateway connection and sends replies via the Discord REST API.

## Configuration

```json
{
  "channels": {
    "discord": {
      "enabled": true,
      "token_ref": "discord_bot_token",
      "proxy": "",
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
| `enabled` | bool | Yes | Activate the Discord channel. |
| `token_ref` | string | Yes | Key name in the Omnipus credential store holding the Bot Token. |
| `proxy` | string | No | HTTP/SOCKS5 proxy URL for all Discord connections (e.g. `socks5://127.0.0.1:1080`). |
| `allow_from` | array | No | Allowlist of Discord user IDs. Empty means all users are accepted. |
| `group_trigger` | object | No | Controls when the bot responds in guild channels. `mention_only: true` restricts responses to @-mentions; `prefixes` lists trigger prefixes. |
| `typing` | object | No | `enabled: true` sends a typing indicator while the bot is processing. |
| `placeholder` | object | No | `enabled: true` posts a placeholder message immediately; the bot edits it with the final reply. `text` is a list of placeholder strings (chosen randomly). |
| `reasoning_channel_id` | string | No | Channel ID where extended reasoning traces are sent separately. |

## Setup

1. Go to the [Discord Developer Portal](https://discord.com/developers/applications) and create a new application.
2. Under **Bot**, create a bot user and copy its **Token**.
3. Store the token in the Omnipus credential store under the key you choose for `token_ref` (e.g. `omnipus credentials set discord_bot_token`).
4. Under **Bot > Privileged Gateway Intents**, enable:
   - **Message Content Intent**
   - **Server Members Intent** (if you use `allow_from` or mention detection)
5. Under **OAuth2 > URL Generator**, grant the **bot** scope with at minimum **Send Messages** and **Read Message History** permissions. Use the generated URL to invite the bot to your server.
6. Set `token_ref` in `config.json` to the key name used in step 3.

## Notes

- **Capabilities.** The Discord channel implements `TypingCapable`, `PlaceholderCapable`, and `MessageEditor` — enable them via the `typing` and `placeholder` config blocks.
- **Message length.** Discord caps individual messages at 2 000 characters; longer responses are automatically split.
- **Group chats.** In guild text channels, use `group_trigger.mention_only: true` so the bot only responds when directly @-mentioned.
- **Deprecated top-level `mention_only`.** `DiscordConfig` still has a top-level `mention_only` field for backward compatibility, but the channel code reads only `group_trigger.mention_only`. The top-level field is inert — leave it unset and configure under `group_trigger`.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../../pkg/channels/README.md).
