//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// T-106 / §3.4 D8d; FR-056; B-70 (Linux half): the Linux backend MUST ignore
// SandboxPolicy.LookupOnlyPaths — it never becomes a grant and never reaches
// ExpandRulesExcluding (Landlock cannot restrict stat(2) or chdir(2), so a
// confined program can already look up any path it can name; it still cannot
// list a folder without a grant).
//
// Oracle (§13 T-106): a policy WITH look-up entries and the same policy
// WITHOUT them produce exactly the same rule list and the same error from
// (*LinuxBackend).linuxFilesystemRules — the function the Linux backend
// itself uses to build its rules — for forChild both true and false (the
// forChild=true path is the one that runs ExpandRulesExcluding, so the deny
// rules below exercise it).
//
// Not a pass if: the comparison is against a rule list the test assembled
// instead of the backend's own output.
func TestLinuxLookupRulesAreIgnored(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "call-work")
	require.NoError(t, os.MkdirAll(work, 0o700), "instrument: the granted folder exists")
	lookupA := filepath.Join(base, "lookup-a")
	lookupB := filepath.Join(base, "lookup-b")
	withLookup := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: work, Access: AccessRead | AccessWrite}},
		DeniedPaths:     []string{filepath.Join(work, "secret.key")},
		LookupOnlyPaths: []string{lookupA, lookupB},
	}
	withoutLookup := withLookup
	withoutLookup.LookupOnlyPaths = nil

	lb := &LinuxBackend{}
	for _, forChild := range []bool{true, false} {
		rulesWith, errWith := lb.linuxFilesystemRules(withLookup, forChild)
		require.NoError(t, errWith, "T-106: the backend builds its rules with look-up entries present (forChild=%v)", forChild)
		rulesWithout, errWithout := lb.linuxFilesystemRules(withoutLookup, forChild)
		require.NoError(t, errWithout, "T-106: the backend builds its rules without look-up entries (forChild=%v)", forChild)
		require.Equal(t, rulesWithout, rulesWith,
			"T-106 §3.4 D8d/FR-056: the Linux backend's own rule list is exactly the same with look-up entries — the field never becomes a grant (forChild=%v)", forChild)
	}
}
