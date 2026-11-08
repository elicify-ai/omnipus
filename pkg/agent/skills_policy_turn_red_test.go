package agent

// r15 §10.1 / S-3.7, S-3.11–13, T-P1/P2; CHECK-12 reconciliation.
// Plan: real loop + real write_file + real policy + a persisted enclosing
// mount. Only the model and human approval decision are process-edge fakes.
// Cases: edit ask => one generic request before publication; edit deny => no
// request/no write; both allow => commit/no request; workspace write => the
// existing no-card behavior (REGRESSION, never forced RED). A separate tools
// test exercises PathHandle.WriteFile without the loop's composition marker.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillPolicy_RealLoopComposesBeforeGenericApproval(t *testing.T) {
	for _, tc := range []struct {
		name    string
		policy  config.ToolPolicy
		outside bool
		asks    int
		commits bool
	}{
		{"skill-ask-auto-on", config.ToolPolicyAsk, false, 1, true},
		{"skill-deny", config.ToolPolicyDeny, false, 0, false},
		{"both-allow", config.ToolPolicyAllow, false, 0, true},
		{"ordinary-workspace-write-regression", config.ToolPolicyAsk, true, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			home := filepath.Join(parent, "omnipus")
			t.Setenv(config.EnvHome, home)
			t.Setenv(config.EnvBuiltinSkills, t.TempDir())
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
			cfg.Agents.Defaults.MaxToolIterations = 10
			cfg.Sandbox.AutoApprove = true
			for name, policy := range map[string]string{"write_file": "allow", "read_file": "allow", "ToolSearch": "allow", "edit_skill": string(tc.policy)} {
				cfg.Sandbox.ToolPolicies[name] = policy
			}
			cfg.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia", Home: filepath.Join(home, "agents", "mia"), Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"write_file": config.ToolPolicyAllow, "read_file": config.ToolPolicyAllow, "edit_skill": tc.policy}}}}}
			require.NoError(t, agentstore.New(home).Create("mia", &cfg.Agents.List[0]))
			require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
			provider := testutil.NewScenario()
			msgBus := bus.NewMessageBus()
			t.Cleanup(msgBus.Close)
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			t.Cleanup(al.Close)
			wsID, ok := workspace.FindForAgent(home, "mia")
			require.True(t, ok, "real-turn membership positive control")
			_, _, err := workspace.CreateMount(home, wsID, "enclosing", parent)
			require.NoError(t, err)
			require.Contains(t, workspace.AllowedMountRoots(home, wsID), parent, "real persisted mount encloses Omnipus home")
			target := filepath.Join(home, "skills", "fresh-skill", "SKILL.md")
			content := "---\nname: fresh-skill\ndescription: policy fixture\n---\nAPPROVED CONTENT\n"
			if tc.outside {
				var work string
				work, err = workspace.SafeWorkDir(home, wsID)
				require.NoError(t, err)
				target = filepath.Join(work, "ordinary.txt")
				content = "ordinary workspace content\n"
			}
			args := map[string]any{"path": target, "content": content}
			raw, err := json.Marshal(args)
			require.NoError(t, err)
			provider.WithToolCall("write_file", string(raw)).WithText("done")
			approver := &autoRecordingApprover{approve: true}
			approver.onRequest = func(req PolicyApprovalReq) {
				_, statErr := os.Stat(target)
				assert.True(t, errors.Is(statErr, os.ErrNotExist), "approval is requested BEFORE publication")
				assert.Equal(t, "write_file", req.ToolName)
				assert.Equal(t, args, req.Args, "generic original argument object, no skill-specific approval fields/text")
				assert.Equal(t, "mia", req.AgentID)
			}
			al.SetToolApprover(approver)
			final, err := al.ProcessDirectAs(context.Background(), "mia", "perform the requested write", "skill-policy-"+tc.name)
			require.NoError(t, err)
			assert.Equal(t, "done", final, "provider reaches the response after the real tool dispatch")
			assert.Len(t, approver.requests(), tc.asks, "composed policy must decide before Auto/approval")
			result := toolResultText(t, provider, "write_file-0")
			if tc.commits {
				body, err := os.ReadFile(target)
				require.NoError(t, err, "approved/allowed real file tool must publish: %s", result)
				assert.Equal(t, content, string(body))
				if !tc.outside {
					assert.Contains(t, result, "saved as skill version ", "real skill write reaches the version pipeline")
				}
			} else {
				_, err := os.Stat(target)
				assert.True(t, errors.Is(err, os.ErrNotExist), "edit_skill deny cannot be bypassed by write_file allow")
				assert.Contains(t, result, "permission_denied")
			}
		})
	}
}
