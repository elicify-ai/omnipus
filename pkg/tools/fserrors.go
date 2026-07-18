// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-046 (unified filesystem & workspace model) typed
// filesystem-resolution error taxonomy (FR-035). ResolvePath (resolvepath.go)
// returns exactly these sentinels so callers (and path_audit.go's
// classifyPathDenialReason) can classify a denial with errors.Is instead of
// sniffing an error message string.
package tools

import (
	"errors"
	"fmt"
)

// ErrOutsideScope means the resolved path falls outside the turn's effective
// working directory and the effective filesystem_scope is not
// fspolicy.FSScopeUnrestricted, so the operation is refused (FR-005/FR-016).
//
// The message deliberately includes both "access denied" and "outside" so it
// stays compatible with pre-existing callers/tests that assert on either
// phrase (the two legacy validators this error class supersedes,
// validatePathWithAllowPaths and getSafeRelPath, used one or the other).
var ErrOutsideScope = errors.New("access denied: path is outside the effective filesystem scope")

// ErrCarveOut means the resolved path falls on or under one of the
// $OMNIPUS_HOME-anchored carve-out roots (master.key, credentials.json,
// agents/, workspaces/) that FR-017 denies unconditionally, regardless of
// filesystem_scope.
var ErrCarveOut = errors.New("access denied: path is a protected carve-out ($OMNIPUS_HOME-anchored)")

// ErrApprovalDenied is RESERVED for P2's filesystem_scope=ask flow: an
// operator explicitly denied an approval request for this path. No P1 code
// path returns this value.
var ErrApprovalDenied = errors.New("access denied: approval was denied for this path")

// ErrApprovalUnavailable is RESERVED for P2's filesystem_scope=ask flow: no
// interactive approver was reachable for this turn (TurnOrigin fail-closed).
// No P1 code path returns this value.
var ErrApprovalUnavailable = errors.New("access denied: no interactive approver available for this path")

// ErrPathInvalid means rawPath could not be resolved at all — an embedded
// NUL byte, or a hard (non-"not exist") failure while walking the path's
// ancestors to realpath it (e.g. a permission error, or ENAMETOOLONG).
var ErrPathInvalid = errors.New("invalid path")

// PermissionDeniedResult builds a structured *ToolResult for a filesystem
// resolution denial, reusing the EXACT "permission_denied" JSON convention
// emitted at pkg/agent/loop.go (~:7327/:7359 —
// {"error":"permission_denied","message":...,"tool":...,"reason":...}) so the
// LLM sees one consistent shape for every permission denial, whether it came
// from tool-policy (loop.go) or filesystem-scope (ResolvePath) enforcement.
//
// classErr is the typed sentinel (ErrOutsideScope/ErrCarveOut/ErrPathInvalid/
// ...) ResolvePath returned; it is preserved on the result via WithError for
// logging but never serialized directly (mirrors DelegationDeniedResult's
// pattern in result.go). detail is an optional human-readable elaboration
// (typically classErr.Error()) included as the "reason" field; when empty the
// "reason" field is omitted entirely, matching loop.go's own two message
// shapes (one with reason, one without).
func PermissionDeniedResult(toolName string, classErr error, detail string) *ToolResult {
	const message = "Access to this path is denied by filesystem policy."

	reason := detail
	if reason == "" && classErr != nil {
		reason = classErr.Error()
	}

	var payload string
	if reason != "" {
		payload = fmt.Sprintf(
			`{"error":"permission_denied","message":%q,"tool":%q,"reason":%q}`,
			message, toolName, reason,
		)
	} else {
		payload = fmt.Sprintf(
			`{"error":"permission_denied","message":%q,"tool":%q}`,
			message, toolName,
		)
	}

	return (&ToolResult{
		ForLLM:  payload,
		IsError: true,
	}).WithError(classErr)
}
