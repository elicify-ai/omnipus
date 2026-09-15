// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestProbeProvider_CopilotFailureNeverClaimsSignIn covers the onboarding
// sign-in probe (ADR-068 FR-036) for github-copilot when the one test
// completion fails with a message that names no sign-in state.
//
// For Copilot nothing about the login is read before that completion: the CLI
// keeps its token in the OS credential store and has no status command, so the
// only pre-check is "the binary exists". The failure message therefore must
// not tell the operator "You're signed in" — on a machine that has never run
// `copilot login` but whose CLI hangs or cannot reach GitHub, that sentence is
// false.
func TestProbeProvider_CopilotFailureNeverClaimsSignIn(t *testing.T) {
	api := newProbeAPI(t)
	dir := neutralFakeCLIDir(t)
	// The fixture the existing probe test uses for a refusal that matches no
	// sign-in marker.
	installFakeCopilot(t, dir, "", "model claude-opus-4.7 is not enabled for your plan", 1, "")
	t.Setenv("PATH", dir)

	w := postProbe(t, api, `{"id":"github-copilot","auth":"sign_in","model":"claude-opus-4.7"}`)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp gen.ProbeProviderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "a failed completion must keep Finish disabled (FR-029)")
	require.NotNil(t, resp.Error)
	assert.NotContains(t, *resp.Error, "signed in to",
		"the Copilot login was never checked, so the message must not assert it")
	assert.Contains(t, *resp.Error, "could not confirm",
		"the message must say the sign-in is unconfirmed")
	assert.Contains(t, *resp.Error, "claude-opus-4.7", "the message must still name the model that failed")
	assert.NotContains(t, *resp.Error, "not signed in",
		"never send the operator to re-authenticate as a certainty over a model problem")
	// SEC-16: the vendor's raw stderr stays a server-debug detail.
	assert.NotContains(t, w.Body.String(), "not enabled for your plan")
}
