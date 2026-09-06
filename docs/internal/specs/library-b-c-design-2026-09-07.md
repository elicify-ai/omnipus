# Library B + C — PDF fill/sign and the missing UI surfaces

Status: RATIFIED requirements (founder interview 2026-09-07) → implementing in parallel
Branch: feat/library-improvements
Source: the two round-2 test reports (Issues 1,2,15,16,17) + founder interview.

Founder rulings (2026-09-07):
- **B**: full scope — human fills form fields AND draws a signature, saved into the PDF. Cryptographic/PKI signing stays OUT (B4, its own ADR).
- **C**: all four surfaces, none deferred.
- **Human search**: full — text + typed properties + views, reusing the agent search index.
- **Sequence**: B and C in parallel.

---

## B — PDF form-fill and drawn signature (frontend, PDF.js)

Builds on ADR-067 §7's measured feasibility (`pdfjs-dist`, `annotationStorage` +
`saveDocument()`, drawn signature as an `/Ink` annotation, re-verified by an
engine unrelated to PDF.js). Scope here is the UI + save, not new evidence.

Requirements:
- The PDF preview gains an **Edit** affordance. In edit mode the human can:
  - fill AcroForm fields — text, checkbox, radio, dropdown — through PDF.js's
    annotation layer (`renderInteractiveForms` / annotation storage);
  - draw a signature (freehand) placed as an `/Ink` annotation on the page.
- **Save** writes the filled/signed values into the PDF bytes via
  `saveDocument()` and persists the new file (same save path the Library edit
  uses for text files). A saved PDF re-opens showing the entered values.
- Honest states: a PDF with no form fields shows no fill affordance; a save
  failure surfaces the reason (never a silent no-op).
- **Out**: PKI/cryptographic signatures, XFA forms, agent-driven filling.
- CSP: the worker already carries `wasm-unsafe-eval` (CSP audit) — no policy
  change; if a save path needs a capability the CSP blocks, reconfigure the
  library first and record the measurement (embed.go rule).
- Files: `src/components/library/preview/` (the PDF preview component + a new
  signature-pad subcomponent) and its tests. No backend beyond the existing
  file-save endpoint.

## C1 — Human vault search (backend endpoint + Library UI)

Today search exists only as the agent `knowledge_find` tool; a person has no
way to search the vault. Ruling: **full search — text + properties + views**.

Requirements:
- **Endpoint (contract-first, Constraint #8):** a REST search over a workspace
  vault that returns, for a query: text hits (note title + snippet), records
  matching by typed property, and matching saved views/bases. Reuse the SAME
  index the agent uses (`pkg/vaultprops` / `knowledgefind`) so it inherits the
  round-2 prefix/coverage/freshness fixes — do NOT build a second search
  engine. Schema in `contracts/`, regen, then handler in `pkg/gateway`.
- **UI:** a search box in the Library (keyboard-reachable, `/` or ⌘K style),
  results grouped by kind (Notes / Records / Views), each result opening the
  right surface (a note in the preview, a base as its views). Empty and
  index-not-ready states are honest (reuse the round-2 freshness signal).
- Files: `contracts/`, `pkg/gateway/` (search handler), `src/components/library/`
  (a NEW search component + results) + `src/lib/api`. Owns its own files; must
  not edit the create-vault / chrome files C2–C4 own.

## C2 — Create a vault (UI + existing creation path)

No way to create a vault in the UI. Requirement: a discoverable control
(Library header / workspace switcher) that creates a new vault, using the
SAME workspace/vault-creation path onboarding already uses (find it; do not
invent a second). Names it, lands the user in the empty vault. Honest failure.
Files: the Library/workspace nav + creation call; a NEW small create-vault
component. Coordinates with C3/C4 (same nav area) — see ownership below.

## C3 — Distinct vault icon (cosmetic)

Vaults render with a generic folder glyph. Requirement: a distinct vault icon
(Phosphor, per brand) wherever a vault is listed, so it reads as a vault not a
folder. Small. Files: the vault list item component + icon usage.

## C4 — New tab carries panel state

Opening the Library in a new tab loses the current selection/view. Requirement:
the new tab inherits the panel's current item + view (via URL/query state), and
once inherited the slide-over panel may close. Files: the Library route + panel
state (URL param) + the open-in-new-tab control.

## Build structure (parallel, file-disjoint)

Three tracks, three worktrees, merged independently:
- **wt-B** — PDF fill/sign. Owns `src/components/library/preview/**` (PDF parts).
- **wt-C-search** — C1. Owns `contracts/` search schema, `pkg/gateway` search
  handler, and a NEW `src/components/library/search/**` component + `src/lib/api`.
- **wt-C-chrome** — C2 + C3 + C4. Owns the Library nav / route / workspace
  switcher / vault-list-item (create-vault, vault icon, new-tab state).

Collision rule: C-search and C-chrome both live under `src/components/library`
but in DISJOINT subtrees (`search/` vs the nav/route files). Neither edits
`LibraryPreviewPane.tsx` (B's neighbour) beyond wiring its own entry point; if a
shared file (a route index, a barrel export) genuinely must change on both
sides, that change is serialized through the lead, not done twice.

Each track: contract-first for any wire type; `npm run typecheck` + scoped
`vitest` green; build tags `goolm,stdjson` for any Go; reproduce/observe in the
real renderer (Playwright) before claiming a UI behaviour done; commit per unit,
no `Co-Authored-By`; do not push (lead integrates). Then the 7-reviewer gate:
`/code-review` + `/grill-code` over the whole B+C diff, fix all, CI green, and a
UAT that drives each surface in a real browser.

---

## Icon system — LOCKED (founder, 2026-09-07)

The container hierarchy uses ONE coherent custom set on Phosphor's 24px grid.
Rule proven the hard way this session: **every icon is judged at 16px first**,
because a fine inner symbol that reads at 32px can vanish at list size.

| Concept | Base shape | Treatment | Colour | Knockout symbol |
|---|---|---|---|---|
| **Workspace** | rounded **tile** (not a folder — it's the container above) | solid fill + knockout | gold `--color-accent` #d4af37 | **bold 2×2 cells** (large, thick — reads at 16px) |
| **Vault** | folder | solid fill + knockout | gold `--color-accent` #d4af37 | **spark** (4-point, enlarged to fill the front panel) |
| **Folder** | folder | outline | muted `--color-muted` #9ca3af | none |
| **Mount** | folder | solid fill + knockout | **`--color-mount` #8ea3bd** (Liquid-Silver pewter — NEW token) | **external ↗** |

Notes:
- Knockouts are cut to `--color-primary` (#0a0a0b).
- Colour does the first-glance separation (gold vault / silver mount / muted
  folder / gold tile-workspace); the knockout confirms the kind.
- Workspace is a TILE, not a folder, so gold-tile never blurs with gold-vault-
  folder; a mount that *contains* vaults keeps the mount icon at its root with
  vault icons on the vault folders inside it.
- One new palette token to add: **`--color-mount` #8ea3bd**. Everything else
  reuses existing tokens.
- Custom set (deliberate) — Phosphor has no coherent workspace/vault/mount
  family; native Phosphor is kept everywhere else in the app.
