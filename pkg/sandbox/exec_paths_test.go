// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sameDirectory reports whether two path spellings name the same directory on
// the filesystem under test. It is what makes the case-variance and symlink
// tests below GENUINE — they assert that the overlap guard drops a grant on a
// directory the kernel really can reach two ways, not merely that two strings
// compare a certain way.
func sameDirectory(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// The single invariant this whole feature rests on: an exec path is readable
// and executable, and NEVER writable. A directory that is both writable and
// executable would let an agent write a binary and then run it, which is the
// one capability the sandbox is meant to withhold.
func TestBuildExecPathRules_NeverGrantWrite(t *testing.T) {
	rules := buildExecPathRules(
		[]string{"/opt/toolchain", "/usr/local/bin"},
		nil,
		nil,
	)
	require.Len(t, rules, 2)

	for _, r := range rules {
		assert.Zero(t, r.Access&AccessWrite, "exec path %q must not be writable", r.Path)
		assert.NotZero(t, r.Access&AccessRead, "exec path %q must be readable (exec requires read to mmap)", r.Path)
		assert.NotZero(t, r.Access&AccessExecute, "exec path %q must be executable", r.Path)
	}
}

// DefaultPolicy is the real entry point, so assert the invariant there too —
// a future refactor could reintroduce write access by routing exec paths
// through the shared allowedPaths loop.
func TestDefaultPolicy_ExecPathsNeverCarryWriteBit(t *testing.T) {
	policy := DefaultPolicy(
		"/omnipus-test/home",
		[]string{"/omnipus-test/data"},
		[]string{"/omnipus-test/tools"},
		nil,
		nil,
	)

	var found bool
	for _, r := range policy.FilesystemRules {
		if r.Path == "/omnipus-test/tools" {
			found = true
			assert.Zero(t, r.Access&AccessWrite, "exec path must never be writable")
			assert.NotZero(t, r.Access&AccessExecute)
		}
	}
	assert.True(t, found, "the exec path should have produced a rule")
}

func TestBuildExecPathRules_RejectsUnsafeRoots(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"filesystem root", "/"},
		{"etc", "/etc"},
		{"etc subdirectory", "/etc/ssl"},
		{"proc", "/proc"},
		{"sys", "/sys"},
		{"dev", "/dev"},
		{"boot", "/boot"},
		{"root home", "/root"},
		{"relative path", "relative/tools"},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var warned []string
			rules := buildExecPathRules([]string{tc.path}, nil, func(_, p string) {
				warned = append(warned, p)
			})
			assert.Empty(t, rules, "%q must not produce an exec rule", tc.path)
			if tc.path != "" {
				assert.NotEmpty(t, warned, "a dropped entry must warn, not vanish silently")
			}
		})
	}
}

// Overlap is the one way this feature could manufacture a writable+executable
// directory, so the exec grant must lose in both nesting directions.
//
// The second argument is the set of paths that ACTUALLY received a write grant
// (derived by DefaultPolicy from the rules it built), not the operator's raw
// config — checking only the config missed the unconditional $OMNIPUS_HOME,
// /tmp and $TMPDIR grants.
func TestBuildExecPathRules_DropsOverlapWithWritablePaths(t *testing.T) {
	cases := []struct {
		name      string
		execPath  string
		writePath string
	}{
		{"identical", "/omnipus-test/tools", "/omnipus-test/tools"},
		{"exec inside writable", "/omnipus-test/tools/bin", "/omnipus-test/tools"},
		{"writable inside exec", "/omnipus-test/tools", "/omnipus-test/tools/bin"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var warned bool
			rules := buildExecPathRules(
				[]string{tc.execPath},
				[]string{tc.writePath},
				func(string, string) { warned = true },
			)
			assert.Empty(t, rules, "overlapping exec grant must be dropped")
			assert.True(t, warned, "the operator must be told why the grant was dropped")
		})
	}
}

