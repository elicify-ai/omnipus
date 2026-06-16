# Adversarial Review: Unified Project + Task + Milestone Management

**Spec reviewed**: `docs/internal/specs/project-task-milestone-spec.md`
**Review date**: 2026-06-09
**Verdict**: REVISE

## Executive Summary

This is a well-structured spec with strong traceability, good BDD coverage, and thorough attention to state machine boundaries. However it contains six MAJOR defects that will produce silent data loss, broken agent functionality, or contradictory implementation paths if shipped as-is. Critical gaps include: an unspecified cascade when a project is deleted (milestone files become orphaned forever), a missing functional requirement for the `agent_id` filter that appears only in BDD scenarios, a progress formula that silently treats `failed` tasks as blocking 100% completion, a contradictory status transition table vs UI selector spec, and a missing pagination spec for the milestone list endpoint. No CRITICAL findings were identified.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 7 |
| OBSERVATION | 4 |
| **Total** | **17** |

---

## Findings

### MAJOR Findings

#### MAJ-001 — Project delete does not specify cascade behavior for milestone files

- **Lens**: Incompleteness
- **Affected section**: "REST API Changes → DELETE /api/v1/projects/{project_id}/milestones/{id}" (line 314); Regression Test Requirements table (line 1176); Integration boundary "Milestone Storage" (line 608)
- **Description**: The spec specifies that `DELETE /api/v1/projects/{project_id}/milestones/{id}` clears `milestone_id` on tasks (FR-L2-011). The spec also specifies in the regression test section that project cascade delete "must also clear `milestone_id` on tasks whose milestone was in the deleted project." However, neither the regression table, the functional requirements, nor the behavioral contract specify what happens to the **milestone JSON files themselves** when their parent project is deleted. The existing `handleProjectDelete` in `rest_projects.go` cascades task files and link entries, but has no knowledge of milestones. After this feature ships, deleting a project will leave orphaned milestone files in `~/.omnipus/milestones/` with their `project_id` pointing to a non-existent project.
- **Impact**: After project deletion, `GET /api/v1/projects/ghost-id/milestones` correctly returns 404 because the project is gone. However the milestone files on disk are never cleaned up. Over time, `~/.omnipus/milestones/` accumulates files with no valid parent project. A `GET /api/v1/projects/{id}/milestones` must scan all milestone files (since they are stored flat, not per-project) — those orphaned files will still be scanned on every milestones-list call even though they can never match a valid project. This is a silent storage leak with O(all orphaned milestones) overhead per milestone list request, proportional to total historical project deletions.
- **Recommendation**: Add a new functional requirement: "FR-L2-028: `DELETE /api/v1/projects/{id}` MUST also delete all milestone files whose `project_id` equals the deleted project ID. This cascade MUST run before the project file itself is deleted, using the same best-effort pattern as task cascade (individual file deletion failures are logged WARN and do not abort the cascade). The `milestone_id` field on all tasks referencing those milestones MUST also be cleared (same pattern as FR-L2-011)." Add `TestHandleProjects_Delete_CascadesMilestones` to the TDD plan. Update the regression table.

---

#### MAJ-002 — `agent_id` filter on `GET /api/v1/board/tasks` has no functional requirement and is absent from the existing handler

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: L2-4 Acceptance Scenario 5 ("Given `GET /api/v1/board/tasks?agent_id=mia&status=next`"); BDD Scenario "Filter next tasks by agent" (line 789); Functional Requirements section (no corresponding FR)
- **Description**: L2-4 AC5, BDD scenario "Filter next tasks by agent", SC-L2-005's verification step (`GET /api/v1/board/tasks?milestone_id=<mid>`), and Test 14 (`TestBoardTask_FilterByAgentAndStatus`) all require that `GET /api/v1/board/tasks` accepts an `?agent_id=` query parameter. However, there is no functional requirement that specifies this filter. The existing `handleBoardTaskList` in `rest_board.go` only filters on `project_id` and `status` — there is no `agent_id` filter parameter today. Similarly, the spec adds `?milestone_id=` as a filter (noted in the REST API changes table at line 280) but also has no corresponding FR for this filter. An implementer reading only the FR section will miss both new filters entirely.
- **Impact**: Test 14 will fail at implementation time because the handler does not support the filter. The BDD scenario will have no corresponding server-side implementation. Agents that poll for their own tasks via this endpoint (per the agent execution lifecycle description) will receive tasks for all agents, not just themselves, breaking the agent task-pickup flow.
- **Recommendation**: Add two functional requirements: "FR-L2-XXX: `GET /api/v1/board/tasks` MUST accept an `?agent_id=` query parameter and return only tasks where `agent_id` equals the value." and "FR-L2-YYY: `GET /api/v1/board/tasks` MUST accept a `?milestone_id=` query parameter and return only tasks where `milestone_id` equals the value." Add both filters to the traceability matrix against L2-4 and L2-7 respectively.

