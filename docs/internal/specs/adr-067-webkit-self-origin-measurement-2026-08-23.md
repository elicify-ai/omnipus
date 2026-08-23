# WebKit and CSP `'self'` under an attribute-sandboxed iframe — measurement, 2026-08-23

Companion to `adr-067-preview-isolation-experiment-2026-08-22.md`. That experiment measured the
§10.3 policy with the **header alone**. This one measures the **header + iframe attribute
composition**, which is what ADR-067 FR-005b actually ships — and which had never been measured.

## Verdict

**WebKit is wrong; Chromium and Firefox are spec-correct. It is a confirmed upstream bug, already
fixed on WebKit `main`, not yet in any shipping Safari.**

- CSP3 §2.2.2 ("Parse a response's Content Security Policies") sets a policy's self-origin to
  **the response URL's origin** — explicitly so `'self'` keeps working for documents that have an
  opaque origin. The document's own (opaque) origin is never consulted.
- WebKit instead derives the self-source from the document's `SecurityOrigin`
  (`Source/WebCore/page/csp/ContentSecurityPolicy.cpp::updateSourceSelf`). With the iframe
  *attribute*, the origin is already opaque when the CSP object is built, so the self-source has
  no scheme or host and matches nothing.
- With the *header* `sandbox` directive alone, the CSP object is constructed **before** the
  directive flips the origin, so `'self'` still captures the URL origin. That is why the
  2026-08-22 header-only experiment was green and this defect stayed hidden.

