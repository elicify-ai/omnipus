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

Production files, documentation and the implementer's worktree are untouched. Matching behavior documentation belongs to the implementing lead; the architect named `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-orphan-acceptance-tests/docs/memory.md` as the documentation TODO.

## Execution record

Pending RED receipt. Tests will run against the unfixed `380af72061a3b0c798f1a58b1fe9c71d71cf1228` production tree, with the test-only pack added. Named failure output, raw command exit code and collected leaf-case count will be reported; negative controls already passing on pre-change code will be distinguished from observed RED. GREEN, mutation, full-suite and reachability certification remain unverified/deferred to CHECK and CI.
