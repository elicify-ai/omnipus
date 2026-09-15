# ADR-083 review — adversarial findings

- **Document reviewed:** `docs/internal/architecture/ADR-083-embedded-content-in-knowledge-base-notes.md` (768 lines)
- **Worktree / branch:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate`, `integrate/library-improvements-v0.1.1`, HEAD `def10b90e`
- **Review date:** 2026-09-09
- **Mode:** structured-spec (labelled decisions D-A…D-D, D1…D9, Q1…Q6, a scope section and a sequencing table; no BDD/FR/traceability apparatus)
- **Method:** every load-bearing factual claim in the ADR was re-checked against the code on this branch. Nothing was taken on the ADR's word, including its own five self-declared corrections.

---

## 1. Executive summary

Twenty-eight findings: **5 CRITICAL, 10 MAJOR, 8 MINOR, 5 OBSERVATION**.

The ADR's five claimed corrections to its brief are all **correct** — verified independently
against the code. Its structural instincts (one renderer per kind, an op rather than a ninth
tool, contract-first for the write path, real cycle detection) are sound, and its refusal to
inherit Obsidian's silent degradations is right.

But three of the ADR's *measured* claims are false in ways that invalidate the arguments built
on them, and the resolver at the centre of the design has a state model that is not exhaustive
— on a bounded link graph it will print "Nothing in this knowledge base is named X" about a
file that exists. That is the precise class of defect the last five commits on this branch were
fixing.

**Verdict: BLOCK.**

---

## 2. Verification of the ADR's own five claimed corrections

The ADR asks to be checked rather than trusted. It was. All five hold.

| # | Claim | Verdict | Evidence |
|---|---|---|---|
| 1 | Embed→image branch is gated on image extensions; everything else becomes a link with an "embed shown as a link" badge | **CONFIRMED** | `knowledgeMarkdown.tsx:304` `IMAGE_EXTENSIONS`; `:375` `if (parsed.embed && IMAGE_EXTENSIONS.has(extensionOf(parsed.target)))`; `:595` the literal badge text |
| 2 | `knowledge_link` / `knowledge_set_property` are not live tools | **CONFIRMED** | `pkg/config/defaults.go:625-645` lists exactly the eight named; `pkg/coreagent/core.go:483-485` records the nine as "all RETIRED" |
| 3 | No record-field write endpoint; `PUT .../content` exists, unversioned whole-file overwrite | **CONFIRMED — and understated**, see C5 and m3 | `contracts/openapi.yaml:8143` (`getLibraryContent`), `:8191` (put). No version field on either |
| 4 | `knowledge_version_conflict` is an error code, not a tool | **CONFIRMED** | `contracts/components/schemas/KnowledgeConflictError.yaml` exists; no such tool name anywhere |
| 5 | `knowledge_read` does not distinguish an embed from a link | **CONFIRMED** | `pkg/knowledge/knowledge_read.go:109-125` — `ReadLink` has `Form/Alias/Heading/Resolved/Reason/Ambiguous/Candidates/Line`, no `Embed`; rendered at `:217` |

**Also confirmed exactly as measured:** the pane-shape counts (LibraryImagePreview 2,
BasePreview 5, LibraryPdfPreview 13); `spaContentSecurityPolicy` ends `frame-src 'self'` with
`img-src 'self' data: blob:` (`pkg/gateway/embed.go:159-174`); KaTeX and `remark-math` are wired
(`kbMarkdownBase.tsx:263-264`, `package.json:93,100,102`); `KB_REHYPE_PLUGINS` contains no
`rehype-raw`; `BasePreview`'s view-result query uses `staleTime: 60_000` and
`refetchOnWindowFocus: false` (`BasePreview.tsx:185-189`).

**Three measured claims are FALSE.** See C1, M1 and m1.

---

## 3. Findings

### CRITICAL

---

**C1 — D2(a)'s "verified safe" is false; the widening does not compile for the fallback renderer**
*Lens: Incorrectness · Section: D2(a), §10 "Measured directly"*

The ADR states, twice, that this was measured: *"every renderer reads only `entry.path` or
`entry.name` — never `size`, `modified_at`, `is_dir` or `is_hidden`"*, and rests D2(a)'s whole
safety argument on it (*"Widening a parameter type is a no-op at every existing call site, and
the compiler proves it"*).

It is not true. `src/components/library/preview/LibraryDownloadCard.tsx:48`:

```
{label} · {formatLibrarySize(entry.size)} · modified {formatRelative(entry.modified_at)}
```

`LibraryDownloadCard` takes `entry: LibraryEntry` (`:25`), reads `entry.size` **and**
`entry.modified_at`, and takes no `workspaceId` at all. It is reachable from an embed: D1 stage
3 makes `classifyLibraryEntry`'s ten kinds the dispatch table, and kind `other` routes to it
(`LibraryPreviewPane.tsx:237`), as do the `binary` and `too_large` fallbacks (`:607`, `:610`).

This matters beyond one file. The ADR's argument is *"the compiler proves it"* — the compiler
would have disproved it in one run, and the ADR states plainly that nothing was executed. The
`LibraryFileRef` pick as specified (`name | path | mime | is_text_editable`) cannot type the
fallback renderer, which is exactly the renderer an embed of an unsupported kind needs.

> **Fix.** Either (a) exclude `LibraryDownloadCard` from the widening and state that an embed of
> an unsupported kind falls back to the existing "embed shown as a link" treatment rather than a
> download card (which is probably the right product answer anyway — a download card inside a
> paragraph is nonsense); or (b) add `size` and `modified_at` to `LibraryFileRef` and state where
> the resolver gets them from, given §D1 explicitly rejects inventing them. Do not ship (b)
> silently: the ADR's own reason for returning a `LibraryFileRef` was that fabricating those two
> fields is "a cardinal error". Then run `npm run typecheck` before ratifying, not after.

---

**C2 — D1's three-state model is not exhaustive, and the missing state prints a false statement**
*Lens: Incompleteness / Incorrectness · Section: D1 stage 2, D-B, D4*

D1 stage 2 gives a table of exactly three states: edge found + resolved, edge found +
unresolved, graph not loaded. It is presented as total. At least three real conditions fall
outside it, all producing "graph is loaded, and there is no matching edge":

1. **Allow-listed YouTube.** `pkg/knowledge/links.go:546-549` returns no `Link` at all when the
   markdown destination is external: *"A scheme URL leaves the collection … neither is an edge
   in the note graph."* So D9's `![](https://www.youtube.com/…)` has **no edge, ever**. Under
   D1's table as written, that is not `resolved`, not `unresolved` and not `unknown`.
2. **A bounded graph.** `KnowledgeGraphResponse` is bounded by FR-054 and reports
   `truncated`, `hop_limit_applied` and `node_limit_applied`
   (`contracts/components/schemas/KnowledgeGraphResponse.yaml:15-18, 67-84`). A dashboard in a
   large knowledge base can legitimately have edges the response omitted.
3. **A skipped note.** `KnowledgeGraphSkip` enumerates `symlink`, `outside_root`, `unreadable`,
   `not_addressable`, `node_limit`, `hop_limit`. A note skipped for any of these contributes no
   edges.

In cases 2 and 3, D-B's mandate ("never an empty box") plus D4's copy produces the reader-facing
sentence **`Nothing in this knowledge base is named "Tasks.base"`** about a file that exists and
that the reader can open in the next pane. And per D-B that false statement is *also reported to
agents*, so an agent summarising the dashboard repeats it.

This is not a hypothetical class of defect on this branch. The four commits immediately below
HEAD are `stop answering 'searched everything' when a mount was never opened`, `enforce the vault
query bound and stop claiming a complete search over a partly-unloaded corpus`, and `give the
agent grep tool the honesty signals the human UI already shows`. The ADR reproduces the same
mistake in a new surface.

> **Fix.** Add a fourth state, `indeterminate`, for "graph loaded, no matching edge, and the
> response reported truncation or a relevant skip". It renders as a *distinct* marker naming the
> bound that was hit — not the "no such file" marker — and reports itself to agents as
> indeterminate, not as broken. Separately, state explicitly in D1 that an external allow-listed
> URL is recognised **before** the graph lookup and never enters the three-state model at all.
> D9 implies this; D1 must say it, because D1 is where the implementer will be.

---

**C3 — D9 mutates a CSP string whose own freeze condition is explicitly unmet, and names none of the artefacts that measure it**
*Lens: Insecurity / Inoperability · Section: D9, §7.2*

`pkg/gateway/embed.go:36-46` states, in the file the ADR proposes to edit:

> THIS STRING WAS SHIPPED UNMEASURED, AND IS NOW MEASURED — but not yet FROZEN. §10.7's freeze
> condition is a headed run in Chromium, Firefox AND Safari with zero violations … Firefox and
> WebKit remain outstanding.

The ADR proposes adding a third-party host to `frame-src` in that string. Its only stated test
obligation is *"a test must assert they are equal"* (SPA allow-list vs Go string) and §7.2's
*"reviewed as security work"*. It names none of:

- `pkg/gateway/embed_csp_test.go` — `TestSpaServedWithCSP` (spec test 68) and
  `TestSpaCsp_DirectiveFloor` (spec test 119), which pin the directive floor;
- `tests/e2e/csp-assumptions.spec.ts`, the Chromium-only measurement run;
- `docs/internal/architecture/csp-audit-2026-09-05.md`, the audit report that records the
  unfrozen status and would become stale on the day this merges.

Worse, the same string's `frame-ancestors 'none'` is documented at `embed.go:21-28` as the
**compensating control** for the *preview* policy's `frame-src 'self'` — FR-006b. The two
directives in this one string are coupled by an argument the ADR does not engage with, while
proposing to change one of them.

> **Fix.** D9 must state: (a) which of the three named test artefacts change and how; (b) that
> the csp-audit document is amended in the same PR; (c) that the Chromium measurement is re-run
> with a YouTube embed in the journey list, and that Firefox/WebKit remain outstanding *after*
> this change too (i.e. the change does not silently claim the freeze); (d) that
> `frame-ancestors 'none'` is untouched and why the coupling at `embed.go:21-28` is unaffected.

---

**C4 — The `html` and `other` kinds are in D1's dispatch table and absent from §5's scope, reopening a surface §2.5 declares closed**
*Lens: Insecurity / Inconsistency · Section: D1 stage 3, §2.5, §5*

D1 stage 3: *"Run the resolved path's filename through the existing `classifyLibraryEntry`. Its
ten kinds are the dispatch table."* §5's In list names nine things and does **not** include
`html` or `other`. §5's Out list does not exclude them either.

`![[page.html]]` classifies as `html` (`libraryPreviewKind.ts:80`) and, in the pane, mounts
`LibraryHtmlFrame` — a token-minted (`previewTokenRequestFor`, `LibraryPreviewPane.tsx:401`),
sandboxed iframe carrying **agent-generated HTML**, governed by
`libraryIsolationPolicyTemplate`. D1 as written mounts that frame inline in the note reader.

§2.5 states the opposite conclusion for the neighbouring case: *"A hand-written `<iframe>` in a
note is therefore not rendered as HTML at all. The capability table's 'iframe' row is closed by
construction, and §5 keeps it closed."* It is not closed by construction if `![[x.html]]` routes
to a frame. D9's entire security argument is that exactly one external host gains frame
permission; it does not consider that the wikilink route grants *same-origin* framing of
arbitrary collection HTML inside the reader, and the SPA's `frame-src 'self'` permits it.

`other` has the mirror problem: it routes to `LibraryDownloadCard`, whose `onDownload` prop the
note reader does not supply (see C1).

> **Fix.** §5 must enumerate a disposition for **all ten** kinds plus `note`, with no gaps. The
> recommended disposition for `html` is the existing "embed shown as a link" fallback (§2.2's
> honest treatment), with a one-line reason: a note reader is not an isolation boundary and must
> not become one. If `html` is genuinely wanted inline, that is its own ADR with its own threat
> model, not a row in a dispatch table.

---

**C5 — §4.1's hazard is not a future risk; it is the SPA's live note-save path today, and §4.2 does not close it**
*Lens: Incorrectness / Incompleteness · Section: §4.1, §4.2, §7.4*

§4.1 frames the unversioned door as an adjacent hazard: *"So the SPA has an unguarded door and
the agent has a guarded one, into the same files."* It is stronger than that. The SPA
**already walks through it, for note bodies, today**:

`src/components/library/preview/useLibraryFileEditor.ts:84`

```
mutationFn: (content: string) => putLibraryContent(workspaceId, { path, content }),
```

No `expect_version`. `useLibraryFileEditor` backs `LibraryTextPreview`, which backs
`LibraryMarkdownPreview`, `LibraryCodePreview` and `LibraryMermaidPreview` — i.e. the Library
editor a person uses to edit a knowledge-base note's body. And neither
`handleLibraryContentPut` (`pkg/gateway/rest_library.go:545`) nor
`handleLibraryContentBinaryPut` (`:610`) calls `logLibraryAudit` — verified: zero occurrences
inside those handler bodies, while `delete`/`upload`/`mkdir`/`create_vault`/`rename` all do
(`:494, :827, :876, :1014, :1238`).

So the existing state is: a human editing a note body from the SPA can silently clobber an
agent's concurrent write, with no audit record, today. §4.2 correctly refuses to route the *new*
inline field edit through that door — and then leaves the larger existing hole entirely
unmentioned. §7.4 "Residual" lists only Windows locking.

This changes the risk calculus of the whole ADR. §7.2 says *"A second write door into the
knowledge base exists after step 5"*; there are already two unguarded ones
(`content`, `content-binary`) plus the guarded agent path, and step 5 makes a third guarded one.

> **Fix.** §7.4 must record the existing unversioned, unaudited SPA note-save path as a live
> residual with its file:line, and §4.2 must state whether closing it is in or out of scope for
> this ADR. Recommended: in scope, as a step 0 — add `expect_version` to `LibraryContentRequest`
> and `logLibraryAudit` to both put handlers — because step 5 makes concurrent human/agent writes
> materially more likely (the ADR says so itself) while leaving the bigger door open.

---

### MAJOR

---

**M1 — `heading_found` and block ids are NOT on the wire; §2.3's "all of this is already on the wire" is false, and two decisions depend on it**
*Lens: Incorrectness / Incompleteness (Constraint #8) · Section: §2.3, D4, D5, §6*

§2.3 claims resolution already reports *"for a `#Heading` fragment — whether that heading was
actually found in the target. All of this is already on the wire: `KnowledgeGraphEdge.yaml`
carries `embed`, `resolution`, `ambiguous`, `candidates` and `heading`."*

The five listed fields do exist. `heading_found` does not. `ResolvedLink.HeadingFound`
(`pkg/knowledge/links.go:693`) and `ResolvedLink.BlockID` (`:80`) are both real in Go and
**neither is projected onto the edge**: `knowledgeEdge()` (`pkg/gateway/rest_knowledge.go:621-647`)
sets `FromPath`, `ToPath`, `Resolution`, `Ambiguous`, `LinkText`, `Alias`, `Heading`, `Embed`,
`Candidates` — and stops. `KnowledgeGraphEdge.yaml` has no `heading_found` and no `block`
property.

Two decisions rest on the false claim:

- **D4** row 3, *"File found, heading not"* — cannot be computed client-side from the edge.
  Its second half, *"and lists the headings it has"*, needs a `KnowledgeOutline` fetch that no
  section mentions.
- **D5** block references, *"`links.go` already keeps block anchors separate from heading
  text so a `#^abc123` is never matched against a heading"* — true in Go, invisible to the SPA.

Both therefore require a schema change plus `scripts/gen-contracts.sh` plus committed generated
diffs under Constraint #8 — obligations §6's sequencing table, §7.2's cost list and §4.2's
contract list all omit. §4.2 lists exactly two new schemas, both for the write path.

> **Fix.** Add `heading_found` and `block` to `KnowledgeGraphEdge.yaml`, populate them in
> `knowledgeEdge()`, regenerate, and add the contract work to steps 2 and 3 of §6 as explicit
> line items. Correct §2.3 to say what is on the wire and what is not. Say where D4's "lists the
> headings it has" comes from.

---

**M2 — D1 stage 2's edge-matching key is under-specified and, as written, ambiguous on the founder's own dashboards**
*Lens: Ambiguity / Incorrectness · Section: D1 stage 2*

The rule is: *"find the edge whose `link_text` matches the written target and whose `embed` is
true."*

`link_text` is set from `l.Target` (`pkg/gateway/rest_knowledge.go:628`), and `Target` is the
wikilink with the `#fragment` **already stripped** (`links.go:474-479` splits on `#` before
assigning). So `![[Tasks.base#Needs Daniel]]` and `![[Tasks.base#Awaiting founder]]` produce two
edges with **identical `link_text`, identical `to_path`, identical `embed`** — distinguished only
by `heading`. The founder's dashboards are described as fifteen-plus named views; several from
one `.base` is the expected shape, not an edge case.

Two further omissions in the same rule:

- **`from_path` is not in the key.** §2.3 says the reader loads *"the collection's link graph"*.
  Without filtering on `from_path == the note being read`, an identically written embed in any
  other note matches.
- The rule does not say what happens on **multiple matches** — pick first? refuse? — which is
  precisely the "resolving it deterministically is not a licence to stay quiet" principle
  `KnowledgeGraphEdge.yaml`'s own description states for the ambiguity case.

> **Fix.** State the key as `(from_path == notePath) && embed && link_text == target &&
> heading == fragment && block == blockId`, and state the behaviour when zero or more than one
> edge matches. Both depend on M1's wire fields landing first.

---

**M3 — D7 mixes two path coordinate systems and the conversion is unstated**
*Lens: Infeasibility · Section: D7*

D7: *"The embed resolver calls it for the `.base` file the graph resolved."*

- `KnowledgeGraphEdge.to_path` is **collection-relative** (its own description says so).
- `GET /library/{workspace_id}/knowledge/base-views` requires `path` = *"Workspace-relative path
  of the .base file"* (`contracts/openapi.yaml:9074-9080`).

Passing `to_path` through is wrong for every collection not mounted at the workspace root — it
404s, or worse resolves to a different file of the same name nearer the root. The conversion
helper exists — `collectionPathToWorkspacePath(collectionRoot, collectionRelativePath)` at
`src/components/library/knowledge/KnowledgeBacklinks.tsx:112`, whose own header warns *"Guessing
that they are the same would produce a link that opens the wrong file — or nothing — for every
collection that is not mounted at the workspace root"* — but the ADR neither names it nor says
where the resolver obtains the collection root. `KnowledgeBacklinks.tsx` is not in §10's
read-in-full list.

The same conversion is needed for every non-`.base` kind too, since all the renderers take
workspace-relative `entry.path`.

> **Fix.** D1 stage 3 must state that the resolver converts collection-relative → workspace-relative
> via `collectionPathToWorkspacePath` using the collection root from `KnowledgeBaseInfo`, and that
> the converted path is what populates `LibraryFileRef.path`. Add `KnowledgeBacklinks.tsx` to §10.

---

**M4 — D2(b) and D8 contradict each other, and D8's WL-1 paragraph contradicts D8's own wiring instruction**
*Lens: Inconsistency · Section: D2(b), D8*

D2(b) states the variant rule as absolute: *"`variant` may change **layout only** … It may never
change what is fetched, which states exist, or how any non-happy state is rendered"*, enforced by
a test asserting non-happy states are **byte-identical** across variants.

D8 then requires: *"`ViewCellLink`'s resolver props (`resolveWikilink`, `linkHref`, `onOpenPath`)
must come from **the embedding note's reader**, not from the pane."* Those props decide what a
cell link resolves to and which of `resolved` / `unresolved` / `unknown` treatment it renders
(`ViewCellLink.tsx:42-49, 76-90`) — i.e. they change what is fetched *and* how a non-happy state
renders. The byte-identical test D2(b) mandates would fail on the change D8 mandates.

Within D8 there is a second contradiction. D8 says the embed *inherits* WL-1 — base links show
`unknown` because a view resolves only against loaded rows, while note-body links resolve against
the whole graph. But if the note reader's resolver is threaded in, the embed resolves against the
note reader's graph and would show **more** resolved links than the same view in the pane. D8
cannot both thread the reader's resolver in and inherit the pane's asymmetry.

> **Fix.** Either narrow D2(b) to "layout and injected resolver only, with the injected resolver
> named as the one permitted non-layout difference and its own cross-variant test", or drop D8's
> resolver threading and accept that a link in an embed opens in the pane. Then correct D8's WL-1
> paragraph to describe whichever behaviour is actually chosen, since the two produce visibly
> different link colouring.

---

**M5 — D2(c)'s `[INFERRED]` names an API the code does not use, and a shared port breaks the existing failure detection**
*Lens: Incorrectness / Infeasibility · Section: D2(c), Q6*

The ADR's inference is about `GlobalWorkerOptions.workerPort`. `LibraryPdfPreview` does not use
it. It constructs a worker inside the load effect and hands it over per document:

```
port = new Worker(`${ASSET_BASE}pdf.worker.min.mjs`, { type: 'module' })   // :410
const pdfWorker = pdfjs.PDFWorker.create({ name: 'omnipus-library-pdf', port })  // :419
```

So the thing to verify is not "does PDF.js support `GlobalWorkerOptions.workerPort`" but "can one
`PDFWorker` instance back several concurrent `getDocument` calls in the pinned build". Different
question, different answer surface.

More consequentially, a shared port breaks a control the file was deliberately built around
(`:425-443`): each instance attaches `port.addEventListener('error', …, { once: true })` and
races it against `task.promise`, because a missing worker file *"makes `new Worker` succeed
synchronously but fail asynchronously … and `task.promise` hangs on 'Opening…' forever."* With
one shared port:

- one document's worker error rejects **every** mounted instance's load promise, so a healthy PDF
  reports the failure of an unrelated one — a false error, which is the mirror of C2's false
  success;
- once the shared port has errored, every subsequent mount reuses a dead port, and the
  reference-counted lifecycle in D2(c) has no "the port is poisoned, replace it" state.

Neither is mentioned in D2(c) or Q6.

> **Fix.** Restate the `[INFERRED]` item as the correct question. Specify the shared port's
> failure lifecycle explicitly: error attribution per document, and a poisoned-port replacement
> rule. Given the complexity this adds to a component whose header is about *not* silently
> degrading, seriously consider the simpler alternative the ADR already lists in Q6 — a small
> pool with a visible ceiling — as the primary rather than the fallback.

---

**M6 — `op=embed` collides with an existing argument name, and `editArgNames` is global across ops**
*Lens: Ambiguity / Incorrectness · Section: D6*

Two mechanical problems the ADR's *"Mechanically this is additive"* claim misses.

**(a) `heading` means two different things.** `editArgNames`
(`pkg/knowledge/knowledge_edit.go:121-128`) already contains `heading`, used by
`op=append_section` to name **a heading of `path`** (the file being written). D6 uses `heading`
for **a heading of the `target`** while introducing `section` for the heading of `path`. Same
tool, same argument name, opposite referent, decided by the value of `op`. The tool's own header
(`:112-120`) says the argument list exists because *"a silently ignored argument is a caller that
believes it narrowed something"* — a same-name/different-referent argument is worse than a
silently ignored one, because the model has a plausible wrong reading.

**(b) `unknownArgs` is checked once, globally.** `Execute` calls
`unknownArgs(args, editArgNames)` before dispatching on `op` (`:298`). Adding `view`, `page`,
`width` and `block` to that list makes them **accepted on every op** — so `op=link` with
`width=400` now passes validation and is silently ignored. That is the exact regression the
comment above `editArgNames` describes (`"bodyy" for "body"` passing through and reporting
success).

> **Fix.** Rename D6's target-fragment argument to something unambiguous (`target_heading` /
> `target_block`), and add a per-op argument whitelist so an argument no op reads is refused for
> that op, not merely for the tool. State both in D6 rather than leaving them to the implementer,
> because both are invisible in review — a passing test suite will not catch either.

---

**M7 — There is no audit path for a REST-originated knowledge write, and the precondition is larger than §10 records**
*Lens: Inoperability / Insecurity (Repudiation) · Section: §4.2, §10, Q1*

§10 flags this as unverified. Verified now, and it is worse than "not traced":

- `EditNoteRequest` requires `Audit AuthorAudit` and `Actor AuthorActor`
  (`pkg/knowledge/author.go:613-616`). A REST caller has neither — that is Q1, correctly
  identified as blocking step 5.
- `pkg/knowledge/audit.go:18` states the knowledge tools *"bypass"* the gateway's
  `logLibraryAudit` path *"entirely"*. So §4.2's *"one audit sink"* is aspirational: the gateway
  sink and the knowledge sink are two sinks today.
- `pkg/knowledge/audit.go:65-69`: *"NOTE for whoever wires these into the gateway: pkg/audit's
  IsValidEventName does not yet know them, so today each one triggers audit's warn-once 'unknown
  event name' log … That file is outside this package's ownership."* So step 5 also requires a
  change to `pkg/audit/events.go` — a package neither §4.2 nor §6 mentions, and one the ADR's
  own source note flags as out of the knowledge package's ownership.

> **Fix.** Promote this from "not verified" to a named precondition list on §6 step 5: (i) Q1
> answered; (ii) `pkg/audit/events.go` extended with the knowledge event names; (iii) an explicit
> statement of whether the REST field-write emits a `knowledge.*` event, a `library.*` event, or
> both, and which sink reads it. Mark step 5 as blocked in the sequencing table, not only in §9.

---

**M8 — D5 never says how a transcluded note's text is obtained**
*Lens: Incompleteness · Section: D5, D1*

The `note` kind is introduced in D1 stage 3 and given cycle detection in D5, but nothing states
how the transcluded content reaches the renderer. `LibraryMarkdownPreview` requires a `content`
prop (`:94-98`) — it does not fetch — and `LibraryFileRef` carries no content. Unanswered:

- which call fetches it (`GET .../content`? `knowledge/read`?), and its cache key;
- what heading/block slicing runs against — the whole file client-side, or a server-side range;
- how it interacts with D3's lazy mount (a five-deep nest is five sequential fetches, each
  starting only when the previous one renders into view);
