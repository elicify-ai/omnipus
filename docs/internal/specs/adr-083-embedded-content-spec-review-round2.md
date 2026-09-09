# ADR-083 implementation spec + ADR — adversarial review (grill pass 2 of 2)

- **Documents under review, as a pair:**
  - [`docs/internal/specs/adr-083-embedded-content-spec.md`](adr-083-embedded-content-spec.md) — revision 2, 3,979 lines
  - [`docs/internal/architecture/ADR-083-embedded-content-in-knowledge-base-notes.md`](../architecture/ADR-083-embedded-content-in-knowledge-base-notes.md) — revision 3, 2,038 lines
- **Round-1 review:** [`adr-083-embedded-content-spec-review.md`](adr-083-embedded-content-spec-review.md) (7 CRITICAL, 13 MAJOR, 9 MINOR, 3 OBSERVATION; BLOCK). **Not overwritten.**
- **Worktree / commit:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate`, HEAD `def10b90e`
- **Detected mode:** `plan-spec` — full structural checks applied.
- **Method.** Every claimed count was **recounted by script**, not read. Every code fact newly asserted in revision 2 was re-checked against `def10b90e` by opening the file. The X7 category the spec introduces ("a test that cannot fail because its subject already exists") was applied by hand to **all 113 numbered test rows**. Commands and their outputs are quoted where a number is disputed.
- **Founder rulings D-A…D-D, Q1–Q9, N1–N4 are NOT re-opened.** Neither is the settled position that both save doors share the *existing* knowledge version token. Where a finding touches a ruling it is about the documents' **execution** of it.

---

## 1. Executive summary

Revision 2 fixed most of round 1's substance. The three deletions (cycle detection, print, the
already-done audit task) are correct and cleanly argued, the C2/C3/C5/C6/C7 corrections are real
and code-verified, and the new X7/X8 register categories are the best thing in either document.

It then failed its own instruments in three measurable ways.

**The register's completeness claim is false again, at four times the scale.** Round 1 found six
tests outside the register (M5). Revision 2 promoted "every numbered test appears in exactly one
register row" to a standing invariant and a success criterion (SC-026, "0 tests outside the
register") — and **at least 36 numbered tests appear nowhere in the register at all**, including
tests **106, 107 and 108**, the three tests revision 2 added to close round 1's own C2 and M7
findings.

**The X7 sweep the spec demands was not performed on the spec.** X7 says: run every new test before
implementing; if it passes, it is not this work's test. Applied by hand to all 113 rows, **at least
eight tests cannot fail on the current tree**, and every one of them sits inside an X7 check whose
text is "every one must fail". Two more sit outside every X7 check. Round 1 found one instance; this
round finds nine more.

**And the C2 fix, the single most important correction in the revision, has a hole in exactly the
case the correction itself identified.** EMB-021's matching rule compares a skip's path (or its
basename) against the edge's target. The ADR's own §13 C2 row records that an unreadable *directory*
is a walk-level skip that "takes its files with it" — and `WalkContained` records that skip under the
**directory's** path (`pkg/knowledge/contain.go`, the `ReadDir` error branch: `RelPath: cur.rel`).
No file under it will ever match the rule. Every note beneath an unreadable directory still gets
*"Nothing in this knowledge base is named X"*. Separately, the whole cross-check is specified for the
**reader only** — the agent surface, which US-2's own narrative says is owed the same honesty, has
no skip cross-check requirement and no test.

**Verdict: BLOCK.**

| Severity | Count |
|---|---:|
| CRITICAL | 5 |
| MAJOR | 12 |
| MINOR | 8 |
| OBSERVATION | 4 |

---

## 2. Did round 1's findings actually get fixed?

The brief asked specifically whether findings were fixed or reworded. Twenty-nine of thirty-two were
genuinely fixed. Three were not.

| R1 | Disposition in revision 2 | Verified? |
|---|---|---|
| C1 cycle detection unreachable | Feature **deleted** (N1). EMB-056/057/058, tests 53–56, SC-011, E2–E7, four scenarios, US-7 AS-4…7 all gone | **FIXED** — struck-through entries present and consistent; but see **M3** for three surviving dangling references |
| C2 skipped target says "nothing is named X" | EMB-021 cross-check + B5/B5a/B5b/B6/B6a + tests 102/103/105 | **PARTIALLY FIXED** — see **C1(r2)** (directory skips) and **C2(r2)** (agent surface) |
| C3 wire cannot distinguish absence from containment | `unresolved_reason` added as CW-2's third field; test 111; B12 rewritten | **FIXED** — verified `KnowledgeGraphEdge.yaml` has no `reason` and is `additionalProperties: false` |
| C4 `LibraryFileRef` unbuildable | Narrowed to `name`/`path`; extension-only classification; three divergences named; test 109 | **FIXED** — verified `classifyLibraryEntry` reads `entry.mime` and `entry.is_text_editable`, and no `getLibraryEntry` exists |
| C5 US-10 unimplementable | Re-routed through `RecordWriteRequest`; CW-4…CW-7 | **FIXED** — verified all four schemas have **zero** `#/components/schemas/…` references in `openapi.yaml` |
| C6 CW-1 incomplete; PDF save breaks | ETag on four operations; EMB-007; regression row added | **FIXED in intent, INCOMPLETE in mechanism** — see **C3(r2)**, **M6**, **M7** |
| C7 lock-free compare-and-swap | EMB-006; tests 99/100; G7a/G7b/G7c | **FIXED in intent, UNDER-SPECIFIED** — see **M8** |
| M1 FR-091/test 73 already done | Deleted; X7 category created; task 116 | **FIXED** — verified all five names in `pkg/audit/audit.go:324-328` and the stale comment at `pkg/knowledge/audit.go` |
| M2 CSP oracle is a Markdown file | Added to Impact Assessment, Regression table, test 62 | **FIXED** |
| M3 `heading_found` false for `.base`/blocks | EMB-039, test 110, B13a/B13b | **PARTIALLY FIXED** — see **M4**: test 110 conflates a Go test with a SPA assertion, and EMB-039's reader rule has no SPA test |
| M4 negative assertions applied once | Promoted to Invariant 1; pairings named per row | **PARTIALLY FIXED** — see **M9**: two of the named pairings are vacuous |
| M5 six tests outside the register | Promoted to Invariant 2 + SC-026 | **NOT FIXED — REGRESSED.** See **C4(r2)**: 36 tests are outside |
| M6 guard has no self-test | Test 119 + SC-029 | **FIXED** |
| M7 SVG/raw-frame untested | Tests 107, 108 | **FIXED as coverage** — but test 107 cannot fail (**C5(r2)**) |
| M8 `EditNoteRequest` enforces nothing | Symbols table corrected; `NewWriter` named in EMB-090 | **FIXED** — verified |
| M9 agent surface echoes the path | Asymmetry **kept and stated** (founder ruling) + EMB-024 + test 93 | **FIXED as ruled** |
| M10 SPA test files have no paths | Every SPA row carries a directory; `.github/workflows/pr.yml` in Impact Assessment | **FIXED** |
| M11 three more unversioned doors | Assumptions row naming `deleteLibraryEntry`/`uploadLibraryFiles`/transfer | **FIXED** |
| M12 A-3's "fourth may be required" | CW-3; header says seven | **FIXED** |
| M13 `nodes[].exists` dropped | EMB-022; test 104; B8b/B8c | **FIXED** |
| m1 broken doc reference | Principle inlined | **FIXED** |
| m2 five wrong line citations | Converted to `file::symbol` | **FIXED** — one residual (**m6**) |
| m3 `IsValidEventName` file | Corrected | **FIXED** |
| m4 FR namespace collision | Renamed FR- → EMB- throughout the spec | **FIXED in the spec, RE-CREATED in the ADR** — see **M12** |
| m5 test 38 duplicates coverage | Noted as "a second net" | **FIXED as a note** — but the test still cannot fail (**C5(r2)**) |
| m6 out-of-scope collection | EMB-014's second clause, B8a, test 106 | **FIXED** |
| m7 "both embeds show the new value" | Test 118, EMB-092 | **FIXED** |
| m8 print stated unconditionally | Superseded by N3 | **FIXED** |
| **m9 A-5's editable-cell ruling lives only in the ambiguity table** | A-5 now says *"This is now stated in EMB-046 and EMB-088"* | **NOT FIXED — REWORDED.** See **M11**. Neither requirement contains the carve-out |

