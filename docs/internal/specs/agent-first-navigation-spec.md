# Feature Specification: Agent-first navigation and agent identity

**Created**: 2026-10-07 (UTC)
**Status**: In review — sole correction in progress; contract/visual dependencies remain. Not implementation approval.
**Size**: Feature. One frontend specification; backend U1 and this frontend land jointly.
**Input**: Corrected **Agent-first navigation and agent identity**, D1–D14, at `b6fd39efb5bc4ee5bb77cb6f16be5baeec34265e`, with the newer founder answers below.
**Worktree**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav`
**Branch**: `work/adr-frontend-navigation-20261007`

## Authority and scope

A person chooses a colleague within a workspace, resumes the exact last chat, and starts extras deliberately. The open chat owns its Activity panel; the existing Sessions modal is the cross-session overview. P0: correct destination and saved-history continuation; P1: identity, attention and inspectable activity.

**Correction — 2026-10-08, sole correction round:** the prior draft left selection/acknowledgement, moving pages, live keyboard targeting and phase selection underspecified, and faded identity ink below its contrast promise. The corrected contract below and S's answers control; no production or contract file is changed.

### Sources and precedence

Newest founder answers override older design/prototype wording. Keys below are reusable citations; N/B cite the decision by title. Runtime facts come from current code; publication is not implementation.

| Key | Source / immutable baseline |
|---|---|
| S | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/FOUNDER-ANSWERS-SPEC-20261008.md` — Q-M4–Q-M6, Q-G1–Q-G4 and approved CUT C01–C14. |
| G | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/agent-first-navigation-spec-review.md` — sole review of `fbcb417a948d1473310b8ccd6b5f92269dac2a11`; six MAJOR/two MINOR findings. |
| N | **Agent-first navigation and agent identity**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-20261007-agent-first-navigation-and-agent-identity.md`, D1–D14 at `b6fd39efb5bc4ee5bb77cb6f16be5baeec34265e`. |
| F | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/FOUNDER-ANSWERS-20261007.md` — entry, Q9 FINAL, activity scope/origin, always-nested helpers and folding. |
| R | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/requirements.md` — R1–R47 plus subrequirements; dispositions below. |
| M | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/session-modal-review/review.md` — A1–A7/D1–D8, overridden by F/S where answered. |
| W | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/index.html` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/` — approved visual reference, not product acceptance. |
| B | **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, D1.1 at `b76b412f61ff73d9a6643d34518224472d84223a`; read-only source copy `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/spec-evidence/backend-adr-b76.md`. |
| BS | Backend spec at `0e1fececc5df91c140e21fd27cf03bfd0ee3c8dc`; read-only source copy `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/spec-evidence/backend-spec-published.md`. B's commit supplies the ADR, not this later spec. |
| K | Tasks-panel branch `work/tasks-panel-layout-20261007`, observed source tip `7946ac5f00163fa036033e75f329fcab2193bdaa`: reuse FilterMenu/ViewSwitch/HoverCard; final publication remains DEP-KIT. |
| P | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/CLAUDE.md` — hard constraints, reachability, documentation, contracts and feature delivery gates. |

### Commission boundary

| In this squad | Outside / companion |
|---|---|
| Agent-row main/extra/history navigation, exact entry, freshness and attention | New chat store/address book, transcript-heavy navigation or new policy grants. |
| Shared four-figure/role/palette identity, existing editor preview and canonical migration | Uploads/GIFs; later bounded-image unit retains R17 safety requirements. No new creation interview or full roster/Team redesign. |
| Above-feed kind, name-only messages, inline responder and immediate command cleanup | Full workspace-qualified @ messaging and complete backend safe-point Clear; no old switching aliases retained meanwhile. |
| Starting-chat plans/start links, local panel, Sessions status/filters/strict hierarchy/folding and origin-row shell counts | New dashboard/executor/scheduler, cross-workspace plans or authority from merely viewing independent runs. |
| Minimum shared kit jobs and existing shell behavior | Broader Agents/Connectors/Skills/settings rollout; R44 header/R45 Tooltip and #1221 per-agent Auto removal stay companion-owned. |

Backend U1, generated contracts, canonical identity and activity coverage land **jointly** with frontend on one tested candidate after founder approval. Independent kit preparation may proceed; unresolved DEP/WF holds never authorize guessed consumers.

**Process exception:** Q13=A commissions exactly one SPEC grill and this one correction; no second grill. S Q-G4 permits success coverage for positive capabilities and error/edge **plus meaningful recovery** for negative/edge criteria. Keep the old strict exit-1 receipt; no relabelling or duplicate filler. This is document correction, not implementation approval.

## Reachability

Check invocation before correctness gates. All entries below require the **real joint candidate**; no new builtin/policy grant is proposed.
| Actor / entry | Destination / proof | Tests |
|---|---|---|
| Person: workspace disclosure / agent row | Validated pair's main and actual send destination | T-01/T-12/T-23 |
| Workspace name, login/cold entry, modal workspace switch | Exact remembered chat or validated Ava welcome main | T-02/T-12/T-23 |
| Past sessions | One modal with removable workspace + agent filters | T-13/T-23 |
| + New chat | Deliberate extra; original delivery safeguard; main intact | T-03/T-13/T-23 |
| Magnifier / /sessions | Overall metadata, filters and real nested helper targets | T-18/T-19/T-26 |
| Agents/New agent or Team/edit | Existing global editor/preview; saved identity and locks | T-16/T-22/T-25 |
| Start-result link / plan pill | Actual plan Tasks/Graph or actual task/helper session, live/replay | T-20/T-27 |
| Open-session activity / origin-row shell Open | That chat's existing Activity panel only | T-21/T-26/T-27 |
| Default-workspace Admin | Real main/send before obsolete entry removal | T-23 |
| Agent: existing plan/task/question tools | Authorized actual results, scheduler and attention sources | T-24/T-27 |

## Available Reference Patterns

Use P's contract/kit rules and the existing seams below; no generic infrastructure is added. The optional `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/reference/go-implementation/00-overview.md` is absent; reference-directory control found `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/reference/built-in-tools.md`.

## Existing Codebase Context

GitNexus MCP unavailable; this checkout is **not indexed**. Source/caller-derived impact is **Inferred**, not a graph verdict. Repeat impact analysis before implementation. Registry entries combine direct consumers and affected journeys; no production symbol is edited here.

| Key | Source::symbol — reuse/modify | Direct consumers / affected flow |
|---|---|---|
| C-NAV | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/layout/Sidebar.tsx`::Sidebar | Shell rows → validated selection; drawer/pin preserved. |
| C-SHELL | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/layout/AppShell.tsx`::AppShell; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useAgentsCrossTabRefresh.ts`::useAgentsCrossTabRefresh | Move sole picker-owned refresh to shell; roster/member focus/reconnect. |
| C-SELECT | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/useSelectSession.ts`::useSelectSession, attachAndSeed | Sidebar/modal → atomic selection; failed cross-workspace attach. |
| C-RESTORE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/session.ts`::enterWorkspaceChat, resolveRememberedSessionFromServer, attachToSession | Browser pointer/route → attach/replay; winning foreground/read boundary. |
| C-MODAL | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/search/SearchModal.tsx`::SearchModal, AgentSessionList, AgentHeader, SessionRow, handleSwitchWorkspace, bucketByAgent, sortSessions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/ui.ts`::useUiStore.openSearchModal | Search/filter/hierarchy; replace index activation and helper orphan-root fallback. |
| C-TREE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/sessions/SessionTree.tsx`::SessionTree, flattenSessionTree | Real descendants, folded presentation, virtual/plain keyboard reachability. |
| C-FEED | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ChatScreen.tsx`::ThinkingIndicator, InlineThinkingIndicator, AssistantMessage, VirtualAssistantMessageRow, AssistantMessageAvatar, OmnipusComposer | All feed paths/producer phases; preserve phrases, errors and composer controls. |
| C-EDITOR | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/agents/AgentFormFields.tsx`::AvatarHeader, AvatarColorPicker, IconPicker | Current wizard/profile → shared preview/global identity, not fixed Robot. |
| C-ACTIVITY | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useRunningActivity.ts`::useRunningActivity; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ActivityBar.tsx`::ActivityBar, ActivityPill; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ActivityPanel.tsx`::ActivityRow | Existing local projection, actual task/run/plan/shell attribution. |
| C-PLAN | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/workspaces/WorkspaceTasksTab.tsx`::handleSelectPlan | Existing Tasks/Graph selection; real starting-chat hand-off. |
| C-COMMAND | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useSlashMenu.ts`::useSlashMenu | Unified menu/registry/help cutover, no aliases. |
| C-WIRE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/Session.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/Agent.yaml`::properties | Generated consumers only; new main/attention/figure shapes remain DEP holds. |

Footprint spans shell/session/chat, identity/kit and task inspection; keep existing ownership/cache/attachment rather than another service. Paging seam: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/pkg/agent/loop_session.go`::ListAllSessions and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/lib/api/sessions.ts`::fetchSessions; mutable offset traversal alone cannot certify complete activity (DEP-ACT).

## User Stories & Acceptance Criteria

Acceptance IDs are stable `US-story.acceptance` keys. Their normative Given/When/Then outcomes live **once** in the linked BDD cases; the matrix links requirements/tests. Priorities are P0 (destination/history safety) and P1 (usable identity/activity).

### US-1 — Choose a colleague, not a recent thread (P0)
A person reaches the intended colleague's main. **Why:** avoid wrong-chat sends. **Independent test:** same agent, two workspaces, newer extras.
**Acceptance IDs:** US-1.1 → BDD-01.1; US-1.2 → BDD-01.2; US-1.3 → BDD-01.3; US-1.4 → BDD-01.4.

### US-2 — Return to the exact chat safely (P0)
A person resumes their own last chat or the validated welcome main. **Why:** no silent new work/recipient change. **Independent test:** main/extra pointers, reload, failure and late response.
**Acceptance IDs:** US-2.1 → BDD-02.1; US-2.2 → BDD-02.2; US-2.3 → BDD-02.3; US-2.4 → BDD-02.4.

### US-3 — Start extras deliberately and find history (P1)
A person keeps the main while using row actions. **Why:** parallel conversations without a session tree. **Independent test:** keyboard/touch actions and original first-delivery recovery.
**Acceptance IDs:** US-3.1 → BDD-03.1; US-3.2 → BDD-03.2; US-3.3 → BDD-03.3; US-3.4 → BDD-03.4.

### US-4 — Know when a main needs attention (P1)
A person finds real decisions/outcomes, not unrelated activity. **Why:** truthful attention. **Independent test:** four sources, negative sessions and observed-open races.
**Acceptance IDs:** US-4.1 → BDD-04.1; US-4.2 → BDD-04.2; US-4.3 → BDD-04.3; US-4.4 → BDD-04.4.

### US-5 — Recognize one consistent identity (P1)
A person recognizes the same figure/badge/color everywhere. **Why:** no workspace/responder drift. **Independent test:** every approved size/color and restart-stable migration.
**Acceptance IDs:** US-5.1 → BDD-05.1; US-5.2 → BDD-05.2; US-5.3 → BDD-05.3; US-5.4 → BDD-05.4.

### US-6 — Preview/edit through the existing flow (P1)
A person previews global edits in one editor. **Why:** no local-only identity. **Independent test:** custom save, protected editability and rejected activation.
**Acceptance IDs:** US-6.1 → BDD-06.1; US-6.2 → BDD-06.2; US-6.3 → BDD-06.3.

### US-7 — Read a truthful feed (P1)
A person sees the actual author/responder and kind. **Why:** displayed and sending identity agree. **Independent test:** live/replay/virtual/plain, real phase inputs and controls.
**Acceptance IDs:** US-7.1 → BDD-07.1; US-7.2 → BDD-07.2; US-7.3 → BDD-07.3; US-7.4 → BDD-07.4.

### US-8 — Understand sessions and hierarchy (P1)
A person inspects the existing overall view. **Why:** no lost/mislabelled helpers. **Independent test:** all lifecycle/kinds, strict parent context, accessible/protected actions.
**Acceptance IDs:** US-8.1 → BDD-08.1; US-8.2 → BDD-08.2; US-8.3 → BDD-08.3; US-8.4 → BDD-08.4.

### US-9 — Find current work without reopening (P1)
A person filters live metadata and follows folded helpers. **Why:** no false quiet overview. **Independent test:** unopened A from B, moving pages, stable activation and every original target.
**Acceptance IDs:** US-9.1 → BDD-09.1; US-9.2 → BDD-09.2; US-9.3 → BDD-09.3; US-9.4 → BDD-09.4; US-9.5 → BDD-09.5.

### US-10 — Follow plans and actual runs (P1)
A starter sees real state and drill-down. **Why:** accepted is not running. **Independent test:** real starts/live/replay links and no-origin plans.
**Acceptance IDs:** US-10.1 → BDD-10.1; US-10.2 → BDD-10.2; US-10.3 → BDD-10.3; US-10.4 → BDD-10.4.

### US-11 — Keep panel local, overview global (P1)
A person monitors work without gaining control. **Why:** preserve ownership. **Independent test:** A/B isolation, scheduler-only runs, shell count and task-child dedupe.
**Acceptance IDs:** US-11.1 → BDD-11.1; US-11.2 → BDD-11.2; US-11.3 → BDD-11.3.

### US-12 — Continue saved chats and reach Admin (P0)
An existing user retains legitimate history. **Why:** fresh-fixture demos cannot prove cutover. **Independent test:** supported saved install, real follow-up/restart and replacement Admin access.
**Acceptance IDs:** US-12.1 → BDD-12.1; US-12.2 → BDD-12.2; US-12.3 → BDD-12.3.

## Behavioral Contract

The normative contract is the single FR/decision/acceptance/BDD/test matrix below; error/recovery and boundary outcomes are in its linked BDD cases, not a second restatement.

## Edge Cases

EC-01–EC-12 trace respectively to BDD-E01–BDD-E12; each retains its independent action and boundary oracle, with DS fixtures in the test plan.

## Explicit Non-Behaviors & Safeguards

The system must not add a second navigation/activity/session store, synthetic parent edge, prose-derived target, tool-policy grant, demo/upload lane or screen-local copy of a kit job (N D1/D5/D7/D11–D14). Other prohibitions belong to FR-007/009/011/020/024/030/032/034/036; these references do not weaken them.

### Numeric and presentation oracles

| Oracle | Exact constraint | Source |
|---|---|---|
| Identity | Four figures; 31 roles/five groups; ten named colors, vocabulary below. Badge stays at every size, no circle/role fallback. | R10/R11/R16; N D6; F Q5 |
| Sizes | Sidebar 26 px icon/13 px name; Team 18; roster 40; inline responder 48. Other new list/editor sizes need WF approval. | R13; N D6 |
| Contrast / selection | Identity ink fully opaque and ≥3:1 on sidebar `#111113` and chat `#0A0A0B`, including dimmest motion frame; normal-color names, identity never recolored gold by selection. | R12/R16; S Q-G2 |
| Attention | Icon +18% warning-yellow `#EAB308` halo/1.6 s; collapsed 8 px right dot beside caret. No agent-row dot; distinct confirmed main-agent count. | N D4; R29 |
| Working | Scale 0.97–1.03, 1.6 s pulse/glow, 2.4 s sheen. | N D8/W |
| Thinking | Scale 0.96–1.01/2.6 s. **Decorative glow** opacity 0.55–1; figure/badge ink opacity stays 1. No parent opacity that fades the ink. | N D8 corrected by S Q-G2 |
| Waiting / motion accessibility | Peak scale 1.07/3.4 s. Reduced motion disables all loops; stable name/state text remains. Mount/state rules are in the indicator table, not inferred from animation. | R14/R15; N D8 |
| Labels | `Sessions`; `Main chat` / `Extra chat — title`; real task/helper kind. `Main chat needs your attention` unless authoritative reason supports more specific wording. | M A3/D7; N D3/D4 |
| Actions | Independent accessible names/targets, no nested buttons; adjacent coarse-pointer targets ≥44 px and non-overlapping. Focus/colors use kit rules, never color/motion-only meaning. | N D3/D7; P |
| Coverage | Active counts never inherit the eight-item recent-finish cap; failed/incomplete/unstable enumeration is unknown/partial, not false/zero/complete. | FR-011/032; DEP-ACT |

