# Grill-Spec Review (Round 2): Preview on Main Listener

**Spec reviewed**: `docs/internal/specs/preview-on-main-listener-spec.md` (revised draft, 2026-07-15)
**ADR context**: `docs/internal/architecture/ADR-044-preview-on-main-listener.md`
**Prior review**: `docs/internal/specs/preview-on-main-listener-spec-review.md` (Round 1, BLOCK — this file does not overwrite it)
**Reviewer mode**: plan-spec (full structural + 8-lens adversarial)
**Date**: 2026-07-15

---

## Executive Summary

The revision genuinely closes most of the Round-1 findings (CSRF prefix-exemption, live toggle,
listener deletion, contract change). But moving `/preview/` onto the SPA's own origin collides with
two things the spec never inspected: the SPA's **existing same-origin iframe guard** (which strips
`allow-same-origin` and so **breaks a previewed app's own login** — the feature's headline
requirement) and the **fresh-install onboarding path** (which issues **no** session cookie, so
removing the JS token locks a fresh install out). A third defect is that the spec's central security
claim — "the cookie migration makes the same-origin path approach safe" — is false: the CSRF control
it leans on is read-and-echo-able by any same-origin previewed app.

**Findings**: 3 CRITICAL, 5 MAJOR, 4 MINOR, 2 OBSERVATION.

**Verdict**: **BLOCK.**

---

## Findings Table

