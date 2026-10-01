# Orphan-restart recovery acceptance — RED plan

## Observable behavior

A durably restart-canceled tool-call group never returns to model context after a rebuild, reload, later turn or forced pre-turn trim. Only that group, its owned partial results and its bound records disappear from the view; every other message and its original archive address survive. Unsupported recovery evidence never launders an invalid request into a valid one.

## Independent specification sources

| Expectation | Source |
|---|---|
| Exact parsed cancellation discriminator, local binding, exclusions, full-archive scan, original indexes, all three readers | Architect Option A, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-mechanism-ruling-20261001/ruling.md` — Shared pure model view |
| Later orphan reusing an ID gets its own record; repeated recovery does not duplicate the record | Same ruling — Position-scoped recovery idempotency |
| User/control preservation; no archive changes or recovery-driven cursor change; validation before repair | Same ruling — Unchanged constraints; context-overflow ADR MAJ-CW-005/006 |
| Persisted result projections address original `(tool_call_id, archive_line)` | Context-overflow ADR MAJ-CW-002/005; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-orphan-acceptance-tests/contracts/asyncapi.yaml` — ToolResultRecallMark |
| Existing restart behavior and retained audit archive | Tool-registry spec FR-069/088, as reconciled by the architect's explicit one-l spelling ruling |

The supplied coordination log was absent in this fresh worktree. The dispatcher supplied the full ruling, then its external artifact was read directly. No expected message list or address comes from running or reading the new implementation. Existing signatures, fixtures and unchanged error vocabulary are read only to attach assertions to real seams.

## Unit boundary

```text
REAL: JSONL persistence and metadata, session backend, RecoverOrphanedToolCalls,
      assembleMessages, mapWindowMessages, newWindowCheckpoint,
      trimWindowChecked, prepareCallMessages, provider-send rejection.
EDGE: existing recording HTTP endpoint replaces the paid remote model only.
```

The tests use existing runtime entry points instead of calling the not-yet-present helper. They therefore compile on the pre-fix tree and expose behavioral RED, not an undefined-symbol build failure. No production test hook, special flag, fake result, validator replacement or mocked store is introduced. The one dynamic system-prompt envelope is outside this recovery oracle; the entire archive-derived message sequence after it is compared exactly.

## Case table

| ID | Case / class | Input | Contract-derived expected result |
|---|---|---|---|
| R1 | First recovery / state | Unmarked orphan followed by an unrelated system control; call real recovery, then assembly | A bound marker is archived; model history contains exactly the user and unrelated control |
| R2 | Reload / state | Bound cancellation, partial result, preserved controls and later completed group; reopen the real store | Same exact visible sequence and original indexes; bytes and window state unchanged |
| R3 | Later turn / state | New complete and live incomplete groups after a canceled group | Canceled group remains excluded; later messages have their actual addresses, including incomplete live slots |
| R4a/b | Forced pre-turn trim / state | Canceled group in evictable prefix, or canceled group in retained suffix | Exact first whole-turn cut, dropped/remaining counts and metadata; no resurrection; archive bytes unchanged |
| M1 | Current mapping / exact values | Clean request sequence with a pinned envelope and transient recall slots after an excluded group | Only ephemeral slots map to -1; every archived live/control message maps to its explicit source index |
| M2 | Projection / exact values | Projection keys for a canceled partial result and a later result; checkpoint the independent clean candidate | Canceled slot stays excluded; live result is projected at its real address; unrelated fields/messages and persisted metadata unchanged |
| M3 | Cursor/anchor boundaries | Skip at 0, within/after canceled entries, at a live suffix, at Count; explicit user anchors | Preserve the selected anchor/suffix and filter pairs together; no compacted indexes or inferred cursor |
| P1 | Parallel cancellation / equivalence | One partial result and multiple markers for one parallel group; other parallel groups before/after it | Only the one abandoned group and its records are removed; all completed groups and controls survive |
| I1 | Repeated ID / state | Completed, canceled and live complete/incomplete groups share call_0 | Only locally bound canceled groups are excluded; later live incomplete group still rejects |
| I2 | New orphan with old ID / state | Earlier call_0 cancellation followed by a later orphan call_0; recover twice | Exactly one new marker for the later occurrence; second recovery appends nothing; both users/narration survive |
| N1 | Marker negatives / errors | Absent, truncated/non-object JSON, substring/nested text, wrong role/type/reason, missing/empty/non-string ID, undeclared/resolved ID, graceful marker without ID | Entire raw sequence remains visible/addressable; incomplete group rejected with the unchanged specific validation error; zero provider requests |
| N2 | Binding boundaries / errors | Marker before declaration, or after a newer user/assistant/plain narration | No backward search across the boundary; genuinely incomplete group remains rejected |
| N3 | Malformed/duplicate structure / errors | Empty or duplicate declared IDs, duplicate/unowned/empty-ID results; even with matching or duplicate markers | Invalid sequence remains visible; exact validation rejection before send, never an exclusion-manufactured pass |
| N4 | Completed group / negative control | Fully completed group with one or duplicate matching marker(s) | Complete group and unbound markers remain visible and unchanged; validation succeeds and real HTTP endpoint receives retained calls/results |
| N5 | Empty/unbound / boundaries | Empty archive or only unbound marker/control | No removal or cursor movement; exact original sequence retained |

