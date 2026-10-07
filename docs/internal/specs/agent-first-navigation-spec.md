# Feature Specification: Agent-first navigation and agent identity

**Created**: 2026-10-07 (UTC)
**Status**: Draft — WIP; founder questions and dependency checks remain. Not implementation approval.
**Size**: Feature. One frontend specification; backend U1 and this frontend land jointly.
**Input**: Corrected **Agent-first navigation and agent identity**, D1–D14, at `b6fd39efb5bc4ee5bb77cb6f16be5baeec34265e`, with the newer founder answers below.
**Worktree**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav`
**Branch**: `work/adr-frontend-navigation-20261007`

## Discovery, authority and confirmed requirements

This is specification work from an approved design, not a new architecture decision or a production implementation. A person chooses a colleague within a workspace, returns to the exact last conversation on workspace entry, and deliberately starts extra conversations only from that colleague's row. The open chat keeps its own activity panel. The existing Sessions modal is the overview across conversations, including background work; there is no new dashboard.

The recorded founder interview and explicit wireframe approval satisfy discovery confirmation for the settled scope. This document does not re-interview those decisions. Unanswered choices are recorded under **Questions for the founder** for team-lead's interview. The ambiguity gate remains open until the founder resolves or explicitly acknowledges every warning; a completed draft is not a finalized or approved spec.

### Source register and precedence

Source keys expand to absolute paths. Cite decisions by title plus decision heading, never an ADR number alone. Latest founder answers override older requirements, ADR wording and prototype labels. The prototype is visual reference, not delivered functionality or authority for its demo controls.

| Key | Binding source and verified baseline |
|---|---|
| N | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/architecture/ADR-20261007-agent-first-navigation-and-agent-identity.md` — **Agent-first navigation and agent identity**, D1–D14, corrected/Proposed, at `b6fd39efb5bc4ee5bb77cb6f16be5baeec34265e`. This spec does not amend or re-grill that ADR. |
| F | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/FOUNDER-ANSWERS-20261007.md` — Q1–Q12, Q9 FINAL, wireframe approval, **Q-FE-11 and Q-FE-12**. These last two are answered, not open choices. |
| R | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/requirements.md` — R1–R47, including subrequirements R6a–R6c and R30a–R30b. The source-disposition table below records narrower first-squad scope and supersession. |
| M | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/session-modal-review/review.md` — A1–A7 and D1–D8. A2's Needs me option and D8's repeated-helper collapsing remain founder choices. The review's existing-app evidence is not a new approved modal wireframe. |
| W | **Only approved visual reference:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/index.html`; Q9 reference screenshots in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/`. Its adjacent assets supply approved figures, palette and motion. Missing screens go under Wireframe additions needed; this task changes none of them. |
| B | **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, branch `work/adr-session-core-20261006`, D1.1 at `b76b412f61ff73d9a6643d34518224472d84223a`; immutable evidence copy `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/spec-evidence/backend-adr-b76.md`. Later migration/Admin/cross-workspace answers in F/N override conflicting older clauses. |
| BS | Published backend specification `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/specs/session-core-spec.md`, verified from pushed branch `work/adr-session-core-20261006` at `0e1fececc5df91c140e21fd27cf03bfd0ee3c8dc`; immutable evidence copy `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/spec-evidence/backend-spec-published.md`. Publication is not proof of generated contracts or working U1. |
| K | Tasks-panel squad, branch `work/tasks-panel-layout-20261007`, observed pushed tip `7946ac5f00163fa036033e75f329fcab2193bdaa`. Reuse its kit FilterMenu, ViewSwitch and shadcn HoverCard; do not build duplicates. Exact integration exports and publication readiness are dependency checks, not assumed APIs. |
| P | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/CLAUDE.md` — Definition of Done, Hard Constraints, Spec-Driven Workflow, review gates and user-facing documentation; current code wins over dated architecture descriptions. |

### Discovery summary

| Topic | Confirmed requirement |
|---|---|
| Actors | Person using the web interface; eligible conversation agents, workers and protected built-ins; backend owners supplying validated session destinations and state. Workers remain inspectable through legitimate existing sessions, never sidebar conversation agents. |
| Problem | Session recency and a composer picker obscure the stable colleague/workspace destination. Background work disappears when the foreground chat changes. Identity differs between editor, lists and chat. |
| Main walkthrough | Expand workspace → select agent's main → use Past sessions for a specific older conversation or + New chat for an extra → inspect the open chat's Activity panel → open Sessions for work in other chats. |
| Constraints | Preserve permissions, protected identities, saved history and existing execution control. No inferred waiting from prose; no silent empty/zero state on failed data. Respect reduced motion and shared-component standards. |
| Integration | Existing workspace membership, session search/attachment, agent editor, chat rendering, plan/task inspection, live updates and shared kit. Backend U1, canonical identity persistence and published contracts are release dependencies. |
| Priority | P0 for correct destination/send gating and saved-chat continuation; P1 for identity, attention and activity reachability. No new deadline or release line is invented. |
| Human evaluation | In a joint build, a person reaches the intended main/extra/history, sees truthful identity and state, finds background work from another chat, and follows real plan/run links. Screenshots of the prototype alone cannot satisfy acceptance. |

### Latest-answer corrections applied to the design

| Older wording | Binding specification rule |
|---|---|
| N D14's unanswered aggregate scope; old Q-FE-11 options | **F Q-FE-11:** Activity side panel shows only the open session. Improve the existing Sessions modal for the overall view; no aggregate side panel or elsewhere badge is commissioned. |
| N D13's unanswered plan association | **F Q-FE-12:** Pill and parent-panel row belong to the actual starting chat; modal carries cross-session access. A plan created only in Tasks has no invented chat origin. |
| Avatars on bubbles; static/composer-linked indicator | **F Q9 FINAL:** Name-only agent bubbles, with animated agent/name/phrase inline where the old thinking indicator stood. No separate composer status line. |
| Main interface awaiting a backend spec | BS is now pushed at the recorded tip; consume its agreed main/attention direction. Generated-contract and runtime dependencies remain open until verified. |
| Images/GIF controls in R46 | **F Q6/Q7:** No uploads or GIF behavior in this squad. Figure/role/color only; future image safety requirements are retained as later work. |

## Scope and delivery boundary

| First-squad capability | Decisions and boundary |
|---|---|
| Navigation, restore and freshness | N D1–D5: agent rows, main click, exact remembered chat/welcome-main entry, independent Past sessions and + New chat actions, main-only attention, persistent refresh ownership without the removed picker. Keep workspace ordering/archive/pin/drawer and established focus behavior. |
| Shared identity and existing editor | N D6–D10: four approved figures, 31 role badges, ten-color palette, one shared renderer, global create/edit preview, canonical built-in and one-time custom identity migration. Protected fields retain their locks. Only identity presentation changes in the existing Agents roster and Team. |
| Chat and immediate cleanup | N D3/D8/D11: persistent kind label, name-only messages on every rendering path, inline animated responder indicator, no composer agent picker; remove old switching/new-chat commands and rename the session command without an alias. |
| Plan/task visibility | N D13 plus F Q-FE-12: starting-chat plan pill, successful start-result links, open-session panel rows and real Tasks/Graph/session drill-down. Existing plan/task execution remains backend-owned. |
| Overall activity | N D14 **as narrowed by F Q-FE-11**, M A1–A7/D1–D8: improve the one existing Sessions modal; keep workspace → agent → session grouping and real child hierarchy. No second overview, aggregate side panel or synthetic session relationships. |
| Joint release | N D12: independent kit/UI preparation may proceed, but agent navigation lands with working backend U1 after joint integration and the founder's approval. Main-only attention, saved-chat upgrade migration, default-workspace Admin, canonical identity and required activity/start-origin contracts must be on the same tested candidate. |

### Later and companion work — recorded, not commissioned here

