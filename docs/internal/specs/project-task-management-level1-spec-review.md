# Adversarial Review: Level 1 Project & Task Management

**Spec reviewed**: `docs/internal/specs/project-task-management-level1-spec.md`
**Review date**: 2026-06-08
**Verdict**: REVISE

## Executive Summary

This is a thorough spec with good structure and explicit resolutions to many prior ambiguities. However, it contains three CRITICAL defects — a missing `SessionID`-extraction contract that will produce silent link-file poisoning, an unresolved concurrent-write race during cascade delete, and a Monitor cost endpoint referenced but never defined. Seven MAJOR findings include a 301-from-SPA architectural mistake, undefined PUT partial-update semantics, and CI-incompatible integration tests. The spec must be revised before implementation.

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 7 |
| MINOR | 5 |
| OBSERVATION | 3 |
| **Total** | **18** |

---

## Findings

### CRITICAL Findings

#### CRIT-001 — `ToolResultHookResponse` carries no `SessionID`; linker hook extraction path unspecified

- **Lens**: Incorrectness
- **Affected section**: FR-008 ("the hook receives the current session ID from the turn context"); Test 10 (`TestSessionAutoLink_LinkFileWritten`); Tests 34–36 (integration loop tests)
- **Description**: FR-008 states the hook receives the session ID from "the turn context." The actual `ToolResultHookResponse` struct (`pkg/agent/hooks.go:203-211`) has no `SessionID` field. The session ID is stored in the `context.Context` passed as the first arg to the `AfterTool` interceptor, via `tools.ToolTranscriptSessionID(ctx)` (`pkg/tools/base.go:169`). This works only if the `turnCtx` passed to `al.hooks.AfterTool(turnCtx, …)` at `loop.go:5274` carries the transcript session ID — which it does via `tools.WithTranscriptSessionID(turnCtx, ts.opts.TranscriptSessionID)` at `loop.go:3767`. However, the spec does not state this extraction path, making it easy for the implementing engineer to use a background context, a fresh context, or to attempt to read a field that does not exist on `ToolResultHookResponse`.
- **Impact**: If implemented without this constraint, the linker writes empty `session_id` values to `project_session_links.jsonl`. Because deduplication key is `(project_id, session_id)`, all empty-string session entries deduplicate to one entry but `GET /api/v1/projects/{id}/sessions` returns `[{"session_id":"", "created_at":"…"}]` — poisoned output with no error signal.
- **Recommendation**: Add to FR-008: "The `ProjectSessionLinker` hook MUST obtain the session ID via `tools.ToolTranscriptSessionID(ctx)` (NOT from `ToolResultHookResponse` — that struct has no `SessionID` field). If the returned value is empty string (non-tracked session), the hook MUST log WARN and MUST NOT write any link entry."

---

#### CRIT-002 — Cascade delete rewrites the link file while the linker hook may concurrently append; no serialisation specified

- **Lens**: Incompleteness
- **Affected section**: FR-007 step 2 ("remove all lines with `project_id=<id>` from `project_session_links.jsonl`"); Link-file integration boundary ("concurrent appends use a per-file mutex; compaction triggered when file exceeds 100,000 lines or 10 MB (background goroutine, no read blocked)")
- **Description**: FR-007 step 2 requires rewriting (read-filter-write) the entire JSONL link file during a DELETE request. FR-008 says the linker hook appends to the same file during active sessions, protected by a per-file mutex. But the spec defines two independent locking scopes with no coordination between them: (a) the DELETE handler's rewrite lock and (b) the linker hook's append mutex. If the delete rewrite and a linker append overlap, the rewrite may overwrite the new append entry, or the append may re-add an entry that was just removed. There is no specification of a single mutex serialising both paths.
- **Impact**: Under concurrent workload — multiple active agent sessions writing tasks to project P while an operator calls `DELETE /api/v1/projects/P` — the link file can contain entries for the just-deleted project or can silently drop link entries for other projects. No error is raised.
- **Recommendation**: Add to the link-file integration boundary: "A single named process-level mutex (`linkFileMu sync.Mutex`) MUST serialise ALL writes to `project_session_links.jsonl` — both the linker-hook append path and the cascade-delete rewrite path, and compaction. This mutex MUST be held for the full duration of any rewrite or append operation. Reads are not mutex-gated (JSONL append is atomic at the OS level for writes < 4 KB on Linux)."

