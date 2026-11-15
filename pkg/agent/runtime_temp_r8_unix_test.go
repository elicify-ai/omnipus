//go:build darwin || linux

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder Q49 A / FR-9: a real native continuation rechecks runtime temp before
// every process, including --version. Keep the saved path and session unchanged;
// only its directory's runtime write grant changes after the positive control.
func TestR8_RuntimeTempProgramRefusedBeforeActualDispatch_Q49(t *testing.T) {
	for _, spelling := range []string{"ordinary", "symlink"} {
		t.Run(spelling, func(t *testing.T) {
			f := t11cNativeFixtureFor(t, nil)
			temp, err := filepath.EvalSymlinks(filepath.Dir(f.program))
			require.NoError(t, err)
			if spelling == "symlink" {
				alias := filepath.Join(t.TempDir(), "r8-runtime-temp-alias")
				require.NoError(t, os.Symlink(temp, alias))
				require.NotEqual(t, temp, alias, "instrument: runtime spelling differs from the checked file spelling")
				temp = alias
			}
			t.Setenv("TMPDIR", temp)
			require.Equal(t, temp, os.TempDir(), "instrument: the program now lies in actual runtime temp")
			f.ts.opts.ExternalCLIResume = true
			_, runErr := runExternalCLISubTurn(f.ctx, f.al, f.ts, "same saved program under the runtime temp grant")
			if assert.Error(t, runErr, "Q49: actual dispatch must refuse the runtime-temp program") {
				assert.Regexp(t, `^This agent cannot run: `, runErr.Error())
				assert.Contains(t, runErr.Error(), "Codex")
				assert.Contains(t, runErr.Error(), "agent_writable_location", "Q49: writable location is the failure class")
			}
			assert.Equal(t, f.beforeCalls, t11bNativeCalls(t, f.marker), "Q49: no additional native work process")
			afterStarts, err := os.ReadFile(f.marker + ".all")
			require.NoError(t, err)
			assert.Equal(t, f.beforeStarts, afterStarts, "Q49: no further process start, including version probes")
		})
	}
}
