//go:build !windows

// Must match contract_test.go's constraint: the mustPassAsyncAPI /
// mustFailAsyncAPI helpers this file uses are defined there, and that file is
// !windows. Without the same tag this file compiles on Windows WITHOUT its
// helpers and fails as "undefined" — which is how it was found.

package generated

import "testing"

// ── FileExistsRefusal — ADR-059 W5 ────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml #/components/schemas/FileExistsRefusal
//
// The producer is pkg/tools.FileExistsRefusalResult, whose whole job is to make
// a write refusal machine-distinguishable from an I/O failure. A payload that
// violates the schema is NOT dropped at the SPA edge — the union is documentary
// in both generated artifacts (ADR-060 W1); it renders as a raw JSON blob,
// which leaves the caller with
// NOTHING — strictly worse than the prose the discriminator replaced. So the
// four minLength:1 constraints are not decoration; they are the difference
// between the fix working and the fix silently un-doing the thing it fixed.

func TestContract_FileExistsRefusal_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "FileExistsRefusal", FileExistsRefusal{
		Error:  "file_exists",
		Reason: "file: /w/a.svg already exists. Set overwrite=true to replace.",
		Tool:   "write_file",
		Path:   "/w/a.svg",
	})
}

func TestContract_FileExistsRefusal_ZeroValue(t *testing.T) {
	// The zero value violates all four minLength:1 constraints at once. This
	// is the shape a producer yields if it forwards unchecked caller input —
	// which is why FileExistsRefusalResult defaults every field.
	mustFailAsyncAPI(t, "FileExistsRefusal", FileExistsRefusal{},
		"every field is required with minLength 1; the zero value has four empty strings")
}

func TestContract_FileExistsRefusal_EmptyPathRejected(t *testing.T) {
	// Singled out because path is the field most likely to arrive empty from a
	// caller (a malformed args map), and the one the original producer did not
	// defend — it guarded only reason.
	mustFailAsyncAPI(t, "FileExistsRefusal", FileExistsRefusal{
		Error:  "file_exists",
		Reason: "already exists",
		Tool:   "write_file",
	}, "path is required with minLength 1")
}

func TestContract_FileExistsRefusal_WrongDiscriminatorRejected(t *testing.T) {
	// `error` is a const in the schema. A payload carrying any other value is
	// not a FileExistsRefusal, and the gateway's allow-list would not route it
	// as one — the schema must agree.
	mustFailAsyncAPI(t, "FileExistsRefusal", FileExistsRefusal{
		Error:  "file_missing",
		Reason: "not found",
		Tool:   "write_file",
		Path:   "/w/a.svg",
	}, "error is a const: file_exists")
}
