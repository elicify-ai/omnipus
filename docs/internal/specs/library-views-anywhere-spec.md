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
`ee7640c90` (read-only checkout used to verify every claim below).

**Routing note (CLAUDE.md "Routing rule")**: issue #1017 carries no milestone. This change
is structural — it deletes a storage convention, adds two contract fields, and changes a
tool's write surface with no back-compat path (D-E below). That shape matches v0.3
("Workspaces redesign... fresh-build, no back-compat"), not v0.1 stabilization or v0.2's
"quick fixes only" pentest scope. Recommend routing to v0.3; team-lead should confirm before
scheduling.

---

## 1. Facts established from the code (verified, cited `file::symbol`)

| # | Claim | Evidence | Certainty |
|---|---|---|---|
| F1 | A view has at most one `source` string, records where it was imported from, and is never re-read. Data is one `filter` tree plus an optional single `type`. No joins. | `contracts/openapi.yaml::components.schemas.ViewDef` (source field description); `pkg/records/view.go::SavedView.Source` | Verified |
| F2 | `.base` files are ordinary, visible Library entries today (extension-classified, not hidden); views are hidden because they live under a dot-prefixed control-plane directory. | `pkg/library/entries.go::listDir` (hidden = `strings.HasPrefix(de.Name(), ".")`, line ~41); `src/components/library/preview/libraryPreviewKind.ts::classifyLibraryEntry` (`if (e === 'base') return 'base'`) | Verified |
| F3 | One read choke point loads every view: `records.ViewsDir` feeds `LoadAll`/`LoadViews`, consumed by `rest_knowledge_views.go`, `rest_knowledge_base_views.go`, `knowledge_describe.go`, and `knowledge_find`'s `ViewFindLoader`. Writers hard-code the same directory. | `pkg/records/view.go::ViewsDir`, `::LoadViews` | Verified |
| F4 | Full call-site inventory of `records.ViewsDir(...)` — 8 sites across 6 files: reader `pkg/records/view.go:544` (`LoadViews`); writers `pkg/knowledge/knowledge_base_create.go:252` (`MkdirAll` on KB creation), `pkg/knowledge/knowledge_configure.go:802,849` (`write_view`), `pkg/knowledge/knowledge_configure_create_view.go:822,914` (`create_view`), `pkg/vaultimport/run.go:559` and `pkg/vaultimport/rederive.go:195` (`.base` one-shot importer), `pkg/gateway/rest_library_write.go:1053` (`MkdirAll` on write). | Grep sweep, `grep -rn "ViewsDir(" pkg/ --include='*.go'` (GitNexus MCP not available in this worktree/session — see §1a) | Verified (grep), Inferred (completeness — see §1a) |
| F5 | Agent tools touching views: `knowledge_describe`, `knowledge_find`, `knowledge_configure` (ops `write_view`, `create_view`, plus reads via the shared `ViewFindLoader`). Founder rule: agents must do everything wherever views are stored. | `pkg/knowledge/knowledge_configure.go::opWriteView, opCreateView`; `pkg/knowledge/knowledge_describe.go::renderViews`; `pkg/records/knowledgefind/find.go` (ViewFindLoader references) | Verified |
| F6 | Renderer for a view's rows already exists (`ViewPartsRenderer.tsx`, an exhaustive switch over `ViewResultPart.part`); the Library preview pane's `renderBody` switch has no `'view'` case — only `'base'`, which mounts `BasePreview` (tabs over evaluated view results). | `src/components/library/preview/viewparts/ViewPartsRenderer.tsx::renderPart`; `src/components/library/LibraryPreviewPane.tsx::renderBody` (case `'base'` at ~line 172, no `'view'` case) | Verified |
| F7 | `ViewDef.name` is documented "unique within the vault" (global, not per-directory) and this is already enforced: `loadViewPaths` rejects any two views sharing a `name` today, and `ViewSet.Resolve` looks a view up by slug (`Def.Name`, unique by construction) or, on ambiguity, by `DisplayLabel()` (label collisions ARE allowed and disambiguated via `ViewAmbiguousLabel`/`ViewLabelCandidate`). | `contracts/openapi.yaml::ViewDef.name` description; `pkg/records/view.go::Resolve, ViewAmbiguousLabel, ViewLabelCandidate, RejectViewDuplicateName` | Verified |
| F8 | `ViewDef.kind` is a closed 8-value enum (`table, list, tiles, board, calendar, summary, trend, breakdown`) used by `create_view`'s composer; it is a *different* vocabulary from `ViewDef.layout` (6 values: `table, cards, board, calendar, gallery, map`) and from the render-time `ViewResultPart.part` (8 values: `table, list, tiles, columns, calendar, figures, chart, crosstab`). `kind` is NOT required by the schema (only `name` is) — `write_view`'s hand-authored path can omit it. | `contracts/openapi.yaml` (`kind:` enum ~line 710; `layout:` enum ~line 162); `pkg/knowledge/view_kinds.go::ViewKindTable...ViewKindBreakdown` (re-exported from `generated.ViewDefKind*`) | Verified |
| F9 | Both schema files (`SchemaDir`) and view files (`ViewsDir`) are written today with the same plain `.yaml` suffix, distinguished only by which control-plane directory holds them. | `pkg/knowledge/knowledge_configure.go::controlPlaneFileExt = ".yaml"` (used at line 551 for schemas and line 849 for views) | Verified |
| F10 | `pkg/knowledge.CollectionRoot` + `WalkContained` already exist and do exactly the safe, symlink-refusing, containment-checked, whole-collection walk this feature needs: never follows a symlink (FR-044), every path proven to resolve inside the real (post-symlink) collection root (FR-043), and permanently skips `.obsidian`, `.omnipus-vault`, `.git`, `.trash` at any depth via `scanSkippedDirNames`. `BuildLinkGraph` already pays this walk's full cost once per collection-scope request for the link graph. | `pkg/knowledge/contain.go::CollectionRoot, WalkContained, ErrCollectionRootInvalid`; `pkg/knowledge/graph.go::BuildLinkGraph` | Verified |
| F11 | `pathsafe` (`ValidateComponent`, `ValidateRelPathLength`, `RuleSet.ValidateNameShape`, `FirstIllegalRune`, `IsReservedDeviceName`, …) is the existing filename-safety layer; `CollectionRoot.ResolveContained` / `ResolveContainedNoSymlink` / `ResolveControlWritePath` are the existing path-containment layer for writes. Neither is currently exercised by the view writers, because today's writer only ever joins a fixed, server-controlled `ViewsDir(root)` with a slugged name — there is no caller-supplied destination folder to validate. | `pkg/pathsafe/pathsafe.go`, `pkg/pathsafe/rules.go`; `pkg/knowledge/contain.go::CollectionRoot.ResolveContained` et al.; `pkg/knowledge/knowledge_configure.go:849` (fixed join, no destination argument) | Verified |
| F12 | This repo has no existing persistent index/cache for views. `LoadViews` does one flat `os.ReadDir` per call (cheap: O(views), one directory). The only content-hash snapshot pattern in `pkg/records` is `SnapshotSchemas` for **schemas**, not views, and nothing wires it to views. The codebase's standing scale assumption for a large collection, used repeatedly for budgeting incremental work, is **100,000 files** (e.g. "reconcile 100,000 unchanged files in under 2 seconds", MV-4). | `pkg/records/view.go::LoadViews` (single `os.ReadDir`); `pkg/records/invalidate.go::SnapshotSchemas`; `pkg/knowledge/manifest.go` (100,000-file budget comment) | Verified |

