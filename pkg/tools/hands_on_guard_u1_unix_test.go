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
