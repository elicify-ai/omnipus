# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Omnipus is an agentic core: a single Go binary with the SPA embedded via `go:embed`, kernel-level sandboxing (Landlock + seccomp on Linux 5.13+), RBAC, audit logging, encrypted credential management, and compiled-in Go channels. Community-facing, MIT-licensed, no telemetry.

**Domain:** omnipus.ai

## Status

Active development. Substantial parts of the system are implemented and running on `main`: the agent loop and turn engine (`pkg/agent/`), 5 core agents (`pkg/coreagent/`), 35 `system.*` tools defined in `pkg/sysagent/tools/`, the tool registry and MCP integration (`pkg/tools/`, `pkg/mcp/`), skills loader and ClawHub registry (`pkg/skills/`), session/memory storage (`pkg/session/`, `pkg/memory/`), the gateway with embedded SPA (`pkg/gateway/`), credential boot contract, audit/policy/sandbox wiring, and ~16 in-process Go channels (Telegram, Discord, Slack, Matrix, IRC, Teams, Google Chat, WhatsApp, …). Onboarding flow, REST + WebSocket APIs, and the React SPA are functional.

> **Note on the historical "system agent" naming.** Earlier docs and the BRD describe an `omnipus-system` agent as a distinct always-on agent that holds the `system.*` tools. **There is no such runtime agent.** The 35 `system.*` tools are ordinary builtins; per-agent policy decides which agent can call which one (see `docs/specs/tool-registry-redesign-spec.md`). The `pkg/sysagent/` package name is preserved as a tool-grouping namespace only — it does not represent an agent. All references below have been updated to reflect this; the central tool registry redesign is complete.

