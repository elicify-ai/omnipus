# ADR-094 — Preview isolation specification (issue #798)

- **Decision record:** [ADR-094 — Preview isolation with full capability — two serving modes](../architecture/ADR-094-preview-isolation-model.md) (Status: Accepted, 2026-09-26, with the 2026-09-26 spec-review correction note)
- **Founder decisions:** [ADR-094-founder-decisions.md](../architecture/ADR-094-founder-decisions.md) — F794-1 … F794-7 (authoritative); plus the four round-1 grill decisions Q1–Q4 (2026-09-26, Decisions Log below). This spec invents no new acceptance criteria — every criterion derives from the ADR or from Q1–Q4.
- **Round-1 review:** [adr-094-preview-isolation-spec-review.md](adr-094-preview-isolation-spec-review.md) — verdict BLOCK (3 CRITICAL, 13 MAJOR, 8 MINOR, 3 OBSERVATION). Every finding is addressed in this revision; the disposition table is at the bottom.
- **Issue:** #798 — Preview pages can call the authenticated API as the logged-in user (same-origin residual) (`security` / `priority:P1-high` / `area:gateway`)
- **Inputs:** ADR-094 §2 (two serving modes), §5 (blast radius), §6 (reachability), §8 (test strategy), §11 (affected components); ADR-094 review (2026-09-26); the 2026-09-26 spec-review correction note atop ADR-094
- **Status:** Draft (plan-spec output; round-1 findings fixed; for grill-spec round 2)
- **Date:** 2026-09-26 (fix round applied same day)

## Summary

Today a served preview runs same-origin script with the user's full gateway credentials (issue #798). ADR-094 rebuilds web_serve preview serving in two modes: **Mode 1**, a self-generated per-preview subdomain of the gateway host (`<label>.localhost:<port>`) served by Host dispatch inside the single binary, and **Mode 2**, the unchanged `/preview/` path with full capability (no `sandbox` directive anywhere) and a server-side control stack. Mode selection happens at web_serve mint time from the canonical gateway origin; misconfigured origins fail closed (F794-6).

This revision adds what round 1 proved missing. **The SPA is in scope**: web_serve returns both URLs in one tool result (new `isolated_url` field beside the existing `path`/`url`), and the SPA picks the one link that will work in the user's browser — Mode 1 on Chromium-family/Firefox, Mode 2 on everything else (Q2). The agent's built-in browser gains SSRF admission for the Mode 1 label host class (`pkg/security/ssrf.go::isAllowedGatewayOrigin`, exact host class and negative set specified) so the panel can open the primary URL (CRIT-002). The dev proxy strips only Omnipus's own credentials and forwards the app's cookies and headers unchanged (Q1), which is what makes in-app login real in both modes. Mode 1's real cross-origin barriers — CORS origin-reflection refusal, header-only CSRF, and the WebSocket origin check — are named and pinned with tests, not mis-stated as cookie absence (MAJ-003). The navigation guard exempts a short, named list of read-only download/media addresses so downloads keep working (Q3), and a planted look-alike cookie is detected, cleared in the same response, and reported to the user with a retry message (Q4).

## Contract-first status

`contracts/openapi.yaml` and `contracts/asyncapi.yaml` gain **no delta** — `S-7.2` stays green. The dual-URL result travels inside the **tool-result payload**, which is not a wire contract: the SPA casts it from the opaque `ToolCall.result` field into `src/lib/api/sessions.ts::ServeWorkspaceResult` / `RunInWorkspaceResult`, both carrying the `// not-wire-format` opt-out. The new `isolated_url` field rides that same opt-out (FR-022). If the architect later prefers moving the tool-result shape into `contracts/`, that is a separate decision — this spec does not make it, and `make verify-contracts` must stay green either way.

## Existing codebase context

*GitNexus MCP tools are not connected in this headless spec-writing session. Per the shared rules, no impact run is claimed; the impact table is **Inferred** (from ADR-094 §5/§11, whose citations were verified by the architect, plus the grep sweep below).* Verified this session by grep in this checkout (`2129aa458`):

