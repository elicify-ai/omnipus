# Planning & Goals — Implementation Wave Plan

**Spec**: `docs/internal/specs/planning-goals-spec.md` (grill-passed, 3 rounds). **ADR**: ADR-049.
**Branch**: `feature/planning-goals`. **Base/target**: `release/v0.1.1`.
**Verification**: CI is authority for Go tests (never full local suite — OOM). Push + `ci-omnipus` worker per gate. Frontend gates (`npm run typecheck`, `npx vitest run`) + `make verify-contracts` run locally-cheap.

## Sequencing rationale

- **Constraint #8 (contract-first)** forces a blocking **Wave 0**: all wire types + codegen land before any handler/consumer code. Codegen (`scripts/gen-contracts.sh`) regenerates the whole `pkg/api/generated/` + `src/lib/api/generated/` atomically, so it must be ONE agent (a parallel second author would clobber the generated tree).
- Backend foundation packages are mostly **disjoint** (`pkg/plan` is new; `pkg/config` bounds; `pkg/coreagent`+`pkg/security` seeding/privilege) → parallel. The one shared hotspot is `pkg/task` (tags + criteria + migration + attempt_count) → single agent to avoid conflicts.
- **Wave 2 (runtime)** touches `pkg/agent` (task_executor.go, loop.go, subturn.go, new plan engine) and `pkg/gateway` — high conflict risk in `pkg/agent`. Partition by file ownership; where two concerns touch loop.go, use **worktree isolation** and reconcile at merge, or sequence.
- **SPA (Wave 3)** partitions cleanly by component directory → parallel, needs only Wave 0's generated TS types (component/unit tests use mocks; full E2E waits for backend).

## Wave 0 — Contracts & codegen (BLOCKING, 1 agent, backend-lead)

Author every schema in `contracts/components/schemas/` and wire it into `openapi.yaml`/`asyncapi.yaml`, then `scripts/gen-contracts.sh`, commit generated diff atomically. Deliver `make verify-contracts` green.

New/changed (spec Part A contract table C1–C20 + R-section additions):
- `Plan.yaml` (5-value `state` enum draft/approved/running/done/failed; `plan_phase`, `failed_reason`, `progress`, `owner_agent_id`, `dod[]`, `bounds`), `PlanCreateRequest`, `PlanUpdateRequest`, `PlanListResponse`.
- `AcceptanceCriterion.yaml`, `EvidenceRecord.yaml`, `JudgeVerdict.yaml`, `CriterionVerdict.yaml`.
- `Task.yaml` edits: `+tags`, `+plan_id`, `+criteria`, `+attempt_count`, `+max_attempts`; **remove** `milestone_id`. Same for `TaskCreateRequest`/`TaskUpdateRequest`.
- `Message.yaml`: `type` enum `+judge_verdict`, optional `verdict` object.
- `Agent.yaml`/`AgentUpdateRequest.yaml`: document `system` semantics, `+rubric` (editable-on-system).
- `SlashCommand.yaml`: `+argument_hint` (optional).
- AsyncAPI frames: `GoalStatusFrame`(`goal_status`), `LoopStatusFrame`(`loop_status`), `PlanStatusFrame`(`plan_status`), `JudgeVerdictFrame`(`judge_verdict`) + `receive*` ops.
- **Remove** (R1–R7): `Milestone*.yaml`, milestone paths/refs/tag in `openapi.yaml`, `Task.milestone_id`, generated `Milestone*` types.
- Config: no new wire type (PlanningConfig is server-side config.json), but confirm no contract references milestones remain.

DoD: `make verify-contracts` exit 0; generated Go+TS committed in the same commit; grep shows zero `Milestone`/`milestone_id` in contracts + generated.

## Wave 1 — Backend foundations (parallel, after Wave 0; 4 agents)

