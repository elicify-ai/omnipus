// buildGodModeTestAPI: shared test harness for PUT /api/v1/agents/{id}
// god-mode-adjacent tests elsewhere in this package.
//
// Traces to: docs/internal/uat/remediation-decisions.md O14.

package gateway

import (
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// buildGodModeTestAPI builds a minimal restAPI wired to a single custom agent
// "test-agent" in a temp home dir. The caller controls allowGodMode.
//
// ADR-054: agents are per-entity records under entities/agents/<id>.json, not
// config.json's agents.list — updateAgent's persist step resolves and
// mutates the target exclusively via agentstore.Store.Update, which 404s
// with errAgentVanishedDuringUpdate ("agent no longer exists") when no
// matching entity file exists. "test-agent" is therefore seeded via BOTH the
// in-memory cfg.Agents.List literal (needed for mustAgentLoop's initial
// AgentRegistry construction and the pre-persist "does this agent exist /
// is it locked" checks, which read a.agentLoop.GetConfig()) AND a real
// agentstore entity record (needed for it to survive an actual write),
// mirroring the pattern proven in rest_test.go's seedAgentEntities and
// rest_agent_executor_test.go's buildExecutorTestAPI.
func buildGodModeTestAPI(t *testing.T, allowGodMode bool) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	// config.json on disk carries only agents.defaults — agents.list is
	// never read from disk by any production code path (ADR-054), so seeding
	// it here would only assert a stale, misleading shape.
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096}}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	testAgent := config.AgentConfig{ID: "test-agent", Name: "Test Agent"}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{testAgent},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// ADR-054: persist a matching entity record so updateAgent's
	// agentstore-backed persist step can find "test-agent" at all.
	testAgentForStore := testAgent
	require.NoError(t, agentstore.New(tmpDir).Create("test-agent", &testAgentForStore))

	return &restAPI{
		agentLoop:    al,
		homePath:     tmpDir,
		allowGodMode: allowGodMode,
	}
}
