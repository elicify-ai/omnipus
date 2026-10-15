# L1 SPA RED pack — lane l1-ui-red-u23

Base: d701bcbd05bb64ce004f076f03ebf29b01675441. Unit-2 comparison: 22c9e7b70.

## Oracle freeze (before writing assertions)

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/docs/internal/specs/omnipus-system-agent-spec.md.
Binding approval amendment: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/L1-AMENDMENTS-20261013.md §1.2 R6, §1.5. B3 is excluded by dispatch.

Behavior: a person can submit a destination-bound secret once through authenticated REST without putting the value in shared state, and can deliberately decide an inline approval only in its root chat. Private-variable channel access defaults off and must reflect confirmed server state.

Real boundary: React components, Zustand stores, session/workspace selection, API transport/validators, real singleton QueryClient and its mutation cache, auth/logout. Doubles only for fetch and WebSocket; console spies observe the output boundary without replacing internal helpers. Router behavior uses a real in-memory router if navigation is exercised. Do not read Unit-2 implementer tests or function bodies. Only exported signatures, component paths, local entry shape and store API were extracted from the four explicitly permitted production files.

## Cases and independent expected results

| Criterion | Input / sequence | Expected result | Source |
|---|---|---|---|
| U2-C01 | Provider/private/Slack request | Acting name, purpose, exact server recipient, owner pair for private; password inputs | FR-061; §6.8; BDD-6.1 |
| U2-C02 | Type then submit sentinel | Exactly one authenticated submit with exact fields; no credential/password endpoint; value absent from all observed store/cache emissions, other requests, socket sends, console output | FR-062, FR-075, FR-083; BDD-6.2/6.15 |
| U2-C03 | Missing/short/NUL/oversize UTF-8 values; 0/1/2/7/8/9 fields; serialized body boundary | Invalid values/card/body never sent; valid boundary values sent unmodified; associated/announced validation | FR-071, §6.2; BDD-6.9/12.9 |
| U2-C04 | While submit is outstanding, repeated click/Enter | Submit disabled; one POST, never an automatic replay | FR-074/075; BDD-6.4/6.10 |
| U2-C05 | Submitted/cancelled/stopped/interrupted/refused or unmount | Detached controls cleared, no replay, terminal announced; submitted names only | FR-074/075; BDD-6.5 |
| U2-C06 | Lost response/frame; empty snapshot; value-free state says pending/submitted/terminal/404 | Unknown/checking or unavailable, never infer Saved from absence; GET only; references shown for confirmed submitted | FR-079; §6.3; BDD-6.7 |
| U2-C07 | Two labelled fields, Enter in first then last | First initially focused; first Enter never submits; last submits; no composer key event; terminal returns focus | FR-109; §6.3; BDD-12.9 |
| U2-C08 | Private names list for agent, multiple workspaces, integration and other-agent controls | Only private entries for selected agent, each owner pair and version/state metadata; no values; default channel availability off | K6; FR-241; §6.3; US-7 |
| U2-C09 | Channel toggle confirmed / lost PATCH response | Exact K49 body; confirmed state only; uncertainty visible, no optimistic Saved or auto replay | K49; FR-241; §6.3 |
| U2-C10 | List pending / empty / error / stale refresh | Distinguish loading, true empty, Error + Retry, cached + stale hint | §6.3 private-secrets row |
| U3-C01 | Acting helper, root chat, another workspace active | Inline only in root (fallback session when root omitted); no dialog; queue preserved outside root | amendment §1.2 R6; FR-092; BDD-11.2/11.3 |
| U3-C02 | Peer/room received turn | Header acting agent + exact origin label; room root receiver; Open run goes to acting session | K7; amendment R2/R6; FR-251 |
| U3-C03 | Composer focused as approval appears; Enter and Escape | Focus stays; neither key resolves; composer stays unlocked | amendment R6 safety floor; §1.5 |
| U3-C04 | Explicit allow_once/deny/allow/cancel; bash exact/prefix | Existing actions/previews/scopes preserved, only approval-id keyed authenticated POST resolves | amendment R6; ADR-092 D4 |
| U3-C05 | Timeout and multi-tab resolution; duplicate interactions | No grace timer or auto-approval; server resolution clears; no double resolution/resurrection | amendment R6; K7 resolved frame |
| U3-C06 | Other chat in same workspace / no workspace / other workspace | Waiting-elsewhere banner points to root chat; no focus steal; disappears on resolution | amendment R6; BDD-11.3 |
| U3-C07 | Eight existing legacy test files | Replace modal-only focus/Escape/workspace assumptions; preserve all other assertions, all stub fixtures and resolution security checks | team-lead steer; amendment §1.5 (B3 replacement deferred) |

Bounds are specification bytes, not UTF-16 code units. Values: 3/4/5 and 16,383/16,384/16,385 bytes; multibyte counterexample; NUL. Fields: 0/1/2 and 7/8/9. Body: exact JSON size 65,535/65,536/65,537 bytes using ASCII fixtures and counted envelope bytes. No application validation helper supplies expected values.

## Public path assumptions

Unit 2 (confirmed signatures at 22c9e7b70): SecretRequestCard({entry}) and SecretRequestThreadTail from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/chat/SecretRequestCard.tsx; AgentPrivateSecrets({agentId}) from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/agents/AgentPrivateSecrets.tsx; useSecretRequestStore from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/store/secretRequests.ts; submitSecretRequest/getSecretRequest/listSecrets/updateSecretOptions from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/lib/api/secrets.ts.

Unit 3 public surface confirmed by the UI owner's team-lead (2026-10-14): ToolApprovalPanel and ToolApprovalThreadTail, no required props, from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/agents/ToolApprovalPanel.tsx; approvalChatId from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/store/toolApproval.ts; rootSessionId/actingAgentName/originLabel/roomRef on local entries. Existing CrossWorkspaceApprovalBanner path/name/testid retained. The new surface is absent at the base and produces explicit BLOCKED assertions, never skips or a silently substituted modal. The independent oracles did not change when these names were confirmed.

## What would break these (deferred to independent CHECK)

| Mutant | Critical detector |
|---|---|
| Put a typed value into the secret store or mutation variables | Capture every store and QueryClient emission while typing/submitting |
| Replay after lost response or CSRF recovery | Count POST attempts; exact subsequent GET-only request list |
| Validate code units rather than UTF-8 bytes | Multibyte limit cases |
| Keep control values on disposal/terminal | Retain detached input handles, assert empty values |
| Treat omitted snapshot request as submitted | Pending/404 recovery cases forbid Saved |
| Auto-focus Deny / bind composer Enter or Escape | Focus/key tests retain pending approval and zero POSTs |
| Use acting session or workspace instead of root | Same-workspace other-chat + Omnipus cross-workspace control |
| Weaken deny/scope/resolved-id protection | Preserved legacy assertions plus direct HTTP body checks |

## Known gaps / browser and UAT rows

DOM tests do not establish visual docking, responsive/touch layout, screen-reader speech, browser password-manager behavior, real browser session cookie isolation, backend durable store/audit/model-request secrecy, root-origin ancestry classification, unattended backend refusal, or raw credential-route reauthentication. These remain browser/UAT or backend rows. Browser reload snapshot completeness and stub deletion are B3, explicitly not changed here. The secret-card loading text without a request is only testable through its real mounting workflow; no guessed preparing phase is invented.

CHECK must be a different qa-lead instance. Seeing GREEN, mutation probes (three or more), and the Proof-of-failability checklist are **deferred to CHECK**. Comparison results at 22c9e7b70 are observations, not CHECK or feature acceptance. Only one test file per local process; no full local suite.
