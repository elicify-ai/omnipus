# ADR-20261007 — Agent-first navigation and agent identity

**Correction — 2026-10-07, the sole correction round:** at `554d21ffd` this ADR required avatars on messages, static chat icons and preservation of the old thinking indicator. Those rules are superseded by founder Q9: names-only bubbles and an animated indicator inline in the feed replace the old indicator; no composer status line. Founder Q5 selects Omnipus as default; Q6/Q7 defer avatar uploads/GIFs. The one review also exposed missing canonical seeding, refresh ownership and workspace-entry rules, addressed below. WIP checkpoints belong to this same correction round, not additional review/fix rounds.

**F — binding founder answers:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/FOUNDER-ANSWERS-20261007.md`::Q1–Q12, plus the direct team-lead steering in this correction conversation. Latest steering fixes Q9’s inline placement and approves `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/index.html` as the Q9 visual reference. The screenshots under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/` are prototype evidence, not product acceptance.

**Review:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/ADR-20261007-agent-first-navigation-and-agent-identity-review.md`::MAJ-001–MAJ-004, MIN-001–MIN-002. Reviewed commit `554d21ffd6963def6fa937865cb2b1e3df86f3c3`; exactly one grill and this one correction. Founder answers override review recommendations.

| Header | Value |
|---|---|
| Status | Proposed — founder-confirmed requirements recorded; remaining founder questions are Q-FE-1–Q-FE-10. Not an implementation-ready specification or a delivery claim. |
| Date | 2026-10-07 (UTC). |
| Decider | Daniel Piatkowski. Recommendations in the open questions are not decisions. |
| Author | Architect. |
| Size and scope | Feature-size frontend design. The first squad covers agent navigation, the shared agent icon, message avatars and static status line, and avatar choices in the existing create/edit slide-outs. Later work is identified separately in D11. |
| Code baseline | `work/adr-frontend-navigation-20261007`, based on `release/v0.1.1` at `c6837a42dcfb503fb34a0cea86cd3ccc451e7b9b`. |
| ID | Minted by `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/scripts/new-adr-id.sh` with the working title “Agent-first navigation and agent identity”. |
| Review boundary | This document awaits its one ADR-mode grill, the founder interview on that review, and one correction round. It does not run that review, write a specification, edit contracts, or authorize implementation or landing. |

## Context

The sidebar should answer **“who do I want to talk to, in which workspace?”**, not **“which thread was recently active?”** Clicking Mia opens Mia’s main session in that workspace. **Past sessions** finds older conversations; **+ New chat** deliberately starts a parallel one. Each agent keeps the same visual identity across the sidebar, Team, roster, messages and editor. Chat remains the main surface, with workspace tools opening the existing side panels. These are the revised requirements, not a new conversation store or a new agent-management system (R1–R8, R20–R24, R46).

This is a **design task**, not a production change. All R1–R46 identifiers were traced to the requirements source, including the October 7 revisions. The brief’s “R-figures” shorthand is not a separate numbered requirement: its source is requirements §5 question 15, read alongside R46 and the figure assets. The choice of product default and exact offered figure variants remains open; the wireframe’s default is not approval (S1::§5 question 15; S2::agentIcon; Q-FE-3).

### Sources and precedence

The source keys below expand to absolute paths. Requirement citations throughout this ADR refer to **S1**; evidence keys E1–E12 expand to the exact `file::symbol` citations in the next table.

| Key | Source and authority |
|---|---|
| S1 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/requirements.md` — R1–R46, §5 open questions, §7 documentation TODOs and §9 identity requirements. Revised requirements win over the prototype. |
| S2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/index.html`::sidebar, think-preview, fig-switch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/assets/agent-marks.js`::agentIcon, figureInner, PALETTE, toPalette; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/assets/person-icons-data.js`::PERSON_ICONS; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/components.html`::AgentIcon and AttentionIndicator gallery entries. Visual references, not production code or permission to copy their demo behavior. |
| S3 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/joint-delivery-plan-session-core.md`::Split of work, Order, Rules both sides follow. A draft joint plan; the direct frontend commission supersedes its “each step lands on its own” wording for this first squad. |
| B | **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D1, D7, D8, D10 Contract-first, Affected components. Its October 7 rewrite was read, not the obsolete “default session” draft alone. Backend runtime decisions remain there. |
| S4 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/CONTINUATION-20261005.md`::2026-10-06/07 founder entries, especially main-session mechanism, 12:44 navigation answers, 14:10 clear/hide decisions and the frontend/sidebar commission. Later founder answers supersede earlier draft language. |
| S5 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/CLAUDE.md`::Definition of Done, Hard Constraints, Spec-Driven Workflow; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/.claude/skills/omnipus-design-system/SKILL.md`::Publishing a component is a four-part contract, A recurring UI job uses a catalogued component. Governs delivery, generated contracts and component publication. |
| S6 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/AS-IS-architecture.md`::Contract-first wire-format pipeline; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/plugin-extensibility-assessment.md`::Side-by-side scorecard. Dated architecture context; current code and contracts win. No extension-runtime change is proposed here. |

