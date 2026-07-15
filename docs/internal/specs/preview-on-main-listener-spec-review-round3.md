# Spec Grill Review (Round 3) — Preview on Main Listener

**Spec under review**: `docs/internal/specs/preview-on-main-listener-spec.md` (v3, 2026-07-15)
**Input ADR**: `docs/internal/architecture/ADR-044-preview-on-main-listener.md`
**Prior reviews**: `preview-on-main-listener-spec-review.md` (r1), `preview-on-main-listener-spec-review-round2.md` (r2)
**Mode**: `plan-spec` (BDD scenarios + FR-xxx + SC-xxx + traceability matrix present)
**Reviewer stance**: adversarial, read-only. Every finding below is grounded in the current `hotfix/v0.1.1` code.

---

## Executive Summary

This v3 spec resolves the product-level contradictions from rounds 1–2, but the **auth-model migration it folds in (Bearer→HttpOnly cookie) introduces a new, code-grounded regression that rounds 1–2 did not exist to catch**: once the SPA drops its Bearer token and CSRF becomes load-bearing, the mismatched lifetimes of the session cookie (24 h, persistent) and the CSRF cookie (session-only, dies on browser close) silently lock every returning same-day user out of all writes. Three further MAJOR findings concern an infeasible port-aware SSRF requirement, an un-gated `public_url` whose CSP consumer stays boot-frozen, and cookie-on-WS-handshake extending the accepted same-origin residual to the agent-control WebSocket without saying so.

**Findings**: 1 CRITICAL, 4 MAJOR, 6 MINOR, 2 OBSERVATION.

**Verdict**: **BLOCK** (one CRITICAL).

---

## Findings

| ID | Severity | Lens | Section | Summary |
|----|----------|------|---------|---------|
| C-1 | CRITICAL | Incompleteness / Insecurity(DoS) | US-5, US-7, FR-010, FR-011 | Returning same-day user is authenticated by the 24 h session cookie but has no CSRF cookie (session-scoped, no MaxAge) → all state-changing calls 403 with no recovery path. |
| M-1 | MAJOR | Infeasibility | US-10, FR-018, S16, Ambiguity #3 | Port-aware "gateway host:port only, block other local ports" cannot be done in the existing host-only SSRF checker; allowlisting `localhost` opens every loopback port. |
| M-2 | MAJOR | Inconsistency | US-6, FR-007 | Un-gating `gateway.public_url` from `RestartGatedKeys` leaves the boot-frozen CSP/frame-ancestors consumer (`gateway.go:1887-1916`) silently stale with no restart warning. |
| M-3 | MAJOR | Incompleteness / Insecurity(EoP) | US-5, FR-009 | Cookie-on-WS-handshake lets a same-origin malicious preview open an authenticated agent-control WebSocket without the auth-frame token; the accepted residual is scoped only to `/api/v1/*`. |
| M-4 | MAJOR | Incompleteness | US-3, FR-005, FR-006 | `WebServeTool` holds a construction-frozen `gatewayPreviewBaseURL`; "compute the URL live" and "gate on live `preview_enabled`" have no live-config accessor and no specified injection mechanism. |
| m-1 | MINOR | Incorrectness | US-2 AS-1, SC-001 | "Exactly ONE TCP listener" is imprecise; assert "no preview listener" (the health server owns its own `http.Server`). |
| m-2 | MINOR | Inconsistency | Non-Behaviors, ADR D3/Risk | CSRF is cited as a mitigation against the same-origin residual, but a same-origin preview reads the readable CSRF cookie and defeats it — CSRF provides zero protection there. |
| m-3 | MINOR | Ambiguity | US-9 AS-2, FR-017 | The agent's built-in browser carries no SPA session, so for a login-gated preview it "reviews" the logged-out app while the user's tab shows the logged-in app — divergence unstated. |
| m-4 | MINOR | Test coverage | Test Datasets, TDD plan | "neither credential → 401" has a dataset row but no BDD scenario and no dedicated test; it is the branch most likely to regress when the cookie fallback is added to `checkBearerAuth`. |
| m-5 | MINOR | Infeasibility | Integration Boundaries, Regression #7 | e2e "inject `omnipus-session`" needs a raw token whose bcrypt `SessionTokenHash` is already seeded server-side; only the "log in during setup" path is turn-key. |
| m-6 | MINOR | Inoperability | US-4, S7 | Toggling `preview_enabled` off 404s `/preview/` but does not tear down already-running dev servers; they linger (resources, egress) until idle TTL — undocumented. |
| O-1 | OBSERVATION | Overcomplexity / scope | whole spec | The Bearer→cookie auth migration is bundled into the preview-listener change, multiplying blast radius across the entire SPA API + WS surface; it is separable and could ship first to de-risk. |
| O-2 | OBSERVATION | Ambiguity | Symbols table | `WebServeTool` lives at `pkg/tools/web_serve.go`, not the `pkg/tools/browser/` implied by the bare filename; line numbers otherwise match. |

