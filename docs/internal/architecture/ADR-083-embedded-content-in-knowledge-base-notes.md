# ADR-083 — Embedded content inside knowledge-base notes

- **Status:** Proposed — **revision 5**. Rev 2 followed an adversarial grill review of rev 1; rev 3 followed the grill review of the *implementation spec*, which found this ADR to be the source of several of the spec's defects (including one outright self-contradiction); rev 4 follows the **second** spec review (5 CRITICAL, 12 MAJOR, 8 MINOR, 4 OBSERVATION; BLOCK) and three further founder rulings; **rev 5 records one further founder ruling, N8, and retires a capability rev 4 had scheduled.** Four decisions (D-A…D-D in §2.6) plus Q1–Q9 (§9) are **founder-ratified** and settled. Four rulings from rev 3 — **N1 nested embeds are out, N2 the version token is required, N3 print support is dropped, N4 an anonymous-actor write under auth bypass is allowed** — are in §2.8. Three further rulings — **N5 the two save doors share the EXISTING knowledge version token (closing A-11), N6 agents DO see the raw path plus its reason, redaction rejected as theatre, N7 record editing stays in this ADR** — are in §14.1 and are equally settled. **N8 — an embedded diagram FILE (`![[chart.mmd]]`) is out of scope and will not be built; the ` ```mermaid ` FENCE is a different mechanism and is untouched** — is in §15, and it **supersedes §5's and §6's revision-4 text**, which routed a `.mmd` embed to the mermaid renderer in step 6.
- **Deciders:** Daniel Piatkowski (founder — ratified the mount budget, the unresolved-marker rule, the external-content boundary, the interactivity split, N1–N4, and **N8**); architect (resolver shape, component reuse, agent surface, write-path contract, sequencing)
- **Date:** 2026-09-09 (rev 1), revised 2026-09-09 (rev 2), revised 2026-09-09 (rev 3), revised 2026-09-09 (rev 4), revised 2026-09-11 (rev 5 — founder ruling N8, §15)
- **Branch / commit this was verified against:** `integrate/library-improvements-v0.1.1`, HEAD `def10b90e`, worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate`. Every repo-relative path below is relative to that worktree root.
- **Number verification:** `ADR-083` is free. Checked three ways: the directory listing tops out at `ADR-082`; `git log --all --diff-filter=A --name-only -- 'docs/internal/architecture/ADR-08*'` returns only ADR-080, ADR-081 and ADR-082 across every ref, past and present; and no `ADR-083` path exists on any branch. ADR-082's own header records why this check is not ceremony — ADR-081 was drafted as "ADR-069" and had to be renumbered after a merge.
- **Closes:** [`defect-list-wikilink-rendering-2026-09-08.md`](../defect-list-wikilink-rendering-2026-09-08.md) **WL-5** (a base view cannot be embedded in a note), which that document correctly classifies as a missing capability rather than a bad import.
- **Relates:** [ADR-067](ADR-067-omnipus-knowledge-base-and-render-first-preview.md) (the knowledge base, the render-first preview surface, and §10.3/§10.4/§10.7's isolation policies), [ADR-068](ADR-068-vault-records-typed-record-layer.md) (the typed record layer, `knowledge_edit`'s consolidation, FR-070b/c, FR-106), [ADR-082](ADR-082-rename-vault-to-knowledge-base.md) (naming)
- **Reviews that produced these revisions:** rev 2 ← [`ADR-083-…-review.md`](ADR-083-embedded-content-in-knowledge-base-notes-review.md) (28 findings, BLOCK); rev 3 ← [`adr-083-embedded-content-spec-review.md`](../specs/adr-083-embedded-content-spec-review.md) (7 CRITICAL, 13 MAJOR, 9 MINOR, 3 OBSERVATION; BLOCK). rev 4 ← [`adr-083-embedded-content-spec-review-round2.md`](../specs/adr-083-embedded-content-spec-review-round2.md) (5 CRITICAL, 12 MAJOR, 8 MINOR, 4 OBSERVATION; BLOCK). §12 records rev 2's dispositions; **§13 records rev 3's**, including the findings that were resolved by *deleting a feature* rather than by building one; **§14 records rev 4's**, plus founder rulings N5–N7.
- **Sources read in full before drafting:** [`obsidian-embed-capability-reference.md`](../obsidian-embed-capability-reference.md) (the researched Obsidian surface, its nine traps, the DOM contract, and its own honest open items), WL-5, and the code files listed in §10.
- **Constraints in force:** Hard Constraint **#6** (tool policy is exactly two layers; no wildcards for static builtins), Hard Constraint **#8** (contract-first wire formats), Hard Constraint **#7** (release responsibility)

---

## 1. What this is about, in one paragraph

The founder's operating view is a dashboard: a note that contains nothing of its own and
assembles itself out of fifteen or more live data views. `Founder Cockpit.md` states the rule
in its own text — *"assembled entirely from Base-view embeds — never hand-typed data."* Today
those dashboards render as a list of links, not as data. This ADR decides how a note displays
things that live somewhere else: a saved data view, an image, a PDF, another note, a sound
file, a video. It decides what happens when the thing is missing, how many of them may be on
one page, whether you can change anything from inside one, and how an agent writes one without
being taught the notation.

---

## 2. Context

### 2.1 What the founder's own knowledge base actually contains

Measured across 784 migrated notes. These counts rank the work; they do not define the
capability surface, which is Obsidian's.

| Embedded thing | Uses | Notes |
|---|---:|---|
| A named view of a data table (`![[X.base#View]]`) | **75** | 100% name a specific view. An entire `07-Dashboards/` folder depends on it. |
| Mermaid diagrams | 163 | **Already works** — a fenced code block, a different mechanism entirely |
| Images | 12 | Already works |
| SVG images | 4 | Already works — see §2.5 |
| PDF | 1 | Does not render |
| Everything else (audio, video, note transclusion, search fences, external video) | 0 | Does not render |

### 2.2 What happens today — corrected against the code

The brief for this ADR stated that an embed of a PDF, MP3 or MP4 renders as a broken image.
**That is not what the code does**, and the difference changes the fix, so it is recorded here
rather than passed over.

In `src/components/library/preview/knowledgeMarkdown.tsx`, `remarkKbWikilinks` converts an
embed to an image node **only when the target's file extension is in `IMAGE_EXTENSIONS`**.
Everything else — a PDF, a sound file, a video, a `.base` data file — falls past that branch
and becomes an ordinary wikilink node. The `a` slot then renders it through `CollectionLink`,
which appends a small grey badge reading, literally, **"embed shown as a link"**.

So the present behaviour is *honest but useless*: it tells the reader that something was meant
to be shown and was not, instead of pretending. That is a deliberate, tested treatment and this
ADR keeps it — it becomes the fallback when a kind genuinely cannot be rendered, rather than
the answer for every kind.

Two consequences of getting this right:

- The fix is **not** "remove a wrong return". It is "add branches for the other kinds and leave
  the fallback in place".
- There is no broken-image bug to point at as evidence, so **the only way to know this feature
  regressed is a test that asserts each kind mounts its renderer.** A visual check cannot tell
  a deliberate fallback from a failure to route. §11's per-step exit criteria make that a gate
  rather than an aspiration.

### 2.3 The half of the machinery that already exists — and the extension point rev 1 missed

`pkg/knowledge/links.go` extracts every link in a note, including embeds, for **any** target
type — not just markdown. It records `Embed: true` for the `![[…]]` form, and `ResolvedLink`
reports the resolved path, the failure reason, whether the name was ambiguous, and which
candidates it matched.

**Correction to revision 1.** Rev 1 claimed *"for a `#Heading` fragment — whether that heading
was actually found in the target. All of this is already on the wire."* **The second sentence
was false and the decisions built on it were unimplementable.** Measured on `def10b90e`:

| Field | Exists in Go | On the wire (`KnowledgeGraphEdge.yaml`) |
|---|---|---|
| `Embed` | yes | yes (`embed`) |
| resolved target | yes (`To`) | yes (`to_path`) |
| `Heading` (the fragment text) | yes | yes (`heading`) |
| `Ambiguous`, `Candidates` | yes | yes |
| link text as written | yes (`Target`) | yes (`link_text`) |
| **`HeadingFound`** | **yes** (`ResolvedLink.HeadingFound`) | **NO** |
| **`BlockID`** | **yes** (`ResolvedLink.BlockID`) | **NO** |
| **why a link is `unresolved`** | yes (`ResolvedLink.Reason`, `ResolveState`) | **NO — and worse, deliberately collapsed** |

`pkg/gateway/rest_knowledge.go::knowledgeEdge` sets exactly nine fields and stops; neither
`heading_found` nor a block property exists in the schema. **D4's "file found, heading not"
case and D5's block transclusion therefore require Constraint #8 contract work that rev 1 never
sequenced.** §6 now carries it as explicit line items.

**Correction added in revision 3 — the third row above is new, and it is the one that breaks a
security-relevant message.** `KnowledgeGraphEdge.yaml` has **no `reason` property at all**, it is
`additionalProperties: false`, and the description of its `resolution` enum collapses two
different facts into one value, on purpose:

> `"unresolved"` means no target matched, **or** the target lay outside the collection root — in
> which case the target was NOT read (FR-043).

So on today's wire the reader **cannot tell "no such file" from "refused for leaving the knowledge
base"**. D6's read-time containment rule — *the marker says `this embed points outside the
knowledge base` and names nothing further* — is therefore **unimplementable as rev 2 wrote it**:
the reader has only one value for both cases and will print the missing-file sentence for a
containment refusal. That is the wrong message in the direction that matters, because it tells a
reader the escaping target does not exist rather than that it was refused. §6 step 1c now carries
the fix as contract work (a fourth field, `unresolved_reason`), not as an implementer's detail.

**A second correction, on `heading_found`'s meaning.** `HeadingFound` has exactly one assignment
site (`pkg/knowledge/graph.go`, inside `BuildLinkGraph`'s resolution loop), guarded by
`res.State == ResolveResolved && res.Heading != ""`, and it looks the heading up in `g.headings`,
which is populated **only for markdown files**. Two consequences a reader must be told about
rather than left to discover:

- For a **`.base`** target — every one of the founder's 75 dashboard embeds — `g.headings[to]` is
  nil and `heading_found` is **false**. A reader that keys the "no such heading" marker off that
  field alone renders it across the entire dashboard.
- For a **block** reference (`![[Note#^abc]]`), the parser fills `BlockID` and leaves `Heading`
  empty, so the guard never fires and `heading_found` stays false. Same false marker.

`heading_found` is meaningful **only when the target is markdown and a heading fragment was
written**. D4 states the rule; §11.2 names the test that has to cover both false-marker paths,
because today's test exercises markdown headings only and passes while both ship.

**The extension point already exists, and rev 1 did not name it.** `KnowledgeNoteView.tsx`
already builds a `resolveEmbedUrl` memo that looks up `graph.edges.find(e => e.embed === true
&& (e.link_text === target || basenameOf(e.to_path) === target))` and returns a download URL —
this is the image path §2.2 describes, and it is *already* graph-backed. **D1 is an extension of
`resolveEmbedUrl`, not a new function beside it.** That matters twice over: it is less new code
than rev 1 implied, and its existing matching key is the one M2 shows to be wrong, so the bug
being fixed is live today for images as well as prospective for everything else.

Two further pieces of the note reader that D1 must reuse rather than rebuild, both already in
`KnowledgeNoteView.tsx`: `collectionRoot` (derived by probing each ancestor directory's
`KnowledgeBaseInfo` for a matching `collection_id`) and the `toWorkspacePath` memo it feeds. See
D1 stage 4.

### 2.4 The renderers are *mostly* uniform, and all of them are pane-shaped

**Correction to revision 1.** Rev 1 claimed *"every preview renderer takes the same prop pair,
`{ workspaceId, entry }`"*. That is true of three of ten and false of the rest. Measured:

| Renderer | File | Actual props |
|---|---|---|
| `LibraryImagePreview` | `preview/LibraryImagePreview.tsx` | `{ workspaceId, entry }` |
| `LibraryVideoPreview` | `preview/LibraryVideoPreview.tsx` | `{ workspaceId, entry }` |
| `LibraryPdfPreview` | `preview/LibraryPdfPreview.tsx` | `{ workspaceId, entry }` |
| `LibraryMarkdownPreview` | `preview/LibraryMarkdownPreview.tsx` | `+ content, onSaved?, onOpenNote?` |
| `LibraryCodePreview` | `preview/LibraryCodePreview.tsx` | `+ content, onSaved?` |
| `LibraryMermaidPreview` | `preview/LibraryMermaidPreview.tsx` | `+ content, onSaved?` |
| `LibraryTextPreview` | `preview/LibraryTextPreview.tsx` | `+ content, renderView, editorFilename, onSaved?` |
| `BasePreview` | `preview/BasePreview.tsx` | `+ loadContent, loadBaseViews, loadViewResult, onOpenNote?` |
| `LibraryDownloadCard` | `preview/LibraryDownloadCard.tsx` | `{ entry, reason, onDownload }` — **no `workspaceId`** |
| `LibraryAudioPreview`, `LibraryHtmlBody`, `LibraryHtmlFrame`, `LibraryTextBody` | **module-private functions inside `src/components/library/LibraryPreviewPane.tsx`** — not exported, no file of their own | various |

Note the pane's real path: `src/components/library/LibraryPreviewPane.tsx`, **not** under
`preview/`. Two consequences rev 1 missed:

- **Audio and video reuse is not free.** §6 step 6 needs an extraction step first, because
  `LibraryAudioPreview` has no module to import from. §6 now contains it.
- **`LibraryDownloadCard` cannot be typed by the widening D2(a) proposes**, and is reachable from
  an embed via the `other`, `binary` and `too_large` paths. D2(a) and §5 now resolve this.

`libraryPreviewKind.ts` classifies a file into ten kinds — image, video, html, pdf, audio, base,
markdown, mermaid, text, other. That is the dispatch table this feature needs, and it exists.
§5 now gives every one of the ten an explicit disposition.

The obstacle is shape. Each renderer assumes it owns a full pane. Counting occurrences of
`h-full`, `inset-0`, `absolute` and `flex-1`: `LibraryImagePreview` 2, `BasePreview` 5,
`LibraryPdfPreview` 13. Those cannot be corrected from outside by a wrapper — a wrapper with a
fixed height is exactly what `h-full` and `absolute inset-0` resolve *against*, and a `flex-1`
child of a non-flex parent collapses. D2(b) decides how to fix it from inside.

Separately, `LibraryPdfPreview` creates a background worker **per document load**, inside the
load effect (`new Worker(…)` then `pdfjs.PDFWorker.create({ name, port })`). One PDF in a pane is
one worker; a page with several PDF embeds is several. D2(c) revisits this, corrected.

### 2.5 Three things that already work and need no work

- **Mathematics.** `remark-math` and `rehype-katex` are both wired into the knowledge-base
  markdown pipeline (`kbMarkdownBase.tsx`), and `katex` ships in `package.json`. `$x$` and
  `$$x$$` render today. The capability reference marked this "verify"; it is verified.
- **SVG.** `.svg` maps to `image/svg+xml`, sits on the inline allow-list
  (`pkg/gateway/library_mime.go`), and classifies as `image`, never as `html` — so it is drawn
  in an `<img>`, where the browser runs none of its scripts. The SPA's own policy allows
  same-origin images. Once the resolver routes it, an SVG embed works. **Do not** "improve"
  this by injecting the SVG inline so it scales; that turns the reader into a script host, and
  `libraryPreviewKind.ts`'s header already warns against exactly this.
- **Raw HTML in a note.** The knowledge-base pipeline does **not** include `rehype-raw`
  (`KB_REHYPE_PLUGINS` is `[rehypeKatex, rehypePhosphorEmoji]`). A hand-written `<iframe>` in a
  note is therefore not rendered as HTML at all.

> **The "iframe row is closed" claim, corrected.** Rev 1 said the capability table's iframe row
> is *"closed by construction"*. That was true only of hand-written markup, and rev 1's own D1
> reopened it by the other door: `![[page.html]]` classifies as `html`, and the pane routes
> `html` to `LibraryHtmlBody` → `LibraryHtmlFrame`, a token-minted (`previewTokenRequestFor`),
> sandboxed iframe carrying **agent-generated HTML**. Mounting that inline in the note reader
> would put an isolation boundary inside a reading surface that is not one. §5 now closes it
> deliberately: **`html` falls back to the §2.2 link treatment and is never framed inline.**

### 2.6 The four ratified decisions

Recorded as settled. The rest of this ADR is built to satisfy them.

| | Decision |
|---|---|
| **D-A** | **Mount budget: lazy on scroll, no hard cap.** Embeds start work when they scroll into view and stop when they are far outside it. A forty-embed dashboard must work; only the visible part should cost anything. |
| **D-B** | **A missing target is visible, and is reported to agents.** Never an empty box — an empty box reads as "there is no data", which is a different and false statement. The unresolved state must also reach an agent reading the note, so an agent looking at a dashboard knows part of it is broken. |
| **D-C** | **External content: YouTube only, by explicit allow-list.** No other external video, no external images. Images stay same-origin; only the frame permission opens, and only to the allow-listed host. |
| **D-D** | **Embeds are interactive, with one sharp line through the middle.** Links inside views work in embeds, and record fields are editable — a `status` enum as a dropdown, a text field inline — in both the ordinary view surface and inside a dashboard embed. **But:** editing a *record's* field persists to that record; changing a *view's* filter or sort stays local to that one embed and is never written back to the shared `.base`. Obsidian writes it back, so filtering one embed silently re-filters every other embed of the same view, and users resort to CSS to hide the toolbar. We do not copy that. |

### 2.7 One live defect this ADR inherits, stated up front

The SPA's existing note-save path is **unversioned and unaudited today**. Full treatment in
§4.1; it is named here because it changes the risk calculus of the whole document, and rev 1
described it as a prospective hazard rather than a current defect. §9 Q7 asks the founder
whether closing it is a precondition of this work.

### 2.8 Four further ratified decisions (revision 3)

Recorded as settled, alongside D-A…D-D. Each resolves one or more findings from the spec review,
and **three of the four resolve them by removing a promise rather than by building a mechanism.**
That direction is deliberate: a promise the product cannot keep is worse than an absent feature,
because only the absent feature is visible.

| | Decision | What it resolves |
|---|---|---|
| **N1** | **Nested embeds are OUT. One level only.** A transcluded note's own embeds render as **links**, never as nested content. There is therefore no second level of transclusion, no cycle, no depth, and **no cycle-detection apparatus** — the mechanism, its tests and its success criterion are deleted rather than moved. D5 is rewritten around this. | The review's C1, by removing the feature. Rev 2 was internally contradictory here: D5 said nested embeds were out and then, two bullets later, described "a five-deep nest [as] five sequential fetches". The second half is deleted. |
| **N2** | **The version token is REQUIRED on both Library whole-file save doors, not optional.** A save without one is rejected. No caller is exempt — including the PDF-annotation save, whose read path must therefore be given a way to obtain a token (§4.2a). | Spec ambiguity A-1. It makes the review's C6 *harder*, not easier, and §4.2a specifies the fix rather than leaving it to be discovered. |
| **N3** | **Print support is DROPPED from this work entirely.** The `beforeprint` guarantee is removed from this ADR, and every print requirement, test and scenario is removed from the spec. Printing a dashboard today prints **only what has already mounted**. Making printing complete is separate, unscheduled work. | The review's A-9, by deleting the promise rather than weakening it. See D3. |
| **N4** | **An inline record edit under authentication bypass IS ALLOWED, and is audited as an anonymous actor.** It is **not** refused with 503 — that was the architect's recommendation and it is overruled. The actor value is the literal string **`anonymous`** (§4.2b). | Spec ambiguity A-6's second half. The accepted consequence is recorded in §7.2 and is not soft-pedalled: **an `anonymous` entry cannot answer "who".** |

---

## 3. Decisions

### D1 — The resolver: parse, ask the graph, classify, convert

**What it means.** One function turns the text a person typed inside `![[ ]]` into "here is the
thing, and here is what can draw it" — or into an honest statement of what it could not
determine. It extends `KnowledgeNoteView.tsx`'s existing `resolveEmbedUrl` (§2.3) rather than
sitting beside it, so there is one embed-resolution path and not two.

**Four stages, in order.**

#### Stage 0 — recognise an external allow-listed URL *before* the graph is consulted

A markdown-link embed whose destination has a URL scheme (`![](https://www.youtube-nocookie.com/…)`)
**never enters the graph model at all.** `pkg/knowledge/links.go::parseMarkdownLink` returns no
`Link` for an external destination — its own comment says *"A scheme URL leaves the collection …
neither is an edge in the note graph"* — so there is no edge to find, ever, and asking the graph
about one and reporting the miss would state a falsehood.

Order: external-allow-list check (D9) first; wikilink/graph path second. Rev 1 implied this in
D9 and did not state it in D1, which is where an implementer works.

#### Stage 1 — parse

`parseWikilink` already separates target, `#fragment` and `|payload`, and the `a` slot already
receives them. One correction is needed for the embed form: today the `|` payload is always read
as a display alias. In an embed it is usually a **size**. The rule:

> For the embed form only, a `|` payload matching `^\d+(x\d+)?$` is a size; anything else stays
> display text. Fragment is read first, size second.

That order is the one the capability reference captured live from Obsidian, and it is the
reason `alt` text there is inconsistent (`"file > fragment"` without a size, `"file#fragment"`
with one). Sizing applies to images only. It parses on audio and video and resizes nothing —
that is a documented Obsidian no-op (trap 3), and copying a control that does nothing is worse
than not offering it, so **an embed size on a non-image is refused at write time** (see D6) and
ignored at render time.

#### Stage 2 — ask the link graph, with an exhaustive state model

The note reader already loads this note's outbound links: a `kind: 'links'` graph query keyed on
`['library', 'knowledge', 'graph', 'links', workspaceId, collectionId, collectionNotePath]`.

**The matching key** — rev 1 said only *"the edge whose `link_text` matches the written target
and whose `embed` is true"*, which is ambiguous on the founder's own dashboards. `link_text` is
`ResolvedLink.Target`, and `links.go`'s wikilink parser **strips the `#fragment` before assigning
`Target`**. So `![[Tasks.base#Needs Daniel]]` and `![[Tasks.base#Awaiting founder]]` produce two
edges with identical `link_text`, identical `to_path` and identical `embed`, separable only by
`heading`. Several named views from one `.base` is the *expected* shape here, not an edge case.

> **The key is:** `embed === true` **and** `link_text === target` **and** `heading === fragment`
> (both absent counts as equal) **and** `block === blockId` (M1's new wire field). `from_path`
> is not in the key **because the query is already scoped to this note** — `kind: 'links'`
> returns only `g.Links(notePath)`. That implicit scoping is load-bearing and breaks for nested
> transclusion; D5 states what a transcluded note does instead.
>
> **Zero matches** → `indeterminate` (below), never `unresolved`.
> **More than one match** → render the first in response order **and** report the ambiguity
> alongside, exactly as `KnowledgeGraphEdge.yaml`'s own description requires for name ambiguity:
> *"resolving it is not a licence to stay quiet about it."*

**Four states, not three.** Rev 1's table had three and presented them as total. They are not,
and the missing state prints a false sentence about a file that exists.

| Condition | State | What the reader sees | What an agent is told |
|---|---|---|---|
| Edge matched, `resolution` ≠ `unresolved` | **resolved** | the mounted renderer | the embed, with its target |
| Edge matched, `resolution` = `unresolved` | **unresolved** | the D4 marker naming target and reason | `(unresolved embed)` with the reason |
| Graph query still in flight | **loading** | a reserved placeholder, never a marker | nothing — the agent path reads the file, not the query |
| Graph query **failed** | **graph_unavailable** | **one page-level error** with the failure and a Retry — not one per embed | the read tool does not consult the SPA query; unaffected |
| Graph loaded, **no matching edge** | **indeterminate** | a *distinct* marker: "this embed could not be checked against the knowledge base" plus the reason it could not be | `(embed, not checked)` with the reason — **never** "no such file" |

**Why `indeterminate` must exist, with each producer's measured status. Revision 3 CORRECTS this
list: rev 2 named the wrong producers, and the correction changes where the guard has to go.**

Rev 2 asserted that "a note skipped for any of these contributes no edges while the graph reports
success". That conflates two different notes: the note *containing* the embed, and the note the
embed *points at*. Measured on `def10b90e`, `BuildLinkGraph` builds its resolution index from the
walk's file list —

```go
index: NewNoteIndex(walk.Files),
```

— and the two kinds of skip land on opposite sides of that line:

- **A WALK-level skip removes the target from the index.** `WalkContained` reports `symlink`,
  `outside_root`, `irregular`, and `unreadable` *for a directory it could not list* (which takes
  every file beneath it with it). None of those files enters `walk.Files`, so a link **to** one of
  them **resolves as `unresolved`** — a *matching edge*, not zero edges. Under the table above
  that is the `unresolved` state, and the reader prints *"Nothing in this knowledge base is named
  …"* **about a file the reader can open in the next pane.** This is the exact falsehood the whole
  five-state model exists to prevent, arriving through the one door the model does not watch.
- **A SCAN-level skip leaves the target IN the index.** `unreadable` for an individual *file*
  (open, stat, or the FR-111 eviction check failing) and `outside_root` from the loop's second
  `ResolveContained` are appended to `g.skipped` **after** `walk.Files` was captured. The target
  therefore stays in the index and the link **resolves normally** — so this produces neither
  `unresolved` nor `indeterminate`, and the embed will try to render a file the indexer could not
  read.

> **Therefore the cross-check belongs on the `unresolved` state, not only on the zero-match
> state.** Before rendering "nothing is named X", the resolver **MUST** consult
> `KnowledgeGraphResponse.skipped` — which is whole-collection and populated for every kind — for
> an entry naming this embed's target. If one is found, it renders `indeterminate` with the skip
> reason instead.
>
> **The matching rule, stated so two engineers cannot implement two things.** For an `unresolved`
> edge, `to_path` is *"the normalised link text"* (`KnowledgeGraphEdge.yaml`), **not** a real path,
> while `KnowledgeGraphSkip.path` is a real collection-relative path. The comparison is therefore:
> a skip entry matches when **any** of three clauses holds. All three run on normalised,
> `/`-separated, cleaned collection-relative strings.
>
> 1. its `path` equals `to_path`;
> 2. the basename of its `path` (with and without a markdown extension) equals `to_path`. Basename
>    matching is deliberately included because one dominant case — a symlinked note referenced by
>    name — has no path in the link text at all;
> 3. **(revision 4, C1(r2)) its `path` names a DIRECTORY that is a proper ancestor of `to_path`** —
>    `to_path` begins with the skip's `path` followed by `/`. **Clauses 1 and 2 alone leave the
>    largest walk-level case broken**, and it is the very case the bullet above this box describes:
>    an unreadable directory is recorded under **its own** path (`contain.go`'s `ReadDir` error
>    branch, `RelPath: cur.rel`) and its files never enter `walk.Files` at all, so for a link to
>    `notes/private/plan.md` the only skip in the answer says `notes/private` — and neither
>    `notes/private` = `notes/private/plan.md` nor `private` = `plan`. Revision 3 verified this
>    refinement in §13's C2 row and then shipped a rule that could not express it.
>
> **Two boundaries on clause 3.** It matches only on a full path-segment boundary, so a skip naming
> `notes/priv` never suppresses a target under `notes/private/`; and a **basename** match is never
> treated as a directory match. **And one deliberate non-producer:** `contain.go`'s `mode.IsDir()`
> branch skips `scanSkippedDirNames` (`.obsidian`, `.omnipus-vault`, `.git`, `.trash`) with
> `continue` and appends **nothing** to `Skipped`, with an in-place comment stating the omission is
> deliberate — *"Skipped means content this walk could not address … Tool state is not content."*
> A link into one of those correctly reports absence, and the fix for clause 3 is **not** to add
> those names to the skip list.
>
> A match is a *reason to stop claiming absence*, not a claim of identity, which is why the looser
> comparison is the safe direction here.
>
> **This cross-check is owed on BOTH surfaces (revision 4, C2(r2) — D-B requires it).** Revision 3
> specified it against the graph answer the SPA fetches, which is the reader only. `knowledge_read`
> never consults that answer: it projects `ResolvedLink` into `ReadLink` and `renderReadLinks` prints
> `"  %s (unresolved) %s"`, so a walk-skipped target reached an **agent** as plainly unresolved, with
> no reason, and would be summarised as missing — on the surface whose output gets pasted into a
> report. The agent projection must consult the same skip set under the same three clauses. See the
> spec's EMB-021a.

The genuinely live producers of the zero-match `indeterminate` state are therefore:

1. **A key mismatch — live today.** Any disagreement between the SPA's written-target string and
   `link_text` (normalisation, the fragment split above, an alias) yields zero matches on a
   loaded graph. This is not hypothetical: it is the failure mode M2 identifies in the *existing*
   `resolveEmbedUrl`. Its reason is *"no reason available"*, so **in practice the zero-match
   marker will usually have no reason to give** — which is honest and must not be dressed up.
2. **An out-of-scope collection — live today, and newly named in revision 3.**
   `rest_knowledge.go` answers a graph request for a collection outside the caller's scope with
   **200 and an empty body** (`jsonOK(w, resp)` before the root is opened), deliberately, so that
   a 403 does not confirm the collection exists. Every embed on that page then sees a loaded graph
   with zero edges and zero skips. That is N markers saying "could not be checked, no reason
   available", where **one page-level statement is the right answer** — see D4.
3. **A truncated graph — NOT reachable today, and stated as such.** `KnowledgeGraphResponse`
   carries required `truncated` plus `hop_limit_applied` / `node_limit_applied` (FR-054), but
   `rest_knowledge.go`'s handler sets `resp.Truncated` **only in the `neighbourhood` branch**;
   the `links` branch never clamps. So on `def10b90e` a `links` query cannot report truncation.
   The client must still honour the field — it is required on the response, the handler is the
   only thing keeping it false, and a client that ignores a required honesty flag is one commit
   away from the defect above. **[Measured]**

**`nodes[].exists` is part of the resolution procedure, not a detail below it.** Rev 2 quoted only
the edge match and never mentioned the node check. Today's `resolveEmbedUrl` does **not** stop at
the edge:

```ts
const node = graph.nodes.find((n) => n.path === edge.to_path)
if (node && node.exists === false) return undefined
```

`KnowledgeGraphNode.exists` is a **required** field whose own description says *"False for the
target of an unresolved link. The client MUST mark such a node visibly and MUST NOT navigate on
click."* An implementer who extends the edge lookup and drops the node check loses a live guard —
and, together with the skip cross-check above, loses the only place today's code refuses to hand
out a download URL for a target that does not exist.

> **The rule: `resolution` and `exists` must agree, and a disagreement is itself an
> `indeterminate` signal.** `resolution !== 'unresolved'` with `exists === false` (or the reverse)
> means the two halves of one response contradict each other; the reader has no basis to claim
> either, so it says it could not check. A node absent from `nodes[]` entirely is not a
> disagreement and is not a signal.

`loading` must not be drawn as `unresolved`, and `indeterminate` must not be drawn as
`unresolved` either. `knowledgeMarkdown.tsx` already argues the first at length for links, and it
is more expensive for an embed: telling a reader their dashboard is broken while the data is
still arriving is a false alarm they will act on. The second is worse still, because it is not
transient — it is a permanent false statement, reported to agents, about a file the reader can
open in the next pane.

#### Stage 3 — classify

Run the resolved path's filename through the existing `classifyLibraryEntry`. Its ten kinds are
the dispatch table, **with §5's disposition for each — including two that do not mount a
renderer at all** (`html` and `other` fall back to the §2.2 link treatment; see §5). One kind is
added that a *file* classifier cannot produce, because it is a property of the target being
markdown **and** the embed being a transclusion: **`note`** (D5).

#### Stage 4 — convert the path coordinate system

`KnowledgeGraphEdge.to_path` is **collection-relative**. Every renderer's `entry.path`, every
Library address, and `GET /library/{id}/knowledge/base-views`'s `path` query parameter are
**workspace-relative**. Passing `to_path` straight through is wrong for every collection not
mounted at the workspace root: it 404s, or worse resolves to a different file of the same name
nearer the root.

> The resolver converts with `collectionPathToWorkspacePath(collectionRoot, toPath)` from
> `src/components/library/knowledge/KnowledgeBacklinks.tsx`, whose own header states the failure
> being avoided. The collection root is **not** re-derived: `KnowledgeNoteView.tsx` already
> computes `collectionRoot` (ancestor `KnowledgeBaseInfo` probe) and already wraps it as a
> `toWorkspacePath` memo. The resolver takes that memo as an argument. The converted path is what
> populates `LibraryFileRef.path`.

**What the resolver returns.** Not a `LibraryEntry`. That type requires `size` and
`modified_at`, which a graph edge does not carry, and inventing them would be fabricating data
this codebase treats as a cardinal error. It returns a **`LibraryFileRef`** — see D2(a).

### D2 — Reuse the renderers by widening them, not by copying them

**What it means.** The same component draws a PDF whether it fills the screen or sits in the
middle of a note. There is exactly one PDF renderer, one image renderer, one base-view
renderer, forever.

This is not a preference. `knowledgeMarkdown.tsx`'s own header records what happened the last
time: the markdown parser, plugin stack and element renderers *"were hand-copied once and
drifted three times."* A second, inline copy of `BasePreview` would be that mistake, again, on
the surface the founder actually reads.

#### (a) Widen the prop — for the eight renderers it fits, and not the ninth

Introduce a client-side type

```
LibraryFileRef = Pick<LibraryEntry, 'name' | 'path'>
```

and change the prop from `entry: LibraryEntry` to `entry: LibraryFileRef` on the renderers that
read only those fields.

> **Correction to revision 2 (review finding C4).** Rev 2 wrote this type as
> `Pick<LibraryEntry, 'name' | 'path' | 'mime' | 'is_text_editable'>` and justified excluding
> `size`/`modified_at` on the grounds that the resolver *"genuinely cannot supply"* them and
> inventing them is the cardinal error. **The identical argument kills the two fields it kept, and
> rev 2 did not notice.** Both `mime` and `is_text_editable` are **server-sniffed** values on
> `LibraryEntry` (`is_text_editable` is a *required* property of that schema); neither is on
> `KnowledgeGraphEdge`; and there is **no single-entry GET** in the Library API — the operations
> are `listLibraryWorkspaces`, `listLibraryEntries`, `getLibraryContent`, `downloadLibraryFile`,
> `deleteLibraryEntry`, `putLibraryContent`, `putLibraryContentBinary`, `uploadLibraryFiles`,
> `createLibraryDirectory`, `renameLibraryEntry`, `moveLibraryEntry`, `copyLibraryEntry`,
> `mintLibraryPreviewToken`, `getLibraryInlineDisposition`. There is no `getLibraryEntry`. So the
> resolver cannot obtain either field without a directory listing per embed target's parent.
>
> **This matters because the classifier reads both.** `classifyLibraryEntry`'s parameter type is
> `ClassifiableEntry { name, mime?, is_text_editable }`, and its body reads
> `(entry.mime ?? '').toLowerCase()` and `if (entry.is_text_editable) return 'text'`. A ref whose
> `is_text_editable` was invented as `false` classifies **differently from the pane** — which
> breaks the one property D2 exists to guarantee, silently, *upstream* of the renderer where no
> cross-variant test can see it. `npm run typecheck` cannot catch it either: a fabricated
> `mime: ''` and `is_text_editable: false` typecheck perfectly.

**Decision: inline classification is EXTENSION-ONLY, and the divergence is named rather than
hidden.** The resolver calls a sibling of the classifier —
`classifyLibraryRefByExtension(name: string)` — which runs the same ladder with the two
server-sniffed inputs removed. Measured against `classifyLibraryEntry`'s actual body, that ladder
is identical for six of the ten kinds and differs in exactly two places:

| Classifier rung | Extension-only behaviour | Divergence from the pane |
|---|---|---|
| `mime.startsWith('image/')` → `image` | falls through to `IMAGE_EXTS` | An image with a **non-image extension** (or none) classifies `other` inline and `image` in the pane |
| `mime.startsWith('video/')` → `video` | falls through to `VIDEO_EXTS` | Same, for video |
| `html` / `pdf` / `audio` / `base` / `markdown` / `mermaid` | **identical** — all six are already extension-only | none |
| `entry.is_text_editable` → `text` | unavailable | A text file with an unknown or absent extension classifies **`other`** inline and **`text`** in the pane |
| default → `other` | identical | — |

All three divergences land on kinds that fall back to the §2.2 link treatment either way (`text`
and `other` both do), **except** the image/video-by-MIME case, which is a real loss: an
extensionless PNG shows as a link inline and as a picture in the pane. That is the accepted cost
of not fabricating a value, and §11.2 names the test that pins it — a test asserting the *same
file* classifies the same in both surfaces for every kind where it must, and asserting the
divergence explicitly for the three where it does not. Without that test the property FR-027
claims is checked nowhere.

*Rejected alternatives, recorded so this is not re-derived:* **fetch the entry** (there is no
single-entry endpoint; a directory listing per embed target's parent is a request per embed, an
entry in D3's concurrency bound and a reserved-height story while it is in flight — all of that to
recover two fields that change the answer for one edge case); **extend the graph edge with the
server's classification** (a fifth contract change on a schema shared by three other consumers, to
serve one client).

> **Correction to revision 1.** Rev 1 asserted, twice, that *"every renderer reads only
> `entry.path` or `entry.name`"* and rested the whole safety argument on *"the compiler proves
> it"*. **That was false, and nothing was compiled.** `LibraryDownloadCard` renders
> `{label} · {formatLibrarySize(entry.size)} · modified {formatRelative(entry.modified_at)}`,
> takes `onDownload: (entry: LibraryEntry) => void`, and takes no `workspaceId`. It is reachable
> from an embed three ways in the pane: `case 'other'`, and the `too_large` / `binary` fallbacks
> inside `LibraryTextBody`.

**Decision: exclude `LibraryDownloadCard` from the widening.** It keeps `LibraryEntry`. An embed
of a kind with no inline renderer does **not** produce a download card; it falls back to §2.2's
"embed shown as a link" treatment. Two reasons, and the second is the stronger:

- A download card in the middle of a paragraph is the wrong product answer regardless of types.
- The alternative — adding `size` and `modified_at` to `LibraryFileRef` — would require the
  resolver to obtain two fields a graph edge does not carry. Rev 1's own stated reason for
  returning a `LibraryFileRef` at all was that fabricating them is a cardinal error. We do not
  get to invoke that principle in D1 and abandon it here.

**Verification obligation, not a claim:** run `npm run typecheck` (`tsc -b --noEmit` — a bare
`tsc --noEmit` is a silent no-op in this repo) against the widened type **before this ADR is
ratified**, not after. Rev 1's "the compiler proves it" was an argument about a command nobody
ran; one run would have caught this.

#### (b) One layout variant — and the resolver props are *not* part of it

Add `variant?: 'pane' | 'inline'`, defaulting to `'pane'`. The rule is narrow and must be tested,
not merely written down:

> `variant` may change **layout only** — height, overflow, and whether chrome is shown. It may
> never change what is fetched, which states exist, or how any non-happy state is rendered.

**The cross-variant test, scoped so it is possible.** Rev 1 said the non-happy states must be
"byte-identical", which cannot literally hold if `variant` changes container classes. The
assertion is:

> For each renderer, mount it in both variants with **identical** props otherwise, and assert
> that the set of rendered state test-ids and their text content are equal. The outermost
> container's `className` is exempt; nothing else is.

A variant that quietly drops the "N views could not be loaded" notice inline would be invisible
in review and catastrophic in use — it is the exact silent-loss shape `BasePreview`'s own header
says that surface exists to end.

> **Resolved contradiction with D8.** Rev 1's D2(b) and D8 could not both hold: D8 threads a
> *different resolver* into an embedded `BasePreview`, and a resolver decides which of
> `resolved` / `unknown` / `unresolved` a cell link renders. The resolution is that
> `resolveWikilink` / `linkHref` / `onOpenPath` are **ordinary props, independent of `variant`**.
> The cross-variant test holds them fixed. Which resolver an embed is actually given is a
> separate, visible product decision — see D8 and §9 Q8.

#### (c) The PDF worker — corrected inference, and an explicit failure lifecycle

> **Correction to revision 1.** Rev 1's `[INFERRED]` named `GlobalWorkerOptions.workerPort`.
> **`LibraryPdfPreview` does not use that API.** It constructs a `new Worker(…)` inside the load
> effect and hands it over per document as `pdfjs.PDFWorker.create({ name: 'omnipus-library-pdf',
> port })`. So the question to verify is not "does PDF.js support `workerPort`" but **"can one
> `PDFWorker` instance back several concurrent `getDocument` calls in the pinned build"** — a
> different question with a different answer surface.

The bigger problem rev 1 missed is that sharing the port breaks a control the file was
deliberately built around. Each instance attaches `port.addEventListener('error', …, { once: true })`
and races it against `task.promise`, because a missing worker file *"makes `new Worker` succeed
synchronously but fail asynchronously … and `task.promise` hangs on 'Opening…' forever."* With
one shared port and one `once: true` listener per mount:

- one document's worker error rejects **every** mounted instance's load promise, so a healthy PDF
  reports the failure of an unrelated one — a false error, and the exact mirror of the false
  success D1 stage 2 exists to prevent;
- once the shared port has errored, every later mount reuses a dead port, and a naive reference
  count has no "the port is poisoned, replace it" state.

**Decision: a small pool with a visible ceiling is the primary design, not the fallback.** Rev 1
had it the other way round. Concretely:

- A module-scope pool of at most **N = 2** `PDFWorker` instances (the count is a tuning knob, not
  a contract; one is enough for every measured use — §2.1 counts one PDF in the whole vault).
- Each `getDocument` leases a worker. A lease beyond the ceiling **queues with a visible
  indicator** ("waiting for a PDF worker") — never a silent wait.
- Error attribution stays per document: the worker's `error` event **poisons the pooled worker
  and rejects only the leases currently held on it**, and the poisoned worker is terminated and
  removed from the pool so the next lease creates a fresh one.
- The pool is created lazily on first lease and terminated when the last lease is released.

> `[UNVERIFIED]` Whether one `PDFWorker` can back several concurrent `getDocument` calls in the
> **vendored** build in this tree. Not read out of the pinned version; not executed. If it holds,
> N can rise; if it does not, N = 1 and the queue does the work. **The design above is correct
> either way**, which is why it replaces rev 1's inference-dependent shared port. §9 Q6 is
> narrowed accordingly.

This lands in **step 1** of the sequencing, not later with PDF page fragments, because step 1 is
what first allows more than one PDF on a page. D-A's lazy mounting bounds how many are
*concurrent*; it does not bound them to one.

### D3 — Lazy mount, no cap (implements D-A)

Wrap each embed in an intersection observer. Mount when it enters the viewport plus a margin;
unmount when it is well outside. Because every embed reads through TanStack Query, an
unmount/remount is a cache hit rather than a refetch.

**Reserve the height before mounting.** Without a placeholder of roughly the right size, a
fifteen-module dashboard jumps under the reader's cursor as each module arrives. The reserved
height is per kind: a table view is tall, an image is its aspect ratio when known, a PDF is one
page. **For a `note` transclusion the height is unknowable before fetching**, so it reserves a
fixed three-line placeholder and accepts one reflow — stated rather than discovered.

**Inherit both of `BasePreview`'s staleness settings, and both figures are different.** Rev 1
named only one. Measured: the **view-result** query is `staleTime: 60_000` with
`refetchOnWindowFocus: false`; the **base-views** query is `staleTime: 10_000` with
`refetchOnWindowFocus: false`. An embed must inherit both, or alt-tabbing back into the app
re-evaluates fifteen views at once. The base-views query key is already
`['library', workspaceId, 'knowledge', 'base-views', entry.path]` — **keyed on the base file's
path, so N embeds of one `.base` already share one request.** D7 must not invent a different
key; that dedup is why D7's extra call is cheap rather than N-fold.

**Failure behaviour, which rev 1 omitted entirely.**

- A view evaluation that fails inside an embed renders **that embed's own error** with the
  server's reason and a manual Retry. It does **not** retry automatically on re-entering the
  viewport — scrolling up and down forty times must not be forty retries. `retry: false` on the
  query, and the error is held in the query cache so a remount shows the error, not a fresh
  attempt.
- One page-level graph failure is one page-level error (D1 stage 2's `graph_unavailable`), not
  fifteen per-embed errors.

**A bound on concurrent work, which D-A requires and rev 1 never set.** D-A rejects a cap on
*count* and explicitly endorses a bound on *work*. Scrolling fast through a forty-module
dashboard must not fire forty simultaneous evaluations at a single-binary server.

> At most **4** view evaluations in flight at once, page-wide. Beyond that, an embed shows
> "queued" — visible, per Q6's principle, never a silent wait. An embed scrolled back out of view
> before its turn cancels its queue slot.

**Print and browser find — N3: the print guarantee is DROPPED, and the limitation is stated
instead.** A lazily-mounted dashboard printed or Cmd-F'd contains only what has been scrolled
past. Rev 2 answered that with a `beforeprint` handler that "holds the print until all of them
settle or error".

> **That is not achievable and revision 3 stops promising it.** `beforeprint` is dispatched
> synchronously and cannot await asynchronous work; a print started from the browser's own menu
> cannot be held open. A handler written against it appears to work locally on a warm cache and
> fails exactly when the modules are slow — which is the only time it matters. Shipping it would
> be the silent-subset failure one door further along: a guarantee in the document, a partial page
> on the paper, and nothing anywhere saying which.

**So, plainly: printing a dashboard prints only the modules that have already mounted.** That is
true today and stays true after this work. Making printing complete — an application-owned print
control that mounts everything, waits, then prints — is **separate, unscheduled work**. It is not
in this ADR's scope, not in its sequencing, and not in its exit criteria.

What this ADR *does* owe the reader is honesty about it, which is cheap and is kept:

> While any embed on the page is unmounted, the reader shows **one line** stating that
> find-in-page **and printing** will not reach modules that have not been scrolled to. The notice
> disappears once every embed on the page is mounted, and never appears on a note with no embeds.

That one line is the whole of this ADR's print position. It is a statement, not a mechanism, and
it is the honest form of a capability we are not building.

### D4 — A missing target says what is missing, on both surfaces (implements D-B)

**For the reader.** A block-level marker that names the target and the reason, drawn in the same
muted, dotted treatment `UnresolvedLink` already uses so the two cannot drift apart visually.
`indeterminate` uses a **visibly different** marker from `unresolved` — same family, different
statement.

| State | Case | What it says |
|---|---|---|
| unresolved | No file matched — **and no skip entry matches the target under ANY of the three clauses**, including the ancestor-directory clause added in revision 4 | `Nothing in this knowledge base is named "Tasks.base"` |
| **unresolved** | **Target outside the collection root** (`unresolved_reason = outside_root`) | `This embed points outside the knowledge base` — **and names nothing further**. A *different* sentence from the row above, decided by a wire field, not guessed |
| unresolved | File found, view not | `"Tasks.base" has no view called "Needs Daniel"` — **and lists the views it does have** |
| unresolved | File found, heading not | `"Plan" has no heading "Q3"` — and lists the headings it has |
| unresolved | The view exists but cannot be served | the server's own reason, verbatim (`unservable_reason`) |
| unresolved | Ambiguous name | resolved per the fixed tie-break ladder, **and** the ambiguity reported alongside |
| **indeterminate** | **Unresolved edge, but a skip entry matches the target** — by path, by basename, **or by naming an ancestor directory of it (revision 4, C1(r2): the dominant walk-level shape, which the first two clauses could not match)** | `This embed could not be checked against the knowledge base` + the skip reason — **never** "nothing is named X". This is the dominant real case (§D1 stage 2) |
| **indeterminate** | No matching edge on a loaded graph | same marker; the reason is a reported truncation, or **"no reason available"**, which is what the common key-mismatch case will actually say |
| **indeterminate** | `resolution` and `nodes[].exists` disagree | same marker; reason "the knowledge base gave two different answers about this target" |
| **page-level** | The graph loaded with **zero edges and zero skips** — the out-of-scope-collection answer | **one** statement that this knowledge base could not be read, not N per-embed markers. See D1 stage 2 producer 2 |

Four dependencies rev 1 and rev 2 did not sequence, all now in §6:

- **"File found, heading not" needs `heading_found` on the wire** (§2.3). It cannot be computed
  client-side from today's edge.
- **The containment row needs `unresolved_reason` on the wire** (§2.3's revision-3 correction).
  Without it there is one value for two facts and the reader prints the wrong sentence. This is
  a **prerequisite of a P0 story**, not a nicety.
- **"and lists the headings it has" needs an outline fetch.** `GET /library/{workspace_id}/knowledge/outline`
  exists; the resolver calls it **only when the unresolved-heading case fires**, never eagerly.
- **The "file found, heading not" row must not fire for a `.base` target or a block reference.**
  `heading_found` is false for both by construction (§2.3). The reader consults it only when the
  target is markdown **and** a heading fragment was written.

`unservable_reason` and `candidates` are not new behaviour to invent — `KnowledgeBaseView` and
`KnowledgeGraphEdge` already carry them, and both were built on the principle that resolving
something deterministically is not a licence to stay quiet about it.

**For the agent.** Today an agent reading a dashboard cannot tell an embed from a link.
`knowledge_read` renders a `LINKS` section from a `ReadLink`, and `ReadLink` has no embed flag
— even though `ResolvedLink`, which it is projected from, embeds `Link` and therefore already
*has* `Embed bool`. The projection simply drops it.

**Decision:** add `Embed bool` to `ReadLink`, populate it from the source it is already projected
from, and mark it in the existing `LINKS` section. The current renderer
(`knowledge_read.go::renderReadLinks`) emits, per link:

```
  -> <peer>[ "alias"][ #heading][ (ambiguous: also matches …)][ (line N)]
  -> (unresolved) <Form>[ — <reason>]
```

so the embed marker slots into the existing prefix position:

```
  -> [embed] 06-Bases/Tasks.base #Needs Daniel (line 12)
  -> (unresolved embed) ![[Decisions.base#Awaiting founder]] — no such file (line 18)
```

> `[UNVERIFIED]` The exact rendered strings above. `renderReadLinks` was read on `def10b90e` and
> the format above is transcribed from it, but no test was run and no live output captured. The
> implementer asserts against the real renderer, not against this block.

**Deliberately not** a fifth member of `knowledge_read`'s `include` parameter. `include` accepts
four members (`ReadIncludeOrder`) and defaults to all of them, so a fifth one changes the
response for every read of every note, including the overwhelming majority that embed nothing.
Marking embeds inside the section that already exists costs zero extra tokens on a note without
embeds and puts the fact where a model is already looking.

**One overload that must be named rather than left implicit.** `ReadLink` has a `Heading` field
and nothing that distinguishes a `.base` **view label** from a markdown **heading**. The line
`-> [embed] 06-Bases/Tasks.base #Needs Daniel` reuses the heading slot for a view label, so an
agent cannot tell "section of a note" from "saved view of a data table" — a distinction that
changes what the agent should say about a broken one.

> **Decision: discriminate, do not overload.** A `.base` target renders `view "Needs Daniel"`
> rather than `#Needs Daniel`. The kind is already known at projection time from the target's
> extension, so this costs one branch and no new field.

**A second overload, found by the spec review (M9) and NOT present in rev 2: the agent surface
echoes the escaping path that D6 forbids the reader to echo.** Measured, `renderReadLinks` prints,
for any unresolved link:

```go
fmt.Fprintf(b, "  %s (unresolved) %s", arrow, l.Form)
```

`l.Form` is the link **as written**. For `![[../outside/secret.md]]` the whole escaping path
reaches the model verbatim — today, before this ADR changes anything — and D4's `Embed` marker
preserves it. Rev 2 applied the redaction rule to one surface and was silent on the other.

> **Decision: the two surfaces differ, deliberately, and here is why.** An agent reading a note
> through `knowledge_read` is reading **that note's own source**, which already contains the
> escaping path in plain text; redacting it from the `LINKS` projection while the `BODY`
> projection carries it would be theatre, and would leave the agent unable to say *which* embed is
> broken. The reader's marker is different: it is rendered **beside** the source rather than
> instead of it, for a person who did not ask to see the path.
>
> **What is NOT acceptable is leaving that as an unstated asymmetry**, which is what rev 2 did.
> It is written down here, and §11.2 names a test asserting the agent line **does** carry the
> containment reason — `(unresolved embed) ![[../outside/secret.md]] — outside the knowledge base`
> — so the agent is told *why*, not merely that something failed.

### D5 — Transclusion, **one level only** (N1)

**What it means.** A note can show another note's contents inside itself. The shown note's **own**
embeds appear as links, not as content. There is no second level.

#### The rule

> **A transcluded note's embeds render as the §2.2 link fallback with a one-line reason. Always.
> No exception, no depth, no nesting.**

Rev 2 stated this rule and then contradicted it. Its D5 said *"Nested embeds inside a transclusion
are OUT of step 3"* and, two bullets later, described *"a five-deep nest [as] five **sequential**
fetches, each starting when the previous renders into view."* Both halves were carried into the
implementation spec, which built a cycle detector, a depth cap, four tests and a success criterion
**on top of an input the product cannot construct**: if nesting never reaches level 2, then A→B→A
cannot occur, a six-deep chain cannot occur, and a diamond collapses to two links. The second half
is deleted. This half is the rule.

The reason the rule holds is the same one rev 2 gave for the first half, and it is worth keeping
because it is the constraint, not a preference: the outer note's `kind: 'links'` graph query
returns only the *outer* note's edges (`g.Links(notePath)`). An inner note's embeds resolved
against it would find zero matching edges and land, every one of them, in `indeterminate` —
technically honest and practically useless. The link fallback is a better answer, and it is
already built and already tested.

#### What is deleted, and what would be needed to bring it back

Deleted from this ADR and from the implementation spec: the **cycle set**, the **depth cap of 5**,
the "circular embed" marker, the diamond case, and their four tests and success criterion.

Nesting was considered and declined. Recording what it would take, so a future reader does not
re-derive it from scratch:

1. **One `kind: 'links'` graph query per transcluded note**, keyed on that note's path — without
   which every nested embed is `indeterminate` and the feature is worse than the link fallback.
2. **A place in D3's concurrency bound** for those queries. Today that bound counts view
   evaluations only; a nest would add a second class of in-flight work to the same budget.
3. **A reserved-height story per level.** D3 reserves a fixed three lines for a transclusion and
   accepts one reflow; N levels is N reflows, compounding.
4. **Then, and only then, a cycle set** — the set of collection-relative note paths currently on
   the render stack, in React context, pushed per level and **popped on unmount** (a stack, never
   a visited-ever set, or a diamond is misreported as a loop) — plus a resource bound for deep
   non-cyclic nesting, which must fire **visibly**. Obsidian's documented cap of 5 degrades
   silently to a bare filename, and its 2021 report of mutually recursive embeds pinning a CPU
   during export was never closed; both are behaviours to refuse, not to copy.

Items 1–3 are the reason the answer is "not now": they are the cost, and §2.1 measures **zero**
transclusions across 784 notes. Item 4 is the part everyone reaches for first and is the *last*
thing that becomes necessary — which is precisely how a cycle detector with no reachable input got
into rev 2 and into the spec.

#### How the transcluded text is obtained

`LibraryMarkdownPreview` requires a `content` prop; it does not fetch. `LibraryFileRef` carries no
content.

> - **The fetch is `GET /library/{workspace_id}/content`**, the same call `LibraryPreviewPane`
>   already makes, on the same query key `libraryQueryKeys.content(workspaceId, workspacePath)`
>   — so a transcluded note already open in the pane costs nothing extra.
> - **Heading and block slicing runs client-side**, against the fetched text, using the same
>   parser the reader already uses. No server-side range endpoint is introduced.
> - **A note that transcludes itself** (`A` embedding `A`) is not a cycle under this rule and needs
>   no detector: A's embed of A is a first-level transclusion, it renders A's text once, and A's
>   *inner* embed of A is a nested embed, which renders as a link. It terminates because the rule
>   terminates it, not because anything counted.

Block references (`![[Note#^blockid]]`) resolve the same way. `links.go` already keeps block
anchors separate from heading text (`l.BlockID` vs `l.Heading`) so a `#^abc123` is never matched
against a heading — **but `BlockID` is not projected onto the wire** (§2.3), so this is invisible
to the SPA until the contract work in §6 step 1c lands. Rev 1 cited the Go behaviour as though the
client could see it.

### D6 — The agent surface: `knowledge_edit` with `op="embed"` — **not** a new `knowledge_embed` tool

**This overrules the framing in the brief for this ADR, with reasons.**

The brief asked for a `knowledge_embed` tool "following the `knowledge_link` precedent". The
*principle* of that precedent is right and is kept: the agent supplies a path plus named
modifiers, and the tool writes the notation — `knowledge_link`'s own description says *"the link
syntax is written for you."* But the precedent's **form** was retired.

Verified on this branch:

- `knowledge_link` and `knowledge_set_property` are **not live tools**. Neither appears in the
  global ceiling (`pkg/config/defaults.go`) nor in any per-agent seed (`pkg/coreagent/core.go`).
  The live knowledge catalogue is eight names: `knowledge_describe`, `knowledge_find`,
  `knowledge_read`, `knowledge_list`, `knowledge_edit`, `knowledge_restructure`,
  `knowledge_configure`, `knowledge_base_create`.
- `knowledge_edit` **is** the consolidation of them, and its file header states the reason
  (FR-070c): *"policy resolves on the tool name alone, so five near-synonymous tools with
  independent policy toggles were never the five-tools-in-one this file is instead."* Its closed
  op set is `create`, `set_property`, `append_section`, `link`, `replace_body`.

Adding a ninth top-level tool would re-create precisely the pattern this codebase finished
retiring three weeks ago.

**Why an op is also the cheaper answer under Constraint #6.** Tool policy has exactly two
layers and no wildcards for static builtins, so every new static tool name needs a ceiling
entry in `pkg/config/defaults.go` **and** an entry in each per-agent seed map in
`pkg/coreagent/core.go`. Choosing an op means **no ceiling entry and no seed change at all** —
`knowledge_edit` already carries `"allow"` in the ceiling and an explicit value in every seed.
An operator who has already decided how much writing an agent may do in their knowledge base
does not have to decide again, and cannot end up with a half-configured install where one agent
rides the ceiling for a capability nobody granted it (the accepted-but-noted state in ADR-077
R1). §7.2 records the flip side of that trade as an accepted consequence.

**It satisfies the blast-radius rule (FR-070b) as written.** `knowledge_edit` writes only the
file named in `path` and never a second file. An embed is one line of text inserted into one
note. It composes no destination from two arguments, targets no directory, and touches nothing
the caller did not name.

**The op.**

| Argument | Meaning |
|---|---|
| `path` | The note the embed is written into — the **only** file this call writes |
| `target` | What to embed: a note name, or a path relative to the collection root |
| `view` | For a `.base` target: which saved view, by its human label |
| `target_heading` / `target_block` | For a note target: one section, or one anchored block **of the target** |
| `page` | For a PDF target: which page |
| `width` | For an image target only |
| `section` | Which heading **of `path`** to put it under (created if absent) — same semantics as `op=link` |
| `expect_version` | Required, per FR-106 |

> **Two mechanical corrections rev 1's "mechanically this is additive" claim missed.**
>
> **(a) `heading` was going to mean two opposite things.** `editArgNames` already contains
> `heading`, and `knowledge_edit`'s own `Parameters()` documents it as *"append_section: the
> section's heading"* — a heading **of `path`**. Rev 1's `op=embed` reused the same name for a
> heading **of `target`**, with the referent decided by the value of `op`. The tool's own header
> says the argument list exists because *"a silently ignored argument is a caller that believes
> it narrowed something"* — a same-name/opposite-referent argument is worse than a silently
> ignored one, because a model has a plausible wrong reading. Hence `target_heading` /
> `target_block` above.
>
> **(b) `unknownArgs` is checked once, globally, before the op dispatch.**
> `knowledge_edit.go::Execute` calls `unknownArgs(args, editArgNames)` *before* switching on `op`.
> Adding `view`, `page`, `width`, `target_heading` and `target_block` to that one list makes them
> **accepted on every op** — so `op=link` with `width=400` would pass validation and be silently
> ignored. That is the exact regression the comment above `editArgNames` describes (`"bodyy"` for
> `"body"` passing through and reporting success). **A per-op accepted-argument set is therefore
> part of this work, not an implementer's detail**: the global sweep stays as the outer net, and a
> second, per-op sweep refuses an argument no op reads *for that op*. Both are invisible in
> review and a passing test suite catches neither, so §11 names the test.

**Validation happens at write time, not at read time.** This is the whole value of the tool
over the agent typing the notation itself:

- the target resolves inside the collection — reuse `cleanNoteArg`, exactly as `op=link` already
  applies containment to its link target (`authoring_tools.go`, the `op=link` branch, whose own
  comment says *"refusing here means such a link is never written at all, rather than written and
  then reported unresolved forever"*);
- a named `view` exists among the views actually imported from that `.base`, and a named
  `target_heading` exists in the target note. A modifier that does not resolve is **refused,
  listing what does exist** — the same refusal shape `knowledge_read` already uses for an unknown
  section;
- modifiers are checked against the target's kind. `width` on an audio or video target is
  refused rather than accepted-and-ignored, because Obsidian accepting it and doing nothing is
  a documented trap, not a feature to reproduce.

**Read-time containment, which write-time validation does not cover.** The founder's vault is
authored in Obsidian and synced, so an embed can arrive that `op=embed` never wrote.
`links.go` marks a target outside the collection root `unresolved` and does not read it (FR-043),
so containment holds. **The marker must not echo the escaping path back to the reader** — it says
`this embed points outside the knowledge base` and names nothing further; the full target is
already visible in the note's source if the reader wants it.

### D7 — `#View` names a **label**; the server's slug is the address

WL-5 asked, correctly, to settle this before building. It is settled here.

All 75 vault embeds name a view the way a person says it — `![[Tasks.base#Needs Daniel]]`. The
server's addressable identity for a view is the **importer's slug** (`KnowledgeBaseView.name`,
e.g. `tasks--needs-daniel`), and that schema's description states in capitals that the slug is
the only address and **must never be reconstructed by a client**: the importer's collision
counter cannot be mirrored, and mirroring it is precisely the defect that made two tabs render
the same rows under different names.

**So the mapping is label → slug, and it comes from the server.**
`GET /library/{id}/knowledge/base-views` already returns, per view, both `name` (slug) and
`label` (display), plus the collection to evaluate against. The embed resolver calls it for the
`.base` file the graph resolved, matches the fragment against `label`, and passes the resulting
`name` verbatim to `GET .../knowledge/view`. Nothing is re-derived.

> **The `path` this endpoint takes is WORKSPACE-relative** (`contracts/openapi.yaml`, the
> `getKnowledgeBaseViews` `path` parameter: *"Workspace-relative path of the .base file"*), while
> the graph's `to_path` is **collection-relative**. Rev 1 passed one to the other. The resolver
> passes the **converted** path from D1 stage 4. This is not a corner case: it is wrong for every
> collection not mounted at the workspace root, which is the founder's own layout.

> **Cost:** none beyond the first embed of each `.base`. The existing query key is
> `['library', workspaceId, 'knowledge', 'base-views', entry.path]` — already keyed on the base
> file — so fifteen embeds over five base files are five requests, not fifteen. Reuse that key
> verbatim; do not introduce a per-embed one.

Matching rule, stated so it cannot drift: exact `label` match first; then case-insensitive
`label`; then exact `name`, so an embed written against the slug also works. If two views share
a label, **do not pick one** — render unresolved naming both (§9 Q3 asks whether the founder
wants that or a deterministic first-match).

**The fragment-less form `![[X.base]]`**, which rev 1 listed in scope and decided nowhere:

> Render the **first imported servable view**, and **say so** — a one-line caption naming the
> view being shown and the fact that the embed did not choose one. Rendering all views as tabs
> (what the pane does) is wrong inline: a dashboard module that silently shows tab one of five is
> the silent-subset failure again. Zero of the founder's 75 embeds use this form, so the cost of
> the caption is nil and the honesty is free.

### D8 — Interactivity: record state persists, view shape stays local (implements D-D)

Two kinds of state, and the line between them is the decision:

| | Where it lives | Who sees the change |
|---|---|---|
| **A record's field** — a status, a date, an owner | The note's frontmatter, on disk | Everyone, everywhere. That is the point. |
| **A view's shape** — filter, sort, visible columns | React state in that one embed | Only that embed, only this session |

**View shape is never written back to the `.base` file or to the saved-view YAML.** This is a
deliberate departure from Obsidian, which does write it back, so that filtering an embedded
base in one note re-filters every other embed of it — behaviour users work around by writing
CSS to hide the toolbar. That a workaround exists at all is the evidence it is a defect and not
a contract.

Corollary, recorded so a future "helpful" fix does not quietly reintroduce Obsidian's behaviour:
**because view shape is per-embed and non-persistent, a reader who filters an embed and reloads
loses that filter. That is intended.**

**Links inside views: the resolver question is NOT settled here.** Rev 1 stated flatly that
`ViewCellLink`'s resolver props *"must come from the embedding note's reader, not from the pane"*
and, three paragraphs later, that an embed *inherits* WL-1's asymmetry. **Those two cannot both
be true**, and the reason is measurable:

| Resolver | Source | States it can answer |
|---|---|---|
| `BasePreview`'s own | the view's **loaded rows** | `resolved` or `unknown` — never `unresolved`, because absent from a subset is not absent from the collection |
| `KnowledgeNoteView`'s | the note's **link graph** | `resolved`, `unresolved`, `unknown` |

Threading the reader's resolver in makes an embedded view show **more resolved links, in a
different colour**, than the same view in the pane. That is a visible product difference, not a
wiring detail. `ViewCellLink` was deliberately built to take these as plain props rather than as
context precisely because a view has many cells across many rows — that design choice is what
makes either answer a wiring change and not a rewrite.

> **Deferred to the founder as §9 Q8**, because it changes what D-D's "links inside views work in
> embeds" actually looks like. *Architect's recommendation:* thread the **reader's** resolver.
> A dashboard embed's whole purpose is to be read in the note; a link that opens the pane instead
> of the note the reader is in is the wrong destination, and an honest `unresolved` is better than
> a permanent `unknown`. The cost is that the same view renders differently in two places, which
> should be stated in the UI rather than hidden.

Whichever is chosen, `onOpenPath` comes from the reader (so a click opens in the reader the
person is already in) and `linkHref` from the reader's `libraryNoteHref` — those two are
uncontroversial and are not what Q8 asks about.

WL-1 itself is not fixed here: fixing colour without fixing scope would make an embed *claim*
verification it does not have.

### D9 — External content: YouTube only, and it changes the **SPA's** policy, not the Library isolation policy

This section is security-critical and contains the single most consequential correction in this
ADR.

**The two policies are different, and only one of them is the right one to change.**

| Policy | Where | What it governs |
|---|---|---|
| `libraryIsolationPolicyTemplate` | `pkg/gateway/library_isolation_policy.go` → applied in `inline_serving.go` | The **isolated inline preview** — agent-generated HTML served into a sandboxed frame with an opaque origin |
| `spaContentSecurityPolicy` | `pkg/gateway/embed.go` | **The application itself** — every byte of the SPA, including the note reader |

A YouTube embed inside a knowledge-base note renders **in the SPA document**. It is therefore
governed by `spaContentSecurityPolicy`, which today ends `… frame-src 'self'; object-src 'none';
base-uri 'none'; form-action 'self'; frame-ancestors 'none'`.

**Decision: add exactly one host to `spaContentSecurityPolicy`'s `frame-src`, namely
`https://www.youtube-nocookie.com`. Do not touch `library_isolation_policy.go`.**

Changing the isolation policy instead would open *agent-generated HTML previews* to third-party
framing — a completely different and far larger blast radius than showing a video in a note —
and that file is guarded by a test that parses the package's own source and fails the build if a
second copy of the policy string appears anywhere. Its header explains why in terms worth
repeating here: a dropped directive there has **no visible symptom**; the preview still renders
and is simply no longer contained.

#### The measurement obligations rev 1 omitted

`embed.go`'s own header states, about the string this decision edits:

> THIS STRING WAS SHIPPED UNMEASURED, AND IS NOW MEASURED — but not yet FROZEN. §10.7's freeze
> condition is a headed run in Chromium, Firefox AND Safari with zero violations … Firefox and
> WebKit remain outstanding.

Rev 1 proposed editing an actively-unfrozen, actively-measured string while naming none of the
artefacts that measure it. All three exist and all three change:

| Artefact | What changes |
|---|---|
| `pkg/gateway/embed_csp_test.go` | `TestSpaServedWithCSP` and `TestSpaCsp_DirectiveFloor` pin the directive floor and assert `frame-ancestors 'none'`. The floor test's second half mutates the string to prove the checker can fail; the new `frame-src` value must be added to the expected policy in both, and the floor must keep asserting `frame-ancestors 'none'` unchanged. |
| **`docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` §10.7** | **Newly named in revision 3 (review finding M2), and missing it fails CI.** `TestSpaServedWithCSP` does **not** compare against a literal in the test file — it reads its oracle out of that document: `os.ReadFile(specDocRelPath)`, then `regexp.MustCompile("(?m)^default-src 'self';.*$")`, then `require.Len(t, matches, 1, "§10.7 must contribute exactly one literal policy line to the spec")`. The policy line in the spec doc is the oracle and must be edited in the same change. |
| `pkg/gateway/embed.go`'s **second, derived policy** | **Newly named in revision 3.** `spaPdfWorkerContentSecurityPolicy = withWasmCompilation(spaContentSecurityPolicy)` is served on the PDF worker path. Adding a `frame-src` host propagates into it automatically. **Decision: that is intended and harmless** — the worker path frames nothing, so an unused allowance there is inert; but D9's "exactly the allow-listed frame hosts and nothing else" must be understood as a statement about **both** strings, and the test that pins it must read the served header on both paths rather than assume one. |
| `tests/e2e/csp-assumptions.spec.ts` | The Chromium measurement run (A0–A6, with A0 as the positive control proving both violation channels fire). **A new journey is required**: a note containing an allow-listed YouTube embed, click-to-play pressed, asserting zero `frame-src` violations — and A6 ("the SPA refuses to be framed") must be re-run unchanged to prove `frame-ancestors` was not weakened. |
| `docs/internal/architecture/csp-audit-2026-09-05.md` | Amended **in the same PR**. It records the unfrozen status and the per-assumption verdicts; merging this without amending it makes it stale the day it lands. |

> **Q9's config key and that byte-for-byte oracle are structurally incompatible, and revision 3
> resolves it rather than leaving the collision to CI.** A policy string that varies per install
> cannot be pinned to *"exactly one literal policy line"* in a document. The resolution:
>
> - the **oracle document keeps a single literal line — the shipped default**, with the default
>   allow-list in it, and `TestSpaServedWithCSP` keeps reading it unchanged;
> - a **second** test asserts the *configured-empty* install's served header **differs** from that
>   default, and that the external host is absent from it;
> - **both assertions live in one test body**, so neither can pass alone. A test that only checks
>   "the emptied setting yields no external host" passes on a build where the host was never added
>   in the first place.

**The freeze is not claimed by this change.** After it merges, Chromium is measured and Firefox
and WebKit remain outstanding — exactly as before. The amended audit says so.

**`frame-ancestors 'none'` is untouched, and here is why the coupling is unaffected.**
`embed.go:21-28` documents `frame-ancestors 'none'` as FR-006b's *compensating control* for the
**preview** policy's `frame-src 'self'`: a previewed page may embed any gateway page, so the
control belongs on the framed resource, which is the SPA. This decision changes what the SPA may
**frame outward** (`frame-src`); it changes nothing about what may **frame the SPA**
(`frame-ancestors`). The two directives share a string and nothing else. The A6 re-run is what
proves that, rather than this paragraph.

#### What follows from D-C's "no external images"

Stated so nobody re-litigates it later: `img-src` stays `'self' data: blob:`. There is therefore
**no YouTube thumbnail**. The click-to-play placeholder is drawn locally — a play control over a
surface panel, with the video title only if we already have it.

**Click-to-play is mandatory, not cosmetic.** An eagerly mounted player frame contacts Google
the moment it mounts, for every embed, before anyone has chosen to watch anything. With D-A's
lazy mounting that is still one contact per embed scrolled past. A placeholder means the network
contact happens exactly when a person presses play, and never otherwise.

**The frame, specified:**

- Source is constructed, never passed through:
  `https://www.youtube-nocookie.com/embed/<id>` where `<id>` matches `^[A-Za-z0-9_-]{11}$`, plus
  an optional integer `start`. A user-supplied query string is never forwarded.
- `sandbox="allow-scripts allow-same-origin allow-presentation"` — **no** `allow-top-navigation`,
  **no** `allow-popups`. (`allow-same-origin` grants the frame *its own* origin; it is
  cross-origin to us either way, and the player does not function without it.)
- `referrerpolicy="no-referrer"`, `allow="encrypted-media; picture-in-picture; fullscreen"`.
- Recognised **only** from a markdown link whose URL host is on the allow-list — `![](https://…)`
  — and recognised at D1 stage 0, before the graph is consulted, because `links.go` emits no edge
  for an external destination and never will. Never from `![[…]]`, which addresses files inside
  the collection and nothing else. Raw `<iframe>` markup stays inert, because the pipeline has no
  raw-HTML plugin (§2.5).
- **The allow-list exists twice — once in the SPA, once in the Go policy string — and a test
  must assert they are equal.** If they drift, the symptom is a blank frame with nothing anywhere
  naming the cause. That is not hypothetical: `embed.go`'s own comment records that shipping
  `connect-src 'self'` without the ICE schemes broke the live browser view and surfaced as a
  *server-side* "capture/encoder/ICE" error, with CSP named nowhere.

**Rollback.** Unlike every other step here, this one cannot be undone by not using a feature: the
host is in the application's policy for every operator, including a sovereign no-telemetry
deployment that does not want it. §9 Q9 asks whether it should be a config key.

Everything else stays out, permanently: all plugin formats (Dataview, Excalidraw, Kanban,
Templater, Charts, ABC, Admonition — not core Obsidian, zero vault use) and **Canvas**, which
the founder ruled out outright and which even in Obsidian renders shapes without their card
text.

---

## 4. The write path — the highest-consequence part of this ADR

Inline record editing (D-D's second half) is the only part of *this work* that writes to the
knowledge base from the browser. It is phased last, because it has no existing safe door — and
because, as §4.1 now records accurately, the door that does exist is already open.

### 4.1 What exists today, precisely — including a live defect this ADR inherits

- **No record-field write endpoint.** Correct.
- **Two whole-file write endpoints exist, and both are unversioned and unaudited.** Rev 1 named
  one and framed it as an adjacent hazard. Measured on `def10b90e`:

| Endpoint | Contract | Version token | Audit |
|---|---|---|---|
| `PUT /api/v1/library/{workspace_id}/content` | *"Full replacement text content"*, *"overwriting any existing content entirely"*, up to 10 MB. `LibraryContentRequest` requires only `path` and `content`. | **none**, on the request or on the `GET` counterpart | **none** — `handleLibraryContentPut` never calls `logLibraryAudit` |
| `PUT /api/v1/library/{workspace_id}/content-binary` | Sibling, same *"overwriting any existing content entirely"*, up to 25 MB base64. It is the door a filled PDF goes through. | **none** | **none** — `handleLibraryContentBinaryPut` never calls `logLibraryAudit` |

- Every other Library mutation **is** audited: `logLibraryAudit` is called for `library.delete`,
  `library.upload`, `library.mkdir`, `library.create_vault`, `library.rename` and the transfer
  modes, in `pkg/gateway/rest_library.go`. The two content writes are the only mutating handlers
  in that file that are not.
- Meanwhile every agent write to a note goes through `EditNote` with a **mandatory** version
  token (FR-106 — an empty token is refused too, deliberately), a lock, an atomic write, and an
  audit record (FR-090). `knowledge_edit`'s header states the invariant: *"every op below still
  goes through `EditNote` … so every write shares ONE lock, ONE version-token compare-and-swap and
  ONE atomic-write path."*

> **This is a CURRENT DEFECT, not a prospective hazard, and the SPA walks through it today for
> note bodies.** `src/components/library/preview/useLibraryFileEditor.ts`'s mutation is
> `putLibraryContent(workspaceId, { path, content })` — no version token. `useLibraryFileEditor`
> backs `LibraryTextPreview`, which backs `LibraryMarkdownPreview`, `LibraryCodePreview` and
> `LibraryMermaidPreview` — i.e. it *is* the editor a person uses to edit a knowledge-base note's
> body from the Library. **So today: a human editing a note in the Library saves last-write-wins,
> with no conflict check, and leaves no audit record, while every other library mutation is
> audited.** An agent's concurrent write is silently clobbered.
>
> Rev 1 said only *"the SPA has an unguarded door and the agent has a guarded one, into the same
> files"* and listed nothing about it in §7.4. That understated a shipped data-loss path as a
> design tension. It is recorded here as a live residual and in §7.4 as one.

**Three more unversioned doors, named in revision 3 so the safety claim can be qualified honestly
(review finding M11).** `rest_library.go`'s mutating routes are `DELETE entries`, `PUT content`,
`PUT content-binary`, `POST upload`, `POST mkdir`, `POST vaults`, `POST rename`, and
`POST move|copy`. Step 0 puts a version check on **two** of them.

| Still version-free after step 0 | Can it destroy a note an agent is mid-write on? |
|---|---|
| `deleteLibraryEntry` | **Yes**, unconditionally — and it does not refuse an existing destination, because it has none |
| `uploadLibraryFiles` | **Yes** — an upload over an existing path replaces it, with no 409 |
| `renameLibraryEntry`, `moveLibraryEntry` / `copyLibraryEntry` | Partly — these at least refuse an existing destination with 409, so the loss requires the *source* to be mid-write |

All four **are** audited, so the loss is traceable after the fact; none is preventable. §7.1's
claim is therefore **scoped to whole-file content writes** and no longer stated unqualified.
Closing the other three is separate, sized work — not something to leave implied by a sentence
about "every surface".

### 4.2 Decision: a new endpoint, contract first, onto the same guarded path

**Inline editing must not go through `PUT .../content`.** Doing so would mean the browser reads
the whole note, changes one field, and writes the whole note back, with no way to detect that an
agent wrote to it in between. That is exactly the lost update the version token exists to
prevent, arriving through the one door that has no lock on it — and it would produce no audit
record, quietly violating FR-090 for every human edit.

Per Constraint #8, the contract comes first — schema, then generated types, then handler:

- `contracts/components/schemas/KnowledgeRecordFieldWrite.yaml` —
  `{ collection_id, path, property, value, expect_version }`
- `contracts/components/schemas/KnowledgeRecordFieldWritten.yaml` — `{ path, version, changed }`
- A new operation under `/library/{workspace_id}/knowledge/`, referencing both, with **409 →
  `KnowledgeConflictError`**, which already exists, is already generated in Go and TypeScript,
  and already has a runtime validator at `src/lib/api/generated/schemas.ts`.
- The handler routes through **`RecordWriteRequest`'s existing semantics**, not through a raw
  `SetProperty` splice — see §4.2c, which is a **change from revision 2** and the largest one in
  the write path.

**"One audit sink" is aspirational today, and step 5 must make it true rather than assume it.**
`pkg/knowledge/audit.go` states that the knowledge tools *"bypass"* the gateway's
`logLibraryAudit` path *"entirely"* — so there are two sinks, not one.

Step 5's preconditions, corrected in revision 3:

1. **Q1 answered** — a REST caller has no `AgentID`. **Rev 2's supporting claim here was false and
   is withdrawn:** it said *"`EditNoteRequest` requires both `Audit AuthorAudit` and
   `Actor AuthorActor`."* Measured, it requires **neither**. `Audit` is an interface and every
   emit is guarded `if a.sink != nil`, so a nil `Audit` **writes the file and records nothing,
   with no error**; `Actor.AgentID` is copied into the record unchecked; and `EditNote`'s only
   precondition is `if c == nil`. The emptiness guarantee lives one layer up, in
   `pkg/knowledge/audit.go`'s `NewWriter`. **The REST handler MUST construct its writer through
   `NewWriter`** — that is the only place an actor with neither an agent nor a user is rejected,
   and an implementer who reads "call `EditNote`" and does so directly bypasses it.
2. ~~`pkg/audit/events.go` extended with the `knowledge.*` event names.~~ **DELETED — this work
   does not exist. It was already done before this ADR was written.** All five names —
   `knowledge.note.create`, `.write`, `.edit`, `.rename`, `.delete` — are registered in
   `IsValidEventName` (`pkg/audit/audit.go`, **not** `events.go`, which holds only constants), and
   `pkg/knowledge/audit_event_names_test.go` already asserts them *with the negative control*
   (`assert.False(t, audit.IsValidEventName(...("knowledge.note.not_a_real_event")))`).
   **The source of the error is a stale comment** in `pkg/knowledge/audit.go`, above the
   `EventKnowledgeNote*` constants, which still reads *"pkg/audit's IsValidEventName does not yet
   know them, so today each one triggers audit's warn-once 'unknown event name' log … the names
   belong in pkg/audit/events.go's switch."* Every clause of that is now wrong. Rev 2 read the
   comment; the spec read rev 2; the spec then specified a test (its test 73) whose subject
   already exists and which is green on day one with no implementation. **The remaining task is
   one line: correct that comment.** If step 5's REST write emits a *sixth* name, then and only
   then does a real `IsValidEventName` change appear — and it must be named, with the negative
   control kept.
3. **An explicit decision on which event the REST field-write emits.** **Decided:**
   `knowledge.note.write`, through the knowledge sink, because the write goes through `EditNote`;
   a `library.*` event would describe the transport rather than the change. **This is one of the
   five names already recognised**, so precondition 2 does not reappear through this door either.

**In scope, as step 0 (§9 Q7, answered "yes"):** closing §4.1's two existing unversioned,
unaudited doors. §4.2a and §4.2b specify what rev 2 left as "add `expect_version`".

### 4.2a Step 0, specified: the token is required, the read must supply one, and the swap needs a lock

Rev 2's step 0 was one table row: *"Add `expect_version` to `LibraryContentRequest` +
`LibraryBinaryContentRequest`, thread it through `useLibraryFileEditor`, and add `logLibraryAudit`
to both handlers."* Three things are missing from that, and each is load-bearing.

**(i) N2 — the token is REQUIRED, and there is nothing on the read side to supply it.**
A save without a token returns **400**. That matches the agent path, which refuses an empty token
deliberately (FR-106), and it is the only version that actually closes the door: an optional token
leaves every caller that omits it on exactly today's unguarded path.

But **the read side carries no token at all**, so today there is nothing to send back:

| Read | What it returns | Token? |
|---|---|---|
| `GET /library/{ws}/content` → `LibraryContentResponse` | `{path, content, size, is_text, too_large, mime}` | **none**, and `handleLibraryContentGet` sets **no headers at all** |
| `GET /library/{ws}/download` (`downloadLibraryFile`) | the bytes | **none** |
| `PUT .../content` and `PUT .../content-binary` → `LibraryEntry` | `{name, path, is_dir, is_hidden, size, modified_at, mime, mount, is_knowledge_base, is_text_editable}` | **none** |

> **Decision: the version token is an HTTP response header, `ETag`, on BOTH reads and BOTH writes,
> and the request carries it in the body as `expect_version`.** A header is the only mechanism that
> reaches all three consumers with one change: the JSON reader (`getLibraryContent`), the
> **byte-stream** reader (`downloadLibraryFile`, which returns no JSON body to put a field in), and
> the write responses. `pkg/gateway` already has an `ETag` precedent on `/providers/catalog`
> (quoted strong SHA-256, with `If-None-Match` handling), and OpenAPI declares response headers
> natively, so this is contract-expressible under Constraint #8 rather than an undeclared
> side-channel. The **request** side stays a body field rather than `If-Match`, so that a missing
> token is a schema-visible 400 rather than a silently-absent header.
>
> **Three things this decision left unspecified, all decided in revision 4.**
>
> **(a) The token's VALUE — founder ruling N5.** It is the **existing** knowledge token:
> `pkg/knowledge/version.go`'s `ComputeVersionToken` / `ReadNoteVersion`, named here by symbol
> because revision 3 named neither anywhere (`grep -c` returned 0 in this document and in the spec),
> which left an implementer with no pointer and a pull toward size + modification time — the
> derivation `version.go`'s own header spends four paragraphs refusing, and which `NoteVersion`
> marks *"carried for display … never the decision."* **No back-compat is owed:** `rest_library.go`
> has zero token code today. For a file outside every knowledge base, the same computation over the
> same bytes; `ReadNoteVersion` needs a `*Collection` and the streaming half is unexported, so a
> thin exported sibling is **named work**, not an assumption — one definition of "changed", never
> two.
>
> **(b) There is nothing in the response BODY to hash on the door that matters.**
> `library.ContentResult` omits `Content` **by design** for a binary file and for a text file over
> `MaxContentBytes`, and `handleLibraryContentGet` builds its response from that struct. A token
> derived from `ReadContent` would therefore be the hash of an **empty string, identical for every
> binary file in the workspace** — on the **PDF path, the one N2 explicitly refuses to exempt**.
> `getLibraryContent` MUST read the file's bytes itself to compute the header even when it returns
> no `content` field at all. `handleLibraryDownload` already holds the bytes; the JSON reader does
> not, and that read is new work.
>
> **(c) The header's SHAPE, and exactly where it lands.** The value is the RFC **quoted-strong**
> form (`ETag: "v1:…"`), matching the `/providers/catalog` precedent and matching what Go's
> `scanETag` can parse — it **silently ignores** an unquoted value. `expect_version` in a body is
> the **bare** token; a quoted one is rejected with **400**, never 409, because otherwise a client
> that forgot to strip the quotes gets a conflict indistinguishable from a real one, forever, on the
> PDF save path. And the header is set in **`handleLibraryDownload` only** — not in
> `applyLibraryByteHeaders` and not in `serveLibraryContent`, whose body is
> `applyLibraryByteHeaders(...)` then `http.ServeContent(...)`, and whose own doc comment says
> `ServeContent` is used for *"Range requests (audio and video seeking) and conditional GETs."*
> An `ETag` in the shared helper changes `If-None-Match` (304s begin) and `If-Range` (validates
> against the ETag rather than `Last-Modified`) for **every** caller, `serveLibraryPath` included.
>
> **(d) The mechanism by which a response header reaches SPA code does not exist yet.**
> `src/lib/api.ts::request<T>` — the SPA's single API entry point — returns the parsed body and
> discards `res.headers`; the one place headers are read sits inside a bespoke hand-rolled fetch for
> the providers catalogue, not inside `request<T>`. The PDF *loader* is fine (it holds a raw
> `Response`); `putLibraryContent` and `putLibraryContentBinary` are not. Named as work with a
> chosen option in the spec's EMB-007c, because an unchosen mechanism is how the test for this gets
> written against a stub that hands the component a header production cannot obtain.

**(ii) N2's hard case — the PDF-annotation save, which cannot be exempted.**
`putLibraryContentBinary` has exactly **one** non-test caller: `LibraryPdfPreview.tsx`'s
`handleSave`. It loads its document with a raw `fetch(libraryDownloadUrl(workspaceId, path))` — a
byte stream — and never calls `fetchLibraryContent`. **There is no read on its path that could
return a token**, so making the token mandatory breaks the annotated-PDF save with a 400 on merge.
Rev 2's own ambiguity note claimed *"the only caller is our own editor, so the breaking change
costs one line"*; that is the wrong caller.

> **The fix, specified rather than deferred:** `downloadLibraryFile`'s 200 gains the same `ETag`
> header, `LibraryPdfPreview`'s loader reads `res.headers.get('ETag')` off the `Response` it
> already holds and stores it beside the document bytes, and `handleSave` sends it as
> `expect_version`. Same origin, so no `Access-Control-Expose-Headers` question arises. After a
> successful save the handler's own `ETag` replaces the stored one, so a second annotation pass in
> the same session does not conflict with the first. `LibraryPdfPreview.test.tsx`'s existing save
> round-trip test breaks under this change and is named in the regression list rather than
> discovered by CI.

**(iii) A compare-and-swap with no lock is not a compare-and-swap (review finding C7).**
Rev 2 specified the token and said nothing about atomicity. `handleLibraryContentPut` takes no
lock of any kind: it validates, opens the root, calls `checkCreateName`, then
`root.WriteContent(rel, []byte(req.Content))`. A handler that hashes, compares to `expect_version`,
and writes has a window between the comparison and the write in which an agent's `EditNote`
completes — and that agent's write is then silently lost, **behind a check that returned 200.**

This is not an inference. `pkg/knowledge/version.go`'s own header states it:

> A compare-and-swap that reads the token, compares it and then writes is not atomic on its own:
> two Omnipus writers can both read the same token, both pass the comparison, and both write — and
> one write is lost, silently, which is the precise failure US-14 exists to prevent.

> **Decision: the comparison and the write happen inside `knowledge.WithNoteWriteLock`, with the
> same `NoteLockConfig` the agent path uses** — same `CollectionRoot`, same `LockDir`
> (`LockDirFor` under `$OMNIPUS_HOME`), same collection-relative `rel`. The lock key is
> `collectionRoot + "\x00" + path.Clean(rel)`, so **all three of those must match or the two
> writers take different locks and the guard is decorative.** For a Library file that is **not**
> inside a knowledge base there is no collection root and no agent path to race; the empty
> `CollectionRoot` is the documented degraded mode — the in-process striped mutex is still taken,
> the cross-process advisory lock is not — and that is stated rather than described as if it were
> the full guarantee.
>
> **The derivation this decision depends on does not exist, and revision 4 names it as work (M7).**
> The Library handler is given a **workspace**-relative path against the **workspace** root; the
> lock key needs a **collection** root and a **collection**-relative path. Nothing in
> `rest_library.go` converts one to the other — the only knowledge-base detection there is
> `annotateKnowledgeBaseEntries`, which answers "is this **directory entry** a knowledge base" via
> `knowledge.Detection.IsKnowledgeBase`. There is no "which collection encloses this file" helper
> anywhere in the tree. So the handler must walk the path's ancestors applying **the same rule
> `knowledge.Detect` uses** (not a second, hand-rolled marker test), and obtain the lock directory
> from `LockDirFor(home, collectionRoot)` rather than constructing it — a constructed path would put
> lock files inside the operator's vault, which `LockDirFor`'s own header explains at length is
> exactly what must not happen.
>
> **And where knowledge bases NEST, the innermost wins.** That is stated because it is the one input
> on which the two writers can silently pick different keys — which is the decorative-guard failure
> this whole decision exists to prevent, arriving through the derivation rather than through the
> lock. Dataset G7d and test 124 pin it.

### 4.2b N4: the actor, including under authentication bypass

Q1 settles the *form* of the actor; N4 settles the *value* and the bypass case.

| Case | Actor recorded |
|---|---|
| An authenticated person | **`user:<auth user id>`** — the stable authentication user id, never a display name (a rename breaks the log's continuity) and never an email (personal data in a file an operator may share) |
| `gateway.dev_mode_bypass` is on and no user can be identified | **`anonymous`** — the literal string, exactly that, lower-case, with no prefix and no colon |
| An agent | unchanged: the agent id, as today |

**N4 overrules the architect's recommendation of a 503.** The write is allowed and is recorded.

> **The consequence the founder accepted, stated without softening: an `anonymous` entry cannot
> answer "who".** It records that a human-shaped write happened, when, to which file, and what
> changed — and nothing about the person. It is therefore **not** equivalent to a `user:<id>`
> entry and must never be read as one.
>
> Two obligations follow from that, both mechanical:
>
> - **`anonymous` entries must be greppable as a class.** The actor is the literal token
>   `anonymous`, never `user:anonymous`, never an empty string, and never a placeholder id — so
>   `grep` on the audit file separates the two populations exactly, with no false positives from
>   a user whose id happens to be "anonymous" (impossible: every attributable actor carries the
>   `user:` prefix).
> - **An empty actor stays a refusal.** N4 permits an *unattributed* write, not an
>   *unattributable* one. A request that reaches the handler with neither an authenticated user
>   nor bypass active is still rejected — by `NewWriter`, per §4.2's precondition 1 — and §11.2
>   names a test that asserts the refusal happens **before** the file is touched.

### 4.2c C5: the record-field write goes through `RecordWriteRequest`, not `SetProperty`

Rev 2 said the handler *"calls the same `EditNote` + `SetProperty` path `knowledge_edit`'s
`op=set_property` uses"*. Revision 3 changes that, because `SetProperty` is a raw frontmatter
line-splicer and **ADR-068 already built a typed contract with the guards this surface needs**:

`RecordWriteRequest` requires a `version_token` on update, splices rather than reserialises,
validates against the record's schema naming the expected shape, and carries two prohibitions in
its own description:

> RELATIONS AND PERSON PROPERTIES ARE NOT WRITABLE HERE … Derived values are never written into
> frontmatter (D9, FR-046) — a request naming a derived property is rejected, not honoured.

Routing through `SetProperty` gives up all four. Worse, it gives them up on a surface where the
client **cannot compensate**, because — measured — a view cell carries no type information at all:

```yaml
# VaultFindCell, in full
required: [property, value]
properties:
  property: {type: string}
  value:    {type: string}   # "The rendered value" — always text
```

No declared type, no enum member list, no date/text discriminator, and **nothing marking a cell as
derived or as a relation**. So §4.6's "an editor is offered only for types the schema describes"
cannot be evaluated by the client, a dashboard would offer an editor for a derived or relation
property, and the write would land through the one path that does not refuse it. That is a
data-integrity regression against a rule ADR-068 established deliberately.

**Four contract facts this surface needs, none of which exists today** (all are §6 line items, and
all are prerequisites of step 5 rather than details inside it):

1. **A record-schema read operation.** `RecordSchema.yaml` and `VaultRecord.yaml` are declared in
   `openapi.yaml`'s `components` and referenced from **no path** — verified by grepping for
   `#/components/schemas/RecordSchema` and `…/VaultRecord` across the whole spec: zero usages. The
   entire ADR-068 typed-record layer is agent-tool-only on the wire.
2. **Cell metadata**: per cell, its declared type, its enum members where it has them, and two
   flags — `derived` and `relation`. Without these the client is guessing.
3. **A version token reachable per row.** `VaultFindRow` has `id`, `path`, `title`, `line`,
   `status`, `text`, `cells`, `joins`, `stale` — **no `version_token`**. Either the row schema
   gains one, or the SPA reads each record before each edit, which is N unbudgeted requests **and**
   a fresh TOCTOU window between the read and the write. The row field is the right answer.
4. **`RecordWriteRequest` wired to a path**, with **409 → `KnowledgeConflictError`** — which is
   itself referenced from no path today, so step 5 is its first wiring.

**And a limitation that is now an artefact of a choice, not of the platform.** Rev 2 recorded
"a list-valued property cannot be set this way" as inherent, citing `SetProperty`'s scalar-only
signature. It is not inherent: `SetPropertyList(key string, values []string)` and
`SetPropertyScalarChecked(key, value string)` both exist in `pkg/knowledge/knowledge_edit_list.go`.
Under `RecordWriteRequest` the question is decided by the record's schema, not by a Go signature.
**This ADR still offers no list editor** — a multi-value control is real UI work with no measured
demand — but it is now a scope decision with a way forward, rather than a wall.

### 4.3 What `knowledge_version_conflict` actually is, and what it implies

A correction: it is **not a tool**. It is the single permitted value of `code` in
`KnowledgeConflictError` — the typed 409 body for a refused write.

What it implies is a complete contract, already built and already shipped, that this feature
inherits rather than invents:

- The version token is **opaque** and computed over the file's **content**, not its modification
  time (FR-107). Clients must never parse it, order it, or construct one.
- Every read that a write may follow returns the current token; every write must send back the
  token it read.
- A conflict response names the path, the token the caller sent, and **the token the file now
  has** — the last of these exists specifically so a caller can re-read, merge and retry.

### 4.4 What the person sees on a conflict

On 409: the field reverts to the server's value, the row states *"this changed while you were
editing"*, and a Retry appears with the fresh value visible.

**Never retry automatically.** Resending the write with the server's `actual_version` is
literally "overwrite whatever changed" — the exact thing the token forbids, dressed as a
convenience. §11 names the test, because a future refactor will "helpfully" add one.

### 4.5 Two embeds of the same view, on the same page

Both must reflect a write. Both read through TanStack Query keyed on
`['library', ws, 'knowledge', 'view-result', collectionId, viewSlug]` — the key `BasePreview`
already uses — so invalidating on success updates every mounted embed.

The cost is real and must be scoped: a view result is an evaluation, not a file read.
**Invalidate only view-result queries, and only for the collection written to.** A blanket
invalidation re-evaluates every mounted view on the page for a one-field edit.

**Three other caches also go stale on a property write, and rev 1 named none of them.** A
frontmatter change can alter the note's own rendered body, its outline, and (if the property
holds a wikilink) its link graph:

> On success, additionally invalidate `libraryQueryKeys.content(ws, workspacePath)` and the
> outline and `links` graph queries **for the written note only**. Not collection-wide: those
> three are per-note keys and a collection-wide sweep would re-fetch every open note for a
> one-field edit.

### 4.6 Which fields are editable

Derived from the record's schema, never hand-configured — record schemas already declare field
types including `enum`, `text` and `date`. **The schema must reach the client for this to be
evaluable at all; see §4.2c, contract facts 1 and 2.**

| Declared type or property attribute | Editor |
|---|---|
| `enum` | dropdown of the declared values |
| `date` | date input |
| `text` | inline text |
| **`derived`** | **none.** ADR-068 D9/FR-046: a derived value is never written into frontmatter, and `RecordWriteRequest` rejects a request naming one. Offering an editor for it is offering a control the layer beneath refuses |
| **`relation` or a person property** | **none.** ADR-068 FR-045: these are modified through `RelationWriteRequest`'s three explicit verbs, because a read-then-write round trip silently replaces a relation list |
| a list-valued property | none — a scope decision, not a platform limit (§4.2c) |
| anything else the schema does not describe | read-only, with an "open the note" affordance |

Deriving beats configuring, and an editor the schema cannot describe is simply not offered.

> **The two new rows are the point of this table, and they must be asserted positively.** A test
> that only checks "no editor renders for a derived property" passes on a component that renders
> **no editors at all**. The fixture must carry a writable `enum` **in the same view**, assert
> that one *is* editable, and assert the derived and relation cells are not — in one test, so
> neither half can pass alone. §11.2 names it.

**Not editable: a record's title or path.** Renaming cascades to notes the caller did not name,
which belongs to `knowledge_restructure` by the blast-radius rule (§9 Q5 confirms).

---

## 5. Scope — every classifier kind gets a disposition, with no gaps

Rev 1's In list named nine things and omitted `html` and `other`, both of which its own D1 stage
3 routed through `classifyLibraryEntry`. That gap would have mounted an agent-authored HTML
frame inside the note reader. All eleven kinds (ten from the classifier plus `note`) are
enumerated here.

| Kind | Disposition | Step |
|---|---|---|
| `image` (incl. SVG) | `LibraryImagePreview`, `variant="inline"`, with `\|width` sizing | 1 |
| `pdf` | `LibraryPdfPreview`, `variant="inline"`, pooled worker | 1 |
| `base` | `BasePreview`, `variant="inline"` — named view, or first servable view with a caption | 2 |
| `markdown` | `note` transclusion (D5), **not** `LibraryMarkdownPreview`'s editor shell — **one level only (N1)** | 3 |
| `note` (synthetic) | transclusion of a note, heading or block (D5) — **one level only; the transcluded note's own embeds are links (N1)** | 3 |
| `video` | `LibraryVideoPreview`, `variant="inline"` — already its own exported module, no extraction needed | 6 |
| `audio` | `LibraryAudioPreview` — **requires extraction from `LibraryPreviewPane.tsx` into its own module first** (§2.4) | 6 |
| **`mermaid`** | **§2.2 link fallback, permanently — an embedded diagram FILE is out of scope (founder ruling N8, §15).** A ` ```mermaid ` fence inside a note is a **different mechanism** and is untouched: §2.1 measures 163 of them, they render through `kbMarkdownBase.tsx`'s `language === 'mermaid'` branch, and they keep working. What is refused is `![[chart.mmd]]`. Revision 4 of this row said the embed "routes to the mermaid renderer" in step 6; that expectation is **retired**, not merely unscheduled. The refusal is expressed in `inlineEmbedTreatment` (`knowledgeMarkdown.tsx`), which returns `link-only` for this kind with the ruling written beside it, and is held by `knowledgeMarkdown.diagramEmbed.test.tsx`. `classifyEmbedKind` deliberately still answers `mermaid` for a `.mmd` target — see §15 for why the refusal lives at the rendering decision rather than in the classifier. | — |
| `text` | §2.2 link fallback. A plain-text file inline is a wall of unstyled text with no reader affordance; no measured use. | — |
| **`html`** | **§2.2 link fallback, permanently.** Never framed inline. See §2.5's correction: mounting `LibraryHtmlFrame` in a note would put a token-minted isolation boundary carrying agent-generated HTML inside a reading surface that is not an isolation boundary. If inline HTML is genuinely wanted later, it is its own ADR with its own threat model, not a row in a dispatch table. | — |
| **`other`** | **§2.2 link fallback**, not a download card (D2(a)). Same for the `binary` and `too_large` fallbacks. | — |

Also **in**: image sizing (`\|400`), the inline ` ```base ` fence, PDF page fragments,
` ```query ` fences, mathematics (already works), and allow-listed YouTube.

> **The ` ```base ` fence is deferred from step 2 to step 6.** Rev 1 listed it in step 2 and
> specified nothing about it — not its body's grammar, not how it addresses a collection, not
> whether it names a `.base` file at all. Zero of the founder's 784 notes use it. Specifying a
> grammar nobody has written against is speculation; it moves to step 6 next to the ` ```query `
> fence, where the same reasoning already applies.

**Out, explicitly and permanently.** Every plugin format — Dataview, Excalidraw, Kanban,
Templater, Charts, ABC, Admonition — none of which is core Obsidian and none of which the vault
uses. **Canvas**, ruled out by the founder ("we will not support that at all"). **Embedded
diagram files** (`![[chart.mmd]]`), ruled out by the founder in N8 (§15) — *not* the ` ```mermaid `
fence, which is a different mechanism and stays. External images of any kind. Any external frame
host other than allow-listed YouTube. Inline framing of collection HTML.

---

## 6. Sequencing

The founder's proposed order is endorsed, with changes, all stated with reasons. Every step's
exit criteria are in §11.

**Contract-first work is the spine of this sequence.** Rev 2 sequenced **two** contract changes.
Revision 3 counts **seven**, and the ordering below is determined by them, because under
Constraint #8 no client or handler code may be written before its schema lands.

| # | Contract change | Step |
|---|---|---|
| **CW-1** | `expect_version` on `LibraryContentRequest` + `LibraryBinaryContentRequest`; an `ETag` response header declared on `getLibraryContent`, `downloadLibraryFile`, `putLibraryContent`, `putLibraryContentBinary`; a new `LibraryConflictError`; 400 + 409 on both writes | 0 |
| **CW-2** | `heading_found` (boolean), `block` (string) **and `unresolved_reason` (enum)** on `KnowledgeGraphEdge` | 1c |
| **CW-3** | The video allow-list on the settings/state payload the reader already fetches | 4 |
| **CW-4** | A record-schema read operation (`RecordSchema` / `VaultRecord` wired to a path) | 5 |
| **CW-5** | Cell metadata on `VaultFindCell`: declared type, enum members, `derived`, `relation` | 5 |
| **CW-6** | `version_token` on `VaultFindRow` | 5 |
| **CW-7** | `RecordWriteRequest` wired to a path, with 409 → `KnowledgeConflictError` (its first wiring) | 5 |

| Step | What | Blocked by | Why here |
|---|---|---|---|
| **0** | **CW-1**, then: require `expect_version` on both whole-file saves (400 if absent or empty, **N2**), 409 on stale with the current token, the compare-and-swap **inside `WithNoteWriteLock`** (§4.2a iii), `ETag` on both reads and both writes, `useLibraryFileEditor` threading the token, `LibraryPdfPreview` reading it off the download `Response` and sending it (§4.2a ii), and `logLibraryAudit` on both handlers | CW-1 | Closes the live, shipped data-loss path §4.1 documents. Placed first because every later step makes concurrent human/agent writes more likely while this door stands open. **Scope note:** this closes the two *content* doors; delete, upload and transfer stay version-free (§4.1) and §7.1's claim is scoped accordingly. |
| **1** | The resolver (D1) + prop widening and `inline` variant (D2a/b) + the **pooled PDF worker** (D2c) + `knowledge_edit op=embed` (D6) — covering images, SVG, sizing, PDF | — | The cheapest way to prove the resolver and the inline variant work, on kinds with no unknowns. It de-risks step 2. The PDF worker moves **up** to here because this step is what first allows more than one PDF on a page. |
| **1c** | **CW-2:** add `heading_found`, `block` **and `unresolved_reason`** to `KnowledgeGraphEdge.yaml`, populate all three in `rest_knowledge.go::knowledgeEdge`, run `scripts/gen-contracts.sh`, commit the generated diff atomically | — | Constraint #8. D1's edge key needs `block`; D4's "file found, heading not" needs `heading_found`; **D4's containment row and D6's read-time containment need `unresolved_reason`, without which the reader prints "nothing is named that" for a refused escaping path.** Rev 1 asserted the first two were already on the wire and sequenced nothing; rev 2 sequenced two and missed the third. Can run in parallel with step 1's SPA work; **must land before step 2's marker copy is trusted, and before any US-2 marker is written.** |
| **2** | Base view embeds, read-only, including the label→slug mapping and path conversion (D7) | 1, 1c | The whole point of the exercise — 75 uses and an entire dashboards folder |
| **2m** | **A one-shot migration report**: which of the 75 existing embeds resolve to a real view and which do not, run before step 2 ships | 1c | A single query over data that already exists. If D7's label match fails for some of the 75, the founder learns it from a report, not from a dashboard full of markers. |
| **3** | Note / heading / block transclusion, **one level only (N1)** — a transcluded note's own embeds render as links | 1c | Zero vault uses today, but it is the most fundamental Obsidian idiom and a vault written in Obsidian will grow them. **No cycle apparatus:** with one level there is no second level to loop through, so the cycle set, the depth cap and their tests are deleted rather than deferred (D5). |
| **4** | **CW-3**, then the YouTube allow-list (D9) | CW-3, Q9 | Self-contained; touches the SPA's security policy and must not be entangled with the renderer work. Ships with the **four** CSP artefact changes — including `adr-067-knowledge-base-and-preview-spec.md` §10.7, whose literal line is `TestSpaServedWithCSP`'s oracle — and the audit amendment, in the same PR. |
| **5** | **CW-4, CW-5, CW-6, CW-7**, then inline record editing (D8, §4), in **both** the ordinary view surface and dashboard embeds at once | **CW-4…CW-7**, Q1, N4's actor (§4.2b), the `NewWriter` construction (§4.2 precondition 1), and Q8 | Last, because it is the only new write path, and because shipping it in one surface first guarantees the two diverge. **Four contract changes, not one:** rev 2 listed a request and a result schema and assumed the rest of ADR-068's record layer was already reachable from the wire. It is not — `RecordSchema`, `VaultRecord` and `RecordWriteRequest` are declared in `components` and referenced from **no path**. |
| **6** | Audio (**with the `LibraryAudioPreview` extraction first**), video, PDF page fragments, ` ```query ` and ` ```base ` fences | — | In scope, no evidenced use. Build when a real need appears rather than on speculation. **Nested embeds are no longer listed here** — under N1 they are out, not deferred; D5 records what bringing them back would require. **Mermaid embeds are no longer listed here either** — under N8 (§15) they are out, not deferred; §5's `mermaid` row records the refusal and the scope boundary against the ` ```mermaid ` fence. |

**"Ship steps 0–5 and stop" is a legitimate outcome.** §2.1 measures zero uses for everything in
step 6. That option is recorded in §8 rather than left implicit.

**Out of scope, and newly named as such in revision 3:** making printing complete (N3), and closing
the delete / upload / transfer doors (§4.1). Both are real work; neither is in this sequence, and
neither should be discovered as missing from a claim made elsewhere in this document.

---

## 7. Consequences

### 7.1 Gained

- Dashboards render as data. The knowledge base stops being materially less useful than the
  Obsidian original for the founder's primary operating view.
- One renderer per file kind, everywhere — pane or inline — enforced by the compiler rather than
  by discipline, for the eight kinds where the widening holds.
- A broken embed is visible to a reader **and** legible to an agent, so an agent summarising a
  dashboard can say "three of these modules are broken" instead of quietly summarising a subset —
  and an *uncheckable* embed says so rather than claiming the file does not exist.
- Obsidian's silent degradation past a nesting cap is not inherited, because there is no nesting
  to degrade: one level, and the second level is a link (N1).
- **Whole-file content writes** from every surface share one lock, one compare-and-swap and one
  audit record, once step 0 lands.

  > **That claim is scoped, deliberately, and revision 3 stops stating it unqualified.** Rev 2
  > said *"every knowledge-base write from every surface"*. Measured, `deleteLibraryEntry`,
  > `uploadLibraryFiles` and the transfer modes remain version-free after step 0 (§4.1), and all
  > three can destroy or replace a note an agent is mid-write on. They are audited, so the loss is
  > traceable; it is not preventable. Closing them is separate, sized work.

### 7.2 Costs and new obligations

- `spaContentSecurityPolicy` gains a third-party frame host. It is one host, on one directive,
  and it is the application's own policy — a change that must be reviewed as security work, with
  a test pinning the SPA's allow-list to the Go policy string, **three test/doc artefacts
  amended in the same PR**, and the Chromium journey re-run (D9).
- A second write door into the knowledge base exists after step 5. It is guarded identically to
  the first, but it is a second door and must be reviewed as one. (§4.1's two Library doors are a
  separate count, and are unguarded until step 0.)
- ~~**`pkg/audit/events.go` must change**~~ — **withdrawn in revision 3. This cost does not exist.**
  The five `knowledge.*` names are already in `IsValidEventName` (in `audit.go`, not `events.go`)
  and already have a test with a negative control. Rev 2 inherited the claim from a stale comment
  in `pkg/knowledge/audit.go`, which is itself the only remaining task here: correct it.
- **The new REST write door sits OUTSIDE the tool-policy system entirely.** §7.2's Constraint #6
  paragraph below is about *agent* granularity; this is a different and larger point. An operator
  who has set `knowledge_edit: deny` for every agent on their install still gets a **human** write
  door into the knowledge base after step 5, and **no tool policy can close it** — tool policy
  governs tools, and this is a REST endpoint. That is not an argument against the endpoint (the
  Library already has whole-file write doors on the same footing), but it is an ungoverned surface
  and is recorded as one rather than presented as tidiness. The zero-line tool-policy diff is
  genuinely zero (§7.2) partly *because* of this, which is worth seeing clearly rather than
  counting as a saving.
- **An `anonymous` audit entry cannot answer "who" (N4).** Under authentication bypass a human
  record edit is written and recorded with the literal actor `anonymous`. Those entries are a
  distinct population from `user:<id>` ones, must be greppable as such, and must never be read as
  attributable. §4.2b states the obligations.
- Every preview renderer acquires a layout variant, and every renderer's non-happy states now
  need testing twice.
- **Inline kind classification can disagree with the pane's** for an extensionless or
  wrongly-extensioned image, video or text file (D2a). Three named cases, none fabricated, one
  test pinning them.
- The pooled PDF worker introduces lifecycle the per-document worker did not have: a lease count
  that leaks means a worker that never terminates, and a poisoned worker that is not evicted
  means every later PDF fails.
- `KnowledgeGraphEdge` gains two fields, so every consumer of that schema regenerates.
- **An accepted consequence of D6's op choice (Constraint #6's flip side):** an operator cannot
  grant note editing while withholding embed authoring, because both resolve on the single policy
  name `knowledge_edit`. That is the deliberate FR-070c trade — the alternative is the
  near-synonymous-tools pattern this codebase just retired — but it is a real reduction in
  policy granularity and is recorded rather than left implicit.

### 7.3 Explicitly worse than before

- A dashboard is now expensive in a way a list of links was not. Fifteen modules are fifteen view
  evaluations, mitigated by lazy mounting, a 60-second staleness window, base-views dedup by
  path, and D3's four-in-flight bound — but not eliminated.
- Embedded views inherit WL-1 in whichever form Q8 chooses. This ADR does not fix WL-1 and must
  not paper over it.
- **Browser find does not reach unmounted embeds, and neither does printing (N3, D3).** Rev 2 said
  *"Print does, by mounting everything first"*; that promise is withdrawn. A printed dashboard
  contains only the modules already mounted. The reader states this in one line while any embed is
  unmounted; that statement is the whole of the mitigation.

### 7.4 Residual

- **The Library's two unversioned, unaudited whole-file write endpoints are LIVE today**
  (§4.1): `PUT /library/{ws}/content` (`pkg/gateway/rest_library.go::handleLibraryContentPut`)
  and `PUT /library/{ws}/content-binary` (`::handleLibraryContentBinaryPut`), reached from
  `src/components/library/preview/useLibraryFileEditor.ts`'s mutation. A human editing a note
  body in the Library can silently clobber an agent's concurrent write and leaves no audit
  record. This ADR does not close it unless Q7 is answered "yes" (step 0). It is recorded here
  because inline editing makes concurrent writes materially more likely while it stands open.
- Windows has no cross-process file locking anywhere in the file-store family. A concurrent
  inline edit and agent write from two processes against the same home directory is protected
  in-process only. This is the pre-existing ADR-054 §5.1 position; this ADR does not change it.
- The graph's `truncated` flag is never set for `kind: 'links'` on this branch (D1 stage 2,
  producer 3). The client honours it anyway. If a future change adds a bound to that branch
  without a client that reads the flag, the false-statement defect returns.
- **Three more unversioned Library doors stay open after step 0** — `deleteLibraryEntry`,
  `uploadLibraryFiles`, and the rename/move/copy transfer modes (§4.1). All are audited; none is
  version-checked. Closing them is separate, sized work.
- **Printing is incomplete and stays incomplete (N3).** A dashboard prints what has mounted. The
  reader says so; nothing fixes it in this work.
- **Under authentication bypass a record edit is recorded as `anonymous` (N4)** — a real,
  accepted reduction in attribution, not a gap. §4.2b.
- **`heading_found` is false for every `.base` target and every block reference**, by construction
  (§2.3). A reader that consults it outside its meaningful range renders "no such heading" across
  a whole dashboard. The rule is stated in D4; the constraint lives in the graph builder and would
  be better expressed in `KnowledgeGraphEdge.yaml`'s own description so a future consumer cannot
  re-learn it the hard way.
- **`ResolvedLink.BlockID` is parsed and never read** anywhere in the tree today — no resolution
  logic, no test, no wire type. CW-2 is its first consumer, so there is no existing behaviour to
  regress and equally no existing test to lean on: its first assertion is the one this work
  writes.

---

## 8. Alternatives rejected

**A second, inline-only set of renderers.** Rejected: it is the hand-copied-and-drifted failure
`knowledgeMarkdown.tsx`'s header documents, aimed at the surface the founder reads daily.

**A wrapper element around the existing pane renderers.** Rejected on mechanics, not taste:
`h-full` and `absolute inset-0` resolve *against* the wrapper, and `flex-1` collapses inside a
non-flex parent. `LibraryPdfPreview`'s 13 occurrences make this unfixable from outside.

**A hard cap on embeds per page.** Rejected by the founder (D-A). A forty-module dashboard is a
legitimate document; the correct bound is on *work*, not on *count* — D3 now sets one.

**A single shared PDF worker port (rev 1's D2(c)).** Rejected in this revision: it names an API
the code does not use, it makes one document's worker failure reject every other mounted
document, and it has no poisoned-port recovery. Replaced by a bounded pool with visible queueing.

**A download card for embeds of unsupported kinds.** Rejected: `LibraryDownloadCard` needs `size`
and `modified_at`, which a graph edge does not carry, and inventing them is the cardinal error D1
refuses. A download card inside a paragraph is also the wrong product answer.

**Framing `![[x.html]]` inline.** Rejected: a note reader is not an isolation boundary and must
not become one.

**A new `knowledge_embed` tool.** Rejected — see D6. It contradicts FR-070c, requires a ceiling
entry plus a change to every per-agent seed under Constraint #6, and re-creates the
near-synonymous-tools pattern `knowledge_edit` exists to have ended.

**Reusing `PUT .../content` for field edits.** Rejected — see §4.2. Whole-file overwrite, no
version token, no audit record.

**Copying Obsidian's depth counter — and, in revision 3, building a cycle detector at all.**
Rev 2 rejected the counter in favour of a real cycle set plus a visible cap. **Revision 3 rejects
both**, because N1 removes the input: with one level of transclusion there is no second level to
loop through, so a cycle cannot be constructed and a depth cannot be exceeded. Shipping a detector,
four tests and a success criterion against an input the product cannot generate is the *feature*
equivalent of a test whose subject has been deleted — it passes forever and proves nothing. D5
records what would have to exist first if nesting is ever wanted.

**Refusing an inline record edit under authentication bypass with 503.** This was the architect's
recommendation and the founder **overruled** it (N4). The write is allowed and audited as
`anonymous`. Recorded here because the losing argument is still the honest description of the
cost: an `anonymous` entry cannot answer "who", and §4.2b says so rather than implying the log is
complete.

**Fabricating `mime` and `is_text_editable` on the resolver's file reference.** Rejected — see
D2(a). The same principle that excluded `size` and `modified_at` excludes these; rev 2 invoked it
in D1 and abandoned it in D2(a) without noticing.

**Routing the record-field write through `SetProperty`.** Rejected in revision 3 — see §4.2c. It
is a raw frontmatter splicer with none of `RecordWriteRequest`'s guards, on a surface where the
client cannot tell a derived or relation property from an ordinary one and therefore cannot
compensate.

**Holding the browser's `beforeprint` event open until every embed settles.** Rejected — see D3
and N3. The event is synchronous; the guarantee cannot be delivered, and one that appears to work
on a warm cache is worse than none.

**Copying Obsidian's write-back of view state.** Rejected by the founder (D-D), and independently
by the capability reference, which notes users hide the toolbar with CSS to escape it.

**Ship steps 0–5 and stop.** *Not* rejected — recorded as a legitimate outcome. §2.1 measures
zero uses for every kind in step 6 (audio, video, PDF page fragments, both fence forms, nested
transclusion). Building them is speculation; the evidence supports stopping at step 5 and adding
a kind when a real note needs it.

---

## 9. Open questions for the founder

These are **not** answered here. Each carries a recommendation and the reason for it. Q1–Q6 are
unchanged from revision 1; Q7–Q9 are new, raised by findings that touch a ratified decision or a
scope boundary the architect may not move alone.

**Q1 — Who is recorded as the author of a human's inline record edit? — ANSWERED (and extended by
N4).**
Every knowledge-base mutation is audited with an actor of `{AgentID, WorkspaceID}` (FR-090). A
person editing a field in the SPA has no agent id.
*Answer:* **`user:<auth user id>`** — the stable authentication user id, never a display name and
never an email. Under authentication bypass with no identifiable user, **N4** answers the case Q1
left open: the write is **allowed** and recorded as the literal actor **`anonymous`**, not refused
with 503. §4.2b specifies both, and the consequence the founder accepted.
*Correction to rev 2's supporting claim:* it said *"`EditNoteRequest` requires both `Audit` and
`Actor`."* It requires **neither** — a nil `Audit` writes the file and records nothing. The
enforcement point is `pkg/knowledge/audit.go`'s `NewWriter`, and the handler must go through it.
See §4.2 precondition 1.

**Q2 — Does an embedded base view show any controls at all?**
D-D settles that filter and sort changes stay local. It does not settle whether those controls
are *visible* inside an embed. *Recommendation:* no toolbar in an embed by default — fifteen
modules with fifteen toolbars is unreadable, and Obsidian users hide it — with an explicit
opt-in modifier if you want one on a particular embed. This is a product call, not a technical
one.

**Q3 — Two imported views that share a display label.**
*Recommendation:* refuse at write time and render unresolved-with-reason at read time, naming
both. Silently picking one is how the original slug-collision defect behaved. But if you would
rather it always resolve to the first deterministically, that is a defensible product choice and
it is yours.

**Q4 — May an embed name a view the server has marked unservable?**
The server already reports both the flag and the reason. *Recommendation:* render the server's
reason in place (which D-B requires regardless), and separately decide whether `op=embed` should
*refuse to write* an embed of a currently-unservable view. Refusing is stricter but can block a
legitimate "I will fix that view next" workflow.

**Q5 — Confirm that a record's title and path are not inline-editable.**
*Recommendation:* not editable. Renaming cascades to notes the caller did not name, which is
`knowledge_restructure`'s territory under the blast-radius rule.

**Q6 — The PDF worker pool's ceiling.** *(Narrowed by this revision.)* Rev 1 asked whether a
shared port is acceptable if PDF.js does not support it. D2(c) now specifies a pool with visible
queueing that is correct either way, so the remaining question is only the ceiling: **N = 2, or
N = 1?** *Recommendation:* N = 2, falling to 1 if the vendored build turns out not to support
concurrent documents on one worker. §2.1 counts one PDF in the entire vault, so either is
adequate today; the visible queue is what matters, not the number.

**Q7 — Is closing the Library's existing unversioned, unaudited note-save path a precondition of
inline editing, or separate work?** *(New — raised by review finding C5.)*
The defect is live today (§4.1, §7.4): a human editing a note body in the Library saves
last-write-wins with no conflict check and no audit record, while every other library mutation is
audited. This ADR did not create it and does not depend on it, but step 5 makes concurrent
human/agent writes materially more likely while it stands open.
*Recommendation: **in scope, as step 0.*** Three reasons. (i) It is a shipped data-loss path in
the surface this ADR is about, and Constraint #7's "fix everything, no excuses" applies to
pre-existing failures by name. (ii) It is small and contract-shaped — two schema fields, one
thread-through in `useLibraryFileEditor`, two `logLibraryAudit` calls — days, not weeks.
(iii) Deferring it means shipping a feature whose stated safety argument ("one lock, one
compare-and-swap, one audit record") is untrue of the bigger door in the same product.
*If you would rather not:* it becomes a tracked issue with a target date under Constraint #7, and
this ADR's §7.4 stands as the record. What is **not** acceptable is shipping step 5 while
describing the knowledge base as having one guarded write path.

**Q8 — In an embedded base view, do links resolve against the note reader's graph or the view's
loaded rows?** *(New — raised by review finding M4; touches D-D.)*
The two answers produce visibly different link colouring for the same view in two places (D8's
table). Rev 1 asserted both and could not have implemented either.
*Recommendation:* thread the **reader's** resolver. A dashboard embed exists to be read in the
note, an honest `unresolved` beats a permanent `unknown`, and a click should open in the reader
the person is already in. The cost — the same view looks different in the pane and in a note —
should be **stated in the UI**, not hidden. *The alternative* (keep the view's own resolver) is
defensible if you value "a view looks the same everywhere" more than "a link tells the truth";
say so and D8 will be rewritten to match, including its WL-1 paragraph.

**Q9 — Should the YouTube frame host be an operator config key, or a build constant?**
*(New — raised by review finding m6; touches D-C.)*
D-C ratifies *what* may be framed. It does not settle whether an operator may decline it. As
specified, every install — including a sovereign, no-telemetry deployment — carries
`https://www.youtube-nocookie.com` in the application's own CSP, with no way to turn it off and
no rollback if the click-to-play placeholder regresses.
*Recommendation:* a config key, **default on**. The SPA reads the same allow-list, so the
existing SPA↔Go equality test extends to cover it. Default off would mean the founder's own
YouTube embeds do not render on a fresh install, which is the wrong default for the person who
asked for the feature. If you would rather it be a build constant, say so and §7.2 records "no
per-install control" as an accepted cost.

---

## 10. What was verified, and what was not

### 10.1 Read in full on `def10b90e`

`docs/internal/obsidian-embed-capability-reference.md`,
`docs/internal/defect-list-wikilink-rendering-2026-09-08.md` (WL-5),
`src/components/library/preview/knowledgeMarkdown.tsx`,
`src/components/library/preview/libraryPreviewKind.ts`,
`src/components/library/preview/BasePreview.tsx`,
`src/components/library/preview/viewparts/ViewPartsRenderer.tsx`,
`src/components/library/preview/viewparts/ViewCellLink.tsx`,
`pkg/knowledge/authoring_tools.go`, `pkg/knowledge/knowledge_edit.go`,
`pkg/knowledge/knowledge_read.go`, `pkg/knowledge/links.go`,
`pkg/gateway/library_isolation_policy.go`, `pkg/gateway/embed.go`,
`pkg/config/defaults.go`, `pkg/coreagent/core.go`, and the relevant contract schemas.

**Added in revision 2** (each was a file rev 1 reasoned about without reading):
`src/components/library/LibraryPreviewPane.tsx`,
`src/components/library/preview/LibraryDownloadCard.tsx`,
`src/components/library/preview/LibraryPdfPreview.tsx`,
`src/components/library/preview/useLibraryFileEditor.ts`,
`src/components/library/knowledge/KnowledgeNoteView.tsx`,
`src/components/library/knowledge/KnowledgeBacklinks.tsx`,
`pkg/gateway/rest_knowledge.go`, `pkg/gateway/rest_library.go`, `pkg/knowledge/audit.go`,
`pkg/knowledge/author.go`, `contracts/components/schemas/KnowledgeGraphResponse.yaml`,
`KnowledgeGraphSkip.yaml`, `KnowledgeGraphEdge.yaml`, `LibraryContentRequest.yaml`.

### 10.2 Five corrections to the brief this ADR was given — all re-confirmed by the review

1. The embed branch is **gated on the image extensions**; a PDF, sound file, video or `.base`
   embed renders as a link with an "embed shown as a link" badge, **not** as a broken image (§2.2).
2. `knowledge_set_property` and `knowledge_link` are **not live tools** (§D6).
3. A record-field write endpoint does not exist, but `PUT .../content` **does** — an unversioned
   whole-file overwrite (§4.1).
4. `knowledge_version_conflict` is **an error code, not a tool** (§4.3).
5. `knowledge_read` does **not** currently distinguish an embed from a link (§D4).

### 10.3 Three claims revision 1 recorded as "measured" that were FALSE

Recorded here rather than quietly corrected, because the pattern matters more than the three
facts: all three were **read** rather than **run**, and rev 1's §10 explicitly said nothing was
executed while its prose said "the compiler proves it".

| Rev 1 claim | Reality on `def10b90e` |
|---|---|
| *"every renderer reads only `entry.path` or `entry.name` — never `size`, `modified_at` …"* (D2a) | `LibraryDownloadCard` reads both `entry.size` and `entry.modified_at`, and takes no `workspaceId`. |
| *"All of this is already on the wire … `heading_found`"* (§2.3) | `heading_found` and `BlockID` exist in Go and are **not** projected by `knowledgeEdge()` and **not** in `KnowledgeGraphEdge.yaml`. |
| *"Every preview renderer takes the same prop pair `{ workspaceId, entry }`"* (§2.4) | True of 3 of 10. Four more are module-private functions inside `LibraryPreviewPane.tsx` with no module of their own. |

### 10.3a Four claims revision 2 recorded as measured that were ALSO false

Found by the spec review, verified independently before being accepted here. The pattern is the
same one §10.3 names — **read, not run** — and it survived a revision written specifically to
correct that pattern, which is why it is recorded as its own section rather than folded above.

| Rev 2 claim | Reality on `def10b90e` |
|---|---|
| *"`EditNoteRequest` requires both `Audit AuthorAudit` and `Actor AuthorActor`"* (§4.2 precondition 1) | It requires **neither**. `Audit` is an interface guarded `if a.sink != nil` on every emit, so nil writes the file and records nothing; `Actor.AgentID` is copied in unchecked; `EditNote`'s only precondition is `if c == nil`. The enforcement lives in `NewWriter`. |
| *"`pkg/audit/events.go` must be extended with the `knowledge.*` event names"* (§4.2 precondition 2, §7.2, §6, §11.1) | **Already done, before this ADR existed.** Five names are registered in `IsValidEventName` — which is in `audit.go`, not `events.go` — and `pkg/knowledge/audit_event_names_test.go` already asserts them with a negative control. The claim came from a **stale comment** in `pkg/knowledge/audit.go`. |
| *"a skipped note … contributes no edges while the graph reports success"* (D1 stage 2 producer 1) | Conflates the containing note with the target. A **walk-level** skip removes the target from `NewNoteIndex(walk.Files)`, so a link to it yields a **matching `unresolved` edge**; a **scan-level** skip happens after that list is captured, so the target stays indexed and the link **resolves normally**. Neither produces zero edges. |
| *"a five-deep nest is five sequential fetches"* (D5), two bullets after *"nested embeds inside a transclusion are OUT"* | A direct self-contradiction. The spec inherited **both halves** and built a cycle detector, a depth cap, four tests and a success criterion on the half that is false. N1 deletes it. |

**The lesson worth carrying forward, because it is not "check your facts".** Every one of these
four survived a revision whose explicit purpose was to catch exactly this class. Three of them are
*inherited* claims — a comment, a prior revision, an earlier sentence in the same section — and
none was re-measured because each was already written down. **A claim being present in the
document is not evidence for it.** The spec's own false-green register now carries this as a named
category (*"a test whose subject already exists"*), which is where it belongs: the same failure,
one layer down.

### 10.4 Measured directly, and re-confirmed

The pane-shape counts (`LibraryImagePreview` 2, `BasePreview` 5, `LibraryPdfPreview` 13); the two
CSP strings and where each is applied; `spaContentSecurityPolicy` ending
`frame-src 'self'; … frame-ancestors 'none'`; `img-src 'self' data: blob:`; that mathematics and
SVG already work; that `KB_REHYPE_PLUGINS` has no raw-HTML plugin; `BasePreview`'s two staleness
figures (60 s view-result, 10 s base-views) and its base-views key being the `.base` path;
`getKnowledgeBaseViews`'s `path` being workspace-relative while `to_path` is collection-relative;
`unknownArgs` being called once before the `op` dispatch; `heading`'s existing `append_section`
meaning; `logLibraryAudit`'s six call sites and its absence from both content-put handlers;
`putLibraryContent`'s call site carrying no version; `resp.Truncated` being set only in the
`neighbourhood` branch; `resp.Skipped` being populated for every kind.

### 10.5 Not verified, and marked as such

- Whether one `PDFWorker` can back several concurrent `getDocument` calls in the **vendored**
  build (D2(c), `[UNVERIFIED]`). The pool design is correct either way.
- The exact rendered strings of `knowledge_read`'s `LINKS` section for an embed (D4,
  `[UNVERIFIED]`) — `renderReadLinks` was read; no output was captured.
- **Nothing in this ADR was executed. No build, no test run, no browser check.** Two commands
  would materially raise confidence and are named as pre-ratification obligations rather than
  suggestions: `npm run typecheck` against a stub `LibraryFileRef` (catches the D2(a) class of
  error), and step 2m's migration report over the 75 existing embeds.

---

## 11. Acceptance criteria and named tests

Rev 1 had no acceptance criteria and named three tests. A step with no exit condition ships when
someone says it works, which for this feature is exactly the judgement §2.2 says a human cannot
make by looking.

### 11.1 Per-step exit criteria

| Step | Exits when |
|---|---|
| 0 | Both content endpoints reject a **missing or empty** `expect_version` with 400 (N2) and a **stale** one with a typed 409 carrying the current token; both emit an audit event **on a default install, read back from the sink**; the comparison and the write happen inside one `WithNoteWriteLock` and a test proves an interleaved `EditNote` cannot be lost; the PDF-annotation save obtains its token from the download `ETag` and still succeeds; the Library editor surfaces the conflict **without auto-retrying**. |
| 1 | Each of `image`, `pdf` mounts its renderer inline **and a test asserts which renderer mounted per kind** (§2.2 — there is no broken-image symptom to look for); `npm run typecheck` passes with `LibraryFileRef` narrowed to `name`/`path`; **a test mounts the same file inline and in the pane and asserts the classifier agreed**, plus the three named divergences (D2a); two PDFs on one page both render. |
| 1c | `make verify-contracts` passes with `heading_found`, `block` **and `unresolved_reason`** on `KnowledgeGraphEdge`, generated diffs committed atomically with the spec change, **and each of the three is asserted in both of its states by a handler-level test** — contract verification says nothing about whether a handler populates a field. |
| 2 | All 75 vault embeds resolve to their named view, or step 2m's report names each exception and the founder has seen it; a collection mounted **below** the workspace root resolves correctly. |
| 3 | A note transcludes another note, a heading and a block; **a transcluded note's own embed renders as a link with a reason and NOT as a could-not-be-checked marker**; a note that transcludes itself renders once and terminates. **No cycle or depth assertion exists, because no cycle or depth mechanism exists (N1).** |
| 4 | The SPA and Go allow-lists are asserted equal **by parsing the served header**; the default install's header matches the ADR-067 spec doc's literal line **and** an emptied-config install's differs, both in one test; the Chromium journey passes with a YouTube embed; the non-allow-listed control still violates; A6 still passes unchanged; the CSP audit **and the ADR-067 spec doc §10.7** are amended in the same PR. |
| 5 | CW-4…CW-7 have landed; a derived and a relation cell get **no** editor while a writable enum in the same fixture **does**; `user:<id>` appears in the audit record on real boot wiring; **an `anonymous` actor appears for a bypassed write and is distinguishable from `user:<id>` by grep** (N4); a request with neither is refused **before the file is touched**; a 409 reverts the field and offers Retry with **no** automatic resend. |

### 11.2 Tests this ADR requires by name

| Area | The test |
|---|---|
| Renderer routing | Per kind, assert which renderer mounted — including that `html`, `other` and `text` mount the **§2.2 link fallback** and not a renderer |
| **Classifier agreement (new, D2a)** | Mount the **same file** in the pane and inline; assert the classifier returned the same kind. Plus the three named divergences asserted explicitly: an extensionless image, an extensionless video, and an extensionless text file |
| Resolver state model | Feed a graph response with a `skipped` entry covering the embed's target and assert the marker says **indeterminate**, not "nothing is named X"; separately feed `truncated: true` and assert the same |
| **Resolver state model (new, D1 stage 2)** | Feed an edge whose `resolution` is **`unresolved`** *and* a `skipped` entry naming that target; assert **indeterminate**, not the missing-file marker. This is the dominant real case and rev 2 routed it to the wrong state |
| **Resolver state model (new, D1 stage 2)** | Feed `resolution: 'exact_path'` with the matching node's `exists: false`; assert **indeterminate**. Then the reverse. A resolver that reads only the edge passes neither |
| Resolver state model | Feed a **failed** graph query and assert one page-level error, not N per-embed markers, and not N reserved placeholders |
| **Resolver state model (new, D1 stage 2)** | Feed the out-of-scope-collection answer — **200, zero edges, zero skips** — with fifteen embeds; assert **one** page-level statement, not fifteen "no reason available" markers |
| **Containment (new, D4/CW-2)** | Two edges, both `unresolved`: one for absence, one with `unresolved_reason: outside_root`. Assert **different** markers, and that the second contains no path segment. A single-edge test passes on a component that renders one marker for everything |
| **`heading_found` range (new, §2.3)** | Extend the found/not-found pair with a `.base` target carrying a view fragment and a markdown target carrying a block anchor; assert the "no such heading" marker is **absent** for both |
| Edge matching | A fixture with two embeds of one `.base` under different fragments; assert each resolves to its own view |
| Path conversion | A collection mounted **below** the workspace root — a root-mounted fixture passes either way and proves nothing |
| Cross-variant | Per renderer, both variants with identical props: equal state test-ids and text; container `className` exempt — **plus a control asserting the container class differs**, or the whole suite passes on a component that ignores `variant` |
| PDF pool | Two embeds where one document's worker fails: assert the healthy one still renders, the failed worker is evicted, and a third mount gets a fresh one |
| `unknownArgs` per-op | Assert `op=link` with `width` is **refused**, not ignored; and `op=embed` with `body` is refused; **and that each argument is still accepted by the op that reads it** — a sweep that refuses everything passes the refusal half alone |
| **Step 0 lock (new, §4.2a iii)** | Start an `EditNote` between the handler's version comparison and its write (via a test seam); assert exactly one write survives and the loser gets a 409. Sequential stale-token tests pass against a lock-free implementation |
| **Step 0 binary door (new, §4.2a ii)** | The PDF-annotation save obtains its token from the download response's `ETag` and completes; `LibraryPdfPreview.test.tsx`'s existing save round-trip is updated in the same change |
| Conflict path | Assert **no** automatic retry after a 409 — one request, still one after the longest retry window, exactly one more on press |
| CSP | The allow-list equality test **parsing the served header**; default-vs-emptied in one test body; the new Chromium journey; the non-allow-listed control; A6 unchanged |
| **SVG isolation (new)** | An SVG containing a `<script>`: assert it renders inside `<img>`, that the script's side effect did **not** occur, and that no `<svg>` element appears in the note's DOM. Rev 2 spent a paragraph on this property and named no test for it |
| **Raw frame markup (new)** | A note whose source contains `<iframe src="https://…">` produces **no** `<iframe>` in the rendered output. The property rests entirely on `KB_REHYPE_PLUGINS` having no raw-HTML plugin — true today and one line away from not being |
| **Agent surface containment (new, D4)** | A containment-refused embed's `LINKS` line carries the reason (`— outside the knowledge base`), asserted against the **real renderer output**, not against the sample in this document |
| **Editor gating (new, §4.6)** | One fixture, one test: a writable `enum` cell **is** editable, a `derived` cell is **not**, a `relation` cell is **not**. Split across tests, the negative halves pass on a component that renders no editors |
| **Anonymous actor (new, N4)** | A bypassed write is **accepted** and records the literal actor `anonymous`; an authenticated write records `user:<id>`; a request with neither is **refused before the file is touched**. All three in one test, so "it refused everything" and "it accepted everything" both fail |
| Regression | The §2.2 "embed shown as a link" fallback still fires for the kinds that reach it — this work reroutes past a tested behaviour |
| ~~Cycle detection~~ | **Deleted (N1).** There is no cycle apparatus to test. A cycle test would have to construct a render tree production cannot construct |
| ~~Print~~ | **Deleted (N3).** Replaced by: the reader's one-line notice is present while any embed is unmounted, absent when all are mounted, and **never present on a note with no embeds** |

---

## 12. What the review changed, and what it did not

The review's 28 findings, and this revision's disposition. Three are rejected with reasons; a
finding being rejected does not mean it was wrong to raise.

| Finding | Disposition | Where |
|---|---|---|
| C1 `LibraryDownloadCard` breaks the widening | **Accepted.** Card excluded from widening; unsupported kinds fall back to the link treatment. | D2(a), §5 |
| C2 state model not exhaustive | **Accepted**, with one measured correction: of the three producers named, the *skipped note* and *key mismatch* are live today; a truncated `links` query is **not reachable** on this branch because `resp.Truncated` is set only in the `neighbourhood` branch. The state is added regardless, and the client honours `truncated` regardless. | D1 stage 2, §7.4 |
| C3 CSP artefacts unnamed | **Accepted.** All three named, re-measurement required, `frame-ancestors` coupling addressed. | D9 |
| C4 `html`/`other` reopen a closed surface | **Accepted.** All eleven kinds enumerated; `html` never framed inline. | §2.5, §5 |
| C5 unversioned/unaudited save is live | **Accepted** and strengthened to a current defect, with `content-binary` (m3) folded in. Whether to fix it here is Q7 with a recommendation. | §4.1, §7.4, §6 step 0, Q7 |
| M1 `heading_found`/`block` not on the wire | **Accepted.** §2.3 corrected; contract work sequenced as step 1c. | §2.3, §6 |
| M2 edge key ambiguous | **Accepted.** Full key stated, plus zero/multiple behaviour. **Partial correction:** `from_path` is *not* needed in the key — the `kind: 'links'` query is already scoped to the note. That implicit scoping is stated because it breaks for nested transclusion. | D1 stage 2 |
| M3 path coordinate mismatch | **Accepted.** **Correction to the fix:** the collection root is not obtained from `KnowledgeBaseInfo` directly — `KnowledgeNoteView` already derives it and already exposes `toWorkspacePath`. The resolver takes that, not a new derivation. | D1 stage 4, D7 |
| M4 D2(b)↔D8 contradiction | **Accepted.** Resolver props declared independent of `variant`; the product question goes to the founder. | D2(b), D8, Q8 |
| M5 PDF inference names the wrong API | **Accepted.** Pool with visible ceiling is now primary; error attribution and poisoned-worker eviction specified. | D2(c), Q6 |
| M6 `heading` collision + global `unknownArgs` | **Accepted.** Renamed to `target_heading`/`target_block`; per-op whitelist made part of the work. | D6, §11 |
| M7 no REST audit path | **Accepted** — **two corrections since (see §13, M1 and m3).** ~~Three~~ **two** named preconditions on step 5: the third was deleted by M1 because its work was already done, and the file is `pkg/audit/audit.go`, not ~~`pkg/audit/events.go`~~ — `IsValidEventName` lives there. | §4.2, §6 |
| M8 transclusion fetch unspecified | **Accepted.** Fetch, key, slicing side named; nested embeds explicitly out of step 3. | D5 |
| M9 lazy mount has no failure/bound/print story | **Accepted** — ~~`beforeprint`~~ **SUPERSEDED BY N3 (see §13, A-9).** Retry policy and the four-in-flight bound stand; the `beforeprint` mechanism and the print guarantee it served are **deleted**, and the browser-find limitation is now stated on screen together with printing (EMB-071). This row is a *current disposition*, not a history entry, so a reader looking up "how was M9 resolved?" must not be told `beforeprint`. | D3 |
| M10 `unknown` conflates loading with failure | **Accepted.** Split into `loading` and `graph_unavailable`. | D1 stage 2 |
| m1 prop-pair claim false | **Accepted.** §2.4 rewritten with measured signatures; extraction added to step 6. | §2.4, §6 |
| m2 two staleness figures | **Accepted.** Both stated. **Correction:** the base-views key already dedupes by `.base` path, so D7 adds no N-fold cost — it adds one call per distinct base file. | D3, D7 |
| m3 two write endpoints | **Accepted**, folded into C5. | §4.1 |
| m4 base-first-view and ` ```base ` fence undecided | **Accepted.** First-view decided with a caption; the fence deferred to step 6 rather than specified on speculation. | D7, §5 |
| m5 Q1 blocks step 5 but the table does not say so | **Accepted.** "Blocked by" column added. | §6 |
| m6 no rollback / per-install control | **Accepted for D9** (Q9). **Rejected for the rest:** a renderer that either routes or does not needs no flag, and adding one per step would add configuration surface with no failure mode it addresses. | D9, Q9, §7.2 |
| m7 D4 sample unverified; view label overloads heading | **Accepted.** Sample marked `[UNVERIFIED]` against the real renderer; the `.base` case now renders `view "X"` rather than `#X`. | D4 |
| m8 "byte-identical" unscoped | **Accepted.** Assertion specified; container class exempt. | D2(b) |
| O1 Constraint #6 flip side | **Accepted.** Recorded as an accepted consequence. | §7.2 |
| O2 keep the `[INFERRED]` discipline; run typecheck | **Accepted.** Typecheck is a pre-ratification obligation. | D2(a), §10.5 |
| O3 D-D corollary | **Accepted.** Recorded. | D8 |
| O4 promote the "test each kind mounts" line | **Accepted.** It is a step-1 exit criterion. | §11.1 |
| O5 "ship 1–5 and stop" | **Accepted.** Recorded as a considered option. | §8, §6 |
| Review §7 Q1 (Obsidian-authored escaping embed) | **Accepted.** Read-time containment stated; the marker does not echo the escaping path. | D6 |
| Review §7 Q3 (embed inside a code fence) | **Rejected as a decision, recorded as a known gap.** The parser's fence handling for embeds was **not verified** and rev 2 will not assert a behaviour it did not read. It is an implementation obligation with a named test (`![[x]]` inside a ` ``` ` fence mounts nothing), not an architectural decision. |
| Review §7 Q2 (whose permissions evaluate a view) | **Rejected as out of scope, on a measured basis.** No route in `pkg/gateway/rest_library.go` or `rest_knowledge.go` performs any per-user or per-role check — grepped for `RequireRole` / `RoleAdmin` / `IsAdmin` / role comparisons: zero hits. Every authenticated caller reaches the same files with the gateway's own filesystem access, so a view evaluates identically whoever opens the note, and there is no second answer for this ADR to choose between. `[UNVERIFIED]` whether any *future* per-user Library authorization is planned — if it lands, whose principal a view evaluates under becomes that work's question, not this one's, and this row is the record that it was considered and found vacuous today rather than overlooked. |
| Review §7 Q5 (per-workspace rate limit on view evaluations) | **Rejected as out of scope, mitigated in substance.** No per-workspace evaluation rate limit exists to interact with; D3's four-in-flight bound is the work bound D-A asked for. If one is added later, D3's queue indicator is the surface it shows through. |
| Review §7 Q4 (`note` reserved height) | **Accepted.** Fixed three-line placeholder, one reflow accepted. | D3 |
| Review §7 Q6 (migration report for the 75) | **Accepted.** Step 2m. | §6 |
| Review §7 Q7 (stale caches after a property write) | **Accepted.** Content, outline and links invalidated for the written note only. | §4.5 |

---

## 13. What the SPEC review changed (revision 3)

> **Requirement-id namespaces in this document — read before following any `FR-` citation
> (revision 4, resolving M12 of the round-2 review).** Two populations of `FR-` ids exist and this
> document contains both, which is the collision the spec's `FR-` → `EMB-` rename was made to end.
>
> - **`FR-nnn` cited below and in §2–§11 belongs to ADR-067 or ADR-068** — `FR-090`, `FR-106`,
>   `FR-107`, `FR-043`, `FR-046` and the rest. These are **live**, and several are cited **inside
>   production code comments** (`pkg/knowledge/author.go`, `author_test.go`). Follow them to their
>   own ADR.
> - **The implementation spec's requirements are `EMB-nnn` and always were, from revision 2
>   onwards.** Where §13 below quotes a finding as it was raised — against `FR-001`, `FR-091` — the
>   live id is given alongside it. **This document contains no live `FR-` id of the spec's.**
>
> Revision 3 of this ADR left `FR-001` and `FR-091` in §13 reading as current, so a reader met
> `FR-001` (spec, retired), `FR-090` (ADR-067, live, cited in Go), `FR-091` (spec, retired) and
> `FR-106` (ADR-067, live, cited in Go) in one document with nothing to tell them apart.

The [implementation spec's review](../specs/adr-083-embedded-content-spec-review.md) returned
7 CRITICAL, 13 MAJOR, 9 MINOR and 3 OBSERVATION findings against the spec. Several of them are
this ADR's defects, inherited. This table records only the ones that changed **this document**;
the spec's own revision log covers the rest.

Every finding below was **re-verified against the code before being accepted**. Where the review's
description needed correcting, the correction is stated rather than quietly applied.

| Finding | Disposition | Where |
|---|---|---|
| **C1** — cycle detection has no reachable input; D5 contradicts itself | **Accepted, and resolved by REMOVING the feature (founder ruling N1).** Not by making cycles reachable, which was the review's recommended Option A. The self-contradicting bullet is deleted; the cycle set, the depth cap, their four tests and their success criterion are deleted; D5 records what nesting would require if it is ever wanted, so this is not re-derived. | §2.8, D5, §5, §6, §8, §11 |
| **C2** — a skipped target still produces "nothing is named X" | **Accepted, with the review's own analysis refined.** The review's walk/scan split is correct and verified. Two refinements: `unreadable` appears at **both** levels (a directory that cannot be listed is walk-level and takes its files with it; a file that cannot be opened is scan-level), and `not_addressable` is a *gateway* mapping of the Go `irregular` reason, not a `pkg/knowledge` constant. The cross-check is now specified on the `unresolved` state with an explicit matching rule. | D1 stage 2, D4 |
| **C3** — the wire cannot distinguish "no such file" from "outside the root" | **Accepted.** Verified: `KnowledgeGraphEdge` has no `reason` property, is `additionalProperties: false`, and its `resolution` description collapses the two cases in words. Added as **CW-2's third field**, `unresolved_reason`, sequenced into step 1c ahead of every US-2 marker. | §2.3, D4, §6 |
| **C4** — `LibraryFileRef` cannot be produced | **Accepted.** Verified: `classifyLibraryEntry` reads `entry.mime` and `entry.is_text_editable`; there is no `getLibraryEntry` operation. Resolved by **narrowing the type to `name`/`path` and classifying by extension only**, with the three divergences from the pane enumerated and a test that pins them. The review's fetch-the-entry and extend-the-edge options are rejected with reasons. | D2(a), §11.2 |
| **C5** — US-10 is not implementable; CW-3 omits what it needs | **Accepted in full.** Verified: `VaultFindCell` is `{property, value}`; `VaultFindRow` has no version token; `RecordSchema`, `VaultRecord`, `RecordWriteRequest` and `KnowledgeConflictError` are referenced from **no path**; `SetPropertyList` and `SetPropertyScalarChecked` exist. The write is re-routed through `RecordWriteRequest`'s semantics and **four** contract changes (CW-4…CW-7) are sequenced. | §4.2c, §4.6, §6 |
| **C6** — CW-1 is incomplete; **EMB-001** (cited as `FR-001` when the finding was raised) breaks PDF annotation saving | **Accepted, and made harder by founder ruling N2.** Verified: `LibraryContentResponse` has no version field and its handler sets no headers; both PUTs return `LibraryEntry`, which has none either; `LibraryPdfPreview.tsx`'s `handleSave` is the only production caller of the binary door and loads via a raw `fetch(libraryDownloadUrl(...))`. **No exemption.** Resolved with an `ETag` header on both reads and both writes — the only mechanism that reaches a byte-stream reader — with the repo's `/providers/catalog` ETag as precedent. | §4.2a, §6 |
| **C7** — step 0's compare-and-swap has no lock | **Accepted.** Verified: `handleLibraryContentPut` takes no lock and calls `root.WriteContent` directly, and `pkg/knowledge/version.go`'s own header states the failure verbatim. Resolved by running the comparison and the write inside `WithNoteWriteLock` with the **same** `CollectionRoot`, `LockDir` and `rel` the agent path uses — all three must match or the two writers take different locks. | §4.2a iii |
| **M1** — **EMB-091** (cited as `FR-091` when the finding was raised) and its test describe work already done | **Accepted, and it is the finding that matters most here.** Independently verified. The step-5 precondition is deleted, the stale comment is named as the single remaining task, and the pattern is recorded as its own section (§10.3a) because it survived a revision written to catch exactly it. | §4.2, §7.2, §10.3a, §6, §11.1 |
| **M2** — the CSP change breaks the existing test's oracle | **Accepted.** Verified: `TestSpaServedWithCSP` reads its oracle from `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` §10.7 and requires **exactly one** literal policy line there. Both that document and the derived `spaPdfWorkerContentSecurityPolicy` are added to D9's artefact table, and the config-key-vs-byte-oracle collision is resolved rather than left to CI. | D9 |
| **M3** — `heading_found` is always false for `.base` and block targets | **Accepted.** Verified at its single assignment site: the guard is `res.Heading != ""` and `g.headings` is markdown-only. Stated as a range constraint in §2.3 and D4, with the two extra test rows named. | §2.3, D4, §11.2 |
| **M8** — `EditNoteRequest` does not enforce the actor | **Accepted.** Verified. Rev 2's claim is withdrawn in place, and `NewWriter` is named as the enforcement point the handler must go through. | §4.2, §10.3a |
| **M9** — the agent surface echoes the escaping path | **Accepted as a finding; the asymmetry is KEPT and justified rather than closed.** Verified: `renderReadLinks` prints `l.Form` verbatim. An agent reading a note already has the note's source, so redacting the `LINKS` projection alone would be theatre. What was unacceptable was leaving it **unstated**, which rev 2 did. Now stated, with a test asserting the agent line carries the containment *reason*. | D4 |
| **M11** — three more unversioned doors | **Accepted.** Verified: `logLibraryAudit` fires for delete, upload, mkdir, create_vault, rename and the transfer modes; none of them is version-checked. §7.1's claim is now scoped to whole-file content writes and the three doors are named in §4.1 and §7.4. | §4.1, §7.1, §7.4 |
| **M13** — the model drops today's `nodes[].exists` guard | **Accepted.** Verified in `KnowledgeNoteView.tsx`, in both `resolveWikilink` and `resolveEmbedUrl`. Added to the resolution procedure with an explicit disagreement rule. | D1 stage 2 |
| **A-9** (print) | **Accepted, and resolved by dropping the promise (founder ruling N3).** Not by scoping it to an application-owned print control, which was the review's recommendation — that control is not built in this work either. | §2.8, D3, §7.3, §11 |
| **A-6** (bypassed write) | **Accepted as a question; the architect's answer is OVERRULED (founder ruling N4).** 503 is rejected; the write is allowed and audited as the literal actor `anonymous`. The accepted consequence — an `anonymous` entry cannot answer "who" — is recorded in §7.2 with two mechanical obligations, not as a footnote. | §2.8, §4.2b, §7.2, §8, Q1 |
| **O1** — Constraint #6's zero-line diff | **Accepted as a reframing, not a correction.** The zero-line claim is independently verified and stands: exactly eight `knowledge_*` ceiling keys, all `allow`; `knowledge_link`/`knowledge_set_property` absent from the ceiling, every per-agent seed and the registry; `AuthoringTools()` has zero production callers. What is now stated is *why* part of it is zero — **the new REST write door sits outside the tool-policy system entirely**, so an operator who denies `knowledge_edit` to every agent still has an ungoverned human write door. | §7.2 |
| **O2** — `BlockID` is parsed and never read | **Accepted.** Recorded as a residual: CW-2 is its first consumer, so there is no existing behaviour to regress and no existing test to lean on. | §7.4 |
| **m2** — line-number citations | **Accepted, and applied to the spec rather than here.** This ADR cites `file::symbol` throughout; the spec's Symbols table did not and now does. CLAUDE.md's own rule is that line numbers in these files go stale within days. | (spec) |
| **m3** — `IsValidEventName` is in `audit.go`, not `events.go` | **Accepted.** Corrected everywhere it appeared. | §4.2, §7.2, §10.3a |
| **m6** — an out-of-scope collection returns 200 with empty edges and empty skips | **Accepted, and promoted.** Verified in `rest_knowledge.go`. It is not a minor: it is a **live producer** of N "no reason available" markers where one page-level statement is correct, and it is now a named producer in D1 stage 2 and a row in D4's table. | D1 stage 2, D4 |
| **m8** — the print requirement is stated unconditionally | **Superseded by N3.** The requirement is deleted rather than split. | §2.8, D3 |
| Review §5's missing register category | **Accepted, and it is the sharpest thing in the review.** *"A test that cannot fail because its subject already exists"* is now a category in the spec's false-green register — inside the document that defines the register, which is where its absence caused the failure. | (spec) |

---

## 14. The second spec review, and three further founder rulings (revision 4)

The implementation spec's [second adversarial review](../specs/adr-083-embedded-content-spec-review-round2.md)
returned 5 CRITICAL, 12 MAJOR, 8 MINOR and 4 OBSERVATION findings, BLOCK. Most of them are the
spec's, and the spec's own revision 3 resolves them. This section records **the three founder
rulings** and **the findings that changed this document**.

### 14.1 Three founder rulings, all settled

| # | Ruling | What follows |
|---|---|---|
| **N5** | **The two save doors share the EXISTING knowledge version token.** `pkg/knowledge/version.go`'s `ComputeVersionToken` / `ReadNoteVersion`, named by symbol. | **A-11 is CLOSED, not open.** No back-compat is owed: `rest_library.go` contains **zero** token code today, so there is no second definition to migrate. Neither this ADR nor the spec named either function in revision 3 — `grep -c` returned **0** in both — which left a step-0 implementer with no pointer and a strong pull toward size + modification time, the derivation `version.go`'s own header spends four paragraphs refusing. Specified in the spec's **EMB-007a**. |
| **N6** | **Agents DO see the raw path** for an out-of-base embed, together with its reason. | **Redaction on the agent surface is rejected as theatre.** An agent reading a note already receives the note's body, so hiding the path in the `LINKS` projection alone removes the agent's ability to say **which** embed is broken without removing its access to the path. §13's M9 disposition — keep the asymmetry, state it, test the reason — is **ratified**, not merely tolerated. The spec's **EMB-024** now says so, so a later change cannot read it as an oversight and "fix" it. |
| **N7** | **Record editing stays in this ADR**, as the last step, with its four contract changes (CW-4…CW-7). | Not split into a separate ADR. §6's sequencing and §4.2c stand as written. |

### 14.2 Findings that changed this document

| Finding | Disposition | Where |
|---|---|---|
| **C1(r2)** — the C2 fix cannot match an unreadable-**directory** skip | **Accepted, and it is the sharpest finding of the round.** §13's own C2 row records the refinement — *"a directory that cannot be listed is walk-level and takes its files with it"* — and the matching rule that shipped could not express it: `WalkContained`'s `ReadDir` error branch records the skip under **the directory's** path (`RelPath: cur.rel`) while the rule compared path equality or basename, so no file beneath it ever matched. Every note under an unreadable directory still got *"Nothing in this knowledge base is named X"* — the exact sentence D-B exists to prevent, in the dominant walk-level shape. Resolved with a third, **ancestor-prefix** clause on path-segment boundaries, plus an explicit carve-out for `scanSkippedDirNames`, which is deliberately **not** reported in `Skipped`. | D1 stage 2, D4; spec EMB-021 |
| **C2(r2)** — the fix was reader-only; the agent surface had none | **Accepted. This contradicted D-B**, which requires both surfaces, and it contradicted the spec's own US-2 narrative, which promises agents the same honesty. `knowledge_read` never consults the graph answer: it projects `ResolvedLink` into `ReadLink` and prints `"  %s (unresolved) %s"`, so a walk-skipped target reached the agent as plainly unresolved and would be summarised as missing — on the surface whose output gets pasted into a report. Resolved as a **requirement**, not a residual: the spec's **EMB-021a**, with `ReadLink` gaining a skip-derived reason and test 120 asserting all three clauses plus a near-miss. | D4; spec EMB-021a |
| **C3(r2)** — the token had no specified value, and none to compute on the binary door | **Accepted in all three parts, and settled by N5 above.** (a) A-11 closed. (b) The functions are now named by symbol. (c) The gap that mattered: `library.ContentResult` omits `Content` by design for a binary file **and** for a text file over `MaxContentBytes`, and `handleLibraryContentGet` builds its response from that struct — so a token derived from `ReadContent` would be the hash of an **empty string, identical for every binary file**, on the **PDF door, the one N2 refuses to exempt**. `getLibraryContent` must read the bytes itself; `handleLibraryDownload` already holds them. | §4.2a; spec EMB-007a |
| **M5** — the ETag lands inside `http.ServeContent`, and its shape was unspecified | **Accepted, and both halves are decided rather than left to CI.** `serveLibraryContent` is `applyLibraryByteHeaders` then `http.ServeContent`, whose own doc comment says it is used for *"Range requests … and conditional GETs"* — so an `ETag` in the shared helper changes `If-None-Match` and `If-Range` for **every** caller, `serveLibraryPath` included. **Decision: the header is set in `handleLibraryDownload` only.** And the value is **quoted-strong on the wire, bare in `expect_version`** — Go's `scanETag` silently ignores an unquoted header, and a client that forgets to strip the quotes would otherwise get a 409 indistinguishable from a real conflict, forever, on the PDF save path. | §4.2a; spec EMB-007b |
| **M6** — `src/lib/api.ts::request` discards response headers | **Accepted.** The mechanism §4.2a's ETag decision depends on **does not exist**: the SPA's single API entry point returns the parsed body only, and the one place headers are read sits inside a bespoke fetch, not inside `request<T>`. The PDF *loader* is fine (raw `fetch`); the *save* is not. Named as work with a chosen option. | spec EMB-007c |
| **M7** — the lock's collection-root derivation has no named helper and none exists | **Accepted.** §4.2a(iii)'s decision is right and re-verified — `noteLockKey(collectionRoot, rel)`, and `handleLibraryContentPut` takes no lock of any kind. What was missing is the conversion from a **workspace**-relative path to a **collection** root: `annotateKnowledgeBaseEntries` only answers "is this *directory entry* a knowledge base". Specified as work, with the nested-collection rule decided (**innermost wins**) because that is precisely the input two writers can silently disagree about — and disagreement is the decorative-guard failure this decision exists to prevent. | §4.2a iii; spec EMB-006a |
| **M12** — this ADR re-created the `FR-` namespace collision, and two §12 dispositions were stale | **Accepted.** A namespace note now heads §13; §13's C6 and M1 rows give the live `EMB-` ids alongside the ids the findings were raised under; and §12's M9 and M7 rows are amended **in place** with what superseded them, because §12 is read as current dispositions, not as history. | §12, §13 |
| **C4(r2)**, **C5(r2)**, **M1–M4**, **M8–M11**, **M13**, minors | **Accepted; resolved in the spec.** They are failures of the spec's own instruments — a completeness invariant asserted twice and kept zero times, an X7 category defined and never run, a finding answered with a sentence claiming it had been answered — and the spec's revision 3 executes each check and records the result with the command that produced it. | (spec) |
| **O3** — the ADR/spec pair is otherwise in unusually good agreement | **Recorded.** CW-1…CW-7 match §6 row for row; N1/N3/EMB-091's deletions are symmetric in both; §13 is honest about which recommendations were **rejected**. The only divergences found were mechanical, and both are closed above. | — |

### 14.3 The pattern, named for the third time

Two reviews in a row found the same shape, and it is worth stating as a rule rather than as an
observation, because stating it as an observation is what failed twice: **a rule this document
states is not thereby applied to this document.** Revision 2 created the X7 category — *"run every
new test before implementing; if it passes, it is not this work's test"* — explained it better than
anything else in the file, and never ran it; running it found eleven tests that pass on the current
tree. Revision 2 promoted register completeness from an observation to a measured success criterion
and broke it in the same edit with the three tests it had just added. Revision 2 answered a finding
about a missing carve-out with a sentence asserting the carve-out had been added to two
requirements, and it was in neither.

The countermeasure in revision 3 is not a fourth statement of the rule. It is that **each of the
three checks now names the command that re-measures it**, and the two that can be mechanised are
mechanised: SC-026 is a `diff` of two extracted number columns, SC-030 and SC-031 are `grep`s.
A count that is wrong because it was derived rather than measured belongs in the same category as a
test that cannot fail: both are claims whose subject was never examined.

---

## 15. Founder ruling N8 — embedded diagram files are out of scope (revision 5)

**The ruling, in the founder's words (2026-09-11):** *"diagrams / obsydian diagrams are out of
scope and will not be a feature of omnipus KBs"*.

| # | Ruling | What follows |
|---|---|---|
| **N8** | **An embedded diagram FILE — `![[chart.mmd]]` — is not supported, and will not be.** | §5's `mermaid` row is now a **permanent link fallback**, alongside `html`, `text` and `other`, not a step-6 deferral. It is removed from §6's step 6. This **retires revision 4's expectation** that the embed "routes to the mermaid renderer"; a reader must not be able to mistake today's link fallback for an unfinished feature. |

### 15.1 The scope boundary, stated so it cannot drift

The ruling is about **embedded diagram files**. It is **not** about ` ```mermaid ` **fenced code
blocks**, and the two are different mechanisms end to end:

| | Embedded diagram file | Fenced diagram block |
|---|---|---|
| Written as | `![[chart.mmd]]` | ` ```mermaid ` … ` ``` ` |
| Reaches the renderer via | the wikilink/embed pipeline (`remarkKbWikilinks` → `inlineEmbedTreatment`) | the markdown `code` slot (`kbMarkdownBase.tsx`, `language === 'mermaid'`) |
| Uses in the founder's vault (§2.1) | **0** | **163** |
| Disposition | **Refused (N8)** | **Untouched — keeps working** |

Fenced blocks were already recorded in §2.1 as *"Already works — a fenced code block, a different
mechanism entirely"*, and §2.5 lists them among the things that need no work. N8 does not disturb
either statement. A change that removed fenced mermaid rendering would silently delete 163 working
diagrams from the founder's own knowledge base; `knowledgeMarkdown.diagramEmbed.test.tsx` renders a
refused `.mmd` embed and a working fence **in the same note** so that neither half can be satisfied
by breaking the other.

The **standalone Library preview** of a `.mmd` file (`LibraryMermaidPreview`, reached through
`classifyLibraryEntry`) is likewise a different surface and is **not** in scope of this ruling.

### 15.2 Why the refusal lives in `inlineEmbedTreatment`, not in `classifyEmbedKind`

Two expressions were available. The one chosen keeps `classifyEmbedKind` answering `mermaid` for a
`.mmd` target and refuses it at `inlineEmbedTreatment`, for three reasons:

1. **The classifier's contract is a fact, not a policy.** Its own doc comment says it *"Classifies
   an embed's WRITTEN target by extension alone"*. A `.mmd` file **is** a mermaid file. Making it
   answer `other` would encode a product decision as a false statement about the file, and would
   put `.mmd` in the same bucket as `archive.zip` and `README`.
2. **It would create a silent divergence.** `classifyEmbedKind`'s header states it is a deliberate
   sibling of `classifyLibraryEntry` *"sharing the same extension tables"*. `classifyLibraryEntry`
   must keep returning `mermaid` (§15.1's third surface), so deleting the row here would make the
   two disagree for a reason no comment at either site would explain.
3. **Discoverability is the whole requirement.** `inlineEmbedTreatment` is the single exhaustive
   switch that decides how each kind renders inline — the exact place a future implementer looks
   before "finishing" the feature. An explicit `case 'mermaid':` carrying the ruling cannot be read
   as an oversight; a missing row in a classifier can.

The prior comment at that site called the gap a **DEFERRAL** and told the next reader how to
complete it (*"Whoever picks it up adds `'block-mount'` here"*). That instruction is now removed —
it was the opposite of the ruling.

### 15.3 The spec rows this ruling made stale — now corrected

`docs/internal/specs/adr-083-embedded-content-spec.md` carried the step-6 expectation in two
places — its test table (test **82**, `audio-video-mermaid-pdf-page`) and its C8 coverage row
(*"`.mmd` | Deferred | diagram renderer, inline (step 6)"*). Both were stale under N8. They were
flagged here rather than edited at the time, because the spec was out of scope for the change that
recorded this ruling.

**Both are now fixed** (2026-09-12): `mermaid` is struck from test 82's name, and C8 is re-stated
as a permanent refusal citing §15, with the fenced-block distinction spelled out in the row itself
so the two mechanisms cannot be collapsed by a later reader.

A **third** staleness was found while making those edits and is also fixed: the spec cited **no
test at all** for the N8 refusal, even though `knowledgeMarkdown.diagramEmbed.test.tsx` was already
written and passing. It is now **test 126**. A spec that records a ruling but not its enforcement
invites someone to "implement" the deferred feature the stale row still promised.

`docs/internal/uat/library-uat-plan.md` needed no expectation changed — its file-type table is
about the **standalone Library preview pane**, which §15.1's last line already places outside this
ruling — but a three-case table was added beneath it, because a tester reading "Mermaid file
`.mmd` → rendered diagram" next to a ruling titled "embedded diagram files are out of scope" can
reasonably conclude the two contradict each other. They do not; they are different surfaces.
