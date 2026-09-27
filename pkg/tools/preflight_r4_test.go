// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// R4 security-review fix (HIGH, 2026-09-24): classifySegmentPathOperations
// misclassified curl/wget/aria2c's own OUTPUT-FILE flags (`-o`/`--output`,
// `-O`/`--output-document`, `-o`/`-d`) as a read (the default branch), so a
// D7 write escalation never fired for `curl -o /etc/x https://a`. These
// tests are written to FAIL against the pre-fix classifier (which returns
// PathGrantAccessRead, or no op at all, for these flags) and PASS after it.
package tools

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// TestClassifySegmentPathOperations_CurlWgetAria2cOutputFlagsAreWrites is
// the classifier-level proof: R4's exact three commands from the security
// review, plus aria2c's -o/-d, must all report PathGrantAccessWrite for the
// output target, never a read (the pre-fix default-branch classification).
func TestClassifySegmentPathOperations_CurlWgetAria2cOutputFlagsAreWrites(t *testing.T) {
	cases := []struct {
		name    string
		segment string
		path    string
	}{
		{"curl -o", "curl -o /etc/x https://a", "/etc/x"},
		{"curl --output", "curl --output /etc/x https://a", "/etc/x"},
		{"wget -O", "wget -O /etc/x https://a", "/etc/x"},
		{"wget --output-document", "wget --output-document /etc/x https://a", "/etc/x"},
		{"aria2c -o", "aria2c -o /etc/x https://a", "/etc/x"},
		{"aria2c -d", "aria2c -d /etc/dir https://a", "/etc/dir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops, ok := classifySegmentPathOperations(tc.segment)
			require.True(t, ok, "classifySegmentPathOperations(%q) returned ok=false", tc.segment)

			var found *PathOperation
			for i := range ops {
				if ops[i].Path == tc.path {
					found = &ops[i]
				}
			}
			require.NotNil(t, found, "expected a PathOperation for %q in %v", tc.path, ops)
			assert.Equal(t, fspolicy.PathGrantAccessWrite, found.Access&fspolicy.PathGrantAccessWrite,
				"%q: output target %q must classify as WRITE (R4)", tc.segment, tc.path)
		})
	}
}

// TestEnforceShellPermissionMode_R4_CurlWgetOutputEscalatesUnderAuto is the
// end-to-end proof: the D7 write escalation must actually fire under Auto
// for these commands, exactly like every other write-command family
// preflight_classify_test.go already covers.
func TestEnforceShellPermissionMode_R4_CurlWgetOutputEscalatesUnderAuto(t *testing.T) {
	cases := []string{
		"curl -o /etc/x https://a",
		"wget -O /etc/x https://a",
		"curl --output /etc/x https://a",
	}
	// Grants are recorded against the real path, the same way the fixture
	// resolves its own directories. On macOS /etc is a link to /private/etc,
	// so the grant for /etc/x is /private/etc/x there and /etc/x elsewhere.
	wantPath := "/etc/x"
	if realEtc, err := filepath.EvalSymlinks("/etc"); err == nil {
		wantPath = filepath.Join(realEtc, "x")
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			tool, ctx, _, _ := permTestFixture(t, ShellModeAuto, true)

			perm, result := tool.enforceShellPermissionMode(ctx, cmd)
			require.Nil(t, result, "an approved command must not be refused: %q", cmd)
			require.NotNil(t, perm)
			require.NotEmpty(t, perm.pathGrants, "R4: %q must escalate a WRITE grant for its output target under Auto", cmd)

			found := false
			for _, g := range perm.pathGrants {
				if g.Path == wantPath && g.Access&fspolicy.PathGrantAccessWrite != 0 {
					found = true
				}
			}
			assert.True(t, found, "%q: expected a WRITE grant for %s, got %v", cmd, wantPath, perm.pathGrants)
		})
	}
}