| ADR-cited symbol | Verified present | Role |
|---|---|---|
| `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` | ✅ | Today's wide preview CSP (`'self'`, `unsafe-inline`, no `sandbox`) — retired for preview responses by this feature |
| `pkg/gateway/rest_workspace.go::resolveMainOrigin` | ✅ | Third-origin computation — retired for preview responses by this feature |
| `pkg/gateway/rest_workspace.go::workspaceContentType` | ✅ (map var) | MIME map gaining `.mjs`/`.woff`/`.woff2`/`.wasm` |
| `pkg/gateway/library_isolation_policy.go::libraryIsolationOrigins` | ✅ | Origin-list derivation (canonical + loopback aliases; fails closed on non-concrete hosts) — **stays unrenamed** (round-1 OBS-001: web_serve consumes only the validity checks, not this source list) |
| `pkg/tools/web_serve.go::previewOriginUnresolvedResult` | ✅ | Existing fail-closed path for an empty canonical origin — extended to all F794-6 refusal triggers |
| `pkg/agent/served_subdirs.go::ServedSubdirs` | ✅ | **Static** preview registration store (web_serve's `t.served`): `Register` renews in place on same-directory re-serve, `Evict`, `purgeExpired` janitor, `SetOnEvict` |
| `pkg/sandbox/dev_servers.go::DevServerRegistry` | ✅ | **Dev** registration store (web_serve holds it as the `devReg` field): `Register`, `Unregister`, `UnregisterByAgent`, `SetOnEvict`, `sweepExpired` janitor |
| `pkg/gateway/preview_token.go::Mint` | ✅ (method) | **Library** preview-token store (ADR-067 stage 1) — *not* a web_serve store; 43-char base64url tokens stay the Mode 2 credential |
| `pkg/gateway/rest_preview.go::addPreviewCORSHeaders` / `::handleServePreviewPreflight` | ✅ | CORS emission extended to prefix-wide `ACAO: *`; preflight today answers `GET, HEAD, OPTIONS` |
| `pkg/gateway/rest_preview.go::proxyDevRequest` | ✅ | Gains the credential filter (Q1) in its Director and the Mode 2-scoped redirect rule in its response-modification step |
| `pkg/gateway/rest_preview.go::neutralizeReservedSetCookies` | ✅ | Existing reserved-`Set-Cookie` neutralization — preserved unchanged |
| `pkg/gateway/rest_preview.go::reservedGatewayCookieNames` | ✅ | The reserved set: `omnipus-session`, `csrf`, `__Host-csrf` — the credential filter and the planted-cookie rule key off this same set |
| `pkg/gateway/middleware/session_cookie.go::WriteSessionCookie` | ✅ (no `Domain` attribute set) | Host-only session cookie — Mode 1's load-bearing control |
| `pkg/gateway/middleware/csrf.go::CSRFMiddleware` | ✅ (header-only token read: `r.Header.Get(CSRFHeaderName)`, never a form field) | Mode 1's second load-bearing control |
| `pkg/gateway/rest.go::isAllowedOrigin` | ✅ | CORS reflection check — refuses `http://<label>.localhost:<port>` today (hostname equals `localhost`/`127.0.0.1` only); a future "helpful" widening is pinned shut by test |
| `pkg/gateway/websocket.go::wsCheckOrigin` | ✅ (shared by chat and browser WebSockets) | WebSocket origin check — refuses the label Origin today; pinned shut by test |
| `pkg/security/ssrf.go::isAllowedGatewayOrigin` | ✅ | Gateway-origin SSRF exception — today exact gateway host + `/preview/` path only (`::requiredGatewayOriginPathPrefix`); gains the Mode 1 label host class (CRIT-002) |
| `pkg/security/ssrf.go::CloneWithGatewayOrigin` | ✅ | Browser-dedicated checker clone; wired at `pkg/agent/loop_wire.go` (the `browserSSRF` wiring, hardcoded `("localhost", cfg.Gateway.Port)`) — the shared singleton gains nothing |
| `pkg/tools/web_serve.go::ToolNameWebServe` | ✅ (`const ToolNameWebServe = "serve_web"`) | The agent-facing tool name is `serve_web` |
| `pkg/tools/web_serve.go` result JSON | ✅ | `{"kind":"static","path","url","expires_at"}` / `{"kind":"dev",…,"command","port","_summary"}` — the SPA-parsed tool-result payload gaining `isolated_url` |
| `src/lib/preview-url.ts::resolvePreviewHref` | ✅ (`::validatePreviewPath`, `::PREVIEW_PATH_REGEX`) | Today rejects any URL whose path is not `/preview|serve|dev/{agent}/{token}/…` — extended to accept the Mode 1 URL under the FR-023 rule |
| `src/components/chat/IframePreview.tsx` | ✅ (4 `noopener` occurrences; `probeOnce` HEAD poll) | Opener severed on every preview link — preserved; warmup probe re-targeted to the Mode 2 URL (FR-025) |
| `src/lib/library-attachment.ts::mediaRefURL` | ✅ | Builds `/api/v1/media/…` and `/api/v1/media/workspace/…` — the chat attachment download targets exempted from the navigation guard (Q3) |
| `src/lib/api/library.ts::libraryDownloadUrl` | ✅ | Builds `/api/v1/library/{ws}/download?path=…` for `LibraryExplorer.tsx::handleDownload`'s `<a download>` — the Library download address exempted from the navigation guard (Q3) |
| `pkg/gateway/gateway.go` / `gateway_boot.go` | ✅ (wrap order: CSRF outermost → configSnapshot → mux; `WrapHTTPHandler` stacks outermost-last) | Host-dispatch wiring point; ordering clauses in FR-028 |

### Symbols involved

| Symbol | Role | Change |
|---|---|---|
| `pkg/tools/web_serve.go::Execute` | mints preview URLs from the canonical origin | mode selection from ADR-094 §2.4; dual URLs (`isolated_url`) when Mode 1 applies; `previewOriginUnresolvedResult` extended to wildcard-host `public_url`, unparseable-origin, and trailing-dot-origin refusals |
| `pkg/tools/web_serve.go::Description` | agent-facing tool text (`serve_web`) | **owned by prometheus-prompt-engineer**: both modes, Mode 2 asset rule, console-check guidance |
| new `pkg/gateway` host-dispatch file (name per backend-lead) | preview-Host mux | label-grammar validation (lower-cased Host), 404 for an unknown label, reuses the preview registry lookup; mounts exactly one handler |
| new `pkg/gateway/webserve_isolation_policy.go` (name per backend-lead) | Mode 2 CSP builder + tripwires | literal template of ADR-094 §2.3, boot-frozen origin list, percent-encoded prefix |
| `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` / `::resolveMainOrigin` | today's preview CSP and third-origin computation | retired for preview responses (remaining callers untouched — ADR-094 §5) |
| new `pkg/gateway/middleware` origin-validity helper (name per backend-lead, next to `::CanonicalGatewayOrigin`) | shared parse/wildcard/emptiness validity checks | **placed in `middleware`** because `pkg/gateway` imports `pkg/tools` and `pkg/tools/web_serve.go` already imports `pkg/gateway/middleware` — a `pkg/gateway` home would be an import cycle (round-1 MIN-004/OBS-001) |
| `pkg/gateway/rest_preview.go` CORS pair | reflects main-origin only today | emits `ACAO: *` under the prefix; deletes upstream `ACAO`/`ACAC` before overriding; preflight method set pinned (FR-012) |
| new Fetch-Metadata / Service-Worker middleware guard | — | rejects `Sec-Fetch-Dest: document` on main-Host `/api/v1` **except the named read-only exemption list (Q3)**; refuses `Service-Worker: script` under `/preview/` |
| `pkg/gateway/rest_preview.go::proxyDevRequest` | dev-server proxy | credential filter (Q1); normative 4-step `Location` rule **Mode 2 only** (FR-013); upstream `ACAO`/`ACAC` deletion in `ModifyResponse` |
| `pkg/security/ssrf.go::isAllowedGatewayOrigin` | SSRF exception for the agent's panel | gains the Mode 1 label host class, exact rule per FR-021 |
| `pkg/gateway/preview_path_redact.go` / `pkg/gateway/pathredact` | path redaction for logs/audit | extended to label-aware Host/full-URL redaction; the build-breaking inventory guard extends (FR-026) |
| `pkg/gateway/gateway.go` / `gateway_boot.go` | mux wiring | dispatch wiring per FR-028's ordering clauses |

### Impact assessment (Inferred — no GitNexus in this session)

| Symbol modified | Risk | Dependents that must be re-tested |
|---|---|---|
| `buildWorkspaceCSP` retirement for previews | **HIGH** — every preview response's CSP changes | `rest_preview_test.go`, `preview_iframe_test.go` (frame-ancestors assertions change to `'none'`), `rest_preview_proof_test.go` |
| `isAllowedGatewayOrigin` label-host extension | **HIGH** — a security focus area (`pkg/security`); security-lead reviews | `pkg/security/ssrf_test.go` (ADR-073's `TestGatewayOriginException_ScopedToPreviewPath` keeps its bare-host scope), the singleton-isolation tests, `pkg/agent` browser-SSRF wiring tests |
| `proxyDevRequest` Director credential filter | **HIGH** — replaces a deliberate strip-all (the strip-all test intentionally flips) | `rest_preview_test.go` (the strip-all assertions are **rewritten**, not kept green), e2e dev-proxy specs |
| `resolvePreviewHref` extension + link selection | MEDIUM — SPA link rendering changes shape | `src/lib/preview-url` vitest cases (updated), `IframePreview` component tests, warmup e2e spec |
| `libraryIsolationOrigins` — **no rename** | LOW — Library surface untouched by this revision (round-1 OBS-001 adopted) | Library preview tests keep green unchanged |
| `resolveMainOrigin` retirement for previews | MEDIUM — remaining callers keep today's behaviour | callers listed in ADR-094 §5; grep sweep at implementation time |
| `previewOriginUnresolvedResult` extension | MEDIUM — fail-closed triggers widen | `rest_preview_web_serve_e2e_test.go`, `tests/e2e/web-serve-canonical.spec.ts`, `tests/e2e/web-serve-malformed.spec.ts` |
| `workspaceContentType` additions | LOW — additive map entries | static-serving tests |
| `Execute` mode selection + `isolated_url` | MEDIUM — tool-result payload changes shape (opt-out covered) | web_serve tool-text tests; `Description` text (prometheus-prompt-engineer) |

### Relevant execution flows

| Flow | Relevance |
|---|---|
| web_serve mint → tool result → SPA link selection → user opens link in tab | mode selection decides the URL shape; the SPA picks the one link that works in the detected engine (Q2) |
| web_serve mint → agent navigates built-in browser panel | the browser-dedicated SSRF clone admits the label host class (CRIT-002); the panel is a real Chrome that enforces the served headers — the agent sees exactly what the user sees |
| `/preview/` request → static serve or dev-proxy | CSP template emission, CORS, guards, credential filter, Mode 2-scoped redirect rule |
| preview page → fetch/form/navigation/SW attempt → `/api/v1` | the control stack (ADR-094 §2.3 rows 1–12) plus Mode 1's three real controls are what stop this |
| planted duplicate reserved cookie on any request → response clears it | detection → same-response `Set-Cookie` clears → typed retry error (Q4) |

### Cluster placement

Gateway surface cluster (`pkg/gateway`, `pkg/security`, `pkg/tools/web_serve.go`) **plus an SPA slice** (`src/lib/preview-url.ts`, `src/components/chat/IframePreview.tsx` and the chat tool-result card) — round-1 CRIT-001 put the SPA in scope, overriding ADR-094 §2.5's "No SPA changes required" line (see Flags for the architect). Also spans agent tool text (`serve_web` `Description`, prometheus-prompt-engineer) and user docs (`docs/previews.md`). No `contracts/` delta (see Contract-first status).

### Available reference patterns

Not applicable — `docs/reference/go-implementation/` does not exist in this repository. The pattern source for this feature is ADR-067's Library isolation machinery (`pkg/gateway/library_isolation_policy.go`), whose measured origin-derivation logic and fail-closed host-shape checks this feature reuses via the shared middleware validity helper — never re-implements.

## Decisions Log

All decisions dated 2026-09-26. F794-1…F794-7 live in `ADR-094-founder-decisions.md` (authoritative, referenced not restated). The round-1 grill (verdict BLOCK) raised four founder questions; the founder answered; three security-lead tightenings on CRIT-002 follow.

| # | Decision | Wording (founder, verbatim as relayed by squad-lead gateway-security) | Settles |
|---|---|---|---|
| Q1 | Dev-proxy credential filter, both modes | "**A** — the dev-proxy Director strips ONLY Omnipus's own credentials: Cookie pairs named exactly `omnipus-session`, `csrf`, `__Host-csrf`, and an `Authorization: Bearer` token matching one of the gateway's own registered secrets. Everything else (including any cookies/headers the previewed app itself sets) forwards unchanged, in BOTH modes." | CRIT-003; supersedes ADR-044 FR-013's request-side strip-everything (per the ADR's correction note item 2) |
| Q2 | SPA shows only the link that works (option B) | "**B** — the SPA detects the browser and shows ONLY the one link that will work there (not both links)" — with (a) feature detection preferred over UA sniffing, naming the API or stating plainly why sniffing is necessary; (b) a defined uncertain-detection fallback; (c) a test per browser family, explicitly including Safari/WebKit. | CRIT-001; puts the SPA in scope despite ADR-094 §2.5's "no SPA changes required" |
| Q3 | Navigation guard exempts named read-only download/media addresses (option A) | "**A** — exempt a short named list of read-only download/media addresses from the navigation (`Sec-Fetch-Dest: document`) guard on `/api/v1`." The list is named in FR-010 with a safety argument per entry. | MAJ-007 |
| Q4 | Planted cookie: detect, clear in the same response, tell the user to retry (option A) | "**A** — detect a planted look-alike cookie (a `Cookie` header carrying more than one value for a reserved gateway cookie name, or one at a path that shadows the real one per RFC 6265 ordering), delete the planted duplicate(s) in the same response (a `Set-Cookie` clearing them at the shadowing path), and show the user a 'please retry' message." | MAJ-004 |
| F-1 | SSRF admission is deployment-agnostic by construction (security-lead ruling) | The ADR correction note's condition (a) ("gateway host is exactly localhost") does not actually gate Mode-1-only deployments — the browser-dedicated checker clone is wired with a hardcoded `localhost` host (`pkg/agent/loop_wire.go` `browserSSRF`), so the label class is admitted wherever the grammar and port match, on any deployment shape. The **real** Mode-1-vs-Mode-2 gate is the label registry/host mux: non-Mode-1 deployments mint no labels, so every label-class URL 404s via the empty registry. Spec'd accordingly in FR-021; ADR wording flagged for the architect. | CRIT-002 tightening |
| F-2 | The preview-host mux lower-cases `Host` before registry lookup (security-lead ruling) | `FOO.LOCALHOST:<port>` and `foo.localhost:<port>` must resolve to the same registry entry — DNS is case-insensitive and the SSRF checker already lower-cases its host compare (`pkg/security/ssrf.go::isAllowedGatewayOrigin` does `strings.ToLower`). Normative in FR-005; positive + consistency test in the TDD plan. | CRIT-002 tightening |
| F-3 | CRIT-002 negative-test set (security-lead ruling) | Seven named negatives folded into the GREEN/CHECK plan (DS-6): the ADR-073 hole re-attempted through the new host class; trailing-dot; portless; upper-case positive+consistency; non-http(s) scheme out of scope; singleton isolation; empty label. | CRIT-002 tightening |

## User stories and acceptance criteria

### US-1 — A full web app renders with storage and login on the default loopback install (Mode 1) (P0)

*As the user, I want a preview of an agent-built web app to open as a complete application on its own origin, so that storage-backed apps and in-app logins work exactly as they would outside Omnipus (F794-1, F794-2).*

**Why P0:** the founder rejected every model that sandboxes capability away; this is the primary serving mode wherever the browser self-resolves `*.localhost`.
**Independent test:** serve a storage-and-login web app on a default loopback install, open the chat preview link in Chromium or Firefox, use it as an app.

1. **Given** a default loopback install (canonical origin `http://localhost:<port>`) and Chromium or Firefox, **When** the user opens the web_serve preview link from chat (the card shows the Mode 1 link, per US-8), **Then** the link is `http://<label>.localhost:<port>/`, the app renders as a full web app, `localStorage`/`sessionStorage`/IndexedDB persist across a reload, `document.cookie` works for the previewed app, and an in-app login round-trip works — the dev-proxy credential filter (Q1, FR-020) is what makes the login persist through the proxy in either mode.
2. **Given** a Mode 1 preview, **When** the previewed app sets cookies or makes same-origin requests, **Then** it operates on its own origin's cookie jar and storage — no gateway cookie is present on the preview origin and none is set by the preview path.
3. **Given** a mint where Mode 1 applies, **When** the tool result is read, **Then** it carries **both** URLs — the Mode 1 primary in the new `isolated_url` field and the unchanged `/preview/` fallback in `url`/`path` — so WebKit users and non-loopback deployments keep a working link, and old transcripts replay unchanged (FR-022).

### US-2 — A served preview cannot call the authenticated API as the user (issue #798 acceptance) (P0)

*As the user, I want script in a served preview document to be unable to perform authenticated state-changing `/api/v1/*` requests as me, so that opening a preview my own agent produced cannot change my configuration, agents, credentials references or tool policy.*

**Why P0:** this is the issue's own acceptance criterion; the whole feature exists for it.
**Independent test:** the issue's acceptance test, executed in CI on both paths (ADR-094 §8 RED): script in a served preview attempting an authenticated state-changing API call fails, while ordinary rendering still works.

1. **Given** today's code (RED, before implementation), **When** script in a served `/preview/` document reads `document.cookie` for the CSRF value and POSTs to `/api/v1/…` with the echoed header, **Then** the request succeeds and a state change lands — proving both hole and instrument (ADR-094 §8 RED; dev-proxy path included, not only static).
2. **Given** Mode 1 after implementation, **When** a page at `http://<label>.localhost:<port>/` attempts authenticated calls to `http://localhost:<port>/api/v1/…` while the browser holds `Domain=localhost` gateway cookies, **Then** every state-changing attempt is stopped by the three real Mode 1 controls — **not** by cookie absence (the cookies are same-site and DO ride such requests): a credentialed POST carrying `X-Csrf-Token` is blocked because its CORS preflight is refused (`isAllowedOrigin` returns false for the label origin, so `ACAO` is never reflected); a form POST is rejected by the header-only CSRF gate (the token is read from `X-Csrf-Token` only, never a form field); a WebSocket upgrade from the label Origin is refused (`wsCheckOrigin` returns false); and a credentialed GET's response is unreadable (no `ACAO` reflection).
3. **Given** Mode 2, **When** the page attempts `fetch`/XHR/`EventSource`/`sendBeacon` to `/api/v1/*` at any nesting, **Then** the request is blocked by CSP source confinement (control-stack row 1); a form POST to `/api/v1/*` at any nesting is blocked by confined `form-action` (row 2).
4. **Given** Mode 2, **When** a top-level navigation GET targets `/api/v1/*` carrying `Sec-Fetch-Dest: document`, **Then** the server rejects the request (row 3) **except** for the named read-only exemption list (Q3, FR-010); **When** a `/preview/` request carries `Service-Worker: script` or `Sec-Fetch-Dest: serviceworker`, **Then** it is refused (row 4) — a service worker could otherwise outlive the page.
5. **Given** Mode 2, **When** the page iframes the SPA or accesses `window.opener`, **Then** both are already severed: SPA responses carry `frame-ancestors 'none'` (row 5) and every preview link carries `noopener noreferrer` (row 6).
6. **Given** Mode 2, **When** an upstream 302 bounces a subresource or fetch toward `/api/v1`, **Then** the redirect rule applies and any `Location` resolving outside the token prefix becomes a 502 with no `Location` (row 8).
7. **Given** a previewed app (Mode 1 tossed `Domain=localhost` cookies, or Mode 2 same-origin `document.cookie` at a shadow path) that plants a duplicate of a reserved gateway cookie name, **When** a request arrives whose `Cookie` header carries more than one occurrence of a reserved name, **Then** the gateway rejects state-changing requests, clears the planted duplicates in the same response, and the user sees the retry message — the lockout lasts one click (Q4, FR-015).

### US-3 — The Mode 2 fallback renders fully on WebKit and non-loopback deployments (P0)

*As the user on Safari/WebKit, an IP-literal address, a LAN/real-domain/Tailscale deployment or HTTPS ingress, I want the `/preview/` fallback to render modern agent-built bundles completely, so that previews work everywhere Mode 1 cannot (F794-5, F794-7).*

**Why P0:** everywhere outside Mode 1's narrow window, Mode 2 is the only preview surface.
**Independent test:** the realistic-bundle fixture renders on Chromium, Firefox and WebKit through the `/preview/` path, served both statically and through the dev proxy.

1. **Given** WebKit/Safari on a loopback install, **When** the same preview is opened (the card shows the Mode 2 link, per US-8), **Then** it lands on the Mode 2 URL and renders — module scripts, `crossorigin` stylesheets and `fetch('./data.json')` all succeed. These are same-origin CORS-mode requests and need no `Access-Control-Allow-Origin` to render; the prefix-wide `ACAO: *` exists for F794-3's cross-origin reads, not for rendering (round-1 MAJ-006).
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

### US-5 — Dev-server previews render in both modes; redirects stay confined where same-origin applies (P1)

*As the user, I want dev-server previews to render through the proxy in both modes, with Mode 2's redirects confined to the token prefix, so that proxy serving is as safe and as working as static serving (ADR-094 §6; round-1 MAJ-013).*

**Why P1:** the proxy path is the second serving path; its Mode 2 redirect rule is normative and its failures are the ones a string-prefix check misses.
**Independent test:** serve the bundle through a dev server with scripted redirect responses; assert in-prefix rewriting and out-of-prefix refusal in Mode 2, and pass-through in Mode 1.

1. **Given** a dev-server preview in Mode 1, **When** the app renders, **Then** it renders natively on its own host (root-absolute assets and HMR work — Mode 1 fixes them), and its own POST routes (`/login`, `/api/todos`) reach the dev server untouched by the CSRF gate (FR-028).
2. **Given** a dev-server preview in Mode 2, **When** the upstream responds 301/302/303/307/308 with a `Location` that resolves (WHATWG-parsed) to a gateway alias origin and begins with the token prefix, **Then** it is emitted; a root-relative `Location` is re-rooted under the prefix; anything else becomes a 502 with no `Location`.
3. **Given** a dev-server preview in Mode 2, **When** the upstream responds 304, **Then** it passes untouched (dev servers send it routinely); **When** a `Location` appears on a non-redirect status, **Then** it is deleted.
4. **Given** a dev-server preview in Mode 1, **When** the upstream responds 301/302/303/307/308 with any `Location` — same-host root-relative, absolute to the label host, or absolute to the gateway's main origin — **Then** it is emitted **unchanged**: the app owns its host's navigation (the CSRF-free Host dispatch serves the app's own routes), and a navigation away to the main origin exposes nothing a typed URL would not (F794-4's phishing residual already covers imitation); no redirect ever re-enters a gateway handler with preview authority.

### US-6 — An agent produces a rendering preview unaided (P0)

*As the agent, I want the web_serve tool text to teach me both modes and the Mode 2 asset rule, so that following it alone — no human help — I produce a preview that renders in my built-in browser panel and in the user's tab (ADR-094 §6).*

**US-6 Why P0:** the repo's Definition of Done is reachability; a preview mode the agent cannot use unaided is not a feature.
**Independent test:** an agent following the tool text alone serves a full web app; the preview renders in the built-in browser panel and the user's tab.

1. **Given** the updated tool text, **When** an agent follows it alone to serve a full web app, **Then** the preview renders in the agent's built-in browser panel — which requires the SSRF label-host admission (FR-021) so the panel can open the Mode 1 URL — and in the user's tab via the link the SPA selected (US-8).
2. **Given** Mode 2, **When** the agent builds the app, **Then** the tool text taught it the asset rule (assets relative or under the token prefix, e.g. Vite `base: './'`) and to check the browser console in the built-in panel when a preview renders blank.

### US-7 — The accepted residuals are documented (P1)

*As the user, I want the security residuals I am living with named in user-facing docs, so that an informed operator knows what a preview can and cannot do (F794-4, F794-7).*

**Why P1:** the founder accepted these residuals on the condition they be documented; documentation is part of the acceptance.
**Independent test:** read the user docs cold; find both residuals and the troubleshooting step without asking a developer.

1. **Given** the shipped user docs, **When** a reader looks for preview limitations, **Then** they find the F794-4 phishing residual (an agent-built page can imitate an Omnipus login screen at the real address) named plainly.
2. **Given** a blank preview, **When** the reader follows the troubleshooting note, **Then** they are told to check the browser console (CSP violations are console-only) — drafted by the implementing lead, audited by docs-verifier.
3. **Given** the Mode 2 fallback, **When** its residual is described, **Then** the F794-7 same-origin popup-scripting residual is documented (hosted-version constraint tracked in elicify-ai/omnipus-ai#1177, outside this repo's scope).

### US-8 — The chat card shows the one link that works in the user's browser (Q2) (P0)

*As the user, I want the preview card to show only a link that works in my browser, so that clicking it never lands me on "can't find server" or a degraded mode my browser did not need.*

**Why P0:** founder decision Q2 (option B); the dual-URL mint is useless if the card shows the wrong one.
**Independent test:** vitest over the selection rule per engine family; Playwright proves the rendered link per engine matrix (Mode 1: Chromium + Firefox; Mode 2: Chromium, Firefox, WebKit); a manual macOS Safari holdout confirms end-to-end.

1. **Given** a Mode 1 mint (`isolated_url` present) opened in Chromium, Edge or Firefox, **When** the card renders, **Then** it shows **only** the Mode 1 link — no second link is rendered (Q2: "ONLY the one link").
2. **Given** a Mode 1 mint opened in Safari/WebKit or any engine the detection cannot place in a `*.localhost`-resolving family, **When** the card renders, **Then** it shows **only** the Mode 2 fallback link — the fail-safe choice, because Mode 2 renders on every engine while Mode 1 resolves only where RFC 6761 `*.localhost` handling exists.
3. **Given** a Mode 2-only mint (`isolated_url` absent or empty) or a legacy transcript (no `isolated_url` field at all), **When** the card renders, **Then** it shows the Mode 2 fallback link — unchanged from today's behaviour.
4. **Given** a mint whose `isolated_url` is malformed (wrong host shape, label failing the FR-005 grammar, or a port different from the SPA's own listener port), **When** the card renders, **Then** the SPA treats it as absent and shows the Mode 2 fallback link (client-side re-validation, never a blind render).

## UI screens and states

One screen is touched: the **chat preview card** (`src/components/chat/IframePreview.tsx` and its tool-result block). No new screen, no route change.

| State | Trigger | Card shows |
|---|---|---|
| Mode 1 + Mode 2 mint, resolving engine | result carries non-empty `isolated_url`, engine detected as Chromium-family or Firefox | the Mode 1 link only ("Open preview") |
| Mode 1 + Mode 2 mint, non-resolving engine | `isolated_url` present, engine is WebKit/unknown | the Mode 2 fallback link only |
| Mode 2-only mint | `isolated_url` absent/empty (non-loopback, HTTPS, IP-literal, real-domain deployments) | the Mode 2 fallback link only — today's rendering |
| Legacy transcript | result predates the feature (no `isolated_url` field) | the Mode 2 fallback link only — replay unchanged |
| Malformed `isolated_url` | client-side re-validation fails (FR-024) | the Mode 2 fallback link only |
| Warmup — waiting | dev preview registered, server not yet answering | current "starting dev server" state, probe running |
| Warmup — ready | probe returns 2xx/3xx | link enabled |
| Warmup — timed out | probe budget exhausted | current "Dev server did not respond in time" message — but the probe now targets the Mode 2 URL (FR-025), so a live Mode 1 dev server never falsely times out |
| Refused mint | web_serve returned an error (F794-6 triggers) | the error text renders in chat as today (no card, no link) |

## User journey

1. Agent serves a full web app (`serve_web`); the tool result carries both URLs (Mode 1 mint) or one (Mode 2-only).
2. The chat card renders the one link for the user's engine (US-8).
3. The user clicks. Chromium/Firefox on loopback land on `http://<label>.localhost:<port>/` and use the app as an app — storage, cookies, login, forms, popups, downloads.
4. A Safari user lands on `http://localhost:<port>/preview/{agent}/{token}/` — full render, F794-3 cross-origin reads work, control stack active.
5. If a preview renders blank (Mode 2), the tool text and `docs/previews.md` point the user at the browser console.
6. If the user's engine is neither family, the fallback link still works — no dead end anywhere.

## Accessibility and keyboard

- Each card link carries an **accessible name**: the Mode 1 link is "Open preview" (plus the app name when available); the Mode 2 fallback is "Open preview (compatible mode)" when it is the rendered link because of the engine rule. The name states the destination behaviour, not the mechanism ("compatible mode" — never "Mode 2").
- The link is a real `<a href>` with `target="_blank"` and `rel="noopener noreferrer"` (preserved) — keyboard-activatable, middle-clickable, context-menu "copy link" works for free.
- Focus order is unchanged: the card keeps its position in the message flow; the link is reachable by Tab in DOM order; a visible focus ring per the design system's `focus-visible` ownership rule.
- State changes on the card (warmup ready / timed out) surface through the existing toast (`useUiStore` toasts, aria-live region) — no new live region is invented.
- No colour-only signal distinguishes link states; the warmup states keep their existing text + icon affordances (Phosphor icons, decorative, `aria-hidden`).

## Design system

Before touching anything under `src/components/`, load the `omnipus-design-system` skill (root `CLAUDE.md`). Constraints for this feature:

- **Catalogue-first, no new component.** The card's link/button reuse the catalogued `Button` / `IconButton` (already used by `src/components/chat/IframePreview.tsx`); the retry surface for the planted-cookie message (Q4) reuses the existing toast, not a new banner component; the warmup states reuse the card's existing progress affordances.
- **Tokens only.** Any spacing, colour, type size or shadow in the changed card markup comes from the existing token set; no raw hex, no new OKLCH names (the Tailwind v4 colour-name trap is a red build).
- **Icons:** Phosphor, decorative use stays `aria-hidden`.
- The design-system CI gate (catalogue checks, manifest, Storybook static build) must pass with the changed card; a new catalogued-component *use* needs no manifest change, only the existing publication contract.
## Edge cases

| Case | Expected |
|---|---|
| Valid label shape, no live registration | 404 — an attacker-resolvable label is worthless (ADR-094 §2.2) |
| Label grammar boundaries | 1–63 chars, letters/digits/hyphens, no leading/trailing hyphen are valid; 64 chars, leading/trailing hyphen, `_` (the current token shape) are invalid |
| Label case | Labels are minted lower-case; the mux lower-cases the incoming `Host` before registry lookup, so `FOO.LOCALHOST:<port>` routes to the same registration as `foo.localhost:<port>` (F-2). Upper-case input is never minted. |
| Host validation | exact match against grammar + literal `localhost` suffix + the **canonical origin's port** (the port the label URL was minted with — the public_url port, not the listener port; round-1 MAJ-008); anything else falls to the main mux unchanged. An absent port equals the canonical origin's port (implicit 80). |
| Port-mapped deployment (`public_url=http://localhost:8080`, listener on 5000) | Mode 1 minted with `:8080`; a request with `Host: <label>.localhost:8080` dispatches to the preview even though the listener port differs (DS-3 row 8) |
| Canonical origin `http://localhost` (implicit port 80) | Mode 1, URL minted **without** `:port` (`http://<label>.localhost/`). Known limitation: the agent's built-in browser cannot open a portless label URL — the SSRF checker requires an explicit port and fails closed (F-3 negative 3); the user's tab is unaffected. Documented in `docs/previews.md` is not required (agent-panel-only). |
| Canonical origin `http://LOCALHOST:<port>` | case-normalized — Mode 1, label URL minted lower-case (DS-3 row 9) |
| Canonical origin `http://localhost.:<port>` (trailing dot) | **refuse** (fail-closed per F794-6's spirit: ambiguous hostname forms are not guessed) (DS-3 row 10) |
| Hostile agent ID (`;`, `,`, spaces, quotes — `validation.EntityID` admits them) | percent-encoded to the RFC 3986 unreserved set in CSP sources; no directive split |
| Redirect `Location` forms (Mode 2) | dot-segments, `%2e`, protocol-relative `//host/…`, backslash `/\host`, absolute-same-origin (`http://127.0.0.1:5000/api/v1/x` — the Director does not rewrite `Host`) all resolve outside the prefix → 502, no `Location` |
| Mode 1 redirects | any `Location` passes through unchanged — the app owns its host's navigation (US-5 AC4, FR-013) |
| `304 Not Modified` | passes untouched (dev servers send it routinely) |
| `Location` on a non-redirect status | deleted (Mode 2; Mode 1 passes the response through) |
| Upstream `Set-Cookie` for reserved gateway cookie names | neutralized (existing behaviour, preserved) |
| Upstream `Content-Security-Policy`/`X-Frame-Options` | stripped (existing behaviour, preserved); a `<meta http-equiv="Content-Security-Policy">` in agent HTML can only tighten — multiple policies intersect |
| Upstream `ACAO`/`ACAC` on proxied responses | deleted before the prefix-wide override |
| Planted duplicate reserved cookie (either mode) | detection on the `Cookie` header shape → clears ride the same response; state-changing requests get the typed retry error (Q4, FR-015) |
| `[::1]` IPv6-literal reader | today's documented limitation unchanged: the CSP cannot name it, rendering degrades with the existing Library-style WARN (ADR-094 §4 Neutral 4) |
| Pre-16.4 Safari (no Fetch Metadata headers) | the navigation-guard layer is absent on such engines; mitigated by the GET-side-effect-free invariant (audit action), the named read-only exemption list being the only `/api/v1` document-navigations allowed, and CSRF gating only state-changing methods — accepted, ADR-094 §2.3 row 3 |
| SPA deep links | invariant "no SPA route performs a state change from URL parameters on load" — spec requirement; audited before landing |
| Preview embedded in an iframe | `frame-ancestors 'none'` on preview responses in both modes; embedding a preview inside the SPA needs its own analysis — out of scope (ADR-094 §4 Neutral 3) |
| Agent panel vs port-80 install | the panel cannot open a portless Mode 1 URL (SSRF requires an explicit port); the user's tab can — accepted limitation, see the `http://localhost` row above |

## Behavioral contract

Primary flows:
- When a default loopback install mints a preview, the system returns the full app's URL `http://<label>.localhost:<port>/` **and** the `/preview/` fallback in one tool result, and the SPA renders exactly the one link the detected engine can open.
- When a non-Mode-1 deployment or engine mints or opens a preview, the system serves the full app on the unchanged `/preview/` path with the confined control stack.
- When the previewed app uses storage, cookies, login, forms, popups or downloads, the system leaves it unimpaired in both modes — no `sandbox` directive exists in the web_serve model, and the proxy forwards the app's own cookies and headers (only Omnipus's reserved credentials are stripped).
- When the agent's built-in browser opens a Mode 1 URL, the system admits the label host class at the SSRF layer and the panel renders what the user sees.

Error flows:
- When the canonical origin is misconfigured (wildcard-host `public_url`, unparseable, empty, trailing-dot), the system refuses to mint — no preview URL, no degraded policy.
- When script in a served preview attempts an authenticated state-changing API call, the system blocks it — by the three real Mode 1 controls (CORS reflection refusal, header-only CSRF, WS origin check) or by the control stack (Mode 2).
- When an upstream redirect resolves outside the token prefix in Mode 2, the system replaces the response with a 502 and no `Location`.
- When a valid label has no live registration, the system answers 404 — including every label-class URL on a non-Mode-1 deployment (the registry is empty there; the SSRF admission is deployment-agnostic by construction).
- When a request carries a planted duplicate of a reserved gateway cookie, the system clears the duplicates in the same response and returns the typed retry error on state-changing requests.

Boundary conditions:
- When `https://localhost`, an IP literal, a real domain or a Tailscale name is configured, the system serves Mode 2 only.
- When the `Host` is anything other than a valid `<label>.localhost(:canonical-port)` or the gateway's own host, the system behaves exactly as today (main mux).
- When the user's engine is Chromium-family or Firefox, the card shows the Mode 1 link; when it is WebKit, unknown, or the mint is Mode 2-only, it shows the Mode 2 link — never both (Q2).

## Explicit non-behaviors and safeguards

### Qualitative prohibitions

- The system must not reintroduce a `sandbox` CSP directive into the web_serve model, because F794-1/F794-2 require full capability (storage APIs throw under `sandbox` — review-measured).
- The system must not converge web_serve on the ADR-067 opaque-origin sandbox model, because the founder rejected it (round-1's Decision, F794-1).
- The system must not degrade to a `'self'`-based fallback policy under any trigger, because F794-6 makes every misconfigured origin a refusal.
- The system must not touch the Library (`/library-preview/`) or mail (`/mail-preview/`) surfaces' CSP, opaque-origin or no-redirect machinery, because their payloads are untrusted content and no founder decision changes them (ADR-094 §2.5).
- The system must not change preview tokens, TTLs, revocation, ADR-044's consolidation (one listener, deleted config keys, `gateway.preview_enabled` hot-flip), or add config keys — all unchanged scope (ADR-094 §2.5). The SPA's opener-severing UX (`rel="noopener noreferrer"` on every preview link) is unchanged; what changes is which single link the card renders (Q2).
- The system must not add a `contracts/` change: the dual-URL result rides the tool-result payload's existing `// not-wire-format` opt-out (see Contract-first status) — `make verify-contracts` stays green.
- The system must not rely on closing the Mode 2 popup-scripting residual with a header, because no header stack can (F794-7 accepts it; the hosted version must not rely on the fallback — tracked in elicify-ai/omnipus-ai#1177).
- The system must not silently widen a CSP source (bare origin instead of prefix, extra scheme), because the tripwires treat the literal template as the oracle.
- The system must not widen `isAllowedOrigin` or `wsCheckOrigin` to admit `*.localhost`, because that would reopen issue #798 in Mode 1 with every planned test green (round-1 MAJ-003) — pinned shut by unit tests and a CHECK mutation.
- The system must not let the SSRF label-host admission grow beyond FR-021's exact class (never a wildcard `*.localhost` accept, never a second label, never another port), because a hostile resolver answering for `evil.localhost` would then go unexamined (ADR-094 correction note item 1).
- The system must not apply the Mode 2 redirect-rewrite rule to Mode 1 responses, because the in-prefix check would 502 every Mode 1 login redirect (round-1 MAJ-013).
- The system must not show both links on the card in any state, because Q2 chose exactly one (the fallback-only states are not "both").

### Machine-verifiable constraints

**Serving modes and URL shapes**
- When the canonical origin is `http://localhost:<port>` (or `http://localhost` implicit 80), web_serve MUST return both `http://<label>.localhost:<canonical-port>/…` (primary) and the `/preview/` fallback URL in one tool result.
- When the canonical origin is `https://localhost:<port>`, an IP literal, a real domain or a Tailscale name, web_serve MUST return the `/preview/` URL only.
- When the canonical origin is a wildcard-host `public_url`, unparseable, empty, or has a trailing dot (`http://localhost.:<port>`), web_serve MUST refuse with an error result and mint no URL.

**Mode 1 host dispatch**
- A request whose `Host` is `<label>.localhost(:canonical-port)` (label matches the grammar, lower-cased) MUST reach a preview-host mux that mounts **exactly one handler**: preview serving (static file lookup or dev-proxy forward). **No gateway handler is reachable under a preview Host** — no API, no auth, no SPA. In dev mode every path forwards to the upstream (an app exposing its own `/api/v1/todos` or `/auth/login` is served by the app, and the gateway API handler is provably not invoked); in static mode a path with no corresponding file is a **file-server 404**, not a gateway response. An unknown label is a 404. (This is the ADR §2.2 normative-1 intent — "fail-closed by construction" — read precisely; the ADR's literal "`/api/v1/*` and `/auth/*` answer 404" sentence is imprecise for dev mode and is flagged for the architect.)
- Labels MUST match the grammar: letters, digits, hyphens; 1–63 chars; no leading or trailing hyphen; minted **lower-case** from fresh entropy in a hostname-safe encoding; mapped to the same registry entry as the Mode 2 token. The mux MUST lower-case the incoming `Host` before the registry lookup (F-2).
- A valid-shaped label with no live registration MUST answer 404.
- The agent for a label MUST be resolved from the registration (the token↔agent mismatch rule preserved); the Mode 2 token remains the Mode 2 credential.
- Dispatch ordering (FR-028): the CSRF gate MUST NOT run on preview-Host requests; the preview-host mux MUST see a live config snapshot (its `IsPreviewEnabled()` check is hot-flip, no restart); the preview-host mux MUST apply the same preview rate limiter the `/preview/` prefix uses, **per label**; the Fetch-Metadata and Service-Worker guards (FR-010/FR-011) apply to **main-Host** requests only (a Mode 1 app's own `/api/v1/page` document navigation reaches the app). Whether backend-lead achieves this by reordering the two `WrapHTTPHandler` calls in `gateway_boot.go`, by a preview-Host exemption inside `middleware.CSRFMiddleware`, or by a dispatch outside the CSRF wrap that re-applies the snapshot + limiter itself is an implementation choice — the five FR-028 tests pin the behaviour either way.

**Mode 1 credential filter (Q1, both modes)**
- In the dev proxy's request direction, the `Cookie` header MUST be filtered: only pairs whose name is exactly `omnipus-session`, `csrf` or `__Host-csrf` (case-sensitive, the `reservedGatewayCookieNames` set) are dropped; every other pair forwards unchanged; the header is omitted entirely when nothing remains. This applies identically in Mode 1 and Mode 2 — one code path, no mode-conditional behaviour.
- The `Authorization` header MUST be deleted only when it is `Bearer` with a token matching one of the gateway's own registered secret values (the credential store injected at boot — `pkg/gateway/CLAUDE.md`, "Credential boot order"); any other value forwards unchanged.
- In the response direction nothing changes: `neutralizeReservedSetCookies` keeps dropping upstream `Set-Cookie` for the reserved names (anti-fixation), and the previewed app's own `Set-Cookie` now survives to its next request — which is what makes its login persist.
- The FR-010 threat context (the app reading the operator's session/CSRF values) stays closed by name-scoping instead of header deletion.

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
- Mode 1 responses carry `frame-ancestors 'none'` and no source directives (ADR-094 §9 MIN-001 disposition, §4 Neutral 3) and no `sandbox` directive; the exact remaining header set stays minimal to what the ADR names. Asserted by a dedicated header-set integration case, static and proxied.
- A main-Host `/api/v1` request carrying `Sec-Fetch-Dest: document` MUST be rejected before reaching the API handler, **except** for the exemption list in FR-010 (the exact status code is an implementation choice the ADR leaves open — pinned by the implementing RED/GREEN pair; the requirement is that the request never reaches the handler).
- A `/preview/` request carrying `Service-Worker: script` or `Sec-Fetch-Dest: serviceworker` MUST be refused (same status-code note).
- Every successful response under the token prefix, static and proxied, MUST carry `Access-Control-Allow-Origin: *` and MUST NOT carry `Access-Control-Allow-Credentials`; on proxied responses any upstream ACAO/ACAC MUST be deleted before the override. Preflight requests under the prefix MUST be answered with `Access-Control-Allow-Methods: GET, HEAD, OPTIONS` exactly and **no wildcard `Access-Control-Allow-Headers`** — F794-3 accepted cross-origin *reads* only, and a wildcard would permit cross-origin non-simple writes to the dev upstream (round-1 MIN-005).
- Redirect rule (normative, ADR-094 §2.3, **Mode 2 only**): on status ∈ {301, 302, 303, 307, 308}, the system MUST resolve `Location` against the request URL with WHATWG parsing (dot-segments and `%2e` normalised); emit it when the result's origin is a gateway alias origin (the alias set: the canonical origin plus, when the canonical origin is loopback, `127.0.0.1:<port>` and `localhost:<port>` on the same port — `::1` is deliberately excluded, it is CSP-inexpressible; this is `libraryIsolationOrigins`' loopback branch, cited not renamed) and its path begins with `/preview/{agent}/{token}/`; else, when the raw value was root-relative, re-root it under the token prefix and apply the same check; otherwise replace the response with HTTP 502 and no `Location`. A `Location` header on any non-redirect status MUST be deleted. `304 Not Modified` MUST pass untouched. **Mode 1 responses pass every `Location` through unchanged** (US-5 AC4).
- The MIME map MUST answer `.mjs` → `text/javascript`, `.woff`/`.woff2` → `font/*`, `.wasm` → `application/wasm` (the review verified these are absent today — their absence blocks module scripts under `nosniff`).
- On any request whose `Cookie` header carries **more than one occurrence of a reserved gateway cookie name** (exact name match against `omnipus-session` / `csrf` / `__Host-csrf`), the system MUST: treat that credential read as failed for the request; and on the response emit `Set-Cookie` clear lines (empty value, `Max-Age=0`, past `Expires`) for the affected reserved name at the request path, every ancestor directory of the request path, and `/` — in **both** the `Domain=localhost` form (the Mode 1 toss vector) and the host-only form (the Mode 2 `document.cookie` shadow-path vector). State-changing requests additionally get the typed error (below). The rule is mode-independent: the request shape carries no mode marker, and a same-origin Mode 2 page can plant host-only duplicates exactly as a Mode 1 page plants `Domain=localhost` ones. Bounded: at most 2 × (request-path depth + 1) clear lines per affected name.
- The typed error for a detected planted duplicate on a state-changing request MUST be the gateway's standard JSON error envelope with a machine-readable code (`planted_cookie_cleared`) and the human message "Omnipus cleared cookies set by a preview — please retry"; the SPA surfaces it through its standard error rendering with a Retry action (Q4).
- One shared origin-**validity** derivation MUST serve the mint-time fail-closed checks of all three preview surfaces: a helper in `pkg/gateway/middleware` next to `::CanonicalGatewayOrigin` (importable by `pkg/tools` — `pkg/gateway` imports `pkg/tools`, so a `pkg/gateway` home is an import cycle), performing the parse / wildcard-host / emptiness / trailing-dot checks. The Library's `libraryIsolationOrigins` stays **unrenamed** — its source-list output is not consumed by web_serve (round-1 OBS-001 adopted; the rename FR is dropped). Per-surface policy templates and their tripwires stay separate; the third-origin computation is retired for preview responses; Mode 2 CSP sources use the canonical origin only (no loopback-alias widening).

**Tool result, SPA selection, warmup (Q2, CRIT-001)**
- The tool result payload MUST gain one field: `isolated_url` — the absolute Mode 1 URL `http://<label>.localhost:<canonical-port>/` when Mode 1 was minted; absent or empty otherwise. `path` and `url` keep today's Mode 2 fallback meaning in every mint. Both existing variants (`static`, `dev`) gain it; `dev`'s other fields are unchanged. Old transcripts (no field) replay as Mode 2-only.
- `src/lib/preview-url.ts::resolvePreviewHref` MUST accept `isolated_url` and return it as the href only when all hold: its host is exactly one grammar-valid label + `.localhost` (lower-cased, no second dot, no other suffix), its port equals the SPA's own listener port (`window.location.port`), its scheme is `http`, and the engine rule below selects Mode 1. Any failure ⇒ it resolves to the `url`/`path` fallback exactly as today.
- The SPA engine rule (Q2 option B): Mode 1 link iff `isolated_url` is present, valid per the rule above, **and** the detected engine resolves `*.localhost`. Detection is by feature detection first: `navigator.userAgentData` (the Chromium platform API — present in Chromium-family including Edge, absent in Safari and Firefox) ⇒ Chromium family. Firefox is the one Mode-1-capable engine with no detection API, so for it **UA-string sniffing is necessary** — stated plainly, not hidden: the UA token `Firefox/` is the only remaining signal, and Firefox's UA-freeze keeps that token stable. Every other engine — Safari/WebKit, unknown UAs, detection failures — gets the Mode 2 link, the fail-safe choice because Mode 2 renders everywhere.
- The dev-server warmup probe MUST always target the Mode 2 URL (`url`/`path` — same-origin, same registration) even when the rendered link is Mode 1: the SPA's own CSP `connect-src 'self' …` blocks a cross-origin HEAD to a label host, so probing the Mode 1 URL would falsely time out every Mode 1 dev preview (round-1 MAJ-009).
- The `serve_web` tool text MUST teach both modes, the Mode 2 asset rule (assets relative or under the token prefix) and the console-check guidance — owned by prometheus-prompt-engineer; an agent following it alone MUST be able to produce a rendering preview (ADR-094 §6).

**SSRF admission for the agent's panel (CRIT-002, F-1/F-2/F-3)**
- `pkg/security/ssrf.go::isAllowedGatewayOrigin` MUST admit `http(s)://<label>.localhost:<gwPort>/<any path>` if and only if **all** of: (a) a gateway origin is configured whose host is exactly `localhost`; (b) the URL's literal, pre-resolution host is one label followed by exactly `.localhost` — no second dot, no other suffix; (c) the label, lower-cased, matches the FR-005 grammar exactly; (d) the port equals the configured gateway port. The bare gateway host keeps ADR-073's `/preview/` path scope untouched; the `127.0.0.1`/`::1` loopback-literal equivalence is **not** extended to the label class; non-http(s) schemes are out of scope for the label class (the existing scheme-agnostic handling of the bare host is unchanged).
- The admission is **deployment-agnostic by construction** (F-1): the browser-dedicated checker clone is wired with a hardcoded `localhost` host (`pkg/agent/loop_wire.go` `browserSSRF`), so the label class passes the SSRF layer wherever the grammar and port match, regardless of deployment shape. The real Mode-1-vs-Mode-2 gate is the label registry and the host mux: non-Mode-1 deployments mint no labels, so every label-class URL 404s via the empty registry. A test MUST prove a label-class URL 404s via empty registry on a non-Mode-1 deployment.
- Layering (unchanged from the ADR correction note): the SSRF grant is network reachability for the host class only; per-label authorization stays at the gateway host mux (unknown label ⇒ 404); guessing a label is guessing ~2^128 of mint entropy. The wiring stays the existing `CloneWithGatewayOrigin` browser clone — the shared singleton that provider base_url and skill-installer validation consult gains nothing (asserted by a variant of the existing singleton-isolation test).

**Redaction, audit, rate limiting (MAJ-010)**
- Every gateway site that records a request `Host` or a full URL into a log file or the audit chain MUST redact a Mode 1 label (`<label>.localhost`), exactly as `pkg/gateway/preview_path_redact.go` already redacts token-bearing paths at its six inventoried sites; the redaction inventory and its **build-breaking guard** (`preview_path_redact_test.go`) MUST extend to the Host/URL-recording sites.
- Mode 1 requests MUST be audited with the same events Mode 2 uses (e.g. `auditDevSuccess` / `dev.proxied` for proxied Mode 1), carrying the redacted label.
- The preview rate limiter MUST apply **per label** on Mode 1 hosts (today the token-bucket sits only on the `/preview/` prefix).

**Tool text and docs**
- The web_serve tool text MUST teach both modes and the Mode 2 asset rule and the console-check guidance (FR-017).
- User docs — named files: `docs/previews.md` (primary; its "Logins inside a development preview do not stick" line becomes wrong once Q1 lands and MUST be rewritten) and `docs/security.md` **only if** its preview mention changes (today it is an approval-flow list item, no promise — verify at implementation) — MUST name the F794-4 phishing residual, the F794-7 fallback residual, and carry the blank-preview troubleshooting note (console-only violations), plus the dual-URL explanation (which link appears where and why) and the note that re-serving a **different** directory mints a new label — a new origin with empty storage and a logged-out app (round-1 OBS-002, so users do not report it as a bug).

## Integration boundaries

### Dev-server upstream (proxy path)
- **Data in:** proxy requests forwarded to the registration's loopback port; the credential filter strips only the reserved cookie names and the gateway's own Bearer value (Q1) — the app's own cookies and headers forward.
- **Data out:** upstream responses with CSP template emission (Mode 2), `ACAO: *` override (upstream ACAO/ACAC deleted first), reserved-`Set-Cookie` neutralization, the Mode 2-scoped redirect rule; Mode 1 responses pass through with redirects untouched.
- **Contract:** HTTP over the registration's loopback port; the Director does not rewrite `Host`, so absolute-same-origin `Location` forms are possible and must be handled (Mode 2: 502; Mode 1: pass).
- **On failure:** upstream down or out-of-policy Mode 2 redirect → error surfaced (502 per the redirect rule), never a fallback to another mode or a degraded policy.
- **Development:** a real dev server and the committed realistic-bundle fixture — never mocked at this boundary.

### User's browser (the tab)
- **Data in:** the one link the SPA selected (Q2); the SPA re-validates `isolated_url` client-side before rendering it.
- **Data out:** requests subject to the served headers; Chromium-family/Firefox on loopback resolve `*.localhost` internally (RFC 6761); WebKit delegates to the system resolver and gets the Mode 2 link selected for it.
- **On failure:** an engine or deployment where neither mode resolves → web_serve refusal path (F794-6) — never a degraded policy.

### Agent's built-in browser panel
- **Data in:** the same primary URL web_serve returned; admission for the label host class via the browser-dedicated SSRF clone (FR-021); a real Chrome that enforces the same served headers (ADR-094 correction note item 1).
- **Contract:** the agent sees exactly what the user sees — this is the agent's own review surface and part of the reachability claim (US-6).
- **On failure:** a blank render in the panel is diagnosable via the console (tool text guidance, user docs note); a portless Mode 1 URL is not openable from the panel (explicit-port SSRF rule) — documented limitation.

### Registry and tokens
- **Contract:** unchanged tokens (43-char base64url), TTLs, revocation mechanics; **stores:** static = `pkg/agent/served_subdirs.go::ServedSubdirs`, dev = `pkg/sandbox/dev_servers.go::DevServerRegistry` (web_serve holds it as the `devReg` field); `pkg/gateway/preview_token.go::Mint` is the Library store, not a web_serve store (round-1 MAJ-005).
- **Label lifecycle (FR-029):** the label is minted with the token and **renews in place** with it — re-serving the same directory keeps the same token and label (the recorded `ServedSubdirs.Register` regression: unconditional rotation silently 404'd URLs already handed to users, and a new origin wipes the app's storage and login); past `maxTokenLifetime` the token rotates and **the label rotates with it** (new origin — storage reset is the existing bounded-renewal behaviour); the label becomes unresolvable on **every** path that makes the token unresolvable: expiry (janitor `purgeExpired`/`sweepExpired`), `Evict`, replace (different-directory re-serve), `Unregister`/`UnregisterByAgent`, `SetOnEvict`-driven callbacks, and `preview_enabled=false` hot-flip. The label↔token map lives in the same store and under the same lock as the token. No new config keys.

## BDD Scenarios

### Feature: Mode 1 — label-host isolation

```gherkin
Scenario: S-1.1 Dual-URL mint on a Mode 1 deployment
  Given a default loopback install with public_url http://localhost:<port>
  When an agent calls web_serve for a directory
  Then the tool result contains both url (the /preview/ path) and isolated_url (http://<label>.localhost:<port>/)
  And the label matches the grammar (lower-case letters, digits, hyphens, 1-63 chars, no leading/trailing hyphen)
  Traces to: US-1 AC1 | Happy Path

Scenario: S-1.2 Non-Mode-1 deployment mints the fallback only
  Given public_url https://localhost:<port> (or an IP literal, a real domain, or a Tailscale name)
  When an agent calls web_serve for a directory
  Then the tool result contains url only and no isolated_url
  Traces to: US-4 AC4 | Alternate Path

Scenario: S-1.3 Misconfigured canonical origin refuses to mint
  Given public_url http://<wildcard-host>:<port> (or empty, unparseable, or trailing-dot http://localhost.:<port>)
  When an agent calls web_serve
  Then the tool call returns an error result and no URL is minted
  And the served policy is never degraded to a weaker fallback
  Traces to: US-4 AC1 | Error Path

Scenario: S-1.4 Label renews in place; rotation and revocation retire it
  Given a minted label for a directory
  When the same directory is re-served, then when the token renews in place the label is unchanged
  And when maxTokenLifetime forces rotation, the label rotates with the token and the old label 404s
  And when the token expires, is evicted, is replaced by a different-directory re-serve, or preview_enabled flips off, the label 404s
  Traces to: US-1 AC3 | Edge Case
```

```gherkin
Scenario: S-2.1 Mode 1 full app with storage
  Given the user opens http://<label>.localhost:<port>/ in Chromium
  Then the full app renders with no sandbox directive in effect
  And localStorage, sessionStorage, indexedDB, cookies and the History API all work
  Traces to: US-2 AC1 | Happy Path

Scenario: S-2.2 Mode 1 authenticated reads are blocked by three real controls (rewritten, round-1 MAJ-003)
  Given the user is logged into Omnipus in the same browser (a valid omnipus-session cookie for localhost)
  When script on http://<label>.localhost:<port>/ attempts fetch('http://localhost:<port>/api/v1/agents', {method:'GET', credentials:'include'})
  Then the response is refused or unusable to the script because ALL of:
    the CORS layer does not reflect a <label>.localhost Origin, so the browser denies the script the cross-origin read
    a GET carries no X-Csrf-Token header, so any state-changing call is refused by the CSRF layer
    a WebSocket connection attempt from the label origin is refused by the WS origin check
  And the gateway's own API handler may still have serviced the request server-side — the isolation claim is browser-side
  And a regression test proves the pre-change behaviour: the same fetch DOES return data cross-origin today
  Traces to: US-2 AC2 | Error Path

Scenario: S-2.3 Mode 2 unchanged isolation
  Given the user opens the /preview/ URL
  Then the CSP template is byte-identical to the spec template (tripwire-checked)
  And a script attempting fetch to the gateway origin is blocked by connect-src
  Traces to: US-2 AC3 | Happy Path

Scenario: S-2.4 Same token, two hosts, one registration
  Given a live registration for (agent, token, label)
  When the token URL and the label URL are both requested
  Then both resolve to the same registration and the same served directory
  Traces to: US-2 AC4 | Happy Path

Scenario: S-2.5 Unknown label 404s
  When a request arrives for a grammar-valid label with no live registration
  Then the response is 404 and no gateway handler produced it
  Traces to: US-2 AC2 | Error Path

Scenario: S-2.6 Preview-Host request never reaches a gateway handler (round-1 MAJ-002)
  Given a Mode 1 request to <label>.localhost:<port>
  When the path is /api/v1/agents or /auth/session in static mode
  Then the response is a file-server 404 and the gateway API/auth handler is provably not invoked (test asserts the handler did not run)
  And in dev mode the same path forwards to the upstream app, which serves its own response
  And the gateway's /api/v1 handler is still provably not invoked
  Traces to: US-2 AC2 | Edge Case

Scenario: S-2.7 Route-mismatch requests skip the CSRF gate (round-1 MAJ-001)
  Given the gateway's middleware wrap order (CSRF outermost over the config snapshot over the mux)
  When a POST with a valid session cookie but no X-Csrf-Token header targets a preview Host
  Then the CSRF gate does not refuse it, and it reaches the preview-host mux
  And the preview-host mux sees a live config snapshot (hot-flip preview_enabled=false retires Mode 1 without restart)
  And the preview rate limiter applies, per label
  Traces to: US-5 AC1 | Edge Case

Scenario: S-2.8 Preflight answer is method-locked, no wildcard headers (round-1 MIN-005)
  Given a cross-origin OPTIONS preflight from a label origin to the token prefix
  Then the response allows exactly GET, HEAD, OPTIONS and carries no Access-Control-Allow-Headers wildcard
  Traces to: US-3 AC1 | Edge Case

Scenario: S-2.9 Host is lower-cased before the registry lookup (F-2)
  Given a live registration with label myapp
  When a request arrives with Host FOO.LOCALHOST:<port> (or MyApP.localhost:<port>)
  Then it routes to the same registration as myapp.localhost:<port>
  Traces to: US-1 AC1 | Edge Case

Scenario: S-2.10 Non-Mode-1 deployment: label-class URLs 404 via the empty registry (F-1)
  Given public_url https://localhost:<port> (SSRF admission unchanged, deployment-agnostic)
  When a request arrives for any <label>.localhost:<port>
  Then the response is 404 because the label registry is empty
  And no gateway or preview handler ran
  Traces to: US-2 AC2 | Edge Case

Scenario: S-2.11 Wrong-port label host falls through to the main mux
  Given the canonical origin's port is 5000
  When a request arrives with Host <label>.localhost:9999 (or portless against an explicit-port canonical origin)
  Then the preview mux does not claim it and the main mux handles it exactly as today
  Traces to: US-2 AC2 | Edge Case
```

### Feature: Credential filter (Q1)

```gherkin
Scenario: S-3.1 The app's own login sticks while Omnipus credentials are stripped (rationale corrected, round-1 MAJ-003)
  Given a dev-mode preview of an app that sets its own session cookie on login
  When the user logs into the app inside the preview and navigates
  Then the app's own cookie forwards on the next request and the login persists
  And the Cookie header that reaches the upstream contains no omnipus-session, csrf or __Host-csrf pair
  And docs/previews.md no longer claims logins do not stick
  Traces to: US-1 AC1 | Happy Path

Scenario: S-3.2 Name-scoped filter, exact names only
  Given an upstream-bound request with cookies omnipus-session=x; myapp_session=y; csrf=z; __Host-csrf=w; CSRF=t
  Then the forwarded Cookie header is myapp_session=y; CSRF=t (exact, case-sensitive name match)
  Traces to: US-1 AC1 | Edge Case

Scenario: S-3.3 Authorization deleted only for the gateway's own Bearer
  Given an upstream-bound request with Authorization: Bearer <gateway-secret> in one case, and Bearer <anything-else> in another
  Then the first is deleted and the second forwards unchanged; non-Bearer Authorization values forward unchanged
  Traces to: US-1 AC1 | Edge Case

Scenario: S-3.4 Reserved Set-Cookie neutralization preserved
  When the upstream app sets a Set-Cookie named omnipus-session or __Host-csrf
  Then the response's Set-Cookie is neutralized before it reaches the browser (existing behaviour, unchanged)
  Traces to: US-1 AC1 | Happy Path

Scenario: S-3.5 One code path, both modes
  Given the same proxy code path serves Mode 1 and Mode 2 requests
  Then the credential filter behaves identically in both modes (no mode-conditional branch exists)
  Traces to: US-1 AC1 | Edge Case
```

### Feature: Planted-cookie detection and recovery (Q4)

```gherkin
Scenario: S-4.1 Duplicate reserved name detected, cleared in the same response
  Given a browser holding a planted duplicate of omnipus-session (Mode 1 Domain=localhost toss, or a Mode 2 document.cookie shadow-path plant)
  When any request reaches the gateway carrying two omnipus-session values in the Cookie header
  Then the response carries Set-Cookie clear lines (empty, Max-Age=0, past Expires) for omnipus-session at the request path, every ancestor directory, and /, in both Domain=localhost and host-only forms
  And the gateway treats the credential read for that request as failed
  Traces to: US-2 AC7 | Error Path

Scenario: S-4.2 State-changing request gets the typed retry error
  Given a request that carries a planted duplicate of a reserved name
  When the request method is POST/PUT/PATCH/DELETE
  Then the gateway answers with the standard JSON error envelope, code planted_cookie_cleared, message "Omnipus cleared cookies set by a preview — please retry"
  And the SPA renders it with a Retry action
  Traces to: US-2 AC7 | Error Path

Scenario: S-4.3 Unaffected names and single-valued requests are untouched
  Given a request whose Cookie header carries each reserved name at most once (normal logged-in traffic)
  Then no clear lines are emitted and the request proceeds normally
  And a request carrying duplicates of a non-reserved name (e.g. two myapp_session values) is not intercepted
  Traces to: US-2 AC7 | Happy Path
```

### Feature: Redirect rule (Mode 2) and Mode 1 pass-through

```gherkin
Scenario: S-5.1 In-prefix redirect emitted (Mode 2)
  When the upstream answers 302 with Location /preview/{agent}/{token}/next
  Then the Location is emitted unchanged
  Traces to: US-5 AC2 | Happy Path

Scenario: S-5.2 Out-of-prefix redirect replaced (Mode 2)
  When the upstream answers 302 with Location /api/v1/config (raw, protocol-relative //localhost:<port>/api/v1/config, or absolute same-origin http://127.0.0.1:<port>/api/v1/config)
  Then the response is 502 with no Location header
  Traces to: US-5 AC2, US-2 AC6 | Error Path

Scenario: S-5.3 Root-relative redirect re-rooted then checked (Mode 2)
  When the upstream answers 302 with Location /next (same-origin, inside the app)
  Then the Location is re-rooted under the token prefix, re-checked, and emitted
  Traces to: US-5 AC2 | Alternate Path

Scenario: S-5.4 Mode 1 passes every redirect through (round-1 MAJ-013)
  Given a Mode 1 proxied response with Location /login (or any absolute form)
  Then the Location is emitted unchanged regardless of prefix
  Traces to: US-5 AC4 | Alternate Path

Scenario: S-5.5 Non-redirect statuses and 304
  When the upstream answers 200 with a stray Location header
  Then the Location is deleted (Mode 2) / emitted (Mode 1)
  And a 304 passes untouched in both modes
  Traces to: US-5 AC3 | Edge Case
```

### Feature: SSRF admission for the agent's panel

```gherkin
Scenario: S-6.1 Label URL opens in the agent's browser
  Given a Mode 1 mint and the browser checker clone
  When the agent's browser navigates to http://<label>.localhost:<port>/
  Then the SSRF layer admits it and the page renders
  Traces to: US-6 AC1 | Happy Path

Scenario: S-6.2 Singleton isolation unchanged (CRIT-002 tightening)
  Given the shared SSRF singleton consulted for provider base_url and skill-installer validation
  When a label-class URL is checked against the singleton
  Then it is refused, exactly as before this feature
  Traces to: US-6 AC1 | Edge Case

Scenario Outline: S-6.3 The seven named negatives (F-3)
  Given the SSRF label-host rule with a configured gateway origin http://localhost:<port>
  When the agent's browser requests the URL in the table
  Then the verdict column holds exactly

  | # | URL | Verdict |
  |---|-----|---------|
  | 1 | http://<registered-label>.localhost:<port>/api/v1/agents | Admitted by SSRF (host class) — and answered 404 by the gateway (empty label on non-Mode-1, or preview mux in Mode 1); ADR-073 path scope untouched for the bare host |
  | 2 | http://foo.localhost.:<port>/ | Refused (trailing dot) |
  | 3 | http://foo.localhost/ (no port) | Refused (explicit-port requirement; fails closed) |
  | 4 | http://FOO.LOCALHOST:<port>/ | Admitted AND routed consistently (lower-cased to a grammar-valid label; F-2) |
  | 5 | ftp://foo.localhost:<port>/ | Out of scope for the label class (non-http(s)) — existing scheme handling of the bare host unchanged |
  | 6 | http://localhost:<port>/preview/... (bare host) | Unaffected singleton/ADR-073 behaviour — path-scoped as today |
  | 7 | http://ba..localhost:<port>/ or http://-bad-.localhost:<port>/ | Refused (grammar: empty label part / leading+trailing hyphens) |

  Traces to: US-6 AC1 | Edge Case
```

### Feature: Redaction, audit, per-label rate limiting

```gherkin
Scenario: S-7.1 Mode 1 label redacted wherever Mode 2 paths are (updated)
  Given a Mode 1 request to a label host
  When the gateway logs or audits the request at any of the inventoried sites
  Then the label is redacted exactly as token-bearing paths are, and the redaction guard (build-breaking) covers the extended inventory
  Traces to: US-2 AC2 | Happy Path

Scenario: S-7.2 Per-label rate limit
  Given a label host serving requests
  When one label's request rate exceeds the preview rate limit
  Then that label is throttled and other labels and the main host are unaffected
  Traces to: US-2 AC2 | Edge Case
```

### Feature: SPA dual-URL card (Q2)

```gherkin
Scenario: S-8.1 Chromium link and Safari fallback from one result
  Given a Mode 1 dual-URL result rendered in the chat card
  When the engine is Chromium-family (navigator.userAgentData present)
  Then the card shows exactly the Mode 1 link (isolated_url, re-validated client-side)
  And when the engine is Safari/WebKit or detection is uncertain, the card shows exactly the Mode 2 link (url/path) — never both
  Traces to: US-8 AC1 | Happy Path

Scenario: S-8.2 Firefox link by UA sniff (necessary, stated)
  Given the engine is Firefox (no navigator.userAgentData API exists)
  When the card selects a link
  Then the UA token Firefox/ selects the Mode 1 link
  Traces to: US-8 AC1 | Alternate Path

Scenario: S-8.3 Old transcripts replay as Mode 2-only
  Given a persisted serve_web result without isolated_url
  When the card renders
  Then it behaves exactly as today (Mode 2 link)
  Traces to: US-8 AC3 | Edge Case

Scenario: S-8.4 Warmup probes the Mode 2 URL even for Mode 1 links (round-1 MAJ-009)
  Given a dev-mode Mode 1 result
  When the card warms the dev server before showing Ready
  Then the probe targets the Mode 2 URL (same-origin, permitted by the SPA's connect-src), not the cross-origin label URL
  And the Mode 1 link still shows Ready when the Mode 2 probe succeeds
  Traces to: US-8 AC1 | Edge Case

Scenario: S-8.5 Tampered isolated_url falls back
  Given a replayed or hand-edited result whose isolated_url has a second dot, another suffix, a foreign port, https, or an invalid label
  When the card selects a link
  Then resolvePreviewHref rejects it and renders the Mode 2 fallback
  Traces to: US-8 AC4 | Error Path
```

## TDD Plan

Tests are written before implementation. Order 2 is the real RED gate: it proves the vulnerability exists today (round-1 MAJ-006 — the plan's falsifiability hinges on this row).

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | TestPreviewLabelGrammar | Unit | S-1.1, S-6.3#7 | Mint/validation: DS-1 rows — grammar bounds, lower-case minting, hostname-safe encoding |
| 2 | TestPreviewHostDispatch_REDAPIReachableToday | Integration (RED) | S-2.2, S-2.6 | **RED**: with a live session cookie, `GET http://<label>.localhost:<port>/api/v1/agents` returns the gateway's agent list TODAY (the #798 hole). This test must fail-to-pass — it pins that the gateway handler becomes unreachable and that a cross-origin script read is refused |
| 3 | TestPreviewHostDispatch_NoGatewayHandler | Integration | S-2.6 | Static mode: `/api/v1/agents`, `/auth/session` under a live label Host → file-server 404; a handler-invoked spy proves the gateway API/auth handler never ran; dev mode: same paths forward to the upstream |
| 4 | TestPreviewHostDispatch_Ordering | Integration | S-2.7 | CSRF gate does not run on preview-Host POSTs; live config snapshot (hot-flip retires Mode 1, no restart); per-label rate limit applies |
| 5 | TestPreviewHostMux_LowerCaseLookup | Unit | S-2.9 | Host lower-cased before registry lookup (F-2) — upper-case label routes to the same registration |
| 6 | TestPreviewSSRFLabelClass | Unit | S-6.1, S-6.3 | `pkg/security/ssrf.go::isAllowedGatewayOrigin` label class: DS-6's seven named rows, exact verdicts |
| 7 | TestPreviewSSRF_SingletonIsolation | Unit | S-6.2 | The shared singleton refuses label-class URLs, unchanged (variant of the existing singleton-isolation test) |
| 8 | TestPreviewSSRF_EmptyRegistry404 | Integration | S-2.10 | Non-Mode-1 deployment: label-class URL 404s via the empty label registry; no gateway handler ran (F-1's gate) |
| 9 | TestCanonicalOriginValidation | Unit | S-1.2, S-1.3, S-2.11 | The shared origin-validity helper: DS-3 rows — wildcard/empty/unparseable/trailing-dot refuse; port-mapped and implicit-80 accept; wrong-port Host falls through |
| 10 | TestPreviewCredentialFilter | Integration | S-3.1–S-3.5 | Dev-proxy request direction: DS-4 rows — name-scoped cookie filter, gateway-Bearer-only deletion, both modes one path, reserved Set-Cookie neutralization preserved |
| 11 | TestPreviewPlantedCookie | Integration | S-4.1–S-4.3 | DS-5 rows — duplicate-name detection, clear-line set (paths × Domain/host-only), typed retry error, normal traffic untouched |
| 12 | TestPreviewRedirectRule | Integration | S-5.1–S-5.3, S-5.5 | Mode 2: DS-2 rows — in-prefix emit, out-of-prefix 502 (incl. absolute-same-origin and protocol-relative forms), re-root+check, non-redirect Location deletion, 304 untouched |
| 13 | TestPreviewRedirectRule_Mode1Passthrough | Integration | S-5.4 | Mode 1: DS-2b rows — every Location emitted unchanged |
| 14 | TestPreviewCSPHeaderSet | Integration | S-2.1, S-2.3 | Mode 2 CSP byte-identical to the template (tripwire); Mode 1 header set: `frame-ancestors 'none'`, no source directives, no `sandbox` (static + proxied) |
| 15 | TestPreviewCORSPreflightPin | Integration | S-2.8 | Preflight answers GET, HEAD, OPTIONS exactly, no allow-headers wildcard |
| 16 | TestPreviewNavigationGuard | Integration | US-2 AC4, FR-010, FR-011 | Main-Host `/api/v1` with `Sec-Fetch-Dest: document` rejected before the handler, except the FR-010 GET-only exemption addresses (library download, media paths — each exercised); `/preview/` `Service-Worker: script`/`Sec-Fetch-Dest: serviceworker` refused |
| 17 | TestPreviewLabelLifecycle | Integration | S-1.4, FR-029 | Renew-in-place (same dir ⇒ same label), rotation at maxTokenLifetime (label rotates, old 404s), and 404 on every revocation path (janitor expiry, Evict, replace, Unregister/UnregisterByAgent, SetOnEvict, hot-flip) |
| 18 | TestPreviewRedactionInventory | Unit | S-7.1 | Host/URL redaction at the extended inventory; the build-breaking guard enforces the extended list |
| 19 | TestPreviewPerLabelRateLimit | Unit | S-7.2 | One label throttled; other labels and the main host unaffected |
| 20 | TestServeWebResult_IsolatedURL | Unit | S-1.1, S-1.2, S-8.3 | Tool result gains `isolated_url` (static + dev variants; absent on non-Mode-1); `// not-wire-format` note retained |
| 21 | TestResolvePreviewHref_Validation | Unit (vitest) | S-8.1, S-8.5 | `src/lib/preview-url.ts::resolvePreviewHref`: DS-8 rows — valid Mode 1 accepted, second dot / foreign port / https / bad label rejected → fallback |
| 22 | TestPreviewCardEngineSelection | Component (vitest) | S-8.1, S-8.2, S-8.3 | userAgentData ⇒ Mode 1; Firefox UA token ⇒ Mode 1; WebKit/unknown ⇒ Mode 2; never both links |
| 23 | TestPreviewWarmupTarget | Unit (vitest) | S-8.4 | Warmup HEAD hits the Mode 2 URL even when the rendered link is Mode 1 |
| 24 | TestPreviewIsolationE2E | E2E | S-2.1, S-2.2, S-3.1, S-8.1 | Playwright isolation projects: Mode 1 Chromium+Firefox open the full app with working storage/login; Mode 2 +WebKit gets the fallback; S-2.2's controls asserted in a real browser (named projects, `retries: 0`, Firefox included — round-1 MAJ-012) |

### CHECK mutations (each MUST flip at least one test red, else the suite is not sensitive)

| # | Mutation | Must flip |
|---|----------|-----------|
| M-1 | Widen `pkg/gateway/rest.go::isAllowedOrigin` to reflect `<label>.localhost` Origins | S-2.2 (cross-origin read succeeds) — order 2's RED row proves the flip direction (retargeted from the unfalsifiable round-1 mutation 1) |
| M-2 | Widen `pkg/gateway/websocket.go::wsCheckOrigin` to accept label origins | S-2.2's WS assertion |
| M-3 | Widen the SSRF label class (accept any port, or a multi-label `a.b.localhost`) | S-6.3 rows 3, 7 |
| M-4 | Remove the mux's Host lower-casing | S-2.9 |
| M-5 | Revert the credential filter to delete the whole Cookie header (round-1's strip-everything reading) | S-3.1, S-3.2 |
| M-6 | Remove planted-cookie detection/clearing | S-4.1, S-4.2 |
| M-7 | Remove Host/full-URL redaction | S-7.1 |
| M-8 | Apply the out-of-prefix 502 rule to Mode 1 responses too | S-5.4 |
| M-9 | Render both links / drop engine detection | S-8.1 |

### Test datasets

**DS-1 — Label grammar** (Traces to S-1.1, S-6.3#7)

| Row | Input | Valid? | Notes |
|---|---|---|---|
| 1 | `a` | yes | min length 1 |
| 2 | 63 `a`s | yes | max length |
| 3 | 64 `a`s | no | over max |
| 4 | `-lead` | no | leading hyphen |
| 5 | `trail-` | no | trailing hyphen |
| 6 | `has_underscore` | no | `_` (today's token shape) is not hostname-safe |
| 7 | `MiXeD` | minted lower-case | never minted upper; incoming upper is lower-cased at the mux (F-2) |
| 8 | `` (empty) | no | empty label rejected |

**DS-2 — Redirect Location forms, Mode 2** (Traces to S-5.1–S-5.3, S-5.5)

| Row | Location raw | Resolved origin/path | Verdict |
|---|---|---|---|
| 1 | `/preview/{agent}/{token}/next` | alias, in prefix | emit unchanged |
| 2 | `/api/v1/config` | alias, out of prefix | 502, no Location |
| 3 | `//localhost:<port>/api/v1/config` | protocol-relative → alias, out | 502 |
| 4 | `http://127.0.0.1:<port>/api/v1/config` | loopback alias, out | 502 (Director does not rewrite Host) |
| 5 | `/next` | root-relative → re-rooted under prefix, in | emit re-rooted |
| 6 | `/preview/{agent}/{token}/../../etc` | dot-segments normalise out | 502 |
| 7 | `%2e%2e%2fapi` | percent-encoded dot-segments normalise out | 502 |
| 8 | `http://evil.example/` | foreign origin | 502 |
| 9 | `data:text/html,x` | not an http(s) resource | 502 |

**DS-2b — Mode 1 redirect pass-through** (Traces to S-5.4)

| Row | Location raw | Verdict |
|---|---|---|
| 1 | `/login` | emitted unchanged |
| 2 | `http://127.0.0.1:<port>/api/v1/config` | emitted unchanged (out-of-prefix 502 does NOT apply) |
| 3 | `https://auth.example.com/oauth` | emitted unchanged |

**DS-3 — Canonical origin matrix** (Traces to S-1.1–S-1.3, S-2.11)

| Row | public_url | Mode | Minted primary | Label-Host with port… |
|---|---|---|---|---|
| 1 | `http://localhost:5000` | 1 | `http://<label>.localhost:5000/` | dispatches |
| 2 | `https://localhost:5000` | 2 | — (fallback only) | — |
| 3 | `http://127.0.0.1:5000` | 2 | — | — |
| 4 | `http://myhost.example.com:5000` | 2 | — | — |
| 5 | `http://myhost.tailnet-name.ts.net:5000` | 2 | — | — |
| 6 | `http://*.wildcard.example:5000` | refuse | no mint | — |
| 7 | `http://localhost.:5000` | refuse | no mint | — |
| 8 | `http://localhost:8080` (listener 5000) | 1 | `http://<label>.localhost:8080/` — **canonical-port** mint | `:8080` dispatches despite listener 5000 |
| 9 | `http://LOCALHOST:5000` | 1 (case-normalized) | `http://<label>.localhost:5000/` | dispatches |
| 10 | `http://localhost` (implicit 80) | 1 | `http://<label>.localhost/` — portless; agent panel cannot open it (explicit-port SSRF), user tab can | portless dispatches |
| 11 | `http://localhost:5000`, Host `<label>.localhost:9999` | — | — | falls through to main mux |
| 12 | `http://localhost:5000`, Host `<label>.localhost` (portless vs explicit-port origin) | — | — | falls through |

**DS-4 — Credential filter cookie strings** (Traces to S-3.1–S-3.5)

| Row | Incoming Cookie header | Forwarded |
|---|---|---|
| 1 | `omnipus-session=s1; myapp_session=y; csrf=c1; __Host-csrf=h1; CSRF=lower-mismatch` | `myapp_session=y; CSRF=lower-mismatch` (exact, case-sensitive names) |
| 2 | `myapp_session=y` | unchanged |
| 3 | `omnipus-session=s1` only | header omitted |
| 4 | `myapp_session=a; myapp_session=b` | both forward (duplicates of non-reserved names are not the filter's business) |
| 5 | `Authorization: Bearer <gateway-registered-secret>` | deleted |
| 6 | `Authorization: Bearer some-other-token` | unchanged |
| 7 | `Authorization: Basic dXNlcjpwYXNz` | unchanged |
| 8 | upstream `Set-Cookie: omnipus-session=…` | neutralized (response direction, existing) |
| 9 | upstream `Set-Cookie: myapp_session=…` | forwards (the app's login sticks) |

**DS-5 — Planted-cookie requests** (Traces to S-4.1–S-4.3)

| Row | Cookie header | Detection | Response |
|---|---|---|---|
| 1 | `omnipus-session=real; omnipus-session=planted` | duplicate reserved name | clear lines (both forms, request path + ancestors + `/`); GET proceeds credential-less |
| 2 | row 1 but `POST /api/v1/agents` | same | clear lines + JSON error `planted_cookie_cleared` |
| 3 | `csrf=one; csrf=two` | duplicate reserved name | cleared |
| 4 | `__Host-csrf=one; __Host-csrf=two` | duplicate reserved name | cleared |
| 5 | `myapp_session=a; myapp_session=b` | non-reserved duplicate | not intercepted |
| 6 | `omnipus-session=only` | single occurrence | not intercepted |
| 7 | request path `/a/b/c` with duplicate | — | clear lines at `/a/b/c`, `/a/b`, `/a`, `/` × 2 forms — bounded |

**DS-6 — SSRF label-class negatives** (Traces to S-6.1, S-6.3; the F-3 seven)

| # | URL (gateway origin `http://localhost:5000`) | Verdict |
|---|---|---|
| 1 | `http://<registered-label>.localhost:5000/api/v1/agents` | SSRF-admitted; gateway answers 404/preview-mux (never the bare-host ADR-073 scope) |
| 2 | `http://foo.localhost.:5000/` | refused (trailing dot) |
| 3 | `http://foo.localhost/` | refused (no explicit port) |
| 4 | `http://FOO.LOCALHOST:5000/` | admitted; routed lower-cased |
| 5 | `ftp://foo.localhost:5000/` | label class out of scope; bare-host scheme handling unchanged |
| 6 | `http://localhost:5000/preview/…` | bare host, ADR-073 path scope, unchanged |
| 7 | `http://ba..localhost:5000/` and `http://-bad-.localhost:5000/` | refused (grammar) |

**DS-7 — CSP/policy template strings** (Traces to S-2.1, S-2.3, S-2.8)

| Row | Variation | Expected |
|---|---|---|
| 1 | Builder output for `(agent, token)` | byte-identical to the template (tripwire) |
| 2 | Prefix with chars needing percent-encoding | encoded to RFC 3986 unreserved set before interpolation |
| 3 | Agent ID `; drop` | percent-encoded, no directive split |
| 4 | Preflight | methods `GET, HEAD, OPTIONS`, no allow-headers wildcard |

**DS-8 — `resolvePreviewHref` inputs** (Traces to S-8.1, S-8.5)

| Row | `isolated_url` | Result |
|---|---|---|
| 1 | `http://myapp.localhost:5000/` | Mode 1 href (engine-gated) |
| 2 | `http://myapp.evil.localhost:5000/` | fallback (second dot) |
| 3 | `http://myapp.localhost:9999/` | fallback (foreign port) |
| 4 | `https://myapp.localhost:5000/` | fallback (https) |
| 5 | `http://-bad-.localhost:5000/` | fallback (grammar) |
| 6 | absent / empty | fallback exactly as today |

### Regression test requirements

The feature modifies existing behaviour (the dev proxy strips all cookies/Authorization today; the gateway API is reachable on label hosts today; docs claim logins don't stick). Regressions:

| Behaviour preserved | Existing test / requirement |
|---|---|
| Mode 2 serving end-to-end | existing preview suites stay green unchanged |
| `preview_enabled` hot-flip | existing hot-flip test stays green; extended by order 4's Mode 1 clause |
| CSRF on the main mux | the existing csrf-realmux suite stays green — untouched paths keep gating |
| Path redaction inventory | `preview_path_redact_test.go`'s build-breaking guard EXTENDS to the Host/URL sites (M-7 flips it red) |
| Token revocation/TTL | existing token suites stay green; label shares their lifecycle (S-1.4) |
| Dev-proxy warmup | `src/components/chat/IframePreview.tsx::probeOnce` spec updated (S-8.4) — the probe target changes, the warmup UX does not |
| **Credential strip-everything is deliberately NOT preserved** | the current strip-all test is REWRITTEN to the name-scoped contract, not kept green against old behaviour (M-5 flips it red) |
| Library and mail preview surfaces | untouched; their own suites stay green |
| Browser test matrix | isolation projects in `playwright.config.ts` (named, `retries: 0`, Firefox); shard group `preview-isolation` in `tests/e2e/shards.json` unchanged in shape |

## Functional Requirements

| ID | Requirement | Priority | Traces to |
|---|---|---|---|
| FR-001 | On a Mode 1 deployment the web_serve result MUST contain both the Mode 1 primary URL and the unchanged `/preview/` fallback, in one result, in both `static` and `dev` variants. | P0 | US-1 AC3, S-1.1 |
| FR-002 | On a non-Mode-1 deployment (https-loopback, IP literal, real domain, Tailscale) the result MUST contain the fallback only — no `isolated_url`. | P0 | US-1 AC2, S-1.2 |
| FR-003 | A wildcard-host, empty, unparseable or trailing-dot canonical origin MUST refuse to mint — never a degraded policy. | P0 | US-1 AC3, S-1.3 |
| FR-004 | The web_serve model MUST NOT contain a `sandbox` CSP directive in either mode; Mode 1 must leave storage APIs, cookies, forms, popups and downloads fully functional. | P0 | US-1 AC1, S-2.1 |
| FR-005 | Labels MUST match the grammar (letters/digits/hyphens, 1–63 chars, no leading/trailing hyphen), be minted lower-case from fresh entropy in a hostname-safe encoding, and the preview-host mux MUST lower-case the incoming `Host` before the registry lookup (F-2). | P0 | US-1 AC1, S-1.1, S-2.9, S-6.3#4 |
| FR-006 | A preview-Host request MUST reach a mux that mounts exactly one handler — preview serving; no gateway API/auth/SPA handler is reachable (static: file-server 404 for unknown paths; dev: forward to upstream). | P0 | US-2 AC2, S-2.6 |
| FR-007 | A grammar-valid label with no live registration MUST answer 404; on a non-Mode-1 deployment every label-class URL 404s via the empty registry (F-1's real gate). | P0 | US-2 AC2, S-2.5, S-2.10 |
| FR-008 | The label MUST map to the same registry entry as the Mode 2 token (same store, same lock; agent resolved from the registration). | P0 | US-1 AC3, S-2.4 |
| FR-009 | The three real Mode 1 controls MUST hold and MUST NOT be widened: the CORS layer refuses to reflect label origins, CSRF stays header-only (`X-Csrf-Token`), and the WS origin check refuses label origins. | P0 | US-2 AC2, S-2.2; mutations M-1, M-2 |
| FR-010 | A main-Host `/api/v1` navigation (`Sec-Fetch-Dest: document`) MUST be rejected before the API handler, EXCEPT the named read-only GET-only download/media addresses (Q3): `/api/v1/library/{workspaceId}/download`, `/api/v1/media/workspace/…`, `/api/v1/media/…` — each safe because it is a read-only byte stream gated by the same auth as the SPA's own fetches, navigated to by explicit user download clicks (`src/lib/library.ts::libraryDownloadUrl`, `src/lib/library-attachment.ts::mediaRefURL`, `LibraryExplorer.tsx::handleDownload`, ChatScreen attachment anchors); no state change, no credential exposure beyond the user's own session. | P0 | US-2 AC4, S-2.2 |
| FR-011 | A `/preview/` request carrying `Service-Worker: script` or `Sec-Fetch-Dest: serviceworker` MUST be refused. | P0 | US-2 AC4 |
| FR-012 | On proxied responses under the token prefix: upstream `ACAO`/`ACAC` deleted, then `Access-Control-Allow-Origin: *` without allow-credentials; preflights answer exactly `GET, HEAD, OPTIONS` with no allow-headers wildcard (F794-3: reads only). | P0 | US-3 AC1, S-2.8 |
| FR-013 | The 4-step `Location` rule applies to **Mode 2 only**: resolve (WHATWG, dot-segments/%2e normalised) → emit if alias-origin + in-prefix → else re-root raw root-relative values under the prefix and re-check → else 502, no `Location`; `Location` deleted on non-redirect statuses; 304 untouched. **Mode 1 passes every `Location` through unchanged.** | P0 | US-2 AC6, US-5 AC2–AC4, S-5.1–S-5.5 |
| FR-014 | The Mode 2 CSP response header MUST be byte-identical to the spec template (origin list frozen at boot, prefix percent-encoded, no raw interpolation); a static tripwire treats the literal template as the oracle; Mode 1 responses carry `frame-ancestors 'none'`, no source directives, no `sandbox`. | P0 | US-2 AC3, S-2.1, S-2.3 |
| FR-015 | A `Cookie` header carrying more than one occurrence of a reserved gateway cookie name (exact match: `omnipus-session`, `csrf`, `__Host-csrf`) MUST: mark the credential read failed for that request; emit clear lines (empty, `Max-Age=0`, past `Expires`) for the affected name at the request path, every ancestor directory, and `/`, in both `Domain=localhost` and host-only forms; and on state-changing methods answer the standard JSON error envelope with code `planted_cookie_cleared` and the message "Omnipus cleared cookies set by a preview — please retry" (Q4). Mode-independent; bounded at 2 × (path depth + 1) lines per name. | P0 | US-2 AC7, S-4.1–S-4.3 |
| FR-016 | The web_serve tool text MUST teach both modes, the Mode 2 asset rule (assets relative or under the token prefix) and the console-check guidance; an agent following it alone MUST produce a rendering preview (ADR-094 §6). | P0 | US-6 AC1–AC2 |
| FR-017 | The MIME map MUST answer `.mjs` → `text/javascript`, `.woff`/`.woff2` → `font/woff(2)`, `.wasm` → `application/wasm` (module scripts/fonts/wasm are blocked under `nosniff` today). | P1 | US-3 AC1 |
| FR-018 | `docs/previews.md` MUST be rewritten: the "logins do not stick" claim becomes wrong with Q1; name the F794-4 phishing residual, the F794-7 fallback residual, the blank-preview troubleshooting note, the dual-URL explanation, and the new-label-on-different-directory storage-reset note. `docs/security.md` only if its preview mention changes (verify at implementation). | P1 | US-7 AC1–AC3; OBS-002 |
| FR-019 | The shared origin-validity helper (parse / wildcard-host / emptiness / trailing-dot) MUST live in `pkg/gateway/middleware` beside `::CanonicalGatewayOrigin` (a `pkg/gateway` home would be an import cycle — `pkg/tools` imports it today); `libraryIsolationOrigins` is NOT renamed (OBS-001 adopted). | P1 | US-4 AC1–AC4, S-1.3, DS-3 |
| FR-020 | The dev-proxy Director MUST filter, in BOTH modes via one code path: `Cookie` pairs named exactly `omnipus-session` / `csrf` / `__Host-csrf` dropped (case-sensitive), all else forwarded (header omitted when empty); `Authorization` deleted only for a `Bearer` value matching a gateway-registered secret; response-direction `neutralizeReservedSetCookies` preserved (Q1). | P0 | US-1 AC1, S-3.1–S-3.5 |
| FR-021 | `pkg/security/ssrf.go::isAllowedGatewayOrigin` MUST admit the label class iff all of: (a) a configured gateway origin's host is exactly `localhost`; (b) the literal pre-resolution host is one grammar-valid label + exactly `.localhost`; (c) the lower-cased label matches FR-005's grammar; (d) the port equals the configured gateway port. Bare host keeps ADR-073's `/preview/` path scope; loopback-literal equivalence NOT extended; non-http(s) out of scope for the class. Admission is deployment-agnostic by construction (F-1: `pkg/agent/loop_wire.go` `browserSSRF` hardcodes `localhost`); the empty registry is the real Mode-2 gate (FR-007). The shared singleton gains nothing (S-6.2). | P0 | US-6 AC1–AC3, S-6.1–S-6.3 |
| FR-022 | The tool result payload MUST gain `isolated_url` (absolute Mode 1 URL when minted; absent/empty otherwise); `path`/`url` keep the fallback meaning; both variants gain it; old transcripts replay Mode 2-only; rides the `// not-wire-format` opt-out — no `contracts/` delta. | P0 | US-1 AC1, US-8 AC3, S-8.3 |
| FR-023 | `src/lib/preview-url.ts::resolvePreviewHref` MUST accept `isolated_url` and return it only when: host = exactly one lower-cased grammar-valid label + `.localhost`, port = `window.location.port`, scheme `http`, and FR-024's engine rule selects Mode 1; any failure ⇒ the existing fallback resolution. | P0 | US-8 AC1, US-8 AC4, S-8.1, S-8.5 |
| FR-024 | Engine rule (Q2): Mode 1 link iff `isolated_url` present+valid AND the engine resolves `*.localhost` — feature detection via `navigator.userAgentData` (Chromium family) first; Firefox detected by the UA token `Firefox/` (necessary: no detection API exists; UA-freeze keeps the token stable); Safari/WebKit, unknown, and detection failures get the Mode 2 link. The card MUST NOT show both links in any state. | P0 | US-8 AC1/AC2, S-8.1, S-8.2 |
| FR-025 | The dev-server warmup probe MUST target the Mode 2 URL even when the rendered link is Mode 1 (the SPA's own `connect-src` blocks a cross-origin HEAD to the label host — a Mode 1 probe would falsely time out every Mode 1 dev preview). | P0 | US-8 AC1, S-8.4 |
| FR-026 | Every gateway site recording a request `Host` or full URL into logs/audit MUST redact the Mode 1 label as `preview_path_redact.go` redacts token paths; the build-breaking inventory guard extends to the new sites. | P0 | US-2 AC2, S-7.1 |
| FR-027 | The preview rate limiter MUST apply per label on Mode 1 hosts (same limiter as the `/preview/` prefix; one label's throttle leaves others and the main host untouched). | P0 | S-7.2 |
| FR-028 | Dispatch ordering: the CSRF gate MUST NOT run on preview-Host requests; the preview-host mux MUST see a live config snapshot (`preview_enabled` hot-flip without restart); the per-label limiter MUST apply; FR-010/FR-011 apply to main-Host only. Implementation mechanism (wrap reorder vs. in-middleware exemption vs. out-of-wrap dispatch) is backend-lead's choice; five tests pin the behaviour. | P0 | US-5 AC1, S-2.7 |
| FR-029 | Label lifecycle: minted with the token; renews in place with same-directory re-serve; rotates with the token at `maxTokenLifetime`; unresolvable on every token-revocation path (janitor expiry, `Evict`, different-directory replace, `Unregister`/`UnregisterByAgent`, `SetOnEvict`, `preview_enabled=false`); label↔token map in the same store under the same lock; no new config keys. | P0 | US-1 AC3, S-1.4 |

## Success criteria

| ID | Criterion | Verified by |
|---|---|---|
| SC-001 | On a default loopback install, the served Mode 1 URL renders the full app in Chromium and Firefox with working `localStorage`, `sessionStorage`, IndexedDB, `document.cookie`, History API, forms, popups and an in-app login round-trip (zero `sandbox` effects). | Order-24 E2E + manual smoke |
| SC-002 | A script on the Mode 1 origin cannot read an authenticated gateway API response in a real browser (CORS non-reflection), cannot complete a state-changing call (header-only CSRF), and cannot open a WS (origin check) — while the server-side handler reachability change is separately pinned by order 3. | Order-2 RED + order-24 E2E |
| SC-003 | Mode 2 behaviour is unchanged: CSP byte-identical (tripwire green), all existing preview suites green. | Order-14 + existing suites |
| SC-004 | The previewed app's own login sticks in BOTH modes; exactly the three reserved cookie names and the gateway's own Bearer are stripped; `docs/previews.md` no longer claims otherwise. | Order-10 + S-3.1 |
| SC-005 | A planted duplicate of a reserved cookie is detected, cleared in the same response (all paths, both forms), and the user recovers with one click on a state-changing request. | Order-11 |
| SC-006 | Every CHECK mutation M-1…M-9 flips at least one named test red; the suite is sensitive to all nine. | CHECK phase (mutation log) |
| SC-007 | User download paths still work: library download and chat media/attachment downloads open from `/api/v1` document navigations (the FR-010 exemption list), while a generic `/api/v1/agents` document navigation is still rejected. | Order-24 E2E + order 16's guard rows |
| SC-008 | The SSRF label class admits exactly the seven DS-6 verdicts — no more (no wildcard, no second label, no other port, singleton unchanged). | Order-6/7 |

## Traceability matrix

| FR | User Story / AC | BDD Scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 AC3 | S-1.1 | TestServeWebResult_IsolatedURL |
| FR-002 | US-4 AC4 | S-1.2 | TestCanonicalOriginValidation, TestServeWebResult_IsolatedURL |
| FR-003 | US-4 AC1–AC3 | S-1.3 | TestCanonicalOriginValidation |
| FR-004 | US-1 AC1 | S-2.1 | TestPreviewCSPHeaderSet, TestPreviewIsolationE2E |
| FR-005 | US-1 AC1 | S-1.1, S-2.9, S-6.3#4/#7 | TestPreviewLabelGrammar, TestPreviewHostMux_LowerCaseLookup, TestPreviewSSRFLabelClass |
| FR-006 | US-2 AC2 | S-2.6 | TestPreviewHostDispatch_REDAPIReachableToday, TestPreviewHostDispatch_NoGatewayHandler |
| FR-007 | US-2 AC2 | S-2.5, S-2.10 | TestPreviewSSRF_EmptyRegistry404 |
| FR-008 | US-1 AC3 | S-2.4 | TestPreviewHostDispatch_NoGatewayHandler, TestPreviewHostMux_LowerCaseLookup |
| FR-009 | US-2 AC2 | S-2.2 | Order-2 RED, TestPreviewIsolationE2E; mutations M-1, M-2 |
| FR-010 | US-2 AC4 | S-2.2 (guard context) | TestPreviewNavigationGuard, TestPreviewIsolationE2E |
| FR-011 | US-2 AC4 | — (same guard test) | TestPreviewNavigationGuard |
| FR-012 | US-3 AC1 | S-2.8 | TestPreviewCORSPreflightPin |
| FR-013 | US-2 AC6, US-5 AC2–AC4 | S-5.1–S-5.5 | TestPreviewRedirectRule, TestPreviewRedirectRule_Mode1Passthrough; M-8 |
| FR-014 | US-1 AC1, US-2 AC3 | S-2.1, S-2.3 | TestPreviewCSPHeaderSet |
| FR-015 | US-2 AC7 | S-4.1–S-4.3 | TestPreviewPlantedCookie; M-6 |
| FR-016 | US-6 AC1–AC2 | holdout H-5 | UAT agent-run (docs-verifier audits the text against behaviour) |
| FR-017 | US-3 AC1 | — (rendering proof) | TestPreviewIsolationE2E (bundle renders incl. module scripts) |
| FR-018 | US-7 AC1–AC3 | holdout H-6 | docs-verifier audit against code |
| FR-019 | US-4 AC1–AC4 | S-1.3 | TestCanonicalOriginValidation |
| FR-020 | US-1 AC1 | S-3.1–S-3.5 | TestPreviewCredentialFilter; M-5 |
| FR-021 | US-6 AC1 | S-6.1–S-6.3 | TestPreviewSSRFLabelClass, TestPreviewSSRF_SingletonIsolation; M-3 |
| FR-022 | US-1 AC3, US-8 AC3 | S-1.1, S-8.3 | TestServeWebResult_IsolatedURL |
| FR-023 | US-8 AC1, US-8 AC4 | S-8.1, S-8.5 | TestResolvePreviewHref_Validation |
| FR-024 | US-8 AC1, US-8 AC2 | S-8.1, S-8.2 | TestPreviewCardEngineSelection, TestPreviewIsolationE2E; M-9 |
| FR-025 | US-8 AC1 | S-8.4 | TestPreviewWarmupTarget |
| FR-026 | US-2 AC2 | S-7.1 | TestPreviewRedactionInventory; M-7 |
| FR-027 | US-2 AC2 | S-7.2 | TestPreviewPerLabelRateLimit |
| FR-028 | US-5 AC1 | S-2.7 | TestPreviewHostDispatch_Ordering |
| FR-029 | US-1 AC3 | S-1.4 | TestPreviewLabelLifecycle |

Every FR appears; every scenario traces to at least one FR (via its parent AC's FRs in the matrix above).

## Ambiguity warnings

| # | What's ambiguous | Likely agent assumption | Resolution |
|---|---|---|---|
| A-1 | Exact HTTP status for the Fetch-Metadata/SW guard rejections | 403 | Deliberately unpinned — the ADR leaves it open; the requirement is "never reaches the handler", pinned by the implementing RED/GREEN pair. Backend-lead chooses; qa-lead's tests assert reachability, not the code. |
| A-2 | Middleware-order mechanism for FR-028 (wrap reorder vs. in-middleware exemption vs. out-of-wrap dispatch) | reorder in `gateway_boot.go` | Left to backend-lead; five tests pin the behaviour, not the mechanism. |
| A-3 | Where the credential filter reads the gateway's registered secrets from | the credential store injected at boot | Stated (FR-020); the exact accessor is implementation detail. |
| A-4 | `docs/security.md` changes needed | none (its preview mention is an approval-list item, no promise) | Verify at implementation; FR-018 scopes it conditionally. |
| A-5 | Clear-line count worst case (very deep request paths) | unbounded growth | Bounded normatively: 2 × (path depth + 1) per affected name (FR-015); normal SPA paths are shallow. |
| A-6 | Firefox UA-token sniff vs. future UA freeze changes | token stable | Stated as the necessary residual in FR-024; if Firefox ships a detection API, switch to it (note in tool text is not required — internal). |

## Flags for the architect (ADR-094 wording only — this spec does not edit the ADR)

| # | ADR-094 location | Issue | Suggested direction |
|---|---|---|---|
| AF-1 | §2.5 "No SPA changes required; no contract changes" | Contradicted by founder Q2 (SPA link selection is in scope) and the `isolated_url` tool-result field | Amend to "no SPA changes required for isolation itself; the link-selection card change (Q2) is additive SPA work; the tool result gains one field under the `// not-wire-format` opt-out" |
| AF-2 | §2.2 Mode 1 row: "`/api/v1/*` and `/auth/*` answer 404" | Imprecise for dev mode: the preview-host mux forwards every path to the upstream (an app's own `/api/v1/todos` must work); the precise claim is "no gateway handler is reachable under a preview Host" | Reword to the mux-level claim (FR-006) |
| AF-3 | §2.2 cookie-ordering sentence | Reads as if the Cookie header carried path attributes; the real detection rule is duplicate-name occurrence (RFC 6265 §5.4 ordering) | Reword per FR-015 |
| AF-4 | §2.1 module-script row + control-stack row 12 | MIME gaps (.mjs/.woff/.wasm) block module scripts under `nosniff`; row 12's "`frame-ancestors 'none'` strips embedding" is garbled | Add the MIME additions to the ADR's serving duties; reword row 12 |
| AF-5 | SSRF correction-note condition (a) | "Gateway host is exactly localhost" does not gate Mode-1-only-ness — the browser checker clone is wired with hardcoded `localhost` (`pkg/agent/loop_wire.go` `browserSSRF`); the real gate is the empty label registry (F-1) | Reword to the deployment-agnostic statement + registry gate (FR-021) |
| AF-6 | §2.2 redirect row | Does not scope the rule to Mode 2; applied to Mode 1 it would 502 every login redirect (round-1 MAJ-013) | Add "Mode 2 only; Mode 1 passes `Location` through" (FR-013) |

## Holdout evaluation scenarios (post-implementation, not in the TDD plan)

| # | Scenario | Expected |
|---|---|---|
| H-1 | On a default loopback install, have an agent serve a Vite-built storage app (uses `localStorage` and a login); open the chat link in Chrome | Full app renders at `http://<label>.localhost:<port>/`; login persists across reload; no console errors |
| H-2 | Same preview opened in macOS Safari | Card shows the `/preview/` link; app renders and works (module scripts load, fetch works); no "can't find server" |
| H-3 | In the Mode 1 tab, paste `fetch('http://localhost:<port>/api/v1/agents').then(r=>r.text()).then(console.log)` in DevTools | CORS blocks the read (TypeError), regardless of what the network tab shows server-side |
| H-4 | Log into Omnipus, open a Mode 1 preview whose app tosses `document.cookie='omnipus-session=x;domain=.localhost;path=/'`, then POST from the real SPA | One "please retry" click recovers; afterwards a single `omnipus-session` remains and traffic is normal |
| H-5 | Fresh agent, no human help: "serve this web app and check it renders" following only the updated tool text | Panel renders the preview; agent's console-check guidance demonstrably followed on any blank render |
| H-6 | Read the shipped `docs/previews.md` cold | Names both accepted residuals, the dual-URL explanation, and contains no "logins do not stick" claim |
| H-7 | Dev-mode app answers 302 to `/api/v1/config` in Mode 2 | User sees an error surface (502), never a config response; in Mode 1 the same app's redirect lands the user wherever the app sent them |

## Definition of done (two-line claim)

- **Code correct and tested:** all 24 TDD rows green in CI, M-1…M-9 each flip a named test red, existing preview/CSRF/redaction suites green.
- **Reachable by a user/agent:** `serve_web` remains registered for the agents that had it; a chat card renders the one working link; an agent following the tool text alone produces a rendering preview in its panel (US-6), verified per the reachability section.

## Reachability (checked before "done")

| Surface | Check |
|---|---|
| `serve_web` tool | still registered in the builtin catalog with an explicit policy entry for every agent that had it (Hard Constraint #6) — `grep -rl '"serve_web"' pkg/coreagent/ pkg/config/ pkg/tools/` finds the registrations; no policy regressions |
| Chat card | `IframePreview.tsx` renders the selected link in a running gateway+SPA build (order-24 E2E covers the real click) |
| Agent panel | an agent opens the Mode 1 URL through the built-in browser (SSRF admission + label routing live), not just in tests |
| Tool text | the updated `serve_web` description ships in the binary the agent sees (embedded, not docs-only) |

## Assumptions

1. `*.localhost` resolution: Chromium/Firefox resolve internally (RFC 6761); WebKit delegates to the system resolver — the engine-selection rule is built on this and the E2E matrix proves it per engine.
2. Founder decisions Q1–Q4 are final for this round (verbatim in the Decisions Log); further changes go through the founder, not this spec.
3. The gateway's registered secret values are readable at proxy time from the injected credential store (FR-020) — verified feasible at implementation; if not, backend-lead escalates before substituting a weaker match.
4. Cookie clear-line storm risk is bounded by real SPA path depths; if a pathological app creates deep paths, the FR-015 bound keeps the response sane.
5. The ADR-094 wording flags (AF-1…AF-6) are resolved by the architect without changing the normative content of this spec; if one overturns a normative choice, the spec is amended and re-grilled, not silently diverged.

## Round-1 finding dispositions

| Finding | Severity | Disposition |
|---|---|---|
| CRIT-001 dual-URL contract unspecified | CRIT | Fixed — Decisions Log Q2; FR-022/023/024, US-8, S-8.1–S-8.5, orders 19–24; ADR §2.5 contradiction flagged (AF-1) |
| CRIT-002 SSRF exactness unspecified | CRIT | Fixed — FR-021 with the ADR correction note's exact rule + F-1/F-2/F-3; DS-6 seven negatives; orders 6–8; ADR condition-(a) wording flagged (AF-5) |
| CRIT-003 credential filter unspecified | CRIT | Fixed — Decisions Log Q1; FR-020; S-3.1–S-3.5; DS-4; order 10; mutation M-5 |
| MAJ-001 CSRF/middleware ordering unknown | MAJ | Fixed — wrap order cited (`gateway_boot.go`, CSRF outermost), FR-028 normative clauses, mechanism left to backend-lead, order 4 pins it |
| MAJ-002 host-dispatch semantics wrong | MAJ | Fixed — FR-006/S-2.6: one handler, no gateway handler reachable, dev forwards, static 404 is file-server; ADR's literal 404 sentence flagged (AF-2) |
| MAJ-003 "isolation by cookie absence" wrong | MAJ | Fixed — US-2 AC2 and S-2.2 restated against the three real controls (CORS non-reflection, header-only CSRF, WS origin check); S-3.1's rendering rationale corrected (same-origin requests need no ACAO); FR-009 forbids widening; M-1/M-2 |
| MAJ-004 planted cookies unhandled | MAJ | Fixed — Decisions Log Q4; FR-015; S-4.1–S-4.3; DS-5; order 11; M-6 |
| MAJ-005 wrong registry citations | MAJ | Fixed — `pkg/agent/served_subdirs.go::ServedSubdirs`, `pkg/sandbox/dev_servers.go::DevServerRegistry`, `pkg/gateway/preview_token.go::Mint` marked Library-store-only |
| MAJ-006 unfalsifiable CHECK mutation 1 | MAJ | Fixed — retargeted to the cross-origin read; order 2 is a real RED proving today's hole; M-1 direction proven |
| MAJ-007 nav-guard breaks downloads | MAJ | Fixed — Decisions Log Q3; FR-010 exemption list with per-entry safety argument; order 16; SC-007 |
| MAJ-008 canonical vs listener port | MAJ | Fixed — canonical-origin port normative everywhere (FR-001/005, DS-3 row 8, edge-case table) |
| MAJ-009 warmup probe vs connect-src | MAJ | Fixed — FR-025 probes the Mode 2 URL; S-8.4; order 22 |
| MAJ-010 Host redaction + per-label limit missing | MAJ | Fixed — FR-026/027; S-7.1/S-7.2; orders 17/18; M-7 |
| MAJ-011 UI/journey/a11y/design-system sections missing | MAJ | Fixed — UI screens and states table, user journey, accessibility and keyboard, design system sections present |
| MAJ-012 browser matrix underpowered | MAJ | Fixed — Mode 1: Chromium+Firefox; Mode 2: +WebKit; named isolation projects, `retries: 0`, Firefox; Safari holdout H-2 |
| MAJ-013 redirect rule would 502 Mode 1 | MAJ | Fixed — FR-013/S-5.4 scoped Mode 2 only; Mode 1 pass-through; M-8; AF-6 |
| MIN-001 docs requirement drops F794-7 and does not name the doc | Fixed — FR-018 names `docs/previews.md` and carries F794-7, the blank-preview troubleshooting note, the dual-URL explanation and the storage-reset note |
| MIN-002 FR-009 and FR-007 have no asserting test | Fixed — FR-009: order-2 RED + order-24 E2E + mutations M-1/M-2; FR-007: order 8 (TestPreviewSSRF_EmptyRegistry404) |
| MIN-003 redirect alias set undefined; conflicts with canonical-only CSP sources | Fixed — alias set enumerated from `libraryIsolationOrigins`' loopback branch (canonical + `127.0.0.1`/`localhost` same-port, `::1` excluded); Mode 2 CSP sources stay canonical-only; FR-013/FR-019 |
| MIN-004 shared origin-validity helper home | MIN | Fixed — FR-019: `pkg/gateway/middleware` beside `::CanonicalGatewayOrigin`; import-cycle reasoning stated |
| MIN-005 preflight wildcard headers | MIN | Fixed — FR-012 pins methods and no wildcard; S-2.8; order 15 |
| MIN-006 registered name is `serve_web`; no Reachability section | Fixed — `serve_web` (`pkg/tools/web_serve.go::ToolNameWebServe`) used throughout; Reachability section added (registration, card, panel, tool text) |
| MIN-007 canonical-origin edge cases missing from DS-3 | Fixed — DS-3 rows 6–12: wildcard, trailing dot, port-mapped listener, LOCALHOST case, implicit-80, wrong-port fall-through |
| MIN-008 regression table omits touched existing tests | Fixed — regression table names them: hot-flip, csrf-realmux, redaction inventory guard, token revocation/TTL suites, warmup spec, the strip-all rewrite, Library/mail suites, Playwright projects |
| OBS-001 `libraryIsolationOrigins` rename | OBS | Adopted the review's recommendation — no rename; FR-019 records why |
| OBS-002 a new label is a new origin — the app's storage and login are lost | Fixed — FR-018's storage-reset docs note; user-facing so it is not reported as a bug |
| OBS-003 line-number citation and off-branch SHA | Fixed — `file::symbol` citations throughout; codebase context pins this branch at `2129aa458` |

## Handoff

**Status:** round-2 spec, all round-1 findings dispositioned above (none skipped; no dispute stands — AF-1…AF-6 are ADR-wording flags for the architect, not spec gaps).
**Founder decisions logged verbatim:** Q1 (credential filter), Q2 (one-link card + tool-result shape), Q3 (nav-guard exemptions), Q4 (planted-cookie recovery); security-lead rulings F-1/F-2/F-3 folded into FR-021/FR-005 and the TDD plan.
**Out of scope, unchanged:** ADR-067 opaque-origin model (rejected), Library/mail preview surfaces, tokens/TTLs/revocation, ADR-044 consolidation and config keys, `contracts/` schemas, opener-severing UX.
**Implementation sequence:** orders 1–24 (Go unit → integration RED → integration → vitest → E2E); `qa-lead` RED from this spec; `backend-lead` + `frontend-lead` GREEN; `security-lead` re-review of the credential filter, SSRF rule, planted-cookie path and redaction before the gate.
