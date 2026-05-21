<div align="center">
<img src="src/assets/logo/omnipus-avatar.svg" alt="Omnipus" width="240">

<h1>Omnipus</h1>

<h3>Multi-agent orchestration — sovereign, sandboxed, single binary.</h3>

<p>An opinionated agent runtime with five named coworkers, hand-off between them, a Landlock+seccomp sandbox applied to the gateway itself on Linux 5.13+, and 15 chat channels. One <code>go build</code>, no database, runs on a $10 VPS.</p>

<p>
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/React-19-61DAFB?style=flat&logo=react&logoColor=white" alt="React">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  <a href="https://omnipus.ai"><img src="https://img.shields.io/badge/Website-omnipus.ai-D4AF37?style=flat&logo=google-chrome&logoColor=white" alt="Website"></a>
</p>

<img src="docs/marketing/screenshots/04-agents-roster.png" alt="Omnipus agent roster" width="900">

</div>

---

## Why Omnipus

Most agent frameworks give you orchestration **or** a security story. Omnipus ships both, in a single Go binary, without pulling in Postgres, Redis, or a Python runtime.

- **Five named agents out of the box** — not one general-purpose chatbot, but a team with defined roles and delegation rules.
- **Real hand-off, not fake role-play** — agents pass control through a transcript the next agent can actually read.
- **Deny-by-default security** — Landlock + seccomp applied to the gateway process before it listens (Linux 5.13+; app-level fallback elsewhere), SSRF guard wired into every outbound-HTTP tool, three-tier tool policy (allow / ask / deny), encrypted credential store.
- **Runs anywhere** — single static binary, embedded SPA, auto-generates its own encryption key on first boot. Works on a laptop, a $10 VPS, or a Raspberry Pi.

---

## The five core agents

<img src="docs/marketing/screenshots/04-agents-roster.png" alt="Agents roster" width="900">

| Agent | Role | What they do |
|---|---|---|
| **Mia** | Coach & Guide | Default agent. Onboards you to the platform, explains features, answers setup questions. |
| **Jim** | General Purpose | Warm, fast, reliable. Research, writing, analysis, coordination with other agents. |
| **Ava** | Agent Builder | Interviews you about what you need, then creates a custom agent with tools, persona, and prompt. |
| **Ray** | Researcher | Deep research with citations. Web search, web fetch, synthesis — then hands visual/automation work to Max. |
| **Max** | Automator | Browser automation, plan-then-execute, multi-step orchestration with approval gates. |

Identity (name, description, color, icon, prompt) is **locked** on core agents — users can change their model and tool policy, but can't silently replace Mia with a knock-off. Custom agents are unlimited.

---

## Live demos

All screenshots below are real conversations captured against the running binary.

### Max screenshots a page, inline

Ask Max to screenshot a URL. He chains `browser.navigate` → `browser.screenshot`, the image streams back into the chat through the media pipeline, and renders inline so you can read the page without leaving the conversation.

<img src="docs/marketing/screenshots/13-max-screenshot-demo.png" alt="Max screenshots anthropic.com and describes the page in one sentence" width="900">

### Ray researches with sources

Ray fans out web searches, synthesises, and always prints the source URLs. His prompt is tuned to refuse to bluff — if the evidence isn't there, he says so.

<img src="docs/marketing/screenshots/14-ray-research-demo.png" alt="Ray research with citations" width="900">

### Ava builds a custom agent live

Tell Ava what you need. She writes the persona, picks the tools, calls `system.agent.create`, and shows you a summary card for the new agent. It shows up in the roster immediately.

<img src="docs/marketing/screenshots/15-ava-build-agent.png" alt="Ava builds Penny the pricing analyst" width="900">

### Hand-off across agents

Mia routes by intent, not by name. Tell her what you need — "I need an agent to help me build a marketing website" — and she picks the right agent on the team, says why, and calls the `handoff` tool. The receiving agent picks up in the same transcript with full context — no copy-paste, no re-explaining. Below, Mia evaluates the request, names **Jim** ("our everyday task agent — excellent at hands-on work like writing code, creating files"), hands off in 12 ms, and Jim immediately scopes the build with five questions about the SaaS, stack, sections, design, and assets.

<img src="docs/marketing/screenshots/16-handoff-mia-to-jim.png" alt="Mia routes a website-build request to Jim by intent" width="900">

---

## What's under the hood

