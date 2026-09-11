# Feature Specification: Embedded content inside knowledge-base notes (ADR-083)

**Created**: 2026-09-09
**Revised**: 2026-09-09 — **revision 3**, after [a second adversarial review](adr-083-embedded-content-spec-review-round2.md) returned BLOCK (5 CRITICAL, 12 MAJOR, 8 MINOR, 4 OBSERVATION) and the founder issued three further rulings (below). Revision 2 followed [the first review](adr-083-embedded-content-spec-review.md) (7 CRITICAL, 13 MAJOR, 9 MINOR, 3 OBSERVATION) and founder rulings N1–N4.
**Status**: Draft
**Input**: [`ADR-083 — Embedded content inside knowledge-base notes`](../architecture/ADR-083-embedded-content-in-knowledge-base-notes.md) (**revision 3**), its [ADR-level review](../architecture/ADR-083-embedded-content-in-knowledge-base-notes-review.md) (28 findings, BLOCK), [this spec's own review](adr-083-embedded-content-spec-review.md) (32 findings, BLOCK), and [`false-green-patterns.md`](../false-green-patterns.md).
**Worktree / branch**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate`, `integrate/library-improvements-v0.1.1`, HEAD `def10b90e`. Every repo-relative path in this spec is relative to that worktree root.
**Founder ratification**: D-A, D-B, D-C, D-D (ADR §2.6), Q1–Q9 (ADR §9) **and N1–N4 (ADR §2.8)** are settled. This spec implements them; it does not re-open them.

> **Requirement ids in this document are `EMB-xxx`, not `FR-xxx`.** They were renamed in revision 2.
> ADR-067's `FR-090`, `FR-106` and `FR-107` are cited **inside production code comments**
> (`pkg/knowledge/author.go`, `author_test.go`), and this spec's own numbering collided with them
> on a surface directly adjacent to that code. An implementer reading `FR-106` in a Go file and
> `FR-106` in this spec would have found two unrelated requirements. `EMB-` ends that.

### What revision 3 changed, in one paragraph

Revision 3 adds **no new capability**. It executes three checks this document had already specified
and never run, and repairs what running them found. **The X7 sweep** — *"run every new test before
implementing; if it passes, it is not this work's test"* — was defined in revision 2 and applied to
nothing; run by hand against all 113 rows it found **eleven** tests that pass on the current tree,
every one of them inside a check whose text says it must fail, including test **117**, which asserted
a property of a fixture it wrote itself and so could not fail in **any** implementation. **The
register's completeness invariant** was promoted to a success criterion and immediately broken by the
three tests added in the same edit; membership now lives in one coverage table keyed by test number
and is measured by a `diff`, not by scanning prose. **Every `Traces to`** was checked for the first
time to see whether it *resolves*: one pointed at a scenario the print deletion had removed, five
were off by one, and one acceptance scenario had a test and two dataset rows and no scenario at all.
Beyond the instruments, three substantive gaps closed: the skip cross-check now matches an
**ancestor directory**, which is the shape `WalkContained` actually produces and the one the C2
correction missed; the same honesty is now owed and tested on the **agent** surface, not the reader
alone; and the version token has a **named value** — the existing `ComputeVersionToken` /
`ReadNoteVersion` — including on the binary door, where the response body it would otherwise be
hashed from is omitted by design.

### The three founder rulings settled in revision 3

1. **The two doors share the EXISTING knowledge token.** `pkg/knowledge/version.go`'s
   `ComputeVersionToken` / `ReadNoteVersion`, named by symbol. `rest_library.go` has **zero** token
   code today, so nothing needs back-compat. **A-11 is CLOSED**, not open. See EMB-007a.
2. **Agents DO see the raw path** for an out-of-base embed, plus its reason. Redaction on the agent
   surface was **rejected as theatre**: the agent already receives the note's body, so hiding the
   path only in the links list removes its ability to say *which* embed is broken without removing
   its access to the path. EMB-024's asymmetry is **ratified**, not tolerated.
3. **Record editing stays in this ADR**, as the last step, with its four contract changes
   (CW-4…CW-7). It is not split out.

### What revision 2 changed, in one paragraph

Three capabilities were **removed**, not repaired: cycle detection (its input is unreachable once
nested embeds are out — N1), print completeness (the browser cannot deliver it — N3), and an audit
event-name task that was **already done before this spec was written**. Three were found
unbuildable on the current wire and are now sequenced as contract work: the containment refusal,
the inline record editor, and the version token on the binary save door. One — the resolver's
five-state model — was routing its dominant real case to the wrong state, stating that a file does
not exist when it does. Full disposition in the ADR's §13 and in this document's own review log.

---

## What this delivers, in one paragraph

A note in the knowledge base can today only *mention* the things it depends on. The founder's
operating view is a dashboard note that is supposed to assemble itself out of fifteen or more
saved data views — and it renders as a list of links. This work makes a note show the things it
points at: a saved data view, a picture, a PDF, another note, a video. It also makes a *broken*
one say so, out loud, to the person reading and to any agent summarising the page — because the
failure this feature must never have is a dashboard that looks complete and is not.

---

## Available Reference Patterns

`docs/reference/go-implementation/` **does not exist in this repository** — verified by directory
listing on `def10b90e`. There are no reference patterns to draw on. The authoritative prior art is
the repository's own code and the ADRs cited throughout this document.

---

## Existing Codebase Context

> **GitNexus was not available in this session** — no `query` / `context` / `impact` / `trace`
> tool was exposed. Per the skill's fallback rule this section is built from direct code citation
> instead, using the facts the ADR and its review already measured on `def10b90e`. Nothing below
> is inferred; anything not measured is marked `[UNVERIFIED]`.

### Symbols Involved

> **Citations are `file::symbol`, not `file:line`.** Revision 1 used line numbers and **five of
> them were already wrong** — `rest_library.go:1404` pointed at a doc comment rather than a call,
> `knowledge_edit.go:298` at the `unknownArgs` call rather than `Execute`, `links.go:80` at a field
> on the wrong type. CLAUDE.md says line numbers in these files go stale within days, and this
> table proved it inside one week.

| Symbol | File | Role | What is known about it |
|---|---|---|---|
| `remarkKbWikilinks` | `src/components/library/preview/knowledgeMarkdown.tsx` | **extends** | Converts an embed to an image node **only** when the extension is in `IMAGE_EXTENSIONS`. Everything else falls through to a wikilink node rendered with the badge `embed shown as a link`. The fix is to **add branches**, not remove a wrong return. |
| `resolveEmbedUrl` | `KnowledgeNoteView.tsx::resolveEmbedUrl` | **extends** | Already graph-backed. Its match key is `e.link_text === target \|\| basenameOf(e.to_path) === target` — **no fragment**, so two named views of one `.base` are indistinguishable today. A live bug, not a prospective one. **It also checks `graph.nodes.find(n => n.path === edge.to_path)` and returns `undefined` when `node.exists === false`** — a live guard revision 1 omitted from this table and from EMB-011. See EMB-022. |
| `resolveWikilink` | `KnowledgeNoteView.tsx::resolveWikilink` | **reads** | The sibling memo, immediately above `resolveEmbedUrl`. It performs the **same** `node.exists === false` check and returns `{ state: 'unresolved' }`. The two must not diverge. |
| `collectionRoot` / `toWorkspacePath` | `KnowledgeNoteView.tsx` | **calls** | Already derived (ancestor `KnowledgeBaseInfo` probe) and already memoised. The resolver takes the memo as an argument; it must not re-derive the root. |
| `collectionPathToWorkspacePath` | `KnowledgeBacklinks.tsx::collectionPathToWorkspacePath` | **calls** | The collection-relative → workspace-relative conversion. Its own header states the failure being avoided. |
| `classifyLibraryEntry` | `libraryPreviewKind.ts::classifyLibraryEntry` | **calls, and cannot be called from the resolver** | Ten kinds: image, video, html, pdf, audio, base, markdown, mermaid, text, other. **Its parameter type is `ClassifiableEntry { name, mime?, is_text_editable }`, and its body reads `(entry.mime ?? '').toLowerCase()` and `if (entry.is_text_editable) return 'text'`.** A graph edge carries neither field and there is no single-entry GET, so the resolver uses an **extension-only** sibling — see EMB-034 and the Conservative Type Design section. |
| `ClassifiableEntry` | `libraryPreviewKind.ts::ClassifiableEntry` | **reads** | `is_text_editable` is **required** on it. A resolver-built ref would have to invent it, which is the cardinal error this work refuses. |
| `LibraryImagePreview`, `LibraryVideoPreview`, `LibraryPdfPreview` | `src/components/library/preview/` | **modifies** | The three that already take `{ workspaceId, entry }`. Pane-shape counts: image 2, PDF 13. |
| `BasePreview` | `BasePreview.tsx` | **modifies** | Takes `+ loadContent, loadBaseViews, loadViewResult, onOpenNote?`. Pane-shape count 5. Two staleness settings: view-result 60 s, base-views 10 s, neither refetching on window focus. |
| `LibraryDownloadCard` | `LibraryDownloadCard.tsx` | **excluded** | Reads `entry.size` and `entry.modified_at`; takes no `workspaceId`. **Must not be reachable from an embed.** |
| `LibraryAudioPreview`, `LibraryHtmlBody`, `LibraryHtmlFrame`, `LibraryTextBody` | module-private inside `src/components/library/LibraryPreviewPane.tsx` | **extracts (step 6)** | Not exported, no file of their own. Audio reuse requires an extraction step first. |
| `useLibraryFileEditor` | `useLibraryFileEditor.ts` | **modifies** | Calls `putLibraryContent(workspaceId, { path, content })` — **no version token**; zero matches for `version`/`etag`/`if-match`/`revision`/`token` in the whole file. This is the live data-loss path. |
| **`LibraryPdfPreview::handleSave`** | `LibraryPdfPreview.tsx::handleSave` | **modifies — the hard case** | **The ONLY production caller of `putLibraryContentBinary`.** It calls `doc.saveDocument()`, base64-encodes, and puts. Its loader uses a raw `fetch(libraryDownloadUrl(workspaceId, path))` — a byte stream — and never calls `fetchLibraryContent`, so **nothing on its path can return a version token today.** Under founder ruling N2 there is no exemption; EMB-007 specifies the fix. Revision 1's ambiguity A-1 named `useLibraryFileEditor` as "the only caller" and was wrong. |
| `handleLibraryContentPut` / `handleLibraryContentBinaryPut` | `pkg/gateway/rest_library.go::handleLibraryContentPut`, `::handleLibraryContentBinaryPut` | **modifies** | The only two mutating handlers in the file that never call `logLibraryAudit` (which fires for `library.delete`, `.upload`, `.mkdir`, `.create_vault`, `.rename` and the transfer modes — **six** call sites, not the seven revision 1 listed; the seventh citation was the function's own definition). **Neither takes any lock**: the text handler validates, opens the root, calls `checkCreateName`, then `root.WriteContent`. See EMB-006. |
| `deleteLibraryEntry` / `uploadLibraryFiles` / `renameLibraryEntry` / `moveLibraryEntry` | `pkg/gateway/rest_library.go` | **untouched — named as residual** | Audited, **not** version-checked, and still able to destroy a note an agent is mid-write on after step 0. See Assumptions. |
| `handleLibraryContentGet` | `pkg/gateway/rest_library.go::handleLibraryContentGet` | **modifies** | Builds a `gen.LibraryContentResponse{Path, IsText, Size, TooLarge}` (+ optional `Mime`, `Content`) and calls `jsonOK`. **It sets no headers at all**, so there is nothing for EMB-004's "send back the token it received" to receive. |
| `handleLibraryDownload` | `pkg/gateway/rest_library.go::handleLibraryDownload` | **modifies** | Serves bytes via `serveLibraryContent`. Gains the same version header, because it is the read on the PDF save path (EMB-007). |
| `knowledgeEdge` | `pkg/gateway/rest_knowledge.go::knowledgeEdge` | **modifies** | Sets nine fields and stops. Neither `HeadingFound`, nor `BlockID`, nor any reason for an `unresolved` resolution is projected. |
| `BuildLinkGraph` | `pkg/knowledge/graph.go::BuildLinkGraph` | **reads — the source of the honesty problem** | Builds its resolution index as `index: NewNoteIndex(walk.Files)`. **A walk-level skip removes the target from that list; a scan-level skip is appended after it is captured.** The two therefore produce opposite outcomes for a link *to* the skipped file. See EMB-021. |
| `WalkContained` | `pkg/knowledge/contain.go::WalkContained` | **reads** | Emits the walk-level skips: `symlink`, `outside_root`, `irregular`, and `unreadable` **for a directory it could not list** — which takes every file beneath it out of `walk.Files` too. |
| `knowledgeSkip` | `pkg/gateway/rest_knowledge.go::knowledgeSkip` | **reads** | Maps Go `SkipReason` → wire enum. Go has `symlink`, `outside_root`, `unreadable`, `irregular`; the wire has `symlink`, `outside_root`, `unreadable`, `not_addressable`, `node_limit`, `hop_limit`. **`irregular` maps to `not_addressable`; `node_limit` and `hop_limit` have no `pkg/knowledge` producer at all.** Fixture rows must use values this mapper can actually emit. |
| `ResolvedLink` | `pkg/knowledge/links.go::ResolvedLink` | **calls** | Has `HeadingFound` and `BlockID` in Go. `parseMarkdownLink` returns **no** `Link` for a scheme URL. The wikilink parser strips `#fragment` before assigning `Target`. |
| `ReadLink` / `renderReadLinks` | `pkg/knowledge/knowledge_read.go::ReadLink`, `::renderReadLinks` | **modifies** | `ReadLink` has ten fields — `To/From/Form/Alias/Heading/Resolved/Reason/Ambiguous/Candidates/Line` — and **no `Embed`**, even though the `ResolvedLink` it is projected from has one. `renderReadLinks` prints `fmt.Fprintf(b, "  %s (unresolved) %s", arrow, l.Form)` — **`l.Form` is the link as written, so an escaping path already reaches the model verbatim.** See EMB-024. |
| `editArgNames` + `Execute` | `pkg/knowledge/knowledge_edit.go::editArgNames`, `::Execute` | **modifies** | `unknownArgs(args, editArgNames)` runs **once, before** the `op` dispatch. Already contains `heading`, meaning a heading **of `path`**. |
| `spaContentSecurityPolicy` | `pkg/gateway/embed.go::spaContentSecurityPolicy` | **modifies** | Ends `frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`. `img-src 'self' data: blob:`. The file's own header records the string as measured but **not frozen**. |
| **`spaPdfWorkerContentSecurityPolicy`** | `pkg/gateway/embed.go::spaPdfWorkerContentSecurityPolicy` | **changes as a side effect** | `= withWasmCompilation(spaContentSecurityPolicy)`, served on the PDF worker path. **A new `frame-src` host propagates into it automatically.** Revision 1 listed neither it nor the fact that a second policy string exists. |
| **`TestSpaServedWithCSP`'s oracle** | `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` §10.7 | **must be edited in the same change** | The test does **not** compare against a literal in the test file. It does `os.ReadFile(specDocRelPath)`, matches `(?m)^default-src 'self';.*$`, and requires **exactly one** match. Miss this document and CI fails on a byte-for-byte assertion in a file nobody thought was code. |
| `libraryIsolationPolicyTemplate` | `pkg/gateway/library_isolation_policy.go` | **untouched** | A different policy for a different surface. **Do not edit it.** |
| `EditNote` / `SetProperty` | `pkg/knowledge/author.go::EditNote`, `::SetProperty` | **calls — with a correction** | Revision 1 stated *"`EditNoteRequest` requires both `Audit AuthorAudit` and `Actor AuthorActor`."* **It requires neither.** `Audit` is an interface and every emit is guarded `if a.sink != nil`, so **a nil `Audit` writes the file and records nothing, with no error**; `Actor.AgentID` is copied into the record unchecked; `EditNote`'s only precondition is `if c == nil`. See EMB-090's named enforcement point. |
| **`NewWriter`** | `pkg/knowledge/audit.go::NewWriter` | **calls — the actual enforcement point** | The **only** place an actor with neither an agent nor a user is rejected. A handler that "calls `EditNote`" directly bypasses it. |
| **`WithNoteWriteLock`** | `pkg/knowledge/version.go::WithNoteWriteLock` | **calls** | D14 tier-1 mutual exclusion: in-process striped mutex, then a POSIX advisory file lock. Key is `collectionRoot + "\x00" + path.Clean(rel)` — **all three of collection root, lock dir and relative path must match the agent path's or the two writers take different locks.** Empty `LockDir` is a documented *degraded* mode (in-process only), not a disabled one. |
| **`SetPropertyList` / `SetPropertyScalarChecked`** | `pkg/knowledge/knowledge_edit_list.go` | **reads** | Both exist. So "a list-valued field cannot be edited" is a **scope decision**, not a platform limit — revision 1 recorded it as the latter. |
| **`RecordWriteRequest`** | `contracts/components/schemas/RecordWriteRequest.yaml` | **wires (step 5)** | The typed record-write contract ADR-068 already built: `version_token` required on update, splice-not-reserialise, schema validation, and two prohibitions in its own description — relations and person properties are not writable here, and a request naming a **derived** property is rejected. **Referenced from no path in `openapi.yaml`.** |
| **`VaultFindCell` / `VaultFindRow`** | `contracts/components/schemas/` | **extends (step 5)** | `VaultFindCell` is `{property, value}` **only** — no declared type, no enum members, no `derived` or `relation` flag. `VaultFindRow` has `id/path/title/line/status/text/cells/joins/stale` — **no version token.** EMB-088 cannot be evaluated by the client without both. |
| **`RecordSchema` / `VaultRecord` / `KnowledgeConflictError`** | `contracts/components/schemas/` | **wires (step 5)** | All three are declared in `openapi.yaml`'s `components` and referenced from **no path** — verified by grepping `#/components/schemas/<name>`: zero usages each. The whole ADR-068 typed-record layer is agent-tool-only on the wire. |

### Impact Assessment

GitNexus was unavailable, so blast radius is stated from direct call-site measurement rather than
graph traversal. Treat the `d=1` column as "measured, non-exhaustive".

| Symbol modified | Risk | d=1 (WILL BREAK — must be updated or tested) | d=2 (LIKELY AFFECTED — should be tested) |
|---|---|---|---|
| `spaContentSecurityPolicy` | **CRITICAL** | `pkg/gateway/embed_csp_test.go` (`TestSpaServedWithCSP`, `TestSpaCsp_DirectiveFloor`); **`docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` §10.7 — the test's actual oracle, read at runtime with `os.ReadFile`**; **`spaPdfWorkerContentSecurityPolicy`, the derived second policy served on the PDF worker path** | `tests/e2e/csp-assumptions.spec.ts` (A0–A6); `docs/internal/architecture/csp-audit-2026-09-05.md`; every SPA surface that frames anything |
| `KnowledgeGraphEdge` schema | **HIGH** | `pkg/api/generated/`, `src/lib/api/generated/` (both regenerate); `make verify-contracts`; `rest_knowledge.go::knowledgeEdge` must populate **all three** new fields | Every consumer of the graph edge: `KnowledgeNoteView`, `KnowledgeBacklinks`, `KnowledgeOutline` |
| `LibraryContentRequest` / `LibraryBinaryContentRequest` schemas **+ an `ETag` response header on four operations** | **HIGH** | `useLibraryFileEditor`; `handleLibraryContentPut`; `handleLibraryContentBinaryPut`; `handleLibraryContentGet`; `handleLibraryDownload`; **`LibraryPdfPreview::handleSave` and its loader**; generated types both sides | `LibraryTextPreview` → `LibraryMarkdownPreview` / `LibraryCodePreview` / `LibraryMermaidPreview`; the signature-pad save path |
| **`LibraryPdfPreview.test.tsx`'s save round-trip** | **HIGH** | **Named explicitly because EMB-001 breaks it.** The file exists (~35 KB) and contains an explicit save round-trip assertion that passes today with no token. It must be updated **in the same change**, not discovered by CI | The whole PDF annotation feature |
| `VaultFindCell` / `VaultFindRow` schemas | **HIGH** | `pkg/api/generated/`, `src/lib/api/generated/`; every producer of a `knowledge_find` answer | `ViewPartsRenderer`, `ViewCellLink`, `BasePreview` |
| `editArgNames` / `knowledge_edit.Execute` | **HIGH** | All five existing ops (`create`, `set_property`, `append_section`, `link`, `replace_body`) — the per-op sweep changes validation for every one | `pkg/knowledge/authoring_tools_test.go`; every agent prompt that calls `knowledge_edit` |
| `entry` prop on eight renderers (`LibraryEntry` → `LibraryFileRef`) | **MEDIUM** | `LibraryPreviewPane.tsx`'s dispatch; each renderer's own test file | `npm run typecheck` across `src/` |
| `LibraryPdfPreview` worker lifecycle | **MEDIUM** | `LibraryPdfPreview.test.tsx`; `pdfInkAnnotation.ts` and `pdfBinaryEncoding.ts` consumers | The Library preview pane's PDF path |
| **`.github/workflows/pr.yml`** | **MEDIUM** | **Named explicitly because this work adds ~30 new SPA test files.** The vitest matrix is a hardcoded `VITEST_PATTERNS` list in that file; `scripts/check-vitest-coverage.mjs` fails when a test file matches no pattern in it. A new directory outside the matrix is **silently skipped while the job reports green** — 116 of 422 files (27%) once were | Every SPA test job |
| `ReadLink` | **LOW** | `knowledge_read.go::renderReadLinks` and its tests | Agent output for every note read |
| `resolveEmbedUrl` | **LOW** | `KnowledgeNoteView.test.tsx` | Image embeds that work today — the regression surface |

> **HIGH/CRITICAL flag for the founder:** the CSP change (US-9) touches a string the codebase
> itself records as **shipped, measured, and not yet frozen**, and whose `frame-ancestors 'none'`
> directive is documented as the compensating control for a *different* policy's `frame-src`. It
> is the single highest-consequence line in this work and is sequenced as its own isolated change
> for that reason.

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Note read → markdown pipeline → wikilink/embed slot → rendered note | The surface this whole feature lives in. Every new branch is inserted here. |
| Note read → `kind: 'links'` graph query → edge list | The resolver's evidence source. Its bounds and skips are the honesty problem. |
| Library preview pane → classify kind → mount renderer | The renderer inventory being widened. The pane must keep working identically. |
| Library editor → whole-file save | The live unversioned, unaudited door closed by step 0. |
| Agent → `knowledge_edit` → `EditNote` → lock, compare-and-swap, atomic write, audit | The guarded write path everything new must join, not bypass. |
| Base view → evaluate → rows → cell links | Reused inline. Its resolver props become the reader's (Q8). |

### Cluster Placement

This work spans **three** areas that normally move independently, which is the main architectural
risk in it:

1. **The knowledge-base reader** (SPA) — the resolver, the markdown branches, lazy mounting.
2. **The gateway's Library and knowledge REST surface** (Go) — **seven** contract changes, two new
   endpoints, two audit call sites, one lock.
3. **The application's security policy** (Go + e2e) — one directive on one string, **plus a second
   derived string and an oracle that lives in a specification document.**

Nothing in (1) may be merged on an assumption about (2); the contract work is sequenced ahead of
the client work that depends on it. (3) ships alone.

> **Revision 1 said "two contract changes" and listed three in its own table. The real count is
> seven** (CW-1…CW-7 below). That is not bookkeeping: under Hard Constraint #8 the contract
> determines merge order, so an undercount is a plan that cannot be executed in the order it
> states. Three of the four newly-found changes are prerequisites of **P0** stories.

---

## User Stories & Acceptance Criteria

### User Story 1 — A note saved by a person can no longer silently erase an agent's work (Priority: P0)

Today, when a person edits a note's body in the Library and presses save, the whole file is
replaced. Nothing checks whether anyone else changed that file since it was opened, and nothing is
written to the activity record. If an agent wrote to the same note thirty seconds earlier, that
work disappears without a message, a warning, or a trace. Every other change to the library — a
delete, an upload, a rename, a folder creation — is recorded. These two saves are not.

This is a defect that exists today and is not caused by anything else in this spec. It is first
because everything that follows makes people and agents write to the same notes more often.

**Why this priority**: it is live data loss on the exact surface the rest of this work is about,
and Hard Constraint #7 makes a pre-existing failure ours to fix. It is also small: two contract
fields, one change in the editor, two recording calls.

**Independent Test**: open a note in the Library, have a second writer change it, then save from
the first window. The save must be refused with a clear explanation, and both the refusal and any
accepted save must appear in the activity record.

**Acceptance Scenarios**:

1. **Given** a note opened for editing, **When** nothing else has changed it and the person saves,
   **Then** the save succeeds, the response hands back the file's new version marker, and an
   activity record naming the file and the person is written.
2. **Given** a note opened for editing, **When** another writer changes it first and the person
   then saves, **Then** the save is refused with a conflict, the message names the file, and no
   content is overwritten.
3. **Given** a refused save, **When** the person looks at the editor, **Then** they are told the
   file changed while they were editing and are offered a way to reload and try again.
4. **Given** a refused save, **When** the person does nothing, **Then** the system never resends
   the save on its own.
5. **Given** a binary file saved through the same door (for example an annotated PDF), **When** it
   is saved, **Then** it is subject to the same conflict check and the same activity record —
   **and the reader that opened it must have been given a version marker to send back**, because
   that path reads raw bytes and today receives nothing it could return.
6. **Given** a save that is checked against a version marker, **When** an agent writes to the same
   file in the instant between the check and the write, **Then** exactly one of the two writes
   survives and the loser is told so — the check and the write are one indivisible step, not two.
7. **Given** a save sent with no version marker at all, **When** it arrives, **Then** it is
   refused. There is no caller that is allowed to skip the check.

---

### User Story 2 — A broken embed says what is wrong; an unverifiable one says it cannot tell (Priority: P0)

An embed that cannot be shown must never render as an empty space. An empty space reads as *"there
is no data here"*, which is a different and false statement. But there is a second, subtler
failure that matters more: sometimes the reader's own evidence is incomplete — a note was skipped,
a name did not match, the answer was cut short — and in that case the honest sentence is *"I could
not check this"*, not *"nothing here is called that"*. Saying the second one about a file that
exists and can be opened in the next pane is the exact class of falsehood the last several commits
on this branch were fixing.

The same honesty is owed to agents. An agent summarising a fifteen-module dashboard must be able
to say "three of these are broken" rather than quietly summarising the twelve that worked.

**Why this priority**: it is the feature's integrity property. Every other story is worth less if
this one is wrong, because a wrong answer delivered confidently is worse than no answer.

**Independent Test**: point an embed at a name that does not exist, at a name that exists but whose
view does not, and at a note the indexer skipped. All three must produce visibly different,
truthful messages — and the third must not claim the file is missing.

**Acceptance Scenarios**:

1. **Given** an embed naming a file that does not exist, **When** the note is read, **Then** a
   visible marker names the target and states that nothing in this knowledge base carries that
   name.
2. **Given** an embed naming a file that exists but a view that does not, **When** the note is
   read, **Then** the marker says so **and lists the views that do exist**.
3. **Given** an embed naming a file that exists but a heading that does not, **When** the note is
   read, **Then** the marker says so and lists the headings that do exist.
4. **Given** an embed whose target the indexer refused to read — a symbolic link, a folder it
   could not open — **When** the note is read, **Then** a **visibly different** marker states that
   the embed could not be checked and gives the reason, and it **never** states that nothing
   carries that name. *(This is the dominant real case, and today it produces the missing-file
   sentence about a file the reader can open in the next pane.)*
5. **Given** the reader's own evidence is incomplete for some other reason — a name that did not
   match, an answer cut short — **When** the note is read, **Then** the same could-not-be-checked
   marker appears, with a reason where one exists and **"no reason available"** where none does.
6. **Given** the two halves of one answer disagree about the target — the link says it resolved,
   the target says it does not exist — **When** the note is read, **Then** the reader believes
   neither and says it could not check.
7. **Given** the evidence has not arrived yet, **When** the note is read, **Then** a reserved,
   correctly-sized placeholder is shown and no failure message appears.
8. **Given** the request for evidence fails outright, **When** the note contains fifteen embeds,
   **Then** exactly one page-level error appears with the reason and a retry — not fifteen
   markers and not fifteen placeholders.
9. **Given** a knowledge base the reader is not allowed to see, which answers successfully with
   nothing in it, **When** a note with fifteen embeds is read, **Then** exactly one page-level
   statement appears — not fifteen "could not be checked, no reason available" markers.
10. **Given** an embed pointing outside the knowledge base, **When** the note is read, **Then** it
    is refused with a message **distinct from the missing-file message**, and that message does not
    repeat the outside path back to the reader. *(The two cases are one value on today's answer;
    telling a reader an escaping target "does not exist" is the wrong statement.)*
11. **Given** an agent reads a note containing embeds, **When** it looks at the links section,
    **Then** each embed is marked as an embed, each broken one is marked broken **with its
    reason** — including "outside the knowledge base" where that is the reason — and a saved data
    view is described as a view rather than as a section of a document.

---

### User Story 3 — Pictures and PDFs appear inside the note (Priority: P0)

The cheapest proof that the whole mechanism works, on the two kinds with no unknowns. A picture
already works today; a PDF does not. Both must appear in place, at a sensible size, with the same
component that draws them in the full-screen preview — not a second copy of it.

There is one trap specific to this story and it is stated here rather than discovered later: there
is **no visible symptom** when this breaks. A PDF that fails to route does not render broken — it
renders as a tidy link with a small grey badge, which is a deliberate, tested, honest treatment.
A person looking at the page cannot tell a correct fallback from a failure to route.

**Why this priority**: it proves the resolver, the shared-renderer approach and the inline layout
on the easiest possible material, and de-risks the story that actually matters (US-5).

**Independent Test**: a note with one picture, one SVG, one sized picture and two PDFs. Each shows
its own content in place; both PDFs render at once; the page does not jump as they arrive.

**Acceptance Scenarios**:

1. **Given** a note embedding a picture, **When** it is read, **Then** the picture is drawn in
   place by the same component the full-screen preview uses.
2. **Given** a note embedding a picture with a width given after a `|`, **When** it is read,
   **Then** the picture is drawn at that width.
3. **Given** a note embedding an SVG, **When** it is read, **Then** it is drawn as an image and
   none of its scripts run.
4. **Given** a note embedding a PDF, **When** it is read, **Then** the PDF is drawn in place.
5. **Given** a note embedding two PDFs, **When** it is read, **Then** both render, and if one
   fails the other is unaffected and still shows its content.
6. **Given** a note embedding a file whose kind has no inline treatment, **When** it is read,
   **Then** it keeps today's link-with-a-badge treatment and no download card appears.
7. **Given** the same file shown in the full-screen preview and embedded in a note, **When** both
   are drawn, **Then** the system decided it was the same kind of thing in both places — **except**
   for three named cases where it provably cannot, which are listed rather than left to be found.
   *(The full-screen preview asks the server what a file is; an embed has only the file's name. For
   a file whose name does not say what it is, the two disagree.)*

---

### User Story 4 — The knowledge base reports whether a named heading or block was actually found (Priority: P0)

Two of the messages US-2 promises cannot be written today, because the information they need never
leaves the server. The server knows whether a `#Section` was found in the target note and knows
which anchored block a link named; the answer it sends to the reader carries neither. Until it
does, "this note has no heading called Q3" is a sentence the reader is not entitled to say, and
two embeds of the same file under different anchors cannot be told apart.

**Why this priority**: it blocks US-2's third acceptance scenario and US-7 entirely, and it is
contract work, which by Hard Constraint #8 must land before the code that reads it.

**Independent Test**: ask for a note's links where one link names a real heading and another names
a heading that does not exist. The two answers must differ in the reported found/not-found flag.

**Acceptance Scenarios**:

1. **Given** a note linking to a heading that exists in the target, **When** its links are
   requested, **Then** the answer reports the heading as found.
2. **Given** a note linking to a heading that does not exist in the target, **When** its links are
   requested, **Then** the answer reports the heading as not found, and the file itself still
   resolves.
3. **Given** a note linking to an anchored block, **When** its links are requested, **Then** the
   answer carries the block anchor separately from any heading text.
4. **Given** the contract change, **When** the repository's contract check runs, **Then** it passes
   with the specification and the generated artefacts committed together in one change.

---

### User Story 5 — A dashboard note shows its data views as live data (Priority: P0)

This is the reason the work exists. The founder's `Founder Cockpit.md` states its own rule —
*"assembled entirely from Base-view embeds — never hand-typed data"* — and today renders as a list
of links. Seventy-five embeds across the knowledge base name a specific saved view of a data
table, and an entire dashboards folder depends on them.

Two things make this harder than it looks. A person writes the view's **display name**; the system
addresses views by a machine name it generates, which a reader must never try to guess. And the
address of the file differs depending on where the knowledge base sits inside the workspace — a
knowledge base that is not at the top level is the founder's own arrangement, and getting this
wrong opens a different file with the same name.

**Why this priority**: 75 measured uses and the founder's primary operating view.

**Independent Test**: a knowledge base mounted **below** the workspace root, containing one data
file with two differently-named views, embedded twice in one note. Each embed shows its own view's
rows.

**Acceptance Scenarios**:

1. **Given** a note embedding a named view, **When** it is read, **Then** the view's rows are shown
   in place, drawn by the same component the full-screen preview uses.
2. **Given** a note embedding two different views of the same data file, **When** it is read,
   **Then** each shows its own view and they are not confused with one another.
3. **Given** a knowledge base mounted below the workspace root, **When** a view in it is embedded,
   **Then** it resolves to the right file.
4. **Given** an embed naming a data file with no view named, **When** it is read, **Then** the
   first available view is shown **and a caption says which view is being shown and that the
   embed did not choose one**.
5. **Given** two views that share the same display name, **When** one is embedded, **Then** the
   embed is refused and the message names both.
6. **Given** a view the server has marked as unable to be served, **When** it is embedded,
   **Then** the server's own reason is shown in place.
7. **Given** an embedded view, **When** the reader hovers over it or moves keyboard focus into it,
   **Then** its controls appear; at rest they are not shown.

---

### User Story 6 — Before dashboards ship, the founder is told which existing embeds will work (Priority: P0)

Seventy-five embeds already exist, written by hand in another application over months. Some of
them may name a view whose display name has since changed. The difference between learning that
from a one-page report and learning it from a dashboard full of error markers is the whole value
of this story.

**Why this priority**: it is a single query over data that already exists, and it converts a
possible bad first impression into a known list.

**Independent Test**: run the report against a knowledge base seeded with a deliberate mismatch.
The report must name the mismatch.

**Acceptance Scenarios**:

1. **Given** the existing knowledge base, **When** the report is run, **Then** it lists every
   embed of a data view, the note it is in, and whether it resolves to a real view.
2. **Given** an embed naming a view that no longer exists, **When** the report is run, **Then**
   that embed appears in the report's failure list with the reason.
3. **Given** the report, **When** it is produced, **Then** it is run and reviewed **before** the
   dashboard story ships, not after.

---

### User Story 7 — A note can show another note's contents, one level deep (Priority: P1)

Showing one note inside another is the most fundamental idea in this kind of tool. This story
delivers exactly one level of it: a note shows another note, or one section or one anchored block
of it. **The shown note's own embeds appear as links.** There is no second level.

> **Founder ruling N1, and the reason a whole mechanism left this story.** Revision 1 of this spec
> required a loop detector *and* a depth limit, with four tests and a success criterion. It also
> required — in the same story, as acceptance scenario 8 — that a shown note's embeds render as
> links. **Those two requirements cannot both hold.** If the second level never renders as content,
> then A showing B showing A never reaches the second A: there is no loop to detect, no depth to
> exceed, and no diamond to misreport. The four cycle tests could only have passed against a page
> the product is incapable of building.
>
> That is the same failure this document's own false-green register exists to catch — a test whose
> subject can be deleted while the test stays green — one level higher up: the **feature**, not the
> test, was the thing that could not be exercised. The founder's ruling resolves it by **removing
> the feature**: no cycle set, no depth cap, no markers for either. The rule that survives is the
> one that was always true.
>
> What nesting would require, if it is ever wanted, is recorded in ADR-083 D5 so nobody re-derives
> it: one graph query per transcluded note, a place for those queries in the four-in-flight budget,
> a reserved-height story per level — **and then** a cycle set, which is the part everyone reaches
> for first and the last thing that becomes necessary.

**Why this priority**: no note in the knowledge base uses this today, but a knowledge base authored
in Obsidian will grow them, and one level covers the idiom.

**Independent Test**: a note showing another note that itself contains a picture embed. The other
note's text appears; its picture appears as a link with a stated reason, not as content and not as
an unchecked marker.

**Acceptance Scenarios**:

1. **Given** a note showing another note, **When** it is read, **Then** the other note's contents
   appear in place.
2. **Given** a note showing one section of another note, **When** it is read, **Then** only that
   section appears.
3. **Given** a note showing one anchored block of another note, **When** it is read, **Then** only
   that block appears.
4. **Given** a note shown inside another note that itself contains embeds, **When** it is read,
   **Then** those inner embeds render as links with a one-line reason — **not** as content, and
   **not** as could-not-be-checked markers.
5. **Given** a note that shows itself, **When** it is read, **Then** its text appears once, the
   inner self-embed appears as a link, and the reader terminates — because the rule terminates it,
   not because anything counted.
6. **Given** a note showing another note that is empty, **When** it is read, **Then** an empty
   region states that the note is empty, rather than showing blank space with no explanation.

---

### User Story 8 — A forty-module dashboard stays responsive, and says what it cannot do (Priority: P1)

A dashboard with forty modules is a legitimate document and there is no limit on how many a note
may have. What must be limited is the *work*: a module starts costing something when it scrolls
into view and stops when it is well out of view. Scrolling quickly from top to bottom must not
fire forty simultaneous evaluations at a single server process.

That design has a consequence, and it is handled here by **stating it** rather than by promising a
fix. A page whose modules only exist once you have scrolled past them cannot be found by the
browser's find-in-page, and **cannot be printed whole**.

> **Founder ruling N3: print support is dropped from this work entirely.** Revision 1 promised that
> the browser's print event would hold the print open until every module settled. It cannot: that
> event is dispatched synchronously and cannot await asynchronous work, and a print started from
> the browser's own menu cannot be held at all. A handler written against it works locally on a warm
> cache and fails exactly when the modules are slow — the only time it matters.
>
> So: **printing a dashboard prints what has already mounted.** That is true today and stays true.
> Making printing complete is separate, unscheduled work, and is not in this spec's scope,
> sequencing, tests or success criteria. What this story owes is one honest line in the reader, and
> that line now covers printing as well as find-in-page.

**Why this priority**: without it, the founder's own primary document is the worst case for the
feature that exists to serve it.

**Independent Test**: a note with forty view modules. Scroll top to bottom quickly; count the
evaluations in flight at any moment. Confirm the reader is telling you, on screen, that unmounted
modules are unreachable by find and by print.

**Acceptance Scenarios**:

1. **Given** a note with forty modules, **When** it is opened, **Then** only the modules near the
   viewport begin work and the rest show correctly-sized reserved space.
2. **Given** the same note, **When** the reader scrolls quickly to the bottom, **Then** no more
   than four evaluations are in flight at once and any beyond that show a visible waiting state.
3. **Given** a module that has scrolled out of view before its turn, **When** its turn arrives,
   **Then** it gives up its place in the queue.
4. **Given** a module whose evaluation failed, **When** the reader scrolls past it forty times,
   **Then** it is not retried forty times; it shows its error and offers a manual retry.
5. **Given** a note with unmounted modules, **When** it is read, **Then** a one-line note states
   that **find-in-page and printing** will not reach modules that have not been scrolled to; the
   note disappears once every module is mounted, and never appears on a note with no embeds.
6. **Given** a reader who leaves the application and returns, **When** they come back to a
   dashboard, **Then** the modules are not all re-evaluated at once.

---

### User Story 9 — An allow-listed video plays in a note, and contacts nobody until it is pressed (Priority: P1)

One external service is allowed inside a note, and one only. Everything else stays inside the
knowledge base: no external pictures, no other external video, no other external frames.

The part of this that is easy to get wrong is the placeholder. A video frame that is put on the
page as soon as the module appears contacts the video provider **for every embed, before anyone
has chosen to watch anything** — which for a page of ten videos is ten contacts with a third party
that nobody asked for. So the thing shown before you press play is drawn locally, out of nothing
but a shape and a play control. It is not the provider's own preview picture, because fetching
that picture is itself the contact we are avoiding.

**Why this priority**: it is self-contained, and it is the only part of this work that changes the
application's own security policy — so it ships alone and is reviewed as security work.

**Independent Test**: a note with an allow-listed video and a non-allow-listed one. Watch the
network. Before pressing play there must be no contact with the provider at all; after pressing,
exactly the expected one. The non-allow-listed one must not be framed.

**Acceptance Scenarios**:

1. **Given** a note containing an allow-listed video link, **When** it is read, **Then** a locally
   drawn placeholder with a play control appears and **no network request leaves for the video
   provider**.
2. **Given** that placeholder, **When** the reader presses play, **Then** the player appears and
   plays.
3. **Given** a note containing a video link for any other host, **When** it is read, **Then** it is
   not framed and falls back to a link.
4. **Given** a note containing hand-written frame markup, **When** it is read, **Then** nothing is
   framed.
5. **Given** the application's security policy, **When** it is served, **Then** exactly one
   external frame host has been added, the rule preventing the application from being framed by
   others is unchanged, and no external picture host has been added.
6. **Given** an operator who does not want the external host, **When** they turn it off in
   configuration, **Then** the host is absent from the served policy and the reader shows the
   embed as a link.
7. **Given** the policy change, **When** it merges, **Then** the browser measurement is re-run with
   a video in it, the existing framing assertion is re-run unchanged, and the security audit
   document is amended in the same change.

---

### User Story 10 — A person edits a record's field from inside a note, safely and attributably (Priority: P1)

A dashboard is more useful if you can act on it. Changing a task's status from a dashboard should
change the task — everywhere, for everyone, because that is what a status is. But changing how a
*view* is filtered or sorted should change only the one place you are looking at, and must never
be written back to the shared definition. The application this was migrated from does write it
back, so filtering one dashboard silently re-filters every other dashboard using the same view.
Users work around it by hiding the controls with stylesheet hacks. That workaround existing is the
evidence it is a defect.

Every change made this way must go through exactly the same door an agent's change goes through:
the same lock, the same "has anyone else changed this since I read it" check, the same rules about
what may be written at all, and the same activity record. And it must be attributable to the person
who made it — not to an empty field, and not borrowed from some agent's name.

> **What revision 1 got wrong here, and it was the whole story.** Revision 1 routed this write
> through a raw frontmatter line-editor. There is already a **typed record-write contract** in this
> system, built deliberately, which refuses two things that editor does not: **a value the system
> calculates rather than stores**, and **a link to another record**. Overwriting either by hand is
> how the tool this system replaced silently deletes data.
>
> Worse, the dashboard **cannot tell those fields apart** — what a view sends to the browser today
> is a property name and a rendered string, with no indication of a field's type, its allowed
> values, or whether it is calculated or a link. So revision 1 would have offered an editor for a
> calculated field, and the write would have landed through the one path that does not refuse it.
>
> The consequence for planning is that this story is **four contract changes, not one**, and three
> of them exist only to let the browser ask questions it currently has no way to ask.

**Why this priority**: it is the only new way of writing to the knowledge base in this work, and
it is the highest-consequence part of it.

**Independent Test**: two windows on the same record. Change the field in one, then in the other.
The second must be refused with a clear explanation, must not overwrite, and must not resend
itself. The activity record must name a person, not an agent. In the same view, a calculated field
and a linked record must offer no editor at all while an ordinary field does.

**Acceptance Scenarios**:

1. **Given** a record shown in a view with a fixed set of allowed values for a field, **When** the
   reader changes it, **Then** the change is saved to that record and every other place showing
   that record updates.
2. **Given** the same record, **When** the change is saved, **Then** an activity record names the
   person who made it, distinctly from any agent.
3. **Given** a record changed by someone else since it was displayed, **When** the reader saves,
   **Then** the change is refused, the field returns to the stored value, the row says it changed
   while they were editing, and a retry is offered with the current value visible.
4. **Given** a refused change, **When** nothing further is done, **Then** the system never resends
   it by itself.
5. **Given** a field whose type the record's own definition does not describe as editable,
   **When** it is displayed, **Then** no editor is offered and a way to open the note is offered
   instead.
6. **Given** a field the system **calculates** rather than stores, or a field that **links to
   another record**, **When** it is displayed, **Then** no editor is offered for it — **while an
   ordinary field in the same view is still editable**, so that "no editors anywhere" cannot be
   mistaken for correct behaviour.
7. **Given** a record's name or location, **When** it is displayed, **Then** it is never editable
   here — **while an ordinary field in the same row is.**
8. **Given** a reader who filters or sorts an embedded view, **When** they reload the page,
   **Then** the filter is gone and the shared definition is unchanged.
9. **Given** a reader who filters an embedded view, **When** they look at another embed of the same
   view on the same page, **Then** it is unaffected.
10. **Given** a saved change to a record's field, **When** it succeeds, **Then** the note's own
    content, its outline and its links are refreshed **for that note only**, and every embed of the
    affected view on the page shows the new value.
11. **Given** an installation running with authentication turned off, **When** a person edits a
    record field, **Then** the change **is** saved and **is** recorded — with an actor that says,
    plainly, that nobody could be identified. It is not refused, and it is not recorded as if
    somebody had been.

---

### User Story 11 — An agent adds an embed without being taught the notation (Priority: P1)

An agent should be able to say "put the *Needs Daniel* view of the tasks table under the *This
week* heading of the cockpit note" and have the correct text written for it. The value is not
saving keystrokes — it is that the request is **checked before it is written**. If the view does
not exist, the agent is told so, and is told what does exist, instead of writing a line that
renders as an error forever.

This is added to the existing note-editing tool rather than as a new tool of its own. The
repository finished retiring a family of near-identical tools three weeks ago for a stated reason,
and adding a ninth would recreate exactly that pattern.

**Why this priority**: without it every agent-written embed is a guess at notation, and the
knowledge base fills with broken lines nobody notices.

**Independent Test**: ask for an embed of a view that does not exist. The tool must refuse and list
the views that do. Then ask for one that does; the resulting note must render it.

**Acceptance Scenarios**:

1. **Given** an agent naming a note, a data file and a view that all exist, **When** it asks to
   embed, **Then** the correct line is written into the named note under the named section and
   nothing else is written.
2. **Given** an agent naming a view that does not exist, **When** it asks to embed, **Then** the
   request is refused and the refusal lists the views that do exist.
3. **Given** an agent naming a target outside the knowledge base, **When** it asks to embed,
   **Then** the request is refused and no line is written.
4. **Given** an agent giving a width for something that is not a picture, **When** it asks to
   embed, **Then** the request is refused rather than accepted and quietly ignored.
5. **Given** an agent supplying an argument that this operation does not read, **When** it makes
   the request, **Then** the request is refused for **this operation**, not merely accepted
   because some other operation reads it.
6. **Given** an agent omitting the "the file was in this state when I read it" token, **When** it
   makes the request, **Then** the request is refused.
7. **Given** this whole capability, **When** an operator inspects their permission settings,
   **Then** **nothing has changed** — no new permission to grant, no new entry anywhere.

---

### User Story 12 — Sound, video, diagrams and page fragments, when a real note needs them (Priority: P4)

Every remaining kind — audio files, video files, diagram files, a specific page of a PDF, and the
two inline query notations — is in scope and has **zero measured uses** across 784 notes. Building
them now is speculation. They are specified so that when the first real need appears the answer is
already decided, and they are sequenced last so that they can be dropped without loss.

**Why this priority**: no evidence of demand. "Ship the earlier stories and stop" is an explicitly
legitimate outcome.

**Independent Test**: a note embedding a sound file and a video file. Both play in place, using the
same components the full-screen preview uses — not copies of them.

**Acceptance Scenarios**:

1. **Given** a note embedding a sound file, **When** it is read, **Then** it plays in place using
   the same component the full-screen preview uses.
2. **Given** a note embedding a video file, **When** it is read, **Then** it plays in place using
   the same component the full-screen preview uses.
3. **Given** the sound component now used in two places, **When** the code is inspected, **Then**
   there is exactly one of it and no copy was left behind.
4. **Given** a note embedding a specific page of a PDF, **When** it is read, **Then** that page is
   shown.

---

## Behavioral Contract

**Primary flows**

- When a note embeds something the knowledge base can draw, the system draws it in place with the
  same component the full-screen preview uses.
- When a note embeds a named view of a data table, the system shows that view's current rows.
- When a note embeds a picture with a width, the system draws it at that width.
- When a note embeds another note, a section or an anchored block, the system shows exactly that.
- When a note embeds an allow-listed video, the system shows a locally drawn play control and
  contacts nobody until it is pressed.
- When a person changes a record's field from inside an embed, the system saves it to that record
  and every place showing it updates.
- When an agent asks to embed something, the system writes the notation for it after checking that
  what it named exists.

**Error flows**

- When the named target does not exist, the system says so, names the target, and gives the reason.
- When the target exists but the named view or section does not, the system says so and lists what
  does exist.
- When the target lies outside the knowledge base, the system refuses it **with a message distinct
  from the missing-file message** and does not repeat the outside path back to the reader.
- When the indexer refused to read the target, the system says the embed could not be checked and
  gives that reason — and never says the file is missing.
- When the system's own evidence is incomplete for any other reason, it says the embed could not be
  checked, and says "no reason available" when it has none rather than inventing one.
- When the two halves of one answer disagree about a target, the system believes neither.
- When the evidence request fails outright — or comes back empty because the whole knowledge base
  was unreadable — the system shows exactly one page-level statement with a retry.
- When a view cannot be evaluated, the embed shows the server's own reason and a manual retry, and
  is not retried automatically.
- When a save is refused because someone else changed the file first, the system says so, restores
  the stored value, offers a retry, and never resends the save by itself.
- When a shown note contains embeds of its own, they render as links with a stated reason.

**Boundary conditions**

- When evidence has not arrived yet, the system shows correctly-sized reserved space and no failure
  message.
- When a note has more embeds than fit on screen, only those near the viewport do any work; there
  is no limit on how many a note may contain.
- When more than four evaluations would run at once, the extra ones show a visible waiting state.
- When modules are unmounted, the system states that find-in-page and printing will not reach them.
- When two views share the same display name, the system refuses rather than picking one.
- When a data file is embedded without naming a view, the system shows the first available view
  **and says that it chose**.
- When a file kind has no inline treatment, the system falls back to today's link-with-a-badge and
  never to a download card.

---

## Edge Cases

- **An embed inside a code fence or inline code.** Expected: nothing is drawn. The notation is
  quoted text, not an instruction. *(This behaviour was not verified in the existing parser; it is
  an implementation obligation with a named test, not an assumption.)*
- **Two embeds of the same data file under different view names in one note.** Expected: each shows
  its own view. This fails today.
- **A knowledge base that is not at the top of the workspace.** Expected: correct resolution. A
  top-level fixture passes whether or not the conversion happens, so it proves nothing.
- **A name that matches more than one file.** Expected: the fixed tie-break decides it **and** the
  ambiguity is reported alongside.
- **A view the server has marked unable to be served.** Expected: the server's own reason appears
  in place, whether or not writing such an embed is allowed.
- **A target the indexer refused to read.** Expected: "could not be checked", never "does not
  exist". **This is two different cases and only one of them is what you would guess.** When the
  refusal happened while walking the folders — a symbolic link, an unreadable directory — the
  target never entered the index, so a link to it comes back as a *matching* answer saying
  "unresolved", which is exactly the shape that produces the false sentence. When the refusal
  happened while reading the file itself, the target is still in the index and the link
  **resolves normally**, so no marker appears at all and the embed tries to draw a file that could
  not be read. Neither produces "no answer at all", which is what revision 1 assumed both did.
- **A truncated answer.** Expected: honoured as "could not be checked", even though today's
  server never truncates this particular answer. A client that ignores an honesty flag is one
  change away from a false statement.
- **A knowledge base the reader is not entitled to see.** Expected: **one** page-level statement.
  The server answers such a request with a success and an empty body — deliberately, so that a
  refusal does not confirm the knowledge base exists — which looks identical to "this note has no
  links". Fifteen embeds on such a page would otherwise each say "could not be checked, no reason
  available".
- **A link that says it resolved and a target that says it does not exist, in the same answer.**
  Expected: "could not be checked". The reader has no basis to prefer either half.
- **An embed that names a path outside the knowledge base, written by hand elsewhere and synced
  in.** Expected: refused at read time; the outside path is not echoed.
- **A width given on a sound or video file.** Expected: refused when written, ignored when read.
  The original application accepts it and does nothing, which is worse than not offering it.
- **A single-item view, and an empty view.** Expected: rendered as an empty result, not as an
  error, and never as blank space with no explanation.
- **A record field whose value is a list.** Expected: no editor offered. The layer beneath can only
  write one value on one line, and offering a control it cannot honour is a control that reports
  success and does something else.
- **Two windows editing the same record.** Expected: the second is refused, restores the stored
  value, and offers a retry.
- **A person's save racing an agent's write in the same instant.** Expected: one survives, the
  other is told. A version check that is not held together with the write does not achieve this —
  it returns success to both and loses one silently, which is the exact defect the check exists to
  prevent, now with a reassuring green tick in front of it.
- **A calculated field, and a field that links to another record, in an editable view.** Expected:
  neither offers an editor, **while an ordinary field in the same view does.**
- **An installation with authentication switched off.** Expected: a record edit is saved and
  recorded with an actor that plainly says nobody was identified. Not refused; not recorded as if
  somebody had been.
- **A note that shows a note that shows a picture.** Expected: the middle note's text, and the
  picture as a link with a stated reason. Not the picture. Not an unchecked marker.
- **A page of ten videos.** Expected: zero contacts with the video provider until a play control
  is pressed.
- **An operator who turns the external video host off.** Expected: absent from the served policy;
  the embed falls back to a link.
- **A printed dashboard, from any print path.** Expected: the modules that have mounted, and a
  statement on screen — before printing — that this is what will happen. Not a promise of
  completeness (**N3**).

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not show an empty space where an embed failed, because an empty space reads as
  "there is no data", which is a different and false statement.
- The system must not say a file is missing when it has not been able to check, because the reader
  can open that file in the next pane and an agent will repeat the sentence.
- The system must not draw a second copy of any renderer for inline use, because the same
  hand-copying produced three separate drifts in the markdown pipeline already.
- The system must not put an isolation boundary inside the note reader. A reading surface is not
  an isolation boundary and must not become one; agent-authored web pages are never framed inside
  a note.
- The system must not fetch anything from the external video provider before a person presses play
  — including the provider's own preview picture. An eager frame is one third-party contact per
  embed, for a video nobody chose to watch.
- The system must not add any external picture host. Pictures stay inside the knowledge base.
- The system must not write a view's filter or sort back to the shared definition, because that
  makes filtering one dashboard re-filter every other dashboard using the same view.
- The system must not resend a refused save by itself. Re-sending with the value the server now has
  is literally "overwrite whatever changed", dressed as a convenience.
- The system must not offer an editor for something the layer beneath cannot write — a list-valued
  field, a record's name, a record's location.
- The system must not accept an argument it does not act on. An argument that is accepted and
  ignored is a caller that believes it narrowed something.
- The system must not add a new tool name for embed authoring, because that recreates the family of
  near-identical tools this codebase finished retiring.
- The system must not weaken, reorder or drop any other directive in the application's security
  policy while adding the one host, and must not touch the separate policy that isolates previews.
- The system must not render a second level of transclusion (**N1**). A shown note's own embeds are
  links.
- The system must not build a loop detector or a depth cap. With one level there is nothing for
  either to catch, and a mechanism whose input the product cannot construct passes every test
  forever while proving nothing.
- The system must not promise a complete printout (**N3**). Printing reaches what has mounted; the
  reader says so on screen rather than the document saying otherwise.
- The system must not check a version marker and then write as two separate steps. A window between
  them loses the other writer's work behind a check that returned success.
- The system must not exempt any caller from the version marker (**N2**), including the one whose
  read path does not currently return one. The read path is changed instead.
- The system must not record a human edit as if a person had been identified when none was
  (**N4**). An unidentified edit is recorded as unidentified, and must be findable as such.
- The system must not offer an editor for a value it calculates or a link between records, and must
  not route a record write through a path that would accept one.
- The system must not claim a browser security measurement is complete. After this work, one
  browser is measured and two remain outstanding, exactly as before.
- The system must not add a hard limit on how many embeds a note may contain. The bound is on
  work, not on count.
- The system must not invent a file's type or editability in order to decide how to draw it. Where
  it cannot know, it says what it can tell from the name and accepts the named divergence.

### Machine-Verifiable Constraints

**Error codes and messages (HTTP)**

- When a whole-file save is sent with a version token that no longer matches, the system MUST
  return **HTTP 409** with a typed body carrying the path, the token sent, and the token the file
  now has.
- When a whole-file save is sent with **no** version token, the system MUST return **HTTP 400**.
  *(An empty token is refused deliberately on the agent path today; the two doors must not
  disagree — see Ambiguity **A-1**.)*
- When a record-field write is sent with a stale token, the system MUST return **HTTP 409** with
  `code = knowledge_version_conflict`.
- When an agent submits an embed request naming a view that does not exist, the tool MUST refuse
  and the refusal text MUST enumerate the view labels that do exist.
- When an agent submits an argument that the named operation does not read, the tool MUST refuse
  **for that operation**, even if another operation reads it.
- When a record-field write arrives with no identifiable person because authentication bypass is
  active, the system MUST **accept** it and record the actor as the literal string `anonymous`
  *(founder ruling **N4**; this overrules revision 1's 503)*.
- When a record-field write arrives with **neither** an authenticated user **nor** bypass active,
  the system MUST refuse it **before the file is touched**.

**Version markers on the wire**

- Every read that a write may follow MUST return the current version marker as an **`ETag`
  response header** — on `getLibraryContent`, on `downloadLibraryFile`, and on both write
  responses. A header is required rather than a body field because one of the three consumers
  reads **raw bytes** and has no JSON body to carry one.
- A write MUST carry the marker in its request **body** as `expect_version`, not as `If-Match`, so
  that omitting it is a schema-visible 400 rather than a silently absent header.
- The version comparison and the write MUST occur inside **one** acquisition of the same lock the
  agent write path takes — same collection root, same lock directory, same collection-relative
  path. All three must match, or the two writers take different locks and the guard is decorative.

**Performance and resource bounds**

- No more than **4** view evaluations may be in flight page-wide at any instant.
- No more than **2** PDF worker instances may exist at any instant, page-wide.
- Transclusion is **one level**. There is no depth limit because there is no depth (**N1**).
- View-result answers MUST be considered fresh for **60 seconds**; view-list answers for
  **10 seconds**; neither refetches when the window regains focus.
- Returning to the application after switching away MUST NOT re-evaluate every mounted view.

**Scope boundaries**

- The system MUST NOT frame any host other than the configured allow-list, whose shipped default
  contains exactly one entry.
- The system MUST NOT mount a renderer for the `html`, `other` or `text` kinds; all three fall back
  to the link treatment.
- The system MUST NOT reach the download card from any embed.
- The system MUST NOT support any plugin notation (Dataview, Excalidraw, Kanban, Templater,
  Charts, ABC, Admonition) or Canvas, permanently.
- The system MUST NOT change any tool-permission entry, in the global ceiling or in any per-agent
  set. The expected diff for permissions is **zero lines**.

**Data constraints**

- A video identifier MUST match `^[A-Za-z0-9_-]{11}$`; anything else is not framed.
- A frame URL MUST be constructed from that identifier plus an optional integer start offset. A
  caller-supplied query string is never forwarded.
- A size given after `|` in an embed MUST match `^\d+(x\d+)?$` to be read as a size; anything else
  is display text.
- A size MUST apply to pictures only. On any other kind it is refused when written and ignored when
  read.
- A view fragment names a **display label**. The machine name MUST come from the server and MUST
  NEVER be reconstructed by the reader.
- Paths crossing from the link graph to any renderer or endpoint MUST be converted from
  knowledge-base-relative to workspace-relative first.

### Conservative Type Design

> Revision 1 cited `docs/reference/conservative-type-design.md` here. **That file does not exist,
> and neither does `docs/reference/`** — which this spec verifies two sections earlier for a
> sibling path and then failed to do for its own citation. The principle is inlined below instead:
> **a type must not carry a field its producer cannot honestly fill.** Inventing one is worse than
> omitting it, because a fabricated value type-checks, renders, and is wrong.

One new client-side type is introduced: a narrowed file reference carrying **only name and path**.

> **Revision 1 also included MIME type and text-editability, and that was the same error it was
> written to avoid.** It excluded `size` and `modified_at` on the grounds that the resolver cannot
> supply them. Measured, **it cannot supply the other two either**: both are server-sniffed values
> on the full entry type, `is_text_editable` is *required* there, neither is on a graph edge, and
> there is **no single-entry read operation** in the Library API to fetch one from.

**The consequence, stated rather than discovered:** the classifier reads both of those fields, so
an embed classifies by **file extension alone**. Six of the ten kinds — `html`, `pdf`, `audio`,
`base`, `markdown`, `mermaid` — are already extension-only and are unaffected. Three cases diverge
from the full-screen preview, and all three are named here so a test can pin them rather than a
user finding them:

| Case | Full-screen preview | Inline embed |
|---|---|---|
| An image whose filename does not say it is one | drawn as a picture | link with a badge |
| A video whose filename does not say it is one | played | link with a badge |
| A text file with an unknown or absent extension | shown as text | link with a badge |

The first two are a real loss. The third is not: text falls back to a link either way.

No other new nominal type is warranted: the embed kind reuses the existing classifier's kinds plus
one addition, and the layout variant is a two-valued string, not a type.

---

## Prerequisites

- **Hardware / OS**: macOS arm64 or Linux x86_64 for development. The kernel-sandbox features are
  irrelevant to this work; nothing here is platform-specific except the pre-existing Windows
  file-locking gap noted under Assumptions.
- **Required runtimes**: Go (go.mod requires 1.26.4; targets 1.22+), Node 20+ for the SPA and its
  test suites.
- **Required services**: none. The knowledge base is file-based (JSON/JSONL) under `~/.omnipus/`.
  No database, no cache server.
- **Network assumptions**: the application is fully offline-capable. The **only** outbound
  third-party contact this work can create is to the allow-listed video host, and only after a
  person presses play. An operator may turn that off.
- **Accounts / credentials**: none required for this feature. A model provider key is needed only
  to exercise the agent-authoring story end-to-end.

---

## Development Setup

1. `cd /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate`
2. `npm install`
3. `export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH`
4. `go mod download`
5. `make gen-contracts` — regenerates wire types from `contracts/`; must be idempotent on a clean tree
6. `npm run build && rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/`
7. `CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus ./cmd/omnipus/`
8. `export OMNIPUS_HOME=/tmp/omnipus-adr083 && rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"`
9. `OMNIPUS_BEARER_TOKEN="" /tmp/omnipus gateway --allow-empty &`

**Expected first-run behaviour**: the gateway binds port 5000 and serves the embedded SPA. A
knowledge base created through the UI appears under `$OMNIPUS_HOME`.

**Common first-run failures**:

- `build constraints exclude all Go files in .../pkg/channels/matrix` — a **missing build tag**,
  not a broken package. Always pass `-tags goolm,stdjson`, or use `make`.
- Port 5000 already bound — check `lsof -i :5000 | grep LISTEN`, or set `gateway.port` in
  `$OMNIPUS_HOME/config.json`.
- The SPA looks stale — the binary embeds `pkg/gateway/spa/`, **not** the Vite output. Re-run
  step 6. Verify with `grep -c "<a string you just added>" pkg/gateway/spa/assets/index-*.js`.
- `tsc --noEmit` exits 0 while proving nothing — the TypeScript root is a project-references root
  with no `include`. Always use `npm run typecheck`, which runs `tsc -b --noEmit`.

---

## Tech Stack

| Category | Choice | Version / Pin | Source |
|---|---|---|---|
| Backend language | Go | go.mod requires 1.26.4; targets 1.22+ | `CLAUDE.md` — Tech Stack |
| Build tags | `goolm,stdjson` | — | `Makefile` (`GO_BUILD_TAGS`) |
| Frontend | TypeScript, React 19, Vite 6 | — | `CLAUDE.md` — Tech Stack |
| UI kit | shadcn/ui (Radix + Tailwind v4), Phosphor Icons | — | `CLAUDE.md` |
| Server state | TanStack Query | — | `CLAUDE.md`; `BasePreview.tsx` query keys |
| Markdown pipeline | remark / rehype, `remark-math`, `rehype-katex` | `katex` in `package.json` | `kbMarkdownBase.tsx:263-264` |
| PDF rendering | pdf.js, module worker from the app's asset base | vendored in-tree | `LibraryPdfPreview.tsx` |
| Contracts | OpenAPI + AsyncAPI → oapi-codegen (Go), openapi-typescript + openapi-zod-client (TS) | — | `CLAUDE.md` — Constraint #8 |
| Storage | File-based JSON/JSONL under `~/.omnipus/`; atomic write (temp + rename) | — | `CLAUDE.md` — Storage |
| Test framework (Go) | `go test` with `-tags goolm,stdjson` | — | `CLAUDE.md` — Quality Gates |
| Test framework (SPA) | vitest + Testing Library | — | `npx vitest run` |
| Test framework (E2E) | Playwright | — | `tests/e2e/` |
| Datastore | **[None]** — no PostgreSQL, no Redis | — | `CLAUDE.md` |
| External APIs | **[None]** except the allow-listed video host, which is framed, never called from the server | — | ADR §D9 |

---

## Deployment / Runtime

- **Target environment**: a single Go binary with the SPA embedded, on a local workstation or a
  single-node server. Unchanged by this work.
- **Online / offline**: fully offline-capable. The only third-party contact this work introduces is
  a video frame the reader must press to load, and an operator may switch the host off entirely.
- **Resource limits**: security-feature RAM overhead stays under 10 MB beyond baseline (Hard
  Constraint #3). This work adds at most 2 PDF worker processes and 4 concurrent view evaluations
  per page.
- **Start / stop commands**: `omnipus gateway`; unchanged.
- **Health check**: unchanged. The one new operator-visible setting is the video-host allow-list.
- **Logs / telemetry**: `$OMNIPUS_HOME/logs/gateway.log`. Two new kinds of activity record appear —
  whole-file library saves (which today produce none) and record-field writes. No telemetry leaves
  the machine.
- **Rollback**: every story except the security-policy one rolls back by not using the feature — a
  renderer either routes or it does not. The security-policy change is the exception, which is why
  it gets an operator switch (US-9, acceptance scenario 6).

---

## Integration Boundaries

### The knowledge base's link graph (in-process HTTP, gateway → SPA)

- **Data in**: the note being read, and which kind of graph answer is wanted.
- **Data out**: this note's outbound links, each with how it was resolved, whether it was an embed,
  the heading fragment, whether the heading was found (**new**), the block anchor (**new**),
  whether the name was ambiguous and what else it matched; plus a list of notes skipped and why,
  and a flag saying whether the answer was cut short.
- **Contract**: `contracts/components/schemas/KnowledgeGraphEdge.yaml` and its response wrapper.
  Contract-first: schema, generate, commit generated artefacts in the same change.
- **On failure**: exactly one page-level error with the reason and a retry. Never one marker per
  embed, never silent placeholders.
- **On incomplete success**: the skip list and the truncation flag are honesty signals. If either
  covers this embed's target, the answer is "could not check", not "does not exist".
- **Development**: real service. A simulated twin would be the wrong tool here — the whole risk is
  in the honesty signals, and a twin that always answers completely would hide exactly the failure
  under test. Fixtures supply the *shapes* (a skip entry, a truncation flag, an outright failure).

### The Library file endpoints (in-process HTTP, gateway → SPA)

- **Data in**: a workspace-relative path; for a save, the full replacement content **and the version
  token the caller read** — required, no exemptions (**N2**).
- **Data out**: file content **and the current version token as an `ETag` response header** — on
  the JSON read, on the **byte-stream download**, and on both writes. Today none of the four
  returns one, in a header or a body.
- **Contract**: `LibraryContentRequest` / `LibraryBinaryContentRequest` gain `expect_version`; four
  operations gain a declared `ETag` response header; a Library-specific typed conflict body is
  added. Contract-first. **The response schemas are part of this change** — revision 1 listed only
  the request schemas, leaving the token with nowhere to come from.
- **On failure**: a stale token returns 409 with the current token so the caller can reload and
  retry deliberately. A missing or empty token returns 400.
- **Atomicity**: the comparison and the write are one step, under the agent path's lock. Without
  that, an agent write landing between them is lost behind a check that returned 200.
- **Development**: real service.

> **The byte-stream reader is why the marker is a header.** The PDF annotation editor loads its
> document with a plain `fetch` of the download URL and saves through the binary door. It receives
> no JSON, so a body field would not reach it, and under **N2** it cannot be exempted. It reads the
> `ETag` off the response it already holds and sends it back on save.

### The saved-view evaluation endpoint (in-process HTTP)

- **Data in**: the machine name of a view, and the knowledge base to evaluate it against.
- **Data out**: rows, plus any reason the view could not be served.
- **Contract**: existing. The reader must pass the **workspace-relative** path when listing views
  and the server's **machine name** verbatim when evaluating one.
- **On failure**: the embed shows the server's own reason and a manual retry. It is never retried
  automatically, and never on re-entering the viewport.
- **Development**: real service; fixtures for the failure and unservable shapes.

### The external video provider (browser → third party, framed only)

- **Data in**: an eleven-character identifier and an optional start offset, both constructed by us.
  A caller-supplied query string is never forwarded.
- **Data out**: the player.
- **Contract**: an embedded player frame on an allow-listed host, sandboxed, with no top-level
  navigation and no popups, sending no referrer.
- **On failure**: the frame fails visibly. There is no fallback path and there must not be one —
  a fallback nobody can detect hides the real defect indefinitely, which is exactly what happened
  the last time this project shipped an undetectable video fallback.
- **On refusal**: if the operator has switched the host off, the embed renders as a link.
- **Development**: **real host, gated behind an explicit test flag.** The security measurement is
  worthless against a simulated twin, because what is being measured is the browser's enforcement
  of a policy against a real cross-origin load.

### The browser's own print subsystem — NOT AN INTEGRATION IN THIS WORK (N3)

Revision 1 treated printing as a boundary with a contract (*"every embed present on the
printout"*). **It is removed.** The browser's print event is dispatched synchronously and cannot
await asynchronous work, so nothing in this work can hold a print open while modules load. There
is no handler, no end-to-end print journey, and no printout assertion.

What remains is a **statement**, not an integration: while any embed is unmounted, the reader says
on screen that find-in-page and printing will not reach it. That statement is covered by EMB-071
and its test; there is no print-subsystem contract to honour.

Making printing complete is separate, unscheduled work.

### The PDF rendering worker (in-browser background process)

- **Data in**: a document to render.
- **Data out**: rendered pages.
- **Contract**: at most 2 workers page-wide; a lease beyond that queues **visibly**; a worker that
  errors is attributed only to the documents currently leasing it, is terminated, and is removed so
  the next lease creates a fresh one.
- **On failure**: the affected document shows its own error. A healthy document must never report a
  neighbour's failure — that is a false error, the mirror image of the false success this whole
  spec exists to prevent.
- **Development**: real, in the browser test environment.

---

## BDD Scenarios

### Feature: Embedded content inside knowledge-base notes

> Scenario titles are the identifiers used throughout the TDD plan, the datasets and the
> traceability matrix. A Scenario Outline counts as one scenario; each Examples row is an
> independent test case.

---

#### Background

- **Given** a workspace containing a knowledge base
- **And** the reader is authenticated

---

### US-1 — Closing the unguarded save door

#### Scenario: Person saves a note nobody else has touched

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a note opened in the Library editor, whose version token was returned when it was read
- **And** no other writer has changed it since
- **When** the person saves the note
- **Then** the save succeeds
- **And** an activity record is written naming the file and the person
- **And** the response carries the file's new version token

---

#### Scenario: Person's save is refused because an agent wrote to the note first

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path

- **Given** a note opened in the Library editor
- **And** an agent has since written to the same note through the guarded path
- **When** the person saves the note
- **Then** the system returns HTTP 409 with a typed conflict body
- **And** the body names the path, the token that was sent, and the token the file now has
- **But** the file on disk still holds the agent's content

---

#### Scenario: Save sent with no version token at all is refused

**Traces to**: User Story 1, Acceptance Scenario 7
**Category**: Error Path

- **Given** a caller that omits the version token entirely
- **When** it sends a whole-file save
- **Then** the system returns HTTP 400
- **But** the file is unchanged

---

#### Scenario: Refused save shows the person what happened and offers a deliberate retry

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path

- **Given** a save that was refused as a conflict
- **When** the person looks at the editor
- **Then** it states that the file changed while they were editing
- **And** a retry control is offered
- **And** pressing it re-reads the current content before any further save

---

#### Scenario: A refused save is never resent by the system

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a save that was refused as a conflict
- **When** the person does nothing for the length of the editor's longest retry window
- **Then** exactly one save request has been made in total
- **And** no further save request is made until the person presses retry

---

#### Scenario: A binary save goes through the same guard

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** an annotated PDF opened from the Library
- **And** another writer has changed the same file
- **When** the person saves it
- **Then** the system returns HTTP 409
- **And** an accepted binary save writes an activity record naming the file and the person

---

#### Scenario: The reader that opens a binary file is given a version marker to send back

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** the PDF editor, which opens its document by fetching raw bytes rather than a
  structured answer
- **When** it loads a document
- **Then** the response carries the file's current version marker as a header
- **And** the editor keeps it alongside the loaded document
- **And** a subsequent save sends that exact marker back
- **And** after a successful save the marker the editor holds is replaced by the new one, so a
  second annotation pass in the same session does not conflict with the first

---

#### Scenario: A version check and its write cannot be interleaved by an agent

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a note whose current version marker a person's editor holds
- **And** a save in progress that has compared the marker and not yet written
- **When** an agent's write to the same note begins in that instant
- **Then** exactly one of the two writes is on disk afterwards
- **And** the other caller is told its write did not land
- **But** neither caller is told its write succeeded when it did not

---

### US-2 — Honest embed states

#### Scenario: An embed naming a file that does not exist says so

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** a note containing an embed of a name no file in the knowledge base carries
- **And** the link graph has loaded successfully and reports that link as unresolved
- **When** the note is read
- **Then** a visible marker appears in place of the embed
- **And** it names the target
- **And** it states that nothing in this knowledge base carries that name

---

#### Scenario: An embed naming a real file but a view that does not exist lists the views that do

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Error Path

- **Given** a note embedding a data file that exists, naming a view label it does not have
- **When** the note is read
- **Then** the marker states that the file has no view by that name
- **And** it lists the view labels the file does have

---

#### Scenario: An embed naming a real note but a heading that does not exist lists the headings that do

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Error Path

- **Given** a note embedding another note, naming a heading that note does not contain
- **And** the link graph reports the file as resolved and the heading as not found
- **When** the note is read
- **Then** the marker states that the note has no such heading
- **And** it lists the headings the note does have
- **And** the headings are fetched only because this case fired, never in advance

---

#### Scenario: A target the indexer refused to read is "could not be checked", not "does not exist"

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path

> **This is the dominant real case and revision 1 routed it to the wrong state.** A target excluded
> while the folders were being walked is absent from the index, so the answer contains a **matching
> edge marked unresolved** — not zero edges. Revision 1 watched only the zero-edge case, so this
> produced the missing-file sentence about a file the reader can open in the next pane.

- **Given** a note containing an embed of a note the indexer refused to walk into
- **And** the answer contains a **matching** edge for that embed, marked unresolved
- **And** the answer's skip list names that target, with a reason
- **When** the note is read
- **Then** the could-not-be-checked marker appears, carrying the skip reason
- **But** the missing-file sentence does not appear anywhere on the page
- **And** the same page, with the skip entry removed and nothing else changed, **does** show the
  missing-file sentence — proving the cross-check is what made the difference

---

#### Scenario Outline: Every honesty signal the server can actually emit produces the right marker

**Traces to**: User Story 2, Acceptance Scenarios 4, 5 and 6
**Category**: Edge Case

- **Given** a note containing an embed
- **And** the answer carries `<evidence>`
- **When** the note is read
- **Then** a marker distinct from the missing-file marker appears
- **And** it states that the embed could not be checked against the knowledge base
- **And** it gives `<stated reason>`
- **But** it never states that nothing is named that

**Examples**:

> **Every reason below is one the server's own skip-reason mapper can produce.** Revision 1's table
> listed `node_limit`, which has **no producer anywhere in the knowledge package** — a fixture
> built from it would have tested a shape the system cannot emit. The Go reasons are `symlink`,
> `outside_root`, `unreadable` and `irregular`; the last maps onto the wire as `not_addressable`.

| evidence | stated reason |
|---|---|
| a matching unresolved edge, and a skip entry naming the target with reason `symlink` | the target is a symbolic link and was not followed |
| a matching unresolved edge, and a skip entry with reason `unreadable` | the target could not be read |
| a matching unresolved edge, and a skip entry with reason `not_addressable` | the target's name cannot be represented on this platform |
| no matching edge, and the truncation flag set to true | the answer was cut short |
| no matching edge, no skip entry, no truncation flag | **no reason was available** |
| a matching edge marked resolved, whose target node reports that it does not exist | the knowledge base gave two different answers about this target |

---

#### Scenario: A file the indexer could not read still resolves, and that is recorded rather than hidden

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case

> The mirror of the scenario above, and the half revision 1 also had backwards. A file that failed
> while being **read** — rather than while being **walked** — is already in the index by then, so
> the link resolves normally and no marker appears at all.

- **Given** a note embedding a file that the indexer indexed and then failed to read
- **And** the answer's skip list names that file
- **When** the note is read
- **Then** the embed **resolves** and its renderer is mounted
- **And** no could-not-be-checked marker appears for it
- **And** this behaviour is recorded as known rather than asserted as desirable — the embed will
  attempt to draw a file the indexer could not read

---

#### Scenario: A knowledge base that answers with nothing produces one statement, not fifteen

**Traces to**: User Story 2, Acceptance Scenario 9
**Category**: Error Path

- **Given** a note containing fifteen embeds
- **And** a request for its links that succeeds with **no edges and no skips** — the answer given
  for a knowledge base the reader is not entitled to see
- **When** the note is read
- **Then** exactly one page-level statement appears
- **But** no per-embed marker appears
- **And** in particular the phrase "no reason available" appears at most once on the page

---

#### Scenario: An embed whose evidence has not arrived shows reserved space, not a failure

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Edge Case

- **Given** a note containing four embeds
- **And** the link graph request is still in flight
- **When** the note is read
- **Then** four correctly-sized reserved placeholders appear
- **But** no marker of any kind appears
- **And** no page-level error appears

---

#### Scenario: A failed evidence request produces one page-level error, not fifteen

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Error Path

- **Given** a note containing fifteen embeds
- **When** the link graph request fails
- **Then** exactly one page-level error appears, naming the failure and offering a retry
- **But** no per-embed marker appears
- **And** no reserved placeholder is left showing indefinitely

---

#### Scenario: A containment refusal says something different from a missing file

**Traces to**: User Story 2, Acceptance Scenario 10
**Category**: Error Path

> **The load-bearing half is the comparison, not the redaction.** "The refusal text does not contain
> the path" passes trivially when the marker is the ordinary missing-file marker — which is exactly
> what happens today, because the answer collapses both cases into one value. The two markers must
> be asserted **against each other, in one test**.

- **Given** a note, written elsewhere and synced in, whose embed names a path outside the
  knowledge base root
- **And** a second embed in the same note naming a file that simply does not exist
- **When** the note is read
- **Then** the first marker states that the embed points outside the knowledge base
- **And** the second marker states that nothing carries that name
- **And** the two markers are **different**
- **But** the first contains no segment of the escaping path
- **And** the target file is never read

---

#### Scenario: A name matching more than one file resolves and reports the ambiguity anyway

**Traces to**: User Story 2, Edge Cases — "A name matching more than one file" (US-2 has no acceptance scenario for ambiguity; the behaviour is specified in Edge Cases only, and this pointer says so rather than borrowing AS-1)
**Category**: Edge Case

- **Given** a note embedding a name that matches two files
- **When** the note is read
- **Then** the first match in the answer's order is rendered
- **And** the ambiguity is reported alongside it, naming the alternatives

---

#### Scenario: An agent reading a dashboard can tell embeds from links, and broken from working

**Traces to**: User Story 2, Acceptance Scenario 11
**Category**: Happy Path

- **Given** a note containing one working embed of a data view, one broken embed, one embed naming
  a path outside the knowledge base, and one ordinary link
- **When** an agent reads the note
- **Then** the links section marks the working embed as an embed
- **And** marks the broken one as an unresolved embed with its reason
- **And** marks the escaping one as an unresolved embed whose stated reason is that it lies outside
  the knowledge base — **so the agent is told why, not merely that something failed**
- **And** describes the data view as a view rather than as a heading
- **And** leaves the ordinary link unmarked

> **The escaping path itself IS still shown to the agent, deliberately, and this is the one place
> the two surfaces differ.** The reader's marker hides it; the agent's line does not. An agent
> reading a note is reading that note's own source, which already contains the path in plain text,
> so redacting the links section alone would be theatre and would leave the agent unable to say
> *which* embed is broken. What was unacceptable was leaving the asymmetry unstated, which is what
> revision 1 did — it applied the redaction rule to one surface and was silent on the other.

---

### US-3 — Pictures and PDFs inline

#### Scenario Outline: Each embeddable kind mounts its own renderer, and the link fallback does not appear

**Traces to**: User Story 3, Acceptance Scenarios 1, 3, 4
**Category**: Happy Path

- **Given** a note embedding a file of kind `<kind>`
- **And** the link graph resolves it
- **When** the note is read
- **Then** `<renderer>` is mounted in place
- **But** the "embed shown as a link" badge does not appear for that embed

**Examples**:

| kind | renderer |
|---|---|
| image (PNG) | the image renderer |
| image (SVG) | the image renderer |
| pdf | the PDF renderer |
| base | the saved-view renderer |
| markdown | the note transclusion renderer |

---

#### Scenario Outline: Kinds with no inline treatment keep today's link-with-a-badge

**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** a note embedding a file of kind `<kind>`
- **When** the note is read
- **Then** the "embed shown as a link" badge appears
- **But** no renderer is mounted
- **And** no download card appears

**Examples**:

| kind |
|---|
| html |
| other |
| text |

---

#### Scenario: An SVG embed is drawn as a picture and runs none of its scripts

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Edge Case

> Revision 1 wrote this scenario, mapped it to a test that asserts **which renderer mounted**, and
> left the two clauses that matter — the ones the ADR spends a paragraph on — asserted nowhere.

- **Given** an SVG file containing a script element with an observable side effect
- **And** a note embedding it
- **When** the note is read
- **Then** it is drawn inside an image element
- **And** the side effect has **not** occurred
- **And** no SVG element appears anywhere in the note's rendered document

---

#### Scenario: The same file is classified the same way inline and in the pane, except where it provably cannot be

**Traces to**: User Story 3, Acceptance Scenario 7
**Category**: Edge Case

> The full-screen preview classifies a file using what the server sniffed — its media type and
> whether it is editable as text. An embed has only the filename. Revision 1's narrowed reference
> type carried those two server-sniffed fields as though a link graph could supply them; it cannot,
> and a fabricated value would have made the two surfaces disagree **silently and upstream of every
> renderer**, where no cross-variant test can see it.

- **Given** a set of files covering all ten kinds, each with a conventional extension
- **When** each is classified for the pane and for an embed
- **Then** the two answers are equal for every one of them
- **And given** an image file, a video file and a text file, each with **no extension**
- **When** each is classified for the pane and for an embed
- **Then** the pane answers image, video and text
- **And** the embed answers "other" for all three, falling back to the link treatment
- **And** that divergence is asserted explicitly, not discovered

---

#### Scenario: A picture with a width given after a bar is drawn at that width

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a note embedding a picture with `|400` after the target
- **When** the note is read
- **Then** the picture is drawn 400 units wide
- **And** the text "400" does not appear as a caption or alternative text

---

#### Scenario: Two PDFs on one page both render, and one failing does not break the other

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Edge Case

- **Given** a note embedding two PDFs
- **And** the background worker serving the first one fails
- **When** the note is read
- **Then** the second PDF still renders its content
- **And** the first shows its own error
- **And** the failed worker is discarded so that a third PDF mounted afterwards gets a fresh one

---

#### Scenario: A third PDF beyond the worker ceiling waits visibly rather than silently

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Edge Case

- **Given** two PDF embeds already holding both available workers
- **When** a third PDF embed scrolls into view
- **Then** it shows a visible waiting state
- **And** it renders once a worker is released

---

#### Scenario: The same picture component draws the pane and the note

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the picture renderer mounted in its full-pane layout and in its inline layout with
  otherwise identical inputs
- **When** both are rendered
- **Then** the set of state identifiers and their text is equal across the two
- **And** the outermost container's layout classes differ, proving the layout variant was read

---

### US-4 — Heading and block facts reach the reader

#### Scenario: A link to a heading that exists reports the heading as found

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a note linking to a heading that exists in the target note
- **When** the note's links are requested
- **Then** the answer resolves the file
- **And** reports the heading as found

---

#### Scenario: A link to a heading that does not exist reports the heading as not found

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Error Path

- **Given** a note linking to a heading the target note does not contain
- **When** the note's links are requested
- **Then** the answer still resolves the file
- **But** reports the heading as not found

---

#### Scenario: A link to an anchored block carries the anchor separately from any heading

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a note linking to an anchored block in another note
- **When** the note's links are requested
- **Then** the answer carries the block anchor in its own field
- **And** the heading field is empty
- **And** the anchor is never compared against heading text

---

#### Scenario: The contract and the generated artefacts land together

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the two new properties added to the graph-edge specification
- **When** the repository's contract verification runs
- **Then** it passes
- **And** the generated Go and TypeScript artefacts in the change match the specification exactly

---

### US-5 — Dashboards render as data

#### Scenario: A note embedding a named view shows that view's rows

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a data file with a view labelled "Needs Daniel"
- **And** a note embedding that data file with the fragment "Needs Daniel"
- **When** the note is read
- **Then** the rows of that view appear in place
- **And** they are drawn by the same component the full-screen preview uses

---

#### Scenario: Two embeds of one data file under different view names each show their own view

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a data file with views labelled "Needs Daniel" and "Awaiting founder", whose rows differ
- **And** a note embedding the same file twice, once under each label
- **When** the note is read
- **Then** the first embed shows only the rows of "Needs Daniel"
- **And** the second shows only the rows of "Awaiting founder"

---

#### Scenario: A knowledge base mounted below the workspace root resolves correctly

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a knowledge base mounted at a path two levels below the workspace root
- **And** a second, different file of the same name nearer the workspace root
- **And** a note in the knowledge base embedding that name
- **When** the note is read
- **Then** the file inside the knowledge base is shown
- **But** the file nearer the root is never requested

---

#### Scenario Outline: A view fragment matches a display label, then case-insensitively, then a machine name

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** a data file with a view labelled "Needs Daniel" whose machine name is
  "tasks--needs-daniel"
- **And** a note embedding it with the fragment `<fragment>`
- **When** the note is read
- **Then** the result is `<outcome>`
- **And** no machine name is ever constructed by the reader

**Examples**:

| fragment | outcome |
|---|---|
| `Needs Daniel` | the view's rows are shown |
| `needs daniel` | the view's rows are shown |
| `tasks--needs-daniel` | the view's rows are shown |
| `Needs  Daniel` (double space) | the missing-view marker, listing the labels that exist |

---

#### Scenario: A data file embedded without naming a view shows the first available one and says so

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** a data file with three views
- **And** a note embedding it with no fragment
- **When** the note is read
- **Then** the first available view's rows appear
- **And** a caption names the view being shown and states that the embed did not choose one
- **But** the other views are not shown as tabs

---

#### Scenario: Two views sharing a display label are refused rather than guessed

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Error Path

- **Given** a data file with two views that share the display label "Open"
- **And** a note embedding it with the fragment "Open"
- **When** the note is read
- **Then** a marker states that the label is ambiguous
- **And** it names both views
- **But** neither view's rows are shown

---

#### Scenario: A view the server cannot serve shows the server's own reason

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Error Path

- **Given** a view the server has marked as unable to be served, with a stated reason
- **And** a note embedding it
- **When** the note is read
- **Then** that exact reason is shown in place
- **And** it is shown whether or not writing such an embed is permitted

---

#### Scenario: An embedded view's controls appear on hover and on keyboard focus, not at rest

**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Happy Path

- **Given** a note containing an embedded view
- **When** the note is read and the pointer is elsewhere
- **Then** the view's toolbar is not shown
- **And** when the pointer enters the embed, the toolbar appears
- **And** when keyboard focus enters the embed instead, the toolbar also appears

---

#### Scenario: Fifteen embeds over five data files make five view-list requests, not fifteen

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a note with fifteen embeds drawn from five distinct data files
- **When** all fifteen are mounted
- **Then** exactly five view-list requests are made
- **And** each is keyed on its data file's path

---

### US-6 — The pre-flight report

#### Scenario: The report lists every existing view embed and whether it resolves

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a knowledge base containing embeds of saved views
- **When** the report is run
- **Then** it lists each embed, the note containing it, the data file and the view label
- **And** states for each whether it resolves to a real view

---

#### Scenario: The report names an embed whose view label no longer exists

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Error Path

- **Given** a knowledge base seeded with one embed naming a view label that does not exist
- **When** the report is run
- **Then** that embed appears in the failure list
- **And** the reason is stated
- **And** the report's failure count is exactly one

---

#### Scenario: The report is produced before dashboards ship

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the dashboard story is ready to merge
- **When** the merge is proposed
- **Then** the report exists, has been run against the real knowledge base, and has been reviewed
- **And** every failure it names has either been fixed or explicitly accepted

---

### US-7 — Transclusion, one level deep

#### Scenario: A note shown inside another note appears in place

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** note A embedding note B, where B has three paragraphs
- **When** A is read
- **Then** all three of B's paragraphs appear inside A
- **And** the text is fetched through the same request the full-screen preview uses, so a note
  already open costs nothing extra

---

#### Scenario Outline: A section or a block shows only that part

**Traces to**: User Story 7, Acceptance Scenarios 2, 3
**Category**: Happy Path

- **Given** note A embedding note B with the fragment `<fragment>`
- **When** A is read
- **Then** `<shown>` appears inside A
- **But** the rest of B does not

**Examples**:

| fragment | shown |
|---|---|
| `#Results` | the Results section and its subsections |
| `#^abc123` | only the block anchored abc123 |

---

#### Scenario: Embeds inside a transcluded note render as links with a reason

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Alternate Path

> **This is now the single unqualified rule of US-7 (N1).** Revision 1 had it as one scenario among
> eight, four of which described nesting it forbids.

- **Given** note A embedding note B, where B itself embeds a picture and a data view
- **When** A is read
- **Then** B's text appears inside A
- **And** B's picture embed appears as a link with a one-line reason
- **And** B's data-view embed appears as a link with a one-line reason
- **But** neither appears as a could-not-be-checked marker
- **And** no second level of content is rendered anywhere on the page

---

#### Scenario: A note that shows itself renders once and stops, with nothing counting

**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Edge Case

- **Given** note A containing an embed of note A
- **When** A is read
- **Then** A's text appears once inside the embed
- **And** the self-embed inside that copy appears as a link with a one-line reason
- **And** the reader settles
- **But** no loop marker appears, because there is no loop detector
- **And** no depth message appears, because there is no depth counter

---

#### Scenario: A note that shows an empty note says the note is empty

**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case

- **Given** note A embedding note B, where B has no content
- **When** A is read
- **Then** the region states that the note is empty
- **But** it is not blank space with no explanation
- **And** it is not a failure marker

---

### US-8 — A forty-module dashboard stays responsive, and says what it cannot do

#### Scenario: Only modules near the viewport begin work

**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a note with forty view modules
- **When** it is opened at the top
- **Then** only the modules within the viewport and its margin have begun evaluating
- **And** the remainder occupy correctly-sized reserved space
- **And** the page does not shift under the reader as modules arrive

---

#### Scenario: Fast scrolling never exceeds four evaluations in flight

**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a note with forty view modules
- **When** the reader scrolls from top to bottom in under two seconds
- **Then** the number of evaluations in flight never exceeds four at any instant
- **And** every module beyond the fourth shows a visible waiting state until its turn
- **And** all forty eventually either render or show an error

---

#### Scenario: A module scrolled out of view before its turn gives up its place

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a queued module that has not started evaluating
- **When** it is scrolled well out of view
- **Then** it leaves the queue
- **And** the next waiting module starts instead

---

#### Scenario: A failed module is not retried by scrolling past it

**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Error Path

- **Given** a module whose evaluation failed with a server-stated reason
- **When** the reader scrolls it out of view and back into view ten times
- **Then** exactly one evaluation request was made in total
- **And** the module shows the server's reason each time
- **And** a manual retry control is offered
- **And** pressing it makes exactly one further request

---

#### Scenario: The reader states what unmounted modules cannot do, and says nothing when they can

**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Alternate Path

> **This replaces revision 1's print scenario (N3).** The guarantee that every module appears on a
> printout is withdrawn — the browser's print event cannot be held open while modules load. The
> honest statement takes its place, and now covers printing as well as find-in-page.

- **Given** a note with at least one unmounted module
- **When** the note is read
- **Then** a one-line note states that **find-in-page and printing** will not reach modules not yet
  scrolled to
- **And** when every module on the page is mounted, the note is gone
- **But** on a note containing no embeds at all, the note never appeared
- **And** all three states are asserted in one test, so "the notice never renders" cannot pass

---

#### Scenario: Returning to the application does not re-evaluate every module

**Traces to**: User Story 8, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a dashboard with fifteen mounted modules, all evaluated within the last ten seconds
- **When** the reader switches to another application and back
- **Then** no further evaluation request is made
- **And** no further view-list request is made

---

### US-9 — Allow-listed video, click to play

#### Scenario: An allow-listed video shows a locally drawn placeholder and contacts nobody

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a note containing a markdown-link embed whose host is on the allow-list
- **When** the note is read
- **Then** a placeholder with a play control appears, drawn from local assets only
- **And** zero network requests have been made to the video provider or to any image host
- **But** no player frame exists in the page

---

#### Scenario: Pressing play loads the player, once

**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the placeholder from the previous scenario
- **When** the reader presses the play control
- **Then** a player frame is created on the allow-listed host
- **And** exactly one navigation to the provider occurs
- **And** the frame carries no referrer, permits no top-level navigation and permits no popups

---

#### Scenario Outline: Only a valid identifier on an allow-listed host is framed

**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Error Path

- **Given** a note containing a markdown-link embed with destination `<destination>`
- **When** the note is read
- **Then** the result is `<outcome>`

**Examples**:

| destination | outcome |
|---|---|
| an allow-listed host with an eleven-character identifier | a placeholder with a play control |
| an allow-listed host with a ten-character identifier | a link, not framed |
| an allow-listed host with a query string of extra parameters | a placeholder; the extra parameters are not forwarded when played |
| a different video host | a link, not framed |
| an arbitrary web page | a link, not framed |
| a wikilink form naming a video identifier | a link, not framed — the wikilink form addresses files only |

---

#### Scenario: Hand-written frame markup in a note stays inert

**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Error Path

- **Given** a note whose source contains a hand-written frame element pointing at any host
- **When** the note is read
- **Then** no frame is created
- **And** the markup is not rendered as active content

---

#### Scenario: The served policy gains exactly one frame host and loses nothing

**Traces to**: User Story 9, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the application's served security policy
- **When** it is inspected after this change
- **Then** its frame-source directive lists exactly the allow-listed hosts and nothing else
- **And** its rule preventing the application from being framed is byte-for-byte unchanged
- **And** its picture-source directive is byte-for-byte unchanged
- **And** the allow-list the reader uses is equal to the allow-list in the served policy

---

#### Scenario: An operator can decline the external host

**Traces to**: User Story 9, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** an operator who sets the video-host setting to empty
- **When** the application is served
- **Then** the served policy contains no external frame host
- **And** a note containing an allow-listed video renders it as a link
- **And** no placeholder with a play control appears

---

#### Scenario: The browser measurement is re-run with a video and the framing assertion is unchanged

**Traces to**: User Story 9, Acceptance Scenario 7
**Category**: Edge Case

- **Given** the browser policy measurement suite
- **When** it is run after this change
- **Then** its positive control still produces a violation, proving the measurement can detect one
- **And** a journey containing an allow-listed video, played, produces zero frame-source violations
- **And** a journey containing a non-allow-listed host produces exactly one frame-source violation
- **And** the assertion that the application refuses to be framed passes unchanged
- **And** the security audit document has been amended in the same change

---

### US-10 — Inline record editing

#### Scenario: Changing a status from a dashboard changes the record everywhere

**Traces to**: User Story 10, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a note containing two embeds of the same view, both showing a record whose status
  field has a fixed set of allowed values
- **When** the reader picks a new value in the first embed
- **Then** the record's stored value changes
- **And** both embeds show the new value
- **And** the change went through the same lock, version check and atomic write an agent's change
  uses

---

#### Scenario: A person's edit is attributed to a person, not an agent

**Traces to**: User Story 10, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an authenticated person editing a record field
- **When** the change is saved
- **Then** an activity record is written
- **And** its actor is a reserved person identifier derived from the authenticated user's **stable
  identifier** — never a display name, never an email address
- **But** the actor is neither empty nor any agent's identifier

---

#### Scenario: An edit that nobody can be identified for is saved, and recorded as exactly that

**Traces to**: User Story 10, Acceptance Scenario 11
**Category**: Alternate Path

> **Founder ruling N4**, which overrules revision 1's refusal-with-503. The write is allowed. What
> is not allowed is recording it as though somebody had been identified.

- **Given** an installation running with authentication bypassed and no identifiable user
- **When** a person edits a record field
- **Then** the change is saved
- **And** an activity record is written whose actor is the single word `anonymous`
- **And** that actor is **not** prefixed the way an identified person's is, so the two populations
  can be separated by a plain text search over the activity file
- **But given** a request with neither an authenticated user nor bypass active
- **Then** it is refused **before the file is touched**
- **And** all three cases are asserted in one test, so neither "it refuses everything" nor "it
  accepts everything" can pass

---

#### Scenario: A record changed by someone else refuses the edit and restores the stored value

**Traces to**: User Story 10, Acceptance Scenario 3
**Category**: Error Path

- **Given** a record displayed in an embed
- **And** an agent changes the same field before the reader commits
- **When** the reader commits their change
- **Then** the system returns HTTP 409 with the typed conflict body
- **And** the field returns to the stored value
- **And** the row states that it changed while they were editing
- **And** a retry is offered with the current value visible

---

#### Scenario: A refused edit is never resent automatically

**Traces to**: User Story 10, Acceptance Scenario 4
**Category**: Edge Case

- **Given** an edit refused as a conflict
- **When** the reader takes no action for the length of the longest retry window in the application
- **Then** exactly one write request has been made in total
- **And** when the reader presses retry, exactly one further write is made, carrying the current
  version token

---

#### Scenario Outline: An editor is offered only for field types the record's definition describes

**Traces to**: User Story 10, Acceptance Scenarios 5 and 6
**Category**: Alternate Path

> **Every row in this table needs information the browser cannot get today.** What a view sends is
> a property name and a rendered string — no type, no allowed values, no indication of whether a
> field is calculated or a link. Three of this story's four contract changes exist to fill that
> gap; without them this scenario is not testable and this behaviour is not buildable.

- **Given** a record whose field is declared as `<declared type>`
- **And** an ordinary editable field on the same record, in the same view
- **When** the record is displayed in an embedded view
- **Then** `<editor>` is offered for the first field
- **And** the ordinary field **is** editable — asserted in the same test, so a component that
  renders no editors at all fails every row

**Examples**:

| declared type | editor |
|---|---|
| a fixed set of allowed values | a dropdown of the declared values |
| a date | a date input |
| free text | an inline text field |
| **a value the system calculates rather than stores** | **no editor; a way to open the note instead** |
| **a link to another record** | **no editor; a way to open the note instead** |
| a list of values | no editor; a way to open the note instead |
| anything the definition does not describe | no editor; a way to open the note instead |

---

#### Scenario: A write naming a calculated field or a link is refused by the write path too

**Traces to**: User Story 10, Acceptance Scenario 6
**Category**: Error Path

> Two independent guards, deliberately. The browser must not offer the editor; the write path must
> refuse the write even if something else offers it. Revision 1 had neither — it could not evaluate
> the first, and it routed around the second.

- **Given** a request to write a value the system calculates
- **When** it reaches the write path
- **Then** it is refused, not honoured
- **And given** a request to write a link between records through the same path
- **Then** that too is refused, and the caller is told which path does accept it
- **But** a request naming an ordinary field on the same record succeeds

---

#### Scenario: A record's name and location are never editable here

**Traces to**: User Story 10, Acceptance Scenario 7
**Category**: Edge Case

- **Given** a record shown in an embedded view
- **And** an ordinary editable field on the same row
- **When** the reader attempts to edit its title or its path
- **Then** no editor is offered for either
- **And** a way to open the note is offered instead
- **But** the ordinary field on that same row **is** editable, in the same test

---

#### Scenario: A view filter set inside an embed is local, temporary, and invisible elsewhere

**Traces to**: User Story 10, Acceptance Scenarios 8 and 9
**Category**: Happy Path

- **Given** a note containing two embeds of the same view
- **When** the reader applies a filter to the first embed
- **Then** only the first embed's rows change
- **And** the second embed is unaffected
- **And** the shared view definition on disk is byte-for-byte unchanged
- **And** reloading the page loses the filter

---

#### Scenario: A successful field write refreshes only what went stale, and only for that note

**Traces to**: User Story 10, Acceptance Scenario 10
**Category**: Edge Case

- **Given** a page with embeds drawn from two different knowledge bases
- **When** a field is written on a record in the first
- **Then** the view results for the first knowledge base are refreshed
- **And** the written note's own content, outline and links are refreshed
- **But** no other note's content, outline or links are refetched
- **And** nothing belonging to the second knowledge base is refetched

---

### US-11 — Agent-authored embeds

#### Scenario: An agent asks for an embed and the correct notation is written for it

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a note, a data file and a view label that all exist
- **When** an agent asks to embed that view into that note under a named section
- **Then** the embed line is written under that section
- **And** the section is created if it did not exist
- **And** no file other than the named note is written

---

#### Scenario: An agent naming a view that does not exist is refused and told what does

**Traces to**: User Story 11, Acceptance Scenario 2
**Category**: Error Path

- **Given** a data file whose views are labelled "Open" and "Closed"
- **When** an agent asks to embed the view "Pending"
- **Then** the request is refused
- **And** the refusal lists "Open" and "Closed"
- **But** the note is unchanged

---

#### Scenario: An agent naming a target outside the knowledge base is refused before anything is written

**Traces to**: User Story 11, Acceptance Scenario 3
**Category**: Error Path

- **Given** an agent naming a target that escapes the knowledge base root
- **When** it asks to embed
- **Then** the request is refused
- **And** the note is unchanged
- **And** no such line is ever written, rather than written and reported broken forever

---

#### Scenario Outline: Modifiers are checked against the target's kind

**Traces to**: User Story 11, Acceptance Scenario 4
**Category**: Error Path

- **Given** an agent asking to embed a target of kind `<kind>` with modifier `<modifier>`
- **When** it makes the request
- **Then** the result is `<outcome>`

**Examples**:

| kind | modifier | outcome |
|---|---|---|
| picture | a width | accepted |
| sound file | a width | refused, naming the reason |
| video file | a width | refused, naming the reason |
| data file | a width | refused, naming the reason |
| data file | a view label that exists | accepted |
| note | a heading that exists in the target | accepted |
| note | a heading that does not exist in the target | refused, listing the headings that do |

---

#### Scenario: An argument this operation does not read is refused for this operation

**Traces to**: User Story 11, Acceptance Scenario 5
**Category**: Error Path

- **Given** the note-editing tool, whose operations between them accept a superset of arguments
- **When** the linking operation is called with a width
- **Then** the call is refused
- **And** when the embed operation is called with a body
- **Then** that call is refused too
- **But** each argument is still accepted by the operation that genuinely reads it

---

#### Scenario: An agent that omits the version token is refused

**Traces to**: User Story 11, Acceptance Scenario 6
**Category**: Error Path

> **Added in revision 3 (M2).** US-11 AS-6 was the one acceptance scenario in the document with a
> test (37) and two dataset rows (H9, H10) and **no BDD scenario**. Test 37 traced instead to *"An
> agent asks for an embed"*, whose Then-clauses say nothing about a token — so the obligation was
> asserted by a test that pointed at a scenario which did not assert it.

- **Given** an agent asking to embed, with the target, the destination and the modifier all valid
- **When** it makes the request with **no** version token at all
- **Then** the request is refused and nothing is written
- **And** when it makes the same request with an **empty** version token
- **Then** that request is refused too, for the same stated reason
- **And** when it makes the same request carrying the **current** token
- **Then** the request succeeds and exactly one line is written
- **And** the refusal names the missing token rather than naming the operation as unknown

---

#### Scenario: The permission surface does not change at all

**Traces to**: User Story 11, Acceptance Scenario 7
**Category**: Edge Case

- **Given** a fresh installation and a pre-existing installation
- **When** the embed capability ships
- **Then** the set of knowledge tool names in the global permission ceiling is exactly the eight
  that exist today
- **And** no per-agent permission set gains or loses an entry
- **And** the reconciliation pass that heals older installations adds nothing for this work

---

### US-12 — Deferred kinds

#### Scenario Outline: Sound and video play in place using the shared component

**Traces to**: User Story 12, Acceptance Scenarios 1, 2
**Category**: Happy Path

- **Given** a note embedding a file of kind `<kind>`
- **When** the note is read
- **Then** it plays in place
- **And** the component drawing it is the same one the full-screen preview uses

**Examples**:

| kind |
|---|
| audio |
| video |

---

#### Scenario: The extracted sound component exists exactly once

**Traces to**: User Story 12, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the sound renderer, extracted from the preview pane into its own module
- **When** the source tree is inspected
- **Then** exactly one definition of it exists
- **And** the full-screen preview and the inline embed both mount that one definition

---

#### Scenario: A PDF page fragment shows that page

**Traces to**: User Story 12, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a note embedding a PDF with a page number
- **When** the note is read
- **Then** that page is shown
- **And** the surrounding pages are not

---

#### Scenario: An embed inside a code fence draws nothing

**Traces to**: User Story 3, Edge Cases — "Embed notation inside a fenced code block" (US-3 AS-6 is the *link-with-a-badge* outcome, which is the opposite of the fence's "no marker of any kind"; the fence behaviour is specified in Edge Cases only)
**Category**: Edge Case

- **Given** a note whose fenced code block contains embed notation
- **When** the note is read
- **Then** the notation appears as literal code text
- **And** no renderer is mounted
- **And** no marker of any kind is drawn
- **And** the same holds for embed notation inside inline code

---

## Contract-First Work (Hard Constraint #8)

Every byte that crosses the gateway/SPA boundary is defined in `contracts/` **before** any Go or
TypeScript is written. The order is always: edit the specification, run `scripts/gen-contracts.sh`,
commit the generated artefacts **in the same commit** as the specification change, then write the
handler or consumer against the generated type only. `make verify-contracts` fails on drift.

There are **seven** contract changes in this work.

> **Revision 1's header said "three", its own body then said "a fourth may be required", and the
> real count is seven.** That is not bookkeeping. Under Constraint #8 the contract determines merge
> order, so an undercount produces a plan that cannot be executed in the order it states. Four of
> the seven were found by measuring rather than by reading: **three of them are prerequisites of
> P0 or P1 stories whose acceptance scenarios are otherwise not implementable at all.**

| # | Change | Files | Belongs to | Must land before |
|---|---|---|---|---|
| **CW-1** | `expect_version` on both whole-file save requests; **an `ETag` response header declared on `getLibraryContent`, `downloadLibraryFile`, `putLibraryContent` and `putLibraryContentBinary`**; a Library-specific typed conflict body; **400 and 409 on both write operations** | `LibraryContentRequest.yaml`, `LibraryBinaryContentRequest.yaml`, new `LibraryConflictError.yaml`, plus the four operations in `contracts/openapi.yaml`. **Two non-contract files are named here because CW-1 is unimplementable without them (revision 3): `src/lib/api.ts::request`, the SPA's single API entry point, which discards `res.headers` and therefore cannot deliver the declared header to any consumer (EMB-007c); and `pkg/gateway/inline_serving.go`, because the header must land in `handleLibraryDownload` and NOT in the shared `serveLibraryContent`/`applyLibraryByteHeaders` helpers, whose `http.ServeContent` call implements conditional GET and `If-Range` for every caller (EMB-007b).** | US-1 (step 0) | any code in US-1 |
| **CW-2** | `heading_found` (boolean), `block` (string) **and `unresolved_reason` (enum: `no_match`, `outside_root`)** on the graph edge | `KnowledgeGraphEdge.yaml` | US-4 (step 1c) | **US-2's containment marker**, US-2's heading marker, US-5's edge key, US-7 |
| **CW-3** | The video-host allow-list on the settings/state payload the reader already fetches | the settings/state schema in `contracts/` | US-9 (step 4) | any reader code that decides whether to draw a play control |
| **CW-4** | Wire `RecordSchema` / `VaultRecord` to a path, so the browser can read a record's field declarations | `contracts/openapi.yaml` (the schemas already exist) | US-10 (step 5) | any editor-gating code in US-10 |
| **CW-5** | Cell metadata on the view answer: declared type, enum members, and `derived` / `relation` flags | `VaultFindCell.yaml` | US-10 (step 5) | EMB-088; without it the browser cannot tell a calculated field from an ordinary one |
| **CW-6** | `version_token` on a view row | `VaultFindRow.yaml` | US-10 (step 5) | EMB-086; the alternative is one extra read per edit **and** a fresh race window between it and the write |
| **CW-7** | Wire `RecordWriteRequest` to a path, with 409 → `KnowledgeConflictError` | `contracts/openapi.yaml` (both schemas already exist; **this is the first wiring of either**) | US-10 (step 5) | any write code in US-10 |

**Why CW-4 through CW-7 are four changes and not one.** All four target schemas are already
written and already generated. What none of them has is a **path**: grepping `openapi.yaml` for
`#/components/schemas/RecordSchema`, `…/VaultRecord`, `…/RecordWriteRequest` and
`…/KnowledgeConflictError` returns **zero usages each**. The entire typed-record layer is
agent-tool-only on the wire today. Revision 1 proposed two brand-new schemas instead and never
noticed the built ones sitting unreachable — which would have produced a second record-write
contract beside the one that already carries the guards.

**Four traps specific to contract work here**, all previously observed in this repository:

1. `make verify-contracts` proves the generated artefacts match the specification. It proves
   **nothing** about whether a handler populates a new field. CW-2 therefore needs a behavioural
   pair per field — a heading that exists and one that does not, an anchor present and absent, a
   refusal for absence and one for containment — because a handler that hardcodes a zero value
   passes contract verification completely.
2. The generated runtime validator on the SPA side can be **weaker than the contract**. Nested
   inline objects do not get strict checking, and no schema language expresses a cross-field rule.
   Probe the validator **with a control**: a payload that should be rejected, plus one that should
   be accepted. Two earlier attempts to verify this exact property produced false results.
3. Discriminated unions must be hosted inline in `openapi.yaml`; external file references inside a
   `oneOf` generate non-compiling accessors. None of CW-1…CW-7 needs one, and none should
   introduce one.
4. **A response header is a wire format and must be declared, not just set.** CW-1's `ETag` is not
   an implementation detail the handler may add quietly — `openapi.yaml` declares response headers
   natively (there is existing precedent in this file), and an undeclared header is a
   hand-written wire format under Constraint #8. There is a working `ETag` implementation in
   `pkg/gateway` already, on the providers catalogue, to follow for shape.

---

## Tool Policy (Hard Constraint #6)

**Expected change to tool policy: NONE. Zero lines, in both layers.**

| Layer | Expected change | Why |
|---|---|---|
| Global ceiling (`pkg/config/defaults.go`) | **none** | Embed authoring is an operation on the existing `knowledge_edit` tool, which already carries `"allow"` in the ceiling. No new static tool name is introduced, so no new ceiling entry is required. |
| Per-agent overrides (`pkg/coreagent/core.go` seeds) | **none** | Every seeded agent already carries an explicit value for `knowledge_edit`. |
| Reconciliation on load (`config.ReconcileToolPolicyCeiling`) | **none added** | It heals a ceiling forward when a *new static builtin tool* appears in defaults. Nothing new appears. |

The live knowledge tool catalogue is and remains exactly eight names: `knowledge_describe`,
`knowledge_find`, `knowledge_read`, `knowledge_list`, `knowledge_edit`, `knowledge_restructure`,
`knowledge_configure`, `knowledge_base_create`. `knowledge_link` and `knowledge_set_property` are
retired and have zero ceiling entries; they are not a precedent to follow in form, only in
principle.

**The zero-line claim is independently verified and stands.** Exactly eight `knowledge_*` keys in
the ceiling, all `allow`; `knowledge_link` and `knowledge_set_property` absent from the ceiling,
from every per-agent seed and from the registry; `AuthoringTools()` has zero production callers and
is dead code. Four coupled Go catalogues exist and are mutually enforced by tests; **no contracts
enum and no SPA list of tool names exists**, so adding an operation to an existing tool needs no
entry anywhere.

**Two accepted costs, and the second is larger than the first.**

1. **Granularity.** Because embed authoring shares one policy name with note editing, an operator
   cannot grant one and withhold the other. That is the deliberate trade the consolidation made;
   the alternative is the family of near-synonymous tools this codebase finished retiring.
2. **The new REST write door sits outside the tool-policy system entirely** — and this is part of
   *why* the diff is zero, which the tidiness framing above obscures. Tool policy governs tools.
   US-10 adds a **REST endpoint**, so an operator who has set `knowledge_edit: deny` for every
   agent on their installation **still has a human write door into the knowledge base, and no
   policy setting can close it.** That is not an argument against the endpoint — the Library
   already has whole-file write doors on exactly the same footing — but it is an **ungoverned
   surface**, and recording it as such is different from presenting a zero-line diff as a saving.

**The test that pins the first claim**, and why it must assert set equality rather than membership:

> `TestKnowledgeToolPolicy_CatalogueUnchangedByEmbedOp` — assert the set of `knowledge_*` keys in
> the global ceiling is **exactly** the eight names, that no per-agent seed map gains or loses a
> key, and that running the reconciliation pass over a config written before this work produces a
> byte-identical `sandbox.tool_policies` for the knowledge group.

A test asserting only *"`knowledge_edit` is present in the ceiling"* passes whether or not a ninth
tool was added, which is exactly the regression it is supposed to catch. Set equality is the
assertion; membership is not.

> **This test is a second net over an existing one, not the only one, and that must be said.**
> `TestCatalog_MatchesGlobalCeilingEntryForEntry` already asserts the ceiling matches the static
> tool catalogue entry for entry, and `pkg/coreagent/constructor_seed_test.go` already asserts every
> per-agent seed's key set matches the catalogue. The set-equality reasoning above is sound and the
> test is worth having — but a completion report must not present it as the sole guard, because a
> regression here would already have failed two other tests first.

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Pure functions and single components: notation parsing, the resolver's state decision, the walk-skip cross-check, label→machine-name matching, path conversion, argument validation | Validates the decision logic in isolation, with the evidence supplied as a fixture so every honesty signal can actually be produced |
| Integration | A mounted note reader with a stubbed transport; a booted gateway handler with a real file store | Validates that components agree — that a resolver decision reaches the right renderer, that a handler's refusal reaches the editor, that an audit record actually lands |
| E2E | The real binary with the embedded SPA, driven in a browser | Validates the things only a browser can answer: security policy enforcement, network contact before and after a click, and scroll-driven mounting |

> **Two scopes were removed from this table in revision 3, and the removal is the point.**
> The Unit row listed *"the cycle set"* and the E2E row listed *"printing"*. Both name work that
> **no longer exists**: N1 deleted cycle detection in full (EMB-056/057/058, tests 53–56) and N3
> deleted the print guarantee in full (EMB-070, test 88). These three rows are the first thing an
> implementer reads before writing a test, so two of them were instructing work that had been
> deleted three sections below. **Completeness check: SC-031**, and it is deliberately **scoped
> rather than a document-wide string ban** — no live requirement, numbered test row, dataset row,
> success criterion or row of this table may specify a cycle set, a depth cap or a print guarantee.
> A global ban would also forbid EMB-071's on-screen statement, which names find-in-page **and
> printing** as what will not reach unmounted embeds — and that statement is N3's *replacement* for
> the guarantee, not a residue of it. The three per-scope commands are printed under SC-031; each
> must return nothing. **Re-measured on revision 3: 0 in each of the five scopes.**

### Test Implementation Order

Write these before the implementation. Within each step: unit, then integration, then end-to-end.

> **Every SPA test file below carries a directory, and that is not cosmetic.** Revision 1 named
> ~30 new SPA test files as bare filenames. The vitest matrix is a **hardcoded `VITEST_PATTERNS`
> list inside `.github/workflows/pr.yml`**; a test file in a directory no pattern matches is
> **silently skipped while the job reports green**, which is how 116 of 422 files (27%) once never
> ran. `scripts/check-vitest-coverage.mjs` is the guard, and it cannot guard a file whose location
> nobody decided. `.github/workflows/pr.yml` is in the Impact Assessment for this reason.

> **Numbering keeps its gaps.** Tests deleted in revision 2 keep their numbers as struck-through
> rows rather than being renumbered, so every `Traces to:` line and every traceability row above
> and below stays valid, and so a reader of the review can find what happened to each one. New
> tests start at 97.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| **Step 0 — closing the save door** |
| 1 | `TestLibraryContentPut_MissingVersionReturns400` | Integration | Save sent with no version token at all is refused | A save with no token is refused; the file is unchanged. **The empty-string and absent-field cases are both asserted** |
| 2 | `TestLibraryContentPut_StaleVersionReturns409WithCurrentToken` | Integration | Person's save is refused because an agent wrote first | Stale token → 409, typed body naming path, sent token and current token |
| 3 | `TestLibraryContentPut_FreshVersionSucceedsAndReturnsNewToken` | Integration | Person saves a note nobody else has touched | Happy path, and the **response header** carries the new token |
| 4 | `TestLibraryContentBinaryPut_StaleVersionReturns409` | Integration | A binary save goes through the same guard | The binary door has the same guard as the text door |
| 5 | `TestLibraryContentPut_WritesAuditRecordOnDefaultInstall` | Integration | Person saves a note nobody else has touched | Boots the **real** default wiring and reads the record back from the sink — not an injected stub |
| 6 | `TestLibraryContentBinaryPut_WritesAuditRecordOnDefaultInstall` | Integration | A binary save goes through the same guard | Same, for the binary door |
| 7 | `src/components/library/preview/useLibraryFileEditor.conflict.test.tsx::sends-the-token-it-read` | Unit | Person saves a note nobody else has touched | The editor sends back the token it received, not an empty string |
| 8 | `src/components/library/preview/useLibraryFileEditor.conflict.test.tsx::surfaces-conflict-without-resending` | Unit | A refused save is never resent by the system | One request before, one request after the longest retry window, one more only on press. **Two clauses added in revision 3 (X7 sweep), because the first three pass today**: `useLibraryFileEditor` uses `useMutation` with no `retry` option (default 0) and a failed save leaves the draft dirty, so "one, still one, one more on press" is satisfiable by today's generic error path with no conflict handling at all. The test MUST additionally assert **(a)** the surfaced state is the **conflict** state carrying the server's current token, distinguishable from the existing generic save error, and **(b)** the retry request carries the **fresh** token from the 409 body, not the token the first attempt sent |
| **97** | `TestLibraryContentGet_ReturnsVersionHeader` | Integration | The reader that opens a binary file is given a version marker | The JSON read sets the header. Today `handleLibraryContentGet` sets **no headers at all**, so this test fails before the change and cannot pass by accident. **Three rows, added in revision 3 (C3(r2)):** a **text** file; a **binary** file, whose response body carries no `content` field at all; and a **`too_large`** text file, likewise. The last two are the ones that catch a token derived from `library.ContentResult.Content` — which is omitted by design in both cases, so such a token would be the hash of an empty string, identical for every binary file in the workspace |
| **98** | `TestLibraryDownload_ReturnsVersionHeader` | Integration | The reader that opens a binary file is given a version marker | The **byte-stream** read sets the same header — the one the PDF editor can actually read |
| **99** | `TestLibraryContentPut_CompareAndWriteAreAtomicUnderAgentWrite` | Integration | A version check and its write cannot be interleaved by an agent | **The C7 test.** A seam releases an `EditNote` between the handler's comparison and its write; assert exactly one write survives and the loser is told. Sequential stale-token tests (2, 4) pass against a lock-free implementation — this one does not |
| **100** | `TestLibraryContentPut_TakesTheSameLockKeyAsTheAgentPath` | Integration | A version check and its write cannot be interleaved by an agent | Asserts the lock is taken with the **same collection root, lock directory and relative path** the agent path uses. Two writers taking two different locks pass test 99 by luck on a fast machine; this pins the key itself |
| **101** | `src/components/library/preview/LibraryPdfPreview.version.test.tsx::reads-token-from-download-and-sends-it` | Integration | The reader that opens a binary file is given a version marker | The PDF loader stores the header value and `handleSave` sends it. **Paired with**: a stale-token save surfaces a conflict; and a second save in the same session uses the token from the first save's response, not the original load's. **The mechanism is now named (M6)**: the *loader* already holds a raw `Response` (`fetchPdfBytes`, `credentials: 'include'`) and reads the header off it directly — but the *save* goes through `src/lib/api.ts::request`, **which returns the parsed body and discards `res.headers` entirely**. This test MUST exercise the chosen mechanism from EMB-007c, not a stub that hands the component a header the production path cannot obtain |
| **121** | `TestLibraryVersionToken_IdenticalAcrossBothReadDoors` | Integration | The reader that opens a binary file is given a version marker | **The C3(r2) test that catches two token definitions.** For the same file, the token in `getLibraryContent`'s header and the token in `downloadLibraryFile`'s header are **byte-identical**, and both equal `knowledge.ReadNoteVersion`'s `Token` for that note. Run over three files: a markdown note inside a knowledge base, a PDF inside one, and a file **outside** every knowledge base. Without this row two doors can each be "correct" and disagree, and the disagreement only ever surfaces as a 409 nobody can clear |
| **123** | `TestLibraryContentPut_QuotedTokenInBodyIsRejectedWith400` | Integration | Save sent with no version token at all is refused | **The M5 shape test.** The header is the RFC-quoted strong form; `expect_version` is the **bare** token. A body sending the **quoted** form is rejected with **400**, never 409 — so a client that forgot to strip the quotes gets a shape error it can act on rather than an endless conflict it cannot. **Paired in the same body:** the bare form on the same file succeeds |
| **124** | `TestLibraryContentPut_LockKeyUsesTheInnermostEnclosingCollection` | Integration | A version check and its write cannot be interleaved by an agent | **The M7 test.** A knowledge base nested inside another knowledge base: the lock key names the **innermost**, and it matches the key the agent path takes for the same file. Test 100 asserts the key matches "the agent path's" without saying which collection that is when two apply — this row decides it. **Paired:** a file with **no** enclosing collection takes the degraded in-process-only path, asserted explicitly rather than by the absence of a failure |
| **Step 1 — resolver, renderers, PDF pool, agent authoring** |
| 9 | `src/components/library/knowledge/embed/embedNotation.test.ts::fragment-then-size` | Unit | A picture with a width given after a bar | Fragment parsed before size; a non-numeric payload stays display text |
| 10 | `src/components/library/knowledge/embed/embedNotation.test.ts::size-refused-on-non-image` | Unit | Modifiers are checked against the target's kind | A size on a non-picture is ignored at read time |
| 11 | `src/components/library/knowledge/embed/embedResolver.test.ts::external-url-recognised-before-graph` | Unit | Only a valid identifier on an allow-listed host is framed | An external destination never consults the graph and never reports a miss. **Paired (M4):** the same test asserts the placeholder **is** rendered for that destination — otherwise "never reports a miss" passes on a reader that recognises nothing |
| 12 | `src/components/library/knowledge/embed/embedResolver.test.ts::edge-key-includes-fragment-and-block` | Unit | Two embeds of one data file under different view names | Two edges with identical link text separate on fragment |
| 13 | `src/components/library/knowledge/embed/embedResolver.test.ts::state-resolved-unresolved-loading-failed-indeterminate` | Unit | Outline: every honesty signal the server can actually emit | All five states produced from **evidence shapes the server can emit** — see datasets B5/B6/B6a. A fixture built from revision 1's rows tested nothing |
| 14 | `src/components/library/knowledge/embed/embedResolver.test.ts::indeterminate-never-says-no-such-file` | Unit | Outline: every honesty signal the server can actually emit | Asserts the missing-file sentence is **absent** from every indeterminate outcome, **paired in the same test** with a positive assertion that the could-not-be-checked marker **is** present with its reason |
| 15 | `src/components/library/knowledge/embed/embedResolver.test.ts::honours-truncation-flag` | Unit | Outline: every honesty signal the server can actually emit | Truncation reported → indeterminate, even though today's server never sets it here |
| 16 | `src/components/library/knowledge/embed/embedResolver.test.ts::converts-collection-path-below-root` | Unit | A knowledge base mounted below the workspace root | Fixture mounted **two levels below** root, with a same-named decoy nearer the root asserted never requested; a root-mounted fixture proves nothing |
| 17 | `src/components/library/knowledge/embed/embedResolver.test.ts::containment-marker-differs-from-missing-file-marker` | Unit | A containment refusal says something different from a missing file | **Renamed and rewritten (C3, M4).** Revision 1's version asserted only that the refusal text lacks the path — which passes when the marker **is** the missing-file marker, which is what happens today. Two edges in one fixture, both unresolved, one with `unresolved_reason: outside_root`: assert the markers **differ**, then assert the redaction |
| 18 | `src/components/library/knowledge/embed/embedResolver.test.ts::ambiguous-resolves-and-reports` | Unit | A name matching more than one file | First in order rendered; alternatives reported |
| **102** | `src/components/library/knowledge/embed/embedResolver.test.ts::unresolved-with-a-skip-entry-is-indeterminate` | Unit | A target the indexer refused to read is "could not be checked" | **The C2 test, and the most important new one in step 1.** A **matching unresolved** edge plus a skip entry naming the target → could-not-be-checked. **Paired:** the identical fixture with the skip entry removed **does** produce the missing-file sentence, so the cross-check is proved to be what made the difference |
| **103** | `src/components/library/knowledge/embed/embedResolver.test.ts::skip-matching-rule-path-basename-and-ancestor` | Unit | A target the indexer refused to read is "could not be checked" | The stated matching rule, **all three clauses (C1(r2), revision 3)**: (1) a skip whose `path` equals `to_path`; (2) a skip whose **basename** equals `to_path`, with and without a markdown extension; (3) **a skip naming an ancestor DIRECTORY of `to_path`** — the clause revision 2 was missing, and the one that covers the dominant walk-level shape. **Four near-misses that must NOT suppress:** a skip naming a different file; a skip naming a **sibling** directory; a skip whose path is a **string prefix but not a path-segment prefix** (`notes/priv` against `notes/private/plan.md`); and a **basename** that coincides with a directory name, which must not be treated as a directory match. Revision 2's version tested only the two clauses that already worked, so it would have passed with the directory case broken |
| **104** | `src/components/library/knowledge/embed/embedResolver.test.ts::node-exists-disagreement-is-indeterminate` | Unit | Outline: every honesty signal the server can actually emit | **The M13 test.** A resolved edge whose target node reports `exists: false` → indeterminate; and the reverse. A resolver that reads only the edge passes neither. **Paired** with an agreeing pair that must resolve normally |
| **105** | `src/components/library/knowledge/embed/embedResolver.test.ts::scan-level-skip-still-resolves` | Unit | A file the indexer could not read still resolves | The measured mirror: a skip entry for a file that stayed in the index does **not** suppress resolution. Pins today's real behaviour so a future "fix" cannot quietly change it without a decision |
| **106** | `src/components/library/knowledge/KnowledgeNoteView.embeds.test.tsx::empty-answer-is-one-page-level-statement` | Integration | A knowledge base that answers with nothing produces one statement | Fifteen embeds, an answer with zero edges and zero skips → **exactly one** statement; the phrase "no reason available" appears at most once |
| 19 | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::each-kind-mounts-its-renderer` | Integration | Outline: each embeddable kind mounts its own renderer | **The named test from the ADR.** Positive per kind, plus the badge asserted absent |
| 20 | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::html-other-text-fall-back-to-link` | Integration | Outline: kinds with no inline treatment | **PIN OF EXISTING BEHAVIOUR — never evidence that step 1 landed (X7, revision 3).** Badge present, no renderer, no download card. Measured on `def10b90e`: `remarkKbWikilinks` converts an embed to an image node **only** when the extension is in `IMAGE_EXTENSIONS`; every other embed already renders a wikilink node with the badge `embed shown as a link`. **All three clauses hold today**, so this test passes on the first run with zero implementation. It is kept as a regression pin and is excluded from step 1's X7 "must fail" list and from SC-028 |
| 21 | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::no-download-card-reachable` | Integration | Outline: kinds with no inline treatment | **REWRITTEN (X7 + M8, revision 3). The revision-2 form was vacuous in both halves**: `LibraryDownloadCard` is not reachable from the note reader today, and a `.zip` already renders the badge — so the negative and its named pairing *both* passed before any implementation, and the pairing could not rescue it. The real new content is: **the new renderer dispatch never routes to the download card.** Assert it against a kind that **does** mount a renderer after the change (`.pdf`): the PDF renderer is present **and** no download card is anywhere in the document. The `.pdf` half fails today, which is what makes the pair meaningful |
| 22 | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::embed-in-code-fence-mounts-nothing` | Integration | An embed inside a code fence draws nothing | Fenced and inline code both inert. **Paired (M4), and the kind is now NAMED (M8, revision 3): the pairing uses a `.pdf` embed.** With an image the pairing was vacuous — an image embed outside a fence already mounts today, so both halves passed before any implementation. A `.pdf` does not render today, so the out-of-fence half fails until step 1 lands, and the fenced half is then a real contrast rather than a restatement of the status quo |
| **107** | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::svg-drawn-as-image-and-script-inert` | Integration | An SVG embed is drawn as a picture and runs none of its scripts | **PIN OF EXISTING BEHAVIOUR — never evidence that step 1 landed (X7, revision 3).** An SVG carrying a `<script>` with an observable side effect: renders inside `<img>`, the side effect did **not** occur, no `<svg>` element is in the document. **Measured on `def10b90e`: `'svg'` is already in `IMAGE_EXTENSIONS` (`knowledgeMarkdown.tsx`, the `IMAGE_EXTENSIONS` set), so an SVG embed already becomes an image node and all three clauses hold today.** The property is real and worth pinning — it is the SVG-script threat in the STRIDE table — but the test proves nothing about **this** work and a phase-exit report citing it would be truthful and worthless. Excluded from step 1's X7 "must fail" list and from SC-028 |
| **108** | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::raw-frame-markup-is-inert` | Integration | Hand-written frame markup in a note stays inert | **The M7 test for the scenario that had no test at all.** A note whose source contains `<iframe src="https://…">` produces **no** `<iframe>`. **Paired:** an allow-listed markdown-link embed in the same document **does** produce a placeholder. The property rests entirely on the markdown pipeline having no raw-HTML plugin — true today and one line away from not being |
| **109** | `src/components/library/preview/libraryPreviewKind.agreement.test.ts::inline-and-pane-agree-except-where-named` | Unit | The same file is classified the same way inline and in the pane | **The C4 test.** All ten kinds with conventional extensions: both classifiers agree. Then the three extensionless cases: assert the divergence **explicitly**. Without this, EMB-027's central property is checked nowhere — the cross-variant tests hold props fixed and cannot see a divergence that happens upstream of the renderer |
| 23 | `src/components/library/knowledge/KnowledgeNoteView.embeds.test.tsx::page-level-error-not-fifteen-markers` | Integration | A failed evidence request produces one page-level error | Exactly one error node; zero markers; zero permanent placeholders |
| 24 | `src/components/library/knowledge/KnowledgeNoteView.embeds.test.tsx::loading-shows-placeholder-not-marker` | Integration | An embed whose evidence has not arrived | Placeholder present, marker absent |
| 25 | `src/components/library/preview/LibraryImagePreview.variant.test.tsx::cross-variant-states-equal` | Unit | The same picture component draws the pane and the note | State identifiers and text equal; container class exempt; **plus a control asserting the container class differs**, proving the variant was read |
| 26 | `src/components/library/preview/BasePreview.variant.test.tsx::cross-variant-states-equal` | Unit | The same picture component draws the pane and the note | Same assertion for the saved-view renderer, including its "N views could not be loaded" notice |
| 27 | `src/components/library/preview/LibraryPdfPreview.variant.test.tsx::cross-variant-states-equal` | Unit | The same picture component draws the pane and the note | Same assertion for the PDF renderer |
| 28 | `src/components/library/preview/LibraryPdfPreview.pool.test.tsx::two-documents-render-concurrently` | Integration | Two PDFs on one page both render | **PIN — passes today (X7, revision 3, found by this round's own sweep).** Two mounted documents both reach a rendered page. Measured: `LibraryPdfPreview` constructs `new Worker(\`${ASSET_BASE}pdf.worker.min.mjs\`)` **inside its own load effect**, i.e. one worker per component instance, so two mounted documents already both render. **Every clause of 28, 29 and 30 is satisfied by one-worker-per-document — which is the absence of a pool.** They are kept as pins and excluded from the X7 "must fail" list; **test 31 is the only one of the four that can detect whether a pool exists** |
| 29 | `src/components/library/preview/LibraryPdfPreview.pool.test.tsx::failure-attributed-to-one-document-only` | Integration | Two PDFs on one page both render | **PIN — passes today (X7, revision 3).** A worker error rejects only its own leases; the healthy document still renders. With a per-document worker this is trivially true. The register named this trap in revision 2 and then left the test inside a check saying every test in the phase must fail — the contradiction is resolved here in favour of the measurement |
| 30 | `src/components/library/preview/LibraryPdfPreview.pool.test.tsx::poisoned-worker-evicted-and-replaced` | Integration | Two PDFs on one page both render | **PIN — passes today (X7, revision 3).** "A third mount after a failure gets a fresh worker, not the dead one" is unconditionally true when every mount gets its own worker. To become a real assertion it must be stated against the pool: **the failed worker is terminated, the pool's constructed-worker count increments, and the replacement is not the terminated instance** — all three only meaningful once a shared pool exists |
| 31 | `src/components/library/preview/LibraryPdfPreview.pool.test.tsx::third-lease-queues-visibly` | Integration | A third PDF beyond the worker ceiling waits visibly | **The load-bearing test of the pool, and the only one of 28–31 that fails today.** A visible waiting state, never a silent wait, **and the count of constructed workers asserted ≤ 2**. The worker count is what proves a pool exists; the waiting state alone is satisfiable without one |
| 32 | `TestKnowledgeEditEmbedOp_WritesOnlyTheNamedNote` | Unit | An agent asks for an embed | One file written; the section created if absent |
| 33 | `TestKnowledgeEditEmbedOp_UnknownViewRefusedListingWhatExists` | Unit | An agent naming a view that does not exist | Refusal enumerates the real labels |
| 34 | `TestKnowledgeEditEmbedOp_TargetOutsideCollectionRefused` | Unit | An agent naming a target outside the knowledge base | Refused before any write. **REPAIRED (X7, revision 3): assert the refusal MESSAGE, and pair it with a success.** `op: "embed"` does not exist today — `EditTool.Execute`'s `default:` branch calls `refuseOp` — so "the request is refused" **passes right now, for the wrong reason**, and a test that cannot tell *"unknown operation"* from *"target outside the collection"* is not testing containment. The test MUST assert the refusal names containment, **and** in the same body a **valid** `op="embed"` request that **succeeds**, so "everything is refused" fails |
| 35 | `TestKnowledgeEditEmbedOp_ModifierKindMismatchRefused` | Unit | Scenario Outline: modifiers are checked against the target's kind | Width on a non-picture refused, not ignored. **REPAIRED identically (X7, revision 3):** `refuseOp` refuses this today. Assert the refusal names the **kind mismatch**, and pair it in the same body with a width on a **picture** that is **accepted** |
| 36 | `TestKnowledgeEdit_PerOpArgumentSetRejectsForeignArgs` | Unit | An argument this operation does not read | `link` + width refused; `embed` + body refused; each still accepted by its own operation. **Sharpened (X7-adjacent, revision 3):** the first two clauses pass today for reasons unrelated to a per-operation sweep — `width` is not in `editArgNames`, so the **global** `unknownArgs` sweep already refuses `link` + width, and `embed` + body hits `refuseOp`. The test MUST therefore assert that the refusal names **the operation** ("`link` does not read `width`"), not merely that some refusal occurred, and the "still accepted by its own operation" clause (`embed` + width accepted) is the half that cannot pass today |
| 37 | `TestKnowledgeEditEmbedOp_MissingExpectVersionRefused` | Unit | **An agent that omits the version token is refused** | The version token is mandatory, and an empty one is refused too. **Repointed (M2, revision 3)** at the scenario that actually asserts it — revision 2 traced this to *"An agent asks for an embed"*, whose Then-clauses say nothing about a token. **REPAIRED (X7, revision 3):** `refuseOp` refuses this today too. Assert the refusal names the **missing token**, and pair it with the same request **carrying the current token**, which must succeed |
| 38 | `TestKnowledgeToolPolicy_CatalogueUnchangedByEmbedOp` | Unit | The permission surface does not change at all | **PIN / THIRD NET — passes today by design (X7, revision 3).** Set equality on the ceiling; no seed change; reconciliation adds nothing. **All three are true now** — verified: exactly eight `knowledge_*` keys in `pkg/config/defaults.go` — and two existing tests already assert the ceiling and the seeds entry-for-entry. The reasoning for keeping it is sound (a third net against a ninth tool appearing), but it is a pin: excluded from the X7 "must fail" list and from SC-028, and never citable as evidence that step 1 landed |
| **Step 1c — the wire gains three facts** |
| 39 | `TestKnowledgeEdge_HeadingFoundTrueAndFalse` | Integration | A link to a heading that exists / does not exist | The **pair**. A handler hardcoding `false` fails the first half |
| **110** | `TestKnowledgeEdge_HeadingFoundIsFalseForBaseAndBlockTargets` | Integration | An embed naming a real note but a heading that does not exist | **The M3 test, NARROWED to one surface (M4, revision 3).** A **Go** handler test over `knowledgeEdge`: `heading_found` is `false` by construction for a `.base` target and for a block reference — the field is only ever set for a **markdown** target with a heading fragment. **Paired:** a markdown target with a heading fragment that IS found returns `true`, so a handler hardcoding `false` fails. Fails today because the field does not exist. *(Revision 2's row carried this Go name at Integration level while stating a **React** assertion — "the reader's 'no such heading' marker is absent". One test cannot be both; the reader half is now test 122.)* |
| **122** | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::no-heading-marker-for-base-and-block` | Integration | An embed naming a real note but a heading that does not exist | **The reader half of EMB-039 (M4, revision 3), which had no test on the surface the rule governs.** For a `.base` target with a view fragment, and for a markdown target with a block anchor, the "no such heading" marker is **absent**. **Paired in the same fixture — mandatory, and this is why:** as an unpaired negative it passes on a reader that never renders that marker, which is today's reader. The same document therefore contains a **markdown** target whose heading is genuinely missing, asserted to **render** the marker. Added to SC-027's pairing list |
| 40 | `TestKnowledgeEdge_BlockAnchorProjectedSeparately` | Integration | A link to an anchored block | The anchor is its own field; the heading field is empty. **Note:** this is the first assertion `BlockID` has ever had — it is parsed today and read by nothing |
| **111** | `TestKnowledgeEdge_UnresolvedReasonDistinguishesAbsenceFromContainment` | Integration | A containment refusal says something different from a missing file | **The C3 handler test.** One link to a name that does not exist, one to a path outside the root: both `unresolved`, with **different** `unresolved_reason` values. A handler that hardcodes either value fails one half |
| 41 | `src/lib/api/wireContracts.test.ts::graph-edge-validator-accepts-and-rejects` | Unit | The contract and the generated artefacts land together | Probes the generated runtime validator **with a control** on both sides |
| **120** | `TestKnowledgeRead_WalkSkippedTargetIsCouldNotBeCheckedNotAbsent` | Integration | A target the indexer refused to read is "could not be checked" | **The C2(r2) test — the agent half of the honesty guarantee, which revision 2 specified on the reader only.** A note whose embed targets a file under a directory the walk could not list: `knowledge_read`'s rendered links section MUST carry the skip reason and MUST NOT read as absent. **Three rows, one per EMB-021 clause** (path equality, basename equality, ancestor prefix), **plus a near-miss** naming a sibling directory that must still read as absent. **Paired:** an ordinary unresolved link in the same note still reads as absent, so "everything is could-not-be-checked" fails. Fails today: `ReadLink` has ten fields and no place for a reason, and `renderReadLinks` prints `"  %s (unresolved) %s"` for every unresolved link regardless of why |
| **Step 2 / 2m — dashboards** |
| 42 | `src/components/library/knowledge/embed/baseViewMatch.test.ts::label-then-caseless-then-machine-name` | Unit | Outline: a view fragment matches a display label | The three-step ladder, plus a near-miss that must fail |
| 43 | `src/components/library/knowledge/embed/baseViewMatch.test.ts::duplicate-label-refused-naming-both` | Unit | Two views sharing a display label | Refused; both named; neither rendered |
| 44 | `src/components/library/knowledge/embed/baseViewMatch.test.ts::machine-name-never-constructed` | Unit | Outline: a view fragment matches a display label | Asserts the reader emits no derived machine name for an unmatched label. **Paired (M4):** the same test asserts that a **matched** label does produce the server's machine name verbatim — otherwise "emits no derived name" passes on a reader that emits nothing |
| 45 | `src/components/library/preview/BasePreview.inline.test.tsx::named-view-renders-its-own-rows` | Integration | A note embedding a named view | Rows asserted, not the machine name string |
| 46 | `src/components/library/preview/BasePreview.inline.test.tsx::two-views-one-file-do-not-cross` | Integration | Two embeds of one data file under different view names | Two views with **different rows**; each embed asserted against its own |
| 47 | `src/components/library/preview/BasePreview.inline.test.tsx::no-fragment-shows-first-and-says-so` | Integration | A data file embedded without naming a view | Caption asserted present; tabs asserted absent |
| 48 | `src/components/library/preview/BasePreview.inline.test.tsx::unservable-view-shows-server-reason` | Integration | A view the server cannot serve | The server's exact reason string |
| 49 | `src/components/library/preview/BasePreview.inline.test.tsx::toolbar-on-hover-focus-and-cell-editor-always` | Integration | An embedded view's controls appear on hover and on focus | Absent at rest; present on hover; present on keyboard focus; **present after a tap on the header strip** (A-4's touch path). **Fifth assertion added in revision 3 (M11), and it is the one that makes A-5 real: a cell editor is reachable by keyboard WHILE the toolbar is hidden.** Without it nothing in this plan catches an implementer who reads EMB-046 and hides the editors along with the chrome — the exact mistake A-5 was raised about, on the exact surface founder ruling D-D governs |
| 50 | `src/components/library/preview/BasePreview.inline.test.tsx::view-list-deduped-by-file-path` | Integration | Fifteen embeds over five data files | Exactly five requests counted |
| 51 | `TestEmbedMigrationReport_NamesEveryFailure` | Integration | The report names an embed whose view label no longer exists | Seeded fixture with one deliberate miss; failure count asserted **exactly one** |
| **Step 3 — transclusion, one level** |
| 52 | `src/components/library/knowledge/embed/transclusion.test.tsx::note-section-and-block-slices` | Integration | Outline: a section or a block shows only that part | Slicing runs client-side against fetched text |
| ~~53~~ | ~~`transclusion.test.tsx::self-cycle-reports-loop-not-depth`~~ | — | — | **DELETED (N1).** Its subject does not exist: with one level there is no loop detector. See test 112 for what replaces it |
| ~~54~~ | ~~`transclusion.test.tsx::two-cycle-reports-loop-at-level-two`~~ | — | — | **DELETED (N1).** A two-cycle cannot be constructed — B's embed of A is a nested embed and renders as a link |
| ~~55~~ | ~~`transclusion.test.tsx::diamond-is-not-a-cycle`~~ | — | — | **DELETED (N1).** A diamond collapses to two links |
| ~~56~~ | ~~`transclusion.test.tsx::depth-cap-is-visible`~~ | — | — | **DELETED (N1).** Depth never exceeds 1, so a five-level cap can never fire |
| 57 | `src/components/library/knowledge/embed/transclusion.test.tsx::nested-embeds-fall-back-with-a-reason` | Integration | Embeds inside a transcluded note render as links with a reason | **Now the load-bearing test of US-7.** Link fallback with a reason; **and** the could-not-be-checked marker asserted absent; **and** no second level of content anywhere on the page |
| 58 | `src/components/library/knowledge/embed/transclusion.test.tsx::reuses-the-existing-content-cache-key` | Integration | A note shown inside another note appears in place | An already-open note costs no extra request |
| **112** | `src/components/library/knowledge/embed/transclusion.test.tsx::self-embed-renders-once-and-settles` | Integration | A note that shows itself renders once and stops | A embeds A: text appears **once**, the inner self-embed is a **link**, the render settles. Asserted by **counting rendered copies**, not by a timeout — a deadline-based "it did not hang" assertion is one of the two clock-based traps this repository has already been bitten by |
| **113** | `src/components/library/knowledge/embed/transclusion.test.tsx::empty-note-says-it-is-empty` | Integration | A note that shows an empty note says the note is empty | A stated empty region, not blank space and not a failure marker |
| **Step 4 — the external video host** |
| 59 | `TestSpaCsp_FrameSrcAllowListMatchesServedHeader` | Unit | The served policy gains exactly one frame host | Parses the **served header**, not a constant compared to itself |
| 60 | `TestSpaCsp_DirectiveFloor` (extended) | Unit | The served policy gains exactly one frame host | Existing floor test extended; its self-mutation half must cover the new value |
| 61 | `TestSpaCsp_FrameAncestorsAndImgSrcUnchanged` | Unit | The served policy gains exactly one frame host | **PIN OF EXISTING BEHAVIOUR (X7, revision 3).** Byte-for-byte assertions on the two directives that must not move. **A test asserting that a value equals its current value passes before the change** — both directives are in `spaContentSecurityPolicy` today, unchanged. It is a genuine guard **after** the change (it fails if the edit disturbs a neighbouring directive), and worthless as first-run evidence. Excluded from step 4's X7 "must fail" list and from SC-028 |
| 62 | `TestSpaCsp_OperatorCanDeclineTheHost` | Unit | An operator can decline the external host | **Paired (M4/M2), both halves in ONE test body:** the **default** install's served header contains the host **and** matches the literal line in `adr-067-knowledge-base-and-preview-spec.md` §10.7; the **emptied-config** install's does not contain it and **differs** from that line. The negative half alone passes on a build where the host was never added |
| 63 | `src/components/library/knowledge/embed/youtubeEmbed.test.tsx::identifier-validation-and-url-construction` | Unit | Outline: only a valid identifier on an allow-listed host | Eleven-character rule; query parameters not forwarded |
| 64 | `src/components/library/knowledge/embed/youtubeEmbed.test.tsx::facade-is-local-and-frame-absent` | Unit | An allow-listed video shows a locally drawn placeholder | Placeholder present (positive), frame absent, zero external requests |
| 65 | `src/components/library/knowledge/embed/youtubeEmbed.test.tsx::press-creates-exactly-one-frame` | Unit | Pressing play loads the player, once | Frame created with the exact sandbox, referrer and permission attributes |
| 66 | `tests/e2e/csp-assumptions.spec.ts::A7-youtube-embed-played` | E2E | The browser measurement is re-run with a video | New journey; zero frame-source violations after play |
| 67 | `tests/e2e/csp-assumptions.spec.ts::A8-non-allowlisted-host-violates` | E2E | The browser measurement is re-run with a video | **The negative control** — proves the measurement can still detect a violation. **Its disposition MUST be decided before it is written (X7, revision 3), and it is decided here: A8 frames the non-allow-listed host DIRECTLY, the way the existing A0 positive control does, NOT through the new embed path.** A control that depends on the feature under test is not a control — it goes silent exactly when the feature is broken. Framed directly, A8 violates today (`frame-src 'self'`) and after (the host is not on the allow-list), so it is a **pin** that must pass **both** before and after, alongside test 68. Both are in SC-028's exclusion list |
| 68 | `tests/e2e/csp-assumptions.spec.ts::A6-unchanged` | E2E | The browser measurement is re-run with a video | Existing framing assertion re-run verbatim |
| 69 | `tests/e2e/knowledge-embeds.spec.ts::no-provider-contact-before-play` | E2E | An allow-listed video shows a locally drawn placeholder | Network log asserted empty for the provider, then exactly one entry after press |
| **114** | `TestSpaPdfWorkerCsp_InheritsTheFrameHost` | Unit | The served policy gains exactly one frame host and loses nothing | The **derived second policy** served on the PDF worker path picks up the new host. Asserts the served header on **both** paths rather than assuming one string. Revision 1 listed neither the second string nor its existence |
| **Step 5 — inline record editing** |
| 70 | `TestKnowledgeRecordFieldWrite_GoesThroughTheTypedRecordPath` | Integration | Changing a status from a dashboard | **Renamed (C5).** Same lock, same compare-and-swap, same atomic write as the agent path — **through the typed record-write contract**, not a raw frontmatter splice |
| **115** | `TestKnowledgeRecordFieldWrite_RefusesDerivedAndRelationProperties` | Integration | A write naming a calculated field or a link is refused by the write path too | **The C5(d) test.** A request naming a calculated property is refused; one naming a relation is refused and names the path that does accept it; **one naming an ordinary property on the same record succeeds.** All three together, so "it refuses everything" fails |
| 71 | `TestKnowledgeRecordFieldWrite_StaleTokenReturns409` | Integration | A record changed by someone else refuses the edit | Typed conflict body with the current token |
| 72 | `TestKnowledgeRecordFieldWrite_HumanActorRecordedOnDefaultInstall` | Integration | A person's edit is attributed to a person | Real boot wiring; actor read **out of the audit record**, not out of the request the test built; it is the stable user id, neither empty nor an agent id |
| ~~73~~ | ~~`TestAuditEventNames_KnowsKnowledgeEvents`~~ | — | — | **DELETED.** Its subject already exists: all five `knowledge.*` names are already registered in `IsValidEventName`, and `pkg/knowledge/audit_event_names_test.go` already carries this exact assertion **with the prescribed negative control**. The test would have been green on day one with no implementation. Replaced by task 116 |
| **116** | *(not a test)* correct the stale comment in `pkg/knowledge/audit.go` | — | — | The comment above the `EventKnowledgeNote*` constants still says the audit package "does not yet know" these names and that they "belong in `pkg/audit/events.go`'s switch". Every clause is now wrong, and it is the source of the deleted requirement, the deleted test and a deleted step-5 precondition |
| 74 | `TestKnowledgeRecordFieldWrite_ActorUnderBypassIsAnonymous` | Integration | An edit that nobody can be identified for is saved, and recorded as exactly that | **Rewritten (N4).** Revision 1 asserted a 503. Now: bypass + no user → the write **succeeds** and records the literal actor `anonymous`; an authenticated write records the prefixed user form; **neither** present → refused **before the file is touched**. Three cases, one test |
| **117** | `TestAuditActors_AnonymousIsGreppableAsAClass` | Integration | An edit that nobody can be identified for is saved, and recorded as exactly that | **The N4 obligation — REWRITTEN (X7, revision 3), because as specified it could not fail in ANY implementation.** Revision 2 had the test **write the fixture** activity file itself and then assert a property of strings the test had just produced: no production code was exercised at all, so the assertion was about the test's own data. The rewrite: the three rows MUST be **produced by the production actor-formatting path** — drive three real record-field writes on real boot wiring (an agent write, an authenticated person's write, and a bypass write with no identifiable user), then read the activity file **the product wrote** and assert a plain text search for the anonymous token returns **exactly** the anonymous row. That is why the level moves from Unit to Integration. If the product ever prefixes the anonymous actor, or drops the prefix from identified ones, this version dies and the old one would not have noticed |
| 75 | `src/components/library/preview/viewparts/recordFieldEditor.test.tsx::editor-offered-only-for-described-types` | Unit | Outline: an editor is offered only for field types described | **Paired (C5, M4).** Every negative row is asserted **alongside a writable enum in the same fixture that IS editable**. Covers: a calculated field, a relation, a list, and a type the definition does not describe |
| 76 | `src/components/library/preview/viewparts/recordFieldEditor.test.tsx::title-and-path-never-editable` | Unit | A record's name and location are never editable here | **Paired (M4):** both asserted absent **while an ordinary field on the same row is asserted editable** — otherwise this passes on a component that renders no editors anywhere |
| 77 | `src/components/library/preview/viewparts/recordFieldEditor.test.tsx::conflict-reverts-and-never-auto-retries` | Integration | A refused edit is never resent automatically | One request; still one after the window; exactly one more on press, carrying the fresh token |
| 78 | `src/components/library/preview/embeddedViewState.test.tsx::filter-is-local-and-never-written-back` | Integration | A view filter set inside an embed is local | Second embed unaffected; the definition on disk asserted byte-identical; reload loses it |
| 79 | `src/components/library/preview/embeddedViewState.test.tsx::invalidation-is-scoped-to-the-written-note` | Integration | A successful field write refreshes only what went stale | Second knowledge base asserted untouched |
| **118** | `src/components/library/preview/embeddedViewState.test.tsx::both-embeds-of-one-view-show-the-new-value` | Integration | Changing a status from a dashboard changes the record everywhere | **The m7 gap.** Two embeds of the same view on one page; a write in the first is visible in the **second**. Test 70 asserts the write path and test 79 asserts invalidation is scoped — neither asserts the user-visible half of the requirement |
| **Step 6 — deferred kinds** |
| 80 | `src/components/library/preview/LibraryAudioPreview.test.tsx::single-definition-used-by-both-surfaces` | Unit | The extracted sound component exists exactly once | Same module identity asserted from the pane and from the embed |
| 81 | `scripts/check-no-duplicate-renderer.sh` | Unit | The extracted sound component exists exactly once | A source guard, in the narrow "no second copy exists" sense only — see the false-green register |
| **119** | `scripts/check-no-duplicate-renderer.test.sh` | Unit | The extracted sound component exists exactly once | **The guard's own self-test (M6), following this repository's existing four-script convention** (`check-browser-tests-gated.test.sh`, `check-no-handwritten-wire-types.test.sh`, `check-no-tool-error-from-status.test.sh`). Plant a duplicate definition in a temp tree, assert non-zero exit; remove it, assert zero. **A grep guard with a typo'd pattern exits 0 forever — that is the 673/673 failure verbatim** |
| 82 | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::audio-video-pdf-page` | Integration | Outline: sound and video play in place | Deferred kinds routed to their own renderers. **`mermaid` was struck from this test's name (ADR-083 §15, N8):** an embedded `.mmd` FILE is not a deferred kind awaiting step 6, it is permanently refused, so it must never be asserted to route to a renderer. See test 126 for the refusal that IS asserted |
| **126** | `src/components/library/preview/knowledgeMarkdown.diagramEmbed.test.tsx` | Integration | N8: an embedded diagram file is deliberately not rendered | **Already written and passing** — this row was missing while C8 still said "Deferred". Asserts a resolved `![[diagram.mmd]]` renders as a working LINK and mounts no diagram renderer, that the target is still classified honestly as `mermaid` (the refusal is a rendering decision, not a lie about the file), **and — in the same note — that a fenced ` ```mermaid ` block still draws.** The two halves share one test file on purpose: neither can be satisfied by breaking the other |
| **Cross-cutting — mount budget and regression** |
| 83 | `src/components/library/knowledge/embed/embedLazyMount.test.tsx::only-visible-modules-begin-work` | Integration | Only modules near the viewport begin work | **Counts started evaluations** and asserts reserved space by test identifier; never a screenshot |
| 84 | `src/components/library/knowledge/embed/embedLazyMount.test.tsx::four-in-flight-ceiling-with-visible-queue` | Integration | Fast scrolling never exceeds four evaluations | In-flight count sampled at every scheduler tick; waiting state asserted. **Count, never a clock** |
| 85 | `src/components/library/knowledge/embed/embedLazyMount.test.tsx::queued-module-scrolled-away-releases-slot` | Integration | A module scrolled out of view before its turn | Slot released; next module starts |
| 86 | `src/components/library/knowledge/embed/embedLazyMount.test.tsx::failed-module-not-retried-by-scrolling` | Integration | A failed module is not retried by scrolling past it | One request after ten round trips; manual retry makes exactly one more |
| 87 | `src/components/library/knowledge/embed/embedLazyMount.test.tsx::staleness-and-focus-settings-inherited` | Integration | Returning to the application does not re-evaluate | Both windows (60 s, 10 s) asserted; no refetch on focus |
| ~~88~~ | ~~`knowledge-embeds.spec.ts::print-mounts-every-module`~~ | — | — | **DELETED (N3).** The guarantee it asserted is withdrawn: the browser's print event cannot be held open while modules load. Replaced by test 89's third assertion |
| 89 | `src/components/library/knowledge/embed/embedLazyMount.test.tsx::unmounted-limitation-stated-and-withdrawn` | Integration | The reader states what unmounted modules cannot do | **Three states in one test:** notice present while unmounted and naming **both** find-in-page and printing; absent when all are mounted; **never present on a note with no embeds**. A one-state assertion passes on a notice that never renders |
| 90 | `src/components/library/preview/knowledgeMarkdown.test.tsx::link-fallback-still-fires` (extended) | Integration | Outline: kinds with no inline treatment | **Regression — PIN OF EXISTING BEHAVIOUR (X7, revision 3).** The existing tested badge behaviour still fires for the kinds that reach it. It passes before and after, **correctly** — that is what a regression test is for. Revision 2 left it outside every X7 list **and** outside SC-028's exclusion list, so it would have been reported as an unexplained first-run pass. It is now inside the cross-cutting X7 check, explicitly as an expected pass, and inside SC-028's exclusion list |
| **125** | `src/components/library/preview/knowledgeMarkdown.embeds.test.tsx::rendering-kinds-no-longer-reach-the-fallback` | Integration | Outline: each embeddable kind mounts its own renderer | **The half of the regression that test 90 cannot carry (revision 3).** Test 90 proves the fallback still fires for kinds that *should* reach it; nothing proved it **stops** firing for the kinds this work routes away from it. For `.pdf`, `.base` and `.md`, the badge `embed shown as a link` is asserted **absent** — which fails today, because all three currently take the fallback. Together, 90 and 125 are the pair; 90 alone passes with the whole feature reverted |

### Test Datasets

#### Dataset A — Embed notation: target, fragment and the bar payload

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| A1 | `![[Tasks.base#Needs Daniel]]` | Happy path | target `Tasks.base`, fragment `Needs Daniel`, no size | BDD: A note embedding a named view | The founder's actual shape, 75 times over |
| A2 | `![[photo.png\|400]]` | Happy path | target `photo.png`, size 400 | BDD: A picture with a width given after a bar | Size, not an alias |
| A3 | `![[photo.png\|400x300]]` | Max form | target `photo.png`, size 400×300 | BDD: A picture with a width given after a bar | Both dimensions |
| A4 | `![[photo.png\|Diagram of the flow]]` | Non-numeric | target `photo.png`, display text, **no size** | BDD: A picture with a width given after a bar | The bar is an alias unless it is digits |
| A5 | `![[Plan#Q3\|400]]` | Fragment + size | fragment `Q3` read first, size 400 second | BDD: A picture with a width given after a bar | Order matters; this is the order observed in the source application |
| A6 | `![[song.mp3\|400]]` | Kind mismatch | size ignored at read time; refused at write time | BDD Outline: modifiers are checked against the target's kind | Copying a control that does nothing is worse than not offering it |
| A7 | `![[Tasks.base#]]` | Empty fragment | treated as no fragment → first-view behaviour with a caption | BDD: A data file embedded without naming a view | Empty is not a label |
| A8 | `![[]]` | Empty | nothing is drawn; no marker | Edge Cases — "Degenerate notation" (there is no BDD scenario for `![[]]`; the code-fence scenario asserts nothing about it, and revision 2 traced here only because both outcomes are "nothing is drawn") | Degenerate notation is not an error to report |
| A9 | `![[Tasks.base#Needs Daniel#Extra]]` | Two fragments | first `#` splits; the rest is part of the fragment text | BDD Outline: a view fragment matches a display label | Will not match any label; falls to the missing-view marker |
| A10 | `![[café.png]]` (composed) vs `![[café.png]]` (decomposed) | Unicode | both resolve to the same file, or **both** report could-not-check — never one each | BDD Outline: every honesty signal the server can actually emit produces the right marker | Normalisation mismatch is a live producer of the indeterminate state |
| A11 | `![[Note#^abc123]]` | Block anchor | anchor `abc123`, heading empty | BDD: A link to an anchored block | The anchor is never compared against heading text |
| A12 | A 4,000-character target | Very long | no crash; falls to the missing-file marker | BDD: An embed naming a file that does not exist | Bounded rendering |
| A13 | `![[../outside/secret.md]]` | Escaping | refused; message contains no path | BDD: A containment refusal says something different from a missing file | Read-time containment |

#### Dataset B — The resolver's evidence, and the state it must produce

| # | Input (evidence supplied as a fixture) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| B1 | Graph loaded; one edge, embed true, link text and fragment both match, resolution `exact_path` | Happy path | **resolved** → mount the renderer | BDD Outline: each embeddable kind mounts its own renderer | |
| B2 | Graph loaded; matching edge with resolution `unresolved` | Error | **unresolved** → missing-file marker naming the target | BDD: An embed naming a file that does not exist | |
| B3 | Graph request in flight | Loading | **loading** → reserved placeholder, no marker | BDD: An embed whose evidence has not arrived | |
| B4 | Graph request failed with a network error | Failure | **graph_unavailable** → one page-level error with retry | BDD: A failed evidence request produces one page-level error | |
| **B5** | Graph loaded; **a MATCHING edge marked `unresolved`**; skip list names this target with reason `symlink` | Indeterminate | **indeterminate** → "could not be checked", reason stated | BDD: A target the indexer refused to read | **CORRECTED.** Revision 1 said "zero matching edges" — measured, a walk-level skip removes the target from the index, so a link **to** it produces a *matching unresolved* edge. Revision 1's row described a shape the system does not emit, so a fixture built from it tested nothing. **This is the dominant live producer** |
| **B5a** | Same as B5, with the skip entry **removed** and nothing else changed | Control | **unresolved** → missing-file marker | BDD: A target the indexer refused to read | The paired control. Without it, B5's expected output could be produced by a reader that never says "nothing is named that" at all |
| **B5b** | Matching unresolved edge; skip entry naming a **different** file | Near miss | **unresolved** → missing-file marker | BDD: A target the indexer refused to read | The cross-check must match on the target, not merely notice that some skip exists |
| **B5c** | Matching unresolved edge for `notes/private/plan.md`; **the only skip entry names the DIRECTORY `notes/private`**, reason `unreadable` | **Boundary — the dominant walk-level shape** | **indeterminate** → "could not be checked", **naming the directory** in the reason | BDD: A target the indexer refused to read | **NEW (C1(r2), revision 3). This is the shape revision 2's correction missed.** `WalkContained`'s `ReadDir` error branch records the skip under `RelPath: cur.rel` — the **directory** — and every file beneath it never enters `walk.Files`. Clauses 1 and 2 of EMB-021 both miss (`notes/private` ≠ `notes/private/plan.md`; `private` ≠ `plan`), so without the ancestor clause the reader still says *"Nothing in this knowledge base is named plan"* about a file that exists. **No fixture in revision 2 exercised this** — B5/B5a/B5b all use `symlink` or a same-file skip |
| **B5d** | Identical to B5c with the directory skip entry **removed** | Control | **unresolved** → missing-file marker | BDD: A target the indexer refused to read | The paired control for B5c. Without it, B5c's expected output is producible by a reader that never says "nothing is named that" at all |
| **B5e** | Matching unresolved edge for `notes/private/plan.md`; skip entry names the **sibling** directory `notes/public`; and a second run where it names `notes/priv` (a string prefix that is not a path-segment prefix) | Near miss ×2 | **unresolved** → missing-file marker in **both** | BDD: A target the indexer refused to read | The ancestor clause must match on a full path-segment boundary. A prefix match without the boundary check suppresses honest absence reports for any target whose path happens to start with a skipped directory's name |
| **B5f** | Matching unresolved edge for a target inside `.trash/` or `.obsidian/`; the answer's skip list is **empty** | Deliberate non-producer | **unresolved** → missing-file marker | BDD: A target the indexer refused to read | `contain.go`'s `mode.IsDir()` branch skips `scanSkippedDirNames` (`.obsidian`, `.omnipus-vault`, `.git`, `.trash`) with `continue` and **appends nothing to `Skipped`** — deliberately, with the reason stated in place. So there is no skip entry, absence is reported correctly, and the ancestor clause must not be "fixed" by adding these names to the skip list |
| **B6** | Graph loaded; **the edge RESOLVES normally**; skip list names that file with reason `unreadable` (a scan-level failure) | Resolved | **resolved** → the renderer mounts | BDD: A file the indexer could not read still resolves | **CORRECTED.** Revision 1 expected `indeterminate`. Measured, a scan-level skip is appended *after* the resolution index is built from the walk's file list, so the target stays indexed and the link resolves. That behaviour is recorded, not endorsed — the embed will try to draw a file the indexer could not read |
| **B6a** | Matching unresolved edge; skip reason `not_addressable` | Indeterminate | **indeterminate**, reason stated | BDD Outline: every honesty signal the server can actually emit produces the right marker | The wire value the gateway maps the Go `irregular` reason onto |
| B7 | Graph loaded; zero matching edges; truncation flag true | Indeterminate | **indeterminate**, reason "the answer was cut short" | BDD Outline: every honesty signal the server can actually emit produces the right marker | Not reachable on this branch; the client honours it regardless |
| **B7a** | Graph loaded; **a MATCHING unresolved edge**; no skip; truncation flag true | Indeterminate — **the second shape** | **indeterminate**, reason "the answer was cut short" — **never the missing-file marker** | BDD Outline: every honesty signal the server can actually emit produces the right marker | **NEW (O1, revision 3).** B7 covers only the zero-edge shape, so revision 2's truncation honouring was unreachable in the shape that matters: a matching unresolved edge on an admittedly incomplete answer fell through to an assertion of absence. Not live today — `resp.Truncated` is set only in the neighbourhood branch — which is precisely why the client must handle it |
| B8 | Graph loaded; zero matching edges; no skip, no truncation | Indeterminate | **indeterminate**, "no reason available" | BDD Outline: every honesty signal the server can actually emit produces the right marker | The key-mismatch case; must **never** say "no such file". Realistically the marker will have **no reason to give** here, which is honest and must not be dressed up |
| **B8a** | Graph loaded with **zero edges and zero skips**; fifteen embeds on the page | Empty answer | **one page-level statement**, not fifteen markers | BDD: A knowledge base that answers with nothing produces one statement | The answer the server gives for a knowledge base outside the caller's scope — a deliberate success-with-nothing so a refusal cannot confirm it exists |
| **B8d** | Graph loaded with **zero edges and zero skips** for a note that **is** inside a valid, indexed collection but was itself not indexed | Empty answer — **the second producer** | **one page-level statement** whose wording says the knowledge base **returned no link information for this note**, and does **not** assert why | BDD: A knowledge base that answers with nothing produces one statement | **NEW (m8, revision 3).** EMB-014's trigger fires for two different causes and the reader cannot tell them apart. Wording that diagnoses the cause ("this knowledge base returned nothing") would be a false claim about a note that exists and has fifteen embeds. State what was observed, never why |
| **B8b** | Matching edge, resolution `exact_path`; the matching node reports `exists: false` | Contradiction | **indeterminate**, reason "two different answers" | BDD Outline: every honesty signal the server can actually emit produces the right marker | Today's code already refuses to hand out a URL in this case; revision 1's model dropped the check |
| **B8c** | Matching edge, resolution `unresolved`; the matching node reports `exists: true` | Contradiction | **indeterminate** | BDD Outline: every honesty signal the server can actually emit produces the right marker | The reverse direction, so the rule is symmetric rather than a one-sided guard |
| B9 | Graph loaded; two matching edges | Multiple | first in response order rendered; ambiguity reported alongside | BDD: A name matching more than one file | |
| B10 | Two edges, identical link text and target, fragments `A` and `B` | Boundary — the live bug | each embed resolves to its own view | BDD: Two embeds of one data file under different view names | Fails today |
| B11 | External destination with a URL scheme | Bypass | recognised before the graph; never enters the state model **and a placeholder IS rendered** | BDD Outline: only a valid identifier on an allow-listed host | The graph emits no edge for these, ever. The positive half is required — see test 11 |
| **B12** | Two edges in one fixture, both `unresolved`: one with `unresolved_reason: no_match`, one with `unresolved_reason: outside_root` | Containment | **two different markers**; the containment one contains no path segment | BDD: A containment refusal says something different from a missing file | **CORRECTED — revision 1's row described a payload the contract forbids.** `KnowledgeGraphEdge` has no `reason` property and is `additionalProperties: false`; the fixture could not have been built against the generated type. **Requires CW-2's third field.** The two-edge shape is deliberate: one edge alone cannot prove the markers differ |
| B13 | Matching edge; **markdown** target resolved; heading fragment written; `heading_found` false | Error | "no such heading" marker, listing the headings that exist | BDD: An embed naming a real note but a heading that does not exist | Requires CW-2 |
| **B13a** | Matching edge; **`.base`** target resolved with a view fragment; `heading_found` false | False-marker guard | **no "no such heading" marker** | BDD: An embed naming a real note but a heading that does not exist | `heading_found` is false for every `.base` target by construction — the graph only records headings for markdown files. A reader keying off it renders this marker across **all 75** dashboard embeds |
| **B13b** | Matching edge; markdown target resolved with a **block anchor**; `heading_found` false | False-marker guard | **no "no such heading" marker** | BDD: An embed naming a real note but a heading that does not exist | Same cause: the heading field is empty for a block reference, so the flag is never set |
| B14 | Fifteen embeds; graph request fails | Scale | exactly one page-level error, zero markers | BDD: A failed evidence request produces one page-level error | Counts asserted, not sampled |

#### Dataset C — Kind dispatch, all eleven

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| C1 | `.png` | Happy path | image renderer, inline | BDD Outline: each embeddable kind mounts its own renderer | |
| C2 | `.svg` | Happy path | image renderer, inline, drawn in an image element | BDD: An SVG embed is drawn as a picture | Never injected inline |
| C3 | `.pdf` | Happy path | PDF renderer, inline, pooled worker | BDD Outline: each embeddable kind mounts its own renderer | |
| C4 | `.base` | Happy path | saved-view renderer, inline | BDD: A note embedding a named view | |
| C5 | `.md` | Happy path | transclusion, not the editor shell | BDD: A note shown inside another note | |
| C6 | `.mp3` | Deferred | sound renderer, inline (step 6) | BDD Outline: sound and video play in place | Zero measured uses |
| C7 | `.mp4` | Deferred | video renderer, inline (step 6) | BDD Outline: sound and video play in place | Zero measured uses |
| C8 | `.mmd` | **Permanently out (ADR-083 §15, N8)** | link fallback with badge, exactly as `.html` | BDD Outline: kinds with no inline treatment; enforcement asserted by test 126 | **Ruled out, not deferred** — founder ruling 2026-09-11, *"diagrams / obsydian diagrams are out of scope and will not be a feature of omnipus KBs"*. This row covers the embedded diagram **FILE** (`![[chart.mmd]]`) ONLY, of which the founder's vault contains **zero**. A fenced ` ```mermaid ` block is a **different mechanism** (it reaches the renderer through the markdown `code` slot, not the embed pipeline), it **works today**, there are **163** of them in that vault, and nothing here touches it. Unlike C6/C7 above, this is not waiting on step 6 — there is no step 6 for it |
| C9 | `.html` | **Permanently out** | link fallback with badge; **never framed** | BDD Outline: kinds with no inline treatment | A reading surface is not an isolation boundary |
| C10 | `.txt` | **Out** | link fallback with badge | BDD Outline: kinds with no inline treatment | A wall of unstyled text with no reader affordance |
| C11 | `.zip` (kind "other") | **Out** | link fallback with badge; **no download card** | BDD Outline: kinds with no inline treatment | The download card needs fields the graph cannot supply |
| C12 | A file too large for inline text | Fallback | link fallback with badge, not a download card | BDD Outline: kinds with no inline treatment | Same for the binary fallback |
| **C13** | A file with no extension that the server sniffs as **text** | Edge — **a named divergence** | inline: kind "other" → link fallback. **Pane: kind "text".** Both asserted, in one test | BDD: The same file is classified the same way inline and in the pane | **CORRECTED.** Revision 1 listed only the inline answer, which is correct **only** for a reference whose editability was invented as `false` — the fabrication the narrowed type exists to prevent. The pane asks the server; the embed has only the name. The disagreement is the finding, not the inline answer |
| **C13a** | A file with no extension that the server sniffs as **an image** | Edge — **a named divergence** | inline: link fallback. **Pane: the image renderer.** Both asserted | BDD: The same file is classified the same way inline and in the pane | The one divergence that is a real loss: an extensionless picture shows as a link in a note and as a picture in the pane |
| **C13b** | A file with no extension that the server sniffs as **a video** | Edge — **a named divergence** | inline: link fallback. **Pane: the video renderer.** Both asserted | BDD: The same file is classified the same way inline and in the pane | The third and last divergence. Beyond these three the two classifiers agree, because the other six kinds are already decided by extension alone |
| C14 | `.PNG` (uppercase) | Case | image renderer | BDD Outline: each embeddable kind mounts its own renderer | Extension matching is case-insensitive |

#### Dataset D — View label to machine name

| # | Input fragment (views: label "Needs Daniel" → `tasks--needs-daniel`; label "Open" ×2) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| D1 | `Needs Daniel` | Exact label | that view's rows | BDD Outline: a view fragment matches a display label | |
| D2 | `needs daniel` | Case-insensitive | that view's rows | BDD Outline: a view fragment matches a display label | Second rung of the ladder |
| D3 | `tasks--needs-daniel` | Machine name | that view's rows | BDD Outline: a view fragment matches a display label | Third rung; an embed written against the address still works |
| D4 | `Needs  Daniel` (double space) | Near miss | missing-view marker listing real labels | BDD Outline: a view fragment matches a display label | Not silently normalised |
| D5 | `Open` | Duplicate label | refused, naming both views | BDD: Two views sharing a display label | Silently picking one is the original defect's behaviour |
| D6 | (no fragment) | Empty | first available view + caption | BDD: A data file embedded without naming a view | |
| D7 | A label on a view marked unservable | Unservable | the server's own reason, in place | BDD: A view the server cannot serve | Regardless of write-time policy |
| D8 | A label on a data file with zero imported views | Empty collection | marker stating the file has no views | BDD: An embed naming a real file but a view that does not exist | List is empty, and says so |
| D9 | A label 512 characters long | Very long | missing-view marker; no truncation of the comparison | BDD Outline: a view fragment matches a display label | |

#### Dataset E — Transclusion shapes (one level)

> **Six rows were deleted here in revision 2 (N1), and the reason is worth keeping visible.**
> E2–E7 described self-cycles, mutual cycles, a diamond and a six-deep chain. **None of those
> shapes can occur**, because a transcluded note's own embeds render as links, so nesting never
> reaches a second level. Fixtures built from them would have had to construct a render tree the
> product cannot construct — which is the deletable-subject failure this document's register exists
> to catch, one level up. Their numbers are retired rather than reused.

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| E1 | A → B (B has no embeds) | Happy path | B's content inside A | BDD: Outline — a section or a block shows only that part | |
| ~~E2~~–~~E7~~ | ~~self-cycle, 2-cycle, 3-cycle, diamond, six-deep, five-deep~~ | — | — | — | **DELETED (N1).** Unconstructible: nesting stops at one level |
| E8 | A → B where B embeds a picture **and a data view** | **The rule of US-7** | B's text, plus a link-with-reason for **each** inner embed | BDD: Embeds inside a transcluded note render as links with a reason | Not a could-not-check marker. Two inner embeds, not one, so "it handled the picture case" is not enough |
| E9 | A → B#Missing heading | Missing slice | "no such heading" marker inside the transclusion | BDD: An embed naming a real note but a heading that does not exist | Requires CW-2 |
| E10 | A → B where B is empty | Empty file | an empty region with a stated "this note is empty" | BDD: A note that shows an empty note says the note is empty | Never blank space with no explanation |
| **E11** | A → A | Self-embed | A's text **once**; the inner self-embed as a **link**; the render settles | BDD: A note that shows itself renders once and stops | **Asserted by counting rendered copies, never by a timeout.** A deadline-based "it did not hang" assertion is one of the two clock-based traps this repository has already been bitten by, and it would also pass with the one-level rule broken |

#### Dataset F — External video destinations

| # | Input destination | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| F1 | Allow-listed host, identifier `dQw4w9WgXcQ` (11 chars) | Happy path | local placeholder; frame only after press | BDD: An allow-listed video shows a locally drawn placeholder | |
| F2 | Allow-listed host, identifier of 10 characters | Min − 1 | link, not framed | BDD Outline: only a valid identifier on an allow-listed host | |
| F3 | Allow-listed host, identifier of 12 characters | Max + 1 | link, not framed | BDD Outline: only a valid identifier on an allow-listed host | |
| F4 | Allow-listed host, identifier containing `<script>` | Injection | link, not framed | BDD Outline: only a valid identifier on an allow-listed host | The identifier pattern is the whole filter |
| F5 | Allow-listed host with extra query parameters | Passthrough attempt | placeholder; parameters **not** forwarded on play | BDD Outline: only a valid identifier on an allow-listed host | The URL is constructed, never passed through |
| F6 | A different video host | Not allow-listed | link, not framed | BDD Outline: only a valid identifier on an allow-listed host | |
| F7 | A look-alike host with the allow-listed name as a prefix of a different domain | Spoofing | link, not framed | BDD Outline: only a valid identifier on an allow-listed host | Host comparison is exact, not prefix |
| F8 | A wikilink whose text happens to be a valid identifier | Wrong form | link, not framed | BDD Outline: only a valid identifier on an allow-listed host | The wikilink form addresses files only |
| F9 | Hand-written frame markup | Raw markup | inert | BDD: Hand-written frame markup in a note stays inert | The pipeline has no raw-markup plugin |
| F10 | Allow-listed host with a start offset of `12` | Optional parameter | placeholder; play starts at 12 seconds | BDD: Pressing play loads the player, once | Integer only |
| F11 | Allow-listed host with a start offset of `-1` | Negative | offset dropped; placeholder still shown | BDD: Pressing play loads the player, once | |
| F12 | Operator setting empty | Configuration | no external host in policy; embed renders as a link | BDD: An operator can decline the external host | |

#### Dataset G — Version tokens and conflicts

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| G1 | Current token, whole-file save | Happy path | 200; new token returned; activity record written | BDD: Person saves a note nobody else has touched | |
| **G1a** | `getLibraryContent` on a **binary** file (response carries no `content` field at all) | Boundary — nothing in the body to hash | an `ETag` header is present and is the token of the file's **raw bytes** | BDD: The reader that opens a binary file is given a version marker | **NEW (C3(r2), revision 3).** `library.ContentResult` omits `Content` by design for a binary file, and `handleLibraryContentGet` builds its response from that struct. A token derived from `ReadContent` here is the hash of an empty string — **identical for every binary file in the workspace** — on the PDF door, the one N2 refuses to exempt |
| **G1b** | `getLibraryContent` on a **`too_large`** text file | Boundary — same gap, second producer | an `ETag` header is present and is the token of the file's raw bytes | BDD: The reader that opens a binary file is given a version marker | `ContentResult` omits `Content` above `MaxContentBytes` too. One row per producer, so a fix that handles only the binary case fails here |
| **G1c** | The same file read through `getLibraryContent` and through `downloadLibraryFile` | **The row that catches two token definitions** | the two `ETag` values are **byte-identical**, and both equal `knowledge.ReadNoteVersion`'s `Token` for that note | BDD: The reader that opens a binary file is given a version marker | Two doors can each be "correct" and disagree. The disagreement surfaces only as a 409 nobody can clear, on the save path a person is already mid-edit on |
| **G1d** | A Library file **outside** every knowledge base, read through either door | Degraded scope, same value | the `ETag` is produced by the **same** computation as G1c's | BDD: The reader that opens a binary file is given a version marker | A-11's ruling applies to both populations. `ReadNoteVersion` needs a `*Collection`; the streaming half is unexported, so EMB-007a names the thin exported sibling as work rather than leaving an implementer to invent a second definition of "changed" |
| G2 | Stale token, whole-file save | Conflict | 409, typed body with path, sent token, current token | BDD: Person's save is refused because an agent wrote to the note first | |
| G3 | Empty string token | Empty | 400 | BDD: Save sent with no version token at all | Empty is refused deliberately on the agent path; the doors must agree |
| G4 | Field absent entirely | Null | 400 | BDD: Save sent with no version token at all | See Ambiguity A-1 |
| **G4a** | The **RFC-quoted** form of the current token placed in the request body's `expect_version` | Wrong shape, right value | **400**, and explicitly **not** 409 | BDD: Save sent with no version token at all | **NEW (M5, revision 3).** The header is quoted-strong (`ETag: "v1:…"`, which is also the only form Go's `ServeContent` parses); the body field is bare. A client that forgets to strip the quotes would otherwise get a **409 indistinguishable from a genuine conflict**, forever, on the PDF save path, with no way to make progress. **Paired:** the bare form on the same file succeeds |
| G5 | A token that was never issued | Malformed | 409, not 400 | BDD: Person's save is refused because an agent wrote to the note first | The token is opaque; the client must never parse or construct one |
| G6 | Current token, binary save | Happy path | 200; activity record written | BDD: A binary save goes through the same guard | |
| G7 | Stale token, binary save | Conflict | 409 | BDD: A binary save goes through the same guard | |
| **G7a** | **A whole-file save whose version comparison passes, with an agent's `EditNote` landing between the comparison and the write** | **Race — the door step 0 exists to close** | exactly one write on disk; the loser told | BDD: A version check and its write cannot be interleaved by an agent | **NEW.** G2 and G7 are sequential stale-token cases and **both pass against a lock-free implementation**. Revision 1 had a race row for the record-field door (G10) and none for the Library door, which is the door step 0 is about. Measured: `handleLibraryContentPut` takes no lock of any kind |
| **G7b** | The same save, checking **which lock key** the handler took | Race — the subtler half | the key matches the agent path's: same collection root, same lock directory, same collection-relative path | BDD: A version check and its write cannot be interleaved by an agent | Two writers taking two *different* locks pass G7a by luck on a fast machine. The lock key is `collectionRoot + "\0" + path`, so all three inputs must agree or the guard is decorative |
| **G7c** | A whole-file save of a Library file **outside any knowledge base** | Degraded mode | the write is serialised in-process; the cross-process advisory lock is not taken | BDD: A version check and its write cannot be interleaved by an agent | There is no collection root and no agent path to race. The empty-lock-directory mode is documented as **degraded, not disabled**, and must be described that way rather than as the full guarantee |
| **G7d** | A whole-file save of a file inside a knowledge base that is **itself nested inside another knowledge base** | Race — the ambiguous input | the lock key names the **innermost** enclosing collection, and matches the key the agent path takes for the same file | BDD: A version check and its write cannot be interleaved by an agent | **NEW (M7, revision 3).** G7b asserts the key matches "the agent path's" without saying which collection that is when two apply. Nothing in `rest_library.go` derives an enclosing collection today — `annotateKnowledgeBaseEntries` only answers "is this **directory entry** a knowledge base" — so this derivation is new work with a decision inside it, and two writers deciding differently is precisely the decorative-guard failure EMB-006 exists to prevent |
| G8 | Current token, record-field write | Happy path | 200; activity record with a person actor | BDD: A person's edit is attributed to a person | |
| G9 | Stale token, record-field write | Conflict | 409 with `knowledge_version_conflict` | BDD: A record changed by someone else refuses the edit | |
| G10 | Two writes in flight, same token | Race | exactly one succeeds; the other gets 409 | BDD: A record changed by someone else refuses the edit | The lock and the compare-and-swap, together |
| **G13** | Record-field write naming a **calculated** property | Refusal | refused by the write path, not honoured | BDD: A write naming a calculated field or a link is refused | The typed record contract refuses this; the raw frontmatter path revision 1 chose does not |
| **G14** | Record-field write naming a **relation** property | Refusal | refused, naming the path that does accept it | BDD: A write naming a calculated field or a link is refused | Same guard, second prohibition |
| **G15** | Record-field write naming an **ordinary** property on the same record | Control | 200 | BDD: A write naming a calculated field or a link is refused | Without this row, G13 and G14 pass on a handler that refuses everything |
| **G16** | Record-field write with authentication bypass active and no identifiable user | Attribution | **200**; activity record with actor `anonymous` | BDD: An edit that nobody can be identified for is saved | **N4 overrules revision 1's 503.** The write is allowed |
| **G17** | Record-field write with neither an authenticated user nor bypass active | Attribution | refused **before the file is touched** | BDD: An edit that nobody can be identified for is saved | N4 permits an *unattributed* write, never an *unattributable* one |
| **G18** | An activity file containing agent rows, identified-person rows and anonymous rows | Greppability | a plain text search for the anonymous token returns **exactly** the anonymous rows | BDD: An edit that nobody can be identified for is saved | The N4 obligation. An identified actor carries a prefix the anonymous one does not, so the two populations cannot be confused |
| G11 | Refused write, then no action for the longest retry window | No auto-retry | request count stays at one | BDD: A refused edit is never resent automatically | The property a future refactor will break |
| G12 | Refused write, then the person presses retry | Deliberate retry | exactly one further request, carrying the **fresh** token | BDD: A refused edit is never resent automatically | Proves retry exists at all |

#### Dataset H — Agent embed-authoring arguments

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| H1 | Valid note, data file, existing view label, existing section, current token | Happy path | one line written into one file | BDD: An agent asks for an embed | |
| H2 | View label that does not exist | Missing modifier | refused, listing the labels that exist | BDD: An agent naming a view that does not exist | |
| H3 | Target heading that does not exist in the target note | Missing modifier | refused, listing the headings that exist | BDD Outline: modifiers are checked against the target's kind | |
| H4 | Section of the destination note that does not exist | Creation | section created; embed written under it | BDD: An agent asks for an embed | Same behaviour as the existing linking operation |
| H5 | Width on a sound file | Kind mismatch | refused, naming the reason | BDD Outline: modifiers are checked against the target's kind | Never accepted-and-ignored |
| H6 | Target escaping the knowledge base | Containment | refused; nothing written | BDD: An agent naming a target outside the knowledge base | |
| H7 | Linking operation given a width | Foreign argument | refused **for that operation** | BDD: An argument this operation does not read | The regression the global argument sweep would create |
| H8 | Embed operation given a body | Foreign argument | refused | BDD: An argument this operation does not read | |
| H9 | Embed operation with no version token | Missing required | refused | BDD: An agent asks for an embed | |
| H10 | Embed operation with an empty version token | Empty | refused | BDD: An agent asks for an embed | Empty is refused deliberately |
| H11 | Both a target heading and a target block given | Mutually exclusive | refused, naming the conflict | BDD Outline: modifiers are checked against the target's kind | One slice, not two |
| H12 | A destination path outside the knowledge base | Containment | refused; nothing written | BDD: An agent naming a target outside the knowledge base | The destination is checked as well as the target |

#### Dataset I — Mount budget under load

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| I1 | 1 module | Min | evaluated immediately | BDD: Only modules near the viewport begin work | |
| I2 | 4 modules all in view | At the ceiling | all four in flight | BDD: Fast scrolling never exceeds four evaluations | The just-inside case |
| I3 | 5 modules all in view | Ceiling + 1 | four in flight, the fifth visibly waiting | BDD: Fast scrolling never exceeds four evaluations | |
| I4 | 40 modules, scrolled top to bottom in under 2 s | Stress | in-flight count never exceeds four at any sampled instant | BDD: Fast scrolling never exceeds four evaluations | Count, do not time |
| ~~I5~~ | ~~40 modules, 6 scrolled past, then printed~~ | — | — | — | **DELETED (N3).** The printout guarantee is withdrawn; the browser's print event cannot be held open while modules load |
| I6 | A queued module scrolled away before starting | Cancellation | slot released; next module starts | BDD: A module scrolled out of view before its turn | |
| I7 | A failed module, scrolled past 10 times | Retry storm | exactly one request | BDD: A failed module is not retried by scrolling past it | |
| I8 | 15 modules, window blurred and refocused within 10 s | Focus | zero further requests | BDD: Returning to the application does not re-evaluate | Both staleness windows |
| I9 | 3 PDF modules in view at once | Worker ceiling + 1 | two rendering, one visibly waiting, **and at most 2 workers ever constructed** | BDD: A third PDF beyond the worker ceiling waits visibly | The worker count is what proves a pool exists; the waiting state alone is satisfiable without one |
| I10 | 0 modules | Empty | no queue, no indicator, **no unmounted-limitation notice** | BDD: The reader states what unmounted modules cannot do | The notice must not appear on a note with no embeds |
| **I11** | 40 modules, 6 mounted, 34 unmounted | Notice — present | the notice names **both** find-in-page and printing | BDD: The reader states what unmounted modules cannot do | The N3 replacement for I5. Naming only find-in-page is now incomplete |
| **I12** | 40 modules, all 40 mounted | Notice — withdrawn | the notice is gone | BDD: The reader states what unmounted modules cannot do | I10, I11 and I12 are asserted **in one test**: a notice that never renders passes I10 and I12 alone |

### Regression Test Requirements

This feature **modifies existing functionality**. Three existing behaviours must survive it, and
one of them is the exact behaviour this work routes past.

| Existing behaviour | Existing test | New regression test needed | Notes |
|---|---|---|---|
| A non-image embed renders as a link carrying the "embed shown as a link" badge | `knowledgeMarkdown.test.tsx` (existing badge assertions) | **Yes** — extend to assert the badge still fires for `html`, `other`, `text` and for any kind whose step has not shipped yet | This is the behaviour being routed past. If the fallback silently stops firing, an unsupported embed becomes invisible instead of honest |
| An image embed resolves through the note reader's existing graph-backed lookup | `KnowledgeNoteView.test.tsx` | **Yes** — `resolveEmbedUrl` is being extended, not replaced; assert existing single-image notes still render | The extension changes the match key, which is the riskiest edit in step 1 |
| The Library preview pane renders every kind at full size, unchanged | `BasePreview.test.tsx`, `LibraryPdfPreview.test.tsx`, `LibraryMarkdownPreview.test.tsx` | **Yes** — the cross-variant tests double as the pane regression, since the default variant is the pane | Widening a prop and adding a variant must be a no-op for the pane |
| Whole-file saves succeed from the Library editor | `useLibraryFileEditor.test.tsx` | **Yes** — the happy path must still succeed once a token is required; existing tests will need the token added, which is itself the risk | A test updated to "send a token" hides the case where production forgets to |
| **An annotated PDF saves and re-opens showing the entered values** | **`src/components/library/preview/LibraryPdfPreview.test.tsx` — the save round-trip test, which exists today and passes with no token** | **Yes — and this is a break, not a risk.** EMB-001 makes the token mandatory; the PDF save path has no read that returns one. The test must be updated **in the same change** as the loader's header read, not discovered by CI | **Revision 1 omitted this row entirely** and its ambiguity A-1 named the wrong caller as "the only" one. The annotated-PDF save is a shipped feature; making the token mandatory without EMB-007 breaks it with an HTTP 400 |
| The five existing note-editing operations accept exactly their own arguments | `pkg/knowledge/authoring_tools_test.go` | **Yes** — a per-operation sweep changes validation for all five; assert each still accepts every argument it genuinely reads | The per-op sweep is invisible in review |
| The served security policy's directive floor | `pkg/gateway/embed_csp_test.go` | **Yes** — the floor test must be extended, not relaxed; its self-mutation half must still prove the checker can fail | Relaxing a floor to accommodate a new value is how a floor stops being one |
| **The served policy matches the line documented in `adr-067-knowledge-base-and-preview-spec.md` §10.7** | **`TestSpaServedWithCSP`, which reads that document at runtime as its oracle** | **Yes** — the document is edited in the same change, and the test's "exactly one literal policy line" requirement must still hold afterwards | **Revision 1 did not know this test's oracle was a Markdown file.** Edit the policy without editing §10.7 and CI fails on a byte-for-byte assertion whose source is not in any impact list |
| **The PDF worker path's derived policy** | `pkg/gateway/embed_csp_test.go` | **Yes** — assert the served header on the worker path too | A second policy string is derived from the one being edited. Nothing today asserts what happens to it |
| **Range requests and conditional GETs on the shared byte-stream helper (audio/video seeking, the preview-token path)** | the existing `serveLibraryContent` / `serveLibraryPath` coverage | **Yes — a scope assertion, not just a behaviour one.** Assert that a `Range` request and an `If-Range` request on a path served through `serveLibraryPath` behave **byte-for-byte as before**, and that no `ETag` header appears on that path | **NEW (M5, revision 3).** `handleLibraryDownload` reaches bytes through `serveLibraryContent`, which is `applyLibraryByteHeaders` then `http.ServeContent` — and `ServeContent` implements conditional GET and `If-Range`. An `ETag` set in the **shared** helper changes `If-None-Match` (304s begin) and `If-Range` (validates against the ETag, not `Last-Modified`) for **every** caller, including `serveLibraryPath`. EMB-007b puts the header in `handleLibraryDownload` **only**; this row is what proves it stayed there |

#### Regression Dataset: the honest link fallback

| # | Input | Previous Behaviour | Must Still Produce | Traces to |
|---|---|---|---|---|
| R1 | `![[page.html]]` | link + "embed shown as a link" badge | identical | Regression: link fallback |
| R2 | `![[archive.zip]]` | link + badge | identical | Regression: link fallback |
| R3 | `![[notes.txt]]` | link + badge | identical | Regression: link fallback |
| R4 | `![[song.mp3]]` before step 6 ships | link + badge | identical | Regression: link fallback |
| R5 | `![[photo.png]]` | image drawn | image drawn, now through the extended resolver | Regression: existing image embeds |
| R6 | `[[Note]]` (plain link, not an embed) | ordinary wikilink treatment | identical; no embed machinery involved | Regression: link fallback |
| R7 | A note with no embeds at all | rendered normally, no extra requests | identical, and no find-in-page notice | Regression: existing note rendering |

---

## False-Green Risk Register — one entry per phase

> **Read [`false-green-patterns.md`](../false-green-patterns.md) before trusting any green result
> from this work.** In this repository a guard test has passed 673/673 with the feature it guarded
> deleted; 27% of the browser test suite never ran while CI reported green; a security control
> reported "saved" and changed nothing; and a linter silently capped its findings at three per
> message. The default suspicion when reading any test below must be: **"does this test inject
> what production lacks?"**
>
> Two rules apply to every phase and are not repeated in each row: **capture exit codes without a
> pipe** (`cmd > log 2>&1; echo "exit=$?"`), and **reproduce any reported failure yourself before
> acting on it** — three of five "must fix" items in one prior session did not exist.

### Cross-cutting risks, before the per-phase table

| # | Risk | Why it produces a false green here | The check |
|---|---|---|---|
| X1 | **A new test file that never runs.** The browser test suite is filtered by a coverage guard; a new directory outside it is silently skipped while the job reports green. | 116 of 422 files (27%) were skipped this way once. | `scripts/check-vitest-coverage.mjs` must pass with the new files present. Confirm the new file appears in the run's own `--- PASS`-equivalent output by name, not just that the job was green. |
| X2 | **A type check that checks nothing.** The TypeScript root is a project-references root with no `include` of its own; `tsc --noEmit` without `-b` is a silent no-op. | Every file under `tests/e2e/` was invisible to the gate for the life of the project. | Use `npm run typecheck`. Prove coverage of the new files the same way it was proven before: inject `const x: number = "no"` into one new file, run the gate, confirm a non-zero exit, then remove it — **with a control injection in a known-covered file**. |
| X3 | **A Go test that matched nothing.** `go test -run 'Pattern'` prints `ok` when the pattern matches zero tests. | Observed. | Run with `-v` and count `--- PASS` lines **by name**. |
| X4 | **A build without the tags.** Without `-tags goolm,stdjson` the gateway will not compile and the error reads like a broken package. | Observed repeatedly. | Always `make test` / `make build`, or pass the tags explicitly. Build tags are also a cache namespace — a nested untagged build shares zero cache and recompiles ~858 dependencies. |
| X5 | **A verdict from a moving tree.** | Agents running against a checkout while a verdict is in flight. | Bind every reported verdict to a commit hash. Do not run the full Go suite locally; push and read CI. |
| X6 | **A "measured" claim that was read rather than run.** | The ADR this spec implements had **three** measured claims that were false, all read from code and never executed. Its own review then found **four more**, in a revision written specifically to catch that pattern. Three of the four were *inherited* — from a code comment, from a prior revision, from an earlier sentence in the same section — and none was re-measured because each was already written down. | Every claim in a completion report must name the command that produced it. **A claim being present in a document is not evidence for it.** |
| **X7** | **A test that cannot fail because its subject already exists.** | **This category was missing from revision 1's register, and its absence caused a defect inside the document that defines the register.** Revision 1 specified EMB-091 and test 73 — extend the audit system to recognise the `knowledge.*` event names, and test that it does. **All five names were already registered, and a test asserting exactly that, with the exact negative control this spec prescribes, already existed in the tree.** The test would have been green on day one against zero implementation, and a completion report citing it would have been truthful and worthless. The source was a **stale comment** in the very file the requirement was derived from. | **Run every new test before writing any implementation. If it passes, it is not this work's test.** Then find out why: either the subject already exists (delete the requirement, and correct whatever said otherwise), or the test cannot distinguish the states it claims to. Applied to revision 1 as written, this check alone would have caught EMB-091, test 73 and one whole step-5 precondition. |
| **X8** | **A feature whose input the product cannot construct.** | X7, one level up: not a test whose subject is already built, but a test whose subject can never be *reached*. Revision 1 specified a cycle detector, a depth cap, four tests and a success criterion for transclusion — while the same story required a transcluded note's own embeds to render as links, which makes a second level of nesting impossible and therefore makes a cycle unconstructible. The four tests could only ever have passed against fixtures assembled by hand into a shape production cannot produce. | Before writing a test, **describe the sequence of ordinary product actions that reaches the state under test.** If that sequence does not exist, the feature is unreachable and the question is whether to build the path or delete the feature — not how to build the fixture. |

### The register's two standing invariants

Both were stated once in revision 1 and applied once. They are rules, not observations.

> **Invariant 1 — every negative assertion is paired, in the same test body, with a positive one.**
> A test asserting that something is *absent* passes on a component that renders **nothing at all**,
> and therefore passes with the whole feature reverted. Revision 1 made this exact observation
> about one test and left seven others with the identical shape unpaired. Each names its pairing in
> the order table and in its phase's row below.
>
> **One list, and only one (M8, revision 3).** Revision 2 carried **three different versions of the
> same set** — Invariant 1's prose named seven (11, 17, 21, 22, 44, 62, 76), the step-1 pairing table
> six, and SC-027 ten — reconcilable but not identical, and SC-027 is the one that gets reported.
> **`SC-027` is now the single authoritative list**; this invariant and the per-phase tables
> reference it and do not restate it.
>
> **A pairing must also be non-vacuous, which is a second property and revision 2 checked neither.**
> Three of revision 2's fourteen named pairings failed when the question *"would this test fail if
> its subject were deleted?"* was actually asked: **21** (both halves already held on the current
> tree), **22** (kind unspecified; with an image both halves already held), and **110** (a pure
> unpaired negative with no pairing named at all, and not on any pairing list). All three are
> repaired in revision 3 — see the order table rows for 21, 22, 110 and the new 122.

> **Invariant 2 — every numbered test appears in exactly one row of the register's coverage table.**
> Revision 1's register claimed "one entry per phase" while tests 91–96 sat outside it entirely,
> having been appended after the register was written. **Revision 2 promoted that observation to an
> invariant and a measured success criterion — and then broke it at four times the scale**, with
> ≥36 numbered tests in no row at all, including tests 106, 107 and 108, the three tests revision 2
> added in the same edit to close revision 1's own findings. Diagnosing a mechanism, naming it, and
> making it a rule did not stop it recurring, because **nothing checked it**.
>
> **What changed in revision 3, and why it is a different kind of claim.** Membership no longer
> depends on prose. Every numbered test has a row in the single **Register coverage table** at the
> end of this register, keyed by test number. Prose risk rows in each phase above stay as the
> analysis; the coverage table is the authoritative membership list. That makes the invariant
> checkable by diffing two number columns instead of by scanning prose for names, numbers and
> ranges — three different citation styles, one of which (a range like *"tests 9–38"*) covers a test
> without ever giving it a row, which is exactly how 36 tests hid inside a register that claimed to
> cover them.
>
> **How to re-measure it** (this is the command SC-026 is measured by, and it is the point of the
> rewrite — a count that is derived rather than measured is the same class of defect as a test that
> cannot fail):
>
> ```bash
> S=docs/internal/specs/adr-083-embedded-content-spec.md
> # every live numbered row in the two order tables
> awk '/^### Test Implementation Order$/,/^### Test Datasets$/' "$S" > /tmp/o1
> awk '/^## Test Implementation Order — tests 91/,/^## Traceability Matrix$/' "$S" > /tmp/o2
> cat /tmp/o1 /tmp/o2 | grep -oE '^\| \*{0,2}[0-9]+\*{0,2} \|' | grep -oE '[0-9]+' | sort -n -u > /tmp/tests
> # every number in the register coverage table
> awk '/^#### Register coverage table/,/^## Functional Requirements$/' "$S" \
>   | grep -oE '^\| \*{0,2}[0-9]+\*{0,2} \|' | grep -oE '[0-9]+' | sort -n -u > /tmp/covered
> diff /tmp/tests /tmp/covered && echo "SC-026 OK: 0 tests outside the register"
> ```
>
> A non-empty `diff` is the failure. Both sides must also be free of duplicates, which `sort -u`
> would mask — check with `sort -n /tmp/…| uniq -d` on the raw extraction.

### The X7 sweep, actually run — revision 3

> **Revision 2 defined X7 and did not apply it to revision 2.** That is the same failure the
> category exists to name, one level up: a check stated and not executed. This section records the
> sweep being run, by hand, against **every** numbered row on `def10b90e`, so the next reader can
> re-run it rather than trust it.
>
> **Method, stated so it can be repeated and so its limits are visible.** Each row's stated
> assertions were compared against the code as it exists on `def10b90e`, by reading the relevant
> file. **No test was executed — none of these tests exists yet, which is the whole premise of an
> X7 sweep.** Every entry below is therefore `[VERIFIED BY READING]`, not `[VERIFIED BY RUNNING]`,
> and the sweep's own output is a **prediction that the first real run must confirm**. A row here
> predicting FAIL that passes on the first run is a finding; so is a row predicting PASS that fails.

| Test | Prediction | The measurement behind it |
|---|---|---|
| **20** | **PASSES today** | `remarkKbWikilinks` converts an embed to an image node **only** for an extension in `IMAGE_EXTENSIONS`; every other embed already renders a wikilink node with the badge. Badge present, no renderer, no download card — all three clauses hold now |
| **21** (revision-2 form) | **PASSES today, in both halves** | `LibraryDownloadCard` is not reachable from the note reader, and a `.zip` already renders the badge. The named pairing could not rescue it because the pairing's subject also already exists. **Rewritten** |
| **22** | **Conditional** | The negative half holds today (remark yields `code`/`inlineCode` nodes the text visitor never sees). Whether the pairing rescues it depended on a kind revision 2 never named — with an image, both halves already pass. **Kind now fixed at `.pdf`** |
| **28, 29, 30** | **PASS today** | `LibraryPdfPreview` constructs `new Worker(…)` **inside its own load effect** — one worker per component instance. Two documents rendering concurrently, failure isolation, and "a third mount gets a fresh worker" are all unconditionally true without a pool. **Found by this round's sweep; not in the round-2 review.** Only test **31** (constructed-worker count ≤ 2) can detect a pool |
| **34, 35, 37** | **PASS today, for the wrong reason** | `op: "embed"` does not exist; `EditTool.Execute`'s `default:` branch calls `refuseOp`. The request is refused — by an unknown-operation branch. A test that cannot tell *"unknown operation"* from *"target outside the collection"* is not testing containment. **All three repaired with a message assertion and a succeeding pair** |
| **36** | **FAILS, but two of its three clauses pass for the wrong reason** | `width` is not in `editArgNames`, so the **global** `unknownArgs` sweep already refuses `link` + width; `embed` + body hits `refuseOp`. Only "still accepted by its own operation" can fail today. **Sharpened to assert the refusal names the operation** |
| **38** | **PASSES today** | Exactly eight `knowledge_*` keys in `pkg/config/defaults.go`; no seed change; reconciliation adds nothing. All three true now, and two existing tests already assert the first two entry-for-entry. **Kept as a third net, relabelled a pin** |
| **61** | **PASSES today** | `frame-ancestors` and `img-src` are in `spaContentSecurityPolicy` unchanged. A test asserting a value equals its current value passes before the change |
| **67** | **PASSES today, and should** | A negative control that frames the host directly. **Its construction is now specified** so it cannot accidentally be routed through the feature it controls for |
| **90** | **PASSES today, and should** | A regression pin over existing badge behaviour. Revision 2 left it outside every X7 list *and* outside SC-028 |
| **105, 119-clean** | **PASS today, and should** | Already labelled pins in revision 2 |
| **107** | **PASSES today** | **`'svg'` is already in `IMAGE_EXTENSIONS`.** An SVG embed already becomes an image node, so "renders inside `<img>`", "the side effect did not occur" and "no `<svg>` in the DOM" all hold. The property is real; the test proves nothing about **this** work |
| **117** | **Cannot fail in ANY implementation** | The test **wrote its own fixture** and asserted a property of the strings it had just produced. No production code was exercised. This is a distinct sub-species of X7 — not "the subject already exists" but "there is no subject" — and it is the only one of the eleven that no amount of implementation would have fixed. **Rewritten to drive real writes and read the file the product produced** |
| **3, 8** | **FAIL, but each has clauses that pass today** | Test 3's "succeeds" half and test 8's first three clauses. Both are noted in their rows and in step 0's X7 check so a partial pass is not read as a partial landing |
| every other numbered row | **FAILS today** | Predominantly because the module, endpoint, field or script under test does not exist at all |

### Per-phase register

---

#### Step 0 — Closing the unguarded save door

**What a false green looks like here.** The obvious test posts a save with the *correct* version
token and asserts 200. That test passes with the version check **entirely absent** — a handler that
ignores the field returns 200 too. Likewise, "an audit record was written" is often tested by
constructing the handler with a recorder passed in; if production wires no sink, or wires a
different one, the test still passes. This is the single most common shape of the trap in this
repository: *the test injects what production lacks.*

| Risk | The test that catches it |
|---|---|
| A version field that is accepted and ignored | `TestLibraryContentPut_StaleVersionReturns409WithCurrentToken` — the **stale** case is the load-bearing one. The fresh-token test alone proves nothing. Mutation-check: delete the comparison and confirm this test dies. |
| A version field that is optional in practice | `TestLibraryContentPut_MissingVersionReturns400` — without it, every existing caller keeps the old unguarded behaviour and the door is still open. |
| An audit record that only exists in tests | `TestLibraryContentPut_WritesAuditRecordOnDefaultInstall` — boots the **real** default wiring (precedent: `pkg/knowledge/authoring_audit_default_install_test.go`, `pkg/gateway/knowledge_realboot_wiring_test.go`) and **reads the record back from the sink**. Never assert on an injected recorder. |
| An audit assertion done by grepping the handler source for `logLibraryAudit` | **Forbidden.** That is exactly the substring-scan pattern that passed 673/673 with the gate deleted. Assert the record exists, not that a name appears in a file. |
| A "no auto-retry" test that passes because the request never happened | `useLibraryFileEditor.conflict.test.tsx::surfaces-conflict-without-resending` (test 8) must assert **one** request before, **still one** after the window, and **exactly one more** after pressing retry. The third clause is what proves retry exists at all. |
| **A compare-and-swap with no lock, passing every sequential test** | **The gap revision 1 left open.** Tests 2 and 4 send a stale token and expect 409; **both pass against an implementation that hashes, compares, and then writes with nothing held in between** — which is what `handleLibraryContentPut` does today, since it takes no lock at all. Test **99** releases an agent's `EditNote` **between** the comparison and the write and asserts exactly one write survives. Test **100** asserts the lock **key** matches the agent path's, because two writers on two different locks pass test 99 by luck on a fast machine. The package's own version-control header states the failure verbatim: *"two Omnipus writers can both read the same token, both pass the comparison, and both write."* |
| **A read side with nothing to hand back** | Tests **97** and **98** assert the version header on the JSON read **and** on the byte-stream download. Today `handleLibraryContentGet` sets **no headers at all**, so both fail before the change — which is what makes them worth writing. Without them, "the editor sends back the token it received" (test 7) can be satisfied by an editor sending back a token it invented. |
| **A mandatory token that quietly exempts the one caller that cannot supply one** | Test **101** drives the **PDF** path end to end: the loader reads the header off the download response, the save sends it, a stale one conflicts, and a second save in the same session uses the token from the first save's response. Under **N2** there is no exemption, and the existing `LibraryPdfPreview.test.tsx` save round-trip **breaks** under this change — it is in the Regression table for that reason, not as a precaution. |

> **Deletable-subject warning.** `TestLibraryContentPut_FreshVersionSucceedsAndReturnsNewToken`
> (test 3) — **the name from the order table, verbatim; revision 2 carried a shorter one here and
> one of the two would have become the file that got written (m5)** — would still
> pass with the whole feature reverted, in its *"succeeds"* half. It is included only as a companion to the stale-token test
> and must never be cited alone as evidence that step 0 landed.
>
> **X7 check for this phase (rewritten in revision 3 — enumerated, never a range).** Run **1, 2, 3,
> 4, 5, 6, 7, 8, 97, 98, 99, 100, 101, 121, 123, 124** before any implementation.
> **Must FAIL: 1, 2, 3, 4, 5, 6, 7, 8, 97, 98, 99, 100, 101, 121, 123, 124** — all sixteen.
> **Expected to PASS: none.**
> Test 3 is the one to watch: its *"succeeds"* half passes today, and only its **response-header**
> half fails, so it must never be cited alone as evidence that step 0 landed. Test 8's first three
> clauses also pass today on the existing generic error path — the two clauses added in revision 3
> (a distinguishable **conflict** state, and a retry carrying the **fresh** token) are the halves
> that fail.

---

#### Step 1 — Resolver, renderers, PDF pool, agent authoring

**What a false green looks like here.** This is the phase the ADR itself warns about, in its best
line: *there is no broken-image bug to point at.* A PDF that fails to route renders as a tidy link
with a small grey badge — a deliberate, tested, honest treatment. So:

- A test that renders the note and asserts "no crash" passes with the feature reverted.
- A test that asserts the document contains the text `report.pdf` passes with the **link fallback**,
  because the fallback prints the filename.
- A visual check by a person cannot distinguish a correct fallback from a failure to route.

| Risk | The test that catches it |
|---|---|
| Nothing routed; the fallback caught everything | `knowledgeMarkdown.embeds.test.tsx::each-kind-mounts-its-renderer` asserts a **stable per-renderer identifier is present** AND the "embed shown as a link" badge is **absent** for that embed. Both halves are required. |
| The renderer mounted but the wrong one | The assertion is per-kind and names the specific renderer, not "some renderer". |
| The layout variant prop is never read | `*.variant.test.tsx` asserts state identifiers and text are **equal** across variants — which is trivially true if the component ignores `variant` entirely. **Fix in the test design: add a positive control asserting the outermost container's class list *differs* between variants.** Without that control the whole cross-variant suite passes on a component that never implements the variant. |
| The PDF pool test passes with one worker per document (i.e. no pool) | `LibraryPdfPreview.pool.test.tsx::failure-attributed-to-one-document-only` — with a per-document worker this passes trivially, so it must be paired with `::third-lease-queues-visibly`, which can only pass if a ceiling exists, and with an assertion on the **count of constructed workers** (≤ 2). |
| The per-operation argument sweep silently accepts everything | `TestKnowledgeEdit_PerOpArgumentSetRejectsForeignArgs` must assert **refusal**, and must include the mirror case (each argument still accepted by the operation that reads it). A sweep that refuses everything would pass the refusal half alone. |
| The tool-policy pin passes while a ninth tool was added | `TestKnowledgeToolPolicy_CatalogueUnchangedByEmbedOp` (test 38) asserts **set equality**, never membership. A membership assertion is the deletable-subject trap in its purest form. **Note it is a second net:** two existing tests already assert the ceiling and the seeds match the static catalogue entry for entry, so a regression here would fail those first. |
| **A skip cross-check built against a shape the server does not emit** | Revision 1's datasets B5 and B6 described "zero matching edges" for a skipped target. Measured, a walk-level skip produces a **matching unresolved edge** and a scan-level skip produces a **normal resolution** — neither produces zero edges. A fixture built from those rows would have exercised nothing. Test **102** uses the real shape, and **pairs** it: the identical fixture with the skip entry removed **must** produce the missing-file sentence, proving the cross-check is what made the difference. Test **103** adds the near-miss that must **not** suppress it. |
| **A resolver that reads the edge and ignores the node** | Test **104**. Today's code already refuses to hand out a URL when the target node reports it does not exist; an implementer following revision 1's five-state model would have extended the edge lookup and dropped that guard silently. Both disagreement directions are asserted, **plus** an agreeing pair that must resolve normally. |
| **A classification divergence that no cross-variant test can see** | Test **109**. The cross-variant tests (25–27) hold props fixed, so a divergence occurring **upstream of the renderer** — in what kind the file was decided to be — is invisible to all three. `npm run typecheck` cannot see it either: a fabricated empty media type and `false` editability type-check perfectly. |

> **Negative assertions in this phase, and their required pairings (Invariant 1).**
>
> | Test | The negative assertion | Passes unpaired when… | Its pairing, in the same test body |
> |---|---|---|---|
> | 14 `indeterminate-never-says-no-such-file` | the missing-file sentence is absent | the component renders nothing at all | the could-not-be-checked marker **is** present, with its reason |
> | 11 `external-url-recognised-before-graph` | the graph is never consulted, no miss reported | the external form is not recognised at all | the placeholder **is** rendered for that destination |
> | 17 `containment-marker-differs-from-missing-file-marker` | the refusal text contains no path segment | the marker **is** the ordinary missing-file marker — which is exactly what happens today | a second unresolved edge in the same fixture produces the missing-file marker, and the two are asserted **different** |
> | 21 `no-download-card-reachable` | **REWRITTEN (M8).** Was: no download card on any embed path | **the pairing was vacuous too** — `LibraryDownloadCard` is unreachable from the reader today **and** a `.zip` already renders the badge, so both halves passed before any implementation | now: **the new renderer dispatch never routes to the download card**, asserted against a `.pdf` embed that **does** mount its renderer after the change — the `.pdf` half fails today |
> | 22 `embed-in-code-fence-mounts-nothing` | no renderer, no marker | the whole embed feature is reverted | the identical notation **outside** the fence, in the same document, mounts its renderer — **the kind is `.pdf` (M8)**, because with an image the pairing is vacuous: an image embed outside a fence already mounts today |
> | 44 `machine-name-never-constructed` | no derived machine name for an unmatched label | the reader emits nothing | a **matched** label produces the server's machine name verbatim |
>
> **X7 check for this phase (rewritten in revision 3 — the sweep was defined in revision 2 and
> never run, and this is what running it found).** Run **9–38, 91, 92, 102–109**. Test **93** moved
> to step 1c, where the continuation table already assigned it — revision 2 listed it in **both**
> step 1's and step 1c's checks, breaking "exactly one" a second way.
>
> **Must FAIL (31):** 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 22, 23, 24, 25, 26, 27, 31, 32,
> 33, 34, 35, 36, 37, 91, 92, 102, 103, 104, 106, 108, 109. *(21 also fails, in its rewritten form.)*
>
> **Expected to PASS — pins, and none of them is evidence that step 1 landed (8):**
> **20** every non-image embed already renders the badge; **21** in its revision-2 form both halves
> already held (the rewritten form fails, and that is the point); **28, 29, 30** all satisfied by
> one worker per document, i.e. by the *absence* of the pool; **38** the ceiling, the seeds and the
> reconciliation pass are all already as asserted; **105** pins today's measured scan-level
> behaviour; **107** `'svg'` is already in `IMAGE_EXTENSIONS`, so an SVG embed already renders
> inside `<img>` with no `<svg>` in the DOM.
>
> **Three of these are especially costly and are called out by name: 20, 21 and 107.** They are the
> tests that *appear* to prove "the link fallback still works" and "SVG is safe". They prove
> neither about this work, and a phase-exit report citing them would be truthful and worthless.

---

#### Step 1c — Two facts reach the wire

**What a false green looks like here.** `make verify-contracts` compares the committed generated
artefacts against the specification. It passes when the specification declares `heading_found` and
the generated types carry it — **and says nothing about whether the handler ever sets it.** A
handler that leaves the field at its zero value passes contract verification completely.

| Risk | The test that catches it |
|---|---|
| The field exists on the wire and is never populated | `TestKnowledgeEdge_HeadingFoundTrueAndFalse` (test 39) — the **pair**. A handler hardcoding `false` passes the not-found half and fails the found half. Neither half alone is sufficient. |
| **A "no such heading" marker that fires on every dashboard** | Test **110**. The heading-found flag is set at exactly one place in the graph builder, guarded on the target having a heading fragment, and the builder records headings **only for markdown files**. So it is **false by construction** for a `.base` target — all 75 of the founder's dashboard embeds — and for every block reference. Test 39 exercises markdown headings only and **passes while both false-marker paths ship**. Test 110 adds the two rows that catch it, each asserting the marker is **absent**. |
| **A containment refusal indistinguishable from a missing file** | Test **111**. Two links in one fixture, both unresolved — one for absence, one for a path outside the root — asserting **different** reason values. A handler that hardcodes either passes one half. This is CW-2's third field, and without it the reader has one value for two facts and prints the wrong sentence for a security-relevant case. |
| The block anchor is populated into the heading field | `TestKnowledgeEdge_BlockAnchorProjectedSeparately` (test 40) asserts the heading field is **empty** when an anchor is present. **Note:** the block field is parsed today and read by **nothing** — no resolution logic, no test, no wire type. This is its first assertion ever, so there is no existing behaviour to regress and equally no existing test to lean on. |
| The generated runtime validator is weaker than the contract | `wireContracts.test.ts::graph-edge-validator-accepts-and-rejects` (test 41) — probe **with a control**. Nested inline objects do not get strict checking in the generated validator, and two prior attempts to verify this exact property produced false results: one used a payload rejected for unrelated reasons, one used assertions that can never fail. |
| The agent surface reporting a broken embed with no reason | Test **93**, folded into this phase because it depends on CW-2's reason field. A broken embed's rendered line must carry its reason — including *outside the knowledge base* — asserted against the **real renderer output**, never against the sample transcribed into the ADR. |

> **Deletable-subject warning.** A test asserting only that the specification file contains the
> string for a new field is a source-text assertion and proves nothing about behaviour. It is not
> in this plan and must not be added. The contract check proves the generated artefacts match the
> specification and says **nothing** about whether a handler ever sets a value.
>
> **X7 check for this phase (revision 3):** run **39, 40, 41, 93, 110, 111, 120, 122**.
> **Must FAIL: all eight.** Test 93 appears here and **only** here (revision 2 also listed it under
> step 1). Test 110 is now the **Go** half only — its former SPA assertion is test 122, and 122's
> negative ("no such heading" marker absent) is expected to fail **only because of its pairing**:
> the same fixture's genuinely-missing markdown heading must render the marker, which today's
> reader does not do. Unpaired, 122 would pass on the first run.

---

#### Step 2 / 2m — Dashboards

**What a false green looks like here.** Two fixtures pass whether or not the feature works:

1. **A knowledge base mounted at the workspace root.** The path conversion is the identity function
   there, so a root-mounted fixture passes with the conversion deleted. The review says this
   explicitly and it is the highest-value test-design instruction in this phase.
2. **A data file with only one view.** Every matching strategy — correct label matching, "just take
   the first one", or ignoring the fragment entirely — produces the same answer.

| Risk | The test that catches it |
|---|---|
| Path conversion never happens | `embedResolver.test.ts::converts-collection-path-below-root` uses a knowledge base **two levels below** the root, **and** seeds a decoy file of the same name nearer the root, asserting the decoy is never requested. |
| "First view" masquerading as label matching | `BasePreview.inline.test.tsx::two-views-one-file-do-not-cross` uses two views with **different rows** and asserts each embed against its own rows — never against the view's machine name, which would encode the importer's collision counter into the test. |
| The reader reconstructing a machine name that happens to be right | `baseViewMatch.test.ts::machine-name-never-constructed` asserts no derived name is emitted for an unmatched label. A reconstructed name that matches by luck is the original slug-collision defect returning. |
| The migration report reporting success by checking only that the file exists | `TestEmbedMigrationReport_NamesEveryFailure` runs against a fixture seeded with **one deliberate miss** and asserts the failure count is **exactly one**. A report that checks file existence only would return zero. |
| The view-list deduplication test passing with one embed | `::view-list-deduped-by-file-path` (test 50) uses fifteen embeds over five files and asserts **exactly five** requests. |
| **A resolver-swap test whose fixture cannot show the difference** | Test **94**, folded into this phase (revision 1 left it outside the register entirely). Its whole point is that a link **absent from the loaded rows** but present in the reader's graph renders as resolved. **If the fixture's link is present in the rows, both resolvers agree and the test proves nothing** — it passes identically whether the reader's resolver was threaded through or not. The fixture is the test. |
| **A test whose subject is a sentence** | Test **95**, also folded in. It asserts a UI string exists: trivially satisfiable, and trivially satisfiable **wrongly**. Its pairing is required — the same test mounts the same view in the full-screen pane and asserts the statement is **absent** there, so a component that renders the sentence unconditionally fails. |
| **An outline fetch that is never implemented** | Test **91**, folded in. Revision 1 stated it as "the outline request count is 0 on every path except the missing-heading case". **The zero half passes with the outline fetch never written; the `1` is the load-bearing half** and was mentioned only in passing. It is now stated first. |

> **Deletable-subject warning.** `::named-view-renders-its-own-rows` (test 45) alone would pass on a
> single-view fixture with all matching logic removed. It is only meaningful alongside
> `::two-views-one-file-do-not-cross` (test 46).
>
> **X7 check for this phase (revision 3):** run **42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 94, 95**.
> **Must FAIL: all twelve.** Test 91 is **not** in this list — the continuation table assigns it to
> step 1, and revision 2 named it here as well. Test 45 is the deletable-subject case above: it
> fails today only because the inline variant does not exist, and once it does it would pass on a
> single-view fixture with all matching logic removed, so it is meaningful only alongside 46.

---

#### Step 3 — Transclusion, one level

**What a false green looked like here in revision 1, and why the answer was to delete the
feature.** Revision 1 opened this section with a genuinely sharp observation: a loop detector and a
depth cap are two mechanisms, **the cap will stop a loop too**, so a test that embeds A in B and B
in A and asserts only "the page rendered and did not hang" passes with the loop detector completely
deleted. That analysis was correct and it was the best paragraph in the document.

**It was also one level short.** The same story required a transcluded note's own embeds to render
as **links** — so a second level of transclusion never mounts, so A→B→A never reaches the second A,
so **there is no loop for either mechanism to catch.** The four cycle tests could only ever have
passed against a render tree assembled by hand in a shape production cannot produce. Not a test
whose subject can be deleted — a test whose subject can never be *reached* (**X8**).

Founder ruling **N1** resolves it by removing the feature. No cycle set, no depth cap, no markers
for either, and their four tests and success criterion are deleted rather than deferred.

| Risk | The test that catches it |
|---|---|
| **Nested embeds rendering as content after all** | `::nested-embeds-fall-back-with-a-reason` (test 57) is now the load-bearing test of this story. It asserts the link fallback **with a reason**, asserts the could-not-be-checked marker is **absent**, and asserts **no second level of content anywhere on the page**. Without the third clause, a build that quietly nests passes. The fixture uses **two** inner embeds of different kinds, so "it handled the picture case" is not enough. |
| Nested embeds landing in the could-not-be-checked state | Same test, second clause. Without it, "technically honest and practically useless" passes: every inner embed resolved against the outer note's graph finds no edge and reports that it could not be checked. |
| **A self-embed proved to terminate by a clock** | Test **112** asserts by **counting rendered copies** — A's text appears exactly once, the inner self-embed is a link. A deadline-based "it did not hang" assertion is one of the two clock-based traps this repository has already been bitten by, **and it would also pass with the one-level rule broken**, because five levels of nesting also complete inside any reasonable deadline. |
| **A cycle test reappearing in a later change** | Not a test — a review rule. Any pull request adding a loop marker, a depth counter, or a fixture nesting two levels deep is reintroducing a mechanism whose input does not exist. It needs the nesting work first (ADR-083 D5's four prerequisites), not a fixture. |

> **X7 check for this phase:** tests 52, 57, 58, 112 and 113 must all fail before implementation.
> If test 57 passes already, the link fallback is firing for a reason unrelated to transclusion —
> most likely because transclusion is not implemented at all and every embed is falling back.

---

#### Step 4 — The external video host

**What a false green looks like here.** Three distinct ones, and all three have precedent.

1. **An allow-list equality test that compares a value to itself.** If both sides import the same
   constant, the assertion is a tautology. The Go side is a policy **string**; the test must parse
   the **served response header** and compare against what the reader actually uses.
2. **A browser measurement that measures nothing.** A journey that never presses play never loads a
   frame, so "zero frame-source violations" is guaranteed. The existing suite already solved this
   with a positive control (A0) that proves the violation channels fire; the new journeys must sit
   inside that same discipline.
3. **A "no network contact before play" test that passes because the embed never mounted.** Zero
   requests is also what a deleted feature produces.

| Risk | The test that catches it |
|---|---|
| Tautological allow-list comparison | `TestSpaCsp_FrameSrcAllowListMatchesServedHeader` parses the served header. Mutation-check: change one side only, confirm the test fails. |
| A violation channel that no longer fires | `csp-assumptions.spec.ts::A8-non-allowlisted-host-violates` — the **negative control**. A run in which A8 does not violate is a run whose A7 result means nothing. |
| A silent weakening of a neighbouring directive | `TestSpaCsp_FrameAncestorsAndImgSrcUnchanged` (byte-for-byte) plus the existing `A6` re-run **unchanged**. The framing rule and the picture rule share a string with the directive being edited and nothing else; the assertions are what prove that, not the prose. |
| A floor test relaxed to accommodate the new value | `TestSpaCsp_DirectiveFloor` (test 60) is **extended**, and its existing self-mutation half — which deliberately breaks the string to prove the checker can fail — must cover the new value too. A floor that is lowered to fit is no longer a floor. |
| **An "operator can decline it" test that passes because the host was never added** | Test **62**, and revision 1 left it unpaired. "Empty setting → no external host in the served policy" is satisfied by a build where the host does not exist. **Both halves in one test body:** the **default** install's header contains the host and matches §10.7's literal line; the **emptied** install's does not and differs from it. |
| **A byte-for-byte oracle that lives in a Markdown file nobody listed** | Not a test — a landmine. `TestSpaServedWithCSP` does not compare against a literal in the test file; it **reads `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` at runtime** and requires **exactly one** literal policy line in §10.7. Edit the policy without editing that document and CI fails on an assertion whose source is not in any impact list. It is now in the Impact Assessment and the Regression table. |
| **A second policy string changing unnoticed** | Test **114**. A derived policy is served on the PDF worker path; the new frame host propagates into it automatically. Nothing today asserts what happens to it, and "exactly the allow-listed hosts and nothing else" is a claim about **both** strings. |
| "No contact before play" passing on a missing feature | `knowledge-embeds.spec.ts::no-provider-contact-before-play` (test 69) asserts the **placeholder is present** (positive), then zero provider requests, then **exactly one** after pressing play. Only the three together mean anything. |
| The audit document going stale on merge day | Not a test — a merge condition. `docs/internal/architecture/csp-audit-2026-09-05.md` is amended **in the same change**, and the amendment must still state that two browsers remain unmeasured. This work does not claim the freeze. |

> **X7 check for this phase (revision 3):** run **59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 114**.
>
> **Must FAIL (9):** 59, 60, 62, 63, 64, 65, 66, 69, 114.
>
> **Expected to PASS — pins (3):** **61**, byte-for-byte assertions on two directives that have
> their asserted values today, so it compares a value to itself before the change (a real guard
> after it, worthless as first-run evidence); **67**, the negative control, which frames the
> non-allow-listed host **directly** rather than through the new embed path and therefore violates
> both before and after — a control that depends on the feature under test goes silent exactly when
> the feature breaks; **68**, the existing anti-framing assertion re-run unchanged, which is the
> whole point of re-running it.

---

#### Step 5 — Inline record editing

**What a false green looks like here.** The audit and attribution assertions are the ones most
likely to be written against an injected value. `EditNoteRequest` requires an actor; a test that
constructs the request itself will supply a perfectly good actor while the REST handler in
production supplies an empty one. Separately, "the event name is recognised" passes if the
recogniser accepts everything.

| Risk | The test that catches it |
|---|---|
| A person actor that only exists in the test's own request | `TestKnowledgeRecordFieldWrite_HumanActorRecordedOnDefaultInstall` (test 72) drives the **HTTP handler** on real boot wiring and reads the actor **out of the audit record**, not out of the request it constructed. **The structural claim revision 1 rested on was false:** the edit request type enforces neither an audit sink nor an actor — a nil sink writes the file and records nothing, silently — so the handler must construct its writer through the one constructor that does reject an unattributable actor. An implementer reading "call the edit function" bypasses it. |
| ~~An event-name recogniser that accepts anything~~ | **ROW DELETED — this is the X7 case, and it is the reason X7 exists.** Revision 1's test 73 and EMB-091 specified work that was **already done before this spec was written**: all five names are registered, and a test asserting exactly that, carrying the exact negative control this row demanded, already exists in the tree. The test would have been green on day one against zero implementation. The requirement, the test and a whole step-5 precondition are deleted; the remaining task is **task 116**, correcting the stale comment that said otherwise. |
| **An anonymous actor that cannot be told apart from an identified one** | Test **74** asserts three cases in one body — bypass + no user → **accepted**, actor is the anonymous token; authenticated → the prefixed user form; neither → **refused before the file is touched**. Test **117** then asserts the two populations separate under a plain text search. **N4 accepted that an anonymous entry cannot answer "who"; it did not accept that the two look alike.** A single-case test passes on a handler that records everything as anonymous. |
| **An editor gate that passes because no editors render** | Tests **75** and **76** each pair every negative with a **writable field in the same fixture asserted editable**. Test 76 in particular ("title and path both absent") passes on a component that renders no editors anywhere — which, before CW-4 and CW-5 land, is exactly what it would be, because the browser has no field-type information to gate on. |
| **A write path that accepts what the client should not have offered** | Test **115** is the second, independent guard. Even if the browser wrongly offers an editor for a calculated field, the write must be refused. Its third case — an ordinary property on the same record succeeding — is what stops "refuses everything" passing. |
| "No auto-retry" passing because nothing was ever sent | `recordFieldEditor.test.tsx::conflict-reverts-and-never-auto-retries` (test 77) — one request, still one after the window, exactly one more on press with the **fresh** token. |
| View state quietly written back to disk | `embeddedViewState.test.tsx::filter-is-local-and-never-written-back` (test 78) asserts the definition file's bytes are **unchanged**, not merely that no save was called. A save through a different path would evade the call-count assertion. |
| Invalidation that looks scoped but sweeps everything | `::invalidation-is-scoped-to-the-written-note` (test 79) uses **two** knowledge bases and asserts the second is never refetched. A single-knowledge-base fixture passes under a blanket invalidation. |
| **Scoped invalidation that is scoped so tightly nothing updates** | Test **118** is the mirror of test 79 and revision 1 had only one of the pair. Two embeds of the same view on one page: a write in the first must be visible in the **second**. Test 79 alone is satisfied by an implementation that invalidates nothing at all. |

> **X7 check for this phase (revision 3):** run **70, 71, 72, 74, 75, 76, 77, 78, 79, 115, 117,
> 118**. **Must FAIL: all twelve. Expected to PASS: none.** (116 is a task, not a test.)
> **Test 117 only belongs on this list in its rewritten form.** As revision 2 specified it, the test
> **wrote its own fixture** and then asserted a property of the strings it had just written — no
> production code was exercised, so it could not fail in **any** implementation, before or after.
> The rewritten form drives three real writes on real boot wiring and greps the file the product
> produced. **If any test in this phase passes on the first run, treat it as a finding, not a
> saving** — that is precisely how revision 1's deleted requirement survived two reviews.

---

#### Step 6 — Deferred kinds

**What a false green looks like here.** The sound renderer must be *extracted* from the preview
pane, not *copied*. A test that imports the new module and renders it proves the module exists — it
does not prove the pane stopped using its own private copy. Two copies both passing their own tests
is exactly the drift the markdown pipeline suffered three times.

| Risk | The test that catches it |
|---|---|
| A copy left behind in the pane | `LibraryAudioPreview.test.tsx::single-definition-used-by-both-surfaces` (test 80) asserts the pane and the embed mount the **same module identity**. |
| A second definition reintroduced later by a merge | `scripts/check-no-duplicate-renderer.sh` (test 81) — a source guard. **This is a source-text check, which this document otherwise forbids**, and it is justified here for the same narrow reason `library_isolation_policy_test.go` is: the property being asserted is *"no second copy of this exists in the tree"*, which is a property of the source and of nothing else. It is a supplement to the behavioural test, never a substitute. A note cannot stop `git merge`; a build gate can. |
| **The guard itself never firing** | **`scripts/check-no-duplicate-renderer.test.sh` (test 119) — the guard's own self-test, which revision 1 omitted.** A grep guard with a typo'd pattern exits 0 forever: that is the 673/673 failure verbatim, and it is the exact failure mode a guard script has. **This repository already has the convention** — `check-browser-tests-gated.test.sh`, `check-no-handwritten-wire-types.test.sh` and `check-no-tool-error-from-status.test.sh` and `check-no-removed-providers-selfcheck.sh` all sit beside the scripts they prove — note the fourth's different suffix (`-selfcheck.sh`) before naming the new file (m7). Plant a duplicate definition in a temp tree, assert non-zero exit; remove it, assert zero. **A guard without a self-test is not a gate, it is a decoration.** |
| Shipping speculative kinds at all | Not a test — a decision. Zero of the 784 notes use any step-6 kind. "Ship steps 0–5 and stop" is a legitimate outcome, and building on speculation is the more likely mistake than omitting these. |

> **The round-2 review's O4, and why its cheaper option was NOT taken.** O4 observed that step 6 was
> the only phase whose tests sat entirely outside the X7 sweep, and suggested that *"the cheapest way
> to satisfy M10 for step 6 may be to drop it from the plan rather than to write its X7 check."*
> **Rejected, and the check is written instead.** Dropping a phase to satisfy a completeness
> criterion is the same move as relaxing a floor to fit a new value: it makes the instrument read
> green by shrinking what it measures. Whether step 6 ships is a **product** decision — the row
> above states the case for not shipping it, and "ship 0–5 and stop" remains the recommended
> default — and it must not be made as a side effect of a bookkeeping repair. Writing the check cost
> four lines and revealed that three of the four tests fail today for a reason worth knowing:
> `LibraryAudioPreview` is module-private with no file of its own, so the extraction is a real
> prerequisite rather than a tidy-up.

> **X7 check for this phase (revision 3 — revision 2's check covered only test 119, leaving 80, 81
> and 82 outside every X7 list):** run **80, 81, 82, 119**.
>
> **Must FAIL (3):** **80** — `LibraryAudioPreview` is module-private inside `LibraryPreviewPane.tsx`
> with no file of its own, so the new test file cannot even import it; **81** — the guard script does
> not exist; **82** — the deferred kinds all take the link fallback today.
>
> **Expected to PASS (1, in one direction only): 119's clean-state case** — no duplicate exists yet.
> Then plant one and confirm it **fails**. A self-test that only ever runs in the clean state proves
> the script executes, not that it detects.

---

#### Cross-cutting — the mount budget

**What a false green looks like here.** Every assertion in this area is tempting to write against a
clock, and this repository has been bitten twice by exactly that: one test used a ~10 ms margin to
prove "no retry happened", another used a 2-second deadline to prove "the loop is bounded". Both
lie in both directions — spuriously red on a loaded machine, green on a fast one even when the
logic is broken.

| Risk | The test that catches it |
|---|---|
| Concurrency proved by elapsed time | `embedLazyMount.test.tsx::four-in-flight-ceiling-with-visible-queue` (test 84) **counts** in-flight requests at every scheduler tick. If the property is discrete, count it. Widening a threshold is never the fix. |
| "Not retried" proved by a timer | `::failed-module-not-retried-by-scrolling` (test 86) counts requests across ten scroll round-trips. |
| Lazy mounting proved by a screenshot | `::only-visible-modules-begin-work` (test 83) counts started evaluations, and asserts reserved space by test identifier. |
| ~~Print completeness proved on a short page~~ | **ROW DELETED (N3).** The guarantee is withdrawn, so there is nothing to prove and no way to prove it — the browser's print event is synchronous and cannot be held open while modules load. Revision 1 already suspected this (its own ambiguity A-9 said the test "must be written against the application's own print control … or the limitation stated"), and the founder resolved it by **dropping the promise** rather than by narrowing it to a print control this work does not build. |
| **The unmounted-limitation notice never rendering at all** | Test **89** asserts **three** states in one body: present while modules are unmounted **and naming both find-in-page and printing**; absent when all are mounted; never present on a note with no embeds. Any one of the three alone is passable by a notice that never renders. Dataset rows I10, I11 and I12. |
| **A per-kind reserved height that is one constant** | Test **96**, folded into this phase (revision 1 left it outside the register). Asserting "a height was reserved" passes on a single hardcoded value for every kind. The assertion is that the heights **differ per kind**, and that a transclusion reserves its stated three lines. |
| **A regression pin reported as evidence of progress** | Test **90** passes before and after, correctly — that is what a regression test is. Revision 2 left it outside **every** X7 list and outside SC-028's exclusion list, so its first-run pass would have been an unexplained anomaly in the one report where anomalies matter. Test **125** is the half 90 cannot carry: for `.pdf`, `.base` and `.md` the badge must **stop** firing, which fails today. 90 and 125 are a pair; 90 alone passes with the whole feature reverted. |

> **X7 check for this phase (revision 3 — this phase had NO X7 check at all in revision 2, which
> left eleven numbered rows outside every check in the document, including every mount-budget test:
> the ones this section itself calls "tempting to write against a clock", and therefore the ones
> where a first-run pass is most informative).** Run **83, 84, 85, 86, 87, 89, 90, 96, 125**.
>
> **Must FAIL (8):** 83, 84, 85, 86, 87, 89, 96, 125 — none of the lazy-mount module, the notice, the
> per-kind heights or the routed-away fallback exists today.
>
> **Expected to PASS (1): 90**, the regression pin. Its passing is the correct result and is
> recorded here so it is not reported as an anomaly.

---

#### Register coverage table

> **This table is the register's membership list, and Invariant 2 is measured against it** with the
> `diff` command given under the invariants above. Every numbered row in the two order tables has
> exactly one row here. The prose risk rows in each phase above remain the analysis; **this table is
> what "the register covers every test" means**, because a claim that can only be checked by
> scanning prose for names, bare numbers and ranges is a claim nobody re-checks — which is how 36
> tests sat outside a register asserting it covered them, in a revision written to prevent exactly
> that.
>
> **PIN** in the last column means the test is expected to pass on its first run and is **never**
> evidence a step landed. Every PIN here is also in SC-028's exclusion list, and the two lists are
> required to be identical.

| Test | Phase | The false-green this row is in the register for |
|---|---|---|
| 1 | Step 0 | A version field that is optional in practice — every existing caller keeps the unguarded behaviour |
| 2 | Step 0 | A version field accepted and ignored; the **stale** case is the load-bearing one |
| 3 | Step 0 | **Half-pin.** The "succeeds" half passes today; only the response-header half fails. Never cite alone |
| 4 | Step 0 | The binary door quietly exempted. Sequential-only — passes against a lock-free implementation |
| 5 | Step 0 | An audit record that exists only in tests, via an injected recorder |
| 6 | Step 0 | The same fix wired into one handler and not the other |
| 7 | Step 0 | An editor that sends back a token it invented rather than one it received (97/98 make this real) |
| 8 | Step 0 | "No auto-retry" passing because nothing was ever sent — plus the two clauses added in revision 3, since the first three pass on today's generic error path |
| 9 | Step 1 | A parser that reads the bar payload as a size when it is display text |
| 10 | Step 1 | A modifier silently honoured on a kind that cannot use it |
| 11 | Step 1 | "Never consults the graph" passing on a reader that recognises no external form (paired) |
| 12 | Step 1 | Two named views of one file collapsing to one edge — today's live bug — invisible on a one-view fixture |
| 13 | Step 1 | Five states produced from evidence shapes the server cannot emit |
| 14 | Step 1 | The missing-file sentence absent because nothing renders at all (paired) |
| 15 | Step 1 | A truncation flag honoured only in the shape the server never sets |
| 16 | Step 1 | Path conversion never happening — invisible on a root-mounted fixture; the decoy is asserted unrequested |
| 17 | Step 1 | A containment refusal that **is** the missing-file marker (paired: two edges, markers differ) |
| 18 | Step 1 | Ambiguity "reported" on a fixture with one match |
| 19 | Step 1 | Nothing routed; the fallback caught everything |
| 20 | Step 1 | **PIN.** Every non-image embed already renders the badge with no renderer and no download card |
| 21 | Step 1 | **Rewritten.** The revision-2 form's negative *and* its named pairing both passed today |
| 22 | Step 1 | The fence negative passing with the feature reverted; pairing kind now `.pdf`, since an image pairing was vacuous |
| 23 | Step 1 | One page-level error "asserted" on a fixture with one embed |
| 24 | Step 1 | A placeholder assertion satisfied by an empty node |
| 25 | Step 1 | A `variant` prop never read — cross-variant equality is trivially true then; control: container class differs |
| 26 | Step 1 | Same, saved-view renderer, including its "N views could not be loaded" notice |
| 27 | Step 1 | Same, PDF renderer |
| 28 | Step 1 | **PIN.** Satisfied by one worker per document — the absence of a pool |
| 29 | Step 1 | **PIN.** Per-document workers make failure isolation trivially true |
| 30 | Step 1 | **PIN.** "A fresh worker" is unconditional when every mount builds its own |
| 31 | Step 1 | The only one of 28–31 a pool is needed for: constructed-worker count ≤ 2 with a visible queue |
| 32 | Step 1 | A write that touches more than the named file, or a section silently not created |
| 33 | Step 1 | A refusal that does not enumerate what exists — the half that makes it useful |
| 34 | Step 1 | Refusal by `refuseOp` for the **wrong reason**; repaired with a message assertion and a succeeding pair |
| 35 | Step 1 | Same shape: `refuseOp` refuses a kind-mismatch today, indistinguishably |
| 36 | Step 1 | The **global** argument sweep masquerading as a per-operation one |
| 37 | Step 1 | Same shape: `refuseOp` refuses a missing token today, indistinguishably |
| 38 | Step 1 | **PIN / third net.** Ceiling, seeds and reconciliation are already exactly as asserted |
| 39 | Step 1c | A handler hardcoding `false` — the pair is what catches it |
| 40 | Step 1c | The anchor populated into the heading field; `BlockID`'s first assertion ever |
| 41 | Step 1c | A generated runtime validator weaker than the contract (probe with a control) |
| 42 | Step 2 | A matching ladder that stops at the wrong rung; the near-miss must fail |
| 43 | Step 2 | A duplicate display label silently resolved to one view — the original defect |
| 44 | Step 2 | A derived machine name that happens to be right (paired with a matched label) |
| 45 | Step 2 | **Deletable subject.** Passes on a single-view fixture with all matching removed; meaningful only with 46 |
| 46 | Step 2 | "First view" masquerading as label matching — two views with **different rows** |
| 47 | Step 2 | A caption "asserted" on a file that has one view anyway |
| 48 | Step 2 | A "server's reason" assertion satisfied by any string |
| 49 | Step 2 | A toolbar that never renders passing "absent at rest" — plus the cell-editor clause that makes A-5 real |
| 50 | Step 2 | Deduplication asserted with one embed |
| 51 | Step 2 | A report that checks file existence only and returns zero failures |
| 52 | Step 3 | Slicing "proved" on a note whose whole body is the section |
| 57 | Step 3 | Nested embeds rendering as content; and nested embeds landing in could-not-be-checked |
| 58 | Step 3 | A cache-key claim proved by one request in total |
| 59 | Step 4 | A tautological allow-list comparison — both sides importing one constant |
| 60 | Step 4 | A floor relaxed to accommodate the new value |
| 61 | Step 4 | **PIN.** Byte-for-byte assertions on values the string already has |
| 62 | Step 4 | "Operator can decline" passing on a build where the host was never added (paired) |
| 63 | Step 4 | An identifier rule that accepts anything |
| 64 | Step 4 | Zero external requests is also what a deleted feature produces (paired: placeholder present) |
| 65 | Step 4 | A frame created with the wrong attributes, or created more than once |
| 66 | Step 4 | A journey that never presses play measures nothing |
| 67 | Step 4 | **PIN / negative control.** Frames the host directly, so it cannot go silent when the feature breaks |
| 68 | Step 4 | **PIN.** The existing anti-framing assertion re-run unchanged |
| 69 | Step 4 | "No contact before play" passing because the embed never mounted |
| 70 | Step 5 | A raw frontmatter splice passing as the guarded typed path |
| 71 | Step 5 | A conflict body carrying no current token |
| 72 | Step 5 | A person actor that exists only in the request the test built |
| 74 | Step 5 | A single-case test passing on a handler that records **everything** as anonymous |
| 75 | Step 5 | An editor gate that passes because no editors render anywhere (paired) |
| 76 | Step 5 | Same, for title and path (paired with an ordinary field on the same row) |
| 77 | Step 5 | "No auto-retry" passing because nothing was ever sent |
| 78 | Step 5 | View state written back through a path a call-count assertion cannot see — assert the bytes |
| 79 | Step 5 | Invalidation that looks scoped but sweeps everything (two knowledge bases) |
| 80 | Step 6 | A private copy left behind in the pane while the new module passes its own tests |
| 81 | Step 6 | A grep guard with a typo'd pattern, exiting 0 forever |
| 82 | Step 6 | Deferred kinds routed to the fallback and reported as "rendered" |
| 83 | Cross-cutting | Lazy mounting proved by a screenshot rather than by counted evaluations |
| 84 | Cross-cutting | Concurrency proved by elapsed time — count, never a clock |
| 85 | Cross-cutting | A released slot proved by a timer |
| 86 | Cross-cutting | "Not retried" proved by a ~10 ms margin, which this repository has already been bitten by |
| 87 | Cross-cutting | Both staleness windows asserted by asserting one of them |
| 89 | Cross-cutting | A notice that never renders passes any single one of its three states |
| 90 | Cross-cutting | **PIN.** Regression over existing behaviour; passes before and after, correctly |
| 91 | Step 1 | The `0` half passing with the outline fetch never written; the `1` is load-bearing |
| 92 | Step 1 | An agent surface that marks nothing, "asserted" by the absence of wrong marks |
| 93 | Step 1c | A broken embed reported to an agent with no reason at all |
| 94 | Step 2 | A fixture whose link is present in the rows, so both resolvers agree and nothing is proved |
| 95 | Step 2 | A test whose subject is a sentence — paired against the pane, where it must be absent |
| 96 | Cross-cutting | One hardcoded reserved height for every kind |
| 97 | Step 0 | A read side with nothing to hand back — plus the binary and `too_large` rows that catch a token hashed from an omitted `content` field |
| 98 | Step 0 | The same, on the byte-stream door: the only read on the PDF save path |
| 99 | Step 0 | A compare-and-swap with no lock, passing every sequential test |
| 100 | Step 0 | Two writers on two **different** lock keys, passing test 99 by luck on a fast machine |
| 101 | Step 0 | A mandatory token that exempts the one caller that cannot supply one — and a header the production path cannot read (EMB-007c) |
| 102 | Step 1 | A skip cross-check built against a shape the server does not emit (paired: skip removed → missing-file sentence) |
| 103 | Step 1 | A matching rule tested only on the clauses that already work; the ancestor clause and four near-misses are the new content |
| 104 | Step 1 | A resolver that reads the edge and drops today's node-existence guard |
| 105 | Step 1 | **PIN.** Pins today's measured scan-level behaviour so a later "fix" cannot change it silently |
| 106 | Step 1 | Fifteen markers where one statement belongs — **and** a statement that diagnoses a cause it cannot know (B8d) |
| 107 | Step 1 | **PIN.** `'svg'` is already in `IMAGE_EXTENSIONS`; all three clauses hold today |
| 108 | Step 1 | The no-`<iframe>` negative holds today; the allow-listed pairing is what makes it fail |
| 109 | Step 1 | A classification divergence upstream of the renderer, which no cross-variant test can see |
| 110 | Step 1c | A `heading_found` hardcoded `false`; **narrowed to the Go surface**, its former SPA half is test 122 |
| 111 | Step 1c | A handler hardcoding either `unresolved_reason` value |
| 112 | Step 3 | A self-embed proved to terminate by a clock — which also passes with the one-level rule broken |
| 113 | Step 3 | Blank space passing as "says the note is empty" |
| 114 | Step 4 | A second, derived policy string changing unnoticed |
| 115 | Step 5 | A client-side gate trusted as the only guard; the third case stops "refuses everything" passing |
| 116 | Step 5 | *(Task, not a test.)* The stale comment that produced a deleted requirement, a deleted test and a deleted step-5 precondition — X7's own origin |
| 117 | Step 5 | **A test asserting a property of its own fixture.** Rewritten in revision 3 so the rows come from the production actor formatter |
| 118 | Step 5 | Invalidation scoped so tightly that nothing updates — the mirror of 79 |
| 119 | Step 6 | **PIN in its clean case only.** A self-test that only ever runs clean proves the script executes, not that it detects |
| 120 | Step 1c | The agent surface still saying "does not exist" about a walk-skipped file (paired: an ordinary unresolved link still reads as absent) |
| 121 | Step 0 | Two token definitions that are each "correct" and disagree — surfacing only as an unclearable 409 |
| 122 | Step 1c | "No such heading marker absent" passing on a reader that never renders it (paired with a genuinely missing markdown heading) |
| 123 | Step 0 | A quoted-vs-bare shape error reported as **409**, indistinguishable from a real conflict, forever |
| 124 | Step 0 | A lock key derived from the **outer** collection when knowledge bases nest |
| 125 | Cross-cutting | The link fallback continuing to fire for kinds this work routes away from it, while test 90 reports green |

---

## Functional Requirements

> Numbering is grouped by area; gaps are deliberate so a group can grow without renumbering.
> Requirements **deleted in revision 2** keep their numbers as struck-through entries rather than
> being reused, so every traceability row and every review finding stays followable.

### Closing the unguarded save door (US-1)

- **EMB-001**: Both whole-file Library save endpoints MUST require a version token, and MUST reject a
  request that omits it or sends it empty with HTTP 400. **No caller is exempt (N2).**
- **EMB-002**: Both whole-file Library save endpoints MUST reject a stale version token with HTTP 409
  and a typed body naming the path, the token sent, and the token the file now has.
- **EMB-003**: Both whole-file Library save endpoints MUST write an audit record for every accepted
  write, on a default installation with no test wiring.
- **EMB-004**: The Library editor MUST send back the version token it received, MUST surface a
  conflict to the person, and MUST NOT resend a refused save on its own.
- **EMB-005**: The version-token fields, the response header carrying the token, and the typed
  conflict body MUST be defined in `contracts/` before any handler or client code, with generated
  artefacts committed in the same change.
- **EMB-006** *(new)*: The version comparison and the write MUST occur inside a **single**
  acquisition of the same lock the agent write path takes — the same collection root, the same lock
  directory and the same collection-relative path. A comparison followed by an unheld write is not a
  compare-and-swap and MUST NOT be shipped as one. Where the file lies outside any knowledge base,
  the in-process half of that lock still applies and the cross-process half is absent; that MUST be
  described as degraded, not as the full guarantee.
- **EMB-006a** *(new in revision 3 — M7: the derivation EMB-006 assumes and nothing provides)*: The
  Library handler is given a **workspace-relative** path against the **workspace** root; the lock
  EMB-006 requires is keyed on a **collection** root and a **collection-relative** path. That
  conversion is **work, and it does not exist**: the only knowledge-base detection in
  `rest_library.go` is `annotateKnowledgeBaseEntries`, which answers "is this *directory entry* a
  knowledge base" via `knowledge.Detection.IsKnowledgeBase`. There is no "which collection encloses
  this file" helper anywhere. The handler therefore MUST:
  1. Resolve the **enclosing collection root** for the file by walking the path's ancestors and
     applying **the same rule `knowledge.Detect` uses** — not a second, hand-rolled marker test.
  2. Where knowledge bases **nest**, take the **innermost** enclosing collection. This is stated
     because it is the one input the two writers can silently disagree about, and disagreement is
     exactly the "decorative guard" failure EMB-006 exists to prevent.
  3. Obtain the lock directory from `knowledge.LockDirFor(home, collectionRoot)` — never by
     constructing the path, which would put the lock inside the operator's vault.
  4. Where **no** collection encloses the path, `CollectionRoot` is empty, `LockDir` is empty, and
     the mode is the **degraded** one EMB-006's second sentence describes: in-process serialisation
     only, cross-process advisory locking absent. Degraded, not disabled, and never described as the
     full guarantee.
- **EMB-007** *(new, amended in revision 3)*: Every read that a write may follow MUST return the
  current version token as a response header — including the **byte-stream download**, which is the
  read on the annotated-PDF save path and today returns nothing the caller could send back. The PDF
  editor MUST capture that header on load, send it on save, and replace it from the save's own
  response so a second save in the same session does not conflict with the first.
- **EMB-007a** *(new in revision 3 — C3(r2)(a)(b): the token's VALUE, which revision 2 never named)*:
  **The two doors share the existing knowledge token — founder ruling, closing A-11.** The value MUST
  be produced by `pkg/knowledge/version.go`'s existing code and by nothing else:
  - For a Library file **inside** a knowledge base: `knowledge.ReadNoteVersion(collection, rel)`,
    whose `Token` field is the answer. It streams the file from disk through
    `computeVersionTokenFrom`, so it is already the "raw bytes" computation and already handles the
    absent-file case with `knowledge.TokenAbsent`.
  - For a Library file **outside** every knowledge base: the same computation over the same bytes.
    `knowledge.ComputeVersionToken(content []byte)` is exported and correct but takes the whole file
    in memory; the streaming half (`readNoteVersionAbs`) is unexported. **This is a named piece of
    work, not an assumption:** export a thin sibling — `knowledge.ReadFileVersion(abs string)
    (NoteVersion, error)`, delegating to the existing `readNoteVersionAbs` — so both populations and
    both doors produce a **byte-identical** token from one implementation. Defining a second
    "changed" is the failure this requirement exists to prevent.
  - **No caller derives a token from size and modification time.** `version.go`'s own header spends
    four paragraphs on why that is insufficient, and `NoteVersion` carries `Size` and `ModTime`
    explicitly marked *"carried for display and as a cheap pre-filter — never as the decision."*
  - **The token MUST NOT be computed from a response body.** `library.ContentResult`
    (`pkg/library/content.go`) omits `Content` by design for a binary file and for a text file over
    `MaxContentBytes`, and `handleLibraryContentGet` builds its response from that struct. A token
    derived from `ReadContent` would therefore be the hash of an **empty string, identical for every
    binary file in the workspace** — on the **PDF door**, the one N2 explicitly refuses to exempt.
    `getLibraryContent` MUST read the file's bytes itself to compute the header **even when it
    returns no `content` field at all** (binary, `too_large`). `handleLibraryDownload` already holds
    the bytes; `handleLibraryContentGet` does not, and that read is the new work.
- **EMB-007b** *(new in revision 3 — M5: the header's SHAPE, and where it lands)*: The token crosses
  the wire in **two different forms**, and confusing them produces a 409 that looks exactly like a
  genuine conflict on the PDF save path, with no way for a person to make progress. So:
  - **On the wire as a header**: the RFC-conformant **quoted strong** form — `ETag: "v1:…"` —
    matching the `contracts/openapi.yaml` providers-catalogue precedent the ADR cites, and matching
    what Go's `http.ServeContent` can actually parse (`scanETag` requires a leading `"` or `W/"` and
    **silently ignores** an unquoted value). A weak (`W/`) form MUST NOT be emitted.
  - **In a request body**: the **bare** token in `expect_version`, unquoted. The client strips the
    surrounding quotes on read; the server accepts the bare form and MUST reject a quoted one with
    **400**, not 409, so a shape error can never be mistaken for a conflict.
  - **Where it lands on the download path**: `handleLibraryDownload` reaches the bytes through
    `serveLibraryContent`, whose body is `applyLibraryByteHeaders(...)` then
    `http.ServeContent(...)`, and whose own doc comment says `ServeContent` is used *"for what it is
    genuinely good at: Range requests (audio and video seeking) and conditional GETs."* Setting an
    `ETag` before that call therefore changes behaviour for **every** caller of the helper —
    `If-None-Match` starts producing 304s and `If-Range` starts validating against the ETag instead
    of `Last-Modified` — and `serveLibraryContent` is also reached from `serveLibraryPath`. The
    header MUST be set in **`handleLibraryDownload` only**, not inside `applyLibraryByteHeaders`
    and not inside `serveLibraryContent`, so the preview-token path and the audio/video Range path
    are unchanged. If a later change moves it into the shared helper, the conditional-GET and
    `If-Range` consequences MUST be assessed and regression-tested first; a regression row for the
    Range path is in the Regression table for that reason.
- **EMB-007c** *(new in revision 3 — M6: how the header reaches SPA code at all)*: EMB-004 requires
  the Library editor to *"send back the version token it received"* and EMB-007 requires the PDF
  editor to *"replace it from the save's own response"*. Both need a **response header** to reach
  component code, and **the SPA's single API entry point discards them.**
  `src/lib/api.ts::request<T>(path, init?, schema?)` returns the parsed body and nothing else;
  `res.headers` is read exactly twice in the whole file — once for `Content-Length`, and once at
  `providersCatalogETag = res.headers.get('ETag')`, which sits inside a **bespoke hand-rolled
  fetch** with module-level ETag state, not inside `request<T>`. The PDF *loader* is fine — it
  already uses a raw `fetch` and can read the header off the `Response` it holds — but
  `putLibraryContentBinary` and `putLibraryContent` both go through the helper, so **the new token in
  a PUT response is unreachable today**. This work MUST choose, and record, one of:
  - **(a) Widen `request<T>`'s return contract** so a caller can opt into the response's headers.
    This touches every SPA API call site's type surface and is the larger blast radius.
  - **(b) Add bespoke fetches** beside the providers-catalogue one, for the four Library
    version-carrying operations only.
  **Recommended: (b)**, scoped to the four operations, because (a) changes the return type of the
  one function every SPA API call passes through, for four callers' benefit. `src/lib/api.ts` is in
  the Symbols table and in CW-1's `d=1` impact column for this reason. **An unchosen mechanism is
  how test 101 gets written against a stub that hands the component a header production cannot
  obtain.**

### Resolution and honesty (US-2)

- **EMB-010**: The system MUST recognise an external allow-listed destination **before** consulting
  the link graph, and MUST never report such a destination as a missing file.
- **EMB-011**: The system MUST identify an embed's graph edge by embed-flag, written target, heading
  fragment and block anchor **together**; two embeds of one file under different fragments MUST
  resolve independently.
- **EMB-012**: The system MUST distinguish five states — resolved, unresolved, loading,
  graph-unavailable, and indeterminate — and MUST NOT collapse any two of them.
- **EMB-013**: When the graph loaded and no edge matched, the system MUST render a marker that is
  visually distinct from the missing-file marker, MUST state that the embed could not be checked,
  MUST give the reason where one is available, MUST say **"no reason available"** where none is, and
  MUST NOT state that nothing carries that name.
- **EMB-014**: When the graph request fails, the system MUST render exactly one page-level error with
  the failure and a retry, and MUST NOT render a per-embed marker or a persistent placeholder. The
  same single-statement treatment MUST apply when the graph **succeeds with no edges and no skips**
  — the answer given for a knowledge base outside the caller's scope — rather than one
  could-not-be-checked marker per embed. **The statement MUST NOT assert why the answer was empty**
  (m8, revision 3): the same zero-edges-zero-skips shape is also produced by a perfectly valid
  collection whose note simply was not indexed, and a statement reading "this knowledge base
  returned nothing" would then be a false claim about a note that exists and has fifteen embeds.
  The wording is *"the knowledge base returned no link information for this note"* — what was
  observed — and never a diagnosis of the cause. Dataset **B8d** pins the second producer.
- **EMB-015**: While the graph request is in flight, the system MUST render a correctly-sized
  reserved placeholder and MUST NOT render any marker.
- **EMB-016**: An unresolved embed MUST name its target and its reason; where the reason is a missing
  view or a missing heading, it MUST list what does exist.
- **EMB-017**: An embed naming a target outside the knowledge base MUST be refused at read time, the
  target MUST NOT be read, the marker MUST be **distinct from the missing-file marker**, and it MUST
  NOT contain the escaping path.
- **EMB-018**: When more than one edge matches, the system MUST render the first in response order
  **and** report the ambiguity naming the alternatives.
- **EMB-019** *(widened in revision 3 — O1)*: The client MUST honour the response's truncation flag
  as an honesty signal even though the current server never sets it for this query kind, and MUST do
  so in **both** shapes it can occur in: a truncated answer with **no matching edge**, and a
  truncated answer with a **matching unresolved** edge. Revision 2 reached the flag only through
  EMB-013's *"the graph loaded and no edge matched"* branch, so a matching unresolved edge on an
  admittedly incomplete answer fell through EMB-021 (no skip) straight to the **missing-file
  marker** — an assertion of absence drawn from an answer the server has already said is
  incomplete. Not live today (`resp.Truncated` is set only in the neighbourhood branch — measured),
  which is exactly why it belongs in the client: EMB-019 exists because *"a client that ignores an
  honesty flag is one change away from a false statement"*. Dataset **B7a** covers the second shape.
- **EMB-020**: `knowledge_read` MUST mark an embed as an embed in its links section, MUST mark an
  unresolved embed as unresolved with its reason, and MUST describe a saved data view as a view
  rather than as a heading.
- **EMB-021** *(new, amended in revision 3)*: Before rendering the missing-file marker for an
  **unresolved** edge, the system MUST consult the answer's skip list for an entry naming that
  embed's target, and MUST render the could-not-be-checked marker with the skip reason if one is
  found. Every comparison below runs on **normalised, `/`-separated, cleaned, collection-relative**
  paths — the same normalisation the resolver already applies to the edge's target. A skip entry
  matches when **any** of these holds:
  1. **Path equality** — the skip's path equals the edge's target.
  2. **Basename equality** — the skip path's final segment equals the target, with and without a
     markdown extension.
  3. **Ancestor prefix** *(added in revision 3 — the case the C2 correction missed)* — the skip's
     path names a **directory** that is a proper ancestor of the edge's target: the target begins
     with the skip's path followed by `/`. A bare basename match MUST NOT be treated as a directory
     match; only clause 3 suppresses for a whole subtree, and only on a full path-segment boundary,
     so a skip naming `notes/priv` never suppresses a target under `notes/private/`.
- **EMB-021 rationale and the two traps it steps around.**
  *(a)* Without clause 3 the **dominant walk-level shape stays broken.** `WalkContained`'s `ReadDir`
  error branch records an unreadable directory under **the directory's own path**
  (`pkg/knowledge/contain.go`, `RelPath: cur.rel`) and every file beneath it simply never enters
  `walk.Files` — so a link to `notes/private/plan.md` yields a matching **unresolved** edge while
  the only skip entry in the answer says `notes/private`. Clauses 1 and 2 both miss (`notes/private`
  ≠ `notes/private/plan.md`; `private` ≠ `plan`), and the reader prints *"Nothing in this knowledge
  base is named plan"* about a file that exists. The ADR's own §13 C2 row records this shape — *"a
  directory that cannot be listed is walk-level and takes its files with it"* — and clause 3 is what
  finally implements it.
  *(b)* Clause 3 MUST NOT sweep up the **deliberately unreported** directories. `contain.go`'s
  `mode.IsDir()` branch skips `scanSkippedDirNames` (`.obsidian`, `.omnipus-vault`, `.git`,
  `.trash`) with `continue` and **appends nothing to `Skipped`**, with an in-place comment saying
  the omission is deliberate (*"Skipped means content this walk could not address … Tool state is
  not content"*). A link into one of those directories therefore has **no** skip entry, correctly
  reports absence, and is untouched by clause 3. This is stated so that an implementer does not
  "fix" the gap by adding those names to the skip list, which would convert every correct absence
  report inside them into a could-not-be-checked marker.
- **EMB-021a** *(new in revision 3 — the agent half of the same guarantee)*: The **agent** surface
  MUST apply the identical cross-check. `knowledge_read`'s link projection MUST consult the same
  walk-skip set the reader consults, using the same three clauses, and MUST report a skipped target
  as **could not be checked, with the skip reason** — never as plainly unresolved. This requires
  `ReadLink` to carry the skip-derived reason (it has ten fields today and no place to put one) and
  `renderReadLinks` to print it, alongside the `Embed` marker EMB-020 already adds.
  *(US-2's own narrative states the obligation — "the same honesty is owed to agents. An agent
  summarising a fifteen-module dashboard must be able to say 'three of these are broken' rather than
  quietly summarising the twelve that worked" — and revision 2 implemented it on the reader only.
  `knowledge_read` never sees the graph answer: it projects `ResolvedLink` into `ReadLink` and
  `renderReadLinks` prints `"  %s (unresolved) %s"`, so for a walk-level skip the agent was told the
  target is unresolved with no reason and would summarise it as missing. The reader got the
  corrected sentence and the agent got the old one — and the agent's output is the surface that gets
  pasted into a report. Founder decision **D-B** requires both surfaces, so this is a requirement,
  not a residual.)*
- **EMB-022** *(new)*: The resolution procedure MUST consult the answer's node list as well as its
  edge list. A node reporting that the target does not exist MUST NOT be resolved. Where the edge's
  resolution and the node's existence flag **disagree**, the system MUST render the
  could-not-be-checked marker rather than believing either. *(This guard exists in today's code and
  revision 1's five-state model dropped it.)*
- **EMB-023** *(new)*: The answer MUST distinguish, per unresolved edge, whether nothing matched or
  the target lay outside the collection root. **This is a contract change (CW-2), not a client
  inference** — today one value carries both facts and the reader cannot tell them apart.
- **EMB-024** *(new; the asymmetry is RATIFIED by founder ruling in revision 3, not merely
  tolerated)*: The agent surface MUST carry the containment reason on an unresolved embed's rendered
  line, **and MUST continue to show the escaping path itself**, unlike the reader surface. Redaction
  on the agent surface was considered and **rejected as theatre**: an agent reading a note already
  receives that note's body, so hiding the path in the links list removes the agent's ability to say
  **which** embed is broken without removing its access to the path. A redaction that costs
  diagnostic precision and buys no confinement is a control that only appears to be one. The
  asymmetry is deliberate, is stated in the ADR (§13, D4), and MUST NOT be "fixed" by a later change
  that reads it as an oversight.

### Kinds, paths and renderers (US-3)

- **EMB-025**: Each of the eleven kinds MUST have exactly the disposition in the scope table; `html`,
  `other` and `text` MUST fall back to the link treatment and MUST NOT mount a renderer.
- **EMB-026**: The download card MUST NOT be reachable from any embed, including the oversized and
  binary fallbacks.
- **EMB-027**: There MUST be exactly one renderer per kind, shared between the full-screen pane and
  the inline embed. No inline-only copy may be created.
- **EMB-028**: A renderer's layout variant MUST change layout only — height, overflow, and whether
  chrome is shown. It MUST NOT change what is fetched, which states exist, or how any non-happy
  state is rendered.
- **EMB-029**: The resolver MUST convert a knowledge-base-relative path to a workspace-relative path,
  using the reader's existing derived root, before that path reaches any renderer or endpoint.
- **EMB-030**: A size given after a bar MUST apply to pictures only. On any other kind it MUST be
  refused at write time and ignored at read time.
- **EMB-031**: An SVG embed MUST be drawn inside an image element and MUST NOT be injected inline
  into the document.
- **EMB-032**: At most two PDF worker instances MAY exist page-wide; a lease beyond that MUST show a
  visible waiting state; a worker error MUST reject only the leases held on that worker; and a
  failed worker MUST be terminated and removed so the next lease creates a fresh one.
- **EMB-033**: Embed notation inside a fenced code block or inline code MUST mount nothing and MUST
  render no marker.
- **EMB-034** *(new)*: Inline kind classification MUST use the file's **extension only**. It MUST
  NOT fabricate the media type or the text-editability flag that the full-screen preview's
  classifier reads, because a link graph carries neither and no single-entry read exists to fetch
  them. The three cases where this diverges from the pane — an image, a video and a text file whose
  filename does not identify it — MUST be asserted explicitly by a test that classifies the **same
  file** for both surfaces.

### Wire facts (US-4)

- **EMB-035**: The graph edge MUST carry whether a named heading was found in the target.
- **EMB-036**: The graph edge MUST carry a block anchor in its own property, separate from heading
  text.
- **EMB-037**: All **three** new properties MUST be defined in `contracts/` first, populated by the
  handler, and committed together with their generated artefacts.
- **EMB-038**: The system MUST fetch a target note's outline only when the missing-heading case
  fires, never in advance — **and MUST make exactly one such request when it does.**
- **EMB-039** *(new, amended in revision 3 — M4)*: The heading-found flag is meaningful **only**
  when the target is markdown **and** a heading fragment was written. This is a rule about **two
  surfaces**, and revision 2 gave it a test on neither:
  - **Server (Go):** the handler MUST set the flag `false` for a `.base` target and for a block
    reference, by construction — the graph builder records headings only for markdown files. **Test
    110.**
  - **Reader (SPA):** the reader MUST NOT consult the flag for a `.base` target or for a block
    reference, and MUST NOT render the "no such heading" marker in either case. **Test 122**, which
    is a **paired** assertion: the same fixture also contains a markdown target whose heading
    genuinely is missing and which **does** render the marker. Unpaired, "the marker is absent"
    passes on a reader that never renders that marker — which is today's reader.
  *(Consulting the flag on a `.base` target renders the "no such heading" marker across all 75
  dashboard embeds. Revision 2 mapped this requirement to a single row that carried a **Go** test
  name at **Integration** level while stating a **React** assertion; one test cannot be both, and
  the surface the rule actually governs had no test at all.)*

### Saved data views (US-5)

- **EMB-040**: A fragment on a data file MUST name a display label; the machine name MUST be obtained
  from the server and MUST NEVER be reconstructed by the client.
- **EMB-041**: Label matching MUST proceed exact label, then case-insensitive label, then machine
  name, and MUST stop at the first match.
- **EMB-042**: Two views sharing a display label MUST be refused, naming both, and neither rendered.
- **EMB-043**: A data file embedded with no fragment MUST render the first available view **and**
  display a caption naming the view shown and stating that the embed did not choose one. It MUST
  NOT render the other views as tabs.
- **EMB-044**: A view the server marks unservable MUST render the server's own reason in place,
  regardless of whether writing such an embed is permitted.
- **EMB-045**: The view-list request MUST be keyed on the data file's path so that N embeds of one
  file share one request.
- **EMB-046** *(amended in revision 3 — M11, and this time in the requirement rather than in a
  sentence claiming it is in the requirement)*: An embedded view MUST reveal its **controls** on
  pointer hover, on keyboard focus, and on a tap of the embed's header strip (A-4's touch path), and
  MUST NOT show them at rest. **"Controls" here means the view's toolbar chrome — and only that.**
  An **editable cell is content, not chrome**: it stays **always active and always
  keyboard-reachable**, showing its affordance on hover of *the cell*, and MUST NOT be hidden by this
  rule. An implementer applying the toolbar rule to the whole embed hides the editors, which makes
  founder ruling **D-D** invisible on the exact surface D-D is about. See EMB-088.
- **EMB-047**: A change to a view's filter, sort or visible columns MUST remain local to that one
  embed for that session, and MUST NEVER be written to the shared view definition.
- **EMB-048**: Links inside an embedded view MUST resolve against the note reader's link graph, and
  the click destination MUST open in the reader the person is already in.
- **EMB-049**: The system MUST state in the interface that an embedded view's links may render
  differently from the same view in the full-screen pane.

### Transclusion (US-7)

- **EMB-055**: A note, a section of a note, or an anchored block MUST be renderable inside another
  note, **one level deep**.
- ~~**EMB-056**~~: ~~cycle detection using the render stack~~ — **DELETED (N1).** Its input is
  unreachable: EMB-060 makes a second level of transclusion impossible, so a cycle cannot be
  constructed by any sequence of product actions.
- ~~**EMB-057**~~: ~~a note reached by two different parents is not a loop~~ — **DELETED (N1).**
  A diamond collapses to two links; there is no loop reporting to get wrong.
- ~~**EMB-058**~~: ~~nesting stops at five levels with a visible message~~ — **DELETED (N1).**
  Depth never exceeds one, so a five-level cap can never fire.
- **EMB-059**: Transcluded text MUST be obtained through the existing file-content request and cache
  key; heading and block slicing MUST run client-side.
- **EMB-060**: Embeds inside a transcluded note MUST render as the link fallback with a one-line
  reason, and MUST NOT render as could-not-be-checked markers. **This is the single unqualified
  rule of US-7 (N1)** — it admits no depth, no exception, and no second level.
- **EMB-061** *(new)*: A note embedding itself MUST render its own text once, render the inner
  self-embed as a link under EMB-060, and settle. Termination MUST be a consequence of EMB-060 and
  MUST NOT be asserted by a deadline — a deadline-based assertion also passes with EMB-060 broken.
- **EMB-062** *(new)*: A transcluded note with no content MUST render a stated empty region, never
  blank space and never a failure marker.

### Mount budget (US-8)

- **EMB-065**: An embed MUST begin work when it enters the viewport plus a margin and MUST stop when
  it is well outside; there MUST be no limit on how many embeds a note may contain.
- **EMB-066**: The system MUST reserve a per-kind height before mounting; a transclusion's reserved
  height is a fixed three lines and one reflow is accepted.
- **EMB-067**: At most four view evaluations MAY be in flight page-wide; any beyond that MUST show a
  visible waiting state; an embed scrolled out of view before its turn MUST release its place.
- **EMB-068**: A failed evaluation MUST render its own error with the server's reason and a manual
  retry, and MUST NOT retry automatically on re-entering the viewport.
- **EMB-069**: An embed MUST inherit both staleness windows — sixty seconds for a view result, ten
  seconds for a view list — and MUST NOT refetch when the window regains focus.
- ~~**EMB-070**~~: ~~every embed MUST be mounted before a print completes~~ — **DELETED (N3).**
  The browser's print event is dispatched synchronously and cannot await asynchronous work, and a
  print started from the browser's own menu cannot be held at all. The requirement promised
  behaviour the platform does not offer, and a handler written against it would have appeared to
  work on a warm cache and failed when the modules were slow. Replaced by EMB-071's second clause.
- **EMB-071**: The reader MUST state, while any embed on the page is unmounted, that **find-in-page
  and printing** will not reach unmounted embeds; the statement MUST disappear once every embed is
  mounted; and it MUST NOT appear on a note with no embeds. All three states MUST be asserted in one
  test, because any one of them alone is satisfied by a statement that never renders.

### External video (US-9)

- **EMB-075**: The system MUST frame only a host on the configured allow-list, whose shipped default
  contains exactly one entry, and MUST recognise it only from a markdown-link form.
- **EMB-076**: A video identifier MUST match exactly eleven characters from the unreserved set; the
  frame URL MUST be constructed from it plus an optional integer offset; a caller-supplied query
  string MUST NOT be forwarded.
- **EMB-077**: The pre-play placeholder MUST be drawn from local assets only; no request MUST reach
  the provider or any external host before a person presses play.
- **EMB-078**: The frame MUST be sandboxed without top-level navigation and without popups, MUST send
  no referrer, and MUST permit only encrypted media, picture-in-picture and fullscreen.
- **EMB-079**: The application's served policy MUST gain exactly the allow-listed frame hosts and
  nothing else; the rule preventing the application from being framed and the picture-source rule
  MUST be byte-for-byte unchanged; the separate preview-isolation policy MUST NOT be touched.
- **EMB-080**: The allow-list the reader uses MUST equal the allow-list in the served policy, asserted
  by a test that reads the served header.
- **EMB-081**: The allow-list MUST be an operator configuration key, defaulting to enabled; when
  emptied, no external frame host appears in the served policy and the embed renders as a link.
- **EMB-082**: The browser measurement MUST be re-run with an allow-listed video journey and a
  non-allow-listed control, the existing anti-framing assertion re-run unchanged, and the security
  audit document amended in the same change. The change MUST NOT claim the measurement freeze.

### Inline record editing (US-10)

- **EMB-085**: A record-field write MUST go through the same lock, version compare-and-swap, atomic
  write and audit path an agent's write uses, **via the existing typed record-write contract** wired
  to a new path (CW-7). It MUST NOT reuse the whole-file save endpoint, and it MUST NOT route
  through the raw frontmatter property-setter, which carries **none** of that contract's guards.
- **EMB-086**: A stale version token MUST return HTTP 409 with the typed knowledge conflict body.
  The token MUST be reachable **per row** on the view answer (CW-6); reading each record separately
  before each edit is not an acceptable substitute, because it is one request per edit **and** it
  opens a fresh race window between the read and the write.
- **EMB-087**: A refused write MUST restore the stored value, state that it changed while the person
  was editing, offer a retry showing the current value, and MUST NEVER be resent automatically.
- **EMB-088**: An editor MUST be offered only for field types the record's own definition describes
  as editable. A **derived** property, a **relation or person** property, a list-valued field, and a
  field type the definition does not describe MUST each get no editor and a way to open the note
  instead. **Every such assertion MUST be paired, in the same fixture, with an editable field that
  IS offered an editor** — otherwise a component rendering no editors at all satisfies the whole
  requirement. **The editor an editable field IS offered is not hover-gated** — EMB-046's toolbar
  rule governs the view's chrome, never a cell. A cell editor MUST be reachable by keyboard while
  the toolbar is hidden, and test 49 asserts exactly that (M11): without it, nothing in this plan
  would catch an implementer who applied the hover rule to the whole embed.
- **EMB-089**: A record's title and path MUST NOT be editable here, **while an ordinary field on the
  same row is** — asserted together.
- **EMB-090**: A person's write MUST be audited with a reserved person actor derived from the
  authenticated user's **stable identifier** — never a display name, never an email address —
  distinct from every agent identifier and never empty. The handler MUST construct its writer
  through the constructor that rejects an unattributable actor; the edit request type itself
  enforces **neither** an audit sink nor an actor, and a nil sink writes the file and records
  nothing with no error.
- ~~**EMB-091**~~: ~~the audit system MUST recognise the knowledge event names~~ — **DELETED. This
  work was already done before this spec was written.** All five names are registered, and a test
  asserting it with the prescribed negative control already exists in the tree. The requirement came
  from a stale comment claiming otherwise; correcting that comment is the only remaining task. If a
  **sixth** event name is ever emitted, this requirement returns for that one name, with the
  negative control kept.
- **EMB-092**: A successful write MUST refresh the view results for the knowledge base written to,
  and the written note's own content, outline and links — and nothing else. **Every embed of the
  affected view on the page MUST show the new value**; scoping the invalidation is only half the
  requirement, and the half revision 1 stated alone is satisfied by invalidating nothing.
- **EMB-093**: When no person can be identified because authentication bypass is active, the write
  MUST be **accepted** and recorded with the actor `anonymous` — the literal token, unprefixed, so
  that a plain text search separates it exactly from the prefixed identified-person form
  (**founder ruling N4**, which overrules revision 1's HTTP 503). A request with **neither** an
  authenticated user **nor** bypass active MUST be refused **before the file is touched**: N4
  permits an unattributed write, never an unattributable one.
- **EMB-094** *(new)*: The view answer MUST carry, per cell, its declared type, its allowed values
  where it has them, and whether it is derived or a relation (CW-5); and the record's field
  declarations MUST be readable over the wire (CW-4). **Without both, EMB-088 cannot be evaluated by
  the client at all** — today a cell is a property name and a rendered string, and nothing more.

### Agent authoring and tool policy (US-11)

- **EMB-095**: Embed authoring MUST be an operation on the existing note-editing tool, not a new tool
  name.
- **EMB-096**: The change MUST alter **no** tool-policy entry — not in the global ceiling, not in any
  per-agent seed, and nothing added by the reconciliation pass.
- **EMB-097**: The tool MUST write only the file named by the caller, creating the destination
  section if absent.
- **EMB-098**: The tool MUST validate at write time that the target resolves inside the knowledge
  base, that a named view label exists, and that a named target heading exists — refusing and
  listing what does exist otherwise.
- **EMB-099**: The tool MUST refuse a modifier that does not apply to the target's kind rather than
  accepting and ignoring it.
- **EMB-100**: The tool MUST enforce a **per-operation** accepted-argument set in addition to the
  existing global sweep, so an argument no operation reads for that operation is refused.
- **EMB-101**: The tool's target-fragment arguments MUST be named distinctly from the existing
  destination-section argument, so no argument name has two opposite meanings.
- **EMB-102**: A version token MUST be required, and an empty one refused.

### Deferred kinds and regression (US-12)

- **EMB-105**: Sound, video, diagram files and PDF page fragments MAY be embedded, using the same
  components the full-screen preview uses. The pane-private sound renderer MUST be extracted into
  its own module before reuse, and exactly one definition of it may exist.
- **EMB-106**: The existing link-with-a-badge fallback MUST continue to fire for every kind that
  reaches it, including kinds whose step has not yet shipped.

---

## Success Criteria

- **SC-001**: Both whole-file Library save endpoints reject a **missing or empty** token with 400 and
  a **stale** one with 409, and write an audit record on an accepted save, verified on a default
  installation with no test-only wiring. Measured: 6 integration tests pass; the audit record is
  read back from the sink.
- **SC-001a**: An agent's write released **between** the handler's version comparison and its write
  results in exactly **1** surviving write, and the loser receives a 409. Measured by a test seam,
  not by timing. **0** of the pre-existing sequential stale-token tests can detect this.
- **SC-001b**: The lock key the Library handler takes is byte-identical to the key the agent path
  takes for the same file: same collection root, same lock directory, same relative path. Asserted
  on the key itself, not inferred from SC-001a passing.
- **SC-001c**: The annotated-PDF save completes end to end with a token it obtained from the
  download response header. **0** callers of either save endpoint are exempt from the token.
- **SC-001d** *(new, revision 3)*: For the same file, the `ETag` from `getLibraryContent` and the
  `ETag` from `downloadLibraryFile` are **byte-identical**, and both equal
  `knowledge.ReadNoteVersion`'s `Token`. Measured over **3** files: a markdown note inside a
  knowledge base, a PDF inside one, and a file outside every knowledge base. **1** token
  computation exists in the tree, not 2.
- **SC-001e** *(new, revision 3)*: `getLibraryContent` returns an `ETag` on a **binary** file and on
  a **`too_large`** text file — the two cases where `library.ContentResult` omits `Content` by
  design. Measured: **2** of 2 return a header, and neither header equals the token of the empty
  string.
- **SC-001f** *(new, revision 3)*: A body sending the **quoted** form of the current token in
  `expect_version` is refused with **400**, and **0** such requests produce a 409. On the shared
  byte-stream helper's other callers, **0** responses gain an `ETag` and Range/`If-Range` behaviour
  is byte-for-byte unchanged.
- **SC-002**: The Library editor makes exactly **1** save request when refused, **1** after waiting
  the longest retry window in the application, and exactly **2** in total after the person presses
  retry.
- **SC-003**: For each of the 11 classifier kinds, a test asserts which renderer mounted — or
  asserts the link fallback for the three that must not mount one. **11 of 11** covered, with the
  fallback badge asserted absent on every mounting kind.
- **SC-004**: `npm run typecheck` exits 0 with the narrowed file-reference type in place, and an
  injected type error in one of the new files produces a non-zero exit with at least one `error TS`
  line (control run).
- **SC-005**: All **5** resolver states are produced from **evidence shapes the server can actually
  emit** and asserted independently; **0** of the indeterminate cases render the missing-file
  sentence. Every fixture reason is one the gateway's skip mapper can produce — **0** fixtures use a
  reason with no producer in the knowledge package.
- **SC-005a** *(widened, revision 3)*: For a target excluded during the folder walk, the reader
  renders the could-not-be-checked marker in **3** of 3 skip shapes — a skip naming the **file**, a
  skip naming the file's **basename**, and a skip naming an **ancestor directory** — and with the
  skip entry removed from each identical fixture it renders the missing-file marker in **3** of 3.
  Both halves in one test. **4** near-misses (a different file, a sibling directory, a
  string-but-not-segment prefix, a basename coinciding with a directory name) render the
  missing-file marker, i.e. **0** false suppressions. The ancestor case is the one revision 2's rule
  could not match, and it is the shape `WalkContained` produces most often.
- **SC-005e** *(new, revision 3)*: `knowledge_read`'s rendered links section reports a walk-skipped
  embed as **could not be checked, with the reason**, in **3** of 3 skip shapes, and as absent in
  **0** of them. An ordinary unresolved link in the same note still reads as absent in **1** of 1
  cases. Measured on the **agent** surface, not the reader's: US-2's narrative promises agents the
  same honesty, founder decision **D-B** requires it on both surfaces, and revision 2 delivered it
  on the reader alone.
- **SC-005b**: A containment refusal and an absent file produce **2 different** markers, and the
  containment marker contains **0** segments of the escaping path.
- **SC-005c** *(split across its two surfaces, revision 3)*: On the **server**, `heading_found` is
  `false` for a `.base` target and for a block reference in **2** of 2 cases, and `true` for a
  markdown target whose heading exists in **1** of 1 — the pair, so a hardcoded `false` fails. On the
  **reader**, the "no such heading" marker appears **0** times for those same two shapes, while
  appearing **1** time for a markdown target whose heading is genuinely missing **in the same
  fixture** — without that positive half the count of 0 is producible by a reader that never renders
  the marker, which is today's reader.
- **SC-005d**: Classifying the same file for the pane and for an embed agrees for **10 of 10** kinds
  with conventional extensions, and diverges in exactly **3** named extensionless cases, all
  asserted.
- **SC-006**: A note with 15 embeds and a failed graph request renders exactly **1** page-level
  error and **0** per-embed markers.
- **SC-007**: `make verify-contracts` exits 0 with the **three** new graph-edge properties, and each
  is asserted in **both** of its states by a handler-level test — proving the handler populates
  rather than hardcodes. Contract verification alone proves **0** of this.
- **SC-007a**: The contract-first work in this plan is **7** changes (CW-1…CW-7), each with a named
  "must land before" cell, and **0** code in a dependent story merges ahead of its contract.
- **SC-008**: All **75** existing view embeds are accounted for in the migration report before the
  dashboard change merges; every failure it names is either fixed or explicitly accepted by the
  founder in writing.
- **SC-009**: A knowledge base mounted **2** levels below the workspace root resolves its embeds
  correctly, with a same-named decoy nearer the root never requested (0 requests to the decoy).
- **SC-010**: 15 embeds over 5 data files produce exactly **5** view-list requests.
- ~~**SC-011**~~: ~~cycle and depth reporting~~ — **DELETED (N1).** Every state it measured is
  unreachable: with one level of transclusion there is no cycle to report and no depth to exceed.
  Replaced by **SC-011a**.
- **SC-011a**: In a note transcluding a note that itself contains **2** embeds, both inner embeds
  render as links with a reason, **0** could-not-be-checked markers appear, and **0** second-level
  content regions exist anywhere on the page. A note embedding itself renders its own text exactly
  **1** time.
- **SC-012**: Scrolling a 40-module note top to bottom in under 2 seconds never exceeds **4**
  evaluations in flight at any sampled instant, and all 40 finish in a rendered or errored state.
- **SC-013**: A failed module scrolled past 10 times makes exactly **1** evaluation request.
- ~~**SC-014**~~: ~~40 modules on the printout~~ — **DELETED (N3).** The guarantee is withdrawn.
  Replaced by **SC-014a**.
- **SC-014a**: On a 40-module note with 34 unmounted, the reader's notice is present **1** time and
  names **both** find-in-page and printing; with all 40 mounted it is present **0** times; on a note
  with no embeds it is present **0** times. All three measured in one test.
- **SC-015**: Before a play control is pressed, **0** network requests reach the video provider or
  any external host; after it is pressed, exactly **1** navigation to the provider occurs.
- **SC-016**: The served policy's frame-source directive lists exactly the configured allow-list;
  the anti-framing and picture-source directives are byte-for-byte identical to their pre-change
  values; the preview-isolation policy file is unchanged (0 lines).
- **SC-017**: The browser measurement's positive control still produces a violation, the
  allow-listed video journey produces **0** frame-source violations, and the non-allow-listed
  control produces exactly **1**.
- **SC-018**: With the video setting emptied, the served policy contains **0** external frame hosts.
- **SC-019**: A person's record-field write produces an audit record whose actor is the reserved
  person form, on real boot wiring; **0** records carry an empty or agent actor for a human edit.
- **SC-019a**: Under authentication bypass with no identifiable user, the write **succeeds** and
  records the actor `anonymous`. Over a fixture activity file containing agent, identified-person
  and anonymous rows, a plain text search for the anonymous token returns **exactly** the anonymous
  rows — **0** false positives. A request with neither a user nor bypass is refused with **0** bytes
  written to the file.
- **SC-019b**: In a fixture containing a writable enum, a derived property and a relation property
  in one view, **1** editor is offered and **2** are not. Neither half is measured alone.
- **SC-019c**: A write naming a derived property is refused; one naming a relation is refused; one
  naming an ordinary property on the same record succeeds. **3** cases, one test.
- **SC-020**: A record-field write refused with 409 produces exactly **1** request, still **1** after
  the longest retry window, and exactly **2** after a deliberate retry — the second carrying the
  fresh token.
- **SC-021**: A filter applied inside an embed leaves the shared view definition byte-for-byte
  unchanged (0 bytes differ) and leaves a second embed of the same view unchanged.
- **SC-022**: A field write on knowledge base A produces **0** refetches of any query belonging to
  knowledge base B.
- **SC-023**: The set of `knowledge_*` keys in the global permission ceiling is exactly the **8**
  live names before and after this work; **0** per-agent seed entries change; the reconciliation
  pass adds **0** entries.
- **SC-024**: `golangci-lint run --build-tags=goolm,stdjson --max-issues-per-linter=0
  --max-same-issues=0` reports **0** new findings attributable to this work. (Both flags are
  mandatory; without them the reported count has been wrong by 3–30×.)
- **SC-025**: `gofmt -l . | wc -l` is 0; `npx vitest run` exits 0; `scripts/check-vitest-coverage.mjs`
  exits **0** with every new test file present, **and the run's own verbose output names each new
  file by path**. A green job is not evidence a file ran: 116 of 422 files (27%) once never ran
  while CI reported green, because the vitest matrix is a hardcoded pattern list in
  `.github/workflows/pr.yml` and a file outside it is silently skipped.
- **SC-026** *(now machine-checkable, revision 3)*: Every numbered test in this plan appears in
  **exactly one** row of the register's **Register coverage table**. **Measured by the `diff`
  command printed under Invariant 2**, not by reading: extract the number column of the two order
  tables and the number column of the coverage table, and diff them. A non-empty diff is the
  failure; duplicates on either side are checked separately with `uniq -d` on the unsorted
  extraction. **Re-measured on revision 3: 119 numbered rows, 119 coverage rows, diff empty, 0
  duplicates on either side.** Revision 2 asserted this criterion as met with **≥36** tests outside,
  because "counting" meant scanning prose for names, bare numbers and ranges — and a range like
  *"tests 9–38"* covers a test without ever giving it a row.
- **SC-027** *(the single authoritative pairing list, revision 3)*: Every negative assertion in this
  plan is paired, in the same test body, with a positive one, **and the pairing is non-vacuous** —
  the paired positive must be something that does **not** already hold on the pre-change tree.
  Measured: **0** unpaired and **0** vacuous negative assertions across tests **11, 14, 17, 21, 22,
  44, 57, 62, 74, 76, 89, 95, 102, 104, 108, 115, 120, 122, 123, 124, 125**. Revision 2 carried
  **three** lists of this set (Invariant 1's prose: seven; the step-1 table: six; SC-027: ten) and
  they were not the same list; this one is now the only one, and the other two reference it. Three
  of revision 2's fourteen named pairings were **vacuous** — 21 and 22 (both halves already held)
  and 110 (no pairing named at all) — and all three are repaired.
- **SC-028** *(exclusion list rebuilt from the actual sweep, revision 3)*: Every new test was run
  **before** its implementation and **failed**. Measured: the count of new tests that passed on that
  first run is **0**, excluding **exactly** the rows labelled **PIN** in the Register coverage
  table: **20, 28, 29, 30, 38, 61, 67, 68, 90, 105, 107, and 119's clean-state case** — **twelve**,
  not the two revision 2 named. Revision 2's list was unmeetable as written: the step-4 X7 check
  itself excluded test 68, the Regression table requires extensions that assert behaviour holding
  today, and eight further tests could not fail at all. **A criterion that cannot be met is reported
  as met with a caveat, which is the worst of both.** Two coupling rules keep the lists from
  drifting: **(i)** the PIN set in the coverage table and this enumeration MUST be identical, and
  **(ii)** every excluded row MUST carry the words *"pin of existing behaviour"* or **PIN** in the
  order table. Any test **not** on this list that passes on the first run is reported as a finding,
  not as progress.
- **SC-029**: `scripts/check-no-duplicate-renderer.test.sh` exits non-zero against a tree with a
  planted duplicate and zero against a clean one. **Both** directions measured; a guard proven only
  in the clean state proves it executes, not that it detects.
- **SC-030** *(new, revision 3)*: Every `Traces to:` line in the BDD section **resolves to an
  acceptance scenario that exists**, and every acceptance scenario in every user story is the target
  of at least one of them. Measured: **83** scenarios, **83** traces, **0** dangling, **0**
  uncovered. Revision 2 counted scenarios and counted traces and **never checked that a trace
  resolved** — which left one pointing at US-8 AS-7, a scenario the print deletion had removed, and
  five more off by one after an insertion, and one acceptance scenario (US-11 AS-6) with a test and
  two dataset rows and no scenario at all.
- **SC-031** *(new, revision 3 — stated at the scope it can actually be measured at)*: **No live
  requirement, numbered test row, dataset row, success criterion, or Test Hierarchy row specifies a
  cycle set, a depth cap, or a print guarantee.** Measured: **0** in each of the five scopes.
  `cycle` and `depth cap` survive only inside the struck-through EMB-056/057/058 and SC-011 entries
  and the N1 narrative explaining the deletion. `print` survives in two senses the deletion never
  touched: **EMB-071's on-screen statement**, which names find-in-page *and printing* as what will
  not reach unmounted embeds — N3's **replacement** for the guarantee, not a residue of it — and
  `prints` as an ordinary verb about program output. A document-wide string ban would forbid both,
  which is why this criterion is scoped. Re-measure per scope; each must return nothing:
  `awk '/^## Functional Requirements$/,/^## Success Criteria$/' "$S" | grep -E '^- \*\*EMB' | grep -iE 'cycle|depth cap'`;
  `awk '/^### Test Implementation Order$/,/^### Test Datasets$/' "$S" | grep -E '^\| \*{0,2}[0-9]+' | grep -iE 'cycle|depth cap'`;
  `awk '/^### Test Hierarchy$/,/^### Test Implementation Order$/' "$S" | grep -iE 'cycle|depth cap|print'`.
  Three references to deleted features survived revision 2's own deletions, **two of them in the
  Test Hierarchy** — the first three rows an implementer reads before writing a test, instructing
  work that had been deleted three sections below.

---

## Test Implementation Order — tests 91–96, now folded into their phases

> **Revision 1 appended these six after the false-green register was written, and none of them
> appeared in any phase's risk table.** That broke the register's own completeness claim, and two of
> them are exactly the shapes the register exists to catch. They are listed here for continuity of
> numbering, but **each now has a row in its phase's register table**, like every other test, and
> the register carries a completeness assertion so this cannot recur.

| Order | Test Name | Level | Phase | Traces to BDD Scenario | Description |
|---|---|---|---|---|---|
| 91 | `src/components/library/knowledge/embed/embedResolver.test.ts::outline-fetched-only-when-heading-missing` | Unit | Step 1 | An embed naming a real note but a heading that does not exist | The outline request count is **0** on every path **and exactly 1 on the missing-heading path**. **The `1` is the load-bearing half** — the `0` half passes with the outline fetch never implemented — and it is now stated first rather than in passing |
| 92 | `TestKnowledgeRead_MarksEmbedsAndViewLabels` | Unit | Step 1 | An agent reading a dashboard can tell embeds from links | Embeds marked; a data view rendered as a view, not a heading; ordinary links unmarked |
| 93 | `TestKnowledgeRead_UnresolvedEmbedCarriesReason` | Unit | Step 1c | An agent reading a dashboard can tell embeds from links | A broken embed is marked broken **with its reason** — including *outside the knowledge base* for a containment refusal — asserted against the real renderer output, never against the sample in a document |
| 94 | `src/components/library/preview/viewparts/ViewCellLink.embed.test.tsx::resolves-against-the-readers-graph` | Integration | Step 2 | A note embedding a named view shows that view's rows | **The fixture is the whole test.** A link **absent from the loaded rows** but present in the reader's graph must render as resolved; clicking opens in the reader. If the fixture's link is present in the rows, both resolvers agree and the test proves nothing |
| 95 | `src/components/library/preview/BasePreview.inline.test.tsx::states-link-rendering-differs-from-the-pane` | Integration | Step 2 | A note embedding a named view shows that view's rows | Asserts a UI string exists — **trivially satisfiable, and trivially satisfiable wrongly.** Its subject is a sentence, not a behaviour. **Paired:** the same test mounts the same view in the pane and asserts the statement is **absent** there, so a component that renders the sentence unconditionally fails |
| 96 | `src/components/library/knowledge/embed/embedLazyMount.test.tsx::reserved-height-per-kind` | Integration | Cross-cutting | Only modules near the viewport begin work | Per-kind reserved heights; a transclusion reserves three lines and accepts one reflow |

---

## Traceability Matrix

> Rebuilt in revision 2, extended in revision 3. **90 live requirements, 90 rows, exact set
> equality, 0 duplicates — re-measured, with the commands in the completeness table below.** Five
> requirements were deleted (EMB-056, EMB-057, EMB-058, EMB-070, EMB-091); their numbers are retired
> rather than reused, and they appear in the requirements section as struck-through entries with the
> reason, so a reader of the review can follow each one. Test numbers 53–56, 73 and 88 are likewise
> retired. Revision 3 adds five requirements — **EMB-006a** (the collection-root derivation the lock
> needs and nothing provides), **EMB-007a** (the token's value), **EMB-007b** (its wire shape and
> where the header lands), **EMB-007c** (how a response header reaches SPA code at all), and
> **EMB-021a** (the agent half of the honesty guarantee) — and seven tests: 120–125, plus the
> narrowing of 110.

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| EMB-001 | US-1 | Save sent with no version token at all is refused | 1 |
| EMB-002 | US-1 | Person's save is refused because an agent wrote to the note first; A binary save goes through the same guard | 2, 4 |
| EMB-003 | US-1 | Person saves a note nobody else has touched; A binary save goes through the same guard | 5, 6 |
| EMB-004 | US-1 | Refused save shows the person what happened; A refused save is never resent by the system | 7, 8 |
| EMB-005 | US-1 | Person saves a note nobody else has touched | 1, 2, 3 (all depend on CW-1 landing first) |
| **EMB-006** | US-1 | A version check and its write cannot be interleaved by an agent | 99, 100 |
| **EMB-006a** | US-1 | A version check and its write cannot be interleaved by an agent | 100, 124 |
| **EMB-007** | US-1 | The reader that opens a binary file is given a version marker to send back | 97, 98, 101 |
| **EMB-007a** | US-1 | The reader that opens a binary file is given a version marker to send back | 97, 121 |
| **EMB-007b** | US-1 | Save sent with no version token at all is refused; The reader that opens a binary file is given a version marker to send back | 123, 98, plus the Range regression row |
| **EMB-007c** | US-1 | Person saves a note nobody else has touched; The reader that opens a binary file is given a version marker to send back | 7, 101 |
| EMB-010 | US-2, US-9 | Outline: only a valid identifier on an allow-listed host is framed | 11 |
| EMB-011 | US-2, US-5 | Two embeds of one data file under different view names each show their own view | 12, 46 |
| EMB-012 | US-2 | Outline: every honesty signal the server can actually emit; An embed whose evidence has not arrived; A failed evidence request | 13, 23, 24 |
| EMB-013 | US-2 | Outline: every honesty signal the server can actually emit | 13, 14 |
| EMB-014 | US-2 | A failed evidence request produces one page-level error; A knowledge base that answers with nothing produces one statement | 23, 106 |
| EMB-015 | US-2 | An embed whose evidence has not arrived shows reserved space | 24 |
| EMB-016 | US-2 | An embed naming a file that does not exist; …a view that does not exist; …a heading that does not exist | 13, 33, 48, 91 |
| EMB-017 | US-2 | A containment refusal says something different from a missing file | 17 |
| EMB-018 | US-2 | A name matching more than one file resolves and reports the ambiguity anyway | 18 |
| EMB-019 | US-2 | Outline: every honesty signal the server can actually emit | 15 |
| EMB-020 | US-2 | An agent reading a dashboard can tell embeds from links, and broken from working | 92, 93 |
| **EMB-021** | US-2 | A target the indexer refused to read is "could not be checked"; A file the indexer could not read still resolves | 102, 103, 105 |
| **EMB-021a** | US-2 | A target the indexer refused to read is "could not be checked"; An agent reading a dashboard can tell embeds from links, and broken from working | 120 |
| **EMB-022** | US-2 | Outline: every honesty signal the server can actually emit | 104 |
| **EMB-023** | US-2, US-4 | A containment refusal says something different from a missing file | 111, 17 |
| **EMB-024** | US-2 | An agent reading a dashboard can tell embeds from links, and broken from working | 93 |
| EMB-025 | US-3 | Outline: each embeddable kind mounts its own renderer; Outline: kinds with no inline treatment | 19, 20 |
| EMB-026 | US-3 | Outline: kinds with no inline treatment keep today's link-with-a-badge | 21 |
| EMB-027 | US-3 | The same picture component draws the pane and the note; The same file is classified the same way inline and in the pane | 25, 26, 27, 109 |
| EMB-028 | US-3 | The same picture component draws the pane and the note | 25, 26, 27 |
| EMB-029 | US-3, US-5 | A knowledge base mounted below the workspace root resolves correctly | 16 |
| EMB-030 | US-3, US-11 | A picture with a width given after a bar; Outline: modifiers are checked against the target's kind | 9, 10, 35 |
| EMB-031 | US-3 | An SVG embed is drawn as a picture and runs none of its scripts | 107 |
| EMB-032 | US-3 | Two PDFs on one page both render; A third PDF beyond the worker ceiling waits visibly | 28, 29, 30, 31 |
| EMB-033 | US-3 | An embed inside a code fence draws nothing | 22 |
| **EMB-034** | US-3 | The same file is classified the same way inline and in the pane, except where it provably cannot be | 109 |
| EMB-035 | US-4 | A link to a heading that exists / does not exist | 39 |
| EMB-036 | US-4 | A link to an anchored block carries the anchor separately | 40 |
| EMB-037 | US-4 | The contract and the generated artefacts land together | 41 |
| EMB-038 | US-2, US-4 | An embed naming a real note but a heading that does not exist | 91 |
| **EMB-039** | US-2, US-4 | An embed naming a real note but a heading that does not exist | 110 (server), 122 (reader) |
| EMB-040 | US-5 | Outline: a view fragment matches a display label; A note embedding a named view shows that view's rows | 42, 44, 45 |
| EMB-041 | US-5 | Outline: a view fragment matches a display label | 42 |
| EMB-042 | US-5 | Two views sharing a display label are refused rather than guessed | 43 |
| EMB-043 | US-5 | A data file embedded without naming a view shows the first available one | 47 |
| EMB-044 | US-5 | A view the server cannot serve shows the server's own reason | 48 |
| EMB-045 | US-5 | Fifteen embeds over five data files make five view-list requests | 50 |
| EMB-046 | US-5 | An embedded view's controls appear on hover and on keyboard focus | 49 |
| EMB-047 | US-10 | A view filter set inside an embed is local, temporary, and invisible elsewhere | 78 |
| EMB-048 | US-5, US-10 | A note embedding a named view shows that view's rows | 94 |
| EMB-049 | US-5 | A note embedding a named view shows that view's rows | 95 |
| EMB-055 | US-7 | A note shown inside another note appears in place; Outline: a section or a block shows only that part | 52, 58 |
| EMB-059 | US-7 | A note shown inside another note appears in place | 58 |
| EMB-060 | US-7 | Embeds inside a transcluded note render as links with a reason | 57 |
| **EMB-061** | US-7 | A note that shows itself renders once and stops, with nothing counting | 112 |
| **EMB-062** | US-7 | A note that shows an empty note says the note is empty | 113 |
| EMB-065 | US-8 | Only modules near the viewport begin work | 83 |
| EMB-066 | US-8 | Only modules near the viewport begin work | 96 |
| EMB-067 | US-8 | Fast scrolling never exceeds four evaluations; A module scrolled out of view before its turn | 84, 85 |
| EMB-068 | US-8 | A failed module is not retried by scrolling past it | 86 |
| EMB-069 | US-8 | Returning to the application does not re-evaluate every module | 87 |
| EMB-071 | US-8 | The reader states what unmounted modules cannot do, and says nothing when they can | 89 |
| EMB-075 | US-9 | Outline: only a valid identifier on an allow-listed host; Hand-written frame markup in a note stays inert | 63, 108 |
| EMB-076 | US-9 | Outline: only a valid identifier on an allow-listed host is framed | 63 |
| EMB-077 | US-9 | An allow-listed video shows a locally drawn placeholder and contacts nobody | 64, 69 |
| EMB-078 | US-9 | Pressing play loads the player, once | 65 |
| EMB-079 | US-9 | The served policy gains exactly one frame host and loses nothing | 60, 61, 114 |
| EMB-080 | US-9 | The served policy gains exactly one frame host and loses nothing | 59 |
| EMB-081 | US-9 | An operator can decline the external host | 62 |
| EMB-082 | US-9 | The browser measurement is re-run with a video | 66, 67, 68 |
| EMB-085 | US-10 | Changing a status from a dashboard changes the record everywhere | 70 |
| EMB-086 | US-10 | A record changed by someone else refuses the edit and restores the stored value | 71 |
| EMB-087 | US-10 | A record changed by someone else refuses the edit; A refused edit is never resent automatically | 71, 77 |
| EMB-088 | US-10 | Outline: an editor is offered only for field types described; A write naming a calculated field or a link is refused | 75, 115 |
| EMB-089 | US-10 | A record's name and location are never editable here | 76 |
| EMB-090 | US-10 | A person's edit is attributed to a person, not an agent | 72 |
| EMB-092 | US-10 | A successful field write refreshes only what went stale; Changing a status from a dashboard changes the record everywhere | 79, 118 |
| EMB-093 | US-10 | An edit that nobody can be identified for is saved, and recorded as exactly that | 74, 117 |
| **EMB-094** | US-10 | Outline: an editor is offered only for field types the record's definition describes | 75 (blocked on CW-4 and CW-5) |
| EMB-095 | US-11 | An agent asks for an embed and the correct notation is written for it | 32 |
| EMB-096 | US-11 | The permission surface does not change at all | 38 |
| EMB-097 | US-11 | An agent asks for an embed and the correct notation is written for it | 32 |
| EMB-098 | US-11 | An agent naming a view that does not exist; An agent naming a target outside the knowledge base | 33, 34 |
| EMB-099 | US-11 | Outline: modifiers are checked against the target's kind | 35 |
| EMB-100 | US-11 | An argument this operation does not read is refused for this operation | 36 |
| EMB-101 | US-11 | An argument this operation does not read is refused for this operation | 36 |
| EMB-102 | US-11 | **An agent that omits the version token is refused** | 37 |
| EMB-105 | US-12 | Outline: sound and video play in place; The extracted sound component exists exactly once; A PDF page fragment shows that page | 80, 81, 82, 119 |
| EMB-106 | US-3, US-12 | Outline: kinds with no inline treatment keep today's link-with-a-badge; Outline: each embeddable kind mounts its own renderer | 90, 125 |
| **Regression** | US-1, US-3, US-5 | Outline: kinds with no inline treatment; Outline: each embeddable kind mounts its own renderer | 90, 101, 125, and the regression table above |
| **US-6** (report) | US-6 | The report lists every existing view embed; The report names an embed whose label no longer exists; The report is produced before dashboards ship | 51, plus SC-008 as a merge condition |

**Completeness check — RE-MEASURED in revision 3, with the command for each figure.**

> **Why every row now carries a command.** Revision 2 headed this table *"counted, not asserted"*
> and then asserted the one row nobody could recompute: *"Live numbered tests: **106**"*, derived by
> taking four ranges that **already exclude** the six retired numbers and subtracting the six
> retired numbers again. The true figure was **113**. **A count that is wrong because it was derived
> rather than measured is the same class of defect as a test that cannot fail** — both are claims
> whose subject was never examined. So the rule for this table is now: *if you cannot print the
> command that produced the number, the number does not go in the table.*
>
> Set `S=docs/internal/specs/adr-083-embedded-content-spec.md` before running any of these.

| Property | Count | The command that produced it |
|---|---|---|
| Live functional requirements | **90** | `awk '/^## Functional Requirements$/,/^## Success Criteria$/' "$S" \| grep -oE '^- \*\*EMB-[0-9]+[a-z]?\*\*' \| sort -u \| wc -l` |
| Struck-through (retired) requirements | **5** | EMB-056, EMB-057, EMB-058, EMB-070, EMB-091 — numbers retired, never reused |
| Requirement rows in this matrix | **90** | `awk '/^## Traceability Matrix$/,/^## Ambiguity Warnings$/' "$S" \| grep -oE '^\| \*{0,2}EMB-[0-9]+[a-z]?\*{0,2} \|' \| sort -u \| wc -l`, plus 2 non-requirement rows (regression, the report) |
| Matrix ↔ requirements set equality | **exact, 0 duplicates** | `diff` of the two extractions above; `uniq -d` on the unsorted matrix extraction |
| BDD scenarios | **83** | `grep -c '^#### Scenario' "$S"` |
| `Traces to:` back-references | **83** | `grep -c '^\*\*Traces to\*\*' "$S"` — equal to the scenario count, so no scenario is unattributed |
| `Traces to:` that **resolve** to an acceptance scenario that exists | **83 of 83** | The SC-030 check: for each trace, compare its `AS-n` against the count of `^n. **Given**` lines in that user story's block. **Revision 2 counted scenarios and counted traces and never checked that a trace resolved** — leaving one pointing at US-8 AS-7, deleted by N3, and five more off by one |
| Acceptance scenarios with ≥1 BDD scenario | **80 of 80** | Same check, other direction. Revision 2 had **6** uncovered, one of them (US-11 AS-6) with a test and two dataset rows and no scenario at all |
| **Live numbered test rows** | **119** | `{ awk '/^### Test Implementation Order$/,/^### Test Datasets$/' "$S"; awk '/^## Test Implementation Order — tests 91/,/^## Traceability Matrix$/' "$S"; } \| grep -oE '^\| \*{0,2}[0-9]+\*{0,2} \|' \| grep -oE '[0-9]+' \| sort -n -u \| wc -l` |
| Of which are **tests** | **118** | The same, minus row **116**, which is explicitly *(not a test)* — a task |
| Retired test numbers | **6** | 53, 54, 55, 56, 73, 88 — struck-through rows, numbers never reused, so every `Traces to` above and below stays valid |
| Gaps or duplicates in 1…125 | **0 and 0** | `comm -23 <(seq 1 125 \| sort) <(sort /tmp/tests)` returns exactly the six retired numbers; `uniq -d` on the unsorted extraction is empty |
| Numbered tests in exactly one register coverage row | **119 of 119** | The SC-026 `diff` command printed under Invariant 2. **Re-measured: diff empty** |
| Tests cited by at least one matrix row | **all** | Except the regression extensions, cited by the regression table instead — deliberately, because a regression protects behaviour that has no new requirement |

**What changed here, and why the counts moved.** In revision 2, five requirements and six tests
were deleted and eleven requirements and twenty-three tests were added; the deletions were the
substantive half (three cycle requirements whose input the product cannot construct, one print
requirement the platform cannot honour, and one requirement whose work was already finished before
the spec was written). **Revision 3 adds five requirements and six tests and deletes none.** Its
substantive half is different in kind: it is not new capability but the **execution of three checks
this document already specified and had never run** — the X7 sweep, the register's completeness
invariant, and the resolution of every `Traces to`. Each of the three had been stated twice and kept
zero times.

## Ambiguity Warnings

> The founder has ratified D-A, D-B, D-C, D-D, Q1–Q9 **and N1–N4**. **None of those is re-opened
> here.** Revision 2 marks each of revision 1's ten rows with its disposition, and adds the gaps
> found while resolving the review.

### Resolved by founder ruling or by measurement — no longer open

| # | What was ambiguous | How it is settled now |
|---|---|---|
| **A-1** | Whether the version token on the two whole-file save endpoints is required or optional | **RESOLVED — required (N2).** A save without one is rejected with 400. Revision 1's supporting argument was wrong on a fact: it said *"the only caller is our own editor, so the breaking change costs one line there."* The **binary** door's only production caller is the PDF annotation save, which loads raw bytes and has **no read on its path that returns a token**. Requiring the token without EMB-007 breaks a shipped feature with an HTTP 400. EMB-007 changes the read side instead of exempting the caller. |
| **A-2** | Which typed conflict body the two Library endpoints return | **RESOLVED — a Library-specific one.** Labelling a conflict on a spreadsheet as a *knowledge* version conflict is a wrong label in a place a caller branches on. Note additionally that the knowledge conflict body is referenced from **no path** today, so US-10 is its first wiring — a fact revision 1 did not have. |
| **A-3** | How the reader learns the video allow-list | **RESOLVED — it crosses the wire, as CW-3.** Revision 1 stated this as *"a fourth may be required"* inside a section headed "there are three". It is not conditional: Q9 makes the host an operator configuration key, EMB-080 requires the reader's list to equal the served policy's, and a build constant cannot carry a value an operator changed. **The collision with the byte-for-byte policy oracle is also resolved** (see the register's step-4 rows): the oracle document keeps the *shipped default*, and a second assertion in the same test proves an emptied install differs from it. |
| **A-4** | Whether "toolbar on hover/focus" has a touch path | **RESOLVED — reveal on hover, on focus-within, and on a tap of the embed's header strip.** Now an assertion inside test 49 rather than a row in this table. |
| **A-5** | Whether cell editors are hover-gated the way the toolbar is | **RESOLVED — editors are content, not chrome.** A toolbar is hover/focus-gated; an editable cell stays always active and always keyboard-reachable, showing its affordance on hover of the **cell**. **Revision 2's row claimed "this is now stated in EMB-046 and EMB-088" and it was stated in NEITHER** — EMB-046 still said controls hide at rest with no exception, and EMB-088 said nothing about hover-gating. That is the same failure class as X7, applied to a requirement instead of a test: *a claim whose subject does not exist*. **Fixed for real in revision 3:** EMB-046 now carries the carve-out verbatim ("controls" means the view's toolbar chrome; an editable cell is content, always active, always keyboard-reachable), EMB-088 carries the pointing clause, and **test 49 asserts a cell editor is reachable while the toolbar is hidden** — without that assertion nothing in the plan would catch the mistake. |
| **A-6** | Which identifier the person actor uses, and what happens under authentication bypass | **RESOLVED, and the second half was OVERRULED.** The identifier is the **stable authentication user id** — never a display name (a rename breaks the log's continuity), never an email (personal data in a file an operator may share). Under bypass with no user, revision 1 recommended **HTTP 503**; **founder ruling N4 overrules it**: the write is **accepted** and recorded with the actor `anonymous`. See EMB-093 for the two obligations that follow. |
| **A-7** | Whether an agent can be in the "could not check" state | **RESOLVED by measurement, and it is smaller than it looked.** The agent surface has **two** states, and the honesty is carried by the reason rather than by a third marker — but the reason field on the wire could not distinguish "no match" from "outside the root", which is CW-2's third field. With that field, EMB-024 gives the agent surface the same reasons the reader gets. |
| **A-8** | What the pre-flight report is | **RESOLVED — a script under `scripts/`, plus one integration test over a seeded fixture.** No new product surface for a one-off question. **One caveat revision 1 raised and did not settle:** the report enumerates note paths and view labels from a private knowledge base, so **its output is reviewed and not committed**; only the script and its fixture-based test are. |
| **A-9** | Whether the print guarantee is achievable | **RESOLVED — the guarantee is DROPPED (N3).** Revision 1 recommended narrowing it to the application's own print control. The founder dropped it entirely: that control is not built in this work either, and a guarantee scoped to a path nobody ships is still a guarantee nobody can rely on. Printing reaches what has mounted; EMB-071 says so on screen. |
| **A-11** | **Whether the Library's version token is the same value the knowledge path computes.** | **RESOLVED — founder ruling: ONE token, and it is the EXISTING one.** Both doors use `pkg/knowledge/version.go`'s `ComputeVersionToken` / `ReadNoteVersion` — named here by symbol, because neither document named either function in revision 2 (`grep -c` returned **0** in both), leaving a step-0 implementer with no instruction pointing at them and a strong pull toward size + modification time, which `version.go`'s own header spends four paragraphs refusing. **No back-compat is owed:** `rest_library.go` contains **zero** token code today, so there is no second definition to migrate. The value, the wire shape, the binary/oversized gap and the outside-a-collection case are specified in **EMB-007a** and **EMB-007b**; the derivation the lock needs is **EMB-006a**. This was revision 2's only open ambiguity that *"changes step 0's work"*, and leaving it open with a recommendation invited an implementer to decide it a second time. |
| **A-10** | Where a video's title comes from | **RESOLVED — never fetched.** The only title shown is the text the author typed in the markdown link. Fetching a title is a contact with the provider, which is the whole thing click-to-play exists to prevent. |

### Still open — raised while resolving the review

| # | What is ambiguous | Likely agent assumption | Question to resolve | Recommendation |
|---|---|---|---|---|
| **A-12** | **What an embed of a knowledge base's own control-plane file does** — a records or views directory entry, or a data file the importer marked unservable at *import* time rather than at *evaluation* time. EMB-044 covers evaluation-time unservability only. | An agent will let it fall through to the ordinary kind dispatch and render a control-plane file as content. | Is a control-plane file embeddable at all? | **No** — it falls back to the link treatment with a stated reason. And an import-time failure is a **different sentence** from an evaluation-time one: "this file did not import" is not "this view could not be evaluated", and dataset D8 currently assumes the first while describing the second. |
| **A-13** | **Whether the `[embed]` marker changes the token cost of every dashboard read.** The marker is free for a note with no embeds, which revision 1 stated; it says nothing about the note with fifteen, or the founder's with seventy-five. | An agent will ship it unmeasured, because the no-embed case is obviously free. | What does the marker cost on the real cockpit note? | **Measure it before step 1 merges** — one read of the real note, before and after, tokens counted. It is one command, and the answer is either "negligible" or "this needs a compact form", which are very different plans. |
| **A-14** | **When the reader learns that the operator emptied the video allow-list mid-session.** EMB-081 says the embed renders as a link when the setting is emptied, without saying when the reader finds out. | An agent will read the value once at boot and never again, so an already-open page keeps offering a play control that the served policy now blocks — a control that does nothing, which this document forbids elsewhere. | Boot-time value, or re-read? | **Re-read on the same schedule as the rest of the settings payload**, and treat the change as it would any other settings change. A play control that fails silently is precisely the undetectable-fallback shape this project removed from the live browser view. |

## Evaluation Scenarios (Holdout)

> **These are for post-implementation evaluation only.** They must NOT be visible to an implementing
> agent during development, must NOT be turned into tests in the plan above, and are deliberately
> **excluded from the traceability matrix**. They are written from the outside — what a person
> observes — and are chosen so that reading them gives no shortcut to passing them. Ten scenarios:
> four happy path, three error, three edge case.

### H-1: The real dashboard, on real data
- **Setup**: the founder's own knowledge base, synced, with `Founder Cockpit.md` untouched.
- **Action**: open it and read it top to bottom without scrolling back.
- **Expected outcome**: every module shows either its own live rows or a marker that names what is
  wrong with it. No module is blank. No module shows a sentence about a file that can be opened in
  the next pane.
- **Category**: Happy Path

### H-2: An agent summarises a partly broken dashboard
- **Setup**: a dashboard with ten modules, three of which name views that no longer exist.
- **Action**: ask an agent, in ordinary language, "what is on my cockpit page, and is any of it
  broken?"
- **Expected outcome**: the agent reports the seven working modules and names the three broken ones.
  It does not quietly summarise seven and call it the page.
- **Category**: Happy Path

### H-3: A picture, a PDF and a view in one note
- **Setup**: a new note with one photograph sized to 400, one two-page PDF, and one named view.
- **Action**: read it.
- **Expected outcome**: all three appear in place at once. The photograph is at the requested width.
  The page does not visibly jump as they arrive.
- **Category**: Happy Path

### H-4: The same content, two ways
- **Setup**: a data view and a PDF, each open in the full-screen preview and embedded in a note.
- **Action**: compare the two.
- **Expected outcome**: the same information, the same states, the same wording for anything that
  went wrong. Only the size and the surrounding chrome differ.
- **Category**: Happy Path

### H-5: Two people, one record
- **Setup**: the same record open in two browser windows, in an embedded view.
- **Action**: change the status in the first window; then, without reloading, change it in the
  second.
- **Expected outcome**: the second is refused with a plain explanation, the field shows the first
  window's value, and a retry is offered. Waiting several minutes changes nothing — the second
  change is never applied on its own.
- **Category**: Error

### H-6: The knowledge base moves
- **Setup**: a knowledge base containing working view embeds, sitting at the top of the workspace.
- **Action**: move it two folders down and reload the note.
- **Expected outcome**: the embeds still resolve to the same views. Nothing resolves to a
  same-named file elsewhere in the workspace.
- **Category**: Error

### H-7: The network goes away mid-read
- **Setup**: a dashboard with fifteen modules.
- **Action**: open it, then disconnect the network before the modules finish, then reconnect and
  press retry.
- **Expected outcome**: one clear error, not fifteen. No grey placeholder sits there forever. After
  retry, the page fills in.
- **Category**: Error

### H-8: Two notes that point at each other
- **Setup**: two notes, each embedding the other, written by hand.
- **Action**: open either one.
- **Expected outcome**: it renders promptly and stays responsive. The processor does not spin. You
  see the other note's contents, and inside them, the embed pointing back at you appears as a
  **link with a stated reason** — not as content, not as an error, and not as a bare filename with
  no explanation.
- **Category**: Edge Case

### H-9: Printing a long dashboard without scrolling it
- **Setup**: a forty-module dashboard, freshly opened, scrolled only to the sixth module.
- **Action**: read what the page tells you about printing, then print it.
- **Expected outcome**: the page told you, before you printed, that modules you have not scrolled to
  will not be on the printout — and the printout matches what it told you. Nothing claimed
  completeness. The failure this scenario is looking for is a silent partial page, not a partial
  page.
- **Category**: Edge Case

### H-10: A sovereign install that wants no third parties
- **Setup**: a fresh installation with the video host setting emptied, and a note containing an
  allow-listed video.
- **Action**: open the note; watch the network from the moment the page loads.
- **Expected outcome**: no request reaches any external host at any point. The video appears as a
  link. Nothing offers a play control that does nothing.
- **Category**: Edge Case

---

## Assumptions

- The knowledge base remains file-based (JSON/JSONL under `~/.omnipus/`), with no database and no
  cache server. Nothing in this work introduces one.
- The single-binary and no-CGo constraints hold. Nothing here adds a runtime dependency; the PDF
  worker and the video frame are browser-side.
- The reader is the SPA served by the gateway from the embedded bundle. The Vite development server
  is a convenience, not the surface under test — a change is not proven until it is in
  `pkg/gateway/spa/`.
- **Windows has no cross-process file locking anywhere in the file-store family.** A concurrent
  human edit and agent write from two processes against the same home directory is protected
  in-process only. This is the pre-existing position and this work does not change it — but note
  that step 0 and step 5 make concurrent writes *more likely*, so the pre-existing gap becomes
  easier to hit. It is a residual, recorded, not closed here.
- No per-user or per-role authorisation exists on any library or knowledge route today. A view
  therefore evaluates identically whoever opens the note. If per-user authorisation is added later,
  whose permissions a view evaluates under becomes that work's question, not this one's.
- The graph's truncation flag is never set for the query kind this feature uses on the current
  branch. The reader honours it anyway. If a future change adds a bound to that branch without a
  reader that honours it, the false-statement defect returns.
- **Three Library doors stay unversioned after step 0**, and the "one compare-and-swap from every
  surface" claim is scoped accordingly rather than repeated unqualified. Measured: `rest_library.go`
  has eight mutating routes; step 0 puts a version check on **two** of them. `deleteLibraryEntry`
  and `uploadLibraryFiles` can destroy or replace a knowledge note an agent is mid-write on, with no
  conflict check; the rename/move/copy transfer modes at least refuse an existing destination with
  409, so losing data there requires the *source* to be mid-write. **All four are audited**, so the
  loss is traceable after the fact — it is simply not preventable. Closing them is separate, sized
  work, not something this spec leaves implied.
  **One of the three is worse than "unversioned", and revision 3 states it rather than letting the
  shared word cover it (O2).** `uploadLibraryFiles` replacing an existing note takes **no lock
  either** — not merely no *conflict check*. So after step 0 an upload can still interleave with an
  agent's `EditNote` and **lose the agent's write**: a lost update, not an unrecorded one. That
  distinction is exactly the argument step 0 rests on — EMB-006's *"a comparison followed by an
  unheld write is not a compare-and-swap"* — so applying the weaker word to this door understates
  it by the same amount step 0 exists to correct. `deleteLibraryEntry` and the transfer modes are
  destructive-but-ordered; the upload path is genuinely racy.
- **Printing is incomplete and stays incomplete (N3).** A dashboard prints what has mounted. The
  reader says so; nothing in this work changes it. Making printing complete is unscheduled.
- **Under authentication bypass a record edit is recorded as `anonymous` (N4).** That is a real,
  accepted reduction in attribution: such an entry cannot answer "who". It is distinguishable from
  an identified entry by a plain text search, and must never be read as one.
- **Nesting is out, not deferred (N1).** A transcluded note's own embeds are links. If nesting is
  ever wanted, ADR-083 D5 records the four things that must exist first — a graph query per
  transcluded note, a place for those queries in the concurrency budget, a per-level reserved-height
  story, and only then a cycle set.
- "Ship steps 0–5 and stop" is a legitimate outcome. US-12 has zero measured uses across 784 notes.
- Nothing in the source ADR was executed — no build, no test run, no browser check. Two commands
  were named there as pre-ratification obligations and remain outstanding: the type check against
  the narrowed file-reference type, and the pre-flight report over the existing embeds.
- **Every code fact newly asserted in revision 2 of this spec was read on `def10b90e`; none was
  executed.** That is the same standing that produced seven false "measured" claims across the ADR's
  two revisions, so it is stated rather than glossed. What is different here is that each claim
  names the symbol it came from (`file::symbol`, never `file:line`), so a reader can check any of
  them in one command. Anything not read is marked `[UNVERIFIED]` at the point of use.
- CI is the authority for Go test and build results. The full Go suite must not be run in the
  development environment; a single narrowly-scoped test with the build tags is the local limit.

---

## Clarifications

### 2026-09-09 — settled before this spec was written (founder-ratified, ADR §2.6 and §9)

- Q: How many embeds may one note have? → A: **No hard cap.** They mount lazily on scroll; the bound
  is on work, not on count.
- Q: What does a missing embed look like? → A: **A visible marker**, never an empty box — and the
  unresolved state is reported to agents too.
- Q: What external content is allowed? → A: **Allow-listed video only.** No other external video, no
  external pictures — therefore no provider thumbnail, and the click-to-play placeholder is drawn
  locally. That is mandatory, not cosmetic: an eager frame contacts the provider per embed before
  anyone chooses to watch.
- Q: Are embeds interactive? → A: **Yes.** Links work and record fields are editable — in both the
  ordinary view surface and dashboard embeds. A **record** edit persists; a **view** filter or sort
  stays local to that embed and is never written back to the shared definition.
- Q: Is the existing unversioned, unaudited save path in scope? → A: **Yes — step 0**, before any
  embed work.
- Q: Who is recorded as the author of a person's inline edit? → A: A reserved `user:<id>` actor,
  distinct from any agent identifier.
- Q: What do links inside an embedded view resolve against? → A: **The note reader's graph**, not the
  view's loaded rows. This also fixes the existing colour asymmetry between base links and note
  links.
- Q: Does an embedded view show controls? → A: **On hover or focus** — not always, not never.
- Q: Two views with the same display label? → A: **Refuse and name both.**
- Q: A view the server cannot serve? → A: **Render its reason regardless.**
- Q: Are a record's title and path editable inline? → A: **No.** Renaming cascades to notes the
  caller did not name.
- Q: How many PDF workers? → A: **Two**, with a visible queue beyond that.
- Q: Is the video host configurable? → A: **Yes, a config key, default on.**

### 2026-09-09 — four further rulings, settled after this spec's own review (ADR §2.8)

- Q: Does a shown note's own embeds render as content? → A: **No. One level only (N1).** They are
  links. **This removes cycle detection and the depth cap entirely** — not defers them: with one
  level, a cycle cannot be constructed by any sequence of product actions, so the mechanism, its
  four tests and its success criterion are deleted. Revision 1 built a detector against an input the
  product cannot produce.
- Q: Is the version token on the whole-file saves required or optional? → A: **Required (N2).** No
  exemptions. The PDF-annotation save, whose read path returns no token today, gets a **fix** — the
  download response carries the token as a header — not an exemption.
- Q: Does this work guarantee a complete printout? → A: **No. Print support is dropped (N3).** The
  browser's print event is synchronous and cannot be held open while modules load. Printing reaches
  what has mounted; the reader states that on screen. Making printing complete is separate,
  unscheduled work.
- Q: What happens to a record edit when authentication is bypassed and nobody can be identified?
  → A: **It is saved, and audited with the actor `anonymous` (N4).** Not refused with 503, which was
  the architect's recommendation and is overruled. The accepted consequence: **an `anonymous` entry
  cannot answer "who"**, so those entries are a distinct population from identified ones and must be
  greppable as such.

### 2026-09-09 — three further rulings, settled after this spec's SECOND review (ADR §14.1)

- **N5 — one version token, and it is the EXISTING one.** Both save doors use
  `pkg/knowledge/version.go`'s `ComputeVersionToken` / `ReadNoteVersion`, named by symbol.
  **A-11 is closed.** No back-compat is owed: `rest_library.go` contains zero token code today, so
  there is no second definition to migrate — the risk was never migration, it was an implementer
  inventing a second one, most likely from size and modification time. See EMB-007a, EMB-007b.
- **N6 — agents DO see the raw path** for an out-of-base embed, together with its reason.
  **Redaction on the agent surface is rejected as theatre.** An agent reading a note already
  receives that note's body, so hiding the path in the links list removes the agent's ability to
  say *which* embed is broken while removing none of its access to the path. A control that costs
  diagnostic precision and buys no confinement is a control only in appearance. EMB-024's asymmetry
  is **ratified**; a later change must not read it as an oversight.
- **N7 — record editing stays in this spec**, as the last step, with its four contract changes
  (CW-4…CW-7). Not split out.

### 2026-09-09 — three claims from this spec's revision 1 that were false against the code

Recorded because the pattern matters more than the three facts, and because all three were
*inherited* rather than invented — read from the ADR, from a code comment, or from an earlier
sentence in the same document, and never re-measured.

- *"`EditNoteRequest` requires both an audit sink and an actor."* It requires **neither**. A nil
  sink writes the file and records nothing, with no error.
- *"The audit system must be extended with the knowledge event names."* **Already done**, and a test
  asserting it — with the exact negative control this spec prescribed — already existed. The source
  was a stale comment saying the opposite.
- *"A skipped note contributes no edges."* It contributes a **matching unresolved edge** when the
  skip happened during the folder walk, and **no skip effect at all** when it happened while reading
  the file. Neither produces zero edges, so both of revision 1's fixtures tested nothing.

### 2026-09-09 — scope, restated so it cannot drift

- **In**: named view embeds, first-view embeds, the inline data fence, pictures including SVG,
  picture sizing, PDFs, PDF page fragments, note/heading/block transclusion, sound, video, query
  fences, mathematics (already working), and allow-listed video.
- **Out, permanently**: every plugin notation (Dataview, Excalidraw, Kanban, Templater, Charts, ABC,
  Admonition) and Canvas. External pictures. Any external frame host other than the allow-listed
  video host.
- **Falls back to a link, never framed inline**: `html`, `other` and `text`.

### 2026-09-09 — raised by this spec, not yet answered

**Revision 1 raised ten (A-1…A-10). All ten are now closed** — by founder ruling, by measurement,
or by being promoted into a requirement where an implementer would actually read them.

**A-11 is CLOSED by founder ruling in revision 3**, and it was the only one of the four that changed
step 0's work: **the two doors share the existing knowledge token**, `ComputeVersionToken` /
`ReadNoteVersion`, named by symbol in EMB-007a. No back-compat is owed — `rest_library.go` has zero
token code today.

**Three remain open (A-12…A-14)**, all found while resolving the round-1 review:

- **A-12** — what an embed of a knowledge base's own control-plane file does, and whether an
  import-time failure gets a different sentence from an evaluation-time one.
- **A-13** — what the agent-surface embed marker costs on the real seventy-five-embed cockpit note.
  One command answers it.
- **A-14** — when the reader learns the operator emptied the video allow-list mid-session. Get this
  wrong and an open page offers a play control the served policy now blocks — a control that does
  nothing, which is the undetectable-fallback shape this project has already removed once.
