# Preview isolation — browser experiment results

- **Date:** 2026-08-22
- **Answers:** ADR-067 D15's mandated verification (AC-15.4), and review findings C-01, C-02, m-09
- **Engines:** Chromium 151.0.7922.34 · Firefox 153.0 · WebKit 26.5 — headless for §1–§5; **§6A was run HEADED on Chromium only**, which changed one conclusion (see §6A.1)
- **Method:** five candidate policies × two load modes (top-level navigation, embedded iframe) × three engines, each swept twice
- **Harness:** `docs/internal/experiments/preview-isolation/` (committed; see §6) — a page loading an external stylesheet, external script, webfont, audio and a PDF, which also attempts egress to a **second origin** by seven vectors (image, fetch, beacon, WebSocket, iframe, form, popup)

> **Ground truth is server-side.** Both origins log every request they receive. What arrives was allowed; what never arrives was blocked. The in-page JavaScript report is corroborating only — a page can misreport what happened to it; a server log cannot.

---

## 1. Result

**24 of 25 compared rows agree across all three engines.** The single disagreement is the no-policy control's egress list, which is racy: with no policy the form-submit probe navigates the document away mid-load, so the exact subset of vectors that fire before teardown varies. All three agents reported this independently and unprompted.

| policy | egress vectors reaching the second origin | origin opaque | cookie readable | CSS | JS |
|---|---|---|---|---|---|
| **`self`** | **none** | yes | **no — throws** | ✅ | ✅ |
| **`origin`** | **none** | yes | **no — throws** | ✅ | ✅ |
| `nosandbox` | `/x/popup` | no | **YES — `omnipus_probe=SECRET`** | ✅ | ✅ |
| `sandboxonly` | beacon, fetch, img, iframe, ws (5 of 7) | yes | no — throws | ✅ | ✅ |
| `none` (control) | all 7 | — | — | ✅ | ✅ |

Identical in top-level and embedded mode, on every engine. The control fired all seven vectors on all three engines, so the harness measures what it claims to.

---

## 2. The question D15 could not answer, answered

### 2.1 `'self'` DOES match under an opaque origin — all three engines

ADR-067 D15 carried this warning:

> ⚠️ *"under an opaque origin, CSP `'self'` does not match the serving origin. A policy combining `sandbox` with `'self'` source expressions may therefore block the very subresources it intends to allow."*

**That is false.** With `sandbox allow-scripts` (no `allow-same-origin`), every engine reported `window.origin === "null"` and `origin_opaque: true`, and *simultaneously* loaded and executed the **external** script and **external** stylesheet gated by `script-src 'self'` / `style-src 'self'`.

`'unsafe-inline'` cannot explain this — it does not authorise external `src`/`href`. And the directives were provably live: in the same runs, the cross-origin image, fetch, beacon, WebSocket and iframe were each refused with violations naming those exact directives.

The reason is that `'self'` resolves against the policy's **self-origin** — the URL the protected resource was served from — not the document's (opaque) origin.

**Consequences:**
- **The A14 fallback is not needed.** Serving previews from a distinct origin — which would have reintroduced the second listener ADR-044 deliberately removed — is off the table.
- **`'self'` is preferred over naming the origin explicitly.** The two policies were indistinguishable on every measured field, on every engine. An explicit origin additionally hardcodes a hostname that would break behind a reverse proxy — directly relevant given `gateway.public_url`.

### 2.2 Isolation is complete under `sandbox` + source directives

Zero of seven egress vectors escaped under `self` or `origin`, on any engine. `document.cookie` did not return empty — it **threw `SecurityError`**, as did `localStorage`. The cookie test is meaningful rather than vacuous: the same page under `nosandbox` read back `omnipus_probe=SECRET`.

### 2.3 Review finding C-02 confirmed — and it needs both mechanisms

C-02 argued that `connect-src 'none'` leaves other exfiltration routes open. Correct, and sharper than stated:

- Under `nosandbox` — `default-src 'none'`, `form-action 'none'`, `frame-src 'self'` — **`window.open` still reached the second origin** on all three engines. No CSP directive covers popup navigation; `navigate-to` was dropped from the specification.
- Only the `sandbox` directive stopped it, by omitting `allow-popups`. Same for form submission.
- Conversely, `sandboxonly` sealed the origin but let **five of seven** vectors out.

**Neither mechanism is sufficient alone. The shipped policy must combine `sandbox` (for popups, forms, downloads, origin) with source directives (for fetch, img, frame, media, font, connect).**

---

## 3. Two findings that change ADR-067 D15

### 3.1 PDF does not render under `sandbox` — all three engines

