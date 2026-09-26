# Adversarial Review: ADR-094 — Preview isolation specification (issue #798)

**Document reviewed**: `docs/internal/specs/adr-094-preview-isolation-spec.md` (Status: Draft, commit `6f94c849b`)
**Mode**: Spec
**Round**: Round 1 of 2
**Review date**: 2026-09-26
**Verdict**: BLOCK

## Executive Summary

The spec faithfully restates ADR-094's decisions, but in three places it cannot be built as written. Each gap makes a success criterion unreachable. (1) The SPA cannot show a Mode 1 link and has no way to send a Safari user to the Mode 2 link, yet the spec says "no SPA changes". (2) The agent's built-in browser is blocked by the SSRF gate (the check that stops the browser from reaching internal addresses) from opening any `*.localhost` URL. (3) The dev proxy strips every `Cookie` and `Authorization` header, and FR-019 keeps that strip unchanged. At the same time, the spec requires a cookie-backed login through the dev proxy in both modes.

Frontend coverage was **not** as thorough as backend coverage. The spec has no section for UI states, user journey, accessibility or design-system components, because it assumed no SPA change was needed. That assumption is false (CRIT-001, MAJ-009). Several Mode 1 controls are also mis-stated or left out: the global CSRF middleware's ordering, the host-dispatch 404 rule, cookie tossing via longer paths, and the real cross-origin controls. So their tests would either assert the wrong thing or be missing.

This review does not re-open F794-1 to F794-7. Every finding is about how the spec puts the accepted design into practice.

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 13 |
| MINOR | 8 |
| OBSERVATION | 3 |
| **Total** | **27** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The SPA cannot render the Mode 1 link, and nothing sends a Safari user to Mode 2. "No SPA changes" is false

- **Lens**: Reachability; UI states & journey gaps; Contract-first gaps
- **Affected section**: Cluster placement ("No SPA cluster changes"); US-1 AC1/AC3; US-3 AC1; Behavioral contract, boundary condition 3; SC-001; SC-002; FR-001
- **Failure scenario**: backend-lead ships dual URLs in the tool result, exactly as specified. The chat card builds its link through `resolvePreviewHref`, which accepts only a URL whose path matches `/preview/{agent}/{token}/…`. A Mode 1 URL (`http://<label>.localhost:<port>/`) fails that check, so the card falls back to `result.path` and renders the Mode 2 link. SC-001 ("the link is `http://<label>.localhost:<port>/`") is never true for a real user, however green the backend tests are.

  There is a second failure. "Chromium/Firefox/Edge get the Mode 1 URL and everything else the Mode 2 URL" (Behavioral contract), and "WebKit … lands on the Mode 2 URL" (US-3 AC1, SC-002), both need a selection step. The server cannot do it: it mints once and does not know which browser will click. No SPA logic is specified. If the SPA is taught to use the Mode 1 URL, a Safari user who clicks it gets "cannot find server".
- **Evidence**:
  - `src/lib/preview-url.ts::resolvePreviewHref` → `validatePreviewPath` → `PREVIEW_PATH_REGEX = /^\/(?:preview|serve|dev)\/…/`, applied to both `url` and `path`.
  - `src/components/chat/IframePreview.tsx` builds `href` only from `resolvePreviewHref`.
  - The tool result is structured JSON that the SPA parses and that is persisted in transcripts: `pkg/tools/web_serve.go` emits `{"kind":"static","path":%q,"url":%q,"expires_at":%q}` (and a dev variant). The SPA casts it to `src/lib/api/sessions.ts::ServeWorkspaceResult` (`path`, `url`, `expires_at` only; `// not-wire-format` opt-out).
  - The spec's "preview URLs travel in tool-result text" is therefore wrong. They travel in named JSON fields the SPA reads.
