# Using Antigravity Provider in Omnipus

This guide explains how to set up and use the **Antigravity** (Google Cloud Code Assist) provider in Omnipus.

## Prerequisites

You need a Google account and Google Cloud Code Assist enabled (usually available via the "Gemini for Google Cloud" onboarding).

## 1. Authentication

Antigravity (Google Cloud Code Assist) uses OAuth and is configured through the
onboarding wizard or the web UI.

**Via the web UI (recommended):** Open `http://localhost:5000/onboarding` or go to
**Settings → Providers → Add provider** and select Antigravity. Follow the OAuth flow
in your browser.

**Via the terminal:** Run `omnipus onboard` and select the Antigravity provider. The
wizard opens a browser for the OAuth flow and completes automatically.

**Headless/VPS:** If your server cannot reach `localhost`, note the OAuth redirect URL
the wizard prints, open it in your local browser, complete the login, then copy the
final `localhost:51121/...` URL from your browser's address bar and paste it back into
the terminal. Omnipus extracts the authorization code and finishes the flow.

## 2. Managing Models

### List Available Models

To see which models your project has access to and check their quotas, open
**Settings → Providers → Antigravity** in the web UI.

### Switch Models

You can change the default model in `~/.omnipus/config.json` or override it per run:

```bash
# Override for a single command
omnipus mia --model antigravity/gemini-3-flash "Hello"
```

## 3. Real-World Usage (Coolify/Docker)

Set the `OMNIPUS_AGENTS_DEFAULTS_MODEL_NAME` environment variable to select the model, for example `OMNIPUS_AGENTS_DEFAULTS_MODEL_NAME=antigravity/gemini-3-flash`.

> Note: `OMNIPUS_AGENTS_DEFAULTS_MODEL` is the v0 schema form. In a default v1 install the v0 key is not declared on the v1 `AgentsDefaultsConfig.Model` field (`pkg/config/config.go:646`), so a value set there is silently ignored. Use the v1 `..._MODEL_NAME` env var above (or the corresponding `agents.defaults.model_name` JSON key).

For authentication persistence, if you've logged in locally you can copy your credentials to the server:

```bash
scp ~/.omnipus/auth.json user@your-server:~/.omnipus/
```

Alternatively, run the `auth login` command once on the server if you have terminal access.

## 4. Troubleshooting

### Empty Response

If a model returns an empty reply, it may be restricted for your project. Try
`antigravity/gemini-3-flash` (the only antigravity model string hard-coded in the repo,
per `pkg/providers/antigravity_provider.go:21`).

### 429 Rate Limit

Antigravity has strict quotas. Omnipus will display the "reset time" in the error
message if you hit a limit.

### 404 Not Found

Ensure you are using a model ID listed under **Settings → Providers → Antigravity →
Available models**. Use the short ID (e.g., `gemini-3-flash`) not the full path.

## 5. Supported Antigravity Models

The only antigravity model string hard-coded in the repository is `gemini-3-flash` (used as the default in `pkg/providers/antigravity_provider.go:21`). Any other model id is whatever Google's Cloud Code Assist backend reports for the authenticated project at request time — verify against **Settings → Providers → Antigravity → Available models** in the web UI.

| Model string | Source |
|---|---|
| `antigravity/gemini-3-flash` | `pkg/providers/antigravity_provider.go:21` (`antigravityDefaultModel`) |
