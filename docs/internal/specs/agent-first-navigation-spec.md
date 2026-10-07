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