---

#### CRIT-003 — FR-021 depends on a token-usage summary endpoint that is never defined

- **Lens**: Incompleteness
- **Affected section**: FR-021 ("Token Usage — per-agent token counts aggregated from `SessionMeta.Stats`, no dollar estimates in MVP; new summary endpoint, no new data collection"); Assumption ("Monitor cost breakdown reads from session stats aggregated across sessions")
- **Description**: FR-021 says "new summary endpoint" but never defines the HTTP method, path, auth requirements, request parameters, response schema, or aggregation logic. `GET /api/v1/agents` returns agent configuration, not token aggregates. No such endpoint exists in `pkg/gateway/rest.go`. This also violates Constraint #8 (contract-first): no OpenAPI schema is specified, so `make verify-contracts` will fail and generated types cannot be referenced by the frontend.
- **Impact**: Frontend and backend engineers will implement incompatible interfaces independently. The Monitor cost breakdown section either ships broken or is silently stubbed. CI `make verify-contracts` fails because the endpoint will be added unilaterally without a schema.
- **Recommendation**: Define the endpoint in the spec. Proposed minimum: `GET /api/v1/stats/tokens?period=month` (authenticated, non-admin) returns `{ agents: [{ agent_id: string, agent_name: string, tokens_in: int, tokens_out: int, tokens_total: int }], period_start: string, period_end: string }`. Add a schema to `contracts/components/schemas/TokenUsageSummary.yaml`. Specify aggregation: iterate all session meta files, group by `agent_id`, sum `Stats.TokensIn/Out/Total`; `period=month` = current calendar month UTC. Add this endpoint to FR-021 and the traceability matrix.

---

### MAJOR Findings

#### MAJ-001 — FR-030 requires a server-side HTTP 301 that the SPA architecture cannot deliver

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: FR-030 ("The gateway MUST issue a `301 Moved Permanently` redirect from `/command-center` … to `/tasks`"); UQ-7 ("Both must be in place.")
- **Description**: The Omnipus SPA is embedded in the Go binary and served via a catch-all handler that returns `index.html` for all non-API paths — it does not differentiate between `/command-center` and `/tasks` at the HTTP layer. A `301` from the Go gateway would only fire if the gateway registers a dedicated route for `/command-center` that pre-empts the SPA catch-all. This is possible but wasteful and fragile: (a) the SPA router's `routeTree.gen.ts` already owns `/command-center`; (b) a `301` is browser-cached permanently — if this feature rolls back, cached redirects persist for users; (c) adding a Go HTTP handler for a SPA route violates the single-page-application contract.
- **Impact**: The 301 requirement produces dead Go code (the gateway catch-all serves `index.html` before any specific path handler runs for non-API paths — the exact behaviour depends on handler registration order and is not guaranteed). Two engineers will implement conflicting approaches.
- **Recommendation**: Remove the "gateway MUST issue a 301" language from FR-030. Replace with: "The `/command-center` TanStack Router route definition MUST be replaced with a redirect component: `export const Route = createFileRoute('/_app/command-center')({ component: () => <Navigate to="/tasks" replace /> })`. This handles both bookmark navigation (SPA renders, then redirects) and client-side navigation. No server-side HTTP redirect is required or appropriate for a single-page application."

---

#### MAJ-002 — `PUT /api/v1/projects/{id}` partial-update semantics are not specified

- **Lens**: Ambiguity
- **Affected section**: US-1 AS3 (PUT with `{"core_team":["mia","jim"]}`); FR-012 (PUT with `{"pinned":true,"pin_order":N}`); FR-029 (PUT with `{"status":"archived"}`); FR-001
- **Description**: The spec uses `PUT` for three different patch operations, each sending only a subset of fields. HTTP PUT semantics require a full representation — if the client sends only `{"core_team":["mia","jim"]}`, a strict PUT replaces the resource and should clear `name`, `description`, `repository`, `status`. The spec never states whether the handler uses replace or merge semantics. Existing handlers in Omnipus use merge semantics (e.g. config update at `rest.go:2267`: "Deep merge nested objects so partial updates don't wipe sibling keys"), but this is not stated for projects.
- **Impact**: If the handler uses strict-PUT semantics and the frontend sends partial updates (as it will — the pin-to-top PUT from the sidebar sends only `{"pinned":true,"pin_order":N}`), project names and descriptions are silently cleared on every pin operation.
- **Recommendation**: Add to FR-001: "PUT /api/v1/projects/{id} uses merge (partial-update) semantics: only fields present in the JSON request body are updated; fields absent from the request body are left unchanged. This matches the existing merge pattern in the codebase."