---

## 3. Recount of every claimed figure

The brief asked me to recount rather than trust. Commands and results:

| Claim in the spec | Command | Result | Verdict |
|---|---|---|---|
| 85 live requirements | `grep -oE '^- \*\*EMB-[0-9]+\*\*' \| sort -u \| wc -l` | **85** | **TRUE** |
| 5 struck requirements | — | EMB-056, 057, 058, 070, 091 | **TRUE** |
| 85 matrix rows | `grep -cE '^\| \*?\*?EMB-[0-9]+'` | **85** | **TRUE** |
| Matrix ↔ requirements set equality | `comm` both directions | **empty both ways; no duplicates** | **TRUE** |
| 82 BDD scenarios | `grep -c '^#### Scenario'` | **82** | **TRUE** |
| 82 `Traces to:` back-references | `grep -c '^\*\*Traces to\*\*'` | **82** | **TRUE** |
| Scenario↔trace interleave (no scenario without a trace, no orphan trace) | positional scan | **clean** | **TRUE** |
| 7 contract changes CW-1…CW-7 | manual | **7, and the ADR §6 table matches row for row** | **TRUE** |
| 0 untraced dataset rows | 121 rows scanned | **0 empty**, but **2 dangling titles** (A10, A13) and **7 near-miss citations** | **MISLEADING — see M14** |
| **"Live numbered tests: 106"** | row scan | **113** | **FALSE — see M1** |
| **"0 tests outside the register" (SC-026)** | name+number scan of the register | **≥36 outside** | **FALSE — see C4(r2)** |
| Every acceptance scenario has ≥1 BDD scenario | reference-set diff | **6 uncovered** | **FALSE — see M2** |

---

## 4. Findings

### CRITICAL

---

#### C1(r2) — EMB-021's matching rule cannot match an unreadable-directory skip, so the dominant walk-level case still says "nothing is named X"

**Lens:** Incorrectness / Incompleteness
**Sections:** EMB-021; US-2 AS-4; BDD *"A target the indexer refused to read is 'could not be checked', not 'does not exist'"*; datasets B5, B5a, B5b, B6a; tests 102, 103; SC-005a; ADR §13's C2 row, D1 stage 2, D4

EMB-021 states the matching rule precisely — which is right, and is what round 1 asked for:

> The comparison MUST be: the skip's path equals the edge's target, **or** the skip path's basename equals it, with and without a markdown extension.

The ADR's own §13 C2 row records the refinement it verified when accepting the finding:

> `unreadable` appears at **both** levels (**a directory that cannot be listed is walk-level and takes its files with it**; a file that cannot be opened is scan-level)

Measured, that directory skip is recorded under the **directory's** path, not under any file beneath
it (`pkg/knowledge/contain.go`, the `ReadDir` error branch inside `WalkContained`):

```go
entries, err := fsys.ReadDir(cur.real)
if err != nil {
    ...
    out.Skipped = append(out.Skipped, SkippedEntry{
        RelPath: cur.rel,          // the DIRECTORY, not its files
        Reason:  SkipUnreadable,
        ...
    })
    continue
}
```

Every file under `cur.rel` is therefore absent from `walk.Files`, so a link to `notes/private/plan.md`
produces a **matching unresolved edge** whose `to_path` is the normalised link text — and the only
skip entry in the answer names `notes/private`. Neither clause of EMB-021's rule fires: the paths are
not equal, and the basenames (`private` vs `plan`) are not equal either.

So the reader renders *"Nothing in this knowledge base is named plan"* — about a file that exists,
which is the exact sentence US-2 exists to prevent and the exact defect C2 was raised for. The
correction closed the symlink / `outside_root` / `irregular` cases (all of which record a **file**
path) and left open the one the ADR singled out as the reason the refinement mattered.

Datasets B5/B5a/B5b all use `symlink` or a same-file skip, so **no fixture in the plan exercises this
shape**, and test 103 (`skip-matching-rule-path-and-basename`) tests exactly the two clauses that
already work plus a near-miss — it would pass with the directory case broken.

**Fix.**
1. Extend EMB-021's rule to a **prefix** clause: a skip whose path is an ancestor directory of the
   edge's target suppresses the missing-file marker for every target beneath it. State the comparison
   on normalised, `/`-separated, cleaned collection-relative paths, and state that a bare basename
   match must **not** be treated as a directory match.
2. Add dataset **B5c**: *walk-level `unreadable` skip naming a directory; an embed of a note beneath
   it → indeterminate, with the directory named in the reason.* Add its paired control (skip removed →
   missing-file marker) as B5d.
3. Add a row to test 103 for the ancestor case **and** a near-miss ancestor (a sibling directory) that
   must **not** suppress.
4. Note in EMB-021 that `.trash` and `.obsidian/**` directories are skipped by `scanSkippedDirNames`
   and **deliberately not reported in `Skipped`** (the code says so in place), so a link into one of
   them correctly reports absence and must not be swept up by the new prefix clause.

---

#### C2(r2) — the C2 fix is reader-only; nothing gives the agent surface the same cross-check, and the agent is where the sentence gets repeated

**Lens:** Incompleteness / Incorrectness
**Sections:** EMB-020, EMB-021, EMB-024; US-2 narrative and AS-11; tests 92, 93; ADR D4

This is the brief's question (3) — *does the C2 fix eliminate every path to a false "nothing is named
X", including what reaches agents?* It does not.

US-2's own narrative states the obligation:

> The same honesty is owed to agents. An agent summarising a fifteen-module dashboard must be able to
> say "three of these are broken" rather than quietly summarising the twelve that worked.

But the fix is specified entirely on the client:

- **EMB-021** is in the *Resolution and honesty (US-2)* group and is written against "the answer's
  skip list" — the `KnowledgeGraphResponse` the SPA fetches. Its two tests (**102**, **103**) are both
  `src/components/library/knowledge/embed/embedResolver.test.ts`. Its success criterion (SC-005a) is a
  reader assertion.
- **EMB-020** requires the agent line to mark an embed and carry "its reason".
- **EMB-024** extends that to the **containment** reason only.
- **Tests 92 and 93** cover embed-marking, view-vs-heading wording, and the containment reason. Neither
  covers a skipped target.

`knowledge_read` does not consult the graph response at all — it projects `ResolvedLink` into
`ReadLink`, and `renderReadLinks` prints `"  %s (unresolved) %s"`. For a walk-level skip the resolver's
state is `ResolveUnresolved` with no skip knowledge, so the agent is told the target is unresolved with
no reason, and will summarise it as missing. The reader gets the corrected sentence; the agent gets the
old one — and the agent's output is the surface that gets pasted into a report.

**Fix.** Either:

- **(a)** Add an FR: *the `knowledge_read` link projection MUST consult the same walk-skip set and MUST
  report a skipped target as "could not be checked" with the skip reason, never as unresolved.* Add
  `Embed` **and** a skip-derived reason to `ReadLink`, and add a test row to 92/93 asserting a
  walk-skipped embed's line carries the skip reason and does **not** read as absent; or
- **(b)** State explicitly, as a residual with the founder's sign-off, that the honesty guarantee is
  reader-only in this work and that an agent will report a walk-skipped target as unresolved — and
  remove the "the same honesty is owed to agents" sentence from US-2's narrative, because as written it
  promises something no requirement delivers.

Do not leave it as it is: the narrative promises it, no requirement implements it, and no test would
notice.

---

