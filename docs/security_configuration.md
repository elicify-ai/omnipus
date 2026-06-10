# Security Configuration

## Overview

Omnipus does not maintain a separate `.security.yml` file. The earlier
PicoClaw-style `.security.yml` credential-separation mechanism has been
**removed** — `pkg/config/security.go:22-27` carries a comment to that effect.
Sensitive values are no longer stored in a parallel YAML file; they are
encrypted at rest in a single JSON store and referenced by name from
`config.json`. This page documents the current model and points at
[credential_encryption.md](credential_encryption.md) for the full story.

The shape of the current model:

- **`config.json`** — non-sensitive configuration: model names, channel
  enablement, routing bindings, sandbox policy, etc. Safe to share and
  (optionally) commit to version control.
- **`credentials.json`** — encrypted (AES-256-GCM) JSON store of every secret
  the gateway needs. Never shared; mode `0600`; backed by a master key in
  `$OMNIPUS_HOME/master.key` (or `OMNIPUS_MASTER_KEY` / `OMNIPUS_KEY_FILE`).
- **`config.json` `*_ref` fields** — a string that names an entry in
  `credentials.json`. The gateway decrypts the named entry at boot via
  `credentials.ResolveBundle` and threads the plaintext into the subsystem
  that needs it (model providers, channels, web tools, skills).

## File structure

```
~/.omnipus/
├── config.json         # Main configuration (safe to share)
├── credentials.json    # Encrypted secret store (mode 0600; never share)
└── master.key          # 256-bit master key, mode 0600; back this up
```

## How it works

1. Operators run `omnipus credentials set <name> <value>` (or use the SPA at
   **Settings → Security → Credential Vault**) to write a secret into
   `credentials.json`. The store auto-generates `$OMNIPUS_HOME/master.key`
   on a fresh install and prints a backup banner.
2. Operators edit `config.json` and reference the secret by name through an
   `*_ref` field. For example, an OpenRouter provider entry looks like:
   ```json
   {
     "providers": [
       {
         "model_name": "fast",
         "provider": "openrouter",
         "model": "z-ai/glm-5v-turbo",
         "api_key_ref": "openrouter_api_key"
       }
     ],
     "agents": { "defaults": { "model_name": "fast" } }
   }
   ```
3. At boot, the gateway calls `credentials.ResolveBundle(cfg, store)`
   (`pkg/credentials/bundle.go:35-39`) which walks every `*Ref` field on the
   config, decrypts the named entry, and bundles the plaintexts for
   consumers. The same plaintexts are pushed into
   `Config.RegisterSensitiveValues(...)` (`pkg/gateway/rest.go:2214`) so the
   sensitive-data redaction filter (see
   [sensitive_data_filtering.md](sensitive_data_filtering.md)) can substitute
   them with `[FILTERED]` if they ever appear in tool output.
4. On credential rotation, the operator runs
   `omnipus credentials rotate` (or sets a new `OMNIPUS_MASTER_KEY` and
   rotates the entries). The store re-encrypts every entry under the new key.

## Supported `*_ref` fields

The full list of fields resolved by `ResolveBundle` lives in
`pkg/credentials/bundle.go` and is exhaustively asserted by
`pkg/credentials/bundle_test.go::TestResolveBundle_AllChannelRefs`. The
most-used fields:

| Section | Field(s) | Conventional credential name |
|---|---|---|
| `providers[]` | `api_key_ref` | `openrouter_api_key`, `anthropic_api_key`, `glm_api_key`, … |
| `channels.telegram` | `token_ref` | `telegram_bot_token` |
| `channels.discord` | `token_ref` | `discord_bot_token` |
| `channels.slack` | `bot_token_ref`, `app_token_ref` | `slack_bot_token`, `slack_app_token` |
| `channels.feishu` | `app_secret_ref`, `encrypt_key_ref`, `verification_token_ref` | `feishu_app_secret`, `feishu_encrypt_key`, `feishu_verification_token` |
| `channels.matrix` | `access_token_ref` | `matrix_access_token` |
| `channels.line` | `channel_secret_ref`, `channel_access_token_ref` | `line_channel_secret`, `line_channel_access_token` |
| `tools.web.brave` | `api_key_ref` | `brave_api_key` |
| `tools.web.tavily` | `api_key_ref` | `tavily_api_key` |
| `tools.skills.github` | `token_ref` | `github_token` |
| `tools.skills.registries.clawhub` | `auth_token_ref` | `clawhub_auth_token` |
| `voice` | `elevenlabs_api_key_ref` | `elevenlabs_api_key` |

There is no environment-variable path-based override for any of these
channels or providers (e.g. no `OMNIPUS_CHANNELS_TELEGRAM_TOKEN` path
override). To inject a value without writing it to disk, set the credential
in `credentials.json` (encrypted) and use the `*_ref` indirection. Per-field
`env:` struct tags are honored for *some* non-secret overrides (see
`pkg/config/config.go` for the canonical list), but secret fields only accept
plaintext in `config.json` or a credential reference through `*_ref`.

## Environment variables

The only environment variables that govern the credential subsystem itself
are the master-key sources documented in
[credential_encryption.md](credential_encryption.md):

| Variable | Purpose |
|---|---|
| `OMNIPUS_MASTER_KEY` | 64-char hex master key (256 bits). Used directly. |
| `OMNIPUS_KEY_FILE` | Path to a `0600` file containing a 64-char hex master key. |

There is no environment-variable override that bypasses `*_ref` indirection
to inject a plaintext secret into a channel or provider. This is by design.

## Security best practices

### Back up `master.key`

`$OMNIPUS_HOME/master.key` (or the env-var / key-file source that supplied
it) is the only thing that decrypts `credentials.json`. Losing it loses every
secret in the store. The auto-generate banner on first boot is intentionally
loud: read it.

### Set restrictive permissions

The credential store is written `0600` by construction; the master key file
is checked at `0600` and refused at any other mode. Do not weaken the home
directory's own permissions either: `$OMNIPUS_HOME` should be `0700` per
BRD SEC-27.

### Use one credential per provider

A single credential store entry is keyed by a unique name (e.g.
`openrouter_api_key`). Avoid reusing the same name across providers — the
bundle resolver hands the same plaintext to every `*_ref` that names it,
which is usually wrong (and may produce a confusing 401 on a provider that
didn't actually issue the key).

### Rotate regularly

Run `omnipus credentials rotate` to re-encrypt every entry under a new master
key. Update `OMNIPUS_MASTER_KEY` or copy the new `master.key` to all
deployments before revoking the old key. If you need to change the value of
a single credential, run `omnipus credentials set <name> <new-value>` — the
store re-encrypts that entry in place without touching the others.

### Never commit `credentials.json` or `master.key`

Both files are secrets. Add them to `.gitignore`:

```
credentials.json
master.key
```

## Removed from earlier designs

The following were part of an earlier `.security.yml` draft and are **not**
present in the current implementation:

- `.security.yml` as a separate file (replaced by `credentials.json`).
- `loadSecurityConfig`, `saveSecurityConfig`, `applySecurityConfig`,
  `securityPath` functions (do not exist in `pkg/config`).
- `load_security_config`, `save_security_config` Go API symbols.
- `OMNIPUS_CHANNELS_TELEGRAM_TOKEN` path-based env override
  (no such override exists; the only override is `OMNIPUS_MASTER_KEY` /
  `OMNIPUS_KEY_FILE` for the master key itself).
- `security.example.yml` (no template ships with the current release).

## See also

[credential_encryption.md](credential_encryption.md) — the master-key model,
cipher parameters, and on-disk format.

[sensitive_data_filtering.md](sensitive_data_filtering.md) — the in-process
redaction filter that consumes the resolved credential plaintexts.
