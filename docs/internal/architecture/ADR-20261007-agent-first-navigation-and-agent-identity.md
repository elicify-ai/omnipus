# ADR-20261007 — Agent-first navigation and agent identity

**Correction — 2026-10-07, the sole correction round:** at `554d21ffd` this ADR required avatars on messages, static chat icons and preservation of the old thinking indicator. Those rules are superseded by founder Q9: names-only bubbles and an animated indicator inline in the feed replace the old indicator; no composer status line. Founder Q5 selects Omnipus as default; Q6/Q7 defer avatar uploads/GIFs. The one review also exposed missing canonical seeding, refresh ownership and workspace-entry rules, addressed below. WIP checkpoints belong to this same correction round, not additional review/fix rounds.

**F — binding founder answers:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/FOUNDER-ANSWERS-20261007.md`::Q1–Q12, plus the direct team-lead steering in this correction conversation. Latest steering fixes Q9’s inline placement and approves `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/index.html` as the Q9 visual reference. The screenshots under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/` are prototype evidence, not product acceptance.

**Review:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/ADR-20261007-agent-first-navigation-and-agent-identity-review.md`::MAJ-001–MAJ-004, MIN-001–MIN-002. Reviewed commit `554d21ffd6963def6fa937865cb2b1e3df86f3c3`; exactly one grill and this one correction. Founder answers override review recommendations.

| Header | Value |
|---|---|
| Status | **Proposed** — binding founder answers folded in; only Q-FE-11/Q-FE-12 remain frontend product choices. No implementation-ready or delivery claim for their dependent activity behavior. |
| Date | 2026-10-07 (UTC); sole correction after the founder interview. |
| Decider | Daniel Piatkowski. Open-question recommendations are not decisions. |
| Author | Architect. |
| Size and scope | Feature-size frontend design: navigation/restore/freshness, shared figure identity, inline animated indicator and name-only bubbles, editor preview, plan/start-link visibility and cross-session activity. No first-squad avatar uploads/GIFs. |
| Baselines | Reviewed ADR `554d21ffd6963def6fa937865cb2b1e3df86f3c3`; production sources remain release `c6837a42dcfb503fb34a0cea86cd3ccc451e7b9b`. Work branch `work/adr-frontend-navigation-20261007`. |
| ID | Existing ADR identity retained; no rename or new ADR minted in this correction. |
| Review boundary | Exactly one ADR grill, founder interview and this one correction round. WIP commits preserve this same round and are not squashed. No second grill or further author edits after publication without team-lead’s request; unresolved choices go to the founder. No code/specification/contract edit or landing authorization. |

## Context

The sidebar should answer **“who do I want to talk to, in which workspace?”**, not **“which thread was recently active?”** Clicking Mia opens Mia’s main session in that workspace. **Past sessions** finds older conversations; **+ New chat** deliberately starts a parallel one. Each agent keeps the same visual identity across the sidebar, Team, roster, messages and editor. Chat remains the main surface, with workspace tools opening the existing side panels. These are the revised requirements, not a new conversation store or a new agent-management system (R1–R8, R20–R24, R46).

This is a **design correction**, not production work. R1–R46 were traced to S1; “R-figures” is shorthand for §5 question 15 plus R46, not a fabricated requirement ID. F now answers the figure/default choices and approves the updated Q9 wireframe. The only new unanswered frontend choices concern activity scope and plan association, Q-FE-11/Q-FE-12 (F::Q5/Q9; latest wireframe-approval steering).

### Sources and precedence

The source keys below expand to absolute paths. Requirement citations throughout this ADR refer to **S1**; evidence keys E1–E12 expand to the exact `file::symbol` citations in the next table.

| Key | Source and authority |
|---|---|
| S1 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/requirements.md` — R1–R46, §5 open questions, §7 documentation TODOs and §9 identity requirements. Revised requirements win over the prototype. |
| S2 | **Founder-approved Q9 visual reference:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/index.html`::chat-feed, chat-label, think-preview, fig-switch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/app.js`::messageHtml, renderChat, renderThinkPreview, renderWaitDemo; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/assets/agent-marks.js`::agentIcon, figureInner, LOGO_CSS, PALETTE, toPalette; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/assets/person-icons-data.js`::PERSON_ICONS. The existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/components.html` gallery remains component context. Q9 screenshot positions/default/responder identity were inspected; demos are not live execution or production acceptance. |
| S3 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/joint-delivery-plan-session-core.md`::Split of work, Order, Rules both sides follow. A draft joint plan; the direct frontend commission supersedes its “each step lands on its own” wording for this first squad. |
| B | **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md` at **`b76b412f61ff73d9a6643d34518224472d84223a`**::D1.1, R-ACTIVITY/D5, D8/D10, DEL-F01–F43. Verified immutable contract baseline. The subsequent dated migration/Admin/cross-workspace amendments and settled 17:45 waiting/task decisions are binding via F/S4, not reopened from conflicting b76 text. |
| B-spec | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/specs/session-core-spec.md` — existing Draft, being amended/pushed. The latest waiting direction is explicitly confirmed by team-lead: `Session.needs_attention`, optional/readOnly, boolean on every main, omitted elsewhere. **Updated spec SHA pending** until verified; no fabricated publication/implementation claim. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/session-core-adr-20261007/QUESTIONS.md`::Q7/Q8 CLOSED records the 17:45 answers. |
| I1021 / I493 | https://github.com/elicify-ai/omnipus/issues/1021 and https://github.com/elicify-ai/omnipus/issues/493 — bodies and comments read with `gh issue view`, including the founder’s 2026-10-07 hand-over. New frontend visibility scope, not authority for old unrelated execution findings. |
| S4 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/CONTINUATION-20261005.md`::2026-10-06/07 founder entries, especially main-session mechanism, 12:44 navigation answers, 14:10 clear/hide decisions and the frontend/sidebar commission. Later founder answers supersede earlier draft language. |
| S5 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/CLAUDE.md`::Definition of Done, Hard Constraints, Spec-Driven Workflow; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/.claude/skills/omnipus-design-system/SKILL.md`::Publishing a component is a four-part contract, A recurring UI job uses a catalogued component. Governs delivery, generated contracts and component publication. |
| S6 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/AS-IS-architecture.md`::Contract-first wire-format pipeline; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/plugin-extensibility-assessment.md`::Side-by-side scorecard. Dated architecture context; current code and contracts win. No extension-runtime change is proposed here. |

The backend specification now exists as B-spec, superseding the first draft’s absent-spec observation. Its latest agreed waiting shape is consumed as a **design dependency**, not as generated-contract/runtime delivery. Backend U1 denotes S3’s main-session/addressing unit; document publication alone does not mean U1 works or may land separately (B::D1.1/D10; F’s joint-delivery instruction).

### Verified frontend reality

