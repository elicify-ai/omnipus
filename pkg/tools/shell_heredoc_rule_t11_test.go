// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Q11 A narrows eligibility, not the normal guard's decisions. Expected values
// come from that ruling, FR-1/FR-2/FR-3, U-1 and the security shape conditions.
// NormalAllow is independently specified; the root-writable comparison proves
// the full unchanged-normal-decision relationship, not a copied output oracle.
type t11HereDocRuleCase struct {
	name, cmd                string
	allow, normalAllow       bool
	normalReason, normalPath string
}

func t11CheckHereDocRuleCase(t *testing.T, tool *ExecTool, cwd string, c t11HereDocRuleCase) {
	t.Helper()
	normal := func() string {
		restore := t11ForceRootWritable(t, true)
		defer restore()
		writable, err := rootDecision()
		require.NoError(t, err, "FR-3: normal-check comparison needs a successful root probe")
		require.True(t, writable, "FR-3: a writable root disables the literal exception")
		return tool.guardCommand(context.Background(), c.cmd, cwd)
	}()
	writable, err := rootDecision()
	require.NoError(t, err, "fixture: restored root probe must succeed")
	require.False(t, writable, "fixture: each actual decision uses the restored unwritable-root condition")
	got := tool.guardCommand(context.Background(), c.cmd, cwd)
	record, err := json.Marshal(map[string]any{
		"name": c.name, "command": c.cmd, "allow": c.allow, "normal_allow": c.normalAllow,
		"normal_reason": c.normalReason, "normal_path": c.normalPath, "normal": normal, "actual": got,
	})
	require.NoError(t, err, "fixture: record exact command and both decisions")
	t.Logf("Q11_CASE %s", record)
	if c.normalAllow {
		require.Equal(t, "", normal, "FR-2: this command passes the normal check; << is not a block list")
	} else {
		require.NotEmpty(t, normal, "FR-2/FR-3: the independently specified normal decision is refusal")
		if c.normalReason != "" {
			require.Contains(t, normal, c.normalReason, "FR-2: preserve the specified normal refusal reason")
		}
		if c.normalPath != "" {
			require.Contains(t, normal, `"`+c.normalPath+`"`, "FR-2: refusal concerns the specified path, not a sibling spelling")
		}
	}
	if c.allow {
		require.Equal(t, "", got, "Q11/FR-1: this approved envelope or normal allowance must not be refused\ncommand: %q", c.cmd)
	} else {
		require.NotEmpty(t, got, "Q11/FR-2: this nonmatching envelope or prior refusal must not receive the exception\ncommand: %q", c.cmd)
	}
	if c.allow == c.normalAllow {
		require.Equal(t, normal, got, "Q11: withholding the exception preserves the COMPLETE normal decision, not a blanket refusal")
	}
}

func TestGuard_HereDocumentWholeCommandRule(t *testing.T) {
	tool, cwd := guardFixture(t)
	t.Cleanup(t11ForceRootWritable(t, false))
	absent := t11AbsentTop(t)
	require.False(t, t11FirstExists(absent), "fixture: literal first component must not exist")
	require.False(t, t11FirstExists("/tm"), "fixture: proof fragment must have an absent first component")
	require.True(t, t11FirstExists("/tmp"), "fixture: proof's ordinary outside-path component must exist")
	cases := t11HereDocProgramCases(absent)
	cases = append(cases, t11HereDocMarkerCases(absent)...)
	cases = append(cases, t11HereDocBoundaryCases(absent)...)
	cases = append(cases, t11HereDocNormalAllowances()...)
	cases = append(cases, t11HereDocAdditionalCases(absent)...)
	proofs := t11HereDocProofCases()
	require.Len(t, proofs, 36, "fixture: preserve all 36 commissioned R11 proof inputs")
	cases = append(cases, proofs...)
	seen := make(map[string]bool)
	for _, c := range cases {
		require.False(t, seen[c.name], "fixture: each requirement/proof row has a unique name")
		seen[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			t11CheckHereDocRuleCase(t, tool, cwd, c)
		})
	}
}

func TestGuard_HereDocumentShapeKeepsPriorChecks(t *testing.T) {
	tool, cwd := guardFixture(t)
	t.Cleanup(t11ForceRootWritable(t, false))
	absent := t11AbsentTop(t)
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	credential := filepath.Join(home, "credentials.json")
	require.NoError(t, os.WriteFile(credential, []byte(`{"fixture":true}`), 0o600))
	wrap := func(body string) string { return "python3 <<'PY'\n" + body + "\nPY\n" }
	cases := []t11HereDocRuleCase{
		{"eligible literal still depends on root writability", wrap("print('" + absent + "/v1/users')"), true, false, "path outside working dir", absent + "/v1/users"},
		{"matching shape cannot allow a protected internal file", wrap("print('" + credential + "')"), false, false, "protected internal files", ""},
		{"matching shape cannot allow parent traversal", wrap("print('../shared/x')"), false, false, "path traversal detected", ""},
		{"matching shape cannot exempt an existing root component", wrap("print('/tmp/t11-q11-outside')"), false, false, "path outside working dir", "/tmp/t11-q11-outside"},
		{"matching shape cannot exempt adjacent fragments", wrap("print('/tm' 'p/x')"), false, false, "path outside working dir", "/tm"},
		{"absolute program position stays refused", "/bin/python3 <<'PY'\nprint('" + absent + "/v1/users')\nPY\n", false, false, "RUNNING a program", ""},
		{"workspace path remains an ordinary allowance", wrap("print('" + filepath.Join(cwd, "document.txt") + "')"), true, true, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t11CheckHereDocRuleCase(t, tool, cwd, c)
		})
	}
}