---

#### MAJ-003 — Milestone progress formula silently counts `failed` tasks as "not done", misrepresenting project health

- **Lens**: Incorrectness / Ambiguity
- **Affected section**: FR-L2-010 ("count(tasks where milestone_id=X AND status='done') / count(tasks where milestone_id=X)"); Data Model "Milestone progress" (line 192); L2-5 AC3 (1 done, 2 inbox → progress 0.333)
- **Description**: FR-L2-010 defines progress as `done / total` where total is ALL tasks with `milestone_id = X`, regardless of status. This means: a milestone with 3 tasks (1 done, 1 failed, 1 active) shows 33% progress. The failed task and the active task both count against the denominator. This is arguably correct for "completion" tracking. However the spec never explicitly acknowledges this semantic choice. It is equally valid to: (a) count failed as equivalent to "not done" (current formula, unstated); (b) exclude failed from denominator (progress = done / (done + non-failed-non-done)); (c) count failed as "terminal non-success" and show it separately. The test dataset at L2-5 AC3 uses only `done/inbox/next` and does not test the `failed` case.
- **Impact**: If an engineer implements this differently from the intent (e.g. excluding failed from total), progress percentages will differ between frontend and backend implementations. The milestone progress bar in `ProjectHeader` will display a value that may mislead operators about how much work is complete vs. stuck.
- **Recommendation**: Make the formula explicit about all 6 status values: "FR-L2-010: Milestone progress MUST be computed as `count(tasks where milestone_id=X AND status='done') / count(tasks where milestone_id=X)`. ALL tasks referencing the milestone are counted in the denominator regardless of status (inbox, next, active, waiting, done, failed). A `failed` task reduces the ratio identically to an `inbox` task." Add a test dataset row: milestone M with 1 done, 1 failed — expected progress = 0.5.

---

#### MAJ-004 — Status transition table contradicts FR-L2-023 ("Status selector, all 6 values") for the `done → next` transition

- **Lens**: Inconsistency
- **Affected section**: Status Transitions table (lines 158–166: "done → inbox (retry) only"); FR-L2-023 ("Status (all 6 values)"); L2-10 AS1 ("Status (selector, all 6 values)")
- **Description**: The status transition table defines exactly which human-to-human transitions are permitted. The `done` row allows only `→ inbox (retry)`. But FR-L2-023 and L2-10 AS1 say the Status selector shows "all 6 values" and is editable, which implicitly allows `done → next`, `done → waiting`, `done → failed` directly from the selector. The server-side transition guard only explicitly blocks `→ active` (without agent-context). It does not block any other transitions. Two implementations are possible: (a) the selector shows all 6 values and the server accepts all non-`active` transitions freely (making the transition table purely informational/UX guidance), or (b) the transition table is enforced server-side and the selector shows only legal target statuses for the current state. These are incompatible design choices.
- **Impact**: If backend engineers implement server-side transition enforcement based on the table (blocking `done → next`) and frontend engineers implement an unrestricted selector (per FR-L2-023), `PUT` calls from the UI will return 400. If neither enforces the table, the table is misleading documentation that will confuse future engineers maintaining this code.
- **Recommendation**: Add a clarifying note to the transition table: "The status transition table is informational — it describes the intended GTD workflow. Server-side enforcement ONLY applies to the `→ active` transition (requires agent-context header). All other human-to-human status transitions listed in the table (and any not listed, except `→ active`) are accepted by the server without error. The frontend selector presents all 6 values without filtering by current state." If stricter enforcement is actually desired, add that as a separate FR and update the selector spec to show only legal transitions.

---

#### MAJ-005 — Dual-mode `TaskDetailPanel` adaptation: which prop controls GTD vs workflow mode is unspecified

