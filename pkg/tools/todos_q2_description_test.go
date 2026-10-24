package tools

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSetTodosDescriptionQ2Exact(t *testing.T) {
	data, err := os.ReadFile("testdata/set_todos_q2_description.txt")
	if err != nil {
		t.Fatalf("read founder-approved Description fixture: %v", err)
	}
	want := strings.TrimSuffix(string(data), "\n")
	tool := NewSetTodosTool(nil)
	if got := tool.Description(); got != want {
		t.Errorf("Description = %q, want exact approved %q", got, want)
	}
	params := tool.Parameters()
	if !reflect.DeepEqual(params["required"], []string{"outcome", "todos"}) {
		t.Fatalf("Description requires outcome and full todos list, got %#v", params["required"])
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("parameter properties missing")
	}
	todos, ok := properties["todos"].(map[string]any)
	if !ok {
		t.Fatal("todos parameter missing")
	}
	items, ok := todos["items"].(map[string]any)
	if !ok {
		t.Fatal("todo item schema missing")
	}
	itemProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("todo item properties missing")
	}
	if !reflect.DeepEqual(itemProperties["active_form"], map[string]any{"type": "string", "maxLength": 500}) {
		t.Errorf("active_form must remain an item-level string with max500: %#v", itemProperties["active_form"])
	}
	if _, topLevel := properties["active_form"]; topLevel {
		t.Error("active_form must not become a top-level parameter")
	}
	if !reflect.DeepEqual(items["required"], []string{"text"}) {
		t.Errorf("active_form must remain optional, got required=%#v", items["required"])
	}
}
