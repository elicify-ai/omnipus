// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGuard_HereDocumentLogicalBoundaries retains the R10/R11 shell-derived
// input shapes (GNU Bash section 3.6.6 and harmless Bash/dash probes). Q11 A
// supersedes their former broad eligibility: only the complete plain quoted
// envelope retains the exception. Every other shape uses the normal check,
// even when a particular shell would regard the remaining text as body data.
// Fixtures reach only the real guard; no fixture command is executed.
func TestGuard_HereDocumentLogicalBoundaries(t *testing.T) {
	tool, cwd := guardFixture(t)
	t.Cleanup(t11ForceRootWritable(t, false))
	writable, err := rootDecision()
	require.NoError(t, err, "fixture: the controlled root probe must succeed")
	require.False(t, writable, "fixture: the literal exemption requires an unwritable root")
	require.False(t, t11FirstExists("/tm"), "fixture: /tm must be absent for the S5c literal")
	absent := t11AbsentTop(t)
	require.False(t, t11FirstExists(absent), "fixture: the stand-alone literal's first component must be absent")

	continuation := string([]byte{'\\', '\n'})
	require.Equal(t, []byte{'\\', '\n'}, []byte(continuation), "fixture: actual backslash/newline bytes")
	joined := "EO" + continuation + "F\n"
	require.Equal(t, "EOF\n", strings.ReplaceAll(joined, continuation, ""), "fixture: the joined delimiter is EOF")
	evenEnding := string([]byte{'\\', '\\', '\n'})
	require.Equal(t, []byte{'\\', '\\', '\n'}, []byte(evenEnding), "fixture: even backslash parity before newline")
	literal := "print('" + absent + "/v1/users')\n"
	body := literal + "value = 1 + " + continuation + "2\n"
	tail := `echo "echo {'/tm',}p/x" | ba` + continuation + "sh"
	physical := "python3 <<EOF\nnote\nEOF\n" + tail
	nested := "bash -c '" + strings.ReplaceAll(tail, "'", "'\\''") + "'"
	backslashEnd := "EOF" + continuation
	singleBackslash := "python3 <<'EOF\\'\nnote\n"
	doubleBackslash := "python3 <<\"EOF\\\\\"\nnote\n"

	cases := []struct {
		name, cmd string
		refuse    bool
		rawPairs  int
	}{
		{"unquoted two-line delimiter refuses",
			"python3 <<EOF\nnote\n" + joined + tail, true, 2},
		{"physical delimiter refuses",
			physical, true, 1},
		{"nested script continuation refuses",
			nested, true, 1},
		{"unquoted body continuation uses normal check",
			"python3 <<PY\n" + body + "PY\n", true, 1},
		{"quoted multiline body stays allowed",
			"python3 <<'PY'\n" + body + "PY\n", false, 1},
		{"unclosed single-quoted document uses normal check",
			"python3 <<'EOF'\nnote\n" + joined + tail, true, 2},
		{"double-quoted delimiter uses normal check",
			"python3 <<\"EOF\"\nnote\n" + joined + tail, true, 2},
		{"three-line delimiter refuses",
			"python3 <<EOF\nnote\nE" + continuation + "O" + continuation + "F\n" + tail, true, 3},
		{"tab-stripped joined delimiter refuses",
			"python3 <<-EOF\nnote\n\tEO" + continuation + "F\n" + tail, true, 2},
		{"interior-tab document uses normal check",
			"python3 <<-EOF\nnote\n\tEO" + continuation + "\tF\n" + tail + "\n\tEOF\n", true, 2},
		{"unquoted even-backslash document uses normal check",
			"python3 <<EOF\nnote\nEO" + evenEnding + "F\n" + tail + "\nEOF\n", true, 2},
		{"joined delimiter at input end uses normal check",
			"python3 <<EOF\n" + literal + "EO" + continuation + "F", true, 1},
		{"terminal pair at input end uses normal check",
			"python3 <<EOF\n" + literal + "EOF" + continuation, true, 1},
		{"continued delimiter word refuses",
			"python3 <<EO" + continuation + "F\nnote\nEOF\n" + tail, true, 2},
		{"single-quoted backslash physical end refuses",
			singleBackslash + backslashEnd + tail, true, 2},
		{"single-quoted backslash body uses normal check",
			singleBackslash + joined + tail + "\n" + backslashEnd, true, 3},
		{"double-quoted backslash physical end refuses",
			doubleBackslash + backslashEnd + tail, true, 2},
		{"double-quoted backslash body uses normal check",
			doubleBackslash + joined + tail + "\n" + backslashEnd, true, 3},
	}

	ctx := context.Background()
	physicalRefusal := tool.guardCommand(ctx, physical, cwd)
	require.Contains(t, physicalRefusal, "path outside working dir", "FR-2/S5c: physical-boundary control must refuse")
	require.Contains(t, physicalRefusal, `"/tm"`, "fixture: the reference refusal must concern the S5c literal")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Raw byte-pair counts include an escaped pair in the even-parity case.
			require.Equal(t, c.rawPairs, strings.Count(c.cmd, continuation), "fixture: raw backslash/newline pair count")
			t.Logf("T11_BOUNDARY name=%q refuse=%t command=%q", c.name, c.refuse, c.cmd)
			if c.refuse && strings.Contains(c.cmd, absent+"/v1/users") {
				// Q11 preserves this command's complete normal refusal. A
				// one-line reference names a different command segment.
				t11CheckHereDocRuleCase(t, tool, cwd, t11HereDocRuleCase{
					c.name, c.cmd, false, false, "path outside working dir", absent + "/v1/users",
				})
				return
			}
			got := tool.guardCommand(ctx, c.cmd, cwd)
			if c.refuse {
				path, reference := "/tm", physicalRefusal
				require.NotEmpty(t, got, "Q11/FR-2: the unsupported envelope or prior shell refusal uses the normal check\ncommand: %q", c.cmd)
				require.Contains(t, got, "path outside working dir", "FR-2: retain the existing path refusal")
				require.Contains(t, got, `"`+path+`"`, "FR-2: refusal concerns the specified literal")
				require.Equal(t, reference, got, "Q11/FR-2: preserve the complete independently checked normal refusal")
				return
			}
			require.Equal(t, "", got, "Q11/FR-1: the approved whole-command quoted envelope retains the literal allowance\ncommand: %q", c.cmd)
		})
	}
}
