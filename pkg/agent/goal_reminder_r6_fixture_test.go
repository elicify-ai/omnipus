package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/require"
)

// R6's additional real actors are configuration inputs, not fake execution
// state. R1/R2/Stop callers retain the identical default newGRHarness path.
func grNewR6Harness(t *testing.T) *grHarness {
	t.Helper()
	h := newGRHarness(t, grHarnessOptions{
		extraAgents:   []config.AgentConfig{{ID: string(coreagent.IDJudge), Type: config.AgentTypeSystem, Home: t.TempDir()}},
		extraPolicies: map[string]string{"execute_plan": "allow", "AskUserQuestion": "allow", "bash": "allow"},
	})
	judge, ok := h.al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	require.True(t, ok)
	judge.Provider = unmetJudgeProvider("The claim is not proof of productive goal work.")
	return h
}
