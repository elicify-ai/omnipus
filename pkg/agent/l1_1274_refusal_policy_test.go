package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestL11274SchemaRefusalsNameTheRejectedInput(t *testing.T) {
	for _, class := range []string{"unsupported-cli", "missing-cli", "missing-native-soul"} {
		t.Run(class, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			args := l11274CreateArgs("Schema refusal", "claude-code")
			wantField, wantValue := "cli", "unsupported-cli"
			switch class {
			case "unsupported-cli":
				args["cli"] = wantValue
			case "missing-cli":
				delete(args, "cli")
				wantValue = "required"
			case "missing-native-soul":
				args["agent_type"] = "Main"
				delete(args, "soul")
				delete(args, "cli")
				wantField, wantValue = "soul", "required"
			}
			result := f.execute(t, "create_agent", args)
			if !result.IsError || !strings.Contains(result.ForLLM, wantField) || !strings.Contains(result.ForLLM, wantValue) {
				t.Errorf("%s refusal must identify %q and %q in ForLLM: IsError=%v, result=%q", class, wantField, wantValue, result.IsError, result.ForLLM)
			}
			l11274AssertAbsent(t, f, "schema-refusal")
		})
	}
}

// Oracle: omnipus-system-agent spec §6.5 + ADR OSA-3. "allow-to-ceiling"
// means NO create_agent override, not a hardcoded per-agent allow. The ask and
// deny controls make the real resolver prove it is actually following the ceiling.
func TestL11274OmnipusCreatePolicyRidesAndNeverOverridesCeiling(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ceiling    string
		agentDeny  bool
		wantPolicy string
	}{
		{"fresh-allow", "", false, "allow"},
		{"operator-ask", "ask", false, "ask"},
		{"operator-deny", "deny", false, "deny"},
		{"per-agent-deny-tightens", "allow", true, "deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newL11274Fixture(t, tc.ceiling, tc.agentDeny)
			omnipus, ok := f.al.GetRegistry().GetAgent("omnipus")
			if !ok || omnipus == nil {
				t.Fatal("BLOCKED: Omnipus not implemented — required by spec §6.5 / #1274")
			}
			tool, ok := omnipus.Tools.Get("create_agent")
			if !ok {
				t.Fatal("BLOCKED: create_agent not registered — required by spec §6.5 / #1274")
			}
			override, exists := omnipus.LoadToolPolicy().Policies["create_agent"]
			if tc.agentDeny {
				if !exists || override != config.ToolPolicyDeny {
					t.Errorf("explicit deny fixture did not reach the live map: exists=%v, value=%q", exists, override)
				}
			} else if exists {
				t.Errorf("allow-to-ceiling must have no Omnipus create_agent entry, got %q", override)
			}
			if got := tools.EffectiveToolPolicy(omnipus.LoadToolPolicy(), tool.Scope(), omnipus.AgentType, "create_agent"); got != tc.wantPolicy {
				t.Errorf("live effective create_agent policy = %q, want %q", got, tc.wantPolicy)
			}
			if got := f.al.ResolveApprovalToolPolicy("omnipus", "create_agent"); got != tc.wantPolicy {
				t.Errorf("real approval resolver policy = %q, want %q", got, tc.wantPolicy)
			}
		})
	}
}
