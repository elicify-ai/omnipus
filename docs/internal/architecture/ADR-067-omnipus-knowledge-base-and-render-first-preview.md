# ADR-067 — The Omnipus knowledge base, and render-first preview

- **Status:** Proposed (2026-08-22) — **revision 2**, after adversarial review (`ADR-067-…-review.md`, verdict BLOCK: 5 critical, 18 major, 9 minor, 7 observations). Every finding is answered in Appendix A.
- **Implements:** [issue #632](https://github.com/elicify-ai/omnipus/issues/632); founder interview 2026-08-21 (requirements: [`../specs/library-improvements-requirements-2026-08-21.md`](../specs/library-improvements-requirements-2026-08-21.md))
- **Builds on:** [ADR-063](ADR-063-unified-file-access-engine-and-mounts.md) (workspace mounts), [ADR-046](ADR-046-unified-filesystem-workspace-model.md), [ADR-044](ADR-044-preview-on-main-listener.md), [ADR-037](ADR-037-remove-global-delegation-policy.md) (workspace = trust boundary), [ADR-054](ADR-054-entity-config-separation.md) §5 (flock platform limits)
- **Corrects:** #632's index-engine recommendation (D3), its `.obsidian/`-only detection rule (D1), its agent-only framing of Part B (D8)
- **Branch:** `feat/library-improvements`

---

## 1. Context

### 1.1 The Library reads files; it does not understand them

The Library (ADR-063) lists, previews, edits, renames, moves and deletes files across the
workspace `work/` tree and mounted folders. It has no notion of a *document collection*: no
search, no cross-document structure, no way to ask "what refers to this?".

For an operator whose knowledge lives in an Obsidian-format vault, Omnipus can show them one
note and nothing else. For agents it is worse: grep is the only retrieval mechanism, and
there is no way at all to ask what links to a note.

### 1.2 Obsidian's ceiling is architectural, and we can clear it

Obsidian has **no search index**; it loads every file's metadata into memory at startup.
Measured community reports: ~12,000 notes → "daily operations can feel sluggish"; ~35,000
files → ~10 min initial index and a graph view that crashed the app; ~104,000 files → 27.1s
startup and 2.6–3.3 GB JS heap. Obsidian's team: *"we don't expect that breaking past the
100k+ is gonna be easy."*

Omnipus already ships a pure-Go, on-disk, memory-mapped BM25 index — `pkg/memrooms/index`
(bleve **scorch**) — with `github.com/blevesearch/bleve/v2` an existing **direct** dependency.

**This ADR's own performance targets** (absent in revision 1; a claim with no threshold cannot
be falsified):

| Metric | Target | Measured at |
|---|---|---|
| `knowledge_search` p95 latency | < 500 ms | 100,000 notes |
| Peak RSS during initial index | < 512 MB | 100,000 notes |
| Peak RSS steady-state (index open, idle) | < 64 MB | 100,000 notes |
| Time to first usable (partial) result | < 5 s from KB open | any size |
| Incremental reconcile, no changes | < 2 s | 100,000 files |

Reference corpus for acceptance: a synthesised 100k-note fixture vault. The operator's real
vault (748 notes, 6.2 MB markdown, 2,856 files) is ~1% of target and is **not** a valid
scale test.

### 1.3 Writes land on the operator's real disk

A mount's `host_path` is an absolute path on the operator's machine, realpath-resolved and
immutable (FR-8.5). A write inside a mount modifies the operator's actual files — the same
files their Obsidian app, editor and sync agent also touch.

The reference vault is simultaneously an Obsidian vault, a **git repo** and a **Syncthing
folder** (`.git`, `.stfolder`, `.stignore` at its root), written by the `ev` CLI and by 56
agents. That is the target environment, not an edge case.

### 1.4 The preview pane shows source where it should show the document

`src/components/library/preview/libraryPreviewKind.ts` classifies into six kinds — `image`,
`video`, `markdown`, `mermaid`, `text`, `other`. HTML falls into `text` and renders as
source; PDF falls into `other` and renders as a download card.

---

## 2. Decision

Each decision carries **acceptance criteria** (AC). A decision without a falsifiable AC is not
ready to implement.

### D1 — The Omnipus knowledge base, compatible with Obsidian

Omnipus gets **its own** knowledge base that *speaks* the Obsidian on-disk format. It does not
wrap, depend on, or require the Obsidian app. Agent tools are named `knowledge_*`.

**A folder is a KB if its root contains `.omnipus-vault/` or `.obsidian/`.** Either alone
suffices. Detection is one deterministic directory check — no content sniffing.

Omnipus writes `.omnipus-vault/` when it creates or initialises a KB. **Omnipus never creates
`.obsidian/`** — the Obsidian app creates its own configuration on first open, so
compatibility costs us nothing.

**The name `.omnipus-vault/` is decided, not provisional** (revision 1 left it open while
stating the rule normatively — an ADR cannot do both). It is distinct from `.omnipus/`, which
already names the workspace memory-room directory at `workspaces/<id>/.omnipus/`
(`pkg/workspace/instructions.go`). That is a *sibling of* `work/`, so there is no structural
path collision — the reason for a distinct name is that one identifier must not mean two
things across the product.

**Marker trust:** a marker only takes effect inside an already-granted mount, so it cannot
widen access. But marker *contents* (templates, settings) are **operator data, not Omnipus
configuration**: they are read as data, never executed, never interpolated into a prompt
without escaping, and template-driven writes are subject to D12's containment rules.

> **AC-1.1** A folder containing only `.obsidian/` is detected as a KB.
> **AC-1.2** A folder containing only `.omnipus-vault/` is detected as a KB.
> **AC-1.3** A folder of `.md` files with neither marker is **not** a KB.
> **AC-1.4** Detection performs no file-content reads (asserted by a read-counting fake).

### D2 — KB identity lives in the marker

`.omnipus-vault/` holds the KB's display name and its template directory. The mount record
remains **only** the access grant.

Scope note: revision 1 also placed a general "settings" payload here. That was speculative —
no requirement depended on it. **The marker holds a name and a `templates/` directory, and
nothing else** until a concrete setting justifies more.

> **AC-2.1** Moving a KB folder and re-mounting it elsewhere preserves its display name with
> no Omnipus-side migration.

### D3 — One index engine: bleve scorch, stored outside the KB, keyed by realpath

Built on **bleve scorch**, following the pattern proven by `pkg/memrooms/index` (derived and
rebuildable, rebuild-on-corruption, reference-counted process-wide registry, bounded
bolt-open timeout).

> **Corrects #632**, which recommends `pkg/utils/bm25.go`. That engine's doc states: *"The
> engine is stateless between queries: no caching, no invalidation logic. All indexing work is
> performed inside `Search()` on every call."* It re-indexes the whole corpus per query —
> correct for tool discovery, unusable for a vault. #632's requested ADR decision on index
> storage is answered: **scorch, no second SQLite user, no new dependency.**

**Stored outside the KB**, under `$OMNIPUS_HOME`. `pkg/memrooms/index` defaults to
`<root>/.index/bleve/`; that default MUST be overridden. Writing a large derived index into
the operator's folder would pollute, sync and version it. `ev` sets the precedent: its index
and locks live in `~/.cache`, *"never inside the iCloud vault"*.

**Index identity is the realpath of the KB root — never workspace+mount.** The same host
folder can be mounted into several workspaces, and twice into one: `CreateMount` checks
*name* collisions only, never `HostPath`. One corpus therefore gets exactly one index, shared
and **reference-counted** by the mounts pointing at it.

