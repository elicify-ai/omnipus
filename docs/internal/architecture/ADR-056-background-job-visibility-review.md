# Adversarial Review: ADR-056 — Unified background-job visibility for agents

**Spec reviewed**: `docs/internal/architecture/ADR-056-background-job-visibility.md`
**Review date**: 2026-07-27
**Input mode detected**: structured-spec (formal FR/NFR/D/G/R identifiers; no BDD scenarios or traceability matrix)
**Verdict**: **BLOCK**

## Executive Summary

Four CRITICAL findings. The ADR's normalized 4-value status vocabulary structurally
reproduces the exact "indistinguishable silence" failure it exists to eliminate — the two
plan conditions that mean *stuck* (`stalled`, `awaiting_owner_correction`) both collapse
into `running`, and the ADR names `stalled` in its own problem statement. Separately, the
ADR's `[ASSUMPTION]` at G2 is **false in the specific way R5 feared**: background shell
sessions carry no agent attribution at all, live only in memory, and self-delete 30 minutes
after finishing — so the `shell` kind cannot satisfy FR-2, NFR-1 or D6 as written. Two
`[FACT]`-tagged claims in §1 and NFR-2 do not survive checking against the code they cite.

| Severity | Count |
|----------|-------|
| CRITICAL | 4 |
| MAJOR | 15 |
| MINOR | 8 |
| OBSERVATION | 4 |
| **Total** | **31** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The normalized status vocabulary re-creates the failure the ADR exists to fix

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: FR-9, D3, D4; §1 "Indistinguishable silence"
- **Description**: §1 states the problem precisely — *"From outside, queued behind the cap,
  running, and **stalled** look identical."* FR-9 then fixes the normalized axis at exactly
  four values: `queued | running | failed | completed`. Against the real wire model this
  vocabulary has **no slot for stalled**. `contracts/components/schemas/Plan.yaml:80-89`
  defines `plan_phase` as `dispatching | judging | synthesizing | idle |
  awaiting_owner_correction | stalled`, orthogonal to the 5-value `state` (`:58-64`). Every
  one of those phases occurs while `state == running`, so under FR-9 they all normalize to
  `running`:
  - `stalled` — *"the plan is `running` with a NON-terminal member DAG (real work remains)
    but no member is currently dispatchable or in flight"* (`Plan.yaml:104-109`). This is
    the literal word §1 uses for the failure.
  - `awaiting_owner_correction` — the parked state that UAT 2026-07-26 found **9 of 11
    plans** sitting in (ADR-055 §1), one for 15+ minutes with progress 1/1.
  - A `running` plan paused because the *"owner agent [is] disabled mid-loop"* or the
    *"judge [is] temporarily unavailable"* (`Plan.yaml:220-224`).

  D4's confidence block lists its evidence as *"plan states (draft/approved/running/done/
  failed)"* — that is the `state` enum only. The ADR's model of plan status is one enum;
  the real model is `state` × `plan_phase` × `paused_reason`. D4 records this as *"Missing:
  The exact mapping table per kind"*, which understates it: the target vocabulary is too
  small to hold the source, so no mapping table can be written without losing the answer.

  Two supposed mitigations do not rescue it. FR-9 says the native status **MAY** be carried
  — optional, so an implementation omitting it is conformant. And D3 sorts on the
  *normalized* axis, so even with a native field a stalled plan sorts into the middle of the
  `running` block, contradicting D3's own stated rationale that *"problems surface without
  scrolling."*
- **Impact**: An agent calls the new tool on a plan parked at `awaiting_supervision` and is
  told `running`. It reports "the plan is progressing" and waits. This is the 2026-07-26 UAT
  incident verbatim, now with a tool that actively confirms the wrong conclusion — strictly
  worse than no tool, because silence at least prompts investigation.
- **Recommendation**: Widen the normalized vocabulary to include a **non-progressing but not
  terminal** value, and make the native field mandatory. Concretely: (a) change FR-9's
  vocabulary to `queued | running | attention | failed | completed`, where `attention` covers
  `plan_phase ∈ {stalled, awaiting_owner_correction/awaiting_supervision}`, any non-empty
  `paused_reason` on a `running` plan, and `blocked` tasks; (b) change FR-9's "MAY" to
  "MUST" for the native status field; (c) change D3's sort to
  `attention → queued → running → failed → completed` so the states needing a human or agent
  decision are first; (d) replace D4's "Missing: the exact mapping table" with the actual
  table, written against `state` × `plan_phase` × `paused_reason`, not against `state` alone.

---

#### [CRIT-002] The `shell` kind is not owner-attributable, not persisted, and self-deletes — FR-2, NFR-1 and D6 are all unimplementable for it

- **Lens**: Infeasibility
- **Affected section**: FR-2, NFR-1, D6, G2, R5
- **Description**: G2 records the shell registry as an open question with the assumption
  *"`groupBashSessions`-equivalent state exists server-side `[ASSUMPTION]`"*, and R5 warns
  *"if background bash sessions are not attributable to a starting agent, that kind cannot be
  owner-scoped."* The code answers all three parts, and R5's feared case is the actual case:

  1. **No agent attribution.** `pkg/tools/session.go:77-104` — `ProcessSession` carries
     `ID, PID, Command, PTY, Background, StartTime, ExitCode, Status, …, OwnerSessionID`.
     `OwnerSessionID` is documented in-place as *"the chat/transcript session ID that owns
     this background process"* — **a chat session, not an agent**. There is no `AgentID`
     field anywhere in the file. Scoping shells by `OwnerSessionID` therefore reproduces the
     *active-session-only* limitation for which Option C was rejected (§5, Option C
     "Weaknesses").
  2. **In-memory only.** `session.go:330-332` — `SessionManager` is
     `sessions map[string]*ProcessSession` with no persistence layer. Every handle is lost on
     gateway restart. NFR-1 says *"An agent that has lost an id MUST be able to recover it
     from this tool alone"*; §1's motivating trigger is *"a wake starts a fresh turn."*
     Across a restart, recovery for shells is impossible in principle.
  3. **Terminal rows self-delete on a 30-minute timer.** `session.go:344` starts a cleaner
     goroutine — *"runs every 5 minutes, cleans up sessions done for >30 minutes"* —
     implemented at `session.go:365-366` (`cutoff := time.Now().Add(-30 * time.Minute)`).
     D6 requires that *"terminal rows are bounded by an explicit `limit`, and the response
     reports how many were omitted."* The tool cannot report a count for rows that no longer
     exist. This is silent truncation — precisely what NFR-3 and D6 forbid, and the exact
     objection D6 raises against the SPA's `RECENTLY_FINISHED_CAP`.
  4. `SessionManager.List()` (`session.go:633`) returns `[]SessionInfo` for **all** sessions
     globally with no filter; the only scoping method is `KillAllForSession(sessionID)`
     (`:446`), keyed on the chat session.

  §5 recommends Option B over four kinds, and §8's roll-up calls this *"operational, not
  architectural."* It is architectural: FR-2's fourth kind requires new persisted, per-agent
  bookkeeping in `pkg/tools/session.go` that no decision in §6 covers.
