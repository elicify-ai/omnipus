# ADR-006: CSRF Double-Submit Cookie Pattern

## Status

Accepted (2026-04-19) — implemented in Sprint A PR-H and hardened in Sprint B (issue #97).

## Context

The Omnipus gateway exposes state-changing REST endpoints (agents, config, sessions, credentials) to a browser-hosted SPA. Without CSRF protection, an attacker origin could trick a user's browser into issuing credentialed POST/PUT/PATCH/DELETE requests that ride the user's bearer cookie, mutating gateway state.

Three mitigations were on the table:

1. **SameSite-only** — rely on `SameSite=Strict` cookies alone. Rejected: older browser coverage is uneven, and some in-scope flows (desktop Electron loading localhost) can produce SameSite-less contexts.
2. **Synchronizer-token (stateful)** — server stores a per-session token in the credential store or memory. Rejected: adds a write path on every GET (to rotate), complicates the file-based storage model (no server session store), and couples the middleware to the credential store lifecycle.
3. **Double-submit cookie (stateless)** — server issues a random token in a `__Host-csrf` cookie; client echoes it in `X-Csrf-Token` on every state-changing request; server does a constant-time compare.

## Decision

Use double-submit cookie. Wire captured in `pkg/gateway/middleware/csrf.go`:

- Cookie name: `__Host-csrf` (the prefix forces Secure + Path=/ + no Domain at the browser layer).
- Header name: `X-Csrf-Token` (Go's canonical MIME form; case-insensitive on the wire).
- Token: 32 random bytes from `crypto/rand`, base64-url-encoded, no padding (43 chars).
- Compare: `crypto/subtle.ConstantTimeCompare` to avoid timing leaks.
- Gated methods: POST, PUT, PATCH, DELETE. GET/HEAD/OPTIONS pass through.
- Fail-closed: missing cookie → 403; missing header → 403; mismatch → 403 + audit log entry via optional `WithReporter`.
- Exempt paths: the cookie-issuer endpoints (`/api/v1/onboarding/complete`, `/api/v1/auth/login`, `/api/v1/auth/register-admin`) and operational probes (`/health`, `/ready`, `/reload`). Issuer endpoints call `IssueCSRFCookie` on success to seed the client.

Configuration is expressed via functional options (`WithExemptPath`, `WithReporter`, `WithClientIPFunc`, `WithDefaultExempts`). Callers cannot mutate the resolved exempt set after construction — the constructor deep-copies into a private map.

## Consequences

- Stateless: no server-side token store, no write path on read requests.
- SPA must read `document.cookie` to echo the token — hence `HttpOnly=false`. Exfiltration risk is bounded by the same-origin policy on the cookie.
- First requests from a fresh install flow through the three exempt bootstrap endpoints, which issue the cookie on their 200 responses; subsequent requests are gated.
- Operator tooling hitting `/reload` via curl (no browser, no cookies attached) is unaffected because `/reload` is exempt.

## References

- Issue #97 (Sprint A CSRF wiring), Sprint B Plan 4 §F18/F25/F26
- `pkg/gateway/middleware/csrf.go`, `pkg/gateway/middleware/csrf_test.go`
- OWASP CSRF Prevention Cheat Sheet
- MDN `__Host-` cookie prefix documentation
