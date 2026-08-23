// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// get_capability_catalog_test.go — D18: GET /providers/model-capabilities
// needs a gateway-facing exported accessor for the AgentLoop's capability
// catalog (getCapabilityCatalog was unexported; SetCapabilityCatalog was
// already exported — this closes that asymmetry). These tests prove
// GetCapabilityCatalog delegates to the same RLock-guarded read as the
// internal getCapabilityCatalog: the embedded-seed catalog NewAgentLoop
// wires by default (loop.go ~:645), and the exact instance passed to
// SetCapabilityCatalog afterward.

package agent

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/capabilities"
)

// newMinimalAgentLoopForCapabilityTest builds a bare AgentLoop (via the
// shared mustNewAgentLoop test helper) for tests that only care about the
// capability-catalog accessor, avoiding the dogsled lint finding that
// newTestAgentLoop's 5-value return (al, cfg, msgBus, provider, cleanup)
// would trigger when cfg/msgBus/provider go unused here.
func newMinimalAgentLoopForCapabilityTest(t *testing.T) *AgentLoop {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	return al
}

func TestGetCapabilityCatalog_DefaultsToEmbeddedSeedCatalog(t *testing.T) {
	al := newMinimalAgentLoopForCapabilityTest(t)

	// NewAgentLoop wires the embedded-seed catalog by default (loop.go
	// ~:645) — the exported accessor must surface it, not nil, so a fresh
	// gateway boot (no SetCapabilityCatalog repo-pull override yet) still
	// serves real per-model modalities from GET /providers/model-capabilities.
	got := al.GetCapabilityCatalog()
	require.NotNil(
		t,
		got,
		"NewAgentLoop wires the embedded seed by default — exported accessor must surface it",
	)
	assert.NotEmpty(t, got.Models(), "embedded seed catalog must contain models")
}

func TestGetCapabilityCatalog_ReturnsSetCatalog(t *testing.T) {
	al := newMinimalAgentLoopForCapabilityTest(t)

	seed := []byte(`{
		"version": "test-1",
		"schema_version": "1",
		"updated_at": "2026-01-01T00:00:00Z",
		"source": "test",
		"default_resize_budget": {"long_edge_px": 4096, "max_bytes": 10485760},
		"models": [{"id": "vision-model", "provider": "test", "input_modalities": ["text", "image"]}]
	}`)
	catalog, err := capabilities.NewCatalog(seed, nil, nil, nil)
	require.NoError(t, err)

	al.SetCapabilityCatalog(catalog)

	got := al.GetCapabilityCatalog()
	require.NotNil(t, got)
	assert.Same(
		t,
		catalog,
		got,
		"exported accessor must return the exact instance SetCapabilityCatalog stored, via the same lock-guarded path as the internal getter",
	)
}
