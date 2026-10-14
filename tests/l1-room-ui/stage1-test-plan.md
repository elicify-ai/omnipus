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

## RED / CHECK boundary

RED proves a failure only on an actual historical pre-change implementation, with an expected-versus-actual behavioural failure. A current-head pass does not establish RED. No production mutations will be performed by this writer. GREEN verification, mutation execution and the skill's Proof-of-failability checklist are deferred to a fresh CHECK instance. The ledger will distinguish observed results, qualified RED evidence, and unverified claims.

## Deliberate gaps

No backend endpoint reachability/security claims, live-browser layout or visual claims, approval timeout/rule-editor behaviour, broader rooms/private-secret functionality, complete frontend suite, Go suite, user-facing documentation changes or independent CHECK verdict. Those are outside this test-authoring dispatch. The preserved legacy tests retain their own error/boundary cases.

Execution logs: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/ . These are local evidence artifacts, not production edits.
