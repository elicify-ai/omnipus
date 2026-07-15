# Adversarial Review: Preview on Main Listener

**Spec reviewed**: docs/internal/specs/preview-on-main-listener-spec.md
**Review date**: 2026-07-15
**Verdict**: BLOCK

## Executive Summary

The spec's structural scaffolding (BDD, traceability, TDD plan) is complete, and the
core idea (collapse the second TCP listener onto the main mux) is sound. But three
critical findings block implementation: the entire CSRF rationale is built on a false
premise about the current handler AND is internally contradictory; the headline
sessionStorage/e2e migration is infeasible because Playwright `storageState` cannot
persist sessionStorage; and the agent-browser SSRF behaviour the spec depends on is
asserted, never verified, and likely false for the prioritised public-URL path. A
further six major findings show the deletion/impact set is under-enumerated
(AboutResponse contract + 3 frontend consumers missed; hot-reload mechanism for a
boot-registered route unspecified; `gatewayPreviewBaseURL` is boot-frozen so live
`public_url` won't take effect; multi-tab auth regression unaddressed).

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 6 |
| MINOR | 3 |
| OBSERVATION | 2 |
| **Total** | **14** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] CSRF story is factually wrong and self-contradictory; both prescribed fixes break

- **Lens**: Incorrectness / Inconsistency / Infeasibility
- **Affected section**: Symbols table (`CSRFMiddleware | extends | Add /preview/ to defaultExemptPaths`); Explicit Non-Behaviors ("MUST NOT add CSRF enforcement… GET-only token-gated; CSRF is irrelevant"); FR-010; BDD S10
- **Description**: The spec calls `/preview/` "GET-only" in four places. The actual handler is not. `HandlePreview` (`pkg/gateway/rest_preview.go:43`) handles `OPTIONS` (preflight, lines 44-47) and, on the dev-server branch, hands the request to `proxyDevRequest` (line 313) which is an `httputil.ReverseProxy` that forwards **every** HTTP method — including POST/PUT/PATCH/DELETE — to the loopback dev server (Vite/Next HMR and API routes routinely POST). The static branch restricts to GET/HEAD (line 117), but the dev branch does not.
  CSRF is applied **globally** to the main mux, not per-route: `pkg/gateway/gateway.go:2255 csrfMW := middleware.CSRFMiddleware(...)`. Per `pkg/gateway/middleware/csrf.go`, safe methods (GET/HEAD/OPTIONS) pass through, but POST/PUT/PATCH/DELETE are enforced (require matching `__Host-csrf` cookie + `X-Csrf-Token` header). So once `/preview/` lives on the main mux, dev-server POST routes through the proxy get **403 CSRF**.
  The spec's two instructions are both broken:
  - FR-010 / S10 ("no special handling needed") → dev proxy POST routes return 403; Vite/Next dev servers break.
  - Symbols table ("add `/preview/` to `defaultExemptPaths`") → (a) the exempt check is an **exact-path** match (`csrf.go: if _, ok := exempt[r.URL.Path]`), so it would never match a tokenized URL `/preview/<agent>/<token>/<file>` anyway; (b) the documented invariant on `defaultExemptPaths` requires every POST/PUT/PATCH/DELETE there to call `IssueCSRFCookie` — `/preview/` is not a cookie issuer, so adding it violates the invariant.
