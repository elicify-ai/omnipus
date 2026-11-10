package agentmutation

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
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

// Agent.yaml editable_fields pins these six names and refusal texts. A
// locked flag alone is not a capability lock (ADR-090): the custom worker
// remains editable, while the built-in worker's executor is protected.
func TestT11ExecutorDescriptors_ExactPolicyMatrix(t *testing.T) {
	const nativeReason = "Only external-CLI workers have a runtime environment."
	const protectedReason = "PROTECTED_FIELD: fields executor: field is protected for this agent"
	if thirdpartycli.NativeRuntimeRefusalText != nativeReason {
		t.Fatalf("native refusal=%q, want contract text %q", thirdpartycli.NativeRuntimeRefusalText, nativeReason)
	}
	customLocked := externalAgent("codex")
	customLocked.Locked = true
	builtinLocked := customLocked
	builtinLocked.ID = "worker"
	cases := []struct {
		name     string
		agent    config.AgentConfig
		editable bool
		reason   string
	}{
		{"native", config.AgentConfig{ID: "native-worker", Type: config.AgentTypeWorker}, false, nativeReason},
		{"external", externalAgent("codex"), true, ""},
		{"locked_custom_external", customLocked, true, ""},
		{"locked_builtin_external", builtinLocked, false, protectedReason},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.editable {
				if err := ValidateFields(tc.agent, []string{"executor"}); err != nil {
					t.Fatalf("runtime edit unexpectedly refused: %v", err)
				}
			} else if tc.reason == protectedReason {
				err := ValidateFields(tc.agent, []string{"executor"})
				if err == nil || err.Error() != protectedReason {
					t.Fatalf("locked built-in executor error=%v, want %q", err, protectedReason)
				}
			}
			want := map[string]FieldDescriptor{
				"executor.cli":                {Name: "executor.cli", Reason: "Locked after create; create a new agent to switch CLIs."},
				"executor.effective_cli_path": {Name: "executor.effective_cli_path", Reason: "Set in Settings -> Third-party agents."},
				"executor.kind":               {Name: "executor.kind", Reason: "Derived from the agent type."},
			}
			for _, name := range []string{"executor.env_overrides", "executor.env_overrides_remove", "executor.cli_args"} {
				want[name] = FieldDescriptor{Name: name, Editable: tc.editable, Reason: tc.reason}
			}
			got := make(map[string]FieldDescriptor)
			for _, descriptor := range FieldDescriptors(tc.agent) {
				if !strings.HasPrefix(descriptor.Name, "executor") {
					continue
				}
				if _, duplicate := got[descriptor.Name]; duplicate {
					t.Fatalf("duplicate executor descriptor %q", descriptor.Name)
				}
				got[descriptor.Name] = descriptor
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("executor Name/Editable/Reason map=%#v, want exactly %#v", got, want)
			}
		})
	}
}

func externalAgent(cli string) config.AgentConfig {
	return config.AgentConfig{ID: "external", Type: config.AgentTypeWorker, Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: cli}}}
}

func TestValidateFieldsReportsEveryOffendingFieldInRequestOrder(t *testing.T) {
	agent := config.AgentConfig{ID: "mia", Type: config.AgentTypeCore, Locked: true}
	err := ValidateFields(agent, []string{"model", "name", "skills", "soul"})
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error=%v want FieldError", err)
	}
	want := []string{"name", "soul"}
	if len(fe.Fields) != len(want) {
		t.Fatalf("fields=%v want=%v", fe.Fields, want)
	}
	for i := range want {
		if fe.Fields[i] != want[i] {
			t.Fatalf("fields=%v want=%v", fe.Fields, want)
		}
	}
}

// auto_approve_disabled (the retired per-agent "Never auto-approve" switch)
// is no longer a known agent field: a write naming it is refused, with no
// compatibility path, for the operator and the agent mutation path alike.
func TestRetiredAutoApproveDisabledFieldIsUnknown(t *testing.T) {
	agent := config.AgentConfig{ID: "writer", Type: config.AgentTypeWorker}
	err := ValidateFields(agent, []string{"auto_approve_disabled"})
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Code != InvalidInput || len(fe.Fields) != 1 || fe.Fields[0] != "auto_approve_disabled" {
		t.Fatalf("error=%#v want INVALID_INPUT for auto_approve_disabled", err)
	}
	for _, d := range FieldDescriptors(agent) {
		if d.Name == "auto_approve_disabled" {
			t.Fatalf("descriptor %#v still lists the retired field", d)
		}
	}
}
