# Spec review — ADR-067 knowledge base and render-first preview (adversarial grill, pass #4)

- **Reviewed:** `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` (Draft, 1,998 lines, 112 requirements)
- **Prior passes:** [round 1](adr-067-knowledge-base-and-preview-spec-review.md) (BLOCK — 5C/21M) · [round 2](adr-067-knowledge-base-and-preview-spec-review-round2.md) (BLOCK — 5C/18M) · [round 3](adr-067-knowledge-base-and-preview-spec-review-round3.md) (BLOCK — 4C/21M/13m/6O)
- **Supporting:** [ADR-067](../architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md) · [preview isolation experiment](adr-067-preview-isolation-experiment-2026-08-22.md) · the committed harness `docs/internal/experiments/preview-isolation/`
- **Branch:** `feat/library-improvements`, worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements`, HEAD `e02cf009`
- **Date:** 2026-08-22
- **Method:** every code claim below was read on this branch in this session. The §10.3 policy string was **re-derived by executing the committed harness's own module namespace** and compared byte-for-byte against the spec's fenced block. The §16 traceability matrix, the §13.1 test table and the ADR's acceptance criteria were compared **mechanically**, not by eye. `false-green-patterns.md` was read from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo` (it is not on this branch — and that turns out to matter, see R4-C-02).
- **Scope discipline:** round 3's closed findings are stated as closed and not re-argued. Nothing is padded.

---

## 1. Executive summary

**Four CRITICAL, sixteen MAJOR, fourteen MINOR, four OBSERVATION. Verdict: BLOCK.**

This revision did the work. Round 3's four criticals are genuinely closed or closed in
substance, and fourteen of its twenty-one majors are properly closed — not reworded. The
three most expensive asks all landed:

- **§10.3 exists and is verifiable.** I executed `docs/internal/experiments/preview-isolation/server.py`'s
  and `server2.py`'s module namespaces and compared their `POLICIES["self"]` and
  `POLICIES["active"]` against the spec's fenced block. **All three are byte-identical.** The
  provenance claim is true, the harness is committed, and the experiment is reproducible.
  R3-C-02's central defect is closed.
- **§0 no longer misreports the experiment.** Both offending rows now read "Measured —
  Chromium only", cite `§6A.2` / `§6A.3`, state that the Firefox and WebKit runs were stopped,
  name the space-advance oracle and record that the fixture's own oracle was found broken. I
  checked each against the experiment's text. R3-C-01 is closed.
- **The token has a security envelope.** FR-003d/e/f/g plus §10.5 and §10.6 are a faithful and
  in places improved rendering of ADR D15.6. R3-C-04 is closed in substance.

What blocks is a different class of problem from round 3, and it is the predictable cost of a
five-agent merge: **the prose is now well ahead of the machinery that is supposed to enforce
it.** Four things go wrong, and three of them are one-line-verifiable:

1. **The spec's threat model enumerates two URLs for a Library file's bytes. There are three.**
   `/api/v1/media/workspace/{workspace}/{id}` (`rest.go:5307`, `withOptionalAuth`) serves
   Library-resolved bytes with `Content-Disposition: inline` and a metadata-derived
   `Content-Type`, on the gateway origin, with **no CSP** (`rest.go:10184-10192`).
   `pkg/library/entries.go:157` maps `.svg` → `image/svg+xml` and `.html` → `text/html`. So the
   route that §10.4 says is "closed" because it "serves attachments" does not, for this third
   URL. §10.5's two-row table, FR-003g's *"Only the token path serves inline"*, and §10.4's
   *"Both URLs are closed, by different means"* are all built on an incomplete enumeration.
2. **`src/components/library/` is in no vitest group, so none of the SPA unit tests this spec
   plans there will run.** Verified on this branch: `pr.yml:196-209` hardcodes four path
   patterns; `src/components/library/` matches none; **117 of 426 SPA test files (27%) never
   execute** while the job reports green; two patterns point at directories that do not exist
   (`src/components/command-center/` was deleted; `src/components/projects/` holds zero tests);
   and `scripts/check-vitest-coverage.mjs` — the guard `false-green-patterns.md` §4 says now
   enforces this — **exists on `main` and not on this branch**. Test 1, the spec's own named
   HIGH-risk release gate (SC-013), lands in that directory. The spec mentions vitest **zero
   times**, while spending five paragraphs on the equivalent Playwright problem.
3. **`FR-008a` does not exist.** Test 94 — the sole gate on `.svg`, the newest security
   decision in the document and the one the brief asked me to grill hardest — traces to a
   requirement that appears nowhere in the spec. It is also absent from §16, is assigned to no
   named spec file (so it inherits `retries: 3` and Chromium-only), and the gate itself
   (*"must pass before `.svg` ships inline"*) is prose with no MV and no SC behind it.
4. **FR-015c's minimum type table omits `.css` and `.js`, which §10.4 requires to be served
   inline.** MV-24 says an extension absent from the table is served `application/octet-stream`;
   FR-015 requires `nosniff` on every inline response. A stylesheet or script served
   `application/octet-stream` under `nosniff` is **refused by every browser**. As written, the
   two sections together break FR-003 and US-1 AS-4 — the flagship scenario.

