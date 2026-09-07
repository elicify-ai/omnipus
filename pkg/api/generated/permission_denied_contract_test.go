//go:build !windows

// Must match contract_test.go's constraint — see the note in
// file_exists_refusal_contract_test.go. Same helpers, same tag.

package generated

import "testing"

// ── PermissionDenied / ToolAssemblyDuplicate — issue #618 ─────────────────────
// Traces to:
//   contracts/asyncapi.yaml #/components/schemas/PermissionDenied
//   contracts/asyncapi.yaml #/components/schemas/ToolAssemblyDuplicate
//
// The producers are pkg/tools.PermissionDeniedPayload (shared by
// pkg/tools.PermissionDeniedResult and pkg/agent's denialPayloadJSON) and
// pkg/tools.ToolAssemblyDuplicatePayload (pkg/agent/loop.go's
// checkToolDedupInvariant guard). Before this fix both were hand-built with
// fmt.Sprintf's %q verb — Go-string quoting, not JSON quoting — which breaks
// on invalid UTF-8 or a control byte outside \n\t\r, and neither had a
// contract schema, an allow-list entry, or a length budget at all. A payload
// that violates this schema is NOT dropped at the SPA edge — the frame's
// `result` generates as z.unknown()/any, so the oneOf/anyOf union is
// documentary in both artifacts today (ADR-060 W1). It instead fails the
// hand-written detector and renders as a raw JSON blob (or fails
// json.Unmarshal downstream entirely), which leaves the caller with NOTHING —
// strictly worse than the prose the discriminator replaced. The minLength:1
// constraints below are not decoration.

func TestContract_PermissionDenied_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "PermissionDenied", PermissionDenied{
		Error:     "permission_denied",
		Message:   "Access to this path is denied by filesystem policy.",
		Tool:      "write_file",
		Reason:    "access denied: path is outside the effective filesystem scope",
		Permanent: true,
	})
}

func TestContract_PermissionDenied_PermanentFalse(t *testing.T) {
	// "saturated" is the one approval-flow reason that is NOT permanent —
	// permanent:false must be schema-valid too (it is a plain bool, not a
	// const), so this fixture guards against a future author accidentally
	// tightening the schema to `const: true`.
	mustPassAsyncAPI(t, "PermissionDenied", PermissionDenied{
		Error:     "permission_denied",
		Message:   "Too many approval requests were already pending. This one may be retried later.",
		Tool:      "bash",
		Reason:    "saturated",
		Permanent: false,
	})
}

func TestContract_PermissionDenied_ZeroValue(t *testing.T) {
	// The zero value violates every minLength:1 constraint at once (error is
	// also not the const value). This is the shape a producer yields if it
	// forwards unchecked caller input — which is why PermissionDeniedPayload
	// defaults every string field.
	mustFailAsyncAPI(t, "PermissionDenied", PermissionDenied{},
		"error/message/tool/reason are all required with minLength 1; the zero value has four empty strings")
}

func TestContract_PermissionDenied_EmptyReasonRejected(t *testing.T) {
	mustFailAsyncAPI(t, "PermissionDenied", PermissionDenied{
		Error:     "permission_denied",
		Message:   "Access to this path is denied by filesystem policy.",
		Tool:      "write_file",
		Permanent: true,
	}, "reason is required with minLength 1")
}

func TestContract_PermissionDenied_WrongDiscriminatorRejected(t *testing.T) {
	// `error` is a const in the schema. A payload carrying any other value is
	// not a PermissionDenied, and the gateway's allow-list would not route it
	// as one — the schema must agree.
	mustFailAsyncAPI(t, "PermissionDenied", PermissionDenied{
		Error:     "access_denied",
		Message:   "Access to this path is denied by filesystem policy.",
		Tool:      "write_file",
		Reason:    "outside scope",
		Permanent: true,
	}, "error is a const: permission_denied")
}

func TestContract_ToolAssemblyDuplicate_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "ToolAssemblyDuplicate", ToolAssemblyDuplicate{
		Error:   "tool_assembly_duplicate",
		Message: "duplicate tool \"send_file\" in assembled tools[]",
	})
}

func TestContract_ToolAssemblyDuplicate_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "ToolAssemblyDuplicate", ToolAssemblyDuplicate{},
		"error/message are both required with minLength 1; the zero value has two empty strings")
}

func TestContract_ToolAssemblyDuplicate_WrongDiscriminatorRejected(t *testing.T) {
	mustFailAsyncAPI(t, "ToolAssemblyDuplicate", ToolAssemblyDuplicate{
		Error:   "duplicate_tool",
		Message: "duplicate tool in assembled tools[]",
	}, "error is a const: tool_assembly_duplicate")
}