- **Lens**: Ambiguity
- **Affected section**: Assumptions (line 1362: "the component detects context via a prop"); Frontend Component Map (line 1036: "Adapt in-place"); L2-10 scope
- **Description**: The spec states in the Assumptions section: "The adapted `TaskDetailPanel` drops the `fetchSubtasks` query and the subtasks UI section entirely when used in the GTD context. The existing workflow-task usage of `TaskDetailPanel` in `CommandCenterScreen` is unaffected (those two contexts use different instances or the component detects context via a prop)." The phrase "different instances or the component detects context via a prop" presents two mutually exclusive implementation paths without specifying which one. No prop name, no type signature, and no routing contract is defined. FR-L2-024 says "MUST NOT display a subtasks section" but does not define the mechanism.
- **Impact**: If one engineer uses a `mode: 'gtd' | 'workflow'` prop and another creates a `GTDTaskDetailPanel.tsx` as a separate component, the two implementations will have different file layouts, different test files, different import paths, and different prop interfaces — causing merge conflicts and requiring rework.
- **Recommendation**: Specify the mechanism explicitly. Proposed: "The `TaskDetailPanel.tsx` component MUST accept a `taskMode: 'gtd' | 'workflow'` prop (required). When `taskMode === 'gtd'`, the component renders using the `BoardTask` type, shows the new fields (prompt, priority, milestone, result, retry button), and MUST NOT render the subtasks section or call `fetchSubtasks`. When `taskMode === 'workflow'`, behavior is unchanged from today. The new `TaskDetailSlideOver` passes `taskMode='gtd'`. The existing `CommandCenterScreen` usage passes `taskMode='workflow'`." Add this prop definition to FR-L2-023 and FR-L2-024.

---

#### MAJ-006 — Milestone list endpoint has no pagination specification despite O(N) task scan per request

- **Lens**: Incompleteness
- **Affected section**: "GET /api/v1/projects/{project_id}/milestones" (line 294); FR-L2-013 (ordering only, no pagination); Integration boundary "Milestone Storage" (line 608)
- **Description**: The milestone list endpoint is specified to return all milestones for a project in a single response with no pagination. Additionally, computing `progress` for each milestone in a list response requires scanning all task files (`~/.omnipus/tasks/*.json`) — the spec says "a single-pass scan of all task files builds a `map[milestoneID]counts`" (Assumptions, line 1361). For a project with 50 milestones and 10,000 tasks, every `GET /api/v1/projects/{pid}/milestones` call performs a full task directory scan. The existing `GET /api/v1/board/tasks` has an explicit `limit` (default 200, max 1000). The milestone list has no such bound. No non-functional requirement addresses this.
- **Impact**: A project that accumulates many milestones over its lifetime will cause increasingly expensive list responses. More importantly, the missing `total` count in the response means the frontend cannot implement pagination later without a breaking API change. The omission also contrasts with the existing `BoardTaskListResponse` pattern (which includes `total`, `limit`, `offset`) — inconsistency makes the API harder to learn.
- **Recommendation**: Either: (a) Add explicit no-pagination rationale with a cap: "Milestones per project are bounded by human cognition. The list endpoint returns all milestones (no pagination). Projects with more than 200 milestones are not a supported use case in this release." OR (b) Define a paginated response consistent with `BoardTaskListResponse`. Also add a non-functional requirement: "The milestone list response MUST include the `total` count field for forward compatibility." Add `TestHandleMilestones_ListAllReturnsTotal` to the TDD plan.

---

### MINOR Findings

#### MIN-001 — `MilestoneUpdateRequest.yaml` does not specify how to clear `due_date`

- **Lens**: Incompleteness
- **Affected section**: `MilestoneUpdateRequest.yaml` schema (line 261); Data model "due_date: optional date string" (line 188)
- **Description**: The `MilestoneUpdateRequest` schema defines `due_date` as `{type: string}` with no `nullable: true` and no discussion of clearing. A user who wants to remove a due date from a milestone (set it to "no deadline") has no specified path: sending `{}` (omit) is a no-op, sending `{"due_date": ""}` returns 400 per the test dataset (row 6), and sending `null` is not handled. The milestone on-disk struct uses `omitempty`, so the field disappears when empty — but there is no path from "has due_date" to "no due_date" via PUT.
- **Recommendation**: Specify the clearing semantic explicitly: "To clear an existing `due_date`, the PUT body MUST omit the field — omitting a field in the `MilestoneUpdateRequest` leaves it unchanged, BUT `due_date` MAY be set to `null` (JSON null) to explicitly clear it. The handler MUST accept `null` for `due_date` and write the milestone without the field." Update the schema with `nullable: true` for `due_date` in `MilestoneUpdateRequest.yaml`. Add test dataset row: `due_date: null` → 200, `due_date` absent from subsequent GET.

