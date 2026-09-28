// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
)

// WP-C RED tests for the recap path's no-answer handling (spec sec 1 C4 recap
// clause; sec 2.2 row: persistResponse's reasoning substitution DELETED (D20);
// sec 8.1 session_end row). Pinned helper:
// pkg/agent/session_end.go::recapResultIsFailure.

func TestRecapResultIsFailure_ReasoningOnlyResultIsCandidateFailure(t *testing.T) {
	const reasoning = "The recap JSON was drafted here; the answer stayed empty."

	cases := []struct {
		name string
		r    *providers.LLMResponse
		want bool
	}{
		{"nil result", nil, true},
		{"empty content", &providers.LLMResponse{}, true},
		{"whitespace-only content", &providers.LLMResponse{Content: "   \n"}, true},
		{"reasoning-only result (the D20 case)", &providers.LLMResponse{Content: "", Reasoning: reasoning}, true},
		{"content that strips to empty", &providers.LLMResponse{Content: "```json\n\n```"}, true},
		{"orphan markup that strips to empty", &providers.LLMResponse{Content: "<tool_call>x</tool_call>"}, true},
		{"real recap envelope", &providers.LLMResponse{Content: "{\"recap\": \"worked\"}"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recapResultIsFailure(tc.r)
			assert.Equal(t, tc.want, got,
				"recapResultIsFailure must treat an empty-after-strip result as a candidate failure (D20/D26)")
		})
	}
}
