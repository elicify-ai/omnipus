# L1 UI stage 1 — RED pack and before/after ledger

**Bottom line:** 13 existing files retargeted, with every assertion and case declaration preserved; nine new real-application acceptance cases cover dispatch 2(a)–(f). The final new tests show 9/9 behavioural RED failures on unchanged historical production. Current-head serial execution receipts collect 269/269 passing cases across 14 files. **Independent GREEN verification, production mutation probes and the Proof-of-failability checklist remain deferred to fresh CHECK. This is not a CHECK verdict or release approval.**

## Specification and source states

| Item | Source |
|---|---|
| Stage-1 expectations | Team-lead dispatch on 2026-10-14: founder decided unserved features HIDDEN and approvals use the RELEASE pop-up; Q1 A clarified RED/CHECK responsibilities |
| Additional fixture repairs | Team-lead supplemental dispatch: first-send agent selection in two store packs; exact client-frame contract set includes attach_room |
| Production under test | ca09d06f9d34bbcc63f158dc63d06973b9bb62f3 |
| Unchanged historical RED baseline | 08a204c98ab043f8f2494a5e042bb675eb4e17f3 — the immediate parent of the stage-1 production change, not an invented mutant |
| Retarget/fixture commit | 75d4ab0e194e6f09106ee0987ce352428113c682 |
| Test plan | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-test-plan.md |
| Machine-readable evidence | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-execution-receipt.json — exact commands in raw logs, exits, case counts, named failures and SHA-256 hashes of logs and executed source |
| Raw saved execution artifacts | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/ — retained locally; every exact file location is in the machine-readable receipt |

## Existing tests: before → after

Every before run used the original test file on production ca09d06f9. Each after run used the same production with only the listed test-fixture change. Each file ran individually and serially with verbose collection and an unpiped raw exit. All assertion call expressions and test/describe declarations are unchanged; **610 assertion expressions preserved**. The sole expected-data change is adding the independently specified attach_room discriminator to the exact expected set, not changing its equality matcher.

| File | Before receipt (exit=1) | Test-only retarget | After receipt (exit=0) |
|---|---|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/sidebar/WorkspaceAgentList.rooms.test.tsx | 7 failed / 10 passed; Group chats absent | Partial feature-switch mock, ROOMS_ENABLED=true; all 7 original room-section oracles retained, other 10 cases unchanged | 17/17 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/chat/AddParticipant.test.tsx | 1 failed / 8 passed; real header Add participant absent | ROOMS_ENABLED=true in the test configuration; header case and all its assertions retained | 9/9 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/AppShell.test.tsx | 1 failed / 28 passed; cross-workspace banner absent | INLINE_TOOL_APPROVALS_ENABLED=true in the test configuration; coexistence oracle retained | 29/29 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.test.tsx | Collection failure: missing registerChatSetReplaying; no cases executed | Spread importOriginal for session module, preserving the mocked hook/static actions | 35/35 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.focus-trap.test.tsx | Same collection failure | Same importOriginal completion; existing mock hook unchanged | 6/6 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.kb5.test.tsx | Same collection failure | Same importOriginal completion; existing mock hook unchanged | 4/4 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.m5.test.tsx | Same collection failure | Same importOriginal completion; existing mock hook unchanged | 18/18 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.panelGuard.test.tsx | Same collection failure | Same importOriginal completion; existing hook/static unchanged | 2/2 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.panelGuard.matrix.test.tsx | Same collection failure | Same importOriginal completion; existing hook/static unchanged | 2/2 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/Sidebar.shortcuts.editor.test.tsx | Same collection failure | Same importOriginal completion; existing hook/static unchanged | 9/9 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/store/chat.sessionModeUpdate.test.ts | 3 failed / 16 passed; no first-send frame without an owner | First-send fixture selects Mia/core; all Auto-approve assertions unchanged | 19/19 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/store/chat.recipient-send.test.ts | 1 failed / 27 passed; minting payload undefined | Only the minting case selects Jim/core; all recipient/content/session assertions unchanged | 28/28 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/lib/ws.test.ts | 1 failed / 81 passed; exact set lacked attach_room | Add attach_room, sourced from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/contracts/asyncapi.yaml::operations.sendAttachRoom and components.schemas.AttachRoomFrame.type.const; exact equality unchanged | 82/82 |
| **Total existing pack** | 14 behavioural failures plus 7 collection failures (collection failures are NOT behavioural RED) | No assertion weakening, skipping, deletion, timeout widening or production changes | **260/260** |

## New cases and exact mutants assigned to fresh CHECK

Acceptance file: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/AppShell.stage1.test.tsx . Support: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-harness.tsx . The harness imports the **actual complete production route tree**, not a test redirect or test-only AppShell substitute. Switches are unmocked. No production component, hook, store or API client is mocked/spied. The doubles are HTTP/WebSocket and missing browser capabilities only.

