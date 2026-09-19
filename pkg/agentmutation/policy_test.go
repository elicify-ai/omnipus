package agentmutation

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestValidateFieldsAppliesADR090RoleMatrix(t *testing.T) {
	tests := []struct {
		name   string
		agent  config.AgentConfig
		fields []string
		code   ErrorCode
	}{
		{name: "ordinary capability edit", agent: config.AgentConfig{ID: "mia", Type: config.AgentTypeCore, Locked: true}, fields: []string{"skills", "mcp_servers", "tool_policy_changes"}},
		{name: "ordinary same-value identity echo protected", agent: config.AgentConfig{ID: "mia", Type: config.AgentTypeCore, Locked: true}, fields: []string{"name"}, code: ProtectedField},
		{name: "ordinary soul protected", agent: config.AgentConfig{ID: "jim", Type: config.AgentTypeCore, Locked: true}, fields: []string{"soul"}, code: ProtectedField},
		{name: "hidden nonblank soul editable", agent: config.AgentConfig{ID: "judge", Type: config.AgentTypeSystem, Locked: true}, fields: []string{"soul"}},
		{name: "hidden capabilities fixed", agent: config.AgentConfig{ID: "plansupervisor", Type: config.AgentTypeSystem, Locked: true}, fields: []string{"skills"}, code: ProtectedField},
		{name: "hidden memory fixed", agent: config.AgentConfig{ID: "judge", Type: config.AgentTypeSystem, Locked: true}, fields: []string{"memory_enabled"}, code: ProtectedField},
		{name: "custom native capabilities editable", agent: config.AgentConfig{ID: "writer", Type: config.AgentTypeWorker}, fields: []string{"skills", "mcp_servers"}},
		{name: "type immutable", agent: config.AgentConfig{ID: "writer", Type: config.AgentTypeWorker}, fields: []string{"type"}, code: ProtectedField},
		{name: "external native skills unsupported", agent: externalAgent("codex"), fields: []string{"skills"}, code: InvalidInput},
		{name: "external tools unsupported", agent: externalAgent("codex"), fields: []string{"tool_policy_changes"}, code: InvalidInput},
		{name: "unknown field invalid", agent: config.AgentConfig{ID: "writer"}, fields: []string{"made_up"}, code: InvalidInput},
		{name: "retired heartbeat invalid", agent: config.AgentConfig{ID: "writer"}, fields: []string{"heartbeat_interval"}, code: InvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFields(tc.agent, tc.fields)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Code != tc.code || len(fe.Fields) != 1 || fe.Fields[0] != tc.fields[0] {
				t.Fatalf("error=%#v want code=%s field=%s", err, tc.code, tc.fields[0])
			}
		})
	}
}

func externalAgent(cli string) config.AgentConfig {
	return config.AgentConfig{ID: "external", Type: config.AgentTypeWorker, Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: cli}}}
}

func TestValidateFieldsReportsEveryOffendingFieldInRequestOrder(t *testing.T) {
	agent := config.AgentConfig{ID: "mia", Type: config.AgentTypeCore, Locked: true}
	err := ValidateFields(agent, []string{"model", "name", "skills", "soul", "icon"})
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error=%v want FieldError", err)
	}
	want := []string{"name", "soul", "icon"}
	if len(fe.Fields) != len(want) {
		t.Fatalf("fields=%v want=%v", fe.Fields, want)
	}
	for i := range want {
		if fe.Fields[i] != want[i] {
			t.Fatalf("fields=%v want=%v", fe.Fields, want)
		}
	}
}
