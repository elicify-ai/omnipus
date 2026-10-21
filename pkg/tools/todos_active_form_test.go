package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Expectations come from the founder's 2026-10-21 active_form requirements.
func TestSetTodosActiveForm(t *testing.T) {
	t.Run("accepted_persisted_and_read_back", func(t *testing.T) {
		dir := t.TempDir()
		tool := NewSetTodosTool(task.New(dir))
		ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
		result := tool.Execute(ctx, map[string]any{
			"outcome": "Quarter plan",
			"todos": []any{map[string]any{
				"text": "Draft the Q4 summary", "status": "in_progress",
				"active_form": "  Drafting the Q4 summary \n",
			}},
		})
		if result.IsError {
			t.Fatalf("active_form must be accepted: %s", result.ForLLM)
		}
		wantResult := "Outcome: Quarter plan\n- [in_progress] Draft the Q4 summary (active_form: Drafting the Q4 summary)\n"
		if result.ForLLM != wantResult {
			t.Errorf("read-on-write = %q, want %q", result.ForLLM, wantResult)
		}
		reloaded := task.New(dir)
		cards, err := reloaded.List(task.Filter{AgentID: "mia"})
		if err != nil || len(cards) != 1 {
			t.Fatalf("reload: cards=%v, err=%v", cards, err)
		}
		want := `[{"text":"Draft the Q4 summary","status":"in_progress","active_form":"Drafting the Q4 summary"}]`
		assertTodoActiveFormJSON(t, cards[0].Todos, want)
		card, err := reloaded.Get(cards[0].ID)
		if err != nil {
			t.Fatalf("read card: %v", err)
		}
		assertTodoActiveFormJSON(t, card.Todos, want)

		// Replacement must remove an earlier active form when it is omitted.
		replacement := NewSetTodosTool(reloaded).Execute(ctx, map[string]any{
			"outcome": "Quarter plan",
			"todos":   []any{map[string]any{"text": "Publish the summary", "status": "completed"}},
		})
		if replacement.IsError {
			t.Fatalf("replace: %s", replacement.ForLLM)
		}
		cards, err = task.New(dir).List(task.Filter{AgentID: "mia"})
		if err != nil || len(cards) != 1 || cards[0].ID != card.ID {
			t.Fatalf("replacement must reuse the card: cards=%v, err=%v", cards, err)
		}
		assertTodoActiveFormJSON(t, cards[0].Todos, `[{"text":"Publish the summary","status":"completed"}]`)
	})

	t.Run("optional_trimmed_and_bounded", func(t *testing.T) {
		cases := []struct {
			name    string
			item    map[string]any
			want    string
			wantErr string
		}{
			{name: "absent", item: map[string]any{"text": "Draft the Q4 summary"}},
			{name: "empty", item: map[string]any{"text": "Draft the Q4 summary", "active_form": ""}},
			{name: "whitespace", item: map[string]any{"text": "Draft the Q4 summary", "active_form": " \t\n "}},
			{name: "one_character", item: map[string]any{"text": "Draft the Q4 summary", "active_form": "a"}, want: "a"},
			{name: "two_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": "ab"}, want: "ab"},
			{name: "499_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("a", 499)}, want: strings.Repeat("a", 499)},
			{name: "500_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("a", 500)}, want: strings.Repeat("a", 500)},
			{name: "500_unicode_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("界", 500)}, want: strings.Repeat("界", 500)},
			{name: "trim_before_limit", item: map[string]any{"text": "Draft the Q4 summary", "active_form": " " + strings.Repeat("a", 500) + " "}, want: strings.Repeat("a", 500)},
			{name: "501_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("a", 501)}, wantErr: "todos[0]: active_form must be 500 characters or fewer"},
			{name: "501_unicode_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("界", 501)}, wantErr: "todos[0]: active_form must be 500 characters or fewer"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				store := task.New(t.TempDir())
				ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
				result := NewSetTodosTool(store).Execute(ctx, map[string]any{
					"outcome": "Quarter plan", "todos": []any{tc.item},
				})
				if tc.wantErr != "" {
					if !result.IsError || result.ForLLM != tc.wantErr {
						t.Fatalf("error = %t, %q; want %q", result.IsError, result.ForLLM, tc.wantErr)
					}
					return
				}
				if result.IsError {
					t.Fatalf("valid optional active_form rejected: %s", result.ForLLM)
				}
				cards, err := task.New(store.Dir()).List(task.Filter{AgentID: "mia"})
				if err != nil || len(cards) != 1 {
					t.Fatalf("reload: cards=%v, err=%v", cards, err)
				}
				want := map[string]any{"text": "Draft the Q4 summary", "status": "pending"}
				wantResult := "Outcome: Quarter plan\n- [pending] Draft the Q4 summary"
				if tc.want != "" {
					want["active_form"] = tc.want
					wantResult += " (active_form: " + tc.want + ")"
				}
				assertTodoActiveFormJSON(t, cards[0].Todos, mustTodoActiveFormJSON(t, []any{want}))
				if result.ForLLM != wantResult+"\n" {
					t.Errorf("read-on-write = %q, want %q", result.ForLLM, wantResult+"\n")
				}
			})
		}
	})

	t.Run("rejects_non_strings_without_writing", func(t *testing.T) {
		for name, value := range map[string]any{
			"number": 42.0, "boolean": true, "null": nil,
			"array": []any{"Drafting"}, "object": map[string]any{"text": "Drafting"},
		} {
			t.Run(name, func(t *testing.T) {
				store := task.New(t.TempDir())
				ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
				result := NewSetTodosTool(store).Execute(ctx, map[string]any{
					"outcome": "Quarter plan",
					"todos": []any{
						map[string]any{"text": "First step"},
						map[string]any{"text": "Draft the Q4 summary", "active_form": value},
					},
				})
				want := "todos[1]: active_form must be a string"
				if !result.IsError || result.ForLLM != want {
					t.Errorf("error = %t, %q; want %q", result.IsError, result.ForLLM, want)
				}
				cards, err := store.List(task.Filter{AgentID: "mia"})
				if err != nil || len(cards) != 0 {
					t.Fatalf("invalid input must not create a card: cards=%v, err=%v", cards, err)
				}
			})
		}
	})

	t.Run("existing_item_without_active_form_unchanged", func(t *testing.T) {
		dir := t.TempDir()
		store := task.New(dir)
		card := &task.Task{
			Title: "Quarter plan", Prompt: "Quarter plan", Action: task.ActionLLM,
			AgentID: "mia", WorkspaceID: "ws-1", Status: task.StatusInProgress,
			Scratchpad: true, Todos: []task.Todo{{Text: "Draft the Q4 summary", Status: task.TodoPending}},
		}
		if err := store.CreateByAgent(card, "mia"); err != nil {
			t.Fatalf("create existing card: %v", err)
		}
		ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
		result := NewSetTodosTool(task.New(dir)).Execute(ctx, map[string]any{
			"outcome": "Quarter plan", "todos": []any{map[string]any{"text": "Draft the Q4 summary"}},
		})
		want := "Outcome: Quarter plan\n- [pending] Draft the Q4 summary\n"
		if result.IsError || result.ForLLM != want {
			t.Fatalf("existing item changed: error=%t, result=%q, want=%q", result.IsError, result.ForLLM, want)
		}
		cards, err := task.New(dir).List(task.Filter{AgentID: "mia"})
		if err != nil || len(cards) != 1 || cards[0].ID != card.ID {
			t.Fatalf("existing card identity changed: cards=%v, err=%v", cards, err)
		}
		assertTodoActiveFormJSON(t, cards[0].Todos, `[{"text":"Draft the Q4 summary","status":"pending"}]`)
	})
}

func TestSetTodosActiveFormParameters(t *testing.T) {
	params := NewSetTodosTool(nil).Parameters()
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing parameter properties")
	}
	todos, ok := properties["todos"].(map[string]any)
	if !ok {
		t.Fatal("missing todos schema")
	}
	items, ok := todos["items"].(map[string]any)
	if !ok {
		t.Fatal("missing todo item schema")
	}
	itemProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing todo item properties")
	}
	want := map[string]any{"type": "string", "maxLength": 500}
	if !reflect.DeepEqual(itemProperties["active_form"], want) {
		t.Errorf("active_form schema = %#v, want %#v", itemProperties["active_form"], want)
	}
	if !reflect.DeepEqual(items["required"], []string{"text"}) {
		t.Errorf("active_form must be optional: required = %#v", items["required"])
	}
}

func assertTodoActiveFormJSON(t *testing.T, value any, want string) {
	t.Helper()
	got := mustTodoActiveFormJSON(t, value)
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("decode actual todos: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected todos: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("todos JSON = %s, want %s", got, want)
	}
}

func mustTodoActiveFormJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode todos: %v", err)
	}
	return string(data)
}
