// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// newCreateAutoApproveAPI is newShellModeAuditAPI (audit on, global
// Auto-approve on) with the seeded agent given a complete tool-policy map, so
// the create path's coverage guard (Hard Constraint #6) passes — a real
// install gets that from ReconcileToolPolicyCeiling at boot.
func newCreateAutoApproveAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	api, root := newShellModeAuditAPI(t)
	cfg := api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		cfg.Agents.List[i].Tools = coreagent.NewCustomAgentToolsCfg()
	}
	return api, root
}

// ADR-092 review finding C: AgentCreateRequestMain and ...Subagent both
// define auto_approve_disabled, but the create handler never read it — an
// agent created with "Never auto-approve" came out with Auto-approve on.
func TestAgentCreate_AutoApproveDisabledIsPersisted(t *testing.T) {
	for _, tc := range []struct {
		kind string
		body string
		want bool
	}{
		{"Main on", `{"name":"Careful Main","type":"Main","soul":"s","auto_approve_disabled":true}`, true},
		{"Subagent on", `{"name":"Careful Sub","type":"Subagent","description":"d","soul":"s","auto_approve_disabled":true}`, true},
		{"Main off", `{"name":"Plain Main","type":"Main","soul":"s","auto_approve_disabled":false}`, false},
		{"Main omitted", `{"name":"Default Main","type":"Main","soul":"s"}`, false},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			api, root := newCreateAutoApproveAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
			created := decodeAgentResp(t, w.Body.Bytes())

			require.NotNil(t, created.AutoApproveDisabled, "the create response must echo the switch")
			assert.Equal(t, tc.want, *created.AutoApproveDisabled)

			stored, err := agentstore.New(api.homePath).Get(created.Id)
			require.NoError(t, err)
			assert.Equal(t, tc.want, stored.AutoApproveDisabled, "the switch must be persisted on create")

			_, read := getAgent(t, api, created.Id)
			require.NotNil(t, read.AutoApproveDisabled)
			assert.Equal(t, tc.want, *read.AutoApproveDisabled, "GET must read back what create saved")

			// FR-032(a): creating an agent with Auto-approve forced off is a
			// mode change for that agent and is audited like the PUT path.
			events := shellModeChanges(t, root)
			if tc.want {
				require.Len(t, events, 1)
				assert.Equal(t, "agent", events[0].Details["level"])
				assert.Equal(t, "ask", events[0].Details["new_mode"])
				assert.Equal(t, created.Id, events[0].AgentID)
			} else {
				assert.Empty(t, events, "no switch set: nothing changed, nothing audited")
			}
		})
	}

	t.Run("subagent_3p has no such field", func(t *testing.T) {
		api, _ := newCreateAutoApproveAPI(t)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(
			`{"name":"Ext","type":"subagent_3p","description":"d","soul":"s","auto_approve_disabled":true,`+
				`"executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"}}`))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "an external CLI worker cannot carry the switch; body: %s", w.Body.String())
	})
}