The backend specification is planned at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/specs/session-core-spec.md`. It was **not present when checked** and is not cited as an existing specification. Backend U1 means the main-session/addressing delivery identified by S3, not a claim that U1 or its wire contract already ships (S3::Step 1; B::D1/D10).

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

**Source inconsistencies are explicit, not silently repaired.** S1’s objective, older R15/R19 wording and prototype animation still describe animated chat icons or a chat logo; revised R13/R14/R24 remove that logo and require static agent icons. R29’s detailed text specifies an icon pulse on agent rows, despite its acceptance summary still saying “dot”. R9’s no-face wording predates the geometric figure exploration and R46. D4/D6/D8 follow the newer instructions; remaining image/figure choices go to Q-FE-3/Q-FE-5. No older source file is edited by this ADR (R13, R14, R24, R29, R46; S2::agentIcon).

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

### D2 — Sidebar membership comes from agents, not session recency

Each expanded workspace lists its eligible main conversation agents. Use the existing agent taxonomy and worker predicate, joined to authoritative workspace membership. Do not manufacture membership from session history, a delegation edge, the existence of a computed main ID, or the global roster. Workers, including workers on a workspace team, never become sidebar conversation rows; hidden engine agents remain hidden (R3–R5, R7; E8; B::D1/D8).

Keep existing workspace ordering, archive access, sidebar pin/drawer behavior and keyboard/focus handling unless the later specification identifies an unavoidable change. Removing a session tree is not permission to remove its sessions, cancel running work, or redesign the shell. The global Agents entry still lists the full roster, including workers and unassigned agents (R6–R8, R21; E1; S5::Retired surfaces).

Chat remains the underlying workspace surface. Preserve the existing panel toggles and the separate global/workspace Library scopes; do not add a Chat header item or another panel system (R1–R2; E11).

### D3 — Main navigation and extra-chat creation are different actions

| Action | Target behavior |
|---|---|
| Click an agent’s name/icon row | Resolve and open that agent’s **main session in that workspace**, even if a newer extra chat exists. Reuse session attachment/navigation rather than changing the speaker inside the currently open session. R4; E2; B::D1/D8. |
| Past sessions | Open the existing SearchModal in session mode, initially filtered by **both agent and workspace**. Extend its existing opening/filter state; do not build another history modal. The user can broaden the filter to recover other saved sessions. R21–R22; E3. |
| + New chat | Start one extra chat with that main agent in that workspace. It does not replace, reset or relabel the main session. It is the sole extra-chat creation entry point. R4/R22; B::D8. |
| Sidebar magnifier / target `/sessions` command | Open the same general session search. Renaming the existing `/resume` entry changes its name, not its search behavior. R21–R22; E3/E4; B::D8. |

The selected agent row shows **Past sessions, then + New chat** on the right. Other rows reveal them on hover or keyboard focus; touch users must not depend on hover. They are separately labelled kit controls, not nested buttons that also trigger main-session navigation. Use product icons rather than storing the wireframe’s pictographs as UI labels (R22; S5::Brand & UI and component standard).

A row click must not silently choose the most recent session, fabricate a session record, start an extra chat, or fall back to a different agent when main-session resolution fails. Preserve visible attach failure and retry behavior. Ordinary workspace entry must not become another hidden extra-chat creation path; adapt existing generic New chat callers accordingly (R4/R22; E1–E3; B::D8). Missing main sessions are addressed in D10, not solved with a frontend-only ID map.

The requested first/later placement of command cleanup is inconsistent in the brief. Q-FE-8 resolves that cutover; no first-squad landing may violate the sole-entry-point rule or leave `/agents` opening the removed picker (R20/R22; E4; S3::Step 2).

### D4 — Waiting means the user has an outstanding action, not unread activity

Render the **agent’s own icon pulsing** when the backend says that agent awaits this user’s answer or approval in that workspace. There is **no extra dot on an agent row**. A collapsed workspace shows one **right-aligned yellow dot beside the expand caret**, labelled with the number of distinct waiting agents, not the number of sessions or messages (R29).

| Cue | Required presentation |
|---|---|
| Agent row | Gentle 18% scale with fading yellow `#EAB308` halo, 1.6-second loop; label/hover explanation “Waiting for your reply”. Remains visible on the selected row. R29. |
| Collapsed workspace | 8px yellow dot, the same pulse, right-aligned in the icon column; explanation “N agents waiting for your reply”. R29. |
| Reduced motion | Stop the loops; keep the icon/dot and meaningful text visible. R15/R29. |
| Clear condition | Backend waiting state ends. Merely opening the chat or expanding the workspace does **not** clear it. R29. |

Use the existing warning-yellow token for attention, not selection gold or a newly chosen orange. Identity color and attention color remain different things. The pulse is a reusable kit presentation, not independent animation logic on each screen (R16/R29/R41; S5::design-system rules).

**Do not infer waiting from the last message containing a question mark, an unread counter, an agent’s global active/idle status, or a child’s lifecycle label.** Main sessions, extra chats and helper approvals make those shortcuts ambiguous. The scope of the authoritative attention projection is Q-FE-2; initial load, updates and reconnect depend on D5. Uploaded-image pulsing is Q-FE-5 (R29; E8/E10; B::D10 Existing helper approval popup).

### D5 — Consume backend contracts; do not invent session wire fields

