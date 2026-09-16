# Projects UI — 2026-05

## Status

Draft, decisions logged 2026-05-03. Specifies the three SPA surfaces affected by introducing **projects** as a first-class user concept. Companion docs:

- `docs/internal/design/sandbox-redesign-2026-05.md` — establishes the two-room (private agent + shared project) workspace topology.
- `docs/internal/design/memory-redesign-2026-05.md` — defines what lives in each room and how memory scopes to project context.

## Context

A **project** is a first-class user-facing entity, not an inferred concept. It is:

- A directory on disk with a `.omnipus/` subdirectory (memories, learnings, `last-project-session.md`).
- A roster of agent members (operator-managed).
- Optionally tracked under git, so memory travels with the repo.

Sessions are bound to one project (or none = "personal") at creation time, immutably. The binding determines:

- Default scope of `remember()` and `retrospective()` (project room when bound, private otherwise).
- Whether `last-project-session.md` is auto-injected into the prompt at session start.
- How the session appears in history (under its project, or under "Personal").

Three SPA surfaces need to evolve to support this model.

## Surface 1 — New session modal

The session-creation flow gains a project picker before the agent picker. Agent list filters to that project's members when a project is chosen.

```
┌─ New session ─────────────────────────┐
│                                       │
│  Project:  ▾  none (personal)         │
│              website-foo              │
│              api-rebuild              │
│              + New project…           │
│                                       │
│  Agent:    ▾  Jim                     │
│              (filtered to project     │
│               members if a project    │
│               is selected; all agents │
│               if "none")              │
│                                       │
│          [ Cancel ]   [ Start ]       │
└───────────────────────────────────────┘
```

Behavior:

- Default project: last-used by this operator, or "none" on first run.
- When project changes, agent list refreshes; if the previously-selected agent is not a member, fallback to the project's first member.
- "+ New project…" opens a small project-creation dialog: name, root path on disk, initial member roster. Creates `<root>/.omnipus/` and seeds default MOCs (see memory-redesign).
- The chosen project_id is stored on the session metadata at creation. Immutable thereafter.
- A "personal" session has `project_id = null`.

## Surface 2 — Command Center

Command Center pivots from "list of agents" to "list of rooms." Two top-level groupings: **Personal** (per-agent rooms) and one entry per **project room** the operator manages.

```
COMMAND CENTER
──────────────
▼ Personal                   ← per-agent rooms (always visible)
    Jim                          • last session: "iframe preview", 2h ago
    Mia                          • last session: "design tokens", 1d ago
    Ava                          • last session: "deployment runbook", 4h ago

▼ website-foo                ← project room
    Members: Jim, Mia
    Last project session: "iframe preview wiring" — Jim, 2h ago
    Memories: 47
    Open joint retros: 3

▼ api-rebuild
    Members: Ava, Ray
    Last project session: "schema migration plan" — Ava, 1d ago
    Memories: 12
    Open joint retros: 0

+ New project
```

Each row surfaces concrete data from the room's `.omnipus/` directory:

- Personal/agent rows: agent's `last-session.md` headline + ago-time.
- Project rows: member roster, `last-project-session.md` headline + ago-time, count of `memories/*.md`, count of `learnings/*-joint.md` not yet operator-acknowledged.

Clicking a row opens that room's detail view: memory list, learnings list, recent sessions. The project detail view also exposes membership management (add/remove agents) and a "share via git" toggle that controls the project's `.gitignore` rules.

## Surface 3 — Session history

History list supports three groupings via a top-level selector. Default is "Project, then agent" because that matches how operators think about retrospectively finding work.

```
Sessions
Group by: ▾ Project then agent | Agent only | Project only

▼ website-foo
   ▼ Jim
       2026-05-03  iframe preview wiring          [project-bound]
       2026-05-02  cors handling on /api          [project-bound]
   ▼ Mia
       2026-05-02  design tokens setup            [project-bound]
▼ api-rebuild
   ▼ Ava
       2026-05-01  schema migration plan          [project-bound]
▼ Personal
   ▼ Jim
       2026-04-29  general chat                   [private]
       2026-04-28  brainstorm on naming           [private]
```

Three modes:

- **Project then agent** (default) — outer group is project (or "Personal"), inner group is agent. Matches "what was I doing on X?"
- **Agent only** — flat list of an agent's sessions across all projects + personal. Matches "what was Jim up to last week?"
- **Project only** — flat list of a project's sessions, all agents flattened by date. Matches "what's been done on X?"

