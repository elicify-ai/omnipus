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

		// SPEC CHANGE 2026-10-24 (founder decision): OLD — a replacement
		// omitting active_form succeeded and removed the earlier active form;
		// NEW — active_form is required on every item of every call, so the
		// replacement is rejected and the earlier card keeps its active form.
		replacement := NewSetTodosTool(reloaded).Execute(ctx, map[string]any{
			"outcome": "Quarter plan",
			"todos":   []any{map[string]any{"text": "Publish the summary", "status": "completed"}},
		})
		assertActiveFormRequiredRejection(t, replacement, 0)
		cards, err = task.New(dir).List(task.Filter{AgentID: "mia"})
		if err != nil || len(cards) != 1 || cards[0].ID != card.ID {
			t.Fatalf("rejected replacement must not touch the card: cards=%v, err=%v", cards, err)
		}
		assertTodoActiveFormJSON(t, cards[0].Todos,
			`[{"text":"Draft the Q4 summary","status":"in_progress","active_form":"Drafting the Q4 summary"}]`)
	})

	t.Run("required_trimmed_and_bounded", func(t *testing.T) {
		cases := []struct {
			name            string
			item            map[string]any
			want            string
			wantErr         string
			wantErrContains []string
		}{
			// SPEC CHANGE 2026-10-24: absent/empty/whitespace were accepted
			// under the old optional spec; active_form is now required and
			// must be non-empty after the established trim.
			{name: "absent", item: map[string]any{"text": "Draft the Q4 summary"}, wantErrContains: activeFormRequiredErrElements},
			{name: "empty", item: map[string]any{"text": "Draft the Q4 summary", "active_form": ""}, wantErrContains: activeFormRequiredErrElements},
			{name: "whitespace", item: map[string]any{"text": "Draft the Q4 summary", "active_form": " \t\n "}, wantErrContains: activeFormRequiredErrElements},
			{name: "one_character", item: map[string]any{"text": "Draft the Q4 summary", "active_form": "a"}, want: "a"},
			{name: "two_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": "ab"}, want: "ab"},
			{name: "499_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("a", 499)}, want: strings.Repeat("a", 499)},
			{name: "500_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("a", 500)}, want: strings.Repeat("a", 500)},
			{name: "500_unicode_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("界", 500)}, want: strings.Repeat("界", 500)},
			{name: "500_supplementary_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("\U00020000", 500)}, want: strings.Repeat("\U00020000", 500)},
			{name: "trim_before_limit", item: map[string]any{"text": "Draft the Q4 summary", "active_form": " " + strings.Repeat("a", 500) + " "}, want: strings.Repeat("a", 500)},
			{name: "501_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("a", 501)}, wantErr: "todos[0]: active_form must be 500 characters or fewer"},
			{name: "501_unicode_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("界", 501)}, wantErr: "todos[0]: active_form must be 500 characters or fewer"},
			{name: "501_supplementary_characters", item: map[string]any{"text": "Draft the Q4 summary", "active_form": strings.Repeat("\U00020000", 501)}, wantErr: "todos[0]: active_form must be 500 characters or fewer"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				store := task.New(t.TempDir())
				ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
				result := NewSetTodosTool(store).Execute(ctx, map[string]any{
					"outcome": "Quarter plan", "todos": []any{tc.item},
				})
				if len(tc.wantErrContains) > 0 {
					assertActiveFormRequiredRejection(t, result, 0)
					return
				}
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
					// SPEC CHANGE 2026-10-24: item 0 now carries active_form so
					// the only violation under the new required-spec is item 1's
					// non-string value.
					"todos": []any{
						map[string]any{"text": "First step", "active_form": "Preparing the first step"},
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

	t.Run("existing_item_without_active_form_rejected_card_unchanged", func(t *testing.T) {
		// SPEC CHANGE 2026-10-24: OLD — a call without active_form succeeded
		// and left the existing item unchanged; NEW — the call itself is
		// rejected (the requirement is on the call's items; the stored item's
		// legacy missing active_form does not excuse the call) and the card is
		// unchanged.
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
		assertActiveFormRequiredRejection(t, result, 0)
		cards, err := task.New(dir).List(task.Filter{AgentID: "mia"})
		if err != nil || len(cards) != 1 || cards[0].ID != card.ID {
			t.Fatalf("rejected call must not touch the existing card: cards=%v, err=%v", cards, err)
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
	// SPEC CHANGE 2026-10-24: OLD — active_form optional (required was
	// ["text"]); NEW — the founder decision requires active_form on every
	// todo item.
	if !reflect.DeepEqual(items["required"], []string{"text", "active_form"}) {
		t.Errorf("active_form must be required: required = %#v, want [text active_form]", items["required"])
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
