# Draft architecture — documentation map

**Status:** draft (2026-09-12; not implemented, not an ADR, not a spec)
**Companion:** [draft-module-map.md](draft-module-map.md) (code). This note is the same idea for **docs**.
**Do not:** rewrite user guides or delete `.preview-doc/` until this is accepted.

## What we already trust

| Bucket | Where | Stance |
|---|---|---|
| ADRs | `docs/internal/architecture/ADR-*.md` (~139) | **Truth.** Continuously maintained. Code + ADR on conflict: code still wins, but these explain *why*. |
| Specs | `docs/internal/specs/` (~170) | **Truth for in-flight work.** Maintained with the features they describe. |
| Wire contracts | `contracts/` | **Machine truth.** CI (`make verify-contracts`) already gates drift. |
| Agent-facing | root `CLAUDE.md` | Operating rules for agents — fat, separate problem (module-map draft). |

## Confirmed: user / operator guides are outdated

Spot-check against the product as of 2026-09-12 (routes, CLAUDE.md retired surfaces, 4-base roster). **No files were edited for this check.**

The handbook lives in `docs/*.md`, `docs/operations/`, `docs/channels/`. Core pages still describe a product that no longer exists:

| Claim in the user docs | Reality |
|---|---|
| **Five teammates including Max** (`concepts.md`, `using-omnipus-ui.md`) | Max retired. Roster is Mia · Jim · Ava · Ray (+ workers / Judge / Plan Supervisor, not chat colleagues). |
| **Command Center** is the sidebar home for tasks (`using-omnipus-ui.md` § Command Center, `concepts.md` tasks) | Screen deleted. `/tasks` and `/automations` are redirects into a **workspace** Board/Calendar. |
| **Chat is the home screen**; work lives elsewhere | Chat is a **workspace tab**, next to Board, Calendar, List, Team. |
| Sidebar: Chat, Command Center, Agents, Connectors, Skills | Missing **Workspaces**. Command Center gone. |
| **Channels** as the product word (index, `channels.md`, screenshot alts) | UI is **Connectors**; package/API still `channels`. Guides mix the two. |
| Task board as a global Command Center | Board is **per workspace**. Goals/plans/calendar are the same product, unnamed in the handbook. |
| Screenshots of Command Center and “Channels” | Captions already admit they predate the rename — still the pictures. |

Last notable user-doc commit touching these files (2026-08-28) was the Channels → Connectors **rename**, not a tour of Workspaces. `concepts.md` still teaches Max and Command Center.

So: **yes, the user/operator layer is substantially stale.** ADRs/specs are not the problem. This handbook is.

## `.preview-doc/` — may be deleted (not done)

~16 HTML concept pages for the v0.3 Workspaces direction. CLAUDE.md already says they supersede the 2026-05 `docs/internal/design/` drafts **in intent**. They are discussion-stage, not shipping docs.

**Recommendation:** delete `.preview-doc/` once this docs map is accepted, *after* any still-true ideas are pointed at from ADRs/specs (or dropped). Do not treat HTML mockups as a third handbook. **Not deleted in this pass.**

`docs/internal/design/*-2026-05.md` (Rooms, 5-core) stays marked superseded; do not teach from it.

## Audit (2026-09-12) — four parallel read-only passes

No product docs were edited. Findings below.

### User handbook — rewrite the story, not a typo pass

Highest blast radius: `concepts.md`, `using-omnipus-ui.md`.

