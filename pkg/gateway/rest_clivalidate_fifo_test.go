//go:build !windows

// Unix-only coverage: a target that is NOT a regular file — a FIFO with the
// execute bit set — is path-not-allowed WITHOUT a spawn (T11 spec §3.2: the
// final file must be a regular executable; a refusal is path-not-allowed).
// An empty path stays missing-binary; a FIFO is not empty. Guarded off
// Windows because syscall.Mkfifo is unix-only.

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
// not a regular file. Spec §3.2 classifies that path-not-allowed and never
// starts it (execing a FIFO would block on open).
func TestValidateCLI_NonRegularExecutableRejected(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "claudefifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o755), "create an executable FIFO")

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	resp := api.validateCLI(context.Background(), "claude-code", fifo)
	assert.Equal(t, gen.CliValidateResponseReason("path-not-allowed"), resp.Reason,
		"spec §3.2: a non-regular file is path-not-allowed, not missing-binary")
	assert.Nil(t, resp.ResolvedPath, "no resolved path when nothing is launched")
}
