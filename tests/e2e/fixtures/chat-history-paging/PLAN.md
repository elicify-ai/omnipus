# Chat history paging — RED browser plan

## Behaviour and authority

A reader opens recent history, browses whole older groups without losing their current place or focus, and keeps live controls when saved history cannot be read.

Independent oracle: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-e2e/docs/internal/specs/chat-history-paging-spec.md` — D1–D15, B01–B51, T16/T18/T21/T24/T28/T35 and the test datasets. The spec clarification at `34612f1720ea22a5a47599c658f16dd14ff3ae0e` was fetched/read as a Git object (no production/docs merge into this test branch). Final fixture/diagnostic names come from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md`::F/G/H. Expected IDs are fixture-authored; limits/floors derive from D5; settled drift is at most **1 CSS pixel**, never a tolerance fitted to a run.

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
| Derived index removal/corruption | Resolved by F: real `.chat-history/FORMAT` (`omnipus-chat-history-index/1\n`) and v1 `checkpoint.json`; actual stopped-child assertion, containment/no-symlink and healthy baseline positive controls. No placeholder artifact-name input remains |
| Real90-day / retention seeding | Resolved by G: unchanged RED-BE driver commits `0f6fbfbded522d8ead2863add33b37e335b7f841` and safety follow-up `c499b41e8a84baa839ccb7863b05088a16989266`, prebuilt once at candidate with native Go test-compile mode. TS sends generated Message fixtures; the same-package driver uses the private clock and actual writer/rollover/sweep, with strict plan and named PASS/receipt checks. Never an exported production clock setter or copied index implementation |
| Native Windows retention/read-handle certification, all 51 BDD scenarios and full performance/UAT campaign | Separate assigned packs/campaigns; no browser pack claims those complete |
| GREEN, at least3 mutations and proof-of-failability gate | **Deferred to independent CHECK**; this instance must not audit its own suite |
| Local browser run | Not planned: real embedded build is heavy. Use a tests-only pushed commit on Fly, targeted specs only |

## Explicit seams and remaining coverage limits

| Seam / coverage | Handoff |
|---|---|
| Public production symbols used | Existing `session::NewUnifiedStore` and `UnifiedStore::{GetMeta,AppendTranscriptAddressed,Close}` through the test executable; generated `SessionHistoryResponse`, `SessionDetail`, `WsFrame`; existing `GatewayProcess`, `createTerminalSession`, `openSessionByDeepLink`, `startTerminalOutcomeProvider`. No missing product function is imported to manufacture a compile failure |
| Exact diagnostic / allocation seam | Spec34612f172/H fixes `message=event=history_page`, carrier/outcome, all five physical-read categories/totals, selected40 and lookahead0..1 on the simple fixture. Allocation attribution is NOT a production-event field. Separate isolated `history_page_measurement` receipt uses short_prefix/long_prefix/sample_count/limit/min_rows/allocated_bytes; absent receipt is UNVERIFIED, never0 or PASS. Private CI input `OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT` points to an absolute log with a candidate `HEAD: <full SHA>` and the two H JSON records; backend/CI owns its real measurement producer |
| Missing-baseline/rolled-partition seams | Converted to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-e2e/tests/e2e/fixtures/chat-history.ts`::sessionHistoryIndexPaths/removeSessionHistoryIndex/corruptSessionHistoryIndex/seedChatHistoryDayPartitions/sweepChatHistoryFixture and GatewayProcess::assertStoppedForFixture. Official prebuilt bridge input is `OMNIPUS_CHAT_HISTORY_FIXTURE_BINARY`; plan is `OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN`, selected driver is TestChatHistoryFixtureDriver and success has exactly one CHAT_HISTORY_FIXTURE_RESULT. No placeholder scenario executable or guessed index filename remains. RED-BE alone authors/changes the Go driver |
| T35 current controls — extended dispatch | Separate real bash approval and AskUserQuestion variants use a scripted external MODEL only; native discovery, tool execution, pending registries, browser decisions and Stop remain real. Each has action→real held-model turn→engine-observed Stop plus direct pending-control Stop. Existing approval-modal Cancel is its documented cancel_turn/owner-Stop path, not a forced background click. Secret is explicitly **BLOCKED: request_secret not implemented (WU-4/5)** per squad/chief ruling; no guessed arguments or fake secret frame/card. Its owning work unit must land before genuine secret acceptance |
| T16 window bounds / measurements | Browser traversal catches immediate oldest-drop and tests forward reload after crossing500. Exact500-reader/100-tail/inactive-heap accounting remains T09/CHECK/UAT work, not a DOM-count heap claim. Natural non80 real rows and virtual-canvas estimate are covered; the literal40px/H800 matrix is not certified by injected heights |
| T21 presentation | Reduced motion,640px reflow and actual no-ResizeObserver fallback are authored. Reflow is not a claim that browser chrome zoom was set to200%; exact browser-zoom acceptance remains UAT |
| T24 extended branches | Separate genuine approval/question cases keep the old reader across ordinary reconnect while a new viewer obtains a real bounded snapshot with the same current decision. A distinct same-boot retention_exceeded case disconnects only the old viewer and sends five valid256-KiB client messages through an authenticated real socket, waits for real sequenced saved echoes exceeding the1-MiB journal, then checks actual snapshot reason/window refresh/anchor/current engine Stop. No journal-size override, fake server frame or gateway restart forces that reason. Obsolete failure is authored; the separate obsolete-success variant remains a named gap |
| T33 additional dispatch | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-e2e/tests/e2e/fixtures/session-history.ts` exports the actual all-retained-page reader and parent-wake absence oracle without Playwright registration. Existing chat and parent-wake consumers import it; original value/absence assertions remain, HTTP failure now throws in the common guard. RED-FE owns the Vitest wrong-shape/multi-page/clear tests against this real import; no test code is copied into the adapter |

