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
// only), FR-042 (no two entries on for one agent share a canonical git host,
// at switch-on AND on edit), the conflict messages table (685–687) and
// dataset D-3 row 22 (host `GitHub.com.` stored `github.com`). Expected
// values come from the spec, never from the handlers. Every conflict row
// builds its own fixture, so no Given leaks between rows (CHECK F15).

package gateway

import (
	"encoding/json"
	"fmt"
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

// pgGithubPresetPresent asserts the S-85 Given's first leg: the github preset
// entry exists in the real list (presets ship under pkg/programs/presets).
// The read goes through the person's BROWSER SESSION (CHECK R14: the earlier
// helper sent the person's bearer token to this person-session-only route — a
// correct gate answers 403, so the Given could never read the list). At RED
// the list route does not exist and the row fatal-BLOCKs naming it.
func pgGithubPresetPresent(t *testing.T, f *pgGateFixture) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", nil)
	pgAttach(f, req, "", true, true)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		routeMissing(t, w, "GET /programs (the github preset entry of the S-85 Given)", "S-85's Given / preset catalog")
	}
	require.Equal(t, http.StatusOK, w.Code,
		"the program list reads under the person session (a bearer token gets 403): %s", w.Body.String())
	var list map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list), "the list body decodes: %s", w.Body.String())
	entries, _ := list["entries"].([]any)
	var ids []string
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	require.Contains(t, ids, "github", "the github preset entry exists (the conflict is between two real entries, FR-042)")
}

// pgCreateProgramEntry creates one custom entry through the production chain
// (POST /programs, person browser session) and returns the created body. At
// RED the create route does not exist and the row fatal-BLOCKs naming it.
func pgCreateProgramEntry(t *testing.T, f *pgGateFixture, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/programs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	pgAttach(f, req, "", true, true)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		routeMissing(t, w, "POST /programs (create the custom entry)", "S1 contract table / S-85 / S-34")
	}
	require.Equal(t, http.StatusCreated, w.Code, "the custom entry is created: %s", w.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created), "the create body decodes: %s", w.Body.String())
	id, _ := created["id"].(string)
	require.NotEmpty(t, id, "the created entry carries its id (the switch and the edit address it)")
	return created
}

// pgSwitchProgramOn switches one program on for Dev through the person
// browser session (FR-007's only allowed caller), preserving the programs
// already on: the PUT carries the UNION of Dev's current list and the new id
// (CHECK R14: the earlier singleton list REPLACED the agent's programs, so
// the second switch-on silently turned the first entry off and the S-85 edit
// Given never contained both entries). A refused switch still proposes the
// union — the refusal is the server's, never a wrong request body.
func pgSwitchProgramOn(t *testing.T, f *pgGateFixture, programID string) *httptest.ResponseRecorder {
	t.Helper()
	current := pgDevPrograms(t, f)
	seen := map[string]bool{}
	parts := make([]string, 0, len(current)+1)
	for _, p := range current {
		s, ok := p.(string)
		require.True(t, ok, "programs entries are strings: %v", p)
		if !seen[s] {
			seen[s] = true
			parts = append(parts, fmt.Sprintf("%q", s))
		}
	}
	if !seen[programID] {
		parts = append(parts, fmt.Sprintf("%q", programID))
	}
	return pgPutAgentPrograms(t, f, "dev", `{"programs":[`+strings.Join(parts, ",")+`]}`)
}

// pgDevPrograms reads Dev's programs list through the real read schema.
func pgDevPrograms(t *testing.T, f *pgGateFixture) []any {
	t.Helper()
	view := pgAgentByID(t, f, "dev")
	programs, ok := view["programs"].([]any)
	require.True(t, ok, "the read schema carries programs as a list: %v", view["programs"])
	return programs
}

