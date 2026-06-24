# Tool-System Refactor UAT Report (§1–§7) — Outcome-Verified — v0.1.0

**Date:** 2026-06-24 · **Branch:** `feat/0.1.0-uat-fixes`
**Scope:** the §1–§7 tasks-system + tool-naming refactor (`docs/internal/design/tasks-system-refactor-2026-06.md`).
**Method:** wire-contract inspection + a **live LLM agent** (z-ai/glm-5.2 via OpenRouter) prompted to
use the renamed/new tools, with **every result verified via an independent channel** (board API
read-back) — not "tool fired". Plus the Go-level outcome tests and the green CI go-test suite.

## Headline

| Check | Result |
|-------|--------|
| CI `go-test` (ci-omnipus worker, full suite + flake filter) | **ALL GATES GREEN (exit 0)** |
| CI `gofmt` / `go-vet` / `contracts` / `spa` (ci-omnipus) | all **ALL GATES GREEN** |
| CI `e2e` Playwright matrix (132 tests, live glm-5.2) | **113 passed · 3 flaky (retry-passed) · 16 skipped · 0 failed** |
| 7-reviewer quality gate | all HIGH findings fixed (see below) |
| Wire registry (`GET /api/v1/tools`) | **78 tools, 16 domain categories, no `system` category, zero old names, scope=[core,general]** |
| Live agent — `set_todos` (new §3 tool) | **PASS** (outcome-verified) |
| Live agent — `create_task` (renamed) | **PASS** (outcome-verified) |
| Per-agent tool list (mia) | 64 tools, **zero old names**, new names present |
| `typecheck` / `vitest` (2590) / `verify-contracts` | green |

## Wire-contract verification (the rename is live end-to-end)

`GET /api/v1/tools` on a booted gateway returned **78 tools** across **16 domain categories**
(agents 8, browser 7, channels 5, communication 7, delegation 5, filesystem 5, mcp 3, memory 3,
platform 5, providers 4, shell 1, skills 6, tasks 9, tool_discovery 2, web 3, workspaces 5) — **no
`system` category**. Zero pre-rename names present (`task_create`, `web_search`, `system.agent.create`,
`browser.navigate`, `system.pin.*`, `system.backup.create`, … all absent). `scope` enum is now
`[core, general]` (the vestigial `system` value dropped). `workspace_shell`/`_bg` are correctly absent
(config-gated, experimental).

## Live LLM agent scenarios (outcome-verified via board API)

### Scenario 1 — `set_todos` (new §3 scratchpad tool)
Prompt (to the default agent): set a scratchpad for goal `UAT-smoke-check` with two checklist items
(`inspect the registry` = in_progress, `verify the outcome` = pending). **Verification:** `GET
/api/v1/tasks` showed a board task titled exactly `UAT-smoke-check`, status `in_progress`, todos =
`[{in_progress, "inspect the registry"}, {pending, "verify the outcome"}]`. **PASS.** Confirms, end
to end through a real LLM: the new `set_todos` tool is advertised and called; the agent-unaware facade
created a **board-visible** task (the agent never called `create_task`); and the **tri-state Todo
status** (`{text, status}`, not the old `done` bool) round-trips.

### Scenario 2 — `create_task` (renamed from `task_create`)
Prompt: use `create_task` to create `UAT-rename-task`, self-assigned. **Verification:** board showed
`UAT-rename-task`, agent `mia`, status `in_progress`. **PASS.** The renamed task tool is in the
agent's tool list under its new name and executes when the LLM calls it (self-assignment is permitted
— the trust-set self-exemption fix).

### Per-agent tool surface
`GET /api/v1/agents/mia/tools` returned 64 tools with **no** old names and the new names present
(`create_task`, `set_todos`, `send_message`, `list_directory`). The rename reaches the per-agent
runtime registry, not just the global catalog.

## Go-level outcome tests (behavior, in CI)

Green in the CI go-test suite: the new `set_todos` tests (create/replace/hijack-prevention/
archive-previous/atomic-create/no-task_id-leak), the `Todo` tri-state + legacy-`done` migration +
invalid-status guard, the §6 channel `configure→test` end-to-end credential-ref test, the §4
cross-workspace delegation/ownership tests (10 cases), the permission-map coverage test (every
`AllTools()` tool has rbac+confirmation+ratelimit entries), and the no-dot / no-`system`-category
invariant tests for both the 37 sysagent tools and the general builtins.

## Reviewer-gate findings — disposition

All HIGH findings fixed: `configure_channel` silent no-op (never wrote the credential ref →
channel never connected while reporting success); §4 delegation parity (cross-workspace task tools
bypassed the FR-6.2 gate); `list_models` fail-open rate-limit gap; `set_todos` real-task hijack +
board pollution + selection-divergence (added a disk-only `Scratchpad` discriminator + archive-
previous + unified selection); `Todo.UnmarshalJSON` invalid-status escape. MEDIUM/LOW cleanup: dead
`BackupCreateTool` removed, `workspace_shell` user-facing strings, stale `cron` in the auto-approve
list, wrong "41 system tools" docs → 37.

## Deferred / tracked (not blockers)

- `query_cost` ships as a registered honest `[NOT IMPLEMENTED]` stub (§6 explicitly *deferred* the
  cost-store decision — kept intentionally, returns a clear NOT_IMPLEMENTED error).
- A `serve_web` SPA tool-UI component is still registered under the legacy `web_serve` key (custom
  preview component); the tool itself works under `serve_web` — cosmetic SPA-UX follow-up.
- §4 full shared-core consolidation (the two task-tool implementations remain separate); the
  security-relevant delegation/ownership parity was wired this cycle.

## CI e2e gate (comprehensive agent-driven Playwright UAT)

Full matrix on the ci-omnipus worker against the live glm-5.2 model: **113 passed, 3 flaky, 16
skipped, 0 hard failures**. The 3 flaky tests passed on retry and are **pre-existing LLM-timing
flakiness, not refactor regressions**: `media.spec:16` (Mia, the Assistant persona, intermittently
refuses the browser tool — a documented finding) and `bug-regression Bug-5 (a/b)` (replay
frame-ordering timing, no tool-name surface). Playwright itself exited 0; the runner's non-zero
status is the known SSH-wrapper teardown false-signal.

## Conclusion

The §1–§7 refactor is live and correct end-to-end. Every CI gate is green (go-test, gofmt, go-vet,
contracts, spa, and the e2e Playwright matrix with 0 hard failures), the wire contract reflects the
full rename + recategorization, and a real LLM agent successfully uses the new `set_todos` tool and
the renamed `create_task` with independently-verified board outcomes.
