# Adversarial Review: Mid-turn steering — correct message ordering

**Spec reviewed**: `docs/internal/specs/mid-turn-steering-message-ordering-spec-2026-08-24.md`
**Derived from**: `docs/internal/architecture/ADR-070-mid-turn-steering-message-ordering.md` (Accepted)
**Review date**: 2026-08-24
**Verdict**: REVISE

## Executive Summary

The spec's engineering content is sound and its "Symbols Involved" table was verified
line-for-line against the current `src/store/chat.ts` (all cited handlers, the missing
`findOpenAssistantMessageId` helper, `ChatMessage`'s field set, `AssistantMessage.status`,
and `buildMessageStatus` all match exactly as described). The blocking problem is narrower
but real: the spec's own **Regression Test Requirements** table asserts that an existing,
currently-passing test needs no changes and will keep passing under the fix — a claim
directly contradicted by that test's own assertions. Trusting this table as written means
`npx vitest run` will NOT exit 0 after implementation (violating SC-003/SC-004) via a test
the spec never told anyone to touch.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 1 |
| MINOR | 4 |
| OBSERVATION | 4 |
| **Total** | **9** |

---

## Findings

### MAJOR Findings

#### [MAJ-001] Regression table falsely claims an existing test is unaffected by the fix

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: "Regression Test Requirements" table, row 1 (spec line ~554):
  > `Mid-turn steer appends only a user message, no second placeholder` | `chat.mid-turn-send.test.ts`: `'appends only a user message after the streaming assistant bubble...'` | **No** — still true under the fix, unmodified assertion still holds (only the bubble's `isStreaming`/`closedBySteer` fields change, not message count)
- **Description**: This is self-contradictory on its own terms — it says the bubble's
  `isStreaming` field *changes*, then claims the *assertion on that exact field* still
  holds. Verified against the real test, `src/store/chat.mid-turn-send.test.ts:117-142`
  (`'appends only a user message after the streaming assistant bubble — no second
  placeholder'`): this test calls `sendMessage('first message')` then, with **no
  intervening `token`/`tool_call_start` frame**, calls `sendMessage('steer this turn')`
  and immediately asserts on the pre-steer bubble:
  ```ts
  expect(messages[1].isStreaming).toBe(true)   // line 130
  expect(messages[1].status).toBe('streaming') // line 131
  ```
  ADR-070 §2.1 (which the spec's own "Symbols Involved" table cites as governing this
  exact code path, `sendMessage`'s `isStreaming` branch) requires the fix to close that
  *same* bubble in the *same* store update that appends the steer's user message:
  `isStreaming: false`, `closedBySteer: true`, with status reported as finished (§2.4:
  "A reply segment closed this way MUST report itself as 'finished'... identically to an
  ordinarily completed reply"). Under the fix, `messages[1].isStreaming` becomes `false`
  and `messages[1].status` becomes `'done'` at the exact point this test asserts `true`/
  `'streaming'` — the test fails immediately, with zero further frames needed to trigger
  it. This is not an inference: it is the literal, first-order consequence of applying
  §2.1 to the code at `src/store/chat.ts:2471-2531` (the `if (isStreaming) { ... }`
  branch), which today performs *only* `applyMessageArray(allMsgs, b)` with no bubble
  mutation at all.
- **Impact**: The spec's Test Implementation Order table (19 tests) and the "3 existing
  tests to REWRITE" list (`:421`, `:487`, `:511` — all independently verified accurate by
  direct line-number check) together are presented as the complete regression surface. A
  4th, unlisted test will go red the moment §2.1 is implemented. Two concrete failure
  modes: (a) SC-003 ("100% of the Test Implementation Order table... and 100% of the three
  existing tests listed as REWRITE... passing — verified by `npx vitest run` exiting 0")
  is unsatisfiable as scoped, because a test outside both enumerated sets is now red; (b)
  worse, an implementer following this table at face value, seeing an *unplanned* failure
  in a test whose own docstring says "no second assistant placeholder... stays live and
  keeps receiving content," could reasonably read the failure as evidence their fix is
  *wrong* (since the test's ORIGINAL purpose was to pin exactly the behavior being
  inverted) rather than as an expected, spec-scoped test update — risking either a
  weakened fix or time lost re-deriving what this review already establishes directly.
- **Recommendation**: Move `'appends only a user message after the streaming assistant
  bubble — no second placeholder'` (`chat.mid-turn-send.test.ts:117`) into the "REWRITE"
  list alongside the three already there. Its message-count/role/content assertions
  (lines 126-129, 132-134, 138, 141) are genuinely unaffected and should stay; only lines
  130-131 need updating to `isStreaming: false` / `status: 'done'` (and a `closedBySteer:
  true` assertion added, matching the new Test #4/#5 pattern). Update the row's "New
  Regression Test Needed" cell to "Yes — REWRITE (lines 130-131 only)" and add this test
  name to SC-003's enumerated rewrite list (making it 4, not 3).

---

### MINOR Findings

#### [MIN-001] Traceability Matrix under-cites 5 of the 19 planned tests

- **Lens**: Inconsistency
- **Affected section**: "Traceability Matrix" (FR-001 through FR-008 rows) vs. "Test
  Implementation Order" table.
- **Description**: Tests #1, #2, #3 (the three `findOpenAssistantMessageId` unit tests)
  and #18, #19 (the two `ChatScreen` component/DOM-order tests) never appear as a cited
  `Test Name(s)` in any FR row of the Traceability Matrix, even though each has an
  explicit "Traces to BDD Scenario" entry in the Test Implementation Order table (Tests
  #1/#2/#18/#19 all trace to "A reply continues into a new segment," which the matrix
  cites under FR-001/FR-002 — but only via Tests #4/#5/#7/#8, never these five). Test #3
  in particular traces to a *parenthetical*, non-BDD-scenario justification ("supports
  `tool_call_start`'s existing, unchanged behavior") that appears nowhere in the matrix at
  all. The "Completeness check" note beneath the matrix ("Every FR-xxx has at least one
  BDD scenario and test... Every BDD scenario above appears in at least one row") is true
  as literally worded, but reads as stronger than it is: it does not mean every planned
  test is traceable from the matrix, and a reader auditing FR-by-FR would not discover
  Tests #1/#2/#3/#18/#19 exist at all without cross-referencing the other table by hand.
  This matters specifically for #18/#19: ADR-070 §4/F3 flags the `ChatScreen` render
  assertion as the one claim in the whole ADR that, pre-revision, existed *only* as an
  uncommitted throwaway repro — exactly the kind of claim that most needs an unambiguous,
  matrix-level trace, not an implicit one.
- **Recommendation**: Either (a) add Tests #1/#2/#3 to the FR-001/FR-002 rows and Tests
  #18/#19 to the FR-002 row (or a new "supports FR-001/FR-002" note for #3), or (b) add an
  explicit sentence to the Completeness check clarifying that the matrix cites the primary
  test per FR, not the full set, and pointing readers to the Test Implementation Order
  table's own "Traces to BDD Scenario" column for the complete picture.

---

#### [MIN-002] Regression audit never mentions the already-existing `ChatScreen.mid-turn-send.test.tsx`

- **Lens**: Incompleteness
- **Affected section**: "Symbols Involved" table (row for `hasStreamingMessage` /
  `VirtualizedMessageListInner`) and "Regression Test Requirements" table.
- **Description**: `src/components/chat/ChatScreen.mid-turn-send.test.tsx` (585 lines,
  referenced only in ADR-070's own header metadata, never in the spec body) is a
  committed, currently-passing component test suite covering the mid-turn-send **UI
  gates** (Enter-to-steer bypass, the mid-stream Send button, Attach/drag-drop while
  streaming). The spec's "Symbols Involved" table lists `ChatScreen.tsx`'s
  `hasStreamingMessage`/`VirtualizedMessageListInner` as "unchanged, test target... needs
  its own committed test" but never states whether this *other*, already-existing
  `ChatScreen` test file is in scope, affected, or confirmed clean. Direct reading shows
  its tests seed `useChatStore` with only a flat `isStreaming: true` (no per-message
  `messagesById`/`isStreaming` structure exercising the ordering fix), so they are very
  likely unaffected — but the spec asserts nothing about this file at all, leaving the
  "Regression impact explicitly addressed" bar (required by this skill's own Phase 1
  check) incompletely met for one real file that shares the same component under test as
  the two NEW tests (#18/#19) this spec adds.
- **Recommendation**: Add a row to "Regression Test Requirements" for
  `ChatScreen.mid-turn-send.test.tsx`, stating explicitly that its UI-gate assertions are
  orthogonal to the ordering fix (no `messagesById`/per-message `isStreaming` fixtures)
  and require no changes — turning an implicit, unstated assumption into an audited one.

---

#### [MIN-003] FR-008 downgrades to SHOULD what its own BDD scenario and test treat as unconditional

- **Lens**: Ambiguity
- **Affected section**: FR-008: "The system **SHOULD** preserve the exact ordering of
  multiple follow-ups sent in quick succession... and **MUST** start only one new reply
  segment."
- **Description**: The ordering-preservation half of FR-008 is stated as SHOULD (a
  recommendation, not a hard requirement), but every other artifact treats it as
  unconditional: Acceptance Scenario 3 ("each follow-up appears in the order it was
  sent"), the "Scenario Outline: Multiple rapid follow-ups preserve their own order" BDD
  scenario (no conditional language), and Test #9 (a hard assertion, not a
  best-effort/flaky-tolerant one) all read as MUST. A reader implementing strictly from
  FR-008's own modal verb could treat the ordering guarantee as best-effort (e.g.,
  acceptable to occasionally coalesce or reorder under load) while still claiming FR-008
  compliance, contradicting Test #9 and SC-003's 100%-passing bar for it.
- **Recommendation**: Change "SHOULD preserve" to "MUST preserve" in FR-008, matching the
  BDD scenario, the acceptance criteria, and the test's own unconditional assertion — there
  is no companion text anywhere explaining why ordering-preservation specifically should be
  softer than the "one segment" half of the same requirement.

---

#### [MIN-004] "Number of rapid steers" dataset (N=1/2/3) tests one boundary condition three times

- **Lens**: Overcomplexity (test-overhead)
- **Affected section**: "Dataset: Number of rapid steers" (rows 1-3) and Test #9.
- **Description**: Per ADR-070 §2.1, the steer-close logic is a single flag-flip
  (`isStreaming: false`, `closedBySteer: true`) executed identically on every mid-turn
  `sendMessage` call, with no per-count branching anywhere in the fix. N=2 already proves
  the meaningful boundary this dataset exists to establish ("not one bubble per steer" —
  FR-008's second half); N=3 exercises the identical code path a third time with no new
  boundary crossed (it is not, e.g., testing a ring-buffer eviction threshold or a
  batching limit — no such limit exists in the design). The dataset appears to be
  inherited from the pre-existing test's already-established N=3 precedent
  (`chat.mid-turn-send.test.ts`'s `'3 rapid mid-turn sends...'`) rather than independently
  justified by a distinct condition N=2 doesn't already cover.
- **Recommendation**: Either drop the N=3 row (N=1 happy path + N=2 boundary is
  sufficient to prove the "one segment regardless of count" claim) or, if N=3 is kept for
  parity with the existing test's established count, add one sentence explaining what N=3
  additionally verifies that N=2 does not (if genuinely nothing, remove it — per this
  skill's own overcomplexity test: "if a junior engineer asked 'why can't we just test
  N=2?' and the answer is 'well, the old test used 3'... it's unnecessary").

---

### Observations

#### [OBS-001] SC-002's "byte-identical" wording overclaims relative to what Tests #15-17 verify

- **Lens**: Infeasibility
- **Suggestion**: SC-002 states reload reproduces "byte-identical role/order sequencing"
  to the live session, "verified by the integration replay tests (Test #15-17)." Tests
  #15-17 are Vitest assertions on the reconstructed Zustand store's shape (message count,
  role sequence, tool-call attachment) — a reasonable and standard verification approach,
  but not literally a byte-level diff between a captured live snapshot and a captured
  reload snapshot of the same session. Consider softening SC-002 to "matching role/order
  sequencing" or, if "byte-identical" is meant literally, note that no test in the plan
  actually performs a live-vs-reload snapshot diff (that would require a live UAT +
  reload UAT pairing, not a unit-level store assertion) and add one if that literal
  guarantee is actually intended.

---

#### [OBS-002] `closedBySteer`'s "never crosses the wire" guarantee has no test enforcing it

- **Lens**: Insecurity (Information Disclosure)
- **Suggestion**: The "Conservative Type Design" section and ADR §2.4 both assert
  `closedBySteer` is "purely internal... never serialized to the wire," but nothing in the
  TDD plan asserts this — e.g., a test confirming it's absent from any REST
  serialization, persisted-session JSON, or exported/copied transcript payload. Given
  `ChatMessage` already intersects several other internal-only fields (`errorDetail`,
  `mergedReplayIds`) that share this same unenforced "never serialized" convention, this
  is a pre-existing pattern rather than a new gap this spec introduces — but since the
  spec explicitly calls out the field's internal-only nature as a design decision worth
  documenting, it would cost little to add one assertion (e.g., in a Copy-button or
  export-payload test) that it never appears in outbound data.

---

#### [OBS-003] No telemetry signal exists to catch a future silent regression of this exact bug class

- **Lens**: Inoperability
- **Suggestion**: ADR-070 §1.1 documents that the bug was "empirically confirmed... HEAD
  `eaa7b131`" via a currently-green test that had encoded the wrong ordering as intended
  behavior — i.e., this exact defect class (a passing test silently normalizing broken
  ordering) already happened once. The spec's "Deployment / Runtime" section states "No
  new logging planned" with no counter-argument. Given the project's MIT/no-telemetry
  posture this is likely an intentional, correct call — but the spec could note explicitly
  that a future regression's only detection mechanism is this same test suite (i.e., "no
  telemetry is add ed because the regression-guard tests below are considered sufficient
  coverage" is a stronger statement than silence), rather than leaving the omission
  unaddressed.

---

#### [OBS-004] No test proves `closedBySteer` doesn't leak across an unrelated, later turn

- **Lens**: Incompleteness
- **Suggestion**: Every BDD scenario and dataset in this spec concerns a single steered
  turn. There is no scenario or test for "a steered turn completes normally, then a
  wholly separate, unsteered turn begins" — confirming the new bubble opened for the
  later turn starts with `closedBySteer` unset/false and that no residual state from the
  prior steer (e.g., a stale `closedBySteer: true` on a bubble that's later reused by some
  future code path) affects it. By reading `chat.ts`, no current code path appears to
  reuse a bubble across turns in a way this could bite — so this is likely a non-issue —
  but an explicit regression test would make that "likely" into "verified," matching this
  spec's own stated rigor for every other adjacent boundary (Dataset rows explicitly cover
  "compound" edge cases like row 3 of "Replay merge eligibility" for exactly this kind of
  belt-and-braces reason).

---

## Structural Integrity (plan-spec format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1/2/3 each carry 2-3 Acceptance Scenarios |
| Every acceptance scenario has BDD scenarios | PASS | Each AS has a `Traces to:` BDD scenario |
| Every BDD scenario has `Traces to:` reference | PASS | All 10 BDD scenario blocks carry one |
| Every BDD scenario has a test in TDD plan | PASS | Verified against the 19-row Test Implementation Order table |
| Every FR appears in traceability matrix | PASS | FR-001–FR-008 all present, FR-007 correctly noted as a negative/no-test requirement |
| Every BDD scenario in traceability matrix | PASS (with caveat) | Scenarios present, but see MIN-001 — not every *test* that supports them is cited |
| Test datasets cover boundary conditions, edge cases, error scenarios | PARTIAL | See MIN-004 (rapid-steer dataset redundancy); otherwise datasets (steer timing, replay merge eligibility) genuinely exercise distinct boundaries, not relabeled happy paths |
| Regression impact is explicitly addressed | FAIL | See MAJ-001 (false "no change needed" claim) and MIN-002 (untouched-file omission) |
| Success criteria are measurable with no subjective language | PASS (with caveat) | See OBS-001 on SC-002's literal wording |

---

## Test Coverage Assessment

Reviewed against the actual current source (`src/store/chat.ts`, `chat.mid-turn-send.test.ts`,
`ChatScreen.tsx`, `ChatScreen.virtualization.test.tsx`, `chat.tool-call-offset.test.ts`) rather
than the spec's prose alone.

- The 19-test plan's **unit** and **integration** tiers are well-targeted at the actual code:
  every cited line/handler (`sendMessage`'s isStreaming branch at `chat.ts:2471-2531`, the
  `case 'token'` raw-tail/isStreaming guard at `chat.ts:3319-3329`, the bare
  `findLastAssistantMessageId` calls in `subagent_start` at `chat.ts:4325` and all three
  `media` branches at `chat.ts:4929/4950/4962`, the `replay_message` coalesce+merge blocks at
  `chat.ts:4719-4874`, and the C8 sweep's `lastContentEmpty`/`lastIsThisTurn` computation at
  `chat.ts:3830-3835`) was independently confirmed to match the spec's description exactly —
  including the currently-**absent** `findOpenAssistantMessageId` helper, confirming it is
  genuinely new code, not a rename.
- The **E2E (component)** tier (Tests #18/#19) is feasible with existing tooling —
  `ChatScreen.virtualization.test.tsx`'s `seedStreamingStore`/`makeBucketMessages` helpers
  (lines 408-449) already seed exactly the `messagesById`/`messageOrder` shape these two new
  tests would need, and the `streaming-message-anchor` testid (`ChatScreen.tsx:1401`) and
  `hasStreamingMessage` derivation (`ChatScreen.tsx:1309`) match the spec's description.
- The one confirmed **gap** is MAJ-001: a 4th existing test outside the plan's 19 + 3-REWRITE
  scope will break.
- Dataset "Replay merge eligibility" row 1 (no intervening entry → merge) is independently
  confirmed already covered by `chat.tool-call-offset.test.ts`'s "WS-replay same-turn merge"
  describe block (lines 272-333) and traced to remain passing under the proposed raw-tail-guard
  fix (the candidate bubble is still the raw tail when nothing intervenes, so the new condition
  is a no-op for this case) — the spec's regression claim for this row is accurate.

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `findOpenAssistantMessageId` / bubble-closure logic | ok | ok | ok | ok | ok | ok | Pure client-side presentation state; no new trust boundary, no new input surface |
| `closedBySteer` field | ok | ok | ok | risk (OBS-002) | ok | ok | Internal-only by design; no test enforces it stays off the wire (low severity — pattern already shared by several sibling fields) |
| `replay_message` raw-tail guard | ok | ok | ok | ok | ok | ok | No new data accepted from the wire; operates only on already-trusted, already-received frames |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Should `'appends only a user message after the streaming assistant bubble — no second
   placeholder'` (`chat.mid-turn-send.test.ts:117`) be added to the REWRITE list now, or is
   there a reason the spec author considers its `isStreaming`/`status` assertions somehow
   exempt from the §2.1 close-at-steer-time rule that this review did not find in the code?
2. Is "byte-identical" in SC-002 meant literally (requiring a live-vs-reload snapshot diff
   test that doesn't currently exist in the plan), or is it shorthand for "matching
   role/order sequencing" as verified by Tests #15-17's structural assertions?
3. Was `ChatScreen.mid-turn-send.test.tsx` deliberately scoped out because its UI-gate tests
   are known-orthogonal to the ordering fix, or was it simply not discovered during spec
   authoring (the spec's own "Existing Codebase Context" note says GitNexus was unavailable
   this session, which is a plausible cause for a missed file)?

---

## Verdict Rationale

REVISE, not BLOCK: the one MAJOR finding (MAJ-001) is a narrow, mechanically fixable gap — a
single test needs to move from "unaffected" to "REWRITE," which is a one-line table edit plus
a ~5-line test update once acknowledged — not a design flaw. Everything else the spec claims
about the current codebase (handler behavior, line-level code shapes, existing test coverage,
tooling feasibility) was independently verified accurate by direct reading, which is the
strongest evidence available that this spec's engineering judgment is sound; the defects found
here are entirely in the *audit trail* (traceability completeness, regression-table accuracy),
not in the underlying design already validated by ADR-070's own accepted, twice-grilled review.
Fix MAJ-001 before implementation begins (it affects the definition of "done" via SC-003); the
four MINOR findings should be addressed in the same revision pass since they're cheap and
improve auditability; the four OBSERVATIONs are optional polish.

### Recommended Next Actions

- [ ] Add `chat.mid-turn-send.test.ts:117`'s test to the REWRITE list and update its
      `isStreaming`/`status` assertions (MAJ-001) — **blocking**.
- [ ] Update SC-003's "3 existing tests" count to 4, listing all four REWRITE test names.
- [ ] Add Tests #1/#2/#3/#18/#19 to the Traceability Matrix, or add a scoping note
      explaining the matrix cites primary tests only (MIN-001).
- [ ] Add a Regression Test Requirements row for `ChatScreen.mid-turn-send.test.tsx`
      confirming it's unaffected (MIN-002).
- [ ] Change FR-008's "SHOULD preserve" to "MUST preserve" (MIN-003).
- [ ] Trim or justify the N=3 row in the "Number of rapid steers" dataset (MIN-004).

---
---

# Round 2 Review (2026-08-24)

**Scope**: (1) verify Round 1's fixes actually landed correctly in the spec; (2) fresh,
independent adversarial pass on the current spec + ADR-070 + current `src/store/chat.ts` /
`src/components/chat/ChatScreen.tsx`, looking specifically for anything Round 1 missed.

**Verdict**: **REVISE**

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 2 (both new — NEW-001, NEW-002) |
| MINOR | 0 |
| OBSERVATION | 0 |
| **Total (Round 2 net-new)** | **2** |

## Part A — Verification of Round 1's fixes

All four items requested were checked directly against the current spec text and the current
source tree (not re-derived from the spec's own claims).

### A1. MAJ-001 fix — VERIFIED CORRECT

`src/store/chat.mid-turn-send.test.ts:117-142` was read directly. The test at line 117
(`'appends only a user message after the streaming assistant bubble — no second
placeholder'`) asserts, at the cited lines:
- Line 130: `expect(messages[1].isStreaming).toBe(true)`
- Line 131: `expect(messages[1].status).toBe('streaming')`
- Lines 126-129, 132-134, 138, 141 are exactly the "unaffected" lines the spec's Regression
  table now claims (message count/role/content on all three messages, the single-assistant
  count, and the bucket-level `isStreaming`) — confirmed unaffected by the fix (closing the
  bubble in place changes neither array length nor role/content, and bucket-level
  `isStreaming` genuinely stays `true` because the overall turn is still in flight).

The spec's Regression Test Requirements table row for this test now reads: "**YES — REWRITE
(lines 130-131 only).** ... `expect(messages[1].isStreaming).toBe(true)` /
`expect(messages[1].status).toBe('streaming')` must become `false`/`'done'`, plus a new
`expect(messages[1].closedBySteer).toBe(true)` assertion" — this is line-accurate and
correctly scoped (only 130-131 need to change; the eight other assertions listed as staying
do genuinely stay under the described fix). **MAJ-001 is correctly and completely fixed.**

### A2. SC-003 test count — VERIFIED CONSISTENT

SC-003 lists exactly three REWRITE tests: `chat.mid-turn-send.test.ts:117`, `:421`, `:511` —
not two, not four. Cross-checked against the Regression Test Requirements table, which lists
the same three rows as "YES — REWRITE" and explicitly keeps `:487` ("steer → done (no tool
calls): unchanged") as "No" (traced to keep passing unmodified). Line numbers were verified
directly against the test file:
- `:117` → `'appends only a user message after the streaming assistant bubble...'` (confirmed above)
- `:421` → `'steer → tool_call_start → token → done: exactly one assistant bubble...'` (confirmed by direct read)
- `:487` → `'steer → done (no tool calls): unchanged — single assistant bubble, fully finalized'` (confirmed by direct read)
- `:511` → `'a tool call BEFORE the steer and one AFTER both keep correct owner/offset on the same bubble'` (confirmed by direct read)

All four line numbers are exact matches to the current file content — no drift since Round 1.
**SC-003 is internally consistent with the Regression table.**

### A3. Traceability Matrix additions — VERIFIED ACCURATE

Every test citation added to the matrix was checked against the "Traces to BDD Scenario"
column of the Test Implementation Order table it must agree with:
- Tests #1, #2 (added under FR-001) both trace to "A reply continues into a new segment" —
  matches FR-001's cited scenario.
- Test #5 (already present under FR-001) traces to "A follow-up sent before any reply text
  exists" — matches FR-001's second cited scenario.
- Tests #1, #2 (added under FR-002), #18, #19 (added under FR-002) all trace to "A reply
  continues into a new segment"; Test #8 traces to "A follow-up with no further reply
  content" — both match FR-002's cited scenarios exactly.
- Test #3's exclusion from the matrix (kept as prose beneath it rather than a row) is
  correctly justified: its own "Traces to BDD Scenario" entry in the Test Implementation
  Order table is a parenthetical, non-scenario justification, so listing it in a scenario-keyed
  matrix row would have been the exact misrepresentation MIN-001 flagged.

**MIN-001 is correctly and completely fixed** — every new citation was independently checked
against its source row, not just trusted.

### A4. Underlying engineering claims — RE-VERIFIED against current source (see Part B for what this surfaced)

Every code citation in ADR-070 that Round 2 re-checked line-for-line still matches the live
tree exactly, confirming no drift since Round 1's own verification pass:
- `sendMessage`'s `isStreaming` branch (`chat.ts:2470-2531`): confirmed it performs only
  `applyMessageArray(allMsgs, b)` today, no bubble mutation — the fix site is exactly where
  described.
- `case 'token'`'s raw-tail/`isStreaming`-gated fallback (`chat.ts:3320-3328`): confirmed
  present and matches the description ("Only reuse the last assistant bubble if it is still
  streaming... Stuffing them back into the closed bubble is what produced the
  'text-then-image-at-bottom' ordering").
- `case 'tool_call_start'`'s raw-tail-then-`isStreaming` fallback (`chat.ts:4048-4096`):
  confirmed present, logic matches `findOpenAssistantMessageId`'s proposed semantics exactly
  (raw tail if assistant, else backward-scan match only if `isStreaming`).
- `case 'subagent_start'` (`chat.ts:4325`) and all three `case 'media'` branches
  (`chat.ts:4929`, `4950`, `4962`): confirmed all four are still bare, unguarded
  `findLastAssistantMessageId` calls exactly as described.
- `replay_message`'s two `lastMsgId`-consuming branches (`chat.ts:4719` computing the shared
  `lastMsgId`, consumed by the coalesce-into-empty-placeholder branch starting at the same
  line and the general `sameTurn && compatibleProducer` merge branch starting ~`chat.ts:4797`):
  confirmed both branches read the SAME `lastMsgId`/`candidate`, so a single shared raw-tail
  guard computed once alongside `lastMsgId` (as the ADR implies) correctly covers both.
- The C8 sweep's `lastContentEmpty`/`lastIsThisTurn` computation (`chat.ts:3830-3835`) and its
  consumer (`isLastThisTurn = id === lastMsgId && lastIsThisTurn && !lastIsTerminalError` at
  `chat.ts:3854`): confirmed the proposed `!lastMsg.closedBySteer` addition to `lastIsThisTurn`
  is sufficient — traced the full C8 branch (`chat.ts:3854-3937`) by hand for the "steer with
  empty content, then immediate error" case and confirmed it correctly falls through to
  pushing a NEW error bubble, leaving the `closedBySteer` bubble untouched, matching Test #6's
  expectation.
- `hasStreamingMessage`/`streaming-message-anchor` (`ChatScreen.tsx:1309`, `:1401`): confirmed
  present, unchanged, exact line match to Round 1's own citations.
- `closedBySteer` and `findOpenAssistantMessageId`: confirmed **absent** from both files today
  (0 matches for `closedBySteer` in `chat.ts`/`ChatScreen.tsx`) — correctly described as new,
  not-yet-implemented additions.

No inaccuracies found in this re-verification — everything Round 1 checked still holds, and
the additional lines checked in Round 2 (`case 'tool_call_start'`'s fallback,
`replay_message`'s two branches, the C8 consumer expression, the "done" sweep's own
finalization loop) all match their descriptions exactly.

---

## Part B — Fresh findings (Round 2, independent of Round 1)

Round 1's audit scope was "which handlers **write/append** content via an unguarded backward
scan." Round 2 asked a different question: **are there consumers that *read* "the last
assistant message" via the same unguarded scan, whose behavior depends on *when* that message's
`status` transitions — not just *which* message it is?** Two such consumers exist, neither
mentioned anywhere in ADR-070, the spec's Symbols Involved table, its FR list, or its 19-test
plan, and neither has any existing test coverage.

### [NEW-001] `markLastMessageInterrupted` will mislabel the closed pre-steer bubble as `'interrupted'` if Stop/Escape/`/cancel` is used before the post-steer segment starts

- **Lens**: Incompleteness / Incorrectness
- **Affected code**: `markLastMessageInterrupted` (`src/store/chat.ts:1837-1876`), specifically
  the bare `findLastAssistantMessageId` call at `chat.ts:1840`, unconditionally invoked by
  `cancelStream` (`chat.ts:2871`: "always mark the last assistant message as interrupted when
  the user explicitly invokes cancel (stop button, Escape, `/cancel`, or the browser panel's
  'Take over')" — the comment's own words).
- **Description**: Once the fix lands, there is a real, UI-reachable window — from the instant
  a mid-turn steer closes the pre-steer bubble (`isStreaming:false`, `status:'done'`,
  `closedBySteer:true`) until the next `token`/`tool_call_start`/`subagent_start`/`media` frame
  opens a new bubble — during which the pre-steer bubble is the ONLY assistant message in
  `messageOrder`, and is therefore what the bare `findLastAssistantMessageId` scan at
  `chat.ts:1840` resolves to. Bucket-level `isStreaming` stays `true` throughout this window
  (the overall turn is still in flight — confirmed by `sendMessage`'s isStreaming branch,
  which "leaves `isStreaming` untouched"), so the Stop button remains visible and clickable the
  entire time. If the user clicks Stop (or presses Escape, or sends `/cancel`) in this window,
  `markLastMessageInterrupted` takes the branch at `chat.ts:1874-1875`
  (`const m = draft.messagesById[lastMsgId!]; if (m) { m.isStreaming = false; m.status =
  'interrupted'; ... }`) and overwrites the already-finished, already-`'done'` pre-steer
  bubble's status to `'interrupted'` — mislabeling a segment the user already read as
  successfully complete as "(interrupted)" instead, even though nothing about that segment was
  actually cancelled (only the *next*, not-yet-started segment was).
- **Why Round 1 (and the spec's own test plan) missed this**: the spec's own qualitative
  prohibition ("The system must not let a later, unrelated error accidentally overwrite or
  relabel a reply segment that was already correctly finished because of a follow-up") and its
  Edge Case text ("only the currently-open (post-follow-up) segment is marked cancelled; the
  already-finished pre-follow-up segment is untouched") both anticipate exactly this failure
  MODE — but only for the **error** path (C8 sweep, fixed via §2.6/Test #6) and for
  **cancellation after a new bubble has already opened** (Test #14: `'sendMessage(steer) → new
  bubble opens → cancel: only the new bubble is marked interrupted...'`). Test #14's own name
  requires the new bubble to already exist before cancel fires — it never exercises "steer,
  then cancel with NO intervening frame," which is the one sequence that actually breaks. No
  test in the 19-test plan, and no existing test (`ChatScreen.mid-turn-send.test.tsx`'s
  cancel-related tests all mock `cancelStream` itself via `useChatStore.setState({
  cancelStream: mockCancelStream })`, so they never exercise the real
  `markLastMessageInterrupted` bubble-mutation logic at all), reaches this code path.
- **Impact**: A common, everyday interaction (Stop button / Escape / `/cancel` — not an
  obscure edge case) silently corrupts the LIVE (in-memory) status of an already-correct,
  already-shown reply segment, in the single most ordinary "user changes their mind right
  after steering" flow. It is not permanent — a page reload replays from the backend's
  correctly-ordered transcript and self-heals — but for the remainder of that live session the
  user sees their own successfully-delivered follow-up-triggering reply mislabeled
  "(interrupted)", and (per the "T24b fix" comment at `chat.ts:1874` and the broader
  `isInterrupted` consumers cited in ADR-070 §2.4, e.g. `ChatScreen.tsx:1143`) this also
  suppresses/alters whatever UI treatment `'interrupted'` status carries (the "(interrupted)"
  suffix, and per `chat.ts:3288-3296`'s `case 'token'` guard, permanently blocks that bubble
  from ever receiving trailing tokens — though none were coming for it anyway since it's
  already closed).
- **Recommendation**: `markLastMessageInterrupted` needs the same eligibility guard the four
  ADR-070 §2.1 handlers are getting. Route its `lastMsgId` resolution through
  `findOpenAssistantMessageId` (or an equivalent check gating on `isStreaming` before
  mutating), and when it resolves to `null` (steer closed the last bubble, nothing has opened
  since), fall through to the SAME "no assistant message exists yet — create an interrupted
  placeholder" branch this function already has for the analogous "cancel fired between
  session_started and the first token frame" case (`chat.ts:1855-1865`) — mirroring how
  ADR-070 §2.1 tells `subagent_start` to "open a new placeholder" rather than silently drop or
  misattribute. Add this as a new §2.1-adjacent subsection to ADR-070 (a fifth guarded call
  site, alongside `token`/`tool_call_start`/`subagent_start`/`media`), a new symbol row in the
  spec's Symbols Involved table, a new FR (or an amendment to FR-006, which currently only
  covers the error path), and a new BDD scenario + test explicitly sequenced as "steer →
  cancel, with NO intervening token/tool_call_start" — the one ordering Test #14 does not
  cover.

---

### [NEW-002] The "New response from {agent}" ARIA live-region announcement will fire twice — once prematurely — for every steered turn that had already produced visible text before the steer

- **Lens**: Incompleteness / Insecurity (Information Disclosure — N/A) / general behavioral
  correctness
- **Affected code**: `lastAssistantMessageId` (`src/store/chat.ts:1539`, inside
  `bucketToForeground`) — a bare, unguarded `findLastAssistantMessageId(bucket.messageOrder,
  bucket.messagesById)` call exposed as a top-level derived store field — consumed by
  `ChatScreen.tsx:2871-2883` (`lastAssistantMessage`, `shouldAnnounce`, the announce-tracking
  `useEffect`) and rendered at `ChatScreen.tsx:2990-2993`:
  ```tsx
  <div aria-live="polite" aria-atomic="true" className="sr-only">
    {shouldAnnounce && lastAssistantMessage?.status === 'done' && (
      <span>New response from {activeAgentName}</span>
    )}
  </div>
  ```
- **Description**: Traced the full sequence for the primary happy-path scenario (BDD "A reply
  continues into a new segment"):
  1. Bubble A streams (`status:'streaming'`) — no announcement (render condition requires
     `status === 'done'`).
  2. User steers. Per §2.1's fix, bubble A closes IN PLACE: `isStreaming:false`,
     `status:'done'`, `closedBySteer:true`. `messageOrder` becomes `[user, A(done), user-steer]`.
     `lastAssistantMessageId` (still computed via the bare, unguarded scan at `chat.ts:1539`,
     which this spec does not touch) resolves to A — the only assistant message present — and
     A's `status` is now `'done'`. `shouldAnnounce` is `true` (A's id has never been announced;
     `lastAnnouncedIdRef.current` is still `null`), and the render condition
     (`lastAssistantMessage?.status === 'done'`) is now also `true` for the first time. The
     ARIA live region renders `"New response from {agent}"` and the `useEffect` records A as
     announced — **at the exact moment the user sent their follow-up**, before the agent has
     said anything in response to it.
  3. The next `token` frame opens bubble B (`status:'streaming'`) — `lastAssistantMessageId`
     now resolves to B (the new raw tail). No re-announcement while streaming.
  4. The turn truly finishes: B's `status` → `'done'`. `lastAssistantMessageId` still resolves
     to B (raw tail, assistant, done). `shouldAnnounce` is `true` again (B's id differs from
     the ref, which holds A's id) — the ARIA region announces `"New response from {agent}"` a
     **second** time, correctly this time.
  Net effect: screen-reader users hear "New response from {agent}" twice for a single steered
  exchange — once immediately after they send their own follow-up (which is not itself a
  response, and misleadingly implies the agent's reply is already complete when a second
  segment is about to stream), and once when the turn genuinely finishes.
- **Why this was missed**: ADR-070 §2.4 (the "keep `status:'done'`, don't add a new status
  value" decision) explicitly analyzed `ChatScreen.tsx:2881` and `:2991` — the EXACT two lines
  this finding concerns — but only for whether a hypothetical new `'steered'` status value
  would silently mismatch this consumer's plain `===` check (a **type/value-safety** question,
  correctly answered "no problem, it's still `'done'`"). That analysis never asked the
  orthogonal, **timing** question: does `lastAssistantMessageId` *itself* — and therefore
  *when* this consumer sees a `'done'` transition — change as a side effect of the fix? It
  does, because `lastAssistantMessageId`'s own derivation (`chat.ts:1539`) is a fifth
  unguarded `findLastAssistantMessageId` call site that ADR-070 §2.1/F2 never enumerated
  (F2 covered `subagent_start` and the three `media` branches, but not this read-only,
  store-level derived field). The spec's Symbols Involved table has no row for
  `lastAssistantMessageId` or `bucketToForeground` at all.
- **Impact**: A genuine accessibility regression, reachable in the single most common scenario
  this whole feature spec exists to fix (Scenario 1 / "A reply continues into a new segment"),
  with zero test coverage today and none added by the 19-test plan (confirmed:
  `lastAssistantMessageId` and `shouldAnnounce`/`"New response from"` appear in no test file in
  the repo). An implementer following this spec's TDD plan to the letter, with `npx vitest run`
  fully green, would ship this regression undetected.
- **Recommendation**: Either (a) route `bucketToForeground`'s `lastAssistantMessageId`
  derivation through the new `findOpenAssistantMessageId` helper instead of the bare scan —
  traced by hand this resolves the immediate problem (during the steer→next-frame gap, the raw
  tail is the steer's user message and the backward-scan candidate is no longer `isStreaming`,
  so the helper correctly returns `null`, suppressing the premature announcement until bubble B
  exists), without disturbing the ordinary non-steer case (confirmed: once B — or any ordinary
  final reply — closes, it IS the raw tail, so the helper's raw-tail branch returns it
  unconditionally, matching current behavior exactly); or (b) if `lastAssistantMessageId` is
  judged to need different semantics than "open for writing" (it is a read-oriented "most
  recent reply" field, not a write-target resolver, so reusing the write-side helper may not be
  the right fit long-term), explicitly scope this consumer's behavior in the spec/ADR and add a
  dedicated test asserting the ARIA region announces exactly once per steered turn, not per
  segment. Either way, this needs an explicit decision recorded in ADR-070 and a new FR/BDD
  scenario/test — it is currently unaddressed, not deliberately out of scope.

---

## Round 2 Verdict Rationale

REVISE. Round 1's five findings (MAJ-001 + MIN-001 through MIN-004) are all verifiably,
correctly, and completely addressed in the current spec — nothing to redo there. However,
Round 2's independent pass — asking "what else *reads* 'the last assistant message' via the
same unguarded scan, and does its behavior depend on *when* that resolution changes" rather
than Round 1's "what else *writes* via the same unguarded scan" — surfaced two more real,
UI-reachable call sites (`markLastMessageInterrupted` for cancellation, `lastAssistantMessageId`
for the ARIA live region) that share the exact defect PATTERN this whole ADR exists to fix, are
not mentioned anywhere in ADR-070 or the spec, and have zero existing or planned test coverage.
Both are concretely reachable through ordinary UI actions (Stop button; any steered turn with
visible pre-steer text) in the feature's primary happy path, not obscure edge cases requiring
unusual timing or malicious input.

### Recommended Next Actions (Round 2)

- [ ] Add a fifth guarded call site to ADR-070 §2.1 for `markLastMessageInterrupted`
      (`chat.ts:1840`), with a fallback to creating a fresh `'interrupted'` placeholder when no
      open bubble exists — mirroring the existing "no assistant message yet" branch in the same
      function (NEW-001) — **blocking**.
- [ ] Add a BDD scenario + test for "steer → cancel with no intervening frame" (the ordering
      Test #14 does not cover) (NEW-001) — **blocking**.
- [ ] Decide and document `lastAssistantMessageId`'s intended behavior across a steer boundary
      (route through `findOpenAssistantMessageId` or explicitly scope as unaffected-by-design),
      and add a test asserting the ARIA "New response" announcement fires exactly once per
      steered turn (NEW-002) — **blocking**.
- [ ] Add `lastAssistantMessageId` / `bucketToForeground` (chat.ts:1539) and
      `markLastMessageInterrupted` (chat.ts:1837) as new rows in the Symbols Involved table.
