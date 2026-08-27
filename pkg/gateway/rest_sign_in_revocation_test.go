// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/auth"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
)

// TestSignOut_RevokesEvenWhileARefreshIsInFlight drives the finding through
// the actual DELETE /providers/{id}/sign-in handler (FR-048) rather than the
// providers-package seam.
//
// The failure it pins: the token is minutes from expiry, an agent turn starts
// a refresh, the vendor takes a couple of seconds. In that window the
// operator — who believes the token is compromised — clicks Sign out. The
// handler called store.Delete with no synchronisation against the refresh
// path at all, so the delete succeeded, the response said success, the audit
// log recorded provider.signed_out, and then the exchange completed and wrote
// a fresh access+refresh pair straight back. The grant was live again and
// nothing surfaced it.
//
// BDD: Given a stored openai-chatgpt credential inside the refresh lead and a
// refresh exchange parked at the vendor, When the operator calls DELETE
// .../sign-in, Then the request does not complete until the exchange has, the
// stored entry is absent afterwards, and GET .../sign-in/status agrees the
// provider is not signed in.
func TestSignOut_RevokesEvenWhileARefreshIsInFlight(t *testing.T) {
	api, store := signInAPIWithUnlockedStore(t)
	entryName := credentials.OAuthEntryName("openai")

	blob, err := json.Marshal(map[string]any{
		"access_token":  "compromised-access-token",
		"refresh_token": "compromised-refresh-token",
		"account_id":    "acc_1",
		// Inside needsOAuthRefresh's five-minute lead, so the token source
		// goes to the vendor.
		"expires_at": time.Now().Add(4 * time.Minute).Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(entryName, string(blob)))

	// The parked vendor is released through a sync.Once and that release is
	// registered BEFORE the server's own Close (t.Cleanup is LIFO), so a
	// t.Fatal on any failure path below still unparks the handler. Without
	// that, httptest.Server.Close blocks on the parked handler and a failing
	// test hangs the package instead of reporting — which is how a real
	// regression here would first present itself.
	entered := make(chan struct{})
	var releaseOnce sync.Once
	gate := make(chan struct{})
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-gate
		resp := map[string]any{
			"access_token":  "resurrected-access-token",
			"refresh_token": "resurrected-refresh-token",
			"expires_in":    3600,
		}
		if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
			t.Errorf("encoding vendor refresh response: %v", encErr)
		}
	}))
	t.Cleanup(vendor.Close) // registered first -> runs second
	t.Cleanup(release)      // registered second -> runs first

	// The agent path's token source, built exactly as CreateProviderFromConfig
	// builds it, over the same shared credential store the handler uses.
	tokenSource := providers_pkg.NewStoreOAuthTokenSource("openai-chatgpt", store,
		auth.OAuthProviderConfig{
			Issuer:   vendor.URL,
			ClientID: "test-client",
			Timeout:  providers_pkg.MaxOAuthRefreshLockHold,
		})
	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		if _, _, srcErr := tokenSource(); srcErr != nil {
			t.Errorf("the refresh itself must succeed here: %v", srcErr)
		}
	}()

	<-entered

	// Everything the handler needs is prepared on the test goroutine —
	// doJSON's require.* calls must not run on a spawned one.
	signOutReq := httptest.NewRequest(http.MethodDelete, "/api/v1/providers/openai-chatgpt/sign-in", nil)
	signOutReq = signOutReq.WithContext(
		context.WithValue(signOutReq.Context(), configContextKey{}, api.agentLoop.GetConfig()))
	signOutRec := httptest.NewRecorder()

	signOutDone := make(chan struct{})
	go func() {
		defer close(signOutDone)
		api.HandleProviders(signOutRec, signOutReq)
	}()

	// Long enough that an unsynchronised delete — a local file write taking
	// microseconds — would have finished many times over.
	select {
	case <-signOutDone:
		t.Fatal("sign-out returned while the vendor exchange was still in flight: " +
			"nothing orders the delete against the write that follows the exchange, " +
			"so the refreshed credential can land after the operator revoked it")
	case <-time.After(200 * time.Millisecond):
	}

	release()
	<-signOutDone
	<-refreshDone

	require.Equal(t, http.StatusOK, signOutRec.Code, signOutRec.Body.String())
	var result gen.OperationResult
	require.NoError(t, json.Unmarshal(signOutRec.Body.Bytes(), &result))
	assert.True(t, result.Success)

	_, getErr := store.Get(entryName)
	require.Error(t, getErr,
		"REVOKED CREDENTIAL RESURRECTED: the entry sign-out deleted exists again, "+
			"rewritten by the exchange that was in flight")
	var notFound *credentials.NotFoundError
	require.ErrorAs(t, getErr, &notFound)

	statusRec := doJSON(t, api, http.MethodGet, "/api/v1/providers/openai-chatgpt/sign-in/status", nil)
	var status gen.SignInStatus
	require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
	assert.Equal(t, gen.SignInStatusStateNotSignedIn, status.State,
		"the status endpoint must agree the provider is signed out")
}