**Index file permissions:** the index contains full note bodies. It is created `0700`
(directory) / `0600` (files), like the credential store.

> **AC-3.1** Mounting one host folder into two workspaces produces **one** index directory.
> **AC-3.2** The index path is under `$OMNIPUS_HOME` and no file is created inside the KB root
> during indexing (asserted by a before/after tree diff of the KB).
> **AC-3.3** Index directory mode is 0700, file mode 0600.

### D4 — Freshness: persisted manifest plus incremental scan

A manifest (path, size, mtime, content hash) persists alongside the index. On KB open,
Omnipus scans and re-parses **only what changed**. The index survives restarts; there is no
cold rebuild and no startup penalty.

**Indexing is batched with bounded memory** — a chunk size and commit cadence, never one
whole-corpus batch. Revision 1 inherited `pkg/memrooms/index`'s `rebuildLocked`, which loads
every document into a single in-memory batch; at 100k notes that is the exact shape §1.2
criticises Obsidian for.

`doctor` reports index-versus-disk drift.

> **AC-4.1** Peak RSS during a 100k-note initial index stays under the §1.2 budget (measured,
> not asserted).
> **AC-4.2** A no-change reconcile over 100k files completes within the §1.2 budget.
> **AC-4.3** Editing one note externally and reopening the KB re-parses exactly one file.

### D5 — Search returns partial results, and says so

While indexing, search returns what is indexed so far with a **persistent, unmissable
statement of incompleteness**.

**Two phases, stated separately** — revision 1 specified a ratio without saying how the
denominator is known:

1. **Enumeration** — walking the tree to establish the total. Indeterminate progress:
   "Scanning — 12,400 files found so far." Never a ratio, never "0 of 0".
2. **Indexing** — a real ratio: "Indexing — 1,240 of 98,000 notes. Results are incomplete."

For an **incremental** reconcile the denominator is the changed-file count from the manifest
scan, and the banner appears only if that reconcile exceeds 2 seconds.

**Precedence:** if a KB is both empty and indexing, the indexing state wins; D13's empty-KB
first run appears only once indexing completes and the corpus is genuinely empty.

Silent background indexing is rejected: a search returning 3 of 30 real hits is
indistinguishable from a KB with 3 matches.

> **AC-5.1** During enumeration the banner shows an indeterminate state and never a ratio.
> **AC-5.2** A search issued mid-index returns results **and** the incompleteness statement in
> the same response payload (not a separate race-prone channel).
> **AC-5.3** A freshly created, still-indexing KB shows the indexing state, not the empty state.

### D6 — Deterministic graph construction, with containment

Extraction, resolution, indexing, link rewriting and drift detection are performed by a
parser. **No model is invoked anywhere in the indexing path.**

**Resolution algorithm**, in order:
1. Exact vault-relative path match (with and without `.md`).
2. Unique basename match across the KB.
3. **Tie-break when a basename is ambiguous:** shortest vault-relative path wins; if still
   tied, lexicographically-first path wins. **The result is additionally reported as
   ambiguous** — the link resolves deterministically *and* `unresolved`/`doctor` lists it, so
   determinism never hides the ambiguity.
4. No match → `unresolved`. Never guessed.

**Containment invariant (security):** every walked path and every resolved link target MUST
realpath inside the KB root. `[[../../../.ssh/id_rsa]]`, absolute-path links, and symlinks
escaping the root are reported as `unresolved` and **never followed or read**. Symlinks inside
the KB are **not followed** (skipped and reported), which also disposes of loop detection.

**Reproducibility is asserted behaviourally, not on bytes.** A scorch index is not
byte-reproducible — segment names, ids, merge scheduling and timestamps vary per run.
Revision 1's "byte-identical" property test would have failed non-deterministically or been
weakened into one that asserts nothing (`docs/internal/false-green-patterns.md`).

> **AC-6.1** For a fixture KB, deleting and rebuilding the index yields the **identical ranked
> result set** for a fixed query corpus, and identical `links`/`backlinks`/`unresolved`/
> `orphans` sets. Asserted in CI.
> **AC-6.2** A wikilink to a path outside the KB root resolves to `unresolved`, and no read of
> that path occurs (asserted by a read-recording filesystem fake).
> **AC-6.3** An ambiguous basename resolves per the tie-break **and** appears in `doctor`.
> **AC-6.4** The graph is correct with no agent running (all AC above execute with no LLM).

### D7 — Retrieval and authoring tools, workspace-scoped

**Retrieval:** `knowledge_search` (BM25; top-N; folder scoping; returns path, title, matched
excerpt) and `knowledge_graph` (`links`, `backlinks`, `unresolved`, `orphans`, neighbourhood).

**Authoring:** `knowledge_create`, `knowledge_link`, `knowledge_set_property`,
`knowledge_append_section`, `knowledge_tasks`, `knowledge_move` / `knowledge_rename`.

**Isolation (security).** Both retrieval tools are scoped to **the calling agent's workspace's
mounts**. Resolution path: agent → workspace → `AllowedMountRoots(home, workspaceID)` → KBs
within those roots. Mounts are per-workspace and ADR-037 makes workspace membership the
delegation trust boundary; an unscoped "search every mounted vault" would let an agent read a
KB mounted only into a different workspace. **Cross-workspace search is out of scope** and
would need its own ADR section and an explicit operator grant.

**Cost bounds.** `knowledge_search` accepts `top_n` ≤ 100 (default 20), returns excerpts
capped at 512 bytes each, and is rate-limited per agent. `knowledge_graph` neighbourhood
queries are bounded by hop count ≤ 3 and node count ≤ 500. Unbounded full-corpus queries in a
loop are a plausible self-DoS at 100k documents.

**Authoring rules:** fail loudly rather than write something wrong; idempotent where the
operation allows; verify after writing; never require the agent to emit raw `[[…]]`, YAML or
heading anchors.

> **AC-7.1** An agent in workspace A issuing `knowledge_search` gets **zero** hits from a KB
> mounted only in workspace B. Negative test, required.
> **AC-7.2** `knowledge_graph backlinks` on a note returns links expressed as `[[Note]]`,
> `[[Note|alias]]`, `[[folder/Note]]` and `[[Note#Heading]]` — all four forms.
> **AC-7.3** `top_n` above the cap is clamped, not errored, and the clamp is reported.

### D8 — The human gets the retrieval surface too

> **Corrects #632**, which frames Part B as agent tools only.

For a KB the Library gains **search**, a document **outline**, and **backlinks**. The index is
built regardless, so the marginal cost is UI — and it makes index correctness observable by
the operator rather than only through an agent's account of it.

**No visual graph view.** It is the surface that fails first in every Obsidian report
(unusable at ~35k files), and backlinks deliver most of its value in a form that reads faster.
If ever added it MUST be a bounded local-neighbourhood view, never a whole-corpus render.

### D9 — Adaptive reading layout

When the selected file belongs to a KB, the preview pane becomes a note reader with a right
rail (outline + backlinks); search sits in the explorer header. In the docked aside the rail
collapses to toggles rather than splitting.

Source for the no-split constraint: the operator direction recorded in
`src/routes/_app/library.tsx`'s `layout="split"` comment — *"Side-by-side here, stacked in the
docked aside (operator direction, 2026-08-04) … the narrow docked aside would be unusable cut
in two."*

### D10 — Rename and move rewrite inbound links, including frontmatter

Renaming or moving a note within a KB rewrites every inbound link to it.