Use existing published error envelopes. No new latency/RAM/page/title budgets, error-body shape, backend phase property or credential logging is commissioned. No nominal wire types outside generated contracts. No metadata browsing, filtering or placeholder/summary expansion implies goal acknowledgement or control authority.

## Prerequisites
Existing authenticated gateway/browser, supported Linux/macOS/Windows and provider for real-agent acceptance; no new service/runtime requirement (P).

## Development Setup
Use P's existing build/test/SPA-embed instructions and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/getting-started.md`; no copied setup tutorial or local heavy gate. Establish the joint candidate before consumer tests.

## Tech Stack
P and committed `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/package.json` / `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/go.mod` own versions. Existing embedded SPA/single Go binary, generated boundaries and shared caches; no new runtime or datastore.

## Deployment / Runtime
U1/frontend/contracts/identity/K publish on one exact candidate. Existing operational lifecycle remains; no new command. Offline/stale metadata retains honest intent, not acknowledgements or cancellation. Backend migration/restart readiness requires real saved-history continuation and canonical identity proofs; no welcome-screen shortcut.
S Q-G3 adds **no downgrade/rollback promise, procedure, support, warning or extra documentation/test scope**. Existing upgrade behavior alone remains commissioned; interrupted conversion/retry is not redefined as post-success rollback.

## Integration Boundaries

Backend-lead publishes existing-schema extensions and generated types **before consumers**. No new wire-property/endpoint names are guessed here. Fixtures exercise real reducers/validators; joint rows require real gateway/runtime on one combined SHA. These are readiness holds, not claims of delivered APIs.

| Dependency / owner | Needed input → output / invariant | Failure / readiness proof |
|---|---|---|
| DEP-U1 — session-core | B D1.1: required current `Session.type=main`, validated `id/workspace_id/agent_id`, computed `protected`; readOnly `WorkspaceMemberConfig.main_session_id` for eligible mains. Main ID `main-session-<workspaceid>-<agentid>` is not client-created/UUID-assumed. Heartbeat keeps enabled/interval/body, deletes session_id. Immutable owner versus `Message.agent_id`; Admin default-main exception without fake membership. | Unknown/load refusal is not deletion; one winning tuple, no unresolved send. Publish generated main/Ava/Admin/hide/protection/migration shapes and prove T-12/T-22/T-23/T-28. |
| DEP-ATT — session-core + prompt owner | BS C-ATTENTION: optional/readOnly `Session.needs_attention`, true/false on every valid main including Admin, omitted elsewhere. Sources: pending structured question/approval or unseen met/rounds_exhausted/other; stopped_by_user excluded. Shared bounded goal-seen metadata; decisions clear on resolution only. Existing attach `ack_attention` optional/default false needs the supersession-safe observed-bound semantics below. Prompt owner authors structured-answer rule; backend wires it. | Missing main value/source remains unknown. No browser seen flag or per-user notification store. Publish agreed acknowledgement correlation/bound representation and unopened-main updates before consumers; T-12/T-14/T-24 combine selection and read races. |
| DEP-ID — backend identity | Existing Agent list/create/update/editability/persistence → figure/role/color, approved canonical inventory, one-time mapping. Fix compiled built-ins, fresh seed and startup enforcement together; use shared global editor/preview. No separate figure wire representation was verified at baseline. | Preview != saved/activated; no local shadow/recoloring/unlock. Publish representation before persistence consumers; real custom/built-in migration, two restarts and lock proofs, T-16/T-22/T-25/T-28. |
| DEP-ACT — backend task/plan/session owners | Existing plan state/phase/progress, actual start/run/session/workspace handles, real starting-chat association, task/scheduler/child/shell metadata → local panel and consistent Sessions enumeration. Tasks-only plans have no chat origin. No prose URLs, fake parent, foreground target substitute or agent-facing list_jobs use. Shell row: **N background commands running**, Open → origin chat's Activity panel (S Q-M4). Unavailable parent context: **parent chat unavailable**, authorized relation only, helper still Openable (S Q-M5). Needs me = mains with confirmed needs_attention only (S Q-M6). | Unattributed/global verdict, missing origin/handle/page/source is unknown/error. Task-child duplicates count once; independent monitoring is not Stop authority. Publish actual execution-vs-queued, shell origin, consistent enumeration and snapshot/live coverage; T-19/T-20/T-21/T-26/T-27. |
| DEP-KIT — frontend/Tasks owner | One data-supplied AgentIcon; no fetching/resolution inside. Reuse FilterMenu/ViewSwitch and shadcn HoverCard, kit Dialog/disclosure/Badge/Item/SearchField; catalog/barrel/style/manifest/story/executed static evidence, layout-only call-site classes. | No local substitute. K tip verified first two, not HoverCard; final exports/publication/check-mark conformity remain owner checks. All eleven manifest kinds and real in-context/contrast evidence, T-15/T-18/T-25. |
| DEP-CUT — frontend/session-core + companions | Coordinate generated producer/owner/type/command/handover deletions. Preserve first-send, replay/order/errors, model/Auto/attachments/Stop. #1221 removes per-agent Auto; R44/R45 remain separate. | No obsolete field/alias/control resurrected. Shared cache/attachment path, no row sockets/pollers/full transcripts; T-17/T-23/T-29/T-30. |
| DEP-UX — frontend/team-lead | F/S settle product choices; WF-01–WF-05 still need approved missing visual states, including corrected Thinking glow and input-derived indicator. | Existing screenshots/demo controls are not new-screen approval. No open answered question, new brand decision or invented size. |

### Foreground commitment and bounded read (MAJ-001; S Q-G1)

1. Selection, workspace/login/cold restore first validates/attaches **without acknowledgement**. Transport-send success or server attachment alone does not establish a shown view.
2. Commit only the winning intent's coherent workspace/session/immutable owner after validated catch-up/view data is ready and the main is actually shown as foreground. Prefetch, hidden/background reconnect and overtaken A are not opens.
3. Only that committed view may acknowledge its **observed saved-outcome bound**, through the agreed existing operation. Bind read intent to that open, not merely a reused session ID. The backend must honor the captured bound, never recapture newer outcomes on delayed/retried acknowledgement. A newer outcome not shown remains unseen; unresolved questions/approvals are untouched.
4. For A→B and A→B→A, late first-A success/failure cannot acknowledge or replace anything; a later winning A open has its own bound. Existing boolean alone cannot prove this: DEP-ATT's published sequence/correlation/bound agreement is mandatory. No second read service.

### Complete overview boundary (MAJ-002)

DEP-ACT must provide **stable authorized enumeration at one snapshot/cut**, or an explicit incomplete result until reconciliation establishes that coverage. Complete requires all pages from that cut plus reconciliation of subsequent authorized updates; cursor exhaustion, dedupe, all-success HTTP or cached buckets alone proves nothing. No all-idle/zero-running claim while consistency is unknown. Reuse existing listing/snapshot/live machinery and coalesced invalidation, metadata only. T-19/T-26 force `s1,s2 | s3,s4`, then s4 moves to the prefix: later page `s2,s3` succeeds/end-cursor, but s4 must eventually appear without an intervening complete/zero claim.

### Indicator input mapping (MAJ-005; precedence top to bottom)

Use session/turn-correlated published snapshots/frames, current tool start/result and pending-card/approval records. Actual producer of this reply slot wins over immutable owner; owner is only idle-chat identity, never a guest fallback. Missing correlation/phase input is DEP-ACT, not a new phase field or global/scalar goal heuristic. Stable accessible state text does not announce decorative phrase rotation.
| Authoritative input / precedence | Phase / phrase | Mounted / recovery |
|---|---|---|
| Failed/unreconciled snapshot, disconnect or ambiguous producer | Static **Unavailable/reconnecting**; known cached identity marked stale, otherwise Unknown agent | Keep visible status/recovery; no guessed working animation. Reconcile snapshot before choosing phase. |
| This session has unresolved question card or tool approval | **Waiting**; waiting for your input / approval, from actual reason; both → generic waiting for your input | Remains mounted even with no active/running message; use pending record's producer, not owner. Resolution selects the next matching row. |
| Correlated active turn has a genuinely executing tool call | **Working**; stable existing tool-specific label where accurate, otherwise working | Mounted for actual producer until result/turn transition; pending decision overrides it. |
| Correlated active turn awaiting/generating model response, no executing tool or pending decision | **Thinking**; appropriate existing phrases | Mounted for actual producer; tokens are not proof a different chat is running. |
| Only queued accepted work, no active turn/decision | Static **Queued**, not executing/needs input | Show meaningful text, no executing/waiting pulse; actual queued producer only when supplied. |
| Terminal done/stopped/failed/interrupted receipt for this turn, no pending decision | No active phase; retained result/error stays truthful | Remove that turn's indicator, not its outcome/error. Never inherit its phase into the next turn. |
| Resolved idle chat with no active reply/queued work/decision | Static **Idle** with known owner identity | Idle presentation only, not a new active-turn claim; no duplicate indicator appended to a terminal reply. |

Pending snapshot status/session/agent, `active_turn` producer and real tool execution inputs must agree. Concurrent uncorrelated producers remain honest Unknown rather than arbitrarily selecting one. T-17/T-25 feed these inputs, including overlapping active-turn + pending approval, pending question after turn end, queue, terminal, guest and reconnect—not preclassified phase props.

## Wireframe reference and additions needed

Use **W only**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/index.html`: sidebar/ws-list for rows/cues, chat-label for kind, chat-feed/think-preview for names-only inline responder; approved Q9 screenshots under W's evidence directory. Screenshots: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/01-inline-thinking.png`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/02-name-only-bubbles.png`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/03-addressed-jim-answering.png`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/04-extra-chat-toggle.png`. Demo controls/app-wide figure switch never ship. S Q-G2 overrides the old ink fade; numeric motion/size oracles live in Safeguards.

### Wireframe additions needed
Team-lead obtains approval in that reference project; this task edits no wireframe. Current modal screenshots are not redesigned-screen approval.
| ID | Missing state / required content |
|---|---|
| WF-01 | Desktop/phone Sessions: M A1–A7/D1–D7, main-first/strict nested hierarchy, main-only Needs me, parent chat unavailable, title/meta/status/attention/plan, shared groups/search/filters/counts, stable keyboard identity through live reorder/removal and coarse targets. |
| WF-02 | Starting-chat plan pill/results/local panel: approved/running/paused/failure, independent Open, real Tasks/Graph/run links and no-origin Tasks plan. |
| WF-03 | Existing unified editor: per-agent figure/role/color choices and matching preview, global save/error/locks. No new creation interview. |
| WF-04 | Entry/loading/stale/unknown/failed attach, missing welcome main, committed shown-view read boundary, drawer/zoom/reduced motion and input-derived inline state with corrected decorative Thinking glow. |
| WF-05 | Origin-row **N background commands running** and Activity Open; folded **N similar helper runs** and expanded original targets/statuses/counts under parent, every filter/search/virtual/plain state. |

### Locked identity vocabulary
Figures: **Robot, Man, Woman, Omnipus**, default Omnipus; approved minimal eyes, transparent figure + role badge at every named size (F Q5/N D6).
| Group | Exact R11 roles |
|---|---|
| Create (6) | Writer; Designer; Image creator; Video producer; Audio and voice; Social media |
| Build (8) | Developer; Data engineer; Data analyst; IT and operations; Automation; Security; Quality and QA; Science and lab |
| Business (10) | Orchestrator; Project manager; Product manager; Sales; Marketing; Finance; Legal and compliance; Customer support; Documents; Researcher |
| People (4) | People and HR; Tutor; Knowledge and library; Translator |
| Personal (3) | General assistant; Personal assistant; Office assistant |

| Ordered palette | Hex |
|---|---|
| 1 Azure | `#3B82F6` |
| 2 Sky | `#38BDF8` |
| 3 Cyan | `#22D3EE` |
| 4 Indigo | `#818CF8` |
| 5 Violet | `#A78BFA` |
| 6 Purple | `#C084FC` |
| 7 Fuchsia | `#E879F9` |
| 8 Pink | `#F472B6` |
| 9 Orange | `#FB923C` |
| 10 Grey | `#9CA3AF` |

**Mapping (F Q8/N D10):** exactly `#` + six hex digits; invalid/missing → Grey. RGB saturation **<0.25** → Grey, else closest non-Grey hue by shorter circular distance. Equal distances retain the first published palette entry (strict improvement). Persist once, not per render. DS-I freezes independent threshold/tie/wrap expectations.
**Role migration:** Code → Developer; Chat → General assistant; MagnifyingGlass → Researcher; PencilSimple → Writer; Shield → Security; unmatched → General assistant; valid curated roles unchanged. Backend canonical seeds must agree.

## BDD Scenarios

BDD is behavior-driven development. Each row is a scenario with one **When** action; `Traces to: US-x.y` means User Story x, Acceptance Scenario y. Categories stay unchanged. Outline examples are independent cases. Shared invariant references point to the single FR/contract home, not implementation output. Recovery rows below extend existing IDs, not the 58-ID inventory.

### Stable navigation / attention
| Scenario | Category | Traces to: | Given | When | Then / And |
|---|---|---|---|---|---|
| BDD-01.1 Main despite newer extra | Happy Path | US-1.1 | Mia's distinct mains/newer extras in Product launch/Operations | Select Product launch/Mia | Its main/owner row opens; next message never goes to extra/Operations. |
| BDD-01.2 Eligibility outline | Happy Path | US-1.2 | Expanded authoritative agent/eligibility below | Render navigation | Presence matches example; no worker/hidden/main-ID guess. |
| BDD-01.3 Existing panel navigation | Alternate Path | US-1.3 | Workspace chat open | Activate Team | Existing desktop panel/open state; no Chat/duplicate Library/replaced Agents destination. |
| BDD-01.4 Refresh failure | Error Path | US-1.4 | Roster/member request fails; no cache or prior cache | Expand workspace | Error/Retry or marked stale cache; never empty-team success/fake main. |
| BDD-02.1 Exact restore outline | Happy Path | US-2.1 | Browser pointer valid/visible; other chat newer | Enter Operations through example | Exact conversation/immutable owner returns; committed shown main uses bounded read, not initial attach. |
| BDD-02.2 Welcome outline | Happy Path | US-2.2 | Saved/welcome states below | Enter workspace | Validated Ava main or unavailable/Team/Retry/no-send; zero extras. |
| BDD-02.3 Failed entry | Error Path | US-2.3 | A committed; B validation/load/attach fails | Settle B attempt | A tuple stays coherent, B intent retryable/no send; no false switch/fallback/goal acknowledgement. |
| BDD-02.4 Late selection success | Edge Case | US-2.4 | A unseen goal, delayed A resolution/attach, B wins before A shown | Deliver A success | B/pointers remain, A goal remains unseen for everyone; transport/server success is not foreground commit. |
| BDD-03.1 Deliberate extra | Happy Path | US-3.1 | Selected pair, possibly unconfirmed first delivery | Activate + New chat | Existing abandonment guard; main intact, retries use original delivery identity. |
| BDD-03.2 Pair-filtered history | Happy Path | US-3.2 | Older Product launch/Mia sessions among pairs | Activate Past sessions | One modal, visible removable pair filters; broadening reveals authorized results. |
| BDD-03.3 Return to main/actions | Alternate Path | US-3.3 | Mia extra open/owner selected | Activate row by keyboard | Main opens; Past sessions then New chat are independent named hover/focus/touch targets. |
| BDD-03.4 Refused history attach | Error Path | US-3.4 | Target inaccessible/disconnected | Select result | Visible refusal/recovery, no successful switch/closed interaction/lost committed chat. |
| BDD-04.1 Attention-source outline | Happy Path | US-4.1 | Unopened main, source below | Reconcile attention | Exact source signal; no extra/helper/prose/global source inference. |
| BDD-04.2 Shared observed open | Alternate Path | US-4.2 | Two people, unseen goal plus pending question/approval | Commit the main's shown foreground open | Observed-bound goal clears for both; decisions/newer outcome remain. Navigation/login/cold restore count only at that commit. |
| BDD-04.3 Reduced-motion attention | Edge Case | US-4.3 | Two distinct mains need attention, reduced motion | Render collapsed workspace | Static 8 px warning cue/text/count two; expanded icon cue, zero loops/row dots. |
| BDD-04.4 Missing attention | Error Path | US-4.4 | Main value missing/source failed | Render snapshot | Unknown/unavailable/Retry, never false/zero; no read acknowledgement. |

