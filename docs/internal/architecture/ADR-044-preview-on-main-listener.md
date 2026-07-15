# ADR-044: Serve /preview/ on the main gateway listener (path approach)

- **Status:** Accepted
- **Date:** 2026-07-15
- **Deciders:** Daniel Piatkowski (operator)
- **Evidence level (highest used):** 1 (user-input: hosting model requires path approach; operator decisions on auth, CSRF, and the disable toggle) + 3 (documented pattern: industry comparison) + 5 (expert reasoning: same-origin risk assessment grounded in code)

> **Operator decisions (2026-07-15 interview + 2 grill rounds) — these supersede the earlier sub-decisions below where they conflict:**
> 1. **Login protection → browser-managed (HttpOnly) cookie.** The auth token moves out of JS-readable storage into the **already-built** `omnipus-session` HttpOnly cookie the previewed app cannot read. Cross-tab login is preserved (no new-tab re-login). This **replaces D3** — see the revised D3 below.
> 2. **CSRF → the `/preview/` prefix is exempt** (all methods; prefix match, not exact-path) so previewed apps' data-submissions (their own logins, form saves) work. The gateway API keeps CSRF. Confirms D1.
> 3. **Disable toggle → live, no restart.** Always-on; a single Settings → Gateway switch (`gateway.preview_enabled`) disables it, checked live per-request. The separate listener, its mux, and its config keys are **deleted entirely** (no back-compat). This **replaces D4**; see D5.
> 4. **Presentation → link + built-in browser, NOT an embedded same-origin iframe.** Round 2 grill (C-1) proved a same-origin embedded iframe cannot be both isolated from the SPA and able to hold the previewed app's own login (`IframePreview.isSameOriginAsApp` already strips `allow-same-origin` on same origin). The operator's model: chat renders a **clickable link** to the preview (opens in the user's real browser), and the **agent uses the built-in browser live panel (ADR-038, already on `hotfix/v0.1.1`)** to review the preview and present it to the user. No same-origin iframe → the C-1 one-way-door never triggers. See the new **D6**.

## 1. Problem Understanding

The dev-build preview feature (`serve_web` dev mode → `DevServerRegistry` → reverse proxy → `/preview/<token>/`) is fully implemented in code but unreachable on deployments that expose only one public port. The code deliberately registers `/preview/` on a **separate preview listener** (a second TCP port, default `gateway.port+1`) and hard-404s it on the main mux (`gateway.go:2193-2200`, `rest.go:4864-4877`). This is an **intentional origin-isolation boundary**: the preview handler is token-only / no-auth / no-CSRF (FR-023, `rest_preview.go:15-16`), which is safe only because the preview lives on a **different browser origin** (different port).

The hosting model (desktop app, Docker, single-public-port cloud pods) cannot always expose a second port. `[FACT]` The operator has directed: the path approach (`/preview/` on the main listener) is required.

Two additional defects compound the problem:
- **Boot-only wiring**: the preview listener + `gatewayPreviewBaseURL` bind only at boot (`gateway.go:1898`, `loop.go:192`); a runtime flip of `preview_listener_enabled` is a no-op. `[FACT]`
- **Config-honesty lie**: `set_config` returns `requires_restart:false` for `preview_listener_enabled` (`sysagent/tools/config.go:153-161` omits the preview prefix), but the gateway's `RestartGatedKeys` correctly classifies it as restart-gated (`rest_pending_restart.go:64`). `[FACT]`

**Stakeholders**: the operator (runs Omnipus on desktop/Docker), the agent (uses `serve_web` + browser tools), and any future hosted deployment.

**Blast radius**: the preview surface, the auth model, and every deployment's public exposure.

## 2. Extracted Requirements

### Functional
- FR-1: `serve_web(path, command="npm run dev")` MUST return a URL reachable by both the agent's headless browser and the user's real browser. `[FACT]` (from the issue)
- FR-2: The preview URL MUST work on deployments that expose only one public port. `[FACT]` (operator requirement)
- FR-3: The user MUST be able to log in to the previewed app (OAuth, session cookies, etc.). `[FACT]` (operator requirement)
- FR-4: The preview MUST NOT require a second TCP port at the browser level. `[FACT]` (hosting model)
- FR-5: The existing reverse-proxy, dev-server registry, lifecycle, Landlock bind-port allow-list, and npm egress proxy MUST be reused unchanged. `[FACT]` (they are fully implemented)

### Non-Functional
- NFR-1 (Security): the preview MUST NOT enable privilege escalation via the same-origin surface. `[INFERENCE]` from the grill-spec review.
- NFR-2 (Operability): a runtime config flip of `preview_listener_enabled` MUST either work or honestly report `requires_restart:true`. `[FACT]` (the lie is a real defect)
- NFR-3 (Maintainability): the change MUST be small and surgical — reuse, not rewrite. `[FACT]`

### Constraints
- Omnipus is a desktop app (or self-hosted Docker). `[FACT]`
- The hosting model may expose only one public port (e.g., a Fly pod proxying 8080). `[FACT]`
- Single Go binary, no new runtime deps. `[FACT]` (CLAUDE.md hard constraint #1)
- The preview handler is token-only / no-auth / no-CSRF by design (FR-023). `[FACT]`
- The SPA stores the auth token in `sessionStorage` (per-tab), with a `localStorage` fallback. `[FACT]` (`src/store/auth.ts:23`, `src/lib/api.ts:609-611`)

## 3. Gaps and Ambiguities

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve |
|---|---|---|---|---|
| 1 | Is the auth token ever in `localStorage` in production? | Determines the severity of the same-origin risk. The SPA writes to `sessionStorage` (`auth.ts:23`), but `getAuthHeaders()` (`api.ts:609`) has a `localStorage` fallback. | The fallback exists for legacy/e2e compatibility; in normal desktop use, the token is in `sessionStorage` only. | Confirm the localStorage fallback is never written by the current SPA (it only reads it as a fallback). |
| 2 | Does the CSRF middleware exempt `/preview/` or not? | Determines whether mounting `HandlePreview` on the main mux breaks (CSRF rejects unauthenticated GET) or requires exemption. The CSRF middleware wraps the main mux (`gateway.go:2289`); the preview handler sends no CSRF header. | `/preview/` must be exempted from CSRF (it's a GET-only reverse proxy with token-in-path auth). | Specify the exact exemption. |
| 3 | Does the deployment's reverse proxy support wildcard subdomains? | If it does, the subdomain approach (distinct origin, safer) is viable alongside the path approach. The operator has stated the path approach is required. | The hosting model does not support wildcard DNS; the path approach is the only option. | Confirm with the operator. |
| 4 | What happens to the separate preview listener + `preview_listener_enabled` + `gatewayPreviewBaseURL` after the fix? | The legacy config keys need a clear deprecation/role. | The separate listener becomes optional (for operators who expose two ports); the main-listener path is the primary. | Declare in the ADR. |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Works on single-public-port deployments | **Critical** | The operator's hard requirement |
| Login works (OAuth, session cookies) | **Critical** | The preview must be a first-party origin for the app's own auth |
| Same-origin security risk is acceptable or mitigable | **High** | The grill-spec flagged this; must be documented + mitigated |
| Reuses existing code (reverse proxy, registry, lifecycle) | **High** | The feature is fully built; the change should be wiring, not rewrite |
| No new runtime deps / single binary | **Hard constraint** | CLAUDE.md #1 |
| Honest config signals (`requires_restart`) | **Medium** | The lie is a real defect; fix alongside |

## 5. Option Analysis

### Option A — Path approach: mount `/preview/` on the main listener

| Dimension | Assessment |
|---|---|
| Strengths | Works on any deployment (single port, desktop, Docker). Reuses the existing `HandlePreview` handler + reverse proxy + dev-server registry unchanged. No DNS, TLS, or infra changes. Same path, same handler — only the mux registration moves. |
| Weaknesses | **Removes the origin-isolation boundary.** The preview lives on the same browser origin as the gateway → the dev build's JS shares the gateway's cookie scope. The preview handler is token-only / no-CSRF (FR-023); mounting it on the CSRF-wrapped main mux requires a CSRF exemption for `/preview/`. |
| Risks | **Same-origin credential exposure (conditional).** The SPA stores the auth token in `sessionStorage` (per-tab); a preview opened as a **new tab** has its own sessionStorage and **cannot read** the SPA's token — this materially lowers the risk vs. the grill-spec's assumption of `localStorage`. The `localStorage` fallback path (`api.ts:609`) IS per-origin and readable — but the SPA only WRITES to `sessionStorage` (`auth.ts:23`), so in normal operation the `localStorage` fallback is empty. The CSRF cookie (`__Host-csrf`, Path=/) IS readable (per-origin), but useless without the Bearer token. Residual risk: if a future change moves the token to `localStorage`, or if the Playwright e2e storageState (which writes to `localStorage`) leaks into a production session, the same-origin escalation becomes real. |
| Complexity | Low — move the route registration, add a CSRF exemption, fix `requires_restart`. |
| Cost implications | Minimal build cost; zero run cost. |
| Operational impact | One config key (`preview_on_main_listener`, or just invert the mux rule). The separate preview listener becomes optional (for operators who expose two ports). |

### Option B — Subdomain approach: per-preview subdomain Host-routed to the one port

| Dimension | Assessment |
|---|---|
| Strengths | **Preserves origin isolation** — each preview is a distinct browser origin → the dev build's JS cannot read the gateway's storage or ride its cookies. This is the industry pattern (Lovable `*.lovable.app`, Replit `*.replit.dev`, Codespaces `<port>-<ws>.app.github.dev`). Login works natively (first-party origin). No CSRF collision. |
| Weaknesses | Requires **wildcard DNS + wildcard TLS cert** for the deployment's hostname. On a single-public-port Fly pod, the operator would need `*.pod-omnipus.fly.dev` configured — which the Fly proxy may not support without operator infrastructure. The operator has stated this is not feasible with the hosting model. |
| Risks | DNS/cert provisioning complexity. If the wildcard cert lapses or the DNS breaks, all previews break. |
| Complexity | Medium — wildcard DNS + cert + Host-header routing in the gateway. |
| Cost implications | Wildcard cert cost (or Let's Encrypt DNS-01); DNS management. |
| Operational impact | Requires platform-level DNS + TLS config per deployment. |

### Option C — Keep the separate preview port; make it reachable

| Dimension | Assessment |
|---|---|
| Strengths | **Zero code change** to the preview handler or auth model. Origin isolation preserved. |
| Weaknesses | Requires the deployment to expose a second port — which the hosting model does not support. On desktop, a second loopback port works; on a single-public-port pod, it does not. |
| Risks | Doesn't solve the problem for single-public-port deployments. |
| Complexity | Zero code; deployment-only. |
| Cost implications | None. |
| Operational impact | Every single-public-port deployment must find a way to expose a second port (tunnel, reverse proxy) — the problem the issue is about. |

### Option D — Service-worker same-origin serving (StackBlitz WebContainers model)

| Dimension | Assessment |
|---|---|
| Strengths | Zero extra ports, zero DNS, zero TLS. Same-origin. The dev build runs in-browser via WASM. |
| Weaknesses | **Requires reimplementing the dev-server runtime in WASM** — a massive effort (StackBlitz spent 7 years). No real backend, no real network egress, no real OAuth. Omnipus already HAS a real dev-server lifecycle in the sandbox; SW serving would duplicate and limit it. |
| Risks | Chromium-only (Safari/Firefox limited). Proprietary core (WebContainer is closed-source). |
| Complexity | **Extreme** — a new runtime, not a wiring change. |
| Cost implications | Enormous build cost; ongoing maintenance. |
| Operational impact | Fundamental architecture shift. |

**Rejected**: Option B (operator confirmed hosting model doesn't support wildcard DNS); Option C (doesn't solve the single-port problem); Option D (disproportionate effort, limits the existing real-backend capability).

## 6. Recommended Architecture

**Option A — the path approach.** Mount `/preview/` on the main gateway listener. The operator's hosting model requires it; the feature is fully built; the change is wiring.

**Key security finding that lowers the risk:** `[FACT]` The SPA stores the auth token in **`sessionStorage`** (`auth.ts:23: sessionStorage.setItem('omnipus_auth_token', token)`), NOT `localStorage`. `sessionStorage` is **per browsing context (tab/iframe)** — a preview opened as a new tab or embedded iframe has its **own** sessionStorage and **cannot read** the SPA's token. The CSRF cookie (`__Host-csrf`, Path=/) IS per-origin and readable, but is a double-submit token that authenticates nothing without the Bearer token. `[INFERENCE]` The same-origin credential-exposure risk is therefore **lower than the grill-spec assessed** — it is conditional on the token being in `localStorage`, which the current SPA does not do in normal operation.

**Rejected options:**
- B (subdomain): loses on the hosting-model constraint.
- C (separate port): doesn't solve the single-port problem.
- D (WebContainers): disproportionate effort + limits the real-backend capability.

### Sub-decisions

**D1: CSRF exemption for `/preview/`.** The preview handler is GET-only (reverse-proxy to the dev server); it sends no CSRF header. Mounting it on the CSRF-wrapped main mux (`gateway.go:2289`) requires exempting `/preview/` from the CSRF middleware. This is safe: the preview is token-gated (FR-023); CSRF protects state-changing requests against the gateway API, not against a GET reverse-proxy.

CONFIDENCE: High
  Basis         : the CSRF middleware wraps the main mux (`WrapHTTPHandler`, gateway.go:2289); the preview handler is GET-only token-gated
  Evidence      : csrf.go:256 (CSRFMiddleware), gateway.go:2289 (WrapHTTPHandler), rest_preview.go:15-16 (token-only auth)
  Missing       : the exact exempt-path registration (need to add `/preview/` to the exempt set or use `WithExemptPath`)
  Would improve : implement and test the exemption

**D2: serve_web returns the public URL when `gateway.public_url` is set, else localhost — computed live.** The agent's headless browser cannot navigate to `localhost` by default (SSRF blocks loopback, `ssrf.go:222-225`). When `public_url` is set, `serve_web` returns `gateway.public_url + "/preview/<token>/"` — a public host that already passes the SSRF check (proven earlier: `browser_open_tab` reached the pod's `fly.dev` URL). When `public_url` is empty (desktop), it returns `http://localhost:<gateway.port>/preview/<token>/`, and the agent browser's SSRF checker must allow **only the gateway's own host:port** — NOT all of loopback. Grill M-2 note: the SSRF check is host/IP-only and strips the port (`ssrf.go:336-341`), so a naïve "allow localhost" would open **every** local port to the agent browser; the allowlist must be scoped to the exact gateway origin (host + port), and the browser only needs to reach the gateway port (the gateway reverse-proxies to the dev port — the dev port is never navigated directly). The URL must be built **live** at call time, not from the boot-frozen `gatewayPreviewBaseURL`.

CONFIDENCE: High
  Basis         : SSRF blocks loopback (ssrf.go:222-225); the agent browser needs a reachable URL scoped to the gateway origin only
  Evidence      : the SSRF check is host/IP-only (ssrf.go:336-341 strips port); public hosts pass; NewSSRFChecker is already wired into the browser LiveView path
  Missing       : the exact port-aware gateway-origin allowlist wiring + a named test proving other local ports stay blocked
  Would improve : implement and test both paths incl. the "other local ports still blocked" negative

**D3 (REVISED per operator decision #1): Move auth to a browser-managed HttpOnly cookie.** The original D3 ("sessionStorage-only; remove the `localStorage` fallback") was rejected during the interview for two reasons the grill-spec surfaced: (a) `sessionStorage` is per-tab, so it forces a re-login every time the operator opens the app in a new tab — the operator ruled that out; (b) Playwright `storageState` cannot carry `sessionStorage`, so the removal would break every authenticated e2e spec. Instead, the auth token uses the **already-built** `omnipus-session` cookie (`pkg/gateway/middleware/session_cookie.go`: **HttpOnly, `SameSite=Strict`, `Secure=requestIsSecure`, `Path=/`, 24 h**; bcrypt `SessionTokenHash` on disk). It is already issued at `/auth/login` + register-admin; this ADR **completes** the half-wired migration (the SPA and the primary `checkBearerAuth` path still use bearer-from-`sessionStorage`). A required addition: **`POST /onboarding/complete` must also issue the session cookie** — today it issues only the CSRF cookie, so a fresh install would otherwise finish onboarding with no cookie and, once the JS token is removed, get locked out (grill C-2). Properties:

- **The previewed app cannot read the token** — HttpOnly means no JS (SPA or preview) can read it, which is the same-origin protection this ADR needs.
- **Cross-tab login is preserved** — the cookie is per-origin; new tabs are already logged in. No re-login.
- **e2e keeps working** — Playwright `storageState` DOES persist cookies, so the test harness carries the session as a cookie (simpler than the old localStorage mirror).
- **Residual (accepted):** on a same-origin path deployment, a malicious previewed app can still *initiate* authenticated in-browser calls to `/api/v1/` (the cookie auto-rides), but **cannot exfiltrate the token off-machine**. Complete isolation would require a separate origin (Option B), which the hosting model rules out. This residual is mitigated by: (i) CSRF on `/api/v1/` (unchanged); (ii) **scoping the auth cookie so `/preview/` never carries it** (Path-scope to the API prefix and/or strip it in the reverse proxy) so the previewed dev server never even sees the operator's session; (iii) the existing preview-token gate.
- **The `sessionStorage`/`localStorage` token paths are deleted** (`auth.ts:23`, `api.ts:609-611`) — no JS-readable token remains. The SPA sends requests with `credentials: 'include'`; the browser attaches the cookie.

CONFIDENCE: High
  Basis         : HttpOnly cookies are the standard defense against same-origin token theft; the CSRF double-submit machinery already exists
  Evidence      : csrf.go CSRFMiddleware already issues a readable `__Host-csrf` cookie; Playwright storageState carries cookies
  Missing       : the exact login-handler change (issue Set-Cookie) and cookie Path-scope value
  Would improve : implement + test the login→cookie→authenticated-request round trip, and the "cookie absent on /preview/" assertion

**D4 (REVISED per operator decision #3): Delete the separate preview listener, its mux, and its config keys — no back-compat.** The original D4 ("separate listener stays optional") was rejected: the operator directed that redundant code be removed and the feature be always-on with a single live toggle. Deleted entirely: the second TCP listener + `previewMux`/`previewServer` and their `RegisterPreviewHandler`/`WrapPreviewHandler`/`ListenAndServe` plumbing (`pkg/channels/manager.go`), the `SetupPreviewServer` boot path, and the config keys `preview_port` / `preview_host` / `preview_origin` / `preview_listener_enabled` with their defaults, validation, and restart-gating. `/preview/` is registered on the main mux, always. There is no two-port mode and no origin-isolation fallback.

CONFIDENCE: High
  Basis         : operator directive (remove redundant code; always-on; single toggle); the main-listener path fully replaces the listener
  Evidence      : the reverse proxy + dev-server registry are listener-agnostic; only the mux registration and the second bind are being removed
  Missing       : the complete deletion set (enumerated in the spec's deletion checklist)
  Would improve : a compile + `make verify-contracts` pass confirming no dangling references

**D5 (NEW per operator decision #3): A single live "disable preview" toggle under Settings → Gateway.** The feature is always-on by default. One boolean (`gateway.preview_enabled`, default `true`) gates it, **checked live inside the `/preview/` handler and in `serve_web`** — flipping it takes effect on the next request with no restart. When disabled: `/preview/` returns 404 and `serve_web` refuses with a clear error. This retires the `requires_restart` lie for the preview surface (there is nothing to restart) and satisfies NFR-2 honestly.

CONFIDENCE: High
  Basis         : a path-mounted route reading a live config flag needs no rebind; the toggle is a pure runtime check
  Evidence      : config hot-reload already exists (SetReloadFunc); no listener lifecycle is involved
  Missing       : the Settings UI control + wiring the live read
  Would improve : implement + test enable→disable→enable within one process

**D6 (NEW per operator decision #4 + grill C-1): Present previews via a link + the built-in browser live panel — never an embedded same-origin iframe.** The round-2 grill proved a same-origin embedded iframe is a genuine one-way-door: `IframePreview.isSameOriginAsApp` (`src/components/chat/IframePreview.tsx`) already strips `allow-same-origin` when the framed origin equals the SPA origin (its comment: "breaks localStorage/cookie access inside the iframe but prevents full SPA compromise"). On the shared origin this fires for every preview → the framed app can't hold a login; lifting the guard would let a scripted framed app reach `window.parent` and take over the SPA. Resolution — the preview is NOT embedded:

- **Chat renders the preview URL as a clickable link.** The user opens it in their own top-level browser tab, where the app runs fully and can log in. (The existing `LinkOnlyFallback` is the seam; the embedded-iframe render path is removed for dev previews, retiring the now-moot `isSameOriginAsApp` guard for this surface.)
- **The agent reviews and presents the preview via the built-in browser live panel** (ADR-038: `pkg/gateway/browser_ws.go`, `pkg/tools/browser/live.go`, `src/components/browser/BrowserLive{Panel,View}.tsx` — already on `hotfix/v0.1.1`). The live panel is a **screencast** (image frames over WS with input forwarded), not a same-origin frame, so it carries no SPA session and raises no isolation dilemma. The agent's headless browser is a separate process with no SPA cookies.
- **Residual (C-3, accepted):** the tab the *user* chooses to open IS same-origin with the SPA, so a malicious previewed app there can read the readable CSRF cookie (`HttpOnly:false`, `Path:/`, `csrf.go`) and ride the auto-attached `omnipus-session` cookie to call `/api/v1/*` **while that tab is open** — it just cannot exfiltrate the token off-machine (HttpOnly). This is the accepted single-user-desktop residual; the spec states it explicitly in Non-Behaviors rather than claiming same-origin is "safe."

CONFIDENCE: High
  Basis         : removes the embedded-iframe surface that creates the isolation-vs-login dilemma; the live panel already exists on-branch
  Evidence      : IframePreview.isSameOriginAsApp guard (src/components/chat/IframePreview.tsx); ADR-038 live-panel files present on hotfix
  Missing       : the chat link-render swap + wiring the agent-browser SSRF allowlist for the gateway origin
  Would improve : implement + UAT the link + built-in-browser presentation flow

## 7. Risks and Caveats

| Risk | Severity (desktop) | Severity (public) | Mitigation | Residual |
|---|---|---|---|---|
| **Same-origin token theft by a previewed app** | Low | Medium | Move the token to an HttpOnly cookie (D3) — no JS, SPA or preview, can read it | Token cannot be exfiltrated; see next row for in-browser abuse |
| **Malicious previewed app initiates authenticated `/api/v1/` calls in the browser (cookie auto-rides)** | Low | Medium | CSRF on `/api/v1/` (unchanged); auth cookie Path-scoped/stripped so `/preview/` never carries it; preview-token gate | Cannot be fully closed on a same-origin path deployment (would need Option B); accepted per hosting model |
| **CSRF exemption on `/preview/` widens the surface** | Low | Low | Exemption is for the `/preview/` prefix only (proxied to the dev server, not the gateway API); CSRF still protects all `/api/v1/` routes | None |
| **Preview token enumerable on public origin** | Low | Medium | 30-min idle TTL (already implemented); 256-bit token | Discovery requires a URL leak |
| **`requires_restart` lie / stale toggle** | Medium (misleading) | Medium | Delete restart-gating; the single `preview_enabled` toggle is read live per request (D5) | None after fix |
| **Process-group teardown leak** | Low | Low | Signal the Setpgid group (P2) | Orphaned npm grandchildren until idle timeout |

**One-way-door warning:** mounting `/preview/` on the main mux and deleting the separate listener inverts a deliberate security rule (`gateway.go:2193-2200`) with no back-compat path. If the same-origin residual later proves unacceptable (e.g., a hosted multi-tenant deployment), full isolation requires re-introducing a distinct origin (Option B: subdomain, or a second port) — a re-architecture, not a config flip. This is accepted deliberately: the product is a single-user desktop/Docker app, the token is no longer JS-readable (HttpOnly), and the hosting model forbids the isolated-origin options.

## 8. Confidence Assessment

**Overall: the path approach is the right call given the hosting-model constraint.** The risk is lower than initially assessed (sessionStorage, not localStorage). The fix is wiring, not architecture.

| Decision | Confidence | Key uncertainty |
|---|---|---|
| Path approach (Option A) | High | Hosting model requires it; feature is built; residual is accepted |
| CSRF exemption for `/preview/` (D1) | High | Prefix-match exemption; proxied to the dev server, not the gateway API |
| serve_web returns public/localhost URL (D2) | High | SSRF analysis is grounded; URL computed live at call time |
| Auth → HttpOnly cookie (D3, operator decision) | High | Standard defense; CSRF machinery exists; e2e carries cookies |
| Delete separate listener + config keys (D4, operator decision) | High | Deletion set enumerated in the spec; no back-compat by directive |
| Live disable toggle (D5, operator decision) | High | Pure runtime flag read; no listener lifecycle |

## 9. Validation / Next Steps

- **Red-team:** `/grill-spec` was run on the spec (BLOCK verdict, 3 CRITICAL / 6 MAJOR). Every load-bearing finding is resolved by the operator decisions above (auth→cookie fixes CRIT-002/MAJ-004; CSRF prefix-exemption fixes CRIT-001; SSRF own-origin verified for public + allowlisted for localhost addresses CRIT-003; contract change addresses MAJ-001; live toggle addresses MAJ-002/MAJ-003). Review: `docs/internal/specs/preview-on-main-listener-spec-review.md`.
- **Spec:** `docs/internal/specs/preview-on-main-listener-spec.md` (being revised to match these decisions).
- **Experiments to confirm during implementation:**
  1. Login issues a `Set-Cookie` (HttpOnly, Secure-when-TLS, SameSite=Lax); an authenticated `/api/v1/` request succeeds with only the cookie; the cookie is **absent** on a `/preview/` request (Path-scope/strip).
  2. CSRF prefix-exemption lets a POST through `/preview/` reach the dev server; a POST to `/api/v1/` without the CSRF token is still 403.
  3. Enable→disable→enable the `preview_enabled` toggle within one process; `/preview/` returns 404 while disabled and `serve_web` refuses; both recover live.
  4. `make verify-contracts` is green after dropping `preview_port` / adding `preview_enabled` to `AboutResponse`.
