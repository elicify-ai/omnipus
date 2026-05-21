<div align="center">
<img src="src/assets/logo/omnipus-avatar.svg" alt="Omnipus" width="240">

<h1>Omnipus</h1>

<h3>A team of AI agents you actually own.</h3>

<p>Five named agents that hand off to each other, remember what happened, and run inside a kernel-level sandbox. One Go binary. No database. No SaaS. No telemetry. Boot it on a $10 VPS and you're done.</p>

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

**Mia routes by intent.** Tell her what you need — "I need an agent to help me build a marketing website" — and she picks the right teammate, says why, and hands off in 12 ms. The receiving agent picks up in the same transcript. No copy-paste.

<img src="docs/marketing/screenshots/16-handoff-mia-to-jim.png" alt="Mia routes a website-build request to Jim by intent" width="900">

**Ray researches with sources.** Web searches fan out, results synthesise into a numbered list with citations. He won't fake an answer.

<img src="docs/marketing/screenshots/14-ray-research-demo.png" alt="Ray researches open-source agent frameworks with citations" width="900">

**Max sees the web.** `browser.navigate` → `browser.screenshot` chained in one turn. The image streams back through the media pipeline and renders inline.

<img src="docs/marketing/screenshots/13-max-screenshot-demo.png" alt="Max screenshots anthropic.com and describes the page" width="900">

**Ava builds an agent live.** Tell her what you need, watch her call `system.agent.create`, get a summary card. New agent shows up in the roster instantly.

<img src="docs/marketing/screenshots/15-ava-build-agent.png" alt="Ava builds Penny the pricing analyst" width="900">

---

## What you actually get

- **Real multi-agent orchestration.** Hand-off, sub-agents, task delegation, shared transcript, per-agent budgets — not just a chatbot with personas. → [docs/architecture/AS-IS-architecture.md](docs/architecture/AS-IS-architecture.md)
- **Memory that compounds.** Every session closes with an automatic retro and a rolling `LAST_SESSION.md`. The next turn recalls them. Single binary, no embeddings, no extra services. → [docs/memory.md](docs/memory.md)
- **Kernel-level sandbox.** Landlock + seccomp applied to the gateway process *before* `net.Listen` on Linux 5.13+. Three-tier per-tool policy (allow/ask/deny). SSRF guard wired into every outbound HTTP tool. → [docs/operations/sandbox-config.md](docs/operations/sandbox-config.md)
- **15+ chat channels** compiled in: Telegram, Discord, Slack, WhatsApp, Matrix, Line, Feishu, DingTalk, Google Chat, IRC, WeCom, Weixin, QQ, OneBot, plus Web Chat. → [pkg/channels/README.md](pkg/channels/README.md)
- **Skills & MCP.** Install reusable skill bundles from ClawHub; register MCP servers at runtime. Hooks observe or rewrite any tool call. → [docs/skills.md](docs/skills.md) · [docs/hooks/README.md](docs/hooks/README.md)
- **20+ LLM providers.** OpenRouter, Anthropic, OpenAI, Google Gemini, DeepSeek, Qwen, Moonshot, Groq, Cerebras, Mistral, MiniMax, Ollama, vLLM, Azure, GitHub Copilot, NVIDIA, Volcengine, ModelScope, Zhipu, and more. Fallback chains, multi-key rotation, streaming, vision. → [docs/providers.md](docs/providers.md)

---

## Install

### One-liner (recommended)

```bash
curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
omnipus gateway
# open http://localhost:5000
```

Detects your OS+arch, fetches the matching binary from the latest GitHub Release, verifies SHA256, installs to `/usr/local/bin/omnipus`. Override with `OMNIPUS_INSTALL_DIR=$HOME/.local/bin` if you don't have sudo.

### Docker

```bash
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -p 127.0.0.1:5001:5001 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest
```

Or with compose: `curl -O https://raw.githubusercontent.com/elicify-ai/omnipus/main/docker/docker-compose.yml && docker compose up`.

### From source (contributors)

```bash
git clone https://github.com/elicify-ai/omnipus.git
cd omnipus
make build        # builds SPA + Go binary in one step
./build/omnipus gateway
```

Requires Go 1.26+ and Node 24+. `make build` runs `spa-embed` first so `go:embed` picks up the latest Vite output.

### First boot

Two ports open: **5000** for SPA + API, **5001** for sandboxed agent preview iframes. The onboarding wizard runs on first visit: Welcome → Provider → API Key → Model → Admin Account → Done.

A 256-bit AES key auto-generates at `~/.omnipus/master.key` (mode `0600`). **Back it up** — losing it means losing every encrypted credential. For headless deployments, pre-provision via `OMNIPUS_KEY_FILE` or `OMNIPUS_MASTER_KEY`. → [docs/credential_encryption.md](docs/credential_encryption.md)

