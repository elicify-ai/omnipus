# ADR-056: `list_jobs` — unified background-job visibility for agents

- **Status:** Proposed (v2 — revised after `/grill-spec` BLOCK, 31 findings)
- **Date:** 2026-07-27
- **Related:** [ADR-055](ADR-055-plan-supervisor.md) (PlanSupervisor); [ADR-053](ADR-053-unified-goal-plan-subagent.md)
- **Deciders:** Operator (Daniel Piatkowski); architecture input: Albert
- **Evidence level (highest used):** 1 — operator decisions + direct codebase verification

> **v2 changelog.** v1 was blocked. The corrections that changed the design:
> the **`shell` kind cannot ship** — background shells carry no agent id, are in-memory
> only, and self-delete (CRIT-002, independently confirmed twice); the normalized status
> vocabulary had **no slot for "stuck"**, re-creating the very ambiguity the ADR exists to
> remove (CRIT-001); `list_tasks` **already exists** and v1's problem table said otherwise
> (MAJ-001); `GET /activity` **already exists** and was missing from the option analysis
> (MAJ-004); and `queued` is **not derivable** as v1 assumed (MAJ-003). v2 ships **three
> kinds**, names the tool, and bounds every list.

---

## 1. Problem Understanding

An agent can start background work and then loses sight of it. Two concrete failure modes:

**Lost handles.** Seven of the nine `delegate` actions require a `session_id` the agent
must already hold `[FACT: delegate.go — inbox, inbox_ack, steer, respond, cancel,
follow_up, peek]`. If the agent loses an id — context trimmed, or a wake starts a fresh
turn — the work becomes unreachable: still running, still consuming, no handle.

**Indistinguishable silence.** Plan execution is genuinely asynchronous and admission-capped:
`PlanEngine.Start` runs `runTickLoop()` on a ticker plus a conditional `runEventLoop()`,
and admission goes through `Admit`/`resolveGlobalCap` (default cap **16**, shared across
plans, `/goal` and `/loop`) `[FACT: plan_engine.go:544, 623, 637, 2182, 2248;
config/planning.go:17]`. A plan may legitimately sit doing nothing.

### What already exists `[v2 — corrects v1]`

v1's problem table was wrong in two places, and omitted a third surface:

| Surface | Reality |
|---|---|
| `list_tasks` | **Exists** (`pkg/tools/task.go:27`) and is **already owner-scoped** via a required `role=assignee\|delegator`, plus a status filter `[FACT]`. v1 said "No status read." Its real defect is an unbounded `json.Marshal` of all matches. |
| `delegate status` **without** `session_id` | **Already enumerates** subagents (`delegate.go:1343-1358`) `[FACT]`. Two limits: it reads the **in-memory** task map (lost on restart) and scopes by origin channel/chat, not by owner. |
| `GET /activity` | **Exists** (`contracts/openapi.yaml:4280`) — a server-side cross-store aggregator returning ≤50 events from the last 24h `[FACT]`. It is **event**-shaped (what happened), not **state**-shaped (what is running now). |

So this ADR is narrower than v1 implied: it unifies and makes durable what is
partially available, rather than filling a total void.

**Blast radius:** one new read-only tool over three existing stores. No change to dispatch
or any existing tool.

---

## 2. Extracted Requirements

### Functional

- **FR-1** One read-only tool, **`list_jobs`**, listing background work the calling agent
  started. `[FACT: operator; name assigned in v2 — closes MAJ-006]`
- **FR-2** Kinds: **`plan`, `subagent`, `task`**. The `shell` kind is **deferred** — see D7.
  `[revised in v2 — CRIT-002]`
- **FR-3** Each row carries `kind`, `id`, `label`, `status`, `native_status`, `started_at`.
  The `id` is the handle that kind's other tools accept: plan → `plan_id`, task →
  `task_id`, subagent → `session_id`. `[v2 — closes MAJ-009]`
- **FR-4** Labels: plan → title; task → title; subagent → agent name. `[FACT: operator]`
- **FR-5** Tasks: **standalone only** (`plan_id == ""`), and only those the agent actually
  started or that are still live — not its whole authored backlog. `[FACT: operator, scoped in v2 — MAJ-013]`