### 1a. GitNexus availability

GitNexus MCP tools (`query`, `context`, `impact`, `trace`, `explain`) were **not available** in
this session/worktree (checked via tool search; none resolved). Per `omnipus-shared-rules` rule
9's fallback, F4's call-site inventory is a Grep sweep (`grep -rn "ViewsDir(" pkg/ --include='*.go'`),
labeled Inferred for completeness — a rename or an indirection (e.g. a wrapper that calls
`ViewsDir` without the literal substring `ViewsDir(`) would not show up in a literal grep. The
implementing lead should re-run this sweep (or GitNexus `impact({target: "ViewsDir", direction:
"upstream"})` if the target checkout has it indexed) before deleting the old writers.

---

## 2. Firm decisions (not founder questions)

These are decided here, with the evidence that makes them low-risk, so the spec below is
internally consistent. Team-lead/backend-lead may still revisit any of them; each cites why
it did not need to go to the founder.

- **D-DEDUP — Reuse the existing name/label dedup mechanism unchanged.** F7 shows `ViewDef.name`
  is *already* required to be globally unique across the vault, and `Resolve`/`ViewAmbiguousLabel`
  already handle label collisions. "Anywhere" storage does not introduce a new collision model —
  it only widens `loadViewPaths`'s input from "one directory's file list" to "the tree-walk's file
  list" (§4). `RejectViewDuplicateName` is reused, not reinvented.
- **D-ICON — The per-kind icon (founder: "table, calendar, chart, …") keys off `ViewDef.kind`**
  (F8's 8-value enum: table/list/tiles/board/calendar/summary/trend/breakdown), not `layout` and
  not the render-time `part` vocabulary. `kind` is the only one of the three that is a single,
  definitional, top-level property of a saved view — the thing "what sort of view is this" asks
  about — and it is what `create_view` already writes. A `.view` file with no `kind` (legal under
  the schema, since only `name` is required — F8) falls back to a generic "view" icon (Edge Case
  EC-3).
- **D-WALK — Discovery is a dedicated `.view`-extension pass over `WalkContained`'s file list**
  (F10), not folded into `BuildLinkGraph`'s walk (which only opens `.md` files and is scoped to
  the link-graph build, not a general-purpose file index). `WalkContained` itself is reused
  as-is: no new symlink or containment logic is written for this feature (Hard Constraint #6's
  security posture: do not reinvent a containment check that already exists and is tested).
