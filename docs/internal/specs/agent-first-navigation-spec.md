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
| F | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/adr-frontend-nav-20261007/FOUNDER-ANSWERS-20261007.md` — Q1–Q12, Q9 FINAL, wireframe approval, **Q-FE-11/Q-FE-12 and Session modal A2/D8 + grouping**. All are answered: helpers always nested, All / Running / Needs me, and expandable repeated-helper folding are decisions. |
| R | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/requirements.md` — R1–R47, including subrequirements R6a–R6c and R30a–R30b. The source-disposition table below records narrower first-squad scope and supersession. |
| M | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/session-modal-review/review.md` — A1–A7 and D1–D8. F's newer modal answer settles A2 and D8: implement All / Running / Needs me and repeated-identical-helper folding; helpers never become top-level rows in any search/filter. The review's existing-app evidence is not a new approved modal wireframe. |
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
| M A2/D8 unresolved; current orphan-as-root modal fallback | **F Session modal A2/D8 + grouping:** helpers are always under their real parent, including search/filter hits; exact filter choices All / Running / Needs me; repeated identical helper runs fold under their parent into an expandable `N similar helper runs` row. No completed-only restriction is added. Missing-parent treatment is Q-M5, never permission for a top-level helper. |

## Scope and delivery boundary

| First-squad capability | Decisions and boundary |
|---|---|
| Navigation, restore and freshness | N D1–D5: agent rows, main click, exact remembered chat/welcome-main entry, independent Past sessions and + New chat actions, main-only attention, persistent refresh ownership without the removed picker. Keep workspace ordering/archive/pin/drawer and established focus behavior. |
| Shared identity and existing editor | N D6–D10: four approved figures, 31 role badges, ten-color palette, one shared renderer, global create/edit preview, canonical built-in and one-time custom identity migration. Protected fields retain their locks. Only identity presentation changes in the existing Agents roster and Team. |
| Chat and immediate cleanup | N D3/D8/D11: persistent kind label, name-only messages on every rendering path, inline animated responder indicator, no composer agent picker; remove old switching/new-chat commands and rename the session command without an alias. |
| Plan/task visibility | N D13 plus F Q-FE-12: starting-chat plan pill, successful start-result links, open-session panel rows and real Tasks/Graph/session drill-down. Existing plan/task execution remains backend-owned. |
| Overall activity | N D14 **as narrowed by F Q-FE-11**, M A1–A7/D1–D8 with F's newest modal answer: improve the one Sessions modal, with helpers **always beneath their parent in every search/filter**, the All / Running / Needs me filter and expandable `N similar helper runs` folding. Keep workspace → agent → parent-session grouping and real relations; no top-level helper, second overview, aggregate side panel or synthetic relationships. |
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
| Person: sidebar magnifier or session slash command | The same Sessions modal listing authorized chats/helpers/runs, All / Running / Needs me and expandable repeated helpers; helpers always remain beneath the parent, even when only a helper matches. | Find A's work from B; use filters/search and expand `N similar helper runs` to follow each real helper. |
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
| C-MODAL | Extend `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/components/search/SearchModal.tsx`::SearchModal, AgentSessionList, AgentHeader, SessionRow, handleSwitchWorkspace, bucketByAgent, sortSessions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/adr-frontend-nav/src/store/ui.ts`::useUiStore.openSearchModal. Keep metadata search/Unfiled and true child nesting; add pair/activity filters, shared repeated-helper presentation and M's information. **Current orphan-as-root behavior is superseded for helpers by latest F:** every helper/search/filter match remains under its parent; unavailable-parent treatment is Q-M5, never a fabricated parent or top-level helper. Workspace switching currently starts fresh; apply N D3 instead. |
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
2. **Given** an agent's main, extras and real helper/run children, **When** its group is revealed, **Then** the main is first/pinned and extras retain recency; helpers stay under their real parent **always**, including search/filter hits. Missing parent context is visibly unavailable under the approved Q-M5 treatment, never a top-level helper or invented parent.
3. **Given** desktop, phone, keyboard or assistive input, **When** the modal is explored, **Then** the title is Sessions, subtitle explains chats/helpers/runs, shared group/row/count/identity/search/filter controls are used, focus and independent actions work, and no new handmade tooltip/view appears.
4. **Given** a protected main, inaccessible/deleted destination or failed list source, **When** a delete/open/list action is attempted, **Then** protection/refusal/error is explicit, no hidden session is opened, and an outage is not Unfiled/empty data.

### User Story 9 — Find current work without reopening the modal (Priority: P1)

A person narrows Sessions by existing title/workspace/date and new row-pair filters, sees current activity while it remains open, and distinguishes a complete overview from missing pages or stale data.

**Why this priority**: A stale or partial list claiming no running work would recreate issue #493.
**Independent Test**: Open Sessions in chat B, update work in unopened chat A, filter it and inspect the live result without closing the modal.

