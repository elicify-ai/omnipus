# Backend RED handoff — coverage versus blockers

Base: `d54ac55a33312f7bdfb8a500fe08853bcb05179b`. Own work branch: `red/1266-be`.
Worktree: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be`.

This is a tests-only RED pack, **not a finished feature and not an independent CHECK**. Missing-seam fatal cases are executable blockers, **not behavioral RED coverage**. All supplied expectations are spec-first. No production, contracts, existing assertions, CI configuration, test exclusions or runtime permissions were changed.

## Traceability and readiness

| Test | BDD IDs | Executable behavior now / explicit missing-seam boundary |
|---|---|---|
| T01 | B01,B02,B17 | Real messages-handler envelope/suffix/actual-lookahead table, exclusive older/newer and closed-window continuation; exact0/1/19/20/21/39/40/41/99/100/101 cases. |
| T02 | B19,B20,B22,B27 | Real issued tokens compared to independent D2 length-prefixed hash oracle, frozen6-vector instrument, append stability, foreign/unknown404. Rollover/reconstruction/tombstone/collision-binding internals remain BLOCKED. |
| T03 | B03,B13 | Real two-clear250/60/0,1,19,20,100 fixture, explicit one-barrier crossing, marker-like text and unchanged canonical bytes. Model-context internal-state observer remains unavailable. |
| T04 | B18,B24 | Real canonical settle/guard-mismatch/projection/retract/disposition/cancel fixture with hand-derived expected fields; atomic101-row case. Full special/media/span/room_ref emitter inventory remains BLOCKED until the typed origin seam and Train3 catch-up. |
| T06 | B21,B22,B31,B40 | BLOCKED: indexed cut/read observer/combined bounded recovery APIs absent; explicit prefix1000/100000, limit40/100 and pending0/1/63/64/65 datasets. No timing substitute. |
| T11 | B19,B20 | Real raw-query REST and socket/read-loop validation on/off, exact error messages/statuses, registered auth wrapper, missing/hidden session access before bad limits, reversed/outside-range and foreign404-before-order cases using independently derived checked positions. Malformed unrecognized-key exact error copy remains BLOCKED. |
| T12 | B01,B02,B03,B18,B24,B31,B37,B38,B43,B46,B51 | Real REST/detail/snapshot selected-page metadata and ordinary-frame origins; separate real tool-only start/result ownership and pending-no-fabricated-result case; unavailable metadata-only detail. Exhaustive persisted source variants, divergent carrier exact-meter internals and consumer UI tests are not claimed. |
| T13 | B04,B05,B06,B26,B31,B43 | Real hub snapshot/projection/done/complete/held-live ordering and uncut41-frame incremental regression. Complete current-card/span/result-coverage fixture remains BLOCKED; no browser old-reader preservation claim. |
| T19 | B27,B41,B50 | BLOCKED core retained-set/tombstone/deletion-gap observer matrix plus real Unix stale-handle instrument. Native Windows partial busy-file test and named blocked generation cases use windows constraints. |
| T22 | B30,B35,B36,B44,B45 | Real standalone-build/CLI tests prepared for dry-run before backup and complete manifest/independent trees. Command currently absent; all are BLOCKED at the named missing-main check. Publication/idempotence seam is separately BLOCKED. |
| T23 | B33 | Real request/log-capture instrument, required outcome/count fields and body/parameter/result/credential/query/raw-position/private-mark canaries. Storage fault barrier remains BLOCKED; planted-leak mutation is deferred to independent CHECK. |
| T25 | B30,B35,B36,B44,B45 | BLOCKED: standalone command/shared constructor/real bounded reader and stopped-receipt callable input contract absent; do not copy the reducer into tests. Frozen D2 vectors are supplied for shared-reader agreement. |
| T26 | B32,B50 | BLOCKED: immutable content-read/captured-cut/handle barriers absent. Local named fatal proves blocker visibility only, not append progress or lock correctness. Native Windows counterpart is separate. |
| T27 | B23,B24,B37,B38,B39 | Real200-KiB byte cut,256-KiB floor override, normal40/20, advisory100-floor and >4-MiB101-row atomic behavior. Exact near8-MiB×100 admission/meter probe and advertised-budget semantic validator remain BLOCKED; logical screen geometry is frontend/E2E scope. |
| T29 | B22,B40,B41,B42 | BLOCKED: real publication fault/recovery/read-count APIs absent; all four crash stages,0/1/63/64/65, mixed32+32 vs33+32, torn/framed corruption, private coverage and coalesced-owner cases remain loud. |
| T30 | B30,B35,B36,B44,B45 | Real built CLI dry-run/manifest/empty/zero-byte/Unicode/golden fingerprint, actual extra/missing/tampered backup, unconverted-first and missing stopped evidence tests prepared. Main/schema/native receipt/cutover/publication matrix absent and BLOCKED. |
| T31 | B04,B20,B31,B40,B42,B43,B49 | Real canonical-only old-data copy omits indexes without guessing their names, durable append/no partial baseline, metadata-only detail, typed live_only/retry/no resend, incomplete-controls negative. Actual running-engine Stop/current interaction/attention-seen proof remains BLOCKED, not inferred from binding. |
| T32 | B18,B23,B37,B38,B46 | BLOCKED: one prepared injected request factory/meter and side-effect counters absent. Nil/error/negative/overflow/inconsistent/cancel fixtures must not be replaced with guessed weights. |
| T36 | B27,B41,B50 | Native Windows actual file-open sharing-busy positive control, skip/continue/two real later deletions, handle-close next-sweep retry. Retained-generation/token+rank-only privacy still BLOCKED. Linux Fly cannot certify it. |

## No invented production API contract

The contract owner confirmed Wave1 introduced wire contracts only: **no agreed Go reader/options/fault signatures and no derived-index artifact names**. This pack deliberately does not invent them or import gateway into session. Existing callable handlers/hub/store methods and D13's explicitly named CLI phases/flags are used; absent internal behavior uses `t.Fatal("BLOCKED: … not implemented — required by …")`, never test-only production logic, reflection-based fake API matching, skip or a mere symbol-exists green.

New diagnostics use the specification's proposed `history_page` event and spelled-out field names (`limit`, `min_rows`, `row_count`, `source_group_count`, `projected_bytes`, `soft_byte_budget`, `duration_ms`, `has_earlier`, `has_later`, `stop_reason`, `retention_generation`, `outcome`). These are expectations to confirm with the implementation, not a claim that they already exist. Exact currently-unnamed internal meter/read/publication contracts need the architect/backend handoff before the blocked tests can become behavioral tests.

## Execution and independent proof

Evidence directory: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021`.