- **FR-6** Sort: `queued` → `running` → `blocked` → `failed` → `completed`; secondary key
  `started_at` **descending**. `[FACT: operator; `blocked` + tiebreak added in v2 — CRIT-001, MAJ-010]`
- **FR-7** No spend/token cost. `[FACT: operator]`
- **FR-8** Summary only — never child transcripts, member output, or shell stdout.
- **FR-9** `status` is normalized across kinds; `native_status` carrying the kind's own
  value is **REQUIRED**, not optional. `[strengthened in v2 — CRIT-001]`
- **FR-10** Every list is bounded and every omission is reported. `[v2 — CRIT-004]`
- **FR-11** A per-kind read failure yields an explicit error entry for that kind, never a
  silent omission. `[v2 — MAJ-011]`

### Non-Functional

- **NFR-1 (recoverability)** An agent that has lost a handle for **live** work MUST be able
  to recover it from `list_jobs` alone. Terminal work is best-effort (see D6/MAJ-008).
- **NFR-2 (context safety)** The response MUST be bounded in rows *and* field lengths.
  *(v1 cited `toolVisibility.ts` here; that citation was wrong — its own header says it is
  "a PURE UI decision" motivated by human readability, not agent context cost
  `[FACT: grill-verified]`. The requirement stands on its own.)*
- **NFR-3 (honesty)** Truncation MUST be reported with a count. A dropped row is precisely
  the failure the caller is hunting.
- **NFR-4 (cost)** One call reads at most three stores, performs no LLM work, and does not
  re-read whole session files where avoidable (see R1).

### Constraints

- **Constraint #6:** a builtin tool needs an explicit policy entry. An agent with **no
  per-agent policy map** inherits the permissive **global** default (the resolver returns
  the global when only the agent side is empty; it denies only when both are)
  `[FACT — verified in pkg/tools/compositor.go + pkg/config/defaults.go]`. See ADR-055 D8,
  which now states the same thing — earlier revisions of these two ADRs disagreed.
- **Constraint #8:** contracts first, then `scripts/gen-contracts.sh`, artifacts committed
  atomically.

---

## 3. Gaps and Ambiguities

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve |
|---|---|---|---|---|
| G1 | Cost of enumerating subagents durably | `LifecycleStore.List` is O(sessions × lines): it scans the dir and `tail`-reads **every line** of each JSONL `[FACT]` | **Accepted** — operator chose to ship all three kinds regardless of scan cost | Add a per-owner index only if it becomes a real problem |
| G2 | Session lifecycle records are **never swept** — `storage.retention.session_days` covers transcripts only `[FACT]` | G1 degrades without bound over time | Unbounded growth is a pre-existing defect, not this ADR's | File separately; it makes G1 worse every day |
| G3 | Default `limit` for terminal rows | Unbounded growth vs. losing the failure sought | Live rows always full; terminal default 20 | Pick during /plan-spec |
| G4 | Two subagent status vocabularies exist: durable 8-state (`queued`…`timed_out`) and legacy in-memory 4-state, with a `cancelled`/`canceled` spelling split `[FACT]` | The normalized mapping must handle both | Map from the durable set; treat legacy as fallback | Confirm which is authoritative per row |
| G5 | Does `list_jobs` supersede `delegate status`-without-id? | Two enumeration paths with different scoping | Keep both; document the difference | Decide during /plan-spec |

---

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Recovers lost handles for live work | 30% | The unreachable-work failure mode |
| Distinguishes queued / running / stuck | 25% | v1 failed this (CRIT-001) |
| Context safety and boundedness | 20% | A firehose is worse than no tool |
| Ships only what is actually derivable | 15% | v1 specified an unimplementable kind |
| Consistency with existing surfaces | 10% | `list_tasks`, `delegate status`, `GET /activity` |

---

## 5. Option Analysis

### Option A — Per-kind tools (`plan_status`, `delegate list`, …)

