// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_onboarding_custom_hosted_guard_test.go — the custom onboarding path on
// the hosted edition with sandbox.ssrf OFF (restAPI.ssrfChecker nil).
//
// Requirement (custom endpoint design D3 and §6, hosted port note step 2): the
// custom path's address check and its key-check refusal use the hosted
// edition's always-on endpoint guard, not only the optional sandbox.ssrf
// checker. A custom endpoint the hosted guard refuses — directly, or because a
// permitted origin redirects to a blocked target — is refused with 422 and
// the design's internal-network text, before anything is stored: no provider
// row, no default model, no credential, onboarding still open, and the
// refused address receives no request.
//
// Core keeps its configurable guard: with sandbox.ssrf off a local endpoint is
// still accepted there (the controls at the end).

package gateway

import (
	"encoding/json"
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
)

// hostedGuardCustomIDs are four distinct custom spellings: a plain custom
// name, and three near-spellings of the test catalog's "plantco" (case,
// a Cyrillic final "о", a trailing dot) that are each their own custom id.
var hostedGuardCustomIDs = []string{"mybox", "PlantCo", "plantcо", "plantco."}

// hostedGuardClientSeq numbers the client addresses these tests send from.
// Onboarding completion is rate-limited per client address (3 a minute), so
// every request, including repeats under -count, uses an address of its own.
var hostedGuardClientSeq atomic.Uint32

// nextHostedGuardClientIP returns an unused client address from the
// benchmarking range (198.18.0.0/15, RFC 2544) for one request.
func nextHostedGuardClientIP() string {
	n := hostedGuardClientSeq.Add(1)
	return fmt.Sprintf("198.18.%d.%d", (n/250)%256, n%250+1)
}

// postHostedGuardComplete sends a custom onboarding completion through the
// production route table. The core edition needs the admin account in the
// body; hosted runs signed in.
func postHostedGuardComplete(
	t *testing.T, env *onboardAuthEnv, edition, id, endpoint, ip string,
) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"provider": map[string]any{
		"auth_method": "api_key",
		"id":          id,
		"protocol":    "openai-compatible",
		"endpoint":    endpoint,
		"model":       "guard-model",
		"api_key":     "sk-guard-submitted",
	}}
	if edition == config.EditionCore {
		body["admin"] = map[string]any{"username": "guard-admin", "password": "guard-admin-password-123"}
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPost, "/api/v1/onboarding/complete", string(raw), edition == config.EditionHosted), ip)
}

// assertHostedGuardRefusedNothingStored is the refusal outcome: 422 with the
// design's M8 text and no field, config.json byte-for-byte unchanged (so no
// provider row and no default model), no credential, onboarding still open.
func assertHostedGuardRefusedNothingStored(
	t *testing.T, env *onboardAuthEnv, w *httptest.ResponseRecorder, id string, configBefore []byte,
) {
	t.Helper()
	requireErrNoField(t, w, http.StatusUnprocessableEntity, msgM8)
	assert.Equal(t, string(configBefore), string(mustRawConfig(t, env.home)),
		"a refused custom endpoint must not change config.json")
	saved := readSavedConfig(t, env.home)
	assert.Empty(t, saved.Agents.Defaults.DefaultModel, "a refused custom endpoint must not set a default model")
	assertNothingStored(t, env.api, env.home, id)
}

// TestOnboardingCustomHostedGuard_SSRFOff_InternalAddress_422: hosted, sandbox
// ssrf off, no operator exception, a custom endpoint on loopback. Each of the
// four spellings is refused with 422 before any write, and the loopback
// listener is never contacted.
func TestOnboardingCustomHostedGuard_SSRFOff_InternalAddress_422(t *testing.T) {
	for i, id := range hostedGuardCustomIDs {
		t.Run(fmt.Sprintf("id-%d", i), func(t *testing.T) {
			env := newPlantEnv(t, config.EditionHosted, true)
			require.Nil(t, env.api.ssrfChecker, "precondition: sandbox.ssrf is off")
			canary := startPlantCanary(t)
			before := mustRawConfig(t, env.home)

			w := postHostedGuardComplete(t, env, config.EditionHosted, id, canary.URL+"/v1",
				nextHostedGuardClientIP())

			assertHostedGuardRefusedNothingStored(t, env, w, id, before)
			assert.Equal(t, int64(0), canary.hits.Load(), "the refused internal address must receive no request")
		})
	}
}

