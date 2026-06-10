# Credential Encryption

> **Note — replaces earlier designs.** This document supersedes earlier drafts that
> described an SSH-key-as-second-factor scheme (passphrase + Ed25519 SSH key,
> HKDF-SHA256 KDF, `enc://<base64>` wire format, 16-byte salt + 12-byte nonce
> concatenated and base64-wrapped). Omnipus no longer ships that design. The
> current implementation uses an AES-256-GCM encrypted JSON store keyed by a
> single 256-bit master key, derived from one of: a hex env var, a 0600 key file,
> an auto-generated file on first boot, or an interactive passphrase prompt.
>
> This document describes what is actually in `pkg/credentials/`.

## Overview

Omnipus stores secrets (model API keys, channel tokens, webhook credentials,
skill tokens) in a single encrypted JSON file at `~/.omnipus/credentials.json`.
The file is encrypted with AES-256-GCM using a 256-bit master key. The master
key is provisioned at boot from one of the sources below; whichever source is
used at boot is the same source that must be presented on every subsequent boot
to decrypt the file.

## Key provisioning (boot unlock priority)

`pkg/credentials/keymgr.go::Unlock` tries the following sources, in order, and
stops at the first one that succeeds:

| Priority | Source | How it's used |
|---|---|---|
| 1 | `OMNIPUS_MASTER_KEY` env var | A 64-character hex string (256 bits). Decoded and used directly as the AES-256 key. No KDF. |
| 2 | `OMNIPUS_KEY_FILE` env var | Path to a file containing a 64-character hex key. The file must exist with mode `0600` (owner read+write only); a stricter check refuses to load a key file with any other permissions and emits a `credentials.master_key_load` audit event with `decision: deny`. |
| 3 | Default key file at `$OMNIPUS_HOME/master.key` | Same strict-0600 check as (2). Used when no env var is set but the key file exists. |
| 4 | Auto-generate on a truly fresh install | Only fires when no env var, no key file, and no existing `credentials.json` are present. Writes a fresh 256-bit key (hex-encoded) to `$OMNIPUS_HOME/master.key` with mode `0600` (atomic `O_EXCL` create so two concurrent boots cannot clobber each other), and prints a prominent backup banner to stdout: `"BACK THIS FILE UP. Losing it makes your encrypted credential store (API keys, channel tokens) permanently inaccessible."` |
| 5 | Interactive passphrase prompt | Used only when no env-var, no key file, and no auto-generate path is available. Requires a TTY. The passphrase is run through Argon2id to derive a 256-bit AES key. |

If none of (1)–(5) succeeds and no TTY is available, `Unlock` returns an error
and the gateway refuses to boot.

## Cipher

| Property | Value | Source |
|---|---|---|
| Symmetric cipher | AES-256-GCM | `pkg/credentials/store.go:450-468` (`encrypt`) |
| Key length | 32 bytes (256 bits) | `store.go:51` (`keyLen = 32`) |
| Nonce length | 12 bytes, random per encryption | `store.go:50` (`nonceLen = 12`) |
| Auth tag | 16-byte GCM tag appended to ciphertext | standard AES-GCM behavior |
| KDF (passphrase mode only) | Argon2id, time=3, memory=64 MiB, parallelism=4, keyLen=32 | `store.go:54-56`, `store.go:206-208` (`DeriveKeyFromPassphrase`) |
| KDF (env-var / key-file mode) | None — the key bytes are the AES key directly | `store.go:121-130` (`UnlockWithKey`) |
| Salt | 32 bytes random, base64-encoded, stored in `credentials.json` | `store.go:49` (`saltLen = 32`); only used by the passphrase path |

## On-disk format (`credentials.json`)

```json
{
  "version": 1,
  "salt": "<base64, 32 random bytes>",
  "credentials": {
    "openrouter_api_key": {
      "nonce": "<base64, 12 random bytes>",
      "ciphertext": "<base64, AES-256-GCM(plaintext) including 16-byte auth tag>"
    },
    "telegram_bot_token": {
      "nonce": "...",
      "ciphertext": "..."
    }
  }
}
```

Schema details:

- `version` is always `1` (constant `storeVersion` in `pkg/credentials/store.go:48`).
- `salt` is used only when the store is unlocked with a passphrase (Argon2id input). When unlocked from `OMNIPUS_MASTER_KEY` or a key file, the salt is not used for key derivation; it is still persisted so the same file can be re-encrypted under a passphrase later via `RotateWithPassphrase`.
- Each entry under `credentials` is keyed by a name (e.g. `openrouter_api_key`). The Go type is `pkg/credentials/store.go:81-84`:
  ```go
  type encEntry struct {
      Nonce      string `json:"nonce"`
      Ciphertext string `json:"ciphertext"`
  }
  ```
