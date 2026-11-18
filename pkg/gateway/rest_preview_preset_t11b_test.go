package gateway

// Founder Q12 A / S6; FR-10 and the preview response contract: the displayed
// argv is the actual ordered command, including unfiltered person parameters.
// Expected suffix is derived from that order, not runner.Build*Args output.

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
	wantSuffix := []string{"--model", "agent-model", "--dangerously-bypass-approvals-and-sandbox", "--model", "person model", "-"}
	require.GreaterOrEqual(t, len(resp.Argv), len(wantSuffix))
	assert.Equal(t, wantSuffix, resp.Argv[len(resp.Argv)-len(wantSuffix):],
		"FR-10: filtered agent args, unfiltered person preset last, then the documented stdin marker")
	assert.Equal(t, 1, strings.Count(strings.Join(resp.Argv, "\x00"), "--dangerously-bypass-approvals-and-sandbox"),
		"the agent's bypass is dropped, but the person's saved bypass is displayed")
	require.Len(t, resp.DroppedArgs, 1)
	assert.Equal(t, "--dangerously-bypass-approvals-and-sandbox", resp.DroppedArgs[0].Flag)
	assert.NotEmpty(t, resp.DroppedArgs[0].Reason)
	assert.Equal(t, gen.ExecutorCommandPreviewResponsePromptDeliveryStdin, resp.PromptDelivery)
	assert.True(t, strings.HasSuffix(resp.CommandLine, " --model agent-model --dangerously-bypass-approvals-and-sandbox --model 'person model' -"),
		"S6: the copyable command must show the same quoted person-preset suffix: %s", resp.CommandLine)
}
