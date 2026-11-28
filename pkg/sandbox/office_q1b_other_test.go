//go:build !linux

package sandbox

import (
	"testing"
)

// TestOfficeQ1B_NonLinuxConfinedBootUnchanged passes on current HEAD (PIN).
// The three reads are Linux-only; macOS and every other OS stay as they are.
func TestOfficeQ1B_NonLinuxConfinedBootUnchanged(t *testing.T) {
	home := t.TempDir()
	policy := DefaultPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil)
	q1bAssertNoDuplicatePaths(t, policy.FilesystemRules)
	q1bAssertPreQ1BRulesUnchanged(t, home, policy.FilesystemRules)
	got := q1bIndex(policy.FilesystemRules)
	want := q1bIndex(q1bPreQ1BRules(home))
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("non-Linux confined boot gained %s; Q1 B does not change macOS", path)
		}
	}
	for path := range want {
		if _, ok := got[path]; !ok {
			t.Errorf("non-Linux confined boot lost %s", path)
		}
	}
	for _, path := range []string{"/etc/passwd", "/usr/share/fonts", "/usr/local/share/fonts"} {
		if _, ok := got[path]; ok {
			t.Errorf("non-Linux confined boot must not grant %s", path)
		}
	}
}