| Case | Oracle from dispatch | Historical RED on 08a204c98 | Exact CHECK mutant (not run by RED) |
|---|---|---|---|
| S1-a1 | Expanded populated workspace has no Group chats and sends zero room-collection reads | Group chats span existed; expected null | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/lib/featureSwitches.ts::ROOMS_ENABLED: false → true |
| S1-a2 | Member main chat has no Add participant and no room reads | Add participant button existed; expected null | Same ROOMS_ENABLED: false → true |
| S1-a3 | Real direct room route redirects to workspace chat, sends no room REST requests or attach_room | Actual path /workspaces/sales/rooms/launch instead of /workspaces/sales/chat | Same ROOMS_ENABLED: false → true; additional narrow probe: remove disabled-room beforeLoad redirect in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/routes/_app/workspaces.$workspaceId.rooms.$roomId.tsx::Route |
| S1-b | Real sidebar Edit opens shell-owned conversation-agent profile; Tools has no Private secrets and no GET secrets | Private secrets heading existed; expected null | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/lib/featureSwitches.ts::AGENT_PRIVATE_SECRETS_ENABLED: false → true |
| S1-c Approve Once | Settings stays open, active-chat approval is visible; exact POST action allow_once, credentials include, then modal resolves | No Tool Approval Required dialog although frame entered real queue | Remove the `!INLINE_TOOL_APPROVALS_ENABLED && <ToolApprovalModal />` mount from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/layout/AppShell.tsx::AppShell |
| S1-c Deny | Same Settings reachability; exact POST action deny | Same missing dialog after real queue receipt | Same AppShell modal-mount removal |
| S1-d | Child session, root_session_id absent; parent main remains open; live incoming approval opens modal without Go there navigation | Same missing dialog after real queue receipt | Same AppShell modal-mount removal; additional targeted probe: change `const visible = queue` to `const visible = queue.filter((entry) => entry.sessionId === useSessionStore.getState().activeSessionId)` in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/agents/ToolApprovalModal.tsx::ToolApprovalModal |
| S1-e | Exactly one approval surface: one dialog, one Approve Once button including hidden nodes, no inline card/tail, no banner | Missing modal despite own-chat pending approval | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/lib/featureSwitches.ts::INLINE_TOOL_APPROVALS_ENABLED: false → true |
| S1-f | Marketing approval is actionable in modal while Sales and Mia main remain active | Missing modal after other-workspace frame entered queue | Same AppShell modal-mount removal; additional targeted probe: import useWorkspacesStore and change `const visible = queue` to `const visible = queue.filter((entry) => entry.workspaceId === useWorkspacesStore.getState().activeWorkspaceId)` in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/agents/ToolApprovalModal.tsx::ToolApprovalModal |

All nine final cases failed for the required behavioural difference, with no collection errors. Current-head execution collected all nine successfully with exit=0. Mutation execution, mutation-kill claims, green verification and the Proof-of-failability checklist are **deferred to CHECK**, not met by these receipts.

## Positive controls / authoring corrections

| Control or correction | Evidence / result |
|---|---|
| Forbidden-request recorder can see real traffic | Historical source issues room/secrets traffic; current tests also require successful real agents/workspaces/tools reads. Every request is recorded before HTTP response handling. Unknown requests fail afterEach, never default to an empty success. |
| WebSocket instrumentation is live | Each mounted shell creates one real WsConnection with a browser-edge TestSocket. Main cases assert actual attach_session output. Incoming approval frames use generated validation, real parser and real provider dispatcher before asserting exact queue id and modal content. |
| The main-only branch is genuinely exercised | Connection-open reattach clears attachedSessionType. The harness therefore selects Mia through the real sidebar after opening the socket and asserts main type, avoiding a vacuous Add participant absence assertion. |
| Duplicate responsive profile DOM is scoped correctly | Profile Basics/Tools are queried through accessible tabpanels, not an ambiguous test id matching both desktop and hidden phone copies. |
| Assertion-preservation instrument can detect weakening | Its in-memory controls detect toEqual→toBeTruthy and it→it.skip before comparing all 13 files. No production mutation. |
| Draft request-body correction | Initial planning draft incorrectly said approved=true/false. Corrected before assertions to the independently read ToolApprovalActionRequest contract action=allow_once/deny. |
| Typecheck correction | First typecheck caught unsupported exact options on role queries. Removed unused options; the library's role matcher uses exact accessible-name equality. No oracle widened. Importing the complete route tree in the separate tests project also needed the actual application router type registration; a type-only import from the production main module fixes this without executing it or editing production. |
| Historical attempt 1 | Not counted as all-nine RED proof: it exposed main-type and duplicate profile setup errors alongside valid feature failures. Attempts 2 and final provide 9/9 behavioural RED; final receipt hashes bind to the executed final source. |

