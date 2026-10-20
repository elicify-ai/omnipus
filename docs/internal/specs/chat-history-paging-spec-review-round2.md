# Adversarial Review: Chat history paging

**Document reviewed**: docs/internal/specs/chat-history-paging-spec.md (commit `e1e470b21`, branch `feature/1266-chat-history-paging`)
**Mode**: Spec
**Round**: Round 2 of 2 (final)
**Review date**: 2026-10-21
**Verdict**: REVISE

## Executive Summary

Round 1's fixes are genuine: all 16 round-1 findings are addressed in the normative text and I found no leftover of the removed jump-to-message work. The fixes introduced new defects. There are 0 CRITICAL, 5 MAJOR, 5 MINOR and 1 OBSERVATION findings. The largest new risk is a consequence of the greenfield decision: the spec forbids any automatic tail catch-up, so a routine unclean shutdown can turn a session's history into a permanent 500 that only the stopped-instance script can clear, while the repo's existing ordinal index heals the same crash window automatically (MAJ-201). The offline script is specified as a second implementation of the projection rules in Python (MAJ-202) and cannot be built as written because its backup manifest has no format or producer (MAJ-203).

Frontend coverage (lenses 5, 8, 9, 10) is now as thorough as backend coverage: D8 names catalogued Button/ErrorState/Skeleton, focus and announcement rules, and B28/B29/T20/T21 exist. The remaining frontend gaps are small (MIN-202 and the consumer inventory in MIN-205).

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 5 |
| MINOR | 5 |
| OBSERVATION | 1 |
| **Total** | **11** |

### Founder decisions recorded as accepted (not re-raised)

| Decision | Treatment in this review |
|---|---|
| Jump-to-message descoped (Non-goal) | Accepted. I checked for residue (B14-B16, T17, FR-011, "around", "neighborhood", permalink): only negations remain. |
| Greenfield, no product migration path or preparing state; separate one-off script | Accepted. I review the script's correctness and buildability, not whether it should exist. |
| 500+100 window and tail cache (MAJ-008) | Accepted as drafted. |
| Page byte budget with a two-screen floor and whole groups | Accepted. I review correctness and testability only (MAJ-204, MIN-201, MIN-203). |
| Train 3 / interim fix: build now, catch up per landing, only landing order gated | Accepted. I confirmed `origin/work/train3-approvals-rooms` is still `573c3674462650ce0f7af8e3e02de0fca50ae295` and `fix/chat-replay-render-once` still has no ref in `git branch -r`, as C21 says. |

---

## Round 1 verification (finding by finding)