- **Recommendation**:
  1. Delete "No SPA cluster changes". Add a frontend section.
  2. Name the new result field(s). For example, `isolated_url` for Mode 1, with `url`/`path` kept as the Mode 2 fallback so old transcripts replay. Architect decides the shape.
  3. State that the `ServeWorkspaceResult`/`RunInWorkspaceResult` opt-out covers them, or move them into `contracts/` (architect's call).
  4. Specify the `resolvePreviewHref` extension: accept `http://<label>.localhost:<port>/` only when the label matches the FR-005 grammar and the port matches the SPA's own port.
  5. Specify how a WebKit user reaches Mode 2 (founder question Q2).
  6. Add BDD scenarios and vitest cases for card rendering with both URLs, with only the fallback, and for an old transcript.

---

#### [CRIT-002] The agent's built-in browser cannot navigate to a Mode 1 URL. The SSRF exception covers only the exact gateway host plus the `/preview/` path

- **Lens**: Reachability; Inconsistency with AS-IS
- **Affected section**: US-6; S-6.1; SC-004; Integration boundaries → "Agent's built-in browser panel"; Symbols involved (no `pkg/security` row)
- **Failure scenario**: The agent calls `serve_web` on a default loopback install and navigates its panel to the primary `http://<label>.localhost:<port>/` URL. `BrowserManager.ValidateURL` calls the SSRF checker. The gateway-origin exception needs `host == gatewayHost` (`localhost`) and a path under `/preview/`. `<label>.localhost` fails both, so the URL falls through to the full SSRF path, resolves to loopback and is blocked.

  The result: the agent cannot review a Mode 1 preview. SC-004 ("renders in the agent's built-in browser panel") fails for Mode 1. The spec says the agent "sees exactly what the user sees", but it will see a different URL, or nothing.
- **Evidence**:
  - `pkg/security/ssrf.go::isAllowedGatewayOrigin`: `hostMatches := host == gwHost`, loopback literals only; `return strings.HasPrefix(extractPath(rawURL), requiredGatewayOriginPathPrefix)` with `requiredGatewayOriginPathPrefix = "/preview/"`.
  - `pkg/tools/browser/manager.go::ValidateURL` calls `m.ssrf.CheckURL`.
- **Recommendation**:
  - Add a requirement and a blast-radius row: `pkg/security/ssrf.go::isAllowedGatewayOrigin` accepts `<label>.localhost:<gwport>` (label per FR-005 grammar, any path) **only** when the configured gateway host is `localhost`.
  - This is a security-focus-area change. It needs a security-lead review and a negative test: `<label>.localhost:<other-port>`, `<label>.example.com`, and a malformed label must stay blocked.
  - Add an integration test: the agent's panel navigates to a Mode 1 URL and renders.
  - Also state which URL the tool text tells the agent to open in its panel.

---

#### [CRIT-003] The dev proxy strips every `Cookie` and `Authorization` header, yet the spec requires cookie-backed login through the dev proxy in both modes

- **Lens**: Inconsistency / contradiction; Infeasibility
- **Affected section**: US-1 AC1 ("an in-app login round-trip works"); S-3.1 ("storage and a cookie-backed login work in both modes"); TDD order 12 ("served both statically and through the dev proxy, in both modes; storage and a cookie-backed login work in both modes"); FR-019 ("dev-proxy `Cookie` strip unchanged"); Integration boundaries → Dev-server upstream ("upstream `Cookie` header stripped — existing behaviour, preserved"); Regression row "Dev-proxy upstream `Cookie` header stripped … keep green"
- **Failure scenario**: The spec contradicts itself. Suppose an agent builds a dev-server app with a server-side session (Express + `express-session`, Django, Next auth). The browser sends the app's cookie, and the Director deletes it before forwarding. The app sees every request as logged out. The same happens to an app using a Bearer header (JWT), because `Authorization` is deleted too.

  Two outcomes follow. Either test order 12 fails on correct code, or backend-lead drops the strip and breaks FR-019 and the "keep green" regression row. F794-1/F794-2 ("full web apps … login") are not met for dev-server previews in either mode. `docs/previews.md` already tells users "Logins inside a development preview do not stick", which contradicts US-1.
- **Evidence**:
  - `pkg/gateway/rest_preview.go::proxyDevRequest` Director: `req.Header.Del("Cookie")`, `req.Header.Del("Authorization")`. Its own comment reads: "a browser-cookie session INSIDE the previewed app does not persist across requests. Deliberate".
  - `docs/previews.md`: "Logins inside a development preview do not stick."
  - ADR-094 §2.1 claims "Cookies and login … ✓" while citing the same strip as "unchanged". The contradiction starts in the ADR.
- **Recommendation**: The founder decides the replacement (Q1). The recommended rule:
  - Replace strip-all with **strip the reserved gateway names only** (`omnipus-session`, `__Host-csrf`, `csrf`), filtered by cookie name within the `Cookie` header.
  - Forward `Authorization` unless it carries the gateway's own Bearer credential.
  - Apply the rule in both modes. In Mode 1 the gateway cookies do not reach the label host anyway, except tossed `Domain=localhost` ones, which the filter removes.
  - Add DS rows: app cookie forwarded; each reserved name removed; a mixed header is filtered correctly.
  - Update FR-019, the regression row and `docs/previews.md`.
  - Architect adds a dated correction to ADR-094 §2.1.

---

### MAJOR Findings

#### [MAJ-001] Where Mode 1 host dispatch sits relative to the global middleware chain is unspecified. As read, the CSRF middleware rejects every Mode 1 app POST

- **Lens**: Incompleteness; Infeasibility
- **Affected section**: FR-004; "Mode 1 host dispatch" constraints; Symbols involved (`gateway.go` / `gateway_boot.go` "dispatch wiring")
- **Failure scenario**: The main handler is wrapped by `configSnapshotMiddleware` and `CSRFMiddleware`. The CSRF middleware exempts by **path prefix** (`/preview/`, webhooks, `/library-preview/`).

  Suppose host dispatch is installed inside that wrap, the natural reading of "before the mux". Then a Mode 1 app's `POST /login` or `fetch('/api/todos', {method:'POST'})` on `<label>.localhost` has no `/preview/` prefix. It is rejected with "csrf cookie missing". Forms and login, both required by F794-2, fail in Mode 1.

  Now suppose dispatch is installed outside every wrap. Then the live `cfg.IsPreviewEnabled()` check (it reads the config snapshot) and the token-redacting rate limiter are lost. The new Fetch-Metadata and Service-Worker guards (FR-010/FR-011) are also unscoped: applied before dispatch, they reject an app's own `/api/v1/…` page navigation on its own host.
- **Evidence**: `pkg/gateway/gateway_boot.go` (the `csrfMW := middleware.CSRFMiddleware(` wrap and the `WrapHTTPHandler(stg.api.configSnapshotMiddleware)` wrap); `pkg/gateway/middleware/csrf.go::defaultExemptPrefixes` (`PreviewPathPrefix`, `WebhookPathPrefix`, `LibraryPreviewPathPrefix`); `pkg/gateway/rest_preview.go::HandlePreview` (`!cfg.IsPreviewEnabled()`).
- **Recommendation**: Add a normative ordering clause:
  1. Host dispatch runs after the config snapshot is available.
  2. It runs before `CSRFMiddleware`. Alternatively, the CSRF middleware exempts requests whose Host is a valid preview Host; architect picks one.
  3. FR-010/FR-011 guards apply only to main-Host requests.
  4. The preview Host applies the same live `IsPreviewEnabled()` check and the same rate limiter.

  Add tests:
  - A Mode 1 POST to `/login` reaches the preview.
  - `preview_enabled=false` makes a Mode 1 Host return 404, hot-flipped with no restart. Extend `preview_listener_hot_flip_test.go`.
  - `Sec-Fetch-Dest: document` to `<label>.localhost/api/v1/page` reaches the preview.

---

#### [MAJ-002] "`/api/v1/*` and `/auth/*` answer 404 under a preview Host" contradicts "the app owns the whole host"

- **Lens**: Inconsistency; Ambiguity
- **Affected section**: FR-004; "Mode 1 host dispatch" first bullet; TDD order 4 ("`/api/v1/*`, `/auth/*` → 404 under a preview Host")
- **Failure scenario**: An agent-built full-stack app exposes `/api/v1/todos` or `/auth/login`, both common. Under Mode 1, S-5.1 promises "the app owns the whole host", but FR-004 and test order 4 require a 404 for those paths. Either the app breaks, or a correct implementation fails order 4.

  ADR §2.2's intent is that the preview Host mux mounts no **gateway** handlers. It does not say those paths are unreachable for the app.
- **Evidence**: Spec FR-004 versus §2.1 of the ADR ("Root-absolute assets ✓ fixed — the app owns the whole host") and S-5.1.
- **Recommendation**:
  - Rewrite FR-004: "Under a preview Host, no gateway handler is reachable. Every path is served by the preview (static file lookup or dev proxy). An unknown label is a 404."
  - Replace test order 4's 404 assertion with this: a dev server answering `/api/v1/x` with a marker returns the marker under the preview Host, and the gateway API handler is provably not invoked. For example, a spy handler, or a request carrying a valid session cookie still gets the marker and never gateway JSON.
  - Keep a static-mode case: `/api/v1/x` with no such file is a 404 from the file server.

---

#### [MAJ-003] Mode 1's security premise is mis-stated, so the test oracle is wrong and the real controls go unpinned

- **Lens**: Security; Testability & false-green risk; Inconsistency with AS-IS
- **Affected section**: US-2 AC2; S-2.2; TDD order 2 ("RED Mode 1 shape … against a stub host dispatch"); Summary ("the preview origin holds no gateway credential")
- **Failure scenario**: A script on `<label>.localhost` runs `fetch('http://localhost:<port>/api/v1/…', {credentials:'include'})`. The `omnipus-session` cookie **is** attached. Host-only limits which *target* host receives the cookie, and the target is `localhost`. `SameSite=Strict` allows the request because `<label>.localhost` and `localhost` are same-site, as ADR §2.2 itself states for cookie tossing. A WebSocket to `ws://localhost:<port>/api/v1/chat/ws` carries the cookie too. ADR §2.2's "WebSocket — the handshake carries no session cookie" is wrong.

  What actually stops #798 in Mode 1 today:
  - `setCORSHeaders` / `isAllowedOrigin` refuse to reflect `http://<label>.localhost:<port>`, so a preflight for the required `X-Csrf-Token` header fails.
  - The CSRF check reads the token only from the header, never from a form field.
  - `wsCheckOrigin` rejects the label Origin.

  None of these is named or tested. A future change that "helpfully" allows `*.localhost` in `isAllowedOrigin` or `wsCheckOrigin` (both already special-case `localhost`) reopens #798 in Mode 1 with every planned test green.

  S-2.2's oracle, "no gateway cookie is attached", is false when the request targets `localhost`, and trivially true when it targets the label host. Test order 2 cannot fail on today's code, so it is not a RED.
- **Evidence**:
  - `pkg/gateway/rest.go::isAllowedOrigin` (reflects configured origin, same Host, or hostname `localhost`/`127.0.0.1` only).
  - `pkg/gateway/websocket.go::wsCheckOrigin` (same shape; shared by the chat and browser WebSockets).
  - `pkg/gateway/middleware/csrf.go::CSRFMiddleware` (`r.Header.Get(CSRFHeaderName)` only).
  - `pkg/gateway/middleware/origin.go`: `RequireMatchingOriginOnStateChanging` is **not wired** for any route, per its own doc comment and the `pkg/gateway/rest.go` registration comment.
- **Recommendation**:
  - Rewrite US-2 AC2 and S-2.2 against the real controls, targeting `http://localhost:<port>` from a `<label>.localhost` page:
    1. A credentialed `POST` with `X-Csrf-Token` is blocked at preflight, and the state change does not land.
    2. A form POST is rejected by CSRF because the header is missing.
    3. A WebSocket upgrade from the label Origin is refused.
    4. A credentialed GET response is unreadable.
  - Add unit cases: `isAllowedOrigin` and `wsCheckOrigin` return false for `http://<label>.localhost:<port>`.
  - Add CHECK mutations: widen `isAllowedOrigin` or `wsCheckOrigin` to accept `*.localhost` → a named test turns red.
  - Replace test order 2 with a RED that fails today. For example, the preview-Host 404-for-gateway-handlers case from MAJ-002 fails today because the main mux serves the SPA under any Host.
  - Architect adds a dated correction to ADR-094 §2.2's WebSocket row.

---

#### [MAJ-004] Cookie tossing with a longer `Path` locks the user out. FR-015 covers CSRF only and specifies no recovery

- **Lens**: Security (DoS); Incompleteness; UI states
- **Affected section**: US-2 AC7; S-2.7; FR-015; Edge case "Cookie tossing on Mode 1"
- **Failure scenario**: A Mode 1 page sets `omnipus-session=x; Domain=localhost; Path=/api/`. Browsers send cookies with longer paths first (RFC 6265 §5.4), so every `/api/…` request carries the tossed cookie ahead of the genuine `Path=/` one. `r.Cookie` returns the first match, so the server resolves garbage and answers 401. The user re-logs in, and the new host-only `Path=/` cookie still sorts second. The result is a login loop until the user clears cookies by hand.

  The same move on `csrf` (plain HTTP, which is Mode 1's only scheme) produces FR-015's rejection, or a mismatch, on every state-changing call. FR-015 names only CSRF, and it specifies what gets rejected but not how the user recovers. The user sees the SPA fail with no explanation.

  ADR §2.2's claim that "RFC 6265 ordering … puts the genuine host-only cookie first" holds only at equal path.
- **Evidence**: `pkg/gateway/middleware/session_cookie.go` (`r.Cookie(SessionCookieName)`; `WriteSessionCookie` `Path: "/"`); `pkg/gateway/middleware/csrf.go::csrfCookieValue` (`r.Cookie(CSRFCookieName)`, then `r.Cookie(CSRFCookieNameHTTP)`). Browser acceptance of `Domain=localhost` from a `.localhost` subdomain is taken from ADR §2.2 and is **Inferred**, not measured here.
- **Recommendation**:
  - Extend FR-015 to duplicate `omnipus-session` occurrences.
  - Specify recovery (founder question Q4). Recommended: on a detected duplicate, the response expires the `Domain=localhost` variants of the reserved names with `Set-Cookie … Domain=localhost; Max-Age=0` across the relevant paths, and returns a typed error the SPA shows with Retry.
  - Add tests: tossed `Path=/api/` session cookie → the request is not authenticated as garbage, and recovery clears it. Tossed CSRF → the same.
  - Architect adds a dated correction to ADR §2.2's ordering sentence.

---

#### [MAJ-005] The label registry is mis-cited, and the label lifecycle (renew, evict, revoke, disable) is not specified

- **Lens**: Incompleteness; Inconsistency with AS-IS
- **Affected section**: Existing codebase context (rows for `preview_token.go::Mint` and `pkg/tools/web_serve.go::DevServerRegistry`); FR-005; FR-006; Integration boundaries → Registry and tokens
- **Failure scenario**:
  - `pkg/gateway/preview_token.go::Mint` is the **Library** preview-token store (ADR-067). A backend-lead following the table extends the wrong store.
  - The web_serve stores are `pkg/agent/served_subdirs.go::ServedSubdirs` (static) and `pkg/sandbox/dev_servers.go::DevServerRegistry` (dev). The static one is not mentioned at all.
  - `ServedSubdirs.Register` **renews in place** when the same directory is re-served. Its comment records the real regression that happened when the token rotated. If the label is minted fresh on renew, every edit-then-re-serve loop 404s the Mode 1 URL the user already opened, and a new origin also wipes the app's storage and login.
  - If the label is not dropped on `Evict`, the janitor's `purgeExpired`, `Unregister`/`UnregisterByAgent` or the `SetOnEvict` callbacks, a dead preview stays reachable by label. That extends a credential past its TTL.
- **Evidence**: `pkg/gateway/preview_token.go` header ("Library preview-token store (ADR-067 stage 1 …)"); `pkg/agent/served_subdirs.go::ServedSubdirs.Register` (renew-in-place comment), `::Evict`, `::purgeExpired`, `::SetOnEvict`; `pkg/sandbox/dev_servers.go::DevServerRegistry.Unregister`, `::UnregisterByAgent`, `::SetOnEvict`; `pkg/tools/web_serve.go` holds `devReg *sandbox.DevServerRegistry` as a field, not a type.
- **Recommendation**:
  - Correct the table.
  - Add FR rules:
    1. The label is minted with the token and **renewed in place** with it.
    2. The label becomes unresolvable on every path that makes the token unresolvable: expiry, evict, replace, unregister, `preview_enabled=false`.
    3. The label↔token map lives in the same store and under the same lock as the token.
  - Add tests for each lifecycle edge, including "re-serve the same directory → same label".

---

#### [MAJ-006] CHECK mutation 1 ("drop `ACAO: *` → the Mode 2 fixture render fails") cannot fail. The measurement behind it was taken under `sandbox`

- **Lens**: Testability & false-green risk; Inconsistency
- **Affected section**: CHECK mutation 1; SC-002; US-3 AC1 rationale ("the review measured it rendering blank today without CORS"); Test order 12
- **Failure scenario**: Mode 2 has no `sandbox` directive, so the document's origin is the gateway origin. Its module script, `crossorigin` stylesheet and `fetch('./data.json')` are **same-origin** CORS-mode requests, and those never need `Access-Control-Allow-Origin`. Remove `ACAO: *` and the fixture still renders, so mutation 1 survives.

  CHECK then either reports a non-detecting test or, worse, someone bends the fixture to make it fail. The review's measurement used `Content-Security-Policy: sandbox allow-scripts; …`, an opaque origin, which is the rejected model. ACAO in Mode 2 now exists only for F794-3's cross-origin reads, not for rendering.
- **Evidence**: `docs/internal/architecture/ADR-094-preview-isolation-model-review.md` "Browser measurement" (fixture CSP starts with `sandbox allow-scripts`). The same-origin CORS behaviour is **Inferred (high)** from the Fetch standard; it was not measured here.
- **Recommendation**:
  - Retarget mutation 1 to test order 9: a cross-origin read from a third origin succeeds with ACAO and fails without it. For example, a page on a different port fetches a prefix file.
  - Drop "renders blank without CORS" from US-3/SC-002's rationale.
  - Architect adds a dated correction to ADR-094 control-stack row 12 and §2.1's module-script row.

---

#### [MAJ-007] FR-010 (reject `/api/v1` requests carrying `Sec-Fetch-Dest: document`) breaks the SPA's own download and open-in-tab navigations

- **Lens**: Inconsistency with AS-IS; Incompleteness (regression)
- **Affected section**: FR-010; US-2 AC4; S-2.4; Regression requirements (no row)
- **Failure scenario**: The Library "Download" button creates an `<a href="/api/v1/library/{ws}/download?…" download>` and clicks it. Chat attachments render `<a href={m.url} download>`. The library API documents its download URL as "Meant for an `<a href download>` or `window.open()`".

  A top-level navigation to these URLs sends `Sec-Fetch-Dest: document`. That covers "open in new tab", a middle-click, and `window.open()`. For the `download`-attribute click itself it is **Unknown**, pending measurement. The guard then rejects the navigation, and downloads stop working for every user in every mode.

  In Mode 2 the preview and the SPA are same-origin, so `Sec-Fetch-Site` cannot tell the SPA's navigation from the preview's. No regression row covers this.
- **Evidence**: `src/lib/api/library.ts::libraryDownloadUrl` (doc comment); `src/components/library/LibraryExplorer.tsx` `handleDownload`; `src/components/chat/ChatScreen.tsx` (`<a … href={m.url} download={m.filename}>`).
- **Recommendation**: Founder question Q3. Whatever is chosen:
  - Add a measured browser test (Chromium, Firefox, WebKit) proving Library and chat downloads still work.
  - Add a regression row.
  - If an exemption list is chosen, list its routes and assert each is a read-only GET.

---

#### [MAJ-008] Host validation against the "listener port" breaks Mode 1 behind port mapping (Docker, reverse proxy)

- **Lens**: Ambiguity; Incompleteness
- **Affected section**: Edge case "Host validation" ("literal `localhost` suffix + listener port"); FR-004
- **Failure scenario**: Docker maps `8080→5000` and `gateway.public_url=http://localhost:8080`, so the canonical origin is `http://localhost:8080` and Mode 1 is minted as `http://<label>.localhost:8080/`. The browser sends `Host: <label>.localhost:8080`, but the listener is on `5000`. The request fails validation, falls to the main mux and serves the SPA or a 404. Mode 1 is dead on every port-mapped install, with no error.
- **Evidence**: The spec's edge-case table; ADR §2.2 normative 3 (same wording). `docs/docker.md` exists, and port mapping is the documented Docker shape (not re-read in detail; **Inferred**).
- **Recommendation**: Validate against the **canonical origin's port** (the same port the label URL was minted with), not the listener port. Add a DS-3 row: `public_url=http://localhost:8080` with the listener on 5000 → Mode 1 minted and dispatched.

---

#### [MAJ-009] The SPA's dev-server warmup probe cannot reach a Mode 1 URL

- **Lens**: UI states & journey gaps; Reachability
- **Affected section**: US-5 AC1; S-5.1; SC-003 (Mode 1 half); no UI-states section
- **Failure scenario**: For dev previews, `IframePreview` polls `fetch(href, {method:'HEAD'})` every 2 s and shows the link as ready only on a 2xx or 3xx. With a Mode 1 `href`, the fetch is cross-origin. The SPA's CSP `connect-src 'self' stun: turn: turns:` blocks it outright, so every Mode 1 dev preview card ends in "Dev server did not respond in time" after 60 s, although the server is up.
- **Evidence**: `src/components/chat/IframePreview.tsx` (`probeOnce`: `fetch(target, { method: 'HEAD' })`); `pkg/gateway/embed.go` (the SPA CSP string `"connect-src 'self' stun: turn: turns:; …"`).
- **Recommendation**: Specify that the probe always targets the **Mode 2** URL (same-origin, the same registration) even when the link shown is Mode 1. Add a UI-states table for the card: waiting / probing / ready / timed-out / URL-unavailable / fallback-only. Update `tests/e2e/iframe-preview-warmup.spec.ts` for a Mode 1 result.

---

#### [MAJ-010] The Mode 1 credential moves from the path into the Host header, and log and audit redaction covers paths only

- **Lens**: Security (information disclosure, repudiation)
- **Affected section**: FR-005; Integration boundaries; no logging or audit requirement
- **Failure scenario**: In Mode 1 the label is the bearer credential: whoever holds the URL gets the preview. `preview_path_redact.go` exists because "any site that records a request path can write a live read credential into a log … or into the HMAC-chained audit record". It redacts **paths** at six inventoried sites.

  A Mode 1 request carries the credential in `Host`, and in `Origin`/`Referer` on the app's own sub-requests. Any access log, audit entry (`auditDevSuccess`, `dev.proxied`) or error log that records Host or full URL writes a live label into the audit chain, which outlives log rotation. The token-bucket rate limiter also "sits directly on the preview prefix", so Mode 1 has none.
- **Evidence**: `pkg/gateway/preview_path_redact.go` (package doc: six recording sites, the build-breaking inventory guard in `preview_path_redact_test.go`); `pkg/gateway/rest_preview.go::proxyDevRequest` (`auditDevSuccess(r, "dev.proxied", …)`).
- **Recommendation**: Add FRs:
  1. Every site that records a Host or full URL redacts a preview label, which extends the redaction inventory and its guard.
  2. Mode 1 requests are audited with the same events as Mode 2, carrying a redacted label.
  3. The preview rate limiter applies per label.

  Add tests that extend `preview_path_redact_test.go`'s inventory.

---

#### [MAJ-011] Frontend structural sections are missing: UI states, user journey, accessibility, design system

- **Lens**: UI states & journey gaps; Accessibility & keyboard; Design-system reuse & brand
- **Affected section**: Whole spec (structural)
- **Failure scenario**: CRIT-001 and MAJ-009 make SPA work unavoidable: dual-link rendering, fallback labelling, a changed warmup target. With no UI-states, journey, accessibility or design-system section, frontend-lead has to invent all of it. Examples: how a second "compatible link" is labelled for screen readers, whether it is a catalogued `Button`/`IconButton` or a raw anchor, and what the card shows for a Mode 2-only deployment. Those are exactly the gaps this gate exists to catch.
- **Evidence**: The spec has no such headings. `src/components/chat/IframePreview.tsx` already uses catalogued `Button`/`IconButton` and the `useUiStore` toasts.
- **Recommendation**: Add these sections:
  - **UI screens and states** for the preview card: Mode 1+2 result, Mode 2-only result, legacy transcript, refused mint (FR-003 error text shown in chat), warmup states.
  - **User journey**: chat → card → open in tab (Chromium/Firefox vs Safari) → blank-render troubleshooting.
  - **Accessibility and keyboard**: the accessible name of each link, the focus order, the copy-link toast.
  - **Design system**: reuse `Button`/`IconButton` and the existing toast (`catalog.json` checked); no new component.

---

#### [MAJ-012] The browser test matrix is contradictory and not wired: "both modes on WebKit", no named Playwright project, and Firefox absent

- **Lens**: Testability & false-green risk; Infeasibility
- **Affected section**: TDD order 12 ("on Chromium **and** WebKit … in **both** modes"); SC-001 ("Chromium or Firefox"); SC-005 ("executes in CI")
- **Failure scenario**:
  - "Mode 1 on WebKit" contradicts the ADR's engine table, where WebKit gets Mode 2.
  - Worse, Playwright's Linux WebKit resolves names through the OS, and Linux runners with systemd-resolved do resolve `*.localhost` (**Inferred, medium**; not measured). A CI WebKit run could pass Mode 1 and "prove" something macOS Safari will not do. It also cannot prove SC-002's "Safari lands on Mode 2".
  - No Playwright project, `testMatch` glob or `tests/e2e/shards.json` group is named. A spec file outside them never runs, which is the false green SC-005 forbids.
  - The existing isolation projects pin `retries: 0` for security assertions. The spec does not require that for the #798 acceptance test.
  - SC-001 names Firefox, but no test runs Firefox for Mode 1.
- **Evidence**: `playwright.config.ts` (the `isolation-chromium` / `isolation-firefox` / `isolation-webkit` projects with `retries: 0`, and `ISOLATION_SPEC_GLOBS`); `tests/e2e/shards.json` (the `preview-isolation` group); `.github/workflows/pr.yml` (the webkit/firefox install).
- **Recommendation**:
  - Split order 12 into two matrices. Mode 1: Chromium and Firefox. Mode 2: Chromium, Firefox and WebKit.
  - Name the spec files and add them to `ISOLATION_SPEC_GLOBS` and the `preview-isolation` shard with `retries: 0`.
  - State that "Safari lands on Mode 2" is proven by the SPA link-selection unit tests (CRIT-001) plus a manual holdout on macOS Safari, not by Linux WebKit.

---

#### [MAJ-013] The Mode 2 redirect rule is not scoped to Mode 2. Applied in the shared proxy, it 502s every Mode 1 redirect

- **Lens**: Ambiguity; Incompleteness
- **Affected section**: FR-013; "Redirect rule (normative)"; SC-003 ("including the upstream-redirect case" in both modes); DS-2 (Mode 2 only)
- **Failure scenario**: `proxyDevRequest` is the one proxy for both modes. FR-013 says "the proxy MUST apply the normative redirect rule", with the in-prefix check against `/preview/{agent}/{token}/`. For a Mode 1 request, a dev server's `302 Location: /login` does not begin with the token prefix. Step 3 then re-roots it under `/preview/…`, which is a wrong URL on a Mode 1 host, or step 4 turns it into a 502. Every Mode 1 login redirect breaks.

  ADR §2.3 says "Mode 1 needs no equivalent rule", but the spec never carries that scoping into an FR. No Mode 1 redirect test exists, although SC-003 claims redirects in both modes.
- **Evidence**: `pkg/gateway/rest_preview.go::proxyDevRequest` (a single `ModifyResponse`); spec FR-013 compared with ADR-094 §2.3's last redirect paragraph.
- **Recommendation**: Rewrite FR-013 as: "Mode 2 requests only". For Mode 1:
  - A same-host redirect passes through.
  - A `Location` whose origin is the gateway's main origin is emitted unchanged, since it is a top-level navigation the host mux does not serve. Alternatively it is refused; architect picks one.

  Add DS-2b Mode 1 rows: `/login` passes; an absolute `http://<label>.localhost:<port>/x` passes.

---

### MINOR Findings

#### [MIN-001] The docs requirement drops F794-7 and does not name the doc to change

- **Lens**: Inconsistency; Incompleteness
- **Affected section**: FR-018; SC-006, compared with US-7 AC3 and S-7.1
- **Failure scenario**: US-7 AC3 and S-7.1 require the F794-7 fallback residual to be documented, but FR-018 and SC-006 list only F794-4 and the troubleshooting note. The docs audit passes without it. `docs/previews.md`, the existing user doc, is not named. Its "Logins inside a development preview do not stick" line becomes wrong once CRIT-003 is fixed, and it never mentions the two URLs.
- **Recommendation**: Add F794-7 to FR-018 and SC-006. Name `docs/previews.md` (and `docs/security.md` if it states preview promises) as the files to update, including the dual-URL explanation.

#### [MIN-002] FR-009 and FR-007 have no asserting test

- **Lens**: Testability
- **Affected section**: Traceability rows FR-009 (orders 6 and 12) and FR-007 (order 4)
- **Failure scenario**: Order 6 tests the Mode 2 builder, and order 12 checks rendering, not headers. Nothing asserts that Mode 1 responses carry exactly `frame-ancestors 'none'`, no source directives and no `sandbox`. Order 4's description has no token↔agent mismatch case.
- **Recommendation**: Add a Mode 1 header-set integration case (static and proxied) and a label→wrong-agent case.

#### [MIN-003] "Gateway origin (any alias)" in the redirect rule is undefined, and it conflicts with FR-016's canonical-only sources

- **Lens**: Ambiguity
- **Affected section**: Redirect rule step 2; FR-016; DS-2 row 7
- **Failure scenario**: One implementer treats "any alias" as the canonical origin only, and another includes `127.0.0.1`, `[::1]` and the LAN IP. DS-2 row 7 then passes for different reasons, and an alias-origin in-prefix redirect is emitted by one build and refused by the other.
- **Recommendation**: Enumerate the alias set (for example, the `previewIsolationOrigins` output) and add one DS-2 row per alias.

#### [MIN-004] The shared origin helper cannot be called from `pkg/tools` because of the import direction

- **Lens**: Infeasibility
- **Affected section**: FR-016; FR-003; Symbols (`previewIsolationOrigins`)
- **Failure scenario**: F794-6's wildcard-host refusal happens at mint time in `pkg/tools/web_serve.go`. `pkg/gateway` imports `pkg/tools`, so web_serve cannot call a `pkg/gateway` helper. Backend-lead either duplicates the check, which breaks "one shared derivation", or discovers the cycle mid-GREEN.
- **Evidence**: `pkg/gateway/central_builtin_registry.go` imports `pkg/tools`; `pkg/tools/web_serve.go` imports `pkg/gateway/middleware` and calls `middleware.CanonicalGatewayOrigin`.
- **Recommendation**: Place `previewIsolationOrigins`, or at least its concrete-host and parse checks, in `pkg/gateway/middleware` next to `CanonicalGatewayOrigin`.

#### [MIN-005] The prefix preflight response is unspecified

- **Lens**: Ambiguity; Security
- **Affected section**: FR-012 ("preflight requests under the prefix MUST be answered")
- **Failure scenario**: An implementer answers with `Access-Control-Allow-Methods: *` and `Allow-Headers: *`, which permits cross-origin non-simple writes to the dev upstream. F794-3 accepted cross-origin **reads** only. Today's preflight allows `GET, HEAD, OPTIONS`.
- **Recommendation**: Pin `Access-Control-Allow-Methods: GET, HEAD, OPTIONS` and no wildcard `Allow-Headers`. Add a test.

#### [MIN-006] The tool's registered name is `serve_web`, and there is no explicit Reachability section

- **Lens**: Reachability; Ambiguity
- **Affected section**: The whole spec ("web_serve tool"); Definition of done
- **Failure scenario**: Agents see and call `serve_web`. The reachability grep in root `CLAUDE.md` for `"web_serve"` would miss the registration, and the tool-text owner may write the wrong name into examples.
- **Evidence**: `pkg/tools/web_serve.go` (`const ToolNameWebServe = "serve_web"`); `pkg/tools/browser/manager.go::errFileSchemeBlocked` ("there is no tool called web_serve").
- **Recommendation**: Use `serve_web` for the agent-facing name. Add a "Reachability" section stating the registration and policy status (existing, unchanged), the screen (the chat preview card, changed per CRIT-001) and the agent panel path (CRIT-002).

#### [MIN-007] Canonical-origin edge cases are missing from DS-3

- **Lens**: Incompleteness
- **Affected section**: DS-3; FR-001
- **Failure scenario**: `http://localhost` (port 80, no explicit port), `http://LOCALHOST:<port>` and `http://localhost.:<port>` each have an unspecified result. An exact string match on `http://localhost:<port>` silently sends them to Mode 2, or builds a malformed label URL.
- **Recommendation**: Add DS-3 rows with expected results: normalise case, reject or handle the trailing dot, and treat an implicit port 80 as Mode 1 with no `:port` in the URL.

#### [MIN-008] The regression table omits existing tests the change touches

- **Lens**: Testability (regression blind spots)
- **Affected section**: Regression requirements
- **Failure scenario**: These tests would go red or silently lose meaning, and nobody is told to update them:
  - `pkg/gateway/preview_listener_hot_flip_test.go`, `preview_csrf_realmux_test.go`, `preview_path_redact_test.go`, `preview_token_revocation_test.go`
  - `tests/e2e/iframe-preview-warmup.spec.ts`
  - `tests/e2e/preview-isolation.spec.ts` and `preview-bundle.spec.ts` (Library, touched by the rename)
  - the `src/lib/preview-url` vitest cases
- **Recommendation**: Add each one with "keep green" or "update" plus the reason.

---

### Observations

#### [OBS-001] The FR-016 rename touches the Library for little web_serve benefit

- **Lens**: Overcomplexity
- **Affected section**: FR-016
- **Suggestion**: Mode 2 uses the canonical origin only, and Mode 1 uses no source list, so web_serve consumes the helper only for F794-6's validity checks. State exactly which outputs web_serve consumes. If it is only the checks, a shared validation function in `middleware` (MIN-004) meets MAJ-005's intent without renaming the Library's source-list builder.

#### [OBS-002] A new label means a new origin, and the app's storage and login are lost

- **Lens**: UI states & journey
- **Affected section**: US-1
- **Suggestion**: Re-serving a *different* directory mints a new label, which is a new origin with empty storage. Say so in `docs/previews.md` so users do not report it as a bug.

#### [OBS-003] A line-number citation, and an off-branch checkout SHA

- **Lens**: Ambiguity
- **Affected section**: Existing codebase context (`rest_workspace.go:85`; "checkout (`b023a7a59`)")
- **Suggestion**: Use `file::symbol` only, per root `CLAUDE.md`. Cite the commit the grep actually ran on.

---

## Structural Integrity

### Variant A: Spec mode (plan-spec output)

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | `Draft` |
| ADR linked (or explicitly stated not needed) | PASS | ADR-094 + founder decisions |
| Contract changes stated first, citing `contracts/` | FAIL | Claims "no wire change", but the tool-result JSON the SPA parses gains fields (CRIT-001); the opt-out is not stated |
| API and data section | PASS (partial) | Machine-verifiable constraints; the registry and lifecycle are wrong or missing (MAJ-005) |
| UI screens and states (loading/empty/error/partial) | FAIL | Absent (MAJ-011) |
| User journey section | FAIL | Only implied by user stories |
| Accessibility and keyboard section | FAIL | Absent |
| Design-system components, catalogue-first | FAIL | Absent |
| Security and user promises section (when touched) | PASS (partial) | Non-behaviours are present; `docs/previews.md` is not named (MIN-001) |
| BDD acceptance scenarios, oracle from spec | PASS (partial) | S-2.2's oracle is false (MAJ-003); CHECK mutation 1 is unfalsifiable (MAJ-006) |
| Traceability table: requirement -> scenario -> test | PASS (partial) | FR-009/FR-007 have no asserting test (MIN-002) |
| Reachability section (tool policy / screen wiring) | FAIL | Only as "Definition of done"; the SPA and agent-panel paths are unreachable (CRIT-001, CRIT-002) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Frontend component states | The preview card with Mode 1+2, Mode 2-only and legacy results; warmup against the Mode 2 URL | S-1.1, S-1.3, S-5.1 |
| Mode 1 real controls | CORS preflight refusal, CSRF header-only, WebSocket origin refusal from a label Origin | S-2.2 |
| Middleware ordering | A Mode 1 POST passes CSRF; FR-010/FR-011 not applied to preview Hosts | S-1.1, S-2.4 |
| Lifecycle | Label renew-in-place, evict, expire, `preview_enabled` hot-flip | S-1.3 |
| Negative (DoS) | Longer-path tossed session and CSRF cookies; recovery | S-2.7 |
| Regression | Library and chat downloads under FR-010 | S-2.4 |
| Agent panel | SSRF allows a Mode 1 label, and nothing else | S-6.1 |
| Mode 1 redirects | Same-host redirect passes | S-5.2 |
| Engine matrix | Mode 1 on Firefox; `retries: 0` isolation project | S-1.1, S-3.1 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| DS-1 labels | Uppercase letters (DNS is case-insensitive — does the mux fold case?) | Add "`ABC` = `abc` → same registration" or "uppercase is rejected"; pick one |
| DS-2 redirects | Mode 1 rows; alias-origin rows | See MAJ-013, MIN-003 |
| DS-3 origins | Implicit port 80, case, trailing dot, port-mapped `public_url` | See MIN-007, MAJ-008 |
| New DS (proxy header filter) | App cookie forwarded; reserved names removed; `Authorization` handling | See CRIT-003 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Mode 1 host dispatch | ok | ok | risk | risk | risk | ok | Label in Host not redacted or audited (MAJ-010); no per-label rate limit |
| Mode 1 → main-origin requests | risk | ok | ok | ok | risk | risk | Controls unpinned (MAJ-003); cookie-tossing lockout (MAJ-004) |
| Mode 2 control stack | ok | ok | ok | ok | risk | ok | FR-010 breaks legitimate downloads (MAJ-007) |
| Dev proxy | ok | ok | ok | risk | ok | ok | The header-filter replacement (CRIT-003) must still keep gateway cookies away from the upstream |
| Agent panel SSRF exception | ok | ok | ok | ok | ok | risk | Extension must stay exact: label grammar + gateway port only (CRIT-002) |

**Legend**: risk = identified threat not mitigated in the document, ok = adequately addressed or not applicable

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | Yes (existing; unchanged) | `pkg/tools/web_serve.go` `ToolNameWebServe = "serve_web"`; the spec never states it (MIN-006) |
| User-facing: named screen/component renders it? | **No for Mode 1** | `src/lib/preview-url.ts::resolvePreviewHref` rejects non-`/preview/` URLs (CRIT-001) |
| Agent panel can open it? | **No for Mode 1** | `pkg/security/ssrf.go::isAllowedGatewayOrigin` (CRIT-002) |
| Test plan describes execution, not just authorship? | Partly | SC-005 says "executes in CI", but no project, shard or `retries: 0` is named (MAJ-012) |

---

## Unasked Questions

1. What exact JSON field names carry the Mode 1 URL, and does the fallback stay in `url`/`path` for replay? (CRIT-001; architect decides the shape)
2. Does the preview Host fold label case, given that DNS is case-insensitive and browsers lowercase hostnames?
3. Which recording sites (access log, audit, error log) see `Host`, and does the redaction guard's inventory grow? (MAJ-010)
4. Where exactly in the `gateway_boot.go` wrap chain does host dispatch sit? (MAJ-001)
5. Which file do the new browser specs live in, and which Playwright project runs them? (MAJ-012)
6. Is the `Sec-Fetch-Dest` value of a `download`-attribute anchor click measured on all three engines? (MAJ-007)

---

## Questions for the founder

**Q1 — The dev-server preview login (CRIT-003).**
*Context and impact.* You decided previews must run full web apps with login (F794-1/F794-2). Today, though, the gateway deletes every cookie and every `Authorization` header before a request reaches an agent's development server. That was a deliberate safety choice, and the user docs say "logins inside a development preview do not stick". The accepted design (ADR-094) keeps that deletion "unchanged" and also promises login. Both cannot be true. Any app with a server-side login (Express, Django, Next.js) stays logged out in both preview modes.
*Options.*
- **(A) Delete only Omnipus's own cookies** (`omnipus-session`, `csrf`, `__Host-csrf`), and the gateway's own Bearer credential. Forward the app's cookies and headers, in both modes. **Recommended**: it keeps the original safety goal (the app never sees your Omnipus login) and makes app logins work.
- **(B)** Forward app cookies only on the subdomain mode (Chrome/Firefox on localhost). Keep delete-everything on the `/preview/` path. Safari, LAN and remote users keep broken dev-server logins.
- **(C)** Keep delete-everything. Dev-server apps cannot hold a cookie login. This contradicts F794-1/F794-2 and must be documented.

Answer as "Q1 A/B/C".

**Q2 — How a Safari user reaches the working link (CRIT-001).**
*Context and impact.* On a default install, Chrome and Firefox can open the new isolated subdomain link. Safari cannot: it shows "can't find server". The server cannot know in advance which browser will click. So the chat card must decide, and today it can only show the old `/preview/` link.
*Options.*
- **(A) Show both links on the card**: "Open preview" (subdomain) as the main button, and a secondary "Open in Safari / compatible mode" link (`/preview/`), with one line of help text. **Recommended**: it is predictable, works in every browser, and needs no browser detection.
- **(B)** Detect the browser in the SPA and show only the matching link. One link, but browser detection is brittle (Safari on iOS, Chromium forks, future Safari support).
- **(C)** Show only the subdomain link, and document "use Chrome/Firefox or copy the fallback from the agent's message". Simplest, but it breaks the Safari promise in SC-002.

Answer as "Q2 A/B/C".

**Q3 — The new "no page navigation to the API" guard would break downloads (MAJ-007).**
*Context and impact.* To stop a preview from steering the browser to Omnipus's internal API, the design rejects any page navigation to `/api/v1/…`. But Omnipus's own Library "Download" button and chat attachment downloads navigate to `/api/v1/…` download addresses. On the `/preview/` path the preview and Omnipus share an address, so the server cannot tell which one is navigating. As written, downloads (at least "open in new tab") stop working for everyone.
*Options.*
- **(A) Exempt a short, named list of read-only download and media addresses from the guard**, each proven to change nothing. **Recommended**: downloads keep working. The guard still covers every other API address. A navigation cannot read the file back to the preview anyway.
- **(B)** Change the SPA to download via background requests instead of navigation. The guard stays absolute, but this is more SPA work and very large files are held in memory.
- **(C)** Drop the navigation guard and rely only on the rule that no API read ever changes anything. Less protection in depth.

Answer as "Q3 A/B/C".

**Q4 — Recovery from a preview that plants cookies to lock you out (MAJ-004).**
*Context and impact.* A page on the subdomain mode can plant look-alike Omnipus cookies that the browser sends ahead of your real ones. It cannot take over your account, but it can make Omnipus reject every action and log you out in a loop until you clear your browser cookies by hand. The spec plans to detect this and reject, but says nothing about getting you out of it.
*Options.*
- **(A) Detect it, automatically delete the planted cookies in the same response, and show "Omnipus cleared cookies set by a preview — please retry"**. **Recommended**: the lockout lasts one click.
- **(B)** Detect and reject only, and document "clear cookies for localhost" in troubleshooting.
- **(C)** Do not detect it. Accept the lockout risk as part of the documented subdomain residuals.

Answer as "Q4 A/B/C".

---

## Verdict Rationale

**BLOCK.** CRIT-001 and CRIT-002 mean the feature as specified is unreachable in its primary mode. The chat card cannot show the Mode 1 link, and the agent's panel is refused by the SSRF gate. SC-001 and SC-004 would fail even with every planned test green, which is the false-delivery pattern the repo's Definition of Done exists to stop. CRIT-003 is a direct contradiction between a preserved security behaviour (FR-019) and a P0 acceptance criterion (US-1, S-3.1). The founder must choose the replacement rule (Q1).

The MAJOR set clusters around Mode 1's integration with existing gateway machinery: middleware ordering, host-dispatch semantics, port validation, the label lifecycle, redaction, and mis-stated controls with the wrong test oracles. It also includes one unfalsifiable CHECK mutation (MAJ-006) and one regression the control stack would cause (MAJ-007).

Before round 2 the spec must:
1. Add the SPA/frontend work and the result-field shape.
2. Add the SSRF extension.
3. Resolve the dev-proxy header rule.
4. Specify the Mode 1 middleware placement and FR-004 semantics.
5. Fix the registry citations and lifecycle.
6. Retarget the invalid tests.

Several findings also expose factual errors in ADR-094 itself: the §2.1 login and module-script rows, the §2.2 WebSocket row and cookie-ordering sentence, and control-stack row 12. Those need dated corrections by the architect. They are not grounds to re-open F794-1 to F794-7.

### Recommended Next Actions

- [ ] Founder interview on Q1–Q4 (squad-lead) before the fix round
- [ ] Architect: decide the result-field shape and the opt-out/contract status (CRIT-001); dated corrections to ADR-094 §2.1, §2.2, §2.3 row 12 (CRIT-003, MAJ-003, MAJ-004, MAJ-006)
- [ ] Spec author: add the frontend sections and the SPA requirements (CRIT-001, MAJ-009, MAJ-011)
- [ ] Spec author: add the SSRF exception requirement with negative cases (CRIT-002)
- [ ] Spec author: rewrite FR-004, FR-013 and FR-015; add the middleware-ordering, port-validation, lifecycle and redaction FRs (MAJ-001, 002, 004, 005, 008, 010, 013)
- [ ] Spec author: fix the test plan (MAJ-003, MAJ-006, MAJ-012, MIN-002, MIN-008) and the traceability rows

### Next step in the process

```
Verdict: BLOCK

Review written to: docs/internal/specs/adr-094-preview-isolation-spec-review.md

This is grill round 1 of 2 (fixed). Next: team-lead interviews the
founder on "Questions for the founder", then the spec author fixes
round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the
corrected spec at docs/internal/specs/adr-094-preview-isolation-spec.md — regardless of
this round's verdict.
```