- **Impact**: Implementation begins against a four-kind contract, discovers at step 2 of §9
  that shells have no owner, and either (a) fabricates the ownership link R5 explicitly warns
  against, (b) silently scopes shells by chat session — shipping a roster that is
  owner-scoped for three kinds and session-scoped for the fourth, with no field saying so, or
  (c) stalls mid-implementation. Meanwhile a shell that failed 31 minutes ago — a prime
  recovery target — is simply absent, reported as if it never existed.
- **Recommendation**: Resolve G2 **now**, before the ADR is accepted, not at §9 step 1. Then
  either: **(a)** ship three kinds (plan, subagent, task), state that shells are excluded
  because `ProcessSession` has no agent attribution and no persistence, and file the
  bookkeeping as follow-up work — the ADR's own §9.1 fallback, which the evidence now
  mandates rather than merely permits; or **(b)** keep four kinds and add explicit decisions
  covering: an `AgentID` field on `ProcessSession` set at spawn, persistence of terminal
  shell records for at least the roster's retention horizon, and removal or lengthening of
  the 30-minute cleanup for terminal records. Option (b) invalidates §1's "Blast radius: one
  new read-only tool; four store reads. No change to dispatch" — update that line.

---

#### [CRIT-003] FR-4 mandates returning the raw shell command as the label, with no redaction requirement

- **Lens**: Insecurity (Information Disclosure)
- **Affected section**: FR-4 (label table: `shell` → "the command"); FR-8; NFR-2
- **Description**: FR-4 fixes the shell row's label as *the command*. `ProcessSession.Command`
  (`pkg/tools/session.go:81`) and `SessionInfo.Command` (`:651`) hold the raw, unfiltered
  command string. Agent-issued shell commands routinely embed credentials inline —
  `curl -H "Authorization: Bearer …"`, `export TOKEN=…`, `psql postgres://user:pass@…`. The
  ADR never mentions redaction, truncation, or any egress filter.

  This project has purpose-built machinery for exactly this and applies it at named seams,
  which makes the omission a gap rather than an unavoidable risk:
  `Config.RegisterSensitiveValues` / `Config.SensitiveDataReplacer`
  (`pkg/config/security.go:44,64`) compile every resolved credential plaintext into a
  `strings.Replacer`. It is applied at the git-evidence commit seam
  (`pkg/gitevidence/repo.go:57`), the auto-commit secret scanner
  (`pkg/audit/secretscan.go:13-21`), and — the closest analogue — the cross-session content
  **egress filter** at `pkg/agent/session_messaging_wire.go:124,190`, described there as
  *"the SAME credential-store SensitiveDataReplacer the agent's own outputs obey (N-10)."*
  It is **not** a blanket filter on tool results, so a new tool gets no protection by default.
- **Impact**: An agent calls the roster and receives a row labelled
  `curl -H "Authorization: Bearer sk-live-…" https://api…`. The secret is now in the calling
  agent's context, in the persisted session JSONL (`toolVisibility.ts:8-9` confirms hidden
  tool calls are still persisted in full), and in the next LLM request body. Worst case the
  caller is a *different* agent from the one that ran the command, turning a within-agent
  secret into a cross-agent disclosure. NFR-2 ("MUST NOT be able to poison the caller's
  context") is stated but never applied to this field.
- **Recommendation**: Add an explicit decision (D7) requiring every free-form string in the
  response — shell `label` above all, plus plan/task titles and `paused_reason`/
  `failed_reason` if carried — to pass through `Config.SensitiveDataReplacer()` before
  serialization, citing `session_messaging_wire.go:190` as the precedent seam. Add a
  `maxLength` to the label in the contract schema (see CRIT-004) and state that redaction
  runs **before** truncation, so a partially truncated secret cannot survive.

---

#### [CRIT-004] D6 leaves running and queued rows unbounded, contradicting NFR-2; no response or field size limit anywhere

- **Lens**: Inconsistency / Insecurity (Denial of Service)
- **Affected section**: D6; NFR-2; NFR-4; FR-3; FR-4
- **Description**: NFR-2 states *"The response MUST NOT be able to poison the caller's
  context"* and calls FR-8 *"a hard boundary, not a default."* D6 then states *"Running and
  queued rows are always returned in full"* — an unbounded branch. Only terminal rows are
  bounded. Nothing in FR-3, FR-4 or D6 caps the row count, the total response size, or the
  length of any individual field. FR-4's shell label is a raw command, which may be a
  multi-kilobyte heredoc or generated script.

  NFR-4 bounds *cost per call* (*"reads at most four stores and performs no LLM work"*) but
  says nothing about the size of what those reads return, and nothing about call frequency —
  R2 only *"considers documenting"* that this is not a per-turn poll. Combined with G1's
  O(sessions) scan, an agent polling every turn performs a directory walk every turn.
- **Impact**: An agent that dispatched 200 background shells (a parallel fan-out — the wave
  pattern this project mandates) calls the roster and receives 200 unbounded rows, each
  labelled with a full command. The response alone can exceed the context budget, triggering
  the `windowTrim` eviction that discards *real* conversation history. The tool built to
  recover from lost context becomes the thing that destroys it — and NFR-2 is asserted as
  satisfied while the largest branch of the response is uncapped.
- **Recommendation**: Make D6's bound total, not terminal-only. Specify in the contract
  schema: a `maxLength` on `label` (256 chars is ample for recovery) and on any native-status
  string; a hard maximum row count across **all** statuses with a documented default; and a
  `truncated` object reporting omitted counts **per status bucket**, so an agent can tell
  "3 running omitted" from "40 completed omitted". State the response's maximum serialized
  size. Additionally, promote R2's *"consider documenting"* into a decision: state the
  intended call cadence in the tool description itself, since that description is the only
  thing the model actually reads.

---

### MAJOR Findings

#### [MAJ-001] §1's problem table is factually wrong about tasks — `list_tasks` already exists

- **Lens**: Incorrectness
- **Affected section**: §1 problem table, row "Task | `run_task` | No status read"; FR-2
- **Description**: The claim is false. `list_tasks` is a registered general builtin
  (`pkg/coreagent/core.go:303` in `allStaticToolNames`; `pkg/tools/task.go:27`). It is
  already owner-scoped and status-filterable: its parameters (`task.go:35-51`) are a
  **required** `role: assignee|delegator` plus optional
  `status: inbox|next|in_progress|blocked|done|failed`, and `Execute` (`task.go:53-67`)
  resolves the owner from the turn context via `ToolAgentID(ctx)`, setting
  `filter.AgentID` for `assignee` or `filter.CreatedBy` for `delegator`. The ADR's
  premise — that the task kind has no read path — is therefore wrong, which undermines the
  §5 option analysis: the marginal value of adding `task` as a fourth kind is much lower than
  stated, and the ADR never argues for it against the incumbent.

  Ironically, the *real* task-side defect supports the ADR's thesis and is not mentioned:
  `list_tasks` returns `json.Marshal(tasks)` — the **full task objects**, unbounded, no
  limit, no field projection (`task.go:71-78`). That is exactly the context firehose FR-8
  forbids.
- **Impact**: A reviewer or implementer trusting §1 builds a fourth enumerator duplicating an
  existing tool, and the agent ends up with two overlapping task-listing tools with different
  scoping semantics and no guidance on which to call.
- **Recommendation**: Correct the §1 row to *"`list_tasks` exists (role=assignee|delegator,
  optional status filter) but returns full task objects with no limit — a context hazard, and
  not unified with the other three kinds."* Then either justify the `task` row against
  `list_tasks` explicitly, or drop the kind and file a separate fix bounding `list_tasks`'
  output.

