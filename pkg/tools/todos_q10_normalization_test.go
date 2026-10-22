package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// not-wire-format: independent founder Q10=A fixture, never a gateway type.
type q10FormCase struct {
	Name     string `json:"name"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Reject   bool   `json:"reject"`
}

func toolQ10Cases(t *testing.T) []q10FormCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "q10-active-form-normalization.json"))
	require.NoError(t, err)
	var fixture struct {
		Cases []q10FormCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Len(t, fixture.Cases, 38, "Q10 fixture must include all eleven bidi controls and preservation/boundary controls")
	return fixture.Cases
}

// Oracle: founder Q10=A / security F1. Both the raw parser and real tool/store
// path must normalize copy; the tool's exact response and fresh store are real.
func TestTodosActiveFormQ10Normalization(t *testing.T) {
	for _, operation := range []string{"parse", "execute_and_reload"} {
		t.Run(operation, func(t *testing.T) {
			for _, tc := range toolQ10Cases(t) {
				t.Run(tc.Name, func(t *testing.T) {
					raw := []any{map[string]any{"text": "Draft the summary", "status": "in_progress", "active_form": tc.Input}}
					const lengthError = "todos[0]: active_form must be 500 characters or fewer"
					want := []task.Todo{{Text: "Draft the summary", Status: task.TodoInProgress, ActiveForm: tc.Expected}}
					if operation == "parse" {
						got, err := parseTodosArg(raw)
						if tc.Reject {
							require.EqualError(t, err, lengthError, "501 normalized code points must be rejected")
							require.Nil(t, got, "rejected parse must not return a partial todo list")
							return
						}
						require.NoError(t, err, "normalization precedes the length check")
						require.Equal(t, want, got, "parsed active_form must be normalized exactly, preserving legitimate joiners/RTL")
						return
					}
					dir := t.TempDir()
					ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-q10")
					result := NewSetTodosTool(task.New(dir)).Execute(ctx, map[string]any{"outcome": "Normalization proof", "todos": raw})
					cards, err := task.New(dir).List(task.Filter{AgentID: "mia"})
					require.NoError(t, err)
					if tc.Reject {
						require.True(t, result.IsError, "501 normalized code points must not be saved")
						require.Equal(t, lengthError, result.ForLLM)
						require.Empty(t, cards, "rejected tool input must not create a task")
						return
					}
					require.False(t, result.IsError, "normalizable form rejected: %s", result.ForLLM)
					require.Len(t, cards, 1, "actual tool must persist exactly one checklist")
					got, err := task.New(dir).Get(cards[0].ID)
					require.NoError(t, err)
					require.Equal(t, want, got.Todos, "fresh persisted readback must contain normalized bytes")
					data, err := json.Marshal(got.Todos)
					require.NoError(t, err)
					item := map[string]any{"text": "Draft the summary", "status": "in_progress"}
					wantResult := "Outcome: Normalization proof\n- [in_progress] Draft the summary"
					if tc.Expected != "" {
						item["active_form"] = tc.Expected
						wantResult += " (active_form: " + tc.Expected + ")"
					}
					wantJSON, err := json.Marshal([]any{item})
					require.NoError(t, err)
					require.JSONEq(t, string(wantJSON), string(data), "invisible-only optional fields must be omitted, not null or invisible strings")
					require.Equal(t, wantResult+"\n", result.ForLLM, "read-on-write must use normalized display copy")
				})
			}
		})
	}
}