---

#### MAJ-003 — `task_count` computation for the list endpoint is O(N×M) unless explicitly specified otherwise

- **Lens**: Infeasibility
- **Affected section**: FR-004; SC-001 ("≤ 100ms at p95 measured against up to 1,000 task files"); Assumption ("For ≤ 10,000 tasks this is acceptable < 100ms")
- **Description**: SC-001 only benchmarks `GET /api/v1/projects/{id}` (single project). The list endpoint `GET /api/v1/projects` must attach `task_count` to every project. FR-004 says "compute at read time by counting task files" but does not specify the algorithm. A naive implementer calls `listEntities[task]()` once per project, producing O(N×M) disk reads. The correct approach reads all tasks once and partitions counts in a `map[project_id]int` — O(M) regardless of N — but this is not stated.
- **Impact**: With 20 projects and 5,000 tasks, the naive implementation makes 100,000 sequential file reads on the hot list path. SC-001 passes (it only tests a single GET) while the list endpoint degrades silently.
- **Recommendation**: Add to FR-004: "When computing `task_count` for a list response (`GET /api/v1/projects`), the handler MUST call `listEntities[task]()` exactly once and build a `map[string]int` count (keyed by `project_id`) before attaching counts to each project — not once per project." Add SC-001b: "`GET /api/v1/projects` with 20 projects and 1,000 tasks returns in ≤ 200ms at p95."

---

#### MAJ-004 — Invalid `status` values on project update have no test coverage

- **Lens**: Ambiguity
- **Affected section**: UQ-2 ("Any other value is rejected with 400"); FR-029; Traceability matrix row for FR-029
- **Description**: UQ-2 resolves that invalid status values are rejected with 400, but there is no BDD scenario, TDD test name, or dataset covering this. The "Board Task Status" dataset (rows 6–8) correctly handles invalid task statuses; there is no equivalent "Project Status" dataset. `PUT /api/v1/projects/{id}` with `{"status":"deleted"}` has no test.
- **Impact**: Without a test, validation will likely be skipped. Agents or direct API callers can store arbitrary strings as `status`, corrupting the archive filter and causing `GET /api/v1/projects?status=archived` to silently miss projects stored as `"archved"`.
- **Recommendation**: Add a "Project Status Validation" dataset: `{"status":"active"}` → 200, `{"status":"archived"}` → 200, `{"status":"deleted"}` → 400, `{"status":""}` → 400, `{"status":"ACTIVE"}` → 400. Add BDD scenario "Invalid project status rejected with 400" tracing to FR-029. Add a TDD plan entry for this scenario.

---

#### MAJ-005 — `core_team` array is unbounded; no deduplication or length limit specified

- **Lens**: Incompleteness
- **Affected section**: FR-005; Edge Cases ("core_team agent IDs that no longer exist: stored IDs may reference deleted agents. Display gracefully"); US-10 AS5 (agent autocomplete)
- **Description**: FR-005 specifies storing `core_team` but never specifies: (a) maximum array length, (b) duplicate entry behaviour, or (c) whether non-existent agent IDs are validated at write time (the spec says graceful display, implying no write-time validation, which is correct). An agent can submit `{"core_team": [<1000 copies of same agent ID>]}` with no specified rejection.
- **Impact**: (a) Unbounded arrays are a trivial DoS against the list endpoint's in-memory computation; (b) duplicate IDs render as duplicate avatars in the project header, confusing users; (c) the `task_count` computation loops over all task files — a bloated `core_team` does not affect it directly, but storing 1,000-element arrays in project JSON increases the per-project read cost.
- **Recommendation**: Add to FR-005: "The backend MUST deduplicate `core_team` entries (case-sensitive) before storage and MUST reject `core_team` arrays with more than 20 entries with HTTP 400. The backend MUST NOT validate that IDs correspond to existing agents at write time (by design — lazy resolution at read time is correct for deleted-agent handling)." Add test: `{"name":"p","core_team":["a","a","b"]}` → stored as `["a","b"]`; `{"name":"p","core_team":[<21 entries>]}` → 400.

