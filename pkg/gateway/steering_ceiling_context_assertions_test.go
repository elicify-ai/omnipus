package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func ceilingLiveAssertInputOrder(t *testing.T, messages []providers.Message, inputs []string) {
	t.Helper()
	var userBodies []string
	for _, message := range messages {
		if message.Role == "user" {
			userBodies = append(userBodies, message.Content)
		}
	}
	text := strings.Join(userBodies, "\n")
	previous := -1
	for _, input := range inputs {
		assert.Equal(t, 1, strings.Count(text, input), "instruction text occurs exactly once, including within merged user messages")
		index := strings.Index(text, input)
		assert.Greater(t, index, previous, "instruction batches retain their acceptance order")
		previous = index
	}
}

func ceilingLiveAssertContext(t *testing.T, messages []providers.Message, inputs []string) {
	t.Helper()
	ceilingLiveAssertInputOrder(t, messages, inputs)
	var calls, resultIDs, resultBodies []string
	var declarations, results []int
	for i, message := range messages {
		for _, call := range message.ToolCalls {
			calls = append(calls, call.ID)
			declarations = append(declarations, i)
		}
		if message.Role == "tool" {
			resultIDs = append(resultIDs, message.ToolCallID)
			resultBodies = append(resultBodies, message.Content)
			results = append(results, i)
		}
	}
	assert.Equal(t, []string{"live-note-1", "live-note-2"}, calls, "only the two actually completed calls reach the next turn")
	assert.Equal(t, []string{"live-note-1", "live-note-2"}, resultIDs, "each completed result keeps its own call identity once")
	assert.Equal(t, []string{"note-1-result", "note-2-result"}, resultBodies, "the real model receives the completed result payloads, not merely the follow-up text")
	require.Len(t, declarations, 2)
	require.Len(t, results, 2)
	assert.Less(t, declarations[0], results[0], "first result follows its declaration")
	assert.Less(t, results[0], declarations[1], "completed groups retain execution order")
	assert.Less(t, declarations[1], results[1], "second result follows its declaration")
}
