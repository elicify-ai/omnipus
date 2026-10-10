package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Same-path sweep regressions: the null guard and decoder must recognise the
// same key, including Unicode simple folds (long s and Kelvin sign), not just
// an ASCII ToLower approximation. These existing fields are not wire additions.
func TestAgentIdentity_UpdateFoldedMemberNullIsZeroWrite(t *testing.T) {
	for _, tc := range []struct{ key, field string }{
		{"SKILLS", "skills"}, {"ſkills", "skills"}, {"sKills", "skills"},
		{"MCP_SERVERS", "mcp_servers"}, {"TOOL_POLICY_CHANGES", "tool_policy_changes"},
		{"SOUL", "soul"}, {"ſoul", "soul"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			before := savedAgent(t, api, "test-agent")
			store := agentstore.New(api.homePath)
			stateBefore, err := store.ReadState("test-agent")
			require.NoError(t, err)
			body := fmt.Sprintf(`{"revision":%q,%q:null,"color":"#22D3EE","description":"must not save"}`, stateBefore.Revision, tc.key)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
			rec := httptest.NewRecorder()
			api.HandleAgents(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assert.Equal(t, map[string]any{"error": tc.field + " must not be null"}, decodeObject(t, rec.Body.Bytes()))
			assert.Equal(t, before, savedAgent(t, api, "test-agent"))
			stateAfter, err := store.ReadState("test-agent")
			require.NoError(t, err)
			assert.Equal(t, stateBefore, stateAfter)
		})
	}
}

func TestAgentIdentity_UpdateFoldedClearIsNotOmission(t *testing.T) {
	for _, field := range []string{"context_window_override", "max_tool_iterations"} {
		t.Run(field, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			window := 4096
			store := agentstore.New(api.homePath)
			_, err := store.Update("test-agent", func(record *config.AgentConfig) error {
				record.ContextWindowOverride = &window
				record.MaxToolIterations = 30
				return nil
			})
			require.NoError(t, err)
			api.agentLoop.GetConfig().Agents.List[0].ContextWindowOverride = &window
			api.agentLoop.GetConfig().Agents.List[0].MaxToolIterations = 30
			stateBefore, err := store.ReadState("test-agent")
			require.NoError(t, err)
			body := fmt.Sprintf(`{"revision":%q,%q:null}`, stateBefore.Revision, strings.ToUpper(field))
			req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
			rec := httptest.NewRecorder()
			api.HandleAgents(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
			record, err := store.Get("test-agent")
			require.NoError(t, err)
			if field == "context_window_override" {
				assert.Nil(t, record.ContextWindowOverride, "decoder-recognised explicit null clears, not silently retains")
				assert.Equal(t, 30, record.MaxToolIterations, "omitted other override stays")
			} else {
				assert.Zero(t, record.MaxToolIterations, "decoder-recognised explicit null clears, not silently retains")
				require.NotNil(t, record.ContextWindowOverride, "omitted other override stays")
				assert.Equal(t, window, *record.ContextWindowOverride)
			}
		})
	}
}

func TestAgentIdentity_UpdateKeyLookalikesCannotHideNull(t *testing.T) {
	for _, key := range []string{"FIGURE ", "ＦＩＧＵＲＥ", "FIGURЕ"} { // Last E is Cyrillic, not Latin.
		t.Run(key, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			store := agentstore.New(api.homePath)
			stateBefore, err := store.ReadState("test-agent")
			require.NoError(t, err)
			before := savedAgent(t, api, "test-agent")
			body := fmt.Sprintf(`{"revision":%q,"FIGURE":null,%q:"Woman","color":"#22D3EE"}`, stateBefore.Revision, key)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
			rec := httptest.NewRecorder()
			api.HandleAgents(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, before, savedAgent(t, api, "test-agent"))
			stateAfter, err := store.ReadState("test-agent")
			require.NoError(t, err)
			assert.Equal(t, stateBefore, stateAfter, "a whitespace/homoglyph key is not a valid sibling or override")
		})
	}
}
