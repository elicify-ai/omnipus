# Spec review — ADR-067 knowledge base and render-first preview (adversarial grill, pass #3)

- **Reviewed:** `docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md` (Draft, 1,610 lines)
- **Prior passes:** [round 1](adr-067-knowledge-base-and-preview-spec-review.md) (BLOCK — 5C/21M/17m/7O) · [round 2](adr-067-knowledge-base-and-preview-spec-review-round2.md) (BLOCK — 5C/18M/14m/6O)
- **Supporting:** [ADR-067](../architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md) (D15 rev 3, D15.6, D15.7) · [preview isolation experiment](adr-067-preview-isolation-experiment-2026-08-22.md)
- **Branch:** `feat/library-improvements`, worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements`
- **Date:** 2026-08-22
- **Method:** every code claim below was re-read on this branch in this session. GitNexus `impact` was run against `pathsafe.ValidateComponent`. `false-green-patterns.md` was read from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo` (it is not on this branch).
- **Scope discipline:** round 2's answered findings are dropped, not re-argued. Findings that are genuinely closed are stated as closed. Nothing is padded to raise a count.

---

## 1. Executive summary

**Four CRITICAL, twenty-one MAJOR, thirteen MINOR, six OBSERVATION. Verdict: BLOCK.**

The revision does real work. Round 2's R2-C-01 is properly closed — NB-12 is struck and
superseded, NB-16 replaces it, and DS-3 and E-4 were rewritten so the dataset no longer
asserts the opposite of the acceptance scenarios. R2-C-02 is closed in substance. R2-C-05
is closed in the ADR (D15.7) and largely in the spec. The reframed STAGE 0 — *files Omnipus
owns* versus *files the operator already has* — is a genuinely better argument than revision
1's portability trade, and the mount-immutability evidence supporting it is correct.

But the two things this pass was asked to check hardest both fail, and they fail in the same
way: **a claim was upgraded rather than verified.**

**§0 misreports the document it cites.** The section was created to fix R2-C-04 — the spec
never absorbing the measurement. It opens *"This spec's security requirements are not
proposals. They were measured."* Two of its ten rows are not in the cited document:

- The webfont row is recorded as **"Measured**, with a rendered-width oracle". The experiment
  says, in bold: *"**This finding is not settled, and must not be recorded as if it were.**
  The fixture font is a 68-byte stub, not a valid font… A re-test with a real font file and
  the `Access-Control-Allow-Origin` header is required before any claim about webfonts is
  made."* Its §5 lists webfonts under **"Not settled"**. The string `rendered-width` appears
  **zero** times in the experiment — it is the oracle test 60 is *required to build*, not one
  that was run.
- The type-confusion row is recorded as "Measured, with a positive control". The words
  `confusion`, `nosniff` and `positive control` appear **zero** times in the experiment. That
  measurement exists only in ADR D15.4, which says *"did not execute **in Chromium**"* — one
  engine, a caveat §0 drops while its own header advertises "3 engines".

That is round 2's R2-M-10 inverted rather than fixed, and it is now load-bearing: §0 is the
authority the rest of the spec defers to.

**The measured policy still does not exist as text anywhere.** `default-src`, `script-src`,
`connect-src` — **0 occurrences in the spec**. The ADR names fragments only. The experiment
lists *"the exact shipped directive string"* under **"Not settled"**, records the five policies
by label (`self`, `origin`, `nosandbox`, `sandboxonly`, `none`) rather than by content, and
points reproduction at `scratchpad/csp-exp/`, **which does not exist** anywhere under the
workspace. The winning policy is unrecoverable and the experiment is not reproducible. The
gap between `default-src 'self'` and `connect-src 'none'` is precisely the P0 control of
stage 1, and no artefact in this body of work states which one ships.

**STAGE 0 is not implementable where the spec places it.** `library.CleanRelPath(raw string)`
is a package-level pure function that receives a string and nothing else. All twelve
non-test callers are in `pkg/gateway/rest_library.go`, and every one calls it **before**
`a.openLibraryRoot(...)` — the mount map (`Root.mounts`) does not exist yet. At the instant
the shape rules run, the code cannot know which of STAGE 0's two populations the path belongs
to. §2.1 nevertheless lists `CleanRelPath` as **"Not modified"**. Separately, a build tag is
compile-time and global: it cannot express FR-0001's runtime mount-versus-workspace
distinction, and test 0f explicitly demands the mount exemption hold **"including Windows
builds"**. FR-0001 and FR-0001a therefore require two different mechanisms and the spec names
one.

**A structural note worth stating plainly: the ADR is now ahead of the spec in three places.**
D15.6 records that the MIME table in `rest_workspace.go` is *unreachable* from
`rest_library.go` — the spec's §2.1 still credits `workspaceContentType` with the audio fix.
D15.3 declares *"Scope for this ADR: rendering only"* — the spec never declares form filling
out of scope. D15.6 requires token *revocation on logout* — FR-003b/c drop it. The spec is
the document that gets implemented; it should be brought up to its own ADR.

| Severity | Count |
|---|---|
| CRITICAL | 4 |
| MAJOR | 21 |
| MINOR | 13 |
| OBSERVATION | 6 |
| **Total** | **44** |

### Answers to the questions this pass was asked

| Question | Answer |
|---|---|
| **(a) Are the five criticals really closed?** | **Two fully, one in substance, two partially.** R2-C-01 **closed**. R2-C-02 **closed in substance** (two named gaps, R3-M-17). R2-C-05 **closed in the ADR, mostly in the spec** — but FR-014 still tells the reader the risk vanished (R3-M-08). R2-C-03 **partially** — mechanism chosen, security envelope dropped (R3-C-04). R2-C-04 **partially, and partly falsified** (R3-C-01, R3-C-02). |
| **Does FR-0002b prevent the `..` regression or merely assert it?** | **It prevents it.** Verified: `ValidateComponent("..")` fails today only via `hasTrailingDotOrSpace` (`pathsafe.go:167`) — the claim is exactly right. FR-0002b requires an *independent* rejection and locates the guarantee in the callee rather than in callers, and test 0h names the mutation. That is a mechanism, not a restatement. **Two gaps:** it covers `..` but not `.` (which fails by the same single rule), and test 0h is vacuous under the Windows tag where the trailing-dot rule is still on. |
| **(b) Is the reframed STAGE 0 buildable?** | **No, as written.** The owned/not-owned distinction is not available at the `pathsafe`/`CleanRelPath` call site (R3-C-03). The *argument* is sound; the *placement* is wrong by one layer. |
| **(c) Token delivery (FR-003a/b/c, D15.6)** | Buildable — ADR-044's `/preview/<agent>/<token>/` proves a bare token-path registration works on the main mux, and `ServedSubdirs` is correctly **not** reused. But the spec states no TTL, no revocation, no store, no endpoint, no contract schema, and does not say which URL carries the inline policy (R3-C-04). The logging residual is real and concrete: `pkg/gateway/rest_auth.go:289` logs `r.URL.Path` at WARN, and library routes are wrapped in `withRateLimit`. |
| **(d) Outstanding round-2 majors** | **16 of 18 still stand**, ranked in §4. Two are closed: R2-M-05 is closed *by the ADR* but not by the spec; R2-M-18 was **partly wrong** and is corrected below (R3-M-16). |
| **(e) Test quality vs `false-green-patterns.md`** | Improved where §0 touched it (the cookie BDD now asserts a throw). **Unfixed:** DS-5 and US-2 AS-1 still carry the vacuous oracle (R3-M-02); tests 59/61 are still source-text scans (trap 2); `retries: 3` still applies to security assertions (verified); `Bench_*` still unrunnable; and STAGE 0 introduces a build-tag split whose Windows half **CI never executes** (R3-M-01) — trap "build tags are a cache namespace", in its most consequential form. |
| **(f) Untrue codebase assertions** | **Six.** §2.1 `CleanRelPath` "Not modified"; §2.1 `workspaceContentType` "Modified"; §2.1 `isSafeHref` (TS) "bypassed by a KB-specific link renderer"; §4 "New dependencies: none"; §0's two experiment rows. Verified **true and accurate**: the GitNexus CRITICAL rating (17 impacted / 2 direct — reproduced exactly), the `withUploadAuth` and `SameSite=Strict` facts behind FR-003a, and the `ServedSubdirs` 32-byte/base64url/24h shape behind D15.6. |

---

## 2. Status of round 2's findings