---

#### [MAJ-002] "Owner-scoped" is undefined — the Plan wire schema already carries three ownership fields

- **Lens**: Ambiguity
- **Affected section**: D2, G4, FR-1
- **Description**: G4 poses a two-way question: *"Does a plan 'belong to' the agent that
  called `execute_plan`, or to its ADR-055 `Owner`?"* The wire model already has **three**
  candidate fields, and G4 names none of them:
  - `owner_agent_id` (`Plan.yaml:154`) — *"Agent responsible for this plan — woken at
    decision points."*
  - `owner` (`Plan.yaml:244`, readOnly) — *"Username of the user who created this plan."*
  - `created_by` (`Plan.yaml:251`, readOnly) — *"Username (**or agent ID**) that created the
    plan"* — an untyped field conflating both principal kinds in one string namespace.

  All three are required (`Plan.yaml:23,25`). Worse, ADR-055 D4 proposes renaming
  `OwnerAgentID` → `Owner{Kind,ID}` — but a field literally named `owner` already exists as a
  `string`, so that is a **type change on a live wire field name**, not a rename into a free
  slot. ADR-055 never mentions `owner` or `created_by` exist; ADR-056 inherits the error and
  builds D2 on it.

  Under ADR-055 these are genuinely three different principals: D10 makes Owner settable
  during `draft`, so owner ≠ creator, and neither need be the agent that called
  `execute_plan`.
- **Impact**: Two concrete losses. (1) A plan authored by a human in the UI (`owner`/
  `created_by` = "alice") and executed by an agent appears on **no agent's roster** — the
  agent that started it and is waiting on it gets nothing, defeating FR-1 and NFR-1 for a
  routine case. (2) Two implementers reading D2 pick different fields and produce different
  rosters, with no test able to say which is right.
- **Recommendation**: Replace G4's binary question with a decision naming the exact field.
  Recommended predicate: *the agent that called `execute_plan`*, because FR-1 says "work
  **that agent started**" and NFR-1 is about recovering *the caller's* handle — which is a
  different relation from ADR-055's Owner (a notification target). If that field does not
  exist, say so and add it. Separately, flag the `owner` / `created_by` / `owner_agent_id`
  triplication back to ADR-055 before its rename lands.

---

#### [MAJ-003] The `queued` predicate is not derivable — there is no queued plan state, and the only signal is free-form English

- **Lens**: Infeasibility
- **Affected section**: FR-9, D3, D4, G5, R4
- **Description**: D3 makes `queued` the top-sorted status and D3's evidence line asserts
  plans genuinely queue. They do — but not observably. `Plan.yaml:58-64` has no `queued`
  state; a cap-waiting plan is `state: approved`, described as *"auto-advances to `running`
  on its next tick — **or** stays `approved` in a legitimate cap-waiting state when the global
  active-loop cap is full (see `paused_reason`)"* (`:69-72`). So `approved` covers both "not
  ticked yet" and "blocked behind the cap", and the discriminator is `paused_reason` — a
  **free-form string** (`:217-225`) with `example: "owner agent disabled"`, non-empty in
  three unrelated conditions: owner agent disabled mid-loop, judge temporarily unavailable,
  or cap-waiting. There is no machine-readable enum, code, or flag distinguishing them.

  Deriving `queued` as specified therefore requires parsing English prose. Nothing in D4 or
  G5 acknowledges this; G5 asks only whether queue *depth* is observable, assuming the
  boolean already is.
- **Impact**: The implementer either substring-matches `paused_reason` (fragile, breaks
  silently when the message is reworded) or maps all `approved` plans to `queued` — reporting
  a plan that will start in 200ms identically to one blocked behind a full cap, which is the
  §1 complaint restated.