**The guarantee is journalled and crash-recoverable, not atomic.** Revision 1 said
"atomically", which contradicts D14's write journal, D11's copy-then-delete move, and
residual 4. There is no filesystem primitive that renames a file and rewrites N others
atomically. The real guarantee: planned rewrites are journalled before any are applied, so a
crash leaves a state `doctor` detects and can complete or roll back.

Matches Obsidian, which *"can automatically update internal links in your vault when you
rename a file."*

**Rewriting covers body links AND frontmatter — Obsidian does not do the latter.** Measured:
651 of 748 notes (**87%**) in the reference vault carry frontmatter wikilinks (`up:`,
`related:`). Matching Obsidian would silently sever the structural hierarchy across most of
the vault on one rename, in the operator's real files. Requires a YAML-aware writer that
preserves the remainder of the block.

A rename performed **outside** Omnipus breaks links. Accepted — Obsidian has the identical
limitation.

> **AC-10.1** Renaming a note with N inbound links rewrites all N, in body and frontmatter.
> **AC-10.2** Killing the process mid-rewrite leaves a journal that `doctor` reports, and
> completing it produces the same end state as an uninterrupted run.
> **AC-10.3** A note whose frontmatter is non-trivial (comments, anchors, nested lists)
> survives rewriting with only the link value changed — byte-compared.

### D11 — Creating a KB: workspace-first, movable

Omnipus creates a new KB inside the workspace's `work/` tree. It does **not** gain the ability
to create directories at arbitrary host paths.

> Mounting requires a pre-existing directory —
> `ErrMountTargetInvalid = "workspace: mount target is not an existing directory"`, enforced by
> `os.Stat` + `IsDir()`. Omnipus therefore has no create-a-vault path at all today, while
> Obsidian's primary onboarding path is exactly that.

Relocating afterwards uses the existing transfer path; `MoveInto` already supports work-tree →
mount. Three properties MUST be surfaced in the UI rather than discovered:

1. **Copy-then-delete, not atomic** (`os.Root` cannot rename across independently-opened
   roots). A cleanup failure after a successful copy is reported as an error leaving a
   duplicate — deliberately, not silently.
2. **The destination must already be a mount**, so moving to `~/Documents/MyVault` first
   requires mounting `~/Documents`, which the Add-mount dialog flags as a broad grant.
3. **Paths change**, so the index is re-keyed to the new realpath (D3) — a rebuild only if the
   manifest cannot be re-rooted.

### D12 — Notes are created from templates

A "New note" action is added to the Library (none exists today — only New Folder and Upload).
It is **template-aware, with templates in the KB's `.omnipus-vault/templates/`**, seeding
frontmatter and structure. `knowledge_create` already specifies template-based creation for
agents; the human must not get less.

**The marker is a dotfile, so the Library hides it by default** (`pkg/library/entries.go`
filters `strings.HasPrefix(de.Name(), ".")` unless `includeHidden`). Templates therefore need
a first-class surface: a **KB settings panel** that lists, opens and edits templates. "Edit the
template" MUST be a reachable action, never an `includeHidden=true` URL trick.

Template expansion is textual substitution over a fixed, documented variable set. Templates are
operator data (D1): no code execution, no shell, no network.

> **AC-12.1** Creating a note from a template produces frontmatter that the KB's own schema
> validates.
> **AC-12.2** Templates are reachable and editable without enabling hidden files.

### D13 — Lifecycle: empty, revoked, moved, evicted

- **Empty KB** — a real first run offering "create your first note" from a template, subject to
  D5's precedence rule.
- **Revoke** — decrements the index's reference count (D3). The index is deleted **only when
  the last mount referencing that realpath goes away**, after a **7-day grace period**, so a
  re-mount inside that window skips a cold rebuild. Revoke never deletes operator files.
- **KB folder renamed or moved on disk** — the mount breaks by design (`host_path` immutable).
  `MountStatus` already models this; it MUST be surfaced with a re-point action.
- **Evicted / placeholder files** — an evicted read MUST fail loudly and MUST NOT be indexed as
  an empty note. **Detection is provider-specific and only Apple's is in scope this round:**
  iCloud exposes a `.icloud` sidecar with the logical name, which is detectable. OneDrive and
  Dropbox use reparse points / extended attributes with the *same* logical filename and are
  **not** detected — recorded as residual 6. A zero-byte `.md` whose manifest hash changes to
  the empty hash is additionally flagged by `doctor` as suspicious regardless of provider.

> **AC-13.1** Revoking one of two mounts pointing at the same realpath leaves the index intact.
> **AC-13.2** An iCloud-evicted note produces a loud error and is absent from the index —
> never present with empty content.

### D14 — Write concurrency

`ev`'s model is the proven prior art: atomic temp + rename in the target folder; a per-path
advisory lock plus a **shared** KB lock for single-file writes; one **exclusive** KB-wide lock
for multi-file operations, held only for the operation; a **write journal recording planned
rewrites before they are applied**; **bounded lock waits where expiry is a loud error, not a
hang** — bound set to **5 s**, matching `pkg/memrooms/index`'s `boltOpenTimeout` precedent,
configurable.

**Optimistic concurrency, explicitly.** Revision 1 said "detect-before-write (content hash /
mtime)" without a baseline to compare against. The mechanism:

- Every KB read (tool or UI) returns an opaque **version token** = content hash.
- Every write requires the token of the version it is modifying.
- A mismatch is a typed, actionable error naming the path — never an overwrite.
- **mtime alone is insufficient** and is not used as the sole detector: Syncthing preserves
  source mtimes on replication, and several filesystems have 1-second granularity, so a
  sub-second external write is invisible to mtime. mtime/size are a fast pre-filter; the hash
  is the decision.

Advisory locks only coordinate processes that take them, fixing three tiers:

| Tier | Writers | Guarantee |
|---|---|---|
| 1 | Omnipus gateway, agents, UI | **Mutual exclusion** — striped in-process mutex + `fileutil.WithFlock` sidecar + atomic write |
| 2 | the `ev` CLI | **Mutual exclusion by opt-in** — same lock files, same semantics. **Deferred until O-2 closes** |
| 3 | Obsidian app, Syncthing, git, editors | **Cannot be locked.** Version-token mismatch → refuse loudly |

**Tier 2 is deferred, not dropped.** It is the one part of D14 that cannot be built
unilaterally: it depends on `ev`'s lock-file layout becoming a documented contract (O-2).
Tiers 1 and 3 cover Omnipus's own writers and every uncoordinated writer, including `ev`
itself in the interim.

**Tier-1 test shape is inherited explicitly, not by assertion.** `pkg/entity` proves its
guarantee with real cross-process tests (`store_crossprocess_test.go`,
`flock_isolation_test.go`, both `!windows`-gated). KB writes MUST have equivalents; citing
another package's tests does not transfer the guarantee.

> **AC-14.1** Two OS processes writing the same note concurrently: one succeeds, one fails with
> a typed conflict error. Never both succeed with one write lost.
> **AC-14.2** A write whose version token is stale is refused, and the refusal is audited.
> **AC-14.3** Lock-wait expiry produces an error within the bound, never a hang.

### D15 — Render-first preview: Omnipus renders documents; only active content is sandboxed

**Revision 3, 2026-08-22.** Supersedes revision 2's per-format isolation split, which is
withdrawn. Grounded in two measured experiments, both recorded in
[the preview isolation experiment](../specs/adr-067-preview-isolation-experiment-2026-08-22.md).

The preview pane renders the artifact; **source appears only after pressing Edit**.

#### D15.1 — The dividing line is "who renders it", not "what format is it"

