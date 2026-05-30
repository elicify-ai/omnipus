# ADR-008: `ctxkey` Leaf Package and Gateway-Local Type Aliases

## Status

Accepted (2026-04-19) — codifies the layout introduced in Sprint A PR-G/PR-H.

## Context

The gateway stores three values on `context.Context`: the authenticated user's role, the `*config.UserConfig` for that user, and a snapshot of `*config.Config` taken at request entry. Go's `context` package compares keys by **concrete type identity** — `ctx.Value(TypeX{})` returns a value only if it was stored with exactly the same type. Two packages each declaring `type RoleContextKey struct{}` produce two incompatible keys.

Before this layout, keys lived in `pkg/gateway/auth.go` alongside `checkBearerAuth`. The new RBAC middleware in `pkg/gateway/middleware/rbac.go` needed to read the role key written by the gateway's auth layer — but `pkg/gateway/middleware` cannot import `pkg/gateway` without a circular dependency (gateway imports middleware to wrap its mux).

## Decision

1. Create a **leaf package** `pkg/gateway/ctxkey` that exports only empty struct types: `RoleContextKey{}`, `UserContextKey{}`, `ConfigContextKey{}`. It has zero runtime code and zero dependencies on other Omnipus packages, so anyone can import it without creating a cycle.

2. In `pkg/gateway/auth.go`, retain the gateway-local identifiers via **type aliases** (Go 1.9+ `type X = ctxkey.X`), as seen at `pkg/gateway/auth.go:36-44`:

   ```go
   type configContextKey = ctxkey.ConfigContextKey
   type RoleContextKey = ctxkey.RoleContextKey
   type UserContextKey = ctxkey.UserContextKey
   ```

   Because these are aliases (not new type definitions), `ctxkey.RoleContextKey{}` and `gateway.RoleContextKey{}` are the **same type** — a key written via one reads back via the other.

3. All new code writes and reads keys via `ctxkey.*` directly. The aliases exist only to avoid a churn-heavy rename across existing gateway-internal callers.

## Consequences

- The circular-import wall between `pkg/gateway` and `pkg/gateway/middleware` is broken: middleware imports `ctxkey` (leaf), not `gateway`.
- Key identity is guaranteed by the language — no risk of two packages "racing" on their own local declarations.
- Aliases (not new types) preserve method sets and zero-cost identity equality; no runtime overhead.
- A future refactor can drop the aliases entirely once all gateway-internal call sites migrate to `ctxkey.X`; the aliases are purely a migration convenience.
- Adding a new context key is a two-file change: declare it in `ctxkey/ctxkey.go`, optionally add an alias in `pkg/gateway/auth.go` if existing code uses a short name.

## References

- `pkg/gateway/ctxkey/ctxkey.go` (leaf package)
- `pkg/gateway/auth.go:36-44` (alias declarations)
- `pkg/gateway/middleware/rbac.go:29` (cross-package read via `ctxkey.RoleContextKey{}`)
- Issue #98, Sprint B Plan 4 §F29
- Go type aliases proposal: https://go.dev/design/18130-type-alias
