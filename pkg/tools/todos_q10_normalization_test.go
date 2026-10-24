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
// SPEC CHANGE (founder Q74, 2026-10-24): the fixture's ten empty-after-trim
// cases are superseded — "Q10 empty cases superseded by founder Q74
// 2026-10-24": they are rejected with the required-active_form message
// (error: "required") instead of accepted as omitted. Trim normalization for
// non-empty values and the 38-case count are unchanged; the three 501-length
// cases keep their exact length-error pin (error field absent = length).
type q10FormCase struct {
	Name     string `json:"name"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Reject   bool   `json:"reject"`
	Error    string `json:"error"`
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
							if tc.Error == "required" {
								// Q74: empty-after-trim values are rejected as
								// required, with the corrective message elements.
								if err == nil {
									t.Fatalf("empty-after-trim active_form must be rejected, parse accepted %q", tc.Input)
								}
								assertActiveFormRequiredErrorText(t, err.Error(), 0)
								require.Nil(t, got, "rejected parse must not return a partial todo list")
								return
							}
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
						if tc.Error == "required" {
							// Q74: empty-after-trim values are rejected as
							// required, with the corrective message elements.
							assertActiveFormRequiredRejection(t, result, 0)
							require.Empty(t, cards, "rejected tool input must not create a task")
							return
						}
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
