# Feature Specification: Omnipus knowledge base and render-first preview

- **Implements:** [ADR-067](../architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md) (20 decisions, 43 acceptance criteria)
- **Origin:** [issue #632](https://github.com/elicify-ai/omnipus/issues/632); founder interview 2026-08-21 ([requirements](library-improvements-requirements-2026-08-21.md))
- **Branch:** `feat/library-improvements`
- **Date:** 2026-08-22
- **Status:** Draft — implementation-ready for stages 1 and 2; stages 3 and 4 specified in full but gated.

Structured by ADR-067 D20's four stages. Every acceptance criterion in the ADR
(`AC-x.y`) maps to at least one named test in §12; the mapping is asserted in §16.

---

## 0. Measured ground truth

This spec's security requirements are not proposals. They were **measured** on 2026-08-22 and
are recorded in
[the preview isolation experiment](adr-067-preview-isolation-experiment-2026-08-22.md) —
5 candidate policies × 2 load modes × 3 engines (Chromium 151, Firefox 153, WebKit 26.5),
twice each, with **server-side request logs as ground truth** rather than in-page reports.
24 of 25 compared rows were identical across engines.

| Claim | Status |
|---|---|
| `sandbox allow-scripts` (no `allow-same-origin`) + source directives blocks **all seven** egress vectors (image, fetch, beacon, WebSocket, iframe, form, popup) | **Measured** |
| Under that policy `document.cookie` and `localStorage` **throw `SecurityError`** — they do not return empty | **Measured.** Any test asserting "empty" is a false-green: it also passes when the page failed to load |
| CSP `'self'` **does** match under an opaque origin | **Measured.** The ADR's original warning was wrong; the distinct-origin fallback is retired |
| An explicit origin behaves identically to `'self'` and additionally breaks behind a reverse proxy | **Measured** |
| Both mechanisms are required — `window.open` escapes source directives alone; five of seven vectors escape `sandbox` alone | **Measured** |
| Webfonts need `Access-Control-Allow-Origin`; CORS is the blocker | **Measured — Chromium only** (experiment §6A.2), with a space-advance oracle after the fixture's own oracle was found broken. **Not measured on Firefox or WebKit** — those runs were stopped. `document.fonts.status` reports `"loaded"` on failure and **must not** be used |
| An HTML file named `.pdf` does not execute — blocked by content-type dispatch, **even with no CSP** | **Measured — Chromium only** (experiment §6A.3), with a positive control proving the detection was not blind. **Not measured on Firefox or WebKit** |
| PDF.js writes form values and ink signatures correctly, rendered by macOS PDFKit | **Measured** (experiment §7) — one hand-built single-field form. Complex forms untested |
| Adobe Acrobat compatibility; complex real-world forms; Safari headful PDF; PDF size threshold | **NOT verified** — see AW-10/11/12. Nothing in this spec may assume them |
| A sandboxed preview can authenticate | **DISPROVED** — the experiment harness was an unauthenticated static server, which hid this. See FR-003a |

**A conclusion in §3.1 was later corrected by §6A.1** — headless Chromium has no PDF viewer, so an earlier "PDF fails under sandbox everywhere" reading was partly an artifact. Headed, a *top-level* PDF renders even sandboxed; a *framed* PDF is blocked. The Library case is framed, so the practical conclusion held, but the reasoning did not.

**Test rule that follows — narrowed, with the derivation shown.** The headed rule is a fact about
the **browser's own** PDF viewer, which D15 revision 3 removed from the Library path: PDF.js draws
into a canvas, and a canvas renders identically headless. Headed is required **only** where the
browser's own PDF handling is what is being measured — the top-level `.pdf` type-confusion case
(headless Chromium turns every `.pdf` navigation into a download, so "no script ran" would be true
for the wrong reason) and the browser-viewer negative control. Everything else stays headless.
**That case also needs a third control:** a genuine PDF served at top level must *render* in the
same run, or the result is **inconclusive** rather than a pass.

**Superseded rule:** Headless
Chromium has no PDF viewer and headless WebKit renders no PDFs at all; both previously
produced false negatives.

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
| `workspaceContentType` | `pkg/gateway/rest_workspace.go` | **Referenced only — NOT modified.** Verified: read solely by `contentTypeForPath`, whose four call sites are all in `rest_workspace.go` and `rest_preview.go`. The Library never reaches it, so adding audio keys here would change nothing a reader sees |
| `handleLibraryDownload` | `pkg/gateway/rest_library.go` | **MODIFIED** — the real serving path. Hard-codes `attachment` and delegates the type to `http.ServeContent`; needs an inline mode and must set `Content-Type` itself |
| `libraryContentType` (new) | `pkg/gateway/rest_library.go` | **NEW** — the in-binary extension→type table the Library consults. Deliberately not shared with the workspace map: different allow-lists, different threat models |
| `isSafeHref` (TS) | `src/lib/url-safe.ts` | **Not modified** — bypassed by a KB-specific link renderer |
| `isSafeHref` (Go) | `pkg/utils/markdown.go` | **Not modified** — recorded for the divergence in §2.4 |
| `OpenOrCreate` | `pkg/memrooms/index/index.go` | **Not modified** — pattern copied into a sibling package |
| `CleanRelPath` | `pkg/library/root.go` | **MODIFIED (Stage 0)** — keeps addressing safety unconditionally; name-shape validation moves out of it to the create path, after root resolution |
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

New dependencies: **one — `pdfjs-dist`**, floor `6.2.108`, Apache-2.0. `bleve/v2` is already
direct. (An earlier revision said "none". That was false.)

**It is not one file, and the ADR's "~1.6 MB" counts only the JavaScript.** Measured from the
published tarball: `cmaps/` 1.6 MB (character maps for CJK and other non-Latin encodings),
`standard_fonts/` 800 KB (the 14 base fonts, for PDFs that embed none), `wasm/` 1.5 MB (JPEG 2000
and JBIG2 decoders — scanned documents), `iccs/` 20 KB. Real embedded cost **≈ 5.5 MB**.
`build/pdf.sandbox*.mjs` (65 KB) — the engine that runs a PDF's own JavaScript — **MUST NOT
ship**: disabling scripting while shipping its interpreter leaves the control one edit from being
undone.

---

# STAGE 0 — Platform-correct filenames (gates nothing; everything else depends on it)

Founder decision, 2026-08-22: *"we need something like a build flag — for linux and mac we
support all filenames; windows specifics need only go into a windows build and not limit
linux and mac."*

## 4A. User Story — Stage 0

### US-0 — My own filenames are not refused (Priority: P1)

As an operator I want Omnipus to open **the documents already sitting on my disk**, whatever
they are called. I did not name them for Omnipus's convenience, and a tool that reads my
folder should show me what is in it.

**Why P1:** it gates the knowledge-base work. A collection Omnipus cannot fully address is a
collection it cannot fully index, search or link — and the operator is never told which files
are missing. Measured on the reference vault: **3 of 748 notes are currently unreachable** —
one for an illegal character, two for exceeding a 100-character component limit (longest is
106 runes). None of those three was named by Omnipus.

> ⚠️ **CRITICAL-risk change.** Impact analysis rates `pathsafe.ValidateComponent` **CRITICAL**
> — 17 dependent symbols, 2 direct, spanning Gateway (13, direct) and Agent (4, direct), with
> 29 assertions in `pkg/pathsafe/pathsafe_test.go` locking in current behaviour. CLAUDE.md
> forbids proceeding past a CRITICAL rating without explicit acknowledgement; this section is
> that acknowledgement.

#### Which call sites this touches, and who supplies the input

The acknowledgement above is not enough on its own, because Stage 0's argument is *"these are the
operator's own files"* — and that is true of **one** of the four places these functions are
called. Enumerated from source; there are no others outside tests.

| Call site | Where the name comes from | Trust | What Stage 0 may do |
|---|---|---|---|
| `library.CleanRelPath` | the `?path=` of a Library call, naming a file already on disk | **Operator-controlled, authenticated** — the justified case | Name-shape rules stop applying. Containment untouched (FR-0002) |
| `agent.SanitizeUploadFilename` | the `filename` field of a browser upload | **Semi-trusted** — caller authenticated, value not validated | **Nothing.** A create, so shape rules stay on |
| `utils.SanitizeFilename` | the filename an attachment carries from **Discord, Telegram, Feishu, QQ** | **Untrusted and remote — attacker-chosen** | **Nothing, on any platform.** Its own doc says it *"genuinely CANNOT reject"* — only rewrite, since there is nobody to error to |
| `notifications.sanitize` | an authenticated recipient's username | **Trusted, and structurally immune** — allow-listed to `[A-Za-z0-9._-]` before pathsafe sees it | **Nothing** |

**So the relaxation applies to exactly one of the four**, and the machine agrees: the upstream
impact of the sanitising function names four chat channels — which is what "this is the remote
input path" looks like when measured rather than asserted.

- **FR-0001d** Any relaxation MUST be scoped to the **validating** read path. The sanitising
  function's behaviour MUST be unchanged for every caller on every platform. *Why this needs
  saying:* the most natural implementation — making the illegal-character set build-tag-dependent
  — changes **both**, because the validating and sanitising functions share one predicate. That
  would relax the remote-attachment path as a side effect of relaxing the operator's read path,
  which is the opposite of the intent.

#### The rule is being applied to files Omnipus does not own

This is a category error, not a trade-off. There are two populations of file and only one of
them is ours:

| | Who named it | Who owns it | Naming rules |
|---|---|---|---|
| **Mounted folders** | The operator, long before Omnipus existed | The operator | **None of ours.** Omnipus is a reader of these files. It reports what is on disk |
| **Workspace storage** (`workspaces/<id>/work/`) | Omnipus, or an agent | Omnipus | Windows-safe naming **in Windows builds only**, per the founder decision |

Revision 1 of this section framed the change as trading portability for access. **That framing
was wrong.** The portability argument does not apply to mounted content at all:

- A mount stores an **absolute host path**, realpath-resolved and **immutable** — `pkg/workspace/mount_test.go` asserts *"HostPath must NEVER change on its own — FR-8.5 forbids silent re-binding."*
- That path is meaningful only on the machine it was created on. Copying `$OMNIPUS_HOME` to another OS breaks the mount regardless of filenames, because the path does not exist there.

**So there is no Windows scenario in which a mounted file's name matters.** Refusing to open
`Meeting: notes.md` protects nothing; it just makes the operator's own documents invisible
inside a feature whose entire purpose is reading their existing documents.

What genuinely must not change, on any platform, is **containment** — traversal, root
confinement, symlink escape. Those are unrelated to whether a name is Windows-legal, and
FR-0002 keeps them unconditional.

**Independent test:****Independent test:** mount an existing folder containing `Meeting: 2026-01-01.md`, `Why?.md`
and a 106-character filename. All three list, open, index and link — and a traversal attempt
in the same folder is still refused.

**Acceptance scenarios**

1. **Given** a mounted folder containing `Meeting: notes.md`, **When** it is listed, **Then** the file appears and can be opened — on every platform.
2. **Given** the same file, **When** it is indexed, **Then** it is searchable and linkable.
3. **Given** a Windows build, **When** Omnipus **creates** a file in workspace storage, **Then** Windows-safe naming is enforced as it is today — the restriction applies to what Omnipus writes, not to what the operator already has.
4. **Given** a filename of 106 characters, **When** it is opened on macOS, **Then** it works.
5. **Given** a filename containing a double quote, **When** it is downloaded, **Then** the response headers are correctly quoted and not malformed.
6. **Given** any platform, **When** a path attempts traversal (`..`), **Then** it is refused — **traversal defence is unchanged and unconditional**.

### Requirements — Stage 0

> **Layering correction (round-3 review, verified).** An earlier draft put the owned/not-owned
> distinction inside `pathsafe`, reached through `library.CleanRelPath`. **That is unbuildable
> there.** Verified: `CleanRelPath(raw string)` is a package-level pure function with no
> receiver, and **all 12 non-test callers in `rest_library.go` call it BEFORE
> `openLibraryRoot`** — `Root.mounts` does not exist yet, so at validation time the code cannot
> know which population the path is in. A build tag is also compile-time and cannot express a
> runtime distinction; the earlier draft conflated the two axes.
>
> **The fix splits by *purpose*, not by population — which removes the need to know the
> population on the read path at all.** Name-shape rules exist to stop Omnipus *creating*
> unportable names. They have no business on the read path.

**Two validations, at two layers:**

| | Applies to | Where | Conditional? |
|---|---|---|---|
| **Addressing safety** — `..`, traversal, control characters, root confinement | Every path, read or write | `CleanRelPath`, unchanged position | **Never.** Every platform, every build |
| **Name shape** — Windows characters, reserved device names, trailing dot/space, length | Only names Omnipus **creates** | **After root resolution**, where mount context exists | Workspace storage only; skipped inside mounts |

#### The mechanism, and what CI actually executes

The split must be a **parameter, not a compile-time fork of behaviour** — otherwise half of
Stage 0 ships with zero executed assertions while CI reports green.

**Verified: no CI job runs Go tests on Windows.** All nineteen workflows run on Ubuntu or macOS.
The only Windows exposure is cross-compilation, which proves the code **compiles** and asserts
nothing about what it does. Worse, `cross-platform.yml`'s own header says Windows is *"covered by
cross-platform-extra.yml"* — **a file that has never existed in this repository**, not in the
tree and not in its history. The one place a reader would look for Windows coverage points at
nothing. Correcting that comment is part of this stage.

**Mechanism.** `pathsafe` gains a rule-set **value** with two instances, differing only in whether
the Windows-shape rules are active. Every rule function takes the set as an argument; the existing
exported functions keep their signatures and delegate to the active set, so none of the seventeen
dependent symbols changes and the critical blast radius stays at zero call-site edits. The active
set is chosen by `GOOS` in a pair of one-line files — **not** a custom build tag, which would be a
runtime footgun in platform clothing: an operator running the Linux binary with the tag set would
get Windows rules on a filesystem that never needed them, the exact behaviour Stage 0 removes.

**Consequence for testing:** because the rule set is a value, *both* verdicts are exercised on one
Linux runner by a table that passes each set explicitly. Only one fact needs a Windows machine —
that the right set is selected there.

**CI changes required, each a gate:** a narrow `windows-latest` job building `pathsafe` and
`library` and running `pathsafe`'s tests; `GOOS=windows go vet` on the Linux leg to catch the
selection file failing to compile; and correcting that workflow comment.

> **Residual, stated rather than implied:** `pkg/library`'s Windows *runtime* behaviour stays
> unexecuted — its symlink-escape tests need a privilege Windows withholds by default. The
> Windows job builds that package and does not run it.

### Requirements — Stage 0

- **FR-0001** The system MUST NOT apply name-shape rules when **reading, listing, indexing or linking** an existing file, on any platform. Those files are the operator's; Omnipus reports what is on disk.
- **FR-0001a** The system MUST apply name-shape rules only when **creating or renaming**, and MUST evaluate them **after root resolution**, so the destination's population is known. The enforcement point MUST be **one new method on the resolved root**, not a rule repeated per handler: five handlers create or rename, and the one that forgot would silently accept what the other four refuse. Exactly those five MUST call it on their **destination** path — content-put, upload, mkdir, rename, transfer. Population is decided by the mount predicate that already exists, so mount detection is stated once in the package rather than a sixth time here.
- **FR-0001b** The system MUST NOT apply name-shape rules to a create or rename **inside a mount**.
- **FR-0001c** The system **MUST** apply Windows-safe naming to what it creates in workspace storage, selected by the build target rather than a runtime flag. *`MUST`, not `MAY`:* two mandatory tests require a Windows-illegal name to be refused on create under the Windows rule set, so an implementation applying nothing would satisfy a `MAY` and fail them both. A permission no implementation can decline is a requirement written in the wrong mood.
- **FR-0002** The system MUST NOT relax path-traversal, containment or root-confinement checks on any platform, in any build.
- **FR-0002a** The system MUST separate **control-character rejection** (r <= 0x1F) from Windows-shape rejection before any split. They are fused in `pathsafe.firstIllegalRune` today. The split MUST also cover the **sanitising** paths — `replaceIllegalRunes` / `SanitizeComponent`, used at untrusted ingest (`pkg/utils/media.go`, `pkg/notifications/store.go`) — not only the validating one.
- **FR-0003** The Library download handler MUST build `Content-Disposition` to **RFC 6266**, emitting `filename*=UTF-8''<percent-encoded>` for any non-ASCII name alongside an ASCII fallback. *Rewritten because the previous requirement tested a case that already works.* Verified by running the current construction: a double quote is already escaped, and CR/LF/NUL are already escaped, so header injection is already blocked here. **The real gap is non-ASCII** — `Ünïcödé — Näme.md` is emitted as raw UTF-8 inside a quoted string, which carries no declared character set, so a client may read it as Latin-1 and save `ÃœnÃ¯cÃ¶dÃ©`. Stage 0 makes such names strictly more common. Round 2's claim that `%q` escapes non-ASCII is **false**; that is why this requirement changed rather than being dropped.
- **FR-0004** Length limits MUST be measured in the unit the thing they protect uses. Three units are in play and the code conflates them: the component cap is **100 runes**, its own rationale cites Windows `MAX_PATH` which counts **UTF-16 code units**, and every POSIX filesystem targeted caps a component at **255 bytes**. A 90-rune CJK name is 270 bytes — inside the current cap and impossible to create. On **creation**, POSIX builds MUST enforce 255 bytes and Windows builds MUST keep the rune budget; on the **read** path no length limit applies at all (FR-0001), because a name already on disk is by construction inside its own filesystem's limit.
- **FR-0002b** The system MUST reject `..` **and** `.` independently of the trailing-dot rule. Verified: `ValidateComponent("..")` currently fails *only* via `hasTrailingDotOrSpace`. `library.CleanRelPath` has its own check, but the guarantee MUST NOT depend on every caller repeating it.

### Tests — Stage 0

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 0a | `TestNameShape_CreateOnlyNotRead` | Unit | FR-0001, FR-0001a | The same name is accepted for READ and rejected for CREATE in workspace storage on a Windows build |
| 0b | `TestPathsafe_TraversalStillRefused` | Unit | AS-6, FR-0002 | **The guard that must not regress** |
| 0c | `TestLibrary_QuoteInFilenameHeaderSafe` | Integration | AS-5, FR-0003 | Header injection via filename |
| 0d | `TestLibrary_LengthRulesByUnit` | Integration | AS-4, FR-0004 | Three cases, one per unit. **(a)** 106-rune Latin basename: opens on the read path. **(b)** 93-rune / **273-byte** CJK basename: read-addressable, refused on POSIX **create** with a byte-count error. **(c)** 240-rune mounted path: read-addressable, because the whole-path cap is create-side only. Renamed — at 106 Latin characters the old case is 106 bytes and could not distinguish a correct byte rule from no byte rule |
| 0e | `TestPathsafeRegression_WindowsUnchanged` | Unit | AS-3 | The 29 existing assertions still hold under the Windows tag, for **workspace storage** |
| 0f | `TestMountedFile_NoNameShapeValidation` | Integration | FR-0001, FR-0001b | A mounted file with any OS-legal name lists, opens, indexes AND can be created — on every platform, including Windows builds |
| 0g | `TestPathsafe_ControlCharsRejectedEveryPlatform` | Unit | FR-0002a | NUL, CR, LF rejected under **every** build tag |
| 0l | `TestSanitizeComponent_UnchangedOnEveryBuild` | Unit | FR-0001d, NB-18 | The sanitising function produces byte-identical output under **both** rule sets for the remote-attachment corpus. **Catches the likely implementation** — a build-tag-dependent character set relaxes the sanitiser too, because it shares a predicate with the validator |
| 0h | `TestPathsafe_DotAndDotDotRejectedWithoutTrailingDotRule` | Unit | FR-0002b | **Guards the exact regression:** `.` and `..` must fail with the trailing-dot rule disabled. MUST run under **every** build tag — it is vacuous on Windows, where the mutated rule is still on |

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

1. **Given** an `.html` containing `document.cookie` access, **When** it is opened as a top-level browser tab at **its preview-token URL** (the only URL that serves it inline — the authenticated Library path still serves an attachment, FR-003g), **Then** the read **throws `SecurityError`** and `window.origin` is the string `"null"`. Asserting "the value is empty" is forbidden: measured, the read throws — and empty is also what a page reports when it never loaded at all.
2. **Given** the same file, **When** it is rendered inside the preview pane, **Then** both assertions hold again — **and** the same run demonstrates a **positive control**: the identical page served without the sandbox directive reads the session cookie back. Without that control, a page that failed to load produces the same verdict as one correctly contained.
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
| E-4 | Note filename containing `:` or `?` **in a mounted folder** | Addressable (STAGE 0). Only files Omnipus creates in workspace storage on Windows builds are restricted |
| E-5 | Note 200 MB in size | **Fully indexed — never capped, never truncated, never skipped.** Segmented into consecutive 8 MB index documents, each carrying the note's path and the absolute byte offset of its start; hits collapse into one result scored by its best segment. *Corrected:* an earlier revision offered "a documented body cap, or skipped and reported", which the requirement now forbids outright |
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
- **NB-17** PDF preview in this release is **read-only**. The system must not ship form filling or drawn signing — not because they fail, but because they work: the experiment proved both write correctly, which is *why* PDF.js was chosen, and shipping them is a separate decision with its own user stories. **NB-13 and NB-14 reinforce this line rather than carve out of it** — read alone they exclude only the cryptographic and agent-driven edges, which reads as endorsing the human case, a capability with no user story, no scenario, no requirement and no test.
- **NB-18** The system must not relax any name rule at a call site whose input arrives from a **remote sender**. The argument for relaxation — *"these are the operator's own files"* — is simply untrue there.
- **NB-15** The system must not include the **Librarian** — the judgement layer that proposes links, spots duplicates and flags orphans (founder decision 2026-08-22: keep it out for now). It is the one component allowed to be wrong, and it is far easier to design once the collection data is visible. No part of graph correctness may ever depend on it.
- **NB-3** The system must not build a whole-collection graph visualisation, because it is the surface that fails at scale in every comparable tool.
- **NB-4** The system must not change relative-link handling outside the knowledge-base reader, because the shared helper is consumed by chat markdown, which renders untrusted model output. **The Go/TS divergence in §2.4 is recorded, not resolved.**
- **NB-5** The system must not call a language model anywhere in the indexing, resolution or link-rewriting path, because derived data must be reproducible.
- **NB-6** The system must not write its index inside the operator's collection, because it would be synced, versioned and backed up as though it were their data.
- **NB-7** The system must not follow symbolic links out of a collection, because the indexer runs with the operator's full filesystem permissions.
- **NB-8** The system must not overwrite a file that changed since it was read, even when the change looks trivial.
- **NB-9** The system must not silently skip files it cannot address or read; every exclusion must be reportable.
- **NB-10** The system must not search across workspace boundaries, because workspace membership is the product's trust boundary.
- **NB-11** The system must not execute or interpret template content beyond a fixed documented substitution set.
- **NB-12** ~~The system must not relax `pathsafe` for mounted folders.~~ **Superseded by STAGE 0** (founder decision 2026-08-22): the rules were being applied to files Omnipus does not own. What must NOT be relaxed is **containment** — see FR-0002 and NB-16.
- **NB-16** The system must not relax control-character rejection, `..` rejection, or any containment check on any platform. `pathsafe.firstIllegalRune` currently **fuses** C0 controls (`r <= 0x1F` — NUL, CR, LF) with the Windows character set in one predicate; splitting them is mandatory, not optional. See FR-0002a.

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
- MV-13 Every response on the preview-token path carries a `Content-Security-Policy` **byte-identical to the literal string in §10.3**; an attachment response on the authenticated Library path carries none. Asserting the string is the point — a constraint that only says "a policy is present" is satisfied by `default-src *`.
- MV-14 Each supported audio extension returns its specific MIME type, never `application/octet-stream`.
- MV-15 Index directory mode 0700; index file mode 0600.

**Boot**
- MV-16 Booting with all knowledge tools registered produces zero tool-policy coverage gaps.
- MV-17 Loading a freshly seeded configuration backfills **zero** `knowledge_*` policy entries.
- MV-18 The index grace period after the last mount is revoked is exactly 7 days (FR-109a).
- MV-19 Indexing a collection of 100,000 attachments reads **zero** content bytes from them (FR-039a).
- MV-20 A preview token is refused 15 minutes after minting and accepted at 14, against a named constant rather than a literal repeated in handler and test.
- MV-21 The SPA shell response carries a Content-Security-Policy that passes the **directive floor in MV-25**. The earlier form — "a policy is present, and contains no `'unsafe-eval'`" — was satisfied by `default-src *`, which grants everything the policy exists to withhold.
- MV-22 The set of extensions served inline on the token path is exactly the documented allow-list — no more, no fewer.
- MV-23 No preview token reaches any log line **or any audit record**, asserted at **each of the six sites** rather than one. Driven: a real 429, a real CSRF rejection and a real bypass-gate 503 with token-bearing paths, asserting every captured record. The 429 case MUST first assert a 429 actually occurred — if the path is not rate-limited the capture is empty and the assertion passes having tested nothing.
- MV-24 A file whose extension is absent from the Library type table is served `application/octet-stream` **even when its bytes would sniff to something else** — the oracle is an HTML payload under an unknown extension, which must not come back as `text/html`.

---

### 10.3 The measured isolation policy — the literal string

This is one header value. It is the whole of the P0 control for stage 1, reproduced verbatim
because a nickname ("the measured shape") cannot be implemented and cannot be tested.

**Every response on the preview-token path MUST carry exactly this `Content-Security-Policy`,
byte for byte, whatever the file's type:**

```
sandbox allow-scripts; default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; media-src 'self'; frame-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'
```

**Provenance.** Recovered from `docs/internal/experiments/preview-isolation/server.py`
(`POLICIES["self"]`) and confirmed byte-identical to `server2.py`'s `active`. Both harnesses are
committed, so this string is evidence rather than recollection. It is the row recorded as `self`
in [the experiment](adr-067-preview-isolation-experiment-2026-08-22.md) §1 — zero of seven egress
vectors, opaque origin, cookie unreadable, CSS and JS working — on Chromium 151, Firefox 153 and
WebKit 26.5, twice each, top-level and embedded, with server-side request logs as ground truth.

**Why `allow-same-origin` is absent — the load-bearing omission.** Withholding it is what makes
the origin opaque, which is what made `document.cookie` and `localStorage` **throw** on all three
engines. Granting it beside `allow-scripts` hands the page the session cookie and undoes the
entire control.

**Why `allow-popups`, `allow-forms` and `allow-downloads` are absent.** Measured: with source
directives but no `sandbox`, `window.open` still reached the external origin on **every** engine.
No CSP directive covers popup navigation. The sandbox is the only thing that closes it.

**Not in the string, and never tested:** `frame-ancestors`. It would decide who may embed a
preview. Under this policy an embedded preview reads nothing and reaches nothing, so the residual
is interface deception rather than data disclosure. **Do not add it on reasoning alone** — confirm
in all three engines that the pane still loads, and record the run.

**`'unsafe-inline'` is deliberate and is not the boundary.** A previewed report normally contains
inline scripts and styles; removing it breaks ordinary documents without adding protection. What
contains the page is the opaque origin plus zero egress.

**Both mechanisms are required.** `sandbox` alone let five of seven vectors out; source directives
alone let `window.open` out. An implementation shipping either half satisfies neither FR-005 nor
FR-006 — and would pass any requirement that merely asks for "a policy".

### 10.4 The inline allow-list

Five requirements refer to "the allow-list" and until now it was never written down. This is it.
**Inline** means one thing: the preview-token path serves these extensions *without*
`Content-Disposition: attachment`, and every one of those responses carries the §10.3 policy.

| Group | Extensions | Why safe inline |
|---|---|---|
| **Active documents** | `.html`, `.htm` | Sandboxed by §10.3. The class the policy exists for |
| **Bundle code** | `.css`, `.js` | Subresources of an already-sandboxed document; they inherit its opaque origin |
| **Fonts** | `.woff2`, `.woff`, `.ttf`, `.otf` | Not executable. Needs `Access-Control-Allow-Origin` to load at all (FR-019). **None of these four is in the workspace MIME map today**, so adding them is part of this work |
| **Images** | `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.avif`, `.ico`, `.bmp` | Raster formats; not documents in any context |
| **`.svg`** | `.svg` | **On the list — see below** |
| **Media** | `.mp3`, `.m4a`, `.aac`, `.ogg`, `.opus`, `.wav`, `.flac`, `.mp4`, `.webm`, `.mov` | Not documents |
| **Inert text** | `.txt`, `.md`, `.json` | Rendered as text; no script host |
| **Everything else, `.pdf` included** | — | **Attachment** |

**`.pdf` is deliberately absent.** PDF.js fetches the bytes from the *authenticated* Library
endpoint, where `attachment` stays exactly as today — a disposition header affects navigation,
not `fetch()`. A PDF therefore never becomes a browser document at all, which is the point of
FR-018.

**Why `.svg` is on the list.** SVG **is** scriptable, and opened at its own URL it is a document
that runs its own `<script>`. It is included anyway because the token path applies **one policy
to every byte it serves** — so an SVG there gets the same containment `.html` gets: opaque
origin, zero egress. **The earlier justification is withdrawn.** It read: *"excluding it would break `<img src="logo.svg">` inside ordinary bundles."* Excluding an extension means serving it as an attachment — and that header governs **navigation, not subresource loading**, so an `<img>` renders it normally. The sentence that justified putting the one scriptable non-HTML format on this list was an unverified browser-behaviour claim pointing the opposite way. The decision no longer rests on it; it rests on containment alone.

**The middle option, evaluated and rejected.** Serving `.svg` with the correct type **and** as an attachment would close the top-level case outright with no new measurement — but it works *only if* the withdrawn claim is false. If it isn't, every bundle logo silently stops rendering, and a missing image is the kind of failure nobody files a bug about. Inline works whichever way browsers behave, and its remaining uncertainty sits on the security side, where a test settles it, rather than the rendering side, where it shows up as silent breakage.

**Three contexts, and only one was covered.** An `.svg` is reachable three ways: as a document at its token URL (test 94); as a subresource inside a sandboxed bundle, where `<img>` runs SVG in secure static mode so the script never executes (**test 122**); and inside the SPA, classified as an image and drawn in an `<img>`, fetched over the authenticated path which serves attachments (**test 123**). **All three must pass before `.svg` ships inline** — rows two and three are where a future refactor breaks the property *silently*: swapping the embed renderer to inline-SVG injection "so it scales properly" turns the reader into a script host, and nothing else notices.
Inside the SPA it never becomes a document either: it is classified as an image and drawn in an
`<img>`, which never runs an SVG's scripts, and fetched over the authenticated path, which serves
attachments. **Both URLs are closed, by different means.**

> **This is reasoned from the measured HTML result, not separately measured.** An SVG document
> under the §10.3 policy was never run. Test 94 exists to close that gap and must pass before
> `.svg` ships inline.

**Adding to this table requires a type-confusion test in the same commit** (FR-016). Test 59
reads this table as its source of truth.

### 10.5 The preview token's envelope

| Property | Rule |
|---|---|
| **Minting** | Only by an authenticated request, for a workspace and path the caller may already read. A token never widens access |
| **Scope** | One workspace, one path — a single file, or one bundle root and its descendants. Never a whole workspace |
| **Shape** | 32 random bytes from a cryptographic source, base64url — matching `pkg/agent/served_subdirs.go` |
| **Lifetime** | **15 minutes.** Long enough to load and read a bundle; short enough that a token found later in a log is already dead. 96× below the 24-hour ceiling a copy-paste implementation would inherit |
| **Renewal** | **Dropped as originally described — it was not buildable.** The SPA cannot detect that the frame's request failed: the frame is cross-origin and opaque, and `onload` fires for an error page exactly as for content. Replaced by FR-003m |
| **Shape, borrowed narrowly** | §10.5 cites `pkg/agent/served_subdirs.go` for **byte count and encoding only**. Do **not** copy its renewal branch — verified, re-registering the same directory returns the **same token string**, so the token survives as long as the tab is open, the exact property a 15-minute lifetime exists to prevent |
| **Revocation** | Expiry, **plus** logout of the minting session, mount revoke, and deletion or move of the named path |
| **Storage** | In-memory, keyed by token. A gateway restart invalidates every live preview — accepted, since a restart also drops the page holding them |
| **Route** | `POST /api/v1/library/preview-token` mints; `/library-preview/<token>/<path>` serves, **GET and HEAD only** |
| **Not reused** | **Not** `ServedSubdirs` — agent-scoped, one registration per agent, so it would evict a live `web_serve` token |

**Two URLs, deliberately different:**

| URL | Auth | Disposition | Carries §10.3? | Used by |
|---|---|---|---|---|
| `/api/v1/library/…?path=` | session cookie | **`attachment`, unchanged** | No | The SPA — PDF byte fetches, text reads, `<img>`/`<audio>`, downloads |
| `/library-preview/<token>/…` | token in the path | **inline**, allow-list only | **Yes, every response** | Sandboxed documents and their subresources |

### 10.6 How the document is embedded

**`<iframe src="<token URL>">`. Never `srcdoc`.** Three reasons, the first fatal on its own:

1. **`srcdoc` breaks FR-003** — its relative URLs resolve against the *embedder*, so no bundle
   stylesheet, script, font or media would load.
2. **`srcdoc` has no response**, so nothing carries the §10.3 policy — which is exactly what
   FR-005 requires the origin to come from.
3. It inherits its parent's context in engine-specific ways; a real URL does not.

The frame also carries `sandbox="allow-scripts"` (defence in depth if the header is ever stripped
by a proxy), `referrerpolicy="no-referrer"` (the token must not leave in a `Referer`), and an
empty `allow=""` (delegates no camera, microphone or anything added to Permissions Policy later).

> **Composition rule, and it is the opposite of intuition.** With a `sandbox` attribute *and* a
> `sandbox` directive, the effective sandbox is the **intersection** — a capability exists only
> if **both** grant it. Adding a token to one side alone grants nothing. Worth knowing before
> someone "fixes" a broken preview by editing one of them.

**Relationship to ADR-044.** Reused: the shape — a bare, path-token-authenticated prefix on the
*main* listener. ADR-044 removed the separate preview listener deliberately and this does not
bring it back. Also reused: the accepted trade that a URL-borne credential appears in logs and
history. Not reused: `ServedSubdirs`, and not the `/preview/` prefix, which keeps its meaning.

### 10.7 The SPA's own policy

The SPA is served with **no policy at all** today. Tolerable while it rendered only its own code;
not once it parses arbitrary PDFs from disk and from agents, next to the session cookie.

Starting list, on every SPA shell response:

```
default-src 'self'; script-src 'self'; worker-src 'self' blob:; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' blob:; connect-src 'self' stun: turn: turns:; frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
```

**This is a proposal, not a measurement.** Unlike §10.3, no experiment stands behind it. Its
assumptions, each with the symptom if wrong: no inline bootstrap script (white screen at boot);
`worker-src` covers however the PDF.js worker URL resolves (**see below**); Tailwind and Radix
need inline styles (broken layout); same-origin WebSocket matches `'self'` (the live connection
silently fails); Shiki needs no WebAssembly (code blocks stop highlighting); nothing embeds the
SPA (any embedding surface goes blank).

**AMENDED 2026-08-23 — `connect-src 'self'` cannot express this app's ICE servers.**

> **CORRECTION, same day.** This amendment first claimed `connect-src 'self'` *caused* the
> `E2E — ui-heavy` failure. **It did not.** Widening the directive changed nothing (run
> 32597431686 failed identically), and `idle-no-reconnect` — which holds a WebSocket open for 90
> seconds — passed under the policy throughout, so `'self'` was never blocking the socket. The
> real failure was the agent's own Chrome being refused a navigation to `/preview/<agent>/<token>/`
> with `net::ERR_NETWORK_ACCESS_DENIED`, leaving its tab on `chrome-error://` with nothing to
> capture; the "capture/encoder/ICE" frame was the downstream symptom. The timing fit perfectly and
> the mechanism was plausible, and it was still wrong — recorded here because a confident wrong
> diagnosis is exactly what this document keeps warning about.
>
> The change below **stands on its own merits**: `browserWebRTC.ts` really does configure an
> external STUN server unconditionally, so a policy permitting only `'self'` really is wrong for
> this app and would have failed the moment a client needed it. It was a latent defect, not this
> one.

The substantive point, unchanged: ICE servers are not `'self'`. Original evidence: `E2E — ui-heavy`
(`browser-live-video.spec.ts`) passed on this branch at CI run 32584338562 with **no** SPA policy,
then failed **4 of 4 attempts** at run 32595538468 once this policy shipped, with the gateway
sending `BrowserWebRTCStateFrame` reason `error` — whose contract text is *"a runtime failure
(capture/encoder/ICE)"*.

The assumption list above named the WebSocket and missed the one that actually bit: **ICE servers
are not `'self'`.** `src/lib/browserWebRTC.ts` configures `stun:stun.l.google.com:19302`
unconditionally in **both** peer-connection factories, and ADR-062 tier 3 adds gateway-minted
`turn:`/`turns:` relay URLs on top. A policy that permits only `'self'` cannot express any of them.

The fix is scheme sources — `stun: turn: turns:` — not a wildcard: ordinary HTTP and WebSocket
egress stays pinned to `'self'`, and only the ICE schemes are opened. Those schemes cannot carry a
document or a fetch, so widening them costs none of what this policy is for.

Note the failure shape, because it is the one this spec keeps warning about: the browser blocked a
client-side connection and the symptom surfaced as a **server-side** error frame. Nothing anywhere
said "CSP". That is why the headed three-engine run below is a requirement and not a formality —
and it is still outstanding: this amendment is one measurement of one directive on one engine, not
the freeze.

**Non-negotiable regardless:** **no `'unsafe-eval'`.** If a bundled library needs it, the library
is reconfigured or replaced — FR-019a is exactly that move for PDF.js.

> **FR-019b and FR-019c can silently defeat each other.** PDF.js loads its parser as a separate
> worker. If `worker-src` does not permit the URL the built worker resolves to, **PDF.js does not
> fail — it falls back to parsing on the main thread**, which is what FR-019c forbids, reached by
> satisfying FR-019b, with a console warning as the only symptom. Validate them together; test 96
> asserts the *thread*, not the configuration.

**Frozen only after** a headed run in Chromium, Firefox and Safari with zero policy violations,
while exercising initial load, the WebSocket, a Mermaid diagram, a highlighted code block, and a
PDF. Until then FR-019b's string is a proposal and this spec says so.

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
  Then reading the cookie THROWS a SecurityError (it does not return an empty string — asserting "empty" also passes when the page failed to load)
  And the document's origin is opaque
# Traces to: US-2, AS-1

Scenario: A previewed page cannot read the session cookie when embedded
  Given the same HTML file
  When it renders inside the Library preview pane
  Then reading the cookie THROWS a SecurityError (it does not return an empty string — asserting "empty" also passes when the page failed to load)
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

Scenario: The deny-backfill never supplies a knowledge tool's policy
  Given a freshly seeded installation
  When the gateway loads its configuration
  Then no knowledge tool policy is backfilled to deny
  And every knowledge tool carries its seeded posture
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
| 1 | `TestClassifyLibraryEntry_TableDiffIsExactlyIntended` | Unit | SC-013, Regression | **HIGH-risk guard.** Compares the live table against a fixture committed **before** the change; the diff must be exactly the three intended groups. Fails on an unintended fourth row **and** on a missing intended one — the previous "zero diffs" form could only ever fail |
| 2 | `TestClassifyLibraryEntry_NewKinds` | Unit | US-1 AS-1,5,6 | html / pdf / audio classification |
| 3 | `TestLibraryInline_AudioContentType` | Integration | MV-14, FR-015a | Each of the seven audio extensions **through the Library handler** — not the workspace map, which this path never reaches. A unit test on that map passes green while Library audio serves as `application/octet-stream` |
| 3a | `TestLibraryInline_TypeIsHostIndependent` | Integration | FR-015b, MV-24 | `.aac` — absent from Go's built-in table — returns `audio/aac` regardless of the machine's `/etc/mime.types`; an HTML payload under an unknown extension returns `application/octet-stream`, proving no sniff. A sniffing implementation returns `text/html` and fails |
| 3b | `TestLibraryTypeTable_MatchesInlineAllowList` | Unit | FR-015c | The type table and the §10.4 allow-list are the **same set**. Catches the round-4 omission where `.css` and `.js` were inline-allowed with no type |
| 4 | `TestInlineDisposition_AllowListOnly` | Unit | US-2 AS-5 | Non-allow-listed types stay attachments |
| 5 | `TestInlinePreview_ResponseCarriesIsolationPolicy` | Integration | US-2 AS-1 | Response headers, independent of embedder |
| 6 | `TestStripPrivateComments` | Unit | US-3 AS-1 | `%%…%%` removed |
| 7 | `TestLibraryDeepLink_RoundTrip` | Integration | US-3 AS-2,3 | Select → URL → reload → same file |
| 8 | `TestLibraryDeepLink_MissingPath` | Integration | US-3 AS-5 | Graceful not-found |
| 9 | `E2E_PreviewBundle_AllAssetsLoad` | E2E (browser) | US-1 AS-4 | **Real browser.** css + js + font + audio |
| 10 | `E2E_PreviewIsolation_TopLevelNavigation` | E2E (browser) | US-2 AS-1 | Asserts the read **throws** and `window.origin === "null"`. **Positive control required in the same run.** Fails if the sandbox directive is dropped, and fails (rather than falsely passing) if the page never loads |
| 11 | `E2E_PreviewIsolation_NetworkBlocked` | E2E (browser) | US-2 AS-3 | Egress asserted by **server-observed request arrival**, never by console text — the experiment found console wording differs per engine, so a string match silently stops matching on a new version. All seven vectors, plus a positive control |
| 12 | `E2E_PreviewIsolation_BrowserMatrix` | E2E (browser) | MV-13 | Tests 10 and 11 **and their positive controls** on Chromium, Firefox and WebKit at **`retries: 0`**. Not Safari proper — see SC-012 |
| 57 | `E2E_PdfRendersViaPdfJs` | E2E (browser) | US-1 AS-5, AC-15.4 | **Real browser, 3 engines, HEADED.** Headless has no PDF viewer and previously produced a false negative |
| 58 | `TestTypeConfusion_HtmlNamedPdfDoesNotExecute` | Integration + E2E | FR-015, AC-15.5 | **The critical control.** Served `application/pdf`, `nosniff` present, no script runs, nothing reaches an external origin. Requires a **positive control** (same payload as `text/html`) proving the detection is not blind |
| 59 | `TestInlineAllowList_EveryExtensionHasALiveConfusionCase` | Unit (table-driven) | FR-016, AC-15.7 | **Not a source scan.** The allow-list is a table; a second table maps each extension to a *data-only* case run through one shared assertion runner, plus a per-case **positive control**. Because cases carry no assertions of their own, "a test that asserts nothing" is not expressible. Catches: adding an extension with no case; supplying an inert payload; deleting the runner's execution check |
| 59a | any `scripts/check-*.sh` used instead | CI gate | FR-016 | MUST ship a `--self-test` wired in **ahead of** the real run, matching `check-no-handwritten-wire-types.sh --self-test` at `pr.yml:252-260`. A checker with no self-test is a grep wearing a gate's uniform |
| 60 | `E2E_FontAppliesWithCorsHeader` | E2E (browser) | AC-15.1, FR-019 | Real font covering the measured glyphs, on an inline element, asserted by **rendered width**. `document.fonts.status` is NOT the oracle — it reports "loaded" on failure |
| 61 | `preview-pdf.spec.ts › PDF.js loads only when a PDF is opened` | E2E (browser) | AC-15.6, FR-018 | **Runtime, two ordered phases in one session.** Phase 1: open a `.md`, assert **zero** requests match the PDF.js chunk. Phase 2: open a `.pdf` in the same session, assert the chunk **is** requested and the canvas renders — which is what stops phase 1 passing because the app never loaded. Catches converting the lazy import to a static one |
| 61a | `TestSpaEmbed_PdfJsChunkIsNamed` | Unit (build gate) | AC-15.6, FR-018 | **Re-aimed from the config file to the built artefact**, because the config branch it was written against does not exist. Asserts the SPA output holds **exactly one** named PDF.js chunk carrying a PDF.js marker, and that the entry chunk carries none. **Zero matching files is a failure, never a pass.** Must read a **freshly built** output — pointing it at a stale embed directory is a stale-artefact green |
| 64 | `E2E_BundleLoadsViaTokenPath` | E2E (browser, HEADED) | FR-003a | **Against the real authenticated gateway**, not a static server — the gap that hid this |
| 65 | `TestPreviewToken_ScopeAndExpiry` | Integration | FR-003b | Token outside its path/workspace is refused; expired token is refused |
| 66 | `TestPreviewResponse_NoReferrerAndVisibleExpiry` | Integration | FR-003c | |
| 67 | `TestPdfJs_HardeningFlagsAtCallSite` | Unit | FR-019a *(XFA and scripting options only)* | Asserts the options the call site actually passes. **It cannot detect either failure it used to be cited for:** the eval option no longer exists, so asserting it would pass forever; and a build that silently parses on the main thread carries the *same* configuration as one that does not. Those two are tests 121 and 96 |
| 67a | `E2E_HostilePdfFailsInert` | E2E (browser, HEADED) | AC-15.8 | A malformed/hostile PDF fails to render **without** executing script, navigating, or issuing a network request |
| 68 | `TestSpaServedWithCSP` | Integration | FR-019b, AC-15.9 | |
| 62 | `TestIndex_LargeNoteSegmentedNotSkipped` | Integration | FR-034a | A 200 MB note fully indexed — never skipped, never capped — and **peak resident memory stays under 128 MB above baseline**, well below the file's own size, so a whole-file read fails rather than merely being slower. Oracle: a high-water resident-memory reading, whose unit **differs between Linux and macOS** and must be normalised. Two rejected oracles: "no error" passes on a skip, and Go's heap statistics measure the wrong thing entirely |
| 101 | `TestIndex_SegmentedNoteCollapsesToOneHit` | Integration | FR-034a | A term in three separate segments of one note returns **exactly one** result, scored by its best segment, with offsets absolute within the file so the excerpt re-read lands correctly. **Catches** the naive implementation returning one hit per segment — three rows for one note, ranked as three notes |
| 63 | `TestOutline_PlainMarkdownOutsideKB` | Integration | FR-062 | An ordinary .md file gets an outline; it does NOT get search or backlinks |
| 69 | `TestExcerpt_ReReadAtQueryTimeNotStored` | Integration | FR-050a | Index a note, **edit the file on disk**, then query — the excerpt must reflect current bytes, and the stored document must carry no excerpt field. Catches caching the excerpt at index time |
| 69a | `TestExcerpt_UnavailableIsReportedNotFabricated` | Integration | FR-050a | Deleted, unreadable, and term-removed. Each returns the hit with an explicit reason. Catches returning `""` on error, and dropping the hit. "No panic" would pass all three |
| 70 | `TestAttachments_IndexedByNameNeverRead` | Integration | FR-039a, MV-19 | Read-counting filesystem wrapper: searching `diagram-v3` finds it **and** counted content reads are exactly 0. Catches skipping attachments *and* reading them — either half alone is passable by a broken implementation |
| 71 | `TestKnowledgeBase_SecondMountIsRefusedNotMerged` | Integration | FR-026 | Second root refused; a wikilink naming a note only in the second stays unresolved, proven by a read-recording fake |
| 72 | `TestHealthCheck_RunsOnScheduleAndOnlyReportsFailures` | Integration | FR-038a | Injected clock; **count runs**, not elapsed time. Healthy → zero notifications; one bad file → exactly one report. Catches downgrading to a single boot run, and reporting on healthy runs |
| 73 | `TestLifecycle_GracePeriodIsExactlySevenDays` | Integration | FR-109a, MV-18 | Boundary **pair** on an injected clock: at 7d−1m the index exists and re-mount parses zero files; at 7d+1m it is gone. A one-sided test would survive 3 days or 30 |
| 74 | `TestPdfViewer_ReadOnlyOptionsAtCallSite` | Unit | NB-17 | Asserts the options the component actually hands PDF.js — annotation editor disabled, form entry off |
| 75 | `preview-pdf.spec.ts › form fields are inert` | E2E (browser) | NB-17 | Type into the form field: nothing appears, the file is **byte-identical** on disk, and **no write request reached the gateway**. The disk-hash and server-side halves stop a UI-only fix passing |
| 80 | `E2E_PdfCjkGlyphsRender` | E2E (browser) | FR-018a | A CJK PDF renders **visible glyphs** — rendered text-layer content and non-blank pixels, never "no error". Fails the likely implementation, which omits `cmaps/` |
| 81 | `E2E_PdfNonEmbeddedFontRenders` | E2E (browser) | FR-018a | A PDF relying on a base font renders with correct metrics |
| 82 | `E2E_PdfAssetMissingIsVisible` | E2E (browser) | FR-018b | With one asset directory removed, the pane shows an error naming it. **A 200-with-index.html must not read as success** |
| 83 | `TestSpaEmbed_PdfJsAssetsPresent` | Unit (build gate) | FR-018c | The enumerated asset list is non-empty and every entry exists in the SPA output. Fails on a version bump that adds a directory |
| 84 | `TestSpaEmbed_PdfSandboxNotShipped` | Unit (build gate) | FR-019a | `pdf.sandbox*.mjs` absent from the built SPA — the scripting interpreter is not shipped, so disabling it is not a flag to flip back |
| 85 | `TestChatMarkdown_PrivateCommentsNotHidden` | Unit | FR-011 | `%%secret%%` still visible in **chat**, marker and content. The regression FR-011 creates if implemented in the only renderer that exists |
| 86 | `TestChatMarkdown_CompositionUnchanged` | Unit | FR-013d | Chat's `a` slot and plugin lists are exactly today's. Behavioural, not a source scan |
| 87 | `TestKbMarkdown_InheritsSharedRenderers` | Unit | FR-013a, FR-013b | The KB composition renders table, fence, mermaid and image identically to chat, differing **only** in the two listed places |
| 88 | `TestLibraryRow_MediaThumbnailKindsUnchanged` | Unit | SC-016 | The row's thumbnail predicate stays exactly `image` or `video` after the union widens |
| 89 | `TestLibraryPreviewPane_NoUnhandledKind` | Unit | SC-017 | Every union member mounts a surface. TypeScript cannot catch this — the pane uses `&&` chains, not an exhaustive switch |
| 90 | `TestPreviewPolicy_LiteralHeader` | Integration | MV-13, FR-005a | Byte-exact policy string on every token-path response. Negative half: the authenticated path carries none and still says `attachment` |
| 91 | `TestPreviewToken_TtlBoundary` | Integration | MV-20, FR-003d | Accepted at 14 minutes, refused at 15, against the named constant |
| 92 | `TestPreviewToken_InvalidatedOnLogout` | Integration | FR-003d | Mint, log out, use — refused. Also mount revoked, and file deleted |
| 93 | `TestPreviewPath_TokenNeverLogged` | Integration | MV-23, FR-003e | Drives a **real 429** with a capturing log handler; the record contains neither the token nor an unredacted path. Reading the code is not the test |
| 94 | `E2E_SvgWithScript_TopLevel_IsInert` | E2E (browser) | FR-008a | An `.svg` whose script beacons the cookie, opened top-level at its token URL. **Positive control required** — the same payload with no policy must execute, or the negative proves nothing |
| 95 | `E2E_PreviewFrame_SandboxComposition` | E2E (browser) | FR-005b | Frame carries the three attributes; the bundle renders; with the response header removed the attribute alone still blocks egress |
| 96 | `E2E_PdfJs_ParsesOnRealWorker` | E2E (browser) | FR-019c | Asserts a real worker was constructed and no fallback warning was emitted. Run **with the SPA policy applied** — that is the point |
| 110 | `E2E_PreviewSameOrigin_ReachableButUnauthenticated` | E2E (browser) | FR-006, FR-006a | **The column the experiment never measured.** A previewed page loads an image from a gateway path; the server asserts the request **arrived** (documenting the accepted residual) and carried **no session cookie**. Positive control: the same path from the authenticated app does carry it. **Catches** flipping the cookie's same-site mode — which turns an accepted residual into authenticated API calls from untrusted content, with no other symptom |
| 111 | `E2E_PreviewCannotFrameTheSpa` | E2E (browser) | FR-006b | A previewed page nests the real app. The shell request arrives server-side; in the browser the nested context never requests the app's entry chunk, because the policy refused to render it. Console text is **not** the oracle — engines word it differently |
| 112 | `TestPreviewToken_EntropyAndFailClosed` | Unit | FR-003h | 1,000 mints: each 32 bytes, all distinct. Then the entropy source errors and minting MUST return an error and issue **no** token. **Catches** the one a distinctness check alone misses — ignoring the error and issuing a zero-filled token |
| 113 | `TestPreviewTokenPath_ContainedAtSyscall` | Integration | FR-003i | Four refusals with a read-recording filesystem proving zero reads outside the scope: traversal, percent-encoded traversal, an absolute path, and **a symlink inside the scope pointing outside**. Positive control required — an ordinary nested file **is** served, or "404 for everything" passes. **Catches** the likely implementation, which refuses the first three and follows the symlink |
| 114 | `TestPreviewTokenPath_VerbsGetHeadOnly` | Integration | FR-003j | Table over seven methods: GET and HEAD succeed, the rest 405 with `Allow`, no body consumed. **Catches** registering on a bare prefix, which accepts every method and quietly voids the argument that no CSRF exemption is needed |
| 115 | `TestPreviewToken_RateLimitedAndCapped` | Integration | FR-003k | The path returns 429 past its window; the mint endpoint is limited; the ninth live token is refused. **Catches** omitting rate limiting — which also silently empties test 118's capture |
| 116 | `TestPreviewToken_ReMintRotatesValue` | Integration | FR-003m | Re-minting returns a **different** value and the first is refused afterwards. **Catches** exactly the copy-the-precedent mistake: renewing in place returns the previous string, which passes any "renewal works" assertion while making the lifetime meaningless |
| 117 | `TestPreviewToken_ExpiredResponseIsPolicyCarrying` | Integration | FR-003n | Expired, revoked and unknown each return 404, HTML, `nosniff`, a non-empty body and the policy byte-identically — and are **indistinguishable from each other**. **Catches** a bare not-found (blank frame) and a status split that reveals whether a token ever existed |
| 118 | `TestRequestPathRedaction_EveryLoggingSite` | Integration | FR-003e | Drives a **real** 429, CSRF rejection and bypass-gate response with token-bearing paths, capturing logs **and the audit entry**. Each assertion first asserts the status occurred, so an empty capture fails rather than passes. **Catches** fixing one site only — and specifically the raw route written into the audit record, the most durable store the product has |
| 119 | `TestSpaCsp_DirectiveFloor` | Integration + Unit | FR-019b | Two halves; the second is the point. Fetch the shell, parse the header, assert each floor item. Then feed the **same checker** a mutation table — a wide-open policy, the framing control removed, eval added, worker source dropped — and assert each is **rejected**. **Catches a checker that cannot fail**, which is what the previous test was |
| 120 | `playwrightConfig.test.ts › every spec has a project, no isolation project retries` | Unit | SC-012 | Asserts the projects exist, each has **zero retries**, each match resolves to exactly the intended files, and the default project ignores those while matching **every other** end-to-end spec on disk. **Catches the trap this fix would otherwise introduce:** adding projects makes unmatched specs stop running entirely — a shard with five specs where four match runs four and reports green, the same false-green as the vitest gap, in a different tool. **Must not live under `tests/`**, which matches the test runner's own config but none of CI's four groups |
| 121 | `TestSpaEmbed_NoEvalInPdfJsBundle` | Unit (build gate) | FR-019a, AC-15.10 | The obligation FR-019a states **first** and nothing tested: no eval path in the shipped bundle. Scans the shipped chunk and worker files; zero matches required, and **zero files scanned is a failure**. Positive control: the same scanner over a fixture containing both patterns must report both — otherwise a glob that stops matching passes silently. Catches an upgrade that reintroduces the path, precisely the case a call-site flag could never catch |
| 122 | `preview-isolation.spec.ts › an SVG subresource renders and stays inert` | E2E (3 engines) | FR-008a | The second of the three `.svg` contexts, which test 94 never reaches. A bundle whose page carries `<img src="logo.svg">`, the SVG carrying a script that beacons the cookie. Asserts the image **decoded** — not merely a 200 — and that **zero requests reached the beacon origin**, server-side. Catches dropping `.svg` from the list (the image fails to paint — the breakage the withdrawn justification predicted, now measured), and rendering the embed through `<object>`/`<iframe>`, which would run the script |
| 123 | `TestSvgInSpa_ImageNotDocument` | Unit + Integration | FR-008a | The third context. Classification is `image`, the preview mounts an `<img>`, and a knowledge-base embed renders through the image path — never `<object>`, `<iframe>` or injected markup. Server half: the authenticated route answers with an attachment, the correct type and `nosniff`. **Catches the refactor nobody would flag in review.** Gated on the CI-coverage fix, because it lands in a directory that runs nowhere today |
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
| 35 | `TestBoot_NoKnowledgeToolDenyBackfill` | Integration | FR-071, AC-17.2 | Two halves, and the second is what makes the first mean anything. **(a)** With the builtin registry **populated** — never a hand-assembled config — the repair returns **zero** `knowledge_*` pairs. **(b) Positive control:** delete one seeded entry and assert the repair returns **exactly that one**. Without (b) the test passes vacuously: coverage validation returns nothing when the tool registry is empty, and the repair derives its gap list from that same call — so a harness that never populates the registry reports green with the seeding entirely absent |
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
| 2 | `Meeting: 2026-01-01.md` | **addressable in a mounted folder** (STAGE 0). Rejected only when Omnipus *creates* it in workspace storage on a Windows build | US-0 AS-1 |
| 3 | `Why?.md` | as above | US-0 AS-1 |
| 4 | `elicify-* packages.md` | as above (present in the reference collection) | US-0 AS-1 |
| 5 | `Ünïcödé — Näme.md` | fully addressable | — |
| 5a | name containing NUL / CR / LF | **rejected on every platform** — never relaxed | FR-0002a |
| 6 | `.hidden.md` | indexed; hidden in the explorer unless shown | M-13 |
| 7 | 106-character basename | **addressable in a mounted folder** (STAGE 0); measured — 2 of 748 reference notes exceed 100 runes | US-0 AS-4 |
| 8 | `CON.md` | addressable in a mounted folder; reserved-name rule applies only to what Omnipus creates on Windows | US-0 AS-3 |

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
| 5 | html reading `document.cookie` | **throws `SecurityError`**; `window.origin === "null"`. Positive control in the same run: same page, no sandbox → reads back `omnipus_probe=SECRET` | US-2 AS-1 |
| 6 | html issuing a network request | blocked | US-2 AS-3 |
| 7 | `doc.pdf` | **PDF.js canvas plus text layer, in the pane** — non-blank pixels and a known fixture string in the text layer; **not** handed to the browser's viewer. The "native viewer" verdict was revision 2's and is withdrawn | US-1 AS-5 |
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

### 13.3a The SPA tests that run nowhere

**Verified, and this is the most consequential gate defect in the spec.** CI runs vitest in four
hardcoded groups (`pr.yml`): `src/lib/ src/store/ src/routes/ src/test/`,
`src/components/chat/`, `src/components/agents|settings|skills|shared|ui/`, and
`src/components/layout|command-center|projects/`.

**`src/components/library/` matches none of them.** Eleven existing test files in the exact
directory this feature modifies **run nowhere in CI**. One of the configured patterns points at
`src/components/command-center/`, a directory this project's own documentation records as
deleted. The checker that would catch all this exists on `main` and is **not on this branch**.

**Test 1 — the HIGH-risk release gate for `classifyLibraryEntry` — lands in that directory.** As
things stand it would be written, reviewed, reported green, and never execute once.

- **FR-085** CI MUST run the SPA tests for `src/components/library/`, and the group definitions
  MUST be verified against the tree rather than hand-maintained: a test file matching no group
  MUST fail the build. Bring across `scripts/check-vitest-coverage.mjs` from `main`.
- **FR-086** Patterns naming directories that do not exist MUST fail the build, so a stale
  pattern cannot masquerade as coverage.

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 97 | `TestVitestGroups_EveryTestFileMatchesAGroup` | CI gate | FR-085 | Enumerates `**/*.test.ts(x)` and asserts each matches exactly one configured group. **Catches** the current state — 11 library files matching none |
| 98 | `TestVitestGroups_NoStalePatterns` | CI gate | FR-086 | Every configured pattern resolves to an existing directory. **Catches** the `command-center` pattern, which points at a deleted directory |
| 99 | `TestNoUnprotectedInlineRoute` | Integration | FR-008b, FR-008c | Enumerates every handler setting `Content-Disposition: inline` and asserts each carries the §10.3 policy, an extension-derived type and `nosniff`. **Catches the round-4 finding**: `/api/v1/media/workspace/` serving HTML inline with no policy |

---

### 13.4 The browser matrix, and why retries are dangerous here

None of this exists today. Verified: `playwright.config.ts` declares **no** `projects` (Chromium
only), CI installs Chromium alone, `retries: process.env.CI ? 3 : 2` applies to every spec, and
nothing runs headed.

**Retries are the dangerous half.** "The cookie was not readable" and "nothing reached the
external origin" are not properties a fourth attempt establishes. A security assertion allowed to
retry reports identically to one that passed first time — and those three retries were added for
an unrelated reason (real-LLM latency under suite load), so nobody weighing that trade ever
considered these tests.

Required, each a named piece of work:

1. **Three projects.** `isolation-chromium`, `isolation-firefox`, `isolation-webkit`, each with
   `testMatch` limited to the two isolation spec files and **`retries: 0`** set at project level.
   Scoping is not cosmetic — an unscoped Firefox project would run all ~50 existing specs, most
   of them real-LLM, a second and third time.
2. **Install the engines** in CI and in the worker image.
3. **Assign the new specs to a shard.** `scripts/e2e-shards.sh check` already fails CI on a spec
   assigned to no shard, so a spec that would silently never run cannot merge.
4. **Headed only where §0's derivation earns it** — the top-level `.pdf` case and the
   browser-viewer control. The rest stays headless.
5. **Assert the configuration itself** — a test importing `playwright.config.ts` that asserts the
   three projects exist and each has `retries === 0`. **Catches** deleting the Firefox project or
   raising isolation retries to 1; without it the matrix silently collapses back to one
   retry-tolerant engine while every gate stays green, which is the state we are in now.

---

## 14. Functional Requirements

**Preview (stage 1)**
- **FR-001** The system MUST render HTML, PDF and audio files in the preview pane instead of their source or a download card.
- **FR-002** The system MUST show a file's source only after the reader chooses Edit.
- **FR-003** The system MUST load relative subresources of an HTML bundle (stylesheets, scripts, fonts, media).
- **FR-003a** The system MUST serve preview bytes from a **token-bearing path**, because a sandboxed document has an opaque origin and can send neither the `SameSite=Strict` session cookie nor an `Authorization` header on `<link>`/`<script>`. Verified: `/api/v1/library/` is behind `withUploadAuth`, so FR-003 is otherwise unsatisfiable.
- **FR-003b** A preview token MUST be minted only by an authenticated request, MUST be scoped to one workspace and one Library path (a file, or one bundle root and its descendants), MUST be short-lived, and MUST NOT grant access the caller does not already have.
- **FR-003c** Preview responses MUST carry `Referrer-Policy: no-referrer`, and token expiry MUST surface as a visible error rather than a blank frame.
- **FR-003d** A preview token MUST expire **15 minutes** after minting (MV-20), and MUST additionally be invalidated when the minting session logs out, when the workspace mount is revoked, and when the file or bundle root is deleted or moved. **Expiry alone is not revocation:** without logout invalidation, an administrator's token stays a valid unauthenticated read grant after they log out — the outcome FR-003b forbids, reached by omission. The token store is in-memory; a gateway restart invalidates every live preview, which is accepted because a restart also drops the page holding them.
- **FR-003e** A preview token MUST NOT reach any log **or any audit record**. Stated per site, because a universal claim with a single-instance oracle is satisfied by fixing one site. **Six** places in `pkg/gateway` record a request path; five raw. The worst is not a log at all: `gateway.go`'s reporter closure writes the raw route into an **audit entry**, which is HMAC-chained and outlives log rotation. **Why that is reachable, which is not obvious:** FR-003f argues the token path needs no CSRF exemption because it is GET/HEAD-only — correct about *blocking*, wrong about *logging*. CSRF is applied gateway-wide with prefix exemptions, and `/library-preview/` is not one, so a `POST` to a token URL reaches the CSRF gate **before** the router can 405 it, and the raw path is recorded. **The existing helper cannot be reused as-is:** `sanitisePreviewPath` takes the token as an argument (five of the six sites never hold it) and its fallback assumes the token is the **third** path segment — true of `/serve/<agent>/<token>/`, false of `/library-preview/<token>/`, where it is the second, so it would blank the wrong segment and leave the credential in place. The generalised helper MUST be **prefix-driven** and MUST fall back to a static placeholder, never the raw path.
- **FR-003f** The minting request and response MUST be defined in `contracts/` before any handler code (FR-080). Minted at `POST /api/v1/library/preview-token`; served from a bare `/library-preview/<token>/<relative-path>` prefix on the main listener, **GET and HEAD only** — which is why it needs no CSRF exemption, since that middleware gates state-changing verbs only.
- **FR-003g** The authenticated Library path MUST continue to serve `Content-Disposition: attachment` unchanged. Only the token path serves inline and only it carries the §10.3 policy. "Its Library URL", wherever this spec uses the phrase, means the **token** URL.
- **FR-003h** A preview token MUST be **32 bytes from `crypto/rand`, `base64.RawURLEncoding`**. Minting MUST **fail closed** if the entropy source errors — no token, no fallback, no shortened value. This is the entire security of an unauthenticated bearer path and existed only as a sentence in a table.
- **FR-003i** The token path MUST resolve its relative path through the **same containment chain as the authenticated path** — `library.CleanRelPath` for shape, and an `os.Root`-confined open at the **syscall** boundary — confined to the token's own scope root, not merely the workspace. **The most serious gap §10.5 left uncarried:** this is a new, unauthenticated, path-addressed file server shipping in the same release that relaxes name-shape validation, and `os.Root` appeared nowhere in this spec. A `filepath.Clean`+`Join` implementation passes every string-level traversal test and still follows a symlink out.
- **FR-003j** The token path MUST serve **GET and HEAD only**; every other method returns 405 with `Allow: GET, HEAD`, without reading a body. FR-003f uses this as the *reason* no CSRF exemption is needed, so it must be asserted — a handler on a bare prefix accepts every method by default.
- **FR-003k** The token path and the mint endpoint MUST be rate-limited, with at most **8 live tokens per session**. Two reasons: minting creates a credential in an in-memory store, so an uncapped caller is a memory-growth path; and the no-token-in-logs oracle **is** a forced 429 on this path — without rate limiting that test captures nothing and passes.
- **FR-003m** **Renewal is dropped and replaced.** It was not buildable: the frame is cross-origin, opaque and sandboxed, so the embedder can read neither status nor body, and `onload` fires identically for an error page. What ships: the SPA shows a **visible expiry notice in Omnipus chrome outside the frame**, driven by the expiry timestamp the mint already returned, plus an explicit **Reload**. Re-minting MUST return a **new** value and invalidate the previous one. No timer-driven silent reload — that discards scroll position and in-document state, a product decision nobody has made.
- **FR-003n** A request bearing an expired, revoked or unknown token MUST receive a **human-readable HTML body** — the in-frame half of "visible error rather than a blank frame" — with `404`, `text/html`, `nosniff`, and the §10.3 policy byte-identically. Expired, revoked and unknown MUST be **indistinguishable**: a `410`-vs-`404` split is a working oracle for whether a token ever existed.
- **FR-004** The system MUST execute scripts in a previewed HTML document.
- **FR-005** The system MUST bind every inline-previewed document to an opaque origin, established by the response and not by the embedder.
- **FR-005a** The system MUST serve the **literal policy string in §10.3** on every preview-token response, and MUST combine **both** mechanisms — the `sandbox` directive and the source directives. Measured on all three engines: `sandbox` alone let five of seven egress vectors out; source directives alone let `window.open` out, because no CSP directive covers popup navigation.
- **FR-005b** The system MUST embed previewed documents with `<iframe src="<token URL>">` — **never `srcdoc`**, which resolves relative URLs against the embedder and so cannot load a bundle's subresources at all (FR-003), and which has no response to carry FR-005's policy. The frame MUST also carry `sandbox="allow-scripts"`, `referrerpolicy="no-referrer"` and an empty `allow=""`. The effective sandbox is the **intersection** of attribute and header, so adding a token to only one grants nothing.
- **FR-006** The system MUST block egress from a previewed document **to any origin other than the gateway's own**, and MUST permit no `fetch`, XHR, `sendBeacon` or WebSocket to **any** origin, the gateway's included. *The earlier wording — "MUST block network egress" — overstated what was measured:* the experiment's ground truth was requests arriving at a **second** origin standing in for the internet, and the policy's `'self'` sources permit subresource loads back to the gateway.
- **FR-006a** Same-origin **subresource** loads from a previewed document remain possible, and this is **accepted** on one stated condition: they arrive **unauthenticated**. The condition holds because the document has an opaque origin, making the request cross-site, and the session cookie is `SameSite=Strict`. Residual accepted alongside: attacker-timed requests reach the gateway and land in the request log; their responses are unreadable to the page. **The condition MUST be asserted, not assumed** — one edit to the cookie's `SameSite` mode turns this into authenticated API calls from untrusted content, with no other symptom.
- **FR-006b** The SPA shell MUST be served with `frame-ancestors 'none'`. The measured policy contains `frame-src 'self'`, so a previewed page may embed any gateway page including the real SPA — the nested context reads no cookie, but renders genuine Omnipus chrome inside attacker-authored content. The control belongs on the **framed** resource; narrowing `frame-src` instead would invalidate the measurement.
- **FR-007** The system MUST display a persistent untrusted-content boundary outside any inline-rendered frame.
- **FR-008** The system MUST continue to serve non-allow-listed file types as attachments.
- **FR-008a** The inline allow-list is the table in **§10.4** and nothing else (MV-22). `.svg` is on it: the token path applies **one policy to every byte it serves**, so an SVG there has the same opaque origin and zero egress `.html` gets. **This is reasoned from the measured HTML result, not separately measured — test 94 must pass before `.svg` ships inline.**
- **FR-008b** **Every** route that serves Library-resolved bytes inline MUST carry the §10.3 policy, the extension-derived `Content-Type` and `nosniff` — not only the token path. *Found in round 4, verified:* `/api/v1/media/workspace/{workspace}/{id}` (registered `withOptionalAuth`) serves Library-resolved bytes with `Content-Disposition: inline` via `http.ServeFile` and **no policy at all**, and `pkg/library/entries.go` maps `.svg`→`image/svg+xml` and `.html`→`text/html`. An HTML file in a workspace media library is therefore served **inline, as real HTML, on the gateway origin, today** — a live exposure independent of this feature. `http.ServeFile` also sniffs, which FR-015 forbids.
- **FR-008c** The set of inline-serving routes MUST be **enumerated and asserted**, not assumed. §10.5's two-URL table was written from an incomplete enumeration and was therefore wrong; a test MUST fail if any handler sets `Content-Disposition: inline` without going through the shared policy-and-type helper.
- **FR-009** The system MUST return a specific MIME type for every supported audio extension.
- **FR-010** The system MUST NOT render Office documents.
- **FR-011** The system MUST hide `%%…%%` comments **in the knowledge-base reader only**, and MUST NOT hide them in chat. *Why the scope matters:* the only markdown renderer that exists today is the chat one, so a naive implementation changes what chat shows — and chat renders untrusted model and tool output, where silently deleting the text between two markers hides content from the reader rather than protecting them. Verified: chat renders `%%secret%%` literally today.
- **FR-012** The system MUST make the selected file addressable by URL.
- **FR-013** The system MUST NOT alter relative-link handling outside the knowledge-base reader.
- **FR-013a** The KB reader MUST render through a **second composition of the existing markdown pipeline, not a second pipeline** — accommodating a decision already recorded in the code rather than reversing it. `LibraryMarkdownPreview.tsx`'s header states the view *"reuses HistoricalMessageMarkdown VERBATIM … deliberately NOT a second markdown pipeline"*. What that forbids is duplicating the parser, plugin stack and element renderers, which drifted three times when hand-copied. The seam for a third thin composition already exists: `markdown-shared.tsx::createLinkRenderer` is a factory taking a parameter, and `historicalMarkdownComponents` is itself assembled from `commonMarkdownComponents`.
- **FR-013b** The KB composition MAY diverge from chat's in exactly two places: **(1)** the `a` slot — a KB link renderer resolving relative links, wikilinks and heading links inside the collection; **(2)** appended remark plugins for `%%…%%` stripping, callouts, highlights and frontmatter suppression. Everything else MUST be inherited unchanged. Any divergence not on this list is a defect.
- **FR-013c** The KB components map MUST be a **module-scope constant**. `historical-markdown.tsx` records why: react-markdown keys each entry by object reference, so a map rebuilt per render remounts every element on every keystroke.
- **FR-013d** The chat composition MUST be unchanged — no KB plugin in chat's plugin lists, no KB link renderer in chat's `a` slot. **There is no compiler check for this**; both are plain literals, so it is guarded by test only.
- **FR-014** The system MUST sandbox content the **browser** executes (HTML and bundles). Formats Omnipus renders itself — images, video, audio, markdown, Mermaid, code and PDF — are drawn by SPA components, never become browser documents, and therefore have no sandbox to apply.
- **FR-015** The system MUST derive `Content-Type` from the file extension, never from content sniffing, and MUST send `X-Content-Type-Options: nosniff` on every inline response.
- **FR-015a** The Library handler MUST set `Content-Type` **itself, before serving bytes**, from a table compiled into the binary, and MUST NOT delegate to `http.ServeContent`. *Why this is a requirement:* `handleLibraryDownload` calls `ServeContent` with no type set, and that function then does two things FR-015 forbids — asks the host operating system, then **sniffs the first 512 bytes**. The map this spec previously named is unreachable from the Library.
- **FR-015b** That table MUST be the only source of the type, and the handler MUST NOT consult the host MIME registry. *Consequence otherwise:* the same binary answers differently on different machines. Verified on Go 1.26 — `.aac` is not in its built-in table, so on a developer Mac it resolves from `/etc/apache2/mime.types` and in a scratch container it does not, falls through to sniffing, and serves as `application/octet-stream`, which browsers refuse to play. The test written on the Mac passes; the shipped container is broken. Same for `.ttf`, `.otf`, `.woff`, `.woff2`.
- **FR-015c** The table MUST cover **every extension in §10.4**, and at minimum: `.html`/`.htm`, **`.css`, `.js`**, the four webfont extensions, the eight image extensions including `.svg`, the seven audio extensions and the three video extensions, plus `.pdf`, `.txt`, `.md`, `.json`. *Round 4 caught the omission:* an earlier list left out `.css` and `.js` — which §10.4 requires inline — so with the octet-stream default and mandatory `nosniff` every browser would refuse a bundle's own stylesheet and script, breaking US-1 AS-4, the flagship scenario. The table and §10.4 MUST be the same set, asserted by test 3b. An extension absent from the table MUST be served `application/octet-stream` as an attachment — one stated default, no second guess, no sniff.
- **FR-016** The system MUST fail its build if an extension is added to the inline allow-list without a corresponding type-confusion test.
- **FR-017** The system MUST describe the isolation rule accurately: only content the browser executes is sandboxed. It MUST NOT imply that formats Omnipus renders itself are sandboxed, nor that HTML is not.
- **FR-018** The system MUST render PDFs with PDF.js inside the SPA, as a component alongside the existing image and video previews, and MUST load that bundle lazily rather than in the initial payload. **The bundle MUST land in a named chunk**, produced by a new branch in the build's chunking function. *Verified:* that function returns exactly four names today and `pdfjs-dist` is not yet a dependency — so the name the laziness test matches does not exist and no build step creates it. **A lazy import alone does not produce a name**; it produces a hash-named chunk, which is exactly what makes that test match nothing and pass. **Two properties here are unmeasured and MUST be measured:** that a manual chunk reachable only through the lazy import stays lazy (the bundler will hoist it into the eager graph if anything imports it statically), and that PDF.js's worker builds and runs as a **real** worker — workers go through a separate build pipeline whose default output format differs from what PDF.js ships, and getting it wrong produces no build error, just the silent main-thread fallback.
- **FR-018a** The system MUST ship PDF.js's **runtime asset directories** — `cmaps/`, `standard_fonts/`, `wasm/`, `iccs/` — and point PDF.js at them. These are fetched **per document**, not per bundle, so bundling the JavaScript does not bring them. Without `cmaps/` a Japanese, Chinese or Korean PDF renders **blank**; without `standard_fonts/` a non-embedding PDF renders with wrong metrics; without `wasm/` a scanned PDF loses its images.
- **FR-018b** A missing asset MUST fail **visibly**. *The sharp edge:* `newSPAHandler` answers any path outside the embedded tree with `index.html` and **HTTP 200**. So an un-embedded character map does not 404 — PDF.js receives an HTML page, the document renders blank, and nothing names the cause. The handler MUST return a real 404 under the PDF.js asset prefixes, and the viewer MUST surface a fetch failure as a visible error.
- **FR-018c** The asset list MUST be **enumerated from the installed package at build time**, never hand-maintained — a hand-listed set silently loses whatever the next version adds, invisible until someone opens the one PDF that needs it. The build MUST fail if the enumeration is empty or any entry is missing from the SPA output.
- **FR-018d** `pdfjs-dist` MUST be pinned with a named upgrade owner — it is a parser fed hostile input.
- **FR-019** The system MUST serve font responses with `Access-Control-Allow-Origin` so webfonts in sandboxed HTML bundles resolve; it MUST NOT rely on `document.fonts.status` as a success signal. *(The experiment measured CORS as the blocker and the header as the fix; re-assert against the real handler.)*
- **FR-019a** The system MUST ensure no `eval` path exists in the shipped PDF.js bundle, MUST disable XFA and PDF scripting, and MUST **exclude `pdf.sandbox*.mjs` from the build entirely**. *Corrected by measurement:* an earlier revision required asserting `isEvalSupported: false` at the call site. **That option no longer exists** — zero occurrences in `pdf.mjs`, `pdf.worker.mjs` or any type definition of 6.2.108, and zero occurrences of `new Function(` in the minified worker. Asserting a key PDF.js ignores would have passed forever while proving nothing: a security requirement that could not fail. The property is instead asserted against the **shipped artefact** and enforced at runtime by a CSP without `unsafe-eval` (FR-019b), so a future version reintroducing the path fails loudly. Absence of the scripting interpreter is a stronger guarantee than a flag that disables it. **"Disable PDF scripting" therefore means excluding the interpreter, NOT passing `enableScripting: false`** — that option is not part of `getDocument`'s parameters in 6.2.108 (it belongs to the viewer and annotation-layer components, which this preview does not use), so passing it would be the same could-not-fail assertion in a second place.
- **FR-019b** The system MUST serve the SPA with a Content-Security-Policy. Verified: it is served with none today, which was tolerable when the SPA rendered only its own code and is not once it parses arbitrary PDFs.
- **FR-019c** The system MUST keep PDF parsing on a worker and MUST NOT silently fall back to main-thread parsing.

**Detection and identity (stage 2)**
- **FR-020** The system MUST treat a folder as a knowledge base if its root contains `.omnipus-vault/` or `.obsidian/`.
- **FR-021** The system MUST NOT read file contents to decide detection.
- **FR-022** The system MUST write `.omnipus-vault/` when creating or initialising a knowledge base.
- **FR-023** The system MUST NOT create `.obsidian/`.
- **FR-024** The system MUST store a knowledge base's display name and template location in its marker.
- **FR-025** The system MUST create knowledge bases inside the workspace tree, not at arbitrary host paths.
- **FR-026** A knowledge base MUST be exactly one mounted folder (AW-5). The system MUST refuse a second root with a typed error naming both, and MUST NOT resolve a link, backlink or search hit across two collections.

**Index and search (stage 2)**
- **FR-030** The system MUST store the index outside the collection.
- **FR-031** The system MUST key the index by the collection root's resolved real path and reference-count it across mounts.
- **FR-032** The system MUST create index directories 0700 and index files 0600.
- **FR-033** The system MUST re-parse only files whose recorded size, modification time or content hash changed.
- **FR-034** The system MUST index in bounded-memory batches, never a single whole-collection batch.
- **FR-034a** The system MUST NOT impose a maximum note size — no note refused, skipped or truncated (AW-7). **Bounded memory comes from segmenting a note into several index documents, not from reading it in chunks.** Chunked reading bounds the read buffer only; the index's unit of work is the *document*, so a whole-note document makes peak memory a property of the single largest file. The precedent being copied has exactly that shape — `pkg/memrooms/index` indexes one document per file — so this is a deliberate deviation, not an inherited default. A note over 8 MB is indexed as consecutive segments, each carrying the note's path and the **absolute byte offset** of its start; hits from several segments MUST collapse into **one** result scored by its best segment. Link, backlink and outline extraction stream over the whole file and are never segmented.
- **FR-035** The system MUST return partial results with an incompleteness statement in the same response.
- **FR-036** The system MUST report an indeterminate state while the total is unknown.
- **FR-037** The system MUST clamp result counts above the cap and report the clamping.
- **FR-038** The system MUST provide a drift check that runs without any agent.
- **FR-038a** That check MUST run **automatically — every 6 hours by default, operator-configurable — plus once on mount**, with no button anywhere (AW-6). It MUST report only when something is wrong; a healthy run produces no notification. At most one run per collection in flight. It MUST run in-process: the gateway holds the exclusive index lock, so a separate command-line check could not open the index.
- **FR-039** The system MUST persist the index across restarts without rebuilding.
- **FR-039a** Attachments MUST be indexed by **filename and path only** (AW-2). `diagram-v3.png` is findable by name; the indexer MUST NOT open an attachment's contents for any reason.

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
- **FR-050a** The excerpt MUST be produced by **re-reading the file at query time**, never stored in the index (AW-1), so it always matches disk. Three consequences the decision left unstated: **(a)** when the re-read fails or the match has moved — deleted, unreadable, evicted — the hit is still returned with path and title plus a machine-readable reason, never a fabricated excerpt and never a silently dropped result; **(b)** the re-reads are **budgeted**, because MV-1 allows 500 ms across up to 20 results; **(c)** offsets are absolute within the file, so FR-034a's segmentation cannot misdirect the re-read.
- **FR-051** The system MUST provide link, backlink, unresolved, orphan and neighbourhood queries.
- **FR-052** The system MUST scope all retrieval to knowledge bases mounted into the calling agent's workspace.
- **FR-053** The system MUST return an empty result set, not a permission error, for out-of-scope collections.
- **FR-054** The system MUST bound neighbourhood queries by hop count and node count.
- **FR-055** The system MUST rate-limit agent retrieval.

**Reading surface (stage 2)**
- **FR-060** The system MUST render wikilinks, aliased links, heading links, path links and embeds.
- **FR-061** The system MUST render callouts and highlights, and MUST NOT render frontmatter as body content.
- **FR-062** The system MUST show an outline of headings for **any** markdown file, whether or not it belongs to a knowledge base. Search and backlinks remain knowledge-base-only, because only those require an index.
- **FR-063** The system MUST show inbound links for the open note.
- **FR-064** The system MUST collapse the reading rail to toggles when docked.
- **FR-065** The system MUST mark unresolved links visibly and MUST NOT navigate on click.

**Boot, contracts, audit (stage 2)**
- **FR-070** The system MUST enumerate every knowledge tool explicitly in the default configuration and per core agent, with no wildcards.
- **FR-071** The system MUST NOT let the deny-backfill supply any knowledge tool's policy. A load that backfills a `knowledge_*` entry is a **seeding defect** and MUST fail the test suite, not merely warn. *Corrected by measurement:* an earlier revision required migrating configurations written before this feature — there are no prior installs to migrate, and its stated fear was wrong anyway. Verified: `repairAndValidateToolPolicyCoverage` runs **repair before validate**, and the repair writes `ToolPolicyDeny` and logs a WARN, so **boot does not abort** on a coverage gap. A forgotten tool ships **silently denied**, with a log line as the only evidence — a quieter and worse failure than the one the old requirement guarded.
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
- **FR-109a** That grace period is **exactly 7 days** (AW-8), measured from the last revoke. Re-attaching inside the window reuses the index with no rebuild. The number lived only in §17, where nothing could enforce it.
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
- **SC-012** The preview isolation tests pass on all three engines in the matrix — Chromium, Firefox and **WebKit** — at `retries: 0`. **Safari proper is not covered:** Playwright's WebKit is not Safari and no macOS runner is planned. Earlier drafts promised coverage nobody is building.
- **SC-013** The preview classification table changes in **exactly the rows this feature intends and nowhere else**. The current table is committed as a fixture **before** any code change; afterwards it differs in exactly three groups — `.html`/`.htm`, `.pdf`, and the seven audio extensions. A fourth diff fails the build. *Why the wording changed:* the previous form demanded zero diffs while FR-001 and FR-018 require exactly those three to change kind, so a guard written to it fails the moment the feature lands, and one written to pass has been weakened to whatever the implementer left in.
- **SC-016** The Library row's inline-thumbnail predicate still admits exactly two kinds, `image` and `video`, after the union widens. Otherwise every audio and PDF row downloads the whole file into an `<img>` that cannot render it.
- **SC-017** Every member of `LibraryPreviewKind` mounts a preview surface. TypeScript cannot catch a gap here — the pane dispatches with `{kind === '…' && …}` chains, not an exhaustive switch, so a new kind compiles clean and renders an empty pane.
- **SC-014** Chat markdown link-handling tests remain green and unmodified.
- **SC-015** `make verify-contracts` exits zero with no drift.

---

## 16. Traceability Matrix

| Requirement | User story | BDD scenario | Test |
|---|---|---|---|
| FR-0001 | US-0 | (read path never shape-validates) | 0a, 0f |
| FR-0001a | US-0 | (shape checked after root resolution) | 0a |
| FR-0001b | US-0 | (no shape rules inside mounts) | 0f |
| FR-0001c | US-0 | (workspace naming, Windows builds) | 0e |
| FR-0001d | US-0 | (sanitiser unchanged at remote ingest) | 0l |
| FR-0002 | US-0 | Traversal still refused | 0b |
| FR-0002a | US-0 | (control chars kept on all platforms) | 0g |
| FR-0002b | US-0 | (`..` rejected independently) | 0h |
| FR-0003 | US-0 | A non-ASCII filename downloads under its own name | 0c |
| FR-0004 | US-0 | Length rules measured in the unit each limit protects | 0d |
| FR-001 | US-1 | HTML page renders / Documents and media render | 2, 9 |
| FR-002 | US-1 | Source is available behind Edit | 9 |
| FR-003 | US-1 | A complete bundle loads all of its assets | 9 |
| FR-003a | US-1 | (token-bearing preview path) | 64 |
| FR-003b | US-2 | (token scope and lifetime) | 65 |
| FR-003c | US-2 | (referrer policy, visible expiry) | 66 |
| FR-003d | US-2 | (token TTL and revocation) | 91, 92 |
| FR-003e | US-2 | (token never logged) | 93 |
| FR-003f | US-2 | (contract types and route) | 36, 65 |
| FR-003g | US-2 | (authenticated path stays an attachment) | 4, 90 |
| FR-003h | US-2 | (token entropy, encoding, fail-closed) | 112 |
| FR-003i | US-2 | (token path contained at the syscall) | 113 |
| FR-003j | US-2 | (GET and HEAD only) | 114 |
| FR-003k | US-2 | (rate-limited, live-token cap) | 115 |
| FR-003m | US-2 | (renewal rotates the value) | 116 |
| FR-003n | US-2 | (expired response carries the policy, uniform) | 117 |
| FR-004 | US-1 | Scripts in a previewed page execute | 9 |
| FR-005 | US-2 | Cannot read the session cookie (both contexts) | 5, 10, 12 |
| FR-005a | US-2 | The literal policy string, both mechanisms | 90, 12 |
| FR-005b | US-2 | Frame mechanism and sandbox composition | 95 |
| FR-006 | US-2 | Cannot reach the network | 11 |
| FR-006a | US-2 | (same-origin subresources reach the gateway, unauthenticated) | 110 |
| FR-006b | US-2 | (the SPA refuses to be framed by a preview) | 111 |
| FR-007 | US-2 | Untrusted content is visibly marked | 10 |
| FR-008 | US-2 | Types outside the allow-list are attachments | 4 |
| FR-008a | US-2 | (allow-list closed; `.svg` inert in all three contexts) | 59, 94, 122, 123 |
| FR-008b | US-2 | (every inline route carries the policy) | 99 |
| FR-008c | US-2 | (inline routes enumerated and asserted) | 99 |
| FR-085 | — | (every SPA test file matches a group) | 97 |
| FR-086 | — | (no stale group patterns) | 98 |
| FR-009 | US-1 | Documents and media render natively | 3 |
| FR-010 | US-1 | (negative — NB-2) | 1 |
| FR-011 | US-3 | Private comments do not render | 6 |
| FR-012 | US-3 | A selected file is addressable by URL | 7, 8 |
| FR-013 | — (NB-4) | (regression) | Existing chat suites |
| FR-013a | US-7 | (KB composition, not a second pipeline) | 87 |
| FR-013b | US-7 | (only two permitted divergences) | 87 |
| FR-013c | US-7 | (module-scope components map) | 87 |
| FR-013d | — | (chat composition unchanged) | 86 |
| FR-014 | US-1, US-2 | Documents and media render natively | 57 |
| FR-015 | US-2 | An HTML file named .pdf does not execute | 58 |
| FR-015a | US-1 | (Library sets the type itself) | 3 |
| FR-015b | US-1 | (host-independent type) | 3a |
| FR-015c | US-1 | (table coverage, octet-stream default) | 3, 3a |
| FR-016 | US-2 | (build gate) | 59 |
| FR-017 | US-2 | (documentation) | — doc review |
| FR-018 | US-1 | A PDF renders in the preview pane | 57, 61, 61a |
| FR-018a | US-1 | (runtime assets shipped) | 80, 81 |
| FR-018b | US-1 | (missing asset fails visibly) | 82 |
| FR-018c | US-1 | (assets enumerated at build) | 83 |
| FR-018d | US-1 | (version pinned, owner named) | 83 |
| FR-019 | US-1 | A complete bundle loads all of its assets | 60 |
| FR-019a | US-2 | (hardening asserted on the shipped artefact, not the call site) | 121, 84, 67 |
| FR-019b | US-2 | (SPA CSP) | 68 |
| FR-019c | US-1 | (the oracle is the thread, not the configuration) | 96 |
| FR-020 | US-4 | Marker presence decides status | 13 |
| FR-021 | US-4 | Detection reads no file contents | 14 |
| FR-022 | US-4 | Creating writes only the Omnipus marker | 15 |
| FR-023 | US-4 | Creating writes only the Omnipus marker | 15 |
| FR-024 | US-4 | Identity survives relocation | 16 |
| FR-025 | US-4 | (creation location) | 15 |
| FR-026 | US-4 | (one collection, one folder) | 71 |
| FR-030 | US-5 | (index location) | 22 |
| FR-031 | US-16 | Revoking one of two mounts | 24 |
| FR-032 | US-5 | (permissions) | 23 |
| FR-033 | US-5 | (incremental) | 25 |
| FR-034 | US-5 | (bounded memory) | 38 |
| FR-034a | US-5 | (no size cap, chunked) | 38, 62 |
| FR-035 | US-6 | Partial results are labelled as partial | 30 |
| FR-036 | US-6 | An unknown total is not shown as a ratio | 29 |
| FR-037 | US-8 | Counts above the cap are clamped | 28 |
| FR-038 | US-11 | Rebuilding produces identical answers | 32 |
| FR-038a | US-11 | (health check on a schedule) | 72 |
| FR-039 | US-5 | (persistence) | 25 |
| FR-039a | US-8 | (attachments by name only) | 70 |
| FR-040 | US-7 | Every wikilink form resolves | 17 |
| FR-041 | US-11 | Ambiguous basename resolves and is reported | 18 |
| FR-042 | US-7 | (unresolved) | 17 |
| FR-043 | US-10 | Links that escape never resolve | 19 |
| FR-044 | US-10 | Symbolic links are skipped | 20, 21 |
| FR-045 | US-11 | Indexing never calls a language model | 33 |
| FR-046 | US-11 | Rebuilding produces identical answers | 32 |
| FR-050 | US-8 | (ranked results) | 28 |
| FR-050a | US-8 | (excerpt re-read at query time) | 69, 69a |
| FR-051 | US-8 | Every wikilink form resolves | 17 |
| FR-052 | US-9 | Cannot search another workspace | 26 |
| FR-053 | US-9 | Cannot address another workspace | 26 |
| FR-054 | US-8 | (bounds) | 28 |
| FR-055 | US-8 | (rate limit) | 28 |
| FR-060 | US-7 | Every wikilink form resolves | 17 |
| FR-062 | US-7 | (outline for any markdown) | 63 |
| FR-061 | US-7 | Every wikilink form resolves | 17 |
| FR-062 | US-7 | Every wikilink form resolves | 17 |
| FR-063 | US-7 | Every wikilink form resolves | 17 |
| FR-064 | US-7 | Every wikilink form resolves | 17 |
| FR-065 | US-7 | Every wikilink form resolves | 17 |
| FR-070 | — | Booting produces no coverage gaps | 34 |
| FR-071 | — | Deny-backfill never supplies a knowledge policy | 35 |
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
| FR-109a | US-16 | (grace period is exactly 7 days) | 73 |
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
| AC-15.5 | 58 | AC-15.8 | 67a |
| AC-15.6 | 61 | AC-15.9 | 68 |
| AC-15.7 | 59 | AC-15.10 | 121, 84 |
|  |  | AC-17.2 | 35 |

---

## 17. Ambiguity Warnings — resolved in interview 2026-08-22

All nine were put to the founder and answered. Recorded as decisions, not assumptions.

| # | Question | **Decision** |
|---|---|---|
| **AW-1** | Excerpt source | **Re-read the file at query time.** Keeps the index small at 100k notes and the excerpt always matches disk. Costs a little query latency on files that are usually small. Closes ADR O-5 |
| **AW-2** | Are attachments indexed | **Yes — filenames only, never contents.** `diagram-v3.png` is findable by name; nothing reads inside it. Closes ADR O-6 |
| **AW-3** | How unaddressable filenames surface | **Superseded by Stage 0.** The filename rules become platform-specific, so most such files stop being unaddressable on macOS and Linux. Whatever remains is reported by the health check |
| **AW-4** | Rename link-rewriting default | **Automatic**, matching Obsidian, with the journal as the safety net. Closes ADR O-4 |
| **AW-5** | May one KB span several mounts | **No — exactly one mounted folder**, as an Obsidian vault is one folder. Links across separate collections have no meaning in the format. Closes ADR O-7 |
| **AW-6** | Where the health check runs | **Automatically, no button.** Runs on a schedule and reports only when something is wrong. Closes ADR O-8 — note this rules out a CLI check, which could not open the index while the gateway holds it |
| **AW-7** | Note size cap | **No cap.** Obsidian has none (*"There is no hard limit"* — its forum); a cap would be a restriction we invent. Memory safety comes from **reading files in chunks**, not from refusing to index them |
| **AW-8** | Index grace period after detach | **7 days**, then delete. Re-attaching within the week skips a full rebuild |
| **AW-9** | Outline for markdown outside a KB | **Yes — any `.md` file gets the heading outline and reading layout.** Only search and backlinks require a real collection, because only those need an index. The outline costs nothing extra once built, and a reader who gets it in one folder but not another reads that as a bug |

**Where each decision is enforced.** A decision recorded here and nowhere else does not get
built — five of these nine had no requirement and no test until this revision, which is the quiet
way an interview's answers evaporate between transcript and code.

| Decision | Requirement | Test |
|---|---|---|
| AW-1 excerpt re-read at query time | FR-050a | 69, 69a |
| AW-2 attachments by filename only | FR-039a, MV-19 | 70 |
| AW-3 unaddressable filenames | superseded by Stage 0; remainder reported by FR-038a | 72 |
| AW-4 automatic link rewriting | FR-103, FR-104 | 43, 45 |
| AW-5 one collection is one folder | FR-026 | 71 |
| AW-6 health check on a schedule | FR-038, FR-038a | 72 |
| AW-7 no note size cap | FR-034a | 62, 101 |
| AW-8 7-day grace period | FR-109, FR-109a, MV-18 | 52, 73 |
| AW-9 outline for any markdown | FR-062 | 63 |

**Remaining open — genuinely undecided, not deferred by omission:**

| # | Question | Why still open |
|---|---|---|
| **AW-10** | Does Adobe Acrobat display PDF.js-written form values and signatures? | Verified against macOS PDFKit and the in-tree Go reader; Acrobat untested. Blocks *promising* form filling, not rendering |
| **AW-11** | Do complex real forms (checkboxes, radio groups, inherited appearances) round-trip? | The tested fixture was one text field |
| **AW-12** | PDF page-count or size threshold before PDF.js becomes slow | Unmeasured |

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
