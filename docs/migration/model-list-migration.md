# Migration Guide: From `providers` (v0) to `providers` Array (v1)

This guide explains how to migrate from the legacy keyed-object `providers` config
(v0) to the current array-of-models `providers` format (v1), and documents the
complete `ModelConfig` field reference.

## Why Migrate?

The v1 `providers` array is model-centric rather than vendor-centric:

- **Zero-code provider addition** — any OpenAI-compatible endpoint works with
  `openai/` prefix and an `api_base` URL, no code changes needed.
- **Load balancing** — duplicate `model_name` entries trigger automatic
  round-robin selection across their endpoints.
- **Explicit per-model configuration** — each entry carries its own
  `api_base`, `proxy`, `thinking_level`, `extra_body`, etc. rather than
  sharing a single provider block.
- **Credential store integration** — API keys live in `credentials.json`
  (AES-256-GCM encrypted) and are referenced via `api_key_ref`, never stored
  as plaintext.

## Migration happens automatically at boot

When Omnipus loads a config with `version` absent or `0`, the loader:

1. Detects the old `providers` keyed-object format.
2. Converts each non-empty provider block to a `ModelConfig` entry.
3. Writes the result into the in-memory v1 schema.

**No manual config edit is required for the initial migration.** The converted
config is used for the current run. If you want to persist the migrated form,
save it via the REST API (`PUT /api/v1/config`) or let the gateway write it
on the next config-save event.

Additionally, the loader accepts both `"model_list"` and `"providers"` as the
top-level JSON key — old configs that use `"model_list"` are silently renamed
at load time. The canonical key written by `SaveConfig` is `"providers"`.

## Credential migration

If the v0 config contained plaintext `api_key` values, the migration code
moves them into `credentials.json` (requires `OMNIPUS_MASTER_KEY` to be set)
and replaces each entry with an `api_key_ref` pointer. See
[ADR-004](../internal/architecture/ADR-004-credential-boot-contract.md) for the full
credential boot contract.

## Before: Legacy v0 `providers` (keyed object)

```json
{
  "providers": {
    "openai": {
      "api_key": "sk-your-openai-key",
      "api_base": "https://api.openai.com/v1"
    },
    "anthropic": {
      "api_key": "sk-ant-your-key"
    },
    "deepseek": {
      "api_key": "sk-your-deepseek-key"
    }
  },
  "agents": {
    "defaults": {
      "provider": "openai",
      "model": "gpt-4o"
    }
  }
}
```

## After: Current v1 `providers` (array)

```json
{
  "version": 1,
  "providers": [
    {
      "model_name": "gpt4",
      "model": "openai/gpt-4o",
      "api_base": "https://api.openai.com/v1",
      "api_key_ref": "OPENAI_API_KEY"
    },
    {
      "model_name": "claude-sonnet",
      "model": "anthropic/claude-sonnet-4.6",
      "api_key_ref": "ANTHROPIC_API_KEY"
    },
    {
      "model_name": "deepseek",
      "model": "deepseek/deepseek-chat",
      "api_key_ref": "DEEPSEEK_API_KEY"
    }
  ],
  "agents": {
    "defaults": {
      "model_name": "gpt4"
    }
  }
}
```

Note: `api_key_ref` names a credential stored in `credentials.json`. Use
`omnipus credentials set OPENAI_API_KEY sk-your-key` (or the REST API) to
populate it.

## Protocol Prefixes

The `model` field uses `[protocol/]model-identifier` format. The protocol
determines which HTTP client, auth flow, or CLI shim is used.

### First-party protocols (non-HTTP)

| Prefix | Description |
|--------|-------------|
| `anthropic/` | Anthropic API (api-key auth). Default base: `https://api.anthropic.com/v1` |
| `anthropic-messages/` | Anthropic Messages API (native format, no OpenAI shim) |
| `azure/` or `azure-openai/` | Azure OpenAI; `api_base` and `api_key_ref` required |
| `bedrock/` | AWS Bedrock; credentials from AWS SDK env/profile/IAM |
| `claude-cli/` or `claudecli/` | Claude via local CLI binary |
| `codex-cli/` or `codexcli/` | Codex via local CLI binary |