| Round-1 finding | Spec claim | Verified against spec text and code | Result |
|---|---|---|---|
| CRIT-001 target route unreachable | Descoped | No target route, loader, producer or test remains; only the Non-goals and Decisions rows mention it. | Fixed |
| MAJ-001 snapshot while reading | D7 keeps the reader, refreshes the tail and the closed range | `src/store/chat/slices/catchup-frames.ts::handleSessionSnapshotFrame` does call `applySnapshotHistoryWipe` today, so D7 is a real target change; behaviour, failure and expiry rows are specified. Two gaps remain: MIN-202. | Fixed with gaps |
| MAJ-002 no byte bound | D4a soft 4 MiB, floor, atomic groups | Arithmetic in D-B1/D-B1b/D-B2 is correct (20 x 201 KiB = 4,020 KiB fits 4,096 KiB; 21 x 200 KiB = 4,200 KiB does not; 40 x 8 KiB is far under). New defects: MAJ-204, MIN-201, MIN-203. | Fixed with new defects |
| MAJ-003 retention | C18/D14 | `pkg/session/archive_expiry.go::sweepRolledPartitionsLocked`, `removePartitionFn`, `rowResolvable` exist as cited. New defects: MAJ-201 (stale index after crash), MIN-204. | Fixed with new defects |
| MAJ-004 first open after upgrade | Greenfield plus offline script | The 503 and preparing design is gone. New defects: MAJ-201, MAJ-202, MAJ-203, MAJ-205. | Fixed with new defects |
| MAJ-005 ordinal manifest | Per-frame `HistoryEntryOrigin` | All 15 cited component frame schemas exist (MediaFrame is inline in asyncapi.yaml) under `contracts/components/schemas/`; the manifest is gone from REST and WS. | Fixed |
| MAJ-006 Train 3 and interim | Build now, chat parser, `room_ref`, PRE-RED-1 | `RoomHistoryResponse.yaml`, `pkg/rooms/history.go`, `src/store/rooms.ts::loadOlder` exist at `573c3674`. `room_ref` is in D4/D-P1c/FR-005. | Fixed |
| MAJ-007 accessibility | D8, FR-017, B28/B29, D-K1/2, T20/T21 | `src/components/ui/error-state.tsx` has `role="alert"`, `skeleton.tsx` has `aria-hidden`, `button.tsx` sets `aria-disabled` when pending, all as D8 says. | Fixed |
| MAJ-008 | Accepted by founder | Not reopened. | Accepted |
| MIN-001 estimate | A-driven `estimateSize`, D-G1d | Arithmetic in D5 and B07 checks (800/400 -> 20/4; 1600/10 -> 100/100). | Fixed |
| MIN-002 precedence | One order for REST and WS | D11 states it; D-V2 covers it. | Fixed |
| MIN-003 lock scope | Capture under shard, I/O outside | `pkg/session/archive_day_store.go::ReadAtRaw` does hold `s.mu` across file I/O as C3 says; D1/T26 address it. | Fixed |
| MIN-004 log canaries | B33/D-Z1/T23 | Present, with a planted-leak control. | Fixed |
| OBS-001/002/003 | Incorporated | D2 alternatives, D8 catalogue names, DOC-2 API subsection. All three doc pages and the cited headings exist. | Fixed |

---

## Findings

### CRITICAL Findings

None.

### MAJOR Findings

#### [MAJ-201] Derived-index crash consistency is unspecified, and the spec forbids the bounded self-heal the repo already uses

- **Lens**: Incompleteness / Inconsistency with the tree
- **Affected section**: D1 "Derived publication" and "Cold/fault"; Deployment/Runtime paragraph 1; D14 (sweep); B22; D-R4
- **Failure scenario (a, append)**: The gateway is killed, the laptop loses power or the process is OOM-killed after an archive record is durable but before its index metadata is published. At next start the covered tail does not equal the archive tail. D1 says this "fails visibly until operator repair" and forbids "automatic tail scan/rebuild". The session's history (the main session included) returns the fixed 500 on every open. Recovery is the stopped-instance, full-backup, dry-run/apply script. Every accepted archive append (including private model records) is a crash window, so this is a routine event, not an exotic one.
- **Failure scenario (b, sweep)**: The process dies after `removePartitionFn` deleted a partition but before the index drops it. The next sweep finds nothing to remove (it counts only files it deletes), so the generation never advances. D14's "reader reselects against the new retained generation" has nothing to trigger on, and the stale lookahead keeps advertising the gone prefix. The spec states the goal ("must not perpetually advertise a gone prefix") but names no mechanism for this crash case.
- **Evidence**: `pkg/session/ordinal_index.go` (package header, "PUBLICATION") already solves the same shape: "A crash between them leaves one complete row with no offset; the next open completes it from the saved tail ... never a lifetime recount", and cuts a torn trailing row. `pkg/session/archive_expiry.go::sweepRolledPartitionsLocked` returns a count only, not which keys were removed. Founder R1-Q3 forbade backfilling pre-feature data and a lifetime rebuild; it did not address completing a bounded crash tail, and the spec's own D1 invariant already allows reading "the one lookahead".
- **Recommendation**: State the write order (archive durable, then index row, with an intent or covered-end marker) and a bounded reconciliation: at open, if archive end exceeds covered end by a delta of at most a small stated bound, index only that delta; anything else (gap, mismatch, bound exceeded) stays the D11 error. For the sweep, make the reader treat a missing partition that the index still lists as expired and publish that fact (generation advance) on first detection. Add BDD and dataset rows for crash between archive and index, and between partition delete and index update, with the negative control that a delta over the bound still errors. This needs the founder's choice because it relaxes the "no automatic repair" wording (Q1).
- **Severity**: MAJOR. **Certainty**: High that the spec forbids it as written; Inferred on frequency (depends on the append protocol the backend-lead picks).