1. **Given** authorized running, attention-needed and inactive conversations, **When** the person selects **All / Running / Needs me**, **Then** the matching set appears with other filters composed; a matching helper stays inside its parent even when the parent itself does not match. Running uses authoritative executing state, never queued-as-running; Needs me includes confirmed main attention and any additional non-main source only after Q-M6 is answered.
2. **Given** Sessions remains open with an unchanged title search, **When** background work changes state or membership changes, **Then** status, attention, plan presence and results reconcile without reopening; focus/reconnect also recover missed updates.
3. **Given** a page/source is missing, delayed or failed, **When** Sessions loads or refreshes, **Then** visible partial/unknown coverage and Retry replace any complete/zero claim; cached conversations alone do not prove unseen work is covered.
4. **Given** more results than a single page/viewport and a child-only title match, **When** the person searches or moves the keyboard highlight, **Then** the matching child remains reachable under its parent with truthful counts, bounded rendering and a usable plain fallback.
5. **Given** nine consecutive identical helper runs under one parent and the modal's current search/filter match set, **When** the person expands **9 similar helper runs**, **Then** all nine original helper identities, statuses and Open targets appear under that same parent. Folding does not merge work, cross parents or silently omit matching helpers; the summary count reflects the actual grouped match set.

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
| Overall activity | When Sessions opens, the system shows authorized metadata with All / Running / Needs me and expandable `N similar helper runs` summaries. Helpers remain under their real parent in every search/filter, never top-level; live updates and folding do not change identity/counts/control authority. |
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
| EC-08 | Child-only search/filter hit, unavailable parent, more than 20 visible rows, repeated identical titles | Helpers **always stay under their parent**, never top-level; unavailable-parent treatment follows Q-M5. Repeated identical helpers fold into expandable `N similar helper runs`; each original identity/status/target remains reachable and the filtered count stays truthful. |
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
| Promote a helper to a top-level result when its parent fails a search/filter, or treat a repeated-helper summary as one merged execution | F Session modal A2/D8 + grouping: always nested, expandable folding only; each real identity/status/target/count remains intact. |
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
| Scope/counts | Active metadata counts are uncapped by the eight-item recent-finish limit. Missing pages are not complete. Matching helpers always remain beneath their parent; repeated identical helper siblings fold into `N similar helper runs`, counting original matching identities, not one merged job. | N D14; F Session modal A2/D8 + grouping; C-MODAL/C-TREE. |
| Modal activity filter | Exact options **All / Running / Needs me**. Running uses authoritative executing metadata; confirmed main attention matches Needs me, further non-main matching waits for Q-M6. No top-level helper in any option or text/pair/date filter. | F Session modal A2/D8 + grouping; N D14; BS C-ATTENTION; DEP-ACT. |
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
| Sessions presentation | Use kit Dialog, flat group/disclosure composition, Badge, shared row/Item, SearchField and filter controls; publish a missing recurring job once, preferring a shadcn port. Keep real hierarchy despite flat group headers: F now requires helpers under their parent in **every** search/filter. Fold consecutive identical helper siblings per M D8/F into expandable `N similar helper runs`, preserving every original ID/status/target and filtered count. No orphan-as-root fallback for helpers; Q-M5 settles unavailable real-parent context. R35's uppercase labels remain, not a new lowercase style. |
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
| DEP-UX | Team-lead/founder: Q-M4/Q-M5/Q-M6 and missing wireframe screens | A2/D8 and always-under-parent are **decided**. Remaining shell presentation, unavailable-parent treatment and non-main Needs me source-scope are unresolved, plus missing visual approval. | Interview only those narrower questions and approve missing states. Do not reopen the filter presence/labels, folding or always-nested rule. |

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
| WF-01 | Improved Sessions modal on desktop and phone | All M A1–A7/D1–D7 details: lifecycle/kind/attention, main ordering with true child nesting, origin plan pill, flat kit groups/counts/rows/search/date/activity filters, keyboard/focus and touch targets. All / Running / Needs me and always-nested helpers are decided; show nonmatching parent context for a matching helper. Q-M5/Q-M6 clarify unavailable-parent/non-main attention cases only. |
| WF-02 | Starting-chat plan pill, successful start links and plan/task rows in existing Activity panel | Approved/running/paused/failure states, independent Open controls, real plan/task/helper drill-down and Tasks-only no-origin case. No new overview or borrowed task-card layout. |
| WF-03 | Figure/role/color controls in the **existing unified** create/edit slide-outs | Per-agent four-figure chooser, five grouped role choices, ten named swatches, locked built-in/error/save states and matching shared live preview. The prototype's new creation interview/app-wide switch does not specify this editor. |
| WF-04 | Navigation restore/loading/stale/unknown/failed attach with drawer/coarse input | Clear unresolved destination/send gating, last-known data + Retry, missing welcome main and main-only attention unknown state. Preserve shell behavior at narrow width, zoom and reduced motion. |
| WF-05 | Overall activity for non-session work and **decided** repeated-helper folding | Shell presentation follows Q-M4. Show folded `N similar helper runs` under the actual parent in All / Running / Needs me/search, with expanded original statuses/identities/targets and truthful count. No completed-only restriction, cross-parent merge, top-level helper or fake shell session. |

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

## BDD Scenarios

