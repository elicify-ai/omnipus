# Spec Grill — Findings Report

**Spec under review**: `docs/internal/specs/adr-057-session-unification-spec.md` (2298 lines)
**Source ADR**: `docs/internal/architecture/ADR-057-session-parent-child-parity.md` v4 (737 lines, Accepted)
**Reviewed**: 2026-08-03, branch `feature/plan-swimlane-board`
**Mode**: `plan-spec` (BDD scenarios, FR-xxx, SC-xxx, traceability matrix all present)
**Grill**: #1 of 2

---

## 1. Executive Summary

The spec is unusually well-cited and its "governing constraint: silent failure" framing is the right
instinct — but the discipline it prescribes (no spies, real stores, observable artefacts) is aimed at
exactly one failure shape and misses two others that this spec is riddled with: **negative/static gate
tests that pass when their search finds nothing**, and **acceptance criteria satisfied by total absence
of the behaviour under test**. Worse, one Functional Requirement (FR-031), implemented literally against
the code it cites, *causes* the silent failure its own user story exists to prevent, and one P0 acceptance
scenario (BDD-04) targets a code path that already does what the spec says it does not.

The file-ownership table — presented as "a safety mechanism, not bureaucracy" for a shared working tree
where agents have already reverted each other — is not exhaustive. Five files that FRs explicitly require
changing have **no owner at all**, and one unit is scheduled two waves before its stated dependency exists.

**Findings: 6 CRITICAL, 14 MAJOR, 5 MINOR, 2 OBSERVATION.**

**Verdict: BLOCK.**

---

## 2. Findings

### CRITICAL

---

#### C-1 — FR-031 re-keys `Inherit` in a way that makes grant inheritance a silent no-op

**Lens**: Incorrectness / Insecurity (DoS-by-hang) · **Section**: FR-031 (line 1926), US-6, W10, AC-7

FR-031 states, in full:

> `ApprovalGrantStore.Inherit`'s first argument MUST become the child's own session id.

The cited implementation (`pkg/security/approvalgrants.go:112-129`) uses **one** `sessionID` for **both**
the source lookup and the destination write:

```go
func (s *ApprovalGrantStore) Inherit(sessionID, parentAgentID, childAgentID string) {
	if s == nil || sessionID == "" || parentAgentID == "" || childAgentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	parentSet, ok := s.grants[grantKey{sessionID: sessionID, agentID: parentAgentID}]
	if !ok || len(parentSet) == 0 {
		return                                    // <-- silent, unlogged, uncounted
	}
	childKey := grantKey{sessionID: sessionID, agentID: childAgentID}
	...
}
```

Implemented literally, `Inherit(childID, parentAgentID, childAgentID)` looks up
`grants[{childID, parentAgentID}]`, which **never exists** — the parent's grants live under the *parent's*
session id. `!ok` fires, the function returns, and the doc comment at `:109-110` records that this is a
documented silent no-op ("No-op on ... or when the parent currently holds no grants for this session").

Downstream: `pkg/agent/loop.go:8617`'s `IsAllowed(ts.transcriptSessionID = childID, ...)` returns false,
the child falls through to `CheckGrantOrRequestApproval` (`:8630-8631`) and blocks on a human for up to
300 s — with the delegate span hidden from the thread (`src/lib/toolVisibility.ts:218-223`). **That is,
verbatim, the failure US-6 exists to prevent**, and FR-031 as written is its cause.

The correct change is a two-key operation (`InheritFrom(srcSessionID, srcAgentID, dstSessionID, dstAgentID)`).
No FR, BDD scenario, dataset row or test in this spec specifies it. Test #25
(`TestApprovalGrants_InheritKeyedToChildSession`, "Grant resolves under the child key, and only there")
would be written against FR-031's wording; a test that seeds the grant under the child key to begin with
passes green while production hangs. This is the spec's own m-5 "distinct ids" trap, inside the
requirement that names it.

**Fix**: Rewrite FR-031 as a source/destination pair — the grant is read under
`{parentRoutingOrSessionID, parentAgentID}` and written under `{childSessionID, childAgentID}`. Change
`Inherit`'s signature accordingly (U17 owns `approvalgrants.go`). Add an FR requiring `Inherit` to log +
count the "parent held no grants" branch so a future re-key cannot regress silently. Amend BDD-31 to
assert the grant is resolvable under the **child** key *and* was **not** present under it before the
spawn, with parent and child ids distinct.

---

#### C-2 — FR-003 / BDD-04 / test #7 target a suppression path that is already counted; the test passes against unmodified code

**Lens**: Incorrectness (factual) · **Section**: US-1 AS-4 (line 152), BDD-04 (line 730), FR-003 (line 1883), test #7

The spec asserts three times that `pkg/agent/turn.go:1296-1299` is "entirely silent" today and that the
change must make it "a counted, logged signal rather than returning silently". BDD-04's `But` clause is
literally *"the function does not return silently as it does today"*.

Verified against the tree:

```go
// pkg/agent/turn.go:1295-1298  (appendErrorTranscript)
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		return
	}
```

`abandonedWritesSuppressed` is declared at `turn.go:25`, documented at `:21-24` as backing the operator
metric `omnipus_abandoned_writes_suppressed_total`, exposed for tests at `:44`
(`AbandonedWritesSuppressed()`), and incremented at **seven** sites (`:866`, `:1097`, `:1172`, `:1226`,
`:1297`, `:1496`, `loop.go:7596`). The path is already counted. Only the log line is missing.