---

#### MIN-002 — Status transition dataset does not include `active → done` from non-agent-context

- **Lens**: Incompleteness
- **Affected section**: "Dataset: Extended GTD Status Transitions (human context)" (line 1145); status transition table
- **Description**: The transition dataset has 10 rows covering key paths. It does not include a test case for `active → done` without agent-context. The transition table shows `active → done` is a system/agent-only transition. A human trying to manually close a stuck active task (e.g. the agent crashed) would need to know whether `active → done` is blocked or allowed without agent-context. The dataset only tests `active → done` with agent-context (row 5) and `active → failed` with agent-context (row 6).
- **Recommendation**: Add to the dataset: `| 11 | active | done | false | 200 or 400? | L2-3 |`. The spec must decide: should humans be able to force-close an active task? The transition table implies `active → done` requires agent-context (only listed under "To (system/agent)"), but this is not enforced by any stated FR. Clarify and add an explicit test case.

---

#### MIN-003 — The `/tasks` redirect loading state is specified in AW-6 but not in FR or a BDD scenario

- **Lens**: Incompleteness
- **Affected section**: AW-6 resolution (line 1281); FR-L2-016; BDD Scenario "/tasks route redirects to Inbox project" (line 861)
- **Description**: AW-6 specifies: "A loading state (spinner) MUST be shown while the projects list loads. If the fetch fails, redirect falls back to `/` (root chat)." This is a real behavior requirement — it is testable and has a user-visible effect. However it appears only in the Ambiguity Warnings table, not in FR-L2-016 and not in any BDD scenario or test. The BDD scenario for the `/tasks` redirect (line 861) only tests the happy path. There is no test for the failure case (fetch fails → redirect to root).
- **Recommendation**: Add to FR-L2-016: "If the `GET /api/v1/projects` fetch fails while resolving the Inbox project ID for the redirect, the component MUST navigate to `/` (root chat) and log a console.warn." Add a BDD scenario: "Given the `/tasks` route is navigated to AND the projects API fetch returns an error, When the route resolves, Then the browser is redirected to `/`." Add test 22.1: `tasks-route.test.tsx — fetch failure redirects to root`.

---

#### MIN-004 — `result` field max-length validation is defined in OpenAPI schema (50,000 chars) but no enforcement is specified in handler or FR

- **Lens**: Incompleteness
- **Affected section**: `BoardTask.yaml` additions: `result` `maxLength: 50000` (line 222); FR-L2-004 (no max-length for result)
- **Description**: The `BoardTask.yaml` schema sets `maxLength: 50000` for `result`. The `BoardTaskUpdateRequest.yaml` (which is the schema through which agents set `result`) does not show a `maxLength` for `result` in the spec as written. FR-L2-004 documents `result` as "string, optional" with no mention of the 50,000 character limit. The existing board task handler enforces field-level validation manually (e.g. name ≤ 200, description ≤ 2000). No FR or handler extension specifies that `result` must also be capped on write.
- **Recommendation**: Add to FR-L2-004: "The `result` field MUST be rejected with `400` when its length exceeds 50,000 characters." Confirm that `BoardTaskUpdateRequest.yaml` also carries `maxLength: 50000` for `result` (not just `BoardTask.yaml`). Add test dataset row: `result` of 50,001 chars → 400.

---

#### MIN-005 — `due_date` sort ordering: spec says "null due_dates last" but the on-disk format is an omitted string field (`omitempty`), not JSON null

- **Lens**: Incorrectness
- **Affected section**: FR-L2-013 ("ordered by `due_date ASC` (null last)"); on-disk struct `DueDate string \`json:"due_date,omitempty"\``
- **Description**: FR-L2-013 says "null due_dates last." But the on-disk struct uses `omitempty` — milestones without a due date are stored with the field absent (not `null`). When Go reads these files, `DueDate` will be an empty string `""`. Sorting `"" < "2026-01-01"` in Go string comparison puts empty strings FIRST (before any dated milestone), not last. The sort implementation must explicitly treat `""` as "greater than" all non-empty dates, which is non-obvious.
- **Recommendation**: Add to FR-L2-013: "When sorting, a milestone with an absent or empty `due_date` MUST be treated as later than any milestone with a non-empty `due_date` (i.e. placed last). Concretely: in Go, use `if a.DueDate == "" { return false }` / `if b.DueDate == "" { return true }` before the lexicographic comparison." Add a sort-order test case with a mix of dated and undated milestones.

