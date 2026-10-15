# WU-10 RED gap closure — independent CHECK F1–F3

Branch: work/omnipus-l1-ui-red-wu10, starting at ba81f97a68e6021cce1883420f1c97ce1d926b2f. Frozen implementation: 5abe738ccead35985bc85c59dd84a5188558b920.

This is RED-author strengthening, **not an independent CHECK of my own suite**. No production code or mutation is authored here. Team-lead Q1=B explicitly allows unavailable fix/mutant control runs to be UNVERIFIED; the implementer's #1066 fix provides its good tree, and fresh CHECK must re-run M01/M28. Q2=A explicitly authorizes replacing opposed legacy auto-advance expectations with #1066 explicit-confirm expectations while retaining all unrelated behavior.

## Sources read before expectations

| Authority | Requirement / evidence |
|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/CHECK-REPORT.md (full report) | F1 unobserved forbidden automatic advance; F2 collapsed Default masks Omnipus sidebar re-admission; F3 vacuous no-version negative masks missing warning. F4 docs remains implementing lead's job, not a test-code change |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/gap-closure/issue-1066.txt | gh issue view 1066 exit 0; Required behavior 4: single- and multi-select both advance on confirm, not selection alone; pointer and keyboard paths |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/docs/internal/specs/omnipus-system-agent-spec.md::FR-004, FR-100 | Omnipus excluded from workspace sidebar rows; fixed entry remains reachable and unique |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/gap-closure/issue-1076.txt | gh issue view 1076 exit 0; healthy rows remain usable and user message identifies invalid agent id and field, not a version mismatch |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/check-evidence/C03-v2-results.json and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/check-evidence/C03-v2-v2-test-delta.diff | Calibrated timer-drain predicate fails exact head and passes disabled-auto control; old test passes bad code. Initial timeout instrument is not used |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/check-evidence/counterprobe-results.json | C01 and C02 expose surviving M01/M28; reported source/test restores mean those bad/good variant trees are not retained as runnable snapshots |

## Cases and old → new

| Gap | Old observation | Stronger observation / independent oracle |
|---|---|---|
| #1066 single choice | Choose then immediately confirm; automatic advance is never observed | Choose by pointer or keyboard Space. Drain already-scheduled timers using the calibrated fake-clock pattern. Original question and pressed answer stay visible; next question absent; no answer frame sent. Only explicit Next/Enter then advances. No stopwatch, widened timeout or mocked component |
| Opposed legacy #1066 | Selection automatically moves to next question after an elapsed fixture delay | Selection alone remains on current question after pending callbacks; explicit confirm moves it. Original answered counts, exact submitted answer values/frame, free-text semantics, grace-window behavior, cancel/terminal/hostile-markdown and negative checks remain |
| FR-004 sidebar exclusion | Only fixed entry counted while Default is collapsed | Open Default through the real disclosure. Positive ordinary Mia row proves the rows render; Default group contains no Omnipus button/group, and global fixed Omnipus entry remains exactly one and above Workspaces. Sales/pointer/navigation assertions remain |
| #1076 warning delivery | Console diagnosis plus a negative check that empty messages pass | The API suite positively requires the real store's delivered warning; a separate integration suite mounts REAL AppShell, the existing legitimate host of REAL ToastContainer, and requires the visible status naming exact offending id and name field. Use ids unique to each warning scenario to avoid page-life dedup from earlier boundary cases. Retain real console diagnosis, healthy-neighbor and no-version guards; add an entirely-valid-response control. No mocked addToast, renderer or warning formatter, and no private UI import |

## Boundary and controls

Real components, actual stores, actual QueryClient, generated wire types and real ToastContainer. Network/socket/router/clock remain process-edge doubles only. No unit/helper/formatter/delivery mock is added. The fake-clock predicates use the calibrated synchronous event pattern; keyboard activation sends keydown/keyup and the native detail=0 click only when not prevented, because jsdom does not perform that browser default. Existing full keyboard journeys still use user-event. Timer-drain assertions observe state, not elapsed speed. An initial async user-event/fake-clock attempt timed out and is retained as invalid instrument evidence, not a product defect or a reason to widen deadlines.

The direct ToastContainer-import draft passed its runtime check but failed the design-system scanner because the catalog marks it application-private. That draft is not the final evidence. Moving the import next to the component would not satisfy the scanner either; no exemption, public export, indirect re-export or configuration change was made. Final warning coverage instead uses the store delivery in the API suite plus AppShell's existing legitimate ToastContainer host. The real router and application singleton QueryClient run; only companion network APIs and non-opening raw HTTP/WebSocket edges are doubled. The AppShell receipt leaves jsdom's unavailable Worker/scrollTo and existing route-discovery warnings visible. No warning suppression or claim of browser acceptance follows from that control.

