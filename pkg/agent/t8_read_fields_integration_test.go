package agent

// r17 §7.13 / scenarios 20.1–20.11: actual reader registration, stores and
// projection, not the test-owned pure-helper declarations. Missing readers
// stop conditionally; every case includes its production-result oracle.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func t8SelectableSchemaNames(t *testing.T, schema string, identity ...string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "components", "schemas", schema+".yaml"))
	require.NoError(t, err)
	var doc struct {
		Properties yaml.Node `yaml:"properties"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.Equal(t, yaml.MappingNode, doc.Properties.Kind, "instrument: a real schema property map was found")
	var out []string
	for i := 0; i < len(doc.Properties.Content); i += 2 {
		name := doc.Properties.Content[i].Value
		require.NotContains(t, []string{"absent_fields", "truncated_fields"}, name, "Task/Plan/Goal must not gain result metadata")
		skip := false
		for _, id := range identity {
			if name == id {
				skip = true
			}
		}
		if !skip {
			out = append(out, name)
		}
	}
	require.NotEmpty(t, out)
	return out
}

type t8ReaderCase struct {
	name, object, schema, idKey string
	args                        map[string]any
	selected                    map[string]any
}

func t8FieldReaderCases(t *testing.T, f *t8ReadFixture) []t8ReaderCase {
	t.Helper()
	tk := f.task(t, "field-task", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
	p := f.plan(t, "field-plan", "reader-a", "reader-a", "agent", testHarnessWorkspaceMembershipID)
	g := f.taskGoal(t, tk)
	return []t8ReaderCase{
		{"get_task", "task", "Task", "id", map[string]any{"task_id": tk.ID}, map[string]any{"id": tk.ID, "title": "T8 visible task", "prompt": "the stored instruction"}},
		{"get_plan", "plan", "Plan", "id", map[string]any{"plan_id": p.ID}, map[string]any{"id": p.ID, "title": "T8 visible plan", "objective": "objective"}},
		{"get_goal", "goal", "Goal", "goal_id", map[string]any{"task_id": tk.ID}, map[string]any{"goal_id": g.GoalID, "prompt": "goal prompt", "state": "defining"}},
	}
}
func t8FieldsCall(t *testing.T, f *t8ReadFixture, tc t8ReaderCase, fields any, present bool) *tools.ToolResult {
	t.Helper()
	args := map[string]any{}
	for k, v := range tc.args {
		args[k] = v
	}
	if present {
		args["fields"] = fields
	}
	return f.call(t, "reader-a", testHarnessWorkspaceMembershipID, tc.name, args)
}
func TestT8ReadFields_ExactValidationThroughRegistry(t *testing.T) {
	f := t8ReadLoop(t)
	for _, tc := range t8FieldReaderCases(t, f) {
		t.Run(tc.name, func(t *testing.T) {
			valid := t8SelectableSchemaNames(t, tc.schema, tc.idKey)
			list := strings.Join(valid, ", ")
			required := fmt.Sprintf(`fields is required for %s: pass ["all"] or one or more of: %s`, tc.object, list)
			cases := []struct {
				name    string
				value   any
				present bool
				want    string
			}{
				{"20.1 missing", nil, false, required}, {"20.2 empty", []any{}, true, required}, {"20.2 string", "all", true, required},
				{"20.2 number", 42, true, required}, {"20.2 mixed", []any{valid[0], 42}, true, required},
				{"20.3 unknown", []any{valid[0], "nope"}, true, fmt.Sprintf(`unknown field "nope" for %s. Valid: all, %s`, tc.object, list)},
				{"20.4 all combined", []any{"all", valid[0]}, true, `"all" cannot be combined with other field names`},
			}
			for _, name := range []string{tc.idKey, "absent_fields", "truncated_fields"} {
				cases = append(cases, struct {
					name    string
					value   any
					present bool
					want    string
				}{"identity or metadata " + name, []any{name}, true, fmt.Sprintf(`unknown field %q for %s. Valid: all, %s`, name, tc.object, list)})
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					r := t8FieldsCall(t, f, tc, c.value, c.present)
					require.True(t, r.IsError)
					assert.Equal(t, c.want, r.ForLLM)
					assert.NotContains(t, r.ForLLM, "invalid arguments for tool", "20.10 field validation precedes generic schema refusal")
				})
			}
		})
	}
}
func TestT8ReadFields_SchemaAndExactProjection(t *testing.T) {
	f := t8ReadLoop(t)
	for _, tc := range t8FieldReaderCases(t, f) {
		t.Run(tc.name, func(t *testing.T) {
			valid := t8SelectableSchemaNames(t, tc.schema, tc.idKey)
			inst, ok := f.al.GetRegistry().GetAgent("reader-a")
			require.True(t, ok)
			tool, ok := inst.Tools.Get(tc.name)
			if !ok {
				t.Fatalf("BLOCKED: %s not implemented/registered — required by r17 20.9", tc.name)
			}
			raw, err := json.Marshal(tool.Parameters())
			require.NoError(t, err)
			var schema map[string]any
			require.NoError(t, json.Unmarshal(raw, &schema))
			props, ok := schema["properties"].(map[string]any)
			require.True(t, ok)
			field, ok := props["fields"].(map[string]any)
			require.True(t, ok)
			assert.Contains(t, schema["required"], "fields")
			assert.Equal(t, float64(1), field["minItems"])
			assert.Equal(t, true, field["uniqueItems"])
			assert.NotContains(t, field, "default")
			items, ok := field["items"].(map[string]any)
			require.True(t, ok)
			wantEnum := []any{"all"}
			for _, v := range valid {
				wantEnum = append(wantEnum, v)
			}
			assert.Equal(t, wantEnum, items["enum"])
			var selected []any
			for _, name := range valid {
				if _, ok := tc.selected[name]; ok {
					selected = append(selected, name)
				}
			}
			got := t8ReadObject(t, t8FieldsCall(t, f, tc, selected, true))
			assert.Equal(t, tc.selected, got, "20.6 exactly identity plus the two requested fixture values")
			all := t8ReadObject(t, t8FieldsCall(t, f, tc, []any{"all"}, true))
			for key, value := range tc.selected {
				assert.Equal(t, value, all[key], "20.5 all includes every carried fixture value")
			}
			for key := range all {
				assert.Contains(t, append([]string{tc.idKey, "absent_fields", "truncated_fields"}, valid...), key, "no extra property leaks from the full object")
			}
		})
	}
}
func t8EncodedCharacters(t *testing.T, v any) int {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return utf8.RuneCount(raw)
}
func TestT8ReadFields_RealStoredLongText(t *testing.T) {
	t.Run("20.8 task longest cut only and stored data untouched", func(t *testing.T) {
		f := t8ReadLoop(t)
		tk := f.task(t, "long-task", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
		result, prompt, description := strings.Repeat("R", 50000), strings.Repeat("P", 10000), strings.Repeat("D", 2000)
		_, err := f.ts.Update(tk.ID, task.Patch{Result: &result, Prompt: &prompt, Description: &description})
		require.NoError(t, err)
		out := t8ReadObject(t, f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_task", map[string]any{"task_id": tk.ID, "fields": []any{"result", "prompt", "description"}}))
		assert.Equal(t, map[string]any{"id": tk.ID, "result": result[:8000] + "…[cut: 8,000 of 50,000 characters]", "prompt": prompt, "description": description, "truncated_fields": []any{"result"}}, out)
		assert.LessOrEqual(t, t8EncodedCharacters(t, out), 48000)
		all := t8ReadObject(t, f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_task", map[string]any{"task_id": tk.ID, "fields": []any{"all"}}))
		assert.Equal(t, tk.ID, all["id"])
		assert.Equal(t, result[:8000]+"…[cut: 8,000 of 50,000 characters]", all["result"], "20.8 the all-fields call obeys the same exact cut")
		assert.Equal(t, prompt, all["prompt"], "20.8 stops after the longest field makes the response fit")
		assert.Equal(t, description, all["description"])
		assert.Equal(t, []any{"result"}, all["truncated_fields"])
		assert.LessOrEqual(t, t8EncodedCharacters(t, all), 48000)
		stored, err := f.ts.Get(tk.ID)
		require.NoError(t, err)
		assert.Equal(t, result, stored.Result)
		assert.Equal(t, prompt, stored.Prompt)
	})
	t.Run("20.8 goal reason cut", func(t *testing.T) {
		f := t8ReadLoop(t)
		tk := f.task(t, "long-goal", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
		g := f.taskGoal(t, tk)
		text := strings.Repeat("L", 50000)
		_, err := f.gs.Update(g.GoalID, func(v *goal.Goal) error { v.LatestReason = text; return nil })
		require.NoError(t, err)
		out := t8ReadObject(t, f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_goal", map[string]any{"task_id": tk.ID, "fields": []any{"latest_reason"}}))
		assert.Equal(t, map[string]any{"goal_id": g.GoalID, "latest_reason": text[:8000] + "…[cut: 8,000 of 50,000 characters]", "truncated_fields": []any{"latest_reason"}}, out)
		all := t8ReadObject(t, f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_goal", map[string]any{"task_id": tk.ID, "fields": []any{"all"}}))
		assert.Equal(t, g.GoalID, all["goal_id"])
		assert.Equal(t, text[:8000]+"…[cut: 8,000 of 50,000 characters]", all["latest_reason"])
		assert.Equal(t, []any{"latest_reason"}, all["truncated_fields"])
		assert.LessOrEqual(t, t8EncodedCharacters(t, all), 48000)
		stored, err := f.gs.Get(g.GoalID)
		require.NoError(t, err)
		assert.Equal(t, text, stored.LatestReason, "all-fields projection never edits the original")
	})
}
func TestT8ReadFields_RealOversizeArraysAndSmallerRetry(t *testing.T) {
	t.Run("20.8b plan DoD never cut", func(t *testing.T) {
		f := t8ReadLoop(t)
		p := f.plan(t, "array-plan", "reader-a", "reader-a", "agent", testHarnessWorkspaceMembershipID)
		var dod []task.AcceptanceCriterion
		for i := range 30 {
			dod = append(dod, task.AcceptanceCriterion{Kind: task.KindProse, Text: fmt.Sprintf("%02d ", i) + strings.Repeat("D", 1990), Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "reader-a"}})
		}
		p, err := f.ps.Update(p.ID, plan.Patch{DoD: &dod})
		require.NoError(t, err)
		require.Greater(t, t8EncodedCharacters(t, p.DoD), 48000)
		r := f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_plan", map[string]any{"plan_id": p.ID, "fields": []any{"dod"}})
		require.True(t, r.IsError)
		assert.Regexp(t, `^result too large for plan: \d+ characters after cutting long text \(limit 48,000\); largest field: dod \(\d+ characters\)\. Request fewer fields\.$`, r.ForLLM)
		all := f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_plan", map[string]any{"plan_id": p.ID, "fields": []any{"all"}})
		require.True(t, all.IsError, "20.8b all fields must refuse the same uncuttable array")
		assert.Regexp(t, `^result too large for plan: \d+ characters after cutting long text \(limit 48,000\); largest field: dod \(\d+ characters\)\. Request fewer fields\.$`, all.ForLLM)
		out := t8ReadObject(t, f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_plan", map[string]any{"plan_id": p.ID, "fields": []any{"state"}}))
		assert.Equal(t, map[string]any{"id": p.ID, "state": "draft"}, out)
		after, err := f.ps.Get(p.ID)
		require.NoError(t, err)
		assert.Equal(t, p.DoD, after.DoD)
	})
	t.Run("20.8b goal history built through real reactivation", func(t *testing.T) {
		f := t8ReadLoop(t)
		tk := f.task(t, "array-goal", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
		g := f.taskGoal(t, tk)
		now := time.Now().UTC()
		_, err := f.gs.Update(g.GoalID, func(v *goal.Goal) error {
			if err := v.Activate("history-session-0", now); err != nil {
				return err
			}
			for i := range 12 {
				if err := v.Terminate(gen.GoalStateCleared, fmt.Sprintf("%02d", i)+strings.Repeat("H", 5000), now.Add(time.Duration(i)*time.Second)); err != nil {
					return err
				}
				if err := v.Reactivate(fmt.Sprintf("history-session-%d", i+1), now.Add(time.Duration(i+1)*time.Second)); err != nil {
					return err
				}
			}
			return nil
		})
		require.NoError(t, err)
		stored, err := f.gs.Get(g.GoalID)
		require.NoError(t, err)
		require.Len(t, stored.TerminalHistory, 12)
		require.Greater(t, t8EncodedCharacters(t, stored.TerminalHistory), 48000)
		r := f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_goal", map[string]any{"task_id": tk.ID, "fields": []any{"terminal_history"}})
		require.True(t, r.IsError)
		assert.Regexp(t, `^result too large for goal: \d+ characters after cutting long text \(limit 48,000\); largest field: terminal_history \(\d+ characters\)\. Request fewer fields\.$`, r.ForLLM)
		all := f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_goal", map[string]any{"task_id": tk.ID, "fields": []any{"all"}})
		require.True(t, all.IsError, "20.8b all-fields call returns no partial object either")
		assert.Regexp(t, `^result too large for goal: \d+ characters after cutting long text \(limit 48,000\); largest field: terminal_history \(\d+ characters\)\. Request fewer fields\.$`, all.ForLLM)
		out := t8ReadObject(t, f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "get_goal", map[string]any{"task_id": tk.ID, "fields": []any{"state"}}))
		assert.Equal(t, map[string]any{"goal_id": g.GoalID, "state": "active"}, out)
		after, err := f.gs.Get(g.GoalID)
		require.NoError(t, err)
		assert.Equal(t, stored, after, "projection errors never mutate persisted history")
	})
}
