//go:build darwin || linux

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B10 / FR-049 / spec §3.2. The real turn and registered tool are exercised;
// only the model and the person's response are scripted. Names stay neutral.
func TestOfficeUAT_B10(t *testing.T) {
	for _, auto := range []bool{false, true} {
		mode := "auto-off"
		if auto {
			mode = "auto-on"
		}
		for _, row := range []string{"case-1", "case-2", "control"} {
			t.Run(mode+"/"+row, func(t *testing.T) {
				provider := testutil.NewScenario()
				al, instance, home := officeUATB10Loop(t, provider, auto)
				target := officepreview.OwnRoot(t)
				name := "LibreOffice"
				switch row {
				case "case-1":
					officepreview.TrustedOffice(t, home, target)
				case "case-2":
					name = "PDF renderer"
					officepreview.RegisteredRendererFixture(t, home, target, true)
				case "control":
					target = t.TempDir()
				}
				resolved, err := filepath.EvalSymlinks(target)
				require.NoError(t, err)
				args := map[string]any{"host_path": resolved, "reason": "Office UAT B10 fixture"}
				encoded, err := json.Marshal(args)
				require.NoError(t, err)
				provider.WithToolCall("ToolSearch", `{"names":["request_mount"]}`).
					WithToolCalls([]providers.ToolCall{autoToolCall("office-b10-call", "request_mount", string(encoded))}).
					WithText("fixture turn complete")
				approver := &autoRecordingApprover{approve: true}
				al.SetToolApprover(approver)

				answer, err := al.runAgentLoop(context.Background(), instance, processOptions{
					SessionKey: "office-b10-session", Channel: "webchat", ChatID: "office-reader",
					WorkspaceID: "office-uat", UserInitiated: true, UserMessage: "Use the supplied tool call.",
				})
				require.NoError(t, err, "instrument: routing and the scripted provider must complete")
				require.Equal(t, "fixture turn complete", answer)
				require.Equal(t, 3, provider.CallCount(), "discovery, the real call, then its result in the final provider request")
				result := toolResultText(t, provider, "office-b10-call")
				requests := approver.requests()
				mounts, _ := workspace.LoadMounts(home, "office-uat")
				if row == "control" {
					require.Len(t, requests, 1, "instrument: an ordinary request really reaches the person")
					assert.Equal(t, "request_mount", requests[0].ToolName)
					assert.Equal(t, args, requests[0].Args)
					require.Len(t, mounts, 1)
					assert.Equal(t, resolved, mounts[0].HostPath)
					assert.Contains(t, result, "Mounted")
					return
				}
				assert.Empty(t, requests, "B10: the request must settle without consulting the person")
				assert.Equal(t, officepreview.ProgramMountRefusal(name, resolved), result, "FR-049: the actual call returns the specified refusal")
				assert.Empty(t, mounts, "B10: no access is recorded")
			})
		}
	}
}

func officeUATB10Loop(t *testing.T, provider providers.LLMProvider, auto bool) (*AgentLoop, *AgentInstance, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.RestrictToWorkspace = true
	cfg.Sandbox.AutoApprove = auto
	cfg.Sandbox.ToolPolicies["request_mount"] = string(config.ToolPolicyAsk)
	roleTools := coreagent.NewCustomAgentToolsCfg()
	roleTools.Builtin.Policies["request_mount"] = config.ToolPolicyAsk
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Tools: roleTools}}
	record := filepath.Join(home, "workspaces", "office-uat.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(record), 0o700))
	require.NoError(t, os.WriteFile(record, []byte(`{"id":"office-uat","name":"Office UAT","status":"active","core_team":["mia"]}`), 0o600))
	_, err := workspace.EnsureWorkDir(home, "office-uat")
	require.NoError(t, err)
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	al, err := NewAgentLoop(cfg, mb, provider)
	require.NoError(t, err)
	t.Cleanup(al.Close)
	instance, found := al.GetRegistry().GetAgent("mia")
	require.True(t, found)
	tool, found := instance.Tools.Get("request_mount")
	require.True(t, found, "instrument: use the registered production tool, never a stub")
	require.IsType(t, &tools.RequestMountTool{}, tool)
	return al, instance, home
}