Each row shows session id, date, the operator-visible session topic (derived from `last-session.md` headline or first user message if recap hasn't run yet), and a [project-bound] / [private] tag.

Clicking a row opens the session transcript. Search bar at top filters across visible groups.

## Cross-surface invariants

- The "Personal" bucket is rendered explicitly everywhere, never as a missing-project. Operators understand that some sessions are deliberately not project-scoped.
- Project membership is operator-managed, not agent-managed. An agent doesn't decide which projects to join.
- Project deletion is operator-only and warns about the `<project>/.omnipus/` directory's content (memories, learnings) before proceeding.
- Switching projects requires a new session — no in-place rebinding (locked in memory-redesign Q2).

## Backend touchpoints

The SPA needs these REST endpoints (specified informally; full schema goes in the implementation sprint):

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/projects` | list all projects with member roster, last-session headline, counts |
| `POST /api/v1/projects` | create project (name, root path, initial members) |
| `PATCH /api/v1/projects/{id}` | update name / membership / git-share toggle |
| `DELETE /api/v1/projects/{id}` | cascade-delete project (memories, learnings, tasks, bound sessions, last-project-session.md, .omnipus/ dir). Requires name-confirmation in the body |
| `GET /api/v1/projects/{id}/deletion-preview` | returns counts (memories, learnings, tasks, bound sessions, edges) for the danger-zone dialog before delete |
| `GET /api/v1/projects/{id}/summary` | project detail: memories list, learnings list, recent sessions |
| `POST /api/v1/projects/{id}/members` | add agent to project (re-adds previously removed agents transparently) |
| `DELETE /api/v1/projects/{id}/members/{agent_id}` | remove agent from project. Authored content stays. Tasks assigned to the agent become unassigned. Re-addable later |
| `POST /api/v1/sessions` | accepts optional `project_id` at creation; immutable once set |
| `GET /api/v1/sessions?group_by={project_agent,agent,project}` | history with grouping |
| `GET /api/v1/memory/search?scope=...&tier=memories,learnings,sessions&q=...` | cross-project search; defaults to all-projects+personal, memories tier only |

Sessions retain their existing per-agent storage (`agents/<id>/.omnipus/sessions/...`); the `project_id` lives on the session metadata, not in the directory layout. History grouping is done by indexing `project_id` server-side.

## Locked decisions (resolving prior open questions)

| Was | Decision | Source |
|---|---|---|
| Session project binding | Operator picks project + agent at session creation. Immutable for the session lifetime — to switch projects, start a new session | Q2 |
| Project archival | **Not implemented.** Project lifecycle is binary — active or deleted | Q11 |
| Session-on-deleted-project behavior | **Cascade-delete.** Sessions bound to the project are deleted with it. Operator types project name to confirm; preview shows exact counts before delete | Q10 |
| Cross-project search | Filter dropdown above search bar; default scope "All projects + Personal", default tier "Memories only". Operator can narrow scope (specific project) or broaden tiers (include learnings, sessions). Same ranking as agent-side recall but no project boost (operator isn't in a project context) | Q14 |
| Agent removal from project | One-click confirmation. Removed agent loses read AND write access. Authored content stays (immutable). Tasks assigned to the agent become unassigned. Re-addable at any time, no special flow | Q15 |

### Project deletion dialog (Q10)

```
Delete project "website-foo"

⚠ This action is permanent and cannot be undone.

The following will be deleted:
  • 47 memories in the project room
  • 12 learnings in the project room
  • 7 tasks (4 active, 3 done)
  • last-project-session.md
  • 23 session transcripts bound to this project, across Jim, Mia, Ava
  • 156 graph edges referencing project content
  • The .omnipus/ directory in <project_root>

If this project is committed to git, the deletion is a regular `git rm`
and can be recovered via git history (operator action).

Type the project name to confirm:
  [                                                  ]

                                       [ Cancel ]  [ Delete ]
```

Counts are dynamic, fetched from `GET /api/v1/projects/{id}/deletion-preview` before render.

### Agent removal dialog (Q15)

```
Remove "Jim" from project "website-foo"?

Jim will lose read and write access to this project.
He can be re-added at any time — no data is deleted.

What stays:
  • 12 memories authored by Jim (preserved with author: jim)
  • 4 learnings authored by Jim
  • 23 of Jim's session transcripts (in his private room, unaffected)

3 tasks currently assigned to Jim will become unassigned.

                                  [ Cancel ]   [ Remove ]
```

No name-typing confirmation — this isn't destructive.

### Cross-project search surface (Q14)

```
Memory > Search

  Search: [ SOC2 evidence policy                                    ]🔍
  Scope:  [▾ All projects + Personal ▾]   Tiers: [✓ Memories  [ ] Learnings  [ ] Sessions]

  Results (8)
  ─────────
  ▼ website-foo (3)
       SOC2 evidence collection runbook         author: ava   2d ago
       Audit log retention policy               author: jim   1w ago
       Compliance freeze decision               author: jim   3w ago
  ▼ api-rebuild (2)
       SOC2 evidence pipeline                   author: ava   5d ago
       Auth event audit format                  author: ava   2w ago
  ▼ Personal (3)
       Jim · SOC2 evidence templates I keep     2w ago
       Jim · Compliance quick-ref               1mo ago
       Jim · Auditor contact info               3mo ago
```

## Remaining open questions

1. **Project-membership model under multi-user (Cloud) deployments.** In SaaS, a "user" might have their own set of agents and projects. Project membership becomes a tenant-scoped concept. Out of scope for the OSS / Desktop variants but noted for SaaS spec.
2. **Recent-activity surface across all projects** in Command Center — useful for operators with many projects? Or always-collapsed by default? Defer to UI implementation.
3. **Project picker keyboard accessibility.** Arrow-key + type-to-filter must work; the current agent picker may need a refactor. Frontend-lead implementation detail.

## Sequencing

This UI work is sequenced **after** the backend changes from `sandbox-redesign-2026-05.md` and `memory-redesign-2026-05.md` land. Before backend support exists, projects can't be created or bound. Within UI work, the order is:

1. New session modal with project + agent dropdowns (smallest user-visible change).
2. Command Center pivot to project-grouped rooms.
3. Session history with groupable view.
4. Project management surface (membership editor, git-share toggle).

## References

- `docs/internal/design/sandbox-redesign-2026-05.md` — two-room workspace topology.
- `docs/internal/design/memory-redesign-2026-05.md` — what lives in `<project>/.omnipus/`; auto-injection rules for `last-project-session.md`.