---

#### MAJ-006 — Linker hook fires on every `system.task.update` regardless of whether `project_id` changed; unbounded file growth

- **Lens**: Incompleteness
- **Affected section**: FR-008 ("append a link entry … when an agent executes `system.task.create` or `system.task.update` with a non-empty `project_id`"); link-file deduplication ("duplicates are dropped at read time")
- **Description**: A typical agent session updates the same task's status multiple times (inbox → next → active → done). Each `system.task.update` returns the unchanged `project_id` in its result. With 10 status updates in a single session, 10 identical `(project_id, session_id)` entries are appended — correct at read time but bloating the file. A single automated session with 500 task updates appends 500 entries, reaching the 100,000-line compaction trigger after 200 such sessions. The spec never specifies write-time deduplication.
- **Impact**: High-volume agent workloads trigger compaction continuously. Compaction runs in a background goroutine — if the append rate exceeds the compaction rate, the file grows unboundedly until disk exhaustion. The devpod disk has been observed at ≥96% full.
- **Recommendation**: Add to FR-008: "Before appending, the linker MUST consult an in-memory LRU set (process-scoped, capacity ≥ 1,000 entries, keyed by `project_id + ':' + session_id`) and skip the append if the pair was already appended during this process lifetime. This write-time dedup is best-effort (cleared on restart); read-time dedup remains the durable guarantee." This reduces redundant appends by ~99% for typical sessions.

---

#### MAJ-007 — Tests 34–36 require a live agent loop + LLM but are listed in the CI-expected SC-003 test run

- **Lens**: Infeasibility (FEA-03)
- **Affected section**: TDD Plan Tests 34–36 (`TestAgentLoop_TaskCreate_WithProjectID_LinksSession`, `TestAgentLoop_TaskUpdate_WithProjectID_LinksSession`, `TestAgentLoop_TaskCreate_NoProjectID_NoLink`); SC-003 test command (`go test -run 'TestProject|TestBoardTask|TestSessionAutoLink|TestHandle'`)
- **Description**: Tests 34–36 are described as "Full loop path: ProcessMessage → tool execute → result → link written to jsonl; no mock of loop internals." Running `ProcessMessage` through the full agent loop requires a configured LLM provider that returns a tool call — this is NOT possible in CI without a live API key and a non-deterministic model response. These tests are NOT tagged `//go:build eval` but they require LLM connectivity. SC-003's test command pattern matches them (`TestAgentLoop_*`) and is presented as a CI gate.
- **Impact**: SC-003 fails on the first CI run. Either (a) engineers add LLM mocks (defeating "no mock of loop internals"), (b) engineers add the `eval` tag (silently removing them from SC-003), or (c) CI fails permanently. None of these outcomes are specified.
- **Recommendation**: Resolve the contradiction. Preferred approach: Tests 34–36 inject a fake tool execution result directly into the hook dispatch (bypassing LLM) — a unit test of the hook wiring, not a full loop test. Rename to `TestProjectSessionLinker_*` and classify as Unit. The "no mock of loop internals" requirement must be dropped or replaced with "no mock of the linker hook itself." Update SC-003 to exclude `TestAgentLoop_*` from the CI pattern or accept the eval tag.

---

### MINOR Findings

#### MIN-001 — BDD "More than 5 unpinned projects collapses" does not specify WHICH 5 are visible

- **Lens**: Inconsistency
- **Affected section**: US-5 AS2 ("oldest projects are collapsed"); BDD Scenario "More than 5 unpinned projects collapses overflow"
- **Description**: The BDD scenario asserts "5 projects are visible and '▸ 2 more…'" but does not assert that the 5 visible projects are the 5 newest. A developer reading only the BDD scenario (not US-5 AS2) could implement arbitrary ordering of the visible 5.
- **Recommendation**: Amend the BDD scenario: "Given 7 unpinned projects P1(newest, created_at=T7) through P7(oldest, created_at=T1), When sidebar renders, Then P1 through P5 are visible in order (newest first), and P6 and P7 are collapsed into '▸ 2 more…'."

---

#### MIN-002 — "/" search activation scope is undefined

