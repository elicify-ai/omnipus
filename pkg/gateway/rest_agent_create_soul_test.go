// Regression tests for the createAgent soul + worker-must-have-executor fixes.
//
// Background:
//   1. createAgent previously write-dropped req.Soul — the contract accepted
//      it, the FE sent it, but nothing landed on disk. A "draft" agent created
//      without a soul stayed in the draft state forever on the soul-empty path.
//   2. createAgent also did not enforce the FE-only rule that a worker must
//      declare an executor (workers run via delegation, not native).
//
// These tests prove:
//   - Worker create with `soul:"X"` → 201, response soul="X",
//     <workspace>/SOUL.md contains "X".
//   - Custom create with `soul:"X"` → 201, response soul="X",
//     <workspace>/SOUL.md contains "X".
//   - Worker create with no executor → 400.
//   - Worker create with executor omitted in custom creates (regression guard).

package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// readSoulMDForAgent returns the on-disk SOUL.md contents for the given agentID.
func readSoulMDForAgent(t *testing.T, api *restAPI, agentID string) string {
	t.Helper()
	workspace := filepath.Join(api.homePath, "agents", agentID)
	data, err := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	require.NoError(t, err, "expected SOUL.md at %s/SOUL.md", workspace)
	return string(data)
}