BDD means behavior-driven development: concrete Given/When/Then examples form the independent behavioral oracle. Each scenario has one action, a parent acceptance back-reference and one category. IDs remain stable through review. Conditional choices below remain held until the founder records the answer; tests then instantiate the chosen branch, never a guessed branch.

### Feature: Stable navigation and safe destination

#### Scenario: BDD-01.1 — Open the main despite a newer extra
**Traces to:** User Story 1, Acceptance Scenario 1
**Category:** Happy Path
- **Given** Mia has distinct validated mains and newer extras in Product launch and Operations
- **When** her Product launch row is selected
- **Then** Product launch's Mia main opens with its owner row selected
- **And** neither the newer extra nor Operations receives the next message

#### Scenario Outline: BDD-01.2 — List only eligible colleagues
**Traces to:** User Story 1, Acceptance Scenario 2
**Category:** Happy Path
- **Given** the expanded workspace contains `<agent>` with authoritative `<eligibility>`
- **When** navigation renders
- **Then** sidebar presence is `<presence>`

| agent | eligibility | presence |
|---|---|---|
| Mia | Eligible main colleague/member | Present |
| Native worker | Worker/member | Absent |
| External worker | Worker/member | Absent |
| Judge or Plan Supervisor | Hidden engine agent | Absent |
| Admin | Validated default-workspace main | Present only there |

#### Scenario: BDD-01.3 — Preserve chat and existing panel navigation
**Traces to:** User Story 1, Acceptance Scenario 3
**Category:** Alternate Path
- **Given** chat is open in a workspace with existing panel controls
- **When** Team is activated
- **Then** the existing Team panel opens beside desktop chat with truthful open state
- **And** no Chat header item, duplicate Library launcher or replaced Agents destination appears

#### Scenario: BDD-01.4 — Show roster or membership failure honestly
**Traces to:** User Story 1, Acceptance Scenario 4
**Category:** Error Path
- **Given** roster or membership refresh fails with either no cache or a previous good cache
- **When** the workspace is expanded
- **Then** no-cache shows a load error and Retry; cached data remains visibly last-known/stale with Retry
- **And** no empty-team success or invented main target is shown

#### Scenario Outline: BDD-02.1 — Restore the exact remembered chat
**Traces to:** User Story 2, Acceptance Scenario 1
**Category:** Happy Path
- **Given** a browser remembers a valid visible `<kind>` for Operations while another chat is more recent
- **When** Operations is entered through `<entry>`
- **Then** the exact remembered conversation and owner return

| kind | entry |
|---|---|
| Main | Workspace name |
| Extra | Workspace name after cold reload |
| Extra | Login entry |
| Main | Existing modal workspace switch |

#### Scenario Outline: BDD-02.2 — Resolve the welcome main without creating an extra
**Traces to:** User Story 2, Acceptance Scenario 2
**Category:** Happy Path
- **Given** `<saved state>` and `<welcome availability>` for the entered workspace
- **When** the workspace is entered
- **Then** `<outcome>` occurs with zero extra-chat creation

| saved state | welcome availability | outcome |
|---|---|---|
| No real pointer | Validated Ava main | Ava main opens |
| Confirmed deleted/hidden/inaccessible pointer | Validated Ava main | Ava main opens; invalid destination is not reused |
| No real pointer | No eligible/accessible welcome destination | Unavailable state with Team/manage, Retry and no send |

#### Scenario: BDD-02.3 — Refuse split selection after failed load or attach
**Traces to:** User Story 2, Acceptance Scenario 3
**Category:** Error Path
- **Given** A is committed and a B entry's validation/load/attach fails
- **When** the pending B attempt settles
- **Then** A's committed workspace/session/owner remain mutually consistent and B intent remains retryable
- **And** unresolved-target send is unavailable; no fallback or false switch closes the interaction

#### Scenario: BDD-02.4 — Reject a late losing selection
**Traces to:** User Story 2, Acceptance Scenario 4
**Category:** Edge Case
- **Given** B has won while an older A request is pending
- **When** A's response arrives
- **Then** B stays active and another workspace's remembered destination is not overwritten

#### Scenario: BDD-03.1 — Start an extra with delivery protection
**Traces to:** User Story 3, Acceptance Scenario 1
**Category:** Happy Path
- **Given** a colleague/workspace main is selected and a pending unconfirmed first message may exist
- **When** + New chat is activated
- **Then** a deliberate extra starts only through the existing abandonment decision where required
- **And** main remains intact; retained/retried first delivery uses its original request identity, not changed selections

#### Scenario: BDD-03.2 — Open pair-filtered past sessions
**Traces to:** User Story 3, Acceptance Scenario 2
**Category:** Happy Path
- **Given** Product launch/Mia has older sessions among other pairs
- **When** its Past sessions action is activated
- **Then** the existing Sessions modal opens with visible Product launch and Mia filters
- **And** broadening either filter can reveal its authorized additional results

#### Scenario: BDD-03.3 — Keep extra ownership and independent row actions
**Traces to:** User Story 3, Acceptance Scenario 3
**Category:** Alternate Path
- **Given** Mia's extra is open and its owner row is selected
- **When** Mia's row is activated by keyboard
- **Then** Mia's main opens, not the extra
- **And** Past sessions then + New chat remain independent named targets, visible on selected/hover/focus/touch rows

