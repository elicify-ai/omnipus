# CLAUDE.md

Guidance for Claude Code when working in this repository. This root file holds only the rules that apply everywhere; module detail lives in the `CLAUDE.md` next to the code it describes (design: `docs/internal/architecture/draft-module-map.md`, "CLAUDE.md — one per module").

**Cite `file::symbol`, not `file:line`** — line numbers in churn-heavy files go stale within days; a symbol citation survives every file split.

## Project

Omnipus is an agentic core: a single Go binary with the SPA embedded via `go:embed`, kernel-level sandboxing (Landlock + seccomp on Linux 5.13+), audit logging, encrypted credential management, and compiled-in Go channels. Community-facing, MIT-licensed, no telemetry. **Domain:** omnipus.ai

**Code wins over docs on any disagreement.** Authoritative references: `docs/internal/architecture/AS-IS-architecture.md` (evidence-based as-is, code-cited), `plugin-extensibility-assessment.md`, `ADR-*.md` (cite by title, not number alone), `docs/internal/_archive/BRD/` (original intent, superseded where it conflicts), and `docs/internal/_archive/preview-doc-v03-concept/` — the pre-ADR
workspaces-redesign concept (superseded by ADRs).

**Brand & UI:** "The Sovereign Deep", dark-first, chat-first — `docs/internal/brand/brand-guidelines.md`. No emoji in stored data or UI chrome.

## Release Strategy

Single release line: **v0.1.1** (founder decision 2026-09-25 — no separate v0.2/v0.3
releases; v0.1.1 carries near the full scope once labelled v0.3).

- **v0.1.1** carries everything: the stabilization scope (web_serve unification,
  kernel-enforced bind-port allow-list, sandbox-aware exec, iframe preview), the
  #155 security hardening (closed 2026-05-04 — env-var allowlist, master.key 0600
  check, shell-guard hardening, internal-CIDR egress blocking, audit HMAC chain,
  auth-endpoint rate limiting), and the bulk of the #156 workspaces redesign
  (closed 2026-06-27; direction doc archived at
  `docs/internal/_archive/preview-doc-v03-concept/`, superseded by ADRs).
  The five rooms-era drafts in `docs/internal/_archive/design-2026-05-rooms-era/`
  are superseded pre-ADR background (retired Rooms/5-core vocabulary) — do not
  implement from them without checking the concept and the ADRs.