| Key | Current fact | Evidence read at the baseline |
|---|---|---|
| E1 | The sidebar expands workspaces into recent session trees, with a workspace-level New chat action. Assets already contains Agents and a separate Admin chat entry. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/layout/Sidebar.tsx`::Sidebar, WorkspaceSessionTree, SidebarSessionRow, ASSET_ITEMS. |
| E2 | Session selection already attaches the selected session, handles attach failure visibly, and navigates to workspace chat or the standalone session route. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/useSelectSession.ts`::useSelectSession, attachAndSeed, reportAttachFailure. |
| E3 | Session search already has workspace/agent groups, nested children and an Unfiled path. Its opening state accepts a workspace filter but no agent filter. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/search/SearchModal.tsx`::SearchModal, AgentHeader, AgentSessionList; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/ui.ts`::UiStore, useUiStore.openSearchModal. |
| E4 | The composer still renders an AgentPicker. Selecting an `@` suggestion currently changes the agent and clears the input. `/resume` opens session search; `/agents` opens the picker. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ChatScreen.tsx`::OmnipusComposer; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useSlashMenu.ts`::useSlashMenu, selectMentionAgent, runClientCommand. |
| E5 | Live and historical message rendering already resolves each message’s agent separately. Both draw filled-circle avatars; the historical path duplicates the avatar markup. The feed has its own thinking indicator. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ChatScreen.tsx`::AssistantMessageAvatar, AssistantMessage, VirtualAssistantMessageRow, ThinkingIndicator, InlineThinkingIndicator. |
| E6 | The existing create identity step and profile share color/icon controls. AvatarHeader always selects Robot, regardless of the edited icon. Profile identity edits use server-provided field editability. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/agents/AgentFormFields.tsx`::AvatarHeader, AvatarColorPicker, IconPicker; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/agents/wizard/Step1Identity.tsx`::Step1Identity; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/agents/AgentProfile.tsx`::AgentProfile. |
| E7 | The current eight-color palette includes brand/semantic colors that R16 excludes. Hex values are the stored identity data; color names are presentation. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/lib/constants.ts`::AVATAR_COLORS, AVATAR_COLORS_BY_NAME, avatarColorName. |
| E8 | Worker detection already recognizes native and external workers. Chat eligibility distinguishes the visible core colleagues from hidden engine agents; Admin has a deliberate team-scoping exception. Roster refresh already runs on WebSocket connect/reconnect, independently of the picker mounting. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/lib/api/agents.ts`::isWorker; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useChatAgents.ts`::isChatEligibleAgent, isStandaloneChatOperator, useChatAgents; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/connection.ts`::useConnectionStore.setConnected. |
| E9 | Avatar, AvatarImage and AvatarFallback are already published kit primitives. AgentIcon is not in the current catalog. Avatar currently clips into a circle, so its existing visual behavior cannot be imposed unchanged on transparent figure glyphs. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/ui/avatar.tsx`::Avatar, AvatarImage, AvatarFallback; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/design-system/catalog.json`::entries; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/index.ts`::Avatar exports; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/styles/library.css`::Avatar source registration. |
| E10 | Current member/session schemas describe heartbeat sessions, not the new main-session contract. Agent exposes color/icon, but no separate figure or uploaded-image identity. Session lifecycle state is a session/helper projection, not a workspace-wide “this agent awaits this user” guarantee. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/WorkspaceMemberConfig.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/WorkspaceMemberHeartbeat.yaml`::session_id; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/Session.yaml`::type, lifecycle_state; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/Agent.yaml`::properties. |
| E11 | Workspace tools already toggle the shared side panels, with no Chat entry. The current compact header uses a second menu-icon trigger. Tooltip still has its hand-built interface. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/workspaces/WorkspaceTabBar.tsx`::WorkspaceTabBar, WORKSPACE_TABS, PANEL_TOGGLE_SEGMENTS; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/ui/tooltip.tsx`::Tooltip. |
| E12 | Team and activity surfaces have existing agent-avatar rendering to replace, not new teams or activity ownership to invent. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/workspaces/team/WorkspaceTeamGraph.tsx`::AgentNode; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ActivityAvatar.tsx`::ActivityAvatar. |

**Precedence and supersession are explicit.** F and the later confirmed steering win over both this first draft and conflicting earlier S1/review text. Q9 replaces static/composer/message-avatar/preserved-thinking rules with the approved inline animated design; Q5 selects the four figures with Omnipus default; Q6/Q7 defer images/GIFs; Q4 narrows attention to the main’s four sources with the 17:45 clear/mapping rules. Backend no-migration/no-Admin-main/no-cross-workspace clauses are superseded only by the confirmed cutover/addressing amendments. R29 still means agent-icon pulse and collapsed-workspace dot, not dots on agent rows. No source/requirement/other ADR file is edited by this one-file correction.

**Incidental issue for team-lead — not fixed here:** on a cross-workspace selection, E2 sets the active workspace before checking whether attachment succeeds, then returns on failure without restoring the workspace. The setter only changes that value (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/workspacesStore.ts`::useWorkspacesStore.setActiveWorkspaceId). A refused attachment can therefore leave the previous chat selected with the target workspace in the selection store. The write/return ordering is **Verified** from source; the visible user impact is **Inferred, medium confidence**, not browser-tested. Team-lead should verify this in the joint failed-attachment case; this note does not authorize a side fix (E2; D3/D12).

## Decision

### D1 — A bounded first squad, not the entire menu redesign

| First-squad item | Boundary and basis |
|---|---|
| Sidebar | Workspaces expand to main conversation agents; agent click opens the main session; Past sessions and + New chat live on the row; R29 waiting cues. R3–R5, R21–R22, R29. |
| Shared identity | One kit AgentIcon: figure, role badge and color. Four figures; default Omnipus. Uploaded agent images and GIF behavior are outside this squad. F::Q5–Q7 overrides first-squad R17–R19; R41/R46. |
| Chat | Name-only agent bubbles; inline animated AgentIcon/name/phrase replaces the old thinking indicator. No composer status line or agent picker. Persistent Main/Extra label above the feed. F::Q2/Q9/Q10 supersedes earlier R14/R24 wording. |
| Create/edit | Extend the existing unified slide-outs with figure, role and palette choices plus shared live preview. No avatar upload control or second editor. R7/R8/R46; F::Q5–Q7; E6. |
| Plan/activity visibility | Add the #1021 plan pill, start-tool links and drill-down; resolve #493’s active-session-only visibility through one existing activity projection. Reuse B::R-ACTIVITY/D5; remaining product choices are Q-FE-11/Q-FE-12. |
| Joint delivery | Build independent frontend pieces now. Do not land the agent-navigation behavior separately from backend U1. Joint integration and the founder’s landing approval are required. S3::Split of work; S4::frontend/sidebar commission. |

The broader Agents-feed redesign, new creation interview, Connectors, Skills & Tools, permission controls, app-wide component swaps and other R43 waves are not smuggled into this first squad. The roster’s identity renderer changes here; the roster’s full layout does not (R6a–R8, R30–R43; D11).

### D2 — Agent membership and one persistent freshness owner

Expanded workspaces show eligible main conversation agents from authoritative workspace/member and Agent data, never from session recency or a guessed computed ID. Workers and hidden engine agents stay absent even when they are members. The founder’s **Admin default-workspace exception** is consumed from validated backend data, not fake membership; no Admin row is invented in other workspaces (R3–R5/R7; F::Q12; D5/D10).

**AppShell is the persistent navigation freshness owner.** Move `useAgentsCrossTabRefresh` out of the removed AgentPicker and mount it once in authenticated AppShell, extending the existing invalidation to the shared agent and workspace/member caches. The current hook handles focus/visibility; AgentPicker is its sole production mount, so preserving reconnect alone is insufficient (MAJ-003; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useAgentsCrossTabRefresh.ts`::useAgentsCrossTabRefresh; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/layout/AppShell.tsx`::AppShell; E8).

| Trigger | Shared-cache behavior |
|---|---|
| Focus / visibility becomes visible, even within staleTime | Refresh roster and membership through that one persistent owner. |
| Existing WebSocket connect/reconnect | Preserve existing agent invalidation and coordinate membership refresh; no duplicate reconnect service. |
| Sidebar navigation entry / workspace expansion | Refresh the destination roster/member data; replaces picker-open refresh after a separate membership save. |
| Agent-created push followed later by member save | Use pushes plus the entry/focus recovery above; neither event nor array equality proves membership is current. |
| Failed refresh | Keep usable last-known data with visible stale/error state and Retry. First-load failure is not an empty team. |