| Round 2 | Status on the current text | Where |
|---|---|---|
| **R2-C-01** Stage 0 self-contradiction | **CLOSED.** NB-12 struck through and marked superseded; NB-16 added; DS-3 rows 2/3/4/8 rewritten as mount-vs-creation verdicts; E-4 rewritten; AS-3 added for the Windows-creation case | one residual: DS-3 row 7 (R3-M-13) |
| **R2-C-02** control chars fused with Windows chars | **CLOSED in substance.** FR-0002a splits the predicate, names both untrusted ingest points, NB-16 makes it unconditional, test 0g asserts NUL/CR/LF under every tag; DS-3 row 5a added | gaps: R3-M-17 |
| **R2-C-03** sandboxed preview cannot authenticate | **PARTIAL.** Problem correctly stated, mechanism chosen, `ServedSubdirs` correctly excluded | R3-C-04 |
| **R2-C-04** measurement never absorbed | **PARTIAL, and partly falsified** | R3-C-01, R3-C-02 |
| **R2-C-05** PDF.js hardening absent | **CLOSED in the ADR (D15.7); mostly in the spec** (FR-019a/b/c, tests 67/67a/68) | residuals: R3-M-08, R3-M-05 |
| R2-M-01 audio MIME points at the wrong function | **Not answered**, and now contradicts ADR D15.6 | R3-M-03 |
| R2-M-02 SC-013 unsatisfiable | Not answered | R3-M-09 |
| R2-M-03 no KB link renderer; FR-011 unscoped | Not answered | R3-M-04 |
| R2-M-04 "no new dependencies" false | Not answered | R3-M-05 |
| R2-M-05 form filling not out of scope | **Closed by ADR D15.3**, not by the spec | R3-M-14 |
| R2-M-06 tests 59/61 source-text scans | Not answered | R3-M-19 |
| R2-M-07 browser matrix / `retries: 3` | Not answered | R3-M-06 |
| R2-M-08 test 57 stale rationale | **Worse** — promoted into §0 as measured ground truth | R3-M-07 |
| R2-M-09 five §17 decisions with no FR/test | Not answered | R3-M-12 |
| R2-M-10 FR-019 overclaims the font fix | **Worse** — now asserted in §0 as measured | R3-C-01 |
| R2-M-11 cookie oracle vacuous | **Half-answered** — §12 BDD fixed, US-2 AS-1/AS-2 and DS-5 not | R3-M-02 |
| R2-M-12 FR-034a vs E-5, chunking ≠ bounded indexer | Not answered | R3-M-10 |
| R2-M-13 test 0b guards a package that lacks the guarantee | Not answered | R3-M-11 |
| R2-M-14 FR-112 ⊥ AW-3, no test | Not answered | R3-m-08 |
| R2-M-15 embedding mechanism unspecified | Not answered | R3-M-15 |
| R2-M-16 pathsafe absent from §2.2 | Not answered | R3-M-18 |
| R2-M-17 FR-0004 runes vs bytes | Not answered; evidence weakened | R3-M-13 |
| R2-M-18 FR-0003 tests the working case | Not answered — **and round 2's evidence was wrong** | R3-M-16 |
| R2-m-01…m-14 | m-01, m-02, m-03, m-04, m-05, m-06, m-07, m-08, m-09, m-10, m-11, m-12, m-13, m-14 all unchanged | §5 |

---

## 3. CRITICAL

### R3-C-01 — §0 records as "Measured" two things the cited experiment does not contain, and one it explicitly forbids recording

- **Lens:** Incorrectness / Insecurity (false assurance)
- **Affected:** §0 rows 6 and 7, FR-019, test 58, test 60, AC-15.1, ADR D15.2

§0's premise is *"This spec's security requirements are not proposals. They were **measured**
on 2026-08-22 and are recorded in [the preview isolation experiment]."* Grepped against that
document:

| §0 row | Experiment says |
|---|---|
| "Webfonts need `Access-Control-Allow-Origin`; CORS is definitively the blocker — **Measured**, with a rendered-width oracle" | §3.2: *"**Likely fix**… **Not verified** — it was not tested."* and *"**This finding is not settled, and must not be recorded as if it were.**"* §5 lists webfonts under **Not settled**. `rendered-width` — **0 occurrences** |
| "An HTML file named `.pdf` does not execute… **Measured**, with a positive control" | `confusion`, `report.pdf`, `nosniff`, `positive control` — **0 occurrences**. The measurement exists only in ADR D15.4, which scopes it to **Chromium** |

The webfont row is the serious one. The experiment did not merely fail to test it — it
explains *why the fixture could not settle it* (a 68-byte stub font that could never render on
any engine), warns that `document.fonts.status` reports `"loaded"` on failure, and instructs
that no webfont claim be made until a re-test with a real font. §0 then makes the claim, and
attributes to it an oracle that the experiment describes as the thing a future test must
build.

