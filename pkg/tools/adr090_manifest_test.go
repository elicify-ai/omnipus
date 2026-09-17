package tools

import (
	"reflect"
	"sort"
	"testing"
)

func TestADR090_GlobalVisibility37(t *testing.T) {
	wantFull := []string{
		"AskUserQuestion", "Skill", "append_file", "bash", "create_plan", "create_task", "delegate", "edit_file",
		"execute_plan", "fetch_url", "get_workspace", "goal_claim", "grep", "inspect_session", "library_list", "library_read",
		"list_agents", "list_directory", "list_jobs", "list_mounts", "list_tasks", "message_parent", "plan_correct", "read_file",
		"recall_conversation", "recall_memory", "remember", "search_web", "send_file", "send_message", "set_goal", "set_todos",
		"stop_plan", "switch_agent", "update_task", "write_file",
	}
	sort.Strings(wantFull)
	if got := FullManifestToolNames(); !reflect.DeepEqual(got, wantFull) {
		t.Fatalf("compressed full definitions = %#v, want literal ADR-090 set %#v", got, wantFull)
	}
	if got := InfraManifestToolNames(); !reflect.DeepEqual(got, []string{"ToolSearch"}) {
		t.Fatalf("infra definitions = %#v, want [ToolSearch]", got)
	}
	if got := PreviewedLazyToolNames(); !reflect.DeepEqual(got, []string{"serve_web"}) {
		t.Fatalf("compressed previews = %#v, want [serve_web]", got)
	}
}
