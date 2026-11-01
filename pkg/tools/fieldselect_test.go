// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// fieldselect_test.go — RED tests for the shared field-selection helper
// (tool-parity spec r17 §7.13; scenarios 20.1–20.11; spec §13.1 tests 35,
// 35b, 36; FR-180–FR-186).
//
// ORACLE: every expected value below is derived from the spec's normative
// texts (§7.13 error table, projection rules, size bound, cut rule, per-tool
// Valid lists; §7.12.1 agent names). Nothing was derived from running an
// implementation. The missing helper fails explicitly BLOCKED via test-only
// adapters; no stub return value is claimed as behavioral RED. get_agent exists
// at the pin; the other three read tools are absent.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------------
// Spec-derived FieldSpec fixtures (§7.13 per-tool table; §7.12.1 for agent).
// -----------------------------------------------------------------------------

func t8ValidAgent() []string {
	// §7.12.1: the 28 selectable names of AgentDetail (identity id+revision
	// and metadata excluded).
	return []string{
		"name", "description", "type", "locked", "figure", "role", "color", "voice",
		"model", "provider", "fallback_models", "model_chain", "model_params",
		"max_tool_iterations", "context_window_override", "memory_enabled",
		"soul", "agent_md", "skills", "tool_permissions", "stored_overrides",
		"connectors", "executor", "runnable", "program_warning",
		"activation_status", "editable_fields", "has_main_chat",
	}
}

func t8ValidPreview() []string {
	// §7.12.1: the preview accepts only these two names (plus "all").
	return []string{"tool_permissions", "connectors"}
}

func t8ValidTask() []string {
	// §7.13 get_task row: schema properties − id − result metadata, in the
	// table's order.
	return []string{
		"title", "description", "prompt", "action", "status", "agent_id",
		"cancel_reason", "agent_name", "run_isolated", "priority", "blocked_by",
		"todos", "parent_task_id", "workspace_id", "tags", "plan_id", "write_set",
		"stream", "is_join", "judge_rounds", "goal_max_rounds", "criteria", "dod",
		"attempt_count", "max_attempts", "effective_max_attempts", "trigger",
		"due", "surface", "source_channel", "source_chat_id", "session_id",
		"result", "artifacts", "owner", "created_by", "created_at", "updated_at",
		"started_at", "completed_at", "last_activity_at", "assignee_warning",
		"rollup",
	}
}

func t8ValidPlan() []string {
	// §7.13 get_plan row.
	return []string{
		"workspace_id", "title", "objective", "description", "state",
		"plan_phase", "last_unmet_terminal_signature", "owner_session_id",
		"failed_reason", "supervision", "source_channel", "source_chat_id",
		"owner_agent_id", "dod", "rationale", "bounds", "judge_rounds",
		"active_loop", "paused_reason", "last_activity_at", "progress", "owner",
		"created_by", "created_at", "updated_at", "approved_at", "started_at",
		"completed_at",
	}
}

func t8ValidGoal() []string {
	// §7.13 get_goal row.
	return []string{
		"owner_kind", "owner_id", "source", "prompt", "definition", "criteria",
		"dod", "max_rounds", "round", "attempts_used", "state", "created_at",
		"started_at", "last_activity_at", "active_session_id", "latest_reason",
		"terminal_reason", "latest_claim", "latest_verdict",
		"superseded_criteria", "terminal_history", "zero_output_pushes",
		"question_rounds_used", "route_channel", "route_chat_id",
	}
}

func t8SpecAgent() FieldSpec {
	return FieldSpec{Object: "agent", Identity: []string{"id", "revision"}, Valid: t8ValidAgent()}
}

func t8SpecPreview() FieldSpec {
	return FieldSpec{Object: "agent preview", Identity: []string{"preview_of"}, Valid: t8ValidPreview()}
}

func t8SpecTask() FieldSpec {
	return FieldSpec{Object: "task", Identity: []string{"id"}, Valid: t8ValidTask()}
}

func t8SpecPlan() FieldSpec {
	return FieldSpec{Object: "plan", Identity: []string{"id"}, Valid: t8ValidPlan()}
}

func t8SpecGoal() FieldSpec {
	return FieldSpec{Object: "goal", Identity: []string{"goal_id"}, Valid: t8ValidGoal()}
}