**This breaks D15's PDF row and m-09's `<iframe src>` mechanism.**

The PDF is served (HTTP 200, correct content type) and then fails to display:

| engine | observed |
|---|---|
| Chromium | frame commits `chrome-error://chromewebdata/`. Frame-tree probe confirms it. Renders **normally** under `nosandbox` via the built-in viewer |
| Firefox | `NS_ERROR_ABORT` — *"Download of doc.pdf was blocked because the triggering iframe has the sandbox flag set"* |
| WebKit | `Frame load interrupted`, frame commits no document. Also logs *"Not allowed to download due to sandboxing"* |

Chromium and Firefox both render the PDF correctly **without** the sandbox directive, which isolates the cause: **sandbox breaks the browser's PDF viewer**, because viewers route through a download/plugin path that sandbox blocks without `allow-downloads`.

> **WebKit caveat, stated rather than smoothed:** headless WebKit failed to render the PDF **even in the no-policy control**, so its result is confounded by headless mode and cannot distinguish "sandbox broke it" from "headless WebKit never renders PDFs". The Chromium and Firefox evidence is what establishes the cause. Whether Safari renders it headful is **unverified**.

**Options for the spec round** (none yet chosen):
1. **Per-type policy** — HTML gets `sandbox`; PDF gets a sandbox-free but otherwise strict policy. Justifiable because a PDF is not arbitrary scriptable content and the browser's PDF viewer is itself a sandbox — but it means the isolation guarantee differs by file type, which must be stated plainly rather than implied.
2. **Add `allow-downloads` to the sandbox** for PDF responses. Needs testing; may simply turn a failed render into a download prompt.
3. **Drop inline PDF**, keep the download card. Honest, and loses the row D15 called "perfect fidelity, no rendering code".

### 3.2 Webfonts fail under an opaque origin — and the fixture cannot settle it

Chromium and Firefox both observed the font **request succeed (200) and then be rejected**:

> `Access to font at 'http://127.0.0.1:8810/f/probe.woff2' from origin 'null' has been blocked by CORS policy: No 'Access-Control-Allow-Origin' header is present`

Fonts are fetched in CORS mode. Under an opaque origin the page is cross-origin **to its own server**, so its own font is refused. `font-src 'self'` is satisfied and irrelevant — a second, unrelated mechanism rejects it.

**Likely fix:** emit `Access-Control-Allow-Origin` on font responses (without credentials). **Not verified** — it was not tested.

> **This finding is not settled, and must not be recorded as if it were.** The fixture font is a 68-byte stub, not a valid font, so it could never render on any engine. WebKit reported `fonts_status: "loaded"`, but Chromium and Firefox both demonstrated that field returns `"loaded"` even when the font was CORS-blocked or rejected by the font sanitiser — **it is not a success oracle**. A re-test with a real font file and the `Access-Control-Allow-Origin` header is required before any claim about webfonts is made.

---

## 4. Operational consequences not previously recorded

1. **A sandboxed preview cannot call back to Omnipus.** `connect-src 'none'` blocks even same-origin fetch; and even without it, a request from `origin: null` is a CORS request the gateway would refuse. Fine for a static report; it would break any preview that expects to talk to the API.
2. **Cookies stop being sent to the page's own subresources.** Firefox logged *"Cookie 'omnipus_probe' has been rejected because it is in a cross-site context"* for the stylesheet, script, PDF and audio. Any authenticated same-origin subresource loses its cookie under this policy.
3. **`document.cookie` throws rather than returning empty.** Any page doing an unguarded read hard-errors. Worth knowing before blaming Omnipus for a broken preview.
4. **Violation message wording differs per engine** (*"violates the following Content Security Policy directive"* / *"The page's settings blocked…"* / *"Refused to load… because it does not appear in…"*). Any test matching on message text will be engine-specific and brittle. Assert on **server-observed request arrival**, not on console strings.

---

## 5. What this settles, and what it does not

**Settled (three engines, twice each, server-side ground truth):**
- `'self'` works under an opaque origin — C-01's blocking unknown is resolved.
- The A14 distinct-origin fallback is unnecessary.
- `'self'` is preferred to an explicit origin (equal behaviour, no hardcoded hostname).
- `sandbox` + source directives together achieve zero egress across seven vectors with the session cookie sealed.
- Neither mechanism alone is sufficient — C-02 confirmed.
- CSS, JavaScript and audio all work correctly under the proposed policy.

**Not settled:**
- **Webfonts** — needs a valid font and an `Access-Control-Allow-Origin` re-test (§3.2).
- **PDF** — fails under sandbox on all three engines; the remedy is an open design choice (§3.1).
- **Safari headful** — only headless WebKit was measured, and its PDF result is confounded.
- **The exact shipped directive string** — the winning *shape* is established; the final list must be fixed against the real handler and re-verified.