- **Recommendation**: Specify the predicate explicitly, and prefer structured fields over
  prose. `active_loop` (`Plan.yaml:210`, *"True while this plan counts toward the global
  active-loop cap — iff `state == running`"*) is machine-readable. Then either (a) define
  `queued := state == approved` and accept the conflation, documenting it; or (b) add a
  machine-readable `paused_kind` enum (`cap_wait | owner_disabled | judge_unavailable`)
  alongside the prose `paused_reason` — a contract change that must precede this ADR's step 1.
  Fold R4's cap-pressure numbers `(active, maxConcurrent)` into the same decision, since
  they are the honest form of the same answer.

---

#### [MAJ-004] The option analysis missed the existing server-side `GET /activity` endpoint

- **Lens**: Incompleteness
- **Affected section**: §1 "Precedent", §5 Options A/B/C, §9 step 4
- **Description**: §1 cites as precedent only the SPA's client-side `useRunningActivity`
  hook, and Option C proposes extending *that* model. But a server-side aggregator already
  ships: `GET /activity` (`contracts/openapi.yaml:4280`) — *"Returns up to 50 activity events
  from the last 24 hours, sorted reverse-chronological. Includes session_start events from
  all agent stores and task lifecycle events."* Its `ActivityEvent` schema carries `id`,
  `type` (`session_start | task_created | task_updated`), **`agent_id`**, `agent_name`,
  `timestamp`, `summary` — an agent-attributed, cross-store, server-side aggregation.

  It is almost certainly *not* the right answer (a 24-hour window and a silent 50-cap fail D6
  for the same reason the SPA's cap does; it is an event log, not a live roster). But an ADR
  that evaluates three options and picks one must show it considered the closest existing
  thing. §9 step 4 then proposes *"Optional REST parity for the SPA"* without noticing it
  would sit directly beside `/activity` doing an overlapping job.
- **Impact**: The recommendation rests on an incomplete field. A reviewer who knows about
  `/activity` cannot tell whether it was rejected or missed — and §9 step 4 risks shipping
  two adjacent, subtly different activity REST surfaces.
- **Recommendation**: Add `GET /activity` to §5 as Option D (or as a sub-case of Option C)
  and reject it explicitly on the record — its 24h window, its silent 50-cap, and its
  event-log rather than live-roster shape. Then state in §9 step 4 whether REST parity
  extends `/activity` or adds a sibling route, and what happens to `/activity` either way.

---

#### [MAJ-005] FR-1's "every agent" contradicts the boot-re-enforced System Agent tool-policy seed

- **Lens**: Inconsistency / Insecurity (Elevation of Privilege)
- **Affected section**: FR-1; §2 Constraints, "the seeded posture is `allow` for every agent"
- **Description**: `systemAgentSeed` (`pkg/coreagent/core.go:847-859`) returns
  `denyAllThenOverride(...)`: the Judge gets exactly `read_file`, `list_directory`,
  `inspect_session` as `allow` and **every other static builtin name is an explicit `deny`**;
  any System Agent other than the Judge is all-deny. The header comment (`:828-840`) states
  this set is *"re-enforced every boot"* as tamper protection, per ADR-052 R3-2/FR-027.
  ADR-055 D1 adds PlanSupervisor as a second System Agent under the same contract.

  So FR-1's *"available to **every** agent"* and the Constraint-#6 note's *"the seeded
  posture is `allow` for every agent"* are false as written. Note the failure is **silent**,
  not loud: because `denyAllThenOverride` enumerates `allStaticToolNames`, adding a new name
  auto-generates an explicit `deny` for System Agents — Constraint #6 coverage is satisfied,
  boot does not abort, and FR-1 is simply not met. Conversely, an implementer who takes FR-1
  literally adds the tool to the override map and widens a deliberately minimal, boot-locked
  tamper surface — the risk ADR-055 R4 tracks.
- **Impact**: Either a stated functional requirement ships unmet with no failing check, or a
  security invariant established by two prior ADRs is relaxed as an unremarked side effect of
  a seeding note.
- **Recommendation**: Rewrite FR-1 as *"available to every **chat-capable** agent (core,
  Main, Subagent, subagent_3p); System Agents (Judge, PlanSupervisor) retain their
  deny-all-then-override seed unless a named case is added"*, and state which. If System
  Agents genuinely need it, say so and justify the widened surface against ADR-055 R4 rather
  than inheriting it from the word "every".

---

#### [MAJ-006] The tool is never named, and §9 lists only two of at least three hardcoded name mirrors

- **Lens**: Incompleteness
- **Affected section**: D1, §9 "Implementation order" step 3
- **Description**: The ADR describes "one tool" throughout and never gives it a name. Under
  Constraint #6 the name is not cosmetic — it is a literal string that must appear in
  multiple independently hardcoded catalogs. §9 step 3 names two (`pkg/config/defaults.go`,
  `pkg/coreagent/core.go`). There are at least three, and the code says so explicitly:
  - `pkg/coreagent/core.go:295` — `allStaticToolNames`.
  - `pkg/config/defaults.go:275-282` — the `ToolPolicies` map, commented *"a second,
    independent hardcoded literal"* because `pkg/config` cannot import `pkg/coreagent`.
  - `pkg/gateway/gateway.go:734-740` — `buildKnownBuiltinToolNames`, commented *"Mirrors
    pkg/coreagent/core.go's allStaticToolNames literal-for-literal
    (TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog enforces the two stay
    in sync)"*, with an explicit union list for tools whose implementation has not landed yet.

  This third mirror matters precisely because §9's own ordering (contracts → enumerators →
  tool) creates an interval where the name exists in the catalogs before the implementation
  registers itself.
- **Impact**: A boot-aborting coverage gap, or a failing catalog-sync test, discovered at
  integration rather than design time — for a tool nobody can even refer to unambiguously in
  review.
- **Recommendation**: Name the tool in D1 (e.g. `list_background_work`, avoiding collision
  with the existing `list_tasks` / `list_agents` semantics). Add `pkg/gateway/gateway.go`'s
  `buildKnownBuiltinToolNames` to §9 step 3 and name the sync test that will fail if it is
  missed. State whether the tool is a general builtin (`pkg/tools`) or a sysagent tool
  (`pkg/sysagent/tools`) — see MIN-003.

---

#### [MAJ-007] NFR-2's supporting evidence is misattributed — `toolVisibility` is a UI render filter, not a context-safety mechanism

- **Lens**: Incorrectness
- **Affected section**: NFR-2 `[FACT: src/lib/toolVisibility.ts, CLAUDE.md UI rules]`
- **Description**: NFR-2 argues that context safety *"is why FR-8 is a hard boundary"* and
  cites `toolVisibility.ts` as evidence that *"the existing `toolVisibility` rules hide
  `delegate` and background-`bash` chatter for exactly this reason."* The file states the
  opposite rationale in its own header (`src/lib/toolVisibility.ts:1-15`): *"This is a PURE
  UI decision — **it never touches the persisted session transcript** (JSONL on disk keeps
  every tool call untouched). It only decides whether a given tool call renders inline in the
  live/finalized chat view when 'verbose chat' is off… noisy background infra with no
  standalone meaning to **someone reading the conversation**."*

  The concern is human readability of the chat thread. It has no effect on any agent's token
  budget — hidden calls remain in full in the agent's history. Citing it as precedent for
  agent context safety inverts its meaning.
- **Impact**: NFR-2 — the justification for FR-8, one of the ADR's two hard boundaries — is
  supported by evidence that does not support it. This matters because it is the same
  reasoning gap that produced CRIT-004: the ADR believes context safety is already an
  established, precedented concern here, so it never derives an actual size budget.
