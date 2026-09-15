# ADR-083 implementation spec — adversarial review (grill pass 1 of 2)

- **Spec under review:** [`docs/internal/specs/adr-083-embedded-content-spec.md`](adr-083-embedded-content-spec.md) (3,187 lines)
- **Source ADR:** [`ADR-083`](../architecture/ADR-083-embedded-content-in-knowledge-base-notes.md) revision 2, and its [review](../architecture/ADR-083-embedded-content-in-knowledge-base-notes-review.md) (28 findings, BLOCK)
- **Worktree / commit:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate`, `integrate/library-improvements-v0.1.1`, HEAD `def10b90e`
- **Detected mode:** `plan-spec` (BDD scenarios with `Traces to:`, FR-xxx, SC-xxx, traceability matrix — full structural checks applied)
- **Method:** every factual claim in the spec's *Symbols Involved*, *Impact Assessment*, *Contract-First Work*, *Tool Policy* and *False-Green Register* sections was re-checked against the code and the contracts on `def10b90e`. Nothing below is inferred from the spec's own prose.
- **Founder-ratified decisions (D-A…D-D, Q1–Q9) are NOT re-opened.** Where a finding touches one, it is about the spec's *execution* of that ruling, never the ruling.

---

## 1. Executive summary

Seven CRITICAL and thirteen MAJOR findings. The spec is materially better evidenced than the ADR it implements, and its false-green register is the strongest artefact in it — but it inherits three unexamined ADR assertions that turn out to be false against the code, and three of its own headline capabilities (cycle detection, containment refusal, inline record editing) cannot be built as written on the current wire.

The single most consequential finding is **C1**: FR-060 forbids the very construction FR-056 and FR-058 exist to guard. Nested embeds inside a transclusion fall back to links, so a two-note cycle and a six-deep nest *cannot occur in production* — and the four cycle/depth tests (53–56) can only pass against fixtures the product is incapable of generating. That is the exact 673/673 shape the spec's own step-3 register is written to prevent, one level further up.

The second is **C2/C3**: the five-state resolver still emits *"Nothing in this knowledge base is named X"* about files that exist. Both of the spec's two "live today" producers of the `indeterminate` state are misidentified — a skipped target yields an `unresolved` edge, not zero edges — and the wire cannot distinguish "no such file" from "outside the collection root" at all.

**Verdict: BLOCK.**

| Severity | Count |
|---|---:|
| CRITICAL | 7 |
| MAJOR | 13 |
| MINOR | 9 |
| OBSERVATION | 3 |

---

## 2. Verification of the spec's own factual claims

The task brief noted that the previous two rounds each found "measured" claims that were false. This round re-measured all of them. **Most of the spec's claims are accurate** — a real improvement on rev 1 of the ADR. The exceptions are load-bearing.

### 2.1 Verified TRUE (no action)

| Claim | Result |
|---|---|
| `resolveEmbedUrl` at `KnowledgeNoteView.tsx:240`, match key at `:245` with no fragment | **TRUE**, verbatim |
| `IMAGE_EXTENSIONS` gate at `knowledgeMarkdown.tsx:375`; badge text at `:595` | **TRUE**; badge string is exactly `embed shown as a link` |
| `collectionPathToWorkspacePath` at `KnowledgeBacklinks.tsx:112` | **TRUE** |
| `useLibraryFileEditor.ts:84` sends no version token; nothing in the file tracks one | **TRUE** — zero matches for `version\|etag\|if-match\|revision\|token` in the whole file |
| `LibraryDownloadCard` reads `entry.size`/`entry.modified_at`, no `workspaceId` | **TRUE** |
| Ten classifier kinds, in the stated order | **TRUE** (`LIBRARY_PREVIEW_KINDS`, `libraryPreviewKind.ts:36-47`) |
| Pane-shape counts image 2 / BasePreview 5 / PDF 13 | **TRUE, exact** |
| `staleTime` 60 s (view-result) / 10 s (base-views), neither refetching on focus | **TRUE** |
| `KB_REHYPE_PLUGINS = [rehypeKatex, rehypePhosphorEmoji]`, no raw-HTML plugin anywhere in `src/` | **TRUE** |
| PDF: `new Worker(...)` per load, `PDFWorker.create({name,port})`, `once:true` error listener | **TRUE** |
| Both content PUT handlers are the only mutating handlers in `rest_library.go` that never audit | **TRUE** (route table checked handler by handler) |
| `knowledgeEdge` projects exactly nine fields; no `HeadingFound`, no `BlockID` | **TRUE** |
| `ReadLink` has ten fields and no `Embed` | **TRUE** |
| `spaContentSecurityPolicy` string, its ending, `img-src 'self' data: blob:` | **TRUE, byte-exact** |
| `resp.Truncated` set only in the neighbourhood branch; `resp.Skipped` filled before the kind switch | **TRUE** |
| Exactly eight `knowledge_*` keys in the global ceiling, all `allow`; `knowledge_link`/`knowledge_set_property` absent from ceiling, seeds and registry | **TRUE** |
| `GET /library/{ws}/content` returns no version token, in schema or header | **TRUE** |
| `docs/reference/go-implementation/` does not exist | **TRUE** |
| 77 BDD scenarios, 79 FRs — the completeness-check figures | **TRUE**, both counted |

### 2.2 Verified FALSE or materially misleading

| # | Spec claim | Reality on `def10b90e` |
|---|---|---|
| V1 | *"`EditNoteRequest` requires both `Audit AuthorAudit` and `Actor AuthorActor`"* (Symbols table) | **No validation exists on either.** `Audit` is an interface; nil silently no-ops every emit (`if a.sink != nil` guards). `Actor.AgentID` is copied into the record unchecked (`author.go:1209-1210`). The emptiness check lives in `pkg/knowledge/audit.go`'s `NewWriter`, **not** in `EditNote`. A direct `EditNote` call with a zero actor and nil audit writes the file and records nothing. → **M8** |
| V2 | *"`pkg/audit/events.go` must be extended with the `knowledge.*` event names"* (FR-091, test 73, ADR step-5 precondition #2) | **Already done.** Five names are registered — `knowledge.note.create/write/edit/rename/delete` at `pkg/audit/audit.go:324-328` — and `pkg/knowledge/audit_event_names_test.go` already asserts them **with the exact negative control the spec prescribes** (line 60). The spec inherited a **stale comment** at `pkg/knowledge/audit.go:65-70` that still says the opposite. → **M1** |
| V3 | *"The only caller is our own editor, so the breaking change costs one line there"* (Ambiguity A-1) | `putLibraryContentBinary`'s only production caller is `LibraryPdfPreview.tsx:820` — the PDF annotation save, **not** `useLibraryFileEditor`. The spec's own Impact Assessment contradicts A-1 by listing "the signature-pad and PDF-annotation save paths that use the binary door". → **C6** |
| V4 | *"a skipped note … contributes no edges while the graph reports success"* (inherited from ADR D1 stage 2, restated as datasets B5/B6) | Conflates the *containing* note with the *target*. `BuildLinkGraph` indexes `walk.Files`; a walk-level skip (`symlink`, `outside_root`, `not_addressable`) removes the file from the index, so a link **to** it resolves `unresolved` — a matching edge, not zero edges. A scan-level skip (`unreadable`) happens *after* `walk.Files` is captured, so the target stays in the index and the link **resolves**. → **C2** |
| V5 | Line citations | Five are wrong: `logLibraryAudit` "at 1404" is the doc comment (definition 1409); `Execute` "at 298" is at 283 (298 is the `unknownArgs` call); `BlockID` "at :80" is at 83 and is on `Link`, not `ResolvedLink`; the wikilink fragment strip is 476–484, not 474–479; `knowledgeEdge` is 621–**648**. `IsValidEventName` is in `audit.go`, not `events.go`. → **m2, m5** |
| V6 | *"See `docs/reference/conservative-type-design.md` for the full principle"* | That file does not exist. `docs/reference/` does not exist — which the spec itself verifies, two sections earlier, for a sibling path. → **m1** |

---

## 3. Findings

### CRITICAL

---

#### C1 — Cycle detection has no reachable input: FR-060 forbids the construction FR-056/FR-058 exist to catch

**Lens:** Inconsistency / Infeasibility
**Sections:** FR-056, FR-057, FR-058, FR-060; US-7 acceptance scenarios 4–7; BDD scenarios *"A note that shows itself"*, *"Two notes that show each other"*, *"Nesting deeper than the limit stops visibly"*, *"A note reached by two different parents"*; dataset E2–E7; tests 53, 54, 55, 56; SC-011

FR-060 (inherited verbatim from ADR D5) states:

> Embeds inside a transcluded note MUST render as the link fallback with a one-line reason.

Unqualified. The stated reason — the outer note's `kind: 'links'` graph contains only the outer note's edges — applies identically to a nested *note transclusion*, and the ADR says so: *"One graph query per transcluded note is the correct fix and it is a step-6 item."*

Therefore, in step 3 as specified:

- **A → B → A is impossible.** B's embed of A is a nested embed; it renders as a link. No second level of transclusion ever mounts, so the cycle set is never asked about a repeat.
- **A → B → C → A is impossible**, for the same reason.
- **A six-deep chain is impossible.** Depth never exceeds 1, so the five-level cap can never fire.
- The diamond case (E5) collapses to "two links".

Every US-7 scenario except AS-1, AS-2, AS-3 and AS-8 describes behaviour the product cannot produce. Tests 53–56 can only pass by constructing a render tree that production is incapable of constructing — which is precisely the deletable-subject shape the spec's own step-3 register is written against, moved one level up: the *feature*, not the test, is the thing that cannot be exercised.

The ADR is internally contradictory here and the spec inherited both halves without noticing. D5 says nested embeds are out, then two bullets later says *"a five-deep nest is five sequential fetches, each starting when the previous renders into view."*

**Fix.** Decide, explicitly, and write it as an FR:

- **Option A (recommended):** carve nested **note transclusions** out of FR-060 — a transcluded note issues its own `kind: 'links'` graph query, scoped to itself, and its *file* embeds (image/pdf/base) fall back to links until step 6. This makes cycles reachable and the cycle set meaningful. It adds one graph query per transclusion level, which must be added to FR-067's concurrency budget (which today bounds view evaluations only) and to D3's reserved-height rules.
- **Option B:** keep FR-060 as written and **move all of FR-056–FR-058, US-7 AS-4…AS-7, dataset E2–E7 and tests 53–56 into step 6**, alongside nested embeds. Ship step 3 as single-level transclusion with no cycle apparatus, and say so.

What is not acceptable is shipping a cycle detector, four tests for it, and SC-011, against an input the product cannot generate.

---

#### C2 — A skipped target still produces "Nothing in this knowledge base is named X" about a file that exists

**Lens:** Incorrectness / Incompleteness
**Sections:** FR-012, FR-013, FR-016; US-2 AS-1 and AS-4; BDD *"Scenario Outline: An embed the reader could not check"*; datasets B5, B6; tests 13, 14; SC-005

The five-state model routes to `indeterminate` only when **the graph loaded and no edge matched** (FR-013). The spec names two producers as "live today" — a skipped note (B5) and a key mismatch (B8). Measured, **both B5 and B6 are wrong**:

`BuildLinkGraph` (`pkg/knowledge/graph.go:71-93`) builds its resolution index from `walk.Files`:

```go
index: NewNoteIndex(walk.Files),
```

- A **walk-level** skip (`symlink`, `outside_root`, `not_addressable`; `pkg/knowledge/contain.go:406,425,439,465`) removes the file from `walk.Files`, so it is absent from the index. A link to it therefore **resolves as `unresolved`** — a *matching edge*, not zero edges. Under FR-013 that is the `unresolved` state, and the reader renders US-2 AS-1's sentence: *"Nothing in this knowledge base is named …"* — **about a file the reader can open in the next pane.** This is the exact falsehood the whole US-2 story exists to prevent, arriving through the door the state model does not watch.
- A **scan-level** skip (`unreadable`, `graph.go:106-155`) is appended to `g.skipped` *after* `walk.Files` was captured, so the file **stays in the index** and the link **resolves normally**. Dataset B6's expected output (`indeterminate`) is unreachable; the real behaviour is `resolved`, and the embed will attempt to render a file the indexer could not read.

So of the spec's three claimed producers, one (truncation) is admittedly unreachable, and the two claimed live are both misclassified. The only genuinely live producer is B8 (key mismatch / normalisation), whose stated reason is *"no reason available"* — meaning the `indeterminate` marker will, in practice, always say it has no reason.

**Fix.**

1. **Add a cross-check to the `unresolved` state, not only to the zero-match state.** Before rendering "nothing is named X", the resolver must consult `KnowledgeGraphResponse.skipped` — which is whole-collection and populated for every kind (`rest_knowledge.go:499-501`) — for an entry naming this target. If one is found, render `indeterminate` with the skip reason. Write this as a new FR and state the matching rule precisely (see below).
2. **State the matching rule.** For an unresolved edge, `to_path` is *"the normalised link text"* (`KnowledgeGraphEdge.yaml`), not a real path, while `KnowledgeGraphSkip.path` is a real collection-relative path. The spec says only *"a skip entry naming this embed's target"* and never says whether the comparison is on full path, basename, or normalised text. Two competent engineers will implement different things.
3. **Correct datasets B5 and B6** to their measured behaviour, and add a dataset row for the real dominant case: *target removed from the index by a walk-level skip → currently `unresolved` → must be `indeterminate`*.
4. Test 13 must produce all five states from **evidence shapes the server can actually emit**, and the register's deletable-subject note must be extended to say that a fixture built from the spec's current B5/B6 rows tests nothing.

---

#### C3 — The wire cannot tell "no such file" from "outside the knowledge base", so FR-017 is unimplementable and no contract change is sequenced for it

**Lens:** Infeasibility / Insecurity (Information Disclosure)
**Sections:** FR-017; US-2 AS-7; BDD *"An embed pointing outside the knowledge base is refused without echoing the path"*; dataset B12; test 17; the Contract-First table (CW-1…CW-3)

`KnowledgeGraphEdge` has **no `reason` field**. Its only resolution signal is the `resolution` enum, whose own description states:

> `"unresolved"` means no target matched, **or** the target lay outside the collection root — in which case the target was NOT read (FR-043).

The two cases are collapsed into one value, deliberately, at the schema level.

Consequences:

- **Dataset B12** — *"matching edge; resolution `unresolved` with reason `outside_root`"* — describes a payload the contract forbids. `additionalProperties: false`; there is no `reason` property. The fixture cannot be built against the generated type.
- **FR-017** cannot be satisfied: the reader has no way to know it must say *"this embed points outside the knowledge base"* rather than *"nothing is named that"*. It will render the missing-file sentence for a containment refusal — a *security-relevant* wrong message, because it tells the reader the escaping target does not exist rather than that it was refused.
- **Test 17** (`outside-root-refused-without-echoing-path`) is a pure negative assertion (*"the refusal text does not contain the path"*), which passes trivially when the marker is the ordinary missing-file marker. It cannot detect the defect it is named for.

The spec sequences three contract changes and lists a possible fourth. **This is a fifth**, and it is a prerequisite of a P0 story.

**Fix.** Add to CW-2 (the graph-edge change, already open for `heading_found` and `block`) a field that distinguishes the refusal — either an `unresolved_reason` enum (`no_match` / `outside_root`) or, preferably, reuse the existing vocabulary by adding the responsible `KnowledgeGraphSkip.reason` value. Populate it in `rest_knowledge.go::knowledgeEdge`, and pair the test: one edge unresolved for absence, one unresolved for containment, asserting **different** markers. Then rewrite B12 against the real shape.

---

#### C4 — `LibraryFileRef` cannot be produced by the resolver: `classifyLibraryEntry` reads two fields no graph edge carries

**Lens:** Infeasibility / Incorrectness
**Sections:** *Conservative Type Design*; FR-025, FR-027, FR-028, FR-029; datasets C10–C13; tests 19, 20, 25–27; SC-003, SC-004

The spec (following ADR D2a) introduces

```
LibraryFileRef = Pick<LibraryEntry, 'name' | 'path' | 'mime' | 'is_text_editable'>
```

and justifies excluding `size`/`modified_at` on the grounds that *"the resolver genuinely cannot supply"* them and inventing them is the cardinal error.

**The same is true of `mime` and `is_text_editable`, and the spec does not notice.** Both are server-sniffed values on `LibraryEntry`; `is_text_editable` is a *required* property of that schema. Neither is on `KnowledgeGraphEdge`, and there is **no single-entry GET** in the Library API (`listLibraryWorkspaces`, `listLibraryEntries`, `getLibraryContent`, `downloadLibraryFile`, … — no `getLibraryEntry`), so the resolver cannot fetch one without a directory listing per embed target's parent.

This matters because `classifyLibraryEntry` **reads both**:

```
libraryPreviewKind.ts:76   const mime = (entry.mime ?? '').toLowerCase()
libraryPreviewKind.ts:91   if (entry.is_text_editable) return 'text'
```

So an inline embed classified from a fabricated ref will classify **differently from the pane** for exactly the kinds the spec routes to the link fallback. Dataset **C13** ("a file with no extension → kind `other` → link fallback") is the case: in the pane, the server's `is_text_editable` would make it `text`. The spec's expected output is correct only for a ref whose `is_text_editable` was invented as `false`.

That breaks FR-027 ("exactly one renderer per kind, shared between pane and inline"), FR-028 ("variant may change layout only — never which states exist"), and silently invalidates the cross-variant tests 25–27, which hold props fixed and would never see a classification divergence that happens *upstream* of the renderer.

SC-004 (`npm run typecheck` exits 0) does not catch this: a fabricated `mime: ''` and `is_text_editable: false` typecheck perfectly.

**Fix.** Choose and write it down:

1. **Narrow the type honestly** to `Pick<LibraryEntry, 'name' | 'path'>` and add an FR stating that inline classification is **extension-only**, plus an FR and a test asserting the divergence from pane classification for the three kinds where `mime`/`is_text_editable` decide it — or
2. **Fetch the entry**, and then budget it: one request per embed, a query key, a place in FR-067's concurrency bound, and a reserved-height story while it is in flight — or
3. **Extend the graph edge** with the server's classification (a fifth contract change).

Whichever is chosen, add a test that mounts the *same file* in the pane and inline and asserts the classifier returned the same kind — the property FR-027 claims and nothing currently checks.

---

#### C5 — US-10 (inline record editing) is not implementable on the current wire, and CW-3 omits every contract change it actually needs

**Lens:** Infeasibility / Incompleteness / Insecurity (Tampering)
**Sections:** FR-085, FR-086, FR-088, FR-089, FR-092; US-10 AS-1, AS-3, AS-5; BDD *"An editor is offered only for field types the record's definition describes"*; tests 70, 71, 75, 76; CW-3; §4.6 of the ADR; SC-019, SC-020

Four independent blockers, none named in the spec.

**(a) A view cell carries no type information.** `VaultFindCell` is, in full:

```yaml
required: [property, value]
properties:
  property: {type: string}
  value:    {type: string}   # "The rendered value" — always text
