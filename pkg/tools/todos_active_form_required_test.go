package tools

// RED test pack — founder decision 2026-10-24: set_todos must REQUIRE
// `active_form` on every todo item, so the chat's "working on…" line always
// shows the current step (#1406). Train 3 hands-on row WO1 failed end to end
// because the agreed UAT model never sent the then-optional field. Issue
// #1444 adds that the rejection must be a normal, recoverable validation
// error the model can retry, not a turn failure.
//
// Test plan (expected values derived from the spec, never from the code):
//
//	Behaviour: a set_todos call whose todo item has no usable active_form is
//	rejected before any write, with a corrective message the model can act
//	on; the tool schema and Description state the requirement.
//
//	Spec sources:
//	  R1 schema   — brief item 1: active_form required on every item, type
//	                string, maxLength 500 ("trimmed, non-empty after trim" is
//	                behavioural and covered by R2; JSON schema here cannot
//	                express trimming). The exact required list is pinned as
//	                ["text", "active_form"]: `text` was already required, the
//	                founder decision adds only active_form, and the order
//	                follows the existing declaration order (assumption flagged
//	                to the chief — the one thing the spec does not fix).
//	  R2 validate — brief item 2: missing, empty or whitespace-only
//	                active_form is rejected; the message names the item
//	                index, says active_form is required, and carries an
//	                example phrase. "Whitespace" includes input that the
//	                established Q10=A normalization trims to empty
//	                (tests/fixtures/q10-active-form-normalization.json). The
//	                canonical example phrase of this feature is "Drafting the
//	                Q4 summary" (Description text, UAT evidence) — pinned so
//	                the example stays consistent across surfaces.
//	  R3 describe — brief item 3: the Description states the requirement and
//	                the purpose. Pinned at phrase level until the prompt
//	                engineer's text lands; testdata/set_todos_q2_description.txt
//	                is then updated via the chief.
//	  R4 retry    — issue #1444: the rejection is an IsError tool result,
//	                writes nothing, and a corrected retry succeeds on both the
//	                create and the replace path.
//	  R5 Q74      — chief ruling 2026-10-24: the wider old-spec tests are
//	                reconciled by the same test author in this change
//	                (todos_test.go, task_provenance_test.go, the uatRun4
//	                fixture, and the Q10 fixture's empty-after-trim cases —
//	                "Q10 empty cases superseded by founder Q74 2026-10-24":
//	                the required-message replaces acceptance; trim
//	                normalization for non-empty values and the 38-case count
//	                stay).
//	  R3 fixture  — pending: the prompt engineer's Description text lands on
//	                work/t3-afreq-text; testdata/set_todos_q2_description.txt
//	                is then updated to match it exactly (chief sends the
//	                merged SHA).
//
//	Mutations for CHECK (deferred per RED rules, elicify-test-writing step 4):
//	remove the required entry from the item schema; skip the
//	empty-after-trim check in the parser; leave "Optionally" in the
//	Description; write the card before validating the items.
//
//	Known gaps: the frontend display is covered by the existing control
//	(src/components/chat/AgentStatusIndicator.active-form.test.tsx); the
//	agent system-prompt checklist rule (pkg/agent rule-3 text) is a
//	different surface and out of scope here.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// activeFormRequiredExample is the canonical example phrase the rejection
// message and the Description must carry (spec source: the feature's own
// example everywhere — Description text, tests, UAT evidence).
const activeFormRequiredExample = "Drafting the Q4 summary"

// activeFormRequiredErrElements is the spec-pinned rejection-message content
// for item 0: the item index, the requirement itself, the example phrase.
var activeFormRequiredErrElements = []string{
	"todos[0]",
	"active_form is required",
	strings.ToLower(activeFormRequiredExample),
}

// assertActiveFormRequiredRejection asserts the spec-pinned elements of the
// required-active_form rejection: an IsError tool result (the recoverable
// class a model can retry — issue #1444) whose message names the item index,
// states that active_form is required, and carries the example phrase.
func assertActiveFormRequiredRejection(t *testing.T, result *ToolResult, itemIndex int) {
	t.Helper()
	if result == nil {
		t.Fatal("no tool result")
	}
	if !result.IsError {
		t.Fatalf("call must be rejected (active_form required), got success: %q", result.ForLLM)
	}
	assertActiveFormRequiredErrorText(t, result.ForLLM, itemIndex)
}

