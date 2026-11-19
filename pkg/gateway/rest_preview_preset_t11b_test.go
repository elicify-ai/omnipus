package gateway

// Founder Q12 A / S6; FR-10 and the preview response contract: the displayed
// argv is the actual ordered command, including unfiltered person parameters.
// Q-OC1=A (2026-11-19) excepts preset model options when Omnipus emits its
// configured model; #1880 also removes worker model options unconditionally.
// Expected suffix is derived from those rules, not runner.Build*Args output.

import (
	"net/http"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostAgentsExecutorPreview_SavedBypassPresetAppearsLast_S6(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	t.Setenv(config.EnvHome, t.TempDir())
	program := t11bSavedProgram(t, api, "codex")
	const preset = `--dangerously-bypass-approvals-and-sandbox --model "person model"`
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		row := cfg.Sandbox.ThirdPartyAgents.CLIs["codex"]
		row.PresetArgs = preset
		cfg.Sandbox.ThirdPartyAgents.CLIs["codex"] = row
		return nil
	}))
	model := "driver-model"
	agentArgs := "--model agent-model --dangerously-bypass-approvals-and-sandbox"
	code, resp := postExecutorPreview(t, api, gen.ExecutorCommandPreviewRequest{
		Cli: gen.ExternalCliToolCodex, Model: &model, CliArgs: &agentArgs,
	})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, program, resp.Binary, "contract: display the path exactly as saved")
	assert.Equal(t, []string{model}, opencodeFix1880PreviewModels(t, resp.Argv), "Q-OC1=A: only Omnipus's configured model is passed")
	assert.NotContains(t, resp.Argv, "agent-model", "#1880: worker arguments cannot replace the configured model")
	assert.NotContains(t, resp.Argv, "person model", "Q-OC1=A: a conflicting preset model is removed too")
	wantSuffix := []string{"--dangerously-bypass-approvals-and-sandbox", "-"}
	require.GreaterOrEqual(t, len(resp.Argv), len(wantSuffix))
	assert.Equal(t, wantSuffix, resp.Argv[len(resp.Argv)-len(wantSuffix):],
		"FR-10/Q-OC1=A: the rest of the person preset remains unfiltered and last, before the stdin marker")
	assert.Equal(t, 1, strings.Count(strings.Join(resp.Argv, "\x00"), "--dangerously-bypass-approvals-and-sandbox"),
		"the agent's bypass is dropped, but the person's saved bypass is displayed")
	require.Len(t, resp.DroppedArgs, 3)
	dropped := make([]string, 0, len(resp.DroppedArgs))
	for _, drop := range resp.DroppedArgs {
		dropped = append(dropped, drop.Flag)
		assert.NotEmpty(t, drop.Reason)
	}
	assert.Equal(t, []string{"--model agent-model", "--dangerously-bypass-approvals-and-sandbox", "--model person model"}, dropped,
		"S6/Q-OC1=A: disclose worker model, worker bypass and conflicting preset model in input order")
	assert.Equal(t, gen.ExecutorCommandPreviewResponsePromptDeliveryStdin, resp.PromptDelivery)
	assert.True(t, strings.HasSuffix(resp.CommandLine, " --dangerously-bypass-approvals-and-sandbox -"),
		"S6: the copyable command must show the same surviving person-preset suffix: %s", resp.CommandLine)
	assert.NotContains(t, resp.CommandLine, "agent-model")
	assert.NotContains(t, resp.CommandLine, "person model")
}
