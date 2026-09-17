# CLAUDE.md

Guidance for Claude Code when working in this repository. This root file holds only the rules that apply everywhere; module detail lives in the `CLAUDE.md` next to the code it describes (design: `docs/internal/architecture/draft-module-map.md`, "CLAUDE.md — one per module"). Text cut from this file awaits placement in `docs/internal/architecture/claude-md-module-extracts.md`, keyed by destination path.

**Cite `file::symbol`, not `file:line`** — line numbers in churn-heavy files go stale within days; a symbol citation survives every file split.

## Project

Omnipus is an agentic core: a single Go binary with the SPA embedded via `go:embed`, kernel-level sandboxing (Landlock + seccomp on Linux 5.13+), audit logging, encrypted credential management, and compiled-in Go channels. Community-facing, MIT-licensed, no telemetry. **Domain:** omnipus.ai

**Code wins over docs on any disagreement.** Authoritative references: `docs/internal/architecture/AS-IS-architecture.md` (evidence-based as-is, code-cited), `plugin-extensibility-assessment.md`, `ADR-*.md` (cite by title, not number alone), `docs/internal/_archive/BRD/` (original intent, superseded where it conflicts), and `docs/internal/_archive/preview-doc-v03-concept/` — the current v0.3 direction (pre-ADR).

**Brand & UI:** "The Sovereign Deep", dark-first, chat-first — `docs/internal/brand/brand-guidelines.md`. No emoji in stored data or UI chrome.

## Release Strategy (v0.1 → v0.2 → v0.3)

