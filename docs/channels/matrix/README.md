> Back to [README](../../../README.md)

# Matrix

Matrix channel for Omnipus. Uses the `mautrix-go` library with the `goolm` pure-Go OLM implementation for end-to-end encryption. Supports full bidirectional messaging, media send/receive, typing indicators, and placeholder messages.

## Build requirement

The Matrix channel requires the `goolm` build tag **and CGo enabled** in the gateway:

```bash
CGO_ENABLED=1 go build -tags goolm,stdjson ./...
```

The matrix subpackage itself only needs `goolm`, but `pkg/gateway/channel_matrix.go` is gated by `//go:build !mipsle && !netbsd && !(freebsd && arm) && cgo`, so a `CGO_ENABLED=0` build silently excludes Matrix from the gateway even when `goolm` is set. Matrix is unavailable on `linux/mipsle`, `netbsd/*`, and `freebsd/arm` due to upstream `modernc.org/sqlite` and `modernc.org/libc` build failures on those targets.

## Configuration

Add this to `config.json`:

```json
{
  "channels": {
    "matrix": {
      "enabled": true,
      "homeserver": "https://matrix.org",
      "user_id": "@your-bot:matrix.org",
      "access_token_ref": "matrix_access_token",
      "device_id": "",
      "join_on_invite": true,
      "allow_from": [],
      "group_trigger": {
        "mention_only": true,
        "prefixes": []
      },
      "placeholder": {
        "enabled": true,
        "text": ["Thinking...", "Processing...", "Typing..."]
      },
      "reasoning_channel_id": "",
      "message_format": "richtext",
      "crypto_database_path": "",
      "crypto_passphrase_ref": "matrix_crypto_passphrase"
    }
  }
}
```

Credentials (`access_token_ref`, `crypto_passphrase_ref`) are resolved from the Omnipus credential store — the config field holds the credential key name, not the secret value itself.

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | bool | Yes | Enable the Matrix channel. |
| `homeserver` | string | Yes | Matrix homeserver URL, e.g. `https://matrix.org`. |
| `user_id` | string | Yes | Bot Matrix user ID, e.g. `@bot:matrix.org`. |
| `access_token_ref` | string | Yes | Credential store key for the bot access token. |
| `device_id` | string | No | Optional Matrix device ID; auto-fetched via `whoami` when E2EE is enabled and this field is empty. |
| `join_on_invite` | bool | No | Auto-join rooms when the bot receives an invite. Default: `true` (seeded by `SeedConfig`). |
| `allow_from` | []string | No | User allowlist (Matrix user IDs). Empty means all senders are allowed. |
| `group_trigger` | object | No | Group-chat trigger rules — `mention_only` (bool) and/or `prefixes` ([]string). |
| `placeholder` | object | No | Placeholder message config (see below). |
| `reasoning_channel_id` | string | No | Room ID to route reasoning/thinking output to a separate conversation. |
| `message_format` | string | No | Output format: `"richtext"` (default) renders markdown as HTML; `"plain"` sends plain text. |
| `crypto_database_path` | string | No | Directory for the E2EE SQLite database. Defaults to `~/.omnipus/workspace/matrix` when empty. |
| `crypto_passphrase_ref` | string | No | Credential store key for the E2EE session pickle passphrase. Must not change once set. Leave empty to disable E2EE. |

### Placeholder config

| Field | Type | Description |
|---|---|---|
| `enabled` | bool | Send a placeholder message before the final reply. Default: `false`. |
| `text` | string or []string | Placeholder text. Multiple values: one is chosen at random. Default: `"Thinking..."`. |

## Setup

1. Register a Matrix account for your bot on any homeserver (e.g. `matrix.org`).
2. Generate an access token via the `/_matrix/client/v3/login` API or a Matrix client.
3. Store the token in the Omnipus credential store:
   ```bash
   omnipus credentials set matrix_access_token <token>
   ```
4. Set `access_token_ref: "matrix_access_token"` in `config.json`.
5. (Optional) To enable E2EE, also store a passphrase:
   ```bash
   omnipus credentials set matrix_crypto_passphrase <passphrase>
   ```
   Set `crypto_passphrase_ref: "matrix_crypto_passphrase"`. The SQLite database will be created at `crypto_database_path` on first start.
6. Build with `-tags goolm` and start the gateway.

## Capabilities

- Text send/receive with configurable markdown-to-HTML rendering (`richtext`) or plain text
- Incoming image, audio, video, and file download into the media store
- Outgoing image, audio, video, and file upload via Matrix media API
- Encrypted media attachments (decrypted transparently when E2EE is active)
- Group trigger rules: `mention_only` and/or `prefixes`
- Typing indicator (`m.typing`) with automatic refresh
- Placeholder message with in-place edit on final reply (`MessageEditor`)
- Auto-join invited rooms (`join_on_invite`)
- End-to-end encryption via `goolm` + `cryptohelper` (opt-in via `crypto_passphrase_ref`)

For deeper details on how channels are orchestrated, see [pkg/channels/README.md](../../../pkg/channels/README.md).