// Finding A. The overlap guard used a case-SENSITIVE string compare, but macOS
// APFS is case-insensitive by default and Seatbelt matches (subpath …) the same
// way. So allowed_paths ".../work" plus allowed_exec_paths ".../WORK" named ONE
// directory, produced no warning, kept the exec grant, and yielded a directory
// that was writable AND executable — confirmed with a live sandbox-exec child
// that wrote a script there and ran it.
//
// Real directories, not string literals: on a case-insensitive volume the test
// additionally asserts the two spellings are the same inode, so the escalation
// being closed is the real one.
func TestBuildExecPathRules_DropsCaseVariantOverlap(t *testing.T) {
	base := t.TempDir()
	writable := filepath.Join(base, "work")
	require.NoError(t, os.MkdirAll(writable, 0o755))

	upper := filepath.Join(base, "WORK")
	if sameDirectory(t, writable, upper) {
		t.Log("volume is case-insensitive: .../work and .../WORK are the same directory")
	} else {
		t.Log("volume is case-sensitive: the drop below is the deliberate fail-safe bias")
	}

	for _, tc := range []struct {
		name     string
		execPath string
	}{
		{"upper exec vs lower writable", upper},
		{"mixed exec vs lower writable", filepath.Join(base, "Work")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var warned bool
			rules := buildExecPathRules(
				[]string{tc.execPath},
				[]string{writable},
				func(string, string) { warned = true },
			)
			assert.Empty(t, rules, "a case variant of a writable path must not get an exec grant")
			assert.True(t, warned, "the operator must be told why the grant was dropped")
		})
	}

	// And the reverse declaration order: writable spelled in upper case.
	var warned bool
	rules := buildExecPathRules(
		[]string{writable},
		[]string{upper},
		func(string, string) { warned = true },
	)
	assert.Empty(t, rules, "overlap must be caught whichever side carries the odd casing")
	assert.True(t, warned)
}

// Finding B. The guard compared the DECLARED strings while the profile renderer
// resolves symlinks, so allowed_exec_paths pointing at a symlink into a writable
// tree passed the check and rendered as read+exec on the writable target. This
// is not hypothetical: the shipped seed includes ~/.local/bin and ~/go/bin,
// which dotfile managers routinely symlink into a managed (and writable) tree.
func TestBuildExecPathRules_DropsSymlinkedOverlap(t *testing.T) {
	base := t.TempDir()
	writable := filepath.Join(base, "writable")
	realBin := filepath.Join(writable, "bin")
	require.NoError(t, os.MkdirAll(realBin, 0o755))

	link := filepath.Join(base, "toolbin")
	require.NoError(t, os.Symlink(realBin, link))
	require.True(t, sameDirectory(t, link, realBin), "the symlink must really point into the writable tree")

	t.Run("exec declared via symlink", func(t *testing.T) {
		var warned bool
		rules := buildExecPathRules(
			[]string{link},
			[]string{writable},
			func(string, string) { warned = true },
		)
		assert.Empty(t, rules, "a symlink into a writable tree must not get an exec grant")
		assert.True(t, warned)
	})

	t.Run("writable declared via symlink", func(t *testing.T) {
		var warned bool
		rules := buildExecPathRules(
			[]string{realBin},
			[]string{link},
			func(string, string) { warned = true },
		)
		assert.Empty(t, rules, "overlap must be caught when the WRITABLE side is the symlink")
		assert.True(t, warned)
	})

	t.Run("nested under the symlink", func(t *testing.T) {
		rules := buildExecPathRules(
			[]string{filepath.Join(link, "nested", "not-created-yet")},
			[]string{writable},
			nil,
		)
		assert.Empty(t, rules, "resolution must reach through a symlink to paths that do not exist yet")
	})

	// Control: a symlink to a directory OUTSIDE every writable root keeps its
	// grant. Without this, "drop everything" would pass the three cases above.
	outside := filepath.Join(base, "outside-tools")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	safeLink := filepath.Join(base, "safelink")
	require.NoError(t, os.Symlink(outside, safeLink))

	rules := buildExecPathRules([]string{safeLink}, []string{writable}, nil)
	require.Len(t, rules, 1, "a symlink outside the writable tree is still a legitimate exec path")
	assert.Equal(t, safeLink, rules[0].Path, "the DECLARED path is what goes in the rule; the renderer resolves it")
}

