# ADR-094: Preview isolation with full capability — two serving modes (issue #798)

- **Status:** Accepted (2026-09-26 — every founder question answered: F794-1…6 plus F794-7, option A, §10; two founder-decided corrections from the #798 spec grill's round 1 applied same day — see the second correction note)
- **Date:** 2026-09-26 (correction round 1 applied same day — this ADR gets exactly one grill and one correction round, per the feature-flow rule; the spec grill's round 1 then surfaced two factual ADR gaps, corrected same day by founder decision)
- **Author:** architect (gateway-security squad)
- **Deciders:** founder decided F794-1 … F794-7 on 2026-09-26 (`ADR-094-founder-decisions.md`, authoritative — F794-7 accepts §10's option A); team-lead grill round complete (review of 2026-09-26, `ADR-094-preview-isolation-model-review.md`); founder decided the two spec-review corrections on 2026-09-26 (CRIT-002's allowlist rule; CRIT-003's Q1 option A) — security-lead reviews both changes before landing
- **Evidence level:** highest used — 1 (user-input: issue #798 acceptance + founder decisions F794-1…7) + 3 (documented pattern: the ADR-067 Library policy and its measured defects; the review's browser measurement) + 5 (code read this session, `file::symbol` cited) + external browser-platform facts cited in §7 with sources
- **Builds on:** "ADR-067 — Omnipus knowledge base and render-first preview" §10.3 and its dated amendments (2026-08-23, 2026-09-09, 2026-09-14); "ADR-044 — Serve /preview/ on the main gateway listener (path approach)"; the email spec's MC-10/MC-37 (`docs/internal/specs/email-mail-view-spec.md`, branch `feature/email-mail`)
- **Supersedes-in-part:** "ADR-044 — Serve /preview/ on the main gateway listener (path approach)" FR-3's residual-risk text (previewed-app login), re-based on F794-1/F794-2: FR-3 is now guaranteed by design, not accepted as a residual; and FR-013's request-side strip-everything rule (`Cookie`/`Authorization`), replaced by the dev-proxy credential filter (second correction note, item 2)

---

**Correction (round 1, 2026-09-26).** The first round of this ADR recommended converging web_serve on the ADR-067 sandbox model (opaque origin + path confinement). The founder rejected that model — F794-1 ("that is not an option because the preview is intended to run full web apps, full functional") and F794-2 ("everything, we need to be able to run full web apps, it is for software development"). The Decision below is rebuilt on those constraints. Two factual errors the review proved are also corrected:

1. §2.3/§6 of round 1 claimed the built-in browser panel "consumes no CSP". **Wrong** — it is a real Chrome that navigates to the same `/preview/` URL and enforces the same response headers (MAJ-001). The screencast is only the pixel transport. In the new model this becomes a feature: the agent sees exactly what the user sees.
2. §2.1 clause 9 of round 1 described a degraded `'self'` mode reachable in the "wildcard bind, no `gateway.public_url`" shape. **Wrong** — web_serve already fails closed there (`pkg/tools/web_serve.go::previewOriginUnresolvedResult`); no URL is ever minted (MAJ-002).

The title changed too: round 1's "one isolation model for every served preview surface" is no longer the claim. The Library and mail surfaces keep the ADR-067 sandbox model — their payloads are untrusted *content*, not apps, and no founder decision touches them. **web_serve deliberately diverges.**

---

**Correction (2026-09-26, spec-review round — CRIT-002 / CRIT-003).** The #798 spec grill's round 1 (`docs/internal/specs/adr-094-preview-isolation-spec-review.md`, verdict BLOCK) found two factual gaps in this ADR: places where the Decision as written cannot be built without contradicting F794-1/F794-2. The founder decided both on 2026-09-26; neither re-opens F794-1…F794-7 or §10. `pkg/security` is a standing security focus area, so security-lead reviews both changes before landing.

1. **The SSRF allowlist blocks the agent's own browser from every Mode 1 URL (CRIT-002).** The gateway-origin SSRF exception admits only the exact gateway host plus a path under `/preview/` (`pkg/security/ssrf.go::isAllowedGatewayOrigin`, `::requiredGatewayOriginPathPrefix`; scope set by "ADR-073 — Scope the gateway-origin SSRF exception to `/preview/*`, not the whole origin"). A Mode 1 URL `http://<label>.localhost:<port>/` fails both tests, falls through to the full SSRF path, resolves to loopback and is blocked — so §6's "renders in the agent's built-in browser panel" was unachievable in Mode 1, and §5/§11 never mentioned `pkg/security` at all. **Corrected allowlist rule (normative):** the exception gains a second, disjoint host class — admit `http(s)://<label>.localhost:<gwPort>/<any path>` if and only if **all** of: (a) a configured gateway origin's host is exactly `localhost` — the check `isAllowedGatewayOrigin` performs; not itself the Mode-1 gate, see the F-1 precision note below; (b) the URL's literal, pre-resolution host is one label followed by exactly `.localhost` — no second dot, no other suffix; (c) the label, lower-cased, matches §2.2's normative-2 grammar exactly (letters, digits, hyphens; 1–63 chars; no leading or trailing hyphen); (d) the port equals the configured gateway port. **Nothing broader — never a wildcard `*.localhost` accept**, which would reopen SSRF to any locally-resolvable subdomain an attacker names (a hostile resolver answering for `evil.localhost` would then go unexamined). **F-1 precision (security-lead ruling):** the admission is deployment-agnostic by construction — the browser-dedicated checker clone is wired with a hardcoded `localhost` host (`pkg/agent/loop_wire.go` `browserSSRF`), so the label class passes the SSRF layer wherever the grammar and port match, on any deployment shape; condition (a) does not by itself make the admission Mode-1-only, and the real Mode-1-vs-Mode-2 gate is the label registry: non-Mode-1 deployments mint no labels, so every label-class URL 404s via the empty registry (spec FR-021 keeps (a)–(d) as the admission iff and states the same). Blocked, and negative-tested: `<label>.localhost:<other-port>`, `<label>.example.com`, `a.b.localhost`, malformed labels (`-lead`, `trail-`, `_under`, 64 chars), and the bare gateway host outside `/preview/` (ADR-073's path scope is unchanged — only the label host class gains any-path). The `127.0.0.1`/`::1` loopback-literal equivalence is **not** extended to the label class (Mode 1 hosts are label-form only, §2.4). Layering: the SSRF grant is network reachability for the host class only; per-label authorization stays at the gateway host mux (an unknown or unregistered label is a 404, §2.2 normative 1/3), and guessing a label is guessing ~2^128 of mint entropy (§2.2 normative 2). The wiring is the existing browser checker clone (`pkg/security/ssrf.go::CloneWithGatewayOrigin`, wired per the `::AllowGatewayOrigin` doc comment in `pkg/agent/loop.go`) — no new allowlist mechanism, and the shared singleton that provider and skill-installer URL validation consult gains nothing beyond it.
2. **The dev proxy strips every `Cookie` and `Authorization` header, so a previewed app's server-side login can never persist (CRIT-003).** `pkg/gateway/rest_preview.go::proxyDevRequest`'s Director deletes both headers wholesale (its own comment: "a browser-cookie session INSIDE the previewed app does not persist across requests. Deliberate"), while §2.1's capability contract promised cookie-backed login in both modes — the contradiction starts in this ADR, and it defeats F794-1/F794-2 ("full web apps … login"). **Corrected credential filter (founder decision Q1, option A — normative, both modes):** in the request direction, strip only Omnipus's own credentials — (a) `Cookie`: drop only the cookie pairs whose name is exactly `omnipus-session`, `csrf` or `__Host-csrf` (exact, case-sensitive name match; forward every other pair; omit the header entirely when nothing remains); (b) `Authorization`: delete only a `Bearer` credential whose token matches one of the gateway's own registered secret values (the credential store injected at boot — `pkg/gateway/CLAUDE.md`, "Credential boot order"); any other `Authorization` value forwards unchanged. Everything else the browser sends forwards untouched. This supersedes the FR-013 strip-everything rule of "ADR-044 — Serve /preview/ on the main gateway listener (path approach)" on the request side only: the threat FR-013 closed (the app reading the operator's session/CSRF values) stays closed by name-scoping instead of header deletion. The response direction is unchanged — `pkg/gateway/rest_preview.go::neutralizeReservedSetCookies` keeps dropping upstream `Set-Cookie` for the reserved names (anti-fixation) — and the previewed app's own `Set-Cookie` now survives to its next request, which is what makes its login persist. In Mode 1 the filter removes only tossed `Domain=localhost` reserved names (host-only cookies never reach the label host, §2.2); one code path serves both modes, so no mode-conditional behaviour exists. `docs/previews.md`'s "Logins inside a development preview do not stick" line is now wrong; the spec fix updates it (separate dispatch; docs-verifier audits).

---

## 1. Context

### 1.1 The finding (issue #798)

Agent-generated HTML served as a preview can run script that calls the gateway's authenticated `/api/v1/*` endpoints as the logged-in user, including state-changing requests. Every step of that chain is present in today's code:

| # | Chain step | Evidence |
|---|---|---|
| 1 | One origin: `/preview/` is registered bare on the main gateway listener; no separate preview origin | `pkg/gateway/rest.go::registerPreviewEndpoints`; `pkg/gateway/rest_preview.go::HandlePreview` |
| 2 | The web_serve preview CSP admits the whole gateway: bare `'self'` in source directives, `connect-src 'self'`, `form-action 'self'`, **no `sandbox` directive** | `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` |
| 3 | Inline script executes (`script-src 'unsafe-inline'`) | `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` |
| 4 | The session cookie is gateway-wide and host-only: `omnipus-session`, `Path=/; HttpOnly; SameSite=Strict`, **no `Domain` attribute → host-only by construction** | `pkg/gateway/middleware/session_cookie.go::WriteSessionCookie`, `::IssueSessionCookie` |
| 5 | The CSRF cookie is script-readable (`HttpOnly:false`) and its value must be echoed in `X-Csrf-Token` on every POST/PUT/PATCH/DELETE; GET is not gated; Bearer-authenticated requests skip the gate entirely | `pkg/gateway/middleware/csrf.go::CSRFMiddleware`, `::IssueCSRFCookie`, `::stateChangingMethods`; SPA reads it at `src/lib/api/http.ts::readCSRFCookie` |
| 6 | The API accepts the session cookie, so those requests authenticate as the user | `pkg/gateway/middleware/session_cookie.go::RequireSessionCookieOrBearer` |
| 7 | The user's only action is opening a preview their own agent produced: the SPA opens previews as a top-level tab, `target="_blank" rel="noopener noreferrer"` | `src/components/chat/IframePreview.tsx` (all four open paths carry `noopener noreferrer`) |

What limits it today (corrected per review MIN-006 — round 1 understated this): `connect-src 'self'` blocks only **fetch-class** egress to third-party origins. CSP does not govern top-level navigation, so the script can read API data as the user and send it out via `location = …`, `window.open` or an anchor. It can also drive the SPA itself — same origin, no sandbox, `window.opener`-free but popup-capable. Today's preview is uncontained same-origin script.

### 1.2 The founder constraints (2026-09-26, authoritative)

| ID | Decision |
|---|---|
| F794-1 | Dropping in-preview login/storage (ADR-044 FR-3) is rejected: "the preview is intended to run full web apps, full functional." |
| F794-2 | Full browser capability is required: "everything … scripts, storage, cookies, login, forms, popups, downloads." No capability may be sandboxed away. |
| F794-3 | Cross-origin reads of files under a preview's token prefix are accepted (the token in the URL is the access key). |
| F794-4 | Accepted and to be documented: an agent-built page could imitate an Omnipus login screen at the real address. |
| F794-5 | Previews run under `/preview/` as today, **or** on a subdomain Omnipus generates itself "within the Omnipus app alone without dependencies on the environment" — no DNS setup, no extra configured port, no new configured origin. A separately configured origin/port is rejected. |
| F794-6 | Misconfigured origins (wildcard `public_url`, unparseable origin) fail closed — team-lead's stated default, not objected to by the founder. |

### 1.3 Three surfaces, deliberately two models

Omnipus serves untrusted browser-rendered content in three places. The ADR-067 sandbox model (opaque origin, path-confined sources, `connect-src 'none'`) remains correct for the first two because their payloads are untrusted **content**: a Library note and a sanitized mail have no legitimate use for storage, cookies or login.

| Surface | Prefix | Payload | Model |
|---|---|---|---|
| Library preview | `/library-preview/{token}/…` | knowledge-base notes, rendered bundles | ADR-067 sandbox model — unchanged (`pkg/gateway/library_isolation_policy.go`) |
| Mail HTML preview | `/mail-preview/…` | sanitized email HTML | Same model, spec'd (MC-10, MC-37) — unchanged |
| web_serve preview | `/preview/{agent}/{token}/…` | **full agent-built web apps** | **This ADR — two serving modes** |

The round-1 goal of converging all three on one model is dropped as a goal. The new convergence point is the **capability contract** (§2.1) and the shared origin-derivation helper (§2.6), not the policy string.

### 1.4 Why this is an ADR and why now

The residual round 1 accepted is now founder-rejected, and issue #798 (`security`/`priority:P1-high`/`area:gateway`) stays open. The mail surface is about to multiply the number of untrusted-HTML surfaces; web_serve's model must land before that.

---

## 2. Decision

**web_serve previews run full web apps in two serving modes.** No CSP `sandbox` directive exists anywhere in the web_serve model; storage, cookies, login, forms, popups and downloads are intact (F794-1, F794-2).

- **Mode 1 — self-generated per-preview subdomain of the gateway host** (`<label>.localhost:<port>`), served by Host dispatch inside the single Go binary. Primary wherever the browser can resolve it with zero environment setup (F794-5's second option). Issue #798 is **structurally dead** here: the session and CSRF cookies are host-only, so the preview origin holds no gateway credential and is cross-origin to the API.
- **Mode 2 — the `/preview/` path, same origin, full capability, plus a server-side control stack.** The fallback wherever Mode 1 cannot resolve. #798 is closed for every direct path (fetch, form, redirect, service worker, navigation, iframe); one residual — same-origin popup scripting of the SPA — cannot be closed by any header stack and is **accepted as a documented residual (F794-7, §10)**.

### 2.1 The capability contract (what "full web app" means here)

| Capability | Mode 1 (subdomain) | Mode 2 (path) |
|---|---|---|
| `localStorage` / `sessionStorage` / IndexedDB | ✓ real origin | ✓ real origin (no sandbox — nothing throws) |
| Cookies and login for the previewed app | ✓ its own origin's cookie jar | ✓ same-origin cookies; dev-proxy upstreams see no gateway cookies — only the reserved names are stripped, the app's own cookies forward (credential filter, spec-review correction CRIT-003 — supersedes FR-013's strip-everything) |
| Forms | ✓ unconstrained | ✓ within the token prefix (`form-action` confined — the app's own posts work) |
| Popups, `window.open`, `target=_blank` | ✓ | ✓ |
| Downloads, modals | ✓ | ✓ (no `sandbox` flag list exists to omit them — round-1 MAJ-004 dissolves) |
| Module scripts, `crossorigin`, `fetch('./data.json')` | ✓ same-origin, no CORS needed | ✓ via prefix-wide `Access-Control-Allow-Origin: *` (F794-3) **plus the MIME-map additions** (`.mjs` → `text/javascript`, `.woff`/`.woff2` → `font/*`, `.wasm` → `application/wasm` — module scripts are MIME-blocked under `nosniff` without them, spec FR-017) — the review measured these failing blank without ACAO |
| Dev-server HMR (`ws://`) | ✓ own host, native | ✓ `connect-src` carries the `ws`-scheme form of the prefix (round-1 OBS-003) |
| Root-absolute assets (`/assets/x.js`, `/@vite/client`) | ✓ **fixed** — the app owns the whole host | ✗ unchanged (round-1 Negative 2 stands: they resolve against the gateway root, outside the prefix) |

### 2.2 Mode 1 — the self-generated subdomain

**URL shape.** `http://<label>.localhost:<port>/…`, where `<label>` is a fresh preview label minted with the registration. Minted by the app, resolved by the browser — no DNS record, no hosts-file edit, no extra port, no new configured origin. That is F794-5's second option literally.

**Applicability (narrow, and stated honestly):** only when the canonical origin is exactly `http://localhost:<port>` (§2.4). Grounding: Chromium, Firefox and Edge resolve `*.localhost` to loopback internally per RFC 6761, without querying DNS; Safari/WebKit delegates to the system resolver, which does not (cited §7). On a LAN IP, a real domain, or Tailscale, `*.localhost` is unresolvable — MagicDNS has no wildcard support (tailscale/tailscale#1196) and its device certificates cover only the exact device name. TLS for dynamic subdomains would need a wildcard certificate (requires domain + DNS) or a private CA installed into browser trust stores — both environment dependencies, both rejected by F794-5. Mode 1 is loopback HTTP only.

**Why #798 dies structurally.** Each step of §1.1's chain, killed without any CSP:

| Chain step | Killed by |
|---|---|
| Session cookie rides the preview's requests | The cookie is **host-only** — no `Domain` attribute is ever set (`pkg/gateway/middleware/session_cookie.go::WriteSessionCookie`), so the browser sends it to `localhost` only, never to `<label>.localhost` |
| CSRF token readable | `__Host-csrf` is host-only by prefix construction (RFC 6265bis: `__Host-` forbids `Domain`, `pkg/gateway/middleware/csrf.go::CSRFCookieName`); the plain-HTTP `csrf` fallback also sets no `Domain` (`::IssueCSRFCookie`). Different host → not sent, not readable |
| `fetch('/api/v1/…')` | Cross-origin request with no ambient credential → 401; CORS blocks even reading the error |
| Scripting the SPA (popup path) | The popup is **cross-origin** → same-origin policy blocks DOM access. `window.opener` is already severed: every preview link carries `rel="noopener noreferrer"` (`src/components/chat/IframePreview.tsx`) |
| WebSocket to the API | Same credential absence — the handshake carries no session cookie |

**Enforcement in the binary (normative for the implementer):**

1. **Host dispatch before the mux.** A request whose `Host` is `<label>.localhost(:port)` with a syntactically valid label goes to a preview-host mux; that mux mounts **exactly one handler — preview serving** (registry lookup by label, reusing `HandlePreview`'s registry logic with the agent resolved from the registration, so the token↔agent mismatch rule is preserved); **no gateway API/auth/SPA handler is reachable under a preview Host** (spec FR-006) — fail-closed by construction. Within that mount, a static preview's file server answers 404 for unknown paths, and a dev preview forwards **every** path to the upstream (the previewed app's own `/api/v1/todos` must work). Every other `Host` reaches today's main mux unchanged.
2. **Label grammar.** A Mode 1 label is DNS-safe: letters, digits, hyphens; 1–63 chars; no leading or trailing hyphen. The current preview tokens are 43-char base64url (`pkg/gateway/preview_token.go::Mint`, shape borrowed from `pkg/agent/served_subdirs.go`), which contains `_` — **not** hostname-safe — so Mode 1 mints its own label from fresh entropy in a hostname-safe encoding (e.g. base32 of 16 bytes ≈ 26 chars), mapped to the same registry entry. The existing token remains the Mode 2 credential.
3. **Host validation.** Exact match against the label grammar plus the literal `localhost` suffix and the listener port; anything else falls to the main mux. An attacker-resolvable label without a live registration is a 404. (The main mux has no Host allowlist today — only the WebSocket origin check reads `r.Host`, `pkg/gateway/websocket.go` — that is today's posture and unchanged; a DNS-rebinding page can reach only tokenless preview 404s, because host-only cookies never travel cross-host.)
4. **SSRF admission for the agent's panel.** The gateway-origin SSRF exception (`pkg/security/ssrf.go::isAllowedGatewayOrigin`) admits the Mode 1 label host class — `<label>.localhost` on the configured gateway port, label per normative 2's grammar, any path, when a configured gateway origin's host is exactly `localhost` (F-1: admission is deployment-agnostic by construction — the browser checker clone is wired with hardcoded `localhost`, `pkg/agent/loop_wire.go` `browserSSRF` — so this condition is not the Mode-1 gate; the empty label registry is: non-Mode-1 deployments mint no labels, and every label-class URL 404s via it) — so the built-in browser can open the primary URL; the bare gateway host keeps ADR-073's `/preview/` path scope untouched. Full rule and negative-test set: the spec-review correction note at the top of this ADR.

**Residuals (named, bounded):**

- **Cookie tossing.** A previewed app on `<label>.localhost` can set `Domain=localhost` cookies — same-site, so `SameSite=Strict` does not block them, and they **do ride** label-origin requests to the gateway (spec MAJ-003's corrected premise: Mode 1's real barriers are the CORS origin-reflection refusal, the header-only CSRF gate, and the WebSocket origin check — never cookie absence). Bounded: it cannot mint a *valid* session value (bcrypt-verified server-side, `session_cookie.go::ResolveUserFromCookie`); `__Host-csrf` cannot be tossed (the prefix makes browsers refuse `Domain`'d cookies) on TLS; the plain-HTTP `csrf` fallback name is tossable in principle. For a tossed duplicate, RFC 6265 §5.4's Cookie-header construction (longest request-path first, earliest-created first at equal length) decides which value `r.Cookie` and the SPA read first — the genuine earlier-created cookie normally wins, but the `Cookie` header itself carries no path attributes, so ordering is not a detection mechanism. Detection (spec FR-015): more than one occurrence of a reserved gateway cookie name (exact match: `omnipus-session`, `csrf`, `__Host-csrf`) in the `Cookie` header fails the credential read for that request; the gateway clears the planted duplicates in the same response — clear lines (empty value, `Max-Age=0`, past `Expires`) for the affected name at the request path, every ancestor directory, and `/`, in both `Domain=localhost` and host-only forms, bounded at 2 × (path depth + 1) lines per name — and answers state-changing methods with the `planted_cookie_cleared` retry error.
- **Phishing at the real address** (F794-4) — accepted and to be documented, unchanged.
- **WebKit/Safari, LAN-IP, real-domain, Tailscale, HTTPS, second-device** access — all get Mode 2 or nothing (§2.4).

### 2.3 Mode 2 — the `/preview/` path with the control stack

**Shape.** URL, tokens, TTLs and revocation unchanged; same origin as the API; **no `sandbox` directive** (F794-1/F794-2). The boundary moves from the sandbox to server-side controls. In same-origin mode the CSRF double-submit is *not* itself a boundary — same-origin script reads the cookie by design (`src/lib/api/http.ts::readCSRFCookie`) — so the load-bearing control against direct API calls is CSP source confinement; everything else is layered around it.

**The literal policy (the tripwire oracle; `${ORIGIN}` = canonical origin, `${PREFIX}` = `/preview/{agent}/{token}/`, `${ORIGIN-WS}` = the `ws://`/`wss://` form of the canonical origin):**

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

Notes: `script-src` gains `'unsafe-eval'` — dev-server bundles commonly need it, F794-2 grants it, and it does not weaken #798 defence because CSP governs the *network* of the document, however the script was produced. `blob:` workers inherit the creating document's policy, so confinement holds inside them. The origin list is frozen at boot; the string is byte-stable per `(agent, token)` (review MIN-001). Agent-ID and token go into `${PREFIX}` percent-encoded to the RFC 3986 unreserved set — `validation.EntityID` admits `;`, `,`, spaces and quotes, any of which would split directives (review MIN-002); the builder must never interpolate raw registry values.

**The control stack — what stops a same-origin, full-capability page from calling `/api/v1` as the user:**

| # | Attack path | Control | Engine reach |
|---|---|---|---|
| 1 | `fetch` / XHR / `EventSource` / `sendBeacon` to `/api/v1/*` | `connect-src` confined to the token prefix — CSP3 host-source path matching is by request URL and engine-independent; the Library measured three engines under exactly this matching (ADR-067, 2026-09-14) | All engines |
| 2 | Form POST to `/api/v1/*` at any nesting | `form-action` confined to the token prefix | All engines |
| 3 | Top-level navigation GET to `/api/v1/*` | Server rejects any `/api/v1` request carrying `Sec-Fetch-Dest: document` (a navigation has no business on a JSON API) | Chromium 76+, Firefox 90+, Safari 16.4+ (verified §7); older engines lack the header — mitigated by the GET-side-effect-free invariant (audit action §5) and by the fact that CSRF already gates only POST/PUT/PATCH/DELETE (`csrf.go::stateChangingMethods`) |
| 4 | Service-worker registration from the preview (a SW could outlive the page and re-request) | Server refuses any `/preview/` request carrying `Service-Worker: script` (and `Sec-Fetch-Dest: serviceworker`) — the header is spec-mandated on SW script fetches | All SW-capable engines |
| 5 | Iframing the SPA and scripting the frame | SPA responses already carry `frame-ancestors 'none'` (absorbed into the single SPA CSP header, `pkg/gateway/embed.go`, ADR-067 FR-006b) — **already closed, verified this session** | All engines |
| 6 | `window.opener` access to the opening SPA | Already severed: every preview link carries `rel="noopener noreferrer"` (`src/components/chat/IframePreview.tsx`) — **already closed, verified this session** | All engines |
| 7 | **Popup scripting of the SPA**: `window.open('/')` then same-origin DOM automation of the real UI (clicking the app's own buttons makes its own authenticated, CSRF-correct calls) | **None exists.** The popup is same-origin, so SOP permits full DOM access; COOP only severs *cross*-origin openers; the SPA's `script-src 'self'` (`pkg/gateway/embed.go::spaBaseContentSecurityPolicy`, no `unsafe-inline`/`unsafe-eval`) blocks *injected script* but not DOM automation; `window.open` is user-gesture-gated and a spoofed click supplies one. **Residual accepted — F794-7** | — |
| 8 | Redirect pivots (upstream 302 bounces a subresource or fetch to `/api/v1`; CSP3 §6.7.2.9 discards a source's path after redirect) | The one normative redirect rule (below) | All engines |
| 9 | Upstream-supplied CSP/XFO weakening the policy | Already stripped (`pkg/gateway/rest_preview.go` FR-007d, verified); a `<meta http-equiv="Content-Security-Policy">` can only tighten — multiple policies intersect | All engines |
| 10 | Session/CSRF fixation via upstream `Set-Cookie` | Already neutralized for reserved gateway cookie names (`rest_preview.go::neutralizeReservedSetCookies`, verified) | All engines |
| 11 | Cross-origin reads of preview bytes | **Accepted** (F794-3): every successful response under the prefix, static and proxied, carries `Access-Control-Allow-Origin: *` and never `Access-Control-Allow-Credentials`; on proxied responses this overrides any upstream ACAO/ACAC, deleted first in `ModifyResponse` | — |
| 12 | CORS-mode subresources (`<script type="module">`, `crossorigin` stylesheets, `fetch('./data.json')`) fail blank from an unsandboxed-but-confined page — without ACAO, and module scripts additionally MIME-blocked under `nosniff` without the `.mjs` mapping | `ACAO: *` (row 11) **plus the MIME-map additions** (`.mjs` → `text/javascript`, `.woff`/`.woff2` → `font/*`, `.wasm` → `application/wasm` — §5's MIME row, spec FR-017); the review measured module scripts and fetches failing without them and succeeding with them on Chromium and WebKit | Chromium, WebKit measured; Firefox per the same Fetch standard |

**The one normative redirect rule — Mode 2 (path mode) only; Mode 1 passes every `Location` through unmodified** (spec FR-013; the scoping is round-1 MAJ-013's finding — applied to Mode 1 the rule would 502 the app's own login redirects). It replaces round-1's inconsistent clause 6 / §2.2 / §8 triad (review MAJ-003). In `pkg/gateway/rest_preview.go::proxyDevRequest`'s `ModifyResponse` (the Mode 2 dev-proxy path):

1. When status ∈ {301, 302, 303, 307, 308}: resolve `Location` against the request URL with WHATWG parsing (dot-segments and `%2e` normalised).
2. If the result's origin is the gateway origin (any alias) **and** its path begins with `/preview/{agent}/{token}/`, emit it.
3. Else, if the raw value was root-relative, re-root it under the token prefix and apply step 2 again.
4. Otherwise replace the response with 502 and no `Location`.

A `Location` header on any non-redirect status is deleted. `304 Not Modified` is allowed untouched (dev servers send it routinely). The tripwire fails on any redirect whose `Location` resolves outside the token prefix, and must include the dot-segment, `%2e`, protocol-relative (`//host/…`), backslash (`/\host`) and absolute-same-origin (`http://127.0.0.1:5000/api/v1/x` — the Director does not rewrite `Host`, so dev servers can emit the gateway host) cases. Mode 1 applies none of the above — every `Location` passes through unchanged: the app owns the whole host, so any same-origin redirect is in-scope by definition; only a cross-origin redirect to the *gateway* origin would matter, and the host mux does not route it.

**The dev-proxy credential filter (spec-review correction CRIT-003; founder decision Q1, option A).** The Director of `pkg/gateway/rest_preview.go::proxyDevRequest` no longer strips every `Cookie` and `Authorization` header — that made a server-side login inside the previewed app impossible, defeating F794-1/F794-2. In both modes it strips only Omnipus's own credentials: `Cookie` pairs named exactly `omnipus-session`, `csrf`, `__Host-csrf` (every other pair forwards; the header is omitted when nothing remains), and an `Authorization: Bearer` value matching one of the gateway's own registered secret values (any other `Authorization` value forwards). Everything else the browser sends forwards unchanged; `pkg/gateway/rest_preview.go::neutralizeReservedSetCookies` keeps neutralizing upstream `Set-Cookie` for the reserved names on the response side. Full rule, layering and the ADR-044 FR-013 supersedes-note: the spec-review correction at the top of this ADR.

**What Mode 2 cannot stop (named):** row 7's popup scripting (accepted, F794-7); navigation-based egress is bounded but real — a navigation cannot read the response back, and reads require the fetch class row 1 blocks; the fake-login residual (F794-4, document it); and SPA deep links that mutate state from URL parameters on load (review OBS-002) — an invariant, not a control: "no SPA route performs a state change from URL parameters on load", audited by security-lead before this lands.

### 2.4 Mode selection and fail-closed rules (F794-6)

Decided at web_serve mint time from `middleware.CanonicalGatewayOrigin(cfg)` — the one derivation, `pkg/gateway/middleware/origin_canonical.go`:

| Canonical origin | Mode | Rationale |
|---|---|---|
| `http://localhost:<port>` | **Mode 1**, plus the Mode 2 URL returned as fallback | Browsers self-resolve `*.localhost`; plain HTTP needs no certificate |
| `https://localhost:<port>` | Mode 2 only | `*.localhost` certificates need a private CA in browser trust stores — environment dependency |
| Any IP literal (`127.0.0.1`, LAN IP) | Mode 2 only | Browsers do not resolve subdomains of IP literals (§7, Inferred-high) |
| Real domain or Tailscale name | Mode 2 only | Wildcard DNS or wildcard certificates are environment dependencies (F794-5); MagicDNS resolves flat device names only (tailscale#1196) |
| Wildcard-host `public_url` (e.g. `http://*.example.com`), unparseable origin | **Refuse — fail closed** | F794-6: operator misconfiguration, not a hosting limit |
| Empty (wildcard bind, no `public_url`) | **Refuse — fail closed** | Already today's behaviour (`pkg/tools/web_serve.go::previewOriginUnresolvedResult`) — unchanged |

The tool result returns **both** URLs when Mode 1 applies (primary subdomain + `/preview/` fallback), so WebKit users and non-loopback deployments keep a working link. Degraded `'self'` fallback modes no longer exist in the web_serve model: misconfigured origins are refused (F794-6), so the Mode 2 policy is always emitted in full. The Library keeps its own degradation posture — unchanged scope.

### 2.5 What does not change

- **Library and mail previews** keep the ADR-067 sandbox model — untrusted content, not apps (§1.3); their CSP, opaque-origin and no-redirect machinery is untouched by F794-1…6.
- **The SPA** (`src/components/chat/IframePreview.tsx`, `src/lib/preview-url.ts::resolvePreviewHref`) — no changes required for isolation itself; the link-selection card (founder Q2: detect the browser, render the one link that works, re-validating the new `isolated_url` tool-result field client-side) is additive SPA work riding the `// not-wire-format`-opted tool-result payload; when Mode 1 applies the link target is the subdomain URL.
- **ADR-044's consolidation** — one listener, deleted `preview_port`/`preview_host`/`preview_origin`/`preview_listener_enabled` keys, `gateway.preview_enabled` hot-flip: all untouched. No new config keys; no environment setup.
- **The built-in browser panel** — enforces whatever the served response carries (corrected per MAJ-001); with no sandbox, apps work identically for the agent's review and the user's tab.
- **Contracts** — no wire-format change: preview URLs travel in tool-result text, not in contract-typed schemas; no `openapi.yaml`/`asyncapi.yaml` delta.

### 2.6 Shared origin derivation (review MAJ-005)

One neutral helper derives the origin list for all three surfaces: rename `pkg/gateway/library_isolation_policy.go::libraryIsolationOrigins` to `previewIsolationOrigins` and share it — its concrete-host fail-closed check (blocks a wildcard `public_url`), `public_url` normalisation, IPv6-expressibility check and loopback handling are measured, load-bearing logic that must not be re-implemented per surface. The per-surface *policy templates and their tripwires* stay separate (the mail spec's MC-37 rationale stands: shared builders drift together). `pkg/gateway/rest_workspace.go::resolveMainOrigin` is retired for preview responses; its remaining callers are listed in the blast radius. Mode 2's CSP sources use the canonical origin only — web_serve mints absolute URLs, so loopback aliases buy nothing for rendering (review OBS-001, adopted).

---

## 3. Alternatives considered — rejected

*(Letters relabelled to avoid ADR-044's Option A/B collision — review MIN-004. ADR-044's "Option A" is the path approach, its "Option B" the separate origin.)*

| Alternative | Why rejected |
|---|---|
| **A. Second configured origin/port** (ADR-044's Option B) | F794-5 rejects environment-dependent origins outright; ADR-044 already deleted the second listener and its config keys. Re-opening it is a new founder decision, not a variant of this ADR. |
| **B. Public wildcard-DNS names** (`nip.io`, `sslip.io`) | Resolves without local setup, but depends on a public third-party DNS service and cannot obtain TLS certificates for dynamic subdomains — an environment/internet dependency, rejected under F794-5. |
| **C. Converge web_serve on the ADR-067 sandbox model** (round-1's Decision) | **Rejected by the founder** — F794-1/F794-2: the opaque origin makes `localStorage`, `sessionStorage`, IndexedDB and `document.cookie` throw (review-measured on Chromium and WebKit), so storage-backed apps crash and in-preview login is impossible. Previews are for full web apps. |
| **D. Cookie scoping** (`Path`/`SameSite`) | Near no-op against the issue's own chain (round-1's analysis stands): the script targets `/api/v1` directly; `SameSite=Strict` already set; the CSRF cookie must stay script-readable for the SPA. The decisive cookie fact is **host-only-ness** — already true (`session_cookie.go` sets no `Domain`) and it is Mode 1's load-bearing control. A single-`Path` scoping tweak may ride as defence-in-depth with the implementing PR if qa-lead's RED shows a residual subresource-cookie flow (round-1 MIN-005: a cookie has one `Path`; the two-path idea is dropped). |
| **E. Relocate the CSRF secret to SPA memory** (out-of-band double-submit) | Considered for Mode 2; rejected for now. With `connect-src` confinement holding (the load-bearing control), a CSP-bypassed attacker can re-fetch a fresh token from a cookie-authenticated session endpoint anyway, so the change adds SPA-auth churn (reload/re-mint flows, `src/lib/api/http.ts`) without closing the popup residual. Revisit only if qa-lead's RED demonstrates a concrete flow CSP does not cover. |

---

## 4. Consequences

### Positive

1. Issue #798's acceptance criterion is met **structurally** in Mode 1: the preview origin holds no gateway credential (host-only cookies) and is cross-origin to the API (SOP) — no CSP enforcement required, nothing for a future engine quirk to unwind.
2. F794-1/F794-2 hold in both modes: real origins, real storage, real cookie jars, login, forms, popups, downloads — the sandbox model is gone from web_serve.
3. Mode 1 **fixes** root-absolute assets (`/assets/x.js`, `/@vite/client`) and makes dev-server HMR native — round-1's Negative 2 no longer applies to Mode 1.
4. F794-3 is implemented once (prefix-wide `ACAO: *`), which also fixes the review-measured blank-render of module-script bundles in Mode 2.
5. The fail-closed rules (F794-6) shrink the degraded surface to zero for web_serve: every served preview has a canonical origin and a fully-confined policy.
6. Library and mail keep their proven model; a future fourth content surface still has the ADR-067 precedent to copy.

### Negative

1. **Mode 1's applicability is narrow**: `http://localhost` + Chromium/Firefox/Edge + HTTP only. WebKit/Safari users, IP-literal browsing, LAN/real-domain/Tailscale deployments, HTTPS ingress and second devices all land in Mode 2.
2. **Mode 2 keeps the popup residual** (§2.3 row 7): a determined agent-built page can popup-script the SPA and drive its UI as the user. Not closable by any header stack without the sandbox the founder rejected. F794-7 accepts this on the fallback path and requires it documented; the hosted version must not rely on the fallback (tracked in elicify-ai/omnipus-ai#1177).
3. **Two code paths** to build and test (host-dispatch mux + label registry; CSP builder + control stack), roughly doubling the preview test matrix.
4. **Cookie tossing** from a Mode 1 preview into the `Domain=localhost` namespace is possible (bounded, §2.2) and gets one hardening check (duplicate-cookie rejection on state-changing requests).
5. Test churn: `pkg/gateway/preview_iframe_test.go` frame-ancestors assertions change to `'none'`; the old wide-CSP string tests change.
6. Mode 2's `worker-src … blob:` and `'unsafe-eval'` widen *capability* within the document; they do not widen the network boundary, but the tripwire must assert the network sources stay confined.

### Neutral

1. `script-src 'unsafe-inline'` remains in Mode 2's policy (the app's script is the point), joined by `'unsafe-eval'`.
2. The SPA is untouched beyond the additive link-selection card (Q2); preview URL shapes on the path mode are unchanged; tokens, TTLs and revocation are unchanged.
3. `frame-ancestors 'none'` on preview responses (both modes) closes the iframe-embed option for now — embedding a preview inside the SPA needs its own analysis (a same-origin frame is script-reachable; a cross-origin Mode 1 frame can still attempt top-level navigation) and stays out of scope.
4. IPv6-literal readers (`[::1]`) keep today's documented limitation: the CSP cannot name them, rendering degrades, and the existing Library-style WARN explains why (unchanged scope).

---

## 5. Blast radius (of implementing this ADR)

| Area | Files | Change |
|---|---|---|
| Host dispatch + preview-host mux | new small file in `pkg/gateway` (name per backend-lead), wired in `pkg/gateway/gateway.go` / `gateway_boot.go` | Label-grammar validation, mux separation (404 everything non-preview under a preview Host), reuse of `HandlePreview`'s registry logic |
| Mode 1 label registry | extends the dev-server/static registries (`pkg/gateway`, `pkg/tools/web_serve.go::DevServerRegistry`) | Mint DNS-safe labels alongside tokens; map label ↔ registration |
| Mode selection + dual URLs | `pkg/tools/web_serve.go` (`Execute`, `previewOriginUnresolvedResult` extended) | F794-6 refusal table; primary + fallback URLs in the tool result |
| **Agent-facing tool text** | `pkg/tools/web_serve.go::Description` + relevant skill text — **owned by prometheus-prompt-engineer** | Teach both modes and Mode 2's asset rule (relative or under the prefix, e.g. Vite `base: './'`); the agent must produce a rendering preview unaided |
| Mode 2 CSP builder | `pkg/gateway/rest_workspace.go::buildWorkspaceCSP` retired for preview responses; new `pkg/gateway/webserve_isolation_policy.go` (name per backend-lead) | §2.3's literal template, percent-encoded `${PREFIX}`, boot-frozen origin list, per-`(agent, token)` byte-stable string, tripwires |
| MIME map | `pkg/gateway/rest_workspace.go::workspaceContentType` | Add `.mjs → text/javascript`, `.woff/.woff2 → font/*`, `.wasm → application/wasm` (review CRIT-001 verified these are absent) |
| CORS | `pkg/gateway/rest_preview.go::addPreviewCORSHeaders`, `::handleServePreviewPreflight` | Emit `ACAO: *` under the prefix (F794-3); delete upstream `ACAO`/`ACAC` in proxy `ModifyResponse` before overriding |
| Redirect rule | `pkg/gateway/rest_preview.go::proxyDevRequest.ModifyResponse` | §2.3's normative 4-step rule — **Mode 2 only**; Mode 1 passes `Location` through (spec FR-013) |
| Dev-proxy credential filter | `pkg/gateway/rest_preview.go::proxyDevRequest` Director | Replace strip-everything with the reserved-name `Cookie` filter + gateway-Bearer-only `Authorization` deletion (spec-review correction CRIT-003; supersedes ADR-044 FR-013's request half); tests: reserved names removed, mixed header filtered, app cookie + foreign Bearer forwarded |
| Fetch-Metadata + SW guard | middleware (new small guard) applied to `/api/v1` and `/preview/` | Reject `Sec-Fetch-Dest: document` on `/api/v1`; refuse `Service-Worker: script` under `/preview/` |
| SSRF gateway-origin exception | `pkg/security/ssrf.go::isAllowedGatewayOrigin` (+ `::AllowGatewayOrigin` / `::CloneWithGatewayOrigin`, wired per the `::AllowGatewayOrigin` doc comment in `pkg/agent/loop.go`) | Admit the Mode-1 label host class (grammar per §2.2 normative 2 + configured gateway port, gwHost == `localhost` — deployment-agnostic by construction, F-1: the empty label registry is the real Mode-1-only gate, any path); the bare gateway host keeps ADR-073's `/preview/` path scope; negative tests: wrong port, non-`localhost` suffix, multi-label host, malformed label, non-localhost gwHost, bare-host non-preview path (spec-review correction CRIT-002) |
| Origin helper | `pkg/gateway/library_isolation_policy.go::libraryIsolationOrigins` → `previewIsolationOrigins` (rename + share) | MAJ-005; retire `rest_workspace.go::resolveMainOrigin` for preview responses |
| SPA | none required for isolation; additive link-selection card (Q2) — `IframePreview.tsx` + `src/lib/preview-url.ts::resolvePreviewHref` read the new `isolated_url` field | Opener already severed; link target becomes the Mode 1 URL when applicable |
| User docs | preview/blank-render troubleshooting + the F794-4 phishing residual note | Drafted by the implementing lead, audited by docs-verifier (review MIN-007/MIN-008) |
| Tests | new: label grammar, host-dispatch 404s, redirect-rule cases, Fetch-Metadata/SW guards, CSP tripwires, hostile-agent-ID case, per-trigger fail-closed cases, SSRF label-host negative cases, dev-proxy credential-filter cases; updated: `preview_iframe_test.go`, `rest_preview_test.go`, `rest_preview_proof_test.go`, `rest_preview_web_serve_e2e_test.go` | §8 |
| Contracts | none | No wire-format change |

Audit actions riding with implementation: security-lead verifies the SPA invariant "no route mutates state from URL parameters on load" (review OBS-002) and backend-lead verifies "no GET on `/api/v1` mutates state" — both invariants are load-bearing assumptions of the control stack. Security-lead also reviews the two spec-review corrections before landing: the `pkg/security/ssrf.go` label-host admission (CRIT-002, with its negative-test set) and the dev-proxy credential filter (CRIT-003, including that the FR-013 session-riding threat stays closed by name-scoping).

---

## 6. Reachability (Definition of Done)

- A real user opens a web_serve preview from chat on a default loopback install in Chromium or Firefox: the link is `http://<label>.localhost:<port>/`, the app renders **with storage and login working** (a full app, not a static page).
- The same preview opened in WebKit/Safari lands on the Mode 2 URL and renders — module scripts, `crossorigin` CSS and `fetch('./data.json')` all succeed (the review-measured failure shape must not reproduce).
- A dev-server preview renders through the proxy in both modes, including the upstream-redirect case (rewritten Location stays in-prefix).
- An agent following the web_serve tool text alone — no human help — produces a preview that renders in the agent's built-in browser panel and in the user's tab (review MAJ-006).
- The issue's acceptance test executes in CI, not merely exists, on both paths.
- User-facing docs name the F794-4 phishing residual and the blank-preview troubleshooting step.

## 7. Platform / browser behaviour

External facts are cited; each was checked this session, not recalled.

| Engine / access shape | Behaviour | Grounding |
|---|---|---|
| Chromium, Firefox, Edge on `localhost` | Mode 1 primary: `*.localhost` → loopback internally per RFC 6761, no DNS query, no setup | RFC 6761 ("Special-Use Domain 'localhost'"); implementations verified via [MDN localhost notes](https://developer.mozilla.org/en-US/docs/Glossary/Loopback) and [Tailscale-ecosystem write-ups of `*.localhost` handling](https://github.com/tailscale/tailscale/issues/1196) — Chromium and Firefox implement RFC 6761 internally, Safari does not |
| Safari / WebKit | Mode 2 always: WebKit delegates `*.localhost` to the system resolver, which does not resolve subdomains | Same sources; WebKit bug tracker history on RFC 6761 non-implementation |
| Fetch Metadata (`Sec-Fetch-*`) | Present in all three engines since Safari 16.4 (March 2023); forbidden headers, JS cannot strip them | [MDN `Sec-Fetch-Dest`](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Sec-Fetch-Dest) (Safari 16.4 full support); [Safari 16.4 release notes](https://developer.apple.com/documentation/safari-release-notes/safari-16_4-release-notes) ("Added support for Fetch Metadata Request Headers"); control-stack row 3 loses this layer only on pre-16.4 engines |
| LAN IP / real domain / Tailscale | Mode 1 impossible: IP-literal subdomains unresolvable *(Inferred, high — no browser special-cases them)*; real domains need wildcard DNS (environment); MagicDNS resolves flat device names only, and device certs cover only the exact name — no wildcards | [tailscale/tailscale#1196](https://github.com/tailscale/tailscale/issues/1196) (open feature request); [Tailscale DNS docs](https://tailscale.com/kb/1081/magicdns) |
| HTTPS anywhere | Mode 2 only: dynamic subdomain certificates need a wildcard CA (domain + DNS) or a trusted private CA (trust-store install) — both environment dependencies (F794-5) | CA/Browser Forum baseline rules forbid public issuance for `.localhost`; wildcard issuance requires domain control — Inferred (high) |
| Module scripts / `crossorigin` / `fetch` under Mode 2 without ACAO | Blank render — measured on Chromium and WebKit by the review (2026-09-26); fixed by `ACAO: *` (F794-3) | `ADR-094-preview-isolation-model-review.md`, "Browser measurement" |
| Storage APIs under a `sandbox` CSP directive | Throw `SecurityError` — measured, Chromium and WebKit (why round-1's model was rejected) | Same review measurement; F794-1/F794-2 |

## 8. Test strategy

- **RED (qa-lead, browser-level, proves both hole and instrument, on both paths):**
  - Path URL against current code: script in a served preview reads `document.cookie` (CSRF value), `fetch('/api/v1/…', {method:'POST'})` succeeds with the echoed header, a state-changing request lands.
  - Mode 1 shape against a stub host-dispatch: a request to `<label>.localhost` carrying a `Domain=localhost` cookie jar shows whether any gateway cookie is attached (must be none, once implemented).
  - Dev-proxy path included, not only static.
- **GREEN (backend-lead) unit level:** one negative assertion per control-stack row of §2.3 (confined `connect-src`/`form-action`, navigation-`Sec-Fetch-Dest` rejection, `Service-Worker: script` refusal, redirect-rule cases including dot-segment/`%2e`/protocol-relative/backslash/absolute-same-origin, `ACAO: *` + upstream header deletion, `frame-ancestors 'none'`, `sandbox` absent); label-grammar and host-dispatch 404 cases; MIME additions; per-trigger fail-closed cases (wildcard-host `public_url`, unparseable origin, empty origin — F794-6); hostile-agent-ID CSP-construction case (MIN-002).
- **Browser GREEN (the issue's acceptance):** a committed **realistic-bundle fixture** (review MAJ-007): a real Vite production build — module script, `crossorigin` stylesheet, `fetch('./data.json')`, a guarded `localStorage` probe, an in-prefix form POST, a `target=_blank` link — asserted to render and function on Chromium and WebKit, served **both** statically and through the dev proxy, in both modes. Storage and a cookie-backed login work in both modes (F794-1/F794-2 acceptance).
- **CHECK (qa-lead, mutation):** drop `ACAO: *` → Mode 2 fixture render fails; widen `connect-src` to the bare origin → builder tripwire fails; string-prefix (non-WHATWG) Location check → the dot-segment/`%2e` tripwire cases fail; remove the `Service-Worker` refusal → SW test fails; remove the navigation rejection → navigation test fails; accept a non-DNS-safe label → grammar test fails; drop the `sandbox`-absent assertion → tripwire fails.
- **Regression sweep:** `rest_preview_web_serve_e2e_test.go`, the e2e web-serve specs — including `tests/e2e/web-serve-canonical.spec.ts` and `tests/e2e/web-serve-malformed.spec.ts` (added per the review's coverage gap) — and any vitest touching preview links.
- **Rollback and diagnosis** (review MIN-008): the rollback is a revert commit — no flag, deliberate for a security fix. Blank-preview diagnosis: CSP violations are console-only; the user docs carry the one-line troubleshooting note, and the tool text tells the agent to check the console in the built-in browser panel.

## 9. Review disposition

One table row per finding in `ADR-094-preview-isolation-model-review.md` (1 CRITICAL, 8 MAJOR, 8 MINOR, 3 OBSERVATION). Dispositions: **Accepted** (fixed in this correction), **Adopted** (reviewer suggestion folded in), **Superseded** (the founder's decisions replace the finding's premise), **Rejected** (with reason).

| Finding | Disposition | Where |
|---|---|---|
| CRIT-001 opaque origin breaks module scripts/fetch; CORS only for fonts | **Accepted** — restructured: Mode 1 needs no CORS (same-origin on its own host); Mode 2 gets prefix-wide `ACAO: *` (F794-3) with upstream-header override; MIME additions (`.mjs`, fonts, `.wasm`) normative | §2.1, §2.3 rows 11–12, §5 |
| MAJ-001 built-in browser enforces the CSP; Q1's fallback false; loss wider than login | **Accepted + Superseded** — the factual corrections stand (correction note item 1); the premise dissolves because F794-1/F794-2 reject the sandbox entirely, so storage/login work in every viewer | Correction note; §2.5 |
| MAJ-002 Q2's premise false — web_serve already fails closed on empty origin | **Accepted** — old clause 9 deleted; fail-closed extended to wildcard-host `public_url` and unparseable origins (F794-6); real triggers tabled | §2.4 |
| MAJ-003 clause 6 contradicts §2.2/§8; Location rule under-specified | **Accepted** — one normative 4-step rule; 304 allowed; tripwire extended to dot-segment, `%2e`, protocol-relative, backslash, absolute-same-origin | §2.3 (redirect rule) |
| MAJ-004 sandbox flag set unspecified | **Superseded** — no `sandbox` directive exists in the new model (F794-2), so no flag list can be mis-implemented; the escape-flag risk (`allow-popups-to-escape-sandbox`) dies with the directive | §2, §2.1 |
| MAJ-005 helper-sharing ambiguous; `public_url` used verbatim; third origin computation | **Accepted** — shared `previewIsolationOrigins`; per-surface templates stay separate; `resolveMainOrigin` retired for previews | §2.6, §5 |
| MAJ-006 agents not told the new constraints | **Accepted** — tool-text row owned by prometheus-prompt-engineer; agent-reachability bullet in §6 | §5, §6 |
| MAJ-007 no realistic-bundle fixture; missing mutations | **Accepted** — Vite-build fixture with module script/crossorigin CSS/fetch/storage probe/form/popup link, both serving paths, both modes; six named mutations | §8 |
| MAJ-008 no normative policy string | **Accepted** — literal template with placeholders, made the tripwire oracle | §2.3 |
| MIN-001 "byte-stable" vs per-token build | **Accepted** — origin list frozen at boot; string byte-stable per `(agent, token)`; Mode 1 emits no source list | §2.3, §2.6 |
| MIN-002 agent ID/token unescaped in CSP | **Accepted** — percent-encoding to RFC 3986 unreserved set + hostile-ID tripwire; Mode 1 label grammar is DNS-safe by construction | §2.2 (normative 2), §2.3, §8 |
| MIN-003 `frame-ancestors` contradiction; `*` fallback | **Accepted** — Mode 2 template carries `frame-ancestors 'none'`; the `*` fallback is gone (fail-closed posture, F794-6); test churn named | §2.3 (template), §4 Negative 5 |
| MIN-004 option letters collide with ADR-044's | **Accepted** — alternatives relabelled; ADR-044 referenced by title and its letters cross-referenced | §3 |
| MIN-005 two-path cookie idea inexpressible | **Accepted** — dropped; single-`Path` variant only, and it gates nothing | §3 Alternative D |
| MIN-006 §1.1 understates today's severity | **Accepted** — severity sentence rewritten (navigation egress named) | §1.1 |
| MIN-007 fake-login residual unnamed | **Accepted** — named in both modes; F794-4 accepts and requires documentation | §2.2, §2.3, §5 |
| MIN-008 rollback and diagnosis absent | **Accepted** — revert-commit rollback; docs troubleshooting note; agent console guidance in tool text | §8, §5 |
| OBS-001 loopback aliases may buy nothing for web_serve | **Adopted** — Mode 2 sources use the canonical origin only; absolute minting makes aliases pointless | §2.6 |
| OBS-002 SPA deep-link state changes outside CSP | **Adopted** — stated as an invariant with a security-lead audit action before landing | §2.3 (cannot-stop list), §5 |
| OBS-003 inline PDF and HMR unmeasured | **Adopted (partially)** — HMR: Mode 2 `connect-src` carries the `ws`-scheme prefix form; Mode 1 native. Inline PDF: the sandbox is gone, so the measured sandbox-specific question no longer arises; `.pdf` serving unchanged | §2.1, §2.3 (template) |

The review's founder-question set is answered by F794-1…6 (`ADR-094-founder-decisions.md`): Q1′ → F794-1; Q2′ → F794-6; Q3 → F794-3; Q4 → F794-2; Q5 → F794-4. The review's unasked questions 1–7 are answered by §2.3's template (1, 2), §2.6 (3), §2.4 (4), §10 (5), §5's audit actions (6), and §7 + §2.1 (7).

## 10. Question for the founder — answered (F794-7)

**FQ-794-7 — On the Mode 2 fallback path, a determined preview page can still popup-script the SPA and act as the user. Accept that residual, or fail Mode 2 closed?**

*Context.* Mode 1 kills #798 structurally, but only where the browser self-resolves `*.localhost` — Chromium/Firefox/Edge on `http://localhost`. Everywhere else (Safari/WebKit, `127.0.0.1` or LAN-IP browsing, real-domain and Tailscale deployments, HTTPS ingress), previews must stay same-origin on `/preview/`. There, no header stack can stop a same-origin page from opening the SPA in a popup and driving its DOM — clicking the real app's buttons — which makes authenticated state-changing API calls as the user. Everything else is closed server-side (§2.3's control stack); this one path is not closable without the sandbox you rejected (F794-1/F794-2). Hardening already in place or in this ADR: the SPA blocks *injected* script (`script-src 'self'`, no `unsafe-inline`/`unsafe-eval`), preview links carry `noopener noreferrer`, SPA iframing is refused — but same-origin DOM automation is not blockable. A spoofed click supplies the user gesture `window.open` needs.

*Impact.* **(A)** keeps previews working on every deployment and engine; the cost is a documented, hardened-but-open same-origin residual on the fallback path only — Mode 1 users (the default desktop install) are unaffected. **(B)** makes previews subdomain-only: Safari users, `127.0.0.1`/LAN users, and every non-loopback deployment get no previews at all — which cuts against "full web apps" harder than (A)'s residual does. **(C)** is (A) plus an explicit operator opt-in before the fallback serves on non-loopback hosts — safest default, more setup friction for LAN/Docker users.

*Options.* **(A) Accept the residual on the Mode 2 fallback with the documented hardening stack (recommended)** — it is the same trust boundary as F794-4's accepted phishing residual (the user opened a page their own agent built), it cannot reach Mode 1 users, and (B) removes the feature for most non-default deployments. (B) Fail Mode 2 closed — previews exist only where Mode 1 works. (C) (A) with an operator opt-in required for non-loopback fallback serving.

*Answer:* **Decided — F794-7 (2026-09-26): option A.** "Accept and document the `/preview/` fallback residual (same-origin popup scripting); the hosted version must not rely on the fallback" — tracked in elicify-ai/omnipus-ai#1177. (B) and (C) not taken. The hosted-version clause is a constraint on the hosted deployment, not on this codebase's fallback path: every self-hosted install keeps the Mode 2 fallback working, while the hosted product must not depend on it — that follow-up is tracked in the hosted-product repo, outside this ADR's implementation scope (`ADR-094-founder-decisions.md`, F794-7).

## 11. Affected components

`pkg/gateway/rest_workspace.go` (`buildWorkspaceCSP` retired for previews; `workspaceContentType` additions; `resolveMainOrigin` retired for previews), `pkg/gateway/rest_preview.go` (CORS, redirect rule, Director credential filter), new `pkg/gateway/webserve_isolation_policy.go` and a new host-dispatch/label-registry file (names per backend-lead), `pkg/gateway/library_isolation_policy.go` (`libraryIsolationOrigins` → `previewIsolationOrigins`, shared), `pkg/gateway/gateway.go` / `gateway_boot.go` (dispatch wiring), `pkg/tools/web_serve.go` (mode selection, dual URLs, `Description` text with prometheus-prompt-engineer), middleware Fetch-Metadata/Service-Worker guard, `pkg/security/ssrf.go` (`isAllowedGatewayOrigin` gains the Mode-1 label host class — spec-review correction CRIT-002, security-lead-reviewed); test updates in `pkg/gateway/preview_iframe_test.go`, `rest_preview_test.go`, `rest_preview_proof_test.go`, `rest_preview_web_serve_e2e_test.go` and the two e2e specs. The Library (`pkg/gateway/library_isolation_policy.go` policy, `rest_library_preview.go`) and the mail surface (`docs/internal/specs/email-mail-view-spec.md`, branch `feature/email-mail`) are untouched. No SPA changes are required for isolation itself: the link-selection card (founder Q2) is additive SPA work, and the tool result gains one `isolated_url` field under the `// not-wire-format` opt-out — no `contracts/` delta.
