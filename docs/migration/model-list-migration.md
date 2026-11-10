# Provider and model configuration

Configure providers in **Settings → Providers**. Use [Providers and models](../providers-and-models.md) for the setup steps and the [provider reference](../reference/providers-and-models.md) for the shipped catalog. This page summarizes the current configuration format.

## Choose an exact provider and model

Each entry in `providers` names an exact provider ID and a model ID offered by that provider. The model has no transport-protocol prefix: a slash inside a model ID is part of that ID. The installation's default model is the `provider` and `model` pair in `agents.defaults.default_model`.

```json
{
  "providers": [
    {
      "provider": "openrouter",
      "model": "z-ai/glm-5.2",
      "api_key_ref": "OPENROUTER_API_KEY"
    }
  ],
  "agents": {
    "defaults": {
      "default_model": {
        "provider": "openrouter",
        "model": "z-ai/glm-5.2"
      }
    }
  }
}
```

`api_key_ref` names a credential saved in the encrypted Credential Vault, not the key itself. Add it through **Settings → Security → Credential Vault**. Never put a plaintext key in the configuration.

## Endpoint and authentication choices

| Field | Meaning |
|---|---|
| `provider` | Exact catalog ID or your custom endpoint's ID |
| `model` | Exact model ID sent to the provider |
| `protocol` | Selects an offered secondary endpoint; a custom endpoint requires `openai-compatible` or `anthropic` |
| `api_base` | Overrides the endpoint address; required for a custom endpoint |
| `api_key_ref` | Name of the encrypted stored credential |
| `auth_method` | `api_key` or `sign_in`; sign-in works only where Omnipus offers an actual sign-in mechanism |

A custom endpoint needs both `api_base` and `protocol`. A display label in `name` does not change routing. Use the exact provider/model pair, not a `model_name` alias.

External command-line agents are [workers](../agents.md#workers-and-delegation), not model providers. Choose an actual provider for native Omnipus agents; use an external worker when you want to delegate to a command-line agent.
