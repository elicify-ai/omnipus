//go:build !windows && !cgo

// Unix-only coverage for the MAJ-004 pre-spawn guard: a target that
// exec.LookPath ACCEPTS (it reports X_OK) but is NOT a regular file — a FIFO
// with the execute bit set — must still be rejected missing-binary WITHOUT a
// spawn. This exercises the guard's second condition (resolved target is not a
// regular file), which LookPath alone does not catch. Guarded off Windows
// because syscall.Mkfifo is unix-only.

package gateway

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestValidateCLI_NonRegularExecutableRejected: a FIFO with the exec bit set is
// resolvable by exec.LookPath (X_OK succeeds, it is not a directory) yet is not a
// regular file, so the pre-spawn guard MUST classify missing-binary and never
// attempt to exec it (execing a FIFO would block on open).
func TestValidateCLI_NonRegularExecutableRejected(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "claudefifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o755), "create an executable FIFO")

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	resp := api.validateCLI(context.Background(), "claude-code", fifo)
	assert.Equal(t, gen.CliValidateResponseReasonMissingBinary, resp.Reason,
		"a non-regular executable (FIFO) must be rejected missing-binary without a spawn")
	assert.Nil(t, resp.ResolvedPath, "no resolved path on a no-spawn rejection")
}