The negative table deliberately exceeds 30% of executable leaf cases. There is no numeric product limit in this helper contract: cursor boundaries and zero/one/multiple group/result/marker cardinalities are the meaningful boundaries. Token-window sizing itself is not this pack's oracle; a generous fixture budget prevents accidental size relief in the recovery/projection tests.

## Broken implementations these tests are designed to catch

| Probe for fresh CHECK | Expected failing row |
|---|---|
| Leave assembly on raw WindowHistory, or filter only the initial recovery argument | R1, R2, R3 |
| Leave mapper on raw history / use compacted positions | M1, M2, M3 |
| Leave forced trimmer on raw history | R4a, R4b |
| Replace position binding with session-global canceled-ID set | P1, I1, I2 |
| Authorize substring/wrong-reason/graceful records or search across user/assistant boundaries | N1, N2 |
| Ignore malformed/duplicate declarations/results or suppress the validation error | N3; zero-request oracle |
| Strip whole tails, controls or completed groups | P1, R1/R2, N4 |
| Rewrite the archive, fabricate a result, change Skip for recovery | All exact archive/metadata assertions |

These are planned breakage predictions, **not mutation receipts**. This RED author never CHECKs its own suite. GREEN and at least three actual scratch-clone mutation probes are **deferred to a fresh CHECK instance**, under the qa-lead separate-context rule.

## Deliberate gaps and handoff

| Gap | Reason / owner |
|---|---|
| New private helper called directly | Missing on pre-fix tree; its three real callers are exercised instead |
| Actual GREEN and mutations | Separate-context CHECK duty; do not infer them from static fault predictions |
| Full Go suite, race and platform matrix | CI is authoritative; local execution is one locked, narrowly scoped test symbol only |
| Live UI/session campaign | Not this test-only RED dispatch; existing ProcessDirect regression remains an additional integration gate |
| Recovery write-failure durability and duplicate audit-event count | Not injected here; exact archived marker multiplicity is tested, existing audit regression remains required |
| Performance/footprint guarantees | No benchmark or unmeasured latency claim |
| Exact dynamic pinned prompt and recall-hint whitespace | Outside recovery behavior; mark schema fields/address and retained structure are asserted, not producer-dependent encoding order |
| Identical valid markers for one already-bound incomplete group | Ruling permits multiple records for a parallel group but does not declare duplicate valid marker multiplicity invalid; duplicate records cannot authorize removal of a complete or structurally invalid group |

This RED author changed no production files, user-facing documentation or files in the implementer's worktree. Matching behavior documentation belongs to the implementing lead; the architect named `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-orphan-acceptance-tests/docs/memory.md` as the documentation TODO. After the RED receipt was finalized, the squad lead separately combined the production fix into this branch; that combine is not a production edit or GREEN certification by this author.