Three further phantoms: **`0l`** (the only test for FR-0001d, the requirement that keeps the
remote attacker-controlled attachment path unrelaxed), **`3a`** (the only test for FR-015b and
half of FR-015c — the host-MIME-registry requirement whose own text says *"The test written on
the Mac passes; the shipped container is broken"*), and **`62a`** (cited by §17's AW-7 row).
Each is a requirement whose entire enforcement is a test id that was never defined.

**R3-O-06's process observation is now four for four.** The same three sections — §9 Edge
Cases, §13.2 Datasets, §16 Traceability — again retain a superseded verdict, and this time §3
and §11 joined them. E-5 still contradicts FR-034a. AS-5, DS-3 row 5 and test 0c still test the
double-quote case FR-0003 was rewritten *because it already works*. §11 still names the A14
distinct-origin fallback that §0 row 3 says is retired. §3 P-5 still says "Required for stage
1 — Chrome, Firefox, **Safari**" while SC-012 says Safari proper is not covered. §16 points
FR-019c at test 67, the configuration test that §10.7 explicitly says cannot detect the failure
it is meant to catch, and omits test 96 which was written for exactly that.

| Severity | Count |
|---|---|
| CRITICAL | 4 |
| MAJOR | 16 |
| MINOR | 14 |
| OBSERVATION | 4 |
| **Total** | **38** |

### Answers to the questions this pass was asked

| Question | Answer |
|---|---|
| **(a) Are round 3's criticals and majors genuinely closed?** | **All four criticals: two fully (R3-C-01, R3-C-02-in-substance), two in substance with named residuals (R3-C-03, R3-C-04).** Of twenty-one majors, **fourteen fully closed, seven partial or open** — full table in §2. **Reworded rather than fixed: none.** The partials are all *incomplete propagation* — a requirement was correctly rewritten and its scenario, dataset row or matrix row was not. That is a different and less serious failure than round 3's, which was claims being upgraded without verification. |
| **(b) Is the `.svg` reasoning sound, and is test 94 sufficient?** | **The containment reasoning is sound. The justification for including it is not, and test 94 is not sufficient.** "One policy to every byte" genuinely does extend the measured HTML containment to an SVG document, and the spec is honest that this is reasoned not measured. But the stated *reason* for including it — *"Excluding it would break `<img src="logo.svg">`"* — is an unverified browser-behaviour claim (`Content-Disposition` is not honoured for subresource loads), and the obvious middle option (correct type **plus** `attachment` on the token path, which closes the top-level document case with no new measurement) is never evaluated. Test 94 tests one of the three contexts §10.4 reasons about, gates on a non-existent FR-008a, has no engine assignment, and inherits `retries: 3`. R4-C-03, R4-M-06. |
| **(c) Is §10.5's token envelope buildable? Gaps against FR-003a..g?** | **Buildable, with five gaps.** Present in §10.5 and in **no** requirement, MV or test: the 32-byte/base64url **entropy**, the **renewal** mechanism, the **GET/HEAD-only** restriction as an asserted property (it is the stated reason no CSRF exemption is needed), **path containment inside the token's scope**, and **rate limiting on the token path** (which MV-23 presumes exists in order to drive a 429). Renewal is additionally not buildable as described: a sandboxed cross-origin iframe exposes no status to its embedder, and the shape §10.5 says to match (`served_subdirs.go:137-151`) **renews in place and returns the same token**, which would defeat the TTL rationale. R4-M-02, R4-M-03. |
| **(d) Contradictions introduced by the merge** | **Ten, listed in §3–§4.** The sharpest: FR-015c ⊥ §10.4 (R4-C-04); FR-003g/§10.5 ⊥ the third serving route (R4-C-01); §16's FR-019a/FR-019c rows ⊥ FR-019a's and §10.7's own corrections (R4-M-05); AC-17.2 ⊥ the FR-071 rewrite (R4-M-09); Stage 0's test table ⊥ the mechanism paragraph that rejected build tags (R4-M-04); E-5 ⊥ FR-034a (R4-M-11); §11 A14 and §3 P-5 ⊥ §0 and §13.4 (R4-M-10). Plus three phantom test ids, a doubled `**Independent test:**` at line 244, and a header that still says 37 acceptance criteria while §16 now correctly lists 43. |
| **(e) Test quality vs `false-green-patterns.md`** | **Substantially improved, one catastrophic regression.** Tests 59, 59a, 61, 61a, 67a, 69a, 70, 72, 73, 75, 80, 82, 84, 90, 93, 94 are all built to the document's rules — positive controls, behavioural rather than source-text oracles, boundary *pairs*, server-side ground truth, `--self-test` ahead of the real run. That is the strongest test plan any pass has seen. **The regression is trap #4 verbatim** (R4-C-02): the spec applies the hardcoded-allowlist lesson to Playwright in detail and not at all to vitest, on a branch where the vitest allowlist is currently dropping 27% of the suite and where the guard that fixed it is missing. Also unfixed across three passes: `Bench_*` (`go test` will not run them), and test 0b guarding traversal in a package that disclaims it. |
| **(f) Still-untrue codebase assertions** | **Six, all minor except the first.** (1) §10.4/§10.5/FR-003g's claim that the authenticated Library path is the only non-token route — false, `/api/v1/media/workspace/` exists (R4-C-01). (2) *"29 assertions in `pkg/pathsafe/pathsafe_test.go`"* — actual: 8 test functions, 25 `assert`/`require` call sites, 48 table cases in `TestValidateComponent` alone. Test 0e is named for a number with no referent. (3) *"all 12 non-test callers"* of `CleanRelPath` — there are **11**. (4) NB-16/FR-0002a say C0 controls and Windows characters are fused *"in one predicate"* — true of `replaceIllegalRunes` (`pathsafe.go:277`, one `\|\|`), **false** of `firstIllegalRune` (`:334-339`, two sequential `if`s); the rationale names the two the wrong way round. (5) §2.4 understates the sanitiser divergence — `tel:` also differs. (6) Test 61a assumes a named PDF.js chunk group; `vite.config.ts:167-180` uses the function form of `manualChunks` returning exactly four names, none of them a pdf chunk. **Verified true and accurate:** the §10.3 provenance and byte-identity, the `withUploadAuth` / `SameSite=Strict` / `ServeContent`-sniffs chain behind FR-003a and FR-015a, `newSPAHandler`'s 200-for-everything (`embed.go:55-58`), `repairAndValidateToolPolicyCoverage`'s repair-before-validate (`gateway.go:934-948`), the `cross-platform-extra.yml` phantom and the zero-Windows-jobs count across all **19** workflows, `pathsafe`'s 100-rune / 200-rune constants, `ValidateComponent(".")`/`("..")` failing only via `hasTrailingDotOrSpace`, the `notifications.sanitize` allow-list, and `%%…%%` rendering literally in chat today. |

---

## 2. Status of round 3's findings

**Closed means closed — these should not be re-opened by a later pass.**

| Round 3 | Status | Evidence |
|---|---|---|
| **R3-C-01** §0 misreports the experiment | **CLOSED** | Both rows now read "Measured — **Chromium only**", cite experiment §6A.2/§6A.3, state the Firefox/WebKit runs were stopped, name the space-advance oracle and the broken fixture oracle, and forbid `document.fonts.status`. Each checked against the experiment's own text. *Residual (R4-m-01): rows 1–5 still carry no section citation — fix item 4 was to require one per row.* |
| **R3-C-02** policy exists only as a label; harness gone | **CLOSED in substance** | §10.3 carries the literal string. I executed both harness modules and confirmed `POLICIES["self"] == POLICIES["active"] == the spec's fenced block`, byte for byte. Harness committed. FR-005a makes "both mechanisms" a **requirement**, not evidence. MV-13 asserts the byte-exact string and forbids the "a policy is present" form. *Residuals: §11's A14 and §3's P-5 (R4-M-10); same-origin reachability never discussed (R4-M-01).* |
| **R3-C-03** Stage 0 unbuildable at `CleanRelPath` | **CLOSED in the argument; OPEN in the mechanism** | The split-by-purpose reframe is correct and §2.1 now says `CleanRelPath` is **MODIFIED**. The rule-set-as-a-value mechanism genuinely defeats R3-M-01. *But the create-side enforcement point still has no name, FR-0001c is a `MAY` under `MUST`-pass tests, and the test table still speaks in build tags — R4-M-04.* |
| **R3-C-04** token security envelope dropped | **CLOSED in substance** | FR-003d (15 min, logout + mount-revoke + delete revocation, in-memory store), FR-003e (never logged, naming the site), FR-003f (contract + route + GET/HEAD), FR-003g (authenticated path unchanged), MV-20, MV-23, §10.5, §10.6, tests 91/92/93. This is a faithful and in places improved rendering of D15.6. *Residuals: R4-M-02, R4-M-03.* |
| R3-M-01 Windows half never runs in CI | **CLOSED** | Rule set is a value; both verdicts exercised on one Linux runner; a `windows-latest` job, `GOOS=windows go vet`, and the workflow-comment correction are each named as a gate. The `cross-platform-extra.yml` phantom is independently verified: zero `windows-latest` across all 19 workflows, and the file appears nowhere in reachable history. |
| R3-M-02 vacuous cookie oracle in AS/DS | **CLOSED** | US-2 AS-1/AS-2 now require the **throw** plus `window.origin === "null"` plus a positive control; DS-5 row 5 rewritten; DS-5 row 7 rewritten from "native viewer" to PDF.js canvas + text layer. |
| R3-M-03 audio MIME points at an unreachable map | **CLOSED** | §2.1 corrected (`workspaceContentType` **not** modified, `handleLibraryDownload` **is**); FR-015a/b/c added with the `ServeContent`-sniffs and host-registry rationale; MV-24 gives the octet-stream default a live oracle. *Residual: FR-015c's minimum list — R4-C-04.* |
| R3-M-04 KB link renderer absent; FR-011 unscoped | **CLOSED** | FR-011 scoped to the KB reader with the chat rationale; FR-013a/b/c/d specify a second **composition** (not a second pipeline), naming `createLinkRenderer` and `commonMarkdownComponents` — both verified to exist as described; tests 85/86/87. |
| R3-M-05 "no new dependencies" false; PDF.js assets | **CLOSED** | §4 names `pdfjs-dist` 6.2.108 with a **measured** 5.5 MB real cost and the `pdf.sandbox*.mjs` exclusion; FR-018a/b/c/d; tests 80–84. FR-018b's `newSPAHandler`-returns-200 sharp edge is verified accurate (`embed.go:55-58`). |
| R3-M-06 no matrix; `retries: 3` on security assertions | **CLOSED as a requirement** | §13.4 is thorough: three scoped projects, `retries: 0`, engine install, shard assignment, headed only where §0 earns it, and a test asserting the config itself. *Residuals: the two spec files are never named (R4-M-06), and P-5 is stale (R4-M-10).* |
| R3-M-07 stale headed rule promoted into §0 | **CLOSED** | §0 narrows it to the two cases that need it, shows the derivation, and adds the third control (a genuine PDF must render in the same run or the result is inconclusive). |
| R3-M-08 FR-014 misleads; FR-019b unfalsifiable | **PARTIAL** | §10.7 is new and good — a proposed string, an honest "this is not a measurement", a freeze condition, and the FR-019b⊥FR-019c worker trap written out with its symptom. Test 96 was added for it. **But FR-014's final clause is unchanged, MV-21 still admits `default-src *`, and §16 points FR-019c at test 67 rather than 96 — R4-M-05, R4-M-13.** |
| R3-M-09 SC-013 unsatisfiable | **CLOSED** | Rewritten as a frozen pre-change fixture with exactly three intended diff groups; test 1 rewritten to fail on a fourth diff **and** on a missing intended one. |
| R3-M-10 FR-034a ⊥ E-5; chunking ≠ bounded indexer | **PARTIAL** | FR-034a now picks segmentation, explains why chunked reading bounds only the read buffer, and cites the `pkg/memrooms/index` precedent it deviates from. **E-5 is unchanged and still says "documented body cap, or skipped" — R4-M-11.** Test 62 still asserts "bounded peak memory" with no figure. |
| R3-M-11 test 0b guards the wrong layer | **NOT CLOSED** | Test 0b is byte-identical to round 3's. R4-m-06. |
| R3-M-12 five §17 decisions with no FR/test | **CLOSED** | §17 gained an enforcement table; FR-026, FR-038a, FR-039a, FR-050a, FR-109a added with MV-18/MV-19; tests 69/69a/70/71/72/73. Tests 69a and 70 in particular are model false-green-resistant designs. *Residual: `62a` is a phantom.* |
| R3-M-13 FR-0004 runes vs bytes; DS-3 lost the boundary | **PARTIAL** | FR-0004 rewritten correctly (255 bytes POSIX / runes Windows / no limit on read), with the 90-rune-CJK worked example. **DS-3 still has no >255-byte row, and `MaxRelPathLength` still appears zero times — R4-M-12.** |
| R3-M-14 form filling not out of scope in the spec | **CLOSED** | NB-17 added, explicitly reframing NB-13/NB-14 as reinforcements rather than carve-outs; tests 74 and 75, both with server-side halves. |
| R3-M-15 embedding mechanism; ADR-044 uncited | **CLOSED** | §10.6 gives `<iframe src>`-never-`srcdoc` with three reasons, the attribute set, the intersection rule, and an explicit ADR-044 reused/not-reused paragraph. FR-005b carries it. Test 95. |
| R3-M-16 FR-0003 tests the working case | **PARTIAL** | FR-0003 rewritten to RFC 6266 with the corrected `%q` evidence — the requirement is right. **AS-5, DS-3 row 5 and test 0c were not updated and still describe the double-quote case — R4-M-14.** |
| R3-M-17 FR-0002a names only the validating half; `.` uncovered | **CLOSED** | FR-0002a now names `replaceIllegalRunes`/`SanitizeComponent` and both untrusted ingest files; FR-0002b covers `.` **and** `..`. Verified: `TestValidateComponent`'s 48-case table contains **no** case for `.` or `..`, so FR-0002b is genuinely new coverage. |
| R3-M-18 `pathsafe` absent from §2.2 | **PARTIAL** | The call-site trust table in §4A is the better half of the ask and is accurate — I verified all four sites and the `notifications.sanitize` allow-list claim. **§2.2 still has five rows and none is `ValidateComponent`/`SanitizeComponent` — R4-m-05.** |
| R3-M-19 tests 59/61 are source-text scans | **CLOSED** | 59 rewritten as a two-table structural design with a per-case positive control and the explicit "a test that asserts nothing is not expressible" argument; 59a requires a `--self-test` wired ahead of the real run, matching `pr.yml:252-260` (verified verbatim); 61 rewritten as a two-phase runtime browser test; 61a added. |
| R3-M-20 allow-list never enumerated; `.svg` unanswered | **CLOSED as an enumeration** | §10.4 is the table three passes asked for. *Its `.svg` row is where the new findings are — R4-C-03, R4-M-06.* |
| R3-M-21 §16 omits six ACs | **CLOSED** | Counted mechanically: the ADR has 43 distinct `AC-x.y`; §16's table now has all 43. *Residual: the header still says 37 — R4-m-08.* |
| R3-m-01, m-04, m-07, m-08, m-09, m-10, m-11, m-12, m-13 | **NOT CLOSED** | Carried forward in §5 unchanged. |
| R3-m-02 FR-062 listed twice | **NOT CLOSED** | Still two rows, still pointing at tests 63 and 17. |
| R3-m-03 five FRs all trace to test 17 | **NOT CLOSED** | FR-060, FR-061, FR-063, FR-064, FR-065 all still → 17. |
| R3-m-05 deep-link encoding | **NOT CLOSED** | FR-012 unchanged. Sharper now: Stage 0 makes `#` and `?` legal in POSIX filenames. |
| R3-m-06 "reading mode" undefined | **NOT CLOSED** | §2.1 unchanged. |

---

## 3. CRITICAL

### R4-C-01 — There is a third URL that serves Library bytes inline, on the gateway origin, with no policy; three sections assert there are two

- **Lens:** Insecurity (Information Disclosure / stored XSS) / Incompleteness
- **Affected:** §10.4 (the `.svg` row), §10.5 (the two-URL table), FR-003g, MV-22, FR-014, US-2, H-6

§10.5 closes with a table headed **"Two URLs, deliberately different"**. FR-003g states it as a
requirement: *"Only the token path serves inline and only it carries the §10.3 policy."* §10.4's
`.svg` argument depends on it: *"fetched over the authenticated path, which serves attachments…
**Both URLs are closed, by different means.**"*

Verified on this branch, there is a third:

```go
// pkg/gateway/rest.go:5307
cm.RegisterHTTPHandler("/api/v1/media/workspace/", a.withOptionalAuth(a.HandleMediaByRef))

// pkg/gateway/rest.go:10184-10192  (serveMedia, reached from HandleMediaByRef)
h.Set("X-Content-Type-Options", "nosniff")
if meta.ContentType != "" { h.Set("Content-Type", meta.ContentType) }
if meta.Filename != ""   { h.Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", meta.Filename)) }
http.ServeFile(w, r, localPath)
```

`HandleMediaByRef` (`rest.go:10085-10095`) serves `/api/v1/media/workspace/{workspace}/{id}` and
resolves the id **through the owning workspace's Library** — its own error handling names
`library.ErrNotFound` and `Library.ResolvePathWithCaller`. So these are Library bytes. And
`pkg/library/entries.go:157` maps `.svg` → `image/svg+xml`, `.html` → `text/html`.

The composite: a Library `.svg` or `.html`, served **`Content-Disposition: inline`** with a
**scriptable `Content-Type`**, on the **gateway origin**, carrying **no
`Content-Security-Policy`**, behind **`withOptionalAuth`** (`rest_auth.go:311` — anonymous
pass-through is by design for this middleware). Opened as a top-level tab that is a document
that runs its own script next to the session cookie. That is the precise outcome US-2 is P0
for, and it is reachable without touching anything this feature adds.

This is not a defect the spec introduces — it exists today. It is a defect in the spec's
**threat model**, and the threat model is what the `.svg` decision rests on. A reader
implementing §10.4 will conclude that SVG is closed on every path, ship it, and be wrong.

- **Impact:** §10.4's stated justification for putting a scriptable format on the inline
  allow-list is unsound as written, because one of the "both URLs" it closes is not the only
  other one. FR-003g's guarantee is narrower than its wording. MV-22 ("the set served inline on
  the token path is exactly the allow-list") remains literally true while the property a reader
  takes from it — *inline serving is confined to the allow-list* — is false. H-6's hostile-page
  holdout would not catch it, because H-6 opens the file at its Library URL.
- **Fix:** (1) Enumerate **every** route that serves workspace or Library bytes, with its auth,
  disposition, type source and policy — `handleLibraryDownload`, the token path, `serveMedia`
  /`HandleMediaByRef`, and `rest_preview.go::serveStaticFile` (which uses
  `workspaceContentType`, `rest_workspace.go:87`, where `.svg` **is** mapped). Replace §10.5's
  two-row table with that. (2) Decide and state what happens to `serveMedia`: either it comes
  under the same allow-list and policy discipline, or it is scoped to a non-document type set,
  or its inline disposition is justified in writing. (3) Re-derive §10.4's `.svg` row against
  the corrected enumeration. (4) Add a test in the R4-C-03 gate that asserts the property for
  **every** route in the table, not just the token path.

### R4-C-02 — `src/components/library/` runs in no vitest group, so the spec's own HIGH-risk release gate would never execute

- **Lens:** Infeasibility / test integrity (`false-green-patterns.md` trap #4, verbatim)
- **Affected:** tests 1, 2, 87, 88, 89, 61a; SC-013, SC-016, SC-017; §13; §4

`false-green-patterns.md` §4 — *"A hardcoded allowlist decides what CI runs"* — records that
116 of 422 SPA test files never ran while the job reported green, that two patterns pointed at
long-deleted directories, and that it is *"now enforced by `scripts/check-vitest-coverage.mjs`,
which mirrors vitest's own filter semantics and fails with the exact uncovered list."*

Measured on **this branch**, not on main:

- `.github/workflows/pr.yml:196-209` hardcodes four groups:
  `src/lib/ src/store/ src/routes/ src/test/` · `src/components/chat/` ·
  `src/components/agents/ settings/ skills/ shared/ ui/` ·
  `src/components/layout/ command-center/ projects/`, consumed as
  `npx vitest run --reporter=verbose $VITEST_PATTERNS` (`:229`).
- `src/components/library/` **matches no group.** It already contains **seven** test files,
  including `LibraryPreviewPane.test.tsx` and `preview/libraryPreviewKind.test.ts` — the two
  files tests 1, 2, 88 and 89 extend.
- Totals: 426 test files under `src/`; 309 matched by the four groups; **117 (27%) run
  nowhere.** `src/components/workspaces/` and `src/components/browser/` are also uncovered — the
  same directories the false-green document names.
- `src/components/command-center/` **does not exist** (deleted per CLAUDE.md's retired-surfaces
  list) and `src/components/projects/` holds zero test files, so two of the eleven patterns are
  dead — *"the matrix looked broader than it was"*, reproduced exactly.
- `scripts/check-vitest-coverage.mjs` exists in
  `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/scripts/` and **does not exist on
  this branch.**

So the spec's most-cited guard — test 1, `TestClassifyLibraryEntry_TableDiffIsExactlyIntended`,
labelled **HIGH-risk guard** and made a release gate by SC-013 — is planned into a directory CI
does not execute. The same is true of test 88 (SC-016, whose own text says the failure mode is
*"every audio and PDF row downloads the whole file into an `<img>` that cannot render it"*),
test 89 (SC-017), test 87 and test 61a.

The asymmetry is the tell: §13.4 spends five numbered items on the equivalent Playwright
problem and correctly cites `scripts/e2e-shards.sh check` as the in-tree solution. The word
`vitest` appears **zero times** in the spec.

- **Impact:** Every SPA unit test in stage 1 ships green and unexecuted. The HIGH-risk change
  CLAUDE.md required an explicit acknowledgement for would go out with its guard inert, and
  nothing in the plan would reveal it — a green vitest job with the file absent from its output
  is indistinguishable from a green job with it passing.
- **Fix:** (1) Add `src/components/library/` (and, since it is the same one-line fix,
  `src/components/workspaces/` and `src/components/browser/`) to `pr.yml`'s matrix, as a named
  piece of work in §13 with the same weight §13.4 gives the Playwright projects. (2) Port
  `scripts/check-vitest-coverage.mjs` onto this branch and wire it as a gate — the spec's own
  argument for `e2e-shards.sh` ("a spec assigned to no shard cannot merge") applies unchanged.
  (3) Delete the two dead patterns. (4) Add a `§13.5` mirroring §13.4 for the SPA unit suite,
  and state for each of tests 1, 2, 61a, 85–89 which gate executes it.

### R4-C-03 — `FR-008a` does not exist, so the `.svg` decision has no requirement, and its only gate is an unassigned test

- **Lens:** Incompleteness / Insecurity
- **Affected:** §10.4, test 94, FR-008, MV-22, §16, §13.4

Mechanically checked: every `FR-\d+[a-z]?` referenced anywhere in the document, against every
`FR-` defined in bold. Exactly one is referenced and never defined: **`FR-008a`**, and its only
reference is test 94's `Traces to` column.

Test 94 is `E2E_SvgWithScript_TopLevel_IsInert` — the sole gate §10.4 places on `.svg`:

> *"This is reasoned from the measured HTML result, not separately measured. An SVG document
> under the §10.3 policy was never run. **Test 94 exists to close that gap and must pass before
> `.svg` ships inline.**"*

Four things are wrong with that gate:

1. **It traces to a requirement that does not exist.** There is nothing in §14 stating what an
   inline-served SVG must do.
2. **It is absent from §16.** Cross-checked mechanically: tests 27, 31, 37, 39, 59a, 61a, 74,
   75, 84, 85, 88, 89, **94** and 96 are defined in §13.1 and cited in no §16 row. For 74/75
   (NB-17) and 88/89 (SC-016/017) that is explicable — §16 lists only FR and MV rows. For 94 it
   is not, because 94's own trace target is the phantom.
3. **It has no engine assignment.** §13.4 item 1 scopes the three isolation projects'
   `testMatch` to *"the two isolation spec files"* — **which are never named anywhere in the
   document**. If test 94 is not in one of the two, it runs Chromium-only at `retries: 3`, which
   §13.4's own argument says makes a security assertion indistinguishable from a real one.
4. **The gate is prose.** "Must pass before `.svg` ships inline" has no MV, no SC, and no CI
   expression. Nothing prevents `.svg` reaching the allow-list table with test 94 skipped,
   quarantined or not yet written.

- **Impact:** The newest security decision in the document is reasoned rather than measured —
  which the spec says openly and honestly — and the mechanism it names to convert that reasoning
  into evidence is not wired to anything. The most likely outcome is that `.svg` ships and test
  94 does not, because no gate notices.
- **Fix:** (1) Write **FR-008a**: an inline-served SVG document MUST be inert under the §10.3
  policy — no script effect, no egress, `document.cookie` throws — asserted at its token URL
  with a positive control. (2) Add it and test 94 to §16. (3) Name the two isolation spec files
  in §13.4 and put test 94 in one of them, or add a third project. (4) Promote the ship gate to
  an **SC**, so the release checklist carries it.

### R4-C-04 — FR-015c's minimum type table omits `.css` and `.js`, which §10.4 requires to be served inline; with `nosniff` that breaks the flagship scenario

- **Lens:** Inconsistency (merge) / Infeasibility
- **Affected:** FR-015c, MV-24, §10.4, FR-003, FR-015, US-1 AS-4, tests 9, 59, 64

Three requirements written by different hands now disagree:

| Source | Says |
|---|---|
| **§10.4** | The inline allow-list includes `.css`, `.js`, eight raster image extensions, `.svg`, ten media extensions and `.txt`/`.md`/`.json` — served on the token path **without** `attachment`. |
| **FR-015c** | The type table *"MUST cover **at minimum** the seven audio extensions, `.pdf`, `.html`/`.htm`, and the four webfont extensions"*. **`.css`, `.js`, images, `.svg` and the inert-text group are not in the minimum.** |
| **MV-24 / FR-015c** | *"An extension absent from the table MUST be served `application/octet-stream` as an attachment — one stated default, no second guess, no sniff."* |
| **FR-015** | *"MUST send `X-Content-Type-Options: nosniff` on **every** inline response."* |

An implementation that satisfies FR-015c's stated minimum and nothing more serves
`style.css` and `app.js` as `application/octet-stream` with `nosniff`. **Every browser refuses
both**: `nosniff` blocks a stylesheet whose type is not `text/css` and a script whose type is
not a JavaScript MIME type. That is the whole content of US-1 AS-4 — *"all four load and the
page appears styled, scripted and correctly typeset"* — and of tests 9 and 64.

There is a second-order contradiction inside the same clause: MV-24 says an absent extension is
served **as an attachment**, while §10.4 says the allow-list decides disposition and MV-22 says
the inline set is *exactly* the allow-list. For `.css` those two rules give opposite answers.

The webfont half of FR-015c is right and was well-earned — `.woff2`/`.woff`/`.ttf`/`.otf` are
indeed absent from `workspaceContentType` (`rest_workspace.go:87-102`, verified). The defect is
that the minimum list was written against the *audio* problem and never re-derived after §10.4
enumerated the allow-list.

- **Impact:** As written the spec is internally unsatisfiable for the headline scenario. An
  implementer following FR-015c ships a preview where no bundle stylesheet or script loads, and
  discovers it at test 9 — the last test in stage 1's browser tier.
- **Fix:** Make the two tables one relation. State that **the type table MUST contain an entry
  for every extension on the §10.4 allow-list**, plus `.pdf` (which is fetched, not navigated),
  and that MV-24's octet-stream default applies only to extensions on **neither** list. Add
  `.css`, `.js`, the eight raster types, `.svg` and `.txt`/`.md`/`.json` to FR-015c's minimum,
  or replace the minimum with the derived rule. Give test 59's table the type assertion as well
  as the confusion case, so the two lists cannot drift again.

---

## 4. MAJOR

Ranked by what goes wrong soonest and worst.

### R4-M-01 — FR-006 says "block network egress"; the measured policy permits same-origin requests to any gateway path, and the experiment never measured that

**Affected:** FR-006, §10.3, §11, test 11, US-2 AS-3, H-6

The experiment's ground truth is *"which paths each server actually received"*, where the
second server *"is a DIFFERENT ORIGIN standing in for 'the internet'"* (harness header,
`server.py:5-9`). Every "zero of seven egress vectors" row is a statement about **the second
origin**. Same-origin reachability was never a measured column.

The shipped policy permits it broadly: `script-src 'self'`, `style-src 'self'`,
`img-src 'self' data: blob:`, `font-src 'self'`, `media-src 'self'`, **`frame-src 'self'`**. And
§10.3's own measurement note explains why `'self'` is permissive here: *"`'self'` resolves
against the policy's self-origin — the URL the protected resource was served from — not the
document's (opaque) origin."* In the experiment that origin hosted a fixture directory. In
production it hosts `/api/v1/*` and the SPA.

Two concrete consequences the spec does not state:

- **`<img src="/api/v1/…?x=stolen">` reaches the gateway.** Unauthenticated — the cookie is
  `SameSite=Strict` (`session_cookie.go:148`) and the opaque origin makes it a cross-site
  request, which the experiment corroborates (§4.2: *"Cookies stop being sent to the page's own
  subresources"*). But it is reachable traffic from untrusted content, it lands in the request
  log, and FR-006's plain wording forbids it. `connect-src 'none'` closes `fetch`/XHR/beacon/
  WebSocket; it closes nothing that loads a subresource.
- **`frame-src 'self'` lets a previewed page embed the real SPA.** The nested context inherits
  the sandbox flags, so it cannot read cookies — but it renders genuine Omnipus chrome inside
  attacker-authored content, which is H-6's *"draw a convincing Omnipus login form"* made
  easier rather than harder. §10.7's proposed `frame-ancestors 'none'` would close it, but
  §10.7 is explicitly a proposal and **no requirement or MV mandates that directive** (MV-21
  asserts only that a policy exists and omits `'unsafe-eval'`).

§10.3 gives a rationale paragraph for `allow-same-origin`, for `allow-popups`/`allow-forms`/
`allow-downloads`, for `'unsafe-inline'` and for the absent `frame-ancestors`. It gives none for
`frame-src 'self'` or `media-src 'self'`, and none for the difference between the experiment's
origin and the gateway's.

- **Fix:** (1) Restate FR-006 as what was measured and what ships: *"MUST block egress to any
  origin other than the gateway's own, and MUST NOT permit `fetch`/XHR/beacon/WebSocket to any
  origin including its own."* (2) Add a §10.3 paragraph on the production-vs-experiment origin
  difference and what same-origin subresource loads remain possible. (3) Justify `frame-src
  'self'` explicitly, or tighten it — a preview bundle that frames a sibling page needs it; one
  that does not, does not. (4) Promote `frame-ancestors 'none'` on the SPA from §10.7's proposal
  into FR-019b, since it is the control for the framing case and costs nothing to require.

### R4-M-02 — §10.5 specifies five token properties that no requirement, constraint or test carries

**Affected:** §10.5, FR-003b/d/f, MV-20, MV-23, tests 65, 91, 92

§10.5 is a good table. Five of its rows exist only there — §10 is *"Explicit Non-Behaviors and
Safeguards"*, §10.5 is neither a `MUST` nor an `MV`, and §16 has no row for it.

| §10.5 property | FR? | MV? | Test? |
|---|---|---|---|
| Lifetime 15 minutes | FR-003d | MV-20 | 91 |
| Revocation set | FR-003d | — | 92 |
| Route + GET/HEAD only | FR-003f | — | **no test asserts the verb restriction** |
| Minting / scope | FR-003b | — | 65 |
| **Shape — 32 random bytes from a cryptographic source, base64url** | **none** | **none** | **none** |
| **Renewal — the SPA re-mints and reloads the frame** | **none** | **none** | **none** |
| **Path containment within the token's scope** | **none** | **none** | **none** |
| **Rate limiting on the token path** | **none** | presumed by MV-23 | 93 (presumes a 429 is reachable) |
| Storage in-memory | FR-003d | — | — |

The entropy row is the whole security of an unauthenticated bearer path and it is a prose
sentence. The GET/HEAD row is load-bearing in a different way: FR-003f uses it as the **reason**
the token path needs no CSRF exemption (*"since that middleware gates state-changing verbs
only"*), and nothing asserts it.

**Path containment is the most serious of the five.** The token path is a brand-new,
unauthenticated, path-addressed file server: `/library-preview/<token>/<relative-path>`. FR-0002
keeps containment unconditional *for `CleanRelPath`*, and nothing says the token handler uses
`CleanRelPath`, or `os.Root`, or anything at all. The string `os.Root` appears zero times in the
spec. This lands in the same stage as Stage 0, which is simultaneously **relaxing** name-shape
validation on the read path — so the one handler where a missed containment check is
unauthenticated is the one handler with no stated containment requirement.

Two smaller gaps in the same area: the mint endpoint is a `POST` that creates a credential and
has no stated rate limit or per-session live-token cap, against an in-memory store — a trivially
reachable memory-growth path for an authenticated user; and MV-23 requires *forcing a 429 on the
token path*, which only works if the path is rate-limited, a fact no requirement establishes.

- **Fix:** Promote all five to requirements. **FR-003h:** token entropy and encoding, asserted.
  **FR-003i:** the token path MUST resolve `<relative-path>` through the same containment chain
  as the authenticated path and MUST confine at the syscall boundary; add a test to the Stage-1
  table. **FR-003j:** GET and HEAD only, every other verb 405, asserted. **FR-003k:** the token
  path is rate-limited (which MV-23 already assumes). Add a live-token cap and a mint rate limit.

### R4-M-03 — Token renewal is not buildable as described, and copying the named precedent defeats the TTL rationale

**Affected:** §10.5 (Renewal), FR-003c, FR-003d, MV-20

§10.5: *"If a preview outlives its token, the SPA re-mints (an authenticated request, so no
widening) and reloads the frame. FR-003c's visible error is the fallback when re-minting
fails."*

Three problems.

1. **The SPA cannot detect that the frame's request failed.** The preview is a cross-origin,
   opaque-origin, sandboxed iframe. `iframe.onload` fires for an error page exactly as it fires
   for content, and the embedder can read neither the status nor the body. So "if a preview
   outlives its token" has no observable trigger — renewal has to be a **timer** keyed to
   MV-20's constant. That is fine, and it is a different design from the one the sentence
   describes.
2. **FR-003c's "visible error rather than a blank frame" therefore has to be served by the
   token handler**, as a human-readable body, in response to a request bearing an expired
   token — content served on the token path *without* a valid token, which MV-13 says must
   still carry the §10.3 policy. None of that is stated, and test 66
   (`TestPreviewResponse_NoReferrerAndVisibleExpiry`) has no note describing the oracle.
3. **The named precedent renews in place.** §10.5's Shape row says *"matching
   `pkg/agent/served_subdirs.go`"*. Verified: on re-registration of the same directory that
   file does **not** mint a new token — it renews the existing entry and returns **the same
   token string** (`served_subdirs.go:137-151`), carrying `FirstIssued` forward so the 24-hour
   ceiling bounds total life rather than time-since-renewal. An implementer copying the cited
   file gets a token that survives for as long as the tab is open **with an unchanged value**,
   which is precisely the property §10.5's lifetime rationale relies on not holding (*"short
   enough that a token found later in a log is already dead"*).

Reloading the frame also discards the previewed document's state — scroll position, any
interaction in an agent-authored report — every fifteen minutes, silently. That is a product
decision nobody has made.

- **Fix:** Specify renewal as a timer at a stated fraction of MV-20, state that re-minting MUST
  produce a **new token value** and invalidate the old one, specify the expired-token response
  as a policy-carrying human-readable error, and say what happens to frame state on reload (or
  extend rather than reload, which requires the handler to accept a second live token for the
  same scope).

### R4-M-04 — Stage 0's mechanism paragraph rejects build tags; the requirements and the test table still speak in build tags, and the create-side enforcement point has no name

**Affected:** FR-0001a, FR-0001c, tests 0a, 0e, 0f, 0g, 0h; §2.1

The re-layering is right and R3-C-03's central objection is answered. Three residuals, all from
incomplete propagation.

1. **No named enforcement point on the create side.** FR-0001a: *"MUST evaluate them **after
   root resolution**, so the destination's population is known."* Which function? §2.1 lists no
   new symbol. `CleanRelPath` is called by all eleven `rest_library.go` handlers — read *and*
   write (upload, mkdir, rename, move/copy) — so something must now apply shape rules after
   `openLibraryRoot` in the write handlers, and nothing names it. This is the same defect class
   as R3-C-03, one layer up and much smaller, but an implementer still has to invent it.
2. **FR-0001c is a `MAY` under `MUST`-pass tests.** *"The system **MAY** apply Windows-safe
   naming to workspace storage."* Test 0a requires *"the same name is accepted for READ and
   rejected for CREATE in workspace storage"*; test 0e requires the existing assertions to hold.
   An implementation applying nothing satisfies FR-0001c and fails both tests. One of the two is
   wrong.
3. **The test table was not rewritten to the new mechanism.** The mechanism paragraph is
   explicit that the split is *"a **parameter, not a compile-time fork of behaviour**"*, chosen
   by `GOOS`, *"**not** a custom build tag, which would be a runtime footgun in platform
   clothing"*. The tests still say: 0a *"on a Windows build"*; 0e *"under the Windows tag"*;
   0g *"under **every** build tag"*; 0h *"MUST run under **every** build tag — it is vacuous on
   Windows, where the mutated rule is still on"*. Under a rule-set value there are no build tags
   to run under; there are two values, both exercised on one runner — which is the mechanism's
   whole selling point and the thing that closed R3-M-01. Test 0h's vacuity note in particular
   is now wrong: with the rule set as a parameter, 0h is non-vacuous for both values.

- **Fix:** Name the create-path enforcement function and add it to §2.1 and §2.2. Change
  FR-0001c to `MUST`. Rewrite rows 0a, 0e, 0g and 0h in terms of "with the POSIX rule set" and
  "with the Windows rule set", and state which single fact needs the `windows-latest` job (the
  selection), matching the mechanism paragraph's own conclusion.

### R4-M-05 — §16 points FR-019a and FR-019c at the call-site test that FR-019a and §10.7 both say cannot detect the failure

**Affected:** §16, FR-019a, FR-019c, tests 67, 84, 96; AC-15.10

FR-019a's correction is the best single paragraph added this round:

> *"an earlier revision required asserting `isEvalSupported: false` at the call site. **That
> option no longer exists**… Asserting a key PDF.js ignores would have passed forever while
> proving nothing: a security requirement that could not fail. The property is instead asserted
> against the **shipped artefact**."*

§10.7 makes the matching point for FR-019c:

> *"If `worker-src` does not permit the URL the built worker resolves to, **PDF.js does not
> fail — it falls back to parsing on the main thread**… test 96 asserts the *thread*, not the
> configuration."*

§16 then says:

| Requirement | §16's test | The test §13.1 actually wrote for it |
|---|---|---|
| FR-019a | **67** (`TestPdfJs_HardeningFlagsAtCallSite`) | 67 **and 84** (`TestSpaEmbed_PdfSandboxNotShipped` — the shipped-artefact assertion) |
| FR-019c | **67** | 67 **and 96** (`E2E_PdfJs_ParsesOnRealWorker`) |
| FR-011 | **6** | 6 **and 85** (`TestChatMarkdown_PrivateCommentsNotHidden` — the regression FR-011 exists to prevent) |
| FR-016 | **59** | 59 **and 59a** |
| FR-034a | 38, 62 | 62 (and §17 cites a non-existent `62a`) |

So the matrix — the artefact a QA engineer reads to decide what to write — points three
corrected requirements back at the tests their corrections replaced. §16 is also missing the
requirement that FR-019a's third obligation creates: *"MUST ensure no `eval` path exists in the
shipped PDF.js bundle"* has **no test at all**. Test 84 asserts `pdf.sandbox*.mjs` is absent,
which is the third obligation, not the first.

- **Fix:** Add 84 to FR-019a, 96 to FR-019c, 85 to FR-011, 59a to FR-016. Add a test asserting
  zero `eval(` / `new Function(` in the built SPA output (the artefact assertion FR-019a
  specifies), and cite it. Re-derive §16 mechanically — this is the fourth pass in which a
  hand-maintained matrix has fallen behind a prose decision.

### R4-M-06 — `.svg`'s inclusion rests on an unverified browser-behaviour claim, and the option that needs no measurement is never evaluated

**Affected:** §10.4, FR-008, FR-014, test 94, MV-22

The containment half of §10.4's `.svg` argument is sound: one policy over every byte on the
token path does extend the measured HTML result to an SVG document, and the spec is properly
honest that this is reasoned rather than measured.

The **inclusion** half is not. The stated reason is:

> *"Excluding it would break `<img src="logo.svg">` inside ordinary bundles."*

Excluding an extension from the allow-list means, per §10.4's own definition, serving it with
`Content-Disposition: attachment`. `Content-Disposition` governs **navigation**, not subresource
loading — an `<img>` renders an attachment-dispositioned response normally. §10.4 asserts the
opposite, as fact, in the sentence that justifies putting the one scriptable non-HTML format on
the list. This is the class of claim the rest of the document insists on measuring; here it is
reasoned, and it points the other way.

**The middle option is never considered.** Serve `.svg` on the token path with the correct
`image/svg+xml` type **and** `Content-Disposition: attachment`: bundles keep working (subresource
loads ignore the header), and a top-level navigation downloads instead of creating a scriptable
document. That closes the case §10.4 is worried about without needing test 94 to succeed, and
without shipping a scriptable format on reasoning alone. It costs one column in the allow-list
table — disposition, alongside inline-vs-attachment — which the table currently treats as a
single binary.

A third context is unexamined: `.svg` inside the KB reader, where FR-060 requires `![[image.svg]]`
embeds to render. That path is `<img>`-based today (`MarkdownImage`) and therefore inert, but
nothing records it as load-bearing.

- **Fix:** Either measure the `<img src>`-under-`attachment` claim and cite it, or drop it and
  justify `.svg` on the containment argument alone. Evaluate the type-plus-attachment option
  explicitly and record why it was rejected if it is. Split §10.4's table into *type* and
  *disposition* columns. State the KB-embed context alongside the other two.

### R4-M-07 — MV-23 claims "no log line anywhere"; there are six sites recording a request path, five raw, one of them a persisted audit record

**Affected:** MV-23, FR-003e, test 93

FR-003e names one site and requires a general property: *"Every site recording a request path
MUST route it through one redaction helper."* MV-23 states the property universally: *"**No log
line anywhere in the gateway** contains a preview token."* Test 93 asserts it at exactly one
site, by driving a 429.

Enumerated on this branch — every site in `pkg/gateway` that records a **request** path:

| Site | What it records | Redacted? |
|---|---|---|
| `rest_auth.go:289` | `"path", r.URL.Path` — WARN on 429 | raw *(the one FR-003e names)* |
| `middleware/csrf.go:381` | `"path", r.URL.Path` — WARN, CSRF re-mint failure | raw |
| `middleware/bypass_gate.go:52` | `"path", r.URL.Path` — WARN, admin route blocked | raw |
| `gateway.go:4619` | `"route", route` — WARN, CSRF mismatch | raw |
| **`gateway.go:4630`** | **`audit.Entry.Details["route"] = route` — a persisted audit record** | **raw** |
| `rest_preview_audit.go:253/268/280` | `sanitised_path` | **redacted**, via `sanitisePreviewPath` |

Two things follow. First, a universal claim with a single-instance oracle is the shape
`false-green-patterns.md` warns about — test 93 passes with four raw sites untouched. Second,
site 5 writes the path into the **audit log**, which is HMAC-chained and retained: a token there
outlives `gateway.log` rotation entirely, and FR-003e's *"MUST NOT reach any log"* is strictly
violated in the most durable store the product has.

The spec also does not cite the helper that already exists. `sanitisePreviewPath`
(`rest_preview_audit.go:326-344`) is the in-tree precedent for exactly this — though it is
token-shape-specific (it replaces the third path segment) and would need generalising, which is
worth saying.

- **Fix:** List all six sites in FR-003e. Require the shared helper and cite
  `sanitisePreviewPath` as the pattern, noting it must be generalised. Extend test 93 to drive
  **each** raw site (429, CSRF mismatch, bypass gate) with a token-bearing path, and assert the
  audit **record** as well as the log line.

### R4-M-08 — Three requirements' only test is a test id that does not exist

**Affected:** FR-0001d, FR-015b, FR-015c, AW-7; §16, §17

Cross-checked mechanically against §13.1 and the Stage-0 table:

| Cited | By | Defined? |
|---|---|---|
| **`0l`** | §16, FR-0001d | **No.** Stage 0 defines 0a–0h. |
| **`3a`** | §16, FR-015b and FR-015c | **No.** §13.1 defines test 3 only. |
| **`62a`** | §17, AW-7 | **No.** |

The two that matter are security requirements, and both are ones this review pass would
otherwise have recorded as well-written:

- **FR-0001d** is the requirement that stops Stage 0's relaxation leaking into the sanitising
  path — the one whose callers are, in the spec's own words, *"the filename an attachment
  carries from **Discord, Telegram, Feishu, QQ** … **Untrusted and remote — attacker-chosen**"*.
  NB-18 restates it as a prohibition. Its entire enforcement is `0l`.
- **FR-015b** is the host-MIME-registry requirement whose own text ends *"The test written on
  the Mac passes; the shipped container is broken."* Its entire enforcement is `3a`.

- **Fix:** Define `0l` (`SanitizeComponent`'s output for a remote-supplied name is byte-identical
  under both rule sets), `3a` (the type table is consulted and `mime.TypeByExtension` is not —
  provable by asserting `.aac` and `.woff2` resolve correctly in an environment with no system
  MIME database), and `62a` or remove the citation.

### R4-M-09 — The FR-071 rewrite retired a case ADR AC-17.2 still requires, and §16 marks it covered

**Affected:** FR-071, AC-17.2, test 35, §16

FR-071's correction is right and well-evidenced — I verified `repairAndValidateToolPolicyCoverage`
(`gateway.go:934-948`): repair runs first, writes `ToolPolicyDeny` (`validate.go:565`), logs a
WARN, and boot does not abort. The old requirement's premise was indeed wrong.

But the ADR's acceptance criterion is unchanged: **AC-17.2** — *"Loading a config written before
this ADR yields the seeded posture, not `deny`."* FR-071 explicitly deletes that case:
*"there are no prior installs to migrate."* §16's AC table nonetheless maps AC-17.2 → test 35,
and §16's own preamble asserts *"Every ADR `AC-x.y` maps to a named test."* Test 35 now asserts
something else — that the repair returns zero `knowledge_*` pairs on a seeded config.

**Test 35 has a second problem, from the same verification.** `ValidateToolPolicyCoverage`
returns `nil` early when `len(knownTools) == 0` (`validate.go:449-451`), and
`RepairIncompleteToolPolicyCoverage` derives its gap list from that same call (`validate.go:534`).
So "the repair returns zero `knowledge_*` pairs" passes **vacuously** in any harness that does
not populate the builtin tool registry — which an integration test constructing a config by hand
very plausibly would not. The test needs a positive control: a deliberately omitted tool must
produce exactly one repaired pair.

A third fact worth carrying: repair mutates the in-memory `*config.Config` only, with no
write-back, so the WARN recurs on every boot rather than being a one-time event.

- **Fix:** Amend AC-17.2 in the ADR to match the spec's corrected premise, or state in §16 that
  it is retired and why. Give test 35 the positive control and require a populated registry.
  Record the no-write-back behaviour in FR-071.

### R4-M-10 — Four sections still carry verdicts the same revision retired

**Affected:** §3 P-5, §11, §16 AC-15.4, spec header

Each is a one-line edit, and each contradicts something else in the same document.

1. **§11 still names the A14 fallback:** *"if no single-origin policy satisfies both isolation
   and subresource loading, fall back to serving previews from a distinct origin (ADR
   alternative A14)."* §0 row 3 says *"the distinct-origin fallback is retired"*; the experiment
   §2.1 says *"The A14 fallback is not needed."* R3-C-02's fix item asked for this explicitly.
2. **§3 P-5 still reads "Required for stage 1 — Chrome, Firefox, Safari."** §13.4 now specifies
   Chromium/Firefox/WebKit, and SC-012 says plainly *"Safari proper is not covered… Earlier
   drafts promised coverage nobody is building."* P-5 is one of those drafts.
3. **AC-15.4 is mapped to the wrong test, and cannot be satisfied.** The ADR (line 605):
   *"A `.pdf` renders in the preview pane via PDF.js in Chrome, Firefox and **Safari**."*
   §16 maps it to **test 12**, `E2E_PreviewIsolation_BrowserMatrix` — an isolation test whose own
   row says *"Not Safari proper — see SC-012"*. Test 57 (the actual PDF test) also claims
   AC-15.4. So one AC has two claimants that disagree, and neither delivers what it requires,
   while §16's preamble asserts complete coverage.
4. **The header still says "37 acceptance criteria"** where §16 now correctly lists 43.

- **Fix:** Retire A14 from §11 with a one-line reason. Rewrite P-5 to point at §13.4's work
  list and say WebKit. Either amend AC-15.4 to WebKit in the ADR or record it in §16 as
  knowingly unmet with SC-012 as the reason. Correct the header count.

### R4-M-11 — E-5 still contradicts FR-034a, and test 62 still has no number

**Affected:** E-5, FR-034a, AW-7, MV-2, test 62

FR-034a is now a real design — segmentation at 8 MB, absolute byte offsets, hits collapsing to
one result scored by best segment, streaming for links/backlinks/outline — with an explicit
statement that it deviates from the `pkg/memrooms/index` precedent it copies. That answers the
mechanism half of R3-M-10.

E-5 is unchanged: *"Note 200 MB in size | Indexed with a **documented body cap**, or **skipped**
and reported — never an unbounded read."* FR-034a: *"MUST NOT impose a maximum note size — no
note refused, skipped or truncated."* Both are current text, and E-5 is a section a test author
reads. AW-7's own wording is also now stale — it still says *"Memory safety comes from **reading
files in chunks**"*, the mechanism FR-034a's new paragraph exists to say is insufficient.

Test 62 is *"A 200 MB note is fully indexed with **bounded peak memory** — never skipped, never
capped"*, with no figure and no harness named. R3-M-10's fix item asked for both. MV-2's 512 MB
covers the whole first index, not a single note, so it cannot serve as the number.

- **Fix:** Rewrite E-5 to FR-034a's verdict. Rewrite AW-7's mechanism sentence to segmentation.
  Give test 62 a peak-RSS figure and name the harness that reads it.

### R4-M-12 — DS-3 still has no case above the filesystem limit, and `MaxRelPathLength` is still unmentioned

**Affected:** FR-0004, DS-3 row 7, test 0d, US-0 AS-4

FR-0004 is now correct: POSIX builds enforce 255 **bytes** on creation, Windows builds keep the
rune budget, the read path has no limit, with the 90-rune-CJK worked example. That is the
requirement R3-M-13 asked for.

The dataset was not updated. DS-3 row 7 is still the **106-character** basename, and test 0d is
still `TestLibrary_LongFilenameOpens` at 106 characters. 106 runes is comfortably inside 255
bytes for Latin text and passes under any design, correct or not. **There is no case anywhere in
DS-3 that exercises FR-0004's byte rule**, so the requirement that changed has no dataset row and
no test that could distinguish a correct implementation from the current one.

`MaxRelPathLength` (200 runes, `pathsafe.go:123`, enforced at `root.go:402` after the
per-component loop) appears **zero times** in the spec, across four passes. It is the limit a
deep vault hits before any component limit.

- **Fix:** Add a DS-3 row for a name above 255 bytes (e.g. 90 CJK runes = 270 bytes) with the
  expected verdict: creatable-and-rejected-cleanly on POSIX, addressable if already on disk.
  Extend test 0d or add one. State FR-0004's answer for `MaxRelPathLength`.

### R4-M-13 — MV-21 still admits `default-src *`, so §10.7's careful policy is unenforced

**Affected:** FR-019b, MV-21, §10.7, test 68, AC-15.9

§10.7 is honest and useful — a proposed string, an explicit *"This is a proposal, not a
measurement"*, each assumption paired with the symptom if wrong, a freeze condition, and the
FR-019b⊥FR-019c worker trap written out. That is a better artefact than round 3 asked for.

Nothing binds it. FR-019b requires *"a Content-Security-Policy"* with no directives. MV-21
requires the header to exist and to contain no `'unsafe-eval'`. Test 68 is
`TestSpaServedWithCSP` with no note. So `Content-Security-Policy: default-src *` satisfies
FR-019b, MV-21, test 68 and AC-15.9 simultaneously — the exact false-green R3-M-08 raised, moved
from "no directives specified anywhere" to "directives specified in a section nothing asserts
against".

The asymmetry with §10.3 is the point: MV-13 asserts the preview policy **byte-identically** and
says why (*"a constraint that only says 'a policy is present' is satisfied by `default-src *`"*).
The SPA policy gets no equivalent, though it is the origin holding the session cookie.

**FR-014 is also still unchanged** on the clause R3-M-08 named: *"…are drawn by SPA components,
never become browser documents, and **therefore have no sandbox to apply**."* FR-019a's own
preamble contradicts it. FR-017 instructs the documentation to follow FR-014's framing.

- **Fix:** Give MV-21 a directive floor that can fail — at minimum no `'unsafe-eval'`,
  `object-src 'none'`, `base-uri 'none'`, `frame-ancestors 'none'` and an explicit `connect-src`
  — and state that the full string becomes byte-asserted once §10.7's freeze condition is met,
  with the freeze itself an SC. Rewrite FR-014's final clause to say those formats are parsed in
  the SPA's own origin and their containment is the parser's correctness.

### R4-M-14 — FR-0003 moved to RFC 6266; its scenario, dataset row and test did not

**Affected:** FR-0003, US-0 AS-5, DS-3 row 5, test 0c

FR-0003 is now right, and its evidence is right — I re-ran the `%q` cases and confirm the
round-2 claim it corrects was false. The requirement is: *"MUST build `Content-Disposition` to
**RFC 6266**, emitting `filename*=UTF-8''<percent-encoded>` for any non-ASCII name… **The real
gap is non-ASCII**."*

Everything downstream still describes the case the requirement says already works:

- **US-0 AS-5:** *"Given a filename containing a **double quote**, When it is downloaded, Then
  the response headers are correctly quoted and not malformed."*
- **DS-3 row 5:** `Ünïcödé — Näme.md` → *"fully addressable"* — addressing, not downloading,
  which is the axis FR-0003 is about.
- **Test 0c:** `TestLibrary_QuoteInFilenameHeaderSafe`, noted *"Header injection via filename"*.

A QA engineer writing from AS-5 and test 0c writes an assertion against behaviour the spec
itself documents as already correct, and FR-0003's actual requirement ships untested.

- **Fix:** Rewrite AS-5 to a non-ASCII name and a `filename*` assertion. Add a DS-3 row asserting
  the *downloaded* header for `Ünïcödé — Näme.md` and for a name containing a semicolon. Rename
  test 0c and restate its oracle as "the response carries both an ASCII `filename=` fallback and
  a correct `filename*=UTF-8''…`".

### R4-M-15 — §13.4's browser matrix is scoped to "the two isolation spec files", which are never named, while nine tests need it

**Affected:** §13.4, SC-012, tests 10, 11, 12, 57, 60, 64, 67a, 94, 95, 96

§13.4 item 1: *"`isolation-chromium`, `isolation-firefox`, `isolation-webkit`, each with
`testMatch` limited to **the two isolation spec files**"*, with the correct reasoning that an
unscoped Firefox project would re-run ~50 real-LLM specs.

The two files are not named, and only three test rows carry a spec filename at all
(`preview-pdf.spec.ts` for 61 and 75, `viteConfig.test.ts` for 61a). The tests that need
multi-engine, headed, or `retries: 0` coverage are: 10, 11, 12, 57, 60, 64, 67a, 94, 95, 96 —
ten of them, spanning isolation, PDF rendering, fonts, bundle loading, hostile PDF, SVG, frame
composition and worker isolation. They will not fit two files without arbitrary grouping, and
whichever ones land outside get Chromium-only at `retries: 3` — which §13.4's own second
paragraph says is indistinguishable from a passing security assertion.

§13.4 item 5 is otherwise excellent (a test asserting the config itself, with the failure it
catches spelled out). It should assert the `testMatch` set too, or the scoping silently drifts
the same way.

- **Fix:** Name the files. Assign every one of the ten tests to a named spec file and state its
  project set (three engines / Chromium only) and headed-ness in the §13.1 row. Extend item 5's
  config test to assert each project's `testMatch` covers the named files.

### R4-M-16 — Test 61a requires a named PDF.js chunk group that `vite.config.ts` has no mechanism for today, and no build symbol is in §2.1

**Affected:** tests 61, 61a; FR-018, AC-15.6, §2.1, §2.2

Test 61 identifies the PDF.js chunk **by name**; test 61a exists to stop that name changing,
with the right reasoning (*"otherwise test 61 passes by matching nothing, the precise failure of
grepping a hashed filename"*).

Verified: `vite.config.ts:167-180` uses the **function** form of `manualChunks(id)` and returns
exactly four names — `react`, `router`, `motion`, `icons`. There is no pdf chunk and no pdf
dependency (`pdfjs-dist` is absent from `package.json`, as expected for a new dependency). The
file's own header (`:159-166`) records that Vite 8 dropped the object form. So a named chunk
group is a change to `vite.config.ts`'s `manualChunks` that must be specified, and a lazy
`import()` alone will not produce one — it produces a hash-named chunk, which is exactly what
61a is written to prevent.

Neither `vite.config.ts` nor the SPA-serving handler (`newSPAHandler`, `pkg/gateway/embed.go:21`
— which FR-018b requires to start returning 404 under the asset prefixes, and FR-019b requires to
start emitting a CSP) appears in §2.1 or §2.2. Two build/serving changes with real blast radius
have no impact assessment, while `classifyLibraryEntry` has a full one.

- **Fix:** State the chunk name and that it comes from a fifth `manualChunks` branch. Add
  `vite.config.ts::manualChunks` and `newSPAHandler` to §2.1/§2.2 with impact ratings.

---

## 5. MINOR

- **R4-m-01** §0's rows 1–5 carry no section citation. R3-C-01's fix item 4 asked for a rule that
  every row names the section it comes from; rows 6–8 comply, rows 1–5 do not.
- **R4-m-02** *"29 assertions in `pkg/pathsafe/pathsafe_test.go` locking in current behaviour"*
  (§4A) and test 0e's *"The 29 existing assertions still hold"*. Actual: **8** test functions,
  **25** `assert`/`require` call sites, **48** table cases in `TestValidateComponent` alone plus
  19 in `TestSanitizeComponent`. The number 29 has no referent, and a test named for it cannot be
  written.
- **R4-m-03** *"all **12** non-test callers in `rest_library.go`"* (Stage 0 layering note).
  There are **11**, at lines 234, 276, 308, 358, 385, 533, 571, 609, 614, 668, 673 — the same
  eleven round 3 listed while also saying twelve. They are the only non-test callers in the repo.
- **R4-m-04** NB-16 and FR-0002a say C0 controls and Windows characters are fused *"in one
  predicate"* in `firstIllegalRune`. Verified: `firstIllegalRune` (`pathsafe.go:334-339`) uses
  **two sequential `if` statements**; `replaceIllegalRunes` (`:277`) uses the single `||`. The
  rationale names the two functions the wrong way round, and the practical consequence is
  inverted — the split is nearly free in the validating function and is the real work in the
  sanitising one.
- **R4-m-05** §2.2's impact table still has five rows and still omits `pathsafe.ValidateComponent`
  (CRITICAL, 17 impacted) and `SanitizeComponent`. §4A's trust table is the better half of
  R3-M-18's ask and is accurate; the impact table itself was not updated.
- **R4-m-06** Test 0b (`TestPathsafe_TraversalStillRefused`, labelled *"The guard that must not
  regress"*) is unchanged and still targets a package whose own doc says *"Callers remain
  responsible for rejecting separators, `.`, `..`, NUL, and absolute paths themselves"*
  (`pathsafe.go:152-155`). R3-M-11 unaddressed.
- **R4-m-07** §16 lists **FR-062 twice**, with tests 63 and 17, out of numeric order. R3-m-02
  unaddressed. FR-060/061/063/064/065 all still trace to test 17, a link-resolution unit test
  that cannot verify callouts, highlights, frontmatter suppression, backlink display or rail
  collapse. R3-m-03 unaddressed.
- **R4-m-08** The header says *"37 acceptance criteria"*; §16 now correctly covers 43.
- **R4-m-09** **No knowledge tool is named anywhere in the spec** — `knowledge_*` appears only as
  a glob. FR-070 requires every knowledge tool enumerated *"explicitly… with no wildcards"* (Hard
  Constraint #6) and test 34 asserts zero coverage gaps; neither is achievable without the list.
  ADR D17 already names two (`knowledge_search`, `knowledge_graph`). R3-m-04 unaddressed, third
  pass.
- **R4-m-10** Tests 37–39 are still `Bench_*`. `go test` will not run them; `go test -bench`
  requires the `Benchmark` prefix. Raised in rounds 1, 2 and 3.
- **R4-m-11** FR-112 still traces to *"— see AW-3"*, i.e. no test, while AW-3 defers to *"whatever
  remains is reported by the health check"* (test 72). Circular. R3-m-08 unaddressed.
- **R4-m-12** MV-10 still says the lock bound is *"configurable"* without naming a config key
  (R3-m-09). §2.1 still records `buildWorkspaceCSP` as *"its gaps drive the new inline policy"*
  without naming the gaps (R3-m-13) — worth naming now that they are measured
  (`rest_workspace.go:75-83`): under `default-src 'none'` it emits **no `font-src` and no
  `media-src`**, so `/serve/` blocks webfonts and audio outright, while permitting
  `connect-src 'self'` and `form-action 'self'` with no `sandbox` directive. That is looser than
  §10.3 in one direction and stricter in another, and both deserve a recorded decision.
- **R4-m-13** Still no observability requirement (R3-m-10) and no kill switch (R3-m-11) —
  `preview_enabled` appears **zero times**, though `gateway.preview_enabled` is a live,
  per-request, no-restart flag (`config.go:2963`, `IsPreviewEnabled` at `:4357`) and is the exact
  in-tree precedent for disabling an inline-preview surface without a deploy.
- **R4-m-14** Merge artifacts: line 244 reads `**Independent test:****Independent test:**` (the
  label emitted twice). §1.2 cross-references at P-4 and E-3 still dangle — §1 in this document is
  "Available Reference Patterns (N/A)"; they mean ADR §1.2. R3-m-12 unaddressed. §2.4's divergence
  record is narrower than the code: `tel:` is also accepted by the TS sanitiser and rejected by
  the Go one.

---

## 6. OBSERVATIONS

- **R4-O-01 — §10.3 is now the best artefact in this body of work, and it is verifiable in
  thirty seconds.** Executing the two committed harness modules and comparing their policy
  literals against the spec's fenced block is a check anyone can repeat, and it passes. That is
  a materially different standard of evidence from "the winning shape is established", and it is
  worth preserving the pattern: **a security control whose text can be re-derived from a
  committed artefact is auditable; one that is described is not.** The same treatment applied to
  §10.7 (once frozen) and to the inline allow-list would close R4-M-13 and R4-C-04 by
  construction.
- **R4-O-02 — the test plan is genuinely good, and its quality is uneven in a diagnosable way.**
  Tests written *in response to a named false-green* are excellent: 59 ("a test that asserts
  nothing is not expressible"), 69a (three failure modes, each with an explicit reason, and
  "'no panic' would pass all three"), 70 (a read-counting wrapper that catches *both* halves),
  72 ("count runs, not elapsed time"), 73 (a boundary **pair**), 75 (disk hash *and*
  server-side), 90 (byte-exact with a negative half), 93 (drive a real 429). Tests inherited
  from earlier passes and not revisited — 0b, 3, 37–39, 62, 68 — are the weak ones. The lesson
  is not "write better tests"; it is that **the improvement came from naming the specific way
  each test could lie**, and the untouched rows are the ones nobody did that for.
- **R4-O-03 — the stale-section failure is now four for four, and this pass shows why a grep is
  not enough.** R3-O-06 proposed grepping §9, §13.2 and §16 against each changed decision. This
  revision's stale rows were in §3, §9, §11, §13.1, §13.2, §16 **and** §17 — the set moved. What
  would have caught all of them is what caught them here: **compare §16 against §13.1's own
  `Traces to` column mechanically, and compare cited test ids against defined ones.** That is
  twenty lines of script, it found four phantoms and five disagreements in this pass, and it
  does not depend on knowing which decisions changed.
- **R4-O-04 — three of this pass's four criticals are facts about the repository, not about the
  spec's reasoning**, which matches `false-green-patterns.md`'s own observation that *"every real
  defect that session came from a measurement, not from reading code"*. The third serving route,
  the vitest gap and the phantom FR were all invisible to careful reading and obvious to a
  three-command check. Before the next revision, run: the route enumeration for workspace bytes;
  `find src -name '*.test.*'` against `pr.yml`'s patterns; and the identifier cross-check in
  R4-O-03.

---

## 7. Structural integrity (`plan-spec` mode)

| Check | Result | Change since round 3 |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** (US-0 … US-17) | — |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** — US-0's six still have no BDD block; §16 cites parenthetical prose where a scenario name belongs | unchanged |
| Every BDD scenario has a `Traces to:` | **PASS** | — |
| Every BDD scenario has a corresponding test | **FAIL** | unchanged |
| Every FR appears in the traceability matrix | **PASS** — all 112 defined FRs have a row | improved |
| No FR referenced that is not defined | **FAIL** — `FR-008a` (R4-C-03) | new |
| No test cited that is not defined | **FAIL** — `0l`, `3a`, `62a` (R4-M-08) | new |
| §16 agrees with §13.1's own `Traces to` column | **FAIL** — 5 disagreements (R4-M-05); FR-062 duplicated | new |
| Every defined test is reachable from §16 or an SC/NB | **FAIL** — test 94 is orphaned in both directions | new |
| ADR acceptance criteria fully mapped | **PASS on count** (43/43) — **FAIL on content**: AC-15.4 mis-mapped and unsatisfiable, AC-17.2 retired by FR-071 but marked covered | improved then regressed |
| Test datasets cover boundary / edge / error | **PARTIAL** — DS-5 rows 5 and 7 **fixed**; DS-3 still lacks a >255-byte case (R4-M-12); DS-3 row 5 still tests addressing where FR-0003 needs downloading (R4-M-14); DS-5 still has no PDF.js rows (CJK, malformed, hostile) though tests 80/82/67a exist | improved |
| Edge cases agree with the requirements | **FAIL** — E-5 ⊥ FR-034a (R4-M-11) | unchanged |
| Regression impact explicitly addressed | **PASS** — §13.3's six rows now each have a named guard; the chat-`%%…%%` row exists (test 85) | **improved** |
| Success criteria measurable, no subjective language | **PARTIAL** — SC-013 **fixed**; SC-012 honest about WebKit; SC-016/017 added and sharp. FR-007's *"persistent untrusted-content boundary"* still subjective and untestable as written | improved |
| Requirements traceable to measurement | **PASS** — §0's rows now match the cited document; §10.3 is byte-verifiable | **fixed** |
| Every planned test runs in some CI gate | **FAIL** — SPA unit tests land in an unexecuted directory (R4-C-02); 10 E2E tests have no project assignment (R4-M-15) | new |

---

## 8. Test-quality assessment against `false-green-patterns.md`

**Genuinely improved, and worth recording.** Test 59's two-table design makes "a test that
asserts nothing" structurally inexpressible. 59a requires a `--self-test` wired **ahead of** the
real run, citing `pr.yml:252-260` — verified verbatim, including its own comment explaining that
the self-test previously ran nowhere. Test 61's two ordered phases make phase 1 unable to pass
by the app never loading. Test 69a enumerates three failure modes and states that *"'no panic'
would pass all three"*. Test 70 counts reads so that neither skipping nor reading attachments
passes. Test 72 counts runs rather than elapsed time (trap #3). Test 73 is a boundary **pair**.
Test 75 pairs a disk hash with a server-side assertion. Test 90 is byte-exact with a negative
half. Test 93 drives a real 429 rather than reading code. §13.4 item 5 asserts the test
configuration itself. This is the strongest plan any pass has reviewed.

**Still not trustworthy:**

| Test | Problem | Finding |
|---|---|---|
| 1, 2, 87, 88, 89, 61a | Land in `src/components/library/`, which **no vitest group matches** — they run nowhere while the job is green | **R4-C-02** |
| 94 | Traces to a non-existent FR; in no §16 row; in no named spec file, so Chromium-only at `retries: 3` | **R4-C-03** |
| `0l`, `3a`, `62a` | Cited as the sole enforcement of FR-0001d, FR-015b/c and AW-7; **never defined** | **R4-M-08** |
| 35 | Passes vacuously when `knownTools` is empty (`validate.go:449-451`); no positive control | R4-M-09 |
| 68 | Still asserts a header exists; `default-src *` passes MV-21 | R4-M-13 |
| 62 | Still "bounded peak memory" with no figure and no named harness | R4-M-11 |
| 0a, 0e, 0g, 0h | Written against build tags, the mechanism Stage 0 explicitly rejected | R4-M-04 |
| 0b | Guards traversal in a package whose doc disclaims it | R4-m-06 |
| 0c | Tests the double-quote case FR-0003 documents as already working | R4-M-14 |
| 0d | 106 characters — inside 255 bytes, so it cannot exercise FR-0004's byte rule | R4-M-12 |
| 17 | Sole trace for FR-060/061/063/064/065, none of which it can verify | R4-m-07 |
| 37–39 | `Bench_*` — `go test` will not run them | R4-m-10 |
| 61a | Requires a named chunk group `vite.config.ts` has no branch for | R4-M-16 |
| 93 | Asserts a universal claim ("no log line anywhere") at one of six sites | R4-M-07 |

**Missing entirely:** a test for FR-019a's shipped-artefact `eval` assertion; a test for the
token path's verb restriction; a test for path containment inside a token's scope; a test for
`.svg` in the SPA `<img>` context and the KB-embed context; any assertion that the inline
allow-list's extensions are present in the type table (R4-C-04).

---

## 9. STRIDE delta since round 3

| Component | Threat | Status |
|---|---|---|
| Inline preview response | Information disclosure — session cookie readable | **CLOSED.** Measured, and the policy is now byte-verifiable against a committed harness (§10.3, MV-13, FR-005a) |
| Inline preview egress | Information disclosure — exfiltration to an external origin | **CLOSED** for cross-origin. **OPEN for same-origin** — the policy permits subresource loads to any gateway path and the experiment never measured that axis (R4-M-01) |
| Preview embedding | Spoofing / UI deception | **PARTIAL.** §10.6 and FR-005b fix the mechanism; `frame-src 'self'` lets a preview embed the real SPA, and no requirement mandates `frame-ancestors 'none'` on it (R4-M-01) |
| Preview token | Information disclosure — URL-borne credential | **SUBSTANTIALLY CLOSED** — TTL, revocation set, redaction, no-referrer, in-memory store. **Residual: five sites still log a raw request path, one into the audit record** (R4-M-07); entropy unrequired (R4-M-02) |
| Preview token path | Elevation — traversal on an unauthenticated file server | **NEW / OPEN.** No containment requirement anywhere on the token handler, in the same stage that relaxes read-path name validation (R4-M-02) |
| Preview token | Denial of service | **OPEN.** No rate limit on the mint POST, no live-token cap, in-memory store |
| **Library media route** | **Stored XSS — inline scriptable content on the session origin, no policy** | **NEW / OPEN, and pre-existing.** `/api/v1/media/workspace/` serves Library bytes `inline` with `image/svg+xml` / `text/html` and no CSP (R4-C-01) |
| Inline allow-list — `.svg` | Elevation — scriptable document served inline | **PARTIAL.** Containment reasoning sound; inclusion justified by an unverified claim; the gate is a test with no requirement (R4-C-03, R4-M-06) |
| Library serving path | Spoofing (type confusion) | **CLOSED in requirement** — FR-015a/b/c, MV-24, test 58 with a positive control. Undercut by FR-015c's incomplete minimum (R4-C-04) |
| PDF rendering | Elevation — parser bug on the gateway origin | **SUBSTANTIALLY CLOSED** — FR-018a-d, FR-019a-c, tests 67/67a/80–84/96. FR-014 still argues the opposite; the SPA CSP is unenforced (R4-M-13) |
| Filename ingest — remote/untrusted | Tampering — attacker-chosen names reaching a relaxed path | **CLOSED in requirement** (FR-0001d, NB-18, the §4A trust table) — **untested**, its only test is a phantom (R4-M-08) |
| Filename ingest | Elevation — traversal, `.`/`..`, C0 | **CLOSED** — FR-0002, FR-0002a, FR-0002b, NB-16, tests 0g/0h. Verified that `TestValidateComponent` has no `.`/`..` case today, so 0h is real new coverage. Test 0b still aimed at the wrong layer (R4-m-06) |
| Note content → agent prompt | Injection | **OPEN** — unchanged since round 1. Notes are indexed, excerpted and returned to agents; nothing addresses instruction content inside a note reaching an agent's context |

---

## 10. Unasked questions

1. **What serves a Library file's bytes, exhaustively?** Four routes were found in one session;
   the spec names two. Until that list is closed, no statement of the form "only X serves inline"
   can be trusted.
2. **Which two files are "the isolation spec files"?** Ten tests need the matrix.
3. **Does the token path use `CleanRelPath` and `os.Root`?** Neither appears in any requirement
   about it.
4. **What does the token handler return for an expired token, and does that response carry the
   §10.3 policy?** FR-003c requires it to be visible; MV-13 requires every token-path response to
   carry the policy; nothing reconciles them.
5. **Is `src/components/workspaces/` (57 test files, the v0.3 flagship) also unexecuted on this
   branch?** It matches no vitest group here. Out of this spec's scope, in this spec's blast
   radius.
6. **Who owns `pdfjs-dist`?** FR-018d requires *"a named upgrade owner"*. Nobody is named.
7. **Does a note's own text reach an agent's prompt unescaped?** FR-050's excerpts and FR-051's
   graph answers both return operator-authored (and possibly agent-authored) note content to an
   agent. No requirement, no NB, no test.
8. **What is FR-007's boundary, concretely?** *"A persistent untrusted-content boundary"* is the
   only control against R4-M-01's framing case and against H-6's login form, and it is the one
   requirement in the P0 story with no measurable form.

---

## 11. Verdict

**BLOCK.**

Say the good part plainly, because it is most of the diff. **Round 3's four criticals are
closed** — two outright, two in substance with named residuals — and **fourteen of its
twenty-one majors are closed properly rather than reworded.** §10.3 is byte-verifiable against a
committed harness, which I confirmed by executing it. §0 now matches the document it cites,
caveats and all. §10.4, §10.5, §10.6 and §13.4 are four sections that did not exist and needed
to. FR-019a's `isEvalSupported` correction and FR-071's repair-before-validate correction are
both cases of a requirement being **falsified by measurement and rewritten**, which is the
behaviour this project keeps asking for and rarely gets. The test plan is the strongest any pass
has seen.

What blocks is that the enforcement machinery did not keep up with the prose, and three of the
four criticals are one command away from being visible:

1. **A third route serves Library bytes inline with no policy** (R4-C-01), and the `.svg`
   decision, FR-003g and §10.5 all assume there are two. `grep -n "Content-Disposition" pkg/gateway/*.go`
   would have found it.
2. **The directory this feature lives in runs in no vitest group** (R4-C-02). Test 1 — the
   spec's own HIGH-risk release gate — would never execute. This is `false-green-patterns.md`
   trap #4 reproduced at 27%, on a branch where the guard that fixed it is missing.
3. **`FR-008a` does not exist** (R4-C-03), so the newest security decision in the document is
   gated by a test with no requirement, no matrix entry and no engine assignment. Two more
   phantoms (`0l`, `3a`) are the sole enforcement of the remote-filename requirement and the
   host-MIME requirement.
4. **FR-015c and §10.4 disagree about `.css` and `.js`** (R4-C-04), and with `nosniff` that
   breaks the flagship bundle scenario before it can be demonstrated.

None of the four is a design problem. All four are a document that grew faster than its indexes,
which is exactly what a five-agent merge produces. **The cheapest thing available is not another
review pass: it is twenty lines of script** that cross-checks cited-versus-defined identifiers
and §16-versus-§13.1, plus one `find`/`grep` pair against `pr.yml`'s vitest patterns. That
combination produced three of this pass's four criticals and five of its majors, and unlike
R3-O-06's proposal it does not require knowing in advance which decisions changed.

To address these findings:

```
/plan-spec --revise docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md docs/internal/specs/adr-067-knowledge-base-and-preview-spec-review-round4.md
```
