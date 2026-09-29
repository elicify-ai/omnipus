// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — test 22 of docs/internal/specs/read-boundary-consistency-spec.md
// (S-5.5, MV-3, FR-021): the roots-searched event name the spec fixes
// (Ambiguity Warning 2) is registered in validEventNames — so writing it logs
// no "unknown event" warning — and satisfies the wire contract's event
// pattern, read from AuditEntry.yaml at run time (contractEventPattern, the
// #667 guard's own helper).
//
// The name is a literal on purpose: the production constant does not exist
// before GREEN, and this test's oracle is the spec's string, not whatever
// constant GREEN picks to hold it.
package audit_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

const searchRootsEventName = "path.search_roots"

// TestAuditEventNames_SearchRootsRegistered is test 22.
func TestAuditEventNames_SearchRootsRegistered(t *testing.T) {
	if !contractEventPattern(t).MatchString(searchRootsEventName) {
		t.Fatalf("%q does not match AuditEntry.yaml's event pattern (MV-3)", searchRootsEventName)
	}
	if !audit.IsValidEventName(audit.EventName(searchRootsEventName)) {
		t.Fatalf("audit.IsValidEventName(%q) = false; FR-021 requires it registered in validEventNames (S-5.5)", searchRootsEventName)
	}
	// Discriminating control: the check is not "every dotted name is valid".
	if audit.IsValidEventName(audit.EventName("path.search_roots_typo")) {
		t.Fatal("IsValidEventName accepted an unregistered name; the instrument cannot see a missing registration")
	}
}
