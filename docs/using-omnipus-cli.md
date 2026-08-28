# Using Omnipus from the command line

Omnipus ships as a single binary called `omnipus`. This guide covers everything you
need to run Omnipus from a terminal: set it up, talk to your agents, manage secrets,
and run it as a persistent server.

> Prefer a graphical app? See [Using Omnipus from the web UI](using-omnipus-ui.md).

Every block below is copy-paste ready.

---

## 1. Install the binary

```bash
curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
```

No sudo? Send it somewhere in your home directory instead:

```bash
OMNIPUS_INSTALL_DIR="$HOME/.local/bin" curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
```

For the full platform matrix (macOS, Linux, ARM, manual downloads, Docker), see the
[install section of the project README](../README.md#install).

Confirm it worked:

```bash
omnipus version
# omnipus v1.x.x  (commit abc1234, built 2026-05-30)
```

---

## 2. Set up without the web wizard

The first time you run Omnipus, it needs an AI provider, an API key, a default model,
and an admin account. The terminal wizard handles all of that:

```bash
omnipus onboard
```

It walks you through, step by step:

```text
Welcome to Omnipus.
? Pick a provider (1-4):  1) OpenRouter  2) OpenAI  3) Anthropic  4) Other
> 1
? Paste your API key:     ********************
  Testing connection...   OK
? Default model:          openrouter/google/gemini-2.5-flash
? Admin username:         daniel
? Admin password:         ******** (min 8 characters)
Setup complete. You can now start Omnipus.
Access your dashboard at: http://localhost:5000
```

### Headless setup (no prompts) — for servers and scripts

On an unattended box (Docker entrypoint, CI, a remote VPS) pass all answers as flags:

```bash
omnipus onboard --non-interactive \
  --provider openrouter \
  --api-key 'sk-or-v1-...' \
  --model 'openrouter/google/gemini-2.5-flash' \
  --admin-username admin \
  --admin-password 'choose-a-strong-one'
```

To keep secrets out of your shell history:

```bash
printf 'sk-or-v1-...\nchoose-a-strong-one\n' | omnipus onboard --non-interactive \
  --provider openrouter \
  --api-key-stdin \
  --admin-username admin \
  --admin-password-stdin
```

---

## 3. Start the server

This starts Omnipus: the web app, the API, and all your chat channels.

```bash
omnipus start
# http://localhost:5000        (web UI + API)
# http://192.168.1.10:5000    (LAN — shown when bound to 0.0.0.0)
# http://localhost:5001        (agent iframe previews)
```

> The old command `omnipus gateway` is kept as an alias and continues to work, but
> `omnipus start` is the preferred name going forward.

Useful flags:

| Flag | What it does |
|------|--------------|
| `-d`, `--debug` | Verbose logs |
| `-T`, `--no-truncate` | Don't shorten long log lines |
| `--allow-empty` | Boot even if no provider is configured yet (first-run) |

```bash
# With debug logging
omnipus start -d
```

To expose Omnipus on a real domain (HTTPS, behind nginx or Caddy), follow
[Reverse proxy setup](operations/reverse-proxy.md).

---

## 4. Run a one-shot task from the terminal

With the server running, you can send a task directly to a named agent and get
the answer back in your terminal — no browser required:

```bash
# Talk to a specific agent
omnipus jim "Summarise the open GitHub issues in elicify-ai/omnipus"

# Override the model for this turn
omnipus mia --model openrouter/google/gemini-2.5-flash "Draft a welcome email"
```

The agent's reply streams to **stdout**; tool activity and progress go to **stderr**,
so you can pipe or redirect cleanly:

```bash
omnipus jim "List the top 5 files by size" > files.txt
```

Run `omnipus` (no arguments) to see the available agents:

```bash
omnipus
# Available agents:
#   mia    Mia — your primary assistant
#   jim    Jim — coding and file tasks
#   ray    Ray — research and web search
#   ava    Ava — builder and orchestrator
#
# Usage: omnipus <agent> "<prompt>" [--model <slug>]
```

### Ask-policy tools in one-shot mode

Some tools (like Jim's shell) require explicit approval. Without `--yes` the tool
is auto-denied and the run continues:

```bash
# Deny any approval-required tools and continue
omnipus jim "Fix the bug in main.go"

# Auto-approve approval-required tools for this run
omnipus jim --yes "Run the test suite and fix failures"
```

---

## 5. Manage API keys and secrets safely

Omnipus keeps your API keys and other secrets in an encrypted vault — they are never
written in plain text and never printed back to you.

```bash
# Add or update a secret
omnipus credentials set OPENAI_API_KEY sk-...
omnipus credentials set ANTHROPIC_API_KEY sk-ant-...
omnipus credentials set TELEGRAM_BOT_TOKEN 123456:ABC...

# See which secrets exist (names only — values are never shown)
omnipus credentials list
# OPENAI_API_KEY      (set)
# TELEGRAM_BOT_TOKEN  (set)

# Remove one (asks you to confirm)
omnipus credentials delete OPENAI_API_KEY

# Re-encrypt every stored secret under a new passphrase
omnipus credentials rotate
```

The vault is locked with a master key. Keep a backup of it somewhere safe — without
it, the encrypted secrets cannot be recovered. For details, see
[Credential encryption](credential_encryption.md).

---

## 6. Health and safety checks

```bash
# Check your configuration for common security and safety problems
omnipus doctor

# Review the security and activity log
omnipus audit

# Walk the audit log and verify the HMAC chain
omnipus audit verify
```

Run `omnipus doctor` after any big config change, and especially before exposing
the gateway to the internet — it flags risky settings in plain language.

---

## 7. Headless and server tips

### Run it under systemd

Create a service that runs `omnipus start` and keeps it alive across reboots. Point
your reverse proxy at it for HTTPS — see [Reverse proxy setup](operations/reverse-proxy.md).

### Or run it in Docker

See [Running with Docker](docker.md).

### Pre-provision the master key for unattended starts

On a headless box you can supply the master key through an environment variable:

```bash
export OMNIPUS_MASTER_KEY=<64-hex-char-key>
omnipus start
```

See [Credential encryption](credential_encryption.md) for details.

### Watch the logs while you set things up

```bash
omnipus start -d --no-truncate
```

---

## 8. What the CLI can do

| Want to… | CLI | Web app |
|---|---|---|
| Set up Omnipus for the first time | ✅ `omnipus onboard` | ✅ web wizard |
| Start the server | ✅ `omnipus start` | — |
| Run a one-shot task | ✅ `omnipus <agent> "<prompt>"` | ✅ chat UI |
| Override the model for one turn | ✅ `--model <slug>` | ✅ model picker |
| Manage API keys securely | ✅ `omnipus credentials set/list/delete/rotate` | ✅ Settings → Providers |
| Check configuration health | ✅ `omnipus doctor` | — |
| Review the audit log | ✅ `omnipus audit [verify]` | — |
| Chat in the browser | — | ✅ |
| Create / edit custom agents | — | ✅ (a form, or ask Ava) |
| Connect channels (Telegram, Discord, Slack…) | — | ✅ Connectors → Configure |
| Browse past sessions | — | ✅ history panel |
| Task board (Command Center) | — | ✅ |
| Add MCP servers | — | ✅ Settings → MCP |
| Set your preferences | — | ✅ Settings → Profile |

---

## 9. Where to go next

[Getting started](getting-started.md) is the quickest path from zero to your first chat.

[Using Omnipus from the web UI](using-omnipus-ui.md) covers the graphical half of this guide.

[Concepts](concepts.md) explains the agents, sessions, tools, and how routing works.

[Troubleshooting](troubleshooting.md) is the place to start when something doesn't behave.
