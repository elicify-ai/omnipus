# Adversarial Review (round 2): `list_jobs` — unified background-job visibility

**Spec reviewed**: `docs/internal/specs/list-jobs-spec.md` (rev 2, 2026-07-27, Status: Draft)
**Prior review**: `docs/internal/specs/list-jobs-spec-review.md` (BLOCK — 3 CRITICAL / 17 MAJOR / 7 MINOR / 3 OBSERVATION)
**Review date**: 2026-07-27
**Verdict**: **BLOCK**

## Executive Summary

Rev 2 closes the rev-1 findings it set out to close — the three CRITICALs, the traceability
failures and the evidence errors are genuinely fixed, and the mechanical completeness claims in the
matrix now verify (51/51 scenarios, 36/36 FRs, 66 tests, 84 dataset rows, zero dangling test
references — all re-checked here by title match, not by counting lines). It also introduced
**five new CRITICAL defects**, four of them inside requirements added in rev 2 to fix rev-1
findings. Two of the P0 user stories (US-2 "tell queued from stuck", US-3 "never see another
principal's work") are broken by the new text: `cap_active=0` — the entire signal US-2 AS-5 exists
to deliver — is now required to be omitted by **two independent** rev-2 rules, and the new
`per-principal` memo (FR-032c) will serve one turn's cross-workspace roster to a later
workspace-scoped turn. Separately, FR-010's task predicate adopts `Task.CreatedBy`, which is
**verifiably mixed-namespace** in this tree (`c.Username` on the REST path, `callerID` on the tool
path) — the exact hazard the spec's own strongest correction (C4) identified and eliminated for
plans, re-imported one predicate later with no analysis and no `[VERIFIED]` tag.

| Severity | Count |
|----------|-------|
| CRITICAL | 5 |
| MAJOR | 18 |
| MINOR | 10 |
| OBSERVATION | 4 |
| **Total** | **37** |

**What is NOT wrong** (stated once so it is not re-litigated). The nine ADR corrections C1–C9 are
**not** re-audited here — the rev-1 review audited them independently and found all nine hold, and
this review found nothing contradicting that; every finding below is against text rev 2 *added*.
Verified fresh for this review: FR-013/FR-025's "disk-only, no contract change" reasoning is correct
— `contracts/components/schemas/SessionLifecycleRecord.yaml` carries `additionalProperties: false`
but already omits `parent_durable_key`, `origin_channel` and `origin_chat_id`, and
`pkg/api/generated/contract_test.go` does not round-trip `session.LifecycleRecord`, so the disk↔wire
divergence is pre-existing and deliberate. The FR-019a bytes-vs-runes correction is right; the
FR-002 clamp resolution is right; FR-023's four grant sites are right; and every mechanical
completeness claim in the Traceability Matrix re-verified (see *Structural Integrity*).

---

## Findings

### CRITICAL Findings

#### [R2-CRIT-001] The per-principal memo (FR-032c) serves one turn's roster to a differently-scoped turn — a cross-workspace disclosure against the P0 scoping control, and a correctness break on every narrowed call

- **Lens**: Insecurity (Information Disclosure) / Incorrectness
- **Affected section**: FR-032(c); BDD *"Repeated calls under a per-principal memo stay honest"*; test 33d `TestListJobs_MemoTTL`; Ambiguity #5 (marked ✅ RESOLVED)
- **Description**: FR-032(c) requires "a **per-principal memo with a 2–5 s TTL**" and nothing else.
  The memo **key is never defined**. The BDD scenario reinforces the narrow reading ("Given a
  per-principal memo with a TTL of `T` … the second call performs **zero** store scans"), and the
  test description says "Second call within TTL does zero scans". A memo keyed on the principal
  alone is therefore not merely permitted — it is what the spec, the scenario and the test all
  describe. Three consequences, none addressed anywhere in the spec:

  1. **Cross-workspace disclosure.** FR-009 makes `ToolWorkspaceID(ctx)` fail *open*: a
     workspace-less turn returns the caller's rows across **every** workspace with
     `workspace_scoped=false`. Agents are bound to different workspaces on different channels, and
     the same agent id calls the tool from both. A workspace-less call at `t=0` populates the memo
     with a cross-workspace roster; a W1-bound call at `t=1.5 s` gets it back verbatim — W2 plan
     titles, W2 task titles, W2 handles, and `workspace_scoped=false` on a turn that was scoped.
     This directly violates US-3 AS-4 ("only W1 rows are returned"), FR-009, and SC-004.
  2. **Every narrowed call returns the wrong roster.** `kind`, `status`, `include_terminal`,
     `include_drafts` and `limit` all change the response. Within the TTL the memo returns the
     previous response regardless — so `list_jobs(kind="plan")` returns task and subagent rows,
     directly contradicting FR-022 ("When `kind` is supplied, the system MUST read only that kind's
     store"). Worse, it breaks the **recovery path rev 2 added to close CRIT-003**: the BDD
     scenario *"A post-restart DEFAULT call never looks like 'no work at all'"* ends with "the
     caller can recover the three rows by re-calling with `include_terminal=true`" — an agent doing
     exactly that within 2–5 s receives the memoized empty roster and concludes the work is gone.
     The *"Roster at scale"* evaluation scenario ("Call `list_jobs` with default arguments, **then**
     narrow with `kind` and `status`" → "the narrowed calls surface the specific rows") asserts a
     behaviour the memo makes impossible.
  3. **Audit hole.** FR-032(a) requires "Every call MUST emit exactly one structured audit/log
     entry" carrying counters derived from the scan. On a memo hit there is no scan. Whether the
     entry is emitted, and with what values, is undefined — so the forensic trail US-8 exists to
     provide has 2–5 s gaps under exactly the repeated-probing pattern the "Cross-agent probing
     under adversarial prompting" evaluation scenario describes.
- **Impact**: A P0 security control (US-3, "Scoping is therefore a security control, not a
  convenience") is defeated by a performance optimisation adopted verbatim from a reviewer's
  shorthand. An agent on an unbound channel leaks its own other-workspace work into a scoped turn's
  transcript — and the transcript is persisted. Independently, the tool returns wrong answers to
  every narrowed call made inside the TTL, which is the normal interaction pattern.
- **Recommendation**: Rewrite FR-032(c) to state the memo key exhaustively: `(principal,
  workspace_id, workspace_scoped, kind, status, include_terminal, include_drafts, effective_limit)`.
  State that the audit entry (FR-032a) is emitted on hits as well as misses, carrying a `memoized:
  true` marker and the memoized counters. Add two BDD scenarios and two tests: (a) a workspace-scoped
  call immediately following a workspace-less call returns only W1 rows; (b) a `kind`-narrowed call
  within the TTL reads that kind's store. **Then re-justify the memo**: an argument-keyed memo is
  trivially bypassed by an agent varying `limit`, so it is not a DoS control. If cost is the concern,
  FR-032(d) is the real control and the memo should be dropped or demoted to an OPTIONAL
  optimisation — do not ship a cache that both fails to bound cost and breaks scoping.

---

#### [R2-CRIT-002] FR-033 requires `cap_active` to be omitted when zero — deleting the exact signal US-2 AS-5 exists to deliver

- **Lens**: Inconsistency
- **Affected section**: FR-033 vs. US-2 AS-5, SC-003, BDD *"Cap pressure distinguishes a real queue from a stopped engine"*, Edge Case *"Engine stopped with approved plans present"*
- **Description**: FR-033 enumerates the counters that MUST be omitted when zero and explicitly
  includes the cap pair: *"Every diagnostic counter (`total_omitted`, per-kind `omitted`, per-kind
  `unreadable`, per-kind error entries, `terminal_suppressed`, `limit_clamped_to`,
  **`cap_active`/`cap_max`**) MUST be **omitted when zero or not applicable**."* But `cap_active = 0`
  is not a nominal-state absence — it is the **entire content** of US-2 AS-5:

  > **Given** a plan in `state=approved` while the plan engine is **not** admitting and the
  > installation's global active count is 0 … **Then** the response carries `cap_active=0` and
  > `cap_max=16` — so "nothing will ever start it" is distinguishable from "waiting for a slot".

  SC-003 asserts the same. The BDD scenario asserts "the response carries `cap_active = 0` and
  `cap_max = 16`". Test 32 `TestListJobs_CapPressureWithoutAdmit` was specifically strengthened in
  rev 2 to "assert the emitted values (`0`/`16`)". Under FR-033 those fields are absent, so the test
  fails and the story's mechanism is gone — and `cap_active=0` becomes indistinguishable from
  "unreliable snapshot" (dataset row 11) and "no engine wired" (dataset row 12), both of which also
  omit the pair.
- **Impact**: The one number that distinguishes *"a dead engine will never start my plan"* from
  *"a healthy queue, wait"* is deleted by a context-saving rule. The agent then waits forever on
  work nothing will start — the failure mode US-2 is written to prevent, arriving through the
  requirement added to fix a different one.
- **Recommendation**: Remove `cap_active`/`cap_max` from FR-033's enumeration and add an explicit
  carve-out: *"`cap_active`/`cap_max` are state, not diagnostics. They are emitted as a pair
  whenever the snapshot is usable — including `cap_active = 0`, which is load-bearing — and omitted
  as a pair only when the snapshot is absent, unreliable or stale (FR-029)."* Add a dataset row for
  `cap_active = 0` with a usable snapshot, asserting presence.

---

#### [R2-CRIT-003] FR-029's staleness rule independently deletes the same signal, and the staleness bound is never stated

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: FR-029; US-2 AS-5; Edge Case *"Engine stopped with approved plans present"*; Ambiguity #2 (bound values); dataset *Store failure modes*
- **Description**: FR-029 requires `list_jobs` to omit both cap fields when *"`observedAt` is older
  than **a stated staleness bound**"*. That bound is **never stated** — not in FR-029, not in the
  Success Criteria, and not in Ambiguity #2, which is the spec's own register of unresolved bound
  values (it lists the sub-bounds, `labelMax`, `nativeStatusMax` and the hard `limit` max, and
  omits this one). There is no dataset row for a stale snapshot, no BDD scenario, and no test:
  the traceability matrix maps FR-029 to three tests covering the *global-count* and *unreliable*
  cases only.

  The staleness rule then collides head-on with the case it must serve. The snapshot is refreshed
  by `Tick`. An engine that is **stopped** — the Edge Case *"Engine stopped with approved plans
  present. Expected: `queued` **plus** cap-pressure fields showing `active` far below `cap`"* — does
  not tick, so its snapshot ages past any bound and both fields are omitted. US-2 AS-5's premise
  ("the plan engine is **not** admitting") is precisely the state in which FR-029 suppresses the
  answer. An engine that never started has a zero `observedAt`, which is maximally stale.
- **Impact**: Two independent rev-2 mechanisms (this and R2-CRIT-002) each, alone, void US-2 AS-5,
  SC-003's first clause, the Edge Case and test 32. An implementer following the FRs writes code
  that cannot pass the tests the same document specifies.
- **Recommendation**: State the bound as a number (and add it to Ambiguity #2). Then decide the
  stopped-engine case explicitly and state it in FR-029: a stale-but-`reliable` snapshot from a
  stopped engine is the *most* informative reading available, so either (a) emit the pair with an
  additional `cap_observed_at` so the caller sees the staleness, or (b) emit the pair plus an
  explicit `engine_running: false`. Omitting is the one disposition that destroys the story. Add a
  dataset row (stale snapshot) and a test.

---

#### [R2-CRIT-004] FR-010 adopts `Task.CreatedBy` as an ownership predicate — verifiably mixed-namespace in this tree, re-importing the hazard C4 eliminated

- **Lens**: Insecurity (Information Disclosure) / Inconsistency
- **Affected section**: FR-010 task predicate; US-1's ownership-axis block; C4; dataset *Calling principal* row 6
- **Description**: C4 is the spec's strongest correction. It rejects `Owner`/`CreatedBy` for plans on
  the ground that they are **mixed-namespace** (`callerID` on the tool path, `c.Username` on the
  REST path) and selects the validated, always-an-agent-id `OwnerAgentID` instead, noting this
  *"eliminates R4's mixed-namespace risk entirely"*. FR-010 then adopts `Task.CreatedBy == caller`
  for the task kind with **no namespace analysis, no validator citation, and no `[VERIFIED]` tag on
  the namespace question** — the one property C4 established is decisive.

  Verified in the working tree, this branch:

  | Write site | Value written to `CreatedBy` | Namespace |
  |---|---|---|
  | `pkg/tools/task.go:531` | `callerID` | agent id |
  | `pkg/tools/todos.go:147` | `agentID` | agent id |
  | **`pkg/gateway/rest_tasks.go:847`** | **`c.Username`** | **username** |

  `pkg/task/task.go:314-316` documents `Owner`/`CreatedBy` only as *"server-set attribution
  (read-only on the wire)"* — it does not constrain the namespace. So `Task.CreatedBy` is exactly as
  mixed as `Plan.CreatedBy`, and the spec knows this pattern well enough to have written a whole
  correction about it.
- **Impact**: A human user whose username equals an agent id — `mia`, `jim`, `ava`, `ray` are all
  plausible usernames, and the base roster's ids are public — has every standalone task they created
  in the SPA surfaced, **with its title**, in that agent's roster as `relation="dispatched"`, on a
  P0 story whose thesis is *"Never see another principal's work"*. The disclosure is silent: no
  counter, no marker, and the row looks exactly like legitimately dispatched work. Dataset row 6
  tests the username/agent-id collision **for plans only** — the kind where the spec already fixed
  it.
- **Recommendation**: Either (a) restrict the `dispatched` half of FR-010 to an agent-id-namespaced
  field, adding one to `task.Task` if none exists (the same disposition C4 reached for plans), or
  (b) if `CreatedBy` must be used, state the namespace hazard explicitly in FR-010, require the
  predicate to reject a match when the value was written by the REST path, and add the
  username-collision dataset row and test for **tasks** mirroring row 6. Do not ship the predicate
  with the hazard unanalysed. Note that `list_tasks`' `role="delegator"` has the same flaw — A7
  already requires that to be filed.

---

#### [R2-CRIT-005] FR-032(d)'s per-call scan ceiling is unspecified, untested, and contradicts every exactness requirement in the spec — reopening CRIT-003

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: FR-032(d) vs. FR-017, FR-018, FR-031, SC-006, SC-017; Ambiguity #2; Traceability Matrix row FR-032
- **Description**: FR-032(d): *"The system MUST enforce a **hard per-call work bound** — a maximum
  number of records scanned per kind, configurable — with overflow reported through the existing
  omission counters."* No default value. No config key name. Not in Ambiguity #2's register of bound
  values. **No BDD scenario, no dataset row, and no test** — the matrix maps FR-032 to
  `TestListJobs_AuditEntryEmitted`, `TestToolPolicy_GlobalDenyKillSwitch` and `TestListJobs_MemoTTL`,
  none of which touches (d).

  "Overflow reported through the existing omission counters" is not achievable for most of them.
  You can count directory entries cheaply, so `omitted` can stay exact — but every other counter
  requires *loading* the record:

  - **`terminal_suppressed`** (FR-031) is *"an exact count, per kind and in total, of terminal rows
    that **exist for the caller** and were withheld"*. Terminality and ownership are both fields
    inside the file. Past the ceiling you cannot know either.
  - **`unreadable`** (FR-018, SC-006) counts records that failed to parse. An unscanned record is
    not known to be readable or not.
  - Per-kind `omitted` is owner-filtered; unscanned ids cannot be attributed to the caller.

  So on the first install that exceeds the ceiling, SC-017's *"`terminal_suppressed = N` in 100% of
  trials"* is false, FR-017's *"exact count"* is false, and the post-restart response reverts to
  under-reporting — **the CRIT-003 defect rev 2 exists to close**, arriving through the requirement
  added to bound cost. A5 explicitly states the stores grow monotonically and are never swept, so
  exceeding the ceiling is the steady state, not the exception.
- **Impact**: The tool's honesty guarantee — the property US-4 and US-5 are built on ("a short list
  that looks complete is the worst possible output") — silently degrades on exactly the large,
  old installs FR-032(d) was written for, with no marker distinguishing "bounded scan" from
  "complete scan".
- **Recommendation**: Pick a precedence and state it. Either (i) exactness wins and the ceiling
  applies only to **row materialization**, not to scanning, in which case FR-032(d) does not bound
  cost and should say so; or (ii) the ceiling wins, in which case FR-017/FR-018/FR-031 must be
  restated as *lower bounds* whenever the ceiling was hit, and the response MUST carry an explicit
  `scan_truncated: {kind: ids_seen}` marker (a `notes` field, so FR-033's omit-when-zero applies) —
  and SC-006/SC-017 must be re-scoped to below-ceiling populations. Give the ceiling a default and a
  config key, add it to Ambiguity #2, and add a dataset row plus `TestListJobs_ScanCeilingReported`.

---

### MAJOR Findings

#### [R2-MAJ-001] `limit` as a total cap applied after the sub-bounds re-opens the starvation hole the sub-bounds are now the sole defence against

- **Lens**: Incorrectness
- **Affected section**: FR-016 ("`limit` is a TOTAL cap applied *after* the sub-bounds"), FR-007, SC-016, operator ruling 1
- **Description**: Operator ruling 1 withdrew the `blocked`-first reorder, and rev 2 states plainly
  that FR-016's sub-bounds are now *"the ONLY anti-starvation mechanism"*. But FR-016 also makes
  `limit` a total cap applied **after** the sub-bounds, over a list sorted `queued → running →
  blocked`. A caller passing `limit=30` against 25 `queued` + 25 `running` + 3 `blocked` receives 25
  queued + 5 running and **zero blocked rows** — the sub-bound reserved them, and the total cap
  removed them again, because `blocked` sorts last of the three live groups. A small `limit` is
  exactly what a context-conscious agent passes, and the tool description (FR-016) will teach it the
  live maximum is 75.
- **Impact**: The single defect the operator ruling made load-bearing is trivially reachable from a
  normal argument. SC-016 does not catch it — it exercises the default call only.
- **Recommendation**: State that `limit` is applied **proportionally across the reserved groups**,
  or that each group retains at least `min(group_size, ceil(limit × group_share))`, or simply that
  `limit` never reduces a group below its populated reservation until all other groups are
  exhausted. Add a dataset row (`limit=30`, 25/25/3) and extend SC-016 to assert the property under
  a caller-supplied `limit`.

---

#### [R2-MAJ-002] The `omitted` counter is keyed by kind in the requirements and by status group in the only test that asserts values

- **Lens**: Inconsistency
- **Affected section**: FR-017, FR-033, US-4 AS-1 vs. SC-016 and BDD *"A large queued and running population cannot evict a blocked row"*
- **Description**: FR-017: *"Every omission MUST be reported with an exact count, **per kind** and in
  total."* FR-033 enumerates *"per-kind `omitted`"*. US-4 AS-1: *"an exact `omitted` count **per
  kind**"*. But SC-016 and its BDD scenario assert *"`omitted` reports `375` for **`queued`**, `375`
  for **`running`**, and `0` for **`blocked`**"* — a per-**status-group** keying. The two key spaces
  are not interchangeable and the fixture does not say which kind the 800 jobs are.
- **Impact**: The one test that proves the spec's only anti-starvation mechanism cannot be written
  from the requirements. An implementer picks per-kind (as three FRs say) and `TestBounds_PerStatusSubBounds`
  fails; picks per-group and FR-017 is unimplemented.
- **Recommendation**: Emit both — `omitted: {by_kind: {...}, by_status: {...}}` — or pick one and fix
  the other three references. Given the sub-bounds are per status group and the error entries are per
  kind, emitting both is the honest shape.

---

#### [R2-MAJ-003] FR-033's omit-when-zero rule was not propagated to the scenarios and datasets that assert `total_omitted = 0`

- **Lens**: Inconsistency
- **Affected section**: FR-033, US-1 AS-4 vs. BDD *"Empty roster is a success, not an error"* (line 721) and *Bounds and truncation* dataset rows 1, 2, 3
- **Description**: FR-033 requires `total_omitted` to be omitted when zero. US-1 AS-4 agrees:
  the empty roster *"carries **no** diagnostic counters at all"*. But the BDD scenario that AS-4
  traces to still asserts *"the roster has zero rows, **`total_omitted = 0`**, and no error
  entries"*, and dataset rows 1–3 all specify `total_omitted=0` as an expected **output**. This is
  the same class of unpropagated-change defect as rev 1's `limit` error-vs-clamp contradiction that
  rev 2 wrote a ⚠️ block to fix.
- **Impact**: `TestListJobs_EmptyRosterIsSuccess` (which the matrix maps to **both** FR-002 and
  FR-033) is specified twice with opposite expectations.
- **Recommendation**: Rewrite the scenario's third bullet as *"the roster has zero rows, no
  `total_omitted` field, and no error entries"*, and change dataset rows 1–3's expected output to
  *"`total_omitted` absent"*.

---

#### [R2-MAJ-004] FR-033 mandates "exactly one always-present field" and never says which one

- **Lens**: Ambiguity
- **Affected section**: FR-033; BDD *"Diagnostic counters are absent when nominal"*; `TestDiagnostics_OmittedWhenNominal`
- **Description**: *"Exactly **one** always-present field MUST remain so the caller can distinguish
  'nothing to report' from 'field missing'"* — the field is never named, in FR-033 or anywhere else.
  The BDD scenario asserts *"exactly one always-present nominal marker"* without naming it either.
  The test cannot be written.
- **Impact**: Two implementers produce two different response shapes for the nominal case, which is
  the most common case.
- **Recommendation**: Name it. `"notes": null` vs `"notes": {...}` is the cheapest form and needs no
  extra field: state that `notes` is always present and is `null`/absent-object when nominal, and
  drop the separate marker.

---

#### [R2-MAJ-005] FR-030/SC-005's response-size identity mixes runes and bytes — the same unit error FR-019a corrected one requirement earlier

- **Lens**: Infeasibility
- **Affected section**: FR-030, SC-005, dataset *Bounds* row 19, `TestListJobs_ResponseSizeBound`
- **Description**: FR-030 sets `labelMax = 120` **runes** and `nativeStatusMax = 200` **runes**, then
  requires the response bound to be the arithmetic identity `maxRows × (labelMax + nativeStatusMax +
  fixedRowOverhead) + envelopeOverhead`. SC-005 and test 33b measure *"the serialized response
  length"* / `len(result.Content)` — **bytes**. A 120-rune label of CJK or emoji is 360–480 bytes, so
  the identity understates the true maximum by up to 4×, before JSON escaping (`\"`, `\n`,
  `\uXXXX`) inflates it further. Dataset row 19's fixture ("10 000-rune labels") does not state the
  alphabet, so the test passes or fails on the author's choice of characters — **precisely the
  defect FR-019a's ⚠️ block identifies and corrects for `FilterMinLength`**, reintroduced one
  requirement later. Additionally `maxRows`, `fixedRowOverhead` and `envelopeOverhead` are never
  given values anywhere, so the identity is not evaluable.
- **Impact**: A release-gate criterion (SC-005) that either fails on multi-byte data or is quietly
  loosened by whoever writes the test.
- **Recommendation**: Define the maxima in **bytes** for the size identity while keeping **runes**
  for the truncation boundary (truncate to `min(120 runes, 480 bytes)`), or multiply the identity by
  4 and say so. Give `maxRows`, `fixedRowOverhead` and `envelopeOverhead` numeric values. Make
  dataset row 19 specify a 4-byte-rune alphabet explicitly, and add an ASCII mirror row.

---

#### [R2-MAJ-006] "Plan and task rows are always `actionable=true`" is false for terminal rows — US-6's guarantee is applied to one kind out of three

- **Lens**: Incorrectness
- **Affected section**: FR-011; US-6 ("Handing back a handle that fails on use is worse than admitting the handle is gone")
- **Description**: FR-011 states flatly: *"Plan and task rows are always `actionable=true`."* With
  `include_terminal=true` the roster contains `done`/`failed` plans and `done`/`failed` tasks.
  `execute_plan` will not run a `done` plan; task action tools will not act on a `done` task. Those
  rows carry `actionable=true` and a handle that fails on use — the exact defect US-6 exists to
  prevent, flagged honestly for subagents and left unflagged for the two kinds where it is equally
  true.
- **Impact**: An agent recovering a handle from a terminal plan row wastes a turn discovering the
  handle is inert, and the field it was told to trust said otherwise.
- **Recommendation**: Restate FR-011: *"`actionable` is `false` for any row whose `status` is
  `failed` or `completed`, for all three kinds. For `subagent` it is additionally `false` when the
  session id does not resolve in the current process."* Add the terminal plan/task cases to
  `TestListJobs_PostRestartTombstone` or a sibling.

---

#### [R2-MAJ-007] There is no way to reach row 26 of a group — the tool cannot satisfy its own scale scenario

- **Lens**: Incompleteness
- **Affected section**: FR-002 (argument list, "and no others"), FR-016 (sub-bounds 25/25/25), Evaluation Scenario *"Roster at scale"*
- **Description**: Bounds are hard at 25 per live status group. The only narrowing arguments are
  `kind` and `status`, and neither escapes the bound: `kind="plan", status="queued"` still returns at
  most 25 of the caller's 400 queued plans. There is no offset, no cursor, no label filter, no
  `since`, and FR-002 forbids adding one. The *"Roster at scale"* evaluation scenario (500 live jobs)
  expects *"the narrowed calls surface **the specific rows**"* — which is unreachable for 94 % of
  them.
- **Impact**: The tool's headline use case (US-1: find the handle for the job I lost) fails
  deterministically once the caller has more than 25 jobs in a group, and reports that failure only
  as a large `omitted` count.
- **Recommendation**: Add one bounded escape hatch to FR-002 — the cheapest is `label_contains`
  (server-side substring match, applied **before** the bounds so the counters stay exact), or an
  opaque `after` cursor derived from the FR-020 total order (which exists precisely to make this
  safe). Add a scenario and a test, and correct the evaluation scenario's expectation either way.

---

#### [R2-MAJ-008] `started_at DESC` truncates oldest-first, dropping exactly the work US-1 exists to recover

- **Lens**: Incorrectness
- **Affected section**: FR-007, FR-020; US-1
- **Description**: Within a group, rows are ordered by `started_at` **descending**, and truncation
  takes the head of the sorted list. So the rows dropped first are the **oldest** live jobs. US-1's
  entire premise is recovering a handle lost because *"its context window was trimmed, or a wake
  started a fresh turn"* — i.e. work that has been running long enough to fall out of context. The
  spec gives no rationale for DESC anywhere, and operator ruling 1 constrained the *group* order,
  not the intra-group direction, so this is not settled by the ruling.
- **Impact**: Under the load where truncation bites, the tool systematically hides the long-running,
  most-likely-forgotten jobs and shows the ones the agent just started and still has ids for.
- **Recommendation**: Either justify DESC explicitly against US-1, or switch the live groups to
  `started_at` **ASC** (oldest first) and keep DESC for the terminal groups (most-recently-finished
  first), which matches what each group is for. Whichever is chosen, state the reasoning in FR-007 so
  it is not silently reversed later.

---

#### [R2-MAJ-009] FR-029's cost justification is factually wrong: `Tick` never computes the active count, so "refreshed unconditionally on every `Tick`" is a new `pe.mu` acquisition plus a second full store scan per tick — not marginal cost

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: FR-029; Integration Boundaries *"`pkg/agent` — `PlanEngine` cap authority"*; *Relevant Execution Flows* row `PlanEngine.Tick`; FR-021; SC-013
- **Description**: FR-029 requires a lock-free accessor returning *"the last value
  `computeActiveLocked` produced, refreshed **unconditionally on every `Tick`**"*, and justifies the
  cost twice: *"`Tick` already performs an unfiltered `pe.planStore.List(plan.Filter{})` on every
  pass and does **not** hold `pe.mu`… so the refresh is marginal cost on work already being done"*
  and *"That is what makes FR-029's cached snapshot cheap: the scan the snapshot needs is already
  happening."* Verified in the working tree, this branch:

  - `computeActiveLocked` has **exactly one caller**: `admitLocked` (`plan_engine.go:2189`), reached
    only from `Admit` (`:2185`) **under `pe.mu.Lock()`** (`:2183-2185`). `Tick` does not call it,
    directly or transitively, except via `tryStartApprovedPlan` → `Admit` — i.e. **only when an
    approved plan exists**. On a pass with no approved plans the count is never computed at all.
  - `computeActiveLocked` performs **its own** `pe.planStore.List(plan.Filter{})` (`:2221ff`, quoted
    in the spec's own CRIT-001 block). It does not reuse the slice `Tick` already loaded at `:678`.
  - `Tick` additionally returns early on the overlap guard (`if !pe.claimTick()`, `:673`) and on a
    list error (`:679-682`), so even "every pass" is not every timer fire.

  So the scan the snapshot needs is **not** already happening. Implementing FR-029 as written adds,
  to every tick of the dispatch loop, forever: one `pe.mu` acquisition, one full unfiltered plan-store
  scan loading every plan file, and one call to every registered `activeCounter` — on installs where
  nothing is queued and nothing needs admitting. This is a cost regression in the hot path, justified
  by a claim about that hot path that is not true, in the section of a spec that exists to correct
  the ADR's wrong code claims.

  The alternative reading is worse: an implementer who takes the "does not hold `pe.mu`" sentence
  literally writes a **second, lock-free re-derivation** of the count — a parallel number that can
  diverge from the authoritative one, which is precisely the *"substituted a number that is not the
  same number"* defect CRIT-001 exists to prevent, relocated. `TestPlanEngine_CapSnapshotIsLockFreeAndGlobal`
  would pass, because it asserts values under a controlled fixture rather than identity with
  `Admit`'s number, and SC-013 constrains only the `list_jobs` call path, not `Tick`.
- **Impact**: Either a permanent added scan + lock on the plan dispatch loop for a read-only
  visibility feature, or a divergent second implementation of the cap count. The spec's stated
  reason for believing neither will happen is incorrect.
- **Recommendation**: Correct the cost claim in FR-029 and in the *Relevant Execution Flows* row, then
  specify the mechanism precisely: publish the snapshot from **inside** the existing `pe.mu`-held
  computation — `admitLocked` already has the value and the `reliable` flag — via a single
  `atomic.Pointer[capSnapshot]` store. One producer, no new lock, no second scan, lock-free reader,
  and the snapshot is by construction the same number `Admit` used. Accept that the snapshot then
  refreshes only when admission runs, and resolve the stopped/idle-engine consequence together with
  R2-CRIT-003 (which is the same case seen from the staleness side). Add to SC-013: *"the snapshot
  value is identical to the value `admitLocked` computed in the same pass"*, asserted by a test that
  reads both.

---

#### [R2-MAJ-010] US-8 AS-4 has no BDD scenario, and FR-032(d) and (e) have no test — the identical structural failure rev 2 closed at US-6 AS-3

- **Lens**: Incompleteness (structural)
- **Affected section**: US-8 AS-4; FR-032(d),(e); Traceability Matrix row FR-032
- **Description**: The rev-1 review's structural check *"every acceptance scenario has ≥1 BDD
  scenario"* failed at exactly one point (US-6 AS-3), and rev 2 fixed it and said so. Rev 2 then
  added US-8 with four acceptance scenarios and left AS-4 — *"When an operator reads the tool
  documentation, Then it explains what a non-zero `unreadable` means and what to do about it"* —
  with no BDD scenario. The nearest candidate, *"The tool description states its own limits"*, has
  four `Then` clauses and none is the runbook. FR-032's matrix row names three tests, none of which
  covers (d) the work bound or (e) the runbook.
- **Impact**: The same check fails again at a new point, so the spec's structural-integrity claim is
  once again true only of the parts that were checked.
- **Recommendation**: Add a BDD scenario asserting the operator documentation content (or fold it
  into `TestListJobs_DescriptionContract` if the runbook lives in the description), and add
  `TestListJobs_ScanCeilingReported` for FR-032(d) (see R2-CRIT-005). Re-run the structural check
  over US-7 and US-8, which rev 1's review never saw.

---

#### [R2-MAJ-011] The audit trail for a P0 security boundary is specified at Debug, and `pkg/audit` is conflated with structured logging

- **Lens**: Inoperability / Insecurity (Repudiation)
- **Affected section**: FR-032(a); US-8 AS-1; Symbols Involved (`pkg/audit` — "calls"); Evaluation Scenario *"Cross-agent probing under adversarial prompting"*
- **Description**: FR-032(a) requires *"exactly one structured audit/log entry at **Debug**"*. Debug
  is off in normal production configurations, so the forensic record does not exist when it is
  needed — and US-8's stated motivation is that *"a cross-agent scoping bug would have left no
  forensic trail"*. An entry that is compiled in but not emitted leaves the same absence. Separately,
  the requirement writes "audit/log" as if they were one thing: the Symbols table lists `pkg/audit`
  (a persisted, tamper-evident subsystem with its own decision/severity model) while the requirement
  describes an slog level. Only one of the two satisfies US-8, and the spec never picks.
- **Impact**: Ships a security-boundary tool whose access record is a disabled debug line, and the
  post-incident investigation the evaluation scenario contemplates still has nothing to read.
- **Recommendation**: Split (a) into two: a **`pkg/audit` record per call** (always written, carrying
  principal, workspace, scoped flag, kinds, row count — this is the security control), and an
  **optional Debug slog line** carrying the diagnostic counters (this is the debugging aid). State
  which subsystem, with which fields, for each. Update `TestListJobs_AuditEntryEmitted` to assert
  against the audit store, not a log capture.

---

#### [R2-MAJ-012] Unmappable native states require a sixth `status` value that FR-006 forbids, with no FR, no scenario and no test

- **Lens**: Inconsistency
- **Affected section**: FR-006 vs. *Plan native state* dataset row 11 and *Subagent lifecycle* dataset row 10
- **Description**: FR-006: *"The normalized `status` vocabulary MUST be **exactly** `queued |
  running | blocked | failed | completed`."* Dataset row 11 (`state="wat"`) and subagent row 10
  (`state=""`) both require *"row carries an **explicit unknown marker**, no panic"* — a sixth
  value, or a new field, neither of which any FR defines. Both rows mis-trace to *"Scenario: A
  failed store yields a per-kind error"*, which is about a directory-level read failure, not about
  an unrecognised enum value. No BDD scenario and no test covers either.
- **Impact**: Two error-path behaviours the datasets require are unspecified and untested. On real
  data (a plan written by a newer build, a hand-edited file) the implementer's guess decides between
  a panic, a dropped row, and a silent mapping to `failed`.
- **Recommendation**: Add an FR: *"A native state that maps to no normalized value MUST produce a
  row with `status='blocked'` and `native_status='unknown:<raw>'` (redacted and bounded per FR-019
  / FR-030), and MUST count in a `notes.unmapped` counter."* Or extend the vocabulary to six values
  and update FR-006, ADR-056 D3 and the amendment table. Re-trace both dataset rows and add a
  scenario + test.

---

#### [R2-MAJ-013] There is no normative response shape anywhere in the spec

- **Lens**: Ambiguity
- **Affected section**: FR-003, FR-031, FR-033, FR-025, US-3, US-2
- **Description**: The response fields are defined across at least six places: FR-003 (11 row
  fields + conditional `generation`), FR-009 (`workspace_scoped`), FR-021/FR-029 (`cap_active`,
  `cap_max`), FR-031 (`terminal_suppressed`, per kind and total), FR-033 (a `notes` object, six
  counter families, one unnamed always-present field), FR-002 (`limit_clamped_to`), FR-018 (per-kind
  error entries). FR-025 correctly forbids putting this in `contracts/`, so **no artifact defines
  it** — and the spec contains **no example response**. The `notes` object introduced by FR-033 is
  named once and never given a shape, while other requirements speak of the same counters as
  top-level fields ("the response carries `cap_active=0`", "the response carries `workspace_scoped
  = false`").
- **Impact**: Every BDD assertion of the form "the response carries X" is ambiguous about where X
  lives. Two implementers produce two incompatible JSON shapes, and the LLM-facing contract — the
  actual product — is defined nowhere.
- **Recommendation**: Add a *Response Shape* section with one fully-worked JSON example of the
  nominal case and one of the degraded case (truncation + unreadable + a per-kind error + suppressed
  terminals + clamped limit), and state that these two examples are normative. It costs half a page
  and removes a dozen ambiguities at once.

---

#### [R2-MAJ-014] Six requirements mandate content in `Description()`, which is resident in every agent's prompt on every request, with no length bound and no test on size

- **Lens**: Overcomplexity / Inconsistency
- **Affected section**: FR-012, FR-016, FR-023, FR-024, FR-033, FR-035; `TestListJobs_DescriptionContract`
- **Description**: The description MUST state: that `actionable=false` is informational only
  (FR-012); that the roster is a best-effort near-snapshot (FR-024); that an id is meaningful only
  with its kind (FR-035); that an operator can disable the tool globally (FR-023); the omit-when-zero
  convention (FR-033); and that `limit` above 75 is only reachable with `include_terminal=true`
  (FR-016). Ambiguity #4 adds a seventh (documenting the difference from `list_tasks` and `delegate
  status`). Omnipus sends the full tool set on **every** request, so this text is a fixed per-request
  token cost for every agent that has the tool — which FR-023 requires to be all of them. Meanwhile
  FR-033 deletes zero-valued counters from the **response** specifically to protect the caller's
  context. The spec optimises the once-per-call payload and ignores the every-request one, and
  `TestListJobs_DescriptionContract` asserts presence of each clause with no bound on total size.
- **Impact**: A net context regression that the spec never weighs, on the axis it claims to care
  about most.
- **Recommendation**: Add a stated maximum (e.g. 900 characters) to FR-012 and assert it in
  `TestListJobs_DescriptionContract`. Move the operator-facing material (kill switch, runbook,
  `unreadable` semantics) out of the tool description into the operator docs FR-032(e) already
  requires — an operator does not read tool descriptions and an LLM does not need the kill switch.

---

#### [R2-MAJ-015] FR-015 makes the delegation **write** path refuse work to serve a listing tool, with no flag, no fallback and one positive-path test

- **Lens**: Inoperability
- **Affected section**: FR-015, A2, FR-034, Impact Assessment (`DelegateTool` mint — MEDIUM)
- **Description**: FR-015 requires `delegate`'s lifecycle mint to **fail closed** — error, no
  record, *"the delegation does not proceed"* — when `strings.TrimSpace(ToolAgentID(ctx))` is empty.
  This converts a previously-working code path into a hard failure, in the core delegation feature,
  to guarantee a field that only `list_jobs` reads. A2 verifies `ToolAgentID` on four call sites and
  offers FR-008 as *"the backstop for any path that is not"* — but FR-008 backstops the **read**
  side; nothing backstops the write side. The FR-023 kill switch disables `list_jobs`, not the mint
  guard, so there is no way to turn this off in the field. Coverage is one negative test (28) and
  one positive test (`TestDelegateMint_StampsParentAgentID`) against a single mint site, while FR-034
  requires the field to be carried forward on **every generation mint** (`follow_up`/Play), a path
  that does not exist yet.
- **Impact**: If any delegation entry point reaches the mint without an agent id — now or after a
  future refactor — delegation stops working entirely, with no flag to disable the guard and no
  degradation path.
- **Recommendation**: Keep fail-closed (it is the right default) but make it operable: add a config
  key (or reuse `sandbox` posture) that downgrades the guard to *log-and-mint-with-empty*, state the
  rollback procedure, and require a positive-path regression test **per mint site** — enumerate them
  in the Regression table rather than naming one. Raise the `DelegateTool` mint risk from MEDIUM to
  HIGH in the Impact Assessment: its failure mode is "delegation stops", not "a field is missing".

---

#### [R2-MAJ-016] `status` naming a terminal value against the default `include_terminal=false` silently returns nothing

- **Lens**: Incompleteness
- **Affected section**: FR-002, FR-016, BDD *Scenario Outline: Argument validation*
- **Description**: `status` accepts any of the five normalized values, including `failed` and
  `completed`. `include_terminal` defaults to `false` and FR-016 excludes terminal rows unless it is
  true. So `list_jobs(status="failed")` — the single most natural query after "what am I still
  working on?" — returns an empty roster. `terminal_suppressed` (FR-031) partially rescues it, but
  no requirement, scenario or dataset row covers the combination, and the argument-validation table
  does not list it.
- **Impact**: A plausible agent query returns nothing, and the agent concludes nothing failed.
- **Recommendation**: State the disposition in FR-002: *"When `status` names a terminal value,
  `include_terminal` is implied `true`."* (Auto-implying is better than erroring, by FR-002's own
  clamp rationale.) Add the row to the argument-validation table and a test.

---

#### [R2-MAJ-017] Unknown-argument handling contradicts FR-002's own stated principle for `limit`

- **Lens**: Inconsistency
- **Affected section**: FR-002; BDD *Scenario Outline: Argument validation* (row `relation | any value | validation error`)
- **Description**: FR-002 resolves the `limit` overflow case by clamping, with an explicit rationale:
  *"clamping wins because it is the one disposition that never costs the caller a turn."* The same
  requirement then hard-errors on **any** unknown argument, and the BDD table's last row makes the
  point concrete: an agent that passes `relation` — a field it just read off a response row — gets a
  validation error and zero rows. LLM tool calls include stray arguments routinely. The spec applies
  opposite dispositions to two instances of the same class of input error and reconciles neither.
- **Impact**: The wasted turn the clamp was designed to avoid is reintroduced through a more common
  input mistake.
- **Recommendation**: Ignore unknown arguments and report them once in `notes.ignored_args` (which
  FR-033 already omits when empty), reserving hard errors for **known** arguments with invalid
  values. Update the table row and `TestArgs_Validation`.

---

#### [R2-MAJ-018] FR-034's "newest generation only" has no selection mechanism and conflicts with `include_terminal=true`

- **Lens**: Ambiguity
- **Affected section**: FR-034; Edge Case *"A subagent session resumed to a new generation"*; test 39a
- **Description**: FR-034 requires a row to represent *"the **newest generation** of a session"*, but
  never says how older generations are excluded. The lifecycle store's own invariant is that a
  terminal record is never mutated — `follow_up`/Play mint a **new** record. If prior generations are
  separate records, they are terminal and would legitimately appear as rows under
  `include_terminal=true`, contradicting the Edge Case's *"exactly one row"*. If they share a session
  id, `ListLenient` must be told which to return, and the spec does not say. Test 39a asserts
  "exactly one row" without stating the arguments.
- **Impact**: Under `include_terminal=true` — the argument the CRIT-003 recovery path tells agents to
  use — a resumed session shows N rows or 1, undecided.
- **Recommendation**: State the rule: *"When multiple records share a `session_id`, only the record
  with the highest `generation` is emitted, in all argument combinations; superseded generations are
  neither emitted nor counted in `terminal_suppressed`."* Say so in FR-034 and make test 39a run
  both with and without `include_terminal`.

---

### MINOR Findings

#### [R2-MIN-001] Dangling scenario reference in the Bounds dataset

- **Lens**: Inconsistency
- **Affected section**: *Bounds and truncation* dataset row 6 — "Scenario: Blocked rows are not starved"
- **Description**: No scenario by that title exists; the real title is *"A large queued and running
  population cannot evict a blocked row"*. Mechanically checked: it is the only dangling
  scenario reference in the six datasets.
- **Recommendation**: Retitle the reference.

---

#### [R2-MIN-002] `L` (the single "live limit") survives in the Bounds dataset after FR-016 replaced it with three sub-bounds

- **Lens**: Inconsistency
- **Affected section**: *Bounds and truncation* dataset rows 2–6
- **Description**: Rows 2–4 speak of `L−1` / `L` / `L+1` "live jobs" and row 6 of "`L` running + 1
  blocked". FR-016 replaced the single live limit with `queued=25 / running=25 / blocked=25` and a
  75-row total, so `L` is undefined: "`L` live jobs → all returned" is false if all `L` are `queued`.
- **Recommendation**: Restate rows 2–6 against the sub-bounds (e.g. "24 / 25 / 26 `queued` jobs").

---

#### [R2-MIN-003] The `ask` policy verdict is never addressed

- **Lens**: Incompleteness
- **Affected section**: FR-023, US-7, *Tool-policy resolution* dataset
- **Description**: The vocabulary is `allow | ask | deny` and the compositor's precedence is
  `deny > ask > allow`. FR-023, US-7 and all 12 dataset rows discuss only `allow` and `deny`. An
  operator who sets `list_jobs: ask` globally makes every call block on a human prompt — for an
  autonomous background agent recovering a handle mid-turn, that is a hang, not a prompt.
- **Recommendation**: Add a dataset row for `ask` and state the intended behaviour (most likely:
  `ask` is a supported but discouraged setting, documented in the runbook alongside the kill switch).

---

#### [R2-MIN-004] A7's "MUST be filed before this spec merges" carries no issue number

- **Lens**: Inoperability
- **Affected section**: A7
- **Description**: A7 escalates `list_tasks`' unguarded `ToolAgentID(ctx)` (the same fail-open class
  US-3 closes here, in a tool every agent already has `allow` for) to *"MUST be filed before this
  spec merges, not after"* under Constraint #7 — and names no issue. Obligations without ids are
  lost at merge.
- **Recommendation**: File the issue now and put its number in A7 (and in R2-CRIT-004's `CreatedBy`
  half, which is the same defect in the same file).

---

#### [R2-MIN-005] The memo introduces shared mutable state with no stated concurrency discipline

- **Lens**: Incompleteness
- **Affected section**: FR-032(c); SC-012; test 39 `TestListJobs_ConcurrentDuringDispatch`
- **Description**: FR-032(c) adds a per-principal cache read and written by concurrent tool calls
  (test 39 runs 8 goroutines under `-race`), and specifies no locking, no eviction policy, and no
  bound on the number of principals retained. FR-028's contention budget is stated precisely for
  `DelegateTool.mu`; the memo gets nothing.
- **Recommendation**: State the lock (or `sync.Map`), an entry cap, and that the memo is
  process-local and not persisted.

---

#### [R2-MIN-006] SC-002's "500 rows" clause has no implementing test

- **Lens**: Infeasibility
- **Affected section**: SC-002; tests 1–5
- **Description**: SC-002 requires the `stalled`/`awaiting_owner_correction` → `blocked` mapping to be
  *"asserted at both small (5 rows) and large (500 rows) roster sizes"*. The named tests
  (`TestNormalizeStatus_*`, `TestNormalizeStatus_StalledIsBlockedNotRunning`) are table-driven pure
  unit tests over single values; none constructs a 500-row roster. Rev 2 withdrew SC-012's unmeasured
  latency claim for exactly this reason and left this one.
- **Recommendation**: Either drop the size clause (the mapping is size-independent by construction)
  or name the test that builds the 500-row fixture.

---

#### [R2-MIN-007] SC-011's "zero substrings of length ≥ 4" will false-positive on ordinary text

- **Lens**: Infeasibility
- **Affected section**: SC-011
- **Description**: The criterion forbids the output containing **any** 4-character substring of a
  registered secret. Real secrets contain common sequences (`http`, `test`, `1234`, `pass`, `-----`),
  and legitimate labels contain them too, so the assertion fails on correct output. Rev 1's version
  had the same shape and rev 2 widened it to two fields and every length from 1 byte, increasing the
  exposure.
- **Recommendation**: Assert on the secret's **distinctive** substrings (e.g. every 8-byte window,
  or the full value and its longest 3 windows), and state the corpus explicitly so the criterion is
  reproducible.

---

#### [R2-MIN-008] Two required dependencies are missing from Integration Boundaries

- **Lens**: Incompleteness
- **Affected section**: Integration Boundaries; FR-005; FR-019
- **Description**: FR-019 requires every free-text field to pass through
  `config.Config.FilterSensitiveData`, and FR-005 requires the agent registry to resolve
  `LifecycleRecord.AgentID` to a display name. Both are hard runtime dependencies of the tool. The
  registry appears in Symbols Involved but neither has an Integration Boundaries entry stating its
  data-in/data-out/on-failure contract — and "on failure" is real for both (no config handle → no
  redaction; no registry → FR-005's raw-id fallback, which is specified only in prose).
- **Recommendation**: Add two short Integration Boundaries entries, each with an explicit
  dependency-absent behaviour, matching the pattern used for `DelegateTool` and `PlanEngine`.

---

#### [R2-MIN-009] A Clarifications answer lists the argument set without `include_drafts`

- **Lens**: Inconsistency
- **Affected section**: Clarifications, 2026-07-27: *"What are the tool's input parameters? → A: FR-002 — `kind`, `status`, `include_terminal`, `limit`."*
- **Description**: `include_drafts` was added by rev 2 and the clarification was not updated. Minor,
  but this spec's clarifications are treated as decisions of record.
- **Recommendation**: Add `include_drafts`.

---

#### [R2-MIN-010] `intentionally_stopped` is required on every row but its derivation is defined for only two of the three kinds

- **Lens**: Incompleteness
- **Affected section**: FR-003 (required row fields) vs. FR-006 (derivation)
- **Description**: FR-003 makes `intentionally_stopped` a required field on every row. FR-006 derives
  it *"**only** from the closed portion of each kind's reason field — `session.LifecycleCancelled`,
  `plan.FailedReasonStoppedByUser`"* — two kinds. `task.Status` has no `cancelled` value and
  `task.Task` has no reason field, so a deliberately stopped task is indistinguishable from a crashed
  one and will report `intentionally_stopped=false`. That is a wrong value, not a missing one, and it
  produces exactly the harm FR-006 was written to prevent (an agent re-dispatching work someone
  deliberately stopped) for one third of the roster.
- **Recommendation**: State it explicitly in FR-006: *"`task` rows always report
  `intentionally_stopped=false`; the task model carries no stop-intent signal. This is a known blind
  spot, not a derivation."* Add a dataset row so the gap is visible rather than inferred, and
  consider omitting the field for `task` rows instead of asserting a value that may be false.

---

### Observations

#### [R2-OBS-001] The spec declares itself not ready by its own gate

- **Lens**: —
- **Affected section**: Ambiguity Warnings, "Gate status"
- **Suggestion**: *"Items 2 and 3 change observable behaviour and should be answered before
  implementation starts"* — bound values and the `native_status` separator format are both still
  open, and item 2's stakes rose when the reorder was withdrawn. Resolve 2 and 3 in the same pass as
  the CRITICALs; they are cheap operator decisions, not analysis.

---

#### [R2-OBS-002] A read-only listing tool now changes seven packages and adds four new public APIs plus a cache and a scan governor

- **Lens**: Overcomplexity
- **Affected section**: FR-013, FR-014, FR-026, FR-027, FR-028, FR-029, FR-032(c),(d)
- **Suggestion**: The store-side work (a disk field, two filter fields, three lenient siblings, a
  delegate batch accessor, a plan-engine snapshot) is independently useful and independently
  reviewable; the tool is a thin consumer of it. Consider splitting into two waves — "job-visibility
  substrate" and "`list_jobs`" — so the substrate can land and soak while the tool's remaining
  design questions (bounds, pagination, memo) are settled. Two of the CRITICALs here are in the
  tool half only.

---

#### [R2-OBS-003] `workspace_id` on every row is redundant in the common case

- **Lens**: Overcomplexity
- **Affected section**: FR-003, FR-009
- **Suggestion**: When `workspace_scoped=true`, every row's `workspace_id` is the same value the
  envelope already carries. On a spec that deletes zero-valued counters to save caller context,
  emitting a constant per row is inconsistent. Emit `workspace_id` per row **only** when
  `workspace_scoped=false`.

---

#### [R2-OBS-004] The `relation` field carries information for one kind out of three

- **Lens**: Overcomplexity
- **Affected section**: FR-010
- **Suggestion**: `plan` is always `runs`, `subagent` is always `dispatched`; only `task` varies.
  This is fine and probably worth keeping for uniformity — but say so in FR-010, so a future reader
  does not conclude the constant values are a bug.

---

## Structural Integrity

### Variant A: Plan-Spec Format

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | **PASS** | 8 stories, 40 acceptance scenarios |
| Every acceptance scenario has BDD scenarios | **FAIL** | US-8 AS-4 (operator documentation content) has none — R2-MAJ-010. The same check failed in rev 1 at US-6 AS-3, which rev 2 fixed; the two user stories rev 2 *added* were not re-checked |
| Every BDD scenario has `Traces to:` reference | **PASS** | 51/51, mechanically verified; all 51 now anchor to a User Story, as claimed |
| Every BDD scenario has a test in TDD plan | **PASS** | 66 ordered tests; every test named in the matrix exists in the plan (0 dangling) |
| Every FR appears in traceability matrix | **PASS** | 36/36 (FR-001…FR-035 + FR-019a), mechanically verified |
| Every BDD scenario in traceability matrix | **PASS** | 51/51 by exact title match |
| Test datasets cover boundaries/edges/errors | **PARTIAL** | 6 datasets / 84 rows, well-constructed. Missing: stale cap snapshot (R2-CRIT-003); scan-ceiling overflow (R2-CRIT-005); `limit` interacting with the sub-bounds (R2-MAJ-001); username/agent-id collision for **tasks** (R2-CRIT-004); terminal `status` + default `include_terminal` (R2-MAJ-016); `ask` policy verdict (R2-MIN-003). One dangling scenario reference (R2-MIN-001); rows 2–6 use a retired `L` (R2-MIN-002) |
| Regression impact addressed | **PASS** | 13-row table, the strongest section of the spec; extend per R2-MAJ-015 (per-mint-site positive tests) |
| Success criteria are measurable | **PARTIAL** | 20 SCs, all quantified. SC-003 and SC-016 are unsatisfiable as written (R2-CRIT-002/003, R2-MAJ-002); SC-005's identity is unevaluable (R2-MAJ-005); SC-002's 500-row clause has no test (R2-MIN-006); SC-011's threshold false-positives (R2-MIN-007). No cost or latency criterion survives at all (SC-012's was withdrawn), so FR-032(c)(d)'s effectiveness is unmeasurable |

**Independently re-verified and correct**: the spec's mechanical completeness table. 51 scenarios /
51 `Traces to` / 51 `Category` lines; category tally 12 Happy + 2 Alternate + 19 Error + 18 Edge =
51; 36 FRs defined and 36 in the matrix; 66 ordered tests; 84 dataset rows (10/15/14/20/13/12); zero
matrix-named tests missing from the TDD plan; zero scenarios missing from the matrix by exact title.
Rev 2's correction of rev 1's false "verified mechanically" claim is itself accurate.

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Cache correctness | No test that a memo hit respects a *changed* argument set or a *changed* workspace scope; `TestListJobs_MemoTTL` tests only same-args repetition | R2-CRIT-001; *Repeated calls under a per-principal memo stay honest*; *Roster at scale* |
| Cost-bound behaviour | FR-032(d) has no test at any level; the ceiling has no value to test against | R2-CRIT-005 |
| Snapshot staleness | FR-029's third omission trigger (stale) is untested; only `unreliable` and `absent` are covered | R2-CRIT-003; dataset *Store failure modes* rows 11–12 |
| Bound interaction | No test combines a caller-supplied `limit` with the per-group sub-bounds | R2-MAJ-001; SC-016 |
| Terminal actionability | No test asserts `actionable` for terminal **plan/task** rows | R2-MAJ-006 |
| Cross-namespace ownership | No task-side equivalent of dataset row 6's username/agent-id collision | R2-CRIT-004 |
| Snapshot identity | No test asserts the cap snapshot equals the number `admitLocked` computes | R2-MAJ-009; SC-013 |
| Description size | `TestListJobs_DescriptionContract` asserts presence of ≥6 clauses, never total length | R2-MAJ-014 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Calling principal | Username colliding with an agent id, **task** kind | Add a row mirroring row 6: standalone task with `created_by="mia"` (a human), caller = agent `mia` → row MUST NOT appear |
| Store failure modes | Stale-but-reliable snapshot | Add: `observedAt` older than the staleness bound → both cap fields absent (and a companion for a **stopped** engine, whose disposition R2-CRIT-003 requires deciding) |
| Store failure modes | Scan ceiling exceeded | Add: 10 000 records with a 1 000-record ceiling → rows bounded, `scan_truncated` reported, and state what happens to `terminal_suppressed` |
| Bounds and truncation | `limit` below the sum of populated sub-bounds | Add: `limit=30` against 25 queued / 25 running / 3 blocked → assert blocked rows survive |
| Bounds and truncation | Multi-byte label at the **byte** size bound | Add an explicit 4-byte-rune row and an ASCII mirror, per R2-MAJ-005 |
| Tool-policy resolution | `ask` verdict | Add: global `list_jobs: ask` → stated behaviour |
| Plan / Subagent status | Unmapped native state | Both existing rows (plan 11, subagent 10) need a defined output value and a correct `Traces to` |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Calling-principal resolution | ok | ok | ok | ok | ok | ok | FR-008 fail-closed is correct and well-covered (dataset rows 2–4, SC-004) |
| Workspace resolution | ok | ok | ok | **risk** | ok | ok | Fails open by design and is now labelled (`workspace_scoped`) — acceptable, **but** the memo re-opens it silently (R2-CRIT-001) |
| Per-principal memo (new) | ok | ok | **risk** | **risk** | **risk** | ok | Unkeyed → cross-workspace disclosure (R2-CRIT-001); undefined audit on hit; does not bound cost since args vary |
| Task ownership predicate | **risk** | ok | ok | **risk** | ok | ok | `CreatedBy` is username-namespaced on the REST path — a colliding username impersonates an agent for attribution purposes (R2-CRIT-004) |
| Row `label` | ok | ok | ok | ok | ok | ok | Redact-then-truncate, byte-correct `FilterMinLength` gate — correct as of rev 2 |
| Row `native_status` | ok | ok | ok | ok | **risk** | ok | Redaction closed by FR-019; the size bound is unevaluable (R2-MAJ-005) |
| Cross-kind store reads | ok | ok | **risk** | ok | **risk** | ok | Audit is at Debug (R2-MAJ-011); the only cost control (FR-032d) is unspecified and untested (R2-CRIT-005) |
| Delegate session index | ok | ok | ok | ok | ok | ok | FR-028's one-acquisition budget is the right control and is tested (SC-019) |
| PlanEngine cap snapshot | ok | **risk** | ok | ok | **risk** | ok | Refresh locking unspecified — either new contention in the dispatch loop or a divergent parallel count (R2-MAJ-009) |
| Delegation mint (write path) | ok | **risk** | ok | ok | **risk** | ok | Fail-closed guard can halt delegation with no flag or fallback (R2-MAJ-015) |
| Tool-policy grant | ok | ok | ok | ok | ok | ok | Four sites correct; god-mode modelled; kill switch documented. `ask` unmodelled (R2-MIN-003) |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. **What is the memo's key?** Principal alone, as written, is both a scoping leak and a correctness
   break. If the key includes the arguments, the memo no longer bounds cost — so what is it for?
2. **What is the cap snapshot's staleness bound, and what does a stopped engine report?** The
   stopped engine is the case the field exists for, and it is the case staleness suppresses.
3. **What is the per-call scan ceiling's default, its config key, and which counter absorbs the
   overflow?** And when it is hit, is the roster still allowed to claim exact counts?
4. **Is `omitted` keyed by kind or by status group?** Three requirements say one thing and the only
   value-asserting test says the other.
5. **Which single field is always present?** FR-033 requires exactly one and names none.
6. **How does a caller reach job #26 in a group?** There is no offset, cursor or label filter, and
   FR-002 forbids adding one.
7. **Why `started_at` DESC?** Truncating oldest-first drops precisely the work US-1 is about.
8. **Is `Task.CreatedBy` an agent id or a username?** It is both, in this tree, today.
9. **What does `list_jobs(status="failed")` return by default?** Nothing — is that intended?
10. **What happens when a native state maps to no normalized value?** Two dataset rows require an
    "unknown marker" that FR-006's exhaustive vocabulary forbids.
11. **If the mint refuses (FR-015), what does the calling agent see, and does the parent turn fail?**
    And how does an operator disable the guard in an incident?
12. **What is the maximum acceptable `Description()` length**, given it is resident in every agent's
    prompt on every request and six requirements mandate content in it?
13. **Does `ask` mean anything for this tool?** An `ask` verdict on an autonomous background recovery
    call is a hang, not a prompt.

---

## Verdict Rationale

**BLOCK.** Rev 2 is a substantial improvement — the evidence discipline is real, the nine ADR
corrections hold, the mechanical completeness claims now verify, and the three rev-1 CRITICALs are
genuinely closed. But four of the five new CRITICALs live inside requirements rev 2 *added*, and two
of them (R2-CRIT-002, R2-CRIT-003) each independently delete the mechanism of a P0 acceptance
scenario, so the spec now specifies code that cannot pass its own tests. R2-CRIT-001 turns a
performance optimisation into a cross-workspace disclosure against the P0 scoping control, and
R2-CRIT-005 reopens the CRIT-003 dishonest-roster defect through the requirement added to bound cost.
R2-CRIT-004 is the most serious in isolation because it is a live code fact, not an internal
contradiction: `Task.CreatedBy` is written as a username on the REST path in this tree, and FR-010
uses it as an agent-ownership predicate.

The pattern worth naming: every one of the new CRITICALs is a **rev-2 requirement that was not
cross-checked against the requirements it interacts with**. FR-033 was written against the response
payload and not checked against US-2; FR-029's staleness clause was written against the unreliable
case and not checked against the stopped-engine case; FR-032(c)(d) were adopted from the review's
suggested fixes verbatim without deriving their keys, values or interactions; FR-010's task predicate
was chosen without applying C4's own test. Rev 3 needs an interaction pass over the new FRs, not more
evidence work — the evidence work on the **inherited** claims (C1–C9) is done and is good.

One qualification on that praise, because it bears on how rev 3 should proceed: the evidence
discipline was applied rigorously to the ADR's claims and **less rigorously to rev 2's own new
ones**. R2-MAJ-009 is the clearest case — FR-029's twice-stated cost justification ("the scan the
snapshot needs is already happening") carries a `[VERIFIED:]` tag on a real line range, but the
inference drawn from it is wrong: `computeActiveLocked` is reachable only from `Admit` under
`pe.mu`, has no call site in `Tick`, and performs its own second store scan. A correct citation
supporting an incorrect conclusion is the failure mode this spec's own ⚠️ blocks were invented to
catch, and it survived into the requirement text. Apply the same standard to FR-026…FR-035 that was
applied to C1–C9.

None of this is unfixable and none of it requires re-opening the ADR. The five CRITICALs and the
first eight MAJORs are a focused revision.

### Recommended Next Actions

- [ ] Define the memo key exhaustively, or drop the memo — R2-CRIT-001
- [ ] Remove `cap_active`/`cap_max` from FR-033's omit-when-zero enumeration — R2-CRIT-002
- [ ] State the cap-snapshot staleness bound and decide the stopped-engine disposition — R2-CRIT-003
- [ ] Replace `Task.CreatedBy` with an agent-id-namespaced predicate, or state and guard the hazard; file the `list_tasks` twin — R2-CRIT-004
- [ ] Give FR-032(d) a value, a config key, a `scan_truncated` marker and a precedence over FR-017/018/031 — R2-CRIT-005
- [ ] Fix `limit` vs. the per-group sub-bounds — R2-MAJ-001
- [ ] Pick one key space for `omitted` (or emit both) — R2-MAJ-002
- [ ] Propagate FR-033 into the empty-roster scenario and Bounds dataset rows 1–3 — R2-MAJ-003
- [ ] Name the always-present field — R2-MAJ-004
- [ ] Make the response-size identity byte-based and give its constants values — R2-MAJ-005
- [ ] Make `actionable` false for terminal plan/task rows — R2-MAJ-006
- [ ] Add one bounded escape hatch (cursor or `label_contains`) or correct the scale scenario — R2-MAJ-007
- [ ] Justify or reverse `started_at DESC` for live groups — R2-MAJ-008
- [ ] Specify the snapshot publication mechanism and assert identity with `admitLocked` — R2-MAJ-009
- [ ] Add the US-8 AS-4 scenario and FR-032(d)/(e) tests; re-run the structural check over US-7/US-8 — R2-MAJ-010
- [ ] Split the audit record from the debug log line and move it off Debug — R2-MAJ-011
- [ ] Define the unmapped-native-state behaviour — R2-MAJ-012
- [ ] Add a normative *Response Shape* section with two worked examples — R2-MAJ-013
- [ ] Bound `Description()` and move operator material to the runbook — R2-MAJ-014
- [ ] Make FR-015's mint guard operable (flag + per-site positive tests) and raise its risk rating — R2-MAJ-015
- [ ] Resolve Ambiguities #2 and #3 in the same pass — R2-OBS-001