#### C3(r2) — the version token has no specified *value*, and on the binary door there is nothing to compute it from

**Lens:** Infeasibility / Ambiguity
**Sections:** EMB-001, EMB-002, EMB-006, EMB-007; CW-1; A-11; datasets G1–G7c; tests 1–4, 97–101; ADR §4.2a(i)(ii); SC-001, SC-001c

Three compounding gaps, all inside the P0 story.

**(a) A-11 is left OPEN, and the founder has settled it.** The spec's still-open ambiguity table asks
*"whether the Library's version token is the same value the knowledge path computes"* and recommends
"One." That question is settled — both doors share the **existing** knowledge token. Leaving it as an
open ambiguity with a recommendation invites an implementer to decide it again, and A-11 is described
in the spec's own closing section as *"the only one of the four that changes step 0's work."*

**(b) Neither document names the function.** `grep -c ComputeVersionToken` and `grep -c ReadNoteVersion`
return **0 in the spec and 0 in the ADR**. `pkg/knowledge/version.go` exports both
(`ComputeVersionToken(content []byte) VersionToken` at `version.go::ComputeVersionToken`,
`ReadNoteVersion(c *Collection, rel string)` at `::ReadNoteVersion`). A step-0 implementer reading
either document has no instruction pointing at them, and the thing they will reach for instead —
size + modification time — is what `version.go`'s own header spends four paragraphs refusing.

**(c) On the binary and oversized paths there is no content to hash.** `library.ContentResult`
(`pkg/library/content.go`) omits `Content` by design:

> A text file over `MaxContentBytes` is reported `TooLarge` with `Content` omitted … [a binary file
> has] `Content == "" and IsText == false`.

`handleLibraryContentGet` builds its response from that struct. So on the **PDF path** — the one door
N2 explicitly refuses to exempt — a token derived from `ReadContent` would be the hash of an empty
string, identical for every binary file in the workspace. The token must be computed from the raw
bytes, by a read the handler performs itself, and **neither document says so**. `handleLibraryDownload`
has the bytes; `handleLibraryContentGet` does not.

**Fix.**
1. Move A-11 out of the open table into the resolved table with the founder's ruling, and add to
   EMB-007: *the token is `knowledge.ComputeVersionToken` over the file's **raw bytes**, for every
   Library file, inside and outside a knowledge base, on every one of the four operations.*
2. State that `getLibraryContent` must read the file's bytes to compute the header **even when it
   returns no `content` field** (binary, `too_large`), and add dataset rows: *G1a — `getLibraryContent`
   on a binary file returns an `ETag`*; *G1b — on a `too_large` text file returns an `ETag`*; *G1c —
   the `ETag` from `getLibraryContent` and the one from `downloadLibraryFile` for the same file are
   byte-identical.* G1c is the row that catches two token definitions.
3. Add the same to test 97's description, which today says only "the JSON read sets the header".

---

#### C4(r2) — SC-026 and Invariant 2 are false again, and three of revision 2's own new tests are outside the register

**Lens:** Incompleteness / false measured claim
**Sections:** *The register's two standing invariants*, Invariant 2; SC-026; the whole per-phase register

Revision 2 promoted round-1's M5 into a rule and a measured criterion:

> **Invariant 2 — every numbered test appears in exactly one register row.** … "the register covers
> every test" must be a checkable claim rather than a hope.

> **SC-026**: Every numbered test in this plan appears in **exactly one** false-green register row.
> Measured by counting: **0** tests outside the register.

I counted. Taking the register as `## False-Green Risk Register` → `## Functional Requirements`, and
searching for each numbered test **by number and by name**, in table rows and in prose:

**At least 36 numbered tests appear nowhere in the register.** Confirmed by direct `grep` over the
section for each name — every one returns 0:

```
svg-drawn                              0    (test 107)
raw-frame-markup                       0    (test 108)
empty-answer-is-one                    0    (test 106)
toolbar-on-hover                       0    (test 49)
unservable-view                        0    (test 48)
identifier-validation                  0    (test 63)
facade-is-local                        0    (test 64)
press-creates                          0    (test 65)
note-section-and-block                 0    (test 52)
empty-note-says                        0    (test 113)
TestKnowledgeEditEmbedOp               0    (tests 32, 33, 34, 35, 37)
TestKnowledgeRead_Marks                0    (test 92)
GoesThroughTheTypedRecordPath          0    (test 70)
StaleTokenReturns409                   0    (test 71)
audio-video-mermaid                    0    (test 82)
```

Full list of the 36: 13, 15, 18, 20, 23, 24, 26, 28, 30, 32, 33, 34, 35, 37, 42, 43, 47, 48, 49, 52,
58, 63, 64, 65, 66, 70, 71, 82, 85, 87, 90, 92, 106, 107, 108, 113.

Three of those — **106, 107 and 108** — are tests revision 2 *added* to close round 1's C2 and M7. They
were appended to the order table and never given a register row, which is precisely the mechanism that
produced round-1's M5 (tests 91–96 appended after the register was written). The document diagnosed the
mechanism, named it, made it a rule, and then repeated it with its own new tests.

Two further breaks of "exactly one":

