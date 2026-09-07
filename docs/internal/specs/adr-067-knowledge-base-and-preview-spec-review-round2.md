# Spec review — ADR-067 knowledge base and render-first preview (adversarial grill, pass #2)

- **Reviewed:** `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` (Draft, 1,531 lines)
- **Prior pass:** `docs/internal/specs/adr-067-knowledge-base-and-preview-spec-review.md` (BLOCK — 5 CRITICAL / 21 MAJOR / 17 MINOR / 7 OBSERVATION)
- **Supporting measurement:** `docs/internal/specs/adr-067-preview-isolation-experiment-2026-08-22.md`
- **Branch:** `feat/library-improvements` @ `14f8c52f`, worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements` (clean tree)
- **Reviewer mode:** `plan-spec`
- **Scope:** whether the revision discharges pass #1, plus the new content (STAGE 0, D15 revision 3, §17 decisions). Answered findings are verified and dropped, not re-argued.
- **Verification:** every symbol, line and count below was read on this branch in this session. The reference-vault figures were re-measured independently (§7.1).
- **Date:** 2026-08-22

---

## 1. Executive summary

**Five CRITICAL, eighteen MAJOR, fourteen MINOR, six OBSERVATION. Verdict: BLOCK.**

The revision is a real improvement in two places and a regression in one. The experiment
document is the best artefact in this whole body of work — it is honest about its own
confounds, it uses server-side logs as ground truth, and it names four things it did *not*
settle. The problem is that **the spec never cites it, never records what it measured, and
never writes down the policy it settled.** The string `experiment` does not appear in the
spec. Neither does `sandbox`, `allow-scripts`, `default-src`, or `script-src`. Pass #1's
C-01 was *answered by the experiment* and then **not absorbed by the spec**, so an
implementer reading only the spec is in exactly the position C-01 described: inventing the
control that gates the whole of stage 1.

Worse, the experiment recorded — in its own §4.2, from a Firefox console log — the finding
that breaks stage 1 as designed: *"Cookies stop being sent to the page's own subresources."*
The Library file endpoint is registered behind `withUploadAuth` (`pkg/gateway/rest.go:5216`)
and the session cookie is `SameSite=Strict`
(`pkg/gateway/middleware/session_cookie.go:148`). A sandboxed preview's own stylesheet,
script and font are therefore **unauthenticated requests to an authenticated endpoint**. The
experiment could not see this because its harness was a static Python server with no auth.
FR-003 ("MUST load relative subresources") is not merely undesigned — as specified against
the current gateway it is **unsatisfiable**, and the in-tree mechanism that solves it
(ADR-044's `/preview/<agent>/<token>/`) is never mentioned.

The two genuinely new sections have opposite problems. **STAGE 0 is internally
contradictory**: FR-0001 requires relaxing `pathsafe`, NB-12 forbids it in the same
document, and DS-3 — the dataset tests are written from — still asserts the *pre-Stage-0*
verdict for four filenames. A test written from AS-1 and a test written from DS-3 row 2
assert opposite outcomes for the same input. **D15 revision 3 is a clean idea presented
without its cost**: moving PDF parsing into the SPA means an untrusted, attacker-supplied
PDF is now parsed by a large JavaScript library running **on the gateway origin with the
session cookie**, rather than in an opaque-origin frame. That may still be the right call,
but the spec states the trade backwards — FR-014 says these formats "have no sandbox to
apply" as though the risk vanished, when what actually happened is that it moved somewhere
with *less* isolation than before.

Three of pass #1's five CRITICALs are not fixed. C-03 in particular is worse than
unfixed: the spec changed the test name to `TestContentTypeForPath_AudioExtensions` (the
correct function) while leaving §2.1 pointing the fix at `workspaceContentType` in
`rest_workspace.go` — a map the Library serving path still never reaches, because
`handleLibraryDownload` hands off to `http.ServeContent`
(`pkg/gateway/rest_library.go:593`).

| Severity | Count |
|---|---|
| CRITICAL | 5 |
| MAJOR | 18 |
| MINOR | 14 |
| OBSERVATION | 6 |
| **Total** | **43** |

### Answers to the six questions this pass was asked

| Question | Answer |
|---|---|
| (a) Does STAGE 0 protect the containment guarantees it relaxes around? | **No.** Traversal survives, but by accident of *other* code — and STAGE 0 does not know that (R2-C-02, R2-C-01, R2-M-13). FR-0001 build-tags away C0-control rejection, which is not a Windows concern; FR-0002's test is aimed at a package that does not implement traversal defence at all. |
| (b) Is the PDF.js decision implementable and honestly bounded? | **Implementable, not bounded.** Its security cost is understated (R2-C-05), it is a new dependency the spec says it does not have (R2-M-04), and form filling/signing are **never stated as out of scope** — NB-13/NB-14 exclude only the *cryptographic* and *agent-driven* cases, which reads as endorsing the human case that has no FR, no scenario and no test (R2-M-05). |
| (c) Do §17's decisions have requirements and tests? | **Four of nine do. Five are assertions only.** AW-1, AW-2, AW-5, AW-6 have no FR and no test; AW-8's "7 days" appears nowhere but §17 (R2-M-09). AW-3 is circular with FR-112 (R2-M-14). |
| (d) Are measured claims traceable, and does the spec avoid overclaiming? | **Not traceable at all** — the experiment is never cited (R2-C-04). One overclaim: FR-019 states the CORS font fix as settled; the experiment explicitly marks it *"Not verified"* (R2-M-10). Credit where due: AW-10/11/12 record Acrobat, complex forms and the size threshold as open, correctly. |
| (e) Test quality against `false-green-patterns.md` | **Two tests are the documented anti-patterns in specification form** (tests 59 and 61 are source-text scans — trap #2), the security E2E inherits `retries: 3`, and the three `Bench_*` tests still cannot be executed by `go test` (R2-M-06, R2-M-07). |
| (f) Untrue codebase assumptions | **Seven.** The auth/cookie model (R2-C-03), `workspaceContentType` (R2-M-01), the non-existent KB link renderer (R2-M-03), `classifyLibraryEntry`'s "purely additive" claim (R2-M-02), "no new dependencies" (R2-M-04), the browser matrix (R2-M-07), and `http.ServeContent`'s sniffing fallback vs FR-015 (R2-M-01). |

---

## 2. Status of pass #1's findings

Verified individually. "Answered" means the spec now contains the fix, not that the fix was
discussed.

| Pass-1 | Status | Evidence |
|---|---|---|
| **C-01** CSP undesigned | **Partly** — settled by the experiment, **not recorded in the spec**. No directive string, no citation. See R2-C-04 |
| **C-02** FR-003 ⊥ FR-006 | **Partly** — the experiment shows both are achievable (`'self'` matches under an opaque origin). The spec still states both as unqualified absolutes and still has no requirement distinguishing "sibling asset" from "third-party host". §8 still reads "When a document requests any network destination, the system blocks it" |
| **C-03** audio MIME in the wrong function | **NOT answered.** §2.1 still lists `workspaceContentType` … `rest_workspace.go` … "**Modified** — audio MIME types added". The word "unreachable" appears once in the spec, at line 136, about vault notes. See R2-M-01 |
| **C-04** no KB markdown renderer | **NOT answered.** §2.1 still asserts `isSafeHref` (TS) is "bypassed by a KB-specific link renderer". No such component exists; `LibraryMarkdownPreview.tsx:27` renders via `HistoricalMessageMarkdown`, the chat renderer, verbatim. No FR creates a separate one. See R2-M-03 |
| **C-05** readiness overclaimed | **Answered.** Status line now reads "implementation-ready for stages 1 and 2; stages 3 and 4 specified in full but gated" and P-1/P-3 remain open. Honest |
| **M-01** tools never named | Not answered — no tool name appears anywhere in the spec |
| **M-02** ten FRs trace to tests that cannot verify them | Partly — FR-062 gained test 63, but FR-060/061/063/064/065 still all point at test 17 (`TestResolveLink_AllFourForms`), and FR-062 now appears **twice** in the matrix with two different tests |
| **M-03** `Bench_*` cannot run | Not answered — tests 37–39 still named `Bench_*` |
| **M-04** 100k fixture unspecified | Not answered — P-4 still "To build", no generator spec |
| **M-05** MV-4 ⊥ FR-107 | Not answered — MV-4 (2 s over 100k) and FR-033/FR-107 unchanged |
| **M-06** allow-list never enumerated / SVG | Not answered. Note `.svg` is already `image/svg+xml` in `workspaceContentType` and already classifies as `image` in `libraryPreviewKind.ts:14` |
| **M-07** ADR-044 removed the SPA's only iframe | Not answered — `iframe`, `ADR-044` and `srcdoc` appear nowhere in the spec |
| **M-08** FR-090 stage placement | Not answered |
| **M-09** MV-12 vs US-9 AS-2 | Not answered |
| **M-10** marker format unspecified | Not answered — FR-024 still says "store … in its marker" with no format |
| **M-11** note content → agent prompts | Not answered |
| **M-12** hardlinks / TOCTOU | Not answered |
| **M-13** index outlives revoke with full text | Partly — AW-8 fixes the *duration* (7 days) but nothing requires the index to stop being searchable |
| **M-14** stage 3 authoring tools | Not answered |
| **M-15** no REST surface | Not answered |
| **M-16** browser matrix does not exist | Not answered — P-5 still "**Required for stage 1**", and it still does not exist (R2-M-07) |
| **M-17** SC-013 baseline / wrong function credited | **Worse.** Now provably self-contradictory — see R2-M-02 |
| **M-18** US-14 on Windows | Not answered |
| **M-19** residual omits the length rule | **Answered, and correctly** — STAGE 0 measures it (1 illegal char, 2 over 100 runes, longest 106). I reproduced this: it is exactly right (§7.1) |
| **M-20** reproducibility test flaky | Partly — test 32 now says "**Never byte comparison**"; no tie-break rule added |
| **M-21** three tests need unmandated seams | Not answered |
| **m-01 … m-17** | m-09 answered by the experiment but not recorded; the rest unchanged. m-04, m-10, m-13, m-14, m-16 remain |

---

## 3. CRITICAL

### R2-C-01 — STAGE 0 and NB-12 are direct contradictions, and DS-3 still encodes the pre-Stage-0 verdict

- **Lens:** Inconsistency
- **Affected:** FR-0001, US-0 AS-1/AS-3, NB-12, E-4, DS-3 rows 2/3/4/7/8, AW-3

The spec requires the change and forbids it, in the same document:

- **FR-0001:** "MUST apply Windows-specific filename restrictions … **only in Windows builds**, via build tags."
- **NB-12:** "The system **must not relax `pathsafe`** for mounted folders as a side effect of this work; that is a separate decision with its own blast radius."

NB-12 was written when the relaxation was out of scope. STAGE 0 puts it in scope and NB-12
was not retracted. §10.1 is the "explicit non-behaviours" section — the place an implementer
checks before writing code that looks risky. It currently tells them to stop.

The dataset is worse, because datasets are what tests are written from:

| DS-3 row | Spec says | STAGE 0 says |
|---|---|---|
| 2 `Meeting: 2026-01-01.md` | "**not addressable**; reported unaddressable" | AS-1: "it appears and can be opened" |
| 3 `Why?.md` | "as above" | as AS-1 |
| 4 `elicify-* packages.md` | "as above" | as AS-1 |
| 7 300-character basename | "rejected by the length rule" | FR-0004: "MUST NOT apply the component-length limit" |
| 8 `CON.md` | "not addressable; reported" | FR-0001: rejected only in Windows builds |

E-4 in §9 repeats the same stale verdict. AW-3 says "**Superseded by Stage 0**" — but nothing
downstream of AW-3 was updated. A QA engineer given DS-3 and a QA engineer given §4A write
tests that fail each other.

- **Impact:** Two mutually exclusive test suites, both traceable to the spec. Whichever is
  written first becomes the de-facto decision, silently.
- **Fix:** (1) Retract NB-12 explicitly, or scope it to something STAGE 0 does not do — say
  which. (2) Rewrite DS-3 rows 2, 3, 4, 7, 8 as a **platform matrix** (POSIX verdict |
  Windows verdict) rather than a single verdict column. (3) Rewrite E-4 the same way. (4)
  Delete or re-scope FR-112 (see R2-M-14).

### R2-C-02 — FR-0001 build-tags away control-character rejection, which is not a Windows rule, at two untrusted-input call sites STAGE 0's rationale never mentions

- **Lens:** Insecurity (Tampering / Information Disclosure), Incompleteness
- **Affected:** FR-0001, FR-0003, US-0 AS-5, §4A "What is traded away"

FR-0001 names the rules to make conditional as "illegal characters, reserved device names,
trailing dot or space". In the code, "illegal characters" is **one predicate covering two
unrelated things** (`pkg/pathsafe/pathsafe.go:332`):

```go
func firstIllegalRune(name string) (rune, bool) {
	for _, r := range name {
		if r <= 0x1F {            // C0 controls — NOT a Windows rule
			return r, true
		}
		if strings.ContainsRune(illegalRunes, r) {   // <>:"|?* — the Windows rule
			return r, true
		}
	}
```

`illegalRunes` is `` `<>:"|?*` ``. The `r <= 0x1F` branch covers NUL, CR and LF and exists for
reasons that have nothing to do with NTFS. Under FR-0001 as written, a POSIX build stops
rejecting `\r`, `\n` and NUL in filenames. That is precisely the injection primitive FR-0003
then tries to patch — and FR-0003 only mentions quotes.

Two further consequences the §4A rationale ("mounted folders … the operator's own files on
their own machine") does not cover, because `pathsafe` is shared:

1. **`SanitizeComponent` is the only defence at `pkg/utils/media.go:97`** — inbound chat
   attachment filenames from Discord, Feishu, Telegram. These are **remote, attacker-chosen**
   names, not the operator's own files. It rewrites via `replaceIllegalRunes`
   (`pathsafe.go:273`), which carries the same fused `r <= 0x1F` branch.
2. **`pkg/agent/upload_workpath.go:179`** calls `ValidateComponent` on upload filenames. It
   happens to survive, because it does its own separator, `.`/`..` and
   `unicode.IsControl` checks first (lines 169–178) — defence that exists there and nowhere
   else. STAGE 0 does not know this, and does not say so.

There is also a concrete, checkable regression: `ValidateComponent("..")` currently fails via
`hasTrailingDotOrSpace`. Build-tag that rule away on POSIX and `ValidateComponent("..")`
**starts returning nil**. `CleanRelPath` still rejects it (`pkg/library/root.go:386` runs the
`HasPrefix(seg, "..")` check *before* `ValidateComponent`), so the Library is safe — but any
future caller that reaches for `ValidateComponent` as "the safety check" inherits a silently
weaker one.

- **Impact:** A filename-borne CR/LF reaching a response header, or a NUL reaching a path
  syscall boundary, at exactly the ingest points the operator does not control.
- **Fix:** Split the rule set explicitly in FR-0001. Name the three **unconditional** rules —
  C0 control characters (including NUL, CR, LF), path separators, `.`/`..`/`..`-prefixed —
  and the three **Windows-only** rules — the `<>:"|?*` set, reserved device names, trailing
  dot/space. Add an FR stating that `SanitizeComponent`'s untrusted-input contract is
  unchanged on every platform, and a test asserting a CR/LF/NUL name is still rejected under
  the POSIX tag. Add `ValidateComponent("..") != nil` to test 0b as a platform-independent
  assertion.

### R2-C-03 — A sandboxed preview's own subresources arrive unauthenticated at an authenticated endpoint; FR-003 is unsatisfiable as specified

- **Lens:** Infeasibility / Incompleteness
- **Affected:** FR-003, FR-005, US-1 AS-4, tests 9 and 60, §11 "The browser"

The experiment recorded this and the spec did not read it. Experiment §4.2:

> **Cookies stop being sent to the page's own subresources.** Firefox logged *"Cookie
> 'omnipus_probe' has been rejected because it is in a cross-site context"* for the
> stylesheet, script, PDF and audio.

Against the real gateway that is not a curiosity, it is a 401:

- `/api/v1/library/` is registered as
  `a.withUploadAuth(withRateLimit(configLimiter, a.HandleLibrary))` — `pkg/gateway/rest.go:5216`.
- The session cookie is `SameSite: http.SameSiteStrictMode` —
  `pkg/gateway/middleware/session_cookie.go:148` (and 230, 287, 319).

A document with an opaque origin has a null site-for-cookies, so every subresource it
requests is cross-site and carries no cookie. It cannot add an `Authorization` header either
— a `<link>`, `<script src>` or `@font-face` has no such affordance. So under the policy the
experiment settled on, **the CSS, the JS and the font all 401**, and US-1 AS-4 ("all four
load and the page appears styled, scripted and correctly typeset") fails.

The experiment could not have caught this: §6 shows the harness is `python3 server.py`
serving static files with no auth at all. It measured *browser* behaviour correctly and told
the truth about what it measured; the spec then treated a result from an unauthenticated
server as a result about an authenticated one.

This project already solved this problem once. ADR-044's `/preview/<agent>/<token>/…` is
registered **bare** on the main mux, token-authenticated in the URL path, CSRF/Origin
prefix-exempt — precisely so that a preview's subresources need no cookie. The spec mentions
that mechanism only to say it will not touch it (§13.3: "No `ServedSubdirs` registration is
added by this feature").

- **Impact:** Stage 1's headline user story fails on first contact with a real bundle, after
  the policy has been frozen and the E2E tests written. The discovery point is test 9, at the
  end of stage 1.
- **Fix:** Decide the subresource authentication model **before** freezing the policy, and
  write it into §11 and a new FR. Options, in preference order: (a) serve inline previews
  under a path-token URL modelled on ADR-044's `/preview/…` and state the departure from
  §13.3 explicitly; (b) a per-preview capability token in the query string with a bounded
  TTL; (c) prove empirically that a `SameSite=None; Secure` variant is acceptable — noting
  the gateway's default HTTP-on-localhost deployment makes `Secure` unusable. Then **re-run
  the experiment against an authenticated endpoint**, because that is the configuration being
  shipped.

### R2-C-04 — The spec records none of what was measured, and never cites the experiment

- **Lens:** Incompleteness / Inconsistency
- **Affected:** §10.2 MV-13, FR-005, FR-006, §11, §3 P-5, NB-2

Grepped on this branch. In `adr-067-knowledge-base-and-preview-spec.md`:

- `experiment` — **0 occurrences**
- `sandbox`, `allow-scripts`, `default-src`, `script-src`, `Content-Security-Policy` — **0 occurrences**
- `adr-067-preview-isolation-experiment-2026-08-22.md` — **not referenced**

The experiment established a great deal that the spec needs and does not carry:

| Settled by measurement | Where the spec records it |
|---|---|
| `'self'` matches under an opaque origin | Nowhere |
| A14 (distinct origin) is unnecessary | Nowhere — §11 still names A14 as the fallback |
| `'self'` preferred to a named origin (no hardcoded host behind a proxy) | Nowhere |
| **Neither `sandbox` nor source directives alone is sufficient** — popups escape CSP, `sandboxonly` leaks 5 of 7 vectors | Nowhere |
| Zero of seven egress vectors escape under the winning shape | Nowhere |
| `document.cookie` **throws `SecurityError`**; it does not return empty | Contradicted — see R2-M-11 |
| A sandboxed preview cannot call back to the Omnipus API at all | Nowhere |

§11 still says: *"if no single-origin policy satisfies both isolation and subresource
loading, fall back to serving previews from a distinct origin (ADR alternative A14)"* — a
contingency the measurement retired. §3 P-5 still lists the browser matrix as "Required for
stage 1" as though nothing had been run.

- **Impact:** The implementer either re-derives the policy (and may land on `sandboxonly`,
  which the measurement shows leaks five of seven vectors, or on `nosandbox`, which leaks the
  session cookie outright) or re-runs the experiment. Meanwhile A14 remains a live branch in
  the plan with no owner, which is what pass #1 flagged.
- **Fix:** Add a `§10.3 Measured isolation policy` to the spec containing: the literal
  directive string as it will be emitted by the handler; the `sandbox` token list with a
  one-line reason per token (`allow-scripts` yes; `allow-popups`, `allow-forms`,
  `allow-downloads`, `allow-same-origin` **no**, each with the vector it closes); the engines
  and versions verified; a link to the experiment document; and the explicit statement that
  **both mechanisms are required and neither is sufficient alone**. Change MV-13 from a
  paraphrase to a literal string assertion. Retire A14 from §11 and record why. Change P-5 to
  "Partly done — shape verified 2026-08-22; final string to be re-verified against the real
  handler (experiment §5)".

### R2-C-05 — D15 revision 3 moves untrusted PDF parsing onto the SPA's own origin, and the spec presents that as removing risk

- **Lens:** Insecurity (Elevation of Privilege), Overcomplexity of claim
- **Affected:** FR-014, FR-017, FR-018, NB-2, test 57

FR-014, in full:

> The system MUST sandbox content the **browser** executes (HTML and bundles). Formats
> Omnipus renders itself — images, video, audio, markdown, Mermaid, code and PDF — are drawn
> by SPA components, never become browser documents, and **therefore have no sandbox to
> apply**.

The first two clauses are true. The conclusion does not follow. Under revision 2 an untrusted
PDF was parsed by the browser's own PDF engine inside an opaque-origin frame. Under revision
3 it is parsed by **PDF.js executing on the gateway origin**, in the same JavaScript realm as
the SPA, with the session cookie, `localStorage`, and every authenticated API route one
`fetch` away. A parser bug is no longer contained by an origin — it *is* the origin.

This is not hypothetical for this specific library: CVE-2024-4367 was arbitrary JavaScript
execution in PDF.js reachable through a crafted font, exploitable in the default
configuration because `isEvalSupported` defaults to true. The threat model here is exactly
the one the ADR built stage 1 around — "a page an agent wrote or downloaded" (US-2). An agent
that downloads a hostile PDF now hands it to a parser running with the operator's session.

The spec has no hardening requirement of any kind for this. Nothing sets `isEvalSupported:
false`; nothing disables `enableXfa`; nothing pins or floors the version; nothing constrains
the worker; nothing requires a CSP on the SPA document (there is none today —
`Content-Security-Policy` is set only on `/serve/`, `/dev/` and `/preview/` responses, never
on the SPA shell).

Also note the position it leaves stage 1 in: after revision 3, **HTML is the only sandboxed
format**, so the entire measured isolation apparatus protects exactly one file type, while
the format the measurement was re-run for is now deliberately outside it.

- **Impact:** A single PDF.js parser vulnerability is a full session compromise, silently, in
  the product's stated threat model. The spec's own language would lead a reviewer to
  conclude the opposite.
- **Fix:** (1) Rewrite FR-014's final clause: state that SPA-rendered formats are parsed **in
  the SPA's origin**, and that their containment is the parser's correctness, not an origin
  boundary. (2) Add FR-018a: PDF.js MUST be configured with `isEvalSupported: false` and XFA
  disabled, MUST run its parsing in the worker, and its version MUST be floored at a stated
  minimum with a documented upgrade owner. (3) Add an FR for a CSP on the SPA document that
  excludes `unsafe-eval`, or record explicitly that the SPA ships without a CSP and why that
  is accepted. (4) Add a test asserting a PDF containing a JavaScript action / embedded file
  triggers nothing — with a **positive control** in the same style as test 58. (5) Record the
  alternative that was rejected (opaque-origin frame + `allow-downloads`, experiment §3.1
  option 2, still untested) and why.

---

## 4. MAJOR

### R2-M-01 — C-03 unfixed: the audio-MIME fix still points at a map the Library path never reaches, and FR-015 contradicts the code that serves the bytes

- **Lens:** Incorrectness | **Affected:** §2.1, FR-009, FR-015, MV-14, test 3

§2.1 still reads: `workspaceContentType` | `pkg/gateway/rest_workspace.go` | "**Modified** —
audio MIME types added". That is a `map[string]string` at `rest_workspace.go:87`, read only
by `contentTypeForPath` (`:106`), whose only callers are `rest_workspace.go:322,346` and
`rest_preview.go:289,314`. The Library download handler does not call it:

```go
// pkg/gateway/rest_library.go:591-593
w.Header().Set("X-Content-Type-Options", "nosniff")
w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
http.ServeContent(w, r, filename, fi.ModTime(), f)
```

`http.ServeContent` derives the type from `mime.TypeByExtension` and, when that returns
nothing, **sniffs the first 512 bytes**. So:

- Test 3 can pass against the map while Library audio stays `application/octet-stream`.
  That is the exact false-green shape pass #1 named.
- **FR-015 is violated by the current code**, not merely unimplemented: it says "MUST derive
  `Content-Type` from the file extension, **never from content sniffing**", and `ServeContent`
  sniffs.
- `mime.TypeByExtension` consults the host (`/etc/mime.types`, the Windows registry), so the
  type for a given extension is **machine-dependent**. MV-14 ("never
  `application/octet-stream`") is therefore not a property of the binary, and a test that
  passes on CI can fail on an operator's box.

- **Fix:** Correct §2.1 to name `pkg/gateway/rest_library.go`'s serving path as the modified
  site. Add an FR: the Library serving path MUST set `Content-Type` explicitly from an
  in-binary extension table before calling `ServeContent`, and MUST return a stated default
  for unknown extensions — never consulting the host MIME registry and never sniffing. Point
  test 3 at that table. Consider hoisting `workspaceContentType` into a shared table so there
  is one, not two.

### R2-M-02 — SC-013 and test 1 are unsatisfiable: the kinds that must change are pre-existing inputs

- **Lens:** Inconsistency / Infeasibility | **Affected:** §2.2 HIGH-RISK WARNING, SC-013, test 1, FR-001

§2.2: "Adding kinds must be **purely additive**: no existing input may change classification.
SC-013 and `TestClassifyLibraryEntry_ExistingKindsUnchanged` enforce this."
SC-013: "Every pre-existing preview classification is unchanged (**zero diffs** against the
current classification table)."

The current table (`src/components/library/preview/libraryPreviewKind.ts:30-40`) resolves:

| input | today | after FR-001 |
|---|---|---|
| `report.html` | `'text'` (via `is_text_editable`) | `'html'` |
| `manual.pdf` | `'other'` | `'pdf'` |
| `podcast.mp3` | `'other'` | `'audio'` |

Three pre-existing inputs must change classification — that *is* the feature. A guard written
to SC-013's literal wording fails the moment FR-001 is implemented; a guard written to pass
has been weakened to whatever the implementer left in it, which is pass #1's M-17 with no
baseline artefact to appeal to.

Separately, the real blast radius is mis-stated. Widening the `LibraryPreviewKind` union
forces changes in **both** consumers: `LibraryPreviewPane.tsx:60` (which surface to mount)
and `LibraryEntryRow.tsx:90` (which icon to draw). The row's icon for every `.html`, `.pdf`
and `.mp3` in every workspace changes as a side effect, and no scenario mentions it.

- **Fix:** Restate SC-013 as a **frozen allow-list of intended diffs**: commit the current
  classification table as a checked-in fixture *before* the change, and have test 1 assert
  the new table differs from it in exactly the three enumerated rows and nowhere else. Add an
  acceptance scenario for the row-icon change so it is a decision rather than a side effect.

### R2-M-03 — C-04 unfixed: the "KB-specific link renderer" does not exist, no FR creates it, and FR-011 is unscoped so it changes chat

- **Lens:** Incorrectness / Ambiguity | **Affected:** §2.1, FR-011, FR-013, FR-060, FR-061, NB-4, §13.3

§2.1: `isSafeHref` (TS) | "**Not modified** — bypassed by a KB-specific link renderer."
There is no KB-specific link renderer. `src/components/library/preview/LibraryMarkdownPreview.tsx:8`
imports `HistoricalMessageMarkdown` from `@/components/chat/historical-markdown` and line 27
renders with it directly — the comment in that file says it reuses the chat renderer
"VERBATIM". No FR anywhere requires building a separate one; FR-060/061/062 describe rendering
behaviour without saying where it lives.

FR-011 is the sharp end: "The system MUST hide `%%…%%` comments **when rendering markdown**."
Unscoped. Implemented in the only renderer that exists, it silently hides `%%…%%` in **chat**
— i.e. in untrusted model output. That is a behaviour change in a surface §13.3 promises is
untouched, and no test guards it (§13.3's row protects *link* handling only).

- **Fix:** Add an FR mandating a KB/Library markdown renderer distinct from the chat one,
  naming what it may diverge on (relative links, `%%…%%`, wikilinks, callouts, highlights)
  and what it must inherit. Scope FR-011 to that renderer in its own text. Add a regression
  test asserting chat still renders `%%…%%` unchanged, and add it to §13.3.

### R2-M-04 — "New dependencies: none" is false, and PDF.js's non-JS assets are unaccounted for

- **Lens:** Incorrectness / Incompleteness | **Affected:** §4, FR-018, test 61

§4: "**New dependencies: none.** `bleve/v2` is already direct." `pdfjs-dist` is not in
`package.json` on this branch. FR-018 requires it. That is a new dependency, and under
CLAUDE.md constraint #1 it must be embedded into the single binary via `go:embed`.

Beyond the JS: PDF.js needs `cmaps/` (CJK encodings) and `standard_fonts/` fetched at runtime
from the serving origin. Vite does not bundle these automatically — they are copied assets.
Omit them and CJK PDFs render **blank pages with no error**, which is the silent-degradation
failure mode this project has a whole document about. Nothing in the spec mentions them, and
"lazily loaded" (FR-018) does not address them because they are fetched per-document, not per
bundle.

- **Fix:** Correct §4 to name `pdfjs-dist` with a version floor. Add an FR covering the
  `cmaps/` and `standard_fonts/` asset paths: where they are copied to, that they are embedded,
  and how they are served. Add a test rendering a CJK PDF and asserting glyph presence — not
  "no error".

### R2-M-05 — Form filling and signing are never declared out of scope, and NB-13/NB-14 imply the opposite

- **Lens:** Ambiguity / Incompleteness | **Affected:** NB-13, NB-14, AW-10, AW-11, FR-018, US-1

The experiment's §7 measured form filling and ink signatures and was careful about what it
established. The spec's non-behaviours then exclude only the *edges*:

- **NB-13** — no cryptographic/legally-verifiable signatures.
- **NB-14** — no XFA, no **agent-driven** form filling.

Read together by an implementer, these exclude PKI signing, XFA, and agent filling — and
therefore **endorse** human form filling and human drawn signatures as in scope. But there is
no user story, no acceptance scenario, no FR and no test for either. FR-018 says only
"render". AW-10 muddies it further: "Blocks *promising* form filling, not rendering" — which
tells you what is blocked from being *promised*, not what is being *built*.

Meanwhile the two open questions that would gate shipping it (AW-10 Acrobat, AW-11 complex
forms) sit in the "genuinely undecided" table with no decision date and no owner.

- **Fix:** Add **NB-16**: "The system must not offer PDF form filling or signature drawing in
  this release. PDF preview is read-only. The measurement in the experiment's §7 establishes
  feasibility for a future decision; it is not a commitment." Then reword NB-13/NB-14 so they
  read as reinforcements of NB-16 rather than as carve-outs from an assumed capability. If
  read-only is *not* the intent, the capability needs a user story, scenarios, FRs, tests, and
  AW-10/AW-11 closed first.

### R2-M-06 — Tests 59 and 61 are source-text scans: the documented false-green pattern, specified

- **Lens:** Infeasibility | **Affected:** tests 59, 61; FR-016, FR-018

`docs/internal/false-green-patterns.md` §2: *"A guard test asserted a component still called
`shouldRenderToolCall` by checking the file text contained that identifier. Deleting the entire
gate and leaving the name in a comment kept 673/673 passing. **Rule:** assert on behaviour,
never on source text."*

- **Test 59** `TestInlineAllowList_RequiresTypeConfusionTest` — "Adding an extension without a
  test fails CI". The only way to implement "a test exists for extension X" is to scan test
  source for X. A comment mentioning `.svg` satisfies it. A test named for `.svg` that asserts
  nothing satisfies it.
- **Test 61** `TestPdfJsBundleLazyLoaded` — "PDF.js absent from the initial SPA payload". The
  obvious implementation greps the built `index-*.js`. A renamed chunk, a changed minifier or
  a vendor-prefix change silently turns it green. It also has a Go test name for a frontend
  build artefact, and no requirement guarantees a build output exists when it runs.

- **Fix:** Test 59: make the property structural instead — derive the allow-list from a table
  that pairs each extension with a **test-case identifier**, and have the unit test iterate
  the table and fail on a missing/empty case, so the coverage relation is data the compiler
  and the runner both see. (`scripts/e2e-shards.sh check` is the in-tree precedent for making
  coverage an enforced property rather than a maintained list.) Test 61: assert at runtime in
  a browser test that no PDF.js network request occurs before a PDF is selected, and that one
  does after — behaviour, not bytes. Rename it to the frontend convention.

### R2-M-07 — SC-012's three-engine matrix does not exist, CI installs one browser, and the security E2E inherits `retries: 3`

- **Lens:** Infeasibility | **Affected:** SC-012, tests 10, 11, 12, 57, 60; §3 P-5, §11

Measured on this branch:

- `playwright.config.ts` declares **no `projects`** — Chromium only. No Firefox project, no
  WebKit project.
- `.github/workflows/pr.yml:1140` — `npx playwright install --with-deps chromium`.
- `playwright.config.ts` sets **`retries: process.env.CI ? 3 : 2`**, sized for real-LLM flakes.
- Nothing runs headed; test 57 demands "**3 engines, HEADED**".

So SC-012 ("pass in Chrome, Firefox and Safari") and tests 12/57 require: three browser
projects, a WebKit/Firefox install step, a headed mode (xvfb on Linux), and — for Safari
specifically — a macOS runner, since Playwright's WebKit is not Safari and the experiment
itself flags Safari headful as unverified.

The `retries: 3` interaction is the dangerous one. A security assertion that passes on the
fourth attempt is reported identically to one that passes on the first. For "the cookie was
not readable" or "the request did not reach the external origin", a retry-tolerant result is
not evidence.

- **Fix:** Change §3 P-5 from "Required" to "**To build**", with the concrete work named:
  Playwright `projects` for firefox/webkit, the CI install line, the headed/xvfb decision, and
  whether Safari-proper is in scope or SC-012 should say "WebKit" and admit the gap. Require
  the isolation specs to run with **`retries: 0`** via a per-project or per-file override, and
  say so in §13. Assign each new E2E spec to a shard in `tests/e2e/shards.json` (see R2-O-02).

### R2-M-08 — Test 57's stated rationale is stale under revision 3, and its cost is now unjustified

- **Lens:** Inconsistency | **Affected:** test 57, experiment §7.4

Test 57's note: "**Real browser, 3 engines, HEADED.** Headless has no PDF viewer and
previously produced a false negative."

That reasoning belonged to revision 2, where the *browser's* PDF viewer rendered the file.
Under revision 3, PDF.js draws to a `<canvas>` — headless renders canvas perfectly well, and
the browser's PDF viewer is never involved. The test is carrying the justification for a
design that was withdrawn, and using it to demand the most expensive infrastructure in the
plan.

- **Fix:** Rewrite the note: headed is no longer required for PDF.js; state what the oracle
  actually is (rendered canvas pixels non-blank plus text-layer content matching a known
  string — `document.fonts.status`-style proxies are not oracles, per the experiment's own
  §3.2). Keep the multi-engine requirement only if there is a stated reason PDF.js would
  differ per engine.

### R2-M-09 — Five of the nine §17 decisions have no requirement and no test

- **Lens:** Incompleteness | **Affected:** §17, §14, §13.1

| Decision | FR? | Test? | Consequence |
|---|---|---|---|
| **AW-1** excerpt re-read at query time | **No** — FR-050 says "returning path, title and matched excerpt", nothing about when | **No** | Unspecified behaviour when the file changed between index and query (excerpt may not contain the match); unspecified when the file is cloud-evicted (E-16 says loud failure at *index* time; a query-time read can block for seconds); unbudgeted contribution to MV-1's 500 ms p95 across up to 20 files per query |
| **AW-2** attachments indexed by filename only | **No** | **No** | DS-2 row 5 has 100,000 attachments and nothing states that a *content* read is forbidden — which is exactly the "never reads inside it" guarantee AW-2 makes |
| **AW-5** a KB is exactly one mounted folder | **No** | **No** | Nothing rejects a second mount claiming the same collection; FR-031 ref-counts by realpath but that is a different invariant |
| **AW-6** health check automatic, on a schedule | Partly — FR-038 says "provide a drift check that runs without any agent" | **No** | No interval, no failure surface, no statement of the exclusive-index-lock consequence AW-6 itself notes (which collides with E-16) |
| **AW-8** 7-day grace period | Partly — FR-109 says "a grace period" | Test 52 exists, with no value to assert | "7 days" appears nowhere but §17; the test cannot assert an unstated number |

- **Fix:** Promote each to an FR with the number in it (AW-8 → an MV), and give AW-1, AW-2 and
  AW-5 a test each. For AW-1 specifically, add the failure case: what the response contains
  when the query-time read fails or the file no longer contains the match.

### R2-M-10 — FR-019 states as settled a fix the experiment explicitly marks unverified, and test 60 has no negative control

- **Lens:** Incorrectness | **Affected:** FR-019, test 60, §19

Experiment §3.2: *"**Likely fix:** emit `Access-Control-Allow-Origin` on font responses
(without credentials). **Not verified** — it was not tested. … This finding is not settled,
and must not be recorded as if it were."*

FR-019 records it as settled: "The system MUST serve font responses with
`Access-Control-Allow-Origin` so webfonts in sandboxed HTML bundles resolve". No caveat, no
assumption in §19, no citation.

Two further gaps:

1. **Which responses are "font responses"?** Extension-derived, presumably — but the
   extension set is never enumerated (`.woff2`, `.woff`, `.ttf`, `.otf`, `.eot`), and a font
   referenced with no extension or an odd one silently misses the header and fails the same
   way. Adding a permissive CORS header to an authenticated endpoint also deserves a sentence
   on what it does and does not expose (with `*`, credentialed reads are refused by the
   browser — worth stating rather than leaving a reviewer to work out).
2. **Test 60 has no negative control.** Asserting "rendered width matches the webfont" can
   pass by coincidence if the fallback font has similar metrics. Test 58 was given a positive
   control for exactly this reason; test 60 needs the mirror: the same assertion **without**
   the ACAO header must fail.

- **Fix:** Mark FR-019 as *unverified at authoring* with a pointer to experiment §3.2, or run
  the re-test first and cite it. Enumerate the font extensions. Add the negative control to
  test 60. Add an assumption to §19.

### R2-M-11 — The cookie scenarios assert the wrong observable, and can pass vacuously

- **Lens:** Incorrectness (test oracle) | **Affected:** §12 BDD, US-2 AS-1/AS-2, tests 10, 12

Spec BDD: *"Then the displayed cookie value is empty"* and US-2 AS-1: *"the read yields
nothing"*.

Experiment §2.2 measured the opposite mechanism: *"`document.cookie` did not return empty — it
**threw `SecurityError`**, as did `localStorage`."*

An assertion of "displayed cookie value is empty" is satisfied by a page that threw before
writing anything, by a page that failed to load at all, and by a blank frame. It is green
under total failure. That is a vacuous oracle in the exact sense the false-green document
describes, and it is the P0 control of stage 1.

The experiment also shows how to make it non-vacuous, and the spec should adopt it: the same
page under `nosandbox` read back `omnipus_probe=SECRET` — a **positive control** proving the
probe works.

- **Fix:** Rewrite AS-1/AS-2 and the BDD to assert the measured behaviour: the read **throws**
  a `SecurityError` and `window.origin === "null"`. Add the positive control (same page, no
  sandbox, cookie readable) as a required part of tests 10 and 12. Assert egress on
  **server-observed request arrival**, not console strings — experiment §4.4 shows the console
  wording differs per engine and is brittle.

### R2-M-12 — FR-034a's stated memory mechanism does not bound a full-text indexer, and test 62 asserts a property the design cannot deliver

- **Lens:** Infeasibility | **Affected:** FR-034a, AW-7, test 62, E-5, MV-2

FR-034a: "memory safety comes from reading files in **bounded chunks**, never from refusing to
index a large file." Test 62: "A 200 MB note is **fully indexed** with bounded peak memory —
never skipped, never capped."

Chunked *reading* bounds the read buffer. It does not bound the indexer, because bleve's unit
of work is the **document**: analysing a 200 MB document produces a token stream and a term
dictionary for that document which must exist before the segment is written. Streaming the
bytes off disk in 64 KB pieces does not change that.

There are only two real designs, and the spec picks neither:

1. **One index document per note.** Peak memory scales with the largest note. MV-2's 512 MB
   is then a function of the biggest file in the collection, not of the collection — and a
   200 MB note plausibly breaches it.
2. **Split a large note into several index documents.** Bounded, but it changes observable
   semantics: hit counts, ranking, dedup, excerpt offsets and backlink attribution all now
   operate on fragments. None of that is specified.

E-5 still says the opposite of FR-034a — "Indexed with a **documented body cap**, or skipped
and reported" — which pass #1 flagged as m-17 and which AW-7 supposedly settled.

- **Fix:** Choose design 1 or 2 and write it into FR-034a. If 1, restate MV-2 as a function of
  the largest single note and give the fixture a stated maximum. If 2, add FRs for the
  fragment/document mapping and for how ranking, counts and excerpts are reconciled. Fix E-5
  to match. Give test 62 a stated peak-RSS number it can actually assert, and reconcile it with
  M-03's finding that the harness does not measure RSS.

### R2-M-13 — Test 0b targets a package that does not implement the guarantee it is named for

- **Lens:** Incorrectness | **Affected:** FR-0002, test 0b

Test 0b is `TestPathsafe_TraversalStillRefused`, Unit, "**The guard that must not regress**".
But `pathsafe` does not implement traversal defence, and says so
(`pkg/pathsafe/pathsafe.go`, `ValidateComponent` doc): *"Callers remain responsible for
rejecting separators, `.`, `..`, NUL, and absolute paths themselves — this function assumes
those are already handled."*

The actual defence is a chain in three other places: `CleanRelPath`'s leading-`/` rejection,
`path.Clean` + `fs.ValidPath`, and the `HasPrefix(seg, "..")` loop
(`pkg/library/root.go:360-388`), plus `os.Root` confinement at the syscall boundary and
`SanitizeUploadFilename`'s own checks (`pkg/agent/upload_workpath.go:169-178`).

A unit test in `pkg/pathsafe` therefore guards none of it — which is the more dangerous shape,
because it is *named* as the guard and will be trusted as one.

- **Fix:** Retarget FR-0002's test to the layer that owns the guarantee: `pkg/library`'s
  `CleanRelPath` (traversal, absolute, `..`-prefixed, encoded variants) **and** the `os.Root`
  confinement, under **both** build tags. Keep a `pathsafe`-level assertion only for the
  narrow properties `pathsafe` does own — and add the `ValidateComponent("..")` case from
  R2-C-02, which is a real regression risk.

### R2-M-14 — FR-112 and AW-3 are circular, and FR-112 has no test

- **Lens:** Inconsistency | **Affected:** FR-112, AW-3, §16, E-4

- FR-112: "MUST report files it cannot address, rather than omitting them silently."
- §16 traceability: `| FR-112 | US-16 | (unaddressable reported) | — see AW-3 |` — i.e. **no test**.
- AW-3: "**Superseded by Stage 0.** … Whatever remains is reported by the health check."
- The health check has no FR of its own beyond FR-038 and no test (R2-M-09).

So the only requirement covering NB-9's "never silently skip" for filenames points at a
decision that points at a stage that removes most of the cases, with the remainder handed to
an unspecified mechanism. And there is a case Stage 0 *creates* that nobody owns: a file
created on macOS with `:` in its name, in a `$OMNIPUS_HOME` later opened by a **Windows**
build. It exists on disk, it lists, and every operation on it 400s — the exact
"silently missing from search" outcome H-7 is designed to catch.

- **Fix:** Give FR-112 a real test and a real surface. Add an acceptance scenario for the
  cross-platform case (POSIX-created name opened by a Windows build): the file must be listed
  as unaddressable with a reason, not silently 400. Rewrite AW-3 to point at that FR rather
  than forward at Stage 0.

### R2-M-15 — How HTML is embedded is still unspecified, and the ADR-044 precedent is never considered

- **Lens:** Incompleteness | **Affected:** FR-005, FR-007, US-1, US-2, §11

`iframe`, `srcdoc`, `sandbox` (the attribute) and `ADR-044` appear **zero times** in the spec.
Yet US-2 AS-2 requires the file to render "inside the preview pane", which means an embedded
browsing context, and FR-005 requires the opaque origin to come from the **response**, not the
embedder — a distinction that only makes sense once you have decided what the embedder is.

Unanswered: `<iframe src>` vs `srcdoc`; whether the frame carries its own `sandbox` attribute
in addition to the response header (they compose, and the intersection is what applies); what
`allow` / `referrerpolicy` are set to; how the "persistent untrusted-content boundary"
(FR-007) is positioned relative to a frame that can resize itself; and what happens when the
document sets `X-Frame-Options` or a `frame-ancestors` of its own.

Pass #1 raised this as M-07 with the ADR-044 context (the SPA's only iframe embed was removed
for a documented isolation reason). Nothing changed.

- **Fix:** Add an FR fixing the embedding mechanism and its attribute set, with the
  composition rule between the frame's `sandbox` attribute and the response's `sandbox`
  directive stated explicitly. Reference ADR-044, state whether this is a departure, and if so
  why the measurement makes it acceptable now.

### R2-M-16 — §2.2 rates `classifyLibraryEntry` HIGH but never grounds STAGE 0's CRITICAL rating, and the two are treated differently

- **Lens:** Inconsistency | **Affected:** §2.2, §4A

§2.2 is a proper impact table for the frontend symbol: risk, impacted count, direct
dependents, all verifiable (and I verified them: `LibraryPreviewPane.tsx:60`,
`LibraryEntryRow.tsx:90`).

STAGE 0's CRITICAL rating for `pathsafe.ValidateComponent` — "17 dependent symbols, 2 direct,
spanning Gateway (13, direct) and Agent (4, direct), with 29 assertions" — is asserted in
prose and **not in §2.2's table**, so the two highest-risk changes in the spec are documented
to different standards. It also does not say which of those dependents are on the
*untrusted-input* path, which is the distinction that matters (R2-C-02): there are four real
call sites — `pkg/library/root.go:398` (operator files, the justified case),
`pkg/agent/upload_workpath.go:179`, `pkg/utils/media.go:97` and `pkg/notifications/store.go:124`
(the last three are not "the operator's own files on their own machine").

- **Fix:** Add `pathsafe.ValidateComponent` and `SanitizeComponent` as rows in §2.2 with the
  same columns. Add a column or a following paragraph classifying each call site as
  operator-supplied or remote/untrusted, and state which ones the relaxation is intended to
  reach. Then re-justify FR-0001 against that list rather than against "mounted folders".

### R2-M-17 — FR-0004 removes a limit the measurement does not justify removing, and confuses runes with bytes

- **Lens:** Incorrectness / Overcomplexity | **Affected:** FR-0004, US-0 AS-4, DS-3 row 7

FR-0004: "MUST NOT apply the component-length limit on **platforms whose filesystem does not
require it**."

Every filesystem Omnipus targets requires one. ext4, APFS, HFS+, XFS, btrfs all cap a single
component at **255 bytes**. `MaxComponentNameLength` is 100 **runes**
(`pkg/pathsafe/pathsafe.go:115`) — a different unit. A 200-rune CJK name is 600 bytes and
fails at `open(2)` with `ENAMETOOLONG` regardless of what FR-0004 says.

The measured need does not support removal either. The reference vault's two over-limit names
are **101 and 106 runes** (§7.1). Both fit comfortably under 255 bytes. Raising the POSIX cap
to the filesystem's real limit fixes every measured case; removing the check converts a clean
400 into a filesystem errno surfacing mid-operation, possibly after a partial multi-file write
in stage 3.

`MaxRelPathLength` (200 runes, `pathsafe.go:123`) is not mentioned by FR-0004 at all, and it is
the constraint a deep vault hits first.

- **Fix:** Restate FR-0004 as: POSIX builds enforce the filesystem's own component limit (255
  **bytes**, measured in bytes) and Windows builds keep the current rune-based budget. State
  what happens to `MaxRelPathLength`. Fix DS-3 row 7 to a platform matrix. Add a test for a
  256-byte name on POSIX asserting a clean, typed rejection rather than an errno.

### R2-M-18 — FR-0003's scenario tests the case that already works and omits the one that is broken

- **Lens:** Incompleteness | **Affected:** FR-0003, US-0 AS-5, test 0c

AS-5 covers a filename containing a **double quote**. The current code already handles that:
`fmt.Sprintf("attachment; filename=%q", filename)` (`pkg/gateway/rest_library.go:592`) escapes
`"` as `\"`, which is valid quoted-string escaping.

What `%q` does *not* handle is non-ASCII: it escapes `Ünïcödé` to `Ü…`, so the browser
offers a file with literal backslash-u escapes in its name. DS-3 row 5 asserts
`Ünïcödé — Näme.md` is "fully addressable" — which is true for addressing and false for
downloading. STAGE 0 makes this strictly more common, since it is the change that admits more
exotic names.

The genuinely correct construction is RFC 5987/6266: an ASCII-safe `filename=` plus a
`filename*=UTF-8''<percent-encoded>` parameter. The spec asks for neither.

- **Fix:** Restate FR-0003 to require RFC 6266 encoding with the `filename*` parameter for any
  name outside ASCII. Extend AS-5 and test 0c to cover a non-ASCII name, a name with a
  semicolon, and (per R2-C-02) a name containing CR/LF — asserting the last is rejected before
  it reaches a header at all.

---

## 5. MINOR

- **R2-m-01** §2.1 lists `workspaceContentType` as though it were the function; it is a
  `map[string]string` (`rest_workspace.go:87`) and the function is `contentTypeForPath` (`:106`).
  Test 3's name was corrected; the symbol table was not.
- **R2-m-02** §16 lists **FR-062 twice**, with different tests (test 63 and test 17), and out of
  numeric order between FR-060 and FR-061. One of the two rows is wrong.
- **R2-m-03** FR-060, FR-061, FR-063, FR-064 and FR-065 all still trace to test 17
  (`TestResolveLink_AllFourForms`), a link-resolution unit test that cannot verify callouts,
  highlights, frontmatter suppression, backlink display, rail collapse or unresolved-link
  click suppression. Pass #1's M-02, minus FR-062.
- **R2-m-04** The inline allow-list is still never enumerated. `.svg` is the case that matters
  and it is now double-relevant: `libraryPreviewKind.ts:14` already classifies `svg` as
  `image` (safe, an `<img>`), while `workspaceContentType` maps it to `image/svg+xml` (a
  scriptable document if opened top-level). `nosniff` does not help — the type is already
  correct. Name the list, and say which side of it SVG is on.
- **R2-m-05** No tool name appears anywhere in the spec (pass #1 M-01). FR-070 requires every
  knowledge tool to be enumerated explicitly with no wildcards, and test 34 asserts zero
  coverage gaps — neither is achievable without the list.
- **R2-m-06** Deep-link encoding is still unspecified (pass #1 m-10): FR-012 says only
  "addressable by URL". No statement on percent-encoding, whether the path is
  workspace-qualified, or what happens to a `#` or `?` in a filename — which STAGE 0 now makes
  legal on POSIX.
- **R2-m-07** §2.1 says `LibraryPreviewPane` "becomes reading mode for KB files". No FR
  defines "reading mode".
- **R2-m-08** §3 P-5 still says "**Required for stage 1**" rather than "To build", after an
  experiment ran and after this review measured that the matrix does not exist.
- **R2-m-09** Tests 37–39 are still `Bench_*`. `go test` will not run them; `go test -bench`
  needs the `Benchmark` prefix. Pass #1's M-03.
- **R2-m-10** Test 62 asserts "bounded peak memory" with no number, against a harness pass #1
  established does not measure RSS.
- **R2-m-11** MV-10 still says the lock bound is "configurable" without naming a config key
  (pass #1 m-06).
- **R2-m-12** No observability requirement anywhere in the spec — no metric, no structured log
  event, nothing for index progress, search latency, or preview policy violations (pass #1
  m-13). Given MV-1..MV-5 are all runtime properties, there is nothing to observe them with in
  production.
- **R2-m-13** No kill switch for inline preview (pass #1 m-14). `gateway.preview_enabled`
  exists as precedent for exactly this and is not cited.
- **R2-m-14** §11's "The browser" section still lists the A14 distinct-origin fallback as the
  failure path, after the measurement retired it (experiment §2.1).

---

## 6. OBSERVATIONS

- **R2-O-01** The experiment document is the strongest artefact in this set. Server-side logs
  as ground truth, a control row that must fire all seven vectors or the harness is declared
  broken, an explicit refusal to record the webfont result as settled, and a self-correction
  in §7.4 admitting the earlier PDF conclusion was right for the wrong reason. It deserves to
  be cited by the spec rather than absorbed by osmosis.
- **R2-O-02** The E2E "hardcoded allowlist" trap (false-green §4) is **already fixed** in this
  repo: `tests/e2e/shards.json` is the single source of truth and `scripts/e2e-shards.sh check`
  fails CI if any spec is unassigned. The spec should simply name the shard each new E2E spec
  joins, and can rely on the check rather than inventing coverage machinery (relevant to
  R2-M-06's fix for test 59).
- **R2-O-03** `pkg/entity`'s cross-process test (re-execs the test binary as real OS processes)
  is correctly cited for test 48. That is the right precedent and it is the strongest test in
  the plan.
- **R2-O-04** §18's holdout scenarios remain excellent, and H-7 in particular is the only place
  in the document that would catch R2-M-14's cross-platform unaddressable case. Consider
  whether one holdout deserves promotion to a required test — carefully, since §18 forbids
  referencing them during the build.
- **R2-O-05** The `/preview/<agent>/<token>/…` mechanism (ADR-044) is an in-tree, already-shipped
  answer to R2-C-03: token-in-path, no cookie, CSRF/Origin prefix-exempt, on the main listener.
  Whether or not it is reused, the spec should say why.
- **R2-O-06** `pkg/pathsafe`'s package doc is a 50-line argued case for platform-independence
  ("a workspace that behaves differently depending on which machine opened it is a portability
  bug"). STAGE 0 reverses that decision. Whatever the outcome, that doc comment must be
  rewritten in the same commit — leaving it in place while the code contradicts it is how the
  next reader concludes the change was a mistake.

---

## 7. Independent measurements

### 7.1 The reference-vault figures are correct

STAGE 0 claims: *"3 of 748 notes are currently unreachable — one for an illegal character, two
for exceeding a 100-character name limit (longest is 106)."* I re-measured against
`/Users/danielpiatkowski/Library/Mobile Documents/iCloud~md~obsidian/Documents/Elicify-KB-Agent-Vault`,
excluding `.obsidian/`:

| Measurement | Result |
|---|---|
| Total `.md` notes | **748** |
| Names containing `<>:"\|?*` | **1** — `Decision — close the ev recall grounding-reference gap in 6 elicify-* skill packages.md` |
| Names over 100 runes | **2** — 101 runes and 106 runes |

Exactly as claimed, including the "longest is 106". This is the finding pass #1's M-19 asked
for and it was delivered properly. Note the consequence for R2-M-17: both long names are far
under any filesystem's 255-**byte** limit, so raising the cap fixes them and removing it is not
required by the evidence.

### 7.2 Codebase claims checked

| Spec claim | Verdict |
|---|---|
| `classifyLibraryEntry` — dependents `LibraryEntryRow`, `LibraryPreviewPane` | **True** (`LibraryEntryRow.tsx:90`, `LibraryPreviewPane.tsx:60`) |
| `buildWorkspaceCSP` — sole caller `setWorkspaceSecurityHeaders` | **True** (`rest_workspace.go:123`) |
| `workspaceContentType` modified → audio fixed on the Library path | **False** — `rest_library.go:593` uses `http.ServeContent` |
| `isSafeHref` (TS) bypassed by a KB-specific link renderer | **False** — no such renderer exists |
| `OpenOrCreate` in `pkg/memrooms/index/index.go` | **True** (`index.go:110`) |
| `AllowedMountRoots` in `pkg/workspace/mount.go` | **True** (`mount.go:365`) |
| `ValidateToolPolicyCoverage` in `pkg/config` | **True** |
| `CleanRelPath`'s `pathsafe` rule produces residual R-7 | **True** (`root.go:398`) |
| "New dependencies: none" | **False** — `pdfjs-dist` absent from `package.json` |
| Three-browser matrix available | **False** — Chromium-only config, Chromium-only CI install |
| §2.4 Go/TS `isSafeHref` divergence | **True** (`pkg/utils/markdown.go:28`, `src/lib/url-safe.ts:16`) |

---

## 8. Structural integrity results (`plan-spec` mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** (US-0 … US-17) |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** — US-0's six scenarios have no BDD block at all; §16 cites "(platform filename matrix)" as a scenario name that does not exist in §12 |
| Every BDD scenario has a `Traces to:` | **PASS** |
| Every BDD scenario has a corresponding test | **FAIL** — pass #1's m-08 (four uncovered) unchanged |
| Every FR appears in the traceability matrix | **PASS**, but FR-062 appears twice with conflicting tests (R2-m-02) |
| Every BDD scenario appears in the matrix | **PARTIAL** |
| Test datasets cover boundary / edge / error | **FAIL** — DS-3 encodes the pre-STAGE-0 verdict (R2-C-01); DS-5 has no PDF.js-specific rows (CJK, forms, malformed, large) |
| Regression impact explicitly addressed | **PARTIAL** — §13.3 exists; two of its six rows are assertions with no guard (pass #1 m-11), and the chat-`%%…%%` regression it needs is missing (R2-M-03) |
| Success criteria measurable, no subjective language | **FAIL** — SC-013 is unsatisfiable as worded (R2-M-02); SC-012 names infrastructure that does not exist; FR-007's "persistent untrusted-content boundary" remains subjective (pass #1 m-09) |

---

## 9. Test coverage assessment

**What improved.** Test 58 requiring a positive control is exactly right and is the model the
rest of the security tests should follow. Test 60's rejection of `document.fonts.status` as an
oracle is a direct, correct application of the experiment's own §3.2. Test 32's "Never byte
comparison" closes a real trap. Test 63 gives AW-9 a real check.

**What still cannot be trusted.**

| Test | Problem | Reference |
|---|---|---|
| 10, 12 | Oracle asserts "cookie is empty"; measurement says it **throws**. Green under total page failure | R2-M-11 |
| 12, 57 | Require three engines / headed; CI has one engine, headless | R2-M-07 |
| 10, 11, 12, 57, 60 | Inherit `retries: 3` — a security assertion that passes on attempt 4 | R2-M-07 |
| 59, 61 | Source-text scans; false-green pattern #2 | R2-M-06 |
| 37, 38, 39 | `Bench_*` — `go test` will not run them | R2-m-09 |
| 1 | Guards a property that contradicts the feature | R2-M-02 |
| 0b | Guards traversal in a package that does not implement it | R2-M-13 |
| 3 | Asserts against a map the serving path does not read | R2-M-01 |
| 60 | No negative control | R2-M-10 |
| 62 | No asserted number; the design cannot deliver the property | R2-M-12 |

**Missing entirely:** a PDF.js hostile-input test (JS action, embedded file, malformed xref) —
the only defence for R2-C-05; a test that chat markdown still renders `%%…%%`; a test for the
POSIX-name-on-Windows-build case; tests for AW-1, AW-2 and AW-5.

---

## 10. STRIDE summary

| Component | Threat | Status |
|---|---|---|
| Inline preview response | **Information disclosure** — session cookie readable | Closed by measurement; **not recorded in the spec** (R2-C-04); oracle is vacuous (R2-M-11) |
| Inline preview response | **Elevation** — page calls the API as the operator | Closed by measurement (`connect-src` + opaque origin); consequence #1 (previews cannot call back at all) unrecorded |
| Inline preview subresources | **Denial of service (self-inflicted)** — 401 on every asset | **OPEN** (R2-C-03) |
| PDF rendering | **Elevation** — parser bug executes on the gateway origin with the session | **OPEN**, and the spec argues the opposite (R2-C-05) |
| PDF rendering | **Tampering** — form/signature writes to the operator's files | Out of scope by omission only; boundary not stated (R2-M-05) |
| Filename ingest (`pathsafe`) | **Tampering** — CR/LF/NUL in a remote-supplied filename | **OPEN** under FR-0001 as written (R2-C-02) |
| Filename ingest | **Elevation** — traversal | Closed, but by code STAGE 0 does not name and test 0b does not reach (R2-M-13) |
| Library serving path | **Spoofing (type confusion)** — HTML served as PDF | Test 58 is well designed; FR-015 is contradicted by `ServeContent`'s sniffing fallback (R2-M-01) |
| KB index | **Information disclosure** — full note text survives revoke by 7 days | Duration decided (AW-8); searchability during grace still unconstrained (pass #1 M-13) |
| Agent retrieval | **Information disclosure** — cross-workspace | Well covered: FR-052/053, MV-12, test 26 as a required negative test |
| Note content → agent prompt | **Injection** | **OPEN** — unchanged from pass #1 M-11 |

---

## 11. Unasked questions

1. If a sandboxed preview's subresources cannot be authenticated by cookie, and ADR-044's
   token path is off the table, **what is the third option?** The spec has to name one.
2. **What is the PDF.js upgrade owner and cadence?** A vendored parser on the session origin
   is a standing obligation, not a one-time integration.
3. **Does the operator ever see which isolation applied?** After revision 3, HTML is sandboxed
   and PDF is not. FR-007's "untrusted content" boundary is one badge for two very different
   postures. Should they read differently?
4. **What happens to a workspace when the build changes platform?** A `$OMNIPUS_HOME` created
   by a macOS build and opened by a Windows build after STAGE 0 — files present, listed,
   400 on every operation. Who tells the operator?
5. **Is `MaxRelPathLength` in or out of STAGE 0?** It is the limit a deep vault hits first and
   FR-0004 does not mention it.
6. **What does search return when the query-time excerpt read (AW-1) fails?** A result with no
   excerpt, a dropped result, or an error? Each is defensible; none is specified.
7. **Is `.svg` inline or attachment?** It is the one extension where the answer changes the
   threat model, and it has been unanswered across two passes.
8. **Does the shipped policy get re-verified after it is written into the handler?** The
   experiment's §5 says the exact string "must be fixed against the real handler and
   re-verified". Nothing in §3 or §13 schedules that.

---

## 12. Verdict

**BLOCK.**

The five CRITICALs are: STAGE 0 contradicting itself and its own dataset (R2-C-01); the
control-character rule being build-tagged away at untrusted ingest points (R2-C-02); the
sandboxed preview's subresources arriving unauthenticated at an authenticated endpoint
(R2-C-03); the measured policy never being written into the spec or cited (R2-C-04); and
PDF.js relocating untrusted parsing onto the session origin while the spec describes it as
having no sandbox to need (R2-C-05).

R2-C-03 and R2-C-04 are cheap to fix and should be fixed together, because both are the same
omission: the experiment's results were read as a verdict and not as a set of facts to write
down. R2-C-01 is an afternoon of editing. R2-C-02 and R2-C-05 are real design decisions that
need a paragraph each and a test each.

To address these findings:

```
/plan-spec --revise docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md docs/internal/specs/adr-067-knowledge-base-and-preview-spec-review-round2.md
```
