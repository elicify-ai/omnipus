package tools

// Oracle: tool-parity r17 D-1 and D-11; expectations are specified bounds,
// not values sampled from the existing tool. Each case uses a fresh store.
import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskFields_D1_Boundaries(t *testing.T) {
	type row struct {
		name, field string
		value       any
		accept      bool
	}
	var rows []row
	for _, bound := range []struct {
		field string
		max   int
	}{{"title", 200}, {"description", 2000}, {"prompt", 10000}} {
		lengths := []int{bound.max - 1, bound.max, bound.max + 1}
		if bound.field == "title" {
			lengths = append([]int{0, 1, 2}, lengths...)
		}
		for _, n := range lengths {
			rows = append(rows, row{fmt.Sprintf("%s length %d", bound.field, n), bound.field, strings.Repeat("x", n), n <= bound.max && (bound.field != "title" || n > 0)})
		}
	}
	rows = append(rows, row{"title whitespace", "title", " ", false}, row{"due malformed", "due", "not-a-date", false})
	for _, n := range []int{15, 16, 17} {
		tags := make([]any, n)
		for i := range tags {
			tags[i] = fmt.Sprintf("tag-%02d", i)
		}
		rows = append(rows, row{fmt.Sprintf("tags count %d", n), "tags", tags, n <= 16})
	}
	for _, n := range []int{63, 64, 65} {
		rows = append(rows, row{fmt.Sprintf("tag length %d", n), "tags", []any{strings.Repeat("x", n)}, n <= 64})
	}
	for _, n := range []int{-1, 0, 1, 4, 5, 6} {
		rows = append(rows, row{fmt.Sprintf("priority %d", n), "priority", n, false})
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			if tc.field == "priority" {
				t.Fatal("BLOCKED: r17 D-1 lists priority boundary inputs but does not define the accepted range — no priority-range oracle is inferred from implementation or the draft fixture")
			}
			store, _ := parityStorePair(t)
			tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
			before, err := json.Marshal(tk)
			require.NoError(t, err)
			res := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, tc.field: tc.value})
			got, err := store.Get(tk.ID)
			require.NoError(t, err)
			if !tc.accept {
				require.True(t, res.IsError, "D-1 must refuse %s: %s", tc.name, res.ForLLM)
				assert.Contains(t, res.ForLLM, tc.field)
				after, err := json.Marshal(got)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after), "invalid field leaves full task unchanged")
				return
			}
			require.False(t, res.IsError, "D-1 accepts %s: %s", tc.name, res.ForLLM)
			switch tc.field {
			case "title":
				assert.Equal(t, tc.value, got.Title)
			case "description":
				assert.Equal(t, tc.value, got.Description)
			case "prompt":
				assert.Equal(t, tc.value, got.Prompt)
			case "priority":
				assert.Equal(t, tc.value, got.Priority)
			case "tags":
				want := []string{}
				for _, v := range tc.value.([]any) {
					want = append(want, v.(string))
				}
				assert.Equal(t, want, got.Tags)
			}
		})
	}
}
func TestTaskStatus_D11_TransitionMatrix(t *testing.T) {
	for _, tc := range []struct {
		from   task.Status
		to     string
		accept bool
	}{
		{task.StatusInbox, "next", true}, {task.StatusNext, "inbox", true}, {task.StatusFailed, "next", true}, {task.StatusFailed, "inbox", true},
		{task.StatusDone, "inbox", false}, {task.StatusDone, "next", false}, {task.StatusInbox, "in_progress", false},
		{task.StatusInbox, "blocked", false}, {task.StatusNext, "blocked", false}, {task.StatusFailed, "blocked", false}, {task.StatusDone, "blocked", false}, {task.StatusInProgress, "blocked", false},
	} {
		t.Run(string(tc.from)+" to "+tc.to, func(t *testing.T) {
			store, _ := parityStorePair(t)
			tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", tc.from, false)
			r := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "status": tc.to})
			assert.Equal(t, !tc.accept, r.IsError, "D-11 result: %s", r.ForLLM)
			got, err := store.Get(tk.ID)
			require.NoError(t, err)
			want := tc.from
			if tc.accept {
				want = task.Status(tc.to)
			}
			assert.Equal(t, want, got.Status)
			if tc.from == task.StatusDone && tc.to != "blocked" {
				assert.Contains(t, r.ForLLM, `"done" → "`+tc.to+`" is not permitted (done is terminal)`)
			}
		})
	}
}