// t8Thousands formats the full length with the thousands separators explicitly
// required by the cut marker (§7.13).
func t8Thousands(n int) string {
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func t8CutMarker(fullLen int) string {
	return fmt.Sprintf("…[cut: 8,000 of %s characters]", t8Thousands(fullLen))
}

func t8EncodedLen(t *testing.T, v any) int {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return utf8.RuneCount(b) // §7.13 measures JSON-encoded characters, not UTF-8 bytes.
}

// t8SchemaProps reads the committed source schema's property order. Generated
// Go structs alphabetize fields, so reflection cannot provide schema order.
func t8SchemaProps(t *testing.T, v any) []string {
	t.Helper()
	rt := reflect.TypeOf(v)
	require.Equal(t, reflect.Ptr, rt.Kind(), "pass a pointer to the generated struct")
	path := filepath.Join("..", "..", "contracts", "components", "schemas", rt.Elem().Name()+".yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read the source schema, not a second hand-maintained property list")
	return t8OrderedSchemaProps(t, data)
}

func t8OrderedSchemaProps(t *testing.T, data []byte) []string {
	t.Helper()
	var document struct {
		Properties yaml.Node `yaml:"properties"`
	}
	require.NoError(t, yaml.Unmarshal(data, &document))
	require.Equal(t, yaml.MappingNode, document.Properties.Kind, "schema must declare a properties mapping")
	content := document.Properties.Content
	require.Zero(t, len(content)%2, "ordered mapping contains complete key/value pairs")
	names := make([]string, 0, len(content)/2)
	for i := 0; i < len(content); i += 2 {
		names = append(names, content[i].Value)
	}
	require.NotEmpty(t, names, "an empty schema read cannot certify an enum")
	return names
}

// Test-side instrument control, not production-enum or scenario-20.9 proof.
func TestT8SchemaPropertyInstrument_PreservesOrderAndSeesAddedProperty(t *testing.T) {
	base := []byte("type: object\nproperties:\n  id: {type: string}\n  z_last_alphabetically: {type: string}\n  a_first_alphabetically: {type: string}\n")
	want := []string{"id", "z_last_alphabetically", "a_first_alphabetically"}
	assert.Equal(t, want, t8OrderedSchemaProps(t, base), "retain source order rather than generated alphabetical order")
	mutated := append(append([]byte{}, base...), []byte("  zz_probe: {type: string}\n")...)
	got := t8OrderedSchemaProps(t, mutated)
	assert.Equal(t, append(append([]string{}, want...), "zz_probe"), got,
		"the instrument sees an added schema property; production enum must still be bound and checked independently")
}

// -----------------------------------------------------------------------------
// Test 35 — TestFieldSelect_ParseAndProject (scenarios 20.1–20.8, 20.8b, 20.11)
// -----------------------------------------------------------------------------

func TestFieldSelect_ParseAndProject(t *testing.T) {
	t8FieldSelectParseCases(t)
	t8FieldSelectProjectionCases(t)
	t8FieldSelectCutCases(t)
	t8FieldSelectOversizeCases(t)
}

func t8FieldSelectParseCases(t *testing.T) {
	t.Helper()
	t.Run("parse: fields missing, empty, wrong type, non-string items (20.1, 20.2)", func(t *testing.T) {
		for _, spec := range []FieldSpec{t8SpecAgent(), t8SpecPreview(), t8SpecTask(), t8SpecPlan(), t8SpecGoal()} {
			want := fmt.Sprintf(
				`fields is required for %s: pass ["all"] or one or more of: %s`,
				spec.Object, strings.Join(spec.Valid, ", "))
			for name, args := range map[string]map[string]any{
				"missing":            {},
				"empty array":        {"fields": []any{}},
				"a string, not list": {"fields": "all"},
				"a number":           {"fields": 42},
				"mixed non-strings":  {"fields": []any{spec.Valid[0], 42}},
			} {
				selected, errRes := t8ValidateFieldsArg(t, args, spec)
				require.Nil(t, selected, "%s/%s: no names may be selected on refusal", spec.Object, name)
				require.NotNil(t, errRes, "%s/%s: fields=%v must be refused", spec.Object, name, args["fields"])
				assert.True(t, errRes.IsError, "%s/%s: refusal must be an error result", spec.Object, name)
				assert.Equal(t, want, errRes.ForLLM,
					"%s/%s: the required-fields text is byte-for-byte (§7.13)", spec.Object, name)
			}
		}
	})

	t.Run("parse: unknown name names only the unknown name (20.3)", func(t *testing.T) {
		// Each reader with one valid name plus "nope": the text names only
		// "nope" and lists that reader's full valid set.
		cases := []struct {
			spec FieldSpec
			val  string
		}{
			{t8SpecAgent(), "name"},
			{t8SpecTask(), "title"},
			{t8SpecPlan(), "title"},
			{t8SpecGoal(), "prompt"},
		}
		for _, tc := range cases {
			want := fmt.Sprintf(`unknown field "nope" for %s. Valid: all, %s`,
				tc.spec.Object, strings.Join(tc.spec.Valid, ", "))
			selected, errRes := t8ValidateFieldsArg(t, map[string]any{"fields": []any{tc.val, "nope"}}, tc.spec)
			require.Nil(t, selected)
			require.NotNil(t, errRes)
			assert.True(t, errRes.IsError)
			assert.Equal(t, want, errRes.ForLLM, "%s: unknown-field text is byte-for-byte", tc.spec.Object)
		}
	})

	t.Run("parse: identity and metadata names are never selectable (§7.13, P-2)", func(t *testing.T) {
		cases := []struct {
			spec    FieldSpec
			answers []string
		}{
			{t8SpecAgent(), []string{"id", "revision", "absent_fields", "truncated_fields"}},
			{t8SpecTask(), []string{"id", "absent_fields", "truncated_fields"}},
			{t8SpecPlan(), []string{"id", "absent_fields", "truncated_fields"}},
			{t8SpecGoal(), []string{"goal_id", "absent_fields", "truncated_fields"}},
			{t8SpecPreview(), []string{"preview_of", "absent_fields", "truncated_fields"}},
		}
		for _, tc := range cases {
			for _, name := range tc.answers {
				want := fmt.Sprintf(`unknown field %q for %s. Valid: all, %s`,
					name, tc.spec.Object, strings.Join(tc.spec.Valid, ", "))
				_, errRes := t8ValidateFieldsArg(t, map[string]any{"fields": []any{name}}, tc.spec)
				require.NotNil(t, errRes, "%s: %q must not be selectable", tc.spec.Object, name)
				assert.Equal(t, want, errRes.ForLLM)
			}
		}
	})

	t.Run("parse: all cannot be combined (20.4)", func(t *testing.T) {
		for _, spec := range []FieldSpec{t8SpecAgent(), t8SpecPreview(), t8SpecTask(), t8SpecPlan(), t8SpecGoal()} {
			selected, errRes := t8ValidateFieldsArg(t,
				map[string]any{"fields": []any{"all", spec.Valid[0]}}, spec)
			require.Nil(t, selected)
			require.NotNil(t, errRes)
			assert.True(t, errRes.IsError)
			assert.Equal(t, `"all" cannot be combined with other field names`, errRes.ForLLM,
				"%s: the all-combination text is fixed", spec.Object)
		}
	})

	t.Run("parse: all selects every valid name; two and three names select exactly those (20.5, 20.6)", func(t *testing.T) {
		spec := t8SpecTask()
		selected, errRes := t8ValidateFieldsArg(t, map[string]any{"fields": []any{"all"}}, spec)
		require.Nil(t, errRes, "all alone must be accepted")
		assert.Equal(t, spec.Valid, selected, `["all"] selects every valid name, in schema order`)

		selected, errRes = t8ValidateFieldsArg(t, map[string]any{"fields": []any{"title", "status"}}, spec)
		require.Nil(t, errRes)
		assert.Equal(t, []string{"title", "status"}, selected)

		selected, errRes = t8ValidateFieldsArg(t, map[string]any{"fields": []any{"title", "trigger", "result"}}, spec)
		require.Nil(t, errRes)
		assert.Equal(t, []string{"title", "trigger", "result"}, selected)
	})

	t.Run("parse: preview rejects agent-valid names with the preview text (19.4b, §7.13 row 4)", func(t *testing.T) {
		want := `fields for a new-agent preview: pass ["all"] or one or more of: tool_permissions, connectors`
		// "soul" is valid for get_agent but not for a preview.
		selected, errRes := t8ValidateFieldsArg(t, map[string]any{"fields": []any{"soul"}}, t8SpecPreview())
		require.Nil(t, selected)
		require.NotNil(t, errRes)
		assert.True(t, errRes.IsError)
		assert.Equal(t, want, errRes.ForLLM)

		// A name invalid for BOTH keeps the generic unknown-field text with
		// the preview's object name and valid list (§7.13 row 2 with
		// <object> = "agent preview").
		generic := `unknown field "nope" for agent preview. Valid: all, tool_permissions, connectors`
		_, errRes = t8ValidateFieldsArg(t, map[string]any{"fields": []any{"nope"}}, t8SpecPreview())
		require.NotNil(t, errRes)
		assert.Equal(t, generic, errRes.ForLLM)
	})

}

func t8FieldSelectProjectionCases(t *testing.T) {
	t.Helper()
	t.Run("project: identity plus selected keys, nothing else (20.5, 20.6)", func(t *testing.T) {
		task := &generated.Task{
			Id:     "t-1",
			Title:  "ship it",
			Prompt: strPtr("do the work"),
			Status: generated.TaskStatus("inbox"),
		}
		out, errRes := t8Project(t, task, t8SpecTask(), []string{"title", "prompt"})
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.ElementsMatch(t, []string{"id", "title", "prompt"}, t8Keys(out),
			"exactly identity + the requested keys the object carries")
		assert.Equal(t, "t-1", out["id"])
		assert.Equal(t, "ship it", out["title"])
		assert.Equal(t, "do the work", out["prompt"])
	})

	t.Run("project: selected but uncarried value is listed, never null-filled (20.7, P-2 rule 2)", func(t *testing.T) {
		// A task without a due date: due is dropped by omitempty in the full
		// marshalled object, so it is absent — left out, named in
		// absent_fields, never null (§7.13 rule 2's own example).
		task := &generated.Task{Id: "t-2", Title: "no due", Status: generated.TaskStatus("next")}
		out, errRes := t8Project(t, task, t8SpecTask(), []string{"due"})
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.ElementsMatch(t, []string{"id", "absent_fields"}, t8Keys(out),
			"the projection carries exactly id and absent_fields")
		assert.Equal(t, []string{"due"}, t8StringSlice(t, out["absent_fields"]))
		_, has := out["due"]
		assert.False(t, has, "no due key")
		encoded, err := json.Marshal(out)
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":"t-2","absent_fields":["due"]}`, string(encoded),
			"§7.13 rule 2's exact example: no due:null and no other keys")
	})

	t.Run("project: null is kept only where the full object carries null (§7.13 rule 1)", func(t *testing.T) {
		spec := FieldSpec{Object: "synthetic", Identity: []string{"id"}, Valid: []string{"maybe_null", "filled"}}
		full := map[string]any{"id": "x", "maybe_null": nil, "filled": "value"}
		out, errRes := t8Project(t, full, spec, []string{"maybe_null", "filled"})
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.Contains(t, out, "maybe_null")
		require.Nil(t, out["maybe_null"], "a carried null stays null")
		assert.Equal(t, "value", out["filled"])
	})

}

