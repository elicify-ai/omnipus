// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestPathGrantAccessBitsMatchSandboxConstants pins ADR-091 FR-036's own
// requirement — PathGrant "reus[es] the same sandbox.AccessRead|AccessWrite|
// AccessExecute bitmask ... not a new vocabulary" — as a byte-for-byte
// equivalence test. fspolicy cannot import pkg/sandbox (stdlib-only leaf,
// pkg/sandbox imports fspolicy, so the reverse would cycle), so
// fspolicy.PathGrantAccessRead/Write/Execute are separately-declared
// constants; this test is what keeps the two sets from silently drifting
// apart if either is ever renumbered.
func TestPathGrantAccessBitsMatchSandboxConstants(t *testing.T) {
	cases := []struct {
		name       string
		fspolicy   uint64
		sandboxBit uint64
	}{
		{"Read", fspolicy.PathGrantAccessRead, sandbox.AccessRead},
		{"Write", fspolicy.PathGrantAccessWrite, sandbox.AccessWrite},
		{"Execute", fspolicy.PathGrantAccessExecute, sandbox.AccessExecute},
	}
	for _, tc := range cases {
		if tc.fspolicy != tc.sandboxBit {
			t.Errorf("fspolicy.PathGrantAccess%s (%d) != sandbox.Access%s (%d) — the two vocabularies have drifted apart",
				tc.name, tc.fspolicy, tc.name, tc.sandboxBit)
		}
	}
}
