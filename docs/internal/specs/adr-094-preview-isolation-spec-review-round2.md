# Adversarial Review: ADR-094 — Preview isolation specification (issue #798), round 2

**Document reviewed**: `docs/internal/specs/adr-094-preview-isolation-spec.md` (Status: Draft, commit `d2f92e056`)
**Mode**: Spec
**Round**: Round 2 of 2 (final)
**Review date**: 2026-09-27
**Verdict**: BLOCK

## Executive Summary

The fix round did real work. Most of round 1's findings are closed in substance: the SPA is in scope, the result field is named, the SSRF rule is exact, the credential filter is name-scoped, host dispatch has ordering clauses, the label lifecycle and the redaction duties are specified, and the frontend sections exist. Q1–Q4 and F-1/F-2/F-3 are recorded in the Decisions Log and carried into requirements.

One decision is put into practice wrongly, and that makes this round a BLOCK. **Q4's recovery rule (FR-015) deletes the user's real session cookie and misses the exact planted-cookie path round 1 described.** The clear set includes the host-only `Path=/` form, which is the genuine `omnipus-session` and `csrf` cookie. It also leaves out trailing-slash paths (`/api/`), so the planted cookie survives. The result is an automated login loop: exactly the lockout Q4 option A was chosen to remove. The spec's own holdout H-4 expects the opposite outcome.

Eight MAJOR findings remain:

- The SPA file that forwards the tool result to the card drops the new field.
- The planted-cookie detector's position relative to the outermost CSRF middleware is unspecified.
- The gateway-Bearer match points at the wrong store and would cost a bcrypt check per proxied request.
- Three test-plan gaps: the Mode 1 control pins, the missing Mode 2 RED for #798 itself, and the SSRF negative rows.
- The Playwright wiring.
- The frontend Retry surface for Q4.

Frontend coverage is now close to backend coverage: UI states, journey, accessibility and design-system sections exist. The remaining frontend gaps are one reachability defect (MAJ-001) and one untested surface (MAJ-008).

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 8 |
| MINOR | 9 |
| OBSERVATION | 2 |
| **Total** | **20** |

---

## Round-1 findings: fixed or not (checked against the current text)

| Round-1 ID | Status in `d2f92e056` | Evidence / residual |
|---|---|---|
| CRIT-001 SPA cannot render Mode 1 | **Fixed in the spec's model; residual gap** | `isolated_url` (FR-022), `resolvePreviewHref` rule (FR-023), engine rule (FR-024), UI-states table. Residual: `src/components/chat/tools/WebServeUI.tsx` copies named fields and drops `isolated_url`, and the spec never names that file (→ MAJ-001) |
| CRIT-002 SSRF blocks the agent panel | **Fixed in substance; residual gap** | FR-021 exact class plus F-1/F-2/F-3. The round-1 negatives `<label>.localhost:<other-port>` and `<label>.example.com` are still absent from DS-6, and M-3 cannot flip (→ MAJ-006) |
| CRIT-003 dev proxy strips all cookies | **Fixed per Q1; residual** | FR-020, DS-4, S-3.x. Residual: the "gateway's own Bearer" source is the wrong store (→ MAJ-003) |
| MAJ-001 middleware order | Fixed | FR-028 plus S-2.7. Residual defence-in-depth invariant → MIN-003 |
| MAJ-002 `/api/v1` 404 vs app owns host | Fixed | FR-006, S-2.6, AF-2 |
| MAJ-003 Mode 1 controls mis-stated | **Partly fixed** | Controls are named (US-2 AC2, FR-009). But the round-1 unit pins for `isAllowedOrigin`/`wsCheckOrigin` are absent, S-2.2 claims a pre-change behaviour the code does not have, and M-1's "order 2 proves the flip" is wrong (→ MAJ-004) |
| MAJ-004 cookie-tossing lockout | **Not fixed. The operationalization reintroduces the lockout** | → CRIT-001 |
| MAJ-005 registry mis-cited, lifecycle | Fixed | Stores re-cited correctly, FR-029. Minor dev-store nuance → MIN-005 |
| MAJ-006 unfalsifiable mutation 1 | Fixed (rationale dropped, M-1 retargeted) | The retarget's proof pointer is wrong → MAJ-004 |
| MAJ-007 nav guard breaks downloads | Fixed per Q3 | Residuals → MIN-002 |
| MAJ-008 listener vs canonical port | Fixed for dispatch | Panel-side port unspecified → MIN-004 |
| MAJ-009 warmup probe vs `connect-src` | Fixed | FR-025, S-8.4, order 23 |
| MAJ-010 Host redaction, per-label limit | Fixed | FR-026/027, S-7.x |
| MAJ-011 frontend sections missing | Fixed | Sections present. Small inaccuracies → MIN-006 |
| MAJ-012 browser matrix | **Partly fixed** | Matrix split and Firefox added. The spec file is still unnamed, the `ISOLATION_SPEC_FILES`/`shards.json` edits are not stated, and no WebKit skip for Mode 1 (→ MAJ-007) |
| MAJ-013 redirect rule scoped | Fixed | FR-013, S-5.4, DS-2b, M-8 |
| MIN-001 docs | Fixed | FR-018 |
| MIN-002 FR-007/FR-009 tests | Fixed (FR-007); weak for FR-009 | → MAJ-004 |
| MIN-003 alias set | **Partly fixed** | Enumerated, but no in-prefix alias row (→ MIN-009) |
| MIN-004 helper home | Fixed | FR-019 |
| MIN-005 preflight pin | Fixed | FR-012, S-2.8 |
| MIN-006 `serve_web`, Reachability | Fixed | |
| MIN-007 DS-3 edge rows | Fixed | DS-3 rows 6–12 |
| MIN-008 regression table | Largely fixed | Names suites generically. `tests/e2e/iframe-preview-warmup.spec.ts` is not named |
| OBS-001/002/003 | Fixed / adopted | |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Q4's clear rule deletes the real session and misses trailing-slash plants, so the lockout becomes an automated login loop