// TestSignInRefreshOAuthConfig_BoundsTheSharedRefreshLockHold pins the
// timeout alignment. deviceCodeStatus may refresh, and it does so holding the
// process-wide per-vendor lock that live agent turns and sign-out queue
// behind. Left at the auth package's 30s interactive default it silently
// became the ceiling for all of them, making the agent path's deliberate 20s
// bound ("a hung vendor costs one turn") not a ceiling at all.
//
// The oracle is the config the status path actually builds — asserted for
// every device_code provider, and against a seam that deliberately supplies
// an UNBOUNDED config, so the test fails if the override is removed rather
// than passing on the seam's own value. A wall-clock measurement would mean
// a 20-second test of the auth package's HTTP client, which is not what this
// is about.
func TestSignInRefreshOAuthConfig_BoundsTheSharedRefreshLockHold(t *testing.T) {
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("this vendor is never meant to be called")
	}))
	defer unreachable.Close()
	withDeviceCodeVendor(t, unreachable) // returns Timeout: 0 — the 30s default

	base, cfgErr := oauthConfigFor("openai-chatgpt")
	require.NoError(t, cfgErr)
	require.Zero(t, base.Timeout,
		"the seam must supply an unbounded config, or this test would pass without the override")

	for _, providerID := range []string{"openai-chatgpt", "xai"} {
		cfg, err := signInRefreshOAuthConfig(providerID)
		require.NoError(t, err, providerID)
		assert.Equal(t, providers_pkg.MaxOAuthRefreshLockHold, cfg.Timeout,
			"%s: a sign-in path that may refresh must bound its hold on the shared per-vendor lock", providerID)
		assert.Equal(t, unreachable.URL, cfg.Issuer,
			"%s: the endpoint must still come from oauthConfigFor", providerID)
	}

	// And that ceiling is the agent path's own bound, not an independent
	// number that can drift away from it.
	assert.Equal(t, 20*time.Second, providers_pkg.MaxOAuthRefreshLockHold)
}

// TestDeviceCodeStatus_UsesTheBoundedConfig closes the loop: the bounded
// config is not merely available from signInRefreshOAuthConfig, it is what
// deviceCodeStatus actually hands to the token source.
//
// The oracle is the config captured at the constructor seam. It cannot be the
// fake vendor's view of the request: OAuthProviderConfig.Timeout becomes a
// CLIENT-side http.Client.Timeout and request context deadline
// (pkg/auth/oauth.go's doOAuthPost), and neither of those crosses the wire, so
// the server's r.Context() has no deadline to report. Nor can it be a
// wall-clock measurement, which would mean a 20-second test of the auth
// package's HTTP client rather than of this file's decision.
func TestDeviceCodeStatus_UsesTheBoundedConfig(t *testing.T) {
	api, store := signInAPIWithUnlockedStore(t)

	blob, err := json.Marshal(map[string]any{
		"access_token": "tok",
		"account_id":   "acc_1",
		"expires_at":   time.Now().Add(1 * time.Hour).Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName("openai"), string(blob)))

	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("this vendor is never meant to be called")
	}))
	defer unreachable.Close()
	withDeviceCodeVendor(t, unreachable) // supplies Timeout: 0 — the 30s default

	var captured auth.OAuthProviderConfig
	var captureCount int
	prev := newStoreOAuthTokenSource
	newStoreOAuthTokenSource = func(
		providerID string, s *credentials.Store, cfg auth.OAuthProviderConfig,
	) func() (string, string, error) {
		captured = cfg
		captureCount++
		return prev(providerID, s, cfg)
	}
	t.Cleanup(func() { newStoreOAuthTokenSource = prev })

	status := api.deviceCodeStatus("openai-chatgpt")
	require.Equal(t, gen.SignInStatusStateSignedIn, status.State)

	require.Equal(t, 1, captureCount, "deviceCodeStatus must build exactly one token source")
	assert.Equal(t, providers_pkg.MaxOAuthRefreshLockHold, captured.Timeout,
		"deviceCodeStatus handed the token source an unbounded config: its refresh would hold the "+
			"shared per-vendor lock past the ceiling every agent turn and sign-out queues behind")
	assert.Equal(t, unreachable.URL, captured.Issuer,
		"the vendor endpoint must still come from oauthConfigFor")
}
