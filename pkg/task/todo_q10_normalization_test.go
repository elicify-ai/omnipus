package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// not-wire-format: local independent Q10=A test cases, not a gateway schema.
type q10FormCase struct {
	Name     string `json:"name"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Reject   bool   `json:"reject"`
}

func taskQ10Cases(t *testing.T) []q10FormCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "q10-active-form-normalization.json"))
	require.NoError(t, err)
	var fixture struct {
		Cases []q10FormCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Len(t, fixture.Cases, 38, "Q10 fixture must not silently lose a class or boundary")
	return fixture.Cases
}

// Oracle: founder Q10=A / security F1. The private validator must apply its
// bound after normalization, and both persistence entry points normalize copy.
// No assertion requires the validator itself to mutate its input slice.
func TestTaskTodoActiveFormQ10Normalization(t *testing.T) {
	for _, operation := range []string{"validate", "create_and_reload", "update_and_reload"} {
		t.Run(operation, func(t *testing.T) {
			for _, tc := range taskQ10Cases(t) {
				t.Run(tc.Name, func(t *testing.T) {
					todos := []Todo{{Text: "Draft the summary", Status: TodoInProgress, ActiveForm: tc.Input}}
					const lengthError = "todo[0]: active_form must be 500 characters or fewer"
					if operation == "validate" {
						err := validateTodos(todos)
						if tc.Reject {
							require.ErrorIs(t, err, ErrValidation)
							require.EqualError(t, err, lengthError)
						} else {
							require.NoError(t, err, "499/500 visible points plus removed controls, and invisible-only copy, are valid after normalization")
						}
						return
					}
					dir := t.TempDir()
					store := New(dir)
					card := &Task{Title: "Normalization proof", Prompt: "Normalization proof", Action: ActionLLM, Status: StatusInProgress, Scratchpad: true, AgentID: "mia", WorkspaceID: "ws-q10"}
					var err error
					var prior *Task
					if operation == "create_and_reload" {
						card.Todos = todos
						err = store.CreateByAgent(card, "mia")
					} else {
						card.Todos = []Todo{{Text: "Keep the previous item", Status: TodoInProgress, ActiveForm: "Keeping the previous item"}}
						require.NoError(t, store.CreateByAgent(card, "mia"), "positive control: seed a valid checklist")
						prior, err = New(dir).Get(card.ID)
						require.NoError(t, err)
						_, err = store.Update(card.ID, Patch{Todos: &todos})
					}
					if tc.Reject {
						require.ErrorIs(t, err, ErrValidation)
						require.EqualError(t, err, lengthError)
						if operation == "create_and_reload" {
							cards, listErr := New(dir).List(Filter{})
							require.NoError(t, listErr)
							require.Empty(t, cards, "rejected creation must leave no saved task")
						} else {
							after, readErr := New(dir).Get(card.ID)
							require.NoError(t, readErr)
							require.Equal(t, prior, after, "rejected replacement must preserve the whole prior task")
						}
						return
					}
					require.NoError(t, err, "normalize before validating and persisting")
					saved, err := New(dir).Get(card.ID)
					require.NoError(t, err)
					require.Equal(t, []Todo{{Text: "Draft the summary", Status: TodoInProgress, ActiveForm: tc.Expected}}, saved.Todos, "stored form must be exact normalized bytes")
					item := map[string]any{"text": "Draft the summary", "status": "in_progress"}
					if tc.Expected != "" {
						item["active_form"] = tc.Expected
					}
					want, err := json.Marshal([]any{item})
					require.NoError(t, err)
					actual, err := json.Marshal(saved.Todos)
					require.NoError(t, err)
					require.JSONEq(t, string(want), string(actual), "blank normalized active_form is omitted from saved JSON")
				})
			}
		})
	}
}
