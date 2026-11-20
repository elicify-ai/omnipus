Status: Draft

ADR: none — no open design decision. The founder answered #1096 Q1 to Q4 (all A) and S-Q9 A, S-Q14 A. The shapes below are architect decisions. Two behaviour choices are open with the founder (FQ-3 and FQ-4 below); the spec is written for the recommended answers. **No developer starts on this package until the founder has answered them.**

# Feature Specification: Signed-in browsers (#1096)

**Created**: 2026-11-20 (moved out of `settings-redesign-spec.md` in its round-1 fix)
**Input**: the [#1096] block of `docs/internal/specs/settings-redesign-spec.md` at `1d8e31042c`; its round-1 review `docs/internal/specs/settings-redesign-spec-review.md` (findings CRIT-001, MAJ-011, MAJ-015, MAJ-016, MAJ-017, MAJ-018, MIN-002 and review questions Q3, Q4); founder answers #1096 Q1 to Q4 A, S-Q9 A, S-Q14 A (`/workspace/omnipus-coordination/handoffs/founder-questions-20261119/team-b-open-questions.md`); the wireframe's Sign-in section (`/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/app.js::signin`); the backend sizing `/workspace/logs/settings-1519/sizing.md`.
**Code baseline**: release `c2f8f8e735`. Every `file::symbol` below was read at that commit.
**Epic**: #1096 (Stage: Spec).
**Scope decision**: the chief cut this block out of the Settings package on 2026-11-20 at 05:08Z (founder answer pending; the founder can bring it back). Settings ships with its Sign-in section hidden. This spec owns the Sign-in section, the session set and its endpoints. When it lands, the Settings section list gains Sign-in (Security group, after Keys & secrets) and the old `?tab=devices` link opens it again (design-notes, "URLs keep working").
**Numbering**: user stories, requirements, datasets and hands-on rows keep their Settings-spec numbers (US-7, US-8, FR-074, FR-076 to FR-091, D-5 to D-7, G-19 to G-24) so earlier references stay valid. New items start at FR-092, D-14 and G-27.
**Riders** (moved with this block; confirmed by the chief on 2026-11-20): #689 (closed by this work), #1595, #1146, #598. Rider acceptance: "Riders" below.
**Agent tool field table**: no agent-managed object changes. The browser sign-in is server-owned credential state, not something an agent may set: `id`, `hash`, `issued_at`, `last_used_at`, `legacy` and `label`/`device_kind` are all derived by the server; `set_config` cannot write `gateway.users` (`pkg/sysagent/tools/config.go::validateConfigKey` refuses that subtree). No tool reads the list.

---

## Overview

Today one person can be signed in on one browser only: signing in on a phone signs the laptop out (#689, #1595). This package lets one person stay signed in on up to 10 browsers at once. Settings → Sign-in lists them with their last use, marks this browser, and lets the person end any other browser or all other browsers at once. Signing in on an 11th browser ends the one used longest ago. "Sign out" in the account menu ends this browser only. Changing the password ends every sign-in and every app token, as it does today.

Ending a browser's sign-in ends everything that sign-in can still do: its requests, its live connections, and the temporary preview links it opened (Library, mail and attachment previews). Content already downloaded to that browser cannot be taken back; the docs say so.

---

## Existing Codebase Context

GitNexus MCP tools were not connected; every fact comes from Grep and Read at `c2f8f8e735`. Risk levels are **Inferred**; backend-lead runs `impact` before editing.

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/gateway/rest_auth.go::HandleLogin`, `::HandleLogout`, `::HandleChangePassword` | modifies | Single `session_token_hash` slot: overwritten on login, cleared on logout; password change clears `tokens`, `token_hash` and `session_token_hash` |
| `pkg/gateway/middleware/session_cookie.go::ResolveUserFromCookie`, `::IssueSessionCookie`, `::MintSessionToken`, `::SessionCookieMaxAge`, `::IsCrossSiteSubresourceRequest` | modifies / keeps | One bcrypt compare per user; 24-hour cookie; cross-site subresource reads refused before any compare |
| `pkg/gateway/middleware/planted_cookie.go::genuineSessionResend`, `::plantedResendMaxComparisons` | modifies | ADR-094 FR-015 guard: verifies distinct candidates (at most 4 compares), refuses ambiguity, keeps original expiry |
| `pkg/gateway/middleware/csrf.go::CSRFMiddleware` | keeps | Skips the double-submit check for any request with `Authorization: Bearer` |
| `pkg/gateway/preview_token.go::PreviewSessionKey`, `::PreviewTokenStore.InvalidateSession`, `::Mint` | modifies | Preview grants are keyed by a digest of the **whole raw credential**; `HandleLogout` revokes the caller's own grants only |
| `pkg/gateway/rest_auth.go::revokePreviewTokensForSession`, mail preview store `invalidateSession`, `mailAttachmentTokens.revokeSession` | modifies | The three grant kinds revoked at logout today |
| `pkg/config/config.go::UserConfig` (`SessionTokenHash`, `SessionIssuedAt`, `Tokens`, `MaxUserTokens = 10`) | modifies | Bearer tokens are already a capped set |
| `pkg/gateway/websocket.go::WSHandler.ServeHTTP`, `::authenticateWS`; `pkg/gateway/browser_ws.go` (cookie read; `readLoop` calls `detachWebRTCViewer`) | modifies | Authenticate at upgrade, then register separately; no registry by sign-in; nothing closes a socket when its sign-in ends |
| `pkg/gateway/signin_provider.go::restAPIHost.IssueSession`, `::WriteSessionToken` | modifies | Platform-mode sign-in and the desktop claim route |
| `pkg/gateway/middleware/person_session.go::RequirePersonSession` | reuses | Refuses CLI token, dev bypass and non-account identities with 403 |
| `pkg/config/config.go` file size | constraint | 2,994 lines, not grandfathered; the fail limit is 3,000. The session entry type goes in a new `pkg/config/browser_sessions.go` (sizing.md) |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| `middleware.ResolveUserFromCookie` | **HIGH** (Inferred) | `auth.go` cookie path, `rest_auth.go::withOptionalAuth`, `websocket.go::authenticateWS`, `browser_ws.go`, `signin_provider.go::ResolveSessionUser`, `session_cookie.go` itself | every authenticated REST route, both WebSockets, platform sign-in |
| `middleware.genuineSessionResend` | **HIGH** (Inferred) | `PlantedCookieGuard` | every request with a duplicated reserved cookie |
| `PreviewSessionKey` | **HIGH** (Inferred) | Library, mail and attachment preview minting and revocation | every preview link a browser opens |
| `HandleLogin` / `IssueSessionCookie` | MEDIUM | `/auth/login`, `/onboarding/complete`, platform callback | onboarding and sign-in flows |
| WebSocket registration | MEDIUM | chat and live-browser sockets | live chat, live browser view |

The three **HIGH** rows need security-lead sign-off on this spec before GREEN starts.

---

## Contract Changes (contract-first — Hard Constraint #8)

Every new or changed shape goes into `contracts/` before any Go or TypeScript; backend-lead edits and regenerates with `scripts/gen-contracts.sh`.

### C-1 Signed-in browsers

**Decision.** The browser sign-in becomes a capped set, mirroring the bearer-token set (`UserConfig.Tokens`, `MaxUserTokens = 10`), as #689 proposes. Three endpoints under `/auth/browser-sessions` expose it, behind `withAuth` + `middleware.RequirePersonSession` (CLI token, dev bypass and the legacy env token get 403), in both local and platform auth modes.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `BrowserSession` | new | `contracts/components/schemas/BrowserSession.yaml` | One signed-in browser. Never the secret or its hash |
| `BrowserSessionList` | new | `contracts/components/schemas/BrowserSessionList.yaml` | Response of `GET /auth/browser-sessions` |
| `BrowserSessionsSignOutOthersResponse` | new | `contracts/components/schemas/BrowserSessionsSignOutOthersResponse.yaml` | Response of `POST /auth/browser-sessions/sign-out-others` |
| `/auth/logout` description | changed | `contracts/openapi.yaml` | Ends this browser's sign-in and its preview links only (Q2 A) |
| `/auth/change-password` description | changed | `contracts/openapi.yaml` | States today's behaviour: every browser sign-in and every app token ends |

Proposed `BrowserSession.yaml`:

```yaml
title: BrowserSession
type: object
description: >
  One browser signed in to the caller's account (#1096). Carries no secret:
  the id is the non-secret routing prefix of the session cookie, never the
  cookie value or its hash. label and device_kind are derived from the
  User-Agent header and are informational only — a client can send any
  User-Agent.
additionalProperties: false
required: [id, label, device_kind, signed_in_at, last_used_at, expires_at, current]
properties:
  id:
    type: string
    pattern: '^[A-Za-z0-9_-]{16}$'
    description: Non-secret sign-in id (96 random bits, base64url). Stable for the life of the sign-in. Not proof of anything on its own.
  label:
    type: string
    minLength: 1
    maxLength: 64
    description: Browser and operating system, e.g. "Chrome on macOS". "Unknown browser" when not recognised.
    example: Chrome on macOS
  device_kind:
    type: string
    enum: [desktop, phone, tablet, unknown]
    description: Picks the row icon only.
  signed_in_at:
    type: string
    format: date-time
  last_used_at:
    type: string
    format: date-time
    description: >
      Last authenticated request made with this sign-in. Exact while the
      gateway runs; across a gateway restart accurate to 10 minutes (FR-091,
      pending FQ-4).
  expires_at:
    type: string
    format: date-time
    description: signed_in_at plus the sign-in lifetime (24 hours, SessionCookieMaxAge; FQ-3).
  current:
    type: boolean
    description: True only for the browser sign-in the request itself was authenticated with. Always false for a bearer-authenticated request.
```

Proposed `BrowserSessionList.yaml`:

```yaml
title: BrowserSessionList
type: object
additionalProperties: false
required: [sessions, max_sessions]
properties:
  sessions:
    type: array
    maxItems: 10
    items: { $ref: './BrowserSession.yaml' }
    description: >
      Live sign-ins only (expired ones are never listed). Order: the current
      sign-in first, then by last_used_at, newest first; ties by signed_in_at,
      newest first.
  max_sessions:
    type: integer
    minimum: 10
    maximum: 10
    description: How many browsers may be signed in at once (founder #1096 Q1 A).
```

Proposed `BrowserSessionsSignOutOthersResponse.yaml`:

```yaml
title: BrowserSessionsSignOutOthersResponse
type: object
additionalProperties: false
required: [ended]
properties:
  ended:
    type: integer
    minimum: 0
    maximum: 9
    description: Number of other browser sign-ins that were ended.
```

Proposed `openapi.yaml` paths (decision-bearing parts; `security: [BearerAuth: []]` like the other authenticated auth routes):

```yaml
/auth/browser-sessions:
  get:
    operationId: listBrowserSessions
    summary: List the browsers signed in to this account (#1096)
    responses:
      '200': { content: { application/json: { schema: { $ref: '#/components/schemas/BrowserSessionList' } } } }
      '401': { $ref: '#/components/responses/401Unauthorized' }
      '403': { description: Not a signed-in person (CLI token, dev bypass, legacy env token), or a cross-site subresource request. }
/auth/browser-sessions/{id}:
  delete:
    operationId: endBrowserSession
    summary: End one other browser's sign-in (#1096)
    parameters: [{ name: id, in: path, required: true, schema: { type: string } }]
    responses:
      '204': { description: Ended. Its requests, live connections and preview links stop working. }
      '401': { $ref: '#/components/responses/401Unauthorized' }
      '403': { description: Not a signed-in person, or the cross-site request check failed. }
      '404': { description: No live sign-in with this id (unknown, malformed, already ended or expired — one indistinguishable answer). }
      '409': { description: The id is the caller's own current browser sign-in; use POST /auth/logout. }
/auth/browser-sessions/sign-out-others:
  post:
    operationId: signOutOtherBrowsers
    summary: End every browser sign-in except the caller's own (#1096)
    responses:
      '200': { content: { application/json: { schema: { $ref: '#/components/schemas/BrowserSessionsSignOutOthersResponse' } } } }
      '401': { $ref: '#/components/responses/401Unauthorized' }
      '403': { description: Not a signed-in person, or the cross-site request check failed. }
      '409': { description: The request was not authenticated by a browser sign-in (for example it carried a bearer token), so "other" has no meaning. }
```

#### Storage (not on the wire; stated so the contract has one meaning)

Each account's sign-ins live in `config.json` as `gateway.users[].sessions[]`. Each entry has exactly six fields:

| Field | Meaning |
|---|---|
| `id` | 16-character base64url id, assigned once (at sign-in or at migration) and never changed |
| `hash` | bcrypt over the secret part of the cookie only |
| `issued_at` | sign-in time; the 24-hour lifetime counts from it; never renewed |
| `last_used_at` | last use, flushed to disk at most once every 10 minutes per sign-in (FR-091) |
| `label`, `device_kind` | derived from the User-Agent; see FR-076 |
| `legacy` | `true` only for the one entry migrated from today's single slot; absent otherwise (FR-096) |

The cookie value becomes `<id>.<secret>` (the 43-character secret `MintSessionToken` already makes). An ordinary request costs one bcrypt compare (FR-077). A legacy cookie (no `<id>.` prefix) is compared only with the entry marked `legacy: true`.

**One browser identity.** The sign-in `id` is the single identity that binds the cookie, the live-connection registry, every preview grant the browser mints, and the audit entries. The server never stores the plaintext cookie. A preview grant minted by a cookie-authenticated request is keyed by `browser:<id>`, not by a digest of the raw cookie (`PreviewSessionKey` changes for cookie requests; bearer requests keep today's key). Ending a sign-in revokes everything keyed by that identity (FR-092).

**Ending is ordered.** Every end cause (End, sign-out-others, Sign out, password change, eviction, expiry) does, in this order, under the session registry's lock: mark the id ended in memory, revoke its preview grants, close its registered sockets; then the config write removes the entry. A request, socket registration or preview mint that resolved the id before the end re-checks "still live" under the same lock before it registers or publishes anything, and is refused if not (FR-095). If the config write fails, the id stays ended in memory (refused until restart), the end call answers 500, and the next successful config write removes it; after a restart without that write, the entry would be live again, so the end call's 500 tells the person to try again.

#### Migration (Q4 A)

On the first config load after the update, an existing `session_token_hash` (with `session_issued_at`) becomes one entry: new `id`, `legacy: true`, `issued_at` = the old `session_issued_at`, or the migration time when that field is missing, `label` "Unknown browser". The load writes the migrated file once (removing `session_token_hash` and `session_issued_at`). Nobody is signed out by the update. From then on the entry is an ordinary stored entry with a durable marker, so later saves and restarts never repeat the migration or renew its issue time. If the migration write fails, the gateway logs it, keeps serving with the legacy entry built in memory (same `issued_at`, so the expiry does not move), and retries on the next config write. The legacy entry ends like any other: by End, eviction, password change, or 24 hours after its original issue.

#### Threat model (for the security-lead pass)

Realistic risks get a requirement; exotic ones are parked.

| # | Risk | Realistic? | Requirement |
|---|---|---|---|
| T1 | A stolen or forgotten sign-in keeps working after the person ends it, through an open live connection | Yes | FR-084: every end cause closes the sign-in's sockets within 5 s; FR-095: no new socket is admitted after the end |
| T1b | An ended browser keeps reading through preview links it opened before (Library documents, mail, attachments) | Yes (review CRIT-001) | FR-092: every end cause revokes that browser's preview grants; mints racing the end are refused |
| T2 | A replayed cookie works after 24 h because only the browser drops it | Yes | FR-079: the server refuses a sign-in 24 hours old or older |
| T3 | A planted duplicate cookie (ADR-094 FR-015) defeats recovery or gets re-sent | Yes | FR-086 and FR-093: recovery verifies candidates by secret within its own budget; the id is never trusted |
| T4 | CPU exhaustion from many bcrypt compares per request | Yes | FR-077: one compare for an ordinary request; FR-093: recovery capped at 4 |
| T5 | A cross-site request ends sign-ins | Yes | FR-081: cookie-authenticated mutations need the double-submit CSRF header; a bearer request never acts as a browser sign-in |
| T5b | A framed cross-site preview reads with the new cookie format | Yes (review MAJ-018) | FR-094: the Fetch Metadata cross-site subresource refusal stays in front of every new and legacy cookie path |
| T6 | An attacker holding one stolen sign-in ends the owner's other browsers | Accepted | The owner signs in again and changes the password (FR-083); every end is audited (FR-088); docs say what to do (DOC-009) |
| T7 | Probing ids to learn which sign-ins exist | Yes, low | FR-082: one indistinguishable 404 |
| T8 | The list leaks where and how the person works | Yes, low | FR-076: label and device kind only; no IP address, no raw User-Agent |
| T9 | Concurrent sign-ins push the set past 10 | Yes | FR-078: the cap is enforced inside the one serialised config write |
| T10 | No record of who ended what | Yes | FR-088 |

Parked (exotic): timing differences between "unknown id" and "known id, wrong secret"; a forged User-Agent mislabelling a row (the label is informational, documented); binding a sign-in to an IP address or device fingerprint; a clock that jumps backwards (behaviour stated in D-6 row 6); more than one human account (`GatewayConfig.Users` "holds at most one entry"); content already downloaded before an end.

---

## API and Data

| Endpoint | New / Changed | Request / Response |
|---|---|---|
| `GET /auth/browser-sessions` | new | `BrowserSessionList` |
| `DELETE /auth/browser-sessions/{id}` | new | 204 / 404 / 409 |
| `POST /auth/browser-sessions/sign-out-others` | new | `BrowserSessionsSignOutOthersResponse` |
| `POST /auth/login`, `/onboarding/complete`, platform sign-in callback | behaviour | add a sign-in to the set instead of overwriting the slot |
| `POST /auth/logout` | behaviour | ends the caller's own browser sign-in and its preview grants only |
| `POST /auth/change-password` | behaviour (stated) | ends every browser sign-in and every app token (today's `HandleChangePassword`) |
| Library, mail and attachment preview mint routes | behaviour | grants minted by a browser sign-in are keyed by its id |
| Stored: `gateway.users[].sessions[]` | new | six-field entry above; never served by `GET /config` (FR-089) |

---
## User Stories & Acceptance Criteria

### User Story 7 — Stay signed in on several browsers (Priority: P1)

A person wants to use Omnipus on a laptop and a phone at the same time. Today signing in on one signs the other out (#689, #1595). Up to 10 browsers may be signed in at once; an 11th sign-in ends the one used longest ago (Q1 A, S-Q14 A). Sign out ends this browser only (Q2 A). Changing the password ends every sign-in and every app token, as today. Nobody is signed out by the update (Q4 A).

**Why this priority**: founder-set P1 (#1096 epic); it removes a daily annoyance and a test-script hazard.

**Independent Test**: sign in from up to 11 separate browsers or cookie jars and check which remain signed in after each step.

**Acceptance Scenarios**:

1. **Given** browser A is signed in, **When** the same person signs in on browser B, **Then** both A and B stay signed in.
2. **Given** 10 browsers are signed in, **When** an 11th signs in, **Then** the one used longest ago is signed out and the other nine stay signed in.
3. **Given** browsers A and B are signed in, **When** the person chooses Sign out on A, **Then** A is signed out and B stays signed in.
4. **Given** browsers A and B are signed in and a script holds an app token, **When** the person changes the password on A, **Then** A, B and the script are all signed out.
5. **Given** a person signed in before the update, **When** the update is installed and they continue working, **Then** they stay signed in.
6. **Given** a sign-in made 24 hours ago or earlier, **When** a request is made with it, **Then** it is refused.
7. **Given** browser B has a chat open with a live connection, **When** its sign-in is ended from browser A, **Then** B's live connection closes within 5 seconds and its next action leads to the sign-in screen.
8. **Given** a page planted a second cookie with the sign-in cookie's name, **When** the next request arrives, **Then** the sign-in protection still answers as it does today (clears the planted cookie, re-sends the genuine one, refuses changes).
9. **Given** browser B opened a document, mail or attachment preview, **When** B's sign-in ends in any way, **Then** that preview link stops working, while other browsers' previews keep working.
10. **[Pending FQ-4]** **Given** browser A was used a few minutes before a gateway restart and browser B not since the morning, **When** an 11th browser signs in after the restart, **Then** B is signed out, not A.

### User Story 8 — See and end signed-in browsers (Priority: P1)

A person who lost a phone, or used a shared computer, wants to see where they are signed in and end the ones they no longer use (Q3 A, S-Q9 A).

**Why this priority**: the list and the End button are what make multi-browser sign-in safe.

**Independent Test**: with three browsers signed in, open Settings → Sign-in on one of them; end one, then sign out the rest.

**Acceptance Scenarios**:

1. **Given** three browsers are signed in, **When** Sign-in opens on one of them, **Then** three rows show browser and system, and last use; this browser is marked "This browser" and has no End button.
2. **Given** that list, **When** the person ends another browser and confirms, **Then** that row disappears and that browser is signed out.
3. **Given** that list, **When** the person chooses Sign out other browsers and confirms, **Then** only this browser remains, and the confirmation said "You stay signed in here".
4. **Given** only this browser is signed in, **When** Sign-in opens, **Then** this browser's row shows and below it "No other browsers are signed in".
5. **Given** a row whose browser was already signed out elsewhere, **When** the person ends it, **Then** the list refreshes and says "That browser was already signed out."
6. **Given** the list cannot be loaded, **When** Sign-in opens, **Then** an error notice names what failed and offers Retry.
7. **Given** a script using the command-line token, **When** it asks for the list, **Then** it is refused, because only a signed-in person may see or end sign-ins.

### Edge Cases

- **The browser marked "This browser" is ended by another browser.** Its next request is refused and the page goes to the sign-in screen. An unsent message is kept or its loss is said plainly (#1146, FR-097).
- **The gateway restarts.** Every live sign-in stays valid. Last use is accurate to 10 minutes across the restart (FR-091, pending FQ-4).
- **Two sign-ins arrive at the same moment with 10 already live.** One sign-in is ended per new sign-in, and the set never holds more than 10.
- **The desktop app's claim step** gives the app the same sign-in as the system browser that completed it; the list shows it once.
- **A request carries both a session cookie and a bearer token.** It is a bearer request: no current browser sign-in, no CSRF check (as today), and it cannot use Sign out other browsers (409).
- **A preview link was opened in browser B before B was ended.** The link stops working; what B already downloaded stays on B.

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Settings → Sign-in (Security group, after Keys & secrets) | Spinner "Loading Sign-in…" + Skeleton rows | This browser's row + kit Empty "No other browsers are signed in" | Callout naming what failed, the cause, Retry | — | Rows, the limit line, Sign out other browsers |
| End confirm | — | — | "That browser was already signed out." after a 404 | — | Row removed |
| Sign out other browsers confirm | — | — | Callout with the cause and Retry | — | "You stay signed in here"; only this row remains |

Wording follows the wireframe (`app.js::signin`) and `omnipus-voice`. The wireframe's sample row "Last used 3 days ago" (`app.js:286`) cannot occur while a sign-in lasts 24 hours; the sample changes (FQ-3).

## Accessibility and Keyboard

The Settings spec's rules apply (one focus ring, dialog stacking, live regions, 44 × 44 px targets). In addition: each End button's accessible name is "End <label>" ("End Safari on iPadOS"); "This browser" carries an icon and the word, not colour only; after an End, focus moves to the next row's End button, or to "Sign out other browsers…" when none is left, or to the section heading.

## Design-System Components

Catalogued only: `Card` rows (the catalog has no `Item`), `Badge`, `Button`, `Dialog`, `Empty`, `Callout`, `Skeleton`, `Spinner`. No new kit component.

## Security and User Promises

- Focus areas (security-lead review regardless of size): gateway auth and sign-ins (`rest_auth.go`, `middleware/session_cookie.go`, `planted_cookie.go`, `csrf.go`), WebSocket authentication, preview grants.
- `docs/security.md` promises that logout revokes temporary previews and that mail is fetched fresh per request (`docs/security.md::Mail metadata and temporary previews`). This package extends the promise to every way a sign-in ends (DOC-009).
- Threat model: C-1.

---

## Behavioral Contract

Primary flows:
- When a person signs in, the system keeps their other browser sign-ins, up to 10 in total.
- When an 11th browser signs in, the system ends the sign-in used longest ago.
- When a sign-in is ended, the system refuses that browser's next request, closes its live connections within 5 seconds, and stops its preview links.
- When the person chooses Sign out, the system ends this browser only.
- When the password changes, the system ends every browser sign-in and every app token.

Error flows:
- When a sign-in id is unknown, malformed, already ended or expired, the system gives one "not found" answer.
- When someone asks to end the sign-in they are using, the system refuses and points to Sign out.
- When a request carries a bearer token, the system never treats it as a browser sign-in.
- When a cross-site page makes a subresource request with the cookie, the system refuses it before checking the cookie.

Boundary conditions:
- When exactly 10 browsers are signed in, the system ends none; the 11th sign-in ends exactly one.
- When a sign-in is exactly 24 hours old, the system refuses it.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not offer "Sign out everywhere"; Sign out ends this browser only (Q2 A).
- The system must not store or show a browser's IP address or raw User-Agent (T8).
- The system must not end app (bearer) tokens when browsers are ended or signed out; only a password change does, as today.
- The system must not trust the public id as proof of a sign-in; only the secret proves it.
- The system must not store the plaintext cookie anywhere, including as a preview-grant key.
- The system must not stop an agent's running turn because a browser's socket closed (ADR-082).
- The UI must say "browser" and "sign-in", never "session" alone (FR-074), because "Clear all sessions" in Data & retention means chat sessions.

### Machine-Verifiable Constraints

**Error Codes / Messages**:
- `GET /auth/browser-sessions` → 200 `BrowserSessionList`; CLI token, dev bypass or legacy env token → 403; none → 401.
- `DELETE /auth/browser-sessions/{id}` → 204 for another live sign-in; 404 for unknown, malformed, ended or expired; 409 for the caller's own.
- `POST /auth/browser-sessions/sign-out-others` → 200 `{ended: n}`; 409 when the request is not authenticated by a browser sign-in.
- A request whose sign-in was issued 24 h ago or earlier → 401 on authenticated routes; anonymous on optional-auth routes.
- A preview, mail or attachment link minted by an ended sign-in → the same refusal an unknown preview token gets today.

**Performance Bounds**:
- An ordinary request with one session cookie MUST cost at most 1 bcrypt comparison, for new and legacy cookies.
- The duplicate-cookie recovery path MUST cost at most `plantedResendMaxComparisons` (4) comparisons per request.
- Ending a sign-in MUST close its WebSocket connections within 5 s.
- Last use MUST cause at most one config write per sign-in per 10 minutes.

**Scope Boundaries**:
- The system MUST NOT change the sign-in lifetime of 24 hours (FQ-3).
- The system MUST NOT add an agent tool or a `set_config` path for sign-ins.

## Integration Boundaries

### Gateway REST API (SPA ↔ gateway)
- **Data in / out**: the three endpoints; login, logout, change-password, onboarding and platform sign-in.
- **Contract**: `contracts/openapi.yaml` and the three schemas.
- **On failure**: the Sign-in section shows its error Callout with Retry.

### Gateway WebSockets (chat, live browser)
- **Data in / out**: no new frames; the gateway closes a socket whose sign-in ended; registration re-checks the sign-in under the registry lock.
- **Contract**: `contracts/asyncapi.yaml` unchanged.
- **On failure**: the SPA's reconnect gets 401 and goes to sign-in (#1146).

### Preview grants (Library, mail, attachments)
- **Data in / out**: grants keyed by `browser:<id>` for cookie requests.
- **On failure**: a revoked grant answers like an unknown token.

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Which browsers and systems the label recognises | Pull in a User-Agent parsing library | No new dependency: a fixed table (Chrome, Edge, Firefox, Safari, Opera; Windows, macOS, Linux, iOS, iPadOS, Android, ChromeOS); anything else "Unknown browser" |
| 2 | Which credential wins when a request has both a cookie and a bearer header | The cookie | The bearer: CSRF already skips such requests, so they must not act as a browser sign-in |
| 3 | Whether last use survives a restart | In memory only (the round-0 text) | Pending FQ-4; written for A (10-minute flush) |
| 4 | Exact error and notice text | Invent wording | Use this spec's text; frontend-lead applies omnipus-voice |

---

## BDD Scenarios

Expected values come from this spec, the contract and the founder answers — never from today's implementation.

### Feature: Several browsers signed in (User Story 7)

#### Scenario: Signing in on a second browser keeps the first
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path
- **Given** browser A is signed in
- **When** the same person signs in on browser B
- **Then** an authenticated request from A answers 200 and one from B answers 200

#### Scenario Outline: The 11th sign-in ends the one used longest ago
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Edge Case
- **Given** `<live>` live sign-ins with the last-use times in `<state>`
- **When** a new browser signs in
- **Then** `<ended>` is signed out and the set holds `<after>` sign-ins

**Examples**: Dataset D-5.

#### Scenario: Sign out ends only this browser
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Happy Path
- **Given** browsers A and B signed in
- **When** A sends `POST /auth/logout`
- **Then** A's next authenticated request answers 401 and B's answers 200

#### Scenario: A password change ends every sign-in
**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Happy Path
- **Given** browsers A and B signed in, and a script holding an app token from `POST /auth/login`
- **When** A changes the password
- **Then** authenticated requests from A, B and the script all answer 401

#### Scenario: The sign-in from before the update keeps working
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Happy Path
- **Given** a `config.json` with one `session_token_hash` and `session_issued_at` 2 hours ago, and a browser holding the matching cookie (no id prefix)
- **When** the updated gateway starts and the browser makes a request
- **Then** the request answers 200, the list shows one sign-in marked current with the browser's label, and `config.json` has one `sessions` entry and no `session_token_hash`

#### Scenario Outline: A sign-in older than its lifetime is refused
**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a sign-in issued `<age>` ago
- **When** a request is made with its cookie
- **Then** an authenticated route answers `<status>`

**Examples**: Dataset D-6.

#### Scenario: Ending a sign-in closes its live connection
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Happy Path
- **Given** browser B with an open chat WebSocket authenticated by its cookie
- **When** browser A ends B's sign-in
- **Then** B's WebSocket is closed within 5 s, and B's reconnect attempt is refused

#### Scenario Outline: Every way a sign-in ends closes its live connections
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Edge Case
- **Given** browser B with an open chat WebSocket and an open live-browser WebSocket
- **When** B's sign-in ends by `<cause>`
- **Then** both sockets close within 5 s

**Examples**:

| cause |
|---|
| End from browser A |
| Sign out other browsers from A |
| Sign out on B |
| password change on A |
| eviction by an 11th sign-in |
| expiry at 24 h |

#### Scenario: The planted-cookie protection still works with several sign-ins
**Traces to**: User Story 7, Acceptance Scenario 8
**Category**: Edge Case
- **Given** browsers A and B signed in, and a request from A carrying A's cookie plus a planted second `omnipus-session` cookie
- **When** the request reaches the gateway
- **Then** the clear set is sent, A's genuine cookie is re-sent with A's original expiry, a state-changing request answers `planted_cookie_cleared`, and B is unaffected

#### Scenario: One password-strength check per request
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Edge Case
- **Given** 10 live sign-ins and a request carrying the 10th's cookie
- **When** the request is authenticated
- **Then** exactly 1 bcrypt comparison ran (counted through the compare hook `config.SetCompareHashAndPasswordForTest`)

#### Scenario: Simultaneous sign-ins never exceed the cap
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Edge Case
- **Given** 10 live sign-ins
- **When** 5 sign-ins arrive at the same time
- **Then** the set holds exactly 10, the 5 newest are among them, and exactly 5 older ones were ended

#### Scenario: A platform-mode sign-in joins the set
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Alternate Path
- **Given** a platform-mode install with browser A signed in
- **When** browser B completes the omnipus.ai sign-in
- **Then** both A and B stay signed in, and the desktop claim of B's sign-in adds no third entry

#### Scenario: App tokens are not touched
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Edge Case
- **Given** a script holding a bearer token from a REST sign-in, and browsers A and B
- **When** A chooses Sign out other browsers
- **Then** the script's bearer request still answers 200

#### Scenario: The configuration read never shows sign-ins
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Error Path
- **Given** two live sign-ins
- **When** `GET /config` is called
- **Then** the response contains no `sessions` key under any user, and no bcrypt hash string

#### Scenario: Sign-in events are audited
**Traces to**: User Story 7, Acceptance Scenarios 2, 3
**Category**: Alternate Path
- **Given** an empty audit log
- **When** a person signs in, ends another browser, signs out other browsers, and an 11th sign-in evicts one
- **Then** the audit log holds one entry for each, naming the event, the account and the session id, and none holds a cookie value or hash

#### Scenario: A gateway restart keeps every sign-in
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Edge Case
- **Given** browsers A and B signed in, B last used 1 minute ago, and the last-use flush ran after that use
- **When** the gateway restarts
- **Then** A and B both answer 200, and the list shows B's last use as stored (within 10 minutes of the real last use; pending FQ-4)

### Feature: Signed-in browsers list (User Story 8)

#### Scenario: The list shows every signed-in browser
**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Happy Path
- **Given** sign-ins from "Chrome on macOS" (this browser), "Safari on iPadOS" (used 2 hours ago) and "Firefox on Linux" (used 5 hours ago)
- **When** Sign-in opens in Chrome
- **Then** three rows show in that order; the first carries "This browser" and no End button; the others carry End
- **And** the line "Up to 10 browsers stay signed in at once. Signing in on an 11th ends the one used longest ago." shows

#### Scenario: Ending another browser
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path
- **Given** the list from the previous scenario
- **When** the person presses "End Safari on iPadOS" and confirms "End sign-in"
- **Then** that row is gone, and Safari's next request answers 401

#### Scenario: Signing out every other browser
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Happy Path
- **Given** three sign-ins with Chrome current
- **When** the person chooses "Sign out other browsers…" and confirms
- **Then** the confirm said "You stay signed in here", the answer was `{"ended": 2}`, only Chrome's row remains, and Chrome stays signed in

#### Scenario: Only this browser is signed in
**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** one sign-in
- **When** Sign-in opens
- **Then** this browser's row shows and below it the Empty state "No other browsers are signed in"

#### Scenario: Ending a browser that is already gone
**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Error Path
- **Given** the list shows Safari, which was meanwhile ended from another browser
- **When** the person ends Safari
- **Then** the server answered 404, the list refreshes without Safari, and the page says "That browser was already signed out."

#### Scenario: The list cannot be loaded
**Traces to**: User Story 8, Acceptance Scenario 6
**Category**: Error Path
- **Given** `GET /auth/browser-sessions` answers 500
- **When** Sign-in opens
- **Then** an error Callout says the signed-in browsers could not be loaded, with the cause and Retry

#### Scenario Outline: Only a signed-in person may use the browser list
**Traces to**: User Story 8, Acceptance Scenario 7
**Category**: Error Path
- **Given** a caller authenticated by `<credential>`
- **When** it calls `GET /auth/browser-sessions`
- **Then** the answer is `<status>`

**Examples**:

| credential | status |
|---|---|
| browser sign-in of the account | 200 |
| bearer token from `POST /auth/login` | 200 (no row marked current) |
| CLI token | 403 |
| dev bypass on | 403 |
| legacy `OMNIPUS_BEARER_TOKEN` with no account | 403 |
| none | 401 |

#### Scenario Outline: Ending one sign-in by id
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Error Path
- **Given** a signed-in browser with live sign-ins as in `<state>`
- **When** it sends `DELETE /auth/browser-sessions/<id>`
- **Then** the answer is `<status>` and `<effect>`

**Examples**: Dataset D-7.

#### Scenario: Sign out other browsers needs a browser sign-in
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Error Path
- **Given** a caller authenticated only by a bearer token from `POST /auth/login`
- **When** it sends `POST /auth/browser-sessions/sign-out-others`
- **Then** the answer is 409 and no sign-in ended

#### Scenario: Ending a sign-in without the CSRF header is refused
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Edge Case
- **Given** a signed-in browser
- **When** it sends `DELETE /auth/browser-sessions/{id}` without `X-CSRF-Token`
- **Then** the answer is 403 and the sign-in is still live

#### Scenario: End buttons are named for their browser
**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Edge Case
- **Given** the list with "Safari on iPadOS"
- **When** a screen reader reads the row's button
- **Then** its accessible name is "End Safari on iPadOS"

### Feature: Ending a sign-in ends everything it can do (round-1 additions)

#### Scenario Outline: Every way a sign-in ends revokes its preview links
**Traces to**: User Story 7, Acceptance Scenario 9
**Category**: Happy Path
- **Given** browsers A and B signed in, and B has minted a Library document preview, a mail preview and an attachment preview, and A has minted one Library preview
- **When** B's sign-in ends by `<cause>`
- **Then** a fresh request to each of B's three preview links is refused exactly as an unknown preview token is refused today
- **And** A's Library preview still serves its document (positive control)

**Examples**:

| cause |
|---|
| End from browser A |
| Sign out other browsers from A |
| Sign out on B |
| password change on A (A's preview is then refused too) |
| eviction by an 11th sign-in |
| expiry at 24 h (controlled clock) |

#### Scenario: A preview mint racing the end is refused
**Traces to**: User Story 7, Acceptance Scenario 9
**Category**: Edge Case
- **Given** B's preview-mint request has passed authentication and is paused at a test barrier before it publishes the grant
- **When** A ends B, and the paused mint resumes
- **Then** the mint answers 401, no grant for B exists in any of the three stores, and B's preview link (if one was returned) is refused

#### Scenario: A socket registration racing the end is refused
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Edge Case
- **Given** B's chat WebSocket upgrade has passed authentication and is paused at a test barrier before registration
- **When** A ends B, and the paused upgrade resumes
- **Then** the socket is closed at once without being registered, no message is delivered on it, and the same holds for a live-browser socket

#### Scenario: Expiry closes an idle socket without a new request
**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case
- **Given** B's sign-in issued 23 h 59 m 58 s ago (controlled clock) and B's chat socket open and idle
- **When** the clock passes 24 h 0 m 0 s and B sends nothing
- **Then** the socket is closed within 5 s of the 24-hour mark

#### Scenario: An agent's running turn continues when its viewer is ended
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Edge Case
- **Given** browser B watches a chat whose agent is mid-turn
- **When** A ends B
- **Then** B's socket closes, the turn completes, and A sees the finished reply

#### Scenario Outline: Duplicate-cookie recovery verifies by secret, within its budget
**Traces to**: User Story 7, Acceptance Scenario 8
**Category**: Edge Case
- **Given** a same-origin request carrying `<cookies>` in that order
- **When** the planted-cookie guard runs
- **Then** the result is `<result>` and at most `<compares>` bcrypt comparisons ran

**Examples**: Dataset D-14.

#### Scenario Outline: The cross-site read defense covers new and legacy cookies
**Traces to**: User Story 7, Acceptance Scenario 8
**Category**: Error Path
- **Given** a `<cookie>` cookie of a live sign-in
- **When** a request arrives with `<fetch metadata>` on `<route kind>`
- **Then** it is `<result>`, no bcrypt comparison ran for the refused rows, and no cookie is re-sent

**Examples**: Dataset D-15.

#### Scenario: A request with a bearer token never acts as a browser sign-in
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Edge Case
- **Given** browser A's valid cookie and a valid bearer token of the same account in one request, with no CSRF header
- **When** it sends `POST /auth/browser-sessions/sign-out-others`
- **Then** the answer is 409 and no sign-in ended; `GET /auth/browser-sessions` with the same credentials marks no row current

#### Scenario: The legacy sign-in survives saves and restarts
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Edge Case
- **Given** a migrated legacy entry from a `config.json` with `session_issued_at` 2 hours ago, and the old browser holding its unprefixed cookie
- **When** a second browser signs in, an unrelated Gateway setting is saved, and the gateway restarts twice
- **Then** the old cookie still answers 200 after each step, `config.json` holds exactly one entry with `legacy: true` and an unchanged `id` and `issued_at`, and the cookie is refused once 24 hours from the original issue have passed

#### Scenario: A legacy entry with no issue time is dated once
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Edge Case
- **Given** a `config.json` with `session_token_hash` and no `session_issued_at`
- **When** the gateway starts at T, then restarts at T + 2 h
- **Then** the entry's `issued_at` is T after both starts, and the cookie is refused from T + 24 h

#### Scenario: A failed migration write signs nobody out
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Error Path
- **Given** a legacy `config.json` that cannot be written at the first start (read-only file in the test)
- **When** the gateway starts and the old browser makes a request
- **Then** the request answers 200, an error is logged naming the failed migration write, and once the file is writable the next config write stores the legacy entry with the original `issued_at`

#### Scenario: Ending the legacy sign-in
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the migrated legacy entry and a new browser A
- **When** A ends the legacy entry
- **Then** the old browser's unprefixed cookie is refused, and no entry with `legacy: true` remains

#### Scenario: [Pending FQ-4] "Used longest ago" holds across a restart
**Traces to**: User Story 7, Acceptance Scenario 10
**Category**: Edge Case
- **Given** 10 live sign-ins where A signed in at 09:00 and was last used at 11:55, and B signed in at 10:00 and was last used at 10:00, and every other sign-in was used after 11:00; the last-use flush ran at 11:58
- **When** the gateway restarts at 12:00 and an 11th browser signs in before anyone makes a request
- **Then** B is ended and A stays signed in

#### Scenario: An ended browser's unsent message is not lost silently
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Error Path
- **Given** browser B has typed, unsent text in a chat composer
- **When** A ends B and B presses Send
- **Then** B lands on the sign-in screen with a notice that it was signed out; the typed text is either still in the composer after signing in again on the same chat, or the notice says plainly that it was not sent (#1146 acceptance, FR-097)

#### Scenario: A REST sign-in's app token survives a browser sign-in
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Edge Case
- **Given** a script holding a bearer token from `POST /auth/login`
- **When** the same person signs in on a browser
- **Then** the script's next bearer request answers 200 (#1595)

#### Scenario: Eleven sign-ins in one test do not trip a shared limiter
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Edge Case
- **Given** a test that signs in 11 times with the right password, run after another test that used up the login limiter
- **When** the 11 sign-ins run
- **Then** none answers 429, because each test gets its own limiters (#598, FR-098)

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit (Go) | session set (add, cap, eviction, expiry, migration, legacy marker, last-use flush), label table, recovery budget | rules in isolation, with a controlled clock and the bcrypt compare counter (`config.SetCompareHashAndPasswordForTest`) |
| Integration (Go, `httptest` against the gateway's handlers) | the three routes; login, logout, change-password, onboarding, platform sign-in; preview, mail and attachment grant revocation; WebSocket close and admission; planted-cookie and cross-site guards; `GET /config` | the server contract end to end, with test barriers for the race cases (no sleeps as the only oracle) |
| Unit (Vitest + Testing Library) | Sign-in section | screen states with a mocked API |
| E2E (Playwright, one targeted spec file) | rows marked "stable" | the whole journey in real browsers against a real gateway |
| Gates before hand-back | `heavy npm run typecheck`, `heavy npm run lint:design-system-locks`, `scripts/gen-contracts.sh` then `git diff --exit-code` on the generated folders | type, design-system and contract-drift evidence, saved with exit codes |

### Hands-on UAT rows (written at spec time)

The hands-on test runs on the exact cut commit (it touches gateway WebSocket handlers).

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| G-19 | US-7 AS1–AS3 | Sign in on browsers A and B; sign out on A | A and B both work; after Sign out only B works | sign-in | A + B | stable |
| G-20 | US-7 AS2 | Sign in 11 times (separate cookie jars), using jar 3 last-but-one | jar used longest ago is signed out; 10 remain | eviction | 11 jars by script | stable (scripted jars) |
| G-21 | US-7 AS5 | Sign in on the old release; update the binary; sign in on a second browser; save a Gateway setting; restart twice; continue on the old browser | still signed in after every step; list shows the old browser once | migration | same | reason not yet: needs two builds; keep manual |
| G-22 | US-7 AS7 | B in a chat with typed, unsent text; end B from A; B presses Send | B's socket closes ≤ 5 s; B lands on sign-in with the notice; text kept or loss stated (#1146) | live connections | same | stable |
| G-23 | US-8 AS1–AS5 | Three browsers; open Sign-in; End one; Sign out others; End an already-ended row (from a stale tab) | per scenarios | Sign-in section | End + Sign out others | stable |
| G-24 | US-7 AS4 | Two browsers and a script with an app token; change password on A | both browsers and the script are signed out | password change | same | stable |
| G-27 | US-7 AS9 | B opens a Library document preview and a mail attachment preview; copy both links; end B from A; open both links in B and in a private window | both refused; A's own preview still opens | preview revocation | same | stable |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | TestBrowserSessions_SecondSignInKeepsFirst | Integration (Go) | Signing in on a second browser keeps the first | the set exists |
| 2 | TestBrowserSessions_EvictsLeastRecentlyUsed | Unit (Go) | The 11th sign-in ends the one used longest ago | D-5 |
| 3 | TestBrowserSessions_LastUseSurvivesRestart [pending FQ-4] | Unit (Go) | "Used longest ago" holds across a restart | D-5 row 8 |
| 4 | TestBrowserSessions_ExpiryEnforcedServerSide | Unit (Go) | A sign-in older than its lifetime is refused | D-6 |
| 5 | TestBrowserSessions_OneBcryptPerRequest | Unit (Go) | One password-strength check per request | compare counter |
| 6 | TestPlantedCookieGuard_WithSessionSet | Integration (Go) | Duplicate-cookie recovery verifies by secret | D-14, ADR-094 FR-015 |
| 7 | TestSessionCookie_CrossSiteSubresourceRefused | Integration (Go) | The cross-site read defense covers new and legacy cookies | D-15 |
| 8 | TestBrowserSessions_LegacyMigration (subtests: keeps sign-in, survives saves and restarts, dated once, write failure, end legacy) | Integration (Go) | legacy scenarios | Q4 A, FR-085, FR-096 |
| 9 | TestBrowserSessions_ListEndSignOutOthers | Integration (Go) | list / End / sign-out-others scenarios, D-7 | three routes |
| 10 | TestBrowserSessions_PersonOnly | Integration (Go) | Only a signed-in person may use the browser list | 403 matrix |
| 11 | TestBrowserSessions_BearerNeverActsAsBrowser | Integration (Go) | A request with a bearer token never acts as a browser sign-in | FR-081 |
| 12 | TestBrowserSessions_EndClosesWebSockets | Integration (Go) | Every way a sign-in ends closes its live connections; Expiry closes an idle socket | six causes |
| 13 | TestBrowserSessions_AdmissionAfterEndRefused | Integration (Go) | A socket registration racing the end is refused | barrier |
| 14 | TestBrowserSessions_EndRevokesPreviewGrants | Integration (Go) | Every way a sign-in ends revokes its preview links; A preview mint racing the end is refused | three stores, six causes |
| 15 | TestLogout_EndsOnlyThisBrowser / TestChangePassword_EndsAllBrowsersAndTokens | Integration (Go) | Sign out / password change scenarios | Q2 A |
| 16 | TestGetConfig_NeverServesSessions | Integration (Go) | The configuration read never shows sign-ins | T8 |
| 17 | TestBrowserSessions_Audit | Integration (Go) | Sign-in events are audited | T10 |
| 18 | TestLogin_BearerSurvivesBrowserSignIn | Integration (Go) | A REST sign-in's app token survives a browser sign-in | #1595 |
| 19 | TestAuthLimiters_IsolatedPerTest | Unit (Go) | Eleven sign-ins in one test do not trip a shared limiter | #598 |
| 20 | signInSection.test | Unit (Vitest) | list scenarios | — |
| 21 | chatSignedOutDraft.test | Unit (Vitest) | An ended browser's unsent message is not lost silently | #1146 |
| 22 | signed-in-browsers.spec.ts | E2E (Playwright) | rows marked stable | targeted file only |

### Test Datasets

#### Dataset D-5: Cap and eviction

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0 live, new sign-in | Empty | 1 live, none ended | BDD Scenario: The 11th sign-in ends the one used longest ago | |
| 2 | 9 live, new sign-in | Max − 1 | 10 live, none ended | same | |
| 3 | 10 live, last uses t1 < t2 < … < t10, new sign-in | Max + 1 | the t1 sign-in ended; 10 live | same | S-Q14 A |
| 4 | 10 live, t3 oldest use though signed in latest | Order by use, not by age | the t3 sign-in ended | same | "used longest ago", not "oldest" |
| 5 | 10 live, two share the oldest last use, different sign-in times | Tie | of those two, the one signed in earlier ended | same | FR-078 tie rule |
| 6 | 10 live, 2 of them expired | Expired present | the 2 expired pruned, none live ended; 9 live | same | expiry before eviction |
| 7 | 10 live, current browser is the least used | Self | that sign-in is ended | same | no exemption |
| 8 | crossed fixture of the restart scenario (A in 09:00 / used 11:55; B in 10:00 / used 10:00), restart, 11th sign-in | Restart | B ended, A kept | BDD Scenario: "Used longest ago" holds across a restart | pending FQ-4 |
| 9 | 10 live, two share last use and sign-in time | Full tie | the one with the lexically smaller id ended | BDD Scenario: The 11th sign-in ends the one used longest ago | deterministic choice for the test |

#### Dataset D-6: Sign-in lifetime (24 h)

| # | Input (age at request) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0 s | Min | 200 | BDD Scenario: A sign-in older than its lifetime is refused | |
| 2 | 23 h 59 m 59 s | Max − 1 s | 200 | same | |
| 3 | 24 h 0 m 0 s | Max | 401 | same | exactly 24 hours old is refused |
| 4 | 3 days | Far past | 401 | same | wireframe sample row impossible (FQ-3) |
| 5 | migrated legacy, no issue time, restarted after 2 h | Missing field | `issued_at` = first start; 401 from first start + 24 h | BDD Scenario: A legacy entry with no issue time is dated once | not renewed (MAJ-015) |
| 6 | issued 1 h in the future (clock went back) | Clock skew | 200; expires at issued + 24 h | same | parked risk, behaviour stated |
| 7 | valid id, wrong secret | Wrong secret | 401 | BDD Scenario: A sign-in older than its lifetime is refused | one compare |
| 8 | `notacookie` (no dot, not legacy) | Malformed | 401, zero compares when no legacy entry exists | same | |
| 9 | 23 h 59 m 59 s at auth start, 24 h at registration (barrier) | Expiry during auth | socket refused at registration | BDD Scenario: A socket registration racing the end is refused | FR-095 |

#### Dataset D-7: `DELETE /auth/browser-sessions/{id}`

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | id of another live sign-in | Happy | 204; that sign-in refused next | BDD Scenario: Ending one sign-in by id | |
| 2 | id of the caller's own sign-in | Self | 409; nothing ended | same | use Sign out |
| 3 | well-formed unknown id | Unknown | 404 | same | |
| 4 | id ended a moment ago | Already ended | 404 | same | same body as row 3 |
| 5 | id of an expired sign-in | Expired | 404 | same | same body as row 3 |
| 6 | `abc` (fails the pattern) | Malformed | 404 | same | same body as row 3 (T7) |
| 7 | 16 chars with `/` encoded | Special chars | 404 | same | no path traversal |
| 8 | id of another live sign-in, caller is a bearer token only | Bearer caller | 204 | same | no current sign-in to protect; no CSRF (bearer) |
| 9 | cookie caller, no `X-CSRF-Token` | Missing CSRF | 403; sign-in still live | BDD Scenario: Ending a sign-in without the CSRF header is refused | |
| 10 | cookie caller, mismatched `X-CSRF-Token` | Wrong CSRF | 403; sign-in still live | same | |
| 11 | cookie + bearer of the same account, no CSRF | Mixed | 204 for another id (treated as bearer); 409 never, since no current browser | BDD Scenario: A request with a bearer token never acts as a browser sign-in | |

#### Dataset D-14: Duplicate-cookie recovery (`genuineSessionResend` over the set)

| # | Input (cookies, in header order) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | genuine `<idA>.<sA>` then planted `<idA>.<x>` | Same id, plant second | clear set sent; genuine re-sent with its original expiry; mutation answers `planted_cookie_cleared`; ≤ 2 compares | BDD Scenario: Duplicate-cookie recovery verifies by secret, within its budget | |
| 2 | planted `<idA>.<x>` then genuine `<idA>.<sA>` | Same id, plant first | same as row 1 | same | order must not matter |
| 3 | `<idA>.<sA>` and `<idB>.<sB>`, both valid | Two valid | clear only; nothing re-sent | same | ambiguity refused (ADR-094) |
| 4 | legacy unprefixed genuine + planted `<idA>.<x>` | Legacy plus new | legacy re-sent; ≤ 2 compares | same | |
| 5 | 5 distinct candidates, genuine last | Over budget | clear only after 4 compares; nothing re-sent | same | `plantedResendMaxComparisons` |
| 6 | genuine whose sign-in expires between the two compares (controlled clock) | Expiry during verify | clear only; nothing re-sent | same | |
| 7 | two copies of the same genuine value | Exact duplicate | today's rule: treated as one candidate | same | unchanged |

#### Dataset D-15: Cross-site and same-site requests with the cookie

| # | Input (cookie, Fetch Metadata, route kind) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | new cookie, `Sec-Fetch-Site: cross-site`, `Sec-Fetch-Mode: no-cors`, authenticated GET | Cross-site subresource | refused before any compare; no re-send | BDD Scenario: The cross-site read defense covers new and legacy cookies | `IsCrossSiteSubresourceRequest` |
| 2 | legacy cookie, same headers | Cross-site legacy | same as row 1 | same | |
| 3 | new cookie, same headers, optional-auth route | Optional auth | treated as anonymous | same | |
| 4 | new cookie plus planted duplicate, same headers | Cross-site recovery | no recovery, no re-send | same | |
| 5 | new cookie, `Sec-Fetch-Site: same-origin` | Positive control | 200 | same | the check can pass |
| 6 | new cookie, `Sec-Fetch-Site: cross-site`, `Sec-Fetch-Mode: navigate`, top-level GET | Top-level navigation | today's behaviour (allowed) | same | unchanged |
| 7 | new cookie, no Fetch Metadata headers | Old browser | today's behaviour (SameSite=Strict stands) | same | |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Login rate limit 5 failures / 15 min | existing `rest_auth` limiter tests | No — must stay green with per-test limiters | #598 |
| Bearer tokens capped at 10; logout revokes the presented one | existing SEC-1 tests | No | |
| Password change clears `tokens` and `token_hash` | existing change-password tests | Yes — also asserts every browser sign-in ends | |
| Logout revokes the caller's Library, mail and attachment grants | existing logout preview tests (ADR-067 FR-003d) | Yes — re-keyed by sign-in id; still green | |
| Onboarding issues a session cookie | `rest_onboarding_cookie_test.go` | Yes — assert a `sessions` entry instead of `session_token_hash` | oracle changes by design |
| Platform sign-in callback and claim | `rest_platform_auth` tests | Yes — the claim re-sends the same sign-in, no new entry | |
| Planted-cookie guard | ADR-094 FR-015 tests in `planted_cookie_test.go` | Yes — D-14 | HIGH |
| Cross-site subresource refusal | `session_cookie` tests | Yes — D-15 | HIGH |
| Live-browser socket close detaches the WebRTC viewer | `browser_ws` tests | No — new end path must reach `readLoop` cleanup; covered by order 12 | |

---

## Functional Requirements

- **FR-074**: The Sign-in section and its dialogs MUST say "browser" and "sign-in", never "session" alone: the End confirm button reads "End sign-in", its title "End this browser's sign-in?", and the bulk confirm "Sign out other browsers" (review MIN-002).
- **FR-076**: Each sign-in MUST store only the six fields of C-1 Storage; `label` comes from a fixed browser and system table (Ambiguity Warning 1), "Unknown browser" otherwise, and is set at sign-in (for the legacy entry, at its first request after migration, persisted with the next last-use flush). No IP address and no raw User-Agent may be stored, logged in the list or served.
- **FR-077**: The cookie value MUST be `<id>.<secret>`, `id` matching `^[A-Za-z0-9_-]{16}$`. An ordinary request with one session cookie MUST cost at most one bcrypt comparison (lookup by id, then compare the secret); a legacy cookie (no id prefix) is compared only with the entry marked `legacy: true`, and a cookie of neither form costs no comparison.
- **FR-078**: At most 10 sign-ins per account. A new sign-in MUST first prune expired entries, then, if 10 remain, end the one with the oldest last use (tie: earlier `issued_at`; full tie: smaller id), all inside the one serialised config write so concurrent sign-ins never exceed 10 (D-5).
- **FR-079**: The server MUST refuse a sign-in whose age is 24 hours or more, whatever the browser sends (D-6): 401 on authenticated routes, anonymous on optional-auth routes.
- **FR-080**: The three endpoints MUST exist as in C-1, behind `withAuth` and `middleware.RequirePersonSession` (CLI token, dev bypass, legacy env token → 403; none → 401). The list orders the current sign-in first, then by last use, newest first, and marks `current` only for the caller's own browser sign-in.
- **FR-081**: The CSRF rule follows the credential that authenticated the request. A cookie-authenticated DELETE or POST MUST carry the matching double-submit `X-CSRF-Token` (missing or mismatched → 403, nothing ended). A request with `Authorization: Bearer` is a bearer request (CSRF-exempt, as `CSRFMiddleware` does today) and MUST NOT be treated as any browser sign-in, even when it also carries a cookie: `current` is false everywhere and `sign-out-others` answers 409. The cookie stays `HttpOnly`, `SameSite=Strict`, and `Secure` over HTTPS.
- **FR-082**: `DELETE /auth/browser-sessions/{id}` MUST answer the same 404 body for unknown, malformed, ended and expired ids; 409 for the caller's own sign-in; 204 otherwise (D-7). `sign-out-others` answers 409 when the request is not authenticated by a browser sign-in, else 200 `{ended: n}`.
- **FR-083**: A password change MUST end every browser sign-in of the account, the changing browser included, and keep clearing every app token as `HandleChangePassword` does today.
- **FR-084**: Every end cause (End, sign-out-others, Sign out, password change, eviction, expiry) MUST close that sign-in's chat and live-browser WebSockets within 5 seconds, through the existing close path (so `browser_ws.go::readLoop` still detaches the WebRTC viewer); reconnects are refused. Expiry MUST close an idle socket with no new request (a sweep at least every 5 s, or a per-socket deadline). An agent's running turn is not stopped (ADR-082).
- **FR-085**: On the first config load after the update, an existing `session_token_hash` MUST become one entry with `legacy: true` as in C-1 Migration, written once; nobody is signed out by the update (Q4 A).
- **FR-086**: The planted-cookie guard (`middleware/planted_cookie.go::genuineSessionResend`, ADR-094 FR-015) MUST keep its behaviour over the set: verify distinct candidates by their secrets, never by the id; re-send only the single verified genuine cookie with its own original expiry; send the clear set only (no re-send) when none or more than one verifies (D-14).
- **FR-087**: `POST /auth/logout` MUST end only the caller's sign-in and its preview grants. Ending browsers MUST NOT touch app (bearer) tokens. A platform-mode sign-in joins the set like a local one; the desktop app's claim step re-sends the same sign-in and adds no entry.
- **FR-088**: Sign-in, End, sign-out-others, Sign out, eviction and expiry MUST each write one audit entry naming the event, the account and the sign-in id; no entry may contain a cookie value or hash.
- **FR-089**: `GET /config` MUST never serve `sessions` (nor any bcrypt hash) under any user; `gateway.users` stays in `blockedPaths` for writes.
- **FR-090**: The Sign-in section MUST show one row per live sign-in (label, device icon, "Signed in" and "Last used" as relative times), "This browser" on the current one with no End button, an "End <label>" button on the others with the FR-074 confirm, "Sign out other browsers…" with a confirm that says "You stay signed in here", the line "Up to 10 browsers stay signed in at once. Signing in on an 11th ends the one used longest ago.", the Empty state "No other browsers are signed in", "That browser was already signed out." after a 404, and an error Callout with Retry when the list fails.
- **FR-091** *(pending FQ-4, written for A)*: Last use MUST be updated in memory on every authenticated request of that sign-in and flushed to `last_used_at` in `config.json` at most once every 10 minutes per sign-in (batched into one write) and at graceful shutdown. Eviction uses the in-memory value while running and the stored value after a restart, so "used longest ago" holds across a restart to within 10 minutes. If FQ-4 is answered B, last use stays in memory only and the limit line and docs say that after a restart the earliest sign-in is ended first.
- **FR-092**: Every preview, mail-preview and attachment grant minted by a cookie-authenticated request MUST be keyed by that sign-in's id (`browser:<id>`); every end cause MUST revoke all three kinds for that id and none for other ids; a fresh request with a revoked grant MUST be refused as an unknown grant is today.
- **FR-093**: The duplicate-cookie recovery path MUST stay within `plantedResendMaxComparisons` (4) comparisons per request, separate from the one-comparison budget of FR-077; on budget exhaustion it sends the clear set only.
- **FR-094**: The Fetch Metadata cross-site subresource refusal (`IsCrossSiteSubresourceRequest`) MUST run before any cookie verification for new and legacy cookies, on mandatory- and optional-auth routes and in recovery; a refused request causes no mutation and no cookie re-send (D-15).
- **FR-095**: Ending a sign-in MUST be ordered as in C-1 ("Ending is ordered"): a request, socket registration or preview mint that resolved the sign-in before its end re-checks it under the registry lock before it registers or publishes, and is refused if it ended.
- **FR-096**: The legacy entry MUST be marked with `legacy: true`, keep its id and `issued_at` across saves and restarts, accept only the unprefixed cookie, and end like any entry; a failed migration write MUST NOT sign anybody out (C-1 Migration).
- **FR-097**: When a browser's sign-in has ended, its next action MUST lead to the sign-in screen with a notice that it was signed out, and an unsent chat message MUST either be restored in the same chat after signing in again or be named as not sent in that notice (#1146).
- **FR-098**: Each Go test that exercises auth routes MUST get fresh rate limiters through a test fixture that resets the package-level limiters (`globalLoginLimiter` and its siblings) in `t.Cleanup`; production limiters stay as they are (#598, fixture isolation chosen over moving the limiters).

---

## Riders

| Issue | Title (short) | How this package handles it | Acceptance | Test | Docs |
|---|---|---|---|---|---|
| #689 | Web login is single-slot | Closed by this package | FR-076 to FR-091 | orders 1–17 | DOC-009 |
| #1595 | Browser sign-in seems to end an API session | Closed by FR-087 | A REST sign-in's app token survives a browser sign-in | order 18 | DOC-009 |
| #1146 | Ended sign-in loses the unsent message | Carried | FR-097 | order 21; G-22 | DOC-009 |
| #598 | Auth rate limiters are package globals | Carried, fixture isolation only | FR-098 | order 19 | — (no user-visible effect) |

---

## Success Criteria

- **SC-007**: With 10 browsers signed in, all 10 answer 200; the 11th sign-in ends exactly the D-5 target; 1 bcrypt comparison per ordinary request; ended sockets close within 5 s in 6 of 6 end causes.
- **SC-008**: 0 people signed out by the update in the migration test (G-21).
- **SC-010**: 0 preview, mail or attachment grants of an ended sign-in still serve, across 6 end causes and 3 grant kinds (18 cases); other browsers' grants keep serving.
- **SC-011**: 0 sockets admitted after an end in the barrier tests; 0 cross-site subresource requests verified (D-15 rows 1–4).

---

## Reachability

| Deliverable | How a person or agent reaches it | Check |
|---|---|---|
| Sign-in section | Settings → Sign-in (`src/routes/_app/settings.tsx`, `tab=signin`; `tab=devices` opens it again) | Playwright opens it from the account menu |
| Three endpoints | Called by the Sign-in section; registered with their middleware | route-table test |
| Multi-browser sign-in | Every sign-in path (login, onboarding, platform callback) | orders 1, 15 and the regression rows |
| Agent tools | None; no tool or policy change | `grep -rl 'browser-sessions' pkg/sysagent/ pkg/tools/` returns 0 by design |

Where tests run: RED (qa-lead) in `pkg/gateway`, `pkg/gateway/middleware`, `pkg/config`; Vitest next to the Sign-in section and the chat composer; one Playwright spec file. GREEN runs only these targeted tests through `heavy` under the own-tests rule. CHECK is a different qa-lead instance. Hands-on UAT on the exact cut commit is required (gateway WebSocket handlers).

---

## User-facing documentation

| ID | Page | TODO |
|---|---|---|
| DOC-009 | `docs/security.md` | Signing in on several browsers: up to 10, the 11th ends the one used longest ago, 24-hour sign-in, Sign out ends this browser only, password change ends every browser and app token, how to end a lost device's sign-in, that ending a browser also stops its preview links (but cannot take back what it downloaded), and that a stolen sign-in is ended by changing the password |
| DOC-014 | `docs/settings.md` | Add the Sign-in section to the section list and the old-tab map (`devices` → Sign-in) when this package lands |

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | DOC |
|---|---|---|---|---|
| FR-074 | US-8 | Ending another browser; End buttons are named for their browser | signInSection.test | DOC-009 |
| FR-076 | US-7 | The configuration read never shows sign-ins; The list shows every signed-in browser | TestBrowserSessions_ListEndSignOutOthers | DOC-009 |
| FR-077 | US-7 | One password-strength check per request | TestBrowserSessions_OneBcryptPerRequest | — (no user-visible change) |
| FR-078 | US-7 | The 11th sign-in ends the one used longest ago; Simultaneous sign-ins never exceed the cap | TestBrowserSessions_EvictsLeastRecentlyUsed (D-5) | DOC-009 |
| FR-079 | US-7 | A sign-in older than its lifetime is refused | TestBrowserSessions_ExpiryEnforcedServerSide (D-6) | DOC-009 |
| FR-080 | US-8 | Only a signed-in person may use the browser list; The list shows every signed-in browser | TestBrowserSessions_PersonOnly; TestBrowserSessions_ListEndSignOutOthers | DOC-009 |
| FR-081 | US-8 | Ending a sign-in without the CSRF header is refused; A request with a bearer token never acts as a browser sign-in | TestBrowserSessions_ListEndSignOutOthers (D-7 rows 9–11); TestBrowserSessions_BearerNeverActsAsBrowser | — (no user-visible change) |
| FR-082 | US-8 | Ending one sign-in by id; Sign out other browsers needs a browser sign-in | TestBrowserSessions_ListEndSignOutOthers (D-7) | DOC-009 |
| FR-083 | US-7 | A password change ends every sign-in | TestChangePassword_EndsAllBrowsersAndTokens | DOC-009 |
| FR-084 | US-7 | Ending a sign-in closes its live connection; Every way a sign-in ends closes its live connections; Expiry closes an idle socket; An agent's running turn continues | TestBrowserSessions_EndClosesWebSockets; G-22 | DOC-009 |
| FR-085 | US-7 | The sign-in from before the update keeps working | TestBrowserSessions_LegacyMigration; G-21 | DOC-009 |
| FR-086 | US-7 | The planted-cookie protection still works with several sign-ins; Duplicate-cookie recovery verifies by secret | TestPlantedCookieGuard_WithSessionSet (D-14) | — (no user-visible change) |
| FR-087 | US-7 | Sign out ends only this browser; App tokens are not touched; A platform-mode sign-in joins the set | TestLogout_EndsOnlyThisBrowser; TestBrowserSessions_SecondSignInKeepsFirst | DOC-009 |
| FR-088 | US-7 | Sign-in events are audited | TestBrowserSessions_Audit | — (audit log only) |
| FR-089 | US-7 | The configuration read never shows sign-ins | TestGetConfig_NeverServesSessions | — (no user-visible change) |
| FR-090 | US-8 | list, End, Sign out other browsers, only-this-browser, already-gone, cannot-load scenarios | signInSection.test; G-23 | DOC-009, DOC-014 |
| FR-091 | US-7 | "Used longest ago" holds across a restart; A gateway restart keeps every sign-in | TestBrowserSessions_LastUseSurvivesRestart (D-5 row 8) | DOC-009 |
| FR-092 | US-7 | Every way a sign-in ends revokes its preview links; A preview mint racing the end is refused | TestBrowserSessions_EndRevokesPreviewGrants; G-27 | DOC-009 |
| FR-093 | US-7 | Duplicate-cookie recovery verifies by secret, within its budget | TestPlantedCookieGuard_WithSessionSet (D-14 row 5) | — (no user-visible change) |
| FR-094 | US-7 | The cross-site read defense covers new and legacy cookies | TestSessionCookie_CrossSiteSubresourceRefused (D-15) | — (no user-visible change) |
| FR-095 | US-7 | A socket registration racing the end is refused; A preview mint racing the end is refused | TestBrowserSessions_AdmissionAfterEndRefused; TestBrowserSessions_EndRevokesPreviewGrants | — (no user-visible change) |
| FR-096 | US-7 | The legacy sign-in survives saves and restarts; A legacy entry with no issue time is dated once; A failed migration write signs nobody out; Ending the legacy sign-in | TestBrowserSessions_LegacyMigration | DOC-009 |
| FR-097 | US-7 | An ended browser's unsent message is not lost silently | chatSignedOutDraft.test; G-22 | DOC-009 |
| FR-098 | US-7 | Eleven sign-ins in one test do not trip a shared limiter | TestAuthLimiters_IsolatedPerTest | — (test-only) |

Check: 24 FRs (FR-074, FR-076 to FR-098), each with a story, at least one scenario and one named test.

---

## Assumptions

- GitNexus was not connected; the three HIGH rows are **Inferred**. backend-lead runs `impact` on `ResolveUserFromCookie`, `genuineSessionResend` and `PreviewSessionKey` before editing.
- The product holds at most one human account (`GatewayConfig.Users` comment); the set is per account anyway.
- Socket close reaches the existing WebRTC cleanup (`browser_ws.go::readLoop` calls `detachWebRTCViewer`); verified by reading, not run.
- The 10-minute last-use precision is an architect proposal for FQ-4 A; any bound up to 15 minutes keeps the config-write rate low (one write per sign-in per bound).

## Clarifications

### 2026-11-20

- Q: Is #1096 in the Settings package? → A: no longer; cut at 05:08Z by the chief into this spec; founder answer pending (team-lead fix-round-1 brief).
- Q: How does a request find its sign-in among up to 10? → A: a public id before the secret; one bcrypt compare per ordinary request (FR-077).
- Q: What binds sockets, previews and audit to a browser? → A: the sign-in id, never the plaintext cookie (C-1 "One browser identity").
- Q: Which credential wins on a mixed request? → A: the bearer (FR-081).
- Q: How is #598 handled? → A: test-fixture isolation (FR-098).

## Questions for the founder

- **FQ-3 — How long a browser stays signed in.** A sign-in lasts 24 hours today; the wireframe's sample row "last used 3 days ago" cannot happen. **A (recommended):** keep 24 hours in this package and change the sample row. **B:** keep 24 hours here, and open a separate follow-up to consider longer sign-ins. Answer "FQ-3 A" or "FQ-3 B".
- **FQ-4 — Must "used longest ago" stay true after a gateway restart?** (review Q4) Keeping last use only in memory saves disk writes, but after a restart the 11th sign-in could end a browser you used five minutes ago instead of one unused since morning. **A (recommended, matches your S-Q14 A):** save last use to disk at most every 10 minutes, so the rule holds across a restart to within 10 minutes. **B:** accept that after a restart the earliest sign-in is ended first, and say so on screen and in the docs. Answer "FQ-4 A" or "FQ-4 B".

---

## Round-1 fix log

Findings from `docs/internal/specs/settings-redesign-spec-review.md` (round 1 of the Settings spec) that moved here with the [#1096] block.

| Finding | What changed | Section |
|---|---|---|
| CRIT-001 Preview grants outlive an ended browser | One browser identity (sign-in id) keys previews, mail and attachment grants; every end cause revokes them; racing mints refused; 18-case test; G-27; DOC-009 says downloaded content stays | C-1 "One browser identity", "Ending is ordered"; threat T1b; FR-092, FR-095; SC-010 |
| MAJ-011 One-bcrypt limit vs duplicate-cookie recovery | Split budgets: 1 compare for an ordinary request, recovery capped at 4; verification by secret, never by id; clear-only on ambiguity or exhaustion; D-14 order permutations | FR-077, FR-086, FR-093; Performance Bounds; D-14 |
| MAJ-015 Legacy cookie not durably represented | Sixth stored field `legacy: true`; one-time migration write; `issued_at` set once; write-failure path; legacy label persisted with the last-use flush | C-1 Storage and Migration; FR-085, FR-096; D-6 row 5; legacy scenarios; G-21 extended |
| MAJ-016 Restart resets eviction order | Last use flushed at most every 10 minutes (pending FQ-4 A; B fallback written into FR-091); crossed fixture | FR-091; D-5 rows 8–9; scenario "Used longest ago" holds across a restart; FQ-4 |
| MAJ-017 End vs in-flight socket admission (warning) | Ordered end under the registry lock; registration and mints re-check; barrier tests; expiry closes idle sockets | C-1 "Ending is ordered"; FR-084, FR-095; D-6 row 9 |
| MAJ-018 Cross-site read defense not covered | Fetch Metadata refusal required before every cookie path; CSRF rule by credential; bearer never acts as a browser | FR-081, FR-094; D-7 rows 9–11; D-15; threat T5b |
| MIN-002 "End session" wording | Confirm reads "End sign-in"; title "End this browser's sign-in?" | FR-074; scenario "Ending another browser" |
| Review Q3 / FQ-3 | Kept open, recommended A | Questions for the founder |
| Review Q4 | Added as FQ-4, recommended A | Questions for the founder |
| Review source clarification: password change and app tokens | FR-083 now states that app tokens are cleared too (today's `HandleChangePassword`) | FR-083; G-24 |
| MAJ-013 (#1146, #598 parts) | Riders get acceptance, test and docs rows | Riders; FR-097, FR-098 |