## T33 helper correction and reproduction

Independent RED-FE proved the extracted helper accepted row_count0 for one visible narration source. Helper-only fix `30babb310a45e695873feb525e39f27714f450db` adds the narration lower bound and exact narration-only counts; it preserves hidden special metadata as a separate class. The selected RED and GREEN raw logs, source hashes and exact reproduction commands are retained in the external RED-E2E evidence receipt, not converted into a product-GREEN or CHECK claim. A one-file/one-case HTTP abort instrument checks that the external provider really observes cancellation (not a fake Stop boolean); this is a positive instrument, not feature acceptance.

## Section P — dedicated US1.6 ordering strengthening

Authority: final seam decision section P3/P4 and merged spec commit `a1cd8e484dce4436c41268470b32de707cf3f910`. The four approval/question action-and-Stop or direct pending-Stop cases must establish the visible fixed history error, then usable composer, before the initial real send. For unavailable-history reconnects the helper reasserts the error before initial decision focus and again after completed catch-up before restored-decision visibility/identity/enabled/focus checks; the existing final error assertion still precedes the real action/Stop. Existing tool completion, model abort, lifecycle and no-resend oracles stay unchanged. Healthy-history T24 callers keep their original path.

Narrow proof: only the T35 single spec on assigned Fly spare48ee71eb1e9768, with both prebuilt fixture binaries. Record each named runtime failing assertion and separately grep `/cache/tmp: Permission denied (13)` so environment failures are never called behavioural RED. Secret stays explicitly BLOCKED on external WU-4/5. T18 consumer remains byte-for-byte unchanged, including the space in `HEAD: ${commit}`; producer remains RED-BE-owned. Do not resolve or modify the three deferred e59e3d542 release-conflict QA files. GREEN/mutation/proof-of-failability remain deferred to independent CHECK.

## Execution policy and evidence

Local commands only inspect files/git or collect a single spec under the slot wrapper. Heavy build/typecheck/E2E belong on Fly. All claimed commands have saved logs and direct exit codes. Tests-only RED receipts must identify commit and named assertions; compilation/setup/global-setup failure does not prove paging RED. Shard registration is checked with a positive control and real discovery. No skip, expected-failure, .only, swallowed error or green-by-wrapper claim.

skills: omnipus-shared-rules, elicify-test-writing, test-integrity-audit (reference only), gitnexus-impact-analysis
