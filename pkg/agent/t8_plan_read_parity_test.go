package agent

// r17 §§7.3/7.8, US-4/US-5. These replace the declaration-only rows in
// pkg/tools with registered production tools and real task/plan/goal stores.
import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdatePlan_StateAndAuthority(t *testing.T) {
	for _, tc := range []struct {
		id, caller, kind string
		approved         bool
		args             map[string]any
		want             string
	}{
		{"5.1", "reader-a", "agent", false, map[string]any{"objective": "changed objective", "dod": []any{map[string]any{"kind": "prose", "text": "the outcome was verified"}}}, ""},
		{"5.2", "reader-a", "agent", true, map[string]any{"title": "Renamed"}, ""},
		{"5.3", "reader-a", "agent", true, map[string]any{"title": "must not save", "dod": []any{map[string]any{"kind": "prose", "text": "a changed definition"}}}, "dod cannot be changed once the plan has left draft state"},
		{"5.4 unrelated", "reader-c", "agent", false, map[string]any{"description": "must not save"}, "update_plan denied: you are not the owner of a plan with that id"},
		{"5.5 creator", "reader-b", "agent", false, map[string]any{"description": "creator description"}, ""},
		{"5.6 legacy creator", "reader-b", "", false, map[string]any{"description": "must not save"}, "update_plan denied: you are not the owner of a plan with that id"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			f := t8ReadLoop(t)
			p := f.plan(t, "parity-plan", "reader-a", "reader-b", tc.kind, testHarnessWorkspaceMembershipID)
			if tc.approved {
				state := plan.StateApproved
				_, err := f.ps.Update(p.ID, plan.Patch{State: &state})
				require.NoError(t, err)
			}
			before, err := f.ps.Get(p.ID)
			require.NoError(t, err)
			tc.args["plan_id"] = p.ID
			r := f.call(t, tc.caller, testHarnessWorkspaceMembershipID, "update_plan", tc.args)
			after, err := f.ps.Get(p.ID)
			require.NoError(t, err)
			if tc.want != "" {
				require.True(t, r.IsError)
				assert.Equal(t, tc.want, r.ForLLM)
				assert.Equal(t, before, after, "entire record unchanged on refusal")
				return
			}
			require.False(t, r.IsError, "%s", r.ForLLM)
			switch tc.id {
			case "5.1":
				assert.Equal(t, "changed objective", after.Objective)
				require.Len(t, after.DoD, 1)
				assert.Equal(t, "the outcome was verified", after.DoD[0].Text)
			case "5.2":
				assert.Equal(t, "Renamed", after.Title)
				assert.Equal(t, plan.StateApproved, after.State)
			case "5.5 creator":
				assert.Equal(t, "creator description", after.Description)
			}
		})
	}
	t.Run("5.4 unknown id same denial", func(t *testing.T) {
		f := t8ReadLoop(t)
		r := f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "update_plan", map[string]any{"plan_id": "unknown-plan", "title": "no record"})
		require.True(t, r.IsError)
		assert.Equal(t, "update_plan denied: you are not the owner of a plan with that id", r.ForLLM)
	})
}