| Item | Disposition |
|---|---|
| R6a–R6c, broader R37/R38 Agents-page redesign and new creation interview | Later rollout wave. Keep existing roster/editor behavior; apply shared identity here and retain the Agents/New agent naming already required. Do not copy the prototype's new manual/Ava creation flow. |
| R17–R19 avatar upload/GIFs | Deferred by F Q6/Q7. Future unit retains format/SVG rejection/2 MB/server normalization plus pre-decode pixel/work/concurrency limits; animation placement needs founder approval. No upload endpoint, control or preview-only persistence substitute here. |
| R23/R26–R28 full addressed group messaging and cross-workspace collaboration | Backend/later UI units. Explicit workspace + agent address, actual responder, immutable chat owner; no workers offered. For this cutover, old mention switching and suggestions are unavailable. Existing legitimate guest messages still render their actual author. No cross-workspace plans. |
| Complete safe-point clear behavior | Backend session-core unit. This frontend never makes Clear create a chat or delete persisted history; consume that unit's approved behavior when integrated. |
| R30–R32/R34/R36–R40 tool policy/loading/skill categorization, Connectors and broader pages | Later waves. Navigation/identity work must not silently add permission editors, settings moves, skill classifiers or page redesigns. Relevant shared kit styles are reused only where needed here. |
| R33/R35/R39/R41 kit foundation | Use or publish the minimum shared jobs required by this spec. No app-wide migration. FilterMenu/ViewSwitch/HoverCard reuse K; single-choice versus multi-choice semantics follow the kit, not a locally invented dropdown. |
| R42/R43 hardening and full rollout | Separate rollout/check-hardening work. No ratchet or test weakening to hide debt here. |
| R44 measured-space header and R45 Tooltip migration | Companion units. Preserve their behavior; no second header fix or legacy Tooltip wrapper. M D5 explicitly leaves existing native tooltips until R45; introduce no new hand-built tooltip. |
| Per-agent Never auto-approve removal | Companion #1221. Do not restore that obsolete checkbox or wire field; preserve still-supported composer model/Auto/attachment/send/Stop controls. |

A pending founder choice gates only its dependent behavior, not unrelated settled kit preparation. A missing published wire representation gates every consumer of that representation. Neither a draft spec nor a WIP push authorizes production work or landing.

## Reachability

Reachability is checked before correctness gates. This is a frontend feature, not a new agent tool: no new builtin registration or policy grant is proposed. Existing native plan/task tools and backend main addressing still require their owners' integration evidence.

| Actor and entry point | Reachable outcome | Acceptance proof on the joint candidate |
|---|---|---|
| Person: workspace disclosure and agent row | Eligible colleague's validated main in that workspace, even when an extra is newer. Workers and hidden engine agents do not become rows. | Select the same agent in two workspaces; verify different validated mains and the real send destination. |
| Person: workspace name, login entry or workspace switch in Sessions | Exact valid remembered main/extra; otherwise the validated Ava welcome main. | Open, leave and return to each kind; test missing/hidden/error destinations without silent new-chat creation. |
| Person: row Past sessions | Existing Sessions modal with both workspace and agent filters visible and removable. | Open an older conversation for that pair, then broaden filters and reach another workspace. |
| Person: row + New chat | Deliberate extra owned by that pair; main remains reachable by row click. | Start an extra, send once, return to main and find the extra in Sessions. |
| Person: sidebar magnifier or session slash command | The same Sessions modal listing authorized chats/helpers/runs across workspaces; no second activity screen. | Find work started in chat A while chat B is foreground; follow its real parent or run. |
| Person: existing Agents/New agent, existing Team/edit controls | One editor with global figure/role/color choices and a matching live preview; built-in locks unchanged. | Create and edit a custom agent; see matching identity in its workspaces after reload and backend restart. |
| Person: start-result link or starting-chat plan pill | Actual plan through existing Tasks/Graph; task/helper through existing session inspection. | Navigate the returned entity live and after reload, without guessing a result address or opening the main instead of a task run. |
| Person: open-session activity control | Existing Activity panel for that open session only. | Switch from A to B: A's work leaves B's panel but remains reachable in Sessions. View changes do not stop it. |
| Person: Admin in default workspace | Server-validated Admin main before removal of the old Assets-only entry. | Open it and send on the joint candidate; no fake team membership or additional Admin mains. |
| Agent: existing task/plan execution and structured questions | Existing authorized tools feed truthful frontend metadata; no new execution path or granted authority. | Backend owner demonstrates actual tool results, scheduler-only runs and structured question/approval/goal state; frontend follows their published handles. |

**Code correct and tested:** not claimed by this document-only task. The Test-Driven Development Plan describes future executed gates.

**Reachable by a user/agent:** not yet delivered. The entry points above must be exercised against the exact joint implementation commit before landing.

## Available Reference Patterns

The optional generic Go index requested by plan-spec is absent: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/reference/go-implementation/00-overview.md`. A reference-directory control found `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/reference/built-in-tools.md`; no generic infrastructure is invented to fill the missing index. This is frontend presentation over existing services, not new auth/storage/payment infrastructure.

| Reference | Applicable pattern and adaptation |
|---|---|
| P::Contract regeneration; N D5; BS::Contract Changes | Extend existing boundary contracts first, regenerate and consume generated types. Do not invent another session/address/attention store or copy backend structs into frontend code. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/.claude/skills/omnipus-design-system/SKILL.md`::Publishing a component is a four-part contract; A recurring UI job uses a catalogued component | Kit publication: catalog, public barrel, style source registration, manifest/story/executed evidence. Prefer existing primitives and ported shadcn jobs; no screen-local identity/status/pill copies. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/sessions/SessionTree.tsx`::SessionTree, flattenSessionTree | Keep genuine parent/child nesting and virtualized/plain rendering; change session-modal row presentation, not hierarchy ownership. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/internal/false-green-patterns.md`::A substring scan is not a behavioural test; A hardcoded allowlist decides what CI runs | Mount real consumers, assert user-visible outcomes and request destinations, prove test discovery and mutation sensitivity. A document check proves only document coverage. |

Conservative type design: no new nominal type without an invariant or domain meaning; generated wire types remain mandatory. The optional `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/reference/conservative-type-design.md` is not assumed present or cited as read.

## Existing Codebase Context

GitNexus MCP tools are not connected in this session. CLI status reports **Repository not indexed**; change detection for this exact checkout reports repository not found. No graph from another checkout is substituted and no heavy re-index is run. The following context is source-read; planned impact is **Inferred**, not a graph risk verdict. Implementers must repeat upstream impact analysis or the controlled caller sweep before each production edit.

### Symbols involved

These C-keys are exact source citations, reusable throughout this spec. They describe existing seams, not delivered target behavior.

