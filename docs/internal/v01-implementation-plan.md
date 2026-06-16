# v0.1.0 Foundation — Implementation Plan (parallel fan-out, waves)

**Source of truth:** ADR-019 + the 6 PASSED specs (`docs/internal/specs/v01-spec1..6-*-spec.md`) + the concept doc (`.preview-doc/`).
**Branch:** `feat/level1-project-task-mgmt` (specs live here; the `project→workspace` rename subsumes the level1 project-task code). No PR to main without human approval (CLAUDE.md).
**Build/test:** CI is the authority — never run the full Go suite locally (OOM). Local = scoped `-run … -p 1 -tags goolm,stdjson` only. `make verify-contracts` after any contract change. Commit as the human, no Anthropic trailers.
**Execution model:** Agent-tool fan-out (NOT Workflow), ≤8 dev agents/wave, disjoint file trees per agent. After each wave: **6 PR reviewers** over the wave diff → **fix wave** → re-review until clean. Worktree isolation (`isolation: worktree`) where agents touch shared files.

## Dependency order (why these waves)

Spec-1 (Workspace key + rename) is the root — every other spec consumes `workspace_id` / `system.workspace.*`. Contracts come first (Constraint #8). Then the rename. Then the parallelizable middle (channels · agents · memory are disjoint). Then the dependents (runners need agents; skills/auth need agents + consent).

## Waves

### Wave 0 — Contracts-first (BLOCKING, Constraint #8)
1–2 agents (contracts are one atomic regen). ALL v0.1.0 contract deltas in one pass:
- `Project*→Workspace*` schemas + `project_id→workspace_id` everywhere (Spec-1).
- `ChannelEntry`/`ChannelConfigureRequest` + `ChannelId += email` + `instance_id`/`identity` (Spec-2).
- delegation-policy schema `to·accept_from·modes·depth·budget` (Spec-3).
- agent `voice` field; sub-agent `executor` (Spec-3/4).
- task fields `start·due·recurrence·blocked_by` (Spec-5/Spec-8).
- Integrations provider-config types (Spec-6).
→ `scripts/gen-contracts.sh`; `make verify-contracts` green; commit `contracts/` + `pkg/api/generated/` + `src/lib/api/generated/` atomically. **6-reviewer gate on the contract diff.**

### Wave 1 — Spec-1 Workspace rename + owner-gate strip (FOUNDATION)
≤6 agents, disjoint trees (worktree-isolated): Go handlers (`rest_projects→rest_workspaces`) · `ProjectID/project_id` symbols + storage `filepath.Join("projects")` sites · `system.project.*→system.workspace.*` tools + prompt text · the SPA routes/state · the owner-gate strip (delete `canAccess`/`denyIfNoAccess`; rewrite SEC-2 tests to no-denial; keep `rest_patch_ownership`) · greenfield seed `"My Workspace"`. **Compiler-as-oracle**: delete the typed fields → `go build` flags every reader. **6-reviewer gate → fix.**

### Wave 2 — Spec-2 + Spec-3 + Spec-5 (PARALLEL — disjoint subsystems)
≤8 agents across three subsystems:
- **Spec-2 Connections:** `ChannelsConfig`→map (cap 1) + `initChannels` loop + type→factory map + 13 factory constructors + cmd/ readers + producers; email channel (`emersion/go-imap` + `net/smtp`, TLS); Connectors UI; per-instance cred refs.
- **Spec-3 Agents/delegation:** 4-base re-cast (Mia·Assistant/Jim·Orchestrator/Ray·Scout/Ava·Builder + prompts; Max retired) + `voice`; unify 3 allowlists into `to`; gate the sync `subagent` tool; Orchestrator via `SetOnComplete`; Max-parallel fan-out gate; trust-graph UI.
- **Spec-5 Memory/tasks/calendar:** rooms topology + file format + re-point the 3 tools + `bleve` scorch + frozen `counters.jsonl`/sessions logs + MinHash; task fields + `blocked_by` validator (self/N-cycle/orphan/depth); Calendar shell. Greenfield (no MEMORY.md migration, D2).
**6-reviewer gate per subsystem diff → fix.**

### Wave 3 — Spec-4 + Spec-6 (depend on Spec-3)
≤6 agents:
- **Spec-4 Runners:** `executor` dispatch; `ExternalAgentRunner` (bidirectional/consent-routed/resumable); streaming CLI+JSON drivers for **Claude Code + Codex + opencode** (drop `--dangerously-skip`; run WITH their sandbox); git-worktree isolation; connection test; bounded runs.
- **Spec-6 Skills/plugins/auth:** wire stub skill tools to `pkg/skills` (Deps); `system.skill.create/edit` (consent via `ws_approval`, versioned, path-confined); `go:embed` defaults (author `skill-authoring`/`plan`/`daily-briefing`); per-agent allowlist (default-deny); `RegistryConfig`→list + GitHub adapter; **the NEW consent primitive** (HTTP re-auth + tool-layer `ws_approval`); Integrations UI + mic; Profile/Settings; onboarding→Mia.
**6-reviewer gate per diff → fix.**

## After implementation
1. **Completeness check** vs each spec's traceability matrix + the concept doc (`.preview-doc/`) — list gaps, close them (a gap-fix wave).
2. **Two full rounds of the 6 PR reviewers** over the ENTIRE diff → fix all issues each round.
3. **UAT test plan** — `docs/internal/v01-uat-plan.md`: end-to-end scenarios per spec (onboarding→Mia · Workspaces · Connectors+email · Agents/delegation/Orchestrator · runner connection-test · Memory recall · Tasks/DAG · Calendar · Skills/Integrations · Profile/auth), each with steps + expected + holdout evals.
4. **Build + Playwright UI validation:** `npm run build` → sync `pkg/gateway/spa/` → build binary → run on `0.0.0.0:8080`; drive the full UI surface via the Playwright MCP per the UAT plan, impersonating a human; zero console errors (WS-reconnect OK).

## The 6 PR reviewers (per CLAUDE.md review gate)
`pr-review-toolkit:` code-reviewer · code-simplifier · comment-analyzer · pr-test-analyzer · silent-failure-hunter · type-design-analyzer. (If unregistered → 6 parallel `general-purpose` reviewers, one per dimension, + `architect` for the cross-cutting pass.) Every finding fixed or deferred-with-issue.

## Parallelism summary
- Wave 0: 1–2 (atomic). Wave 1: ≤6 (worktree-isolated). Wave 2: ≤8 (3 disjoint subsystems). Wave 3: ≤6.
- Reviewers: 6 in parallel per gate. Final: 2×6 in parallel.
- UAT authoring ∥ build can overlap; Playwright runs after build.