Revision 1 sandboxed every inline file — which **broke PDF**. Revision 2 answered with a
per-format isolation split — relaxing protection for "passive" formats. Both are wrong,
and revision 2 was wrong in the dangerous direction: it traded away isolation.

The actual line is simpler:

| Class | Formats | How it reaches the screen | Isolation |
|---|---|---|---|
| **Omnipus renders it** | images, video, audio, markdown, Mermaid, code, **and now PDF** | A React component in our own SPA draws it. No document, no origin, nothing to sandbox | **Not applicable** — the bytes are parsed and drawn by us, exactly as `LibraryImagePreview` (`<img>`) and `LibraryVideoPreview` (`<video>`) already do |
| **The browser renders it** | `.html`, `.htm`, bundles | Arbitrary agent- or web-authored code executing as its own document | **`sandbox allow-scripts` (no `allow-same-origin`) plus source directives** |
| **Neither** | everything else | Download card, unchanged | `Content-Disposition: attachment` |

**PDF moves into the first class by rendering it with PDF.js.** It stops being a document
the browser opens and becomes bytes a component draws — which is why the isolation
question disappears rather than being traded away.

**Consequence: the isolation guarantee is uniform again.** "Only HTML is sandboxed, because
only HTML is executed by the browser" is a statement that is both true and simple. Revision
2's warning that the guarantee is non-uniform is withdrawn along with the split.

#### D15.2 — Active content: the measured policy

For HTML: `sandbox allow-scripts` **without** `allow-same-origin`, plus source directives.
Measured across Chromium 151, Firefox 153 and WebKit 26.5, twice each, in both top-level
and embedded modes, with server-side request logs as ground truth — 24 of 25 compared rows
identical across engines:

- **Zero of seven egress vectors** reached an external origin (image, fetch, beacon,
  WebSocket, iframe, form, popup).
- `document.cookie` and `localStorage` **threw `SecurityError`** — meaningful, because the
  same page without `sandbox` read back the session cookie.
- External stylesheet, external script and audio all loaded and worked.

**`'self'` is used, not an explicit origin.** Revision 1 warned `'self'` might not match
under an opaque origin. **Measured false on all three engines** — `'self'` resolves against
the URL the resource was served from, not the document's opaque origin. An explicit origin
behaved identically and additionally hardcodes a hostname that breaks behind a reverse
proxy (`gateway.public_url`).

> **AMENDED 2026-08-23 — the paragraph above is wrong in one cell, and it was the cell we
> ship.** Kept rather than deleted, because what was believed and why it failed is the useful
> part. Revision 1's warning was half right after all.
>
> The 2026-08-22 measurement covered the §10.3 **header alone**. It never combined the header
> with the `sandbox` **attribute** on the `<iframe>` that D15.6's delivery model actually uses.
> Measured on 2026-08-23, one engine at a time, with a positive control per cell proving the
> document loaded: on **WebKit**, an embedded preview carrying `sandbox="allow-scripts"` runs
> its inline script but **`'self'` matches nothing** — the external `<script src>` and
> `<link rel=stylesheet>` are refused before any request leaves, with enforce-mode
> `script-src-elem` / `style-src-elem` violations naming them. Chromium and Firefox are
> unaffected in every mode; WebKit is fine top-level, and fine embedded with the attribute
> removed. **Real Safari 26.5.2 reproduces it**, so this is what users see today.
>
> **Containment never moved.** Opaque origin, `document.cookie` and `localStorage` still
> throwing, zero of seven egress vectors — in the broken cell too, against a no-policy control
> that let all seven through. This is a rendering defect against US-1 AS-4, not a security one.
>
> **Whose bug:** WebKit's. CSP3 §2.2.2 sets a policy's self-origin from the *response URL's*
> origin, precisely so `'self'` survives an opaque origin; WebKit derives it from the document's
> `SecurityOrigin`, which the attribute has already made opaque by parse time. Upstream bug
> **316847**, fixed by **315247@main** (2026-06-15), regression introduced by **314912@main** —
> not yet in any shipping Safari. Full record, including the four-engine table:
> `docs/internal/specs/adr-067-webkit-self-origin-measurement-2026-08-23.md`.
>
> **Decision: `'self'` is KEPT and the gateway's explicit origin is ADDED beside it** in the six
> source directives (never `connect-src`, which stays `'none'`). Not replaced — replacing it was
> measured worse: a policy naming `127.0.0.1` while the browser reached the same socket as
> `localhost` blocked subresources on **all three** engines, and the seeded default binds
> `127.0.0.1:5000`, so pure replacement would trade a Safari-only defect for an all-browser one
> triggered by how someone typed the URL. With both sources present, the spec-correct engines
> match via `'self'` regardless of spelling, Safari is carried by the explicit origin, and a
> wrong or absent origin degrades to exactly today's behaviour rather than to a blank page.
> The reverse-proxy objection in the paragraph above stands and is answered the same way: the
> origin is resolved at runtime from `middleware.CanonicalGatewayOrigin(cfg)`, never compiled in,
> and an empty result (a `0.0.0.0` bind with no `public_url` — ordinary Docker) collapses to the
> `'self'`-only string plus one WARN. Adding a host-source is also permanently sound rather than
> a workaround: CSP3 §6.7.2 matches a host-source against the **request URL** and never consults
> the document origin, so it cannot be re-broken by this class of bug.

**Both mechanisms are required.** Measured: with source directives but no `sandbox`,
`window.open` still reached the external origin on every engine — no CSP directive covers
popup navigation. With `sandbox` but no source directives, five of seven vectors escaped.

**Webfonts in bundles need `Access-Control-Allow-Origin` on font responses.** Under an
opaque origin a page is cross-origin to its own server, so its own font is CORS-refused.
Measured: with the header, the font applies; without it, it does not. The blocker is
definitively CORS, established with a rendered-width oracle — `document.fonts.status`
reports `"loaded"` even on failure and **must not be used as a success check**.

#### D15.3 — PDF via PDF.js, in the SPA

`pdfjs-dist` — Mozilla's renderer, the one Firefox uses for every PDF. **Apache-2.0**,
compatible with this project's MIT licence. Shipped size **~1.6 MB** (0.43 MB core +
1.20 MB worker), lazy-loaded only when a PDF is opened, against an SPA that already carries
70 dependencies including Mermaid and Shiki.

Renders to canvas with text selection, search, zoom, rotation, thumbnails and outline.

**Form filling and signing are supported, and this was measured rather than assumed** —
because vendor sources (all sellers of competing paid libraries) claim PDF.js annotations
*"may not save properly into the PDF binary"*. Tested with `pdfjs-dist` 6.2.108 against a
hand-built AcroForm PDF, saved through the same `annotationStorage` path the viewer uses,
then **rendered by macOS Quartz/PDFKit — an engine unrelated to PDF.js**:

- **Form fill** produced a well-formed incremental update carrying both `/V (…)` and an
  appearance stream. macOS renders the filled value.
- **Drawn signature** produced `/Subtype /Ink` with `/InkList`, registered on the page, plus
  an appearance stream. macOS renders the stroke.

Both halves matter: the semantic entry for readers that understand the annotation, and the
appearance stream for those that do not. **The vendor claim is refuted for both cases.**

**Not supported, and MUST NOT be promised:** XFA forms; cryptographic (PKI) signatures —
a drawn signature is an image of intent, not a verifiable one, and belongs in its own ADR;
agent-driven form filling — the supported surface is a human filling fields.
**Not tested:** Adobe Acrobat specifically; complex real-world forms (checkboxes, radio
groups, inherited appearances). The fixture was a single text field.