| Key / symbol | Role and current source context |
|---|---|
| C-NAV | Modify `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/layout/Sidebar.tsx`::Sidebar. Existing selection calls C-SELECT; replace recent-session expansion with eligible main-agent rows. Preserve shell navigation rather than create a new sidebar. |
| C-SHELL | Extend `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/layout/AppShell.tsx`::AppShell and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useAgentsCrossTabRefresh.ts`::useAgentsCrossTabRefresh. AppShell mounts the one SearchModal; AgentPicker is currently the only production refresh-hook mount. Move that responsibility, not its recovery behavior. |
| C-SELECT | Modify `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/useSelectSession.ts`::useSelectSession, attachAndSeed. Current cross-workspace path writes workspace before checking attach success. Joint selection must commit a consistent destination, not retain that partial-write ordering. |
| C-RESTORE | Modify `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/session.ts`::enterWorkspaceChat, resolveRememberedSessionFromServer. Existing browser-scoped pointers and late-result guards are useful; blank/fresh/error fallbacks and mutable-owner precedence must be replaced by N D3/BS. |
| C-MODAL | Extend `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/search/SearchModal.tsx`::SearchModal, AgentSessionList, AgentHeader, SessionRow, handleSwitchWorkspace, bucketByAgent, sortSessions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/ui.ts`::useUiStore.openSearchModal. Existing flat metadata search, Unfiled and child nesting stay; add agent prefilter and M's presentation/activity information. Workspace switching currently starts fresh; apply N D3 instead. |
| C-TREE | Call existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/sessions/SessionTree.tsx`::SessionTree, flattenSessionTree. Plain fallback and virtualization already exist; do not invent a separate activity hierarchy. |
| C-FEED | Replace `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ChatScreen.tsx`::ThinkingIndicator, InlineThinkingIndicator; adapt AssistantMessage, VirtualAssistantMessageRow, AssistantMessageAvatar and OmnipusComposer. Existing rotating phrases/context-specific labels are presentation inputs; scalar old goal state is not retained as a second truth source. |
| C-EDITOR | Extend `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/agents/AgentFormFields.tsx`::AvatarHeader, AvatarColorPicker, IconPicker. Current header hardcodes Robot; use the shared preview and preserve existing editor/wizard consumers. |
| C-ACTIVITY | Extend `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useRunningActivity.ts`::useRunningActivity; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ActivityBar.tsx`::ActivityBar, ActivityPill; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/chat/ActivityPanel.tsx`::ActivityRow. Foreground spans/shells exist; task/run/plan metadata must be integrated, not replaced by a new activity service. The current hook also reads session-agnostic judge verdicts: attribution is required before claiming those belong to the open session. |
| C-PLAN | Call/adapt `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/workspaces/WorkspaceTasksTab.tsx`::handleSelectPlan. Current plan selection sets the plan and Graph view. Starting-chat links need a real hand-off into that workflow; existing local state alone is not a working deep link. |
| C-COMMAND | Adapt `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/hooks/useSlashMenu.ts`::useSlashMenu and existing session-modal opener. Coordinate registry/help/generated command consumers with BS rather than retain frontend aliases. |
| C-WIRE | Consume `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/Session.yaml`::properties and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/contracts/components/schemas/Agent.yaml`::properties through generated types. Baseline has heartbeat/mutable owner and color/icon, not the commissioned main/attention/figure shape. |

### Impact assessment

