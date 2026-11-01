package gateway

import "testing"

// Oracle: r17 §3.6–3.11. These rows need the Settings path/preset
// authority, absent at the contract pin. They deliberately fail rather than
// exercising the older per-agent CLIPath/PATH fallback as if it were T8.
// No process-spawn, argv, audit or safety claim is verified by these fatals.
func TestSpawnSettings_MissingAuthorityPrerequisites(t *testing.T) {
	for _, row := range []struct{ id, want string }{
		{"6.7", "get_agent resolves Settings path and omits program_warning and SOUL"},
		{"6.8", "empty Settings path yields the exact NoPath warning"},
		{"8.14", "created worker reports runnable from the person-controlled Settings path"},
		{"9.9", "resolve the current symlink target anew at dispatch"},
		{"9.10", "unsafe retarget refuses before any execution with exact CheckFailed text"},
		{"9.11", "exec the checked resolvedPath even if the input target changes after checking"},
		{"9.12", "empty Settings path refuses despite a legacy agent CLIPath and a PATH hit"},
		{"9.13", "delegation returns the NoPath refusal to its parent"},
		{"9.14", "creation without a path succeeds but reports runnable false and NoPath"},
		{"9.17", "driver, filtered agent args, unfiltered person preset and hand-off order; audit bypass flag"},
		{"9.18", "person preset --model a follows agent --model b"},
		{"9.19", "preset uses the same quoted-argument parsing as cli_args"},
	} {
		t.Run(row.id, func(t *testing.T) {
			t.Fatalf("BLOCKED: sandbox.third_party_agents.clis Settings path/preset authority not implemented — required by r17 §3 / %s: %s", row.id, row.want)
		})
	}
}
