# Draft architecture — documentation map

**Status:** draft (2026-09-12; re-validated 2026-09-15 after the library-improvements merge ff11e8249; not implemented, not an ADR, not a spec)
**Companion:** [draft-module-map.md](draft-module-map.md) (code). This note is the same idea for **docs**. The companion is adding a **knowledge** product module for the same feature this note calls Knowledge Base — cross-reference it, don't duplicate its module boundary here.
**Do not:** rewrite user guides or delete `.preview-doc/` until this is accepted.

**Changes 2026-09-15** (re-validation after `ff11e8249` landed on `release/v0.1.1`):
- Every stale-text citation re-checked line by line against `1f996b01d`; all 14 line numbers in the two tables below held exactly, no drift found.
- Knowledge Base moved from a TBD placeholder to a scoped new page, and to **step 1** of the Sequence — it's now the single biggest documentation hole, bigger than the Workspaces rewrite.
- ADR and spec counts refreshed (150 ADRs, 198 specs, both higher than the 2026-09-12 estimate).
- Nine more product-surface rows added to the gaps table, each confirmed against `pkg/gateway/rest_*.go` or `src/components/`: Knowledge base, Library preview of knowledge markdown, sign-in / Copilot sign-in, host folders / mounts, automations (redirect, already tracked — no new page needed), mailbox, audit log, god mode, rate limits / retention. Voice was already listed; its shipped status is now confirmed with file citations.
- `docs/tools-reference.md` L25 row corrected: "all five" is five *filesystem tools*, not five agents — dropped as a stale-agent-count finding.
- Noted that ADR numbers renumbered again on 2026-09-15 (commit `1f996b01d`) after the merge collided two ADR-081s and two ADR-082s — cite ADR title alongside number everywhere in this doc that a bare number could go stale.

## What we already trust

| Bucket | Where | Stance |
|---|---|---|
| ADRs | `docs/internal/architecture/ADR-*.md` (150 as of 2026-09-15, `ls docs/internal/architecture/ADR-*.md \| wc -l`) | **Truth.** Continuously maintained. Code + ADR on conflict: code still wins, but these explain *why*. |
| Specs | `docs/internal/specs/` (198 as of 2026-09-15, `find docs/internal/specs -name '*.md' \| wc -l`) | **Truth for in-flight work.** Maintained with the features they describe. |
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

18 HTML concept pages for the v0.3 Workspaces direction (confirmed 2026-09-15, `ls .preview-doc/*.html | wc -l`). CLAUDE.md already says they supersede the 2026-05 `docs/internal/design/` drafts **in intent**. They are discussion-stage, not shipping docs.

**Recommendation:** delete `.preview-doc/` once this docs map is accepted, *after* any still-true ideas are pointed at from ADRs/specs (or dropped). Do not treat HTML mockups as a third handbook. **Not deleted in this pass.**

`docs/internal/design/*-2026-05.md` (Rooms, 5-core) stays marked superseded; do not teach from it.

## Audit (2026-09-12) — four parallel read-only passes

No product docs were edited. Findings below.

### User handbook — rewrite the story, not a typo pass

Highest blast radius: `concepts.md`, `using-omnipus-ui.md`.

All line numbers below re-verified 2026-09-15 against `release/v0.1.1` @ `1f996b01d` — every citation still holds; none drifted.

