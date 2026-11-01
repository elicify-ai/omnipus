package gateway

// Oracle: r17 §7.5 AG-04 and scenarios 12.1–12.2. Real REST handler
// and protected store. No native-agent SOUL requirement is invented for an
// external worker, and no forbidden SOUL is added to make the fixture pass.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
)

func TestRestExternalCreate_SoulContract(t *testing.T) {
	for _, withSoul := range []bool{false, true} {
		t.Run(map[bool]string{false: "12.2 no SOUL accepted", true: "12.1 SOUL refused"}[withSoul], func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			pattern := filepath.Join(api.homePath, "entities", "agents", "*.json")
			before, err := filepath.Glob(pattern)
			require.NoError(t, err)
			body := map[string]any{"type": "subagent_3p", "name": "T8 external worker", "description": "external create parity", "executor": map[string]any{"cli": "codex"}}
			if withSoul {
				body["soul"] = "SECRET-PERSONA"
			}
			data, err := json.Marshal(body)
			require.NoError(t, err)
			w := httptest.NewRecorder()
			api.HandleAgents(w, httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(data)))
			if withSoul {
				require.Equal(t, http.StatusBadRequest, w.Code, "12.1: %s", w.Body.String())
				assert.Contains(t, w.Body.String(), "soul")
				after, err := filepath.Glob(pattern)
				require.NoError(t, err)
				assert.Equal(t, before, after, "a refused create leaves the protected agent inventory unchanged")
				return
			}
			require.Equal(t, http.StatusCreated, w.Code, "12.2 external worker must not require SOUL: %s", w.Body.String())
			// 12.2 requires creation without SOUL input. K-8's absent-soul
			// rule belongs to get_agent/AgentDetail, not the REST Agent echo.
			// Verify empty instruction content, not a different result shape.
			created := decodeAgentResp(t, w.Body.Bytes())
			assert.Equal(t, "subagent_3p", string(created.Type))
			assert.Equal(t, "T8 external worker", created.Name)
			assert.Empty(t, created.Soul, "no SOUL instruction content is invented for the external worker")
			state, err := agentstore.New(api.homePath).ReadState(created.Id)
			require.NoError(t, err, "201 must identify a durably created protected agent")
			require.NotNil(t, state.Agent)
			assert.Equal(t, "subagent_3p", string(state.Agent.Type))
			assert.Equal(t, "T8 external worker", state.Agent.Name)
			assert.Empty(t, state.Soul, "the real stored worker has no SOUL instruction content")
			require.NotNil(t, state.Agent.Subagents)
			require.NotNil(t, state.Agent.Subagents.Executor)
			assert.Equal(t, "codex", state.Agent.Subagents.Executor.CLI)
			assert.Empty(t, state.Agent.Subagents.Executor.CLIPath, "the program remains Settings-owned")
		})
	}
}
