// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// D7 pre-flight write-classifier coverage (ADR-092): the classifier in
// preflight.go must recognize every common write-capable command, not just
// cp/rm/tee/`>`, or a command like `touch /etc/x` is silently misclassified
// as a read (open under ADR-063 D2 for a non-ReadConfined agent) and Auto
// mode never escalates for it — the kernel sandbox remains the real
// boundary, but the prompt itself should fire.
//
// These tests exercise the classifier end-to-end via
// enforceShellPermissionMode (the same entry point
// shell_permission_mode_test.go's DoD tests use, and permTestFixture
// defined there), not ClassifyPathOperations in isolation, so a pass here
// proves the whole D7 pipeline — classification, EvaluateFSPreflight
// containment, and the escalation prompt — agrees for each command.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnforceShellPermissionMode_D7ClassifiesCommonWriteCommands(t *testing.T) {
	cases := []struct {
		name         string
		cmd          func(workDir, outsideDir string) string
		wantEscalate bool
	}{
		{"touch outside workspace", func(w, o string) string {
			return "touch " + filepath.Join(o, "new.txt")
		}, true},
		{"touch inside workspace", func(w, o string) string {
			return "touch " + filepath.Join(w, "new.txt")
		}, false},

		{"mkdir outside workspace", func(w, o string) string {
			return "mkdir " + filepath.Join(o, "newdir")
		}, true},
		{"mkdir inside workspace", func(w, o string) string {
			return "mkdir " + filepath.Join(w, "newdir")
		}, false},

		{"ln outside workspace (linkname escalates)", func(w, o string) string {
			return "ln " + filepath.Join(w, "src") + " " + filepath.Join(o, "link")
		}, true},
		{"ln inside workspace", func(w, o string) string {
			return "ln " + filepath.Join(w, "src") + " " + filepath.Join(w, "link")
		}, false},

		{"chmod outside workspace", func(w, o string) string {
			return "chmod 755 " + filepath.Join(o, "f")
		}, true},
		{"chmod inside workspace", func(w, o string) string {
			return "chmod 755 " + filepath.Join(w, "f")
		}, false},

		{"chown outside workspace", func(w, o string) string {
			return "chown alice " + filepath.Join(o, "f")
		}, true},
		{"chown inside workspace", func(w, o string) string {
			return "chown alice " + filepath.Join(w, "f")
		}, false},

		{"truncate outside workspace", func(w, o string) string {
			return "truncate -s 0 " + filepath.Join(o, "f")
		}, true},
		{"truncate inside workspace", func(w, o string) string {
			return "truncate -s 0 " + filepath.Join(w, "f")
		}, false},

		{"install outside workspace (dest escalates)", func(w, o string) string {
			return "install " + filepath.Join(w, "src") + " " + filepath.Join(o, "dst")
		}, true},
		{"install inside workspace", func(w, o string) string {
			return "install " + filepath.Join(w, "src") + " " + filepath.Join(w, "dst")
		}, false},

		{"dd of= outside workspace", func(w, o string) string {
			return "dd if=/dev/zero of=" + filepath.Join(o, "f") + " bs=1 count=1"
		}, true},
		{"dd of= inside workspace", func(w, o string) string {
			return "dd if=/dev/zero of=" + filepath.Join(w, "f") + " bs=1 count=1"
		}, false},

		{"sed -i outside workspace", func(w, o string) string {
			return "sed -i s/a/b/ " + filepath.Join(o, "f")
		}, true},
		{"sed -i inside workspace", func(w, o string) string {
			return "sed -i s/a/b/ " + filepath.Join(w, "f")
		}, false},
		{"sed WITHOUT -i outside workspace is read-only, never escalates", func(w, o string) string {
			return "sed s/a/b/ " + filepath.Join(o, "f")
		}, false},

		{"rsync two local paths, dest outside escalates", func(w, o string) string {
			return "rsync -av " + filepath.Join(w, "src") + " " + filepath.Join(o, "dst")
		}, true},
		{"rsync two local paths, both inside", func(w, o string) string {
			return "rsync -av " + filepath.Join(w, "src") + " " + filepath.Join(w, "dst")
		}, false},
		{"scp remote source, local dest outside escalates", func(w, o string) string {
			return "scp user@host:/remote/file " + filepath.Join(o, "dst")
		}, true},
		{"scp remote source, local dest inside", func(w, o string) string {
			return "scp user@host:/remote/file " + filepath.Join(w, "dst")
		}, false},

		{"append redirect outside workspace", func(w, o string) string {
			return "echo hi >> " + filepath.Join(o, "f")
		}, true},
		{"append redirect inside workspace", func(w, o string) string {
			return "echo hi >> " + filepath.Join(w, "f")
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// requester is intentionally unused here: a network-capable head
			// (rsync/scp) also draws a D8 network pre-flight call independent
			// of D7 (see TestEnforceShellPermissionMode_NetworkDeniedByDefaultThenWidened),
			// so asserting on the aggregate approval-request count would
			// conflate the two. perm.pathGrants below is D7's own verdict,
			// which keeps this table a clean single-purpose proof of the
			// write classifier regardless of a command's network shape.
			tool, ctx, _, workDir := permTestFixture(t, ShellModeAuto, true)
			outsideDir, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)

			cmd := tc.cmd(workDir, outsideDir)
			perm, result := tool.enforceShellPermissionMode(ctx, cmd)
			require.Nil(t, result, "an approved (or already-contained) command must not be refused: %q", cmd)
			require.NotNil(t, perm)

			gotEscalate := len(perm.pathGrants) > 0
			assert.Equal(t, tc.wantEscalate, gotEscalate, "command %q: D7 escalation mismatch (pathGrants=%v)", cmd, perm.pathGrants)
		})
	}
}

// Read-only commands must never escalate under Auto, even against a path
// outside the workspace — SEC's "open reads" posture (ADR-063 D2) for a
// non-ReadConfined agent. A regression here would mean the write-command
// classifier additions above became overbroad and started flagging reads.
func TestEnforceShellPermissionMode_D7ReadOnlyCommandsNeverEscalate(t *testing.T) {
	cases := []struct {
		name string
		cmd  func(outsideDir string) string
	}{
		{"cat outside workspace", func(o string) string { return "cat " + filepath.Join(o, "f") }},
		{"ls outside workspace", func(o string) string { return "ls " + filepath.Join(o) }},
		{"grep outside workspace", func(o string) string { return "grep foo " + filepath.Join(o, "f") }},
		{"head outside workspace", func(o string) string { return "head " + filepath.Join(o, "f") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)
			outsideDir, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)

			cmd := tc.cmd(outsideDir)
			_, result := tool.enforceShellPermissionMode(ctx, cmd)
			require.Nil(t, result, "a read-only command must never be refused: %q", cmd)
			assert.Equal(t, 0, requester.callCount(), "read-only command %q must never escalate", cmd)
		})
	}
}