`d=1` means direct consumers; `d=2` means their affected user workflows. These are controlled textual caller/source inferences, not GitNexus WILL BREAK/LIKELY AFFECTED results. The saved sweep is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/spec-evidence/caller-sweep.log`.

| Planned modified symbol group | Risk / certainty | d=1 consumers to update or test | d=2 / d=3 critical paths |
|---|---|---|---|
| C-NAV/C-SHELL / refresh hook | Broad shell impact; Inferred | AppShell's Sidebar/SearchModal, removed AgentPicker mount, shared agent/member query observers | Focus/visibility within stale time, reconnect, navigation from non-chat pages, drawer/touch keyboard behavior. |
| C-SELECT/C-RESTORE | Concurrency-sensitive; Inferred | Sidebar and SearchModal selection, WorkspaceTabContainer entry, standalone session route and attach/replay | Late response after another selection, cross-workspace failed attach, saved-pointer reload, pending first-send delivery, send/Stop correlation. |
| C-MODAL/C-TREE / modal store action | Cross-session search impact; Inferred | Existing sidebar and slash openers, workspace-switch mode, filtered tree and visible-row keyboard navigation | Child-only matches/orphans/large trees, protected delete/rename focus, Unfiled inspection, activity metadata pagination and live refresh. Shared flattening need not change unless required by these oracles. |
| C-FEED/C-COMMAND | Multiple rendering paths; Inferred | Live message and historical virtual/plain rows, composer and slash menu | Replay/guest attribution, hidden-tool phrases, non-stream error handling, model/Auto/attachment and first-send safeguards. |
| C-EDITOR / shared AgentIcon | Multi-surface identity; Inferred | Profile/wizard preview, sidebar, Team, roster, modal headers and Activity identity | Fresh/migrated identities, built-in locks/reseeding, all sizes/colors, reduced motion and screenshot/contrast gates. |
| C-ACTIVITY/C-PLAN | Presentation/ownership-sensitive; Inferred | ActivityBar/Panel, real plan-result hand-off, Tasks/Graph and session inspection | Starting-chat association, scheduler-only work, task-child dedupe, monitoring versus Stop authority, reload/unopened-session coverage. |

### Relevant execution flows

| Source-derived flow — not a graph process name | Required preservation |
|---|---|
| Shell → workspace/agent selection → existing validated attach → replay → displayed chat | One committed workspace/session/owner; newer intent wins, main click is not recency. |
| Magnifier/command/Past sessions → one SearchModal → filtered metadata/tree → existing inspection | Genuine children stay under their parent; expanding/searching is not read acknowledgement. |
| Agent edit → backend configuration/canonical seed → shared roster → identity presentation | Preview is not saved truth; reload/restart must agree and locks remain server-owned. |
| Plan/task start or scheduler → backend plan/run metadata → open-chat activity and Sessions → existing drill-down | No synthetic parent edge, no duplicate execution item, no new controller/executor. |

### Cluster placement

No indexed cluster/process names are available. Source-based placement spans shell/navigation, session/chat, agent identity/kit and plan/task inspection. That cross-tree footprint requires the feature gate and joint U1 acceptance, not independent navigation landing.

## User Stories & Acceptance Criteria

These stories describe observable behavior only. Acceptance numbers are stable trace keys; technical mechanisms belong to the later integration/test sections. Open choice branches are not silently enabled.

### User Story 1 — Choose a colleague, not a recent thread (Priority: P0)

A person selects the colleague they want within a workspace and always reaches that colleague's main chat. Newer parallel chats must not redirect that choice.

**Why this priority**: A wrong destination can send the person's message to the wrong conversation.
**Independent Test**: Give Mia mains and newer extras in two workspaces; select each row and inspect the actual destination.

1. **Given** Mia has a main and a newer extra in each of two workspaces, **When** her row is selected in one workspace, **Then** its main opens and that row is selected, not the newer extra.
2. **Given** a team contains eligible colleagues, workers and hidden engine agents, **When** the workspace is expanded, **Then** only eligible colleagues appear; Admin appears only through his validated default-workspace exception.
3. **Given** a usable shell, **When** an existing workspace tool is opened, **Then** it uses the existing panel system, chat stays the base surface, and the distinct global Library/Agents destinations remain reachable without adding a Chat header item.
4. **Given** roster or membership refresh fails, **When** the person expands the workspace, **Then** first-load error is not an empty team; last-known data is visibly stale with Retry and no guessed chat target.

### User Story 2 — Return to the exact chat safely (Priority: P0)

A person returning to a workspace resumes the conversation they actually left. On a first visit or a confirmed invalid saved destination, they reach the workspace's validated Ava welcome main instead of a blank extra chat.

**Why this priority**: Restore must not silently create work or change the intended recipient.
**Independent Test**: Remember a main and then an extra in different workspaces; exercise normal return, cold reload and refused attachment.

1. **Given** a valid visible remembered main or extra, **When** the person enters the workspace from login, its name or the modal workspace switch, **Then** that exact chat and immutable owner return.
2. **Given** no saved real chat or a confirmed deleted/hidden/inaccessible one, **When** the person enters the workspace, **Then** the backend-validated Ava welcome main opens; an absent eligible destination shows Team/manage and Retry with sending unavailable.
3. **Given** session/member loading or attachment fails, **When** entry is attempted, **Then** the intent remains retryable, the last committed selection stays consistent, and no unresolved-target send or silent default/new-chat fallback occurs.
4. **Given** a first selection resolves after a second selection has won, **When** the old result arrives, **Then** it cannot replace the active destination or another workspace's remembered chat.

### User Story 3 — Deliberately start extras and find older chats (Priority: P1)

A person keeps a stable main while deliberately starting extra chats or finding history through the same Sessions modal. These actions must not accidentally select the row's main or erase a message still being delivered.

**Why this priority**: Parallel conversations remain useful without reintroducing the old session tree.
**Independent Test**: Activate both row actions by keyboard and touch, send the first extra message once, and broaden history filters.

1. **Given** a selected colleague/workspace, **When** + New chat is activated, **Then** an extra for that pair starts while the main remains intact; the established first-send delivery/abandonment safeguards remain effective.
2. **Given** several agents and workspaces have history, **When** Past sessions is activated, **Then** the existing modal opens visibly filtered by both row agent and workspace, with filters that can be broadened.
3. **Given** an extra is open, **When** its owner's row is selected, **Then** the main opens; selected-row actions are visible, other-row actions work on hover/focus/touch, and Past sessions precedes + New chat.
4. **Given** opening a past chat is refused or disconnected, **When** its result is selected, **Then** the refusal is visible and retryable without closing into a false successful switch or losing the committed chat.

### User Story 4 — Know when a main needs attention (Priority: P1)

A person sees which main chats need an answer, approval or review of a finished/failed goal, including in collapsed workspaces. Activity elsewhere must not masquerade as that main's waiting signal.

**Why this priority**: Missing a real question or displaying false attention both undermine trust.
**Independent Test**: Drive each permitted source in an unopened main plus negative sources in extras/helpers and inspect both expanded/collapsed navigation.

1. **Given** a main has a pending question, pending approval, unseen finished goal or unseen failed goal, **When** its authoritative state becomes visible, **Then** its own icon pulses and a collapsed workspace shows the warning-yellow dot; unrelated extra/helper work, generic unread text and user-stopped goals do not light it.
2. **Given** outstanding questions/approvals and observed goal outcomes, **When** a person explicitly opens the main, **Then** only observed goal attention is acknowledged for everyone; questions/approvals await resolution and a newer racing goal remains unseen.
3. **Given** attention is present and reduced motion is enabled, **When** navigation is rendered, **Then** loops stop, cues/text remain, no separate agent-row dot appears, and collapsed-workspace counts count distinct main agents.
4. **Given** attention data is missing, incomplete or failed, **When** navigation refreshes, **Then** unavailable/unknown coverage and Retry remain visible, never fabricated false or zero.

### User Story 5 — Recognize one consistent identity (Priority: P1)

A person recognizes the same figure, role badge and color in lists and responding-agent indicators. Small icons must not quietly become a different visual identity.

**Why this priority**: Stable recognition avoids confusion across workspaces and guest replies.
**Independent Test**: Compare all four figures across the named sizes and surfaces with the approved reference.

1. **Given** an agent's approved figure, role and color, **When** sidebar, Team, roster, modal header, Activity identity or inline indicator displays it, **Then** one transparent figure-plus-role rendering precedes its normal-color name; Omnipus is the creation default and no small-size badge fallback is substituted.
2. **Given** the identity choices are shown, **When** a person selects a color or role, **Then** only the ten named colors and 31 roles in the five approved groups are offered, with graphic contrast at least 3:1 on sidebar/chat surfaces.
3. **Given** legacy custom and built-in identities, **When** the approved one-time migration and repeated restart complete, **Then** saved identities use the approved palette/role/default grammar and stay stable; canonical seeding does not restore forbidden colors.
4. **Given** identity loading fails, **When** a surface renders cached identity, **Then** it does not claim a new saved identity or a removed agent from a failed request; unknown identity stays honest.

### User Story 6 — Preview and edit through the existing flow (Priority: P1)

A person creates or edits an agent through the existing unified slide-outs, previews exactly what navigation/chat will show, and knows the edit applies everywhere the agent is used.

**Why this priority**: A separate editor or local-only preview would produce incompatible identities.
**Independent Test**: Change each editable identity choice in the existing flow, save and reopen it in two workspaces.

1. **Given** a custom agent's existing create/edit flow, **When** its figure, role or color is changed, **Then** the live preview matches the shared identity and global save/create behavior remains; no upload or GIF control is offered.
2. **Given** a protected built-in with server-defined editability, **When** its editor is opened, **Then** fixed identity remains locked and allowed settings retain their established editability; a visual change grants no capability.
3. **Given** a create/save fails or is not activated, **When** the person submits or autosave settles, **Then** the real error/save status remains visible and a draft preview is not presented as saved everywhere.

### User Story 7 — Read a clear, truthful chat feed (Priority: P1)

A person sees which chat is open and who actually responds, without bubble avatars or a second composer status line. Model/Auto, attachments, send and Stop keep their established behavior.

**Why this priority**: Feed identity and sending identity must agree even through replay or guest responses.
**Independent Test**: Render live, historical virtual and plain messages plus an actual non-owner producer and reduced-motion mode.

1. **Given** a main, extra, task or helper is inspected, **When** its feed is rendered, **Then** the persistent above-feed kind is truthful; agents' bubbles show actual author names only on every rendering path, never avatars or a renamed task/helper as Extra chat.
2. **Given** a real responding agent and working/thinking/waiting state, **When** the next reply position is displayed, **Then** its shared animated icon/name/phrase replaces the old thinking indicator inline; reduced motion leaves stable meaningful text without loops.
3. **Given** the navigation cutover, **When** composer and command choices are opened, **Then** no agent picker, mention switching, old new-chat command, picker command or old resume alias is available; the session command opens the existing modal and Clear never starts a chat.
4. **Given** a real reply/connection error or active helper tree, **When** the person uses the existing recovery or Stop control, **Then** errors and current control scope remain truthful; changing chats or closing a view never cancels work and no unsupported composer control is restored.

### User Story 8 — Understand sessions and their hierarchy (Priority: P1)

A person uses the existing Sessions modal to understand chats, helpers and runs across workspaces, without confusing a stopped helper with completed work or losing a child underneath search filters.

**Why this priority**: This is the founder-selected overall activity view, not optional duplicate navigation.
**Independent Test**: Open the modal with every lifecycle/kind plus real parent/child, orphan and protected-main fixtures.

1. **Given** authorized sessions of each supported lifecycle and kind, **When** Sessions opens, **Then** each row shows a truthful status chip and quiet kind, a main's confirmed attention dot, and a title line plus muted status/kind/active/tokens metadata line; no HB abbreviation remains.
2. **Given** an agent's main, extras and real helper/run children, **When** its group is revealed, **Then** the main is first/pinned, extras retain established recency order, children remain beneath their true parent, and an unresolved parent does not silently hide its accessible orphan.
3. **Given** desktop, phone, keyboard or assistive input, **When** the modal is explored, **Then** the title is Sessions, subtitle explains chats/helpers/runs, shared group/row/count/identity/search/filter controls are used, focus and independent actions work, and no new handmade tooltip/view appears.
4. **Given** a protected main, inaccessible/deleted destination or failed list source, **When** a delete/open/list action is attempted, **Then** protection/refusal/error is explicit, no hidden session is opened, and an outage is not Unfiled/empty data.

### User Story 9 — Find current work without reopening the modal (Priority: P1)

A person narrows Sessions by existing title/workspace/date and new row-pair filters, sees current activity while it remains open, and distinguishes a complete overview from missing pages or stale data.

**Why this priority**: A stale or partial list claiming no running work would recreate issue #493.
**Independent Test**: Open Sessions in chat B, update work in unopened chat A, filter it and inspect the live result without closing the modal.

1. **Given** authorized running and inactive conversations, **When** the person applies the settled activity filter, **Then** matching sessions appear with their true parent context and other filters compose; the exact Running/Needs me choices await Q-M1/Q-M3 and must not be guessed.
2. **Given** Sessions remains open with an unchanged title search, **When** background work changes state or membership changes, **Then** status, attention, plan presence and results reconcile without reopening; focus/reconnect also recover missed updates.
3. **Given** a page/source is missing, delayed or failed, **When** Sessions loads or refreshes, **Then** visible partial/unknown coverage and Retry replace any complete/zero claim; cached conversations alone do not prove unseen work is covered.
4. **Given** more results than a single page/viewport and a child-only title match, **When** the person searches or moves the keyboard highlight, **Then** the matching child remains reachable under its parent with truthful counts, bounded rendering and a usable plain fallback.

### User Story 10 — Follow plans and actual task runs (Priority: P1)

A person starting work from a chat sees its plan there, follows a successful start result to the real entity, and can find the same starting-chat association later in Sessions.

**Why this priority**: Accepted starts are not proof of running work or usable drill-down.
**Independent Test**: Start one plan and one task, reload and follow each retained link to the actual Graph/run session.

1. **Given** a plan was started from chat A, **When** A's activity is displayed, **Then** its pill/parent-panel row is associated with A and its starting row in Sessions; B does not claim to be the starter, and Tasks-only plans get no fabricated chat origin.
2. **Given** an approved-not-started, running, paused or terminal plan, **When** its state updates, **Then** pill/row state reflects that state, approved means waiting to start, and progress/phase comes from real reported data rather than an optimistic start acceptance.
3. **Given** a successful or idempotent plan/task start with an authorized destination, **When** Open plan or Open task session is activated, **Then** the real plan opens through existing Tasks/Graph or the returned task-run session opens through existing inspection, live and after replay.
4. **Given** missing origin/address metadata, deleted entity or refused drill-down, **When** a pill/result is used, **Then** unavailable/error guidance is visible and no prose-derived address, foreground-session substitute or synthetic parent link is invented.

### User Story 11 — Keep the panel local and the overview global (Priority: P1)

A person monitors the open chat's actual plans/tasks/helpers/shells locally and finds other chats through Sessions. Seeing an independent task does not grant the current chat authority to stop it.

**Why this priority**: This enforces the two newest founder answers and preserves execution ownership.
**Independent Test**: Start work in A, view B's panel and Sessions, and verify a scheduler-only task plus the same MAIN task represented as a child.

1. **Given** work in A and B, **When** B's Activity panel is opened, **Then** only work legitimately associated with B appears; A remains findable in Sessions, without an extra dashboard or aggregate side panel.
2. **Given** task/scheduler/helper/shell records include a task also represented as a real child, **When** the open chat's activity is reconciled, **Then** distinct work counts once with correct kind/state/source; queued/waiting is not counted as executing, and independent runs gain no tree-Stop control.
3. **Given** a missing/stale task/run/plan source or an unattributed global verdict, **When** activity renders, **Then** incomplete coverage is explicit, no false zero or foreign-session row is shown, and Retry uses existing recovery paths.

### User Story 12 — Continue saved chats and reach Admin after cutover (Priority: P0)

An existing user upgrades without losing saved ordinary, extra, Unfiled, child or former heartbeat conversations. Admin's replacement destination works before the old entry disappears.

**Why this priority**: Fresh-fixture demos cannot prove a safe navigation/storage cutover.
**Independent Test**: Upgrade a known supported saved installation, reopen and continue its chats, restart again and open Admin's default-workspace main.

1. **Given** supported saved chat and heartbeat histories, **When** the backend-owned upgrade completes, **Then** each legitimate chat remains reachable and continuable, heartbeat history is in its validated main, and repeated upgrade does not duplicate or erase history.
2. **Given** Admin's validated default-workspace main exists on the joint build, **When** his sidebar row is opened, **Then** it is reachable without fake membership or extra workspace mains; only then may the obsolete Assets-only entry be removed.
3. **Given** migration/import or destination resolution fails, **When** saved history is opened, **Then** visible retryable failure replaces empty-success history, no legacy owner/type fallback bypasses the unfinished backend, and no integrated delivery is claimed.

## Behavioral Contract

| Flow | When / Then contract |
|---|---|
| Main selection | When an eligible colleague's row is selected, the system opens that pair's validated main, not the most recent chat. |
| Workspace entry | When a valid visible remembered chat exists, the system restores that exact chat; otherwise, after confirmed invalidity or no pointer, it opens the validated Ava welcome main. |
| Failed resolution | When validation/loading/attachment fails, the system keeps a retryable intent and consistent committed selection; sending to an unresolved destination is unavailable. Failure is not deletion. |
| Parallel/history | When + New chat is used, the system starts a deliberate extra with delivery safeguards; when Past sessions is used, the one Sessions modal opens with the pair's removable filters. |
| Main attention | When an authoritative main question/approval or unseen finished/failed goal exists, the system signals that main only. Explicit open acknowledges observed goals for everyone; answers/decisions resolve questions/approvals. |
| Identity | When an agent is shown outside a message bubble, the system uses its shared transparent figure/role/color identity and normal-color name. Bubbles show only the actual author's name. |
| Responding state | When an agent responds, the system shows that responder's animated icon/name/phrase inline in the feed; reduced motion keeps text and stops loops. |
| Overall activity | When Sessions opens, the system shows authorized chat/helper/run metadata and real relationships across workspaces, updating while open without taking control of those sessions. |
| Local panel | When a chat's Activity panel opens, the system shows only work actually associated with that open chat. Changing chats never stops or completes work. |
| Plans/links | When a plan starts in chat A, the system associates its pill and parent row with A and exposes the real plan/run through authorized existing drill-down, including replay. |
| Missing coverage | When metadata, pagination, origin, identity or authorization is unavailable, the system shows unknown/partial/error and recovery, never fabricated zero, destination or success. |
| Upgrade | When the supported cutover migration succeeds, saved chats stay reachable and continuable; when it fails, the system reports the failure rather than displaying empty history as success. |

## Edge Cases

Each EC key has a dedicated Edge Case scenario below; error recovery also appears under its parent story.

| Key | Boundary / unusual condition | Expected behavior |
|---|---|---|
| EC-01 | Same colleague in two workspaces; newer extras; same display names but different identities | Validate the pair and immutable owner, not name or recency. |
| EC-02 | No real remembered pointer, pending first send, hidden/deleted main, no eligible welcome agent | Pending delivery is not a saved main; use validated entry or honest unavailable state, without silent new extras. |
| EC-03 | Two rapid selections; old response/attach fails after a newer intent | Newer intent wins; no split selection or pointer overwrite. |
| EC-04 | Question and goal overlap; another person opens the main; newer goal races the open | Only observed goal attention clears globally; pending question/approval and newer outcome remain. |
| EC-05 | Invalid/missing hex, saturation just below/equal/above one quarter, circular-hue wrap/tie | Apply the approved one-time mapping, stable published-order tie rule and Grey fallback; no render-time recoloring. |
| EC-06 | All four figures at smallest/largest named sizes; reduced motion/forced colors/zoom | Figure and role badge remain present; contrast, text and input access remain, without changing glyph grammar. |
| EC-07 | Guest author, replayed message, no virtualizer support, truthful task/helper kind | Same actual author and names-only bubble; no owner switch or kind substitution. |
| EC-08 | Child-only search hit, missing parent, more than 20 visible rows, same titles | Preserve accessible parent context/orphan, truthful total and bounded rendering; repeated helpers stay individually accessible unless Q-M2 is approved. |
| EC-09 | Unopened workspace work, missed update, missing list page, zero-token/no-state rows | Reconcile authorized snapshots and mark gaps; show unknown or zero according to actual data, never infer absent execution. |
| EC-10 | Same run appears for starter/assignee and as helper; schedule has no foreground start call | Count distinct real work once; preserve actual associations and independent-control boundaries. |
| EC-11 | Tasks-only plan; duplicate/idempotent start; deleted/inaccessible drill-down target | No invented starting chat or duplicate work; validate the actual target and show unavailable/refusal. |
| EC-12 | Supported saved ordinary/Unfiled/child/heartbeat data; repeated upgrade or import error | Backend conversion keeps continuation and truthful binding, is repeat-safe, and surfaces partial/failure rather than using dual frontend readers. |

## Explicit Non-Behaviors & Safeguards

### Qualitative prohibitions

| The system must not… | Reason / authority |
|---|---|
| Reintroduce a sidebar session tree, composer picker, in-chat owner switching, new chat alias or a second Sessions view | R3/R20–R24; F Q10/Q-FE-11. |
| Create a main from a browser-made address or pick a random/latest agent when resolution fails | N D3/D5; backend owns validation and permissions. |
| Treat viewing/closing/navigating as execution cancellation, outcome completion or a grant of control | N D12–D14; selected-session/tree controls stay distinct from monitoring. |
| Infer waiting from question marks, every unread message, extras/helpers or global activity | F Q4/Q4b; four main-only sources, not a transcript heuristic. |
| Put avatars back on bubbles, add a composer status line or keep duplicate old thinking dots | F Q9 FINAL. |
| Add upload/GIF controls, an app-wide figure switch, demo controls, new creation interview, new task engine or aggregate side panel | F Q5–Q7/Q-FE-11; N D1/D7/D11–D14. Prototype simulations are not product settings. |
| Fake parent edges to nest peer/independent runs, or fake a plan's starting chat from its owner | N D13/D14; F Q-FE-12; B D5. |
| Mark incomplete or failed queries as empty/complete/zero, or normalize missing current identity into success | N D2–D5/D10/D14; BS contract-first and failure semantics. |
| Make locked identity editable, widen tool policy, or use a view's workspace to authorize foreign execution | P Hard Constraints; N D9–D11; backend owns authority. |
| Duplicate K's FilterMenu/ViewSwitch/HoverCard, app-wide tooltip fixes or header fixes | User commission; N D7/D11; M D5/D6; R41/R44/R45. |

### Machine-verifiable constraints

These are observable frontend constraints, not new backend status/error definitions. Existing published error envelopes remain authoritative; no untraced HTTP status or error-body shape is added.

| Category | Constraint / numeric oracle | Source |
|---|---|---|
| Identity choices | Exactly four figures, creation default Omnipus; exactly 31 grouped roles and ten named palette colors. Figure + role at every named size; zero bubble avatars. | N D6/D8; F Q5/Q9; R11/R16. |
| Identity measurements | Sidebar icon 26 px/name 13 px; Team 18 px; roster 40 px; inline responder 48 px. Icon precedes name outside bubbles; graphic contrast ≥3:1 against sidebar `#111113` and chat `#0A0A0B`. Editor/modal list-size additions need wireframe confirmation, not invented dimensions. | N D6; R13/R16. |
| Sidebar attention | Agent icon scale peak +18%, fading warning-yellow `#EAB308` halo, 1.6 s loop; zero separate agent-row dots. Collapsed workspace dot 8 px, beside caret on right; distinct-main-agent count only. | N D4; R29. |
| Inline motion | Working scale 0.97–1.03 / 1.6 s glow-pulse / 2.4 s sheen; Thinking scale 0.96–1.01 / opacity 0.55–1 / 2.6 s; Waiting peak 1.07 / 3.4 s. Idle static; reduced motion zero loops everywhere. | N D8; W adjacent approved motion assets. |
| Semantic labels | Sessions modal title `Sessions`; above-feed `Main chat` or `Extra chat — title`; real task/helper kind retained. General attention accessible label `Main chat needs your attention`; reason wording only with authoritative reason data. | N D3/D4; M A3/D7. |
| Navigation | Zero sidebar worker/hidden-engine rows; row click creates zero extras; unresolved target allows zero sends; no late-result override of the winning selection. | N D2/D3; B D1.1. |
| Activity ownership | Panel has zero unrelated-session rows; starting-chat plan association only; no duplicate actual run across task/helper representations; no queued/waiting item counted as executing. | F Q-FE-11/Q-FE-12; N D13/D14; B D5. |
| Accessibility | Every row action has an independent accessible name/keyboard target, no nested buttons; coarse-pointer adjacent controls have non-overlapping targets at least 44 px. Normal text/focus stay governed by the kit; color/motion alone never communicates state. | P design-system rule; N D3/D7; M D3/D4. |
| Scope/counts | Active metadata counts are uncapped by the eight-item recent-finished display limit. More than one page is not complete; list search includes permitted descendants, not only roots. | N D14; C-ACTIVITY/C-MODAL/C-TREE. |
| Prohibited input paths | Session command has no old resume alias; no new-chat/picker/mention-switch path; Clear creates zero chats and deletes zero saved history. | F Q10; N D11; BS. |

