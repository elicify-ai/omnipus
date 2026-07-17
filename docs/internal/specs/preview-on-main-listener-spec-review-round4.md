# Adversarial Review (Round 4): Preview on Main Listener

**Spec reviewed**: `docs/internal/specs/preview-on-main-listener-spec.md` (v4 — post 3× grill + operator interviews)
**Review date**: 2026-07-15
**Reviewer mode**: plan-spec (BDD + FR-ids + Traceability Matrix + SC-ids all present)
**Prior rounds**: r1 (`-spec-review.md`), r2 (`-round2.md`), r3 (`-round3.md`) — all BLOCK
**Verdict**: **BLOCK**

## Executive Summary

The v4 revision resolves the product-level contradictions from r1–r3 in its narrative and
Functional Requirements, and the core design (collapse the second listener onto the main mux;
move auth to the already-built HttpOnly cookie) is coherent. But the r3 fixes were applied to
the prose/FR layer and **not propagated to the BDD scenarios, the TDD plan, the Symbols table,
or the Impact table** — so the spec now contradicts itself on the single most security-relevant
decision it makes (whether `gateway.public_url` stays restart-gated), and a TDD-first
implementer will build the exact behaviour r3 M-2 was written to prevent. Three further major
gaps survive: the reverse proxy sanitises neither upstream `Set-Cookie` nor the readable CSRF
cookie (shared-origin cookie fixation, unaddressed by the "accepted residual" which only covers
reads); the fresh-install onboarding-cookie fix (FR-011) is underspecified in a way that
re-opens the r2 C-2 lockout; and the CSRF re-mint fix (FR-019) assumes an ordering the spec
never requires.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 4 |
| MINOR | 3 |
| OBSERVATION | 4 |
| **Total** | **12** |

Verified against `hotfix/v0.1.1`: `RestartGatedKeys` currently contains `config.GatewayPublicURL`
(`rest_pending_restart.go:63`); `IssueSessionCookie` requires the user to pre-exist in
`gateway.users` and writes no cookie on failure (`session_cookie.go:175-236`); `proxyDevRequest`
deletes only `Authorization` (`rest_preview.go:340`) and its `ModifyResponse` strips CSP/XFO but
not `Set-Cookie` (`:345-348`); the SSRF checker strips the port before every decision
(`ssrf.go:311-337`); `HandleCompleteOnboarding` creates+appends the admin user then issues only
the CSRF cookie (`rest_onboarding.go:341,381`).

---

## Findings

### CRITICAL

#### [CRIT-001] `gateway.public_url` restart-gating is asserted four different ways; the TDD plan enshrines the r3 M-2 bug

- **Lens**: Inconsistency / Incorrectness / Infeasibility
- **Affected**: FR-007; US-6 AS-1; r3 M-2 (lines 49–54) **vs** Symbols table row (line 128); Impact table row (line 145); BDD S18 (line 419); TDD Test 9 `TestRestartGatedKeysCleaned` (line 447); Test Datasets (implied)
- **Description**: The spec's r3 resolution (M-2, FR-007, US-3, US-6) is explicit and correct: `gateway.public_url` **MUST REMAIN restart-gated** because it feeds boot-frozen CORS allow-origin, WS/SSE/BrowserWS `CheckOrigin`, and CSP `frame-ancestors` (`CanonicalGatewayOrigin`). r3 M-2 even states "the round-1 'make serve_web live for public_url' idea is dropped." But four other locations were never updated off the r2 text and say the **opposite**:
  - Symbols table (line 128): "Remove all preview keys **and** `gateway.public_url` (made live for serve_web, r2 M-4)".
  - Impact table (line 145): "...key deletions + `public_url` un-gated".
  - S18 (line 419): "`RestartGatedKeys` has no preview key **and no `gateway.public_url`**".
  - Test 9 (line 447): "no preview key / **no public_url gated**; isRestartRequired false".

  This is not cosmetic. Test 9 is the executable contract; a TDD-first implementer writes a test asserting `public_url ∉ RestartGatedKeys`, deletes `config.GatewayPublicURL` from the list (it is there today, `rest_pending_restart.go:63`), the test passes, and FR-007 is silently violated. The operator is then told `set_config gateway.public_url` needs no restart while CORS/`CheckOrigin`/CSP `frame-ancestors` stay stale until the next reboot — a security-fence desync (the precise defect r3 M-2 exists to prevent). S18 also contradicts its **own** user story: US-6 AS-1 says RestartGatedKeys "MAY still contain `gateway.public_url`," while S18 says it has "no `gateway.public_url`."