#### Scenario: BDD-03.4 — Retain selection on refused history attachment
**Traces to:** User Story 3, Acceptance Scenario 4
**Category:** Error Path
- **Given** a history destination is inaccessible or the connection rejects attach
- **When** that result is selected
- **Then** visible refusal/recovery remains without claiming a completed switch or losing the committed chat

### Feature: Main-only attention

#### Scenario Outline: BDD-04.1 — Signal only the four main sources
**Traces to:** User Story 4, Acceptance Scenario 1
**Category:** Happy Path
- **Given** an unopened main with no prior attention receives `<source>`
- **When** authoritative attention is reconciled
- **Then** the main's sidebar signal is `<signal>`

| source | signal |
|---|---|
| Pending structured question card | On |
| Pending tool approval | On |
| Unseen finished goal | On |
| Unseen failed goal | On |
| User-stopped goal | Off |
| Ordinary unread message/free-text question | Off; user answers require the structured question workflow |
| Extra/helper-only pending source | Off for this main |
| Generic task notice or global plan verdict | Off unless a real allowed goal source belongs to this main |

#### Scenario: BDD-04.2 — Acknowledge observed goals, not pending decisions
**Traces to:** User Story 4, Acceptance Scenario 2
**Category:** Alternate Path
- **Given** two people view the same main with an unseen goal plus unresolved question/approval
- **When** one person successfully opens the main explicitly
- **Then** observed goal attention clears for both people
- **And** unresolved question/approval and a newer outcome beyond the observed bound remain attention-worthy

#### Scenario: BDD-04.3 — Keep accessible attention without motion
**Traces to:** User Story 4, Acceptance Scenario 3
**Category:** Edge Case
- **Given** two distinct main agents need attention and reduced motion is enabled
- **When** their workspace is rendered collapsed
- **Then** the static warning-yellow 8 px dot and meaningful attention text remain with distinct-agent count two
- **And** expanded icons retain cues without loops or separate agent-row dots

#### Scenario: BDD-04.4 — Treat missing main attention as unknown
**Traces to:** User Story 4, Acceptance Scenario 4
**Category:** Error Path
- **Given** a valid main lacks its required authoritative attention value or the source fetch failed
- **When** the navigation snapshot is rendered
- **Then** attention coverage is unknown/unavailable with Retry, never false/zero because the value was absent

### Feature: Shared identity and existing editor

#### Scenario Outline: BDD-05.1 — Share the four figure identities
**Traces to:** User Story 5, Acceptance Scenario 1
**Category:** Happy Path
- **Given** an agent with `<figure>`, Developer badge and Azure color
- **When** a named identity surface renders it
- **Then** the same transparent figure/role/color precedes its normal-color name, with badge present even at small size

| figure |
|---|
| Robot |
| Man |
| Woman |
| Omnipus — default for a new agent |

#### Scenario: BDD-05.2 — Offer only approved readable choices
**Traces to:** User Story 5, Acceptance Scenario 2
**Category:** Happy Path
- **Given** the agent identity chooser and sidebar/chat reference surfaces
- **When** its choices are opened
- **Then** exactly 31 roles in the five named groups and ten named colors are offered
- **And** every identity graphic meets 3:1 contrast without semantic/brand-color choices

#### Scenario: BDD-05.3 — Persist identity migration through reseeding
**Traces to:** User Story 5, Acceptance Scenario 3
**Category:** Alternate Path
- **Given** fresh, custom legacy and built-in legacy identities and the approved mapping
- **When** the upgraded gateway is restarted repeatedly
- **Then** stored/rendered identities remain in the approved palette/role/figure grammar and fixed-field locks remain unchanged

#### Scenario: BDD-05.4 — Do not invent saved identity after load failure
**Traces to:** User Story 5, Acceptance Scenario 4
**Category:** Error Path
- **Given** an identity lookup fails with last-known or unavailable data
- **When** the identity surface renders
- **Then** its cached/unknown status is honest; a failed fetch does not rename the agent removed or claim a newly saved identity

#### Scenario: BDD-06.1 — Preview through the existing global editor
**Traces to:** User Story 6, Acceptance Scenario 1
**Category:** Happy Path
- **Given** a custom agent in the existing unified slide-out
- **When** its identity choice changes
- **Then** the live preview matches the shared figure/role/color and current global create/autosave semantics remain
- **And** there is no new editor, upload/GIF control or app-wide figure setting

#### Scenario: BDD-06.2 — Preserve protected-field editability
**Traces to:** User Story 6, Acceptance Scenario 2
**Category:** Alternate Path
- **Given** a built-in with authoritative editable-field rules
- **When** its editor opens
- **Then** fixed identity stays locked while permitted existing configuration retains its exact editability

#### Scenario: BDD-06.3 — Keep save/activation failure visible
**Traces to:** User Story 6, Acceptance Scenario 3
**Category:** Error Path
- **Given** a draft preview whose create/save/activation fails
- **When** the operation settles
- **Then** error/save status remains visible and the draft is not reported saved everywhere

### Feature: Truthful feed and immediate command cutover

