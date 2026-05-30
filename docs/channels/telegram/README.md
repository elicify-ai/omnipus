> Back to [README](../../../README.md)

# Telegram

The Telegram channel receives messages via long polling (30-second timeout) using the Telegram Bot API. It supports text, media attachments (photos, voice, audio, documents), streaming replies, and automatic bot command registration.

## Configuration

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token_ref": "telegram_bot_token",
      "base_url": "",
      "proxy": "",
      "allow_from": ["123456789"],
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
      "streaming": {
        "enabled": false,
        "throttle_seconds": 0,
        "min_growth_chars": 0
      },
      "use_markdown_v2": false,
      "reasoning_channel_id": ""
    }
  }
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Enable the Telegram channel at startup |
| `token_ref` | string | Yes | Credential name whose value is the Telegram Bot API token |
| `base_url` | string | No | Custom Bot API server URL (e.g. for self-hosted Bot API); defaults to the official Telegram endpoint |
| `proxy` | string | No | HTTP/HTTPS proxy URL for reaching the Telegram API (e.g. `http://127.0.0.1:7890`); falls back to `HTTP_PROXY`/`HTTPS_PROXY` env vars if unset |
| `allow_from` | array | No | Allowlist of numeric user IDs; empty allows all users |
| `group_trigger.mention_only` | bool | No | In group chats, only respond when the bot is @-mentioned |
| `group_trigger.prefixes` | array | No | In group chats, also respond when the message starts with one of these prefixes |
| `typing.enabled` | bool | No | Send `typing` chat action while processing |
| `placeholder.enabled` | bool | No | Send a placeholder message before the real reply is ready |
| `placeholder.text` | array | No | Candidate placeholder texts; default is `"Thinking..."` |
| `streaming.enabled` | bool | No | Stream the reply by editing the placeholder message incrementally |
| `streaming.throttle_seconds` | int | No | Minimum seconds between streaming edits |
| `streaming.min_growth_chars` | int | No | Minimum new characters required before issuing an edit |
| `use_markdown_v2` | bool | No | Use Telegram MarkdownV2 formatting; default is HTML formatting |
| `reasoning_channel_id` | string | No | Chat ID that receives reasoning/thought output from a secondary agent |

## Setup

1. Search for `@BotFather` in Telegram.
2. Send `/newbot` and follow the prompts to create a new bot.
3. Copy the HTTP API token that BotFather provides.
4. Store the token in the Omnipus credential store (e.g. with key `telegram_bot_token`), then set `token_ref` to that key name in `config.json`.
5. Optionally set `allow_from` to a list of numeric user IDs to restrict access (find your ID via `@userinfobot`).
6. Run `omnipus start` to start the service.

## Built-in Commands

At startup Telegram registers Omnipus's built-in bot commands automatically via `CommandRegistrarCapable`. Registration retries in the background with exponential backoff if the Telegram API is temporarily unavailable. The registered commands are:

| Command | Purpose |
| --- | --- |
| `/start` | Open or resume the current session |
| `/help` | Show help text |
| `/show` | Show the active agent or session details |
| `/list` | List resources (e.g. `/list skills`) |
| `/use` | Select a skill for the next request (e.g. `/use git`) |
| `/switch` | Switch to a different agent |
| `/check` | Show current health/status of the gateway |
| `/clear` | Clear the active session's transcript |
| `/subagents` | List or interact with sub-agents |
| `/reload` | Hot-reload configuration |
| `/cancel` | Interrupt the agent's current turn |

(Defined in `pkg/commands/builtin.go` — the full registered set.)

## Notes

**Capabilities implemented:** `TypingCapable`, `MessageEditor` (edit sent message), `MessageDeleter`, `PlaceholderCapable`, `MediaSender` (photos, voice, audio, documents), `StreamingCapable` (incremental edit-based streaming), `CommandRegistrarCapable`.

**Message length.** The channel enforces a 4000-character soft limit before sending, with an additional overflow-split guard at Telegram's 4096-character API hard limit. Long messages are split at natural break points (newlines, spaces, code-block boundaries).

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../../pkg/channels/README.md).