**Examples — BDD-01.2**
| Agent / eligibility | Presence |
|---|---|
| Mia / eligible member | Present |
| Native worker / worker member | Absent |
| External worker / worker member | Absent |
| Judge or Plan Supervisor / hidden engine | Absent |
| Admin / validated default main | Present only in default workspace |

**Examples — BDD-02.1**
| Kind | Entry |
|---|---|
| Main | Workspace name |
| Extra | Workspace name after cold reload |
| Extra | Login |
| Main | Modal workspace switch |

**Examples — BDD-02.2**
| Saved state | Welcome | Outcome |
|---|---|---|
| No real pointer | Validated Ava main | Open Ava main |
| Confirmed deleted/hidden/inaccessible | Validated Ava main | Open Ava; don't reuse invalid target |
| No real pointer | No eligible/accessible welcome | Unavailable/Team/Retry/no send |

**Examples — BDD-04.1**
| Source | Signal |
|---|---|
| Pending question card | On |
| Pending approval | On |
| Unseen finished goal (met) | On |
| Unseen failed goal (rounds_exhausted/other) | On |
| User-stopped goal | Off |
| Ordinary unread/free-text question | Off; answers require structured tool |
| Extra/helper-only source | Off for main |
| Generic task notice/global plan verdict | Off unless actual allowed goal belongs to main |

### Identity / feed / Sessions
| Scenario | Category | Traces to: | Given | When | Then / And |
|---|---|---|---|---|---|
| BDD-05.1 Figure outline | Happy Path | US-5.1 | Example figure + Developer/Azure | Render named surface | Same transparent figure/badge/color before normal name; badge remains small. |
| BDD-05.2 Readable choices | Happy Path | US-5.2 | Chooser and named backgrounds | Open choices | Exact vocabulary/oracles; all-state ink ≥3:1, no brand/semantic colors. |
| BDD-05.3 Stable migration | Alternate Path | US-5.3 | Fresh/custom/built-in legacy identities | Restart upgraded gateway repeatedly | Approved saved/rendered grammar and field locks persist. |
| BDD-05.4 Identity failure | Error Path | US-5.4 | Lookup failed, cache or absent | Render identity | Honest cached/unknown, not removed/newly saved. |
| BDD-06.1 Existing editor preview | Happy Path | US-6.1 | Custom agent/current unified editor | Change identity choice | Shared matching preview/global create-autosave; no second editor/image/demo preference. |
| BDD-06.2 Protected editability | Alternate Path | US-6.2 | Built-in/server field rules | Open editor | Fixed identity locked, other allowed fields unchanged; no capability gain. |
| BDD-06.3 Save failure | Error Path | US-6.3 | Draft save/create/activation fails | Settle operation | Visible real status, not saved everywhere. |
| BDD-07.1 Name/kind outline | Happy Path | US-7.1 | Mia owner/Jim guest, kind/path below | Display feed | Both real names/no avatars, truthful above-feed kind/owner. |
| BDD-07.2 Input-derived responder outline | Happy Path | US-7.2 | Session/turn/producer/tool/card/approval inputs below | Reconcile/render next reply slot | Indicator table derives phase/mount/name; motion oracles and opaque ink, no composer line/logo/owner switch. |
| BDD-07.3 Command cleanup | Alternate Path | US-7.3 | Joint cutover | Open command/composer choices | /sessions only; old commands/picker/@ switching absent; model/Auto/attachments/send/Stop intact; Clear no new chat/history deletion. |
| BDD-07.4 Selected controls/errors | Error Path | US-7.4 | A helpers/error; B open | Activate B's Stop | Truthful success/refusal/scope, not control of A; navigation never cancelled A, real error not lost in decorative phrases. Recovery action is a separate case below. |
| BDD-08.1 Status/kind outline | Happy Path | US-8.1 | Permitted status/kind below | Display row | Title then muted status/kind/active/tokens; confirmed main dot, no HB/fabricated values. |
| BDD-08.2 Strict hierarchy | Happy Path | US-8.2 | Main M, extras E1/E2, C under E1, O missing parent | Reveal group | M pinned/extras recency; C under E1 even child-only match; O under **parent chat unavailable**, still Openable, no hidden/fake parent title/session. |
| BDD-08.3 Shared accessible actions | Alternate Path | US-8.3 | Desktop/phone, keyboard/coarse input | Focus row action | Sessions/subtitle/shared kit/actions legible; trap/restore focus and rename Escape safety; no new handmade tooltip. Stable activation cases below. |
| BDD-08.4 Protection/refusal | Error Path | US-8.4 | Protected main or inaccessible/failed destination/source | Attempt action | Explicit refusal/protection/load error, no hidden target or Unfiled outage. |
| BDD-09.1 Filter outline | Happy Path | US-9.1 | Composed title/workspace/agent/date and real states below | Select filter | Exact set; helper under nonmatching-parent context, no queued-as-running. Needs me main-only, even if helper awaits a decision. |
| BDD-09.2 Open live overview | Happy Path | US-9.2 | B/modal search matches unopened A | Receive A state/attention/plan/member update | Reconcile in place/no reopen/search reset; no metadata-view goal acknowledgement or stop. |
| BDD-09.3 Coverage error | Error Path | US-9.3 | Cached rows, page/source missing/failed/unstable | Settle refresh | Partial/unknown/Retry, not complete/all-idle/zero; successful moving-page counterexample below. |
| BDD-09.4 Large child-only search | Edge Case | US-9.4 | >20 rows/multiple pages, only child title matches | Search title | Real ancestor/child reachable, truthful coverage/count, bounded virtual or supported plain path. |
| BDD-09.5 Repeated helper expansion | Happy Path | US-9.5 | Nine identical consecutive helper titles/kinds under P, same title under Q | Expand **9 similar helper runs** | Every original ID/status/Open under P; Q separate, filtered counts truthful, summary not execution/session target. |

**Examples — BDD-05.1:** Robot; Man; Woman; Omnipus (new-agent default), each with Developer/Azure across named surfaces.
**Examples — BDD-07.1**
| Kind | Path |
|---|---|
| Main chat | Live |
| Extra chat — Launch notes | Historical virtualized |
| Task run | Historical plain fallback |
| Helper | Replay |

**Examples — BDD-07.2 (real inputs, never injected phase)**
| Producer / input | Motion | Expected phase / mount |
|---|---|---|
| Mia active + executing tool start | Normal | Working / mounted |
| Jim active model-response, Mia owner | Normal | Thinking / Jim mounted |
| Mia unresolved question, no active turn | Normal | Waiting / mounted beyond running-message end |
| Jim active model-response, Mia owner | Reduced | Static Thinking text / Jim mounted, zero loops |
| Active model/tool plus pending approval | Normal | Waiting overrides; actual pending producer |
| Question and approval unresolved | Normal | Generic waiting-for-input, no invented reason/producer |
| Accepted queued work only | Normal | Static Queued; not running/waiting-for-person |
| Correlated terminal done/stopped/failed/interrupted, no decision | Either | That turn's indicator hidden; retained outcome/error truthful |
| Disconnected/missing snapshot or ambiguous producer | Either | Static unavailable/reconnecting/Unknown, no guessed loops |
| Reconnect then valid active/model/tool/decision snapshot | Either | Re-derive matching row, not stale cached phase |
| Resolved idle initial chat, no active reply | Either | Static Idle/known owner, no duplicate terminal-reply indicator |

**Examples — BDD-08.1**
| Status | Kind |
|---|---|
| Working | Main chat |
| Waiting for answer | Extra chat |
| Done | Helper |
| Failed | Task run |
| Stopped with cause | Scheduled run |
| Interrupted | Extra chat |
| Unavailable/no lifecycle | Permitted inspectable chat |

**Examples — BDD-09.1**
| Filter | Matching set |
|---|---|
| All | Authorized matches with real parent context |
| Running | Actual executing only; exclude queued/inactive, retain nonmatching parent as context |
| Needs me | **Mains with confirmed needs_attention only**, no helper/extra even if waiting; absent main value remains unknown |

**Combined live-keyboard cases — BDD-08.3/09.2/09.5 (MAJ-004)**
A highlighted activation target is the **real entity identity and action kind**, never an array index. Live reorder retains it; if removed/hidden, clear activation, move focus safely to search and announce unavailability. Enter cannot activate a replacement without explicit navigation. Summary is expand-only, not a session; every unfolded helper Open stays independent. All cases run virtualized/plain, same/different list length.
| Case / scenario | Given | When | Then |
|---|---|---|---|
| Reorder / BDD-09.2 | Extra A highlighted; B becomes newer | Deliver update | A remains activation target, no index-substituted B |
| Enter-after-reorder / BDD-08.3 | Above reordered list, A still highlighted | Press Enter | Open A, not new row at its prior index |
| Removal / BDD-09.2 | A highlighted | Remove A through live update | Clear target, safe search focus + announcement |
| Enter-after-removal / BDD-08.3 | A unavailable/target cleared | Press Enter | No other session opens; explicit new navigation required |
| Fold-hides-helper / BDD-09.5 | Helper H highlighted/visible | Collapse its summary | Safe cleared activation, no sibling/summary substituted as H |
| Expand / BDD-09.5 | Summary focused | Expand summary | Reveal original helpers, no attach; stable independent targets |
| Rename-cancel / BDD-08.3 | Inline rename active | Press Escape | Cancel rename/restore action focus, modal remains open |

### Plans / local activity / saved install
| Scenario | Category | Traces to: | Given | When | Then / And |
|---|---|---|---|---|---|
| BDD-10.1 Starting-chat origin | Happy Path | US-10.1 | P started in A, other plan Tasks-only | Display A activity | P pill/parent/modal row refers to A; no B or Tasks-only fake origin. |
| BDD-10.2 Plan-state outline | Alternate Path | US-10.2 | Reported state below | Update pill/row | Actual state/phase/progress, not optimistic accepted execution. |
| BDD-10.3 Real start-target outline | Happy Path | US-10.3 | Authorized successful/idempotent result below | Activate Open | Actual Tasks/Graph or run session, not assignee main/duplicate execution. |
| BDD-10.4 Unavailable target | Error Path | US-10.4 | Origin/handle missing or target forbidden/deleted | Use result/pill | Explicit unavailable/refusal/recovery; no prose URL/fake parent/foreground substitute. |
| BDD-11.1 B local/A discoverable | Happy Path | US-11.1 | Distinct A/B work, A previously open; A shell still running after turn | Open B panel | B-only work; A in Sessions with **N background commands running**/origin Activity Open; no aggregate panel/elsewhere badge/new view. |
| BDD-11.2 Distinct work/control | Happy Path | US-11.2 | MAIN task also child, scheduler independent run, queued/waiting helpers, shell | Reconcile open-session activity | Count real items once by kind/state/source; queued/waiting not executing; no fake parent/tree-Stop grant. |
| BDD-11.3 Unattributed/source failure | Error Path | US-11.3 | Task/run/plan source failed or verdict no session relation | Render panel | Explicit partial/unknown/Retry; no unrelated/localized/zero claim. |
| BDD-12.1 Saved continuation | Happy Path | US-12.1 | Supported ordinary/extra/Unfiled/child/heartbeat install | Apply upgrade | Legitimate history continuable; heartbeat under validated main; repeat loses/duplicates zero known content. |
| BDD-12.2 Reach Admin replacement | Happy Path | US-12.2 | Joint validated default Admin main/no membership | Select Admin row | Opens/supports real send before old entry removal; no other-workspace main. |
| BDD-12.3 Import failure | Error Path | US-12.3 | Known saved source/import or destination fails | Open chat | Retryable failure, not empty success, legacy fallback or delivery claim. |

**Examples — BDD-10.2**
| State | Meaning |
|---|---|
| Approved | Waiting to start, not running |
| Running | Real phase/pause/progress |
| Running but paused | Pause reason, not uninterrupted execution |
| Done/failed/stopped | Real terminal result, no continuing-running claim |

**Examples — BDD-10.3**
| Start | Display | Target |
|---|---|---|
| Plan | Live | Actual plan/workspace Tasks/Graph |
| Plan | Replayed after reload | Same actual plan |
| Task | Live | Actual returned run session, not main |
| Task | Replayed after reload | Same real run |

### Boundary cases — EC-01–EC-12
| Scenario | Category | Traces to: | Given | When | Then / And |
|---|---|---|---|---|---|
| BDD-E01 Duplicate names | Edge Case | US-1.1 / EC-01 | Two Mia identities/pairs, mains/extras | Select Operations identity's row | Validated pair only; no name/recency substitution. |
| BDD-E02 Pending-delivery decline | Edge Case | US-3.1 / EC-02 | Unconfirmed first send/no real ID | Decline abandonment | Original delivery/message retained; placeholder not main/saved pointer. |
| BDD-E03 Rapid losing failure/success | Edge Case | US-2.4 / EC-03 | B committed, older A pending; A→B or A→B→A | Settle first A late failure/success | Winning tuple/pointers unchanged, losing A never acknowledges; later winning A has its own observed bound. |
| BDD-E04 Delayed bounded acknowledgement | Edge Case | US-4.2 / EC-04 | Shown committed main captured goal1; goal2 later not shown | Complete/retry acknowledgement | Only original bound seen for everyone; no widened delayed/retry capture, decisions/goal2 stay unseen. |
| BDD-E05 Palette-boundary outline | Edge Case | US-5.3 / EC-05 | Stored condition below | Apply one-time mapping | Expected color persists/repeat-stable, not per-screen remapping. |
| BDD-E06 Extreme size/motion | Edge Case | US-5.1 / EC-06 | Every figure/named size, 20 px root, zoom/forced colors/reduced motion | Render surfaces | Badge/figure/text/focus remain; loops stop, no plain-role swap. Dimmest Thinking ink on both backgrounds stays ≥3:1. |
| BDD-E07 Guest plain replay | Edge Case | US-7.1 / EC-07 | Saved task/helper/Jim guest, no virtualizer | Replay plain path | Jim name-only, true kind, owner unchanged. |
| BDD-E08 Filtered repeated helpers | Edge Case | US-9.4 / EC-08 | 21 rows, nine identical siblings P, same-title Q, parent nonmatch | Apply helper-only title/status filter | Each matching group stays under real parent; filtered original counts/targets intact, no cross-parent merge; unavailable parent placeholder; virtual/plain. |
| BDD-E09 Unopened recovery | Edge Case | US-9.3 / EC-09 | Unopened work/missing page/lifecycle/token values | Reconnect Sessions | Reconcile authorized metadata, explicit unknown/partial; no done/zero/goal acknowledgement from missing data. |
| BDD-E10 Scheduler/task-child dedupe | Edge Case | US-11.2 / EC-10 | Starter/assignee/child same run, scheduler-only second run | Display activity | Each actual run once per legitimate view; no duplicate helper/control grant. |
| BDD-E11 Tasks-only/idempotent plan | Edge Case | US-10.1 / EC-11 | No-origin plan + idempotent retry, deleted target variant | Reconcile plan metadata | No fake origin/duplicate pill; deleted target unavailable. |
| BDD-E12 Interrupted conversion retry | Edge Case | US-12.1 / EC-12 | Known supported data/partial previous upgrade | Resume cutover | Actual continuation or visible failure; repeat never erases/duplicates known content. No rollback case added (S Q-G3). |

**Examples — BDD-E05**
| Condition | Mapping |
|---|---|
| Missing/invalid six-digit hex | Grey |
| Saturation below 0.25 | Grey |
| Saturation equal/above 0.25 | Closest non-Grey circular hue |
| Exact hue-distance tie | First tied published entry |
| Hue across 0/360 | Shorter circular, not linear distance |

