package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WU-24 CHECK F2: the removed credential-copy operation is not a
// compatibility success. Characterization explicitly requested by the
// founder: it must get the existing ordinary unknown-action refusal.
func TestWU24DeletedSignInActionIsUnknown(t *testing.T) {
	api := wu24FreshProviderAPI(t)
	const prefix = "/api/v1/providers/openai-chatgpt/sign-in/"
	unknown := wu24ProviderRequest(t, api, http.MethodPost, prefix+"wu24-unknown-action", nil)
	require.Equal(t, http.StatusMethodNotAllowed, unknown.Code, unknown.Body.String())
	require.JSONEq(t, `{"error":"method not allowed"}`, unknown.Body.String(),
		"positive control pins the ordinary unknown-action response")

	deleted := wu24ProviderRequest(t, api, http.MethodPost, prefix+"im"+"port", nil)
	assert.Equal(t, unknown.Code, deleted.Code, "a deleted operation must not return an empty success")
	assert.JSONEq(t, unknown.Body.String(), deleted.Body.String())
	assert.Equal(t, unknown.Header().Get("Content-Type"), deleted.Header().Get("Content-Type"))
}