// assertActiveFormRequiredErrorText asserts the same spec-pinned message
// elements against a plain error string (parse-path variant).
func assertActiveFormRequiredErrorText(t *testing.T, text string, itemIndex int) {
	t.Helper()
	plain := strings.ToLower(strings.ReplaceAll(text, "`", ""))
	for _, want := range []string{
		fmt.Sprintf("todos[%d]", itemIndex),
		"active_form is required",
		strings.ToLower(activeFormRequiredExample),
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("rejection message %q must contain %q", text, want)
		}
	}
}

// R1: the todo item schema lists active_form in the exact required list.
func TestSetTodosActiveFormRequiredItemSchema(t *testing.T) {
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
	if !reflect.DeepEqual(items["required"], []string{"text", "active_form"}) {
		t.Errorf("todo item required = %#v, want [text active_form]", items["required"])
	}
	itemProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing todo item properties")
	}
	want := map[string]any{"type": "string", "maxLength": 500}
	if !reflect.DeepEqual(itemProperties["active_form"], want) {
		t.Errorf("active_form schema = %#v, want %#v", itemProperties["active_form"], want)
	}
	if _, topLevel := properties["active_form"]; topLevel {
		t.Error("active_form must be an item-level requirement, not a top-level parameter")
	}
}

// R2: missing, empty, whitespace-only and invisible-only active_form values
// are rejected with a corrective message naming the item index.
func TestSetTodosActiveFormRequiredRejections(t *testing.T) {
	cases := []struct {
		name  string
		items []any
		index int
	}{
		{
			name:  "absent",
			items: []any{map[string]any{"text": "Draft the Q4 summary", "status": "in_progress"}},
			index: 0,
		},
		{
			name:  "empty_string",
			items: []any{map[string]any{"text": "Draft the Q4 summary", "status": "in_progress", "active_form": ""}},
			index: 0,
		},
		{
			name:  "whitespace_only",
			items: []any{map[string]any{"text": "Draft the Q4 summary", "status": "in_progress", "active_form": " \t\n "}},
			index: 0,
		},
		{
			// Invisible-only input normalizes to empty (founder Q10=A trim),
			// so it is not "non-empty after trim".
			name:  "invisible_only_normalizes_to_empty",
			items: []any{map[string]any{"text": "Draft the Q4 summary", "status": "in_progress", "active_form": "\u200b"}},
			index: 0,
		},
		{
			name: "second_item_absent_names_index",
			items: []any{
				map[string]any{"text": "First step", "status": "pending", "active_form": "Preparing the first step"},
				map[string]any{"text": "Draft the Q4 summary", "status": "in_progress"},
			},
			index: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
			result := NewSetTodosTool(task.New(dir)).Execute(ctx, map[string]any{
				"outcome": "Quarter plan",
				"todos":   tc.items,
			})
			assertActiveFormRequiredRejection(t, result, tc.index)
			cards, err := task.New(dir).List(task.Filter{AgentID: "mia"})
			if err != nil || len(cards) != 0 {
				t.Errorf("rejected call must not create a card: cards=%v, err=%v", cards, err)
			}
		})
	}
}

// R3: the Description states that active_form is required and what it is for.
// Phrase-level pins until the prompt engineer's text lands; the exact-text
// fixture pin stays in TestSetTodosDescriptionQ2Exact.
func TestSetTodosDescriptionStatesActiveFormRequired(t *testing.T) {
	description := NewSetTodosTool(nil).Description()
	sentences := strings.Split(description, ". ")
	foundRequired := false
	for _, sentence := range sentences {
		plain := strings.ToLower(strings.ReplaceAll(sentence, "`", ""))
		if !strings.Contains(plain, "active_form") {
			continue
		}
		if strings.Contains(plain, "required") {
			foundRequired = true
		}
		if strings.Contains(plain, "optional") {
			t.Errorf("Description sentence still calls active_form optional: %q", sentence)
		}
	}
	if !foundRequired {
		t.Errorf("Description must state, in a sentence about active_form, that it is required; got %q", description)
	}
	lower := strings.ToLower(description)
	if !strings.Contains(lower, "working-on") && !strings.Contains(lower, "working on") {
		t.Errorf("Description must keep the working-on line purpose; got %q", description)
	}
}