Work in progress includes a unified plugin system (issue #151), the Signal channel and a proto-installer for plugin-style channel install/uninstall (currently unpushed in a sibling clone), and various security/UX hardening sprints.

Authoritative architecture references:
- `docs/architecture/AS-IS-architecture.md` — evidence-based as-is, code-cited.
- `docs/architecture/plugin-extensibility-assessment.md` — plugin/extension status across channels, tools, skills, MCP.
- `docs/architecture/ADR-*.md` — accepted architectural decisions.

Background specs in `docs/BRD/` describe original intent and remain useful for context, but where they disagree with the code or the as-is document, the code wins:

- `Omnipus BRD.md` — Main BRD: security + functional requirements, delivery phases
- `Omnipus Windows BRD appendic.md` — Appendix A: Windows kernel security (Job Objects, Restricted Tokens, DACL)
- `Omnipus_BRD_AppendixB_Feature_Parity.md` — Appendix B: feature parity requirements
- `Omnipus_BRD_AppendixC_UI_Spec.md` — Appendix C: UI/UX spec
- `Omnipus_BRD_AppendixD_System_Agent.md` — Appendix D: system agent + 35 system tools
- `Omnipus_BRD_AppendixE_DataModel.md` — Appendix E: file-based data model
- `OpenClaw_vs_Omnipus_Comparison.md` — competitive analysis

## Release Strategy (v0.1 → v0.2 → v0.3)

Three-phase plan locked 2026-05-03 to resolve the dilemma of an unstable WIP branch + a pentest backlog + a large memory/projects redesign.

**v0.1 — Stabilize current branch (`feature/iframe-preview-tier13`).** Ship the in-flight `web_serve` unification, kernel-enforced bind-port allow-list, sandbox-aware `exec`, and iframe preview as one focused PR. Plan: `/home/Daniel/.claude/plans/quizzical-marinating-frog.md`. No memory/projects scope creep — that is explicitly v0.3.

**v0.2 — Security hardening (pentest quick wins).** GitHub issue [#155](https://github.com/dapicom-ai/omnipus/issues/155). Quick fixes only — no architectural changes. Items: env var allowlist switch (`pkg/sandbox/hardened_exec.go::sensitiveEnvKeys`), `master.key` 0600 verification, shell-guard hardening, internal-CIDR egress blocking, audit log integrity (HMAC chain), rate limiting on auth endpoints. Defers structural fixes (process isolation, capability-based RBAC) to v0.3.

**v0.3 / 1.0 — "Rooms" redesign (memory + projects + tasks + sandbox topology).** GitHub issue [#156](https://github.com/dapicom-ai/omnipus/issues/156). Fresh-build, no backward compatibility. Five locked design docs:
- `docs/design/sandbox-redesign-2026-05.md` — two-room workspace topology (private agent rooms + shared project rooms under `.omnipus/`).
- `docs/design/memory-redesign-2026-05.md` — 4-tier memory (sessions / memories / learnings / last-session.md), three tools (`remember`/`recall`/`retrospective`), Dreamcatcher consolidation pass, bleve + JSONL + MinHash, MOCs, no embeddings.
- `docs/design/tasks-redesign-2026-05.md` — tasks scoped per-room, cascade-delete with project, reassignment audit.
- `docs/design/projects-ui-2026-05.md` — three SPA surfaces (session creation modal, Command Center pivoted to rooms, session history with grouping).
- `docs/design/settings-notifications-2026-05.md` — Memory + Dreamcatcher settings tabs, tier-based retention notifications.

**Routing rule:** when the user brings up new work, ask which release phase it belongs to before starting. Pentest findings → v0.2 unless they require structural changes (then → v0.3). Memory / projects / tasks / room-topology work → v0.3. Anything else that isn't completing v0.1 → flag the scope question explicitly.

## Hard Constraints

These are non-negotiable and apply to every decision:

1. **Single Go binary (agentic core)** — all backend features compile into one binary. No new runtime dependencies. The SPA is embedded via `go:embed`.
2. **Pure Go** — no CGo, no external C libraries, no shelling out for security-critical paths. Use `golang.org/x/sys/unix` for kernel interfaces.
3. **Minimal footprint** — total RAM overhead for all security features must stay under 10MB beyond baseline.
4. **Graceful degradation** — features requiring Linux 5.13+ (Landlock, seccomp) must fall back to application-level enforcement on older kernels, non-Linux platforms, and Android/Termux.
5. **Ecosystem compatibility** — follows Omnipus/OpenClaw conventions (SKILL.md, HEARTBEAT.md, SOUL.md, AGENTS.md, JSON config patterns) for skill ecosystem and community compatibility. Omnipus has its own config format but adopts the same concepts.
6. **Deny-by-default for security, opt-in for features** — security policies default to most restrictive; functional features default to disabled. **Documented exception:** when a sandbox mode (`enforce` or `permissive`) is active, the workspace shell tools (`workspace.shell`, `workspace.shell_bg`) are enabled by default for Jim. Rationale: the kernel sandbox itself is the protective layer, and Jim's seed forces `experimental.workspace_shell_enabled = true` at config-creation time anyway — making the helper-default `false` only creates a test-vs-production behavioral gap, not real safety. Operators who want shell tools fully off must set `experimental.workspace_shell_enabled = false` explicitly. With sandbox `off` (god-mode), no implicit defaults apply — operator opt-in is required.
7. **Release responsibility — fix everything, no excuses.** Every branch must be fully green before it ships. Pre-existing failures (lint, vuln, Go test, race, vitest, tsc, Playwright, anything CI runs) are our responsibility to fix regardless of who introduced them. "Pre-existing", "not introduced by my work", "broken on main too" are NEVER acceptable closure paths for an observed failure. Either fix it now, or get explicit user approval to defer with a tracked issue + target date. The release contract is full functionality; we do not ship around known failures.
8. **Contract-first wire formats — single source of truth, runtime-validated.** Every byte that crosses the gateway/SPA boundary (REST request/response, WS frame, persisted JSON consumed by the SPA) MUST be defined in `contracts/openapi.yaml` or `contracts/asyncapi.yaml` **before** any Go or TypeScript code is written. Generated types in `pkg/api/generated/` and `src/lib/api/generated/` are the only legal cross-boundary types — they are committed to the repo, regenerated via `scripts/gen-contracts.sh`, and verified in CI by `make verify-contracts` (fails on drift). **Hand-written wire-format types are FORBIDDEN and actively caught by lint:** (a) any package-level struct in `pkg/gateway/*.go` (non-test, non-generated) that has ≥2 `json:` tags is flagged — opt-out with `// not-wire-format` for internal structs that are not wire types; (b) any `export interface` or `export type = { }` (object-literal) in `src/lib/api.ts` or `src/lib/ws.ts` is flagged — opt-out with `// not-wire-format` for internal callback/helper interfaces. AsyncAPI Zod schemas are generated (not hand-written): `scripts/_gen-asyncapi-types.mjs` emits `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts`, which `scripts/_gen-ts.sh` concatenates into `src/lib/api/generated/schemas.ts`; the hand-written `scripts/_asyncapi-zod-schemas.ts` is deprecated and no longer used. SPA edge validates every incoming payload through the matching zod schema (drop + dropped-frame counter + dev-mode toast on failure; no production crash, no error UI clutter). Backend `pkg/api/generated/contract_test.go` fails on any Go struct that produces schema-invalid JSON. Adding a new wire type means: (a) add schema to `contracts/components/schemas/`, (b) reference it from `openapi.yaml` or `asyncapi.yaml`, (c) run `scripts/gen-contracts.sh`, (d) commit the generated diff, (e) write the handler/consumer using the generated type. Steps in any other order are an error.

## Tech Stack

**Backend:** Go (targeting Go 1.22+ — go.mod requires go 1.26.3; `//go:build go1.22` tags in generated files; `slog` added in 1.21). Key packages: `golang.org/x/sys/unix` (Landlock, seccomp), `chromedp` (browser automation), `whatsmeow` (WhatsApp), `discordgo` (Discord), `telebot` (Telegram), `slack-go` (Slack), `go-nostr` (Nostr), `modernc.org/sqlite` (pure Go SQLite for whatsmeow — no CGo). All channels currently in the codebase are compiled into the single binary as in-process Go implementations. Channels that depend on a non-Go runtime (e.g. Signal, which requires `signal-cli`/JRE) wrap the dependency by spawning a sidecar binary from inside their own `Start()` and communicating with it over local HTTP (Signal) or WebSocket (WhatsApp bridge). There is no generic stdio "bridge protocol"; HTTP-localhost is the de facto pattern.

**Frontend:** TypeScript, React 19, Vite 6, shadcn/ui (Radix + Tailwind CSS v4), AssistantUI (chat), Phosphor Icons (`@phosphor-icons/react`), Zustand (UI state), TanStack Query (server state), TanStack Router, Framer Motion. The SPA is built by Vite into `dist/spa/`, copied into `pkg/gateway/spa/`, and embedded into the Go binary via `go:embed`.

**Storage:** File-based only (JSON/JSONL) for all Omnipus data. No PostgreSQL or Redis. Exception: WhatsApp session uses SQLite via whatsmeow with `modernc.org/sqlite` (pure Go, no CGo). SQLite is isolated to WhatsApp session storage only — never used for Omnipus's own data. Data directory: `~/.omnipus/`. Atomic writes (temp file + rename). Credentials in `credentials.json` (AES-256-GCM encrypted, Argon2id KDF), never in `config.json`. **Sessions:** Day-partitioned JSONL transcripts (`sessions/<id>/<YYYY-MM-DD>.jsonl`) with configurable retention (default 90 days). **Context compression** is single-layer: when the token budget is exceeded, `forceCompression` (`pkg/agent/loop.go:4473-4550`) drops ~50% of the oldest turns and writes a summary note via `SetHistory` + `Save`. The historical claim of a second "tool result pruning" pass is not implemented today. **Concurrency:** per-entity files for high-contention data (tasks, pins). Sessions and memory use a 64-shard mutex pool keyed by FNV hash of session ID (`pkg/memory/jsonl.go:21-77`), not a single-writer goroutine. Atomic writes via temp-file + rename (`fileutil.WriteFileAtomic`). Advisory `unix.Flock` on Linux/macOS (`pkg/fileutil/flock_unix.go:18-22`); on Windows, `LockFileEx` is **not** used — the code relies on the single-writer goroutine pattern instead (see `pkg/fileutil/flock_windows.go:15`).

**Credential provisioning:** All secrets are stored in `credentials.json` (AES-256-GCM, Argon2id KDF). See [ADR-004](docs/architecture/ADR-004-credential-boot-contract.md) for the full boot contract.

**Unlock modes** (tried in priority order):

1. `OMNIPUS_MASTER_KEY` — 64-char hex-encoded 256-bit key in the environment. Use for CI/CD pipelines and container deployments where secrets are injected via env.
2. `OMNIPUS_KEY_FILE` — path to a file (mode 0600) containing the hex key. Use for long-running server deployments where mounting a key file is more practical than env injection.
3. **Default key file** — if `$OMNIPUS_HOME/master.key` exists (mode 0600), it is loaded automatically. This is how auto-generated keys survive across reboots without any env configuration.
4. **Auto-generate on fresh install** — if no key is configured and no `credentials.json` exists yet, the gateway mints a fresh 256-bit key, writes it to `$OMNIPUS_HOME/master.key` with 0600, and logs a prominent backup warning to stderr. This closes the headless first-run chicken-and-egg: a new user on a cloud VPS can start the gateway with zero configuration and still end up with a working encrypted credential store. Auto-generate **never** fires when an existing `credentials.json` is present — that would strand the encrypted data.
5. **Interactive TTY prompt** — passphrase entered at the terminal. Only works when a TTY is attached; never use for headless/daemon mode.

**Critical — back up the master key file.** Whether you provide it via `OMNIPUS_KEY_FILE`, or it was auto-generated to `$OMNIPUS_HOME/master.key` on first boot, losing it makes every credential in `credentials.json` (API keys, channel tokens, etc.) permanently inaccessible. The auto-generate path prints a multi-line warning to stderr on first boot — watch for it in systemd journal / Docker logs.

**Generating a key file manually** (for operators who prefer explicit provisioning over auto-generate):

```bash
openssl rand -hex 32 > /var/lib/omnipus/master.key
chmod 600 /var/lib/omnipus/master.key
export OMNIPUS_KEY_FILE=/var/lib/omnipus/master.key
```

**Key rotation:** Run `omnipus credentials rotate` — the command takes no arguments (`cobra.NoArgs` per `cmd/omnipus/internal/credentials/command.go::newRotateCommand`) and is **passphrase-based**: it unlocks the store via the current mode (env var / key file / interactive prompt), then prompts twice for the new passphrase, then calls `store.RotateWithPassphrase` which atomically re-encrypts every credential under the new key. A `--key-file` flag was never wired up; the rotation path is passphrase-only today. For headless key-file deployments the operational workflow is: stop the gateway, replace `$OMNIPUS_HOME/master.key` with a freshly minted hex key, restart, and re-onboard any agent secrets via `omnipus credentials set` (or the Settings → Security → Credential Vault UI). There is no zero-downtime rotation path in the current CLI — a brief restart is required.

**Boot order:** `NewStore → Unlock → LoadConfigWithStore → InjectFromConfig → ResolveBundle → RegisterSensitiveValues → NewManager → Start` — any failure aborts boot. Channel secrets are passed directly as a `credentials.SecretBundle` to channel constructors; they do not require environment injection.

## Architecture Patterns

**Platform abstraction for sandboxing:** `SandboxBackend` interface with Linux (Landlock+seccomp), Windows (Job Objects+Restricted Tokens+DACL), and Fallback (app-level) backends. Policy engine and audit logging are cross-platform; only enforcement backend varies.

**Channel model:** All channels implement the same `Channel` Go interface (`pkg/channels/base.go:47-56`) plus opt-in capability interfaces (`TypingCapable`, `MessageEditor`, `MessageDeleter`, `ReactionCapable`, `PlaceholderCapable`, `StreamingCapable`, `CommandRegistrarCapable` — see `pkg/channels/interfaces.go:13-70`). Each channel registers a factory at compile time via `channels.RegisterFactory(name, factory)` from a `func init()` in its subpackage (`pkg/channels/registry.go`); activation is then a hardcoded if-ladder over typed config fields in `Manager.initChannels()` (`pkg/channels/manager.go:433-530`). Channels communicate with the agent loop only through the in-process `MessageBus` (`pkg/bus/bus.go`). Channels that wrap an external dependency embed the bridge directly inside their own implementation: WhatsApp uses a WebSocket to a separate bridge process (`pkg/channels/whatsapp/whatsapp.go:31-46`); the in-flight Signal channel spawns `signal-cli-rest-api` as a sidecar and talks to it over HTTP on localhost. There is **no** `BridgeAdapter` type, **no** stdio bridge protocol, and **no** Channel SDK in the codebase today. A generalized plugin/installer is in scoping — see issue #151 and the proto-installer in the unpushed `omnipus-channel-signal` clone (`pkg/channelmanager/`).

**Agent types:** Core (5 agents with prompts compiled into the binary via `pkg/coreagent/core.go:24-150`; identity locked, user can toggle/configure model and tools) and Custom (user-defined). There is **no separate "system" agent**. The 35 `system.*` tools defined in `pkg/sysagent/tools/` are ordinary builtins registered on the central tool registry; per-agent policy (allow/ask/deny, with `system.*: deny` seeded by default on custom agents) decides exposure. The post-redesign code retires the `omnipus-system` naming and removes `WireSystemTools` / `WireAvaAgentTools` as code paths — see `docs/specs/tool-registry-redesign-spec.md`. Note: `config.AgentTypeSystem` (`"system"`) survives in the config schema and the `/api/v1/agents` API contract for legacy/operator-supplied entries — production `SeedConfig` does NOT seed any such entry, but if a config.json contains one, the gateway will surface it. Handler tests in `pkg/gateway/rest_test.go` exercise this contract by injecting a synthetic `omnipus-system` config entry.

The current custom-agent file format is structured: `AGENT.md` (singular) with frontmatter, plus `SOUL.md` for the prompt and `HEARTBEAT.md` for periodic instructions. The legacy `AGENTS.md` (plural) format is still loaded as a fallback (`pkg/agent/definition.go:21-22, 73, 104`) but should not be used for new agents.

**Brand:** "The Sovereign Deep" — dark-first design. Colors: Deep Space Black (`#0A0A0B`), Liquid Silver (`#E2E8F0`), Forge Gold (`#D4AF37`). Fonts: Outfit (headlines), Inter (body), JetBrains Mono (code). Octopus mascot ("Master Tasker"). See `docs/brand/brand-guidelines.md`.

**UI design rules:** Chat-first, dark-first. Sidebar defaults to overlay drawer but can be pinned for persistent navigation. No separate canvas (rich content renders inline, expands to fullscreen). No emoji in stored data or UI chrome (emoji-to-Phosphor-icon translator in chat output only). Tool calls visible by default with collapsible detail.

**Doc/code drift to be aware of.** This file describes the system at the level of intent and has drifted from the implementation in places. The evidence-based as-is lives in `docs/architecture/AS-IS-architecture.md` and the plugin extension assessment in `docs/architecture/plugin-extensibility-assessment.md`. When this file (or anything under `docs/BRD/`) disagrees with those documents or with the code, the **code is the source of truth**. Known drift items already corrected above: there is no `ChannelProvider` interface (it's `Channel`), no `BridgeAdapter` type, no stdio bridge protocol, no Channel SDK, no two-layer compression, no `LockFileEx` on Windows, **no `omnipus-system` agent** (the system-agent concept is fictional; `system.*` tools are ordinary builtins governed by per-agent policy). Issue #151 tracks the unified plugin system that will eventually subsume the channel-installer prototype. The central tool registry redesign (`docs/specs/tool-registry-redesign-spec.md`) is **complete**: `WireSystemTools`, `WireAvaAgentTools`, `ScopeSystem`, `IsSystemAgent`, `ComposeAndRegister`, and the static `builtinCatalog` slice are all removed; policy-only governance via `BuiltinRegistry` + `MCPRegistry` + per-agent `ToolPolicyCfg` atomic pointer is in place.

## Spec-Driven Workflow

Use this sequence when implementing features:

1. Read the relevant BRD section(s) before writing any code
2. Use `/plan-spec` to generate implementation specs with TDD/BDD scenarios
3. Use `/grill-spec` to stress-test specs for gaps before implementation
4. Use `/taskify` to decompose into structured task graphs
5. Implement in Plan Mode first, then switch to normal mode
6. Use `/grill-code` to verify spec compliance after implementation

## Subagent Workflow

The lead (you) orchestrates all work by spawning specialized subagents via the Agent tool. There are no agent teams — you spawn subagents directly, give them focused tasks, and review their output.

### Implementing subagents (spawn via Agent tool with `subagent_type`)
- `frontend-lead` (sonnet) — React/TypeScript UI work. Scope: `src/`, `packages/ui/`
- `backend-lead` (sonnet) — Go backend work. Scope: `pkg/`, `cmd/`, `internal/` (except security packages)
- `security-lead` (opus) — Security implementation. Scope: `pkg/security/`, `pkg/sandbox/`, `pkg/audit/`, `pkg/policy/`
- `qa-lead` (sonnet) — Tests only. Scope: `*_test.go`, `*.test.ts`, `*.test.tsx`

### Review subagents (spawn via Agent tool with `subagent_type`)
- `architect` (opus) — cross-cutting design review, ADRs
- pr-review-toolkit agents (6 total, always run all after implementation work)

### How to use subagents

1. **Decompose the work** — break the task into focused units scoped to one subagent each
2. **Spawn subagents with clear, complete prompts** — include the spec reference, the exact files to modify, and what "done" looks like. Each subagent starts fresh with no prior context.
3. **Run subagents in parallel** when their work is independent (e.g., frontend + backend for the same feature)
4. **Review every subagent's output** — check their functional proof, verify their claims, run the review pipeline
5. **Run QA after implementation** — spawn qa-lead to write tests against the code the other subagents just wrote

### Which subagents to spawn per task type
- **Frontend-only work:** frontend-lead → qa-lead
- **Backend-only work:** backend-lead → qa-lead
- **Security work:** security-lead + backend-lead → qa-lead
- **Full-stack features:** frontend-lead + backend-lead (parallel) → qa-lead
- **Design questions:** architect

### Review pipeline (run after implementation subagents complete)

**Step 1 — Project-specific reviews (in parallel):**
- Go files changed → `architect` for cross-cutting concerns
- Frontend files changed → `architect` for integration coherence
- Security files changed → `architect` for threat model review

**Step 2 — PR-review-toolkit (run ALL 6 in parallel, always):**
1. `pr-review-toolkit:code-reviewer` — CLAUDE.md compliance, bugs, quality
2. `pr-review-toolkit:code-simplifier` — simplify for clarity and maintainability
3. `pr-review-toolkit:comment-analyzer` — verify comment accuracy
4. `pr-review-toolkit:pr-test-analyzer` — test coverage quality
5. `pr-review-toolkit:silent-failure-hunter` — find silent failures, bad error handling
6. `pr-review-toolkit:type-design-analyzer` — type/interface design quality

**Step 3 — Resolve findings:**
- Fix issues found by reviews (spawn the relevant implementing subagent to fix)
- Re-run failed reviews after fixes
- Only create PR when all reviews pass

## Quality Gates

Before reporting any work done, subagents and the lead must verify all applicable gates pass:

```bash
# Backend
export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH
gofmt -l . | wc -l                                          # must be 0
golangci-lint run --build-tags=goolm,stdjson                # exit 0
CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 ./...    # exit 0
CGO_ENABLED=1 go build -tags goolm,stdjson ./...            # exit 0
govulncheck ./...                                           # 0 vulnerabilities

# Frontend
npm run typecheck     # tsc -b --noEmit — exits 0 (see WARNING below)
npx vitest run        # exit 0

# Contracts
make verify-contracts  # exit 0
```

**WARNING — TypeScript typecheck trap.** `tsconfig.json` is a project-references root with no `include`/`files` entries. Running `tsc --noEmit` (without `-b`) is a **silent no-op** — it always exits 0 even when referenced sub-projects have type errors. The correct command is `tsc -b --noEmit`. Use `npm run typecheck` which is wired to the correct command.

## Build & E2E Testing

### SPA Embed Pipeline

The Go binary embeds the frontend SPA from `pkg/gateway/spa/` via `go:embed`. This directory is **not** the Vite build output — `npm run build` outputs to `dist/spa/`. You must sync them before building the binary:

```bash
npm run build                    # builds to dist/spa/
rm -rf pkg/gateway/spa/assets    # remove stale assets
cp -r dist/spa/* pkg/gateway/spa/  # sync to embed dir
CGO_ENABLED=0 go build -o /tmp/omnipus ./cmd/omnipus/  # rebuild binary
```

**If you skip the sync, the binary will serve a stale SPA that does not include your frontend changes.** Verify with: `grep -c "YOUR_NEW_STRING" pkg/gateway/spa/assets/index-*.js` — must be >0.

### E2E Testing with the Embedded SPA

Always test against the embedded SPA (the Go binary), not the Vite dev server. The Vite dev server proxies `/api` to `localhost:18790` which may not match the gateway port.

**Start the gateway:**

```bash
export OMNIPUS_HOME=/tmp/omnipus-e2e-test
rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"
OMNIPUS_BEARER_TOKEN="" ./omnipus gateway --allow-empty &
```

**Known blockers and workarounds:**

1. **Port conflict with other apps** — Port 3000 is the default. If another app (e.g., Next.js) is running on 3000, the gateway silently fails to bind. Check with `lsof -i :3000 | grep LISTEN`. Fix: set a different port in `$OMNIPUS_HOME/config.json` under `gateway.port` (e.g., 5000) before starting.

2. **`gateway.dev_mode_bypass` — what it is and when to use it**

   The auth decision tree in `pkg/gateway/auth.go:55-98` (`checkBearerAuth`, called by `withAuth`) is:

   1. No `Authorization: Bearer …` header → 401 always. Bypass never fires.
   2. `cfg.Gateway.Users` populated → token must match a registered user.
   3. `OMNIPUS_BEARER_TOKEN` env set → token must constant-time-equal the env value.
   4. No users **and** no env token → `dev_mode_bypass: true` lets the caller through as admin (one-time stderr WARN); `dev_mode_bypass: false` returns 401 "no users configured, complete onboarding first".

   **Onboarding does NOT need bypass.** `/api/v1/state`, `/api/v1/onboarding/*`, `/api/v1/auth/login`, `/api/v1/auth/register-admin`, `/api/v1/providers`, `/api/v1/media/`, `/api/v1/uploads/` are wired with `withOptionalAuth` (see `pkg/gateway/rest.go` ~L2004-2098), which never calls `checkBearerAuth`. The SPA onboarding wizard works on a fresh install with `dev_mode_bypass: false` and zero users.

   **When to set `dev_mode_bypass: true`:**
   - Driving a `withAuth`-protected endpoint (e.g., `curl /api/v1/agents`, `/api/v1/sessions`, `/api/v1/config`) before onboarding has minted a real admin user.
   - Go test scaffolding — `pkg/gateway/routes_admin_test.go`, `websocket_m4_test.go`, etc. flip the flag so admin-route tests don't have to register users + log in just to authenticate.
   - Local-dev shells where you intentionally don't want a login step.

   **Defense-in-depth contract:** the paired `RequireNotBypass` middleware (see `TestSandboxConfigPUT_RealMux_DevModeBypass503`) explicitly returns **503** when `dev_mode_bypass=true` is set, on a hand-picked allow-list of high-blast-radius admin routes (e.g., sandbox-config PUT). The flag is loud and self-limiting by design — never set it in production, never remove the `RequireNotBypass` guard without an ADR.

   **Default: `false`.** Only flip it on for the three use cases above. The previous CLAUDE.md note claiming bypass was *required* for onboarding was incorrect and has been removed.

3. **Model must support tool use** — Omnipus sends tools with every LLM request. If the selected model doesn't support tool use (e.g., `google/gemma-2-9b-it` on OpenRouter), the LLM call returns a 404 with "No endpoints found that support tool use." Use a tool-capable model like `z-ai/glm-5-turbo`, `google/gemini-2.5-flash`, or `anthropic/claude-3.5-haiku`.

4. **Gateway logs are in `$OMNIPUS_HOME/logs/`** — `gateway.log` for runtime logs, `gateway_panic.log` for startup errors. Always check `gateway_panic.log` if the gateway exits silently.

### E2E Test Checklist

After frontend+backend changes, verify these flows on the embedded SPA:

1. **Onboarding** — Welcome → Provider → API Key → Model Select → Admin Account → Complete
2. **Chat** — Send message → receive LLM response → multi-turn context retained → token/cost updates
3. **Agents** — List (system + custom) → Create Agent (with Tools & Permissions) → Agent Profile (accordion, tools panel)
4. **System Agent** — Profile shows read-only sections only (no Identity, no Tools & Permissions)
5. **Settings** — Provider shows Connected, all tabs load
6. **Command Center** — Gateway status, task board
7. **Skills & Tools** — 4 tabs, empty states
8. **Sidebar** — All nav items, active highlighting
9. **Console errors** — Zero JS errors (WebSocket reconnect warnings are acceptable)

### Operator configuration: two-port preview

The gateway opens two listeners by default. `gateway.port` (default `5000`) serves the SPA and the authenticated API. `gateway.preview_port` (default `5001`) serves agent-generated HTML previews on a separate origin, providing browser-level isolation between the SPA's admin token and content produced by agents. Setting `gateway.preview_listener_enabled = false` **fully disables the iframe-preview feature**: the second listener is not started, and the `/preview/` path prefix is **not** registered on the main mux either, so requests to `<main-host>:<port>/preview/...` receive a 404 from the SPA catch-all. `web_serve` tool calls still mint tokens, but the URLs they hand back to the agent will not resolve. There is no fallback to single-port serving — disabling the preview listener is a full rollback of the iframe-preview feature. Re-enable and restart to restore functionality. See `docs/operations/reverse-proxy.md` for complete details.

Reverse-proxy operators who terminate TLS at nginx or Caddy should set `gateway.public_url` and `gateway.preview_origin` to the fully-qualified HTTPS URLs that the browser reaches (e.g. `https://omnipus.example.com` and `https://preview.omnipus.example.com`). The gateway uses these values to build correct `Content-Security-Policy` and `frame-ancestors` headers. See `docs/operations/reverse-proxy.md` for complete nginx and Caddy configuration examples.

On Android/Termux, `gateway.preview_listener_enabled` defaults to `false` because Termux processes typically cannot bind a second network port without additional permissions — iframe previews are unavailable in that environment. The gateway detects the Termux environment at boot and applies this default automatically.

## Contract regeneration

All wire-format types that cross the gateway/SPA boundary are generated from two spec files:

- `contracts/openapi.yaml` — REST endpoints
- `contracts/asyncapi.yaml` — WebSocket frames
- `contracts/components/schemas/` — shared JSON Schema component definitions

Generated artifacts (committed to the repo — never hand-edit):

- `pkg/api/generated/` — Go types (generated by `oapi-codegen`)
- `src/lib/api/generated/` — TypeScript types + Zod schemas (generated by `openapi-typescript` and `openapi-zod-client`)

### Running codegen locally

```bash
make gen-contracts
```

This runs `scripts/gen-contracts.sh`, which lints both specs and regenerates all artifacts. Running it twice in a clean tree produces no git diff (idempotent).

### Adding a new wire type (hard-constraint #8 — 5-step process)

1. Add the schema to `contracts/components/schemas/<TypeName>.yaml`
2. Reference it from `contracts/openapi.yaml` (REST) or `contracts/asyncapi.yaml` (WS), or both
3. Run `scripts/gen-contracts.sh` to regenerate all artifacts
4. Commit the generated diff alongside the spec change (one atomic commit)
5. Write the handler (Go) or consumer (TypeScript) using the **generated type only** — never hand-write a parallel struct or interface

Steps in any other order violate hard-constraint #8 and will fail the `verify-contracts` CI gate.

### Handling a verify-contracts CI failure

If the `verify-contracts` CI job fails, it means the committed generated files are stale relative to the spec. Fix:

```bash
make gen-contracts          # regenerate from spec
git diff                    # review the changes
git add pkg/api/generated/ src/lib/api/generated/
git commit -m "chore(contracts): regenerate from spec"
git push
```

Never commit a spec change without also committing the regenerated artifacts. Never edit generated files directly — they will be overwritten on the next `make gen-contracts` run.