### Multi-agent orchestration (the differentiator)

- **Core + custom agents** — 5 named core agents ship in the binary; unlimited custom agents created through Ava or the UI.
- **Hand-off** — atomic control transfer with shared transcript and budget split.
- **Sub-agents** — spawn synchronous `subagent` or background `spawn` tool calls; cloned tool registry, budget controls, status polling.
- **Task delegation** — `task_create` / `task_update` / `task_list` wired to the heartbeat service for background execution.
- **Hook system** — observers, interceptors, approvals around every tool call.
- **Joined session store** — multi-agent conversations share a single day-partitioned JSONL transcript.

### Security posture

<img src="docs/marketing/screenshots/06-max-tools-permissions.png" alt="Per-agent tool policy" width="900">

- **Kernel sandbox applied to the gateway itself** on Linux 5.13+ — Landlock (`restrict_self`) plus a seccomp filter are installed at boot, *before* `net.Listen`, so the HTTP listener never binds unsandboxed. Pure Go via `golang.org/x/sys/unix`. Modes: `enforce` (default), `permissive` (audit-only), `off`. On unsupported kernels / macOS / Windows the backend degrades to app-level checks only — see the limitations section below.
- **Three-tier tool policy per agent** — `allow` / `ask` / `deny`. Tool names and exec commands both support `*` / `?` glob patterns; deny beats allow; interactive approval streams over the WebSocket.
- **SSRF guard wired into every outbound-HTTP tool** — `web_search` (all 7 providers), `web_fetch`, the skills installer, and the exec SSRF proxy all share one `SSRFChecker.SafeClient()`. Blocks private IP ranges, link-local, cloud metadata endpoints, and IPv6 wrappings (IPv4-mapped, 6to4, Teredo); DNS is re-resolved at connect-time to close the rebinding gap. Operator allowlist via `sandbox.ssrf.allow_internal` (IPs / CIDRs / hostnames).
- **Encrypted credential store** — AES-256-GCM with Argon2id KDF; master key auto-generated on first boot, rotation via CLI.
- **Prompt-injection guard**, per-channel rate limits, per-binary exec allowlists.
- **Audit log** — structured JSONL with two-layer redaction: a sensitive-key-name layer (`password`, `api_key`, `authorization`, `bearer`, `client_secret`, and ~15 more, case-insensitive, recursing into nested maps and arrays) plus a value-pattern layer for API-key shapes, Bearer tokens, and emails.

### Security limitations and known gaps

The sandbox is deliberately scoped; be precise about what it does and doesn't do:

- **No LSM enforcement on macOS, Windows, or Linux < 5.13.** The sandbox selects a `FallbackBackend` and enforcement reduces to in-process policy checks. The BRD's Windows story (Job Objects + Restricted Tokens + DACL) is specified in Appendix A but not yet implemented.
- **Permissive mode downgrades to audit-only skip on kernels < 6.12.** Native permissive `landlock_restrict_self` is not available; Omnipus logs the computed policy and installs seccomp with `SECCOMP_RET_LOG`, but does not call `restrict_self`. Plan for kernel ≥ 6.12 if you need a true log-then-enforce workflow.
- **`sandbox.ssrf.allow_internal`** accepts exact hostnames, exact IPs, and CIDR ranges. Glob host patterns (`*.internal.corp`) are **not** supported yet.

When `OMNIPUS_ENV=production` is set and the sandbox is `off` or `permissive`, the gateway prints a multi-line warning to stderr at boot and every 60 seconds thereafter. The banner is not silenceable by design.

### Operator configuration

The sandbox is configured from the SPA at **Settings → Security → Process Sandbox**. Live backend status (`landlock-v4` here), mode toggle, allowed filesystem paths, SSRF policy presets, and the default per-agent profile all live in one panel.

<img src="docs/marketing/screenshots/17-sandbox-settings.png" alt="Settings → Security → Process Sandbox showing landlock-v4 active, Enforce mode selected, allowed-paths input, SSRF policy presets, and the default per-agent profile" width="900">

Operator notes:

