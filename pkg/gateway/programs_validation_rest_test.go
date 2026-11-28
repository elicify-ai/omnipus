package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
)

// D-2 pins HTTP classes, but not a Go error type or exact message for ordinary
// field errors. Each payload changes only its named field in a valid request.
func TestCustomProgram_RESTValidationClasses(t *testing.T) {
	words := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return out
	}
	sites := make([]string, 33)
	for i := range sites {
		sites[i] = fmt.Sprintf("host%d.example", i)
	}
	for _, row := range []struct {
		name, field string
		value       any
		status      int
	}{
		{"display_empty", "display_name", "", 400},
		{"display_65", "display_name", strings.Repeat("a", 65), 400},
		{"duplicate_names_GitHub", "display_name", "github", 409},
		{"words_empty", "program_words", []string{}, 400},
		{"words_9", "program_words", words("word", 9), 400},
		{"word_path", "program_words", []string{"/usr/bin/acme"}, 400},
		{"word_shell", "program_words", []string{"bash"}, 400},
		{"variables_empty", "token_variables", []string{}, 400},
		{"variables_5", "token_variables", words("TOKEN_", 5), 400},
		{"variable_lower", "token_variables", []string{"acme_token"}, 400},
		{"variable_protected", "token_variables", []string{"PATH"}, 400},
		{"variable_reserved", "token_variables", []string{"OMNIPUS_ACME"}, 400},
		{"git_count_reserved", "token_variables", []string{"GIT_CONFIG_COUNT"}, 400},
		{"git_key_reserved", "token_variables", []string{"GIT_CONFIG_KEY_0"}, 400},
		{"git_value_reserved", "token_variables", []string{"GIT_CONFIG_VALUE_0"}, 400},
		{"git_parameters_reserved", "token_variables", []string{"GIT_CONFIG_PARAMETERS"}, 400},
		{"sites_33_unique", "network_sites", sites, 400},
		{"site_scheme", "network_sites", []string{"https://api.acme.example"}, 400},
		{"site_IP", "network_sites", []string{"10.0.0.5"}, 400},
		{"bare_wildcard", "network_sites", []string{"*"}, 400},
		{"git_port", "git", map[string]any{"host": "github.com:443"}, 400},
		{"git_username", "git", map[string]any{"host": "gitlab.example", "username": "x access"}, 400},
		{"check_word", "identity_check", map[string]any{"program_word": "other", "args": []string{}, "timeout_seconds": 20}, 400},
		{"check_args_17", "identity_check", map[string]any{"program_word": "acme", "args": words("arg", 17), "timeout_seconds": 20}, 400},
		{"check_timeout_31", "identity_check", map[string]any{"program_word": "acme", "args": []string{}, "timeout_seconds": 31}, 400},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := pgPersonAPI(t)
			before := pgProgramsList(t, f)
			canary := programstore.SeedCanary(t, f.store)
			storeBefore := programstore.Snapshot(t, f.store, canary)
			body := map[string]any{"display_name": "Acme", "program_words": []string{"acme"}, "token_variables": []string{"ACME_TOKEN"}, "network_sites": []string{"api.acme.example"}}
			body[row.field] = row.value
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			w := pgProgramsSessionCall(t, f, http.MethodPost, "/api/v1/programs", string(raw))
			require.Equal(t, row.status, w.Code, "D-2 %s class: %s", row.name, w.Body.String())
			if row.status == 409 {
				require.Contains(t, w.Body.String(), "GitHub")
			}
			require.Equal(t, before, pgProgramsList(t, f), "only invalid %s was supplied; no entry changed", row.field)
			require.Equal(t, storeBefore, programstore.Snapshot(t, f.store, canary))
		})
	}
}

func TestProgramCredential_TooShortRESTUnchanged(t *testing.T) {
	f := pgPersonAPI(t)
	pgConnectGithub(t, f)
	before := pgProgramEntryByID(t, f, "github")
	canary := programstore.SeedCanary(t, f.store)
	storeBefore := programstore.Snapshot(t, f.store, canary)
	starts := f.ghStarted()
	w := pgProgramsSessionCall(t, f, http.MethodPut, pgSaveRoute, `{"value":"abc1234"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "The token is too short. A token has at least 8 characters.")
	require.Equal(t, before, pgProgramEntryByID(t, f, "github"))
	require.Equal(t, storeBefore, programstore.Snapshot(t, f.store, canary))
	require.Equal(t, starts, f.ghStarted(), "S-02 invalid value starts no account check")
}

// S-127 covers boot callers, not just direct Store.Delete attacks. A
// provider-looking program ref is deliberately eligible for the orphan sweep;
// the store guard must still keep it while the ordinary orphan is removed.
func TestCredentialStore_ProgramOwnedBootOrphanSweep(t *testing.T) {
	f := pgPersonAPI(t)
	const ref = "program_API_KEY"
	_, err := f.store.CommitProgramCredential(ref, "tok_valid_12345", &credentials.CardReceipt{
		RequestID: "req-boot-program", SessionID: "sess-boot-program", References: []string{ref}, SubmittedAt: time.Now(),
	})
	require.NoError(t, err)
	before := programstore.RequireOwned(t, f.store, ref, "tok_valid_12345", "req-boot-program", "sess-boot-program")
	require.NoError(t, f.store.Set("ordinary_API_KEY", "ordinary_orphan_value"))
	sweepOrphanedProviderCredentials(f.api.agentLoop.GetConfig(), f.store, nil)
	_, err = f.store.Get("ordinary_API_KEY")
	var missing *credentials.NotFoundError
	require.ErrorAs(t, err, &missing, "instrument: the real boot sweep reached and removed an ordinary orphan")
	require.Equal(t, before, programstore.Snapshot(t, f.store, "req-boot-program"), "program entry survives the same boot sweep with all state intact")
}
