package t11combined

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK-ROUND2 F2: a crash bypasses t.Cleanup. Check actual Git visibility
// WHILE the fixture and a program are present, which is the same residue a
// crash leaves. This does not pretend that a crash ran normal cleanup. The
// parent separately proves normal cleanup still removes its unique directory.
func TestTrustedProgramDir_CrashResidueIsIgnoredAndNormalCleanupStillRuns(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	checkIgnored := func(path string) ([]byte, error) {
		command := exec.Command("git", "check-ignore", "--no-index", "--", path)
		command.Dir = cwd
		return command.CombinedOutput()
	}
	// Test the instrument with a known ignored path and an ordinary source path.
	ignoredControl := filepath.Join(cwd, "build", "gate-f2-ignored-control")
	output, err := checkIgnored(ignoredControl)
	require.NoError(t, err, "instrument: checked-in build/ ignore must be visible to Git: %s", output)
	require.Equal(t, ignoredControl+"\n", string(output))
	sourceControl := filepath.Join(cwd, "gate-f2-source-control.go")
	output, err = checkIgnored(sourceControl)
	var status *exec.ExitError
	require.ErrorAs(t, err, &status, "instrument: ordinary source must not be hidden")
	require.Equal(t, 1, status.ExitCode())
	require.Empty(t, output)
	var fixture string
	t.Run("residue_is_invisible_before_cleanup", func(t *testing.T) {
		fixture = TrustedProgramDir(t)
		program := filepath.Join(fixture, "fixture-program") // No *.exe ignore may mask a bad directory.
		require.NoError(t, os.WriteFile(program, []byte("test fixture, not executed\n"), 0o755))
		programOutput, programErr := checkIgnored(program)
		assert.NoError(t, programErr, "F2: abrupt termination must not leave this program looking like untracked source: %s", programOutput)
		assert.Equal(t, program+"\n", string(programOutput), "the real program is ignored while it still exists")
		assert.FileExists(t, program, "Git ignore, not premature deletion, explains the visibility result")
	})
	_, err = os.Stat(fixture)
	assert.ErrorIs(t, err, os.ErrNotExist, "normal owning-test cleanup must still remove the unique fixture directory")
}