- **Lens**: Security (DoS); Inconsistency (founder decision and holdout H-4); Incompleteness
- **Affected section**: FR-015; the machine-verifiable planted-cookie bullet; S-4.1; DS-5 row 7; US-2 AC7; H-4; SC-005
- **Failure scenario**:
  1. A Mode 1 page tosses `omnipus-session=x; Domain=localhost; Path=/api/`. This is round 1's MAJ-004 vector, verbatim.
  2. The next SPA request, `GET /api/v1/agents`, carries two `omnipus-session` values. Detection fires.
  3. FR-015 emits clear lines at `/api/v1/agents`, `/api/v1`, `/api` and `/`, in both the `Domain=localhost` and the host-only form.
  4. **(a)** The host-only `Path=/` clear matches the identity (name, host, path) of the *genuine* cookie written by `pkg/gateway/middleware/session_cookie.go::WriteSessionCookie` (host-only, `Path: "/"`). The real session is deleted. The same happens to the real `csrf` cookie.
  5. **(b)** No clear line targets `Path=/api/`. RFC 6265 §5.2.4 stores the attribute value as given, and a cookie's identity includes its exact path string, so `/api` and `/api/` are different cookies. The planted cookie survives.
  6. `src/lib/queryClient.ts::handleAuthError` re-checks the session, gets 401, and calls `forceLogout()`.
  7. The user logs in again. The next `/api/…` request again carries the surviving plant plus the new real cookie. Detection clears the real one again. This repeats forever.

  Q4 was decided as "delete the planted duplicate(s) … at the shadowing path … the lockout lasts one click". The spec's rule deletes the non-planted cookie and keeps the planted one. H-4 ("afterwards a single `omnipus-session` remains and traffic is normal") contradicts FR-015 as written.
- **Evidence**:
  - FR-015: "emit clear lines … at the request path, every ancestor directory, and `/` — in **both** the `Domain=localhost` form … and the host-only form".
  - DS-5 row 7: clears at `/a/b/c`, `/a/b`, `/a` and `/` only, with no trailing-slash forms.
  - `pkg/gateway/middleware/session_cookie.go` (`HttpOnly: true`, `Path: "/"`, no `Domain`).
  - `src/lib/queryClient.ts::handleAuthError` (401 → `_recheckSessionValidity` → `forceLogout`).
- **Recommendation**: Replace the clear set in FR-015, DS-5 and S-4.1 with the following.
  - For every `/`-boundary prefix `P` of the request path, including the full path: clear at both `P` and `P + "/"`.
  - `Domain=localhost` form: every such path, **including `/`**.
  - Host-only form: every such path **except `/`**. A host-only `Path=/` plant is an overwrite of the real cookie, not a duplicate, so it can never be the cause of a detected duplicate.
  - Bound: 2 forms × 2 slash variants × (depth + 1), minus 1.
  - Add DS-5 rows:
    - The `Path=/api/` toss is cleared.
    - The genuine host-only `/` cookie is **not** in the clear set (assert the absence of `Set-Cookie: omnipus-session=; Path=/` with no `Domain`).
    - An E2E version of H-4: after one retry, exactly one `omnipus-session` remains and no forced logout occurred.

---

### MAJOR Findings

#### [MAJ-001] The SPA component that forwards the tool result to the card drops `isolated_url`, and the spec does not name it

- **Lens**: Reachability; Incompleteness
- **Affected section**: Cluster placement ("SPA slice"); Symbols involved; FR-022/FR-023; TDD orders 21–22
- **Failure scenario**: frontend-lead extends `resolvePreviewHref` and `IframePreview` exactly as specified. Orders 21–22 inject props directly and go green. But the chat renders `serve_web` through `WebServeUI`, which builds `iframeResult` field by field (`path`, `url`, `expires_at`, plus `command`/`port` for dev). It also types the parsed result with its own local `WebServeResult` interface, which has no `isolated_url`. The field never reaches `IframePreview`, and every user sees the Mode 2 link. Only order 24's E2E could catch this, if it is wired (MAJ-007).
- **Evidence**:
  - `src/components/chat/tools/WebServeUI.tsx::WebServeResult` (fields `kind`, `url`, `expires_at`, `command`, `port`, `path`).
  - The `iframeResult` object literal in the same file.
  - The spec's SPA slice lists only `src/lib/preview-url.ts`, `src/components/chat/IframePreview.tsx` and "the chat tool-result card", unnamed.