#### Scenario Outline: BDD-07.1 — Show kind and actual names without avatars
**Traces to:** User Story 7, Acceptance Scenario 1
**Category:** Happy Path
- **Given** an inspected `<kind>` with messages from owner Mia and guest Jim
- **When** `<render path>` displays it
- **Then** both actual author names appear without bubble avatars, and the above-feed kind is truthful

| kind | render path |
|---|---|
| Main chat | Live |
| Extra chat — Launch notes | Historical virtualized |
| Task run | Historical plain fallback |
| Helper | Replay |

#### Scenario Outline: BDD-07.2 — Replace thinking dots inline for the actual responder
**Traces to:** User Story 7, Acceptance Scenario 2
**Category:** Happy Path
- **Given** `<responder>` is the real producer in `<state>` with `<motion setting>`
- **When** the next reply slot renders
- **Then** its shared icon/name/phrase appears inline with the locked motion rule, replacing old dots
- **And** there is no composer status line, duplicate chat logo or owner change

| responder | state | motion setting |
|---|---|---|
| Mia | Working | Normal |
| Jim, with Mia still owner | Thinking | Normal |
| Mia | Waiting | Normal |
| Jim, with Mia still owner | Thinking | Reduced — zero loops, meaningful text retained |

#### Scenario: BDD-07.3 — Remove switching and old entry paths
**Traces to:** User Story 7, Acceptance Scenario 3
**Category:** Alternate Path
- **Given** the navigation/command cutover is integrated
- **When** the person opens the unified command/composer choices
- **Then** only the session command opens Sessions; removed picker/new-chat/resume alias and mention switching are unavailable
- **And** model/Auto/attachments/send/Stop stay available; Clear never creates a new chat or deletes saved history

#### Scenario: BDD-07.4 — Preserve errors and selected-session Stop safety
**Traces to:** User Story 7, Acceptance Scenario 4
**Category:** Error Path
- **Given** A has live helper work or a correlated reply error while B is open
- **When** B's existing recovery/Stop action is activated
- **Then** its delivery/refusal and scope are truthful, not a control of A
- **And** merely leaving A did not cancel it; failures do not disappear into decorative status phrases

### Feature: One Sessions modal as overall activity

#### Scenario Outline: BDD-08.1 — Show truthful lifecycle and kind metadata
**Traces to:** User Story 8, Acceptance Scenario 1
**Category:** Happy Path
- **Given** a permitted row in `<status>` and `<kind>`
- **When** Sessions displays the row
- **Then** its status chip and quiet kind remain visible with title then muted status/kind/active/tokens metadata
- **And** main attention uses its confirmed yellow dot; no HB abbreviation appears

| status | kind |
|---|---|
| Working | Main chat |
| Waiting for answer | Extra chat |
| Done | Helper |
| Failed | Task run |
| Stopped with cause | Scheduled run |
| Interrupted | Extra chat |
| Unavailable — no authoritative lifecycle | Permitted inspectable chat |

#### Scenario: BDD-08.2 — Order mains without flattening real children
**Traces to:** User Story 8, Acceptance Scenario 2
**Category:** Happy Path
- **Given** main M, extras E1/E2, helper C under E1 and helper O with unavailable real-parent context
- **When** the agent group is revealed
- **Then** M is first/pinned, extras keep recency and C stays beneath E1 even when only C matches a filter/search
- **And** O follows the founder-approved Q-M5 unavailable-parent treatment, never a top-level helper or invented navigable parent

#### Scenario: BDD-08.3 — Use the shared accessible modal presentation
**Traces to:** User Story 8, Acceptance Scenario 3
**Category:** Alternate Path
- **Given** Sessions at desktop or phone width with keyboard/coarse input
- **When** a row action is focused
- **Then** the title/subtitle, shared group/identity/count/row/search/filter presentation and independent actions remain legible and reachable
- **And** focus is trapped/restored correctly; rename Escape cancels rename before closing the modal; no new handmade tooltip appears

#### Scenario: BDD-08.4 — Preserve protection, refusal and load errors
**Traces to:** User Story 8, Acceptance Scenario 4
**Category:** Error Path
- **Given** a protected main or failed/inaccessible list/destination
- **When** its delete/open/list action is attempted
- **Then** protection or failure is visible, hidden sessions do not open, and a workspace outage never presents as Unfiled/empty success

#### Scenario Outline: BDD-09.1 — Apply the decided All / Running / Needs me filter
**Traces to:** User Story 9, Acceptance Scenario 1
**Category:** Happy Path
- **Given** matching title/workspace/agent/date filters and authoritative running, queued, inactive and main-attention fixtures, with no non-main person-action case requiring Q-M6
- **When** `<filter>` is selected
- **Then** `<matching set>` is shown, with every matching helper beneath its parent even when that parent does not match
- **And** all three decided choices are available, pair filters removable, and queued is not guessed to be executing

| filter | matching set |
|---|---|
| All | All authorized matches, preserving real parent context |
| Running | Actual executing matches; queued/inactive excluded as matches; nonmatching parent retained as context for its running helper |
| Needs me | Confirmed main attention in this fixture; no boolean borrowed by helpers; additional non-main matching waits for Q-M6 |