- **Mode**: `enforce` | `permissive` | `off`. Persisted under `sandbox.mode` in `~/.omnipus/config.json`. Changes take effect at the next gateway restart — the UI surfaces a "Restart required" banner when the saved mode diverges from the running one.
- **CLI override**: `./omnipus gateway --sandbox=enforce|permissive|off` always trumps the config value, useful for one-shot debugging without persisting state.
- **Apply/Install failure** on a kernel that claims Landlock support aborts boot with exit code **78** (`EX_CONFIG`); the HTTP listener never binds. Other boot failures keep exit 1.
- **Status endpoints**: `GET /health` returns `sandbox.{applied, mode, backend}` always, and conditionally `disabled_by`, `audit_only`, `landlock_enforced`, `seccomp_enforced` when relevant. `GET /api/v1/security/sandbox-status` (admin-only) returns the full apply-state including backend capabilities and the bind-port allow-list.

### Built-in tools (27 loaded by default)

Files (`read_file`, `write_file`, `edit_file`, `append_file`, `list_dir`), shell (`exec` with PTY + approval), web (`web_search`, `web_fetch`), tasks (`task_create` / `update` / `delete` / `list`), agents (`agent_list`, `subagent`, `spawn`, `spawn_status`, `handoff`, `return_to_default`), browser (`navigate`, `click`, `type`, `screenshot`, `get_text`, `wait`), skills (`find_skills`, `install_skill`), comms (`message`, `send_file`), scheduling (`cron`), and more. Additional tools register from MCP servers at runtime.

### Connectivity

<img src="docs/marketing/screenshots/10-settings.png" alt="Provider matrix" width="900">

**20+ LLM providers** compiled in — OpenRouter, Anthropic, OpenAI, Google Gemini, DeepSeek, Qwen, Moonshot, Groq, Cerebras, Mistral, MiniMax, Ollama, vLLM, Azure, GitHub Copilot, Volcengine, ModelScope, NVIDIA, Avian, LongCat, Shengsuanyun, Vivgrid, Zhipu. Fallback chains, multi-key rotation, streaming, vision.

**15 chat channels** — Web Chat, Telegram, Discord, Slack, Matrix, WhatsApp, Line, QQ, WeCom, Weixin, IRC, Feishu, DingTalk, Google Chat, OneBot. All compiled in; no external services needed.

### Operator surfaces

| | |
|---|---|
| <img src="docs/marketing/screenshots/08-command-center.png" alt="Command Center" width="420"> | **Command Center** — gateway status, agent summary, task board, activity feed, rate-limit events, approval queue. |
| <img src="docs/marketing/screenshots/07-ava-profile.png" alt="Agent profile" width="420"> | **Agent profile** — model, temperature, per-tool policy, session history, activity timeline. Identity fields read-only on core agents. |
| <img src="docs/marketing/screenshots/12-agent-picker-menu.png" alt="Agent picker" width="420"> | **Agent picker** — switch who you're talking to in one click; sessions stay with the session, not the agent. |
| <img src="docs/marketing/screenshots/01-login.png" alt="Login" width="420"> | **Dark-first UI** — "The Sovereign Deep" design system: Deep Space Black, Liquid Silver, Forge Gold accents. Chat-first, no separate canvas. |

---

## Install

### Quick install (recommended)

```bash
curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
omnipus gateway
# open http://localhost:5000
```

The script detects your OS+arch, downloads the matching binary from the latest GitHub Release, verifies its SHA256, and installs to `/usr/local/bin/omnipus`. Override the target with `OMNIPUS_INSTALL_DIR=$HOME/.local/bin` if you don't have sudo.

### Docker

```bash
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -p 127.0.0.1:5001:5001 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest

# open http://localhost:5000
```

Or with compose:

```bash
curl -O https://raw.githubusercontent.com/elicify-ai/omnipus/main/docker/docker-compose.yml
docker compose --profile gateway up
```

### Build from source (contributors)

Requires Go 1.26+ and Node 24+:

```bash
git clone https://github.com/elicify-ai/omnipus.git
cd omnipus
make build               # builds SPA + Go binary in one step
./build/omnipus gateway  # open http://localhost:5000
```

`make build` automatically runs the `spa-embed` target (Vite build + copy into `pkg/gateway/spa/` for `go:embed`) before compiling.

### First boot

The gateway opens **two ports**: `5000` for the SPA + API, `5001` for preview iframes (isolated origin). Both must be reachable from your browser.

Onboarding wizard runs on first visit to `http://localhost:5000`: Welcome → Provider → API Key → Model → Admin Account → Done.

