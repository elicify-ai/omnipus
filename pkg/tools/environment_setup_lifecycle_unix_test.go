//go:build !windows

package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is ordinary Bash lifecycle behavior, not a second supervisor: the
// existing timeout group-kill reaches an inherited child in the same group.
func TestEnvironmentSetup_TimeoutReachesInheritedGroupChild(t *testing.T) {
	dir := t.TempDir()
	target := &lifecycleTarget{prefix: pathForTempSubdir(t, "prefix")}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-timeout-child-1")

	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command": `sh -c 'exec sleep 30' >/dev/null 2>&1 &
echo $! > ` + filepath.Join(dir, "child.pid") + `
sleep 60`,
		"purpose":         "timeout reaches inherited child",
		"scope":           "shared",
		"timeout_seconds": float64(1),
	})
	require.True(t, completion.IsError, "a timed-out install must surface as an error: %s", completion.ForLLM)
	assert.Contains(t, completion.ForLLM, "timed out")

	raw, err := os.ReadFile(filepath.Join(dir, "child.pid"))
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	assert.False(t, pidAlive(pid), "the existing timeout group kill must reach an inherited child")
}

func TestEnvironmentSetup_DetachedChildLimitationIsHonest(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	h := newLifecycleHarness(t, lifecycleStore{target: &lifecycleTarget{prefix: prefix}})
	_, completion := h.startAndWait(t, h.ctx("mia", "alpha", "", "t-detached-notice-1"), map[string]any{
		"command": `echo plain > "$OMNIPUS_ENV_PREFIX/artifact.txt"`,
		"purpose": "Bash lifecycle limitation notice",
		"scope":   "shared",
	})
	require.False(t, completion.IsError, "install failed: %s", completion.ForLLM)
	assert.Contains(t, completion.ForLLM, "deliberately detached children can outlive the installer")
}