#### Scenario: BDD-09.2 — Refresh unopened-chat activity while modal stays open
**Traces to:** User Story 9, Acceptance Scenario 2
**Category:** Happy Path
- **Given** Sessions is open in B with a search matching work in unopened A
- **When** A's authorized status/attention/plan or membership update arrives
- **Then** the matching metadata/results reconcile without reopening or clearing the search
- **And** no view action acknowledges A's goals or stops its work

#### Scenario: BDD-09.3 — Mark partial overview coverage
**Traces to:** User Story 9, Acceptance Scenario 3
**Category:** Error Path
- **Given** a page or source is missing/failed while cached rows exist
- **When** the overview refresh settles
- **Then** partial/unknown coverage and Retry are visible, with no complete/all-idle/zero claim from that cache

#### Scenario: BDD-09.4 — Reach a child-only match in a large list
**Traces to:** User Story 9, Acceptance Scenario 4
**Category:** Edge Case
- **Given** more than 20 visible rows, multiple pages and a title match only in a child
- **When** that title is searched
- **Then** its real ancestors reveal the child and keyboard selection reaches it with truthful counts
- **And** viewport-bounded rendering or the supported plain fallback keeps every result reachable

#### Scenario: BDD-09.5 — Expand repeated helpers without flattening or merging jobs
**Traces to:** User Story 9, Acceptance Scenario 5
**Category:** Happy Path
- **Given** nine consecutive identical helper titles/kinds with distinct identities/statuses/targets under P, and the same title under another parent Q
- **When** the folded **9 similar helper runs** row under P is expanded
- **Then** all nine original helpers and their status/Open actions are revealed beneath P, never top-level
- **And** Q's helpers stay in their own parent group; filtered original counts remain truthful and the summary is not a synthetic chat/session or merged execution

### Feature: Plan links and session-scoped activity

#### Scenario: BDD-10.1 — Associate a plan with its real starting chat
**Traces to:** User Story 10, Acceptance Scenario 1
**Category:** Happy Path
- **Given** P was started in A and another plan was created only in Tasks
- **When** A's activity is shown
- **Then** P's pill/parent row and modal starting-row association refer to A
- **And** neither B nor a fabricated chat origin is assigned to the Tasks-only plan

#### Scenario Outline: BDD-10.2 — Keep plan state truthful
**Traces to:** User Story 10, Acceptance Scenario 2
**Category:** Alternate Path
- **Given** a plan's reported `<state>`
- **When** the pill/row updates
- **Then** `<meaning>` appears without inventing execution progress

| state | meaning |
|---|---|
| Approved | Waiting to start, not running |
| Running | Real reported phase/pause/progress |
| Running but paused | Pause reason, not uninterrupted execution |
| Done/failed/stopped | Real terminal result; no continuing-running claim |

#### Scenario Outline: BDD-10.3 — Follow validated successful start results
**Traces to:** User Story 10, Acceptance Scenario 3
**Category:** Happy Path
- **Given** an authorized successful/idempotent `<start>` result retained `<display>`
- **When** its Open control is activated
- **Then** `<target>` opens through the existing workflow without duplicate execution

| start | display | target |
|---|---|---|
| Plan start | Live | Actual plan in its workspace Tasks/Graph |
| Plan start | Replayed after reload | Same actual plan |
| Task start | Live | Returned actual task-run session, not assignee main |
| Task start | Replayed after reload | Same real run session |

#### Scenario: BDD-10.4 — Refuse invented or unavailable start targets
**Traces to:** User Story 10, Acceptance Scenario 4
**Category:** Error Path
- **Given** missing origin/address metadata or a forbidden/deleted plan/run target
- **When** the result/pill is used
- **Then** unavailable/refusal/recovery is visible; no prose-derived link, foreground substitute or fake parent appears

#### Scenario: BDD-11.1 — Keep B's panel local and A reachable in Sessions
**Traces to:** User Story 11, Acceptance Scenario 1
**Category:** Happy Path
- **Given** A and B have distinct ongoing work with A previously open
- **When** B's Activity panel is opened
- **Then** only B-associated work appears there, while A remains findable in the existing Sessions modal
- **And** no aggregate side panel, elsewhere badge or new dashboard replaces that modal

#### Scenario: BDD-11.2 — Deduplicate work and preserve control ownership
**Traces to:** User Story 11, Acceptance Scenario 2
**Category:** Happy Path
- **Given** one MAIN task run also appears as a real child, a scheduler-only independent run, queued/waiting helpers and a shell
- **When** their open-session activity reconciles
- **Then** each distinct actual item counts once under the correct kind/state/source, with queued/waiting excluded from executing counts
- **And** monitoring an independent run does not add tree-Stop authority or a fake parent

#### Scenario: BDD-11.3 — Do not localize unattributed or unavailable work
**Traces to:** User Story 11, Acceptance Scenario 3
**Category:** Error Path
- **Given** a task/run/plan source fails or a global verdict lacks a session association
- **When** the panel renders
- **Then** coverage is explicitly partial/unknown and unrelated work is not attributed to the open chat; Retry is offered for recoverable sources

