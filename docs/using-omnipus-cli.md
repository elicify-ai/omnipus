# Using Omnipus from the command line

Omnipus ships as a single binary called `omnipus`. Everything you can do in the web app you can also drive from a terminal: set it up, chat with your agents, manage API keys, install skills, schedule tasks, and run it as a server. This guide is for people who live in the terminal, or who run Omnipus headless on a server with no screen attached.

> Prefer a graphical app? See [Using Omnipus from the web UI](using-omnipus-ui.md).

Every block below is copy-paste ready. Commands and their flags are real — nothing here is invented.

---

## 1. Install the binary

The fastest way to get the binary is the one-line installer. It detects your OS and architecture, downloads the latest release, verifies it, and installs to `/usr/local/bin/omnipus`:

```bash
curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
```

No sudo? Send it somewhere in your home directory instead:

```bash
OMNIPUS_INSTALL_DIR="$HOME/.local/bin" curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
```

For the full platform matrix (macOS, Linux, ARM, manual downloads, Docker), see the [install section of the project README](../README.md#install).

Confirm it worked:

```bash
omnipus version
# omnipus v1.x.x  (commit abc1234, built 2026-05-30)
```

If `omnipus version` prints a version string, you're ready.

---

## 2. Set up without the web wizard

The first time you run Omnipus, it needs four things: which AI provider to use, an API key for that provider, a default model, and an admin account. The web app asks for these in an onboarding wizard. The terminal has the exact same flow:

```bash
omnipus onboard
```

It walks you through, step by step:

```text
Welcome to Omnipus.
? Pick a provider:        OpenRouter
? Paste your API key:     ********************
  Testing connection...   OK
? Default model:          openai/gpt-4o
? Admin username:         daniel
? Admin password:         ******** (min 8 characters)
Setup complete. You can now start the gateway.
```

This is the terminal twin of the web onboarding — once it finishes, you can start the gateway or jump straight into terminal chat. (New to providers? [OpenRouter or OpenAI are the easiest first pick](providers.md); pick Ollama if you want to run fully local.)

---

## 3. Two ways to run

There are two completely different ways to use Omnipus from here. Pick whichever fits.

### (a) Headless server — serve the web UI and API

This starts the gateway: the web app, the API, and all your chat channels. Use this on a server, or any time you want the browser UI and chat platforms working.

```bash
omnipus gateway
# Gateway listening on http://localhost:5000
# Agent previews on  http://localhost:5001
```

Useful flags:

| Flag | What it does |
|------|--------------|
| `-d`, `--debug` | Verbose logs — handy when something isn't working |
| `-T`, `--no-truncate` | Don't shorten long log lines |
| `--allow-empty` | Start with no users yet (for local development only) |

```bash
# Local dev, with debug logging and no users required yet
omnipus gateway --allow-empty -d
```

To expose the gateway safely on a real domain (HTTPS, behind nginx or Caddy), follow [Reverse proxy setup](operations/reverse-proxy.md). Don't put `--allow-empty` on anything reachable from the internet — that flag is for your laptop.

### (b) Pure terminal chat — no browser at all

If you just want to talk to your agents from the terminal, you don't need the gateway at all:

```bash
omnipus agent
```

This opens an interactive chat (a REPL). Type, get a reply, type again.

- Press **Ctrl+C** to exit.
- Press **Esc twice** to cancel a reply that's still being written.

More on this next.

---

## 4. Chat in your terminal

`omnipus agent` is the terminal version of the chat screen. By default it's interactive:

```bash
omnipus agent
```

```text
omnipus › Plan my week around three deadlines.
Mia › Sure — let's lay them out. What are the three deadlines and when...
omnipus › The Acme report is Friday, ...
```

### Ask one question and exit (one-shot mode)

Use `-m` / `--message` to send a single message and print the answer — no interactive session. This is perfect for scripts:

```bash
omnipus agent -m "Summarise the latest news on small modular reactors."
```

```text
Ray › Here's what I found, with sources:
1. ...
```

### Keep separate conversations with sessions

Each conversation is a session. The default session key is `cli:default`. Use `-s` / `--session` to keep work and personal chats apart — the app remembers what you discussed in each one separately:

```bash
# A dedicated "work" conversation that remembers its own history
omnipus agent -s work

# Send a one-shot into the same "work" session later
omnipus agent -s work -m "What did we decide about the Acme report?"
```

### Override the model for this run

```bash
omnipus agent --model openai/gpt-4o -m "Draft a polite decline email."
```

Add `-d` / `--debug` to any of these if you want to see what's happening under the hood.

---

## 5. Slash commands work here too

Inside `omnipus agent`, the same slash commands you use in the web chat and chat channels work at the prompt. Just type them on a line:

| Command | What it does |
|---------|--------------|
| `/help` | Show available commands |
| `/list models` | List models (also `channels`, `agents`, `skills`) |
| `/switch` | Change the model |
| `/use <skill> [message]` | Force a specific skill for one turn |
| `/clear` | Wipe the current chat history |
| `/cancel` | Stop the current reply |
| `/subagents` | Show the running subagent tree |

```text
omnipus › /list models
omnipus › /use research Find three peer-reviewed sources on sleep and memory.
omnipus › /clear
```

---

## 6. Pick your model

Show the current default model:

```bash
omnipus model
# Current default model: openai/gpt-4o
```

Change it by passing a name:

```bash
omnipus model anthropic/claude-3-5-sonnet
# Default model set to anthropic/claude-3-5-sonnet
```

This sets the default every agent uses unless you override it (for example with `--model` on `omnipus agent`).

---

## 7. Manage API keys and secrets safely

Omnipus keeps your API keys and other secrets in an encrypted vault on disk — they're never written in plain text and never printed back to you.

```bash
# Add or update a secret
omnipus credentials set OPENAI_API_KEY sk-...

# See which secrets exist (names only — values are never shown)
omnipus credentials list
# OPENAI_API_KEY      (set)
# TELEGRAM_BOT_TOKEN  (set)

# Remove one (asks you to confirm; add -f to skip the prompt)
omnipus credentials delete OPENAI_API_KEY

# Re-encrypt every stored secret under a new passphrase
omnipus credentials rotate
# Enter current passphrase to verify existing credentials, then a new passphrase.
```

The vault is locked with a master key (or passphrase). Keep a backup of it somewhere safe — without it, the encrypted secrets can't be recovered. For how the vault works and how to back up or restore the master key, see [Credential encryption](credential_encryption.md).

---

## 8. Add skills from the terminal

Skills give your agents extra abilities. You can find, install, and manage them entirely from the command line:

```bash
# Search the skill registry
omnipus skills search pdf

# Install one
omnipus skills install pdf-tools

# See what's installed
omnipus skills list

# Look at the details of a skill
omnipus skills show pdf-tools

# Update a skill to the latest version
omnipus skills update pdf-tools

# Remove a skill
omnipus skills remove pdf-tools

# Install the skills that ship with Omnipus
omnipus skills install-builtin
```

For what skills are and how to write your own, see [Skills](skills.md).

---

## 9. Schedule recurring tasks

Cron lets you have an agent do something on a schedule — a morning news digest, a nightly backup summary, a weekly report — without you being there. Each task has a name, a message the agent receives when it runs, and a schedule. You set the schedule either with `--every` (run every N seconds) or `--cron` (a classic cron expression).

```bash
# Run a task every day at 9am: give it a name, the message, and a cron expression
omnipus cron add --name morning-news \
  --message "Give me a 5-bullet summary of overnight AI news." \
  --cron "0 9 * * *"
# ✓ Added job 'morning-news' (1)

# Or run something every hour (3600 seconds)
omnipus cron add -n heartbeat -m "Check the queue and report anything stuck." -e 3600

# See all scheduled tasks (each has an ID shown in the list)
omnipus cron list

# Pause one without deleting it (use the ID from the list)
omnipus cron disable 1

# Turn it back on
omnipus cron enable 1

# Delete one for good
omnipus cron remove 1
```

Want the answer delivered to a chat channel instead of just running quietly? Add `--deliver` along with `--channel` and `--to`. Think of cron as handing a teammate a standing instruction: "every weekday at 8am, send me a summary of overnight emails."

---

## 10. Connect accounts that need a login

Some things need you to sign in once so an agent can act on your behalf. Manage those logins here:

```bash
# Sign in
omnipus auth login

# See what's currently signed in
omnipus auth status

# Sign out
omnipus auth logout
```

For Chinese messaging platforms that use a QR-code or scan-to-login flow:

```bash
# WeChat (Weixin) — scan the QR code that appears
omnipus auth weixin

# WeCom (Work WeChat)
omnipus auth wecom
```

For connecting full chat platforms (Telegram, Slack, Discord, and more), see [Chat apps](chat-apps.md).

---

## 11. Health and safety checks

Three commands help you keep an eye on things:

```bash
# Quick overview: is the gateway up, how many agents and channels, etc.
omnipus status

# Check your configuration for common security and safety problems
omnipus doctor

# Review the security and activity log
omnipus audit
```

Run `omnipus doctor` after any big config change, and especially before exposing the gateway to the internet — it flags risky settings in plain language. Use `omnipus audit` whenever you want to see who did what and which tools ran.

---

## 12. Headless and server tips

Running Omnipus on a server with no screen? A few things make life easier.

**Run it under systemd.** Create a service that runs `omnipus gateway` and keeps it alive across reboots. Point your reverse proxy at it for HTTPS — see [Reverse proxy setup](operations/reverse-proxy.md).

**Or run it in Docker.** A container is often the simplest way to keep Omnipus isolated and easy to update. See [Running with Docker](docker.md).

**Pre-provision the master key for unattended starts.** On a headless box you usually can't type the vault password by hand on every restart, so you can supply the master key through an environment variable instead — see [Credential encryption](credential_encryption.md) for the exact variable and how to store it securely.

**Watch the logs while you set things up:**

```bash
omnipus gateway -d --no-truncate
```

---

## What the CLI can't do (yet)

The CLI and the web app aren't identical. The CLI is built for **setup, running a
server, secrets, skills, scheduling, and terminal chat** — and it's the better tool for
headless boxes. But some things are **web-app only** today. If you need one of these,
open the app in a browser (`omnipus gateway`, then visit `http://localhost:5000`):

| Want to… | CLI | Web app |
|---|---|---|
| Chat with an agent | ✅ `omnipus agent` | ✅ |
| Cancel a running reply | ✅ press **Esc** twice | ✅ |
| Use slash commands (`/help`, `/switch`, …) | ❌ not in the REPL | ✅ |
| See replies stream in live | ❌ prints when finished | ✅ |
| Send an image or file in chat | ❌ text only | ✅ |
| Browse / resume / delete past sessions | ⚠️ resume by key (`-s`) only | ✅ full history panel |
| Manage skills | ✅ `omnipus skills …` | ✅ |
| Schedule recurring jobs | ✅ `omnipus cron …` | ✅ (per-agent) |
| Switch model / sign in to a provider | ✅ `omnipus model` / `omnipus auth` | ✅ |
| Add a provider **after** first setup | ⚠️ `credentials set` + config edit | ✅ Settings → Providers |
| **Create / edit custom agents** | ❌ | ✅ (a form, or ask Ava) |
| **Task board (Command Center)** | ❌ | ✅ |
| **Connect most channels** (Telegram, Discord, Slack…) | ⚠️ WeChat only (`auth weixin`/`wecom`) | ✅ |
| **Add MCP servers** | ❌ | ✅ |
| **Set your preferences** ("what agents know about you") | ❌ | ✅ Settings → Profile |
| **Manage users, roles, devices** | ❌ | ✅ (admin) |

A few things are **easier from the CLI** than the app: first-run setup
(`omnipus onboard`), running the server (`omnipus gateway`), rotating the credential
vault (`omnipus credentials rotate`), verifying the audit log (`omnipus audit verify`),
and importing from another install (`omnipus migrate`).

> Closing these gaps is tracked under the **Feature Parity** milestone — see the
> CLI↔UI parity epic in the issue tracker.

---

## 13. Where to go next

- [Getting started](getting-started.md) — the quickest path from zero to your first chat.
- [Using Omnipus from the web UI](using-omnipus-ui.md) — the graphical half of this guide.
- [Concepts](concepts.md) — the five agents, sessions, tools, and how routing works.
- [Troubleshooting](troubleshooting.md) — when something doesn't behave.
