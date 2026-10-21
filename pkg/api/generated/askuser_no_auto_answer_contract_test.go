package generated

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func askContractArray(t *testing.T, value any) []any {
	t.Helper()
	items, ok := value.([]any)
	require.True(t, ok, "fixture value must be an array")
	require.NotEmpty(t, items)
	return items
}

func askContractObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "fixture value must be an object")
	return object
}

func TestContract_AskUserQuestionRejectsAutoAnswerFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema string
		mutate func(*testing.T, map[string]any)
	}{
		{"question_opt_in", "AskUserQuestionCard", func(t *testing.T, v map[string]any) {
			askContractObject(t, askContractArray(t, v["questions"])[0])["default_safe"] = true
		}},
		{"deadline", "AskUserQuestionCard", func(t *testing.T, v map[string]any) {
			v["default_safe_at"] = "2026-10-21T12:30:00Z"
		}},
		{"resolved_headers", "AskUserQuestionCard", func(t *testing.T, v map[string]any) {
			v["auto_resolved"] = []string{"Scope"}
		}},
		{"answer_origin", "AskUserQuestionCard", func(t *testing.T, v map[string]any) {
			v["status"] = "answered"
			v["answers"] = []any{map[string]any{
				"header": "Scope", "question": "Which scope?", "selected": []string{"Backend"}, "auto_default": true,
			}}
		}},
		{"submitted_origin", "AskUserAnswerFrame", func(t *testing.T, v map[string]any) {
			askContractObject(t, askContractArray(t, v["answers"])[0])["auto_default"] = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{
				"card_id": "ask-contract", "session_id": "session-contract",
			}
			if tc.schema == "AskUserQuestionCard" {
				payload["agent_id"] = "mia"
				payload["status"] = "pending"
				payload["created_at"] = "2026-10-21T12:00:00Z"
				payload["questions"] = []any{map[string]any{
					"header": "Scope", "question": "Which scope?", "recommended": "Backend",
					"options": []any{map[string]any{"label": "Backend"}, map[string]any{"label": "Full stack"}},
				}}
			} else {
				payload["type"] = "ask_user_answer"
				payload["answers"] = []any{map[string]any{"header": "Scope", "selected": []string{"Backend"}}}
			}
			mustPassBothCopies(t, tc.schema, payload) // valid control before adding only the removed field
			tc.mutate(t, payload)
			require.Error(t, validateAgainstAsyncAPISchema(t, tc.schema, payload), "canonical inline schema must reject the removed field")
			require.Error(t, validateAgainstComponentSchema(t, tc.schema, payload), "inbound component copy must reject the removed field")
		})
	}
}

func TestContract_AskUserQuestionManualAnswerNeedsNoAutoOrigin(t *testing.T) {
	card := fixtureAskUserCardPending()
	data, err := json.Marshal(card)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	// Construct the current manual-only record independently of generated
	// answer fields, so this same test observes the old schema rejecting it.
	delete(payload, "default_safe_at")
	delete(payload, "auto_resolved")
	for _, raw := range askContractArray(t, payload["questions"]) {
		delete(askContractObject(t, raw), "default_safe")
	}
	payload["status"] = "answered"
	payload["answers"] = []any{map[string]any{
		"header": "Scope", "question": "Which scope?", "selected": []string{"Backend"},
	}}
	mustPassBothCopies(t, "AskUserQuestionCard", payload)
}
