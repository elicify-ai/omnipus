# ADR-057 UAT — Consolidated Verdict

**Build:** `acd0d0af` · **Target:** `omnipus-uat-swimlane.fly.dev` · **Date:** 2026-08-03
**Batches:** 4 parallel agents + 1 gap-closure agent (pending)

**Totals: 73 PASS · 9 FAIL · 7 BLOCKED**

| Batch | Scope | Result |
|---|---|---|
| 1 | Dispatch, Monitor, Profiles, Delegate-params | 22 PASS / 4 FAIL / 5 BLOCKED |
| 2 | Steer, Respond, Inbox, message_parent | 11 PASS / 2 FAIL |
| 3 | Cancel (soft/hard), follow_up, chat-wide Stop | 8 PASS / 2 FAIL / 1 BLOCKED |
| 4 | Plan/Task lifecycle, Background bash | 32 PASS / 1 FAIL / 1 BLOCKED |

---

## 1. Verdict on ADR-057 itself: the release's own claims HOLD

Every architectural promise of ADR-057 was independently confirmed on a live
deployment, most of them by two different agents using different instruments:

| Claim | Evidence | Batch |
|---|---|---|
| A delegated child owns its own session id and transcript | `GET /sessions/{id}` returns full metadata + ordered transcript | 1, 4 |
| Parent's transcript no longer carries child narration | direct REST read of both | 1 |
| Children are drillable through the general sessions listing | listed with `task_id` linkage, not just reachable by known id | 4 |
| Sessions list returns the paginated `{sessions, next_cursor}` envelope | direct REST | 1 |
| Fan-out honesty — dispatched count == reported count | direct REST | 1 |
| Cancel genuinely stops a child | transcript growth freezes before natural completion; hard ~2s vs soft ~4s | 3 |
| Cancel does not over-reach to siblings or parent | untouched sibling completed naturally; parent stayed usable | 3 |
| Cancel kills the child's background shells | HTTP server: `curl` 200 before → connection-refused after | 3 |
| `stop_plan` stops sub-turns AND background bash | `ps aux` over SSH before/after, twice, two plans | 4 |
| "Always Allow" survives across sibling delegations | real approval → grant → second sibling delegation → no re-prompt | 2 |

The last two matter most: they are the two silent, user-facing defects the
seven-reviewer gate found and this release fixed. Both verified working in production.

### ...but two claims do NOT hold, and one is a regression this release introduced

**An earlier draft of this document stated "no defect found in this UAT was
introduced by ADR-057." That was wrong, and is corrected here.** The gap-closure pass
established both of the following after the first four batches had reported.

- **B1 (below): the D8 grandchild-cascade claim FAILS.** ADR-057 asserts
  `delegate action=cancel` now cancels the child's whole subtree. It does not.
- **B2 (below): `follow_up` on a terminal session is a REGRESSION introduced by this
  release**, via ADR-057's own FR-096 collision guard (`e4c62fd2`, U2, 2026-08-03).

Neither was caught by CI, by the seven-reviewer gate, or by the first four UAT
batches — the first because no test exercises a real three-level cancel, the second
because the guard and its victim live in different packages owned by different units.

---

## 2. Defects found

### BLOCKER — introduced or claimed by this release

**B1 — The D8 grandchild-cascade claim FAILS. The leak ADR-057 was written to close
is still open.**
ADR-057 D8 / R-13 / AC-8 state that `delegate action=cancel` moves from cancelling
one turn to cancelling that child's whole subtree, explicitly to stop grandchildren
running on invisibly after their parent is cancelled.

Verified false against the live build. A real `jim → ray → worker` chain was built
(this chain was already permitted in the default workspace config — the earlier
BLOCKED verdict was a false negative, no config change was required). The grandchild
ran a detached background HTTP server. The middle agent (`ray`) was hard-cancelled.
The grandchild:
- kept posting new transcript messages **2m44s later**,
- reached a **natural `completed`** state, not `cancelled`,
- kept its background HTTP server serving for minutes afterwards (external `curl`).

Root cause: `executeCancel`'s background-shell kill (`pkg/tools/delegate.go:2866`)
targets only the single named session and never walks descendants; and the in-memory
live-turn cascade (`collectLiveDescendantTurnStates`, `pkg/agent/steering.go`) has a
self-documented gap for exactly this shape — a non-root cancel target with an
orphaned intermediate — which the ADR's own authors flag as **untested by its
acceptance criteria**.

*Impact:* the headline user-facing promise of the release does not work. Tokens burn
invisibly and processes survive after an operator believes they cancelled the work.

**B2 — `follow_up` on a terminal session is a no-op — a REGRESSION introduced by this
release.**
Returns a well-formed success with the correct `session_id`, and nothing runs.
Reproduced four times total across two agents, via 20–40s REST polls and two separate
live `attach_session` WS drains (60s and 70s, the latter ignoring the replay's own
`done` frame): message count frozen, the requested unique string never appears, zero
new wire activity.

Causal chain, traced in code: native `follow_up` reuses the terminal session's own id
verbatim (`pkg/tools/delegate.go:3054`), but `spawnSubTurn` unconditionally calls
`CreateSessionWithID` (`pkg/agent/subturn.go:1040-1048`), which by design refuses any
session id whose directory already exists (`pkg/session/unified_api.go:218-229`) —
**always true for a terminal session**. The spawn errors out before any turn starts.

**That refusal is ADR-057's own FR-096 collision guard, added in `e4c62fd2` (U2,
2026-08-03).** The guard is correct in itself and was verified working elsewhere in
this release; `follow_up` is simply an unconsidered casualty of it. Hypothesis H1
confirmed; H2–H5 (wrong session id, output filtered, slow-not-absent, harness error)
each explicitly rejected with evidence.

