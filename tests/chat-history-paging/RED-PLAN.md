# Frontend RED plan — #1266

Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-fe/docs/internal/specs/chat-history-paging-spec.md` (full 1,391-line final round-2 draft read before assertions). Base: d54ac55a33312f7bdfb8a500fe08853bcb05179b. Scope: T07, T08, T09, T10, T14, T15, T20, T33, T34 and the semantic-fixture requirement.

## Observable outcome and boundary

Recent bounded history is validated before use; earlier/newer/refresh actions preserve the current reader, live controls and pending input. Only HTTP, WebSocket, clock/frame scheduling and DOM measurement edges may be doubled. Actual adapters, reducers, catalogued controls, router and query client remain real. No source-text assertions, production mocks, new production API invented by this pack, sleeps, skip/expected-failure markers or config changes.

PRE-RED source: fetched `origin/fix/chat-replay-render-once` at 5fd851c5efaca429adb2267c779930ef37efae80. Read AttachReplayBuffer, WsConnection send/dispatch/disconnect, ChatScreen VirtualizerAutoFollow/VirtualizedMessageListInner/history application. Completion is catch_up_complete, **not done**. Preserve serialized attach generations, error/refusal/disconnect discard, one historical batch, projection after done and upward user intent. No cherry-pick/landing is implied.

## Cases and independent oracles

| Pack | Input / expected outcome | Spec source | Planned CHECK faults |
|---|---|---|---|
| Semantic fixtures | Available empty detail: messages [], flags false, complete zero-count latest page; unavailable detail omits all history fields. Snapshot narration groups 0/1/2 have exactly 0/1/2 rows/groups, first/last iff nonempty. Two sources are not one; tool start/result count as one logical tool row, not two frames. | D3/D4/D10; earlier audit semantic survivor | source_group_count=0; row_count=0; missing available metadata |
| T33 / adapters | Valid envelope, empty/exact-full pages, alternate positive budgets 2097152/4194304/8388608 accepted. Wrong shape/session/flags/cursor/counts/floor/bytes rejected atomically. One bad item retains a position-stable visible placeholder. Metadata-only identity remains routeable. | B02/B23/B31/B51; D-C1/D-E3/D-B5 | raw-array fallback; strip history_state; accept false counts |
| T08 / state | Live values beat stale page overlap; snapshot while historical keeps reader and stages distant tail; live cursor commits only complete; live_only is not history-loaded; obsolete A→B→A success/error cannot write. | B06/B08/B11/B23/B26; D6/D7/D7a | unconditional snapshot wipe; session-only generation fence; live_only marks history complete |
| T09 / window | 499/500/501 reader and 100/101 tail boundaries; whole safe far-edge trim or defer protected commit; forward reload and no hidden gap; leave releases feature caches, retains pending/current controls. | B12/B34; D-M1/D-M2/D9 | oldest-drop prepend; append distant tail; retain inactive bodies |
| T14 / clear | Own operation fresh latest_clear confirms even outside selected page; old marker cannot settle new operation; foreign clear does not teleport historical reader. | B03/B11/B13; D6/D15 | marker-in-array-only confirmation; unrelated clear resets |
| T15 / recovery | Plain/manual earlier path, failed same-boundary Retry, latest loading/error/success with no resend; unavailable history shares UI with live controls. | B02/B10/B11/B23/B31/B34; D-E2 | Retry sends input; error replaces thread shell |
| T20 / accessibility | Enter/Space once, stable pending/failure Retry node, terminal labelled-scroller preventScroll fallback; one status/alert, busy clears, history not announced as new output. | B28/B29; D-K1/D-K2/D8 | disabled focused button; remount Retry; duplicate announcement |
| T34 / refresh | Saved range directly revalidated; 500 groups via 25 contiguous 20-group chunks (>5); partial stage failure retains all cached rows; exact failed/out-of-date/partial/full expiry copy, paused prefetch and range-only Retry. | B26/B27/B47/B48; D-F1/D-F2 | five-request cutoff; stage partial deletion; reset latest |
| T07 / sizing | H800/A80 threshold 1601/1600/1599 → 0/1/1; A40/400 and H1600/A10 → limits40/20/100, floors40/4/100; sample replacement/latest100/reset/invalid samples; actual virtualizer estimate/prefix test, not fake hook. | B07; D-G1..G1d/D5 | fixed80 estimates; duplicate request owner; wrong height source |
| T10 / anchor | Moving reader/live tail, hint32/loader19.5/Retry72/gap16, shrinkage and late image80→640 preserve signed current-anchor offset, not total scroll height. | B08/B09/B17; D-G2/D-G3/D8 | drop negative delta; count below-anchor growth; native double compensation |

## Known gaps / execution

All GREEN, three-mutation proof, final full-suite and independent CHECK remain **deferred to CHECK**. This author does not audit their own pack. Unit/browser geometry claims require the real measurement instrument: if jsdom cannot establish genuine virtualizer/layout behavior, the case is explicitly BLOCKED and handed to T16/T21/T24/T28 browser E2E; injected formulas never count as that evidence. Engine-observed Stop, Go recovery/security/repair, genuine long-session heap/performance and exact-commit UAT are other lanes, not claimed here.

Every quick local execution is one selected test in one Vitest file, under the founder's slotrun wrapper, with saved output and raw exit. Wider verification runs only on pushed refs in Fly. GitNexus tools were unavailable at discovery; manual caller inventory is **Inferred** impact, not graph output. Scope before every commit is checked with the actual Git diff.

## Confirmed seam additions

The architect's `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md` parts J/K confirm the existing adapters' envelope return shape, generated indexed-access aliases and top-level cursor issue paths. The real state owner is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-fe/src/store/chat/history.ts`::createHistorySlice, composed into useChatStore. Direct tests invoke the declared loadLatestHistory/loadOlderHistory/loadNewerHistory/returnToLatestHistory/refreshLoadedHistory/retryHistory/enterHistoryReading/releaseSessionHistory methods. Missing methods fail loudly inside the named case, never collection or a skip. No test-only reducer is substituted. Physical UI measurement remains separate.

RED-E2E owns the whole-retained reader in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-fe/tests/e2e/fixtures/session-history.ts`; T33 imports that exact module after its handed-off commits, never a duplicate. The user's narrowed fixture scope includes every new/modified fixture and four legacy available/paging-semantic suites; other legacy consumer/mock matches are inventory-only pending GREEN migration.