**Supported platforms in v0.1:** Linux amd64, Linux arm64, macOS arm64. → [docs/operations/platform-support.md](docs/operations/platform-support.md)

---

## Documentation

| Topic | Where to go |
|---|---|
| Get started | This README + [docs/README.md](docs/README.md) index |
| Architecture | [AS-IS architecture](docs/architecture/AS-IS-architecture.md), [ADRs](docs/architecture/) (16 decisions) |
| Memory system | [docs/memory.md](docs/memory.md) |
| Channels (per-channel guides) | [pkg/channels/README.md](pkg/channels/README.md) |
| Hooks (subprocess + in-process) | [docs/hooks/README.md](docs/hooks/README.md) |
| Skills & MCP | [docs/skills.md](docs/skills.md) · [docs/tools_configuration.md](docs/tools_configuration.md) |
| Tools reference (full catalog) | [docs/tools-reference.md](docs/tools-reference.md) |
| Sandbox: config & status | [docs/operations/sandbox-config.md](docs/operations/sandbox-config.md) |
| Sandbox: known limitations | [docs/operations/sandbox-limitations.md](docs/operations/sandbox-limitations.md) |
| Reverse proxy & TLS | [docs/operations/reverse-proxy.md](docs/operations/reverse-proxy.md) |
| Credential vault | [docs/credential_encryption.md](docs/credential_encryption.md) |
| Configuration reference | [docs/configuration.md](docs/configuration.md) |
| Troubleshooting | [docs/troubleshooting.md](docs/troubleshooting.md) |
| Business requirements (intent of record) | [docs/BRD/Omnipus BRD.md](docs/BRD/Omnipus%20BRD.md) + 5 appendices |
| Active specs & future designs | [docs/specs/](docs/specs/) · [docs/design/](docs/design/) |

Browse the full index at [docs/README.md](docs/README.md).

---

## Architecture in 10 seconds

```
                    ┌────────────────────┐
                    │   Web UI (SPA)     │   React 19 · Vite 6 · shadcn/ui
                    │   embedded via     │
                    │   go:embed         │
                    └─────────┬──────────┘
                              │ HTTP · WebSocket · SSE
                    ┌─────────┴──────────┐
                    │      Gateway       │   auth, rate limits, CORS
                    └─────────┬──────────┘
                              │
         ┌────────────────────┼────────────────────┐
         │                    │                    │
   ┌─────┴──────┐      ┌──────┴──────┐      ┌──────┴──────┐
   │ Agent Loop │      │ Policy      │      │ Audit       │
   │ + Hooks    │◄────►│ Engine      │      │ Logger      │
   │ + Tools    │      │ allow/ask/  │      │ JSONL +     │
   │ + Handoff  │      │ deny        │      │ redaction   │
   └─────┬──────┘      └─────────────┘      └─────────────┘
         │
   ┌─────┴──────┐      ┌─────────────┐      ┌─────────────┐
   │  Channels  │      │  Sandbox    │      │ Credentials │
   │ 15 compiled│      │ Landlock +  │      │ AES-256-GCM │
   │ in Go      │      │ seccomp +   │      │ Argon2id KDF│
   └────────────┘      │ SSRF guard  │      └─────────────┘
                       └─────────────┘
```

Single Go binary. File-based JSON/JSONL storage at `~/.omnipus/`. No Postgres, no Redis. WhatsApp uses pure-Go SQLite (`modernc.org/sqlite`) in its own session namespace.

---

## Tech stack

**Backend:** Go 1.26+ · `chromedp` · `whatsmeow` · `discordgo` · `telebot` · `slack-go` · `golang.org/x/sys/unix` (Landlock, seccomp).

**Frontend:** TypeScript · React 19 · Vite 6 · shadcn/ui (Radix + Tailwind v4) · AssistantUI · Phosphor Icons · Zustand · TanStack Query/Router.

---

## Status

Pre-1.0, active development on [`feature/iframe-preview-tier13`](https://github.com/elicify-ai/omnipus/tree/feature/iframe-preview-tier13). Three shipping variants share the same Go core and `@omnipus/ui` components:

1. **Omnipus Open Source** (this repo, primary) — single binary, embedded SPA, community focus.
2. **Omnipus Desktop** — Electron wrapper with native menus and auto-update.
3. **Omnipus Cloud / SaaS** — hosted variant with team features.

---

## Contributing

Issues, PRs, discussions — all welcome. Start with the [BRD](docs/BRD/Omnipus%20BRD.md) for context, browse [open issues](https://github.com/elicify-ai/omnipus/issues) for live work, or skim the [ADRs](docs/architecture/) for design decisions already locked.

## License

MIT · [omnipus.ai](https://omnipus.ai)
