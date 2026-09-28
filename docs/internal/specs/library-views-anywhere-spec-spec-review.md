# Adversarial Review: Library views, anywhere — a saved view is a file like a note

**Document reviewed**: `docs/internal/specs/library-views-anywhere-spec.md` (branch `feat/library-views-anywhere`, commit `988c420bd`)
**Mode**: Spec
**Round**: Round 1 of 2
**Review date**: 2026-09-28
**Verdict**: BLOCK

## Executive Summary

2 CRITICAL, 10 MAJOR, 12 MINOR and 3 OBSERVATION findings. The spec is well researched on the
read side (the whole-collection walk, `WalkContained`, is the right primitive and is correctly
described), but it does not follow its own central decision (Q3/B: a view's identity is its
`name`, not its filename) through to the **write** side: the writers still derive the file path
from the name. As a result an ordinary `write_view` can silently overwrite a different view's
file (CRIT-001), and routine copies switch working views off (CRIT-002). It also misses a second
live view writer (the `.base` re-derivation on every Library save). Agents cannot move or rename
a view, the Library preview has no server call it could use to open a view by file, and §4
cannot compile as written (package import cycle). Frontend coverage (preview states,
accessibility of the icon-only kind distinction) is thinner than backend coverage.

This file merges two independent round-1 passes over the same commit. Findings the second pass
added are marked "(second pass)", and each was re-verified first-hand before it was included.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 10 |
| MINOR | 12 |
| OBSERVATION | 3 |
| **Total** | **27** |

All code claims below were checked first-hand in the `views-anywhere` worktree at `988c420bd`.
GitNexus was not used (not indexed for this worktree). Evidence comes from direct Read/Grep and is
labelled Verified. Where a conclusion goes beyond what was read, it is labelled Inferred.

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Writers still derive the file path from the view name, so decoupling (Q3/B) makes `write_view` overwrite or duplicate views

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: Q3 (recommended B), Q5/B, §4 (discovery only), FR-VA-005, FR-VA-016, US-1 AS-4
- **Failure scenario**: two cases.
  - **Case 1, silent overwrite.** A person renames the file of view `a` from `a.view` to
    `weekly.view` at the collection root. The view is still called `a` inside the file, which
    Q3/B explicitly allows. Later an agent calls `create_view view=weekly`. The writer joins
    `<root>/weekly.view` and replaces the file with an atomic overwrite. View `a` is destroyed and
    nothing is reported. Its name collision check passes, because no *view* is named `weekly`.
  - **Case 2, both views disappear.** View `x` has been moved into `projects/x.view`. An agent
    calls `write_view view=x`, which is today documented as an upsert of that name. The writer
    creates `<root>/x.view`. On the next load, two files declare `name: x`. Under D-DEDUP both are
    rejected, so view `x` vanishes from `knowledge_find` for every agent.
- **Evidence**: `pkg/knowledge/knowledge_configure.go::execWriteView` builds
  `viewPath := filepath.Join(records.ViewsDir(root), viewName+controlPlaneFileExt)` and writes it with
  `overwriteControlPlaneFile`, which is `fileutil.WriteFileAtomic`, a replace. The comment in the
  same function reads "write_view is an upsert of THIS exact name".
  `knowledge_configure_create_view.go::execCreateView` has the same join. §4 only redesigns the
  read path. Q5/B says only "default to the collection root" and never says how an *existing*
  view is located for an upsert, or what happens when the target filename is already taken.
- **Recommendation**: add a "write-path identity" section and two FRs:
  - "`write_view` on an existing `name` rewrites the file at that view's discovered `SourcePath`,
    wherever it is. It never creates a second file."
  - "A new view's default filename is `<name>.view` at the collection root. When that path
    already exists (whatever it contains), the write is refused. It never overwrites. The refusal
    names the conflicting file."

  Add dataset rows for both cases above.


---

#### [CRIT-002] Copying a view file, restoring it from trash or syncing it in kills the original through the reject-both duplicate rule

