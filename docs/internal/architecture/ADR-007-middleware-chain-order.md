# ADR-007: Gateway Middleware Chain Order

## Status

Accepted (2026-04-19) — codifies Sprint A PR-H wiring.

## Context

The Omnipus gateway applies several request-time concerns: CSRF enforcement, config snapshot injection (for consistent reads during hot-reload), bearer-token authentication, and admin RBAC. The order these run in determines both correctness (does a bad request short-circuit before we do expensive work?) and security (does auth or CSRF fail first on a hostile request?).

The original Sprint A plan in `temporal-puzzling-melody.md` §1 called for `auth → RBAC → CSRF → handler`. During implementation we inverted that order; this ADR records the rationale so future refactors don't "correct" it back.

## Decision

The middleware stack for the REST mux is, outermost first:

```
CSRF gate  →  configSnapshot injection  →  mux dispatch  →  withAuth (in handler)  →  RequireAdmin (in handler)
```

Source reference, `pkg/gateway/gateway.go:795-800`:

> "We place CSRF BEFORE the per-handler auth gate because (a) auth is currently inlined in withAuth / withOptionalAuth wrappers rather than a separate middleware, and splitting it would be substantial collateral damage for this PR; (b) failing fast on a bad cookie avoids wasting a bcrypt compare on obvious cross-origin forgeries."

Two reasons to put CSRF first:

1. **Fail fast.** A missing or mismatched CSRF cookie is cheap to detect (constant-time string compare). A failed bearer check is expensive (bcrypt compare over the stored admin hash). Letting bad-cookie requests skip bcrypt reduces CPU work under attack and narrows a timing-amplification surface.
2. **Practical constraint.** `withAuth` and `withOptionalAuth` are currently per-handler wrappers, not a chain-level middleware. Hoisting them into a top-level middleware is a larger refactor; the security property (unauthenticated/unauthorized requests are rejected before mutating state) is preserved regardless of order because every mutating handler calls one of the auth wrappers first.

## Consequences

- Attackers exploring the API without a valid cookie hit 403 at the CSRF gate before any authentication cost is paid.
- `configSnapshotMiddleware` wraps the mux but is INSIDE the CSRF gate, so snapshots are only taken for requests that will actually be dispatched.
- If a future PR extracts auth into a proper middleware, the order should become `CSRF → configSnapshot → withAuth → RequireAdmin → mux`. Until then, the per-handler wrappers are the source of truth for auth.
- Exempt paths in CSRF (onboarding/complete, login, register-admin) are the ONLY endpoints that bypass the gate; each of those handlers runs auth logic internally where applicable.

## References

- `pkg/gateway/gateway.go:795-800` (CSRF wrap comment)
- Issue #97, Issue #98
- Sprint B Plan 4 §F29 (this ADR)
- ADR-006 CSRF Double-Submit Cookie Pattern