func t8FieldSelectCutCases(t *testing.T) {
	t.Helper()
	t.Run("project: size bound cuts the longest value first, task fixture (20.8)", func(t *testing.T) {
		result := "RESULT|" + strings.Repeat("r", 50000-len("RESULT|"))
		prompt := "PROMPT|" + strings.Repeat("p", 10000-len("PROMPT|"))
		desc := "DESC|" + strings.Repeat("d", 2000-len("DESC|"))
		task := &generated.Task{
			Id:          "t-3",
			Title:       "oversize",
			Status:      generated.TaskStatus("done"),
			Result:      &result,
			Prompt:      &prompt,
			Description: &desc,
		}
		// Precondition: the untruncated full object is above the bound.
		fullLen := t8EncodedLen(t, task)
		require.Greater(t, fullLen, ResultBoundChars,
			"fixture must be over the %d-character bound before cutting", ResultBoundChars)

		out, errRes := t8Project(t, task, t8SpecTask(), t8ValidTask())
		require.Nil(t, errRes, "the task fixture must fit after cutting result")
		require.NotNil(t, out)
		assert.LessOrEqual(t, t8EncodedLen(t, out), ResultBoundChars,
			"the projection is within the 48,000-character bound")

		assert.Equal(t, []string{"result"}, t8StringSlice(t, out["truncated_fields"]),
			"only result is cut (longest first; prompt stays whole)")
		wantResult := result[:8000] + t8CutMarker(50000)
		assert.Equal(t, wantResult, out["result"],
			"result is its first 8,000 characters plus the fixed marker")
		assert.Equal(t, prompt, out["prompt"],
			"prompt (10,000 > 8,000) stays whole: the result already fits after cutting result")
		assert.Equal(t, desc, out["description"], "description is never a candidate")
		assert.Equal(t, "t-3", out["id"], "identity is always returned")
	})

	t.Run("project: cut order is longest first, markers exact (§7.13 size bound)", func(t *testing.T) {
		spec := FieldSpec{Object: "synthetic", Identity: []string{"id"}, Valid: []string{"sA", "sB", "small"}}
		sA := strings.Repeat("A", 45000)
		sB := "B|" + strings.Repeat("b", 42000-len("B|"))
		full := map[string]any{"id": "x", "sA": sA, "sB": sB, "small": "keep"}
		out, errRes := t8Project(t, full, spec, spec.Valid)
		require.Nil(t, errRes, "cutting sA then sB must make the result fit")
		require.NotNil(t, out)
		assert.LessOrEqual(t, t8EncodedLen(t, out), ResultBoundChars)
		assert.Equal(t, []string{"sA", "sB"}, t8StringSlice(t, out["truncated_fields"]),
			"longest first: sA (45,000) before sB (42,000)")
		assert.Equal(t, sA[:8000]+t8CutMarker(45000), out["sA"])
		assert.Equal(t, sB[:8000]+t8CutMarker(42000), out["sB"])
		assert.Equal(t, "keep", out["small"], "values of 8,000 or less are never touched")
	})

	t.Run("project: cutting stops as soon as the result fits (§7.13 \"until the result fits\")", func(t *testing.T) {
		// After cutting big45k the result fits, so s8001 (8,001 characters)
		// is never reached. NOTE (finding F-SEL-1): the §13.1 test-35 row's
		// "8,001 cut" is only reachable under a cut-all-candidates reading;
		// the operative sentence is "until the result fits". This test pins
		// the operative reading; the ambiguity is reported, not silently
		// resolved.
		spec := FieldSpec{Object: "synthetic", Identity: []string{"id"}, Valid: []string{"big45k", "s8001"}}
		s8001 := strings.Repeat("z", 8001)
		full := map[string]any{"id": "x", "big45k": strings.Repeat("q", 45000), "s8001": s8001}
		out, errRes := t8Project(t, full, spec, spec.Valid)
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.Equal(t, []string{"big45k"}, t8StringSlice(t, out["truncated_fields"]))
		assert.Equal(t, s8001, out["s8001"],
			"once the result fits, no further candidate is cut")
	})

	t.Run("project: exactly 8,000 characters is never a candidate (§7.13 cut rule)", func(t *testing.T) {
		spec := FieldSpec{Object: "synthetic", Identity: []string{"id"}, Valid: []string{"big45k", "s8k"}}
		s8k := strings.Repeat("e", 8000)
		full := map[string]any{"id": "x", "big45k": strings.Repeat("f", 45000), "s8k": s8k}
		out, errRes := t8Project(t, full, spec, spec.Valid)
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.Equal(t, s8k, out["s8k"], "a value of exactly 8,000 characters is untouched")
		assert.NotContains(t, t8StringSlice(t, out["truncated_fields"]), "s8k")
	})

	t.Run("project: identity keys are never cut (§7.13 size bound)", func(t *testing.T) {
		spec := FieldSpec{Object: "synthetic", Identity: []string{"id"}, Valid: []string{"big45k"}}
		longID := strings.Repeat("i", 9000)
		full := map[string]any{"id": longID, "big45k": strings.Repeat("f", 45000)}
		out, errRes := t8Project(t, full, spec, spec.Valid)
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.Equal(t, longID, out["id"], "identity keys are excluded by name, never cut")
		assert.NotContains(t, t8StringSlice(t, out["truncated_fields"]), "id")
	})

	t.Run("project: arrays are never cut or altered (§7.13 cut rule)", func(t *testing.T) {
		spec := FieldSpec{Object: "synthetic", Identity: []string{"id"}, Valid: []string{"big45k", "list"}}
		list := []any{strings.Repeat("L", 6000), strings.Repeat("M", 6000)}
		full := map[string]any{"id": "x", "big45k": strings.Repeat("f", 45000), "list": list}
		out, errRes := t8Project(t, full, spec, spec.Valid)
		require.Nil(t, errRes)
		require.NotNil(t, out)
		got := t8AnySlice(t, out["list"])
		require.Len(t, got, 2, "the array keeps both entries")
		assert.Equal(t, strings.Repeat("L", 6000), got[0])
		assert.Equal(t, strings.Repeat("M", 6000), got[1])
		assert.NotContains(t, t8StringSlice(t, out["truncated_fields"]), "list",
			"arrays are never cut, whatever their encoded length")
	})

	t.Run("project: agent soul, goal latest_reason, plan paused_reason cut (20.8)", func(t *testing.T) {
		soul := "SOUL|" + strings.Repeat("s", 60000-len("SOUL|"))
		agent := &generated.AgentDetail{Id: "a-1", Revision: "r1"}
		setString(t, agent, "Soul", soul)
		out, errRes := t8Project(t, agent, t8SpecAgent(), t8ValidAgent())
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.Equal(t, []string{"soul"}, t8StringSlice(t, out["truncated_fields"]))
		assert.Equal(t, soul[:8000]+t8CutMarker(60000), out["soul"])
		assert.LessOrEqual(t, t8EncodedLen(t, out), ResultBoundChars)

		latest := strings.Repeat("l", 50000)
		goal := &generated.Goal{GoalId: "g-1", State: generated.GoalState("active"), LatestReason: &latest}
		out, errRes = t8Project(t, goal, t8SpecGoal(), t8ValidGoal())
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.Equal(t, []string{"latest_reason"}, t8StringSlice(t, out["truncated_fields"]))
		assert.Equal(t, latest[:8000]+t8CutMarker(50000), out["latest_reason"])

		paused := strings.Repeat("p", 50000)
		plan := &generated.Plan{Id: "pl-1", State: generated.PlanState("running"), PausedReason: &paused}
		out, errRes = t8Project(t, plan, t8SpecPlan(), t8ValidPlan())
		require.Nil(t, errRes)
		require.NotNil(t, out)
		assert.Equal(t, []string{"paused_reason"}, t8StringSlice(t, out["truncated_fields"]))
		assert.Equal(t, paused[:8000]+t8CutMarker(50000), out["paused_reason"])
	})

}

