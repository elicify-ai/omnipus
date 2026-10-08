package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// An invalid performance.max_delegation_depth must not be swallowed into the
// default when validating saved edges: it logs ERROR naming the key and yields
// a ceiling no positive edge depth satisfies (same rule as the agent loop).
func TestDelegationDepthCeiling_InvalidFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.log")
	require.NoError(t, logger.EnableFileLogging(path))
	t.Cleanup(logger.DisableFileLogging)

	invalid := &config.Config{Performance: config.PerformanceConfig{MaxDelegationDepth: -1}}
	ceiling := delegationDepthCeiling(invalid)
	assert.Equal(t, 0, ceiling, "invalid limit must fail closed, not fall back to the default")

	logged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(logged), `"config_key":"performance.max_delegation_depth"`)
	assert.True(t, strings.Contains(string(logged), `"level":"error"`))

	// A saved edge with a positive depth is rejected against that ceiling.
	d := 1
	_, msg := buildWorkspaceDelegationEdges(
		[]gen.WorkspaceDelegationEdge{{FromAgent: "jim", ToAgent: "ava", Depth: &d}},
		map[string]bool{"jim": true, "ava": true}, ceiling)
	assert.Contains(t, msg, "exceeds the maximum allowed depth")

	// Controls: unset keeps the default 3, an explicit value is honoured.
	assert.Equal(t, 3, delegationDepthCeiling(&config.Config{}))
	assert.Equal(t, 5, delegationDepthCeiling(&config.Config{Performance: config.PerformanceConfig{MaxDelegationDepth: 5}}))
}