---

## Detailed Findings

### C-1 (CRITICAL) — Returning-user write-lockout: session cookie outlives the CSRF cookie

**Grounding**
- Session cookie: `SessionCookieMaxAge = 86400` (24 h), written by `IssueSessionCookie` (`pkg/gateway/middleware/session_cookie.go:78,233`). Persistent across browser restarts.
- CSRF cookie: `IssueCSRFCookie` sets **no `MaxAge`** — "session cookie; lives until the browser closes" (`pkg/gateway/middleware/csrf.go:407`).
- `IssueCSRFCookie` is called at exactly two places: login (`rest_auth.go:484`) and onboarding-complete (`rest_onboarding.go:381`). There is no re-issue path for an already-authenticated session.
- `CSRFMiddleware` (state-changing method, not `/preview/`-exempt, no `Authorization: Bearer`): missing CSRF cookie → `writeCSRFError(w, "csrf cookie missing")` = 403 (`csrf.go:256`+).

**Failure scenario**
A user logs in Monday morning (both cookies issued). They quit the browser at lunch and reopen it at 2pm — within the 24 h session window. The browser restores `omnipus-session` (persistent) but discards the CSRF cookie (session-scoped). The SPA no longer holds a Bearer token (FR-010 deletes it), so:
- `GET /api/v1/*` and the WS handshake authenticate fine via the session cookie (looks logged in).
- The first `POST/PUT/PATCH/DELETE` — create an agent, send a chat message, save a setting — hits `CSRFMiddleware` with a valid session cookie, no Bearer, and **no CSRF cookie** → **403 "csrf cookie missing"**.
- Nothing re-mints the CSRF cookie: the SPA is authenticated, so it never revisits `/auth/login`. The user is silently locked out of every write with no in-app recovery (they must manually log out and back in — which the UI has no reason to prompt).

Under today's Bearer model this never bites: `CSRFMiddleware` skips the check entirely when `Authorization: Bearer` is present (`csrf.go`). Making CSRF load-bearing (US-7) without a CSRF-cookie lifecycle that matches the session cookie is a shipped-feature production incident, and it hits the most ordinary user behaviour (close and reopen the browser same-day).

**Recommended fix (specify one)**
1. Issue/refresh the CSRF cookie on any authenticated request that lacks it — e.g. inside `configSnapshotMiddleware` or on `GET /api/v1/state` — so a returning session self-heals; **or**
2. Give the CSRF cookie the same 24 h `MaxAge` as the session cookie **and** add an explicit re-mint endpoint the SPA calls on boot when the cookie is absent; **or**
3. Have the SPA detect a `403 csrf cookie missing`, call a bootstrap endpoint to re-obtain the pair, and retry.

Add a BDD scenario + test: "valid session cookie, CSRF cookie absent → state-changing POST succeeds (cookie re-minted), not 403." This is the exact gap the current TDD plan (tests 14, 20) does not cover — both assume the CSRF cookie is present.

---