// Finding C. /private/tmp/x and /private/var/folders/…/T/x are the real
// locations of the built-in /tmp and $TMPDIR writable roots, but they do not
// match them textually, so the exec grant was kept with no warning. No
// privilege gain over declaring /tmp directly — but silent, which is worse than
// the loud drop the operator gets for the equivalent /tmp spelling.
func TestDefaultPolicy_DropsExecPathOnPrivateAliasOfWritableRoot(t *testing.T) {
	aliases := map[string]string{}
	for _, root := range []string{"/tmp", filepath.Clean(os.TempDir())} {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil || resolved == root {
			continue
		}
		aliases[root] = resolved
	}
	if len(aliases) == 0 {
		t.Skip("no writable root on this platform is reached through a symlink; nothing to alias")
	}

	for root, resolved := range aliases {
		t.Run(root, func(t *testing.T) {
			execPath := filepath.Join(resolved, "omnipus-exec-alias")
			var warned bool
			policy := DefaultPolicy(
				"/omnipus-test/home",
				nil,
				[]string{execPath},
				func(string, string) { warned = true },
				nil,
			)
			for _, r := range policy.FilesystemRules {
				if r.Path == execPath {
					t.Fatalf("%q is the real location of the writable root %q and must not get an exec grant: %+v",
						execPath, root, r)
				}
			}
			assert.True(t, warned, "the drop must be explained, not silent")
		})
	}
}

// Finding D. SystemRestrictedPaths is declared in Linux terms, but on macOS the
// real /etc is /private/etc, and a case-insensitive volume also reaches it as
// /ETC or /Private/Etc. isSystemRestricted matched literal strings, so
// allowed_paths ["/ETC"] rendered (allow file-write* (subpath "/private/etc")):
// the sandbox permitted the write and only Unix file ownership refused it. A
// gateway running as root would have written to /etc.
func TestIsSystemRestricted_CoversPlatformAliasesAndCase(t *testing.T) {
	// Case variants of the declared list work on every platform, because the
	// comparison folds case unconditionally (fail-safe by design).
	for _, p := range []string{"/ETC", "/Etc", "/etc/SSL", "/ROOT/.ssh", "/Dev/sda1", "/BOOT/grub"} {
		assert.True(t, isSystemRestricted(p), "%q is a case variant of a restricted path", p)
	}

	// Platform aliases are derived, not hard-coded: whatever each declared entry
	// resolves to on THIS filesystem must also be restricted, in any casing.
	var checkedAlias bool
	for _, declared := range SystemRestrictedPaths {
		resolved, err := filepath.EvalSymlinks(declared)
		if err != nil || resolved == declared {
			continue
		}
		checkedAlias = true
		assert.True(t, isSystemRestricted(resolved),
			"%q is the real location of %q and must be restricted", resolved, declared)
		assert.True(t, isSystemRestricted(strings.ToUpper(resolved)),
			"%q is a case variant of the real location of %q", strings.ToUpper(resolved), declared)
		assert.True(t, isSystemRestricted(filepath.Join(resolved, "child")),
			"children of the real location must be restricted too")
	}
	if !checkedAlias {
		t.Log("no restricted path is reached through a symlink on this platform (expected on Linux)")
	}

	// The other half of the contract, and the reason /private/var is NOT in the
	// restricted set: the workspace and temp directories must stay writable.
	// /var is where macOS keeps $TMPDIR (/private/var/folders/…), so sweeping in
	// the whole /private alias tree would have turned the per-user temp dir
	// read-only and broken mktemp, npm, pip, git and `go build` under the sandbox.
	tmpDir := filepath.Clean(os.TempDir())
	mustStayWritable := []string{
		"/tmp",
		"/TMP",
		tmpDir,
		filepath.Join(tmpDir, "session-scratch"),
		t.TempDir(),
		"/var/log",
		"/private",
		"/private/var",
		"/private/var/folders",
		"/private/tmp",
		"/etcetera",
		"/etc-backup",
		"/home/user",
		"/Users/someone/work",
	}
	for _, p := range mustStayWritable {
		assert.False(t, isSystemRestricted(p), "%q must NOT be treated as a restricted system path", p)
	}
	if resolvedTmp, err := filepath.EvalSymlinks(tmpDir); err == nil {
		assert.False(t, isSystemRestricted(resolvedTmp),
			"the resolved per-user temp dir %q must stay writable", resolvedTmp)
	}
}