### Combined coverage and meaningful recovery cases (existing IDs)
Each row below is an independent Given/When/Then case, not a compound When or new scenario ID. Error/edge categories remain error/edge. Negative outcomes followed by actual supported recovery satisfy S Q-G4, not the former literal Happy Path-label gate.
| Scenario(s) | Given | When | Then |
|---|---|---|---|
| BDD-09.2/09.3/09.4 | `s1,s2,s3,s4`, first offset page `s1,s2`; s4 becomes active/moves ahead | Fetch second offset page | Success/end cursor returning `s2,s3` cannot certify complete/zero-running; reconcile stable cut until s4 appears. |
| BDD-01.4 | Roster/member source recovered; error/stale cache visible | Retry expansion | Real eligible roster, notice clears only on success, no invented target. |
| BDD-02.3 | Original B intent still current, valid after failed attach | Retry B entry | Valid coherent B shown/send enabled; acknowledge only shown main's observed bound. |
| BDD-02.4/BDD-E03 | Losing A suppressed; B still valid | Explicitly select A again | New winning intent attaches/commits A; no old callback/pointer/bound reused. |
| BDD-03.4 | Authorized history restored/connection live, original intent current | Retry selected history | Actual original chat opens once; no previous false success or unrelated target. |
| BDD-04.4 | Main projection source recovered | Retry snapshot | Actual true/false cue, unknown notice clears; no automatic metadata-read acknowledgement. |
| BDD-04.3 | Reduced-motion mode left static cues | Disable reduced motion | Locked motion returns, meaningful text/distinct count unchanged. |
| BDD-05.4 | Agent lookup succeeds after failure | Retry identity | Authoritative same agent displayed, not a fabricated saved/deleted identity. |
| BDD-06.3 | Failure resolved, existing create/autosave path available | Retry supported save | Only authoritative saved/activated receipt updates global identity, no preview-as-save shortcut. |
| BDD-07.4 | B connection/selected control restored | Retry B control/recovery action | Supported B outcome/control only; A remains outside its scope, correlated error/reply safe. |
| BDD-08.4 | List/ordinary destination recovered; main still protected | Retry supported non-destructive load/open | Authorized intended row opens; protected delete still refused, no hidden metadata leak. |
| BDD-09.3/BDD-E09 | Failed page/coverage reconciled to stable authorized cut | Retry overview | Missing running work appears; complete only with proof, no false quiet interval. |
| BDD-09.4/BDD-E08 | Highlighted helper hidden/removed during live change | Navigate explicitly to another visible target | Intended new identity activatable; prior Enter opened no substituted session. |
| BDD-10.4 | Missing published handle/authorized target now available | Retry actual Open | Real plan/run opens, not guessed URL; permanently forbidden/deleted stays refused. |
| BDD-11.3 | Source restored or verdict properly attributed | Retry activity load | Real local counts/state appear, no foreign/global localization; bad attribution stays unavailable. |
| BDD-12.3/BDD-E12 | Supported importer/source repaired, known histories retained | Retry upgrade/open | Actual known chat content/binding/continuation, no fake empty-success or duplicate history. |

## Test-Driven Development Plan

These are **proposed tests**, not executed results. QA writes RED before production; independent QA performs CHECK after GREEN. Expected values come from this spec, F/R/M and frozen datasets, not implementation output. Shared wire fixtures must validate through real generated validators; do not mock the navigation/attachment/reducer under test. Unit tests precede integration, then end-to-end tests; within each level, foundations precede consumers.

### Test hierarchy and proposed locations

| Level | Scope and existing conventions |
|---|---|
| Unit | Identity/state/ordering/filter/deduplication rules beside their owning source as `.test.ts`/`.test.tsx`; Vitest + Testing Library existing helpers. |
| Integration | Real AppShell/sidebar without AgentPicker, real query/store/attach/rendering seams and a controlled gateway twin for failures/races; separate real-gateway U1 pack below. Extend existing component/store tests or add adjacent files. |
| E2E | Real candidate UI/backend and supported saved installation; proposed `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/tests/e2e/agent-first-navigation.spec.ts` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/tests/e2e/sessions-activity.spec.ts`. These files do not exist yet; QA owns them. |
| Kit evidence | Published stories/manifests with executed static-Storybook interaction, axe, keyboard, browser, pointer, reduced-motion, forced-colors, root-size, zoom/reflow and screenshot evidence. Add only applicable truthful checks, never mark unexecuted behavior inapplicable. |

### Test implementation order

Each test name is a proposed family; each listed BDD scenario becomes a concrete named case, with every outline example independently executed. This table is not permission to collapse assertions into a single snapshot or text scan.

| Order | Test name | Level | Traces to BDD scenario(s) | Independent oracle / what must fail when broken |
|---|---|---|---|---|
| 1 | T-01 eligibleMainPair | Unit | BDD-01.1, BDD-01.2, BDD-E01 | Membership/eligibility and validated pair dominate name/recency; workers/hidden agents absent, Admin default-only. |
| 2 | T-02 restoreWinningIntent | Unit | BDD-02.1, BDD-02.2, BDD-02.3, BDD-02.4, BDD-E03 | Exact browser pointer, validated Ava fallback only on confirmed absence; latest intent wins, no unresolved send. |
| 3 | T-03 extraDeliveryGuard | Unit | BDD-03.1, BDD-03.3, BDD-E02 | Explicit row action and preserved original first-send recovery/abandonment. |
| 4 | T-04 mainAttentionProjection | Unit | BDD-04.1, BDD-04.2, BDD-04.4, BDD-E04 | Four-source truth, non-main exclusion and unknown-versus-false; observed goals only acknowledged. |
| 5 | T-05 paletteMigrationOracle | Unit | BDD-05.2, BDD-05.3, BDD-E05 | Frozen palette order/saturation/circular-distance/tie and old-role fixtures; repeat mapping stable. Backend migration test uses same external expected values. |
| 6 | T-06 identityVocabulary | Unit | BDD-05.1, BDD-05.2, BDD-E06 | Four figures, 31 grouped roles, ten colors and all named sizes; no small badge fallback. |
| 7 | T-07 immutableAuthorAndKind | Unit | BDD-07.1, BDD-07.2, BDD-E07 | Actual message/turn producer differs from session owner without changing owner/kind. |
| 8 | T-08 modalHierarchyAndFilters | Unit | BDD-08.2, BDD-09.1, BDD-09.4, BDD-09.5, BDD-E08 | Main first; helpers always under their parent in every filter/search. All / Running / Needs me and repeated-helper grouping/counts/expansion are decided. Unavailable parent treatment stays Q-M5; no synthetic summary-session identity. |
| 9 | T-09 workIdentityAndOrigin | Unit | BDD-10.1, BDD-10.2, BDD-11.2, BDD-E10, BDD-E11 | Real starting chat, distinct work keys, scheduler-only runs and queued/waiting exclusions; no synthetic control parent. |
| 10 | T-10 contractCoverageUnknown | Unit | BDD-01.4, BDD-05.4, BDD-08.4, BDD-11.3 | Actual generated validation plus unknown handling; failed/unattributed sources cannot become false/removed/local/zero. |
| 11 | T-11 shellFreshnessWithoutPicker | Integration | BDD-01.4, BDD-09.2 | Mount real AppShell/sidebar with no AgentPicker. Agent-created update then separate member save; focus/visibility/expand inside staleTime refresh both caches with visible errors. Hook-only green is insufficient. |
| 12 | T-12 atomicSelectionAndLateAttach | Integration | BDD-02.1, BDD-02.2, BDD-02.3, BDD-02.4, BDD-03.4, BDD-E03 | Route/store/display/send tuple and saved pointers agree; reject attach transport/server failure and old result without partial workspace commit. |
| 13 | T-13 rowActionsAndPastFilters | Integration | BDD-03.1, BDD-03.2, BDD-03.3, BDD-E02 | Real independent actions, visible/removable workspace+agent filters, keyboard/touch and first-send protection. |
| 14 | T-14 attentionIntentAndMotion | Integration | BDD-04.1, BDD-04.2, BDD-04.3, BDD-04.4, BDD-E04 | Exact cues/text and generated `ack_attention` foreground-open intent; no ack on metadata search/prefetch/reconnect, failed/unauthorized attach. |
| 15 | T-15 sharedAgentIconPublication | Integration | BDD-05.1, BDD-05.2, BDD-05.4, BDD-E06 | Actual shared component across surfaces, graphic contrast, central motion, complete publication and executed accessibility checks. |
| 16 | T-16 globalEditorSaveAndLocks | Integration | BDD-06.1, BDD-06.2, BDD-06.3 | Existing wizard/profile preview, global update, editability and real save/activation error status; uploads absent. |
| 17 | T-17 feedPathsAndCommandCutover | Integration | BDD-07.1, BDD-07.2, BDD-07.3, BDD-07.4, BDD-E07 | Live/virtual/plain/replay names-only messages and actual inline responder; preserved model/Auto/errors/controls, exact command removal/rename and no Clear-to-new action. |
| 18 | T-18 modalRowsAndControls | Integration | BDD-08.1, BDD-08.2, BDD-08.3, BDD-08.4 | Status/kind/dot, group/count/shared-row/search controls, protected delete, rename Escape/focus and independent action targets at desktop/phone. |
| 19 | T-19 modalLiveCoverageAndPages | Integration | BDD-09.1, BDD-09.2, BDD-09.3, BDD-09.4, BDD-09.5, BDD-E08, BDD-E09 | Real metadata/live updates, All / Running / Needs me and matching helpers always inside parent context; expand repeated helpers with truthful filtered counts/targets, keyboard summary action not attach; pagination errors and large/plain paths. |
| 20 | T-20 validatedStartResultLinks | Integration | BDD-10.1, BDD-10.2, BDD-10.3, BDD-10.4, BDD-E11 | Published result/origin/workspace, real Tasks/Graph hand-off and real run session; live/replay/idempotence, denied/missing target. |
| 21 | T-21 openSessionActivityOwnership | Integration | BDD-11.1, BDD-11.2, BDD-11.3, BDD-E10 | Panel A/B isolation and Sessions overview, task/helper dedupe and unchanged independent Stop authority; unattributed judge work not localized. |
| 22 | T-22 upgradeAndCanonicalSeeds | Integration | BDD-05.3, BDD-12.1, BDD-12.2, BDD-12.3, BDD-E12 | Actual backend importer/config/fresh seed plus repeated gateway restarts; UI reload alone cannot prove this. |
| 23 | T-23 jointU1Navigation | E2E | BDD-01.1, BDD-01.2, BDD-01.3, BDD-02.1, BDD-02.2, BDD-02.3, BDD-02.4, BDD-03.1, BDD-03.2, BDD-03.4, BDD-12.2, BDD-E01, BDD-E02, BDD-E03 | Real U1 destinations/attachment/message outcomes, row/history/Admin reachability and preserved panels; no stand-in main backend. |
| 24 | T-24 jointMainAttention | E2E | BDD-04.1, BDD-04.2, BDD-04.3, BDD-04.4, BDD-E04 | Real unopened-main four sources, two-person shared ack, append/open race, resolution, initial load/reconnect and reduced motion. |
| 25 | T-25 identityAndFeedSurfaces | E2E | BDD-05.1, BDD-05.2, BDD-06.1, BDD-06.2, BDD-06.3, BDD-07.1, BDD-07.2, BDD-07.3, BDD-E06, BDD-E07 | Compare actual product surfaces with W, locked choices/preview, actual guest author, all feed paths and motion/zoom/coarse input. |
| 26 | T-26 sessionsOverallActivity | E2E | BDD-08.1, BDD-08.2, BDD-08.3, BDD-08.4, BDD-09.1, BDD-09.2, BDD-09.3, BDD-09.4, BDD-09.5, BDD-11.1, BDD-E08, BDD-E09 | Real A/B visibility; All / Running / Needs me and helper-only search keep helpers under parent; folded N similar helper runs expands every original target. Verify live/partial coverage, virtual/plain/phone/keyboard/protection, no cross-parent grouping. |
| 27 | T-27 realPlanTaskDrilldown | E2E | BDD-10.1, BDD-10.2, BDD-10.3, BDD-10.4, BDD-11.2, BDD-11.3, BDD-E10, BDD-E11 | Real plan/task/scheduler starts with actual destination; accepted versus running, replay links, no-origin, duplicate work/control authority. |
| 28 | T-28 savedInstallContinuation | E2E | BDD-05.3, BDD-12.1, BDD-12.3, BDD-E05, BDD-E12 | Supported saved install then real follow-up messages, identity/restart/repeat upgrade and visible partial failure; fresh fixtures alone fail acceptance. |
| 29 | T-29 failureAndControlBoundaries | E2E | BDD-01.4, BDD-05.4, BDD-07.4, BDD-11.3 | Force real offline/refusal/error paths; keep committed chat, no unrelated Stop and no fabricated completed/zero state. |
| 30 | T-30 guideAndReachabilityWalkthrough | E2E | BDD-01.3, BDD-03.1, BDD-03.2, BDD-07.3, BDD-08.3, BDD-12.2 | Walk the updated user guides against actual UI destinations/labels/commands; docs-verifier independently audits factual parity, not only link existence. |

### Joint backend U1 integration pack — mandatory landing hold

Team-lead owns the joint candidate and execution receipts; QA coordinates backend/front-end expectations. U1 must be working, not merely an approved ADR or a published spec. Every row runs against **one exact combined SHA**. If a dependency changes, repeat its affected checks and exact-commit acceptance. No frontend-only main-navigation landing or undocumented compatibility fallback.

| Pack | Joint inputs and proof | Test families |
|---|---|---|
| J-01 | Two workspaces, same agent, validated main plus newer extra; remembered main/extra, first visit and confirmed missing/hidden/unavailable welcome; assert real attached and subsequent message destination. | T-12, T-23 |
| J-02 | Load/server/transport failure and controlled late resolution after a newer selection; displayed workspace/session/owner and persisted pointer stay consistent; unresolved sending unavailable. | T-12, T-23, T-29 |
| J-03 | Question/approval/met/rounds_exhausted/other/stopped_by_user in main and negative extra/helper sources; initial unopened main, shared observed acknowledgement, prefetch/reconnect/failed-open exclusion and newer outcome race. | T-14, T-24 |
| J-04 | Still-connected second tab creates agent, saves membership separately, first tab refocuses/expands inside staleTime; real shell with no picker updates roster+membership and honest stale/Retry state. | T-11, T-23 |
| J-05 | Frozen real supported-install ordinary/extra/Unfiled/child/heartbeat histories with known bindings/content; upgrade/repeat/restart and actual follow-up; migration failure visible; Admin main reachable before old entry removal. | T-22, T-23, T-28 |
| J-06 | Fresh/custom/built-in identity changes and mapping; canonical definitions/fresh seed/startup persistence/repeated restart agree, locks unchanged; all shared visuals and reduced motion verified against W. | T-15, T-16, T-22, T-25, T-28 |
| J-07 | Real plan start in A, task start, scheduler-only run, MAIN child dedupe, no-origin Tasks plan; actual Open plan/run session live and replay; B panel local, A in Sessions; partial/unopened metadata coverage explicit. | T-19–T-21, T-26, T-27 |
| J-08 | Immediate command/picker/handover cutover coordinated with canonical producers/generated types; preserve first-send, kickoff/replay/ordering/refusal, model/Auto and Stop/tree boundaries; doc instructions match. | T-17, T-23, T-29, T-30 |

### Execution integrity and gates

| Step | Required evidence — future implementation work |
|---|---|
| RED | Named tests fail on pre-change code. Prefer CI on a tests-only commit; any necessary one narrow local run is serialized by team-lead. Save real command/output/exit code and exact SHA. No test-only green without negative controls. |
| GREEN | Implementing lead changes only commissioned production behavior, using published contracts. Remote CI owns heavy Go/build/node/Storybook/browser gates; local typecheck is the real `npm run typecheck`, never bare TypeScript no-op. |
| CHECK | Independent QA audits test integrity and mutates recency/pair resolution, no-picker refresh, missing-attention coercion, wrong producer, panel scope, start origin and task-child dedupe in isolation. Every relevant mutation must fail its named assertion; restore before the gate. No skip/deletion/relaxed assertion to manufacture green. |
| Discovery | Prove new test files are included by existing CI group/shard mechanisms. Read actual configuration and executed named test counts; passing a filter that selects zero tests proves nothing. |
| Feature review | Required five reviewers: code-reviewer, silent-failure-hunter, pr-test-analyzer, architect cross-cutting and security-lead. QA CHECK and docs-verifier are additional, not replacements. All findings closed or founder-approved tracked deferrals. |
| Hands-on acceptance | Joint engine-touching work requires uat-tester and independent uat-validator on the exact landing SHA, founder-set provider/model, real screenshots and authorized entry-point proof. Prototype or mock-only tests cannot certify runtime readiness. |
| Landing | Founder approval after joint packs, applicable remote gates, updated user docs and independent acceptance. Keep separate claims: code correct and tested; reachable by a user/agent. This draft claims neither. |

### Test Datasets

Frozen expected values come from the binding design, not a run of the implementation. Each row links to a BDD oracle. An upper bound not supplied by the sources is **not invented**: `N` below is the committed backend page size, not a product limit. Very large fixtures exercise coverage/rendering, not a new supported-capacity promise.

#### Dataset DS-N — destinations, ordering and failure

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| N01 | One eligible agent with main M and newer extra E | Minimum nonempty/happy | Row opens M, never E | BDD-01.1 | Exact pair fixture |
| N02 | Same name on different identities in two workspaces | Duplicate/identity | Correct validated pair | BDD-E01 | Names are not keys |
| N03 | Empty eligible roster/no Ava association | Zero/empty | Honest unavailable + Team/manage + Retry; no send | BDD-02.2 | Not a blank extra |
| N04 | Null/absent saved pointer plus valid Ava main | Null/first visit | Validated Ava main | BDD-02.2 | No recency lookup |
| N05 | Remembered valid main/extra; another tab's chat newer | Happy/cold restore | Exact browser pointer | BDD-02.1 | Both chat kinds |
| N06 | Confirmed deleted/hidden/forbidden remembered target | Invalid/permission denied | Validated welcome destination or unavailable; hidden main never opened | BDD-02.2 | Only confirmed invalidity falls back |
| N07 | Timeout/offline/server failure checking saved target | Dependency failure | Retain intent; visible Retry; no false deletion/new-chat fallback | BDD-02.3 | Controlled failed response, not elapsed-time guess |
| N08 | A resolution late after B commit; attach rejection variants | Concurrent access/race | B remains consistent; A cannot overwrite pointers | BDD-E03 | Force order deterministically |
| N09 | Pending first delivery with unknown real chat ID | Transient/abandonment | Original delivery retained on decline; placeholder not saved as main | BDD-E02 | New-chat guard preserved |
| N10 | Authoritative worker/hidden-agent member; Admin default/other pair | Eligibility edge | Worker/hidden absent; Admin only validated default main | BDD-01.2 | No invented team membership |

#### Dataset DS-A — attention, acknowledgement and motion

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| A01 | Main pending structured question/approval, separately and together | Happy/OR composition | Attention true; resolution clears only resolved source | BDD-04.1, BDD-04.2 | Not text parsing |
| A02 | Main unseen met / rounds_exhausted / other | Goal-source matrix | True; finished / failed / failed respectively | BDD-04.1 | Exact BS mapping |
| A03 | stopped_by_user only; generic unread text/task notice/global verdict | Negative source | False for valid main unless another allowed source exists | BDD-04.1 | No new waiting source |
| A04 | Extra/helper pending source; non-main response omits attention | Scope/absent | No agent-main signal from that other session | BDD-04.1 | Non-main omission is normal |
| A05 | Valid main false; valid main true; main value absent | False/true/missing | Off; On; Unknown respectively | BDD-04.4 | General optional field is not a false fallback |
| A06 | One goal observed; next outcome appended after captured bound | Concurrent open/write | Only observed goal acknowledged | BDD-E04 | Stable saved identity, backend-owned order |
| A07 | User A successful explicit open; User B same main | Shared-state edge | Observed goal clears for both; questions/approvals persist | BDD-04.2 | Pair-wide, not per-person inbox |
| A08 | Background prefetch/reconnect/replay/failed or unauthorized attach | Ack-negative matrix | No acknowledgement | BDD-E09, BDD-04.4 | Generated ack false/absent where applicable |
| A09 | Two sessions for one main agent; two distinct mains needing attention | Count/deduplication | Workspace count by distinct confirmed mains, not sessions | BDD-04.3 | No agent-row dot |
| A10 | Normal/reduced motion; selected/unselected/collapsed rows | Motion/accessibility | Locked pulse/dot; zero loops under reduced motion, text retained | BDD-04.3 | Measure cue and target geometry |

#### Dataset DS-I — identity, migration and rendering

Arithmetic fixtures were independently calculated and compared with the approved prototype source; receipts are `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/spec-evidence/palette-oracles.log` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/spec-evidence/palette-source-comparison.log`. These prove the reference calculation only, not the backend migration.

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| I01 | All 4 figures × 31 roles × ten colors | Full valid vocabulary | Same figure/badge/color across named surfaces; Omnipus creation default | BDD-05.1, BDD-05.2 | Parameterized, not copied from component options |
| I02 | 18/26/40/48 px; 20 px root; zoom/forced colors/reduced motion | Min/max named size/access edge | Badge/figure remain; meaningful text/targets, no loops when reduced | BDD-E06 | Small-size plain-role substitution must fail |
| I03 | `#D4AF37` | Former brand hue | Orange `#FB923C` | BDD-E05 | Migrate once, persist |
| I04 | `#7B1FA2` | Former low-contrast color | Fuchsia `#E879F9` | BDD-E05 | Not a guessed Purple mapping |
| I05 | `#AD1457` | Former low-contrast color | Pink `#F472B6` | BDD-E05 | Circular hue oracle |
| I06 | `#A0A079` | Threshold minus one RGB step; saturation 0.24375 | Grey `#9CA3AF` | BDD-E05 | Strictly below 0.25 |
| I07 | `#A0A078` | Exact threshold; saturation 0.25 | Orange `#FB923C` | BDD-E05 | Equality goes to hue matching |
| I08 | `#A0A077` | Threshold plus one RGB step; saturation 0.25625 | Orange `#FB923C` | BDD-E05 | Just above |
| I09 | `#000000`, `#FFFFFF`, `#E2E8F0` | Zero/grey/semantic-silver | Grey `#9CA3AF` | BDD-E05 | No brand/semantic picker colors |
| I10 | Missing/null/empty; `bad`, `#fff`, invalid digits | Empty/null/malformed | Grey `#9CA3AF` | BDD-E05 | Six-digit syntax only |
| I11 | `#3B82F6` / already-valid curated role | Valid/repeat | Azure unchanged; valid role preserved | BDD-05.3 | No remapping every startup/render |
| I12 | Normalized hue exactly midway between Azure and Indigo; same saturation ≥0.25 | Exact tie | Azure, earlier published entry | BDD-E05 | Tie stage oracle, no arbitrary persisted test color |
| I13 | Hue immediately below 360 and above 0 | Circular endpoint | Shorter circular hue distance, not linear distance | BDD-E05 | Compare non-Grey candidates |
| I14 | Code/Chat/MagnifyingGlass/PencilSimple/Shield/other old icon | Migration matrix | Developer/General assistant/Researcher/Writer/Security/General assistant | BDD-05.3 | Backend role representation published first |
| I15 | Name `a`; 100-character valid name; 101-character invalid response name | Existing schema min/max/max+1 | Valid response sizes render; invalid response fails actual generated validation, never successful identity | BDD-05.4, BDD-06.3 | C-WIRE Agent schema, not new title policy |
| I16 | Unicode/combining/RTL name; `<script>`-like title; 10 KiB malformed response string | Unicode/special/large invalid | Valid text is inert/accessible; malformed contract response shows failure, not execution or silently clipped success | BDD-05.4, BDD-08.4 | No new string budget; validate actual schema |

