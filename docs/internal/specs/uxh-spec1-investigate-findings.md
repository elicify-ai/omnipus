# Spec-1 Investigate Findings — #358 WhatsApp QR, #359 Session Expiry

**Date:** 2026-06-04
**Branch:** feat/s1-investigate
**Scope:** US-8 (WhatsApp QR) and US-9 (session TTL) from uxh-spec1-showstoppers-and-registry.md

---

## Issue #358 — WhatsApp QR Does Not Appear After Enable & Save

### Root Cause (DEEP — recommend deferral + tracked issue)

The QR never appears because the WhatsApp channel is never **started** when the
operator clicks "Enable & Save" in the Configure panel.

**Code path trace:**

1. SPA `doSaveAndEnable` mutation (`ChannelConfigPanel.tsx:611-627`) calls:
   - `configureChannel(channelId, payload)` → `PUT /api/v1/channels/whatsapp/configure`
   - `enableChannel(channelId)` → `PUT /api/v1/channels/whatsapp/enable`

2. `setChannelEnabled` (`pkg/gateway/rest.go:4554`) calls `safeUpdateConfigJSON`
   which writes `channels.whatsapp.enabled = true` to `config.json` and then calls
   `refreshConfigAndRewireServices` (`pkg/gateway/rest.go:2183`).

3. `refreshConfigAndRewireServices` calls `agentLoop.SwapConfig(newCfg)` — this
   **only swaps the in-memory config pointer**. It does NOT call `TriggerReload`
   and does NOT call `ChannelManager.Reload`.

4. `ChannelManager.Reload` is only called from the gateway main loop's
   `handleConfigReload` path (`pkg/gateway/gateway.go:1631`), reached via:
   - A manual `TriggerReload` → `manualReloadChan` signal (not sent by
     `setChannelEnabled`), OR
   - The config file watcher polling every 2 seconds (only active when
     `cfg.Gateway.HotReload == true`, default is `false` per `defaults.go:363`).

5. `channel.Start()` is called by `Manager.StartAll` (at initial boot,
   `pkg/channels/manager.go:924`) and by `Manager.Reload` (the diff path,
   `pkg/channels/manager.go:1699+`). Since neither is triggered by
   `setChannelEnabled`, the WhatsApp channel object is never started.

6. `WhatsAppNativeChannel.Start` (`pkg/channels/whatsapp_native/whatsapp_native.go:189`)
   is where `client.GetQRChannel` is called and the QR background goroutine is
   spun up. Without `Start`, no QR loop runs, `emitPairing` is never called, and
   no `whatsapp_pairing` WS frame is ever sent to the SPA.

7. The `WhatsAppNativeNotice` component (`src/components/skills/ChannelConfigPanel.tsx:264`)
   subscribes via `whatsapp_pairing_subscribe` on mount, but receives nothing
   because the backend emits no events — the channel has not started.

**Additional compounding issue (SPA UX):**

`doSaveAndEnable` calls `onOpenChange(false)` on success (`ChannelConfigPanel.tsx:621`),
which closes the panel. Even if the backend did start the channel and emit a QR
frame, the `WhatsAppNativeNotice` is unmounted on success because the panel closes.
The operator never sees the QR in the "Save & Enable" path.

When using "Save" only (not "Save & Enable"), the panel stays open, but the channel
is still not started for the same reason above.

**Observer wiring is correct once the channel runs:**

`Manager.SetPairingObserver` (`pkg/channels/manager.go:147`) is called at boot
(`pkg/gateway/gateway.go:1039`) and injects the WS forwarder callback into any
channel that implements `PairingObservable`. The wiring is correct — the problem
is that the channel never reaches `Start()`, so the observer is never invoked.

### Fix Recommendation (deep — do not implement in Spec-1)

Two separate sub-fixes are needed:

**Sub-fix A (backend):** `setChannelEnabled` should call `TriggerReload` (same
pattern used by agent-create, token-rotate, and other handlers at
`pkg/gateway/rest.go:1369, 1798, 2817, 3575, 4152`) after writing the config so
that the gateway main loop fires `ChannelManager.Reload` which starts the newly
enabled channel.

**Sub-fix B (SPA UX):** For WhatsApp, "Save & Enable" must NOT close the panel on
success — it should keep it open so the QR can render. The current
`onOpenChange(false)` call in `doSaveAndEnable.onSuccess` is correct for all other
channels (no pairing flow) but wrong for WhatsApp. Either: (a) add a
`keepOpenAfterEnable` prop to `ChannelConfigPanel` for channels with a pairing
flow, or (b) have the WS `whatsapp_pairing` frame itself open the panel
(complex). Option (a) is simpler.

**Security ACs from FR-111 ride to the follow-up** per spec: WS frame must be
delivered only to authenticated admin sessions; consumed QR not re-served; pairing
events audit-logged.

### Proposed GitHub Issue