| Dimension | Assessment |
|---|---|
| Strengths | Each shaped to its kind; no normalization |
| Weaknesses | The agent must already know *which kind* it lost — the one thing it cannot know when the handle is gone |
| Risks | Divergent scoping per tool, as `delegate status` already shows |
| Complexity | Higher total surface |
| Cost | N × contract + policy |
| Operational | N capability decisions per agent |

### Option B — One unified `list_jobs` over three kinds *(recommended)*

| Dimension | Assessment |
|---|---|
| Strengths | One concept; solves lost-handle recovery; one policy entry; absorbs `delegate status`'s enumeration into a durable, owner-scoped form |
| Weaknesses | Needs a normalized status vocabulary (FR-9) and must reconcile two subagent vocabularies (G4) |
| Risks | Normalization hiding nuance — mitigated by REQUIRED `native_status` |
| Complexity | Medium |
| Cost | 1 × contract + policy |
| Operational | One capability decision |

### Option C — Extend `GET /activity` `[v2 — closes MAJ-004]`

| Dimension | Assessment |
|---|---|
| Strengths | **Already exists** server-side and already aggregates across stores `[FACT: openapi.yaml:4280]`; no new aggregation layer |
| Weaknesses | **Event**-shaped, not state-shaped: ≤50 events from the last 24h, reverse-chronological. A plan running for 30 h with no recent event does not appear — the exact case the ADR must cover. Not agent-scoped. |
| Risks | Retrofitting state semantics onto an event feed produces a surface that is neither |
| Complexity | Low to call, high to re-shape |
| Cost | Low |
| Operational | Would need agent scoping and a state projection — at which point it is Option B behind an existing URL |

### Option D — Extend the SPA's ActivityPanel model verbatim

| Dimension | Assessment |
|---|---|
| Strengths | Zero new concepts |
| Weaknesses | Scoped to the **active session** and silently caps at **8** recently-finished `[FACT: RECENTLY_FINISHED_CAP]`. A plan outlives its session, and a silent cap deletes the answer |
| Risks | Inherits limits designed for glanceability, not recovery |
| Complexity | Low |
| Cost | Low |
| Operational | Needs rescoping anyway ⇒ Option B |

---

## 6. Recommended Architecture

**Option B** — `list_jobs`, three kinds, owner-scoped, bounded.

### D1 — Normalized status includes a "stuck" slot `[v2 — closes CRIT-001]`

v1 fixed the vocabulary at `queued|running|failed|completed`. That has **no slot for
stuck** — and `plan_phase` (`stalled`, `awaiting_owner_correction`) is **orthogonal to
`state`** and occurs while `state == running` `[FACT: Plan.yaml]`. So every stuck plan
would have normalized to `running`, re-creating the exact ambiguity §1 names. v1's D4
evidence line cited only the `state` enum, which is how the gap was missed.

v2 vocabulary: **`queued | running | blocked | failed | completed`**, where `blocked`
means *live but unable to progress without intervention*.

Mapping (illustrative; finalize in /plan-spec):

| Kind | → `blocked` when |
|---|---|
| plan | `plan_phase ∈ {stalled, awaiting_owner_correction}`, or `paused_reason != ""` |
| task | `status == blocked` (unmet dependency) |
| subagent | lifecycle `needs_input` or `paused` |

`native_status` is **REQUIRED** (FR-9), so nuance is never lost — a plan `failed` for
`stopped_by_user` and one for `judge_rounds_exhausted` remain distinguishable.

```
CONFIDENCE: High
  Basis         : Closes the ADR's own stated failure mode, which v1 structurally could not express.
  Evidence      : Plan.yaml plan_phase orthogonal to state; task StatusBlocked; lifecycle needs_input/paused.
  Missing       : The exhaustive per-kind mapping table.
  Would improve : Enumerating every terminal and stuck state per kind during /plan-spec.
```

### D2 — `queued` is derived from `state == approved` `[v2 — closes MAJ-003]`