### Feature: Existing-installation cutover

#### Scenario: BDD-12.1 — Continue real saved chat and heartbeat histories
**Traces to:** User Story 12, Acceptance Scenario 1
**Category:** Happy Path
- **Given** a supported saved install contains ordinary, extra, Unfiled, child and heartbeat conversations
- **When** the backend-owned upgrade is applied
- **Then** legitimate conversations remain reachable and continuable, with heartbeat history under its validated main
- **And** repeated upgrade preserves identity/content without duplicate histories

#### Scenario: BDD-12.2 — Reach Admin before removing the old entry
**Traces to:** User Story 12, Acceptance Scenario 2
**Category:** Happy Path
- **Given** the joint candidate publishes Admin's default-workspace main without team membership
- **When** Admin's row is selected
- **Then** it opens and supports a legitimate message before the obsolete Assets-only entry is removed
- **And** no other-workspace Admin main is invented

#### Scenario: BDD-12.3 — Expose saved-history import failure
**Traces to:** User Story 12, Acceptance Scenario 3
**Category:** Error Path
- **Given** import or destination resolution failed for known saved history
- **When** the person opens that chat
- **Then** retryable failure remains visible rather than empty-success history, legacy owner/type guess or delivered-feature claim

### Feature: Explicit boundary cases

#### Scenario: BDD-E01 — Differentiate duplicate names across workspaces
**Traces to:** User Story 1, Acceptance Scenario 1; EC-01
**Category:** Edge Case
- **Given** two different agent identities share the name Mia and each pair has a main plus extra
- **When** the Operations row for one identity is selected
- **Then** its validated pair alone opens; display name/recency never substitutes identity

#### Scenario: BDD-E02 — Preserve a pending first send without saving a fake main
**Traces to:** User Story 3, Acceptance Scenario 1; EC-02
**Category:** Edge Case
- **Given** an extra's first send is unconfirmed with no real saved chat ID
- **When** New chat abandonment is declined
- **Then** original message/delivery recovery remain and no pending placeholder is recorded as a main or real remembered destination

#### Scenario: BDD-E03 — Win a rapid selection race without split state
**Traces to:** User Story 2, Acceptance Scenario 4; EC-03
**Category:** Edge Case
- **Given** A's older resolution is pending while B has committed
- **When** A fails or completes late
- **Then** B's displayed/sending workspace/session/owner remain consistent and B's pointer is not overwritten

#### Scenario: BDD-E04 — Retain a goal beyond the observed open bound
**Traces to:** User Story 4, Acceptance Scenario 2; EC-04
**Category:** Edge Case
- **Given** an explicit main open captured one goal while another saved outcome raced beyond its bound
- **When** acknowledgement completes
- **Then** only the captured goal is seen for all viewers; the newer goal and unresolved decisions remain attention-worthy

#### Scenario Outline: BDD-E05 — Apply color boundaries without render-time drift
**Traces to:** User Story 5, Acceptance Scenario 3; EC-05
**Category:** Edge Case
- **Given** stored `<color condition>` in the supported migration source
- **When** the one-time identity mapping is applied
- **Then** `<mapping>` is persisted, stable on repeat and not recomputed by each rendered screen

| color condition | mapping |
|---|---|
| Missing/invalid six-digit hex | Grey |
| Saturation below 0.25 | Grey |
| Saturation equal to or above 0.25 | Closest non-Grey circular hue |
| Exact equal hue distances | First tied palette entry in published order |
| Hue across 0/360 boundary | Shorter circular distance, not linear difference |

#### Scenario: BDD-E06 — Keep role badges and access at size/motion extremes
**Traces to:** User Story 5, Acceptance Scenario 1; EC-06
**Category:** Edge Case
- **Given** every figure at 18/26/40/48 px, 20 px root/zoom/forced colors and reduced motion
- **When** identity/status surfaces render
- **Then** figure and badge remain present, text/focus/access remain meaningful and loops stop without substituting plain role glyphs

#### Scenario: BDD-E07 — Preserve guest identity through plain replay
**Traces to:** User Story 7, Acceptance Scenario 1; EC-07
**Category:** Edge Case
- **Given** a saved helper/task feed with actual guest Jim and no virtualizer support
- **When** replay renders the plain path
- **Then** Jim's name-only reply and truthful inspected kind remain; owner does not change

#### Scenario: BDD-E08 — Keep filtered repeated helpers inside parent context
**Traces to:** User Story 9, Acceptance Scenario 4; EC-08
**Category:** Edge Case
- **Given** 21 visible rows, nine consecutive identical helper runs under P and another same-title group under Q, including a helper whose parent does not match the current filter
- **When** a helper-only title/status filter is applied
- **Then** matching helpers and any repeated-helper summary remain inside their own parent context, never top-level or merged across parents
- **And** the summary counts original matching helpers, expands to their individual identities/status/Open targets and follows the same rule in virtualized/plain paths; unavailable real parents follow Q-M5

