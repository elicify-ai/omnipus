# ADR-094: One isolation model for every served preview surface (issue #798)

- **Status:** Proposed
- **Date:** 2026-09-26
- **Author:** architect (gateway-security squad)
- **Deciders:** pending — founder on Q1 and Q2 below; team-lead grill round owed before Accepted
- **Evidence level:** highest used — 1 (user-input: issue #798 acceptance; founder-held decisions FR-3 and the degraded-mode residual) + 3 (documented pattern: the Library isolation policy and its measured defects) + 5 (expert reasoning grounded in code read this session)

**Builds on:** "ADR-067 — Omnipus knowledge base and render-first preview" §10.3 and its dated amendments (2026-08-23, 2026-09-09, 2026-09-14); "ADR-044 — Serve /preview/ on the main gateway listener (path approach)"; the email spec's MC-10/MC-37 (`docs/internal/specs/email-mail-view-spec.md` on branch `feature/email-mail`, security sign-off absorption 2026-09-26).

**Supersedes-in-part, pending founder Q1:** the FR-3 residual-risk assessment in "ADR-044 — Serve /preview/ on the main gateway listener (path approach)" (the previewed app's ability to log in from inside the preview tab).

---

## 1. Context

### 1.1 The finding (issue #798)

Agent-generated HTML served as a preview can run script that calls the gateway's authenticated `/api/v1/*` endpoints as the logged-in user, including state-changing requests. Every step of that chain is present in today's code:

| # | Chain step | Evidence (read this session) |
|---|---|---|
| 1 | One origin: `/preview/` is registered bare on the main gateway listener; there is no separate preview origin | `pkg/gateway/rest.go::registerPreviewEndpoints`; `pkg/gateway/rest_preview.go::HandlePreview` |
| 2 | The web_serve preview CSP admits the whole gateway: bare `'self'` in `script-src`/`style-src`/`img-src`/`font-src`, `connect-src 'self'`, `form-action 'self'`, and **no `sandbox` directive at all** | `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` |
| 3 | Inline script executes (`script-src 'unsafe-inline'`) | `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` |
| 4 | The session cookie is gateway-wide and rides same-site requests: `Path=/; HttpOnly; SameSite=Strict` — SameSite blocks cross-site sends, not same-site ones | `pkg/gateway/middleware/session_cookie.go` (cookie attributes at issuance) |
| 5 | The CSRF cookie is script-readable: `__Host-csrf` is `HttpOnly:false` (`__Host-` mandates `Path=/`), and `/api/v1/*` state-changing requests require it echoed in `X-Csrf-Token` | `pkg/gateway/middleware/csrf.go` (header contract and cookie attributes) |
| 6 | The API accepts the session cookie, so those requests authenticate as the user | `pkg/gateway/middleware/session_cookie.go::RequireSessionCookieOrBearer` (issue #798's "withAuth" maps here — no `withAuth` symbol exists in the tree) |
| 7 | The user's only action is opening a preview their own agent produced: the SPA opens previews as a top-level tab, `target="_blank"` / `window.open` | `src/components/chat/IframePreview.tsx` (link-out design, US-9/FR-016) |

What limits it today: `connect-src 'self'` blocks fetch to third-party origins, so direct exfiltration is blocked — the CSP is an exfiltration blocker, not an impersonation blocker. The script can read the API and perform state-changing actions as the user within the origin. Prompt injection is the realistic trigger: agents read untrusted content, and the user's only action is clicking a link their own agent produced.

### 1.2 Three surfaces, one bug class

Omnipus serves untrusted browser-rendered content from its own origin in three places:

| Surface | Prefix | Serves | Today's isolation |
|---|---|---|---|
| Library preview | `/library-preview/{token}/…` | knowledge-base notes, rendered bundles | **The reference model** — path-confined CSP sources, `sandbox` directive without `allow-same-origin`, `connect-src 'none'`, no-redirect tripwire (`pkg/gateway/library_isolation_policy.go`; built per "ADR-067 — Omnipus knowledge base and render-first preview" §10.3) |
| Mail HTML preview | `/mail-preview/…` | sanitized email HTML | **Spec'd to the same model** — dedicated non-API prefix, path-confined sources, `sandbox` mirroring the iframe attribute, `connect-src/form-action/base-uri 'none'`, own named builder (MC-10, MC-37, T62 in `docs/internal/specs/email-mail-view-spec.md`, branch `feature/email-mail`) |
| web_serve preview | `/preview/{agent}/{token}/…` | agent-built static exports and proxied dev servers | **The laggard** — a wide same-origin CSP with no sandbox, the exact surface issue #798 is about |

The Library policy is the product's proof that this class is solvable **without** a second origin. Its two measured defects are the reason the model looks the way it does:

- **DEFECT 1 (2026-08-23):** under WebKit, `'self'` stops matching once a `sandbox` attribute is layered on (upstream bug 316847; fixed at 315247@main, not in any shipping Safari). Fix: never rely on `'self'` — name origins explicitly; CSP3 host-sources match the **request URL**, immune to document-origin opacity.
- **DEFECT 2 (2026-09-14):** WebKit attaches the `SameSite=Strict` session cookie to a framed preview's subresource requests while labelling them `Sec-Fetch-Site: cross-site` — SameSite cannot help because site-for-cookies is computed from the top-level page, and the top-level page IS Omnipus. Fix: path-confine every source to the preview prefix and remove `'self'`; a source whose path ends in `/` matches only URLs under that path (CSP3 §6.7.2.10), so the authenticated API is not reachable from a preview at all.

The no-redirect companion invariant: CSP3 §6.7.2.9 ignores a source's path after a redirect, so confinement is exactly as strong as "nothing under the prefix ever answers with a redirect". The Library enforces it with `pkg/gateway/library_preview_no_redirect_test.go` — hostile paths, an `/api/` sentinel, and a positive control proving the stdlib mux WOULD have redirected (so the harness can see the failure).

### 1.3 Why this is an ADR and why now

"ADR-044 — Serve /preview/ on the main gateway listener (path approach)" deliberately consolidated the preview onto the main listener and consciously accepted a same-origin residual. That residual is now a filed, P1-labelled security finding (issue #798, `security`/`priority:P1-high`/`area:gateway`). The mail surface has already spec'd its half of the convergence (MC-10); the Library surface implements it. Converging web_serve completes the model before the email feature lands and multiplies the number of untrusted-HTML surfaces.

---

## 2. Decision

**Converge all three surfaces on the one isolation model the Library preview already ships** (per "ADR-067 — Omnipus knowledge base and render-first preview" §10.3, as amended 2026-09-14), applied to `/preview/` with two web_serve-specific adaptations. Until the founder answers Q1, this ADR is Proposed, not Accepted.

### 2.1 The model — one model, ten clauses

1. **Dedicated non-API prefix.** Previews serve outside `/api/v1`, token-in-path, registered bare (no session middleware). All three surfaces already satisfy this (`/library-preview/`, `/mail-preview/`, `/preview/`).
2. **Path-confined sources only.** Every gateway source in the six source directives is `<canonical-origin><preview-path>` — never a bare origin, never `'self'` while an origin is known. The canonical origin is the boot-frozen `middleware.CanonicalGatewayOrigin(cfg)` (`pkg/gateway/middleware/origin_canonical.go::CanonicalGatewayOrigin`) — the same input the Library policy, CORS and the WebSocket origin check already share.
3. **`sandbox` directive, never `allow-same-origin`.** The document's origin is opaque: `document.cookie` and `localStorage`/`sessionStorage` throw, so neither the session cookie nor the CSRF token can be read by preview script — the impersonation chain dies at its credential step, independent of network controls. The opaque origin is also what makes the token-in-path URL safe to reuse in future iframe embeds (an opaque-origin frame cannot reach `window.parent`).
4. **`connect-src` confined, never `'self'`.** web_serve-specific: confined **to the token prefix** (see 2.2), not `'none'` — this preserves FR-007c's documented reason for `'self'` (hydrated SPA exports fetching their own `/data.json`, per `pkg/gateway/rest_workspace.go::buildWorkspaceCSP`'s comment) while excluding `/api/v1` on every engine.
5. **`form-action` confined to the token prefix** (the current `'self'` exists for the previewed app's own form posts, per `pkg/gateway/rest_workspace.go::buildWorkspaceCSP`); `base-uri 'none'`; `object-src 'none'`; `frame-ancestors` unchanged (origin-level, main origin).
6. **No-redirect invariant + tripwire.** Nothing under `/preview/` ever answers 3xx or writes a `Location` header, enforced by a tripwire mirroring `pkg/gateway/library_preview_no_redirect_test.go`: hostile paths, an `/api/` sentinel, and a stdlib-mux positive control proving the harness can see a redirect.
7. **Per-surface named builder.** The web_serve policy is built in its own file and never calls, imports or copies the Library builder — the mail spec's MC-37 pattern (`docs/internal/specs/email-mail-view-spec.md`), whose rationale holds: two surfaces sharing a builder drift together, and a dropped directive has no visible symptom. Per-surface tripwires assert the built string: no bare `'self'` in any source directive, every host source confined to the preview path, `sandbox` present without `allow-same-origin`, `connect-src` confined, `base-uri`/`object-src` `'none'`.
8. **Loopback aliases, IPv6 honesty, byte-stable order.** Canonical origin first, then the two CSP-expressible loopback aliases (`127.0.0.1`, `localhost`) when the canonical host is loopback; no IPv6 literal ever emitted (CSP3 §2.3.1 has no IPv6 syntax — measured per the 2026-09-09 amendment to "ADR-067 — Omnipus knowledge base and render-first preview"). Fixed order, byte-stable output.
9. **Degraded mode degrades loudly, never silently.** With no usable canonical origin (wildcard bind, no `gateway.public_url`), sources fall back to `'self'` exactly as the Library's `pkg/gateway/library_isolation_policy.go::freezeLibraryIsolationPolicy` does, **but `connect-src 'none'` is kept even degraded** — it is the one directive that needs no origin to confine. The residual in that shape is narrower than today's (fetch-based reads and all state-changing calls die; only state-blind subresource GETs remain) and is announced by a one-shot boot WARN naming the fix (`gateway.public_url` / a concrete `gateway.host`).
10. **Font CORS companion (opaque-origin reachability).** Under the opaque origin, a previewed bundle's own webfonts are CORS-fetched from a `null` origin; browsers refuse `ACAO:*` on credentialed requests but these are uncredentialed under the opaque origin. Emit `Access-Control-Allow-Origin: *` on webfont responses under the prefix — the Library already does exactly this (`pkg/gateway/rest_library_preview.go`, FR-019 webfont case, `libraryPreviewNeedsCORS`).

### 2.2 The web_serve adaptations

- **Per-token confinement, tighter than the Library's.** The Library confines to its whole prefix (`/library-preview/`); web_serve CAN confine to `/preview/{agent}/{token}/` because the CSP is built per response with the token at hand (`serveStaticFile` and `proxyDevRequest` both hold it — `pkg/gateway/rest_preview.go`). Two previews on one origin then cannot even load each other's bundles.
- **The dev proxy must uphold clause 6 itself.** The Library serves files it writes; web_serve reverse-proxies an upstream that answers redirects (e.g. a previewed app redirecting to its own root-relative `/login`). CSP3 §6.7.2.9 discards a source's path after a redirect, so one upstream 302 whose Location escapes the prefix reopens the hole. `pkg/gateway/rest_preview.go::proxyDevRequest`'s `ModifyResponse` must therefore rewrite root-relative upstream Locations under the preview prefix — or strip/neutralize them — before they reach the browser. This rule has no Library analogue and is the one genuinely new enforcement point.

`script-src 'unsafe-inline'` stays. Unlike mail (sanitized HTML, `script-src 'none'`) and Library notes (no scripts), a web_serve preview's whole purpose is running the agent's static export — script is the point, so the sandbox + confinement are the boundary, exactly as in the Library policy ("'unsafe-inline' is deliberate and is not the boundary", `pkg/gateway/library_isolation_policy.go` header).

### 2.3 What does not change

- **The SPA link-out UX (US-9/FR-016).** Previews still open top-level via `src/components/chat/IframePreview.tsx`; a CSP `sandbox` **directive** applies to top-level documents, so isolation does not depend on framing. No SPA change is required.
- **ADR-044's consolidation.** The listener consolidation, deleted config keys, and `gateway.preview_enabled` hot-flip are untouched.
- **The built-in browser panel** (agent review path) — a CDP screencast process, no SPA session, no CSP consumer.

---

## 3. Alternatives considered — rejected

### Option A — separate origin/port for previews

Genuinely cross-origin from the API: no ambient credentials, cookies are per-origin by construction, `document.cookie` inside the preview holds only the previewed app's own login. It is the strongest shape on paper — and it was **already adjudicated**: "ADR-044 — Serve /preview/ on the main gateway listener (path approach)" chose the path approach because the hosting model (desktop, Docker, single-public-port pods) cannot expose a second port; the separate listener, its mux and the `preview_port`/`preview_host`/`preview_origin`/`preview_listener_enabled` keys were **deleted entirely**, with the hot-flip machinery replaced by the live `gateway.preview_enabled` check (`pkg/gateway/preview_listener_hot_flip_test.go` documents the replacement).

Revisiting it means re-litigating a settled founder decision to buy an isolation level the sandbox+confinement model already delivers measurably (Library: seven egress vectors measured contained, three engines, `pkg/gateway/library_isolation_policy.go` header). The alias problem (DEFECT 1/2, `127.0.0.1` vs `localhost` vs IPv6) resurfaces at the new origin's layer regardless. Rejected: re-opens ADR-044 for no isolation gain over the recommended model, at real deployment cost. **If the founder wants this origin model anyway, that is a new decision superseding ADR-044, not a variant of this ADR.**

### Option C — cookie scoping (`Path`/`SameSite`)

Measured against the issue's own chain, scoping is a near no-op:

| Scoping change | Effect on the attack |
|---|---|
| Session cookie `Path=/` → `/api/v1` | The script targets `/api/v1/*` directly, where the cookie must be sent by design — nothing changes. It only stops the cookie riding `/preview/` subresource GETs, and `/preview/` is token-only and ignores the cookie (`pkg/gateway/rest_preview.go::HandlePreview`, FR-023) |
| Session cookie `SameSite` → Lax/… | Already `Strict` (`pkg/gateway/middleware/session_cookie.go`); Strict is the strongest SameSite, and SameSite governs cross-site only — the preview is same-site by construction |
| CSRF cookie `HttpOnly:true` | Breaks the SPA (it must read the cookie to echo `X-Csrf-Token`, per `pkg/gateway/middleware/csrf.go`), and the SPA and the preview share the origin, so anything readable to one is readable to the other until origins are separated |

The one real cookie finding — WebKit attaching SameSite=Strict cookies on framed same-site requests (DEFECT 2) — is a framing-geometry fact, not a cookie-attribute fact; no attribute fixes it. Rejected as a primary control; it buys nothing the sandbox+confinement model does not already deliver. Optional defense-in-depth (session cookie `Path=/api/v1, /auth`) may ride along with the implementing PR if qa-lead's RED shows any residual subresource-cookie flow, but it gates nothing.

---

## 4. Consequences

### Positive

1. The issue #798 acceptance criterion becomes testable and passes: script in a served preview cannot perform an authenticated state-changing `/api/v1/*` request (opaque origin kills credential reads; confined `connect-src` kills the call itself; confined `form-action` kills form POSTs; the no-redirect invariant kills pivot via redirect).
2. All three untrusted-HTML surfaces share one reviewable model — the convergence the mail spec's MC-10 already mandates for itself — so a future fourth surface has a precedent to copy instead of a debate to re-open.
3. The per-token confinement is **stronger** than the Library's prefix-wide form: two coexisting previews cannot load each other's bundles.
4. `connect-src` confined to the token prefix **keeps** FR-007c's hydrated-export reachability (`/data.json`) that the Library's flat `'none'` cannot express.
5. Degraded mode is strictly safer than today's degraded mode: `connect-src 'none'` survives even where no origin can be named.

### Negative

1. **Previewed-app login inside the preview tab breaks** (founder Q1). Under the opaque origin, cookie- and storage-backed logins cannot persist — `document.cookie` and storage throw. ADR-044's FR-3/D6 chose the unsandboxed top-level tab *for* that capability; this ADR supersedes it. Login demonstrations move to the agent's built-in browser panel (ADR-038, no SPA session) or a normal browser tab outside Omnipus; the URL stays copyable.
2. **Root-absolute-asset dev servers were already broken and stay broken** (no new regression, but no rescue either): an HTML document referencing `/assets/x.js` or `/@vite/client` root-absolute resolves those requests against the gateway origin root, outside `/preview/` — blocked under confinement, MIME-mismatched today (the SPA catch-all answers unknown paths with `index.html`, `pkg/gateway/embed.go` spaHandler). Only apps whose assets ride under the token prefix work, before and after.
3. **The degraded-mode residual remains a residual** (founder Q2): on a wildcard bind with no `gateway.public_url`, sources fall back to unconfined `'self'` — subresource GETs against the whole gateway still ride the session cookie (state-blind; fetch-based reads are dead via `connect-src 'none'`), exactly the Library's documented residual plus its WARN.
4. `frame-ancestors`-asserting tests and any test pinning the old CSP string must be updated (test-only churn, no behavior).

### Neutral

1. `script-src 'unsafe-inline'` remains in the policy — unchanged exposure, unchanged boundary statement ("not the boundary").
2. The SPA link-out UI is untouched; the preview URL shape is untouched; tokens, TTLs and revocation are untouched.
3. IPv6-literal readers (`[::1]`) keep today's Library-identical limitation: unnamed by the policy, degraded rendering, WARNed.

---

## 5. Blast radius (of implementing this ADR)

| Area | Files | Change |
|---|---|---|
| CSP construction | `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` (retired for preview responses), **new** `pkg/gateway/webserve_isolation_policy.go` (name per backend-lead) | The six source directives, `sandbox`, `connect-src`, `form-action`, `base-uri`, `object-src`, per-token confinement, alias emission, freeze + degraded WARN |
| Serving paths | `pkg/gateway/rest_preview.go::serveStaticFile`, `::proxyDevRequest` | Call the new builder with token context; add the upstream-Location rewrite/strip rule |
| Font CORS | `pkg/gateway/rest_preview.go` (static + proxy responses) | `ACAO:*` on webfont responses (Library FR-019 pattern, `pkg/gateway/rest_library_preview.go`) |
| Tripwire | **new** `pkg/gateway/preview_no_redirect_test.go` | Mirror `pkg/gateway/library_preview_no_redirect_test.go` with `/api/` sentinel + stdlib-mux positive control |
| Existing tests | `pkg/gateway/preview_iframe_test.go` (frame-ancestors assertions), `rest_preview_test.go`, `rest_preview_proof_test.go`, `rest_preview_web_serve_e2e_test.go` | CSP-string assertion updates; e2e gains the issue's acceptance case |
| SPA | none required | Optional follow-up only: re-consider an iframe embed now that opaque-origin framing is safe (not in this ADR's scope) |
| Contracts | none | No wire-format change: CSP headers are not contract-typed; no `openapi.yaml`/`asyncapi.yaml` delta |

## 6. Reachability (Definition-of-Done)

- A real user opens a web_serve preview from chat (`src/components/chat/IframePreview.tsx` link → new tab): the static export renders with its stylesheet, script and images loaded from its own token prefix, on the default loopback install and with `gateway.public_url` set.
- A dev-server preview whose assets live under the token prefix renders through the proxy, including the upstream-redirect case (rewritten Location stays in-prefix).
- The agent's built-in browser panel still demonstrates the preview (screencast path consumes no CSP).
- The issue's acceptance test executes in CI, not merely exists.

## 7. Platform / browser behaviour

| Engine | Behaviour under the recommended model | Grounding |
|---|---|---|
| Chromium / Firefox | `sandbox` directive applies to the top-level document; path-confined sources load the bundle's own assets; `connect-src` violations refused pre-flight | Library measured all three engines loading bundle assets under path confinement, 2026-09-14 (`pkg/gateway/library_isolation_policy.go` header) |
| WebKit / Safari | Same containment. DEFECT 1 is moot (no `'self'` is relied on; host-sources match the request URL). DEFECT 2 is moot for the directive-only, top-level shape, and moot generally because containment never depended on cookie behaviour | "ADR-067 — Omnipus knowledge base and render-first preview" amendments 2026-08-23 and 2026-09-14 |
| Any engine, wildcard-bind Docker, no `public_url` | Degraded `'self'` sources + `connect-src 'none'` + WARN — rendering works, residual is the state-blind GET surface | `pkg/gateway/library_isolation_policy.go::freezeLibraryIsolationPolicy` degradation posture, tightened per clause 9 |
| IPv6-literal access (`[::1]:port`) | Degraded rendering (assets unnamed by the policy), documented, non-security | 2026-09-09 HP-2 amendment; `libraryIsolationHostIsCSPExpressible` |

## 8. Test strategy

- **RED (qa-lead, browser-level, proves both hole and instrument):** a Playwright case against the current code asserting the issue's attack works — script in a served preview reads `document.cookie` (CSRF token), `fetch('/api/v1/…', {method:'POST'})` succeeds with the echoed CSRF header, a state-changing request lands. It must go red → proves the hole, proves the instrument.
- **GREEN (backend-lead):** unit tests on the new builder — one negative assertion per clause of §2.1 (no bare `'self'`, every source token-confined, `sandbox` without `allow-same-origin`, confined `connect-src`/`form-action`, `'none'` pairs, degraded-mode shape + WARN); the no-redirect tripwire with sentinel + positive control; the proxy Location-rewrite cases (in-prefix rewritten, cross-prefix stripped, upstream 302 followed only in-prefix); the font-CORS case; updated frame-ancestors assertions.
- **Browser GREEN (the issue's acceptance):** the RED case flips — cookie read throws, the POST fetch is refused with a `connect-src` violation before leaving the page, a manual form POST is refused by `form-action`, the bundle still renders. Minimum engines: Chromium + WebKit (the known-divergent engine); Firefox where the CI grid carries it.
- **CHECK (qa-lead, mutation):** drop the `sandbox` directive → browser test must fail; re-add `'self'` beside confined sources → builder tripwire must fail; let the proxy pass a cross-prefix Location through → tripwire must fail.
- **Regression sweep:** `rest_preview_web_serve_e2e_test.go`, the e2e web-serve spec, and any vitest touching preview links.

## 9. Questions for the founder

**Q1 — Supersede ADR-044's FR-3 (previewed-app login from inside the preview tab)?**
Context: the sandbox directive (no `allow-same-origin`) makes cookie/storage-backed login inside the preview tab impossible — and ADR-044 chose the unsandboxed top-level tab partly *for* that capability (FR-3/D6). This is the one genuine feature regression in the recommended model.
Impact: with Q1 = yes, in-tab preview logins stop working (login demos move to the agent's built-in browser panel or an outside browser tab); with Q1 = no, the impersonation hole stays open and the only remaining shapes are Option A (a second origin — re-opens ADR-044's hosting-model decision) or accepting issue #798.
Options: **(A) Supersede FR-3 — isolation outranks in-tab login; URL stays copyable (recommended)**; (B) keep FR-3 and re-open ADR-044 for a second preview origin; (C) keep FR-3 and accept issue #798 as wontfix.
Answer: *(pending)*

**Q2 — Accept the degraded-mode residual on wildcard-bind deployments with no `gateway.public_url`?**
Context: with no canonical origin the sources must fall back to unconfined `'self'` (CSP has no path-only source), so on such deployments subresource GETs from a preview still ride the session cookie across the whole gateway — state-blind only, because `connect-src 'none'` survives even degraded. The Library accepted exactly this shape with a boot WARN.
Impact: (A) keeps previews working on Docker/LAN deployments least able to set `public_url`, with a named residual; (B) refuses to serve previews there (fail-closed), removing the feature for exactly those deployments; (C) adds a middle fallback (sources `'self'`, `connect-src 'none'`, plus a per-response `sandbox`-only policy) — note (A) already ships (C)'s connect-src half.
Options: **(A) Accept with the one-shot boot WARN, Library-consistent (recommended)**; (B) fail closed — previews 404 in that deployment shape; (C) accept and additionally document the residual in user-facing docs.
Answer: *(pending)*

---

## 10. Affected components

`pkg/gateway/rest_workspace.go`, `pkg/gateway/rest_preview.go`, new `pkg/gateway/webserve_isolation_policy.go` (name per backend-lead), new no-redirect tripwire test; test updates in `pkg/gateway/preview_iframe_test.go`, `rest_preview_test.go`, `rest_preview_proof_test.go`, `rest_preview_web_serve_e2e_test.go`; no SPA changes; no contract changes. Converges with `pkg/gateway/library_isolation_policy.go` (reference implementation) and the MC-10/MC-37 posture of `docs/internal/specs/email-mail-view-spec.md` (branch `feature/email-mail`).