func t8FieldSelectOversizeCases(t *testing.T) {
	t.Helper()
	t.Run("project: array-only oversize fails with the too-large template, no partial object (20.8b)", func(t *testing.T) {
		// Synthetic map fixture for the shared helper: terminal_history alone
		// exceeds the bound. This is NOT the real-store goal history required
		// by 20.8b; that integration fixture remains a traceability gap.
		entry := map[string]any{"at": "2026-10-31T00:00:00Z", "outcome": strings.Repeat("T", 6000)}
		history := make([]any, 0, 12)
		for i := 0; i < 12; i++ {
			history = append(history, entry) // 12 × ~6,100 ≈ 73,000 characters
		}
		goal := map[string]any{
			"goal_id":          "g-2",
			"state":            "active",
			"terminal_history": history,
			"prompt":           "small",
		}
		out, errRes := t8Project(t, goal, t8SpecGoal(), t8ValidGoal())
		require.NotNil(t, errRes, "an uncuttable array over the bound must fail the call")
		assert.True(t, errRes.IsError)
		assert.Nil(t, out, "no partial object is returned")
		reLarge := regexp.MustCompile(
			`^result too large for goal: \d+ characters after cutting long text \(limit 48,000\); ` +
				`largest field: terminal_history \(\d+ characters\)\. Request fewer fields\.$`)
		assert.Regexp(t, reLarge, errRes.ForLLM,
			"the too-large text follows the §7.13 template; <n>/<m> are derived numbers the spec does not pin")

		// Synthetic Plan-shaped map; not a persisted plan or read-tool proof.
		// A smaller selection of the same object must then fit. Real-store
		// plan DoD and numeric-error-count assertions remain integration gaps.
		dod := make([]any, 0, 10)
		for i := 0; i < 10; i++ {
			dod = append(dod, map[string]any{"text": strings.Repeat("D", 6000)})
		}
		plan := map[string]any{"id": "pl-2", "state": "running", "dod": dod}
		out, errRes = t8Project(t, plan, t8SpecPlan(), t8ValidPlan())
		require.NotNil(t, errRes)
		assert.Nil(t, out)
		reLargePlan := regexp.MustCompile(
			`^result too large for plan: \d+ characters after cutting long text \(limit 48,000\); ` +
				`largest field: dod \(\d+ characters\)\. Request fewer fields\.$`)
		assert.Regexp(t, reLargePlan, errRes.ForLLM)

		out, errRes = t8Project(t, plan, t8SpecPlan(), []string{"state"})
		require.Nil(t, errRes, "fields=[\"state\"] on the same plan succeeds")
		require.NotNil(t, out)
		assert.ElementsMatch(t, []string{"id", "state"}, t8Keys(out))
	})

	t.Run("project: too-large with a long string and a large array; input untouched (20.11)", func(t *testing.T) {
		spec := FieldSpec{Object: "synthetic", Identity: []string{"id"}, Valid: []string{"longstr", "biglist"}}
		list := []any{strings.Repeat("L", 6000), strings.Repeat("M", 6000), strings.Repeat("N", 6000),
			strings.Repeat("O", 6000), strings.Repeat("P", 6000), strings.Repeat("Q", 6000),
			strings.Repeat("R", 6000), strings.Repeat("S", 6000), strings.Repeat("U", 6000)}
		longstr := strings.Repeat("w", 20000)
		full := map[string]any{"id": "x", "longstr": longstr, "biglist": list}
		out, errRes := t8Project(t, full, spec, spec.Valid)
		require.NotNil(t, errRes, "even after cutting longstr the array keeps the result over the bound")
		assert.True(t, errRes.IsError)
		assert.Nil(t, out, "no partial object")
		assert.Regexp(t, regexp.MustCompile(
			`^result too large for synthetic: \d+ characters after cutting long text \(limit 48,000\); `+
				`largest field: biglist \(\d+ characters\)\. Request fewer fields\.$`), errRes.ForLLM)
		// The input object was not altered before the error.
		assert.Equal(t, longstr, full["longstr"])
		assert.Equal(t, list, full["biglist"])
	})
}

