# Providers and models

Providers connect Omnipus to the services that run its models. This page helps you connect one, choose a tool-capable model, and plan for provider failures.

## What it is

A provider supplies access to one or more models. Omnipus ships with a catalog of supported providers and their known models. The live catalog appears when you open **Settings → Providers**, select **Connect a provider**, and choose a provider.

The catalog includes cloud services, local model servers, command-line providers, and custom endpoints. These are the main choices.

| Provider choice | How you connect | What to expect |
|---|---|---|
| Popular cloud providers | Enter an application programming interface (API) key or use **Sign in** when offered | The catalog supplies the known model list |
| Other catalog providers | Search under **All providers** and enter the requested credential | Unsupported entries are disabled with a reason |
| Ollama and other local providers | Select the local provider and confirm its endpoint | Omnipus asks the local server which models are installed |
| Command-line providers | Use **Sign in** with the provider's command-line tool | Omnipus uses that tool's signed-in session |
| Custom endpoint | Choose **Custom endpoint** and provide an ID, base URL, protocol, and API key | You add model names yourself when no live list is available |

## When you would use it

Connect a provider before assigning one of its models to an [agent](agents.md). Add another provider when you want a different model family, a local option, or a fallback if the first service is unavailable.

Use a fallback chain when an agent must keep working through temporary failures. A fallback chain is an ordered list: Omnipus tries the main model first, then each fallback in the order you set.

## How to add a provider

1. Open **Settings**, then select **Providers**.
2. Select **Connect a provider**. You see popular choices, recent choices, search, **All providers**, and **Custom endpoint**.
3. Choose a provider. If it has variants, choose the plan, region, and authentication method shown.
4. Enter the API key, or select **Sign in** when the provider offers it.
5. Select **Continue**, then complete the connection step. The provider appears in your configured provider list.
6. Choose a model in the **Default model** card, or open an agent and choose its model there.

For a custom endpoint, choose either the `openai-compatible` or `anthropic` protocol. Enter the exact model names that the endpoint accepts. Omnipus cannot discover a custom endpoint's catalog for you.

## Models known to support tools

Omnipus sends tool definitions with every model request. A model without tool use fails instead of continuing as a text-only model.

The following short list contains examples marked as tool-capable in the shipped catalog. Model availability still depends on your provider and account.

| Provider | Tool-capable model | Catalog model name |
|---|---|---|
| Anthropic | Claude Haiku 4.5 | `claude-haiku-4-5` |
| Anthropic | Claude Sonnet 4.6 | `claude-sonnet-4-6` |
| DeepSeek | DeepSeek Chat | `deepseek-chat` |
| Google | Gemini 2.5 Flash | `gemini-2.5-flash` |
| Mistral | Mistral Large | `mistral-large-latest` |
| OpenAI | GPT-4.1 | `gpt-4.1` |
| OpenAI | GPT-5 | `gpt-5` |
| xAI | Grok 4.3 | `grok-4.3` |

For any other model, check the provider's documentation for **tool use**, **tool calling**, or **function calling** for that exact model name. Do not assume every model from a supported provider can use tools. The model picker shows the live list but does not currently show the catalog's tool-use marker.

Some providers cannot be enumerated from a published catalog. Ollama and other local providers depend on what is installed on your machine, so Omnipus requests their live list. A custom endpoint has no catalog to enumerate; you maintain its model names under **Models** in that provider's settings.

## Limits and things to watch

A fallback is not used for every error. Omnipus moves to the next model after temporary failures such as timeouts, connection drops, overload, or rate limits. It skips a model that is cooling down after recent failures. A successful request clears that model's cooldown.

Authentication and billing failures also move to the next model. Invalid request format, context overflow, image-size errors, and unrecognized errors stop the chain. Canceling a request also stops it. If every eligible model fails or is cooling down, the whole request fails with the recorded attempts.

You set fallbacks per agent. Open the agent's **Identity** settings and use **Fallback models**. Their order matters. A fallback should use a tool-capable model and a provider with valid credentials; otherwise it cannot rescue the request.

The catalog changes over time. Use the provider and model lists in **Settings → Providers** as the live list. Treat the examples on this page as a starting point, not a complete inventory.

## Related pages

- [Agents](agents.md) — choose the main model and fallback models for each agent.
- [Settings](settings.md) — find the Providers tab and other installation controls.
- [Tools](tools.md) — understand the tools sent with every model request.
- [Troubleshooting](troubleshooting.md) — diagnose connection and credential failures.