#### Dataset DS-S — Sessions, activity and links

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| S01 | Zero sessions versus failed sessions/workspaces query | Empty versus failure | Honest no matches versus explicit error/Retry, never Unfiled outage | BDD-08.4, BDD-09.3 | Positive valid-empty control |
| S02 | 1, 19, 20 and 21 visible rows | Minimum/threshold−1/threshold/threshold+1 | Complete keyboard-reachable result set; viewport virtualization beyond current 20-row threshold and valid plain fallback | BDD-09.4, BDD-E08 | Existing C-MODAL threshold, not capacity cap |
| S03 | N−1/N/N+1 rows across published page size; later-page error; 1,000 metadata rows | Page boundary/very large | Complete authorized matches or visibly partial coverage; no transcript fan-out | BDD-09.3, BDD-09.4 | N from real committed contract |
| S04 | Main + extras + real helper + unavailable parent + child-only title/agent/status match | Hierarchy/ordering/filter context | Main first; helpers always beneath real parent, nonmatching parent remains context; Q-M5 governs unavailable-parent notice, never root promotion | BDD-08.2, BDD-09.1, BDD-E08 | No fake parent or top-level helper |
| S05 | Nine consecutive identical helper titles/kinds under P, distinct IDs/status/targets; same title under Q | Repeated content/count/parent boundary | Decided `9 similar helper runs` under P expands to all nine originals; no cross-parent merge. Filtered summary counts only matching originals; no execution merge | BDD-09.5, BDD-E08 | Test 1/2/9 repeated helpers, mixed states, every filter, search and virtual/plain paths |
| S06 | Every published lifecycle/kind; missing lifecycle; zero/missing tokens; invalid timestamp | State/null/zero | Truthful status/kind or unavailable state; zero versus missing not fabricated; no dangling metadata | BDD-08.1, BDD-E09 | Values through real generated validators |
| S07 | One task run projected as starter/assignee/helper; scheduler-only independent run; >8 active items | Duplicates/non-tool origin/old finish cap+1 | Distinct actual counts uncapped; correct source/control scope | BDD-11.2, BDD-E10 | Eight-item finish display cap cannot cap active work |
| S08 | Plan started in A; internal owner elsewhere; Tasks-only plan; idempotent start | Origin/absent/retry | A association only; no Tasks-only origin; no duplicate work | BDD-10.1, BDD-E11 | Requires published real origin |
| S09 | Approved/running/paused/done/failed-with-stop-reason plan; success/replayed result | State matrix/happy | Canonical state/progress and actual Open plan/run destination | BDD-10.2, BDD-10.3 | Stopped is not invented Plan.state enum |
| S10 | Missing handle, unauthorized/deleted target, unexpected/truncated payload | Dependency/error | Explicit unavailable/refusal; no fabricated URL or foreground/main substitute | BDD-10.4 | Published error surfaces only |
| S11 | A unseen in this browser; modal open in B; missed WS update and reconnect | Initial coverage/concurrency | A's metadata reconciles; B panel local; no goal ack on refresh | BDD-09.2, BDD-E09, BDD-11.1 | Cache-only coverage must fail |
| S12 | Same origin chat with live shell but no real stored shell-chat session | Non-session work | Follow approved Q-M4 metadata/drill-down presentation, never fake a chat row | BDD-11.1, BDD-11.3 | Held until UI/contract choice settled |

Row dates/empty/null constraints preserve existing behavior and generated validation, rather than adding a new date-format API. Error twins include timeout, permission denied, unavailable dependency, truncated/unexpected data and recovery; the real candidate must also demonstrate those failure paths.

### Regression Test Requirements

This **modifies existing functionality**. Existing assertions for preserved behavior stay strong and pass unchanged where their fixture/interface remains current. Canonical main/immutable-owner/generated-contract fixture adaptations and explicitly retired behavior are tracked separately; do not demand that an old switching/fresh-entry test stay green by retaining the removed behavior. QA authors those targeted replacements, recording which acceptance decision changes the old oracle.

