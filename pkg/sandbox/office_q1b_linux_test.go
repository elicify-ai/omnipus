//go:build linux

package sandbox

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// Kernel header bits (x/sys/unix), the same oracle T-118 uses. Not the
// backend's private constants.
const (
	q1bReadFile = uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE)
	q1bReadDir  = uint64(unix.LANDLOCK_ACCESS_FS_READ_DIR)
)

// TestOfficeQ1B_ConfinedBootHasExactlyTheThreeReads is RED on current HEAD:
// the confined boot set does not yet grant the three reads, so the path
// diff names them as missing. Rights are checked through
// accessToLandlockRights, where the variant-C unit test checks its mask.
func TestOfficeQ1B_ConfinedBootHasExactlyTheThreeReads(t *testing.T) {
	home := t.TempDir()
	policy := DefaultPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil)
	q1bAssertNoDuplicatePaths(t, policy.FilesystemRules)
	q1bAssertPreQ1BRulesUnchanged(t, home, policy.FilesystemRules)
	q1bAssertConfinedPathSet(t, home, policy.FilesystemRules)
	q1bAssertThreeReadRights(t, policy.FilesystemRules)
}

func q1bAssertConfinedPathSet(t *testing.T, home string, rules []PathRule) {
	t.Helper()
	got := q1bIndex(rules)
	required := []string{"/etc/passwd", "/usr/share/fonts"}
	localExists := false
	if _, err := os.Stat("/usr/local/share/fonts"); err == nil {
		localExists = true
		required = append(required, "/usr/local/share/fonts")
	}
	for _, path := range required {
		if _, ok := got[path]; !ok {
			t.Errorf("confined boot is missing %s (founder Q1 B)", path)
		}
	}
	allowed := map[string]struct{}{}
	for path := range q1bIndex(q1bPreQ1BRules(home)) {
		allowed[path] = struct{}{}
	}
	// /usr/local/share/fonts is optional only when the directory is absent:
	// include-then-skip and omit-when-absent both match the decision then.
	for _, path := range []string{"/etc/passwd", "/usr/share/fonts"} {
		allowed[path] = struct{}{}
	}
	if localExists {
		allowed["/usr/local/share/fonts"] = struct{}{}
	} else if _, present := got["/usr/local/share/fonts"]; present {
		allowed["/usr/local/share/fonts"] = struct{}{}
	}
	for path := range got {
		if _, ok := allowed[path]; !ok {
			t.Errorf("confined boot grants %s; Q1 B widens nothing else", path)
		}
	}
}

func q1bAssertThreeReadRights(t *testing.T, rules []PathRule) {
	t.Helper()
	got := q1bIndex(rules)
	checks := []struct {
		path string
		dir  bool
	}{
		{path: "/etc/passwd", dir: false},
		{path: "/usr/share/fonts", dir: true},
	}
	if _, err := os.Stat("/usr/local/share/fonts"); err == nil {
		checks = append(checks, struct {
			path string
			dir  bool
		}{path: "/usr/local/share/fonts", dir: true})
	} else if rule, ok := got["/usr/local/share/fonts"]; ok {
		checks = append(checks, struct {
			path string
			dir  bool
		}{path: rule.Path, dir: true})
	}
	for _, check := range checks {
		rule, ok := got[check.path]
		if !ok {
			t.Errorf("no rule for %s; cannot check Landlock rights", check.path)
			continue
		}
		const forbidden = AccessWrite | AccessExecute | AccessMakeSocketRemoveFile
		if rule.Access&forbidden != 0 {
			t.Errorf("%s access=%#x includes write, execute or socket rights; Q1 B grants read only", check.path, rule.Access)
		}
		if rule.Access&AccessRead == 0 {
			t.Errorf("%s does not grant read", check.path)
		}
		for _, abi := range []int{1, 2, 3, 4, 5, 6} {
			backend := LinuxBackend{abiVersion: abi, allRights: ^uint64(0)}
			mapped := backend.accessToLandlockRights(rule.Access)
			if check.dir {
				// One directory rule. Landlock path-beneath covers the tree,
				// which is the decision's "recursive" read-file + read-dir.
				if mapped != q1bReadFile|q1bReadDir {
					t.Errorf("ABI %d %s rights=%#x, want READ_FILE|READ_DIR (%#x)", abi, check.path, mapped, q1bReadFile|q1bReadDir)
				}
				continue
			}
			// AccessRead maps to READ_FILE|READ_DIR. READ_DIR is directory-only
			// and the kernel rejects it on a file, so the effective grant is
			// read-file. No other bit is allowed.
			if mapped&^q1bReadDir != q1bReadFile {
				t.Errorf("ABI %d %s effective rights=%#x, want read-file (%#x)", abi, check.path, mapped, q1bReadFile)
			}
		}
	}
}
