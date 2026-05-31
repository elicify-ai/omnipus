<div align="center">
<img src="src/assets/logo/omnipus-avatar.svg" alt="Omnipus" width="240">

<h1>Omnipus</h1>

<h3>A team of AI agents you actually own.</h3>

<p>Five named agents that hand off to each other, remember what you discussed, and do real work — research, writing, code, browsing, automation.</p>

<p>You run them yourself: no cloud account, no subscription, no data leaving your machine — except the calls to the AI model you choose.</p>

<p><b>New here?</b> → <a href="docs/getting-started.md">Get started in 10 minutes</a> · <a href="docs/concepts.md">How it works</a> · <a href="docs/using-omnipus-ui.md">Use the web app</a> · <a href="docs/using-omnipus-cli.md">Use the terminal</a></p>

<p>
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/React-19-61DAFB?style=flat&logo=react&logoColor=white" alt="React">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  <a href="https://omnipus.ai"><img src="https://img.shields.io/badge/Website-omnipus.ai-D4AF37?style=flat&logo=google-chrome&logoColor=white" alt="Website"></a>
</p>

<img src="docs/marketing/screenshots/04-agents-roster.png" alt="Omnipus agent roster" width="900">

</div>

---

## Meet the team

Five named coworkers ship with every install. Identity is locked (no silent knock-offs); you control their model and tool policy.

| Agent | Role | Best at |
|---|---|---|
| **Mia** | Coach & Guide | Onboarding new users, routing requests to the right teammate by intent — not by name. |
| **Jim** | General Purpose | Hands-on implementation: writing code, creating files, scoping projects, coordinating across agents. |
| **Ava** | Agent Builder | Interviews you, then creates a brand-new custom agent — persona, tools, prompt — in seconds. |
| **Ray** | Researcher | Deep research with citations. Web search, fetch, synthesis. Refuses to bluff when evidence is thin. |
| **Max** | Automator | Browser automation. Plan-then-execute multi-step flows with approval gates. |

Need more? Ava builds unlimited custom agents and Omnipus runs them all in the same binary.

---

## See it work

Four live screenshots, captured against the running gateway.

**Mia routes by intent.** Tell her what you need — "I need an agent to help me build a marketing website" — and she picks the right teammate, says why, and hands off in 12 ms.

The receiving agent picks up in the same transcript. No copy-paste.

<img src="docs/marketing/screenshots/16-handoff-mia-to-jim.png" alt="Mia routes a website-build request to Jim by intent" width="900">

**Ray researches with sources.** Web searches fan out, results synthesise into a numbered list with citations. He won't fake an answer.

<img src="docs/marketing/screenshots/14-ray-research-demo.png" alt="Ray researches open-source agent frameworks with citations" width="900">

**Max sees the web.** `browser.navigate` → `browser.screenshot` chained in one turn. The image streams back through the media pipeline and renders inline.

<img src="docs/marketing/screenshots/13-max-screenshot-demo.png" alt="Max screenshots anthropic.com and describes the page" width="900">

**Ava builds an agent live.** Tell her what you need, watch her call `system.agent.create`, get a summary card. New agent shows up in the roster instantly.

<img src="docs/marketing/screenshots/15-ava-build-agent.png" alt="Ava builds Penny the pricing analyst" width="900">

---

## What you actually get

- **A team, not a chatbot.** Five named agents who hand work to each other in the same conversation — Mia hears your request, picks the right teammate, and passes control over. No copy-paste, no re-explaining. → [How it works](docs/concepts.md)
- **They delegate and parallelize.** Agents can plan work and assign tasks to each other — track it all on the Command Center board — and break a big job into parallel subagents that report back. → [Using the web app](docs/using-omnipus-ui.md)
- **Memory that learns.** When a conversation winds down, Omnipus writes a recap *and* records the lessons learned — what went well, what to improve. The recap carries into your next session automatically and the lessons are kept for recall, so your team builds on past work instead of starting cold. → [docs/memory.md](docs/memory.md)
- **Agents that know your preferences.** Tell them once in Settings → Profile ("be concise", "I use Python", your timezone) and every agent keeps it in mind. → [How it works](docs/concepts.md)
- **Reach them anywhere.** Use the web app, the terminal, or wire your agents into Telegram, Discord, Slack, WhatsApp, and 10 other chat platforms — voice notes and images included. → [docs/chat-apps.md](docs/chat-apps.md)
- **You stay in control.** Agents ask permission before running anything sensitive (Allow / Deny / Always), and every action is logged so you can see exactly what happened. → [Using the web app](docs/using-omnipus-ui.md)
- **Extend it.** Install reusable **skills**, connect **MCP** servers, and let Ava build brand-new custom agents for you on demand. → [docs/skills.md](docs/skills.md)
- **Your keys, your machine.** API keys are encrypted on disk, nothing phones home, and there's no telemetry. Pick from 35+ AI providers — including fully-local options like Ollama. → [docs/providers.md](docs/providers.md)

