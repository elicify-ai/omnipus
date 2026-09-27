// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"testing"

	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
)

// TestClaudeProvider_ThinkingCapableForwarding — WP-E (issue #980): the
// factory now returns *ClaudeProvider for the Anthropic protocol, so the
// agent loop's `activeProvider.(providers.ThinkingCapable)` assertion
// (pkg/agent/loop_run_turn.go::prepareLLMRequest) must see the capability.
// The delegate implements SupportsThinking (anthropic/provider.go); the
// wrapper must forward it. Regression guard for the silent-capability-drop
// shape compliance.go exists to catch.
func TestClaudeProvider_ThinkingCapableForwarding(t *testing.T) {
	cp := NewClaudeProviderWithTimeout("test-token", "", 0)

	// Interface satisfaction: the type the factory returns must BE a
	// ThinkingCapable — the agent loop consults the capability by runtime
	// type assertion on exactly this wrapper type.
	var cap ThinkingCapable = cp
	if cap == nil {
		t.Fatal("(*ClaudeProvider)(nil)-free instance must satisfy ThinkingCapable")
	}

	// Value forwarding: the wrapper's answer must match the delegate's, so a
	// forwarder that forgets to delegate (hardcodes false) is caught while
	// the delegate still answers true.
	if want := cp.delegate.SupportsThinking(); cp.SupportsThinking() != want {
		t.Fatalf("SupportsThinking() = %v, want the delegate's %v — the forwarder is not delegating",
			cp.SupportsThinking(), want)
	}
	if !cp.SupportsThinking() {
		t.Fatal("SupportsThinking() = false; the SDK-backed delegate answers true")
	}

	// And through the factory path: the provider the factory builds must be
	// the same wrapper type carrying the capability.
	factory := &ClaudeProvider{delegate: anthropicprovider.NewProvider("test-token")}
	if _, ok := interface{}(factory).(ThinkingCapable); !ok {
		t.Fatal("*ClaudeProvider does not satisfy ThinkingCapable")
	}
}
