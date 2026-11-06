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

// TestGuard_HereDocumentLogicalBoundaries covers T11 section 3.1, FR-1/FR-2
// and S5c. Boundary oracles are GNU Bash Reference Manual section 3.6.6,
// independent bash -n checks and harmless Bash/dash boundary probes. Quoting,
// tab placement, backslash parity and end-of-input distinguish document data
// from following shell text.
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
		{"unquoted body continuation stays allowed",
			"python3 <<PY\n" + body + "PY\n", false, 1},
		{"quoted multiline body stays allowed",
			"python3 <<'PY'\n" + body + "PY\n", false, 1},
		{"single-quoted delimiter keeps data allowed",
			"python3 <<'EOF'\nnote\n" + joined + tail, false, 2},
		{"double-quoted delimiter keeps data allowed",
			"python3 <<\"EOF\"\nnote\n" + joined + tail, false, 2},
		{"three-line delimiter refuses",
			"python3 <<EOF\nnote\nE" + continuation + "O" + continuation + "F\n" + tail, true, 3},
		{"tab-stripped joined delimiter refuses",
			"python3 <<-EOF\nnote\n\tEO" + continuation + "F\n" + tail, true, 2},
		{"interior tab keeps document data allowed",
			"python3 <<-EOF\nnote\n\tEO" + continuation + "\tF\n" + tail + "\n\tEOF\n", false, 2},
		{"even backslashes keep document data allowed",
			"python3 <<EOF\nnote\nEO" + evenEnding + "F\n" + tail + "\nEOF\n", false, 2},
		{"joined delimiter at input end stays allowed",
			"python3 <<EOF\n" + literal + "EO" + continuation + "F", false, 1},
		{"terminal pair at input end stays allowed",
			"python3 <<EOF\n" + literal + "EOF" + continuation, false, 1},
		{"continued delimiter word refuses",
			"python3 <<EO" + continuation + "F\nnote\nEOF\n" + tail, true, 2},
		{"single-quoted backslash physical end refuses",
			singleBackslash + backslashEnd + tail, true, 2},
		{"single-quoted backslash body stays allowed",
			singleBackslash + joined + tail + "\n" + backslashEnd, false, 3},
		{"double-quoted backslash physical end refuses",
			doubleBackslash + backslashEnd + tail, true, 2},
		{"double-quoted backslash body stays allowed",
			doubleBackslash + joined + tail + "\n" + backslashEnd, false, 3},
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
			got := tool.guardCommand(ctx, c.cmd, cwd)
			if c.refuse {
				require.NotEmpty(t, got, "FR-2/S5c: this boundary leaves shell text that must refuse\ncommand: %q", c.cmd)
				require.Contains(t, got, "path outside working dir", "FR-2: retain the existing path refusal")
				require.Contains(t, got, `"/tm"`, "FR-2: refusal concerns the S5c literal")
				require.Equal(t, physicalRefusal, got, "FR-2/S5c: equivalent shell text has the complete physical-boundary refusal")
				return
			}
			require.Equal(t, "", got, "FR-1: non-shell document data retains the literal allowance\ncommand: %q", c.cmd)
		})
	}
}