- **Recommendation**:
  - Add `src/components/chat/tools/WebServeUI.tsx` (`WebServeResult` and the `iframeResult` construction) to Symbols involved and FR-022.
  - State that the `IframePreview` result prop types (`ServeWorkspaceResult` and `RunInWorkspaceResult` in `src/lib/api/sessions.ts`) gain the field under the same opt-out.
  - Add a component test at the `WebServeUI` level: a raw tool-result JSON string with `isolated_url` renders the Mode 1 link under a Chromium engine stub.

#### [MAJ-002] The planted-cookie detector's position in the middleware chain is unspecified; behind the outermost CSRF gate, it never runs on POSTs

- **Lens**: Incompleteness; Infeasibility
- **Affected section**: FR-015; FR-028 (covers only preview-Host ordering); S-4.2
- **Failure scenario**:
  1. `CSRFMiddleware` is the outermost wrap. `csrfCookieValue` reads the **first** `csrf` cookie, which is the longer-path plant.
  2. A tossed `csrf` duplicate on `POST /api/v1/…` is therefore rejected by the CSRF gate with a mismatch before any inner planted-cookie detector runs.
  3. No clear lines are sent, no `planted_cookie_cleared` code is returned, and the user sees a generic CSRF error on every write, indefinitely.

  Likewise, if detection sits after session auth, `r.Cookie` has already resolved the planted `omnipus-session`.
- **Evidence**:
  - `pkg/gateway/gateway_boot.go`: `WrapHTTPHandler(configSnapshotMiddleware)` is followed by `WrapHTTPHandler(csrfMW)`, and wraps stack outermost-last, so CSRF is outermost. The spec states this itself in the codebase table.
  - `pkg/gateway/middleware/csrf.go::csrfCookieValue` (`r.Cookie(CSRFCookieName)`, then `r.Cookie(CSRFCookieNameHTTP)`).
- **Recommendation**: Add a normative clause: "planted-cookie detection runs outside (before) `CSRFMiddleware` and before any session or credential read, on main-Host requests." Add an S-4.2 variant where the duplicate is `csrf` (not `omnipus-session`) on a POST, asserting that the typed error and the clear lines appear rather than a CSRF mismatch.

#### [MAJ-003] "Bearer matching the gateway's own secrets" points at the wrong store, and the right store makes it a bcrypt check per proxied request

