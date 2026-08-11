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
// resolution denial, sharing the ONE "permission_denied" wire producer
// (PermissionDeniedPayload, result.go — contracts/asyncapi.yaml's
// PermissionDenied schema) with pkg/agent's tool-policy denial path
// (tool_denial.go's denialPayloadJSON) so the LLM sees one consistent,
// schema-valid shape for every permission denial, whether it came from
// tool-policy (loop.go) or filesystem-scope (ResolvePath) enforcement.
//
// Before issue #618's fix this built the JSON by hand with fmt.Sprintf's
// %q verb — that is Go-string quoting, not JSON quoting, and broke on any
// path containing invalid UTF-8 or a C0/C1 control byte outside \n\t\r,
// producing unparseable output the gateway's whole-document json.Unmarshal
// would reject. It also carried no length budget at all: ResolvePath's
// ErrOutsideScope embeds rawPath three times, so an ordinary ~830-character
// path (a quarter of Linux PATH_MAX) already overflowed the downstream
// 2000-rune truncation cap before it stopped being valid JSON. Both defects
// are now closed by routing through PermissionDeniedPayload, which
// marshals with encoding/json and clamps to the same 1900-rune budget every
// other structured refusal in this file uses.
//
// classErr is the typed sentinel (ErrOutsideScope/ErrCarveOut/ErrPathInvalid/
// ...) ResolvePath returned; it is preserved on the result via WithError for
// logging but never serialized directly (mirrors DelegationDeniedResult's
// pattern in result.go). detail is an optional human-readable elaboration
// (typically classErr.Error()) included as the "reason" field; when both
// detail and classErr are empty/nil, PermissionDeniedPayload defaults reason
// to a non-empty placeholder rather than omitting the field, since the
// contract requires reason with minLength:1.
//
// permanent is always true here: a filesystem-scope denial is a property of
// the path and the effective scope, both fixed for the rest of the turn —
// unlike a tool-policy "saturated" approval-queue denial, retrying the same
// call cannot succeed.
func PermissionDeniedResult(toolName string, classErr error, detail string) *ToolResult {
	const message = "Access to this path is denied by filesystem policy."

	reason := detail
	if reason == "" && classErr != nil {
		reason = classErr.Error()
	}

	encoded, err := PermissionDeniedPayload(toolName, message, reason, true)
	if err != nil {
		// Fall back to a plain text error if marshaling somehow fails —
		// prose the caller can still read is better than nothing.
		return ErrorResult(fmt.Sprintf("%s (%s)", message, reason)).WithError(classErr)
	}

	return (&ToolResult{
		ForLLM:  string(encoded),
		IsError: true,
	}).WithError(classErr)
}
