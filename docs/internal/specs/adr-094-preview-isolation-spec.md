# ADR-094 — Preview isolation specification (issue #798)

- **Decision record:** [ADR-094 — Preview isolation with full capability — two serving modes](../architecture/ADR-094-preview-isolation-model.md) (Status: Accepted, 2026-09-26)
- **Founder decisions:** [ADR-094-founder-decisions.md](../architecture/ADR-094-founder-decisions.md) — F794-1 … F794-7 (authoritative; this spec invents no new acceptance criteria — every criterion below derives from the ADR)
- **Issue:** #798 — Preview pages can call the authenticated API as the logged-in user (same-origin residual) (`security` / `priority:P1-high` / `area:gateway`)
- **Inputs:** ADR-094 §2 (two serving modes), §5 (blast radius), §6 (reachability), §8 (test strategy), §11 (affected components); ADR-094 review (2026-09-26) for what was already checked and rejected
- **Status:** Draft (plan-spec output; for grill-spec round 1)
- **Date:** 2026-09-26

## Summary

Today a served preview runs same-origin script with the user's full gateway credentials (issue #798). ADR-094 rebuilds web_serve preview serving in two modes: **Mode 1**, a self-generated per-preview subdomain of the gateway host (`<label>.localhost:<port>`) served by Host dispatch inside the single binary — the session and CSRF cookies are host-only, so the preview origin holds no gateway credential and issue #798 dies structurally, with no CSP doing the work; and **Mode 2**, the unchanged `/preview/` path with full capability (no `sandbox` directive anywhere) and a server-side control stack — CSP source confinement plus navigation/service-worker guards, prefix-wide `Access-Control-Allow-Origin: *`, and one normative redirect rule. Mode selection happens at web_serve mint time from the canonical gateway origin; misconfigured origins fail closed (F794-6). Library and mail previews keep the ADR-067 sandbox model unchanged.

## Existing codebase context

*GitNexus MCP tools are not connected in this headless spec-writing session. Per the shared rules, no impact run is claimed; the impact table is **Inferred** (from ADR-094 §5/§11, whose citations were verified by the architect this session, plus the grep sweep below).* Verified this session by grep in this checkout (`b023a7a59`):

| ADR-cited symbol | Verified present | Role |
|---|---|---|
| `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` | ✅ | Today's wide preview CSP (`'self'`, `unsafe-inline`, no `sandbox`) — retired for preview responses by this feature |
| `pkg/gateway/rest_workspace.go::resolveMainOrigin` | ✅ | Third origin computation — retired for preview responses by this feature |
| `pkg/gateway/rest_workspace.go::workspaceContentType` | ✅ (map var, rest_workspace.go:85) | MIME map gaining `.mjs`/`.woff`/`.woff2`/`.wasm` |
| `pkg/gateway/library_isolation_policy.go::libraryIsolationOrigins` | ✅ | Origin-list derivation, renamed `previewIsolationOrigins` and shared across all three surfaces |
| `pkg/tools/web_serve.go::previewOriginUnresolvedResult` | ✅ | Existing fail-closed path for an empty canonical origin — extended to all F794-6 refusal triggers |
| `pkg/tools/web_serve.go::DevServerRegistry` | ✅ | Dev-server registration store extended with Mode 1 labels |
| `pkg/gateway/rest_preview.go::addPreviewCORSHeaders` / `::handleServePreviewPreflight` | ✅ | CORS emission extended to prefix-wide `ACAO: *` |
| `pkg/gateway/rest_preview.go::proxyDevRequest` | ✅ | Gains the normative `Location`-rewrite rule in its response-modification step |
| `pkg/gateway/rest_preview.go::neutralizeReservedSetCookies` | ✅ | Existing reserved-cookie neutralization — preserved unchanged |
| `pkg/gateway/preview_token.go::Mint` | ✅ (method) | 43-char base64url tokens stay the Mode 2 credential |
| `pkg/gateway/middleware/session_cookie.go::WriteSessionCookie` | ✅ (no `Domain` attribute set) | Host-only session cookie — Mode 1's load-bearing control |
| `pkg/gateway/middleware/csrf.go::CSRFCookieName` | ✅ (`__Host-csrf`) | Host-only-by-prefix CSRF cookie under TLS |
| `src/components/chat/IframePreview.tsx` | ✅ (4 `noopener` occurrences) | Opener severed on every preview link — preserved unchanged |
| `pkg/gateway/gateway.go` / `gateway_boot.go` | ✅ | Host-dispatch wiring point |

### Symbols involved

| Symbol | Role | Change |
|---|---|---|
| `pkg/tools/web_serve.go::Execute` | mints preview URLs from the canonical origin | mode selection from ADR-094 §2.4; dual URLs when Mode 1 applies; `previewOriginUnresolvedResult` extended to wildcard-host `public_url` and unparseable-origin refusals |
| `pkg/tools/web_serve.go::Description` | agent-facing tool text | **owned by prometheus-prompt-engineer**: both modes, Mode 2 asset rule, console-check guidance |
| new `pkg/gateway` host-dispatch file (name per backend-lead) | preview-Host mux | label-grammar validation, 404-everything-non-preview, reuses the preview registry lookup |
| new `pkg/gateway/webserve_isolation_policy.go` (name per backend-lead) | Mode 2 CSP builder + tripwires | literal template of ADR-094 §2.3, boot-frozen origin list, percent-encoded prefix |
| `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` / `::resolveMainOrigin` | today's preview CSP and third origin computation | retired for preview responses (remaining callers untouched — ADR-094 §5) |
| `pkg/gateway/library_isolation_policy.go::libraryIsolationOrigins` | measured origin-list logic | renamed `previewIsolationOrigins`, shared; per-surface policy templates stay separate |
| `pkg/gateway/rest_preview.go` CORS pair | reflects main-origin only today | emits `ACAO: *` under the prefix; deletes upstream `ACAO`/`ACAC` before overriding on proxied responses |
| new Fetch-Metadata / Service-Worker middleware guard | — | rejects `Sec-Fetch-Dest: document` on `/api/v1`; refuses `Service-Worker: script` under `/preview/` |
| `pkg/gateway/rest_preview.go::proxyDevRequest` | dev-server proxy | normative 4-step `Location` rule; upstream `ACAO`/`ACAC` deletion in `ModifyResponse` |
| `pkg/gateway/gateway.go` / `gateway_boot.go` | mux wiring | dispatch wiring for the preview-host mux |

### Impact assessment (Inferred — no GitNexus in this session)

| Symbol modified | Risk | Dependents that must be re-tested |
|---|---|---|
| `buildWorkspaceCSP` retirement for previews | **HIGH** — every preview response's CSP changes | `rest_preview_test.go`, `preview_iframe_test.go` (frame-ancestors assertions change to `'none'`), `rest_preview_proof_test.go` |
| `libraryIsolationOrigins` rename + share | **MEDIUM** — Library surface touched by rename only | Library preview tests; mail-surface spec consumers on `feature/email-mail` (MC-37 keeps templates separate) |
| `resolveMainOrigin` retirement for previews | MEDIUM — remaining callers keep today's behaviour | callers listed in ADR-094 §5; grep sweep at implementation time |
| `previewOriginUnresolvedResult` extension | MEDIUM — fail-closed triggers widen | `rest_preview_web_serve_e2e_test.go`, `tests/e2e/web-serve-canonical.spec.ts`, `tests/e2e/web-serve-malformed.spec.ts` |
| `workspaceContentType` additions | LOW — additive map entries | static-serving tests |
| `Execute` mode selection + dual URLs | MEDIUM — tool-result text changes shape | web_serve tool-text tests; `Description` text (prometheus-prompt-engineer) |