// -----------------------------------------------------------------------------
// Test 35b — TestFieldSelect_ThroughRegistry (scenario 20.10, FR-186)
// -----------------------------------------------------------------------------

// TestFieldSelect_ThroughRegistry runs the §7.13 texts through the production
// registry boundary: Registry.Execute must answer a bad fields argument with
// the shared §7.13 text — never the generic "invalid arguments" text — and
// never reach the tool's Execute. Registration point per §16:
// general_builtin_catalog.go::GeneralBuiltinMetadata.
func TestFieldSelect_ThroughRegistry(t *testing.T) {
	const (
		getTask = "get_task"
		getPlan = "get_plan"
		getGoal = "get_goal"
	)

	type readerCase struct {
		tool   string
		spec   FieldSpec
		valid  string // one valid name for that reader
		objArg map[string]any
	}
	cases := []readerCase{
		{getTask, t8SpecTask(), "title", map[string]any{"task_id": "t-x"}},
		{getPlan, t8SpecPlan(), "title", map[string]any{"plan_id": "p-x"}},
		{getGoal, t8SpecGoal(), "prompt", map[string]any{}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			tool, ok := t8FindGeneralTool(tc.tool)
			if !ok {
				t.Fatalf("BLOCKED: %s not implemented — required by spec §7.9/§16 (registration in "+
					"general_builtin_catalog.go); scenarios 20.10 cannot run against it yet", tc.tool)
			}
			reg := NewToolRegistry()
			reg.Register(tool)

			badArgs := func(fields any) map[string]any {
				args := map[string]any{}
				for k, v := range tc.objArg {
					args[k] = v
				}
				if fields != nil {
					args["fields"] = fields
				}
				return args
			}

			t.Run("fields missing gives the required text, not generic validation", func(t *testing.T) {
				res := reg.Execute(context.Background(), tc.tool, badArgs(nil))
				require.NotNil(t, res)
				assert.True(t, res.IsError)
				want := fmt.Sprintf(`fields is required for %s: pass ["all"] or one or more of: %s`,
					tc.spec.Object, strings.Join(tc.spec.Valid, ", "))
				assert.Equal(t, want, res.ForLLM)
				assert.NotContains(t, res.ForLLM, "invalid arguments for tool",
					"the shared error is the one the agent sees (§7.13 validation boundary)")
			})

			t.Run("fields as a string gives the required text", func(t *testing.T) {
				res := reg.Execute(context.Background(), tc.tool, badArgs("all"))
				require.NotNil(t, res)
				assert.True(t, res.IsError)
				want := fmt.Sprintf(`fields is required for %s: pass ["all"] or one or more of: %s`,
					tc.spec.Object, strings.Join(tc.spec.Valid, ", "))
				assert.Equal(t, want, res.ForLLM)
			})

			t.Run("unknown name gives the unknown text through the registry", func(t *testing.T) {
				res := reg.Execute(context.Background(), tc.tool, badArgs([]any{tc.valid, "nope"}))
				require.NotNil(t, res)
				assert.True(t, res.IsError)
				want := fmt.Sprintf(`unknown field "nope" for %s. Valid: all, %s`,
					tc.spec.Object, strings.Join(tc.spec.Valid, ", "))
				assert.Equal(t, want, res.ForLLM)
				assert.NotContains(t, res.ForLLM, "invalid arguments for tool")
			})

			t.Run("all combined gives the combination text through the registry", func(t *testing.T) {
				res := reg.Execute(context.Background(), tc.tool, badArgs([]any{"all", tc.valid}))
				require.NotNil(t, res)
				assert.True(t, res.IsError)
				assert.Equal(t, `"all" cannot be combined with other field names`, res.ForLLM)
			})
		})
	}
}