- The on-disk file is written with mode `0600` (`store.go:402-404`) and is also protected by an advisory OS-level `flock` for cross-process defense in depth.
- A successful read requires the AES-GCM authentication tag to verify; a wrong key returns `ErrWrongKey` (`store.go:65`).

## How config fields reference credentials

Omnipus config files (`config.json`) never contain plaintext secrets for
production-shaped fields. Each secret-bearing field has a paired `*_ref` field
that holds the **name** of the credential in `credentials.json`. The
`pkg/credentials/bundle.go::ResolveBundle` function walks every `*Ref` field on
the loaded config, decrypts the corresponding entry, and exposes the resolved
value to the subsystem that needs it.

Examples (see `pkg/credentials/bundle_test.go` for the full list):

| Config field | Credential name convention |
|---|---|
| `providers[].api_key_ref` | `openrouter_api_key`, `anthropic_api_key`, … |
| `channels.telegram.token_ref` | `telegram_bot_token` |
| `channels.slack.bot_token_ref` / `app_token_ref` | `slack_bot_token`, `slack_app_token` |
| `channels.feishu.{app_secret,encrypt_key,verification_token}_ref` | `feishu_app_secret`, `feishu_encrypt_key`, `feishu_verification_token` |
| `channels.matrix.access_token_ref` | `matrix_access_token` |
| `channels.line.{channel_secret,channel_access_token}_ref` | `line_channel_secret`, `line_channel_access_token` |
| `tools.web.brave.api_key_ref` | `brave_api_key` |
| `tools.web.tavily.api_key_ref` | `tavily_api_key` |
| `tools.skills.github.token_ref` | `github_token` |
| `tools.skills.registries.clawhub.auth_token_ref` | `clawhub_auth_token` |
| `voice.elevenlabs_api_key_ref` | `elevenlabs_api_key` |

The CLI command `omnipus credentials set <name> <value>` writes the named
credential; the SPA at **Settings → Security → Credential Vault** edits the
same store. To set a value without an `*_ref` indirection, edit
`credentials.json` directly with the same `<name>` and AES-GCM nonce+ciphertext
pair shown above.

## Migration

The only secret material that must move with the data directory is
`$OMNIPUS_HOME/master.key` (or, alternatively, the value of
`OMNIPUS_MASTER_KEY` / `OMNIPUS_KEY_FILE` for the new host). Copy
`credentials.json` alongside it. No re-encryption is needed — the ciphertext
is portable as long as the key is the same.

## Security considerations

### Master key backup is mandatory

Losing `master.key` (or the `OMNIPUS_MASTER_KEY` / `OMNIPUS_KEY_FILE` source)
makes every entry in `credentials.json` permanently inaccessible. There is no
recovery path; AES-GCM authentication failures return `ErrWrongKey` and the
gateway will refuse to boot. Back the key file up out-of-band.

### Master key file mode is enforced

`loadKeyFile` (`pkg/credentials/keymgr.go:239-292`) refuses to read a key file
whose mode is anything other than `0600` (any bit outside `0o600`, including
group-read `0o640` or world-readable `0o644`, is a denial). Symlinks are
followed: the **target** must be a regular file with `0600`; the symlink's own
permission bits (typically `0o777` on Linux) are ignored. Every load attempt
(success or failure) is recorded as a `credentials.master_key_load` audit event
so an attacker who provisioned a `0644` `master.key` to bypass enforcement
leaves a loud audit footprint on every boot.

### Ciphertext tampering is detected

AES-GCM authentication ensures any modification to either the nonce or the
ciphertext bytes causes `decrypt` to fail with `ErrWrongKey`. There is no
"silently return corrupt plaintext" path.

### Plaintext credentials are still supported

Existing `config.json` files that hold plaintext in a `token` / `api_key`
field continue to work — the `*_ref` indirection is the recommended shape but
not enforced at the schema level. Migrate by running
`omnipus credentials set <name> <value>` for each plaintext secret and
replacing the field with `<name>_ref: "<name>"`.

## Removed from earlier designs

The following were part of an earlier draft and are **not** present in the
current implementation:

- `OMNIPUS_KEY_PASSPHRASE` (replaced by the file/env unlock modes).
- `OMNIPUS_SSH_KEY_PATH` and the auto-detected `~/.ssh/omnipus_ed25519.key`
  (no SSH-key second factor).
- `enc://<base64>` wire format (credentials live in `credentials.json`, not
  inline in `config.json`).
- HKDF-SHA256 key derivation (replaced by direct key bytes in env/file mode,
  Argon2id in passphrase mode only).
- 16-byte salt + 12-byte nonce + ciphertext concatenated and base64-wrapped
  wire format (replaced by JSON entries with separate `nonce` and `ciphertext`
  base64 fields).
