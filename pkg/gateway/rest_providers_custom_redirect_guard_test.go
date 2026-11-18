// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_providers_custom_redirect_guard_test.go — PUT /api/v1/providers/{id}
// for a custom provider whose permitted address redirects to one the address
// guard refuses.
//
// Requirement: a custom row's save-time key check runs through the effective
// endpoint checker (the configured sandbox.ssrf checker, else the hosted
// edition's always-on guard), and a policy refusal during that check — here
// a redirect to an address nobody allowed — refuses the save with 422 and the
// internal-network text before anything is written, exactly as the custom
// onboarding path does. Create and update are both covered; an update must
// keep the earlier row. With no checker at all (core, sandbox.ssrf off) the
// redirect is followed and the row saved as before.

package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	providercommon "github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// redirectingOrigin answers every request with a 307 to target and counts
// the requests it received.
type redirectingOrigin struct {
	*httptest.Server
	hits atomic.Int64
}

func startRedirectingOrigin(t *testing.T, target string) *redirectingOrigin {
	t.Helper()
	o := &redirectingOrigin{}
	o.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.hits.Add(1)
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(o.Close)
	return o
}

// putCustomRedirectProvider sends a custom-provider PUT with a key, model,
// protocol and api_base. Hosted runs signed in; core uses the first-run
// window.
func putCustomRedirectProvider(t *testing.T, env *onboardAuthEnv, edition, id, base string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"api_key":"sk-put-submitted","model":"put-model","protocol":"openai-compatible","api_base":%q}`, base)
	return serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/"+id, body, edition == config.EditionHosted),
		nextHostedGuardClientIP())
}

// seedEarlierCustomRow is the row an update would replace.
func seedEarlierCustomRow(t *testing.T, env *onboardAuthEnv, id string) {
	t.Helper()
	seedProviderRow(t, env, map[string]any{
		"provider": id, "custom": true, "protocol": "openai-compatible",
		"model": "earlier-model", "api_base": "https://earlier.example.invalid/v1",
	})
}

// TestProviderPutCustomRedirectGuard_Hosted_SSRFOff_422: hosted, sandbox ssrf
// off. "localhost" is on the operator's allow list, so the origin passes the
// up-front checks and is asked once; it redirects to an address nobody
// allowed (the loopback literal, or the cloud metadata address). The save is
// refused with 422, config.json is byte-for-byte unchanged (no new row, the
// earlier row kept on update), no key is stored, and the redirect target is
// never reached.
func TestProviderPutCustomRedirectGuard_Hosted_SSRFOff_422(t *testing.T) {
	targets := []struct {
		name   string
		canary bool
		url    string
	}{
		{name: "loopback", canary: true},
		{name: "metadata", url: "http://169.254.169.254/v1/chat/completions"},
	}
	for _, tc := range targets {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing-%t", tc.name, existing), func(t *testing.T) {
				env := newPlantEnv(t, config.EditionHosted, true)
				require.Nil(t, env.api.ssrfChecker, "precondition: sandbox.ssrf is off")
				if existing {
					seedEarlierCustomRow(t, env, "redirect-put")
				}
				target := startPlantCanary(t)
				targetURL := tc.url
				if tc.canary {
					targetURL = target.URL + "/v1/chat/completions"
				}
				origin := startRedirectingOrigin(t, targetURL)
				providercommon.SetEndpointAllowlist([]string{"localhost"})
				t.Cleanup(func() { providercommon.SetEndpointAllowlist(nil) })
				base := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1) + "/v1"
				before := mustRawConfig(t, env.home)

				w := putCustomRedirectProvider(t, env, config.EditionHosted, "redirect-put", base)

				requireErrNoField(t, w, http.StatusUnprocessableEntity, msgM8)
				assert.Equal(t, string(before), string(mustRawConfig(t, env.home)),
					"a refused custom PUT must not change config.json")
				_, err := env.api.credStore.Get("redirect-put_API_KEY")
				assert.Error(t, err, "a refused custom PUT must not store the submitted key")
				assert.Equal(t, int64(1), origin.hits.Load(), "the permitted origin must have been asked once")
				assert.Equal(t, int64(0), target.hits.Load(), "the refused redirect target must receive no request")
			})
		}
	}
}

// TestProviderPutCustomRedirectGuard_CoreSSRFOn_422: core with sandbox.ssrf
// on applies its configured checker the same way: the redirect to the
// unlisted loopback literal refuses the save.
func TestProviderPutCustomRedirectGuard_CoreSSRFOn_422(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	env.api.ssrfChecker = security.NewSSRFChecker([]string{"localhost"})
	target := startPlantCanary(t)
	origin := startRedirectingOrigin(t, target.URL+"/v1/chat/completions")
	base := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1) + "/v1"
	before := mustRawConfig(t, env.home)

	w := putCustomRedirectProvider(t, env, config.EditionCore, "redirect-put", base)

	requireErrNoField(t, w, http.StatusUnprocessableEntity, msgM8)
	assert.Equal(t, string(before), string(mustRawConfig(t, env.home)), "a refused custom PUT must not change config.json")
	_, err := env.api.credStore.Get("redirect-put_API_KEY")
	assert.Error(t, err, "a refused custom PUT must not store the submitted key")
	assert.Equal(t, int64(1), origin.hits.Load(), "the permitted origin must have been asked once")
	assert.Equal(t, int64(0), target.hits.Load(), "the refused redirect target must receive no request")
}

// TestProviderPutCustomRedirectGuard_CoreSSRFOff_StillSaved: the core
// control. With sandbox.ssrf off there is no address guard; the redirect is
// followed and the custom row and key are saved as before.
func TestProviderPutCustomRedirectGuard_CoreSSRFOff_StillSaved(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	require.Nil(t, env.api.ssrfChecker, "precondition: sandbox.ssrf is off")
	target := startPlantCanary(t)
	origin := startRedirectingOrigin(t, target.URL+"/v1/chat/completions")
	base := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1) + "/v1"

	w := putCustomRedirectProvider(t, env, config.EditionCore, "redirect-put", base)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, int64(1), origin.hits.Load(), "the origin must have been asked once")
	assert.Equal(t, int64(1), target.hits.Load(), "core with the guard off must follow the redirect")
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.Equal(t, "redirect-put", rows[0]["provider"])
	assert.Equal(t, base, rows[0]["api_base"])
	assert.Equal(t, true, rows[0]["custom"])
	key, err := env.api.credStore.Get("redirect-put_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "sk-put-submitted", key)
}