---

#### MIN-006 — L2-10 AS9 specifies a confirmation dialog for unsaved edits but no BDD scenario or test covers it

- **Lens**: Incompleteness
- **Affected section**: L2-10 Acceptance Scenario 9 ("any unsaved edits are discarded after confirmation if changes were made"); TDD plan (no test for this)
- **Description**: L2-10 AS9 requires: "Given task detail open, When user presses Escape or clicks ✕, Then slide-over closes; any unsaved edits are discarded after confirmation if changes were made." No BDD scenario covers this branch. No TDD test covers it. The phrase "after confirmation" implies a browser confirmation dialog or a custom modal — neither is specified (type, message content, confirm/cancel labels). This is observable behavior with user-visible copy.
- **Recommendation**: Either promote to a BDD scenario: "Given task detail has unsaved edits, When user presses Escape, Then a confirmation dialog is shown with message 'Discard changes?' and Cancel/Discard buttons. Given user clicks Discard, Then the slide-over closes. Given user clicks Cancel, Then the slide-over stays open." OR acknowledge that the confirmation is OPTIONAL and change the acceptance criterion to: "unsaved edits are discarded without confirmation" (simpler). Add the corresponding Vitest test.

---

#### MIN-007 — Inbox project `pinned: false` on auto-creation means it sorts below user-created pinned projects but no sidebar ordering rule specifies this

- **Lens**: Incompleteness
- **Affected section**: FR-INX-1 (`pinned: false`); L2-6 AS4 ("Inbox appears in the Projects list"); L2-1 AS4
- **Description**: FR-INX-1 specifies the Inbox project is created with `pinned: false`. L2-6 AS4 says "Inbox appears in the Projects list (pinned or newest-first per existing sort logic)." L2-1 AS4 says Inbox appears in the Projects list. But the spec does not specify where Inbox appears relative to user-created projects. On a fresh install, Inbox is the newest project and sorts first. After a user creates more projects, Inbox is the oldest and sorts last under "newest-first" ordering. Since Inbox is the default entry point, pushing it to the bottom of the list is a poor UX.
- **Recommendation**: Add to FR-INX-1 or as a separate FR: "The Inbox project MUST always appear first in the sidebar Projects list, above all other projects regardless of pinning or creation date. It MUST NOT be reorderable." OR: "The Inbox project MUST be treated as always-pinned for display purposes." Pick one and add an acceptance scenario for the ordering.

---

### Observations

#### OBS-001 — `X-Omnipus-Agent-Context` header security model (AW-2) merits a tracked issue

- **Lens**: Insecurity
- **Affected section**: AW-2 resolution (line 1277); Integration boundary "Agent-Context Header" (line 621)
- **Description**: AW-2 acknowledges: "Any authenticated user can provide the header. MVP does not distinguish 'user' from 'agent' at the token level." This means any logged-in human with a browser and DevTools can set their own tasks to `active` and `session_id` arbitrarily. The spec correctly defers agent tokens to v0.3. However the issue tracking this gap is not referenced.
- **Suggestion**: Add a note: "Issue TBD (v0.3): Introduce agent-specific bearer tokens so `X-Omnipus-Agent-Context` can be validated against a token class, not just presence."

---

#### OBS-002 — E2E tests 39–42 are listed but no Playwright setup requirement is noted

- **Lens**: Infeasibility
- **Affected section**: TDD Plan, tests 39–42 (E2E Playwright)
- **Description**: Four Playwright E2E tests are specified but no pre-condition for Playwright availability in CI is noted. The existing spec for the test environment (SC-L2-011 and SC-L2-012) does not include an E2E verification step. If CI does not run Playwright, these tests provide no signal.
- **Suggestion**: Either confirm in SC-L2-011/012 that Playwright tests run in CI (and reference the Playwright stage in CI config), or explicitly mark these as manual/post-merge verification scenarios (like the Evaluation Scenarios section).

---