| ID | Sev | Lens | Section | Finding | Fix |
|----|-----|------|---------|---------|-----|
| C-1 | CRITICAL | Incorrectness / Incompleteness | US-1, US-7, FR-3(ADR) | The SPA's `IframePreview` drops `allow-same-origin` whenever the iframe origin equals the SPA origin (`src/components/chat/IframePreview.tsx:134-148`; its own comment: "losing allow-same-origin … breaks localStorage/cookie access inside the iframe"). Today previews live on a *different* origin (2nd port) so the guard never fires and the framed app keeps cookie/storage access. This spec forces the preview onto the SPA origin → the guard now fires for **every** preview → the framed app can no longer set/read cookies or localStorage → **its own login (OAuth/session) cannot persist**. That is exactly the requirement US-1 ("the user MUST be able to log in to the previewed app") and US-7 exist to satisfy. The CSRF exemption (US-7) lets the POST *reach* the dev server, but the sandbox still prevents the app from keeping the resulting session. `IframePreview.tsx` is listed in the symbol table only for "Drop preview_port reads"; the guard is unmentioned. | Resolve the contradiction explicitly. Either (a) accept that same-origin previews cannot run stateful apps and rewrite US-1/US-7/FR-3 to drop the "login works" claim; or (b) re-introduce a distinct origin for previews (Option B / second port) — which the ADR rejected. There is no same-origin design that both isolates the SPA and lets the framed app hold its own session. Pick one and state it. |
| C-2 | CRITICAL | Incompleteness | US-5, FR-009, FR-010 | The spec asserts the session cookie is "already issued at login" and treats issuance as done. But the **fresh-install path** is `POST /api/v1/onboarding/complete` (`src/lib/api.ts:2278`, `src/routes/onboarding.tsx:555` → `setToken(resp.token,…)`), and `HandleCompleteOnboarding` (`pkg/gateway/rest_onboarding.go`) issues a bearer token but **never calls `WriteSessionCookie`/`IssueSessionCookie`** (only `/auth/login` and register-admin do — `rest_auth.go:476`). Once FR-010 removes the JS-stored token, a fresh install finishes onboarding with **neither a stored token nor a session cookie** → the first authenticated request 401s → the install is bricked until a manual `/auth/login`. (Note the misleading comment `api.ts:5` that *claims* the cookie is issued on `/onboarding/complete`; it is not.) | Add session-cookie issuance to `HandleCompleteOnboarding` (and audit password-change / any other token-minting handler). Add an FR + BDD scenario: "fresh install → onboarding → first `/api/v1/*` authenticates via cookie, no re-login." |
| C-3 | CRITICAL | Insecurity (STRIDE: EoP / Info-Disclosure) | US-5, US-7, FR-011 | The spec's justification (US-5 "Why this priority": *"the security mitigation that makes the same-origin path approach safe"*; *"CSRF is now load-bearing because auth is ambient"*) is false for the threat it names. On the direct-navigation preview path (US-1: "the user opens the same URL") the previewed page is a **full same-origin document**. Its JS can read the CSRF double-submit cookie — `__Host-csrf`/`csrf` is `HttpOnly:false, Path:/` (`pkg/gateway/middleware/csrf.go:404,419`) — and echo it in `X-Csrf-Token`, while the browser auto-attaches `omnipus-session` (same-origin, `SameSite=Strict` permits same-origin). Result: **full authenticated `/api/v1/*` access from a malicious previewed app.** CSRF, the cited control, provides zero protection against a same-origin caller. `FR-012`'s proxy-strip only hides the session from the *server-side* dev server; it does nothing about *browser-side* JS. The ADR accepts this residual (Risk row 2), but the spec omits it from Explicit Non-Behaviors and actively overstates safety. | Correct the security narrative: state that on a same-origin path deployment a previewed app can drive the authenticated API in-browser, that CSRF does **not** mitigate this, and add it to Explicit Non-Behaviors as an accepted residual. Do not claim the design is "safe". Consider O-1. |
| M-1 | MAJOR | Inconsistency / Incompleteness | US-8, FR-014 | The contract deletion set omits `preview_origin`. `AboutResponse.yaml` still defines `preview_origin` (lines 61-67) and `resolveEffectivePreview` reads `aboutInfo.preview_origin` (`src/lib/preview-url.ts:262-274`), and `rewriteLegacyURL` takes `previewPort`. FR-014/US-8 only mention dropping `preview_port`/`preview_listener_enabled`, but SC-006's grep gate forbids `preview_origin` too → the spec contradicts itself and leaves a dangling field + dead SPA code. | Add `preview_origin` to the AboutResponse drop-set in FR-014/US-8; specify the rework of `resolveEffectivePreview`/`rewriteLegacyURL`/`buildIframeURL`, not just "drop preview_port reads". |
| M-2 | MAJOR | Insecurity / Infeasibility | FR-015, US-3 AS-4 | FR-015 mandates the agent browser's SSRF checker "allowlist" the gateway/dev origin for the localhost case, but gives no mechanism and ignores a real widening: `pkg/security/ssrf.go` blocks all loopback by default and its host check is **port-blind** (the ADR itself notes ssrf.go strips the port). Allowlisting `localhost`/`127.0.0.1` therefore opens **every** local port to the agent's real browser (other gateways, databases, local admin UIs) — not just the dev-server port. No config key, no scoping, and no named test are specified (FR-015's "Test Name(s)" is a parenthetical). | Specify exactly what is allowlisted and how it is scoped (ideally the specific registered dev port, if the checker can be made port-aware; otherwise document the all-local-ports widening as accepted). Add a named SSRF unit test to the TDD table. |
| M-3 | MAJOR | Incompleteness / Infeasibility | FR-009, US-5 | WS cookie auth is underspecified and its current mechanism is mischaracterized. The spec says "drop token-in-URL"; the SPA actually authenticates with a first-message `{"type":"auth","token":"…"}` **frame** (`pkg/gateway/websocket.go:653` + `src/lib/ws.ts`), validated against the generated `WsFrame` Zod/AsyncAPI schema. Reusing the cookie requires reading it at the **HTTP upgrade** and making the auth frame *optional* — a handshake-protocol change that touches the AsyncAPI contract (Constraint #8) and `resolveBearerIdentity` (which has no cookie path). None of this is in scope. | Rewrite FR-009's WS clause: read the cookie on the upgrade request, make the auth frame optional/legacy, and enumerate the AsyncAPI contract impact. Add it to the US-8 contract work. |
| M-4 | MAJOR | Inconsistency | US-3 AS-3, FR-005, FR-013 | US-3 AS-3 promises `public_url` changes take effect for `serve_web` "live … no restart", but `gateway.public_url` (`config.GatewayPublicURL`) remains in `RestartGatedKeys` (`pkg/gateway/rest_pending_restart.go`) and is NOT in the spec's deletion set → `set_config`/the Settings UI will report "restart required" for `public_url`, contradicting US-3. Separately, for `serve_web` to read `public_url` live it needs a live-config accessor wired into `WebServeTool` (today it holds a boot-frozen `gatewayPreviewBaseURL` string, `pkg/tools/web_serve.go:129,257,570`); the spec asserts "live compute" without specifying that plumbing. | Decide whether `public_url` is restart-gated. If `serve_web` reads it live, either remove it from `RestartGatedKeys` or carve out the honesty signal; and specify the live-config accessor passed to `WebServeTool`/`WireTier13Deps`. |
| M-5 | MAJOR | Inoperability | US-5, FR-010 | The SPA Bearer→cookie switch is a big-bang cutover with a hidden coupling and no rollback story. `CSRFMiddleware` currently **skips** any request carrying `Authorization: Bearer` (`csrf.go:328-331`), so the SPA never echoes CSRF today. The instant the SPA drops the bearer header (FR-010), **every** state-changing call must echo CSRF or it 403s — and there are ≥3 flip sites (`src/store/auth.ts:23`, `src/lib/api.ts:609`, `:2591`, plus the WS path). Miss one, or land the token removal before the CSRF-echo wiring, and all writes break. The spec flags this HIGH-risk in its impact table but provides no feature flag, no incremental path, and no rollback. | Add a sequencing/rollback plan: land CSRF-echo on all state-changing calls first (verified while still Bearer), then flip storage, behind a revertable change. Enumerate every token-read/echo site as an explicit checklist. |
| m-1 | MINOR | Inconsistency | Key grounding, Ambiguity #3 | ADR-044 D3 specifies `SameSite=Lax`; the implemented cookie is `SameSite=Strict` (`session_cookie.go:150`). The spec relies on the "already-built" cookie without stating the value. `Strict` blocks the session cookie on cross-site top-level navigation (e.g. following an external link) → first load is unauthenticated. Ambiguity Warning #3 dismisses SameSite wholesale; that is only true for the token-in-path preview, not the SPA session UX. | State the actual value (`Strict`), reconcile with the ADR, and note the external-link entry consequence. |
| m-2 | MINOR | Ambiguity | Symbol table | `serve_web` lives in `pkg/tools/web_serve.go`, not `pkg/sysagent/tools/`; the spec cites bare filenames + line numbers without package paths, inviting edits to the wrong file. | Qualify file references with package paths. |
| m-3 | MINOR | Structural | TDD plan, FR-015 | FR-015 has no named test row in the TDD table (only a parenthetical), violating the plan-spec rule that every FR maps to a named test. | Add `TestSSRFAllowsPreviewURL` (or similar) as a TDD row. |
| m-4 | MINOR | Overcomplexity | US-7 AS-3, FR-011 | The `/preview/` exemption from `RequireMatchingOriginOnStateChanging` may be unnecessary: a previewed app's POST is same-origin, so its `Origin` header already matches and the check passes. The exemption is likely dead configuration. | Verify whether the origin exemption is needed; drop it if the same-origin POST already passes. |

---

## Structural Integrity Results (plan-spec mode)

| Check | Result |
|-------|--------|
| Every user story has ≥1 acceptance scenario | PASS (US-1…US-8) |
| Every acceptance scenario has ≥1 BDD scenario | PASS (minor: US-2 AS-4 only loosely via S15) |
| Every BDD scenario has `Traces to:` | PASS (S1–S15) |
| Every BDD scenario has a TDD test | PASS (S1–S15 → tests 1–22) |
| Every FR in the traceability matrix | PASS (FR-001…FR-015) |
| Every FR maps to a **named** test | **FAIL** — FR-015 has only a parenthetical (m-3) |
| Test datasets cover boundary/edge/error | PASS |
| Regression impact addressed | PASS (Regression Tests §1–6) |
| Success criteria measurable / no subjective language | PASS |
| Internal consistency of the deletion set | **FAIL** — `preview_origin` missed (M-1); `public_url` restart-gating unresolved (M-4) |

---

## Test Coverage Assessment

- **Missing negative/security tests**: no test exercises the C-1 iframe-sandbox behaviour, the C-3
  same-origin API-abuse path, or the C-2 fresh-install-onboarding cookie. These are the highest-risk
  paths and are untested.
- **SSRF**: no named test for FR-015 (m-3); the localhost widening (M-2) is unverified.
- **WS**: `TestWSAuthAcceptsSessionCookie` is listed but the handshake-vs-frame protocol change
  (M-3) is not reflected in the test's description or the AsyncAPI contract tests.
- **E2E storageState**: Regression §6 migrates to cookie `storageState` — good — but there is no
  scenario covering fresh-install onboarding (C-2), which is where the cookie is never set.

---

## STRIDE Threat Summary

| Component / flow | Threats identified |
|------------------|--------------------|
| Same-origin previewed app (direct-nav) → `/api/v1/*` | **EoP / Info-disclosure** (C-3): reads JS-readable CSRF cookie + rides ambient session; CSRF ineffective |
| Same-origin previewed app (iframe) | Sandboxed (mitigated) — but at the cost of breaking the app's own session (C-1) |
| Agent headless browser → localhost preview | **SSRF widening** (M-2): port-blind allowlist exposes all local services |
| SPA session cookie | Token exfiltration mitigated by HttpOnly; `SameSite=Strict` vs ADR's `Lax` (m-1) |
| Preview reverse proxy → dev server | Session-strip covers server-side only; does not address browser-side JS (C-3) |
| Fresh-install onboarding | **DoS-of-self** (C-2): no cookie issued → locked out after token removal |

---

## Unasked Questions

1. When the preview shares the SPA origin and `IframePreview` drops `allow-same-origin`, how is a
   previewed app with its own login expected to work at all? (C-1)
2. On a fresh install, which handler sets the session cookie before the SPA's first authenticated
   request? (C-2)
3. Given the CSRF cookie is same-origin-readable, what actually stops a malicious previewed app from
   driving the authenticated API in-browser — and does the spec claim it does? (C-3)
4. Is `gateway.public_url` restart-gated or live? Both are asserted. (M-4)
5. Exactly which host/port does the SSRF checker allowlist, and does that expose non-preview local
   services? (M-2)
6. What is the atomic cutover + rollback plan for Bearer→cookie + CSRF-echo across every SPA call
   site? (M-5)

---

## Observations

- **O-1**: Partial hardening for C-3 — scope the CSRF cookie to `Path=/api` (requires dropping the
  `__Host-` prefix, which already has an HTTP fallback) so `/preview/` JS cannot read it. This does
  not close the same-origin hole (the session cookie still auto-rides) but removes the trivial
  double-submit bypass.
- **O-2**: The iframe-isolation-vs-app-session dilemma (C-1) is the canonical reason the industry
  uses a distinct per-preview origin (Option B). The ADR flagged this as a one-way door; C-1 is the
  concrete manifestation. If a hosted/multi-tenant deployment ever appears, this must be revisited.

---

## Verdict

**BLOCK** — 3 CRITICAL findings (C-1 breaks the feature's headline requirement; C-2 bricks fresh
installs; C-3 makes a false security claim). Address these plus the 5 MAJOR findings, then re-grill.
