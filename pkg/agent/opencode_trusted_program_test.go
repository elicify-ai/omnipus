package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/require"
)

// Keep the existing CLI fixture's bytes, executable mode and spaced install
// path, but install its test copy outside the agent-writable runtime temp root.
// Neither the production writable-root policy nor TMPDIR/TMP/TEMP is changed.
// This helper is deliberately limited to the OpenCode reconciliation tests;
// other l11274Executable callers retain their existing location semantics.
func opencodeTrustedProgram(t *testing.T, cli string) string {
	t.Helper()
	source := l11274Executable(t, "", cli, true)
	content, err := os.ReadFile(source)
	require.NoError(t, err)
	dir := filepath.Join(t11combined.TrustedProgramDir(t), "fake CLI installs")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	program := filepath.Join(dir, filepath.Base(source))
	require.NoError(t, os.WriteFile(program, content, 0o700))
	_, err = exec.LookPath(program)
	require.NoError(t, err, "instrument: the trusted copy is still an executable program")
	return program
}
