# Feature Specification: Omnipus knowledge base and render-first preview

- **Implements:** [ADR-067](../architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md) (20 decisions, 37 acceptance criteria)
- **Origin:** [issue #632](https://github.com/elicify-ai/omnipus/issues/632); founder interview 2026-08-21 ([requirements](library-improvements-requirements-2026-08-21.md))
- **Branch:** `feat/library-improvements`
- **Date:** 2026-08-22
- **Status:** Draft — implementation-ready for stages 1 and 2; stages 3 and 4 specified in full but gated.

Structured by ADR-067 D20's four stages. Every acceptance criterion in the ADR
(`AC-x.y`) maps to at least one named test in §12; the mapping is asserted in §16.

---

## 1. Available Reference Patterns

**N/A.** `docs/reference/go-implementation/` does not exist in this repository. The
patterns this feature reuses are in-tree and are named in §2 instead.

---

## 2. Existing Codebase Context

Derived from a GitNexus index built on this branch at commit `effdacb`
(62,595 nodes, 239,217 edges, 2,145 clusters, 300 flows).

### 2.1 Symbols involved

| Symbol | File | Role |
|---|---|---|
| `classifyLibraryEntry` | `src/components/library/preview/libraryPreviewKind.ts` | **Modified** — gains `html`, `pdf`, `audio` kinds |
| `LibraryPreviewPane` | `src/components/library/LibraryPreviewPane.tsx` | **Modified** — mounts new surfaces; becomes reading mode for KB files |
| `LibraryExplorer` | `src/components/library/LibraryExplorer.tsx` | **Modified** — search header, KB awareness |
| `HandleLibrary` | `pkg/gateway/rest_library.go` | **Extended** — inline disposition, KB endpoints |
| `buildWorkspaceCSP` | `pkg/gateway/rest_workspace.go` | **Referenced** — its gaps drive the new inline policy; not itself changed |
| `setWorkspaceSecurityHeaders` | `pkg/gateway/rest_workspace.go` | Sole caller of the above |
| `workspaceContentType` | `pkg/gateway/rest_workspace.go` | **Modified** — audio MIME types added |
| `isSafeHref` (TS) | `src/lib/url-safe.ts` | **Not modified** — bypassed by a KB-specific link renderer |
| `isSafeHref` (Go) | `pkg/utils/markdown.go` | **Not modified** — recorded for the divergence in §2.4 |
| `OpenOrCreate` | `pkg/memrooms/index/index.go` | **Not modified** — pattern copied into a sibling package |
| `CleanRelPath` | `pkg/library/root.go` | **Not modified** — its `pathsafe` rule produces residual R-7 |
| `AllowedMountRoots` | `pkg/workspace/mount.go` | **Called** — the isolation boundary for KB scoping |
| `ValidateToolPolicyCoverage` | `pkg/config/validate.go` | **Satisfied** — new tools must be seeded or boot aborts |

### 2.2 Impact assessment

| Symbol modified | Risk | Impacted | Direct dependents |
|---|---|---|---|
| **`classifyLibraryEntry`** | **HIGH** | 5 | `LibraryEntryRow`, `LibraryPreviewPane` |
| `isSafeHref` (TS) — *if changed* | MEDIUM | 13 | 5 callers across Chat (7 hits, direct) and Tools (2, indirect) |
| `buildWorkspaceCSP` | LOW | 5 | `setWorkspaceSecurityHeaders` |
| `OpenOrCreate` (memrooms) — *if changed* | LOW | 5 | `pkg/agent/memory.go` (Agent module) |
| `isSafeHref` (Go) | LOW | 6 | 2 |

> **HIGH-RISK WARNING (CLAUDE.md mandate).** `classifyLibraryEntry` is HIGH risk. It is
> the single decision point for how every Library file renders, and both the row
> component and the preview pane depend on it directly. Adding kinds must be
> **purely additive**: no existing input may change classification. SC-013 and
> `TestClassifyLibraryEntry_ExistingKindsUnchanged` enforce this.

### 2.3 Cluster placement

Primary cluster: **Library** (235 symbols, 40 files). The feature also touches
**Config** (tool-policy seeding), **Security** (CSP, containment), **Chat**
(markdown renderers — *read-only*, see §2.4), **Agent** (tool registration) and
**Media** (MIME types). Spanning six clusters is itself a staging argument (D20).

### 2.4 Divergence discovered during grounding — record, do not "fix"

The two link sanitisers disagree, and the disagreement is load-bearing for D16:

| Implementation | Relative link (`/foo`, `foo.md`) |
|---|---|
| `pkg/utils/markdown.go::isSafeHref` (Go) | **Accepted** — `scheme == ""` returns true |
| `src/lib/url-safe.ts::isSafeHref` (TS) | **Rejected** — `new URL(href)` throws with no base |

The TS rejection is an artefact of the parsing mechanism, not an authored policy —
its own test is named *"rejects relative paths (not parseable by URL constructor)"*,
describing the cause rather than an intent. The Go side, which sanitises the same
class of untrusted markdown, permits them.

**This does not license a blanket change** (ADR-067 D16, review finding M-7). It
raises confidence that the KB-scoped approach is correct rather than a workaround.
Unifying the two is explicitly **out of scope** — see §7 NB-4.

### 2.5 Relevant execution flows

GitNexus reports **no named execution process** for the Library preview path or for
`isSafeHref`; both are request-scoped rather than part of a traced flow. The KB
feature therefore introduces new flows rather than extending existing ones. The
`Agent` module flows that consume `pkg/memrooms/index` are unaffected because §11
copies the pattern into a sibling package instead of generalising in place.

---

## 3. Prerequisites

| # | Prerequisite | Status |
|---|---|---|
| P-1 | ADR-067 accepted | **Blocked** — a founder-visible vault design note must exist first (ADR §7) |
| P-2 | O-3 marker name resolved | **Done** — `.omnipus-vault/` (D1) |
| P-3 | O-2 `ev` lock contract agreed | **Open** — gates stage 4 only |
| P-4 | 100k-note fixture vault generator | **To build** — required before any §1.2 performance claim |
| P-5 | Browser matrix available for CSP verification | **Required for stage 1** — Chrome, Firefox, Safari |

---

## 4. Development Setup and Tech Stack

Backend Go (tags `goolm,stdjson`), frontend React 19 + Vite, contracts via
`scripts/gen-contracts.sh`. Build and test through `make build` / `make test` —
never raw `go test ./...`, which fails to compile `pkg/channels/matrix`.

**CI is the authority for Go tests.** Do not run the full suite locally. At most one
narrowly-scoped local test: `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/...`.

New dependencies: **none**. `bleve/v2` is already direct.

---

# STAGE 1 — Render-first preview (gates on nothing)

## 5. User Stories — Stage 1

### US-1 — See the document, not its source (Priority: P1)

As an operator, when I open an HTML page, PDF or audio file in the Library, I want to
see the rendered page, the document, or a player — not source code or a download
button. Today an agent builds me a report and I get its markup; a PDF gives me a
download card. Source is still one click away behind **Edit**, which is where it
belongs for a file I mostly want to read.

**Why P1:** it is the smallest independently shippable improvement in the whole ADR,
touches no knowledge-base machinery, and fixes the daily friction of agent-produced
artifacts being unreadable in place.

**Independent test:** put an `index.html` with an external stylesheet, an external
script, a webfont, a `.pdf` and a `.mp3` into a workspace. Open each in the Library.
All render; the HTML page's script runs; Edit reveals source for the HTML only.

**Acceptance scenarios**

1. **Given** an `.html` file in a workspace, **When** I select it, **Then** the pane shows the rendered page and no source code.
2. **Given** a rendered HTML page, **When** I press Edit, **Then** the pane shows the file's source in an editor.
3. **Given** an HTML page whose script writes into the DOM, **When** it renders, **Then** the script's effect is visible.
4. **Given** a folder containing `index.html`, an external `.css`, an external `.js` and a webfont, **When** I open `index.html`, **Then** all four load and the page appears styled, scripted and correctly typeset.
5. **Given** a `.pdf`, **When** I select it, **Then** it is displayed inside the pane by Omnipus's own renderer, with selectable text — not handed to the browser's viewer and not downloaded.
6. **Given** an `.mp3`, **When** I select it, **Then** an audio player appears and plays it.
7. **Given** an unsupported binary (`.zip`), **When** I select it, **Then** the existing download card appears unchanged.

### US-2 — Untrusted pages cannot reach my session (Priority: P0)

As an operator, when the Library renders a page an agent wrote or downloaded, I need
certainty it cannot read my login session, call the API as me, or phone home — however
that page is opened, including in its own browser tab.

**Why P0:** removing `Content-Disposition: attachment` without a response-borne control
creates stored cross-site scripting on the gateway origin. This is the only P0 in
stage 1 and it gates US-1's release.

**Independent test:** open an inline `.html` that tries to read `document.cookie` and
POST to `/api/v1/agents`, both embedded in the pane **and** as a top-level tab. Both
attempts fail in both contexts.

**Acceptance scenarios**

1. **Given** an `.html` containing `document.cookie` access, **When** it is opened as a top-level browser tab at its Library URL, **Then** the read yields nothing and the document's origin is opaque.
2. **Given** the same file, **When** it is rendered inside the preview pane, **Then** the same holds.
3. **Given** an `.html` that issues a network request to any host, **When** it renders, **Then** the request is blocked.
4. **Given** any inline-rendered page, **When** it is displayed, **Then** a persistent "untrusted content" boundary is visible in Omnipus chrome outside the frame.
5. **Given** a request for a file type not on the inline allow-list, **When** it is fetched, **Then** the response is served as an attachment exactly as today.

### US-3 — Private notes stay private, and links work (Priority: P2)

As an operator I want `%%private asides%%` in my markdown to stay invisible as they are
in Obsidian, and I want to link directly to a file so I can bookmark it, use the back
button, and share a pointer to it.

**Why P2:** the comment leak is a small but real privacy defect; deep-linking is a
prerequisite for stage 2's wikilinks, search results and backlinks, so building it now
avoids reworking navigation later.

**Independent test:** a note containing `%%secret%%` renders without it. Selecting a
file changes the URL; reloading that URL reopens the same file; back returns to the
previous one.

**Acceptance scenarios**

1. **Given** markdown containing `%%an aside%%`, **When** it renders, **Then** neither the marker nor its content appears.
2. **Given** a selected file, **When** I look at the URL, **Then** it identifies that file.
3. **Given** such a URL, **When** I load it fresh, **Then** that file opens selected.
4. **Given** I have opened two files in turn, **When** I press back, **Then** the first reopens.
5. **Given** a path that does not exist, **When** I load its URL, **Then** the Library opens at the containing folder with a clear "not found" message and does not error out.

---

# STAGE 2 — Knowledge base read path (gates on stage 1)

## 6. User Stories — Stage 2

### US-4 — Omnipus recognises a knowledge base (Priority: P1)

As an operator I want Omnipus to notice that a folder I have mounted is a knowledge
base, and to let me create one, so the reading and search features switch on for the
right folders and stay off everywhere else.

**Why P1:** every other stage-2 story depends on detection. It is also the first place
Omnipus writes a marker into a real folder, so it must be deliberate.

**Independent test:** mount three folders — one with `.obsidian/`, one with
`.omnipus-vault/`, one with neither but full of `.md`. The first two are knowledge
bases; the third is not.

**Acceptance scenarios**

1. **Given** a mounted folder containing `.obsidian/`, **When** it is opened, **Then** it is treated as a knowledge base.
2. **Given** a mounted folder containing `.omnipus-vault/`, **When** it is opened, **Then** likewise.
3. **Given** a mounted folder of `.md` files with neither marker, **When** it is opened, **Then** it is an ordinary folder with no knowledge-base features.
4. **Given** detection runs, **When** it decides, **Then** it has read no file contents.
5. **Given** I create a knowledge base, **When** it is created, **Then** `.omnipus-vault/` exists and `.obsidian/` does not.
6. **Given** a knowledge base with a display name, **When** I move the folder and re-mount it elsewhere, **Then** the name is preserved with no migration step.

### US-5 — Search that stays fast on a large collection (Priority: P1)

As an operator with a large note collection I want search that returns in well under a
second and does not make Omnipus slow to start, unlike the tools I use today.

**Why P1:** retrieval is the feature. The measurable targets are the point — Obsidian
degrades at 12,000 notes and needs 27 seconds to start at 104,000.

**Independent test:** against a 100,000-note fixture, measure 95th-percentile search
latency, peak memory during first index, and steady-state memory.

**Acceptance scenarios**

1. **Given** a 100,000-note knowledge base, **When** I search, **Then** the 95th-percentile response is under 500 ms.
2. **Given** the same, **When** it is indexed for the first time, **Then** peak memory stays under 512 MB.
3. **Given** an indexed knowledge base, **When** it sits open and idle, **Then** steady-state memory stays under 64 MB.
4. **Given** an unchanged knowledge base of 100,000 files, **When** it is reopened, **Then** the freshness check completes in under 2 seconds.
5. **Given** a knowledge base being opened, **When** I search, **Then** a usable partial result appears within 5 seconds.
6. **Given** an index exists, **When** Omnipus restarts, **Then** no full rebuild occurs.

### US-6 — Never a confidently incomplete answer (Priority: P0)

As an operator I must always be able to tell the difference between "there are three
matches" and "we have only searched a fraction of your notes so far".

**Why P0:** a search that silently returns a subset is worse than one that refuses. It
is the failure this project has repeatedly had to correct, and it is unfalsifiable
after the fact.

**Independent test:** start a first index of a large collection and search during it.
Results appear together with an unmissable statement of how much has been indexed.

**Acceptance scenarios**

1. **Given** the tree is still being walked, **When** I search, **Then** the state shown is indeterminate — a count found so far, never a ratio and never "0 of 0".
2. **Given** indexing is under way with a known total, **When** I search, **Then** a ratio of indexed to total is shown alongside the results.
3. **Given** a search issued during indexing, **When** results return, **Then** the incompleteness statement arrives in the same payload as the results, not through a separate channel.
4. **Given** indexing has finished, **When** I search, **Then** no incompleteness statement is shown.
5. **Given** a newly created and still-indexing knowledge base, **When** I look at it, **Then** the indexing state is shown, not the empty-collection first run.
6. **Given** a freshness check that finds nothing changed and completes quickly, **When** it runs, **Then** no banner appears at all.

### US-7 — Read a note the way it was written (Priority: P1)

As an operator I want Obsidian's syntax to render properly — links, embeds, callouts,
highlights — with an outline of the note and a list of what links to it, so the Library
is somewhere I can actually read.

**Why P1:** rendering and backlinks are what make a collection navigable rather than a
pile of files.

**Independent test:** open a note using every supported syntax feature. Each renders.
The outline lists its headings. Backlinks list the notes pointing at it. Clicking a
wikilink opens the target.

**Acceptance scenarios**

1. **Given** a note containing `[[Note]]`, `[[Note|alias]]`, `[[Note#Heading]]` and `[[folder/Note]]`, **When** it renders, **Then** each appears as a working link, not literal text.
2. **Given** a note containing `![[image.png]]`, **When** it renders, **Then** the image is displayed.
3. **Given** a note containing a callout and `==highlight==`, **When** it renders, **Then** both are styled, with no raw markers visible.
4. **Given** a note with frontmatter, **When** it renders, **Then** the frontmatter is not shown as a heading or a horizontal rule.
5. **Given** a note with headings, **When** it is open, **Then** an outline of those headings is shown.
6. **Given** a note that three others link to, **When** it is open, **Then** those three are listed as linked mentions.
7. **Given** I click a wikilink, **When** it resolves, **Then** the target note opens and the URL updates to point at it.
8. **Given** a wikilink whose target does not exist, **When** it renders, **Then** it is visibly marked unresolved and clicking it does not navigate.
9. **Given** the pane is docked and narrow, **When** a note is open, **Then** the outline and backlinks collapse to toggles rather than splitting the pane.

### US-8 — Agents can find and follow notes (Priority: P1)

As an agent I need to search the collection by relevance and ask what links to a note,
because grep cannot rank and cannot answer "what refers to this?".

**Why P1:** this is the half of #632 that changes what agents can do.

**Independent test:** an agent issues a relevance search and receives ranked paths with
titles and matched excerpts; asks for backlinks and receives every inbound link
regardless of which of the four link forms was used.

**Acceptance scenarios**

1. **Given** a knowledge base, **When** an agent searches, **Then** it receives ranked results carrying path, title and a matched excerpt.
2. **Given** a note linked from four notes using the four different link forms, **When** an agent asks for backlinks, **Then** all four are returned.
3. **Given** a request for more results than the cap, **When** it is served, **Then** the count is clamped to the cap and the clamping is reported rather than silently applied.
4. **Given** a neighbourhood request, **When** it is served, **Then** it is bounded by hop count and node count.
5. **Given** links pointing at notes that do not exist, **When** an agent asks for unresolved links, **Then** those are listed.

### US-9 — One workspace cannot read another's notes (Priority: P0)

As an operator I need an agent working in one workspace to be unable to read a
knowledge base I mounted only into a different workspace.

**Why P0:** workspace membership is the trust boundary in this product. An unscoped
search would quietly cross it.

**Independent test:** mount a knowledge base into workspace B only. An agent in
workspace A searches for text that exists only there and gets nothing.

**Acceptance scenarios**

1. **Given** a knowledge base mounted only into workspace B, **When** an agent in workspace A searches for a phrase unique to it, **Then** zero results are returned.
2. **Given** the same, **When** that agent asks for its backlinks or outline, **Then** the knowledge base is not addressable at all.
3. **Given** a knowledge base mounted into both workspaces, **When** agents in each search, **Then** both find it.

### US-10 — Links cannot be used to read outside the collection (Priority: P0)

As an operator I need a link inside a note to be incapable of reaching files outside
that collection, whether by traversal, an absolute path, or a symbolic link.

**Why P0:** the indexer walks a real folder on my disk with my permissions.

**Independent test:** author notes containing a traversal link, an absolute-path link,
and place a symlink inside the collection pointing outside it. None resolves, and no
read of the target occurs.

**Acceptance scenarios**

1. **Given** a note containing a link that traverses upwards out of the collection, **When** it is resolved, **Then** it is reported unresolved and the target is never read.
2. **Given** a link with an absolute filesystem path, **When** it is resolved, **Then** likewise.
3. **Given** a symbolic link inside the collection pointing outside it, **When** the collection is walked, **Then** it is skipped and reported, never followed.
4. **Given** a symbolic link forming a loop inside the collection, **When** the collection is walked, **Then** the walk completes and does not hang.

### US-11 — The same answer every time (Priority: P1)

As an operator I need the link graph to be built by rules, not judgement — so that the
same notes always produce the same answers, on any machine, with no agent involved.

**Why P1:** every downstream answer inherits the graph's correctness.

**Independent test:** delete and rebuild the index for a fixture collection. The ranked
results for a fixed set of queries, and the link/backlink/unresolved/orphan sets, are
identical.

**Acceptance scenarios**

1. **Given** a fixture collection, **When** the index is deleted and rebuilt, **Then** a fixed query set returns identical ranked results.
2. **Given** the same, **When** rebuilt, **Then** the link, backlink, unresolved and orphan sets are identical.
3. **Given** two notes sharing a basename, **When** a link uses that bare basename, **Then** it resolves by the stated tie-break **and** is additionally reported as ambiguous.
4. **Given** indexing runs, **When** it completes, **Then** no language model was called at any point.
5. **Given** no agent has ever run, **When** the graph is queried, **Then** it is complete and correct.

---

# STAGE 3 — Write path (gates on stage 2)

## 7. User Stories — Stage 3

### US-12 — Create a note that already fits (Priority: P1)

As an operator I want a New note action that starts from my own templates, so notes
arrive with the frontmatter and structure my collection expects instead of blank.

**Why P1:** there is no way to create a note in the Library at all today. A blank note
silently violates any collection with a schema.

**Independent test:** create a note from a template. Its frontmatter validates against
the collection's schema. Templates are editable without turning on hidden files.

**Acceptance scenarios**

1. **Given** a knowledge base with templates, **When** I create a note, **Then** I may choose a template and the new note contains its frontmatter and structure.
2. **Given** the created note, **When** it is validated against the collection's schema, **Then** it passes.
3. **Given** templates live in a hidden folder, **When** I want to edit one, **Then** a settings surface lists and opens them without enabling hidden files.
4. **Given** a template containing substitution placeholders, **When** a note is created, **Then** placeholders are replaced from a fixed documented set and nothing else is interpreted.
5. **Given** a template containing what looks like an instruction or a script, **When** a note is created, **Then** it is inserted as plain text and never executed.

### US-13 — Renaming a note does not break the collection (Priority: P0)

As an operator I need renaming or moving a note to update every link pointing at it —
including links in frontmatter, which Obsidian leaves broken — because those links are
how my collection is structured.

**Why P0:** 87% of notes in the reference collection carry frontmatter links. A rename
that only fixes body links silently severs most of the structure, in real files.

**Independent test:** rename a note with inbound links in both body and frontmatter.
Every link is updated. Interrupt the operation and confirm it is detectable and
completable.

**Acceptance scenarios**

1. **Given** a note with inbound links in note bodies, **When** I rename it, **Then** all are updated.
2. **Given** a note with inbound links in other notes' frontmatter, **When** I rename it, **Then** those are updated too.
3. **Given** frontmatter containing comments, anchors and nested structures, **When** a link in it is rewritten, **Then** only the link value changes and the rest is byte-identical.
4. **Given** a rename interrupted part-way, **When** the collection is checked, **Then** the interruption is reported and completing it produces the same result as an uninterrupted run.
5. **Given** a rename made outside Omnipus, **When** the collection is checked, **Then** the resulting broken links are listed as unresolved.

### US-14 — Nothing I wrote is ever silently lost (Priority: P0)

As an operator with a collection also touched by Obsidian, a sync agent, git and a CLI,
I need Omnipus to refuse a write when the file changed underneath it rather than
overwrite my work.

**Why P0:** the target collection genuinely has five concurrent writers. A lost note is
undetectable after the fact.

**Independent test:** read a note, modify it externally, then attempt to write. The
write is refused with an error naming the file. Two processes writing concurrently:
exactly one succeeds.

**Acceptance scenarios**

1. **Given** a note read at one version, **When** it is changed externally and a write is attempted, **Then** the write is refused with a typed error naming the path.
2. **Given** two Omnipus processes writing the same note at once, **When** both complete, **Then** exactly one succeeds and one fails — never both succeed with one write lost.
3. **Given** an external change that preserves the modification time, **When** a write is attempted, **Then** it is still detected and refused.
4. **Given** a lock that cannot be acquired, **When** the bound elapses, **Then** an error is returned within that bound and the operation never hangs.
5. **Given** a refused write, **When** it is refused, **Then** an audit record is written.

### US-15 — Every change to my files is on the record (Priority: P1)

As an operator I need every write an agent makes to my real files to be auditable,
including the ones that were refused.

**Why P1:** agents now write unattended to files outside the Library's audited path.

**Acceptance scenarios**

1. **Given** any knowledge-base mutation by an agent, **When** it completes, **Then** an audit record names the agent, the collection, the paths touched, the operation and the outcome.
2. **Given** a multi-file link rewrite, **When** it completes, **Then** the full set of touched paths is recorded, not just the renamed note.
3. **Given** a refused write, **When** it is refused, **Then** it is audited as a refusal, not omitted.

### US-16 — Sensible behaviour at the edges (Priority: P2)

As an operator I want the awkward moments handled: an empty collection, one I have
detached, one whose folder I moved, and files my cloud provider has not downloaded.

**Why P2:** each is individually small; together they are most of the first-week
experience.

**Acceptance scenarios**

1. **Given** an empty knowledge base that has finished indexing, **When** I open it, **Then** I am offered to create my first note from a template.
2. **Given** one folder mounted into two workspaces, **When** I revoke one mount, **Then** the other workspace's search keeps working.
3. **Given** the last mount of a collection is revoked, **When** I re-mount it within the grace period, **Then** no full rebuild occurs.
4. **Given** I rename the collection's folder on disk, **When** I return to Omnipus, **Then** the mount is shown as broken with an action to point it at the new location.
5. **Given** a note whose content is not on disk because the cloud provider evicted it, **When** indexing reaches it, **Then** it fails loudly and is absent from the index rather than present and empty.

---

# STAGE 4 — `ev` lock interoperation (gated on O-2)

### US-17 — Omnipus and `ev` never write at the same moment (Priority: P3)

As an operator running both tools against one collection I want them to genuinely
exclude each other rather than merely detect each other after the fact.

**Why P3:** stages 1–3 already prevent data loss through detection and refusal. This
upgrades `ev` from "detected" to "coordinated", and it cannot be built unilaterally.

**Gated on O-2** — requires `ev`'s lock-file layout and semantics to become a
documented, stable contract.

**Acceptance scenarios**

1. **Given** `ev` holds the collection-wide write lock, **When** Omnipus attempts a multi-file operation, **Then** it waits and then proceeds, rather than refusing.
2. **Given** Omnipus holds a per-path lock, **When** `ev` writes the same note, **Then** `ev` observes the lock.
3. **Given** `ev` is not installed, **When** Omnipus writes, **Then** behaviour is unchanged from stage 3.

---

## 8. Behavioral Contract

**Preview**
- When a file is HTML, PDF or audio, the system renders it rather than its source.
- When the reader presses Edit on a rendered file, the system shows its source.
- When an inline document is served, the system binds it to an opaque origin regardless of how it was opened.
- When a document requests any network destination, the system blocks it.
- When a file type is not on the inline allow-list, the system serves it as an attachment.

**Detection and identity**
- When a folder's root carries either marker, the system treats it as a knowledge base.
- When neither marker is present, the system treats it as an ordinary folder.
- When the system creates a knowledge base, it writes its own marker and never Obsidian's.

**Search and index**
- When a search runs against a partially indexed collection, the system returns results and states the incompleteness in the same response.
- When the total is not yet known, the system reports an indeterminate state rather than a ratio.
- When the index exists and the collection is unchanged, the system performs no rebuild.
- When an agent searches, the system restricts results to knowledge bases mounted into that agent's workspace.
- When a result count above the cap is requested, the system clamps it and reports the clamp.

**Graph**
- When a link resolves outside the collection root, the system reports it unresolved and does not read the target.
- When a symbolic link is encountered, the system skips and reports it.
- When a basename is ambiguous, the system resolves by the stated rule and additionally reports the ambiguity.
- When the index is rebuilt from the same files, the system produces identical query and graph answers.

**Writing**
- When a note is renamed, the system rewrites inbound links in bodies and frontmatter.
- When a write's version token does not match the file on disk, the system refuses and names the path.
- When a lock cannot be acquired within the bound, the system errors rather than waiting indefinitely.
- When any mutation completes or is refused, the system writes an audit record.

---

## 9. Edge Cases

| # | Condition | Expected behaviour |
|---|---|---|
| E-1 | Collection with 0 notes | First-run offer to create a note; search returns empty without error |
| E-2 | Collection with exactly 1 note | Search, outline and backlinks all work; orphans lists that note |
| E-3 | Collection with 100,000 notes | §1.2 targets met |
| E-4 | Note filename containing `:` or `?` | Not addressable through the Library; reported as an "unaddressable" class (residual R-7). Never silently omitted |
| E-5 | Note 200 MB in size | Indexed with a documented body cap, or skipped and reported — never an unbounded read |
| E-6 | Wikilink to a note that does not exist | Rendered as unresolved; listed by the unresolved query |
| E-7 | Two notes sharing a basename | Tie-break applies; ambiguity reported |
| E-8 | Symlink loop inside the collection | Walk terminates; loop reported |
| E-9 | Marker present but unreadable (permissions) | Detection fails loudly; the folder is not silently downgraded to "ordinary" |
| E-10 | Same folder mounted twice into one workspace | One index; both mounts usable; revoking one leaves the other working |
| E-11 | Collection folder deleted while mounted | Mount reported broken; index retained through the grace period |
| E-12 | HTML file with no closing tags / malformed | Rendered as the browser renders it; never crashes the pane |
| E-13 | HTML bundle referencing a missing asset | Page renders; the missing asset is visible as a failure, not silent |
| E-14 | Audio format outside the supported list | Download card, not a broken player |
| E-15 | Index directory deleted while Omnipus runs | Detected; rebuilt; no corrupt-state answers served in the meantime |
| E-16 | Two Omnipus processes opening the same index | One opens; the other reports a bounded lock error rather than hanging |
| E-17 | Frontmatter that is not valid YAML | Note still indexed for body text; frontmatter reported as malformed |
| E-18 | Template referencing an undefined placeholder | Placeholder left literal; reported; never blank-substituted silently |

---

## 10. Explicit Non-Behaviors and Safeguards

### 10.1 Qualitative prohibitions

- **NB-1** The system must not create `.obsidian/` in any folder, because fabricating another application's configuration directory makes Omnipus responsible for a format it does not own.
- **NB-2** The system must not render Office documents, because no browser renders them natively and every accurate route requires a runtime dependency this project forbids. (PDF is different: PDF.js is a pure client-side library under a compatible licence, measured to work.)
- **NB-13** The system must not promise cryptographic or legally-verifiable signatures. A drawn signature is an image of intent. PKI signing is a separate decision with its own ADR.
- **NB-14** The system must not claim XFA form support, nor agent-driven form filling; neither is supported by the chosen renderer.
- **NB-3** The system must not build a whole-collection graph visualisation, because it is the surface that fails at scale in every comparable tool.
- **NB-4** The system must not change relative-link handling outside the knowledge-base reader, because the shared helper is consumed by chat markdown, which renders untrusted model output. **The Go/TS divergence in §2.4 is recorded, not resolved.**
- **NB-5** The system must not call a language model anywhere in the indexing, resolution or link-rewriting path, because derived data must be reproducible.
- **NB-6** The system must not write its index inside the operator's collection, because it would be synced, versioned and backed up as though it were their data.
- **NB-7** The system must not follow symbolic links out of a collection, because the indexer runs with the operator's full filesystem permissions.
- **NB-8** The system must not overwrite a file that changed since it was read, even when the change looks trivial.
- **NB-9** The system must not silently skip files it cannot address or read; every exclusion must be reportable.
- **NB-10** The system must not search across workspace boundaries, because workspace membership is the product's trust boundary.
- **NB-11** The system must not execute or interpret template content beyond a fixed documented substitution set.
- **NB-12** The system must not relax `pathsafe` for mounted folders as a side effect of this work; that is a separate decision with its own blast radius.

### 10.2 Machine-verifiable constraints

**Performance** (measured on the 100,000-note fixture)
- MV-1 Search 95th-percentile latency < 500 ms.
- MV-2 Peak resident memory during first index < 512 MB.
- MV-3 Steady-state resident memory with index open and idle < 64 MB.
- MV-4 Unchanged-collection freshness check < 2 s.
- MV-5 First usable partial result < 5 s after opening the collection.

**Limits**
- MV-6 Result count requested above 100 is clamped to 100 and the response states it was clamped.
- MV-7 Default result count is 20.
- MV-8 Excerpt length capped at 512 bytes per hit.
- MV-9 Neighbourhood queries bounded at 3 hops and 500 nodes.
- MV-10 Lock acquisition bound 5 s, configurable; expiry returns an error.

**Responses**
- MV-11 A version-token mismatch returns HTTP 409 with a typed error body naming the path.
- MV-12 A request for a knowledge base outside the caller's workspace returns an empty result set — not 403, which would confirm its existence.
- MV-13 An inline preview response carries a content-security policy establishing an opaque origin; an attachment response does not.
- MV-14 Each supported audio extension returns its specific MIME type, never `application/octet-stream`.
- MV-15 Index directory mode 0700; index file mode 0600.

**Boot**
- MV-16 Booting with all knowledge tools registered produces zero tool-policy coverage gaps.
- MV-17 Loading a configuration written before this feature yields the seeded policy posture, never `deny`.

---

## 11. Integration Boundaries

### bleve scorch (in-process index)
- **In:** note text, frontmatter, paths. **Out:** ranked hits.
- **Contract:** existing direct dependency; no version change.
- **Failure:** corrupt index → remove and rebuild. Lock contention → bounded error, never a hang.
- **Development approach:** real, not mocked — a mocked index cannot exercise the ranking or scale criteria.
- **Regression boundary:** the pattern from `pkg/memrooms/index` is **copied into a sibling package**, not generalised in place. `OpenOrCreate` is LOW risk (5 impacted, 1 direct caller in `pkg/agent/memory.go`) but memory-room recall is live functionality and the shared surface is small enough that duplication is cheaper than coupling.

### The operator's filesystem (mounted collection)
- **In:** note files. **Out:** note writes, marker, templates.
- **Contract:** POSIX; cross-process locking via advisory locks.
- **Failure:** evicted file → loud error. Missing folder → broken mount. Permission denied → reported, never skipped silently.
- **Development approach:** real temporary directories. Cross-process behaviour tested by re-executing the test binary as separate OS processes, matching `pkg/entity`'s existing shape.

### `ev` CLI (stage 4 only)
- **In/Out:** shared advisory lock files outside the collection.
- **Contract:** **not yet agreed — this is O-2.**
- **Failure:** `ev` absent → stage-3 behaviour unchanged.
- **Development approach:** simulated lock-holder process; real `ev` for acceptance.

### The browser (preview rendering)
- **In:** document bytes plus security headers. **Out:** rendered document.
- **Contract:** content-security policy semantics, which **differ between engines** — hence the three-browser gate.
- **Failure:** if no single-origin policy satisfies both isolation and subresource loading, fall back to serving previews from a distinct origin (ADR alternative A14).
- **Development approach:** real browsers. A unit test asserting a policy string proves nothing about whether a page renders.

---

## 12. BDD Scenarios

### Feature: Render-first preview

```gherkin
Scenario: An HTML page renders instead of showing its markup
  Given a workspace contains "report.html"
  When the operator selects "report.html" in the Library
  Then the preview pane displays the rendered page
  And the pane does not display the file's markup
# Traces to: US-1, AS-1

Scenario: Source is available behind Edit
  Given "report.html" is rendered in the preview pane
  When the operator presses Edit
  Then the pane displays the file's source in an editor
# Traces to: US-1, AS-2

Scenario: Scripts in a previewed page execute
  Given a workspace contains an HTML file whose script appends visible text
  When the operator selects that file
  Then the appended text is visible in the rendered page
# Traces to: US-1, AS-3

Scenario: A complete bundle loads all of its assets
  Given a workspace folder contains "index.html", "style.css", "app.js" and "font.woff2"
  And "index.html" references all three by relative path
  When the operator selects "index.html"
  Then the stylesheet is applied
  And the script has executed
  And the webfont is used to render text
# Traces to: US-1, AS-4

Scenario Outline: Documents and media render natively
  Given a workspace contains a file "<file>"
  When the operator selects it
  Then the pane displays "<surface>"

  Examples:
    | file        | surface              |
    | manual.pdf  | the PDF viewer       |
    | podcast.mp3 | an audio player      |
    | archive.zip | the download card    |
# Traces to: US-1, AS-5, AS-6, AS-7

Scenario: A previewed page cannot read the session cookie in a top-level tab
  Given a workspace contains an HTML file that reads document.cookie and displays it
  When the operator opens that file's Library URL as a top-level browser tab
  Then the displayed cookie value is empty
  And the document's origin is opaque
# Traces to: US-2, AS-1

Scenario: A previewed page cannot read the session cookie when embedded
  Given the same HTML file
  When it renders inside the Library preview pane
  Then the displayed cookie value is empty
# Traces to: US-2, AS-2

Scenario: A previewed page cannot reach the network
  Given a workspace contains an HTML file that requests an external URL on load
  When it renders
  Then the request is blocked
# Traces to: US-2, AS-3

Scenario: Untrusted content is visibly marked
  Given any file is rendered inline in the preview pane
  When the operator looks at the pane
  Then a persistent untrusted-content boundary is shown outside the rendered frame
# Traces to: US-2, AS-4

Scenario: File types outside the inline allow-list are still attachments
  Given a workspace contains "data.bin"
  When it is fetched from the Library
  Then it is served as an attachment
# Traces to: US-2, AS-5

Scenario: Private comments do not render
  Given a note containing "%%internal aside%%"
  When it renders
  Then neither the marker nor "internal aside" appears
# Traces to: US-3, AS-1

Scenario: A selected file is addressable by URL
  Given the operator selects "notes/plan.md"
  When they inspect the address
  Then it identifies "notes/plan.md"
  And loading it fresh reopens that file selected
# Traces to: US-3, AS-2, AS-3

Scenario: A URL for a missing file degrades gracefully
  Given a Library URL naming a path that does not exist
  When it is loaded
  Then the Library opens at the containing folder
  And a not-found message names the missing path
# Traces to: US-3, AS-5
```

### Feature: Knowledge base detection and identity

```gherkin
Scenario Outline: Marker presence decides knowledge-base status
  Given a mounted folder containing "<marker>"
  When it is opened
  Then it is treated as "<verdict>"

  Examples:
    | marker           | verdict           |
    | .obsidian/       | a knowledge base  |
    | .omnipus-vault/  | a knowledge base  |
    | neither          | an ordinary folder|
# Traces to: US-4, AS-1, AS-2, AS-3

Scenario: Detection reads no file contents
  Given a mounted folder with a marker and 500 notes
  When detection runs
  Then no note file is opened for reading
# Traces to: US-4, AS-4

Scenario: Creating a knowledge base writes only the Omnipus marker
  Given the operator creates a knowledge base
  When creation completes
  Then ".omnipus-vault/" exists at its root
  And ".obsidian/" does not exist
# Traces to: US-4, AS-5

Scenario: Identity survives relocation
  Given a knowledge base named "Research" mounted at one path
  When the operator moves the folder and mounts it at another path
  Then it is still named "Research"
  And no migration step was required
# Traces to: US-4, AS-6
```

### Feature: Search behaviour and honesty

```gherkin
Scenario: Partial results are labelled as partial
  Given a knowledge base whose first index is in progress with a known total
  When the operator searches
  Then results are returned
  And the same response states how many notes of the total are indexed
# Traces to: US-6, AS-2, AS-3

Scenario: An unknown total is not shown as a ratio
  Given a knowledge base whose file tree is still being walked
  When the operator searches
  Then the response states a count found so far
  And it does not state a ratio
# Traces to: US-6, AS-1

Scenario: Completed indexing shows no incompleteness notice
  Given a fully indexed knowledge base
  When the operator searches
  Then no incompleteness statement is present
# Traces to: US-6, AS-4

Scenario: Indexing state outranks the empty-collection first run
  Given a newly created knowledge base that is still indexing
  When the operator opens it
  Then the indexing state is shown
  And the empty-collection first run is not shown
# Traces to: US-6, AS-5

Scenario: A fast unchanged reconcile shows nothing
  Given an unchanged knowledge base whose freshness check completes under the threshold
  When it is opened
  Then no progress banner appears
# Traces to: US-6, AS-6

Scenario: Requested result counts above the cap are clamped and reported
  Given a knowledge base with 500 matching notes
  When an agent requests 400 results
  Then 100 results are returned
  And the response states that the count was clamped
# Traces to: US-8, AS-3
```

### Feature: Workspace isolation

```gherkin
Scenario: An agent cannot search another workspace's knowledge base
  Given a knowledge base mounted only into workspace B
  And it contains the unique phrase "zarquon-seven"
  When an agent in workspace A searches for "zarquon-seven"
  Then zero results are returned
# Traces to: US-9, AS-1

Scenario: An agent cannot address another workspace's knowledge base at all
  Given the same knowledge base
  When an agent in workspace A requests its backlinks
  Then the knowledge base is not addressable
# Traces to: US-9, AS-2

Scenario: A shared knowledge base is visible from both workspaces
  Given a knowledge base mounted into workspace A and workspace B
  When agents in each search for a phrase it contains
  Then both receive results
# Traces to: US-9, AS-3
```

### Feature: Link resolution and containment

```gherkin
Scenario Outline: Links that escape the collection never resolve
  Given a note containing the link "<link>"
  When the link is resolved
  Then it is reported unresolved
  And the target path is never read

  Examples:
    | link                        |
    | [[../../../.ssh/id_rsa]]    |
    | [[/etc/passwd]]             |
# Traces to: US-10, AS-1, AS-2

Scenario: Symbolic links are skipped, not followed
  Given a symbolic link inside the collection pointing outside it
  When the collection is walked
  Then the link is skipped
  And it is reported
  And nothing outside the collection is read
# Traces to: US-10, AS-3

Scenario: A symlink loop terminates the walk
  Given a symbolic link inside the collection forming a loop
  When the collection is walked
  Then the walk completes
  And the loop is reported
# Traces to: US-10, AS-4

Scenario Outline: Every wikilink form resolves
  Given a note "Target" and a note linking to it as "<form>"
  When links are resolved
  Then the link resolves to "Target"

  Examples:
    | form                |
    | [[Target]]          |
    | [[Target\|alias]]   |
    | [[Target#Heading]]  |
    | [[folder/Target]]   |
# Traces to: US-7, AS-1; US-8, AS-2

Scenario: An ambiguous basename resolves deterministically and is reported
  Given two notes named "Index" in different folders
  And a note linking to "[[Index]]"
  When links are resolved
  Then the link resolves to the shorter path
  And the ambiguity is reported
# Traces to: US-11, AS-3
```

### Feature: Reproducibility

```gherkin
Scenario: Rebuilding produces identical answers
  Given a fixture knowledge base and a fixed set of queries
  When the index is deleted and rebuilt
  Then each query returns an identical ranked result set
  And the link, backlink, unresolved and orphan sets are identical
# Traces to: US-11, AS-1, AS-2

Scenario: Indexing never calls a language model
  Given a knowledge base
  When it is indexed and its links resolved
  Then no model request was issued
# Traces to: US-11, AS-4
```

### Feature: Writing safely

```gherkin
Scenario: Renaming updates body and frontmatter links
  Given a note "Old" linked from a note body and from another note's frontmatter
  When "Old" is renamed to "New"
  Then the body link points at "New"
  And the frontmatter link points at "New"
# Traces to: US-13, AS-1, AS-2

Scenario: Frontmatter survives rewriting untouched apart from the link
  Given a note whose frontmatter contains comments, anchors and nested lists
  And it links to "Old"
  When "Old" is renamed
  Then only the link value differs from the original frontmatter
# Traces to: US-13, AS-3

Scenario: An interrupted rename is detectable and completable
  Given a rename affecting twenty inbound links
  When the process is killed after the journal is written and before all rewrites complete
  And the collection is checked
  Then the incomplete rename is reported
  And completing it yields the same result as an uninterrupted rename
# Traces to: US-13, AS-4

Scenario: A stale write is refused
  Given a note read at one version
  And the note is modified by another program
  When a write using the original version is attempted
  Then the write is refused
  And the error names the path
  And the file on disk is unchanged
# Traces to: US-14, AS-1

Scenario: An external change that preserves modification time is still detected
  Given a note read at one version
  And the note is rewritten externally with its modification time restored
  When a write using the original version is attempted
  Then the write is refused
# Traces to: US-14, AS-3

Scenario: Concurrent writers cannot both win
  Given two Omnipus processes writing the same note simultaneously
  When both complete
  Then exactly one reports success
  And exactly one reports a conflict
# Traces to: US-14, AS-2

Scenario: A refused write is audited
  Given a write refused because the file changed
  When the refusal completes
  Then an audit record names the agent, the collection, the path and the refusal
# Traces to: US-14, AS-5; US-15, AS-3
```

### Feature: Lifecycle

```gherkin
Scenario: An empty collection offers a first note
  Given an empty knowledge base that has finished indexing
  When the operator opens it
  Then they are offered to create a first note from a template
# Traces to: US-16, AS-1

Scenario: Revoking one of two mounts does not destroy the shared index
  Given one folder mounted into workspace A and workspace B
  When the mount in workspace A is revoked
  Then search in workspace B still returns results
# Traces to: US-16, AS-2

Scenario: Re-mounting inside the grace period skips a rebuild
  Given the last mount of a collection was revoked
  When it is re-mounted within the grace period
  Then no full rebuild occurs
# Traces to: US-16, AS-3

Scenario: A moved collection folder surfaces a broken mount
  Given a mounted collection whose folder is renamed on disk
  When the operator opens the Library
  Then the mount is shown as broken
  And an action is offered to point it at the new location
# Traces to: US-16, AS-4

Scenario: An evicted file is never indexed as empty
  Given a note whose content has been evicted by the cloud provider
  When indexing reaches it
  Then indexing reports a loud error for that file
  And the note is absent from the index
# Traces to: US-16, AS-5
```

### Feature: Boot and policy

```gherkin
Scenario: Booting with the new tools produces no coverage gaps
  Given a fresh installation with all knowledge tools registered
  When the gateway boots
  Then it starts successfully
  And no tool-policy coverage gap is reported
# Traces to: FR-070

Scenario: An existing configuration is migrated rather than denied
  Given a configuration written before this feature existed
  When it is loaded
  Then the knowledge tools carry their seeded posture
  And none was backfilled to deny
# Traces to: FR-071
```

---

## 13. Test-Driven Development Plan

### 13.1 Test hierarchy and order

Unit → integration → end-to-end, within each stage. Cross-process and browser tests
come last within their stage because they are slowest and most environment-dependent.

| Order | Test name | Level | Traces to scenario | Notes |
|---|---|---|---|---|
| **Stage 1** |
| 1 | `TestClassifyLibraryEntry_ExistingKindsUnchanged` | Unit | Regression | **HIGH-risk guard.** Every pre-existing input keeps its current kind |
| 2 | `TestClassifyLibraryEntry_NewKinds` | Unit | US-1 AS-1,5,6 | html / pdf / audio classification |
| 3 | `TestContentTypeForPath_AudioExtensions` | Unit | MV-14 | Each audio extension → specific MIME |
| 4 | `TestInlineDisposition_AllowListOnly` | Unit | US-2 AS-5 | Non-allow-listed types stay attachments |
| 5 | `TestInlinePreview_ResponseCarriesIsolationPolicy` | Integration | US-2 AS-1 | Response headers, independent of embedder |
| 6 | `TestStripPrivateComments` | Unit | US-3 AS-1 | `%%…%%` removed |
| 7 | `TestLibraryDeepLink_RoundTrip` | Integration | US-3 AS-2,3 | Select → URL → reload → same file |
| 8 | `TestLibraryDeepLink_MissingPath` | Integration | US-3 AS-5 | Graceful not-found |
| 9 | `E2E_PreviewBundle_AllAssetsLoad` | E2E (browser) | US-1 AS-4 | **Real browser.** css + js + font + audio |
| 10 | `E2E_PreviewIsolation_TopLevelNavigation` | E2E (browser) | US-2 AS-1 | Cookie unreadable, origin opaque |
| 11 | `E2E_PreviewIsolation_NetworkBlocked` | E2E (browser) | US-2 AS-3 | Egress blocked |
| 12 | `E2E_PreviewIsolation_BrowserMatrix` | E2E (browser) | MV-13 | Chrome + Firefox + Safari |
| 57 | `E2E_PdfRendersViaPdfJs` | E2E (browser) | US-1 AS-5, AC-15.4 | **Real browser, 3 engines, HEADED.** Headless has no PDF viewer and previously produced a false negative |
| 58 | `TestTypeConfusion_HtmlNamedPdfDoesNotExecute` | Integration + E2E | FR-015, AC-15.5 | **The critical control.** Served `application/pdf`, `nosniff` present, no script runs, nothing reaches an external origin. Requires a **positive control** (same payload as `text/html`) proving the detection is not blind |
| 59 | `TestInlineAllowList_RequiresTypeConfusionTest` | Unit (build gate) | FR-016, AC-15.7 | Adding an extension without a test fails CI |
| 60 | `E2E_FontAppliesWithCorsHeader` | E2E (browser) | AC-15.1, FR-019 | Real font covering the measured glyphs, on an inline element, asserted by **rendered width**. `document.fonts.status` is NOT the oracle — it reports "loaded" on failure |
| 61 | `TestPdfJsBundleLazyLoaded` | Unit (build) | AC-15.6, FR-018 | PDF.js absent from the initial SPA payload |
| **Stage 2** |
| 13 | `TestDetectKnowledgeBase_MarkerMatrix` | Unit | US-4 AS-1,2,3 | Both markers, neither |
| 14 | `TestDetectKnowledgeBase_NoContentReads` | Unit | US-4 AS-4 | Read-counting fake |
| 15 | `TestCreateKnowledgeBase_WritesOwnMarkerOnly` | Integration | US-4 AS-5 | No `.obsidian/` |
| 16 | `TestKnowledgeBaseIdentity_SurvivesRelocation` | Integration | US-4 AS-6 | |
| 17 | `TestResolveLink_AllFourForms` | Unit | US-7 AS-1 | |
| 18 | `TestResolveLink_TieBreakAndAmbiguityReport` | Unit | US-11 AS-3 | Resolves **and** reports |
| 19 | `TestResolveLink_ContainmentTraversal` | Unit | US-10 AS-1,2 | Read-recording fake proves no read |
| 20 | `TestWalk_SymlinkSkippedAndReported` | Unit | US-10 AS-3 | |
| 21 | `TestWalk_SymlinkLoopTerminates` | Unit | US-10 AS-4 | |
| 22 | `TestIndexLocation_OutsideCollection` | Integration | NB-6 | Before/after tree diff of the collection |
| 23 | `TestIndexPermissions_0700_0600` | Integration | MV-15 | |
| 24 | `TestIndexIdentity_SharedByRealpath` | Integration | US-16 AS-2 | One folder, two mounts, one index |
| 25 | `TestManifest_ReparsesOnlyChangedFiles` | Integration | US-5 AS-6 | |
| 26 | `TestSearchScope_CrossWorkspaceReturnsEmpty` | Integration | US-9 AS-1 | **Negative test, required** |
| 27 | `TestSearchScope_SharedMountVisibleToBoth` | Integration | US-9 AS-3 | |
| 28 | `TestSearchResultCap_ClampedAndReported` | Unit | US-8 AS-3 | |
| 29 | `TestProgress_EnumerationHasNoRatio` | Unit | US-6 AS-1 | |
| 30 | `TestProgress_PartialResultsCarryIncompleteness` | Integration | US-6 AS-3 | Same payload |
| 31 | `TestProgress_EmptyVsIndexingPrecedence` | Unit | US-6 AS-5 | |
| 32 | `TestRebuild_IdenticalQueryAndGraphAnswers` | Integration | US-11 AS-1,2 | **Never byte comparison** |
| 33 | `TestIndexing_NoModelCalls` | Unit | US-11 AS-4 | Failing model client |
| 34 | `TestBoot_ZeroToolPolicyGaps` | Integration | FR-070 | |
| 35 | `TestConfigMigration_NoDenyBackfill` | Integration | FR-071 | |
| 36 | `TestContracts_NoDrift` | Integration | FR-080 | `make verify-contracts` |
| 37 | `Bench_Search_p95_100k` | Perf | MV-1 | Fixture collection |
| 38 | `Bench_InitialIndex_PeakRSS_100k` | Perf | MV-2 | |
| 39 | `Bench_Reconcile_Unchanged_100k` | Perf | MV-4 | |
| **Stage 3** |
| 40 | `TestNewNote_FromTemplateValidates` | Integration | US-12 AS-1,2 | |
| 41 | `TestTemplates_ReachableWithoutHiddenFiles` | Integration | US-12 AS-3 | |
| 42 | `TestTemplate_NoExecution` | Unit | US-12 AS-5, NB-11 | Instruction-looking content stays literal |
| 43 | `TestRename_RewritesBodyAndFrontmatter` | Integration | US-13 AS-1,2 | |
| 44 | `TestRename_FrontmatterByteStableApartFromLink` | Integration | US-13 AS-3 | |
| 45 | `TestRename_InterruptedIsDetectedAndCompletable` | Integration | US-13 AS-4 | Kill after journal |
| 46 | `TestWrite_StaleVersionTokenRefused` | Integration | US-14 AS-1 | |
| 47 | `TestWrite_MtimePreservedChangeStillDetected` | Integration | US-14 AS-3 | Hash, not mtime |
| 48 | `TestWrite_ConcurrentCrossProcess_ExactlyOneWins` | Integration | US-14 AS-2 | **Re-executes the test binary as real OS processes**, matching `pkg/entity` |
| 49 | `TestLock_BoundedWaitErrors` | Unit | MV-10 | |
| 50 | `TestAudit_MutationAndRefusalRecorded` | Integration | US-15 AS-1,3 | |
| 51 | `TestAudit_MultiFileRewriteRecordsAllPaths` | Integration | US-15 AS-2 | |
| 52 | `TestLifecycle_RevokeRefCountAndGrace` | Integration | US-16 AS-2,3 | |
| 53 | `TestLifecycle_BrokenMountSurfaced` | Integration | US-16 AS-4 | |
| 54 | `TestEvicted_LoudFailNotEmptyIndex` | Integration | US-16 AS-5 | |
| **Stage 4** |
| 55 | `TestEvInterop_SharedLockObserved` | Integration | US-17 AS-1,2 | Simulated `ev` lock holder |
| 56 | `TestEvInterop_AbsentEvUnchanged` | Integration | US-17 AS-3 | |

### 13.2 Test datasets

**DS-1 — Link resolution**

| # | Input link | Collection state | Expected | Traces to |
|---|---|---|---|---|
| 1 | `[[Target]]` | one `Target.md` | resolves | US-7 AS-1 |
| 2 | `[[Target\|alias]]` | one `Target.md` | resolves, alias shown | US-7 AS-1 |
| 3 | `[[Target#Heading]]` | heading exists | resolves to heading | US-7 AS-1 |
| 4 | `[[folder/Target]]` | nested | resolves | US-7 AS-1 |
| 5 | `[[Index]]` | `a/Index.md`, `b/c/Index.md` | resolves to `a/Index.md`; ambiguity reported | US-11 AS-3 |
| 6 | `[[Missing]]` | absent | unresolved | US-7 AS-8 |
| 7 | `[[../../../.ssh/id_rsa]]` | file exists outside | unresolved; **no read** | US-10 AS-1 |
| 8 | `[[/etc/passwd]]` | exists | unresolved; **no read** | US-10 AS-2 |
| 9 | `[[]]` | — | unresolved, no crash | E-6 |
| 10 | `[[Target]]` × 5,000 in one note | one target | all resolve; bounded time | E-5 |

**DS-2 — Collection scale**

| # | Notes | Attachments | Expected | Traces to |
|---|---|---|---|---|
| 1 | 0 | 0 | first-run offer; empty search | E-1 |
| 2 | 1 | 0 | search/outline/backlinks work; the note is an orphan | E-2 |
| 3 | 748 | 2,108 | reference shape; all features work | — |
| 4 | 100,000 | 0 | MV-1..MV-5 met | US-5 |
| 5 | 100,000 | 100,000 | MV-1..MV-5 met with attachments present | O-6 |

**DS-3 — Filenames**

| # | Filename | Expected | Traces to |
|---|---|---|---|
| 1 | `Ordinary Note.md` | fully addressable | — |
| 2 | `Meeting: 2026-01-01.md` | **not addressable**; reported unaddressable | E-4, R-7 |
| 3 | `Why?.md` | as above | E-4 |
| 4 | `elicify-* packages.md` | as above (present in the reference collection) | E-4 |
| 5 | `Ünïcödé — Näme.md` | fully addressable | — |
| 6 | `.hidden.md` | indexed; hidden in the explorer unless shown | M-13 |
| 7 | 300-character basename | rejected by the length rule; reported | E-4 |
| 8 | `CON.md` | not addressable; reported | E-4 |

**DS-4 — Write conflicts**

| # | Scenario | Expected | Traces to |
|---|---|---|---|
| 1 | Read, write, unchanged in between | succeeds | US-14 |
| 2 | Read, external edit, write | refused, path named | US-14 AS-1 |
| 3 | Read, external edit with mtime restored, write | refused | US-14 AS-3 |
| 4 | Two processes, same note, simultaneous | exactly one succeeds | US-14 AS-2 |
| 5 | Write with no version token | rejected as malformed | MV-11 |
| 6 | Lock held beyond the bound | error inside the bound | MV-10 |
| 7 | File deleted between read and write | refused, reported as missing | E-11 |

**DS-5 — Preview inputs**

| # | File | Expected | Traces to |
|---|---|---|---|
| 1 | `page.html`, self-contained | renders | US-1 AS-1 |
| 2 | bundle: html + css + js + font | all four load | US-1 AS-4 |
| 3 | html referencing a missing asset | renders; failure visible | E-13 |
| 4 | malformed html | renders as the browser does; no crash | E-12 |
| 5 | html reading `document.cookie` | empty | US-2 AS-1 |
| 6 | html issuing a network request | blocked | US-2 AS-3 |
| 7 | `doc.pdf` | native viewer | US-1 AS-5 |
| 8 | `.mp3`, `.m4a`, `.aac`, `.ogg`, `.opus`, `.wav`, `.flac` | each plays | MV-14 |
| 9 | `audio.aiff` (unsupported) | download card | E-14 |
| 10 | `archive.zip` | download card unchanged | US-1 AS-7 |

### 13.3 Regression requirements

This feature **modifies existing functionality**. Behaviour that must be preserved:

| Existing behaviour | Protected by |
|---|---|
| Every current preview classification is unchanged | `TestClassifyLibraryEntry_ExistingKindsUnchanged` (**HIGH-risk guard**) |
| Chat markdown continues to reject relative links | Existing `MarkdownText.test.tsx` and `markdown-shared.test.tsx` **stay green and unmodified** |
| Non-preview Library downloads remain attachments | `TestInlineDisposition_AllowListOnly` |
| Memory-room recall is unaffected | `pkg/memrooms/**` suites stay green; sibling-package approach means no source change |
| `web_serve` preview tokens are not evicted | No `ServedSubdirs` registration is added by this feature |
| Existing workspace CSP behaviour is unchanged | `buildWorkspaceCSP` is not modified; new policy is a separate route |

**Seam tests** — `pkg/library` is called in a new way (inline disposition) and
`pkg/workspace` is called in a new way (mount → knowledge-base resolution). Both get
explicit seam tests: items 4 and 26.

---

## 14. Functional Requirements

**Preview (stage 1)**
- **FR-001** The system MUST render HTML, PDF and audio files in the preview pane instead of their source or a download card.
- **FR-002** The system MUST show a file's source only after the reader chooses Edit.
- **FR-003** The system MUST load relative subresources of an HTML bundle.
- **FR-004** The system MUST execute scripts in a previewed HTML document.
- **FR-005** The system MUST bind every inline-previewed document to an opaque origin, established by the response and not by the embedder.
- **FR-006** The system MUST block network egress from a previewed document.
- **FR-007** The system MUST display a persistent untrusted-content boundary outside any inline-rendered frame.
- **FR-008** The system MUST continue to serve non-allow-listed file types as attachments.
- **FR-009** The system MUST return a specific MIME type for every supported audio extension.
- **FR-010** The system MUST NOT render Office documents.
- **FR-011** The system MUST hide `%%…%%` comments when rendering markdown.
- **FR-012** The system MUST make the selected file addressable by URL.
- **FR-013** The system MUST NOT alter relative-link handling outside the knowledge-base reader.
- **FR-014** The system MUST sandbox content the **browser** executes (HTML and bundles). Formats Omnipus renders itself — images, video, audio, markdown, Mermaid, code and PDF — are drawn by SPA components, never become browser documents, and therefore have no sandbox to apply.
- **FR-015** The system MUST derive `Content-Type` from the file extension, never from content sniffing, and MUST send `X-Content-Type-Options: nosniff` on every inline response.
- **FR-016** The system MUST fail its build if an extension is added to the inline allow-list without a corresponding type-confusion test.
- **FR-017** The system MUST describe the isolation rule accurately: only content the browser executes is sandboxed. It MUST NOT imply that formats Omnipus renders itself are sandboxed, nor that HTML is not.
- **FR-018** The system MUST render PDFs with PDF.js inside the SPA, as a component alongside the existing image and video previews, and MUST load that bundle lazily rather than in the initial payload.
- **FR-019** The system MUST serve font responses with `Access-Control-Allow-Origin` so webfonts in sandboxed HTML bundles resolve; it MUST NOT rely on `document.fonts.status` as a success signal.

**Detection and identity (stage 2)**
- **FR-020** The system MUST treat a folder as a knowledge base if its root contains `.omnipus-vault/` or `.obsidian/`.
- **FR-021** The system MUST NOT read file contents to decide detection.
- **FR-022** The system MUST write `.omnipus-vault/` when creating or initialising a knowledge base.
- **FR-023** The system MUST NOT create `.obsidian/`.
- **FR-024** The system MUST store a knowledge base's display name and template location in its marker.
- **FR-025** The system MUST create knowledge bases inside the workspace tree, not at arbitrary host paths.

**Index and search (stage 2)**
- **FR-030** The system MUST store the index outside the collection.
- **FR-031** The system MUST key the index by the collection root's resolved real path and reference-count it across mounts.
- **FR-032** The system MUST create index directories 0700 and index files 0600.
- **FR-033** The system MUST re-parse only files whose recorded size, modification time or content hash changed.
- **FR-034** The system MUST index in bounded-memory batches, never a single whole-collection batch.
- **FR-035** The system MUST return partial results with an incompleteness statement in the same response.
- **FR-036** The system MUST report an indeterminate state while the total is unknown.
- **FR-037** The system MUST clamp result counts above the cap and report the clamping.
- **FR-038** The system MUST provide a drift check that runs without any agent.
- **FR-039** The system MUST persist the index across restarts without rebuilding.

**Graph (stage 2)**
- **FR-040** The system MUST resolve links by exact path, then unique basename, then shortest path, then lexicographic order.
- **FR-041** The system MUST report an ambiguous basename as ambiguous even though it resolves.
- **FR-042** The system MUST report a link as unresolved when no target matches.
- **FR-043** The system MUST ensure every walked path and resolved target resolves inside the collection root.
- **FR-044** The system MUST skip and report symbolic links rather than following them.
- **FR-045** The system MUST NOT invoke a language model in the indexing, resolution or rewriting path.
- **FR-046** The system MUST produce identical query and graph answers after a rebuild from unchanged files.

**Retrieval and isolation (stage 2)**
- **FR-050** The system MUST provide relevance search returning path, title and matched excerpt.
- **FR-051** The system MUST provide link, backlink, unresolved, orphan and neighbourhood queries.
- **FR-052** The system MUST scope all retrieval to knowledge bases mounted into the calling agent's workspace.
- **FR-053** The system MUST return an empty result set, not a permission error, for out-of-scope collections.
- **FR-054** The system MUST bound neighbourhood queries by hop count and node count.
- **FR-055** The system MUST rate-limit agent retrieval.

**Reading surface (stage 2)**
- **FR-060** The system MUST render wikilinks, aliased links, heading links, path links and embeds.
- **FR-061** The system MUST render callouts and highlights, and MUST NOT render frontmatter as body content.
- **FR-062** The system MUST show an outline of a note's headings.
- **FR-063** The system MUST show inbound links for the open note.
- **FR-064** The system MUST collapse the reading rail to toggles when docked.
- **FR-065** The system MUST mark unresolved links visibly and MUST NOT navigate on click.

**Boot, contracts, audit (stage 2)**
- **FR-070** The system MUST enumerate every knowledge tool explicitly in the default configuration and per core agent, with no wildcards.
- **FR-071** The system MUST migrate existing configurations to the seeded posture rather than allowing the deny-backfill to apply.
- **FR-080** The system MUST define every new cross-boundary type in the contracts before any implementation code, and MUST carry index progress as a streaming frame rather than a REST field.
- **FR-090** The system MUST write an audit record for every knowledge-base mutation and every refusal.

**Writing (stage 3)**
- **FR-100** The system MUST offer note creation from collection-defined templates.
- **FR-101** The system MUST make template surfaces reachable without enabling hidden files.
- **FR-102** The system MUST substitute only a fixed documented placeholder set and MUST NOT execute template content.
- **FR-103** The system MUST rewrite inbound links in both note bodies and frontmatter on rename or move.
- **FR-104** The system MUST journal planned rewrites before applying any, and MUST make an interrupted rewrite detectable and completable.
- **FR-105** The system MUST preserve all frontmatter apart from the rewritten link value.
- **FR-106** The system MUST require a version token for every write and MUST refuse on mismatch.
- **FR-107** The system MUST NOT rely on modification time alone to detect an external change.
- **FR-108** The system MUST bound lock waits and MUST error rather than hang on expiry.
- **FR-109** The system MUST delete a collection's index only when its last mount is revoked and a grace period has elapsed.
- **FR-110** The system MUST surface a broken mount with an action to re-point it.
- **FR-111** The system MUST fail loudly on an evicted file and MUST NOT index it as empty.
- **FR-112** The system MUST report files it cannot address, rather than omitting them silently.

**Interoperation (stage 4)**
- **FR-120** The system SHOULD honour `ev`'s advisory locks when a stable contract exists.
- **FR-121** The system MUST behave exactly as in stage 3 when `ev` is absent.

---

## 15. Success Criteria

- **SC-001** Search 95th-percentile latency is under 500 ms on a 100,000-note collection.
- **SC-002** Peak memory during a 100,000-note first index is under 512 MB.
- **SC-003** Steady-state memory with a 100,000-note index open is under 64 MB.
- **SC-004** An unchanged 100,000-file freshness check completes in under 2 seconds.
- **SC-005** A usable partial result is available within 5 seconds of opening any collection.
- **SC-006** Gateway boot with all knowledge tools registered reports zero tool-policy gaps.
- **SC-007** An agent in a workspace without a given collection mounted retrieves zero results from it, in 100% of attempts across the isolation dataset.
- **SC-008** Zero links resolving outside the collection root across the DS-1 dataset, with zero reads of those targets.
- **SC-009** Rebuilding a fixture index yields identical ranked results and identical graph sets across 10 consecutive rebuilds.
- **SC-010** Renaming a note updates 100% of inbound links, body and frontmatter, across the reference collection's link distribution.
- **SC-011** Zero lost writes across 1,000 concurrent cross-process write attempts.
- **SC-012** The preview isolation tests pass in Chrome, Firefox and Safari.
- **SC-013** Every pre-existing preview classification is unchanged (zero diffs against the current classification table).
- **SC-014** Chat markdown link-handling tests remain green and unmodified.
- **SC-015** `make verify-contracts` exits zero with no drift.

---

## 16. Traceability Matrix

| Requirement | User story | BDD scenario | Test |
|---|---|---|---|
| FR-001 | US-1 | HTML page renders / Documents and media render | 2, 9 |
| FR-002 | US-1 | Source is available behind Edit | 9 |
| FR-003 | US-1 | A complete bundle loads all of its assets | 9 |
| FR-004 | US-1 | Scripts in a previewed page execute | 9 |
| FR-005 | US-2 | Cannot read the session cookie (both contexts) | 5, 10, 12 |
| FR-006 | US-2 | Cannot reach the network | 11 |
| FR-007 | US-2 | Untrusted content is visibly marked | 10 |
| FR-008 | US-2 | Types outside the allow-list are attachments | 4 |
| FR-009 | US-1 | Documents and media render natively | 3 |
| FR-010 | US-1 | (negative — NB-2) | 1 |
| FR-011 | US-3 | Private comments do not render | 6 |
| FR-012 | US-3 | A selected file is addressable by URL | 7, 8 |
| FR-013 | — (NB-4) | (regression) | Existing chat suites |
| FR-014 | US-1, US-2 | Documents and media render natively | 57 |
| FR-015 | US-2 | An HTML file named .pdf does not execute | 58 |
| FR-016 | US-2 | (build gate) | 59 |
| FR-017 | US-2 | (documentation) | — doc review |
| FR-018 | US-1 | A PDF renders in the preview pane | 57, 61 |
| FR-019 | US-1 | A complete bundle loads all of its assets | 60 |
| FR-020 | US-4 | Marker presence decides status | 13 |
| FR-021 | US-4 | Detection reads no file contents | 14 |
| FR-022 | US-4 | Creating writes only the Omnipus marker | 15 |
| FR-023 | US-4 | Creating writes only the Omnipus marker | 15 |
| FR-024 | US-4 | Identity survives relocation | 16 |
| FR-025 | US-4 | (creation location) | 15 |
| FR-030 | US-5 | (index location) | 22 |
| FR-031 | US-16 | Revoking one of two mounts | 24 |
| FR-032 | US-5 | (permissions) | 23 |
| FR-033 | US-5 | (incremental) | 25 |
| FR-034 | US-5 | (bounded memory) | 38 |
| FR-035 | US-6 | Partial results are labelled as partial | 30 |
| FR-036 | US-6 | An unknown total is not shown as a ratio | 29 |
| FR-037 | US-8 | Counts above the cap are clamped | 28 |
| FR-038 | US-11 | Rebuilding produces identical answers | 32 |
| FR-039 | US-5 | (persistence) | 25 |
| FR-040 | US-7 | Every wikilink form resolves | 17 |
| FR-041 | US-11 | Ambiguous basename resolves and is reported | 18 |
| FR-042 | US-7 | (unresolved) | 17 |
| FR-043 | US-10 | Links that escape never resolve | 19 |
| FR-044 | US-10 | Symbolic links are skipped | 20, 21 |
| FR-045 | US-11 | Indexing never calls a language model | 33 |
| FR-046 | US-11 | Rebuilding produces identical answers | 32 |
| FR-050 | US-8 | (ranked results) | 28 |
| FR-051 | US-8 | Every wikilink form resolves | 17 |
| FR-052 | US-9 | Cannot search another workspace | 26 |
| FR-053 | US-9 | Cannot address another workspace | 26 |
| FR-054 | US-8 | (bounds) | 28 |
| FR-055 | US-8 | (rate limit) | 28 |
| FR-060 | US-7 | Every wikilink form resolves | 17 |
| FR-061 | US-7 | Every wikilink form resolves | 17 |
| FR-062 | US-7 | Every wikilink form resolves | 17 |
| FR-063 | US-7 | Every wikilink form resolves | 17 |
| FR-064 | US-7 | Every wikilink form resolves | 17 |
| FR-065 | US-7 | Every wikilink form resolves | 17 |
| FR-070 | — | Booting produces no coverage gaps | 34 |
| FR-071 | — | Existing configuration is migrated | 35 |
| FR-080 | — | (contracts) | 36 |
| FR-090 | US-15 | A refused write is audited | 50, 51 |
| FR-100 | US-12 | (template creation) | 40 |
| FR-101 | US-12 | (templates reachable) | 41 |
| FR-102 | US-12 | (no execution) | 42 |
| FR-103 | US-13 | Renaming updates body and frontmatter | 43 |
| FR-104 | US-13 | An interrupted rename is detectable | 45 |
| FR-105 | US-13 | Frontmatter survives untouched | 44 |
| FR-106 | US-14 | A stale write is refused | 46 |
| FR-107 | US-14 | Mtime-preserving change still detected | 47 |
| FR-108 | US-14 | (lock bound) | 49 |
| FR-109 | US-16 | Re-mounting inside the grace period | 52 |
| FR-110 | US-16 | A moved folder surfaces a broken mount | 53 |
| FR-111 | US-16 | An evicted file is never indexed as empty | 54 |
| FR-112 | US-16 | (unaddressable reported) | — see AW-3 |
| FR-120 | US-17 | Shared lock observed | 55 |
| FR-121 | US-17 | Absent `ev` unchanged | 56 |

**ADR acceptance-criteria coverage.** Every ADR `AC-x.y` maps to a named test:

| AC | Test | AC | Test |
|---|---|---|---|
| AC-1.1 | 13 | AC-7.1 | 26 |
| AC-1.2 | 13 | AC-7.2 | 17 |
| AC-1.3 | 13 | AC-7.3 | 28 |
| AC-1.4 | 14 | AC-10.1 | 43 |
| AC-2.1 | 16 | AC-10.2 | 45 |
| AC-3.1 | 24 | AC-10.3 | 44 |
| AC-3.2 | 22 | AC-12.1 | 40 |
| AC-3.3 | 23 | AC-12.2 | 41 |
| AC-4.1 | 38 | AC-13.1 | 52 |
| AC-4.2 | 39 | AC-13.2 | 54 |
| AC-4.3 | 25 | AC-14.1 | 48 |
| AC-5.1 | 29 | AC-14.2 | 46 |
| AC-5.2 | 30 | AC-14.3 | 49 |
| AC-5.3 | 31 | AC-15.1 | 9 |
| AC-6.1 | 32 | AC-15.2 | 10 |
| AC-6.2 | 19 | AC-15.3 | 3 |
| AC-6.3 | 18 | AC-15.4 | 12 |
| AC-6.4 | 33 | AC-17.1 | 34 |
|  |  | AC-17.2 | 35 |

---

## 17. Ambiguity Warnings

Unresolved points where an implementer would otherwise guess.

| # | What is ambiguous | Likely assumption | Question to resolve |
|---|---|---|---|
| **AW-1** | Excerpt source — highlighting from the index, or re-reading the file at query time | Index highlighting, because it is nearer to hand | Which? Highlighting needs stored fields and grows the index materially at 100k notes; re-reading needs a match locator and costs query latency (ADR O-5) |
| **AW-2** | Whether attachments are indexed as metadata | Skip them entirely | Are image and PDF attachments discoverable by name? The reference 104k-file collection was roughly half images, so this changes the scale target's meaning (ADR O-6) |
| **AW-3** | How unaddressable filenames are surfaced | A line in the drift report only | Should they also appear in the Library with an explanatory state? FR-112 has no UI home yet |
| **AW-4** | Rename link-rewriting default | Automatic, matching Obsidian | Automatic, or confirm first? (ADR O-4) |
| **AW-5** | Whether one knowledge base may span several mounts | One collection = one mounted folder | Confirm (ADR O-7) |
| **AW-6** | Where the drift check is invoked from | A button in the Library | REST, SPA, CLI, or all three? A CLI check cannot open the index while the gateway holds it (ADR O-8) |
| **AW-7** | Body-size cap for a single note | 1 MB | What is the cap, and is an oversized note truncated-and-reported or skipped-and-reported? |
| **AW-8** | Grace period before index deletion | 7 days, as the ADR states | Confirm; it trades disk against rebuild cost |
| **AW-9** | Whether the reading rail appears for markdown outside a knowledge base | No — knowledge bases only | Should an ordinary `.md` file get an outline too? Cheap, and arguably expected |

---

## 18. Evaluation Scenarios (Holdout)

**Not for use during development.** These are for verifying the finished
implementation from the outside. They must not be referenced in the traceability
matrix or written as automated tests during the build.

### H-1 (happy) — Bring your own collection
Mount the real Elicify vault. Without reading any documentation, find a note you
know exists using a phrase from its middle. Confirm the result is the right note and
appeared quickly. Open it; confirm it reads the way it does in Obsidian.

### H-2 (happy) — Follow the structure
From that note, use the linked-mentions list to reach a note you did not know linked
to it. Confirm the link was genuinely there.

### H-3 (happy) — Agent recall
Ask an agent a question answerable only from the collection. Confirm it cites a real
note path that exists, and that the cited note actually contains the answer.

### H-4 (error) — The interrupted rename
Rename a heavily-linked note and kill Omnipus mid-operation. Restart. Confirm you are
told something is incomplete, that completing it fixes every link, and that no note
was left pointing at a name that no longer exists.

### H-5 (error) — The double writer
Open a note in Obsidian and edit it. At the same time have an agent append to it.
Confirm that whichever loses is told clearly, and that neither version is lost
without warning.

### H-6 (edge) — The hostile page
Place an HTML file that tries to read cookies, call the API, phone out to an
external host, and draw a convincing Omnipus login form. Open it both in the pane
and as its own tab. Confirm the first three fail and that the fourth is obviously
framed as untrusted content.

### H-7 (edge) — The awkward collection
Point Omnipus at a collection containing a note with a colon in its name, a symlink
out of the tree, a 50 MB note, two notes sharing a basename, and a note whose
content the cloud provider has evicted. Confirm every one is either handled or
reported — and that nothing is silently missing from search.

---

## 19. Assumptions

- **A-1** The operator's collection is on a POSIX filesystem. Windows gets in-process protection only (ADR residual 3).
- **A-2** The reference collection's shape (748 notes, 87% carrying frontmatter links) is representative of structure, not of scale.
- **A-3** Existing chat markdown behaviour is correct and must not change.
- **A-4** `bleve` scorch remains suitable at the stated targets; if benchmarks disprove this, the engine decision reopens (ADR A2).
- **A-5** The 100,000-note fixture is synthesised rather than real, and its link density is modelled on the reference collection.

---

## 20. Clarifications

### 2026-08-22
- Spec scope: **all four stages in full detail** (founder decision).
- Marker name: `.omnipus-vault/` — ADR O-3 closed.
- Discovered during grounding: the Go and TypeScript link sanitisers disagree on relative links (§2.4). Recorded; **not** resolved here.
- `classifyLibraryEntry` is HIGH-risk per impact analysis; a dedicated regression guard is test 1 and a release gate (SC-013).