v1 assumed a queued signal existed. It does not: when the cap is full the engine **only
logs** — no record write, no event, no phase change `[FACT: plan_engine.go:743-748]`. The
contract's claim that `paused_reason` covers cap-waiting is **unimplemented**: the only
paused reason ever written is `owner_disabled` `[FACT]`.

But `queued` **is** derivable without engine changes: `plan.StateApproved`'s own comment is
*"ready to run (or cap-waiting)"* `[FACT: plan.go:72]`. An `approved` plan has not begun
dispatching, so **`approved` → `queued`** is exact for the roster's purpose.

Optionally enrich with cap pressure: `Admit` already returns `(ok, active, maxConcurrent)`
`[FACT]`, so "queued behind 16 active" costs nothing extra.

```
CONFIDENCE: High
  Basis         : Uses an existing, documented state rather than requiring new engine bookkeeping.
  Evidence      : plan.go:72 comment; plan_engine.go:743-748 log-only cap branch; Admit's signature.
  Missing       : Whether to surface cap pressure in v1 of the tool.
  Would improve : Including it — the data is free.
```

### D3 — Sort `queued → running → blocked → failed → completed`, tiebreak `started_at` DESC

Operator-confirmed ordering, extended with `blocked` (D1) and a secondary key. Without a
tiebreak, order and truncation are nondeterministic across calls `[MAJ-010]` — which would
make a bounded list unreliable precisely when it matters.

```
CONFIDENCE: High
  Basis         : Operator-confirmed; the tiebreak makes bounded output reproducible.
  Evidence      : n/a — design choice.
  Missing       : None material.
  Would improve : n/a
```

### D4 — Scope: work this agent started `[FACT: operator]`

Not workspace-wide — that leaks other agents' work and is unbounded.

Per kind: plan → the plan's `owner` (ADR-055 D2); task → `CreatedBy`, already supported by
`task.Filter` and used by `list_tasks` `[FACT]`; subagent → the durable lifecycle
parent linkage.

**Caveat `[MAJ-002 / MAJ-012]`:** `CreatedBy` is a **mixed namespace** — agent ids on the
tool path, usernames on the REST path `[FACT]`. Owner-scoping works but the comparison must
be namespace-aware, not a bare string match, or an agent named like a user could collide.

```
CONFIDENCE: Medium-High
  Basis         : Two of three kinds have cheap, existing owner filters; the third is the cost driver (G1).
  Evidence      : task.Filter.CreatedBy; ADR-055 D2 owner field; LifecycleStore.List has no parent filter.
  Missing       : Namespace disambiguation for CreatedBy.
  Would improve : A kind prefix or explicit principal type.
```

### D5 — Tasks: standalone, and live-or-recent only `[FACT: operator, scoped in v2 — MAJ-013]`

`plan_id == ""` — the same predicate `requirePlanExecuting` uses `[FACT:
task_executor.go:2005-2007]`, and consistent with ADR-055's rule that intervention and
observation happen at plan level, never member level.

v1's "directly triggered" was ambiguous: read as "every task this agent created", a busy
agent's backlog would flood the roster and bury live work. v2 scopes to tasks that are
**non-terminal**, plus terminal ones within the D6 bound.

```
CONFIDENCE: High
  Basis         : Reuses an enforced predicate; the added scoping prevents the roster degrading into a backlog dump.
  Evidence      : requirePlanExecuting's PlanID == "" test; rest_tasks.go:1648 rejecting member-level restart.
  Missing       : None material.
  Would improve : n/a
```

### D6 — Everything is bounded; omissions are counted `[v2 — closes CRIT-004, MAJ-008]`

v1 left running and queued unbounded, contradicting its own NFR-2. v2:

- **Live rows** (`queued`/`running`/`blocked`): bounded by a generous `limit`, and any
  truncation is reported with an exact count.
- **Terminal rows**: bounded by a smaller default (G3).
- **Field lengths**: `label` truncated to a fixed maximum. Titles and commands are
  user/agent-authored and otherwise unbounded.
- **Never silent.** Do not adopt the SPA's silent `RECENTLY_FINISHED_CAP = 8` `[FACT]`.