#### OBS-003 — Milestone `updated_at` absence is documented but deserves clarification on what "implementing PUT" adds

- **Lens**: Ambiguity
- **Affected section**: Milestone on-disk struct note (line 190): "No `updated_at` field — milestones are immutable after creation except via explicit PUT (add `updated_at` when implementing PUT if needed; not required for MVP)"
- **Description**: The note "add `updated_at` when implementing PUT if needed" is vague. The milestone `PUT` endpoint IS specified (returning `200 with updated Milestone`). If `updated_at` is conditionally added, the `Milestone.yaml` schema must account for it, and clients must not assume its presence.
- **Suggestion**: Decide now: either add `updated_at` to the milestone struct and schema unconditionally (simpler), or explicitly exclude it from the schema. The parenthetical "if needed" will become a debate at implementation time.

---

#### OBS-004 — No API client `getMilestone(projectId, milestoneId)` function listed

- **Lens**: Incompleteness
- **Affected section**: "API client additions (`src/lib/api.ts`)" (line 1067)
- **Description**: The API client additions list `fetchMilestones`, `createMilestone`, `updateMilestone`, `deleteMilestone`. The `GET /api/v1/projects/{project_id}/milestones/{id}` endpoint is defined server-side but no corresponding `getMilestone(projectId, milestoneId)` client function is listed. This endpoint is needed for the `TaskDetailPanel` milestone selector to show the milestone name when `milestone_id` is set but the project's milestone list has not been loaded.
- **Suggestion**: Add `getMilestone(projectId, milestoneId)` to the API client additions table, or note that the single-milestone GET is always satisfied by the list cache (if milestones are always loaded as a group when a project loads).

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | L2-1 through L2-11 all have acceptance scenarios |
| Every acceptance scenario has BDD scenarios | PASS | All acceptance scenarios trace to BDD scenarios |
| Every BDD scenario has `Traces to:` reference | PASS | All BDD scenarios include Traces to: |
| Every BDD scenario has a test in TDD plan | PASS | All BDD scenarios map to at least one numbered test |
| Every FR appears in traceability matrix | PASS | FR-L2-001 through FR-L2-027 all present |
| Every BDD scenario in traceability matrix | PASS | Traceability matrix covers all BDD scenario groups |
| Test datasets cover boundaries/edges/errors | PASS (with gaps) | Priority and due_date datasets are good; failed-status-in-progress-formula is missing (see MAJ-003) |
| Regression impact addressed | PASS (with gaps) | MAJ-001 cascade regression is missing (project delete leaves orphaned milestones) |
| Success criteria are measurable | PASS | All SC-L2-xxx criteria are measurable or reference specific CI commands |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Project delete → milestone cascade | No test verifies milestone files are deleted when their parent project is deleted | MAJ-001; TestHandleProjects_Delete_Cascades regression row |
| `active → done` without agent-context | No test for whether a human can manually close a stuck `active` task | MIN-002; status transitions dataset |
| `done → next` directly | No test for whether the UI's all-6-values status selector allows this | MAJ-004; task detail status selector |
| Milestone sort with mixed dated/undated | Sort test only covers all-dated or all-undated cases | MIN-005; TestHandleMilestones_CRUD |
| Milestone list with `failed`-status tasks in progress | No test dataset row for failed tasks' contribution to progress denominator | MAJ-003; TestMilestoneProgress_Computed |
| Redirect failure path | No test for `/tasks` → Inbox ID fetch failure → fallback to `/` | MIN-003; tasks route test |
| `result` field at 50,001 chars | No test dataset row for result field boundary | MIN-004; TestBoardTask_ExtendedFields |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Extended GTD Status Transitions | `active → done` without agent-context | Add row 11: `active → done, false, 200 or 400` with explicit decision |
| Milestone Due Date Validation | `due_date: null` (clearing existing value) | Add row 7: `null` → `200, due_date absent` |
| Priority Validation | No gap | Full coverage |
| Milestone progress | Tasks with `status: failed` in denominator | Add: 1 done, 1 failed → expected `0.5` |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `X-Omnipus-Agent-Context` header gate | risk | ok | ok | ok | ok | ok | Any authenticated human can set `active` status and `session_id` — spoofing agent identity. Acknowledged in AW-2, no tracking issue referenced (OBS-001) |
| Milestone CRUD endpoints | ok | ok | ok | ok | ok | ok | Auth required (withAuth), audit logging specified (FR-L2-025), FK validation prevents cross-project writes |
| `result` field (agent-set) | ok | ok | risk | ok | ok | ok | `result` is write-once from agent context but humans can overwrite it via UpdateRequest (schema includes `result`). No non-repudiation: who set the result (human or agent) is not tracked |
| Inbox auto-creation (boot) | ok | ok | risk | ok | ok | ok | Audit log not specified for Inbox auto-creation. A silent Inbox creation on every restart (if the check has a bug) would not be auditable |
| Milestone delete → task FK clear | ok | ok | risk | ok | risk | ok | No audit log entry specified for the task-file rewrites triggered by milestone delete. A bulk field-clear of hundreds of tasks is not auditable from the current spec |
| Project cascade delete (new milestone cascade) | ok | ok | risk | ok | ok | ok | Project delete emits an audit log, but the implied milestone file deletes (MAJ-001) do not have a specified audit requirement |

