// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Tests the two guards guardCommand still runs: substitutionGuard
// (structural command-substitution judgement) and the path-containment
// scan's secret-set carve-out (checkPathSegment's inTurnSecretSet check via
// IsCarveOut) — exercised through guardCommand, the tool's own real entry
// point, rather than through their internals directly.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBashSafetyGuard_StructuralGuardsSurvive is the issue #767 two-sided
// oracle: ordinary prose must not trip the surviving guards, and the
// substitution/secret-path shapes those guards protect must remain blocked.
//
// restrict=true: the secret-path cases below are caught by
// checkPathSegment's IsCarveOut check (shell_path_guard.go), which — like
// the rest of the path-containment scan — only runs when restrictToWorkspace
// is true (guardCommand's own early return: "if !t.restrictToWorkspace {
// return \"\" }"). An unrestricted tool (restrict=false) has no text-level
// secret-path protection at all — the kernel sandbox is the real boundary
// there (ADR-091 D2) — so restrict=true is the realistic, default posture
// this test exercises.
func TestBashSafetyGuard_StructuralGuardsSurvive(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), true)
	require.NoError(t, err)

	benign := []struct {
		name string
		cmd  string
	}{
		{"backticked_template", "printf '%s\\n' 'Use the `template` key'"},
		{"system_in_prose", "printf '%s\\n' 'System status is healthy'"},
		{"config_filename_in_prose", "printf '%s\\n' 'Document config.json in the setup guide'"},
	}
	for _, tc := range benign {
		t.Run(tc.name, func(t *testing.T) {
			if msg := tool.guardCommand(context.Background(), tc.cmd, t.TempDir()); msg != "" {
				t.Errorf("benign command was blocked: %s\ncommand: %q", msg, tc.cmd)
			}
		})
	}

	dangerous := []struct {
		name string
		cmd  string
	}{
		{"dangerous_dollar_substitution", "echo $(find . -name '*.go')"},
		{"dangerous_backtick_substitution", "echo `find . -name '*.go'`"},
		{"omnipus_config_path", "cat ~/.omnipus/config.json"},
		{"omnipus_system_path", "cat ~/.omnipus/system/audit.jsonl"},
	}
	for _, tc := range dangerous {
		t.Run(tc.name, func(t *testing.T) {
			if msg := tool.guardCommand(context.Background(), tc.cmd, t.TempDir()); msg == "" {
				t.Errorf("SECURITY REGRESSION: dangerous command was allowed: %q", tc.cmd)
			}
		})
	}
}

// TestBashSafetyGuard_AcceptedD2ResidualRisk documents ADR-091 D2's accepted
// trade explicitly rather than leaving it implicit: `rm -rf`, a blanket
// `${...}` parameter expansion, and `sudo` inside the WORKSPACE are not
// blocked by any text guard (they never touch anything a kernel sandbox
// would deny either — a destructive-but-in-workspace command is exactly the
// class D2's own text names: "the same accepted trade Claude Code and Codex
// ship and document").
func TestBashSafetyGuard_AcceptedD2ResidualRisk(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	require.NoError(t, err)

	for _, cmd := range []string{
		"rm -rf build",
		"echo ${GITHUB_TOKEN:+YES}",
		"sudo -n true",
	} {
		t.Run(cmd, func(t *testing.T) {
			msg := tool.guardCommand(context.Background(), cmd, t.TempDir())
			require.Empty(t, msg,
				"ADR-091 D2 accepts this residual risk for an in-workspace command; "+
					"if this now fails, a guard was reintroduced that the ADR deliberately removed: %q", cmd)
		})
	}
}
