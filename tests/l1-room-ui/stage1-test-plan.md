# L1 UI stage 1 — tests-only RED plan

Source: team-lead dispatch, founder decisions 2026-10-14, followed by Q1 A clarification. This dispatch is the stage-1 acceptance specification. It intentionally supersedes the earlier rooms/private-secrets/inline-approval reachability expectations at shipped switch values; the original enabled-feature oracles remain unchanged.

## Behaviour under test

The real application shell hides unserved rooms and agent-private secrets and presents every pending tool approval in the release pop-up, regardless of route, session ancestry or active workspace.

## Boundary

REAL: application shell, sidebar, chat header, agent profile/tools panel, actual route definitions and redirects, query client, generated API clients, stores, WebSocket parsing and application dispatch, approval modal and controls. The stage-1 acceptance file must not mock feature switches, these components, their hooks, the router or stores.

DOUBLES: HTTP fetch and browser WebSocket only, plus browser capabilities that jsdom cannot implement. Responses use generated contract types. The network recorder records every request before responding, including forbidden/unexpected reads. A successful shell/sidebar/chat/tools mount and known network/socket traffic provide positive instrument controls for absence assertions.

Retargeted legacy files keep their existing component boundaries. Feature-switch mocks only select the explicitly requested future-enabled configuration. Seven incomplete session-module mocks spread importOriginal, retaining the original mocked hook/actions and every assertion while preserving real registration exports.

## Case table and exact CHECK mutants

| ID | Case/input | Expected observable outcome | Independent source | Exact mutant for fresh CHECK |
|---|---|---|---|---|
| S1-a1 | Real shell, expanded populated workspace | Workspace/member controls present; no Group chats section; zero GET workspace rooms collections | Dispatch 2(a) | Change ROOMS_ENABLED from false to true in the feature-switch module |
| S1-a2 | Member main chat open through real shell | Real member header/composer present; no Add participant control; zero room-list reads | Dispatch 2(a) | Change ROOMS_ENABLED from false to true |
| S1-a3 | Direct workspace room URL | Actual room beforeLoad redirects to that workspace chat; no room REST requests, no attach_room frame, zero workspace room-list reads | Dispatch 2(a) | Change ROOMS_ENABLED from false to true (or remove the disabled-room beforeLoad redirect) |
| S1-b | Conversation agent profile, Tools panel | Actual Tools panel loaded; no Private secrets section; zero GET secrets reads | Dispatch 2(b) | Change AGENT_PRIVATE_SECRETS_ENABLED from false to true |
| S1-c-approve | Settings route, approval session equals active main chat | Release modal visible without navigation; Approve sends exactly POST /api/v1/tool-approvals/{id} with action=allow_once | Dispatch 2(c), release approval request contract | Remove ToolApprovalModal mount from AppShell |
| S1-c-deny | Settings route, approval session equals active main chat | Release modal visible without navigation; Deny sends exactly the same POST with action=deny | Dispatch 2(c), release approval request contract | Remove ToolApprovalModal mount from AppShell |
| S1-d | Parent main chat open; incoming child approval with no root_session_id | Modal appears from incoming approval without changing chat/route; no Go there detour | Dispatch 2(d) | Remove ToolApprovalModal mount from AppShell (additional targeted mutant: restore active-session-only queue filter) |
| S1-e | Chat open with its own approval pending | Exactly one approval UI: modal, zero inline cards and zero approval banners | Dispatch 2(e) | Change INLINE_TOOL_APPROVALS_ENABLED from false to true |
| S1-f | Different workspace active; incoming approval belongs to another workspace | Modal shows that approval without switching workspace or route | Dispatch 2(f) | Remove ToolApprovalModal mount from AppShell (additional targeted mutant: restore active-workspace-only queue filter) |

The exact API body is to be confirmed from the generated contract before writing assertions, not from observed implementation output. No numeric input domain is introduced. Approval count boundaries are empty/one; child/root absence and same/different workspace are the required equivalence classes. S1-a1/a2/a3/b/e are negative-path or invariant tests (5 of 9 = 55.6%).

## Retargeting ledger plan

Capture the original ten files individually on UI head ca09d06f9d34bbcc63f158dc63d06973b9bb62f3 before editing. Preserve case names and assertion expression trees exactly. Record the switch/mock-only diff and original failure mechanism per file. Run only individual files, serially, with command, source state and raw exit recorded; a collection error is a harness failure, not behavioural RED proof.

Supplemental team-lead dispatch adds three existing packs: select an explicit owner in the first-send Auto-approve/recipient fixtures; add the contract's attach_room discriminator to the exact WebSocket frame set. Preserve every existing assertion and case declaration in these too. The full before/after ledger therefore contains 13 existing files, plus the new nine-case shell file.

## Accepted follow-up (g)/(h), before writing assertions

Source: team-lead accepts the chief's stage-1 requirements, 2026-10-14.

| Case | Boundary and independent oracle | Exact mutant for fresh CHECK |
|---|---|---|
| S1-g rooms | Static import of the real feature-switch module, no mock; ROOMS_ENABLED must be exactly false per founder's stage-1 decision of 2026-10-14 | Set ROOMS_ENABLED=true in the production switch module |
| S1-g secrets | Same real module; AGENT_PRIVATE_SECRETS_ENABLED must be exactly false per the same decision | Set AGENT_PRIVATE_SECRETS_ENABLED=true |
| S1-g approvals | Same real module; INLINE_TOOL_APPROVALS_ENABLED must be exactly false per the same decision | Set INLINE_TOOL_APPROVALS_ENABLED=true |
| S1-h enabled rooms | Keep all seven enabled room-section oracles; add a visible group-mount assertion to their shared helper and a visible assertion for the second workspace section | CHECK can hide the enabled Group chats section; no production mutation by RED |
| S1-h enabled header | Existing real ChatScreen header case already asserts Add participant.toBeVisible with ROOMS_ENABLED mocked ON; preserve it byte-for-byte | Existing enabled-path oracle retained |
| S1-h enabled banner | Keep original coexistence/order assertions and INLINE_TOOL_APPROVALS_ENABLED mocked ON; add visibility of both the approval banner and connection-error banner | CHECK can hide the mounted approval banner; no production mutation by RED |