## Execution record

**RED executed, not GREEN-certified.** The single narrow tagged run used tests-only commit `0966af8d3bba3d3d2cce14f26c9a622098f29309` on unfixed production baseline `380af72061a3b0c798f1a58b1fe9c71d71cf1228`. The raw Go exit was **1**, with **76/76 executable leaf cases collected: 26 FAIL, 50 PASS, zero skipped**. The failure log contains behavioral expected-versus-actual failures, not a missing-symbol or collection failure. Passing baseline controls have not been described as observed RED or mutation-proven.

| Group | Executable leaves | FAIL | PASS |
|---|---:|---:|---:|
| Rebuild/reload/later turn/forced trim | 5 | 5 | 0 |
| Mapping/projection/cursor-anchor boundaries | 13 | 7 | 6 |
| Parallel cancellation/reused IDs/idempotency | 6 | 6 | 0 |
| Unsupported markers/binding boundaries/invalid structure | 43 | 3 | 40 |
| Completed/unbound/parsed-JSON controls | 9 | 5 | 4 |
| Total | 76 | 26 | 50 |

| Receipt | Immutable evidence |
|---|---|
| Exact command, environment, frozen tree and original file hashes | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-acceptance-red-20261001/red-manifest.json` |
| Named failures and expected-versus-actual output | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-acceptance-red-20261001/red.log` |
| Direct exit code and all 76 named leaf results | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-acceptance-red-20261001/red-receipt.json` |
| Raw log SHA256 | `6d2ebb366827a90a62186269215fc61b2de29de3fe406993c8b35b9907b5c8a3` |

The raw receipt was finalized at **2026-10-01T15:44:33.984Z**. The branch reflog records the separate squad-lead combine `d8a5f6def01252b18a2b253ef20102986f0dd32f` at **2026-10-01T15:45:29Z**, after that receipt. The dispatcher confirmed that combine was deliberate. All seven test-source hashes still match the frozen RED manifest; this later plan-only execution update does not change the tested source. No extra RED, GREEN or mutation test process was launched by this author.

### Unresolved assertions — retained, not worked around

| Case | Observed RED and consequence | Disposition |
|---|---|---|
| N1 `wrong_role_tool` and N3 `empty_result_ID_in_group` | Raw malformed-ID tool messages remain visible, but mapping returns -1; exact-address assertions abort before their later provider-rejection/zero-send checks | Keep exact original-address expectations and report the two unexecuted dependent checks. Dispatcher/architect adjudicates the malformed-address contract; no assertion is loosened. |
| Both N4 completed-group variants | Validation succeeds, one actual HTTP request is recorded and the fixture response is returned; the final assertion expects trailing systems in `received[1:]` | Existing send-time normalization composes system contents into the first system message. The log does not print `received[0]`; exact sent system retention was not verified. This is a boundary/oracle question, not established recovery data loss. Hold the assertion unchanged pending the architect's ruling. |
| M2 projection details | The pre-fix run fails exact archive mapping before dependent recall-mark field assertions | Those later field assertions were not executed in the RED run; GREEN and mutation evidence must establish them. |

The dispatcher has routed the N4 boundary question for an architect ruling. No outcome is presumed. A possible strengthening for malformed-ID negatives is to run their existing exact rejection/zero-send checks before the exact mapping assertion; this has **not** been performed and would retain every assertion.

Step 4 item 2 (GREEN), item 3 (mutations), and the Proof-of-failability checklist remain **deferred to a fresh CHECK instance**. Full-suite/race/platform results belong to CI. The unchanged `TestRecoverOrphan_Wired_In_SessionLoadPath` remains an additional integration requirement, not a result certified by this pack's author.

Code correct and tested: **not certified — this is RED evidence with unresolved assertion boundaries, not a CHECK verdict.**

Reachable by a user or agent: **not certified in this test-only dispatch.**