- **v0.1 — Stabilize `feature/iframe-preview-tier13`.** Ship `web_serve` unification, kernel-enforced bind-port allow-list, sandbox-aware `exec`, iframe preview as one PR. No memory/projects creep.
- **v0.2 — Security hardening (pentest quick wins).** Issue [#155](https://github.com/elicify-ai/omnipus/issues/155). Quick fixes only (env var allowlist, `master.key` 0600 check, shell-guard hardening, internal-CIDR egress blocking, audit HMAC chain, auth-endpoint rate limiting); structural fixes → v0.3.
- **v0.3 / 1.0 — Workspaces redesign.** Issue [#156](https://github.com/elicify-ai/omnipus/issues/156). Fresh-build, no back-compat. Direction: `docs/internal/_archive/preview-doc-v03-concept/` (pre-ADR). The five rooms-era drafts in `docs/internal/_archive/design-2026-05-rooms-era/` are superseded pre-ADR background (retired Rooms/5-core vocabulary) — do **not** implement from them without checking the concept and the forthcoming ADR.

**Routing rule:** when new work comes up, ask which phase it belongs to first. Pentest findings → v0.2 unless structural (→ v0.3). Memory / tasks / agents / workspaces / plugins / marketplaces / room-topology → v0.3. Anything else not completing v0.1 → flag the scope question.

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

1. **Single Go binary** — all backend features compile into one binary. No new runtime deps. SPA embedded via `go:embed`.
2. **Pure Go** — no CGo, no external C libs, no shelling out for security-critical paths. Use `golang.org/x/sys/unix` for kernel interfaces.
3. **Minimal footprint** — security-feature RAM overhead < 10MB beyond baseline.
4. **Graceful degradation** — Linux 5.13+ features (Landlock, seccomp) fall back to app-level enforcement on older kernels, non-Linux, Android/Termux.
5. **Ecosystem compatibility** — follow Omnipus/OpenClaw conventions (SKILL.md, HEARTBEAT.md, SOUL.md, AGENTS.md, JSON config).
6. **Two layers, no third — the reconciled global ceiling IS the default; per-agent overrides only tighten (ADR-077).** Layer 1: the global ceiling (`cfg.Sandbox.ToolPolicies`), kept complete for the whole static catalog (general + browser + `system.*`-legacy-named sysagent tools) by `config.ReconcileToolPolicyCeiling` (ADR-076) on every load; an old install's ceiling self-heals forward additively — a static builtin tool added to `defaults.go` after an install's `config.json` was last written gets its shipped default added to `sandbox.tool_policies` on the next load — and Reconcile never overwrites an operator-set value or re-adds a retired key; reconciling to the shipped default (including `bash = allow`) is intended, not a gap (ADR-077 D2). Layer 2: deliberately sparse per-agent overrides (`AgentConfig.Tools.Builtin.Policies`) that under strictest-wins (`pkg/tools/compositor.go::resolveEffectivePolicyWith`) only ever *tighten* below the ceiling — an agent with no entry riding the ceiling is the normal, intended state, not a gap. There is no hardcoded allow/deny/ask fallback anywhere in the Go code, no `DefaultPolicy`/`GlobalDefaultPolicy` field, and no fail-closed per-agent `deny` backfill or own-coverage boot log — removed by operator decision, do not reintroduce (guard: `scripts/check-no-fail-closed-backfill.sh`). To lock a tool down, set an explicit `deny`, per-agent (tighten one agent) or global (tighten the ceiling for everyone). `bash` is registered for every agent regardless of sandbox mode — the kernel sandbox is the protective layer — and resolves `allow` from the ceiling for an agent with no explicit entry (accepted risk, ADR-077 R1; Jim's seed grants `bash: allow` so he has shell on a fresh install). `config.ValidateToolPolicyCoverage` still runs but is a never-firing correctness tripwire after Reconcile. Exception — MCP tools: MCP-server tool names aren't known until an operator connects the server at runtime, so per-server `mcp_<server>_*` wildcard bulk policies remain the mechanism there; the no-wildcard rule applies to the static builtin catalog only.
7. **Release responsibility — fix everything, no excuses.** Every branch fully green before shipping. Pre-existing failures (lint, vuln, Go test, race, vitest, tsc, Playwright — anything CI runs) are ours to fix regardless of origin. "Pre-existing"/"not mine"/"broken on main too" are NEVER acceptable closure paths. Fix now, or get explicit user approval to defer with a tracked issue + target date.
8. **Contract-first wire formats — single source of truth, runtime-validated.** Every byte crossing the gateway/SPA boundary (REST req/resp, WS frame, persisted JSON the SPA reads) MUST be defined in `contracts/openapi.yaml` or `contracts/asyncapi.yaml` **before** any Go/TS code. Generated types in `pkg/api/generated/` and `src/lib/api/generated/` are the only legal cross-boundary types — committed, regenerated via `scripts/gen-contracts.sh`, verified by `make verify-contracts` (fails on drift). **Hand-written wire-format types are FORBIDDEN and lint-caught** by `scripts/check-no-handwritten-wire-types.sh` (opt out: `// not-wire-format`). AsyncAPI Zod schemas are generated, not hand-written. The 5-step process is under Contract regeneration, below.

## Tech stack and platforms

**Backend:** Go (go.mod requires 1.26.4; targets 1.22+), pure Go (Hard Constraint #2) — `golang.org/x/sys/unix` for kernel interfaces, `modernc.org/sqlite` (no CGo) for the few SQLite uses. All channels are in-process Go; channels wrapping a non-Go runtime spawn a sidecar from their own `Start()` — there is no generic stdio bridge protocol. **Frontend:** TypeScript, React 19, Vite 6, shadcn/ui (Radix + Tailwind v4), AssistantUI, Phosphor Icons, Zustand, TanStack Query + Router, Framer Motion; Vite builds to `dist/spa/`, copied to `pkg/gateway/spa/`, embedded via `go:embed`. **Storage:** file-based only (JSON/JSONL); no PostgreSQL/Redis — SQLite only for WhatsApp/Matrix sessions and the knowledge base's derived, disposable properties index. Data dir `~/.omnipus/`; atomic writes (`fileutil.WriteFileAtomic`). Credentials in `credentials.json` (AES-256-GCM, Argon2id), never in `config.json` — boot contract: ADR-004.

**TypeScript:** Prefer `unknown` over `any`; narrow before use. `unknown` forces a check at the point of use, so the error surfaces where the data actually arrives rather than three call-frames later. Enforced by `@typescript-eslint/no-explicit-any` (error).

**Supported platforms (founder decision, 2026-09-16): Linux, macOS, Windows — and nothing else.** The BSDs are explicitly out: no CI leg builds them, no release artifact ships for them, nothing is tested there. Do not add `//go:build freebsd|netbsd|openbsd` terms or BSD branches back — that is a regression against this decision, not portability.

## Build, test, and quality gates

**Build tags are always `goolm,stdjson`; `CGO_ENABLED=0`.** The Matrix channel (`pkg/channels/matrix`) is gated behind `//go:build goolm` and the gateway imports it, so without the tags the package will not even compile — `build constraints exclude all Go files in .../pkg/channels/matrix → [setup failed]` is a missing build tag, not a flake, an OOM, or a real bug. Prefer `make test` / `make build`, which inject the tags.

**Never run the full Go test suite locally — CI is the authority for Go test/build results.** `go test ./...` OOM-kills this environment; push and read the checks instead. At most one narrowly-scoped local test when you must (`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/<one>/`); never run multiple Go test suites in parallel.

**Typecheck trap:** `tsconfig.json` is a project-references root with no `include`/`files` — bare `tsc --noEmit` is a silent no-op that always exits 0. Use `npm run typecheck` (wired to `tsc -b --noEmit`).

**Size budgets (founder ruling, 2026-09-15):** one file, one job; one function, one job. A file warns over 2,000 lines and fails over 4,000; a function warns over 120 lines and fails over 240 — same numbers for production and test code, but a React component only warns, never fails. Grandfathered entries (`scripts/budgets/*.txt`) may only shrink — do not add to one, extract first. `make lint-budgets` runs both gates with their self-checks.

**UAT provider/model (founder-set, 2026-09-11):** any agent-driven onboarding uses `openrouter` + `z-ai/glm-5.3-flash` — not whatever an onboarding wizard defaults to. Omnipus sends tools every request, so a non-tool model (e.g. `google/gemma-2-9b-it`) returns 404.

## Retired surfaces — do NOT reintroduce (operator directive, 2026-07-19)

A merge from a branch cut before a removal can resurrect deleted files/surfaces as ordinary, conflict-free additions — **always resolve by keeping the deletion.** Re-adding any of these is a regression, not a conflict resolution.

- **Command Center screen / Schedules UI / `src/components/command-center/`** — deleted; the `/tasks` and `/automations` route files stay redirect stubs into the workspace Board/Calendar. Scheduled/recurring work lives exclusively in the workspace Calendar tab; per-agent heartbeats are the only agent-level exception. The `/api/v1/schedules` REST entity and the `pkg/cron` engine remain — the engine executes task triggers and heartbeats.
- **Raw cron entry or display in any UI** — forbidden product-wide. All scheduling is UI-driven (calendar recurrence editor; see `docs/internal/specs/calendar-recurrence-redesign-spec.md`). Cron survives under the hood only (engine, API, heartbeats).
- **JPEG live-browser screencast fallback** (ADR-061) — WebRTC is the only live-browser video path; a WebRTC failure must be visible (persistent error + Retry), never a blank panel or a silent degrade. Guard: `scripts/check-no-jpeg-screencast.sh`.
- **Goal confirm-gate machinery** (ADR-088) — goals activate instantly; the working agent authors the record via `set_goal`; steering replaces confirmation. Guard: `scripts/check-no-goal-confirm-gate.sh`.
- **Fail-closed per-agent tool-policy backfill** (ADR-077) — see Hard Constraint #6. Guard: `scripts/check-no-fail-closed-backfill.sh`.
- **Goal-ending-on-lost-UI watchdog** (ADR-082) — a turn never depends on a UI connection; only an explicit Stop/cancel (`RequestCancel`, `InterruptSessionHard`) ends a turn early. Guard: `scripts/check-no-orphan-turn-watchdog.sh`.

Guards are wired into CI via `scripts/guards.sh` (`make lint-guards`), each with a Makefile target; full deletion inventories (every symbol, file and wire type) live in the module extracts file.

## Spec-Driven Workflow

When implementing features: (1) read the relevant BRD/spec section(s); (2) `/plan-spec` for TDD/BDD specs; (3) `/grill-spec` to stress-test; (4) `/taskify` to decompose; (5) implement in Plan Mode first; (6) `/grill-code` to verify compliance.

## Issue & Project Board Conventions

Follow `docs/internal/issue-and-board-conventions.md` (applies to lead + every subagent). The rules that have actually bitten:

- **Every PR MUST close its issues via keyword (mandatory), one keyword per issue, in the PR *description*** — `Closes #1, closes #2`, not `Closes #1, #2` (which only closes #1). Auto-close only fires on merge into the default branch (`main`), and on squash-merge commit-message keywords are unreliable — the PR body is what GitHub honors. (Sprint #258 / PR #292 left 8 issues open despite shipping their fixes.) A PR that cannot auto-close must still reference every issue it resolves, and whoever merges closes them with a comment citing the PR.
- Issue Type (Bug / Feature / Task / Epic) is set via GraphQL `updateIssueIssueType` — `gh` has no `--type` flag; the `bug`/`enhancement` labels are retired/deleted, never recreate them. Labels are cross-cutting only (`priority:*`, `area:*`, plus `security`/`tech-debt`/`test-coverage`/`documentation`); `type:*` labels are PR/changelog only, never on issues.
- Board #3 automation adds new issues as Backlog and sets Done on close — do not do either by hand. You do set Sprint and promote Status as work proceeds.

## Subagent Workflow

The lead orchestrates all work by spawning subagents via the Agent tool (no teams): decompose into focused units, give each a complete prompt (spec ref, exact files, definition of done), run independent work in parallel, review every output, run QA after implementation. Implementing: `frontend-lead` (`src/`, `packages/ui/`), `backend-lead` (`pkg/`, `cmd/`, `internal/` except security), `security-lead` (security areas), `qa-lead` (tests only); design questions → `architect`. **Review pipeline — 7-reviewer quality gate (MANDATORY):** runs twice, after each feature (before its PR merges to base) and on the whole epic diff before the final `→ main` PR. All seven clean or each finding explicitly deferred with a tracked issue. Hard release rule, on par with Constraint #7.

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
