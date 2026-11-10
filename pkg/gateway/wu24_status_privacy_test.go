package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WU-24 CHECK F5 / ADR-068 FR-050: this is the STATUS handler's privacy
// boundary, not the separate provider-list handler. A seeded, nonempty
// account and an authenticated positive control make redaction observable.
//
// The anonymous caller only reaches the handler through FR-050's pre-auth
// onboarding window, which exists in local mode (core edition) only. In
// platform mode (hosted) the window is closed, so the anonymous request is
// refused outright and carries no sign-in state at all.
func TestWU24SignInStatus_AnonymousOmitsSeededAccount(t *testing.T) {
	for _, tc := range []struct {
		edition       string
		anonymousCode int
	}{
		{config.EditionCore, http.StatusOK},
		{config.EditionHosted, http.StatusUnauthorized},
	} {
		t.Run(tc.edition, func(t *testing.T) {
			api := wu24FreshProviderAPI(t) // pins hosted; re-pinned below
			withEdition(t, tc.edition)
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
			require.Equal(t, tc.anonymousCode, anonymous.Code, anonymous.Body.String())
			if tc.anonymousCode == http.StatusOK {
				assert.JSONEq(t, `{"state":"signed_in"}`, anonymous.Body.String(),
					"same saved login, but the anonymous response must omit the label key entirely")
			} else {
				assert.NotContains(t, anonymous.Body.String(), account)
				assert.NotContains(t, anonymous.Body.String(), "signed_in")
			}

			// The anonymous observation must not erase the account from the source.
			authedAgain := doJSONWithUser(t, api, http.MethodGet, path, nil, "admin")
			require.Equal(t, http.StatusOK, authedAgain.Code, authedAgain.Body.String())
			assert.JSONEq(t, authed.Body.String(), authedAgain.Body.String())
		})
	}
}