- **Lens**: Ambiguity (AMB-01)
- **Affected section**: FR-014; BDD Scenario "Inline search filters project list"
- **Description**: FR-014 says "inline project search activated by typing '/' in the Projects section" but does not define: Does the user need keyboard focus within the sidebar Projects section first? Does pressing "/" anywhere on the page trigger it? The "/" key is also a common browser find-shortcut (Firefox) and a Vim-mode trigger in some components.
- **Recommendation**: Add to FR-014: "The '/' shortcut activates project search only when keyboard focus is within the sidebar Projects section (e.g., a project row is focused or the section header is active). Global '/' presses outside the sidebar are not intercepted." Update the BDD scenario to add "And the sidebar Projects section has keyboard focus" as a precondition.

---

#### MIN-003 — Inline search: case sensitivity and match type unspecified

- **Lens**: Ambiguity (AMB-01)
- **Affected section**: FR-014; BDD Scenario "Inline search filters project list" ("user types '/' then 'mob'")
- **Description**: The BDD scenario shows "mob" matching "mobile-app" (substring match), but neither case sensitivity nor match algorithm is stated. "Case-sensitive substring" and "case-insensitive fuzzy" produce different results for inputs like "API" vs "api".
- **Recommendation**: Add to FR-014: "Search is case-insensitive substring match on project `name` only. Results update on every keystroke with no debounce delay."

---

#### MIN-004 — Traceability matrix names phantom test `TestProjectToolSchemas_Valid` not in TDD plan

- **Lens**: Inconsistency (CON-02)
- **Affected section**: Traceability Matrix row for FR-025 ("TestProjectToolSchemas_Valid"); TDD Plan Test 33 ("TestProjectToolDescriptions_AgentUsability")
- **Description**: The traceability matrix and TDD plan use different test names for the same requirement. `TestProjectToolSchemas_Valid` does not appear in the TDD plan. `TestProjectToolDescriptions_AgentUsability` does not appear in the traceability matrix.
- **Recommendation**: Use `TestProjectToolDescriptions_AgentUsability` (Test 33) in both places. Update the traceability matrix FR-025 row accordingly.

---

#### MIN-005 — `PUT /api/v1/board/tasks/{id}` and `PUT /api/v1/projects/{id}` BDD scenarios omit response body content

- **Lens**: Ambiguity
- **Affected section**: US-2 AS4 ("response is 200"); BDD Scenario "Update board task status" ("response is 200 and GET … returns status")
- **Description**: All other success responses specify body contents (e.g., "201 with task object containing project_id and status"). Both PUT scenarios say only "200" — whether the body is the full updated entity, a delta, or empty is unspecified.
- **Recommendation**: Add to US-2 AS4 and the BDD scenario: "200 with the full updated task object (same shape as the create response)." Add the same to the project PUT scenario in US-1 AS3.

---

### Observations

#### OBS-001 — SC-002 "50 tasks in ≤ 500ms" lacks a measurement baseline

- **Lens**: Infeasibility (FEA-04)
- **Affected section**: SC-002
- **Suggestion**: SC-002 asserts cascade delete of 50 files completes in ≤ 500ms with no evidence that this is achievable on the devpod disk (observed at ≥96% full). Fifty sequential `os.Remove` calls each trigger an ext4 journal flush. Recommend either measuring the baseline before committing to 500ms, or relaxing to ≤ 2s with a note that it is filesystem-dependent.

---

#### OBS-002 — FR-021 Activity section polls `GET /api/v1/agents` but that endpoint returns config, not real-time activity

- **Lens**: Incorrectness (COR-05)
- **Affected section**: FR-021 ("Activity — poll `GET /api/v1/agents` every 10s for currently active agent IDs")
- **Suggestion**: `GET /api/v1/agents` returns agent configuration objects, not activity state. Active-agent tracking is maintained in-process via `al.agentCurrentSession.Store(agent.ID, transcriptSessionID)` (`loop.go:3322`). There is no REST endpoint exposing this store. Before implementation, confirm what endpoint the Monitor Activity section should poll — `GET /api/v1/status` may already include relevant data, or a new endpoint may be needed. This should be a pre-implementation spike, not a discovery during development.

---

#### OBS-003 — "Linked sessions" in task detail are project-scoped, not task-scoped; framing is misleading

