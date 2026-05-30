// Package policy — admin role predicate (FR-015).
//
// FR-015 binds the admin-ask gate to the existing user-role check
// `User.Role == config.UserRoleAdmin` (`pkg/gateway/rest_users.go:54-56,116`).
// No new RBAC infrastructure is introduced.
//
// This file exposes a single helper, `IsAdmin`, that the A3 gateway lane
// calls from `POST /api/v1/tool-approvals/{approval_id}` to gate
// approve/deny/cancel actions on `RequiresAdminAsk` tools (FR-059).
//
// We keep this in `pkg/policy/` (rather than inlining in pkg/gateway) so
// the security lane owns the predicate and tests for it. The function is
// trivial today; the indirection is deliberate so that future RBAC
// granularity changes (per `docs/specs/rbac-granularity-spec.md`) have one
// chokepoint to update.

package policy

import "github.com/dapicom-ai/omnipus/pkg/config"

// IsAdmin reports whether the supplied user has the `admin` role.
//
// Returns false when `u` is nil — callers MUST treat a missing user as
// non-admin (fail-closed). The current `withAuth` middleware guarantees a
// non-nil user only when the request is authenticated; unauthenticated
// requests must be rejected with HTTP 401 by the caller before this
// function is consulted.
//
// Threat model: an attacker who can forge a `*config.UserConfig` value
// with `Role = UserRoleAdmin` already has in-process control of the
// gateway and the admin gate is moot; this predicate is not a defense
// against a compromised process, only against under-privileged callers.
//
// FR-015.
func IsAdmin(u *config.UserConfig) bool {
	if u == nil {
		return false
	}
	return u.Role == config.UserRoleAdmin
}