#### Scenario: BDD-E09 — Recover unopened-workspace metadata without false coverage
**Traces to:** User Story 9, Acceptance Scenario 3; EC-09
**Category:** Edge Case
- **Given** an unopened workspace has real work and a later page is unavailable, including rows with no lifecycle or token metadata
- **When** Sessions reconnects
- **Then** authorized metadata reconciles with explicit unknown/partial state; missing values do not become done/zero and background recovery acknowledges no goal

#### Scenario: BDD-E10 — Count scheduler and task-child identity once
**Traces to:** User Story 11, Acceptance Scenario 2; EC-10
**Category:** Edge Case
- **Given** starter/assignee/child projections refer to the same run and another run was scheduler-only
- **When** activity is displayed
- **Then** each real run is represented once per legitimate viewing session without duplicate helper count or inferred controller authority

#### Scenario: BDD-E11 — Keep a Tasks-only/idempotent plan origin honest
**Traces to:** User Story 10, Acceptance Scenario 1; EC-11
**Category:** Edge Case
- **Given** a Tasks-only plan has no chat origin and another start is an idempotent retry
- **When** plan metadata reconciles
- **Then** no origin is invented for the first and no duplicate pill/work item is created for the second; deleted targets remain unavailable

#### Scenario: BDD-E12 — Repeat cutover without empty-history success
**Traces to:** User Story 12, Acceptance Scenario 1; EC-12
**Category:** Edge Case
- **Given** known supported saved history with an interrupted or partially failed previous upgrade
- **When** backend cutover resumes
- **Then** legitimate saved content/binding remains continuable or its failure is explicit; repeat work never duplicates or erases known history

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

## Assumptions

| Item | Status / boundary |
|---|---|
| Approved design/interview | F's approved design and latest modal steering are explicit confirmation of the settled scope. This is not assumed implementation, independent review approval or final ambiguity acceptance. |
| Backend U1/identity/activity | Required contracts and actual runtime integration remain owner dependencies. No unfinished dependency is disguised by client fallbacks or invented wire types. |
| Later items | Full @/clear integration, uploads/GIFs, broader page redesign and app-wide kit swaps remain later/companion units as recorded, not silently delivered here. |
| Current source baseline | This worktree's production code is the cited baseline; later branch publications are pinned sources, not checkout changes. Graph context is unavailable here; future impact is source-inferred. |
| Modal decisions | Always-under-parent helpers, All / Running / Needs me and expandable N similar helper runs are **decided**, not assumptions or pending A2/D8 questions. Narrow missing-parent/non-main attention/shell cases remain explicitly unanswered. |
| No unspecified defaults | No extra product bounds, endpoints, persisted property names, avatar storage, permission grants or unreviewed alternative glyphs are assumed. |

Incidental notes for team-lead, not side fixes: (1) current selection source writes active workspace before failed attach; N D3 already commissions the consistent-selection replacement, with runtime proof still required. (2) K's observed FilterMenu source marks the selected item with the text `active`, whereas R39 asks for a check mark; its owner resolves that centrally, not via a duplicate here. (3) push receipts report 36 default-branch vulnerabilities (16 high/16 moderate/4 low); this author has not inspected or classified them, and this one-spec task does not change dependencies or claim vulnerability clearance.

## Clarifications

### 2026-10-07

| Recorded answer / source fact | Specification effect |
|---|---|
| F Q1/Q2 | Exact remembered visible chat, otherwise server-validated Ava welcome main; persistent above-feed Main/Extra label. |
| F Q4/Q4b; published BS C-ATTENTION | Main-only four-source needs_attention; structured user-question rule; shared observed-goal acknowledgement through published optional ack_attention, not prefetch/reconnect or unresolved decisions. |
| F Q5–Q8 | Four figures/Omnipus default/role at every size; no first-squad uploads/GIFs; exact one-time palette threshold/ties/examples recorded. |
| F Q9 FINAL and W approval | Name-only bubbles, actual responding-agent animation inline in the feed, no composer status line/old bouncing dots. |
| F Q10 | Old paths removed now; /resume → /sessions without alias, @ switching/suggestions unavailable until proper messaging, Clear never new chat. |
| F Q11/Q12 and later BS amendments | Supported upgrade preserves real saved-chat continuation, heartbeat→main, Admin default-workspace main/no fake membership. Backend scope is consumed, never re-invented. |
| F Q-FE-11/Q-FE-12 | Activity panel belongs only to open session; overall view is existing Sessions modal. Plan pill/parent row in actual starting chat, other chats via modal, no-origin Tasks plan in existing workspace surfaces. |
| F **Session modal A2/D8 + grouping**, re-read after team-lead steering | Helpers **always** nested under parent, never top-level even on search/filter hits; All / Running / Needs me and expandable **N similar helper runs** are decided. Updated US-8/US-9, BDD-08.2/09.1/09.5/E08, T-08/T-19/T-26, FR-030/033/034 and their traces. Former A2/D8/name-choice questions retired; only Q-M4/Q-M5/Q-M6 remain narrow unresolved cases. |
| P feature workflow / plan-spec ambiguity gate | This is a complete **draft for interview/review**, not approved implementation. Team-lead handles outstanding interview, two spec grill/fix rounds and later joint gates; no extra ADR grill or independent author approval. |
