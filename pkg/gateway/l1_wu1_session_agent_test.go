package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B2 / FR-122 / BDD-13.3: real REST call, not a validator-only test.
// The valid explicit-owner control precedes each negative input in both modes.
func TestL1WU1SessionCreate_RequiresNamedAgentValidationOnAndOff(t *testing.T) {
	for _, validation := range []bool{false, true} {
		mode := "validation_off"
		if validation {
			mode = "validation_on"
		}
		t.Run(mode, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			api.agentLoop.GetConfig().Gateway.ValidateInbound = validation
			srv := httptest.NewServer(http.HandlerFunc(api.HandleSessions))
			defer srv.Close()
			call := func(body string) (int, []byte) {
				t.Helper()
				response, err := srv.Client().Post(srv.URL+"/api/v1/sessions", "application/json", strings.NewReader(body))
				require.NoError(t, err)
				defer response.Body.Close()
				raw, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				return response.StatusCode, raw
			}
			t.Run("explicit_agent_positive_control", func(t *testing.T) {
				status, raw := call(`{"agent_id":"test-agent"}`)
				require.Equal(t, http.StatusCreated, status, "ordinary extra chat stays available: %s", raw)
				var meta gen.Session
				require.NoError(t, json.Unmarshal(raw, &meta))
				assert.Equal(t, "test-agent", meta.AgentId)
				require.NotEmpty(t, meta.Id)
				stored, err := api.agentLoop.GetSessionStore().GetMeta(meta.Id)
				require.NoError(t, err)
				assert.Equal(t, "test-agent", stored.AgentID)
			})
			for _, row := range []struct {
				name, body string
				semantic   bool
			}{
				{"omitted", `{}`, true},
				{"empty", `{"agent_id":""}`, true},
				{"whitespace", `{"agent_id":"  "}`, true},
				{"null", `{"agent_id":null}`, true},
				{"wrong_type", `{"agent_id":42}`, false},
				{"extra_field", `{"agent_id":"test-agent","unexpected":true}`, false},
				{"malformed", `{"agent_id":`, false},
			} {
				t.Run(row.name, func(t *testing.T) {
					store := api.agentLoop.GetSessionStore()
					before, err := store.ListSessions()
					require.NoError(t, err)
					status, raw := call(row.body)
					assert.Equal(t, http.StatusBadRequest, status, "FR-122: missing/malformed named agent: %s", raw)
					if row.semantic && (!validation || row.name == "whitespace") {
						assert.Contains(t, string(raw), "an agent must be named", "§6.2 semantic refusal must survive disabled schema validation")
					}
					after, err := store.ListSessions()
					require.NoError(t, err)
					assert.Equal(t, before, after, "FR-122: no session may be minted under an invented agent")
				})
			}
		})
	}
}

func TestL1WU1OmnipusSessionCreate_RefusesSecondConversationWithMainID(t *testing.T) {
	// FR-005 / BDD-1.7. Explicit fixture tests the boundary before the new seed
	// exists; WU-2 separately tests the actual seed and REST identity.
	api := l1WU2OmnipusWriteAPI(t)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	main, err := store.GetOrCreateMainSession("default", "omnipus")
	require.NoError(t, err)
	before, err := store.ListSessions()
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"omnipus"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleSessions(w, r)
	assert.Equal(t, http.StatusConflict, w.Code, "FR-005: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "Omnipus has one conversation")
	assert.Contains(t, w.Body.String(), main.ID, "refusal must give the existing main, not a dead end")
	after, err := store.ListSessions()
	require.NoError(t, err)
	assert.Equal(t, before, after, "refused second conversation must not mint an extra chat")
	for _, meta := range after {
		if meta.AgentID == "omnipus" {
			assert.Equal(t, session.SessionTypeMain, meta.Type, "only Omnipus's one main remains")
		}
	}
}