- **Impact**: The P1 dev-mode use case (`serve_web(command="npm run dev")`, which is the first user story's example command) silently breaks: the iframe loads, then dev-server HMR/API POSTs fail with CSRF 403, and the operator gets no guidance from the spec on why.
- **Recommendation**: Ground the CSRF decision in the real handler. Either (a) explicitly restrict `HandlePreview`/`proxyDevRequest` to safe methods (GET/HEAD/OPTIONS) and reject others with 405 — then FR-010's "no special handling" is actually correct and the Symbols-table "extends" row must be **deleted**; or (b) keep method-pass-through and specify a real CSRF exemption mechanism that works for tokenized paths (e.g. prefix matching in the CSRF middleware, or registering the dev proxy outside the CSRF-wrapped chain) — and reconcile that with the `defaultExemptPaths` invariant. Pick one, state it, and add a BDD+test for "POST to `/preview/<token>/` through the dev proxy is not CSRF-rejected". Remove the contradiction between the Symbols table and FR-010.

---

#### [CRIT-002] "Migrate Playwright storageState to sessionStorage" is infeasible — storageState cannot persist sessionStorage

- **Lens**: Infeasibility
- **Affected section**: Integration Boundaries ("Playwright e2e storageState writes auth token to localStorage; Must migrate to sessionStorage"); Regression Tests #5; US-5
- **Description**: Playwright's `context.storageState()` / `storageState` file persists **cookies and localStorage only** — it does **not** capture or restore `sessionStorage`. The current e2e harness knows this: `tests/e2e/auth.spec.ts:108-115` deliberately mirrors the token out of sessionStorage *into localStorage* (`localStorage.setItem('omnipus_auth_token', token)`) immediately before `context.storageState({ path: AUTH_FILE })`, and `global-setup.ts` follows the same pattern. That mirror exists *precisely* because storageState can't carry sessionStorage. If the localStorage fallback is removed (US-5) and the storageState file is "migrated to sessionStorage", every authenticated e2e spec (calendar, settings, handoff, skills, recap-continuity, whatsapp-qr, contract-counters, … — all 9+ specs that read `omnipus_auth_token` from the storageState file) will start every test unauthenticated.
- **Impact**: The entire authenticated e2e suite goes red on merge. The spec frames this as a trivial migration ("must be migrated to sessionStorage"); it is not a migration, it is a redesign of e2e auth.
- **Recommendation**: Replace Regression #5 with a concrete, viable e2e-auth strategy. Options: (a) keep a Bearer-token injection via `context.addInitScript`/`route` interception that seeds sessionStorage before each page load; (b) a `globalSetup`/`beforeAll` that logs in via REST and uses `page.addInitScript` to populate sessionStorage for every context; (c) a per-test `fixture` that sets sessionStorage on navigation. State which, and add a gate that fails CI if any spec reads the token from localStorage. Until this is specified, US-5 cannot ship without breaking CI (Constraint #7).

---

#### [CRIT-003] Agent-browser SSRF pass/fail for the public preview URL is asserted, not verified — and likely false for the prioritised path

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: US-1 (P0, headline), US-3 AS-3 ("a public URL passes (public host) and a localhost URL passes (the dev port is allowlisted)"); Behavioral Contract last bullet; Integration Boundaries (Agent headless browser row)
- **Description**: The spec's #1 priority story is: `public_url` set → `serve_web` returns `https://pod.example.com/preview/…` → the agent's headless browser navigates to it. The spec asserts this "passes" SSRF. But SSRF checkers (this codebase's included — `pkg/security/ssrf.go` and the egress proxy in `pkg/sandbox/egress_proxy.go`) exist to **block** non-allowlisted hosts, and a gateway's own public origin is not automatically allowlisted. Today the preview URL is `localhost:<preview-port>` and passes because loopback is allowlisted; after this change, with `public_url` set, the URL becomes a **public** host. Nothing in the spec verifies that the agent browser's SSRF layer (or the egress proxy, if browser traffic routes through it) permits the gateway's own public origin, nor specifies how to allowlist it. The stale phrase "the dev port is allowlisted" refers to a port that no longer exists post-change.
- **Impact**: If the egress/browser SSRF blocks the gateway's public origin (the default posture for public hosts), the P0 happy path (US-1 AS-1) fails in exactly the deployment the spec prioritises — the agent cannot navigate to the preview it just minted. The failure would surface only at UAT/production, not in any specified test.
- **Recommendation**: Before implementation, verify how the agent's browser navigation reaches the network (does chromedp route through `EgressProxy`? through `SSRFChecker` directly? neither?). Then add an FR: "The SSRF allowlist MUST include the gateway's own public origin (derived from `gateway.public_url`) and loopback on `gateway.port`, so the agent browser can navigate to preview URLs on either." Add a BDD error-path scenario for "SSRF blocks a preview URL whose host is neither loopback nor the configured public origin" and a test.

---

### MAJOR Findings

#### [MAJ-001] AboutResponse contract + AboutInfo interface + 3 frontend consumers are out of scope (Constraint #8 violation)

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: Symbols Involved table (omits them); FR-003
- **Description**: The preview port/origin surface crosses the gateway/SPA boundary via `GET /api/v1/about`, not just config. `contracts/components/schemas/AboutResponse.yaml:13-14` lists `preview_port` and `preview_listener_enabled` as **required**, and `preview_origin` as optional; mirrored in `pkg/gateway/inboundschemas/AboutResponse.yaml`; emitted by `pkg/gateway/rest_settings.go:614-624`; typed in `src/lib/api.ts:2447-2461` (`AboutInfo`) with a Zod schema at `src/lib/api.ts:2490-2491` and generated `src/lib/api/generated/schemas.ts:1480-1482`. Three frontend consumers depend on these fields beyond the one the spec lists:
  - `src/components/chat/IframePreview.tsx:420-427` reads `aboutInfo.preview_port` + `preview_origin` → `buildIframeURL`
  - `src/components/chat/markdown-shared.tsx:27-73` uses `resolveEffectivePreview` + `rewriteLegacyURL`, both of which take `preview_port`/`preview_origin`
  - `src/lib/preview-url.ts` exports `rewriteLegacyURL` and `resolveEffectivePreview` (spec mentions only `buildIframeURL`)
  Removing the config keys leaves the About response advertising a port that no longer exists, and `resolveEffectivePreview` will keep returning a stale port. Per CLAUDE.md Constraint #8, removing wire fields requires the 5-step contract change (schema → spec → `scripts/gen-contracts.sh` → commit generated diff → consume generated types) plus `make verify-contracts`.
- **Impact**: Contract drift (`make verify-contracts` fails); SPA constructs iframe URLs against a phantom preview port; markdown link rewrites (`rewriteLegacyURL`) still swap to the old preview port.
- **Recommendation**: Add to the Symbols table: `AboutResponse.yaml` (both copies), `rest_settings.go` About handler (remove `preview_port`/`preview_listener_enabled`/`preview_origin`), `AboutInfo` + Zod in `api.ts`, `IframePreview.tsx`, `markdown-shared.tsx`, and all three functions in `preview-url.ts`. Add an FR for the contract change and a regression test that `GET /api/v1/about` no longer advertises a separate preview port.

---

#### [MAJ-002] Hot-reload mechanism for a boot-registered main-mux route is unspecified; `serve_web`'s disabled-check now keys off a dead condition

- **Lens**: Incompleteness / Infeasibility
- **Affected section**: US-4, FR-005/FR-006, BDD S4/S6/S7; Behavioral Contract ("toggled… takes effect immediately (hot-reload)")
- **Description**: Go's `http.ServeMux` cannot unregister a pattern at runtime. `/preview/` will be registered on the main mux **once at boot**, so `preview_enabled=false` at runtime can only 404 if `HandlePreview` reads the live config from request context (the `configSnapshotMiddleware` provides one) and short-circuits. The spec never states this mechanism. Separately, `serve_web`'s current disabled-guard keys off `gatewayPreviewBaseURL == ""` (`pkg/tools/web_serve.go:257,418`), returning the "preview disabled" error. After this change `gatewayPreviewBaseURL` is **always** non-empty (public_url or localhost), so that guard becomes dead and `serve_web` will happily mint URLs even when `preview_enabled=false` — silently contradicting FR-005/US-4 AS-2/S9. The spec says serve_web errors when disabled but doesn't say where the new check lives.
- **Impact**: Toggling Preview off in the UI either does nothing for `/preview/` (if the handler isn't wired to live config) or `serve_web` keeps returning working URLs while the UI claims preview is off — a visible inconsistency the operator cannot trust.
- **Recommendation**: Add an FR: "`HandlePreview` MUST consult `preview_enabled` from the request-scoped config snapshot and return 404 when false." Add another: "`serve_web` MUST consult `preview_enabled` (not `gatewayPreviewBaseURL`) and return the 'preview disabled' error when false." Specify that the main-mux registration is unconditional at boot and the toggle is enforced at request time. BDD S6/S7 should assert the route is registered but returns 404, not that it was unregistered.

---

#### [MAJ-003] `gatewayPreviewBaseURL` is boot-frozen in `WebServeTool`; live `public_url` changes won't affect `serve_web` URLs

- **Lens**: Incorrectness
- **Affected section**: US-3, FR-004; Behavioral Contract; Symbols table (`WebServeTool.gatewayPreviewBaseURL | modifies`)
- **Description**: `gatewayPreviewBaseURL` is a plain struct field set **once** in `NewWebServeTool` (`pkg/tools/web_serve.go:126-129,160-183`) at boot, and `serve_web` concatenates it into every result URL (`url := t.gatewayPreviewBaseURL + path`, line 290). The spec's Behavioral Contract implies the URL reflects the current `gateway.public_url`, and US-3/FR-004 are written as if `public_url` is read live. It is not. Compounding this, `GatewayPublicURL` remains in `RestartGatedKeys` (`rest_pending_restart.go:63`), so an operator who changes `public_url` at runtime gets a "restart required" banner — and even after restart the new value only reaches `serve_web` because the tool is reconstructed. The spec never reconciles this.
- **Impact**: An operator who updates `gateway.public_url` (e.g. moving behind a new domain) and does not restart gets stale URLs from `serve_web` that point at the old origin — the agent browser then fails to navigate, with no spec-level guidance.
- **Recommendation**: Either (a) state explicitly that `serve_web` URLs are boot-frozen and `public_url` changes require restart (match `GatewayPublicURL` being restart-gated), and add a BDD/assertion; or (b) specify that `serve_web` reads `public_url` from the live config snapshot at call time (re-resolving on each Execute), and add the corresponding FR + test. Pick one.

---

#### [MAJ-004] sessionStorage-only token logs users out of every new tab; no re-auth mechanism specified

- **Lens**: Incorrectness / Incompleteness (operability)
- **Affected section**: US-5 (Independent Test: "open a new tab to the same origin, confirm the new tab cannot read the token"); FR-008; BDD S8
- **Description**: `sessionStorage` is scoped per-tab and is **not** shared with new tabs (it does survive same-tab reloads). Omnipus is a multi-surface productivity app (Workspaces, Agents, Settings, Channels) where opening a second tab is normal. US-5 frames the resulting forced re-login as the *desired* outcome ("confirm the new tab cannot read the token"). The spec provides no silent re-auth / refresh-token / HttpOnly-cookie mechanism to restore a session in a new tab without re-entering credentials. The security rationale (D3: close the same-origin localStorage read from a preview's JS) is legitimate, but the spec presents only the upside and none of the UX/operability cost or its mitigation.
- **Impact**: Every "open in new tab" workflow silently logs the user out. For the operator's own daily use this is a regression from the current localStorage behaviour, with no remedy in the spec.
- **Recommendation**: Acknowledge the multi-tab regression explicitly in the spec and specify a mitigation: e.g. an HttpOnly session cookie + a `/api/v1/auth/refresh`-style endpoint the SPA calls on cold start to re-seed sessionStorage, or document the forced-relogin as accepted with the operator's sign-off. Add a holdout/edge scenario for "open Omnipus in a second tab" covering whichever behaviour is chosen.

---

#### [MAJ-005] Deletion/impact set is under-enumerated: boot normalisation, warn helper, asyncapi, Termux default, and two test files

- **Lens**: Incompleteness
- **Affected section**: Symbols Involved; US-2; Regression Tests; SC-005
- **Description**: Several live references to the deleted keys/code are not in the spec:
  - `cfg.Gateway.ValidateAndApplyPreviewDefaults()` (`gateway.go:~1880`) mutates the very config fields being deleted; not listed.
  - `shouldWarnPreviewOrigin` (`gateway.go:2908-2913`) and its WARN block (`gateway.go:1903-1908`) become dead; not in the Symbols table, and SC-005's grep (`previewMux|previewServer|SetupPreviewServer`) would not catch it.
  - `contracts/asyncapi.yaml` references preview fields (grep hit) — a second contract surface beyond `AboutResponse.yaml`.
  - Termux/Android: CLAUDE.md records that `preview_listener_enabled` defaults to **false** on Android/Termux because a second port can't bind. With the second listener gone, that rationale evaporates — does `preview_enabled` now default true on Termux? The spec says "default ON" universally without reconciling the Termux history.
  - Two affected tests named in the spec's own Impact Assessment — `preview_listener_hot_flip_test.go` (tests the hot-flip of the deleted `preview_listener_enabled`) and `preview_origin_warn_test.go` (tests the deleted `shouldWarnPreviewOrigin`) — are not in the Regression/deletion list.
- **Impact**: Stale/dead code ships (failed `grep`-based SCs notwithstanding); contract drift on the asyncapi side; ambiguous Termux default.
- **Recommendation**: Add all of the above to the Symbols table and Regression list. Expand SC-005's grep to include `shouldWarnPreviewOrigin|ValidateAndApplyPreviewDefaults|PreviewListenerEnabled`. State the Termux default explicitly. Run `grep -rn 'preview_port\|preview_host\|preview_origin\|PreviewPort\|PreviewHost\|PreviewOrigin\|PreviewListenerEnabled' pkg/ contracts/ src/` and put every non-test hit in the Symbols table.

---

#### [MAJ-006] Main-mux auth posture for `/preview/` is unspecified (token-only must be made explicit)

- **Lens**: Ambiguity / Insecurity
- **Affected section**: Behavioral Contract; Explicit Non-Behaviors; BDD S3; Symbols table
- **Description**: Every `/api/v1/*` route on the main mux is individually wrapped with `api.withAuth(...)` (`gateway.go:2152-2165`); `/preview/` is currently registered bare on the **preview** mux with no `RequireSessionCookieOrBearer` (`rest_preview.go:15-17`, "TOKEN-ONLY… the path token IS the credential"). The spec says token-only is preserved but never states that the main-mux registration must be **bare** (not wrapped in `withAuth`), nor how it interacts with `RequireSessionCookieOrBearer` / `RequireMatchingOriginOnStateChanging` if those wrap the mux at a higher level. The agent's headless browser navigates with only the token URL — no session cookie, no CSRF cookie — so any ambient-credential requirement on `/preview/` breaks the agent navigation path (compounds CRIT-001/CRIT-003).
- **Impact**: If `/preview/` accidentally inherits a session-cookie or origin-checking middleware, the agent browser gets 401/403 on the preview URL; the spec gives no guidance to prevent this.
- **Recommendation**: Add an Explicit Non-Behavior and an FR: "`/preview/` MUST be registered on the main mux WITHOUT `withAuth`, `RequireSessionCookieOrBearer`, or `RequireMatchingOriginOnStateChanging`; the path token is the sole credential (FR-023)." Add a BDD negative scenario: "GET `/preview/<token>/` with no session cookie and no Bearer header succeeds (token-only)."

---

### MINOR Findings

#### [MIN-001] SC-001 does not measure what it claims

- **Lens**: Infeasibility
- **Affected section**: SC-001 ("`ss -ltnp | grep <gateway.port>` shows exactly one listener; no second port is bound")
- **Description**: Grepping for the gateway port will always match exactly the line for that port; it cannot prove a *second* port is absent. The criterion is structurally unable to fail.
- **Recommendation**: Replace with `ss -ltnp` and assert exactly one `LISTEN` line total (or grep for the specific deleted preview port, e.g. `:6061`/`:5001`, and assert zero matches).

---

#### [MIN-002] `GatewayPublicURL` stays restart-gated — undocumented interaction with US-3

- **Lens**: Inconsistency
- **Affected section**: US-6 AS-1; US-3; FR-004
- **Description**: US-6 removes the preview-port keys from `RestartGatedKeys` but `GatewayPublicURL` correctly stays (a listener/origin change needs restart). The spec doesn't connect this to US-3: because `serve_web` URLs are built from `public_url` (MAJ-003), changing `public_url` keeps requiring restart even though *preview* is now "always on". An implementer could mis-read US-6 as "preview is fully hot-reloadable including its URL origin."
- **Recommendation**: Add a note to US-3/US-6: "the preview *surface* is hot-reloadable (FR-006); the preview *origin* (`gateway.public_url`) remains restart-gated, so origin changes still require restart."

---

#### [MIN-003] No test for same-tab reload preserving the session

- **Lens**: Incompleteness
- **Affected section**: US-5 / BDD S8; Test Datasets (Auth storage row)
- **Description**: S8 only asserts `localStorage` is null. It does not verify the positive contract that the token survives a same-tab reload (which sessionStorage does preserve) — the behaviour users actually rely on. The dataset covers only "after login → sessionStorage has token".
- **Recommendation**: Add a BDD/dataset row: "Given a logged-in SPA tab, When the tab is reloaded, Then the session is still authenticated (token read from sessionStorage)."

---

### Observations

#### [OBS-001] Holdout scenario 5 does not cover the multi-tab regression

- **Lens**: Inoperability
- **Affected section**: Holdout Evaluation Scenarios #5
- **Suggestion**: Add a holdout: "Open the Omnipus SPA in a second tab → confirm whether the user is still authenticated or is asked to re-login" — pinning down the accepted behaviour for MAJ-004 before UAT.

---

#### [OBS-002] CSP / same-origin implication of moving `/preview/` onto the SPA origin is unstated

- **Lens**: Insecurity
- **Affected section**: Behavioral Contract; US-5 rationale (D3)
- **Suggestion**: Today the preview is a *different* origin, so the SPA's CSP/cookies are isolated from preview content. Moving `/preview/` to the main origin makes the (agent-authored, less-trusted) preview same-origin with the SPA and with the `omnipus_auth_username` localStorage value. The D3 localStorage-token rationale is only one half of the same-origin exposure; consider documenting the CSP `frame-ancestors`/`default-src` consequence and whether `omnipus_auth_username` (still in localStorage per `auth.ts`) needs the same treatment.

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | |
| Every acceptance scenario has BDD scenarios | FAIL | US-5 AS-1 (sessionStorage has token), US-3 AS-3 (SSRF pass/fail), and US-6 AS-2 (`isRestartRequired` returns false) have no BDD scenario. |
| Every BDD scenario has `Traces to:` reference | PASS | |
| Every BDD scenario has a test in TDD plan | PASS | S1–S12 map to tests 1–14. |
| Every FR appears in traceability matrix | PASS | |
| Every BDD scenario in traceability matrix | FAIL | S11 (unknown token → 404) has no matrix row / no owning FR. |
| Test datasets cover boundaries/edges/errors | FAIL | No dataset for CSRF-on-POST (CRIT-001), SSRF public-vs-loopback (CRIT-003), or sessionStorage tab isolation/reload (MAJ-004/MIN-003). |
| Regression impact addressed | FAIL | Under-enumerated (MAJ-005): `ValidateAndApplyPreviewDefaults`, `shouldWarnPreviewOrigin`, asyncapi.yaml, Termux default, `preview_listener_hot_flip_test.go`, `preview_origin_warn_test.go`. |
| Success criteria are measurable | FAIL | SC-001's grep cannot fail (MIN-001); SC-005's grep misses dead helpers (MAJ-005). |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| CSRF on dev-proxy non-GET | No test that POST/PUT through `/preview/<token>/` to a dev server is not CSRF-rejected (or is intentionally 405'd) | CRIT-001 / S3 / new |
| SSRF allowlisting of own origin | No test that the agent browser reaches the preview on the gateway's public origin AND on loopback:gateway-port | CRIT-003 / US-3 AS-3 |
| Hot-reload route disable | No test that a boot-registered `/preview/` returns 404 while `preview_enabled=false`, then 200 after toggle — *without* unregistering | MAJ-002 / S6/S7 |
| sessionStorage lifecycle | No test for same-tab reload survival or new-tab isolation | MAJ-004 / MIN-003 / S8 |
| E2E auth redesign | No test/gate proving the e2e suite authenticates without localStorage | CRIT-002 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| URL construction | `public_url` with explicit `:443` / `:80` port; trailing slash; userinfo `user@host` | Add rows asserting normalisation |
| CSRF × preview | POST `/preview/<token>/` with and without CSRF cookie/header | Add rows per CRIT-001 decision |
| SSRF | preview URL on public origin vs loopback vs foreign host | Add rows per CRIT-003 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `/preview/` on main mux (token-only) | ok | risk | ok | risk | risk | ok | Token is sole credential — must stay bare (no session cookie) MAJ-006; no rate-limit/brute-force spec on token guessing (D); preview content is agent-authored same-origin JS → CSP/I exposure (OBS-002) |
| Dev reverse proxy (`proxyDevRequest`) | ok | risk | ok | risk | ok | ok | Forwards all methods to loopback dev server; CSRF gap CRIT-001; upstream error messages already sanitised (good) |
| `serve_web` URL minter | ok | ok | ok | risk | ok | ok | Leaks loopback/public origin to the LLM (acceptable); boot-frozen origin MAJ-003 |
| Settings → Preview toggle | ok | ok | ok | ok | ok | ok | Hot-reload; adequate |
| Auth token storage (sessionStorage) | ok | ok | ok | ok | ok | ok | Reduces exposure vs localStorage; but no audit log for re-auth events (R) and multi-tab regression MAJ-004 |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Does the agent's headless browser (chromedp) route its navigation through `EgressProxy`/`SSRFChecker`, and is the gateway's own public origin allowlisted there? (CRIT-003)
2. Should `HandlePreview`/`proxyDevRequest` be restricted to safe methods (405 on others), or must POST/PUT to the dev proxy be CSRF-exempt — and if so, how, given the exact-path exempt check and the `IssueCSRFCookie` invariant? (CRIT-001)
3. How do authenticated Playwright specs obtain their token once localStorage is gone, given storageState cannot carry sessionStorage? (CRIT-002)
4. What re-auth path restores a session in a newly-opened Omnipus tab, or is forced re-login accepted? (MAJ-004)
5. Is `/preview/` confirmed registered bare on the main mux (no `withAuth`, no `RequireSessionCookieOrBearer`, no origin check)? (MAJ-006)
6. Does `preview_enabled` default to `true` on Android/Termux now that the second-port rationale is gone? (MAJ-005)
7. Are `serve_web` URLs intended to reflect a live `public_url`, or is boot-frozen + restart-required the accepted behaviour? (MAJ-003)

---

## Verdict Rationale

The spec is structurally well-formed and the architectural direction (single listener) is correct, but it is not safe to implement as written. The CSRF analysis (CRIT-001) is built on an incorrect reading of the current `HandlePreview` — which is not GET-only on its dev path — and prescribes two mutually contradictory fixes that are each individually broken against a CSRF layer that is applied globally to the main mux. The sessionStorage migration (CRIT-002) is infeasible because Playwright `storageState` cannot transport sessionStorage; the spec's own e2e harness mirrors the token into localStorage for exactly this reason, and every authenticated spec depends on it. The headline public-URL happy path (CRIT-003) depends on SSRF behaviour the spec asserts rather than verifies, and the default posture of SSRF checkers makes the asserted "public host passes" unlikely. The six major findings then show the deletion surface is materially under-scoped: the AboutResponse wire contract and three frontend consumers are invisible to the spec (MAJ-001, a Constraint #8 violation), the hot-reload and disabled-detection mechanisms are unspecified (MAJ-002), and the boot-frozen URL origin (MAJ-003), multi-tab auth regression (MAJ-004), incomplete deletion set (MAJ-005), and unstated auth posture (MAJ-006) each carry real ship-time risk.

### Recommended Next Actions

- [ ] CRIT-001: Decide safe-methods-restriction vs. real CSRF exemption for the dev proxy; remove the Symbols-table/FR-010 contradiction; add POST-to-preview BDD + test.
- [ ] CRIT-002: Replace Regression #5 with a viable e2e-auth design (addInitScript/REST-login); add a CI gate that fails on any localStorage token read.
- [ ] CRIT-003: Verify the agent-browser → SSRF path; add an FR allowlisting the gateway's own public origin + loopback:gateway-port; add SSRF BDD + test.
- [ ] MAJ-001: Bring AboutResponse (both copies), `AboutInfo`/Zod, `rest_settings.go` About handler, `IframePreview.tsx`, `markdown-shared.tsx`, and all of `preview-url.ts` into scope; run the contract 5-step + `make verify-contracts`.
- [ ] MAJ-002: Specify request-time `preview_enabled` enforcement in `HandlePreview` and a `preview_enabled`-based (not empty-baseURL) guard in `serve_web`.
- [ ] MAJ-003: Resolve whether `serve_web` reads `public_url` live or boot-frozen; state it and add a test.
- [ ] MAJ-004: Acknowledge the multi-tab regression and specify re-auth or accepted-forced-relogin.
- [ ] MAJ-005: Enumerate the full deletion set; fix SC-001 (MIN-001) and SC-005 greps.
- [ ] MAJ-006: Add the explicit "bare registration, token-only" FR and a no-cookie BDD.