- **Failure scenario**: Implementer follows Test 9 → removes `public_url` from `RestartGatedKeys` → operator changes `public_url` at runtime, is told no restart is needed → cross-origin API calls, WebSocket handshakes, and iframe `frame-ancestors` continue enforcing the stale origin; `serve_web` links and the origin fences diverge.
- **Recommendation**: Make one statement everywhere: `gateway.public_url` **remains** in `RestartGatedKeys`. Rewrite S18 to "`RestartGatedKeys` has no *preview* key **but retains `gateway.public_url`**"; rewrite Test 9's description to "no preview key gated; `public_url` still gated; `isRestartRequired("gateway.preview_enabled")` false"; delete "and `gateway.public_url`" from Symbols row 128 and its "(made live…, r2 M-4)" note; delete "+ `public_url` un-gated" from Impact row 145; harmonise US-6 AS-1 to "MUST still contain (intentional)". Add a positive assertion to Test 9 that `public_url` **is** present.

### MAJOR

#### [MAJ-001] S3 (and its Test Dataset) require a runtime-live `public_url` that FR-005/US-3 explicitly forbid

- **Lens**: Inconsistency
- **Affected**: BDD S3 (lines 373–374); Test Datasets row "`public_url` A→B runtime → second call uses B" (line 475); TDD Test 5 `TestServeWebOriginMatchesCanonical` (line 443) vs US-3 (lines 190–196) / FR-005 (line 507)
- **Description**: Same stale-r2 residue as CRIT-001, on the serve_web side. S3: "**Given** `public_url` changed `https://a`→`https://b` at runtime **When** `serve_web` runs again **Then** `url` starts `https://b/preview/`" — i.e. serve_web reflects a *runtime* `public_url` change. FR-005/US-3 say the opposite: because `public_url` is restart-gated, "the origin is stable within a process… it need not 'go live' independently." Test 5 is mapped to S3 but its description tests canonical-origin *match*, not a runtime change — so the test disagrees with the scenario it traces. An implementer reading S3 + the dataset builds a live `public_url` read (desyncing serve_web's URL from the boot-frozen origin fences, the very thing US-3 warns against).
- **Failure scenario**: Implementer honours S3 → serve_web reads `public_url` live → the emitted preview URL uses a `public_url` the CORS/CSP/WS fences don't recognise → browser blocks the preview or WS.
- **Recommendation**: Replace S3 with an edge scenario that matches FR-005: e.g. "**Given** `preview_enabled` toggled false→true at runtime **When** `serve_web` runs **Then** it reflects the live `preview_enabled` (URL vs error) **And** the host still equals the boot canonical origin." Delete the "A→B runtime" dataset row or re-label it as "requires restart (documented)". Fix Test 5's mapping/description to match.

#### [MAJ-002] The reverse proxy sanitises neither upstream `Set-Cookie` nor the readable CSRF cookie → shared-origin cookie fixation and CSRF-token leak

- **Lens**: Insecurity (Tampering / Information Disclosure) / Incompleteness
- **Affected**: FR-013; symbol note `proxyDevRequest` (line 110); Explicit Non-Behaviors "Accepted residual" (lines 338–344); `rest_preview.go:340,345-348`
- **Description**: FR-013 strips exactly two things going **to** the dev server: the `omnipus-session` cookie and `Authorization`. Two same-origin exposures remain that the "accepted residual" never covers (it only reasons about the dev app **reading**/riding the session, never **writing**):
  1. **Request direction — CSRF token leak.** The browser sends the whole `Cookie` header (`omnipus-session=…; csrf=…; …`) on every `/preview/` request. Stripping only `omnipus-session` still forwards the readable CSRF cookie (`csrf`/`__Host-csrf`, `HttpOnly:false`, `Path:/`) — and any other origin cookies — to an arbitrary previewed dev app.
  2. **Response direction — cookie fixation (worse).** `proxyDevRequest`'s `ModifyResponse` (`rest_preview.go:345-348`) strips CSP/XFO but does nothing to upstream `Set-Cookie`. Because the preview is served on the **same origin** as the SPA, a previewed dev app can return `Set-Cookie: omnipus-session=<attacker>` or `Set-Cookie: csrf=<fixed>` and plant/overwrite the operator's session and CSRF cookies on the shared origin (session fixation / cookie-tossing). The path approach *creates* this surface; the second listener never had it.
- **Failure scenario**: A previewed app (or a compromised npm dep in it) responds through the proxy with `Set-Cookie: csrf=known` and `Set-Cookie: omnipus-session=…`; the operator's next state-changing `/api/v1/*` call carries the attacker-fixed CSRF token, or the operator's session cookie is clobbered mid-use.
- **Recommendation**: Strip **all** request cookies before forwarding (the dev server has no legitimate need for the operator's origin cookies — it manages its own via the proxied path), not just `omnipus-session`. In `ModifyResponse`, drop or namespace upstream `Set-Cookie` headers that target the reserved names (`omnipus-session`, `csrf`, `__Host-csrf`) — or all of them. If the residual is genuinely accepted, the Non-Behaviors section must state the **write/fixation** vector explicitly, not only the read vector. Add a BDD + test: "a dev-server `Set-Cookie: omnipus-session=x` does not reach the browser as an origin cookie."

#### [MAJ-003] FR-011 (onboarding issues the session cookie) is underspecified in exactly the way that re-opens the r2 C-2 fresh-install lockout

- **Lens**: Incompleteness / Incorrectness
- **Affected**: FR-011; US-5 scope + AS-7; S13; Test 13 `TestOnboardingIssuesSessionCookie`; `session_cookie.go:175-236`, `rest_onboarding.go:129-342,381`
- **Description**: `IssueSessionCookie(username, mutator)` (verified) has hard preconditions: `username != ""`, and its mutator must **find** `gateway.users[<username>]` — if the user is absent or the config write fails it returns an error and writes **no cookie** (contract: "Callers MUST surface a 500"). `HandleCompleteOnboarding` creates and appends the admin user (`rest_onboarding.go:341`) and today issues only the CSRF cookie (`:381`). FR-011 says "also call `IssueSessionCookie`" but specifies none of: (a) it must bind to `body.Admin.Username`; (b) it must run **after** the user-append config mutation has committed (IssueSessionCookie does its own read-modify-write and will not see an uncommitted user); (c) onboarding MUST return **500** (not 200) if the cookie cannot be issued. Post-migration the SPA no longer stores the returned bearer token (FR-010), so an onboarding that returns 200 with the CSRF cookie but **no session cookie** (silent IssueSessionCookie error, or wrong ordering) leaves the fresh install with no credential at all — the r2 C-2 lockout, re-created.
- **Failure scenario**: Implementer calls `IssueSessionCookie` before the user-append commits (or ignores its error) → onboarding returns 200, no session cookie, SPA holds no token → next `/api/v1/*` → 401 → fresh install locked out with no recovery but wiping state.
- **Recommendation**: FR-011 must state: issue the session cookie bound to `body.Admin.Username`, ordered after the admin user is persisted to `gateway.users`, and **fail onboarding (500) if IssueSessionCookie errors** — do not return 200 without the cookie. S13/Test 13 must assert not just that Set-Cookie is present, but that a **subsequent** `/api/v1/*` request carrying that cookie authenticates (round-trip against the persisted `SessionTokenHash`), and add the negative: IssueSessionCookie failure → 500, onboarding not marked complete.

#### [MAJ-004] The CSRF re-mint fix (FR-019) assumes a GET-before-POST ordering and a fresh cookie read that the spec never requires

- **Lens**: Incompleteness / Incorrectness (race)
- **Affected**: FR-019; FR-010; S20; Test 27 `TestCSRFCookieRemintOnGet`; r3 C-1 (lines 39–43)
- **Description**: FR-019 re-mints the CSRF cookie "on any authenticated safe (GET) request that lacks it," which only prevents the returning-user 403 lockout **if two unstated conditions hold**: (1) the SPA reads the CSRF cookie **fresh from the cookie jar per state-changing request** — if it caches a token in memory at load and the gateway later re-mints a *new* value on a GET, the cached echo ≠ the new cookie → double-submit mismatch → 403; and (2) **a GET actually precedes the first POST**. A returning user whose first action is a state-changing request (a POST-only flow, or a deep link that mounts straight into a mutation) hits the write before any GET re-mints the cookie → 403 with no recovery. FR-010 only says "echo the CSRF token on state-changing requests"; it never says "read it fresh," and nothing guarantees the GET-first ordering.
- **Failure scenario**: Returning same-day user (session valid, CSRF cookie expired) opens a URL that fires a POST as its first request → no CSRF cookie yet, none echoed → 403, unrecoverable until they navigate somewhere that does a GET.
- **Recommendation**: Add to FR-010: the SPA MUST read the CSRF cookie **fresh from `document.cookie` per state-changing request** (never a cached copy). Add the missing recovery path: on a 403-CSRF response, the SPA MUST perform a safe GET (to trigger re-mint) and retry once — OR the gateway MUST also (re-)issue the CSRF cookie on the `401→login` and any auth-resolution path, not only on GET. Add a BDD/test for "first request after re-open is a POST" and "cookie re-minted mid-session → next POST reads the new value."

### MINOR

#### [MIN-001] US-6 AS-1 "MAY still contain `public_url`" contradicts FR-007 "MUST REMAIN"
- **Lens**: Ambiguity / Inconsistency
- **Affected**: US-6 AS-1 vs FR-007
- **Description**: Same subject as CRIT-001. AS-1 uses permissive "MAY still contain" while FR-007 mandates "MUST REMAIN restart-gated." "MAY" invites an implementer to legitimately remove it. Pick one modality (MUST) so the requirement is enforceable.
- **Recommendation**: Change AS-1 to "MUST still contain `gateway.public_url` (intentional — r3 M-2)."

#### [MIN-002] SC-010 requires "e2e" green as a hard success criterion, contradicting the project's own gating policy
- **Lens**: Infeasibility
- **Affected**: SC-010
- **Description**: SC-010 lists "e2e" inside "Full CI green." Per CLAUDE.md and the project's operating notes, e2e is **not** a PR gate and the LLM shards are broadly flaky (per-run single-shard timeouts that clear on re-run). Making a green e2e run a binary exit condition is either infeasible-as-stated or contradicts the established policy, and will produce false BLOCKs on flake.
- **Recommendation**: Scope SC-010's e2e clause to the **auth/preview specs added by this change** (tests 25–26) and adopt the project's known re-run allowance for LLM shards; keep e2e out of the hard blocking set consistent with policy, or state the re-run rule explicitly.

#### [MIN-003] S22 is orphaned from the Traceability Matrix; Success-Criteria numbering is out of order
- **Lens**: Inconsistency (structural)
- **Affected**: Traceability Matrix; S22 (line 431) / Test 29; SC list (lines 526–538)
- **Description**: S22 ("no credential → 401") and Test 29 exist in the BDD + TDD sections but S22 appears under no FR row in the Traceability Matrix — the fail-closed 401 is untraced to any FR (FR-009 covers cookie-or-bearer success but not the neither→401 guarantee). Separately, the SC list runs SC-001…SC-010, then SC-012, SC-013, **then** SC-011 — out-of-order numbering that makes the set hard to audit.
- **Recommendation**: Add a fail-closed clause to FR-009 (or a new FR) and give S22/Test 29 a matrix row. Renumber SC-011 into sequence.

### OBSERVATION

#### [OBS-001] No rollback / kill-switch / phased path for the full-surface Bearer→cookie cutover
- **Lens**: Inoperability / Overcomplexity (coupling)
- The US-5 scope note is explicit: cookie auth is added to the **entire** authenticated route set "for the first time" — the single highest-blast-radius change here. `preview_enabled` gives the preview a live kill-switch, but the auth migration has **none**: no feature flag, no phased rollout, no documented rollback beyond reverting code. The retained `Authorization: Bearer` path does not rescue a browser user (who has no bearer token). Also unstated: existing logged-in users hold a JS token but no session cookie; on upgrade the SPA stops reading the token and they are **silently logged out** — a migration-UX event worth documenting. The operator chose to bundle the migration ("keep it bundled, fix everything"), which is respected — but the spec should still state the rollback story and the forced-re-login-on-upgrade so on-call isn't surprised.

#### [OBS-002] Observability blind spot on the new auth path
- **Lens**: Inoperability
- No structured log, audit event, or metric is specified for: cookie-vs-bearer resolution, the cookie/bearer **mismatch** case (`session_cookie.go` itself notes "the mismatch is logged at a configurable level" — the spec neither wires nor surfaces it), CSRF re-mint, logout, or `preview_enabled=false` 404s. A cookie-auth regression and a legitimately-logged-out user both present as bare 401 — on-call cannot distinguish them. Add: a WARN/metric on cookie-resolution failure vs absence, a counter for preview-disabled 404s vs unknown-token 404s, and an audit entry for logout.

#### [OBS-003] SSRF allowlist host-representation is ambiguous (refines Ambiguity #3)
- **Lens**: Ambiguity
- FR-018 allows "the gateway's own host:port," but `serve_web` emits `http://localhost:<gateway.port>` (US-3 AS-2), while the SSRF checker resolves hostnames to IPs and blocks the `127.0.0.0/8` CIDR (`ssrf.go`). The port-aware exception must therefore special-case the **literal `localhost:<port>` token pre-resolution** — not merely `127.0.0.1:<port>` post-resolution — or the agent-browser navigation to the localhost preview URL fails the allowlist even though it is the intended target. Ambiguity #3 identifies the seam (`NewSSRFChecker`) but not the `localhost` vs `127.0.0.1` vs `::1` representation trap or the pre-/post-resolution ordering. Spec should pin the exact host form(s) matched and require a test for `localhost:<port>` (the form serve_web actually emits), not just `127.0.0.1:<port>`.

#### [OBS-004] Dev-server lifecycle on `preview_enabled=false` is unspecified
- **Lens**: Incompleteness (data/resource lifecycle)
- US-4/FR-006 make `/preview/` 404 and `serve_web` refuse when disabled, but say nothing about **already-running** dev servers (`DevServerRegistry` entries + their npm child process groups). They keep running while unreachable (resource leak) and reappear — possibly stale — on re-enable. State whether disabling tears down running dev servers or intentionally leaves them (and document the leak + the idle-TTL that eventually reaps them).

---

## Structural Integrity Results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | PASS |
| Every acceptance scenario has ≥1 BDD scenario | PASS |
| Every BDD scenario has a `Traces to:` back-reference | PASS (via *US-x AS-y* labels) |
| Every BDD scenario has a corresponding TDD test | PASS (S1–S22 all mapped) |
| Every FR appears in the Traceability Matrix | PASS (FR-001…FR-020) |
| Every BDD scenario appears in the matrix | **FAIL** — S22 (no-credential→401) has no FR row (MIN-003) |
| Test datasets cover boundary/edge/error | PARTIAL — good coverage, but the "`public_url` A→B runtime" row encodes contradicted behaviour (MAJ-001) |
| Regression impact explicitly addressed | PASS (Regression Tests §1–7 are thorough) |
| Success criteria measurable, no subjective language | PARTIAL — SC-010 "e2e green" is not reliably measurable per policy (MIN-002); SC numbering out of order (MIN-003) |
| Internal cross-references consistent | **FAIL** — `public_url` gating stated four contradictory ways (CRIT-001); S3 vs FR-005 (MAJ-001); US-6 AS-1 vs FR-007 (MIN-001) |

## Test Coverage Assessment

- **Negative paths**: mostly present (401, 403, 404, disabled). **Missing**: onboarding-cookie-issuance **failure** → 500 (MAJ-003); first-request-is-a-POST after re-open (MAJ-004); dev-server `Set-Cookie` neutralisation (MAJ-002).
- **Boundary/representation**: SSRF test 17 asserts "other local port blocked" but not the exact `localhost:<port>` (vs `127.0.0.1`) form serve_web emits (OBS-003).
- **Concurrency/idempotency**: no test for CSRF re-mint under concurrent GET+POST or a cached-then-re-minted token (MAJ-004).
- **Contract behaviour that a passing test would get *wrong***: Test 9 as written (`public_url` not gated) would pass while violating FR-007 (CRIT-001) — the most dangerous kind of coverage gap: a green test that certifies the wrong behaviour.

## STRIDE Threat Summary

| Component | Threats identified |
|---|---|
| `/preview/` reverse proxy | **Tampering** — upstream `Set-Cookie` unfiltered → session/CSRF fixation on shared origin (MAJ-002). **Info disclosure** — readable CSRF + other origin cookies forwarded to the dev app (MAJ-002). Spoofing — token-in-path only (accepted, stated). |
| Cookie auth surface (`/api/v1/*`, WS) | **Elevation** — same-origin previewed app rides `omnipus-session` to `/api/v1/*` and `/api/v1/{chat,browser}/ws` (accepted + stated, r3 M-3). **Repudiation** — no audit/log on auth resolution or logout (OBS-002). **DoS** — per-request bcrypt on cookie resolution (pre-existing on the bearer path; noted, not new). |
| CSRF middleware + re-mint | **Tampering** — attacker-fixed CSRF token via MAJ-002; **availability** — re-mint race / POST-before-GET → 403 lockout (MAJ-004). |
| Browser SSRF checker | **SSRF** — port-aware localhost exception must be scoped to the exact gateway host:port form (OBS-003); "all localhost" is correctly forbidden. |
| Config surface (`public_url`, `preview_enabled`) | **Integrity of security fences** — un-gating `public_url` (per the stale S18/Test 9) desyncs CORS/CSP `frame-ancestors`/WS `CheckOrigin` from a stale origin (CRIT-001). |

## Unasked Questions

1. When onboarding must issue the session cookie, **which username** does it bind to, and what happens (500? 200-without-cookie?) if `IssueSessionCookie` fails after the user is appended? (MAJ-003)
2. What stops a previewed dev app from writing `Set-Cookie: omnipus-session=…` back through the proxy onto the shared origin? (MAJ-002)
3. Does the SPA read the CSRF cookie fresh per request, and what recovers a returning user whose **first** action is a state-changing request? (MAJ-004)
4. What is the rollback for the cookie-auth cutover if it regresses in production on `hotfix/v0.1.1`, and are existing logged-in users forcibly re-logged-in on upgrade? (OBS-001)
5. On `preview_enabled=false`, are running dev-server child processes torn down or left leaked until idle-TTL? (OBS-004)
6. Does the SSRF exception match `localhost:<port>` (what serve_web emits) or only `127.0.0.1:<port>`? (OBS-003)

---

## Verdict: BLOCK

One CRITICAL (the spec is not consistently implementable and its TDD plan certifies a
security-fence desync) plus four MAJOR. The v4 fixes are sound in the FR/narrative layer but
were not propagated to the BDD/TDD/Symbols/Impact layers, and three security-relevant gaps
(cookie fixation, onboarding-cookie failure handling, CSRF re-mint ordering) are genuinely
unaddressed rather than merely mis-transcribed.

Review written to: `docs/internal/specs/preview-on-main-listener-spec-review-round4.md`

To address these findings, run:
  `/plan-spec --revise docs/internal/specs/preview-on-main-listener-spec.md docs/internal/specs/preview-on-main-listener-spec-review-round4.md`
