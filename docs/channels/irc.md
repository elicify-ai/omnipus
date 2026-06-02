> Back to [Channels](../channels.md)

# IRC

Omnipus connects to any IRC server via a persistent TCP connection (optionally TLS) using the [ergochat/irc-go](https://github.com/ergochat/irc-go) library. The client joins one or more channels at connect time, handles reconnection internally via `ircevent.Connection.Loop()`, and delivers and receives plain-text PRIVMSG lines.

## Configuration

```json
{
  "channels": {
    "irc": {
      "enabled": true,
      "server": "irc.libera.chat:6697",
      "tls": true,
      "nick": "mybot",
      "user": "",
      "real_name": "",
      "password_ref": "",
      "nickserv_password_ref": "",
      "sasl_user": "",
      "sasl_password_ref": "",
      "channels": ["#general"],
      "request_caps": [],
      "allow_from": [],
      "group_trigger": {
        "mention_only": false,
        "prefixes": []
      },
      "typing": {
        "enabled": false
      },
      "reasoning_channel_id": ""
    }
  }
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | bool | Yes | Activate the IRC channel. |
| `server` | string | Yes | IRC server in `host:port` form, e.g. `irc.libera.chat:6697`. |
| `tls` | bool | No | Use TLS for the connection. Default: `false`. |
| `nick` | string | Yes | Bot nickname. |
| `user` | string | No | IRC username (ident). Defaults to `nick` when empty. |
| `real_name` | string | No | IRC real name field. Defaults to `nick` when empty. |
| `password_ref` | string | No | Credential store key for the server password (sent as PASS during registration). |
| `nickserv_password_ref` | string | No | Credential store key for NickServ identification password. Used only when `sasl_user` is not set. |
| `sasl_user` | string | No | SASL PLAIN username. When set, SASL takes priority over NickServ. |
| `sasl_password_ref` | string | No | Credential store key for the SASL PLAIN password. Required when `sasl_user` is set. |
| `channels` | []string | Yes | IRC channels to join on connect, e.g. `["#general", "#dev"]`. |
| `request_caps` | []string | No | IRCv3 capabilities to request. Defaults to `["server-time", "message-tags"]`. |
| `allow_from` | []string | No | Allowlist of IRC nicks. Empty means all nicks are accepted. |
| `group_trigger` | object | No | Controls when the bot responds in channel messages. `mention_only: true` restricts to `nick:` / `nick,` prefix or word-boundary @-mention. `prefixes` lists additional trigger strings. |
| `typing` | object | No | `enabled: true` sends IRCv3 `+typing=active` TAGMSG while processing; requires `message-tags` capability to be acknowledged by the server. |
| `reasoning_channel_id` | string | No | IRC channel name where extended reasoning traces are sent separately. |

## Setup

1. Choose a target IRC network (e.g. [Libera.Chat](https://libera.chat), [OFTC](https://www.oftc.net)).
2. Register a nickname for your bot with NickServ on the chosen network. Follow the network's registration guide (e.g. `/msg NickServ REGISTER <password> <email>` on Libera.Chat).
3. If the network supports SASL PLAIN (recommended), set `sasl_user` to the registered nick and store the password in the credential store:
   ```bash
   omnipus credentials set irc_sasl_password <password>
   ```
   Set `sasl_password_ref: "irc_sasl_password"` in `config.json`.
4. Alternatively, for NickServ-only auth, store the NickServ password:
   ```bash
   omnipus credentials set irc_nickserv_password <password>
   ```
   Set `nickserv_password_ref: "irc_nickserv_password"` in `config.json`. Leave `sasl_user` empty.
5. List the channels the bot should join in `channels`, e.g. `["#omnipus"]`.
6. Set `server` and `tls: true` for IRC networks that offer TLS on port 6697 (standard).

## Notes

- **Capabilities.** The IRC channel implements `TypingCapable` using the IRCv3 `+typing` client tag. The stop function sends `+typing=done`. Typing is silently skipped when the `message-tags` capability is not acknowledged by the server.
- **Message length.** Messages longer than 400 characters are automatically split into multiple PRIVMSGs. Each line of a multi-line response is sent as a separate PRIVMSG.
- **Auth priority.** SASL PLAIN takes priority over NickServ. If both `sasl_user` and `nickserv_password_ref` are set, only SASL is used.
- **Reconnect behavior.** `ircevent.Connection.Loop()` handles reconnection and channel rejoin transparently. The `onConnect` callback fires on every successful connect, re-running NickServ identification and channel joins.
- **Group chat trigger.** In IRC channels (targets starting with `#` or `&`), the group trigger controls response. The most common IRC convention — `botnick: message` or `botnick, message` — is recognized as a mention and the prefix is stripped before the message reaches the agent.
- **Direct messages.** PRIVMSGs sent directly to the bot's nick (not to a channel) are always handled without trigger filtering.

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../pkg/channels/README.md).
