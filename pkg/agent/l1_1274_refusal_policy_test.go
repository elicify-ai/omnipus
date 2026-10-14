package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Oracle: #1274 RED brief R3. An error on some OTHER field does not count as a
// path refusal: the exact supplied path and the path-specific reason must reach
// ForLLM. The current required-soul failure must therefore remain visible.
func TestL11274InvalidCLIPathsRefuseWithModelVisiblePathAndReason(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		for _, class := range []string{"nonexistent", "directory", "non-executable"} {
			t.Run(cli+"/"+class, func(t *testing.T) {
				f := newL11274Fixture(t, "", false)
				valid := l11274Executable(t, f.home, cli, true)
				path, reasons := filepath.Join(filepath.Dir(valid), "missing-"+cli), []string{"does not exist", "not found", "no such file"}
				switch class {
				case "directory":
					path, reasons = filepath.Dir(valid), []string{"directory", "not a file", "regular file"}
				case "non-executable":
					path, reasons = l11274Executable(t, f.home, cli, false), []string{"not executable", "permission denied", "cannot execute"}
				}
				before, err := workspace.ReadState(f.home, l11274WorkspaceID)
				if err != nil {
					t.Fatal(err)
				}
				configBefore, err := os.ReadFile(f.configPath)
				if err != nil {
					t.Fatal(err)
				}
				result := f.execute(t, "create_agent", l11274CreateArgs("Rejected worker", cli, path))
				message := l11274ModelRefusalReason(t, result)
				if !strings.Contains(message, "cli_path") || !l11274ReasonNamesPath(message, path) {
					t.Errorf("refusal must identify cli_path %q in the model-facing reason, got %q", path, message)
				}
				matched := false
				for _, reason := range reasons {
					matched = matched || strings.Contains(strings.ToLower(message), reason)
				}
				if !matched {
					t.Errorf("%s refusal must explain that specific path fault (%v), got %q", class, reasons, message)
				}
				l11274AssertAbsent(t, f, "rejected-worker")
				after, err := workspace.ReadState(f.home, l11274WorkspaceID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Errorf("refused create mutated workspace/team/delegation: before=%+v, after=%+v, err=%v", before, after, err)
				}
				configAfter, err := os.ReadFile(f.configPath)
				if err != nil || !bytes.Equal(configBefore, configAfter) {
					t.Errorf("refused create changed persisted config, err=%v", err)
				}
			})
		}
	}
}

func TestL11274SchemaRefusalsNameTheRejectedInput(t *testing.T) {
	for _, class := range []string{"unsupported-cli", "missing-cli", "missing-native-soul"} {
		t.Run(class, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			path := l11274Executable(t, f.home, "claude-code", true)
			args := l11274CreateArgs("Schema refusal", "claude-code", path)
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
				delete(args, "cli_path")
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
