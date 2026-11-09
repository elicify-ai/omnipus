# Connect a custom endpoint

This page is for a model service the setup wizard does not list. Azure OpenAI is the common example. Any service that speaks the OpenAI or Anthropic wire format at a fixed address works the same way: you connect it through the wizard's **Custom endpoint** row.

## What it is

The wizard's provider list comes from Omnipus's catalog of known providers. Azure OpenAI appears in the list, but its row is disabled — Azure needs a per-deployment URL, and no single catalog address can carry it. The **Custom endpoint** row is where you enter that address yourself. It takes four fields and one model name, and the endpoint it creates behaves like any other provider afterwards.

## How to connect Azure OpenAI in the wizard

On the wizard's **Select your model provider and default model** screen:

1. Choose **Custom endpoint** — the last row in the provider list. If the provider list cannot load, the wizard says so and the row stays available.
2. Fill in the form:

| Field | What to enter |
|---|---|
| Provider id | A name you choose, for example `my-azure`. See the name rules below. |
| API base URL | Your Azure resource's address. Microsoft's Azure OpenAI documentation describes the address; Omnipus uses it exactly as you enter it and sends its requests to `<base URL>/chat/completions`. Omnipus does not check the address's shape, so copy it carefully. |
| Protocol | `openai-compatible` for Azure OpenAI and other OpenAI-format services; `anthropic` for services that speak Anthropic's format. |
| API key | The key the service gave you. |

3. Select **Add endpoint**. The button stays disabled until every field is filled and the name is allowed.
4. In **Default model**, type the exact model name your endpoint serves. A custom endpoint has no model list to pick from. For Azure, this is the deployment name.
5. Select **Check connection**. Omnipus sends one small request to your endpoint with your key, asking for the model you typed.
6. Select **Finish** when the check passes.

**Name rules.** The id must not be the id of a listed provider: typing `azure` is refused with *"azure" is the name of a listed provider. Choose a different name for your custom endpoint, for example "my-azure".* The form shows this refusal as soon as you type a taken name, while the provider list is loaded. A name may be up to 64 characters and may not contain `/`, `\` or `..`; if it breaks those rules, **Finish** names the problem when you complete setup.

## What the check tells you

- **"Key accepted — \<model\> is ready"** — the endpoint answered, the key works for that model, and **Finish** unlocks.
- **An amber warning** — the endpoint could not be reached, or the account has no credit. You can still finish setup; the warning says what Omnipus could not verify.
- **A red error** — the key was rejected, or the request was refused. **Finish** stays locked until a check passes for the model on screen. Open the error's **Technical details** to see the provider's own answer.

If **Finish** itself reports a problem, the message starts with "Could not complete setup: " and names the field to fix — the name, the base URL, the protocol, the model or the key. Fix it and select **Retry setup**.

## Limits and things to watch

- **An internal address is refused when the address guard is on.** If the endpoint's address points at the machine's own network — for example `127.0.0.1` or a `10.x` address — setup refuses it: *"The provider address points at an internal network, which is not reachable from here. Use the provider's public address."* (The connection check shows a shorter version of the same refusal.) On a local install the guard is off unless you enable it (`sandbox.ssrf.enabled` in the config file); with it off, an internal address is not refused. On the hosted edition the guard is always on, even when `sandbox.ssrf.enabled` is off: an internal address is refused unless the operator's allow list names it, and only the operator can change that list. An allowed address that redirects to an internal one is refused the same way. A refused setup saves nothing, and onboarding stays open.
- **The check speaks the OpenAI chat format for both protocols.** Even with the `anthropic` protocol, the connection check sends an OpenAI-format chat request. An Anthropic-protocol service that does not answer that format may show a warning even though the key is fine.
- **A typo in the base URL surfaces at the check, not in the form.** The form accepts any non-empty address.
- **Model names afterwards.** Once setup is done, you maintain the endpoint's model names under **Settings → Providers** — see [Providers and models](providers-and-models.md).

## Related pages

- [Getting started](getting-started.md) — install, the wizard, first chat.
- [Providers and models](providers-and-models.md) — providers after setup, and model requirements.
- [Troubleshooting](troubleshooting.md) — when a connection fails.