**Title:** `fix(whatsapp): QR pairing never appears — channel not started on enable, panel closes before QR renders`

**Body:**
```
## Root cause

Two bugs combine to prevent the WhatsApp QR from ever appearing:

**Bug 1 (backend):** `setChannelEnabled` (`pkg/gateway/rest.go:4554`) calls
`safeUpdateConfigJSON` which only swaps the in-memory config pointer via
`refreshConfigAndRewireServices`. It does NOT call `TriggerReload`, so
`ChannelManager.Reload` is never triggered, `channel.Start()` is never called,
and the `GetQRChannel` goroutine in `whatsapp_native.go:263-297` never runs.
No QR is emitted. The `SetPairingObserver` wiring at `gateway.go:1039` is
correct but unreachable without `Start`.

Fix: add `a.agentLoop.TriggerReload()` to `setChannelEnabled` after the
`safeUpdateConfigJSON` call (same pattern as `HandleCreateAgent` at line 1369).

**Bug 2 (SPA):** `doSaveAndEnable.onSuccess` calls `onOpenChange(false)`
(`ChannelConfigPanel.tsx:621`), closing the panel before any QR frame can arrive.
`WhatsAppNativeNotice` is unmounted and clears its subscription.

Fix: for WhatsApp, keep the panel open after "Save & Enable" so the QR can
render. Add a `keepOpenOnEnable?: boolean` prop to `ChannelConfigPanel` (driven by
whether `channelId === 'whatsapp'`), and skip the `onOpenChange(false)` call when
true.

## Security ACs (FR-111)

Once fixed: (a) `whatsapp_pairing` WS frames must only reach authenticated admin
sessions (already gated by `authenticateWS`); (b) consumed/expired QR not
re-served (whatsmeow handles; `clear('whatsapp_native')` in the SPA handles
client-side); (c) pairing events must be audit-logged (add `audit.Log` call in
`emitPairing` for `PairingStatusLinked` and `PairingStatusError`).

## Effort

Medium (2–4 hours). Backend fix is shallow (one `TriggerReload` call). SPA fix
needs a prop + conditional. Audit logging is additive.
```

---

## Issue #359 — Session Expires After ~2-3 Minutes

### Root Cause (INCONCLUSIVE from code-trace alone — recommend deferral)

**What was ruled out:**

| Candidate | Status | Evidence |
|---|---|---|
| Session cookie MaxAge | NOT the cause | `SessionCookieMaxAge = 86400` (24h), `middleware/session_cookie.go:78` |
| CSRF token TTL | NOT the cause | CSRF cookie has **no MaxAge** (session cookie, lives until browser close), `csrf.go:404-413` |
| Backend token hash TTL | NOT the cause | `BcryptHash.Verify` has no expiry — compares hash only, `pkg/config/bcrypt.go:40-49` |
| `expires_at` in `api.ts:725,752` | NOT auth-related | These fields are on `ServeWorkspaceResult` / `RunInWorkspaceResult` (iframe preview tokens), not on auth responses |
| WS read deadline (60s) | NOT the cause | `readLoop` resets deadline on every frame (`websocket.go:609`); SPA heartbeat ping fires every 30s (`ws.ts:901`) — deadline is never hit under normal conditions |
| Client-side auth timer | NOT FOUND | No `setInterval`/`setTimeout` in auth paths; `useVersionCheck` polls `/api/v1/version` every 60s but does not affect auth |
| Global queryClient 401 handler | NOT the cause | `queryClient.ts` has no global error handler that clears auth on 401 |

**The likely mechanism (not fully reproduced from code-trace):**

The `_app.tsx:beforeLoad` route guard (`src/routes/_app.tsx:9-36`) calls
`validateToken()` (a raw `fetch` call, not TanStack Query-cached) on **every
TanStack Router route transition**. A 401 from `/api/v1/auth/validate` clears the
bearer token from `sessionStorage` and redirects to `/login`.

`HandleLogin` (`pkg/gateway/rest_auth.go:305-407`) **rotates the bearer token on
every login call** — it generates a new token and overwrites `token_hash` in
`config.json`. This means a second browser tab logging in would immediately
invalidate the first tab's `sessionStorage` token.

The bearer token is stored in `sessionStorage` (tab-scoped) and `localStorage`
(persistent fallback). If the `sessionStorage` token differs from the current
`token_hash` in the gateway's in-memory config (e.g. because a concurrent login
rotated it), `validateToken()` gets a 401 and the user is kicked out.

**The ~2-3 minute window** could not be reproduced deterministically from code
alone. Possible causes not yet confirmed:

1. **Token rotation race**: a background reload (triggered by any `safeUpdateConfigJSON`
   call from another handler) re-reads `config.json` and briefly produces a
   `cfg` snapshot where `token_hash` has not yet been flushed to disk. This window
   is theoretically closed by `safeUpdateConfigJSON`'s synchronous
   `refreshConfigAndRewireServices` call, but could be exposed under concurrent
   writes.

