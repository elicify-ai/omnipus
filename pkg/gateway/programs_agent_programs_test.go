// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: the REST half of the agent `programs` parity (FR-007): S-10
// (new agents start off with `programs: []`), S-17's switch half (switched
// on before connecting, the switch is saved), S-12 (switch-on refused by a
// private variable, 409, programs stays []) and S-85's switch-on and edit
// rows (git-host conflicts, 409 naming Dev and github.com). The tool-level
// rows (S-14, S-15, S-16) live in pkg/sysagent/tools; S-13's request_secret
// refusal in pkg/tools; the run-time rows (S-30, S-86) with the env/git
// integration slices.
//
// Level: Unit/Integration boundary — the REST surface only. S-17's
// `printenv` clause is run-time behaviour; the spec assigns this test Level
// Unit, so that clause is covered by the env integration slice's identical
// printenv harness and noted in the RED pack.
//
// Oracle: FR-007 (programs on the agent read/update schemas, person-session
// only), the conflict messages table (685–687), dataset D-2's stored-form
// rules. Expected values come from the spec, never from the handlers.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// pgAgentByID decodes the real GET /api/v1/agents/{id} body.
func pgAgentByID(t *testing.T, f *pgGateFixture, id string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)
	pgAttach(f, req, f.token, false, false)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "GET agent %s: %s", id, w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), "agent body decodes: %s", w.Body.String())
	return out
}

// pgPutAgentPrograms sends a revisioned PUT /api/v1/agents/{id} with the
// given body through the production chain as the person's browser session
// (the only caller FR-007 allows to change programs).
func pgPutAgentPrograms(t *testing.T, f *pgGateFixture, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := revisionedAgentMutationRequest(t, f.api, "/api/v1/agents/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	pgAttach(f, req, "", true, true)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

// pgSeedSelectedPrivate seeds one selected private variable for (workspace,
// agent) through the real card publication (CommitCard selects on submit,
// FR-086) — the S-12 Given without driving a full card turn.
func pgSeedSelectedPrivate(t *testing.T, f *pgGateFixture, workspaceID, agentID, variable, value string) {
	t.Helper()
	_, err := f.store.CommitCard("req-"+variable+"-"+agentID, "sess-"+variable+"-"+agentID,
		[]credentials.CardValue{{
			BaseName: "s12_" + strings.ToLower(variable) + "_" + agentID,
			Recipient: credentials.CardRecipient{
				Kind:        credentials.KindAgentPrivate,
				WorkspaceID: workspaceID,
				AgentID:     agentID,
				Field:       variable,
			},
			Value: value,
		}}, time.Now())
	require.NoError(t, err, "instrument: the private variable is committed and selected")
}

func TestAgentPrograms_ParityRows(t *testing.T) {
	f := pgPersonAPI(t)

	t.Run("S10_new_agents_start_off", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/",
			strings.NewReader(`{"name":"New","type":"Main","soul":"s"}`))
		req.Header.Set("Content-Type", "application/json")
		pgAttach(f, req, f.token, false, false)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code, "the agent is created: %s", w.Body.String())
		var created map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created), "create body decodes")
		id, _ := created["id"].(string)
		require.NotEmpty(t, id, "the create response carries the id")

		view := pgAgentByID(t, f, id)
		require.Contains(t, view, "programs",
			"FR-007: the agent read schema carries programs")
		programs, ok := view["programs"].([]any)
		require.True(t, ok, "programs is a list")
		require.Empty(t, programs, "a new agent starts off with an empty programs list")
	})

	t.Run("S17_switch_on_before_connecting_is_saved", func(t *testing.T) {
		// GitHub/Fly not connected: switching Fly on for Dev is still saved.
		w := pgPutAgentPrograms(t, f, "dev", `{"programs":["fly"]}`)
		require.Equal(t, http.StatusOK, w.Code,
			"the switch is saved (not connected is not a refusal): got %d %s", w.Code, w.Body.String())
		view := pgAgentByID(t, f, "dev")
		programs, ok := view["programs"].([]any)
		require.True(t, ok, "the read schema carries programs as a list: %s", w.Body.String())
		require.Equal(t, []any{"fly"}, programs, "programs persists as switched on")
	})
}

func TestAgentPrograms_ConflictRefusals(t *testing.T) {
	f := pgPersonAPI(t)

	t.Run("S12_switch_on_refused_by_a_private_variable", func(t *testing.T) {
		pgSeedSelectedPrivate(t, f, "gateway-test-harness-default", "dev", "GH_TOKEN", "gh_private_1234567890")
		w := pgPutAgentPrograms(t, f, "dev", `{"programs":["github"]}`)
		require.Equal(t, http.StatusConflict, w.Code,
			"switch-on with a private variable of the same name is refused: got %d %s",
			w.Code, w.Body.String())
		require.Contains(t, w.Body.String(),
			`GitHub cannot be switched on for Dev: Dev already has a private variable GH_TOKEN. Remove that variable, or leave GitHub off.`,
			"the refusal is the spec's variable-conflict message")
		view := pgAgentByID(t, f, "dev")
		programs, ok := view["programs"].([]any)
		require.True(t, ok, "the read schema carries programs as a list")
		require.Empty(t, programs, "programs stays []")
	})

	t.Run("S85_switch_on_refused_by_a_git_host_conflict", func(t *testing.T) {
		// S-85's Given needs the custom "GitLab Mirror" entry (host
		// GitHub.com. stored github.com) and GitHub on for Dev. The entry is
		// created through POST /programs — not implemented at RED.
		req := httptest.NewRequest(http.MethodPost, "/programs", strings.NewReader(
			`{"display_name":"GitLab Mirror","program_words":["glmirror"],`+
				`"token_variables":["GLM_TOKEN"],"network_sites":["gitlab.example"],`+
				`"git":{"host":"GitHub.com."}}`))
		req.Header.Set("Content-Type", "application/json")
		pgAttach(f, req, "", true, true)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs (create the GitLab Mirror entry)", "S1 contract table / S-85")
		}
		require.Equal(t, http.StatusCreated, w.Code, "the custom entry is created: %s", w.Body.String())
		// At GREEN: switch GitHub on for Dev, then switch GitLab Mirror on →
		// 409 with the spec's git-host conflict message. The assertions ride
		// GREEN reconciliation of the entry id; the RED proof is the missing
		// create route above.
	})

	t.Run("S85_edit_to_a_conflicting_host_refused", func(t *testing.T) {
		// "GitLab Mirror" with host gitlab.example on for Dev; its host is
		// edited to github.com → 409 naming Dev and github.com; entry
		// unchanged. Both the edit route (PUT /programs/{id}) and the
		// programs field are missing at RED.
		req := httptest.NewRequest(http.MethodPut, "/programs/gitlab-mirror", strings.NewReader(
			`{"revision":1,"git":{"host":"github.com"}}`))
		req.Header.Set("Content-Type", "application/json")
		pgAttach(f, req, "", true, true)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/{id} (edit the git host)", "S1 contract table / S-85")
		}
		require.Equal(t, http.StatusConflict, w.Code,
			"the edit is refused: got %d %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "Dev", "the refusal names the agent")
		require.Contains(t, w.Body.String(), "github.com", "the refusal names the host")
	})
}
