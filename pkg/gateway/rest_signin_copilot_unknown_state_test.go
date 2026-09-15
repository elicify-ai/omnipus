// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
)

// TestSignInStatus_CopilotUnknownStateIsNotShownAsNotSignedIn covers a Copilot
// sign-in check that returns a state this gateway's mapping does not know —
// for example a state added to pkg/providers without a matching case here.
//
// The mapping used to have no default branch, so such a state silently became
// not_signed_in: the operator was told "not yet, run copilot login" about a
// result nobody had interpreted. It must instead be reported as a failed
// check (an error response the dialog shows as "Sign-in check failed"), with
// the raw state in the server log, and nothing about it remembered.
func TestSignInStatus_CopilotUnknownStateIsNotShownAsNotSignedIn(t *testing.T) {
	const unknownState = "state_from_a_newer_providers_package"
	api, _ := newAuthMethodOnboardingAPI(t)

	previous := copilotSignInCheck
	copilotSignInCheck = func(context.Context, string, string) providers_pkg.CopilotSignInResult {
		return providers_pkg.CopilotSignInResult{State: unknownState, Detail: "test fixture"}
	}
	t.Cleanup(func() { copilotSignInCheck = previous })
	logs := captureSlog(t)

	w := adminRequest(t, api, http.MethodGet, copilotStatusPath)

	require.Equal(t, http.StatusInternalServerError, w.Code,
		"an uninterpreted result must not be answered as a sign-in state; body=%s", w.Body.String())
	assert.NotContains(t, w.Body.String(), `"state"`, "no sign-in state may be claimed")
	assert.Contains(t, w.Body.String(), "does not recognise")
	assert.Contains(t, logs.String(), unknownState, "the raw state must be in the server log; logs=%s", logs.String())

	_, cached := api.copilotProbe.hit()
	assert.False(t, cached, "an unrecognised result must never be cached")
	_, remembered := api.copilotProbe.lastResult()
	assert.False(t, remembered, "an unrecognised result must never be shown on the provider row")
}