Aggravating: the failure is swallowed with **zero** operator-visible signal — not in
the child's transcript, not in the parent's chat, not in `gateway.log` (confirmed by
read-only grep on the box). This is precisely the silent-failure class ADR-057 exists
to eliminate, reintroduced by ADR-057.

### CRITICAL — pre-existing

**C2 — `message_parent(kind="question", wait=true)` never parks the session.**
Per the tool's own documentation `wait=true` should park the child in `needs_input`.
Instead the child runs a further autonomous turn and never parks. `respond()` then
correctly fails closed ("session is not parked on correlation_id …") — so the child
is left permanently stuck: not parked, not progressing, not terminal, and
unreachable via `respond()`. Reproduced twice, including once on a quiet box to rule
out the capacity confound. *(Batch 2.)*

**C3 — `delegate(action="status")` loses every subagent dispatched in a prior WS
connection.**
`pkg/gateway/websocket.go:615` mints a fresh random `chatID` per WebSocket
*connection*; `executeStatus` filters tasks by comparing it against the
`OriginChatID` stamped at dispatch (`pkg/tools/delegate.go:1629/1862`). Any
reconnect — a page refresh, a network blip, or any client opening one connection per
message — makes a live, running child report "not found", while `peek` and
`list_jobs` correctly show it alive. Isolated with paired positive/negative controls.
**Pre-dates this branch: introduced 2026-04-09 (`5fcc204a`).**
*Impact:* an orchestrator asks "is my child alive?", is told no, and may re-dispatch
duplicate work. *(Batch 1, independently observed in Batch 2.)*

### MAJOR

**M1 — `inbox_ack` reports a false success count.** Reports
`"Acknowledged N message(s)"` using the count of *requested* ids, not ids actually
found; a wholly fake message id yields "Acknowledged 1 message(s)." The underlying
ack logic works for real ids — only the reported count lies. *(Batch 2.)*

**M2 — `priority` validation, two distinct defects.**
(a) `priority=0` is silently coerced to the default on **both** the REST and tool
paths — `0` is Go's zero value, indistinguishable from "field absent", so it never
reaches the range check. (b) `priority=6` is correctly rejected by REST
(`400 must be between 1 and 5`) but **silently coerced by the `create_task` tool** —
the agent-facing path does not reach the validation the human-facing path enforces.
*(Batch 4, REST half independently reproduced by the coordinator.)*

**M3 — `label_contains` filter is non-functional** for subagent rows: it matches the
agent *name*, not the caller's custom label (`pkg/tools/list_jobs_sources.go:470-477`).
*(Batch 1.)*

**M4 — `subagent_end` reports `status:"success"` for a killed span.** The control
works; the reporting is untruthful. *(Batch 3.)*

**M5 — Background bash job ids are globally addressable**, not access-scoped to the
originating session: a root session can `poll` a delegated child's job (confirmed).
Cross-session `kill` was **not** confirmed — the job may have ended naturally.
*(Batch 4.)*

**M6 — Superseded task-retry-attempt sessions keep `status:"active"` forever.** Only
the final attempt reaches a terminal status. *(Batch 4.)*

**M7 — `create_task` rich params are unobservable:** `priority`, `stream` and
`write_set` never surface through `list_tasks`/`list_jobs`, so they cannot be
verified by a caller. *(Batch 1.)*

---

## 3. Release recommendation

**Do not merge as-is.** Two items must be resolved first:

1. **B1 (grandchild cascade)** — the release claims a behaviour it does not deliver.
   Either fix the cascade so `executeCancel` walks descendants, or amend ADR-057 D8
   and its acceptance criteria to state what actually ships. Code and spec must not
   assert opposites. Note the ADR's own authors flagged this shape as untested by
   AC-8 — the gap was visible in the source before UAT found it in production.
2. **B2 (`follow_up`)** — a regression this release introduced. The fix is narrow:
   `follow_up` must not route a terminal session id through `CreateSessionWithID`'s
   create-path collision guard. Whatever the fix, the silent-swallow must go too — a
   spawn that dies before starting should be visible somewhere.

The pre-existing defects (C1–C3, M1–M7) are not merge blockers for *this* branch, but
C3 (`delegate status` false "not found" after any reconnect) is severe enough to
warrant its own tracked fix: an orchestrator that believes its child died may
re-dispatch duplicate work.

**Still unverified:** boot-sweep crash-recovery, BLOCKED by design — testing it needs
a machine restart, prohibited here because the box has no volume and agents were
working concurrently. Worth covering before release, since it is the durability
guarantee behind plan recovery.

---

## 4. Methodology & limitations — read before trusting any UI-based claim

Two environment-level confounds were discovered mid-run and materially shaped these
results:

1. **The Playwright MCP browser is a single shared instance across all concurrent
   agents** — one process, one cookie jar, shared tabs. Agents observed each other's
   actions as if they were product behaviour: onboarding wizards advancing untouched,
   login forms self-filling, spontaneous logouts and 401s. **Every one of those reads
   as a product defect and none is.** All four agents abandoned the browser and
   rebuilt isolated REST + raw-WebSocket harnesses; every PASS/FAIL above rests on
   that isolated evidence.

2. **The session-admission soft cap is 4**, and four concurrent UAT agents saturated
   it (`{"active":4,"soft_cap":4,"message":"At capacity — rejecting new session"}`,
   `pkg/agent/loop.go:3325`). This is a consequence of the test design, not a product
   defect — four simultaneous testers is not normal usage. It initially produced a
   false "async children never continue" signal; agents re-ran the affected tests on a
   quiet box before scoring them.

The 7 BLOCKED results are deliberate. Where an agent could not separate saturation
from a genuine failure, it recorded BLOCKED rather than guessing. An honest BLOCKED
is worth more than an assumed PASS.
