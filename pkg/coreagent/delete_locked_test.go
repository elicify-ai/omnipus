// Contract test: Plan 3 §1 acceptance decision — core agent deletion must be refused.
//
// BDD: Given a core agent (Mia, Jim, Ava, Ray, Max), When delete_agent is called,
//
//	Then the request is rejected with a locked-field error and the agent remains in config.
//
// Acceptance decision: Plan 3 §1 "Core agent deletion attempt: refuse, audit-logged"
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/coreagent/delete_locked_test.go

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// findAgent returns a pointer to the agent with the given ID in cfg.Agents.List, or nil.
func findAgent(cfg *config.Config, id string) *config.AgentConfig {
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			return &cfg.Agents.List[i]
		}
	}
	return nil
}

// TestDeleteLockedCoreAgentRejected verifies that all 4 base core agents carry Locked=true
// after SeedConfig, meaning any handler that checks Locked before deletion will block
// the request.
//
// The delete_agent handler itself lives in the gateway (rest.go), but the
// locked-identity contract is established here at the data layer. This test ensures
// that the invariant SeedConfig guarantees — and that a re-seed after tampering
// restores it — holds for every base core agent.
//
// Traces to: Spec-3 (v0.1.0 roster re-cast) — 4-base roster; Max retired.
func TestDeleteLockedCoreAgentRejected(t *testing.T) {
	// BDD: Given a fresh config with all base core agents seeded.
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)

	// All 4 base core agent IDs (Spec-3): Max retired from the seeded base.
	coreIDs := []string{"jim", "ava", "mia", "ray"}

	for _, id := range coreIDs {
		t.Run(id, func(t *testing.T) {
			found := findAgent(cfg, id)
			require.NotNilf(t, found, "core agent %q must be in cfg.Agents.List after SeedConfig", id)

			// BDD: When delete_agent reads the Locked field to decide whether to proceed.
			// BDD: Then it must find Locked=true and refuse.
			assert.Truef(t, found.Locked,
				"core agent %q must have Locked=true — delete handler checks this field before proceeding",
				id)

			// Tamper protection: forcibly unlock, re-seed, verify re-locked.
			found.Locked = false
			require.Falsef(t, found.Locked, "test setup: tamper succeeded (pre-condition)")

			coreagent.SeedConfig(cfg)

			refound := findAgent(cfg, id)
			require.NotNilf(t, refound, "core agent %q must remain in list after re-seed", id)
			assert.Truef(t, refound.Locked,
				"SeedConfig must restore Locked=true on %q after tamper — delete_agent will be refused", id)
		})
	}

	// Differentiation: a custom agent is NOT locked.
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:     "penny-custom",
		Name:   "Penny",
		Type:   config.AgentTypeCustom,
		Locked: false,
	})
	penny := findAgent(cfg, "penny-custom")
	require.NotNil(t, penny)
	assert.False(t, penny.Locked,
		"custom agents must have Locked=false — they are deletable (contrast with core agents)")
}