Consequence: **test #7 (`TestAbandonedTurn_SuppressedWriteIsCounted`, "Suppression emits a signal") is
green against the unmodified tree.** It sits inside US-1 — the P0 story the spec designates as the gate
for every other measurement ("Landing any other work item first means measuring it with a broken
instrument"). A vacuously-passing test inside the gating story is the exact defect class this document
was written to eliminate.

This also means the spec's §"Citation corrections (verified 2026-08-03)" — which claims everything
*else* was re-verified exact — did not re-verify the ADR's own W3 claim before carrying it forward.

**Fix**: Correct FR-003 to "MUST emit a WARN naming the session id and the suppression reason; the
existing `abandonedWritesSuppressed` counter is retained unchanged." Rewrite BDD-04's `But` clause and
test #7 to assert on the **log record** (the genuinely new artefact) plus a counter *delta* across the
call, not the counter's mere existence. Re-verify every other "is silent today" claim in the spec
against the tree before implementation starts — this one propagated unchecked from ADR W3.

---

#### C-3 — Eleven of 83 tests are negative/static gates that pass when their search finds nothing; the spec's three binding rules do not cover this class

**Lens**: Infeasibility / Test coverage · **Section**: "Three rules bind every test" (lines 46-52), TDD plan, SC-003

The spec's three anti-silent-failure rules address (1) spies/fakes, (2) invocation assertions, and
(3) cross-process guarantees. None of them addresses a test whose assertion is *"the search returned
zero results"*. Every such test is green when the search itself is broken — a typo'd pattern, a renamed
symbol, an un-compiled fixture, a changed file path, a parser that silently returns no nodes.

The affected tests:

| # | Test | Vacuous-pass mode |
|---|---|---|
| 3 | `TestSessionIDTypes_DoNotInterconvert` | "compile-fail fixture" — passes if the fixture is never compiled |
| 9 | `TestCacheMu_NoFilesystemInCriticalSection` | AST gate — passes if it locates zero `cacheMu` regions |
| 12 | `TestLifecycleDocComments_NoSharedParentChildClaim` | doc-truth grep — passes if the comment block moved or the file path drifted |
| 17 | `TestMetaWriters_WriterIsolationByteLevel` | passes if the "other" files were never created |
| 19 | `TestMetaDocComments_NoSingleFunnelClaim` | same as #12 |
| 27 | `TestInterruptScope_RequiredByCompiler` | same as #3 |
| 29 | `TestRoutingSessionID_ConsumerSetIsClosed` | enumerates reads and asserts none is outside the set — passes if it enumerates **zero** reads |
| 58 | `TestIsDelegateChildEntry_ZeroNonTestReferences` | grep-for-zero |
| 81 | `TestGateTestsInvertedNotDeleted` | see m-3 |
| 82 | `TestW22CommitContainsOnlyTests` | passes if the commit is not found |
| 83 | `TestAllFixturesUseDistinctParentChildIDs` | passes if it discovers no fixtures |

SC-003 has the same shape at the criterion level: `rg -n "IsDelegateChildEntry" --glob '!*_test.go'`
returning zero is satisfied by deleting `daypartition.go`.

This matters most for #29, which is the sole enforcement of FR-014 and AC-2's closed-consumer-set
property — the structural guarantee that `routingSessionID` never leaks into role-A use. A silently
empty enumeration means the entire D2 safety property is unenforced while reported green.

**Fix**: Add a fourth binding rule: **every exclusion/negative gate MUST first assert a positive
lower bound.** #29 must assert it found ≥ K reads (K = the known consumer-set size, stated in the spec)
before asserting none is outside the set. #9 must assert it located ≥ 1 `cacheMu` region. #12/#19 must
assert the target comment block was located before asserting its content. #3/#27 must be `go build`-driven
with the fixture's *absence* itself a failure. #58/SC-003 must additionally assert a positive count of
`ParentSpawnCallID` field references (proving the file and grep both work). State the expected counts in
the spec so drift is visible in review.

---

#### C-4 — The W24 throttle can be entirely non-functional with tests #20, #21, #22 and #74 all green

**Lens**: Incompleteness / Test coverage · **Section**: US-13, FR-061…FR-067, AC-22, dataset "Stats-throttle timing"

Trace the four throttle tests against a store where the **periodic flusher goroutine is never started**
and only the forced flush points work:

- **#20** (`TestStatsThrottle_NoFileWriteWithinInterval`, BDD-65, SC-023) asserts `stats.json`'s mtime and
  bytes are **unchanged** during a burst. Satisfied by never writing `stats.json` at all. ✅ green.
- **#21** (`..._ExactCountersAfterInterval`, BDD-66) asserts currency "after the flush interval elapses".
  If the test drives the flush explicitly (an injected clock plus a manual tick, which AC-22 permits —
  "with a real clock or an injected fake"), the production wiring is never exercised. ✅ green.
- **#22** (`..._ForcedFlushPoints`, BDD-67) tests exactly the four forced paths that still work. ✅ green.
- **#74** (`..._UngracefulKillBoundedLoss`, BDD-70, dataset row 7) asserts the counters are "behind by at
  most that interval's appends". Dataset row 7 is "K appends, SIGKILL before interval → behind by ≤ K" —
  which a store that has flushed **nothing** satisfies exactly. ✅ green.

So the property that actually matters — *counters converge without any external trigger* — has **zero**
coverage. Under the real production shape (a long-lived gateway that never calls `Close`), a broken
flusher means `stats.json` is stale for the process lifetime, and `ListSessions` recency (AC-22(e),
BDD-69, test #24) is correct only in-memory, which is precisely what #24 tests.

The same critique applies to BDD-65's precondition: it says "a flush interval that has just started",
which cannot be established without observing that flushes happen at all.

**Fix**: Add an FR and a test asserting the **unforced** flush: append once, take no other action, wait
> interval with the real (or advanced fake) clock, and assert `stats.json` on disk is current — with the
store never closed, no `SetMeta`, no `DeleteSession`. Add a negative-control assertion to #20 that
`stats.json` **does** become current after the interval, in the same test, so "unchanged" cannot mean
"never written". Change dataset row 7's expected value from "behind by ≤ K" to a two-sided bound:
counters must be behind by ≥ 0 and the pre-SIGKILL flushed prefix must be non-zero when the run spans
≥ 2 intervals.

---

#### C-5 — W23 + W24 relocate Alternative F's clobber from the file to `metaCache`, and nothing tests it

**Lens**: Incorrectness · **Section**: US-12, US-13, FR-054, FR-058, FR-061, Edge Cases (line 500), Ambiguity #12

The spec rejects Alternative F ("keep the counters in the fused `meta.json` while throttling them")
because "the flusher would clobber goal/loop/status or re-serialise everything" (line 517), and asserts
the interaction is structurally eliminated:

> **A `SetMeta` carrying `Status` lands between a counter bump and a flush.** Expected: structurally
> unrepresentable as a clobber — the flusher owns `stats.json`, which no other writer touches. (line 500)

That is true of the **file**. It is false of the **cache**, and the cache is where W24 puts the counters:

- FR-058 — "`metaCache` MUST continue to hold **one composed `*UnifiedMeta` clone per session**".
- FR-061 — the counter bumps "MUST become **in-memory mutations of the cached meta** under `cacheMu`".
- FR-054 — `writeMetaLocked` is replaced by four targeted writers.

Today `writeMetaLocked` (`pkg/session/unified.go:786-799`) ends with `us.metaCache[sessionID] = meta.Clone()`
— a **whole-document** cache refresh, documented at `:770-778` as "the single invalidation/update point
for every mutation path". If any of the four new targeted writers keeps that shape (the obvious
translation), a `/goal` round's writer replaces the cache entry with a `*UnifiedMeta` composed from disk
— **discarding every unflushed in-memory `Stats.*` delta**. Counters silently go backwards. AC-22(b)'s
"no lost or double-counted delta" is violated, and the only test of that property (#21) is
single-goroutine, single-writer-family, and cannot see it.

Ambiguity #12 raises *where the dirty set lives*, but not this: the shared mutable `*UnifiedMeta` is
itself the fused document, one layer up.

**Fix**: Add an FR: each of the four targeted writers MUST update **only its own field group** in the
cached `*UnifiedMeta`, and MUST NOT replace the cache entry wholesale; the composed-from-disk path
(`readMetaLocked` cache-miss) MUST NOT overwrite a cache entry that is marked dirty. Add a BDD scenario
and test: append K entries (no flush), then run a `/goal set` and a `Status` transition, then force a
flush, and assert `stats.json` equals K's exact deltas. This is the missing negative case for AC-22(b).

---

#### C-6 — `CreateSessionWithID` copying the parent's `Owner` is a two-session operation, which FR-050 forbids and which can invert against `ClearAll`

**Lens**: Infeasibility / Incorrectness · **Section**: FR-006, FR-050, W1, W15, AC-20(d)

FR-006 / W1 require the child's meta to carry the parent's `Owner` verbatim. Verified: `createSessionLocked`
(`pkg/session/unified.go:441-478`) constructs `UnifiedMeta` with **no `Owner` field** (the spec's own
citation-corrections table confirms this for `:448-460`). The value can therefore only come from reading
the **parent's** meta — inside the same operation that creates the **child's** session.

Under W15 that is one operation touching two sessions, i.e. two shards. FR-050 states:

> Lock order MUST be one-directional: `sessionLock(id)` → `cacheMu`. **Two session shards MUST NOT be
> held at once**, except by `ClearAll`/`RetentionSweep`, which MUST take every shard **in index order**.

So the spec's single most-used new operation violates its own lock-order rule. And if the constraint is
honoured by taking the two shards anyway, the acquisition order is *hash order* (`shard(child)` then
`shard(parent)`), which inverts against `ClearAll`/`RetentionSweep`'s index-order acquisition of all 64 —
the exact defect the ADR names as R-19 ("`ClearAll`/`RetentionSweep` taking shards in hash order rather
than index order is the same defect").

AC-20(d)/SC-017's `-race` run does not detect a lock-order inversion that does not happen to deadlock in
that run; Go's race detector is not a lock-order checker.

**Fix**: Add an FR specifying the two-session protocol explicitly — either (a) read the parent's `Owner`
under `sessionLock(parent)`, release it, then create the child under `sessionLock(child)` (accepting a
benign TOCTOU on a field that is immutable after creation), or (b) define a canonical acquisition order
for multi-session operations (index order, matching `ClearAll`) and state it in FR-050 as an exception
rather than a prohibition. Add a lock-order gate test that is not merely `-race`: assert the acquisition
order statically or via an instrumented lock wrapper.

---

### MAJOR

---

#### M-1 — Five files that FRs require changing have no owner in the ownership table

**Lens**: Incompleteness · **Section**: "Work Unit Decomposition & File Ownership" (lines 579-614)

The table opens with "**This table is a safety mechanism, not bureaucracy**" and Rule 2: "A unit that
needs a change in a file it does not own must request it from the owner." For the following files there
is no owner to request from, so an agent will either edit an unowned file (the precise hazard) or drop
the requirement:

| File (verified to exist) | Required by | Owner |
|---|---|---|
| `pkg/agent/external_dispatch.go` | FR-002 (and ADR W3) — MUST use the strict primitive at `:463`, `:550-555`, `:562-564` | **none** |
| `pkg/agent/approval_transcript.go` | FR-002 — MUST use the strict primitive at `:179`, `:183` | **none** |
| `pkg/agent/events.go` | FR-017 — pin `SubTurnSpawnPayload.SessionID` / `SubTurnEndPayload.SessionID`; both defined at `events.go:427` / `:446` | **none** (W21 is assigned to U7/U14, which own `subturn.go`/`delegate.go`) |
| `pkg/agent/boot_sweep.go` | FR-078, AC-19, test #65 | **none** (#65 is assigned to U13+U19, which own `lifecycle.go`, `admission.go`, `session_messaging_wire.go`) |
| `pkg/tools/message_parent.go` | FR-077 — the producer at `:640` MUST change together with the consumers | **none** |

Additionally, FR-068 requires pagination "through all four layers" and the ADR enumerates them as
`unified.go:1247`, `loop.go:5046`, `rest.go:758-812`, `src/lib/api.ts:1379-1388`. W16 is assigned to
**U12 + U18 only** (SPA + `rest.go`). The store layer (`ListSessions`, owned by the U4→U5→U6 chain) and
the loop layer (`ListAllSessions`, verified at `pkg/agent/loop.go:5046`, owned by U9) are **not assigned
W16**. Two of the four layers have no unit tasked with paginating them, so FR-068 cannot be delivered as
scoped.

**Fix**: Assign every file named in an FR to exactly one unit. Add `external_dispatch.go` +
`approval_transcript.go` to U3 or a new U3b (they are turn-adjacent transcript writers); assign
`events.go` to U9 or a new unit for W21; assign `boot_sweep.go` explicitly (U19 is the natural home);
assign `message_parent.go` to U14 alongside its consumers. Add `unified.go`'s and `loop.go`'s pagination
to the W16 row (which means U6 and U9, and therefore a wave change).

---

#### M-2 — U20 is scheduled in Wave A but depends on U18, which lands in Wave C

**Lens**: Inconsistency · **Section**: ownership table (U20 row), integration order (Wave A), cross-unit requests

- Ownership table: **U20** "Depends on: **—**", "Must NOT touch: `pkg/gateway/rest.go` — request the
  cascade-delete hook from U18".
- Cross-unit requests: "U20 | the child-uploads cascade hook invoked on session delete | **U18**".
- Integration order: U20 is in **Wave A**; U18 is in **Wave C**.

U20's acceptance property (FR-071, BDD-78, AC-12, SC-031, test #62 — "deleting a parent removes
`<home>/uploads/<id>/` for every descendant") is entirely dependent on the hook U18 installs. At Wave A
it cannot be satisfied, cannot be tested, and the "Depends on: —" column tells the implementing agent
otherwise.

**Fix**: Either move U20 to Wave D (after U18) and record `Depends on: U18`, or split it: the
`tempdir.go`/`store.go` primitive (no dependency, Wave A) versus the cascade wiring (Wave D, requires the
U18 hook). Test #62 must be assigned to whichever half owns the wiring, and its `Traces to BDD` must not
sit in Wave A.

---

#### M-3 — U7 has an undeclared dependency on U17 (the `Inherit` signature)

**Lens**: Inconsistency · **Section**: ownership table (U7, U17), cross-unit requests

U7's work items include "W10 (Inherit re-key)" and it owns `pkg/agent/subturn.go`, where the call lives
(`subturn.go:916`, verified). The `Inherit` **signature** lives in `pkg/security/approvalgrants.go`,
owned by U17 — and per C-1 above, the signature must change (a one-key `Inherit` cannot express the
re-key). U7's `Depends on` column reads "U2, U3, U5"; U17 is absent, and the pair does not appear in the
cross-unit request table.

It happens to work because U7 is Wave D and U17 is Wave B, but the dependency is unrecorded — so any
wave compaction, re-plan or retry silently breaks it, and the failure mode is a compile error at best,
a green-but-inert `Inherit` at worst.

**Fix**: Add `U17` to U7's `Depends on`. Add a cross-unit request row: "U7 | the two-key `Inherit`
signature | U17". Same treatment for U9, which does "W10 (grant read re-key)" against
`ApprovalGrants().IsAllowed` at `loop.go:8617` — U9's dependency list also omits U17.

---

#### M-4 — U21 exclusively owns the 12 gate test files, but eight other units' tests belong in exactly those files

**Lens**: Inconsistency · **Section**: ownership table (U21), TDD plan

U21 owns "the 12 named `*_test.go` files **+ any test asserting the old contract**", and Rule 1 says a
file has exactly one owner. But the TDD plan assigns new tests to other units that naturally land in
U21's files:

| Test | Assigned unit | Natural file (U21-owned) |
|---|---|---|
| #32, #33, #34 `TestSpawnSubTurn_*` | U7 | `pkg/agent/subturn_test.go` |
| #53, #54 `TestInterrupt_Subtree*` | U8 | `pkg/agent/interrupt_by_session_key_test.go` / `steering_test.go` |
| #38–#42 `TestCancel_*`, `TestOrphanWatchdog_*` | U15 | `cancel_subagent_cascade_test.go`, `cancel_orphan_delegate_test.go`, `orphan_watch_test.go` |
| #25 `TestApprovalGrants_Inherit*` | U17 | `approval_grant_delegation_test.go` |

Worse, U21 must land **last, in its own commit containing no behaviour files** (FR-073, hard ordering
rule 5) — while the units above are supposed to write their tests **before** the implementation
("Write these before the implementation code", line 1647). The two constraints are mutually exclusive
for any test that belongs in a U21 file.

**Fix**: State explicitly that every unit's new tests go in **new** files (`<subject>_adr057_test.go`),
and that U21 touches only the 12 enumerated files. Add that rule to the ownership table's Rules block, not
just to prose.

---

#### M-5 — U2's core error path is defined by U5's work, and they run concurrently in Wave B

**Lens**: Inconsistency / Ordering · **Section**: ownership table (U2, U5), Wave B, dataset row 4

`AppendTranscriptStrict`'s defining property is "returns a non-nil error for a session id with **no
`meta.json`**" (FR-001). Dataset row 4 for that primitive is "Existing session **directory** with no
`meta.json` → non-nil error", traced to *both* BDD-01 **and BDD-61**. BDD-61/FR-055 is U5's requirement
(the D11 composition asymmetry). U2's `Depends on` is "U1, U4" — U5 is absent, and both are scheduled in
**Wave B**, explicitly parallel.

So the two halves of one property are implemented concurrently by two agents with no declared
relationship, in a package where U5 is simultaneously rewriting `readMetaLocked`/`readUnifiedMeta` — the
exact functions U2's new primitive must call to decide whether the session exists.

**Fix**: Either add `U5` to U2's dependencies and move U2 to Wave C, or specify precisely which existence
predicate `AppendTranscriptStrict` calls and freeze that function's signature as a contract in the spec
(so U5 can change its internals without breaking U2). The latter is a cross-unit request row that is
currently missing.

---

#### M-6 — No FR re-keys the approval **registry**; FR-032 presupposes a change nothing requires

**Lens**: Incompleteness · **Section**: FR-031, FR-032, W10, AC-7

FR-031 covers `ApprovalGrantStore` (`pkg/security/approvalgrants.go`). The **pending-approval registry**
is a different store: `pkg/gateway/approvals.go`, with `SessionID` at `:85`, set at `:213`/`:232`, and
matched by **exact equality** at `:419`:

```go
if e.state != ApprovalStatePending || e.SessionID != sessionID {
```

FR-032 requires `cancelAllPendingForSession` to "run over the descendant set, not a single id" — which
only makes sense if each descendant's registry entries carry that descendant's own id. **No FR states
that.** W10's parenthetical "(store, registry, teardown)" in the ownership table is the only mention, and
a work-item parenthetical is not a requirement.

Second, unaddressed consequence: `tool_approval_required` is in `SESSION_SCOPED_FRAME_TYPES` (verified,
`src/store/chat.ts:1241`), so under FR-012 its `session_id` becomes the **routing** key (the chat sid)
while the registry entry is keyed by the **child** sid. The spec never specifies how the client's
approve/deny response is routed back to the child's pending entry. AC-7 tests grant inheritance and
Stop-cancellation but never the interactive approve round-trip.

**Fix**: Add an FR: the pending-approval registry entry's `SessionID` MUST be the acting (child) session
id, and the approve/deny response MUST resolve by approval id (not session id) so the routing-key change
cannot break it. Add a BDD scenario + test for a child raising a real approval that a client then
**approves** — currently only the cancel path is covered.

---

#### M-7 — AC-10 is internally contradictory and conflicts with the spec's own resolutions

**Lens**: Inconsistency / Infeasibility · **Section**: AC-10 (line 2066), Ambiguity #3, Ambiguity #4, SC-016, SC-030

AC-10 (governing text, per the preamble at line 2045) requires:

> A 24-way root fan-out while a second session streams tokens: assert the second session's inter-token
> latency stays **within a stated budget**, and that W17's gate refuses **the 25th** rather than queueing it.

Three conflicts:

1. **The cap.** "Refuses the 25th" implies cap = 24. Ambiguity #4's stated agent assumption is to
   "reuse the existing `maxConcurrent` config value", which the cited UAT
   (`max-parallel-concurrency-gap-2026-07-31.md` §G1) records as **16**. Under that assumption the gate
   refuses the **17th** and AC-10's scenario is unrunnable as written.
2. **The budget.** "A stated budget" is stated nowhere. Ambiguity #3 acknowledges this and proposes
   converting AC-10(a) to a slope assertion — but the AC preamble says "**the ADR text below governs**"
   where the spec differs. So the spec both defers to a criterion it cannot meet and proposes replacing
   it. SC-016 uses the slope; test #72 says "inter-token distribution preserved" with no threshold at all
   and is therefore not a criterion.
3. **The refusal test.** SC-030/#63 use an abstract "cap of N", which does not exercise AC-10's specific
   24/25 topology.

**Fix**: Resolve Ambiguity #4 before Wave A (it is a one-line operator answer) and rewrite AC-10's
numbers to match. Replace "within a stated budget" with the slope formulation the spec already uses for
AC-20, and record that as an explicit ADR amendment rather than a spec-level override — otherwise the
governing-text clause makes AC-10 permanently unsatisfiable. Give test #72 a concrete assertion.

---

#### M-8 — BDD-16 asserts a property that is false for at least five of its 19 rows, and contradicts the spec's own dataset

**Lens**: Incorrectness · **Section**: BDD-16 (lines 853-885), SC-006, dataset "WS frame identity stamping"

BDD-16's Given/Then is: *"Given a child turn emitting frame type `<frame_type>` ... Then `session_id`
equals the routing key **and** `producing_session_id` equals the child's own id"*, applied to all 19
types (list verified exact against `src/store/chat.ts:1236-1250`).

At least five rows cannot satisfy the Given, the Then, or both:

- `replay_message`, `replay_done` — emitted by the gateway replay path, not by a child turn.
- `session_started`, `session_close_ack` — chat-session lifecycle frames, not turn output.
- `rate_limit` — has **no `SessionID` field at all** (`pkg/agent/events.go:525-533`, acknowledged by the
  spec at line 535), and the spec's own dataset row 5 expects `producing_session_id` **absent**, directly
  contradicting BDD-16's Then.

SC-006 ("all 19 ... round-trip both ids ... with zero types unaudited") is therefore unachievable as
literally stated, yet is written as a pass/fail criterion.

**Fix**: Split BDD-16 into three outlines: (a) types a child turn genuinely emits → both ids; (b) types
where `producing_session_id` is correctly absent (root-produced / gateway-produced) → assert absence;
(c) the two known-broken types (`rate_limit`, `replay_done`) → assert the audited, documented gap, per
Ambiguity #11's "document, do not fix" resolution. Rewrite SC-006 as "all 19 types are classified into
(a)/(b)/(c) and each is asserted per its class".

---

#### M-9 — BDD-36 / test #56, the flagship AC-18(b) assertion, is satisfied by total child-transcript loss

**Lens**: Incompleteness · **Section**: BDD-36 (line 1070), test #56, SC-004, AC-18(b)

AC-18(b) and test #56 assert, structurally on the file: *"the parent's `transcript.jsonl` contains no
entry produced by the child"*. The spec explains the choice (line 519): asserted on the file "so the
property cannot be satisfied by a filter someone re-adds".

True — but it *is* satisfied by a child that wrote **nothing anywhere**. And that is the expected outcome
of the spec's own error-handling design: FR-002 requires transcript-write failures to surface as "a
counter increment plus a WARN", not a hard failure. If the child's session mint is broken (C-6's owner
copy, a shard deadlock, a `CreateSessionWithID` bug), every child write errors, gets WARN-logged, and the
turn proceeds — and #56 goes green.

The positive counterpart exists (#57, BDD-37, "all of its entries are returned") but is a **different
test in a different unit** (U18), so a partial implementation passes #56 and defers #57.

**Fix**: Merge the assertions: one test that runs one delegation and asserts, in the same run, that the
parent's `transcript.jsonl` gained zero child entries **and** that `<baseDir>/<childID>/transcript.jsonl`
gained exactly the expected non-zero count with the expected content. Make AC-18(b)'s wording require
both halves.

---

#### M-10 — FR-045's map-eviction requirement has no policy, no bound, and an unmeasurable test

**Lens**: Ambiguity / Infeasibility · **Section**: FR-045, BDD-52, test #31, AC-9

FR-045: "`t.tasks` and `t.sessionIndex` MUST have a deletion path". BDD-52: "Given N delegations
completed **and reaped** ... Then they do not retain **all N** entries."

Three problems: (1) *reaped* is undefined — no FR specifies the trigger (on-terminal? TTL? size cap?),
so two implementers build different things; (2) "do not retain all N" is satisfied by deleting exactly
one entry; (3) with no retention bound stated, the requirement cannot fail. The ADR's own framing
("both grow for the process lifetime") is a genuine unbounded-growth defect that this requirement does
not actually close.

**Fix**: Specify the eviction policy and the bound — e.g. "an entry is deleted when its task reaches a
terminal state and its last `status` read is older than T, and `len(t.tasks)` MUST NOT exceed C". Rewrite
BDD-52 to assert the map size returns to a stated bound after N ≫ C completions, and add an
`AC-9`-adjacent success criterion with a number in it.

---

#### M-11 — Seven traceability rows do not test the FR they claim to cover

**Lens**: Inconsistency · **Section**: Traceability Matrix (lines 2088-2167)

The matrix is structurally complete (every FR has a BDD and a test), which is what the completeness check
at line 2169 verifies. But several rows pair an FR with a test that does not exercise it:

| FR | Requirement | Mapped test | Gap |
|---|---|---|---|
| FR-058 | `metaCache` holds one composed clone; `GetMeta`/`ListSessions` cost nothing extra | #13 `TestReadUnifiedMeta_ComposesFourFiles` | #13 never touches the cache |
| FR-060 | No reader for a fused `meta.json`; MUST NOT modify `migrateLegacy`/`writeUnifiedMetaDirect` | #15 `TestReadUnifiedMeta_MissingMetaJSONIsError` | tests neither clause |
| FR-067 | Flush interval SHOULD be a config key with a default | #74 (SIGKILL durability) | the config-key requirement has **zero** coverage |
| FR-047 | "No requirement MAY depend on `subagent_message`/`subagent_state`" | #78 (E2E drill-down) | a single E2E cannot establish a property over all requirements |
| FR-052 | MUST NOT impose a fixed concurrency cap | #70 (slope) | a slope does not detect a cap above the tested N |
| FR-023 | MUST NOT use `OwnerScopeID`/`ParentAgentID` as the parentage edge | #10, #49 | neither asserts the negative |
| FR-072, FR-073 | W22 commit shape | mapped to **AC-8** | AC-8 is the interrupt-scope criterion; W22 has **no** AC in ADR §10. The column is fabricated rather than left blank |

**Fix**: Add real tests for FR-058 (assert a cache hit costs zero disk reads after the split), FR-060
(assert `migrateLegacy`'s bytes are unchanged — a git-level or golden gate), FR-067 (assert the config key
exists and a non-default value is honoured), FR-023 (a static gate asserting neither field appears in the
walk's code path), and FR-052 (assert throughput continues to rise past 64 concurrent sessions). Change
FR-047 into a review gate rather than an FR, or drop it. Set FR-072/073's AC column to "—" with a note
that W22 is a process item with no ADR AC.

---

#### M-12 — FR-030 requires "every descendant" but only the depth-1 case is tested outside the holdout

**Lens**: Incompleteness · **Section**: FR-030, BDD-25, test #40, SC-008, H-1

FR-030: "`descendants_canceled` ... MUST remain non-empty and **name every descendant** the Stop reached."
BDD-25 and SC-008 assert only "non-empty and contains **the child's** turn id" — one child, depth 1. The
depth-3 assertion exists **only** in H-1, which is explicitly a holdout ("MUST NOT be visible to the
implementing agent"). A holdout scenario cannot serve as acceptance evidence for an FR.

**Fix**: Add a non-holdout BDD scenario and test asserting `descendants_canceled` contains all three ids
at depth 3 (BDD-30 already builds that tree for lifecycle records — extend it to the audit entry).

---

#### M-13 — No unit owns the child-turn-terminal `CloseSession` call site that FR-033 requires

**Lens**: Incompleteness · **Section**: FR-033, W10, U11, U17

FR-033 requires "a child session MUST receive a `CloseSession` on **child-turn terminal**". Verified:
`CloseSession` is defined at `pkg/agent/session_end.go:32` (U17 ✓). Its non-test call sites are
`pkg/gateway/websocket.go:1038` (explicit user close), `pkg/agent/loop.go:1048`/`:1064` (idle sweep) and
`session_end.go:865` (bootstrap) — **none** is a child-turn terminal.

The ownership table assigns "W10 (teardown call site)" to **U11** (`pkg/gateway/websocket.go`) — the user
session-close path, not a child turn. The child's terminal path is in `pkg/agent/subturn.go` (U7) or
`turn.go` (U3), and neither lists that responsibility. The spec's own edge case at line 504 flags the
risk ("provided something actually calls `CloseSession`") and then leaves it unassigned.

**Fix**: Assign the child-terminal `CloseSession` call explicitly (U7 is the natural owner) and add it
to U7's work items. Add a cross-unit request row U7 → U17 for any signature change.

---

#### M-14 — FR-051's reconcile/snapshot split introduces an unaddressed stale-read window

**Lens**: Incorrectness · **Section**: FR-051, US-11 AS-2, BDD-54

Today `ListSessions` runs entirely under `us.mu.Lock()`, and the doc comment at
`pkg/session/unified.go:1240-1246` states the reason explicitly:

> Reconciliation requires mutating `metaCache` (via `readMetaLocked`), which needs the full write lock —
> an RLock cannot be upgraded to a Lock without risking deadlock, so the whole method runs under
> `us.mu.Lock()`.

FR-051 splits this into per-session reconcile under each session's shard, then a snapshot under
`cacheMu.RLock`. Between the reconcile pass and the snapshot, a concurrent `DeleteSession` can evict a
session whose entry the reconcile just installed (or vice versa), so `ListSessions` can return a session
that no longer exists or omit one that does. The spec states no consistency model for `ListSessions`
after striping — not in the FRs, not in Behavioral Contract, not in Edge Cases.

**Fix**: State the intended guarantee explicitly (point-in-time snapshot vs. best-effort eventual) and
add a BDD scenario for `ListSessions` concurrent with `DeleteSession`. AC-20(d)'s "neither deadlock nor
**drop a session**" hints at the property for `ClearAll`/`RetentionSweep` but says nothing about
`DeleteSession`, which is the common case.

---

### MINOR

**m-1 — AC-13 cites the wrong line for the second doc comment.** AC-13 (governing text) says
`lifecycle.go:225-228`, **`:572-575`** and `list_jobs_sources.go:311-315`. FR-022, BDD-22 and US-4 AS-5
all say **`:571-575`**. Verified: `matches` opens at `:565` and the `ParentAgentID` comment block runs
`:571-575`. The spec's own "Citation corrections" table did not catch it, and the AC preamble says the
ADR governs — so the governing text points one line past the block. *Fix*: add the row to the citation
corrections table.

**m-2 — BDD-65 / SC-023's "mtime and bytes unchanged" has no stated precondition that the file exists.**
Under W23, `stats.json` is written lazily; for a fresh session whose only activity is transcript appends,
it may not exist when the burst starts. "Unchanged" over a non-existent path is undefined and will be
implemented three different ways. *Fix*: state that the scenario requires a prior forced flush so
`stats.json` exists with known bytes, and assert on both mtime and content hash.

**m-3 — Test #81 claims an unenforceable property.** `TestGateTestsInvertedNotDeleted` is described as
"All twelve files present **and asserting the new invariant**". A Go test can verify file presence; it
cannot verify that another test's assertions encode a semantic invariant. Only the first half is real.
*Fix*: scope #81 to presence + a required marker comment (e.g. `// ADR-057-W22-inverted`) and move the
semantic check to the review gate, where FR-072 belongs.

**m-4 — Wave B runs two units inside one Go package with no symbol-collision rule.** U2 creates
`pkg/session/unified_api.go` while U5 rewrites `pkg/session/unified.go` — same package, same wave. A
package-level helper introduced by both (a `sessionDir`, a `metaPath`) is a compile break neither unit
owns. *Fix*: require every new file in a shared package to prefix its unexported helpers with the unit's
subject.

**m-5 — FR-067 is the spec's only `SHOULD` among 78 requirements**, and SC-034's gate list does not cover
it, so it is unenforced in either direction. *Fix*: promote to MUST (a config key is cheap) or move it to
Assumptions.

---

### OBSERVATION

**o-1 — The "21 units across 5 waves" figure overstates achievable parallelism.** The storage cluster is
a forced serial chain on one file (U4 → U5 → U6, all writing `pkg/session/unified.go`), U18 is gated on
U5, and U11/U12/U9 are all gated on U10. The real critical path is
`U10 → U11/U12` and `U4 → U5 → U6` / `U4 → U5 → U18`, i.e. four sequential steps regardless of how many
units exist. Consider merging U4/U5/U6 into one unit with three commits — it is the same agent doing the
same file in the same order, and the ownership overhead buys nothing.

**o-2 — W20's named-ID types are enforced at exactly one site.** With 116 non-test `transcriptSessionID`
references across 18 files, a partial conversion is the likely outcome, and nothing in the spec bounds
which references must adopt `SessionID`/`RoutingSessionID`. Test #3 proves the types don't interconvert;
it does not prove they are used. Consider an FR stating the conversion boundary explicitly (e.g. "every
field and parameter in `turnState`, `processOptions` and the `UnifiedStore` public API").

---

## 3. Structural Integrity Results (`plan-spec` mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | 18 stories, all populated |
| Every acceptance scenario has ≥1 BDD scenario | **PASS** | |
| Every BDD scenario has a `Traces to:` back-reference | **PASS** | BDD-01…BDD-87 contiguous |
| Every BDD scenario has a corresponding test | **PASS** | |
| Every FR appears in the traceability matrix | **PASS** | FR-001…FR-078, all rows present |
| Every BDD scenario appears in the matrix | **PASS** | |
| **Matrix rows test what their FR claims** | **FAIL** | M-11 — 7 rows do not; one AC column is fabricated |
| Test datasets cover boundary/edge/error | **PASS (with gaps)** | 6 datasets, well-formed; row 7 of the throttle dataset is trivially satisfiable (C-4) |
| Regression impact explicitly addressed | **PASS** | Strongest section in the document — 13 preserved behaviours, 12 inversions, 8 "mistaken for breakage" rows |
| Success criteria measurable, no subjective language | **FAIL** | SC-006 unachievable as stated (M-8); AC-10's "stated budget" is unstated (M-7); FR-045/BDD-52 unmeasurable (M-10) |
| **File-ownership table is disjoint and exhaustive** | **FAIL** | M-1 (5 unowned files + 2 unassigned pagination layers), M-4 (test-file collision) |
| **Wave ordering honours stated dependencies** | **FAIL** | M-2 (U20 two waves early), M-3/M-5 (undeclared deps) |
| Hard ordering W15→W23→W24 honoured | **PASS** | U4 (Wave A) → U5 (Wave B) → U6 (Wave C); correctly serialised, correctly justified |
| U10 (contracts) before U9/U11/U12 | **PASS** | U10 Wave A; U9 Wave D; U11/U12 Wave C |
| U5 before U18 | **PASS** | U5 Wave B, U18 Wave C |
| U21 last, own commit | **PASS** as stated, **FAIL** in practice | M-4 — collides with the "tests first" instruction |
| All 24 ADR work items W1…W24 covered | **PARTIAL** | see §5 |

---

## 4. Test Coverage Assessment

**What is strong.** The four-tier hierarchy (Unit / Integration / Cross-process / E2E) is correctly
matched to the properties; copying `pkg/entity/store_crossprocess_test.go` for durability is the right
in-house precedent and it was verified to exist. The regression section is genuinely good — it names 12
tests to invert with anchors, and 8 behaviours a reviewer would otherwise call a bug. The "distinct
parent/child ids" corollary is the correct lesson from `message_parent_real_context_test.go:16-17`.

**What is missing.**

1. **No test for the unforced periodic flush** (C-4) — the single load-bearing property of W24.
2. **No concurrent-writer-family test for the counters** (C-5) — the cache-level clobber.
3. **No positive lower bounds on 11 negative gates** (C-3).
4. **No test for the approve round-trip** under the new routing key (M-6); only cancel is covered.
5. **No multi-session lock-order test** (C-6); `-race` is not a lock-order checker.
6. **`ListSessions` × `DeleteSession`** concurrency is untested (M-14).
7. **Idempotency**: `follow_up` reuses `childID` verbatim (`subturn.go:1115-1135`) — generation N+2 against
   a session that already has two generations of history is not covered by #68 or BDD-83.
8. **AC-19 / test #65** is the only restart test, and it asserts the reconcile plus "no orphan-directory
   write". The second half is a negative gate with the C-3 vacuity problem.
9. **`ClearAll`/`RetentionSweep` index-order acquisition** (FR-050, R-19) is folded into #73's `-race`
   run; the ADR calls hash-order acquisition "the same defect", but no test asserts the order.

---

## 5. ADR Work-Item Completeness (W1 … W24)

| W | Covered? | Verdict |
|---|---|---|
| W1 | Yes | FR-005…FR-010; two units (U2 store, U7 agent); the owner-copy mechanism is under-specified (C-6) |
| W2 | Yes, thinly | Split three ways (U5/U10/U12), one FR (FR-008), one BDD, one test. The spec is honest that v4's filter deletion removed one of three justifications. **What is not stated**: which of R-9 (listing) or W19 (drill-down) actually *consumes* `ParentSessionID` — no FR requires either to read it, so W2 could ship as a write-only field and every test would pass. Add a consumer requirement or cut it. |
| W3 | **Partial** | FR-001…FR-003. Two of the six named conversion targets (`external_dispatch.go`, `approval_transcript.go`) have no owner (M-1); FR-003's premise is factually wrong (C-2) |
| W4 | Yes | FR-011, FR-014…FR-016; the closed consumer set's only enforcement is a vacuity-prone gate (C-3, #29) |
| W5 | Yes | FR-012, FR-013, FR-018; BDD-16 is false for 5 of 19 rows (M-8) |
| W6 | Yes | FR-019, FR-020, FR-022, FR-023, FR-078. FR-078 (boot sweep) is assigned to W6+W17 but its file is unowned (M-1) |
| W7 | Yes | FR-021; clean |
| W8 | Yes | FR-024…FR-026, FR-030; FR-030's "every descendant" is only holdout-tested (M-12) |
| W9 | Yes | FR-027…FR-029; clean |
| W10 | **Partial** | FR-031 is actively wrong (C-1); the registry re-key is unrequired (M-6); the child-terminal call site is unowned (M-13) |
| W11 | Yes | FR-034…FR-038. Split U5 (predicate) / U18 (4 sites) across waves B and C. The spec flags this itself ("must land in the same integration window or the tree does not compile") — but the waves put them **two apart**, and no mechanism enforces the window. The intermediate state (predicate deleted, call sites still calling it) does not compile. **This is a real ordering hazard, not just a note.** |
| W12 | Yes | FR-039, FR-040. The coverage map also files FR-076/FR-077 (message ceiling, inbox routing) under W12, which is the ownership walk — those are ADR-053 consequences with no natural work item; the placement is a filing convenience presented as coverage |
| W13 | Yes | FR-041, FR-042; clean. Ambiguity #5 correctly flags the behaviour change |
| W14 | Yes | FR-043…FR-045; FR-045 unmeasurable (M-10) |
| W15 | Yes | FR-048…FR-052; FR-050 forbids an operation W1 requires (C-6); FR-051 introduces an unspecified stale window (M-14) |
| W16 | **Partial** | FR-068 requires four layers; only two have owners (M-1) |
| W17 | Yes, with an unresolved number | FR-069, FR-070. The spec is admirably honest ("required *by* this ADR rather than *of* it", UAT-derived not code-derived), but the cap value is Ambiguity #4 and AC-10 hard-codes an incompatible one (M-7) |
| W18 | Yes | FR-071; wave-ordered wrong (M-2) |
| W19 | Yes | FR-046, FR-047; FR-047 is not testable as an FR (M-11) |
| W20 | Yes | FR-004; enforced at one site only (o-2) |
| W21 | **Partial** | FR-017; `pkg/agent/events.go`, where both payload types live, is unowned (M-1) |
| W22 | Yes | FR-072…FR-074. Correctly flagged as process-not-behaviour. Its ADR-AC mapping is fabricated (M-11) and it collides with the tests-first instruction (M-4) |
| W23 | Yes | FR-053…FR-060; the cache-level clobber is unaddressed (C-5) |
| W24 | Yes | FR-061…FR-067; the load-bearing property is untested (C-4) |

**Summary**: all 24 items are nominally covered. **W3, W10, W16 and W21 are nominally-covered-but-actually-unspecified**
(a named requirement with no owning unit, or a requirement whose stated form is wrong). **W2 and W11** are
covered but structurally fragile — W2 as a possible write-only field, W11 as a two-wave split of a
must-land-together change.

---

## 6. STRIDE Threat Summary

| Component | Threat | Status in spec |
|---|---|---|
| `delegate` gated actions (6 sites) | **Elevation of privilege** — today a child can address siblings/cousins (subtree-wide equality at `delegate.go:1973-1979`) | **Addressed** — FR-039/FR-040, ancestor walk, 9-row topology dataset. Good coverage |
| Ancestor walk | **DoS** — an unbounded or cyclic chain | **Addressed** — depth-bounded, BDD-43, dataset row 7. H-5 covers a broken link (holdout) |
| Root-level delegation | **DoS** — ungated fan-out becomes fsync-bound session creates | **Addressed** — W17/FR-069, but the cap value is unresolved (M-7) |
| Approval grants | **Elevation of privilege** — a grant outliving the child, or a child inheriting more than the parent held | Partly addressed — FR-031 is wrong (C-1); the union semantics for self-delegation are noted in Boundary conditions but untested |
| Pending-approval registry | **Repudiation / availability** — an approval that cannot be routed or cancelled | **Not addressed** (M-6) |
| Session id → filesystem path | **Tampering** (path traversal) | **Addressed** — `validateSessionID` preserved, dataset rows 5/6, BDD-79, `tempdir.go`'s `("",false)` contract retained |
| Child session id collision with an existing directory | **Tampering / information disclosure** — a child adopting another session's transcript, owner and stats | Only in **holdout H-7**. No FR, no BDD, no test in the visible plan. Given `CreateSessionWithID` uses the *exact* supplied id and `createSessionLocked` does `MkdirAll` (idempotent, no existence check at `:463`), this is a live hazard with zero non-holdout coverage |
| Owner inheritance | **Elevation of privilege** — a child running under a different principal | Addressed by FR-006/AC-2; BDD-08 covers the empty-owner case as a log-only signal |
| Audit trail | **Repudiation** — a child's action attributed to the chat | Addressed as regression row 6 ("audit `session_id` becomes the acting session id"), but no FR states it and no test asserts it — it appears only in the regression dataset |
| Uploads directory | **Information disclosure** (retained media) | Addressed — FR-071, but wave-ordered wrong (M-2) |

**Note on H-7**: the holdout mechanism is sound in principle, but using it for the *only* coverage of a
tampering hazard means the implementing agent has no requirement to defend against it. Consider promoting
H-7's property (not its scenario) to an FR.

---

## 7. Unasked Questions

1. **Does `CreateSessionWithID` fail or succeed when the directory already exists?** `createSessionLocked`
   calls `os.MkdirAll` (`:463`), which is idempotent and silent. Nothing in FR-005 says. This is the H-7
   hazard, and it is exactly the "silent create" shape the spec's own governing constraint describes.
2. **What is `Inherit`'s new signature?** (C-1.) The spec asserts a re-key without specifying the operation.
3. **When a child's transcript write fails, does the child turn continue?** FR-002 says "counter + WARN".
   So a child whose session mint failed runs to completion producing no record — and AC-18(b) passes (M-9).
   Should a strict-primitive failure abort the turn?
4. **What is `ListSessions`'s consistency model after striping?** (M-14.)
5. **Who calls `CloseSession` for a child, and on which terminal states** (completed / cancelled /
   failed / abandoned)? (M-13.)
6. **What happens to a child's `stats.json` deltas when the child is `DeleteSession`d mid-flush?**
   FR-064 lists `DeleteSession` as a forced flush point — flush-then-delete or delete-then-drop?
7. **How does the SPA route an approve/deny response back to a child's pending entry** when the frame's
   `session_id` is now the chat's? (M-6.)
8. **Does `producing_session_id` need to survive replay?** BDD-15 asserts span/step correlation after a
   reconnect, but nothing says whether replayed frames carry `producing_session_id` or reconstruct it.
9. **What bounds the number of `sessionLock` shards held by `ClearAll`?** 64 mutexes held simultaneously
   while a `RetentionSweep` walks the whole tree (`retention_sweep.go:35`) is a full store stall — is that
   acceptable, and for how long?
10. **Is `metaCache` bounded?** W24 makes it the authoritative counter home; a long-lived gateway with
    24-way fan-outs now caches a `*UnifiedMeta` per *child* session forever. `cacheLoadFailures`'s
    accepted limitation (Ambiguity #8) is preserved, but nothing addresses cache growth under D1.
11. **What is the expected count for each negative gate?** (C-3.) Without stated expected counts, review
    cannot tell a working gate from a broken one.
12. **Does W2's `ParentSessionID` have a reader?** No FR requires anything to read it (§5, W2 row).

---

## 8. Verdict

**BLOCK** — 6 CRITICAL findings.

Two of them (C-1, C-2) are the spec asserting something about the code that the code contradicts, inside
the P0 stories that gate everything else. Three (C-3, C-4, C-5) are the silent-failure discipline failing
on its own terms — the document correctly identifies that this migration's failures are success-shaped,
then writes ~11 tests that are green on an empty search and 4 tests that are green on a dead flusher. One
(C-6) is a lock-order rule that forbids the change's own central operation.

The 14 MAJOR findings cluster around the file-ownership table, which is the mechanism protecting a shared
working tree where agents have already reverted each other. It is not exhaustive (5 unowned files, 2
unassigned layers), not acyclic across waves (U20 before U18), and not consistent with the TDD plan
(U21's exclusive test-file ownership vs. tests-first).

**Fix order** (they are not independent):

1. **C-1** — resolve `Inherit`'s signature before U17 or U7 is dispatched. It is a two-line ADR-level
   clarification and it changes two units' scopes.
2. **C-2** — re-verify every "is silent today" / "no such call exists today" claim against the tree. C-2
   proves at least one propagated unchecked from the ADR.
3. **M-1, M-2, M-3, M-4, M-5, M-13** — repair the ownership table and the wave graph together. Nothing
   should be dispatched until every FR-named file has exactly one owner and every dependency is declared.
4. **C-3, C-4, C-5, C-6** — these are spec text changes (new FRs, new BDD scenarios, positive lower bounds
   on gates), not implementation changes. They can land in the same revision pass.
5. **M-7** — Ambiguity #4 (the cap) is a one-line operator answer that unblocks AC-10.

---

**Review written to**: `docs/internal/specs/adr-057-session-unification-spec-review.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/adr-057-session-unification-spec.md docs/internal/specs/adr-057-session-unification-spec-review.md
```