### M-1 (MAJOR) — Port-aware SSRF allowlist is not implementable in the current host-only checker

**Grounding**
- `BrowserManager.ValidateURL` → `m.ssrf.CheckURL(ctx, rawURL)` (`pkg/tools/browser/manager.go:323`+), the gate the browser navigate path uses.
- `SSRFChecker.CheckURL` → `extractHost` **strips the port** via `net.SplitHostPort` and passes host-only to `CheckHost` (`pkg/security/ssrf.go`).
- `CheckHost` short-circuits: `if sc.allowList[strings.ToLower(host)] { … return addrs }` — an allowlisted **hostname** skips all IP/range blocking, **for every port**.

**Why the requirement fails**
FR-018 / US-10 AS-3 / S16 require: allow "ONLY the gateway's own host:port (port-aware)" and "MUST still block other local ports." But the checker has no port concept anywhere in its decision path. Allowlisting `localhost` (or `127.0.0.1`) to let the agent browser reach `http://localhost:<gateway.port>/preview/…` simultaneously opens **every** loopback port to the agent browser — the precise outcome FR-018 forbids. Ambiguity #3 frames this as "verify the allowlist wiring," but it is a **design change to a security-critical component**: real port-awareness requires a new port-scoped check layered before/around `SSRFChecker` (which today deliberately discards the port). The named test `TestBrowserSSRFAllowsGatewayOriginOnly` cannot pass against `NewSSRFChecker` as it exists.

**Recommended fix**
Re-scope FR-018 as new work: specify a port-aware gateway-origin gate (e.g. an exact `host:port` allowlist consulted **before** the SSRF host check in `ValidateURL`, or a dedicated pre-check in the LiveView navigate path), with its own design note and the "other local port still blocked" negative test. Alternatively, if host-level allow is acceptable, delete the "block other local ports" clause — but do not claim port-awareness the checker can't deliver. Note the marginal value: since the gateway reverse-proxies `/preview/` to arbitrary dev ports the agent itself started, the practical isolation gain from port-scoping is small; the operator may prefer the honest host-level allow.

---

### M-2 (MAJOR) — Un-gating `public_url` leaves the boot-frozen CSP/frame-ancestors consumer stale

