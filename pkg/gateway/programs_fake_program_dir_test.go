package gateway

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// pgOriginalTemp is the temp folder this test process started with, read once at
// package initialisation, before any test moves the temp variables.
var pgOriginalTemp = os.TempDir()

// pgFakeProgramDir returns a new folder for a fake program that the gateway's
// account check has to run (spec S-08, FR-005).
//
// The check refuses a program that lies in a folder agents can write to, and the
// per-user temp folder counts as one wherever it is not the shared /tmp
// (pkg/thirdpartycli/executor_settings.go::runtimeTempRoots, which reads
// os.TempDir() when the check runs). A folder made with t.TempDir() therefore
// passes where TMPDIR is unset (GitHub CI, the build runners) and is refused
// where it is set (the cloud pods, the Fly CI cluster, macOS): the fake never
// starts, a row waiting for "connected" waits for a state that cannot come, and
// a "nothing started" claim can no longer fail.
//
// The folder is made under the temp folder the process started with. TMPDIR, TMP
// and TEMP then point at a scratch folder next to it for the rest of the calling
// (sub)test, so the temp root the rule derives is the scratch folder and the fake
// lies outside it, on every host. Each call returns a new folder and a new scratch
// folder; both are removed when the (sub)test ends, after the variables are restored.
func pgFakeProgramDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(pgOriginalTemp, "fakeprog-")
	require.NoError(t, err, "instrument: the fake program folder is made under the original temp folder")
	scratch, err := os.MkdirTemp(pgOriginalTemp, "scratch-")
	require.NoError(t, err, "instrument: the scratch temp folder is made under the original temp folder")
	t.Cleanup(func() {
		for _, folder := range []string{dir, scratch} {
			if rmErr := os.RemoveAll(folder); rmErr != nil {
				t.Errorf("the test folder %q could not be removed: %v", folder, rmErr)
			}
		}
	})
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, scratch)
	}
	return dir
}