Use existing query keys, coalesced invalidation and stable cached arrays. Remove picker-specific refresh dependence, not its recovery behavior. The test mounts the real shell/sidebar **without AgentPicker**, creates an agent in another tab, saves membership separately, then refocuses/expands inside the freshness window. A direct hook-only test cannot close MAJ-003 (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useChatAgents.ts`::useChatAgents; review::MAJ-003).

Keep workspace ordering, archive, pin/drawer and keyboard/focus behavior. Agents in Assets still exposes the full roster. Chat stays the base surface, with existing panel toggles and separate global/workspace Library scope; no Chat header entry or new panel system (R1/R2/R6–R8/R21; E1/E11).

### D3 — Main click, exact restore and deliberate extra chat

| Action | Decided destination |
|---|---|
| Agent row click | That agent/workspace pair’s validated main session, never its latest extra chat. Reuse existing attachment/navigation; no in-session speaker switch. R4; B::D1.1/D8. |
| Workspace-name entry, login entry or SearchModal workspace switch | Restore the exact remembered visible chat; otherwise open the server-validated workspace welcome/default agent’s main. F::Q1. |
| Past sessions | Existing SearchModal, initially filtered by both workspace and agent; filters can be broadened. No new history modal. R21/R22; E3. |
| + New chat | Sole extra-chat creation action for the selected main agent/workspace. Preserve the main and current first-send delivery-loss safeguards. R22; F::Q10; E4. |
| Magnifier / `/sessions` | Existing general session search; `/resume` is renamed, not kept as an alias. F::Q10; B::D8. |

The workspace-entry **welcome/default agent means Ava**, the built-in welcome agent, resolved by stable identity and the backend’s validated main association. It is not the global default/star setting (currently seeded to Mia), another agent named Ava, or a browser-generated main ID. Backend must supply an eligible, accessible destination; a missing/invalid association is visible and disables sending rather than choosing a random member (F::Q1; E8; D5).

| Entry or restore state | Required frontend behavior |
|---|---|
| First visit / no real saved pointer | Resolve and attach Ava’s validated main; create no extra chat. |
| Remembered main or extra chat still valid and visible | Restore that exact session with `Session.agent_id` as immutable owner. |
| Remembered session deleted, hidden or no longer authorized | Invalidate that destination and resolve the welcome/default main. Do not bypass membership hiding. |
| No eligible agent / welcome main unavailable | Honest empty/unavailable state with Team/manage and Retry paths; no resolved composer destination and no send. |
| Session/member load fails | Retain the remembered intent and show a retryable error. A failed request is not proof the chat was deleted and must not trigger silent default/new-chat fallback. |
| Attach fails | Stage the requested destination as pending; do not commit a split workspace/session/owner. Keep the last consistent committed selection and the pending intent for Retry, with sending disabled for the unresolved target. Commit the new selection only after validated attach succeeds; do not retain E2’s partial-write inconsistency. |
| Earlier async result arrives after a newer selection | It may refresh its own cache, but cannot replace the newer active destination or another workspace’s saved pointer. |
| Current first-send transient `__pending` | Preserve correlated delivery/recovery and explicit New chat abandonment protection. It is not a main or a saved real destination. |

These cases replace the blank-composer fallback, picker auto-selection and workspace-switch `startNewSession` paths identified in MAJ-004. Use the existing session store/selection flow with a single selection-intent owner; no second restore store. Sending remains disabled until displayed workspace, owner, attached session and the winning intent agree (review::MAJ-004; E2/E4; B::DEL-F08–F13).

**Persistent kind label above the message feed:** “Main chat” or “Extra chat — title”. Use actual `Session.type`, not an ID prefix guess. Existing task/helper inspection must retain its truthful kind, not be renamed Extra chat. When an extra chat is open, its owner’s agent row is selected; row click still opens the main. The kind label makes that difference explicit. No session bar/tree is added (F::Q2; approved S2::chat-label/renderChat; MIN-001).

Row actions remain Past sessions then + New chat: selected-row visibility, hover/keyboard-focus on other rows, usable touch controls, independent accessible names and no nested buttons (R22; S5::component standard).

### D4 — Main-only attention from `Session.needs_attention`

Consume **`Session.needs_attention`**, the settled session-core contract direction: optional boolean/readOnly on the general Session schema, but **present as true/false on every main response**, including Admin’s default-workspace main; omitted on non-main sessions. It is computed server-side and delivered in the existing sessions list, with existing pending-question/approval/goal-outcome snapshots and frames. The session-core specification is the serialization authority; its updated publication SHA is pending, not invented here (F::Q4; founder 17:45 steering; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/session-core-adr-20261007/QUESTIONS.md`::Q7 CLOSED).

| Main-session source | Clear rule |
|---|---|
| Pending structured question card | Resolution clears it, not opening the main. |
| Pending tool approval | Resolution clears it, not opening the main. |
| Unseen goal outcome `met` | Finished-goal attention; explicit foreground open acknowledges observed outcomes. |
| Unseen goal outcome `rounds_exhausted` or `other` | Failed-goal attention; same explicit-open acknowledgement. `stopped_by_user` is excluded. |

Goal seen state is **shared per main chat**: one user’s explicit foreground open clears the observed goal attention for everyone. Backend reuses the existing attach path and bounded seen mark; reconnect, replay recovery and background prefetch are not read acknowledgement. A newer outcome racing the open must remain unseen. The frontend expresses the real user-open intent through the agreed generated attach contract; it does not author the backend read/acknowledgement machinery (17:45 steering; session-core Q7).

Extra chats and helper sessions **do not light the agent’s sidebar signal**. Cross-session activity visibility in D14 is separate and cannot widen this four-source rule. An ordinary free-text question is not detected from punctuation: whenever the agent needs an answer it must use the structured question tool. Prometheus-prompt-engineer owns that prompt change; backend-lead wires it (F::Q4/Q4b; approved S2::main-chat-only waiting demo).

| Cue | Required presentation |
|---|---|
| Agent row | Agent’s own icon pulses: 18% scale, fading warning-yellow `#EAB308` halo, 1.6s loop. No separate agent-row dot; selected row remains signalled. |
| Collapsed workspace | 8px yellow dot, right-aligned beside the expand caret; count distinct main agents with confirmed attention, not sessions/messages. |
| Accessible text | “Main chat needs your attention”; reason-specific question/approval/goal wording only when authoritative reason data is available. Do not call finished work a pending answer. |
| Reduced motion | No loops; icon/dot and meaningful text remain. |
| Failed or incomplete source | Visible unavailable/unknown state; preserve honest last-known data. Missing `needs_attention` on a main or a failed fetch never becomes false or a zero waiting count. |

Use the kit warning-yellow token, not gold/orange. Initial load, unopened workspaces, clearing and reconnect must be verified against the joint backend, not a transcript heuristic or one socket per row (R15/R16/R29/R41; D5/D12).

### D5 — Use the published main contract and settled waiting direction

