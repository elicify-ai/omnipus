//go:build darwin || linux

package agent

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec §3.2 and §4: both initial and continued native runs use the checked
// program. Real local protocol fixtures record their own work and probe starts.
func TestExternalDispatch_RunsCheckedProgram(t *testing.T) {
	for _, spec := range t11bNativeCLIs() {
		t.Run(spec.cli, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			t.Setenv(config.EnvHome, home)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("CODEX_HOME", t.TempDir())
			al, ts := newExternalTestLoop(t, spec.cli, "")
			ts.agent.MaxIterations = extTestEffectiveLimit
			checkedSource, checkedMarker := t11bNativeProgram(t, spec, "selected-fixture")
			otherSource, otherMarker := t11bNativeProgram(t, spec, "other-fixture")
			dir, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			other := filepath.Join(dir, "fixture-cli")
			checked := other + " "
			for _, fixture := range []struct{ source, path string }{{checkedSource, checked}, {otherSource, other}} {
				data, readErr := os.ReadFile(fixture.source)
				require.NoError(t, readErr)
				require.NoError(t, os.WriteFile(fixture.path, data, 0o755))
			}
			checkedInfo, err := os.Stat(checked)
			require.NoError(t, err)
			otherInfo, err := os.Stat(other)
			require.NoError(t, err)
			require.False(t, os.SameFile(checkedInfo, otherInfo), "instrument: distinct fixture files")
			t11bSaveRunSettings(t, al, spec.cli, checked, "")
			roots, rootsErr := thirdpartycli.AgentWritableRoots(home, al.GetConfig())
			require.NoError(t, rootsErr)
			resolved, err := thirdpartycli.CheckProgramPath(spec.cli, checked, roots, rootsErr)
			require.NoError(t, err, "instrument: the real shared check accepts this exact fixture")
			want, err := filepath.EvalSymlinks(checked)
			require.NoError(t, err)
			require.Equal(t, want, resolved)
			require.NotEqual(t, other, resolved)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second) // established native-fixture bound
			defer cancel()
			for i, phase := range []string{"normal", "continued"} {
				t.Run(phase, func(t *testing.T) {
					ts.opts.ExternalCLIResume = i == 1
					_, runErr := runExternalCLISubTurn(ctx, al, ts, "fixed local fixture conversation")
					require.NoError(t, runErr, "instrument: the real native conversation completes")
					checkedCalls := t11bNativeCalls(t, checkedMarker)
					otherCalls := t11bNativeCalls(t, otherMarker)
					assert.Len(t, checkedCalls, i+1, "spec §3.2: each native turn executes the checked program")
					assert.Empty(t, otherCalls, "spec §3.2: another program never performs native work")
					_, markerErr := os.Stat(checkedMarker + ".all")
					assert.NoError(t, markerErr, "the selected file records its own starts")
					_, markerErr = os.Stat(otherMarker + ".all")
					assert.ErrorIs(t, markerErr, fs.ErrNotExist, "another file must not run, even as a version probe")
					calls := append(append([][]string(nil), checkedCalls...), otherCalls...)
					require.Len(t, calls, i+1, "instrument: actual native work was performed")
					if i == 1 {
						assert.Contains(t, strings.Join(calls[1], "\x00"), strings.Join(spec.resumeTokens, "\x00"),
							"FR-043: the continued leg resumes the same native conversation")
					}
				})
			}
		})
	}
}
