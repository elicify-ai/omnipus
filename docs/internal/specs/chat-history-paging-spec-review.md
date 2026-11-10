# Adversarial Review: Chat history paging

**Document reviewed**: docs/internal/specs/chat-history-paging-spec.md
**Mode**: Spec
**Round**: Round 1 of 2
**Review date**: 2026-10-21
**Verdict**: BLOCK

## Executive Summary

The spec is unusually well grounded: every code claim I re-checked (C1, C6, C7, C10, C11, C12, C13, C15, C16, T1, T2, T3) matches the tree or the Train 3 git objects, the pixel math and the B02/B07 boundary tables are arithmetically correct, and no retired surface is reintroduced. It still has 1 CRITICAL, 8 MAJOR, 4 MINOR and 3 OBSERVATION findings. The CRITICAL is a reachability failure: the recommended answer to Q2 builds a message-target route that nothing in the product can ever link to. The MAJORs are mostly behaviours the spec is silent on in situations users will hit (snapshot reconnect while reading history, retention sweeps, first open after upgrade, unbounded page bytes) plus one over-built replay-ownership mechanism.

Frontend coverage (lenses 5, 8, 9, 10) is thinner than backend coverage. D8 states accessibility and recovery requirements, but no functional requirement, scenario or test traces to them (MAJ-007), and the target-load, 503-preparing and reconnect-while-reading states have no UI definition.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 8 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **16** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Q2=A ships a target loader and route that no user can reach

