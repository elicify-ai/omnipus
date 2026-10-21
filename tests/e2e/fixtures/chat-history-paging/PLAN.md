# Chat history paging — RED browser plan

## Behaviour and authority

A reader opens recent history, browses whole older groups without losing their current place or focus, and keeps live controls when saved history cannot be read.

Independent oracle: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-e2e/docs/internal/specs/chat-history-paging-spec.md` — D1–D15, B01–B51, T16/T18/T21/T24/T28/T35 and the test datasets. Expected IDs are fixture-authored; limits/floors derive from D5; settled drift is at most **1 CSS pixel**, never a tolerance fitted to a run.

Pre-RED source read: `origin/fix/chat-replay-render-once` at **5fd851c5efaca429adb2267c779930ef37efae80**. Read `AttachReplayBuffer::{begin,accept,retain,clear}`, `WsConnection::{send,_onFrameParsed,disconnect,_handleClose}` and `ChatScreen::VirtualizerAutoFollow`, plus transport/virtualization regressions. Normal attach buffering ends at **catch_up_complete**, not replay `done`; oversized replay can enter bounded progressive delivery. Paging must not add a second replay buffer, fabricate completion, or fight upward intent. Source reading is not execution evidence.

## Boundary and instruments

| Real | Process-edge controls only |
|---|---|
| Built gateway and its embedded production styles; session store/index/projection; browser WebSocket; React/store; actual virtualizer; real DOM focus and row geometry | External provider script, deferred external image response, and held/failed HTTP delivery of a real gateway response |
| Session fixtures are appended by a separately built **test-tree** Go helper using the real `pkg/session` store. No hand-written index, fake history store, gateway frame injection, CSS height overrides or production-code modifications | An isolated `GatewayProcess` owns every mutable test home/port. Missing-baseline fixtures remove only explicitly declared derived index artifacts, never canonical archive bytes |

Every frame/body uses committed generated types/validators. A captured bad response fails loudly; it never becomes an empty array. Capture real attach/snapshot/complete/page requests and content-free diagnostics. Inspect final screenshots, not only runner status. `catch_up_complete` and settled rendering are separate barriers. Animation frames/response promises/DOM observations are barriers; no sleeps.

## Case table

| Test pack | Cases / independent expected result | BDD / dataset |
|---|---|---|
| T16 open-scroll-anchor | Recent suffix only; first open settles at bottom after complete; actual 2H±1 trigger with coalesced boundary; measured row estimate rather than fixed80; moving-reader prepend and live growth do not change current anchor by >1px; loader/failure/Retry/removal and late image growth use signed correction; explicit clear crossing opens exactly one barrier; terminal has no fetch loop; safe 500-group reader traversal | B01,B07,B08,B09,B12,B13,B17; D-A,D-G,D-M1 |
| T18 prefix-and-real-session-evidence | Equal200-row suffix after1,000/100,000 prefixes; exact selected IDs/positions, bounded source reads and ≤10% selected-work variation. Full-read negative control is assigned to CHECK. **Founder/squad-lead clarification2026-10-21:** B25 genuine-session evidence is team-lead's later hands-on UAT, not an automated fixture; it does not block automated RED, and synthetic success is never called genuine acceptance | Automated B21,B22; D-A1,D-R4. B25/D-U1 separately handed to UAT |
| T21 keyboard-reader-states | Enter/Space earlier/Retry/Return each issue one operation; focused pending/error controls stay mounted; terminal removal focuses labelled scroller without moving it; one status/alert, no historical New response announcement; reduced-motion, reflow and ResizeObserver-absent plain fallback; Return failure retains draft/no resend | B28,B29,B31,B34; D-K,D-M2,D-O4 |
| T24 reconnect-retention-retry | Boot-mismatch snapshot while old reader stays anchored; direct saved-range revalidation, not tail walk; repeated refresh failures retain exact cached/out-of-date state, stop prefetch, retry identical range; partial/full expiry use exact copy/focus; stale A→B→A response cannot update current view; interrupted attach does not complete; failed older boundary retries without send | B04,B05,B06,B10,B11,B20,B26,B27,B34,B47,B48; D-R,D-X,D-F,D-E |
| T28 soft-budget-screen-coverage | 4,194,304 default; independently sized200–201KiB one-row groups:20 fit/21 do not;256–257KiB floor fixture keeps20 although >5MiB; normal40×≤8KiB unchanged; actual≈4H open and≥2H new prefetch via single contiguous chain, not two threshold/top-up owners; whole groups/anchor survive | B37,B38,B39; D-B1,D-B1b,D-B2,D-B3 |
| T35 unavailable-history-live-controls | Real metadata-only detail/session route; fixed500 messages and typed unavailable state→live_only; real external-provider-held engine turn stays running through Retry/reconnect; send saved once without a partial baseline; Stop reaches engine and ends actual provider work while history error remains. Access refusal precedes bad limit; no wipe/history-success/seen/resend | B20,B31,B42,B43,B49,B51; D-N1,D-N2,D-C1 |

Error/negative cases include failed older/latest/range reads, repeated failures, missing baseline, stale deliveries, interrupted control delivery and missing genuine-session evidence. Numeric parser min±1/max±1 and malformed-envelope matrices belong to the separately assigned backend/frontend RED packs; this pack covers real browser geometry, 499/500/501 traversal edges and byte/floor boundaries without duplicating those units.

## Planned CHECK mutations (deferred, not performed by RED)

| Mutation | Required observer |
|---|---|
| Full lifetime read / disposition scan before slicing | T18 discrete selected-record/byte counters, not stopwatch |
| Fixed80 unmeasured estimate, disabled signed correction, or total-scrollHeight compensation | T16 threshold/late-height/moving-reader geometry |
| Separate opening/scroll owners, bytes before floor, or no byte budget | T28 exact boundary request chain/accounting/actual coverage |
| Snapshot wipes historical reader, stale generation accepted, or partial refresh deletes cached rows | T24 reader/focus/Retry/expiry assertions |
| live_only claims history success, Retry resends, or Stop is display-only | T35 real traffic/canonical save/provider cancellation |

## Known external prerequisites / honest limits

| Item | Status |
|---|---|
| Genuine anonymized long-session source, permission/provenance and exact-candidate independent validator receipt | Per founder/squad-lead clarification2026-10-21, team-lead's later hands-on UAT item. The initially drafted automated evidence guard is removed on that explicit instruction; this is neither a skip nor automated genuine-session acceptance |
| Derived index artifact names for a missing-baseline fixture | Requested from backend via dispatcher; no guessed index format |
| Ninety-day physical archive seeding | Current exported writer has a private clock. No production testability change is allowed; record this gap rather than claiming timestamp fields prove physical rollover |
| Native Windows retention/read-handle certification, all 51 BDD scenarios and full performance/UAT campaign | Separate assigned packs/campaigns; no browser pack claims those complete |
| GREEN, at least3 mutations and proof-of-failability gate | **Deferred to independent CHECK**; this instance must not audit its own suite |
| Local browser run | Not planned: real embedded build is heavy. Use a tests-only pushed commit on Fly, targeted specs only |

## Explicit seams and remaining coverage limits

| Seam / coverage | Handoff |
|---|---|
| Public production symbols used | Existing `UnifiedStore::{NewUnifiedStore,GetMeta,AppendTranscriptAddressed,Close}` through the test executable; generated `SessionHistoryResponse`, `SessionDetail`, `WsFrame`; existing `GatewayProcess`, `createTerminalSession`, `openSessionByDeepLink`, `startTerminalOutcomeProvider`. No missing product function is imported to manufacture a compile failure |
| New diagnostic field assumptions | `history_page` in the JSON log's `message`, `outcome=success`, numeric `selected_archive_records`, `selected_archive_bytes`, `allocated_bytes`. D1/performance requires these measurements; the exact spellings are an explicit GREEN/architect handoff, not claimed existing symbols |
| Missing-baseline/rolled-partition seams | Remain loudly BLOCKED until the architect decision at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md`. Current private fixture names `OMNIPUS_E2E_HISTORY_INDEX_ARTIFACTS` and `OMNIPUS_E2E_HISTORY_SCENARIO_BUILDER` are placeholders, **not product configuration/API**. No path is guessed |
| T35 current controls | Real canonical send, live-only readiness, Retry/reconnect and engine-observed Stop are authored. Genuine pending approval/question/secret interaction variants are not yet authored; separate component/server tests or GREEN cannot be misreported as these E2E cases |
| T16 window bounds / measurements | Browser traversal catches immediate oldest-drop and tests forward reload after crossing500. Exact500-reader/100-tail/inactive-heap accounting remains T09/CHECK/UAT work, not a DOM-count heap claim. Natural non80 real rows and virtual-canvas estimate are covered; the literal40px/H800 matrix is not certified by injected heights |
| T21 presentation | Reduced motion,640px reflow and actual no-ResizeObserver fallback are authored. Reflow is not a claim that browser chrome zoom was set to200%; exact browser-zoom acceptance remains UAT |
| T24 remaining variants | Authored incomplete-control delivery, moving old reader after boot-mismatch, same-boot reconnect, repeated refresh failure, stale failure, real expiry fixture seams and >5-page range refresh. Separate pending-control/success-stale/retention-exceeded variants remain a named gap |

## Execution policy and evidence

Local commands only inspect files/git or collect a single spec under the slot wrapper. Heavy build/typecheck/E2E belong on Fly. All claimed commands have saved logs and direct exit codes. Tests-only RED receipts must identify commit and named assertions; compilation/setup/global-setup failure does not prove paging RED. Shard registration is checked with a positive control and real discovery. No skip, expected-failure, .only, swallowed error or green-by-wrapper claim.

skills: omnipus-shared-rules, elicify-test-writing, test-integrity-audit (reference only), gitnexus-impact-analysis