The session-core ADR’s **D10 Contract-first** and **Affected components → Contracts** are the shared contract authority. Backend-lead publishes the agreed schemas and generated Go/TypeScript types before consumers use them. This frontend ADR specifies the information it needs, **not new JSON property names, endpoints, frame types or an alternative session registry** (B::D10; S3::Shared interface; S5::Hard Constraint #8).

| Dependency | Frontend needs | State and owner |
|---|---|---|
| Main-session navigation — U1 | A server-validated association between the workspace/eligible agent and its existing main session, with enough generated session metadata to attach it safely. The founder-decided ID rule is **`main-session-<workspaceid>-<agentid>`**. That rule alone is not proof that a record exists or that membership permits opening it. | Open contract dependency on B::D1/D10; Q-FE-1. Backend owns resolution, existence, protection and visibility. |
| Agent-awaits-user — U1 interface agreement | Authoritative waiting state scoped to workspace and agent, available on first load and after reconnect, plus changes when questions/approvals are answered or withdrawn. It must also cover unopened/collapsed workspaces. | Open contract dependency on B::D10; Q-FE-1/Q-FE-2. Existing session lifecycle data is not claimed sufficient. |
| Static status line | Truthful state of the **open session**, including pending user actions and known execution activity. It must not display the state of another chat merely because the agent is the same. | Reuse generated session/execution/approval data where sufficient; unresolved state/copy mapping is Q-FE-7. |
| Persisted visual identity | Existing agent create/update/list/detail contracts need to express the approved figure/role/image choices and applicable field locks, not just today’s free-form icon and color. | Additional backend dependency for R46; not silently assigned to U1. Backend-lead edits/regenerates after the architect/founder resolves Q-FE-3–Q-FE-6. |
| Later group replies | Authenticated addressed recipient and actual per-message responding-agent identity, including live, replay and deleted-agent display. | Owned by B::D7/D10; only the later UI consumes it. No hand-written guest-message wire type here. |

Reuse existing query caches, session attachment and event invalidation. Roster refresh on connect/reconnect already exists at this baseline; preserve it rather than rebuilding S3’s now-stale U6 suggestion (E8; B::D8). Do not add a second “main sessions” store, subscribe one live chat connection per sidebar row, or read every main transcript to populate navigation. Request failures must not be represented as “nobody waiting” or “no members”; last-known data needs an honest unavailable/stale indication (E1–E3/E8; R29/R41; S5::Definition of Done).

The frontend can build kit and independent UI pieces now. Contract-dependent integration remains held until the **committed generated contract and working U1** are available on the joint candidate. A frontend stub or guessed field does not discharge this dependency (S4::frontend/sidebar commission; S5::Hard Constraint #8).

### D6 — One global visual identity per agent

An agent’s **role icon, figure and color** form one identity. An uploaded image is the alternative image presentation, cropped to a circle; it does not change the agent’s kind, instructions or permissions. Editing identity remains global across the agent’s workspaces, as in the existing unified editor. The prototype’s app-wide figure demo switch is not a production setting (R7–R12, R17, R46; E6; S2::fig-switch).

The role chooser uses the **31 roles in R11**, grouped as **Create / Build / Business / People / Personal**. Use R11’s curated duotone Phosphor role vocabulary, not arbitrary navigation glyphs or roles guessed from an agent’s name. The prototype’s regular-weight filled-figure/badge grammar does not silently supersede R11; its final combined grammar is included in Q-FE-3. Role/figure assets are trusted bundled graphics; accepting those assets does not permit user-uploaded SVG (R9–R11/R17; S2::PERSON_ICONS).

Names use normal product text color. The icon carries the agent’s color; standard selection styling does not recolor the identity glyph into brand gold. The icon and adjacent agent name identify the same agent everywhere (R10/R12/R13).

| Surface | Identity size/arrangement |
|---|---|
| Sidebar | 26px icon, then 13px name text. R13. |
| Team | 18px icon before the name. R13. |
| Agents roster | 40px icon before the name. R13. |
| Chat status line | 48px agent icon before the name/status. No second small icon and no separate Omnipus chat logo. R13/R24. |
| Every agent message | New AgentIcon in **today’s avatar position**, using that message’s author. Preserve the existing message layout/density rather than applying the 48px status-line size to messages. R24; E5. |
| Editor preview and `@` suggestions | The same identity renderer; no separate fixed Robot or colored-circle grammar. Exact unlisted size variants and small-badge treatment are part of Q-FE-3, not invented R13 values. R12/R46; E4/E6. |

Replace the existing avatar palette with the ten named R16 colors. These hex values are governed identity data, not permission to introduce arbitrary chrome colors (R16; E7; S5::Governed data is not chrome).

| Name | Hex | Name | Hex |
|---|---|---|---|
| Azure | `#3B82F6` | Purple | `#C084FC` |
| Sky | `#38BDF8` | Fuchsia | `#E879F9` |
| Cyan | `#22D3EE` | Pink | `#F472B6` |
| Indigo | `#818CF8` | Orange | `#FB923C` |
| Violet | `#A78BFA` | Grey | `#9CA3AF` |

The implementation must verify the required **3:1 graphic contrast** on sidebar/chat surfaces; this ADR records the requirement, not a measured contrast PASS. Gold, warning yellow, semantic greens/reds and Liquid Silver are not selectable agent colors (R16). Figure defaults, the remaining R9 face wording and small-size fallback are Q-FE-3. Exact color migration is Q-FE-6.

### D7 — Publish AgentIcon once through the kit’s four-part contract

**AgentIcon is one shared composite, not one avatar helper per screen.** It renders supplied identity and a named size variant; it does not fetch agents, decide membership, resolve sessions or own activity state. Its glyph lane is transparent. Its uploaded-image lane reuses the existing Avatar image/crop primitives, with appropriate fallback. Any required primitive variant is defined centrally, not a screen-level visual override or an import cycle (R10/R41/R46; E9; S5::component rules 1, 9, 14).

| Publication part | Required implementing change — not created by this ADR |
|---|---|
| Catalog/source | One composite entry in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/design-system/catalog.json`, naming AgentIcon’s implementation and public exports/types. Proposed new source: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/ui/agent-icon.tsx`. |
| Public barrel | Re-export the approved component/API from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/index.ts`. |
| Styling registration | Register its source explicitly in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/styles/library.css`; automatic class discovery is disabled there. |
| Verification manifest | Proposed new `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/design-system/manifests/agent-icon.json`, linked to stories and executed checks, covering unit, interaction, axe, keyboard, browser, pointer, reduced-motion, forced-colors, root-size, zoom and reflow. Inapplicable means genuinely inapplicable with a reason, never “not tested”. |

All four parts are required by S5::Publishing a component is a four-part contract. Story examples and visual checks must cover the approved figure/role/color combinations, every named size, image load failure and the reduced-motion attention use. A catalog entry without rendered/executed checks is not publication evidence (S5::manifest and Storybook rules; R13/R15/R16/R29).

Adopt it in sidebar, Team, Agents roster, SearchModal agent headers, live **and** historical/plain-fallback messages, status line, create/edit preview and existing agent identity displays in Activity. Later `@` suggestions use it too. Preserve external-worker/type labels separately; do not encode agent kind by inventing another avatar renderer. Non-agent terminal/activity symbols are not agent identities and need not be replaced (R12/R41/R46; E3–E6/E12).

Recurring presentation such as the sidebar attention cue also belongs in the kit. It composes with AgentIcon and the workspace dot; it must not become a second agent-identity component (R29/R41; S5::rule 14).

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

### D9 — New choices and live preview in the existing slide-outs

Extend the existing create/edit flow with **figure, curated role icon, palette color and uploaded image** choices. The live preview uses AgentIcon with the current draft choice, so it matches the sidebar/chat after save. AvatarHeader must stop rendering a fixed Robot. Do not use a prototype-only renderer for the preview or create another avatar settings store (R8/R46; E6/E9).

Preserve global autosave/create behavior and server-provided identity field protections. Protected built-ins show the same renderer without gaining an unauthorized edit control. New identity properties need corresponding generated contract/editability support; “locked” is not permission to guess which new fields are writable (R7–R8; E6/E10; **Built-in agent configuration, skills, and visual file reading**::What users and Ava may edit).

PNG, JPG, WebP and GIF are accepted image inputs; SVG is rejected; maximum upload is **2 MB**. The server must validate and re-encode the upload to strip metadata/embedded content before it becomes the saved avatar. A client-side file filter or local preview is not that safety guarantee. Failed upload/save keeps the previous saved identity and shows an actionable error; preview is not reported as saved (R17/R46; S5::Definition of Done).

Storage, ownership, replacement/removal and authenticated retrieval are a backend dependency, Q-FE-4. GIF playback and status-driven image motion conflict with the newer static-icon direction and are Q-FE-5. These cannot be silently treated as a frontend-only upload or a completed part of U1 (R17–R19; E10).

### D10 — Navigation migration preserves reachability, not obsolete data machinery

| Existing state | Required handling |
|---|---|
| Saved ordinary/extra sessions no longer listed in the sidebar | Keep them reachable through Past sessions and general SearchModal, including its existing Unfiled route and nested helper inspection. Do not rename, merge, delete or reassign their history as a side effect of changing navigation. R21/R22; E2/E3. |
| Main agent has no main session yet | Show honest resolution/loading/error state and use the backend U1 get-or-create/resolution behavior. Never choose the latest chat as a substitute or create a frontend-only main. R4; B::D1; D5. |
| Worker is a workspace member | Keep membership, task/delegation capability and access to existing worker sessions through current inspection/search surfaces. Hide only the fresh conversation row; do not create a worker main session or an `@` target. R5/R7/R28; E8; B::D1/D8. |
| Agent leaves/rejoins the team | Follow B’s hide/unhide visibility rule for its main session; existence on disk is not permission to bypass it through SearchModal. Ordinary-history reachability and hidden-main visibility are different rules. B::D1; Q-FE-9. |
| Older icon/color identity | Apply R11’s explicit obvious legacy-icon mappings; unmatched icons become General assistant. R16 requires one automatic nearest-palette color migration. Do not use the prototype’s name-keyword role inference or repeatedly rewrite colors while rendering. R11/R16; S2::toPalette; Q-FE-3/Q-FE-6. |

This ADR does **not** introduce archive conversion, a compatibility reader or heartbeat-to-main backfill in the frontend. B’s greenfield storage/deletion decisions and this commission’s “existing sessions stay reachable” requirement need a joint, explicit preservation boundary. Q-FE-9 is mandatory if U1/storage changes stop the server returning previously accessible records. A frontend test with only fresh sessions cannot prove that preservation requirement (R21; B::Code and compatibility paths to DELETE; S5::false-green guidance).

### D11 — Record later work; do not build it in the first squad

| Later surface | Recorded direction and dependency |
|---|---|
| `@` composer addressing and group replies | Main agents only, to their main sessions; no in-chat switch. Guest replies show the actual responder’s icon/name using AgentIcon. Consume B’s generated messaging/replay contracts; do not duplicate recipient authority, queue, context or wake decisions here. R23; B::D7/D10. |
| Session commands | `/resume` becomes `/sessions`; `/new` is deleted, not kept as an alias. Its minimum first-squad prerequisite is Q-FE-8 because + New chat must be the only extra-chat action. R22; B::D8. |
| `/clear` marker | Render the backend-owned safe-point clear/projection marker. Do not implement it by starting a new chat or deleting history. The exact model/display semantics are B::D8, including its later correction, not the obsolete “history stays on the current display” wording. S4::14:10 Q-R2-9; B::D8. |
| Header modes | R44’s measured available-space progression: labels → icons with kit Tooltip → workspace-name dropdown containing panels and Settings. One sidebar menu icon only. This is already in review according to the commission; this ADR records it, not a second header implementation. R1/R2/R38/R44; E11. |
| Tooltip replacement | R45’s shadcn/Radix interface replaces the old hand-built kit API, with its catalog/manifest/stories/callers updated in that work. No legacy-props wrapper or app-wide tooltip codemod is added by this first squad. R45; E11. |
| Admin entry | Later remove Assets’ Admin chat entry only with its replacement access path. Admin has no main session and no team membership under B; do not invent one to make the new sidebar simpler. First-squad removal of the composer picker must not remove the still-existing standalone Admin access. R25; E1/E8; B::D1; Q-FE-10. |

R26–R28’s older cross-workspace capability language does not authorize a frontend peer-messaging protocol. B::D7 and S4::13:50 Q-R2-6 restrict this group/peer design to the same authorized workspace. Backend capability/permission questions remain with B/the founder, not inferred from a list of visible icons (R23/R26–R28; B::D7).

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

S4::frontend/sidebar commission overrides S3’s independent-landing draft: frontend may build now, but its main-session navigation lands **together with U1 after a joint integration test**. Contract-first still applies within the work branches: frontend consumes committed generated types, not guessed shapes while waiting for the integration branch. Any additional avatar backend work must be explicitly assigned; calling it “frontend” does not make upload/config persistence disappear (S3::Shared interface; S5::Hard Constraint #8; D5/D9).

| Joint proof required before landing | Why it matters |
|---|---|
| Same agent in two workspaces; newer extra chat exists | Row click opens the correct main every time; + New chat leaves both mains intact. R4/R22. |
| Existing saved chats, worker member and absent main record | Search/inspection remains reachable; workers do not become rows; unresolved main never redirects into the wrong chat. R5/R21; D10/Q-FE-9. |
| Question and approval while a workspace is collapsed; selected row; answer/withdrawal; reconnect | Correct yellow attention and distinct-agent count; opening the chat does not clear it; state returns without reading every transcript. R29; Q-FE-1/Q-FE-2. |
| Same identity across Team/roster/sidebar/editor and live/replayed messages | Shared renderer and author identity, not four superficially similar avatar implementations. R12/R24/R46. |
| All approved figures/palette and upload limits/errors; reduced motion; root-size/zoom/reflow | Size/contrast and honest image failure, no chat icon loops or inaccessible row actions. R13–R19/R29/R46. |
| Safe command/picker transition and standalone Admin access | No secret extra-chat entry, in-session switch or now-unreachable operator. R20/R22/R23/R25; Q-FE-8/Q-FE-10. |

The implementing squad follows the feature specification/test-integrity/review gates in S5. Engine-touching joint work requires hands-on user acceptance testing on the **exact candidate commit**, with an independent validator, using the founder-set provider/model. This ADR performs none of those product checks. Keep **“code correct and tested”** separate from **“reachable by a user/agent”** (S5::Definition of Done and Change sizes and the review gate).

### User-facing documentation TODOs

These pages were read. The implementing leads update the matching sections in the same behavior change; docs-verifier audits them against the joint build. Backend/frontend authors coordinate overlapping page edits rather than publishing contradictory main/session instructions. No user-facing page is changed by this design-only task (S1::§7; S5::User-facing documentation).

| Page | Specific first-squad TODO; later additions stay tied to their delivery |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/using-omnipus-ui.md` | Replace the sidebar session-tree/workspace New chat tour with agent rows, main click and Past sessions/+ New chat; remove composer-agent-picker instructions; explain static status line, per-message identity, yellow attention and reduced motion. Move its existing `/new` delivery-loss warning to the row New chat action rather than losing that protection. Update command names only with Q-FE-8’s approved cutover. Later add group replies, clear marker, R44 header modes and Admin replacement. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/agents.md` | Explain role/figure/color/image choices, input types/2 MB/re-encoding, approved motion policy, shared global edits and protected built-in fields; workers hidden from navigation but existing worker sessions still inspectable. Replace dedicated-heartbeat/sidebar wording alongside U1 and remove handover claims alongside the backend cutover. Do not claim universal editable identity or group messaging before it ships. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/workspaces.md` | Replace “workspace expands to chats” and composer-picker guidance; describe main per eligible agent/workspace, extra sessions and Past sessions; document worker membership versus conversation eligibility and backend hide/unhide rules. Keep Team’s actual membership/edit/delegation duties unchanged. |

## Consequences

| Kind | Consequence | Basis / certainty |
|---|---|---|
| Positive | Main-agent navigation stays stable when extra chats or background work become newer. | R4/R22; D2/D3. Inferred design benefit, high confidence; not exercised. |
| Positive | One published AgentIcon prevents preview/live/replay/screen identity drift. | R12/R24/R41/R46; E5/E6/E9. Inferred benefit; publication/adoption still required. |
| Positive | Waiting attention survives a collapsed workspace and is not mistaken for “new unread text”. | R29; D4/D5. Target behavior, not a tested notification guarantee. |
| Negative | U1 alone may not supply attention aggregation or image/figure persistence. Additional contract work and founder answers are required before those items can be called complete. | E10; D5; Q-FE-1–Q-FE-7. Verified current-schema gap, high confidence. |
| Negative | Removing a sidebar history tree and composer picker changes learned habits. Past sessions must remain obvious and saved-chat/unsent-message recovery must not disappear. | R20–R22; E1/E3/E4; documentation TODOs. Inferred user impact, high confidence. |
| Negative | Role badges may be unreadable at small sizes; GIF/status motion and old archive preservation have unresolved source conflicts. | S1::§5 question 15; R14/R18/R19; B::DELETE table; Q-FE-3/Q-FE-5/Q-FE-9. Unknown final policy. |
| Neutral | Session storage, queues, permissions, lifecycle and group-message authority remain backend-owned. This is not another chat engine, worker type or identity service. | B::D1/D7/D10; E8/E10; S5::Hard Constraints. Design boundary. |
| Neutral | Existing Team/global agent-management duties, panel ownership, Stop behavior and product branding remain intact. | R1/R2/R7/R8; E6/E11/E12; **UI-independent turns and session-bound webchat streaming**. No runtime change certified. |

## Alternatives considered

| Alternative | Why rejected or not adopted |
|---|---|
| Open the most recent chat on row click | Explicitly superseded by October 7 R4; an extra chat would hijack the main-session destination. |
| Keep a sidebar session tree or add a session bar above chat | R3/R21 keep sidebar agents-only and use existing SearchModal. Historical/child inspection belongs there and in existing activity surfaces, not another navigation tree. |
| Keep picker/`@` switching the speaker in place | R20/R23 and B::D8 pin sessions to their agents. Later mentions are communication, not handover. Q-FE-8 controls the temporary cutover, not this target decision. |
| Compute the main ID in the browser and pretend it exists | The founder’s ID rule is not a membership/existence assertion. B::D1 owns get-or-create and validation; E2 already expects a real session. |
| Guess waiting from text, global active status or last seen session | Cannot distinguish an outstanding user approval from ordinary activity or waiting in another chat. R29 needs authoritative scoped state; E8/E10. |
| Copy prototype SVG-string renderers, animations and global demo switch into screens | Prototype retains superseded motion/defaults. R14/R41/R46 require static chat and one kit component; Q-FE-3 resolves defaults. |
| Independent local avatar helpers or a second Avatar primitive | Existing kit Avatar provides the image lane; the composite centralizes glyph rendering. E5/E6/E9 show the duplication being removed; R41 forbids another parallel UI job. |
| Fully redesign Agents, Team, all panels and all kit components now | Exceeds the first-squad commission and R43’s separate waves. Change shared identity only where this scope requires it. |
| Ship navigation before U1 with latest-chat fallback | Violates R4 and the founder’s joint-landing instruction. UI preparation can proceed, but unresolved integration remains visibly held. |

### Existing decisions affected — dated scope correction, 2026-10-07

| Decision by title | Narrow relationship |
|---|---|
| **Unified Slash-Command + Skill Menu (skill-as-command, partitioned palette)**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-026-unified-slash-command-skill-menu.md`::D7/D8 | Its `/agents` picker and in-chat switching direction is superseded by R20/R23 and B::D8. The unified skills/commands menu is reused, not replaced. Timing remains Q-FE-8. |
| **Unify delegate sub-turns onto the own-session execution path**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-057-session-parent-child-parity.md`::R-9 listing policy | R3/R21 replace only sidebar session-tree presentation. Existing child-session ownership and modal/activity inspection are not deleted by this frontend ADR. E1/E3 are the current implementation evidence. |
| **Built-in agent configuration, skills, and visual file reading**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-090-built-in-agents-skills-and-visual-reading.md`::Roster, What users and Ava may edit | Keep Admin standalone, hidden engine boundaries and protected identity fields; changing avatar rendering does not grant new identity edit rights. R46 extends supported choices within actual field permissions. |
| **UI-independent turns and session-bound webchat streaming**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md`::P1/P2, D3–D5 | Preserve its boundary: sidebar navigation is viewer attachment, not cancellation or transfer of execution. Live/replay identity must agree. |
| **Remove the “main” sentinel agent**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-064-remove-main-sentinel-agent.md`::Decision | “Main session” does not revive a synthetic agent named main or a fallback speaker. Use the real agent/workspace pair, R4 and B::D1. |

Older files are not silently amended in this one-file commission. The table flags exactly which UI clauses the new decisions supersede; broader backend amendments belong to B (S5::Project and scope boundary).

## Affected components

These are **future implementing targets**, not changed files in this ADR. E-key references resolve to the absolute source citations above.

| Area | Existing components / owner and intended adaptation |
|---|---|
| Navigation and selection | E1 Sidebar; E2 useSelectSession; E8 eligibility predicates. Frontend-lead replaces workspace session rows with agent rows and uses the existing attach/navigation path. |
| Past sessions | E3 SearchModal and UI opening/filter state. Frontend-lead adds scoped agent filtering, reuses existing session groups/child inspection and adopts AgentIcon. |
| Shared kit | E9 Avatar primitives plus D7’s proposed AgentIcon source/manifest; catalog/barrel/style registration. Frontend-lead publishes one composite and reusable attention presentation. |
| Identity data/editor | E6 shared form/wizard/profile; E7 palette; E10 generated agent contracts. Frontend-lead owns controls/preview; backend-lead owns persisted representation, field descriptors, color migration and safe upload support after founder decisions. |
| Chat | E4 OmnipusComposer/useSlashMenu; E5 live/historical avatars and existing thinking indicator. Frontend-lead adds static status line, removes picker/switch behavior at the approved cutover and preserves per-message authorship. |
| Team/roster/activity | E12 AgentNode/ActivityAvatar and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/screens/AgentListScreen.tsx`::AgentListScreen. Adopt shared identity without redesigning their unrelated jobs. |
| Backend session boundary | B::D1/D10 and S3::Shared interface. Backend-lead publishes U1 and generated contracts; frontend does not write backend addressing, queue or lifecycle code. |
| Later header/tooltip | E11 WorkspaceTabBar/Tooltip; separate R44/R45 delivery. Recorded, not first-squad duplication. |
| User help and verification | The three absolute user-doc pages in D12; implementing leads draft, docs-verifier audits, QA covers behavior and an independent validator checks the joint hands-on campaign. S5::Definition of Done. |

## Open questions for the founder

**Answer format: “Q-FE-1 A, Q-FE-2 B …”.** These are recorded questions for the founder interview, not choices delegated to an implementer. No recommendation becomes the default silently. Backend-owned questions are coordinated with B rather than answered twice (S1::§5; S5::Spec-Driven Workflow).

### Q-FE-1 — How does the frontend receive main-session and attention information? (A / B / C)

The current schemas provide a heartbeat session reference and per-session lifecycle, not the required main/agent-attention guarantee. A computed ID cannot establish existence or membership. This is a **contract dependency on B::D10**, and blocks integrated navigation/attention until agreed (E10; R4/R29).

| Option | Contract direction — property names deliberately unchosen |
|---|---|
| A | Extend existing workspace/member/session responses for a validated main association and attention snapshot; reuse existing live-update/reconnect mechanisms. |
| B | Resolve each main through existing session detail/list contracts using the computed ID, and derive agreed attention from those responses/updates; prove coverage for all unopened workspaces. |
| C | A new dedicated navigation response/update surface, only if A/B cannot express the requirements without excessive requests. |

**Recommendation: A.** Reuse existing records and initial-load/reconnect paths; backend architect fixes the precise shape in B, backend-lead edits/regenerates it. Do not invent frontend wire fields here.

### Q-FE-2 — Which outstanding user actions make an agent row wait? (A / B / C)

R29 covers questions and approvals, but does not say whether an extra chat or helper request contributes to its agent/workspace row. A main-only projection can hide an approval in another chat; an indiscriminate helper-state count can falsely ask the user to act (R29; B::D10 Existing helper approval popup).

| Option | Attention scope |
|---|---|
| A | Main session only. |
| B | Main plus extra chats of that agent in that workspace. |
| C | B plus genuinely user-actionable helper/run requests attributed to that agent/workspace, with distinct-agent counting and a route to the actual request. |

**Recommendation: C**, using existing question/approval identities, not every helper `needs_input` state. The backend must define aggregation and clearing; the sidebar remains a projection, not another approval owner.

### Q-FE-3 — Which figures, default and small-size rendering are approved? (A / B / C)

R46 makes figure choice part of agent editing, but §5 question 15 leaves the default and small-size fallback open. The assets contain six figure variants and several face variants; the current demo offers four filled figures and defaults to Robot. R9 still says no faces. This must be resolved before persisting new choices or backfilling missing figures (R9/R11/R13/R46; S2::PERSON_ICONS, FIGURE_FACE).

| Option | Figure policy |
|---|---|
| A | Offer the four demo figures — solid Robot, Man, Woman, Omnipus — with their minimal eye grammar; default Robot; retain figure-plus-role at all specified sizes. |
| B | Same four and minimal-eye grammar, default Omnipus; use the plain role icon at small sizes where the badge is not legible, with the cutoff specified by the founder. |
| C | Choose another offered/default figure set, face policy or small-size cutoff explicitly; no prototype variant becomes available merely because its SVG exists. |

**Recommendation: A** as the smallest consistent set matching the current demo; verify legibility in the design-system stories. This also replaces only R9’s conflicting no-geometric-face wording, not its ban on illustrated mascots. The recommendation is not a brand decision made by this author.

### Q-FE-4 — Where do uploaded agent images live and who removes them? (A / B / C)

An agent identity is global across workspaces and outlives any one chat. R17 explicitly leaves storage open. Session-attached storage can tie identity to unrelated session deletion; workspace storage can make the same agent’s identity differ between workspaces (R8/R17; **media:// Ref Lifecycle and Store Invariants**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-017-media-ref-lifecycle.md`::D5).

| Option | Ownership/lifetime |
|---|---|
| A | Agent-owned application files, referenced through the existing agent configuration, replaced/removed with identity changes and cleaned up by the agent deletion path. |
| B | Durable application-global library/media objects referenced by the agent, with explicit replacement/deletion ownership and authenticated access. |
| C | Defer saved uploads to a later squad; revise the first-squad R46 scope explicitly instead of showing a local-only upload as saved. |

**Recommendation: A**, reusing backend image-validation/re-encoding and file primitives where suitable, not introducing an upload service/runtime. Exact paths, retrieval contract and cleanup are backend design work; this ADR does not claim an existing upload handler already satisfies R17.

### Q-FE-5 — How do GIF and uploaded-image motion fit the static-icon decision? (A / B / C)

R18 permits hover/active GIF playback, R19 forbids status animation of the image itself, R29 scales the agent icon, and newer R14/R24 require static chat icons. Animating a saved GIF or scaling the uploaded image for waiting cannot be chosen by copying the prototype (R14/R17–R19/R24/R29).

| Option | First-squad motion policy |
|---|---|
| A | Accept GIF as a server-re-encoded still first frame for now; images stay static everywhere. Waiting on an image uses the yellow halo without scaling image pixels; ordinary glyph icons retain R29’s scale pulse. |
| B | Allow hover GIF playback only outside chat, never active-status playback or reduced-motion playback; use a static image with halo-only waiting attention. |
| C | Deliver R18’s hover/active playback and image scaling now; explicitly amend R14/R19 and confirm the allowed surfaces, including chat. |

**Recommendation: A** for this static first squad. It requires the founder’s explicit temporary R18/R29 image exception; it is not already decided. Keep full animation requirements on record for later, not secretly implemented.

### Q-FE-6 — What exact nearest-color migration should run once? (A / B / C)

The founder has decided automatic migration to R16’s palette. The prototype matches hue and maps low-saturation colors to Grey, but its thresholds and tie behavior are not themselves confirmed requirements. Different algorithms can noticeably change an existing identity (R16; S1::§5 question 13; S2::toPalette).

| Option | One-time mapping |
|---|---|
| A | Ratify the prototype’s hue-distance/low-saturation-Grey rule, with deterministic cutoff/ties documented and fixture examples approved. |
| B | Use a perceptual color-distance rule, with the metric and examples approved before implementation. |
| C | Founder supplies explicit mappings for legacy colors; unlisted colors await a decision rather than being silently remapped. |

**Recommendation: A.** It matches the reference’s intended migration without a new color-classification system. Persist once through backend-owned configuration; no render-time rewrite or permanently parallel palette.

### Q-FE-7 — What does the permanent status line say outside its three example states? (A / B / C)

R24 names thinking, working and waiting, but requires visibility in every state. Today’s session lifecycle has stopped/interrupted/done and can be absent; it does not distinguish thinking from tool work by itself. An agent-global active flag would mislabel another open chat (R24; E5/E8/E10).

| Option | State/copy policy |
|---|---|
| A | Use truthful existing session/execution/user-action data; add neutral ready/stopped/interrupted/loading/unavailable phrases, and use thinking/working only when the available data supports the distinction. |
| B | Use a smaller session-state vocabulary without the thinking-versus-working distinction; amend R24’s examples accordingly. |
| C | Founder supplies the full phrase/state table and any missing backend exposure before the dependent status-line behavior is specified. |

**Recommendation: A**, with the phrase table confirmed in the founder interview. Preserve existing connection/error and thinking-indicator surfaces; do not create a new backend activity-state machine solely for this text.

### Q-FE-8 — What is the safe first-squad command/mention cutover? (A / B / C)

The brief places `/new` removal in both the first sidebar scope and the later command scope. Today `/agents` opens the soon-removed picker and `@` switches the speaker; leaving either alive contradicts pinned chats. Full group messaging is expressly later (R20/R22/R23; E4; B::D8).

| Option | Cutover |
|---|---|
| A | First squad includes minimum cleanup: remove `/new`, generic extra-chat bypasses and dead picker/switch openers; withdraw switching `@` suggestions until group messaging is ready. Rename `/resume` to `/sessions` with this same cutover so Past sessions and help use one name. Group-reply UI and `/clear` remain later. |
| B | Include the complete `@` messaging UI/backend and command changes with this first joint delivery; explicitly expand the first squad. |
| C | Keep UI preparation on the branch but hold navigation/picker removal landing until the later messaging/command unit is ready; no interim old switching behavior ships with the new navigation. |

**Recommendation: A**, as a narrow, explicit prerequisite exception to the “later” list. Backend registry/help and frontend interception must agree. Preserve the existing unsent/delivery-loss protection on + New chat; deleting a command must not delete that safeguard.

### Q-FE-9 — What saved-session preservation boundary does the joint cutover guarantee? (A / B / C)

The frontend commission requires existing sessions to remain reachable. B removes old storage/conversion/compatibility paths and hides removed-member main sessions. The UI cannot recover records that the server no longer returns or authorize opening a deliberately hidden main. This is a real joint acceptance decision, not a frontend migration algorithm (R21/R22; B::D1 and Code and compatibility paths to DELETE).

| Option | Preservation boundary |
|---|---|
| A | Require the navigation/U1 candidate to keep every previously supported ordinary/extra session reachable through existing search/inspection. Deliberately hidden main sessions follow B. Any storage-format break remains a separately founder-approved backend cutover; failure of this joint check blocks this landing. |
| B | Authorize a narrowly defined backend conversion/reader to preserve older archives in the same cutover, explicitly changing B’s greenfield boundary. |
| C | Accept a defined older-format reachability break, with backup/export guidance and user documentation; explicitly revise this brief’s preservation requirement. |

**Recommendation: A.** Do not make a blanket legacy-data promise or smuggle a second reader into the frontend. Team removal is visibility, not deletion; re-add behavior remains owned by B. The scope of unsupported historical formats must be named before acceptance.

### Q-FE-10 — What replaces Admin chat in workspace navigation? (A / B / C; later)

R25 removes the Assets entry but leaves its replacement open. B explicitly gives Admin no team membership and no main session. Treating Admin as an ordinary main row or mention target would contradict that boundary (R25; S1::§5 question 10; E1/E8; B::D1).

| Option | Replacement entry |
|---|---|
| A | A workspace-accessible operator action opens Admin’s existing standalone chat path, clearly distinct from main-agent navigation. |
| B | An explicit special Admin sidebar row in each workspace, using the standalone operator path rather than a fabricated main session. |
| C | `@admin` only, requiring an explicitly approved backend exception to main-session-only addressing and a discoverable entry for users who do not know the command. |

**Recommendation: A.** Preserve current standalone access in the first squad; choose and test its workspace replacement before removing the Assets entry in the later unit.

skills: omnipus-shared-rules, omnipus-design-system, gitnexus-exploring, ux-heuristics-review, github-cli, jev-use:jev-use, commit-messages

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