Upstream: WebKit bug **316847** "CSP 'self' does not match in opaque-origin http(s) documents"
(rdar://178638597), fixed by **315247@main** (`01c89d15c3f8`, 2026-06-15), which implements exactly
the CSP3 §2.2.2 behaviour and upstreams WPT tests `content-security-policy/sandbox/iframe-self-*`.
The regression arrived via **314912@main** (bug 308756), which landed on a Safari release branch,
which is how it reached shipping Safari 26.5.x before the fix existed.

## Measured

Rig: the byte-identical `libraryIsolationPolicy` string, the exact fixture bytes from
`tests/e2e/fixtures/preview-isolation/bundle/`, the exact `embedPreview` attribute set, served on a
token-shaped path with the policy on every response, against a recording second origin. Ground
truth is server-side arrival. Non-vacuity per cell: the fixture's own `attempted=12` list, proving
the inline script ran. A no-policy control per engine confirmed the egress oracle sees all seven.

Engines actually run: chromium 149.0.7827.55, firefox 151.0, webkit 26.5 (Playwright 1.61.1),
**and real Safari 26.5.2**.

| engine | policy | mode | js | css | subresources reached server | egress of 7 |
|---|---|---|---|---|---|---|
| chromium / firefox | shipped | all modes | yes | yes | yes | 0 |
| webkit | shipped | top-level | yes | yes | yes | 0 |
| **webkit** | **shipped** | **embed + attribute** | **NO** | **NO** | **NONE** | 0 |
| webkit | shipped | embed − attribute | yes | yes | yes | 0 |
| all three | **explicit origin** | all modes | yes | yes | yes | **0** |
| all three | `'self'` **+** explicit origin | embed + attribute | yes | yes | yes | **0** |
| all three | none (control) | embed − attribute | — | — | yes | **all 7** |

Three findings that matter beyond the table:

1. **The failure is pre-network and it is CSP.** In the broken cell the server never sees the
   requests, and a `securitypolicyviolation` listener captured enforce-mode violations naming
   `script-src-elem` and `style-src-elem` with the subresource URLs as `blockedURI`.
2. **The trigger is the ATTRIBUTE ALONE, not the composition.** A sources-only header (§10.3 minus
   the `sandbox` directive) plus the attribute still fails. Attribute with no CSP at all loads
   everything (and leaks 5 of 7). So the header's `sandbox` directive is innocent: *any* policy
   using `'self'` fails in *any* attribute-sandboxed WebKit iframe.
3. **Real Safari 26.5.2 reproduces it.** Not a Playwright-build artefact — end users on current
   Safari would see unstyled, inert previews today.

## Consequence for ADR-067

Option (c) — keep both mechanisms, name the gateway origin explicitly — **works, and is measured
on all four engine builds including real Safari, with all seven egress vectors still at zero.**

It is also permanently spec-sound rather than merely a workaround: CSP3 §6.7.2 host-source
matching compares the **request URL** to the source expression and never consults the document
origin, so it is immune to this entire class of bug by construction. Once the WebKit fix reaches
shipping Safari the explicit origin becomes redundant but never harmful.

**`'self'` must not be reintroduced as the sole source while any affected Safari is supported.**

Implementation caveats:
- The origin must be the one **the browser sees** — derive from `CanonicalGatewayOrigin` /
  `gateway.public_url`, which is why the 2026-08-22 experiment's §2.1 rejected explicit origins
  (hostname hardcoding). Option (c) accepts that cost knowingly.
- A mis-set `public_url` degrades to today's symptom (unstyled preview, containment intact), never
  to a security failure.
- §10.3 / MV-13's "byte-identical literal" contract becomes a config-derived string, so the spec
  text and the string-equality oracles must be amended **together**.

## Shipped-design verification (three engines, three rigs, one binary)

The table above measured the *mechanism*. This section measures the *shipped* design — `'self'`
**kept**, plus the gateway origin and its loopback aliases added — through the real product
(`libraryIsolationPolicy`, real token mint, real E2E suites), on a binary built from this tree.

Binary: single build, newer than every source file under `pkg/`, `src/`, `tests/e2e/` (no source
touched after it). All three rigs ran against that one binary. Engines: chromium 149.0.7827.55,
firefox 151.0, webkit 26.5 (Playwright 1.61.1).

Live `Content-Security-Policy`, captured off the wire per rig:

- **Default rig** — `gateway.host=127.0.0.1`, port 6791, no `public_url`; browser on
  `http://localhost:6791` (a *different* name for the same host — this is what the alias set
  carries):
  `sandbox allow-scripts; default-src 'none'; script-src 'self' http://127.0.0.1:6791 http://localhost:6791 http://[::1]:6791 'unsafe-inline'; style-src 'self' …; img-src 'self' … data: blob:; font-src 'self' …; media-src 'self' …; frame-src 'self' …; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'`
- **Wildcard rig** — `host=0.0.0.0`, no `public_url`: no origin is derivable, so the policy
  collapses to the pre-amendment string, byte-for-byte:
  `sandbox allow-scripts; default-src 'none'; script-src 'self' 'unsafe-inline'; …`
- **Bad-`public_url` rig** — `host=127.0.0.1` with `public_url=https://preview.example.com`: the
  policy names that origin, which is not the one the browser is on.

Results (`preview-isolation.spec.ts`, 13 tests; `preview-svg.spec.ts`, 16 tests):

| rig | webkit | chromium | firefox |
|---|---|---|---|
| default (isolation) | 13/13 | 13/13 | 13/13 |
| default (svg) | 16/16 | 16/16 | 16/16 |
| wildcard, no origin derivable | 11/13 — 11c + 11d RED | 12/13 — 11d RED | 12/13 — 11d RED |
| bad `public_url` | 11/13 — 11c + 11d RED | 12/13 — 11d RED | 12/13 — 11d RED |

Both negative rigs produce the **same shape**, and it is the predicted one: 11c (FR-004, the bundle
runs its own script and applies its own stylesheet) fails on WebKit only — the exact single-engine
blindness this whole measurement is about — while 11d (FR-005b, the composed observation, which
checks the served policy names the browsing origin) fails on all three. Chromium and Firefox render
the misconfigured preview correctly, so without 11d the misconfiguration is invisible on two engines
out of three.

The failure text is operator-actionable rather than an assertion dump: it names the origin the
browser is actually on, states the consequence ("will NOT load in Safari/WebKit … invisible on two
engines out of three"), prints the served policy, and lists the three likely causes in order
(same host under a different name → open the derived origin or set `public_url`; reverse proxy →
set `public_url`; wildcard bind → no origin derivable, set `public_url`). The wildcard rig also logs
the matching WARN at boot: *"library preview: no canonical gateway origin — set
gateway.public_url, or previews will render without their stylesheets and scripts in Safari (other
browsers are unaffected)"*.

Negative control (from the gate run, worth repeating): a policy naming `http://127.0.0.1:9999` —
a real origin that is simply not this one — blocked the bundle's script *and* stylesheet on all
three engines in both modes. The explicit source therefore does real matching; it does not silently
widen to "any loopback".