No new response-time, memory, session-title-length or pagination maximum is approved by the sources. Existing limits and virtualization are preserved; missing performance targets are explicit unknowns, not fabricated success criteria.

## Prerequisites

| Topic | Source-grounded prerequisite |
|---|---|
| Hardware / OS | Existing supported Omnipus install on Linux, macOS or Windows; a supported browser. No new RAM/CPU minimum is set by this frontend work. P::Tech stack and platforms. |
| Development runtimes | Existing repository Node/package-lock toolchain; TypeScript/React/Vite from the lockfile. Backend candidate uses Go 1.26.6 minimum, no CGo, required build tags. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/go.mod`::go; P::Build, test, and quality gates. |
| Services | Existing Omnipus gateway and authenticated account; working U1 and required generated contracts for joint tests. No new database, external activity service or plugin runtime. |
| Network | Browser access to gateway REST/live connection; offline/disconnected/stale behavior is tested explicitly. UI metadata tests need no external model request; real agent acceptance needs the configured provider. |
| Accounts / credentials | Preserve existing gateway authentication/authorization. Agent-driven onboarding/UAT uses founder-set `openrouter` + `deepseek/deepseek-v4.1-flash`; keys stay in existing credential storage, never in fixtures/reports. P::UAT provider/model. |
| Shared components | Integrate K's published FilterMenu/ViewSwitch; HoverCard publication is pending at the observed tip. No dependency is replaced by a local duplicate to unblock a demo. |

## Development Setup

These are future implementing-lead steps, not execution receipts for this spec. Run from the assigned checkout, never the original repository or another squad's working copy. Install/build work is scheduled by team-lead under the shared-machine rule.

| Step | Exact command or required action | Source / expected result |
|---|---|---|
| 1 | `git -C /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav status --short --branch` | Confirm `work/adr-frontend-navigation-20261007`; do not reset/checkout another squad's branch here. |
| 2 | `npm --prefix /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav ci` | Locked frontend dependencies; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/Makefile`::spa-embed uses `npm ci`. No guessed package versions. |
| 3 | Backend-lead publishes schema + regenerated artifacts; team-lead prepares the joint candidate with U1 and K components. | BS::Contract-first proof; P::Contract regeneration. Frontend does not run consumers against guessed main/identity/activity types. |
| 4 | `npm --prefix /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav run dev` | Existing development server; script is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/package.json`::scripts.dev. Connect to the existing gateway; do not invent a second application backend. |
| 5 | For an installed candidate: `omnipus start`, then open its configured address. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/docs/getting-started.md`::How to go from nothing to your first conversation. Default install address is `http://localhost:5000`; a campaign's allocated port may differ. |
| 6 | Exercise the Reachability table first; write/execute the test plan in controlled RED/GREEN/CHECK steps. | Real user invocation on the joint candidate, not a prototype demo or standalone hook mock. |