- whether a transcluded note's own embeds resolve against **its** link graph or the outer note's
  (they are different queries, and the outer note's graph will not contain the inner note's
  edges unless the query kind covers them).

The last point is not cosmetic: without it, every embed inside a transcluded note falls into C2's
missing state.

> **Fix.** D5 must name the fetch, the cache key, the slicing side, and the graph a nested
> transclusion resolves against. If nested embeds inside a transclusion are not supported in
> step 3, say so and render them as the §2.2 link fallback.

---

**M9 — D3's lazy mount has no failure behaviour, no concurrency bound, and no print/find story**
*Lens: Inoperability / Incompleteness · Section: D3, D-A, §7.3*

D3 specifies the mount trigger, the reserved height and the staleness window. It does not specify:

- **What a failed evaluation looks like.** A view query that 500s inside an embed: retry policy?
  visible error? Does it retry on every re-entry into the viewport (scroll up and down forty
  times = forty retries)?
- **A bound on concurrent in-flight evaluations.** D-A bounds *mounting*; §7.3 concedes fifteen
  modules are fifteen evaluations. Scrolling fast through a forty-module dashboard fires forty
  evaluations against a single-binary server with no stated queue or ceiling. D-A rejects a cap on
  *count*, and explicitly endorses a bound on *work* — D3 never sets one.
