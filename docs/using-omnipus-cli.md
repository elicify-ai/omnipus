# Using the Omnipus command line

Omnipus ships as a single binary called `omnipus`. This page covers the terminal half: install, setup, one-shot tasks, secrets, and running it as a server. Prefer a graphical interface? Start with [Using Omnipus from the web UI](using-omnipus-ui.md).

## What it is

One binary, a small set of commands. `omnipus <agent> "<prompt>"` runs a one-shot task. The named commands: `onboard` (first-time setup), `start` and `stop` (the server), `credentials` (secrets), `doctor` and `audit` (health and the security log), `records import-obsidian` (import notes — see [the knowledge base](knowledge.md)), and `version`.

## How to install the binary

1. Run the installer:

   ```bash
   curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
   ```

   The script downloads the binary into `/usr/local/bin`.

2. No write access there? Install into your home directory instead:

   ```bash
   OMNIPUS_INSTALL_DIR="$HOME/.local/bin" curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
   ```

3. Confirm with `omnipus version`, which prints the build version.

Other platforms: see the [project README](../README.md#install).

## How to set up without the web wizard

The first run needs an AI provider, an API key, a default model, and an admin account. The wizard collects all four:

1. Start it:

   ```bash
   omnipus onboard
   ```

2. Pick a provider from the menu:

   ```text
   Select your LLM provider:
     1) OpenRouter
     2) Anthropic
     3) OpenAI
     4) Google Gemini
     5) Groq
     6) DeepSeek
     7) Other (enter provider id)
   Choice [1]:
   ```

3. Enter your API key. Input is hidden, and the wizard checks the key against the provider, asking again if it is rejected.

4. Enter a model, or press Enter to accept the provider's default: the first model in its catalog that can use tools.

5. Enter an admin username and a password of at least 8 characters. When the wizard prints `Onboarding complete.`, you are done.

On an already-configured system it prints `Onboarding is already complete; nothing to do.` A fresh install can also be set up from the browser, which runs the same wizard.

### Headless setup — no prompts

Pass every answer as a flag:

```bash
omnipus onboard --non-interactive \
  --provider openrouter \
  --api-key 'sk-or-v1-...' \
  --model 'z-ai/glm-5v-turbo' \
  --admin-username admin \
  --admin-password 'choose-a-strong-one'
```

To keep secrets out of your shell history, feed them through stdin instead:

```bash
printf 'sk-or-v1-...\nchoose-a-strong-one\n' | omnipus onboard --non-interactive \
  --provider openrouter \
  --api-key-stdin \
  --admin-username admin \
  --admin-password-stdin
```

## How to start and stop the server

1. Start Omnipus:

   ```bash
   omnipus start
   ```

   The web app, the API, and agent previews all load from **one port, 5000 by default**. There is no second port. Before the server comes up, it prints where to connect:

   ```text
   Open the dashboard / log in as admin:
     This machine:   http://localhost:5000
     Hint: bound to localhost — set gateway.host=0.0.0.0 to allow other devices
   ```

2. To reach Omnipus from other devices, set `gateway.host` to `0.0.0.0`; the banner then lists them too.

3. Stop it from another terminal:

   ```bash
   omnipus stop
   ```

   You see `Omnipus gateway stopped.` — or `No Omnipus gateway is running.`

`omnipus start` runs in the foreground, which suits a systemd service. The older spelling `omnipus gateway` still works as an alias. For HTTPS behind nginx or Caddy, see [Reverse proxy setup](operations/reverse-proxy.md); for containers, [Running with Docker](docker.md).

Useful flags:

| Flag | What it does |
|---|---|
| `-d`, `--debug` | Verbose logs |
| `-T`, `--no-truncate` | Stop long values being cut in debug logs; requires `-d` |
| `--sandbox <mode>` | Override the sandbox mode for this run: `enforce`, `permissive`, or `off` |

## How to run a one-shot task from the terminal

One-shot tasks need the server running.

1. Send a task to a named agent:

   ```bash
   omnipus jim "Summarize the open GitHub issues in elicify-ai/omnipus"
   ```

2. To use a different model for this one turn, add `--model`:

   ```bash
   omnipus mia --model openrouter/glm-5.2 "Draft a welcome email"
   ```

The reply streams to stdout; tool activity and progress go to stderr, so you can redirect cleanly:

```bash
omnipus jim "List the top 5 files by size" > files.txt
```

Run `omnipus` with no arguments to list the agents you can address, by ID and name — on a fresh install that is `mia`, `jim`, `ava`, and `ray`.

Some tools, such as an agent's shell, require approval. Without `--yes` they are denied and the run continues; with it, approvals are granted for this run only:

```bash
omnipus jim --yes "Run the test suite and fix failures"
```

## How to manage API keys and secrets

Secrets live in an encrypted credential vault. Values are never written in plain text and never printed back.

```bash
omnipus credentials set OPENAI_API_KEY sk-...
omnipus credentials set TELEGRAM_BOT_TOKEN 123456:ABC...
omnipus credentials list          # names only
omnipus credentials delete OPENAI_API_KEY   # asks to confirm
omnipus credentials rotate        # new passphrase
```

The vault is locked with a master key. Keep a backup of it — without it, the secrets cannot be recovered. See [Credential encryption](credential_encryption.md). On unattended servers, supply the master key as an environment variable instead of a passphrase:

```bash
export OMNIPUS_MASTER_KEY=<64-hex-char-key>
omnipus start
```

## How to check configuration health

```bash
omnipus doctor        # common security and safety issues, in plain language
omnipus audit         # the security and activity log
omnipus audit verify  # verify the log has not been tampered with
```

Run `omnipus doctor` after any big configuration change, and before exposing the server to the internet.

## CLI or web app?

Most jobs can be done from either place.

| Want to… | Command line | Web app |
|---|---|---|
| Start or stop the server | `omnipus start` / `omnipus stop` | — |
| Run a one-off task | `omnipus <agent> "<prompt>"` | Chat |
| Pick a model for one run | `--model <slug>` | The model picker in chat |
| Manage API keys | `omnipus credentials …` | Settings → Providers |
| Review the audit log | `omnipus audit` | Settings → Security |
| Create and edit agents | — | The Agents screen |
| Connect Telegram, Discord, Slack and more | — | The Connectors screen, Configure |
| Add MCP servers | — | The Skills screen |
| Follow task work | — | A workspace's Board and Calendar |
| Set your preferences | — | Settings → Profile |

## Limits and things to watch

- **One-shot tasks need a running server.** If it is down, the command prints `Omnipus isn't running — start it with: omnipus start`. Runs also time out after five minutes by default; change it with `--timeout <duration>`.
- **Talking to a remote server is not supported yet.** The `--url` flag exists but is reserved.
- **Command names shadow agent names, and worker agents cannot be addressed directly.** An agent named `start` cannot be run from the terminal; rename it or use the web app. `omnipus` with no arguments lists the agents you can run.

## Related pages

- [Getting started](getting-started.md) — zero to your first chat.
- [Using Omnipus from the web UI](using-omnipus-ui.md) — the graphical half of this guide.
- [Concepts](concepts.md) — the agents, sessions, tools, and how routing works.
- [Troubleshooting](troubleshooting.md) — for when something misbehaves.
- [The knowledge base](knowledge.md) — where imported notes and records live.
