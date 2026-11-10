package task_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Keep the retired directive only as a negative fixture. The request tail
// contains the clock; no request text may reintroduce the reminder.
const legacyTailReminder = "When starting a new task or acting on a new request, check the \"# Skills\" list for one whose description matches, call Skill before proceeding, and do this silently -- never narrate it."

// Return errors rather than calling Fatal from an asynchronous worker. A broken
// request must fail visibly, not strand an approval/task goroutine indefinitely.
func taskConversationBeforeVolatileTail(messages []providers.Message) ([]providers.Message, error) {
	if len(messages) < 2 {
		return nil, fmt.Errorf("FR-007 fixture: conversation and volatile tail are required")
	}
	tail := messages[len(messages)-1]
	if tail.Role != "user" {
		return nil, fmt.Errorf("FR-007 fixture: final volatile tail must be user-role")
	}
	if strings.Count(tail.Content, "## Current Time") != 1 {
		return nil, fmt.Errorf("FR-007 fixture: final tail must contain exactly one clock")
	}
	if len(tail.Media) != 0 || len(tail.ToolCalls) != 0 || tail.ToolCallID != "" || len(tail.SystemParts) != 0 || tail.ReasoningContent != "" {
		return nil, fmt.Errorf("FR-007 fixture: volatile tail must be plain user text")
	}
	for _, message := range messages {
		if strings.Contains(message.Content, legacyTailReminder) {
			return nil, fmt.Errorf("FR-009 fixture: retired skill reminder must be absent from request text")
		}
		for _, part := range message.SystemParts {
			if strings.Contains(part.Text, legacyTailReminder) {
				return nil, fmt.Errorf("FR-009 fixture: retired skill reminder must be absent from request text")
			}
		}
	}
	conversation := messages[:len(messages)-1]
	for _, message := range conversation {
		if message.Volatile || strings.Contains(message.Content, "## Current Time") {
			return nil, fmt.Errorf("FR-007/009 fixture: volatile notes must occur only in the final tail")
		}
		for _, part := range message.SystemParts {
			if strings.Contains(part.Text, "## Current Time") {
				return nil, fmt.Errorf("FR-007/009 fixture: volatile notes cannot be system parts")
			}
		}
	}
	return conversation, nil
}

func TestPromptCachingLegacyTailBoundary(t *testing.T) {
	const clockError = "FR-007 fixture: final tail must contain exactly one clock"
	const reminderError = "FR-009 fixture: retired skill reminder must be absent from request text"
	prior := []providers.Message{
		{Role: "system", Content: "stable instructions", SystemParts: []providers.ContentBlock{
			{Type: "text", Text: "stable first block"},
			{Type: "text", Text: "stable second block"},
		}},
		{Role: "user", Content: "run the fixture tool"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-1", Name: "fixture_tool", Arguments: map[string]any{}}}},
		{Role: "tool", ToolCallID: "call-1", Content: "exact tool result"},
	}
	tail := providers.Message{Role: "user", Content: "## Current Time\n2026-10-16 00:00 UTC"}
	request := append(append([]providers.Message(nil), prior...), tail)
	conversation, err := taskConversationBeforeVolatileTail(request)
	require.NoError(t, err)
	require.Equal(t, prior, conversation, "only the separately validated final note is outside the conversation")
	require.Equal(t, tail, request[len(request)-1], "checking a request cannot remove or rewrite its tail")

	t.Run("missing reminder is valid", func(t *testing.T) {
		clockOnly := []providers.Message{prior[0], {Role: "user", Content: "## Current Time"}}
		got, err := taskConversationBeforeVolatileTail(clockOnly)
		require.NoError(t, err, "the retired reminder must not be required")
		require.Equal(t, clockOnly[:1], got, "a clock-only tail preserves the conversation")
	})

	for _, tc := range []struct {
		name    string
		request []providers.Message
		want    string
	}{
		{"missing", prior, "FR-007 fixture: final volatile tail must be user-role"},
		{"duplicated", append(append([]providers.Message(nil), request...), tail), "FR-007/009 fixture: volatile notes must occur only in the final tail"},
		{"inside tool group", []providers.Message{prior[0], prior[1], prior[2], tail, prior[3]}, "FR-007 fixture: final volatile tail must be user-role"},
		{"missing clock", []providers.Message{prior[0], {Role: "user", Content: "clock intentionally absent"}}, clockError},
		{"duplicate clock", []providers.Message{prior[0], {Role: "user", Content: "## Current Time\n## Current Time"}}, clockError},
		{"system notes", []providers.Message{{Role: "system", SystemParts: []providers.ContentBlock{{Text: legacyTailReminder}}}, tail}, reminderError},
		{"system clock notes", []providers.Message{{Role: "system", SystemParts: []providers.ContentBlock{{Text: "## Current Time"}}}, tail}, "FR-007/009 fixture: volatile notes cannot be system parts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := taskConversationBeforeVolatileTail(tc.request)
			require.EqualError(t, err, tc.want, "the instrument must reject this malformed request")
			require.Nil(t, got, "invalid tails cannot be silently filtered away")
		})
	}

	for index := range request {
		t.Run(fmt.Sprintf("retired reminder in message %d", index), func(t *testing.T) {
			poisoned := append([]providers.Message(nil), request...)
			poisoned[index].Content += "\n" + legacyTailReminder
			got, err := taskConversationBeforeVolatileTail(poisoned)
			require.EqualError(t, err, reminderError, "every request message must reject the retired directive")
			require.Nil(t, got)
		})
	}
	for index := range prior[0].SystemParts {
		t.Run(fmt.Sprintf("retired reminder in system block %d", index), func(t *testing.T) {
			poisoned := append([]providers.Message(nil), request...)
			poisoned[0].SystemParts = append([]providers.ContentBlock(nil), request[0].SystemParts...)
			poisoned[0].SystemParts[index].Text += "\n" + legacyTailReminder
			got, err := taskConversationBeforeVolatileTail(poisoned)
			require.EqualError(t, err, reminderError, "every structured system text block must reject the retired directive")
			require.Nil(t, got)
		})
	}
}