**Legend**: risk = identified threat not mitigated in spec; ok = adequately addressed or not applicable

---

## Unasked Questions

1. **What is the canonical behavior when a human uses the Status selector to set `done → next` directly?** The transition table only allows `done → inbox` for humans, but FR-L2-023 shows all 6 status values in the selector. Does the selector filter to legal transitions per the table, or are all non-`active` transitions always accepted by the server?

2. **What happens to milestone files when a project is deleted?** The spec specifies task-cascade and link-cascade for project deletion, but is silent on milestone files. (See MAJ-001.)

3. **Should `active → done` be a legal human transition without agent-context?** The transition table implies no, but no server-side enforcement for this is specified. An operator who knows an agent completed work but the task is stuck `active` needs a path to close it.

4. **Does the `result` field get cleared when a task is retried (moved from `failed` back to `next`)?** The spec says "result is preserved" (L2-3 AS3), but this means the failed agent's error output persists into the next execution attempt. The new agent's result will overwrite it only if the agent explicitly sets `result` on completion. If the retry times out, the old error text is still in `result`. Is this the desired behavior?

5. **How many milestones per project are realistic?** The milestone list has no pagination and scans all task files for progress. Is there a documented cap or expected maximum that justifies the O(all tasks) scan per request?

6. **What is the `updated_at` story for milestones?** The current struct has no `updated_at`. The spec notes "add if needed." If a milestone's name is changed via PUT, the `GET` response will not show when it was last modified. Is this acceptable?

7. **What does the sidebar Inbox indicator look like specifically?** L2-6 AS5 says "distinct visual indicator (e.g. inbox icon)" — the "e.g." is ambiguous. Phosphor icon name? Different background color? This needs to be resolved before frontend implementation to avoid rework.

---

## Verdict Rationale

This spec has excellent structural quality — thorough BDD coverage, a complete traceability matrix, well-defined field semantics, and explicit resolution of six known ambiguities. It will produce a largely correct implementation. The reasons for a REVISE verdict rather than PASS are:

MAJ-001 (orphaned milestone files on project delete) is a silent storage leak that will compound over the lifetime of the installation. Fixing it after deployment requires a migration tool; fixing it now requires two sentences in the spec. MAJ-002 (missing FR for `agent_id` filter) means the agent task-pickup flow — the primary execution lifecycle described in L2-4 — will not work out of the box because the handler currently has no `agent_id` filter and there is no FR to drive its implementation. These two findings alone justify revision.

MAJ-003 through MAJ-006 are correctness/consistency issues that different engineers will resolve differently, producing incompatible implementations of the same spec. They should be resolved in the spec before the first wave of development begins.

### Recommended Next Actions

- [ ] Add FR-L2-028 for project-delete → milestone cascade (MAJ-001)
- [ ] Add FR for `agent_id` and `milestone_id` filters on `GET /api/v1/board/tasks` (MAJ-002)
- [ ] Clarify milestone progress formula with respect to `failed` status tasks + add test dataset row (MAJ-003)
- [ ] Resolve status transition table vs. all-6-values selector contradiction — pick enforcement model (MAJ-004)
- [ ] Specify `taskMode` prop name and type for `TaskDetailPanel` dual-mode adaptation (MAJ-005)
- [ ] Add milestone list `total` count field and document pagination stance (MAJ-006)
- [ ] Address MIN-001 through MIN-007 before implementation (advisable but not blocking)
