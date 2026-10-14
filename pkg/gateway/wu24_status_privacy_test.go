package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WU-24 CHECK F5 / ADR-068 FR-050: this is the STATUS handler's privacy
// boundary, not the separate provider-list handler. A seeded, nonempty
// account and an authenticated positive control make redaction observable.
func TestWU24SignInStatus_AnonymousOmitsSeededAccount(t *testing.T) {
	api := wu24FreshProviderAPI(t)
	store, err := api.resolveSignInCredStore()
	require.NoError(t, err)
	const account = "wu24-private-account"
	blob, err := json.Marshal(map[string]any{
		"access_token": "wu24-fixture-access-token", "account_id": account,
		"expires_at": "2099-01-01T00:00:00Z",
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName("openai"), string(blob)))
	const path = "/api/v1/providers/openai-chatgpt/sign-in/status"

	authed := doJSONWithUser(t, api, http.MethodGet, path, nil, "admin")
	require.Equal(t, http.StatusOK, authed.Code, authed.Body.String())
	var visible gen.SignInStatus
	require.NoError(t, json.Unmarshal(authed.Body.Bytes(), &visible))
	require.Equal(t, gen.SignInStatusStateSignedIn, visible.State)
	require.NotNil(t, visible.AccountLabel, "positive control must witness the saved account")
	require.Equal(t, account, *visible.AccountLabel)
	assert.JSONEq(t, `{"state":"signed_in","account_label":"wu24-private-account"}`, authed.Body.String())

	anonymous := doJSON(t, api, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, anonymous.Code, anonymous.Body.String())
	assert.JSONEq(t, `{"state":"signed_in"}`, anonymous.Body.String(),
		"same saved login, but the anonymous response must omit the label key entirely")

	// The anonymous observation must not erase the account from the source.
	authedAgain := doJSONWithUser(t, api, http.MethodGet, path, nil, "admin")
	require.Equal(t, http.StatusOK, authedAgain.Code, authedAgain.Body.String())
	assert.JSONEq(t, authed.Body.String(), authedAgain.Body.String())
}