---

## 6. Reproducing

```
cd docs/internal/experiments/preview-isolation
python3 server.py <MAIN_PORT> <EXT_PORT>     # experiments 1
python3 server2.py <MAIN_PORT> <EXT_PORT>    # experiment 6A
# then drive http://127.0.0.1:<MAIN_PORT>/p/<policy>/ in a browser
# GET /__hits for the server's request log, /__reset between runs
```

Policies: `self`, `origin`, `nosandbox`, `sandboxonly`, `none`. The `none` control **must** show all seven vectors reaching the second origin; if it does not, the harness is broken and no other row means anything.

**Known harness defect:** with no policy, the form-submit probe navigates the document away before the in-page report runs, so the `none` row has no report and a racy egress list. This affects the control only. It does not affect any policy that blocks form submission — i.e. every policy under consideration.

---

## 6A. Second experiment — per-format isolation, the font fix, and type confusion

Run after §1–§5, on the same day, with a **second harness**
(`docs/internal/experiments/preview-isolation/server2.py` + `fixture2/`). Chromium only —
the Firefox and WebKit runs were **stopped before completing** when the design moved to
PDF.js and their results became moot. **Chromium 151.0.7922.34, run HEADED**, each case twice,
byte-identical across runs.

> **Single-engine. Do not generalise these three results to Firefox or Safari.**

### 6A.1 The headless trap — this invalidated an earlier conclusion

**Headless Chromium has no PDF viewer.** Every PDF — including the no-policy control —
became a download: `page.goto` threw `Download is starting`, the page stayed at
`about:blank`. That is a build artifact, not a security control.

**§3.1's conclusion was therefore partly wrong.** Measured headed:

| Case | Result |
|---|---|
| PDF **top-level**, under `sandbox` | **Renders.** The sandbox directive does not apply to a top-level PDF navigation — the embedder reported `origin: "http://127.0.0.1:8910"`, not `null` |
| PDF **top-level**, no policy | Renders |
| PDF **in an iframe**, under `sandbox` | **Blocked** — `chrome-error://chromewebdata/`, `net::ERR_BLOCKED_BY_CLIENT` |
| PDF **in an iframe**, no policy | Renders |

So sandbox breaks the PDF *plugin in a subframe*, not a top-level PDF. The Library case is
the framed one, so §3.1's practical conclusion survives — but its stated reasoning ("sandbox
breaks the PDF viewer") was too broad. **Any future PDF test MUST run headed.**

### 6A.2 The webfont CORS fix — measured, and the fixture oracle was broken

**§3.2 said this was unsettled. It is now settled for Chromium.**

First, the honest part: **the fixture's own oracle reported failure under every policy,
including the no-CSP control**, for two independent reasons — the font (Noto Sans Lycian) has
no glyph for the characters being measured, and the measured element was block-level so its
width was font-independent. The fixture's `font_actually_applied` field carries no
information and must not be used.

A working oracle was built instead: the font's **space advance** (260/1000 em ≈ 10.4px at
40px) versus a monospace fallback (~24px), measured on an inline element.

| Policy | font without `Access-Control-Allow-Origin` | with the header |
|---|---|---|
| no CSP | applied (104.0px) | applied (104.0px) |
| `passive` (no sandbox) | applied (104.0px) | applied (104.0px) |
| `active` (sandbox) | **NOT applied** (240.82px = fallback) | **applied** (104.0px) |

Corroborated by console (`Access to font … from origin 'null' has been blocked by CORS
policy`), by network (`net::ERR_FAILED` without the header, 200 with it), and by
`FontFace.status` (`"error"` vs `"loaded"`).

**CORS is definitively the blocker and the header is definitively the fix — on Chromium.**

> `document.fonts.status` reported `"loaded"` while two of three faces were in
> `status: "error"`. **Confirmed liar. Never use it as a success oracle.**

### 6A.3 Type confusion — blocked, and not by CSP

An HTML document named `evil.pdf`, containing a script that sets the title and beacons the
cookie to the external origin, served as `application/pdf` with `nosniff`:

| Case | Title | HTML rendered | External hits |
|---|---|---|---|
| under `passive` | `""` | none | **none** |
| under `active` | `""` | none | **none** |
| **under `none` — no CSP at all** | `""` | none | **none** |

Chromium routed the bytes to its PDF engine, which rejected them: *"Failed to load PDF
document."* **Content-type dispatch did the work; CSP contributed nothing.**