// The end-to-end half of finding D: an alias or case variant of a restricted
// path reaching DefaultPolicy must lose its write bit, exactly as the literal
// spelling does.
func TestDefaultPolicy_StripsWriteOnAliasedRestrictedPath(t *testing.T) {
	candidates := []string{"/ETC", "/Etc/ssl"}
	if resolved, err := filepath.EvalSymlinks("/etc"); err == nil && resolved != "/etc" {
		candidates = append(candidates, resolved, strings.ToUpper(resolved))
	}

	for _, p := range candidates {
		t.Run(p, func(t *testing.T) {
			var warned bool
			policy := DefaultPolicy("/omnipus-test/home", []string{p}, nil,
				func(string, string) { warned = true }, nil)

			var found bool
			for _, r := range policy.FilesystemRules {
				if r.Path != p {
					continue
				}
				found = true
				assert.Zero(t, r.Access&AccessWrite,
					"%q reaches /etc; the write bit must be stripped", p)
				assert.NotZero(t, r.Access&AccessRead, "read intent is preserved")
			}
			require.True(t, found, "the declared path should still produce a (read-only) rule")
			assert.True(t, warned, "stripping write must be reported to the operator")
		})
	}
}

func TestBuildExecPathRules_KeepsNonOverlappingPaths(t *testing.T) {
	rules := buildExecPathRules(
		[]string{"/omnipus-test/tools"},
		[]string{"/omnipus-test/data"},
		nil,
	)
	require.Len(t, rules, 1)
	assert.Equal(t, "/omnipus-test/tools", rules[0].Path)
}

// A leading ~ must be expanded here. If an unexpanded "~/..." reached the
// Seatbelt renderer it would be rejected as non-absolute, the policy render
// would fail, and applySandbox fails closed — so a cosmetic omission would
// brick boot on every macOS install carrying the seeded default.
func TestExpandUserPath_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, ok := expandUserPath("~/.cargo/bin")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(home, ".cargo/bin"), got)
	assert.True(t, filepath.IsAbs(got), "expanded path must be absolute")

	bare, ok := expandUserPath("~")
	require.True(t, ok)
	assert.Equal(t, filepath.Clean(home), bare)
}

func TestExpandUserPath_RejectsNonAbsolute(t *testing.T) {
	for _, p := range []string{"", "  ", "tools/bin", "./tools", "../tools"} {
		_, ok := expandUserPath(p)
		assert.False(t, ok, "%q must be rejected as non-absolute", p)
	}
}

// The whole point of expanding ~ is that the rendered profile never contains
// one. This asserts the end-to-end property through DefaultPolicy.
func TestDefaultPolicy_ExecPathsAreAbsoluteAfterExpansion(t *testing.T) {
	policy := DefaultPolicy("/omnipus-test/home", nil, []string{"~/.cargo/bin"}, nil, nil)

	var seen bool
	for _, r := range policy.FilesystemRules {
		if r.Access&AccessExecute != 0 && r.Access&AccessWrite == 0 {
			assert.True(t, filepath.IsAbs(r.Path), "rule path %q must be absolute", r.Path)
			assert.NotContains(t, r.Path, "~", "rule path %q must not contain an unexpanded tilde", r.Path)
			seen = true
		}
	}
	assert.True(t, seen, "expected a read+exec rule from the tilde path")
}
