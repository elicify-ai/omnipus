# ADR-022 — Re-auth Consent Gate: Credential-Vault Scope Expansion to v0.1.0

**Status:** Accepted
**Date:** 2026-06-20
**Deciders:** backend-lead, architect, operator
**Supersedes (partial):** expands ADR-021's credential-vault exclusion into v0.1.0

---

## Context

ADR-021 enumerated the v0.1.0 scope of the re-auth consent gate (`requireReAuth`,
FR-12.2) and explicitly deferred three classes of mutation to v0.2:

1. **Credential-vault write/delete** — `setCredential` / `deleteCredential`
   (`POST`/`DELETE /api/v1/credentials[/{key}]`).
2. **Channel-secret configuration** — `configureChannel` (token/secret/password/key
   fields routed to the encrypted credential store via SEC-23 / #289).
3. **User-management mutations** — user create / delete / role change / password reset.

ADR-021's rationale for the deferral was that all three are already behind `withAuth`
+ an admin-role check (defense-in-depth), that FR-12.2 / SC-7 name "settings"
generically without enumerating endpoints, and that v0.1.0 is single-user /
one-password by default — bounding the unattended-session threat the consent token
mitigates.

After ADR-021 landed, the operator reviewed the v0.1.0 hardening scope and approved
**bringing the credential-vault routes into v0.1.0** while leaving channel-secret
configuration and user-management in v0.2. The driver: credential-vault writes are
the **highest-blast-radius** of the three excluded classes — `setCredential` /
`deleteCredential` are the direct, unrestricted path to storing and revoking the API
keys and channel tokens that every other gated surface (providers, integrations,
channels) ultimately dereferences. A silent credential-vault mutation in an
unattended session can insert an attacker-controlled key (exfiltrating future
traffic billed to the operator) or revoke a live provider/channel key mid-session,
with no password re-confirmation. That is a materially worse posture than the
already-gated provider-key PUT, which the vault underpins.

Channel-secret configuration and user-management remain v0.2 per ADR-021:
channel-secret writes are mediated by `configureChannel`'s field-routing (already
SEC-23-enforced, no plaintext fallback) and a narrower surface than the raw vault;
user-management mutations are lower-blast-radius in a single-user deployment and
involve additional UX (password-reset flow) best tackled as a v0.2 batch.

## Decision

`requireReAuth` now covers `setCredential` and `deleteCredential` in v0.1.0. The
v0.1.0 gated-surface table (ADR-021 §Decision) is extended by:

| Area | Endpoint / handler | Spec |
| --- | --- | --- |
| Credential vault — write | `setCredential` (`POST /api/v1/credentials`) | FR-12.2 |
| Credential vault — delete | `deleteCredential` (`DELETE /api/v1/credentials/{key}`) | FR-12.2 |

Both handlers now:

1. Resolve the authenticated user from the request context (401 when absent —
   consistent with the other gated handlers).
2. Call `a.requireReAuth(w, r, user.Username)` **before** any store interaction.
   On a missing/consumed/expired token this writes **403** with the standard
   "this change requires re-typing your password" message and returns early.
3. Proceed with the existing decode → validate → store Set/Delete path only when
   the consent token was valid (and consumed, single-use).

`deleteCredential`'s signature changed from `(w, key)` to `(w, r, key)` so the
handler can read the `X-Reauth-Token` header and the authenticated user; its single
call site in `HandleCredentials` was updated accordingly.

### What stays v0.2 (unchanged from ADR-021)

- **Channel-secret configuration** — `configureChannel`. NOT gated in v0.1.0.
- **User-management mutations** — user create / delete / role change / password
  reset. NOT gated in v0.1.0.

### Tests

`pkg/gateway/reauth_gate_test.go` gains four tests mirroring the existing 6-gate
pattern (negative no-token → 403, positive valid-token → success):

- `TestSetCredential_RequiresReAuth` — no token → 403.
- `TestSetCredential_WithReAuth_Succeeds` — valid token → 201, secret lands in the
  encrypted store and is retrievable.
- `TestDeleteCredential_RequiresReAuth` — no token → 403, and a seeded credential
  is **not** deleted (the gate fired before the store call).
- `TestDeleteCredential_WithReAuth_Succeeds` — valid token → 200, secret removed
  from the store.

The negative tests are a deliberate tripwire: removing `requireReAuth` from either
handler flips them red in CI.

## Rationale

- **Highest-blast-radius excluded mutation.** The credential vault is the root
  store for every provider key, integration key, and (via `_ref`) channel secret.
  Gating the provider-key PUT but not the vault that backs it would leave the
  weaker link open: an attacker with an unattended session could bypass the
  provider-key gate by writing the key directly to the vault.
- **Symmetric write/delete gating.** Deleting a stored secret is as disruptive as
  writing one — it can revoke a channel or provider mid-session. Both directions
  are gated so the posture is consistent.
- **Low marginal cost.** The consent primitive, token store, SPA `ReAuthDialog`
  pattern, and test scaffolding all already exist (ADR-021). Extending to two more
  handlers is a small, well-contained change with no new abstractions.
- **SPA impact is a follow-up (W3 wave).** The credential-vault UI in Settings →
  Security → Credential Vault will need to trigger `ReAuthDialog` before issuing
  the POST/DELETE, the same way the integrations/providers UIs do. That is a
  frontend change tracked separately; the backend gate is correct and safe to ship
  first (the SPA will simply get a 403 until it sends the token, which is the
  desired fail-closed behavior).

## Consequences

**Positive**

- The credential vault — the root of the key/token trust chain — now requires
  password re-confirmation for any write or delete, closing the highest-blast-radius
  gap in ADR-021's v0.1.0 scope.
- The gate boundary remains explicit and enumerated; ADR-021's exclusion table is
  narrowed by exactly the two routes moved here.
- Negative tests guard both routes against a future regression that drops the gate.

**Negative / accepted risk**

- The SPA credential-vault UI must be updated (W3 wave) to call
  `POST /api/v1/auth/reauth` and replay the token; until then, vault writes/deletes
  from the UI will receive 403. This is intentional fail-closed behavior, not a bug.
- Channel-secret configuration and user-management mutations remain ungated in
  v0.1.0 — accepted per ADR-021, tracked for v0.2.

## Follow-up

- **W3 wave (frontend):** wire `ReAuthDialog` into the Credential Vault UI so
  `setCredential` / `deleteCredential` calls carry the `X-Reauth-Token` header.
- **v0.2 hardening:** extend `requireReAuth` to `configureChannel` secret writes
  and user-management mutations, each with positive + negative tests mirroring
  this pattern. Update ADR-021's exclusion table and this ADR when those land.
- Revisit the single-use, in-memory token model if multi-user deployments become a
  v0.2+ target (per ADR-021 follow-up).