FR-019 is unchanged from round 2 and still states the fix as settled. §19 gained no
assumption. Test 60 still has no negative control (the mirror of test 58's positive control):
"rendered width matches the webfont" passes by coincidence whenever the fallback has similar
metrics.

Round 2's R2-M-10 asked for exactly one of two things — mark it unverified, or run the
re-test and cite it. Neither happened; instead the claim was promoted from an FR into the
document's evidence base.

- **Impact:** Every downstream reader now treats CORS-on-fonts as settled. If it is wrong, or
  right for a reason that does not survive contact with the real handler (which serves fonts
  as `application/octet-stream` — `.woff2`/`.woff`/`.ttf`/`.otf` are **absent** from
  `workspaceContentType`, `rest_workspace.go:87-102`), US-1 AS-4 fails at test 60, at the end
  of stage 1, after the policy is frozen. More damaging: §0 is the section the spec now points
  at whenever it declines to restate a control. If §0 is not trustworthy, nothing that defers
  to it is.
- **Fix:** (1) Restate the webfont row as **"Likely, unverified — experiment §3.2"** and add
  the matching assumption to §19, **or** run the re-test with a real font and the header,
  record it as an addendum §8 to the experiment, and cite that. (2) Restate the type-confusion
  row as **"Measured on Chromium only (ADR D15.4); Firefox and WebKit unverified"**, and either
  move that measurement into the experiment document or stop citing the experiment for it.
  (3) Give test 60 the negative control: the same assertion **without** the ACAO header must
  fail. (4) Add a rule to §0 that every row names the section of the cited document it comes
  from — a row with no citation is not evidence.

### R3-C-02 — The winning policy exists as a label, not as text, in any artefact; and the harness that produced it is gone

- **Lens:** Incompleteness / Infeasibility
- **Affected:** FR-005, FR-006, MV-13, §11, §3 P-5, §0

Measured on this branch:

| Artefact | Contains the shipped directive string? |
|---|---|
| The spec | **No.** `default-src` 0 · `script-src` 0 · `connect-src` 0 · `Content-Security-Policy` 1 (FR-019b, about the SPA) · `allow-scripts` 1 (a §0 evidence row, not a requirement) |
| ADR D15.2 | **No.** Fragments only: `script-src 'self'` / `style-src 'self'` quoted as evidence |
| The experiment | **No.** §5: *"**The exact shipped directive string** — the winning *shape* is established; the final list must be fixed against the real handler and re-verified."* §1 names the five policies as `self`, `origin`, `nosandbox`, `sandboxonly`, `none` — **labels**, with no directive text |
| The harness | **Gone.** §6 says `cd scratchpad/csp-exp` — no such directory exists anywhere under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus` |

So the experiment is not reproducible and the policy it settled cannot be read back. This is
not pedantry about wording; the missing text is the P0 control. Two policies both describable
as "the measured shape" differ in the thing that matters:

- `default-src 'self'` satisfies FR-003 (subresources load) and **permits the previewed page
  to `fetch()` any same-origin URL**, including `/api/v1/*`. Cookies do not travel and CORS
  refuses the read, so it is not an authenticated call — but it *is* reachable traffic from
  untrusted content, and nothing in the spec forbids it.
- `default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; media-src 'self'; connect-src 'none'; form-action 'none'; frame-src 'none'; base-uri 'none'`
  is the shape ADR D15.5 actually assumes — it names `connect-src 'none'` as the thing that
  "blocks same-origin fetch". The spec never states it.

Round 2's requested `§10.3 Measured isolation policy` was not added. The consequences it
listed remain live:

- **§11 still names the A14 distinct-origin fallback** as the failure path (line 730), which
  the measurement retired (experiment §2.1: *"The A14 fallback is not needed"*). A14 is a live
  branch in the plan with no owner.
- **§3 P-5 still reads "Required for stage 1"**, as though the matrix run had not happened —
  and, per R3-M-06, the matrix still does not exist.
- **MV-13 is still a paraphrase** ("carries a content-security policy establishing an opaque
  origin"). A machine-verifiable constraint that does not name the string it verifies cannot
  fail. Note also that a CSP does not *establish* an opaque origin; the `sandbox` directive
  does, and no requirement anywhere in the spec names that directive.
- **No requirement states that both mechanisms are needed.** §0 records it as evidence.
  Evidence is not a requirement; an implementer satisfying FR-005/FR-006 with `sandbox` alone
  ships the `sandboxonly` row, which the measurement shows leaks five of seven vectors.

- **Impact:** The implementer re-derives the policy from a table of labels, or re-runs an
  experiment whose harness no longer exists. Either way the frozen artefact of the most
  expensive piece of work in this ADR is a set of five words.
- **Fix:** Add `§10.3 Measured isolation policy` containing the **literal header value as the
  handler will emit it**, one line of rationale per `sandbox` token (`allow-scripts` yes;
  `allow-same-origin`, `allow-popups`, `allow-forms`, `allow-downloads` no, each with the
  vector it closes), the engines and versions, a section-level citation to the experiment, and
  the explicit "neither mechanism is sufficient alone" statement **as a requirement**. Change
  MV-13 to assert that literal string. Retire A14 from §11 with a one-line reason. Change P-5
  to reflect what was and was not run. Restore `scratchpad/csp-exp/` into the repo (or record
  the five policy strings verbatim in the experiment) so the measurement is reproducible.

### R3-C-03 — STAGE 0's owned/not-owned distinction is not available at the call site that enforces it, and §2.1 says the function that would carry it is not modified

- **Lens:** Infeasibility
- **Affected:** FR-0001, FR-0001a, §2.1, tests 0a/0e/0f, US-0 AS-1/AS-3

This is the question that decides whether stage 0 is buildable, and the answer as written is
no.

The shape rules are applied in exactly one place for the Library:

```go
// pkg/library/root.go:350
func CleanRelPath(raw string) (string, error) {
    …
    for _, seg := range strings.Split(cleaned, "/") {
        if strings.HasPrefix(seg, "..") { return "", ErrInvalidPath }
        if err := pathsafe.ValidateComponent(seg); err != nil {   // :398
            return "", fmt.Errorf("%w: %v", ErrInvalidPath, err)
        }
    }
    if err := pathsafe.ValidateRelPathLength(cleaned); err != nil { … }   // :402
```

It is a **package-level function taking one string**. It has no receiver and no context. The
mount/workspace distinction lives on `*library.Root`:

- `Root.mounts map[string]*mountRoot` (`root.go:152`), populated by `openMountRoots` from
  `workspace.LoadMounts` (`root.go:203`).
- `Root.mountFor(rel)` decides membership from the **first segment** of an
  already-cleaned path (`root.go:284`).

And the ordering is fixed the wrong way round at every call site. All twelve non-test callers
are in `pkg/gateway/rest_library.go` (lines 234, 276, 308, 358, 385, 533, 571, 609, 614, 668,
673), and each has this shape:

```go
rel, err := library.CleanRelPath(rawPath)        // shape rules run HERE
if err != nil { jsonErr(w, 400, "invalid path"); return }
root, ok := a.openLibraryRoot(w, workspaceID, "root")   // mounts become known HERE
```

At the moment the shape rules run, `Root.mounts` does not exist. **The code cannot know which
of STAGE 0's two populations the path belongs to.** Implementing FR-0001 requires either
splitting `CleanRelPath` into a shape-free lexical clean plus a shape check applied by
`*Root`, or reordering all twelve handlers and threading the root in. §2.1 lists
`CleanRelPath` as **"Not modified"** and describes its role as merely producing "residual
R-7". That is the single most load-bearing code fact in stage 0 and the spec has it backwards.

**Second, unrelated mechanism problem.** FR-0001a says the Windows rules apply *"only in
Windows builds, via build tags"*. A build tag is a **compile-time global**. It cannot express
FR-0001's runtime population distinction — and FR-0001 is not POSIX-only: §4A's table says
mounted folders have "None of ours" rules, and **test 0f explicitly requires the mount
exemption to hold "on every platform, including Windows builds"**. So a Windows build needs
*both* the tag (for workspace storage) and the runtime mount check (for mounts). FR-0001a's
"via build tags" is at best half the mechanism, and nothing says so.

**Third, a missing axis.** FR-0001a governs files Omnipus **creates**. `CleanRelPath` is
called on read *and* write paths — its own comment says *"Applies to every path operation
(read and write)"*. So the enforcement needs three inputs — build tag, mount-vs-workspace,
create-vs-read — and has a function that receives none of them.

**Fourth, a case STAGE 0 creates and nobody owns.** On a Windows build, Omnipus writing a new
file *inside a mount* is exempt under FR-0001 (not workspace storage) and unrestricted under
FR-0001a (not a Windows-build workspace write). The name reaches the syscall, NTFS refuses it,
and a clean typed 400 becomes a raw errno mid-operation — in stage 3, possibly after a partial
multi-file link rewrite (FR-104's journal covers interruption, not this).

- **Impact:** Stage 0 gates everything. An implementer who takes §2.1 at its word writes the
  build tag into `pathsafe`, discovers on the first mounted-folder test that the tag also
  relaxed workspace storage (or that Windows builds now reject the operator's mounted files),
  and redesigns mid-stage. The alternative — relaxing `pathsafe` globally on POSIX and calling
  it done — satisfies every FR-0001a reading and quietly makes FR-0001's "workspace storage"
  clause dead text on macOS and Linux.
- **Fix:** (1) State the enforcement point explicitly: shape validation moves off
  `CleanRelPath` and onto a `*Root` method (or `CleanRelPath` gains a policy parameter the
  handler derives from the root). Update §2.1 — `CleanRelPath` is **Modified**, and it is a
  higher-risk change than anything currently in §2.2. (2) Split FR-0001a into the runtime
  predicate (population) and the compile-time predicate (platform) and say both are required.
  (3) Add the create-vs-read axis, or state that shape rules apply on write only and that
  reads are unconditionally permissive — which is the simpler design and matches the "Omnipus
  reads what is on disk" argument. (4) Add an acceptance scenario for Omnipus creating a file
  inside a mount on a Windows build.

### R3-C-04 — FR-003a/b/c drop the security envelope that ADR D15.6 specifies, and the token becomes an unbounded bearer credential

- **Lens:** Insecurity (Information Disclosure) / Incompleteness
- **Affected:** FR-003a, FR-003b, FR-003c, tests 64/65/66, FR-080, US-2 AS-1

The mechanism choice is right and it is buildable. Verified: `/api/v1/library` and
`/api/v1/library/` are registered `a.withUploadAuth(withRateLimit(configLimiter,
a.HandleLibrary))` (`rest.go:5216-5217`), `withUploadAuth` → `withAuthAndBodyLimit`
(`rest.go:9484`) is real auth; the session cookie is `SameSite: http.SameSiteStrictMode`
(`session_cookie.go:148`). ADR-044's `/preview/<agent>/<token>/` proves a bare, path-token,
Origin-exempt registration works on the main mux (`middleware/origin.go` header, lines 32-42).
`ServedSubdirs` is correctly **not** reused, for the reason D15.6 gives (verified: one-per-agent
eviction, `served_subdirs.go:139-153`; `maxTokenLifetime = 24h`, `:44`; 32 random bytes →
base64 RawURL, `:115-118`).

The problem is what the spec kept from D15.6 and what it dropped:

| D15.6 property | In the spec? |
|---|---|
| Minted only by an authenticated request; never widens access | **Yes** — FR-003b |
| Scoped to one workspace + one path or bundle root | **Yes** — FR-003b |
| 32 random bytes, base64url | **No** |
| Lifetime "short and bounded, well under 24h" | **Partly** — FR-003b says "MUST be short-lived". **No number anywhere.** No MV |
| **Revocation: expiry plus invalidation on logout** | **No — dropped entirely.** Neither FR-003b nor FR-003c mentions logout |
| Accepted residual: token in logs, history, `Referer` | **Partly** — FR-003c mandates `Referrer-Policy: no-referrer` and says nothing about logs or history |
| Not `ServedSubdirs`, and why | Only as a §13.3 regression row, with no reason |

Consequences:

1. **No TTL number makes test 65 unwritable.** `TestPreviewToken_ScopeAndExpiry` must assert
   "expired token is refused" against an unstated bound. This is the same defect round 2
   raised for AW-8's 7 days (R2-M-09) — a decision that lives only in prose cannot be tested.
2. **No revocation means the token outlives the session that minted it.** With logout
   invalidation dropped, a token minted by an administrator remains a valid, unauthenticated
   read grant for the whole (unstated) TTL after they log out. That is exactly the "grants
   access the caller does not already have" outcome FR-003b forbids, arrived at by omission.
3. **The logging residual is concrete and unmitigated.** `pkg/gateway/rest_auth.go:289` logs
   `slog.Warn("api: rate limit exceeded", "ip", ip, "path", r.URL.Path, …)`. The library routes
   are already wrapped in `withRateLimit`. If the token path is rate-limited the same way — and
   a bare, unauthenticated, file-serving path very much should be — every 429 writes a live
   credential into `gateway.log` at WARN. Neither the ADR nor the spec names this site.
4. **No storage model, no endpoint, no contract type.** FR-080 requires every cross-boundary
   type to be defined in `contracts/` *before* implementation code. A token-mint request and
   response are new wire types and the spec names neither a schema nor a route. Nor does it say
   whether the store is in-memory (previews break across a gateway restart — probably fine, but
   it is a decision) .
5. **Which URL carries the inline policy is undefined.** US-2 AS-1 says the file is opened
   "as a top-level browser tab at **its Library URL**". After FR-003a there are two URLs for the
   same bytes: the authenticated `/api/v1/library/…?path=` (which today hard-codes
   `Content-Disposition: attachment`, `rest_library.go:592`) and the new token path. The spec
   never says which is "its Library URL", which one gets the sandbox headers, or whether the
   authenticated path keeps serving attachments. Test 64 and test 10 target different URLs
   depending on how a reader resolves that.

- **Impact:** A URL-borne read capability with no stated lifetime, no revocation, and a
  known in-tree path that logs it. The most likely implementation — mirror `ServedSubdirs`,
  reuse its 24h ceiling — produces a day-long credential, which is not "short-lived" by any
  reading.
- **Fix:** Promote D15.6's table into the spec verbatim. Add **MV-18: preview-token TTL is
  N minutes** (pick N; 5–15 is defensible for a page the operator is looking at) and point
  test 65 at it. Add **FR-003d: preview tokens are invalidated on logout and on workspace
  mount revocation.** Add **FR-003e: the token path is excluded from any log statement that
  records the request path**, naming `rest_auth.go:289` as the site to fix, or require the
  token to be redacted there. Name the contract schemas and the mint route. State explicitly
  that `/api/v1/library/…` continues to serve `attachment` and that only the token path
  serves inline, and rewrite US-2 AS-1 to name the token URL.

---

## 4. MAJOR

Ranked by what goes wrong soonest and worst.

### R3-M-01 — STAGE 0's build-tag split creates a half of the codebase CI never executes

**Affected:** FR-0001a, tests 0a, 0e, 0g, 0h; §4 "CI is the authority"

Verified: `.github/workflows/*.yml` contains **no** Windows job. The only Windows reference in
the whole build system is `Makefile:213` and `Makefile:238` — `GOOS=windows GOARCH=amd64 $(GO)
build`. CI **cross-compiles** for Windows and never **tests** it.

Test 0a is "Same input, opposite verdicts per build tag". Test 0e is "The 29 existing
assertions still hold **under the Windows tag**". Neither can run in CI as it stands. And the
spec does not say which kind of tag it means:

- **A `GOOS`-implicit split** (`pathsafe_windows.go` / `pathsafe_other.go`) needs a Windows
  runner. `GOOS=windows go test` does not execute on Linux.
- **An explicit custom tag** (`-tags winnames`) can run on any runner, but then it is not a
  platform split at all — it is a flag, and an operator running the Linux binary with the tag
  set gets Windows rules.

`false-green-patterns.md` — *"Build tags are a cache namespace"* — is the relevant warning in
its most consequential form: a `make test` under `goolm,stdjson` shares nothing with, and
proves nothing about, a build under a different tag set. Half of stage 0's behaviour would
ship with zero executed assertions while CI reported green.

**Fix:** State which tagging mechanism is used. If `GOOS`, add a Windows CI job (or at minimum
`GOOS=windows go vet` plus a table-driven test whose *rule set* is a parameter, so both
verdicts are exercised on one runner). Say in §13 which gate runs which tag set.

### R3-M-02 — R2-M-11 is half-fixed: the acceptance scenarios and the dataset still carry the vacuous oracle

**Affected:** US-2 AS-1, US-2 AS-2, DS-5 row 5, tests 10, 12

§12's BDD was corrected properly — *"reading the cookie **THROWS** a SecurityError (it does
not return an empty string — asserting 'empty' also passes when the page failed to load)"*.
The two places tests are actually written from were not:

- **US-2 AS-1** (line 282): *"**Then** the read yields **nothing** and the document's origin
  is opaque."*
- **US-2 AS-2** (line 283): *"the same holds."*
- **DS-5 row 5** (line 1259): `| 5 | html reading document.cookie | **empty** | US-2 AS-1 |`

So the spec now asserts two different observables for the same P0 control, in three places,
and the two that a QA engineer works from are the wrong ones. This is structurally identical
to R2-C-01 — the dataset encoding the superseded verdict — reproduced for a different item
after R2-C-01 was fixed.

**DS-5 carries a second withdrawn verdict in the very next rows.** Row 7 (line 1261) reads
`| 7 | doc.pdf | **native viewer** | US-1 AS-5 |`. That is D15 **revision 2**'s decision. Under
revision 3 the browser's viewer is never involved, and US-1 AS-5 says so explicitly — *"displayed
inside the pane by Omnipus's own renderer… **not handed to the browser's viewer**"*. A test
written from DS-5 row 7 asserts the opposite of the acceptance scenario it traces to.

Neither test 10 nor test 12 mandates the positive control the experiment demonstrated (the
same page under `nosandbox` reading back `omnipus_probe=SECRET`), even though test 58 was
correctly given one.

**Fix:** Rewrite AS-1, AS-2 and DS-5 row 5 to "throws `SecurityError`; `window.origin ===
"null"`". Rewrite DS-5 row 7 to "PDF.js canvas + text layer in the pane". Require the positive
control in tests 10 and 12. Assert egress by **server-observed request arrival**, not console
text (experiment §4 shows the wording differs per engine).

### R3-M-03 — The audio-MIME fix still points at an unreachable map, and the ADR now says so while the spec does not

**Affected:** §2.1, FR-009, FR-015, MV-14, test 3, AC-15.3

§2.1 still reads: `workspaceContentType` | `pkg/gateway/rest_workspace.go` | **"Modified" —
audio MIME types added**. Re-verified on this branch:

- `workspaceContentType` is a `map[string]string` at `rest_workspace.go:87`, read only by
  `contentTypeForPath` (`:106`), whose only callers are `rest_workspace.go:322,346` and
  `rest_preview.go:289,314`. **No Library caller.**
- `handleLibraryDownload` ends `http.ServeContent(w, r, filename, fi.ModTime(), f)`
  (`rest_library.go:593`), which derives the type from `mime.TypeByExtension` — a **host**
  lookup — and **sniffs** the first 512 bytes when that misses.
- The map contains no audio extensions **and no font extensions** (`.woff2`, `.woff`, `.ttf`,
  `.otf` are all absent), which also undercuts FR-019.

ADR **D15.6 states this correctly**: *"The MIME table lives in `rest_workspace.go` and is
**unreachable** from `rest_library.go` — the download handler serves via `http.ServeContent`,
which also **sniffs**, so the inline path must set the type explicitly."* ADR AC-15.3 likewise
requires assertion *"against the **Library** handler, not the workspace MIME table"*. The spec
contradicts its own ADR on a checkable code fact, and test 3
(`TestContentTypeForPath_AudioExtensions`) is aimed at the function the Library never calls —
it will pass green while Library audio serves as `application/octet-stream`.

FR-015 ("MUST derive `Content-Type` from the file extension, **never from content sniffing**")
is additionally violated by the code that ships today, and the spec does not record that as a
change it must make.

**Fix:** Correct §2.1 to name `rest_library.go`'s serving path. Add an FR: the Library inline
path MUST set `Content-Type` from an **in-binary** extension table before `ServeContent`,
never consulting the host MIME registry and never sniffing, with a stated default for unknown
extensions. Point test 3 at that table. Add the font extensions while you are there.

### R3-M-04 — The KB link renderer still does not exist, the code says building one was deliberately rejected, and FR-011 will change chat

**Affected:** §2.1, FR-011, FR-013, FR-060, FR-061, NB-4, §13.3, SC-014

§2.1 still asserts `isSafeHref` (TS) is *"Not modified — **bypassed by a KB-specific link
renderer**"*. Re-verified: no such renderer exists.
`src/components/library/preview/LibraryMarkdownPreview.tsx:8` imports
`HistoricalMessageMarkdown` from `@/components/chat/historical-markdown` and renders with it
directly (`:27`). The file's own header states:

> *"View reuses HistoricalMessageMarkdown **VERBATIM** … This is **deliberately NOT a second
> markdown pipeline**."*

So the spec assumes a component that does not exist and whose absence is a recorded design
decision in the code. No FR anywhere requires building one.

The sharp end is unchanged: **FR-011** — *"The system MUST hide `%%…%%` comments when
rendering markdown"* — is unscoped. Implemented in the only renderer that exists, it hides
`%%…%%` in **chat**, i.e. in untrusted model and tool output. §13.3 promises chat is
untouched, but its guard row and **SC-014** both cover *link handling only*. Nothing catches
this.

**Fix:** Add an FR mandating a Library/KB markdown renderer distinct from the chat one, naming
what may diverge (relative links, `%%…%%`, wikilinks, callouts, highlights) and what must be
inherited. Scope FR-011's text to it. Add a `%%…%%`-in-chat regression test and a §13.3 row.
Correct §2.1 and note that the code currently argues against this design.

### R3-M-05 — "New dependencies: none" is still false, and PDF.js's runtime assets are still unaccounted for

**Affected:** §4, FR-018, FR-019a, test 61

Re-verified: `pdfjs-dist` does not appear in `package.json` on this branch. §4 still says
*"**New dependencies: none.** `bleve/v2` is already direct."* FR-018 and ADR D15.3 both
require it (D15.3 names version `6.2.108` and size `~1.6 MB`). Under CLAUDE.md constraint #1
it must also be embedded into the single binary via `go:embed`, which the spec does not
mention.

Beyond the JS bundle, PDF.js fetches `cmaps/` and `standard_fonts/` **per document** at
runtime from the serving origin. Vite does not bundle these; they are copied assets. Omit
them and CJK PDFs render **blank pages with no error** — the silent-degradation shape this
project has a document about. `cmaps` and `standard_fonts` appear **0 times** in the spec, and
FR-018's "lazily loaded" does not cover them because they are per-document, not per-bundle.

D15.7 also requires *"Version pinned and updated deliberately — this is a parser exposed to
hostile input."* FR-019a covers `eval`, XFA and scripting but **not** the version floor, and
no FR names an upgrade owner or cadence.

**Fix:** Correct §4 to name `pdfjs-dist` with a version floor. Add an FR for the `cmaps/` and
`standard_fonts/` asset paths — where they are copied, that they are embedded, how they are
served — with a test that renders a CJK PDF and asserts **glyph presence**, not "no error".
Add the version floor and an upgrade owner to FR-019a.

### R3-M-06 — The three-engine matrix still does not exist and security assertions still inherit `retries: 3`

**Affected:** SC-012, tests 10, 11, 12, 57, 60, 64, 67a; §3 P-5, §11

Re-verified on this branch:

- `playwright.config.ts` declares **no `projects`** — Chromium only. No Firefox, no WebKit.
- `playwright.config.ts:21` — `retries: process.env.CI ? 3 : 2`, sized for real-LLM flakes.
- Nothing runs headed; tests 57, 64 and 67a demand HEADED.
- §3 P-5 still reads "**Required for stage 1**".

`retries: 3` is the dangerous half. "The cookie was not readable" and "the request did not
reach the external origin" are not properties that a fourth attempt can establish. A
retry-tolerant security assertion reports identically to a real one.

**Fix:** Change P-5 to "To build" with the work named: Playwright `projects` for
firefox/webkit, the CI install line, the headed/xvfb decision, and whether Safari-proper is in
scope or SC-012 should say "WebKit" and admit the gap. Require the isolation specs to run at
**`retries: 0`** via a per-file override and say so in §13. Assign each new E2E spec to a shard
in `tests/e2e/shards.json` (verified present, with `scripts/e2e-shards.sh` as its checker).

### R3-M-07 — The "headed" rule is stale under revision 3 and has been promoted into §0 as measured ground truth

**Affected:** §0 "Test rule that follows", test 57, ADR AC-15.4

§0 states as a consequence of measurement: *"PDF and preview end-to-end tests **must run
headed**. Headless Chromium has no PDF viewer and headless WebKit renders no PDFs at all; both
previously produced false negatives."*

That is a true statement about the **browser's** PDF viewer, and the experiment's §7.4 says
exactly that. Under D15 revision 3 the browser's PDF viewer is never involved: PDF.js draws to
a `<canvas>`, which headless renders correctly. The requirement is carrying the justification
for a design that was **withdrawn in the same revision**, and using it to mandate the single
most expensive piece of infrastructure in the plan (xvfb on Linux plus a macOS runner for
Safari).

Round 2 raised this as R2-M-08 about test 57's note. It has since been elevated from a test
note into §0, the section the spec presents as non-negotiable evidence — so it is now harder,
not easier, to challenge.

**Fix:** Scope the headed rule to the cases that need it (a top-level `.pdf` navigation, if any
remains; the browser-viewer negative control). State PDF.js's actual oracle: non-blank canvas
pixels **plus** text-layer content matching a known string. Keep multi-engine only if there is a
stated reason PDF.js differs per engine.

### R3-M-08 — FR-014 still tells the reader the risk vanished, and FR-019b is unfalsifiable

**Affected:** FR-014, FR-017, FR-019b, test 68, AC-15.9

R2-C-05 is substantially closed — FR-019a/b/c exist, D15.7 is thorough, and test 67a
(`E2E_HostilePdfFailsInert`) is the right shape. Two residuals:

1. **FR-014 is unchanged.** It still ends: *"…are drawn by SPA components, never become
   browser documents, and **therefore have no sandbox to apply**."* FR-019a's own preamble
   contradicts it: *"Rendering PDFs moves untrusted parsing onto the authenticated SPA
   origin."* A reader who stops at FR-014 — and FR-017 instructs the documentation to follow
   FR-014's framing — concludes the risk disappeared. Round 2's fix item (1) was to rewrite
   that clause; it was not done.
2. **FR-019b requires "a Content-Security-Policy" with no directives.** Test 68
   (`TestSpaServedWithCSP`) therefore asserts only that a header exists. `Content-Security-Policy:
   default-src *` passes. This is a false-green in specification form. It also omits the
   collision that will actually bite: the SPA carries Shiki, Mermaid and (now) PDF.js, and a
   CSP without `unsafe-eval` / `wasm-unsafe-eval` may break them — which is the *reason*
   `isEvalSupported: false` matters and is worth stating.
3. **FR-019b and FR-019c can defeat each other, silently.** PDF.js loads its worker from a
   separate script URL, commonly a `blob:` in a bundled build. An SPA CSP that omits
   `worker-src blob:` (or `script-src blob:`) makes the worker fail to instantiate, at which
   point PDF.js falls back to its *fake worker* — main-thread parsing. That is precisely the
   outcome FR-019c forbids ("MUST NOT silently fall back to main-thread parsing"), reached by
   satisfying FR-019b. Neither requirement mentions the other, and test 67 asserts the
   configuration object, not which thread parsing ran on.
4. **§2.1 and §2.2 name no SPA-serving symbol.** A first-ever CSP on the SPA shell is a change
   to the handler that serves `pkg/gateway/spa/` via `go:embed`; that symbol appears in neither
   table, so the change has no impact assessment at all — while `classifyLibraryEntry`, a
   frontend classifier, has a full one.

**Fix:** Rewrite FR-014's final clause to say SPA-rendered formats are parsed **in the SPA's
own origin**, and their containment is the parser's correctness, not an origin boundary; make
FR-017 require that framing. Give FR-019b a directive list (at minimum: no `unsafe-eval`, an
explicit `object-src 'none'` and `base-uri 'none'`, and whatever `worker-src`/`wasm-unsafe-eval`
PDF.js, Shiki and Mermaid actually need), and have test 68 assert that string. Add an assertion
to test 67 or 67a that parsing ran **on the worker** — the only non-vacuous check for FR-019c.
Add the SPA-serving handler to §2.1/§2.2.

### R3-M-09 — SC-013 is still unsatisfiable and the row-icon change is still a side effect

**Affected:** §2.2, SC-013, test 1, FR-001, FR-018

Unchanged from round 2. SC-013 demands *"zero diffs against the current classification
table"*, while FR-001/FR-018 require exactly three pre-existing inputs to change kind.
Re-verified against `libraryPreviewKind.ts:30-40`: `report.html` → `'text'` (via
`is_text_editable`), `manual.pdf` → `'other'`, `podcast.mp3` → `'other'`. A guard written to
SC-013's literal wording fails the moment the feature lands; one written to pass has been
weakened to whatever the implementer left in it.

The blast radius is still mis-stated: widening `LibraryPreviewKind` forces changes in **both**
consumers — `LibraryPreviewPane.tsx:60` (which surface) and `LibraryEntryRow.tsx:90` (which
icon). The row icon for every `.html`, `.pdf` and `.mp3` in every workspace changes, and no
scenario mentions it.

**Fix:** Restate SC-013 as a frozen allow-list of intended diffs — commit the current table as
a checked-in fixture **before** the change; test 1 asserts the new table differs in exactly the
three enumerated rows and nowhere else. Add an acceptance scenario for the icon change.

### R3-M-10 — FR-034a and E-5 still contradict each other, and chunked reading still does not bound a full-text indexer

**Affected:** FR-034a, AW-7, E-5, test 62, MV-2

- **FR-034a:** *"MUST NOT impose a maximum note size… memory safety comes from reading files
  in bounded chunks."*
- **E-5:** *"Note 200 MB in size | Indexed with a **documented body cap**, or **skipped** and
  reported."*

These are opposites and both are current text. Round 2 raised it; nothing changed.

The mechanism claim is also still wrong. Chunked *reading* bounds the read buffer. bleve's
unit of work is the **document**: analysing a 200 MB document produces a token stream and a
per-document term dictionary that must exist before the segment is written. There are two real
designs — one index document per note (peak memory scales with the largest note, so MV-2's
512 MB becomes a property of the biggest file, not the collection) or splitting a note across
several index documents (bounded, but hit counts, ranking, dedup, excerpt offsets and backlink
attribution now operate on fragments). The spec picks neither. Test 62 asserts "bounded peak
memory" with **no number**, against a harness that does not measure RSS.

**Fix:** Choose a design and write it into FR-034a. Fix E-5 to match. Give test 62 a stated
peak-RSS figure and a harness that can read it.

### R3-M-11 — Test 0b still guards traversal in a package whose own documentation disclaims it

**Affected:** FR-0002, test 0b, US-0 AS-6

Test 0b (`TestPathsafe_TraversalStillRefused`, marked *"The guard that must not regress"*)
targets `pkg/pathsafe`. `ValidateComponent`'s doc comment says: *"Callers remain responsible
for rejecting separators, `.`, `..`, NUL, and absolute paths themselves — this function assumes
those are already handled."* Verified: `illegalRunes` deliberately excludes `/` and `\`, and
there is no traversal logic in the package.

The real defence is a chain elsewhere — `CleanRelPath`'s NUL check, backslash rejection,
leading-`/` rejection, `path.Clean` + `fs.ValidPath`, and the `HasPrefix(seg, "..")` loop
(`root.go:353-397`), plus `os.Root` confinement at the syscall boundary and
`SanitizeUploadFilename`'s own checks (`upload_workpath.go:169-178`). A unit test in
`pkg/pathsafe` reaches none of it — and is *named* as the guard, so it will be trusted as one.

The addition of test 0h makes this worse, not better: 0h is a genuine, well-aimed
`pathsafe`-level assertion, which lends 0b false credibility by association.

**Fix:** Retarget FR-0002's test to `pkg/library.CleanRelPath` (traversal, absolute,
`..`-prefixed, backslash, NUL, encoded variants) **and** the `os.Root` confinement, under both
tag sets. Keep a `pathsafe`-level assertion only for what `pathsafe` owns — which is what 0h
already is.

### R3-M-12 — Five of the nine §17 decisions still have no requirement and no test

**Affected:** §17, §14, §13.1

Unchanged from round 2, re-verified against the current text:

| Decision | FR? | Test? | Consequence |
|---|---|---|---|
| **AW-1** excerpt re-read at query time | **No** — FR-050 says only "returning path, title and matched excerpt" | **No** | Unspecified when the file changed between index and query (the excerpt may not contain the match), when the file is cloud-evicted (E-16 covers index time; a query-time read can block), and unbudgeted against MV-1's 500 ms p95 across up to 20 files |
| **AW-2** attachments indexed by filename only | **No** | **No** | DS-2 row 5 has 100,000 attachments; nothing forbids a content read — which is the whole guarantee |
| **AW-5** a KB is exactly one mounted folder | **No** | **No** | Nothing rejects a second mount claiming the same collection; FR-031's realpath ref-count is a different invariant |
| **AW-6** health check automatic, on a schedule | Partly (FR-038) | **No** | No interval, no failure surface, no statement of the exclusive-index-lock consequence AW-6 itself raises |
| **AW-8** 7-day grace period | Partly (FR-109, "a grace period") | Test 52, with no value to assert | "7 days" appears nowhere but §17 |

**Fix:** Promote each to an FR carrying the number (AW-8 → an MV). Give AW-1, AW-2 and AW-5 a
test each. For AW-1, specify the failure case: what the response contains when the query-time
read fails or the file no longer contains the match.

### R3-M-13 — FR-0004 still confuses runes with bytes, and DS-3 row 7 deleted the case that would have caught it

**Affected:** FR-0004, US-0 AS-4, DS-3 row 7, `MaxRelPathLength`

FR-0004 is unchanged: *"MUST NOT apply the component-length limit **on platforms whose
filesystem does not require it**."* Every filesystem Omnipus targets requires one — ext4,
APFS, HFS+, XFS and btrfs all cap a component at **255 bytes**. `MaxComponentNameLength` is
**100 runes** (`pathsafe.go:115`), a different unit. A 200-rune CJK name is 600 bytes and fails
at `open(2)` with `ENAMETOOLONG` whatever FR-0004 says. So FR-0004's condition is never
satisfied and its intent — "let long names through" — is expressed as a test that cannot be
evaluated.

The revision made the evidence weaker rather than the requirement stronger: **DS-3 row 7
changed from a 300-character basename to a 106-character one**. 106 runes is comfortably under
255 bytes and passes under any reasonable design; 300 was the only row in the dataset that
would have exposed the rune/byte confusion and the errno-instead-of-400 failure. The dataset
now contains no case above the filesystem limit.

`MaxRelPathLength` (200 runes, `pathsafe.go:123`) is still not mentioned by FR-0004 at all, and
it is the constraint a deep vault hits first — `CleanRelPath` enforces it at `root.go:402`,
after the per-component loop.

**Fix:** Restate FR-0004: POSIX builds enforce the filesystem's own component limit measured
in **bytes** (255); Windows builds keep the rune budget. State what happens to
`MaxRelPathLength`. Restore a >255-byte row to DS-3 and assert a clean typed rejection, not an
errno.

### R3-M-14 — Form filling and signing are declared out of scope in the ADR and nowhere in the spec

**Affected:** NB-13, NB-14, FR-018, AW-10, AW-11

ADR D15.3 closes this cleanly: *"**Scope for this ADR: rendering only.** Form filling and
signing are recorded as *proven feasible* so the choice of library is made with them in view.
Shipping them is a later decision with its own user stories."*

The spec never says it. NB-13 excludes cryptographic signatures; NB-14 excludes XFA and
**agent-driven** filling. Read together by an implementer, they exclude the edges and thereby
**endorse** human form filling and human drawn signatures — for which there is no user story,
no acceptance scenario, no FR and no test. AW-10's *"Blocks **promising** form filling, not
rendering"* tells you what may not be promised, not what is being built.

**Fix:** Add an NB to the spec mirroring D15.3: PDF preview is read-only in this release; the
experiment's §7 establishes feasibility for a future decision, not a commitment. Reword
NB-13/NB-14 as reinforcements of it rather than carve-outs from an assumed capability.

### R3-M-15 — How HTML is embedded is still unspecified, and ADR-044 is still never mentioned

**Affected:** FR-005, FR-007, US-1, US-2, §11, FR-003a

`iframe` appears **once** in the spec — inside §0's list of egress vectors. `srcdoc`,
`ADR-044` and the `sandbox` *attribute* appear **zero** times. Yet US-2 AS-2 requires the file
to render "inside the preview pane" (an embedded browsing context), and FR-005 requires the
opaque origin to come from the **response** rather than the embedder — a distinction that only
means something once you have decided what the embedder is.

Still unanswered: `<iframe src>` versus `srcdoc`; whether the frame carries its own `sandbox`
attribute in addition to the response directive (they compose, and the **intersection**
applies); what `allow` and `referrerpolicy` are set to; how FR-007's "persistent
untrusted-content boundary" is positioned relative to a frame that can resize itself; and what
happens when the document sets its own `X-Frame-Options` or `frame-ancestors`.

The ADR-044 omission is now conspicuous rather than merely incomplete: FR-003a **is** ADR-044's
mechanism, rediscovered. The spec adopts the technique and never cites the precedent, so a
reader cannot tell that the departure from §13.3's "no `ServedSubdirs`" row was considered.

**Fix:** Add an FR fixing the embedding mechanism and its attribute set, with the composition
rule between the frame `sandbox` attribute and the response `sandbox` directive stated
explicitly. Cite ADR-044 in FR-003a and say what is reused and what is not.

### R3-M-16 — FR-0003 tests a case that already works; round 2's evidence for this was itself wrong

**Affected:** FR-0003, US-0 AS-5, test 0c, DS-3 row 5

**Correcting round 2 first.** R2-M-18 asserted that `fmt.Sprintf("%q", …)` escapes `Ünïcödé`
to backslash-u sequences. **That is false.** Verified by running it:

```
in: Ünïcödé — Näme.md   → "Ünïcödé — Näme.md"      (non-ASCII preserved)
in: a\r\nX: y.md        → "a\r\nX: y.md"           (CR/LF escaped)
in: he said "hi".md     → "he said \"hi\".md"      (quote escaped)
in: nul\x00.md          → "nul\x00.md"             (NUL escaped)
```

So `%q` preserves printable non-ASCII and escapes control characters. Two useful consequences:
CR/LF header injection is **already blocked at this site** (relevant to FR-0002a's rationale,
which implies otherwise), and the double-quote case AS-5 tests **already works**.

**The finding nonetheless stands, for a different reason.** Raw UTF-8 bytes in a
`filename=` quoted-string are not RFC 6266 conformant; the conformant construction is an
ASCII-safe `filename=` plus `filename*=UTF-8''<percent-encoded>`. `RFC` appears **0 times** in
the spec. STAGE 0 makes exotic names strictly more common, and DS-3 row 5 asserts
`Ünïcödé — Näme.md` is "fully addressable" — true for addressing, unverified for downloading.

FR-0003 as written ("MUST correctly encode filenames in HTTP headers, including names
containing quotes") and test 0c ("Header injection via filename") therefore both aim at
behaviour the code already has.

**Fix:** Restate FR-0003 to require RFC 6266 encoding with `filename*` for any non-ASCII name.
Extend AS-5 and test 0c to a non-ASCII name and a name containing a semicolon. Keep a CR/LF
case, but assert it is rejected *before* reaching a header (which is FR-0002a's job) rather
than escaped at it.

### R3-M-17 — FR-0002a names only `firstIllegalRune`; the sanitiser half and the `.` component are uncovered

**Affected:** FR-0002a, FR-0002b, NB-16, test 0g, `pkg/utils/media.go`, `pkg/notifications/store.go`

FR-0002a is the right requirement and it names the right rationale. Two gaps:

1. **The fusion exists in two functions, and FR-0002a names one.** `firstIllegalRune`
   (`pathsafe.go:332`) is the validation path. `replaceIllegalRunes` (`pathsafe.go:273`) is the
   **sanitisation** path and carries the identical `if r <= 0x1F || strings.ContainsRune(illegalRunes, r)`
   predicate. It is what `SanitizeComponent` uses, and `SanitizeComponent` is the *only*
   defence at `pkg/utils/media.go:97` (inbound chat-attachment filenames from Discord, Feishu,
   Telegram — remote, attacker-chosen) and at `pkg/notifications/store.go:124`. FR-0002a's
   phrasing ("They are fused in `pathsafe.firstIllegalRune` today") reads as a complete
   description of the fusion, and it is not. `SanitizeComponent` and `replaceIllegalRunes`
   appear **0 times** in the spec.
   *Mitigating:* the most natural implementation — making the `illegalRunes` constant
   build-tag-dependent — leaves the `r <= 0x1F` branch intact in both functions and is
   therefore safe by accident. FR-0002a should require that outcome rather than depend on it.
2. **`.` is not covered.** FR-0002b requires `..` to be rejected independently. Verified,
   `ValidateComponent(".")` also fails **only** via `hasTrailingDotOrSpace` — the same single
   rule. A component named `.` would validate on a build where that rule is off. Round 2's
   requested unconditional set was "C0 controls, path separators, `.`/`..`/`..`-prefixed";
   the spec kept two of the three items in the third.

Also note test 0h (*"`..` must fail with the trailing-dot rule disabled"*) is **vacuous under
the Windows tag**, where that rule is still enabled — it can only fail on the relaxed build.

**Fix:** Extend FR-0002a to name `replaceIllegalRunes`/`SanitizeComponent` and to state that
`SanitizeComponent`'s untrusted-input contract is unchanged on every platform. Extend FR-0002b
to `.` as well as `..`. Add `SanitizeComponent("a\rb")` to test 0g. State that 0h must run
under both tag sets and is expected to be non-vacuous only under the relaxed one.

### R3-M-18 — `pathsafe` is still absent from §2.2's impact table, and no call site is classified by trust

**Affected:** §2.2, §4A

§2.2 is a proper impact table for the frontend symbol. STAGE 0's CRITICAL rating for
`pathsafe.ValidateComponent` is still asserted in §4A prose and **not** in that table, so the
two highest-risk changes in the spec are documented to different standards.

**Credit where due — the numbers are exactly right.** GitNexus `impact ValidateComponent
--direction upstream` on this branch returns `impactedCount: 17`, `risk: "CRITICAL"`,
`summary.direct: 2`, `modules_affected: 2`, `processes_affected: 5`, with depth counts
`{1: 2, 2: 11, 3: 4}`. The spec's "17 dependent symbols, 2 direct" is reproduced precisely.
(The prose gloss "Gateway (13, direct)" is loose — the gateway's 12 direct callers are of
`CleanRelPath`, not of `ValidateComponent`, which has exactly two direct callers:
`root.go:398` and `upload_workpath.go:179`.)

What is missing is the distinction that decides the change: which call sites carry
**operator-supplied** paths and which carry **remote/untrusted** ones. There are four —
`pkg/library/root.go:398` (operator files: the justified case), `pkg/agent/upload_workpath.go:179`,
`pkg/utils/media.go:97` and `pkg/notifications/store.go:124`. The last three are not "the
operator's own files on their own machine", which is the entire premise of §4A's argument.

**Fix:** Add `ValidateComponent` and `SanitizeComponent` as rows in §2.2 with the same columns.
Add a trust classification per call site and re-justify FR-0001 against that list.

### R3-M-19 — Tests 59 and 61 are still source-text scans, and the in-tree precedent for doing it properly is still uncited

**Affected:** tests 59, 61; FR-016, FR-018

`false-green-patterns.md` §2: *"A guard test asserted a component still called
`shouldRenderToolCall` by checking the file text contained that identifier. Deleting the entire
gate and leaving the name in a comment kept **673/673 passing**. **Rule:** assert on behaviour,
never on source text."*

- **Test 59** (`TestInlineAllowList_RequiresTypeConfusionTest`): the only way to implement
  "a test exists for extension X" is to scan test source for X. A comment mentioning `.svg`
  satisfies it; a test named for `.svg` that asserts nothing satisfies it.
- **Test 61** (`TestPdfJsBundleLazyLoaded`): the obvious implementation greps the built
  `index-*.js`. A renamed chunk or a changed minifier turns it green. It also carries a Go test
  name for a frontend build artefact, and nothing guarantees a build output exists when it runs.

The repo already solved the general problem: `tests/e2e/shards.json` is a single source of
truth and `scripts/e2e-shards.sh` fails CI on any unassigned spec (both verified present). The
spec cites neither — `shards.json` appears **0 times**.

**Fix:** Test 59 — make coverage structural: derive the allow-list from a table pairing each
extension with a test-case identifier, and have the unit test iterate the table and fail on a
missing or empty case, so the coverage relation is data the compiler and runner both see.
Test 61 — assert at runtime in a browser test that no PDF.js request occurs before a PDF is
selected and one does after; rename it to the frontend convention.

### R3-M-20 — The inline allow-list is still never enumerated, and `.svg` is still unanswered across three passes

**Affected:** FR-008, FR-015, FR-016, MV-13, test 4

FR-008 ("MUST continue to serve non-allow-listed file types as attachments") and FR-016
("MUST fail its build if an extension is added to the inline allow-list without a
type-confusion test") both depend on a list that appears nowhere. `svg` appears **0 times** in
the spec.

`.svg` is the case where the answer changes the threat model, and it is now sharper than in
round 2 because FR-003a introduces an unauthenticated path that serves bytes inline.
Re-verified: `libraryPreviewKind.ts:14` classifies `svg` as `image` (drawn via `<img>` — safe,
scripts inert), while `workspaceContentType` maps `.svg` → `image/svg+xml`
(`rest_workspace.go:95`) — a fully scriptable document if fetched top-level. `nosniff` does not
help; the type is already correct.

**FR-014's exemption is unsound for exactly this format.** It exempts images from sandboxing on
the grounds that they "never become browser documents". An SVG opened at its own URL — which is
what US-2 AS-1 requires to be safe, *"opened as a top-level browser tab at its Library URL"* —
**is** a browser document, on the gateway origin, running its own `<script>`. The exemption is
stated by *class* ("images") where the property it relies on holds only by *rendering context*
(inside an `<img>`). Whether the token path serves `.svg` inline decides whether an
agent-authored SVG is a picture or a script.

**Fix:** Enumerate the inline allow-list as a table in §10.2. State which side `.svg` is on and
why. Restate FR-014's exemption in terms of rendering context rather than format class. If SVG
is inline, it needs an AC-15.5-style test of its own.

### R3-M-21 — §16 asserts full ADR acceptance-criteria coverage and omits six, all of them the new security criteria

**Affected:** §16, spec header, §10 preamble, ADR AC-15.5 … AC-15.10

Counted on this branch: the ADR contains **43** distinct `AC-x.y` identifiers. The spec's header
says *"(20 decisions, **37** acceptance criteria)"*, and §16's coverage table contains exactly
**37** — introduced by the sentence *"**Every** ADR `AC-x.y` maps to a named test"*, with §10's
preamble reinforcing it: *"the mapping is asserted in §16."*

The six missing are contiguous and are not incidental:

| Missing | What it covers |
|---|---|
| **AC-15.5** | HTML named `.pdf` does not execute — the type-confusion control |
| **AC-15.6** | PDF.js bundle absent from the initial SPA payload |
| **AC-15.7** | Adding an inline extension without a test fails CI |
| **AC-15.8** | A hostile PDF fails inert |
| **AC-15.9** | The SPA is served with a CSP |
| **AC-15.10** | PDF.js `eval`/XFA/scripting disabled, asserted at the call site |

That is every acceptance criterion added by D15.4 and D15.7 — i.e. the entire output of
round 2's R2-C-05. §13.1's test rows *do* cite them (tests 58, 61, 59, 67a, 68, 67), so the
tests are planned; the defect is that the table which asserts completeness, and the header count
that summarises it, were not updated with the ADR. A reader auditing coverage from §16 concludes
the PDF hardening has none.

**Fix:** Add the six rows, correct the header to 43, and — since this is the third revision in
which a prose decision moved without §16 following (see R3-O-06) — re-derive the table
mechanically rather than by hand.

---

## 5. MINOR

- **R3-m-01** §2.1 lists `workspaceContentType` as though it were a function; it is a
  `map[string]string` (`rest_workspace.go:87`) and the function is `contentTypeForPath` (`:106`).
- **R3-m-02** §16 lists **FR-062 twice** (lines 1474 and 1476), with different tests (63 and 17),
  and out of numeric order between FR-060 and FR-061. One row is wrong.
- **R3-m-03** FR-060, FR-061, FR-063, FR-064 and FR-065 all still trace to test 17
  (`TestResolveLink_AllFourForms`), a link-resolution unit test that cannot verify callouts,
  highlights, frontmatter suppression, backlink display, rail collapse or unresolved-link click
  suppression.
- **R3-m-04** No tool name appears anywhere in the spec. FR-070 requires every knowledge tool to
  be enumerated explicitly with no wildcards and test 34 asserts zero coverage gaps — neither is
  achievable without the list. **ADR D17 already names four** (`knowledge_search`,
  `knowledge_graph`, plus unnamed authoring tools with per-agent postures); the spec need only
  cite it.
- **R3-m-05** Deep-link encoding is still unspecified. FR-012 says only "addressable by URL" —
  no statement on percent-encoding, workspace qualification, or what happens to a `#` or `?` in a
  filename, which STAGE 0 now makes legal on POSIX. `?path=` (ADR D16) is not carried into the spec.
- **R3-m-06** §2.1 says `LibraryPreviewPane` "becomes reading mode for KB files". No FR defines
  "reading mode".
- **R3-m-07** Tests 37–39 are still `Bench_*`. `go test` will not run them; `go test -bench`
  requires the `Benchmark` prefix. Raised in round 1 and round 2.
- **R3-m-08** FR-112 and AW-3 are still circular, and FR-112's traceability row still reads
  "— see AW-3", i.e. **no test**. The cross-platform case remains unowned: a file created on
  macOS with `:` in its name, in a `$OMNIPUS_HOME` later opened by a Windows build — present,
  listed, and 400 on every operation.
- **R3-m-09** MV-10 still says the lock bound is "configurable" without naming a config key.
- **R3-m-10** No observability requirement anywhere — no metric, no structured log event, nothing
  for index progress, search latency, preview policy violations, or preview-token minting.
  MV-1…MV-5 are all runtime properties with nothing to observe them in production.
- **R3-m-11** No kill switch for inline preview. `gateway.preview_enabled` (live, read
  per-request, no restart) is the in-tree precedent for exactly this and appears **0 times**.
- **R3-m-13** §2.1 records `buildWorkspaceCSP` as "Referenced — its gaps drive the new inline
  policy; not itself changed", but never states what the gaps are or that the weaker posture
  **stays**. Verified (`rest_workspace.go:50-70`): `/serve/` renders agent-authored HTML with
  `script-src 'unsafe-inline'`, `connect-src 'self'` and `form-action 'self'` — same-origin, no
  `sandbox` directive. After stage 1, agent HTML is strictly isolated in the Library and
  materially less isolated on `/serve/`. That may be a deliberate and defensible split (a dev
  server is a different job), but two different postures for the same content class should be a
  recorded decision, not an inference a reader has to make from a "not changed" row.
- **R3-m-12** Two dangling cross-references: E-3 ("§1.2 targets met") and P-4 ("any §1.2
  performance claim") point at a §1.2 that does not exist in this document — §1 is "Available
  Reference Patterns (N/A)". They mean ADR §1.2.

---

## 6. OBSERVATIONS

- **R3-O-01** **The reframed STAGE 0 argument is correct and worth keeping.** The
  mount-immutability evidence checks out — `pkg/workspace/mount_test.go` does assert *"HostPath
  must NEVER change on its own"*, and a mount storing a realpath-resolved absolute host path
  genuinely has no cross-machine portability scenario. The conclusion "there is no Windows
  scenario in which a mounted file's name matters" follows. The defect is placement (R3-C-03),
  not reasoning — which is a better position than revision 1 was in.
- **R3-O-02** **ADR D15.6 and D15.7 are the strongest new writing in this body of work.** D15.6
  names the discovery, its provenance ("found by the round-2 review"), the gap in the
  measurement that hid it, the mechanism, a property table, an explicitly accepted residual, and
  the reason `ServedSubdirs` is not reused — all of which I verified as accurate. The spec's
  FR-003a/b/c are a lossy summary of it. The cheapest fix in this review is to copy D15.6's
  table into §14.
- **R3-O-03** **`pkg/pathsafe`'s package doc is a 50-line argued case for platform-independence**
  — *"a workspace that behaves differently depending on which machine opened it is a portability
  bug"* — and `CleanRelPath`'s own comment at `root.go:392-396` repeats it: *"See pathsafe's
  package doc for why these rules apply unconditionally rather than only when actually running on
  Windows."* STAGE 0 reverses that decision. Both comments must be rewritten in the same commit;
  leaving them while the code contradicts them is how the next reader concludes the change was a
  mistake. Raised as R2-O-06 and still not in the spec's work list.
- **R3-O-04** **The experiment remains the best artefact here, and its integrity is exactly what
  §0 failed to preserve.** It refused to record the webfont result, flagged its own harness
  defect, disclosed the WebKit confound rather than smoothing it, and self-corrected in §7.4.
  The spec then recorded the one thing it refused to record. Restoring `scratchpad/csp-exp/` and
  the five policy strings into the repo would make it durable as well as honest.
- **R3-O-06** **The same failure has now occurred in all three revisions, in the same three
  places.** A decision changes in the prose and §9 (Edge Cases), §13.2 (Datasets) or §16
  (Traceability) keeps the superseded verdict. Revision 2: DS-3 and E-4 held the pre-STAGE-0
  filename verdicts (R2-C-01). Revision 3: DS-5 rows 5 and 7 hold the pre-revision-3 cookie and
  PDF verdicts (R3-M-02), E-5 holds the pre-AW-7 size-cap verdict (R3-M-10), and §16 holds the
  pre-D15.7 acceptance-criteria count (R3-M-21). These three sections are what tests are written
  from, so a stale row there is not cosmetic — it produces a passing test that asserts the
  opposite of the decision. **A grep of those three sections against every decision changed in a
  revision is cheaper than a fourth review pass**, and it is the one process change that would
  have prevented three of this pass's majors.
- **R3-O-05** §18's holdout scenarios are still excellent, and H-6 ("the hostile page") and H-7
  ("the awkward collection") remain the only places in the document that would catch R3-C-03's
  Windows-build case and R3-m-08's cross-platform unaddressable file. They cannot be relied on:
  §18 forbids referencing them during the build.

---

## 7. Structural integrity (`plan-spec` mode)

| Check | Result | Change since round 2 |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** (US-0 … US-17) | — |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** — US-0's six scenarios still have no BDD block; §16 cites parenthetical prose ("mounted files are read as-is") where a scenario name belongs | unchanged |
| Every BDD scenario has a `Traces to:` | **PASS** | — |
| Every BDD scenario has a corresponding test | **FAIL** | unchanged |
| Every FR appears in the traceability matrix | **PASS**, but FR-062 appears twice with conflicting tests | unchanged |
| Test datasets cover boundary / edge / error | **FAIL** — DS-3 now agrees with STAGE 0 (**fixed**), but row 7 lost its >255-byte boundary (R3-M-13); DS-5 rows 5 and 7 still encode the superseded cookie and PDF verdicts (R3-M-02); DS-5 still has no PDF.js rows (CJK, malformed, large, hostile) | partly improved |
| ADR acceptance criteria fully mapped | **FAIL** — §16 asserts completeness and covers 37 of the ADR's 43; the six omitted are exactly D15.4's and D15.7's (R3-M-21) | new |
| Regression impact explicitly addressed | **PARTIAL** — §13.3 has six rows; two are assertions with no guard, and the chat-`%%…%%` regression is still missing | unchanged |
| Success criteria measurable, no subjective language | **FAIL** — SC-013 unsatisfiable; SC-012 names infrastructure that does not exist; FR-007's "persistent untrusted-content boundary" still subjective | unchanged |
| Requirements traceable to measurement | **FAIL** — §0 exists (**improvement**) but two of ten rows misreport the cited document | new |

---

## 8. Test-quality assessment against `false-green-patterns.md`

**Genuinely improved.** §12's cookie scenarios now assert a **throw** and say why "empty" is
vacuous. Test 58 requires a positive control. Test 60 rejects `document.fonts.status` as an
oracle. Test 32 says "Never byte comparison". Test 67a (`E2E_HostilePdfFailsInert`) is the test
R2-C-05 asked for. Test 48's cross-process design (re-exec the test binary, matching
`pkg/entity`) is still the strongest thing in the plan.

**Still not trustworthy:**

| Test | Problem | Finding |
|---|---|---|
| 10, 12 | The acceptance scenarios and DS-5 they are written from still say "empty"/"yields nothing" | R3-M-02 |
| 57 | DS-5 row 7 tells its author to assert the **browser's native viewer**, which revision 3 removed | R3-M-02 |
| 67 | Asserts a config object; nothing asserts parsing actually ran on the worker (FR-019c) | R3-M-08 |
| 10, 11, 12, 57, 60, 64, 67a | Inherit `retries: 3`; require three engines and headed against a Chromium-only, headless config | R3-M-06 |
| 0a, 0e | Windows tag half never executes in CI | R3-M-01 |
| 0b | Guards traversal in a package that disclaims it | R3-M-11 |
| 0h | Vacuous under the Windows tag (the rule it mutates is still on) | R3-M-17 |
| 3 | Asserts against a map the Library serving path never reads | R3-M-03 |
| 59, 61 | Source-text scans — trap #2 | R3-M-19 |
| 62 | No asserted number; the design cannot deliver the property | R3-M-10 |
| 65 | Asserts an expiry with no stated TTL | R3-C-04 |
| 68 | Asserts a header exists, not what it says | R3-M-08 |
| 1 | Guards a property that contradicts the feature | R3-M-09 |
| 37–39 | `Bench_*` — `go test` will not run them | R3-m-07 |

**Missing entirely:** a chat-`%%…%%` regression test; a CJK-PDF glyph test; a negative control
for test 60; a POSIX-name-on-Windows-build test; tests for AW-1, AW-2, AW-5; a test that the
preview token is invalidated on logout.

---

## 9. STRIDE delta since round 2

| Component | Threat | Status |
|---|---|---|
| Inline preview response | Information disclosure — session cookie readable | Closed by measurement; policy **still not written down** (R3-C-02); oracle **half-fixed** (R3-M-02) |
| Inline preview subresources | Denial of service (self-inflicted) — 401 on every asset | **CLOSED** — token path chosen (FR-003a), mechanism verified buildable |
| Preview token | Information disclosure — URL-borne credential in logs/history | **NEW / OPEN** — no TTL, no revocation, concrete logging site unmitigated (R3-C-04) |
| PDF rendering | Elevation — parser bug on the gateway origin | **Substantially closed** by D15.7 / FR-019a-c; FR-014 still argues the opposite (R3-M-08) |
| PDF rendering | Tampering — form/signature writes to operator files | Closed **in the ADR**; still open by omission in the spec (R3-M-14) |
| Filename ingest (`pathsafe`) | Tampering — CR/LF/NUL in a remote-supplied filename | **Closed for the validation path** (FR-0002a, NB-16, test 0g); sanitisation path unnamed (R3-M-17) |
| Filename ingest | Elevation — traversal | Closed in code; FR-0002b now adds a real independent `..` guard (**good**); test 0b still aimed at the wrong layer (R3-M-11) |
| Library serving path | Spoofing (type confusion) | Test 58 well designed; FR-015 still contradicted by `ServeContent` sniffing, and the measurement is Chromium-only (R3-M-03, R3-C-01) |
| Inline allow-list | Elevation — scriptable SVG served inline | **OPEN** across three passes (R3-M-20) |
| KB index | Information disclosure — full note text survives revoke | Duration decided (AW-8, prose only); searchability during grace still unconstrained |
| Note content → agent prompt | Injection | **OPEN** — unchanged since round 1 |

---

## 10. Unasked questions

1. **Where does the preview-token store live, and what happens across a gateway restart?**
   In-memory is probably right; it is still a decision with a visible symptom (a preview
   iframe that 404s after a restart).
2. **Does the token path go through `withRateLimit`?** If yes, `rest_auth.go:289` logs the
   token. If no, an unauthenticated file-serving path has no rate limit at all. Both need an
   answer.
3. **Which of the two URLs is "its Library URL" in US-2 AS-1?** The tests differ.
4. **Is `MaxRelPathLength` in or out of STAGE 0?** Still unmentioned, still the limit a deep
   vault hits first.
5. **Who owns the PDF.js version?** A parser exposed to hostile input on the session origin is
   a standing obligation. D15.7 says "updated deliberately"; nobody is named.
6. **What does search return when the AW-1 query-time excerpt read fails?** A result with no
   excerpt, a dropped result, or an error? Each is defensible; none is specified.
7. **Does the operator ever see which isolation applied?** HTML is sandboxed; PDF is parsed
   in-origin. FR-007's one badge covers two very different postures.
8. **Does the shipped policy get re-verified against the real handler?** The experiment's §5
   requires it. Nothing in §3 or §13 schedules it, and the harness to do it with is gone.

---

## 11. Verdict

**BLOCK.**

Three of round 2's five criticals are genuinely closed and should be recorded as closed —
R2-C-01, R2-C-02 and (in the ADR) R2-C-05. The reframed STAGE 0 is a better argument than what
it replaced. D15.6 and D15.7 are good, careful writing.

What blocks is narrower than last time and sharper:

1. **§0 misreports the experiment** (R3-C-01). The section built to fix "the spec never
   absorbed the measurement" records as *Measured* a finding the measurement explicitly
   forbids recording, and attributes to it an oracle that does not exist. Correcting two table
   rows and adding one assumption fixes it; leaving it means every downstream reader inherits a
   false assurance from the document's own evidence base.
2. **The policy still does not exist as text** (R3-C-02), in any of the three artefacts, and
   the harness that produced it is gone. This is an hour of writing and it is the single
   highest-value hour available.
3. **STAGE 0 is placed one layer too low** (R3-C-03). `CleanRelPath` cannot see the
   distinction the requirement depends on, and §2.1 says it is not modified. This is a design
   decision, not an edit — but a small one: move shape validation onto `*Root`, and say that a
   build tag alone cannot express it.
4. **The preview token is specified without its security envelope** (R3-C-04). The ADR already
   has the missing content; copying D15.6's table into §14, adding a TTL number and a
   logout-revocation FR, closes it.

The twenty-one majors are dominated by round-2 findings that were not worked rather than by new
discoveries — which is itself the signal. R3-M-01 (the Windows half of stage 0 never runs in
CI) and R3-M-02 (the cookie oracle fixed in one place of three, plus the PDF row nobody
noticed) are the two that would otherwise ship as green.

**The cheapest process change available** is R3-O-06: three sections — §9, §13.2 and §16 —
have now retained a superseded verdict in every revision, and they are the three a test author
reads. Grepping them against each changed decision before the next revision would have
prevented R3-M-02, R3-M-10 and R3-M-21 outright.

To address these findings:

```
/plan-spec --revise docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md docs/internal/specs/adr-067-knowledge-base-and-preview-spec-review-round3.md
```