- **Lens**: Security (availability) / UI journey
- **Affected section**: D-DEDUP, US-1 AS-3, EC-4, EC-5
- **Failure scenario**: views are now ordinary files, so "duplicate file" in the Library, a trash
  restore, an upload, or a sync client's `open (1).view` conflict copy are routine actions. Each
  creates a second file declaring the same `name`. D-DEDUP then rejects **both**, so the
  original stops answering in `knowledge_find` and in embeds. The person did something harmless
  and a working view silently died. The only signal is in a rejection report that people do not
  see (Holdout 4 admits this). Today this cannot happen by accident, because views live in a
  hidden directory.
- **Evidence**: `pkg/records/view.go::loadViewPaths` has a duplicate-name group, and every member
  is rejected. `knowledge_restructure_trash.go` has no view-name collision check on restore.
- **Recommendation**: founder decision (Founder Q2). Severity raised from MAJOR after the second pass: copy-then-tweak is the ordinary way people make a variant of a note, and EC-5 wrongly calls a new collision "unlikely". At minimum, the spec must require that the
  duplicate state is visible where a person looks: the tree entry and the preview refusal.
---

### MAJOR Findings

#### [MAJ-001] The `.base` re-derivation is a live writer on every Library save, not a "one-shot importer"

- **Lens**: Incompleteness / AS-IS contradiction
- **Affected section**: F1, F4, US-2, FR-VA-008, §5 symbols (`vaultimport/run.go`, `rederive.go` described as "one-shot")
- **Failure scenario**: the operator edits `projects/roadmap.base` in the Library. The save
  handler re-derives that base's views. It writes by slug path, overwrites any `.view` file a
  person has hand-edited (these files are now visible and editable), and **deletes** view files
  that the base "no longer declares". If a person moved an imported view to another folder, the
  re-derivation either writes a second copy beside the `.base` file, which makes a duplicate
  name so both are rejected, or deletes the moved file.
- **Evidence**: the header of `pkg/vaultimport/rederive.go` says it is "called by the Library save
  door after the bytes land" and that "View files this source no longer declares are DELETED".
  `rederive.go::` uses the write loop `path := filepath.Join(viewsDir, filepath.Base(pv.RelPath))`.
- **Recommendation**: treat re-derivation as a first-class writer.
  - Decide which copy is the source of truth once a view is a visible file (Founder Q3 below).
  - Specify that it locates existing views by `source` and slug through the discovery walk, not
    by a fixed path.
  - Specify that it never deletes a file the person moved or edited, or state explicitly that it
    does.

#### [MAJ-002] Agents cannot move or rename a view, which breaks the "agents must do everything" rule

- **Lens**: Reachability
- **Affected section**: Q3/B, EC-5, FR-VA-016, §14 Reachability, Holdout 1
- **Failure scenario**: Q3/B says a view is moved "with the Library's existing move/rename
  operation". That is a human-only surface. The agent's door is `knowledge_restructure`
  rename/move, and it appends `.md` to any non-folder path, so moving `roadmap.view` targets
  `roadmap.view.md`, which is the wrong file. Agents therefore have no knowledge tool to move,
  rename or reorganise views. The only workaround is `bash`/`write_file`, which skips every view
  check.
- **Evidence**: `pkg/knowledge/knowledge_restructure.go::execRenameMove` runs
  `from = ensureMarkdown(from)` and `to = ensureMarkdown(to)` when `folder` is false.
  `pkg/knowledge/authoring_tools.go::ensureMarkdown` appends `.md` to anything that is not
  `.md`/`.markdown`. Agents hold `library_list`/`library_read` only
  (`pkg/coreagent/role_policies_adr090.go` `commonWork`), with no Library move. Trash is fine:
  `knowledge_restructure_trash.go::trashSourcePath` keeps an existing regular non-markdown path.
- **Recommendation**: add a requirement that `knowledge_restructure` rename/move accepts a
  `.view` path unchanged (the same exact-path-first rule `trashSourcePath` already uses). Add a
  reachability row for "agent moves a view", and a test for it.

#### [MAJ-003] The Library preview (US-6) has no server call that can open a view from its file

