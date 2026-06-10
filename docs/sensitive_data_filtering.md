# Sensitive Data Filtering

Omnipus can filter sensitive values (API keys, tokens, secrets, passwords) from tool call results before they are sent to the LLM. This prevents the LLM from seeing its own credentials, which could otherwise leak through tool output or cause confusing behavior.

---

## Overview

When the LLM uses a tool that returns its own credentials (e.g., a tool that echoes the API key being used), those values are automatically replaced with `[FILTERED]` in the message sent to the LLM.

Sensitive values are collected at boot by the credential bundle resolver
(`pkg/credentials/bundle.go::ResolveBundle`) and registered with the config
sensitive-data cache via `Config.RegisterSensitiveValues`
(`pkg/config/security.go:69-77`). The cache is built from two sources:

1. **Reflection-walked `SecureString` fields on `Config`** — `collectSensitive`
   walks the loaded config struct and pulls the plaintext value out of every
   `*SecureString` it finds. This is the historical path and is preserved for
   backward compatibility.
2. **Runtime-registered plaintexts** — the bundle resolver iterates every
   `*_ref` field on the config (model API keys, channel tokens, web-tool API
   keys, skill tokens) and decrypts the corresponding entry from
   `credentials.json`. The resolved plaintexts are then pushed into the cache
   via `newCfg.RegisterSensitiveValues(values)` at `pkg/gateway/rest.go:2214`.

The compiled cache is a `strings.Replacer` (O(n+m) string substitution)
rebuilt lazily on the first filter call and atomically swapped whenever
`RegisterSensitiveValues` is called again (e.g. after a credential rotate).

The earlier `.security.yml` credential-separation mechanism (legacy PicoClaw
design) has been **removed** — `pkg/config/security.go:22-27` carries a comment
to that effect. Credential separation is now handled exclusively by
`credentials.json` and the `*_ref` indirection; see
[credential_encryption.md](credential_encryption.md).

---

## Configuration

Sensitive data filtering is configured in the `tools` section of `config.json`:

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `filter_sensitive_data` | bool | `true` | Enable/disable filtering. When `false`, no filtering is performed. |
| `filter_min_length` | int | `8` | Minimum content length to trigger filtering. Short content is skipped for performance. |

```json
{
  "tools": {
    "filter_sensitive_data": true,
    "filter_min_length": 8
  }
}
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `OMNIPUS_TOOLS_FILTER_SENSITIVE_DATA` | Set to `true` or `false` to override the config value |
| `OMNIPUS_TOOLS_FILTER_MIN_LENGTH` | Override `filter_min_length`. Defined in `pkg/config/config.go:1438` as the `env` tag on `ToolsConfig.FilterMinLength`; default is 8 when unset or non-positive (`GetFilterMinLength`, `pkg/config/config.go:1541-1545`). |

---

## How It Works

On every config reload, the credential bundle resolver decrypts every `*_ref`
entry, then `Config.RegisterSensitiveValues(values)` replaces the runtime
sensitive-data list and resets the compiled `strings.Replacer` cache. The next
`SensitiveDataReplacer()` call rebuilds the cache with the new set.

Before sending any tool result content to the LLM, the filter checks three
conditions in order (`pkg/config/config.go:306-313`): if
`filter_sensitive_data` is `false`, content is passed through unchanged; if
content length is less than `filter_min_length`, content is passed through
unchanged (fast path); otherwise, all sensitive values are replaced with
`[FILTERED]`.

Replacement uses `strings.Replacer` for efficient O(n+m) string substitution,
where n = content length and m = total sensitive value length. Values shorter
than 4 characters are dropped from the replacement table to avoid replacing
unrelated short strings (e.g. common substrings); see
`pkg/config/security.go:97-101`.

---

## Example

Given a credential store with:

```bash
omnipus credentials set openrouter_api_key sk-secret-key-12345
omnipus credentials set telegram_bot_token 123456:ABC-DEF
```

and a `config.json` that references them via:

```json
{
  "providers": [{ "model_name": "fast", "provider": "openrouter",
                  "model": "openai/gpt-4o", "api_key_ref": "openrouter_api_key" }],
  "channels":  { "telegram": { "enabled": true, "token_ref": "telegram_bot_token" } }
}
```

A tool result containing:

```
The model is using API key sk-secret-key-12345 and Telegram bot 123456:ABC-DEF
```

is rewritten to the LLM as:

```
The model is using API key [FILTERED] and Telegram bot [FILTERED]
```

---

## Performance

Content shorter than `filter_min_length` (default 8) is returned unchanged
without any string scanning. The `strings.Replacer` approach achieves O(n+m)
complexity instead of regex, avoiding repeated scans. The replacement map is
rebuilt only when the set of registered values changes (i.e. on a credential
rotate or a config reload), not on every tool call.

---

## Security Considerations

### Credential Exposure Prevention

Without filtering, tools that echo credentials could cause the LLM to see its
own API keys, potentially leading to confusion or credential leakage in logs.

### Defense in Depth

Filtering complements (but does not replace) credential encryption — both
features should be used together. Encryption protects the secrets at rest;
filtering protects them from being surfaced to the LLM in tool output.

### No False Positives

Only values explicitly registered via the credential bundle or stored as
`SecureString` fields on the loaded config are filtered; the LLM's general
knowledge is unaffected.

---

## Related

[Credential Encryption](credential_encryption.md) — encrypting API keys at rest
in `credentials.json`.

[Tools Configuration](tools_configuration.md).


