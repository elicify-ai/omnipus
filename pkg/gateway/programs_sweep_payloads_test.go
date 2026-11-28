package gateway

// S113: concrete program/channel/settings operations use generated request and
// response carriers, actual resource ids/revisions, and a real person session.
// This is payload coverage, not the separate source-to-writer reachability guard.
import (
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
)

func pgConcreteProgramsPayloads(t *testing.T) {
	t.Helper()
	f := pgBoundChannel(t)
	pgCommitProgramReference(t, f)
	canary := programstore.SeedCanary(t, f.store)
	call := func(method, path string, input any, want int, out any) {
		t.Helper()
		body := ""
		if input != nil {
			raw, err := json.Marshal(input)
			require.NoError(t, err)
			body = string(raw)
		}
		if method != http.MethodGet {
			before := programstore.Snapshot(t, f.store, "req-s113", canary)
			configBefore := pgProgramsSection(t, f)
			attack, read := pgServe(t, f, method, path, []byte(body), f.token, false, true)
			require.Equal(t, http.StatusForbidden, attack.Code, "same valid payload, bearer-only %s %s: %s", method, path, attack.Body.String())
			require.Equal(t, int64(0), read, "person-session gate precedes body decode")
			require.Equal(t, before, programstore.Snapshot(t, f.store, "req-s113", canary), "same-request full credential state unchanged")
			require.Equal(t, configBefore, pgProgramsSection(t, f), "same-request Programs metadata unchanged")
		}
		w := pgProgramsSessionCall(t, f, method, path, body)
		require.Equal(t, want, w.Code, "%s %s: %s", method, path, w.Body.String())
		if out != nil {
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), out), "generated response: %s %s", method, path)
		}
	}
	name := "Sweep"
	words := []string{"sweepw"}
	vars := []string{"SWEEP_TOKEN"}
	sites := []string{"api.sweep.invalid"}
	var entry gen.ProgramEntry
	call(http.MethodPost, "/api/v1/programs", gen.ProgramCustomRequest{DisplayName: &name, ProgramWords: &words, TokenVariables: &vars, NetworkSites: &sites}, http.StatusCreated, &entry)
	require.Equal(t, "custom", string(entry.Kind))
	require.Equal(t, words, entry.ProgramWords)
	require.NotEmpty(t, entry.Id)
	revision := entry.Revision
	renamed := "Sweep renamed"
	call(http.MethodPut, "/api/v1/programs/"+entry.Id, gen.ProgramCustomRequest{DisplayName: &renamed, Revision: &revision}, http.StatusOK, &entry)
	require.Equal(t, revision+1, entry.Revision)
	require.Equal(t, renamed, entry.DisplayName)
	path := "/api/v1/programs/" + entry.Id
	call(http.MethodPut, path+"/credential", gen.ProgramCredentialRequest{Value: "sweep_tok_12345"}, http.StatusOK, &entry)
	require.Equal(t, "saved_not_checked", string(entry.Connection.State))
	require.Equal(t, int64(1), entry.Connection.Generation)
	call(http.MethodPost, path+"/check", nil, http.StatusAccepted, &entry)
	require.Equal(t, int64(1), entry.Connection.Generation, "recheck does not publish a token")
	call(http.MethodDelete, path+"/credential", nil, http.StatusOK, &entry)
	require.Equal(t, "not_connected", string(entry.Connection.State))
	require.Equal(t, int64(2), entry.Connection.Generation)
	var list gen.ProgramList
	call(http.MethodGet, "/api/v1/programs", nil, http.StatusOK, &list)
	call(http.MethodPut, "/api/v1/programs/settings", gen.ProgramSettingsUpdate{Revision: list.Revision, WebChatAllowed: true}, http.StatusOK, &list)
	require.True(t, list.WebChatAllowed)
	var setting gen.ChannelProgramsSetting
	call(http.MethodGet, "/api/v1/channels/telegram/programs", nil, http.StatusOK, &setting)
	require.False(t, setting.Allowed)
	call(http.MethodPut, "/api/v1/channels/telegram/programs", gen.ChannelProgramsSetting{Allowed: true, Revision: setting.Revision}, http.StatusOK, &setting)
	require.True(t, setting.Allowed)
	call(http.MethodDelete, path, nil, http.StatusNoContent, nil)
	call(http.MethodGet, "/api/v1/programs", nil, http.StatusOK, &list)
	for _, e := range list.Entries {
		require.NotEqual(t, entry.Id, e.Id, "deleted entry absent from real list")
	}
}
