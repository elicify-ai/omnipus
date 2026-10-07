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