- **Lens**: Reachability
- **Affected section**: Reachability ("Old target" row), D9 (Q2 recommendation), B14/B15/B16, FR-011, Q2
- **Failure scenario**: Q2=A is implemented exactly as recommended. B14 and T17 pass by typing a URL with a message ID into the browser. No UI element anywhere produces such a URL: Sessions search returns sessions, there is no per-message "copy link", and no route consumes a target parameter. The founder's "jump to an old message (from search)" is delivered as a server reader plus a route that only a developer can use. This is the failure the root `CLAUDE.md` "Definition of Done" exists to prevent: green tests, no reachable path.
- **Evidence**: The spec's own C16 says "No target-message navigation parameter is consumed on the inspected chat/session routes" and "Message search is not an established producer of a message ID". I re-checked. `src/routes/_app/sessions.$sessionId.tsx` has no `validateSearch` or search handling, and a case-insensitive grep for `copy.*link|permalink|messageLink` under `src/components/chat` finds only unrelated files (`UntrustedChildText.tsx`, `IframePreview.test.tsx`, `MarkdownText.test.tsx`). The Reachability row says "A validated session/message deep link is a real browser navigation route under Q2=A" but names neither the route, the parameter, nor any link source. The existing deep-link route `/sessions/$sessionId` does exist and is a natural carrier. The spec does not say so.
- **Recommendation**: Make Q2=A include at least one in-product producer plus a named, contract-documented route. Concretely: (1) name the route and search parameter (for example `/sessions/$sessionId?message=<id>`, validated through the route's `validateSearch`, with the parameter documented in DOC-1). (2) Add one producer, for example a "Copy link to message" action on each message row, with keyboard access and an accessible name. (3) Add a BDD scenario and E2E step that go from that action to the target view and highlight. If the founder prefers no producer yet, move the target reader out of this issue (Q2 option C) rather than ship an unreachable one.

---

### MAJOR Findings

#### [MAJ-001] A snapshot reconnect while the reader is deep in history silently throws the reader back to the newest page

- **Lens**: Incompleteness / UI states and journey gaps
- **Affected section**: D7 (snapshot/incremental lifecycle), D6 last paragraph, B06, US2 scenario 5, D-R2
- **Failure scenario**: A person has scrolled back 3,000 messages. The laptop sleeps or the gateway restarts. On reconnect the server judges the cursor unservable (`boot_mismatch`, `retention_exceeded`, or a journal beyond its 2,048-frame / 1 MiB limit) and answers with a snapshot. The client wipes its loaded history, the new generation fences every old page, and the reader lands on the latest 40 groups. This is exactly the "bounced back to the newest messages" that US2 scenario 5 prohibits. The spec says only "an unservable cursor falls back to the bounded latest snapshot" and "a new full snapshot aborts and invalidates", and it defines no reader-position behaviour for it.
- **Evidence**: `src/store/chat/slices/catchup-frames.ts::handleSessionSnapshotFrame` calls `applySnapshotHistoryWipe`, which wipes history by design. The spec's own C9 states the journal retention limits. D7 and D6 contradict US2 scenario 5 on this path.
- **Recommendation**: Define the behaviour for a snapshot that arrives while the view is in historical-read mode. Preferred: keep the reader's window and anchor, apply only the control state, projection and tail (a "tail-only refresh"), and show Return to latest. Fallback: replace the window but show a persistent, dismissible notice that the view was reset to the latest messages. Add a BDD scenario (snapshot arrives while reading history at both memory-policy edges), a dataset row in D-R2, and a test.

#### [MAJ-002] A page has a row-count bound but no byte bound, so the founder's "replay size stays bounded" is explicitly not met

- **Lens**: Infeasibility / Security (denial of service)
- **Affected section**: D4 "Row-budget qualification (Q3)", Performance section ("not established"), Q3, SC-005
- **Failure scenario**: A page may hold up to 100 groups, and each archived record may be up to 8 MiB (C3). A session with large tool results can therefore force a page, and its WS replay, of hundreds of MiB. That means server allocation, WS stall timeouts (`attachReplayStallTimeout` closes a slow client) and browser heap blow-ups on open. This is the exact "heavy open" the issue is meant to remove. Any authenticated caller can also request `limit=100` repeatedly. The spec states it plainly: "no hard replay-byte guarantee is claimed" and "a fixed absolute byte cap ... is not established". The issue's "Done when" says "Memory and the replay size stay bounded on open".
- **Evidence**: Spec C3 (8 MiB admission), Q3 text, and `pkg/gateway/websocket_replay.go::errSendTimeout` (the stall close). Whether result-reference and preview machinery shrinks typical entries is not shown anywhere in the spec (certainty on typical sizes: Inferred).
- **Recommendation**: Add a second stop rule to the selector: accumulate encoded bytes of selected groups, stop before exceeding a soft budget (an architect-proposed value, for example 4 MiB), and always return at least one whole group. Report the stop reason `byte_budget` in `page_info` and in the `history_page` log. This keeps atomic groups intact (Q3=A) while giving SC-005 something falsifiable. State the largest-single-group case as the only exception.

#### [MAJ-003] Retention sweeps delete whole archive partitions, but the index design does not say how has_earlier, cursors or lookahead stay truthful

- **Lens**: Incompleteness / Inconsistency with the tree
- **Affected section**: D1 (index maintenance, "Errors/damage"), Edge Cases ("retention"), D-R4, B22
- **Failure scenario**: `ArchiveDayStore.sweepRolledPartitionsLocked` deletes every rolled partition whose mtime is older than the cutoff. The derived index still lists groups in the swept partitions. The reader at the oldest retained edge sees `has_earlier=true` (index lookahead), requests `before`, tries to read a swept partition, and the spec's rule for "required unreadable state" returns 500. The reader sees an error and Retry at the top of every swept session, indefinitely. The spec lists "expired positions visibly fail" but not that has_earlier and lookahead must reflect retained content only, and nothing distinguishes a swept partition (expected) from a corrupt one (an error).
- **Evidence**: `pkg/session/archive_expiry.go::sweepRolledPartitionsLocked`, `removePartitionFn`, and `repairWindowLocked` (which already uses `partitionGone` to tell swept from damaged for the model window). The spec's D1 does not mention sweep at all. D-R4 has one thin row ("target partition retained/expired").
- **Recommendation**: Add an index obligation: the sweep path updates, or the reader reconciles, the index so that has_earlier is computed over retained partitions only. Reuse the existing `partitionGone` distinction (absent = expired, never a 500). Add dataset rows: first, last and middle partition swept, effect retained while its call was swept, and has_earlier flipping false after a sweep. Add a BDD scenario for reaching the oldest retained edge after a sweep.

#### [MAJ-004] The first open after upgrade has no defined behaviour, and the 503 "preparing" state has no UI

- **Lens**: Incompleteness / UI states
- **Affected section**: Deployment / Runtime, D1 "Cold/legacy behavior", D11 (503 row), D-R4
- **Failure scenario**: Every existing install has no chat index. The spec says both "Boot/cutover prepares old sessions before ordinary history serving" and "missing metadata uses a serialized rebuild with a visible not-ready outcome (503)". These are two different designs. If boot blocks, gateway start time grows with the lifetime size of all sessions. If rebuild is lazy, the long, months-old session that motivated this issue is the first one hit, and its owner sees a 503 for an unknown time. No component, copy, auto-retry or Retry behaviour is defined for 503, and the REST detail route loader (`sessions.$sessionId.tsx` calls `fetchSessionDetail`) would also hit it. The review constitution (INC-10) also asks that data-lifecycle changes say whether existing data is backfilled. A lifetime index build is a backfill in all but name.
- **Evidence**: Spec "Deployment / Runtime" paragraph 2, D11 table 503 row, and no UI row for it in D8. `src/routes/_app/sessions.$sessionId.tsx` (loader).
- **Recommendation**: Pick one (see Founder Q3). Whichever is chosen, specify the 503 UI state (status text, automatic retry cadence with a visible manual Retry, composer remains usable), a progress-free but truthful message, serialized build order (the session being opened first), and a scenario plus test for first-open-after-upgrade on a 100,000-group fixture.

#### [MAJ-005] The WS replay ownership manifest is a brittle second protocol and is not shown to be necessary

- **Lens**: Overcomplexity / Contract-first gaps
- **Affected section**: D4 (manifest, `replay_start`/`replay_count`, `replay_context_ranges`), D10 SessionHistoryEntryRef / PageInfo tables, B23
- **Failure scenario**: The client must validate that manifest ranges plus context ranges cover exactly [0, `replay_frame_count`) with no gap or overlap, and treats any mismatch as a "visible failed attach". The server side must therefore count every frame `streamReplay` can emit in the same order, including the terminal card, warning and special frames. One path that emits an uncounted frame (for example a new special-row type added later, or a path in `dispatchSpecialEntry`) turns into a session that cannot be opened at all, with the failure visible only on long sessions. The REST page repeats the same manifest even though its `Message` entries already carry `history_position`, ids and tool calls, so REST gets two sources of truth that B23 then has to police.
- **Evidence**: `pkg/gateway/replay.go::streamReplay / emitFlatToolCall / dispatchSpecialEntry` (many frame kinds, one shared emitter); spec D4 states the manifest is needed because tool frames "lack an owning message ID". Alternatives table does not consider adding an explicit owner field to the generated tool/special frames, or having the snapshot carry control state only while the page itself is read through the REST page reader.
- **Recommendation**: Before approval, add these alternatives to the Alternatives table with a reason: (a) add a generated `entry_position` (or `owner_message_id`) field to the tool-start/result/special frames, so ownership is local to each frame and no ordinal coverage invariant exists; (b) snapshot carries `page_info` and control state only, history rows come from the REST page. If the manifest stays, drop it from REST (derive from `entries`), and require a server-side test that fails if any frame kind the replayer can emit is not accounted for in coverage.

#### [MAJ-006] The Train 3 and interim replay-fix composition decision only partly holds

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: D7 "Interim overlap", D12, FR-016, Regression table last two rows, Integration Boundaries
- **Failure scenario**: (1) FR-016 and D12 make an unlanded 446-file branch (Train 3) and a branch that "was not available as a local or remote ref" (the interim fix) hard preconditions of integration. If either stalls, a P1 feature stalls with it, and nothing says who decides then. (2) The spec says to reuse Train 3 "semantics, not code" and also to "reuse Train 3's parseHistoryQuery/parseHistoryInt once landed". That helper lives in the room file `pkg/gateway/rest_rooms.go` on Train 3, so reuse means a refactor of Train 3's file. (3) Train 3 changes seams this feature owns: `pkg/gateway/replay.go::buildEntryMessage` adds `RoomRef`, `pkg/session/daypartition.go::TranscriptEntry` gains `RoomRef`, `contracts/openapi.yaml` (+117 lines) and `contracts/asyncapi.yaml` change, and `src/components/chat/ChatScreen.tsx` has a `RoomTrafficMessage` branch. The spec's "special content parity" list and D-P1c omit `room_ref`, so a page built before the rebase would drop room traffic that live and replay show. (4) D7's snapshot sequencing rests on interim-fix code the author never read.
- **Evidence**: `git diff --name-only HEAD...origin/work/train3-approvals-rooms` (touches `pkg/gateway/replay.go`, `pkg/session/daypartition.go`, both contract files, generated types, `ChatScreen`-adjacent tests); `git grep parseHistoryQuery origin/work/train3-approvals-rooms -- pkg` (only `rest_rooms.go`); `git ls-remote` result recorded in the spec; spec D12. Train 3 contract claims T1, T2 and T3 themselves are accurate (read at `573c3674`).
- **Recommendation**: Keep "do not cherry-pick" (that part holds). Change the rest: gate only landing, not spec approval or RED, on those two bases; write a chat-specific query parser now and extract a shared helper later; add `room_ref` to D-P1c and to the parity requirement; make "read the landed interim fix and amend D7" an explicit pre-RED task owned by team-lead; and name who decides if a base has not landed by the time GREEN is complete (see Founder Q1).

#### [MAJ-007] The accessibility, focus and state requirements in D8 have no requirement, scenario or test

- **Lens**: Accessibility and keyboard / UI states / Testability
- **Affected section**: D8 UX table (Accessibility row), Traceability Matrix, BDD Scenarios, T15/T16
- **Failure scenario**: D8 requires keyboard-operable actions, polite page-load status, no focus theft on prepend, no "new response" announcement for historical rows and reduced-motion-safe correction. No FR names them, no BDD scenario exercises them, and T15 mentions only an "accessible target route". A prepend that moves focus, or a screen-reader announcement for each loaded historical row, would pass every planned test. The loading state between a target link being opened and the neighborhood arriving (the old view is held, nothing says it is busy), the Return to latest affordance's own states, and the target highlight (duration, reduced motion, announce) are also undefined.
- **Evidence**: Spec D8 table versus the Traceability Matrix (no FR/BDD for accessibility); `design-system/catalog.json` does list `ErrorState` and `Skeleton`, so reuse is available but the spec names no catalog entry for the loading status or the Return to latest control.
- **Recommendation**: Add FR-017 (accessibility and states), two BDD scenarios (keyboard-only: reach and activate Show earlier, Retry, Return to latest; screen-reader: loading and failure announcements without historical-row announcements and without focus movement on prepend), the corresponding test rows in T15/T16, and one catalog check naming the exact components used. Define the target-loading state and highlight behaviour.

#### [MAJ-008] Q1=A (the recommended memory policy) is more machinery than the stated problem needs

- **Lens**: Overcomplexity
- **Affected section**: D9, Q1, B12, T09, D-M1
- **Failure scenario**: Q1=A requires a reader-centred window, a separate 100-group newest-tail cache, whole-group eviction on the far edge, eviction-aware reload cursors, "Return to latest" and a rule that live messages never attach below a detached window. These are three stores and a detached-tail state machine in a 3,267-line `ChatScreen.tsx` that is already a size-budget concern. A bug in any join produces a hidden gap or a lost anchor, which is the exact visible defect the feature exists to prevent. Remove the 100-group tail cache and the feature still meets every stated requirement: a detached view can simply refetch the newest page on Return to latest, and live frames still update keyed active control state.
- **Evidence**: Spec D9 ("a bounded newest-tail cache of at most 100 complete groups", "Evict whole far-edge groups opposite traversal"). `src/store/chat/messages.ts::MAX_MESSAGES_PER_SESSION` is the existing single ring. Separately, I found no eviction of per-session buckets in `src/store/chat/*.ts` (grep for evict/prune/LRU on `sessionsById` finds nothing; certainty Inferred), so the spec's "no all-session history cache" claim is untested for visited sessions.
- **Recommendation**: See Founder Q4. Recommend one windowing rule (a single 500-group window, far-edge eviction, no tail cache, refetch on Return to latest) and state the per-visited-session bucket behaviour explicitly.

---

### MINOR Findings

#### [MIN-001] The pixel trigger uses virtualizer offsets built on a fixed 80 px estimate

- **Lens**: Ambiguity / Testability
- **Affected section**: D5 (Dtop), D-G1..G3, T07
- **Failure scenario**: `VirtualizedMessageListInner` uses `estimateSize: () => 80` and unmeasured prepended rows keep that estimate. In a chat full of 40 px one-liners, the estimated distance to the top is about twice the real one, so the 2H trigger fires when about one screen is left instead of two, which can show a gap for a reader at the planned 1 viewport/second. T07 tests the formula with chosen A and Dtop values, not Dtop as produced by the virtualizer.
- **Evidence**: `src/components/chat/ChatScreen.tsx::VirtualizedMessageListInner` (`useVirtualizer({... estimateSize: () => 80, overscan: 5})`).
- **Recommendation**: Require `estimateSize` to follow the running average A (clamped), and add a D-G row with real heights well below the estimate and a test that measures Dtop from the virtualizer.

#### [MIN-002] WS and REST disagree on validation precedence for a bad limit

- **Lens**: Inconsistency
- **Affected section**: D11 precedence versus D11 "Bad attach history_limit" row, D10 AttachSessionFrame
- **Failure scenario**: REST validates the paging syntax after the session-access check. The WS row says a bad `history_limit` is rejected "before hub binding" without saying whether that is before or after `resolveSession`'s main-eligibility check. An implementer can validate first and reveal nothing harmful, or after and be consistent with REST. Two tests will then disagree about which error an inaccessible main with a bad limit receives.
- **Evidence**: `pkg/gateway/websocket_replay.go::resolveSession` (validate id, `mainSessionAllowed`, resolve store) runs before `bindHub`.
- **Recommendation**: State one order for both carriers (access refusal first, then limit syntax) and add the case to D-V2.

#### [MIN-003] The page cut is "captured under the addressed session's synchronization" without saying what that covers

- **Lens**: Ambiguity / Concurrency
- **Affected section**: D1 "Locks"
- **Failure scenario**: One reading holds the session shard for the whole multi-record page read. Appends and therefore live turns for that session stall for the duration of a 100-group read. The other reading captures only the tail marker under the lock and reads outside it. Both read as compliant.
- **Evidence**: Spec D1 "Locks" row; `pkg/session/CLAUDE.md` lock-pool rules are cited by the spec but not restated.
- **Recommendation**: State that only the covered-tail marker is captured under the shard lock and all content I/O happens outside it, and add a test that an append is not blocked by a slow page read.

#### [MIN-004] Log content rules and private-field exclusion have no test

- **Lens**: Testability
- **Affected section**: Observability, FR-014, FR-015
- **Failure scenario**: The spec forbids message bodies, raw cursors, raw addresses and arbitrary query strings in the `history_page` log. A planted `slog` of the raw query would pass every listed test. The private-field negative control is in D-P1c, but nothing covers log content.
- **Evidence**: Spec Observability paragraph 1 versus T01-T18 (none asserts log content).
- **Recommendation**: Add a test row (backend) that captures logs for each failure class and asserts the absence of a planted body, raw token and query string.

---

### Observations

#### [OBS-001] The position token's design is defensible but its alternative was not weighed

- **Lens**: Overcomplexity
- **Affected section**: D2, Alternatives table
- **Suggestion**: A one-way hash of session, partition key, offset and entry ID needs a reverse hash-to-address index, while a per-session monotonic chat-occurrence number (stable because the archive is append-only, and not the rejected model ordinal) would also be stable, orderable and allow O(1) neighbour lookup. The Alternatives table rejects model ordinal and base64 address but never compares this one. I found no concrete failure in the hash design, so this is not a finding. One caveat to record: `convRewriteChatLines` in `pkg/session/unified_conv_transcript.go` rewrites partitions during conversion, so "positions survive rebuild" holds only for partitions that are no longer rewritten. State that.

#### [OBS-002] "Loading earlier messages" and "Return to latest" have no named catalog component

- **Lens**: Design-system reuse and brand
- **Suggestion**: `ErrorState`, `Button` and `Skeleton` exist in `design-system/catalog.json`. Name the exact ones for the status line and the return control so the design-system CI gate is not discovered at build time.

#### [OBS-003] User docs TODOs are accurate against the existing pages

- **Lens**: Incompleteness
- **Suggestion**: Verified that `docs/using-omnipus-ui.md` ("Clear a chat's context") contains the sentence "(Reaching those earlier messages on screen is planned separately.)" and that `docs/operations/observability.md` and `docs/protocol/websocket-protocol.md` exist, so DOC-1..3 are real. There is no user-facing REST page that documents `GET /sessions/{id}/messages`, so the breaking array-to-envelope change is only discoverable through `contracts/openapi.yaml`. Consider saying where API consumers learn of it.

---

## Structural Integrity

### Variant A: Spec mode

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | `Draft` |
| ADR linked (or explicitly stated not needed) | PASS | Cites session-core and UI-independent-turns ADRs by title; separately says no new ADR in this dispatch. D2/D3 contract shape decisions would normally come from the architect: acceptable here because the author is the architect. |
| Contract changes stated first, citing `contracts/` | PASS | D10 is complete and names files; ordering by the five steps is stated |
| API and data section | PASS | D3, D10, D11 |
| UI screens and states (loading/empty/error/partial) | FAIL | Error/Retry covered; target-loading, 503 preparing, reconnect-while-reading and Return to latest states are not (MAJ-001, MAJ-004, MAJ-007) |
| User journey section | PASS | Stories 1-4 and Reachability |
| Accessibility and keyboard section | FAIL | Stated in D8 only, no FR/BDD/test (MAJ-007) |
| Design-system components, catalogue-first | FAIL | Says "catalogued Button/ErrorState" but names no component for loading or return control (OBS-002) |
| Security and user promises section (when touched) | PASS | D11, with the gap in MAJ-002 |
| BDD acceptance scenarios, oracle from spec | PASS | B01-B25; expected values are spec-derived (B02, B07 tables recomputed and correct) |
| Traceability table: requirement -> scenario -> test | PASS with gaps | No orphans among FR-001..016, B01..B25 and T01..T18; gaps are the missing accessibility FR (MAJ-007) and log-content test (MIN-004) |
| Reachability section (tool policy / screen wiring) | FAIL | Old-target path unreachable (CRIT-001); other rows are sound; the "no new agent tool" row is correct |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Concurrency | Snapshot arriving while the reader is in a historical window; append not blocked by a slow page read | B06, B11; MAJ-001, MIN-003 |
| Retention | Sweep of first/middle/last partition and effect-retained/call-swept | B22; MAJ-003 |
| Upgrade | First open after upgrade with no index, 503 UI, boot-time versus lazy | B22, B25; MAJ-004 |
| Frontend accessibility | Keyboard-only path, announcements, focus on prepend, reduced motion | none today; MAJ-007 |
| Resource bound | Page of maximum-size records; byte budget stop reason | B24, B21; MAJ-002 |
| Log hygiene | No body/token/query in `history_page` logs | none; MIN-004 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| D-R2 | Snapshot (not incremental) reconnect with a historical window loaded | Add boot_mismatch and retention_exceeded rows |
| D-R4 | Swept partition cases; first-open-after-upgrade | Add rows per MAJ-003 and MAJ-004 |
| D-G1b | Real row heights below the 80 px estimate | Add 40 px one-liner conversation (MIN-001) |
| D-P1c | `room_ref` rows from Train 3 | Add once the base lands (MAJ-006) |
| D-V2 | Bad `history_limit` on WS for an inaccessible main | Add (MIN-002) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Paged REST read | ok | ok | ok | ok | risk | ok | Access parity is real: `gateMainRead` is the only gate and the spec uses exactly that (verified). 404 precedence prevents a cross-session position oracle. DoS: unbounded page bytes and an authenticated caller able to trigger index rebuild (MAJ-002, MAJ-004) |
| WS attach with history_limit | ok | ok | ok | ok | risk | ok | Same byte-bound gap; stall-timeout close under a huge replay |
| Derived index | ok | ok | ok | ok | risk | ok | Crash and sweep reconciliation (MAJ-003); rebuild cost on first open |
| Position token | ok | ok | ok | ok | ok | ok | One-way, same-session selector, not authorization; no leak of partition or offset |
| Browser state | ok | ok | ok | ok | risk | ok | Per-visited-session buckets look unevicted (MAJ-008, Inferred) |

**Legend**: risk = identified threat not mitigated in the document, ok = adequately addressed or not applicable

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | N-A | No new tool; spec states so, and the feature adds no callable capability |
| User-facing: named screen/component renders it? | Partly | Open, scroll-up prefetch and clear-divider crossing are rendered by `ChatScreen`; old-target jump has no producer or named route (CRIT-001) |
| Test plan describes execution, not just authorship? | Yes | T16-T18 and D-U1 require real-browser and independent-validator execution on the exact commit |

---

## Unasked Questions

1. What is the exact behaviour of a snapshot that lands while the reader is mid-history (MAJ-001), and does the answer differ between boot_mismatch and retention_exceeded?
2. Which component renders the 503 "preparing" state, and how often does it retry (MAJ-004)?
3. Which concrete route and parameter carry a target link, and which UI element creates one (CRIT-001)?
4. What is the byte ceiling of a page, and what is its stop reason when hit (MAJ-002)?
5. How does the index learn that a partition was swept (MAJ-003)?
6. After a Return to latest, what happens to the optimistic or pending input that was sent while the reader was in a historical window?
7. Does the per-session `sessionsById` bucket for a session you left stay in memory, and is that consistent with "no all-session history cache"?

---

## Questions for the founder

**Q1 — Should this feature wait for Train 3 and the interim replay fix, or only land after them?**

Context and impact: the spec makes landing of Train 3 (a 446-file branch with approvals and rooms) and of a replay-render fix that no one has read hard preconditions of integration. If either slips, this P1 feature slips with it. The shared code is small: a query parser and anchoring ideas. Train 3 does change files this feature also changes (replay, transcript entry, contracts), so a rebase will be needed in any case.

| Option | Outcome |
|---|---|
| A | Spec as written: everything waits for both landings, then rebase. Safe lineage, but schedule risk and no fallback owner. |
| **B (recommended)** | Approve the spec and start RED/GREEN on the current integration base. Write the chat query parser now. Rebase onto each branch when it lands, and treat the interim-fix re-read as a pre-RED task. Only integration order is gated. |

One-line answer: **Q1 A / B**.

**Q2 — What gives a user a way to jump to an old message in this issue?**

Context and impact: the founder's issue says "jump to an old message (from search)", but today no search returns messages and nothing produces a link to one. Shipping only the loader and a typed-URL route (the spec's recommendation) passes its tests while no user can use it, which fails the Definition of Done (CRIT-001).

