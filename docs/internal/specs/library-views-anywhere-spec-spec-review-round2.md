# Adversarial Review: Library views, anywhere — a saved view is a file like a note

**Document reviewed**: `docs/internal/specs/library-views-anywhere-spec.md` (branch `feat/library-views-anywhere`, commit `e88193d24`)
**Mode**: Spec
**Round**: Round 2 of 2 (final)
**Review date**: 2026-09-29
**Verdict**: BLOCK

## Executive Summary

3 CRITICAL, 7 MAJOR, 12 MINOR and 4 OBSERVATION findings. Round 1's fixes are real on the
read side: the import direction now compiles, the walk is the right primitive, and the
write-identity rule for `write_view`/`create_view` closes round-1 CRIT-001 for those two tools.
But three round-1 fixes marked "Fixed" rest on claims the code contradicts:

- the preview's knowledge-base lookup does not exist in the SPA (MAJ-003 not fixed);
- the agent move/rename fix still appends `.md` to the destination (MAJ-002 not fixed);
- the `view_kind` enum was never extracted (MIN-004 not fixed).

The new provenance field (`derived_from`, FD-3) was designed on a false premise: `source` is
**not** "never re-read". It is already the live provenance key for re-derivation, the `.base`
preview tabs and embeds. That field also leaks through every copy, hand edit and `write_view`.
The result is two new data-loss paths: re-derivation deleting a person's copy, and re-derivation
overwriting a hand-made view that sits beside the `.base`.

Frontend coverage is better than round 1 (states, badges, accessible names). It is still
thinner than the backend coverage: no catalogued components are named, and the preview's error
state has no wire field to render from.

All code claims were checked first-hand in the `views-anywhere` worktree at `e88193d24`, by
Read, Grep and `go list`. GitNexus was not used, because this worktree is not indexed.
Evidence is labelled Verified unless marked Inferred. A second, independent pass (the
grill-spec skill's own fork) ran over the same commit. Its findings were merged here after
first-hand re-verification and are marked "(second pass)".

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 7 |
| MINOR | 12 |
| OBSERVATION | 4 |
| **Total** | **26** |

---

## Round-1 findings: status after the fix round

| Round-1 ID | Spec's disposition | Status verified in round 2 | Evidence / round-2 finding |
|---|---|---|---|
| CRIT-001 | Fixed | **Mostly fixed.** Covered for `write_view`/`create_view`. The same overwrite class survives in the import/re-derivation writer. A move between lookup and write can still recreate a twin | R2-CRIT-002, R2-MIN-002 |
| CRIT-002 | Founder-overridden (FD-2) | **Partly fixed.** Folder copy and folder restore are not covered. The cited "Library restore door" does not exist. Copies of derived views are deleted | R2-MAJ-007, R2-CRIT-001 |
| MAJ-001 | Founder-overridden (FD-3) | **Partly fixed.** Locating by provenance is specified. The premise (`source` is never re-read) is false. The `.base` lifecycle and the provenance of edited views are unspecified | R2-MAJ-001, R2-MAJ-002, R2-MAJ-003 |
| MAJ-002 | Fixed | **Not fixed.** `execRenameMove` still runs `to = ensureMarkdown(to)`, and D-MOVE's carve-out only covers an *existing* path, which a destination never is | R2-MAJ-004 |
| MAJ-003 | Fixed | **Not fixed.** The SPA does not resolve the knowledge base. `collection_id` is opaque and comes only from a `.base`-only endpoint | R2-CRIT-003, R2-MAJ-005 |
| MAJ-004 | Fixed | **Fixed** for `delete_view`. Re-derivation deletes are still a bare `os.Remove` (R2-CRIT-001) | `knowledge_configure.go::execDeleteView` |
| MAJ-005 | Answered (FD-1) | **Fixed** | D-SCOPE, FR-VA-010 |
| MAJ-006 | Fixed | **Fixed.** Stale text is left in §3 Q4 | R2-MIN-006 |
| MAJ-008 | Fixed | **Fixed, verified.** `go list`: `pkg/records` imports only `pkg/api/generated`; `pkg/vaultimport` and `pkg/vaultprops` already import `pkg/knowledge` | R2-MIN-011 (annotation site) |
| MAJ-009 | Fixed | **Partly fixed.** The skipped-folder note is required only in `knowledge_describe`/`knowledge_configure`, not in `knowledge_find`, the listing or the REST view lists | R2-MIN-007 |
| MAJ-010 | Fixed | **Intent fixed.** The rejection reason has no wire field to reach the preview | R2-MAJ-005 |
| MAJ-011 | Fixed (D-PARITY) | **Fixed** | F17, Dataset A7/A9 |
| MIN-001 | Fixed | **Not closed as specified.** `Lstat` then `ReadFile` still races, and the two steps now sit in different packages | R2-MIN-001 |
| MIN-002 | Fixed | **Partly fixed.** No bounded-read mechanism: a size check followed by `ReadFile` reads a file that grew after the check | R2-MIN-001 |
| MIN-003 | Fixed | **Partly fixed.** The absolute bound has no CI gate to run in | R2-MIN-004 |
| MIN-004 | Fixed | **Not fixed.** No `ViewKind` extraction. `ViewDef.kind` is still inline in `openapi.yaml`. The `ViewDef.description`/`source` text rewrites are missing from the list | R2-MAJ-005, R2-MIN-010 |
| MIN-005 | Fixed | **Fixed** (the D9 containment check). The occupied-path case for the same writer is open | R2-CRIT-002 |
| MIN-006 | Fixed | **Fixed, verified.** 9 `ViewsDir(` sites in 7 files; 13 `LoadViews(` sites in 10 files. Missed: `pkg/vaultimport/view_write.go::TranslateBase` (`ProducedView.RelPath`) | R2-MIN-010 |
| MIN-007 | Fixed | **Partly fixed.** Library save vs agent write is covered. Re-derivation writes and deletes take no lock on the view file | R2-MIN-002 |
| MIN-008 | Fixed | **Partly fixed.** States and accessible names added. No catalogued component named (the catalog has `badge`, `tooltip`, `error-state`, `empty-state`, `skeleton`, `collection-state`) | R2-MIN-009 |
| MIN-009 | Fixed | **Fixed** | FR-VA-008b |
| MIN-010 | Fixed | **Partly fixed.** FR-VA-012 is still a no-op, and test 40 cannot fail | R2-MIN-003 |
| MIN-011 | Fixed | **Fixed** | §10 regression item 2 |
| MIN-012 | Fixed | **Partly fixed.** 3 BDD blocks added; several acceptance scenarios still have no named assertion | R2-MIN-007 |
| OBS-001 | Adopted | **Fixed** | F13 |
| OBS-002 | Adopted | **Fixed** | Routing note, FD-4 |
| OBS-003 | Fixed | **Fixed** in intent (FR-VA-026). Its test mapping is wrong | R2-MIN-007 |

Note: round 1 has no MAJ-007 (the numbering skips it). Its "10 MAJOR" count and §18's ten
MAJOR rows are both correct.

---

## Findings

### CRITICAL Findings

#### [R2-CRIT-001] Re-derivation hard-deletes a person's copy of a derived view, and anyone can hand it a delete target

- **Lens**: Security (tampering / availability) / Incompleteness
- **Affected section**: D-PROVENANCE, D-DUPLICATE, §4 step 5, FR-VA-008a, FR-VA-019, EC-5a, Dataset G
- **Failure scenario**:
  1. A person copies `projects/roadmap-open.view`, a view derived from `projects/roadmap.base`,
     through the Library's copy action to make a variant ("copy then tweak", the reason round-1
     CRIT-002 was raised).
  2. D-DUPLICATE rewrites only `name:` (and `label:`), so the copy is saved as
     `roadmap-open-2` with `derived_from: projects/roadmap.base` still set.
  3. The next save of `roadmap.base` runs re-derivation. §4 step 5 selects every view whose
     `derived_from` matches, finds the copy, sees that `roadmap-open-2` is not declared, and
     **deletes it**. The deletion is a bare `os.Remove`: no trash, no audit record.

  The same field is also a delete lever for anyone who can edit a file:
  - Adding `derived_from: X.base` to someone else's hand-made view marks it for deletion on the
    next `X.base` save. The Library editor never refuses a save (D-VALIDATE/F16), and `bash` or
    `write_file` bypass every rule.
  - A sync client's conflict copy produces two files with the same `derived_from` *and* the
    same `name`. "Rewrite in place" is then undefined for two targets.

  "Set only by the pipeline" (D-PROVENANCE, FR-VA-009a) cannot be enforced for a field stored
  inside a user-editable file. Only the two agent tools are gated.