The implementation source is read only via a separate frozen archive under /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/gap-closure. All commands start from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red. Final tests are copied into the archive; production hashes must match its original git archive. Only one test file/process runs at a time; every run has a saved raw log and exit.

| Proof | Owner / claim boundary |
|---|---|
| Strengthened #1066 fails exact 5abe738cc | This author runs one file; must name the forbidden automatic transition, not a timer timeout/import fault |
| Sidebar and delivered warning pass healthy 5abe738cc | This author runs the real expanded/sidebar and real toast paths |
| #1066 passes fixed implementation | Run if implementer supplies an immutable fixed tree; otherwise explicitly UNVERIFIED, never inferred from CHECK's different predicate |
| M01 and M28 killed by final tests | Fresh CHECK re-runs the two recorded production mutants. Author does not mutate production in scratch; historical calibrated kills are read evidence only, not a new run claim |
| No weakening | Compare retained original assertion expressions, whitelist only spec-authorized opposite auto-advance oracle changes; no deleted tests/skip markers/suppressions/configuration changes |

## browser/UAT rows for WU-10

All existing U1–U8 remain mandatory. Strengthen U6 with expanded Default (Mia visible, fixed Omnipus unique, no workspace Omnipus row) and single-choice pointer/Space selection that does not advance until explicit Next/Enter. U7 must show the real invalid-row user warning with exact id/field while healthy neighbors remain selectable. These automatable predicates stay in unit tests as well; UAT is not a substitute. Tester and independent validator use separate accounts/sessions, workspace/account/role badges visible, and validator independently re-drives the critical paths.

## Author-side execution ledger

All final test executions below use frozen implementation 5abe738ccead35985bc85c59dd84a5188558b920 plus test-only overlays. They are five serial single-file runs, not a full suite. Raw logs live in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/gap-closure and are individually bound in the final verification receipt.

| File / criterion | Exact-head result | Proof boundary |
|---|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/src/components/chat/AskUserQuestionCard.keyboard1066.test.tsx — #1066 Required behavior 4 | Exit 1; 2 RED / 7 controls pass | Pointer and keyboard original-question postconditions fail after selection alone; no timeouts. Before strengthening, all 8 original cases passed this same bad implementation. New-suite fixed-tree control remains UNVERIFIED |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/src/components/chat/AskUserQuestionCard.test.tsx — authorized legacy confirm setup | Exit 1; 3 RED / 11 controls pass | Original Scope disappears before confirmation in the three amended scenarios. Answer values, exact frames, free text, grace expiry and negatives retained; fixed-tree continuation remains UNVERIFIED |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/src/components/layout/Sidebar.omnipus.test.tsx — FR-004/FR-100 | Exit 0; 9 controls pass | Expanded Default has real Mia and no Omnipus row; M01 kill by this final suite remains UNVERIFIED pending fresh CHECK |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/src/lib/api/agents.resilience1076.test.ts — #1076 delivered warning | Exit 0; 14 controls pass | Positive real-store warning identifies unique bad id and name; old logging and no-version guards retained. M28 kill by this final suite remains UNVERIFIED pending fresh CHECK |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/src/components/layout/AppShell.warning1076.test.tsx — #1076 visible warning | Exit 0; 2 controls pass | Actual application-host status visibly names bad id/name; valid-response control emits no invented warning. Exact skip-strategy cache shape is explicitly labelled characterization because the issue permits skip or flag; it is not a new skip-only product requirement |

Final source typecheck and design-system scanner receipts are separate from these runtime claims. The matcher-preservation instrument retains all 109 original expressions, including all 4 negative chains, across the four modified existing files; only the two explicitly authorized #1066 title changes occur. Its first deletion calibration removed a surplus duplicate in one file, correctly leaving the baseline multiset satisfied; the final calibration removes the required expression entirely and confirms detection in every file. Negative-chain changes are also detected. This is author-side static preservation evidence, not an independent integrity verdict.

Implementation mutations, the overall test-integrity verdict and gate acceptance stay with fresh independent CHECK. Skill step 4 items 2–3 and the Proof-of-failability gate are **deferred to CHECK**. Full suite/CI, live instance and browser UAT are not run or claimed by this lane. Existing CHECK BLOCK remains unresolved until the implementation defect, the fresh mutation/control proof, same-change documentation and exact-candidate gates/UAT are addressed by their owners.
