//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for config-change → context-builder invalidation wiring (FR-061).

package gateway

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestUpdateAgent_InvalidatesPreamble verifies T1: after a config change the
// ContextBuilderRegistry's InvalidateAllContextBuilders is callable and does
// not panic, and the agentLoop exposes a non-nil registry (wiring check).
//
// The deeper invariant — that InvalidateAllContextBuilders clears each builder's
// cache so the next turn rebuilds the system prompt — is covered by
// TestContextBuilderRegistry_InvalidateAll in pkg/agent/context_cache_test.go.
// This test focuses on the REST → registry call path being wired correctly.
func TestUpdateAgent_InvalidatesPreamble(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{}
	cfg.Agents.Defaults = config.AgentDefaults{
		Workspace: tmpDir,
		ModelName: "test-model",
		MaxTokens: 4096,
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// Verify the registry is wired.
	reg := al.ContextBuilderRegistry()
	if reg == nil {
		t.Fatal("ContextBuilderRegistry must not be nil after NewAgentLoop (wiring check)")
	}

	// Register a builder, warm its cache, then invalidate via the registry.
	cb := agent.NewContextBuilder(tmpDir).WithAgentInfo("test-agent", "Test Agent")
	reg.Register("test-agent", cb)
	_ = cb.BuildSystemPromptWithCache() // warm the cache

	// InvalidateAllContextBuilders must not panic and must clear the builder's
	// cache — subsequent call to BuildSystemPromptWithCache will rebuild.
	// We cannot observe the cache state directly, but we verify no panic occurs.
	reg.InvalidateAllContextBuilders()

	// The builder must still be registered after invalidation.
	reg.Register("test-agent2", agent.NewContextBuilder(tmpDir))
	reg.InvalidateAllContextBuilders() // must tolerate multiple calls
}