**Grounding**
- `RestartGatedKeys` currently lists `config.GatewayPublicURL` (`pkg/gateway/rest_pending_restart.go:36-66`). FR-007 removes it.
- `public_url` is consumed at boot to derive frame-ancestors / CSP: `gateway.go:1887` ("frame-ancestors fallback … set gateway.public_url for strict embedding control") and `:1914-1916` (`cfg.Gateway.PublicURL` https check) — inside the same boot block region the spec deletes at `gateway.go:1893-1926`.
- Live consumers exist too (`rest_workspace.go:144`, origin middleware's `canonicalGatewayOrigin`), but the CSP derivation above is boot-frozen.

**Failure scenario**
An operator changes `public_url` at runtime (the exact scenario US-3 enables for serve_web). serve_web reflects it, but the CSP `frame-ancestors` header — which controls who may embed the gateway in an iframe, a real security control — was frozen at boot from the old value. Because FR-007 removed the restart gate, the config-honesty banner no longer tells the operator a restart is needed, so the stale CSP diverges from config silently.

**Recommended fix**
Before un-gating `public_url`: either (a) keep it restart-gated *for the CSP/frame-ancestors consumer* and compute the serve_web URL live from a separate, un-gated source; or (b) make **all** `public_url` consumers live (move the frame-ancestors derivation out of the boot block into a per-request/hot-reload read) and prove it with a test that a runtime `public_url` change updates the CSP header. The spec must enumerate every `public_url` consumer and state which are live vs. boot-frozen, not validate only serve_web.

---

### M-3 (MAJOR) — Cookie-on-WS-handshake extends the same-origin residual to the agent-control WebSocket, unstated

**Grounding**
- FR-009 adds `omnipus-session` cookie auth to the WS handshake "alongside the existing first-message `{type:"auth",token}` frame."
- The WS upgrader validates Origin: `wsCheckOrigin` allows same-origin (`Origin` host+port == request Host) plus the configured `allowedOrigin` (`pkg/gateway/websocket.go:398-432`).
- With the path approach, the preview is now **same-origin** with the SPA.

**Why it matters**
The accepted C-3 residual is scoped in the spec to "call `/api/v1/*` while that tab is open." But a malicious previewed app in the user's same-origin tab (a) passes `wsCheckOrigin` (same origin → allowed), (b) has the browser auto-attach `omnipus-session`, and (c) with the new cookie-handshake path, authenticates the WS **without** the auth-frame token it cannot read. Previously the auth-frame token was precisely the defense that stopped a same-origin script from opening an authenticated WS. The chat/agent-control WS is higher blast-radius than a single REST call — it can drive the agent, stream the conversation, and take browser control. The spec's residual statement (and ADR Risk table) never mention the WS.

**Recommended fix**
State the WS control channel explicitly as part of the accepted same-origin residual (Non-Behaviors + ADR Risk table), and confirm `wsCheckOrigin` same-origin acceptance is the deliberate boundary. Consider retaining the auth-frame as a *required* second factor on the WS even when the cookie is present (belt-and-suspenders), or documenting why it is intentionally downgraded to cookie-OR-frame. At minimum the threat statement must not understate the surface the spec itself opens.

---

### M-4 (MAJOR) — `serve_web` has no live-config accessor; "build the URL live" is unspecified

**Grounding**
- `WebServeTool` stores `gatewayPreviewBaseURL` as a **constructor argument frozen at build time** (`pkg/tools/web_serve.go:129,160,183`); the URL is built as `t.gatewayPreviewBaseURL + path` (`:290,570`) and the disabled-gate reads `t.gatewayPreviewBaseURL == ""` (`:257,418`).
- There is no `getCfg`/config-snapshot accessor on the tool today.

**Why it matters**
FR-005 requires the URL be computed live from `gateway.public_url` else `http://localhost:<gateway.port>`, and FR-006 requires gating on the live `preview_enabled`. Neither is reachable from the tool's current shape — both need live config at *call* time. The spec says "build live" and "gate on live `preview_enabled`" but never specifies the mechanism (inject a `func() *config.Config` into `NewWebServeTool`, read `Gateway.PublicURL`/`Gateway.Port`/`Gateway.PreviewEnabled` per call). Without that, an implementer either can't satisfy "live" or invents an ad-hoc global. This is the kind of dependency change that belongs in the spec's Symbols/Integration sections.

**Recommended fix**
Specify the live-config injection: `NewWebServeTool(..., getCfg func() *config.Config)` (or reuse an existing snapshot accessor), and state that both the URL build and the `preview_enabled` gate read through it per call. Add it to the Impact Assessment (it changes the tool's constructor signature and every call site / test that builds a `WebServeTool`).

---

### m-1 (MINOR) — "Exactly one TCP listener" is imprecise

`health.NewServer` owns its own `*http.Server` with `ListenAndServe` (`pkg/health/server.go:76-100`). In the gateway path it is mux-mounted (`gateway.go:1871-1872` → `RegisterOnMux(m.mux)`), so after deleting the preview listener one bind remains — but US-2 AS-1 / SC-001 / `TestNoSecondListener` assert a **global** "exactly one TCP listener," which is fragile against health-server (or future) wiring. US-2 AS-2 already greps for `previewServer` absence, which is the correct, precise assertion. Reword AS-1/SC-001 to "no separate **preview** listener binds."

### m-2 (MINOR) — CSRF is not a mitigation against the same-origin residual

ADR-044 D3 mitigation (i) and the Risk table row 2 cite "CSRF on `/api/v1/` (unchanged)" as mitigating a malicious **same-origin** previewed app. CSRF double-submit defends only **cross-origin** attackers who cannot read the cookie; the `__Host-csrf`/`csrf` cookie is `HttpOnly:false` (`csrf.go:403,407`), so a same-origin preview reads it and echoes the token, defeating CSRF entirely. The spec's own Non-Behaviors residual correctly says the preview "can read the readable CSRF cookie" — so the mitigation claim contradicts the residual. State plainly: the only effective controls for the residual are proxy-strip (FR-013, protects the dev server) and HttpOnly (no off-machine exfiltration); CSRF does not help in-browser same-origin abuse.

### m-3 (MINOR) — Agent's built-in browser sees a different app state than the user's tab

US-9 presents "the agent reviews and presents the preview via the built-in browser" as equivalent to what the user sees. The built-in browser is "a separate process with no SPA cookies" (Non-Behaviors residual). For a login-gated previewed app, the user's real tab can log in; the agent's live-panel view remains logged-out. The agent thus reviews a different state. Not a defect, but the spec should note the divergence so the agent-presentation flow isn't oversold for authenticated previews.

### m-4 (MINOR) — "neither credential → 401" is untested

Test Datasets lists `Auth | neither | 401 | S9 (neg)`, but there is no BDD scenario and no TDD-plan entry asserting it. Adding the cookie fallback to `checkBearerAuth` (which today returns 401 on "no Bearer prefix," `auth.go:154`) is exactly where a fail-open regression would land. Add an explicit test: request with neither `Authorization` nor `omnipus-session` → 401 (and the WS equivalent).

### m-5 (MINOR) — e2e cookie "injection" needs server-side hash seeding

Regression #7 / Integration Boundaries offer "log in during setup, **or inject** `omnipus-session`." Direct injection requires a raw token whose bcrypt `SessionTokenHash` is already present on a `UserConfig` (`ResolveUserFromCookie` verifies against the stored hash, `session_cookie.go:255`). Pure `storageState` cookie injection without seeding the matching hash will fail auth. Prefer/require the "log in during global-setup" path, or document the hash-seeding step.

### m-6 (MINOR) — Toggling preview off does not tear down running dev servers

US-4 / S7: `preview_enabled=false` makes `/preview/` 404 and `serve_web` refuse. It does not address dev servers already spawned (`DevServerRegistry` entries + npm processes) — they keep running (RAM, egress, bind ports) until their idle TTL, invisible to the operator who now sees 404. Specify the intended behaviour: leave them to idle-TTL (state it) or tear them down on disable.

### O-1 (OBSERVATION) — The auth migration is bundled into the preview change, enlarging blast radius

The Bearer→HttpOnly-cookie migration (US-5, FR-009/010/011, the whole SPA fetch/WS/auth/onboarding surface + Playwright storageState) is the largest and riskiest part of this spec, yet it is only *incidentally* required by the preview move (to keep the same-origin token unreadable). The Impact Assessment marks four of its rows HIGH. Consider shipping the cookie migration as its own PR/spec first (it stands alone and is independently valuable), then the preview-listener move on top — this shrinks each change's review and rollback surface and isolates C-1's fix. Not a defect; a sequencing recommendation.

### O-2 (OBSERVATION) — Symbol path imprecision

The Symbols table's `WebServeTool URL build (web_serve.go:565-570)` resolves to `pkg/tools/web_serve.go`, not the `pkg/tools/browser/` directory implied by grouping it near the browser symbols. Line numbers match; just fully-qualify the path.

---

## Structural Integrity (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | PASS (US-1…US-10 each have numbered acceptance) |
| Every acceptance scenario has ≥1 BDD scenario | PASS (S1–S19 cover all US) |
| Every BDD scenario has a `Traces to` back-reference | PASS (each S-N names its US-AS) |
| Every BDD scenario has a corresponding TDD test | PASS (S1–S19 all appear in the TDD "Traces" column; S15 via e2e test 26) |
| Every FR appears in the traceability matrix | PASS (FR-001…FR-018 all mapped) |
| Every FR maps to a test | PASS, but see C-1/m-4: the mapped tests assume the CSRF cookie is present and omit the "neither credential" and "CSRF-cookie-absent-but-session-valid" branches |
| Test datasets cover boundary/edge/error | MOSTLY — the CSRF-cookie-absent and dev-server-teardown states are missing (C-1, m-6) |
| Regression impact explicitly addressed | PASS (7 regression items enumerated); m-5 flags an infeasible sub-option |
| Success criteria measurable, no subjective language | MOSTLY — SC-001 imprecise (m-1); SC-008 depends on the infeasible FR-018 (M-1) |

Structure is sound; the defects are in **content correctness**, not form.

---

## STRIDE Threat Summary

| Component / flow | Threats identified |
|---|---|
| `/preview/<token>/` on main mux (bare, all methods) | **Information disclosure / EoP**: same-origin with SPA; readable CSRF cookie + auto-ridden session cookie → in-browser `/api/v1/*` abuse (accepted C-3; but see m-2 — CSRF is not a real mitigation). **Tampering**: token-in-path only; relies on 256-bit token + 30-min TTL. |
| SPA ↔ gateway (cookie auth + CSRF) | **Availability/DoS (self-inflicted)**: C-1 CSRF-cookie lifetime → returning-user write-lockout. **Spoofing**: cookie-OR-bearer both accepted (additive) — verify neither-credential still 401s (m-4). |
| WebSocket handshake (cookie + auth-frame) | **EoP**: M-3 — same-origin preview opens authenticated agent-control WS via cookie, bypassing the auth-frame token; Origin check (`wsCheckOrigin`) is the only remaining boundary. |
| Agent built-in browser → `/preview/` (SSRF) | **SSRF/EoP**: M-1 — allowlisting `localhost` to reach the gateway port opens every loopback port; the port-scoped control the spec promises is not implementable in the current checker. |
| Preview reverse proxy → dev server | **Info disclosure**: FR-013 strips `omnipus-session` + `Authorization` — correct; verify strip happens before *every* forward path, incl. websocket-upgrade proxying if the dev server uses WS. |
| `public_url` runtime change | **Tampering (headers)**: M-2 — CSP/frame-ancestors boot-frozen; un-gating removes the operator's restart signal. |

---

## Unasked Questions (for the author to answer)

1. **CSRF cookie lifecycle (C-1):** what re-mints the CSRF cookie for a returning session that has a valid `omnipus-session` but no CSRF cookie? If nothing does today, which of the three fixes is chosen, and where is its test?
2. **SSRF port scoping (M-1):** is port-aware allow actually required, or is host-level allow acceptable given the gateway proxies to arbitrary dev ports anyway? If required, where does the port check live, since `SSRFChecker` discards the port?
3. **`public_url` consumers (M-2):** enumerate every consumer. Which read live, which are boot-frozen? Is the CSP/frame-ancestors path being made live, or should `public_url` stay restart-gated with serve_web reading a different source?
4. **WS residual (M-3):** is authenticating the agent-control WS by cookie alone (no auth-frame) for a same-origin caller intended? Should the auth-frame remain mandatory as a second factor?
5. **serve_web live config (M-4):** what is the exact accessor injected into `WebServeTool` for the live URL build and `preview_enabled` gate?
6. **Disable semantics (m-6):** on `preview_enabled=false`, do already-running dev servers keep running to idle-TTL, or are they torn down?
7. **Onboarding CSRF echo (C-1 adjacent):** after `HandleCompleteOnboarding` issues both cookies, does the SPA immediately hold a readable CSRF cookie to echo on its first state-changing call, or is there a first-write race?

---

## Verdict

**BLOCK** — C-1 is a code-grounded, high-probability write-lockout for ordinary returning users, introduced by making CSRF load-bearing without a matching CSRF-cookie lifecycle. Resolve C-1 (with a scenario + test), then address the four MAJOR findings (M-1 infeasible SSRF requirement, M-2 stale CSP under un-gated `public_url`, M-3 unstated WS residual, M-4 missing live-config accessor). The MINOR/OBSERVATION items should be folded in during the revise.
