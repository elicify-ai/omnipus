# Library improvements — requirements

- **Branch:** `feat/library-improvements` (worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements`, currently at `6acd378e`, no commits yet)
- **Date:** 2026-08-21
- **Status:** Requirements gathered in interview. **Pre-design** — a design note and ADR are still required before implementation (design-first rule).
- **Source:** founder interview, 2026-08-21, with every option grounded in code, docs, measured vault data, or web research.
- **Revisions:** graph view descoped (§3.7); §4 added after a full user-path walkthrough and research into Obsidian's own vault model.

---

## 1. What this branch delivers

Two features, agreed in interview.

### Feature 1 — Omnipus knowledge base (issue #632), Parts A + B + C

Obsidian-compatible knowledge base: correct rendering, retrieval tools, deterministic authoring tools. **The Librarian layer is the only part of #632 not scoped here** (see open item O-1).

### Feature 2 — Render-first preview for web-renderable files

The preview pane renders the *thing*, not its source. Source appears only after pressing **Edit**.

---

## 2. Feature 2 — render-first preview

### 2.1 Scope

| Format | Behaviour | Basis |
|---|---|---|
| **HTML + self-contained bundles** | Renders as a live page. A folder of `index.html` + CSS + JS renders as the working thing | Agents produce bundles, not lone files; a page whose stylesheet 404s reads as our bug |
| **PDF** | Renders in the browser's native viewer | Perfect fidelity, no dependency, no rendering code |
| **Audio** | Native `<audio>` | Trivial; not covered today |
| **Office (DOCX/PPTX/XLSX)** | **Out of scope.** Keeps the Download card | Decided in interview |

### 2.2 Why Office is excluded

**No browser renders OOXML natively.** The only accurate cheap path is convert-to-PDF via LibreOffice/`soffice`, which is a new external runtime dependency (violates Hard Constraint #1) and shelling out (Constraint #2). Client-side converters give: XLSX good, DOCX fair, **PPTX poor** — and PPTX was the original motivating failure, so the cheap path fails precisely where it's needed. Full fidelity means Collabora CODE via WOPI, which is already a separate project (`omnipus-office`) in the vault with its own ADRs.

### 2.3 HTML execution model

**JavaScript executes**, in a sandboxed iframe with an **opaque origin** — `sandbox` *without* `allow-same-origin`. Scripts run, so agent-built dashboards work; they cannot read the `omnipus-session` cookie or call the API as the user.

Rationale: a page whose JS is silently inert looks broken in an undetectable way — the failure class ADR-061 exists to punish.

Reuses the hardened `/preview/` path (`pkg/gateway/rest_preview.go`), which already strips upstream `Content-Security-Policy` and `X-Frame-Options` and is built to be framed.

> **Departure to record in the ADR:** ADR-044 states chat renders a preview *link*, never an embedded iframe. Embedding in the Library pane is a deliberate exception and must be written down as one.

### 2.4 Known blocker

`pkg/gateway/rest_library.go:592` hard-codes `Content-Disposition: attachment`. Any inline embed today triggers a download. Needs an inline-disposition mode, with a correct `Content-Type` (the endpoint also sets `X-Content-Type-Options: nosniff`, so the type cannot be guessed by the browser).

---

## 3. Feature 1 — the knowledge base

### 3.1 Part A — Obsidian-compatible rendering

Per #632's observed table: wikilinks (plain, aliased, heading-anchored), embeds `![[...]]`, callouts, `==highlight==`, block ids, frontmatter, and **hiding `%%comments%%`** (currently displayed — a privacy defect).

Vault detection: **see §4.1** — Omnipus's own `.omnipus-vault/` marker, also accepting `.obsidian/` when present. Deterministic, no content sniffing as the primary signal. (Supersedes #632's `.obsidian/`-only rule.)

### 3.2 Part B — index and retrieval

**Index engine: reuse `pkg/memrooms/index` (bleve scorch).**

> **This corrects issue #632.** The issue recommends reusing `pkg/utils/bm25.go`. That engine's own doc comment states: *"The engine is stateless between queries: no caching, no invalidation logic. All indexing work is performed inside `Search()` on every call."* It re-indexes the entire corpus per query — fine for tool discovery, unusable for a large vault.
>
> `pkg/memrooms/index` (922 lines) is a pure-Go, on-disk, memory-mapped bleve **scorch** index, already live in-tree. `github.com/blevesearch/bleve/v2` is already a **direct dependency**. Its stated design principles match #632's requirements nearly verbatim: derived and rebuildable from source, rebuild-on-corruption, reference-counted process-wide registry, bolt-lock timeout so contention errors rather than hangs.
>
> **Consequence: "huge index" needs zero new dependencies, and #632's flagged "explicit ADR decision on index storage" resolves to "reuse scorch" — no second SQLite user.**

It is memrooms-typed today (*"only knows about `pkg/memrooms` types"*), so reuse means generalising it or building a sibling on the same pattern.

Tools: `knowledge_search` (BM25) and `knowledge_graph` (`links`, `backlinks`, `unresolved`, `orphans`, neighbourhood).

**Multi-vault:** one search spanning every mounted vault, with result attribution.

### 3.3 Part C — deterministic authoring

All of it: `knowledge_link`, `knowledge_create`, `knowledge_set_property`, `knowledge_append_section`, `knowledge_tasks`, `knowledge_move`/`knowledge_rename`.

### 3.4 Human reading surface

**Search, outline, backlinks.** (A visual graph view was considered and descoped — see §3.7.)

Rationale: the index and backlink graph are built regardless, so the marginal cost is UI. It also makes index correctness *observable by the operator* rather than only through an agent's report.

### 3.5 Layout — adaptive reading mode

```
POP-OUT (split)
┌─────────┬────────────────┬────────┐
│ ⌕ search │  # Note title  │ OUTLINE│
│         │                │  · H2  │
│ ▸ vault │  Body text…    │  · H2  │
│  · note │  [[wikilink]]  │        │
│  · note │                │ LINKED │
│         │                │  ← 3   │
└─────────┴────────────────┴────────┘
DOCKED (narrow) → right rail collapses to two toggles
```

- Vault file selected → preview pane becomes a note reader with a right rail (outline + backlinks).
- Search lives in the explorer header.
- Constraint honoured: the docked aside was judged too narrow to split (operator direction, 2026-08-04), so it degrades to toggles rather than cramming.

### 3.6 Scale target — beat Obsidian

Researched. Obsidian **has no search index**; it loads all file metadata into memory at startup. Measured community reports:

| Vault size | Obsidian behaviour |
|---|---|
| ~12,000 notes | *"daily operations feel sluggish… seriously degraded"* |
| ~35,000 files | ~10 min initial index; **graph view crashed the app** |
| ~104,000 files | **27.1s startup, 2.6–3.3 GB JS heap** (48k → 19.0s, 892 MB) |

Obsidian's team: *"we don't expect that breaking past the 100k+ is gonna be easy."*

**Target:** on-disk index, no startup metadata load, no multi-GB heap, sub-second query at 100k+. Bleve scorch clears this comfortably. **Reference measurement:** current vault = 748 notes, 6.2 MB markdown, 2,856 files, 85 MB total — i.e. today's vault is ~1% of the design target.

### 3.7 Graph view — DESCOPED

**A visual graph view is not part of this branch** (founder decision, 2026-08-21). It was briefly scoped as a global, server-side-computed view before being cut.

What is cut: the visualisation only — node layout, rendering, and the Go layout engine behind it.

**What remains, and is unaffected:**

- `knowledge_graph` (Part B agent tool) — `links`, `backlinks`, `unresolved`, `orphans`, neighbourhood. These are index queries; none needs layout.
- The **backlinks rail** in the reading surface (§3.4).
- The link index itself, which every other feature depends on.

Why it was the right thing to cut first:

- It was **the largest single item in the branch and a from-scratch build**. The two existing graph components — `GraphView.tsx` (task dependencies) and `WorkspaceTeamGraph.tsx` (delegation) — both use `@dagrejs/dagre`, a hierarchical **DAG** layout. A knowledge graph is undirected and cyclic and needs force-directed layout. Neither is reusable, and there is no Go layout library in the tree.
- It is **the surface that fails at scale in every Obsidian report** — unusable at ~35k files, crashing the app; the standing community advice for large vaults is to disable it. Cutting it removes the branch's single biggest scalability risk.
- Backlinks deliver most of what people actually want from a graph, in a form that reads faster and costs nothing extra once the index exists.

If it returns later, it should return as a **local neighbourhood** view rooted at the current note — bounded by hop count rather than vault size, so it cannot degrade with scale.

### 3.8 Freshness — persisted manifest + incremental scan

Store path/mtime/size per file alongside the index. On vault open, scan and re-parse **only what changed**. The index survives restarts, so there is no cold rebuild and no startup penalty at any scale.

Rejected: `fsnotify` (new dependency, and least reliable on the iCloud-synced vault it would need to watch); background interval rescan (work when idle, still a stale window); explicit-reindex-only (contradicts #632's self-healing requirement).

Plus a **`doctor`** check reporting index-vs-disk drift on demand.

### 3.9 Vault location — already solved

**No work required.** `WorkspaceMount.host_path` is an absolute path anywhere on the machine, realpath-resolved, and immutable once stored — `pkg/workspace/mount_test.go:347` asserts *"HostPath must NEVER change on its own — FR-8.5 forbids silent re-binding."* The destructive verb for a mount is **REVOKE** (removes access, deletes nothing), not DELETE.

Consequence to hold onto: **writes inside a mount land on the operator's real disk** — the same files opened in the Obsidian app.

### 3.10 Rename safety — link rewriting, exceeding Obsidian

Renaming or moving a **note** rewrites every inbound link atomically under a write lock.

This matches Obsidian's model, confirmed from its docs: *"Obsidian can automatically update internal links in your vault when you rename a file."* Renames made **outside** Omnipus break links — accepted, same as Obsidian.

**Rewriting covers body links AND frontmatter — Obsidian does not do the latter.**

> **Measured justification:** 651 of 748 notes (**87%**) in the current vault carry frontmatter wikilinks (`up: "[[CRM]]"`, `related:` …). The Obsidian forum confirms frontmatter links are not auto-updated on rename. Matching Obsidian would silently sever the structural `up:` hierarchy across most of the vault on a single rename — in the operator's real files. Requires a YAML-aware writer that preserves the rest of the block; `knowledge_set_property` needs one anyway.

### 3.11 Determinism (hard requirement, from #632)

Parser-produced, never model-produced. Stated resolution algorithm; ambiguity reported, never guessed. Byte-identical rebuild from scratch, asserted in CI as a property test. The graph must be correct with **no agent running at all**.

### 3.12 Also in scope

- **Deep-linking** — selected file becomes URL-addressable (`?path=`). #632's obstacle 2; the shared mechanism for wikilink clicks, search results, backlink clicks, and agent-supplied links.
- **Two standalone markdown bugs**, both affecting *all* markdown in the product, not just vaults:
  - `%%comment%%` renders visibly — private asides are on screen.
  - **Every relative markdown link renders struck through** as *"Link removed: unsafe URL scheme"*, because `isSafeHref` (`src/lib/url-safe.ts`) calls `new URL(href)` with no base. This is also a prerequisite for links working at all.

---

## 4. User paths and lifecycle

Added 2026-08-21 after a path walkthrough and research into Obsidian's own model.

### 4.0 Obsidian's model, for reference

First launch offers exactly two options: **Create new vault** (name + Browse location → Create → opens empty → "Create your first note") and **Open folder as vault** (*"You don't need to do anything special to prepare a folder. If it contains `.md` files, Obsidian will recognise them immediately."*).

Also: **vault name is the folder name** (renaming the vault renames the folder); each vault owns its `.obsidian/` settings; **removing a vault only removes it from the list — files are untouched**; multiple vaults via a switcher.

### 4.1 What makes a folder a knowledge base

**Omnipus has its own marker — `.omnipus-vault/` — and also accepts `.obsidian/` when present.**

Rationale (founder decision): Omnipus is *compatible with* Obsidian, not a wrapper around it. This matches #632's own naming section — *"Obsidian is a format it speaks, not a product it wraps."*

Compatibility is preserved for free: an Omnipus-created vault opens in the Obsidian app, which creates its own `.obsidian/` on first open. **Omnipus never fabricates `.obsidian/`.**

> **Naming caution:** `.omnipus/` is already reserved inside workspaces (ADR-046 keeps it out of agent reach). The vault marker must be distinctly named so one path never means two things.

### 4.2 Vault identity — stored in the marker

The `.omnipus-vault/` marker carries the KB's identity: **name, template location, settings.** The mount remains purely the access grant.

Identity therefore **travels with the folder** — it survives moving it, re-mounting it, or opening it on another machine. Mirrors how Obsidian keeps settings in `.obsidian/`. Gives cross-vault search a real name to attribute results to (§3.2 multi-vault).

### 4.3 Creating a vault — workspace first, movable later

**Omnipus creates new vaults inside the workspace's own `work/` directory**, not at an arbitrary host path. No new host-write capability, no change to mount validation.

> **Why this was necessary:** `pkg/workspace/mount.go:64` — `ErrMountTargetInvalid = "workspace: mount target is not an existing directory"`, enforced by `os.Stat` + `IsDir()`. **Omnipus can only mount folders that already exist and cannot create one on the operator's disk.** Obsidian's primary onboarding path has no equivalent here.

**Moving it out later is supported** — `MoveInto` (`pkg/library/transfer.go`) explicitly handles it: *"work tree into a mounted folder is exactly the case the Transfer dialog exists for."*

Three constraints on that move, to be surfaced in the UI rather than discovered:

1. **Not atomic.** `os.Root` cannot rename across independently-opened roots, so it is copy-then-delete. A cleanup failure after a successful copy is reported as an error (wrapping `errSourceCleanupFailed`) leaving a duplicate behind — deliberately, not silently.
2. **The destination must already be a mount.** Moving a vault to `~/Documents/MyVault` first requires mounting `~/Documents`, which the Add-mount dialog flags as *"This is a broad grant."*
3. **Paths change**, so the index must be re-rooted or rebuilt.

### 4.4 Creating a note — template-aware

**There is no "New note" action anywhere in the Library today** — only New Folder and Upload. Verified by search.

New note must be **template-aware, with templates defined by the vault**, seeding frontmatter and structure.

Rationale: the reference vault has a mandatory schema — base frontmatter on every filed note, a `status:unfiled` capture invariant, Johnny Decimal placement — which a blank note silently violates. #632's `knowledge_create` already specifies *"create a note at a path from a template"* for agents; the human must not get less.

### 4.5 Indexing lifecycle — search works, incompleteness is stated

Results come from what is indexed so far, with a **persistent, unmissable progress banner** naming the real numbers (e.g. *"Indexing — 1,240 of 98,000 notes. Results are incomplete."*).

Rationale: honours #632's *"loud or correct, never silent and wrong"* without locking the operator out during a first index. A partial result set can never be mistaken for a complete one. Rejected: silent background indexing, where *"search returns 3 of 30 real hits"* is indistinguishable from a vault with 3 matches.

### 4.6 Empty vault — real first run

Today an empty location shows a bare *"No files in this workspace yet."* with no action. A newly created vault is now a reachable path, so it gets a genuine first run: **create your first note, from a template** (§4.4). Mirrors Obsidian ending its create flow at "Create your first note".

### 4.7 Detach and re-add

Revoking a mount **deletes its derived index** — it is a cache, and an orphaned index wastes disk and risks stale answers if the folder is later re-mounted. Re-adding reindexes from scratch.

Consistent with existing semantics: a mount's destructive verb is **REVOKE** (removes access, deletes nothing), matching Obsidian, where *"removing a vault only removes it from the vault list."*

### 4.8 Vault moved or renamed on disk

Renaming or moving the vault folder **breaks the mount** — `host_path` is immutable by design (`pkg/workspace/mount_test.go:347`, FR-8.5 forbids silent re-binding). Obsidian by contrast treats renaming a vault as routine, because vault name and folder name are the same thing.

`MountStatus` (`pkg/workspace/mount.go:341`) already models the broken state. Required: **surface it and offer a re-point action** — not new machinery, but currently invisible.

### 4.9 Evicted / placeholder files

A cloud-synced vault with Optimize Storage on has files whose contents are not on disk. Indexing them would silently record empty notes.

**An evicted read must fail loudly, never index as empty** — matching `ev`, whose `read` is documented as *"loud on missing/evicted"*.

Measured: **0** `.icloud` placeholder stubs in the reference vault today, so this does not currently affect it. In scope for other users.

### 4.10 Concurrency and write safety — all three tiers

The reference vault is simultaneously an **Obsidian vault, a git repo, and a Syncthing folder** (`.git`, `.stfolder`, `.stignore` all present at its root), written by `ev` and 56 agents. Omnipus and its agents make at least five concurrent writers.

**`ev`'s model, from its own header** (the proven prior art to mirror):

- Reads: unlimited parallel.
- Single-file writes: atomic temp + `os.replace` in the target folder; **per-path advisory lock in `~/.cache`, never inside the iCloud vault**; plus a **shared vault lock**.
- Multi-file ops (move/rename with link rewriting): one **exclusive vault-wide write lock**, held only for the operation, with a **write journal in the cache recording planned rewrites first** — so a crash mid-rename is *detected by `doctor`*, never a half-rewritten graph.
- **Lock waits are bounded; expiry is a loud error, not a hang.**
- Index lives outside the vault. `delete` is soft.

**Those are advisory locks — they only coordinate processes that take them.** Hence three tiers, each with a different ceiling. **All three are in scope:**

| Tier | Writers | Approach |
|---|---|---|
| **1. Inside Omnipus** | gateway, agents, UI | **Real mutual exclusion.** Pattern already in-tree: `pkg/entity`'s 64-shard striped mutex + `fileutil.WithFlock` sidecar (proven by cross-process tests), plus `WriteFileAtomic` |
| **2. `ev`** | the CLI | **Real mutual exclusion by opting in** — Omnipus takes the same `~/.cache` lock files with the same shared/exclusive semantics. A small shim, deletable the day `ev` retires |
| **3. Uncoordinated** | Obsidian app, Syncthing, git, text editors | **Cannot be locked.** Detect-before-write (hash/mtime compare) and **refuse loudly** rather than overwrite |

Additionally: **copy `ev`'s write-journal pattern** for `knowledge_move`'s link rewriting, so a crash mid-rewrite is detectable by `doctor` rather than leaving a half-rewritten graph.

> **Platform limit:** `fileutil.WithFlock` is a documented **no-op on Windows** (`pkg/fileutil/flock_windows.go`). Tier 1 and tier 2 cross-process guarantees are **POSIX-only**; on Windows only in-process protection applies. Consistent with the rest of the file-store family (ADR-054 §5).

---

## 5. Open items

| # | Item | Note |
|---|---|---|
| **O-1** | **Librarian layer** — in or out? | #632's judgement layer (proposes links, spots duplicates, flags orphans). Not scoped in interview; #632 leaves its form open (core agent / scheduled routine / both). The *layering* is not open: no part of graph correctness may depend on it. |
| ~~O-2~~ | ~~Where the index is stored on disk~~ | **Resolved — index lives OUTSIDE the vault**, under `$OMNIPUS_HOME`. `pkg/memrooms/index` defaults to `<room_root>/.index/bleve/`, which for a mount would write a large index into the operator's real vault and sync it. `ev` sets the precedent explicitly: its SQLite index and its lock files are in `~/.cache`, *"never inside the iCloud vault"*. Reuse must override the default location. |
| **O-3** | Rename: automatic vs prompt | Obsidian offers both (default automatic). Not asked. |
| **O-4** | Which audio formats | Not asked. |
| **O-5** | Non-markdown files in a vault | Attachments — indexed as metadata only, or skipped? Bears directly on scale (the researched 104k-file vault was ~half images). |
| ~~O-6~~ | ~~Graph interaction model~~ | **Closed** — graph view descoped (§3.7). |
| ~~O-7~~ | ~~Concurrent-edit posture~~ | **Resolved — see §4.10** (all three tiers: internal locks, `ev` lock-protocol interop, detect-and-refuse for uncoordinated writers). |
| **O-8** | `ev` lock-file contract | Tier-2 interop (§4.10) depends on `ev`'s `~/.cache` lock paths and shared/exclusive semantics being a **documented, stable contract**, not an implementation detail. Needs agreeing on the `ev` side before Omnipus depends on it. |
| **O-9** | Vault-marker directory name | `.omnipus-vault/` is the working name (§4.1). `.omnipus/` is unavailable — already reserved inside workspaces by ADR-046. Confirm the final name before it is written to real folders, since changing it later means migrating operators' vaults. |
| **O-10** | Windows write safety in vaults | `fileutil.WithFlock` is a no-op on Windows, so tiers 1 and 2 give in-process protection only there. Acceptable per ADR-054 §5 for internal stores — but a *shared* vault with external writers is a weaker case for that precedent. Decide whether Windows gets a compensation or a documented limitation. |

---

## 6. Risks

1. **Scope.** This is all of #632 except the Librarian, plus the preview work. Any one of Parts A, B or C is a substantial piece. Recommend sequencing into shippable stages rather than one branch landing at once — #632 itself says the parts are *"useful independently and best shipped in this order."* **Descoping the graph view (§3.7) materially reduced this risk** but did not remove it — and the §4 path walkthrough then added back: a create-vault flow, a New-note action with templates, indexing progress UX, an empty-vault first run, broken-mount recovery, evicted-file handling, and a three-tier concurrency model. **Net scope is larger than before the graph was cut.** Sequencing is now the main lever left.
2. **Real-file blast radius.** Every write goes to the operator's actual vault. Rename link-rewriting, authoring tools and any bulk operation need to be correct on first release, not iterated in production.
3. **Process.** The design-first rule requires a founder-visible design note in the vault plus an ADR before delivery. Neither exists yet. #632 also explicitly asks for an ADR decision on index storage — this document proposes an answer (§3.2, §O-2) but it is not yet ratified.

---

## 7. Corrections this interview produced

Worth carrying back to issue #632:

1. **`pkg/utils/bm25.go` is the wrong engine** for a scalable KB — it re-indexes the whole corpus per query. Use `pkg/memrooms/index` (bleve scorch), already a direct dependency.
2. **Index storage needs no SQLite decision** — scorch resolves it with zero new dependencies.
3. **Frontmatter link rewriting must exceed Obsidian**, on measured evidence (87% of notes affected).
4. **The graph view is the known scale failure** — unusable at ~35k files in every report. Omnipus is not shipping one (§3.7); `knowledge_graph`'s link queries and the backlinks rail cover the need without the failure mode.