**Expected first-run behavior**: Existing authenticated shell and setup remain. After implemented cutover, normal workspace entry resolves its validated destination; the magnifier opens the one Sessions modal. This is target behavior, not claimed behavior of today's branch.

**Common first-run failures**: Missing required Go build tags, absent SPA embed directory on a fresh worktree, occupied gateway port, missing generated main/figure/activity contracts, or unavailable provider for live agent acceptance. Use P's documented handling. Do not mistake an unbuilt SPA or failed backend dependency for an approved empty chat. Full local Go build/suite and broad frontend suites are forbidden; CI/remote cluster owns heavy gates. No install, build or product test was run in this writing task.

## Tech Stack

Versions below are manifest requirements, not fabricated exact installed versions. Keep the committed lockfile as the installation authority; this spec introduces no runtime dependency.

| Category | Choice / version in source | Source |
|---|---|---|
| Frontend language | TypeScript `^6.0.3` | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/package.json`::devDependencies.typescript. |
| UI / routing / shared state | React 19; existing AssistantUI, TanStack Query/Router and Zustand | Same manifest::dependencies/peerDependencies; P::Tech stack. No competing store/router. |
| Build / styling | Vite `^8.1.5`; existing Tailwind/shadcn/Radix kit and generated design tokens | Same manifest::devDependencies; design-system skill. New visual jobs require four-part publication. |
| Backend runtime | Single Go binary, Go 1.26.6 minimum, pure Go; build tags `goolm,stdjson` | P Hard Constraints; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/go.mod`::go. |
| Storage | Existing file-backed agent/session/configuration storage; browser remembered-chat pointer remains navigation preference, not backend truth | N D3/D5/D9/D10; BS. No new database or second archive. |
| External APIs | Existing gateway REST/WebSocket and generated validation; configured provider only for real execution acceptance | BS::Contract Changes; P Hard Constraint #8. No SPA use of an agent-facing job-listing tool. |
| Unit/component testing | Vitest `4.1.11`, Testing Library, existing jsdom/test helpers | Same manifest::devDependencies; existing test files listed under Regression Test Requirements. |
| Integration/end-to-end testing | Existing Playwright `^1.61.1`, real gateway and candidate data; static Storybook `10.6.0` kit checks | Same manifest; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/playwright.config.ts`::defineConfig. |
| New infrastructure | None | N D1/D5/D7/D13/D14. |

## Deployment / Runtime

| Topic | Required deployment behavior |
|---|---|
| Target | Existing self-hosted Omnipus runtime on supported Linux/macOS/Windows, with the SPA embedded in the same binary. No new server, worker or browser-only main registry. P Hard Constraints; N D5. |
| Delivery | U1 + dependent generated contracts + frontend + shared kit + migration/canonical identity on one joint integration candidate. Independent UI preparation is not an independently shippable navigation release. N D12. |
| Start / stop | Use existing installed `omnipus start` and existing operational lifecycle. This spec adds no runtime command; chat Stop is not server shutdown. Existing operation instructions remain outside navigation implementation. |
| Online / offline | Gateway metadata/attachment needs a connection. Disconnection retains honest last-known data and unresolved intent; reconnect recovers snapshots without acknowledging goals or stopping live work. |
| Resource limits | No new per-row connections/pollers, full-transcript downloads for navigation or second activity/session store. Existing list paging and virtualized/plain paths remain; no new RAM/CPU guarantee is invented. |
| Startup / restart | Backend owns one-cutover saved-chat import and canonical identity seeding. Repeat startup must not undo mapped identities or erase history; test real restart, not only a React remount. |
| Health / logs / telemetry | Use existing load/attachment/error diagnostics and authenticated metadata smoke checks. No new telemetry; report query/contract coverage gaps visibly. Do not log credentials or claim a rendered welcome screen proves migration readiness. |

## Integration Boundaries

### Gateway sessions, membership and U1

| Boundary aspect | Required contract and failure behavior |
|---|---|
| Data in | Authorized workspace/member associations, session identity/kind/immutable owner/protection, real hierarchy and validated welcome/Admin destinations. |
| Data out | Explicit selected/remembered destination and existing authenticated attach intent; extra-chat creation only through the deliberate row action. |
| Published shape consumed | B D1.1: `Session.type = main` replaces heartbeat; current-format type is required; `Session.id`, `workspace_id`, `agent_id` and computed `protected`; `WorkspaceMemberConfig.main_session_id` readOnly for eligible members; heartbeat keeps enabled/interval/body, drops session_id. Computed ID is `main-session-<workspaceid>-<agentid>` but frontend never manufactures it as proof of existence/access. |
| Admin / ownership | Later BS/F override b76's Admin omission: default-workspace main through existing Session responses, no fake membership. Immutable `Session.agent_id` owns grouping/sending; `Message.agent_id` is the actual author. Delete coordinated mutable `active_agent_id`/handover consumers, not genuine guest attribution. |
| Failure | Missing/invalid/inaccessible association is unavailable; failed load is unknown, not missing. Keep single winning selection intent and last committed tuple; send only after validated workspace/session/owner attachment agrees. |
| Development | Generated-contract fixtures for UI/controlled races; real gateway for joint navigation, protection, upgrade and attach acceptance. A mock does not prove U1 works. |

### Main-only attention and explicit open

| Boundary aspect | Required contract and failure behavior |
|---|---|
| Data in | **BS::C-ATTENTION** publishes `Session.needs_attention`: optional readOnly boolean on the general schema, true/false present on every valid main including Admin, omitted on non-main. Existing question/approval/goal snapshots and frames supply authoritative state. |
| Data out | **BS::C-ATTENTION** publishes `AttachSessionFrame.ack_attention`: optional boolean/default false. Only a genuine explicit foreground open of a main requests acknowledgement; background prefetch/reconnect/replay recovery use false/absent. A transport send alone is not successful backend acknowledgement. |
| Goal mapping | Unseen `met` = finished; `rounds_exhausted` or `other` = failed; `stopped_by_user` excluded. Pending structured questions/approvals clear on resolution only. Shared pair-wide observed-outcome mark belongs to backend; a newer outcome beyond that bound stays unseen. |
| Failure | Missing main boolean, failed source or failed/unauthorized attach remains unknown/error and acknowledges nothing; do not coerce to false or synthesize a seen mark in browser storage. |
| Development | Parameterized snapshots/frames for UI; actual unopened-main updates, two people observing one main, explicit-open/append race and restart/reconnect against U1. Backend owner proves computation/seen ordering; frontend proves cue and intent behavior. |

### Agent identity configuration and canonical seeding

| Boundary aspect | Required contract and failure behavior |
|---|---|
| Data in / out | Existing Agent list/create/update responses, protected editability and truthful persistence/activation status. Choices are approved figure + role badge + color, global across workspaces. |
| Publication gap | Baseline Agent schema has `color` and `icon`, **no published separate figure representation**. Backend identity owner must publish the existing Agent contract extension and generated consumer names before persistence code. This spec invents no `figure`, `avatar`, image or role wire key. Canonical built-in role/color/figure values must be published by that owner, not selected from names by frontend. |
| Migration | N D9/D10 + F Q8: automatic one-time stored mapping, canonical definitions/fresh seed/startup enforcement together. R11 obvious old-icon mappings remain, unmatched → General assistant. New/missing figure follows approved Omnipus default through the backend representation. |
| Failure | Draft preview is not saved/activated truth. Save errors retain the real status/retry behavior; no local-only identity shadow, recoloring repair or guessed unlock. |
| Development | Pure identity component fixtures first; actual custom/built-in create/save/read/migrate/reload/repeated restart and lock validation on joint candidate. |

### Plans, tasks, scheduler runs, helpers and shells

| Boundary aspect | Required contract and failure behavior |
|---|---|
| Data in | Existing plan metadata/status/phase/progress; task/run metadata and actual run session; genuine child/session lifecycle; origin-associated shell metadata. Reuse BS/B D5 task/scheduler projection even when no foreground tool started the run. |
| Data out | Existing authorized Tasks/Graph plan selection and session inspection. No new plan executor, report pipeline, job service or SPA call to an agent-facing job-listing tool. |
| Start-result representation | N D13 requires successful/idempotent `execute_plan` and `run_task` results to expose the validated existing plan/task/run/session handle and authoritative workspace. Backend publishes any missing result-navigation schema before consumers; frontend never extracts addresses from prose or adds a URL field. |
| Plan-origin representation | F Q-FE-12 requires the **actual starting chat**, not automatically the plan's internal owner or transport source. The authoritative starting-chat association is not yet verified as a committed generated contract. Backend owner must publish it in an existing contract surface. Tasks-only plans have no origin and remain in existing workspace plan/activity surfaces. |
| Association / counts | Panel shows only the open session's legitimate starter/assignee/child associations. Same MAIN task child counts once as task/run, not again as helper. Shell processes are not invented stored chat sessions; their cross-session modal presentation is Q-M4 and requires metadata coverage. Global unattributed judge verdicts cannot become local session activity by assumption. |
| Failure | Missing run/page/origin/address or forbidden/deleted target: explicit unknown/unavailable/refusal and Retry where recoverable, never synthetic parent, false zero or redirected foreground main. Readiness is held for published snapshot/live coverage of work not opened in this browser. |
| Development | Generated fixtures and existing query invalidation for UI; real start/result/replay/Tasks hand-off, scheduler-only runs, dedupe and unopened-session reconciliation for acceptance. Controllable dependency twins test outages/order, not execution correctness. |

### Shared kit and Tasks-panel overlap

| Boundary aspect | Reuse and ownership |
|---|---|
| Identity / attention / status / pills | One published AgentIcon with figure/role/color/named size/motion; no fetch or session resolution inside the component. Shared recurring attention/inline-status/activity-pill jobs compose existing kit primitives and follow four-part publication. Screens pass layout-only classes. |
| Sessions presentation | Use kit Dialog, flat group/disclosure composition, Badge, shared row/Item, SearchField and filter controls; publish a missing recurring job once, preferring a shadcn port. Keep genuine nested session hierarchy despite flat group-header styling. R35 still uses uppercase group labels; M D1 is not authority for inventing lowercase typography. |
| K integration | Pushed K tip contains FilterMenu and ViewSwitch source/stories/manifests. Consume those components, with conforming selected-item/check behavior centrally repaired by their owner if needed; do not fork them here. Use K's shadcn HoverCard when richer hover content is required, with click/keyboard/touch alternatives. No new view switch is required merely to consume a dependency. |
| Pending publication | HoverCard is not present at the observed K tip. Complete public-export/style/catalog/manifest/executed evidence and final interface agreement are integration dependencies, not presumed from file presence. |
| Failure | If a component/contract is not published, hold its dependent implementation and ask its owner. Do not silently ship a screen-local substitute or treat a nonexistent HoverCard as verified. |
| Development | Static Storybook checks and in-context application interactions; publication metadata alone is not browser/keyboard/accessibility evidence. |

### Dependency ledger — check before consumers and before joint landing

| ID | Dependency / owner | Verified state in this writing task | Landing condition |
|---|---|---|---|
| DEP-U1 | Session-core owner: main identity/membership, Ava destination, default Admin, protected/hide behavior, migration | b76 D1.1 and pushed BS at `0e1fececc5df91c140e21fd27cf03bfd0ee3c8dc` read. Implementation not certified. | Real U1 + generated contracts and continuation/attachment proofs on the candidate. |
| DEP-ATT | Session-core backend: needs_attention/ack_attention, bound/shared seen mark, unopened-main query updates; prompt owner: structured-question rule | Names/semantics published in BS C-ATTENTION; baseline checkout lacks these generated additions. | Generated validation + actual source/ack/open race/reconnect/two-person tests. |
| DEP-ID | Backend identity owner: Agent representation, approved canonical identity inventory and one-time mapping | Design approved; concrete figure wire representation and canonical per-built-in choices are not published here. | Contract + seeds/startup enforcement + locked-field/restart/migration proof. No frontend-picked seed colors. |
| DEP-ACT | Backend task/run/plan owner: real start handles/origin, snapshots/live metadata and coverage for unopened sessions/shells | Existing mechanisms and BS projection direction read; complete required generated shape/coverage is not verified. | Publish/verify missing existing-contract extensions; real A/B session and scheduler/reload/partial-page tests. |
| DEP-KIT | Tasks-panel owner: FilterMenu, ViewSwitch, HoverCard | First two source snapshots read at `7946ac5f00163fa036033e75f329fcab2193bdaa`; HoverCard not yet found there. | One shared implementation each, final publication/API/evidence integrated. |
| DEP-CUT | Session-core command/runtime deletions and companion #1221/R44/R45 | Commissioned boundaries verified; no runtime completion claim. | Coordinated generated consumer cutover, preserved first-send/control behavior, no resurrected obsolete control. |
| DEP-UX | Team-lead/founder: Q-M1–Q-M4 and missing wireframe screens | Pending decisions/visual additions; this draft does not choose them. | Interview, recorded decisions and approved missing visual states before their dependent UI is finalized. |

## Wireframe reference and additions needed

**Approved reference:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/index.html`. Every visual-reference entry below points to that file or its approved Q9 evidence; no alternate prototype is substituted. Source inspection and screenshots verify prototype placement, not delivered behavior or live animation.