- **Lens**: Security; Infeasibility; Inconsistency with AS-IS
- **Affected section**: Q1 operationalization in the machine-verifiable "credential filter" bullet ("the credential store injected at boot — `pkg/gateway/CLAUDE.md`, 'Credential boot order'"); FR-020; A-3; Assumption 3; DS-4 row 5
- **Failure scenario**:
  1. The spec says to match against "the credential store injected at boot". That store holds provider and channel secrets (ADR-004 boot order), not the gateway's REST bearer tokens.
  2. A literal implementation never matches a real gateway token, so the user's gateway Bearer is forwarded to the agent-built upstream. That is the leak Q1 kept closed.
  3. The correct source is bcrypt-hashed: `Gateway.Users[].TokenHash` via `UserConfig.VerifyToken`, plus `Gateway.CLIToken` and the legacy `OMNIPUS_BEARER_TOKEN`, all resolved by `resolveBearerIdentity`.
  4. Matching every `Authorization: Bearer …` that a previewed app sends (the app's own JWT on every fetch) then costs one or more bcrypt compares per proxied request. That makes HMR and API calls slow and is a CPU exhaustion lever for a hostile app.
- **Evidence**:
  - `pkg/gateway/auth.go::resolveBearerIdentity`, `::matchUserBearer` (loops `VerifyToken` over every account).
  - `pkg/config/config_gateway.go::UserConfig.VerifyToken` (bcrypt compare).
  - `pkg/gateway/CLAUDE.md` "Credential boot order" (the ADR-004 store, not the user tokens).
- **Recommendation**:
  - Rewrite the bullet: "delete `Authorization: Bearer <t>` when `t` is accepted by the gateway's own bearer validator (`resolveBearerIdentity` semantics: user tokens, CLI token, legacy env token)."
  - Add a cost bound: cheap format pre-filtering, where the id-tagged form indexes to one hash, and no account scan per request. Architect picks the mechanism.
  - Add a DS-4 row for a non-gateway JWT Bearer, asserting it is forwarded with no bcrypt compare (count via a validator spy).

#### [MAJ-004] The Mode 1 controls are pinned only by the E2E, S-2.2 asserts a pre-change behaviour the code does not have, and M-1's proof pointer is wrong (round-1 MAJ-003 partly unfixed)

- **Lens**: Testability & false-green risk
- **Affected section**: S-2.2 ("a regression test proves the pre-change behaviour: the same fetch DOES return data cross-origin today"); TDD order 2; CHECK M-1/M-2; SC-002; US-2 AC2 ("while the browser holds `Domain=localhost` gateway cookies")
- **Failure scenario**:
  - **(a)** `isAllowedOrigin` already refuses a `<label>.localhost` Origin today (hostname must equal the Host's, `localhost` or `127.0.0.1`). The spec's own codebase table says so. qa-lead cannot write the "fails today" RED S-2.2 demands and will either bend the test or report a non-RED.
  - **(b)** Order 2 is a server-side Host-dispatch test (a request *to* the label host). Widening `isAllowedOrigin` (M-1) does not touch that path, so "order 2's RED row proves the flip direction" is false.
  - **(c)** Round 1 asked for unit cases `isAllowedOrigin(...) == false` and `wsCheckOrigin(...) == false` for the label origin. None is in the plan. M-1 and M-2 then depend solely on order 24's Playwright run, which is the most expensive and least deterministic instrument, and whose wiring is itself open (MAJ-007).
  - **(d)** US-2 AC2 says the browser "holds `Domain=localhost` gateway cookies". The gateway never sets any. The real host-only `localhost` cookies ride label→`localhost` requests because the two hosts are same-site.
- **Evidence**: `pkg/gateway/rest.go::isAllowedOrigin`; `pkg/gateway/websocket.go::wsCheckOrigin`; spec codebase-context row for `isAllowedOrigin` ("refuses … today").
- **Recommendation**:
  - Add a unit test row (`TestMode1OriginPins`) covering `isAllowedOrigin` and `wsCheckOrigin` for `http://<label>.localhost:<port>`, both false. M-1 and M-2 must flip this row.
  - State that these are *pinning* tests of controls that already hold. Their red-before-green is the mutation run, not a pre-change failure.
  - Delete S-2.2's "DOES return data today" line.
  - Split order 2's description: server-side unreachability only.
  - Fix US-2 AC2's cookie wording.

#### [MAJ-005] The issue's own acceptance RED (US-2 AC1) and every Mode 2 browser-level block are absent from the TDD plan

- **Lens**: Testability & false-green risk; Reachability (Definition of Done)
- **Affected section**: US-2 AC1 and AC3 ("executed in CI on both paths … dev-proxy path included"); US-3 AC2/AC3; TDD plan; SC-003; Definition of done
- **Failure scenario**:
  1. No TDD row executes "script in a served `/preview/` document reads `csrf` and POSTs to `/api/v1`" on today's code. That is the #798 hole, and the RED that proves the instrument can see it.
  2. Mode 2's fetch, form and navigation blocks are asserted only as a CSP string (order 14) and server-side header simulations (order 16). S-2.1 is even traced to "US-2 AC1" although it is the Mode 1 storage scenario.
  3. The DoD ("all 24 TDD rows green") can therefore be declared with the issue's acceptance test never run in a browser, on either path.
  4. Similarly, Mode 2 HMR over `ws://` (US-3 AC2) and forms, popups and downloads (US-3 AC3) have no test.
- **Evidence**: TDD orders 1–24 (no row traces US-2 AC1/AC3 or US-3 AC2/AC3); S-2.1 "Traces to: US-2 AC1"; order 14 traces S-2.3.
- **Recommendation**: Add E2E rows in the isolation projects:
  - A RED (fails on today's code) and GREEN pair for the #798 attack: fetch POST, form POST and XHR from a `/preview/` page, static **and** dev-proxy, on Chromium, Firefox and WebKit, asserting no state change landed (a server-side marker).
  - A Mode 2 realistic-bundle render with HMR `ws://`, a form inside the prefix, a popup and a download.

  Retrace S-2.1 to US-1/US-2 AC2 correctly.

#### [MAJ-006] The SSRF negative set lacks the rows that matter: another port, two valid labels, a foreign suffix. M-3 cannot flip (round-1 CRIT-002 residual)

- **Lens**: Security; Testability
- **Affected section**: DS-6; S-6.3; CHECK M-3 ("Widen … accept any port, or multi-label `a.b.localhost` → must flip S-6.3 rows 3, 7"); SC-008
- **Failure scenario**:
  - Row 3 is portless: `extractHostPort` already fails without a port, so an any-port mutation still refuses it.
  - Row 7 is an *empty* label (`ba..localhost`) or bad hyphens, not two valid labels. So both M-3 mutations survive every DS-6 row.
  - `SC-008`'s "no second label, no other port" is untested. A build that admits `a.b.localhost:5000` or `x.localhost:6379`, which is another loopback service (the SSRF admission is network reachability), passes CHECK.
  - Row 5's verdict "out of scope" is not a boolean, so order 6 cannot assert it. Row 1 mixes two layers.
- **Evidence**: `pkg/security/ssrf.go::isAllowedGatewayOrigin` (`extractHostPort` explicit-port requirement); spec DS-6 rows 1–7; round-1 CRIT-002 recommendation (`<label>.localhost:<other-port>`, `<label>.example.com`).
- **Recommendation**:
  - Add DS-6 rows, all refused:
    - `http://foo.localhost:6379/`
    - `http://a.b.localhost:5000/`
    - `http://foo.localhost.evil.com:5000/`
    - `http://foolocalhost:5000/`
    - `http://foo.example.com:5000/`
    - `http://127.0.0.1:5000/api/v1/agents` (the ADR-073 scope for the loopback literal is kept)
  - Give row 5 an explicit boolean (refused for the label class).
  - Split row 1 into its SSRF verdict (admitted) and a separate gateway-404 row.
  - Point M-3 at the new rows.

#### [MAJ-007] The E2E wiring is still unnamed: spec file, `ISOLATION_SPEC_FILES`, shard list, WebKit skip for Mode 1 (round-1 MAJ-012 residual)

- **Lens**: Testability & false-green risk; Reachability
- **Affected section**: TDD order 24 (`TestPreviewIsolationE2E`, which is a Go-style name, not a file); Regression row "Browser test matrix" ("shard group … unchanged in shape")
- **Failure scenario**:
  - The isolation projects run only files listed in `ISOLATION_SPEC_FILES`, and the `preview-isolation` shard runs only its `specs` array. A new spec file added anywhere else runs under the default project on Chromium only, or not in the shard at all. Firefox Mode 1, WebKit Mode 2, `retries: 0` and the whole S-2.2 browser proof silently never execute.
  - The three isolation projects share one file list. Mode 1 tests will run on `isolation-webkit` unless skipped, and Linux WebKit may resolve `*.localhost` via the system resolver (Inferred), producing a green that says nothing about Safari.
  - The shard's single gateway (port 6083) must also be configured with a `http://localhost:<port>` canonical origin for Mode 1 to mint at all.
- **Evidence**: `playwright.config.ts::ISOLATION_SPEC_FILES` (three files today), the `isolation-*` projects `testMatch: [...ISOLATION_SPEC_GLOBS]`, `retries: 0`; `tests/e2e/shards.json` group `preview-isolation` (`specs` list, `port: 6083`).
- **Recommendation**:
  - Name the file (e.g. `tests/e2e/preview-isolation-webserve.spec.ts`).
  - Require it to be appended to `ISOLATION_SPEC_FILES` and to the shard's `specs`.
  - Require `test.skip(browserName === 'webkit')` on the Mode 1 cases, with a comment pointing at the H-2 manual holdout.
  - Require the shard's gateway to boot with a loopback `public_url` so Mode 1 mints.
  - Add a guard assertion that the file is in both lists (a trivial vitest or node check), so dropping it fails CI.

#### [MAJ-008] The Q4 "please retry" surface in the SPA has no named component, no test, and relies on an unstated 401 re-check path

- **Lens**: UI states & journey gaps; Testability
- **Affected section**: FR-015 ("the SPA surfaces it through its standard error rendering with a Retry action"); S-4.2 ("the SPA renders it with a Retry action"); Design system ("reuses the existing toast"); UI screens and states (no row)
- **Failure scenario**:
  - No generic "re-issue the failed mutation" mechanism is named. TanStack mutations are spread across screens, and the toast's `action` is a callback someone must bind to the specific failed call. frontend-lead either builds a global interceptor ad hoc or shows a Retry that does nothing.
  - For planted duplicates on GETs, FR-015 returns a credential-less 401. The spec does not say that recovery depends on `queryClient.ts::handleAuthError`'s session re-check succeeding on the next, now-clean request. A change to that handler, or CRIT-001's current clear set, turns it into a forced logout.
  - No TDD row covers the SPA side of `planted_cookie_cleared` at all.
- **Evidence**: `src/store/ui.ts::Toast` (`action?` exists, which is good, but it needs a bound callback); `src/lib/queryClient.ts::handleAuthError`; TDD orders 1–24 (none SPA-side for Q4).
- **Recommendation**:
  - Name the mechanism: for example, in `src/lib/api/http.ts`, an `ApiError` with `code === 'planted_cookie_cleared'` raises a toast whose action re-invokes the same request once.
  - Add a UI-states row ("planted cookie cleared → toast with Retry").
  - Add a vitest: the code produces a toast, and its Retry re-issues exactly once.
  - State the GET dependency on `handleAuthError`'s re-check, and add a test that a 401 carrying clear lines, followed by a clean re-check, does **not** call `forceLogout`.

---

### MINOR Findings

#### [MIN-001] Cross-reference errors throughout the renumbered spec

- **Lens**: Ambiguity
- **Affected section**: several
- **Failure scenario**: A builder or reviewer follows a pointer to the wrong requirement or file:
  - "The web_serve tool text MUST teach … (FR-017)" points to the MIME requirement; tool text is FR-016.
  - "The FR-010 threat context" in the credential-filter bullets is the nav guard, not the app-reads-credentials threat.
  - The UI-states row "Malformed `isolated_url` (FR-024)" should be FR-023.
  - FR-002 traces "US-1 AC2" and FR-003 traces "US-1 AC3", while the matrix says US-4.
  - FR-010 traces S-2.2, a Mode 1 scenario.
  - FR-010 cites `src/lib/library.ts::libraryDownloadUrl`; the file is `src/lib/api/library.ts`. The codebase table has it right.
  - The disposition table cites "orders 19–24" (CRIT-001), "orders 17/18" (MAJ-010) and "order 22" (MAJ-009); the actual rows are 20–24, 18/19 and 23.
- **Recommendation**: One pass fixing each pointer listed.

#### [MIN-002] The FR-010 exemption list omits the upload download route, and the safety argument omits its load-bearing reason

- **Lens**: Security; Incompleteness
- **Affected section**: FR-010; order 16; SC-007
- **Failure scenario**:
  - `/api/v1/uploads/{session_id}/{filename}` is a GET download route that the contract documents as "the … download URL". SPA comments still describe chat uploads as `/api/v1/uploads/…`. If any rendered attachment `href` uses it, "open in new tab" and possibly downloads break under the guard. Whether the SPA still navigates there is Inferred (medium).
  - The exemption is safe only because these handlers serve active content through `serveLibraryPath`, which attaches off-allow-list types and applies the ADR-067 isolation policy to inline content. If that ever regresses, a preview top-level-navigating to an agent-written `.html` under `/api/v1/media/workspace/…` runs same-origin with the SPA. That is #798 again, through the exemption.
- **Evidence**: `pkg/gateway/rest.go` (`/api/v1/uploads/` → `HandleServeUpload`, `/api/v1/media/…` → `HandleMedia`/`HandleMediaByRef`, `withOptionalAuth`); `pkg/gateway/rest_uploads.go` (ADR-067 FR-008b/FR-015 comment on `serveLibraryPath`); `src/lib/url-safe.ts` comment.
- **Recommendation**:
  - Decide on `/api/v1/uploads/…` (exempt it, or state it is never a navigation target).
  - Cite `serveLibraryPath`'s policy as the exemption's precondition.
  - Add an order-16 row: each exempted route serving a `.html` returns attachment or the ADR-067 policy, never a bare inline document.

#### [MIN-003] The CSRF skip must be exactly the dispatch predicate

- **Lens**: Security (defence in depth)
- **Affected section**: FR-028; S-2.7; S-2.11
- **Failure scenario**: The skip is implemented as "Host ends with `.localhost`" while dispatch requires grammar, the canonical port and a live label. A fall-through request (S-2.11: wrong port, portless) then reaches the main mux with no CSRF check. The real host-only cookie does not ride such Hosts, so exploitability is low (Inferred). The invariant still costs nothing to state.
- **Recommendation**: FR-028 should add: "CSRF is skipped iff the request is dispatched to the preview-host mux; a fall-through Host is CSRF-gated." Add an S-2.11 assertion: a state-changing request with a wrong-port label Host and no token gets the CSRF refusal.

#### [MIN-004] The agent panel's SSRF port on port-mapped installs is unspecified

- **Lens**: Ambiguity; Reachability
- **Affected section**: FR-021(d) ("the configured gateway port"); DS-3 row 8
- **Failure scenario**: The browser clone is wired with the **listener** port (`loop_wire.go`: `CloneWithGatewayOrigin("localhost", cfg.Gateway.Port)`), while Mode 1 is minted with the **canonical** port (8080 vs 5000). The panel refuses `isolated_url` on every port-mapped install. The spec states the analogous portless limitation but not this one. Implementers may "fix" it by admitting the canonical port too, which widens the class.
- **Recommendation**:
  - State that (d) is the listener port as wired.
  - State that the port-mapped panel limitation is accepted, and that the tool text tells the agent to use `url` when the panel refuses. Note the same holds for Mode 2 today (Inferred).
  - Add a DS-6 row.

#### [MIN-005] Label entropy is not normative; the dev-store lifecycle differs from the static store

- **Lens**: Ambiguity
- **Affected section**: FR-005; FR-029; SSRF layering text ("~2^128")
- **Failure scenario**: FR-005 says only "fresh entropy". A 6-character label is grammar-valid and would pass every test, yet the SSRF layering relies on unguessability. FR-029's "renews in place … rotates at `maxTokenLifetime`" describes `ServedSubdirs` only. `DevServerRegistry.Register` has no renew-in-place and no `maxTokenLifetime`: a dev re-serve mints a new token, so a new label and a new origin.
- **Recommendation**: Pin at least 128 bits (for example 26 lower-case base32 characters) with a DS-1 row. Split FR-029 per store.

#### [MIN-006] The UI and accessibility text misdescribes the card, and the copy-link target is unspecified

- **Lens**: UI states & journey; Accessibility
- **Affected section**: Accessibility and keyboard ("State changes … surface through the existing toast"); UI screens and states
- **Failure scenario**:
  - Warmup state changes are announced through the card's own `aria-live="polite"` region and inline message, not toasts (toasts are used for copy-link). A builder following the spec adds warmup toasts: a double announcement.
  - The card's "Copy link" action is not in the state table. A Chrome user copies the Mode 1 link and pastes it into Safari: "can't find server".
- **Evidence**: `src/components/chat/IframePreview.tsx` (`aria-live="polite"`, the `addToast` copy-link calls, `window.open(href…)`).
- **Recommendation**: Correct the accessibility bullet. Add a row: "Copy link copies the rendered link".

#### [MIN-007] FR-015's scope and promises need tightening

- **Lens**: Ambiguity; Security
- **Affected section**: FR-015; DS-5 row 4; US-2 AC7 ("the lockout lasts one click")
- **Failure scenario**:
  - "Any request" includes preview-Host requests, where no gateway credential is read. An app's own `csrf` cookie plus a toss produces clears and a JSON error on the app's POST.
  - `__Host-csrf` cannot be set with `Domain` or a non-`/` path, and cannot exist over plain HTTP. DS-5 row 4 is unreachable, and the `Domain=localhost` clear form for it is rejected by browsers.
  - A hostile preview left open re-plants on every tick, so the "one click" promise holds only once its tab is closed.
- **Recommendation**:
  - Scope detection to main-Host requests.
  - Drop or re-describe row 4.
  - Say in the message or in `docs/previews.md` that a preview which keeps planting must be closed.

#### [MIN-008] The docs must name the three reserved cookie names

- **Lens**: Incompleteness
- **Affected section**: FR-018
- **Failure scenario**: The founder's Q1 names `csrf` as stripped in both directions. Apps whose framework uses a cookie literally named `csrf` lose it through the proxy (the request is filtered and the response `Set-Cookie` is neutralized). The user sees an app-side CSRF failure with no documented cause.
- **Recommendation**: FR-018 adds: "the preview cannot use cookies named `omnipus-session`, `csrf` or `__Host-csrf`".

#### [MIN-009] DS-2 has no in-prefix alias row (round-1 MIN-003 residual)

- **Lens**: Testability
- **Affected section**: DS-2
- **Failure scenario**: Only out-of-prefix alias rows exist. An implementation that emits only canonical-origin `Location`s, and 502s `http://127.0.0.1:<port>/preview/{agent}/{token}/next`, passes.
- **Recommendation**: Add in-prefix rows for `127.0.0.1:<port>` and `localhost:<port>` → emit.

---

### Observations

#### [OBS-001] State that `navigator.userAgentData` is secure-context-only

- **Lens**: Ambiguity
- **Affected section**: FR-024
- **Suggestion**: It is undefined on non-secure origins. That is fine here: loopback is a secure context, and anything else fails safe to Mode 2. Say so in FR-024, so nobody later "fixes" a Mode 2 result on a LAN-IP SPA as a detection bug.

#### [OBS-002] The AF-1…AF-6 flags depend on a concurrent ADR edit

- **Lens**: Inconsistency
- **Affected section**: Flags for the architect; Assumption 5
- **Suggestion**: Assumption 5 already routes a normative overturn back through the spec. When the architect's corrections land, re-verify that AF-5's wording matches FR-021 (the deployment-agnostic admission plus the registry gate). This review cited the spec's own text, not the ADR, because the ADR file may be mid-edit.

---

## Structural Integrity

### Variant A: Spec mode (plan-spec output)

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | `Draft` |
| ADR linked (or explicitly stated not needed) | PASS | ADR-094, founder decisions, round-1 review |
| Contract changes stated first, citing `contracts/` | PASS | "Contract-first status"; opt-out stated. `planted_cookie_cleared` fits `ErrorResponse.code` (free string) — verified |
| API and data section | PASS | Machine-verifiable constraints; Bearer source wrong (MAJ-003) |
| UI screens and states (loading/empty/error/partial) | PASS (partial) | Missing the Q4 retry state and copy-link (MAJ-008, MIN-006) |
| User journey section | PASS | |
| Accessibility and keyboard section | PASS (partial) | Toast vs aria-live inaccuracy (MIN-006) |
| Design-system components, catalogue-first | PASS | |
| Security and user promises section (when touched) | PASS | |
| BDD acceptance scenarios, oracle from spec | PASS (partial) | S-2.2 pre-change claim false (MAJ-004); S-4.1 oracle wrong (CRIT-001) |
| Traceability table: requirement -> scenario -> test | PASS (partial) | US-2 AC1/AC3, US-3 AC2/AC3 untested (MAJ-005); pointer errors (MIN-001) |
| Reachability section (tool policy / screen wiring) | PASS (partial) | Present; `WebServeUI.tsx` gap (MAJ-001) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| RED for the issue itself | Mode 2 attack POST on today's code, static and dev proxy, 3 engines | US-2 AC1, AC3 |
| Unit pins | `isAllowedOrigin` / `wsCheckOrigin` label-origin false | S-2.2, M-1, M-2 |
| SSRF negatives | Other port, two valid labels, foreign suffix, loopback literal | S-6.3, M-3 |
| Frontend | `WebServeUI` field forwarding; Q4 toast and Retry; 401 re-check without logout | S-8.1, S-4.2 |
| Middleware order | Planted `csrf` on POST behind the outermost CSRF gate | S-4.2 |
| E2E wiring | File in `ISOLATION_SPEC_FILES` and the shard; WebKit skip for Mode 1 | order 24 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| DS-5 | Trailing-slash plant path; genuine host-only `/` not cleared | CRIT-001 |
| DS-6 | `:6379`, `a.b.localhost`, `foo.localhost.evil.com`, `foolocalhost` | MAJ-006 |
| DS-4 | Non-gateway JWT Bearer with no bcrypt compare | MAJ-003 |
| DS-1 | Minimum entropy length | MIN-005 |
| DS-2 | In-prefix alias rows | MIN-009 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Planted-cookie recovery | ok | ok | ok | ok | risk | ok | CRIT-001 login loop; MAJ-002 ordering |
| Dev-proxy credential filter | ok | ok | ok | risk | risk | ok | MAJ-003: wrong store leaks the gateway Bearer; bcrypt per request |
| Mode 1 host dispatch | ok | ok | ok | ok | ok | ok | FR-026/027/028 cover it; MIN-003 invariant |
| Mode 1 → main origin | ok | ok | ok | ok | ok | risk | Controls hold today; the pins are E2E-only (MAJ-004) |
| Agent panel SSRF | ok | ok | ok | ok | ok | risk | Widening mutations go undetected (MAJ-006) |
| Nav-guard exemptions | ok | ok | ok | ok | ok | risk | Safety rests on an uncited `serveLibraryPath` policy (MIN-002) |

**Legend**: risk = identified threat not mitigated in the document; ok = adequately addressed or not applicable

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | Yes (existing, unchanged) | `pkg/tools/web_serve.go::ToolNameWebServe = "serve_web"` |
| User-facing: named screen/component renders it? | **Not as specified** | `src/components/chat/tools/WebServeUI.tsx` drops `isolated_url` (MAJ-001) |
| Agent panel can open it? | Yes on non-port-mapped loopback | FR-021; port-mapped refused (MIN-004) |
| Test plan describes execution, not just authorship? | Partly | Order 24 is unwired (MAJ-007); the #798 RED is absent (MAJ-005) |

---

## Unasked Questions

1. Which bearer-validator semantics does the credential filter use, and what bounds its per-request cost? (MAJ-003; architect decides)
2. Which SPA module owns the `planted_cookie_cleared` → Retry binding? (MAJ-008)
3. Is `/api/v1/uploads/{session_id}/{filename}` still a navigation target anywhere in the SPA? (MIN-002)
4. What is the exact E2E spec filename, and how does the shard gateway get a loopback `public_url`? (MAJ-007)

---

## Questions for the founder

None. Every open point in this round puts an existing decision (Q1–Q4, F-1/F-2/F-3) into practice or is an architect/implementation choice. None needs a new founder decision. CRIT-001 is escalated below because it is the blocking finding left after the fixed rounds, not because a new design choice is open: its fix follows directly from Q4's own wording ("at the shadowing path").

---

## Verdict Rationale

**BLOCK**, driven by CRIT-001. FR-015 as written deletes the genuine host-only session and CSRF cookies and never clears a trailing-slash plant. So Q4's "one click" recovery becomes a forced-logout loop on round 1's own example vector, contradicting the founder's wording and the spec's H-4. The fix is mechanical: change the clear-set definition and add three dataset rows.

The MAJOR set is narrower than round 1's and mostly about proof:

- the missing #798 RED and the Mode 2 browser tests (MAJ-005);
- the E2E-only Mode 1 pins with a false pre-change claim (MAJ-004);
- the SSRF negatives that cannot catch widening (MAJ-006);
- the unwired E2E (MAJ-007).

Three are about wiring: `WebServeUI` (MAJ-001), detector placement (MAJ-002) and the SPA Retry surface (MAJ-008). One is a security correctness issue in the Q1 filter (MAJ-003). None re-opens a founder decision.

### Escalation to the founder

| Finding ID | Why it's still open | Founder decision needed |
|---|---|---|
| CRIT-001 | Round 2 is the last grill. FR-015's clear set, as fixed in round 1, logs the user out and misses `Path=/api/` plants: the opposite of Q4 option A. No further grill round will check the correction | Approve fixing it in the round-2 fix pass exactly as recommended (clear every `/`-boundary prefix in both slash forms; `Domain=localhost` at all paths including `/`; host-only at all paths **except** `/`), with the three DS-5 rows and the H-4 E2E. Or accept landing with an explicit security-lead verification of the corrected FR-015 in place of a third grill. Recommended: fix in the round-2 pass, then security-lead verifies |

### Recommended Next Actions

- [ ] Spec author: rewrite FR-015 / DS-5 / S-4.1 clear set (CRIT-001); add detector placement clause (MAJ-002)
- [ ] Spec author + architect: correct the Bearer-match source and bound its cost (MAJ-003)
- [ ] Spec author: add `WebServeUI.tsx` to scope and tests (MAJ-001); Q4 SPA Retry mechanism, state row and tests (MAJ-008)
- [ ] Spec author: add the #798 RED and the Mode 2 E2E rows (MAJ-005); unit pins and S-2.2/order-2/M-1 corrections (MAJ-004); DS-6 rows and M-3 retarget (MAJ-006); E2E file, lists, WebKit skip and shard origin (MAJ-007)
- [ ] Spec author: MIN-001…MIN-009 pointer and dataset fixes
- [ ] security-lead: verify the corrected FR-015 and FR-020 Bearer rule before team-lead plans

### Next step in the process

```
Verdict: BLOCK

Review written to: docs/internal/specs/adr-094-preview-isolation-spec-review-round2.md

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews
the founder on "Questions for the founder", then the spec author fixes
round-2 findings. Any CRITICAL finding still open after that fix is
listed under "Escalation to the founder" above for the founder to
decide — do not run a third grill round. Once resolved, team-lead plans
the implementation (RED / GREEN / CHECK, the 8-reviewer gate).
```
