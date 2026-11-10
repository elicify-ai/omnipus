//go:build darwin || linux

package tools

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Oracle: approved T11 spec §3.1 / FR-1 / U-1. Braces in Python data,
// including a two-entry dictionary, are not unquoted shell brace expansion.
// The guard is real; no shell or Python process is started by this test.
func TestT11U1_BraceLiteralsAreNotShellExpansion(t *testing.T) {
	tool, cwd := guardFixture(t)
	rootWritable := t11RootIsWritable(t)
	_, apiErr := os.Lstat("/api")
	if apiErr != nil && !errors.Is(apiErr, fs.ErrNotExist) {
		t.Fatalf("SETUP: cannot establish whether the API literal's top folder exists: %v", apiErr)
	}
	exemptionApplies := !rootWritable && errors.Is(apiErr, fs.ErrNotExist)
	t.Logf("independent U-1 prerequisites: root_writable=%v api_absent=%v exemption_applies=%v", rootWritable, errors.Is(apiErr, fs.ErrNotExist), exemptionApplies)
	for _, tc := range []struct {
		name, command string
	}{
		{"plain_literal_control", `python3 -c "print('/api/v1')"`},
		{"quoted_dictionary", `python3 -c "payload = {'api': '/api/v1', 'code': 200}; print(payload)"`},
		{"literal_heredoc_dictionary", "python3 <<'PY'\npayload = {\"api\": \"/api/v1\", \"code\": 200}\nprint(payload)\nPY\n"},
		{"stdin_heredoc_dictionary", "python3 - <<'PY'\npayload = {\"api\": \"/api/v1\", \"code\": 200}\nprint(payload)\nPY\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refusal := tool.guardCommand(context.Background(), tc.command, cwd)
			if exemptionApplies {
				require.Empty(t, refusal, "U-1 / FR-1: quoted or heredoc Python data must not be refused as a shell brace-expansion write: %s", tc.command)
			} else {
				require.Contains(t, refusal, "path outside working dir", "FR-2/FR-3: the narrow exemption is off when the root is writable or /api exists")
			}
		})
	}
}

// Oracle: T11 §3.1 / FR-2 / U-2. Fixing literal data must not open actual
// unquoted expansion, an outside home write, or adjacent-literal concatenation.
func TestT11U2_RealWritesRemainRefused(t *testing.T) {
	tool, cwd := guardFixture(t)
	for _, tc := range []struct {
		name, command string
	}{
		{"real_unquoted_brace_expansion", `printf x > {inside.txt,/tmp/t11-outside-brace.txt}`},
		{"outside_home_write", `echo x > ~/t11-outside.txt`},
		{"adjacent_literal_write", `python3 -c "open('/tm' 'p/t11probe','w')"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refusal := tool.guardCommand(context.Background(), tc.command, cwd)
			require.Contains(t, refusal, "path outside working dir", "U-2 / FR-2 must retain its outside-write refusal: %s", tc.command)
		})
	}
}

// Q73 A: only one byte-exact " -" extends the plain-program heredoc form.
// Shell/eval, extra flags and non-final/non-matching closing markers still
// withhold the exception. No command is executed by these real-guard tests.
func TestT11Q73_StrictHeadersAndEndMarkersRemainRefused(t *testing.T) {
	tool, cwd := guardFixture(t)
	_, tmErr := os.Lstat("/tm")
	if tmErr != nil && !errors.Is(tmErr, fs.ErrNotExist) {
		t.Fatalf("SETUP: cannot establish whether the brace fragment's top folder exists: %v", tmErr)
	}
	t.Logf("independent Q73 instrument: root_writable=%v tm_absent=%v", t11RootIsWritable(t), errors.Is(tmErr, fs.ErrNotExist))
	data := "\npayload = {\"api\": \"/api/v1\", \"code\": 200}\nprint(payload)\nX"
	cases := []struct {
		name, command string
	}{
		{"double_dash_option", "python3 -- <<'X'" + data},
		{"two_separate_dashes", "python3 - - <<'X'" + data},
		{"other_flag", "python3 -u <<'X'" + data},
		{"dash_and_other_flag", "python3 - -u <<'X'" + data},
		{"double_space_before_dash", "python3  - <<'X'" + data},
		{"tab_before_dash", "python3\t- <<'X'" + data},
		{"live_command_after_first_end_marker", "python3 - <<'X'" + data + "\npython3 -c \"print('/api/v1')\""},
		{"indented_end_marker_is_not_a_match", "python3 - <<'X'\npayload = {\"api\": \"/api/v1\"}\n X"},
		{"bash_dash_is_not_plain_program_data", "bash - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
		{"sh_dash_is_not_plain_program_data", "sh - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
		{"zsh_dash_is_not_plain_program_data", "zsh - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
		{"dash_dash_is_not_plain_program_data", "dash - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
		{"ksh_dash_is_not_plain_program_data", "ksh - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
		{"mksh_dash_is_not_plain_program_data", "mksh - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
		{"ash_dash_is_not_plain_program_data", "ash - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
		{"fish_dash_is_not_plain_program_data", "fish - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
		{"eval_dash_is_not_plain_program_data", "eval - <<'X'\nprintf x > {'/tm',}p/t11-q73-outside.txt\nX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refusal := tool.guardCommand(context.Background(), tc.command, cwd)
			require.Contains(t, refusal, "path outside working dir", "Q73 A keeps this header/end-marker form outside the literal exception: %s", tc.command)
		})
	}
}
