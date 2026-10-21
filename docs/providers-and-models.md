# Providers and models

Providers connect Omnipus to the services that run its models. This page helps you connect one, choose a tool-capable model, and plan for provider failures.

## What it is

A provider supplies access to one or more models. Omnipus ships with a catalog of supported providers and their known models. The live catalog appears when you open **Settings → Providers**, select **Connect a provider**, and choose a provider.

The catalog includes cloud services, local model servers, and custom endpoints. These are the main choices.

| Provider choice | How you connect | What to expect |
|---|---|---|
| Popular cloud providers | Enter an application programming interface (API) key or use **Sign in** when offered | The catalog supplies the known model list |
| ChatGPT (subscription) | Use **Sign in** with your OpenAI account | Uses your ChatGPT plan; this is where OpenAI sign-in lives |
| Other catalog providers | Search under **All providers** and enter the requested credential | Unsupported entries are disabled with a reason |
| Ollama and other local providers | Select the local provider and confirm its endpoint | Omnipus asks the local server which models are installed |
| Custom endpoint | Choose **Custom endpoint** and provide an ID, base URL, protocol, and API key | You add model names yourself when no live list is available |

## When you would use it

Connect a provider before assigning one of its models to an [agent](agents.md). Add another provider when you want a different model family, a local option, or a fallback if the first service is unavailable.

Use a fallback chain when an agent must keep working through temporary failures. A fallback chain is an ordered list: Omnipus tries the main model first, then each fallback in the order you set.

## How to add a provider

1. Open **Settings**, then select **Providers**.
2. Select **Connect a provider**. You see popular choices, recent choices, search, **All providers**, and **Custom endpoint**.
3. Choose a provider. If it has variants, choose the plan, region, and authentication method shown.
4. Enter the API key, or select **Sign in** when the provider offers it. Sign-in is offered only where Omnipus can actually sign you in. For OpenAI, that is the **ChatGPT (subscription)** provider; the plain **OpenAI** provider takes an API key only.
5. Select **Continue**, then complete the connection step. The provider appears in your configured provider list.
6. Choose a model in the **Default model** card, or open an agent and choose its model there.

For device-code sign-in, Omnipus shows the provider's verification link and a short code. Open the link and approve the code with the provider; Omnipus then checks for completion. The sign-in API identifies this flow with `method: "device_code"`.

**Omnipus's model.** Omnipus, the system agent that creates and changes your agents, uses a model like any agent. If none works, it cannot plan — fix it without a chat: open **Settings → System agents**, choose **Edit** on Omnipus and pick a model from a connected provider. Saving works even while no model works, because it is a settings change, not a chat with the model.

For a custom endpoint, choose either the `openai-compatible` or `anthropic` protocol. Enter the exact model names that the endpoint accepts. Omnipus cannot discover a custom endpoint's catalog for you.

## Picking a model when two providers offer it

The model pickers — next to the chat message box, on an agent's profile, in the new-agent wizard, and on the **Default model** card — list every model offered by your connected and signed-in providers, grouped under the provider that serves it.

The same model name can appear under more than one provider, for example when a local proxy serves models that a subscription provider also offers. When that happens, the check mark appears only under the provider whose row you actually picked — by mouse or by keyboard — and picking the same model under a different provider moves the check mark to that provider's row.

Two limits to know:

- A value restored from a saved setting or conversation rather than picked in this session carries no provider record (for example a recap model saved earlier, or the model a conversation was last answered with). When exactly one provider offers that model, its row is marked; when several providers offer it, no row is marked, because the UI cannot know which one served it.
- In the chat composer the provider choice is currently display-only: the message is sent with the model name only, and the server decides which provider serves it. The agent profile, the new-agent wizard, the **Default model** card, and fallback entries do save the chosen provider.

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

## What the provider check proves

