package gateway

// rest_onboarding_custom_review_test.go — WP-1 (#1700) review round 1.
//
// Three findings of the independent backend review
// (/workspace/omnipus-oss/.tmp/be-review-report.md), each reproduced by the
// reviewer before this file existed:
//
//  1. A custom row whose address passes the first check but is refused by
//     the guard at key-check time (DNS answer changes, or a redirect to a
//     blocked target) was saved with an "unreachable" warning. Expected now:
//     the same 422 and M8 as an address refused up front, nothing stored.
//     Ordinary unreachable endpoints and catalog rows keep their warning.
//  2. A custom id is trimmed first; a blank id, or one that cannot name the
//     "<id>_API_KEY" environment variable (contains "="), is refused 400 on
//     field id with M2, and the reserved/catalog checks are repeated on the
//     trimmed id.
//  3. A custom model is trimmed first; a whitespace-only model is refused
//     400 on field model with M7 before any outbound call, and the trimmed
//     value is what is probed and saved.
//
// Helpers (newRecordingUpstream, newCustomOnboardingAPI, postComplete,
// customCompleteBody, requireErrField, requireErrNoField,
// assertNothingStored, readSavedConfig, msgM*) come from
// rest_onboarding_custom_test.go in the same package.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// rebindingResolver answers the first lookup with a public address and every
// later one with loopback: the DNS change between the first address check and
// the key check's connection.
type rebindingResolver struct {
	mu    sync.Mutex
	calls int
}

func (r *rebindingResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls == 1 {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

// rebindingEndpoint returns a base URL on a made-up host name whose port is
// the recording server's, so that if the guard did NOT refuse the second
// answer, the request would really reach the recording server.
func rebindingEndpoint(t *testing.T, ru *recordingUpstream) string {
	t.Helper()
	u, err := url.Parse(ru.srv.URL)
	require.NoError(t, err)
	return fmt.Sprintf("http://rebind.example.test:%s/v1", u.Port())
}

// --- finding 1: address refused at key-check time ---------------------------

func TestOnboardingCompleteCustomReview_RebindAtKeyCheck_422(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)
	checker := security.NewSSRFChecker(nil)
	resolver := &rebindingResolver{}
	checker.SetResolver(resolver)
	api.ssrfChecker = checker

	w := postComplete(t, api, customCompleteBody(ru.srv.URL,
		map[string]string{"endpoint": rebindingEndpoint(t, ru)}))

	requireErrNoField(t, w, http.StatusUnprocessableEntity, msgM8)
	assert.GreaterOrEqual(t, resolver.calls, 2,
		"the key check must have resolved the host a second time (else this test proves nothing)")
	assert.Empty(t, ru.received(), "the refused address must receive no request")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

func TestOnboardingCompleteCustomReview_RedirectToBlockedAddress_422(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		hits++
		mu.Unlock()
		http.Redirect(w, r, "http://169.254.169.254/v1/chat/completions", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)

	api, tmpDir := newCustomOnboardingAPI(t, false)
	// The endpoint itself (loopback) is allowed; the redirect target (cloud
	// metadata) is not.
	api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1"})

	w := postComplete(t, api, customCompleteBody(srv.URL, map[string]string{"endpoint": srv.URL + "/v1"}))

	requireErrNoField(t, w, http.StatusUnprocessableEntity, msgM8)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, hits, "the allowed endpoint was asked once and answered with the redirect")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

func TestOnboardingCompleteCustomReview_UnreachableStillWarns(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	base := closed.URL
	closed.Close() // nothing listens on this port any more

	api, tmpDir := newCustomOnboardingAPI(t, false)
	api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1"})

	w := postComplete(t, api, customCompleteBody(base, map[string]string{"endpoint": base + "/v1"}))

	require.Equal(t, http.StatusOK, w.Code,
		"an endpoint that is simply not answering keeps today's warn-and-continue (body=%s)", w.Body.String())
	resp := decodeResponse(t, w)
	warning, _ := resp["warning"].(string)
	assert.NotEmpty(t, warning, "the operator must be told the key could not be checked")
	saved := readSavedConfig(t, tmpDir)
	require.Len(t, saved.Providers, 1)
	assert.Equal(t, true, saved.Providers[0]["custom"])
}

func TestOnboardingCompleteCustomReview_CatalogRebindStillWarns(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)
	checker := security.NewSSRFChecker(nil)
	checker.SetResolver(&rebindingResolver{})
	api.ssrfChecker = checker

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{
		"id":       "openrouter",
		"endpoint": rebindingEndpoint(t, ru),
	}, "protocol"))

	require.Equal(t, http.StatusOK, w.Code,
		"a catalog row keeps its skip-with-warning behaviour (body=%s)", w.Body.String())
	resp := decodeResponse(t, w)
	warning, _ := resp["warning"].(string)
	assert.NotEmpty(t, warning)
	assert.Empty(t, ru.received())
	saved := readSavedConfig(t, tmpDir)
	require.Len(t, saved.Providers, 1)
	_, hasCustom := saved.Providers[0]["custom"]
	assert.False(t, hasCustom)
}

// --- finding 2: id normalisation --------------------------------------------

func TestOnboardingCompleteCustomReview_IdRefusals(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		wantMsg string
	}{
		{"blank_after_trim", "   ", msgM2},
		{"not_an_env_var_name", "bad=name", msgM2},
		{"reserved_after_trim", " catalog ", `unknown provider "catalog"`},
		{"listed_after_trim", " openai ", fmt.Sprintf(msgM1Fmt, "openai", "openai")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
			api, tmpDir := newCustomOnboardingAPI(t, false)

			w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": tc.id}))

			requireErrField(t, w, http.StatusBadRequest, "id", tc.wantMsg)
			assert.Empty(t, ru.received(), "a refused id must not reach the endpoint")
			assertNothingStored(t, api, tmpDir, tc.id)
			assertNothingStored(t, api, tmpDir, "")
		})
	}
}

func TestOnboardingCompleteCustomReview_IdTrimmedWhenSaved(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": "  my-proxy  "}))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	saved := readSavedConfig(t, tmpDir)
	require.Len(t, saved.Providers, 1)
	assert.Equal(t, "my-proxy", saved.Providers[0]["provider"])
	assert.Equal(t, "my-proxy_API_KEY", saved.Providers[0]["api_key_ref"])
	assert.Equal(t, "my-proxy", saved.Agents.Defaults.DefaultModel["provider"])
	stored, err := api.credStore.Get("my-proxy_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "k", stored)
}

// --- finding 3: model normalisation -----------------------------------------

func TestOnboardingCompleteCustomReview_WhitespaceModel_400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"model": "   "}))

	requireErrField(t, w, http.StatusBadRequest, "model", msgM7)
	assert.Empty(t, ru.received(), "a refused request must not reach the endpoint")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

func TestOnboardingCompleteCustomReview_ModelTrimmedEverywhere(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"model": "  gpt-4o-prod  "}))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	got := ru.received()
	require.Len(t, got, 1)
	var probeBody map[string]any
	require.NoError(t, json.Unmarshal(got[0].Body, &probeBody))
	assert.Equal(t, "gpt-4o-prod", probeBody["model"], "the trimmed model is what the key check uses")
	saved := readSavedConfig(t, tmpDir)
	require.Len(t, saved.Providers, 1)
	assert.Equal(t, "gpt-4o-prod", saved.Providers[0]["model"])
	assert.Equal(t, "gpt-4o-prod", saved.Providers[0]["model_name"])
	assert.Equal(t,
		map[string]any{"provider": "my-azure", "model": "gpt-4o-prod"},
		saved.Agents.Defaults.DefaultModel)
}