Initial local command under the supplied two-slot wrapper, with tags goolm,stdjson and CGO_ENABLED=0: `TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks/chat` at initial commit `c9bfd984e180c8da777fb52d47122df10e473408`; exit1 and named BLOCKED failure in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/local-red.log`. This is **blocker visibility**, not behavioral lock RED.

A later single exact behavioral case, `TestChatHistoryPage_SelectsNewestAndActualLookahead/count21_limit20`, ran at pushed tests-only commit `943c4d262e9967351f457fffeb2611b203e9c07b`: exit1, named failure `json: cannot unmarshal array into Go value of type generated.SessionHistoryResponse`, message `D3 requires the generated page envelope, never the old full-history array`, in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/local-behavior-red.log`. This is real missing-envelope RED, not a compile error. It does not prove every other case ran.

Fly go-tier dispatch against that same pushed943c4d262 SHA exited2 **before any gate**, because the dispatcher could not read the deployed worker checksum. Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/fly-go-dispatch.log`. The already-running machine was not owned, changed or stopped by this run. Team-lead instructed leaving the worker alone while chief CI-tooling work is in progress and agreed to exact narrow local behavioral cases, one at a time under slotrun; no package-wide local regex/suite is authorized or run.

Any later execution receipts live in the same external evidence directory and must name the final tested SHA. Native Windows named tests/build and the new required history-index-repair wiring are backend-owned and currently UNVERIFIED. Existing negative/regression controls may already pass; do not fabricate that every new test showed behavioral RED.

Independent D2 six-vector and D13 three-vector recomputation/inventory is saved in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/static-check.log`; final staged scope must include new files, not just tracked git diff output. Both JSON fixtures were frozen from Python hashlib/struct, not from product output. The Go helper cross-checks the test-side D2 oracle before checking real issued tokens.

GREEN, mutation probes, restored GREEN and Proof-of-failability are **deferred to a fresh CHECK instance**, not met. The author must not audit this same pack. No full local suite, local race/lint/build, browser run, native Windows run or UAT is claimed.

skills: omnipus-shared-rules, elicify-test-writing, test-integrity-audit (failure-mode prevention only), gitnexus-impact-analysis, github-cli.
