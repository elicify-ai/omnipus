# Env-Awareness + Agent Memory Architecture Spec (v7)

**Created**: 2026-04-24 (v5), extended 2026-04-23 (v6), revised 2026-04-24 (v7 post-grill).
**Status**: Draft — v7 resolves all 6 CRITICAL and 8 MAJOR findings from the v6 grill with code-verified fixes. Fix A (env-awareness) + Fix C (v5 memory) ship together. **Fix B remains out of scope**.
**Scope**: One PR. Parallel subagent implementation. Full-codebase tests + new tests. 6 PR-review agents gate the merge (strict pass).

## v7 change log (one line per CRIT/MAJ resolved, each with file:line verification)

| v6 defect | v7 resolution |
|---|---|
| **CRIT-001** `cfg.Sandbox.AllowedHosts` doesn't exist | Drop `AllowedHostCount` from `NetworkPolicy`. US-10 AC-6 simplified to `outbound-allowed` or `outbound-denied`. Dataset row 5 deleted. Verified: only `AllowNetworkOutbound bool` at `pkg/config/sandbox.go:104`. Follow-up issue tracks `AllowedHosts` addition when sandbox allow-list is implemented. |
| **CRIT-002** `cfg.Gateway.AllowEmptyBearer` doesn't exist | Delete every `allow_empty_bearer` reference. Active-warnings set shrinks to: `DevModeBypass=true` (verified `pkg/config/config.go:1051`), `runtime.GOOS=="windows"`, sandbox is `fallback` on Linux kernel ≥ 5.13. Test #53 dropped. |
| **CRIT-003** `SandboxBackend.Describe()` clashes with existing `DescribeBackend()` | No interface change to `SandboxBackend`. v7 reuses `sandbox.DescribeBackend(backend) Status` at `pkg/sandbox/sandbox.go:618`. New `envcontext.renderSandboxMode(sandbox.Status) string` helper maps `Status` → stable string. FR-059 deleted. |
| **CRIT-003a** `sandbox_fallback.go` / `sandbox_windows.go` don't exist | Lane E's sandbox files corrected to `pkg/sandbox/sandbox.go` + `pkg/sandbox/sandbox_linux.go` + `pkg/sandbox/sandbox_other.go`. Verified via `ls pkg/sandbox/`. |
| **CRIT-004** cache-lifecycle contradiction | Single lifecycle, pinned throughout: **env preamble is part of the cached system prompt**. US-10 AC-11, Behavioral Contract, test #61 all rewritten. REST config hooks call `InvalidateCache()` on a central `ContextBuilderRegistry` (new — FR-061). Test #61b added for cache-rebuild property. |
| **CRIT-005** provider methods can't return errors | Provider interface revised: `Platform() (Platform, error)` and `SandboxMode() (string, error)` (fields that can fail). Other methods cannot fail. FR-054 now reads: "any provider method returning `error` renders the field as `<unknown>` and logs `slog.Debug`". |
| **CRIT-006** SC-010b unbounded on "any primary model" | New explicit allow-list in FR-029a: recap runs only when `recap_model` resolves to `{claude-sonnet-*, gpt-4o-mini, z-ai/glm-*, gemini-flash-*}`. Recap call MUST set `max_tokens=250`, `extended_thinking=false`, `reasoning.exclude=true`. Boot fails with `config error` if `AutoRecapEnabled=true` and `recap_model` is outside the allow-list. |
| **MAJ-001** `buildSubturnContextBuilder` doesn't exist | FR-058 rewritten against real mechanism: subagent inherits parent ContextBuilder via struct-literal at `pkg/agent/subturn.go:402` (`ContextBuilder: baseAgent.ContextBuilder`). Same pointer → same `environmentProvider`. Test #63 asserts `subAgent.ContextBuilder == parentAgent.ContextBuilder`. No wrapper function to edit. |
| **MAJ-002** `validateEntityID` unexported; circular if imported | Move to `pkg/validation/entityid.go` with exported `EntityID(id string) error`. Update 7 gateway callers. New FR-062 captures the move. Dedicated cross-cutting task in lane T's prep. |
| **MAJ-003** `SystemPromptOverride` test was vacuous | Test #46 rewritten as positive behavioral test: spawn subagent with non-empty `cfg.ActualSystemPrompt`, capture child's first LLM prompt via `RecentLLMRequests`, assert parent's `BuildSystemPromptWithCache()` output appears verbatim at the top AND the override does NOT appear in the child's prompt. Also add grep regression: `grep -n 'opts.SystemPromptOverride' pkg/agent/loop.go` must return zero matches. |
| **MAJ-004** `parts[0]` shift not structurally tested | New test #58b `TestContextBuilder_BuildSystemPrompt_StructureAfterEnvPrepend` asserts full layout: `## Environment` → `\n\n---\n\n` → identity marker → `\n\n---\n\n` → bootstrap/skills/memory in existing order. |
| **MAJ-005** Lane E/Lane M line-range overlap | Introduced **PR-0 (prep)**: a one-landing-before-lanes PR that adds empty `parts = append(parts, cb.GetEnvironmentContext())` hook BEFORE identity; `GetMemoryContext()` call already exists. Lanes E and M then only edit their *helper* files, not `BuildSystemPrompt` itself. Ownership matrix updated. |
| **MAJ-006** coord rules still mentioned `LLMProvider.Identity()` | Struck from Coordination rule 1. Added a CI guard test that fails if any file mentions `LLMProvider.Identity` — matches Q3 = C decision. |
| **MAJ-007** bootstrap burst could send 5000+ LLM calls | New FR-032a: bootstrap pass requires separate `BootstrapRecapEnabled=true` opt-in (distinct from `AutoRecapEnabled`); rate-limited to `BootstrapRecapMaxPerMinute` (default 5); archived sessions (missing `.jsonl`) skipped with audit `outcome=skipped_archived`; accumulates cost estimate and aborts on daily-budget exceed. |
| **MAJ-008** shared audit-log retention semantics | FR-011 clarified: memory-mutation entries share `pkg/audit.Logger` (default rotation 50 MB, retention 90 days, verified `pkg/audit/audit.go:71-144`). Operators filter by `event` field via `jq`. Per-event-type retention is out of scope (follow-up). |
| MIN-001 through MIN-007, OBS-001 through OBS-005 | Each addressed inline in FRs below; cross-referenced in the Ambiguity and Clarifications tables. |

## Decisions applied (Q1–Q5 interview, 2026-04-23; unchanged in v7)

| # | Question | Decision | Rationale |
|---|---|---|---|
| Q1 | Env preamble disable-able? | **A — always on, no off-switch** | Agents without env info waste turns on blocked actions (5 of 6 real incidents). |
| Q2 | Verbosity tier / per-turn or per-session? | **A + nuance — one size for everyone, generated once at session start, cached with the system prompt, NOT re-rendered per turn.** Content priority: paths allowed/denied + harness explanation ("new-office onboarding"). | Simpler cache model; kills the latent cache-bug. Fully codified in v7 FR-053 + FR-061 (CRIT-004 resolution). |
| Q3 | Include provider rotation / model-capability info? | **C — neither. Keep preamble strictly about paths + sandbox + active warnings.** Improve over time. | Shortest possible onboarding; provider rotation is a framework-transparent concern. |
| Q4 | Review-gate strictness? | **A — strict, all 6 reviewers must return `pass`.** Blockers AND advisories gate the merge. | No review fatigue loophole. |
| Q5 | Baseline-SHA for test gate? | **C — no baseline. Full-green requirement.** `go test ./...`, `vitest run`, `tsc --noEmit` must all pass cleanly; any pre-existing flakes are fixed in this PR. | Matches "everything in one PR" direction. v7 adds a narrow quarantine escape (OBS-004). |

These decisions flow through to the FRs, SCs, test plan, and Done Criteria below.

## What v6 adds on top of v5

v5 specced Fix C (memory) fully. v6 adds **Fix A — environment-aware minimal system prompt** and re-scopes the PR so both fixes land together under one branch.

| Concern | v5 | v6 |
|---|---|---|
| Fix A — env-awareness prompt | out of scope ("Env-prompt parallel block") | **IN SCOPE** (US-10, US-11, FR-041..FR-060) |
| Fix B — UI `activeSessionId` persistence | out of scope | still out of scope |
| Fix C — memory (remember/recall/retrospective + session-end pipeline) | fully specced | unchanged, carried forward |
| Parallel subagent implementation plan | coarse 11 tracks | **4 parallel lanes** with explicit file-ownership partition |
| E2E coverage | 3 E2E rows | **9 E2E rows** — half target env-awareness regressions (network-sandbox denial, dev-mode-bypass visibility, workspace path leak), half target memory |
| Review gate | 6 PR reviewers | **strict: every reviewer must return `pass`; advisories also block** (Q4 = A) |
| Test suite gate | `go test ./...` + `npx vitest run` + `tsc --noEmit` | **fully-green, no baseline comparison** (Q5 = C) — pre-existing flakes fixed inside this PR |
| Preamble lifecycle | — | **generated once at session start, cached with system prompt, NOT re-rendered per turn** (Q2 = A with nuance); minimal content — paths + sandbox + warnings only (Q3 = C) |

**Fix A in one sentence**: every agent and subagent, at the start of a new session, receives a short "new-office onboarding" block at the top of their system prompt — ABOVE SOUL.md — telling them which paths they can/cannot use, what sandbox and network policy are active, and any operator-side warnings. It's cached with the rest of the system prompt and does NOT regenerate per turn.

---

## Problem Statement

### Class-A failures (env-awareness) — why Fix A

Six real incidents from the preceding sessions, all caused by agents assuming a different environment than they were actually running in:

1. **Network-sandbox blindness** — Landlock ABI-v4 enabled with `AllowNetworkOutbound=false`. Agent tries `curl`, gets `Operation not permitted`, loops with no understanding that network is intentionally denied. Agent should have known upfront.
2. **OMNIPUS_HOME leak** — agent assumes `$HOME/.omnipus` while gateway runs with `OMNIPUS_HOME=/tmp/omnipus-e2e`. Agent writes to the wrong directory; operator investigation needed. Agent should have known the real path.
3. **Dev-mode-bypass invisibility** — operator enables `dev_mode_bypass=true` for onboarding, forgets to disable. Agent continues assuming auth is strict. Agent should have had a visible warning.
4. **Sandbox path confusion** — `/dev/tty` blocked in default sandbox. Agent retries TTY-dependent subprocess without understanding why it hangs. Agent should have known the `safePaths` list.
5. **Provider rotation surprises** — OpenRouter silently rotates between Bedrock/Vertex. Agent's tool_use IDs break across turns. **Per Q3 decision (C), Fix A does NOT surface this in the preamble** — rotation is treated as a framework-transparent concern (transcript-repair pipeline handles it). Deferred to follow-up.
6. **Model capability mismatch** — model doesn't support tool use (e.g., `google/gemma-2-9b-it`), agent keeps requesting tools. **Per Q3 decision (C), Fix A does NOT surface this in the preamble**. The framework should refuse to provision tools to a non-tool-capable model at boot. Deferred to follow-up.

Incidents 1–4 are fully addressed by Fix A (the preamble surfaces paths, sandbox, network, dev-mode warnings). Incidents 5–6 are deferred as framework-side fixes, not agent-awareness fixes. Every incident is a **first-class agent-experience bug**, not a framework bug in isolation.

### Class-C failures (memory) — why Fix C (v5 carried forward)

Agents lose context across sessions. See v5 problem statement in the `Fix C (Memory) — Reference` section below.

---

## Combined scope

**In scope (v6)**:
- Fix A: env-awareness preamble (new section in `BuildSystemPrompt`, before SOUL.md).
- Fix C: memory system (all v5 scope).
- Unified audit (reuses `pkg/audit.Logger`).
- Unified test gate: `go test ./...` + `npx vitest run` + `tsc --noEmit` against a pinned baseline SHA (filled into PR description at branch creation, NOT into spec text).
- 6-reviewer gate.
- 4-lane parallel implementation plan.