**Scope for this ADR: rendering only.** Form filling and signing are recorded as *proven
feasible* so the choice of library is made with them in view. Shipping them is a later
decision with its own user stories.

#### D15.4 — Type confusion remains a required control

Every inline response derives `Content-Type` from the **file extension**, never from content
sniffing, and carries `X-Content-Type-Options: nosniff`.

**Measured:** an HTML document named `report.pdf` did **not** execute in Chromium — no
script ran, no beacon reached the external origin — and it was blocked **even with no CSP
at all**. Content-type dispatch is what does the work; CSP contributed nothing. A positive
control (the same payload served as `text/html`) executed fully, proving the detection was
not blind.

This control is now *more* important, not less: with PDF.js, a `.pdf` is fetched and parsed
by our own SPA code, so a mislabelled file is a parser-input question rather than an
execution question — but the extension→type mapping is still what routes it.

#### D15.5 — Known limits

- **A sandboxed HTML preview cannot call back to Omnipus.** `connect-src 'none'` blocks
  same-origin fetch, and a request from `origin: null` is cross-origin to the gateway
  regardless. Static artifacts only. **A preview needing live data belongs on the existing
  `/preview/` dev-server route** — a different mechanism for a different job.
- **Cookies are not sent to a sandboxed page's own subresources.**
- **`document.cookie` throws** rather than returning empty.
- **PDF.js is single-threaded in the main context for some work**; very large or complex
  PDFs can be slow. A page-count or size threshold may be needed — unmeasured.

#### D15.6 — Delivery: a sandboxed preview cannot authenticate, so the URL carries the grant

**Found by the round-2 review, verified against the code, and it invalidates the naive
design.** `/api/v1/library/` sits behind `withUploadAuth` (`rest.go:5216`). A sandboxed
document has an **opaque origin**, so:

- the `omnipus-session` cookie is **not sent** — `SameSite=Strict`, and the experiment
  logged Firefox refusing it as *"cross-site context"* for the page's own subresources;
- `<link>` and `<script>` **cannot carry an `Authorization` header**.

So a sandboxed HTML bundle cannot fetch its own stylesheet or script from the authenticated
endpoint. The 2026-08-22 experiment did not surface this because its harness was an
**unauthenticated static server** — a genuine gap in that measurement, recorded rather than
glossed.

**Decision: preview bytes are served from a token-bearing path.** The token is in the URL, so
relative subresources inherit it automatically — the only mechanism that works when neither
cookies nor headers can travel.

| Property | Rule |
|---|---|
| **Minting** | Only by an authenticated request from the SPA, for a path the caller may already read. A token never widens access |
| **Scope** | One workspace, one Library path — a single file, or one bundle root and its descendants. Never a whole workspace |
| **Shape** | 32 random bytes, base64url — matching the in-tree precedent in `pkg/agent/served_subdirs.go` |
| **Lifetime** | Short and bounded, well under that file's `maxTokenLifetime = 24h` ceiling. Expiry must surface as a visible error, never a blank frame |
| **Revocation** | Expiry, plus invalidation on logout |
| **Not reused** | **Not** `ServedSubdirs` — its registration is agent-scoped with a one-per-agent cap that would evict a live `web_serve` token, and its path is unauthenticated by design for a different purpose |

> **Accepted residual: a URL-borne token appears in logs, history and `Referer`.** Mitigated by
> short lifetime, narrow scope, and `Referrer-Policy: no-referrer` on preview responses. It is
> not eliminated. This is the cost of the sandbox, and it is the cost every equivalent design
> pays — ADR-044's existing preview path made the same trade.

`rest_library.go` hard-codes `Content-Disposition: attachment`; an inline mode is required.
**The MIME table lives in `rest_workspace.go` and is unreachable from `rest_library.go`** —
the download handler serves via `http.ServeContent`, which also **sniffs**, so the inline path
must set the type explicitly from the extension rather than delegate. Audio and PDF content
types therefore require wiring a type source into the Library handler.

Audio extensions in scope: `.mp3`, `.m4a`, `.aac`, `.ogg`, `.opus`, `.wav`, `.flac`.

> **Size, corrected by measurement.** D15.3's "~1.6 MB" counts the JavaScript only. PDF.js also
> fetches `cmaps/` (1.6 MB), `standard_fonts/` (800 KB), `wasm/` (1.5 MB) and `iccs/` (20 KB)
> **per document** at runtime. All of it must be embedded under hard constraint #1, so the real
> cost is **roughly 5.5 MB**. Omitting any of it degrades **silently** — a CJK PDF renders
> blank, a PDF that does not embed its fonts renders with wrong metrics, a scanned PDF loses its
> images — and `newSPAHandler` makes it worse by answering a missing path with `index.html` and
> **HTTP 200** rather than a 404. See spec FR-018a/b/c.

**Untrusted-content chrome** remains required for HTML: a persistent, non-spoofable boundary
in Omnipus chrome outside the frame.

#### D15.7 — PDF.js hardening: the parser runs on our origin

Rendering PDFs in the SPA moves **untrusted parsing onto the authenticated origin**, next to
the session cookie. Revision 3 stated the isolation question "disappears"; that is true of the
*document* question and false of the *parser* question. Required:

- **No `eval` path in the shipped bundle.** *Corrected 2026-08-22 by measurement:* this line previously required `isEvalSupported: false`. **That option no longer exists** — verified against `pdfjs-dist` 6.2.108, the version D15.3 names: **zero** occurrences of `isEvalSupported` in `build/pdf.mjs`, in `build/pdf.worker.mjs` or in any published type definition, and **zero** occurrences of `new Function(` in the minified worker. Upstream removed the path. Passing the flag now sets a key PDF.js ignores, so AC-15.10's "asserted at the call site" would have passed forever while proving nothing — a security requirement that could not fail. The property must instead be asserted against the **shipped artefact** (no `eval` / `new Function` in the vendored bundle) and enforced at runtime by a CSP without `unsafe-eval`, so a future version that reintroduces it fails loudly.
- **XFA disabled** — unsupported anyway (D15.3), and it is a scripting surface.
- **PDF scripting's interpreter is not shipped at all.** *Corrected 2026-08-22 by measurement, same defect class as the line above:* this previously required `enableScripting` off. **`enableScripting` is not a `getDocument` option in 6.2.108** — it appears only in the viewer and annotation-layer components (`types/web/pdf_viewer.d.ts`, `types/src/display/annotation_layer.d.ts`), which this preview does not use; it renders to its own canvas. Passing it to `getDocument` would set a key PDF.js never reads, giving a second security control that could not fail. The real control is **absence**: `pdf.sandbox*.mjs` — the engine that executes a PDF's own JavaScript, and which does ship in the package — is excluded from the bundle, along with the `quickjs-eval` wasm it alone references. Both are referenced by zero other build artefacts, so excluding them cannot break rendering, and a build that emits either one fails. Absence is a stronger guarantee than configuration, and unlike a flag it cannot be silently ignored by an upstream release.
- **Worker isolation kept** — parsing stays off the main thread; never fall back to the
  fake-worker path silently.
- **Version pinned and updated deliberately** — this is a parser exposed to hostile input.
- **A Content-Security-Policy on the SPA itself.** Verified: the SPA is served today with
  **no CSP at all**. That was tolerable when it rendered only its own code; it is not once it
  parses arbitrary PDFs from the operator's disk and from agents.

