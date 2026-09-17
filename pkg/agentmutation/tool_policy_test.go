package agentmutation

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestApplyToolPolicyChangesPreservesSparseIntent(t *testing.T) {
	known := map[string]struct{}{"read_file": {}, "bash": {}, "search_web": {}}
	start := map[string]config.ToolPolicy{"read_file": config.ToolPolicyAsk, "bash": config.ToolPolicyDeny}
	got, err := ApplyToolPolicyChanges(start, ToolPolicyChanges{Set: map[string]config.ToolPolicy{"search_web": config.ToolPolicyAllow}, Remove: []string{"read_file"}}, known)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny, "search_web": config.ToolPolicyAllow}
	assertPolicyMap(t, got, want)
	assertPolicyMap(t, start, map[string]config.ToolPolicy{"read_file": config.ToolPolicyAsk, "bash": config.ToolPolicyDeny})
}

func TestApplyToolPolicyChangesRejectsInvalidPatchWithoutChangingInput(t *testing.T) {
	known := map[string]struct{}{"read_file": {}, "bash": {}}
	tests := []struct {
		name  string
		patch ToolPolicyChanges
		code  ErrorCode
	}{
		{name: "set remove overlap", patch: ToolPolicyChanges{Set: map[string]config.ToolPolicy{"bash": config.ToolPolicyAllow}, Remove: []string{"bash"}}, code: InvalidInput},
		{name: "duplicate removal", patch: ToolPolicyChanges{Remove: []string{"bash", "bash"}}, code: InvalidInput},
		{name: "unknown set", patch: ToolPolicyChanges{Set: map[string]config.ToolPolicy{"unknown": config.ToolPolicyAllow}}, code: InvalidInput},
		{name: "unknown remove", patch: ToolPolicyChanges{Remove: []string{"unknown"}}, code: InvalidInput},
		{name: "invalid value", patch: ToolPolicyChanges{Set: map[string]config.ToolPolicy{"bash": "sometimes"}}, code: InvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := map[string]config.ToolPolicy{"read_file": config.ToolPolicyAsk}
			_, err := ApplyToolPolicyChanges(start, tc.patch, known)
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Code != tc.code {
				t.Fatalf("error=%#v want %s", err, tc.code)
			}
			assertPolicyMap(t, start, map[string]config.ToolPolicy{"read_file": config.ToolPolicyAsk})
		})
	}
}

func TestApplyToolPolicyChangesMissingRemovalIsNoOpAndLastRemovalLeavesEmptyMap(t *testing.T) {
	known := map[string]struct{}{"read_file": {}, "bash": {}}
	got, err := ApplyToolPolicyChanges(map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}, ToolPolicyChanges{Remove: []string{"read_file", "bash"}}, known)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got=%v want allocated empty sparse map", got)
	}
}

func TestSelectOverridesRequiresFreshInheritedEchoes(t *testing.T) {
	complete := map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow, "bash": config.ToolPolicyDeny}
	ceiling := map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow, "bash": config.ToolPolicyAllow}
	got, err := SelectOverrides(complete, []string{"bash"}, ceiling)
	if err != nil {
		t.Fatal(err)
	}
	assertPolicyMap(t, got, map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny})

	_, err = SelectOverrides(map[string]config.ToolPolicy{"read_file": config.ToolPolicyDeny, "bash": config.ToolPolicyDeny}, []string{"bash"}, ceiling)
	if !errors.Is(err, ErrCeilingChanged) {
		t.Fatalf("error=%v want ErrCeilingChanged", err)
	}
}

func TestSelectOverridesRejectsDuplicateUnknownAndIncompleteNames(t *testing.T) {
	ceiling := map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow, "bash": config.ToolPolicyAllow}
	for _, tc := range []struct {
		name     string
		complete map[string]config.ToolPolicy
		names    []string
	}{
		{name: "duplicate", complete: ceiling, names: []string{"bash", "bash"}},
		{name: "unknown", complete: ceiling, names: []string{"unknown"}},
		{name: "incomplete", complete: map[string]config.ToolPolicy{"bash": config.ToolPolicyAllow}, names: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SelectOverrides(tc.complete, tc.names, ceiling); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSelectOverridesPreservesMCPExactAndWildcardOverrides(t *testing.T) {
	ceiling := map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow, "bash": config.ToolPolicyAllow}
	complete := map[string]config.ToolPolicy{
		"read_file":          config.ToolPolicyAllow,
		"bash":               config.ToolPolicyDeny,
		"mcp_context7_*":     config.ToolPolicyAsk,
		"mcp_context7_query": config.ToolPolicyAllow,
	}
	got, err := SelectOverrides(complete, []string{"bash", "mcp_context7_*", "mcp_context7_query"}, ceiling)
	if err != nil {
		t.Fatal(err)
	}
	assertPolicyMap(t, got, map[string]config.ToolPolicy{
		"bash":               config.ToolPolicyDeny,
		"mcp_context7_*":     config.ToolPolicyAsk,
		"mcp_context7_query": config.ToolPolicyAllow,
	})
}

func TestSelectOverridesRejectsUnknownNonMCPExtra(t *testing.T) {
	ceiling := map[string]config.ToolPolicy{"bash": config.ToolPolicyAllow}
	for _, tc := range []struct {
		name     string
		policy   string
		override string
	}{
		{name: "unknown non-MCP key", policy: "browser_*", override: string(config.ToolPolicyDeny)},
		{name: "invalid MCP value", policy: "mcp_context7_*", override: "sometimes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			complete := map[string]config.ToolPolicy{"bash": config.ToolPolicyAllow, tc.policy: config.ToolPolicy(tc.override)}
			if _, err := SelectOverrides(complete, []string{tc.policy}, ceiling); err == nil {
				t.Fatal("expected invalid extra policy to be rejected")
			}
		})
	}
}

func assertPolicyMap(t *testing.T, got, want map[string]config.ToolPolicy) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}
