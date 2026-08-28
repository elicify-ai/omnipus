// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// signInAPIWithUnlockedStore builds the sign-in test restAPI with the shared
// credential store already injected, so deviceCodeStatus does not pay an
// Argon2id unlock per call (resolveSignInCredStore short-circuits on a
// non-nil a.credStore). Returns the store too.
func signInAPIWithUnlockedStore(t *testing.T) (*restAPI, *credentials.Store) {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	api.credStore = store
	return api, store
}

// TestDeviceCodeStatus_ConcurrentWithSessionWrites is the regression test for
// the pre-auth process-killing defect fixed alongside it: deviceSessions()
// returned the LIVE signInSessions map with signInMu already released, and
// deviceCodeStatus ranged over it unlocked. A concurrent
// putDeviceSession/resolveDeviceSession during that range is
//
//	fatal error: concurrent map iteration and map write
//
// which is Go's UNRECOVERABLE runtime fatal — it takes down the whole gateway
// process, not the request. Both GET /providers/{id}/sign-in/status and
// POST /providers/{id}/sign-in are reachable UNAUTHENTICATED while onboarding
// is incomplete (ADR-068 FR-050), so any unauthenticated client able to reach
// the gateway could kill it.
//
// BDD: Given an open device-code session for openai-chatgpt,
// When many concurrent GET .../sign-in/status reads race many concurrent
// device-code session writes,
// Then no read observes a torn session, every read reports `pending` (the
// pinned session is never resolved and never expires during the test), and
// the process does not fault.
//
// Run with -race: before the fix this reports a data race on the map and on
// deviceCodeSession.resolved / .intervalSeconds (and, un-raced, faults
// outright); after it, the only synchronisation is signInMu and both are
// clean.
func TestDeviceCodeStatus_ConcurrentWithSessionWrites(t *testing.T) {
	api, _ := signInAPIWithUnlockedStore(t)

	// A pinned, never-resolved, never-expiring session for the provider under
	// test. It makes the assertion deterministic (status is always `pending`)
	// AND keeps deviceCodeStatus off the credential-store path, so what the
	// goroutines below contend on is exactly the session map.
	api.putDeviceSession("das_pinned", deviceCodeSession{
		providerID:      "openai-chatgpt",
		createdAt:       time.Now(),
		expiresAt:       time.Now().Add(time.Hour),
		intervalSeconds: 5,
	})

	const (
		readers    = 4
		writers    = 4
		iterations = 400
	)
	var wg sync.WaitGroup
	states := make([][]gen.SignInStatusState, readers)

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			seen := make([]gen.SignInStatusState, 0, iterations)
			for i := 0; i < iterations; i++ {
				seen = append(seen, api.deviceCodeStatus("openai-chatgpt").State)
			}
			states[idx] = seen
		}(r)
	}
	for wr := 0; wr < writers; wr++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				handle := fmt.Sprintf("das_w%d_%d", idx, i)
				api.putDeviceSession(handle, deviceCodeSession{
					providerID:      "openai-chatgpt",
					createdAt:       time.Now(),
					expiresAt:       time.Now().Add(time.Hour),
					intervalSeconds: 5,
				})
				// The two writes that used to happen OUTSIDE the lock: the
				// single-use resolve, and the slow_down interval widening.
				api.widenDeviceSessionInterval(handle)
				api.resolveDeviceSession(handle)
			}
		}(wr)
	}
	wg.Wait()

	for r := range states {
		require.Len(t, states[r], iterations)
		for i, st := range states[r] {
			require.Equal(t, gen.SignInStatusStatePending, st,
				"reader %d iteration %d: the pinned session is open for the whole test, so every status read must be pending", r, i)
		}
	}

	// The pinned session survived; every writer session was resolved away.
	assert.True(t, api.pendingDeviceSessionExists("openai-chatgpt"))
	api.signInMu.Lock()
	remaining := len(api.signInSessions)
	api.signInMu.Unlock()
	assert.Equal(t, 1, remaining, "only the pinned session may remain")
}

