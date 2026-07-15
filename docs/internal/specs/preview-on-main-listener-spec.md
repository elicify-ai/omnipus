# Feature Specification: Preview on Main Listener

**Created**: 2026-07-15
**Revised**: 2026-07-15 (v5 — clean rewrite; post 4× grill-spec + operator interviews)
**Status**: Draft (v5 — supersedes v1–v4, all BLOCK)
**Input**: ADR-044 — Serve /preview/ on the main gateway listener (path approach)
**Reviews**: `...-spec-review.md` (r1), `...-round2.md` (r2), `...-round3.md` (r3), `...-round4.md` (r4). All CRITICAL + MAJOR findings from all four rounds are resolved here (operator: keep the auth migration bundled, fix everything).
**Branch**: `hotfix/v0.1.1` (the built-in browser live panel, ADR-038, is already merged here — no cross-branch dependency).

> **v5 note:** v4 was patch-edited and became internally contradictory (r4 CRIT-001: `public_url` gating stated four ways). This is a clean rewrite so the Symbols table, BDD, TDD, Traceability, and SC all agree. The single authority for every contested point: **`gateway.public_url` STAYS restart-gated; `serve_web` uses the boot-frozen canonical origin; only `preview_enabled` is live.**

---

## Operator decisions (2026-07-15 interviews)

