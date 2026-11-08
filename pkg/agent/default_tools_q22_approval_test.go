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

// Skills r15 §10: Main agents keep their own edit Ask even when an operator
// explicitly allows the global ceiling. The real lazy discovery/gate is exercised.
func TestRunTurn_R15NewMainAsksToEditSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxToolIterations = 10
	// The explicit operator Allow isolates the required per-agent Ask (§10).
	cfg.Sandbox.ToolPolicies["edit_skill"] = "allow"
	cfg.Sandbox.AutoApprove = false
	main := config.AgentConfig{ID: "q22-new-main", Name: "Q22 Main", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
	// §10: this must be constructor data, not a test-set Ask override.
	require.Equal(t, config.ToolPolicyAsk, main.Tools.Builtin.Policies["edit_skill"])
	cfg.Agents.List = []config.AgentConfig{main}
	provider := testutil.NewScenario().
		WithToolCall("ToolSearch", `{"names":["edit_skill"]}`).
		WithToolCall("edit_skill", `{"action":"delete","skill":"q22-test"}`).
		WithText("done")
	loop := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	defer loop.Close()
	loop.WireSysagentDeps(&systools.Deps{})
	approver := &countingDenyApprover{reason: "user"}
	loop.SetToolApprover(approver)
	_, err := loop.ProcessDirectAs(context.Background(), main.ID, "delete a skill", "q22-new-main-session")
	require.NoError(t, err)
	// A Main must consult the real gate despite the operator's global Allow.
	assert.Equal(t, 1, approver.callCount(), "native Main skill authoring must ask exactly once")
}