func TestReads_VisibilityTable(t *testing.T) {
	const ws = testHarnessWorkspaceMembershipID
	t.Run("4.4 real paired goal", func(t *testing.T) {
		f := t8ReadLoop(t)
		tk := f.task(t, "visible-task", "reader-a", "reader-b", ws)
		g := f.taskGoal(t, tk)
		out := t8ReadObject(t, f.call(t, "reader-a", ws, "get_goal", map[string]any{"task_id": tk.ID, "fields": []any{"prompt", "state"}}))
		assert.Equal(t, map[string]any{"goal_id": g.GoalID, "prompt": "goal prompt", "state": "defining"}, out)
	})
	t.Run("4.3 session own active goal", func(t *testing.T) {
		f := t8ReadLoop(t)
		c := task.AcceptanceCriterion{Kind: task.KindProse, Text: "session outcome verified", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "reader-a"}}
		g, err := goal.New(gen.GoalOwnerKindSession, "t8-read-session", gen.GoalSourceChatCompiled, "session prompt", "session definition", []task.AcceptanceCriterion{c}, []task.AcceptanceCriterion{c}, 3, time.Now().UTC())
		require.NoError(t, err)
		require.NoError(t, g.Activate("t8-read-session", time.Now().UTC()))
		require.NoError(t, f.gs.Create(g))
		out := t8ReadObject(t, f.call(t, "reader-a", ws, "get_goal", map[string]any{"fields": []any{"definition", "criteria", "round", "state"}}))
		assert.ElementsMatch(t, []string{"goal_id", "definition", "criteria", "round", "state"}, t8MapKeys(out))
		assert.Equal(t, g.GoalID, out["goal_id"])
		assert.Equal(t, "session definition", out["definition"])
		assert.Equal(t, "active", out["state"])
		assert.Equal(t, float64(0), out["round"])
		criteria, ok := out["criteria"].([]any)
		require.True(t, ok)
		require.Len(t, criteria, 1)
		assert.Equal(t, c.Text, criteria[0].(map[string]any)["text"])
	})
	t.Run("4.5 no session goal is plain result", func(t *testing.T) {
		f := t8ReadLoop(t)
		out := t8ReadObject(t, f.call(t, "reader-a", ws, "get_goal", map[string]any{"fields": []any{"all"}}))
		assert.Equal(t, map[string]any{"goal": nil, "message": "No goal is set in this chat."}, out)
	})
	for _, reader := range []struct{ tool, idKey, object string }{{"get_task", "task_id", "task"}, {"get_goal", "task_id", "task"}, {"get_plan", "plan_id", "plan"}} {
		for _, sameWorkspace := range []bool{false, true} {
			t.Run(fmt.Sprintf("4.6 4.7 4.9 4.10 %s same-workspace-%t", reader.tool, sameWorkspace), func(t *testing.T) {
				const id = "identical-request-id"
				var results []string
				for _, exists := range []bool{true, false} {
					f := t8ReadLoop(t)
					if exists {
						targetWS := "other-workspace"
						if sameWorkspace {
							targetWS = ws
						}
						if reader.tool == "get_plan" {
							f.plan(t, id, "reader-b", "reader-b", "agent", targetWS)
						} else {
							tk := f.task(t, id, "reader-b", "reader-b", targetWS)
							f.taskGoal(t, tk)
						}
					}
					r := f.call(t, "reader-a", ws, reader.tool, map[string]any{reader.idKey: id, "fields": []any{"all"}})
					require.True(t, r.IsError)
					assert.Equal(t, fmt.Sprintf("%s %q not found", reader.object, id), r.ForLLM)
					results = append(results, r.ForLLM)
					if exists && sameWorkspace && reader.object == "task" {
						for _, role := range []string{"assignee", "delegator"} {
							list := f.call(t, "reader-a", ws, "list_tasks", map[string]any{"role": role})
							require.False(t, list.IsError, "%s", list.ForLLM)
							assert.NotContains(t, list.ForLLM, id, "4.10 both list memberships omit unrelated task")
						}
					}
				}
				assert.Equal(t, results[0], results[1], "same ID bytes, hidden and missing fixtures")
			})
		}
	}
	t.Run("4.8 no workspace still requires ownership", func(t *testing.T) {
		f := t8ReadLoop(t)
		tk := f.task(t, "elsewhere-task", "reader-b", "reader-a", "other-workspace")
		out := t8ReadObject(t, f.call(t, "reader-a", "", "get_task", map[string]any{"task_id": tk.ID, "fields": []any{"title", "prompt"}}))
		assert.Equal(t, map[string]any{"id": tk.ID, "title": tk.Title, "prompt": tk.Prompt}, out)
	})
	t.Run("4.11 person-created assignee only", func(t *testing.T) {
		f := t8ReadLoop(t)
		tk := f.task(t, "person-task", "", "reader-b", ws)
		for _, caller := range []string{"reader-a", "reader-b", "reader-c"} {
			t.Run(caller, func(t *testing.T) {
				r := f.call(t, caller, ws, "get_task", map[string]any{"task_id": tk.ID, "fields": []any{"title"}})
				if caller == "reader-b" {
					assert.Equal(t, map[string]any{"id": tk.ID, "title": tk.Title}, t8ReadObject(t, r))
				} else {
					require.True(t, r.IsError)
					assert.Equal(t, fmt.Sprintf("task %q not found", tk.ID), r.ForLLM)
				}
			})
		}
	})
	t.Run("4.12 linked member versus unrelated plan", func(t *testing.T) {
		f := t8ReadLoop(t)
		p := f.plan(t, "member-plan", "reader-b", "reader-b", "agent", ws)
		other := f.plan(t, "unrelated-plan", "reader-b", "reader-b", "agent", ws)
		tk := f.task(t, "member-task", "reader-b", "reader-a", ws)
		_, err := f.ts.Update(tk.ID, task.Patch{PlanID: &p.ID})
		require.NoError(t, err)
		for _, id := range []string{p.ID, other.ID} {
			r := f.call(t, "reader-a", ws, "get_plan", map[string]any{"plan_id": id, "fields": []any{"title", "objective"}})
			if id == p.ID {
				assert.Equal(t, map[string]any{"id": id, "title": p.Title, "objective": p.Objective}, t8ReadObject(t, r))
			} else {
				require.True(t, r.IsError)
				assert.Equal(t, fmt.Sprintf("plan %q not found", id), r.ForLLM)
			}
		}
	})
}
func t8MapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestT8Reads_AbsentDueAndExactProjection(t *testing.T) {
	f := t8ReadLoop(t)
	tk := f.task(t, "projection-task", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
	for _, tc := range []struct {
		fields []any
		want   map[string]any
	}{
		{[]any{"due"}, map[string]any{"id": tk.ID, "absent_fields": []any{"due"}}},
		{[]any{"title", "prompt"}, map[string]any{"id": tk.ID, "title": tk.Title, "prompt": tk.Prompt}},
		{[]any{"title", "prompt", "status"}, map[string]any{"id": tk.ID, "title": tk.Title, "prompt": tk.Prompt, "status": "inbox"}},
	} {
		t.Run(fmt.Sprint(tc.fields), func(t *testing.T) {
			out := t8ReadObject(t, f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_task", map[string]any{"task_id": tk.ID, "fields": tc.fields}))
			want, err := json.Marshal(tc.want)
			require.NoError(t, err)
			got, err := json.Marshal(out)
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(got))
		})
	}
}