1. **Login → browser-managed HttpOnly cookie.** Adopt the already-built `omnipus-session` cookie; drop JS-readable token storage.
2. **CSRF → `/preview/` prefix exempt (all methods).** Previewed apps POST through the proxy; the gateway API keeps CSRF.
3. **Disable toggle → live `preview_enabled`** (default `true`), read per-request; separate listener + `preview_port/host/origin/listener_enabled` deleted (no back-compat).
4. **Presentation → link + built-in browser, NOT an embedded same-origin iframe.** Chat renders a clickable link (opens the user's real browser); the agent reviews and presents the preview via the built-in browser live panel (ADR-038, on-branch).
5. **Scope → keep the auth migration bundled and fix every grill finding** (operator explicitly rejected splitting it out).

## Grounding (verified against `hotfix/v0.1.1` across 4 grill rounds)

- **Session cookie already built + issued at login.** `pkg/gateway/middleware/session_cookie.go`: `SessionCookieName="omnipus-session"` (HttpOnly, `SameSite=Strict`, `Secure=requestIsSecure`, `Path=/`, `MaxAge=86400`), `IssueSessionCookie(username, mutator)`/`ClearSessionCookie`/`ResolveUserFromCookie`/`RequireSessionCookieOrBearer`; login + register-admin issue it (`rest_auth_cookie_test.go`). Disk: bcrypt `UserConfig.SessionTokenHash`. **`IssueSessionCookie` requires the user to pre-exist in `gateway.users` and writes NO cookie on error** (`session_cookie.go:175-236`).
- **Migration is a full-surface cutover.** SPA sends `Bearer` from `sessionStorage ?? localStorage` (`api.ts:609,2591`); the primary `withAuthAndBodyLimit → checkBearerAuth` (`auth.go:127`, `rest.go:316`) is bearer-only. `RequireSessionCookieOrBearer` is wired to **zero live routes** today — this adds cookie auth to the entire authenticated surface for the first time.
- **CSRF cookie is readable + session-scoped + skipped for Bearer.** `IssueCSRFCookie` sets `__Host-csrf`/`csrf` with `HttpOnly:false, Path:/` and **no `MaxAge`** (dies on browser close), issued only at login (`rest_auth.go:484`) + onboarding (`rest_onboarding.go:381`); `CSRFMiddleware` skips the check for `Authorization: Bearer` (`csrf.go:328`) and matches exempt paths by **exact string** (`csrf.go:307`) — tokenized `/preview/<...>` never matches.
- **Onboarding issues only the CSRF cookie.** `HandleCompleteOnboarding` appends the admin user (`rest_onboarding.go:341`) then calls only `IssueCSRFCookie` (`:381`) — no session cookie (r2 C-2).
- **Reverse proxy strips only `Authorization`.** `proxyDevRequest` deletes `Authorization` (`rest_preview.go:340`); its `ModifyResponse` strips CSP/XFO but NOT `Set-Cookie` (`:345-348`); the `Cookie` request header is untouched (r4 MAJ-002).
- **SSRF checker is port-blind.** `CheckURL → extractHost` strips the port (`ssrf.go:337`); `CheckHost`/`CheckIP` decide by host/IP only; `127.0.0.0/8` blocked as a CIDR; `validateSSRFAllowInternal` rejects host:port strings. Both nav paths (`browser_navigate` tool + live-panel `navigate` frame) route through this checker.
- **`public_url` drives boot-frozen origin fences.** `CanonicalGatewayOrigin(cfg)` is computed once at boot and baked into `allowedOrigin` for CORS, WS/SSE/BrowserWS `CheckOrigin`, and CSP `frame-ancestors`; `config.GatewayPublicURL` is in `RestartGatedKeys` (`rest_pending_restart.go:63`) — correctly.
- **Config load ignores unknown nested keys.** No `DisallowUnknownFields` on the `gateway` object — deleting the 4 preview keys won't break boot but silently drops persisted operator values (r3 Q1; acceptable under "no back-compat").
- **Built-in browser live panel present on-branch.** `pkg/gateway/browser_ws.go`, `pkg/tools/browser/{live,manager,tabs,inspect}.go`, `src/components/browser/BrowserLive{Panel,View}.tsx`, `src/routes/_app/browser-live.tsx`.
- **The embedded iframe guard conflicts with same-origin.** `IframePreview.isSameOriginAsApp` strips `allow-same-origin` when framed origin == SPA origin, but still embeds (`WebServeUI.tsx` renders it for `serve_web`); `LinkOnlyFallback` exists and is reusable. Decision #4 removes the embed for dev previews.
- **Logout is client-only today.** `HandleLogout` (`POST /api/v1/auth/logout`, `rest.go:4811`) clears both cookies, but the SPA (`Sidebar.tsx`, `authLogout.ts`, `auth.ts clearAuth`) never calls it (r3 obs).

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `restAPI.HandlePreview` (`rest_preview.go`) | modifies (registration) | Logic unchanged; preview-mux → main mux; stays bare token-only |
| `restAPI.registerPreviewEndpoints` (`rest.go:4864`) | modifies | Register `/preview/` on the **main** mux, bare; gated live by `preview_enabled` |
| `restAPI.proxyDevRequest` + `ModifyResponse` (`rest_preview.go:313,340,345`) | modifies | Strip ALL request `Cookie`s + `Authorization` before forwarding; neutralize upstream `Set-Cookie` for reserved names (r4 MAJ-002) |
| `ChannelManager.previewMux`/`previewServer`/`SetupPreviewServer`/`RegisterPreviewHandler`/`WrapPreviewHandler` | **deletes** | Separate preview listener + mux + plumbing |
| `gateway.go:1893-1926` (`gatewayPreviewBaseURL` boot block) + `SetupPreviewServer` call | **deletes** | Boot-frozen base URL + listener bind |
| `gateway.go:2193-2199` (preview-mux-ONLY rule) | modifies | Inverted: `/preview/` on main mux |
| `WebServeTool` (`web_serve.go:120-192,257,418,565`) | modifies | Receive a live-config accessor; build URL from canonical origin; gate on live `preview_enabled` |
| `checkBearerAuth` (`auth.go:127`) + `withAuthAndBodyLimit` (`rest.go:316`) | modifies | Cookie fallback via `ResolveUserFromCookie` when no `Bearer`; bearer path kept |
| `authenticateWS` (`websocket.go:653`) | modifies | Accept `omnipus-session` on the handshake alongside the existing `{type:"auth"}` frame |
| `HandleCompleteOnboarding` (`rest_onboarding.go`) | modifies | Also issue the session cookie, bound to `body.Admin.Username`, after user-persist; 500 on failure (r4 MAJ-003) |
| `CSRFMiddleware` + `defaultExemptPaths` (`csrf.go:96,256,307`) | modifies | `/preview/` **prefix** exempt (all methods); re-mint the CSRF cookie on authed safe requests lacking it; CSRF cookie `MaxAge=86400` (r4 MAJ-004, r3 C-1) |
| `RequireMatchingOriginOnStateChanging` (`origin.go:150`) | verify | Must not block `/preview/` |
| SPA fetch `request()` (`api.ts:634`), `getAuthHeaders` (`:609`), WS connect (`:2591`), `store/auth.ts`, `onboarding.tsx:555`, logout (`Sidebar.tsx`/`authLogout.ts`) | modifies | Drop JS token; `credentials:'include'`; echo CSRF read **fresh per state-changing request**; 403-CSRF → GET-then-retry-once; logout calls `POST /api/v1/auth/logout` |
| `IframePreview.tsx` (embed path, `isSameOriginAsApp`) + `WebServeUI.tsx` | **removes (embed)** | Render dev previews as a **link** (`LinkOnlyFallback`); retire the same-origin guard for this surface |
| `markdown-text.tsx`/`markdown-shared.tsx`, `src/lib/preview-url.ts` | modifies/removes | Preview URL as a link on the main origin; drop `preview_port`/`preview_origin` reads |
| Built-in browser: `pkg/tools/browser/{live,manager}.go`, `NewSSRFChecker` (`ssrf.go:135`) | extends | Agent navigates the live panel to the preview URL; add a port-aware gateway-origin allow, matching the literal `localhost:<gateway.port>` form serve_web emits (r4 OBS-003) |
| `GatewayConfig.PreviewListenerEnabled` (`config.go:2608`) | modifies | Becomes `PreviewEnabled *bool` (default `true`, live) |
| `GatewayConfig.PreviewPort/PreviewHost/PreviewOrigin`, `IsPreviewListenerEnabled`, `previewListenerEnabledDefault`, `ValidateAndApplyPreviewDefaults`, `shouldWarnPreviewOrigin`, `config/keys.go` preview consts, `cmd/omnipus/internal/doctor/command.go`, `pkg/agent/tier13deps.go`, `pkg/sandbox/dev_url.go` | **deletes** | All separate-port keys, defaults, warnings, doctor checks, and dead references |
| `RestartGatedKeys` (`rest_pending_restart.go:36-66`) | modifies | Remove the **deleted preview keys**; **keep `gateway.public_url`** (it drives boot-frozen origin fences — r3 M-2) |
| `isRestartRequired` (`sysagent/tools/config.go:154`) | modifies | No removed preview key implied restart-gated |
| `AboutResponse` (`contracts/components/schemas/AboutResponse.yaml`) | modifies | Drop `preview_port` + `preview_listener_enabled` + `preview_origin`; add `preview_enabled` (bool); regenerate both trees |
| `src/components/settings/GatewaySection.tsx` | extends | Add the "Preview" toggle (default on) bound to `preview_enabled` |

### Impact Assessment

| Symbol Modified | Risk | d=1 | d=2 |
|---|---|---|---|
| `checkBearerAuth`/`withAuthAndBodyLimit` + WS cookie auth (full-surface) | HIGH | every `withAuth` route; `wave3_bearer_auth_test.go` | whole SPA API surface, WS |
| SPA token→cookie + CSRF-fresh + logout-call (`api.ts`, `auth.ts`, `onboarding.tsx`, `Sidebar.tsx`) | HIGH | `getAuthHeaders`, `request`, WS connect, login/logout/onboarding | every SPA fetch, Playwright storageState |
| Onboarding session-cookie + failure→500 | HIGH | `HandleCompleteOnboarding` | fresh-install auth |
| Proxy cookie sanitize (req + resp) | HIGH | `proxyDevRequest`, `ModifyResponse` | session-fixation surface |
| CSRF re-mint + MaxAge + prefix-exempt | HIGH | `CSRFMiddleware` | all state-changing SPA calls |
| HandlePreview registration (preview→main mux) | HIGH | `registerPreviewEndpoints`, preview tests | serve_web, e2e |
| ChannelManager preview mux removal | HIGH | `gateway.go` boot, `manager.go` StartAll | serve_web lifecycle |
| IframePreview embed → link | MEDIUM | `IframePreview.tsx`, `WebServeUI.tsx`, `markdown-text.tsx`, `preview-url.ts` | chat render |
| Browser SSRF gateway-origin allow (new code) | MEDIUM | `NewSSRFChecker`, `browser_navigate`, live-panel navigate | agent browser navigation |
| `PreviewListenerEnabled`→`PreviewEnabled` + key deletions (`public_url` UNCHANGED) | MEDIUM | config validator, defaults, `RestartGatedKeys`, `AboutResponse` | Settings UI, e2e setup |
| `AboutResponse` contract | MEDIUM | generated Go+TS, `preview-url.ts` | `make verify-contracts` |

### Relevant Execution Flows

| Flow | Relevance |
|------|-----------|
| Gateway boot → listener setup | Preview listener REMOVED; `/preview/` on main mux, gated live; canonical origin baked once (public_url restart-gated) |
| Onboarding / login → cookie | All three (onboarding, login, register-admin) issue `omnipus-session`; onboarding fails 500 if it can't |
| `withAuth`/WS → identity | cookie OR bearer (browser=cookie; CLI=bearer); neither → 401 |
| CSRF middleware | `/api/v1/*` gated (load-bearing); cookie `MaxAge=24h` + re-mint on authed safe request; `/preview/` prefix exempt |
| serve_web → URL | canonical origin (public_url else localhost:port), boot-stable; `preview_enabled` read live |
| Chat → preview presentation | clickable link + agent presents via built-in browser live panel — no embedded iframe |
| Agent built-in browser → preview URL | SSRF allows the gateway origin (literal `localhost:<port>` + resolved forms), else public host |
| Reverse proxy → dev server | strips ALL request cookies + `Authorization`; neutralizes reserved `Set-Cookie` in the response |
| Config hot-reload | `preview_enabled` live; `public_url` still restart-gated |

---

## User Stories & Acceptance Criteria

### US-1 — /preview/ always on the main listener (P0)
`serve_web(command="npm run dev")` returns a main-origin URL (`/preview/<agent>/<token>/`). `/preview/` is registered on the main mux **bare** (token-in-path only; skips CSRF/origin/session-auth checks but keeps the global `configSnapshotMiddleware` for race-free live-config reads); ALL methods proxy to the dev server.

**Acceptance**:
1. `public_url=https://example.com` → URL `https://example.com/preview/<agent>/<token>/`.
2. no `public_url` → URL `http://localhost:<gateway.port>/preview/<agent>/<token>/`.
3. Any method to `/preview/<valid_token>/…` on the main listener → `HandlePreview` serves it (no session/CSRF/origin gate) with a race-free config snapshot.
4. `preview_enabled=false` → any `/preview/` request → 404.

### US-2 — No separate preview listener (P0)
The separate preview listener, mux, server, registration methods, boot-frozen base-URL block, all `preview_port/host/origin/listener_enabled` keys + per-OS/Termux defaults + `ValidateAndApplyPreviewDefaults` + `shouldWarnPreviewOrigin` + doctor checks are **deleted**. No back-compat. (The health server keeps its own `http.Server` — unchanged, out of scope.)

**Acceptance**:
1. Boot binds no separate **preview** listener; SPA + API + `/preview/` share `gateway.port`.
2. Grep `previewMux|previewServer|SetupPreviewServer|RegisterPreviewHandler|WrapPreviewHandler` → zero non-test results.
3. Case-insensitive grep across `pkg/ src/ contracts/ cmd/ tests/` for the JSON keys AND Go identifiers (`PreviewPort|PreviewHost|PreviewOrigin|PreviewListenerEnabled|preview_port|preview_host|preview_origin|preview_listener_enabled|gatewayPreviewBaseURL|IsPreviewListenerEnabled|previewListenerEnabledDefault|ValidateAndApplyPreviewDefaults|shouldWarnPreviewOrigin|PREVIEW_PORT`) → zero non-test, non-generated results.
4. Compile + `make verify-contracts` pass. (Note: deleting the keys does not break boot; an upgrading operator's persisted `preview_*` values are silently dropped — accepted under "no back-compat".)

### US-3 — serve_web returns the correct URL from the canonical gateway origin (P0)
`serve_web` builds the URL from the **canonical gateway origin** — `gateway.public_url` (set) else `http://localhost:<gateway.port>` — the SAME origin used by CORS/CSP/WS `CheckOrigin`. Because `public_url` **stays restart-gated**, the origin is boot-stable, so `serve_web`'s host cannot desync from the origin fences. `WebServeTool` gets a live-config accessor so `preview_enabled` is read live.

**Acceptance**:
1. `public_url=https://pod.example.com` → `url` starts `https://pod.example.com/preview/`.
2. no `public_url` → `url` starts `http://localhost:<gateway.port>/preview/`.
3. `serve_web`'s host equals `CanonicalGatewayOrigin(cfg)` (no desync from CORS/CSP/WS `CheckOrigin`); it does NOT read `public_url` "live" (that key is restart-gated).
4. `preview_enabled` toggled false→true at runtime → the next `serve_web` reflects it (URL vs error), while the host still equals the boot canonical origin.

### US-4 — Live "Preview" toggle in Settings (P1)
Settings → Gateway shows a "Preview" toggle (default ON) bound to `gateway.preview_enabled`. Disabling makes `/preview/` 404 and `serve_web` refuse — immediately; re-enabling restores it live. Disabling does NOT tear down already-running dev servers — they keep running (unreachable via `/preview/`) and are reclaimed by the existing `DevServerRegistry` idle-TTL (documented resource behavior, r4 OBS-004).

**Acceptance**:
1. Toggle off → save `preview_enabled=false` → `/preview/` 404 on the next request (hot-reload).
2. `preview_enabled=false` → `serve_web` errors ("preview disabled").
3. Toggle on → `/preview/` + `serve_web` work immediately (no restart).
4. On disable, running dev servers are NOT force-killed; they remain until idle-TTL reaps them (documented).

### US-5 — Browser-managed login via the existing HttpOnly session cookie (P0)
The SPA authenticates via the `omnipus-session` HttpOnly cookie; no JS-readable token. Cross-tab login preserved; programmatic clients keep `Authorization: Bearer` (both accepted — additive). **Scope note:** cookie auth is added to the ENTIRE authenticated route set for the first time (`RequireSessionCookieOrBearer` is on zero live routes today) — treat the blast radius accordingly.

Scope:
- **Backend (additive):** `checkBearerAuth`/`withAuthAndBodyLimit` + `authenticateWS` resolve identity from `omnipus-session` (via `ResolveUserFromCookie`) when no `Bearer` header is present; the WS handshake accepts the cookie alongside the existing `{type:"auth",token}` frame; neither credential → 401 (fail-closed).
- **Onboarding (r4 MAJ-003):** `HandleCompleteOnboarding` issues the session cookie bound to `body.Admin.Username`, **after** the admin user is persisted to `gateway.users`, and returns **500** (onboarding NOT marked complete) if `IssueSessionCookie` errors — never 200-without-cookie.
- **CSRF durability (r3 C-1 + r4 MAJ-004):** the CSRF cookie `MaxAge` matches the session (24 h); the gateway re-mints it on any authenticated safe request lacking it; the SPA reads the CSRF cookie **fresh from `document.cookie` per state-changing request** (never a cached copy); on a 403-CSRF response the SPA performs a safe GET (to trigger re-mint) and retries once.
- **SPA:** stop persisting/reading the token; `credentials:'include'`; echo CSRF as above; **logout calls `POST /api/v1/auth/logout`** (clears both cookies server-side).
- **Preview isolation (r4 MAJ-002):** the reverse proxy strips ALL request cookies + `Authorization` before forwarding, and neutralizes upstream `Set-Cookie` for the reserved names (`omnipus-session`, `csrf`, `__Host-csrf`) in the dev-server response.

**Acceptance**:
1. After login/onboarding, `sessionStorage`/`localStorage` `omnipus_auth_token` both `null`.
2. Cookie-only `GET /api/v1/*` (no `Authorization`) authenticates.
3. Cookie + fresh CSRF echo `POST /api/v1/*` succeeds; without the CSRF echo → 403.
4. `Authorization: Bearer <valid>` + no cookie still authenticates (programmatic path intact).
5. Neither cookie nor bearer → 401 (fail-closed).
6. A fresh install that completes onboarding authenticates via the cookie on the next call; if `IssueSessionCookie` fails, onboarding returns 500 (no silent lockout).
7. A returning user whose CSRF cookie expired can POST after a GET re-mints it; a returning user whose FIRST action is a POST gets a 403 that the SPA recovers via GET-then-retry.
8. After SPA logout, replaying the old `omnipus-session` cookie → 401 (server cleared it).
9. A previewed dev app's `Set-Cookie: omnipus-session=x` (or `csrf=x`) does NOT reach the browser as an origin cookie; the operator's session/CSRF cookies cannot be fixated.
10. A `/preview/<token>/…` forwarded request carries no `omnipus-session`, no other origin cookie, and no `Authorization`.
11. Second browser tab of the app is already authenticated (no re-login).

### US-6 — requires_restart honesty (P1)
The deleted preview keys are removed from `RestartGatedKeys`. `gateway.public_url` **MUST REMAIN** restart-gated (it drives boot-frozen CORS/CSP/WS `CheckOrigin`). `preview_enabled` is NOT restart-gated.

**Acceptance**:
1. `RestartGatedKeys` contains no removed preview key and **MUST still contain `gateway.public_url`** (intentional — r3 M-2).
2. `isRestartRequired("gateway.preview_enabled")` → `false`.
3. Compile-time: `GatewayPreviewPort/Host/Origin/ListenerEnabled` consts are deleted, so `RestartGatedKeys` no longer references them.

### US-7 — CSRF does not block previewed-app requests (P0)
The `/preview/` **prefix** is exempt from CSRF + origin checks for ALL methods (tokenized URLs never match exact-path exemptions). `/api/v1/*` keeps full CSRF.

**Acceptance**:
1. `POST /preview/<token>/…` without a CSRF token → reaches the dev server, not 403.
2. `POST /api/v1/*` without the CSRF token → 403 (exemption scoped to `/preview/`).
3. `RequireMatchingOriginOnStateChanging` does not block a state-changing `/preview/` request.

### US-8 — AboutResponse contract updated (P0)
Drop `preview_port` + `preview_listener_enabled` + `preview_origin`; add `preview_enabled` (bool); regenerate both trees; migrate SPA consumers; `make verify-contracts` green.

**Acceptance**:
1. Schema updated + `scripts/gen-contracts.sh` regenerates committed artifacts.
2. `contract_test.go` — the Go `AboutResponse` producer emits schema-valid JSON.
3. `grep preview_port|previewPort|preview_origin src/` → zero results.
4. `make verify-contracts` exits 0.

### US-9 — Preview presented via link + built-in browser, NOT an embedded iframe (P0)
Chat renders the preview URL as a clickable link (opens the user's real browser). Dev previews are NOT embedded as a same-origin iframe (`IframePreview` embed + `isSameOriginAsApp` retired for this surface; `WebServeUI` uses the link). The agent reviews and presents the preview via the built-in browser live panel (screencast — not a same-origin frame).

**Acceptance**:
1. A `serve_web` result renders as a safe clickable link; no `<iframe>` embeds the dev preview.
2. The agent navigates the built-in browser to the preview URL; the live panel shows the app and the user can interact.
3. Grep the dev-preview render path → no same-origin iframe embed.

### US-10 — Agent built-in browser reaches the preview (SSRF, scoped, new code) (P0)
The built-in browser's SSRF checker allows the preview URL: a public `public_url` host already passes; for localhost it allows **only the gateway's own host:port** — not blanket loopback, not arbitrary dev ports. **This is new code** (the checker is port-blind; `validateSSRFAllowInternal` rejects host:port): add a port-aware gateway-origin exception evaluated **before** the `127.0.0.0/8` CIDR block, matching the exact host form serve_web emits — the **literal `localhost:<gateway.port>` token pre-resolution**, plus `127.0.0.1:<port>` and `::1:<port>` post-resolution (r4 OBS-003).

**Acceptance**:
1. `public_url` set → agent browser opens the preview URL → SSRF passes.
2. no `public_url` → agent browser opens `http://localhost:<gateway.port>/preview/…` (the exact form serve_web emits) → SSRF passes.
3. agent browser targets a **different** local port (`localhost:<other>`) → SSRF blocks it.
4. The exception is scoped to the gateway's own host:port only; blanket loopback stays blocked.

---

## Behavioral Contract

- Boot: `/preview/` ALWAYS on the main mux (bare, token-in-path, + configSnapshot); no separate preview listener.
- `serve_web` with `preview_enabled=true` → canonical-origin URL; with `false` → error. Host == boot canonical origin, always.
- Any method to `/preview/<token>/…` → `HandlePreview`, token-gated, no session/CSRF/origin gate.
- `/api/v1/*` → cookie-OR-bearer auth (neither → 401) + CSRF (missing → 403) + origin protections.
- CSRF cookie lives 24h and is re-minted on authed safe requests; the SPA reads it fresh per state-changing request and recovers a 403 via GET-then-retry.
- `preview_enabled` toggled → effective next request (no restart); `public_url` change → requires restart (documented).
- SPA authenticates via `omnipus-session` cookie; logout round-trips to the server.
- Onboarding/login/register-admin all issue the session cookie; onboarding fails 500 if it can't.
- Reverse proxy strips all request cookies + `Authorization` and neutralizes reserved `Set-Cookie` in the response.
- Chat renders the preview as a link; the agent presents via the built-in browser; no embedded same-origin iframe.
- Agent browser SSRF allows only the gateway origin (host:port) for localhost; public hosts pass.

## Explicit Non-Behaviors

- MUST NOT bind a second **preview** listener; no preview mux/server.
- MUST NOT keep `preview_port/host/origin/listener_enabled` — deleted, not deprecated.
- MUST NOT require a restart to toggle `preview_enabled`. (`gateway.public_url` DOES require a restart — intentional.)
- MUST NOT store the auth token in `localStorage`/`sessionStorage`.
- MUST NOT wrap `/preview/` in session/CSRF/origin **checks** (it keeps only the configSnapshot).
- MUST NOT forward any origin cookie or `Authorization` to the dev server; MUST NOT let a dev-server `Set-Cookie` plant/overwrite `omnipus-session`/`csrf`/`__Host-csrf` on the shared origin (anti-fixation).
- MUST NOT weaken CSRF/origin on `/api/v1/*` — now load-bearing.
- MUST NOT remove the `Authorization: Bearer` path — programmatic/CLI clients depend on it.
- MUST NOT embed a dev preview as a same-origin iframe.
- MUST NOT allowlist "all localhost" in the browser SSRF checker — exact gateway host:port only.
- **Accepted residual (r2 C-3 + r3 M-3, stated not hidden — READ *and* WRITE vectors):** a top-level preview tab the USER opens is same-origin with the SPA; while that tab is open, a malicious previewed app can (reads) read the readable CSRF cookie and ride the auto-attached `omnipus-session` cookie to call `/api/v1/*` and open `/api/v1/{chat,browser}/ws`. The proxy sanitization (FR-013) closes the WRITE/fixation vector via the proxy, and the token cannot be exfiltrated off-machine (HttpOnly), but same-origin in-browser session-riding cannot be fully closed without a distinct origin (ruled out by the hosting model). This is the accepted single-user-desktop posture (the "attacker" is the user's own agent-generated app). The agent's built-in browser is a separate process and carries no SPA session.
- **Documented caveats:** (a) the agent's built-in browser sees the previewed app logged out (separate process) — expected. (b) `SameSite=Strict` → no cookie on the first cross-site top-level navigation (external deep-link) — kept Strict because previews open same-site; revisit `Lax` only if cross-site deep-link entry becomes a requirement. (c) on upgrade, existing logged-in users hold a JS token but no session cookie → the SPA stops reading the token → they are **logged out once** and must re-login (migration-UX event — see Operability).

## Integration Boundaries

| System | Data Flow | Contract | Failure |
|---|---|---|---|
| Agent built-in browser (chromedp live panel) | navigates to the preview URL | SSRF allows the gateway origin (literal `localhost:<port>` + resolved forms); public hosts pass | other local ports blocked; unreachable → nav error in the panel |
| User's real browser (SPA) | `/api/v1/*` + WS via `omnipus-session` cookie | cookie OR bearer; CSRF fresh-echo on state-changing | missing/expired cookie → 401; missing CSRF → 403 (SPA recovers via GET-retry) |
| User's real browser (preview) | opens the chat **link** `/preview/<token>/…` | token-in-path | unknown/expired token → 404 |
| Previewed dev server | receives proxied requests | MUST NOT receive origin cookies/Authorization; its `Set-Cookie` for reserved names is dropped | proxy strips/neutralizes |
| Settings UI (TanStack Query) | reads/writes `preview_enabled` | GET/PUT config + `AboutResponse.preview_enabled` | hot-reload applies immediately |
| Playwright e2e | auth via cookie in `storageState` (real UI login in global-setup) | `storageState` carries cookies; server holds the matching bcrypt `SessionTokenHash` | direct cookie injection without a seeded hash fails validation |

---

## BDD Scenarios

### S1 — serve_web public URL (Happy) — *US-1 AS-1, US-3 AS-1*
**Given** `public_url="https://pod.example.com"`, `preview_enabled=true` **When** `serve_web(command="npm run dev")` **Then** `url` = `https://pod.example.com/preview/<agent>/<token>/`.

### S2 — serve_web localhost URL (Happy) — *US-1 AS-2, US-3 AS-2*
**Given** no `public_url`, `gateway.port=5000` **When** `serve_web` **Then** `url` = `http://localhost:5000/preview/<agent>/<token>/`.

### S3 — serve_web host == canonical origin; preview_enabled read live (Edge) — *US-3 AS-3,4*
**Given** the gateway booted with a fixed `public_url` **When** `preview_enabled` is toggled false→true at runtime and `serve_web` runs **Then** it reflects the live `preview_enabled` (URL when true, error when false) **And** the URL host still equals the boot `CanonicalGatewayOrigin` (serve_web does NOT read `public_url` live).

### S4 — /preview/ on main mux, all methods, race-free config (Happy) — *US-1 AS-3, US-7 AS-1*
**Given** `preview_enabled=true` **When** `POST /preview/<valid_token>/submit` arrives on the main listener **Then** it reverse-proxies to the dev server, no session/CSRF/origin rejection, with a config snapshot present (no torn read).

### S5 — /preview/ 404 when disabled (Error) — *US-1 AS-4, US-4 AS-1*
**Given** `preview_enabled=false` **When** any `/preview/` request **Then** 404.

### S6 — No separate preview listener (Happy) — *US-2 AS-1,2*
**Given** boot **Then** no separate preview listener; `/preview/` shares the main port; no `previewServer`/`previewMux` field.

### S7 — Toggle off then on, dev servers survive (Alternate) — *US-4 AS-1,2,3,4*
**Given** `preview_enabled=true` with a running dev server **When** set `false` **Then** `/preview/` 404 + `serve_web` errors immediately **And** the running dev server is not force-killed **When** set `true` **Then** both work immediately (no restart).

### S8 — No JS token after login OR onboarding (Happy) — *US-5 AS-1,6*
**Given** a completed login AND a completed fresh-install onboarding **When** `localStorage`/`sessionStorage` `omnipus_auth_token` are read **Then** both `null` **And** the `omnipus-session` cookie is present.

### S9 — Cookie authenticates /api/v1 + WS; neither → 401 (Happy+Error) — *US-5 AS-2,5*
**Given** a logged-in browser with only the cookie **When** `GET /api/v1/state` and the WS handshake occur **Then** both authenticate **And** a request with neither cookie nor bearer → 401.

### S10 — Bearer path intact (Alternate) — *US-5 AS-4*
**Given** `Authorization: Bearer <valid>`, no cookie **When** calling `/api/v1/*` **Then** authenticates.

### S11 — CSRF required on /api, exempt on /preview/ (Error+Edge) — *US-5 AS-3, US-7 AS-1,2*
**Given** cookie auth **When** `POST /api/v1/agents` without a fresh CSRF echo **Then** 403 **When** `POST /preview/<token>/submit` without CSRF **Then** reaches the dev server.

### S12 — Returning user not CSRF-locked-out; POST-first recovers (Error-avoidance) — *US-5 AS-7*
**Given** a valid `omnipus-session` but no CSRF cookie (browser reopened) **When** the SPA makes a GET **Then** the gateway re-mints the CSRF cookie and the next POST (fresh-read echo) succeeds **And When** the returning user's FIRST request is a POST **Then** it 403s and the SPA recovers by doing a GET then retrying once (success).

### S13 — Onboarding issues the cookie or fails 500 (Happy+Error) — *US-5 AS-6*
**Given** a fresh install **When** `POST /onboarding/complete` succeeds **Then** an `omnipus-session` cookie bound to the admin username is set (after user-persist) **And** a subsequent `/api/v1/*` with it authenticates **And When** `IssueSessionCookie` errors **Then** onboarding returns 500 and is not marked complete.

### S14 — Logout clears the server cookie (Happy) — *US-5 AS-8*
**Given** a logged-in browser **When** the SPA logout calls `POST /api/v1/auth/logout` **Then** the response clears `omnipus-session` **And** a later `/api/v1/*` replaying the old cookie → 401.

### S15 — Proxy strips request cookies + neutralizes response Set-Cookie (Happy) — *US-5 AS-9,10*
**Given** the operator's browser holds `omnipus-session` + `csrf` **When** it requests `/preview/<token>/api/foo` and the dev server responds `Set-Cookie: omnipus-session=x` **Then** the forwarded request carries no origin cookie/Authorization **And** the response does not set `omnipus-session`/`csrf`/`__Host-csrf` on the browser.

### S16 — Second tab already authenticated (Happy) — *US-5 AS-11*
**Given** a logged-in browser **When** a second tab of the app opens **Then** it is already authenticated (no re-login).

### S17 — RestartGatedKeys keeps public_url, drops preview keys (Happy) — *US-6 AS-1,2,3*
**Given** the codebase **Then** `RestartGatedKeys` has no removed preview key, **still contains `gateway.public_url`**, and `isRestartRequired("gateway.preview_enabled")` is `false`.

### S18 — AboutResponse contract regenerated & valid (Happy) — *US-8 AS-1,2,4*
**Given** `AboutResponse` drops `preview_port`/`preview_listener_enabled`/`preview_origin`, adds `preview_enabled` **When** gen-contracts + `contract_test.go` + `make verify-contracts` run **Then** trees match, JSON validates, verify-contracts exits 0.

### S19 — Preview renders as a link, not an iframe (Happy) — *US-9 AS-1,3*
**Given** a `serve_web` result in chat **Then** the preview URL renders as a safe clickable link **And** no `<iframe>` embeds the dev preview.

### S20 — Agent presents preview via built-in browser (Happy) — *US-9 AS-2*
**Given** a running preview **When** the agent navigates the built-in browser live panel to the preview URL **Then** the live panel shows the app and the user can interact.

### S21 — Browser SSRF allows the gateway origin only, localhost form (Edge) — *US-10 AS-1,2,3,4*
**Given** the built-in browser SSRF checker **When** it evaluates `http://localhost:<gateway.port>/preview/…` (the literal form serve_web emits) or a public `public_url` host **Then** it passes **When** it evaluates `localhost:<other-port>` **Then** it is blocked.

### S22 — Unknown token 404 (Error) — *US-1 AS-3 (neg)*
**Given** `preview_enabled=true` **When** `/preview/nonexistent-token/` **Then** 404.

### S23 — Upgrade re-login (migration UX) — *Non-Behaviors caveat (c)*
**Given** a user logged in before the upgrade (JS token, no session cookie) **When** the upgraded SPA loads **Then** it no longer reads the token, treats the user as logged out once, and prompts re-login (documented, not an error).

---

## Test-Driven Development Plan

| # | Test | Level | Traces | Description |
|---|---|---|---|---|
| 1 | TestPreviewServedOnMainMux | Unit (Go) | S4 | GET+POST `/preview/<token>/` on main mux reach HandlePreview with a config snapshot |
| 2 | TestPreviewDisabledReturns404 | Unit (Go) | S5 | `preview_enabled=false` → 404 |
| 3 | TestServeWebPublicURL | Unit (Go) | S1 | serve_web → `public_url + /preview/…` |
| 4 | TestServeWebLocalhostURL | Unit (Go) | S2 | serve_web → `localhost:<port> + /preview/…` |
| 5 | TestServeWebOriginMatchesCanonical | Unit (Go) | S3 | serve_web host == `CanonicalGatewayOrigin`; does NOT read public_url live; reads preview_enabled live |
| 6 | TestServeWebDisabledError | Unit (Go) | S5 | serve_web errors when disabled |
| 7 | TestNoSeparatePreviewListener | Unit (Go) | S6 | no preview listener; no previewServer field |
| 8 | TestUnknownTokenReturns404 | Unit (Go) | S22 | `/preview/bogus/` → 404 |
| 9 | TestRestartGatedKeys_KeepsPublicURL_DropsPreview | Unit (Go) | S17 | no removed preview key; `public_url` STILL present; isRestartRequired(preview_enabled) false |
| 10 | TestWithAuthAcceptsSessionCookie | Unit (Go) | S9 | withAuth route authenticates via cookie, no Authorization |
| 11 | TestWithAuthBearerStillWorks | Unit (Go) | S10 | withAuth route still authenticates via Bearer |
| 12 | TestNoCredentialReturns401 | Unit (Go) | S9 | neither cookie nor bearer → 401 |
| 13 | TestWSAuthAcceptsSessionCookie | Integration (Go) | S9 | WS handshake authenticates via cookie (alongside auth-frame) |
| 14 | TestOnboardingIssuesSessionCookie_RoundTrip | Integration (Go) | S13 | onboarding sets cookie bound to admin username; subsequent `/api/v1/*` authenticates |
| 15 | TestOnboardingCookieFailureReturns500 | Unit (Go) | S13 | IssueSessionCookie error → onboarding 500, not marked complete |
| 16 | TestCSRFRequiredOnAPIExemptOnPreview | Integration (Go) | S11 | POST `/api/v1` needs fresh CSRF (403 without); POST `/preview/` exempt |
| 17 | TestCSRFCookieRemintAndMaxAge | Integration (Go) | S12 | valid session + missing CSRF → GET re-mints (MaxAge=24h) → POST succeeds |
| 18 | TestCSRFPostFirstRecovery | Integration (Go)/Unit (TS) | S12 | POST-first returning user 403 → SPA GET-then-retry succeeds; SPA reads cookie fresh per request |
| 19 | TestPreviewProxyStripsRequestCookies | Unit (Go) | S15 | forwarded dev request has no origin cookie / Authorization |
| 20 | TestPreviewProxyNeutralizesSetCookie | Unit (Go) | S15 | dev-server `Set-Cookie: omnipus-session/csrf` is dropped from the browser response |
| 21 | TestLogoutClearsSessionCookie | Unit (Go) | S14 | `POST /api/v1/auth/logout` clears both cookies; SPA logout calls it |
| 22 | TestPreviewToggleHotReload | Integration (Go) | S5,S7 | toggle at runtime; 404 then works; running dev server not force-killed |
| 23 | TestBrowserSSRFAllowsGatewayLocalhostOnly | Unit (Go) | S21 | `localhost:<gateway.port>` (literal) passes; `localhost:<other>` blocked; blanket loopback blocked |
| 24 | TestAboutResponseContractValid | Unit (Go) | S18 | AboutResponse producer emits schema-valid JSON; no `preview_port` |
| 25 | auth.store.test.ts — no JS token + fresh CSRF | Unit (TS) | S8,S11 | login/onboarding store no token; state-changing calls read CSRF fresh from document.cookie |
| 26 | GatewaySection.test.tsx — preview toggle | Unit (TS) | S7 | Settings shows the toggle; off saves `preview_enabled=false` |
| 27 | preview-render.test.tsx — link not iframe | Unit (TS) | S19 | serve_web result renders as a link; no dev-preview `<iframe>` |
| 28 | preview-url.test.ts — main origin + preview_enabled | Unit (TS) | S1,S2 | URL main origin; no `preview_port`/`preview_origin` |
| 29 | TestServeWebEndToEnd | Integration (Go) | S1,S2,S4 | serve_web → navigate → dev build via main mux |
| 30 | e2e: cookie storageState auth (UI login in setup) | E2E (Playwright) | S8,S9 | authenticated specs pass via cookie `storageState` seeded by a real login |
| 31 | e2e: preview link + built-in browser | E2E (Playwright) | S19,S20 | serve_web link renders; agent live-panel navigation shows the app |

### Test Datasets

| Test | Input | Expected | Traces |
|---|---|---|---|
| URL | `public_url="https://pod.example.com"` | `https://pod.example.com/preview/agent/token/` | S1 |
| URL | `public_url=""`, port=5000 | `http://localhost:5000/preview/agent/token/` | S2 |
| Origin | serve_web host vs CanonicalGatewayOrigin | equal | S3 |
| Live toggle | `preview_enabled` false→true, `public_url` fixed | URL flips error→URL; host unchanged | S3 |
| Auth | cookie only / bearer only / neither | authed / authed / 401 | S9 |
| Onboarding | complete success / IssueSessionCookie fail | cookie set + authed / 500 not-complete | S13 |
| CSRF | POST `/api/v1` fresh echo / no echo / POST-first re-open | 200 / 403 / 403-then-GET-retry-200 | S11,S12 |
| CSRF | POST `/preview/<token>/…` no CSRF | reaches dev server | S11 |
| Proxy req | cookies+Authorization present | forwarded has neither | S15 |
| Proxy resp | dev `Set-Cookie: omnipus-session=x` / `csrf=x` | dropped from browser response | S15 |
| SSRF | `localhost:<gateway.port>` (literal) / public host / `localhost:<other>` / `127.0.0.1:<gateway.port>` | pass / pass / block / pass | S21 |
| Token | valid dev / valid static / unknown / expired | proxy / file / 404 / 404 | S4,S22 |
| Toggle | true→false→true | 404 then works, no restart, dev server survives | S5,S7 |
| Contract | AboutResponse producer output | schema-valid; no `preview_port`/`preview_origin` | S18 |
| Render | serve_web result | link element, no dev-preview iframe | S19 |

### Regression Tests

1. **`wave3_bearer_auth_test.go`** — bearer paths unchanged; cookie path ADDITIVE (add cookie cases).
2. **`rest_auth_cookie_test.go` / `session_cookie_test.go`** — login issues both cookies; logout clears both; add the onboarding-issues-cookie + onboarding-failure-500 cases; CSRF cookie now `MaxAge=24h`.
3. **serve_web + preview handler tests** — reverse-proxy/registry/lifecycle/teardown unchanged; separate-port-URL / preview-mux-registrar assertions UPDATED to main origin/mux; `preview_config_test.go`, `preview_disabled_test.go`, `preview_listener_hot_flip_test.go`, `preview_iframe_test.go`, `preview_origin_warn_test.go` updated or deleted.
4. **`csrf_test.go`** — `/preview/` prefix exemption changes no `/api/v1` behavior; add re-mint + MaxAge + cookie-auth-now-enforced cases.
5. **Config tests** — removing the preview keys + defaults (incl Android/Termux) must not break boot; `RestartGatedKeys` test updated (keeps `public_url`, drops preview keys); `TestLoadConfig_LegacyPreviewFields_Ignored` mirrors the ADR-035 SandboxProfile precedent (silent-ignore of the dropped keys).
6. **`IframePreview` tests** — same-origin-guard tests for the dev-preview surface removed with the embed path; add the link-render test (27).
7. **Playwright e2e auth** — migrate `tests/e2e/global-setup.ts` + `tests/e2e/auth.spec.ts` from the localStorage token mirror to a cookie `storageState`. **Preferred: real UI login in global-setup** so the server holds the matching bcrypt `SessionTokenHash` (direct cookie injection without a seeded hash fails validation). Fix for r1 CRIT-002.

---

## Functional Requirements

- **FR-001**: MUST serve `/preview/<token>/…` on the main listener for ALL methods.
- **FR-002**: MUST register `/preview/` bare — token-in-path only; it MUST skip CSRF/origin/session-auth **checks** but MUST retain the global `configSnapshotMiddleware` so live-config reads are race-free (no torn read).
- **FR-003**: MUST NOT bind a second **preview** listener; MUST remove `previewMux/previewServer/SetupPreviewServer/RegisterPreviewHandler/WrapPreviewHandler` + the boot-frozen `gatewayPreviewBaseURL` block.
- **FR-004**: MUST remove `preview_port/host/origin/listener_enabled` + per-OS/Termux defaults + `ValidateAndApplyPreviewDefaults` + `shouldWarnPreviewOrigin` + the `doctor` preview checks + `pkg/config/keys.go` preview consts.
- **FR-005**: `serve_web` MUST build the URL from the canonical gateway origin (`gateway.public_url` else `http://localhost:<gateway.port>`) — the SAME origin as CORS/CSP/WS `CheckOrigin`; it MUST NOT read `public_url` "live" (that key is restart-gated). `WebServeTool` MUST receive a live-config accessor (`func() *config.Config`) to read `preview_enabled` live.
- **FR-006**: MUST provide `gateway.preview_enabled` (default `true`), read live; `false` → `/preview/` 404 + `serve_web` error. Disabling MUST NOT force-kill running dev servers (they idle-TTL out).
- **FR-007**: `preview_enabled` MUST NOT be restart-gated. The deleted preview keys MUST be removed from `RestartGatedKeys`; `gateway.public_url` **MUST REMAIN** in `RestartGatedKeys` (drives boot-frozen origin fences).
- **FR-008**: Settings → Gateway MUST show a "Preview" toggle bound to `preview_enabled`.
- **FR-009**: `checkBearerAuth`/`withAuthAndBodyLimit` + `authenticateWS` MUST authenticate via `omnipus-session` (via `ResolveUserFromCookie`) when no `Bearer` header is present; the `Authorization: Bearer` path MUST remain; **neither credential → 401 (fail-closed)**.
- **FR-010**: The SPA MUST NOT store the auth token in `sessionStorage`/`localStorage`; MUST send `credentials:'include'`; MUST read the CSRF cookie **fresh from `document.cookie` per state-changing request** (never cached); on a 403-CSRF response MUST perform a safe GET then retry once.
- **FR-011**: `HandleCompleteOnboarding` MUST issue the `omnipus-session` cookie bound to `body.Admin.Username`, ordered **after** the admin user is persisted; if `IssueSessionCookie` errors it MUST return **500** and NOT mark onboarding complete (never 200-without-cookie).
- **FR-012**: The CSRF middleware MUST exempt the `/preview/` **prefix** for ALL methods while enforcing CSRF on `/api/v1/*`; `RequireMatchingOriginOnStateChanging` MUST not block `/preview/`.
- **FR-013**: The preview reverse proxy MUST strip ALL request cookies + `Authorization` before forwarding to the dev server, AND MUST neutralize upstream `Set-Cookie` for the reserved names (`omnipus-session`, `csrf`, `__Host-csrf`) in the dev-server response (anti-fixation).
- **FR-014**: `RestartGatedKeys` + `isRestartRequired` MUST NOT classify any removed preview key as restart-gated (and MUST keep `public_url` gated).
- **FR-015**: `AboutResponse` MUST drop `preview_port` + `preview_listener_enabled` + `preview_origin`, add `preview_enabled` (bool); both generated trees regenerated + committed; all SPA `preview_port`/`preview_origin` consumers migrated.
- **FR-016**: Chat MUST render a dev `serve_web` preview as a safe clickable link; it MUST NOT embed the dev preview as a same-origin iframe.
- **FR-017**: The agent MUST be able to navigate the built-in browser live panel to the preview URL and present it to the user.
- **FR-018**: The built-in browser SSRF checker MUST allow the preview URL — public hosts pass; for localhost it MUST allow ONLY the gateway's own host:port via **new port-aware code** (evaluated before the `127.0.0.0/8` block), matching the literal `localhost:<gateway.port>` form serve_web emits plus `127.0.0.1:<port>`/`::1:<port>`; it MUST still block other local ports and blanket loopback.
- **FR-019**: The CSRF cookie's `MaxAge` MUST match the session (24 h), and the gateway MUST re-mint the CSRF cookie on any authenticated safe (GET) request that lacks it.
- **FR-020**: The SPA logout MUST call `POST /api/v1/auth/logout` (clears both cookies server-side); client-side clearing alone MUST NOT be the only logout step.
- **FR-021** (observability, r4 OBS-002): The gateway MUST emit a structured log/metric for cookie-vs-bearer resolution failures (vs absence), the cookie/bearer identity **mismatch** case, CSRF re-mint, logout, and preview-disabled 404s (distinguishable from unknown-token 404s), so a cookie-auth regression is not indistinguishable from a legitimate 401/404.

## Success Criteria

- **SC-001**: After boot, no separate **preview** listener binds; SPA + API + `/preview/` share the main `gateway.port` listener (the health server's own `http.Server` is unchanged/out of scope).
- **SC-002**: `serve_web` returns a canonical-origin URL the agent browser navigates to; a POST through `/preview/` reaches the dev server; serve_web's host equals the origin used by CORS/CSP/WS.
- **SC-003**: Toggling "Preview" off immediately 404s `/preview/` (no restart); on restores it; running dev servers are not force-killed.
- **SC-004**: After login AND fresh onboarding, no `omnipus_auth_token` in JS storage; `/api/v1/*` + WS authenticate via the cookie; a second tab is already logged in; neither credential → 401.
- **SC-005**: A `/preview/` request delivers no origin cookie/`Authorization` to the dev server, and a dev-server `Set-Cookie` for a reserved name never lands on the browser (no fixation).
- **SC-006**: A returning user (session valid, CSRF cookie expired) can perform a state-changing action — via GET-remint or POST-then-GET-retry — no unrecoverable 403.
- **SC-007**: After SPA logout, replaying the old `omnipus-session` cookie → 401.
- **SC-008**: A fresh install that completes onboarding is authenticated via the cookie; an onboarding that cannot issue the cookie returns 500 (no silent lockout).
- **SC-009**: A dev `serve_web` preview renders as a link (no iframe); the agent presents it via the built-in browser; the agent browser reaches `localhost:<gateway.port>` but not other local ports.
- **SC-010**: Expanded grep (SC-006's pattern set, case-insensitive, across `pkg/ src/ contracts/ cmd/ tests/`) returns zero non-test/non-generated results; `grep -rn 'omnipus_auth_token' src/` → zero.
- **SC-011**: `make verify-contracts` exits 0.
- **SC-012**: Full CI green — gofmt, golangci-lint, go-build, go-test, govulncheck, vitest, typecheck, contracts. The e2e specs added by this change (tests 30–31) pass; per project policy e2e (esp. LLM shards) is not a hard blocking gate and follows the established single-shard re-run allowance for flake.
- **SC-013**: The 7-reviewer gate runs on the feature diff and the epic diff; every finding resolved or explicitly deferred with a tracked issue.

### Traceability Matrix

| FR | US | BDD | Tests |
|---|---|---|---|
| FR-001 | US-1 | S4 | TestPreviewServedOnMainMux, TestServeWebEndToEnd |
| FR-002 | US-1 | S4 | TestPreviewServedOnMainMux |
| FR-003 | US-2 | S6 | TestNoSeparatePreviewListener |
| FR-004 | US-2 | S6 | TestNoSeparatePreviewListener (grep), config regression |
| FR-005 | US-3 | S1,S2,S3 | TestServeWebPublicURL, TestServeWebLocalhostURL, TestServeWebOriginMatchesCanonical |
| FR-006 | US-4 | S5,S7 | TestPreviewDisabledReturns404, TestServeWebDisabledError, TestPreviewToggleHotReload |
| FR-007 | US-6 | S17 | TestRestartGatedKeys_KeepsPublicURL_DropsPreview |
| FR-008 | US-4 | S7 | GatewaySection.test.tsx |
| FR-009 | US-5 | S9,S10 | TestWithAuthAcceptsSessionCookie, TestWithAuthBearerStillWorks, TestNoCredentialReturns401, TestWSAuthAcceptsSessionCookie |
| FR-010 | US-5 | S8,S11,S12 | auth.store.test.ts, TestCSRFPostFirstRecovery |
| FR-011 | US-5 | S13 | TestOnboardingIssuesSessionCookie_RoundTrip, TestOnboardingCookieFailureReturns500 |
| FR-012 | US-7 | S11 | TestCSRFRequiredOnAPIExemptOnPreview |
| FR-013 | US-5 | S15 | TestPreviewProxyStripsRequestCookies, TestPreviewProxyNeutralizesSetCookie |
| FR-014 | US-6 | S17 | TestRestartGatedKeys_KeepsPublicURL_DropsPreview |
| FR-015 | US-8 | S18 | TestAboutResponseContractValid, preview-url.test.ts |
| FR-016 | US-9 | S19 | preview-render.test.tsx |
| FR-017 | US-9 | S20 | e2e: preview link + built-in browser |
| FR-018 | US-10 | S21 | TestBrowserSSRFAllowsGatewayLocalhostOnly |
| FR-019 | US-5 | S12 | TestCSRFCookieRemintAndMaxAge |
| FR-020 | US-5 | S14 | TestLogoutClearsSessionCookie |
| FR-021 | US-5 | S9,S14 | (log/metric assertions in TestWithAuthAcceptsSessionCookie, TestLogoutClearsSessionCookie) |

Every FR (FR-001…FR-021) and every BDD scenario (S1…S23) appears above; S22 (unknown-token 404) traces via FR-001, S23 (upgrade re-login) is a documented Non-Behavior caveat with no FR (migration UX, not a behavior to build).

---

## Ambiguity Warnings

| # | Ambiguity | Assumption | Resolution |
|---|---|---|---|
| 1 | Whether the SPA echoes CSRF today (Bearer skips it) | It does not | FR-010 adds fresh-read CSRF echo + GET-retry recovery; tests 17,18,25 |
| 2 | Exact reserved-cookie handling in the proxy | Strip all request cookies; neutralize reserved `Set-Cookie` in the response | FR-013; tests 19,20 |
| 3 | SSRF host representation (`localhost` vs `127.0.0.1` vs `::1`) | Match the literal `localhost:<gateway.port>` form serve_web emits, pre-resolution, plus resolved loopback forms | FR-018; test 23 asserts the literal `localhost:<port>` form |
| 4 | CSRF re-mint ordering (POST-first) | SPA reads CSRF fresh per request + GET-then-retry on 403 | FR-010/FR-019; tests 17,18 |

All are implementation-verification items; the operator's five decisions + four grill rounds resolved every product-level contradiction.

---

## Operability (r4 OBS-001/002/004)

- **Rollback / cutover:** the Bearer→cookie change is the highest-blast-radius piece. Because the backend accepts **both** cookie and bearer (additive), a rollback is: revert the SPA to sending the bearer token (restore `getAuthHeaders`/token storage) — the backend cookie-acceptance can stay. There is no separate feature flag; the SPA layer is the flip. **Document in the PR** that on upgrade, existing logged-in users are logged out once (JS token dropped, no cookie yet) and must re-login (Non-Behaviors caveat c).
- **Observability (FR-021):** emit WARN/metric on cookie-resolution failure vs absence, on the cookie/bearer identity mismatch (already logged at a configurable level in `session_cookie.go` — surface it), on CSRF re-mint, on logout, and a counter distinguishing preview-disabled 404s from unknown-token 404s.
- **Dev-server lifecycle on disable (FR-006):** disabling `preview_enabled` leaves running dev servers alive (unreachable via `/preview/`); the existing `DevServerRegistry` idle-TTL reaps them. Documented, not a leak-forever.

---

## Holdout Evaluation Scenarios

1. **(Happy)** Boot with `public_url`, `serve_web`, click the chat link in a real browser → see the dev build.
2. **(Happy)** Boot without `public_url`, `serve_web`, the agent opens the preview in the built-in browser live panel → the user sees the app.
3. **(Happy)** In a previewed app with its own login form, submit it (POST through `/preview/`) → works (no 403).
4. **(Happy)** Toggle "Preview" off → `/preview/` 404s; on → works. Same process, no restart; a running dev server survives the toggle.
5. **(Error)** `serve_web` with an invalid path → clear error, no crash.
6. **(Edge)** Log in, open a SECOND tab → already authenticated.
7. **(Edge)** Complete a fresh install onboarding → immediately authenticated (cookie); simulate a cookie-issue failure → onboarding 500, not locked out silently.
8. **(Edge)** Reopen the browser same-day (CSRF cookie expired) and immediately trigger a save → it succeeds (re-mint or GET-retry).
9. **(Edge)** Log out → replay the old cookie → 401.
10. **(Edge)** A previewed app tries `Set-Cookie: omnipus-session=x` → it does not affect the operator's session.
11. **(Edge)** After login, inspect JS storage → no `omnipus_auth_token`; cookie jar shows `omnipus-session` (HttpOnly).
12. **(Edge)** Point the agent browser at `localhost:<gateway.port>` → reaches the preview; at a different local port → SSRF-blocked.
13. **(Edge)** Confirm the chat preview is a link (no embedded iframe); confirm no separate preview listener binds.