<details>
<summary><b>For the technically curious</b> — what's under the hood</summary>

- **Single Go binary**, ~30 MB, with the web app embedded. No database, no Redis — file-based storage at `~/.omnipus/`.
- **Kernel-level sandbox** (Landlock + seccomp on Linux 5.13+), three-tier per-tool policy (allow/ask/deny), and an SSRF guard on every outbound HTTP tool. → [docs/operations/sandbox-config.md](docs/operations/sandbox-config.md)
- **Encrypted credential vault** (AES-256-GCM, Argon2id KDF). → [docs/credential_encryption.md](docs/credential_encryption.md)
- **Full audit trail** — every tool call, LLM request, and agent event lands in a replayable on-disk transcript feeding the UI, subprocess hooks, and a tamper-evident audit log. → [docs/observability.md](docs/observability.md)
- **14 in-process chat channels**, **35+ LLM providers** with fallback chains, multi-key rotation, streaming, and vision. → [pkg/channels/README.md](pkg/channels/README.md) · [docs/providers.md](docs/providers.md)
- **Channel-to-agent routing** binds inbound messages to specific agents by channel, account, guild, team, or peer. → [docs/routing.md](docs/routing.md)

</details>

---

## Install

Three supported paths. Pick the one that matches your host, then jump to [First boot](#first-boot).

| Path | When to use | Browser tools (`browser.*`, `web_serve`) |
|---|---|---|
| [Native binary](#native-binary-recommended) | Bare-metal / VPS / WSL2; you own the host kernel | ✅ — Chromium auto-downloads on first `browser.*` call |
| [Docker, minimal image](#docker-minimal-image) | Lowest-overhead deploy; chat + channels only, no browsing | ❌ — see [limitations](#minimal-image-limitations) |
| [Docker, heavy image](#docker-heavy-image) | Full feature parity inside a container | ✅ — apk Chromium pre-baked |

### Native binary (recommended)

```bash
curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
omnipus start
# open http://localhost:5000
```

What the script does:

- Detects your OS and architecture (`uname -s` / `uname -m`).
- Downloads `omnipus_<OS>_<arch>.tar.gz` from the latest GitHub Release.
- Verifies the SHA256 against the published `checksums.txt`.
- Extracts a single ~30 MB self-contained Go binary (SPA embedded via `go:embed`, no shared-lib runtime).
- Installs it to `/usr/local/bin/omnipus`.

It's plain POSIX `sh` — no bash-isms — so it works on Alpine, BusyBox, macOS, and Ubuntu.

Customise via environment:

| Variable | Default | Purpose |
|---|---|---|
| `OMNIPUS_VERSION` | `latest` Release | Pin a tag, e.g. `OMNIPUS_VERSION=v0.1.0` |
| `OMNIPUS_INSTALL_DIR` | `/usr/local/bin` | Use `$HOME/.local/bin` if you don't have sudo |
| `OMNIPUS_REPO` | `elicify-ai/omnipus` | Override only for forks |

**Browser tools.** On the first `browser.navigate` / `browser.screenshot` / `web_serve` call, the gateway looks for `google-chrome` / `chromium` / `chromium-browser` on `$PATH`:

- **Found** — it's used as-is.
- **Not found** — a managed Chromium is downloaded to `$OMNIPUS_HOME/browser/chromium/` (Chrome for Testing, ~150 MB, one-time).

That download needs glibc, so on Alpine hosts install `chromium` via `apk` first — the PATH lookup then resolves and the managed download is skipped.

**Supported platforms in v0.1:** Linux amd64, Linux arm64, macOS arm64. Other targets are tracked in [docs/operations/platform-support.md](docs/operations/platform-support.md).

### Docker, minimal image

```bash
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -p 127.0.0.1:5001:5001 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest
```

Or with compose: `curl -O https://raw.githubusercontent.com/elicify-ai/omnipus/main/docker/docker-compose.yml && docker compose up`.

The published image (`ghcr.io/elicify-ai/omnipus:latest`) is built from [`docker/Dockerfile`](docker/Dockerfile): an Alpine multi-stage build that produces a **~71 MB** runtime image with only `ca-certificates`, `tzdata`, and `curl` on top of the Go binary. Same SPA, same channels, same memory + sessions + audit log as the native install.

#### Minimal image limitations

The minimal image **deliberately excludes Chromium** to keep the artefact small. This means:

- `browser.navigate` / `browser.screenshot` / `browser.read_content` / `browser.console_logs` / `browser.action` and the entire `web_serve` dev-server preview flow **will not work** out of the box.
- The auto-download fallback in `pkg/tools/browser/manager.go` will fetch a managed Chromium from Chrome for Testing — but the binary is **glibc-linked** and the runtime is **Alpine (musl)**, so `exec` fails with a misleading `no such file or directory` (the missing ELF interpreter is `/lib64/ld-linux-x86-64.so.2`, not the binary itself).
- The Max agent will gracefully fall back to `web_fetch` for read-only tasks and explain the missing capability to the user.

If you need browser tools inside Docker, use the heavy image below.

### Docker, heavy image

```bash
docker build -t omnipus:heavy -f docker/Dockerfile.heavy .
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -p 127.0.0.1:5001:5001 \
  -v "$PWD/data:/home/omnipus/.omnipus" \
  omnipus:heavy
```

Built from [`docker/Dockerfile.heavy`](docker/Dockerfile.heavy): same three-stage SPA + Go build as the minimal image, but the runtime stage adds `chromium`, `python3`, `py3-pip`, `uv` / `uvx`, `git`, `jq`, and a global `agent-browser` npm install. About **1.08 GB** on disk in exchange for first-class browser tools and Python MCP server support out of the box.

Heavy image is not currently published to GHCR — build it yourself per the snippet above. (Tracked: ship it from the same release pipeline.)

### From source (contributors)

```bash
git clone https://github.com/elicify-ai/omnipus.git
cd omnipus
make build        # builds SPA + Go binary in one step
./build/omnipus start
```

Requires Go 1.26+ and Node 24+. `make build` runs `spa-embed` first so `go:embed` picks up the latest Vite output.

### First boot

Two ports open:

- **5000** — SPA + API
- **5001** — sandboxed agent preview iframes

The onboarding wizard runs on first visit: Welcome → Provider → API Key → Model → Admin Account → Done.

A 256-bit AES key auto-generates at `~/.omnipus/master.key` (mode `0600`).

**Back it up** — losing it means losing every encrypted credential.

For headless deployments, pre-provision via `OMNIPUS_KEY_FILE` or `OMNIPUS_MASTER_KEY`. → [docs/credential_encryption.md](docs/credential_encryption.md)

### Headless onboarding (no browser)

If you can't open `localhost:5000` — Docker host, remote VPS, CI runner — finish onboarding from the shell instead. Secrets read from stdin so they never appear in `ps`:

```bash
printf '%s\n%s\n' "$OPENROUTER_API_KEY" "$ADMIN_PASSWORD" | \
  omnipus onboard --non-interactive \
    --provider openrouter \
    --api-key-stdin \
    --model 'z-ai/glm-5v-turbo' \
    --admin-username admin \
    --admin-password-stdin

omnipus start
```

`omnipus onboard --help` lists every flag (`--provider`, `--api-key`, `--api-key-stdin`, `--model`, `--admin-username`, `--admin-password`, `--admin-password-stdin`, `--non-interactive`). Same end-state mutations as the SPA wizard — config, credentials, admin user, state — so you can log in immediately with the credentials you just passed.

---

## Documentation

User-facing guides, grouped by what you're trying to do:

| Topic | Where to go |
|---|---|
| **▶ Start here — your first 10 minutes** | [docs/getting-started.md](docs/getting-started.md) |
| **How Omnipus works (plain English)** | [docs/concepts.md](docs/concepts.md) |
| **Using the web app** | [docs/using-omnipus-ui.md](docs/using-omnipus-ui.md) |
| **Using the terminal / CLI** | [docs/using-omnipus-cli.md](docs/using-omnipus-cli.md) |
| **Full documentation index** | [docs/README.md](docs/README.md) |
| **Memory system** | [docs/memory.md](docs/memory.md) |
| **Channel-to-agent routing** | [docs/routing.md](docs/routing.md) |
| **Session history & event stream** | [docs/observability.md](docs/observability.md) |
| **Skills & MCP** | [docs/skills.md](docs/skills.md) · [docs/tools_configuration.md](docs/tools_configuration.md) |
| **Channels (per-channel guides)** | [pkg/channels/README.md](pkg/channels/README.md) |
| **Hooks (subprocess + in-process)** | [docs/hooks/README.md](docs/hooks/README.md) |
| **Tools reference (full catalog)** | [docs/tools-reference.md](docs/tools-reference.md) |
| **LLM providers** | [docs/providers.md](docs/providers.md) |
| **Sandbox: config & status** | [docs/operations/sandbox-config.md](docs/operations/sandbox-config.md) |
| **Sandbox: known limitations** | [docs/operations/sandbox-limitations.md](docs/operations/sandbox-limitations.md) |
| **Reverse proxy & TLS** | [docs/operations/reverse-proxy.md](docs/operations/reverse-proxy.md) |
| **Docker** | [docs/docker.md](docs/docker.md) |
| **Credential vault** | [docs/credential_encryption.md](docs/credential_encryption.md) |
| **Configuration reference** | [docs/configuration.md](docs/configuration.md) |
| **Platform support** | [docs/operations/platform-support.md](docs/operations/platform-support.md) |
| **Troubleshooting** | [docs/troubleshooting.md](docs/troubleshooting.md) |
| **Debug & log spelunking** | [docs/debug.md](docs/debug.md) |

Browse the full index at [docs/README.md](docs/README.md).

---

## Architecture

<img src="docs/marketing/diagrams/architecture.svg" alt="Omnipus architecture: clients on top, gateway with main port 5000 and sandboxed preview port 5001, channel-to-agent routing, agent runtime with hooks/tools/policy/event-bus, four persistence stores (memory, sessions, audit log, credential vault), all wrapped by the Linux kernel sandbox; LLM providers, MCP servers, and ClawHub registry live outside the sandbox and are reached via SSRF-checked outbound HTTP" width="960">

Single Go binary. File-based JSON/JSONL storage at `~/.omnipus/`. No Postgres, no Redis. WhatsApp uses pure-Go SQLite (`modernc.org/sqlite`) in its own session namespace.

---

## Tech stack

**Backend:** Go 1.26+ · `chromedp` · `whatsmeow` · `discordgo` · `telebot` · `slack-go` · `golang.org/x/sys/unix` (Landlock, seccomp).

**Frontend:** TypeScript · React 19 · Vite 6 · shadcn/ui (Radix + Tailwind v4) · AssistantUI · Phosphor Icons · Zustand · TanStack Query/Router.

---

## Status

Pre-1.0 and moving fast:

- **v0.1** — stabilized gateway, iframe preview, sandbox hardening. ✅ Complete.
- **v0.2** — security hardening. ✅ Complete.
- **v0.3 / 1.0** — the "Rooms" redesign of memory, projects, and tasks. 🚧 In design.

A single Go binary with the web app embedded *is* the product — MIT-licensed, community-focused, no telemetry. See the [roadmap](ROADMAP.md).

---

## Contributing

Issues, PRs, and discussions are all welcome.

- **Find work** — browse the [open issues](https://github.com/elicify-ai/omnipus/issues).
- **Ask a question** — [SUPPORT.md](SUPPORT.md) points you to the right channel.
- **Set up to build** — [CONTRIBUTING.md](CONTRIBUTING.md).
- **Community expectations** — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- **Report a vulnerability** — [SECURITY.md](SECURITY.md).
- **Before your first PR** — sign the one-time [Contributor License Agreement](CLA.md). (The Omnipus name and logo are reserved per the [trademark policy](TRADEMARKS.md).)
- **Internal context** — BRDs, ADRs, specs, and designs live in the [internal documentation](docs/internal/README.md).

## License

MIT · [omnipus.ai](https://omnipus.ai)
