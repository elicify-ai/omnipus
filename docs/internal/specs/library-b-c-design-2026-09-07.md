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
  `saveDocument()` and persists the new file. A saved PDF re-opens showing the
  entered values.
- **Binary save path — NEW backend contract (verified gap, 2026-09-07).** The
  existing `PUT /library/{ws}/content` is **text-only**: its `content` field is
  a UTF-8 string, written as `[]byte(req.Content)` with a 10 MB *text* cap
  (`pkg/gateway/rest_library.go` `handleLibraryContentPut`;
  `contracts/components/schemas/LibraryContentRequest.yaml`). Round-tripping a
  filled PDF through it would corrupt the bytes. B therefore needs a
  binary-capable save — either a base64 variant of the content request or a new
  raw-bytes upload route — defined contract-first (Constraint #8) before the UI
  wires to it. This is the one genuinely new backend piece in B.
- Honest states: a PDF with no form fields shows no fill affordance (but a
  drawn signature is still offered); a save failure surfaces the reason and
  keeps the user's entries in the tab (never a silent no-op).
- **Out**: PKI/cryptographic signatures, XFA forms, agent-driven filling.
- CSP: the worker already carries `wasm-unsafe-eval` (CSP audit) — no policy
  change; if a save path needs a capability the CSP blocks, reconfigure the
  library first and record the measurement (embed.go rule).
- Files: `src/components/library/preview/` (the PDF preview component + a new
  signature-pad subcomponent) and its tests, **plus** the binary-save contract
  (`contracts/`) + its `pkg/gateway` handler.

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
- **UI — persistent Library search bar (founder decision, 2026-09-07).** A
  search input that lives in the Library panel (not a ⌘K command palette — that
  option was considered and dropped). Keyboard-reachable. Below it a segmented
  filter (All / Notes / Records / Views) with per-kind counts. Results **replace
  the file list inline**; clearing the query restores the tree. Each result
  opens the right surface (a note in the preview, a base as its views). Empty
  and index-not-ready states are honest (reuse the round-2 freshness signal).
- Files: `contracts/`, `pkg/gateway/` (search handler), `src/components/library/`
  (a NEW search component + results) + `src/lib/api`. Owns its own files; must
  not edit the create-vault / chrome files C2–C4 own.

## C2 — Create a vault (UI + existing creation path)

No way to create a vault in the UI. Requirements:

- **Unified "+" create menu.** The Library gets one `+` control (the kit's
  `DropdownMenu`) that gathers the create actions, scoped to the current
  location: New note, New folder, **New vault**, Add mount…, Upload files…, and
  New workspace. New note/folder/mount/upload/workspace already exist — the menu
  consolidates them and adds New vault. Only New vault is genuinely new UI.
- **New vault dialog (`LibraryNewVaultDialog`, existing dialog family).** Fields:
  Name, and a **Location** picker (which workspace + which folder within it;
  defaults to the current workspace's root). On confirm it **scaffolds, not
  blank**: creates the folder + a `.omnipus-vault/` marker containing seed
  record-types and one starter view, attaches it, and it renders with the vault
  icon. Reuse the SAME creation path onboarding uses (find it; do not invent a
  second). Honest failure.
- **Open existing = Add mount + auto-detect (no separate "open vault" action).**
  When a mount is added, detect a `.omnipus-vault/` marker in it; if present,
  auto-attach, render the vault icon, and index with a visible progress line
  (reuse the round-2 freshness signal). Import-from-Obsidian stays OUT.
- Files: the Library/workspace nav + creation call; a NEW create-vault dialog
  component. Coordinates with C3/C4 (same nav area) — see ownership below.

## C3 — Distinct vault icon (cosmetic)

Vaults render with a generic folder glyph. Requirement: a distinct vault icon
(Phosphor, per brand) wherever a vault is listed, so it reads as a vault not a
folder. Small. Files: the vault list item component + icon usage.

## C4 — Fullscreen tab inherits the selection

The Library **already has a fullscreen control that opens it in a new tab**
(default view is a slide-out panel). Today that new tab opens at the default and
loses where you were. This is NOT a new "open document in a tab" action.

Requirement: when the existing fullscreen control is used, the new full tab
starts with the **same folder/item selection** that was active in the slide-out,
and the slide-out then closes on the originating screen. Selection travels via a
URL/query param (e.g. `/library?open=<relpath>`), which also makes the location
shareable/bookmarkable. Files: the Library route + panel selection state (URL
param) + the existing fullscreen control.

## Build structure (parallel, file-disjoint)

Three tracks, three worktrees, merged independently:
- **wt-B** — PDF fill/sign. Owns `src/components/library/preview/**` (PDF parts)
  **plus** the NEW binary-save contract (`contracts/`) + its `pkg/gateway`
  handler. Both B and C-search touch `contracts/`/`pkg/gateway` — the two schemas
  are disjoint (binary-save vs search), but regen/commit of `contracts/` is
  serialized through the lead to avoid a generated-file clash.
- **wt-C-search** — C1 (persistent search bar). Owns `contracts/` search schema,
  `pkg/gateway` search handler, and a NEW `src/components/library/search/**`
  component + `src/lib/api`.
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
