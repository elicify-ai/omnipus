// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// get_capability_catalog_test.go — the exported accessor for the AgentLoop's
// provider catalog. The gateway reads it back (setupAndStartServices) to hand
// the SAME instance to the REST surface and the 24 h refresh loop, so these
// tests pin two things: a fresh AgentLoop installs NO catalog of its own
// (ADR-067 T067-07 removed the per-loop embedded-seed parse — the gateway
// installs one booted catalog per process), and the accessor returns the
// exact instance SetCapabilityCatalog stored, through the same lock-guarded
// read as the internal getter.

package agent

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
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

func TestGetCapabilityCatalog_NoCatalogUntilInstalled(t *testing.T) {
	al := newMinimalAgentLoopForCapabilityTest(t)

	// NewAgentLoop builds NO catalog of its own. The pre-ADR-067 code parsed
	// a 12 KB seed here; the 2.0.0 snapshot is 2 MB and the gateway installs
	// its own instance moments later, so a second parse would cost boot time
	// and resident memory for a value that is always overwritten. A nil
	// catalog is the documented optimistic posture, and the gate proves it.
	require.Nil(t, al.GetCapabilityCatalog(),
		"a fresh AgentLoop installs no catalog — the gateway installs the one booted catalog")
	assert.True(t, modelSupportsImage(al.GetCapabilityCatalog(), "any-provider", "any-model"),
		"no catalog installed → the presentation gate is optimistic, never closed")
}

func TestGetCapabilityCatalog_ReturnsSetCatalog(t *testing.T) {
	al := newMinimalAgentLoopForCapabilityTest(t)

	doc := []byte(`{
		"schema_version": "2.0.0",
		"version": "v2026.1.1",
		"updated_at": "2026-01-01T00:00:00Z",
		"source": "test",
		"default_resize_limits": {"long_edge_px": 4096, "max_bytes": 10485760},
		"providers": [{
			"id": "test",
			"name": "Test",
			"api": "https://api.example.test/v1",
			"protocol": "openai-compatible",
			"tier": "standard",
			"auth_methods": ["api_key"],
			"resize_limits": {"long_edge_px": 4096, "max_bytes": 10485760},
			"models": [{
				"id": "vision-model", "name": "Vision", "context_window": 1000,
				"input_modalities": ["text", "image"], "tool_call": true, "status": "active"
			}]
		}]
	}`)
	cat, err := catalog.NewCatalog(doc)
	require.NoError(t, err)

	al.SetCapabilityCatalog(cat)

	got := al.GetCapabilityCatalog()
	require.NotNil(t, got)
	assert.Same(
		t,
		cat,
		got,
		"exported accessor must return the exact instance SetCapabilityCatalog stored, via the same lock-guarded path as the internal getter",
	)
}