### Relevant execution flows

| Flow | Relevance |
|---|---|
| web_serve mint → tool result → user opens link in tab | mode selection decides the URL shape; Mode 1 label vs Mode 2 path |
| web_serve mint → agent navigates built-in browser panel | panel is a real Chrome that enforces the served headers — the agent sees exactly what the user sees (ADR-094 correction note item 1) |
| `/preview/` request → static serve or dev-proxy | CSP template emission, CORS, guards, redirect rule |
| preview page → fetch/form/navigation/SW attempt → `/api/v1` | the control stack (ADR-094 §2.3 rows 1–12) is what stops this |

### Cluster placement

Gateway surface cluster (`pkg/gateway` + `pkg/tools/web_serve.go`). Spans into agent tool text (`pkg/tools/web_serve.go::Description`, prometheus-prompt-engineer) and user docs. No SPA cluster changes; no contract changes (ADR-094 §2.5: preview URLs travel in tool-result text, not contract-typed schemas — no `openapi.yaml`/`asyncapi.yaml` delta).

### Available reference patterns

Not applicable — `docs/reference/go-implementation/` does not exist in this repository. The pattern source for this feature is ADR-067's Library isolation machinery (`pkg/gateway/library_isolation_policy.go`), which this feature renames and shares, never re-implements.

## User stories and acceptance criteria

### US-1 — A full web app renders with storage and login on the default loopback install (Mode 1) (P0)

*As the user, I want a preview of an agent-built web app to open as a complete application on its own origin, so that storage-backed apps and in-app logins work exactly as they would outside Omnipus (F794-1, F794-2).*

**Why P0:** the founder rejected every model that sandboxes capability away; this is the primary serving mode wherever the browser self-resolves `*.localhost`.
**Independent test:** serve a storage-and-login web app on a default loopback install, open the chat preview link in Chromium or Firefox, use it as an app.

1. **Given** a default loopback install (canonical origin `http://localhost:<port>`) and Chromium or Firefox, **When** the user opens a web_serve preview link from chat, **Then** the link is `http://<label>.localhost:<port>/`, the app renders as a full web app, `localStorage`/`sessionStorage`/IndexedDB persist across a reload, `document.cookie` works for the previewed app, and an in-app login round-trip works (a full app, not a static page).
2. **Given** a Mode 1 preview, **When** the previewed app sets cookies or makes same-origin requests, **Then** it operates on its own origin's cookie jar and storage — no gateway cookie is present and none is set.
3. **Given** a mint where Mode 1 applies, **When** the tool result is read, **Then** it carries **both** URLs — the subdomain primary and the `/preview/` fallback — so WebKit users and non-loopback deployments keep a working link.

### US-2 — A served preview cannot call the authenticated API as the user (issue #798 acceptance) (P0)

*As the user, I want script in a served preview document to be unable to perform authenticated state-changing `/api/v1/*` requests as me, so that opening a preview my own agent produced cannot change my configuration, agents, credentials references or tool policy.*

**Why P0:** this is the issue's own acceptance criterion; the whole feature exists for it.
**Independent test:** the issue's acceptance test, executed in CI on both paths (ADR-094 §6): script in a served preview attempting an authenticated state-changing API call fails, while ordinary rendering still works.

1. **Given** today's code (RED, before implementation), **When** script in a served `/preview/` document reads `document.cookie` for the CSRF value and POSTs to `/api/v1/…` with the echoed header, **Then** the request succeeds and a state change lands — proving both hole and instrument (ADR-094 §8 RED; dev-proxy path included, not only static).
2. **Given** Mode 1 after implementation, **When** a request to `<label>.localhost` is made carrying a `Domain=localhost` cookie jar, **Then** no gateway cookie is attached (host-only cookies never travel cross-host), an authenticated `/api/v1` call from the preview origin gets a 401, and CORS blocks even reading the error.
3. **Given** Mode 2, **When** the page attempts `fetch`/XHR/`EventSource`/`sendBeacon` to `/api/v1/*` at any nesting, **Then** the request is blocked by CSP source confinement (control-stack row 1); a form POST to `/api/v1/*` at any nesting is blocked by confined `form-action` (row 2).
4. **Given** Mode 2, **When** a top-level navigation GET targets `/api/v1/*` carrying `Sec-Fetch-Dest: document`, **Then** the server rejects the request (row 3); **When** a `/preview/` request carries `Service-Worker: script` or `Sec-Fetch-Dest: serviceworker`, **Then** it is refused (row 4) — a service worker could otherwise outlive the page.
5. **Given** Mode 2, **When** the page iframes the SPA or accesses `window.opener`, **Then** both are already severed: SPA responses carry `frame-ancestors 'none'` (row 5) and every preview link carries `noopener noreferrer` (row 6).
6. **Given** Mode 2, **When** an upstream 302 bounces a subresource or fetch toward `/api/v1`, **Then** the redirect rule applies and any `Location` resolving outside the token prefix becomes a 502 with no `Location` (row 8).
7. **Given** a Mode 1 previewed app that tosses `Domain=localhost` cookies, **When** a state-changing request arrives whose `Cookie` header carries duplicate CSRF-cookie occurrences, **Then** the request is rejected (the bounded cookie-tossing residual, ADR-094 §2.2 hardening note).

### US-3 — The Mode 2 fallback renders fully on WebKit and non-loopback deployments (P0)

*As the user on Safari/WebKit, an IP-literal address, a LAN/real-domain/Tailscale deployment or HTTPS ingress, I want the `/preview/` fallback to render modern agent-built bundles completely, so that previews work everywhere Mode 1 cannot (F794-5, F794-7).*

**Why P0:** everywhere outside Mode 1's narrow window, Mode 2 is the only preview surface; the review measured it rendering blank today without CORS.
**Independent test:** the realistic-bundle fixture renders on Chromium and WebKit through the `/preview/` path, served both statically and through the dev proxy.

1. **Given** WebKit/Safari on a loopback install, **When** the same preview is opened, **Then** it lands on the Mode 2 URL and renders — module scripts, `crossorigin` stylesheets and `fetch('./data.json')` all succeed (the review-measured failure shape must not reproduce).
2. **Given** a Mode 2 dev-server preview, **When** the app opens a hot-reload `ws://` connection, **Then** the connection is allowed (`connect-src` carries the `ws`-scheme form of the prefix).
3. **Given** a Mode 2 static preview, **When** the app posts a form inside the prefix, opens a popup (`window.open` / `target=_blank`), or triggers a download, **Then** each works — no `sandbox` directive exists in the web_serve model.

### US-4 — Misconfigured origins fail closed (F794-6) (P0)

*As the operator, I want a misconfigured gateway origin to refuse to serve previews, so that no degraded or silently-widened policy ever serves (F794-6).*

**Why P0:** F794-6 is a founder decision; the degraded `'self'` fallback mode no longer exists in the web_serve model.
**Independent test:** set each refusal trigger in config, call web_serve, observe refusal; set each Mode-2-only origin shape and observe Mode 2.

1. **Given** a wildcard-host `public_url` (e.g. `http://*.example.com`), **When** web_serve mints, **Then** it refuses — no preview URL is created.
2. **Given** an unparseable origin, **When** web_serve mints, **Then** it refuses.
3. **Given** an empty canonical origin (wildcard bind, no `public_url`), **When** web_serve mints, **Then** it refuses — today's behaviour, preserved (ADR-094 §2.4).
4. **Given** `https://localhost:<port>`, an IP literal (`127.0.0.1`, LAN IP), a real domain or a Tailscale name, **When** web_serve mints, **Then** Mode 2 only — the tool still works everywhere.