**B::D1.1 at `b76b412f61ff73d9a6643d34518224472d84223a` fixes the main-session field names.** Remove the answered main-interface alternatives; do not design another address book. Later founder migration/Admin/addressing amendments override only their conflicting b76 clauses and must be carried by the session-core owner’s updated ADR/spec. Backend-lead alone edits schemas and regenerates before frontend consumption (MIN-002; F::Q11/Q12; S5::Hard Constraint #8).

| Contract | Frontend invariant |
|---|---|
| `Session.type` | Required on current-format responses; `main` replaces `heartbeat`, not a new `default` type. Main creation is server-only; client create enums do not gain it. |
| `Session.id`, `workspace_id`, `agent_id` | Computed main ID **`main-session-<workspaceid>-<agentid>`**, validated workspace and immutable owner. No UUID-only navigation assumption or owner fallback to `active_agent_id`. |
| `Session.protected` | Server-computed protection, independent of heartbeat enablement. Respect it in search/delete controls. |
| `WorkspaceMemberConfig.main_session_id` | ReadOnly validated association for eligible main members; omitted for workers/hidden system agents. Consume existing member responses, not a persisted frontend mapping. |
| `WorkspaceMemberHeartbeat` | Keep `enabled`, `interval_minutes`, `body`; delete `session_id`. Heartbeat consumers use the main association. |
| `Message.agent_id` | Actual contributor identity remains separate from the session owner, including guest replies and replay. Remove mutable handover metadata/actions/frame consumers with the coordinated cutover. |
| `Session.needs_attention` | D4’s settled optional/readOnly boolean, present on mains, omitted elsewhere; field direction confirmed at 17:45. Updated session-core spec publication SHA pending. |

**Admin is the explicit later exception to b76’s omission.** He has a main in the default workspace without fake team membership. The backend publishes it through its existing session/address response surfaces; the frontend consumes that validated pair and does not create an Admin main for every workspace (F::Q12; 17:15/17:25 steering).

| Additional dependency | Owner / frontend boundary |
|---|---|
| Welcome/default main for ordinary entry | Backend supplies a server-validated Ava destination; D3 defines frontend restore/failure behavior, not backend membership creation. |
| Persisted figure/role/color and canonical built-ins | Backend identity owner publishes the existing Agent create/update/list/editability representation and fixes canonical seeds; D6/D9. No invented avatar JSON keys or local-only saved identity. |
| One-cutover chat migration, heartbeat → main | Session-core owner implements the agreed install/upgrade exception; D10 consumes current-format records and verifies continuation. |
| Cross-workspace peer/address protocol | Session-core owner; the later `@` UI carries explicit workspace + agent ID. D11 adds no bare-ID routing or copied history. |
| Running task/scheduler data | Reuse B::R-ACTIVITY/D5 and existing task/run events. D13/D14 add presentation/aggregation, not another task dispatcher or result pipeline. |
| Activity/start links not covered by current schemas | Backend owner extends existing contract surfaces before consumers. No fabricated result URLs, current-session-only snapshot guarantee or handwritten wire types. |

Keep one shared query/cache/attachment path. No per-row live connections, transcript loading for navigation, second main store or new attention/event service. D2 assigns freshness independently of the removed picker. UI preparation can proceed now, but integrated landing remains held for committed generated contracts, working U1 and the matching dependent backend amendments on the joint candidate (R4/R29/R41; F; S5::Definition of Done).

### D6 — Approved figures, role badges and one agent palette

First-squad identity is **figure + role badge + color**, shared across workspaces. Offer **Robot, Man, Woman, Omnipus; DEFAULT = Omnipus**. Use the approved demo figures and minimal-eye grammar; keep figure plus role badge at **every size**, with legibility checks rather than a silent plain-role fallback. The earlier Robot recommendation and open figure question are answered. The app-wide demo switch is not a production setting (F::Q5; approved S2::fig-switch, FIGURE_FACE, figureInner).

The role chooser offers R11’s 31 roles, grouped Create / Build / Business / People / Personal. Reuse the trusted curated figure/badge assets and the standalone Phosphor role vocabulary, not role guesses from names or new illustrated mascots. Q5’s approved combined figure grammar supersedes conflicting older no-face/style wording where necessary. Uploads/GIF choices are deferred, not another first-squad identity lane (R9–R11/R46; F::Q5–Q7).

Names use normal product text color; identity color belongs to the icon. **Q9 is the explicit exception to “icon and name everywhere”: chat bubbles show the actual agent name only.** The inline responding-agent indicator still shows icon and name. Standard selection styling does not recolor an identity glyph to brand gold (R10/R12; F::Q9).

| Surface | Identity size/arrangement |
|---|---|
| Sidebar | 26px icon then 13px name text. R13. |
| Team | 18px icon before name. R13. |
| Agents roster | 40px icon before name. R13. |
| Inline status indicator | 48px responding-agent icon, then name/phrase; no second small icon or separate chat logo. R13 retained by F::Q9; approved S2::renderThinkPreview. |
| Message bubbles | Author’s name only; no avatar, on live and historical paths. F::Q9. |
| Editor / later `@` suggestions | Same figure/role/color renderer and role badge, not fixed Robot or an independent circle helper. R46; F::Q5. |

Use the ten named R16 colors below. Hex is governed identity data, not raw chrome styling. Figure/role/color remain global agent configuration; do not persist them in a separate browser settings store (R8/R16; E7; S5::Governed data is not chrome).

| Name | Hex | Name | Hex |
|---|---|---|---|
| Azure | `#3B82F6` | Purple | `#C084FC` |
| Sky | `#38BDF8` | Fuchsia | `#E879F9` |
| Cyan | `#22D3EE` | Pink | `#F472B6` |
| Indigo | `#818CF8` | Orange | `#FB923C` |
| Violet | `#A78BFA` | Grey | `#9CA3AF` |

Implementation must verify **3:1 graphic contrast** on sidebar/chat surfaces; no measured contrast PASS is asserted. Gold, warning yellow, semantic greens/reds and Liquid Silver are not selectable identity colors. The four-figure/default/no-small-fallback policy is settled in F::Q5; the approved migration rule is D10. Legibility at every size is an acceptance check, not permission to introduce a different glyph silently (R13/R16; F::Q5/Q8).

### D7 — One published AgentIcon and reusable kit presentation

AgentIcon is one composite for supplied figure/role/color, named sizes and approved motion variants. It performs no agent fetch, membership/session resolution or activity aggregation. Keep the transparent glyph grammar; do not force existing Avatar’s circle clipping onto it, duplicate Avatar, paste the prototype’s SVG-string renderer into screens or add an image-upload lane in this squad (R10/R41/R46; F::Q5–Q9; E9).

| Publication part | Implementing requirement — no component created by this ADR |
|---|---|
| Catalog/source | One composite entry in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/design-system/catalog.json`. Proposed source: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/ui/agent-icon.tsx`; explicit public exports/types. |
| Public barrel | Re-export from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/index.ts`. |
| Style registration | Explicit source registration in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/styles/library.css`; automatic discovery is disabled. |
| Manifest/evidence | Proposed `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/design-system/manifests/agent-icon.json`: unit, interaction, axe, keyboard, browser, pointer, reduced-motion, forced-colors, root-size, zoom and reflow; stories plus executed static-Storybook checks. Inapplicable is never “not tested”. |

Apply the same four-part publication rule to genuinely new recurring jobs such as shared attention/inline status/activity-pill presentation. Compose kit Button/Badge/RunningIndicator and approved variants rather than separate screen-local Plan/Agents/Commands pill implementations. Screens pass layout-only classes; look and motion remain central (R41; S5::rules 9/10/14).

Adopt AgentIcon in Sidebar, Team, Agents roster, SearchModal headers, editor preview, inline responding-agent indicator and agent identities in Activity; later `@` suggestions use it too. **Do not put it back in message bubbles.** Non-agent terminal symbols and separate runtime-kind labels retain their own jobs (F::Q9; E3/E6/E12).

Stories must prove all four figures with role badges at every named size, approved motion/amplitudes, contrast and reduced motion. Publication metadata alone is not rendered or accessibility evidence. Avatar image/failure tests belong to the deferred upload unit, not a pretend first-squad image feature (R13/R15/R16/R29; F::Q5–Q9; S5::four-part contract).

### D8 — Inline animated agent indicator; name-only replies

**Founder Q9 is FINAL.** The animated agent status indicator sits **inline in the chat feed, exactly where the old thinking indicator was**, replacing that indicator completely. There is **no separate status line above the composer**. Agent bubbles show their actual author’s **name only, with no avatar**. Apply this to live, replayed, virtualized and plain-fallback messages, not just the next streaming reply (F::Q9; approved S2::chat-feed, messageHtml, renderThinkPreview; E5).

The inline indicator uses the shared AgentIcon, the responding agent’s name and a phrase. During an addressed `@` answer it shows the addressed agent, not the chat’s immutable owner. The owner does not switch; the permanent Main/Extra label above the feed continues to describe the open session. Full `@` messaging remains later and unavailable until its backend contract lands (F::Q2/Q9/Q10; B::D1.1/D7; D3/D11).

| State | Approved reference motion |
|---|---|
| Working | 1.6s pulse/glow, scale 0.97–1.03; 2.4s sheen. |
| Thinking | 2.6s breathing/glow, scale 0.96–1.01, opacity 0.55–1. |
| Waiting | 3.4s pop/flash, peak scale 1.07. |
| Idle / reduced motion | Static icon; meaningful name/state text remains. Reduced motion disables loops. |

These values come from S2::LOGO_CSS and the founder-approved figure renderer, not a new animation design. Reuse the existing `THINKING_MESSAGES` phrases where appropriate; do not retain the old bouncing-dot animation or a duplicate ThinkingIndicator implementation. Phrases are presentation, not evidence of the execution state. Keep stable accessible agent/state text rather than announcing every decorative phrase change (F::Q9; R15; E5).

Use explicit message/turn producer identity and the open session’s real state. Do not resurrect scalar `goalStatus` or guess another chat’s state from a global active-agent flag. Backend’s canonical per-goal/producer replacements remain its integration dependency; Q9 makes the review’s requirement to preserve the old InlineThinkingIndicator moot, not permission to lose genuine errors or correlation (B::DEL-F30–F43; review::Shared deletions).

Remove AgentPicker, preserving the composer’s model, per-chat Auto, attachments and send/Stop controls. The approved wireframe contains demo controls and messages only: no Demo, simulation or app-wide figure switch is added to the product (F::Q9/Q10; S2::chat-feed/fig-switch; R20).

### D9 — Existing editor, canonical identity and deferred uploads

Create/edit slide-outs offer **figure, curated role badge and palette color**, with the same AgentIcon live preview used by sidebar and inline chat. AvatarHeader no longer hardcodes Robot. Preserve the unified editor, global autosave/create semantics and server-provided field editability; a draft preview is not a saved identity (R7/R8/R46; F::Q5–Q7; E6).

**MAJ-002: updating stored values once is insufficient for built-ins.** Backend-owned identity work must update canonical compiled definitions, fresh-install seeding and startup enforcement together. Current `SeedConfig` restores `Color`/`Icon` from compiled definitions and boot persists that roster; Jim currently supplies semantic green/graph and Ava brand gold/wrench. Rendering a migrated color cannot prevent restart from restoring those values (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/pkg/coreagent/seed.go`::SeedConfig; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/pkg/coreagent/core.go`::Jim, Ava; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/pkg/gateway/boot_agent_roster.go`::seedAndPersistAgentRoster).

The canonical role/icon/figure values must follow the approved vocabulary/default, and canonical colors the approved palette. Preserve built-in identity locks, names, base instructions and capability boundaries; do not make protected identity editable or use render-time recoloring as a repair. Require fresh install, existing custom/built-in migration, reload and repeated restarts, with the same rendered identity and unchanged field locks. Identity migration is expressly required by R11/R16, separate from the limited chat-format migration in D10 (F::Q5/Q8; MAJ-002; **Built-in agent configuration, skills, and visual file reading**::What users and Ava may edit).

**MAJ-001 is moot for this squad:** founder Q6/Q7 defers agent-image uploads and GIF/playback placement entirely. No new avatar upload endpoint/control or preview-only saved-image substitute ships here. Existing chat/file/document uploads are not removed by this decision (F::Q6/Q7).

For the later upload unit, retain R17’s format/2 MB/re-encoding/SVG-rejection requirements and the review’s **pre-decode** dimension/pixel bound, bounded normalization/concurrency and, if animation is approved, frame/total-work limits. Current normalization checks `maxImagePixels = 16 × 1024 × 1024` before full decode; post-decode resize alone is not safe. Exact later avatar budgets, authenticated ownership/cleanup and animation placement require that unit’s design and founder approval; no capacity or exploit PASS is claimed (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/pkg/agent/loop_media.go`::maxImagePixels, encodeImageToDataURLCached; review::MAJ-001).

Companion **#1221** deletes the per-agent “Never auto-approve” checkbox and associated layer completely. This avatar/editor decision neither preserves that checkbox nor implements its removal; #1221 owns it. Retain only the still-supported composer controls and consume the companion generated contract without reviving `auto_approve_disabled` (founder steering; https://github.com/elicify-ai/omnipus/issues/1221).

### D10 — The one-cutover migration is a backend dependency

**F::Q11 and the confirmed 17:15 steering override the earlier no-migration clause for THIS cutover only.** Existing saved chats must remain **reachable and continuable** after install/upgrade; heartbeat sessions become mains. The session-core owner records and implements that exception using its existing storage/import mechanisms. This ADR does not choose file conversion, ID remapping, history merging or a dual reader (F::Q11; B’s dated migration amendment; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/CONTINUATION-20261005.md`::17:15).

| State | Frontend requirement |
|---|---|
| Existing saved ordinary/extra/Unfiled/child chats | Keep search/inspection and legitimate continuation through current-format backend responses; verify actual migrated data, not fresh fixtures only. |
| Former heartbeat session | Consume backend migration’s `main` response and validated association; no browser inference from an old heartbeat ID/type. |
| Eligible main missing | Visible resolve/loading/error and backend get-or-create; no latest-chat substitute, extra chat or frontend-only main. |
| Worker on team | Remains a worker, hidden from fresh sidebar conversation rows; existing task/child sessions remain inspectable/continuable under B’s authority. |
| Removed/re-added member | Respect backend hide/unhide; migration is not permission to open a deliberately hidden main. |
| Admin | Validated main in the default workspace, shown as its main-agent row; `@` from elsewhere uses that default-workspace pair. No fake team membership or extra Admin mains. |
| Import/source/contract failure | Visible and retryable; no silent empty-history substitution or “migration succeeded” claim from a rendered Welcome screen. |

Keep the greenfield/delete-superseded rule outside that expressly bounded migration. The frontend does not retain missing-type or mutable-owner fallback chains to compensate for an unfinished backend import. Joint acceptance must open and continue saved chats and former heartbeat history, including failure/retry and repeated upgrade behavior; backend owns the migration algorithm (F::Q11/Q12; B::D1.1 and dated amendment).

**F::Q8 approves S2::toPalette.** For a valid hex, saturation below 0.25 maps to Grey; otherwise minimize circular hue distance among the non-Grey palette entries. Invalid/missing hex maps to Grey; equal distances retain the first entry in the published palette order because the update uses strict `<`. Write thresholds, ties and representative fixtures into the specification for approval. Persist the mapping once; update canonical built-ins as D9 requires, not on every render. R11’s explicit obvious icon mappings remain; unmatched icons become General assistant (R11/R16; F::Q8; S2::hsv/toPalette).

### D11 — Immediate cleanup; later workspace-qualified messaging

**F::Q10 decides the cutover now:** remove `/new`, `/agents` picker openers, generic extra-chat bypasses and mention-driven switching with the sidebar. Rename `/resume` to `/sessions` across registry/client/help/docs, with no old alias. `@` suggestions are unavailable until proper messaging lands. `/clear` must never retain a new-session action or alias, even while its complete safe-point behavior remains backend-dependent. Preserve New chat’s first-send recovery/abandonment guard (F::Q10; B::D8/DEL-F01–F02; E4).

| Later or companion work | Frontend direction |
|---|---|
| Full `@` group messaging | Every selected address is **workspace + agent ID**, resolving to that pair’s main. Default picker workspace is the open chat’s current workspace; another workspace is chosen explicitly and visibly. Carry the qualified pair through draft/send/retry, not a bare name/ID or the mutable global workspace at send time. |
| Addressed replies | Name-only bubble for the actual responder; inline AgentIcon/name/phrase while that agent answers. No owner switch, whole-history copy or guessed return destination. |
| Admin access | Default-workspace main shown there; elsewhere explicitly address Admin’s default-workspace pair. Remove the old Assets-only entry only when the replacement main is actually reachable on the joint build. |
| `/clear` | Consume B’s safe-point context/display marker and retained-history semantics. No browser history deletion or new chat. |
| Header / Tooltip | Retain separately delivered R44 measured-space modes and R45 shadcn-interface replacement; do not build another header fix or legacy Tooltip wrapper here. |
| Avatar uploads/GIFs | R17–R19 deferred. Later founder decision determines animation placement/resource bounds; no first-squad still-GIF substitute. |

The **17:15/17:25 founder decisions supersede b76’s no-cross-workspace limit**: agents collaborate across workspaces through peer messages and tasks in any workspace, **not cross-workspace plans**. Backend owns eligibility, policy enforcement, explicit pair resolution and the merged task-tool contracts; the UI does not grant tools or invent worker mains. 17:45 merges canonical `create_task`/`update_task`/`list_tasks` with optional explicit `workspace_id`; default is the agent’s own workspace, another destination only by explicit choice. This is a session-core dependency, not a new frontend task engine (F; S4::17:15/17:25/17:45; session-core questions::Q8 CLOSED).

Admin’s role/tool protections remain governed by the backend; having a main or viewing another workspace’s activity grants no additional control. Worker sidebar exclusion and valid existing-session inspection remain distinct from backend peer/task capabilities (R5/R7; D5/D10).

### D12 — Build in parallel; integrate and land together

```text
Independent kit + frontend UI preparation (now)
                         |
Backend shapes agreed -> schemas + generated types -> working U1
                         |
Frontend consumes those contracts on the joint candidate
                         |
Joint main/extra/history/attention + identity integration checks
                         |
Feature gates + exact-commit hands-on test + independent validation
                         |
Founder approves -> frontend and U1 land as one coordinated delivery
```

F’s joint-delivery instruction overrides S3’s independent-landing draft: build independent UI pieces now; main-session navigation lands **together with U1 after joint integration**. Consume committed generated types on the candidate, not guessed shapes. Explicitly coordinate canonical visual-identity persistence, needs_attention/attach, the migration/Admin amendments and B::R-ACTIVITY task/run data with their backend owner. Avatar uploads are deferred and are not quietly assigned to U1. Dependent Q-FE-11/Q-FE-12 activity behavior is held until the founder answers it (D5/D9–D14; S5::Hard Constraint #8).

| Joint proof required before landing | Why it matters |
|---|---|
| Two workspaces / main and newer extra / exact remembered main or extra | Row click opens the validated main; entry restores exact valid last chat, otherwise validated Ava main; label and send destination agree. R4/R22; F::Q1/Q2. |
| Missing/hidden/deleted destination, empty team, load/attach failure and late response | No silent new chat, guessed owner, cross-workspace partial commit or overwrite of newer intent; no send before a consistent resolved destination. MAJ-004; D3. |
| Real upgraded ordinary/extra/Unfiled/child and heartbeat histories | Reachable and continuable; heartbeat becomes main; failed migration is visible and retryable. Fresh-only fixtures are insufficient. F::Q11; D10. |
| Main pending cards/approvals and met/rounds_exhausted/other/stopped_by_user outcomes | True/false summary and distinct-agent dot count; extra/helper events do not leak into sidebar signal. Explicit open clears observed goal outcomes for everyone, never asks/approvals/newer racing outcomes; prefetch/reconnect do not acknowledge. D4. |
| Still-connected tab focus plus agent-created then later member save | Shell/sidebar without AgentPicker refreshes both caches inside staleTime; stale/error states remain honest. MAJ-003; D2. |
| Fresh and migrated custom/built-in identities; reload and repeated restart | Canonical seed values stay in palette and approved figure/role grammar, with locks unchanged. MAJ-002; D9. |
| Inline indicator, name-only live/replay bubbles, Main/Extra label | Approved Q9 positions/motion/phrases; actual addressed responder, no old dots/avatars/composer line; reduced-motion loops stop. F::Q2/Q5/Q9; D6–D8. |
| Plans, task/scheduler runs, helpers and shells across selected activity scope | Real counts, state, start-result links, existing-panel presence and actual drill-down; no duplicate run, hidden ongoing work, false zero or invented control authority. D13/D14; Q-FE-11/Q-FE-12. |
| Default-workspace Admin and later explicit cross-workspace addresses | Main is reachable before old entry removal; workspace+agent pair is preserved, no bare-ID addressing or fabricated extra Admin mains. F::Q12; 17:25; D5/D11. |
| Command/deletion overlap | No /new, /agents picker, @ switching or /clear-to-new alias; preserve current first-send, kickoff, replay/order/error safety while canonical contracts replace retired fields. F::Q10; B::DEL-F01–F43. |

The implementing squad follows the feature specification/test-integrity/review gates in S5. Engine-touching joint work requires hands-on user acceptance testing on the **exact candidate commit**, with an independent validator, using the founder-set provider/model. This ADR performs none of those product checks. Keep **“code correct and tested”** separate from **“reachable by a user/agent”** (S5::Definition of Done and Change sizes and the review gate).

### User-facing documentation TODOs

These pages were read. The implementing leads update the matching sections in the same behavior change; docs-verifier audits them against the joint build. Backend/frontend authors coordinate overlapping page edits rather than publishing contradictory main/session instructions. No user-facing page is changed by this design-only task (S1::§7; S5::User-facing documentation).

| Page | Specific first-squad TODO; later additions stay tied to their delivery |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/using-omnipus-ui.md` | Agent rows, exact last-chat/default-main entry, Past sessions/+ New chat, Main/Extra label above feed, name-only bubbles and inline animated responder indicator. Explain main-only `needs_attention`, shared goal acknowledgement versus question/approval resolution and reduced motion. Move `/new` delivery-loss protection to New chat; document removed commands/@ temporarily unavailable. Add plan/start links and the founder-approved cross-session activity scope before that behavior lands. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/agents.md` | Four figures, Omnipus default, role badges at every size, approved palette/migration, global edits, protected built-ins and restart-stable canonical identities. Explicitly no avatar uploads/GIF feature in this squad. Main/heartbeat and default-workspace Admin access track backend delivery; later addressed replies use names-only bubbles and qualified workspace+agent targets. #1221’s lead owns removal of its obsolete checkbox guidance. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/workspaces.md` | Agent rather than session expansion, restore/default entry, per-pair mains/extra chats, freshness/Retry and hide/unhide. Name the one-cutover migration/continuation guarantee and heartbeat→main. Admin’s main is in default workspace; explicit cross-workspace messaging/tasks do not authorize cross-workspace plans. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/plans.md` | Running/waiting plan pill, successful start-tool link, existing ActivityPanel presence and Tasks/Graph drill-down. Explain exact plan association after Q-FE-12 is answered, approved-versus-running state and cross-session visibility without implying a new plan engine or cross-workspace plan. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/tasks.md` | Actual task-run/session links from start results and ActivityPanel, including scheduler runs not started by a foreground tool. Explain real source/workspace, deduplicated counts and monitoring versus control of independent runs, consistent with B::R-ACTIVITY/D5. |

### D13 — Plan pill, start-tool links and real drill-down (#1021)

The handed-over issue’s **R1–R4** are plan/task visibility requirements, distinct from navigation R1–R4. Provide a running plan pill in chat, a link from the successful start tool, presence in the existing ActivityPanel and real drill-down. Historical execution/schema/counting findings in that issue are not extra implementation scope (https://github.com/elicify-ai/omnipus/issues/1021::Requirements and 2026-10-07 hand-over comment).

| Surface | Frontend decision |
|---|---|
| Plan pill | Visible for real ongoing plan work; title and actual reported state. `approved` is “waiting to start”, not running; `running` uses canonical phase/pause/progress. No optimistic running claim from an accepted start. |
| `execute_plan` result | Accessible **Open plan** link attached to the successful/idempotent result, live and after replay. Use its validated plan handle and authoritative workspace metadata, not a URL/title guessed from prose or current workspace. |
| `run_task` result | Accessible **Open task session** link to the actual returned run/session, not the agent’s main; visible error/unavailable guidance if no valid destination exists. |
| ActivityPanel | Plan row plus task/run rows using the existing panel and common status/identity presentation. Distinguish plan, task and helper; do not create a parallel dashboard or synthetic parent edge. |
| Drill-down | Open plan through the existing workspace Tasks/Graph plan-selection workflow; open task/helper through the existing session inspection path. Pill behavior reuses the existing activity-pill → panel interaction; Open controls remain independently keyboard-accessible. |

Reuse B::R-ACTIVITY/D5 for task/scheduler data and execution ownership. A MAIN task child is represented once, not once as task and again as helper; monitoring an independent run does not make it tree-stoppable. Do not add a task executor, result summarizer or new outcome-delivery mechanism (B::D5).

Existing seams: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ActivityBar.tsx`::ActivityPill/ActivityBar; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ActivityPanel.tsx`::ActivityRow; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/workspaces/WorkspaceTasksTab.tsx`::handleSelectPlan; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/lib/api/plans.ts`::fetchPlan/plansQueryKeys; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/Plan.yaml`::state/plan_phase/progress/owner_session_id.

The current start tools already return `plan_id`/state and `task_id`/`session_id`, but not the requested usable navigation experience (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/pkg/tools/plan.go`::PlanExecuteTool.Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/pkg/tools/run_task.go`::TaskRunTool.Execute). Backend owns any required published result/address extensions; frontend does not invent a wire URL field. Existing panel context has no plan-selection field, so the implementing spec must adapt its actual hand-off rather than claim a deep link already works. **Which chats receive plan pills/parent rows is Q-FE-12**; `owner_session_id` or transport `source_chat_id` must not be assumed to identify the starting human chat.

### D14 — Background work remains visible after changing chats (#493)

**The active-session-only visibility ceiling is removed.** Extend the existing `useRunningActivity`/ActivityBar/ActivityPanel projection across the approved scope; do not add another activity store/service. Work started in chat A must not disappear or be reported as “no active background work” merely because chat B is foreground. Exact workspace scope and full rows versus an elsewhere indicator are **Q-FE-11**, not guessed (https://github.com/elicify-ai/omnipus/issues/493::Goal/Definition of done and 2026-10-07 hand-over comment).

| Projection requirement | Boundary |
|---|---|
| Plans / task and scheduler runs / helpers / background shells | Reuse existing plan queries/status invalidation, B::R-ACTIVITY/D5 task/run projection and existing session-bucket activity. No agent-facing `list_jobs` call from the SPA. |
| Starting/owning context | Label real workspace, owner and source/session; clicking Open targets that actual entity, never the foreground session by substitution. |
| Counts | Count real distinct work by authoritative plan/run/child/shell identity. Deduplicate starter/assignee appearances; a plan’s member execution is not duplicated as an unrelated helper/task. Keep kind-specific counts truthful. |
| State | Separate running, queued, awaiting input, stopped and finished. Changing view, disconnecting or hiding a workspace does not cancel or fabricate completion. |
| Initial load / reload / reconnect | Reconcile authorized current snapshots and live updates. Cached `sessionsById` alone cannot prove coverage of work never opened in this browser. |
| Missing/stale source or omitted page | Visible partial/unknown coverage and Retry; not zero/complete. Active counts are not silently capped by the eight-item recent-finish limit. |
| Footprint | Metadata/state projection, not all transcripts, one socket per job/session or one independent poller per row. Reuse shared/coalesced query invalidation. |

At the baseline, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useRunningActivity.ts`::useRunningActivity reads foreground messages/toolCalls; ActivityBar is its sole production consumer. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/chat/routing.ts`::schedulePlanStatusInvalidate already coalesces global plan updates; reuse that mechanism rather than a refetch storm. The exact producer/snapshot coverage remains a backend contract dependency, not a claim it already ships.

Viewing cross-session activity does not widen D4’s **main-only** `needs_attention`, change Stop’s selected-session/tree scope, grant peer authority, or make a plan cross-workspace. This consciously revisits **list_jobs — unified background-job visibility for agents**::Option D’s rejection of the SPA scope extension, while preserving that agent tool’s recovery purpose (https://github.com/elicify-ai/omnipus/issues/493; B::D5; D4).

## One-round correction disposition

This is the author’s correction evidence, **not an independent gate verdict** or product PASS. No second grill is run; unresolved new activity choices go to the founder. All original findings are addressed or scoped moot by the binding answers (review; F).

| Finding | Disposition in this correction | Evidence / acceptance still required |
|---|---|---|
| **MAJ-001** pre-decode upload bounds | **Moot for first squad**: Q6/Q7 defer avatar uploads/GIFs completely. D9 preserves pre-decode/pixel, normalization/concurrency and future animation bounds for the later unit. | F::Q6/Q7; D9; existing maxImagePixels-before-decode source read. No upload endpoint/security PASS asserted. |
| **MAJ-002** built-in identity reseeding | **Addressed**: D9 includes canonical compiled definitions, fresh seed, startup enforcement/persistence and protected-field preservation, not just one-time stored recoloring. | SeedConfig/core definitions/boot source; fresh/custom/built-in, reload/repeated-restart cases in D12. |
| **MAJ-003** picker-owned freshness | **Addressed**: D2 moves the one cross-tab refresh mount to AppShell, refreshes roster+membership and replaces picker-open refresh with navigation/expansion. | Sole AgentPicker mount verified; real shell/sidebar-without-picker test required. |
| **MAJ-004** entry/invalid restore | **Addressed** by F::Q1: exact valid last chat, else validated Ava welcome/default main; D3 covers missing/hidden/empty/error/late/failed-attach and send gating. | Existing session store/selection paths read; D12 negative cases, no runtime claim. |
| **MIN-001** main/extra distinction | **Addressed** by F::Q2: persistent Main/Extra title above the feed, selected owner row during extra chat and row click returning to main. | Approved Q9 reference and D3/D12. |
| **MIN-002** stale main interface question | **Addressed**: D5 consumes b76 D1.1 field names/invariants; answered alternatives removed. Settled needs_attention direction is cited separately, with honest spec publication state. | b76 immutable source; 17:45 record/steering; D4/D5. |

### Founder-answer coverage — answered questions retired

| Interview answer | Rule carried into this correction |
|---|---|
| Q1 | Last valid visible chat, else validated workspace welcome/default main (Ava); D3. |
| Q2 | Main/Extra label above the feed, not in composer status; D3. |
| Q3 | Upload budget question moot for first squad under Q6; later bounds retained, D9. |
| Q4 / Q4b | Main only: structured questions, approvals, finished/failed goals. No prose parsing; structured-question prompt owned by prometheus. D4. |
| Q5 | Four figures, Omnipus default, role badge at every size; D6. |
| Q6 / Q7 | No avatar uploads/GIFs in first squad; later placement/design decision, D9/D11. |
| Q8 | Wireframe nearest-hue/Grey rule approved; thresholds/ties/examples in specification for approval; canonical seeds included, D9/D10. |
| Q9 FINAL + approved wireframe | Inline animated AgentIcon/name/phrase replaces old thinking indicator; name-only bubbles; addressed responder shown. No composer status line, D8. |
| Q10 | Remove old paths now; /resume → /sessions; @ unavailable until messaging; /clear never creates a new session, D11. |
| Q11 + 17:15 | THIS-cutover install/upgrade migration preserves saved-chat reachability/continuation and heartbeat→main. Backend dependency, D10. |
| Q12 + 17:15/17:25 | Admin default-workspace main, shown there and explicitly pair-addressable elsewhere; no fake membership. Backend dependency, D5/D10/D11. |
| 17:25 address rule | Always workspace + agent ID; current workspace default, another workspace chosen explicitly. D11. |
| 17:45 waiting contract | Session.needs_attention; shared observed-goal acknowledgement; met finished, rounds_exhausted/other failed, stopped_by_user excluded; no prefetch/reconnect acknowledgement. D4. |
| 17:45 task collaboration / #1221 | Backend canonical task-tool merge with optional explicit workspace; no cross-workspace plans. Per-agent Never auto-approve removed by companion #1221, not this avatar implementation. D9/D11. |
| #1021 / #493 hand-over | D13/D14 add plan/link/drill-down and cross-session visibility. Q-FE-11/Q-FE-12 are genuinely new unanswered choices, not reopened original questions. |

## Consequences

| Kind | Consequence | Basis / certainty |
|---|---|---|
| Positive | Main-agent navigation stays stable when extra chats or background work become newer. | R4/R22; D2/D3. Inferred design benefit, high confidence; not exercised. |
| Positive | One kit identity/indicator avoids editor/sidebar/responding-agent drift while names-only bubbles keep the feed quiet. | F::Q5/Q9; D6–D9. Inferred benefit, not rendered-product evidence. |
| Positive | Main-only attention stays visible when a workspace is collapsed; background work remains inspectable across the chosen activity scope. | D4/D14; I493. Target behavior, not an executed acceptance result. |
| Negative | The frontend requires U1, canonical identity/seed support, current generated contracts, migrated histories and settled waiting/attach behavior before integration. Document decisions are not implementation. | D5/D9/D10; known baseline E10. Verified dependency, high confidence. |
| Negative | Removing session-tree/picker/old thinking affordances changes learned habits; exact restore, clear Main/Extra labels and delivery safeguards are essential. | F::Q1/Q2/Q9/Q10; D3/D8/D12. Inferred user impact. |
| Negative | Role-badge readability and new motion need measured acceptance; activity scope/plan association remain genuine founder choices. Uploads/GIFs are deferred rather than an unresolved first-squad feature. | F::Q5–Q9; Q-FE-11/Q-FE-12. Product rendering untested. |
| Negative / accepted | Goal attention is shared: another user opening the same main can clear observed goal attention for everyone, unlike pending asks/approvals. | 17:45 founder answer; D4. Verified design decision, not runtime verification. |
| Neutral | Storage/migration, queues, permissions, lifecycle, task modes and peer-message authority remain backend-owned. No new chat/attention/task engine or fake worker/Admin type. | B::D1.1/R-ACTIVITY/D5; F::Q11/Q12 and 17:15/17:25/17:45. |
| Neutral | Existing Team/global agent-management duties, panel ownership, Stop behavior and product branding remain intact. | R1/R2/R7/R8; E6/E11/E12; **UI-independent turns and session-bound webchat streaming**. No runtime change certified. |

## Alternatives considered

| Alternative | Why rejected or not adopted |
|---|---|
| Open the most recent chat on row click | Explicitly superseded by October 7 R4; an extra chat would hijack the main-session destination. |
| Keep a sidebar session tree or add a session bar above chat | R3/R21 keep sidebar agents-only and use existing SearchModal. Historical/child inspection belongs there and in existing activity surfaces, not another navigation tree. |
| Keep picker/`@` switching or old command aliases | F::Q10 removes those paths now; later mentions communicate with explicit workspace+agent pairs, not handover. |
| Compute a main ID and pretend it exists | B::D1.1 supplies the validated association and immutable owner; a computed string alone grants no access. |
| Infer attention from prose/global activity or merge extra/helper attention into the dot | F::Q4/17:45 requires main-only `needs_attention` from the four authoritative sources; activity aggregation is separate. |
| Keep static composer status line, bubble avatars or old bouncing-dot indicator | Explicitly superseded by FINAL Q9 and the approved inline/name-only reference. |
| Copy demo SVG strings/switches into screens or create parallel avatar/pill helpers | Approved graphics/motion are implemented once through the kit’s four-part contract. Demo simulation controls are not product controls. R41; D7. |
| Keep active-session-only activity or treat loaded buckets as complete global truth | #493 requires ongoing work not disappear on navigation; selected scope and coverage must be truthful. D14; Q-FE-11. |
| Build another plan/task dashboard or execution/result system | Existing ActivityPanel, Tasks/Graph and B::R-ACTIVITY/D5 supply the reusable mechanisms. D13. |
| Fully redesign Agents, Team, all panels and all kit components now | Exceeds the first-squad commission and R43’s separate waves. Change shared identity only where this scope requires it. |
| Ship navigation before U1 with latest-chat fallback | Violates R4 and the founder’s joint-landing instruction. UI preparation can proceed, but unresolved integration remains visibly held. |

### Existing decisions affected — dated scope correction, 2026-10-07

| Decision by title | Narrow relationship |
|---|---|
| **Unified Slash-Command + Skill Menu (skill-as-command, partitioned palette)**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-026-unified-slash-command-skill-menu.md`::D7/D8 | F::Q10 now fixes the immediate removal/rename cutover; no old picker/switch/alias timing question remains. Keep the unified skills/commands menu. |
| **list_jobs — unified background-job visibility for agents**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-056-background-job-visibility.md`::Option D | The founder’s #493 hand-over authorizes revisiting the active-session-only SPA visibility ceiling. Retain the agent-facing tool and existing execution stores; D14 changes frontend projection, not that tool’s authority. |
| **Unify delegate sub-turns onto the own-session execution path**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-057-session-parent-child-parity.md`::R-9 listing policy | R3/R21 replace only sidebar session-tree presentation. Existing child-session ownership and modal/activity inspection are not deleted by this frontend ADR. E1/E3 are the current implementation evidence. |
| **Built-in agent configuration, skills, and visual file reading**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-090-built-in-agents-skills-and-visual-reading.md`::Roster, What users and Ava may edit | Keep Admin’s operator/no-team role, fixed identity and hidden-engine boundaries, but F::Q12 supersedes standalone-only navigation with a default-workspace main. Canonical visual seeding changes do not grant identity edit rights; backend owns the amendment. |
| **UI-independent turns and session-bound webchat streaming**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md`::P1/P2, D3–D5 | Preserve its boundary: sidebar navigation is viewer attachment, not cancellation or transfer of execution. Live/replay identity must agree. |
| **Remove the “main” sentinel agent**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-064-remove-main-sentinel-agent.md`::Decision | “Main session” does not revive a synthetic agent named main or a fallback speaker. Use the real agent/workspace pair, R4 and B::D1. |

Older files are not silently amended in this one-file commission. The table flags exactly which UI clauses the new decisions supersede; broader backend amendments belong to B (S5::Project and scope boundary).

## Affected components

These are **future implementing targets**, not changed files in this ADR. E-key references resolve to the absolute source citations above.

| Area | Existing components / owner and intended adaptation |
|---|---|
| Navigation/restore/freshness | E1/E2/E3/E8, session store, AppShell and existing cross-tab refresh hook. Frontend-lead replaces rows/entry fallbacks, uses immutable owner and re-homes freshness; D2/D3. |
| Shared kit | D7 AgentIcon plus recurring attention/inline indicator/activity-pill presentation through catalog/barrel/style/manifest, not new screen-local helpers. |
| Identity/editor | E6/E7 and D9. Frontend-lead owns figure/role/color controls/preview; backend-lead owns current Agent representation, editability, canonical core definitions, seeding/enforcement and one-time identity mapping. Upload unit is later. |
| Chat | OmnipusComposer/useSlashMenu, live/historical/plain message rows and thinking call sites. Names-only bubbles, shared inline animated responder indicator, above-feed kind label and immediate retired-command cleanup; D3/D8/D11. |
| Team/roster | E12 AgentNode/ActivityAvatar and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/screens/AgentListScreen.tsx`::AgentListScreen. Shared identity only, not an unrelated roster/Team redesign. |
| Plans/activity | Existing useRunningActivity, ActivityBar/Panel, plan/task query caches, Tasks/Graph selection, successful execute_plan/run_task result UI and session inspection. Frontend-lead owns D13/D14 presentation; backend owns required result/projection contracts and B::R-ACTIVITY/D5. |
| Shared session consumers | B::D1.1 and DEL-F01–F43 own canonical contract/producer semantics. One coordinated frontend consumer change handles type/owner/restore/deep-link/search/runtime retirement; do not silently move all engine/replay deletions into avatar work. |
| Backend dependencies | U1, main-only needs_attention/read semantics, one-cutover saved-chat/heartbeat migration, Admin exception, qualified cross-workspace addressing and merged task tools. Session-core owner publishes; this architect edits none of their implementations/specs. |
| Companion/later units | #1221 owns per-agent Auto-switch removal; R44/R45 header/Tooltip stay separate; full @ and /clear integrate with their backend unit; avatar uploads/GIFs are deferred. |
| Help/verification | Five named user-doc pages in D12; implementing leads draft matching changes, docs-verifier audits, QA proves real flows and independent validator checks exact-candidate hands-on testing. |

## Open questions for the founder

**Only Q-FE-11/Q-FE-12 remain open**, from the newly handed-over activity scope. Earlier Q-FE-1–Q-FE-10 are answered by F and the confirmed session-core decisions and are removed, not re-interviewed. Recommendations below are not decisions; answer “Q-FE-11 A, Q-FE-12 A”. Backend serialization/publication dependencies remain in D5, without a duplicate founder choice here.

### Q-FE-11 — What scope and detail does cross-session activity show? (A / B / C; new)

#493 requires visibility of genuine work outside the foreground chat, but does not choose a workspace-wide versus installation-wide view or full rows versus an elsewhere badge. This affects expectations when switching workspaces as well as chats; authorization and missing coverage must remain visible (D14; #493::Definition of done).

| Option | Presentation |
|---|---|
| A | Aggregate all authorized sessions in the current workspace, with an explicit elsewhere count/control for work in other workspaces. |
| B | One full aggregate across all authorized workspaces/sessions, labelled/grouped by workspace and source chat. |
| C | Keep foreground detail and add a truthful “N running elsewhere” indicator with navigation to the existing detail surfaces. |

**Recommendation: A**, keeping the existing workspace mental model without silently hiding other-workspace work. Backend snapshot coverage must support the chosen scope; no client-only completeness claim.

### Q-FE-12 — Which chats show a plan’s pill and parent-panel row? (A / B / C; new)

#1021 requires chat/parent-panel parity, but a plan’s internal owner session is not necessarily the human chat that started it, and plans created in the Tasks UI may have no chat origin. Picking one from agent identity or transport chat ID would misattribute the plan (D13; #1021::R1/R3; Plan::owner_session_id/source_chat_id).

| Option | Plan association |
|---|---|
| A | Pill/parent row in the actual starting chat; other sessions see it through the qualified cross-session aggregate. Plans without chat origin stay in workspace activity. |
| B | Pill/row in the owning agent’s workspace main, with starting chat access through the aggregate. |
| C | Labelled workspace-plan pills in every chat in that workspace, without claiming each chat is the parent. |

**Recommendation: A.** Use a real authorized start-origin relation where available; backend owner supplies any required existing-contract extension. No fake child edge, fabricated owner relationship or new plan engine.

skills: omnipus-shared-rules, omnipus-design-system, gitnexus-exploring, ux-heuristics-review, github-cli, commit-messages

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| This is the assigned baseline and one-file design scope | `git branch --show-current`, exit 0 → `work/adr-frontend-navigation-20261007`; `git rev-parse HEAD`, exit 0 → `c6837a42dcfb503fb34a0cea86cd3ccc451e7b9b`; initial `git status --short` empty; `git diff --cached --name-status`, exit 0 → one added ADR only. | Verified, high confidence. |
| All numbered requirements were traced; figure shorthand was not fabricated into a requirement | Controlled Python read of S1/S2, exit 0: `Requirement coverage: 46 numbered IDs; missing=[]`; positive control `R46 present=True`; `R-figures row=False`; `PERSON_ICONS` contains 31 roles. | Verified source, high confidence; open figure policy remains Unknown. |
| Current UI reuse and gaps are grounded in code | E1–E12::the exact source symbols/schema properties read; these are baseline facts, not implemented target behavior. | Verified source, high confidence. |
| Backend rewrite, contract ownership and later founder answers were checked | B::October 7 rewrite, D1/D7/D8/D10; S4::October 6/7 founder entries; backend branch read shows `work/adr-session-core-20261006`. Planned specification checked absent. | Verified source, high confidence; final wire shapes and implementation remain Unknown. |
| Existing ADRs were checked for overlap with a working search control | Controlled architecture scan, exit 0: `ADR_SCAN files_read=174 matched=54 positive_control=ADR-082-ui-independent-turns-and-session-bound-streaming.md`; relevant source clauses reread and cited by title above. | Verified source inventory, high confidence; not a runtime architecture test. |
| ID generator detects a real collision and minted the requested ID | Generator control “Credential Boot Contract”, exit 1, names existing collision; requested-title run, exit 0: `ADR-20261007-agent-first-navigation-and-agent-identity`. | Verified, high confidence. |
| No graph-derived impact or product verification is claimed | `gitnexus status`, exit 0 → `Repository not indexed.`; `gitnexus detect-changes --scope staged`, exit 1 → multiple other repository indexes require an explicit target. This checkout has no index; another checkout’s graph is not substituted. Direct source reads/searches and the single-file diff are the fallback. No production symbol is edited, and no product test/build/UAT is performed. | Verified limitation; future implementation impact is Inferred from E1–E12, not a graph result. |
| **Self-check** | Reread the complete staged ADR diff and the subsequent citation/reuse corrections against all seven commission areas, revised R4/R14/R22/R23/R24/R44–R46, B’s contract boundary, four-part publication, migration, later-only work and the three user-doc TODOs. Document validator, exit 0 → `D1-D12 + Q-FE-1-Q-FE-10 + A/B/C + recommendations + required sections OK`; missing-decision, unknown-requirement and stale-symbol controls were detected. Absolute references and unique date/slug verified; `git diff --cached --check`, exit 0; staged scope is the one ADR. | Verified document/source self-check, high confidence; no implementation or reachability PASS asserted. |
