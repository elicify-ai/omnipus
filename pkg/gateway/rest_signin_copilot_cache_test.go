// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// copilotStatusPath is the Copilot sign-in check route (ADR-068 FR-009).
const copilotStatusPath = "/api/v1/providers/github-copilot/sign-in/status"

// copilotSessionRejectedStderr is the expired-session fixture the Copilot
// sign-in tests already use. The real CLI's wording for an expired or revoked
// session is unverified (it needs a live subscription), so this is a fixture,
// not a claim about the vendor's text.
const copilotSessionRejectedStderr = "Error: your Copilot session has expired. Run `copilot login` again."

// copilotStatusState runs one Check sign-in as an admin and returns the state.
func copilotStatusState(t *testing.T, api *restAPI) gen.SignInStatusState {
	t.Helper()
	w := adminRequest(t, api, http.MethodGet, copilotStatusPath)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got gen.SignInStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got.State
}

// TestCopilotProbe_ExpiredIsNeverCached is the recovery half of the C2 cache.
//
// An `expired` answer tells the operator to run `copilot login` and click
// Check sign-in again. That second click is exactly the transition the cache
// must never mask: it used to be answered from a five-minute cached `expired`,
// so an operator who had just fixed their login kept seeing "expired", with
// nothing on screen saying the answer was old.
//
// The billing protection C2 exists for is kept and re-proved at the end:
// once the operator IS signed in, repeated checks inside the TTL spend no
// further vendor run.
func TestCopilotProbe_ExpiredIsNeverCached(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	dir := neutralFakeCLIDir(t)
	tally := filepath.Join(dir, "invocations")
	installFakeCopilot(t, dir, "", copilotSessionRejectedStderr, 1, tally)
	t.Setenv("PATH", dir)

	require.Equal(t, gen.SignInStatusStateExpired, copilotStatusState(t, api))
	require.Equal(t, 1, countInvocations(t, tally), "the first check must run the CLI once")

	// The operator runs `copilot login`: the same binary now succeeds.
	// Re-installing resets the tally after its own positive control.
	installFakeCopilot(t, dir, "ok", "", 0, tally)

	assert.Equal(t, gen.SignInStatusStateSignedIn, copilotStatusState(t, api),
		"Check sign-in right after `copilot login` must see the new login, never a cached expired")
	require.Equal(t, 1, countInvocations(t, tally),
		"the check that follows an expired answer must run the CLI again")

	for i := 0; i < 5; i++ {
		assert.Equal(t, gen.SignInStatusStateSignedIn, copilotStatusState(t, api), "repeat check %d", i)
	}
	assert.Equal(t, 1, countInvocations(t, tally),
		"repeated checks of a signed-in operator inside the TTL must be answered from the cache (C2)")
}