// t8FindGeneralTool looks a tool up in the production general catalog
// metadata (the §16 registration row the four read tools must join).
func t8FindGeneralTool(name string) (Tool, bool) {
	for _, tl := range GeneralBuiltinMetadata() {
		if tl.Name() == name {
			return tl, true
		}
	}
	return nil, false
}

// -----------------------------------------------------------------------------
// Test 36 — TestFieldSelect_EnumsMatchSchemas (scenario 20.9, FR-184, FR-185)
// -----------------------------------------------------------------------------

// TestFieldSelect_EnumsMatchSchemas pins the enum formula for every read
// tool: fields.enum = ["all"] + schema properties − identity − result
// metadata. Expected values are derived at test time from the generated wire
// types (the committed contracts), never typed by hand twice.
func TestFieldSelect_EnumsMatchSchemas(t *testing.T) {
	metadataNames := []string{"absent_fields", "truncated_fields"}

	// Half that needs no tool: no schema property of Task, Plan or Goal uses
	// a result-metadata name (P-2 / FR-184's negative half).
	t.Run("no schema property collides with result metadata names", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			typ  any
		}{
			{"Task", &generated.Task{}},
			{"Plan", &generated.Plan{}},
			{"Goal", &generated.Goal{}},
		} {
			for _, prop := range t8SchemaProps(t, tc.typ) {
				assert.NotContains(t, metadataNames, prop,
					"%s: property %q must not reuse a result-metadata name", tc.name, prop)
			}
		}
	})

	type enumCase struct {
		tool     string
		spec     FieldSpec
		props    []string
		identity []string
	}
	cases := []enumCase{
		{"get_task", t8SpecTask(), t8SchemaProps(t, &generated.Task{}), []string{"id"}},
		{"get_plan", t8SpecPlan(), t8SchemaProps(t, &generated.Plan{}), []string{"id"}},
		{"get_goal", t8SpecGoal(), t8SchemaProps(t, &generated.Goal{}), []string{"goal_id"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			tool, ok := t8FindGeneralTool(tc.tool)
			if !ok {
				t.Fatalf("BLOCKED: %s not implemented — required by FR-184 (the fields enum must be "+
					"derived from the schema, §7.13)", tc.tool)
			}
			enum := t8FieldsEnum(t, tool.Parameters())

			wantValid := t8Minus(tc.props, append(append([]string{}, tc.identity...), metadataNames...))
			wantEnum := append([]string{"all"}, wantValid...)

			// "all" present exactly once, never a schema property.
			assert.Equal(t, 1, t8Count(enum, "all"), `"all" appears exactly once`)
			assert.NotContains(t, tc.props, "all", `"all" is not a schema property`)

			// Metadata never selectable.
			for _, m := range metadataNames {
				assert.NotContains(t, enum, m)
			}

			// The formula, order included: ["all"] + Valid in schema order.
			assert.Equal(t, wantEnum, enum,
				"%s: fields enum must equal [all] + schema properties − identity − metadata (FR-184)", tc.tool)

			// And the spec's Valid table must equal the schema-derived list
			// (this is the drift alarm: a schema change fails here until the
			// tool's enum follows).
			assert.Equal(t, wantValid, tc.spec.Valid,
				"%s: the spec-table Valid list drifted from the committed schema — update the spec table", tc.tool)
		})
	}
}

