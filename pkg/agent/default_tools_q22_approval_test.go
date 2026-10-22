package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC.md Q22=B: new native Main agents ask to create a skill, even though
// the ceiling must permit exempt Omnipus to create it without approval.
func TestRunTurn_Q22NewMainAsksToCreateSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxToolIterations = 10
	// Q22's input isolates the required per-agent Ask from the old global Ask.
	cfg.Sandbox.ToolPolicies["create_skill"] = "allow"
	cfg.Sandbox.AutoApprove = false
	main := config.AgentConfig{ID: "q22-new-main", Name: "Q22 Main", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
	// SPEC.md Q22=B: this must be constructor data, not a test-set Ask override.
	require.Equal(t, config.ToolPolicyAsk, main.Tools.Builtin.Policies["create_skill"])
	cfg.Agents.List = []config.AgentConfig{main}
	provider := testutil.NewScenario().
		WithToolCall("ToolSearch", `{"names":["create_skill"]}`).
		WithToolCall("create_skill", `{"name":"q22-test","content":"---\nname: q22-test\ndescription: test\n---\nbody"}`).
		WithText("done")
	loop := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	defer loop.Close()
	loop.WireSysagentDeps(&systools.Deps{})
	approver := &countingDenyApprover{reason: "user"}
	loop.SetToolApprover(approver)
	_, err := loop.ProcessDirectAs(context.Background(), main.ID, "create a skill", "q22-new-main-session")
	require.NoError(t, err)
	// SPEC.md Q22=B: unlike Omnipus, a new Main must consult the real approval gate.
	assert.Equal(t, 1, approver.callCount(), "native Main skill authoring must ask exactly once")
}
