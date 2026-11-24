//go:build goolm && stdjson

package agent

// Instrument, not a product test. tests/helpers/sandboxkeys writes spec-named
// sandbox keys into a candidate copy of the live config and makes it live
// through the loop's own reload (AgentLoop.ReloadProviderAndConfig). This file
// holds that reload wiring for the pkg/agent harnesses and shows, on the real
// loop and with a key the product carries TODAY (sandbox.tool_policies), that:
//   - a carried key written through the helper reaches the live config AND
//     changes what the NEXT turn resolves, so a BLOCKED "config does not carry
//     the key" in the D9/D11 rows means the product's config lacks the key,
//     never that the helper cannot publish;
//   - a key the product does not carry is reported as such on the real loop and
//     leaves the live config untouched.
// The helper publishes through the reload, not through a bare config pointer
// swap (AgentLoop.MutateConfig), because every agent instance keeps its own
// policy snapshot that only a reload rebuilds (pkg/agent/instance.go::
// AgentInstance.StoreToolPolicy documents that production never hot-swaps a
// global policy). A passing run proves nothing about the visibility or
// skill-policy stores.

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/tests/helpers/sandboxkeys"

	"github.com/stretchr/testify/require"
)

// reloadPublisher makes a candidate configuration the loop's live one the way a
// person's settings change does in production: through the loop's own
// ReloadProviderAndConfig with the harness's provider, which rebuilds every
// agent instance (and so its policy snapshot) from the candidate.
func reloadPublisher(al *AgentLoop, provider providers.LLMProvider) func(*config.Config) error {
	return func(next *config.Config) error {
		return al.ReloadProviderAndConfig(context.Background(), provider, next)
	}
}

func TestSandboxKeys_RealLoopPublishesAKeyTheProductCarries(t *testing.T) {
	h := newTransitionsHarness(t, nil)

	loaded := h.turn("chat-before")
	require.Contains(t, loaded, "read_file", "fixture: the tool is in the surface before the write")
	var control string
	for _, name := range loaded {
		if name != "read_file" {
			control = name
			break
		}
	}
	require.NotEmpty(t, control, "fixture: an unaffected tool serves as the control")

	sandboxkeys.Store(t, h.al.GetConfig, reloadPublisher(h.al, h.provider),
		map[string]any{"tool_policies": map[string]string{"read_file": "deny"}}, "instrument")
	require.Equal(t, "deny", h.al.GetConfig().Sandbox.ToolPolicies["read_file"], "the live config holds the written entry")

	after := h.turn("chat-after")
	require.NotContains(t, after, "read_file", "the next turn resolves the published policy")
	require.Contains(t, after, control, "the unwritten control tool is untouched")

	published := h.al.GetConfig()
	err := sandboxkeys.Apply(h.al.GetConfig, reloadPublisher(h.al, h.provider),
		map[string]any{"no_such_sandbox_key": map[string]string{"x": "y"}})
	require.ErrorIs(t, err, sandboxkeys.ErrNotCarried)
	require.Same(t, published, h.al.GetConfig(), "a refused write publishes nothing")
}