This resolves the NFR-1 ↔ boundedness conflict `[MAJ-008]`: **recovery is guaranteed for
live work** (the case that matters — terminal work needs no handle to steer), and
best-effort for terminal work, with the count telling the caller when to narrow the query.

```
CONFIDENCE: High
  Basis         : NFR-3; a silent cap asserts "nothing else exists", which is false and actively misleads.
  Evidence      : useRunningActivity.ts RECENTLY_FINISHED_CAP and its UI rationale.
  Missing       : The default numbers (G3).
  Would improve : Observing typical job counts.
```

### D7 — The `shell` kind is DEFERRED, not shipped `[v2 — closes CRIT-002, CRIT-003]`

Background shells **cannot** be owner-attributed today. Independently confirmed twice:

- `ProcessSession` has **no agent id** `[FACT: pkg/tools/session.go:77-104]`. Its only
  owner field is `OwnerSessionID` = the **transcript** session id — and a delegated child
  **shares its parent's transcript session** `[FACT: subturn.go:970]`. So a parent and
  every subagent it spawns stamp the *same* value; the record cannot distinguish them.
- `SessionInfo` (what `List()` returns) **drops even that field** `[FACT: session.go:633-655]`.
- The registry is a process-global in-memory map with **no persistence** — every
  `session_id` becomes unresolvable after a restart `[FACT]`.
- A reaper deletes finished sessions 30 minutes after **StartTime, not completion time**
  `[FACT: session.go:349-397]`.

So FR-1 (recoverability), FR-2 and D4 are all unimplementable for shells. v1 called this
"operational, not architectural" — it is architectural.

**Additionally**, FR-4's "label = the command" would return raw command lines into the
caller's context and the persisted transcript, with **no redaction** — a credential
exfiltration path. `Config.SensitiveDataReplacer` exists and is applied at comparable
egress seams `[FACT: session_messaging_wire.go:124,190]`, just not here.

**Preconditions for adding `shell` later:** an agent id on `ProcessSession`; `SessionInfo`
carrying it; persistence across restart (or an explicit "live only" contract); a
completion-time-based reaper; and `SensitiveDataReplacer` applied to the label.

```
CONFIDENCE: High
  Basis         : Four independent structural blockers, each verified; shipping the kind would produce rows that are wrong, unrecoverable, or leak secrets.
  Evidence      : session.go:77-104, :633-655, :349-397; subturn.go:970.
  Missing       : Nothing — the deferral is unambiguous.
  Would improve : n/a
```

### D8 — Availability: all non-system agents `[v2 — closes MAJ-005]`

v1 said "every agent". That contradicts `systemAgentSeed`'s boot-re-enforced
`denyAllThenOverride` shape, under which a System Agent receives **only** its enumerated
grant `[FACT]`.

v2: seeded `allow` for all non-system agents; System Agents receive it only by explicit
enumeration. PlanSupervisor (ADR-055) is a likely candidate — but that is ADR-055's tool
grant to decide, not this ADR's.

```
CONFIDENCE: High
  Basis         : Corrects a claim that contradicted a boot-enforced tamper guard.
  Evidence      : systemAgentSeed denyAllThenOverride.
  Missing       : Whether PlanSupervisor should hold it.
  Would improve : Deciding in ADR-055's G1.
```

---

## 7. Risks and Caveats

- **R1 — Durable subagent enumeration is expensive.** `LifecycleStore.List` scans the
  directory and `tail`-reads **every line** of each session's JSONL, acquiring a per-session
  mutex each time `[FACT]`. It is O(sessions × lines), not O(sessions).
  *Mitigation:* **operator has accepted this cost** — all three kinds ship regardless.
  Measure opportunistically and add a per-owner index if it becomes a real problem; do not
  gate the feature on it. Note R2 makes this worse over time, so the index is a *when*, not
  an *if*.
- **R2 — And it degrades without bound.** Session lifecycle records are **never swept**;
  `storage.retention.session_days` covers transcripts only `[FACT]`. R1 therefore worsens
  monotonically. *Mitigation:* file as a separate defect (G2) — it is pre-existing, but this
  ADR's read cost is the first consumer to feel it.