| File | Action | Why (evidence) |
|---|---|---|
| `docs/concepts.md` | **REWRITE** | L5/L9/L22: five teammates including **Max**. L44/L60: watch work on the **Command Center**. |
| `docs/using-omnipus-ui.md` | **REWRITE** | L7/L41/L160: five teammates + Max. L13: Chat is the home screen; Command Center in the sidebar. L203–218: entire Command Center section + `08-command-center.png`. Workspaces/calendar/goals absent. |
| `docs/using-omnipus-cli.md` | **EDIT** | L94: preview on `:5001`. L258: Command Center in the CLI/UI table. |
| `docs/getting-started.md` | **EDIT** | L28: `docker … -p 5001:5001`. L65: “Open Chat from the sidebar.” |
| `docs/troubleshooting.md` | **EDIT** | L47/L52/L62: ports 5000+5001 and `"preview_port": 5501` (key deleted, ADR-044). |
| `docs/routing.md` | **EDIT** | L135: Mia knows Jim/Ava/Ray/**Max**. |
| `docs/channels.md` | **EDIT + new screenshots** | Connectors rename captions; still titled Channels. |
| `docs/tools-reference.md` | ~~EDIT~~ **no change** | L25 “All five” is the five *filesystem tools* listed just above it (`read_file`/`write_file`/`edit_file`/`append_file`/`list_dir`), not five agents. Corrected 2026-09-15 — earlier reading of this line was wrong; drop it from the stale-agent-count list. |
| `docs/README.md` | **REWRITE index** | Channels as the product word; no Workspaces. |

**Missing as first-class guides:** Workspaces (chat as a tab), Calendar, Goals, Board, live browser (WebRTC), agent types Main/Subagent/subagent_3p. Screenshots still show five agents and Command Center.

Keep with light audit: `memory.md`, `skills.md`, `docker.md` (preview deletion already noted), most of `configuration.md`.

### Operator docs — mostly true; leftover `exec` and preview keys

`reverse-proxy.md`, `sandbox-config.md`, `sandbox-limitations.md`, credential/security/docker unlock modes: **match CLAUDE.md**.

Stale (line numbers re-verified 2026-09-15, all held):

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
- Confirm-gate for destructive ops — deleted (ADR-088, "work-first goal flow"; guard: `scripts/check-no-goal-confirm-gate.sh`). ADR numbers were renumbered again on 2026-09-15 (commit `1f996b01d`, after the library-improvements merge collided two ADR-081s and two ADR-082s) — cite the title alongside the number, since a future renumber can move ADR-088 again without moving the guard script.
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

## Knowledge Base — the biggest hole (found 2026-09-15)

The 2026-09-12 pass marked `knowledge.md` a placeholder because nothing was specified yet. That changed: the feature shipped on `release/v0.1.1` via the library-improvements merge (`ff11e8249`, 2026-09-15). This is now a bigger gap than the Workspaces rewrite, because it is completely undocumented rather than just describing an old product.

**What shipped, in code:** five Go packages, all new 2026-08-23 through 2026-09-15 — `pkg/knowledge` (notes: rename/move without breaking the collection), `pkg/records` (typed records layer), `pkg/vaultimport` (importing an existing Obsidian-style vault), `pkg/vaultprops` (record property schema), `pkg/library` (the path-safe workspace file tree that `pkg/knowledge` sits on). Binary media (images, video, PDF, audio) is a different package, `pkg/media/library` (ADR-051); that one backs `library.md`.

**What shipped, in decisions.** ADR numbers here collided during the 2026-09-15 renumber (see the ADR-088 note above) — cite by title, not number alone, and expect two files under one number until a further renumbering pass:
- ADR-089 — "Rename 'vault' to 'Knowledge Base', in three staged phases" (`ADR-089-rename-vault-to-knowledge-base.md`)
- ADR-067 — "Omnipus knowledge base and render-first preview" (`ADR-067-omnipus-knowledge-base-and-render-first-preview.md` — two more, unrelated ADR-067 files also exist: "CI design and build targets" and "registry-fed catalog and provider identity")
- ADR-068 — "Vault records: typed record layer" (`ADR-068-vault-records-typed-record-layer.md` — two more, unrelated ADR-068 files also exist: "bash text guard, third rule layer" and "subscriptions, provider deletion and provider UX")
- ADR-081 — "Unified Library search, general file search, and the grep engine" (`ADR-081-unified-library-search-and-grep-engine.md` — this is the ADR-081 that *kept* its number in the 2026-09-15 renumber)
- ADR-083 — "Embedded content in knowledge-base notes" (`ADR-083-embedded-content-in-knowledge-base-notes.md`), amended by a second ADR-083 covering record creation over REST and the `.seq` identifier allocator

**What shipped, in specs** (`docs/internal/specs/`, roughly 18 files once review rounds are counted): `adr-067-knowledge-base-and-preview-spec.md`, `vault-records-spec-2026-08-25.md`, `unified-search-and-grep-spec.md`, `view-kinds-design-2026-09-03.md`, `library-b-c-design-2026-09-07.md`, `library-spec.md`, `library-improvements-requirements-2026-08-21.md`, `workspace-media-library-and-presentation-layer-spec.md`, plus UAT findings (`uat-vault-records-2026-08-28.md`, `uat-library-records-2026-08-26.md`, `uat-findings-view-kinds-2026-09-05.md`) and an implementation plan (`vault-records-implementation-plan-2026-08-28.md`). Several of these have 2-6 numbered "review round" siblings — write from the base spec, skim the review rounds only for ratified changes.

**What shipped, in the product:** notes/records with typed fields, multiple view kinds (not just a flat note list), a unified find/grep across the workspace's knowledge content, importing an existing vault, and embedding rich content (images, code fences that render, queries) inside a note.

**The feature is reachable in the shipped UI.** The route `src/routes/_app/library.tsx` renders `LibraryExplorer`, which imports and renders `KnowledgePanel` (`src/components/library/LibraryExplorer.tsx` line 92). This is a live screen, not dead packages.

**Zero user-facing pages exist.** `grep -ril 'knowledge base' docs --include='*.md' | grep -v internal` returns nothing. No handbook page mentions notes, records, views, vault import, or embedded content at all.

**library.md vs knowledge.md — the split, decided here:** `library.md` covers the Media tab — binary files a workspace holds (images, video, PDFs, audio) and their preview. `knowledge.md` covers everything textual and structured — notes, typed records, views, find/grep across knowledge content, importing an existing vault, and embedding rich content inside a note. They share the Library UI surface but not the engine. `library.md` is backed by `pkg/media/library` (binary media store). `knowledge.md` is backed by `pkg/knowledge` and `pkg/records` on top of `pkg/library` (text file tree). They are different jobs for a reader: "where do my files live" vs "how do I take and structure notes." If the founder disagrees with this split, it is the one open call in this document — everything else here is a correction of fact, not a decision.

**Sequencing:** because this is a whole undocumented feature rather than a stale-text fix, write `knowledge.md` **before** `concepts.md`/`using-omnipus-ui.md` (see Sequence step 1) — the roster/Command Center rewrite touches existing prose, but Knowledge Base has no prose to correct, so it can be drafted in parallel without waiting on anything else, and skipping it another cycle means the biggest gap keeps growing.

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
  knowledge.md              # NEW, WRITE FIRST (see Sequence step 1): notes, records, views, find/grep, vault import, embedded content
  library.md                # NEW: Media tab — the files (images, video, PDFs, audio) a workspace holds; not notes
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

Re-ordered 2026-09-15: Knowledge Base moved to step 1. It has no stale prose to correct (no page exists at all), so it does not need to wait on the roster/Command Center rewrite and can start in parallel today — while every day it doesn't ship, the gap it fills keeps growing as more of the feature lands.

1. **Write `knowledge.md` and `library.md` (split per the decision above).** Source from ADR-089, ADR-067 ("knowledge base and render-first preview"), ADR-068 ("vault records"), ADR-081, ADR-083 (embedded content + the REST/`.seq` amendment), and the specs listed above.
2. Rewrite `concepts.md` + `using-omnipus-ui.md` (Max, Command Center, missing Workspaces).
3. Rewrite `docs/README.md` index.
4. `channels.md` → Connectors; new screenshots.
5. Add `workspaces.md`, `tasks.md`, `plans.md`, `calendar.md`, `goals.md`, `agents.md`, `browser.md`, `tools.md`, `previews.md`, `security.md`. Fold `configuration.md`/`tools-reference.md`/`providers.md`/`routing.md`/credential-and-security docs per the "Existing docs the tree never assigned" table below. Keep `memory.md` as-is (light audit only).
6. Operator EDIT pass: `tools_configuration.md` (`bash`), `troubleshooting.md` (port 5000), `security-considerations.md`, `getting-started.md` docker, `platform-support.md`.
7. Delete `docs/internal/BRD/`. Archive 2026-05 design drafts.
8. Delete `.preview-doc/` when (1–6) hold anything still true.
9. Do **not** rewrite ADRs/specs.

## Enforcement (later)

`scripts/check-no-stale-user-docs.sh` fails **user** handbook (`docs/*.md` except `internal/`) if it reintroduces `Command Center`, `five teammates`, or `Max` as a core agent. ADRs that explain the deletion are exempt. Same family as `check-no-jpeg-screencast.sh`.

## Critical review — gaps (2026-09-12; surfaces re-confirmed and extended 2026-09-15)

The handbook is the right *shape*. These holes remain.

**Product surfaces with no page**

| In the app | Draft today | Gap |
|---|---|---|
| **Knowledge base** (notes, records, views, find/grep, vault import, embedded content) | was a TBD placeholder; now scoped, see the Knowledge Base section above | Biggest gap in the handbook. `knowledge.md`, step 1 of the Sequence |
| **Library preview of knowledge markdown** | missing | The Library preview pane rendering a knowledge note (not just media). Belongs in `knowledge.md` / `library.md`. Keep it distinct from `previews.md` (agent `web_serve` sites on `/preview/`) — this is the Library panel rendering a note, a different surface entirely |
| Workspace **Graph** tab | mentioned in the UI tour only | Need a short `graph.md` or a section in `workspaces.md` — don’t invent a fifth work page if Graph is just “who delegates to whom” |
| Workspace **Team** tab | missing | Delegation trust is **per workspace**. Not an Agents-screen global graph (ADR-037). Belongs in `workspaces.md` or `team.md` |
| **Usage** (`/usage`) | missing | Cost/tokens. Pointer from `settings.md` or a thin `usage.md` |
| **Profile** / “what agents should know about you” | missing | User context. `settings.md` or `profile.md` |
| **Policies** route | missing | Confirm what that screen is; likely folds into `tools.md` / `security.md` |
| **Heartbeats** | missing | Only agent-level schedule left after Command Center died. `agents.md` or `calendar.md` — pick one, don’t orphan it |
| **Email as a tool** (not a chat connector) | missing | ADR-033. `tools.md` + maybe Connectors “mailboxes” |
| **Steering / Stop** | missing | Mid-turn redirect and cancel. `using-omnipus-ui.md` chatting section |
| **Voice** | only Telegram aside today | Confirmed shipped (`pkg/gateway/rest_voice.go`, `src/components/agents/voice-provider-sub.tsx`). Keep as a paragraph under chat/connectors, not its own book |
| **External CLI workers** (`subagent_3p`) | missing | `agents.md` |
| **Onboarding** | `getting-started.md` | Fine; don’t add a second page |
| **Lite build / WhatsApp** | channel page only | Stay in `connectors/whatsapp` |
| **Sign-in / Copilot sign-in** | missing | Confirmed shipped (`pkg/gateway/rest_signin_copilot.go`, `rest_sign_in.go`, `src/components/providers/SignInDialog.tsx`, `AuthMethodControl.tsx`). It's a provider auth method, not a new page — fold into `settings.md` / `providers.md` fold target |
| **Host folders / mounts** | missing | Confirmed shipped (`pkg/gateway/rest_host_folders.go`, `rest_workspace_mounts.go`, `src/components/library/LibraryMounts*.tsx`, `LibraryAddMountDialog.tsx`). Belongs in `library.md` (mounting a folder into the workspace's files) |
| **Automations** | redirect stub only, already tracked | Confirmed still just a redirect (`src/routes/_app/automations.tsx`, `pkg/gateway/rest_automations.go` backs the Board/Calendar it redirects into). No separate page needed — already covered by the "Retired surfaces" rule in CLAUDE.md |
| **Mailbox** | missing | Confirmed shipped (`pkg/gateway/rest_mailbox.go`, `src/components/connectors/EmailMailboxPanel.tsx`). This is the Connectors-side email UI — fold into `connectors.md`, distinct from “email as a tool” above |
| **Audit log** | missing | Confirmed shipped (`pkg/gateway/rest_audit_log.go`, `src/components/settings/AuditLogViewer.tsx`). `security.md` or `settings.md` |
| **God mode** | missing | Confirmed shipped (`pkg/gateway/rest_god_mode.go`, `src/components/settings/GodModeControl.tsx`). Sandbox `off` opt-in — `security.md`, must say plainly what it disables |
| **Rate limits / retention / memory settings** | missing | Confirmed shipped (`src/components/settings/DataSection.tsx` — `session_retention_days`, rate limits). Fold into `settings.md` |

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

**Sequence hole — closed 2026-09-15:** the old step 4 listed workspaces/tasks/plans/calendar/goals/agents/browser and omitted `tools.md`, `library.md`, `previews.md`, `security.md`, `memory.md` keep. Fixed in the Sequence section above (now step 5) and `knowledge.md`/`library.md` moved to their own step 1.

## Validation 2026-09-15 — nothing has been executed

This is a re-validation pass, not an implementation pass. Checked directly against `release/v0.1.1` @ `1f996b01d`:

- **0 of 13 new pages exist**: `workspaces.md`, `tasks.md`, `plans.md`, `calendar.md`, `goals.md`, `agents.md`, `tools.md`, `knowledge.md`, `library.md`, `browser.md`, `previews.md`, `security.md`, `settings.md` — none present in `docs/`. (The `connectors.md` rename and `connectors/` retitle are separate — a rename, not a new page — and also not done.)
- **No rewrite started**: `concepts.md` and `using-omnipus-ui.md` still teach five teammates including Max and the Command Center, word for word (see the re-verified line citations above).
- **BRD still live**: `docs/internal/BRD/` (6 files) is still in the tree, not archived or deleted.
- **`.preview-doc/` still live**: 18 HTML files, not deleted.
- **2026-05 design drafts still live**: the five `docs/internal/design/*-2026-05.md` files are still in their original location; `_archive/design-2026-05-rooms-era/` does not exist (`ls docs/internal/_archive/` shows no such directory).
- **No enforcement script**: `scripts/check-no-stale-user-docs.sh` does not exist. For comparison, the repo already has 25 other `scripts/check-no-*.sh` guards (e.g. `check-no-jpeg-screencast.sh`, `check-no-fail-closed-backfill.sh`, `check-no-goal-confirm-gate.sh`) — the precedent for wiring a mechanical guard into CI is well established, this one specifically just hasn't been written yet.

## Out of scope this pass

Editing any user guide. Deleting BRD, `.preview-doc/`, or design drafts. Splitting `CLAUDE.md` (module-map draft).