The other ten retargeted files (seven session-module mock completions, two first-send fixtures, and the exact client-frame set) have no feature-switch-gated UI mount. They remain unchanged by this follow-up. The unreachability suite and its support must retain the real module; no feature-switch mock is permitted there.

The switch module was introduced at ca09d06f9 already carrying all three false values; its prior absence would be a collection error, not qualified behavioural RED. Record current guard execution honestly and defer its three mutation probes to fresh CHECK. Historical 9/9 behavioural RED for (a)–(f) remains valid. After the additional RoomTrafficEntry retarget, re-run the final sixteen test files individually and serially, plus typecheck, budget and preservation/discovery checks; save fresh logs without overwriting the earlier receipts.

## Additional accepted room/secret mounts and release-modal coverage

Source: team-lead follow-up during resume, 2026-10-14. Production gates are coming from the UI implementer; RED owns no production integration.

| New case | Independent expectation and positive instrument control | Exact fresh CHECK mutant |
|---|---|---|
| S1-a traffic plain request / virtualized request / streaming answer / completed answer | Real app main chat receives room_ref on generated user_message or token/done frames. Store retains that exact reference and ordinary traffic renders, but there is no room-traffic entry/toggle or room link while shipped switches are false. | Set ROOMS_ENABLED=true; narrower probe: remove the ROOMS_ENABLED check at the relevant RoomTrafficMessage mount in ChatScreen |
| S1-b live / snapshot secret | Real app receives either secret_request or session_state.secret_requests; exact pending request is recorded in the real secret store, while no secret card/thread tail/password control renders and no secret endpoint is called. | Set AGENT_PRIVATE_SECRETS_ENABLED=true; narrower probe: remove its check at SecretRequestThreadTail's ChatScreen mount |
| S1-q three approvals | Parent, child without root_session_id, and another-workspace request all enter the real queue. Exactly one modal presents them in arrival order; approve/deny/approve send the exact three POST decisions and leave route/session/workspace unchanged. | Remove AppShell's release modal mount; narrower probe: filter visible approvals by active session, hiding child/other-workspace entries |
| S1-exp expiry | A live request remains actionable before its expires_in_ms deadline, is not actionable at/after expiry, and never posts an automatic decision. Gateway denied_timeout removes it; the next queued request becomes actionable. | Remove AppShell's modal mount; narrower probe: force ToolApprovalCard.hasExpired=false |
| S1-exp zero | Contract-minimum expires_in_ms=0 is non-actionable immediately; dismissing this already-expired notice removes it without a decision POST. Dismissal is a release-UI characterization; non-actionability/no automatic submission is the safety oracle. | Remove AppShell's modal mount; narrower probe: force ToolApprovalCard.hasExpired=false |

Expiry safety derives from the requested release approval flow, ADR-092 D4's no-auto-submit/timeout denial, and the generating ToolApprovalResolvedFrame contract, which explicitly requires dropping the matching queue entry on denied_timeout. Test time is controlled at the clock edge; no sleep/elapsed-time assertion or increased timeout. Rendered expiry copy is not an oracle.

Existing mount discovery found one additional gated carrier pack: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/chat/RoomTrafficEntry.test.tsx renders through real ChatThread in six cases. Retarget that file with ROOMS_ENABLED mocked ON, retain every original oracle, and add visibility to its shared collapsed-toggle control. SecretRequestCard behaviour/security/state packs mount the card/tail directly, not via ChatScreen/ChatThread; do not add unnecessary switch mocks there. Existing ToolApprovalModal-named files render the inline panel and do not establish shipped-modal coverage.

## RED / CHECK boundary

RED proves a failure only on an actual historical pre-change implementation, with an expected-versus-actual behavioural failure. A current-head pass does not establish RED. No production mutations will be performed by this writer. GREEN verification, mutation execution and the skill's Proof-of-failability checklist are deferred to a fresh CHECK instance. The ledger will distinguish observed results, qualified RED evidence, and unverified claims.

## Accepted production integration and completion record

Team-lead required merging UI head e4c84116b36637810c59961e90def0f83dee0370 from its work branch. The tests-only follow-up was preserved first as ec17f7f4e9627a9f70357d31a279ab318aacece6. The conflict-free merge produced tested commit 0da2c2a52d17da9e7ce04c064bff1aa03115171c. Production was inherited unchanged, not edited by QA.

The executed pack is now 14 existing files plus the real-shell file and guard file: 16 serial file runs, 290 collected / 290 successful, all raw exits=0. New cases are 18 behaviours plus 3 constant guards. All 18 behaviours have historical RED; the six added room/secret cases also fail immediately before e4c84116b. Guard mutation proof remains deferred. The full ledger and source-bound receipts are at /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-retarget-ledger.md and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/l1-room-ui/stage1-execution-receipt.json.

## Deliberate gaps

No backend endpoint reachability/security claims, live-browser layout or visual claims, approval timeout/rule-editor behaviour, broader rooms/private-secret functionality, complete frontend suite, Go suite, user-facing documentation changes or independent CHECK verdict. Those are outside this test-authoring dispatch. The preserved legacy tests retain their own error/boundary cases.

Execution logs: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/ . These are local evidence artifacts, not production edits.
