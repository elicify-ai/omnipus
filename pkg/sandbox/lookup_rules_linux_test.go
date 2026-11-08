//go:build linux

package sandbox

import "testing"

// T-106 / §3.4 D8d; FR-056; B-70 (Linux half): the Linux backend MUST ignore
// the look-up-only field — it never becomes a grant and never reaches
// ExpandRulesExcluding (Landlock cannot restrict stat(2) or chdir(2), so a
// confined program can already look up any path it can name). Blocked until
// the field exists (the developer is landing it separately). The committed
// oracle, written against the function the Linux backend itself uses to
// build its rules — (*LinuxBackend).linuxFilesystemRules(policy, forChild)
// — never against a list the test builds:
//
//   - a policy WITH look-up entries and the same policy WITHOUT them produce
//     exactly the same rule list from linuxFilesystemRules, for forChild
//     both true and false, compared as the function's own output (rules and
//     errors identical);
//   - a policy whose only difference is look-up entries renders the same
//     Landlock rule set, so no look-up entry can widen Linux.
//
// Not a pass if: the comparison is against a rule list the test assembled
// instead of the backend's own output.
func TestLinuxLookupRulesAreIgnored(t *testing.T) {
	t.Fatal("BLOCKED: the SandboxPolicy look-up-only folder field does not exist on this code — the one pkg/sandbox addition D8d names (§3.4 D8d; founder Q37 = A) is not implemented here yet. Required by T-106 (§13): a policy with look-up entries produces exactly the same Landlock rules as the same policy without them, through (*LinuxBackend).linuxFilesystemRules — the backend's own rule-building function; the full check is this test's doc comment")
}
