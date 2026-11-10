package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// requireConversationBeforeVolatileTail applies FR-007/009 to legacy tests
// whose real subject is the conversation immediately before the new note.
// It does not filter arbitrary user messages: there must be exactly one final
// user text block containing the clock, which never appears in front.
// Squad-lead Q1=A clarifies that the original caption/media user message is
// last in this returned conversation, immediately before the volatile tail.
func requireConversationBeforeVolatileTail(t *testing.T, messages []providers.Message) []providers.Message {
	t.Helper()
	require.GreaterOrEqual(t, len(messages), 2, "FR-007: conversation plus a separate volatile tail must be present")
	tail := messages[len(messages)-1]
	require.Equal(t, "user", tail.Role, "FR-007: volatile tail is user-role, never system-role")
	require.Contains(t, tail.Content, "## Current Time", "FR-007: clock follows the conversation")
	require.Empty(t, tail.Media, "FR-007: this fixture's volatile tail is text-only")
	require.Empty(t, tail.ToolCalls, "FR-007: the volatile tail cannot declare a tool call")
	require.Empty(t, tail.ToolCallID, "FR-007: the volatile tail is not a tool result")
	require.Empty(t, tail.SystemParts, "FR-007: tail is plain user text, not a marked system block")
	conversation := messages[:len(messages)-1]
	for i, message := range conversation {
		require.NotContains(t, message.Content, "## Current Time", "FR-007: clock cannot precede the tail (message %d)", i)
		for _, part := range message.SystemParts {
			require.NotContains(t, part.Text, "## Current Time", "FR-007: clock cannot be in a system block")
		}
	}
	return conversation
}

// Scripted providers must key their response off the latest conversation
// message (possibly a tool result), not off FR-007's request-only note. Strip
// only the explicitly tagged final block; never guess from its text or erase
// earlier history. Real tail shape/order is asserted above and by the wire tests.
func lastConversationMessageForFixture(messages []providers.Message) providers.Message {
	n := len(messages)
	if n > 0 && messages[n-1].Volatile {
		n--
	}
	if n == 0 {
		return providers.Message{}
	}
	return messages[n-1]
}