### US-5 — Dev-server previews render in both modes with confined redirects (P1)

*As the user, I want dev-server previews to render through the proxy in both modes, and every upstream redirect to stay inside the token prefix, so that proxy serving is as safe and as working as static serving (ADR-094 §6).*

**Why P1:** the proxy path is the second serving path; its redirect rule is normative and its failures are the ones a string-prefix check misses.
**Independent test:** serve the bundle through a dev server with scripted redirect responses; assert in-prefix rewriting and out-of-prefix refusal in both modes.

1. **Given** a dev-server preview in Mode 1, **When** the app renders, **Then** it renders natively on its own host (root-absolute assets and HMR work — Mode 1 fixes them).
2. **Given** a dev-server preview in Mode 2, **When** the upstream responds 301/302/303/307/308 with a `Location` that resolves (WHATWG-parsed) to the gateway origin and begins with the token prefix, **Then** it is emitted; a root-relative `Location` is re-rooted under the prefix; anything else becomes a 502 with no `Location`.
3. **Given** a dev-server preview in Mode 2, **When** the upstream responds 304, **Then** it passes untouched (dev servers send it routinely); **When** a `Location` appears on a non-redirect status, **Then** it is deleted.

### US-6 — An agent produces a rendering preview unaided (P0)

*As the agent, I want the web_serve tool text to teach me both modes and the Mode 2 asset rule, so that following it alone — no human help — I produce a preview that renders in my built-in browser panel and in the user's tab (ADR-094 §6, review MAJ-006).*

**US-6 Why P0:** the repo's Definition of Done is reachability; a preview mode the agent cannot use unaided is not a feature.
**Independent test:** an agent following the tool text alone serves a full web app; the preview renders in the built-in browser panel and the user's tab.

1. **Given** the updated tool text, **When** an agent follows it alone to serve a full web app, **Then** the preview renders in the agent's built-in browser panel and in the user's tab.
2. **Given** Mode 2, **When** the agent builds the app, **Then** the tool text taught it the asset rule (assets relative or under the token prefix, e.g. Vite `base: './'`) and to check the browser console in the built-in panel when a preview renders blank.

### US-7 — The accepted residuals are documented (P1)

*As the user, I want the security residuals I am living with named in user-facing docs, so that an informed operator knows what a preview can and cannot do (F794-4, F794-7, review MIN-007/MIN-008).*

**Why P1:** the founder accepted these residuals on the condition they be documented; documentation is part of the acceptance.
**Independent test:** read the user docs cold; find both residuals and the troubleshooting step without asking a developer.