When Omnipus configures a provider with `configure_provider`, it checks the saved entry straight away: the configured endpoint (your own address if you set one, otherwise the catalog's) is asked for its model list over that provider's protocol, with the saved key. A local provider such as Ollama is checked without a key. The result is `working` when the list comes back and `saved but not working` with the reason otherwise — a refused key, an unreachable address, or a protocol that has no model list. A passing check does not prove that a particular model runs, that you have quota, or that the model supports tools.

`configure_provider` also has a dry run (`dry_run`): it checks the provider id, the key's shape and the address's shape, saves nothing and contacts nobody.

`list_models` lists models through each configured provider's saved endpoint the same way. Asking it for a provider that is not configured is an error that says so, instead of an empty list. `list_providers` shows each provider's effective endpoint (`api_base`), protocol and the name of its saved key reference (`api_key_ref`, never the key), and `test_provider` accepts every provider `list_providers` shows.

## Limits and things to watch

A fallback is not used for every error. Omnipus moves to the next model after temporary failures such as timeouts, connection drops, overload, or rate limits. It skips a model that is cooling down after recent failures. A successful request clears that model's cooldown.

Authentication and billing failures also move to the next model. Invalid request format, context overflow, image-size errors, and unrecognized errors stop the chain. An HTTP 400 response that specifically says the model was not found or is not offered is a `bad_model` failure: it stops without trying another provider. Fix the model/provider pair in the agent's settings. Canceling a request also stops it. If every eligible model fails or is cooling down, the whole request fails with the recorded attempts.

A native conversation turn uses the selected provider's configured connection even if the agent has only one model candidate. It never substitutes the global-default connection for a missing named route. If the provider's saved credentials or endpoint settings cannot produce a usable connection, the turn stops before sending a request for that pair, with a `model_route_unavailable` notice naming the provider and model. Fix those settings, then send the request again. When several rows name the same pair, usable rows are tried in configured order; a missing credential reference or invalid endpoint does not override a working row. A light model keeps its own resolved connection, including when it shares a provider name with the main model but uses another endpoint. Cloud-model lookup also checks the full catalog, not just the saved model pick list.

A model name offered by more than one connected provider is ambiguous. For example, `gpt-5.4` is offered by both **OpenAI** and **ChatGPT (subscription)**. The chat's model picker currently sends only the model name. If you pick such a model there, Omnipus does not guess which account to bill. The switch fails, its error details read `model "gpt-5.4" is offered by several providers (openai, openai-chatgpt); choose the provider`, and the reply uses the agent's current model.

An agent whose model was saved without a provider is resolved differently when the agent starts. If its model name is ambiguous, it can start on the default provider instead of asking. Pin every agent's model to a provider: choose the model together with its provider in the agent's model setting.

When an agent's model was saved without a provider, its context window now comes from the provider the model actually runs on. Before, it came from a 128,000-token fallback. For example, `z-ai/glm-5.3` on OpenRouter now gets its catalog window of 1,048,576 tokens. An agent that runs an external command-line tool manages its own context, so it has no Omnipus context window.

You set fallbacks per agent. Open the agent's profile and use **Fallback models** on the **Basics** tab. Their order matters. A fallback should use a tool-capable model and a provider with valid credentials; otherwise it cannot rescue the request.

Every fallback is a provider and a model together. Saving a fallback without a provider is refused, and the error names the entry. Omnipus never guesses a provider for a fallback. A fallback saved without a provider before this change is not used: the agent runs on its main model and its complete fallbacks, and the server log names the incomplete entry each time the agent loads. In the agent's profile that fallback is marked **Incomplete — choose a provider or remove it**: pick its provider, or remove it. Until every fallback has a provider, other changes to the profile still save, but changes to the fallback list do not. The fallback picker only offers models listed by a connected provider, so a new fallback always has both.

**Where fallbacks come from.** Each agent has its own fallback list, set under **Fallback models** on its profile's **Basics** tab, each entry a provider and a model. An agent without fallbacks of its own runs on its main model only; nothing is inherited, and Omnipus never guesses a provider. A `config.json` key `agents.defaults.model_fallbacks` is not a setting: Omnipus starts normally, ignores it, and logs this warning once at load: `removed setting, ignored: agents.defaults.model_fallbacks — set fallbacks per agent as provider/model pairs`.

The catalog changes over time. Use the provider and model lists in **Settings → Providers** as the live list. Treat the examples on this page as a starting point, not a complete inventory.

## Related pages

- [Agents](agents.md) — choose the main model and fallback models for each agent.
- [Settings](settings.md) — find the Providers tab and other installation controls.
- [Tools](tools.md) — understand the tools sent with every model request.
- [Troubleshooting](troubleshooting.md) — diagnose connection and credential failures.