> **AC-15.8** A malformed or hostile PDF fails to render **without** executing script,
> navigating, or issuing a network request.
> **AC-15.9** The SPA is served with a Content-Security-Policy, asserted in an integration test.
> **AC-15.10** PDF.js is configured with `eval`, XFA and PDF scripting all disabled, asserted
> at the call site rather than by comment.


> **AC-15.1** A fixture bundle — `index.html` + external `.css` + external `.js` + a **real**
> webfont served with `Access-Control-Allow-Origin` — renders with all subresources applied,
> asserted by a rendered-width oracle in a real browser. `document.fonts.status` is not an
> acceptable oracle.
> **AC-15.2** A top-level GET of an inline `.html` yields a document that cannot read
> `document.cookie` and cannot reach any external origin. *(Met 2026-08-22; re-assert against
> the real handler.)*
> **AC-15.3** Each audio extension returns a playable `Content-Type`, asserted against the
> **Library** handler, not the workspace MIME table.
> **AC-15.4** A `.pdf` renders in the preview pane via PDF.js on all three engines in the
> matrix — Chromium, Firefox and **WebKit**. *Amended 2026-08-22:* this read "Chrome, Firefox
> and Safari". Safari proper is not covered and nobody is building that coverage — the engine
> Playwright drives is not Safari and no macOS runner is planned. Naming Safari made the
> criterion unsatisfiable by anything anyone intends to write, which reads as coverage while
> guaranteeing none.
> **Tests MUST run headed** — headless Chromium has no PDF viewer and headless WebKit failed
> to render PDFs even unprotected, which previously produced a false negative.
> **AC-15.5** An HTML document named `.pdf` does not execute: served `application/pdf`,
> `nosniff` present, no script runs, nothing reaches an external origin. Required, with a
> positive control proving the detection is not blind.
> **AC-15.6** Opening a PDF loads the PDF.js bundle lazily; it is absent from the initial
> SPA payload.
> **AC-15.7** Adding an extension to the inline allow-list fails CI unless the change also
> adds an AC-15.5-style test for it.


### D16 — Deep-linking, and two markdown defects

- The selected file becomes **URL-addressable** (`?path=`) — the shared mechanism for wikilink
  clicks, search results, backlink clicks and agent-supplied links, and it restores
  back-button and bookmark behaviour.
- **`%%comment%%` renders visibly.** Obsidian treats these as invisible; a private aside is
  currently on screen. Fixed.
