// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
