package providers

import (
	"testing"

	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
)

// Compile-time compliance assertions.
//
// The agent loop selects the streaming path with a type assertion against
// StreamingProvider. A provider that silently fails that assertion does not
// error — it quietly falls back to a non-streaming call, making the entire
// response a black box: no partial text, no tool-argument progress, no way to
// tell a model that is still working from one that has hung. Delegated
// workers were killed on exactly that ambiguity.
//
// The Anthropic provider had no ChatStream method at all, so every
// Anthropic-backed call took the non-streaming path. Nothing failed loudly;
// there was simply no signal. These assertions make that a build error rather
// than a silent behavioural downgrade.
// These must name the types the FACTORY RETURNS, not the inner types they
// wrap. Asserting the inner type is how this test gave false confidence: it
// pinned *anthropicprovider.Provider, which does satisfy the interface — while
// the only thing that ever constructs it, *ClaudeProvider, did not, because it
// holds its delegate as an unexported non-embedded field so nothing is
// promoted. Every Anthropic install silently took the non-streaming path with
// this test green.
//
// Rule: if a wrapper is what the factory hands the agent loop, the wrapper is
// what gets asserted.
//
// The `var _` assertions themselves now live in compliance.go — a NON-test
// file — so they are a `go build` failure rather than a `go test` failure.
// They were in this file until the review wave pointed out that ADR-059 D1
// explicitly names test-file placement as the reason the compile signal did
// not count. What remains here is the runnable restatement, so the reasoning
// survives in test output too.

// TestStreamingProviderCompliance documents the invariant in a runnable form
// so the reason survives in test output, not just as a var block.
func TestStreamingProviderCompliance(t *testing.T) {
	cases := []struct {
		name string
		p    any
	}{
		{"claude_provider (what the factory returns)", (*ClaudeProvider)(nil)},
		{"anthropic (inner delegate)", (*anthropicprovider.Provider)(nil)},
		{"openai_compat", (*openai_compat.Provider)(nil)},
		{"http_provider", (*HTTPProvider)(nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := tc.p.(StreamingProvider); !ok {
				t.Fatalf("%s does not implement StreamingProvider — the agent loop's type "+
					"assertion will fail and silently fall back to a non-streaming call, "+
					"leaving callers with no progress signal at all", tc.name)
			}
		})
	}
}
