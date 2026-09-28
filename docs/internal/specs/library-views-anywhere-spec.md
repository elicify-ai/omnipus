# Library views, anywhere — a saved view is a file like a note

**Status:** Draft

**Input**: GitHub issue #1017. Founder direction, verbatim, 2026-09-28: "we could simply
treat views as files that can be stored anywhere like a note only with a different icon" /
"a view must be stored like a note, anywhere where it makes sense." Also: "Server first",
per-view-kind icons (table, calendar, chart, …), and a view opens in the Library PREVIEW
pane, not a modal. The top "Saved views" block goes away (#1013, tracked separately — not
this spec's job to implement, noted here only because it changes what "reachable" means for
US-5/US-6 below).

**Evidence baseline**: `feat/library-views-anywhere-spec` cut from `release/v0.1.1` @
`ee7640c90` (read-only checkout used to verify every claim below). Round-1 fixes below were
verified against the same worktree at the grill's reviewed commit `988c420bd` and the round-1
review-merge commit `399afbc9f`.

**Routing note (CLAUDE.md "Routing rule") — corrected in round 1**: the original version of
this note recommended v0.3 because the change drops a back-compat path. That reasoning was
wrong (grill OBS-002): "no migrations, no back-compat shims" is the project's *standing* rule
for every release (2026-09-15 greenfield ruling), not a v0.3 marker — its absence proves
nothing about which release a change belongs to. **Founder decision, FD-4 below (2026-09-28):
this ships in `v0.1.1`**, the current release line, as a feature-size change — storage move,
three agent-tool surfaces, one contract change, a new preview case. Team-lead schedules it
there, not in v0.3.

---

## Founder Decisions (2026-09-28)

These answer the grill review's OWN Q1–Q4
(`docs/internal/specs/library-views-anywhere-spec-spec-review.md`, "Questions for the
founder") — a different, later question set from this spec's own Q1–Q5 in §3 below, which
remain answered as they were except where a decision here explicitly changes one. Recorded
here verbatim in substance, per the founder's direct instruction. Where a decision below
conflicts with what the grill recommended, this decision wins; §18 "Review round 1
dispositions" records that outcome finding-by-finding.

- **FD-1 (answers grill Q1) — a view runs only inside a knowledge base.** A view works
  anywhere INSIDE a knowledge base, at any depth. A `.view` file that sits outside every
  knowledge base — a plain workspace folder, a mounted folder with no knowledge base of its
  own — shows in the Library as a plain, unrunnable file: no view icon, no `is_view` flag on
  the wire, no preview case. See D-SCOPE, FR-VA-001, FR-VA-010, and the MAJ-005 disposition
  in §18.
- **FD-2 (answers grill Q2) — Omnipus-mediated copies are auto-renamed; every other clash
  switches both off, visibly.** A copy, a trash restore, or an upload made THROUGH Omnipus's
  own Library operations that would otherwise produce a second view sharing an existing
  `name` gets that `name` auto-suffixed to stay unique — the person doing the copy never sees
  a collision. Any OTHER same-name clash (a hand-authored second file, a sync client's
  conflict copy, anything landing on disk by a path other than Omnipus's own copy/restore/
  upload code) still switches BOTH views off (D-DEDUP, unchanged) — but now with a visible
  warning that was missing before: a badge on both files in the Library tree, a banner in the
  preview of both, and both named (never one silently missing) in `knowledge_describe` and
  `knowledge_find` output for every agent. See D-DUPLICATE, FR-VA-019–021, CRIT-002
  disposition.
- **FD-3 (answers grill Q3) — `.base` wins; this is NOT the grill's recommendation.** A view
  imported from a `.base` file is re-derived from that `.base` every time it changes, and any
  edit made to such a view — by a person in the Library editor, or by an agent through
  `write_view` — is overwritten the next time its `.base` is saved. The grill recommended the
  opposite ("import once, then independent," its Q3/B); the founder's explicit choice is
  `.base`-wins instead. Because this is a surprising rule for a file that otherwise behaves
  "like a note," it MUST be visible everywhere someone could be surprised by it:
  - the Library tree shows a "derived" badge on such a view's file;
  - the preview shows a banner naming the source `.base` file and stating that edits are
    overwritten on the next `.base` save;
  - `knowledge_describe`, `knowledge_find`, and `knowledge_configure`'s write results all
    state that the view is derived and name its source `.base`.

  A HAND-MADE view — one never written by the `.base` import/re-derivation pipeline — is
  never touched by re-derivation, unconditionally, for as long as it stays hand-made. The
  system tells derived from hand-made by **persisted provenance, not inference**: a new field
  records which `.base` file currently manages a view, set only by the import/re-derivation
  pipeline itself, never by `create_view`/`write_view`, and never inferred from the
  pre-existing `source` field — F1 already documents `source` as descriptive and "never
  re-read," so it cannot double as the field re-derivation depends on without breaking that
  existing invariant, and a hand-made view could legitimately set `source` for its own
  descriptive reasons without thereby becoming derived. See D-PROVENANCE, §4 step 6,
  FR-VA-022–026, MAJ-001 disposition.
- **FD-4 (answers grill Q4) — ships in v0.1.1, not v0.3.** See the corrected Routing note
  above.
- **The original spec's own §3 Q4 (orphan-directory startup WARN) is DROPPED.** Per MAJ-006 it
  is upgrade-only code the 2026-09-15 greenfield ruling forbids. §3 Q4 below is retired in
  favor of firm decision D-Q4-NO-WARN (option A, no WARN); FR-VA-017, SC-VA-007, TDD test 18,
  Dataset E, and Holdout 7 are deleted from this document.

---

## 1. Facts established from the code (verified, cited `file::symbol`)

| # | Claim | Evidence | Certainty |
|---|---|---|---|
| F1 | A view has at most one `source` string, records where it was imported from, and is never re-read. Data is one `filter` tree plus an optional single `type`. No joins. | `contracts/openapi.yaml::components.schemas.ViewDef` (source field description); `pkg/records/view.go::SavedView.Source` | Verified |
| F2 | `.base` files are ordinary, visible Library entries today (extension-classified, not hidden); views are hidden because they live under a dot-prefixed control-plane directory. | `pkg/library/entries.go::listDir` (hidden = `strings.HasPrefix(de.Name(), ".")`, line ~41); `src/components/library/preview/libraryPreviewKind.ts::classifyLibraryEntry` (`if (e === 'base') return 'base'`) | Verified |
| F3 | **Corrected in round 1 — the original claim was Partly false (MIN-006).** There is no `LoadAll` in `pkg/records`. `records.LoadViews` is the one read function every consumer of views calls, and every one of them derives its directory from `records.ViewsDir`. It has 13 production call sites across 10 files (F4). Writers hard-code the same directory via `ViewsDir`/`ViewsDirName`. | `pkg/records/view.go::LoadViews` (13 verified call sites, F4) | Verified |
| F4 | **Corrected in round 1 — the original "8 sites / 6 files" count was inaccurate and incomplete (MIN-006).** Full inventory, re-verified first-hand for round 1. **Writers** — 9 `ViewsDir(` call sites across 7 files: `pkg/records/view.go::LoadViews` (builds the directory it reads); `pkg/knowledge/knowledge_base_create.go` (`MkdirAll` on KB creation); `pkg/knowledge/knowledge_configure.go` ×2 (`write_view`'s name-refusal check, and its write); `pkg/knowledge/knowledge_configure_create_view.go` ×2 (`create_view`'s equivalents); `pkg/vaultimport/run.go` (one-shot importer — F15 below corrects "one-shot"); `pkg/vaultimport/rederive.go` (per-`.base`-save re-derivation, F15); `pkg/gateway/rest_library_write.go` (`MkdirAll` on KB creation via the Library door). Plus `ViewsDirName`-only (no literal `ViewsDir(`) users: `pkg/knowledge/knowledge_configure_create_view.go` (starter-file help text), `pkg/vaultimport/view_translate.go` (the produced `relPath` string), and `pkg/app/internal/records/command.go`'s CLI help text. **Readers** — 13 `records.LoadViews` call sites across 10 files: `pkg/knowledge/knowledge_edit.go`, `pkg/knowledge/tools.go`, `pkg/knowledge/knowledge_configure.go` ×5 (two dedup/refusal checks, `serveRefusalFor`, and a before/after pair around a schema edit), `pkg/knowledge/knowledge_configure_create_view.go`, `pkg/vaultprops/find_env.go`, `pkg/vaultimport/run.go`, `pkg/vaultimport/rederive.go`, `pkg/gateway/rest_knowledge_views.go`, `pkg/gateway/rest_knowledge_base_views.go`. `delete_view` (F5a) is a THIRD writer category (delete, not create/overwrite) that the original inventory omitted entirely (MAJ-004). SC-VA-002's re-sweep at CHECK time must cover both lists plus `delete_view`. | Grep re-sweep for round 1: `grep -rn "ViewsDir(\|ViewsDirName\|\.LoadViews(" pkg/ --include='*.go' \| grep -v _test.go` (GitNexus still unavailable — see §1a) | Verified (grep), Inferred (completeness — see §1a) |
| F5 | Agent tools touching views: `knowledge_describe`, `knowledge_find`, `knowledge_configure` (ops `write_view`, `create_view`, `delete_view` — F5a corrects the original list, which omitted `delete_view`, MAJ-004 — plus reads via the shared `ViewFindLoader`). Founder rule: agents must do everything wherever views are stored. | `pkg/knowledge/knowledge_configure.go::opWriteView, opCreateView, opDeleteView`; `pkg/knowledge/knowledge_describe.go::renderViews`; `pkg/records/knowledgefind/find.go` (ViewFindLoader references) | Verified |
| F5a | **New in round 1 (MAJ-004).** `delete_view` (`opDeleteView`) resolves a view by name against the loaded `ViewSet` and removes its file with a bare `os.Remove` (via `removeControlPlaneFile`) — no trash, no restore, unlike every other agent-facing Library deletion once a view is an ordinary visible file. | `pkg/knowledge/knowledge_configure.go::execDeleteView, removeControlPlaneFile, opDeleteView` | Verified |
| F6 | Renderer for a view's rows already exists (`ViewPartsRenderer.tsx`, an exhaustive switch over `ViewResultPart.part`); the Library preview pane's `renderBody` switch has no `'view'` case — only `'base'`, which mounts `BasePreview` (tabs over evaluated view results). | `src/components/library/preview/viewparts/ViewPartsRenderer.tsx::renderPart`; `src/components/library/LibraryPreviewPane.tsx::renderBody` (case `'base'` at ~line 172, no `'view'` case) | Verified |
| F7 | `ViewDef.name` is documented "unique within the vault" (global, not per-directory) and this is already enforced: `loadViewPaths` rejects any two views sharing a `name` today, and `ViewSet.Resolve` looks a view up by slug (`Def.Name`, unique by construction) or, on ambiguity, by `DisplayLabel()` (label collisions ARE allowed and disambiguated via `ViewAmbiguousLabel`/`ViewLabelCandidate`). | `contracts/openapi.yaml::ViewDef.name` description; `pkg/records/view.go::Resolve, ViewAmbiguousLabel, ViewLabelCandidate, RejectViewDuplicateName` | Verified |
| F8 | `ViewDef.kind` is a closed 8-value enum (`table, list, tiles, board, calendar, summary, trend, breakdown`) used by `create_view`'s composer; it is a *different* vocabulary from `ViewDef.layout` (6 values: `table, cards, board, calendar, gallery, map`) and from the render-time `ViewResultPart.part` (8 values: `table, list, tiles, columns, calendar, figures, chart, crosstab`). `kind` is NOT required by the schema (only `name` is) — `write_view`'s hand-authored path can omit it. | `contracts/openapi.yaml` (`kind:` enum ~line 710; `layout:` enum ~line 162); `pkg/knowledge/view_kinds.go::ViewKindTable...ViewKindBreakdown` (re-exported from `generated.ViewDefKind*`) | Verified |
| F9 | Both schema files (`SchemaDir`) and view files (`ViewsDir`) are written today with the same plain `.yaml` suffix, distinguished only by which control-plane directory holds them. | `pkg/knowledge/knowledge_configure.go::controlPlaneFileExt = ".yaml"` (used at line 551 for schemas and line 849 for views) | Verified |
| F10 | `pkg/knowledge.CollectionRoot` + `WalkContained` already exist and do exactly the safe, symlink-refusing, containment-checked, whole-collection walk this feature needs: never follows a symlink (FR-044), every path proven to resolve inside the real (post-symlink) collection root (FR-043), and permanently skips `.obsidian`, `.omnipus-vault`, `.git`, `.trash` at any depth via `scanSkippedDirNames`. `BuildLinkGraph` already pays this walk's full cost once per collection-scope request for the link graph. | `pkg/knowledge/contain.go::CollectionRoot, WalkContained, ErrCollectionRootInvalid`; `pkg/knowledge/graph.go::BuildLinkGraph` | Verified |
| F11 | `pathsafe` (`ValidateComponent`, `ValidateRelPathLength`, `RuleSet.ValidateNameShape`, `FirstIllegalRune`, `IsReservedDeviceName`, …) is the existing filename-safety layer; `CollectionRoot.ResolveContained` / `ResolveContainedNoSymlink` / `ResolveControlWritePath` are the existing path-containment layer for writes. Neither is currently exercised by the view writers, because today's writer only ever joins a fixed, server-controlled `ViewsDir(root)` with a slugged name — there is no caller-supplied destination folder to validate. | `pkg/pathsafe/pathsafe.go`, `pkg/pathsafe/rules.go`; `pkg/knowledge/contain.go::CollectionRoot.ResolveContained` et al.; `pkg/knowledge/knowledge_configure.go:849` (fixed join, no destination argument) | Verified |
| F12 | This repo has no existing persistent index/cache for views. `LoadViews` does one flat `os.ReadDir` per call (cheap: O(views), one directory). The only content-hash snapshot pattern in `pkg/records` is `SnapshotSchemas` for **schemas**, not views, and nothing wires it to views. `pkg/knowledge/manifest.go` already tracks every file in a collection with size and modification time, at the same 100,000-file scale (MIN-003 — the original Q2 missed this as a reuse candidate). The codebase's standing scale assumption for a large collection, used repeatedly for budgeting incremental work, is **100,000 files** (e.g. "reconcile 100,000 unchanged files in under 2 seconds", MV-4). | `pkg/records/view.go::LoadViews` (single `os.ReadDir`); `pkg/records/invalidate.go::SnapshotSchemas`; `pkg/knowledge/manifest.go` (100,000-file budget comment, existing file-list index) | Verified |
| F13 | **New in round 1 (OBS-001).** `SavedView` already carries `SourcePath` — the absolute path a view was loaded from, set once in `ParseView` and used today by `knowledge_configure`'s write-result `Paths` field. The original spec's §4 step 4 proposed adding a new `Path` field for exactly this purpose; `SourcePath` is reused instead (made collection-relative at the API edge), avoiding a duplicate concept. | `pkg/records/view.go::SavedView.SourcePath` (field ~line 251; set ~line 783) | Verified |
| F14 | **New in round 1 (MIN-007).** The Library content-save door and every `knowledge_configure` control-plane write already resolve to the intended-to-be-SAME lock: `controlPlaneLockKey(root, abs)` is `relControlPlanePath(root, abs)` fed to `WithNoteWriteLock`; `resolveLibraryLock`/`resolveCollectionNoteLock` derive the Library save door's lock the same way (same `CollectionRoot`, same `LockDir`, same collection-relative path) specifically "so the save door and the rename/delete door can never drift apart" (that function's own doc comment). A Library edit of a `.view` file and an agent's `write_view` on the same file already take the same lock PROVIDED both resolve to the identical collection-relative path string for that file — a claim this spec must state explicitly and test (it was previously unstated). | `pkg/knowledge/knowledge_configure.go::controlPlaneLockKey`; `pkg/gateway/rest_library_write.go::resolveLibraryLock, resolveCollectionNoteLock`; `pkg/knowledge/version.go::WithNoteWriteLock` | Verified |
| F15 | **New in round 1 (MAJ-001, corrects the original F1/F4/§5 "one-shot importer" characterization).** `pkg/vaultimport/rederive.go` is a live writer, not a one-shot importer: its own header states it is "called by the Library save door after the bytes land" on every `.base` save, and it DELETES view files its source no longer declares. It resolves its write/delete destination through `resolveViewWritePath` (a containment check) before touching a file. The ORIGINAL one-shot importer, `pkg/vaultimport/run.go::writeAndReloadViews`, has no equivalent check — it writes with a bare `fileutil.WriteFileAtomic` (MIN-005): the two writers behind the "same" feature are inconsistent today, before this spec's changes are even applied. | `pkg/vaultimport/rederive.go` (header comment; `resolveViewWritePath`); `pkg/vaultimport/run.go::writeAndReloadViews` | Verified |
| F16 | **New in round 1 (MAJ-010).** The Library content-save door (`handleLibraryContentPut`) never refuses a write because of what the saved content MEANS — a `.base` save always lands on disk, then `rederiveBaseViewsAfterSave` runs best-effort and only logs on failure ("NEVER a refusal: the save is already on disk"); a markdown save always lands, then its indexes best-effort refresh the same way. This is a deliberate, pre-existing posture for this specific door, not an oversight this spec introduces. | `pkg/gateway/rest_library_write.go::handleLibraryContentPut, rederiveBaseViewsAfterSave, refreshLibraryWriteIndexes` (doc comments) | Verified |
| F17 | **New in round 1 (MAJ-011).** `scanSkippedDirNames`'s own doc comment records an explicit, pre-existing design choice already in force for every OTHER kind of content: "ordinary dotfiles are NOT skipped... `.hidden.md` [is] indexed (it is merely hidden in the explorer)." Agents already see content inside a dot-prefixed folder (other than the four permanently-skipped control-plane names) that the Library hides from a human browsing the tree — today, for notes. The same rule (`WalkContained` never follows a symlink, its own FR-044 comment) already makes a symlinked "mount" folder's contents invisible to every collection walk. Both are pre-existing project behavior that a view now inherits by walking the same primitive — not a new gap this spec creates (MAJ-011's two bullets are parity findings, not regressions). | `pkg/knowledge/scan.go::scanSkippedDirNames` (doc comment); `pkg/knowledge/contain.go::WalkContained` (FR-044 comment) | Verified |
| F18 | **New in round 1 (MAJ-002).** Agents have no Library move/rename door for a non-markdown file today: `knowledge_restructure`'s rename/move op runs `ensureMarkdown` on both `from` and `to` unless the caller passes `folder: true`, appending `.md` to anything not already `.md`/`.markdown` — so a call meaning "move `roadmap.view`" targets `roadmap.view.md`, the wrong file, silently. Trash already gets this right: `(*Trasher).trashSourcePath` keeps an existing regular non-markdown path unchanged, exact-path-first. | `pkg/knowledge/knowledge_restructure.go::execRenameMove`; `pkg/knowledge/authoring_tools.go::ensureMarkdown`; `pkg/knowledge/knowledge_restructure_trash.go::(*Trasher).trashSourcePath` | Verified |

### 1a. GitNexus availability

GitNexus MCP tools (`query`, `context`, `impact`, `trace`, `explain`) were **not available** in
this session/worktree (checked via tool search; none resolved), including for round 1's fixes.
Per `omnipus-shared-rules` rule 9's fallback, every call-site inventory in this document (F4,
F13–F18) is a Grep/Read sweep, labeled Inferred for completeness — a rename or an indirection
(e.g. a wrapper that calls `ViewsDir` without the literal substring `ViewsDir(`) would not show
up in a literal grep. The implementing lead must re-run these sweeps (or GitNexus
`impact({target: "ViewsDir", direction: "upstream"})` / `impact({target: "LoadViews", ...})` if
the target checkout has it indexed) before deleting or changing the signature of either symbol,
per SC-VA-002.