2. **SPA tab-switch behavior**: TanStack Router may re-run `beforeLoad` on
   tab visibility change or SPA re-mount, triggering `validateToken()`. Combined
   with any interim config write (from any settings save), this could produce a
   401 if the gateway briefly has an old in-memory config.

3. **Multi-tab concurrent login**: if the user (or onboarding flow) triggers a
   second login, the new `token_hash` invalidates the existing `sessionStorage`
   token within the configurable `awaitReload` 100ms window.

### Confirmed Absence: CSRF is not the cause

The CSRF middleware (`csrf.go:259-368`) has no TTL — it double-submit-checks the
cookie value against the header on every state-changing request but never expires
or rotates the cookie on its own. The CSRF cookie is a session cookie (no MaxAge),
so it lives as long as the browser tab. The only time a new CSRF cookie is issued
is on `/auth/login`, `/auth/register-admin`, and `/onboarding/complete`.

### Fix Recommendation (deep — do not implement in Spec-1)

The pre-requisite investigation revealed that the expiry mechanism is likely
client-side and depends on runtime behaviour (tab switching, concurrent logins,
TanStack Router re-runs). A deterministic fix requires:

1. Identifying whether `_app.tsx:beforeLoad` is re-run on window focus/tab
   switch in the specific TanStack Router version in use (verify in Playwright).
2. Adding a short-lived `validateToken` result cache (e.g., `staleTime: 30_000`
   in a `useQuery`) to avoid hitting `/auth/validate` on every route transition.
3. If the cause is multi-tab token rotation: implement single-session tokens
   (last-write-wins is the current design) or switch to session-cookie-only auth
   (already issued alongside the bearer token via `WriteSessionCookie` at
   `rest_auth.go:388`) for the WS and API paths.

The session cookie path (`middleware/session_cookie.go:420-488`) is already wired
for HTTP requests via `RequireSessionCookieOrBearer`. The WS auth
(`websocket.go:485-568`) only checks the bearer token — it does not check the
session cookie. Switching the WS to accept the session cookie (or passing it
during the upgrade handshake) would make the WS immune to bearer-token rotation.

### Proposed GitHub Issue

**Title:** `fix(auth): ~2-3 min spurious session expiry — validateToken called on every route, bearer token rotated on each login`

**Body:**
```
## Symptom

Users are logged out after ~2-3 minutes without interaction. The session cookie
(24h) and CSRF cookie (no TTL) are not the cause.

## Root cause (partially identified — needs runtime confirmation)

`src/routes/_app.tsx:beforeLoad` calls `validateToken()` (raw fetch, uncached) on
every TanStack Router route transition. `HandleLogin` (`rest_auth.go:305`) rotates
the bearer token on every login, immediately invalidating any other tab's
`sessionStorage` token. If a concurrent login or a tab-switch triggers
`validateToken()` with a stale token, the guard clears auth and redirects to login.

The WS auth (`websocket.go:489`) also uses the bearer token and does NOT fall back
to the session cookie. A bearer-token rotation mid-session would not immediately
close the WS (auth runs once at connect time), but any subsequent HTTP API call
via `withAuth` that uses the now-stale bearer token would 401.

## Investigation gaps

- Whether `beforeLoad` re-runs on window focus in the specific TanStack Router
  version was not confirmed by code-trace alone.
- The ~2-3 minute interval could not be reproduced deterministically.

## Proposed fixes

1. Cache `validateToken()` result in TanStack Query with `staleTime: 30_000`
   instead of calling it as a raw fetch on every navigation.
2. Implement non-rotating bearer tokens (token stays valid until explicitly
   rotated via token-rotate endpoint) to eliminate the multi-tab invalidation.
3. Or: drop bearer token from WS/HTTP auth entirely and rely solely on the
   session cookie (already issued on login at `rest_auth.go:388`), which does
   not rotate per-login.

## Effort

Medium (3–6 hours). Fix 1 is the least invasive. Fix 2–3 require auth contract
changes.
```

---

## Summary

| Issue | Root Cause Found | Depth | Recommendation |
|---|---|---|---|
| #358 WhatsApp QR | Yes — `setChannelEnabled` never calls `TriggerReload`; SPA panel closes on enable | Deep (two coupled bugs) | File tracked issue; drop from Spec-1 |
| #359 Session expiry | Partial — bearer-token rotation + uncached `validateToken` on every route; ~2-3 min interval not reproduced deterministically | Deep | File tracked issue; drop from Spec-1 per spec default |

**No code fixes made** — both root causes are deep (require multi-file changes
across Go backend and SPA, with SPA changes outside backend-lead scope). Deferral
is recommended per FR-111 and FR-112 time-box rules in the spec.

**No tests added** — no shallow fix was implemented.