- **Evidence**: `pkg/vaultimport/rederive.go::fileTranslatedBase` (the delete loop over `mine`:
  `os.Remove(delPath)` with no `Trasher`, no `AuthorAuditRecord`). D-DUPLICATE (§2) names only
  `name:`/`label:` as rewritten. D-VALIDATE: "The write always lands." (Verified)
- **Recommendation**:
  - Omnipus copy, restore and upload strip `derived_from`: a copy is always hand-made.
  - Re-derivation deletes go through `(*Trasher).Trash`, with an audit record, the same as
    D-DELETE.
  - When more than one file claims the same `derived_from` + `name`, re-derivation touches
    none of them and flags them visibly (the D-DUPLICATE badge).
  - State the tamper model plainly: provenance inside a user-editable file is advisory, and a
    hand-added `derived_from` is honoured (or refused at listing with a rejection code). Pick
    one and test it.
  - Add Dataset F rows and tests for each case.

#### [R2-CRIT-002] Import and re-derivation overwrite whatever file sits at the target path beside the `.base`

- **Lens**: Incompleteness / Inconsistency (CRIT-001 class, second writer)
- **Affected section**: D-WRITE-IDENTITY, §4 step 5 ("a newly-declared view is written beside the `.base` file"), FR-VA-008, FR-VA-008c, Dataset D4
- **Failure scenario**: Q3/B decouples the filename from `name`. A person keeps a hand-made view
  named `pipeline` in the file `projects/roadmap-open.view`. They then add a view "Roadmap open"
  to `projects/roadmap.base` and save it. Re-derivation mints the slug `roadmap-open`, which is
  free, because the `SlugRegistry` reserves view *names*, not filenames. It writes
  `projects/roadmap-open.view` with `fileutil.WriteFileAtomic`, which replaces the file. The
  hand-made view is destroyed without a report. This breaks FR-VA-008c ("a hand-made view MUST
  NOT be … rewritten … for any reason"). The same applies to an unrelated or malformed file
  that happens to sit at that path. D-WRITE-IDENTITY's refuse-on-occupied rule covers only
  `create_view`/`write_view`.
- **Evidence**: `pkg/vaultimport/rederive.go::fileTranslatedBase` (the write loop: `ReadFile`
  comparison, then `WriteFileAtomic(path, …)`, with no "is this path one of mine" check);
  `rederive.go::RederiveBase` (seeds `slugs.Reserve(v.Name())`, which covers names only);
  `pkg/vaultimport/run.go::writeAndReloadViews` (bare `WriteFileAtomic`). (Verified)
- **Recommendation**: add FR-VA-008d. The pipeline writes a new view only onto a free path.
  When `<dir>/<slug>.view` is occupied by anything that is not this `.base`'s own managed file,
  it picks the next free suffixed filename (`<slug>-2.view`) and never overwrites. It rewrites
  in place only the file whose `derived_from` matches. Add Dataset D10/F-8 rows.

#### [R2-CRIT-003] The Library preview cannot open a view: nothing gives the SPA the `collection_id` (round-1 MAJ-003 not fixed)

- **Lens**: Reachability / Contract-first gaps
- **Affected section**: D-ADDRESS, US-6 AS-2, FR-VA-014, §14 "Library preview pane" row
- **Failure scenario**: a person clicks `projects/open.view`. `GET .../knowledge/view` needs a
  `collection_id`. D-ADDRESS says the SPA "already resolves the nearest enclosing knowledge base
  for a `.base` entry" and reuses that. It does not. `BasePreview` reads `collection_id` from the
  server's `GET /knowledge/base-views?path=<.base>` answer, and that endpoint refuses any path
  that is not a `.base`. `collection_id` is opaque ("never parse a path out of it"), so the SPA
  cannot derive it from the tree's `is_knowledge_base` folders either. The payoff story (US-6)
  cannot be reached, and a GREEN-time "confirm" task cannot add the missing wire field without
  a contract change (Hard Constraint #8).
- **Evidence**: `src/components/library/preview/BasePreview.tsx` header ("The same answer
  carries the enclosing collection, so there is no ancestor walk here either") and
  `answer?.collection_id`; `pkg/gateway/rest_knowledge_base_views.go` (refuses a non-`.base`
  extension); `contracts/components/schemas/KnowledgeBaseViews.yaml::collection_id` ("Opaque —
  never parse a path out of it"). (Verified)
- **Recommendation**: add `view_collection_id` (or one nested `view` object, R2-OBS-001) to
  `LibraryEntry` in contract step 1. The gateway already knows the enclosing collection while it
  annotates the listing, through `enclosingCollectionRel`. Alternatively, define a
  path-addressed evaluation endpoint. Either way, write it into D-CONTRACT/FR-VA-009 and remove
  the Inferred GREEN task.

---

### MAJOR Findings

#### [R2-MAJ-001] FD-3's mechanism rests on a false premise: `source` is already the live provenance key

- **Lens**: Inconsistency with AS-IS
- **Affected section**: FD-3 rationale ("F1 already documents `source` as descriptive and 'never re-read'"), F1, D-PROVENANCE, FR-VA-008c, US-2 AS-7, §10 regression item 1
- **Failure scenario**: `source` is read today by three consumers:
  - re-derivation, which picks the views it manages with `DeclaredSource() == baseRelPath`;
  - the `.base` preview's tabs and unloadable counts;
  - `![[X.base#View]]` embeds.

  `create_view`/`write_view` take a `source` argument, and `ensureStarterBaseFile` **creates a
  starter `.base` that declares the agent's view by label**. After this spec, re-derivation keys
  on `derived_from` while the preview and embeds still key on `source`. Walk through the
  existing agent flow:
  1. `create_view view=weekly label="Weekly" source=projects/roadmap.base` writes a hand-made
     view (no `derived_from`) and a starter `roadmap.base` that declares "Weekly".
  2. The first Library save of that `.base` re-derives. It finds no managed views, translates
     "Weekly", sees the name `weekly` reserved, and writes `weekly-2` with label "Weekly" beside
     the `.base`, marked derived.
  3. Two views now share the label "Weekly" and the same `source`. The `.base` preview shows two
     tabs, and the embed `![[roadmap.base#Weekly]]` becomes ambiguous.

  Today, step 2 pins the agent's view instead of minting a twin. FR-VA-008c is therefore a
  behaviour change the spec does not acknowledge, and §10's "BasePreview … unaffected" is
  untested.
- **Evidence**: `pkg/vaultimport/rederive.go::RederiveBase` (`if v.DeclaredSource() ==
  baseRelPath { slugs.Pin(...) }`); `pkg/gateway/rest_knowledge_base_views.go`
  (`if v.DeclaredSource() != source { continue }`); `pkg/records/view.go::commonDeclaredSource`;
  the comment above `pkg/knowledge/knowledge_configure_create_view.go::viewSourceArg` ("`Def.Source`
  is the ONLY thing that ties a saved view to a data file — the Library base preview and the
  `![[X.base#View]]` embed both list views by it"); `::ensureStarterBaseFile` (the `views:` block
  it writes). The "never re-read" text in `contracts/openapi.yaml::ViewDef.source` is stale
  documentation, and code wins. (Verified)
- **Recommendation**: correct F1. State which field drives each consumer: re-derivation,
  derived badge, `.base` preview tabs, embeds, rejection attribution
  (`ViewRejection.Source`). Settle the starter-`.base` flow. This is Founder Q1 below. It
  concerns FD-3's *mechanism*, not its decision (".base wins" and "hand-made stays independent"
  stand).

#### [R2-MAJ-002] Renaming, moving or deleting the `.base` itself is not specified

- **Lens**: Incompleteness (data lifecycle)
- **Affected section**: D-PROVENANCE, EC-5a, US-2, FD-3's "derived" badge/banner promise
- **Failure scenario**: a `.base` is an ordinary Library file.
  - **Rename or move** `projects/roadmap.base` → `archive/roadmap.base`: every derived view
    keeps `derived_from: projects/roadmap.base`. They still show "derived — overwritten on the
    next save of `projects/roadmap.base`", a file that no longer exists, so the promise is false.
    The next save of the moved `.base` finds zero managed views and mints fresh ones, with names
    suffixed against the orphans (`-2`) and labels duplicated. Embeds and preview tabs then key on
    a mix of old and new `source` values.
  - **Delete** the `.base`: its views stay "derived" forever, from nothing.
- **Evidence**: the spec has no rule for a `.base` rename, move or delete. Library renames of files
  inside a knowledge base go through `pkg/gateway/rest_library_knowledge_cascade.go` (the Renamer,
  which rewrites links only). (Verified that no provenance cascade exists; Inferred how the SPA
  renders it.)
- **Recommendation**: specify all three cases. This is Founder Q3 below. Add dataset rows and
  tests, including the orphan-plus-fresh-twin case.

#### [R2-MAJ-003] Editing a derived view (agent or person) silently drops its provenance

- **Lens**: Ambiguity / Inconsistency with FD-3
- **Affected section**: FD-3 ("any edit … through `write_view` — is overwritten the next time its `.base` is saved"), D-PROVENANCE ("never by `create_view`/`write_view`"), §4 step 6 ("Neither tool may set or clear `derived_from`"), FR-VA-009a, D-VALIDATE
- **Failure scenario**:
  - **Agent edit.** `write_view` replaces the whole file with the caller's `definition`. The caller
    may not supply `derived_from` (refused), so the rewritten file has none. The view silently
    becomes hand-made and escapes ".base wins", which contradicts FD-3's own sentence about
    `write_view` edits. The next `.base` save then writes a same-labelled twin (R2-MAJ-001, step 3).
  - **Person edit.** A person who deletes the `derived_from:` line in the Library editor gets the
    same result. The save is never refused.
- **Evidence**: `pkg/knowledge/knowledge_configure.go::execWriteView` builds the file from
  `defMap` alone (`marshalDefinition(defMap)` then `overwriteControlPlaneFile`) and never merges
  the existing file's fields. (Verified)
- **Recommendation**: state the rule. Either:
  - (a) `write_view` on a derived view carries the existing `derived_from` forward server-side,
    and a Library save that removes or changes it is flagged with a `view_rejection`; or
  - (b) derived views are read-only: `write_view`/`create_view` refuse them, and the preview
    offers no edit.

  Founder Q2 below, because the dispatch wording of FD-3 ("marked derived/read-only") and the
  spec's record of it ("edits are overwritten") differ.

#### [R2-MAJ-004] Agent rename/move still targets `.md` on the destination side (round-1 MAJ-002 not fixed) (second pass)

- **Lens**: Reachability
- **Affected section**: D-MOVE, FR-VA-016a, US-8, test 32
- **Failure scenario**: an agent calls `knowledge_restructure` to rename `roadmap.view` to
  `roadmap-q3.view`. D-MOVE skips `ensureMarkdown` only when the path "is an existing regular
  file". `from` qualifies; the destination never exists yet, so `to = ensureMarkdown(to)` turns
  it into `roadmap-q3.view.md`. The view file becomes a markdown note and drops out of discovery.
  A `new_name` given without an extension (for example `roadmap-q3`) is unspecified. Test 32
  proves only the source side.
- **Evidence**: `pkg/knowledge/knowledge_restructure.go::execRenameMove` runs `from =
  ensureMarkdown(from)`, and later, separately, `if !folder { to = ensureMarkdown(to) }`.
  (Verified)
- **Recommendation**: the destination inherits the source's decision. When `from` is a
  non-markdown file, `to` is used as given. An extension-less `new_name` keeps the source's
  extension. An extension change on a `.view` is refused. Add a rename test (`a.view` →
  `b.view`, asserting `b.view` exists and `b.view.md` does not) and a move test.

#### [R2-MAJ-005] Contract gaps: the rejection reason has no wire field, and two enums would be hand-written (MIN-004 not fixed)

- **Lens**: Contract-first gaps (Hard Constraint #8)
- **Affected section**: D-CONTRACT, FR-VA-009, FR-VA-011b, FR-VA-014a, US-6 AS-4, SC-VA-006
- **Failure scenario**: three separate gaps.
  1. **No reason on the wire.** US-6 AS-4 requires the preview to show the rejection *reason*,
     "naming the conflicting path for a duplicate, or the parse error". `LibraryEntry` gets only
     `view_rejection` (a code). `view_name` is absent for a rejected file, so
     `GET .../knowledge/view` cannot be used. No endpoint is named either. The preview has
     nothing to render.
  2. **Rejection codes are Go-only.** `ViewRejectionCode` is a Go type. On the wire today it is a
     free string (`KnowledgeBaseUnloadableView.code`). The SPA must pick between the duplicate
     badge and the broken badge by comparing against `"view_duplicate_name"`, a hand-written
     wire literal.
  3. **`view_kind` has no enum to reference.** `view_kind` is "one of the generated
     `ViewDefKind` values", but `ViewDef.kind`'s enum is inline in `openapi.yaml`, so an external
     `LibraryEntry.yaml` cannot `$ref` it. Round-1 MIN-004's extraction step is absent. §18 marks
     it "Fixed".
- **Evidence**: `contracts/openapi.yaml` (`ViewDef` → `kind:` inline enum with
  `x-enum-varnames`); `contracts/components/schemas/KnowledgeBaseUnloadableView.yaml::code`
  (`type: string`, no enum); `pkg/records/view.go::RejectViewDuplicateName`;
  `ls contracts/components/schemas | grep -i viewkind` returns nothing. (Verified)
- **Recommendation**: make contract step 1 explicit:
  - (a) extract `ViewKind.yaml`, referenced from `ViewDef.kind` and `LibraryEntry.view_kind`,
    keeping the `x-enum-varnames` so the `ViewDefKind*` constants re-exported by
    `pkg/knowledge/view_kinds.go` survive or are renamed on purpose;
  - (b) add a `ViewRejectionCode.yaml` enum including `view_too_large`;
  - (c) add `view_rejection_reason` and `view_conflict_paths` (or the nested object of R2-OBS-001).

  Then regenerate (the 5-step process).

#### [R2-MAJ-006] Every folder listing inside a knowledge base needs a whole-collection walk

- **Lens**: Infeasibility (performance) / Security (denial of service)
- **Affected section**: D-CONTRACT (`view_rejection` "computed once per directory entry during listing"), US-4 AS-5, FR-VA-020, Q2, SC-VA-004
- **Failure scenario**:
  - **Walk per expansion.** `view_rejection: view_duplicate_name` on one entry needs vault-wide
    name dedup. The schema-dependent codes (`view_unknown_type`, `view_unknown_property`, …)
    need `LoadSchemas`. So every Library folder expansion containing a `.view` pays a full
    `WalkContained` walk, a parse of every view, and a schema load. The tree expands folders
    lazily, and a page load can expand many. At the spec's own 100,000-file scale that is one
    full walk per expansion.
  - **Read cost per listing.** A folder holding 2,000 `.view` files at the 256 KiB cap is up to
    ~500 MB read per listing.
  - **Unbounded latency.** SC-VA-004 and FR-VA-018a bound only "discovery" and single write
    calls, so listing latency has no bound at all.
- **Evidence**: the `is_knowledge_base` precedent it is modelled on is a per-entry marker check
  (`pkg/gateway/rest_library.go::annotateKnowledgeBaseEntries` → `detectKnowledgeBaseInRoot`),
  with no collection walk. (Verified for the precedent; the listing cost is Inferred from the
  spec's own requirements.)
- **Recommendation**: set a per-listing latency budget at the 100,000-file scale, or limit the
  listing to per-file facts (kind, label, name, parse errors, size) and compute the duplicate
  and schema state on preview open. Put an aggregate read cap per listing. Evaluate reusing
  `pkg/knowledge/manifest.go`'s file list now, not in a later change.

#### [R2-MAJ-007] FD-2's auto-rename misses folder copy and folder restore, and cites a Library restore door that does not exist (second pass, extended)

- **Lens**: Incompleteness / Reachability
- **Affected section**: D-DUPLICATE, §5 row "`handleLibraryTransfer` (copy mode), `(*Trasher).Restore`, `handleLibraryUpload`", US-7 AS-2, §14 last row, tests 27–29
- **Failure scenario**:
  - **Folder copy.** `library.CopyInto` copies a folder recursively. Duplicating a template
    folder that holds N views produces N collisions. The spec adds the rename step only "when
    the file being written … is `.view`-extension", at the handler level, so every original and
    every copy is switched off. That is exactly the FD-2 case the founder ruled must never be
    seen.
  - **Folder restore.** `(*Trasher).restoreFolder` is likewise uncovered.
  - **No Library restore door.** `(*Trasher).Restore` is reached only by the agent tool
    (`knowledge_restructure`'s restore op). It is not a Library door, and it does not live in
    `rest_library_write.go`. §14's "verify, through the actual HTTP handlers, that a colliding
    … restore lands" and test 28 `TestLibraryRestore_…` describe a surface that does not exist.
- **Evidence**: `pkg/library/transfer.go::CopyInto`;
  `pkg/knowledge/knowledge_restructure_trash_folder.go::restoreFolder`;
  `pkg/knowledge/knowledge_restructure_trash.go::(*Trasher).Restore`, whose only production
  caller is `pkg/knowledge/knowledge_restructure.go`. `grep` finds no restore route in
  `pkg/gateway/rest_library*.go`. (Verified)
- **Recommendation**:
  - Apply the rename per file inside recursive copy and restore. It must live in a package both
    `pkg/library` callers and `pkg/knowledge` can use: `pkg/records` is importable by both
    without a cycle.
  - Fix the §5 citation.
  - Re-point §14 and test 28 at the agent restore op.
  - Name every Omnipus door that writes a file into a knowledge base, and say which ones FD-2
    covers.

---

### MINOR Findings

#### [R2-MIN-001] The symlink-safe, size-capped read is split across two packages and still races

- **Lens**: Security
- **Affected section**: D-SYMLINK-READ, D-SIZECAP, §4 steps 1c/1d/3, FR-VA-022, FR-VA-023
- **Failure scenario**:
  - **The race stays open.** Step 1c `Lstat`s in the `pkg/knowledge` helper. Step 3's
    `records.LoadViewPaths` then does "exactly what today's parser does per file", which is
    `os.ReadFile`, in `pkg/records`, after the whole path list is built. The window MIN-001
    wanted closed now spans the full loop, and `Lstat`-then-`ReadFile` cannot close it even when
    adjacent.
  - **The size cap is not enforced by the read.** A size check followed by `ReadFile` reads a
    file that grew past 256 KiB after the check.
- **Evidence**: `pkg/records/view.go::loadViewPaths` (`os.ReadFile(p)`); `pkg/records` cannot
  import `pkg/knowledge` (MAJ-008). (Verified)
- **Recommendation**: the `pkg/knowledge` helper opens each file with no-follow semantics
  (`os.Root`, or open then `Fstat` compared against the walk's `Lstat`), reads through
  `io.LimitReader(cap+1)`, and hands `(path, bytes)` pairs to a `records` parse function that
  does no I/O. State that the check must hold on Windows too, where there is no `O_NOFOLLOW`.

#### [R2-MIN-002] View-file writes race moves and re-derivation

- **Lens**: Incompleteness (concurrency)
- **Affected section**: D-WRITE-IDENTITY, FR-VA-024, §4 step 5
- **Failure scenario**:
  - **Upsert vs move.** `write_view` finds `SourcePath`, then takes the lock and writes. A
    Library move in between makes the atomic write recreate the file at the old path: a
    same-name pair, both switched off.
  - **Re-derivation vs agent write.** Re-derivation runs *after* the Library save door has
    released its lock on the `.base`, and takes no lock on the view files it rewrites or
    deletes. A concurrent `write_view` or Library save of the same view can be lost or
    resurrected.
- **Evidence**: `pkg/gateway/rest_library_write.go::handleLibraryContentPut` (calls
  `rederiveBaseViewsAfterSave` after `WithNoteWriteLock` returns); `rederive.go` has no
  `WithNoteWriteLock`. (Verified)
- **Recommendation**: under the lock, re-check that the target still exists and still holds
  `name`, and refuse or retry otherwise. Re-derivation takes each view file's lock (the same
  key as FR-VA-024) for its write and delete. Add a test.

#### [R2-MIN-003] FR-VA-012 is vacuous, and test 40 is a false green (second pass)

- **Lens**: Testability
- **Affected section**: FR-VA-012, §9 data constraint 1, test 40
- **Failure scenario**: "hidden" is only a leading dot. `.base` has no carve-out, and `x.view`
  can never be hidden, so test 40 passes with no code change.
- **Evidence**: `pkg/library/entries.go::listDir` (`hidden := strings.HasPrefix(de.Name(), ".")`).
  (Verified)
- **Recommendation**: delete FR-VA-012 and test 40, or restate them as what really needs
  asserting: the content-type and text-editable table entries (FR-VA-012a).

#### [R2-MIN-004] SC-VA-004's "absolute CI-checked bound" has nowhere to run

- **Lens**: Testability
- **Affected section**: FR-VA-018, SC-VA-004, test 17
- **Failure scenario**: no CI gate runs Go benchmarks. The `go` cluster tier runs build, vet,
  lint, test and race. A number "checked by CI" is therefore checked by nothing, and on shared
  runners timing noise makes a hard bound flaky.
- **Evidence**: root `CLAUDE.md` tier table (`go` = `gofmt go-build go-vet lint go-test go-race`).
  (Verified)
- **Recommendation**: name the gate or tier, how the 100,000-file fixture is generated, and the
  tolerance. Or make it a deterministic budget test, such as a count of walks and file reads.

#### [R2-MIN-005] `write_view`/`delete_view` cannot repair a duplicate-rejected name

- **Lens**: Incompleteness
- **Affected section**: D-WRITE-IDENTITY, FR-VA-020
- **Failure scenario**: views `x` in `a/x.view` and `b/x.view` are both rejected, so neither is in
  the `ViewSet`.
  - `write_view view=x` takes the create branch, writes a third `<root>/x.view`, and reports it
    saved while all three stay rejected.
  - `delete_view view=x` answers "no view declared".

  FR-VA-020 names both files to agents but gives them no working repair.
- **Evidence**: `pkg/knowledge/knowledge_configure.go::execDeleteView` (`set.Resolve`);
  `pkg/records/view.go::loadViewPaths` (duplicate groups are rejected out of the set).
  (Verified)
- **Recommendation**: both tools check the rejection report for the name. `write_view` refuses
  and names the colliding paths. The remedy text points to `knowledge_restructure` trash or
  rename by path.

#### [R2-MIN-006] Stale cross-references (second pass, extended)

- **Lens**: Inconsistency
- **Affected section**: FD-3 and D-PROVENANCE ("§4 step 6" is step 5); the §18 CRIT-002 row ("§4 step 5" — dedup is step 4); §3 Q4 (still "Recommendation: B", and "F4's 8 call sites"); D-LOCK (cited in US-3 AS-6, FR-VA-024, test 37, §18, but never defined in §2)
- **Recommendation**: fix the references, and define D-LOCK in §2 or cite D-SEC.

#### [R2-MIN-007] Traceability gaps (second pass, extended)

- **Lens**: Testability
- **Affected section**: §15
- **Failure scenario**:
  - **No test at all:** FR-VA-018a.
  - **Wrong test mapped:** FR-VA-026 (the path in `knowledge_describe`) maps to test 10, a
    Library-listing test.
  - **Acceptance scenarios with no named assertion:** US-2 AS-6 (the derived statement in the
    three tools), US-5 AS-3 (accessible name), US-6 AS-5 and US-6 AS-6.
  - **Skipped-folder note missing from some surfaces:** FR-VA-025 applies only to
    `knowledge_describe`/`knowledge_configure`. It does not reach `knowledge_find`, the listing
    or the REST view lists (`rest_knowledge_views.go`, `rest_knowledge_base_views.go`).
- **Recommendation**: name the tests, and extend FR-VA-025 to every view-reporting surface.

#### [R2-MIN-008] The auto-rename rules are loose (second pass)

- **Lens**: Ambiguity
- **Affected section**: D-DUPLICATE
- **Failure scenario**: three questions are unanswered.
  - What does a suffixed `label` look like?
  - Rewriting the YAML with a round-trip parse drops comments and key order. A person's
    hand-formatted view changes shape on copy.
  - What does a copy do when it does not parse, so has no readable `name`?
- **Recommendation**:
  - Use a targeted rewrite of the `name:` and `label:` scalars only.
  - Specify the label form, for example "Weekly (2)".
  - An unparsable copy lands unchanged.

#### [R2-MIN-009] Catalogue-first and ADR checks are still missing

- **Lens**: Design-system reuse / structural
- **Affected section**: US-5, US-6, FR-VA-015a (structural checklist)
- **Failure scenario**: the new badges, banners, loading, empty and refusal states, and the
  kind-icon tooltip name no catalogued component. The catalog already has `badge.tsx`,
  `tooltip.tsx`, `error-state.tsx`, `empty-state.tsx`, `skeleton.tsx` and
  `collection-state.tsx`. A frontend lead will be free to invent local ones. The spec still does
  not say whether an ADR was needed. It moves the view location text of ADR-068 D10, as
  round 1 noted.
- **Evidence**: `design-system/catalog.json` entries. (Verified)
- **Recommendation**:
  - Add a "Design-system components" subsection mapping each state to its catalogued
    component, with Phosphor icons for the 8 kinds plus the fallback.
  - Add a line stating that no ADR is needed, because ADR-068 D10's location sentence is
    superseded by this spec.

#### [R2-MIN-010] Inventory and contract-text misses

- **Lens**: Incompleteness
- **Affected section**: F4, §5
- **Evidence**:
  - `pkg/vaultimport/view_write.go::TranslateBase` / `ProducedView.RelPath` ("under
    `.omnipus-vault/views/`") must change to "beside the `.base`", and is not listed.
  - `contracts/openapi.yaml::ViewDef.description` ("lives in
    `<vault>/.omnipus-vault/views/<name>.yaml`") and `ViewDef.source` ("never re-read") must
    be rewritten, and are not listed.
  - The `KnowledgeBaseUnloadableView.yaml` example path is stale.
  - `src/components/library/knowledge/KnowledgeViewsList.tsx` (the collection "Saved views"
    list, #1013) describes the old location.
  - `pkg/knowledge/knowledge_configure_create_view.go::ensureStarterBaseFile` writes a comment
    naming the old directory into user files.

  (Verified)
- **Recommendation**: add these to §5 and to SC-VA-002's sweep.

#### [R2-MIN-011] The site of the listing annotation is unnamed, and one placement would create an import cycle

- **Lens**: Infeasibility
- **Affected section**: D-CONTRACT, §5 (no row for where `is_view`… are computed)
- **Failure scenario**: the discovery helper lives in `pkg/knowledge`, and `pkg/knowledge`
  imports `pkg/library`. Computing the view fields inside `pkg/library/entries.go::List`, next to
  the content-type table FR-VA-012a already sends there, would need `pkg/library` →
  `pkg/knowledge`, which is a cycle.
- **Evidence**: `go list` shows `pkg/knowledge` imports `pkg/library`, and `pkg/library` imports
  only `pkg/api/generated`, `pkg/logger`, `pkg/pathsafe` and `pkg/workspace`. The
  `is_knowledge_base` precedent is annotated in the gateway
  (`pkg/gateway/rest_library.go::annotateKnowledgeBaseEntries`). (Verified)
- **Recommendation**: state that the view fields are annotated in the gateway, beside
  `annotateKnowledgeBaseEntries`, and add the §5 row.

#### [R2-MIN-012] §3's status contradicts the Founder Decisions section; nested vaults are unaddressed

- **Lens**: Ambiguity
- **Affected section**: §3 preamble, Founder Decisions preamble, D-SCOPE
- **Failure scenario**:
  - **Q1–Q5 status.** The Founder Decisions preamble says §3's Q1–Q5 "remain answered as they
    were". §3 still calls them "Five open decisions, each with a recommendation the spec
    assumes". A reader cannot tell whether Q1 A, Q2 C, Q3 B and Q5 B are founder-answered or
    author-assumed.
  - **Nested vaults.** A folder holding its own `.obsidian` marker, copied into a knowledge base
    by hand (creation through Omnipus is refused, but a hand copy is not), is walked by the
    outer collection, because `WalkContained` skips `.obsidian` only as a directory name. D-SCOPE
    uses the innermost collection for listing. The outer collection's dedup and agent discovery
    see the inner views too, so the listing and the agents can disagree about duplicates.
- **Evidence**: `pkg/knowledge/contain.go::WalkContained`;
  `pkg/gateway/rest_library_write.go::enclosingCollectionRel` ("innermost collection wins");
  `pkg/knowledge/detect.go::CreateInWorkspace` (refuses nesting on creation only). (Verified;
  the disagreement is Inferred.)
- **Recommendation**: record Q1/Q2/Q3/Q5 as answered, with the date, or keep them as open
  questions for the founder. State the nested-vault rule: stop discovery at a nested marker, or
  accept and document the case.

---

### Observations

#### [R2-OBS-001] Six flat optional fields could be one nested object, and D-VALIDATE's post-write check is dead (second pass)

- **Lens**: Overcomplexity
- **Suggestion**:
  - A single optional `view: { kind, label, name, collection_id, rejection, derived_from }`
    object makes the "only inside a knowledge base" rule a single presence check.
  - D-VALIDATE's "best-effort post-write check" has nowhere to store its result. The listing
    recomputes the state anyway, so drop the post-write step or give it a purpose (a log line).

#### [R2-OBS-002] Agent-made views with a `source` land at the collection root, far from their `.base` (second pass)

- **Lens**: UI journey
- **Suggestion**: US-2's rationale is that a view sits beside its `.base`. When `create_view` or
  `write_view` receives `source`, defaulting to the `.base`'s folder would match that rationale.
  Otherwise, say why the root is right.

#### [R2-OBS-003] Moving a `.view` out of a knowledge base silently turns it into a plain file (second pass)

- **Lens**: UI journey
- **Suggestion**: under FD-1 this is correct, but agent references to the view break with no
  signal. A move-door notice would help ("this file will stop working as a view").

#### [R2-OBS-004] FR-VA-018a's "at most one walk" needs the post-write serve check reworked

- **Lens**: Testability
- **Suggestion**: `serveRefusalFor` reloads after the write to report servability. State that it
  evaluates the pre-write set plus the newly written view, rather than a second walk.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | Draft |
| ADR linked (or explicitly stated not needed) | FAIL | Still neither (R2-MIN-009) |
| Contract changes stated first, citing `contracts/` | PARTIAL | Fields listed; enum extraction, reason/paths and `collection_id` missing (R2-MAJ-005, R2-CRIT-003) |
| API and data section | PARTIAL | Preview addressing rests on a false claim (R2-CRIT-003) |
| UI screens and states (loading/empty/error/partial) | PARTIAL | States named; error state has no data source (R2-MAJ-005) |
| User journey section | PARTIAL | Holdouts and stories only; no end-to-end journey section |
| Accessibility and keyboard section | PARTIAL | Accessible icon name added; keyboard opening of a view from the tree, and banner announcement, unstated |
| Design-system components, catalogue-first | FAIL | R2-MIN-009 |
| Security and user promises section | PARTIAL | Provenance tamper model and pipeline deletes missing (R2-CRIT-001) |
| BDD acceptance scenarios, oracle from spec | PARTIAL | Given/When/Then acceptance scenarios, with 3 formal blocks |
| Traceability table: requirement -> scenario -> test | PARTIAL | R2-MIN-007 |
| Reachability section | PARTIAL | Preview unreachable (R2-CRIT-003); restore row names a non-existent door (R2-MAJ-007) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Data loss | Derived copy deleted on `.base` save; hand-made view overwritten beside `.base` | R2-CRIT-001, R2-CRIT-002 |
| Lifecycle | `.base` rename, move and delete with derived views outstanding | R2-MAJ-002 |
| Provenance edit | `write_view` and a Library edit on a derived view | R2-MAJ-003 |
| Agent rename | `a.view` → `b.view` destination | R2-MAJ-004 |
| Concurrency | Upsert vs move; re-derivation vs `write_view` | R2-MIN-002 |
| Frontend component states | Rejected-view preview with reason and paths; preview opened from the tree end to end | R2-CRIT-003, R2-MAJ-005 |
| Regression | `.base` preview tabs and `![[X.base#View]]` embeds after `derived_from` lands; the starter-`.base` flow | R2-MAJ-001 |
| Performance | Listing latency at 100,000 files | R2-MAJ-006 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| F (provenance) | Copy of a derived view; two files sharing `derived_from` and `name`; hand-added `derived_from` | Rows F-8…F-10 (R2-CRIT-001) |
| D (write paths) | Pipeline write onto an occupied path beside `.base` | Row D10 (R2-CRIT-002) |
| G (duplicates) | Folder copy with N views; folder restore | Rows G-6, G-7 (R2-MAJ-007) |
| A (locations) | Nested vault inside a knowledge base | Row A13 (R2-MIN-012) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Re-derivation pipeline | ok | risk | risk | ok | ok | risk | A hand-added `derived_from` gives a delete lever over another person's view; deletes are unaudited (R2-CRIT-001); overwrite beside `.base` (R2-CRIT-002) |
| Discovery read path | ok | risk | ok | risk | ok | ok | The walk-to-read race remains open; the cap is not enforced by the read (R2-MIN-001) |
| Library listing annotation | ok | ok | ok | ok | risk | ok | A whole-collection walk plus parse on every folder expansion (R2-MAJ-006) |
| Agent write tools | ok | ok | ok | ok | ok | ok | Gated. Provenance drop is a correctness issue (R2-MAJ-003), not a privilege one |
| Library copy/upload doors | ok | ok | ok | ok | risk | ok | Folder copy switches all originals off (R2-MAJ-007) |

**Legend**: risk = identified threat not mitigated in the document, ok = adequately addressed or not applicable

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tools registered + policy entry? | Yes | `grep -rl` finds `knowledge_describe`, `knowledge_find`, `knowledge_configure` and `knowledge_restructure` in `pkg/coreagent/role_policies_adr090.go`, `pkg/coreagent/seed.go`, `pkg/config/defaults.go` and `pkg/tools/auto_approve.go`. `knowledge_describe`/`knowledge_find` are in `commonWork` (allow); `knowledge_configure`/`knowledge_restructure` are granted `ask` |
| Agent move/rename of a view reachable? | No | R2-MAJ-004 |
| Agent repair of a duplicate reachable through view tools? | No | R2-MIN-005 |
| User-facing: named screen renders it? | No, for the preview | `LibraryPreviewPane.tsx::renderBody` `case 'view'` has no `collection_id` source (R2-CRIT-003). The tree icon is reachable once the contract lands |
| Test plan describes execution, not just authorship? | Partly | §14 demands real handler and click checks; the restore row names a door that does not exist (R2-MAJ-007) |

---

## Unasked Questions

1. What happens to derived views when their `.base` is renamed, moved or deleted? (R2-MAJ-002)
2. Does a copy, restore or upload keep `derived_from`? (R2-CRIT-001)
3. Which field drives the `.base` preview tabs, embeds and rejection attribution: `source` or `derived_from`? (R2-MAJ-001)
4. What is the latency budget for a single Library folder listing? (R2-MAJ-006)
5. Where does the preview get the rejection reason and the conflicting paths? (R2-MAJ-005)
6. Does the pipeline ever overwrite a file it does not manage? (R2-CRIT-002)

---

## Questions for the founder

Answer as "Q1 B, Q2 A, …". These concern the **mechanism** behind FD-3 only. FD-1 to FD-4 stand
as decided.

**Q1 — Which field marks a view as belonging to a `.base` file?**
Context and impact: FD-3 was recorded with the reason "`source` is never re-read, so a new field
is needed". The code shows otherwise. `source` already decides which views a `.base` re-derives,
which tabs the `.base` preview shows, and what `![[X.base#View]]` embeds find. Agents also set
`source` when they make a view, and that creates a starter `.base` which declares the view. With
a second field, the two can disagree. Example: an agent's view plus the first save of its
starter `.base` produce two views with the same label.
- A: one field. `source` is the marker. Any view whose `source` names a `.base` is managed by
  it (today's behaviour), and agent-made views with a `source` are managed too. `derived_from` is
  dropped.
- **B (recommended)**: two fields with separate jobs. `derived_from` (written by the pipeline
  only) decides re-derivation, deletion and the "derived" badge. `source` remains only the
  grouping for the `.base` preview tabs and embeds. The starter `.base` that agents trigger no
  longer declares the agent's view, so its first save does not mint a twin.
- C: `derived_from` everywhere. The `.base` preview tabs and embeds switch to it too, so an
  agent-made view with a `source` no longer appears in that base's tabs.

**Q2 — Can a derived view be edited at all?**
Context and impact: FD-3 as written in the spec says edits are allowed and then overwritten on the
next `.base` save. The dispatch describing FD-3 says derived views are "marked derived/read-only".
Today an agent's `write_view` on a derived view silently removes its "derived" marker, and it then
escapes ".base wins".
- A: editable, overwritten later (the spec as written). `write_view` and Library saves keep the
  marker automatically, and the preview banner warns.
- **B (recommended)**: read-only. `write_view` refuses a derived view and names its `.base`; the
  Library editor opens it read-only with the banner. Edits go to the `.base`. This is the
  smallest behaviour to explain, and removes R2-MAJ-003 outright.

**Q3 — What happens to derived views when their `.base` is renamed, moved or deleted?**
Context and impact: today nothing is specified. Views keep pointing at a `.base` that no longer
exists, still badged "derived", and the moved `.base` mints a second set of views with suffixed
names.
- A: they follow the `.base`. On rename or move, the marker is updated. On delete, the derived
  views go to the trash with it.
- **B (recommended)**: they follow on rename or move. On delete, they are released: the marker
  is cleared and they become ordinary hand-made views. No view is lost by deleting a `.base`.
- C: they are released on all three.

---

## Verdict Rationale

**BLOCK.** Three CRITICAL findings remain, and each would ship as a user-visible failure:

- R2-CRIT-001 and R2-CRIT-002 are data-loss paths the fix round created. They come from the new
  provenance field and from the pipeline's write rule once filenames are decoupled from names.
- R2-CRIT-003 leaves US-6, the preview that makes a view "a file like a note", unreachable.

Seven MAJOR findings follow, three of them round-1 fixes that do not hold against the code
(R2-MAJ-004, R2-MAJ-005, and R2-CRIT-003's MAJ-003). R2-MAJ-001 means FD-3's mechanism needs the
founder's re-confirmation on corrected facts before the fix round, not after.

Before implementation:

- answer Q1–Q3;
- rewrite D-PROVENANCE, D-DUPLICATE, D-ADDRESS and D-MOVE;
- make contract step 1 explicit (the `ViewKind` and `ViewRejectionCode` enums, reason and paths,
  `collection_id`);
- bound listing cost;
- add the missing dataset rows and tests.

### Escalation to the founder

This is the final grill round. Any of these still open after fix round 2 goes to the founder for
disposition. No third grill runs.

| Finding ID | Why it's still open | Founder decision needed |
|---|---|---|
| R2-CRIT-001 | Re-derivation deletes copies and hand-marked views; deletes are permanent | Q1 (field), and approval of "copies strip provenance; pipeline deletes go to trash" |
| R2-CRIT-002 | The pipeline overwrites occupied paths beside `.base` | None if the fix round adopts the suffix/refuse rule; otherwise accept the risk explicitly |
| R2-CRIT-003 | The preview has no `collection_id` | None if the fix round adds the contract field; otherwise US-6 is descoped from v0.1.1 |

### Recommended Next Actions

- [ ] Founder interview on Q1–Q3 (R2-MAJ-001, R2-MAJ-002, R2-MAJ-003)
- [ ] Strip `derived_from` on copy, restore and upload; trash and audit pipeline deletes; add a multi-claimant rule (R2-CRIT-001)
- [ ] Pipeline occupied-path rule (R2-CRIT-002)
- [ ] Add `view_collection_id` (or the nested `view` object) to `LibraryEntry` (R2-CRIT-003)
- [ ] Destination-side `ensureMarkdown` fix and rename test (R2-MAJ-004)
- [ ] Contract step 1: `ViewKind`, `ViewRejectionCode`, reason and paths (R2-MAJ-005)
- [ ] Listing latency budget, or per-file-only listing fields (R2-MAJ-006)
- [ ] Recursive copy and restore rename; fix the restore-door citation, §14 and test 28 (R2-MAJ-007)
- [ ] MINOR items R2-MIN-001…012

### Next step in the process

```
Verdict: BLOCK

Review written to: docs/internal/specs/library-views-anywhere-spec-spec-review-round2.md

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews
the founder on "Questions for the founder", then the spec author fixes
round-2 findings. Any CRITICAL finding still open after that fix is
listed under "Escalation to the founder" above for the founder to
decide — do not run a third grill round. Once resolved, team-lead plans
the implementation (RED / GREEN / CHECK, the 8-reviewer gate).
```