- **Lens**: Ambiguity
- **Affected section**: US-9 ("sessions that were auto-linked when an agent modified this task"); US-9 AS2 ("all sessions linked to the task's project are shown")
- **Suggestion**: The link file is keyed by `(project_id, session_id)` — there is no task-level link. Opening task T shows sessions that worked on *any* task in T's project, not sessions that worked on T specifically. US-9's title "Task Detail: Linked Sessions" and the phrase "when an agent modified this task" (spec intro) contradict the actual scoping. Users will see sessions listed for tasks they never touched. Recommend adding a UI callout: "Sessions linked to any task in this project." This is a documentation and UX framing issue, not a design change.

---

## Structural Integrity

### Variant A: Plan-Spec Format

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US1–US12 (US11 and US12 are numbered non-sequentially but present) all have acceptance scenarios |
| Every acceptance scenario has BDD scenarios | FAIL | US-8 AS3 (inline edit of project fields) has no BDD scenario; US-11 AS2 (live agent activity) has no BDD scenario; Monitor Audit Log section (US-11 AS5) has no BDD scenario |
| Every BDD scenario has `Traces to:` reference | PASS | All BDD scenarios have Traces to: |
| Every BDD scenario has a test in TDD plan | FAIL | "Project list sorted newest-first" (BDD) maps to no backend sort test — Test 19 is sidebar ordering only. "Cost breakdown shows per-agent token usage" (BDD) maps to no test (undefined endpoint, CRIT-003). |
| Every FR appears in traceability matrix | PASS | FR-001 through FR-030 all appear |
| Every BDD scenario appears in traceability matrix | FAIL | Monitor Audit Log section has no BDD scenario and no TDD test entry in the matrix |
| Test datasets cover boundaries/edges/errors | FAIL | Missing: Project Status validation dataset (MAJ-004); missing: `core_team` length/dedup dataset (MAJ-005); missing: omitted-status-on-create → default `inbox` dataset for board tasks |
| Regression impact addressed | PASS | Regression table explicitly covers schedules, workflow tasks, and command-center route |
| Success criteria are measurable | FAIL | SC-007 "within 1 render cycle" is not measurable without defining what constitutes a render cycle; SC-002 lacks baseline (OBS-001); no success criterion for `GET /api/v1/projects` list performance (MAJ-003) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Project status validation | No test for `PUT /api/v1/projects/{id}` with invalid `status` | MAJ-004; FR-029, UQ-2 |
| Concurrent cascade + linker append | No concurrency test for delete-rewrite vs. hook-append race | CRIT-002; FR-007, FR-008 |
| `core_team` dedup and length limit | No test for duplicate entries or `>20` entries | MAJ-005; FR-005 |
| Archive + agent task create | No test: can agent still create tasks under archived project? (UQ-4 says yes) | FR-029, UQ-4 |
| Link file absent on first write | No test: linker when `project_session_links.jsonl` does not yet exist | FR-008; link-file boundary |
| Monitor Audit Log non-admin path | No test: non-admin user sees "Audit log requires admin access" | FR-021 |
| `pin_order` tiebreaker | No test for two projects with identical `pin_order` (UQ-1 specifies `created_at` tiebreaker) | FR-012, UQ-1 |
| Board task default status | No test: `POST /api/v1/board/tasks` with no `status` field → defaults to `inbox` | FR-002 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Project Name Validation | `{"name": null}` listed in Scenario Outline Examples but has no TDD plan test explicitly referencing it | Add explicit assertion: null name field → 400 |
| Board Task Status | Missing: `POST` with no `status` field → 201 with `status: "inbox"` (default) | Add row: `{"name":"t"}` (no status) → 201 with `status: "inbox"` |
| Project Status | Entirely absent | Add dataset: active/archived/invalid string/empty string/wrong case (see MAJ-004) |
| `pin_order` | Zero, negative, very large int not specified | Spec says 0 = unset; behaviour of `pin_order: -1` or `pin_order: MAX_INT` is unspecified |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `POST /api/v1/projects` | ok | ok | risk | ok | ok | ok | No audit log entry specified for project create/update/delete (SEC-03 gap) |
| `DELETE /api/v1/projects/{id}` | ok | ok | risk | ok | ok | ok | Cascade delete is high-blast-radius; no audit trail specified |
| `project_session_links.jsonl` | ok | risk | ok | ok | risk | ok | File mode unspecified — default may be 0644 (world-readable); unbounded growth (CRIT-002, MAJ-006) |
| `GET /api/v1/projects/{id}/sessions` | ok | ok | ok | risk | ok | ok | Session IDs returned in plain text; if link file is 0644, any local user can read session IDs directly |
| `GET /api/v1/board/tasks` | ok | ok | ok | ok | risk | ok | No rate limit specified for new endpoints (INC-09 gap) |
| Sidebar project search | ok | ok | ok | ok | ok | ok | Client-side filter only; no new data exposure |
| `system.project.*` agent tools | ok | ok | risk | ok | ok | risk | No audit trail for agent-invoked project mutations; `system.project.delete` `confirm:bool` guard can be bypassed by any agent with tool access |
| Monitor Token Usage endpoint | risk | risk | risk | risk | risk | risk | Entirely unspecified (CRIT-003) — all STRIDE dimensions unknown until endpoint is defined |

