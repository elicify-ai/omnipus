//go:build darwin || linux

package thirdpartycli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder Q49 A: the per-user runtime temp grant is part of the writable union,
// including a symlink spelling. Shared /tmp is deliberately not the fixture.
func TestR8_RuntimeTempIsAgentWritable_Q49(t *testing.T) {
	for _, spelling := range []string{"ordinary", "symlink"} {
		t.Run(spelling, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(config.EnvHome, home)
			trusted := t11combined.TrustedProgramDir(t)
			control := t11WriteExe(t, trusted, "r8-trusted-control", 0o755)
			realTemp, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			temp := realTemp
			if spelling == "symlink" {
				temp = filepath.Join(t.TempDir(), "r8-runtime-temp-link")
				require.NoError(t, os.Symlink(realTemp, temp))
				require.NotEqual(t, realTemp, temp, "instrument: distinct runtime and resolved spellings")
			}
			t.Setenv("TMPDIR", temp)
			require.Equal(t, temp, os.TempDir(), "instrument: the runtime uses the controlled temp folder")
			require.NotContains(t, []string{"/tmp", "/private/tmp"}, realTemp)
			program := t11WriteExe(t, realTemp, "r8-runtime-program", 0o755)
			roots, rootsErr := thirdpartycli.AgentWritableRoots(home, &config.Config{})
			require.NoError(t, rootsErr)
			checked, err := thirdpartycli.CheckProgramPath("claude-code", control, roots, nil)
			require.NoError(t, err, "instrument: a trusted file outside runtime temp remains allowed")
			wantControl, err := filepath.EvalSymlinks(control)
			require.NoError(t, err)
			require.Equal(t, wantControl, checked)
			assert.Contains(t, roots, temp, "Q49: include the runtime spelling")
			assert.Contains(t, roots, realTemp, "Q49: include the resolved runtime spelling")
			resolved, checkErr := thirdpartycli.CheckProgramPath("claude-code", program, roots, nil)
			assert.Empty(t, resolved, "Q49: no launch identity for a runtime-temp program")
			var ce *thirdpartycli.CheckError
			if assert.ErrorAs(t, checkErr, &ce, "Q49: the real shared location check must refuse runtime temp") {
				assert.Equal(t, "agent_writable_location", ce.Check)
			}
		})
	}
}