## Gates and limits

| Gate | Saved receipt | Observed result |
|---|---|---|
| Dependency install | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/npm-ci.log | npm ci exit=0; 1144 packages added |
| Assertion preservation | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/assertion-preservation.log | node preservation instrument exit=0; 610 assertion expressions and all case declarations unchanged; exact frame set = old + attach_room |
| Historical final new-file RED | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/stage1-historical-final.log | One-file Vitest exit=1; 9/9 named behavioural failures |
| Historical production integrity | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/historical-integrity.log | git diff --exit-code exit=0; no tracked production diff in historical checkout |
| Current final new-file receipt | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/stage1-current-final.log | One-file Vitest exit=0; 9/9 collected |
| Typecheck, actual project references | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/typecheck-after-harness-registration.log | npm run typecheck → tsc -b --noEmit; exit=0 |
| Function-budget instrument | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/function-budget-selfcheck.log | exit=0; provably fails on offenders, passes clean/grandfathered fixtures |
| Function budget | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/function-budget.log | exit=0; no FAIL; warn-only output, grandfathered 70 functions / 72 components over 240 |
| Design-system locks | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/design-system-locks.log | npm run lint:design-system-locks exit=0; PASS (0 errors) |
| CI discovery coverage | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/coverage-discovery.log | discovery guard exit=0; all 1106 SPA test files covered by 29 patterns, with one warning about an empty design-system pattern; new file covered |
| Touched-file lint | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/targeted-lint.log | eslint on the 14 touched test files plus harness/instrument only; exit=0 |
| Full suite / independent audit / live UAT | Not run by this RED writer | UNVERIFIED / deferred; no full local Go or frontend suite, no production mutation |

### Scope and incidental notes

GitNexus status reported this worktree unindexed. The impact attempt and pre-commit detect-changes resolved a sibling index 3925 commits stale, returned UNKNOWN / no changes, and are **not trusted scope proof**. Caller searches, assertion-tree comparison and the reviewed Git diff are the fallback. Blast-radius assessment is **Inferred (high confidence): test fixtures/test support only, no production caller or execution-flow edits**.

The existing runtime diagnostics appear on both unchanged historical production and current production: route-generator warnings about authValidation helper/test files; Web Worker unavailable in jsdom, so real parsing falls back inline; and the skills API client reports no generated response validator. No console suppression or lint disable was added. npm ci reports **5 low-severity dependency advisories**; no remediation attempted in this tests-only task. The stale comment above ToolApprovalModal still describes active-workspace scoping while its actual visible queue is app-wide; reported as a documentation note, not changed.

Code correct and tested: **not a delivery verdict**; RED pack and execution receipts supplied, independent CHECK required.
Reachable by a user/agent: **real-shell acceptance cases supplied** for the stage-1 hidden entry points and globally actionable modal; live browser/UAT not claimed.

Skills: omnipus-shared-rules, elicify-test-writing, omnipus-design-system, gitnexus-impact-analysis, jev-use:jev-use, github-cli, commit-messages.

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Existing oracles retained across 13 files | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/verify-stage1-retargeting.mjs::snapshot; instrument exit=0, all 13 comparisons unchanged; raw before/after receipt hashes in committed JSON | Verified (high) |
| New final tests have actual pre-change behavioural RED | Historical final single-file command, exit=1, Tests 9 failed (9); unchanged production SHA 08a204c98 and integrity exit=0 | Verified (high) |
| Current execution covers the entire touched test-file pack | 13 after receipts plus final acceptance receipt, all exit=0; machine-readable count validates 14 files / 269 cases | Verified (high); independent GREEN deferred |
| Typecheck and size/design/discovery instruments ran meaningfully | Gate receipts above; typecheck caught actual authoring errors before correction; budget selfcheck detects offenders; all final raw exits=0 | Verified (high) |
| Impact is limited to tests, not an indexed graph claim | Unindexed/stale-index receipts; caller fallback, assertion comparison and tests-only Git diff | Inferred (high), not a valid GitNexus graph result |
| CHECK mutation outcomes | No production mutants executed by this RED writer; exact probes specified per case | Unknown; deferred to fresh CHECK |
| **Self-check** | Re-read the retarget diff and final test/harness boundary against dispatch (a)–(f); re-ran final new-file historical RED and current receipt, typecheck, preservation control, budget selfcheck/gate, design locks and discovery. Final test source hashes are captured in the execution receipt; no assertion weakening or production edits. | Verified (high); independent audit and live UAT remain unclaimed |