```

There is no declared type, no enum member list, no date/text discriminator, and nothing marking a cell as **derived** or as a **relation**. FR-088 ("an editor MUST be offered only for field types the record's own definition describes as editable") cannot be evaluated by the client. Test 75 has no data to drive it.

**(b) There is no REST route for record schemas.** `RecordSchema.yaml` and `VaultRecord.yaml` are declared in `openapi.yaml`'s `components` (lines 342, 356) and referenced from **no path**. The whole ADR-068 typed-record layer is agent-tool-only on the wire. Obtaining field declarations requires a new operation — a contract change CW-3 does not include.

**(c) A view row carries no version token.** `VaultFindRow` has `id`, `path`, `title`, `cells`, `joins`, `stale` — no `version_token`. FR-085/FR-086's compare-and-swap needs one per editable row. Either the row schema gains it (contract change) or the SPA reads each record before each edit (unbudgeted N requests, and a fresh TOCTOU window between the read and the write).

**(d) The chosen write path bypasses the guards ADR-068 built.** A typed record-write contract **already exists**: `RecordWriteRequest` — `version_token` required on update, splice-not-reserialise, schema validation with the expected shape named, and two explicit prohibitions in its own description:

> RELATIONS AND PERSON PROPERTIES ARE NOT WRITABLE HERE … Derived values are never written into frontmatter (D9, FR-046) — a request naming a derived property is rejected, not honoured.

FR-085 instead routes through `EditNote` + `SetProperty` (`author.go:830`), a raw frontmatter line-splicer with **none** of those guards. Combined with (a), the SPA cannot tell a derived or relation property from an ordinary one, so a dashboard will offer an editor for one, and the write will land through the path that does not refuse it. That is a data-integrity regression against a rule ADR-068 established deliberately.

`SetProperty` is also scalar-only by signature (`func SetProperty(key, value string) NoteEdit`) — the spec's stated "list fields get no editor" limitation is an artefact of this choice, not an inherent one: `SetPropertyList` and `SetPropertyScalarChecked` exist in `knowledge_edit_list.go`.

**Fix.** Before US-10 can be planned:

1. Decide whether the record-field write goes through **`RecordWriteRequest`'s existing semantics** (recommended — it already has the token, the guards and the splice) or through `SetProperty`, and if the latter, state which ADR-068 guarantees are being given up and why.
2. Enumerate the *real* contract changes in the CW table: a record-schema read operation, a version token reachable per row, and cell type/derived/relation metadata — or an explicit design that avoids each.
3. Add an FR and a test: **a derived property and a relation property MUST NOT be offered an editor**, asserted positively (a writable enum in the same fixture is editable) so the test cannot pass on a component that renders no editors at all.

---

#### C6 — CW-1 is incomplete, and FR-001 as written breaks PDF annotation saving

**Lens:** Incompleteness / Inconsistency / Infeasibility
**Sections:** CW-1; FR-001, FR-004, FR-005; US-1 AS-1 and AS-5; Ambiguity A-1; datasets G1–G7; tests 1–8; SC-001

Three defects, one of them a shipped-feature regression.

**(a) The response schemas are missing from CW-1.** The table lists `LibraryContentRequest.yaml`, `LibraryBinaryContentRequest.yaml`, a new `LibraryConflictError.yaml`, and 409s on the two operations. It does **not** list:

- `LibraryContentResponse.yaml` — measured: `{path, content, size, is_text, too_large, mime}`, no version field, and `handleLibraryContentGet` sets **no headers at all**. Without a token on the read, FR-004's *"the editor MUST send back the version token it received"* has nothing to receive.
- The **PUT response**. `putLibraryContent` returns `LibraryEntry`; BDD scenario 1 requires *"the response carries the file's new version token"* and test 3 is named `…SucceedsAndReturnsNewToken`. `LibraryEntry` has no such field.

Both are wire formats under Constraint #8 and must be in the CW table.

**(b) The binary door's only production caller cannot supply a token.** `putLibraryContentBinary` has exactly one non-test caller: `LibraryPdfPreview.tsx:820`, the PDF annotation save. It loads the document through `libraryDownloadUrl` — a byte stream — and never calls `fetchLibraryContent`, so **there is no read on its path that could return a token**, even after (a) is fixed. FR-001 makes the token mandatory with a 400; the annotated-PDF save therefore starts failing with HTTP 400 on merge.

The spec contradicts itself here: Ambiguity A-1 argues *"The only caller is our own editor, so the breaking change costs one line there"*, while its own Impact Assessment lists *"the signature-pad and PDF-annotation save paths that use the binary door"* as d=2 consumers.

No test covers it. Test 4 is server-side only; there is no `LibraryPdfPreview` conflict test in the plan, and the Regression table does not list `LibraryPdfPreview.test.tsx` (which exists, 34,979 bytes, with an explicit save round-trip test at line 739 that will break).

**(c) `LibraryEntry` is not the right conflict carrier and A-2's fix is under-scoped.** A-2 correctly rejects reusing `KnowledgeConflictError` — note additionally that `KnowledgeConflictError` is presently referenced from **no path** in `openapi.yaml`, so CW-3 will be its first wiring.

**Fix.** Extend CW-1 to: `LibraryContentResponse` gains a version token; the two PUT responses carry the new token; `LibraryConflictError.yaml` added; both operations gain 400 and 409. Add an FR and a test for the PDF path: it must obtain a token (a metadata read, or a token on `downloadLibraryFile`) and send it, and the existing `LibraryPdfPreview.test.tsx` save test must be listed in the Regression table.

---

#### C7 — Step 0 specifies a compare-and-swap with no lock, so the lost update it exists to close remains open

**Lens:** Incorrectness / Insecurity (Tampering)
**Sections:** FR-001, FR-002; US-1 AS-2; datasets G2, G7 (and the absence of a G10 equivalent); tests 1–4; SC-001

FR-002 requires the Library PUT to reject a stale token. It does not require that the read-compare-write sequence be **atomic with respect to the agent path**.

The agent path takes a lock: `EditNoteRequest.Lock NoteLockConfig` (`author.go:620`), described in the code as *"D14 tier 1 mutual exclusion"*. `handleLibraryContentPut` takes nothing. A handler that (i) hashes the file, (ii) compares to `expect_version`, (iii) writes, with an agent's `EditNote` running between (ii) and (iii), silently loses the agent's write — the exact failure the token exists to detect, now with a check in front of it that returns 200.

The test plan reflects the gap: dataset **G10** (*"Two writes in flight, same token → exactly one succeeds"*) exists only for the **record-field** write. The Library door has G2 and G7 (sequential stale-token cases), both of which pass against a lock-free implementation.

**Fix.** Add an FR: *the whole-file save MUST perform its version comparison and its write under the same lock the agent path uses, so a concurrent `EditNote` cannot interleave.* Add a dataset row and an integration test that starts an `EditNote` between the handler's compare and its write (a seam or a `sync` hook) and asserts exactly one write survives. Without it, step 0 ships a lock-free CAS and the tests will report green.

---

### MAJOR

---

#### M1 — FR-091 and test 73 describe work that is already done, and the test cannot fail

**Lens:** Incorrectness / test that cannot fail
**Sections:** FR-091; step-5 register row *"An event-name recogniser that accepts anything"*; test 73; the ADR's step-5 precondition #2

`pkg/audit`'s `IsValidEventName` **already recognises** `knowledge.note.create`, `.write`, `.edit`, `.rename` and `.delete` (`pkg/audit/audit.go:324-328`), and `pkg/knowledge/audit_event_names_test.go` already asserts the two lists agree **and already carries the negative control the spec's register demands**:

```go
assert.False(t, audit.IsValidEventName(audit.EventName("knowledge.note.not_a_real_event")))
```

So test 73 duplicates a passing test and will be green on day one with no implementation. The ADR's step-5 precondition — *"a change to a package outside `pkg/knowledge`'s ownership … must be sequenced as its own item with `security-lead` or `backend-lead` ownership"* — is work that does not exist.

The source of the error is a **stale comment** at `pkg/knowledge/audit.go:65-70` which still says *"pkg/audit's IsValidEventName does not yet know them, so today each one triggers audit's warn-once 'unknown event name' log."* The ADR read the comment; the spec read the ADR. This is precisely cross-cutting risk **X6** ("a measured claim that was read rather than run") occurring inside the document that defines X6.

**Fix.** Delete FR-091, test 73 and the step-5 precondition. Replace with a one-line task: *correct the stale comment at `pkg/knowledge/audit.go:65-70`.* If the new REST write emits a **sixth** event name, then FR-091 becomes real for that one name only — say which name, and keep the negative control.

---

#### M2 — The CSP change breaks the existing test's oracle, and two artefacts are missing from the impact list

**Lens:** Incompleteness / Infeasibility
**Sections:** FR-079, FR-080, FR-081; tests 59–62; the Impact Assessment CRITICAL row; step-4 register

Measured, `TestSpaServedWithCSP` does **not** compare against a literal in the test. It reads its oracle out of a specification document:

```go
raw, err := os.ReadFile(specDocRelPath)   // ../../docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md
re := regexp.MustCompile(`(?m)^default-src 'self';.*$`)
require.Len(t, matches, 1, "§10.7 must contribute exactly one literal policy line to the spec")
```

Two consequences the spec misses:

1. **`docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` §10.7 must be edited in the same change.** It is not in the Impact Assessment (which names only `embed_csp_test.go`, `csp-assumptions.spec.ts` and the csp-audit doc) and not in any test row. Miss it and CI fails on the byte-for-byte assertion.
2. **FR-081's operator config key is structurally incompatible with that oracle.** A policy string that varies per install cannot be pinned to *"exactly one literal policy line"* in a document. Either the oracle becomes "the shipped default with the default allow-list", or the test must be restructured. The spec's tests 59–62 assume a constant and say nothing about it. This is the same collision Ambiguity A-3 identifies on the *reader* side and does not follow through to the *test* side.

Also unlisted: `spaPdfWorkerContentSecurityPolicy = withWasmCompilation(spaContentSecurityPolicy)` (`embed.go:260`), a derived second policy served on the PDF worker path. Adding a `frame-src` host propagates into it; FR-079's "exactly the allow-listed frame hosts and nothing else" needs to say whether that is intended there.

One thing the spec gets right and should keep: `TestSpaCsp_DirectiveFloor` already carries a seven-row self-mutation half with a drift guard (`require.NotEqual(t, base, m.policy, …)`). Test 60's instruction to extend rather than relax it is correct and enforceable.

**Fix.** Add the ADR-067 spec doc to the Impact Assessment and to the step-4 exit criteria. Add an FR fixing what the byte-for-byte oracle becomes once the policy is configurable, and a test that the **default** install's served header equals the documented default while a **configured-empty** install's does not — with both assertions in the same test, so neither can pass alone.

---

#### M3 — `heading_found` is always false for `.base` targets and for block references, so the "no such heading" marker will fire on all 75 dashboard embeds

**Lens:** Incorrectness
**Sections:** FR-035, FR-016; US-2 AS-3; dataset B13; test 39; SC-007

`HeadingFound` has exactly one assignment site (`pkg/knowledge/graph.go:169-173`):

```go
res := g.index.Resolve(p.note, l)
if res.State == ResolveResolved && res.Heading != "" {
    if h, ok := findHeading(g.headings[res.To], res.Heading); ok {
        res.HeadingFound = true
```

`g.headings` is populated **only for markdown files** (`if !IsMarkdownPath(rel) { continue }`, `graph.go:96`). Therefore:

- For a `.base` target — every one of the founder's 75 dashboard embeds — `g.headings[to]` is nil, `findHeading` returns false, and `heading_found` is **false**. A reader that keys the "no such heading" marker off that field will render it on the entire dashboard.
- For a **block** reference (`![[Note#^abc]]`), the parser sets `BlockID` and leaves `Heading` empty, so the guard `res.Heading != ""` never fires and `heading_found` stays false. Same false marker.

Test 39 (`TestKnowledgeEdge_HeadingFoundTrueAndFalse`) exercises markdown headings only, so it passes while both false-marker paths ship.

**Fix.** Add an FR: *`heading_found` is meaningful only when the target is markdown **and** a heading fragment was written; the reader MUST NOT consult it for a `.base` target or for a block reference.* Extend test 39 with two more rows — a `.base` target with a view fragment, and a markdown target with a block anchor — each asserting the "no such heading" marker is **absent**. Consider stating the constraint in `KnowledgeGraphEdge.yaml`'s own description so a future consumer cannot re-learn it the hard way.

---

#### M4 — The register's negative-assertion rule is stated once and applied to one test; at least six others have the identical shape

**Lens:** Test coverage gap (the brief's area 1)
**Sections:** step-1 register deletable-subject warning; tests 11, 14, 17, 21, 22, 44, 62, 76

The step-1 register makes exactly the right observation about test 14:

> a negative assertion; a component that renders *nothing at all* also never says "no such file". It must be paired, in the same test, with a positive assertion.

That rule is then applied to **one** test. These have the same shape and are unpaired:

| Test | The assertion | Passes when… |
|---|---|---|
| 11 `external-url-recognised-before-graph` | *"never consults the graph and never reports a miss"* | the external form is not recognised at all and nothing renders |
| 17 `outside-root-refused-without-echoing-path` | *"refusal text asserted **not** to contain the escaping path"* | the marker is the ordinary missing-file marker (which is what C3 shows will actually happen) |
| 21 `no-download-card-reachable` | *"the download card is asserted absent from every embed path"* | the embed pipeline is deleted |
| 22 `embed-in-code-fence-mounts-nothing` | *"no renderer is mounted, no marker of any kind is drawn"* | the whole embed feature is reverted |
| 44 `machine-name-never-constructed` | *"asserts no derived name is emitted for an unmatched label"* | the reader emits nothing |
| 62 `TestSpaCsp_OperatorCanDeclineTheHost` | *"empty setting → no external host in the served policy"* | the host was never added |
| 76 `title-and-path-never-editable` | *"both asserted absent"* | no editors render anywhere |

**Fix.** Promote the rule from a one-off warning to a **register-wide invariant**: *every negative assertion in this plan must be paired, in the same test body, with a positive assertion that the intended output IS present.* Then add the pairing to each row above — e.g. test 22 renders the same embed inside a fence and outside it in one document and asserts the outside one mounts; test 62 asserts the default-on policy contains the host and the emptied one does not, in the same test; test 76 asserts a status cell IS editable in the same fixture in which title and path are not.

---

#### M5 — Six tests get no false-green analysis at all, breaking the register's own completeness claim

**Lens:** Incompleteness
**Sections:** *Test Implementation Order — continuation* (tests 91–96); *False-Green Risk Register — one entry per phase*

Tests 91–96 were, by the spec's own admission, *"derived during the traceability pass"* and appended after the register was written. None appears in any phase's risk table. Two of them are exactly the shapes the register exists to catch:

- **95** `states-link-rendering-differs-from-the-pane` — asserts a UI string exists. Trivially satisfiable, trivially satisfiable *wrongly*, and its subject (FR-049) is a sentence, not a behaviour.
- **91** `outline-fetched-only-when-heading-missing` — *"the outline request count is 0 on every path except the missing-heading case"*. The zero half passes with the outline fetch never implemented; the `1` half is the load-bearing one and is stated only in passing.
- **94** `resolves-against-the-readers-graph` — the whole point is that a link **absent from the loaded rows** renders resolved. If the fixture's link is present in the rows, both resolvers agree and the test proves nothing.

**Fix.** Fold 91–96 into their phases' register tables with the risk and the catching assertion named, as every other test has. Add the register's own completeness assertion to the document: *every numbered test appears in exactly one register row.*

---

#### M6 — The source guard has no self-test, against this repository's own convention

**Lens:** Test coverage gap
**Sections:** test 81 `check-no-duplicate-renderer.sh`; step-6 register

The spec correctly flags that a source-text guard is normally forbidden here and justifies this one narrowly. But it does not require the guard to be *proven capable of failing* — and this repository already has the convention:

```
scripts/check-browser-tests-gated.sh      + check-browser-tests-gated.test.sh
scripts/check-no-handwritten-wire-types.sh + check-no-handwritten-wire-types.test.sh
scripts/check-no-tool-error-from-status.sh + check-no-tool-error-from-status.test.sh
scripts/check-no-removed-providers.sh     + check-no-removed-providers-selfcheck.sh
```

A grep guard with a typo'd pattern exits 0 forever. That is the 673/673 failure verbatim.

**Fix.** Require `scripts/check-no-duplicate-renderer.test.sh` alongside it, following the existing pattern: plant a duplicate definition in a temp tree, assert non-zero exit; remove it, assert zero. Add it to the test order table as its own row.

---

#### M7 — Two security-relevant acceptance clauses and one whole BDD scenario have no test

**Lens:** Incompleteness / Insecurity
**Sections:** US-3 AS-3; US-9 AS-4; BDD *"An SVG embed is drawn as a picture and runs none of its scripts"*, *"Hand-written frame markup in a note stays inert"*; FR-031, FR-075; tests 19, 63, 64

- **US-3 AS-3 / FR-031.** The BDD scenario asserts three things: drawn in an image element, **the script does not execute**, and **the SVG is not injected inline**. The traceability matrix maps FR-031 to test **19** only, which asserts which renderer mounted. The two security clauses — the reasons the ADR spends a paragraph on this — are untested.
- **US-9 AS-4.** *"Hand-written frame markup in a note stays inert"* has its own BDD scenario and dataset row (F9) but **no test in the order table**. FR-075 maps to tests 63 and 64, neither of which touches raw markup. The property rests entirely on `KB_REHYPE_PLUGINS` having no raw-HTML plugin — true today, and a one-line change away from not being true, with no guard.

**Fix.** Add a test asserting an SVG containing `<script>` renders inside `<img>`, that the script's side effect did not occur, and that no `<svg>` element appears in the note's DOM. Add a test that a note whose source contains `<iframe src="https://…">` produces no `<iframe>` in the rendered output. Both are cheap and both protect a stated non-behaviour.

---

#### M8 — `EditNoteRequest` does not enforce the actor, so FR-090's "never empty" has no named enforcement point

**Lens:** Incorrectness / Insecurity (Repudiation)
**Sections:** Symbols table (`EditNote` / `SetProperty` row); FR-085, FR-090, FR-093; test 72

The spec's evidence table states *"`EditNoteRequest` requires both `Audit AuthorAudit` and `Actor AuthorActor`"*. Measured, it requires neither:

- `Audit` is an interface; `newAuthorAuditor` stores it and every emit is guarded `if a.sink != nil`. **A nil `Audit` writes the file and records nothing, with no error.**
- `Actor.AgentID` is copied straight into the record (`author.go:1209-1210`) with no emptiness check.
- `EditNote`'s only precondition is `if c == nil`.

The emptiness guarantee lives one layer up, in `pkg/knowledge/audit.go`'s `NewWriter`, whose own header says so. FR-085 instructs the handler to *"go through the same lock, version compare-and-swap, atomic write and audit path an agent's write uses"* — which, taken literally as "call `EditNote`", **bypasses the only place the actor is checked**.

Test 72 would catch a missing record, so this is not undetected — but the spec's design rationale rests on a structural guarantee that does not exist where it says it does, and an implementer reading the Symbols table will not wire `NewWriter`.

**Fix.** Correct the Symbols table. Add an FR naming the enforcement point explicitly: *the REST record-field write MUST construct its writer through `pkg/knowledge/audit.go`'s `NewWriter`, which is the only place an actor with neither an agent nor a user is rejected.* Add an integration test asserting the handler refuses a request it cannot attribute **before** touching the file (distinct from FR-093's bypass-503 case, which is about there being no user at all).

---

#### M9 — The agent surface still echoes the escaping path that FR-017 forbids on the reader surface

**Lens:** Inconsistency / Insecurity (Information Disclosure)
**Sections:** FR-017, FR-020; US-2 AS-7 and AS-8; test 93

FR-017 requires the reader's marker not to contain an escaping path. D-B requires the same honesty on the agent surface. But `renderReadLinks` prints, for any unresolved link:

```go
fmt.Fprintf(b, "  %s (unresolved) %s", arrow, l.Form)
```

`l.Form` is the link **as written**. For `![[../outside/secret.md]]` the whole escaping path reaches the model verbatim, today, and FR-020's change (marking embeds) preserves it. The spec applies the redaction rule to one surface and is silent on the other.

**Fix.** Either extend FR-017 to the agent surface with an FR and a test row on test 93 (*a containment-refused embed's rendered `LINKS` line contains no path segment outside the collection*), or state explicitly why the two surfaces differ — an agent is a trusted reader of the note's own source, which is a defensible answer but must be written down rather than left as an oversight.

---

#### M10 — 60+ new SPA test files, no file paths, and a hardcoded CI matrix that silently skips uncovered directories

**Lens:** Inoperability / Test coverage gap
**Sections:** cross-cutting risk X1; the entire Test Implementation Order; SC-025

X1 names the right guard. But `scripts/check-vitest-coverage.mjs` does not *add* coverage — it fails when a test file matches no pattern in the **hardcoded vitest matrix inside `.github/workflows/pr.yml`**. Its own header records the incident: 116 of 422 files (27%) never ran while CI was green.

The spec names 96 tests as bare filenames — `embedResolver.test.ts`, `embedNotation.test.ts`, `baseViewMatch.test.ts`, `youtubeEmbed.test.tsx`, `transclusion.test.tsx`, `embedLazyMount.test.tsx`, `recordFieldEditor.test.tsx`, `embeddedViewState.test.tsx`, `wireContracts.test.ts`, `knowledge-embeds.spec.ts` — with **no directory for any of them**, and never lists `.github/workflows/pr.yml` as a file this work must change.

**Fix.** Give every new SPA test file a path in the order table. Add `.github/workflows/pr.yml` to the Impact Assessment. Add a step-1 exit criterion: *`scripts/check-vitest-coverage.mjs` exits 0 and the run's own output names each new file* — which SC-025 half-states and should state fully.

---

#### M11 — Step 0 leaves three more unversioned doors into knowledge notes, and the spec repeats the ADR's "one compare-and-swap" claim unqualified

**Lens:** Incompleteness / Incorrectness
**Sections:** US-1 narrative; FR-001–FR-005; *Assumptions*; ADR §7.1 (inherited)

Measured, `rest_library.go`'s mutating routes are: `DELETE entries`, `PUT content`, `PUT content-binary`, `POST upload`, `POST mkdir`, `POST vaults`, `POST rename`, `POST move|copy`. Step 0 adds a version check to **two** of them. `deleteLibraryEntry`, `uploadLibraryFiles` and the transfer modes remain version-free, and all three can destroy or replace a knowledge note an agent is mid-write on. (Rename and transfer at least refuse an existing destination with 409; delete and upload do not.)

The spec's US-1 narrative is careful — it says other mutations are *"recorded"*, which is accurate. But the ADR's §7.1 claim it implements — *"every knowledge-base write from every surface keeps sharing one lock, one compare-and-swap and one audit record"* — will still be false after step 0, and nothing in the spec says so.

**Fix.** Add a Residual/Assumptions row naming the three remaining unversioned doors by handler name, and state that the safety claim is scoped to whole-file content writes. If the founder wants them closed, that is a separate, sized item — not something to leave implied.

---

#### M12 — A-3's fourth contract change is stated as conditional inside a section that opens "There are three"

**Lens:** Inconsistency / Constraint #8
**Sections:** *Contract-First Work*; Ambiguity A-3; FR-081; tests 59, 62

The section header asserts three contract changes; four paragraphs later the same section says *"A fourth may be required."* It is not conditional. FR-081 mandates an operator config key; FR-080 mandates the reader's allow-list equal the served policy's; the reader cannot learn a runtime value from a build constant. Constraint #8 does not admit a "may be" — the contract must land before the code.

Combined with C3's fifth change and C5's additional ones, the real count is **at least six**, and the sequencing table is the artefact that determines merge order.

**Fix.** Promote A-3 to **CW-4** with concrete files (the settings/state payload schema), an owner and a "must land before" cell. Re-count the section header. Add CW-5 (C3's unresolved-reason field, folded into CW-2 if convenient) and CW-6…n for US-10 once C5 is settled.

---

#### M13 — The five-state model silently drops the one existence signal today's code actually uses

**Lens:** Incompleteness / regression risk
**Sections:** FR-011, FR-012; *Symbols Involved* (`resolveEmbedUrl` row); Regression table row 2; test rows for step 1

`resolveEmbedUrl` today does not stop at the edge. It checks the node:

```ts
const node = graph.nodes.find((n) => n.path === edge.to_path)
if (node && node.exists === false) return undefined
```

`KnowledgeGraphNode.exists` is a **required** field whose description says *"False for the target of an unresolved link. The client MUST mark such a node visibly and MUST NOT navigate on click (FR-065)."*

The spec's Symbols table quotes only the edge match and never mentions `nodes[].exists`. FR-011 defines the key as embed-flag + target + fragment + block, and FR-012's five states are derived from `resolution` and match count alone. An implementer following the spec will extend the edge lookup and drop the node check — losing a live guard and, with C2/C3, losing the one place today's code refuses to hand a download URL for a non-existent target.

**Fix.** Add `nodes[].exists` to FR-011's resolution procedure explicitly, state how it interacts with `resolution === 'unresolved'` (they should agree; if they ever disagree that is itself an `indeterminate` signal), and add a regression test row: *an existing single-image note still renders, and a note whose target node reports `exists: false` still does not.*

---

### MINOR

- **m1 — Broken reference.** *Conservative Type Design* cites `docs/reference/conservative-type-design.md`. Neither the file nor `docs/reference/` exists — which the spec itself verifies for a sibling path two sections earlier. Remove the citation or inline the principle.
- **m2 — Line-number citations, five of them wrong**, in a repository whose CLAUDE.md explicitly says to cite `file::symbol` rather than `file:line` because line numbers go stale within days. Wrong: `rest_library.go:1404` (comment, not a call — definition is 1409), `knowledge_edit.go:298` (`Execute` is at 283), `links.go:80` (`BlockID` is at 83, and on `Link`, not `ResolvedLink`), `links.go:474-479` (the fragment strip is 476–484), `rest_knowledge.go:621-647` (621–648). Convert the Symbols table to `file::symbol`.
- **m3 — `IsValidEventName` is in `pkg/audit/audit.go:134`, not `events.go`.** Both the ADR and the spec name the wrong file. `events.go` holds only constants.
- **m4 — FR namespace collision.** The spec's FR-090 ("a person's write MUST be audited"), FR-106 ("the link-with-a-badge fallback MUST continue to fire") and neighbours collide with ADR-067's FR-090/FR-106/FR-107, which are cited **in production code comments** (`pkg/knowledge/author.go:2`, `author_test.go:11,541,585,618`). Per-spec numbering is the local convention, but this spec sits directly adjacent to ADR-067's surface. Prefix them (e.g. `E-090`) or start at a non-colliding base.
- **m5 — Test 38 duplicates existing coverage.** `TestCatalog_MatchesGlobalCeilingEntryForEntry` already asserts the ceiling matches `allStaticToolNames` entry for entry, and `constructor_seed_test.go:229` already asserts every per-agent seed's key set `ElementsMatch`es the catalogue. Test 38's set-equality reasoning is sound; note in the register that it is a second net over an existing one, not the only one.
- **m6 — An out-of-scope collection returns 200 with empty edges and empty skips** (`rest_knowledge.go:474-476`), which under FR-013 produces N `indeterminate` markers with "no reason available" rather than one page-level statement. Add a dataset row and decide whether that deserves the page-level treatment FR-014 gives an outright failure.
- **m7 — US-10 AS-1's second clause is untested.** *"every other place showing that record updates"* maps to test 70 (which asserts the write path) and test 79 (which asserts invalidation is scoped). No test asserts that **two embeds of the same view both show the new value**, which is the user-visible half of the requirement.
- **m8 — FR-070 states the print guarantee unconditionally** while A-9 argues it may be unachievable through the browser's native print event. An implementer reading only the FRs will build the unachievable version. Split FR-070 into the application-control guarantee and the native-path notice, matching A-9's own recommendation.
- **m9 — A-5's ruling lives only in the ambiguity table.** *"A toolbar is chrome and is hover/focus-gated; an editable cell is content and stays always active"* is the correct call and is not expressed as an FR. FR-046 says the controls hide at rest and FR-088 says editors are offered; an implementer applying FR-046 to the whole embed will hide the editors, making D-D invisible on the surface D-D names. Add it to FR-046 or as a new FR. Same for A-4's touch path.

### OBSERVATION

- **O1 — Constraint #6's zero-line diff is true, and worth reframing.** Verified: exactly eight `knowledge_*` keys in the ceiling, all `allow`; `knowledge_link`/`knowledge_set_property` absent from the ceiling, from every per-agent seed, and from the registry (`AuthoringTools()` has zero production callers — dead code). Four coupled Go catalogs exist (`allStaticToolNames`, the ceiling, `buildKnownBuiltinToolNames`, the per-agent seeds), mutually enforced by tests; **no contracts enum and no SPA list of tool names exist**, so an `op` on an existing tool needs no entry anywhere. The claim holds. But part of *why* it holds is that the new REST record-field write (CW-3) sits **outside the tool-policy system entirely** — an operator who has set `knowledge_edit: deny` for every agent still gets a human write door into the knowledge base that no policy can close. The spec's "accepted cost" paragraph discusses only agent granularity. Worth stating as an ungoverned surface rather than presented purely as tidiness.
- **O2 — `BlockID` is parsed and never read.** `links.go:480` and `:567` write it; nothing anywhere reads it — no resolution logic, no test, no wire type. CW-2 would be its first consumer, so there is no existing behaviour to regress, and equally no existing test to lean on. Test 40 is genuinely the first assertion this field will ever have.
- **O3 — "Ship steps 0–5 and stop" remains the right default.** Zero measured uses across 784 notes for every step-6 kind, and C1's resolution may well move step 3's cycle work into step 6 as well. The spec records this option correctly; keep it visible when the sequencing is revised.

---

## 4. Structural integrity results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** — 12 stories, all with scenarios |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL (2)** — US-9 AS-4 (hand-written frame markup) has a BDD scenario but no test; US-3 AS-3's script/inline clauses are not carried into any scenario's assertions. See M7. |
| Every BDD scenario has a `Traces to:` back-reference | **PASS** — all 77 |
| Every BDD scenario has a corresponding test | **FAIL (1)** — *"Hand-written frame markup in a note stays inert"* appears in no row of the order table (M7) |
| Every functional requirement appears in the traceability matrix | **PASS** — 79 FRs, 79 rows |
| Every BDD scenario appears in the traceability matrix | **PASS** (via grouped rows, as the spec's own completeness note states) |
| Test datasets cover boundary, edge and error conditions | **PASS in form, FAIL in content** — datasets are thorough (A1–A13, B1–B14, C1–C14, D1–D9, E1–E10, F1–F12, G1–G12, H1–H12, I1–I10) but B5, B6, B12 and C13 describe states the system cannot produce (C2, C3, C4), and E2–E7 describe a construction FR-060 forbids (C1) |
| Regression impact explicitly addressed | **PASS with a gap** — the regression table is good and every test file it names exists; it omits `LibraryPdfPreview.test.tsx`'s save round-trip, which FR-001 breaks (C6) |
| Success criteria measurable, no subjective language | **PASS** — 25 SCs, all with counts or exit codes. SC-004's control-run instruction is exemplary. |
| Contract-first sequencing complete (Constraint #8) | **FAIL** — CW-1 is missing two response schemas (C6); C3 needs a fifth change; C5 needs several more; A-3's fourth is stated as conditional (M12) |
| Tool-policy claim (Constraint #6) | **PASS** — independently verified; see O1 for the reframing |

---

## 5. Test coverage assessment

**What is unusually strong and should survive revision:**

- The false-green register's *structure* — one entry per phase, each naming the specific shape of a passing-but-meaningless test — is the best artefact in the document. The step-3 paragraph on the depth cap doing the loop detector's job is exactly right (and is what led to C1).
- Insisting on **pairs**: `heading_found` true *and* false (test 39); the validator probed with an accept *and* a reject (test 41); the retry counted before, after the window, *and* after the press (tests 8, 77).
- Insisting on **counts over clocks** (tests 84, 86) with the two prior incidents cited.
- Requiring **real boot wiring** for audit assertions (tests 5, 6, 72), with the two existing precedents named — and both precedent files verified to exist.
- The two-levels-below-root fixture with a decoy (test 16), and the fifteen-embeds-five-files count (test 50). Both are fixtures that cannot pass on a deleted feature.

**What the register misses:**

1. **The rule it states once is not applied as a rule** (M4) — seven unpaired negative assertions.
2. **Six tests are outside it entirely** (M5).
3. **A test that cannot fail because its subject already exists** (M1, test 73) — the one shape the register does not have a category for. Add one: *"a test whose subject is already implemented"*, with the check being *run it before writing any code; if it passes, it is not this work's test.* That check, applied to the plan as written, would have caught M1 and would catch the class in future.
4. **The guard script has no self-test** (M6), against the repo's own four-script convention.
5. **Two security clauses and one BDD scenario have no test at all** (M7).
6. **Concurrency on the step-0 door is untested** (C7) — G10 exists for the record write and has no Library equivalent.

---

## 6. STRIDE summary

| Component | Threat | Status in the spec |
|---|---|---|
| Embed target resolution | **I**nformation disclosure — an escaping path echoed back | **Under-addressed.** FR-017 covers the reader and is unimplementable on the current wire (C3); the agent surface echoes `l.Form` verbatim and is not covered at all (M9) |
| Embed target resolution | **S**poofing — a false "this file does not exist" about a file that does | **Not addressed.** C2 — the dominant producer routes to the wrong state |
| `spaContentSecurityPolicy` | **T**ampering / **E**oP — weakening the app's own policy | **Well addressed in intent**, incomplete in artefacts: the oracle document is unlisted and becomes unusable once the policy is configurable (M2). The floor test's existing self-mutation half is real and correctly required to be extended. |
| YouTube frame | **I**nformation disclosure — provider contact per embed | **Well addressed.** Click-to-play mandatory, local facade, no thumbnail, constructed URL, 11-char id, `no-referrer`, sandbox without top-nav/popups, A-10 closes the title-fetch loophole. Test 69's three-part assertion is correct. |
| `![[x.html]]` route | **E**oP — agent-authored HTML framed in the reader | **Addressed.** Permanently link-fallback, asserted by tests 20 and 21 — though 21 needs pairing (M4) |
| SVG embed | **E**oP — script execution | **Stated, untested** (M7) |
| `PUT .../content{,-binary}` | **T**ampering + **R**epudiation | **Partially addressed.** Token and audit specified; the lock is not (C7), the read side has no token (C6), and one live caller cannot supply one (C6) |
| Record-field write | **T**ampering — writing a derived or relation property | **Not addressed.** C5(d) — the chosen path has none of ADR-068's guards and the client cannot tell the properties apart |
| Record-field write | **R**epudiation — an unattributable human write | **Addressed in intent** (FR-090, FR-093, test 72) but the structural guarantee is claimed in the wrong place (M8) |
| Lazy view evaluation | **D**enial of service — self-inflicted | **Well addressed.** FR-067's four-in-flight bound, visible queue, slot release, and no auto-retry, all counted rather than timed |
| Transclusion | **D**oS — recursion | **Apparatus specified, input unreachable** (C1) |

---

## 7. Unasked questions

These are questions the spec should have answered and did not. They are not findings in themselves; each one is a decision an implementing agent would otherwise make silently.

1. **When the resolver's edge evidence and the node's `exists` flag disagree, which wins?** Both are on the same response. The spec's model uses neither the node nor a disagreement rule (M13).
2. **What is the version token for a Library file, and is it the same token the knowledge path computes?** If they differ, the same note has two tokens depending on which door read it, and "the doors must agree" (dataset G3's stated rationale) is false at the value level, not just the policy level. `pkg/knowledge/version.go` computes one over note content; nothing in `rest_library.go` computes anything.
3. **What happens to an embed whose target is a knowledge base's own control-plane file** (`records/`, `views/`, a `.base` the importer marked unservable at import time rather than at evaluation time)? FR-044 covers evaluation-time unservability only.
4. **Does the pre-flight report (US-6) run against the founder's live vault, and where does its output live?** A-8 recommends `scripts/` plus committed output, but the report enumerates note paths and view labels from a private knowledge base — committing it puts vault contents in the repo. Worth a line.
5. **What does an embed of a `.base` file do when the collection has zero imported views** (dataset D8) *and* the file itself failed to import? D8 assumes the file imported with an empty view list; an import failure is a different state with a different sentence.
6. **Does `knowledge_read`'s new `[embed]` marker change the token cost of every dashboard read**, and has anyone measured it against the 75-embed cockpit note? D4 argues the marker is free for notes without embeds; it says nothing about the note that has fifteen.
7. **When the operator empties the video allow-list mid-session**, does an already-rendered note re-render as links, or does the reader keep the boot-time value? FR-081 says "when emptied, the embed renders as a link" without saying when the reader learns.

---

## 8. Verdict

**BLOCK.**

Seven CRITICAL findings. Three of them — C1, C2, C3 — mean the spec's own integrity property does not hold: the feature will state falsehoods about files that exist, and the mechanism built to prevent the worst failure mode has no reachable input. Two more — C4, C5 — describe capabilities that cannot be built on the current contracts, with the contract work unsequenced. C6 ships a regression to a working feature, and C7 leaves a lock-free compare-and-swap in the fix for a lost update.

The document's evidence discipline is real and most of its measurements survived re-checking. The failures cluster in exactly two places: **assertions inherited from the ADR without re-measurement** (V2's stale comment, V4's skip semantics) and **rules the spec states correctly and then applies once** (M4's negative-assertion pairing, M5's register completeness). Both are correctable without restructuring the document.

Review written to:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate/docs/internal/specs/adr-083-embedded-content-spec-review.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/adr-083-embedded-content-spec.md docs/internal/specs/adr-083-embedded-content-spec-review.md
```