#### [MAJ-202] The offline script must re-implement the projection rules in a second language, with no mechanism that stops silent divergence

- **Lens**: Infeasibility / Overcomplexity / Testability
- **Affected section**: D1 index contents, D13, T22/T25, D-O1/D-O3
- **Failure scenario**: D1 says the index holds "correction/disposition locations and relevant span dependencies, canonical duplicate ownership, clear marker, previous/next eligible links". Building those from canonical bytes means re-implementing in Python the rules of `pkg/session/transcript_effects.go::applyToolCallEffects`/`readTranscriptMerged`, `input_disposition.go::InputDispositions`, `unified_write.go::deriveCancelTruncation` and duplicate-ID last-wins. A Python edge case that differs from Go (a retract of a retract, a cancel in a far-day partition, an equal-timestamp duplicate) produces an index the Go reader trusts, so the founder's real history is shown wrong with no error. The only guard is T25, which compares output on the fixtures QA freezes; any case QA did not think of passes.
- **Evidence**: `pkg/session/transcript_effects.go` (387 lines, the effect application rules), `pkg/session/input_disposition.go`, spec D13 "Backend-lead owns the private index-format compatibility" and Prerequisites ("Separate optional Python3 standard-library script"). Python is the architect's choice; the founder's decision said "a SEPARATE one-off migration script", not which language.
- **Recommendation**: Build the offline tool in Go as a non-shipped command that links the same index writer and projection code the product uses (single source of truth), keeping every D13 precondition (stop receipt, backup verification, dry-run, idempotence) unchanged. If Python stays, add a required differential test that runs the Go reader's own full projection (`ReadTranscript`) against a script-built index over a randomized corpus including every effect type, and make that a CI gate rather than a QA fixture list. Founder choice: Q2.
- **Severity**: MAJOR. **Certainty**: High that two implementations are required as written; Inferred on divergence likelihood.

#### [MAJ-203] The script cannot be built or run as specified: manifest format and producer are undefined, the conversion sequence is a trap, CI never runs the script's tests

