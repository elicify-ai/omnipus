package systools

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Same rule as gateway.delegationDepthCeiling: an invalid
// performance.max_delegation_depth fails closed (0), not the default.
func TestWorkspaceDelegationDepthCeiling_InvalidFailsClosed(t *testing.T) {
	deps := func(p config.PerformanceConfig) *Deps {
		return &Deps{GetCfg: func() *config.Config { return &config.Config{Performance: p} }}
	}
	if got := workspaceDelegationDepthCeiling(deps(config.PerformanceConfig{MaxDelegationDepth: -1})); got != 0 {
		t.Errorf("invalid limit: ceiling = %d, want 0 (fail closed)", got)
	}
	if got := workspaceDelegationDepthCeiling(deps(config.PerformanceConfig{})); got != workspaceDelegationDepthCeilingFallback {
		t.Errorf("unset: ceiling = %d, want %d", got, workspaceDelegationDepthCeilingFallback)
	}
	if got := workspaceDelegationDepthCeiling(deps(config.PerformanceConfig{MaxDelegationDepth: 5})); got != 5 {
		t.Errorf("explicit: ceiling = %d, want 5", got)
	}
}
