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

## Target buckets (same names as the product)

User docs should follow the **module map**, not the old sidebar.

```
docs/
  getting-started.md          # install → first workspace chat (keep)
  concepts.md                 # rewrite: workspace, 4-base roster, connectors
  using-omnipus-ui.md         # rewrite: tour of workspace tabs, not Command Center
  using-omnipus-cli.md
  operations/                 # reverse-proxy, platform, docker — keep, audit
  connectors/                 # rename from docs/channels/ when guides move
  workspaces.md               # NEW: chat + board + calendar + goals + team
  agents.md                   # roster, default ★, custom / workers
  skills.md                   # keep
  browser.md                  # live browser (missing as a first-class guide)
  settings.md                 # providers, sandbox, usage
  README.md                   # index aligned to the above
```

**Do not** grow a second internal attic. New “why” → ADR. New “what we’re building” → spec. New “how a human uses it” → the thin handbook above.

Internal stays:

| Keep | Fold / don’t add to |
|---|---|
| `architecture/ADR-*` | Random `HANDOVER-*`, `*-rootcause-*` at `docs/internal/` root — `_archive/` or the issue they closed |
| `specs/` | Duplicate design HTML |
| `architecture/AS-IS-architecture.md` | Either refresh or stamp “frozen YYYY-MM” so nobody treats April 2026 as today |
| `BRD/` | Historical; link, don’t update as if current |

## Sequence (after acceptance)

1. Rewrite **`concepts.md`** and **`using-omnipus-ui.md`** (highest blast radius: Max, Command Center, missing Workspaces).
2. Fix **`docs/README.md`** index (Command Center / Channels / five teammates).
3. Retarget **`docs/channels.md`** → Connectors language; keep API/package name `channels` in a one-line note.
4. Add a short **Workspaces** page (chat is a tab).
5. Audit **`operations/`** and **`getting-started.md`** for leftover Max / Command Center.
6. Delete **`.preview-doc/`** when (1–5) cover anything still true.
7. Do **not** rewrite ADRs/specs as part of this — they are the source, not the patient.

## Enforcement (later)

Same idea as the file-budget ratchet, lighter: a `scripts/check-no-stale-user-docs.sh` that fails the **user** handbook (`docs/*.md` except `internal/`) if it reintroduces `Command Center`, `five teammates`, or `Max` as a core agent. Internal ADRs that *explain the deletion* are exempt.

## Out of scope

Editing any user guide in this pass. Deleting `.preview-doc/` in this pass. Splitting `CLAUDE.md` (module-map draft).