| File | Action | Why (evidence) |
|---|---|---|
| `docs/concepts.md` | **REWRITE** | L5/L9/L22: five teammates including **Max**. L44/L60: watch work on the **Command Center**. |
| `docs/using-omnipus-ui.md` | **REWRITE** | L7/L41/L160: five teammates + Max. L13: Chat is the home screen; Command Center in the sidebar. L203–218: entire Command Center section + `08-command-center.png`. Workspaces/calendar/goals absent. |
| `docs/using-omnipus-cli.md` | **EDIT** | L94: preview on `:5001`. L258: Command Center in the CLI/UI table. |
| `docs/getting-started.md` | **EDIT** | L28: `docker … -p 5001:5001`. L65: “Open Chat from the sidebar.” |
| `docs/troubleshooting.md` | **EDIT** | L47/L52/L62: ports 5000+5001 and `"preview_port": 5501` (key deleted, ADR-044). |
| `docs/routing.md` | **EDIT** | L135: Mia knows Jim/Ava/Ray/**Max**. |
| `docs/channels.md` | **EDIT + new screenshots** | Connectors rename captions; still titled Channels. |
| `docs/tools-reference.md` | **EDIT** | L25: “all five” agents. |
| `docs/README.md` | **REWRITE index** | Channels as the product word; no Workspaces. |

**Missing as first-class guides:** Workspaces (chat as a tab), Calendar, Goals, Board, live browser (WebRTC), agent types Main/Subagent/subagent_3p. Screenshots still show five agents and Command Center.

Keep with light audit: `memory.md`, `skills.md`, `docker.md` (preview deletion already noted), most of `configuration.md`.

### Operator docs — mostly true; leftover `exec` and preview keys

`reverse-proxy.md`, `sandbox-config.md`, `sandbox-limitations.md`, credential/security/docker unlock modes: **match CLAUDE.md**.

Stale:

| File | Action | Why |
|---|---|---|
| `docs/tools_configuration.md` | **REWRITE** “Exec Tool” section | ADR-036 unified `exec`/`workspace_shell` into **`bash`**. Env `OMNIPUS_TOOLS_EXEC_*` documented as if live. |
| `docs/operations/security-considerations.md` | **EDIT** | L83 still shows `"preview_origin"`. L108–110 still say `exec: deny`. |
| `docs/debug.md` | **EDIT** | L29 lists `exec` as a current tool. |
| `docs/operations/platform-support.md` | **EDIT** | L51: Windows Job Objects “once #113 closes” — no Windows sandbox backend (Fallback only, ADR-062). |

### BRD — delete (founder direction)

`docs/internal/BRD/` is a **March 2026** draft (six files). It is not the 2026-09 product:

- Roster: 3+1 (General Assistant / Researcher / Content Creator + Omnipus system agent) — not Mia/Jim/Ava/Ray.
- Appendix D: exclusive 41-tool **system agent** that drives the UI — that agent does not exist.
- Appendix C: full **Command Center** spec.
- Confirm-gate for destructive ops — deleted (ADR-081).
- Channels tabs, not Connectors; projects/rooms, not Workspaces.
- **Zero** goals-as-entities, Judge, Plan Supervisor.
- Appendix A: Windows Job Objects as if they ship — they do not.

**Do:** remove `docs/internal/BRD/` from the live tree when this map is accepted. Optional: one-page tombstone in `docs/internal/_archive/` so nobody implements from git history by accident. **Not deleted in this pass.**

### `docs/internal/design/*-2026-05.md` — archive

Five files, Rooms / two-room / Command Center / Max in `sandbox-redesign`. CLAUDE.md already says superseded by `.preview-doc/` intent. Move to `_archive/design-2026-05-rooms-era/` with a banner. Do not teach from them.

### `.preview-doc/` — delete after handbook rewrite (fourth pass complete)

18 HTML pages (2026-08-12) + two PNG QA folders. **ADR-019 already ratified the direction.** Chat-as-workspace-tab, 4-base roster, Automations/Tasks redirects, per-workspace delegation, email-as-tool — **shipping**.

Superseded inside the HTML itself: original email model (ADR-033), Rooms framing, global DelegationPolicy (ADR-037), Max as automator.

Unique leftovers (optional one-liners in an ADR or git history, not a reason to keep the folder): `delegation-audit.html` (if anything is not in ADR-037), `tools-catalog.html` (78-tool snapshot, likely drifted), `impact.html` effort table.

**Do:** delete `.preview-doc/` when the user handbook rewrite is accepted. Pointer: “historical v0.3 concept is in git (2026-08-12).” Visual QA PNGs need not be preserved. **Not deleted in this pass.**

The 2026-05 `design/` Rooms drafts are a *separate* delete/archive (see above); they are not saved by keeping the HTML.

## Target handbook (same names as the product)

Thin. One job per page. ADRs/specs stay internal.

```
docs/
  README.md                 # index: start here → workspace → connectors → agents
  getting-started.md        # install → first workspace chat (EDIT docker 5001)
  concepts.md               # REWRITE: workspace, 4-base roster, connectors, goals
  using-omnipus-ui.md       # REWRITE: workspace tabs (chat, board, calendar, team, …)
  using-omnipus-cli.md      # EDIT: drop 5001 and Command Center
  workspaces.md             # NEW: what a workspace is; chat is a tab; links to board/plans/calendar/goals
  tasks.md                  # NEW: Board + List — create, assign, start, status
  plans.md                  # NEW: a plan is a graph of tasks; play; Definition of Done; Plan Supervisor
  calendar.md               # NEW: workspace Calendar tab, recurrence (not cron)
  goals.md                  # NEW: a goal is its own thing; starts immediately; Judge reads the work
  agents.md                 # NEW: Mia/Jim/Ava/Ray, ★ default, custom, workers, Judge
  connectors.md             # rename channels.md; one line: API package still `channels`
  connectors/               # today’s docs/channels/*.md, retitled
  skills.md                 # KEEP: reusable playbooks an agent picks up
  tools.md                  # NEW: built-in AND MCP tools; Allow/ask/deny applies to both (MCP via per-server mcp_<server>_*); search_web lives here
  memory.md                 # KEEP: what the team remembers (recap, workspace room) — not a Board feature
  knowledge.md              # PLACEHOLDER — Knowledge Bases / knowledge management. TBD; not specified yet
  library.md                # NEW: Media tab + library — files the workspace holds
  browser.md                # NEW: live browser, WebRTC only
  previews.md               # NEW: agent-served sites (web_serve) — a link on the main gateway, not a second port; review in the live browser
  security.md               # NEW user: Allow/Deny, sandbox in plain English, credential vault
  settings.md               # NEW or split from configuration.md
  operations/               # keep; sandbox-config, sandbox-limitations, reverse-proxy (public_url + /preview/)
  operations/sandbox-config.md
  operations/sandbox-limitations.md
  operations/security-considerations.md
  operations/reverse-proxy.md  # preview path for operators: single listener, gateway.public_url
  troubleshooting.md        # EDIT: single port 5000
```

**Do not** grow a second internal attic. New “why” → ADR. New “what we’re building” → spec. New “how a human uses it” → the handbook above.

Internal:

| Keep | Remove from live path |
|---|---|
| `architecture/ADR-*` | `docs/internal/BRD/` (delete; optional archive tombstone) |
| `specs/` | `.preview-doc/` (after handbook rewrite) |
| `architecture/AS-IS-architecture.md` (stamp frozen or refresh) | `design/*-2026-05.md` → `_archive/` |
| | Root `HANDOVER-*`, `*-rootcause-*` → `_archive/` or the closing issue |

## Sequence (after acceptance)

1. Rewrite `concepts.md` + `using-omnipus-ui.md` (Max, Command Center, missing Workspaces).
2. Rewrite `docs/README.md` index.
3. `channels.md` → Connectors; new screenshots.
4. Add `workspaces.md`, `tasks.md`, `plans.md`, `calendar.md`, `goals.md`, `agents.md`, `browser.md`.
5. Operator EDIT pass: `tools_configuration.md` (`bash`), `troubleshooting.md` (port 5000), `security-considerations.md`, `getting-started.md` docker, `platform-support.md`.
6. Delete `docs/internal/BRD/`. Archive 2026-05 design drafts.
7. Delete `.preview-doc/` when (1–5) hold anything still true.
8. Do **not** rewrite ADRs/specs.

## Enforcement (later)

`scripts/check-no-stale-user-docs.sh` fails **user** handbook (`docs/*.md` except `internal/`) if it reintroduces `Command Center`, `five teammates`, or `Max` as a core agent. ADRs that explain the deletion are exempt. Same family as `check-no-jpeg-screencast.sh`.

## Critical review — gaps (2026-09-12)

The handbook is the right *shape*. These holes remain.

**Product surfaces with no page**

| In the app | Draft today | Gap |
|---|---|---|
| Workspace **Graph** tab | mentioned in the UI tour only | Need a short `graph.md` or a section in `workspaces.md` — don’t invent a fifth work page if Graph is just “who delegates to whom” |
| Workspace **Team** tab | missing | Delegation trust is **per workspace**. Not an Agents-screen global graph (ADR-037). Belongs in `workspaces.md` or `team.md` |
| **Usage** (`/usage`) | missing | Cost/tokens. Pointer from `settings.md` or a thin `usage.md` |
| **Profile** / “what agents should know about you” | missing | User context. `settings.md` or `profile.md` |
| **Policies** route | missing | Confirm what that screen is; likely folds into `tools.md` / `security.md` |
| **Heartbeats** | missing | Only agent-level schedule left after Command Center died. `agents.md` or `calendar.md` — pick one, don’t orphan it |
| **Email as a tool** (not a chat connector) | missing | ADR-033. `tools.md` + maybe Connectors “mailboxes” |
| **Steering / Stop** | missing | Mid-turn redirect and cancel. `using-omnipus-ui.md` chatting section |
| **Voice** | only Telegram aside today | Keep as a paragraph under chat/connectors, not its own book |
| **External CLI workers** (`subagent_3p`) | missing | `agents.md` |
| **Onboarding** | `getting-started.md` | Fine; don’t add a second page |
| **Lite build / WhatsApp** | channel page only | Stay in `connectors/whatsapp` |

**Existing docs the tree never assigned** (keep / fold / delete still open):

| File | Likely fate |
|---|---|
| `configuration.md` | Fold into `settings.md` + `operations/` — don’t keep both |
| `tools-reference.md` | Fold into `tools.md` (user) vs `tools_configuration.md` (operator) |
| `providers.md` | Fold into `settings.md` |
| `routing.md` | Fold into `connectors.md` (default agent per connector) |
| `credential_encryption.md`, `security_configuration.md`, `sensitive_data_filtering.md` | Fold into `security.md` (user) + `operations/security-considerations.md` |
| `observability.md`, `debug.md` | Operator; keep under `operations/` or troubleshooting |
| `docker.md` | Keep; already notes single listener |
| `config-versioning.md`, `hooks/`, `protocol/`, `migration/` | Operator/advanced; not the handbook. Keep or `operations/` |
| `operations/platform-support.md` | Keep (Windows sandbox truth) |

**Overlap risk:** `using-omnipus-ui.md` must **tour and link**, not retell `tasks.md` / `plans.md` / `tools.md`. Command Center stays dead — never a screen, never a doc section. The failure mode is a fat tour that duplicates every other page, not bringing that screen back.

**Two indexes, not one:** README for humans using the app; `operations/` README for running the box. The draft mixes them in one tree.

**Sequence hole:** step 4 lists workspaces/tasks/plans/calendar/goals/agents/browser and omits `tools.md`, `library.md`, `previews.md`, `security.md`, `memory.md` keep.

## Out of scope this pass

Editing any user guide. Deleting BRD, `.preview-doc/`, or design drafts. Splitting `CLAUDE.md` (module-map draft).
