package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoalCompileRejectsRemovedAutoAnswerParameter(t *testing.T) {
	const question = `{"header":"Scope","question":"Which scope?","options":[{"label":"Backend"},{"label":"Full stack"}],"recommended":"Backend"`
	control := `{"assessment":{"clarity":"ambiguous"},"clarifying_questions":[` + question + `}]}`
	parsed, err := parseGoalCompileResponse(control)
	require.NoError(t, err, "recommended-only questions remain valid")
	require.Len(t, parsed.ClarifyingQuestions, 1)
	require.Equal(t, "Backend", parsed.ClarifyingQuestions[0].Recommended)
	for _, value := range []string{"true", "false", "null"} {
		t.Run(value, func(t *testing.T) {
			raw := `{"assessment":{"clarity":"ambiguous"},"clarifying_questions":[` + question + `,"default_safe":` + value + `}]}`
			parsed, err := parseGoalCompileResponse(raw)
			require.ErrorIs(t, err, errGoalCompileSchema)
			require.EqualError(t, err, `goal compile response violates the compile schema: clarifying_questions invalid: json: unknown field "default_safe"`)
			require.Nil(t, parsed, "a stale model field must not be silently discarded")
		})
	}
}