// t8FieldsEnum extracts fields.items.enum from a tool's parameter schema.
func t8FieldsEnum(t *testing.T, params map[string]any) []string {
	t.Helper()
	props, ok := params["properties"].(map[string]any)
	require.True(t, ok, "parameters must carry properties")
	fieldsRaw, ok := props["fields"]
	require.True(t, ok, "parameters must declare the fields parameter")
	fields, ok := fieldsRaw.(map[string]any)
	require.True(t, ok)
	itemsRaw, ok := fields["items"]
	require.True(t, ok, "fields must declare items")
	items, ok := itemsRaw.(map[string]any)
	require.True(t, ok)
	enumRaw, ok := items["enum"]
	require.True(t, ok, "fields.items must declare the enum (FR-184)")
	enumList, ok := enumRaw.([]any)
	require.True(t, ok)
	var out []string
	for _, v := range enumList {
		s, ok := v.(string)
		require.True(t, ok, "enum entries must be strings")
		out = append(out, s)
	}
	return out
}

func t8Minus(all []string, drop []string) []string {
	dropped := map[string]bool{}
	for _, d := range drop {
		dropped[d] = true
	}
	var out []string
	for _, a := range all {
		if !dropped[a] {
			out = append(out, a)
		}
	}
	return out
}

func t8Count(list []string, s string) int {
	n := 0
	for _, v := range list {
		if v == s {
			n++
		}
	}
	return n
}