- **Lens**: Incompleteness / Testability
- **Affected section**: D13 "Full backup check", "Current input only", "Exit codes", "Operator sequence"; Prerequisites; T22
- **Failure scenario (a)**: `--backup-manifest` must list "normalized relative paths, kind, size and SHA-256", but no schema is defined and nothing produces it (the operator sequence says "produce/verify full backup + manifest" with no tool). QA cannot write independent T22 fixtures without a format, and team-lead has no way to create one for the live instance.
- **Failure scenario (b)**: D13 refuses unconverted input and verifies the live bytes equal the backup bytes. The C5 cutover runs at boot: `pkg/session/unified_conv.go::CutoverSavedChatsAtBoot` rewrites partitions and is "idempotent", but a never-converted instance converts on first start. The sequence backup, start (cutover rewrites bytes), stop, script then fails with "source drift" against the backup, and the spec offers no "convert first, then back up" step. Whether the founder's live data is already converted is Unknown.
- **Failure scenario (c)**: `.github/workflows/build.yml` runs `python3 scripts/ci-candidate-artifacts.test.py` by explicit name, and `scripts/go-test-shards.py` only shards Go packages. `scripts/migrations/test_chat_history_index.py` therefore runs in no gate unless someone adds a step; the spec only says "QA verifies CI discovery", while backend-lead owns the workflows and none of the three CI tiers (`go`, `node`, `xplat`) lists it. A script that "ships tested" but whose tests never run is exactly the false green the root `CLAUDE.md` warns about.
- **Failure scenario (d)**: Exit semantics for a per-session refusal ("unconverted input", corrupt line) are not mapped to `2` (precondition) or `1` (integrity); D-O2 and B35 say "exact nonzero class" without defining it for this case.
- **Evidence**: spec D13 rows above; `pkg/session/unified_conv.go::CutoverSavedChatsAtBoot`; `.github/workflows/build.yml` (explicit Python test step); `scripts/go-test-shards.py` (Go only).
- **Recommendation**: Define the manifest as a documented JSON schema and ship its generator in the same tool (so the backup step is `tool manifest --root ...`). Add the operator step "if the tool reports unconverted sessions: run the product once to completion of the cutover, stop, take a fresh backup and manifest, then dry-run" and a refusal class `unconverted` (exit `2`). Name the CI step that runs the script's tests and who adds it. Map per-session refusals to an exit code in the table. Add the missing runbook page (see MIN-205 note on docs).
- **Severity**: MAJOR. **Certainty**: High for (a), (c), (d); Inferred for (b) (depends on the live instance's conversion state).

#### [MAJ-204] The byte meter lives in two packages and has no defined home

- **Lens**: Infeasibility / Contract-first gaps
- **Affected section**: D1 "Early stop", D4a "Identical carrier sizing", T27, D-B1
- **Failure scenario**: D4a meters "the larger of the generated public REST history envelope and its generated historical WS page-info/content replay payloads (UTF-8 JSON bytes)" for every candidate group, using "the same server-side projector/meter for REST/detail/snapshot selection". D1 makes selection a `UnifiedStore` (`pkg/session`) job with at most one lookahead. WS replay payloads are produced by the gateway emitters (`pkg/gateway/replay.go::streamReplay`, `emitFlatToolCall`, `dispatchSpecialEntry`, `buildEntryMessage`, 1,985 lines), and `pkg/session` cannot import the gateway. As written, one team will pass a callback from the gateway into the store, another will estimate WS size from REST size, and the two carriers then select different ranges, breaking the parity invariant (FR-002, B18/T12) the spec depends on. Metering the WS form also requires dry-run building frames, with their special-frame side effects, for each candidate and the lookahead group, doubling projection work on the bounded path D1 wants to keep cheap.
- **Evidence**: `pkg/gateway/replay.go` symbols above; spec D1 ("UnifiedStore-owned bounded chat projection reader") and D4a rows "Identical carrier sizing", "Byte stop allowed only after floor".
- **Recommendation**: Decide the meter's seam in the spec: either (a) the byte weight is computed from the public `Message` projection only (one deterministic function in `pkg/session`, with WS frames treated as a bounded multiple verified by a test), or (b) the store accepts an injected `weigh(group) int` supplied by the gateway for both REST and WS so there is one implementation. Add a parity test that the REST and snapshot paths pick the same range for a fixture where REST and WS sizes differ most (tool-heavy versus special-frame-heavy). State whether a bad weigh function fails the page or falls back.
- **Severity**: MAJOR. **Certainty**: High on the layering (`pkg/session` has no gateway import path); Inferred on the implementation each team would pick.

#### [MAJ-205] Behaviour on a session without an index is specified only for reads

- **Lens**: Incompleteness / UI states
- **Affected section**: D11 "Missing/corrupt/incompletely covered required index", "Attach required index missing"; D1 "Derived publication"; B31; D-O4; US1 acceptance 3
- **Failure scenario (a)**: The feature is deployed before the script has run (or a session was created by a path that does not create an index). The person types a message in the main session. The archive append succeeds (canonical data is "not rolled back"). Does the append create an index containing only the new record, so the session now has a valid-looking but partial history, which is the silent partial result the spec forbids? Or does it leave the session unindexed so the covered-tail check fails? Or does it refuse the send? None of this is specified; B31 and D-O4 only test reads.
- **Failure scenario (b)**: For WS, a missing index "uses the existing ErrorFrame/replay-error completion path, no snapshot wipe or successful catch_up_complete". If attach never completes, the person cannot see live streaming, approvals or the Stop control for a session whose agent is still running (ADR-082: execution is independent of the UI). D8 promises "send/Stop remain usable" only for page-request failures, not for a failed attach.
- **Evidence**: spec D11 rows quoted; `pkg/gateway/websocket_replay.go::failCatchUp`, `handleAttachSessionWithAck` (attach completes only via `catch_up_complete`); spec D8 "Older/newer/range failure".
- **Recommendation**: State the append rule for an unindexed or mismatched session: the append never creates or extends an index (covered end stays at the old value, so the later script run indexes everything from canonical bytes), and sending stays allowed. State what the SPA does when attach fails with the history-unavailable error: bind the live channel for controls and Stop with a persistent ErrorState/Retry in place of history. Add B31 variants "append to an unindexed session" and "attach fails, Stop still works", with a dataset row.
- **Severity**: MAJOR. **Certainty**: Inferred (append path not read at index-creation detail; the spec's silence is verified).

### MINOR Findings

#### [MIN-201] The two-screen floor is client-asserted, so any client can switch the byte budget off

- **Lens**: Security (denial of service) / Testability
- **Affected section**: D4a "Floor derived from visible geometry"; D11 `min_rows`; D5 sample average
- **Failure scenario**: `min_rows` is accepted as any integer from 1 to `limit`. A client sending `limit=100&min_rows=100` forces 100 whole groups regardless of bytes (up to 8 MiB each, `ArchiveDayStore` admission), which is the unbounded page round 1 raised in MAJ-002. A polluted sample average (A is `max(1, ...)`, so 1 px is legal) does the same inside the SPA. The founder accepted a soft budget, so this is not a re-raise of the budget; the point is the floor has no server-side sanity bound.
- **Evidence**: spec D4a, D11 min_rows row, D5 "Average".
- **Recommendation**: Record explicitly that `min_rows` is advisory and that the budget is not a defence against an authenticated caller (accepted risk, one sentence in D4a), or cap the effective floor at what the server can justify (for example `min_rows <= ceil(2 * max_viewport / 20)`). Add D-B4 rows `min_rows=limit=100` with 8 MiB groups so the accounting (`byte_budget_exceeded`, `budget_override=height_floor`) is asserted.
- **Certainty**: High.

#### [MIN-202] Refresh failure, expiry and window-refresh budget behaviour have no BDD "Then"

- **Lens**: Testability / UI states
- **Affected section**: D7 rows "Window refresh in progress / failed", "Some rows/anchor expired", "Entire held range expired"; B26; D-R2b; D-X5; D3 "Existing-window refresh"
- **Failure scenario**: B26's Then covers only the successful refresh. The failure copy ("Could not refresh loaded messages." + Retry, out-of-date notice, prefetch paused), the partial-expiry announcement and the entire-range-expired state appear only in D7 prose and in dataset rows labelled "B26,B27"; B27 asserts flags and cursors, not UI. A build that never shows the expired-range state, or never pauses prefetch, passes every scenario. Separately, D3/D4a do not say whether `window_refresh` pages apply the soft byte budget and `min_rows` (which would make a 500-group refresh take more than the 5 requests D7 implies).
- **Evidence**: spec B26/B27 text versus D7 table; D4a "Page accounting".
- **Recommendation**: Add two scenarios (refresh failure with Retry and prefetch paused; held range partly and fully expired with the exact texts and focus fallback) with T24 as the E2E; add one sentence in D3 stating that refresh pages ignore the byte budget (they are bounded by the saved interval) or obey it with continuation.
- **Certainty**: High.

#### [MIN-203] `soft_byte_budget` is a one-value enum in the wire schema

- **Lens**: Contract-first gaps
- **Affected section**: D10 `SessionHistoryPageInfo`; B23
- **Failure scenario**: The 4 MiB default is described as a "design estimate, not measured". Declaring `soft_byte_budget` as `enum 4194304` bakes it into the generated Zod validator. When the default is tuned, a stale embedded SPA or cached tab rejects every page as "contradictory metadata" (B23 rejects pages with broken page-level metadata). The generated TypeScript/Zod and Go types change together with the binary, so risk is limited to cached tabs and external clients.
- **Evidence**: spec D10 row; D4a "Default: 4 MiB ... not a new operator setting".
- **Recommendation**: Declare it `integer, minimum 1`; keep 4,194,304 as a server constant and test it in T27, not in the schema.
- **Certainty**: Medium.

#### [MIN-204] Tombstone contents, growth and Windows delete-while-open are unspecified

- **Lens**: Security (privacy) / Incompleteness
- **Affected section**: D2 tombstones; D1 "Consistent cut"; D14; T19
- **Failure scenario (a)**: Retention exists to delete old conversation data. Tombstones live as long as the session and the spec says only that they carry "no bodies/results". Whether they keep entry IDs, timestamps, sizes or just token plus rank is not stated, and the index grows with lifetime message count, not retained content.
- **Failure scenario (b)**: The cut holds "read-only bounded record/file views" during slow reads. On Windows, Go's `os.Open` opens without `FILE_SHARE_DELETE` (`syscall/syscall_windows.go`, `sharemode := uint32(FILE_SHARE_READ | FILE_SHARE_WRITE)`), so `removePartitionFn` fails while a reader holds the partition, and `sweepRolledPartitionsLocked` returns on the first removal error, aborting the rest of the sweep with an error. The "sweep raced a read, reselect" case in D14 and T19 cannot occur there; the failure mode is different (a skipped sweep) and untested. Windows is a supported platform.
- **Evidence**: `pkg/session/archive_expiry.go::sweepRolledPartitionsLocked`; Go source above (local toolchain, Verified); spec D14.
- **Recommendation**: State the tombstone fields (token and rank only, no entry ID, timestamp or size) and note that growth is accepted. In D1/D14 require that the reader closes or reopens per bounded read rather than holding handles across slow I/O, or that a held partition makes the sweep skip (not abort) and retry later; add a Windows leg to T19/T26.
- **Certainty**: High for (b) mechanism; Inferred that the implementation would hold handles.

#### [MIN-205] The consumer and regression inventory is incomplete, and docs miss the operator runbook

- **Lens**: Incompleteness / Testability
- **Affected section**: C11, D6 ("all C11 consumers"), Regression Test Requirements, DOC-2
- **Failure scenario**: C11 names `src/lib/api/sessions.ts` and `ChatThread.tsx`. Other readers of the array or the `has_earlier_messages` field: `src/routes/_app/sessions.$sessionId.tsx` (`fetchSessionDetail`, `recordSessionHasEarlier(... detail.has_earlier_messages ...)`), `src/store/session/workspaceEntryFlow.ts`, and the e2e tests `tests/e2e/chat.spec.ts` (helper reads `GET .../messages` as the whole transcript), `tests/e2e/parent-wake-stream.spec.ts` and `tests/e2e/stop-scope-tree.spec.ts` (mocks the route with an array). After the envelope change these fail or, worse, read only the latest 40 rows. The regression table does not list them, and there are about a dozen unit test files under `src/components/chat/` and `src/store/chat/` that mock the array shape. DOC-2 puts the operator script's flags, receipt and recovery steps inside a "Paged session API" subsection of `observability.md`, which is the wrong home; `docs/operations/` has no runbook page and `docs/migration/` holds only the model-list migration.
- **Evidence**: Grep of `tests/`, `src/` for `sessions/.*messages|has_earlier_messages` (listed files); `docs/operations/` and `docs/migration/` listings.
- **Recommendation**: Add these files to C11 and the regression table; add DOC-4: a runbook page (for example `docs/operations/chat-history-index.md`) for the script, with DOC-2 limited to the API and metrics.
- **Certainty**: High (files verified present).

### Observations

#### [OBS-201] The opening top-up and the 2H pixel trigger overlap

- **Lens**: Ambiguity
- **Affected section**: D4a "Actual height check", D5 "Automatic older trigger"
- **Suggestion**: After a byte-cut opening of about 2H, the ordinary trigger (`Dtop <= 2H`) fires anyway, so the 4H "fill cycle" and the prefetch rule are two ways to ask for the same request. State that the opening fill cycle is the same trigger with goal 4H until first reached, so one request owner exists and B39 asserts one chain, not two.

---

## Structural Integrity

### Variant A: Spec mode

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | `Status: Draft` |
| ADR linked (or explicitly stated not needed) | PASS | Cites the session-core ADR and UI-independent turns ADR; no new ADR claimed. The persistent derived-index format and crash protocol (MAJ-201) arguably is an open design decision, but the process makes an ADR optional. |
| Contract changes stated first, citing `contracts/` | PASS | D10 inventory; all cited schema files exist; no contract edited by this spec. |
| API and data section | PASS | D3, D10, D11. |
| UI screens and states (loading/empty/error/partial) | PASS | D8 table; gaps in MIN-202 and MAJ-205. |
| User journey section | PASS | Reachability table and D7. |
| Accessibility and keyboard section | PASS | D8, B28/B29, T20/T21. |
| Design-system components, catalogue-first | PASS | Button, ErrorState, Skeleton checked against files. |
| Security and user promises section | PASS | D2, D11, privacy safeguards; MIN-201, MIN-204. |
| BDD acceptance scenarios, oracle from spec | PASS | B37-B39 arithmetic independently re-derived. |
| Traceability table: requirement -> scenario -> test | PASS | Every FR (001-021, FR-011 retired) maps to BDD and tests; every B-ID appears in at least one FR row and one T row. See gap in MIN-202. |
| Reachability section | PASS | One path per feature entry; operator script covered. |
| Documentation TODOs | PASS with gap | DOC-1..3 are real pages and headings; DOC-4 runbook missing (MIN-205). |

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Crash/fault | Crash between archive append and index publish; between partition delete and index update | B22, D-R4, D-X, MAJ-201 |
| Negative/append path | Append and send to an unindexed session; attach-fails-then-Stop | B31, D-O4, MAJ-205 |
| Differential | Script-built index versus full Go projection over randomized effects | T25, MAJ-202 |
| Frontend states | Refresh failure and expired-range UI | B26, MIN-202 |
| Platform | Windows delete-while-open for sweep and slow read | T19, T26, MIN-204 |
| Script CI | A CI step that runs the Python tests | T22, MAJ-203 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| D-B4 | `min_rows = limit = 100` with 8 MiB groups | Add; assert override accounting (MIN-201) |
| D-O2 | Source drift caused by the boot cutover | Add; assert the new `unconverted` class (MAJ-203) |
| D-R4 | Archive tail ahead of index by 1 record and by N+1 records | Add; assert bounded heal versus error (MAJ-201) |
| D-X3 | Removal failure under an open handle | Add Windows variant (MIN-204) |

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Position token | ok | ok | ok | ok | ok | ok | Hash selector, same-session lookup, 404 for foreign; no authority. |
| Paged read (REST/WS) | ok | ok | ok | ok | risk | ok | Client-asserted floor lets an authenticated caller force large pages (MIN-201). |
| Derived index | ok | risk | ok | risk | risk | ok | Script bug writes a wrong index silently (MAJ-202); tombstone contents (MIN-204); crash leaves history unreadable (MAJ-201). |
| Offline script | ok | risk | ok | ok | ok | ok | Backup and stop checks are strong; manifest has no schema (MAJ-203). |

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | N-A | Spec states no new tool or policy entry (Reachability "Agent tools"). |
| User-facing: named screen/component renders it? | Yes | ChatScreen scroller, catalogued Button/ErrorState/Skeleton, clear-divider "Show earlier messages"; Return to latest. Placement of the Return control within the screen is not drawn, minor. |
| Operator path reachable? | Partly | Script has no runnable manifest producer or CI-run tests as specified (MAJ-203). |
| Test plan describes execution, not just authorship? | Yes | Tests-only CI RED, exact-commit UAT with validator; no "written, not executed" claim. |

## Unasked Questions

1. What is the exact append and index-publication order, and what is the bounded recovery after a crash? (MAJ-201)
2. Where does the byte meter live, and is it one function for REST and WS? (MAJ-204)
3. What does the app do on an append, or an attach, for a session with no index? (MAJ-205)
4. Does the live instance's data already pass the C5 cutover, and what is the step if not? (MAJ-203)
5. Do refresh pages obey the byte budget? (MIN-202)

---

## Questions for the founder

**Q1 (A / B / C): What should happen after a crash leaves the newest messages out of the history index?**
Context and impact: the history index is a small side file the app writes after each saved message. If the app is killed, or the machine loses power, in the short moment between the two writes, the message is safely saved but the index is one record behind. As drafted, the app then refuses to show that conversation's history at all (an error with Retry) until you stop the instance and run the offline repair script, which also needs a verified full backup. Your other index (the recall index) already repairs this situation by itself on next start, by reading only the few records after its last known point, never the whole lifetime. Your "no automatic rebuild" decision was about old data from before this feature; this question is only about the last few records after a crash.
Options: A) Allow a small automatic catch-up of only the records after the index's last known point (bounded, for example up to 64 records); anything larger or inconsistent still shows the error and needs the script. B) Keep it as drafted: any gap needs the script. C) Allow the catch-up, but only when the app starts (not when someone opens a chat).
Recommendation: A. It removes a plausible "my main chat is broken after a power cut" case without touching the lifetime-scan rule.
Answer shape: "Q1 A".

**Q2 (A / B): In which language should the one-off repair tool be written?**
Context and impact: you asked for a separate one-off script. To build the index, it must apply the same rules as the app for edited tool results, cancelled turns, discarded inputs and duplicate IDs. In Python, those rules are written a second time; if one differs, your real history can be shown wrong with no error message. A Go version links the app's own code, so there is one set of rules. Cost: a Go tool needs a build step instead of running as a plain script (it is still a separate, non-shipped program, so the single-binary rule is not affected).
Options: A) Go command that reuses the product's index and projection code; B) Python script plus a mandatory automated cross-check against the Go reader on randomized data in CI.
Recommendation: A.
Answer shape: "Q2 A".