### OpenAI-compatible protocols

All of the following use the OpenAI chat-completions wire format. An `api_base`
is required unless a default is listed.

| Prefix | Default `api_base` |
|--------|--------------------|
| `openai/` | `https://api.openai.com/v1` |
| `openrouter/` | `https://openrouter.ai/api/v1` |
| `litellm/` | `http://localhost:4000/v1` |
| `groq/` | `https://api.groq.com/openai/v1` |
| `gemini/` or `google/` | `https://generativelanguage.googleapis.com/v1beta/openai` |
| `zhipu/` | `https://open.bigmodel.cn/api/paas/v4` |
| `nvidia/` | `https://integrate.api.nvidia.com/v1` |
| `ollama/` | `http://localhost:11434/v1` |
| `moonshot/` | `https://api.moonshot.ai/v1` |
| `deepseek/` | `https://api.deepseek.com/v1` |
| `cerebras/` | `https://api.cerebras.ai/v1` |
| `qwen/` | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| `qwen-intl/` | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` |
| `qwen-international/` | (alias of `qwen-intl/`) |
| `dashscope-intl/` | (alias of `qwen-intl/`) |
| `qwen-us/` | `https://dashscope-us.aliyuncs.com/compatible-mode/v1` |
| `dashscope-us/` | (alias of `qwen-us/`) |
| `mistral/` | `https://api.mistral.ai/v1` |
| `volcengine/` | `https://ark.cn-beijing.volces.com/api/v3` |
| `vllm/` | `http://localhost:8000/v1` |
| `avian/` | `https://api.avian.io/v1` |
| `minimax/` | `https://api.minimax.io/v1` (auto-injects `reasoning_split: true`) |
| `longcat/` | `https://api.longcat.chat/openai` |
| `modelscope/` | `https://api-inference.modelscope.cn/v1` |
| `novita/` | `https://api.novita.ai/openai` |
| `shengsuanyun/` | `https://router.shengsuanyun.com/api/v1` |
| `vivgrid/` | `https://api.vivgrid.com/v1` |
| `coding-plan/` | `https://coding-intl.dashscope.aliyuncs.com/v1` |
| `alibaba-coding/` | (alias of `coding-plan/`) |
| `qwen-coding/` | (alias of `coding-plan/`) |
| `coding-plan-anthropic/` | Alibaba Coding Plan with Anthropic-compatible API |
| `alibaba-coding-anthropic/` | (alias of `coding-plan-anthropic/`) |
| `mimo/` | `https://api.xiaomimimo.com/v1` |

If no prefix is specified, `openai` is used as the default.

## `ModelConfig` Field Reference

All fields map directly to JSON tags in `pkg/config/config.go:ModelConfig`.

| Field | JSON tag | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Model name | `model_name` | string | Yes | User-facing alias; used in `agents.defaults.model_name` and `GetModelConfig()` lookups |
| Model | `model` | string | Yes | Protocol-prefixed model ID, e.g. `openai/gpt-4o` |
| Provider | `provider` | string | No | Routing-key override; when set, the loader uses this instead of the prefix in `model` |
| API base | `api_base` | string | No | Override the endpoint URL for HTTP-based protocols |
| API key ref | `api_key_ref` | string | No | Name of a credential in `credentials.json`; resolved at runtime via env var injection |
| Proxy | `proxy` | string | No | HTTP proxy URL for this entry |
| Fallbacks | `fallbacks` | []string | No | Ordered list of `model_name` aliases to try on failure |
| Auth method | `auth_method` | string | No | `oauth` or `token` for OAuth-based protocols |
| Workspace | `workspace` | string | No | Working directory for CLI-based providers (`claude-cli`, `codex-cli`) |
| RPM | `rpm` | int | No | Requests-per-minute cap for this entry; 0 = unlimited |
| Max tokens field | `max_tokens_field` | string | No | Override the field name sent for token limits (e.g. `max_completion_tokens`) |
| Request timeout | `request_timeout` | int | No | HTTP timeout in seconds; `0` uses the provider default (120 s) |
| Thinking level | `thinking_level` | string | No | Extended thinking depth: `off`, `low`, `medium`, `high`, `xhigh`, or `adaptive` |
| Extra body | `extra_body` | object | No | Additional key-value pairs injected verbatim into the request body |
| Name | `name` | string | No | Display-only alias for `model_name`; no effect on routing |