**Out of scope (v6)**:
- Fix B (UI `activeSessionId` persistence).
- Device-file allow-list expansion.
- `SystemPromptOverride` wiring (tracked as follow-up; v6 verifies it stays dead via regression test #46).
- System Agent refactor (issue #141).
- Semantic search via embeddings (v7).
- Moving transcript `system_origin` to a first-class field (v6 uses content-prefix heuristic per v5 CRIT-002 resolution).
- Agent-initiated env-preamble mutation (read-only in v6; operator-only writes).

---

## Available Reference Patterns

| Reference | Purpose in v6 |
|---|---|
| `pkg/fileutil/flock_unix.go:35 WithFlock(path, fn)` | Every memory write wrapped in `WithFlock`. Process+goroutine-safe. Windows no-op with warning. |
| `pkg/fileutil/file.go:54 WriteFileAtomic` | `LAST_SESSION.md` overwrite. Retains temp+rename atomicity. |
| `pkg/fileutil/file.go:144 AppendJSONL` | Template for audit writes (but we use `pkg/audit.Logger`). |
| `pkg/audit/audit.go:71-144 Logger` | Reused for memory-mutation audit AND env-preamble operator changes. |
| `pkg/agent/memory.go:86-112 AppendToday` | Template for `AppendLongTerm` format. |
| `pkg/agent/context.go:196 BuildSystemPrompt` | **v6 inserts env preamble as `parts[0]` here.** |
| `pkg/agent/context.go:265 BuildSystemPromptWithCache` + `sourcePaths():326` | Cache layer. Extended with `memory/sessions/LAST_SESSION.md`. Env preamble NOT cache-keyed on files (derived from runtime state; cheap to rebuild). |
| `pkg/tools/spawn_status.go` | Template for new tool files. |
| `pkg/session/unified.go:206 GetMeta` | Look up `SessionMeta.AgentID` from a sessionID. |
| `pkg/agent/registry.go:103 ListAgentIDs` + `pkg/agent/instance.go:34 Workspace` | Enumerate agents for bootstrap sweep. |
| `pkg/config/config.go:591 RoutingConfig.LightModel` | Recap model source. |
| `pkg/providers/types.go:24-33 LLMProvider.Chat` | Direct LLM call interface — what the recap pipeline invokes. |
| `pkg/session/retention_sweep.go:18 RetentionSweep` + `pkg/gateway/retention_goroutine.go` | Existing sweep cadence. v6 adds a sibling call for memory retros. |
| `pkg/gateway/rest_sandbox_config.go:30` + `pkg/gateway/sandbox_apply.go:75` | Runtime sandbox state surface for env preamble. |
| `pkg/config/home.go` | Canonical `OmnipusHomeDir()` helper for env preamble. |
| `pkg/providers/common/common.go` | Runtime provider + model identity for env preamble. |
| `pkg/agent/subturn.go:443 SystemPromptOverride` | Dead assignment. v6 regression test asserts it stays dead OR, if wired, re-propagates the env preamble. |

---

## Existing Codebase Context

### Symbols involved (v6, code-verified)

**Fix A — env-awareness**

| Symbol | Action | Notes |
|---|---|---|
| `ContextBuilder.GetEnvironmentContext()` | **NEW** | Returns rendered env preamble string. Reads runtime state (no disk I/O). |
| `ContextBuilder.environmentProvider` | **NEW field** | Injected `EnvironmentProvider` interface; testable. |
| `EnvironmentProvider` interface | **NEW** in `pkg/agent/envcontext/provider.go` | `Platform()`, `SandboxMode()`, `NetworkPolicy()`, `WorkspacePath()`, `OmnipusHome()`, `ProviderIdentity()`, `ModelName()`, `ActiveWarnings()`. |
| `DefaultEnvironmentProvider` | **NEW** | Backed by `AgentLoop` + `config.Config` + `runtime.GOOS`. |
| `BuildSystemPrompt` (in `pkg/agent/context.go:196`) | **EDIT** | Insert env preamble as `parts[0]` when non-empty. |
| `subturn.go:402` struct-literal assignment | **NO EDIT** | Subagent already inherits parent's ContextBuilder pointer (MAJ-001 resolution — struct-literal `ContextBuilder: baseAgent.ContextBuilder`). Provider propagates automatically. |

**Fix C — memory** (unchanged from v5, for continuity)

| Symbol | Action | Notes |
|---|---|---|
| `MemoryStore.AppendLongTerm(content, category)` | NEW | Calls `WithFlock(memoryPath, fn)` where `fn` does `O_APPEND` write. |
| `MemoryStore.ReadLongTermEntries()` | NEW | mtime-cached parser. |
| `MemoryStore.SearchEntries(query, limit)` | NEW | Cross-file substring search: MEMORY.md + LAST_SESSION.md + last 30 days of retros. |
| `MemoryStore.WriteLastSession(content)` | NEW | `WriteFileAtomic` (overwrite). |
| `MemoryStore.ReadLastSession()` | NEW | |
| `MemoryStore.AppendRetro(sessionID, r Retro)` | NEW | `WithFlock` wrapper; `sessionID` MUST pass `validateEntityID` first. |
| `MemoryStore.SweepRetros(retentionDays int)` | NEW | Called from retention_goroutine. Per-workspace enumeration. |
| `MemoryStore.mu sync.Mutex` | NEW | Protects in-memory cache. Does NOT serialize file writes (flock does that). |
| `pkg/tools/memory.go` | NEW file | `RememberTool`, `RecallMemoryTool`, `RetrospectiveTool`. |
| `pkg/agent/context.go:getWorkspaceAndRules` Rule 4 | EDIT | Exact new text in FR-018. |
| `pkg/agent/context.go:GetMemoryContext` | EDIT | Prepend `## Last Session`. |
| `pkg/agent/context.go:sourcePaths()` | EDIT | Append `memory/sessions/LAST_SESSION.md`. |
| `pkg/agent/instance.go:NewAgentInstance` | EDIT | Register 3 tools unless agent id ∈ {`main`, `omnipus-system`}. |
| `pkg/agent/session_end.go` | NEW file | Session-end pipeline + bootstrap pass. |
| `pkg/agent/loop.go:AgentForSession` | NEW method | `us.GetMeta(sid).AgentID` → registry lookup. |
| `pkg/agent/loop.go:idleTickers sync.Map` | NEW field | `sessionID → context.CancelFunc`. |
| `pkg/agent/loop.go:agentCurrentSession sync.Map` | NEW field | `agentID → sessionID`, used by lazy trigger. CAS-safe access. |
| `pkg/agent/loop.go:RecentLLMRequests` | NEW method (test hook) | Only populated when env `OMNIPUS_RECENT_LLM_REQUESTS_ENABLED=1`. |
| `pkg/gateway/websocket.go` | EDIT | New frame type `session_close`; hooks into `agentCurrentSession`. |
| `pkg/gateway/retention_goroutine.go:executeSweepTick` | EDIT | After existing sweep, iterate agents and call `SweepRetros(memoryRetentionDays)`. |
| `pkg/config/config.go:OmnipusRetentionConfig` | EDIT | Add `MemoryRetrosDays int` (default 30). |
| `pkg/config/config.go:AgentDefaults` | EDIT | Add `AutoRecapEnabled bool` (default `false`), `IdleTimeoutMinutes` (default 30). |

### Impact Assessment

| Symbol Modified | Risk | Direct Dependents | Indirect |
|---|---|---|---|
| `BuildSystemPrompt` (parts[0] insertion) | **MEDIUM** | Every agent's first LLM call on every session | All LLM-driven features; cache behavior |
| `subturn.go:402` (no edit required) | **LOW** | Pointer already shared; env preamble propagates automatically | Subagent correctness via test #63 pointer-equality assertion |
| `getWorkspaceAndRules` Rule 4 | LOW | Rules text only | None until agents observe the rule |
| `MemoryStore` (new methods) | LOW | Three new tools; session-end | None |
| `retention_goroutine.go` | LOW | Retention cadence | None until N days elapse |
| `AgentDefaults` config | LOW | Config load | Default behavior (opt-in gate keeps it LOW) |

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Agent boot → `BuildSystemPromptWithCache` → LLM call | Fix A adds env preamble as first `parts[]` element |
| Subagent spawn → `subturn.go:402` struct-literal → SAME ContextBuilder pointer → `BuildSystemPromptWithCache` | Fix A: subagent sees identical env preamble automatically (shared pointer). No propagation code needed. |
| Config change via REST (`rest_sandbox_config.go`) | Fix A: env preamble rebuilds on next call; no cache invalidation necessary (env reads are cheap) |
| Session end (any of 3 triggers) → recap → LAST_SESSION.md + retro | Fix C: unchanged from v5 |
| Memory tool call → `WithFlock` write → audit | Fix C: unchanged |

---

## User Stories & Acceptance Criteria

### Fix A user stories

#### US-10 — Env-awareness preamble visible to every agent (P0)

Every agent's `BuildSystemPrompt()` output begins with a `## Environment` section containing platform, sandbox policy, network policy, workspace path, OMNIPUS_HOME, active provider+model, and any active warnings. This section is **always present** (never gated behind config); dev-mode-bypass or any non-default state triggers additional explicit warnings.

**Acceptance**:
1. Core agent (`omnipus-system`, `ava`, `jim`, etc.) receives env preamble at top of system prompt.
2. Custom agent with SOUL.md receives env preamble ABOVE SOUL.md.
3. Env preamble rendered ≤ 2000 runes total (hard cap; over-budget → truncate with ellipsis marker).
4. Platform info reflects `runtime.GOOS` + `runtime.GOARCH` + kernel release (Linux only, via `/proc/version`; darwin/windows report OS version).
5. Sandbox mode: one of `off | fallback | landlock-abi-<N> | unknown`. Derived from existing `sandbox.DescribeBackend(backend) Status` via `envcontext.renderSandboxMode(Status)` helper (CRIT-003 — no interface change).
6. Network policy: `outbound-allowed | outbound-denied`. Derived from `cfg.Sandbox.AllowNetworkOutbound` alone. (Allow-list branch removed in v7 — CRIT-001 — since `AllowedHosts` field doesn't exist yet.)
7. Workspace path: absolute path from `agent.Workspace`.
8. OMNIPUS_HOME: absolute path from `config.OmnipusHomeDir()`.
9. Provider + model: `provider=openrouter, model=z-ai/glm-5-turbo` form.
10. Active warnings section present iff any of: `dev_mode_bypass=true`, Windows host (flock no-op), sandbox in fallback mode on Linux ≥ 5.13 (indicates explicit downgrade), or kernel version cannot be detected on Linux (conservative: warn on unknown-kernel). `allow_empty_bearer` DROPPED (CRIT-002 — never existed).
11. **Rebuilt on each system-prompt cache rebuild**, not every `BuildSystemPrompt` call (v7 CRIT-004 resolution: single cache lifecycle — env preamble embedded in the cached system prompt). Runtime config changes (e.g., operator toggles `dev_mode_bypass` via REST) trigger `InvalidateAllContextBuilders()` (FR-061), so the next turn's prompt-build reflects the change.

#### US-11 — Subagent inherits parent's env preamble (P0)

A subagent spawned from a parent agent receives the SAME env preamble in its system prompt. The env state is a property of the gateway process, not of the agent — so subagent and parent see identical environment info.

**Acceptance**:
1. Subagent's `BuildSystemPrompt` output includes the env preamble at the top (same content as parent).
2. Subagent's ContextBuilder references the same `EnvironmentProvider` instance as the parent (via cloned `ContextBuilder`), so any runtime state change is visible in both.
3. Subagent spawned via `subagent` tool AND via handoff AND via spawn: all three include the preamble.
4. Test-verifiable via `RecentLLMRequests` hook: the subagent's system prompt (captured at send time) contains the env preamble markers.

### Fix C user stories (unchanged from v5)

#### US-1 — Durable memory via `remember` (P0)

Agent calls `remember(content, category)` → entry appended to `<workspace>/memory/MEMORY.md` with timestamp + category metadata. Categories: `key_decision`, `reference`, `lesson_learned`.

**Acceptance**:
1. Valid call → file exists, entry present, audit entry `outcome=ok`, tool returns `"ok"`.
2. Second call → appends; both entries present, newest-first on read.
3. Invalid category → `IsError=true`; audit `outcome=error_invalid`.
4. `len([]rune(content)) > 4096` → `IsError=true`; audit `outcome=error_invalid`.
5. Empty/whitespace content → `IsError=true`; audit `outcome=error_invalid`.
6. Content contains `<!--` substring → `IsError=true`; audit `outcome=error_invalid`.
7. Concurrent writes across different `MemoryStore` instances → serialised via `WithFlock`; no byte-interleaving.
8. NUL bytes stripped from content silently.
9. I/O error → `IsError=true`; audit `outcome=error_io` with classification.

#### US-2 — Recall via `recall_memory` (P0)

Agent calls `recall_memory(query, [limit])` → top-N matching entries (newest-first) across MEMORY.md + LAST_SESSION.md + retros dated within the last 30 days.

**Acceptance**:
1. Query matches → results returned, cap `min(limit, 50)`, default 20.
2. Zero matches → `IsError=false`, `ForLLM="no matching entries"`.
3. Empty query → `IsError=true`.
4. Search is literal substring (regex metachars treated literally), case-insensitive.
5. In-memory cache avoids re-reading files within the same mtime. Cache invalidates on mtime advance.
6. Retros older than 30 days NOT searched.
7. Read audits are NOT written.

#### US-3 — Auto session-end recap (P0, opt-in)

Gated by `agent.memory.auto_recap_enabled` (default `false`). See v5 for full acceptance (AC-1..11).

#### US-4 — Joined retrospective via `retrospective` (P0)

See v5. Acceptance unchanged.

#### US-5 — System-prompt injection (P0)

`GetMemoryContext` emits `## Last Session` + `## Long-term memory`. See v5 AC-1..6 (subagent-visibility clarified in MAJ-007).

#### US-6 — Rule 4 names the three tools (P1)

See v5, FR-018 pinned text.

#### US-7 — Registration scope (P1)

Three tools on all agents with id ∉ `{main, omnipus-system}`. Subagent clones include all three.

### Cross-cutting user stories

#### US-8 — E2E coverage (P0, expanded in v6)

Playwright + Go E2E tests covering **both** env-awareness regressions and memory flow:

- env-awareness: network-sandbox denial visibility, dev-mode-bypass warning, workspace path correctness, subagent inheritance
- memory: remember+recall roundtrip, joined retrospective, retro-recall, cross-session continuity (opt-in)

#### US-9 — Review + full-codebase test gate (P0)

`go test ./...`, `npx vitest run`, `npx tsc --noEmit` all fully green on the PR's final HEAD (no baseline comparison, per Q5 = C). 6 PR-review agents each return `pass`; any `blocker` or `advisory` blocks the merge (per Q4 = A).

#### US-12 — Parallel implementation safety (P0, NEW in v6)

Work partitions into 4 lanes with strict file-ownership. Lanes run in parallel via sub-branches; final integration PR merges them back. No two lanes edit the same file; any cross-lane coordination happens via agreed interfaces defined in v6 `Existing Codebase Context` table.

**Acceptance**:
1. Each lane owns its files exclusively.
2. Cross-lane interfaces (e.g., `EnvironmentProvider`) are defined in one lane and consumed by the other; no merge conflicts expected.
3. Integration PR cleanly merges all four lane branches.

---

## Behavioral Contract

### Env-awareness (Fix A)

- `BuildSystemPrompt()` emits a `## Environment` preamble as `parts[0]` (non-empty).
- Preamble content derives from runtime state: platform, sandbox mode, network policy, workspace path, OMNIPUS_HOME, active warnings. **No provider/model identity** (Q3 = C). **No AllowedHosts count** (CRIT-001).
- Preamble is bounded: ≤ 2000 runes (truncate with `[env context truncated]` suffix if over).
- Subagent shares parent's `*ContextBuilder` pointer (struct-literal assignment at `subturn.go:402`), so it automatically gets the same preamble.
- **Preamble is part of the cached system prompt** (CRIT-004 single lifecycle): rebuilt only when the outer cache invalidates (file mtime or explicit `InvalidateCache()`).
- Operator config change → REST handler calls `InvalidateAllContextBuilders()` (FR-061) → next turn's prompt-build reflects the new env.

### Memory (Fix C, unchanged from v5)

- `remember` / `retrospective` / auto-recap writes → `WithFlock(path, fn)` → append → audit `outcome=ok`.
- `recall_memory` → cache-backed cross-file substring search → newest-first results.
- Session end via one of three triggers (iff opt-in): background goroutine → `AgentForSession` lookup → capped LLM call → LAST_SESSION.md + retro + audit.

### Errors

- Env-awareness: if a sub-field errors (e.g., `/proc/version` unreadable), render the field as `<unknown>`. Never fail `BuildSystemPrompt`.
- Memory: validation failures + I/O errors produce `IsError=true` + audit entry with `outcome` field. LLM recap failures produce heuristic fallback.

### Boundaries

- Env preamble ≤ 2000 runes.
- MEMORY.md injected ≤ 12000 runes.
- Recap input ≤ 2000 tokens.
- Concurrent writers across `MemoryStore` instances → serialise via flock.

---

## Explicit Non-Behaviors

### Env-awareness (Fix A)

- Env preamble is **read-only from the agent's perspective** — no tool mutates it.
- Env preamble does NOT include secrets, credentials, API keys, bearer tokens, or host-identifying information beyond `runtime.GOOS` + kernel release. Hostnames are NEVER included.
- Env preamble does NOT include any allow-list metadata in v7 (the `AllowedHosts` config field doesn't exist yet — CRIT-001). When it lands, the preamble will surface "outbound-allowlist:N" (count only, never hostnames) via follow-up PR.
- Env preamble is NOT user-configurable — it's derived entirely from runtime state.
- Env preamble does NOT trigger any LLM call or I/O outside reading in-process config.
- Env preamble does NOT populate the `RecentLLMRequests` test hook separately — it's part of the system prompt captured in that hook.
- Custom agents CANNOT override the env preamble (deny-by-default for security-critical context).

### Memory (Fix C, unchanged)

- `MEMORY.md` is append-only via tools.
- Three memory tools NOT registered on System Agent.
- Auto-recap is OFF by default; gated by `agent.memory.auto_recap_enabled`.
- `recall_memory` reads NOT audited.
- `retrospective` tool writes contain NO `user_confirmed` field.
- Memory tools do NOT reach into other agents' workspaces.
- Search is literal substring, case-insensitive.

---

## Integration Boundaries

### EnvironmentProvider (`pkg/agent/envcontext/provider.go`, NEW)

```go
package envcontext

type Provider interface {
    Platform() (Platform, error)        // GOOS, GOARCH, Kernel release; err if /proc/version unreadable
    SandboxMode() (string, error)       // "off" | "fallback" | "landlock-abi-N" — err if underlying Status read fails
    NetworkPolicy() NetworkPolicy       // cannot fail: reads one bool from in-memory config
    WorkspacePath() string              // cannot fail: reads one string from agent instance
    OmnipusHome() string                // cannot fail: reads one string from config
    ActiveWarnings() []string           // cannot fail: derived from in-memory state
}

type Platform struct{ GOOS, GOARCH, Kernel string }
type NetworkPolicy struct {
    OutboundAllowed bool
    // NOTE: AllowedHostCount REMOVED in v7 (CRIT-001): cfg.Sandbox has no AllowedHosts field today.
    // When sandbox allow-list lands, reintroduce here and extend rendering to show "outbound-allowlist:N".
}
```

`DefaultProvider` reads from: `runtime.GOOS`, `runtime.GOARCH`, `/proc/version` (Linux only), `sandbox.DescribeBackend(backend)` → rendered via `envcontext.renderSandboxMode(Status)`, `cfg.Sandbox.AllowNetworkOutbound`, `agent.Workspace`, `config.OmnipusHomeDir()`, `cfg.Gateway.DevModeBypass` (verified exists: `pkg/config/config.go:1051`).

**Drops from v6**: `cfg.Sandbox.AllowedHosts` (never existed), `cfg.Gateway.AllowEmptyBearer` (never existed), `ProviderIdentity()` (Q3 = C), `SandboxBackend.Describe()` interface method (CRIT-003 clash).

### `renderSandboxMode(sandbox.Status) string` mapping (pinned)

```
Status{Backend:"none", Available:false}                              → "off"
Status{Backend:"linux",  KernelLevel:true,  ABIVersion:n, n>0}       → fmt.Sprintf("landlock-abi-%d", n)
Status{Backend:"linux",  KernelLevel:false}                          → "fallback"
Status{Backend:"fallback"}                                           → "fallback"
all other states                                                     → "unknown"
```

Fully derived from existing `Status` fields in `pkg/sandbox/sandbox.go:537` — no new backend code.

### ContextBuilder env preamble rendering

`GetEnvironmentContext()` renders the provider's output into a minimal "new-office onboarding" template (Q2 + Q3 decisions: paths-first, no provider info). Paths shown are placeholders — values are runtime-derived (MIN-003 resolution):

```markdown
## Environment

You are running inside the Omnipus agent harness. Read this once; it tells you where you can work and where you cannot.

### Paths you can use
- Workspace (your working directory): <absolute workspace path from agent.Workspace>
- Omnipus home (framework data; read-only unless specified): <absolute OMNIPUS_HOME path from config.OmnipusHomeDir()>

### Paths you cannot use
- Everything outside the workspace above is denied unless explicitly allow-listed.
- `/dev/tty` and other TTY devices are blocked.
- System paths (`/etc`, `/usr`, `/root`, `$HOME` outside workspace) are denied.

### Sandbox & network
- Sandbox: <renderSandboxMode(Status), e.g., "landlock-abi-4" | "fallback" | "off" | "unknown">
- Network: <"outbound-allowed" | "outbound-denied">

### Active warnings
- (one bulleted line per condition; section omitted entirely when empty)
- Example: dev_mode_bypass is ACTIVE — auth checks are relaxed. Do not assume strict auth.
```

Rendered once when the system-prompt cache is built (new session / cache invalidation / explicit `InvalidateCache()`), then embedded inside the cached prompt for every turn in that session. String-building only; no I/O beyond provider reads (which themselves are `sync.Once`-cached per process for expensive fields like `/proc/version`). Output passed through `pkg/audit/redactor.Redact` before emission (MIN-002).

### MemoryStore (unchanged from v5)

All writes go through `fileutil.WithFlock(path, fn)` with `O_APPEND|O_CREATE|O_WRONLY` (MEMORY.md, retros) or `WriteFileAtomic` (LAST_SESSION.md).

Entry format for MEMORY.md:
```
<!-- ts=2026-04-24T10:00:00.000Z cat=key_decision -->
<content>
<!-- next -->
```

Retro format (`memory/sessions/YYYY-MM-DD/<sessionID>_retro.md`):
```
<!-- ts=2026-04-24T10:30:00.000Z trigger=auto fallback=false -->
## Session recap
<recap text>
### Went well
- item
### Needs improvement
- item
<!-- next -->
```

### Audit (reuse `pkg/audit.Logger`)

Memory events:
```go
auditLogger.Log(&audit.Entry{
    Event:     "memory.remember", // or "memory.retrospective", "memory.auto_recap"
    AgentID:   agentID,
    SessionID: sessionID,
    Tool:      "remember",
    Details: map[string]any{
        "category":       "key_decision",
        "outcome":        "ok", // or "error_io", "error_invalid", ...
        "content_sha256": "...",
        "byte_count":     123,
        "trigger":        "user_call",
    },
})
```

Env-preamble observer event (operator-facing, not agent-facing):
```go
auditLogger.Log(&audit.Entry{
    Event:   "config.sandbox.change", // or "config.devmode.toggle", etc.
    Details: map[string]any{"newValue": ..., "oldValue": ...},
})
```

(Reuse existing audit hooks; no new surface for env preamble itself — env is read-only.)

### Session-end pipeline (`pkg/agent/session_end.go`, unchanged from v5)

```go
func (al *AgentLoop) CloseSession(sessionID, trigger string) {
    if !al.cfg.Agents.Defaults.AutoRecapEnabled { return }
    if !al.tryClaimSessionClose(sessionID) { return }
    al.cancelIdleTicker(sessionID)
    go al.runRecap(sessionID, trigger)
}
```

See v5 for full `runRecap` flow.

### WSHandler (unchanged from v5)

`session_close` frame + `agentCurrentSession` sync.Map + lazy CAS via `LoadAndDelete` + `Store`.

### Retention (unchanged from v5)

`retention_goroutine.go:executeSweepTick` iterates `AgentRegistry.ListAgentIDs()` → `SweepRetros(retentionDays)` per agent.

---

## BDD Scenarios (additions to v5)

### Env-awareness (Fix A)

- **Env-preamble present on core agent**: build prompt for `ava`; assert first `parts[0]` begins with `## Environment` and contains platform line.
- **Env-preamble present above SOUL.md**: build prompt for custom agent with non-empty SOUL; assert env preamble appears before the SOUL section.
- **Sandbox mode derived from real backend**: mock backend returns `Describe()` = `"landlock-abi-4"`; assert env preamble contains `Sandbox: landlock-abi-4`.
- **Network-denial warning present**: with `AllowNetworkOutbound=false`, assert env preamble contains `outbound-denied`.
- **Dev-mode-bypass warning**: with `DevModeBypass=true`, assert preamble contains `dev_mode_bypass is ACTIVE`.
- **Workspace path correctness under OMNIPUS_HOME override**: set `OMNIPUS_HOME=/tmp/test-home`; restart; assert env preamble contains `/tmp/test-home/agents/<id>`.
- **Subagent sees same env**: spawn subagent via `subagent` tool; capture subagent's system prompt via `RecentLLMRequests` (hook enabled); assert same `## Environment` block is present.
- **Over-budget truncation**: mock provider that returns 3000-rune warning text; assert preamble truncates to ≤ 2000 runes + `[env context truncated]`.
- **Unknown sub-field renders `<unknown>`**: mock `Platform()` to return an error; assert preamble shows `Platform: <unknown>` and DOES NOT fail the build.
- **No secret leakage**: assert env preamble never contains any env var value whose key name matches `(?i)(key|token|secret|password|auth)`.
- **Hostname never leaked**: assert preamble does NOT contain the host's `os.Hostname()` result.

### Memory (carried from v5)

All v5 BDD scenarios preserved unchanged. Plus v5's additional scenarios:
- Auto-recap opt-in gate (default `false`, no file I/O when disabled).
- Failed memory write generates audit entry.
- Subagent sees parent's in-turn `remember`.
- Retrospective rejects path-traversal session id.
- Lazy close via CAS (double-attach, two close calls).
- Bootstrap pass after crash.

---

## Test-Driven Development Plan

### Test rows (v7: 75 live — slot count `#1..#75` with `#53` and `#66` dropped, `#58b` and `#61b` added; net **75 live tests**)

| # | Test | Level | Lane | Notes |
|---|---|---|---|---|
| 1 | `TestMemoryStore_AppendLongTerm_WithFlockSerialises` | Unit | M | 50 goroutines × 2 instances; 100 entries. Uses real `fileutil.WithFlock`. |
| 2 | `TestMemoryStore_AppendLongTerm_FormatExact` | Unit | M | Bytes match FR-002. |
| 3 | `TestMemoryStore_AppendLongTerm_StripsNull_RejectsCommentInjection` | Unit | M | NUL/whitespace/empty/`<!--`/4097-runes hit expected errors + audit. |
| 4 | `TestMemoryStore_AppendLongTerm_AuditOnFailure` | Unit | M | Force I/O error; assert `outcome=error_io`. |
| 5 | `TestMemoryStore_ReadLongTermEntries_ParsesAndCaches` | Unit | M | Two reads, same mtime → one disk read. |
| 6 | `TestMemoryStore_SearchEntries_NewestFirstLiteral` | Unit | M | Regex metachars treated literally. |
| 7 | `TestMemoryStore_SearchEntries_Cross30DayHorizon` | Unit | M | Retros 29d vs 31d old. |
| 8 | `TestMemoryStore_WriteLastSession_OverwritesAtomically` | Unit | M | `WriteFileAtomic` semantics. |
| 9 | `TestMemoryStore_AppendRetro_ValidatesSessionID` | Unit | M | `"../../etc/passwd"` → error; no write. |
| 10 | `TestMemoryStore_AppendRetro_NoUserConfirmedField` | Unit | M | No `user_confirmed` key on disk. |
| 11 | `TestMemoryStore_SweepRetros_Deletes30DayOld` | Unit | M | Mocked clock. |
| 12 | `TestRememberTool_AllDatasetRows` | Unit | M | Outline. |
| 13 | `TestRememberTool_ReadsAgentAndSessionFromCtx` | Unit | M | Audit fields correct. |
| 14 | `TestRecallMemoryTool_AllDatasetRows` | Unit | M | Outline. |
| 15 | `TestRecallMemoryTool_NoAuditEntryForReads` | Unit | M | No audit pollution. |
| 16 | `TestRetrospectiveTool_AllDatasetRows` | Unit | M | Outline. |
| 17 | `TestRetrospectiveTool_UsesTranscriptSessionID` | Unit | M | `ToolTranscriptSessionID`, not `ToolSessionKey`. |
| 18 | `TestContextBuilder_Rule4_ExactText` | Unit | M | Byte-for-byte match of FR-018. |
| 19 | `TestContextBuilder_Rule4_PreservesOtherRules` | Unit | M | Rules 1,2,3,5,6 still present. |
| 20 | `TestContextBuilder_GetMemoryContext_BothSections` | Integ | M | Preseeded files; both markers. |
| 21 | `TestContextBuilder_GetMemoryContext_RuneBudgetTruncation` | Integ | M | US-5 AC-2. |
| 22 | `TestContextBuilder_GetMemoryContext_OmitsEmptySections` | Integ | M | No empty headers. |
| 23 | `TestContextBuilder_SourcePaths_IncludesLastSession` | Unit | M | CRIT-004 from v4 grill. |
| 24 | `TestContextBuilder_Cache_InvalidatesOnLastSessionWrite` | Integ | M | mtime → rebuild. |
| 25 | `TestContextBuilder_Cache_SamePromptWithinTurn_ParentLLM` | Integ | M | Parent's current turn doesn't see own write. |
| 26 | `TestContextBuilder_Cache_SubagentSeesInTurnRemember` | Integ | M | Subagent DOES see it (v5 MAJ-007). |
| 27 | `TestNewAgentInstance_RegistersThreeToolsForCoreAgent` | Integ | M | |
| 28 | `TestNewAgentInstance_SkipsForSystemAgent` | Integ | M | `main` / `omnipus-system` excluded. |
| 29 | `TestToolRegistry_CloneExcept_PreservesMemoryTools` | Integ | M | |
| 30 | `TestAgentLoop_AgentForSession_UsesGetMeta` | Unit | S | Happy + NotFound. |
| 31 | `TestAgentLoop_AgentForSession_AgentDeleted_ReturnsNotFound` | Unit | S | Meta exists, registry doesn't. |
| 32 | `TestAgentLoop_IdleTickers_RegisterCancel` | Unit | S | |
| 33 | `TestAgentLoop_Close_CancelsAllIdleTickers` | Integ | S | No goroutine leak. |
| 34 | `TestSessionEnd_Disabled_ByDefault` | Integ | S | `AutoRecapEnabled=false` → no recap. |
| 35 | `TestSessionEnd_Explicit_HappyPath` | Integ | S | With opt-in on. |
| 36 | `TestSessionEnd_LazyCAS_NoLostTransition` | Integ | S | Double attach; two closes fire. |
| 37 | `TestSessionEnd_IdleTimeout_MockedClock` | Integ | S | |
| 38 | `TestSessionEnd_EmptySession_SkipsAndAuditsReason` | Integ | S | `[SubTurn Result]` prefix + interrupt-hint filtered. |
| 39 | `TestSessionEnd_AgentDeleted_HeuristicFallback` | Integ | S | `fallback_reason=agent_deleted`. |
| 40 | `TestSessionEnd_LLMError_HeuristicFallback` | Integ | S | |
| 41 | `TestSessionEnd_InputTokenTruncation` | Integ | S | ≤ 2000 tokens sent. |
| 42 | `TestSessionEnd_Idempotent` | Integ | S | Two triggers → one recap. |
| 43 | `TestSessionEnd_BootstrapPass` | Integ | S | Pre-seed + restart. |
| 44 | `TestWSHandler_AgentCurrentSession_UpdatedOnTurn` | Integ | S | |
| 45 | `TestWSHandler_AttachSession_AtomicSwap` | Integ | S | No race on rapid attach. |
| 46 | `TestSubturn_ActualSystemPromptNotReadByLoop` | Unit+Integ | E | **MAJ-003 resolution.** Two assertions: (a) `grep -n 'opts.SystemPromptOverride' pkg/agent/loop.go` returns zero matches (read-side stays absent); (b) spawn subagent with non-empty `cfg.ActualSystemPrompt`; capture child's first LLM system prompt via `RecentLLMRequests`; assert parent's `BuildSystemPromptWithCache()` output appears verbatim AND `cfg.ActualSystemPrompt` string does NOT appear in child's prompt. |
| 47 | `TestAuditLog_AppendsOnEveryMutation_AndFailures` | Integ | M | Reuses `pkg/audit.Logger`; reads NOT audited. |
| 48 | `TestRetentionSweep_DeletesRetrosOlderThanConfig` | Integ | S | Mocked clock; `SweepRetros` called per agent. |
| **49** | `TestEnvironmentProvider_PlatformFromRuntime` | Unit | **E** | `runtime.GOOS/GOARCH` reads correctly; kernel parsed from `/proc/version` on Linux. |
| **50** | `TestEnvironmentProvider_SandboxModeFromBackend` | Unit | **E** | Mocks each backend's `Describe()` return. |
| **51** | `TestEnvironmentProvider_NetworkPolicyComputation` | Unit | **E** | **CRIT-001 resolution — simplified.** Only two states tested: `AllowNetworkOutbound=false` → `outbound-denied`; `=true` → `outbound-allowed`. (No allow-list branch until sandbox allow-list lands.) |
| **52** | `TestEnvironmentProvider_ActiveWarnings_DevModeBypass` | Unit | **E** | `DevModeBypass=true` → warning included. |
| ~~53~~ | ~~`TestEnvironmentProvider_ActiveWarnings_AllowEmptyBearer`~~ | — | — | **DROPPED (CRIT-002).** `cfg.Gateway.AllowEmptyBearer` never existed. Slot vacant; test count recalculated below. |
| **54** | `TestEnvironmentProvider_ActiveWarnings_WindowsFlockNoop` | Unit | **E** | `runtime.GOOS=windows` → "flock is no-op on Windows" warning. |
| **55** | `TestEnvironmentProvider_NoSecretLeakage` | Unit | **E** | Render output; regex-scan for secret-looking patterns; ZERO matches. |
| **56** | `TestEnvironmentProvider_NoHostnameLeakage` | Unit | **E** | Render output; assert `os.Hostname()` string is absent. |
| **57** | `TestEnvironmentProvider_GracefulFieldFailure` | Unit | **E** | **CRIT-005 resolution.** Mock provider returns `(Platform{}, errUnreadable)` from `Platform()` → rendered Kernel line shows `<unknown>`, preamble otherwise complete, `BuildSystemPrompt` does NOT error. Second variant: `SandboxMode()` returns `("", err)` → Sandbox line shows `<unknown>`, others render. Third variant: both error simultaneously (new Dataset row 9) → both lines `<unknown>`, build still succeeds. |
| **58** | `TestContextBuilder_GetEnvironmentContext_TopOfPrompt` | Unit | **E** | Env preamble is `parts[0]` in `BuildSystemPrompt` output. |
| **58b** | `TestContextBuilder_BuildSystemPrompt_StructureAfterEnvPrepend` | Unit | **E** | **MAJ-004 resolution.** Full layout assertion: output begins with `## Environment`, then `\n\n---\n\n`, then identity marker (either `You are <name>` or `## Workspace`), then `\n\n---\n\n`, then bootstrap/skills/memory in the same order as before env-prepend. Guards against future drift that silently re-orders or inserts between preamble and identity. |
| **59** | `TestContextBuilder_GetEnvironmentContext_AboveSoul` | Unit | **E** | Custom agent with SOUL.md → env preamble precedes SOUL section. |
| **60** | `TestContextBuilder_GetEnvironmentContext_Below2000Runes` | Unit | **E** | Feed large warnings; assert ≤ 2000 runes output + truncation marker. |
| **61** | `TestContextBuilder_GetEnvironmentContext_CachedInPrompt` | Unit | **E** | **CRIT-004 resolution — rewritten.** Two back-to-back `BuildSystemPromptWithCache()` calls → provider methods called exactly ONCE total (spy-counter on provider). Demonstrates preamble is part of the cached prompt. |
| **61b** | `TestContextBuilder_GetEnvironmentContext_InvalidateCacheRebuilds` | Unit | **E** | **CRIT-004 + FR-061 resolution.** Call `BuildSystemPromptWithCache()`, then `InvalidateCache()`, then `BuildSystemPromptWithCache()` again → provider methods called twice (once per rebuild). |
| **62** | `TestRestConfigChange_InvalidatesAllContextBuilders` | Integ | **E** | **CRIT-004 resolution.** REST `PATCH /api/v1/security/sandbox` changes `AllowNetworkOutbound`; assert `InvalidateAllContextBuilders()` is called, and each agent's next `BuildSystemPromptWithCache()` yields an updated `Network:` line. |
| **63** | `TestSubturn_ContextBuilderPointerShared` | Integ | **E** | **MAJ-001 resolution.** Spawn subagent via `subturn.go:402` path; assert `childAgent.ContextBuilder == parentAgent.ContextBuilder` (pointer equality). Demonstrates structural inheritance; guards against future refactors that clone. |
| **64** | `TestSubturn_EnvPreambleInSubagentPrompt` | Integ | **E** | Subagent's first LLM call's system prompt (captured via `RecentLLMRequests` hook) contains the env preamble verbatim — proves the shared builder actually renders env for children. |
| **65** | `TestRenderSandboxMode_AllStatusShapes` | Unit | **E** | **CRIT-003 resolution.** Feeds each `sandbox.Status` permutation (from `DescribeBackend` at `sandbox.go:618`) and asserts the rendered string matches the pinned mapping: `{none → "off"}`, `{linux + KernelLevel=true + ABI=N>0 → "landlock-abi-N"}`, `{linux + KernelLevel=false → "fallback"}`, `{fallback → "fallback"}`, otherwise → `"unknown"`. No backend-level change tested. |
| ~~66~~ | ~~`TestProviderIdentity_OpenRouter`~~ | — | — | **DROPPED per Q3 decision (C).** Provider/model identity no longer in env preamble; no `Identity()` method added. Test count falls to 74. |
| **67** | `test_e2e_env_preamble_visible_in_live_session` | E2E | **E** | Playwright: send message, capture system prompt via `RecentLLMRequests`, assert preamble present. |
| **68** | `test_e2e_env_preamble_network_denial_warning` | E2E | **E** | Start gateway with `AllowNetworkOutbound=false`; assert warning in preamble. |
| **69** | `test_e2e_env_preamble_dev_mode_bypass_warning` | E2E | **E** | Start with `DevModeBypass=true`; assert warning. |
| **70** | `test_e2e_env_preamble_subagent_inheritance` | E2E | **E** | Live subagent spawn; assert env preamble in subagent's captured prompt. |
| **71** | `test_e2e_remember_recall_roundtrip` | E2E | M | Playwright; real live gateway (from v5). |
| **72** | `test_e2e_joined_retrospective_flow` | E2E | M | From v5. |
| **73** | `test_e2e_retro_searchable_via_recall_memory` | E2E | M | From v5. |
| **74** | `test_e2e_cross_session_continuity_optin` | E2E | S | With `AutoRecapEnabled=true`: S1 → recap → S2 prompt contains recap marker. |
| **75** | `test_e2e_workspace_path_correctness_under_omnipus_home_override` | E2E | **E** | Start with `OMNIPUS_HOME=/tmp/test-home`; assert env preamble shows correct path AND no agent writes escape the override. |

**Lane column legend**: **E** = env-awareness (Fix A), **M** = memory store/tools (Fix C, first half), **S** = session-end pipeline (Fix C, second half), — = cross-cutting.

### Test datasets

Unchanged from v5. Plus v6 addition:

#### Env-preamble dataset (scenario-driven; feeds tests #49-#66)

| # | Scenario | Platform | Sandbox | Network | Bypass | Expected markers |
|---|---|---|---|---|---|---|
| 1 | Default Linux / Landlock / strict | linux/arm64, 6.8 | landlock-abi-4 | outbound-denied | false | `landlock-abi-4`, `outbound-denied`, no warnings section |
| 2 | Dev host / bypass on | linux/amd64, 6.5 | fallback | outbound-allowed | **true** | bypass warning + fallback-on-modern-kernel warning |
| 3 | Windows | windows/amd64 | fallback | outbound-allowed | false | Windows flock warning |
| 4 | Darwin | darwin/arm64 | fallback | outbound-allowed | false | no bypass; no Windows warning; fallback (non-Linux → no "modern kernel" warning) |
| ~~5~~ | ~~Allow-list mode~~ | — | — | — | — | **DROPPED (CRIT-001)** — `AllowedHosts` doesn't exist. |
| 6 | Landlock off, bypass on | linux/arm64, 6.8 | off | outbound-allowed | true | bypass warning |
| 7 | Over-budget warnings | — | — | — | — | truncation marker `[env context truncated]` present; output ≤ 2000 runes |
| 8 | Single sub-field error | — | — (error) | — | — | `Sandbox: <unknown>` line; rest of preamble intact |
| **9** | **NEW** — multi-field error | — (Platform errors) | — (SandboxMode errors) | outbound-denied | false | BOTH `Kernel: <unknown>` and `Sandbox: <unknown>`; build does NOT fail; preamble still renders all non-errored sections |
| **10** | **NEW** — path with secret-like substring | — | — | — | — | Workspace path `/srv/app-secret-123/.omnipus/agents/ava` preserved (redactor does NOT over-match on legitimate path) |
| **11** | **NEW** — truncation boundary probe | — | — | — | — (warnings mass: 1999 / 2000 / 2001 runes) | exactly one of: full-fit, truncated-at-marker, truncated-at-marker (no mid-glyph split) |

---

## Functional Requirements

### Fix A — Env-awareness

- **FR-041**: New package `pkg/agent/envcontext/provider.go` defines `Provider` interface and `DefaultProvider` struct.
- **FR-042**: `Provider` reads runtime-derived fields via its interface methods; no I/O beyond `/proc/version` + `runtime.*` + in-process config.
- **FR-043**: `DefaultProvider.Platform() (Platform, error)` returns `{GOOS, GOARCH, Kernel}`. On Linux: parse `/proc/version` (via `os.ReadFile`, `sync.Once`-memoized per process); extract release token (e.g., `"6.8.0-107-generic"`) → displayed short form `"6.8"`. On non-Linux: Kernel = `""`; error nil. On Linux with `/proc/version` unreadable: return `(Platform{GOOS, GOARCH, Kernel: ""}, err)` — caller's FR-054 handling renders the sub-field as `<unknown>`.
- **FR-044**: `DefaultProvider.SandboxMode() (string, error)` returns the result of `envcontext.renderSandboxMode(sandbox.DescribeBackend(backend))`. Mapping pinned in the Integration Boundaries section above. Error returned only if an underlying registered backend's `DescribeBackend` panics (recovered; returned as error). The `SandboxBackend` interface itself is NOT modified — v7 uses the existing package-level function `DescribeBackend(SandboxBackend) Status` at `pkg/sandbox/sandbox.go:618`.
- **FR-045**: `DefaultProvider.NetworkPolicy()` returns `{OutboundAllowed: cfg.Sandbox.AllowNetworkOutbound}`. **No `AllowedHostCount` field** in v7 (CRIT-001 resolution — the config field `AllowedHosts` doesn't exist yet). When a future PR adds sandbox allow-list support, re-introduce the field + extend rendering (`outbound-allowlist:N`). Follow-up issue filed.
- **FR-046**: `DefaultProvider.WorkspacePath()` returns `agent.Workspace` (absolute, resolved). Cannot fail — field is a plain `string` on `AgentInstance` at `pkg/agent/instance.go:34`.
- **FR-047**: `DefaultProvider.OmnipusHome()` returns `config.OmnipusHomeDir()` (absolute). `OmnipusHomeDir()` is the canonical helper (verified in code — introduced as part of earlier OMNIPUS_HOME leak fix). Cannot fail.
- **FR-048**: **DROPPED per Q3 decision (C).** Provider + model identity no longer included in env preamble. No `LLMProvider.Identity()` method is added. The preamble stays minimal: paths + sandbox + network + warnings only. Follow-up issue tracks adding model-capability info (tool_use support) once the framework has a clean way to report it.
- **FR-049**: `DefaultProvider.ActiveWarnings()` emits one string per active condition from the set:
  - `dev_mode_bypass=true` → warning `"dev_mode_bypass is ACTIVE — auth checks are relaxed. Do not assume strict auth."` (verified `pkg/config/config.go:1051`).
  - `runtime.GOOS == "windows"` → warning `"running on Windows — pkg/fileutil.WithFlock is a no-op; concurrent memory writes rely on single-writer discipline."`.
  - Sandbox is `fallback` on Linux kernel ≥ 5.13 → warning `"sandbox is running in application-level fallback mode despite a Landlock-capable kernel — this is typically an explicit operator downgrade."`.
  - Kernel version cannot be detected on Linux (Platform() returned error) → the fallback-on-modern-kernel warning is emitted regardless (MIN-001 resolution: conservative — warn on unknown-kernel rather than hide a possible downgrade).
  - Cannot fail. Returns empty slice if no conditions active.
  - **`allow_empty_bearer` warning DROPPED (CRIT-002)** — field never existed; was a ghost.
- **FR-050**: `ContextBuilder.GetEnvironmentContext()` NEW method. Calls provider interface methods, renders the pinned Markdown template (see "Integration Boundaries"), caps at 2000 runes with `[env context truncated]` suffix.
- **FR-051**: `ContextBuilder.BuildSystemPrompt` inserts env preamble as `parts[0]` when non-empty; preserved through `BuildSystemPromptWithCache`.
- **FR-052**: Env preamble is NOT added to `sourcePaths()` — it's runtime-derived, not file-derived. Cache refresh relies on (a) normal file-mtime invalidation from other sources, and (b) explicit `InvalidateCache()` calls from REST config hooks (FR-061).
- **FR-053** (**CRIT-004 resolution — single lifecycle, pinned**): Env preamble lives INSIDE the cached system prompt returned by `BuildSystemPromptWithCache`. It regenerates **only** when the outer cache invalidates — i.e., on new session, SOUL.md mtime change, memory-file mtime change, skill file change, or explicit `InvalidateCache()` call. There is NO per-call re-render path. `/proc/version` read cached per process via `sync.Once`.

  **US-10 AC-11, Behavioral Contract, Clarification "Cache the env preamble?", and test #61 are all aligned with this single lifecycle.** (v6's contradictions in those sections are rewritten below.)

  **Runtime config change → cache invalidation contract**: Operator toggles `dev_mode_bypass`, sandbox mode, or any other state that feeds the env preamble via REST (`pkg/gateway/rest_sandbox_config.go`, `pkg/gateway/rest.go` settings handlers, hot-reload signals). The handler, on successful config write, MUST call `registry.InvalidateAllContextBuilders()` (FR-061). Next turn on each agent observes the new env. Tested by test #62 and test #61b.
- **FR-054** (**CRIT-005 resolution**): Provider methods that return `error` (currently `Platform()` and `SandboxMode()`) signal sub-field failures via that error. When `GetEnvironmentContext()` receives a non-nil error from a method, it renders that method's value as `<unknown>` and calls `slog.Debug("envcontext: field unreadable", "field", <name>, "err", err)`. The remainder of the preamble builds successfully. `BuildSystemPrompt` NEVER fails because of a provider sub-field error.
- **FR-055** (**MIN-002 resolution**): Env preamble rendering passes through `pkg/audit/redactor.Redact(s)` (existing redactor at `pkg/audit/redactor.go`) before emission. No bespoke regex. Test #55 feeds a dataset including AWS keys (`AKIA...`), JWTs (`eyJ...`), OAuth tokens (`ya29...`), OpenRouter keys (`sk-or-v1-...`), and legitimate path segments that contain `secret` substring (e.g., `/srv/app-secret-123/.omnipus`) — asserting secrets are redacted AND legitimate paths are preserved. Removes the "weak keyword substring" shortfall.
- **FR-056**: Env preamble MUST NOT include `os.Hostname()` output anywhere.
- **FR-057**: `ContextBuilder.WithEnvironmentProvider(p envcontext.Provider) *ContextBuilder` builder method to inject test doubles.
- **FR-058** (**MAJ-001 resolution**): Subagent inherits the parent's `*ContextBuilder` by direct struct-literal field assignment at `pkg/agent/subturn.go:402` (`ContextBuilder: baseAgent.ContextBuilder`). Because the child and parent share the same pointer, they automatically share the same `environmentProvider` field. **No `buildSubturnContextBuilder` function exists or is to be added**. Test #63 asserts pointer equality: `subAgent.ContextBuilder == parentAgent.ContextBuilder`. Test #64 then verifies the child's captured system prompt contains the env preamble (proving the shared builder renders it). If a future refactor decides to clone ContextBuilder per-subturn, that's a design change requiring a new FR — not covered here.
- **FR-059**: **DROPPED (CRIT-003).** `SandboxBackend` interface is NOT modified. v7 reuses the existing package-level `sandbox.DescribeBackend(backend) Status` at `pkg/sandbox/sandbox.go:618`. See FR-044 + the pinned `renderSandboxMode` mapping in Integration Boundaries.
- **FR-060**: **DROPPED per Q3 decision (C).** `LLMProvider` interface is NOT modified. No provider files touched.
- **FR-061** (**NEW** — CRIT-004 follow-through): Add `pkg/agent/context_registry.go` with `ContextBuilderRegistry` (thin wrapper around a `sync.Map` of `agentID → *ContextBuilder`). `AgentLoop` owns one registry; populates on `NewAgentInstance` and deletes on agent shutdown. Method `InvalidateAllContextBuilders()` iterates and calls `InvalidateCache()` on each. REST handlers that mutate env-preamble-relevant state (`rest_sandbox_config.go` + `rest.go` settings-write paths) MUST call this after commit. Test #62 verifies end-to-end.
- **FR-062** (**NEW** — MAJ-002 resolution): Create `pkg/validation/entityid.go` with exported `EntityID(id string) error`. Move the current body of `pkg/gateway/rest.go:2297` (unexported `validateEntityID`) into the new package. Update all 7 existing callers in `pkg/gateway/rest.go` (lines 266, 456, 518, 1786, 2297, 2308, 2379, 2431, 2445 — verified via grep) to call `validation.EntityID`. `pkg/agent/memory.go` calls it from the retro-append path (FR-009). No behavior drift: regression test `TestEntityID_BehaviorMatchesOldValidator` feeds the old dataset and asserts identical return values.

### Fix C — Memory (unchanged from v5)

- **FR-001**: `MemoryStore.AppendLongTerm(content, category)`. Use `pkg/fileutil.WithFlock(path, fn)`. `fn` opens `O_APPEND|O_CREATE|O_WRONLY`, mode `0o600`, writes + syncs, closes.
- **FR-002**: Entry format exact: `[<!-- next -->\n\n]<!-- ts=<ISO8601 ms UTC Z> cat=<category> -->\n<content>\n`. Leading separator only if file non-empty before write.
- **FR-003**: Cap: `len([]rune(content)) <= 4096`. Reject content with `<!--` substring.
- **FR-004**: `MemoryStore.ReadLongTermEntries()` parses by separator; cache-backed (mtime-keyed).
- **FR-005**: `MemoryStore.SearchEntries(query, limit)`. Case-insensitive literal substring. Covers MEMORY.md + LAST_SESSION.md + retros within `RetentionMemoryRetrosDays()`. Dedup by timestamp.
- **FR-006**: Legacy MEMORY.md (no separators): treat whole file as one entry `cat=legacy, ts=<file mtime>`.
- **FR-007**: `MemoryStore.WriteLastSession(content)` via `WriteFileAtomic`.
- **FR-008**: `MemoryStore.ReadLastSession()` returns content or empty.
- **FR-009**: `MemoryStore.AppendRetro(sessionID, Retro)`. `sessionID` MUST pass `validateEntityID`. Path `<workspace>/memory/sessions/<YYYY-MM-DD>/<sessionID>_retro.md`. `Retro{Timestamp, Trigger, Fallback, FallbackReason, Recap, WentWell, NeedsImprovement}`. **NO `UserConfirmed`.**
- **FR-010**: `MemoryStore.ReadRetros(daysBack int)` — clamped at `RetentionMemoryRetrosDays()`.
- **FR-011**: Every memory-mutating op (success or failure) emits audit via `pkg/audit.Logger` (verified `pkg/audit/audit.go:71-144`). Fields: `Event`, `AgentID`, `SessionID`, `Tool`, `Details{outcome, content_sha256, byte_count, trigger, ...}`. **Content hashed-only; never logged.** Memory-mutation entries share the gateway's single audit log with security/tool/policy events (MAJ-008). Operators filter by `event` field (e.g., `jq '.[] | select(.event | startswith("memory."))'`). Retention: inherited from `pkg/audit.Logger` defaults (50 MB rotation, 90-day retention) — NOT per-event-class. Documented operator note in a new follow-up file if desired. Per-event-class retention is a deferred follow-up.
- **FR-012**: Writes NEVER crash the caller on audit-logger failure. Log WARN via `slog` and continue.
- **FR-013**: Tool `remember(content:string, category:string)`. Validates per FR-002/003. Returns `"ok"` on success. Audit every call.
- **FR-014**: Tool `recall_memory(query:string, limit?:int)`. `limit` default 20, max 50. Empty query → IsError. Zero-match → `"no matching entries"` IsError=false. **NOT audited.**
- **FR-015**: Tool `retrospective(went_well:[]string, needs_improvement:[]string)`. At least one non-empty. `sessionID` via `tools.ToolTranscriptSessionID(ctx)`. Appends retro with `trigger=joined`. Audit every call.
- **FR-016**: `NewAgentInstance` registers the three tools unless `agentID == "main" || agentID == "omnipus-system"`.
- **FR-017**: None of the three tools added to `ExcludedSpawn/Subagent/Handoff`.
- **FR-018**: Rule 4 exact text:
  ```
  4. **Memory** — Use three dedicated tools:
     - `remember(content, category)` to persist a fact, decision, reference, or lesson to %s/memory/MEMORY.md.
     - `recall_memory(query)` to search your durable memory + recent session recaps + structured retrospectives.
     - `retrospective(went_well, needs_improvement)` to record a reviewed retrospective after confirming its contents with the user.
     Do NOT use write_file on memory/MEMORY.md — that overwrites. The remember tool appends.
  ```
- **FR-019**: `GetMemoryContext` emits `## Last Session` (from LAST_SESSION.md, if non-empty) followed by `## Long-term memory` (from MEMORY.md, budgeted per FR-020).
- **FR-020**: MEMORY.md budget: if `len([]rune(full)) ≤ 12000`, inject full. Else emit newest N entries such that total runes ≤ 12000, min N = 10, followed by literal `older entries available via recall_memory`.
- **FR-021**: `sourcePaths()` adds `memory/sessions/LAST_SESSION.md`. Cache invalidates on its mtime advance.
- **FR-022**: Explicit contract (unit tests #25 + #26):
  - Parent's own current turn does NOT see its own `remember` writes.
  - Subagent spawned in same outer turn AFTER `remember` DOES see it (mtime-based cache invalidation).
- **FR-023**: New WS frame type `{type:"session_close", session_id:string}`. Dispatches to `CloseSession(sessionID, "explicit")`.
- **FR-024**: `WSHandler` maintains `al.agentCurrentSession sync.Map{agentID → sessionID}`. Updated on every successful user-turn processing. On `attach_session`: `LoadAndDelete(agentID)`; if prior non-empty and differs from new, `go CloseSession(prior, "lazy")`; then `Store(agentID, newSessionID)`.
- **FR-025**: Per-session idle ticker (`agent.defaults.idle_timeout_minutes`, default 30). Started on first user turn. Reset on every subsequent turn. Registered in `al.idleTickers sync.Map`. Cancelled on explicit/lazy close, agent shutdown, gateway shutdown.
- **FR-026**: `AgentForSession(sessionID) (*AgentInstance, error)` uses `us.GetMeta(sessionID).AgentID` → `registry.GetAgent(agentID)`. Distinct errors for meta-not-found vs agent-not-found.
- **FR-027**: `CloseSession(sessionID, trigger)` single entry point. Claims via `sync.Map` sentinel; subsequent calls for same sessionID return fast.
- **FR-028**: Human-originated user turn heuristic: `role=="user" AND strings.TrimSpace(content) != "" AND NOT starts-with "[SubTurn Result]" AND NOT equals interrupt-hint literal`. Follow-up issue tracks a proper `system_origin` field.
- **FR-029**: Recap model resolution: `cfg.Routing.LightModel` if non-empty → else agent primary `Model`. Call via `agent.Provider.Chat(ctx, msgs, nil, model, opts)` with `opts["max_tokens"] = 250`, `ctx` timeout 60 s.
- **FR-029a** (**NEW** — CRIT-006 resolution): Recap opts MUST set three cost-guard fields:
  1. `opts["max_tokens"] = 250` (hard output cap).
  2. `opts["extended_thinking"] = false` (Anthropic: disable thinking tokens).
  3. `opts["extra_body"]["reasoning"]["exclude"] = true` (OpenAI/OpenRouter: disable reasoning tokens).
  Provider layers that don't recognise a field must silently ignore it (no error).
  Allow-list enforcement: if `AutoRecapEnabled=true` AND resolved `recap_model` does NOT match any pattern in `{claude-sonnet-*, gpt-4o-mini, gpt-4.1-mini, z-ai/glm-*, gemini-flash-*, claude-haiku-*}`, the gateway MUST fail boot with `config error: recap_model '<model>' is not in the cheap-model allow-list; set cfg.Routing.LightModel to a supported model or set AutoRecapEnabled=false`. Allow-list defined as package constant in `pkg/config/config.go`, extensible via `cfg.Agents.Defaults.RecapModelAllowList []string`.
- **FR-030**: Recap input capped at 2000 tokens (via shared `approxTokenCount(s) = len([]rune(s))/4`). Truncate oldest; prepend `[history truncated for summarisation]`.
- **FR-031**: `MemoryStore.SweepRetros(retentionDays int) (int, error)` deletes date-subdir files older than `retentionDays`. Called from `executeSweepTick` iterating `AgentRegistry.ListAgentIDs() + agent.Workspace`.
- **FR-032**: Bootstrap pass on gateway startup: walks `AgentRegistry.ListAgentIDs()` → per-agent `memory/sessions/` — any session without `LAST_SESSION.md` whose most-recent transcript entry > 30 min ago → enqueue one-shot `CloseSession(sessionID, "bootstrap")`. **Double-gated** by `AutoRecapEnabled=true` AND `BootstrapRecapEnabled=true` (FR-032a). "Most-recent transcript entry" means the `ts` field of the newest entry across all `.jsonl` files in the session's directory (MIN-005 pinned definition).
- **FR-032a** (**NEW** — MAJ-007 resolution): Bootstrap recap safeguards:
  1. **Second opt-in**: new field `cfg.Agents.Defaults.BootstrapRecapEnabled bool` (default `false`). Boot-time recap runs only when this AND `AutoRecapEnabled` are both true. Without both, bootstrap pass is skipped with a single INFO log line `"bootstrap recap skipped: BootstrapRecapEnabled=false"`.
  2. **Rate limit**: new field `cfg.Agents.Defaults.BootstrapRecapMaxPerMinute int` (default `5`). The pass emits `CloseSession` calls at no more than this rate (simple `time.Ticker` loop).
  3. **Archived-session skip**: if a session directory exists but has no `.jsonl` transcript (archived or swept), skip with audit `outcome=skipped_archived`; do not fall through to heuristic fallback.
  4. **Daily cost cap**: accumulate estimated cost across the bootstrap pass (input tokens × model price); if the running total exceeds `cfg.Agents.Defaults.BootstrapRecapDailyBudgetUSD` (default `$1.00`), halt the pass with an audit `outcome=skipped_budget_exceeded` entry and a WARN log line. Budget is per-gateway-process-boot (not per calendar day — simpler).
- **FR-033**: Add `cfg.Agents.Defaults.AutoRecapEnabled bool` (JSON `auto_recap_enabled`, default `false`).
- **FR-033a**: Add `cfg.Agents.Defaults.BootstrapRecapEnabled bool` (JSON `bootstrap_recap_enabled`, default `false`) — second opt-in for FR-032a.
- **FR-033b**: Add `cfg.Agents.Defaults.BootstrapRecapMaxPerMinute int` (JSON `bootstrap_recap_max_per_minute`, default `5`) — rate limit for FR-032a.
- **FR-033c**: Add `cfg.Agents.Defaults.BootstrapRecapDailyBudgetUSD float64` (JSON `bootstrap_recap_daily_budget_usd`, default `1.00`) — cost cap for FR-032a.
- **FR-033d**: Add `cfg.Agents.Defaults.RecapModelAllowList []string` (JSON `recap_model_allow_list`, default `[]` = use compiled-in allow-list from FR-029a). When non-empty, overrides the compiled allow-list.
- **FR-034**: Add `cfg.Storage.Retention.MemoryRetrosDays int` + `RetentionMemoryRetrosDays()` accessor (default 30 when ≤ 0).
- **FR-035**: Add `cfg.Agents.Defaults.IdleTimeoutMinutes int` (default 30).
- **FR-036**: Structured slog metrics: `omnipus_idle_tickers_active`, `omnipus_session_end_recaps_total{trigger,fallback}`, `omnipus_retro_files_deleted_total`, `omnipus_env_preamble_render_seconds` (histogram), `omnipus_env_preamble_field_unknown_total{field}` (counter).
- **FR-037**: `AgentLoop.RecentLLMRequests(sessionID, n)` ring buffer, populated ONLY when `OMNIPUS_RECENT_LLM_REQUESTS_ENABLED=1`. Default disabled.
- **FR-038**: PR includes every live test row (1–75, minus #66 dropped per Q3 decision; net 74 rows).
- **FR-039**: PR passes all 6 PR-review agents strictly — every reviewer returns `pass`. Any `blocker` OR `advisory` finding blocks the merge; fix and re-run. (Q4 decision A.)
- **FR-040**: PR passes `go test ./...`, `npx vitest run`, `npx tsc --noEmit` cleanly with zero failures on the PR's final HEAD. No baseline-SHA comparison. Pre-existing flakes are fixed in this PR (Q5 = C). **Narrow escape hatch (OBS-004)**: up to 2 pre-existing flaky tests may be quarantined via `t.Skip("flaky; tracked in issue #NNNN — unrelated to env-awareness or memory")` with a GitHub issue filed, subject to architect review. Every skip is listed in the PR description.

---

## Success Criteria

### Fix A — Env-awareness

- **SC-015**: Every BuildSystemPrompt call emits env preamble as `parts[0]`. Tests #58, #59 pass.
- **SC-016**: Env preamble ≤ 2000 runes on all dataset rows. Test #60 passes.
- **SC-017**: Env preamble rendering latency ≤ 1 ms on warm-process median (excluding first-ever `/proc/version` read). Benchmark test.
- **SC-018**: No secret-looking substring detected across 1000 randomized config permutations (property test #55).
- **SC-019**: No hostname leakage across 100 randomized host configurations (property test #56).
- **SC-020**: Subagent env preamble byte-equal to parent env preamble at spawn time, for 100 randomized spawn scenarios (test #63, #64).
- **SC-021**: E2E test #67 captures real SPA-driven turn and asserts env preamble presence.
- **SC-022**: E2E test #68 verifies network-denial warning reaches the LLM (visible in `RecentLLMRequests` capture).
- **SC-023**: E2E test #69 verifies dev-mode-bypass warning reaches the LLM.
- **SC-024**: E2E test #70 verifies subagent inheritance.
- **SC-025**: E2E test #75 verifies workspace path correctness under `OMNIPUS_HOME` override (no leak to `$HOME`).

### Fix C — Memory (unchanged from v5)

- **SC-001**: `TestMemoryStore_AppendLongTerm_WithFlockSerialises` passes 100/100 CI runs.
- **SC-002**: `remember` completes in ≤ 100 ms on warm disk.
- **SC-003**: After pre-seeding both files, `BuildSystemPrompt` output contains both markers.
- **SC-004**: `recall_memory` over 1000-entry MEMORY.md returns in ≤ 100 ms cache-hit; ≤ 500 ms cache-miss.
- **SC-005**: Integration test verifies S1's recap marker in S2's next LLM request (via `RecentLLMRequests` with `OMNIPUS_RECENT_LLM_REQUESTS_ENABLED=1`).
- **SC-006**: System Agent tool registry does NOT contain the three memory tools.
- **SC-007**: Subagent clone registry DOES contain the three memory tools.
- **SC-008**: `go test ./...`, `npx vitest run`, `tsc --noEmit` all fully green (zero failures, zero skips) on the PR's final HEAD. No baseline comparison — any pre-existing flake gets fixed here. (Q5 = C.)
- **SC-009**: All 6 PR-review agents return strictly `pass`. Any `blocker` or `advisory` blocks the merge until resolved. (Q4 = A.)
- **SC-010a**: Per-session recap cost with `LightModel` configured: ≤ $0.01 averaged over 10 sessions.
- **SC-010b**: Per-session recap cost with any allow-listed recap_model (FR-029a enforces the allow-list at boot; no unbounded primary-model path): ≤ $0.05 after 2000-token truncation + 250-token output cap + extended_thinking off + reasoning_exclude=true. Allow-list: `{claude-sonnet-*, gpt-4o-mini, gpt-4.1-mini, z-ai/glm-*, gemini-flash-*, claude-haiku-*}` (extensible via config).
- **SC-011**: Audit log gains one entry per memory-mutating call (success AND failure). `recall_memory` calls do NOT increment.
- **SC-012**: `AgentLoop.Close()` cancels all idle tickers within 100 ms; zero leaked goroutines on shutdown.
- **SC-013**: `SweepRetros` deletes files > `MemoryRetrosDays` old on mocked-clock test.
- **SC-014**: With `AutoRecapEnabled=false` (default), zero LLM calls originate from `session_end.go`.

---

## Traceability Matrix

| FR | US | BDD / Scenario | Tests |
|---|---|---|---|
| FR-001 | US-1 | Concurrent writers | #1 |
| FR-002 | US-1 | Format exact | #2 |
| FR-003 | US-1 | Reject invalid input | #3 |
| FR-004..005 | US-2 | Substring / Cross-30-day | #5, #6, #7 |
| FR-006 | (migration) | — | regression test |
| FR-007..008 | US-5 | Both sections | #8, #20 |
| FR-009..010 | US-3, US-4 | retro BDDs | #9, #11 |
| FR-011..012 | US-1..4 | Audit on failure | #4, #47 |
| FR-013..015 | US-1, US-2, US-4 | tool BDDs | #12–17 |
| FR-016..017 | US-7 | registration BDDs | #27–29 |
| FR-018..020 | US-6, US-5 | rule-4 + budget BDDs | #18–22 |
| FR-021..022 | US-5 | cache + subagent visibility | #23–26 |
| FR-023..027 | US-3 | session-end BDDs | #33–45 |
| FR-028 | US-3 | Empty session skip | #38 |
| FR-029..030 | US-3 | LLM error / Truncation | #40, #41 |
| FR-031 | — | SweepRetros | #11, #48 |
| FR-032 | US-3 | Bootstrap | #43 |
| FR-033..035 | US-3, config | Disabled by default | #34 + config load tests |
| FR-036 | — | metric emission | integration (per-metric) |
| FR-037 | US-8 | E2E hook | #37 surrogate |
| FR-038..040 | US-9 | — (meta) | CI |
| **FR-041..042** | **US-10** | **Provider interface defined** | **#49–66** |
| **FR-043** | **US-10** | **Platform from runtime** | **#49** |
| **FR-044** | **US-10** | **Sandbox-mode from Describe()** | **#50** |
| **FR-045** | **US-10** | **Network-policy computation** | **#51** |
| **FR-046..047** | **US-10** | **Workspace + OMNIPUS_HOME** | **#58 (indirect), #75 E2E** |
| ~~FR-048~~ | — | — | **DROPPED (Q3 = C). Follow-up issue for model/provider identity.** |
| **FR-049** | **US-10** | **Active warnings** | **#52–54** |
| **FR-050** | **US-10** | **Render + 2000-rune cap** | **#60** |
| **FR-051** | **US-10** | **parts[0] insertion** | **#58** |
| **FR-052..053** | **US-10** | **Cached with system prompt, regenerates only on cache invalidation; REST config changes trigger InvalidateCache** | **#61, #62** |
| **FR-054** | **US-10** | **Graceful field failure** | **#57** |
| **FR-055** | **US-10** | **No secret leakage** | **#55** |
| **FR-056** | **US-10** | **No hostname leakage** | **#56** |
| **FR-057** | **US-10** | **WithEnvironmentProvider** | **all env unit tests** |
| **FR-058** | **US-11** | **Subagent shares ContextBuilder pointer** | **#63, #64, #70 E2E** |
| ~~FR-059~~ | — | — | **DROPPED (CRIT-003).** Existing `sandbox.DescribeBackend` reused. |
| ~~FR-060~~ | — | — | **DROPPED (Q3 = C).** |
| **FR-061** | **US-10** | **ContextBuilderRegistry + InvalidateAllContextBuilders + REST hook** | **#61b, #62** |
| **FR-062** | — (cross-cutting) | **Move validateEntityID to pkg/validation** | **`TestEntityID_BehaviorMatchesOldValidator`** + #9 regression |
| **FR-029a** | **US-3** | **Recap cost guardrails + allow-list enforcement** | **Boot-time check + recap-opts assertion test** |
| **FR-032a** | **US-3** | **Bootstrap double-opt-in + rate limit + budget cap** | Extension of #43 with rate-limit + skip + cap assertions |
| **FR-033a..d** | **US-3, config** | **New config fields** | config load tests |

Every live FR present (FR-048 and FR-060 dropped per Q3 decision). ✅

---

## Parallel Implementation Plan — 4 Lanes

### Ownership matrix (no lane edits another lane's files)

| Lane | Owner | Files | Interfaces produced | Interfaces consumed |
|---|---|---|---|---|
| **PR-0 prep** (small preliminary) | `backend-lead` | `pkg/agent/context.go` only — inserts stub calls `parts = append(parts, cb.GetEnvironmentContext())` at position 0 (method returns `""` until lane E lands) and confirms `GetMemoryContext()` call already exists. Ships green. **Merges BEFORE lanes E/M/S/T branch out.** | stable shape for lanes E+M to extend | — |
| **E** (env-awareness) | `backend-lead` | `pkg/agent/envcontext/` (NEW package: provider interface, `DefaultProvider`, `renderSandboxMode` helper, redactor wrapper); `pkg/agent/context.go` — body of `GetEnvironmentContext()` only (no `BuildSystemPrompt` edits; PR-0 already wired the call); `pkg/agent/context_registry.go` (NEW — FR-061 `ContextBuilderRegistry`); `pkg/gateway/rest_sandbox_config.go` + `pkg/gateway/rest.go` settings-write paths (hook `InvalidateAllContextBuilders` after config write). **Sandbox files NOT touched** (no interface change — CRIT-003). **No `LLMProvider.Identity()`** (dropped per Q3 = C). | `envcontext.Provider`, `ContextBuilderRegistry.InvalidateAllContextBuilders()` | `sandbox.DescribeBackend` (existing), `pkg/audit/redactor.Redact` (existing) |
| **M** (memory store + tools) | `backend-lead` (parallel instance) | `pkg/agent/memory.go` (extend), `pkg/tools/memory.go` (NEW: 3 tools), `pkg/agent/context.go` (memory integration only: lines 244-250), `pkg/agent/instance.go` (tool registration, lines 30-60) | `MemoryStore.AppendLongTerm`, `ReadLongTermEntries`, `SearchEntries`, `WriteLastSession`, `ReadLastSession`, `AppendRetro`, `SweepRetros` | `pkg/audit.Logger`, `pkg/fileutil.WithFlock`, `pkg/fileutil.WriteFileAtomic` |
| **S** (session-end pipeline + retention) | `backend-lead` (parallel instance) | `pkg/agent/session_end.go` (NEW), `pkg/agent/loop.go` (add `AgentForSession`, `idleTickers`, `agentCurrentSession`, `CloseSession`), `pkg/gateway/websocket.go` (add `session_close` frame), `pkg/gateway/retention_goroutine.go` (extend `executeSweepTick`), `pkg/config/config.go` (add 3 fields) | `AgentLoop.AgentForSession`, `CloseSession`, `tryClaimSessionClose` | `MemoryStore.*` (lane M), `envcontext.Provider` (if needed for fallback recap) |
| **T** (tests + QA) | `qa-lead` | Every `*_test.go` file modified by lanes E, M, S + new files for tests #67-#75 (E2E), dataset fixtures | — | All lane interfaces |

### Coordination rules (v7)

1. **PR-0 lands first.** A tiny prep PR wires the placeholder calls into `BuildSystemPrompt` so lanes E and M only need to fill in helper bodies. This eliminates the line-range overlap (MAJ-005 resolution). **Explicitly: `LLMProvider.Identity()` is NOT published** (MAJ-006 — dropped per Q3 = C). A CI guard test fails if any file mentions `LLMProvider.Identity`.
2. **Interface-first inside PR-0 + Lane E**: the `envcontext.Provider` interface shape is committed in Lane E's first patch; lanes M and S use mock providers in their unit tests and do not block on lane E's body.
3. **Merge order**: PR-0 → (E, M, S in parallel) → T → final integration. Each lane merges into `feature/env-awareness-and-memory`. T's tests run against the fully integrated state.
4. **Shared-file policy**: After PR-0 lands, no two lanes edit the same file except:
   - `pkg/config/config.go` — lane S owns all new config fields (memory + idle + bootstrap + recap allow-list). Lane E does NOT add config fields (env preamble is derived).
   - `pkg/gateway/rest.go` — lane S owns memory-related handlers; lane E owns the settings-write `InvalidateAllContextBuilders` hook (a distinct function). Merge surface is small and well-separated.
5. **Validation move (FR-062)**: lane T's prep phase moves `validateEntityID` → `pkg/validation/entityid.go` with exported `EntityID`. Updates 7 gateway callers. Ships BEFORE lane M needs to call it from `pkg/agent/memory.go`.
6. **Conflict resolution**: If a merge conflict surfaces, architect review is mandatory before resolution — no silent fixup.
7. **CI-enforced ownership**: lane labels map to required file prefixes. A pre-merge GH Action diffs the PR's file list against the lane label; mismatch → CI fail (MIN-006 resolution).

### Lane E breakdown (~8h, v7 revised)

1. Define `envcontext.Provider` interface + `Platform`, `NetworkPolicy` types (Platform and SandboxMode return errors per CRIT-005).
2. Implement `DefaultProvider` reading runtime state; `SandboxMode()` calls `sandbox.DescribeBackend(backend)` and maps via `renderSandboxMode`.
3. **NO sandbox interface change** (CRIT-003 — `sandbox.DescribeBackend` already exists).
4. ~~Add `LLMProvider.Identity()`~~ **DROPPED (Q3 = C).** Plus CI guard test.
5. Add `ContextBuilder.environmentProvider` field + `WithEnvironmentProvider` builder.
6. Body of `ContextBuilder.GetEnvironmentContext()` (the call site already exists from PR-0).
7. **Subagent propagation is automatic** (MAJ-001 — `subturn.go:402` already shares ContextBuilder pointer). Add test #63 only (pointer equality).
8. Create `pkg/agent/context_registry.go` (FR-061) + hook `InvalidateAllContextBuilders()` into REST handlers (`rest_sandbox_config.go`, `rest.go:settings-write`).
9. Wire `pkg/audit/redactor.Redact` into rendering (MIN-002).
10. Unit tests #49–65 (test #53 and #66 dropped, #58b and #61b added) + test #62 (REST → InvalidateCache → next-prompt-shows-change); integration test #67 stub (wired in lane T).

### Lane M breakdown (~12h)

Memory-store extension + three tools + Rule 4. See v5 Track 1-5.

### Lane S breakdown (~10h)

Session-end pipeline + WS handler + retention goroutine + config. See v5 Track 6-9.

### Lane T breakdown (~8h)

All 75 tests wired + run on integrated branch. E2E tests #67-75 (Playwright + Go gateway spins).

**Total (v7)**: PR-0 ~1h + lane E ~8h + lane M ~12h + lane S ~12h (added FR-032a rate-limiting + FR-029a boot check) + lane T ~9h (added `validateEntityID` move + new tests) = ~42h sequential. Parallel execution compresses to ~14-18h wall-clock (not 12-14h — OBS-002 adjustment — integration friction + strict-pass reviewer cycles add overhead).

---

## E2E Test Specification (expanded in v6)

| # | Name | Target regression | Fails visibly if |
|---|---|---|---|
| 67 | `env_preamble_visible_in_live_session` | agent unaware of env | preamble absent from prompt sent to OpenRouter |
| 68 | `env_preamble_network_denial_warning` | network-denial blindness (Class-A #1) | warning missing → agent would retry blocked requests |
| 69 | `env_preamble_dev_mode_bypass_warning` | bypass invisibility (Class-A #3) | bypass warning missing → agent assumes strict auth |
| 70 | `env_preamble_subagent_inheritance` | subagent env-blindness | subagent's captured prompt differs from parent |
| 71 | `remember_recall_roundtrip` | memory broken | tool returns error OR recall misses just-stored entry |
| 72 | `joined_retrospective_flow` | retrospective broken | `retrospective()` produces no file |
| 73 | `retro_searchable_via_recall_memory` | search broken | retro content not returned by `recall_memory` |
| 74 | `cross_session_continuity_optin` (`AutoRecapEnabled=true`) | continuity broken | S1's recap absent from S2's prompt |
| 75 | `workspace_path_correctness_under_omnipus_home_override` | OMNIPUS_HOME leak (Class-A #2) | env preamble shows `$HOME/.omnipus` when `OMNIPUS_HOME=/tmp/test-home` set |

All E2E tests run against the **embedded SPA binary** per CLAUDE.md §SPA Embed Pipeline (not Vite dev server). **Per-test isolated OMNIPUS_HOME** (MIN-004 resolution): each E2E test creates its own `os.MkdirTemp("", "omnipus-e2e-*")` directory; no shared paths. `test_e2e_workspace_path_correctness_under_omnipus_home_override` uses its unique tempdir rather than a hard-coded path.

CI pins: `openrouter-glm` (`openrouter/z-ai/glm-5-turbo`) with `OPENROUTER_API_KEY_CI` on PR CI (from `.github/workflows/pr.yml:298,319`). Tests FAIL (no skip) when key absent.

---

## Review + Test Gates

### 6 PR Reviewers (must pass after implementation)

Run in parallel after lanes E+M+S+T are merged into `feature/env-awareness-and-memory`:

1. `pr-review-toolkit:code-reviewer` — CLAUDE.md compliance + quality
2. `pr-review-toolkit:code-simplifier` — clarity + maintainability
3. `pr-review-toolkit:comment-analyzer` — comment accuracy
4. `pr-review-toolkit:pr-test-analyzer` — test-coverage quality
5. `pr-review-toolkit:silent-failure-hunter` — silent failures + error handling
6. `pr-review-toolkit:type-design-analyzer` — interface/type design

All six must return **pass** or **advisory-only**. Any **blocker** finding requires fix + re-run.

### Full-codebase test gate

Runs against the merged integration branch, NOT per-lane:

```bash
# Go backend
go test ./...

# Frontend unit + types
npx vitest run
npx tsc --noEmit

# SPA embed sync (before any backend-embedded test)
npm run build
rm -rf pkg/gateway/spa/assets
cp -r dist/spa/* pkg/gateway/spa/
CGO_ENABLED=0 go build -o /tmp/omnipus ./cmd/omnipus/

# E2E tests #67-#75
./scripts/e2e-v6.sh
```

All green vs. pinned baseline SHA (PR description holds the SHA; branch creation step records it).

---

## Ambiguity Warnings (v7)

| # | Item | Default | Resolution path |
|---|---|---|---|
| 1 | Recap prompt text (from v5) | `"Summarise this conversation in ≤ 150 words. Then list up to 5 wins, up to 5 needs-improvement items, and up to 5 items worth remembering long-term. Respond ONLY with valid JSON: {\"recap\":\"...\", \"went_well\":[...], \"needs_improvement\":[...], \"worth_remembering\":[...]}"` | lane S implements as-is |
| 2 | Heuristic fallback recap format (from v5) | `"Session <id> ended. Turns: N. Tool calls: M. Fallback reason: <reason>."` | lane S implements as-is |
| 3 | `agent.memory.auto_recap_enabled` default | `false` (CLAUDE.md "opt-in for features") | lane S |
| 4 | Per-agent recap-model opt-in | Inherits from `cfg.Agents.Defaults.AutoRecapEnabled`; no per-agent override in v7 | follow-up issue if needed |
| 5 | Env preamble kernel info on darwin/windows | Kernel field empty string (displayed "Kernel: n/a" or just omitted on non-Linux); no `runtime.Version()` fallback (Go runtime version ≠ kernel version — Unasked Question #4) | follow-up: proper OS-version reader |
| 6 | Env preamble host-identifying fields | Absent (no hostname, no FQDN, no IP) | FR-056 enforced; any future addition requires spec update |
| 7 | Env preamble localization | English-only; hard-coded strings | follow-up if multi-language needed |
| 8 | Recap-model allow-list content | Compiled list `{claude-sonnet-*, gpt-4o-mini, gpt-4.1-mini, z-ai/glm-*, gemini-flash-*, claude-haiku-*}` | operator-extensible via `cfg.Agents.Defaults.RecapModelAllowList` (FR-033d) |
| 9 | Bootstrap recap daily budget unit | Per-boot (not calendar-day) for simplicity — MAJ-007 | follow-up if calendar-day semantics needed |
| 10 | Truncation boundary alignment | `[env context truncated]` marker appended; never mid-glyph split (runes-aware) | tested in dataset row 11 |
| 11 | Multiple-warning join | Warnings joined with `\n- ` bullets; section omitted when empty | Pinned in template example |

---

## Evaluation Scenarios (Holdout — NOT in TDD plan)

H-1: Cross-day continuity (with `AutoRecapEnabled=true`).
H-2: Retro quality.
H-3: Recall accuracy.
H-4: Concurrent-write safety.
H-5: LLM-outage resilience.
H-6: Budget sanity (v6: $0.30 light model / $1.50 primary over 30 sessions).
H-7: Opt-in gate observed: with `AutoRecapEnabled=false`, agent never initiates a background call.
H-8: **NEW** — Env preamble survives config reload. Operator toggles `DevModeBypass=true` via REST; on the next agent turn, the preamble reflects the change.
H-9: **NEW** — Env preamble stable under load. 1000 concurrent `BuildSystemPrompt` calls return byte-identical env preambles (assuming no config change).
H-10: **NEW** — Env preamble does not reveal hostname under any inspected scenario.

---

## Assumptions

- `SystemPromptOverride` remains dead (v5 MAJ-011). Regression test #46 enforces.
- `ReadLongTerm` silent-empty-on-error stays (v5 follow-up).
- Custom agents with shared workspaces share MEMORY.md. Operator hygiene.
- Recap audit entries via existing `pkg/audit.Logger`.
- Subagent sub-turn entries NOT counted as "user turns" via `[SubTurn Result]` prefix heuristic; follow-up for `system_origin` field.
- CI E2E pins `openrouter-glm` with `OPENROUTER_API_KEY_CI`; tests fail if key absent.
- Windows `WithFlock` is a no-op with warning (existing compromise).
- Recap LLM call uses agent's existing Provider + model resolver. No new provider instantiation.
- Rule 4 propagates to subagents via shared ContextBuilder.
- **Env preamble assumes Linux as primary target**. Windows/darwin receive placeholder kernel info until follow-up.
- **Env preamble rendering is reentrant-safe** (no state mutation; pure function of provider reads + in-process config snapshot).
- **Adding `Identity()` to `LLMProvider`** is non-breaking for external consumers because the interface is internal (private package).

---

## Clarifications (v6)

### 2026-04-24 (v7 grill clarifications — CRIT/MAJ resolutions)

- Q: Cache the env preamble? → **yes — embedded inside the cached system prompt.** v7 pins FR-053 as the single lifecycle. Runtime changes trigger `InvalidateAllContextBuilders()` via REST hook (FR-061).
- Q: Add `SandboxBackend.Describe()`? → **no**. v7 reuses existing `sandbox.DescribeBackend(backend) Status` + helper `renderSandboxMode`. No interface change.
- Q: Add `LLMProvider.Identity()`? → **no** (Q3 decision already said no; v7 adds CI guard test to enforce).
- Q: Reveal `AllowedHosts` count? → **no — the field doesn't exist.** Dropped entirely from Fix A (CRIT-001). Follow-up issue reintroduces when sandbox allow-list lands.
- Q: Graceful sub-field failure model? → **provider methods return `error` on fields that can fail** (`Platform()`, `SandboxMode()`). Non-nil error → render `<unknown>`, don't fail build (CRIT-005).
- Q: `buildSubturnContextBuilder`? → **doesn't exist.** Subagent inherits ContextBuilder pointer via struct-literal at `subturn.go:402` (MAJ-001).
- Q: `validateEntityID` reuse from `pkg/agent/memory.go`? → **move to `pkg/validation/entityid.go` with exported `EntityID`** (MAJ-002). Update all 7 gateway callers.
- Q: SystemPromptOverride "dead" regression test? → **rewritten** (MAJ-003) — positive behavioral test asserting override string does NOT appear in child's prompt.
- Q: Bootstrap recap cost? → **double opt-in + rate-limit + daily budget cap** (FR-032a, MAJ-007). Requires `BootstrapRecapEnabled=true` separately.
- Q: Opus recap budget still impossible? → **cheap-model allow-list + reasoning/thinking disabled** (FR-029a, CRIT-006). Boot fails if recap_model outside allow-list.

### 2026-04-23 (Fix A added; parallel-lane plan added; superseded by v7 clarifications above)

- Q: Does env preamble go above or below SOUL.md? → A: **above**. Env is a precondition for agent reasoning.
- Q: Can agent tools mutate the preamble? → A: **no**. Read-only from agent's side.
- Q: Does env preamble emit secrets? → A: **never**. v7 uses `pkg/audit/redactor.Redact` (not bespoke regex, MIN-002). Test #55.
- Q: Hostname ever leaked? → A: **never**. FR-056 + test #56.
- Q: Subagent sees same env as parent? → A: **yes**. Shared ContextBuilder pointer (MAJ-001 reality).
- Q: What if `/proc/version` is missing (non-Linux OR restricted)? → A: FR-054 graceful fallback to `<unknown>` via error-return contract (v7 CRIT-005).

### 2026-04-24 (v5 grill clarifications, carried forward)

- Q: `AgentForSession` lookup chain? → A: `UnifiedStore.GetMeta(sessionID).AgentID`. No parsing.
- Q: "User turn" definition? → A: role=user, non-empty, NOT prefixed with `[SubTurn Result]`, NOT the interrupt-hint literal.
- Q: Retention sweep for retros? → A: new `MemoryStore.SweepRetros` called from `executeSweepTick`.
- Q: Opus recap budget? → A: truncate input to 2000 tokens, cap output at 250.
- Q: Failed-write audit? → A: every attempt audited with `outcome` field.
- Q: Audit file? → A: reuse `pkg/audit.Logger`.
- Q: Retrospective filename? → A: `ToolTranscriptSessionID` (ULID) + `validateEntityID`.
- Q: Auto-recap default? → A: `false` (opt-in).
- Q: CI model? → A: `openrouter-glm`.
- Q: Lazy-trigger concurrency? → A: `sync.Map.LoadAndDelete` + `Store`; atomic transition.
- Q: Subagent sees in-turn `remember`? → A: yes, via shared ContextBuilder + mtime cache invalidation.

---

## Done Criteria (gate before merge)

- [ ] All live FRs implemented: FR-001..FR-047, FR-049..FR-058, FR-061, FR-062 + new FR-029a, FR-032a, FR-033a..d (FR-048, FR-059, FR-060 explicitly dropped).
- [ ] All 75 live tests pass locally AND in CI (slots #53 and #66 empty; #58b and #61b added).
- [ ] `go test ./...`, `npx vitest run`, `tsc --noEmit` fully green, zero failures on PR's final HEAD. Any skip is an explicit quarantine with tracking issue (≤ 2 per OBS-004).
- [ ] All 6 PR reviewers return strictly `pass` — any `blocker` or `advisory` blocks merge until resolved (Q4 = A).
- [ ] Ambiguity warnings resolved or explicitly accepted.
- [ ] SPA rebuild + sync + binary rebuild per CLAUDE.md.
- [ ] Lane merge integrity: PR-0 landed first; lanes E/M/S/T diffs respect CI-enforced ownership (MIN-006).
- [ ] CI guard test `TestNoLLMProviderIdentityReference` returns zero hits (MAJ-006 guard).
- [ ] Follow-up issues filed: `system_origin` first-class field; `ReadLongTerm` silent-empty logging; recap privacy review; semantic search (v8); `SystemPromptOverride` read-side wiring re-validation; Windows/darwin proper OS-version reader; `AllowedHosts` config + allow-listed network rendering; model-capability/tool_use reporting (Q3 deferred); provider identity reporting (Q3 deferred); per-event-class audit retention; calendar-day bootstrap budget variant.

---

## Appendix: Fix C (Memory) — Reference

Full v5 spec carried forward without edits (see "User Stories & Acceptance Criteria" → US-1..9, "Functional Requirements" → FR-001..FR-040, and "Integration Boundaries" → MemoryStore, Audit, Session-end pipeline, WSHandler, Retention).

## Appendix: Sequence of implementation (wall-clock)

```
t=0      +-------+
         | PR-0  | 1h (prep: BuildSystemPrompt hook points)
         +-------+

t=1      +------+
         | Lane E (env-aware)      8h
         +------+
         | Lane M (memory)        12h
         +------+
         | Lane S (session)       12h  (added FR-032a + FR-029a)
         +------+
         | Lane T prep (FR-062)    2h  (runs in parallel with others)
         +------+

t=13     [Integration merge: E→M→S→T]
         [Lane T: 75 test rows + E2E + MAJ-005 validateEntityID move]  ~9h

t=22     [6 reviewers in parallel — strict (Q4 = A)]           ~1h

t=23     [Fix blockers/advisories; re-run until all pass]      ~3-5h
         (v7 budget: longer due to strict-pass; OBS-002)

t=28     [Final review + merge]                                ~1h
```

**Total wall-clock (parallel, v7)**: ~28h (bounded by strict-pass cycle + integration friction). Sequential would be ~42h.