---

## Verdict Rationale

REVISE: no CRITICAL finding, five MAJOR findings. Round 1's blockers are fixed. The new defects come from the fixes themselves: the greenfield choice leaves crash recovery undefined (MAJ-201) and pushes the existing-data path into a second-language tool with no manifest, no CI run and an unsafe boot-cutover sequence (MAJ-202, MAJ-203); the byte budget needs a defined home for its meter (MAJ-204); the missing-index state needs append and attach rules (MAJ-205). Before RED these need decisions or text: Q1 and Q2 from the founder, then the architect's single fix round.

### Escalation to the founder

No CRITICAL finding is open, so nothing is escalated as blocking. MAJ-201 and MAJ-202 are routed through Q1 and Q2 because each needs a founder trade-off decision; if either is answered "keep as drafted", the corresponding failure scenario above remains an accepted risk that should be recorded in the spec's decisions log.

### Recommended Next Actions

- [ ] Founder interview on Q1 and Q2 (team-lead).
- [ ] Architect fix round: MAJ-201 (write order and bounded recovery), MAJ-203 (manifest schema/generator, unconverted-first sequence, exit mapping, CI step), MAJ-204 (meter seam), MAJ-205 (append and attach rules), and per the Q2 answer MAJ-202.
- [ ] Fix MIN-201 to MIN-205 in the same round; add the dataset rows and scenarios listed in the coverage tables.
- [ ] Update `Round 1 disposition` with a Round 2 table; keep Status `Draft` until the founder approves.

### Next step in the process

Verdict: REVISE

Review written to: docs/internal/specs/chat-history-paging-spec-review-round2.md

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews the founder on "Questions for the founder", then the spec author fixes round-2 findings. Any CRITICAL finding still open after that fix is listed under "Escalation to the founder" above for the founder to decide — do not run a third grill round. Once resolved, team-lead plans the implementation (RED / GREEN / CHECK, the 5-reviewer gate).
