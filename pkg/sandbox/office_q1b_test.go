// Founder Q1 B (OFFICE-Q1B-READS.md, 2026-11-18). Oracles are that decision
// and the measured trace, not the current body of DefaultPolicyForModel.
//
// q1bPreQ1BReadOnlySystem is a characterization of the confined boot
// read-only list BEFORE those three reads. The decision says nothing else
// is widened, so this list is frozen here and must not be refreshed from
// the function under test.

package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var q1bPreQ1BReadOnlySystem = []string{
	"/proc",
	"/proc/self",
	"/lib",
	"/lib64",
	"/usr/lib",
	"/usr/lib64",
	"/usr/bin",
	"/opt",
	"/etc/alternatives",
	"/etc/ssl",
	"/etc/ca-certificates",
	"/etc/resolv.conf",
	"/etc/hosts",
	"/etc/nsswitch.conf",
	"/sys/devices/system/cpu",
	"/dev/urandom",
	"/dev/random",
}

// q1bMeasuredNotNeeded are refused in the trace and measured as harmless.
// The decision says not to add them.
var q1bMeasuredNotNeeded = []string{
	"/etc/fonts",
	"/etc/fonts/fonts.conf",
	"/etc/fonts/conf.d",
	"/etc/ld.so.cache",
}

func q1bExtraTemp() string {
	tmp := filepath.Clean(os.TempDir())
	if tmp == "" || tmp == "/tmp" || !filepath.IsAbs(tmp) {
		return ""
	}
	return tmp
}

func q1bIndex(rules []PathRule) map[string]PathRule {
	out := make(map[string]PathRule, len(rules))
	for _, rule := range rules {
		out[rule.Path] = rule
	}
	return out
}

func q1bAssertNoDuplicatePaths(t *testing.T, rules []PathRule) {
	t.Helper()
	seen := map[string]int{}
	for _, rule := range rules {
		seen[rule.Path]++
	}
	for path, n := range seen {
		if n > 1 {
			t.Errorf("path %s appears %d times; Q1 B adds one rule per path", path, n)
		}
	}
}

// q1bAssertPreQ1BRulesUnchanged fails if an existing boot grant is dropped
// or widened. Characterization of the pre-Q1 B grants; the decision's only
// additions are checked by the Linux test.
func q1bAssertPreQ1BRulesUnchanged(t *testing.T, home string, rules []PathRule) {
	t.Helper()
	got := q1bIndex(rules)
	rwx := AccessRead | AccessWrite | AccessExecute
	want := map[string]uint64{
		filepath.Clean(home): rwx,
		"/tmp":               rwx,
		"/dev/null":          AccessRead | AccessWrite,
		"/dev/shm":           AccessRead | AccessWrite,
	}
	if tmp := q1bExtraTemp(); tmp != "" {
		want[tmp] = rwx
	}
	for _, path := range q1bPreQ1BReadOnlySystem {
		want[path] = AccessRead | AccessExecute
	}
	for path, access := range want {
		rule, ok := got[path]
		if !ok {
			t.Errorf("pre-Q1 B boot path %s is missing; the decision does not drop existing grants", path)
			continue
		}
		if rule.Access != access {
			t.Errorf("pre-Q1 B path %s access=%#x, want %#x unchanged", path, rule.Access, access)
		}
	}
}

func q1bPreQ1BOpenPolicy(home string) SandboxPolicy {
	rules := []PathRule{{
		Path:   filepath.Clean(home),
		Access: AccessRead | AccessWrite | AccessExecute,
	}, {
		Path:   "/tmp",
		Access: AccessRead | AccessWrite | AccessExecute,
	}}
	if tmp := q1bExtraTemp(); tmp != "" {
		rules = append(rules, PathRule{
			Path:   tmp,
			Access: AccessRead | AccessWrite | AccessExecute,
		})
	}
	rules = append(rules,
		PathRule{Path: "/dev/null", Access: AccessRead | AccessWrite},
		PathRule{Path: "/dev/shm", Access: AccessRead | AccessWrite},
	)
	connect := make([]NetPortRule, 0, len(DefaultConnectPorts))
	for _, port := range DefaultConnectPorts {
		connect = append(connect, NetPortRule{Port: port})
	}
	return SandboxPolicy{
		FilesystemRules:    rules,
		ConnectPortRules:   connect,
		InheritToChildren:  true,
		ReadsOpen:          true,
		ExecOpen:           true,
		DeniedPaths:        SecretPaths(home),
		DeniedPathPrefixes: SecretBackupPathPrefixes(home),
	}
}

// TestOfficeQ1B_OpenModelByteIdenticalToPreQ1B is the decision's "open
// model is unchanged". It passes on current HEAD (PIN): open already omits
// the enumerated read-only list, so the three reads must not appear here.
func TestOfficeQ1B_OpenModelByteIdenticalToPreQ1B(t *testing.T) {
	home := t.TempDir()
	got := DefaultPolicyForModel(FilesystemModelOpen, home, nil, nil, nil, nil)
	want := q1bPreQ1BOpenPolicy(home)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("open model changed\n got: %#v\nwant: %#v", got, want)
	}
	indexed := q1bIndex(got.FilesystemRules)
	for _, path := range append(append([]string{}, q1bPreQ1BReadOnlySystem...), "/etc/passwd", "/usr/share/fonts", "/usr/local/share/fonts") {
		if _, ok := indexed[path]; ok {
			t.Errorf("open model must not enumerate %s", path)
		}
	}
}

// TestOfficeQ1B_MeasuredHarmlessPathsStayOut passes on current HEAD (PIN).
// /etc/fonts and /etc/ld.so.cache were measured as not needed.
func TestOfficeQ1B_MeasuredHarmlessPathsStayOut(t *testing.T) {
	home := t.TempDir()
	for _, model := range []FilesystemModel{FilesystemModelConfined, FilesystemModelOpen} {
		policy := DefaultPolicyForModel(model, home, nil, nil, nil, nil)
		indexed := q1bIndex(policy.FilesystemRules)
		for _, path := range q1bMeasuredNotNeeded {
			if _, ok := indexed[path]; ok {
				t.Errorf("model %s grants %s; OFFICE-Q1B-READS.md says this read is not needed", model, path)
			}
		}
	}
}

func q1bPreQ1BRules(home string) []PathRule {
	rwx := AccessRead | AccessWrite | AccessExecute
	rules := []PathRule{
		{Path: filepath.Clean(home), Access: rwx},
		{Path: "/tmp", Access: rwx},
	}
	if tmp := q1bExtraTemp(); tmp != "" {
		rules = append(rules, PathRule{Path: tmp, Access: rwx})
	}
	for _, path := range q1bPreQ1BReadOnlySystem {
		rules = append(rules, PathRule{Path: path, Access: AccessRead | AccessExecute})
	}
	rules = append(rules,
		PathRule{Path: "/dev/null", Access: AccessRead | AccessWrite},
		PathRule{Path: "/dev/shm", Access: AccessRead | AccessWrite},
	)
	return rules
}