- **Print, export, and browser find.** A lazily-mounted dashboard printed or Cmd-F'd contains only
  what has been scrolled past. Silently.

The last is the same silent-subset failure D-B exists to prevent, arriving through a different door.

> **Fix.** Add to D3: a retry policy with a stated ceiling; a concurrency limit on in-flight view
> evaluations with a visible indicator when queued (Q6's principle applied here); and an explicit
> statement of print/find behaviour — even if the answer is "not supported in this ADR", it must
> be stated rather than discovered.

---

**M10 — `unknown` collapses "not loaded yet" with "failed to load"**
*Lens: Ambiguity / Inoperability · Section: D1 stage 2*

The table's third row is *"graph not loaded yet → a reserved placeholder, never a marker"*. A
graph request that **fails** (500, network, auth) is also "not loaded". Under the rule as
written, a dashboard whose graph fetch errors shows fifteen reserved grey placeholders forever,
with no error anywhere — a silent failure, and one that looks exactly like "still loading".

> **Fix.** Split into `loading` (placeholder) and `graph_unavailable` (a single page-level error
> naming the failure, with a retry — not fifteen per-embed errors). The distinction is cheap:
> TanStack Query already separates `isPending` from `isError`.

---

### MINOR

---

**m1 — §2.4's "every preview renderer takes the same prop pair" is false for six of ten kinds, and four renderers are not exported at all**
*Lens: Incorrectness · Section: §2.4*

Measured signatures:

| Renderer | Actual props |
|---|---|
| `LibraryImagePreview`, `LibraryVideoPreview`, `LibraryPdfPreview` | `{ workspaceId, entry }` ✓ |
| `LibraryMarkdownPreview`, `LibraryCodePreview`, `LibraryMermaidPreview`, `LibraryTextPreview` | `+ content, onSaved` |
| `BasePreview` | `+ loadContent, loadBaseViews` |
| `LibraryDownloadCard` | `{ entry, reason, onDownload }` — no `workspaceId` |

And `LibraryAudioPreview` (`LibraryPreviewPane.tsx:339`), `LibraryHtmlBody` (`:362`),
`LibraryHtmlFrame` (`:435`) and `LibraryTextBody` (`:589`) are **module-private functions inside
`LibraryPreviewPane.tsx`**, not exported components. There is no `LibraryAudioPreview.tsx` file.
So §6 step 6's "audio, video" reuse requires extracting a private function into its own module
first — a step the sequencing table does not contain.

> **Fix.** Correct §2.4 to state which renderers share the pair and which do not, and add
> "extract the pane-private renderers" as an explicit item in §6 step 6.

---

**m2 — D3's "inherit both" is wrong for one of the two queries, and D7 adds an uncosted call per `.base`**
*Lens: Incorrectness / Incompleteness · Section: D3, D7*

`BasePreview` has two queries with different staleness: view-result at `60_000` (`:188`) and
**base-views at `10_000`** (`:147`). D3 says the embed "must inherit both", naming only the
60-second figure. Separately, D7 requires one `base-views` call per `.base` file per embed
resolution; on a fifteen-module dashboard over five base files that is up to fifteen extra
requests unless the query key dedupes by `.base` path. Neither is costed in §7.3.

> **Fix.** State both staleness figures. State the base-views query key explicitly as keyed on the
> `.base` path so N embeds of one file share one request.

---

**m3 — §4.1 says "a whole-file text write endpoint"; there are two**
*Lens: Incompleteness · Section: §4.1*

`PUT /library/{workspace_id}/content-binary` (`contracts/openapi.yaml:8233`) is a sibling with the
same properties — *"overwriting any existing content entirely"*, no version token, no audit call
in its handler (`rest_library.go:610`). It accepts up to 25 MB of base64. It is the door a filled
PDF goes through.

> **Fix.** Name both in §4.1 and in the C5 residual.

---

**m4 — "base first view" and the ` ```base ` fence appear in §5's scope and in §6 step 2, and are decided nowhere**
*Lens: Incompleteness · Section: §5, §6, D7*

D7 decides label→slug matching for `![[X.base#View]]`. Neither D7 nor any other decision says
which view a fragment-less `![[X.base]]` renders (first imported? first non-unservable? all as
tabs, as the pane does?), nor what the inline ` ```base ` fence's body contains, how it is parsed,
or how it addresses a collection.

> **Fix.** Add a D7 clause for the fragment-less form, and either specify the ` ```base ` fence's
> grammar or move it out of step 2 into step 6 with the ` ```query ` fence.

---

**m5 — Q1 blocks step 5, but §6's table does not say so**
*Lens: Inconsistency · Section: §6, Q1*

§9 Q1 states *"This blocks step 5, not the earlier steps."* §6's row for step 5 says only "Last,
because it is the only write path". A reader working from the sequencing table alone starts step 5
without knowing it is blocked. M7 adds two further preconditions to the same step.

> **Fix.** Add a "Blocked by" column to §6.

---

**m6 — No rollback or per-install control for anything, and D9 in particular**
*Lens: Inoperability · Section: whole document*

There is no feature flag, kill switch or config key anywhere in the ADR. For most of it that is
defensible (a renderer either routes or it doesn't). For D9 it is not: an operator running a
sovereign, no-telemetry deployment gains a hardcoded Google frame host in the application CSP
with no way to decline it, and no way to turn it off if the click-to-play placeholder regresses.

> **Fix.** State the rollback story per step in §7.2, and give D9 a config key (default on or off
> is the founder's call) so the third-party host is an operator decision rather than a build
> constant.

---

**m7 — D4's agent-surface sample output is unverified and the `.base` view case has no field to carry it**
*Lens: Incompleteness · Section: D4, §10*

§10 correctly lists the rendered form as unverified. Additionally: `ReadLink` carries `Heading`
but nothing that distinguishes a `.base` **view label** from a markdown **heading**. D4's sample
line `-> [embed] 06-Bases/Tasks.base #Needs Daniel` reuses the heading slot for a view label, so
an agent cannot tell "section of a note" from "saved view of a data table" — a distinction that
changes what the agent should say about a broken one.

> **Fix.** Either state that the heading slot is deliberately overloaded and why, or add a
> discriminator. Then verify the rendered output rather than the struct.

---

**m8 — D2(b)'s "byte-identical" test is stated but not scoped**
*Lens: Infeasibility · Section: D2(b)*

"Byte-identical" across variants cannot hold literally if `variant` changes layout classes, since
the rendered DOM of an error state includes its container's classes. The intent is clear (same
copy, same states, nothing dropped) but as written the test is either impossible or so loosely
interpreted it proves nothing.

> **Fix.** Specify the assertion: the set of rendered state identifiers and their text content are
> equal across variants; container classes are exempt.

---

### OBSERVATION

**O1** — D6's Constraint #6 argument is correct and was verified (`defaults.go:625-645` grants
`knowledge_edit: "allow"`; no new ceiling or seed entry needed). Worth recording the flip side as
an accepted consequence: an operator cannot grant note editing while withholding embed authoring,
because they share one policy name. That is the deliberate FR-070c trade, but it should be stated
in §7.2 rather than left implicit.

**O2** — The ADR's `[INFERRED]` marker, its "Not verified" section and its refusal to claim
execution are good practice and should be kept. This review found three measured claims wrong
anyway (C1, M1, m1), which argues for one cheap addition before ratification: run
`npm run typecheck` against a stub `LibraryFileRef` change. That single command would have caught
C1, and it costs minutes.

**O3** — D-D's split (record state persists, view shape stays local) is the strongest decision in
the document and the reasoning is sound. Consider recording the corollary explicitly: because view
shape is per-embed and non-persistent, a reader who filters an embed and reloads loses it, and
that is intended — otherwise a future "helpful" fix will re-introduce Obsidian's behaviour.

**O4** — §2.2's "there is no broken-image bug to point at, so the only way to know this regressed
is a test that asserts each kind mounts its renderer" is the single best line in the ADR and
should be promoted into the sequencing table as a per-step exit criterion, not left in context.

**O5** — Alternatives considered and rejected are unusually well argued. The one missing
alternative: **do nothing for kinds with zero measured uses** (audio, video, query fences, PDF
page fragments — §2.1 measures 0 for all of them). §6 step 6 already defers them; §8 could record
"ship steps 1–5 and stop" as a considered option, since it is what the evidence supports.

---

## 4. Structural integrity (structured-spec mode)

| Check | Result |
|---|---|
| Every stated goal has acceptance criteria | **FAIL** — no acceptance criteria anywhere. §6's steps have no exit conditions; "works" is undefined per step. |
| Cross-references internally consistent | **FAIL** — D2(b) ↔ D8 (M4); D1 stage 3 ↔ §5 (C4); Q1 ↔ §6 (m5) |
| Scope boundaries explicit | **PARTIAL** — the Out list is excellent and unambiguous; the In list omits `html`, `other` and the fragment-less `.base` form (C4, m4) |
| Success criteria measurable | **FAIL** — no measurable criterion for any decision. "A forty-embed dashboard must work" (D-A) has no threshold. |
| Requirements referencing each other are consistent | **FAIL** — see M4 |
| Error/failure scenarios addressed per decision | **PARTIAL** — outstanding for D4/D5 (the ADR's core strength); absent for D3 (M9), D1 (C2, M10), D2(c) (M5) |
| Dependencies between requirements identified | **PARTIAL** — §6 gives an order and reasons; it gives no blockers (m5) and no contract-work dependencies (M1) |
| Claims verified against code | **PARTIAL** — the five self-declared corrections are all correct; three separate measured claims are wrong (C1, M1, m1) |

---

## 5. Test coverage assessment

The ADR names exactly three test obligations: the D2(b) cross-variant state test, the D9
allow-list equality test, and §2.2's "a test that asserts each kind mounts its renderer". All
three are the right tests. None is scoped, and the following have no test named at all:

| Untested area | Why it matters |
|---|---|
| The resolver's state model | C2's missing state is invisible without a test that feeds a **truncated** graph response and asserts the marker says "indeterminate", not "no such file" |
| Edge matching (M2) | Needs a fixture with two embeds of one `.base` under different fragments, asserting each resolves to its own view |
| Cycle detection (D5) | The ADR mandates the behaviour and names no test. Needs: A→B→A; A→A; a 6-deep non-cyclic nest hitting the cap **visibly**; and a diamond (A→B, A→C, B→D, C→D) which must **not** be reported as a cycle |
| Coordinate conversion (M3) | A collection mounted below the workspace root is the case that breaks; a root-mounted fixture passes either way |
| Shared PDF worker lifecycle (M5) | Two embeds where one document fails: assert the healthy one still renders |
| Conflict path (§4.4) | Assert no automatic retry — the ADR is emphatic and a future refactor will "helpfully" add one |
| `unknownArgs` per-op (M6) | Assert `op=link` with `width` is **refused**, not ignored |
| CSP (C3) | The equality test is named; the Chromium journey addition is not |

**Regression risk not identified anywhere in the ADR:** §2.2's current honest fallback ("embed
shown as a link") is *tested today* and this work reroutes past it. The ADR says the fallback is
kept, but names no test asserting the fallback still fires for the kinds that reach it.

---

## 6. STRIDE summary

| Component | Threat | Status in ADR |
|---|---|---|
| YouTube frame (D9) | **I**nformation disclosure — a mounted player contacts Google per embed | Addressed well: click-to-play mandatory, `-nocookie`, no thumbnails, `no-referrer`, constructed URL, id regex |
| `spaContentSecurityPolicy` (D9) | **T**ampering / **E**oP — a mistake here weakens the app's own policy | **Under-addressed** — C3: the string is unfrozen and under active measurement; no re-measurement required, no test artefacts named |
| `![[x.html]]` route (C4) | **E**oP / **S**poofing — agent-authored HTML framed inside the reader | **Not addressed at all**; §2.5 asserts the opposite conclusion |
| Inline field write (§4) | **T**ampering — lost update | Addressed: new endpoint, version token, same `EditNote` path, no auto-retry |
| Inline field write (§4) | **R**epudiation — no audit for a human write | **Partially addressed** — Q1 raises the actor; M7 shows two further missing pieces (`pkg/audit/events.go`, sink wiring) |
| `PUT .../content{,-binary}` | **T**ampering + **R**epudiation — unversioned, unaudited, and live today | **Not addressed** — C5, m3 |
| Lazy view evaluation (D3) | **D**enial of service — self-inflicted, N evaluations from one scroll | **Not addressed** — M9; D-A rejects a count cap and no work cap replaces it |
| Transclusion (D5) | **D**oS — recursion | Addressed better than Obsidian: real cycle set plus a visible depth cap |
| Embed target resolution | **S**poofing — an embed naming a path outside the collection | Addressed at write time via `cleanNoteArg` (verified: `authoring_tools.go:362`, applied to `op=link`'s target at `:600`); **read-time containment for an embed written by hand or by Obsidian is not stated** |

---

## 7. Unasked questions

1. **What happens to an embed written directly in Obsidian that names a path outside the
   collection?** D6 gates writes through `cleanNoteArg`, but the founder's vault is authored in
   Obsidian and synced. Read-time containment is never stated. `links.go` marks such a target
   `unresolved` with `outside_root` and does not read it — is the reader's marker allowed to print
   the escaping path back to the user?
2. **Is a `.base` embed evaluated with the reader's permissions or the note author's?** Nothing in
   the ADR discusses whose access a view evaluation runs under when a note is shared or opened by a
   different user.
3. **What does an embed do inside a fenced code block or inline code?** `![[x]]` inside a
   ` ``` ` fence must not mount anything. The parser skips fences for headings (`links.go:218`) —
   does it for embeds, and does the SPA-side resolver agree?
4. **What is the reserved height for a `note` transclusion**, whose size is unknowable before
   fetching? D3 gives per-kind heights and names only tables and images.
5. **Does a dashboard's fifteen view evaluations count toward any per-workspace rate limit or
   cost**, and what does the reader see if they are throttled?
6. **What is the migration story for the 75 existing embeds** if D7's label match fails for some of
   them? Is there a one-shot report telling the founder which of the 75 will render and which will
   not, before step 2 ships? Nothing in the ADR proposes measuring that, and it is a single query
   against data that already exists.
7. **When a base view is edited from an embed and the note's frontmatter changes, does the note's
   own reader re-render?** §4.5 invalidates view-result queries; it does not mention the note
   content query, the outline, or the link graph — all of which can be stale after a property
   write.

---

## 8. Verdict

**BLOCK.**

Five CRITICAL findings. C2 and C5 are the two that must move before anything else: C2 makes the
feature state a falsehood to both a reader and an agent under ordinary conditions, and C5 shows the
data-loss hazard the ADR treats as prospective is already shipping. C1 is cheap to settle and
would have been caught by one `npm run typecheck`.

The document is well argued and unusually honest about its own limits. The failures are
concentrated in exactly the place its own §10 warns about — claims recorded as "measured" that were
read rather than run.

Review written to:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate/docs/internal/architecture/ADR-083-embedded-content-in-knowledge-base-notes-review.md`

Address the findings above, then re-run:
`/grill-spec docs/internal/architecture/ADR-083-embedded-content-in-knowledge-base-notes.md`