| Option | Outcome |
|---|---|
| A | Spec as written: loader plus URL route, no producer. Not reachable. Not recommended. |
| **A+ (recommended)** | Loader, a named route and parameter on the existing session route, and one producer: a "Copy link to message" action on each message. Small UI cost, real reachability. |
| B | Add real message search with its own scoping, indexing and privacy work. Larger scope. |
| C | Move the jump-to-message piece to a follow-up issue; this issue ships paging, prefetch and clear-crossing only. |

One-line answer: **Q2 A+ / B / C**.

**Q3 — How should the first open after an upgrade behave while the history index is built?**

Context and impact: existing installs have no index, and a months-old session is the first one that needs it. Rebuilding blocks either gateway start (a longer boot proportional to all session sizes) or the first open of each session (a wait or error shown to the person who most needs this feature). The spec names both and picks neither.

| Option | Outcome |
|---|---|
| A | Build every session's index at boot, blocking start. Simple behaviour, slow upgrade boot for large data directories. |
| **B (recommended)** | Build in the background after start, serialized, with the session being opened built first. The open shows a clear "Preparing history" state and retries by itself; the composer stays usable. |
| C | Build only on first open, no background work. Smallest footprint, longest first-open wait. |

One-line answer: **Q3 A / B / C**.

**Q4 — Which memory policy, and should a page have a byte limit as well as a row limit? (replaces the spec's Q1 and extends its Q3)**

Context and impact: the founder's own "Done when" says memory and replay size stay bounded on open. The spec's Q1=A adds a window plus a separate 100-group tail cache (MAJ-008), and its Q3 leaves a page's byte size unbounded (MAJ-002), so "bounded replay" is not actually met for sessions with large tool results.

| Option | Outcome |
|---|---|
| A | Spec's Q1=A as written (window plus 100-group tail cache) and row cap only. Most machinery, no byte bound. |
| **B (recommended)** | One 500-group window with far-edge eviction and a refetch on Return to latest (no tail cache), plus a soft page byte budget (value proposed by the architect, always at least one whole group). Keeps the founder's bounded-open promise and keeps the tool groups whole. |
| C | No eviction while reading history (spec's Q1=B), keep the old ring only while following live, and add the byte budget. Simplest, but memory grows with a long reading visit. |

One-line answer: **Q4 A / B / C**. My views on the spec's other questions: Q3 as written (atomic overshoot) is fine once a byte budget exists; Q4 envelope numbers are acceptable as proposed.

---

## Verdict Rationale

BLOCK, because CRIT-001 is the kind of defect the Definition of Done is written for: the recommended Q2 answer yields a feature whose tests pass and whose old-target jump no user can invoke. It is cheap to fix and needs a founder decision (Q2). The remaining findings are REVISE-level but several are the kind that decide whether the feature behaves like the founder's model in real use: reconnect while reading (MAJ-001), retention (MAJ-003), the upgrade open (MAJ-004) and an unbounded page (MAJ-002).

What I verified as sound, so the fix round does not disturb it: the page-semantics core (exclusive cursors, truthful lookahead, ascending output); the pixel and clamp arithmetic (H=800, A=80 gives 20; A=40 gives 40; A=400 gives 20; H=1600, A=10 gives 100; trigger boundary at Dtop = 2H); the 47-character token length (4 + 43 base64url characters of a SHA-256); access parity (`gateMainRead` is the only read gate, as C15 says); the unchanged-incremental-attach rule; and the Train 3 facts T1-T3 (RoomHistoryResponse shape, 100/1000 limits, 24 px trigger, `overflow-anchor:none`, commit `b3b437a35`). No retired surface (Command Center, raw cron, JPEG screencast, goal confirm gate, fail-closed backfill, orphan-turn watchdog, shell block list) is reintroduced.

### Recommended Next Actions

- [ ] Resolve Q1-Q4 with the founder before any fix (CRIT-001, MAJ-002, MAJ-004, MAJ-006, MAJ-008).
- [ ] Add the snapshot-while-reading behaviour, scenario, dataset and test (MAJ-001).
- [ ] Add the sweep/retention obligation, dataset and scenario (MAJ-003).
- [ ] Add the byte budget and its stop reason to D3/D4 and the contract tables (MAJ-002).
- [ ] Add the upgrade first-open behaviour and the 503 UI state (MAJ-004).
- [ ] Add the ownership alternatives to the Alternatives table and either drop or justify the manifest (MAJ-005).
- [ ] Add FR-017 plus accessibility and state scenarios and tests (MAJ-007).
- [ ] Fix MIN-001..004 and fold OBS-001..003 into the next draft as the author sees fit.

### Next step in the process

Verdict: BLOCK

Review written to: docs/internal/specs/chat-history-paging-spec-review.md

This is grill round 1 of 2 (fixed). Next: team-lead interviews the founder on "Questions for the founder", then the spec author fixes round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the corrected spec at docs/internal/specs/chat-history-paging-spec.md — regardless of this round's verdict.