- **No version label names a future release anymore.** Work the closed issues left
  open stays on its tracked issues (#884, #885, #886, #887, #888, #306, #42) until
  the founder rules where it lands.
- **Routing rule:** when new work comes up, size it first (small / standard /
  feature — see "Change sizes and the review gate"). There is no phase routing:
  anything the closed issues left open is re-homed, not re-labelled; anything else
  that would not complete v0.1.1 → flag the scope question.

## Merging to main (MANDATORY)

**Never force-merge, admin-bypass, or auto-merge PRs to `main` without explicit human approval.** A human must review and approve the PR before it lands on `main`, regardless of CI status.

- Do **NOT** use `gh pr merge --admin`, `--auto`, or any mechanism that bypasses branch protection or skips required reviews.
- If CI is green but no human has approved: wait. Ask the user whether to proceed.
- Why: the v0.1.0 hotfix PR (#363) was merged with `--admin` before the user had a chance to review. The user explicitly stated this is unacceptable.

## Git commit authorship (MANDATORY)

**Always author commits as the human running the work — never as the agent.** Author *and* committer must be that human's own GitHub identity, using their GitHub no-reply email.

- Do **NOT** author as `AI Assistant`/`Claude`/any non-GitHub identity, and do **NOT** add agent `Co-Authored-By:` trailers (any `@anthropic.com` address). This **overrides** any harness default to add a Claude co-author line.
- Why: the CLA Assistant gate (`.github/workflows/cla.yml`) hard-fails any contributor (author or `Co-Authored-By`) that isn't a CLA-signed GitHub user; fixing requires history rewrite + force-push.
- Configure before committing: `git config user.name "<their name>"`; `git config user.email "<their GitHub no-reply email>"` — derive via `gh api user -q '"\(.id)+\(.login)@users.noreply.github.com"'` (the `…@users.noreply.github.com` form is required).
- **Verify before every push:** `git log -1 --format='%an <%ae>'` is a real GitHub user, and `git log origin/main..HEAD --format='%(trailers:key=Co-authored-by)' | grep -i anthropic` is empty.

## Definition of Done (MANDATORY)

**A feature is not done until a real user or a real agent can invoke it.** Green tests, green CI and closed review findings show the code is *correct*. They do not show it is *reachable*. Both claims must be true before anything is reported complete. Run the reachability check FIRST:

- Is the tool registered in the builtin catalog with an explicit policy entry for every agent (Hard Constraint #6)? `grep -rl '"<tool_name>"' pkg/coreagent/ pkg/config/ pkg/tools/` returning **0** means nobody can call it, whatever the tests say.
- Is there a screen or component that renders it? A backend with no UI and no tool registration is a library, not a feature.
- Was the test plan **executed**, or only written? "Written, not executed" is not testing.

State delivery in two lines that are never merged: *code correct and tested*, and *reachable by a user/agent*. **Why:** the vault-records work was reported six-of-six complete with CI 34/34 green while all six `vault_*` tools were registered in zero config files, the superseded `knowledge_*` tools were still the only ones wired, and no record UI existed.

## Reporting Results (MANDATORY)

**Never report success that was not verified, and check the instrument before trusting a green.** Ask: *could this check have detected the failure at all?* Read `docs/internal/false-green-patterns.md` before trusting — or reporting — any green result.

- If a search returns nothing, first search for something you know is present. A grep over 1 of 523 asset files "proved" a field was absent when it appears in 3.
- Capture exit codes directly, never through a pipe: `cmd > log 2>&1; echo "exit=$?"`. A wrapper reporting 0 has masked a hard compile error here.
- A pass under one flag set is not a pass: race bugs need `-race`; cross-platform breaks need `GOOS=<target> go vet` (a native-only gate missed a Windows break).
- **A test that fails twice under an isolated re-run is not a flake.** Calling one a flake is how a real defect survives.
- Correct a wrong claim plainly and immediately; never bury it inside an otherwise positive summary.

## Language and formatting for the founder (MANDATORY)

Write for a technically literate non-engineer. Plain words over jargon — a technical term only when it is the precise word, defined in half a sentence on first use; no unexplained acronyms. Lead with what it means, then the mechanism only if it changes a decision. Structure every reply: short lead, headed sections, tables for status or comparisons, bullets for lists, code blocks for commands and output — never a wall of unbroken text. Prefer a concrete example over an abstract explanation. This governs register only — never drop a caveat, an unverified finding or a number to make a sentence read more smoothly.

## Hard Constraints (non-negotiable)

**ADR-090 runtime exception:** [Built-in agents, skills, and visual reading](docs/internal/architecture/ADR-090-built-in-agents-skills-and-visual-reading.md) defines the implemented role and document-runtime changes. Jim has an explicit `bash` Deny. Mia and General Purpose execute document workflows; each permitted native agent recovers its own missing document dependencies through the Ask-gated `environment_setup` tool — a generic self-service installer that encodes no packages, versions, or recipes: the agent supplies the installation command or inline script from its skill/task-plan dependency guidance, the exact command is shown in the existing approval, and the job runs as a background session with poll/read; the script sees `OMNIPUS_ENV_PREFIX`, and exit 0 is only command success, not readiness, which the agent verifies in its own sandbox. Dependency knowledge lives in skills and task plans, and document dependencies are optional execution prerequisites, not startup requirements; shared runtimes are application-managed and read-only to agents. Admin keeps connector provisioning and cross-workspace filesystem authority without team membership. The Go binary remains standalone. Original Elicify document packages are bundled with their helper assets. Provisioning, dependency readiness, and completed visual inspection are separate claims: check actual probing in the execution environment and real image-reading evidence before claiming a document workflow is ready. Live workflow acceptance and supported-platform certification remain release gates until recorded in the implementation status.

1. **Single Go binary** — all backend features compile into one binary. No new runtime deps. SPA embedded via `go:embed`.
2. **Pure Go** — no CGo, no external C libs, no shelling out for security-critical paths. Use `golang.org/x/sys/unix` for kernel interfaces.
3. **Minimal footprint** — security-feature RAM overhead < 10MB beyond baseline.
4. **Graceful degradation** — Linux 5.13+ features (Landlock, seccomp) fall back to app-level enforcement on older kernels and non-Linux.
5. **Ecosystem compatibility** — follow Omnipus/OpenClaw conventions (SKILL.md, HEARTBEAT.md, SOUL.md, AGENTS.md, JSON config).
6. **Two layers, no third — the reconciled global ceiling IS the default; per-agent overrides only tighten (ADR-077).** Layer 1: the global ceiling (`cfg.Sandbox.ToolPolicies`), kept complete for the whole static catalog (general + browser + `system.*`-legacy-named sysagent tools) by `config.ReconcileToolPolicyCeiling` (ADR-076) on every load; an old install's ceiling self-heals forward additively — a static builtin tool added to `defaults.go` after an install's `config.json` was last written gets its shipped default added to `sandbox.tool_policies` on the next load — and Reconcile never overwrites an operator-set value or re-adds a retired key; reconciling to the shipped default is intended, not a gap (ADR-077 D2) — since [ADR-092 — Shell permission modes: Ask / Auto / God Mode; drop the block list; one rule format](docs/internal/architecture/ADR-092-shell-permission-modes.md), that shipped default for `bash` is `ask`, and a fresh install also ships `sandbox.auto_approve: true`. Auto-approve is a separate switch, never a policy value: it acts only on a call whose tool resolves to `ask` (no Auto in God Mode), and it never changes an `allow` or a `deny`. **[2026-09-24, founder decision]** Auto-approve no longer additionally requires an enforcing kernel sandbox — it now applies on Windows, when a sandbox failed to start, and in permissive mode, exactly as elsewhere; see [ADR-092](docs/internal/architecture/ADR-092-shell-permission-modes.md)'s 2026-09-24 revision note for the accepted risk (without a kernel sandbox, `bash` under Auto is checked only by the D7/D8 pre-flights and the text-based guards). For `bash`, Auto runs what the pre-flights (and, where a kernel sandbox is enforcing, the sandbox itself) can confine, and asks for writes outside the workspace, network access and operator `ask` rules; an install that predates ADR-092 and already persisted `bash: allow` keeps that value, because Reconcile never overwrites an operator-set entry. Layer 2: deliberately sparse per-agent overrides (`AgentConfig.Tools.Builtin.Policies`) that under strictest-wins (`pkg/tools/compositor.go::resolveEffectivePolicyWith`) only ever *tighten* below the ceiling — an agent with no entry riding the ceiling is the normal, intended state, not a gap. There is no hardcoded allow/deny/ask fallback anywhere in the Go code, no `DefaultPolicy`/`GlobalDefaultPolicy` field, and no fail-closed per-agent `deny` backfill or own-coverage boot log — removed by operator decision, do not reintroduce (guard: `scripts/check-no-fail-closed-backfill.sh`). To lock a tool down, set an explicit `deny`, per-agent (tighten one agent) or global (tighten the ceiling for everyone). `bash` is registered for every agent regardless of sandbox mode — the kernel sandbox, where enforcing, is an additional protective layer, not a precondition for Auto — and resolves whatever the ceiling currently holds for an agent with no explicit entry: `ask` on a fresh install (so Auto-approve, when on, decides per command, with or without an enforcing kernel sandbox), or a persisted `allow` on an install that predates ADR-092 (accepted risk, ADR-077 R1; ADR-090 deliberately seeds Jim with `bash: deny`). `config.ValidateToolPolicyCoverage` still runs but is a never-firing correctness tripwire after Reconcile. Exception — MCP tools: MCP-server tool names aren't known until an operator connects the server at runtime, so per-server `mcp_<server>_*` wildcard policies remain available; the no-wildcard rule applies to the static builtin catalog only. ADR-090 also requires an agent's explicit server/tool assignment before discovery and execution. Registration alone grants no access. Policy and assignment checks both apply, including to tools loaded before a binding was removed.
7. **Release responsibility — fix everything, no excuses.** Every branch fully green before shipping. Pre-existing failures (lint, vuln, Go test, race, vitest, tsc, Playwright — anything CI runs) are ours to fix regardless of origin. "Pre-existing"/"not mine"/"broken on main too" are NEVER acceptable closure paths. Fix now, or get explicit user approval to defer with a tracked issue + target date.
8. **Contract-first wire formats — single source of truth, runtime-validated.** Every byte crossing the gateway/SPA boundary (REST req/resp, WS frame, persisted JSON the SPA reads) MUST be defined in `contracts/openapi.yaml` or `contracts/asyncapi.yaml` **before** any Go/TS code. Generated types in `pkg/api/generated/` and `src/lib/api/generated/` are the only legal cross-boundary types — committed, regenerated via `scripts/gen-contracts.sh`, verified by `make verify-contracts` (fails on drift). **Hand-written wire-format types are FORBIDDEN and lint-caught** by `scripts/check-no-handwritten-wire-types.sh` (opt out: `// not-wire-format`). AsyncAPI Zod schemas are generated, not hand-written. The 5-step process is under Contract regeneration, below.

## Tech stack and platforms

**Backend:** Go (`go.mod` `go 1.26.6` is the minimum toolchain — a 1.22 compiler cannot build this), pure Go (Hard Constraint #2) — `golang.org/x/sys/unix` for kernel interfaces, `modernc.org/sqlite` (no CGo) for the few SQLite uses. All channels are in-process Go; channels wrapping a non-Go runtime spawn a sidecar from their own `Start()` — there is no generic stdio bridge protocol. **Frontend:** TypeScript, React 19, Vite 8, shadcn/ui (Radix + Tailwind v4), AssistantUI, Phosphor Icons, Zustand, TanStack Query + Router, Framer Motion; Vite builds to `dist/spa/`, copied to `pkg/gateway/spa/`, embedded via `go:embed`. **Storage:** file-based only (JSON/JSONL); no PostgreSQL/Redis — SQLite only for WhatsApp/Matrix sessions and the knowledge base's derived, disposable properties index. Data dir `~/.omnipus/`; atomic writes (`fileutil.WriteFileAtomic`). Credentials in `credentials.json` (AES-256-GCM, Argon2id), never in `config.json` — boot contract: ADR-004.

**Supported platforms (founder decision, 2026-09-16): Linux, macOS, Windows — and nothing else.** The BSDs are explicitly out: no CI leg builds them, no release artifact ships for them, nothing is tested there. Do not add `//go:build freebsd|netbsd|openbsd` terms or BSD branches back — that is a regression against this decision, not portability.

## Build, test, and quality gates

**Build tags are always `goolm,stdjson`; `CGO_ENABLED=0`.** The Matrix channel (`pkg/channels/matrix`) is gated behind `//go:build goolm` and the gateway imports it, so without the tags the package will not even compile — `build constraints exclude all Go files in .../pkg/channels/matrix → [setup failed]` is a missing build tag, not a flake, an OOM, or a real bug. Prefer `make test` / `make build`, which inject the tags.

**SPA embed stub trap:** a fresh clone or new worktree fails to compile `pkg/gateway` with an error that looks like a code defect and is not — `pkg/gateway/spa/` (what `//go:embed all:spa` embeds) is gitignored and absent until the first SPA build. Stub it:

```bash
mkdir -p pkg/gateway/spa/assets && echo '<!doctype html>' > pkg/gateway/spa/index.html && touch pkg/gateway/spa/assets/.keep
```

**Never run the full Go test suite locally — CI is the authority for Go test/build results.** `go test ./...` OOM-kills this environment; push and read the checks instead. At most one narrowly-scoped local test when you must (`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/<one>/`); never run multiple Go test suites in parallel.

**Remote CI cluster — run heavy gates here, not locally.** `ci-omnipus-1` (Fly, `sin`) is
**5 machines** of `performance-8x`/16 GB, each a TIER with its own `/cache` volume (repo
clone, Go build cache, `node_modules`) and its own `/tmp/runci.lock`. Scaled to zero when
idle; a stopped machine bills nothing for compute. One command fans the gates out and stops
every machine it started, on every exit path including Ctrl-C:

```bash
deploy/ci-worker/ci-cluster.sh <git-ref>            # full fan-out (ref must be PUSHED)
deploy/ci-worker/ci-cluster.sh <git-ref> go node    # restrict to tiers
```

| Tier | Gates | ~time |
|---|---|---|
| `go` | `gofmt go-build go-vet lint go-test go-race` | ~43 min (`go-race` alone ~25) |
| `node` | `contracts spa` | ~12 min |
| `xplat` | `embed-build records-no-sqlite cli-verb-guard` | ~8 min |

Gates run **sequentially within a tier** (that machine's warm cache is the point; the lock
would queue a second run for up to 90 min) and **tiers run concurrently**. One tier per
machine is enforced — the dispatcher refuses a map that assigns a machine twice. Do NOT
split a tier across machines: tiers are dependency clusters sharing a warm cache, so
splitting buys N cold caches rebuilding the same thing. Override the whole map with
`CI_CLUSTER_TIERS` (`<name>|<machine-id>|<gates>` per line); machine ids are DATA, never
hardcoded — get them from `fly machines list -a ci-omnipus-1`.

**`e2e` is deliberately NOT in the default map** — it needs the OpenRouter secret, runs
~20-30 min, and would pin a machine for the whole run. Give it its own machine when you
want it (`ci-cluster.sh` with a custom tier, or by hand:
`fly ssh console -a ci-omnipus-1 --machine <id> -C "/cache/runci.sh <ref> e2e"`). Inside
that one gate the Playwright suite fans out across the 24 shards in `tests/e2e/shards.json`
— the same plan `.github/workflows/pr.yml` uses, so the two surfaces cannot drift —
capped 2-wide for render-bound shards (a **measured** cap: a 5-wide run failed 17 specs a
1-wide run passed 16/17) and 4-wide for I/O-bound `llm-*`, with `solo` shards strictly
serial and listed last. `all` is refused outright (exit 2): it would serialise every gate
on one machine.

Three traps that produce a **wrong verdict**, not an error: (1) the `fly ssh console`
wrapper's exit code is NOT the gate's — parse the log for `RESULT:` / `GATE FAILURE(S)`;
(2) `/cache/runci.sh` is deployed per machine, so the dispatcher md5-checks every machine
against the repo copy first and refuses on mismatch — `fly ssh sftp put` has silently
written an **empty** file and truncated another, so repair it from the machine's own git
objects (`git show FETCH_HEAD:deploy/ci-worker/runci.sh`) instead; (3) logs live in `/tmp`
and die with the machine — collect before stopping. Full detail and the remaining traps:
`deploy/ci-worker/CLAUDE.md` and `docs/internal/architecture/ci-cluster-design.md`.

**Typecheck trap:** `tsconfig.json` is a project-references root with no `include`/`files` — bare `tsc --noEmit` is a silent no-op that always exits 0. Use `npm run typecheck` (wired to `tsc -b --noEmit`).

**Design system (MANDATORY):** before touching anything under `src/components/`,
`src/styles/`, `design-system/`, or `packages/ui/`, load the `omnipus-design-system`
skill (`.claude/skills/omnipus-design-system/SKILL.md`; `src/components/ui/CLAUDE.md`
carries the two rules that bite hardest). The design-system CI gate teaches by red build
— the skill states each rule with the script or test that fires if you skip it, so you
catch it before CI does, not after. A recurring UI job (tooltip, inline error banner,
copy-to-clipboard, …) uses a catalogued component, preferring a ported shadcn/ui
component over a new local one — skill rule 14.

**Size budgets (founder ruling, 2026-09-15; file fail limit set to 3,000 on 2026-09-22):** one file, one job; one function, one job. A file warns over 2,000 lines and fails over 3,000; a function warns over 120 lines and fails over 240 — same numbers for production and test code, but a React component only warns, never fails. Grandfathered entries (`scripts/budgets/*.txt`) may only shrink — do not add to one, extract first. `make lint-budgets` runs both gates with their self-checks.

**UAT provider/model (founder-set, 2026-09-11):** any agent-driven onboarding uses `openrouter` + `z-ai/glm-5.3-flash` — not whatever an onboarding wizard defaults to. Omnipus sends tools every request, so a non-tool model (e.g. `google/gemma-2-9b-it`) returns 404.

## Retired surfaces — do NOT reintroduce (operator directive, 2026-07-19)

A merge from a branch cut before a removal can resurrect deleted files/surfaces as ordinary, conflict-free additions — **always resolve by keeping the deletion.** Re-adding any of these is a regression, not a conflict resolution.

- **Command Center screen / Schedules UI / `src/components/command-center/`** — deleted; the `/tasks` and `/automations` route files stay redirect stubs into the workspace Board/Calendar. Scheduled/recurring work lives exclusively in the workspace Calendar tab; per-agent heartbeats are the only agent-level exception. The `/api/v1/schedules` REST entity and the `pkg/cron` engine remain — the engine executes task triggers and heartbeats.
- **Raw cron entry or display in any UI** — forbidden product-wide. All scheduling is UI-driven (calendar recurrence editor; see `docs/internal/specs/calendar-recurrence-redesign-spec.md`). Cron survives under the hood only (engine, API, heartbeats).
- **JPEG live-browser screencast fallback** (ADR-061) — WebRTC is the only live-browser video path; a WebRTC failure must be visible (persistent error + Retry), never a blank panel or a silent degrade. Guard: `scripts/check-no-jpeg-screencast.sh`.
- **Goal confirm-gate machinery** (ADR-088) — goals activate instantly; the working agent authors the record via `set_goal`; steering replaces confirmation. Guard: `scripts/check-no-goal-confirm-gate.sh`.
- **Fail-closed per-agent tool-policy backfill** (ADR-077) — see Hard Constraint #6. Guard: `scripts/check-no-fail-closed-backfill.sh`.
- **Goal-ending-on-lost-UI watchdog** (ADR-082) — a turn never depends on a UI connection; only an explicit Stop/cancel (`RequestCancel`, `InterruptSessionHard`) ends a turn early. Guard: `scripts/check-no-orphan-turn-watchdog.sh`.
- **Shell command-text block list, exec allowlist, and the dead exec-approval manager** (ADR-092) — `defaultDenyPatterns`/`secretGuardPatterns`/`applyDenyPatterns`/`compileDenyPatterns`/`denyPatternMessage`/`operatorDenyPatterns`/`GlobalShellDenyPatterns`, `config.SandboxConfig.ShellDenyPatterns` (wire key `shell_deny_patterns`) and `config.AgentShellPolicy` (wire key `shell_policy`), the per-binary exec allowlist (`HandleExecAllowlist`, `AllowedBinaries`), and `pkg/security/execapproval.go`'s `ExecApprovalManager` are all deleted outright — replaced by the Auto-approve setting (`sandbox.auto_approve`, a per-agent "Never auto-approve" off-switch, a per-chat toggle), the D3 config-file-only `sandbox.command_rules` engine, and D7/D8's kernel-backed pre-flight checks; the unenforced `exec_approval`/`enable_deny_patterns` settings keys and the `/security/exec-allowlist` route are gone too. Guard: `scripts/check-no-shell-deny-patterns.sh`.

Guards are wired into CI via `scripts/guards.sh` (`make lint-guards`); discovery picks up every `scripts/check-*.sh`, and each guard's banned-name list lives in that script.

## Spec-Driven Workflow

Change delivery is sized — small (build, 1 reviewer), standard (build with tests, 3 reviewers), feature (spec-driven, 8-reviewer gate) — full flow, gates and roles: `docs/internal/design/dev-team-setup-design-2026-09-25.md`. `/taskify` is retired; task decomposition is team-lead's planning job (`omnipus-planning-orchestration`).

Feature-size work: founder interview (`interview-me`) → an ADR only if a design decision is still open (`architect` writes it; exactly one `grill-spec` ADR-mode round, one correction round) → spec (`plan-spec`) → exactly two `grill-spec` grill-and-fix rounds, with a founder interview after each grill before its fix → any remaining blocking finding escalated to the founder → team-lead plans → RED/GREEN/CHECK → the 8-reviewer gate → founder's yes → land. `grill-code` reviews the landed code against the spec, complementing the gate. `spec-sync` keeps the spec's `Status:` field current after landing. Skills: `.claude/skills/plan-spec/`, `.claude/skills/grill-spec/`, `.claude/skills/grill-code/`, `.claude/skills/spec-sync/`.

## Issue & Project Board Conventions

Follow `docs/internal/issue-and-board-conventions.md` (applies to lead + every subagent). The rules that have actually bitten:

- **Every PR MUST close its issues via keyword (mandatory), one keyword per issue, in the PR *description*** — `Closes #1, closes #2`, not `Closes #1, #2` (which only closes #1). Auto-close only fires on merge into the default branch (`main`), and on squash-merge commit-message keywords are unreliable — the PR body is what GitHub honors. (Sprint #258 / PR #292 left 8 issues open despite shipping their fixes.) A PR that cannot auto-close must still reference every issue it resolves, and whoever merges closes them with a comment citing the PR.
- Issue Type (Bug / Feature / Task / Epic) is set via GraphQL `updateIssueIssueType` — `gh` has no `--type` flag; the `bug`/`enhancement` labels are retired/deleted, never recreate them. Labels are cross-cutting only (`priority:*`, `area:*`, plus `security`/`tech-debt`/`test-coverage`/`documentation`); `type:*` labels are PR/changelog only, never on issues.
- Board #3 automation adds new issues as Backlog and sets Done on close — do not do either by hand. You do set Sprint and promote Status as work proceeds.

## Subagent Workflow

**Dev team — eleven roles** (dev-team design: `docs/internal/design/dev-team-setup-design-2026-09-25.md`). The main session runs `team-lead`, the project-wide default agent: it orchestrates almost all work — decompose, dispatch, review every output against its evidence table, run the gates, land after the founder's yes — reading code and taking only small steps itself. Feature-size work runs as **squads**: a `squad-lead` with its own worktree(s) and feature branch, orchestrating specialists drawn from the eleven roles (`team-lead`, `squad-lead`, `architect`, `backend-lead`, `frontend-lead`, `security-lead`, `qa-lead`, `uat-tester`, `uat-validator`, `docs-verifier`, `prometheus-prompt-engineer` — files in `.claude/agents/`). An in-session squad hands its fully gated branch back to team-lead, which asks the founder and lands it.

Implementing: `backend-lead` (`pkg/`, `cmd/`, `internal/`, plus the `contracts/` spec edits and regeneration, the CI workflows, `deploy/` and the `Makefile` — **all backend trees, the security areas included: backend-lead implements the security code**), `frontend-lead` (`src/`, `packages/ui/`, `design-system/`); design questions and contract shapes → `architect`; tests → `qa-lead` (RED author, CHECK auditor, UAT campaign planner — never fixes production code); UAT lanes → `uat-tester`, each lane's PASS claims checked by an independent `uat-validator`; user-facing docs → drafted by the implementing lead, audited against the code by `docs-verifier`; agent files, dev-team skills and product prompt text → `prometheus-prompt-engineer`. **`security-lead` audits and reviews — never implements:** standing member of the feature-size gate and on-demand reviewer for any change touching its focus areas (`pkg/auth`, `pkg/credentials`, `pkg/fspolicy`, `pkg/identity`, `pkg/pairing`, `pkg/pathsafe`, `pkg/shellrule`, `pkg/security`, `pkg/sandbox`, `pkg/audit`, `pkg/policy`, plus gateway rate limiting and gateway auth). Security findings route to `backend-lead` for the fix, with `security-lead` verifying the fix closes them.

**Change sizes and the review gate (MANDATORY).** Every change is sized first. **Small** (typo, mechanical edit, no design choice — no RED step): one `code-reviewer` pass. **Standard** (real but non-structural work, built with tests in the same step): the 3 fixed reviewers — `code-reviewer`, `silent-failure-hunter`, `pr-test-analyzer`. **Feature** (structural, security-relevant, cross-tree, or anything needing a spec): RED/GREEN/CHECK, then the **8-reviewer gate** — the 6 `pr-review-toolkit` plugin reviewers plus an `architect` cross-cutting pass and a `security-lead` security pass. "Urgent" is a queue priority (front of the queue), not a fourth size; design-system-only stages keep the lighter `/code-review high` ruling. The gate runs on the feature's work branch before any landing, and again as the whole-epic gate on the integration branch before the `→ main` merge — all eight clean or each finding explicitly deferred with a tracked issue. Hard release rule, on par with Constraint #7. `code-simplifier` is the one plugin reviewer allowed to edit the branch under review; its edits are part of the change, covered by the remaining reviewers or a follow-up `code-reviewer` pass on its diff. Every reviewer finding carries a failure scenario, evidence, severity and certainty — a style preference with no failure scenario is not a finding.

**The how lives in the dev-team skills; this file is the what.** `omnipus-shared-rules` (every role — the plugin reviewers load it with the Skill tool via the dispatch template); `omnipus-backend-rules` / `omnipus-frontend-rules` (their roles only); `omnipus-failure-triage` (loaded on failure dispatches); `omnipus-planning-orchestration` (team-lead and squad-lead, at session start and before every new plan); `elicify-test-writing` / `test-integrity-audit` (qa-lead RED / CHECK; vendored, provenance in each skill's `SOURCE.yaml`). Skills elaborate this file, never contradict it, and link to its sections instead of copying them. The six `pr-review-toolkit` reviewers have no repo files of their own — the reviewer discipline and the shared-skill load instruction reach them through `.claude/templates/plugin-reviewer-dispatch.md`, pasted at the head of every plugin-reviewer dispatch.

**Per-session opt-outs from the team-lead default.** The project-wide `"agent"` setting also applies to headless runs (`claude -p …`) started in this repo. A session that must not orchestrate opts out per session: `--agent ""` on the command line, or `"agent": ""` in `.claude/settings.local.json`; an explicit `--agent <worker-role>` names a role directly. Absent positive evidence that a human is present, team-lead behaves as a worker, not an orchestrator (dev-team design, section 5.3).

## Contract regeneration

Wire types are generated from `contracts/openapi.yaml` (REST), `contracts/asyncapi.yaml` (WS), `contracts/components/schemas/` (shared schemas). Artifacts — committed, never hand-edit: `pkg/api/generated/` (Go, oapi-codegen) and `src/lib/api/generated/` (TS types + Zod, openapi-typescript / openapi-zod-client). `make gen-contracts` regenerates all; idempotent on a clean tree.

**Add a new wire type (Constraint #8, 5 steps in order):** (1) add the schema to `contracts/components/schemas/<TypeName>.yaml`; (2) reference it from `openapi.yaml` and/or `asyncapi.yaml`; (3) run `scripts/gen-contracts.sh`; (4) commit the generated diff alongside the spec change (one atomic commit); (5) write the handler/consumer using the generated type only — never a parallel struct/interface.

**Discriminated unions are the one exception to step 1:** the `oneOf` + `discriminator` wrapper must be hosted INLINE in `openapi.yaml` over internal `#/components/schemas/...` refs — oapi-codegen inlines external file refs inside a `oneOf` as anonymous structs and emits non-compiling `As*` accessors. Precedent: `AgentCreateRequest` — see ADR-034.

**`verify-contracts` CI failure** = committed generated files are stale: `make gen-contracts`, review `git diff`, commit `pkg/api/generated/ src/lib/api/generated/`, push. Never commit a spec change without regenerated artifacts; never edit generated files directly.

## Code intelligence: GitNexus (graphify is RETIRED)

**graphify is retired — do not run it, do not look for `graphify-out/`; it does not exist here** (subagents have wasted effort discovering that the hard way). The knowledge graph is GitNexus — tools and rules in the auto-generated block below. Tell every dispatched subagent to use the GitNexus MCP tools (`query`, `context`, `impact`, `trace`, `explain`) first, and that falling back to direct Read/Grep when the graph does not cover the file is correct, not non-compliance. Operating notes (per-checkout indexes, re-index command, disk limits) live in `.claude/CLAUDE.md`.

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **omnipus** (59425 symbols, 226902 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> Index stale? Run `node .gitnexus/run.cjs analyze` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? `npx gitnexus analyze` (npm 11 crash → `npm i -g gitnexus`; #1939).

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows. For regression review, compare against the default branch: `detect_changes({scope: "compare", base_ref: "main"})`.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `query({search_query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `context({name: "symbolName"})`.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method without first running `impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit changes without running `detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/omnipus/context` | Codebase overview, check index freshness |
| `gitnexus://repo/omnipus/clusters` | All functional areas |
| `gitnexus://repo/omnipus/processes` | All execution flows |
| `gitnexus://repo/omnipus/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