- **A1 `pkg/plan` + config bounds** (backend-lead): new `pkg/plan` package (Plan struct, State/phase/failed_reason, `Store` mirroring `pkg/task/store.go` atomic+striped-lock, state-machine validation with `failed` terminal, same-workspace FK helper, membership/progress read-time). `PlanningConfig` in `pkg/config` (defaults + boot range-validation). Tests: `TestPlan_StateTransitions` (canonical matrix, failed frozen), `TestPlan_CountersPersistAndReload`, `TestBounds_PerEntityOverridesGlobal`.
- **A2 `pkg/task` model** (backend-lead): `Task` `+tags`(normalize/validate/dedup ≤16/≤64rune)`+plan_id`(same-ws FK)`+criteria`+`attempt_count`+`max_attempts`; `AcceptanceCriterion`/`EvidenceRecord`/`JudgeVerdict` types + validation (kind/text/check/author, ask||deny all-machine rejection); evidence redaction-before-truncate + cascade-delete; `judge_verdict` session entry type (`daypartition.go`). Tests: `TestTask_TagValidation`, `TestCriterion_Validate`, `TestEvidence_RedactBeforeTruncate`, `TestCriterion_AuthorRecorded`.
- **A3 Milestone removal + migration** (backend-lead, **after A2** — shares `pkg/task`; sequence or worktree): remove `MilestoneID` field/filter/patch, delete `rest_milestones.go` + route + tests; migration at task-store load reading legacy `milestone_id` from **raw JSON** (map[string]json.RawMessage), sentinel-guarded, normalize→headroom-truncate→ID-ordered suffix, due_date→empty Due, empty-milestone log. Tests: `TestMigrateMilestones_LegacyJSONAfterFieldRemoved`, `_Idempotent`, `_CrashSafe`, `_EmptyLogged`, `TestTask_NoMilestoneField`.
- **A4 System Agent seeding + privilege** (security-lead): seed Judge (`type:system`, locked, all-deny policy keeping Constraint #6 matrix total, editable model+rubric); narrow `IsPrivilegedAgent`→core-only; lifecycle guards (create/delete/disable 400 raw-body sniff); enumeration exclusions (IsChatTarget/default-fallback/routing-binding/delegation/team). Tests: `TestSeed_JudgeSystemAgent`, `TestAgents_SystemTypeUncreatable`, `TestAgents_JudgeUndeletable`, `TestSEC26_*`, `TestSystemAgent_ExcludedFromEnumeration`, `TestSystemAgent_Constraint6_BootCoverage`.

## Wave 2 — Runtime engine (parallel where disjoint, after Wave 1; 4 agents)

- **B1 TaskExecutor goal-loop + judge execution** (backend-lead, owns `task_executor.go` + new `pkg/agent/judge.go`): single-shot→attempt loop (default 3→wake owner); evidence-ladder judge (machine checks via assignee `bash` tool machinery ONLY, 60s timeout, ask→deny, redaction; prose via Judge System Agent no-tools call, evidence-first input; fail-closed; judge-unavailable pause+backoff 60/120/300s→300s, attempt-not-consumed); extend (not fork) `task_completion_signal.go`. Tests: attempt boundaries, timeout, policy triad, judge-unavailability, `TestTaskExecutor_ScratchpadExemptFromGoalLoop`.
- **B2 Plan engine** (backend-lead, owns new `pkg/agent/plan_engine.go` or `pkg/plan/engine.go`): hybrid coordinator (dispatch ready tasks on DAG-clear via `AdvanceBlockedDependents`; owner wakes at decision points via async-notifier; plan-level judge same Judge agent, 20-round; approved→running auto-tick; cap-waiting; boot reconciliation from task store; single-instance overlap guard; idle sweeper on cron; owner lifecycle delete-400/disable-pause). Tests: crash-recovery/reconciliation, global-cap 15/16/17, owner disable/delete, plan-state.
- **B3 /goal + /loop commands + origin gating** (backend-lead, owns `pkg/commands/cmd_goal.go`,`cmd_loop.go` + `handleCommand` hooks + `UnifiedMeta` session state): proof-driven /goal (round=turn+judge, 20-round, reason-forward, status/clear); /loop interval(cron every+continue) + self-paced(at-jobs), 100-run/7d, status/stop; `UserInitiated` flag at 4 origination points (fail-closed); global-cap admission. Tests: origin gating (user/cron/async/delegated/channel), round/run/idle boundaries, `TestRoleGating_SessionOwnership`.
- **B4 Gateway REST + WS frames** (backend-lead, owns `pkg/gateway/rest_plans.go` + WS emit): `/plans` CRUD + approve + `/tasks/{id}/evidence` + `/verdicts`; emit `goal_status`/`loop_status`/`plan_status`/`judge_verdict` frames; server-computed `Plan.progress`. Tests: REST handlers, frame emission, `TestVerifyContracts_NoMilestoneRefs`.

## Wave 3 — SPA (parallel, after Wave 0 for types; full E2E after Wave 2; 4 agents, frontend-lead)

- **C1 Board plan/tag filters + plan lifecycle** (`src/components/workspaces/`): replace `MilestoneFilterPills` with plan selector + tag chips; plan list/cards, create/edit slide-over (goal, DoD editor, owner, bounds), approve (400-payload rendering), Stop/Clear, `planStateColors.ts` (5 states + fallback retained), paused/failed_reason secondary chips.
- **C2 Task criteria/evidence/tags UX** (`src/components/command-center/TaskDetailPanel.tsx`, `TaskCard.tsx`): criteria editor (kind/command/exit-code/author), per-attempt verdict list, evidence viewer (truncation/redaction), attempt N/M from `Task.attempt_count`, tag input+chips, remove milestone dropdown.
- **C3 /goal + /loop chat** (`src/hooks/useSlashMenu.ts`, chat composer): palette entries (DeliveryAgent auto), `argument_hint` ghost, goal-active indicator (condition+round N/20+reason), loop status, status/clear/stop rendering.
- **C4 Agents System section + ActivityPanel transparency + WS consumption** (`src/routes/_app/agents*`, `ActivityPanel.tsx`, `src/store/chat.ts`, `src/lib/ws.ts`): System section (Judge model+rubric editable, no delete/disable/default), judge-verdict span (panel-default), zod-edge validate + query-invalidate the 4 new frames (`plan_status` NOT `_changed`), `useChatAgents` system-type exclusion.

## Review gates (after implementation)

- **Round 1** (task #6): 7 pr-review-toolkit agents (code-reviewer, silent-failure-hunter, type-design-analyzer, comment-analyzer, pr-test-analyzer, code-simplifier + architect) + `/grill-code` on full diff → parallel fix agents.
- **Round 2** (task #7): repeat on post-fix diff; all gates green (gofmt, golangci-lint, CI go test/build/race, govulncheck, vitest, tsc -b, verify-contracts).
- **Deliver** (task #8): embedded-SPA E2E sanity, final CI, PR → `release/v0.1.1` closing ADR/spec issues, no admin-merge.

## Conflict-avoidance rules

- One owner per file. `loop.go`/`task_executor.go` overlaps: B1 owns `task_executor.go` + judge; B3 owns `handleCommand` region of `loop.go`; if B2/B3 both need `loop.go` seams, use worktree isolation and a short reconciliation merge step.
- Every agent: `graphify query` first; build with `-tags goolm,stdjson`; NO full local `go test ./...` (scoped `-run` only, or push to CI).
- Every agent commits as the human identity (GH no-reply), no agent co-author trailer (CLAUDE.md MANDATORY).
