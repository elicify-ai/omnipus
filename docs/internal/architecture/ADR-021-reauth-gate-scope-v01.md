# ADR-021 — Re-auth Consent Gate Scope for v0.1.0

**Status:** Accepted
**Date:** 2026-06-14
**Deciders:** backend-lead, architect

---

## Context

The re-auth consent primitive (FR-12.2 / FR-6.6 / FR-3.3, Spec-3 / Spec-6) requires
the single user to re-type their password before a sensitive settings change. The
flow is: `POST /api/v1/auth/reauth` re-verifies the password and mints a short-lived
(5-minute), single-use consent token (`reAuthTokenTTL`,
`pkg/gateway/rest_integrations_auth.go`); the SPA replays it in the `X-Reauth-Token`
header on the immediately-following mutation; the handler calls
`restAPI.requireReAuth`, which consumes the token (single-use) or returns **403**.

The spec names "settings" generically (FR-12.2 / SC-7) without enumerating every
mutating endpoint. That leaves an open question: which of the gateway's many admin
mutations must carry the consent token in v0.1.0, and which may rely on the existing
auth + admin-role gate alone? This ADR records the v0.1.0 scope decision so the
boundary is explicit, testable, and auditable — rather than implicit in the handler
code.

A coverage review of the gated handlers found two gaps now closed by test, plus a TTL
path that was previously untested:

- Negative (no-token → 403) tests existed for performance, sandbox-config, and
  tool-policies, but **not** for the two highest-blast-radius gated routes: the
  model/provider API-key PUT (`HandleProviders` PUT) and the agent tool-grant PUT
  (`updateAgentTools`). Both were covered only on the happy path.
- No test exercised the consent token's 5-minute TTL / `pruneLocked` expiry sweep —
  only single-use consumption and the `expires_in` response field.

Both gaps are addressed by tests in `pkg/gateway/reauth_gate_test.go` (negative
no-token assertions for both routes; deterministic TTL-expiry and prune assertions
driven by backdating an entry's `expiresAt`, with no change to production behavior).

## Decision

In **v0.1.0** the re-auth consent gate (`requireReAuth`) covers the following sensitive
settings mutations:

| Area | Endpoint / handler | Spec |
| --- | --- | --- |
| Integrations | `handleIntegrationProviderUpdate` (search/voice provider keys) | FR-12.2 |
| Model / provider keys | `HandleProviders` PUT (`/api/v1/providers/{id}`) | FR-12.2 / FR-6.6 |
| Performance | `HandlePerformance` PUT (`max_parallel_agents`) | FR-6.6 / FR-12.2 |
| Sandbox config | `HandleSandboxConfig` PUT | FR-12.2 |
| Tool policies (global) | `HandleToolPolicies` PUT | FR-3.3 / FR-12.2 |
| Agent tool grants | `updateAgentTools` PUT (`/api/v1/agents/{id}/tools`) | FR-3.3 / FR-12.2 |

Each gated route has both a positive (valid token → success) and a negative
(no token → 403) test in `pkg/gateway/reauth_gate_test.go`. The negative tests are a
deliberate tripwire: removing the gate from any of these routes flips a test red in CI.

The gate **deliberately does NOT yet cover** the following mutations in v0.1.0:

- **Credential-vault write/delete** — `setCredential` / `deleteCredential`.
- **Channel-secret configuration** — `configureChannel` (token/secret/password/key
  fields routed to the credential store).
- **User-management mutations** — user create / delete / role change / password reset.

### Rationale for the v0.1.0 exclusions

1. **Already admin + auth gated (defense-in-depth).** Each excluded mutation is behind
   `withAuth` and an admin-role check. The re-auth gate is an *additional* consent
   layer, not the sole protection; its absence does not leave these routes
   unauthenticated.
2. **Spec wording is generic, not enumerative.** FR-12.2 / SC-7 say "settings" without
   listing endpoints, so extending the gate to every mutation is an interpretation
   choice, not a literal requirement. v0.1.0 applies it to the settings surfaces the
   SPA presents as a single "Settings" experience plus the two highest-blast-radius
   capability/credential writes reachable from it.
3. **v0.1.0 is single-user / one-password by default.** The threat the consent token
   mitigates (an unattended authenticated session being used to silently change
   sensitive settings) is bounded in a single-operator deployment. The marginal value
   of gating credential-vault and user-management mutations is real but lower-priority
   than shipping the v0.1.0 foundation.

## Consequences

**Positive**

- The gate boundary is explicit and enumerated, not implicit in handler code.
- Every gated route has a negative test, so a regression that drops the gate fails CI.
- The TTL/expiry path is now covered, so a regression in token expiry (e.g. a gate that
  honors stale tokens) fails CI.
- The excluded routes are documented with rationale, so the exclusion is a recorded
  decision rather than an oversight.

**Negative / accepted risk**

- Credential-vault writes, channel-secret configuration, and user-management mutations
  can be performed in v0.1.0 with only the standard auth + admin-role gate — no
  password re-confirmation. In a shared or compromised-session scenario this is a
  weaker posture than the gated routes. Accepted for v0.1.0; tracked for v0.2.

## Follow-up

- **v0.2 hardening:** extend `requireReAuth` to `setCredential` / `deleteCredential`,
  `configureChannel` secret writes, and user-management mutations
  (create / delete / role / password reset), each with a positive + negative test
  mirroring the pattern in `pkg/gateway/reauth_gate_test.go`. Revisit this ADR's
  exclusion table when that work lands and move the routes from "excluded" to "covered".
- Revisit the single-use, in-memory token model if multi-user deployments become a
  v0.2+ target (per-user concurrent consent tokens, persistence across restart).
