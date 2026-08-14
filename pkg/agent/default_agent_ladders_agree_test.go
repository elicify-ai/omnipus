// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

// TestDefaultAgentLadders_AgreeWithoutAnOverride pins the property that two
// independent resolvers answer "who is the default agent?" the SAME way.
//
// AgentRegistry.GetDefaultAgent (registry map) and
// routing.RouteResolver.resolveDefaultAgentID (cfg.Agents.List) are consulted
// by different callers and were written separately. Their last-resort rungs
// disagreed in two independent ways:
//
//   - ELIGIBILITY: routing skipped System Agents; the registry accepted any
//     non-worker, so a System Agent could be resolved as the chat default.
//   - ORDERING: routing took the first chat target in the config file's slice
//     order; the registry sorted.
//
// They were therefore only guaranteed to agree when the configured override
// was set — which is exactly why the override being unset was a release
// blocker in July 2026 (ADR-064 §7). Removing the "main" sentinel took a rung
// off each ladder but did NOT fix that; this does.
//
// The case below is deliberately adversarial: the config lists agents in an
// order that disagrees with sorted order AND contains a System Agent that
// sorts before every real one. Under the old rules the two resolvers returned
// different agents. If they ever diverge again, this fails.
func TestDefaultAgentLadders_AgreeWithoutAnOverride(t *testing.T) {
	home := t.TempDir()
	agentsCfg := []config.AgentConfig{
		// Slice order (zeta, mia) deliberately disagrees with sorted order.
		{ID: "zeta", Home: filepath.Join(home, "zeta")},
		{ID: "mia", Home: filepath.Join(home, "mia")},
		// Sorts before both, and must be skipped by BOTH resolvers.
		{ID: "aaa-system", Type: config.AgentTypeSystem, Home: filepath.Join(home, "aaa")},
		// Sorts before both, and must be skipped by BOTH resolvers.
		{ID: "aaa-worker", Type: config.AgentTypeWorker, Home: filepath.Join(home, "aaaw")},
	}
	for _, a := range agentsCfg {
		if err := os.MkdirAll(a.Home, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      home,
				ModelName: "test-model",
				MaxTokens: 256,
			},
			List: agentsCfg,
			// No DefaultAgentID: this test is about the LAST RESORT, which is
			// the rung that used to differ.
		},
	}

	registry := NewAgentRegistry(cfg, &mockProvider{})
	fromRegistry := registry.GetDefaultAgent()
	if fromRegistry == nil {
		t.Fatal("registry resolved no default agent, but three eligible agents exist")
	}

	fromRouting := routing.NewRouteResolver(cfg).DefaultAgentIDForTest()

	if fromRegistry.ID != fromRouting {
		t.Fatalf("the two default-agent resolvers disagree: registry=%q routing=%q — "+
			"they answer the same question for the same install and must never differ",
			fromRegistry.ID, fromRouting)
	}
	if fromRegistry.ID != "mia" {
		t.Fatalf("expected the lexicographically-first CHAT-TARGET agent (mia); got %q — "+
			"a System Agent or worker sorting earlier must never be chosen", fromRegistry.ID)
	}
}