// TestOnboardingCustomHostedGuard_SSRFOff_RedirectToBlockedTarget_422: hosted,
// sandbox ssrf off. The operator names "localhost" in the allow list, so the
// origin passes the up-front check and is asked once; it redirects to the
// literal loopback address, which nobody allowed. The key check's refusal of
// that redirect must refuse the request with 422 and store nothing, and the
// redirect target must never be reached.
func TestOnboardingCustomHostedGuard_SSRFOff_RedirectToBlockedTarget_422(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	require.Nil(t, env.api.ssrfChecker, "precondition: sandbox.ssrf is off")
	target := startPlantCanary(t)
	var originHits atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		http.Redirect(w, r, target.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(origin.Close)
	providercommon.SetEndpointAllowlist([]string{"localhost"})
	t.Cleanup(func() { providercommon.SetEndpointAllowlist(nil) })
	base := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1) + "/v1"
	before := mustRawConfig(t, env.home)

	w := postHostedGuardComplete(t, env, config.EditionHosted, "redirect-box", base, nextHostedGuardClientIP())

	assertHostedGuardRefusedNothingStored(t, env, w, "redirect-box", before)
	assert.Equal(t, int64(1), originHits.Load(), "the permitted origin must have been asked once")
	assert.Equal(t, int64(0), target.hits.Load(), "the unlisted redirect target must receive no request")
}

// TestOnboardingCustomHostedGuard_CoreSSRFOff_LocalEndpointStillSaved: the
// core control. With sandbox.ssrf off core has no address guard, so the same
// loopback custom endpoint is probed and saved as before.
func TestOnboardingCustomHostedGuard_CoreSSRFOff_LocalEndpointStillSaved(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	require.Nil(t, env.api.ssrfChecker, "precondition: sandbox.ssrf is off")
	canary := startPlantCanary(t)

	w := postHostedGuardComplete(t, env, config.EditionCore, "mybox", canary.URL+"/v1", nextHostedGuardClientIP())

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, int64(1), canary.hits.Load(), "core with the guard off must probe the local endpoint")
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.Equal(t, "mybox", rows[0]["provider"])
	assert.Equal(t, canary.URL+"/v1", rows[0]["api_base"])
	assert.Equal(t, true, rows[0]["custom"])
	saved := readSavedConfig(t, env.home)
	assert.Equal(t, map[string]any{"provider": "mybox", "model": "guard-model"}, saved.Agents.Defaults.DefaultModel)
	key, err := env.api.credStore.Get("mybox_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "sk-guard-submitted", key)
	assert.True(t, env.api.onboardingMgr.IsComplete())
}

// TestOnboardingCustomHostedGuard_CoreSSRFOff_RedirectStillFollowed: the core
// control for the redirect case. With sandbox.ssrf off core follows the
// redirect to the local target and saves the row.
func TestOnboardingCustomHostedGuard_CoreSSRFOff_RedirectStillFollowed(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	require.Nil(t, env.api.ssrfChecker, "precondition: sandbox.ssrf is off")
	target := startPlantCanary(t)
	var originHits atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		http.Redirect(w, r, target.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(origin.Close)
	base := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1) + "/v1"

	w := postHostedGuardComplete(t, env, config.EditionCore, "redirect-box", base, nextHostedGuardClientIP())

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, int64(1), originHits.Load(), "the origin must have been asked once")
	assert.Equal(t, int64(1), target.hits.Load(), "core with the guard off must follow the redirect")
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.Equal(t, "redirect-box", rows[0]["provider"])
	assert.True(t, env.api.onboardingMgr.IsComplete())
}