- **Recommendation**: Delete the `toolVisibility.ts` citation from NFR-2 or re-label it
  `[INFERENCE]` with the correct reading (*"a UI-side analogue exists, motivated by human
  readability rather than context cost"*). Replace the missing justification with a real
  budget — see CRIT-004's recommendation.

---

#### [MAJ-008] NFR-1 and D6 are in direct conflict

- **Lens**: Inconsistency
- **Affected section**: NFR-1, D6, G3
- **Description**: NFR-1: *"An agent that has lost an id MUST be able to recover it from this
  tool **alone**."* D6: terminal rows are bounded by `limit`, with G3 left open on the default.
  A completed or failed job outside the limit is unrecoverable from this tool, so NFR-1 is
  false whenever the lost handle is terminal and beyond the cap. D6's own honesty mechanism
  (report the omitted count) tells the agent *that* something is missing but gives it no way
  to reach it. Under CRIT-002 the shell case is worse still: the row is gone after 30 minutes
  and cannot even be counted.
- **Impact**: An unsatisfiable requirement. Any test written against NFR-1 as stated fails,
  and the requirement gets quietly reinterpreted during implementation.
- **Recommendation**: Weaken NFR-1 to its achievable form — *"An agent MUST be able to recover
  the handle of any non-terminal job, and of terminal jobs within the retention/limit window,
  from this tool alone"* — and add a `cursor`/`offset` or `status` filter parameter so a
  caller hunting a specific terminal job can page past the default limit. Resolve G3's default
  in the same edit; an unresolved default is not a decision.

---

#### [MAJ-009] FR-3's `id` is unspecified per kind, so NFR-1 may not be satisfiable

- **Lens**: Ambiguity
- **Affected section**: FR-3, NFR-1, FR-4
- **Description**: FR-3 requires each row to carry `kind, id, label, status`. It never says
  *which* id. This is the load-bearing field for NFR-1 — the whole point is that the caller
  can feed it straight back into the follow-up tool — and each kind has more than one
  candidate:
  - `subagent`: `delegate status/peek/inbox` need the **session id**; FR-4 sets the *label*
    to the agent name, so an implementer could plausibly put the agent id in `id`. That row
    would be useless for recovery.
  - `shell`: `ProcessSession` has both `ID` (*"the exec-tool session-poll/read/kill
    identifier used by callers"*) and `OwnerSessionID` (the chat session) — `session.go:79,
    96-104`. Only the former works with `action=poll/read/kill`.
  - `plan` / `task`: the entity id, but the ADR does not say so.
- **Impact**: A conformant implementation can return ids that no other tool accepts, silently
  failing NFR-1 while passing every stated requirement.
- **Recommendation**: Extend FR-4's per-kind table with an `id` column stating the exact
  handle and the tool call it feeds: `plan` → plan id; `task` → task id; `subagent` →
  delegate session id (**not** agent id); `shell` → `ProcessSession.ID` (**not**
  `OwnerSessionID`). Add a stated invariant: *every `id` returned MUST be directly accepted
  by that kind's follow-up tool without transformation.*

---

#### [MAJ-010] D3 defines no secondary sort key, making order and truncation nondeterministic

- **Lens**: Incompleteness
- **Affected section**: D3, D6, R2
- **Description**: D3 specifies ordering by status bucket only. Within a bucket — the common
  case, e.g. six running subagents — the order is undefined. Since D6 truncates terminal rows
  by count, *which* rows survive truncation is also undefined, and can vary between two calls
  that return identical underlying state.
- **Impact**: Two consequences. (1) The agent cannot rely on the roster: the same job appears
  at different positions on successive calls, and a truncated terminal job may appear then
  vanish. (2) R2's context-bloat risk worsens — a re-ordered roster is a materially different
  string on every poll, so nothing dedupes or caches, and every poll costs full price.
- **Recommendation**: Add to D3: within each status bucket, sort by start time descending
  (most recent first). Every kind has the field — `ProcessSession.StartTime`
  (`session.go:83`), `Plan.started_at` (`Plan.yaml:275`), task and session timestamps. State
  that truncation drops the **oldest** terminal rows, and add `started_at` to FR-3's minimum
  row fields so the caller can see the basis of the ordering.

---

#### [MAJ-011] Partial-failure behaviour across the four store reads is unspecified

- **Lens**: Incompleteness / Inoperability
- **Affected section**: NFR-3, NFR-4, D1, §9 step 2
- **Description**: NFR-4 states the call reads four stores. Nothing says what happens when one
  read fails — a corrupt JSONL record, a permissions error, an in-flight write on Windows
  where `fileutil.WithFlock` is a documented no-op (CLAUDE.md, ADR-054 §5.1). Does the tool
  return an error, or return the other three kinds' rows? NFR-3 mandates honesty about
  *truncation* only; it says nothing about *degradation*.
- **Impact**: The likely default is a partial result presented as complete — the agent asks
  "what did I start?", the plan store read fails, it receives only shell and task rows and
  concludes its plan does not exist. That is precisely *"a false statement to hand an agent"*
  (D6's own words) and would trigger the ADR's own worst case: the agent abandons or restarts
  work that is still running.
- **Recommendation**: Extend NFR-3 to cover degradation: *"A response that could not read
  every kind MUST name the kinds it could not read."* Add a `degraded: ["plan"]` (or
  equivalent) field to the response schema and specify that a per-kind read failure is never
  silently equivalent to zero rows.

---

#### [MAJ-012] G1/R1's premise conflicts with existing per-agent partitioning and an existing per-agent session endpoint

- **Lens**: Incorrectness
- **Affected section**: G1, R1, §8 roll-up, §9 step 2
- **Description**: G1 asserts enumeration *"requires scanning session lifecycle records by
  `OwnerScopeID`; there is **no parent index**"*, costed as *"O(all sessions) against 90-day
  retention"*, and §8 elevates this to one of the two genuine unknowns. Two pieces of
  evidence sit against that framing. Agent data is already partitioned per agent on disk at
  `~/.omnipus/agents/<agentID>/` (`pkg/gateway/rest.go:1540`), and
  `GET /agents/{id}/sessions` already exists (`contracts/openapi.yaml:1683`) —
  *"Returns all sessions owned by the specified agent."* If sessions are partitioned per
  agent, the scan is O(**that agent's** sessions), which is already the owner-scoped set, and
  R1's "poor at tens of thousands" scenario largely evaporates.
- **Impact**: The ADR's headline operational risk may be substantially overstated, and §9's
  "measure before shipping" gate may be gating on a non-problem — while the enumerator design
  in §9 step 2 may reimplement an endpoint that already exists.
- **Recommendation**: Read `listAgentSessions`' implementation and state in G1 what it
  actually costs. If it is already per-agent, rewrite G1/R1 accordingly and downgrade R1;
  if it internally scans globally, say so — that is the finding, and it makes the existing
  endpoint the thing to fix rather than the thing to bypass.

---

#### [MAJ-013] FR-5's "directly triggered" is ambiguous for tasks, and may flood the roster with backlog

- **Lens**: Ambiguity
- **Affected section**: FR-5, D5, D2, D3
- **Description**: FR-5 includes tasks *"only when directly triggered — standalone tasks,
  never plan members"*, and D5 supplies the predicate `plan_id == ""`. That predicate
  distinguishes members from non-members. It does **not** answer which non-member tasks are
  "mine", and there are two incompatible readings — assigned-to-me versus created-by-me. This
  is not hypothetical: the existing `list_tasks` makes `role` a **required** parameter with
  exactly those two values, backed by two distinct filter fields (`filter.AgentID` vs
  `filter.CreatedBy`, `pkg/tools/task.go:60-66`), because the ambiguity is real and already
  known.

  A second, larger ambiguity: does the roster include the agent's whole task backlog? Tasks
  persist in lanes `inbox | next | in_progress | blocked | done | failed`. A task sitting in
  `inbox` for three weeks is not "background work I started", yet under FR-9 it normalizes to
  `queued` and under D3 sorts **above everything else**.
- **Impact**: A roster whose top rows are stale backlog items, pushing the actually-running
  work below the fold and consuming the response budget — the opposite of D3's stated goal.
- **Recommendation**: Replace `plan_id == ""` with the full predicate. Recommended:
  `plan_id == "" AND created_by == <caller> AND status ∈ {next, in_progress, blocked}` —
  i.e. tasks the caller dispatched that are actually in flight, excluding `inbox` backlog it
  never started. State explicitly whether `inbox` is in or out, and map `blocked` to the
  `attention` value from CRIT-001 rather than to `queued`.

---

#### [MAJ-014] No measurable success criteria or exit conditions anywhere in the ADR

- **Lens**: Infeasibility
- **Affected section**: whole document; G1, G3, §9
- **Description**: The ADR states no threshold that would tell anyone whether the result
  works. G1's "escape hatch" trigger is *"Acceptable at hundreds of sessions… poor at tens of
  thousands"* — a 100× band with no decision rule and no measurement method. G3 asks *"Pick a
  default `limit`"* and never picks one. NFR-4 bounds the call to "four store reads and no
  LLM work" with no latency or payload budget. There is no criterion for NFR-1 (what fraction
  of lost handles must be recoverable?) or NFR-2 (what response size counts as poisoning?).
- **Impact**: `/plan-spec` cannot derive acceptance criteria, and no test can fail. The
  "measure first" gate in §9 step 2 cannot be executed because no one knows what number would
  change the decision.
- **Recommendation**: Add a §10 with numeric criteria, at minimum: default `limit` value
  (closing G3); p95 latency budget for the call at a stated store size; maximum serialized
  response bytes; and G1's escape-hatch trigger as a concrete number (*"add a per-owner index
  when p95 enumeration exceeds Xms or session count exceeds N"*), plus how that number is to
  be observed — see MIN-006 on the absence of any instrumentation.

---

#### [MAJ-015] Hard dependency on an unaccepted ADR whose relevant decision is a flagged one-way door

- **Lens**: Inconsistency
- **Affected section**: D2, G4, §5 Option B; header "Related: ADR-055"
- **Description**: D2 defines the ADR's scoping rule by reference to *"ADR-055's `Owner`
  principal"*. ADR-055 is **Status: Proposed**, its D4 rename is tracked as R6 (*"One-way
  door: the wire rename… Reversing it after release costs another no-back-compat break"*),
  and its own §9 states grill-spec should run **before** plan-spec precisely because of it.
  ADR-056 carries no statement of what it does if ADR-055's Owner model changes or is
  rejected, no sequencing constraint between the two, and — despite sharing a date and
  deciders — never mentions ADR-055 D6's rename of `awaiting_owner_correction` →
  `awaiting_supervision`, which directly affects its own status mapping (CRIT-001).
- **Impact**: ADR-056's contract schemas (§9 step 1) could be generated against an Owner
  shape that ADR-055's grill changes, forcing a second no-back-compat wire break on a
  brand-new tool.
- **Recommendation**: Add an explicit dependency statement: ADR-056 MUST NOT reach §9 step 1
  until ADR-055 is Accepted, or D2 must be restated in terms of a field that exists today
  (see MAJ-002 — the caller of `execute_plan` is independent of the ADR-055 rename and is
  arguably the correct predicate anyway). Add ADR-055 D6's `awaiting_supervision` rename to
  D4's mapping table.

---

### MINOR Findings

#### [MIN-001] The header's evidence-level claim overstates the ADR's grounding

- **Lens**: Inconsistency
- **Affected section**: header, *"Evidence level (highest used): 1 — user input… + direct
  codebase verification"*
- **Description**: The header claims level 1 throughout, but G2 is tagged `[ASSUMPTION]`,
  §9 defers its verification, and CRIT-002 shows the assumption is false. "Highest used" is
  technically true and practically misleading — the reader cannot tell which decisions rest
  on verification and which on assumption.
- **Recommendation**: State the *lowest* level any recommended decision depends on, or add
  per-decision evidence levels. D1's own confidence block already admits *"Missing:
  Confirmation of a per-agent shell registry (G2)"* while rating itself **High** — reconcile
  the two.

---

#### [MIN-002] R4's cap-pressure fields break the row-schema uniformity that justified Option B

- **Lens**: Overcomplexity / Ambiguity
- **Affected section**: R4, G5, FR-3
- **Description**: R4 recommends including `(active, maxConcurrent)` cap pressure. Those are
  plan-only concepts, but FR-3 defines one uniform row shape and Option B's central argument
  was *"One concept."* The ADR never says whether these are row fields (null for three of
  four kinds), a response-level field, or a `plan`-variant payload.
- **Recommendation**: Put cap pressure at the **response** level, not the row level — it is a
  property of the plan engine, not of any single plan — and say so in FR-3. This also keeps
  the row schema uniform, preserving Option B's stated advantage.

---

#### [MIN-003] Tool package placement is ambiguous

- **Lens**: Ambiguity
- **Affected section**: §9 step 3, *"The tool in `pkg/tools`"*
- **Description**: The catalog splits general builtins (`pkg/tools/*.go`) from sysagent
  management tools (`pkg/sysagent/tools/*.go`) — `pkg/coreagent/core.go:271-280`. The choice
  is load-bearing: it determines `Scope()`/`Category()` and which agents see the tool by
  default, which interacts with FR-1 (MAJ-005).
- **Recommendation**: State the package, the `ToolScope`, and the `ToolCategory` in D1.
  `ScopeGeneral` is the likely fit given FR-1.

---

#### [MIN-004] ADR-053 is cited in the header for `SessionMessage`, which never appears in the body

- **Lens**: Inconsistency
- **Affected section**: header "Related" line
- **Recommendation**: Either use the reference (ADR-053's delegation/session-message model is
  relevant to the `subagent` kind's id semantics — see MAJ-009) or drop it. A dangling
  cross-reference invites readers to assume a dependency was analysed when it was not.

---

#### [MIN-005] §9 step 4 proposes retiring the SPA's ActivityPanel aggregation — scope creep

- **Lens**: Overcomplexity
- **Affected section**: §9 step 4, *"Optional REST parity for the SPA, which could then
  retire its narrower session-scoped aggregation"*
- **Description**: The ADR's own Option C analysis argues the SPA's scoping and 8-item cap are
  deliberate choices for *"a glanceable UI"* — correct for humans, wrong for agents. Step 4
  then proposes retiring that very aggregation on the strength of a tool built to the opposite
  requirements. Replacing an unbounded-by-design agent roster into a glanceable panel is a
  separate UX decision with its own trade-offs.
- **Recommendation**: Delete the "could then retire" clause, or file it as a separate
  follow-up. Keep this ADR's scope at *"one new read-only tool"*, as §1's blast-radius
  statement promises.

---

#### [MIN-006] No observability for the tool itself, and nothing emits the measurement §9 depends on

- **Lens**: Inoperability
- **Affected section**: §9 step 2 (*"measure lifecycle-record counts on a real install"*),
  R1, NFR-4
- **Description**: R1's mitigation is *"measure first; add a per-owner index only if
  measurement demands it"*, and §9 makes that a pre-implementation gate — but nothing in the
  ADR specifies a metric, log line, or counter that would produce the measurement, either
  before or after shipping. An operator whose install crosses the degradation threshold six
  months from now has no signal.
- **Recommendation**: Add to D1: emit a structured log or counter per call carrying
  enumeration duration and per-kind row counts. Note that `pkg/tools/session.go:334-341`
  documents the absence of a metrics convention in `pkg/tools` and uses a package-local
  atomic — follow that precedent rather than inventing one.

---

#### [MIN-007] The roster is a non-atomic composite across four stores; snapshot semantics are unstated

- **Lens**: Incorrectness
- **Affected section**: NFR-4, D1
- **Description**: Four sequential store reads produce four differently-timed views. A plan
  can complete between read 1 and read 4; a shell can be reaped by `cleanupOldSessions`
  mid-call. The ADR never states that the response is not a consistent snapshot. This is
  almost certainly acceptable, but it is the kind of unstated property that becomes a bug
  report.
- **Recommendation**: State it in D1: rows are point-in-time per kind, not a global snapshot;
  add a response-level `generated_at` timestamp.

---

#### [MIN-008] FR-9 says the native status "MAY" be carried; D4 and R3 treat it as required

- **Lens**: Inconsistency
- **Affected section**: FR-9 vs D4, R3
- **Description**: FR-9: *"the native per-kind status **MAY** be carried alongside."* D4:
  *"Carry the native value in a separate field so detail is not lost."* R3's entire mitigation
  is *"native status field alongside the normalized one."* A conformant implementation may
  omit the single field that mitigates R3.
- **Recommendation**: Change FR-9's "MAY" to "MUST". (This is necessary but not sufficient
  for CRIT-001, which needs the normalized vocabulary widened as well.)

---

### Observations

#### [OBS-001] `SessionInfo` is a ready-made starting point for the shell row

- **Lens**: Incompleteness
- **Affected section**: FR-3, §9 step 1
- **Suggestion**: `pkg/tools/session.go:649-655` already defines
  `SessionInfo{ID, Command, Status, PID, StartedAt}` with JSON tags — close to FR-3's row
  plus MAJ-010's `started_at`. If CRIT-002 is resolved in favour of keeping the shell kind,
  extend this type rather than defining a parallel shape (Constraint #8 makes the contract
  schema authoritative, but the mapping is worth naming in §9).

---

#### [OBS-002] `active_loop` is the machine-readable cap signal the ADR is looking for

- **Lens**: Infeasibility
- **Affected section**: G5, R4, MAJ-003
- **Suggestion**: `Plan.yaml:210-215` — `active_loop: boolean`, *"True while this plan counts
  toward the global active-loop cap — iff `state == running`."* Combined with `state`, this
  gives a structured queued/active discriminator without parsing `paused_reason` prose.

---

#### [OBS-003] `progress` distinguishes "running and advancing" from "running and stuck" for free

- **Lens**: Incompleteness
- **Affected section**: FR-3, CRIT-001
- **Suggestion**: `Plan.yaml:234-243` — `progress` is a server-computed 0-1 fraction over
  member tasks, already on the wire. Two successive roster calls showing identical `progress`
  is the cheapest possible stall signal, and it costs one field. Worth adding to FR-3 for the
  `plan` kind even after CRIT-001 is addressed.

---

#### [OBS-004] Document the intended call cadence in the tool description, not only in the ADR

- **Lens**: Inoperability
- **Affected section**: R2
- **Suggestion**: R2 suggests *"documenting that this is a recovery/checkpoint tool, not a
  per-turn poll."* The only text the model reliably reads is `Description()`. Put the cadence
  guidance there verbatim, following the pattern of `pkg/tools/shell.go:357`, whose
  description spells out the full poll/read/kill workflow inline.

---

## Structural Integrity

**Variant B — Structured Spec**

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | **FAIL** | No acceptance criteria or exit conditions anywhere. G3 explicitly leaves the default `limit` unchosen; NFR-1/2/4 have no thresholds (MAJ-014). |
| Cross-references are consistent | **FAIL** | FR-9 "MAY" vs D4/R3 "carry it" (MIN-008); NFR-1 vs D6 (MAJ-008); FR-1 vs Constraint #6 note vs `systemAgentSeed` (MAJ-005); ADR-053 cited but unused (MIN-004). |
| Scope boundaries are explicit | **PARTIAL** | §9's "Deliberately out of scope" is clear and good. But FR-2's four-kind scope is conditional on an unverified — and now falsified — assumption (CRIT-002), and §9 step 4 reaches beyond the stated blast radius (MIN-005). |
| Success criteria are measurable | **FAIL** | Zero numeric thresholds. G1's trigger is a 100× band with no decision rule (MAJ-014). |
| Error/failure scenarios addressed | **FAIL** | No partial-store-read behaviour (MAJ-011); no gateway-restart behaviour (CRIT-002); no behaviour when a kind's registry is unavailable; no oversized-response handling (CRIT-004). |
| Dependencies between requirements identified | **FAIL** | NFR-1↔D6, FR-9↔D4, FR-1↔Constraint #6 are each stated in isolation with no cross-link. The external dependency on unaccepted ADR-055 carries no sequencing constraint (MAJ-015). |
| `[FACT]` citations verify against the code | **FAIL** | 2 of the load-bearing citations do not survive checking: §1's task row (MAJ-001) and NFR-2's `toolVisibility` rationale (MAJ-007). G2's `[ASSUMPTION]` is false (CRIT-002). Citations that **did** verify: `useRunningActivity.ts:176` (`RECENTLY_FINISHED_CAP = 8`, and its active-session-only scope at `:12-15`); `rest_tasks.go:1646-1649` (`PlanID != ""` rejects member restart, supporting D5); the absence of any `plan_status` tool in `allStaticToolNames`. |

---

## Test Coverage Assessment

The ADR states no testing strategy. That is normal for an ADR, but several of its decisions
are only meaningful if a specific test exists, and those should be named before `/plan-spec`.

### Testability of requirements

| Requirement | Testable as written? | Gap |
|---|---|---|
| FR-1 "every agent" | **No** | Contradicted by `systemAgentSeed`; the set is undefined (MAJ-005). |
| FR-5 "directly triggered" | **No** | Predicate is `plan_id == ""` only; owner and lane conditions undefined (MAJ-013). |
| FR-9 normalized status | **No** | No mapping table; target vocabulary too small for the source (CRIT-001). |
| NFR-1 recoverability | **No** | Unsatisfiable for terminal-beyond-limit and for shells across restart (MAJ-008, CRIT-002). |
| NFR-2 context safety | **No** | No size budget defined (CRIT-004). |
| NFR-3 honesty | **Partial** | Truncation reporting is testable; degradation reporting is unspecified (MAJ-011). |
| NFR-4 cost | **Partial** | "Four store reads, no LLM work" is assertable; no latency or size bound. |

### Missing test categories

| Category | Gap Description | Affected requirement |
|----------|----------------|----------------------|
| Negative / failure | No test for one of four store reads failing | MAJ-011, NFR-3 |
| Boundary | No test at `limit`, `limit+1`, or zero jobs; no default to test against | D6, G3 |
| Boundary | No maximum label length, so no truncation test is derivable | CRIT-003, CRIT-004 |
| Security | No test that a caller cannot enumerate another agent's work; no test that a registered credential in a shell command is redacted | CRIT-003, ADR-055 NFR-3 |
| Concurrency | No test for a job transitioning mid-enumeration, or a shell reaped by `cleanupOldSessions` during the call | MIN-007 |
| Lifecycle | No test that a background shell handle survives (or is honestly reported as lost after) a gateway restart | CRIT-002, NFR-1 |
| Determinism | No test that two identical calls return identical ordering | MAJ-010 |
| Policy coverage | No test that the new tool name is present in all three hardcoded catalogs | MAJ-006 |
| Regression | The ADR never identifies existing behaviour that could break — `list_tasks`, `/activity`, and the SPA ActivityPanel all overlap | MAJ-001, MAJ-004, MIN-005 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| The roster tool (entry point) | **risk** | ok | **risk** | **risk** | **risk** | **risk** | S: the ADR never states the tool takes **no** caller-supplied owner/agent parameter — owner must be derived from turn context (`ToolAgentID(ctx)`, as `list_tasks` does). R: no statement that the call is audited. I/D/E: see below. |
| Plan enumerator | ok | ok | ok | **risk** | ok | ok | ADR-055 NFR-3 forbids a non-owner agent *probing the state of* a plan it does not own; ADR-056 never states it inherits that gate, and MAJ-002 leaves "owner" undefined across three fields. |
| Subagent/session enumerator | ok | ok | ok | **risk** | **risk** | ok | I: session labels and ids may cross agent boundaries if scoping is wrong. D: G1's scan on every call, with no rate limit and no cadence guidance beyond R2's "consider documenting" (CRIT-004). |
| Shell enumerator | **risk** | ok | ok | **risk** | ok | ok | S: no agent attribution exists, so any ownership claim is fabricated (CRIT-002). I: raw command labels leak credentials (CRIT-003). |
| Task enumerator | ok | ok | ok | ok | ok | ok | `list_tasks` already resolves the owner from turn context; the same pattern applies cleanly. |
| Tool-policy seeding | ok | **risk** | ok | ok | ok | **risk** | T/E: FR-1's "allow for every agent" would widen the boot-re-enforced System Agent deny-all seed protecting the Judge and (per ADR-055) PlanSupervisor (MAJ-005). |

**Legend**: risk = identified threat not mitigated in the ADR; ok = adequately addressed or not applicable.

---

## Unasked Questions

1. What is the tool's **name**? Nothing downstream — contracts, three policy catalogs, tests
   — can proceed without it.
2. Does the tool accept **any** caller-supplied scoping parameter? If yes, what prevents agent
   A from enumerating agent B's work, given ADR-055 NFR-3 forbids exactly that for plans?
3. When a background shell's owning **chat session** differs from the agent that spawned it
   (a delegated sub-turn), whose roster does it appear on?
4. What does the roster show after a **gateway restart**? Shells are in-memory only; plans are
   boot-swept; sessions are on disk. Three different answers, none stated.
5. Is a plan parked at `awaiting_supervision` (ADR-055 D6) `running`, `attention`, or
   something else? This is the single most important row the tool will ever return.
6. Is a plan/task that was **stopped by the user** `failed` or `completed`? Neither is
   honest — `rest_tasks.go:1656` shows the state is `status=failed, cancel_reason=stopped_by_user`.
7. What is the default `limit` (G3), and what happens when a caller passes `limit=0` or a
   value larger than the total?
8. Does the roster include tasks sitting in the `inbox` lane that the agent never started?
9. If FR-7 excludes cost, how does an agent learn a plan is about to fail from
   `judge_rounds`/`bounds` exhaustion — the very outcome it is polling for?
10. Does this tool call itself get written to the session transcript and count against the
    caller's own context on every subsequent turn? If so, R2's accretion risk is larger than
    stated.
11. What is the relationship between this tool and `GET /activity`, and does §9 step 4 extend
    it or duplicate it?
12. If ADR-055 is not accepted, or its `Owner{Kind,ID}` shape changes under grill, what
    happens to D2?

---

## Verdict Rationale

**BLOCK.** The four CRITICAL findings are not polish items — two of them go to whether the
design achieves its stated purpose at all. **CRIT-001** is the most serious: the ADR names
`stalled` as one of three indistinguishable states in its problem statement, then specifies a
four-value status vocabulary with no slot for it, so `stalled`, `awaiting_owner_correction`
and paused-mid-run all normalize to `running`. The tool would confidently report *"running"*
for the exact plan condition that produced the 2026-07-26 UAT failure — worse than the silence
it replaces, because it manufactures false confidence. **CRIT-002** falsifies the ADR's own
`[ASSUMPTION]` at G2 in the direction R5 feared: `ProcessSession` carries a chat-session id
rather than an agent id, `SessionManager` is an unpersisted in-memory map, and terminal
sessions are deleted 30 minutes after finishing — so FR-2's fourth kind, NFR-1's recoverability
guarantee and D6's explicit-truncation promise are each unimplementable for shells. §8 rates
this *"operational, not architectural"*; it is architectural, and it changes §1's stated blast
radius. **CRIT-003** and **CRIT-004** are the security and context-budget consequences of
requirements written without size or redaction bounds, in a design whose two hard boundaries
are context safety and honesty.

Beyond those, the evidence base needs repair before the ADR can carry the weight §8 assigns it:
two `[FACT]`-tagged claims fail verification (**MAJ-001** — `list_tasks` already does what the
task kind proposes; **MAJ-007** — `toolVisibility` is a UI render filter, cited as though it
were a context-safety precedent), the option analysis missed an existing server-side
`/activity` endpoint (**MAJ-004**), the headline operational risk R1 may rest on a premise the
codebase contradicts (**MAJ-012**), and the scoping rule that defines the entire feature is
undefined across three existing wire fields (**MAJ-002**). Structurally, all six adapted
integrity checks fail or partially fail; most consequentially there are **no measurable success
criteria anywhere** (**MAJ-014**), so `/plan-spec` has nothing to derive acceptance criteria
from.

The recommended shape — one unified, owner-scoped, read-only roster — is not what is being
blocked. Option B is well-argued against A and C, D5's reuse of the existing
`requirePlanExecuting` predicate is sound and verified, and D6's rejection of the SPA's silent
cap is exactly right. What must change before implementation is the status vocabulary, the
kind coverage, and the numbers.

### Recommended Next Actions

- [ ] Widen the normalized status vocabulary and make the native field mandatory; write the
      real mapping table against `state` × `plan_phase` × `paused_reason` — **CRIT-001**,
      MIN-008, MAJ-003
- [ ] Decide the shell kind now, on the evidence: three kinds plus follow-up, or four kinds
      plus explicit decisions for agent attribution, persistence, and the 30-minute reaper —
      **CRIT-002**, MAJ-009
- [ ] Add a redaction decision routing every free-form string through
      `Config.SensitiveDataReplacer()` before serialization — **CRIT-003**
- [ ] Bound the whole response: total row cap, per-field `maxLength`, per-bucket truncation
      counts, maximum serialized size — **CRIT-004**, MAJ-008, MAJ-010
- [ ] Correct the two failed `[FACT]` citations and add `GET /activity` to the option
      analysis — **MAJ-001**, **MAJ-004**, **MAJ-007**
- [ ] Name the tool; add `pkg/gateway/gateway.go`'s `buildKnownBuiltinToolNames` to §9 step 3;
      restate FR-1 to exclude System Agents — **MAJ-006**, **MAJ-005**
- [ ] Define the ownership predicate against a field that exists today — **MAJ-002**, MAJ-013
- [ ] Add a §10 of numeric success criteria, including the default `limit` (closing G3) and
      G1's escape-hatch trigger as a number — **MAJ-014**, MIN-006
- [ ] Specify partial-failure and post-restart behaviour — **MAJ-011**, MIN-007
- [ ] State the sequencing constraint against unaccepted ADR-055 — **MAJ-015**
