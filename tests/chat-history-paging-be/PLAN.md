# Backend RED plan — issue 1266

## Boundary and authority

Readers receive bounded, corrected, same-session history without losing live controls; operators prepare old data only through the separate verified-backup/stopped-instance Go command.

All expected values come from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md`, D1–D15, B01–B51 and its dataset tables. They are not harvested from implementation output. This pack owns T01,T02,T03,T04,T06,T11,T12,T13,T19,T22,T23,T25,T26,T27,T29,T30,T31,T32,T36 (Go only).

Real: UnifiedStore, canonical files, effects/dispositions, REST handlers/auth gate, WS handler/hub/replay, generated public types, separate command when available. Process-edge substitutes only: deterministic time, temporary source/backup files, blocked content-I/O barriers and publication fault injection. No mocks of the reader, reducer, projector, backup validator or repair implementation.

## Cases derived before assertions

| Tests | Inputs | Independent outcome | Authority |
|---|---|---|---|
| T01 | 0/1/19/20/21/39/40/41/99/100/101 ordinary rows, limits 20/40/100; sequential before/after and closed ranges | Exact ascending suffix; continuation iff a retained neighbor exists; empty entries is [] | B01/B02/B17, D-A2/A4, D3 |
| T02 | Equal timestamps, duplicate IDs, Unicode IDs, rollover and unchanged canonical bytes | hp1_ + SHA-256 of four uint64-length-prefixed strings; 47 ASCII characters; distinct occurrences/sessions; expired known fences still select | B19/B20/B22/B27, D2 |
| T03 | 250 old groups,C1,60 groups,C2,0/1/19/20/100 recent groups; marker-like ordinary text | Latest stops at C2; explicit before=C2 crosses only C1–C2 segment; bytes/model state unchanged | B03/B13, D-A3 |
| T04 | Guarded settle/mismatch, projections/retraction, disposition, matching cancel, duplicate IDs, 101-row atomic group | Hand-computed success/result and cancellation/disposition values; whole group; no fake completion/private content | B18/B24, D-P1/P2 |
| T06/T29 | Known checkpoint + 0/1/63/64/65 unique combined archive/disposition/deletion facts; all publication stages, torn unpublished vs framed/canonical corruption | <=64 complete once; 65 publishes no candidate even after repeated Retry; no prefix scan, no baseline seed; durable save remains saved | B21/B22/B31/B40/B41/B42, D-H1/H2/N1 |
| T11 | Digits/range boundaries, duplicate/empty/signed/fraction/overflow/malformed percent key and value; selectors/ranges; invalid auth/hidden main/missing session plus bad limit; validation on/off | Exact D11 status/error and precedence before lookup/bind; no latest fallback | B19/B20, D-V1/V2/B4 |
| T12/T13 | Real REST/detail/hub same cut; tool-only/current projection; held frames; snapshot vs incremental vs unavailable live_only vs incomplete controls | Same selected positions/order/accounting; per-source frame origins; done precedes projection/complete; held seq>W only after complete; no history wipe on unavailable | B01/B02/B03/B04/B05/B06/B18/B24/B26/B31/B37/B38/B43/B46/B51, D4/D7/D7a |
| T19/T26/T36 | Actual first/middle/last expiry, post-unlink gap with next remove_count=0, native Windows held record, permission control, blocked content read | True retained continuation; token/rank-only tombstones; busy only skips held partition and continues/retries; append finishes before release; cut remains unchanged | B27/B32/B41/B50, D-X1..X6/L1 |
| T22/T25/T30 | Standalone command, real source/full backup, initial dry-run before backup, exact v1 manifest/receipt, unconverted/stale post-cutover evidence, repeat/interruption | Exact D13 exits 0/1/2 and summaries, no preflight publication/canonical write; independently frozen hashes/tokens/projection through real reader | B30/B35/B36/B44/B45, D-GO1/GO2/O1..O4 |
| T23 | Actual success/refusal/abort/index/storage outcomes carrying text/parameter/result/credential/token/address/query canaries | history_page event and required count/outcome fields exist; zero canaries in new diagnostics | B33, D-Z1 |
| T27 | 200–201 KiB/group vs 256–257 KiB/group; 40 normal <=8 KiB; >4 MiB atomic 101 rows; limit=min_rows=100 near8 MiB; positive advertised budgets | Default4194304; byte stop after20-floor only; height/atomic override; normal40 unchanged; row/source caps win coincident byte stop | B23/B24/B37/B38/B39, D-B1..B5 |
| T32 | Same cut with divergent REST/WS generated sizes; nil/error/negative/overflow/inconsistent weights | One prepared max-carrier meter, one projection/group, no publishing side effects, no estimated fallback | B18/B23/B37/B38/B46, D-W1/D4b |
| T31 | Missing/mismatched/>64 index then actual durable send/current control attach and Stop | Original bytes preserved, no partial baseline, metadata-only detail/live_only keep authority, no false history success/seen or user resend | B04/B20/B31/B40/B42/B43/B49, D-N1/N2 |

Numeric validation cases include min-1/min/min+1/max-1/max/max+1 and malformed/empty/null/negative classes. Negative/refusal matrices intentionally exceed 30% of cases. Exact equality is used where the spec defines exact values. No sleep-based ordering oracle.

## Pre-RED interim composition

Fetched `origin/fix/chat-replay-render-once` at `5fd851c5efaca429adb2267c779930ef37efae80`, newer than the spec's `da4b75df28c8a5bde751152659531e89b00062ad`. Read from that object: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/src/lib/attach-replay.ts`::AttachReplayBuffer, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/src/lib/ws.ts`::WsConnection changed send/queue/disconnect paths, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/src/components/chat/ChatScreen.tsx`::VirtualizerAutoFollow changed path, and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/src/lib/ws.attach-replay.test.tsx`. Completion remains catch_up_complete, not replay done. The latest version additionally supports visible bounded progressive fallback for oversized raw buffering. This Go pack checks server complete/order semantics; it does not add a second browser buffer or assert that replay done completes attach. Source read is not execution or landing evidence.

## Missing seams and deliberate gaps

D1/D1a describe the store reader/options/checkpoint/fault/read-observer architecture without concrete exported Go signatures. D4b proposes a file/home but not a callable factory signature. The separate command and schema do not exist on base d54ac55a33312f7bdfb8a500fe08853bcb05179b. Ask squad lead for agreed APIs. Where unavailable, an explicit `t.Fatal("BLOCKED: ... not implemented — required by ...")` names the missing behavior, not a compile accident, skip, fake implementation or false green. Missing-seam tests are not claimed as executed behavioral coverage.

Native Windows T19/T26/T36 require correctly constrained `_windows_test.go` sources and named Windows CI execution; Linux Fly is not native certification. Production/CI/schema wiring is backend-lead-owned and will not be edited by RED. Frontend/component/real-browser/UAT, real-process Stop reachability, heap/performance envelope and content-read/crash probes cannot be claimed from static tests or mere test collection.

GitNexus MCP discovery returned no matching tools and this worktree has no index. Manual known-present `ReadTranscriptView` control found REST detail/messages and attach callers. New-file-only additions do not edit those symbols. Impact is Inferred, not graph output. Before each commit inspect staged paths/diff and run the available change-scope fallback; do not claim detect_changes ran.

## Mutations for independent CHECK — not run by this RED instance

| Mutation | Required kill |
|---|---|
| Full lifetime read or full disposition scan before slicing | T06 record/byte/seek counts |
| Fullness implies continuation / inclusive before | T01 exact-full/exclusive traversal |
| Byte stop before min_rows or disabled budget | T27 exact20-floor and normal40 |
| Raw marks instead of length-prefixed SHA-256 | T02/T25 frozen vectors |
| Root publication on65 or replay64 repeatedly | T29 unchanged-root retry |
| Hold shard/archive mutex during content read | T26 deterministic blocked read |
| Fake expiry on Windows busy / keep rich tombstone fields | T19/T36 real handle + exact state |
| done treated as catch-up complete, lost projection result or held-live order | T12/T13 |
| Fabricate empty history or partial baseline after send | T12/T31 |
| Ignore tampered backup / accept pre-cutover equality / rewrite canonical bytes | T22/T25/T30 |
| Raw-query/content logging | T23 required event + canary assertions |
| Guess0/fallback weights or project/send twice | T32 |

## Evidence discipline

One narrow local test, tagged/CGO disabled, under the supplied slotrun wrapper; remaining Go execution on Fly from a pushed tests-only SHA. Save logs and raw exit codes; parse Fly RESULT/GATE FAILURE markers, named failures and actual HEAD. Never run a local full suite. GREEN, all mutation probes, restored-green and Proof-of-failability are **deferred to CHECK**, not met. A fresh instance must audit this pack in AUDIT mode after implementation.
