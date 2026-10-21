package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAskUserQuestion_RejectsRemovedAutoAnswerParameter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"true", true},
		{"false", false},
		{"null", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pending := &fakeAskRegistry{}
			tool := newAskTool(pending)
			registry := NewToolRegistry()
			registry.Register(tool)
			args := validAskArgs()
			question := args["questions"].([]any)[0].(map[string]any)
			delete(question, "default_safe")

			// Control: a recommendation remains valid and never pre-answers.
			require.NoError(t, validateToolArgs(tool.Parameters(), args))
			question["default_safe"] = tc.value
			res := registry.ExecuteWithContext(webCtx(), AskUserQuestionToolName, args, "webchat", "chat-1", nil)
			require.True(t, res.IsError, "a removed parameter must be rejected, not ignored")
			require.Equal(t, `invalid arguments for tool "AskUserQuestion": property "questions[0]": unexpected property "default_safe"`, res.ForLLM)
			require.False(t, res.ParksTurn)
			require.Empty(t, pending.created, "rejected input must not create a pending question")
			_, advertised := askQuestionItemProps(t, tool.Parameters())["default_safe"]
			require.False(t, advertised, "the removed parameter must not be advertised to the model")
		})
	}
}