| Preserved behavior | Existing test source read / tests to keep | New regression family |
|---|---|---|
| Confirmation before ordinary delete; active pointer pruning; rename Enter/Escape/focus restore; Escape closes only outside rename | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/search/SearchModal.test.tsx`::SearchModal — delete flow, rename flow, editing-state contract. Keep assertions; protected heartbeat fixture becomes a protected main under the coordinated contract cutover. | T-18; add main protection independent of heartbeat enabled. |
| Child-inclusive search, child-only match reveals its parent, large fan-out/plain fallback and default verifier exclusion; unavailable helper parents are never promoted to roots under latest F | Same file::session query call shape, nests a child-only match under its parent and virtualizes a large fan-out. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/sessions/SessionTree.tsx`::flattenSessionTree behavior retained. | T-08, T-19, T-26 with new filters/metadata, paging and pinned main. |
| Unfiled standalone inspection; no duplicate replay reset/attach; token seed only after actual attachment | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/useSelectSession.test.tsx`::Unfiled session, never calls setActiveSession, attach → seedSessionTokens → setActiveAgentType sequence. Keep outcome assertions; adapt current immutable owner/attach input. | T-12, T-23 with server-validated/failed cross-workspace selection. |
| Socket rejection is visible; current-chat first Stop versus confirmed tree Stop remains scoped | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/chat.cancel-delivery.test.ts`::cancelStream delivery report, second cancel after the first one ended the turn locally. Existing refusal/tree-scope assertions remain. | T-17, T-29 after navigation/command cutover. |
| Exactly-running helper count excludes queued, lifecycle-terminal/open-span and shells | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useRunningActivity.runningChildren-lifecycle.test.ts`::runningChildren is exactly-lifecycleState-running. | T-09, T-21 task-child/scheduler dedupe and new kind counts. |
| Shell result liveness survives tool completion/baked messages; active work uncapped; recent-finish cap/recency | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useRunningActivity.test.ts`::bash session tracking survives turn finalization, running is uncapped/recentlyFinished capped, mergeAndCapFinished. Keep same-session liveness/recency oracles. | T-21, T-27; correctly attribute verdicts rather than preserve a global leak. |
| One modal instance and clean mode reset; workspace mode includes zero-session real workspaces and excludes Unfiled switch | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/ui.store.searchModal.test.ts`::searchModal UI store — mode; SearchModal test::workspaces mode and workspace-switch arrow. | T-13, T-18 with pair filters and N D3 exact-entry behavior. |
| Focus/visibility invalidation and listener cleanup | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useAgentsCrossTabRefresh.test.tsx`::focus path. Existing hook checks remain but cannot alone prove navigation freshness after picker removal. | T-11 real shell/sidebar without picker, roster + separate membership save. |
| Unified command menu query failure remains visible | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ChatScreen.slash-commands-error.test.tsx`::commands query error. Preserve commands-unavailable/skills partition oracles; remove mention-switch-specific behavior only per F Q10. | T-17; no old aliases, visible failure and still-supported controls. |

| Intentionally changed old oracle | Required replacement — not silent deletion |
|---|---|
| Modal workspace switch starts a fresh session; no-pointer entry shows blank composer | Replace with exact remembered chat/validated Ava main and honest failure. N D3/F Q1; T-02/T-12/T-23. |
| Sessions-mode groups start expanded; handmade group/row controls | Apply R35/M D1 kit group defaults with search/prefilter match reveal while preserving descendants and keyboard scope. Update interaction setup, not assertions that all intended results stay reachable. T-08/T-18/T-19. |
| Heartbeat-only ordering/protection; mutable owner precedence; UUID-only/128-character main assumptions | Coordinate BS main contract/owner/bounds, never browser fallbacks. Same preserved protection/continuation outcomes with validated computed IDs. T-01/T-12/T-22. |
| Picker/mention switching, old commands, bubble avatars, old thinking/goal scalar | Replace only under F Q9/Q10 and BS coordinated deletion map. Keep actual author/correlation/replay/error/context-specific phrase behavior with canonical producers. T-07/T-17/T-25/T-29. |
| Global unattributed verdict shown as local activity | Keep existing verdict detail reachable through correctly attributed existing plan/task/goal inspection; do not preserve misattribution to satisfy old global-feed fixtures. N D14 as narrowed by F Q-FE-11; T-10/T-21. |
| Helper orphan promoted to a root, or filtered helper separated from its nonmatching parent | Latest F overrides that baseline. Every helper stays under its parent in All / Running / Needs me and text/pair/date filtering, including repeated-helper summaries. Q-M5 handles unavailable real parents; T-08/T-19/T-26, BDD-08.2/09.1/09.5/E08. |

#### Regression Dataset DS-R — preserved old behavior

Run these before and after implementation using a frozen baseline fixture; tests of deliberately new main contracts run RED first. Existing rename/protection/mode fixture input adaptations are explicit, not softened expected results.

| # | Input | Previous behavior | Must still produce | Traces to |
|---|---|---|---|---|
| R01 | Ordinary session trash click, then cancel | No delete before confirmation | No delete before confirmation; cancellation does not delete | BDD-08.4 |
| R02 | Inline rename then Escape/Enter; rerender or group collapse while editing | Cancel/commit and restore focus; no wedged modal | Same focus/rename/close semantics | BDD-08.3 |
| R03 | Child-only title/status/agent match at depth 3; unavailable real parent; repeated identical helpers | Previous nesting/search remains; old orphan-as-root rule is superseded by F | Helpers always under parent in every filter/search; Q-M5 unavailable-parent treatment, expandable repeated-helper summary with original targets | BDD-09.1, BDD-09.5, BDD-E08 |
| R04 | Unfiled session from non-chat route | One standalone inspection/attach, not attach-and-vanish | Same inspectable real destination, no duplicate replay reset | BDD-03.4, BDD-12.1 |
| R05 | Background shell dispatch baked after turn ends, then poll completion | Liveness follows result and moves once to finished | Same single actual process/result state, not tool-call status | BDD-11.2 |
| R06 | One running helper, one queued, one terminal open span and shell | Running helper number exactly 1 | Same helper count; new task/kind counts separate | BDD-E10 |
| R07 | Rejected socket send for selected-chat Stop; unrelated A work | Visible refusal, truthful scope | Same refusal and no new control of A | BDD-07.4 |
| R08 | Workspace mode with real empty workspace, then close and open Sessions | Empty workspace reachable; mode resets | Same workspace reachability with new entry destination; no stale mode/pair filter leak | BDD-02.1, BDD-03.2 |
| R09 | Prior cached identity and failed agent query | Unknown, not removed | Same honest identity failure without local saved-value invention | BDD-05.4 |

No source-text presence assertion, snapshot-only test or direct mocked hook invocation can replace a real destination, live-update, authority or rendered-identity oracle. No existing test is declared passing by this spec task.

## Functional Requirements

All MUST statements below are non-negotiable within the commissioned scope. Held choices specify a decision boundary, not an unapproved option. Wire publication and founder/wireframe holds remain; no named requirement authorizes bypassing them.

| ID | Testable requirement | Binding source / ADR decision |
|---|---|---|
| FR-001 | Sidebar MUST list authoritative eligible main colleagues per workspace; exclude workers/hidden engine agents even when members, with only the validated default-workspace Admin exception. | R3/R5/R7; N D2/D5/D10; F Q12. |
| FR-002 | Chat MUST remain the base surface without a Chat header item; existing workspace tools/panel behavior, single workspace Library and distinct global Library/Agents access, workspace ordering/archive/pin/drawer/keyboard behavior MUST be preserved. | R1/R2/R6–R8; N D1/D2/D11. |
| FR-003 | Agent row click MUST attach that pair's validated main, never a newer extra/name-matched/computed-but-unvalidated destination. Extra inspection still selects its owner's row. | R4/R22; N D3/D5; B D1.1. |
| FR-004 | Workspace/login/modal-switch entry MUST restore the exact valid visible remembered main/extra, else the validated Ava welcome main only for no real pointer or confirmed invalidity; unavailable destination and late/failed resolution MUST preserve honest intent, consistent selection and send gating. | R47; F Q1; N D3/D12. |
| FR-005 | + New chat MUST be the deliberate extra-chat action for the selected pair, preserving main and first-send recovery/abandonment protection; its independent named target follows Past sessions and works on selected/hover/focus/touch rows. | R22; F Q10; N D3/D11. |
| FR-006 | Past sessions MUST open the one existing Sessions modal with visible/removable workspace AND agent filters; the magnifier/session command remains the general opener and results use existing validated inspection. | R21/R22; N D3/D11; F Q-FE-11. |
| FR-007 | Cutover MUST remove picker/mention switching, old /new and /agents paths, and rename /resume to /sessions across registry/client/help/docs with no old alias. @ suggestions remain unavailable until real addressed messaging; Clear MUST never create a chat or delete saved history. | R20/R22/R23/R47; F Q10; N D11; BS. |
| FR-008 | A persistent label above the feed MUST show Main chat or Extra chat — title from the real kind; task/helper inspection MUST retain its truthful kind. | R24; F Q2; N D3/D8. |
| FR-009 | Session selection/grouping/sending MUST use immutable Session.agent_id; actual message/turn producer identity MUST remain separate, including guest/live/replay responses. No mutable-owner fallback or history copy. | N D3/D5/D8; B D1.1; F Q9. |
| FR-010 | Authenticated AppShell MUST own the existing cross-tab refresh once after picker removal, coordinating roster/member refresh on focus/visibility inside staleTime, existing reconnect, destination entry/expansion and separate membership saves. | N D2; C-SHELL; R4/R47. |
| FR-011 | Failed/incomplete source data MUST show load/error/last-known/unknown and Retry rather than empty team, removed agent, deleted destination, false attention or successful split selection. | N D2–D5/D10/D14; M A6; P Definition of Done. |
| FR-012 | Sidebar attention MUST consume main-only server needs_attention from pending structured question/approval or unseen met/rounds_exhausted/other outcomes; exclude stopped_by_user, extras/helpers and unrelated unread/global/task events. | R29/R47; F Q4/Q4b; N D4; BS C-ATTENTION. |
| FR-013 | Only successful explicit foreground main open MUST request published ack_attention; it acknowledges observed goals for everyone, not pending asks/approvals or newer racing outcomes. Prefetch/reconnect/replay and failed/unauthorized attach MUST not acknowledge. | N D4/D5; BS C-ATTENTION; F Q4. |
| FR-014 | Attention MUST use the locked 18%/1.6 s icon pulse/halo, no row dot, collapsed right-aligned 8 px warning-yellow dot and distinct-main count; meaningful general text stays under reduced motion with zero loops, unknown remains unknown. | R15/R29; N D4/D6/D7. |
| FR-015 | Shared agent identity MUST offer Robot/Man/Woman/Omnipus, default Omnipus, with approved transparent figure and role badge at every named size; no plain-role fallback or app-wide figure preference. | R9 as superseded/R10/R46/R47; F Q5; N D6/D7. |
| FR-016 | Role choices MUST be the exact 31 roles/five groups and approved badge assets; one-time old icon mappings MUST follow the specified obvious mappings, unmatched → General assistant, preserving valid curated roles. | R11; N D6/D9/D10. |
| FR-017 | Choices MUST be the exact ten named palette colors with graphic contrast ≥3:1 on sidebar/chat; one-time stored color mapping MUST use strict saturation <0.25, circular nearest non-Grey hue and stable first-entry ties, invalid/missing → Grey. | R16/R47; F Q8; N D6/D9/D10. |
| FR-018 | Shared identities MUST preserve the specified 26/18/40/48 px surface sizes, sidebar 13 px name, icon-before-name and normal text name color; selected styling MUST not recolor identity to brand gold. All-state text and non-overlapping keyboard/coarse targets MUST remain accessible. | R12/R13/R15; N D6/D7; P design system. |
| FR-019 | AgentIcon and recurring identity/attention/inline-status/activity-pill jobs MUST be published once through catalog/barrel/style/manifest/story/executed evidence, use kit composition and layout-only screen overrides; MUST reuse K FilterMenu/ViewSwitch/HoverCard and avoid duplicates. | R33/R35/R39/R41; N D7; M D1–D6; user overlap instruction. |
| FR-020 | Existing unified create/edit flow MUST add only figure/role/palette choices and the same live preview/global save semantics; draft != saved/activated, and no upload/GIF/new creation interview is added. | R7/R8/R46; F Q6/Q7; N D9. |
| FR-021 | Canonical built-ins, fresh seeding, startup enforcement and existing custom migration MUST retain approved palette/role/figure through repeated backend restart, with protected-field/capability boundaries unchanged. | N D9/D10; F Q5/Q8; DEP-ID. |
| FR-022 | Agent bubbles MUST show actual author name only, zero avatars, across live/replay/virtual/plain paths; guest attribution MUST not switch owner. | R24; F Q9 FINAL; N D8. |
| FR-023 | Inline shared responding-agent icon/name/phrase MUST replace old thinking dots in the same feed slot with locked working/thinking/waiting motion and reduced-motion static text; reuse appropriate old phrases without stale scalar/global state or duplicate composer line. | R14/R15; F Q9 FINAL; N D8. |
| FR-024 | Composer model/Auto/attachment/send/Stop and real correlated errors/replay/first-send safeguards MUST remain; navigation/view closure MUST not cancel work, change control authority or restore companion #1221's obsolete per-agent checkbox. | R20; N D8/D9/D11/D12; P/BS Stop and companion boundaries. |
| FR-025 | Every permitted Sessions row MUST show authoritative Working/Waiting for answer/Done/Failed/Stopped with cause/Interrupted status when available, using a kit status chip; absent state MUST not become an invented running/done claim. | M A1; N D14 narrowed by F Q-FE-11; C-WIRE. |
| FR-026 | Sessions rows MUST show truthful main/extra/helper/task/scheduled kind and title plus muted status/kind/active/tokens metadata, retaining real available date/token information and removing HB; missing values stay honest. | M A3/D4; N D3/D5/D14; B D1.1/D5. |
| FR-027 | Main-session modal rows MUST show the same confirmed warning-yellow attention source as sidebar; metadata browsing MUST not acknowledge it and non-main rows MUST not inherit the main boolean. | M A4; R29; N D4; BS C-ATTENTION. |
| FR-028 | Starting-chat row in Sessions MUST expose its real plan pill/link, associated with the actual starting chat, not internal owner or guessed source; Tasks-only plans MUST retain no fabricated origin. | M A5; N D13; F Q-FE-12. |
| FR-029 | Sessions metadata MUST reconcile status/attention/plan and roster/membership while open, including missed updates/focus/reconnect and work never opened in the browser; reuse existing queries/live mechanisms, not another activity store/socket per row. | M A6; N D2/D5/D14; F Q-FE-11. |
| FR-030 | Main MUST be first/pinned, then extras by recency; helpers MUST **always** remain under their parent, never top-level, in All / Running / Needs me and title/workspace/agent/date searches/filters. Nonmatching parents stay as context. Q-M5 handles unavailable real parents; preserve legitimate Unfiled inspection, protection and virtual/plain reachability. | M A7; latest F Session modal A2/D8 + grouping; R21/R22; N D3/D5/D10/D14. |
| FR-031 | The existing modal MUST use kit flat group/disclosure, AgentIcon, Badge/count, shared row/Item, SearchField and K filter presentation; title Sessions and explanatory subtitle; preserve focus/rename/delete/keyboard/phone actions. Existing native tooltips remain for R45; no new handmade tooltip or view. | M D1–D7; R33/R35/R39/R41/R45; N D7/D11. |
| FR-032 | Overview and panel MUST display truthful distinct counts and source/coverage; missing pages, unattributed verdicts, malformed snapshots or stale data MUST remain visibly partial/unknown/error, not complete or zero. No full-transcript navigation load or eight-item active cap. | M A6; N D5/D13/D14; F Q-FE-11; DEP-ACT. |
| FR-033 | Sessions MUST offer the decided **All / Running / Needs me** filter, composed with title/workspace/agent/date. Running uses authoritative executing state, not queued-as-running. Matching helpers always remain inside their real parent context. Confirmed main attention matches Needs me; non-main matching scope is Q-M6, not a guess or a vote on filter presence. | M A2; F Session modal A2/D8 + grouping; N D14/BS C-ATTENTION; DEP-ACT/Q-M6. |
| FR-034 | Repeated identical consecutive helper siblings under one parent MUST fold into one expandable **N similar helper runs** row, with all original identities/status/attention/Open targets/counts preserved. Never merge executions/cross parents or promote helper summaries to top-level; applies to every filter/search and virtual/plain path. No completed-only restriction. | M D8 as decided by F Session modal A2/D8 + grouping; N D14. |
| FR-035 | Starting-chat plan pill/parent-panel row MUST use real approved/running/phase/pause/progress state; successful/idempotent start results MUST offer authorized Open plan/actual task-session links live and replay, using existing Tasks/Graph/session hand-off with visible unavailable/error, not prose URLs or a new executor. | N D13; F Q-FE-12; DEP-ACT; B D5. |
| FR-036 | Activity side panel MUST show **only the open session's** legitimate plans/task-scheduler runs/helpers/shells; deduplicate real task children, distinguish queued/waiting/executing/terminal, preserve independent-run Stop authority. Other sessions remain in Sessions; shell overview layout waits for Q-M4. | F Q-FE-11; N D13/D14 as narrowed; B D5; DEP-ACT. |
| FR-037 | Backend-owned one-cutover upgrade MUST keep legitimate saved ordinary/extra/Unfiled/child/heartbeat chats reachable and continuable through current contracts, repeat-safe and visibly failed when import fails; frontend MUST not introduce dual readers/type-owner guesses. | F Q11; R47; N D10/D12; BS C-MAIN/E-MIGRATE. |
| FR-038 | Admin's backend-validated default-workspace main MUST be reachable before removal of the old Assets-only entry, without fake membership/other-workspace mains; later qualified addressing remains deferred and grants no new authority. | R25/R47; F Q12; N D5/D10/D11/D12; BS C-ADDRESS/C-MAIN. |

## Success Criteria

These pass/fail criteria apply to the future integrated feature, not to this document's publication. An unresolved choice/contract/wireframe cannot silently pass.

| ID | Measurable outcome / acceptance evidence |
|---|---|
| SC-001 | Every J-01/J-02 destination variant opens the exact validated main/remembered chat or honest unavailable/error; zero recency substitutions, extra creations on entry, split selections or unresolved-target sends. |
| SC-002 | Each of the four main sources signals and each negative source does not; shared observed-goal acknowledgement, unresolved decisions and newer race behavior match J-03 on initial/unopened/reconnect/two-person cases. Missing source/value never passes as false. |
| SC-003 | All four figures, 31 grouped roles, ten colors and named 18/26/40/48 px surfaces conform to W; icon contrast ≥3:1 on both named backgrounds, zero agent-color names or bubble avatars, zero reduced-motion loops. |
| SC-004 | J-06 fresh/custom/built-in palette/role/figure and protected-field identities remain identical after reload and at least two backend restarts; one-time mapping fixtures match the frozen oracle. |
| SC-005 | One Sessions modal offers **All / Running / Needs me**, truthful metadata/main-first ordering and full or explicit partial coverage. Zero top-level helpers in any search/filter. Repeated identical helpers show expandable **N similar helper runs**, exposing all original targets/counts beneath their parent without cross-parent/execution merge. B's panel stays local; A remains reachable in Sessions. |
| SC-006 | Every successful/idempotent supported plan/task start has a validated live/replayed drill-down to the real plan/run; approved is not reported running; zero invented starting chats, duplicate task-child work or independent tree-Stop grants. |
| SC-007 | J-04 real shell/sidebar without AgentPicker refreshes both roster and separately saved membership on focus/visibility/expansion inside the stale window, with visible error/Retry. Direct-hook-only green is insufficient. |
| SC-008 | Every frozen supported saved-install conversation is reopened and continued after migration; former heartbeat history uses main, repeated cutover loses/duplicates zero known content; failed import stays visibly failed. Admin main is exercised before old entry removal. |
| SC-009 | All removed commands/picker/mention-switch paths are unavailable, no old resume alias, zero Clear-created chats/history deletion. Existing model/Auto/attachments, message-delivery/error/replay and Stop scope checks continue to pass. |
| SC-010 | New/reused kit jobs have complete four-part publication and executed required accessibility/screenshot/static-Storybook evidence. At phone/coarse input, targets are at least 44 px and do not overlap; keyboard rename/filter/open/close retains focus and intended result. |
| SC-011 | All 38 FR rows and all BDD scenarios have trace/test coverage, datasets/negative controls are executed on the implementation, applicable remote gates and five reviewers are clean or explicitly deferred by the founder; no zero-discovered-test green. |
| SC-012 | Six DOC pages' specific changes are in the behavior diff and docs-verifier has audited them against the joint build. uat-tester plus independent uat-validator evidence matches the exact landing SHA; founder approves joint delivery with U1. |

## Traceability Matrix

### First-squad requirement → decision → acceptance scenario → test

N means **Agent-first navigation and agent identity**; B means **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**. Source keys resolve through the absolute-path register. Every FR appears, every BDD scenario is mapped, and each test family is defined in the TDD plan. Held choices have explicit decision-guard tests, not assumed product choices.

| Requirement | Source requirement | ADR decision / latest answer | User Story / acceptance | BDD scenario(s) | Test name(s) |
|---|---|---|---|---|---|
| FR-001 | R3/R5/R7/R47 | N D2/D5/D10; F Q12 | US-1.2, US-12.2 | BDD-01.2, BDD-12.2, BDD-E01 | T-01, T-23 |
| FR-002 | R1/R2/R6/R7/R8 | N D1/D2/D11 | US-1.3 | BDD-01.3 | T-23, T-30 |
| FR-003 | R4/R22 | N D3/D5; B D1.1 | US-1.1, US-3.3 | BDD-01.1, BDD-03.3, BDD-E01 | T-01, T-13, T-23 |
| FR-004 | R47/F Q1 | N D3/D12 | US-2.1–4 | BDD-02.1, BDD-02.2, BDD-02.3, BDD-02.4, BDD-E03 | T-02, T-12, T-23 |
| FR-005 | R22/R47 | N D3/D11; F Q10 | US-3.1/3.3 | BDD-03.1, BDD-03.3, BDD-E02 | T-03, T-13, T-23 |
| FR-006 | R21/R22 | N D3/D11; F Q-FE-11 | US-3.2/3.4 | BDD-03.2, BDD-03.4 | T-12, T-13, T-23, T-30 |
| FR-007 | R20/R22/R23/R47 | N D11; F Q10; B D8 | US-7.3 | BDD-07.3 | T-17, T-25, T-30 |
| FR-008 | R24 | N D3/D8; F Q2 | US-7.1 | BDD-07.1, BDD-E07 | T-07, T-17, T-25 |
| FR-009 | R23/R24 | N D3/D5/D8; B D1.1 | US-1.1, US-7.1/7.2 | BDD-01.1, BDD-07.1, BDD-07.2, BDD-E07 | T-01, T-07, T-12, T-17 |
| FR-010 | R4/R47/M A6 | N D2 | US-1.4, US-9.2 | BDD-01.4, BDD-09.2 | T-11, T-23 |
| FR-011 | R47/M A6 | N D2–D5/D10/D14 | US-1.4/2.3/4.4/5.4/6.3/8.4/9.3/10.4/11.3/12.3 | BDD-01.4, BDD-02.3, BDD-04.4, BDD-05.4, BDD-06.3, BDD-08.4, BDD-09.3, BDD-10.4, BDD-11.3, BDD-12.3 | T-10, T-12, T-14, T-16, T-18–T-22, T-29 |
| FR-012 | R29/R47 | N D4; BS C-ATTENTION; F Q4 | US-4.1 | BDD-04.1 | T-04, T-14, T-24 |
| FR-013 | R29 | N D4/D5; BS C-ATTENTION | US-4.2/4.4, US-9.3 | BDD-04.2, BDD-04.4, BDD-E04, BDD-E09 | T-04, T-14, T-24, T-26 |
| FR-014 | R15/R29 | N D4/D6/D7 | US-4.3/4.4 | BDD-04.3, BDD-04.4 | T-14, T-24 |
| FR-015 | R9/R10/R46/R47 | N D6/D7; F Q5 | US-5.1 | BDD-05.1, BDD-E06 | T-06, T-15, T-25 |
| FR-016 | R11 | N D6/D9/D10 | US-5.2/5.3 | BDD-05.2, BDD-05.3, BDD-E05 | T-05, T-06, T-22, T-28 |
| FR-017 | R16/R47 | N D6/D9/D10; F Q8 | US-5.2/5.3 | BDD-05.2, BDD-05.3, BDD-E05 | T-05, T-15, T-22, T-28 |
| FR-018 | R12/R13/R15 | N D6/D7 | US-5.1, US-8.3 | BDD-05.1, BDD-08.3, BDD-E06 | T-06, T-15, T-18, T-25 |
| FR-019 | R33/R35/R39/R41; M D1–D6 | N D7; K overlap | US-5.1, US-8.3 | BDD-05.1, BDD-08.3 | T-15, T-18, T-25, T-26 |
| FR-020 | R7/R8/R46 | N D9; F Q6/Q7 | US-6.1/6.3 | BDD-06.1, BDD-06.3 | T-16, T-25 |
| FR-021 | R11/R16/R47 | N D9/D10; F Q5/Q8 | US-5.3, US-6.2 | BDD-05.3, BDD-06.2 | T-16, T-22, T-25, T-28 |
| FR-022 | R24 | N D8; F Q9 FINAL | US-7.1 | BDD-07.1, BDD-E07 | T-07, T-17, T-25 |
| FR-023 | R14/R15 | N D8; F Q9 FINAL | US-7.2 | BDD-07.2, BDD-E06 | T-07, T-15, T-17, T-25 |
| FR-024 | R20 | N D8/D9/D11/D12; BS | US-3.1, US-7.3/7.4 | BDD-03.1, BDD-07.3, BDD-07.4, BDD-E02 | T-03, T-13, T-17, T-29 |
| FR-025 | M A1 | N D14 + F Q-FE-11 | US-8.1 | BDD-08.1, BDD-E09 | T-18, T-19, T-26 |
| FR-026 | M A3/D4 | N D3/D5/D14; B D1.1/D5 | US-8.1/8.3 | BDD-08.1, BDD-08.3, BDD-E07 | T-07, T-18, T-26 |
| FR-027 | M A4/R29 | N D4; BS C-ATTENTION | US-4.1/4.2, US-8.1 | BDD-04.1, BDD-04.2, BDD-08.1, BDD-E04 | T-14, T-18, T-24, T-26 |
| FR-028 | M A5 | N D13; F Q-FE-12 | US-10.1 | BDD-10.1, BDD-E11 | T-09, T-20, T-27 |
| FR-029 | M A6 | N D2/D5/D14; F Q-FE-11 | US-9.2/9.3 | BDD-09.2, BDD-09.3, BDD-E09 | T-11, T-19, T-26 |
| FR-030 | M A7/R21/R22; latest F grouping | N D3/D5/D10/D14 + F always-under-parent | US-8.2, US-9.1/9.4/9.5 | BDD-08.2, BDD-09.1, BDD-09.4, BDD-09.5, BDD-E08 | T-08, T-18, T-19, T-26 |
| FR-031 | M D1–D7/R33/R35/R39/R41/R45 | N D7/D11 | US-8.3/8.4 | BDD-08.3, BDD-08.4 | T-18, T-26, T-30 |
| FR-032 | M A6/N activity coverage | N D5/D13/D14 + F Q-FE-11 | US-9.3/9.4, US-11.3 | BDD-09.3, BDD-09.4, BDD-11.3, BDD-E09 | T-10, T-19, T-21, T-26, T-29 |
| FR-033 | M A2 **decided** | N D14 + F Session modal A2/D8 + grouping; only extra non-main scope Q-M6 | US-9.1 | BDD-09.1, BDD-E08 | T-08, T-19, T-26 |
| FR-034 | M D8 **decided** | N D14 + F Session modal A2/D8 + grouping | US-9.4/9.5 | BDD-09.5, BDD-E08 | T-08, T-19, T-26 |
| FR-035 | #1021/M A5 | N D13; F Q-FE-12 | US-10.1–4 | BDD-10.1, BDD-10.2, BDD-10.3, BDD-10.4, BDD-E11 | T-09, T-20, T-27 |
| FR-036 | #493/BS activity | N D13/D14 + F Q-FE-11; B D5 | US-11.1–3 | BDD-11.1, BDD-11.2, BDD-11.3, BDD-E10 | T-09, T-21, T-26, T-27, T-29 |
| FR-037 | R47/F Q11 | N D10/D12; BS migration | US-12.1/12.3 | BDD-12.1, BDD-12.3, BDD-E12 | T-22, T-28 |
| FR-038 | R25/R47/F Q12 | N D5/D10/D11/D12; BS C-MAIN/C-ADDRESS | US-12.2 | BDD-12.2 | T-22, T-23, T-30 |

### Modal-review disposition — every A/D item

| Review item | Requirement / decision | Scenario → test / disposition |
|---|---|---|
| M A1 status | FR-025; N D14 narrowed by F Q-FE-11 | BDD-08.1 → T-18/T-26; exact lifecycle/source kept. |
| M A2 All / Running / Needs me | FR-033; **decided by latest F**, presence/labels no longer open | BDD-09.1/E08 → T-08/T-19/T-26; matching helper always inside parent; Q-M6 only additional non-main source scope. |
| M A3 kind | FR-026; N D5 | BDD-08.1 → T-18/T-26; main/extra/helper/task/scheduled, no HB. |
| M A4 waiting main | FR-027; N D4 | BDD-08.1/04.1/04.2 → T-14/T-18/T-24/T-26. |
| M A5 plan | FR-028; N D13/F Q-FE-12 | BDD-10.1/E11 → T-09/T-20/T-27. |
| M A6 live updates | FR-029/FR-032; N D2/D14 | BDD-09.2/09.3/E09 → T-11/T-19/T-26. |
| M A7 main order + F strict helper grouping | FR-030; N D3/D5 with latest F always-under-parent rule | BDD-08.2/09.1/09.5/E08 → T-08/T-18/T-19/T-26; zero top-level helpers in any filter/search. |
| M D1 group controls | FR-031; R35/N D7 | BDD-08.3 → T-18/T-26; shared flat disclosure/list grouping, real tree kept. |
| M D2 agent marker | FR-015/FR-018/FR-031; N D6/D7 | BDD-05.1/08.3 → T-15/T-18/T-25. |
| M D3 count pill | FR-019/FR-031; N D7 | BDD-08.3 → T-18/T-26; kit Badge. |
| M D4 row metadata | FR-026/FR-031; N D14 | BDD-08.1/08.3 → T-18/T-26; shared row/title/meta, phone checks. |
| M D5 tooltips | FR-031; N D11/R45 | BDD-08.3 → T-18/T-26; **defer existing native-title migration**, add no handmade tooltip. |
| M D6 search/date/filter | FR-019/FR-031; R33/R39/K | BDD-08.3/09.1 → T-18/T-19; SearchField + shared filter presentation, no duplicate FilterMenu. |
| M D7 title | FR-031; N D11 | BDD-08.3 → T-18/T-26/T-30; Sessions + clear subtitle. |
| M D8 repeated helpers | FR-034; **decided by latest F** | BDD-09.5/E08 → T-08/T-19/T-26; expandable `N similar helper runs` under parent, every original target/count/state preserved; not completed-only or an execution merge. |

### Source R1–R47 disposition, including subrequirements

“In scope” means the traced first-squad part; “Preserve” does not authorize a redesign. Later items carry no fabricated current implementation scenario/test. N D1/D11 record them; qualitative non-behaviors prevent scope creep.

| Source requirement | First-squad trace or explicit later disposition |
|---|---|
| R1 | FR-002 → N D2 → BDD-01.3 → T-23/T-30; preserve no Chat entry. |
| R2 | FR-002 → N D2 → BDD-01.3 → T-23/T-30; preserve existing panels/global versus workspace Library. |
| R3 | FR-001/FR-003 → N D2/D3 → BDD-01.1/01.2 → T-01/T-23. |
| R4 | FR-003/FR-004 → N D3/D5 → BDD-01.1/02.1/02.2 → T-01/T-12/T-23. |
| R5 | FR-001 → N D2 → BDD-01.2 → T-01/T-23. |
| R6 | FR-002/FR-015 → N D1/D2/D7 → BDD-01.3/05.1 → T-15/T-23; roster access/shared identity only. |
| R6a | Later Agents-feed redesign; N D1/D11. Shared identity is FR-015/FR-018, not full layout/search regrouping. |
| R6b | Later creation interview/Kind→Role→membership redesign; N D1/D11. FR-020 extends current flow only. |
| R6c | Later roster membership card layout; N D1. No first-squad hover-card duplicate. |
| R7 | FR-001/FR-002/FR-020 → N D2/D9 → BDD-01.2/06.1 → T-01/T-16. |
| R8 | FR-020/FR-021 → N D9 → BDD-06.1/06.2/06.3 → T-16/T-25. |
| R9 | Superseded narrowly by F Q5's minimal-eye four figures; FR-015 → N D6 → BDD-05.1 → T-06/T-15. No unrelated character art. |
| R10 | FR-015/FR-018 → N D6/D7 → BDD-05.1/E06 → T-15/T-25. |
| R11 | FR-016 → N D6/D9/D10 → BDD-05.2/05.3/E05 → T-05/T-06/T-22. |
| R12 | FR-018/FR-022 → N D6/D8 with F Q9 bubble exception → BDD-05.1/07.1 → T-15/T-17. |
| R13 | FR-018/FR-023 → N D6/D8 → BDD-05.1/07.2/E06 → T-15/T-17/T-25. |
| R14 | FR-023 → N D8/F Q9 FINAL → BDD-07.2 → T-17/T-25. |
| R15 | FR-014/FR-018/FR-023 → N D4/D8 → BDD-04.3/07.2/E06 → T-14/T-15/T-25. |
| R16 | FR-017/FR-021 → N D6/D9/D10/F Q8 → BDD-05.2/05.3/E05 → T-05/T-22/T-28. |
| R17 | Deferred avatar upload unit by F Q6; N D9/D11 retains safety obligations, no current upload scenario or endpoint. |
| R18 | Deferred with GIF/placement decision F Q7; old playback proposal not silently adopted. N D9/D11. |
| R19 | Deferred/superseded status-placement assumptions under F Q7/Q9. N D8/D9/D11; no image activity lane now. |
| R20 | FR-007/FR-024 → N D8/D11 → BDD-07.3/07.4 → T-17/T-29. |
| R21 | FR-006/FR-030/FR-031 → N D3/F Q-FE-11 → BDD-03.2/08.2/08.3 → T-13/T-18/T-26. |
| R22 | FR-003/FR-005/FR-006/FR-007 → N D3/D11 → BDD-03.1/03.2/03.3/07.3 → T-13/T-17/T-23. |
| R23 | Immediate removal is FR-007/FR-009; full qualified @ UI later, N D11. F Q9 overrides reply bubble icon wording with actual name only. |
| R24 | FR-008/FR-022/FR-023 → N D3/D8/F Q2/Q9 → BDD-07.1/07.2 → T-17/T-25. |
| R25 | FR-038 → N D5/D10/D11/F Q12 → BDD-12.2 → T-23. Reach replacement before old entry removal. |
| R26 | Backend/later explicit workspace+agent peer messaging; N D11/BS C-ADDRESS. No cross-workspace plans/new frontend authority. |
| R27 | Backend canonical task-family/cross-workspace read changes; BS latest amendments supersede old duplicate-tool names. No new permission/editor scope here; N D11. |
| R28 | Backend eligibility/policy responsibility; latest BS native-worker self/task rules win over broad older wording. Sidebar workers stay absent FR-001; frontend never grants tools. |
| R29 | FR-012/FR-013/FR-014/FR-027 → N D4/BS → BDD-04.1/04.2/04.3/08.1 → T-14/T-24/T-26. |
| R30 | Later Skills & Tools/global policies wave; N D1/D11. |
| R30a | Later tool-specific settings/provider moves; N D1/D11. |
| R30b | Later external-tool defaults/bulk/preset removal; N D1/D11. No reintroduced Unset or agent policy layer. |
| R31 | Later tool/skill loading levels and unified discovery; N D1/D11. Retired switch_agent is not preserved by old locked-tool list. |
| R32 | Later skill categorization/install UI; N D1/D11. No classifier or skill-file rewrite here. |
| R33 | FR-019/FR-031 → N D7/M D6 → BDD-08.3 → T-18/T-26; Sessions SearchField only, no app-wide swap or undecided clear shortcut. |
| R34 | Later Connectors wave; N D1/D11. |
| R35 | FR-019/FR-031 → N D7/M D1 → BDD-08.3/09.4 → T-18/T-19; kit group styling/defaults/search reveal, not loss of real nested tree. |
| R36 | Preserve existing main actions; broader pages later N D1. No modal/page conversion of the founder-selected Sessions modal. |
| R37 | Preserve Agents/New agent naming in current roster/flow; N D1/D2/D9. Full roster action redesign later. |
| R38 | Companion/broader page shell behavior; N D1/D11. Existing drawer/header access preserved FR-002. |
| R39 | FR-019/FR-031/FR-033 → N D7/M D6/K → BDD-08.3/09.1 → T-18/T-19. Reuse shared filter/view jobs, no redundant view tabs. |
| R40 | Central page-title foundation/later app-wide adoption; N D1/D11. This modal spec does not invent a page-size override. |
| R41 | FR-019/FR-031 → N D7 → BDD-05.1/08.3 → T-15/T-18. Only commissioned shared jobs, not full app migration. |
| R42 | Later low-priority lock/check hardening, N D1/D11. No checks weakened or new ratchet invented. |
| R43 | Separate rollout waves, N D1/D11. This spec is one first-squad feature, not all phases. |
| R44 | Separate measured-space header fix, N D11; preserve companion output, do not duplicate it. |
| R45 | Separate shadcn Tooltip migration, N D11/M D5; FR-031 adds no handmade tooltip/old-props wrapper. |
| R46 | FR-020/FR-021 → N D9/F Q5/Q6/Q7 → BDD-06.1/06.2/06.3 → T-16/T-25. Upload subset explicitly deferred. |
| R47 | FR-004/FR-007/FR-015/FR-017/FR-037/FR-038 plus activity FR-028–FR-036 → N D3–D14/F latest answers → corresponding matrix rows above. |

## User-facing Documentation TODOs

These existing pages were read. Implementing leads draft matching updates **in the same behavior change**; docs-verifier independently checks factual accuracy against the exact joint build. Team-lead coordinates page overlap with session-core/Tasks/#1221/header/Tooltip owners. This design-only commit changes none of these user pages and makes no delivered-behavior claim.

| ID | Absolute page / affected existing sections | Specific first-squad update and acceptance link |
|---|---|---|
| DOC-001 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/using-omnipus-ui.md`::The sidebar, The chat area, If the first message loses its connection, Stop and redirect commands | Replace sidebar conversation-tree/More/picker instructions with agent main click, exact restore/Ava fallback, Past sessions/+ New chat, /sessions and no old aliases/@ switching. Explain above-feed Main/Extra, actual name-only replies/inline responder, main-only question/approval/goal attention and shared observed-goal acknowledgement, reduced motion and stale/Retry. Move /new delivery-loss advice to the row New chat action. Distinguish open-chat panel from overall Sessions; document the **decided All / Running / Needs me** filter, helpers always under their parent even on helper-only matches, and expandable **N similar helper runs** with all original targets. Add actual plan/run links; unavailable parent/non-main attention scope follow the recorded narrow answers. FR-002–FR-014/FR-022–FR-036; T-30. |
| DOC-002 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/agents.md`::The base agents, How to create your own agent, default/edit/worker sections | Document four figures/Omnipus default, exact grouped roles/palette, one-time migration and shared global preview/save with protected built-ins stable on restart. Replace mutable handover wording; no avatar upload/GIF feature promised. Explain main versus extra/worker inspection and Admin default main. Preserve existing current create/edit flow and global-default meaning, which is not the Ava welcome destination. Companion owner removes obsolete per-agent Auto checkbox guidance, not this identity work. FR-009/FR-015–FR-024/FR-038; T-25/T-30. |
| DOC-003 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/workspaces.md`::What it is, panels, create/membership/freshness sections | Workspace expansion lists eligible colleagues, pair-specific mains and exact last-chat/default entry; explain extra/history, member hide/unhide and one-cutover continuation/heartbeat→main guarantee. Replace picker-owned refresh instructions with shell/sidebar roster+membership focus/expansion recovery and honest last-known/Retry. Admin lives in default workspace without team membership; later qualified collaboration never implies cross-workspace plans or a frontend permission grant. FR-001–FR-011/FR-037/FR-038; T-23/T-28/T-30. |
| DOC-004 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/plans.md`::What it is, state table, create/run and limits sections | Explain starting-chat pill/modal-row/parent-panel association, Tasks-only no-origin, accepted Approved versus actual Running, real phase/pause/progress, successful start-result Open plan and existing Tasks/Graph drill-down live/replay. No new plan engine, no guess that internal owner is the starting chat. FR-028/FR-035; T-27/T-30. |
| DOC-005 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/tasks.md`::How to create and start a task, If the task conversation cannot be created, task detail/run history | Explain actual task-run/session links from successful starts and Activity/Sessions, including scheduler-only work, real source/workspace, task-child dedupe and monitoring versus independent-run Stop authority. Preserve truthful refused-start behavior; coordinate backend's updated task/recipient modes, not a new scheduler/Schedules page. FR-032/FR-035/FR-036; T-27/T-30. |
| DOC-006 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/getting-started.md`::How to go from nothing to your first conversation, step 7 | This page still tells a new user to choose the composer agent picker. Replace that instruction with eligible sidebar colleague/main selection and row New chat/Past sessions; coordinate first-workspace/onboarding wording with U1 instead of claiming the generic Ava entry rule replaces every onboarding step. FR-003/FR-005/FR-007; T-23/T-30. |

Missing any matching page update in the implementation diff is an Important finding under P, with the concrete impact that a user follows removed picker/command/standalone-Admin instructions or mistakes another chat's activity/control scope. Public docs describe observable behavior, not unpublished wire fields. No unrelated page rewrite is authorized by these TODOs.

## Questions for the founder

**Team-lead interviews the founder using the question tool.** This author does not choose the answers or start an extra ADR grill. Q-FE-11/Q-FE-12, the original Q1–Q12, and the later **Session modal A2/D8 + grouping** answer are closed. **Decided:** helpers always remain under their parent in every search/filter, the exact filter choices are All / Running / Needs me, and repeated identical helpers fold into an expandable `N similar helper runs` row. Former Q-M1/Q-M2 and the alternative filter-name choice Q-M3 are retired, not re-interviewed. Only the narrower unresolved cases below remain; answer form: `Q-M4 A, Q-M5 A, Q-M6 A`. Recommendations are not decisions. Missing backend serialization is handled in the dependency ledger, not turned into guessed wire fields.

### Q-M4 — How does Sessions expose background shells that are not chat sessions? (A / B / C)

N D14 includes shells, but the stored session tree does not make each shell process a real Omnipus chat. F Q-FE-11 selects the existing modal as overall activity. A main can have finished its own turn while a background shell still runs; its own lifecycle chip alone would hide that work. The needed presentation is missing from W and requires authoritative origin-scoped metadata for unopened chats.

| Option | Presentation within the existing modal |
|---|---|
| A | Show real shell activity/count on its origin chat's row; an accessible Open action reaches that origin chat's existing Activity panel. No synthetic session row. |
| B | Expand origin chat into clearly marked **activity entries**, including shells as non-session entries distinct from genuine helper/run children, with existing authorized inspection actions. |
| C | Keep shell detail only after opening the origin chat; the modal identifies the origin's background-work presence but lists no shell count/detail. This deliberately limits the overview. |

**Recommendation: A.** It keeps the one existing hierarchy and makes ongoing shell work discoverable without faking a saved session or building another view. DEP-ACT and WF-05 remain holds until the answer and real data coverage are published.

### Q-M5 — Where is a helper whose real parent cannot be loaded? (A / B)

The newest answer forbids top-level helper rows **always**, superseding the current modal's orphan-as-root fallback. A retained helper can still have a missing/deleted or inaccessible parent. This question does not reopen nesting: it chooses the honest unavailable-parent treatment without inventing a new parent, leaking metadata or silently losing legitimate retained history.

| Option | Treatment — helper never becomes top-level |
|---|---|
| A | Keep the helper beneath a clearly unavailable **real-parent context**, using only authorized relation data; no fake navigable parent session or exposed hidden title. Its legitimate helper inspection remains reachable. |
| B | Show a visible parent-resolution/availability notice and defer its modal row until real authorized parent context is available; retain legitimate independent inspection through existing routes. |

**Recommendation: A.** It preserves the saved-helper reachability guarantee without breaking the always-nested rule. Backend owner must provide truthful authorized parent context; WF-01 covers this case.

### Q-M6 — Which non-main rows, if any, match the already-approved Needs me filter? (A / B)

The **presence and label of Needs me are decided**. Its broadening beyond mains is not defined by the latest answer: BS publishes needs_attention only on mains, while helpers may be waiting for a parent rather than for the person. This is a matching-source question, not another vote on A2; it cannot be answered by inventing a helper boolean or parsing text.

| Option | Matching rule |
|---|---|
| A | Match confirmed main needs_attention only; any matching helper context is shown under its parent through other filters/search. |
| B | Also match non-main sessions with an authoritative unresolved action addressed to the person, not generic waiting. Backend owner publishes/reuses exact question/approval ownership metadata first; sidebar remains main-only. |

**Recommendation: A** for this first squad, because that source is already settled and mains carry person-facing attention. If the founder chooses B, existing authorized pending-question/approval data must establish the additional source; no new guessed wire field is permitted.

### Visual/publication holds, not additional guesses

WF-01–WF-05 require additions to the approved wireframe project and founder approval; DEP-ID/DEP-ACT require the owners' committed generated shapes and canonical identity inventory. Team-lead arranges these with the relevant owners. Existing approved Q9 design, figures/default, main/extra/entry semantics and main-only attention are not reopened here. Prototype color fixtures are presented for Q8's requested review, not a new mapping algorithm choice.

## Ambiguity Warnings

The founder has **not yet reviewed or acknowledged** these warnings in this task. Under plan-spec's ambiguity gate, this artifact remains a reviewable **Draft**, not a finalized implementation-ready spec. Team-lead records each answer/accepted deferral before dependent production work; the two formal spec grill/fix rounds still follow P's founder-interview rules. No second ADR correction/grill is commissioned here.

| ID | What's ambiguous / missing | Likely agent assumption — forbidden until resolved | Question / owner and current state |
|---|---|---|---|
| AW-01 | Matching sources beyond the **decided** All / Running / Needs me filter | Treat every waiting helper as a person-action row | Q-M6; Pending narrow source-scope interview, not A2's presence/label. |
| AW-02 | Missing/inaccessible real parent under the **decided always-nested helper rule** | Keep the old top-level orphan fallback or invent a parent | Q-M5; Pending unavailable-parent treatment; top-level helper is never allowed. |
| AW-03 | Authoritative executing-versus-queued metadata for the **decided Running label** | Treat every current Working response as executing | DEP-ACT; Backend publication dependency, no open filter-name choice. |
| AW-04 | Shells in the overall session-based view | Drop shells from overview or mint fake chat rows | Q-M4; Pending founder interview; WF-05/DEP-ACT. |
| AW-05 | Per-agent figure persistence and canonical built-in role/color/figure inventory | Add a guessed Agent property/local store or choose seed values by name | DEP-ID; Pending backend identity owner publication. UI-only kit fixtures may proceed, persistence consumers may not. |
| AW-06 | Real plan start origin, result-navigation contract and unopened run/shell metadata coverage | Assume owner/transport source is start chat; parse prose URL; treat loaded buckets as all work | DEP-ACT; Pending generated-contract/snapshot evidence. F Q-FE-12 is settled, field names are not invented. |
| AW-07 | Redesigned modal/plan-result/editor/error visuals absent from approved reference | Copy another prototype/current screenshots as approved new design | WF-01–WF-05; Pending additions/approval, no wireframe edit in this task. |
| AW-08 | Shared kit final interfaces/evidence; HoverCard not yet pushed at observed tip | Implement local FilterMenu/ViewSwitch/HoverCard or report file presence as publication | DEP-KIT; Pending Tasks-panel owner hand-off. |
| AW-09 | Main/attention/ack contract shape is documented but not in checked schema snapshots | Handwrite fields/default to false and land without U1 | DEP-U1/DEP-ATT; Pending atomic schema/generated artifacts and real joint tests. |

**No fabricated assumptions:** no new backend URLs, JSON properties, nominal wire types, budgets, role-policy grants, avatar storage or UI collapse/filter semantics are filled in. Recommendations above are not approvals. The draft can be published for the interview while final approval remains held; settled design rules are not blocked from independent test/kit preparation by unrelated unanswered choices.

## Evaluation Scenarios (Holdout)

**Holdout — post-implementation only.** Team-lead excludes this section from implementing-agent briefs/spec extracts and keeps it for the founder/independent evaluator; this task creates only the one canonical spec. These scenarios are not development tests and appear in neither the TDD plan nor traceability matrix. Evaluator supplies previously undisclosed names, message contents, retained data and race ordering, observes the real application externally and records screenshots/results. No source-code inspection as a substitute for outcomes.

### Holdout H-01 — Choose the intended colleague after newer parallel work
**Category:** Happy Path
- **Setup**: Two authorized workspaces share a colleague and contain newer extras with evaluator-chosen similar titles.
- **Action**: Select a colleague row, send a distinct message, leave and return to the workspace.
- **Expected outcome**: Row selection reached its validated main; workspace return restored the exact last chat, not the globally newest conversation. The message was received only at the intended pair.

### Holdout H-02 — Find other-chat work through Sessions, not the local panel
**Category:** Happy Path
- **Setup**: Real plan/task/helper/scheduler work starts in A; B is foreground. Include repeated identical helper titles and a child-only search term chosen by evaluator.
- **Action**: Inspect B's panel, then find A's work with All / Running / Needs me and search, expand the repeated-helper summary and follow real links.
- **Expected outcome**: B's panel contains only its legitimate work; A stays findable in the one Sessions modal. Matching helpers always remain under their parent; the folded summary exposes every original identity/target; the plan belongs to its real starting chat and Open reaches the real plan/run.

### Holdout H-03 — Recognize saved identity and actual responder
**Category:** Happy Path
- **Setup**: Editable custom identity plus locked built-in, real guest responder distinct from chat owner, normal and reduced-motion browser settings.
- **Action**: Edit identity through the current flow, reload/restart the candidate and read live then saved replies.
- **Expected outcome**: Shared figure/badge/color/preview stays consistent and restart-stable; locked identity stays locked. Replies are names-only and inline indicator shows the actual responder; reduced motion stops loops without losing meaning.

### Holdout H-04 — Recover a refused destination without sending into split state
**Category:** Error
- **Setup**: A is committed; evaluator makes B's destination/attachment inaccessible or unavailable during selection.
- **Action**: Attempt B, inspect the error and retry after legitimate recovery.
- **Expected outcome**: Visible failure/retry, no successful-looking workspace/owner mismatch or unresolved-target message; recovery uses the original intended destination rather than a guessed/new chat.

### Holdout H-05 — Expose incomplete overview or invalid start target
**Category:** Error
- **Setup**: Work exists in an unopened session; evaluator denies a later metadata page or removes authorization to a saved plan/run target.
- **Action**: Open Sessions and use the affected result/link.
- **Expected outcome**: Partial/unavailable/refusal is explicit, no all-idle/zero/completeness claim or fabricated URL/foreground substitute; still-authorized work remains inspectable and recovery preserves scope.

### Holdout H-06 — Preserve a newer main goal across another person's open
**Category:** Edge Case
- **Setup**: Two authorized people view one main with pending question/approval and evaluator-triggered goals bracketing a captured explicit-open boundary.
- **Action**: One person opens the main while the newer outcome arrives; the other observes navigation/modal.
- **Expected outcome**: Observed finished/failed goal acknowledgement is shared, pending decisions/newer outcome remain attention-worthy, and searching/reconnecting never acknowledges them by itself.

### Holdout H-07 — Continue retained helper history with strict parent context after upgrade
**Category:** Edge Case
- **Setup**: Supported saved ordinary/extra/Unfiled/child/heartbeat histories and legacy identities, including a retained helper with unavailable parent context treated according to the later founder answer.
- **Action**: Upgrade, reopen/continue histories, search/filter the helper, and repeat restart/upgrade.
- **Expected outcome**: Known content/binding and actual continuation survive without duplication or empty-success failure. Helpers never become top-level rows; unavailable parent treatment is honest and approved. Main/heartbeat and canonical identities remain stable.