## Credential Storage

API keys are **never** stored as plaintext in `config.json`. Use the
credentials API or CLI to store them:

```bash
# CLI
omnipus credentials set OPENAI_API_KEY sk-your-key

# REST (with auth token)
curl -X POST http://localhost:18790/api/v1/credentials \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"key":"OPENAI_API_KEY","value":"sk-your-key"}'
```

Then reference the name in `api_key_ref`:

```json
{
  "model_name": "gpt4",
  "model": "openai/gpt-4o",
  "api_key_ref": "OPENAI_API_KEY"
}
```

## Load Balancing

Duplicate `model_name` entries in `providers` enable round-robin load
balancing. The selection counter is a global atomic integer; each call to
`GetModelConfig(name)` advances it and returns the next entry in the matched
set.

```json
{
  "providers": [
    {
      "model_name": "gpt4",
      "model": "openai/gpt-4o",
      "api_base": "https://api1.example.com/v1",
      "api_key_ref": "KEY_1"
    },
    {
      "model_name": "gpt4",
      "model": "openai/gpt-4o",
      "api_base": "https://api2.example.com/v1",
      "api_key_ref": "KEY_2"
    }
  ]
}
```

Requests for model `gpt4` alternate between `api1` and `api2`.

## Adding a New OpenAI-Compatible Provider

No code changes are required. Set `openai/` as the protocol and supply
`api_base` and `api_key_ref`:

```json
{
  "providers": [
    {
      "model_name": "my-llm",
      "model": "openai/my-model-v1",
      "api_base": "https://api.your-provider.com/v1",
      "api_key_ref": "MY_PROVIDER_API_KEY"
    }
  ]
}
```

## Migration Checklist

- [ ] Set `OMNIPUS_MASTER_KEY` (or `OMNIPUS_KEY_FILE`) before first boot so
      the auto-migration can write plaintext secrets into `credentials.json`.
- [ ] Start Omnipus once — the loader auto-migrates the v0 keyed-object
      `providers` block to the v1 array format in memory.
- [ ] Verify all models resolve: check `GET /api/v1/config` and confirm
      `providers` is an array with the expected entries.
- [ ] Persist the migrated config if desired (REST API or let the gateway write
      it on the next save event).
- [ ] Remove or comment out the old `providers` keyed-object from your
      `config.json` once the array form is in place.
- [ ] Update `agents.defaults.model_name` to reference a `model_name` from the
      new array (replaces the old `agents.defaults.provider` + `model` pair).

## Troubleshooting

### Model not found

```
model "xxx" not found in model_list or providers
```

Ensure the `model_name` in a `providers` entry matches the value in
`agents.defaults.model_name` (or the model name your code passes to
`GetModelConfig`).

### Unknown protocol

```
unknown protocol "xxx" in model "xxx/model-name"
```

Use a supported protocol prefix from the table above. The authoritative list
is the `knownProtocols` map in `pkg/providers/factory_provider.go`.

### Missing API key

```
api_key or api_base is required for HTTP-based protocol "xxx"
```

Set the credential with `omnipus credentials set NAME value` and add
`"api_key_ref": "NAME"` to the entry. For protocols that support it (e.g.
`ollama`, `vllm`), supplying only `api_base` to a local server is also
accepted.

### `api_key` field is dropped from v1

The `api_key` plaintext field exists only in the v0 `modelConfigV0` struct
(`pkg/config/config_old.go:496`) used during migration. The current v1
`ModelConfig` (`pkg/config/config.go:1067`) has **no** `api_key` JSON field
at all — there is no `omitempty` or sentinel entry. A key written at
`providers[i].api_key` is dropped (not just ignored) by the JSON decoder.
Use `api_key_ref` instead.

## Need Help?

- [GitHub Issues](https://github.com/elicify-ai/omnipus/issues)
