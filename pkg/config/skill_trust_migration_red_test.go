// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 4.
// TestSkillTrustMigration_LegacyValues: the C-3 one-time rewrite of the
// legacy trust vocabulary to allow / ask / deny.
//
// Oracle: skills-tools-redesign-spec.md C-3 ("On load, a legacy stored value
// is rewritten once: block_unverified -> deny, warn_unverified -> ask,
// allow_all -> allow; an empty value is treated as ask. The rewrite writes
// one audit entry.") and Dataset D3 rows 1–4 and 10 (idempotent: a stored
// value already in the new vocabulary is not rewritten and writes no audit
// entry on a second start).
//
// The new vocabulary and the migration do not exist at this commit (today's
// SkillTrustLevel only knows block_unverified / warn_unverified /
// allow_all). Per the qa-lead RED rule the test fails LOUD with BLOCKED
// until the migration lands; migrateLegacySkillTrustOf is the test-local
// seam the fix wires to. The "one audit entry" half of S-33 belongs to the
// boot path and is asserted by the integration suite (order 5's batch);
// this unit test pins the mapping and the "rewrote at all" decision the
// audit entry is gated on.

import (
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// migrateLegacySkillTrustOf is the test-local seam for the C-3 one-time
// trust rewrite. The GREEN step wires it to the production entry point; it
// stays nil here so the RED run fails with BLOCKED instead of panicking.
var migrateLegacySkillTrustOf func(stored string) (newValue string, rewrote bool)

// TestSkillTrustMigration_LegacyValues drives Dataset D3 rows 1–4 and 10.
// Traces to: BDD S-33; spec C-3; Dataset D3.
func TestSkillTrustMigration_LegacyValues(t *testing.T) {
	nativebinding.Require(t, "pkg/config", migrateLegacySkillTrustOf,
		"C-3/S-33/D3: native trust migration; post-GREEN load/audit-path re-audit required")
	cases := []struct {
		name        string
		stored      string // the value found in sandbox.skill_trust at load
		wantNew     string // the stored value after load
		wantRewrite bool   // whether the load rewrote it (gates the audit entry)
	}{
		{name: "D3#1 block_unverified -> deny", stored: "block_unverified", wantNew: "deny", wantRewrite: true},
		{name: "D3#2 warn_unverified -> ask", stored: "warn_unverified", wantNew: "ask", wantRewrite: true},
		{name: "D3#3 allow_all -> allow", stored: "allow_all", wantNew: "allow", wantRewrite: true},
		{name: "D3#4 empty -> ask", stored: "", wantNew: "ask", wantRewrite: true},
		{
			// D3#10: a value already in the new vocabulary is never rewritten;
			// starting twice writes no audit entry the second time.
			name:   "D3#10 stored ask stays ask, no rewrite, no audit",
			stored: "ask", wantNew: "ask", wantRewrite: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotNew, rewrote := migrateLegacySkillTrustOf(tc.stored)
			if gotNew != tc.wantNew {
				t.Fatalf("stored %q -> %q, want %q (spec C-3, Dataset D3)", tc.stored, gotNew, tc.wantNew)
			}
			if rewrote != tc.wantRewrite {
				t.Fatalf("rewrote = %v, want %v (the rewrite gates the one audit entry, spec C-3)", rewrote, tc.wantRewrite)
			}
		})
	}
}
