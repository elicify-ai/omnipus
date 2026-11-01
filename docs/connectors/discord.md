> Back to [Connectors](../connectors.md)

# Discord connector

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

## Starting messages

In the connector's **Configure → Starting messages** section (see [Starting messages](../connectors.md#starting-messages)) you list where the agent may start messages. A Discord id in that list can be:

- a person's user id: the agent's message reaches them as a direct message;
- a text channel, announcement channel or thread id: the message is posted there.

Category, forum, voice channel and direct-message channel ids are refused when you save. For a direct-message channel id the refusal says `use the person's user id instead`. A thread inside a forum works; the forum itself does not. An id the bot cannot see is refused with `the bot cannot see this channel`, and an unknown id with `no person, channel or thread with this id was found`.

Write each id as plain digits or as `discord:<id>`; names and `*` are refused. To copy an id in Discord, turn on **Developer Mode** (User Settings → Advanced), then right-click a person and choose **Copy User ID**, or right-click a channel and choose **Copy Channel ID**. The ids in the panel's ignored-senders line (see Notes) are already user ids.

Adding a Discord id needs the connector running: Omnipus checks every new id with Discord before it saves, and while the connector is not running the save is refused with `Connect the channel first to add Discord ids.` If Discord cannot be reached for the check, nothing is saved; see [troubleshooting](../troubleshooting.md#saving-starting-messages-is-refused-or-shows-a-warning). Removing ids and switching to **Only answer** work while the connector is offline.

Discord decides whether a direct message gets through. Under Discord's rules the person must share a server with the bot, and their Discord privacy settings must allow direct messages from it. Omnipus does not check either condition. If Discord refuses, nothing is sent and the failure appears only in the gateway log.

## Notes

- **Capabilities.** The Discord channel implements `TypingCapable`, `PlaceholderCapable`, and `MessageEditor` — enable them via the `typing` and `placeholder` config blocks.
- **Message length.** Discord caps individual messages at 2 000 characters; longer responses are automatically split.
- **Ignored senders.** When `allow_from` is not empty, the bot ignores messages from anyone not on it and sends them no reply. The connector's **Configure** panel then shows one line, `Ignored <count> messages from senders not on the allowed list. Latest: <ids>`. The count runs from when the connector last started, and the ids (for example `discord:123456789012345678`) are the latest ignored senders, most recent first. To let one of them in, select and copy the id, add it to **Allow From**, and save. Saving an enabled connector with a changed **Allow From** restarts it, and once the save message confirms that, the new list applies from the next message; you do not need to disable and enable the connector. If the save message says applying did not complete, see [what a connection's save message means](../troubleshooting.md#what-a-connections-save-message-means).
- **Group chats.** In guild text channels, use `group_trigger.mention_only: true` so the bot only responds when directly @-mentioned.
- **Deprecated top-level `mention_only`.** `DiscordConfig` still has a top-level `mention_only` field for backward compatibility, but the channel code reads only `group_trigger.mention_only`. The top-level field is inert — leave it unset and configure under `group_trigger`.