// R4 (issue #1444): the rejection is a normal, recoverable validation error —
// an IsError tool result that writes nothing, so the model can retry with a
// corrected call and succeed. Covers the create and the replace path; the
// replace path is exactly the train 3 WO1 failure (silent acceptance).
func TestSetTodosActiveFormRejectionIsRetryable(t *testing.T) {
	t.Run("create_path_retry_succeeds", func(t *testing.T) {
		dir := t.TempDir()
		ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
		rejected := NewSetTodosTool(task.New(dir)).Execute(ctx, map[string]any{
			"outcome": "Quarter plan",
			"todos":   []any{map[string]any{"text": "Draft the Q4 summary", "status": "in_progress"}},
		})
		assertActiveFormRequiredRejection(t, rejected, 0)
		cards, err := task.New(dir).List(task.Filter{AgentID: "mia"})
		if err != nil || len(cards) != 0 {
			t.Fatalf("rejected call must leave the store untouched: cards=%v, err=%v", cards, err)
		}
		retried := NewSetTodosTool(task.New(dir)).Execute(ctx, map[string]any{
			"outcome": "Quarter plan",
			"todos": []any{map[string]any{
				"text": "Draft the Q4 summary", "status": "in_progress",
				"active_form": activeFormRequiredExample,
			}},
		})
		if retried.IsError {
			t.Fatalf("corrected retry must succeed: %s", retried.ForLLM)
		}
		cards, err = task.New(dir).List(task.Filter{AgentID: "mia"})
		if err != nil || len(cards) != 1 {
			t.Fatalf("retry must create the card: cards=%v, err=%v", cards, err)
		}
		assertTodoActiveFormJSON(t, cards[0].Todos,
			`[{"text":"Draft the Q4 summary","status":"in_progress","active_form":"Drafting the Q4 summary"}]`)
	})

	t.Run("replace_path_retry_succeeds", func(t *testing.T) {
		dir := t.TempDir()
		ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
		seedTodos := []any{
			map[string]any{"text": "Draft the Q4 summary", "status": "completed", "active_form": "Drafting the Q4 summary"},
			map[string]any{"text": "Publish the summary", "status": "in_progress", "active_form": "Publishing the summary"},
		}
		if first := NewSetTodosTool(task.New(dir)).Execute(ctx, map[string]any{
			"outcome": "Quarter plan", "todos": seedTodos,
		}); first.IsError {
			t.Fatalf("seed call must succeed: %s", first.ForLLM)
		}
		cards, err := task.New(dir).List(task.Filter{AgentID: "mia"})
		if err != nil || len(cards) != 1 {
			t.Fatalf("seed: cards=%v, err=%v", cards, err)
		}
		cardID := cards[0].ID

		rejected := NewSetTodosTool(task.New(dir)).Execute(ctx, map[string]any{
			"outcome": "Quarter plan",
			"todos": []any{
				map[string]any{"text": "Draft the Q4 summary", "status": "completed", "active_form": "Drafting the Q4 summary"},
				map[string]any{"text": "Publish the summary", "status": "in_progress"}, // active_form dropped
			},
		})
		assertActiveFormRequiredRejection(t, rejected, 1)
		afterRejection, err := task.New(dir).Get(cardID)
		if err != nil {
			t.Fatalf("get after rejection: %v", err)
		}
		assertTodoActiveFormJSON(t, afterRejection.Todos,
			`[{"text":"Draft the Q4 summary","status":"completed","active_form":"Drafting the Q4 summary"},{"text":"Publish the summary","status":"in_progress","active_form":"Publishing the summary"}]`)

		retried := NewSetTodosTool(task.New(dir)).Execute(ctx, map[string]any{
			"outcome": "Quarter plan", "todos": seedTodos,
		})
		if retried.IsError {
			t.Fatalf("corrected retry must succeed: %s", retried.ForLLM)
		}
		afterRetry, err := task.New(dir).Get(cardID)
		if err != nil {
			t.Fatalf("get after retry: %v", err)
		}
		assertTodoActiveFormJSON(t, afterRetry.Todos,
			`[{"text":"Draft the Q4 summary","status":"completed","active_form":"Drafting the Q4 summary"},{"text":"Publish the summary","status":"in_progress","active_form":"Publishing the summary"}]`)
	})
}
