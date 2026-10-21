# L1 UI stage 1 — final RED pack and before/after ledger

**Bottom line:** all accepted follow-ups are included. The final pack has **14 retargeted existing files and 21 new cases** (18 real-shell behaviours plus 3 shipped-value guards). All **290 cases in 16 files** have successful, serial, one-file execution receipts on the requested UI head. **629 original assertion expressions and all original case declarations remain**, with five stronger visibility assertions added. The original nine real-shell acceptance oracles also remain unchanged. Independent GREEN verification, production mutations and the skill's Proof-of-failability checklist are **deferred to a fresh CHECK instance**, not claimed as completed.

## SUPERSESSION — founder decision 2026-10-18, Train 3 re-cut: "rooms switched on"

Recorded in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-omnipus-L1-v2-20261013.md`
line 143: "WU-15 rooms + room screens, rooms switched on". This supersedes the 2026-10-14
stage-1 decision FOR ROOMS ONLY. Every test pin that asserted rooms hidden/unreachable
under `ROOMS_ENABLED = false` was re-aimed to the matching switched-on pin at the SAME
assertion strength — no assertion was weakened, and each superseded "must be false /
absent / null" expression became its "must be true / visible" counterpart. Re-aimed in
this retarget (stream C, branch `work/wu15-stage1-retarget`):

| Case | 2026-10-14 pin (superseded) | 2026-10-18 pin (current) | File |
|---|---|---|---|
| S1-g rooms | ships `ROOMS_ENABLED` exactly false | ships `ROOMS_ENABLED` exactly true | `src/lib/featureSwitches.stage1.test.ts` |
| S1-a1 | expanded workspace: no Group chats section, no room read | section + room row visible, room-list read exactly once | `src/components/layout/AppShell.stage1.test.tsx` |
| S1-a2 | member main: no Add participant entry, no room request | Add participant entry visible, no attach_room/room-detail read (sidebar's one list read aside) | same |
| S1-a3 | direct room URL redirects to chat, no room REST, no attach_room | same URL mounts the room, reads it, attaches it, renders from a real snapshot | same |
| S1-a traffic ×4 | room_ref carrier renders no entry/toggle/link | exactly one entry + visible toggle, expanding to the "Open group" link, no room REST read | same |

The S1-b (secrets) and S1-c…S1-exp (approval modal) pins are NOT affected — they keep
the 2026-10-14 stage-1 oracles. The switch-flip proof for the re-aimed pins: with the
shipped `ROOMS_ENABLED = false` each re-aimed pin FAILS; with a temporary local flip to
`true` (never committed) each PASSES. CHECK mutants for the re-aimed pins invert
accordingly (`ROOMS_ENABLED true → false`).

### Switch-flip proof receipts (stream C)

Two-sided proof, one file at a time by explicit path, serial:

| Direction | Tree | Result | Log (in `tests/l1-room-ui/retarget-logs/`) |
|---|---|---|---|
| Must FAIL with the switch off | `94977f1c0` (shipped `ROOMS_ENABLED = false`), scratch clone, only the two re-aimed test files applied | `featureSwitches.stage1.test.ts`: **1 failed / 2 passed** (S1-g rooms), exit 1; `AppShell.stage1.test.tsx`: **7 failed / 11 passed** (S1-a1, S1-a2, S1-a3, four S1-a traffic rows — exactly the steered set), exit 1 | `fail-switch-off-featureSwitches.log`, `fail-switch-off-AppShell.log` |
| Must PASS with the switch on | `470d56f54` merged (the dev's real flip, `ROOMS_ENABLED = true`), no test-side switch mock | `featureSwitches.stage1.test.ts`: 3/3, exit 0; `AppShell.stage1.test.tsx`: **18/18** (all 7 re-aimed pins green, all 11 untouched pins still green), exit 0 | `pass-switch-on-featureSwitches.log`, `pass-switch-on-AppShell.log` |

Every off-switch failure names its superseded pole: missing `group-chats-sales` /
`add-participant` / `room-traffic-entry`, and S1-a3's redirect observed as
`expected '/workspaces/sales/chat' to be '/workspaces/sales/rooms/launch'`. `npm run
typecheck` exit 0 on the merged tree (`typecheck.log`). No production file was authored
or reverted by QA: the switch flip arrives only through the merged dev commit, and the
branch delta over `470d56f54` touches test files, the ledger and its logs only.



## Accepted scope and tested states

| Item | Evidence/source |
|---|---|
| Acceptance source | Team-lead dispatch and accepted follow-ups: founder decision 2026-10-14, stage 1 hides unserved features and uses the release pop-up; (g) real-module false-value guards; (h) visible enabled mounts; added room_ref / secret live/snapshot absence; release-modal queue and expiry |
| Original UI head | ca09d06f9d34bbcc63f158dc63d06973b9bb62f3 |
| Required UI hardening head | e4c84116b36637810c59961e90def0f83dee0370 — merged explicitly at team-lead's request, with candidate server 45f2659a0, catalog fix, four room-traffic gates, secret-tail gate and docs |
| Tests-only follow-up commit | ec17f7f4e9627a9f70357d31a279ab318aacece6 — preserved before production integration |
| Exact executed merge commit | 0da2c2a52d17da9e7ce04c064bff1aa03115171c; later commit changes evidence documents only, not executed code |
| Historical behavioural RED | Unchanged production 08a204c98ab043f8f2494a5e042bb675eb4e17f3: **18/18 named behavioural failures**, no collection/unhandled errors |
| Immediate pre-hardening RED | ca09d06f9: **6 failed / 12 passed** in the extended shell file — four visible room-traffic variants and two visible secret-card carriers. All six pass after the accepted hardening merge. No assertions were adapted. |
| Real constant guards | 3/3 current executions; no qualified historical RED because the real module was introduced already false. Its absence before introduction is not behavioural RED. Three isolated switch mutants are assigned to CHECK. |
| Mutation ownership | No production mutants performed by RED. The two scratch checkouts hold actual unchanged historical/stock commits, not invented implementation variants. |

## Artifacts

| Artifact | Absolute path |
|---|---|
| Plan and case derivation | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-test-plan.md |
| Source-bound serial receipts, exits, named RED failures and log hashes | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-execution-receipt.json |
| Enabled-mount calibration and visible assertions | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-enabled-mount-receipt.json |
| Oracle-preservation instrument | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/verify-stage1-retargeting.mjs |
| Shipped guards | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/lib/featureSwitches.stage1.test.ts |
| Real-shell acceptance | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/AppShell.stage1.test.tsx |
| Application support | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-harness.tsx |
| Raw execution artifacts (local, retained) | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/ |

## Existing files: original oracles retained

Original before-runs were captured before editing. Every final after-run is on merge commit 0da2c2a52, which includes UI head e4c84116b. The exact saved command, raw exit, case count and hash for each run are in the execution receipt. Seven collection failures are explicitly **not** counted as behavioural RED.

| File | Original before receipt | Test-only retarget | Final after, exit=0 |
|---|---|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/sidebar/WorkspaceAgentList.rooms.test.tsx | exit=1; 7 failed / 10 passed | ROOMS_ENABLED mocked ON; every original oracle retained; shared group helper and second-workspace group require visibility | 17/17 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/chat/AddParticipant.test.tsx | exit=1; 1 failed / 8 passed | ROOMS_ENABLED mocked ON; original real-header visible Add participant assertion retained exactly | 9/9 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/AppShell.test.tsx | exit=1; 1 failed / 28 passed | INLINE_TOOL_APPROVALS_ENABLED mocked ON; original coexistence/order assertions retained; both banners additionally require visibility | 29/29 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.test.tsx | exit=1; collection error: missing registerChatSetReplaying | importOriginal session-module completion; original mocked hook/static actions retained | 35/35 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.focus-trap.test.tsx | Same collection error | Same completion, no assertion change | 6/6 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.kb5.test.tsx | Same collection error | Same completion, no assertion change | 4/4 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.m5.test.tsx | Same collection error | Same completion, no assertion change | 18/18 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.panelGuard.test.tsx | Same collection error | Same completion, no assertion change | 2/2 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.panelGuard.matrix.test.tsx | Same collection error | Same completion, no assertion change | 2/2 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.shortcuts.editor.test.tsx | Same collection error | Same completion, no assertion change | 9/9 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/store/chat.sessionModeUpdate.test.ts | exit=1; 3 failed / 16 passed | Select Mia/core in the first-send fixture; Auto-approve assertions unchanged | 19/19 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/store/chat.recipient-send.test.ts | exit=1; 1 failed / 27 passed | Only the minting case selects Jim/core; all recipient/content/session assertions unchanged | 28/28 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/lib/ws.test.ts | exit=1; 1 failed / 81 passed | Exact expected client set adds only attach_room, from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/contracts/asyncapi.yaml::operations.sendAttachRoom and AttachRoomFrame.type.const; equality assertion unchanged | 82/82 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/chat/RoomTrafficEntry.test.tsx | ca09 original exit=0, 9/9; stock e4c84116b unretargeted exit=1, 6 failed / 3 passed | ROOMS_ENABLED mocked ON; all 6 real ChatThread live/REST/replay/ordinary-neighbour carrier cases retain their original oracle; shared toggle additionally requires visibility | 9/9 |
| **Existing total** | Baseline evidence preserved, including harness errors rather than concealed skips | **629 original assertion expressions retained + 5 visibility assertions** | **269/269** |

### Follow-up (h): visible enabled mounts, not vacuous presence

| Gated retarget | ON configuration | Visible positive mount | Unchanged stock e4c84116b with real OFF switches |
|---|---|---|---|
| Seven workspace Group chats cases | ROOMS_ENABLED=true | Shared showSidebar requires group.toBeVisible; the second-workspace section also requires visibility | All 7 fail because the group is absent |
| Real ChatScreen header case | ROOMS_ENABLED=true | Existing Add participant.toBeVisible is unchanged | The header case fails because the button is absent |
| Approval-banner coexistence case | INLINE_TOOL_APPROVALS_ENABLED=true | Original presence/order plus connectionBanner.toBeVisible and approvalBanner.toBeVisible | The coexistence case fails because the approval banner is absent |
| Six RoomTrafficEntry carrier cases | ROOMS_ENABLED=true | All call expectCollapsed, now also toggle.toBeVisible; direct component cases remain covered too | All 6 carriers fail at missing room-traffic-toggle; the 3 direct-component cases still pass |

These 15 stock OFF failures and their final ON receipts calibrate the original oracles without mutating production. The other ten retargets are import/owner/contract repairs, not feature-gated UI mount tests, so no unrelated switch mock was added. SecretRequestCard behaviour/security/state packs mount card/tail directly, not through ChatScreen/ChatThread; they did not require retargeting. A search control found the known real ChatThread carriers, so an empty search for another gated carrier pack was not trusted blindly.

## New cases and exact fresh CHECK mutants

The real-shell acceptance and its support contain **no feature-switch/module mocks or application spies**. Real routes, AppShell, ChatScreen, profile, modal, stores, generated HTTP clients and socket parser/dispatcher execute. The guard statically imports the real feature-switch module. The doubles are browser/network/clock edges only.

Mutation target aliases (all absolute):

| Alias | Target |
|---|---|
| Switches | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/lib/featureSwitches.ts |
| Modal host | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/AppShell.tsx::AppShell |
| Modal queue/expiry | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/agents/ToolApprovalModal.tsx::ToolApprovalModal / ToolApprovalCard |
| Room mounts | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/chat/ChatScreen.tsx::PlainMessageList / VirtualizedMessageList |
| Secret mount | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/chat/ChatScreen.tsx::ChatScreen |

| New case | Frozen oracle | Exact mutant for fresh CHECK (not executed by RED) |
|---|---|---|
| S1-a1 expanded workspace | **Re-aimed 2026-10-18:** Group chats section + room row visible, room-list read exactly once (was: no Group chats, no room REST reads) | Switches: ROOMS_ENABLED=true → false |
| S1-a2 member main header | **Re-aimed 2026-10-18:** Add participant entry visible, no attach_room/room-detail read (was: no Add participant, no room reads) | Switches: ROOMS_ENABLED=true → false |
| S1-a3 direct room URL | **Re-aimed 2026-10-18:** same URL mounts the room, reads it, attaches it, renders from a real snapshot (was: beforeLoad redirect, no room REST, no attach_room) | Switches: ROOMS_ENABLED=true → false |
| S1-b profile Tools | Sidebar Edit opens shell-owned profile; Tools loads, no Private secrets or GET secrets | Switches: AGENT_PRIVATE_SECRETS_ENABLED=false → true |
| S1-c Approve Once on Settings | Current-chat approval shown globally; exact POST action allow_once; no navigation | Modal host: delete its conditional ToolApprovalModal mount |
| S1-c Deny on Settings | Exact POST action deny; no navigation | Modal host: delete its conditional ToolApprovalModal mount |
| S1-d child without root id | Incoming child approval actionable with parent main unchanged; no Go there detour | Modal host: delete modal mount; narrower: visible queue filtered by entry.sessionId === useSessionStore.getState().activeSessionId |
| S1-e own-chat single display | One modal and one Approve Once button including hidden nodes; no inline panel/tail/banner | Switches: INLINE_TOOL_APPROVALS_ENABLED=false → true |
| S1-f other workspace | Marketing approval actionable while Sales/main remain active | Modal host: delete modal mount; narrower: import useWorkspacesStore and filter visible queue by entry.workspaceId === its activeWorkspaceId |
| S1-a traffic plain request | **Re-aimed 2026-10-18:** actual wire room_ref retained; ordinary row visible; exactly one compact entry + visible toggle expanding to the "Open group" link; no room REST read (was: no entry/toggle/link) | Switches: ROOMS_ENABLED=true → false; narrower: remove check at PlainMessageList room mount |
| S1-a traffic virtual request | **Re-aimed 2026-10-18:** same switched-on oracle through real measured virtualized historical rows — closes the WU-15 CHECK finding 2 jsdom gap for the virtualized branch | Switches: ROOMS_ENABLED=true → false; narrower: remove check in rowForMessage |
| S1-a traffic streaming answer | **Re-aimed 2026-10-18:** real token frame carries room_ref; the streaming slot renders the compact entry + toggle (was: no entry in streaming slot) — closes the jsdom gap for the streaming branch | Switches: ROOMS_ENABLED=true → false; narrower: remove check at streaming RoomTrafficMessage mount |
| S1-a traffic completed answer | **Re-aimed 2026-10-18:** real token/done transitions keep room_ref and render the compact entry (was: no room entry) | Switches: ROOMS_ENABLED=true → false; narrower: remove historical-row check |
| S1-b live secret_request | Exact pending record arrives in real store; no card/tail/value field/secret request | Switches: AGENT_PRIVATE_SECRETS_ENABLED=false → true; narrower: remove SecretRequestThreadTail mount guard |
| S1-b session_state.secret_requests | Same absence after real reconnect-style snapshot; exact pending payload retained | Same AGENT_PRIVATE_SECRETS_ENABLED false → true, or secret-tail guard removal |
| S1-q three-entry queue | Parent/child/other-workspace queue is shown in arrival order; approve/deny/approve produce exact 3 POSTs, no navigation or lost request | Modal host: delete modal mount; narrower: filter visible queue by active session, losing child/other-workspace |
| S1-exp deadline | Actions available at deadline−1, absent at deadline; no automatic POST; denied_timeout removes head and promotes next | Modal host: delete modal mount; narrower: force ToolApprovalCard's hasExpired=false |
| S1-exp zero lifetime | Contract-minimum expires_in_ms=0 is non-actionable on arrival; dismiss removes notice without POST | Modal host: delete modal mount; narrower: force hasExpired=false. Dismiss follows agreed release UI; non-actionability/no automatic submission are the safety oracles. |
| S1-g rooms guard | **Re-aimed 2026-10-18:** REAL ROOMS_ENABLED is exactly true, founder Train 3 decision cited in test (was: exactly false per 2026-10-14) | Switches: ROOMS_ENABLED=true → false |
| S1-g secrets guard | REAL AGENT_PRIVATE_SECRETS_ENABLED is exactly false | Switches: AGENT_PRIVATE_SECRETS_ENABLED=false → true |
| S1-g approvals guard | REAL INLINE_TOOL_APPROVALS_ENABLED is exactly false | Switches: INLINE_TOOL_APPROVALS_ENABLED=false → true |

The 18 behavioural cases have actual historical RED on unchanged 08a204c98. The six added room/secret cases also have immediate pre-fix RED on ca09d06f9 and successful final execution after e4c84116b. The three constant guards do not have qualified historical RED: **UNVERIFIED failability pending the explicitly assigned independent mutations**. No constant was changed by RED to manufacture a failure.

## Instrument controls and authoring corrections

| Control/correction | What is established |
|---|---|
| Oracle-preservation instrument | In-memory weakening/suppression controls are detected before comparing all 14 existing packs. It permits exactly the five required added visibility assertions, rejects their absence/duplication, then compares every original assertion/case in order. The original 9-case shell pack is an unchanged prefix. |
| Real browser/socket/store inputs | One real WsConnection per shell. Generated frame validation is live; exact room_ref or secret payload must arrive in real stores. Ordinary message visibility proves the transcript actually draws rows. Main cases require real attach_session and real sidebar member selection. |
| Browser edge corrections | Initial catch-up sequence 0 was invalid; corrected to contract-minimum 1. jsdom's interval-backed animation frame stalled under controlled expiry intervals; frame delivery now uses a real browser-timeout edge. Missing Element.scrollTo and zero viewport measurements were supplied at browser edges only, with prototype restoration. These setup failures/unhandled errors are excluded from RED proof. |
| Expiry | Only Date/interval clocks are controlled, no application helper mocked. Deadline−1/deadline and zero-lifetime cases prove absence of actionable controls and no automatic POST; real denied_timeout frame advances the queue. |
| Approval container discovery | ToolApprovalModal-named legacy files actually load the inline ToolApprovalPanel; their success was not counted as shipped-modal coverage. The real-shell pack directly covers the restored release dialog. |
| Earlier corrections | Draft approved booleans were corrected to independent contract action values before assertions. Unsupported role-query exact options and missing production router type registration in the separate tests project were fixed only in test support. No tolerance widened or production fixed by QA. |

## Final gates

Every command has a saved log with its raw exit, and every executed source/log hash is bound in the execution receipt. Final code remains byte-identical to the tested merge commit; final evidence-only commit is not a new executable state.

| Gate | Exact receipt / result |
|---|---|
| All touched files, original 10, 3 stale fixtures, RoomTrafficEntry, guard and shell | 16 individual serial Vitest commands: all exit=0, **290/290**, in the committed execution receipt |
| Historical behavioural RED | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/extended-historical-final.log — exit=1, 18 failed (18) |
| Historical integrity | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/extended-historical-integrity.log — git diff --exit-code exit=0 |
| Immediate pre-hardening RED | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/final-extended-AppShell.stage1.test.tsx.log — exit=1, 6 failed / 12 passed |
| Enabled visible-mount calibration | Four stock-OFF commands, all exit=1 for expected missing gated UI (7/1/1/6 cases), production unchanged; final ON runs all exit=0. Source-bound enabled-mount receipt captures exact commands/hashes. |
| Preservation | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/merged-final-gate-preservation.log — exit=0, 629 original assertions retained + 5 visible additions |
| Typecheck | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/merged-final-gate-typecheck.log — npm run typecheck / tsc -b --noEmit, exit=0 |
| Targeted lint | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/merged-final-gate-lint.log — touched follow-up code/support only, exit=0 |
| Design-system locks | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/merged-final-gate-design-system.log — exit=0, PASS (0 errors) |
| Function instrument/gate | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/merged-final-gate-function-selfcheck.log and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/merged-final-gate-function-budget.log — both exit=0, no FAIL, warnings retained |
| CI discovery | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/merged-final-gate-discovery.log — exit=0, all 1111 SPA files covered by 29 patterns; one warning about an empty design-system pattern retained |
| Completed CI, whole local suite, Go suite, live UAT, independent CHECK | Not claimed; no whole local suite or production mutation was run by this RED writer |

## Scope and outstanding notes

Relative to accepted UI head e4c84116b, QA changes are **test files/test support/evidence only**. All production changes are inherited unchanged from the explicitly requested merge; no manual production conflict resolution or production fix occurred. The patch was not landed on the integration branch or main. No new user-visible product behaviour was authored by QA, so no additional user-facing documentation draft is required; matching UI hardening docs are inherited from the implementing lead and remain for independent docs verification.

GitNexus reported this checkout unindexed and resolved stale sibling-index results on impact/detect attempts. These results were not trusted as scope proof. Caller searches, reviewed Git diffs, source-hash checks and strict assertion preservation are the fallback; impact assessment is **Inferred (high confidence): test-only edits**.

Runtime diagnostics remain unsuppressed: route-helper discovery warnings, unavailable Web Worker in jsdom (real parsing falls back inline), and skills/commands API clients reporting missing response validators. The original installation reported 5 low-severity dependency advisories, and the prior push reported 40 default-branch advisories (17 high, 19 moderate, 4 low). No security audit/remediation is claimed; dispatcher follow-up is needed, not silently deferred. The stale modal scope comment is an incidental documentation note, not changed by QA.

Code correct and tested: **RED pack and local execution receipts supplied; independent CHECK and wider CI remain outstanding**.
Reachable by a user/agent: **actual-shell reachability/unreachability exercised with process-edge doubles; live browser/UAT not claimed**.

Skills: omnipus-shared-rules, elicify-test-writing, omnipus-design-system, gitnexus-impact-analysis, github-cli, commit-messages; jev-use:jev-use loaded earlier in the same task.

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Every requested follow-up included | Real-module guard file, complete 21-case mutant table above, strict original-oracle comparison and source-bound receipts | Verified (high) |
| Original oracles preserved; visible ON mounts discriminating | Preservation command exit=0, 629 retained / 5 added; stock-OFF 15 expected failures vs final enabled runs; original 9 shell cases unchanged | Verified (high) |
| Behavioural RED before accepted hardening | Unchanged historical SHA 08a204c98, exit=1, 18/18 named failures; ca09 pre-hardening exit=1, 6/18 expected failures; integrity checks exit=0 | Verified (high) |
| Final execution covers exact merged head | Merge commit 0da2c2a52 with e4c84116b ancestor; 16 serial commands exit=0, 290/290; executable source hashes captured | Verified (high); independent GREEN deferred |
| Type, design, discovery and budget checks ran meaningfully | Seven final gate receipts all exit=0; typecheck previously detected real authoring errors; budget selfcheck detects offenders | Verified (high) |
| Production changes are inherited, not QA-authored fixes | Accepted e4c84116b merge; Git delta from that head contains only test/support/evidence files; no manual conflicts | Verified (high) |
| Independent failability/mutation verdict | No production mutants run; 3 guard historical RED claims expressly UNVERIFIED; exact probes assigned to fresh CHECK | Unknown / deferred to CHECK |
| **Self-check** | Reviewed final test/support and ledger diffs against all dispatch rows; re-ran all 16 files after integration, final gates, historical RED and enabled-mount calibration; source/log hashes and tests-only scope verified before final commit/push. No assertions weakened or production edited to pass tests. | Verified (high); independent audit/CI/UAT remain unclaimed |