| Approved surface | Exact reference / acceptance boundary |
|---|---|
| Sidebar agent rows / row actions / collapsed-workspace attention | W::sidebar/ws-list and main-only waiting demo. Keep original shell/pin/header arrangement; do not copy Demo/source simulation controls into the product. |
| Above-feed main/extra label | W::chat-label; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/04-extra-chat-toggle.png`. Actual kind drives the label. |
| Inline responder and name-only bubbles | W::chat-feed/chat-messages/think-preview; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/01-inline-thinking.png` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/02-name-only-bubbles.png`. Feed slot, not composer status row. |
| Actual non-owner responder | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/evidence/q9/03-addressed-jim-answering.png`, illustrating W's approved design. Rendering genuine producer identity is in scope; enabling full @ messaging waits for its backend/later unit. |
| Figure grammar | W's Robot/Man/Woman/Omnipus examples; Omnipus default, minimal eyes and role badge at every size. The app-wide demo switch is not the per-agent editor. |

### Wireframe additions needed

This task **does not edit the wireframe**. Team-lead commissions missing states in the same approved reference project, obtains founder approval, then resolves visual acceptance before production UI is finalized. Existing modal-review screenshots establish current behavior, not an approved redesigned screen.

| ID | Missing screen/state in W | Required content / dependency |
|---|---|---|
| WF-01 | Improved Sessions modal on desktop and phone | All M A1–A7/D1–D7 details: lifecycle/kind/attention, main ordering with true child nesting, origin plan pill, flat kit groups/counts/rows/search/date/activity filters, keyboard/focus and touch targets. Q-M1/Q-M3 filter scope resolved first. |
| WF-02 | Starting-chat plan pill, successful start links and plan/task rows in existing Activity panel | Approved/running/paused/failure states, independent Open controls, real plan/task/helper drill-down and Tasks-only no-origin case. No new overview or borrowed task-card layout. |
| WF-03 | Figure/role/color controls in the **existing unified** create/edit slide-outs | Per-agent four-figure chooser, five grouped role choices, ten named swatches, locked built-in/error/save states and matching shared live preview. The prototype's new creation interview/app-wide switch does not specify this editor. |
| WF-04 | Navigation restore/loading/stale/unknown/failed attach with drawer/coarse input | Clear unresolved destination/send gating, last-known data + Retry, missing welcome main and main-only attention unknown state. Preserve shell behavior at narrow width, zoom and reduced motion. |
| WF-05 | Overall activity for non-session work and repeated-title helpers | Shell association/presentation after Q-M4; optional repeated-helper summary after Q-M2, without hiding distinct status/attention/targets. No fake persisted shell sessions. |