- **D-SEC — Writes go through the existing containment/pathsafe layers, not new ones.** Any
  caller-supplied destination path (new, because F11 shows today's writer never took one) is
  resolved with `CollectionRoot.ResolveContainedNoSymlink` before any file is touched, and the
  final filename is validated with `pathsafe.RuleSet.ValidateComponent`/`ValidateNameShape`
  before write. This is the *reason* the attack surface changes at all (F11) — flagged for
  `security-lead` review before landing (§9).
- **D-CONTRACT — Add three optional `LibraryEntry` fields**, modeled directly on the existing
  `is_knowledge_base` pattern (F2's neighbor field: computed once per directory entry during
  listing, absent when not applicable, optional on the wire so old SPA builds/fixtures keep
  working):
  - `is_view: boolean` — true when the extension classifies as a view (D-Q1 below decides the
    extension).
  - `view_kind: string` — one of F8's 8 `ViewDefKind` values, absent when the file's `kind` is
    unset or the file fails to parse (Edge Case EC-3).
  - `view_label: string` — the view's `DisplayLabel()` (F7: `Label` if set, else `name`), so the
    tree can show the human label without a second round trip.

---

## 3. Questions for the founder

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
| **C (recommended)** | Ship the uncached O(collection) walk for this issue, with the benchmark from option A as a landing gate (not a made-up SLO number), and defer a persistent index to the v0.3 Workspaces redesign this change is already being routed to (§ Routing note) — where a general-purpose collection index is a more natural fit than a views-only cache. |

**Recommendation**: C, with A's benchmark gate kept as a hard requirement regardless of which
option is picked (SC-VA-004 below is written to hold under either A or C).

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

### Q4 — Fresh-install path and the old hidden-directory writers

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

---

## 4. Discovery mechanism (assumes Q1=A, Q2=C, Q3=B)

1. `records.LoadViews` stops constructing a fixed `ViewsDir(vaultRoot)` path and instead:
   a. Builds a `knowledge.CollectionRoot` for the vault/collection root (existing constructor,
      F10 — already used by 10 other call sites, e.g. `pkg/knowledge/knowledge_configure.go:1016`).
   b. Calls `knowledge.WalkContained(fsys, root)` (F10) — unmodified — and filters `WalkResult.Files`
      to those with the `.view` extension (Q1).
   c. For each surviving path, resolves it with `CollectionRoot.ResolveContained` (proves
      containment a second time, mirroring `BuildLinkGraph`'s own belt-and-braces re-check at
      graph.go — F10) before reading, parses it exactly as today (JSON round-trip through the
      generated `ViewDef` type with `DisallowUnknownFields` — unchanged from the current parser,
      F1), and records its collection-relative path.
2. Every existing rejection code (`RejectView*`, F1's list) is unchanged in meaning; only the
   *set of files fed into the same parser* changes, from "one directory's `os.ReadDir` result" to
   "the walk's filtered file list."
3. Name/label dedup (D-DEDUP) runs over that widened file list exactly as it runs today over the
   flat list — no new dedup logic.
4. `SavedView` gains a `Path` (or renames/repurposes an existing field — implementing lead's
   call) carrying the collection-relative path discovered at, so callers that need to address a
   view by path (the Library tree, D-CONTRACT's `view_label`, click-to-open) have it without a
   second walk.

---

## 5. Existing Codebase Context

### Symbols involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/records/view.go::ViewsDir` | Deleted (Q4) | Replaced by the walk in §4; F4 lists every caller that must change. |
| `pkg/records/view.go::LoadViews`, `LoadAll` | Modified | Reader entry point; changes from directory list to filtered walk (§4). |
| `pkg/knowledge/contain.go::CollectionRoot`, `WalkContained` | Reused, unmodified | Discovery's containment and walk primitive (F10, D-WALK). |
| `pkg/pathsafe/*` | Reused, unmodified | Write-path filename/component validation (F11, D-SEC). |
| `pkg/knowledge/knowledge_configure.go::opWriteView`, `pkg/knowledge/knowledge_configure_create_view.go::opCreateView` | Modified | Writers stop hard-coding `ViewsDir`; take a destination resolved per Q5/D-SEC. |
| `pkg/knowledge/view_kinds.go::ViewKindTable`…`ViewKindBreakdown` | Reused, unmodified | Source of the 8-value icon vocabulary (D-ICON). |
| `pkg/vaultimport/run.go`, `rederive.go` | Modified | One-shot `.base` importer's view-writing call sites (F4); target directory changes to co-located-with-`.base` (§8, US-2). |
| `src/components/library/preview/libraryPreviewKind.ts::classifyLibraryEntry` | Modified | Gains a `'view'` kind, extension-classified exactly like `'base'` (F2, F6). |
| `src/components/library/LibraryPreviewPane.tsx::renderBody` | Modified | Gains a `case 'view':` mounting a new preview component that reuses `ViewPartsRenderer` (F6). |
| `contracts/components/schemas/LibraryEntry.yaml` | Modified | Three new optional fields (D-CONTRACT). |

### Impact assessment

GitNexus is unavailable this session (§1a); impact is Grep-sourced and labeled Inferred.

| Symbol modified | Risk (inferred) | d=1 dependents |
|---|---|---|
| `records.ViewsDir` deletion | HIGH — 8 call sites across 6 files (F4) | `knowledge_base_create.go`, `knowledge_configure.go` (×2), `knowledge_configure_create_view.go` (×2), `vaultimport/run.go`, `vaultimport/rederive.go`, `rest_library_write.go` |
| `records.LoadViews` signature/behavior | HIGH — every reader (`rest_knowledge_views.go`, `rest_knowledge_base_views.go`, `knowledge_describe.go`, `knowledgefind.ViewFindLoader`) depends on its current shape and error semantics | 4 direct readers, all of `knowledge_describe`/`knowledge_find`/`knowledge_configure`'s downstream behavior (F5) |

Both are flagged HIGH per `omnipus-shared-rules` rule 9 — the implementing lead must re-run
`impact({target: "ViewsDir", direction: "upstream"})` (or an equivalent Grep sweep, repeated
after any refactor) before deleting either symbol, and must not proceed past a HIGH/CRITICAL
result without a plan for every dependent listed.

### Cluster placement

This feature spans two areas GitNexus would likely call separate clusters: the knowledge-base
control plane (`pkg/records`, `pkg/knowledge`, `pkg/vaultimport`) and the Library/Preview frontend
surface (`src/components/library`). Both must land together — a backend-only or frontend-only
half leaves the feature unreachable (§9, Reachability).

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

### US-2 — `.base` import writes views next to the file they came from (P0)

An operator imports an existing `.base` file's saved views (the one-shot importer, F1/F4). Today
the imported views vanish into the hidden directory even though their `source` (F1) already names
the `.base` file. This story keeps that provenance link visually true: the imported view sits next
to the file it was imported from, the same way a person would file it by hand.

**Why this priority**: P0 — the importer is one of the 8 call sites (F4) that must change together
with the rest of the write path, or the migration story (Q4) is incoherent.

**Independent test**: run the one-shot importer against a `.base` file in a subfolder; the
resulting `.view` file(s) appear in that same subfolder, each with `source` still naming the
`.base` file (F1, unchanged).

**Acceptance Scenarios**:
1. **Given** a `.base` file at `projects/roadmap.base` declaring one view, **When** the one-shot
   importer runs, **Then** the resulting `.view` file is written under `projects/`, not the
   collection root and not `.omnipus-vault/views/`.
2. **Given** an import whose target name collides with an existing view anywhere in the collection
   (D-DEDUP), **When** the importer runs, **Then** the conflicting view is rejected exactly as
   `write_view`/`create_view` would reject it (same `RejectViewDuplicateName` code, F7).

### US-3 — Writes are contained and validated, wherever they land (P0)

A security reviewer needs assurance that "anywhere" does not mean "anywhere on disk." Today's
writer only ever joins a fixed, trusted directory (F11) — there was no caller-supplied path to
attack. This story is the one that introduces a caller-influenced destination (Q5/B: still
system-chosen by default, but the *mechanism* that resolves "collection root" must be the same
mechanism a future explicit destination would use) and requires it be checked exactly like every
other write into a collection.

**Why this priority**: P0 — ships in the same PR as US-1/US-2; this is not a follow-up hardening
pass, it is the gate that makes US-1 safe to land at all (Hard Constraint #6/D-SEC).

**Independent test**: a unit test constructs a `CollectionRoot` over a temp directory containing a
symlink pointing outside it, and asserts the view writer refuses to write through the symlink and
refuses any resolved path outside the root, using the same fixtures `contain_test.go` already uses
for other `CollectionRoot` consumers.

**Acceptance Scenarios**:
1. **Given** a write destination that resolves (after symlink resolution) outside the collection
   root, **When** `create_view`/`write_view` is called, **Then** the write is refused with a
   refusal naming containment, and nothing is written.
2. **Given** a proposed view filename containing a path-traversal segment (`..`) or an
   OS-reserved/illegal component, **When** the view is written, **Then** `pathsafe` rejects it
   before any file is touched (F11).
3. **Given** a legitimate destination inside the collection root, **When** the view is written,
   **Then** it succeeds and is immediately discoverable by the next `knowledge_describe`/
   `knowledge_find` call (US-1).

### US-4 — `LibraryEntry` states whether a file is a view, and what kind (P1)

The SPA (and any other API consumer) needs to know, from an ordinary directory listing, that a
file is a view and which of the 8 kinds it is, without a second round trip per file.

**Why this priority**: P1 — depends on US-1's discovery existing; blocks US-5/US-6 (the UI can't
draw an icon it has no field for).

**Independent test**: `GET /api/v1/library/{workspace_id}/entries` on a directory containing a
`.view` file returns that entry with `is_view: true` and a `view_kind` matching the file's
declared `kind`, verifiable with `curl`/an integration test with no SPA involved.

**Acceptance Scenarios**:
1. **Given** a `.view` file declaring `kind: calendar`, **When** the directory is listed,
   **Then** the entry carries `is_view: true`, `view_kind: "calendar"`, and `view_label` set to
   its `DisplayLabel()` (F7).
2. **Given** a `.view` file declaring no `kind` (legal, F8), **When** the directory is listed,
   **Then** the entry carries `is_view: true` and `view_kind` absent (Edge Case EC-3) — never a
   fabricated default kind.
3. **Given** a `.view` file that fails to parse at all (malformed YAML), **When** the directory is
   listed, **Then** the entry still carries `is_view: true` (it IS a view file by extension) with
   `view_kind` absent — the listing never fails because one file is broken (mirrors F1's
   `RejectViewUnreadable`/`RejectViewInvalidYAML` posture: reported, not hidden).

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

### US-6 — Clicking a view opens it in the Library preview pane (P2)

Per founder direction, a view opens in the Library PREVIEW pane, never a modal — the same surface
every other Library entry already opens in (F6, F2).

**Why this priority**: P2 — depends on US-4/US-5; this is the payoff, not the plumbing.

**Independent test**: `classifyLibraryEntry` (F2/F6) returns `'view'` for a `.view`-extension entry
in isolation, with no rendering involved — the existing unit-test pattern
(`libraryPreviewKind.test.ts`) already covers `'base'` this way.

**Acceptance Scenarios**:
1. **Given** a `.view` file, **When** `classifyLibraryEntry` runs on its `LibraryEntry`, **Then**
   it returns the new `'view'` kind (added to `LIBRARY_PREVIEW_KINDS`, F2), extension-matched
   exactly like `'base'` is today, and placed after the mime-driven checks per the file's own
   documented ordering rule (F2's header comment).
2. **Given** a Library entry classified `'view'`, **When** `LibraryPreviewPane` renders its body,
   **Then** `renderBody`'s switch gains a `case 'view':` that mounts a preview reusing
   `ViewPartsRenderer` (F6) to draw the view's evaluated rows — not a re-implementation of that
   renderer, and not a modal.
3. **Given** the click happens from the tree (US-5) versus from a `.base` file's own view tabs
   (`BasePreview`, F6, unaffected by this spec), **When** either path opens a view, **Then** both
   land in the same preview pane component, honoring EMB-027's "exactly one renderer per kind"
   rule (`libraryPreviewVariant.ts` header, cited for continuity — not modified by this spec).

---

## 7. Behavioral Contract

Primary flows:
- When a `.view` file exists anywhere inside a collection's real (symlink-resolved) boundary, the
  system includes it in every view listing/resolution an agent tool performs, identically to a
  view that used to live under the fixed directory.
- When an agent creates a view with no destination, the system writes it at the collection root as
  an ordinary, visible file.
- When a `.base` file is imported, the system writes the resulting view(s) beside that `.base`
  file.
- When a directory is listed, the system reports, for every `.view`-extension entry, whether it is
  a view, its declared kind (if any), and its display label — without requiring a second request.
- When a person clicks a view in the Library tree, the system opens it in the same preview pane
  every other Library entry uses, rendering its evaluated rows with the existing part renderer.

Error flows:
- When two views anywhere in the collection declare the same `name`, the system rejects the
  second one exactly as it rejects a same-directory collision today (D-DEDUP) — never silently
  picking one, never silently dropping one.
- When a write's resolved destination would leave the collection root (following a symlink or a
  traversal segment), the system refuses the write and writes nothing.
- When a `.view` file cannot be parsed, the system still lists it as `is_view: true` and reports
  the parse failure through the existing rejection-code channel (F1) — never a listing failure,
  never a silent drop.

Boundary conditions:
- When a `.view` file declares no `kind`, the system reports `view_kind` absent, and the tree
  falls back to a generic view icon — never an invented default kind.
- When the collection contains zero `.view` files, discovery returns the same "0 views" shape
  `knowledge_describe` already renders for an empty `ViewSet` today.

---

## 8. Edge Cases

- **EC-1 — Symlinked subfolder.** A `.view` file reachable only through a symlinked directory is
  never traversed to (F10, FR-044's existing rule) — it is invisible to discovery, exactly as any
  other content behind a symlink already is. Not a regression: same rule every other collection
  walk already follows.
- **EC-2 — Control-plane directories.** A `.view`-suffixed file placed inside `.omnipus-vault`,
  `.obsidian`, `.git`, or `.trash` is never discovered (`WalkContained` skips these at any depth,
  F10) — this is what makes Q4's "orphaned old-format files" scenario possible and is why Q4
  proposes a diagnostic rather than silence.
- **EC-3 — No declared `kind`.** Legal under the schema (F8: only `name` is required). Listing
  reports `is_view: true`, `view_kind` absent; the tree shows a generic fallback view icon
  (US-5 AS-1); the preview pane still opens and renders the view's rows normally — `kind` only
  ever drove the composer/icon, never the query itself.
- **EC-4 — Two views, same `name`, different folders.** Rejected (D-DEDUP unchanged) — reported
  through the same `RejectViewDuplicateName` channel that a same-directory collision already uses
  today, now reachable from a wider input set (§4).
- **EC-5 — A view file renamed or moved via an ordinary Library file operation (Q3/B).** The
  view's `Def.Name` (and therefore every agent-facing reference to it) is unaffected; the next
  discovery walk finds it at its new path. If the rename/move produced a NEW name collision with
  another view (unlikely, since `Def.Name` is inside the file content and untouched by a rename —
  this only happens if the move brings two independently-named-identically files into scope
  together for the first time, e.g. two collections merging), EC-4 applies.
- **EC-6 — A `.view` file that is actually just YAML a user wrote by hand for an unrelated
  purpose, using the `.view` extension by coincidence.** Out of scope to prevent (Q1/A accepts
  this risk in exchange for a one-glob, no-content-sniff discovery filter); it will be listed as
  `is_view: true` and attempt to parse as a `ViewDef`, surfacing a rejection code if it is not one
  (F1) — same posture as a malformed genuine view (EC in US-4 AS-3).
- **EC-7 — Very large collection (F12's 100,000-file scale).** Discovery cost is O(collection
  files) plus O(views) parses for kind-tagging (D-ICON) — Q2 requires a benchmark before landing,
  not an assumed number.

---

## 9. Explicit Non-Behaviors & Safeguards

### Qualitative prohibitions

- The system must not silently migrate or rewrite files under the old `.omnipus-vault/views/`
  directory — greenfield rule (Q4); the only allowed reaction to leftover files there is the
  diagnostic WARN in Q4/B, never an automatic move or rewrite.
- The system must not broaden a view's filter semantics while changing where the file lives —
  F1's "a view is never broadened on the operator's behalf" (FR-105) is untouched by this spec;
  nothing here parses or rewrites `filter` trees.
- The system must not follow a symlink during discovery or during a write's destination
  resolution, under any circumstance (F10's FR-044, reused unmodified).
- The system must not let a single malformed `.view` file fail an entire directory listing or an
  entire `knowledge_describe`/`knowledge_find` call (F1's existing per-file rejection posture,
  reused).
- The system must not invent a `view_kind` value for a file that declares none (EC-3) — an
  agent-guessed or heuristically-inferred kind would misrepresent what `create_view` actually
  wrote and is exactly the kind of silent approximation F1 already prohibits for filters.
- The system must not require a new required argument on `create_view`/`write_view` to preserve
  today's zero-argument call pattern (Q5/B) — existing agent prompts/skills that call these tools
  today must keep working unchanged.

### Machine-verifiable constraints

**Contract (LibraryEntry, Hard Constraint #8):**
- `is_view` MUST be present (`true`) only when the entry's extension matches Q1's chosen
  extension; absent for every other entry (mirrors `is_knowledge_base`'s optionality, F2/D-CONTRACT).
- `view_kind`, when present, MUST be one of F8's exact 8 enum values (`table, list, tiles, board,
  calendar, summary, trend, breakdown`) — generated from the same `ViewDefKind` enum `create_view`
  already uses, never a hand-written parallel list (Hard Constraint #8).

**Security (containment, D-SEC):**
- A view write whose resolved destination is outside the collection's real (symlink-resolved)
  root MUST be refused before any filesystem write occurs.
- A view write whose destination path resolves through a symlink at any component MUST be refused
  (`ResolveContainedNoSymlink`, F10/F11), matching `WalkContained`'s own read-side rule.

**Data constraints:**
- The chosen extension (Q1) MUST be excluded from `pkg/library/entries.go`'s hidden-file rule
  exactly as `.base` already is (F2) — a view is not a dot-file and must not be hidden by default.
- `ViewDef.name` uniqueness MUST remain vault-wide (F7), evaluated over the full walk result
  (§4), not per-directory.

**Performance (Q2):**
- Discovery over a benchmark fixture at the repo's standing 100,000-file scale assumption (F12)
  MUST complete within the bound set by the landing-gate benchmark (Q2) — a number earned by
  measurement, not asserted here.

### Conservative type design

No new nominal Go type is introduced for a view's location — a collection-relative path is a
`string`, exactly as every other `WalkContained`/`CollectionRoot` consumer already treats it
(F10). `ViewDefKind` (generated, F8) is reused as-is for `view_kind`; no parallel Go or TS enum is
hand-written for it (Hard Constraint #8).

---

## 10. TDD Plan

| Order | Test Name | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestWalkContained_FindsViewExtensionAnywhere` | Unit | US-1 AS-1 | Views in a subfolder are returned by the discovery filter over `WalkContained`'s file list. |
| 2 | `TestLoadViews_DedupAcrossDirectories` | Unit | US-1 AS-3, EC-4 | Two same-named views in different folders both reject via the existing `RejectViewDuplicateName` code. |
| 3 | `TestLoadViews_ResolveByNameAcrossFolders` | Unit | US-1 AS-2 | `ViewSet.Resolve` finds a view by name regardless of which folder it lives in. |
| 4 | `TestCreateView_DefaultsToCollectionRoot` | Unit | US-1 AS-4, Q5 | No-destination `create_view` call lands the file at the collection root. |
| 5 | `TestVaultImport_WritesViewBesideBaseFile` | Unit | US-2 AS-1 | One-shot importer writes the resulting `.view` file in the `.base` file's own directory. |
| 6 | `TestVaultImport_DuplicateNameRejected` | Unit | US-2 AS-2 | Import collision uses the same rejection code as a manual `write_view` collision. |
| 7 | `TestWriteView_RefusesSymlinkEscape` | Unit | US-3 AS-1, EC-1 | Destination resolving outside the collection root through a symlink is refused, nothing written. |
| 8 | `TestWriteView_RefusesTraversalFilename` | Unit | US-3 AS-2 | A `..`-bearing or reserved-name filename is refused by `pathsafe` before any write. |
| 9 | `TestWriteView_SucceedsAndIsImmediatelyDiscoverable` | Integration | US-3 AS-3 | A legitimate write is found by the very next discovery call. |
| 10 | `TestLibraryEntries_IsViewAndKind` | Integration | US-4 AS-1 | Directory listing reports `is_view`/`view_kind`/`view_label` for a well-formed view. |
| 11 | `TestLibraryEntries_ViewWithNoKind` | Integration | US-4 AS-2, EC-3 | `view_kind` absent, `is_view` still true, for a kind-less view. |
| 12 | `TestLibraryEntries_MalformedViewStillListed` | Integration | US-4 AS-3, EC-6 | A malformed `.view` file still lists with `is_view: true`, no listing failure. |
| 13 | `TestClassifyLibraryEntry_ReturnsViewKind` | Unit (TS) | US-6 AS-1 | `classifyLibraryEntry` returns `'view'` for the chosen extension, positioned per F2's ordering rule. |
| 14 | `TestLibraryPreviewPane_ViewCaseMountsRenderer` | Unit (TS) | US-6 AS-2 | `renderBody`'s `'view'` case mounts a component that reuses `ViewPartsRenderer`, not a re-implementation. |
| 15 | `TestLibraryTree_IconPerKind` | Unit/Visual (TS) | US-5 AS-1 | 8 kinds + fallback render 9 distinct icon states. |
| 16 | `TestLibraryTree_ViewVsBaseIconDistinct` | Unit/Visual (TS) | US-5 AS-2 | A `.base` and a `.view` entry never share an icon. |
| 17 | `BenchmarkViewDiscovery_100kFiles` | Benchmark | SC-VA-004, Q2 | Establishes the real number for the performance gate — run before, not after, landing. |
| 18 | `TestOrphanedLegacyViewsDir_WarnsOnStartup` | Integration | Q4/B | A non-empty `.omnipus-vault/views/` after cutover produces the one-time diagnostic WARN, no other behavior change. |

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
2. **Existing tests that MUST continue to pass unchanged:** the current `RejectView*` unit tests
   in `pkg/records`, `libraryPreviewKind.test.ts`'s existing `'base'`/mime-driven assertions, and
   `knowledge_describe`'s existing VIEWS-section golden output for a fixture with no views.
3. **New regression tests protecting unchanged behavior:** test 2 above (dedup) and test 3
   (resolve) are as much regression tests as new-feature tests — they prove the *widened* input
   set still obeys the *unchanged* rule.
4. **Regression dataset:** a fixture collection identical to whatever `knowledge_describe`'s
   current "0 views" and "N views inline" golden fixtures use, with views moved from the old fixed
   directory into varied subfolders — output must be byte-identical to today's, modulo the new
   `Path` field (§4.4) that did not exist before.

---

## 11. Test Datasets

### Dataset A — Discovery locations

| ID | View location | Expected discovered? | Traces to |
|---|---|---|---|
| A1 | Collection root | Yes | US-1 AS-4 |
| A2 | One level deep (`notes/x.view`) | Yes | US-1 AS-1 |
| A3 | Several levels deep (`a/b/c/x.view`) | Yes | US-1 AS-1 |
| A4 | Inside `.omnipus-vault/` | No (skipped, EC-2) | EC-2, Q4 |
| A5 | Inside `.git/` | No (skipped) | EC-2 |
| A6 | Inside `.obsidian/` | No (skipped) | EC-2 |
| A7 | Behind a symlinked directory | No (never traversed, EC-1) | US-3 AS-1, EC-1 |
| A8 | Empty collection (zero `.view` files) | N/A — "0 views" shape | Behavioral Contract, boundary |

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

### Dataset D — Write-path containment (security, D-SEC)

| ID | Destination input | Expected | Traces to |
|---|---|---|---|
| D1 | Collection root (default, Q5/B) | Written | US-1 AS-4 |
| D2 | A real subfolder inside the collection | Written | US-3 AS-3 |
| D3 | A path containing a `..` traversal segment | Refused, nothing written | US-3 AS-2 |
| D4 | A path that resolves through a symlink to outside the root | Refused, nothing written | US-3 AS-1, EC-1 |
| D5 | A filename that is an OS-reserved device name (e.g. `CON`) | Refused by `pathsafe` before write | US-3 AS-2 |
| D6 | An empty/whitespace-only destination | Treated as "no destination" → collection root (Q5/B), not an error | US-1 AS-4 |

### Dataset E — Legacy directory (Q4)

| ID | Scenario | Expected | Traces to |
|---|---|---|---|
| E1 | `.omnipus-vault/views/` does not exist (true fresh install) | No warning, nothing special | Q4 |
| E2 | `.omnipus-vault/views/` exists and is empty | No warning | Q4 |
| E3 | `.omnipus-vault/views/` exists with N legacy `.yaml` files after cutover | One-time WARN naming the directory and N | Q4/B |

---

## 12. Functional Requirements

- **FR-VA-001**: The system MUST discover every `.view`-extension file reachable inside a
  collection's real, symlink-resolved boundary, using the existing `WalkContained` containment
  and symlink-skip rules unmodified (F10, D-WALK).
- **FR-VA-002**: The system MUST NOT discover a `.view` file inside `.omnipus-vault`, `.git`,
  `.obsidian`, or `.trash`, at any depth (EC-2, reusing `scanSkippedDirNames`).
- **FR-VA-003**: The system MUST reject two views anywhere in the collection that declare the same
  `name`, using the existing `RejectViewDuplicateName` code, evaluated over the full discovery
  result rather than one directory (D-DEDUP, Dataset B1/B2).
- **FR-VA-004**: The system MUST continue to resolve a view by `Def.Name` first, then by
  `DisplayLabel()` with `ViewAmbiguousLabel` on a label collision, unchanged from today (F7,
  Dataset B3).
- **FR-VA-005**: `create_view` and `write_view` MUST succeed with no destination argument
  supplied, writing to the collection root (Q5/B, Dataset D1, D6).
- **FR-VA-006**: Any destination resolved for a view write MUST be checked with
  `CollectionRoot.ResolveContainedNoSymlink` (or equivalent) before any filesystem write; a
  destination outside the real collection root or reached through a symlink MUST be refused with
  nothing written (D-SEC, Dataset D3/D4).
- **FR-VA-007**: Any filename proposed for a view write MUST pass `pathsafe`'s component/name-shape
  validation before any filesystem write (D-SEC, Dataset D5).
- **FR-VA-008**: The one-shot `.base` importer MUST write each imported view's file in the same
  directory as the `.base` file it was imported from (US-2 AS-1).
- **FR-VA-009**: `contracts/components/schemas/LibraryEntry.yaml` MUST gain `is_view` (boolean,
  optional), `view_kind` (string, optional, one of the generated `ViewDefKind` enum values), and
  `view_label` (string, optional), following the 5-step contract-regeneration process (Hard
  Constraint #8) — no hand-written parallel type.
- **FR-VA-010**: `is_view` MUST be true for, and only for, an entry whose extension is the one
  chosen in Q1, independent of whether the file parses as a valid `ViewDef` (US-4 AS-3, Dataset C10).
- **FR-VA-011**: `view_kind` MUST be present only when the file both parses successfully and
  declares a `kind`; absent in every other case (never a fabricated or best-guess value) (EC-3,
  Dataset C9/C10).
- **FR-VA-012**: The chosen view extension MUST NOT be treated as a hidden entry by
  `pkg/library/entries.go`'s dot-prefix rule (F2, mirrors `.base`'s existing treatment).
- **FR-VA-013**: `classifyLibraryEntry` MUST return a new `'view'` member of `LibraryPreviewKind`
  for the chosen extension, added after the mime-driven `image`/`video` checks per the file's own
  ordering rule (F2, US-6 AS-1).
- **FR-VA-014**: `LibraryPreviewPane`'s `renderBody` MUST gain a `case 'view':` that reuses
  `ViewPartsRenderer` to draw the view's evaluated rows inside the standard preview pane, never a
  modal (US-6 AS-2, F6).
- **FR-VA-015**: The Library tree MUST render a distinct icon per `view_kind` value (8 states) plus
  one fallback state for an absent `view_kind`, and MUST NOT reuse the `.base` icon for a `.view`
  entry (US-5 AS-1/AS-2).
- **FR-VA-016**: A rename or move of a `.view` file through an ordinary Library file operation MUST
  NOT alter the file's `Def.Name` content, and the view MUST remain resolvable by that unchanged
  name after the move (Q3/B, EC-5).
- **FR-VA-017**: The system MUST NOT silently migrate, move, or rewrite any file under
  `.omnipus-vault/views/` (Q4). If that directory is non-empty after this feature ships, the
  system MUST log a one-time startup WARN naming the directory and the file count (Q4/B, Dataset
  E3) — no other behavior change.
- **FR-VA-018**: Discovery performance over a fixture at the repo's standing 100,000-file scale
  assumption MUST be measured by a benchmark before this feature lands, and the resulting number
  MUST be recorded as this spec's SC-VA-004 gate (Q2) rather than an assumed figure.

---

## 13. Success Criteria

- **SC-VA-001**: A view file placed anywhere inside a collection (any depth, not the former fixed
  directory) is found by `knowledge_describe`, `knowledge_find`, and the Library entries endpoint,
  with zero loss of reach compared to today's fixed-directory behavior (F5's "agents must do
  everything" rule) — verified by Dataset A rows A1–A3 all passing and F4's 8 call sites each
  having a passing test exercising their post-change behavior.
- **SC-VA-002**: Zero of the 8 call sites in F4 still reference `records.ViewsDir` after this
  feature lands (verified by a Grep/GitNexus re-sweep at CHECK time, per §1a's instruction to the
  implementing lead).
- **SC-VA-003**: A write whose destination would escape the collection root (via traversal or a
  symlink) is refused in 100% of Dataset D's D3/D4 cases, with zero bytes written in each refused
  case (verified by a filesystem-state assertion in the test, not just a returned error).
- **SC-VA-004**: Discovery over the 100,000-file benchmark fixture (FR-VA-018) completes within
  the number the benchmark itself establishes at landing time — this criterion is satisfied by the
  benchmark existing and being checked in CI, not by a number invented in this document.
- **SC-VA-005**: Every one of the 8 `ViewDefKind` values, plus the no-`kind` fallback, renders a
  visually distinct icon in the Library tree (Dataset C, 9/9 states covered by test 15).
- **SC-VA-006**: `make verify-contracts` is clean after the `LibraryEntry` schema change and
  regeneration (Hard Constraint #8) — zero hand-written types in `pkg/api/generated/` or
  `src/lib/api/generated/` implementing `is_view`/`view_kind`/`view_label`.
- **SC-VA-007**: A `.omnipus-vault/views/` directory left over from before this feature produces
  exactly one WARN log line per process startup while non-empty, and zero once emptied (Dataset
  E1–E3).

---

## 14. Reachability

Per CLAUDE.md's Definition of Done: green tests show correctness, not reachability. Each surface
below states who/what can actually invoke the finished feature.

| Surface | Reachable how | Registration/wiring evidence required at CHECK |
|---|---|---|
| `knowledge_describe` | Agent tool call, VIEWS section, unchanged invocation shape | Already registered for every agent per its existing policy entry (unchanged by this spec — no new tool, no new policy row needed); verify `renderViews` output includes a view planted outside the old fixed directory. |
| `knowledge_find` | Agent tool call, view-by-name resolution, unchanged invocation shape | Same — verify `ViewFindLoader.ServeRefusal`/serve path resolves a view planted anywhere in the collection. |
| `knowledge_configure` (`write_view`, `create_view`) | Agent tool call, unchanged required-argument shape (Q5/B keeps it zero-argument for destination) | Verify a call with no destination argument succeeds and the resulting file is later discoverable (closes the loop with `knowledge_describe`/`knowledge_find` above — a write nobody can then find is not reachable). |
| `.base` one-shot importer | Existing import flow (operator-triggered or agent-triggered per its current trigger, unchanged by this spec) | Verify the resulting view is discoverable exactly as an agent-written view would be — the importer is not a special case downstream of the write. |
| `GET /api/v1/library/{workspace_id}/entries` | Existing REST endpoint, no new route | Verify `is_view`/`view_kind`/`view_label` appear on the wire for a real listing, not just in a Go struct — `make verify-contracts` clean plus an integration test hitting the actual HTTP handler. |
| Library tree icon | SPA component, mounted wherever the Library tree already renders (no new route/screen) | A real (or storybook/snapshot) render showing a `.view` entry with its kind-specific icon — a backend field nobody reads in the tree is a library, not a feature (CLAUDE.md's own example of this exact failure mode). |
| Library preview pane `'view'` case | SPA component, mounted from the existing click-to-open flow every other kind already uses | A real click (or an equivalent Playwright/webapp-testing check) opening a `.view` entry and rendering rows via `ViewPartsRenderer` — not merely that the `case 'view':` branch compiles. |
| `.omnipus-vault/views/` orphan WARN (Q4) | Server-startup log line | Verify the WARN appears in a real process's stdout/log under Dataset E3's fixture, not merely that a function returning the warning string exists. |

**Two lines, never merged, per surface above**: "code correct and tested" (TDD plan §10 passing)
and "reachable by a user or agent" (this table's right-hand column, checked against the ACTUAL
registration/wiring, not the test suite).

---

## 15. Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-VA-001 | US-1 | US-1 AS-1, AS-2 | 1, 3 |
| FR-VA-002 | US-1 | EC-2 | (covered by test 1's fixture excluding control-plane dirs; add explicit negative case at implementation) |
| FR-VA-003 | US-1 | US-1 AS-3 | 2 |
| FR-VA-004 | US-1 | (F7, unchanged) | 3 |
| FR-VA-005 | US-1 | US-1 AS-4 | 4 |
| FR-VA-006 | US-3 | US-3 AS-1 | 7 |
| FR-VA-007 | US-3 | US-3 AS-2 | 8 |
| FR-VA-008 | US-2 | US-2 AS-1 | 5 |
| FR-VA-009 | US-4 | US-4 AS-1 | 10 |
| FR-VA-010 | US-4 | US-4 AS-3 | 12 |
| FR-VA-011 | US-4 | US-4 AS-2 | 11 |
| FR-VA-012 | US-1 | (F2 parity) | (covered by test 10's fixture asserting `is_hidden: false`) |
| FR-VA-013 | US-6 | US-6 AS-1 | 13 |
| FR-VA-014 | US-6 | US-6 AS-2 | 14 |
| FR-VA-015 | US-5 | US-5 AS-1, AS-2 | 15, 16 |
| FR-VA-016 | US-1 | EC-5 | (new test at implementation: rename-then-resolve) |
| FR-VA-017 | — (cross-cutting, Q4) | Dataset E | 18 |
| FR-VA-018 | — (cross-cutting, Q2) | Dataset A (scale) | 17 |

Every FR appears above. Two rows note a gap between the TDD plan's numbered tests and a scenario —
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

**Error:**
4. As a human, manually create two `.view` files in different folders with the same `name:` inside
   them; confirm both surface as broken/rejected somewhere a human would actually see it (not just
   in a log an agent reads).
5. As an operator, attempt (via whatever the lowest-level write path exposes, e.g. a crafted
   destination if one is ever exposed) to write a view outside the collection root; confirm the
   attempt visibly fails and no file appears outside the boundary on disk.

**Edge case:**
6. As a human, open a `.view` file with no declared `kind` from the tree; confirm it still opens
   and renders correctly despite showing a generic icon.
7. As an operator upgrading a dev checkout that still has files under `.omnipus-vault/views/`,
   start the server and check the log; confirm the one-time WARN appears and the old files are
   left untouched on disk.

---

## 17. Ambiguity Self-Audit

| What's ambiguous | Likely agent assumption | Resolved by |
|---|---|---|
| Exact extension string | `.yaml` (wrong — collides with schemas, F9) | Q1 |
| Whether kind-tagging happens at listing time (cost) or lazily on open | Listing time, silently, no perf discussion | Q2 |
| Whether renaming a view file renames the view | Yes, silently, breaking agent references | Q3 |
| Whether old `.omnipus-vault/views/*.yaml` files get auto-migrated | Some agents would "helpfully" write a migrator despite the greenfield rule | Q4 (explicitly prohibited, FR-VA-017) |
| Where a view lands with no destination given | Might invent same-folder-as-active-note heuristics | Q5 |
| Which of the three "kind" vocabularies (`kind`/`layout`/`part`) drives the icon | Could easily pick `layout` since it sounds closer to "how it's displayed" | D-ICON (§2), resolved without a founder question because the evidence (F8) is unambiguous once traced |

All six are either resolved by a founder question above (Q1–Q5) or decided with cited evidence
(D-ICON). None are left as a silent assumption.

---

*End of spec.*