First boot also auto-generates an encryption key at `~/.omnipus/master.key` (mode `0600`). **Back it up** — losing it makes the credential store unrecoverable. For headless deployments, pre-provision via `OMNIPUS_KEY_FILE` or `OMNIPUS_MASTER_KEY`.

Rotate the key any time:

```bash
omnipus credentials rotate --old-key-file old.key --new-key-file new.key
```

### Platform support

**Officially supported in v0.1 (CI-tested):**

| OS | Architecture | Notes |
|---|---|---|
| Linux | amd64 (`x86_64`) | Full Landlock + seccomp sandbox on kernel 5.13+ |
| Linux | arm64 (`aarch64`) | Same sandbox support |
| macOS | arm64 (Apple Silicon) | App-level fallback sandbox (no LSM) |

**Planned but not in v0.1** (use source build or Docker for now; tracked for v0.1.1+):

- **macOS amd64 (Intel)** — cross-compile path works; needs CI smoke test on a `macos-13` runner.
- **Windows amd64** — ~15 unit tests assume POSIX semantics; tracked in [#113](https://github.com/elicify-ai/omnipus/issues/113).
- **Linux riscv64, loong64, armv7, mipsle** — Go cross-compile targets exist; no CI verification.
- **FreeBSD, NetBSD** — Go cross-compile targets exist; no GitHub Actions runners available.

If you need a deferred platform, build from source — most still compile with `make build`. The kernel sandbox falls back to app-level enforcement on any non-Linux platform.

---

## Architecture

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
   │ 17 compiled│      │ Landlock +  │      │ AES-256-GCM │
   │ in Go      │      │ seccomp +   │      │ Argon2id KDF│
   └────────────┘      │ SSRF guard  │      └─────────────┘
                       └─────────────┘
```

Single binary. File-based storage (`~/.omnipus/` — JSON + JSONL, atomic writes). No Postgres. No Redis. WhatsApp uses pure-Go SQLite (`modernc.org/sqlite`) in its own session namespace.

---

## Tech stack

**Backend:** Go 1.21+ · `chromedp` (browser) · `whatsmeow` (WhatsApp) · `discordgo` · `telebot` · `slack-go` · `go-nostr` · `modernc.org/sqlite` · `golang.org/x/sys/unix` (Landlock, seccomp)

**Frontend:** TypeScript · React 19 · Vite 6 · shadcn/ui (Radix + Tailwind CSS v4) · AssistantUI · Phosphor Icons · Zustand · TanStack Query / Router · Framer Motion

**Storage:** File-based JSON / JSONL. Day-partitioned session transcripts with configurable retention (default 90 days) and single-layer context compression (drops ~50% of oldest turns and writes a summary note when the token budget is exceeded).

---

## Status

Pre-1.0. Three shipping variants:

1. **Omnipus Open Source** (primary, ships first) — single Go binary, embedded web UI, community focus. This repo.
2. **Omnipus Desktop** — Electron wrapper with native menus and auto-update.
3. **Omnipus Cloud / SaaS** — hosted variant with team features and managed infrastructure.

All three share the same Go core and the `@omnipus/ui` React components.

Active development on [`feature/iframe-preview-tier13`](https://github.com/elicify-ai/omnipus/tree/feature/iframe-preview-tier13).

---

## Specification

The full design is written down, not vibes:

| Document | Scope |
|---|---|
| [Main BRD](docs/BRD/Omnipus%20BRD.md) | 30 security + 36 functional requirements, delivery phases |
| [Appendix A](docs/BRD/Omnipus%20Windows%20BRD%20appendic.md) | Windows kernel security (Job Objects, Restricted Tokens, DACL) |
| [Appendix B](docs/BRD/Omnipus_BRD_AppendixB_Feature_Parity.md) | Feature parity requirements vs. the Claw ecosystem |
| [Appendix C](docs/BRD/Omnipus_BRD_AppendixC_UI_Spec.md) | Full UI / UX spec |
| [Appendix D](docs/BRD/Omnipus_BRD_AppendixD_System_Agent.md) | System agent and system tools |
| [Appendix E](docs/BRD/Omnipus_BRD_AppendixE_DataModel.md) | File-based data model and directory structure |

---

## Contributing

Issues, PRs, discussions — all welcome. Start with the BRD, then browse [open issues](https://github.com/elicify-ai/omnipus/issues) for work in progress.

## License

MIT · [omnipus.ai](https://omnipus.ai)