- **Relative markdown links render struck through** as "Link removed: unsafe URL scheme",
  because `isSafeHref` (`src/lib/url-safe.ts`) calls `new URL(href)` with no base.

  > **This is a scoped behaviour change, not a blanket bug fix** — revision 1 called it a
  > defect affecting "all markdown in the product". It is **test-asserted** behaviour:
  > `src/components/chat/MarkdownText.test.tsx` contains
  > `it('rejects relative paths (not parseable by URL constructor)')` asserting
  > `isSafeHref('/relative/path') === false`, and the helper is consumed by chat markdown,
  > which renders **untrusted model and tool output**. Making model-authored relative hrefs
  > live links on the gateway origin is a security change.
  >
  > **Scope of the change: the KB reader only.** Relative links resolve against the KB root,
  > through a KB-specific link renderer that drives in-app selection (D16's `?path=`), never
  > through the shared `<a>` renderer. **Chat markdown behaviour is unchanged and its existing
  > assertions stay green.** The precedent to follow is `isDisplayableImageSrc`, which already
  > resolves relatively under a narrower allow-list.

### D17 — Tool-policy seeding (Hard Constraint #6)

Absent from revision 1, and a boot-blocker: `config.ValidateToolPolicyCoverage` runs at boot
(`pkg/gateway/gateway.go`, *"aborting boot"*), at reload, and at every agent write.

- **Fresh install:** all `knowledge_*` tools are enumerated explicitly in
  `pkg/config/defaults.go` and per-agent in `coreagent.SeedConfig`. No wildcards — the
  no-default-policy rule admits none for static builtins.
- **Seed posture:** retrieval tools (`knowledge_search`, `knowledge_graph`) `allow` for all
  four core agents; authoring tools `allow` for Jim and Ava, `ask` for Mia and Ray.
- **Existing installs:** the load-path backfill sets unknown tools to `deny` with only a WARN
  (`pkg/config/validate.go`), so every existing operator would upgrade into a KB whose tools
  are all denied and be told only in a log line. A **migration** must seed the new tools at
  the same posture as a fresh install, and the upgrade must surface what changed.

> **AC-17.1** Boot with the new tools registered produces **zero** coverage gaps.
> **AC-17.2** The seed, never the deny-backfill, is the source of every `knowledge_*` posture:
> loading a seeded config produces **zero** backfilled `knowledge_*` entries, and deliberately
> removing one seeded entry produces **exactly one**. *Amended 2026-08-22:* this read *"Loading a
> config written before this ADR yields the seeded posture"* — a back-compat premise with no
> referent, since the tools are new and no earlier config carries a posture for them. It could
> only ever have been satisfied vacuously. The positive control is what makes the replacement
> falsifiable: coverage validation returns nothing when the tool registry is empty, so a test
> that never populates it reports green with the seeding entirely absent.

### D18 — Contract-first wire types (Hard Constraint #8)

Absent from revision 1. Every new byte crossing the gateway/SPA boundary is defined in
`contracts/` **before** any Go/TS code, per CLAUDE.md's 5-step process.

| Wire type | Transport | Carries |
|---|---|---|
| `KnowledgeBaseInfo` | REST | KB detection, display name, root |
| `KnowledgeSearchRequest` / `Response` | REST | query, scoping, hits with excerpt |
| `KnowledgeGraphResponse` | REST | links / backlinks / unresolved / orphans |
| `KnowledgeOutline` | REST | heading tree for the reading rail |
| `KnowledgeIndexProgress` | **WS (AsyncAPI)** | enumeration/indexing phase + counts (D5) — a streaming state, not a REST field |
| `KnowledgeConflictError` | REST | typed version-token mismatch (D14) |
| `LibraryInlineDisposition` | REST | inline-preview metadata (D15) |

### D19 — Audit (Repudiation)

Every KB mutation emits an audit event: the acting agent, the KB root, paths touched, the
operation, and the outcome — **including D14 tier-3 refusals**, which are the
security-relevant ones. Library mutations already route through `a.logLibraryAudit`; the
`knowledge_*` tools write to the operator's real disk outside that path and must not be
exempt.

### D20 — Sequencing

The requirements document's top risk — *"net scope is larger than before the graph was cut;
sequencing is now the main lever left"* — was dropped in revision 1. Reinstated as a decision.

| Stage | Contents | Gates on |
|---|---|---|
| **1** | D15 preview (HTML/PDF/audio), D16's `%%comment%%` fix and `?path=` deep-linking | Nothing. Touches no KB machinery and is independently shippable |
| **2** | D1–D9 read path: detection, marker, index, freshness, search, outline, backlinks, reading layout. D17, D18, D19 | Stage 1; O-3 (resolved in D1) |
| **3** | D10–D14 write path: authoring tools, templates, link rewriting, concurrency tiers 1 and 3, lifecycle | Stage 2 |
| **4** | D14 tier 2 (`ev` lock interop) | **O-2** — a contract on another project's side |

#632 itself says the parts are *"useful independently and best shipped in this order."*

---

## 3. Alternatives considered and rejected

| # | Alternative | Why rejected |
|---|---|---|
| A1 | `pkg/utils/bm25.go` (as #632 proposes) | Re-indexes the whole corpus per query |
| A2 | SQLite FTS5, as `ev` uses | A second SQLite user against the house rule, for no gain over scorch |
| A3 | `.obsidian/`-only detection | A folder Obsidian never opened silently isn't a KB; names a vendor as the sole marker of an Omnipus feature |
| A4 | Omnipus writes `.obsidian/` | Fabricating another application's config directory. Unnecessary |
| A5 | `fsnotify` watcher | New dependency, least reliable on cloud-synced folders — exactly where needed |
| A6 | Index inside the KB (the `memrooms` default) | Pollutes, syncs and versions a large derived artifact in the operator's folder |
| A7 | Global graph view, server-side layout | Largest item, from-scratch, and the surface that fails at scale in every Obsidian report |
| A8 | Office preview via LibreOffice → PDF | External runtime dependency (Constraint #1) and shelling out (Constraint #2) |
| A9 | Office preview via client-side converters | XLSX good, DOCX fair, **PPTX poor** — worst on the format that motivated the work |
| A10 | HTML with scripts disabled | Inert JS is indistinguishable from a broken page — the ADR-061 failure class |
| A11 | Last-write-wins on KB files | With Syncthing replicating and agents writing unattended, a silently lost note is inevitable |
| A12 | Read-only in mounted KBs | Removes the authoring tools from the KB they were designed for |
| A13 | Create KBs at arbitrary host paths | A new broad capability, when workspace-first plus the existing move covers the need |
| A14 | **Serve preview content from a distinct origin** (separate port or host) | **Still rejected — but the 2026-08-22 reason it was rejected for turned out to be false, so the current reason is different.** That entry read: *"It existed only as a fallback if `'self'` failed under an opaque origin; measurement on three engines showed `'self'` works, so the fallback is unnecessary and ADR-044's single listener stands."* Re-measured 2026-08-23 (D15.2's amendment): `'self'` **does** fail under an opaque origin on WebKit, in exactly the embedded-with-`sandbox`-attribute configuration this ADR ships, and on real Safari 26.5.2. What that revives is not this alternative, though — a second origin was only ever one way to give the policy a source it could match, and naming the gateway's own origin explicitly beside `'self'` does the same thing for the price of one config lookup, with no second listener, no second certificate, no second `public_url` and no change to ADR-044. A14 stays rejected on cost; the fallback it was hedging is no longer hypothetical, and has been answered elsewhere |
| A17 | **One sandboxing policy for every inline format** (revision 1) | Measured: breaks a framed PDF on all three engines. Superseded — with PDF.js there is no framed PDF to break, and the uniform policy returns for the one class that needs it |
| A18 | **Add `allow-downloads` to the sandbox so PDFs render** | Unverified, and it weakens the sandbox for active content where downloads are an exfiltration route. Unnecessary once PDF is not rendered by the browser at all |
| A19 | **Drop inline PDF entirely, keep the download card** | Honest and zero-risk, but loses the format the operator most wants to read in place |
| **A20** | **Per-format isolation split** — relax `sandbox` for "passive" formats (revision 2, briefly accepted) | **Withdrawn 2026-08-22.** It bought PDF rendering by *trading away isolation*, and made the security guarantee non-uniform and therefore easy to erode. PDF.js delivers the same outcome with no trade. Retained here because it was committed and must not be re-proposed as new |
| **A21** | **Render PDF server-side in Go** | No mature pure-Go rasteriser exists. `pdfcpu`/`gopdf` create and manipulate but cannot draw a page; `ledongthuc/pdf` (in tree) extracts text only; `go-fitz` renders but is **AGPL** and needs CGo — both disqualifying. The only pure-Go option embeds PDFium as a multi-megabyte WASM blob. It would also ship *images* instead of a document, losing text selection, search and zoom crispness, and put page rasterisation on the gateway's CPU |
| **A22** | **Use the browser's built-in PDF viewer** (revision 1/2's mechanism) | Three different viewers with three behaviours, broken inside a sandboxed frame, no control over text selection or search integration, and no path to form filling or signing. PDF.js is one implementation everywhere |
| **A23** | **A commercial PDF SDK** (PDF.js Express, Apryse, Nutrient) | Richer annotation and real digital signatures out of the box, but proprietary and paid — a poor fit for a community MIT project, and it would be the only non-open component in the stack |
| A15 | **Index keyed by workspace+mount** | Would produce N indexes over one corpus and make D13's revoke destroy a sibling workspace's index |
| A16 | **Blanket relative-link fix across all markdown** | Would make model-authored relative hrefs live links on the gateway origin (D16) |

---

## 4. Consequences

### 4.1 Gained

Retrieval clearing Obsidian's ceiling with no new dependency; agents able to find notes by
relevance and ask what links to a note; authoring that cannot emit malformed wikilinks, YAML
or anchors; an operator able to verify the index themselves; and the `%%comment%%` privacy
defect fixed.

### 4.2 Costs and new obligations

- **`pkg/memrooms/index` reuse is larger than revision 1 stated.** `Search` sets
  `req.Fields = []string{}` and returns `{ID, Score}` only — **no stored fields, no
  highlighting, no fragments** — and hard-caps `limit` at 50. D7's "path, title, matched
  excerpt" and "top-N ≤ 100" are therefore **new work**, not reuse: stored fields or a
  highlight-capable mapping, a parameterised cap, and a path→doc-ID mapping. Excerpt strategy
  (bleve highlighting, which grows the index, versus re-reading the file at query time, which
  needs a match locator) is an open spec decision.
- **`rebuildLocked` must be replaced**, not reused — it builds one batch over the whole corpus
  (M-3/D4).
- **Generalising `pkg/memrooms/index` touches a package memory rooms depend on.** `OpenOrCreate`
  takes a `memrooms.Room`, the registry key derives from the index path, and the rebuild source
  is `room.MemoriesDir`. **Decision: build a sibling package on the same pattern rather than
  generalise in place** — memory-room recall is live functionality, the shared surface is
  small, and the duplication is bounded. Recorded rather than assumed.
- A YAML-aware writer and a write journal become load-bearing for correctness.
- **New failure modes must be surfaced, not swallowed:** partial index, enumeration phase,
  broken mount, evicted file, version-token conflict, non-atomic move leaving a duplicate,
  ambiguous link. Each needs a real UI state.

### 4.3 Explicitly worse than before

- A first mount of a large KB does background work the operator did not ask for (mitigated by
  D5, not eliminated).
- **Writes into a KB can now fail** where a naive write would have succeeded (D14 tier 3).
  Intended — a refusal beats a lost note — but a genuine regression in raw success rate.
- The Library gains a rendering surface for untrusted HTML that did not previously exist, with
  the phishing residual D15 names.

### 4.4 Regression risk (existing behaviour that could break)

| Area | Risk | Mitigation |
|---|---|---|
| Memory-room recall | `pkg/memrooms/index` changes | Sibling package instead of in-place generalisation (§4.2) |
| Chat markdown links | `isSafeHref` scope creep | D16 confines the change to the KB reader; chat assertions stay green |
| `web_serve` previews | Token eviction if `/preview/` were reused | D15 uses the Library endpoint instead |
| Existing installs' tool access | Deny-backfill on upgrade | D17 migration |

---

## 5. Open questions for the spec round

| # | Question |
|---|---|
| **O-1** | **The Librarian layer** (#632's judgement layer) is out of scope. Its form — core agent, scheduled routine, or both — is undecided. The *layering* is not open: no part of graph correctness may depend on it |
| **O-2** | Tier-2 interop requires `ev`'s lock paths and semantics to be a **documented, stable contract**. Blocks stage 4 only |
| **O-4** | Rename link-rewriting: automatic, or prompt first? Obsidian offers both, defaulting to automatic |
| **O-5** | Excerpt strategy: bleve highlighting (stored fields, larger index) versus query-time re-read (needs a match locator) |
| **O-6** | Non-markdown files in a KB — indexed as metadata only, or skipped? Bears on scale: the researched 104k-file vault was roughly half images |
| **O-7** | Whether one KB may span several mounts, or is always exactly one mounted folder |
| **O-8** | `doctor`'s surface: REST endpoint, SPA action, CLI subcommand, or all three. If CLI: scorch holds a **process-exclusive bbolt lock** with a 5 s bound, so a CLI `doctor` would reliably error against a running gateway. It must either proxy to the gateway or operate manifest-only |

> O-3 (marker name) is **closed** — decided as `.omnipus-vault/` in D1.

---

## 6. Accepted residuals

1. **Renames made outside Omnipus break links.** No local tool can observe a rename it did not
   perform; Obsidian has the identical limitation. `unresolved` surfaces the damage after the
   fact.
2. **Tier-3 writers cannot be excluded, only detected.** Between hash check and write there
   remains a window. Narrowed by atomic write and re-check under lock; never closed.
3. **Cross-process locking is POSIX-only.** `fileutil.WithFlock` is a documented no-op on
   Windows, so tiers 1 and 2 degrade to in-process protection there. Consistent with ADR-054
   §5, but a *shared* KB with external writers is a weaker case for that precedent than an
   internal store — ADR-054 §5.1's `LockFileEx` work is the real fix.
4. **Relocating a KB out of the workspace is not atomic** and may leave a duplicate on cleanup
   failure. Reported loudly; not rolled back.
5. **The index can be stale between scans.** Freshness is checked on access, not continuously.
6. **Eviction detection is Apple-only.** OneDrive and Dropbox placeholders are not detected
   (D13); the zero-byte-hash heuristic is a partial backstop, not a guarantee.
7. **Filenames containing `< > : " | ? *`, trailing dot/space, or Windows device names are not
   addressable through the Library.** `library.CleanRelPath` applies `pathsafe.ValidateComponent`
   to **every** segment, unconditionally on every OS, by deliberate design — *"a workspace must
   behave identically whichever OS opens it."* Obsidian note titles routinely contain `:` and
   `?`. **Posture: such files are reported by `doctor` as an explicit "unaddressable" class and
   surfaced in the KB settings panel; they are neither indexed nor silently skipped.** Relaxing
   `pathsafe` for mounted host trees is out of scope here and would need its own ADR.
   Measured on the reference vault: **1 of 748 notes** (0.13%) is affected today — low there
   because its naming uses em-dashes, but a vault using `Meeting: notes` titles would be hit
   far harder.

---

## Appendix A — Review response (traceability)

Verdict of `ADR-067-…-review.md` was **BLOCK**. Findings verified against the codebase before
being actioned; all five criticals reproduced and confirmed.

| Finding | Resolution |
|---|---|
| **C-1** CSP blocks bundles | D15 — inline route gets its own policy; `buildWorkspaceCSP`'s gap named; AC-15.1 fixture bundle asserted in a real browser; A14 fallback recorded |
| **C-2** Inline disposition → stored XSS | D15 — response-borne `sandbox` CSP; AC-15.2 top-level-GET test; distinct-origin alternative recorded as A14 |
| **C-3** `/preview/` cannot serve Library files | D15 — delivery path decided: the Library endpoint. `/preview/` reuse rejected with reasons |
| **C-4** Multi-vault search crosses workspaces | D7 — scoped to the calling agent's workspace mounts; AC-7.1 negative test |
| **C-5** Constraint #6 unaddressed | **New D17** — seed list, per-agent posture, upgrade migration, AC-17.1/17.2 |
| **M-1** `pathsafe` rejects real note names | Residual 7 — posture decided (`doctor` reports an unaddressable class), measured at 1/748 |
| **M-2** `memrooms` reuse understated | §4.2 — stored fields, excerpts, cap, path→doc-ID named as new work; O-5 opened |
| **M-3** Whole-corpus in-memory batch | D4 — batched bounded-memory indexing; AC-4.1 measured RSS budget |
| **M-4** Byte-identical rebuild infeasible | D6 — restated as identical ranked results and identical graph sets (AC-6.1) |
| **M-5** "Atomically" contradicts the journal | D10 — "atomically" removed; journalled + `doctor`-recoverable stated |
| **M-6** One folder, many mounts | D3 — index keyed by realpath, reference-counted; D13 revoke; A15; AC-3.1/13.1 |
| **M-7** Relative-link "fix" is a security control | D16 — reframed as a KB-reader-only change; chat assertions stay green; A16 |
| **M-8** Constraint #8 unaddressed | **New D18** — wire-type table with transports |
| **M-9** "`doctor` without an agent" undefined | O-8 — surface options stated with the bbolt-lock constraint |
| **M-10** Progress denominator undefined | D5 — enumeration and indexing phases specified separately; AC-5.1 |
| **M-11** No write baseline | D14 — version-token optimistic concurrency; mtime insufficiency stated |
| **M-12** No containment for link resolution | D6 — realpath containment invariant, symlink policy, AC-6.2 |
| **M-13** Marker is hidden | D12 — KB settings panel; AC-12.2 |
| **M-14** `memrooms` regression risk | §4.2 — decided: sibling package, not in-place generalisation; §4.4 |
| **M-15** Marker name decided *and* open | D1 — decided; O-3 closed |
| **M-16** No audit for KB writes | **New D19** |
| **M-17** Audio cannot play | D15 — MIME table and media source extension; extension list; AC-15.3 |
| **M-18** Sequencing risk dropped | **New D20** — four stages with gates |
| **m-1** Bad path citation | §1.4 — full path |
| **m-2** False collision rationale | D1 — restated; sibling-of-`work/` fact corrected |
| **m-3** Unsourced layout direction | D9 — cited to `src/routes/_app/library.tsx` |
| **m-4** No measurable target | §1.2 — five targets with a fixture corpus |
| **m-5** Empty vs indexing precedence | D5 — precedence stated; AC-5.3 |
| **m-6** Eviction detection unspecified | D13 — Apple-only in scope; residual 6 |
| **m-7** Tie-break undefined | D6 — written out, plus ambiguity reporting |
| **m-8** Lock bound unstated | D14 — 5 s, configurable |
| **m-9** PDF mechanism unspecified | D15 — `<iframe src>` named |
| **o-1** Tier 2 gated on another project | D14 / D20 — deferred to stage 4 |
| **o-2** Marker identity speculative | D2 — reduced to name + templates |
| **o-3** Six mechanisms at once | D20 — staged |
| **o-4** Phishing surface | D15 — untrusted-content chrome required |
| **o-5** No search cost bound | D7 — `top_n` cap, excerpt cap, hop/node bounds, rate limit |
| **o-6** Revoke cost unbounded | D13 — reference counting + 7-day grace |
| **o-7** No vault design note | Outstanding — see §7 below |
| **Structural: no acceptance criteria** | AC blocks added to D1–D17 |
| **Structural: unmeasurable success** | §1.2 targets |
| **Test coverage: negative scenarios** | AC-7.1, AC-14.1/14.2, AC-17.1, AC-6.2/6.3 |
| **Test coverage: regression risk** | §4.4 |

---

## 7. Outstanding process obligation

Per the design-first rule, a founder-visible **vault design note** must exist and be cited here
before this ADR is ratified. It does not yet exist. This ADR is `Proposed`, not accepted, until
it does.