### Locked identity vocabulary

| Group | Exact role labels from R11 |
|---|---|
| Create (6) | Writer; Designer; Image creator; Video producer; Audio and voice; Social media. |
| Build (8) | Developer; Data engineer; Data analyst; IT and operations; Automation; Security; Quality and QA; Science and lab. |
| Business (10) | Orchestrator; Project manager; Product manager; Sales; Marketing; Finance; Legal and compliance; Customer support; Documents; Researcher. |
| People (4) | People and HR; Tutor; Knowledge and library; Translator. |
| Personal (3) | General assistant; Personal assistant; Office assistant. |

| Palette order — binding for equal hue distances | Hex |
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

Approved migration rule (N D10/F Q8, W adjacent palette asset): valid color is exactly a six-digit hex preceded by `#`. Invalid/missing color → Grey. Calculate hue and saturation from that RGB color; saturation **strictly below 0.25** → Grey. Otherwise choose the nearest non-Grey palette hue using the shorter circular distance. An equal distance keeps the **first palette entry in this table** because improvement is strict, not less-or-equal. Persist once; no per-render mapping. The Test Datasets contain independent boundary/example oracles for founder review; they are not a migration execution receipt. Obvious old role mappings: Code → Developer, Chat → General assistant, MagnifyingGlass → Researcher, PencilSimple → Writer, Shield → Security; other unmatched roles → General assistant. Preserve already-valid curated roles.