---

## 2. Firm decisions (not founder questions)

These are decided here, with the evidence that makes them low-risk, so the spec below is
internally consistent. Team-lead/backend-lead may still revisit any of them; each cites why
it did not need to go to the founder.

- **D-DEDUP — Reuse the existing name/label dedup mechanism, now downstream of D-DUPLICATE's
  auto-rename step.** F7 shows `ViewDef.name` is *already* required to be globally unique across
  the vault, and `Resolve`/`ViewAmbiguousLabel` already handle label collisions. "Anywhere"
  storage does not introduce a new collision model — it only widens `loadViewPaths`'s input from
  "one directory's file list" to "the tree-walk's file list" (§4). `RejectViewDuplicateName` is
  reused, not reinvented, and now runs AFTER FD-2's auto-rename step has already resolved every
  collision Omnipus itself created (D-DUPLICATE) — it still fires, unchanged, for every collision
  that arrives by any other path.
- **D-ICON — The per-kind icon (founder: "table, calendar, chart, …") keys off `ViewDef.kind`**
  (F8's 8-value enum: table/list/tiles/board/calendar/summary/trend/breakdown), not `layout` and
  not the render-time `part` vocabulary. `kind` is the only one of the three that is a single,
  definitional, top-level property of a saved view — the thing "what sort of view is this" asks
  about — and it is what `create_view` already writes. A `.view` file with no `kind` (legal under
  the schema, since only `name` is required — F8) falls back to a generic "view" icon (Edge Case
  EC-3).
- **D-SCOPE (new in round 1, resolves MAJ-005 per founder FD-1) — a view is discoverable, and
  is only classified `is_view` on the wire, inside a knowledge base.** A view can only be
  evaluated against a collection (it queries that collection's records); the Library also shows
  plain workspace folders that are not knowledge bases. Per FD-1: `is_view`/`view_kind`/
  `view_label`/`view_name`/`view_rejection`/`view_derived_from` (D-CONTRACT) are computed for a
  `LibraryEntry` **only** when that entry's collection-relative path falls inside an enclosing
  knowledge base — reusing the existing enclosing-collection lookup the Library write path
  already has (`resolveCollectionNoteLock`'s `enclosingCollectionRel`, F14's neighbor), not a new
  lookup. Outside a knowledge base, a `.view`-extension file is an ordinary file: every one of
  those fields is simply absent, exactly as if the extension were unrecognized. This is also why
  agent-tool discovery (§4) never needed a founder answer here: `knowledge_describe`/
  `knowledge_find`/`knowledge_configure` were already collection-scoped by construction (F3),
  so a `.view` outside every knowledge base was already outside their reach before this spec, and
  stays that way — FD-1 resolves the *Library/UI* classification ambiguity only.
- **D-WALK — Discovery is a dedicated `.view`-extension pass over `WalkContained`'s file list**
  (F10), not folded into `BuildLinkGraph`'s walk (which only opens `.md` files and is scoped to
  the link-graph build, not a general-purpose file index). `WalkContained` itself is reused
  as-is: no new symlink or containment logic is written for this feature (Hard Constraint #6's
  security posture: do not reinvent a containment check that already exists and is tested). The
  direction of the call is corrected in round 1 — see §4 and MAJ-008's disposition; the primitive
  lives, and stays, in `pkg/knowledge`.
- **D-PARITY (new in round 1, resolves MAJ-011 per F17) — the existing dot-folder/mount asymmetry
  is accepted as-is, not changed, and is made explicit rather than left implicit.** A view placed
  inside a dot-prefixed folder other than the four permanently-skipped control-plane names (e.g.
  `.drafts/x.view`) is discoverable to agents but hidden from a human browsing the Library tree —
  exactly the existing, documented rule for `.hidden.md` (F17). A view placed inside a mounted
  (symlinked) folder is visible in the Library tree but invisible to every collection walk,
  views included — exactly the existing, documented no-symlink-follow rule (F10/F17). Both are
  now covered by explicit Dataset A rows (§11) instead of being silently true; MAJ-011 is
  resolved by citation to pre-existing behavior, not by inventing a new skip rule that would
  change what every OTHER kind of content does too.
- **D-SEC — Writes go through the existing containment/pathsafe layers, not new ones**, now
  covering three concrete destinations instead of one hypothetical argument (D-WRITE-IDENTITY
  below fixes CRIT-001's gap in what "destination" even means). Any caller-supplied or
  system-computed destination path is resolved with `CollectionRoot.ResolveContainedNoSymlink`
  before any file is touched, and the final filename is validated with
  `pathsafe.RuleSet.ValidateComponent`/`ValidateNameShape` before write. This is the *reason* the
  attack surface changes at all (F11) — flagged for `security-lead` review before landing (§9).
  `pkg/vaultimport/run.go::writeAndReloadViews` (F15/MIN-005) is brought up to the same check
  `rederive.go::resolveViewWritePath` already runs — a consistency fix, not a new mechanism.
- **D-SYMLINK-READ (new in round 1, resolves MIN-001) — the discovery re-check and the read
  itself both refuse a symlink, not just the resolve.** §4 step 1c is corrected to call
  `CollectionRoot.ResolveContainedNoSymlink` (not the symlink-permitting `ResolveContained`) for
  its second belt-and-braces check, AND the subsequent file read refuses to follow a symlink
  swapped in between the walk and the read (an `Lstat`-then-regular-file check immediately before
  `os.ReadFile`, refusing if the target is no longer a plain file) — closing the TOCTOU window
  MIN-001 identified, where a file the walk found as a regular file could be replaced with a
  symlink before its bytes are read and its content potentially echoed into a rejection `Reason`.
- **D-SIZECAP (new in round 1, resolves MIN-002) — a `.view` file has a maximum size.** Any file
  matching the `.view` extension, anywhere discovery or a directory listing would otherwise read
  it, is capped at **256 KiB** — three orders of magnitude above any real view's expected size
  (a filter tree plus metadata, F1), small enough that even a large collection's total `.view`
  read cost stays bounded. A file over the cap is reported `is_view: true` with a new
  `view_too_large` rejection code (added to the existing `RejectView*` family, F1's rejection-code
  channel) and is never read into memory past the cap. Implementing lead may tune the exact
  number at RED time against a real fixture; 256 KiB is this spec's floor, not an invented-and-
  forgotten constant — it must ship as a named constant, not a literal, so it is one place to
  change.
- **D-DELETE (new in round 1, resolves MAJ-004) — `delete_view` goes through the same Trasher
  every other agent-facing Library deletion uses**, because a view is now "a file like a note"
  (founder framing) and a hand-authored view sitting in a person's own folder deserves the same
  undo path as any other note they might ask an agent to remove. `execDeleteView`'s bare
  `os.Remove` (F5a) is replaced with the same `(*Trasher).Trash` call `knowledge_restructure`'s
  own delete op already uses.
- **D-MOVE (new in round 1, resolves MAJ-002) — `knowledge_restructure`'s rename/move accepts a
  `.view` path unchanged**, the same exact-path-first rule `(*Trasher).trashSourcePath` (F18)
  already applies for trash: when `from` (or `to`) is an existing regular file whose extension is
  not `.md`/`.markdown`, `ensureMarkdown` is skipped for that path, matching trash's existing
  carve-out rather than inventing a second one. This gives agents the Library move/rename door
  Q3/B's "moved and renamed like a note" promise otherwise has no agent-facing mechanism for
  (F18).
- **D-VALIDATE (new in round 1, resolves MAJ-010) — a Library save of `.view` content follows the
  same never-refuse posture the save door already uses for `.base` and markdown (F16), not a new
  refusing exception.** The write always lands; `ParseView`/`ValidateViewAgainstSchemas` run
  as a best-effort post-write check exactly like `rederiveBaseViewsAfterSave` runs for `.base`,
  and the resulting state (parses cleanly / rejected, and why) is what D-CONTRACT's new
  `view_rejection` field on `LibraryEntry` reports at the next listing — the SAME rejection-code
  channel discovery already uses (F1), reused rather than inventing a second one. This is also
  where CRIT-002's visible-warning requirement (FD-2) surfaces in the tree: a duplicate-rejected
  view's `view_rejection` is `view_duplicate_name`, shown as the tree badge and preview banner.
- **D-WRITE-IDENTITY (new in round 1, resolves CRIT-001) — `write_view` locates the file it is
  upserting by the target view's OWN discovered location, never by reconstructing a path from
  the name.** Concretely:
  - `write_view name=X` first asks the current `ViewSet` (already loaded for the dedup check,
    D-DEDUP) whether a view named `X` exists. If it does, the write targets that view's
    `SourcePath` (F13) — wherever it currently lives — and REPLACES that file. It never creates a
    second file for an existing name.
  - If no view named `X` exists yet, the write is a create: the default destination is
    `<collection root>/X.view` (Q5/B, unchanged). If a filesystem entry already exists at that
    exact path — whatever it contains, view or not — the write is REFUSED, naming the conflicting
    path, rather than silently overwriting an unrelated file. `create_view` follows the identical
    rule for its own name.
  - This closes both of CRIT-001's failure cases: a rename that frees up `<name>.view` for reuse
    by an unrelated file can no longer be silently clobbered by a same-named `write_view` (case
    1), and upserting a view that has moved elsewhere in the tree can no longer create a second,
    colliding file that gets both rejected under D-DEDUP (case 2).
- **D-DUPLICATE (new in round 1, implements founder FD-2, resolves CRIT-002) — Omnipus-mediated
  copies are auto-renamed before D-DEDUP ever sees them; anything else still goes through
  D-DEDUP, now with a visible warning.** "Made through Omnipus" means the Library's own copy,
  trash-restore, and upload write paths (`handleLibraryTransfer` in copy mode, `(*Trasher).Restore`,
  `handleLibraryUpload`) — every one of them already the single choke point for its operation
  (no other code path produces a Library copy/restore/upload). When the file being written by one
  of these three operations is `.view`-extension AND its `Def.Name` would collide with a `name`
  already present in the destination collection's `ViewSet`, that operation rewrites the copy's
  `name:` (and, where present, `label:`) inside the YAML content to a unique suffixed value
  (the same numbering pattern the Library's own filename-collision suffixing already uses, e.g.
  `-2`, `-3`, …) BEFORE the file lands — the person doing the copy/restore/upload never sees a
  collision, and the two views are independently addressable afterward. Any collision that does
  NOT arrive through one of these three operations (a hand-authored second file typed directly
  into the Library editor, an external sync client's conflict copy, a `bash`-written file) is
  unaffected by this rule and falls through to D-DEDUP exactly as today — both switched off — but
  now visibly: D-VALIDATE's `view_rejection` field carries `view_duplicate_name` for both files
  (tree badge, preview banner), and `knowledge_describe`/`knowledge_find` name both colliding
  paths in their output rather than one view simply going missing (OBS-003's path-exposure fix
  makes this possible).
- **D-PROVENANCE (new in round 1, implements founder FD-3, resolves MAJ-001) — a new persisted
  field distinguishes a `.base`-derived view from a hand-made one, and re-derivation locates its
  managed views by that field, never by path.** `ViewDef` (contract, Hard Constraint #8) gains an
  optional field, `derived_from: string` — the collection-relative path of the `.base` file
  currently managing this view. It is:
  - set ONLY by the `.base` import pipeline (`run.go`) and the per-save re-derivation pipeline
    (`rederive.go`, F15) when they write a view — never by `create_view`/`write_view`, which
    MUST refuse any caller-supplied `derived_from` value (a hand-made view never has this field);
  - the SOLE source of truth for "is this view derived" — never inferred from the pre-existing
    `source` field (F1's "never re-read" invariant would be broken by reusing it, and a hand-made
    view may legitimately set `source` descriptively without becoming derived, per FD-3);
  - what re-derivation now matches on to locate a `.base`'s currently-managed views (via the same
    discovery walk, §4 step 6) instead of reconstructing a fixed slug path — this is what lets a
    person move a derived view elsewhere in the tree and have re-derivation still find, and still
    correctly overwrite or delete, that exact file (fixing MAJ-001's "moved file gets a second
    copy or gets deleted" failure), consistent with FD-3's "`.base` wins even after a move."
  A view with `derived_from` set is what the UI badges "derived" and what the preview/tool output
  name the source `.base` for (FD-3). A view with `derived_from` absent is hand-made and is never
  touched, read, or reasoned about by the re-derivation pipeline, unconditionally.
- **D-Q4-NO-WARN (new in round 1, resolves MAJ-006 — retires the original spec's own §3 Q4)** —
  firm decision, option A: no startup diagnostic. The original spec's recommended option B (a
  one-time WARN naming a non-empty `.omnipus-vault/views/`) is upgrade-only code the 2026-09-15
  greenfield ruling forbids, and every existing dev knowledge base already has that directory
  (both KB-creation writers `MkdirAll` it, F4) — so the WARN would fire on every existing
  checkout, which is not a rare-edge-case diagnostic, it is noise. FR-VA-017, SC-VA-007, TDD test
  18, Dataset E, and Holdout 7 are deleted. The one REQUIRED action remains: the implementing lead
  greps the e2e/fixture tree for `.omnipus-vault/views/` and relocates any hits as an explicit
  GREEN task (qa-lead checks for this at CHECK) — this was already required and is unaffected.
- **D-ADDRESS (new in round 1, resolves MAJ-003) — the Library preview addresses a view by name
  plus its enclosing collection, both carried on `LibraryEntry`, reusing the existing evaluation
  endpoint rather than adding a new one.** `LibraryEntry` gains `view_name` (D-CONTRACT) — the
  view's authoritative `Def.Name` (F7), present exactly when the file both is inside a knowledge
  base (D-SCOPE) and parses successfully; absent for a malformed or duplicate-rejected file
  (which has no loadable name — `view_rejection` carries why, instead). The SPA already resolves
  the nearest enclosing knowledge base for a `.base` entry today (the same walk `BasePreview`
  relies on to open its own tabs, F6) — reused for a `.view` entry the same way, rather than
  adding a `collection_id` field nothing else on `LibraryEntry` needs. The preview then calls the
  EXISTING `GET .../knowledge/view` endpoint with that collection and `view_name` — no new
  contract endpoint, per Hard Constraint #8's "generated types only" but also its spirit of not
  growing the wire surface when an existing door already does the job. The implementing lead must
  confirm the frontend's enclosing-KB resolution for `.base` is reachable from the Library tree's
  `.view` click path too (GREEN task) — flagged Inferred here because it was not traced end to
  end in this worktree.
- **D-CONTRACT — `LibraryEntry` gains six optional fields**, modeled directly on the existing
  `is_knowledge_base` pattern (F2's neighbor field: computed once per directory entry during
  listing, absent when not applicable, optional on the wire so old SPA builds/fixtures keep
  working), and computed only per D-SCOPE (inside a knowledge base):
  - `is_view: boolean` — true when the extension classifies as a view (Q1 below decides the
    extension) AND the entry is inside a knowledge base (D-SCOPE, FD-1).
  - `view_kind: string` — one of F8's 8 `ViewDefKind` values, absent when the file's `kind` is
    unset or the file fails to parse (Edge Case EC-3).
  - `view_label: string` — the view's `DisplayLabel()` (F7: `Label` if set, else `name`), so the
    tree can show the human label without a second round trip.
  - `view_name: string` (**new in round 1, D-ADDRESS**) — the view's authoritative `Def.Name`,
    absent when the file fails to parse.
  - `view_rejection: string` (**new in round 1, D-VALIDATE/CRIT-002**) — one of the existing
    `RejectView*` codes (reusing F1's rejection-code type, never a hand-written parallel one, Hard
    Constraint #8), present exactly when `is_view` is true but the file is broken, oversize
    (D-SIZECAP), or duplicate-rejected (D-DEDUP/D-DUPLICATE); absent for a healthy view.
  - `view_derived_from: string` (**new in round 1, D-PROVENANCE/FD-3**) — the collection-relative
    path of the `.base` file currently managing this view, present exactly when `ViewDef.derived_from`
    is set; absent for a hand-made view.

---

## 3. Questions for the founder

**Note on numbering (round 1):** these are THIS spec's own Q1–Q5, about extension, discovery
performance, filename/identity coupling, the (now-dropped) orphan directory, and default
placement. They are a different, earlier question set from the grill review's Q1–Q4, which the
founder answered directly as FD-1–FD-4 above. Q4 below is retired in favor of D-Q4-NO-WARN
(§2) — kept here, struck through in effect, only so the historical record of what was asked and
why is not silently deleted.

Five open decisions, each with a recommendation the spec assumes below so the rest of the
document is buildable without stalling on an answer. Answer as **"Q1 A, Q2 B, …"**.

### Q1 — File extension for a view file

**Context and impact**: F9 shows schema files and (today's) view files share the plain `.yaml`
extension, distinguished only by directory. Moving views into the ordinary tree means `.yaml`
alone can no longer mean "this is a view" — a user's unrelated `.yaml` note would collide.
This decides what `is_view` (D-CONTRACT) and the discovery walk (§4) key off.

| Option | Description |
|---|---|
| **A (recommended)** | A new dedicated single extension, `.view` (content stays YAML underneath, same `application/x-yaml` mime treatment `.base` already gets — F2). Direct sibling of the already-accepted `.base` precedent; a one-glob discovery filter. |
| B | Compound extension `.view.yaml` — keeps `.yaml` visible for editors/git, but doubles every filename and is a novel pattern (`.base` is not `.base.yaml`). |
| C | Keep plain `.yaml`, add an internal discriminator (e.g. a `kind: omnipus/view` marker key) and classify by content-sniff. Ambiguous with any other `.yaml` a user drops in the tree; requires opening every `.yaml` file in the collection to classify it — the worst case for §4's performance question. |

**Recommendation**: A.

### Q2 — Discovery performance bound and caching

**Context and impact**: F12 — there is no existing view cache; `LoadViews` today is a cheap
single-directory list. Moving to whole-collection discovery via `WalkContained` (D-WALK) changes
the cost from O(views) to O(files in collection), and D-ICON requires opening and parsing each
discovered `.view` file's YAML (small files, but still a read+parse per file) to get its `kind`
for the tree icon. This is called on every `knowledge_describe`, `knowledge_find`, and Library
listing request that touches views.

| Option | Description |
|---|---|
| A | No cache; require a benchmark-verified bound calibrated to the repo's standing 100,000-file scale assumption (F12) before landing — e.g. "discovering and kind-tagging every `.view` file in a 100,000-file collection completes in under N seconds," N to be set from an actual benchmark, not invented here. |
| B | Build a persistent, invalidation-tracked view index modeled on `SnapshotSchemas`'s content-hash pattern (F12), so repeat calls in one session don't re-walk. More work, more surface, no existing infra to build on yet. |
| **C (recommended)** | Ship the uncached O(collection) walk for this issue, with the benchmark from option A as a landing gate (not a made-up SLO number), and defer a persistent index to the v0.3 Workspaces redesign — where a general-purpose collection index is a more natural fit than a views-only cache. Since Q4 (grill FD-4) now routes THIS feature to v0.1.1, "defer to v0.3" means defer to a later, separate change, not to this feature's own release. |

**Recommendation**: C, with A's benchmark gate kept as a hard requirement regardless of which
option is picked (SC-VA-004 below is written to hold under either A or C).

**Corrected in round 1 (MIN-003):**
- The option list missed an existing reuse candidate: `pkg/knowledge/manifest.go` already tracks
  every file in a collection, with size and modification time, at the same 100,000-file scale
  (F12). A future persistent view index (option B, or its v0.3 successor) should be evaluated
  against reusing that file list before adding a second one — noted here as a design input for
  whoever builds it, not resolved by this spec.
- SC-VA-004 as originally written ("completes within the number the benchmark itself
  establishes") could not fail — any measured number would satisfy it. It is rewritten below
  (§13) to require an ABSOLUTE upper bound fixed before landing (not invented in this document —
  set from a pre-landing measurement, but as a number a CI benchmark can then fail against).
- A single `write_view`/`create_view` call already invokes `LoadViews` two to five times in
  today's code (F4's reader inventory: the name-refusal check, the dedup check, and — for a
  schema edit — a before/after pair). Each becomes a full collection walk once §4 lands. This
  spec requires the walk be performed AT MOST ONCE per tool invocation (FR-VA-018a below) — the
  existing multiple-call sites are refactored to share one discovery result, not benchmarked
  as-is and then multiplied.

### Q3 — Does a view's identity stay bound to its filename, or become independent (a true "file like a note")?

**Context and impact**: Today `viewPath := filepath.Join(ViewsDir(root), viewName+ext)` — the
filename stem IS `Def.Name` by construction (F4's write sites). A note has no such coupling: a
human renames or moves a note freely and its content is untouched. Founder direction says a view
must be stored "anywhere where it makes sense... like a note" — which implies free rename/move
via ordinary Library file operations, but that conflicts with a filename that is also the
agent-facing identifier (F7's `name`, used to `Resolve` a view).

| Option | Description |
|---|---|
| A | Keep filename == `Def.Name` (today's rule). A Library rename/move of a `.view` file must be intercepted as a "rename this view" operation that also rewrites `name:` inside the file, or is refused outside `knowledge_configure`. Simpler invariant, but not full "just a file" freedom — a view is a file with a special rename rule no other note has. |
| **B (recommended)** | Decouple: `Def.Name` inside the YAML is authoritative regardless of filename. An ordinary Library rename/move just moves the file; discovery re-reads content on every walk (§4) so nothing breaks; `Def.Name` collisions are still rejected by the unchanged dedup mechanism (D-DEDUP) regardless of what either file is called. This is what "a view is a file like a note" literally means — full parity with note rename/move — at the cost that a file's on-disk name is no longer a reliable hint of the view's agent-facing name (mitigated by `view_label`, D-CONTRACT, being shown in the tree). |

**Recommendation**: B — it is the direct, literal reading of the founder's own framing, and a
special-cased rename rule (option A) is exactly the kind of "not really a note" exception the
founder's direction is trying to eliminate.

**Corrected in round 1 (CRIT-001):** B's decoupling was decided for the READ side only — §4
originally never said how an *upsert* (`write_view`/`create_view` on an existing name) locates
the file it should replace once the filename no longer has to match the name. D-WRITE-IDENTITY
(§2) closes that gap: an upsert targets the existing view's discovered `SourcePath` (F13), never
a path reconstructed from the name, so B's decoupling is now consistent on both the read and the
write side.

### Q4 — Fresh-install path and the old hidden-directory writers

**Retired in round 1 — see D-Q4-NO-WARN (§2) and MAJ-006.** The question and its two options are
kept below only as the historical record of what was asked; the founder's decision is D-Q4-NO-WARN
(option A, no diagnostic), not a fresh answer to this question. FR-VA-017, SC-VA-007, TDD test
18, Dataset E, and Holdout 7, which depended on option B, are deleted elsewhere in this document.

**Context and impact**: Greenfield rule (no migrations, no back-compat shims — founder ruling
2026-09-15, "delete superseded code" memory rule). F4's 8 call sites into `ViewsDir` are deleted
outright, not versioned around: a fresh install never had a `.omnipus-vault/views/` directory to
migrate, so there is nothing to migrate on the production path. The risk is narrower and closer
to home: this repo's OWN dev checkouts and e2e fixtures may already plant
`.omnipus-vault/views/*.yaml` files (F10: `WalkContained` permanently skips `.omnipus-vault` at
any depth — by design, because it is the control plane, not content — so any such file would
silently stop being discovered the moment the old reader is deleted).

| Option | Description |
|---|---|
| A | Silent: old-format files under `.omnipus-vault/views/` simply stop being found after the cutover. Matches "no migration code" most literally, but a real operator or CI fixture with leftover files gets a silent empty-result regression — exactly the failure mode FR-024/F1's "silent empty result" language elsewhere in this codebase exists to prevent. |
| **B (recommended)** | Same (no auto-migration, still forbidden), plus a one-time, cheap startup diagnostic: if `.omnipus-vault/views/` exists and is non-empty after the cutover ships, log a WARN naming the directory and the count of files it still holds. No runtime behavior change, no migration — purely a "you have orphaned files, move them" signal, in keeping with "No false success" (never let a real gap look like nothing happened). |

**Recommendation**: B. Either way, the implementing lead (backend-lead) must grep the e2e/fixture
tree for `.omnipus-vault/views/` and move any hits as an explicit GREEN task, not an afterthought
(qa-lead should check for this in CHECK).

### Q5 — Where does a newly created view land by default?

**Context and impact**: `create_view`/`write_view` (F5) take no destination argument today —
the directory was implicit (`ViewsDir`). "Anywhere" storage means something must decide where the
file goes when an agent does not say. This interacts directly with D-SEC (§2): any new
destination argument is exactly the new input D-SEC's containment/pathsafe checks exist to
police.

| Option | Description |
|---|---|
| A | Add a required or optional `folder`/`path` argument to `create_view`/`write_view`; the agent chooses. Most flexible, most tool-surface churn, and pushes a placement decision onto every call site that doesn't care. |
| **B (recommended)** | No new required argument: default to the **collection root**. Since the resulting file is an ordinary Library entry once written (Q3 decouples identity from location), a human or agent can move it afterward with the Library's existing move/rename operation — exactly like moving any other note. `folder` can be added later as an *optional* argument without breaking anything, once there's a concrete need. |
| C | Default to co-locating with content the view queries (e.g., near the `.base` or schema file for its `type`) — heuristic, ambiguous when a view has no `type`, and adds a lookup this feature does not otherwise need. |

**Recommendation**: B — smallest tool-surface change, and it is the option that most directly
relies on "a view is just a file" (Q3/B) rather than inventing new placement logic.

**Corrected in round 1 (CRIT-001, MIN-005):** B still holds for a NEW view's default location.
What was missing is what happens when the default path is already occupied, and what an upsert
of an EXISTING name does instead of using the default at all — both closed by D-WRITE-IDENTITY
(§2): create refuses on an occupied path rather than overwriting it; an existing name's upsert
never consults the default at all, going straight to that view's `SourcePath`.

---

## 4. Discovery mechanism (assumes Q1=A, Q2=C, Q3=B, D-WRITE-IDENTITY, D-PROVENANCE)

**Corrected in round 1 (MAJ-008 — the original version of this section does not compile.)**
The original step 1 had `records.LoadViews` build a `knowledge.CollectionRoot` and call
`knowledge.WalkContained` directly from inside `pkg/records`. That is a Go import cycle:
`pkg/knowledge` already imports `pkg/records` (F-cite: `pkg/knowledge/author.go`,
`pkg/knowledge/authoring_tools.go`, `pkg/knowledge/fields.go`, and 10+ other files import
`"github.com/elicify-ai/omnipus/pkg/records"`), and `pkg/records` itself imports only
`"github.com/elicify-ai/omnipus/pkg/api/generated"` (verified: `pkg/records/view.go`'s import
block) — Go refuses the reverse edge. The dependency direction is inverted below: the walk
primitive and every knowledge-base-shaped concept (collection root, containment, provenance
matching) stay in `pkg/knowledge`, which is legal because `pkg/knowledge` already depends on
`pkg/records`, never the other way. `pkg/records` gains a pure, dependency-free function that
takes an already-discovered list of paths and does only parsing/dedup — exactly what it does
today, minus the directory listing.

0. **Scope gate (only for the Library/UI classification path, D-SCOPE/FD-1) — not part of the
   agent-tool discovery below**, which was already collection-scoped: a `LibraryEntry`'s
   view-related fields (D-CONTRACT) are computed only when that entry's path is inside an
   enclosing knowledge base, reusing the existing enclosing-collection lookup (F14's neighbor,
   `enclosingCollectionRel`). Outside a knowledge base, skip straight to "not a view" for the
   wire — no walk, no parse.
1. **New `pkg/knowledge` discovery helper** (name illustrative — implementing lead's call, e.g.
   `knowledge.DiscoverViewFiles(fsys LinkFS, root CollectionRoot) ([]string, WalkResult, error)`):
   a. Builds/receives a `knowledge.CollectionRoot` for the vault/collection root (existing
      constructor, F10 — already used by 10+ other call sites).
   b. Calls `knowledge.WalkContained(fsys, root)` (F10) — unmodified — and filters
      `WalkResult.Files` to those with the `.view` extension (Q1), case-insensitively (MIN-010:
      `.VIEW` matches, matching the loader's existing extension-lowercasing behavior elsewhere)
      and excluding a file whose base name IS the extension alone (a file literally named
      `.view` is a dotfile — hidden by the Library's own dot-prefix rule, F2 — and is correctly
      NOT discovered, per MIN-010; this is stated behavior, not a bug).
   c. For each surviving path, resolves it with `CollectionRoot.ResolveContainedNoSymlink`
      (**corrected in round 1, MIN-001** — the original text cited the symlink-permitting
      `ResolveContained`) before reading. The read itself refuses to follow a symlink swapped in
      after the walk but before the read: `Lstat` the resolved path immediately before
      `os.ReadFile` and refuse (as `RejectViewUnreadable` or equivalent) if it is no longer a
      plain regular file — closing MIN-001's TOCTOU window, where file content reached through a
      race could otherwise be echoed into a rejection `Reason`.
   d. Enforces D-SIZECAP: a file over 256 KiB is reported `view_too_large` (new rejection code,
      F1's family) and is never read past the cap (MIN-002).
   e. Returns the surviving, size-checked, symlink-checked path list plus the raw `WalkResult`
      (so callers can see `Skipped` entries, step 2 below).
   This helper lives in `pkg/knowledge` and is the ONLY place `WalkContained` is called for
   views — every caller (F4's 13 `LoadViews` sites) is updated to call this first.
2. **`SkipUnreadable` becomes a reported condition, not a silent gap (MAJ-009).** The original
   text never said what happens to a `WalkResult.Skipped` entry of reason `SkipUnreadable`
   (an unreadable subfolder — `WalkContained` records it and continues, it does not fail the
   whole walk, unlike today's `LoadViews` which fails outright on an unreadable `ViewsDir`). Any
   `.view` file that COULD exist under a skipped-unreadable subfolder is, by construction,
   unknown to the walk — so this cannot be "found but unreadable," it is "possibly missed
   entirely." Step 1's helper surfaces every `SkipUnreadable` entry from the walk in its return
   value; every caller that renders a view report (`knowledge_describe`, `knowledge_configure`'s
   write results) MUST include a note naming any such skipped subfolder, so a person or agent
   knows the view count may be incomplete, instead of a directory silently vanishing from view —
   the same "never a silent drop" posture §9 already states for a malformed file, now extended to
   an unreadable directory.
3. **`records.LoadViewPaths` (renamed/repurposed from today's `records.LoadViews`) — pure,
   no `pkg/knowledge` import.** Takes the path list from step 1 (already filtered, contained,
   size-checked) plus the current `SchemaSet`, and does exactly what today's parser does per
   file: JSON round-trip through the generated `ViewDef` type with `DisallowUnknownFields`
   (unchanged, F1), producing a `SavedView` per file or a rejection. `records.LoadViews`'s old
   directory-taking signature is deleted outright (greenfield rule) — every one of F4's 13 call
   sites is updated to call step 1's `pkg/knowledge` helper first, then this function, instead of
   one directory-scanning call. `SavedView.SourcePath` (F13, OBS-001) already carries the
   collection-relative-once-resolved path — no new `Path` field is added, correcting the
   original step 4's proposal.
4. **Name/label dedup, now downstream of D-DUPLICATE's auto-rename.** By the time
   `LoadViewPaths` runs, any collision Omnipus itself created via copy/restore/upload has already
   been resolved by D-DUPLICATE's rename-on-write step (§2) — so the dedup this function runs
   (D-DEDUP, `RejectViewDuplicateName`) is unchanged in mechanism and now only ever fires for a
   collision that arrived some other way (FD-2), which is exactly the case it is still supposed
   to reject.
5. **Provenance-based re-derivation (D-PROVENANCE, FD-3) replaces fixed-path targeting.** When
   `pkg/vaultimport` re-derives a `.base` file's views (`rederive.go`/`run.go`, F15), it no longer
   locates its previously-written views by reconstructing `<viewsDir>/<slug>.yaml` or by
   `filepath.Base` matching (today's mechanism). It instead calls step 1's discovery helper over
   the SAME collection, filters to views whose `ViewDef.derived_from` equals this `.base` file's
   own collection-relative path, and reconciles against the newly-translated set: a still-declared
   view is rewritten in place at its CURRENT location (wherever it was moved to — FD-3, ".base
   wins" survives a move); a no-longer-declared view is deleted from its current location; a
   newly-declared view is written beside the `.base` file (US-2, unchanged) with `derived_from`
   set to the `.base`'s own path. `os.Remove`'s `os.ErrNotExist` is never counted as a successful
   deletion in the report (MIN-009 — the original code appends to `Deleted` even when nothing was
   removed).
6. **Write-path identity for `write_view`/`create_view` (D-WRITE-IDENTITY, CRIT-001).** An
   upsert of an existing `name` targets that view's `SourcePath` from the current `ViewSet`
   (loaded via steps 1–3), never a name-reconstructed path; a create whose default path
   (`<collection root>/<name>.view`, Q5/B) is already occupied is refused, naming the conflict,
   rather than overwritten. Neither tool may set or clear `derived_from` on any write — a caller-
   supplied value for that field is refused (D-PROVENANCE's integrity rule).

---

## 5. Existing Codebase Context

### Symbols involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/records/view.go::ViewsDir` | Deleted (Q4) | Replaced by the walk in §4; F4 lists every one of the 9 call sites / 7 files that must change. |
| `pkg/records/view.go::LoadViews` | Deleted, replaced by `records.LoadViewPaths` (**corrected in round 1, MAJ-008/MIN-006** — `LoadAll` never existed) | Reader entry point; changes from directory list to a caller-supplied, already-walked path list (§4 step 3), fixing the import-cycle the original design had. 13 call sites across 10 files (F4) all change their call shape to a two-step form. |
| `pkg/knowledge/contain.go::CollectionRoot`, `WalkContained` | Reused, unmodified | Discovery's containment and walk primitive (F10, D-WALK); now called from a NEW helper inside `pkg/knowledge` itself (§4 step 1), never from `pkg/records`. |
| `pkg/knowledge/scan.go::scanSkippedDirNames` | Reused, unmodified, newly cited (F17) | Basis for D-PARITY's dot-folder/mount resolution of MAJ-011. |
| `pkg/pathsafe/*` | Reused, unmodified | Write-path filename/component validation (F11, D-SEC). |
| `pkg/knowledge/knowledge_configure.go::opWriteView, opCreateView, opDeleteView` | Modified | Writers stop hard-coding `ViewsDir`; `write_view`/`create_view` take D-WRITE-IDENTITY's SourcePath-or-refuse logic (Q5/D-SEC/CRIT-001); `opDeleteView` (F5a, MAJ-004) moves to `(*Trasher).Trash` (D-DELETE). |
| `pkg/knowledge/knowledge_restructure.go::execRenameMove`, `pkg/knowledge/authoring_tools.go::ensureMarkdown` | Modified (**new in round 1, MAJ-002**) | Rename/move accepts a `.view` path unchanged, mirroring `(*Trasher).trashSourcePath`'s existing exact-path-first rule (D-MOVE, F18). |
| `pkg/knowledge/view_kinds.go::ViewKindTable`…`ViewKindBreakdown` | Reused, unmodified | Source of the 8-value icon vocabulary (D-ICON). |
| `pkg/vaultimport/run.go::writeAndReloadViews`, `pkg/vaultimport/rederive.go` | Modified | View-writing call sites (F4/F15); `run.go` gains the containment check `rederive.go` already has (MIN-005); both locate managed views by `derived_from` through discovery, not by fixed path or slug (D-PROVENANCE, §4 step 5), fixing MAJ-001; `rederive.go`'s `Deleted` report only counts an actual removal (MIN-009). |
| `pkg/gateway/rest_library_write.go::handleLibraryTransfer` (copy mode), `(*Trasher).Restore`, `handleLibraryUpload` | Modified (**new in round 1, D-DUPLICATE/FD-2**) | Each gains a `.view`-specific auto-rename-on-collision step before the file lands (CRIT-002). |
| `pkg/gateway/rest_library_write.go::handleLibraryContentPut` | Modified (**new in round 1, D-VALIDATE/MAJ-010**) | Gains a post-write `ParseView`/`ValidateViewAgainstSchemas` best-effort check for a `.view` save, mirroring its existing `.base`/markdown never-refuse posture (F16), feeding `view_rejection` (D-CONTRACT). |
| `src/components/library/preview/libraryPreviewKind.ts::classifyLibraryEntry` | Modified | Gains a `'view'` kind, extension-classified exactly like `'base'` (F2, F6), gated on `is_view` (which is itself gated on D-SCOPE/FD-1 — a `.view` outside a knowledge base never classifies as `'view'`). |
| `src/components/library/LibraryPreviewPane.tsx::renderBody` | Modified | Gains a `case 'view':` mounting a new preview component that reuses `ViewPartsRenderer` (F6), addressed via `view_name` + the enclosing knowledge base (D-ADDRESS, MAJ-003), showing a derived-view banner when `view_derived_from` is present (FD-3) and a duplicate/broken banner when `view_rejection` is present (CRIT-002/MAJ-010). |
| `contracts/openapi.yaml::ViewDef` | Modified (**new in round 1, D-PROVENANCE**) | Gains `derived_from` (optional string). |
| `contracts/components/schemas/LibraryEntry.yaml` | Modified | Six new optional fields, not three (D-CONTRACT, expanded in round 1 for MAJ-003/CRIT-002/MAJ-010/FD-3). |
| `contracts/components/schemas/KnowledgeBaseView.yaml`, `KnowledgeCollectionViews.yaml`, `KnowledgeBaseUnloadableView.yaml` | Modified (**new in round 1, MIN-004**) | Descriptions naming the old `<vault>/.omnipus-vault/views/<name>.yaml` path are rewritten; `KnowledgeBaseView` gains `path` (OBS-003). |

### Impact assessment

GitNexus is unavailable this session (§1a); impact is Grep/Read-sourced and labeled Inferred.

| Symbol modified | Risk (inferred) | d=1 dependents |
|---|---|---|
| `records.ViewsDir` deletion | HIGH — 9 call sites across 7 files (F4, corrected in round 1) | `pkg/records/view.go` (`LoadViews` itself), `knowledge_base_create.go`, `knowledge_configure.go` (×2), `knowledge_configure_create_view.go` (×2), `vaultimport/run.go`, `vaultimport/rederive.go`, `rest_library_write.go` |
| `records.LoadViews` deletion (replaced by `records.LoadViewPaths` + a new `pkg/knowledge` walk helper) | HIGH — 13 call sites across 10 files (F4, corrected in round 1), every one changing call SHAPE, not just an argument | `knowledge_edit.go`, `knowledge_configure.go` (×5), `knowledge_configure_create_view.go`, `vaultprops/find_env.go`, `vaultimport/run.go`, `vaultimport/rederive.go`, `rest_knowledge_views.go`, `rest_knowledge_base_views.go`, and everything downstream of those (`knowledge_describe`, `knowledge_find`'s `ViewFindLoader`, F5) |
| `knowledge_configure.go::execDeleteView` (moves to `(*Trasher).Trash`) | MEDIUM — one call site, but changes the caller-visible outcome (trashed, not hard-deleted) — new in round 1, MAJ-004 | Any agent skill/prompt text asserting `delete_view` is permanent must be corrected (route to `prometheus-prompt-engineer`) |
| `pkg/knowledge/authoring_tools.go::ensureMarkdown` | LOW — one new carve-out (a non-markdown, non-folder existing-file target) added to an existing conditional — new in round 1, MAJ-002 | `knowledge_restructure.go::execRenameMove` only; trash's own carve-out (`trashSourcePath`) is unaffected, used only as a precedent |

Both HIGH rows are flagged per `omnipus-shared-rules` rule 9 — the implementing lead must re-run
`impact({target: "ViewsDir", direction: "upstream"})` and `impact({target: "LoadViews",
direction: "upstream"})` (or an equivalent Grep/Read sweep, repeated after any refactor) before
deleting either symbol, and must not proceed past a HIGH/CRITICAL result without a plan for every
dependent listed.

### Cluster placement

This feature spans two areas GitNexus would likely call separate clusters: the knowledge-base
control plane (`pkg/records`, `pkg/knowledge`, `pkg/vaultimport`) and the Library/Preview frontend
surface (`src/components/library`). Both must land together — a backend-only or frontend-only
half leaves the feature unreachable (§14, Reachability). **Added in round 1:** the Library
write doors (`pkg/gateway/rest_library_write.go` — copy, restore, upload, content-put) are now a
third area this feature touches (D-DUPLICATE, D-VALIDATE) — previously untouched by the original
design, which only changed the agent-tool write paths.

---

## 6. User Stories & Acceptance Criteria

"Server first" (founder direction) is reflected in priority order: backend storage/discovery/
tool-surface ships as P0, the contract fields as P1 (they can only be populated once the backend
walk exists), the UI tree/icon/preview as P2.

### US-1 — A view lives wherever an agent or human puts it (P0)

An agent authoring a view for a `.base` file's collection wants the resulting view stored beside
the content it is about, not exiled to a hidden control-plane directory nobody browses. Today
every view is invisible in the Library (F2) even though the `.base` file that motivated it is
right there. This story makes a view an ordinary Library entry, discoverable anywhere in the
collection (§4), while every agent tool that already knows how to find, use, and write a view
keeps working with zero loss of reach (F5).

**Why this priority**: everything else (icons, preview, contract fields) is inert without this —
"server first."

**Independent test**: with only this story built (no contract/UI changes), `knowledge_describe`'s
VIEWS section and `knowledge_find`'s view-by-name resolution both work identically whether a view
file sits in the collection root, a subfolder, or (transitionally, during development) still under
the old `.omnipus-vault/views/` path before that reader path is deleted.

**Acceptance Scenarios**:
1. **Given** a `.view` file placed by a human directly in a collection subfolder (no tool call
   involved), **When** an agent calls `knowledge_describe` with the views section requested,
   **Then** the view appears in the VIEWS listing exactly as a view under the old fixed directory
   would have.
2. **Given** two `.view` files in different subfolders declaring different `name`s, **When**
   `knowledge_find` is asked to serve one by name, **Then** it resolves the correct one and the
   other is unaffected.
3. **Given** two `.view` files anywhere in the collection declaring the SAME `name`, **When** the
   collection is loaded, **Then** both are rejected as `view_duplicate_name` (D-DEDUP, unchanged
   from today's behavior) — never silently picking one.
4. **Given** `create_view` or `write_view` is called with no destination, **When** the view is
   written, **Then** it lands at the collection root (Q5/B) as an ordinary, non-hidden file.
5. **(New in round 1, CRIT-001, D-WRITE-IDENTITY.) Given** a view named `weekly` already exists
   at `projects/weekly.view`, and a different file (view or not) happens to sit at
   `<collection root>/weekly.view`, **When** `write_view name=weekly` is called with no
   destination, **Then** the write targets `projects/weekly.view` (the existing view's
   `SourcePath`) — the unrelated file at the collection root is never touched, and no second
   `weekly` file is created.
6. **(New in round 1, CRIT-001, D-WRITE-IDENTITY.) Given** no view is named `report` yet, and a
   filesystem entry already exists at `<collection root>/report.view`, **When**
   `create_view name=report` is called with no destination, **Then** the write is refused,
   naming `<collection root>/report.view` as the conflict — the existing entry is never
   overwritten.
7. **(New in round 1, D-SCOPE, FD-1.) Given** a `.view` file sitting in a plain workspace folder
   that is not, and is not inside, any knowledge base, **When** the Library lists that folder,
   **Then** the entry carries no `is_view`/`view_kind`/`view_label`/`view_name` — it renders as
   an ordinary, unrunnable file, exactly as founder decision FD-1 requires.

### US-2 — `.base` import and re-derivation write views next to the file they came from, and re-derivation finds them again wherever they've moved (P0)

An operator imports an existing `.base` file's saved views. Today the imported views vanish into
the hidden directory even though their `source` (F1) already names the `.base` file. This story
keeps that provenance link visually true: the imported view sits next to the file it was imported
from, the same way a person would file it by hand.

**Corrected in round 1 (MAJ-001):** the write pipeline is not one-shot. F15 shows
`pkg/vaultimport/rederive.go` runs on EVERY save of the `.base` file, not once at import time —
and per founder decision FD-3, it is meant to: "`.base` wins," overwriting or deleting a managed
view's file on every re-derivation, for as long as that view stays derived (D-PROVENANCE). This
story now also covers re-derivation finding a MOVED derived view by its persisted `derived_from`
field, not by reconstructing a path — the original design's silent gap (MAJ-001) is what let a
moved derived view either get duplicated or get incorrectly deleted-and-reported (MIN-009).

**Why this priority**: P0 — the importer and the re-derivation pipeline are two of F4's write
sites that must change together with the rest of the write path, or the release story (FD-4, this
ships in v0.1.1) is incoherent.

**Independent test**: run the importer against a `.base` file in a subfolder; the resulting
`.view` file(s) appear in that same subfolder, each with `source` still naming the `.base` file
(F1, unchanged) and `derived_from` newly set to that same path (D-PROVENANCE). Move one of those
files elsewhere in the tree, edit the `.base` file, save it; confirm re-derivation still finds and
correctly rewrites or deletes that moved file, using `derived_from`, not its (now different) path.

**Acceptance Scenarios**:
1. **Given** a `.base` file at `projects/roadmap.base` declaring one view, **When** the importer
   runs, **Then** the resulting `.view` file is written under `projects/`, not the collection root
   and not `.omnipus-vault/views/`, with `derived_from: projects/roadmap.base` (D-PROVENANCE).
2. **Given** an import whose target name collides with an existing view anywhere in the collection
   (D-DEDUP), **When** the importer runs, **Then** the conflicting view is rejected exactly as
   `write_view`/`create_view` would reject it (same `RejectViewDuplicateName` code, F7).
3. **(New in round 1, MAJ-001, D-PROVENANCE.) Given** a derived view has been moved by a person
   from `projects/roadmap-open.view` to `archive/roadmap-open.view` (Q3/B's free-move promise,
   unaffected by FD-3), **When** `projects/roadmap.base` is saved again with that view's
   definition unchanged, **Then** re-derivation locates the view at `archive/roadmap-open.view`
   via its `derived_from` field and rewrites it in place — it does NOT write a second copy beside
   the `.base` file, and does NOT delete the moved file.
4. **(New in round 1, MAJ-001/FD-3.) Given** the same moved view, **When** `roadmap.base` is
   saved with that view's definition REMOVED, **Then** re-derivation deletes
   `archive/roadmap-open.view` (FD-3: `.base` wins, even after a move) and reports it deleted.
5. **(New in round 1, MIN-009.) Given** a managed view's file was already removed by some other
   means before re-derivation runs (e.g. a person deleted it by hand, or `os.Remove` races),
   **When** re-derivation processes that source, **Then** it does NOT report that file as
   "deleted" in its result — `Deleted` only ever names a file re-derivation itself actually
   removed in this run.
6. **(New in round 1, FD-3.) Given** a view with `derived_from` set, **When** a person opens it in
   the Library preview or an agent calls `knowledge_describe`/`knowledge_find`/`knowledge_configure`
   on it, **Then** every one of those surfaces states that the view is derived and names its
   source `.base` file — never silently presenting it as an ordinary, independently-owned file.
7. **(New in round 1, FD-3.) Given** a HAND-MADE view (no `derived_from`, never written by the
   import/re-derivation pipeline) that happens to set a free-text `source` value pointing at a
   `.base` file, for its own descriptive reasons, **When** that `.base` file is saved, **Then**
   the hand-made view is completely untouched — `source` is never read as a re-derivation trigger
   (F1's "never re-read" invariant, D-PROVENANCE).

### US-3 — Writes are contained and validated, wherever they land (P0)

A security reviewer needs assurance that "anywhere" does not mean "anywhere on disk." Today's
writer only ever joins a fixed, trusted directory (F11) — there was no caller-supplied path to
attack. This story is the one that introduces a caller-influenced destination (Q5/B: still
system-chosen by default, but the *mechanism* that resolves "collection root" must be the same
mechanism a future explicit destination would use) and requires it be checked exactly like every
other write into a collection.

**Corrected in round 1 (MIN-005):** under Q5/B no caller ever supplies an arbitrary destination
argument — so the destinations this story actually needs to test are the three that really exist:
the collection root (a create with no existing name, D-WRITE-IDENTITY), beside a `.base` file
(the importer/re-derivation, US-2), and an existing view's own `SourcePath` (an upsert,
D-WRITE-IDENTITY). Dataset D (§11) is rewritten against these three, and `writeAndReloadViews`
(the one-shot importer's writer, F15) is brought up to the SAME containment check
`rederive.go::resolveViewWritePath` already runs — closing the inconsistency MIN-005 found between
the two writers.

**Why this priority**: P0 — ships in the same PR as US-1/US-2; this is not a follow-up hardening
pass, it is the gate that makes US-1 safe to land at all (Hard Constraint #6/D-SEC).

**Independent test**: a unit test constructs a `CollectionRoot` over a temp directory containing a
symlink pointing outside it, and asserts the view writer refuses to write through the symlink and
refuses any resolved path outside the root, using the same fixtures `contain_test.go` already uses
for other `CollectionRoot` consumers. A second unit test swaps a file discovery already walked for
a symlink before the read and asserts the read refuses it too (D-SYMLINK-READ, MIN-001).

**Acceptance Scenarios**:
1. **Given** a write destination that resolves (after symlink resolution) outside the collection
   root, **When** `create_view`/`write_view`/the importer/re-derivation is called, **Then** the
   write is refused with a refusal naming containment, and nothing is written.
2. **Given** a proposed view filename containing a path-traversal segment (`..`) or an
   OS-reserved/illegal component, **When** the view is written, **Then** `pathsafe` rejects it
   before any file is touched (F11).
3. **Given** a legitimate destination inside the collection root, **When** the view is written,
   **Then** it succeeds and is immediately discoverable by the next `knowledge_describe`/
   `knowledge_find` call (US-1).
4. **(New in round 1, MIN-001, D-SYMLINK-READ.) Given** a file discovery's walk found as an
   ordinary regular file, **When** that path is replaced with a symlink before the subsequent
   read (a race an agent with `bash` could induce), **Then** the read refuses it — the symlink's
   target content is never parsed or echoed into a rejection `Reason`.
5. **(New in round 1, MIN-002, D-SIZECAP.) Given** a `.view`-extension file over 256 KiB,
   **When** discovery or a directory listing encounters it, **Then** it is reported
   `view_too_large` and its content past the cap is never read into memory.
6. **(New in round 1, MIN-007, D-LOCK.) Given** an agent's `write_view` and a person's Library
   save of the SAME `.view` file are attempted concurrently, **When** both reach their write,
   **Then** they serialize on the identical lock key (`controlPlaneLockKey` ==
   `resolveLibraryLock`'s derived key for that collection-relative path, F14) — neither write is
   silently lost to the other.

### US-4 — `LibraryEntry` states whether a file is a view, what kind, its name, and its health (P1)

The SPA (and any other API consumer) needs to know, from an ordinary directory listing, that a
file is a view and which of the 8 kinds it is, without a second round trip per file.

**Expanded in round 1** to cover three more findings this same field-set touches: MAJ-003 (the
preview needs an addressable name, not just a display label), CRIT-002/MAJ-010 (a duplicate or
broken view must be visibly flagged in the listing, not just in a log an agent reads), and FD-1/
D-SCOPE (none of this applies outside a knowledge base).

**Why this priority**: P1 — depends on US-1's discovery existing; blocks US-5/US-6 (the UI can't
draw an icon or open a preview it has no field for).

**Independent test**: `GET /api/v1/library/{workspace_id}/entries` on a directory INSIDE a
knowledge base containing a `.view` file returns that entry with `is_view: true`, `view_name`, and
a `view_kind` matching the file's declared `kind`, verifiable with `curl`/an integration test with
no SPA involved. The same directory OUTSIDE a knowledge base returns none of those fields for the
identical file (D-SCOPE).

**Acceptance Scenarios**:
1. **Given** a `.view` file declaring `kind: calendar` inside a knowledge base, **When** the
   directory is listed, **Then** the entry carries `is_view: true`, `view_kind: "calendar"`,
   `view_name` set to `Def.Name`, and `view_label` set to its `DisplayLabel()` (F7).
2. **Given** a `.view` file declaring no `kind` (legal, F8), **When** the directory is listed,
   **Then** the entry carries `is_view: true` and `view_kind` absent (Edge Case EC-3) — never a
   fabricated default kind.
3. **Given** a `.view` file that fails to parse at all (malformed YAML), **When** the directory is
   listed, **Then** the entry still carries `is_view: true` (it IS a view file by extension) with
   `view_kind` and `view_name` absent, but `view_rejection` present naming the parse failure — the
   listing never fails because one file is broken (mirrors F1's `RejectViewUnreadable`/
   `RejectViewInvalidYAML` posture: reported, not hidden — and, new in round 1, reported ON the
   entry itself, not only in an agent-only channel, MAJ-010).
4. **(New in round 1, D-SCOPE/FD-1.) Given** the identical well-formed `.view` file, but placed
   in a folder that is outside every knowledge base, **When** the directory is listed, **Then**
   the entry carries none of `is_view`/`view_kind`/`view_label`/`view_name`/`view_rejection`/
   `view_derived_from` — it lists as a plain file.
5. **(New in round 1, CRIT-002/FD-2.) Given** two `.view` files that ended up sharing a `name`
   through some path OTHER than an Omnipus copy/restore/upload, **When** the directory containing
   either is listed, **Then** BOTH entries carry `view_rejection: "view_duplicate_name"` — never
   one silently missing while the other looks healthy.
6. **(New in round 1, FD-3/D-PROVENANCE.) Given** a `.view` file with `derived_from` set,
   **When** the directory is listed, **Then** the entry carries `view_derived_from` naming the
   source `.base` file's collection-relative path.
7. **(New in round 1, MIN-002/D-SIZECAP.) Given** a `.view` file over 256 KiB, **When** the
   directory is listed, **Then** the entry carries `is_view: true` and `view_rejection:
   "view_too_large"`, with the file's content never read past the cap to compute `view_kind`.

### US-5 — The Library tree shows a view with its own icon (P2)

A person browsing the Library wants to recognize a view at a glance — the founder's own framing:
"only with a different icon." Today a `.view` file (once US-1–US-4 land) would render with
whatever generic file icon the tree uses for an unrecognized extension.

**Why this priority**: P2 — cosmetic/navigational, depends on US-4's contract field.

**Independent test**: render the Library tree over a fixture directory with one view of each of
the 8 `kind` values plus one with no `kind`; assert 9 distinct rendered icon states (8 + fallback)
with a snapshot/visual test, no backend involved (contract data can be mocked once generated types
exist — Hard Constraint #8).

**Acceptance Scenarios**:
1. **Given** a Library directory listing containing views of different kinds, **When** the tree
   renders, **Then** each view's icon matches its `view_kind` (D-ICON's 8-way mapping) and a view
   with no `view_kind` shows the fallback view icon (EC-3) — never the generic unknown-file icon,
   since `is_view` is already known to be true.
2. **Given** the Library tree, **When** it lists a directory containing both a `.base` file and a
   `.view` file, **Then** they render with visually distinct icons (a view is never mistaken for
   the base it may have been imported from).
3. **(New in round 1, MIN-008.) Given** a view whose kind is conveyed only by an icon, **When**
   a screen reader encounters it, **Then** the icon carries an accessible name (e.g. an
   `aria-label` or equivalent) stating the kind in words ("Calendar view"), not merely a visual
   glyph — this spec does not ship an icon-only distinction with no text alternative.
4. **(New in round 1, FD-2/CRIT-002.) Given** a view with `view_rejection` set (duplicate or
   otherwise broken), **When** the tree renders it, **Then** it shows a visible badge distinct
   from every kind icon (a warning overlay, not a 9th "kind"), so a person browsing sees the
   problem without opening the file.
5. **(New in round 1, FD-3.) Given** a view with `view_derived_from` set, **When** the tree
   renders it, **Then** it shows a "derived" badge distinct from both the kind icon and the
   duplicate/broken badge (AS-4) — the three states are visually distinguishable from one
   another, not collapsed into one generic "something's different" mark.

### US-6 — Clicking a view opens it in the Library preview pane, addressed by name and knowledge base (P2)

Per founder direction, a view opens in the Library PREVIEW pane, never a modal — the same surface
every other Library entry already opens in (F6, F2).

**Corrected in round 1 (MAJ-003):** the original spec never said how the preview would actually
FETCH a view's rows once it decides to open one. `view_label` alone is not addressable (F7: a
label can be ambiguous; `Resolve` may return `ViewAmbiguousLabel`). D-ADDRESS (§2) fixes this: the
preview uses the new `view_name` field plus the enclosing knowledge base (resolved the same way
`BasePreview` already resolves it for a `.base` entry) to call the EXISTING
`GET .../knowledge/view` endpoint — no new contract endpoint.

**Why this priority**: P2 — depends on US-4/US-5; this is the payoff, not the plumbing.

**Independent test**: `classifyLibraryEntry` (F2/F6) returns `'view'` for a `.view`-extension entry
in isolation, with no rendering involved — the existing unit-test pattern
(`libraryPreviewKind.test.ts`) already covers `'base'` this way.

**Acceptance Scenarios**:
1. **Given** a `.view` file, **When** `classifyLibraryEntry` runs on its `LibraryEntry`, **Then**
   it returns the new `'view'` kind (added to `LIBRARY_PREVIEW_KINDS`, F2), extension-matched
   exactly like `'base'` is today, and placed after the mime-driven checks per the file's own
   documented ordering rule (F2's header comment).
2. **Given** a Library entry classified `'view'` with `view_name` present, **When**
   `LibraryPreviewPane` renders its body, **Then** `renderBody`'s switch gains a `case 'view':`
   that resolves the entry's enclosing knowledge base, calls `GET .../knowledge/view` with that
   collection and `view_name`, and mounts a preview reusing `ViewPartsRenderer` (F6) to draw the
   returned rows — not a re-implementation of that renderer, and not a modal.
3. **Given** the click happens from the tree (US-5) versus from a `.base` file's own view tabs
   (`BasePreview`, F6, unaffected by this spec), **When** either path opens a view, **Then** both
   land in the same preview pane component, honoring EMB-027's "exactly one renderer per kind"
   rule (`libraryPreviewVariant.ts` header, cited for continuity — not modified by this spec).
4. **(New in round 1, MAJ-003.) Given** a `.view` entry whose `view_name` is absent because the
   file failed to parse or was duplicate-rejected (`view_rejection` present instead), **When** it
   is opened, **Then** the preview shows the rejection's reason (naming the conflicting path for a
   duplicate, or the parse error for malformed YAML) — it never attempts a query with no name to
   query by, and never shows a blank or generic error.
5. **(New in round 1, FD-3.) Given** a `.view` entry with `view_derived_from` set, **When** it is
   opened, **Then** the preview shows a banner naming the source `.base` file and stating that
   edits to this view are overwritten on that file's next save.
6. **(New in round 1, MIN-008.) Given** the preview pane opening a view, **When** it is loading,
   returns zero rows, or the underlying view is `unservable` (F7's `ViewServeRefusal`), **Then**
   each state renders distinctly (a loading state, an empty state, and a refusal state naming the
   remedy) — reusing `BasePreview`'s existing refusal rendering where the same shape applies,
   rather than inventing a fourth, uncatalogued state.

### US-7 — Deleting a view goes to trash, like deleting any other note (P0) *(new in round 1, MAJ-004)*

An agent runs `delete_view` on a file that is now an ordinary, person-visible Library entry,
possibly sitting in a folder someone organized by hand. Today it is hard-deleted with a bare
`os.Remove` (F5a) — no trash, no restore — unlike every other agent-facing Library deletion.

**Why this priority**: P0 — ships with the rest of the write-path changes; a view that behaves
like a note for move/rename but not for delete is exactly the "special-cased file" the founder's
framing is meant to eliminate.

**Independent test**: call `delete_view` on a view anywhere in the tree; confirm the file appears
in `.trash` afterward (via the same restore door any other trashed note uses) rather than being
gone from disk entirely.

**Acceptance Scenarios**:
1. **Given** a view file anywhere in the collection, **When** `delete_view` is called, **Then**
   the file is trashed via `(*Trasher).Trash` (D-DELETE) — not `os.Remove` — and can be restored
   through the existing restore door.
2. **Given** a trashed view is restored, **When** the restore lands and its `name` collides with
   an existing view, **Then** FD-2's auto-rename applies (restore is one of the three
   Omnipus-mediated operations D-DUPLICATE covers) — the restored copy gets a unique suffixed
   `name`, not a rejection.

### US-8 — An agent can move or rename a view, the same as a person can (P0) *(new in round 1, MAJ-002)*

Q3/B promises a view is "moved and renamed like a note," but the only surface that today means is
`knowledge_restructure`'s rename/move — which appends `.md` to any non-folder target, silently
targeting the wrong file for a `.view` path (F18). Agents therefore have no reachable way to
reorganize a view themselves, breaking "agents must do everything wherever views are stored."

**Why this priority**: P0 — without this, Q3/B's central promise is human-only, contradicting
this feature's own founder direction.

**Independent test**: call `knowledge_restructure`'s rename/move op with `from`/`to` naming a
`.view` path (no `folder: true`); confirm the `.view` file itself moves/renames, not a
`.view.md` file.

**Acceptance Scenarios**:
1. **Given** an existing `.view` file, **When** an agent calls `knowledge_restructure` to rename
   or move it, **Then** the operation targets the `.view` path exactly, unchanged by
   `ensureMarkdown` — mirroring `(*Trasher).trashSourcePath`'s existing exact-path-first carve-out
   (D-MOVE, F18).
2. **Given** a view moved this way, **When** the next discovery walk runs, **Then** the view
   resolves at its new path with its `Def.Name` unchanged (Q3/B, EC-5) — and, if it is a derived
   view (FD-3), it remains findable by re-derivation via `derived_from` (US-2 AS-3).

### US-9 — Agents and people see the same view set, and the same is true after the founder's duplicate rule (P1) *(new in round 1, MAJ-011, FD-2)*

A person browsing the Library and an agent calling `knowledge_describe` should not silently
disagree about which views exist, and a routine Library action (copy, restore, upload) should
never quietly disable someone's working view.

**Why this priority**: P1 — this is the acceptance-test-shaped restatement of D-PARITY and
D-DUPLICATE (§2); it exists so the two firm decisions are independently verifiable, not just
asserted in prose.

**Independent test**: place a view inside a dot-prefixed folder (not one of the four
permanently-skipped names) and inside a mounted (symlinked) folder; confirm the first is found by
`knowledge_describe` but hidden from the default Library listing (parity with `.hidden.md`, F17),
and the second is shown in the Library tree but never found by any agent tool (parity with every
other symlinked mount, F10/F17). Separately, copy an existing view through the Library's copy
action; confirm the copy gets an auto-suffixed `name` and the original keeps working.

**Acceptance Scenarios**:
1. **Given** a `.view` file inside `.drafts/` (not `.omnipus-vault`/`.obsidian`/`.git`/`.trash`),
   **When** `knowledge_describe` runs, **Then** the view appears in its VIEWS section, exactly as
   `.hidden.md` already appears in text search today (F17) — and the default (non-`include_hidden`)
   Library listing does not show it, exactly as it would not show `.hidden.md`.
2. **Given** a `.view` file inside a mounted folder, **When** any agent tool discovers views,
   **Then** it is never found (F10's no-symlink-follow rule, unchanged) — even though the Library
   tree shows the mount and the file inside it.
3. **Given** a view named `weekly-status`, **When** it is copied via the Library's copy action to
   the same or a different folder, **Then** the copy is written with an auto-suffixed `name`
   (e.g. `weekly-status-2`) and both the original and the copy resolve independently by name
   (FD-2/D-DUPLICATE) — neither is rejected.

---

## 7. Behavioral Contract

Primary flows:
- When a `.view` file exists anywhere inside a knowledge base's real (symlink-resolved) boundary,
  the system includes it in every view listing/resolution an agent tool performs, identically to a
  view that used to live under the fixed directory (D-SCOPE limits the LIBRARY/UI classification
  to inside a knowledge base, FD-1; agent-tool discovery was already collection-scoped).
- When an agent creates a view with no existing name and no destination, the system writes it at
  the collection root as an ordinary, visible file (Q5/B); when the default path is already
  occupied, the write is refused instead of overwritten (D-WRITE-IDENTITY, CRIT-001).
- When an agent upserts a view by an existing name, the system writes to that view's OWN
  discovered location, never to a path reconstructed from the name (D-WRITE-IDENTITY, CRIT-001).
- When a `.base` file is imported or re-derived, the system writes each resulting view beside that
  `.base` file the first time, and thereafter locates and rewrites (or deletes) that SAME managed
  view via its persisted `derived_from` field wherever it currently sits, even after a move
  (D-PROVENANCE, FD-3).
- When a copy, trash-restore, or upload made THROUGH Omnipus's own Library operations would
  otherwise collide with an existing view's `name`, the system auto-renames the copy's `name`
  before it lands (D-DUPLICATE, FD-2) — no collision is ever seen by the person performing the
  operation.
- When a directory INSIDE a knowledge base is listed, the system reports, for every
  `.view`-extension entry, whether it is a view, its declared kind (if any), its authoritative
  name, its display label, whether it is derived (and from which `.base`), and whether it is
  broken or duplicate-rejected (and why) — without requiring a second request (D-CONTRACT).
- When a person clicks a view in the Library tree, the system resolves its enclosing knowledge
  base and authoritative name, opens it in the same preview pane every other Library entry uses,
  and renders its evaluated rows with the existing part renderer (D-ADDRESS, MAJ-003).
- When an agent moves, renames, or trash-deletes a `.view` file through `knowledge_restructure`,
  the operation targets that exact file — never a `.md`-suffixed miss — and a trashed view can be
  restored like any other note (D-MOVE, D-DELETE, MAJ-002/MAJ-004).

Error flows:
- When two views anywhere in a knowledge base declare the same `name` by a path OTHER than an
  Omnipus copy/restore/upload, the system rejects both exactly as it rejects a same-directory
  collision today (D-DEDUP) — never silently picking one, never silently dropping one — AND now
  visibly flags both in the tree, the preview, and agent tool output (D-DUPLICATE, FD-2,
  CRIT-002).
- When a write's resolved destination would leave the collection root (following a symlink or a
  traversal segment), the system refuses the write and writes nothing.
- When a `.view` file cannot be parsed, the system still lists it as `is_view: true` and reports
  the parse failure through the existing rejection-code channel (F1), surfaced on the
  `LibraryEntry` itself via `view_rejection` — never a listing failure, never a silent drop
  (D-VALIDATE, MAJ-010).
- When re-derivation's `os.Remove` of a managed view finds nothing to remove, the system does not
  report that file as deleted (MIN-009).
- When discovery's walk cannot read a subfolder (`WalkContained`'s `SkipUnreadable`), the system
  surfaces that gap in the view report rather than silently returning fewer views than actually
  exist (MAJ-009).

Boundary conditions:
- When a `.view` file declares no `kind`, the system reports `view_kind` absent, and the tree
  falls back to a generic view icon — never an invented default kind.
- When the collection contains zero `.view` files, discovery returns the same "0 views" shape
  `knowledge_describe` already renders for an empty `ViewSet` today.
- When a `.view` file exceeds 256 KiB, the system reports `view_too_large` and never reads its
  content past the cap (D-SIZECAP, MIN-002).
- When a `.view` file sits outside every knowledge base, the system treats it as a plain,
  unrunnable file on every surface (D-SCOPE, FD-1).

---

## 8. Edge Cases

- **EC-1 — Symlinked subfolder.** A `.view` file reachable only through a symlinked directory is
  never traversed to (F10, FR-044's existing rule) — it is invisible to discovery, exactly as any
  other content behind a symlink already is. Not a regression: same rule every other collection
  walk already follows. **Extended in round 1 (MAJ-011/D-PARITY, F17):** a symlinked MOUNT
  folder the Library tree shows content for is subject to the identical rule — a view inside it is
  visible in the tree but never found by any agent tool. Documented explicitly (Dataset A, §11)
  rather than left as a silent consequence.
- **EC-2 — Control-plane directories.** A `.view`-suffixed file placed inside `.omnipus-vault`,
  `.obsidian`, `.git`, or `.trash` is never discovered (`WalkContained` skips these at any depth,
  F10). **Corrected in round 1 (MAJ-006):** this is no longer tied to an orphan-directory
  diagnostic — D-Q4-NO-WARN drops that idea outright; a leftover `.omnipus-vault/views/*.yaml`
  file simply stops being found, silently, by design, per the greenfield rule.
- **EC-2a — Dot-prefixed, non-control-plane directory (new in round 1, MAJ-011/D-PARITY, F17).** A
  `.view` file inside e.g. `.drafts/` IS discovered by agents (not one of the four permanently-
  skipped names) but IS hidden from the default Library listing (dot-prefix hidden rule, F2) —
  exactly the pre-existing, documented parity with `.hidden.md` (F17). Not a bug; tested
  explicitly (Dataset A) so it stays intentional, not accidental.
- **EC-3 — No declared `kind`.** Legal under the schema (F8: only `name` is required). Listing
  reports `is_view: true`, `view_kind` absent; the tree shows a generic fallback view icon
  (US-5 AS-1); the preview pane still opens and renders the view's rows normally — `kind` only
  ever drove the composer/icon, never the query itself.
- **EC-4 — Two views, same `name`, different folders, NOT via an Omnipus copy/restore/upload.**
  Rejected (D-DEDUP unchanged) — reported through the same `RejectViewDuplicateName` channel that
  a same-directory collision already uses today, now reachable from a wider input set (§4), AND
  now visibly flagged on both files (D-DUPLICATE, FD-2, CRIT-002) rather than silently dropped.
- **EC-4a — Two views, same `name`, produced BY an Omnipus copy, trash-restore, or upload (new in
  round 1, FD-2).** Auto-renamed before EC-4's rule ever applies (D-DUPLICATE) — the person never
  sees a collision.
- **EC-5 — A HAND-MADE view file renamed or moved via an ordinary Library file operation (Q3/B).**
  The view's `Def.Name` (and therefore every agent-facing reference to it) is unaffected; the next
  discovery walk finds it at its new path. If the rename/move produced a NEW name collision with
  another view (unlikely, since `Def.Name` is inside the file content and untouched by a rename —
  this only happens if the move brings two independently-named-identically files into scope
  together for the first time, e.g. two collections merging), EC-4/EC-4a applies as appropriate.
- **EC-5a — A DERIVED view file renamed or moved (new in round 1, FD-3/D-PROVENANCE).** Unlike
  EC-5, this does NOT make the view independent — `derived_from` still names its managing `.base`,
  and the next re-derivation still finds it (via `derived_from`, not path) and still overwrites or
  deletes it, at its NEW location. FD-3's "`.base` wins" survives a move.
- **EC-6 — A `.view` file that is actually just YAML a user wrote by hand for an unrelated
  purpose, using the `.view` extension by coincidence.** Out of scope to prevent (Q1/A accepts
  this risk in exchange for a one-glob, no-content-sniff discovery filter); it will be listed as
  `is_view: true` and attempt to parse as a `ViewDef`, surfacing a rejection code if it is not one
  (F1) — same posture as a malformed genuine view (EC in US-4 AS-3).
- **EC-7 — Very large collection (F12's 100,000-file scale).** Discovery cost is O(collection
  files) plus O(views) parses for kind-tagging (D-ICON) — Q2 requires a benchmark before landing,
  now against an absolute bound (MIN-003), not an "any measured number passes" criterion.
- **EC-8 — A `.view` file over the size cap (new in round 1, MIN-002/D-SIZECAP).** Reported
  `view_too_large`; never read past 256 KiB.
- **EC-9 — A file named exactly `.view`, no stem (new in round 1, MIN-010).** A dotfile by the
  Library's own definition (F2) — hidden by default, and NOT discovered as a view (its "extension"
  is its entire name; there is no distinct view content to classify). Stated explicitly so
  FR-VA-012 ("the view extension must not be hidden") is not misread as applying here.
- **EC-10 — A view outside every knowledge base (new in round 1, D-SCOPE/FD-1).** Lists as a plain
  file on every surface — no `is_view`, no icon, no preview case.

---

## 9. Explicit Non-Behaviors & Safeguards

### Qualitative prohibitions

- The system must not silently migrate or rewrite files under the old `.omnipus-vault/views/`
  directory — greenfield rule. **Corrected in round 1 (MAJ-006):** there is no diagnostic either
  now (D-Q4-NO-WARN) — the only allowed reaction to a leftover legacy file is that it silently
  stops being found; the ONE required action is the implementing lead's GREEN-task grep-and-move
  of e2e/fixture files, not a runtime WARN.
- The system must not broaden a view's filter semantics while changing where the file lives —
  F1's "a view is never broadened on the operator's behalf" (FR-105) is untouched by this spec;
  nothing here parses or rewrites `filter` trees.
- The system must not follow a symlink during discovery or during a write's destination
  resolution, under any circumstance (F10's FR-044, reused unmodified) — **extended in round 1
  (MIN-001/D-SYMLINK-READ)** to the READ itself: a file the walk found as a regular file that is
  later swapped for a symlink must not have its target's content read.
- The system must not let a single malformed `.view` file fail an entire directory listing or an
  entire `knowledge_describe`/`knowledge_find` call (F1's existing per-file rejection posture,
  reused).
- The system must not invent a `view_kind` value for a file that declares none (EC-3) — an
  agent-guessed or heuristically-inferred kind would misrepresent what `create_view` actually
  wrote and is exactly the kind of silent approximation F1 already prohibits for filters.
- The system must not require a new required argument on `create_view`/`write_view` to preserve
  today's zero-argument call pattern (Q5/B) — existing agent prompts/skills that call these tools
  today must keep working unchanged.
- **(New in round 1, CRIT-001/D-WRITE-IDENTITY.)** The system must not overwrite an unrelated
  file, or a moved view's file, when a name-based upsert or create is requested — it must locate
  an existing name's file by its discovered `SourcePath` and must refuse (never overwrite) a
  create whose default path is already occupied.
- **(New in round 1, FD-3/D-PROVENANCE.)** The system must not infer whether a view is derived
  from its `source` field, and must not let `create_view`/`write_view` set or clear
  `derived_from` — provenance is written exclusively by the import/re-derivation pipeline.
- **(New in round 1, FD-2/D-DUPLICATE.)** The system must not let an Omnipus-mediated copy,
  restore, or upload of a `.view` file silently disable the view it was copied from — a collision
  produced by one of those three operations must be resolved by auto-rename before it can trigger
  D-DEDUP, not merely warned about afterward.
- **(New in round 1, MAJ-004/D-DELETE.)** `delete_view` must not hard-delete a view file with no
  trash/restore path — it must go through the same `Trasher` every other agent-facing Library
  deletion uses.
- **(New in round 1, MAJ-002/D-MOVE.)** `knowledge_restructure`'s rename/move must not silently
  target a `.md`-suffixed path when the caller names an existing `.view` file.

### Machine-verifiable constraints

**Contract (LibraryEntry, Hard Constraint #8):**
- `is_view` MUST be present (`true`) only when the entry's extension matches Q1's chosen
  extension AND the entry is inside a knowledge base (D-SCOPE, FD-1); absent for every other
  entry (mirrors `is_knowledge_base`'s optionality, F2/D-CONTRACT).
- `view_kind`, when present, MUST be one of F8's exact 8 enum values (`table, list, tiles, board,
  calendar, summary, trend, breakdown`) — generated from the same `ViewDefKind` enum `create_view`
  already uses, never a hand-written parallel list (Hard Constraint #8).
- `view_name` MUST be present if and only if the file both is `is_view: true` and parses
  successfully (D-ADDRESS); absent whenever `view_rejection` is present.
- `view_rejection`, when present, MUST be one of the existing `RejectView*` codes (F1's family,
  extended with `view_too_large`, D-SIZECAP) — never a hand-written parallel code (D-VALIDATE,
  CRIT-002).
- `view_derived_from`, when present, MUST equal the exact collection-relative path stored in that
  file's own `ViewDef.derived_from` (D-PROVENANCE) — never inferred from `source`.

**Security (containment, D-SEC):**
- A view write whose resolved destination is outside the collection's real (symlink-resolved)
  root MUST be refused before any filesystem write occurs.
- A view write whose destination path resolves through a symlink at any component MUST be refused
  (`ResolveContainedNoSymlink`, F10/F11), matching `WalkContained`'s own read-side rule.
- **(New in round 1, MIN-001.)** A discovered file whose path resolves, at read time, to something
  other than the regular file the walk observed (i.e. swapped for a symlink between walk and read)
  MUST be refused, never read.
- **(New in round 1, MIN-002.)** A `.view` file over 256 KiB MUST NOT be read past that cap.

**Data constraints:**
- The chosen extension (Q1) MUST be excluded from `pkg/library/entries.go`'s hidden-file rule
  exactly as `.base` already is (F2) — a view is not a dot-file and must not be hidden by default
  (this does not apply to a file named exactly `.view`, EC-9, which IS a dotfile).
- `ViewDef.name` uniqueness MUST remain vault-wide (F7), evaluated over the full walk result
  (§4), not per-directory, and evaluated AFTER D-DUPLICATE's auto-rename step for the three
  Omnipus-mediated operations it covers.
- **(New in round 1, D-WRITE-IDENTITY.)** An upsert of an existing `name` MUST write to that
  view's current `SourcePath`; a create whose default path is already occupied MUST be refused.
- **(New in round 1, D-PROVENANCE.)** `derived_from` MUST be settable only by the import/
  re-derivation pipeline; a `create_view`/`write_view` call supplying it MUST be refused.

**Performance (Q2, corrected in round 1 per MIN-003):**
- Discovery over a benchmark fixture at the repo's standing 100,000-file scale assumption (F12)
  MUST complete within an ABSOLUTE upper bound fixed before landing (measured, then hard-coded as
  a CI-checkable number — not "whatever the benchmark measures," which cannot fail).
- A single `write_view`/`create_view`/`delete_view` call MUST perform the collection walk at most
  once (FR-VA-018a) — today's multiple `LoadViews` calls per operation (F4) are refactored to
  share one discovery result.

### Conservative type design

No new nominal Go type is introduced for a view's location — a collection-relative path is a
`string`, exactly as every other `WalkContained`/`CollectionRoot` consumer already treats it
(F10) — `SavedView.SourcePath` (F13) is reused rather than adding a new field. `ViewDefKind`
(generated, F8) is reused as-is for `view_kind`; no parallel Go or TS enum is hand-written for it
(Hard Constraint #8). `derived_from` (new in round 1) is a plain `string`, matching `source`'s own
existing type — no new nominal type for provenance either. `view_rejection` reuses F1's existing
rejection-code representation — no second rejection-code type.

---

## 10. TDD Plan

| Order | Test Name | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestWalkContained_FindsViewExtensionAnywhere` | Unit | US-1 AS-1 | Views in a subfolder are returned by the discovery filter over `WalkContained`'s file list. |
| 2 | `TestLoadViews_DedupAcrossDirectories` | Unit | US-1 AS-3, EC-4 | Two same-named views in different folders both reject via the existing `RejectViewDuplicateName` code. |
| 3 | `TestLoadViews_ResolveByNameAcrossFolders` | Unit | US-1 AS-2 | `ViewSet.Resolve` finds a view by name regardless of which folder it lives in. |
| 4 | `TestCreateView_DefaultsToCollectionRoot` | Unit | US-1 AS-4, Q5 | No-destination `create_view` call, no existing occupant, lands the file at the collection root. |
| 5 | `TestVaultImport_WritesViewBesideBaseFile` | Unit | US-2 AS-1 | Importer writes the resulting `.view` file in the `.base` file's own directory, with `derived_from` set. |
| 6 | `TestVaultImport_DuplicateNameRejected` | Unit | US-2 AS-2 | Import collision uses the same rejection code as a manual `write_view` collision. |
| 7 | `TestWriteView_RefusesSymlinkEscape` | Unit | US-3 AS-1, EC-1 | Destination resolving outside the collection root through a symlink is refused, nothing written. |
| 8 | `TestWriteView_RefusesTraversalFilename` | Unit | US-3 AS-2 | A `..`-bearing or reserved-name filename is refused by `pathsafe` before any write. |
| 9 | `TestWriteView_SucceedsAndIsImmediatelyDiscoverable` | Integration | US-3 AS-3 | A legitimate write is found by the very next discovery call. |
| 10 | `TestLibraryEntries_IsViewAndKind` | Integration | US-4 AS-1 | Directory listing (inside a KB) reports `is_view`/`view_kind`/`view_label`/`view_name` for a well-formed view. |
| 11 | `TestLibraryEntries_ViewWithNoKind` | Integration | US-4 AS-2, EC-3 | `view_kind` absent, `is_view` still true, for a kind-less view. |
| 12 | `TestLibraryEntries_MalformedViewStillListed` | Integration | US-4 AS-3, EC-6 | A malformed `.view` file still lists with `is_view: true`, `view_rejection` set, no listing failure. |
| 13 | `TestClassifyLibraryEntry_ReturnsViewKind` | Unit (TS) | US-6 AS-1 | `classifyLibraryEntry` returns `'view'` for the chosen extension, positioned per F2's ordering rule. |
| 14 | `TestLibraryPreviewPane_ViewCaseMountsRenderer` | Unit (TS) | US-6 AS-2 | `renderBody`'s `'view'` case resolves the enclosing KB + `view_name`, calls the existing view endpoint, and mounts a component reusing `ViewPartsRenderer`. |
| 15 | `TestLibraryTree_IconPerKind` | Unit/Visual (TS) | US-5 AS-1 | 8 kinds + fallback render 9 distinct icon states. |
| 16 | `TestLibraryTree_ViewVsBaseIconDistinct` | Unit/Visual (TS) | US-5 AS-2 | A `.base` and a `.view` entry never share an icon. |
| 17 | `BenchmarkViewDiscovery_100kFiles` | Benchmark | SC-VA-004, Q2 | Establishes the real number for the performance gate — run before, not after, landing; the resulting number becomes an absolute CI-checked bound (MIN-003), not a self-referential pass. |
| 18 | *(deleted, MAJ-006)* | — | — | Was `TestOrphanedLegacyViewsDir_WarnsOnStartup`; dropped with D-Q4-NO-WARN — no replacement needed. |
| 19 | `TestWriteView_UpsertTargetsExistingSourcePath` | Unit | US-1 AS-5, CRIT-001 | Upserting an existing name writes to that view's discovered `SourcePath`, not a name-reconstructed path; an unrelated same-named-by-coincidence file elsewhere is untouched. |
| 20 | `TestCreateView_RefusesOccupiedDefaultPath` | Unit | US-1 AS-6, CRIT-001 | A create whose default collection-root path already exists (view or not) is refused, naming the conflict; nothing is overwritten. |
| 21 | `TestLibraryEntries_OutsideKnowledgeBaseIsPlainFile` | Integration | US-1 AS-7, US-4 AS-4, FD-1/D-SCOPE | A `.view` file outside every knowledge base carries none of the six view fields. |
| 22 | `TestRederive_FindsMovedManagedViewByDerivedFrom` | Integration | US-2 AS-3, MAJ-001/D-PROVENANCE | A moved derived view is located and rewritten in place via `derived_from`, not duplicated. |
| 23 | `TestRederive_DeletesMovedManagedViewWhenNoLongerDeclared` | Integration | US-2 AS-4, FD-3 | A moved derived view whose `.base` no longer declares it is deleted at its current (moved) location. |
| 24 | `TestRederive_DoesNotReportDeletedWhenAlreadyGone` | Unit | US-2 AS-5, MIN-009 | `os.ErrNotExist` on the delete step is not counted as a successful deletion in the report. |
| 25 | `TestHandMadeView_NeverTouchedByRederivation` | Unit | US-2 AS-7, FD-3 | A hand-made view with a descriptive `source` but no `derived_from` is untouched when that named `.base` is saved. |
| 26 | `TestWriteView_RefusesCallerSuppliedDerivedFrom` | Unit | D-PROVENANCE (Non-Behaviors) | `write_view`/`create_view` refuse a call that supplies `derived_from`. |
| 27 | `TestLibraryCopy_AutoRenamesCollidingViewName` | Integration | US-9 AS-3, FD-2/CRIT-002 | Copying a view through the Library's copy action auto-suffixes the copy's `name`; both resolve independently. |
| 28 | `TestLibraryRestore_AutoRenamesCollidingViewName` | Integration | US-7 AS-2, FD-2 | Restoring a trashed view that collides gets the same auto-rename as a copy. |
| 29 | `TestLibraryUpload_AutoRenamesCollidingViewName` | Integration | FD-2 | Uploading a `.view` file that collides gets the same auto-rename. |
| 30 | `TestDuplicateView_VisibleOnBothEntriesAndInAgentOutput` | Integration | US-4 AS-5, US-5 AS-4, US-6 AS-4, CRIT-002 | A non-Omnipus-mediated collision sets `view_rejection` on BOTH `LibraryEntry` rows and is named for both paths in `knowledge_describe`/`knowledge_find` output. |
| 31 | `TestDeleteView_GoesToTrash` | Integration | US-7 AS-1, MAJ-004/D-DELETE | `delete_view` trashes rather than hard-deletes; the file is restorable. |
| 32 | `TestKnowledgeRestructure_MovesDotViewPathUnchanged` | Integration | US-8 AS-1, MAJ-002/D-MOVE | Rename/move of a `.view` path is not `.md`-suffixed by `ensureMarkdown`. |
| 33 | `TestDiscovery_SkipsControlDirsButIndexesDotFolders` | Unit | US-9 AS-1, EC-2/EC-2a, MAJ-011/D-PARITY | Views under `.omnipus-vault`/`.obsidian`/`.git`/`.trash` are never discovered; a view under `.drafts/` (non-control dot-folder) IS discovered. Names the gap the original traceability matrix left as "add at implementation" for FR-VA-002. |
| 34 | `TestDiscovery_NeverFollowsMountSymlink` | Unit | US-9 AS-2, EC-1, MAJ-011/D-PARITY | A view inside a symlinked mount folder is never found by discovery even though the Library tree shows the mount. |
| 35 | `TestWriteView_SwappedSymlinkReadRefused` | Unit | US-3 AS-4, MIN-001/D-SYMLINK-READ | A walked regular-file path swapped for a symlink before the read is refused, not parsed. |
| 36 | `TestDiscovery_RefusesOversizeViewFile` | Unit | US-3 AS-5, MIN-002/D-SIZECAP | A `.view` file over 256 KiB reports `view_too_large` and is never read past the cap. |
| 37 | `TestConcurrentLibrarySaveAndWriteView_ShareOneLock` | Integration | US-3 AS-6, MIN-007/D-LOCK | A Library save and an agent `write_view` on the same file serialize on an identical lock key; neither write is lost. |
| 38 | `TestDiscovery_ReportsUnreadableSubfolder` | Unit | MAJ-009 | A `SkipUnreadable` entry from `WalkContained` is surfaced in the view load report, not silently dropped. |
| 39 | `TestLibraryContentPut_ViewSaveNeverRefusesButFlagsRejection` | Integration | MAJ-010/D-VALIDATE | A Library raw-text save of invalid `.view` content lands (never-refuse posture, F16) and the next listing shows `view_rejection`. |
| 40 | `TestLibraryEntries_IsHiddenFalseForViewExtension` | Unit | FR-VA-012 | Names the gap the original traceability matrix left as "add at implementation": a `.view`-extension entry (not the bare `.view` dotfile, EC-9) has `is_hidden: false`. |
| 41 | `TestKnowledgeRestructure_RenameThenResolveByUnchangedName` | Integration | FR-VA-016, EC-5 | Names the gap the original traceability matrix left as "add at implementation": after a rename/move, the view still resolves by its unchanged `Def.Name`. |
| 42 | `TestClassifyLibraryEntry_ExtensionCaseInsensitive` | Unit (TS) | MIN-010 | `.VIEW` classifies identically to `.view`. |
| 43 | `TestLibraryEntries_BareDotViewIsHiddenNotAView` | Unit | EC-9, MIN-010 | A file literally named `.view` is hidden (dotfile rule) and is not classified `is_view`. |

### BDD scenario blocks (new in round 1, MIN-012)

Three scenarios the original document referenced without a Given/When/Then block:

```
Scenario: Re-derivation locates a moved managed view by provenance, not path
  Traces to: US-2 AS-3
  Given a view file at "projects/roadmap-open.view" with derived_from "projects/roadmap.base"
  And a person has moved that file to "archive/roadmap-open.view"
  When "projects/roadmap.base" is saved again with that view's definition unchanged
  Then re-derivation rewrites "archive/roadmap-open.view" in place
  And no second file is written beside "projects/roadmap.base"

Scenario: A view opened from a base's own tabs and a view opened from the tree land in one preview
  Traces to: US-6 AS-3
  Given a view imported from "projects/roadmap.base"
  When it is opened via BasePreview's tab for that base
  And the same view is opened via a click on its own file in the Library tree
  Then both opens mount the identical preview pane component

Scenario: A view with no declared label falls back to its name for view_label
  Traces to: US-4 AS-1 (view_label clause)
  Given a view file declaring name "open-by-owner" and no label
  When the directory containing it is listed
  Then the entry's view_label is "open-by-owner"
```

### Regression test requirements

This feature modifies existing functionality (F3/F4's read and write choke points).

1. **Existing behaviors that MUST be preserved:**
   - Every `RejectView*` code (F1) keeps its exact meaning and trigger condition.
   - `ViewSet.Resolve`'s slug-then-label lookup order (F7) is unchanged.
   - `knowledge_describe`'s VIEWS rendering shape (full vs. catalog, `viewsInlineThreshold = 12`)
     is unchanged — it renders whatever `ViewSet.Views()` returns, and that list's *contents*
     change (wider discovery) but its *shape* does not.
   - `BasePreview`'s existing tab-per-imported-view rendering (F6, `rest_knowledge_base_views.go`)
     is unaffected — it already reads views by `source`, not by directory.
2. **Existing tests that MUST continue to pass — corrected in round 1 (MIN-011): "unchanged" was
   impossible as originally written.** `pkg/records/view_test.go` calls `ViewsDir` directly, and
   the second grill pass counted roughly 29 Go files and 4 SPA files across the repo that plant
   fixtures under `.omnipus-vault/views/` (count not independently re-verified here — implementing
   lead re-counts at RED time). The correct claim is: fixtures are RELOCATED and any direct
   `ViewsDir`/`LoadViews`-signature call in a test is updated to the new call shape (§4); the
   *assertions* those tests make (what a `RejectView*` code means, what `Resolve` returns, what
   `knowledge_describe`'s golden output looks like for a fixed view set) are unchanged. The
   relocation itself is an explicit GREEN task, tracked the same way as the e2e/fixture sweep
   D-Q4-NO-WARN already requires.
3. **New regression tests protecting unchanged behavior:** test 2 above (dedup) and test 3
   (resolve) are as much regression tests as new-feature tests — they prove the *widened* input
   set still obeys the *unchanged* rule.
4. **Regression dataset:** a fixture collection identical to whatever `knowledge_describe`'s
   current "0 views" and "N views inline" golden fixtures use, with views moved from the old fixed
   directory into varied subfolders — output must be byte-identical to today's. **Corrected in
   round 1 (OBS-001):** no new `Path` field is added (the original §4 step 4 proposal is dropped
   in favor of reusing `SavedView.SourcePath`, F13) — so this dataset's output has no new field to
   account for at all.

---

## 11. Test Datasets

### Dataset A — Discovery locations

| ID | View location | Expected discovered (agent)? | Expected in default Library listing? | Traces to |
|---|---|---|---|---|
| A1 | Collection root | Yes | Yes | US-1 AS-4 |
| A2 | One level deep (`notes/x.view`) | Yes | Yes | US-1 AS-1 |
| A3 | Several levels deep (`a/b/c/x.view`) | Yes | Yes | US-1 AS-1 |
| A4 | Inside `.omnipus-vault/` | No (skipped) | No (hidden) | EC-2 |
| A5 | Inside `.git/` | No (skipped) | No (hidden) | EC-2 |
| A6 | Inside `.obsidian/` | No (skipped) | No (hidden) | EC-2 |
| A7 | Behind a symlinked directory (mount) | No (never traversed, EC-1) | Yes — tree shows the mount's contents | US-3 AS-1, EC-1, MAJ-011 |
| A8 | Empty collection (zero `.view` files) | N/A — "0 views" shape | N/A | Behavioral Contract, boundary |
| A9 *(new, MAJ-011)* | Inside `.drafts/` (dot-prefixed, not a control-plane name) | Yes | No (hidden — dot-prefix rule) | EC-2a, US-9 AS-1 |
| A10 *(new, MAJ-005/FD-1)* | Inside a plain workspace folder, outside every knowledge base | No (never was — collection-scoped) | Listed, but with none of the six view fields (plain file) | EC-10, US-1 AS-7 |
| A11 *(new, MIN-010)* | A file named exactly `.view` (no stem), at the collection root | No | No (dotfile, hidden) | EC-9 |
| A12 *(new, MIN-010)* | `NOTES.VIEW` (uppercase extension) | Yes, same as `.view` | Yes | MIN-010 |

### Dataset B — Name/label collisions

| ID | View 1 name/label | View 2 name/label | Same folder? | Expected | Traces to |
|---|---|---|---|---|---|
| B1 | `open-by-owner` / "Open by owner" | `open-by-owner` / "Open (mine)" | Yes | Both rejected, `view_duplicate_name` | US-1 AS-3 (today's rule) |
| B2 | `open-by-owner` / "Open by owner" | `open-by-owner` / "Different label" | No (different folders) | Both rejected, same code | US-1 AS-3, EC-4 |
| B3 | `open-by-owner` / "All Projects" | `closed-by-owner` / "All Projects" | Either | Both load; `Resolve("All Projects")` returns `ViewAmbiguousLabel` with both as candidates | F7 (unchanged) |
| B4 | `open-by-owner` / (no label) | — | — | `DisplayLabel()` falls back to `name` (F7, unchanged) | US-4 AS-1 |

### Dataset C — `kind` values and icon mapping (D-ICON)

| ID | `kind` value | Expected `view_kind` | Expected icon state | Traces to |
|---|---|---|---|---|
| C1 | `table` | `table` | table icon | US-4 AS-1, US-5 AS-1 |
| C2 | `list` | `list` | list icon | US-5 AS-1 |
| C3 | `tiles` | `tiles` | tiles icon | US-5 AS-1 |
| C4 | `board` | `board` | board icon | US-5 AS-1 |
| C5 | `calendar` | `calendar` | calendar icon | US-5 AS-1 |
| C6 | `summary` | `summary` | summary icon | US-5 AS-1 |
| C7 | `trend` | `trend` | trend icon | US-5 AS-1 |
| C8 | `breakdown` | `breakdown` | breakdown icon | US-5 AS-1 |
| C9 | (absent) | (absent) | fallback view icon | US-4 AS-2, EC-3 |
| C10 | invalid/unrecognized string (hand-edited file) | (absent, treated as unset rather than propagating an invalid enum value to the wire) | fallback view icon | EC-6, Hard Constraint #8 (contract enum integrity) |

### Dataset D — Write-path containment, rewritten in round 1 against the destinations that actually exist (security, D-SEC, MIN-005)

The original Dataset D tested a caller-supplied destination argument that Q5/B says never
exists. Rewritten against the three real destinations: the collection-root default (create), the
importer/re-derivation's beside-the-`.base` write, and an upsert's existing `SourcePath`.

| ID | Writer | Destination scenario | Expected | Traces to |
|---|---|---|---|---|
| D1 | `create_view`, no existing name | Collection root (default, Q5/B), path free | Written | US-1 AS-4 |
| D2 | `create_view`, no existing name | Collection root, path already occupied (view or not) | Refused, naming the conflict; nothing overwritten | US-1 AS-6, CRIT-001 |
| D3 | `write_view`, existing name | That view's current `SourcePath`, wherever it is | Written (replaces that file only) | US-1 AS-5, CRIT-001 |
| D4 | Importer (`run.go`) | Beside the `.base` file, inside the collection | Written, `derived_from` set | US-2 AS-1 |
| D5 | Importer/re-derivation | Destination resolves through a symlink to outside the root | Refused, nothing written | US-3 AS-1, EC-1 |
| D6 | Importer/re-derivation | Destination contains a `..` traversal segment | Refused by `pathsafe` before write | US-3 AS-2 |
| D7 | Any writer | Proposed filename is an OS-reserved device name (e.g. `CON`) | Refused by `pathsafe` before write | US-3 AS-2 |
| D8 | `create_view` | Name given, no destination concept applies (no argument exists) | Treated as collection root, not an error | US-1 AS-4 |
| D9 *(new, MIN-005)* | `writeAndReloadViews` (one-shot importer) | Same symlink-escape input as D5 | Refused — brought up to `resolveViewWritePath`'s existing check (F15) | US-3 AS-1 |

### Dataset E — retired in round 1 (MAJ-006)

The original Dataset E (legacy `.omnipus-vault/views/` startup WARN) is deleted along with
FR-VA-017/SC-VA-007/TDD test 18/Holdout 7 — D-Q4-NO-WARN (§2) drops the diagnostic outright.

### Dataset F — Write-identity and provenance (new in round 1, CRIT-001/D-PROVENANCE)

| ID | Scenario | Expected | Traces to |
|---|---|---|---|
| F-1 | `write_view name=weekly`, `weekly` exists at `projects/weekly.view`, an unrelated file sits at `<root>/weekly.view` | Writes `projects/weekly.view`; `<root>/weekly.view` untouched | US-1 AS-5 |
| F-2 | `create_view name=report`, `<root>/report.view` already exists (any content) | Refused, names the conflict | US-1 AS-6 |
| F-3 | A `.base`-derived view is moved by a person, then its `.base` is re-saved with that view's definition unchanged | Re-derivation rewrites the view at its NEW location, found via `derived_from` | US-2 AS-3 |
| F-4 | Same as F-3, but the `.base` no longer declares that view | Re-derivation deletes it at its NEW location | US-2 AS-4 |
| F-5 | A managed view's file is already gone before re-derivation's delete step runs | `Deleted` in the report does NOT name it | US-2 AS-5 |
| F-6 | A hand-made view sets a descriptive `source` naming a `.base` file, but has no `derived_from` | That `.base`'s next save never touches the hand-made view | US-2 AS-7 |
| F-7 | `write_view`/`create_view` called with a `derived_from` argument | Refused | Non-Behaviors (D-PROVENANCE) |

### Dataset G — Duplicate handling (new in round 1, FD-2/D-DUPLICATE, CRIT-002)

| ID | Operation | Scenario | Expected | Traces to |
|---|---|---|---|---|
| G-1 | Library copy | Copy of view `weekly-status` to a new location | Copy's `name` auto-suffixed (e.g. `weekly-status-2`); both resolve independently | US-9 AS-3 |
| G-2 | Trash restore | Restoring a trashed view whose `name` now collides with a view created since it was trashed | Restored copy's `name` auto-suffixed | US-7 AS-2 |
| G-3 | Upload | Uploading a `.view` file whose `name` collides with an existing view | Uploaded copy's `name` auto-suffixed | FD-2 |
| G-4 | Hand-authored second file (not copy/restore/upload) | Two `.view` files sharing a `name`, neither produced by Omnipus | Both rejected (D-DEDUP); both carry `view_rejection: view_duplicate_name`; both paths named in `knowledge_describe`/`knowledge_find` output | US-9, CRIT-002 |
| G-5 | External sync client conflict copy | A sync tool drops a second file with the same `name` | Same as G-4 — not an Omnipus-mediated operation | CRIT-002 |

---

## 12. Functional Requirements

- **FR-VA-001**: The system MUST discover every `.view`-extension file reachable inside a
  collection's real, symlink-resolved boundary, using the existing `WalkContained` containment
  and symlink-skip rules unmodified (F10, D-WALK), via a helper living in `pkg/knowledge`
  (**corrected in round 1, MAJ-008** — never inside `pkg/records`).
- **FR-VA-002**: The system MUST NOT discover a `.view` file inside `.omnipus-vault`, `.git`,
  `.obsidian`, or `.trash`, at any depth (EC-2, reusing `scanSkippedDirNames`); it MUST discover
  one inside any OTHER dot-prefixed folder (EC-2a, D-PARITY, **new in round 1, MAJ-011**) —
  hidden from the Library, not from agents, exactly as `.hidden.md` already is (F17).
- **FR-VA-003**: The system MUST reject two views anywhere in the collection that declare the same
  `name`, using the existing `RejectViewDuplicateName` code, evaluated over the full discovery
  result rather than one directory (D-DEDUP, Dataset B1/B2), **downstream of FR-VA-019's
  auto-rename step (new in round 1, FD-2)**.
- **FR-VA-004**: The system MUST continue to resolve a view by `Def.Name` first, then by
  `DisplayLabel()` with `ViewAmbiguousLabel` on a label collision, unchanged from today (F7,
  Dataset B3).
- **FR-VA-005**: `create_view` and `write_view` MUST succeed with no destination argument
  supplied; a CREATE with no existing name writes to the collection root (Q5/B, Dataset D1); an
  UPSERT of an existing name writes to that view's discovered `SourcePath` instead (**corrected
  in round 1, CRIT-001/D-WRITE-IDENTITY** — the original wording did not distinguish create from
  upsert, which is exactly what let an upsert silently overwrite or duplicate).
- **FR-VA-005a *(new in round 1, CRIT-001)***: A CREATE whose default collection-root path
  (`<name>.view`) already exists — whatever it contains — MUST be refused, naming the conflict,
  never silently overwritten (Dataset D2/F-2).
- **FR-VA-006**: Any destination resolved for a view write MUST be checked with
  `CollectionRoot.ResolveContainedNoSymlink` (or equivalent) before any filesystem write; a
  destination outside the real collection root or reached through a symlink MUST be refused with
  nothing written (D-SEC, Dataset D5) — **this now covers the one-shot importer's own writer too
  (new in round 1, MIN-005/Dataset D9), which lacked the check before this spec.**
- **FR-VA-007**: Any filename proposed for a view write MUST pass `pathsafe`'s component/name-shape
  validation before any filesystem write (D-SEC, Dataset D7).
- **FR-VA-008**: The `.base` import/re-derivation pipeline MUST write each newly-declared imported
  view's file in the same directory as the `.base` file it was imported from, with `derived_from`
  set to that `.base`'s own collection-relative path (US-2 AS-1, **`derived_from` clause new in
  round 1, D-PROVENANCE**).
- **FR-VA-008a *(new in round 1, MAJ-001/D-PROVENANCE)***: Re-derivation of a `.base` file MUST
  locate its currently-managed views by matching `ViewDef.derived_from` through the discovery walk
  (FR-VA-001), never by reconstructing a fixed slug path — so a managed view that has been moved
  is still found, rewritten in place, or deleted in place (Dataset F-3/F-4).
- **FR-VA-008b *(new in round 1, MIN-009)***: A re-derivation delete step MUST report a view as
  deleted only when the removal actually occurred; `os.ErrNotExist` MUST NOT be reported as a
  deletion (Dataset F-5).
- **FR-VA-008c *(new in round 1, FD-3)***: A hand-made view (no `derived_from`) MUST NOT be read,
  rewritten, or deleted by the re-derivation pipeline for any reason, including a `source` value
  that happens to name the `.base` being saved (Dataset F-6).
- **FR-VA-009**: `contracts/components/schemas/LibraryEntry.yaml` MUST gain `is_view` (boolean,
  optional), `view_kind` (string, optional, one of the generated `ViewDefKind` enum values),
  `view_label` (string, optional), `view_name` (string, optional, **new in round 1, D-ADDRESS**),
  `view_rejection` (string, optional, **new in round 1, D-VALIDATE**), and `view_derived_from`
  (string, optional, **new in round 1, D-PROVENANCE**), following the 5-step contract-regeneration
  process (Hard Constraint #8) — no hand-written parallel type.
- **FR-VA-009a *(new in round 1, D-PROVENANCE)***: `contracts/openapi.yaml::ViewDef` MUST gain
  `derived_from` (string, optional), settable only by the import/re-derivation writer; a
  `create_view`/`write_view` call supplying it MUST be refused.
- **FR-VA-010**: `is_view` MUST be true for, and only for, an entry whose extension is the one
  chosen in Q1 AND whose path is inside a knowledge base (**corrected in round 1, D-SCOPE/FD-1** —
  the original wording omitted the knowledge-base scope gate entirely, MAJ-005), independent of
  whether the file parses as a valid `ViewDef` (US-4 AS-3, Dataset C10).
- **FR-VA-011**: `view_kind` MUST be present only when the file both parses successfully and
  declares a `kind`; absent in every other case (never a fabricated or best-guess value) (EC-3,
  Dataset C9/C10).
- **FR-VA-011a *(new in round 1, D-ADDRESS)***: `view_name` MUST be present if and only if the
  file both is `is_view: true` and parses successfully; absent whenever `view_rejection` is
  present.
- **FR-VA-011b *(new in round 1, D-VALIDATE/CRIT-002)***: `view_rejection` MUST be present exactly
  when the file is `is_view: true` but broken, oversize, or duplicate-rejected, using one of the
  existing `RejectView*` codes extended with `view_too_large` — never a hand-written parallel
  code.
- **FR-VA-012**: The chosen view extension MUST NOT be treated as a hidden entry by
  `pkg/library/entries.go`'s dot-prefix rule (F2, mirrors `.base`'s existing treatment) — this does
  NOT apply to a file named exactly `.view` with no stem (EC-9, **new in round 1, MIN-010**), which
  remains a dotfile.
- **FR-VA-012a *(new in round 1, MIN-010)***: Extension matching for `.view` MUST be
  case-insensitive (`.VIEW` matches), and `.view` MUST be registered in
  `pkg/library/entries.go`'s content-type and text-editable tables.
- **FR-VA-013**: `classifyLibraryEntry` MUST return a new `'view'` member of `LibraryPreviewKind`
  for the chosen extension, added after the mime-driven `image`/`video` checks per the file's own
  ordering rule (F2, US-6 AS-1), gated on `is_view` (and therefore on D-SCOPE/FD-1).
- **FR-VA-014**: `LibraryPreviewPane`'s `renderBody` MUST gain a `case 'view':` that resolves the
  entry's enclosing knowledge base and `view_name` (D-ADDRESS, **corrected in round 1, MAJ-003** —
  the original wording had no addressing mechanism), calls the existing `GET .../knowledge/view`
  endpoint, and reuses `ViewPartsRenderer` to draw the returned rows inside the standard preview
  pane, never a modal (US-6 AS-2, F6).
- **FR-VA-014a *(new in round 1, MAJ-003)***: When `view_name` is absent (a broken or
  duplicate-rejected view), the preview MUST show `view_rejection`'s reason instead of attempting
  a query with no name to query by.
- **FR-VA-015**: The Library tree MUST render a distinct icon per `view_kind` value (8 states) plus
  one fallback state for an absent `view_kind`, and MUST NOT reuse the `.base` icon for a `.view`
  entry (US-5 AS-1/AS-2); each icon MUST carry an accessible text alternative naming the kind
  (**new in round 1, MIN-008**).
- **FR-VA-015a *(new in round 1, FD-2/FD-3)***: The tree MUST render a "duplicate/broken" badge
  when `view_rejection` is present, and a "derived" badge when `view_derived_from` is present,
  each visually distinct from the kind icon and from one another.
- **FR-VA-016**: A rename or move of a `.view` file through an ordinary Library file operation MUST
  NOT alter the file's `Def.Name` content, and the view MUST remain resolvable by that unchanged
  name after the move (Q3/B, EC-5) — for a HAND-MADE view this makes it fully independent; for a
  DERIVED view (**new in round 1, EC-5a/FD-3**) it does NOT detach it from re-derivation, which
  keeps finding it via `derived_from`.
- **FR-VA-016a *(new in round 1, MAJ-002/D-MOVE)***: `knowledge_restructure`'s rename/move MUST
  accept an existing `.view` path unchanged (not `.md`-suffixed by `ensureMarkdown`), mirroring
  `(*Trasher).trashSourcePath`'s existing exact-path-first carve-out.
- **FR-VA-016b *(new in round 1, MAJ-004/D-DELETE)***: `delete_view` MUST trash the file via the
  same `Trasher` every other agent-facing Library deletion uses, not hard-delete it.
- **FR-VA-017**: *(Deleted, MAJ-006 — was the orphan-directory startup WARN. D-Q4-NO-WARN drops
  it outright; no replacement requirement.)*
- **FR-VA-018**: Discovery performance over a fixture at the repo's standing 100,000-file scale
  assumption MUST be measured by a benchmark before this feature lands, and the resulting number
  MUST be recorded as an ABSOLUTE, CI-checkable upper bound (**corrected in round 1, MIN-003** —
  the original wording, "within the number the benchmark itself establishes," could not fail).
- **FR-VA-018a *(new in round 1, MIN-003)***: A single `write_view`/`create_view`/`delete_view`
  call MUST perform the discovery walk at most once, sharing one result across every check that
  call needs (name-refusal, dedup, before/after) — not re-walking per check as today's 13 call
  sites (F4) do.
- **FR-VA-019 *(new in round 1, FD-2/D-DUPLICATE)***: A copy, trash-restore, or upload of a
  `.view` file made through Omnipus's own Library operations, whose `name` would otherwise
  collide with an existing view, MUST have that `name` auto-suffixed to a unique value before the
  file lands.
- **FR-VA-020 *(new in round 1, FD-2/CRIT-002)***: A `.view` name collision arriving by any path
  OTHER than FR-VA-019's three operations MUST still reject both views (D-DEDUP unchanged) AND
  MUST surface `view_rejection: view_duplicate_name` on both `LibraryEntry` rows and name both
  colliding paths in `knowledge_describe`/`knowledge_find` output.
- **FR-VA-021 *(new in round 1, MAJ-010/D-VALIDATE)***: A Library raw-content save of a `.view`
  file MUST always land on disk (never refused for content-semantic reasons, matching the
  existing `.base`/markdown save-door posture, F16); the resulting parse/validation state MUST be
  surfaced via `view_rejection` at the next listing.
- **FR-VA-022 *(new in round 1, MIN-001/D-SYMLINK-READ)***: A file discovery already walked as a
  regular file MUST be re-verified as a regular, non-symlink file immediately before its content
  is read; a file that is no longer a plain regular file at read time MUST be refused, not read.
- **FR-VA-023 *(new in round 1, MIN-002/D-SIZECAP)***: A `.view` file over 256 KiB MUST be
  reported `view_too_large` and MUST NOT have its content read past that cap by any discovery,
  listing, or write-time validation path.
- **FR-VA-024 *(new in round 1, MIN-007/D-LOCK)***: A Library save of a `.view` file and an
  agent's `write_view`/`create_view`/`delete_view` on the SAME file MUST resolve to an identical
  lock key (`controlPlaneLockKey` == the Library save door's derived key) for that collection-
  relative path, so the two paths always serialize through `WithNoteWriteLock`.
- **FR-VA-025 *(new in round 1, MAJ-009)***: A `WalkContained` `SkipUnreadable` entry encountered
  during view discovery MUST be surfaced in the resulting view report (naming the skipped
  subfolder), never silently dropped from the view count with no signal.
- **FR-VA-026 *(new in round 1, OBS-003)***: `knowledge_describe`'s VIEWS section and
  `knowledge_configure`'s write results MUST state each view's collection-relative path
  (`SourcePath`, F13) — not merely its name/label — now that location varies per view.

---

## 13. Success Criteria

- **SC-VA-001**: A view file placed anywhere inside a knowledge base (any depth, not the former
  fixed directory) is found by `knowledge_describe`, `knowledge_find`, and the Library entries
  endpoint, with zero loss of reach compared to today's fixed-directory behavior (F5's "agents
  must do everything" rule) — verified by Dataset A rows A1–A3/A9 all passing and F4's 9
  `ViewsDir(` sites / 13 `LoadViews` call sites (**corrected counts, round 1**) each having a
  passing test exercising their post-change behavior.
- **SC-VA-002**: Zero of F4's 9 `ViewsDir(` sites and zero of its 13 `LoadViews` call sites still
  reference either deleted symbol after this feature lands (**corrected in round 1, MIN-006** —
  the original count was "8 call sites"), verified by a Grep/GitNexus re-sweep at CHECK time, per
  §1a's instruction to the implementing lead — and `delete_view` (F5a) is included in that sweep.
- **SC-VA-003**: A write whose destination would escape the collection root (via traversal or a
  symlink) is refused in 100% of Dataset D's D5/D6 cases (**corrected row references, round 1**),
  with zero bytes written in each refused case (verified by a filesystem-state assertion in the
  test, not just a returned error).
- **SC-VA-004**: Discovery over the 100,000-file benchmark fixture (FR-VA-018) completes within an
  ABSOLUTE bound fixed before landing and checked by CI (**corrected in round 1, MIN-003** — the
  original criterion could not fail as written).
- **SC-VA-005**: Every one of the 8 `ViewDefKind` values, plus the no-`kind` fallback, renders a
  visually distinct icon in the Library tree, each with an accessible text alternative (Dataset C,
  9/9 states covered by test 15, accessibility per MIN-008).
- **SC-VA-006**: `make verify-contracts` is clean after the `LibraryEntry`/`ViewDef`/
  `KnowledgeBaseView` schema changes and regeneration (Hard Constraint #8) — zero hand-written
  types in `pkg/api/generated/` or `src/lib/api/generated/` implementing any of the six new
  `LibraryEntry` fields or `ViewDef.derived_from` (**expanded scope, round 1**).
- **SC-VA-007**: *(Deleted, MAJ-006 — was the legacy-directory WARN criterion.)*
- **SC-VA-008 *(new in round 1, CRIT-001)***: 100% of Dataset F's write-identity cases (F-1, F-2)
  behave as specified — an upsert never creates a second file for an existing name, and a create
  never overwrites an occupied default path — verified by filesystem-state assertions.
- **SC-VA-009 *(new in round 1, FD-2/CRIT-002)***: 100% of Dataset G's cases behave as specified —
  every Omnipus-mediated collision (G-1/G-2/G-3) is auto-renamed with zero rejections, and every
  other collision (G-4/G-5) is visibly flagged on both files, not silently dropped.
- **SC-VA-010 *(new in round 1, MAJ-001/FD-3)***: 100% of Dataset F's provenance cases (F-3–F-7)
  behave as specified, including that a hand-made view with a coincidentally-matching `source` is
  never touched by re-derivation.

---

## 14. Reachability

Per CLAUDE.md's Definition of Done: green tests show correctness, not reachability. Each surface
below states who/what can actually invoke the finished feature.

| Surface | Reachable how | Registration/wiring evidence required at CHECK |
|---|---|---|
| `knowledge_describe` | Agent tool call, VIEWS section, unchanged invocation shape | Already registered for every agent per its existing policy entry (unchanged by this spec — no new tool, no new policy row needed); verify `renderViews` output includes a view planted outside the old fixed directory, states its path (FR-VA-026), and names both files of a duplicate collision (FR-VA-020). |
| `knowledge_find` | Agent tool call, view-by-name resolution, unchanged invocation shape | Same — verify `ViewFindLoader.ServeRefusal`/serve path resolves a view planted anywhere in a knowledge base, and names a duplicate collision's other file. |
| `knowledge_configure` (`write_view`, `create_view`, `delete_view`) | Agent tool call, unchanged required-argument shape (Q5/B keeps it zero-argument for destination) | Verify a call with no destination argument succeeds and the resulting file is later discoverable; verify an upsert of an existing name targets `SourcePath` (D-WRITE-IDENTITY); verify `delete_view` produces a restorable trash entry (**new in round 1, MAJ-004**), not a hard delete. |
| `knowledge_restructure` rename/move on a `.view` path *(new in round 1, MAJ-002)* | Agent tool call, existing rename/move op | Verify the `.view` path itself moves, not a `.md`-suffixed miss — a tool call that silently targets the wrong file is not reachable. |
| `.base` import/re-derivation pipeline | Existing import flow plus the per-save re-derivation trigger from the Library save door (**corrected in round 1, MAJ-001** — not one-shot) | Verify a newly-imported view is discoverable exactly as an agent-written view would be, AND that a moved managed view is still found and correctly rewritten/deleted on the next `.base` save. |
| `GET /api/v1/library/{workspace_id}/entries` | Existing REST endpoint, no new route | Verify all six new fields appear on the wire for a real listing, not just in a Go struct — `make verify-contracts` clean plus an integration test hitting the actual HTTP handler, including one row inside and one outside a knowledge base (D-SCOPE). |
| Library preview pane `'view'` case, addressed by `view_name` *(corrected in round 1, MAJ-003)* | SPA component, mounted from the existing click-to-open flow every other kind already uses | A real click (or an equivalent Playwright/webapp-testing check) opening a `.view` entry, resolving its enclosing knowledge base, calling the existing view-evaluation endpoint, and rendering rows via `ViewPartsRenderer` — not merely that the `case 'view':` branch compiles, and not merely that `view_name` exists in a fixture. |
| Library tree icon, duplicate badge, derived badge | SPA component, mounted wherever the Library tree already renders (no new route/screen) | A real (or storybook/snapshot) render showing a `.view` entry with its kind-specific icon (with accessible name, MIN-008), AND a separate render showing the duplicate/derived badges — a backend field nobody reads in the tree is a library, not a feature (CLAUDE.md's own example of this exact failure mode). |
| Library copy/restore/upload auto-rename *(new in round 1, FD-2)* | Existing Library operations (`handleLibraryTransfer`, `(*Trasher).Restore`, `handleLibraryUpload`) | Verify, through the actual HTTP handlers, that a colliding copy/restore/upload lands with a suffixed `name` and that both views are independently resolvable afterward — not merely that a rename function exists and is unit-tested in isolation. |

**Two lines, never merged, per surface above**: "code correct and tested" (TDD plan §10 passing)
and "reachable by a user or agent" (this table's right-hand column, checked against the ACTUAL
registration/wiring, not the test suite).

---

## 15. Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-VA-001 | US-1 | US-1 AS-1, AS-2 | 1, 3 |
| FR-VA-002 | US-1, US-9 | EC-2, EC-2a | 33 |
| FR-VA-003 | US-1 | US-1 AS-3 | 2 |
| FR-VA-004 | US-1 | (F7, unchanged) | 3 |
| FR-VA-005 | US-1 | US-1 AS-4 | 4, 19 |
| FR-VA-005a | US-1 | US-1 AS-6 | 20 |
| FR-VA-006 | US-3 | US-3 AS-1 | 7, 34, 9 (D9) |
| FR-VA-007 | US-3 | US-3 AS-2 | 8 |
| FR-VA-008 | US-2 | US-2 AS-1 | 5 |
| FR-VA-008a | US-2 | US-2 AS-3 | 22 |
| FR-VA-008b | US-2 | US-2 AS-5 | 24 |
| FR-VA-008c | US-2 | US-2 AS-7 | 25 |
| FR-VA-009 | US-4 | US-4 AS-1, AS-6 | 10 |
| FR-VA-009a | US-2 | (D-PROVENANCE non-behavior) | 26 |
| FR-VA-010 | US-1, US-4 | US-1 AS-7, US-4 AS-4 | 21 |
| FR-VA-011 | US-4 | US-4 AS-2 | 11 |
| FR-VA-011a | US-6 | US-6 AS-4 | 14 |
| FR-VA-011b | US-4 | US-4 AS-3, AS-5, AS-7 | 12, 30, 36 |
| FR-VA-012 | US-1 | (F2 parity, EC-9) | 40, 43 |
| FR-VA-012a | US-6 | (MIN-010) | 42 |
| FR-VA-013 | US-6 | US-6 AS-1 | 13 |
| FR-VA-014 | US-6 | US-6 AS-2 | 14 |
| FR-VA-014a | US-6 | US-6 AS-4 | 14 |
| FR-VA-015 | US-5 | US-5 AS-1, AS-2, AS-3 | 15, 16 |
| FR-VA-015a | US-5 | US-5 AS-4, AS-5 | 15, 16 |
| FR-VA-016 | US-1, US-2 | EC-5, EC-5a | 41 |
| FR-VA-016a | US-8 | US-8 AS-1 | 32 |
| FR-VA-016b | US-7 | US-7 AS-1 | 31 |
| FR-VA-017 | *(deleted, MAJ-006)* | — | — |
| FR-VA-018 | — (cross-cutting, Q2) | Dataset A (scale) | 17 |
| FR-VA-018a | — (cross-cutting, MIN-003) | — | (implementation-level refactor test, named at RED time) |
| FR-VA-019 | US-9 | US-9 AS-3 | 27, 28, 29 |
| FR-VA-020 | US-4, US-9 | US-4 AS-5, US-9 | 30 |
| FR-VA-021 | US-4 | US-4 AS-3 | 39 |
| FR-VA-022 | US-3 | US-3 AS-4 | 35 |
| FR-VA-023 | US-3, US-4 | US-3 AS-5, US-4 AS-7 | 36 |
| FR-VA-024 | US-3 | US-3 AS-6 | 37 |
| FR-VA-025 | — (cross-cutting, MAJ-009) | — | 38 |
| FR-VA-026 | — (cross-cutting, OBS-003) | — | (prompt-text update by `prometheus-prompt-engineer`; assertion added to test 10's fixture) |

Every FR appears above. Remaining gaps between the TDD plan's numbered tests and a scenario are
flagged rather than silently left implicit, per "no false success": the implementing lead adds the
named test at RED time rather than this spec claiming full coverage it has not yet earned.

---

## 16. Holdout Evaluation Scenarios

Not referenced in the TDD plan or traceability matrix above — for post-implementation, external
verification only.

**Happy path:**
1. As a human, drag a `.view` file from one folder to another in the Library UI; confirm the view
   still answers to its original name from an agent chat, unprompted by any code you read.
2. As a human, open a calendar-kind view from the Library tree by clicking its calendar icon;
   confirm the preview pane (not a modal) shows the same rows `knowledge_find` would return for it.
3. As an agent (via chat), create a view with no destination specified; confirm, from the Library
   UI, that it appears at the collection root without being told where to look.
4. *(New in round 1, FD-2.)* As a human, copy an existing view file using the Library's copy
   action; confirm the original view keeps answering to agent chat unaffected, and the copy shows
   up as a separately-addressable view with a different name you never had to type yourself.

**Error:**
5. *(Renumbered from 4.)* As a human, manually create two `.view` files in different folders with
   the same `name:` inside them (NOT via copy/restore/upload); confirm both surface as visibly
   broken/rejected in the Library tree and preview themselves, not only in a log an agent reads.
6. *(Renumbered from 5.)* As an operator, attempt (via whatever the lowest-level write path
   exposes) to write a view outside the collection root; confirm the attempt visibly fails and no
   file appears outside the boundary on disk.
7. *(New in round 1, CRIT-001.)* As an agent, ask to update a view whose file has been moved
   elsewhere in the tree by a person since it was created; confirm, from the Library UI, that the
   update landed at the view's CURRENT location, and that no second file with the same name
   appeared anywhere else.

**Edge case:**
8. *(Renumbered from 6.)* As a human, open a `.view` file with no declared `kind` from the tree;
   confirm it still opens and renders correctly despite showing a generic icon.
9. *(New in round 1, FD-3.)* As a human, edit a view file that was imported from a `.base` file,
   then save that `.base` file again unchanged; confirm your edit is overwritten and the preview
   told you, before you saved the `.base`, that this view is derived and would be.
10. Holdout 7 from the original document (the orphan-directory WARN) is **deleted, MAJ-006** — no
    replacement holdout; D-Q4-NO-WARN means there is nothing external to verify here.

---

## 17. Ambiguity Self-Audit

| What's ambiguous | Likely agent assumption | Resolved by |
|---|---|---|
| Exact extension string | `.yaml` (wrong — collides with schemas, F9) | Q1 |
| Whether kind-tagging happens at listing time (cost) or lazily on open | Listing time, silently, no perf discussion | Q2 |
| Whether renaming a view file renames the view | Yes, silently, breaking agent references | Q3 |
| Whether old `.omnipus-vault/views/*.yaml` files get auto-migrated | Some agents would "helpfully" write a migrator despite the greenfield rule | Retired original Q4; now D-Q4-NO-WARN (explicitly prohibited either way) |
| Where a view lands with no destination given | Might invent same-folder-as-active-note heuristics | Q5 |
| Which of the three "kind" vocabularies (`kind`/`layout`/`part`) drives the icon | Could easily pick `layout` since it sounds closer to "how it's displayed" | D-ICON (§2), resolved without a founder question because the evidence (F8) is unambiguous once traced |
| **(New in round 1.)** How an upsert of an existing name locates the file to replace, once filename ≠ name | Reconstruct `<root>/<name>.view` and overwrite whatever is there (exactly CRIT-001's bug) | D-WRITE-IDENTITY |
| **(New in round 1.)** Whether a routine copy/restore/upload should ever silently break the original view | Assume dedup "just works" the same way it does for a hand-authored collision | FD-2/D-DUPLICATE (founder-decided, not the grill's own recommendation alone) |
| **(New in round 1.)** Whether editing a `.base`-derived view should survive that `.base`'s next save | Assume "like a note" means full independence once imported (the grill's OWN recommendation) | FD-3 (founder decision — explicitly the OPPOSITE of the grill's recommendation) |
| **(New in round 1.)** How the system tells a derived view from a hand-made one | Infer it from whether `source` is set (would break F1's "never re-read" invariant and mis-classify a hand-made view with a descriptive `source`) | D-PROVENANCE — persisted `derived_from`, never inferred |
| **(New in round 1.)** Whether a `.view` outside a knowledge base should still show a view icon | Show the icon regardless of extension, since "anywhere" was said without qualification | FD-1/D-SCOPE |

All eleven are either resolved by a founder question/decision above (Q1–Q5, FD-1–FD-4) or decided
with cited evidence (D-ICON, D-WRITE-IDENTITY, D-PROVENANCE). None are left as a silent
assumption.

---

## 18. Review round 1 dispositions

Every finding from `docs/internal/specs/library-views-anywhere-spec-spec-review.md` (2 CRITICAL,
10 MAJOR, 12 MINOR, 3 OBSERVATION — 27 total), with its disposition. "Fixed" means the spec now
specifies the fix (implementation follows at build time); "Founder-overridden" means a founder
decision took precedence over what the grill recommended; "Deferred" (none below) would mean left
open with a reason — every finding was actionable within this spec, so none were deferred.

| ID | Severity | Disposition | Where |
|---|---|---|---|
| CRIT-001 | Critical | Fixed | D-WRITE-IDENTITY (§2), §4 step 6, US-1 AS-5/AS-6, FR-VA-005/005a, Dataset D2/D3/F-1/F-2, SC-VA-008 |
| CRIT-002 | Critical | Founder-overridden (FD-2) — extends the grill's own recommended option B with an explicit visible-warning requirement the grill's B did not spell out | D-DUPLICATE (§2), §4 step 5, US-9, FR-VA-019/020, Dataset G, SC-VA-009 |
| MAJ-001 | Major | Founder-overridden (FD-3) — founder chose ".base wins" (option A), the OPPOSITE of the grill's recommended option B; MAJ-001's OWN recommendation (locate by discovery, not fixed path) is fixed as part of implementing FD-3 correctly | D-PROVENANCE (§2), §4 step 5, F15, US-2 (retitled), FR-VA-008a/008b/008c, Dataset F, SC-VA-010 |
| MAJ-002 | Major | Fixed | D-MOVE (§2), F18, US-8, FR-VA-016a, test 32 |
| MAJ-003 | Major | Fixed | D-ADDRESS (§2), US-6 (retitled)/AS-2/AS-4, FR-VA-014/014a, test 14 |
| MAJ-004 | Major | Fixed | D-DELETE (§2), F5a, US-7, FR-VA-016b, test 31 |
| MAJ-005 | Major | Founder-overridden/answered (FD-1) — the grill's own recommended option A is what the founder chose, so the fix implements the grill's recommendation directly | D-SCOPE (§2), §4 step 0, US-1 AS-7, US-4 AS-4, FR-VA-010, EC-10, Dataset A10 |
| MAJ-006 | Major | Fixed — original spec's own Q4 retired to firm decision, option A (no WARN) | D-Q4-NO-WARN (§2), §3 Q4 (retired), FR-VA-017 (deleted), SC-VA-007 (deleted), TDD test 18 (deleted), Dataset E (deleted), Holdout 7 (deleted) |
| MAJ-008 | Major | Fixed — this is the only finding that blocked the design from compiling at all | §4 header note and steps 1/3, F-cite of `pkg/records`'/`pkg/knowledge`'s real import graph, §5 symbol table |
| MAJ-009 | Major | Fixed | §4 step 2, FR-VA-025, test 38 |
| MAJ-010 | Major | Fixed | D-VALIDATE (§2), F16, US-4 AS-3, FR-VA-021, test 39 |
| MAJ-011 | Major | Fixed by citation to pre-existing, already-intentional behavior (D-PARITY) rather than a new rule — the underlying asymmetries (dot-folders, symlinked mounts) are shown to already be the project's standing behavior for every other kind of content (F17), so this spec documents and tests them rather than inventing a change that would also have altered non-view content | D-PARITY (§2), F17, EC-1/EC-2a, US-9 AS-1/AS-2, FR-VA-002, Dataset A9/A7 |
| MIN-001 | Minor | Fixed | D-SYMLINK-READ (§2), §4 step 1c, US-3 AS-4, FR-VA-022, test 35 |
| MIN-002 | Minor | Fixed | D-SIZECAP (§2), §4 step 1d, US-3 AS-5, US-4 AS-7, FR-VA-023, test 36, EC-8 |
| MIN-003 | Minor | Fixed | §3 Q2 (corrected), F12, FR-VA-018/018a, SC-VA-004 |
| MIN-004 | Minor | Fixed | §5 symbol table (`KnowledgeBaseView.yaml` etc. rows), FR-VA-009a — implementing lead executes the contract-regeneration 5-step process against the description rewrites the grill named |
| MIN-005 | Minor | Fixed | §3 Q5 (corrected), Dataset D rewritten, FR-VA-006, Dataset D9, F15 |
| MIN-006 | Minor | Fixed | F3/F4 corrected, F5a added, SC-VA-001/SC-VA-002 corrected counts |
| MIN-007 | Minor | Fixed | D-LOCK (folded into D-SEC, §2), F14, US-3 AS-6, FR-VA-024, test 37 |
| MIN-008 | Minor | Fixed | US-5 AS-3/AS-4/AS-5, US-6 AS-6, FR-VA-015/015a |
| MIN-009 | Minor | Fixed | §4 step 5, US-2 AS-5, FR-VA-008b, test 24, Dataset F-5 |
| MIN-010 | Minor | Fixed | §4 step 1b, FR-VA-012/012a, EC-9, Dataset A11/A12, tests 42/43 |
| MIN-011 | Minor | Fixed | §10 Regression item 2 (reworded) |
| MIN-012 | Minor | Fixed | §10 "BDD scenario blocks" (new subsection), test names 40/41 assigned for the two prior "add at implementation" gaps |
| OBS-001 | Observation | Fixed (adopted) | §4 step 3, F13, D-CONTRACT note, TDD regression item 4 |
| OBS-002 | Observation | Fixed (adopted) | Routing note (header), FD-4 |
| OBS-003 | Observation | Fixed | FR-VA-026, §5 symbol table (`KnowledgeBaseView.path` row) |

**Founder decisions that override a grill recommendation (for the record):** CRIT-002 (FD-2
extends, does not simply adopt, the grill's B), MAJ-001/FD-3 (founder chose the OPPOSITE of the
grill's recommended B), and MAJ-005/FD-1 (founder's choice happens to equal the grill's own
recommended A, so no actual override occurred there despite the "founder decision" framing).
Every other finding above was fixed on its own merits, independent of any founder input, because
none of them touched a design axis the founder needed to rule on.

---

*End of spec.*
