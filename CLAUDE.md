# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project

Omnipus is an agentic core: a single Go binary with the SPA embedded via `go:embed`, kernel-level sandboxing (Landlock + seccomp on Linux 5.13+), audit logging, encrypted credential management, and compiled-in Go channels. Community-facing, MIT-licensed, no telemetry. **Domain:** omnipus.ai

## Status

Active development. Most of the system is implemented and running on `main`: agent loop + turn engine (`pkg/agent/`), 5 core agents (`pkg/coreagent/`), 41 `system.*` tools (`pkg/sysagent/tools/`), tool registry + MCP (`pkg/tools/`, `pkg/mcp/`), skills + ClawHub (`pkg/skills/`), session/memory (`pkg/session/`, `pkg/memory/`), gateway + embedded SPA (`pkg/gateway/`), credential boot, audit/policy/sandbox, ~14 in-process Go channels (Telegram, Discord, Slack, Matrix, IRC, Google Chat, WhatsApp, …). WIP: unified plugin system (#151), Signal channel + proto-installer (unpushed sibling clone), security/UX hardening.

**Authoritative references** (code wins over docs on any disagreement):
- `docs/internal/architecture/AS-IS-architecture.md` — evidence-based as-is, code-cited.
- `docs/internal/architecture/plugin-extensibility-assessment.md` — plugin/extension status.
- `docs/internal/architecture/ADR-*.md` — accepted decisions.
- `docs/internal/BRD/` — original intent (Main BRD, Appendices A–E, competitive analysis); useful for context but superseded by code/as-is where they conflict.
- `.preview-doc/` — **the current v0.3 direction** (Workspaces redesign concept, 16 pages): the 4-base roster re-cast, delegation + Orchestrator, plugins & marketplaces, MCP/ACP/A2A protocol hooks, skills authoring + procedural memory, memory rooms, foundation-first roadmap. Discussion-stage / pre-ADR, but it **supersedes the `docs/internal/design/*-2026-05` drafts in intent** — see the Release Strategy note below.

## Release Strategy (v0.1 → v0.2 → v0.3)

Three-phase plan locked 2026-05-03.

- **v0.1 — Stabilize `feature/iframe-preview-tier13`.** Ship `web_serve` unification, kernel-enforced bind-port allow-list, sandbox-aware `exec`, iframe preview as one PR. Plan: `/home/Daniel/.claude/plans/quizzical-marinating-frog.md`. No memory/projects creep.
- **v0.2 — Security hardening (pentest quick wins).** Issue [#155](https://github.com/elicify-ai/omnipus/issues/155). Quick fixes only: env var allowlist (`pkg/sandbox/hardened_exec.go::allowedChildEnvKeys`), `master.key` 0600 check, shell-guard hardening, internal-CIDR egress blocking, audit HMAC chain, auth-endpoint rate limiting. Structural fixes → v0.3.
- **v0.3 / 1.0 — Workspaces redesign (memory + tasks + agents + plugins + sandbox topology).** Issue [#156](https://github.com/elicify-ai/omnipus/issues/156). Fresh-build, no back-compat. **The direction has evolved past the original "Rooms" framing — the current v0.3 direction is the concept in `.preview-doc/` (pre-ADR).** ⚠️ The five `docs/internal/design/*-2026-05.md` drafts (`sandbox-redesign`, `memory-redesign`, `tasks-redesign`, `projects-ui`, `settings-notifications`) are **pre-ADR background, being SUPERSEDED** — they still use the old **"Rooms / project room / 5-core (Mia·Jim·Ava·Ray·Max) + Primary·Sub tiers"** vocabulary, now re-cast to **Workspaces / workspace room / 4-base roster (Mia·Assistant ⭐ · Jim·Orchestrator · Ray·Scout · Ava·Builder; Max retired, automation = platform; specialists = marketplace packs)**. Do **not** implement from the design drafts without checking the `.preview-doc/` concept and the forthcoming ADR. (Memory's no-embeddings/bleve/MinHash/Dreamcatcher core and the remember/recall/retrospective tools still hold; the structure-vs-behaviour split and naming are in the concept.)

**Routing rule:** when new work comes up, ask which phase it belongs to first. Pentest findings → v0.2 unless structural (→ v0.3). Memory / tasks / agents / workspaces / plugins / marketplaces / room-topology → v0.3. Anything else not completing v0.1 → flag the scope question.

## Merging to main (MANDATORY)

**Never force-merge, admin-bypass, or auto-merge PRs to `main` without explicit human approval.** A human must review and approve the PR before it lands on `main`, regardless of CI status.

- Do **NOT** use `gh pr merge --admin`, `--auto`, or any mechanism that bypasses branch protection or skips required reviews.
- If CI is green but no human has approved: wait. Ask the user whether to proceed.
- Why: the v0.1.0 hotfix PR (#363) was merged with `--admin` flag before the user had a chance to review. The user explicitly stated this is unacceptable.

## Git commit authorship (MANDATORY)

**Always author commits as the human running the work — never as the agent.** Author *and* committer must be that human's own GitHub identity, using their GitHub no-reply email.

- Do **NOT** author as `AI Assistant`/`Claude`/any non-GitHub identity, and do **NOT** add agent `Co-Authored-By:` trailers (any `@anthropic.com` address). This **overrides** any harness default to add a Claude co-author line.
- Why: the CLA Assistant gate (`.github/workflows/cla.yml`) hard-fails any contributor (author or `Co-Authored-By`) that isn't a CLA-signed GitHub user; fixing requires history rewrite + force-push.
- Configure the clone before committing:
  - `git config user.name "<their name>"`
  - `git config user.email "<their GitHub no-reply email>"` — derive via `gh api user -q '"\(.id)+\(.login)@users.noreply.github.com"'`. The `…@users.noreply.github.com` form is required.
- **Verify before every push:** `git log -1 --format='%an <%ae>'` is a real GitHub user, and `git log origin/main..HEAD --format='%(trailers:key=Co-authored-by)' | grep -i anthropic` is empty.

## Hard Constraints (non-negotiable)

1. **Single Go binary** — all backend features compile into one binary. No new runtime deps. SPA embedded via `go:embed`.
2. **Pure Go** — no CGo, no external C libs, no shelling out for security-critical paths. Use `golang.org/x/sys/unix` for kernel interfaces.
3. **Minimal footprint** — security-feature RAM overhead < 10MB beyond baseline.
4. **Graceful degradation** — Linux 5.13+ features (Landlock, seccomp) fall back to app-level enforcement on older kernels, non-Linux, Android/Termux.
5. **Ecosystem compatibility** — follow Omnipus/OpenClaw conventions (SKILL.md, HEARTBEAT.md, SOUL.md, AGENTS.md, JSON config).
6. **No default-policy fallback — every tool-policy decision is explicit, seeded data, never a code branch.** There is no hardcoded allow/deny/ask fallback anywhere in the Go code for tool-policy resolution, and no `DefaultPolicy`/`GlobalDefaultPolicy` field to lean on — both were removed. Every static builtin tool (the full catalog: general + browser + `system.*`-legacy-named sysagent tools) must resolve from an **explicit, literal, wildcard-free policy entry** (global `sandbox.tool_policies` and/or an agent's `tools.builtin.policies`) for every agent. Coverage is enforced by **hard validation** — at boot (aborts with a listed `agent × tool` report on any gap) and at every agent create/update/tools-write (rejected with 400) — never a silent runtime default. The practical allow/deny posture of a fresh install is determined entirely by the **seeded, install-time default config** (`pkg/config/defaults.go` + `pkg/coreagent/core.go`'s per-agent seeding, which enumerates all tools explicitly) — that seed is data an operator can edit on their own installation afterward, not a fallback branch baked into the binary. `bash` (ADR-036 unified the retired `exec`/`workspace_shell`/`workspace_shell_bg` tools into it) is registered for every agent regardless of sandbox mode — the kernel sandbox is the protective layer — and is governed exclusively by each agent's explicit tool-policy entry, not a feature flag; Jim's seed grants him `bash: allow` so he has shell access on a fresh install. Sandbox `off` (god-mode) applies no implicit defaults — operator opt-in required. **Exception — MCP tools:** MCP-server tool names aren't known until an operator connects the server at runtime, so they can't be statically pre-enumerated; per-server `mcp_<server>_*` wildcard bulk policies remain the mechanism there. The no-wildcard rule applies to the static builtin catalog only.
7. **Release responsibility — fix everything, no excuses.** Every branch fully green before shipping. Pre-existing failures (lint, vuln, Go test, race, vitest, tsc, Playwright — anything CI runs) are ours to fix regardless of origin. "Pre-existing"/"not mine"/"broken on main too" are NEVER acceptable closure paths. Fix now, or get explicit user approval to defer with a tracked issue + target date.
8. **Contract-first wire formats — single source of truth, runtime-validated.** Every byte crossing the gateway/SPA boundary (REST req/resp, WS frame, persisted JSON the SPA reads) MUST be defined in `contracts/openapi.yaml` or `contracts/asyncapi.yaml` **before** any Go/TS code. Generated types in `pkg/api/generated/` and `src/lib/api/generated/` are the only legal cross-boundary types — committed, regenerated via `scripts/gen-contracts.sh`, verified by `make verify-contracts` (fails on drift). **Hand-written wire-format types are FORBIDDEN and lint-caught:** (a) package-level struct in `pkg/gateway/*.go` (non-test/non-generated) with ≥2 `json:` tags is flagged — opt out with `// not-wire-format`; (b) `export interface`/`export type = {}` object-literal in `src/lib/api.ts` or `src/lib/ws.ts` is flagged — same opt-out. AsyncAPI Zod schemas are generated, not hand-written. SPA edge validates every incoming payload via the matching zod schema (drop + counter + dev-mode toast on failure; no prod crash). Backend `pkg/api/generated/contract_test.go` fails on any Go struct producing schema-invalid JSON. See the 5-step "add a wire type" process under Contract regeneration.

## Tech Stack

**Backend:** Go (go.mod requires 1.26.4; targets 1.22+). Key packages: `golang.org/x/sys/unix` (Landlock/seccomp), `chromedp`, `whatsmeow` (WhatsApp), `discordgo`, `mymmrac/telego` (Telegram), `slack-go`, `modernc.org/sqlite` (pure-Go SQLite for whatsmeow, no CGo). All channels are in-process Go. Channels wrapping a non-Go runtime (e.g. Signal → `signal-cli`) spawn a sidecar from their own `Start()` and talk over localhost HTTP — there is no generic stdio bridge protocol. (WhatsApp is pure-Go in-process via whatsmeow, NOT a sidecar example.)

**Frontend:** TypeScript, React 19, Vite 6, shadcn/ui (Radix + Tailwind v4), AssistantUI (chat), Phosphor Icons, Zustand (UI state), TanStack Query (server state), TanStack Router, Framer Motion. Vite builds to `dist/spa/`, copied to `pkg/gateway/spa/`, embedded via `go:embed`.

**Storage:** File-based only (JSON/JSONL); no PostgreSQL/Redis. SQLite is isolated to WhatsApp session storage only. Data dir `~/.omnipus/`. Atomic writes (temp file + rename, `fileutil.WriteFileAtomic`). Credentials in `credentials.json` (AES-256-GCM, Argon2id), never in `config.json`. Sessions: day-partitioned JSONL (`sessions/<id>/<YYYY-MM-DD>.jsonl`, default 90-day retention). Context compression is **single-layer**: `forceCompression` (`pkg/agent/loop.go:5749-5800+`) drops ~50% of oldest turns + writes a summary (no second "tool result pruning" pass). Concurrency: per-entity files for hot data (tasks, pins); sessions/memory use a 64-shard mutex pool keyed by FNV hash (`pkg/memory/jsonl.go:21-77`). Advisory `unix.Flock` on Linux/macOS; Windows uses the single-writer goroutine pattern (no `LockFileEx`).

### Credential provisioning

All secrets in `credentials.json` (AES-256-GCM, Argon2id). Full boot contract: [ADR-004](docs/internal/architecture/ADR-004-credential-boot-contract.md).

**Unlock modes (priority order):**
1. `OMNIPUS_MASTER_KEY` — 64-char hex 256-bit key in env (CI/CD, containers).
2. `OMNIPUS_KEY_FILE` — path to a 0600 file with the hex key (long-running servers).
3. **Default key file** — `$OMNIPUS_HOME/master.key` (0600) loaded automatically if present.
4. **Auto-generate on fresh install** — if no key configured and no `credentials.json` yet, gateway mints a 256-bit key, writes `$OMNIPUS_HOME/master.key` (0600), logs a backup warning. Never fires when `credentials.json` already exists.
5. **Interactive TTY prompt** — passphrase at terminal; TTY-only, never headless.

**Critical:** back up the master key file — losing it makes every credential permanently inaccessible.

Manual key file: `openssl rand -hex 32 > master.key && chmod 600 master.key && export OMNIPUS_KEY_FILE=...`

**Key rotation:** `omnipus credentials rotate` (no args, passphrase-based) unlocks via current mode, prompts twice for new passphrase, then `store.RotateWithPassphrase` re-encrypts everything. No `--key-file` flag, no zero-downtime path — headless key-file rotation means stop gateway, replace `master.key`, restart, re-onboard secrets via `omnipus credentials set` or Settings → Security → Credential Vault.

**Boot order:** `NewStore → Unlock → LoadConfigWithStore → InjectFromConfig → ResolveBundle → RegisterSensitiveValues → NewManager → Start`. Any failure aborts boot. Channel secrets pass directly as a `credentials.SecretBundle` to constructors.

## Architecture Patterns

**Sandboxing:** `SandboxBackend` interface — Linux (Landlock+seccomp), Windows (Job Objects+Restricted Tokens+DACL), Fallback (app-level). Policy + audit are cross-platform; only the enforcement backend varies.

**Channel model:** All channels implement `Channel` (`pkg/channels/base.go:47-56`) plus opt-in capability interfaces (`TypingCapable`, `MessageEditor`, `MessageDeleter`, `ReactionCapable`, `PlaceholderCapable`, `StreamingCapable`, `CommandRegistrarCapable` — `pkg/channels/interfaces.go:13-70`). Each registers a factory at compile time via `channels.RegisterFactory(name, factory)` in a `func init()`; activation is a hardcoded if-ladder in `Manager.initChannels()` (`pkg/channels/manager.go:582-708`). Channels talk to the agent loop only via the in-process `MessageBus` (`pkg/bus/bus.go`). There is **no** `BridgeAdapter`, **no** stdio bridge protocol, **no** Channel SDK (issue #151 tracks the planned unified plugin system).

**Channels UI + routing:** dedicated top-level **Channels** screen (`src/routes/_app/channels.tsx`), per-channel config in a Configure slide-over (`ChannelConfigPanel`). Agent routing resolved by `pkg/routing/route.go::ResolveRoute` from `cfg.Bindings[]` (most-specific first: peer → guild → team → account → channel-wildcard → default). Per-channel **Default agent** control backed by `GET/PUT /api/v1/channels/{id}/routing` (`ChannelRouting` wire type). **Channel secrets are credential-store-routed** (SEC-23, #289 via #296): `configureChannel` (`pkg/gateway/rest.go`) detects secret fields (token/secret/password/key/api_key), stores each in the encrypted store, writes a `<field>_ref` to `config.json` — no plaintext secret persisted. `Test` validates via `credentialRefResolves`.

**WhatsApp native QR pairing** (#283 via #298): `whatsapp_native` (whatsmeow) emits its pairing QR over the `whatsapp_pairing` WS frame; SPA renders it inline in the Configure panel (`WhatsAppNativeNotice`, `qrcode.react`). Enable & Save, then scan via WhatsApp → Linked Devices. Native WhatsApp ships by **default** (`!lite`, excluding `mipsle`/`netbsd`/`freebsd&arm` where `modernc.org/sqlite` can't build → stub). A **lite** build (`make build-lite`, `-tags lite`) drops whatsmeow (~58 MB smaller); WhatsApp is then unavailable and the manager records a non-fatal failure (gateway boots degraded). The old `whatsapp_native` opt-in tag and `make build-whatsapp-native` are retired — do not reintroduce. Lite UX tracked in #299.

**Agent types (W4 of agent-form-requirements):** 3-type wire taxonomy — `Main` (user-defined chat colleague, runs on the Omnipus engine), `Subagent` (user-defined delegation-only worker on the Omnipus engine), `subagent_3p` (user-defined delegation-only worker on an external CLI: `claude-code` / `codex` / `opencode`). The build-in roster (Mia / Jim / Ava / Ray) returns `type: core` with `locked: true` on the wire and is seeded via `coreagent.SeedConfig`. The legacy `system` enum value survives for backward-compatible config entries but `SeedConfig` seeds none. The wire enum is `[core, system, Main, Subagent, subagent_3p]` per `contracts/components/schemas/Agent.yaml`. Spec: `docs/internal/specs/agent-form-requirements.md` §2. The `isWorker()` helper at `src/lib/api.ts:664-666` recognises `Subagent` / `subagent_3p` / legacy `worker`. Boundary translation between wire (Main/Subagent/subagent_3p) and persisted config (`custom`/`worker`) is in `coreagent.ResolveType` (`pkg/coreagent/core.go:241`) — the persisted constants stay for the 110 internal references; the translation is at the handler.

**Delegation identity — no inheritance from the parent (ADR-032, amended 2026-07-09):** a delegated sub-turn runs as the TARGET agent's own real instance, never the delegating parent's. `spawnSubTurn` (`pkg/agent/subturn.go`) sources every agent-level setting — ID, Name, Workspace, ContextBuilder (soul/persona/memory/skills), Tools, tool policy, AgentType, **and Model/Provider/Candidates/ProviderPool** — from `execSource` (the resolved delegate named by `TargetAgentID`, or the parent itself for self-delegation), for both native and external-cli dispatch, with no per-field exception. (The `DelegationPolicy` field this line once named was itself retired by ADR-037 — `AgentInstance` no longer carries it at all; delegation trust is workspace-scoped only, see the Delegation Graph removal entry below.) The parent's ONLY contribution is the task prompt (`SubTurnConfig.SystemPrompt`, used as the child's first user message) and the fact that delegating to this target was authorized at all (the workspace delegation-graph `trust_set` gate, enforced before `spawnSubTurn` is reached — see "Channel↔Workspace" / workspace delegation edges). **Do not reintroduce a "keep some fields as the parent's" exception** — an earlier, narrower version of this fix deliberately kept Model/Candidates/Provider/ProviderPool parent-sourced (citing a mutex/mismatch risk with the native LLM-call path) and that was itself an identity-inheritance bug: it's resolved by reading the whole mutex-protected quad (`Model`/`Provider`/`Candidates`/`ThinkingLevel`) from the SAME source under one `RLock`, never mixing parent and target fields. Regression coverage: `pkg/agent/subturn_target_identity_test.go` (identity, tool policy, filesystem confinement, provider pool, Model-from-target).

**Delegation Graph removal (ADR-037):** `config.AgentConfig.DelegationPolicy` and the global "Delegation Graph" screen at `/agents/trust` are deleted entirely — no back-compat, matching the ADR-035 precedent for SandboxProfile. Both were confirmed fully dead in the live enforcement path since commit `822202ad` (2026-06-27) made the per-workspace `Delegation[]` edge list (`pkg/workspace/delegation.go`) the sole runtime authority; the global screen looked functional (edits saved with a "Saved" confirmation) but had zero effect on real delegation. **Delegation trust is workspace-scoped, full stop** — an agent's ability to delegate to another agent is a property of the workspace team they're both on, not a global agent attribute; the same agent can sit on multiple workspaces with different rosters and different trust in each. Configure delegation exclusively via a workspace's own Team tab. `PUT /api/v1/agents/{id}` 400s on a `delegation_policy` field in the request body (raw-body sniff, mirrors the ADR-035 `sandbox_profile` precedent) rather than silently dropping it. The retired `DelegationPolicy` Go type survives only as an unexported-shape seed DTO (`coreagent.SeedDelegationEdges`) for new-workspace bootstrap — it is never persisted on `AgentConfig` and never crosses the wire.

**Default agent (single source of truth):** the agent with `AgentConfig.Default` (`json:"default"`) true. `agent.Registry.GetDefaultAgent()` honors it first (then legacy `Agents.Defaults.DefaultAgentID`, then first registered). `pkg/routing/route.go::resolveDefaultAgentID` falls back to the **first ENABLED agent** when none is marked (not the historical `"main"` constant) and logs WARN. `coreagent.SeedConfig` marks **Mia** default on fresh install. Set via Agents screen ★ → `PUT /api/v1/agents/{id}` with `default:true` (handler enforces single-default; `pkg/config` repairs multi-default at load). The flag does NOT relocate the agent's `agents/<id>/` workspace.

**Custom-agent format:** structured `AGENT.md` (singular) with frontmatter + `SOUL.md` (prompt) + `HEARTBEAT.md` (periodic). Legacy `AGENTS.md` (plural) still loads as fallback but is not for new agents.

**Brand:** "The Sovereign Deep" — dark-first. Deep Space Black (`#0A0A0B`), Liquid Silver (`#E2E8F0`), Forge Gold (`#D4AF37`). Fonts: Outfit (headlines), Inter (body), JetBrains Mono (code). Octopus mascot. See `docs/internal/brand/brand-guidelines.md`.

**UI rules:** Chat-first, dark-first. Sidebar is an overlay drawer (pinnable). No separate canvas (rich content inline, expands fullscreen). No emoji in stored data or UI chrome (emoji→Phosphor translator in chat output only). Tool calls visible by default, collapsible. **Exception (`src/lib/toolVisibility.ts`, ADR-036-adjacent):** a closed, narrow set of infra-only calls with no standalone meaning to a reader are hidden by default from the chat THREAD — `load_tool`; `bash`'s background-dispatch/status-poll/read sub-cases; and (as of 2026-07-16) `delegate`'s `run` sub-case (sync or async) and its `status`-poll sub-case, plus its whole SubagentBlock delegation card in the thread. Unlike `load_tool` (still forced visible on error), a failed/denied `delegate` or background-`bash` outcome does **not** override the hide — that failure is left to the calling agent's own response text. The ActivityPanel slide-out (default INVERTS to show everything except `load_tool`) is the durable fallback for inspecting it, but its coverage is narrower than "fully transparent": it only ever shows subagent spans and background bash sessions, capped at the 8 most-recently-finished (`RECENTLY_FINISHED_CAP` in `useRunningActivity.ts`), and (Fix 1, 2026-07-16) stays reachable at idle only while a failure is still retained in that capped list (`ActivityBar.tsx`). A delegation DENIED outright at dispatch time never opens a span, so it never reaches the panel either — verbose chat is the only render surface for that case, and absent verbose chat the calling agent's own narration is the only place the denial surfaces. A user-facing "Verbose chat" override (Settings → Chat) reveals everything, thread and panel alike. Persistence is unaffected — hidden calls still exist in the session transcript, this is render-only.

## Spec-Driven Workflow

When implementing features: (1) read the relevant BRD section(s); (2) `/plan-spec` for TDD/BDD specs; (3) `/grill-spec` to stress-test; (4) `/taskify` to decompose; (5) implement in Plan Mode first; (6) `/grill-code` to verify compliance.

## Issue & Project Board Conventions

Follow `docs/internal/issue-and-board-conventions.md` (applies to lead + every subagent). Essentials:

- **Type is the "kind" axis** (exactly one): **Bug / Feature / Task / Epic** — set the org-level Issue Type, not a label. The `bug`/`enhancement` labels are **retired/deleted**; never recreate them.
- **`gh` has no `--type` flag** — set the Type via GraphQL `updateIssueIssueType` after `gh issue create` (or use the issue templates in the UI, which set `type:` automatically). Type IDs and the exact recipe are in the doc.
- **Labels are cross-cutting only**: `priority:*` (one), `area:*` (one+), plus `security`/`tech-debt`/`test-coverage`/`documentation`. The `type:*` labels are **PR/changelog only** (release-drafter) — never put them on issues.
- **Board #3 automation does the rote work**: new issues auto-add as `Backlog`; closing auto-sets `Done`. Do **not** manually add issues to the board or hand-set initial/`Done` status. You do set **Sprint** (single-select) and promote Status as work proceeds.
- **Bundle related work** under an **Epic** with real sub-issues (`addSubIssue`); sprints are an Epic + its sub-issues sharing one Sprint value.
- **Every PR MUST close its issues via keyword (mandatory).** A PR that resolves issues MUST list a closing keyword **per issue** in the **PR description** (not just commit messages) — e.g. `Closes #264, closes #289, closes #283`. This is non-negotiable: the only acceptable reason an issue stays open after its fix merges is that the work genuinely isn't done. Rules that bite us:
  - **Repeat the keyword for each issue.** `Closes #1, #2` only closes `#1`. Write `Closes #1, closes #2`. Keywords: `close[s|d]` / `fix[es|ed]` / `resolve[s|d]`.
  - **Auto-close only fires when the PR merges into the *default* branch (`main`).** A PR merged into a non-default base (e.g. a release/hotfix branch) links but does **not** close — the closure happens when that branch later merges to `main`, and only if the keywords ride along in that merge's PR body. Target `main` whenever you want the issues closed on merge.
  - **Put keywords in the PR *description*, not only commit messages.** On squash-merge, commit-message keywords are unreliable; the PR body is what GitHub honors. (This is why Sprint #258 / PR #292 left 8 issues open despite shipping their fixes.)
  - If a PR can't use auto-close (non-`main` base), it MUST still reference every issue it resolves, and whoever merges is responsible for closing them with a comment citing the PR.

## Subagent Workflow

You (the lead) orchestrate all work by spawning subagents via the Agent tool (no teams). Decompose into focused units, give each subagent a complete prompt (spec ref, exact files, definition of done), run independent work in parallel, review every output, run QA after implementation.

**Implementing subagents:** `frontend-lead` (sonnet, `src/`, `packages/ui/`), `backend-lead` (sonnet, `pkg/`, `cmd/`, `internal/` except security), `security-lead` (opus, `pkg/security/`, `pkg/sandbox/`, `pkg/audit/`, `pkg/policy/`), `qa-lead` (sonnet, tests only).
**Review subagents:** `architect` (opus, design/ADRs) + the pr-review-toolkit agents.

**Per task type:** frontend-only → frontend-lead → qa-lead; backend-only → backend-lead → qa-lead; security → security-lead + backend-lead → qa-lead; full-stack → frontend-lead + backend-lead (parallel) → qa-lead; design questions → architect.

### Review pipeline — 7-reviewer quality gate (MANDATORY)

Runs **twice**: after EACH feature (before its PR merges to base) AND on the WHOLE epic diff before the final `→ main` PR. All seven must be clean or each finding explicitly deferred with a tracked issue. Hard release rule, on par with Constraint #7.

### Which subagents to spawn per task type
- **Frontend-only work:** frontend-lead → qa-lead
- **Backend-only work:** backend-lead → qa-lead
- **Security work:** security-lead + backend-lead → qa-lead
- **Full-stack features:** frontend-lead + backend-lead (parallel) → qa-lead
- **Design questions:** architect

## Quality Gates

Verify all applicable gates before reporting work done:

```bash
# Backend
export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH
gofmt -l . | wc -l                                          # must be 0
golangci-lint run --build-tags=goolm,stdjson                # exit 0
CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 ./...    # exit 0
CGO_ENABLED=1 go build -tags goolm,stdjson ./...            # exit 0
govulncheck ./...                                           # 0 vulnerabilities

# Frontend
npm run typecheck     # tsc -b --noEmit — exit 0 (see WARNING)
npx vitest run        # exit 0

# Contracts
make verify-contracts  # exit 0
```

**WARNING — typecheck trap.** `tsconfig.json` is a project-references root with no `include`/`files`. `tsc --noEmit` (without `-b`) is a silent no-op (always exits 0). Use `npm run typecheck` (wired to `tsc -b --noEmit`).

## Build & E2E Testing

### Testing & building — CI is the authority (MANDATORY)

**Rule: CI is the source of truth for Go test/build results. Never run the full Go test
suite (especially `pkg/gateway`) locally.** This runs in an **ephemeral, resource-constrained
devpod** — recreated on demand with varying specs (seen: 2–4 cores, 3.8–15 GB RAM, and a
root disk that has been as full as ~96 %). Linking and running the full gateway *test binary*
— which pulls in the pure-Go OLM crypto via the `goolm` tag — can OOM-kill or stall the
session. CI runs on 16 GB runners; trust it. Push and read the checks rather than reproducing
the suite here.

**Always build/test through `make` (or pass the build tags) — never raw `go test ./...`.**
The Matrix channel (`pkg/channels/matrix`) is gated behind `//go:build goolm`, and the gateway
imports it, so **without the tags the package will not even compile** — you get the misleading
`build constraints exclude all Go files in .../pkg/channels/matrix → [setup failed]`. That is
**not** a flake, an OOM, or a real bug — it is a missing build tag. Canonical tags (`Makefile`,
`GO_BUILD_TAGS`): **`goolm,stdjson`**.
- `make test` / `make build` inject `-tags goolm,stdjson` automatically — prefer them.
- Raw invocation MUST carry the tags: `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/...`.

- **To validate backend changes: push and let CI run** — do not run the full suite here.
- **At most one narrowly-scoped local test** when you must (`-tags goolm,stdjson -run '^TestName$' -p 1`).
  A single scoped `pkg/gateway` test is cheap (~86 MB / ~60 s clean); the *full* suite or a
  parallel `./...` is what exhausts RAM → OOM-kills the session.
- **Never run multiple Go test suites in parallel.** **Do NOT use `MemoryMax` cgroup caps with
  swap enabled** — they thrash into unkillable zombies; if you must cap, use `MemorySwapMax=0`
  so a runaway dies instantly. Watch root disk around builds (`df -h /`); clearing
  `~/.cache/go-build` forces a multi-GB recompile.
- Full session/Spec-1 context: `docs/internal/specs/uxh-spec1-STATUS-2026-06-04.md`.

### SPA Embed Pipeline

The binary embeds the SPA from `pkg/gateway/spa/` — **not** the Vite output (`dist/spa/`). Sync before building:

```bash
npm run build                          # → dist/spa/
rm -rf pkg/gateway/spa                 # drop stale embed dir
cp -r dist/spa/* pkg/gateway/spa/      # sync to embed dir
CGO_ENABLED=0 go build -o /tmp/omnipus ./cmd/omnipus/
```

Skip the sync → stale SPA served. Verify: `grep -c "YOUR_NEW_STRING" pkg/gateway/spa/assets/index-*.js` > 0.

### Running the embedded SPA

Always test against the Go binary, not the Vite dev server (which proxies `/api` to `localhost:18790`).

```bash
export OMNIPUS_HOME=/tmp/omnipus-e2e-test
rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"
OMNIPUS_BEARER_TOKEN="" ./omnipus gateway --allow-empty &
```

**Known blockers:**
1. **Port conflict** — default port 5000; if taken the gateway fails to bind. Check `lsof -i :5000 | grep LISTEN`; set `gateway.port` in `$OMNIPUS_HOME/config.json`.
2. **`gateway.dev_mode_bypass`** — auth tree (`pkg/gateway/auth.go:55-102`, the `checkBearerAuth` function): no Bearer header → 401 always; `Gateway.Users` populated → token must match a user; `OMNIPUS_BEARER_TOKEN` set → constant-time match; no users AND no env token → bypass=true lets caller through as admin (one-time WARN), bypass=false → 401. The `withAuth`, `withOptionalAuth`, and `withRateLimit` middleware helpers live in `pkg/gateway/rest_auth.go` (and `pkg/gateway/rest.go` for `withAuth`). **Onboarding does NOT need bypass** (`/api/v1/state`, `/onboarding/*`, `/auth/login`, `/auth/register-admin`, `/providers`, `/media/`, `/uploads/` use `withOptionalAuth`). Set bypass=true only to drive a `withAuth` endpoint pre-onboarding, for Go test scaffolding, or local dev. `RequireNotBypass` middleware returns **503** when bypass=true on high-blast-radius admin routes (e.g. sandbox-config PUT) — never set in production, never remove the guard without an ADR. **Default: false.**
3. **Model must support tool use** — Omnipus sends tools every request; a non-tool model (e.g. `google/gemma-2-9b-it`) returns 404. Use `z-ai/glm-5-turbo`, `google/gemini-2.5-flash`, or `anthropic/claude-3.5-haiku`.
4. **Logs** — `$OMNIPUS_HOME/logs/`: `gateway.log` (runtime), `gateway_panic.log` (startup; check if gateway exits silently).

### E2E checklist (after frontend+backend changes)

Onboarding flow → Chat (multi-turn, token/cost) → Agents (list, create, profile) → System agent (read-only sections) → Settings (all tabs) → Command Center → Skills & Tools tabs → Sidebar nav → zero console errors (WS reconnect warnings OK).

### Preview on the main listener (ADR-044)

The gateway serves the SPA, the authenticated API, AND agent-generated dev previews on a **single** listener (`gateway.port`, default 5000): `/preview/<agent>/<token>/…` is registered **bare** (token-authenticated, CSRF/Origin prefix-exempt) on the main mux. There is **no** separate preview listener — the `preview_port`/`preview_host`/`preview_origin`/`preview_listener_enabled` config keys were **deleted** with no back-compat. The live `gateway.preview_enabled` flag (default true, read per-request, **no restart**) 404s `/preview/` when disabled. Chat renders a preview **link** (no embedded iframe); the agent reviews/presents it via the built-in browser live panel. Reverse-proxy operators set `gateway.public_url` (still **restart-gated**) to the FQ HTTPS URL the browser reaches — it drives the boot-frozen `CanonicalGatewayOrigin` that `web_serve` preview URLs and CSP/CORS/WS `CheckOrigin` all use — see `docs/operations/reverse-proxy.md`.

### Local PR-runner (ci-omnipus Fly worker)

The Go test/build suite runs on a dedicated Fly worker (`ci-omnipus`), **never in the dev pod** (OOM risk — see "Testing & building" above). Driven via `flyctl ssh console`; trigger one gate at a time:

```bash
fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> <gate>"
```

Gates: `all | go-build | go-vet | go-test | contracts | spa | gofmt | quick | embed-build | e2e`.

**Before you read a verdict from this worker as trustworthy, read `deploy/ci-worker/CLAUDE.md`** (auto-loads once you touch any file in that directory, e.g. `runci.sh`) — it covers the two false-signal traps that have both bitten this project before (stale-checkout false-RED, wrapper-exit-code false-GREEN), the e2e gate's mechanics, the `runci.sh` redeploy procedure, and worker lifecycle/cost notes.

## Contract regeneration

Wire types are generated from `contracts/openapi.yaml` (REST), `contracts/asyncapi.yaml` (WS), `contracts/components/schemas/` (shared schemas). Artifacts (committed, never hand-edit): `pkg/api/generated/` (Go, oapi-codegen) and `src/lib/api/generated/` (TS types + Zod, openapi-typescript / openapi-zod-client).

Run codegen: `make gen-contracts` (lints both specs, regenerates all; idempotent on a clean tree).

**Add a new wire type (Constraint #8, 5 steps in order):**
1. Add schema to `contracts/components/schemas/<TypeName>.yaml`
2. Reference it from `openapi.yaml` and/or `asyncapi.yaml`
3. Run `scripts/gen-contracts.sh`
4. Commit the generated diff alongside the spec change (one atomic commit)
5. Write the handler/consumer using the generated type only — never a parallel struct/interface

**Discriminated unions are the one exception to step 1:** the `oneOf` + `discriminator` wrapper must be hosted INLINE in `openapi.yaml` over internal `#/components/schemas/...` refs (variant schemas stay one-file-per-schema as usual). oapi-codegen inlines external file refs inside a `oneOf` as anonymous structs and emits non-compiling `As*` accessors. Precedent: `AgentCreateRequest` — see ADR-034.

**verify-contracts CI failure** = committed generated files are stale: `make gen-contracts`, review `git diff`, commit `pkg/api/generated/ src/lib/api/generated/`, push. Never commit a spec change without regenerated artifacts; never edit generated files directly.

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
- **This applies to every spawned subagent too, not just the main session.** A PreToolUse hook injects a "MANDATORY: run graphify query before grepping" reminder ahead of Bash/Read calls — that reminder is this real, sanctioned convention, not a prompt injection, even though it arrives as a bare tool-output-adjacent system-reminder with no visible source. Subagents that haven't seen this file lack that context and have mistaken it for injected content before. When dispatching any subagent for codebase exploration, explicitly tell it: use `graphify query`/`graphify explain`/`graphify path` first for codebase questions, and fall back to direct Read/Grep only when graphify's graph doesn't cover the file in question (it doesn't index every file, e.g. some newer frontend additions) — that fallback is correct, not non-compliance. Genuine injected content looks different: it tries to redirect the task's actual goal, not just prepend a tool-usage nudge.