// TestDeviceSessions_AbandonedSessionsAreSwept covers the leak: a dialog the
// operator opened and closed never reaches a terminal poll outcome, so before
// the sweep its entry lived in signInSessions until the process exited. The
// sweep runs opportunistically inside the two hot paths (a new session being
// stored, and a status read scanning for a pending one) using the session's
// own expiresAt, which putDeviceSession's caller caps at
// deviceCodeSessionMaxTTL.
//
// BDD: Given an abandoned device-code session whose expiry has passed,
// When another sign-in starts (or a status read scans),
// Then the abandoned entry is gone and the live one is untouched.
func TestDeviceSessions_AbandonedSessionsAreSwept(t *testing.T) {
	api, _ := signInAPIWithUnlockedStore(t)

	api.putDeviceSession("das_abandoned", deviceCodeSession{
		providerID:      "openai-chatgpt",
		createdAt:       time.Now().Add(-deviceCodeSessionMaxTTL - time.Minute),
		expiresAt:       time.Now().Add(-time.Minute), // past its ceiling
		intervalSeconds: 5,
	})
	api.signInMu.Lock()
	require.Len(t, api.signInSessions, 1, "the abandoned session is stored before the sweep can see it as expired")
	api.signInMu.Unlock()

	// An expired session must not report pending — and the scan sweeps it.
	assert.False(t, api.pendingDeviceSessionExists("openai-chatgpt"))
	api.signInMu.Lock()
	_, stillThere := api.signInSessions["das_abandoned"]
	api.signInMu.Unlock()
	assert.False(t, stillThere, "an expired abandoned session must not survive a status scan")

	// The put path sweeps too, and leaves the live session alone.
	api.putDeviceSession("das_stale2", deviceCodeSession{
		providerID: "xai",
		expiresAt:  time.Now().Add(-time.Second),
	})
	api.putDeviceSession("das_live", deviceCodeSession{
		providerID:      "openai-chatgpt",
		createdAt:       time.Now(),
		expiresAt:       time.Now().Add(time.Hour),
		intervalSeconds: 5,
	})
	api.signInMu.Lock()
	_, staleGone := api.signInSessions["das_stale2"]
	_, liveKept := api.signInSessions["das_live"]
	total := len(api.signInSessions)
	api.signInMu.Unlock()
	assert.False(t, staleGone, "putDeviceSession must sweep expired entries")
	assert.True(t, liveKept, "putDeviceSession must not sweep a live entry")
	assert.Equal(t, 1, total)
	assert.True(t, api.pendingDeviceSessionExists("openai-chatgpt"))
}

// TestSignInPoll_SlowDownWidensStoredInterval pins the behaviour that moved
// inside the lock: the vendor's RFC 8628 slow_down back-off must be written
// to the STORED session (so the next poll and a concurrent status read see
// the new floor), not only to the response body.
//
// BDD: Given an open device-code session with interval 1,
// When the vendor answers slow_down twice,
// Then the responses report 2 then 4 seconds, i.e. the widening compounded
// on the stored session rather than restarting from the original interval.
func TestSignInPoll_SlowDownWidensStoredInterval(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	state := "pending"
	server := httptest.NewServer(deviceCodeVendorMux(t, &state))
	defer server.Close()
	withDeviceCodeVendor(t, server)

	startW := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in", nil)
	require.Equal(t, http.StatusOK, startW.Code, startW.Body.String())
	var startResp gen.SignInStartResponse
	require.NoError(t, json.Unmarshal(startW.Body.Bytes(), &startResp))
	deviceCode, err := startResp.AsSignInStartResponseDeviceCode()
	require.NoError(t, err)
	require.Equal(t, 1, deviceCode.IntervalSeconds, "the vendor mux reports interval 1")

	state = "slow_down"
	pollBody := gen.SignInPollRequest{DeviceAuthId: deviceCode.DeviceAuthId}

	for _, want := range []int{2, 4} {
		w := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/poll", pollBody)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp gen.SignInPollResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, gen.SignInPollResponseStatePending, resp.State)
		require.NotNil(t, resp.IntervalSeconds)
		assert.Equal(t, want, *resp.IntervalSeconds,
			"the widened interval must be persisted on the stored session, so it compounds")
	}
}
