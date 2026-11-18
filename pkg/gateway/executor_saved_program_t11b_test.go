package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// t11bSaveProgram arranges the live person-saved Settings row, not a request
// cli_path or a PATH fallback. Real Settings persistence is exercised separately
// by the person-route tests. Keeping this explicit prevents a global positive
// fixture from concealing the no-saved-path refusal rows (T11 §4 / FR-9).
func t11bSaveProgram(t *testing.T, api *restAPI, cli, path string) {
	t.Helper()
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		if cfg.Sandbox.ThirdPartyAgents == nil {
			cfg.Sandbox.ThirdPartyAgents = &config.ThirdPartyAgentsConfig{}
		}
		if cfg.Sandbox.ThirdPartyAgents.CLIs == nil {
			cfg.Sandbox.ThirdPartyAgents.CLIs = map[string]config.ThirdPartyCLIConfig{}
		}
		row := cfg.Sandbox.ThirdPartyAgents.CLIs[cli]
		row.Path = path
		cfg.Sandbox.ThirdPartyAgents.CLIs[cli] = row
		return nil
	}))
}

func t11bSavedProgram(t *testing.T, api *restAPI, cli string) string {
	t.Helper()
	program := t11bWriteExe(t, cli+" saved program")
	t11bSaveProgram(t, api, cli, program)
	return program
}