1. **Given** the shipped user docs, **When** a reader looks for preview limitations, **Then** they find the F794-4 phishing residual (an agent-built page can imitate an Omnipus login screen at the real address) named plainly.
2. **Given** a blank preview, **When** the reader follows the troubleshooting note, **Then** they are told to check the browser console (CSP violations are console-only) — drafted by the implementing lead, audited by docs-verifier.
3. **Given** the Mode 2 fallback, **When** its residual is described, **Then** the F794-7 same-origin popup-scripting residual is documented (hosted-version constraint tracked in elicify-ai/omnipus-ai#1177, outside this repo's scope).

## Edge cases

| Case | Expected |
|---|---|
| Valid label shape, no live registration | 404 — an attacker-resolvable label is worthless (ADR-094 §2.2) |
| Label grammar boundaries | 1–63 chars, letters/digits/hyphens, no leading/trailing hyphen are valid; 64 chars, leading/trailing hyphen, `_` (the current token shape) are invalid |
| Host validation | exact match against grammar + literal `localhost` suffix + listener port; anything else falls to the main mux unchanged |
| Hostile agent ID (`;`, `,`, spaces, quotes — `validation.EntityID` admits them) | percent-encoded to the RFC 3986 unreserved set in CSP sources; no directive split (review MIN-002) |
| Redirect `Location` forms | dot-segments, `%2e`, protocol-relative `//host/…`, backslash `/\host`, absolute-same-origin (`http://127.0.0.1:5000/api/v1/x` — the Director does not rewrite `Host`) all resolve outside the prefix → 502, no `Location` |
| `304 Not Modified` | passes untouched (dev servers send it routinely) |
| `Location` on a non-redirect status | deleted |
| Upstream `Set-Cookie` for reserved gateway cookie names | neutralized (existing behaviour, preserved) |
| Upstream `Content-Security-Policy`/`X-Frame-Options` | stripped (existing behaviour, preserved); a `<meta http-equiv="Content-Security-Policy">` in agent HTML can only tighten — multiple policies intersect |
| Upstream `ACAO`/`ACAC` on proxied responses | deleted before the prefix-wide override |
| `[::1]` IPv6-literal reader | today's documented limitation unchanged: the CSP cannot name it, rendering degrades with the existing Library-style WARN (ADR-094 §4 Neutral 4) |
| Pre-16.4 Safari (no Fetch Metadata headers) | the navigation-guard layer is absent on such engines; mitigated by the GET-side-effect-free invariant (audit action) and CSRF gating only state-changing methods — accepted, ADR-094 §2.3 row 3 |
| Cookie tossing on Mode 1 | `Domain=localhost` cookies are settable but bounded: a valid session value cannot be minted (server-side verification), `__Host-csrf` cannot be tossed under TLS (prefix construction); duplicate-CSRF-cookie rejection is the hardening check |
| SPA deep links | invariant "no SPA route performs a state change from URL parameters on load" — audited by security-lead before this lands (OBS-002) |
| Preview embedded in an iframe | `frame-ancestors 'none'` on preview responses in both modes; embedding a preview inside the SPA needs its own analysis — out of scope (ADR-094 §4 Neutral 3) |

## Behavioral contract

Primary flows:
- When a default loopback install mints a preview, the system serves the full app on `http://<label>.localhost:<port>/` and returns the `/preview/` fallback alongside it.
- When a non-Mode-1 deployment or engine mints or opens a preview, the system serves the full app on the unchanged `/preview/` path with the confined control stack.
- When the previewed app uses storage, cookies, login, forms, popups or downloads, the system leaves it unimpaired in both modes — no `sandbox` directive exists in the web_serve model.

Error flows:
- When the canonical origin is misconfigured (wildcard-host `public_url`, unparseable, empty), the system refuses to mint — no preview URL, no degraded policy.
- When script in a served preview attempts an authenticated state-changing API call, the system blocks it — structurally (Mode 1: no gateway credential on the preview origin) or by the control stack (Mode 2).
- When an upstream redirect resolves outside the token prefix, the system replaces the response with a 502 and no `Location`.
- When a valid label has no live registration, the system answers 404.

Boundary conditions:
- When `https://localhost`, an IP literal, a real domain or a Tailscale name is configured, the system serves Mode 2 only.
- When the `Host` is anything other than a valid `<label>.localhost(:port)` or the gateway's own host, the system behaves exactly as today (main mux).
- When the same preview is opened in different engines, the system gives Chromium/Firefox/Edge on loopback the Mode 1 URL and everything else the Mode 2 URL.

## Explicit non-behaviors and safeguards

### Qualitative prohibitions

- The system must not reintroduce a `sandbox` CSP directive into the web_serve model, because F794-1/F794-2 require full capability (storage APIs throw under `sandbox` — review-measured).
- The system must not converge web_serve on the ADR-067 opaque-origin sandbox model, because the founder rejected it (round-1's Decision, F794-1).
- The system must not degrade to a `'self'`-based fallback policy under any trigger, because F794-6 makes every misconfigured origin a refusal.
- The system must not touch the Library (`/library-preview/`) or mail (`/mail-preview/`) surfaces' CSP, opaque-origin or no-redirect machinery, because their payloads are untrusted content and no founder decision changes them (ADR-094 §2.5).
- The system must not change preview tokens, TTLs, revocation, the SPA link-out UX, ADR-044's consolidation (one listener, deleted config keys, `gateway.preview_enabled` hot-flip), or add config keys — all unchanged scope (ADR-094 §2.5).
- The system must not add a wire-format change (contracts unchanged — ADR-094 §2.5).
- The system must not rely on closing the Mode 2 popup-scripting residual with a header, because no header stack can (F794-7 accepts it; the hosted version must not rely on the fallback — tracked in elicify-ai/omnipus-ai#1177).
- The system must not silently widen a CSP source (bare origin instead of prefix, extra scheme), because the tripwires treat the literal template as the oracle.

### Machine-verifiable constraints

**Serving modes and URL shapes**
- When the canonical origin is `http://localhost:<port>`, web_serve MUST return both `http://<label>.localhost:<port>/…` (primary) and the `/preview/` fallback URL in one tool result.
- When the canonical origin is `https://localhost:<port>`, an IP literal, a real domain or a Tailscale name, web_serve MUST return the `/preview/` URL only.
- When the canonical origin is a wildcard-host `public_url`, unparseable, or empty, web_serve MUST refuse with an error result and mint no URL.

**Mode 1 host dispatch**
- A request with `Host` = `<label>.localhost(:port)` (label matches the grammar) MUST be routed to a mux serving only preview content; `/api/v1/*`, `/auth/*` and every other route MUST answer 404 under a preview Host.
- Labels MUST match the grammar: letters, digits, hyphens; 1–63 chars; no leading or trailing hyphen; minted from fresh entropy in a hostname-safe encoding; mapped to the same registry entry as the Mode 2 token.
- A valid-shaped label with no live registration MUST answer 404.
- The agent for a label MUST be resolved from the registration (the token↔agent mismatch rule preserved); the Mode 2 token remains the Mode 2 credential.

**Mode 2 response headers (the CSP template — the tripwire oracle; `${ORIGIN}` = canonical origin, `${PREFIX}` = `/preview/{agent}/{token}/` percent-encoded to the RFC 3986 unreserved set, `${ORIGIN-WS}` = the `ws://`/`wss://` form of the canonical origin):**

```
default-src 'none';
script-src ${ORIGIN}${PREFIX} 'unsafe-inline' 'unsafe-eval';
style-src ${ORIGIN}${PREFIX};
img-src ${ORIGIN}${PREFIX} data:;
font-src ${ORIGIN}${PREFIX} data:;
media-src ${ORIGIN}${PREFIX};
connect-src ${ORIGIN}${PREFIX} ${ORIGIN-WS}${PREFIX};
form-action ${ORIGIN}${PREFIX};
worker-src ${ORIGIN}${PREFIX} blob:;
base-uri 'none';
object-src 'none';
frame-ancestors 'none';
```

- The CSP response header on every Mode 2 preview response MUST equal the template above: the origin list frozen at boot, the string byte-stable per `(agent, token)`, the prefix percent-encoded — the builder MUST never interpolate raw registry values. No `sandbox` directive exists anywhere in the web_serve model.
- Mode 1 responses carry `frame-ancestors 'none'` and no source directives (ADR-094 §9 MIN-001 disposition, §4 Neutral 3) and no `sandbox` directive; the exact remaining header set stays minimal to what the ADR names.
- A `/api/v1` request carrying `Sec-Fetch-Dest: document` MUST be rejected before reaching the API handler (the exact status code is an implementation choice the ADR leaves open — pinned by the implementing RED/GREEN pair; the requirement is that the request never reaches the handler).
- A `/preview/` request carrying `Service-Worker: script` or `Sec-Fetch-Dest: serviceworker` MUST be refused (same status-code note).
- Every successful response under the token prefix, static and proxied, MUST carry `Access-Control-Allow-Origin: *` and MUST NOT carry `Access-Control-Allow-Credentials`; on proxied responses any upstream ACAO/ACAC MUST be deleted before the override; preflight requests under the prefix MUST be answered.
- Redirect rule (normative, ADR-094 §2.3): on status ∈ {301, 302, 303, 307, 308}, the system MUST resolve `Location` against the request URL with WHATWG parsing (dot-segments and `%2e` normalised); emit it when the result's origin is the gateway origin (any alias) and its path begins with `/preview/{agent}/{token}/`; else, when the raw value was root-relative, re-root it under the token prefix and apply the same check; otherwise replace the response with HTTP 502 and no `Location`. A `Location` header on any non-redirect status MUST be deleted. `304 Not Modified` MUST pass untouched.
- The MIME map MUST answer `.mjs` → `text/javascript`, `.woff`/`.woff2` → `font/*`, `.wasm` → `application/wasm` (the review verified these are absent today — their absence blocks module scripts under `nosniff`).
- On a state-changing request whose `Cookie` header carries duplicate CSRF-cookie occurrences, the system MUST reject the request (cookie-tossing hardening).
- One shared origin derivation MUST serve all three preview surfaces (renamed `previewIsolationOrigins`); per-surface policy templates and their tripwires stay separate; the third origin computation is retired for preview responses; Mode 2 CSP sources use the canonical origin only (no loopback-alias widening).

**Tool text and docs**
- The web_serve tool text MUST teach both modes and the Mode 2 asset rule (assets relative or under the token prefix) and the console-check guidance — owned by prometheus-prompt-engineer; an agent following it alone MUST be able to produce a rendering preview (ADR-094 §6, review MAJ-006).
- User docs MUST name the F794-4 phishing residual and carry the blank-preview troubleshooting note (console-only violations).

## Integration boundaries

### Dev-server upstream (proxy path)
- **Data in:** proxy requests forwarded to the registration's loopback port (upstream `Cookie` header stripped — existing behaviour, preserved).
- **Data out:** upstream responses with CSP template emission, `ACAO: *` override (upstream ACAO/ACAC deleted first), reserved-`Set-Cookie` neutralization, the normative redirect rule.
- **Contract:** HTTP over the registration's loopback port; the Director does not rewrite `Host`, so absolute-same-origin `Location` forms are possible and must be handled.
- **On failure:** upstream down or out-of-policy redirect → error surfaced (502 per the redirect rule), never a fallback to another mode or a degraded policy.
- **Development:** a real dev server and the committed realistic-bundle fixture — never mocked at this boundary.

### User's browser (the tab)
- **Data in:** the preview URL from the tool result / SPA link.
- **Data out:** requests subject to the served headers; Chromium/Firefox/Edge on loopback resolve `*.localhost` internally (RFC 6761); WebKit delegates to the system resolver and lands on Mode 2.
- **On failure:** an engine or deployment where neither mode resolves → web_serve refusal path (F794-6) — never a degraded policy.

### Agent's built-in browser panel
- **Data in:** the same URL web_serve returns; a real Chrome that enforces the same served headers (ADR-094 correction note item 1).
- **Contract:** the agent sees exactly what the user sees — this is the agent's own review surface and part of the reachability claim (US-6).
- **On failure:** a blank render in the panel is diagnosable via the console (tool text guidance, user docs note).

### Registry and tokens
- **Contract:** unchanged tokens (43-char base64url), TTLs, revocation; Mode 1 labels map to the same registry entry; no new config keys; no wire-format change.

## BDD scenarios

### Feature: Preview isolation with full capability (ADR-094)

#### S-1.1 — Mode 1 link opens a full web app on the default loopback install
**Traces to**: US-1, acceptance scenario 1
**Category**: Happy Path
- **Given** a default loopback install (canonical origin `http://localhost:<port>`) and Chromium or Firefox
- **When** the user opens a web_serve preview link from chat
- **Then** the link is `http://<label>.localhost:<port>/` and the app renders as a full web app
- **And** `localStorage`/`sessionStorage`/IndexedDB persist across a reload, `document.cookie` works for the previewed app, and an in-app login round-trip works

#### S-1.2 — Mode 1 app uses its own origin's storage and cookies
**Traces to**: US-1, acceptance scenario 2
**Category**: Happy Path
- **Given** a Mode 1 preview open in the tab
- **When** the previewed app writes storage or sets cookies
- **Then** the data lands on the preview origin's own jar/store
- **And** no gateway cookie is present on the preview origin and none is set

#### S-1.3 — Mode 1 mint returns both URLs
**Traces to**: US-1, acceptance scenario 3
**Category**: Happy Path
- **Given** a canonical origin of `http://localhost:<port>`
- **When** web_serve mints a preview
- **Then** the tool result carries the subdomain primary URL and the `/preview/` fallback URL

#### S-2.1 — RED: today's code lets a served preview call the API as the user
**Traces to**: US-2, acceptance scenario 1
**Category**: Error Path
- **Given** the code before this feature, with a served `/preview/` document containing a probe script
- **When** the probe reads `document.cookie` for the CSRF value and POSTs to `/api/v1/…` with the echoed header
- **Then** the request succeeds and a state change lands (hole and instrument both proven)
- **And** the probe runs against the dev-proxy path too, not only static

#### S-2.2 — Mode 1: no gateway credential crosses to the subdomain
**Traces to**: US-2, acceptance scenario 2
**Category**: Happy Path
- **Given** Mode 1 implemented, with a browser holding `Domain=localhost` cookies
- **When** a page at `http://<label>.localhost:<port>/` attempts an authenticated `/api/v1` call
- **Then** no gateway cookie is attached to the request, the call gets a 401, and CORS blocks reading the error

#### S-2.3 — Mode 2: fetch-class and form-class calls to the API are confined
**Traces to**: US-2, acceptance scenario 3
**Category**: Error Path
- **Given** a served Mode 2 preview document
- **When** the page attempts `fetch`/XHR/`EventSource`/`sendBeacon` to `/api/v1/*` at any nesting, or a form POST to `/api/v1/*` at any nesting
- **Then** each attempt is blocked by the confined `connect-src` / `form-action` sources

#### S-2.4 — Mode 2: navigation and service-worker paths are refused server-side
**Traces to**: US-2, acceptance scenario 4
**Category**: Error Path
- **Given** a served Mode 2 preview document
- **When** a top-level navigation GET targets `/api/v1/*` carrying `Sec-Fetch-Dest: document`
- **Then** the server rejects the request before the API handler
- **And** when a `/preview/` request carries `Service-Worker: script` or `Sec-Fetch-Dest: serviceworker`, it is refused

#### S-2.5 — SPA iframing and opener access stay severed
**Traces to**: US-2, acceptance scenario 5
**Category**: Error Path
- **Given** a served preview in either mode
- **When** the page iframes the SPA or reaches for `window.opener`
- **Then** the SPA response's `frame-ancestors 'none'` refuses the frame and `noopener noreferrer` severs the opener

#### S-2.6 — Redirect pivot out of the prefix becomes a 502
**Traces to**: US-2, acceptance scenario 6
**Category**: Error Path
- **Given** a Mode 2 dev-server preview whose upstream responds 301/302/303/307/308
- **When** the `Location` resolves (WHATWG-parsed) outside the token prefix — dot-segment, `%2e`, protocol-relative, backslash, or absolute-same-origin forms
- **Then** the response is replaced with 502 and no `Location`

#### S-2.7 — Duplicate CSRF-cookie occurrences are rejected
**Traces to**: US-2, acceptance scenario 7
**Category**: Error Path
- **Given** a Mode 1 preview that has tossed `Domain=localhost` CSRF-cookie values
- **When** a state-changing request arrives whose `Cookie` header carries duplicate CSRF-cookie occurrences
- **Then** the request is rejected

#### S-3.1 — Mode 2 renders the realistic bundle on WebKit and Chromium
**Traces to**: US-3, acceptance scenario 1
**Category**: Happy Path
- **Given** the committed realistic-bundle fixture (real Vite production build: module script, `crossorigin` stylesheet, `fetch('./data.json')`, guarded `localStorage` probe, in-prefix form POST, `target=_blank` link)
- **When** the fixture is served on the `/preview/` path — statically and through the dev proxy — and opened on Chromium and WebKit
- **Then** it renders and functions: module scripts, `crossorigin` CSS and `fetch('./data.json')` succeed
- **And** storage and a cookie-backed login work in both modes (F794-1/F794-2 acceptance)

#### S-3.2 — Mode 2 allows dev-server hot reload
**Traces to**: US-3, acceptance scenario 2
**Category**: Happy Path
- **Given** a Mode 2 dev-server preview
- **When** the app opens its hot-reload `ws://` connection to the prefix
- **Then** the connection is allowed (`connect-src` carries the `ws`-scheme prefix form)

#### S-3.3 — Mode 2 leaves forms, popups and downloads unimpaired
**Traces to**: US-3, acceptance scenario 3
**Category**: Happy Path
- **Given** a Mode 2 static preview
- **When** the app posts a form inside the prefix, opens a popup, or triggers a download
- **Then** each works — no `sandbox` directive exists in the web_serve model

#### S-4.1 — Fail-closed on every misconfigured origin (outline)
**Traces to**: US-4, acceptance scenarios 1–3
**Category**: Error Path
- **Given** a canonical origin of `<origin>`
- **When** web_serve mints a preview
- **Then** the mint refuses with an error result and no URL

**Examples**

| origin | trigger |
|---|---|
| `http://*.example.com` | wildcard-host `public_url` |
| `not a url` | unparseable origin |
| *(empty — wildcard bind, no `public_url`)* | today's fail-closed path, preserved |

#### S-4.2 — Mode-2-only origins still work (outline)
**Traces to**: US-4, acceptance scenario 4
**Category**: Alternate Path
- **Given** a canonical origin of `<origin>`
- **When** web_serve mints a preview
- **Then** the result carries the `/preview/` URL only, and the preview serves

**Examples**

| origin |
|---|
| `https://localhost:<port>` |
| `http://127.0.0.1:<port>` (IP literal, incl. LAN IP) |
| `https://omnipus.example.com` (real domain) |
| Tailscale device name |

#### S-5.1 — Mode 1 fixes dev-server hosting natively
**Traces to**: US-5, acceptance scenario 1
**Category**: Happy Path
- **Given** a dev-server preview in Mode 1
- **When** the app loads root-absolute assets and opens its hot-reload connection
- **Then** both work natively — the app owns the whole host

#### S-5.2 — In-prefix redirects are rewritten and emitted
**Traces to**: US-5, acceptance scenario 2
**Category**: Happy Path
- **Given** a Mode 2 dev-server preview whose upstream responds 301/302/303/307/308
- **When** the `Location` resolves to the gateway origin beginning with the token prefix, or was root-relative
- **Then** the emitted `Location` stays inside the token prefix (root-relative re-rooted first)

#### S-5.3 — 304 passes; stray `Location` is deleted
**Traces to**: US-5, acceptance scenario 3
**Category**: Edge Case
- **Given** a Mode 2 dev-server preview
- **When** the upstream responds 304, or a `Location` header on a non-redirect status
- **Then** the 304 passes untouched and the stray `Location` is deleted

#### S-6.1 — Agent produces a rendering preview following the tool text alone
**Traces to**: US-6, acceptance scenarios 1–2
**Category**: Happy Path
- **Given** the updated web_serve tool text (both modes, Mode 2 asset rule, console guidance)
- **When** an agent follows it alone to serve a full web app
- **Then** the preview renders in the agent's built-in browser panel and in the user's tab

#### S-7.1 — Residuals and troubleshooting are in the user docs
**Traces to**: US-7, acceptance scenarios 1–3
**Category**: Happy Path
- **Given** the shipped user docs
- **When** a reader looks for preview limitations or has a blank preview
- **Then** they find the F794-4 phishing residual, the F794-7 fallback residual, and the console-check troubleshooting note

#### S-7.2 — Contract regression: no wire-format change
**Traces to**: US-7 (scope boundary), ADR-094 §2.5
**Category**: Edge Case
- **Given** the landed feature branch
- **When** `make verify-contracts` runs
- **Then** it passes — no `openapi.yaml`/`asyncapi.yaml` delta exists

## TDD plan

Test strategy and ownership follow ADR-094 §8 exactly: **RED** (qa-lead, browser-level, proves both hole and instrument, on both paths) → **GREEN** (backend-lead, unit/integration) → **Browser GREEN** (the issue's acceptance fixture) → **CHECK** (qa-lead, mutation). Tests are written before implementation; the browser-level RED runs against current code first.

### Test implementation order

| Order | Test | Level | Traces to BDD scenario | Description |
|---|---|---|---|---|
| 1 | RED hole-proof, path URL | E2E (browser) | S-2.1 | script in a served `/preview/` document reads `document.cookie` (CSRF value), POSTs an authenticated state change — succeeds on current code; dev-proxy path included, not only static |
| 2 | RED Mode 1 shape | E2E (browser) | S-2.2 | a request to `<label>.localhost` carrying a `Domain=localhost` cookie jar shows whether any gateway cookie attaches (must be none, once implemented), against a stub host dispatch |
| 3 | Label grammar cases | Unit | S-2.2, S-4.1 | every row of dataset DS-1 |
| 4 | Host-dispatch 404 cases | Integration | S-2.2 | preview Host serves only preview routes; `/api/v1/*`, `/auth/*` → 404 under a preview Host; unknown label → 404; non-preview Host reaches the main mux unchanged |
| 5 | Mode selection + fail-closed | Unit | S-1.3, S-4.1, S-4.2 | every row of dataset DS-3, one negative assertion per refusal trigger |
| 6 | CSP builder tripwires | Unit | S-2.3, S-3.3 | template equality (the literal template is the oracle), hostile agent ID (DS-4), `sandbox`-absent assertion, byte-stability per `(agent, token)` |
| 7 | MIME additions | Unit | S-3.1 | every row of dataset DS-5 |
| 8 | Navigation + SW guards | Integration | S-2.4 | `Sec-Fetch-Dest: document` on `/api/v1` rejected; `Service-Worker: script` / `Sec-Fetch-Dest: serviceworker` refused under `/preview/` |
| 9 | CORS pair | Integration | S-3.1 | `ACAO: *` prefix-wide on static and proxied successes, never `ACAC`; upstream ACAO/ACAC deleted before override; preflight answered |
| 10 | Redirect rule cases | Integration | S-2.6, S-5.2, S-5.3 | every row of dataset DS-2, including dot-segment, `%2e`, protocol-relative, backslash, absolute-same-origin |
| 11 | Duplicate-CSRF-cookie rejection | Integration | S-2.7 | tossed jar → state-changing request rejected |
| 12 | Browser GREEN: realistic-bundle fixture | E2E (browser) | S-1.1, S-3.1, S-3.2, S-3.3, S-5.1 | a committed real Vite production build — module script, `crossorigin` stylesheet, `fetch('./data.json')`, guarded `localStorage` probe, in-prefix form POST, `target=_blank` link — asserted to render and function on Chromium **and** WebKit, served **both** statically and through the dev proxy, in **both** modes; storage and a cookie-backed login work in both modes |
| 13 | E2E regression sweep | E2E | S-7.2 + preserved set | `rest_preview_web_serve_e2e_test.go`, `tests/e2e/web-serve-canonical.spec.ts`, `tests/e2e/web-serve-malformed.spec.ts`, any vitest touching preview links |
| 14 | CHECK mutations | Mutation | S-2.3, S-2.4, S-2.6, S-3.1 | the seven named mutations below |

**CHECK mutations** (each must turn a named test red — a mutation that survives means the test could not have detected the failure):

1. Drop `ACAO: *` → the Mode 2 fixture render fails (S-3.1).
2. Widen `connect-src` to the bare origin → the builder tripwire fails (S-2.3).
3. Replace the WHATWG `Location` check with a string-prefix check → the dot-segment/`%2e` tripwire cases fail (S-2.6).
4. Remove the `Service-Worker` refusal → the SW test fails (S-2.4).
5. Remove the navigation rejection → the navigation test fails (S-2.4).
6. Accept a non-DNS-safe label → the grammar test fails (S-2.2 / DS-1).
7. Drop the `sandbox`-absent assertion → the tripwire fails (S-3.3).

### Test datasets

#### DS-1 — Mode 1 label grammar

| # | Input | Boundary type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 1-char label `a` | min | valid | S-2.2 | |
| 2 | 63-char letters/digits/hyphens | max | valid | S-2.2 | |
| 3 | 64 chars | max+1 | invalid | S-2.2 | |
| 4 | leading hyphen | edge | invalid | S-2.2 | |
| 5 | trailing hyphen | edge | invalid | S-2.2 | |
| 6 | current token shape (43-char base64url, contains `_`) | edge | invalid as a label | S-2.2 | `_` is not hostname-safe — why Mode 1 mints its own label |
| 7 | empty | boundary | invalid | S-2.2 | |
| 8 | fresh-entropy mint | happy | valid + mapped to the token's registry entry | S-1.3 | encoding is an implementation choice meeting the grammar |

#### DS-2 — Redirect `Location` forms (dev proxy, Mode 2)

| # | Upstream `Location` | Boundary type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `/preview/{agent}/{token}/app/` (same-origin, in-prefix) | happy | emitted as-is | S-5.2 | WHATWG-resolved |
| 2 | `/next` root-relative | happy | re-rooted under the prefix, emitted | S-5.2 | |
| 3 | `/preview/{agent}/{token}/../../api/v1/x` | edge | 502, no `Location` | S-2.6 | dot-segments normalise outside the prefix |
| 4 | `/preview/{agent}/{token}/%2e%2e/api/v1/x` | edge | 502, no `Location` | S-2.6 | `%2e` normalisation |
| 5 | `//127.0.0.1:<gwport>/api/v1/x` | edge | 502, no `Location` | S-2.6 | protocol-relative |
| 6 | `/\host/api/v1/x` | edge | 502, no `Location` | S-2.6 | backslash form |
| 7 | `http://127.0.0.1:5000/api/v1/x` absolute-same-origin (gateway host) | edge | 502, no `Location` | S-2.6 | the Director does not rewrite `Host` |
| 8 | 304 response | alternate | passes untouched | S-5.3 | dev servers send it routinely |
| 9 | `Location` on 200 | edge | header deleted | S-5.3 | |

#### DS-3 — Mode selection and fail-closed triggers

| # | Canonical origin | Boundary type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `http://localhost:<port>` | happy | Mode 1 primary + `/preview/` fallback, one result | S-1.3 | the only Mode 1 trigger |
| 2 | `https://localhost:<port>` | alternate | Mode 2 only | S-4.2 | subdomain certificates need a private CA — environment dependency |
| 3 | `http://127.0.0.1:<port>` / LAN IP literal | alternate | Mode 2 only | S-4.2 | browsers do not resolve subdomains of IP literals |
| 4 | real domain / Tailscale name | alternate | Mode 2 only | S-4.2 | wildcard DNS or certificates are environment dependencies (F794-5) |
| 5 | `http://*.example.com` (wildcard-host `public_url`) | error | refuse — no URL | S-4.1 | F794-6 |
| 6 | unparseable origin | error | refuse — no URL | S-4.1 | F794-6 |
| 7 | empty (wildcard bind, no `public_url`) | error | refuse — no URL | S-4.1 | today's behaviour, preserved |

#### DS-4 — Hostile agent ID in CSP construction

| # | Agent ID fragment | Boundary type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `a;b` | edge | percent-encoded; directives not split | S-2.3 | `;` would split directives |
| 2 | `a,b c"d` | edge | percent-encoded; no corruption | S-2.3 | `,` space `"` corrupt sources |
| 3 | registry value round-trip | happy | source path matches the served prefix exactly | S-2.3 | never interpolate raw registry values |

#### DS-5 — MIME additions

| # | Extension | Expected type | Traces to | Notes |
|---|---|---|---|---|
| 1 | `.mjs` | `text/javascript` | S-3.1 | absent today — blocks module scripts under `nosniff` |
| 2 | `.woff` / `.woff2` | `font/*` | S-3.1 | absent today |
| 3 | `.wasm` | `application/wasm` | S-3.1 | absent today |
| 4 | existing extensions (`.html`, `.css`, `.js`, …) | unchanged | S-3.1 | additive-only regression |

### Regression requirements

**Load-bearing invariants** — audit actions riding with implementation (ADR-094 §5); both are assumptions of the control stack, verified before landing:
- security-lead: "no SPA route performs a state change from URL parameters on load" (review OBS-002).
- backend-lead: "no GET on `/api/v1` mutates state".

| Existing behaviour | Existing test | New regression test needed | Notes |
|---|---|---|---|
| Upstream CSP/XFO stripped on proxied responses | `rest_preview_test.go` | No — keep green | existing behaviour preserved (ADR-094 row 9) |
| Reserved gateway `Set-Cookie` names neutralized | `rest_preview_test.go` | No — keep green | row 10 |
| Dev-proxy upstream `Cookie` header stripped | `rest_preview_test.go` | No — keep green | FR-013 unchanged (ADR-094 §2.1) |
| Preview tokens, TTLs, revocation | token-store tests | No — keep green | unchanged scope |
| WebSocket origin check reads `r.Host`; main mux has no Host allowlist | `rest_preview_web_serve_e2e_test.go`, e2e specs | No — keep green | today's posture unchanged (ADR-094 §2.2) |
| Library + mail surfaces' CSP/opaque-origin/no-redirect machinery | Library preview tests; mail spec consumers | No — keep green | untouched by F794-1…6 (ADR-094 §2.5) |
| Frame-ancestors on preview responses | `preview_iframe_test.go` | **Yes — update assertions to `'none'`** | named test churn (ADR-094 §4 Negative 5) |
| Wide-CSP string tests | existing CSP tests | **Yes — rewrite for the new template** | same churn |
| No wire-format change | `make verify-contracts` | No — keep green | S-7.2 |

## Functional requirements

- **FR-001**: web_serve MUST mint a Mode 1 subdomain URL as primary plus the `/preview/` fallback URL in one tool result when the canonical origin is `http://localhost:<port>`.
- **FR-002**: web_serve MUST serve Mode 2 only for `https://localhost:<port>`, IP literals, real domains and Tailscale names.
- **FR-003**: web_serve MUST refuse (fail closed, no URL minted) when the canonical origin is a wildcard-host `public_url`, unparseable, or empty (F794-6).
- **FR-004**: a request whose Host is a valid `<label>.localhost(:port)` MUST reach a preview-host mux serving only preview content; `/api/v1/*`, `/auth/*` and every other route MUST answer 404 under a preview Host.
- **FR-005**: Mode 1 labels MUST match the grammar (letters, digits, hyphens; 1–63 chars; no leading/trailing hyphen), be minted from fresh entropy in a hostname-safe encoding, and map to the same registry entry as the Mode 2 token.
- **FR-006**: a syntactically valid label with no live registration MUST answer 404.
- **FR-007**: the agent MUST be resolved from the registration for label lookup — the token↔agent mismatch rule preserved.
- **FR-008**: every Mode 2 preview response MUST carry a CSP header equal to the literal template above (the tripwire oracle): boot-frozen canonical origin, byte-stable per `(agent, token)`, prefix percent-encoded to the RFC 3986 unreserved set, raw registry values never interpolated, `frame-ancestors 'none'`, and no `sandbox` directive anywhere in the web_serve model.
- **FR-009**: Mode 1 preview responses MUST carry `frame-ancestors 'none'` and no source directives, and no `sandbox` directive.
- **FR-010**: an `/api/v1` request carrying `Sec-Fetch-Dest: document` MUST be rejected before reaching the API handler.
- **FR-011**: a `/preview/` request carrying `Service-Worker: script` or `Sec-Fetch-Dest: serviceworker` MUST be refused.
- **FR-012**: every successful response under the token prefix (static and proxied) MUST carry `Access-Control-Allow-Origin: *`, MUST NOT carry `Access-Control-Allow-Credentials`, MUST have upstream ACAO/ACAC deleted before the override on proxied responses, and MUST have preflight answered under the prefix.
- **FR-013**: the proxy MUST apply the normative redirect rule (WHATWG resolution, in-prefix emission, root-relative re-rooting, 502 + no `Location` otherwise, `Location` deleted on non-redirect statuses, 304 untouched).
- **FR-014**: the MIME map MUST add `.mjs → text/javascript`, `.woff/.woff2 → font/*`, `.wasm → application/wasm`.
- **FR-015**: a state-changing request whose `Cookie` header carries duplicate CSRF-cookie occurrences MUST be rejected.
- **FR-016**: one shared origin derivation MUST serve all three preview surfaces (renamed `previewIsolationOrigins`); per-surface policy templates and tripwires MUST stay separate; the third origin computation MUST be retired for preview responses; Mode 2 sources MUST use the canonical origin only.
- **FR-017**: the web_serve tool text MUST teach both modes, the Mode 2 asset rule (relative or under the prefix) and the console-check guidance (prometheus-prompt-engineer).
- **FR-018**: user docs MUST name the F794-4 phishing residual and carry the blank-preview troubleshooting note (drafted by the implementing lead, audited by docs-verifier).
- **FR-019**: the preservation set MUST hold: tokens/TTLs/revocation unchanged; dev-proxy `Cookie` strip unchanged; upstream CSP/XFO strip unchanged; reserved `Set-Cookie` neutralization unchanged; Library and mail surfaces untouched; no contracts delta; SPA link-out UX unchanged; no new config keys.

## Success criteria

Derived verbatim from ADR-094 §6 (Reachability / Definition of Done) — no criterion invented:

- **SC-001**: a real user opens a web_serve preview from chat on a default loopback install in Chromium or Firefox; the link is `http://<label>.localhost:<port>/` and the app renders **with storage and login working** (a full app, not a static page).
- **SC-002**: the same preview opened in WebKit/Safari lands on the Mode 2 URL and renders — module scripts, `crossorigin` CSS and `fetch('./data.json')` all succeed (the review-measured failure shape does not reproduce).
- **SC-003**: a dev-server preview renders through the proxy in both modes, including the upstream-redirect case (rewritten `Location` stays in-prefix).
- **SC-004**: an agent following the web_serve tool text alone — no human help — produces a preview that renders in the agent's built-in browser panel and in the user's tab.
- **SC-005**: the issue's acceptance test (script in a served preview cannot perform an authenticated state-changing `/api/v1/*` request; ordinary rendering still works) **executes in CI, not merely exists**, on both paths.
- **SC-006**: user-facing docs name the F794-4 phishing residual and the blank-preview troubleshooting step.

## Traceability matrix

| Requirement | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | S-1.3, S-1.1 | order 5, 12 |
| FR-002 | US-4 | S-4.2 | order 5 |
| FR-003 | US-4 | S-4.1 | order 5 |
| FR-004 | US-2 | S-2.2 | order 4 |
| FR-005 | US-1, US-2 | S-2.2, S-1.3 | order 3, 5 (DS-1) |
| FR-006 | US-2 | S-2.2 | order 4 |
| FR-007 | US-2 | S-2.2 | order 4 |
| FR-008 | US-2, US-3 | S-2.1 (RED baseline), S-2.3, S-2.5, S-2.6, S-3.3 | order 1 (RED), 6, 12, 14 |
| FR-009 | US-2, US-3 | S-1.1, S-3.3 | order 6, 12 |
| FR-010 | US-2 | S-2.4 | order 8, 14 |
| FR-011 | US-2 | S-2.4 | order 8, 14 |
| FR-012 | US-3 | S-3.1 | order 9, 12, 14 |
| FR-013 | US-2, US-5 | S-2.6, S-5.2, S-5.3 | order 10, 14 (DS-2) |
| FR-014 | US-3 | S-3.1 | order 7 (DS-5) |
| FR-015 | US-2 | S-2.7 | order 11 |
| FR-016 | US-2, US-4 | S-2.3, S-4.1 | order 5, 6 |
| FR-017 | US-6 | S-6.1 | order 12 (agent-reachability), SC-004 holdout |
| FR-018 | US-7 | S-7.1 | docs-verifier audit; SC-006 holdout |
| FR-019 | US-7 | S-7.2 + regression table | order 13 + kept-green set |

**Completeness check**: every FR-001…FR-019 appears; every BDD scenario S-1.1…S-7.2 appears; every scenario traces to at least one FR via its user story.

## Ambiguity warnings

| # | What's ambiguous | Likely agent assumption | Disposition |
|---|---|---|---|
| 1 | Exact HTTP status code for the `Sec-Fetch-Dest: document` rejection and the `Service-Worker: script` refusal — ADR-094 §2.3 rows 3–4 say "reject"/"refuse" without pinning a code | an agent would pick a plausible 4xx and hard-code it | **Accepted assumption** — the ADR's test strategy tests the rejection, not the code; the implementing RED/GREEN pair pins it. Not founder-level; the ADR delegates file names to backend-lead the same way |
| 2 | Mode 1's response-header set beyond `frame-ancestors 'none'` — the ADR names only frame-ancestors, no source directives (§9 MIN-001), no `sandbox` (§4 Neutral 3) | an agent might copy the Mode 2 template to Mode 1 | **Accepted assumption** — Mode 1 emits nothing beyond what the ADR names; any addition is a spec deviation flagged at review |
| 3 | Mode 1 label encoding — the ADR's "e.g. base32 of 16 bytes ≈ 26 chars" is an example | an agent might treat base32/16-bytes as normative | **Accepted assumption** — the grammar (FR-005) is normative; any fresh-entropy hostname-safe encoding meeting it qualifies |
| 4 | New file names (host-dispatch file, `webserve_isolation_policy.go`) | — | **Resolved by the ADR** — "name per backend-lead" (§5, §11) |

No founder questions are outstanding: ADR-094 is Accepted with F794-1…F794-7 folded in, and every acceptance criterion above derives from the ADR.

## Holdout evaluation scenarios (post-implementation — not for development; must NOT be referenced in the TDD plan or traceability matrix)

> Evaluated outside the codebase by the user or a separate evaluator, after development completes. Written so reading the implementation cannot game them.

- **H-1 (Happy Path)** — Setup: a default loopback install, Chromium. Action: have an agent build a small note-taking app with `localStorage` persistence and a login form; open the chat preview link. Expected: the link is a `<label>.localhost` URL; notes survive a reload; logging in inside the preview works.
- **H-2 (Happy Path)** — Setup: the same preview, opened in Safari. Action: click the same chat link. Expected: the `/preview/` URL opens and the app renders and functions (module script bundle, data fetch).
- **H-3 (Happy Path)** — Setup: a fresh conversation with an agent, tool text only. Action: ask the agent to serve a full web app; give no human help. Expected: the agent's built-in browser panel and the user's tab both show a rendering app.
- **H-4 (Error)** — Setup: `gateway.public_url` set to `http://*.example.com`. Action: run web_serve. Expected: a refusal error naming the misconfiguration; no preview URL anywhere.
- **H-5 (Error)** — Setup: a Mode 2 preview open in any engine. Action: from the preview's console, run an authenticated state-changing API call with credentials. Expected: the call is blocked or unauthenticated; the attempted change does not land.
- **H-6 (Edge)** — Setup: a dev-server preview whose upstream 302-redirects with a dot-segment `Location` escaping the prefix. Action: load the preview. Expected: an error page, not the API's response.
- **H-7 (Edge)** — Setup: a preview that renders blank. Action: follow the user docs' troubleshooting note. Expected: the note leads the user to the browser console and names CSP as the likely cause.

## Definition of done

Per the repo's Definition of Done, reachability is checked first and delivery is stated in two lines that are never merged:

- **Code correct and tested**: the RED/GREEN/CHECK cycle of the TDD plan executed — including the browser-level RED on current code (hole and instrument proven), the realistic-bundle fixture on Chromium and WebKit on both serving paths and both modes, the seven CHECK mutations each turning a named test red, and the regression sweep green — with CI as the authority for Go results.
- **Reachable by a user/agent**: SC-001…SC-006 hold — the six reachability bullets of ADR-094 §6, each evidenced (SC-004 is an agent working from tool text alone; SC-005 is the issue's own acceptance test executing in CI).

Rollback is a revert commit — no flag, deliberate for a security fix (ADR-094 §8). Blank-preview diagnosis lives in the user docs' troubleshooting note and the tool text's console guidance; CSP violations are console-only.

## Assumptions

- ADR-094 (Accepted) and its founder-decisions file are the requirements source and the equivalent of an interview-me Decisions Log — per the dispatch; no separate interview artifact exists.
- The two audit-action invariants (SPA deep links, GET-side-effect-free API) hold; they are verified by security-lead and backend-lead respectively before landing (ADR-094 §5), not re-derived here.
- The hosted-version constraint of F794-7 (elicify-ai/omnipus-ai#1177) is outside this repo's implementation scope.
- CLAUDE.md and the shared rules govern anything this spec does not state (gates, contract procedure, reviewer gate, landing rules).

## Clarifications

All founder clarifications are recorded, dated and verbatim-first in [ADR-094-founder-decisions.md](../architecture/ADR-094-founder-decisions.md) (F794-1 … F794-7, 2026-09-26) and folded into ADR-094 §2 and §10; they are referenced from this spec, not restated.

---

*Handoff: this spec goes to grill-spec round 1 (dispatched by squad-lead gateway-security, not by plan-spec). Status stays `Draft` until the grill rounds and founder-visible gates complete.*