**Key unmitigated threats:**

1. **Repudiation — project mutations not audited**: `POST/PUT/DELETE /api/v1/projects` and `POST/DELETE /api/v1/board/tasks` never mention `pkg/audit`. High-blast-radius cascade deletes are invisible in the audit log. Add FR: "All project and board-task mutating REST operations MUST emit an audit log entry via `pkg/audit`."

2. **Tampering — link file permission unspecified**: The link-file boundary specifies `os.OpenFile with O_APPEND|O_CREATE` but no file mode. Without explicit `0600`, the file may be created `0644` (world-readable on Linux). Session IDs are sensitive. Add `0600` to the boundary spec.

3. **Denial of Service — no rate limit on new endpoints**: `GET /api/v1/projects` and `GET /api/v1/board/tasks` iterate the filesystem on every request. No rate limit is specified. Add to FR-001 and FR-002: "All new authenticated endpoints MUST be covered by the existing per-IP authenticated rate limiter."

---

## Unasked Questions

1. **Who can delete a project?** The spec says all new REST endpoints are "authenticated" but never specifies whether project deletion requires admin role. In a multi-user Omnipus instance, any non-admin user could delete another user's projects via `DELETE /api/v1/projects/{id}`. Is there a `RequireAdmin` gate, or is project management scoped per-user?

2. **What does `GET /api/v1/projects/{id}/sessions` return when the link file contains corrupted (non-JSON) lines?** Compaction skips malformed lines, but the spec only states this for compaction — not for the read path of the sessions endpoint.

3. **Is there a maximum number of projects?** `GET /api/v1/board/tasks` is bounded at 1,000 items (FR-027) but `GET /api/v1/projects` has no stated limit. What does the endpoint do with 10,000 projects?

4. **How does the SPA sidebar know to refresh after an agent creates a project via `system.project.create`?** The agent tool writes directly to disk with no WS notification. If the sidebar polls `GET /api/v1/projects`, at what interval? If it does not poll, the sidebar is stale until navigation.

5. **What is the migration story for existing stored `"task_count": 0` in project JSON files?** The current `project.go:84` writes `"task_count": 0` into the JSON. The new struct must drop the `TaskCount int \`json:"task_count"\`` field to avoid using a stale stored zero instead of the computed value. This file migration must be explicitly addressed.

6. **What does `GET /api/v1/board/tasks?project_id=<non-existent-id>` return?** The spec returns 404 for `GET /api/v1/projects/{id}` with a non-existent ID, but `GET /api/v1/board/tasks?project_id=<nonexistent>` would return `200 []` (empty list, since no tasks match). Is this the intended behavior?

7. **Is `GET /api/v1/projects` paginated?** FR-027 adds pagination to `GET /api/v1/board/tasks` but `GET /api/v1/projects` has no pagination specification. State explicitly whether pagination is in scope.

---

## Verdict Rationale

CRIT-001 and CRIT-002 are silent data-corruption traps that will not be caught by any test in the current plan: the linker hook will produce empty session IDs or be race-corrupted in production before the link file ever grows large enough to trigger observable problems. CRIT-003 blocks an entire screen section — there is no endpoint to implement against, no schema to generate. These three items require resolution before any implementation code is written.

MAJ-001 wastes implementation effort on a Go HTTP handler that the SPA architecture makes unreachable for 99% of users. MAJ-007 will cause SC-003 to fail on the first CI run. Both are quick to fix in the spec but expensive to fix post-implementation.

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/project-task-management-level1-spec.md docs/internal/specs/project-task-management-level1-spec-review.md
```