// pgPutProgramEntry edits one custom entry through the production chain
// (PUT /programs/{id}, person browser session). The body carries the
// revision the caller read, so a stale-revision refusal can never stand in
// for the conflict under test.
func pgPutProgramEntry(t *testing.T, f *pgGateFixture, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/programs/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	pgAttach(f, req, "", true, true)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
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
	// Each conflict row builds its own gateway fixture: a conflict Given (a
	// seeded private variable, a created entry, a switched-on program) must
	// never leak into another row's state (CHECK F15).

	t.Run("S12_switch_on_refused_by_a_private_variable", func(t *testing.T) {
		f := pgPersonAPI(t)
		pgSeedSelectedPrivate(t, f, "gateway-test-harness-default", "dev", "GH_TOKEN", "gh_private_1234567890")
		w := pgPutAgentPrograms(t, f, "dev", `{"programs":["github"]}`)
		require.Equal(t, http.StatusConflict, w.Code,
			"switch-on with a private variable of the same name is refused: got %d %s",
			w.Code, w.Body.String())
		require.Contains(t, w.Body.String(),
			`GitHub cannot be switched on for Dev: Dev already has a private variable GH_TOKEN. Remove that variable, or leave GitHub off.`,
			"the refusal is the spec's variable-conflict message")
		require.Equal(t, []any{}, pgDevPrograms(t, f), "programs stays []")
	})

	t.Run("S85_switch_on_refused_by_a_git_host_conflict", func(t *testing.T) {
		f := pgPersonAPI(t)
		// S-85's Given, built on the real surfaces: the github preset entry
		// exists, the custom "GitLab Mirror" is created (display host
		// `GitHub.com.` — D-3 row 22 stores it as `github.com`), and GitHub
		// is switched on for Dev. FR-042: the switch-on that would give two
		// entries on for one agent the same canonical git host is refused.
		pgGithubPresetPresent(t, f)
		created := pgCreateProgramEntry(t, f,
			`{"display_name":"GitLab Mirror","program_words":["glmirror"],`+
				`"token_variables":["GLM_TOKEN"],"network_sites":["gitlab.example"],`+
				`"git":{"host":"GitHub.com."}}`)
		mirrorID, _ := created["id"].(string)
		stored := pgProgramEntryByID(t, f, mirrorID)
		git, ok := stored["git"].(map[string]any)
		require.True(t, ok, "the stored entry carries its git config: %s", pgEntryJSON(stored))
		require.Equal(t, "github.com", git["host"],
			"D-3 row 22: the display host `GitHub.com.` is stored as `github.com` (the conflict is on the canonical host)")

		w := pgSwitchProgramOn(t, f, "github")
		require.Equal(t, http.StatusOK, w.Code,
			"the Given: GitHub switches on for Dev (persistence itself is S-17's row): got %d %s", w.Code, w.Body.String())
		require.Equal(t, []any{"github"}, pgDevPrograms(t, f), "the Given is real: GitHub on for Dev")

		// When: "GitLab Mirror" is switched on for Dev.
		w = pgSwitchProgramOn(t, f, mirrorID)
		require.Equal(t, http.StatusConflict, w.Code,
			"the second switch-on is refused: got %d %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(),
			`GitLab Mirror cannot be switched on for Dev: GitHub already supplies the git login for github.com to Dev. Switch one of them off.`,
			"the refusal is the spec's git-host conflict message (spec message table)")

		// Then: the refused switch leaves the agent's programs unchanged.
		require.Equal(t, []any{"github"}, pgDevPrograms(t, f),
			"the refusal leaves Dev's programs unchanged — GitLab Mirror is not added")
	})

	t.Run("S85_edit_to_a_conflicting_host_refused", func(t *testing.T) {
		f := pgPersonAPI(t)
		// S-85's edit clause, with FR-042's rule made explicit in the Given:
		// GitHub (github.com) on for Dev AND the custom entry with the
		// non-conflicting host `gitlab.example` also on for Dev — two entries
		// on for one agent, no conflict yet. Editing the entry's host to
		// `github.com` would give the two entries one canonical host, so the
		// edit is refused; the entry is unchanged.
		pgGithubPresetPresent(t, f)
		created := pgCreateProgramEntry(t, f,
			`{"display_name":"GitLab Mirror","program_words":["glmirror"],`+
				`"token_variables":["GLM_TOKEN"],"network_sites":["gitlab.example"],`+
				`"git":{"host":"gitlab.example"}}`)
		mirrorID, _ := created["id"].(string)

		w := pgSwitchProgramOn(t, f, "github")
		require.Equal(t, http.StatusOK, w.Code, "the Given: GitHub on for Dev: got %d %s", w.Code, w.Body.String())
		w = pgSwitchProgramOn(t, f, mirrorID)
		require.Equal(t, http.StatusOK, w.Code,
			"the Given: the entry with the non-conflicting host is on for Dev: got %d %s", w.Code, w.Body.String())
		require.Equal(t, []any{"github", mirrorID}, pgDevPrograms(t, f), "the Given is real: both entries on for Dev")

		// When: the host is edited to `github.com`, carrying the revision the
		// caller just read (a stale-revision refusal can never stand in for
		// the conflict under test).
		before := pgProgramEntryByID(t, f, mirrorID)
		rev, ok := before["revision"].(float64)
		require.True(t, ok, "the entry carries its revision: %s", pgEntryJSON(before))
		w = pgPutProgramEntry(t, f, mirrorID, fmt.Sprintf(`{"revision":%v,"git":{"host":"github.com"}}`, rev))
		require.Equal(t, http.StatusConflict, w.Code,
			"the edit is refused: got %d %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "Dev", "the refusal names the agent")
		require.Contains(t, w.Body.String(), "github.com", "the refusal names the host")

		// Then: the entry is unchanged — same JSON as before the refused edit
		// (host still gitlab.example, revision not bumped).
		after := pgProgramEntryByID(t, f, mirrorID)
		require.Equal(t, pgEntryJSON(before), pgEntryJSON(after),
			"the refused edit leaves the entry unchanged")
	})
}
