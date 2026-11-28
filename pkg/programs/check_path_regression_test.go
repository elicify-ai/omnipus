package programs

// FR005/S08: the actual check never starts an executable in writable roots,
// including an outside symlink into one. No validator injection or fake Service.
import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramCheck_PathRegression(t *testing.T) {
	for _, kind := range []string{"writable_directory", "outside_symlink_into_writable_directory"} {
		t.Run("F3_"+kind, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Fatal("BLOCKED: native Windows symlink/path child fixture not established — FR005/S08")
			}
			f := newCheckFixture(t, map[string]checkChildCase{"tok_valid_12345": {Label: "octocat"}})
			_, err := f.svc.Publish("github", "tok_valid_12345", nil, nil)
			require.NoError(t, err)
			require.Equal(t, "octocat", f.await(t, "github", "connected").AccountLabel, "instrument: this same executable started and completed when outside writable roots")
			started := filepath.Join(f.bin, "tok_valid_12345.started")
			require.FileExists(t, started)
			require.NoError(t, os.Remove(started))
			require.NoError(t, os.Remove(filepath.Join(f.bin, "tok_valid_12345.observation")))
			// Declare the real containing folder as this agent's writable home. The
			// filesystem and actual shared location check, not a mocked denial, decide.
			f.cfg.Agents.Defaults.Home = f.bin
			if kind == "outside_symlink_into_writable_directory" {
				outside := t.TempDir()
				require.NoError(t, os.Symlink(filepath.Join(f.bin, "gh"), filepath.Join(outside, "gh")))
				t.Setenv("PATH", outside)
			}
			_, err = f.svc.Recheck("github")
			require.NoError(t, err)
			f.svc.Wait()
			conn := f.svc.Connection("github")
			require.Equal(t, "saved_not_checked", conn.State, "FR005: gateway must not execute an agent-writable check path")
			// S08 specifies the state and an installation-location explanation, not
			// an exact sentence; do not copy implementation wording into an oracle.
			require.Contains(t, conn.Message, "installed", "S08: explain the refused installation location")
			require.NoFileExists(t, started, "actual rejected executable did not start")
			require.NoFileExists(t, filepath.Join(f.bin, "tok_valid_12345.observation"), "no token-bearing check child observation")
		})
	}
}