func t8Keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func t8StringSlice(t *testing.T, v any) []string {
	t.Helper()
	if v == nil {
		return nil
	}
	raw, ok := v.([]string)
	if ok {
		return raw
	}
	anyList, ok := v.([]any)
	require.True(t, ok, "expected a string slice, got %T", v)
	out := make([]string, 0, len(anyList))
	for _, e := range anyList {
		s, ok := e.(string)
		require.True(t, ok)
		out = append(out, s)
	}
	return out
}

func t8AnySlice(t *testing.T, v any) []any {
	t.Helper()
	list, ok := v.([]any)
	require.True(t, ok, "expected []any, got %T", v)
	return list
}

func strPtr(s string) *string { return &s }

// setString sets an exported struct field by name on a pointer-to-struct —
// used where the RED tests need a generated field whose Go name may shift
// with regeneration; the json name stays the spec's contract.
func setString(t *testing.T, obj any, field, value string) {
	t.Helper()
	rv := reflect.ValueOf(obj).Elem()
	f := rv.FieldByName(field)
	require.True(t, f.IsValid(), "field %s does not exist on %T — regenerate check", field, obj)
	require.Equal(t, reflect.Ptr, f.Kind(), "field %s must be a *string", field)
	require.Equal(t, reflect.String, f.Type().Elem().Kind())
	f.Set(reflect.ValueOf(&value))
}