**Positive control** (run twice, identical): the same payload served as `text/html` in a
same-origin iframe on an un-CSP'd page executed fully — title `EVIL_EXECUTED`, the heading
rendered, and `/x/evil-fetch` and `/x/evil-beacon` both reached the external origin. So the
negatives above are real, not blind instrumentation.

### 6A.4 Harness defects recorded rather than hidden

- `active-cors` **did not exercise the fix as shipped**: the server adds the header only on
  `?c=1`, and `index.html` requests the font without it. The winning column above came from an
  injected `@font-face`, not the fixture.
- The `none` control has no in-page report in either harness: with no policy the form-submit
  probe navigates the document away before the report runs.

---

## 7. Addendum — PDF.js form filling and signature (measured 2026-08-22)

Tested because the proposal to render PDFs in the SPA with **PDF.js** (rather than the
browser's built-in viewer) hinges on whether it can also *edit*. Third-party sources —
all vendors of competing paid libraries — claim PDF.js annotations *"may not save
properly into the PDF binary for compatibility with other PDF readers."*

**That claim is refuted for both cases tested.**

Method: `pdfjs-dist` 6.2.108; a hand-built AcroForm PDF (828 bytes, one text field),
validated first by the in-tree Go reader `ledongthuc/pdf`. Values set through
`annotationStorage` — the same path the viewer uses — then `saveDocument()`. Output
inspected byte-wise, then **rendered by macOS Quartz/PDFKit**, an engine with no
relationship to PDF.js.

### 7.1 Form filling — works, and writes correctly

Saved output is a **proper incremental update** (two `%%EOF`), containing both halves:

- **The value**: object 5 re-emitted as `/V (Daniel Piatkowski)`
- **The appearance stream**: a new object — `/Tx BMC q BT /Helv 12 Tf 0 g … (Daniel Piatkowski) Tj ET Q EMC`

The appearance stream is what makes other readers *display* the value rather than
merely store it. Both are present; the xref and `/Prev` trailer are well-formed.

**Independently rendered by macOS: the filled name appears in the field.**

### 7.2 Drawn signature (ink annotation) — works, and writes correctly

Saved output contains:

- Page object re-emitted with `/Annots [5 0 R 7 0 R]` — the annotation is registered
- `/Subtype /Ink` with `/InkList [[60 120 75 145 …]]` — the stroke, semantically
- An appearance stream with real operators — `2 w 1 J 1 j / 0 G / 60 120 m / 75 145 l … S`

Again both halves: a reader that understands ink annotations gets the structure, and one
that doesn't still draws the stroke.

**Independently rendered by macOS: the signature stroke appears.**

### 7.3 What this does and does not establish

**Established by measurement:** PDF.js fills AcroForm fields and adds ink annotations,
saves both correctly into the PDF binary as standards-compliant incremental updates
with appearance streams, and a wholly independent engine renders both.

**NOT established, and must not be claimed:**
- **XFA forms** — unsupported by PDF.js. Untested here.
- **Cryptographic signatures** — PDF.js has no PKI signing. A drawn signature is an
  image of intent, not a verifiable one. Separate project, separate ADR.
- **Programmatic filling by an agent** — the storage API worked from Node here, but the
  supported product surface is a human filling fields in the viewer. An agent-driven
  fill is not a documented capability.
- **Adobe Acrobat specifically** — macOS PDFKit and the in-tree Go reader were the
  independent checks. Acrobat was not tested.
- **Complex real-world forms** — the fixture was a single text field, hand-built. Radio
  groups, checkboxes, appearance-inheriting fields and pre-existing appearance streams
  are untested.

### 7.4 Consequence for D15

Rendering PDFs via PDF.js **inside the SPA** — as a component, like the existing
`LibraryImagePreview` and `LibraryVideoPreview` — means a PDF never becomes a browser
document with an origin at all. It is bytes we parse and draw.

That **removes the need for D15.1's per-format isolation split**: the "passive" class
existed only for PDF, since images, audio and video are already plain elements in the
SPA. A uniform `sandbox` policy for the one thing that needs it (HTML) becomes possible
again — a stricter and simpler posture than the split.

**Also corrected by measurement, and important:** the earlier finding that PDFs fail
under sandbox on all engines was **partly a headless artifact**. Headless Chromium has
no PDF viewer at all, so every PDF became a download regardless of policy. Run headed,
a **top-level** PDF renders even under `sandbox` (the directive does not apply to a
top-level PDF navigation), while a PDF **in a frame** is blocked. The Library case is
the framed one, so the conclusion stands — but the reasoning behind it was wrong, and
any future test of this MUST run headed.
