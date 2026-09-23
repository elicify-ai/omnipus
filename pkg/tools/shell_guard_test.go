// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// ADR-091 D2 deletes the regex block-list layer (defaultDenyPatterns/
// secretGuardPatterns/applyDenyPatterns/compileDenyPatterns/
// denyPatternMessage) this file used to test directly. What survives —
// substitutionGuard (structural command-substitution judgement) and the
// path-containment scan's secret-set carve-out (checkPathSegment's
// inTurnSecretSet check via IsCarveOut) — is exercised below through
// guardCommand, the tool's own real entry point, rather than through the
// deleted internals.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBashSafetyGuard_StructuralGuardsSurvive is the issue #767 two-sided
// oracle, narrowed to what ADR-091 D2 actually left behind: ordinary prose
// must not trip the surviving guards, and the substitution/secret-path
// shapes those guards protect must remain blocked.
//
// restrict=true (unlike the pre-ADR-091 version of this test, which used
// restrict=false): the secret-path cases below are caught by
// checkPathSegment's IsCarveOut check (shell_path_guard.go), which — like
// the rest of the surviving path-containment scan — only runs when
// restrictToWorkspace is true (guardCommand's own early return: "if
// !t.restrictToWorkspace { return \"\" }"). Pre-ADR-091, the now-deleted
// secretGuardPatterns regex ran UNCONDITIONALLY ahead of that check, which is
// what let the old restrict=false fixture still catch these two cases — an
// accepted narrowing D2 makes explicit (the ADR's own words: "the kernel
// sandbox is the real boundary" for an unrestricted tool; restrict=true is
// the realistic, default posture this test now exercises).
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
// trade rather than silently dropping the coverage assertion: `rm -rf` and a
// blanket `${...}` parameter expansion inside the WORKSPACE are no longer
// blocked by any text guard (they never touched anything a kernel sandbox
// would deny either — a destructive-but-in-workspace command is exactly the
// class D2's own text names: "the same accepted trade Claude Code and Codex
// ship and document"). This is the honest replacement for what the deleted
// defaultDenyPatterns regressions used to pin as "must stay blocked."
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