- **Test 93 is in two X7 lists** (step 1: "tests 9–38, **102–109 and 91–93**"; step 1c: "tests 39, 40,
  41, **93**, 110 and 111"), while the continuation table assigns it to step 1c.
- **Test 91 is in two X7 lists** (step 1 and step 2), while the continuation table assigns it to step 1.

**Fix.** Give each of the 36 a register row in its phase, with the risk and the catching assertion
named, as every other test has. Resolve 91 and 93 to one phase each. Then make SC-026 machine-checkable
by stating the command: *for each numbered row in the order table, `grep` the register section for its
name; zero hits is a failure.* An invariant asserted twice and broken twice needs a check, not a third
statement.

---

#### C5(r2) — the X7 sweep was not applied to this plan: at least eight tests cannot fail, and each sits under an X7 check demanding it fail

**Lens:** Test coverage gap (the brief's area 1)
**Sections:** cross-cutting risk X7; every phase's X7 check; SC-028

X7 is the register's newest and sharpest category, created because round 1 found one instance of it
*inside the document that defines it*. Applied by hand to all 113 numbered rows against the code on
`def10b90e`, there are **at least nine more**.

| Test | Assertion as specified | Why it passes today, with zero implementation | Under an X7 "must fail" check? |
|---|---|---|---|
| **20** `html-other-text-fall-back-to-link` | badge present, no renderer, no download card | `remarkKbWikilinks` converts an embed to an image **only** when the extension is in `IMAGE_EXTENSIONS`; every other embed already renders a wikilink node with the badge `embed shown as a link` (`knowledgeMarkdown.tsx`, the `isEmbed` branch). All three clauses hold now | **Yes** — step 1, "tests 9–38 … every one must fail" |
| **21** `no-download-card-reachable` | no download card on any embed path; **pairing:** a `.zip` renders the badge | `LibraryDownloadCard` is not reachable from the note reader today, and `.zip` already renders the badge. **Both halves pass.** The M4 pairing does not rescue it because the pairing's subject also already exists | **Yes** — step 1 |
| **34** `TargetOutsideCollectionRefused` | the request is refused before any write | `op: "embed"` does not exist; `EditTool.Execute`'s `default:` branch calls `refuseOp`. The request is refused — for the wrong reason | **Yes** — step 1 |
| **35** `ModifierKindMismatchRefused` | width on a non-picture refused | Same: `refuseOp` | **Yes** — step 1 |
| **37** `MissingExpectVersionRefused` | the token is mandatory; empty refused | Same: `refuseOp` fires before any argument or token check | **Yes** — step 1 |
| **38** `CatalogueUnchangedByEmbedOp` | ceiling `knowledge_*` set == the eight names; no seed change; reconciliation adds nothing | All three are true **now**. Verified: exactly eight keys in `pkg/config/defaults.go`. The spec itself notes two existing tests already assert this entry-for-entry | **Yes** — step 1 |
| **61** `FrameAncestorsAndImgSrcUnchanged` | byte-for-byte on `frame-ancestors` and `img-src` | Both directives are in `spaContentSecurityPolicy` today, unchanged. A test asserting a value equals its current value passes before the change | **Yes** — step 4, "tests 59–69 … must all fail … except test 68" |
| **107** `svg-drawn-as-image-and-script-inert` | renders inside `<img>`; side effect absent; no `<svg>` in the DOM | **`'svg'` is in `IMAGE_EXTENSIONS`** (`knowledgeMarkdown.tsx:304`). An SVG embed already becomes an image node today, so all three clauses hold | **Yes** — step 1, "102–109" |
| **117** `AnonymousIsGreppableAsAClass` | over a fixture activity file containing agent / identified / anonymous rows, a text search returns exactly the anonymous rows | The test **writes the fixture**. The property asserted is a property of strings the test itself produced — no production code is exercised. It cannot fail in any implementation | **Yes** — step 5 |
| **90** `link-fallback-still-fires` (extended) | the existing badge behaviour still fires | It is a regression test over existing behaviour and passes before and after, correctly | **No** — outside every X7 list, and **not** in SC-028's exclusion list |
| **22** `embed-in-code-fence-mounts-nothing` | fence inert; **pairing:** identical notation outside the fence mounts its renderer | The negative half holds today (remark yields `code`/`inlineCode` nodes, which the text-node visitor never sees). Whether the pairing rescues it **depends on the kind, which is unspecified** — with an image it also passes today | **Yes** — step 1. Conditional |

Three of these — 20, 21 and 107 — are especially costly, because a phase-exit report citing them would
be truthful and worthless: they are the tests that appear to prove "the link fallback still works" and
"SVG is safe", and they prove neither about this work.

**Fix.**
1. For **34, 35, 37**: pair each refusal with *a valid `op="embed"` request that **succeeds** in the
   same test*, and assert the refusal **message**, not just the refusal. A test that cannot tell
   "unknown operation" from "target outside the collection" is not testing containment.
2. For **20, 21, 61, 90, 107**: relabel them as **pins of existing behaviour**, remove them from their
   phases' X7 "must fail" lists, add them to SC-028's exclusion list, and say in each row that they are
   never evidence a step landed. Where a real new assertion exists, split it out: test 21's real new
   content is *"the new renderer dispatch never routes to the download card"*, which needs the dispatch
   to exist — assert it against a kind that **does** mount a renderer after the change.
3. For **38**: keep it (the reasoning is sound) but move it out of the "must fail" list and mark it as
   the third net it is.
4. For **117**: rewrite it so the fixture rows are produced by **the production actor-formatting code**,
   not by the test. Otherwise it asserts nothing.
5. For **22**: name the kind used in the pairing, and make it one that does not render today (PDF or
   `.base`).
6. Add an X7 check to the **cross-cutting** phase, which currently has none — tests 83–87, 89, 90, 96
   and task 116 sit outside every X7 list.

---

### MAJOR

---

#### M1 — "106 live numbered tests" is wrong; there are 113, and the arithmetic double-subtracts

**Lens:** Incorrectness (false measured claim)
**Sections:** Traceability Matrix, *Completeness check — counted, not asserted*

The completeness table states:

> | Live numbered tests | **106** | 1–52, 57–72, 74–87, 89–119, minus the 6 retired numbers (53, 54, 55, 56, 73, 88) |

Counted by scanning every order-table row: **113 live numbered rows**, six struck (53–56, 73, 88), no
gaps, no duplicates. The stated ranges already exclude the six retired numbers — 52 + 16 + 14 + 31 =
113 — so subtracting them again produces 106. The table is headed *"counted, not asserted"*; this row
was asserted.

(Of the 113, one — row **116** — is explicitly *"(not a test)"*, so the true test count is 112.)

**Fix.** Correct the figure to **113 rows / 112 tests / 1 task**, drop the second subtraction, and state
the counting command so the next revision recomputes rather than transcribes.

---

#### M2 — one dangling `Traces to:`, five mistraces, and six acceptance scenarios with no BDD scenario

**Lens:** Inconsistency / Incompleteness
**Sections:** BDD Scenarios; US-1 AS-7, US-2 AS-7/AS-11, US-8 AS-6/AS-7, US-10 AS-9/AS-10, US-11 AS-6

Validating every `Traces to:` against each story's actual acceptance-scenario count:

**Dangling (points at a scenario that does not exist):**

| Scenario | Traces to | Reality |
|---|---|---|
| *Returning to the application does not re-evaluate every module* | **US-8, AS-7** | US-8 has **6** acceptance scenarios. This is the print deletion's fallout: N3 removed the old AS-5 and the reference below it was not renumbered |

**Mistraced (points at the wrong scenario; the subject is covered, the pointer is not):**

| Scenario | Traces to | Should be |
|---|---|---|
| *Save sent with no version token at all is refused* | US-1 AS-2 (the conflict case) | **US-1 AS-7** |
| *An embed whose evidence has not arrived shows reserved space* | US-2 AS-5 (incomplete evidence) | **US-2 AS-7** |
| *A failed evidence request produces one page-level error* | US-2 AS-6 (the two halves disagree) | **US-2 AS-8** |
| *An agent reading a dashboard can tell embeds from links* | US-2 AS-8 | **US-2 AS-11** |
| *A view filter set inside an embed is local* | US-10 AS-7 (title/path not editable) **and 8** | **US-10 AS-8 and AS-9** |
| *A successful field write refreshes only what went stale* | US-10 AS-9 | **US-10 AS-10** |

US-10's is a clean off-by-one caused by inserting the new AS-6 (the calculated/relation ruling) without
renumbering the traces below it.

**Genuinely uncovered:** **US-11 AS-6** — *"Given an agent omitting the version token, When it makes the
request, Then the request is refused."* No BDD scenario covers it. Test **37** and datasets **H9/H10**
exist, but test 37's *Traces to BDD Scenario* column points at *"An agent asks for an embed"*, whose
Then-clauses say nothing about a token. So EMB-102's story-level obligation has a test and a dataset and
no scenario, and the test traces to a scenario that does not assert it.

**Fix.** Renumber the six traces. Add a BDD scenario for US-11 AS-6 (*"An agent that omits the version
token is refused"*) and repoint test 37 at it. Add the scenario/acceptance cross-check to the
completeness table as a counted property, since the current table counts scenarios and traces but never
checks that a trace *resolves*.

---

#### M3 — three dangling references to deleted features survive inside the plan's own scaffolding

**Lens:** Inconsistency
**Sections:** BDD Scenarios US-8 heading; *Test Hierarchy*

Round-1 corrections deleted cycle detection and print. Three references were not:

1. **Line 1893** — the BDD section heading reads *"### US-8 — A forty-module dashboard stays cheap and
   **prints whole**"*. The user story itself was retitled (*"stays responsive, and says what it cannot
   do"*); the BDD heading was not. It is a direct restatement of the guarantee N3 withdrew, at the head
   of the scenarios that replace it.
2. **Line 2521** — the Test Hierarchy's Unit row lists the scope as *"notation parsing, the resolver's
   state decision, label→machine-name matching, path conversion, **the cycle set**, argument
   validation"*. The cycle set was deleted in full by N1.
3. **Line 2523** — the E2E row lists *"security policy enforcement, network contact before and after a
   click, **printing**, and scroll-driven mounting"*. There is no print E2E test; test 88 was deleted.

These are the first three rows an implementer reads before writing a single test, and two of them
instruct work that no longer exists.

**Fix.** Retitle the BDD US-8 section to match the story. Remove "the cycle set" and "printing" from the
Test Hierarchy. Add a completeness check: the strings `cycle`, `depth cap` and `print` must appear only
inside an explicit deletion note.

---

#### M4 — test 110 is a Go handler test carrying a SPA assertion, and EMB-039's reader rule has no SPA test at all

**Lens:** Infeasibility / Incompleteness
**Sections:** EMB-039; test 110; datasets B13a, B13b; SC-005c; the traceability matrix row for EMB-039

Test 110 is specified as:

> | **110** | `TestKnowledgeEdge_HeadingFoundIsFalseForBaseAndBlockTargets` | **Integration** | … Two rows, each asserting **the reader's "no such heading" marker is absent** |

The name and level make it a **Go** gateway test over `knowledgeEdge`. Its stated assertion is about a
**React** marker. One test cannot be both, and the row conflates two:

- a Go test that `heading_found` is `false` for a `.base` target and for a block reference — worth
  having, and it fails today because the field does not exist; and
- a SPA test that the reader does not render the "no such heading" marker in those two cases.

EMB-039 is a **reader** rule — *"The reader MUST NOT consult it for a `.base` target or for a block
reference"* — and the traceability matrix maps it to test 110 **only**. So the rule that prevents a
false marker firing across all 75 dashboard embeds has no test on the surface it governs.

Worse, as a SPA assertion it is a **pure unpaired negative**: "the marker is absent" passes on a reader
that never renders that marker — which is today's reader. It is not in SC-027's list of negatives
requiring pairing.

**Fix.** Split test 110 into `110a` (Go, `heading_found` false for `.base` and block; fails today) and
`110b` (SPA, `knowledgeMarkdown.embeds.test.tsx`, the marker absent for a `.base` target and a block
anchor **paired in the same test with a markdown target whose heading is genuinely missing, which DOES
render the marker**). Map EMB-039 to both. Add 110b to SC-027's pairing list.

---

#### M5 — the ETag lands inside `http.ServeContent`, and neither document says what shape the value takes

**Lens:** Incompleteness / Incorrectness
**Sections:** CW-1; EMB-007; ADR §4.2a(i); tests 98, 101; the Integration Boundaries note on the byte-stream reader

This is the brief's question (5). The mechanism is under-specified in two ways that both fail silently.

**(a) The download path is `http.ServeContent`, which implements conditional GET and Range.**
`handleLibraryDownload` calls `serveLibraryContent`, whose body is:

```go
applyLibraryByteHeaders(w, displayName, forceAttachment)
http.ServeContent(w, r, "", modTime, content)
```

and whose own doc comment says it is used *"for what it is genuinely good at: Range requests (audio and
video seeking) and conditional GETs."* Setting an `ETag` before that call changes `ServeContent`'s
behaviour for **every** consumer of the helper, not just the PDF loader: `If-None-Match` now produces
304s and `If-Range` now validates against the ETag rather than `Last-Modified`. `serveLibraryContent` is
also reached from `serveLibraryPath`. Neither document mentions `ServeContent`, `If-None-Match` on this
path, `If-Range`, or which of the helper's callers are in scope.

**(b) The value's shape is unspecified, and the two shapes are incompatible.** The precedent the ADR
cites is explicit about it — `contracts/openapi.yaml` on the providers catalogue:

> Headers: `ETag: "<sha256>"` (quoted, strong) … a weak (`W/`) or unquoted value [is not matched]

RFC-conformant `ETag` values are quoted; Go's `ServeContent` only honours a quoted value (its `scanETag`
requires a leading `"` or `W/"`) and silently ignores an unquoted one. But `expect_version` is a body
field carrying a bare `VersionToken`. So the client must strip the quotes on read and the server must
add them on write — and if either side forgets, every comparison fails **as a 409 that looks exactly
like a genuine conflict**, on the PDF save path, with no way for a user to make progress.

**Fix.** Add to EMB-007: the header value is the RFC-quoted strong form of the token; the client strips
the surrounding quotes before placing it in `expect_version`; the server accepts the bare form in the
body and never the quoted one. Add a dataset row: *G4a — a save sending the **quoted** value in
`expect_version` is refused with 400, not 409*, so the two shapes cannot be confused for a conflict. And
add an FR or an explicit scope note stating whether the ETag is set on `handleLibraryDownload` only or
inside `applyLibraryByteHeaders`, plus a regression row for the audio/video Range path.

---

#### M6 — `src/lib/api.ts`'s request helper discards response headers, and it is in no impact list

**Lens:** Incompleteness / Infeasibility
**Sections:** EMB-004, EMB-007; CW-1; Impact Assessment; tests 7, 101

EMB-004 requires the Library editor to *"send back the version token it received"* and EMB-007 requires
the PDF editor to *"replace it from the save's own response"*. Both need a **response header** to reach
SPA code.

Measured: the SPA's single API entry point is `async function request<T>(path, init?, schema?): Promise<T>`
(`src/lib/api.ts`). It returns the parsed body and nothing else — `res.headers` is read exactly twice in
the whole file, once for `Content-Length` and once at `providersCatalogETag = res.headers.get('ETag')`,
which sits inside a **bespoke hand-rolled fetch** with module-level ETag state, not inside `request<T>`.

So EMB-004/EMB-007 require either (a) widening `request<T>`'s return contract, which touches every SPA
API call, or (b) three more bespoke fetches beside the providers one. Neither is named. The Impact
Assessment's CW-1 row lists `useLibraryFileEditor`, the four handlers, `LibraryPdfPreview::handleSave`
"and its loader", and the generated types — **not `src/lib/api.ts`**, and the Symbols table has no row
for it.

The PDF *loader* is fine — it already uses a raw `fetch` (`fetchPdfBytes`, `credentials: 'include'`) and
can read the header off the `Response` it holds, exactly as the ADR says. The *save* is not:
`putLibraryContentBinary` goes through the helper, so the new token in the PUT response is unreachable
without the change above.

**Fix.** Add `src/lib/api.ts::request` to the Symbols table and to CW-1's d=1 impact column, state which
of (a) or (b) is chosen, and add the choice to test 101's description — it currently says "a second save
in the same session uses the token from the first save's response" without saying how that response's
header reaches the component.

---

#### M7 — EMB-006's "same collection root, same collection-relative path" has no named derivation, and no helper exists

**Lens:** Infeasibility / Ambiguity
**Sections:** EMB-006; datasets G7a, G7b, G7c; tests 99, 100; SC-001b; ADR §4.2a(iii)

This is the brief's question (6). The decision is right — the comparison and the write go inside
`knowledge.WithNoteWriteLock` with the agent path's `NoteLockConfig`. Verified: the lock key is
`noteLockKey(collectionRoot, rel) = collectionRoot + "\x00" + path.Clean(rel)`
(`pkg/knowledge/version.go::noteLockKey`), and `handleLibraryContentPut` takes no lock of any kind.

What is missing is the derivation. The Library handler is given a **workspace-relative** path against a
**workspace** root; the lock needs a **collection** root and a **collection-relative** path. Nothing in
either document names a function that gets from one to the other, and none exists: the only
knowledge-base detection in `rest_library.go` is `annotateKnowledgeBaseEntries`, which decides whether a
**directory entry** is a knowledge base (`knowledge.Detection.IsKnowledgeBase`). There is no
"which collection encloses this file" helper.

That derivation has to walk ancestors to find the enclosing collection, decide what happens with nested
knowledge bases, and produce a `LockDir` via `LockDirFor(home, collectionRoot)`. Every one of those is a
place the two writers can silently pick different keys — which the spec correctly identifies as the
failure mode ("the guard is decorative") and pins with test 100 and SC-001b, but does not prevent.

**Fix.** Add an FR naming the derivation as work: *the handler MUST resolve the enclosing collection
root for a workspace-relative Library path by the same rule `knowledge.Detect` uses, and MUST use
`LockDirFor` for the lock directory; where no collection encloses the path, `CollectionRoot` is empty and
the mode is degraded (EMB-006's second sentence).* State the nested-collection rule (innermost wins, or
refuse). Add dataset **G7d**: *a file in a knowledge base nested inside another knowledge base — the lock
key names the innermost*, because test 100 as written asserts the key matches "the agent path's" without
saying which collection that is when two apply.

---

#### M8 — the negative-assertion pairing table lists two pairings that are vacuous

**Lens:** Test coverage gap (the brief's area 2)
**Sections:** Invariant 1; the step-1 pairing table; SC-027

I checked each listed pairing by asking the brief's question — *would this test genuinely fail if its
subject were deleted?*

| Test | Negative | Named pairing | Fails if the subject is deleted? |
|---|---|---|---|
| 14 | missing-file sentence absent | could-not-be-checked marker present with its reason | **Yes** |
| 11 | graph never consulted, no miss reported | the placeholder **is** rendered | **Yes** |
| 17 | refusal contains no path segment | a second unresolved edge yields the missing-file marker; the two differ | **Yes** |
| **21** | no download card on any embed path | a `.zip` embed renders the badge | **NO** — both halves already hold on the current tree (see C5(r2)) |
| **22** | fence: no renderer, no marker | the identical notation outside the fence mounts its renderer | **Unknown** — the kind is unspecified; with an image, both halves already hold |
| 44 | no derived machine name for an unmatched label | a matched label yields the server's machine name verbatim | **Yes** |
| 62 | emptied config → no external host | default install contains the host **and** matches §10.7's literal line | **Yes** |
| 76 | title and path not editable | an ordinary field on the same row **is** editable | **Yes** |
| 89 | (three states asserted together) | — | **Yes** |
| 95 | statement present inline | statement absent in the pane | **Yes** |
| 108 | no `<iframe>` from raw markup | an allow-listed embed yields a placeholder | **Yes** |
| 115 | derived and relation refused | an ordinary property on the same record succeeds | **Yes** |
| 74 | refused with neither user nor bypass | accepted under bypass; `user:` form when authenticated | **Yes** |
| **110** | "no such heading" marker absent ×2 | **none named** | **NO** — see M4 |

Eleven of fourteen hold. Three do not, and one of them (110) is not on any pairing list at all.

Separately, the plan carries **three different lists of the same set**: Invariant 1's prose names seven
(11, 17, 21, 22, 44, 62, 76); the step-1 pairing table has six (adding 14, moving 62 and 76 to their own
phases); SC-027 names ten (adding 89 and 95). They are reconcilable but they are not the same list, and
SC-027 is the one that will be reported.

**Fix.** Repair 21, 22 and 110 as set out in C5(r2) and M4. Then make one list, in SC-027, and have the
per-phase tables reference it rather than restate it.

---

#### M9 — SC-028's exclusion list contradicts two X7 checks and the regression tests

**Lens:** Inconsistency
**Sections:** SC-028; step-4 X7 check; step-6 X7 check; test 68; test 90; the Regression table

SC-028 reads:

> the count of new tests that passed on that first run is **0**, excluding the two rows explicitly
> labelled as pins of existing behaviour (tests 105 and 119's clean-state case).

But the step-4 X7 check says *"tests 59–69 and 114 must all fail … **except test 68** … which must pass
both before and after"*, and the Regression table requires several extensions of existing tests
(`link-fallback-still-fires`, the CSP floor test, `useLibraryFileEditor.test.tsx`) that assert behaviour
which holds today. Test 68 and test 90 will both pass on the first run and neither is excluded.

With C5(r2)'s findings folded in, the true exclusion list is at least: 3 (partly), 20, 21, 38, 61, 68,
90, 105, 107, 119-clean — ten, not two. As written, SC-028 is a criterion that cannot be met and will
therefore be reported as met with a caveat, which is the worst of both.

**Fix.** Rebuild the exclusion list from the X7 sweep once C5(r2) is applied, state it as an explicit
enumeration, and require that every excluded row carry the words "pin of existing behaviour" in the order
table so the two lists cannot drift.

---

#### M10 — the cross-cutting register phase has no X7 check, and eleven tests sit outside every X7 list

**Lens:** Incompleteness
**Sections:** the per-phase register's X7 check lines; *Cross-cutting — the mount budget*

Eight phases carry an X7 check; the **cross-cutting** phase does not. Collecting the enumerated ranges
(step 0: 1–8, 97–101; step 1: 9–38, 102–109, 91–93; step 1c: 39, 40, 41, 93, 110, 111; step 2: 42–51, 91,
94, 95; step 3: 52, 57, 58, 112, 113; step 4: 59–69, 114; step 5: 70–79, 115, 117, 118; step 6: 119),
these numbered rows are covered by **no** X7 check:

**80, 81, 82, 83, 84, 85, 86, 87, 89, 90, 96** and task **116**.

That includes every mount-budget test — the ones the register's own cross-cutting section says are
"tempting to write against a clock", which is exactly where a first-run pass is most informative.

**Fix.** Add an X7 check to the cross-cutting section covering 83–87, 89, 90 and 96, and to step 6
covering 80, 81 and 82. Note that 90 is expected to pass (M9).

---

#### M11 — A-5 claims a fix that was not made: neither EMB-046 nor EMB-088 carries the editable-cell carve-out

**Lens:** Incorrectness (a finding reworded rather than fixed)
**Sections:** Ambiguity A-5; EMB-046; EMB-088; test 49; ADR D8

Round 1's m9 said A-5's ruling lived only in the ambiguity table and would be lost, because an
implementer applying EMB-046 to the whole embed hides the editors. Revision 2's A-5 row now says:

> **This is now stated in EMB-046 and EMB-088** rather than living only in this table, because an
> implementer applying the toolbar rule to the whole embed would have hidden the editors — making the
> interactivity ruling invisible on the exact surface it names.

It is not stated in either. In full:

> - **EMB-046**: An embedded view MUST reveal its controls on pointer hover and on keyboard focus, and
>   MUST NOT show them at rest.
> - **EMB-088**: An editor MUST be offered only for field types the record's own definition describes as
>   editable. [derived / relation / list / undescribed get no editor] … Every such assertion MUST be
>   paired, in the same fixture, with an editable field that IS offered an editor …

EMB-046 has no cell carve-out; EMB-088 says nothing about hover-gating. The finding was answered with a
sentence asserting it had been answered — which is the same failure class as X7 (a claim whose subject
does not exist), applied to a requirement rather than a test. The consequence is the one A-5 itself
names: an implementer reading EMB-046 hides the editors, and D-D becomes invisible on the surface D-D is
about.

**Fix.** Add the carve-out to EMB-046 verbatim: *"controls" here means the view's toolbar chrome. An
**editable cell is content, not chrome**: it stays always active and always keyboard-reachable, showing
its affordance on hover of the cell.* Add a clause to EMB-088 pointing at it. Add an assertion to test 49
(which already covers hover/focus/tap) that **a cell editor is reachable while the toolbar is hidden** —
without it, nothing in the plan would catch the mistake.

---

#### M12 — the ADR re-creates the FR-namespace collision the EMB- rename existed to end, and two of its §12 dispositions are now wrong

**Lens:** Inconsistency (ADR↔spec divergence)
**Sections:** ADR §12, §13; spec's opening note on the FR→EMB rename

The spec's first substantive paragraph explains the rename:

> ADR-067's `FR-090`, `FR-106` and `FR-107` are cited **inside production code comments** … An
> implementer reading `FR-106` in a Go file and `FR-106` in this spec would have found two unrelated
> requirements. `EMB-` ends that.

The ADR still carries **both populations in one document**. It legitimately cites ADR-067's `FR-090`
(×3), `FR-106` (×4), `FR-107`, `FR-043`, `FR-046` and others — and in §13 it cites the spec's **retired**
ids as if they were live:

- *"**C6** — CW-1 is incomplete; **FR-001** breaks PDF annotation saving"* — now `EMB-001`
- *"**M1** — **FR-091** and its test describe work already done"* — now `EMB-091`

So a reader of the ADR meets `FR-001` (spec, retired), `FR-090` (ADR-067, live, cited in Go), `FR-091`
(spec, retired) and `FR-106` (ADR-067, live, cited in Go) in the same document. The ADR carries **zero**
occurrences of `EMB-`.

Two further §12 rows state dispositions that later sections reverse:

- *"M9 lazy mount has no failure/bound/print story | **Accepted.** Retry policy, four-in-flight bound,
  **`beforeprint`**; browser-find limitation stated."* — D3 and §2.8 withdrew `beforeprint` entirely (N3).
- *"M7 no REST audit path | **Accepted.** **Three named preconditions on step 5, including
  `pkg/audit/events.go`.**"* — one of those three was deleted by M1, and `IsValidEventName` is in
  `audit.go`, not `events.go`, which §13's own m3 row corrects.

§12 is presented as "what the review changed", i.e. current dispositions, not history — so a reader
looking up "how was M9 resolved?" is told `beforeprint`.

**Fix.** Rewrite §13's C6 and M1 rows to cite `EMB-001` and `EMB-091`, and add a line to §13 recording
the rename so the ADR's remaining `FR-` ids are unambiguously ADR-067's. Amend the two §12 rows in place
with a "superseded by N3 / by M1" note rather than leaving the stale mechanism named.

---

### MINOR

- **m1 — Two dataset `Traces to:` targets name scenario titles that no longer exist.** **A10** cites
  *"BDD Outline: an embed the reader could not check"* and **A13** cites *"BDD: An embed pointing outside
  the knowledge base"*. Both were renamed in revision 2 to *"Every honesty signal the server can actually
  emit produces the right marker"* and *"A containment refusal says something different from a missing
  file"*. The "0 untraced dataset rows" claim is true only in the sense that no cell is empty.
- **m2 — Seven near-miss dataset citations.** B6a, B7, B8, B8b, B8c cite *"every honesty signal the server
  can emit"* (actual: *"…can **actually** emit produces the right marker"*); G2 and G5 cite *"Person's
  save is refused because an agent wrote first"* (actual: *"…wrote **to the note** first"*). Findable, but
  they defeat an exact-match completeness check.
- **m3 — Dataset A8 (`![[]]`) traces to the code-fence scenario.** A degenerate empty notation and a
  fenced embed are different subjects; the code-fence scenario asserts nothing about `![[]]`.
- **m4 — The code-fence BDD scenario traces to US-3 AS-6 and sits under the US-12 heading.** US-3 AS-6 is
  *"a file whose kind has no inline treatment keeps the link-with-a-badge"* — the opposite outcome from
  the fence's "no marker of any kind". There is no acceptance scenario about code fences anywhere; the
  behaviour appears only in Edge Cases. Either add one to US-3 or trace the scenario to the Edge Cases
  entry explicitly.
- **m5 — Test 3's name differs between the order table and the register.** Order table:
  `TestLibraryContentPut_FreshVersionSucceedsAndReturnsNewToken`. Register's deletable-subject warning:
  `TestLibraryContentPut_FreshVersionSucceeds`. One of them will be the file that gets written.
- **m6 — One residual `file:line` citation.** The Tech Stack table cites `kbMarkdownBase.tsx:263-264` for
  the markdown pipeline, against the spec's own standing instruction two sections earlier that citations
  are `file::symbol` because line numbers in these files go stale within days.
- **m7 — Test 119's convention list names three of the repository's four self-test scripts.** Present:
  `check-browser-tests-gated.test.sh`, `check-no-handwritten-wire-types.test.sh`,
  `check-no-tool-error-from-status.test.sh`, **and `check-no-removed-providers-selfcheck.sh`**. Round 1
  listed all four; revision 2 dropped one. Harmless, but the fourth uses a different suffix convention
  (`-selfcheck.sh`), which is worth knowing before naming a new file.
- **m8 — EMB-014's "zero edges and zero skips" trigger also fires for a valid collection whose note was
  not indexed.** The page-level statement then says the knowledge base returned nothing, about a note
  that exists and has fifteen embeds. Add a dataset row deciding whether that is acceptable.

### OBSERVATION

- **O1 — EMB-019's truncation honouring is unreachable in the one shape that matters.** EMB-013 routes to
  indeterminate only when *"the graph loaded and no edge matched"*. A **matching unresolved** edge on a
  **truncated** answer therefore falls through EMB-021 (no skip) to the missing-file marker, despite the
  answer being admittedly incomplete. Dataset B7 covers only the zero-edge case. Not live today
  (`resp.Truncated` is set only in the neighbourhood branch — verified), but EMB-019 exists precisely
  because "a client that ignores an honesty flag is one change away from a false statement". Consider
  making truncation suppress the missing-file marker in **both** shapes.
- **O2 — the three remaining unversioned doors are described as lacking a *conflict check*, which
  understates one of them.** The Assumptions row is accurate about delete/rename/transfer. But
  `uploadLibraryFiles` replacing a note takes **no lock either**, so after step 0 an upload can still
  interleave with an agent's `EditNote` and lose it — a lost update, not merely an unrecorded one. Worth
  one clause, since step 0's whole argument is that a check without a lock is not a compare-and-swap.
- **O3 — the ADR/spec pair is otherwise in unusually good agreement.** CW-1…CW-7 match row for row
  between spec §*Contract-First Work* and ADR §6; the deletions (N1, N3, EMB-091) are reflected
  symmetrically in both; §13's disposition table is honest about which review recommendations were
  **rejected** (C1's Option A, C4's fetch-the-entry, A-9's narrowing, A-6's 503) rather than quietly
  adopting them. The divergences found are M3 (three stale spec references) and M12 (two stale ADR
  dispositions plus the id namespace) — small, and both mechanical.
- **O4 — "Ship steps 0–5 and stop" remains the right default, and step 6 is now the only phase whose
  tests are entirely outside the X7 sweep.** With zero measured uses across 784 notes, the cheapest way
  to satisfy M10 for step 6 may be to drop it from the plan rather than to write its X7 check.

---

## 5. Structural integrity results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** — 12 stories; counts 7/11/7/4/7/3/6/6/7/11/7/4 |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL (6)** — US-1 AS-7, US-2 AS-7, US-2 AS-11, US-8 AS-6, US-10 AS-10, US-11 AS-6. Five are mistraces; US-11 AS-6 is genuinely uncovered (**M2**) |
| Every BDD scenario has a `Traces to:` back-reference | **PASS** — 82 scenarios, 82 traces, correctly interleaved |
| Every `Traces to:` resolves to a real acceptance scenario | **FAIL (1)** — US-8 AS-7 does not exist (**M2**) |
| Every BDD scenario has a corresponding test | **PASS** — spot-checked; the *Traces to BDD Scenario* column is populated on every numbered row |
| Every functional requirement appears in the traceability matrix | **PASS** — 85 requirements, 85 rows, exact set equality, no duplicates |
| Every BDD scenario appears in the traceability matrix | **PASS** (via grouped rows, as the spec states) |
| Test datasets cover boundary, edge and error conditions | **PASS in form, FAIL in one content case** — 121 rows, all traced; but no row exercises the unreadable-directory skip (**C1(r2)**), and B13a/B13b's expected outcome has no SPA test (**M4**) |
| Regression impact explicitly addressed | **PASS** — the table is strong; `LibraryPdfPreview.test.tsx`'s save round-trip is now named as a break rather than a risk. Missing: the audio/video Range path, if the ETag lands in the shared helper (**M5**) |
| Success criteria measurable, no subjective language | **PASS in form, FAIL in three values** — SC-026 is false (**C4(r2)**), SC-028's exclusion list is wrong (**M9**), and the "106 live tests" figure it rests on is wrong (**M1**) |
| Contract-first sequencing complete (Constraint #8) | **PASS** — CW-1…CW-7 each carry files, owner story and a "must land before" cell; the ADR's §6 table matches. Gaps are in CW-1's *mechanism*, not its sequencing (**M5**, **M6**) |
| Tool-policy claim (Constraint #6) | **PASS** — independently re-verified: exactly eight `knowledge_*` keys in the ceiling, all `allow`. The ungoverned-REST-door reframing from round 1's O1 is carried through honestly |
| False-green register completeness (the spec's own Invariant 2 / SC-026) | **FAIL** — ≥36 numbered tests outside the register (**C4(r2)**) |
| X7 self-application (the spec's own new category) | **FAIL** — ≥8 tests cannot fail, all inside "must fail" checks (**C5(r2)**) |

---

## 6. Test coverage assessment

**What is genuinely strong and must survive the next revision:**

- **X8** ("a feature whose input the product cannot construct") is a real contribution and is correctly
  applied to the one case that motivated it. The step-3 register's account of why the cycle detector had
  to be *deleted* rather than deferred is the best reasoning in either document.
- The **paired** design is right where it is applied: found/not-found (39), accept/reject on the
  validator (41), one-then-still-one-then-exactly-one-more (8, 77), three-states-in-one-body (74, 89,
  115), the two-edge containment fixture (17, 111).
- **Counts, never clocks** (84, 86, 112) with both prior incidents cited by name.
- **Real boot wiring** for every audit assertion (5, 6, 72), with the two existing precedent files named.
- Fixtures that cannot pass on a deleted feature: two levels below root with a decoy (16), fifteen
  embeds over five files (50), two views with **different rows** (46), at most two workers **counted**
  (31, I9).

**What it misses, in order of cost:**

1. **The register does not cover 36 of its own tests** (C4(r2)) — including the three added this
   revision to close round-1 findings.
2. **X7 was not run against the plan** (C5(r2)) — eight to eleven tests are day-one green, and eight of
   them are inside checks that say they must fail.
3. **The C2 cross-check misses the directory case and the agent surface entirely** (C1(r2), C2(r2)).
4. **Two named pairings are vacuous and one negative has no pairing** (M8, M4).
5. **The mount-budget phase has no X7 check at all** (M10).
6. **Test 117 asserts a property of its own fixture** and cannot fail in any implementation (C5(r2)).

---

## 7. STRIDE summary

| Component | Threat | Status after revision 2 |
|---|---|---|
| Embed target resolution | **S**poofing — a false "this file does not exist" | **Improved, still open.** EMB-021 + EMB-022 close the symlink / `outside_root` / `irregular` / node-disagreement paths. The **unreadable-directory** path stays open (C1(r2)) and the **agent** path is untouched (C2(r2)) |
| Embed target resolution | **I**nformation disclosure — the escaping path echoed | **Addressed as ruled.** EMB-017 redacts the reader; EMB-024 states the agent asymmetry with a reason and a test. The founder's ruling is implemented, not merely cited |
| `PUT .../content{,-binary}` | **T**ampering — lost update | **Design correct, mechanism incomplete.** EMB-006 + tests 99/100 + G7a/G7b are the right shape; the collection-root derivation is unnamed (M7) and the token's value is unspecified on the binary path (C3(r2)) |
| `PUT .../content{,-binary}` | **R**epudiation | **Addressed.** EMB-003, tests 5/6 on real boot wiring, `NewWriter` named as the enforcement point |
| `GET .../download` | **T**ampering / availability | **New, unassessed risk.** An ETag inside `http.ServeContent` changes conditional-GET and `If-Range` behaviour for every consumer of the shared helper (M5) |
| Record-field write | **T**ampering — writing a derived or relation property | **Addressed properly.** Routed through `RecordWriteRequest`; two independent guards (EMB-088 client, test 115 server); the third "ordinary property succeeds" case stops "refuses everything" passing |
| Record-field write | **R**epudiation — an unattributable write | **Addressed as ruled (N4).** Accepted-and-recorded, `anonymous` unprefixed, refusal when neither user nor bypass, all three in one test. The greppability obligation's **test cannot fail** (C5(r2)) |
| `spaContentSecurityPolicy` | **T**ampering / **E**oP | **Well addressed.** Served-header parsing, the floor test extended not relaxed, the §10.7 Markdown oracle in both the impact list and the regression table, the derived PDF-worker policy now asserted (test 114). Test 61 is a pin, not a guard (C5(r2)) |
| YouTube frame | **I**nformation disclosure | **Well addressed.** Local facade, no thumbnail, constructed URL, 11-char pattern, exact host comparison (F7), `no-referrer`, sandbox, A-10's title loophole closed |
| SVG embed | **E**oP — script execution | **Now tested, but the test cannot fail** — `svg` is already in `IMAGE_EXTENSIONS` (C5(r2)). The property holds today; the test is a pin and must be labelled one |
| `![[x.html]]` route | **E**oP | **Addressed.** Permanently link-fallback; test 20/21 are pins of existing behaviour rather than new guards (C5(r2)) |
| Lazy view evaluation | **D**oS — self-inflicted | **Well addressed.** Four-in-flight, visible queue, slot release, no auto-retry, all counted |
| Transclusion | **D**oS — recursion | **Closed by construction (N1).** No mechanism, no unreachable tests |

---

## 8. Unasked questions

Decisions an implementing agent will otherwise make silently.

1. **When a skip names an ancestor directory, does the reader name the directory in the marker, or only
   the reason?** Naming it is more useful and leaks a path the reader already has; not naming it makes
   the marker unactionable. (C1(r2))
2. **Which collection wins when knowledge bases nest?** EMB-006's lock key depends on the answer, and
   two writers picking differently is exactly the "decorative guard" case. (M7)
3. **Is the ETag set on `handleLibraryDownload` alone or inside `applyLibraryByteHeaders`?** The second
   changes behaviour for the preview-token path and the audio/video Range path. (M5)
4. **Does `getLibraryContent` read the file's bytes to compute a token it cannot otherwise produce for a
   binary or oversized file — and is that read budgeted?** Today the handler returns metadata without
   ever holding the content. (C3(r2))
5. **What does the reader do when the graph answer's `skipped` list is itself incomplete?** EMB-019
   honours `truncated`, but truncation of the skip list specifically would silently re-open C2.
6. **Which kind does test 22's out-of-fence pairing use?** With an image, both halves pass today. (M8)
7. **Does test 117's fixture come from the production actor formatter or from the test?** As written it
   is the test, and the assertion is then about nothing. (C5(r2))
8. **After A-11 is closed, does the token for a Library file *outside* any knowledge base use the same
   function?** The spec's own recommendation says yes ("it costs nothing and avoids a second definition
   of 'changed'"), but no requirement says it.

---

## 9. Verdict

**BLOCK.**

Five CRITICAL findings. Two of them (C1(r2), C2(r2)) mean the feature's integrity property still does
not hold: after a correction written specifically to stop it, the system will still tell a reader that a
file does not exist when it does — for every note under an unreadable directory — and will still tell an
*agent* so in every skipped case. One (C3(r2)) leaves the P0 save door without a defined token value on
the exact path the founder refused to exempt. Two (C4(r2), C5(r2)) are failures of the document's own
instruments: the register's completeness invariant is false at four times round 1's scale, and the X7
category the revision introduced was never applied to the revision.

The pattern is the same one round 1 identified and is worth naming again, because it survived a revision
written to catch it: **rules this document states are not applied to this document.** Invariant 2 was
promoted from an observation to a criterion and immediately broken by the three tests added in the same
edit. X7 was created, explained better than anything else in the file, and not run. A-5 was answered with
a sentence claiming it had been answered.

None of that requires restructuring. The deletions are right, the contract sequencing is right, the
ADR/spec pair agrees almost everywhere, and the evidence discipline is real — every code fact I re-checked
in revision 2 was accurate, which is the first time that has been true across three rounds. What is
needed is one mechanical pass that actually executes the three checks the document already specifies.

Review written to:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate/docs/internal/specs/adr-083-embedded-content-spec-review-round2.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/adr-083-embedded-content-spec.md docs/internal/specs/adr-083-embedded-content-spec-review-round2.md
```