- **R3 — Two subagent status vocabularies** (G4), including a `cancelled`/`canceled`
  spelling split `[FACT]`. *Mitigation:* normalize from the durable set; treat the legacy
  in-memory set as fallback.
- **R4 — Mixed-namespace `CreatedBy`** (D4). *Mitigation:* namespace-aware comparison.
- **R5 — Depends on an unaccepted ADR `[MAJ-015]`.** D4's plan scoping rests on ADR-055 D2
  (reuse `owner`). ADR-055 is Proposed. *Mitigation:* D2 is now the *low-risk* option there
  (reuse, not rename), so the dependency is far weaker than in v1 — but this ADR should not
  be accepted before ADR-055.
- **R6 — Two enumeration paths coexist** (G5): `list_jobs` and `delegate status`-without-id,
  with different scoping. *Mitigation:* document, or supersede the latter.

---

## 8. Confidence Assessment

| Decision | Confidence |
|---|---|
| D1 Status vocabulary with `blocked` | **High** |
| D2 `queued` derived from `approved` | **High** |
| D3 Sort + tiebreak | **High** |
| D4 Owner scoping | **Medium-High** — mixed `CreatedBy` namespace |
| D5 Standalone, live-or-recent tasks | **High** |
| D6 Bounded, counted omissions | **High** |
| D7 Defer `shell` | **High** — four verified blockers |
| D8 Non-system agents by default | **High** |
| Option B over A/C/D | **High** — C is event-shaped, D is mis-scoped, A multiplies surface |

**Roll-up:** v2 is lower-risk than v1 chiefly because it **ships less**: three kinds instead
of four, and every list bounded. The remaining Medium (D4) and the two highest risks (R1/R2)
are **cost** questions about durable subagent enumeration. The operator has accepted that
cost: **all three kinds ship together**. Staging `plan` + `task` first was considered and
rejected — `subagent` is the kind where handles are actually lost in practice, so shipping
without it would miss the primary use case.

---

## 9. Validation / Next Steps

**Resolve before implementation:**

1. **G1 / R1** — measure `LifecycleStore.List` cost at real session counts. **Not a gate**
   (the operator accepted the cost); it sizes the follow-up index and tells us how urgent
   R2's unswept-records defect is.
2. **G4** — confirm which subagent status vocabulary is authoritative per row.
3. **G3** — pick the live and terminal `limit` defaults.
4. **G5** — decide whether `list_jobs` supersedes `delegate status`-without-id.
5. **R2** — file the unswept-lifecycle-records defect separately.

**Success criteria `[v2 — closes MAJ-014]`:**

- An agent that has discarded a handle recovers it for **live** work in one call (NFR-1).
- A stalled plan appears as `blocked`, never `running` (D1 — the CRIT-001 regression test).
- A cap-queued plan appears as `queued`, distinct from `running` (D2).
- Truncation always reports a count; no silent omission (NFR-3).
- A store read failure produces an explicit per-kind error, not a short list (FR-11).
- Response size stays bounded with 500 live jobs and pathological labels (NFR-2).

**Implementation order** (Constraint #8):

1. `contracts/components/schemas/` — row + response; `scripts/gen-contracts.sh`; commit
   spec + artifacts atomically.
2. Per-kind enumerators: plan store, task store (`Filter.CreatedBy`), lifecycle records.
3. The `list_jobs` tool in `pkg/tools` + explicit policy seeding (D8). Verify the seeded
   grant, given the permissive-floor risk in `compositor.go`.
4. Optional REST parity, which could then let the SPA retire its narrower session-scoped
   aggregation.

**Deliberately out of scope:** the `shell` kind (D7, with stated preconditions), and
PlanSupervisor's *detailed* per-plan adjudication view — that is decision evidence, not a
status line, and belongs with ADR-055.

**Handoff:**

- Re-red-team: `/grill-spec docs/internal/architecture/ADR-056-background-job-visibility.md`
- Then: `/plan-spec docs/internal/architecture/ADR-056-background-job-visibility.md`