- **Lens**: Contract-first gaps / Reachability
- **Affected section**: D-CONTRACT, US-6 AS-2, FR-VA-014, §14 "Library preview pane" row
- **Failure scenario**: a person clicks `projects/open.view`. To draw rows, the SPA must call
  `GET .../knowledge/view`, which requires a `collection_id` and the view's `name`. `LibraryEntry`
  would carry only `view_label`. A label is explicitly **not** unique (`Resolve` returns
  `ViewAmbiguousLabel`), and neither the collection nor the name is given. A malformed or
  duplicate-rejected `.view` file has no loadable name at all, so the preview cannot even show
  *why* it is broken.
- **Evidence**: `contracts/openapi.yaml` `getKnowledgeViewResult` has `collection_id` and `view`,
  both required. `pkg/records/view.go::ViewSet.Resolve` has label-ambiguity handling. The
  `LibraryEntry` fields proposed in D-CONTRACT are `is_view`, `view_kind` and `view_label` only.
- **Recommendation**: before any code (Hard Constraint #8), define **one** of these in the
  contract:
  - a path-addressed evaluation (`GET .../knowledge/view?path=<rel>`) that answers with a
    `refusal` for a malformed or duplicate file; or
  - `view_name` plus the owning `collection_id` on `LibraryEntry`.

  Also add `path` to `KnowledgeBaseView`, so the collection views list and agents can say where a
  view lives.

#### [MAJ-004] `delete_view` is missing from the spec, and it hard-deletes what is now an ordinary visible file

- **Lens**: Incompleteness
- **Affected section**: F5 (lists `write_view`, `create_view` only), §5 symbols, §14
- **Failure scenario**: an agent runs `delete_view`. It resolves the view by name and removes the
  file with a bare `os.Remove`. The file is now a user-visible Library file, possibly in a folder
  the person organised. Every other agent deletion of a Library file goes to `.trash` and can be
  restored. This one cannot. The spec also never states whether `delete_view` keeps working
  against a view anywhere in the tree.
- **Evidence**: `pkg/knowledge/knowledge_configure.go::execDeleteView` calls
  `removeControlPlaneFile(target, v.SourcePath)`. `::removeControlPlaneFile` does `os.Remove(abs)`.
  The op table `opDeleteView = "delete_view"` exists and is absent from F5.
- **Recommendation**: add `delete_view` to F5, §5 and §14. Decide whether it goes through the
  existing `Trasher` (recommended: a view is "a file like a note"). Add a test.

#### [MAJ-005] "Anywhere" is silently narrowed to "inside a knowledge base", while `is_view` is set anywhere

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: Input (founder: "stored anywhere like a note"), §4 (collection walk), FR-VA-010, US-4
- **Failure scenario**: the Library tree covers the whole workspace work tree, not only knowledge
  bases. A `.view` file dropped in a plain folder, outside any knowledge base, gets
  `is_view: true` and a kind icon (FR-VA-010 is extension-only). But no tool discovers it, and
  it cannot be evaluated because there is no collection to query. The person sees a view that
  opens to nothing. A view sitting inside a *mounted* folder raises the same question.
- **Evidence**: `contracts/components/schemas/LibraryEntry.yaml` `path` is "Workspace-relative
  path from the work-tree root". §4 builds a `CollectionRoot` per knowledge base.
  `pkg/gateway/rest_knowledge_views.go::handleKnowledgeViews` is collection-addressed.
- **Recommendation**: founder decision (Founder Q1). The spec must then state what `is_view` and
  the preview do for a `.view` outside a knowledge base. Recommended: set `is_view` only when the
  file is inside a knowledge base, or return a refusal state the preview can show.

#### [MAJ-006] Q4/B's startup WARN is upgrade-only code, which the greenfield ruling forbids

- **Lens**: Inconsistency (project rule)
- **Affected section**: Q4, FR-VA-017, SC-VA-007, TDD test 18, Dataset E, Holdout 7, §14 last row
- **Failure scenario**: the recommended option adds startup code that enumerates every
  workspace's knowledge bases to look for a legacy directory. It exists only for installs that
  predate this change. That is exactly the "upgrade-only code" the founder ruled to drop on
  2026-09-15 ("no migrations, no upgrade backfills; drop upgrade-only code and tests, verify the
  fresh-install path"). It also adds a new startup scan with its own failure modes. And because
  `knowledge_base_create` and `handleLibraryCreateVault` seed an empty `.omnipus-vault/views/`
  today, every existing dev knowledge base has that directory.
- **Evidence**: `pkg/knowledge/knowledge_base_create.go` and
  `pkg/gateway/rest_library_write.go::handleLibraryCreateVault` both run
  `os.MkdirAll(records.ViewsDir(...))`. The founder ruling is recorded in the project memory
  "Greenfield, no upgrade path".
- **Recommendation**: make Q4 a firm decision, option A (no WARN), not a founder question. Keep
  the explicit GREEN task to move repo fixtures and e2e seeds out of `.omnipus-vault/views/`.
  Delete FR-VA-017's WARN clause, SC-VA-007, test 18, Dataset E3 and Holdout 7.



---

#### [MAJ-008] §4 cannot compile: `pkg/records` calling `pkg/knowledge` is an import cycle (second pass)

- **Lens**: Infeasibility
- **Affected section**: §4 steps 1a–1c, D-WALK, §5 symbol table
- **Failure scenario**: §4 has `records.LoadViews` build a `knowledge.CollectionRoot` and call
  `knowledge.WalkContained`. `pkg/knowledge` already imports `pkg/records` (for example
  `pkg/knowledge/author.go`), and `pkg/records` imports only `pkg/api/generated`. Go refuses the
  reverse import, so the design cannot build.
- **Recommendation**: invert it. Export today's `loadViewPaths` as `records.LoadViewPaths`. Add a
  discovery helper in `pkg/knowledge` (walk plus `.view` filter) that every caller uses. Rewrite
  §4 and the symbol table, noting that about 13 `LoadViews` call sites change signature.

#### [MAJ-009] A view in an unreadable subfolder vanishes silently (second pass)

- **Lens**: Incorrectness
- **Affected section**: §4, §9 ("must not … silent drop")
- **Failure scenario**: today an unreadable views directory makes `LoadViews` return an error.
  `WalkContained` instead records an unreadable subfolder in `Skipped` (`SkipUnreadable`) and
  returns success. Views in that folder disappear with no report, which breaks the spec's own
  no-silent-drop rule.
- **Evidence**: `pkg/knowledge/contain.go::WalkContained`, which returns an error only for the
  root.
- **Recommendation**: map `SkipUnreadable` entries to a view-load report entry, and test it.

#### [MAJ-010] Library edits skip write-time validation, and a broken or duplicate view looks healthy in the tree (second pass)

- **Lens**: Inoperability / Incompleteness
- **Affected section**: US-4 AS-3, §7 error flows, Holdout 4
- **Failure scenario**: `ViewDef` promises a bad view is "REJECTED at write time (D15), not stored
  and discovered broken later". But a Library text edit, upload or rename of a `.view` file writes
  it with no view validation. No field on `LibraryEntry` carries a rejection, so a duplicate
  (CRIT-002) or broken view shows a normal kind icon.
- **Recommendation**: decide whether a Library content write of a `.view` runs `ParseView` plus
  `ValidateViewAgainstSchemas` and refuses on failure (recommended). Add an optional rejection
  code to `LibraryEntry`, or state that only the preview shows it.

#### [MAJ-011] Agents and people see different sets of views: dot-folders and mounts (second pass)

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: FR-VA-001, FR-VA-012, EC-1, EC-2
- **Failure scenario**:
  - `WalkContained` skips only four folder names. A view in `.drafts/` is found by agents but
    hidden in the Library.
  - A mounted folder is a symlink that the Library shows as a folder
    (`pkg/library/entries.go::annotateMount`), but the walk never follows it. A view there shows
    in the tree but is invisible to agents.
- **Recommendation**: align the discovery skip set with the Library's hidden rule, and state what
  happens to mounts. Add these as dataset rows.
---

### MINOR Findings

#### [MIN-001] The read path cites the wrong containment primitive, and the file read follows symlinks

- **Lens**: Security
- **Affected section**: §4 step 1c, FR-VA-001, §9 "must not follow a symlink during discovery"
- **Failure scenario**: §4 re-checks each discovered path with `ResolveContained`. That method
  *accepts* a path that reaches its target through an in-collection symlink. `loadViewPaths` then
  calls `os.ReadFile`, which follows a symlink. If a file found by the walk is swapped for a
  symlink before the read (an agent with `bash` can do this), the target's bytes are parsed.
  Fragments of those bytes can appear in the YAML error text of a rejection `Reason`. The window
  is small, but the spec's own §9 prohibition is not met by the mechanism it prescribes.
- **Evidence**: see the doc comments of `pkg/knowledge/contain.go::ResolveContained` and
  `::ResolveContainedNoSymlink`, and `pkg/records/view.go::loadViewPaths` (`os.ReadFile(p)`).
- **Recommendation**: specify `ResolveContainedNoSymlink`, plus a read that does not follow
  symlinks (an `Lstat` regular-file check, or an open-then-`Stat` comparison through `LinkFS`).
  Add a test that swaps a walked file for a symlink.

#### [MIN-002] No size cap on `.view` files read during discovery and directory listing

- **Lens**: Security (resource exhaustion)
- **Affected section**: §4, D-CONTRACT (`view_kind` needs a parse per listed file), US-4, EC-7
- **Failure scenario**: any file named `*.view`, dropped anywhere, is fully read into memory on
  every `knowledge_describe`, `knowledge_find` and `knowledge_configure` call, and on every
  directory listing that contains it. Examples are an upload, a sync client, or a 500 MB file
  created by mistake. Before this change only the hidden control directory was read.
- **Recommendation**: add a maximum `.view` file size and a rejection for oversize files (for
  example `view_too_large`). If the code reaches the wire, it goes through the contract first. Add
  a dataset row.

#### [MIN-003] Performance: the per-call walk multiplies, the option list misses the existing index manifest, and the landing gate cannot fail

- **Lens**: Infeasibility / Testability
- **Affected section**: Q2, FR-VA-018, SC-VA-004, EC-7
- **Failure scenario**: a single `write_view` call runs `LoadViews` two or three times: the
  collision check, `serveRefusalFor`, and in schema edits a before/after pair. Each run becomes a
  full walk of the collection. SC-VA-004 says it passes "within the number the benchmark itself
  establishes", which no result can fail. Go benchmarks are not part of any CI gate. Q2 also omits
  the obvious option of reusing the knowledge index's existing file list
  (`pkg/knowledge/manifest.go`), which already tracks every file with size and modification time.
- **Evidence**: `pkg/knowledge/knowledge_configure.go` has 5 `LoadViews` calls, including
  `::serveRefusalFor`. `pkg/knowledge/manifest.go` holds the MV-4 100,000-file budget.
- **Recommendation**: set an absolute budget (for example, under X ms at 100k files, with X from a
  pre-spec measurement). Load once per operation. Add "reuse the manifest's file list" as an
  option in Q2.

#### [MIN-004] Contract work is under-specified: the `view_kind` enum is inline, and the location is stated in other schema descriptions

- **Lens**: Contract-first gaps
- **Affected section**: D-CONTRACT, FR-VA-009, SC-VA-006
- **Failure scenario**: `view_kind` "one of the generated `ViewDefKind` enum values" cannot be
  referenced from `LibraryEntry.yaml`, because the `kind` enum is inline in `ViewDef` in
  `openapi.yaml`. Extracting it renames the generated constants that
  `pkg/knowledge/view_kinds.go` re-exports. Several contract descriptions also still say views
  live in `<vault>/.omnipus-vault/views/<name>.yaml`. That text would become false and ship in the
  generated types.
- **Evidence**: `contracts/openapi.yaml` `ViewDef.description` and `ViewDef.kind`;
  `contracts/components/schemas/KnowledgeBaseView.yaml`, `KnowledgeCollectionViews.yaml` and
  `KnowledgeBaseUnloadableView.yaml` (all mention `.omnipus-vault/views`);
  `pkg/knowledge/view_kinds.go::ViewKindTable`.
- **Recommendation**: add contract step 1, "extract `ViewKind` as
  `contracts/components/schemas/ViewKind.yaml`, referenced by `ViewDef.kind` and
  `LibraryEntry.view_kind`". List every description that must be rewritten. List the
  `KnowledgeBaseView.path` addition from MAJ-003.

#### [MIN-005] The write-containment stories test a destination argument that Q5/B says does not exist, and the importer write is not covered

- **Lens**: Testability / Inconsistency
- **Affected section**: US-3, Dataset D (D2–D4, D6), FR-VA-006, US-2
- **Failure scenario**: under Q5/B no caller supplies a destination. D2 ("a real subfolder"), D3
  and D4 therefore have no input to feed, and RED tests either get invented against a
  non-existent parameter or silently skipped. Meanwhile the real new write-to-arbitrary-folder
  path is the importer writing beside a `.base` file. Today `run.go::writeAndReloadViews` writes
  with a bare `WriteFileAtomic` and no containment check, unlike `rederive.go`, which calls
  `resolveViewWritePath`. The spec does not name that gap.
- **Recommendation**: rewrite Dataset D against the destinations that actually exist: the
  collection root, beside a `.base` file, and the existing path of an upsert. Add
  "`writeAndReloadViews` must use the same no-symlink check as `rederive`".

#### [MIN-006] The call-site inventory is inaccurate and incomplete

- **Lens**: Incompleteness
- **Affected section**: F3, F4, §5 impact table, SC-VA-002
- **Evidence**:
  - `LoadAll` does not exist in `pkg/records`.
  - `LoadViews` has about 13 production callers, not 4. The missing ones include
    `knowledge_edit.go`, `knowledge/tools.go`, `vaultprops/find_env.go`, five in
    `knowledge_configure.go`, and both `vaultimport` files.
  - "8 sites across 6 files" is actually 9 `ViewsDir(` sites across 7 files.
  - `ViewsDirName` users are missed: `vaultimport/view_translate.go` (the produced `relPath`),
    the starter-`.base` text in `knowledge_configure_create_view.go`, and the
    `pkg/app/internal/records/command.go` help text.
  - `delete_view` is missing (MAJ-004).
  - F4 also cites `file:line`, against the repo's citation rule.
- **Recommendation**: replace F3/F4 with the full list, cited `file::symbol`. Make SC-VA-002 cover
  both `ViewsDir` and `ViewsDirName`.

#### [MIN-007] Lock and concurrency between Library saves and agent view writes are unspecified

- **Lens**: Incompleteness
- **Affected section**: §4, US-3
- **Failure scenario**: the agent writes hold `controlPlaneLockKey`, while a person saving the same
  `.view` in the Library editor goes through the Library write path. Nothing states that the two
  serialise, so a concurrent edit and `write_view` can lose one write. This is Inferred: the
  Library-side lock was not traced.
- **Recommendation**: state which lock guards a `.view` file, and that both paths take it.

#### [MIN-008] Preview and tree UI states and accessibility are missing

- **Lens**: UI states & journey / Accessibility
- **Affected section**: US-5, US-6, FR-VA-014, FR-VA-015
- **Failure scenario**: there are no loading, empty (zero rows), refusal, unservable or duplicate
  states for the preview pane. The 9 kind icons are the only thing that tells kinds apart: there
  is no text alternative and no accessible name, and screen-reader users hear the same thing for
  every view. No design-system catalog check is made for the icon set (Phosphor) or the
  preview's error banner.
- **Recommendation**: add a states table for the preview (reusing `BasePreview`'s refusal
  rendering where possible), an `aria-label`/tooltip naming the kind, and a line pointing to the
  catalogued components.


---

#### [MIN-009] Re-derivation reports "Deleted" for a file it did not delete (second pass)

- **Lens**: Incorrectness (false success)
- **Affected section**: US-2, MAJ-001
- **Evidence**: `pkg/vaultimport/rederive.go` ignores `os.ErrNotExist` on `os.Remove` and still
  appends the slug to `res.Deleted`. With views anywhere, a view the person moved is reported
  deleted but remains on disk.
- **Recommendation**: report "deleted" only when a file was actually removed, and locate the file
  by `source` match.

#### [MIN-010] Extension details are missing: letter case, a file named just `.view`, content type, text-editable flag (second pass)

- **Lens**: Ambiguity
- **Affected section**: Q1, FR-VA-010, FR-VA-012
- **Recommendation**: state whether `.VIEW` matches (today's loader lowercases extensions). State
  that a file named exactly `.view` is hidden and not discovered. FR-VA-012 is a no-op, because
  `x.view` never starts with a dot. Add FRs to put `.view` in `pkg/library/entries.go`'s content
  type table and its text-editable set.

#### [MIN-011] "Existing tests pass unchanged" is impossible (second pass)

- **Lens**: Testability
- **Affected section**: §10 Regression item 2
- **Evidence**: `pkg/records/view_test.go` calls `ViewsDir`, and many Go and SPA tests plant files
  in `.omnipus-vault/views`. The second pass counted 29 Go and 4 SPA files (count not
  re-verified).
- **Recommendation**: reword to "fixtures relocated; assertions unchanged", and list the
  relocation as a GREEN task.

#### [MIN-012] The BDD structure and "Traces to" lines are missing (second pass)

- **Lens**: Testability / traceability
- **Affected section**: §6, §15
- **Recommendation**: add Given/When/Then scenario blocks with `Traces to:` lines. Trace US-2
  AS-2, US-6 AS-3 and `view_label` into the matrix. Name the two "add at implementation" tests
  now (for example `TestDiscovery_SkipsControlDirs`, `TestRenameThenResolve`).
---

### Observations

#### [OBS-001] `SavedView.SourcePath` already exists

- **Lens**: Overcomplexity
- **Affected section**: §4 step 4
- **Suggestion**: reuse `SavedView.SourcePath` (made collection-relative at the edge) rather than
  adding `Path`.

#### [OBS-002] The routing argument misreads the greenfield rule

- **Lens**: Inconsistency
- **Affected section**: Routing note
- **Suggestion**: "no back-compat" is a standing, project-wide founder ruling (2026-09-15), not a
  v0.3 marker. The case for routing to v0.3 must rest on scope, not on the absence of a
  migration. See Founder Q4.

#### [OBS-003] Agents are not told where a view lives

- **Lens**: Reachability
- **Affected section**: §14 `knowledge_describe` row
- **Suggestion**: once the location varies, `knowledge_describe`'s VIEWS section and the
  `knowledge_configure` result should state each view's collection-relative path. Today the
  `Path` in the configure result goes through `relControlPlanePath`. This needs a prompt-text
  update by `prometheus-prompt-engineer`.

---

## Verified citations

| Spec claim | Result | Evidence |
|---|---|---|
| F1 single `source`, `type` optional | Verified | `pkg/records/view.go::ParseView` (empty `type:` refused, absent allowed) |
| F2 `.base` visible, dot-prefix hides | Verified | `pkg/library/entries.go::List`; `libraryPreviewKind.ts::classifyLibraryEntry` |
| F3 one read choke point, `LoadAll` | Partly false | `LoadAll` absent; `LoadViews` has about 13 callers (MIN-006) |
| F4 8 sites / 6 files | Inaccurate | 9 `ViewsDir(` sites / 7 files, plus `ViewsDirName` users (MIN-006) |
| F5 agent tools | Incomplete | `delete_view` missing (MAJ-004) |
| F7 name unique, label ambiguous | Verified | `view.go::loadViewPaths`, `::ViewSet.Resolve` |
| F8 three vocabularies, `kind` 8 values | Verified | `openapi.yaml` `ViewDef.kind`/`layout`; `view_kinds.go` |
| F9 `.yaml` for both | Verified | `knowledge_configure.go::controlPlaneFileExt` |
| F10 `WalkContained` no-follow, skip set | Verified | `contain.go::WalkContained`; `scan.go::scanSkippedDirNames` |
| F11 fixed join, no destination | Verified | `knowledge_configure.go::execWriteView` |
| F12 no view cache, 100k budget | Verified, but misses the manifest file list (MIN-003) | `manifest.go` |
| Tool registration (§14) | Verified | `knowledge_describe`/`find`/`configure` in `pkg/config/defaults.go`, `pkg/coreagent/seed.go`, `role_policies_adr090.go` |
| Importer is one-shot | False | `vaultimport/rederive.go` runs on every Library `.base` save (MAJ-001) |

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | Draft |
| ADR linked (or explicitly stated not needed) | FAIL | Neither; the storage move arguably reverses the ADR-068 D10 location text |
| Contract changes stated first, citing `contracts/` | PARTIAL | `LibraryEntry` only; evaluation-by-path, `ViewKind` extraction and description rewrites missing |
| API and data section | PARTIAL | No evaluation API for the preview (MAJ-003) |
| UI screens and states | FAIL | MIN-008 |
| User journey section | PARTIAL | Holdouts only |
| Accessibility and keyboard section | FAIL | MIN-008 |
| Design-system components, catalogue-first | FAIL | Not mentioned |
| Security section | PARTIAL | Write side covers a non-existent input; read-side primitive wrong (MIN-001, MIN-005) |
| BDD acceptance scenarios | PASS | |
| Traceability table | PASS | Two gaps honestly flagged |
| Reachability section | PARTIAL | Agent move/rename and preview addressing missing (MAJ-002, MAJ-003) |

## Test Coverage Assessment

| Category | Gap | Affected |
|---|---|---|
| Write identity | Upsert of a moved view; create onto an occupied filename | CRIT-001 |
| Re-derivation | `.base` save after a view was moved or edited | MAJ-001 |
| Agent move/rename | `knowledge_restructure` on `.view` | MAJ-002 |
| Delete | `delete_view` of a view outside the root; trash/restore | MAJ-004, CRIT-002 |
| Symlink race | Walked file swapped for a symlink before read | MIN-001 |
| Resource limit | Oversize `.view` | MIN-002 |
| Frontend states | Preview loading/refusal/duplicate; icon accessible name | MIN-008 |

---

## Questions for the founder

Answer as "Q1 A, Q2 B, …".

**Q1 — Where may a view live?**
Context and impact: you said "anywhere where it makes sense, like a note". A view can only be
*run* against a knowledge base (it queries that base's records). The Library also shows plain
folders that are not knowledge bases. This decides whether a `.view` in a plain folder shows as a
view.
- **A (recommended)**: anywhere *inside* a knowledge base. A `.view` elsewhere shows as a plain
  file.
- B: anywhere in the workspace. A view outside a knowledge base shows the view icon and explains
  on open that it has no knowledge base to query.
- C: anywhere, and a view outside a knowledge base must name the knowledge base it queries.

**Q2 — What happens when two view files carry the same name (for example after "duplicate file",
restore from trash, or a sync conflict copy)?**
Context and impact: today both are switched off, which is safe but means copying a view silently
breaks the original for every agent. Now that views are ordinary files, copies will be common.
- A: keep today's rule (both off) and show a clear "duplicate" warning on both files in the tree
  and preview.
- **B (recommended)**: the copy is renamed automatically (`name` gets a suffix) when it is
  created through Omnipus (copy, restore, upload). Files that arrive by other means fall back to
  A.
- C: the older file wins and the newer one is flagged.

**Q3 — For views imported from a `.base` file, which copy is the source of truth once the view
is a visible, editable file?**
Context and impact: today every save of the `.base` file rewrites its views and deletes the ones
it no longer lists. Once a person can see, edit and move the `.view` files, that rewrite can undo
their edits or delete a file they moved.
- A: the `.base` wins. Edits to imported `.view` files are overwritten on the next `.base` save,
  and the preview says so.
- **B (recommended)**: import once. After the `.view` file exists it is independent, and editing
  the `.base` no longer rewrites or deletes it (re-import is an explicit action).
- C: the `.view` wins. The `.base` is re-derived from the `.view` files.

**Q4 — Which release does this belong to?**
Context and impact: the spec routes it to v0.3 because it drops back-compat. But "no back-compat"
is already your standing rule for every release, so that is not a reason by itself. The change is
mid-sized: storage, three agent tools, one contract change, and a new preview.
- A: v0.1.1 (current release branch), as a feature-size change.
- **B (recommended)**: v0.3, together with the Saved-views block removal (#1013) it depends on
  for reachability.
- C: its own milestone between the two.

(Q4 of the spec, the orphan-directory WARN, is not re-asked. Under your 2026-09-15 greenfield
ruling it should simply be dropped, see MAJ-006.)

Note: the second pass also offered, for Q2, the option "the filename is the name, like a note: renaming renames the view, and a copy becomes a new view". That option removes CRIT-001 and CRIT-002 at the root, but it reverses the spec's Q3/B. If you prefer it, answer **Q2 D**.
