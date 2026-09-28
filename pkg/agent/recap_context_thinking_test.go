// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
)

// WP-C test for the recap-context reader (spec section 8.1 row for
// pkg/agent/session_end.go: "Exclude from recap context assembly - recap
// context is answer content"). The pinned production symbol is
// pkg/agent/session_end.go::filterRecapUserTurns (FR-028 filter feeding
// recap context).
//
// Guard: expected GREEN in RED - the filter is role-keyed on "user" with a
// non-empty-content requirement, and a thinking entry carries no role and
// empty content (C3). The test pins the outcome the section 8.1 explicit
// filter must preserve, including the planted-role variant.

func TestFilterRecapUserTurns_ThinkingNeverBecomesRecapContext(t *testing.T) {
	now := time.Now().UTC()
	userTurns, toolCallCount := filterRecapUserTurns([]session.TranscriptEntry{
		{Role: "user", Content: "the real instruction", Timestamp: now},
		{
			Type:         session.EntryTypeThinking,
			Role:         "", // real shape: no role
			Content:      "",
			ThinkingText: "recap probe " + thinkingSentinel,
			Timestamp:    now,
		},
		{
			Type:         session.EntryTypeThinking,
			Role:         "user", // PLANTED - the explicit-filter case
			Content:      "",     // display copy stays in thinking_text (C3)
			ThinkingText: "recap probe planted " + thinkingSentinel,
			Timestamp:    now,
		},
		{Role: "assistant", Content: "the answer", Timestamp: now},
	})

	assert.Equal(t, []string{"the real instruction"}, userTurns,
		"recap context must carry exactly the real user instruction - thinking text never enters recap context (section 8.1)")
	assert.Zero(t, toolCallCount, "no tool calls were planted; the count must stay zero")
}
