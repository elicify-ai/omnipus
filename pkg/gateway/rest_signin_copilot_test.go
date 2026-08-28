// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
)

// ADR-068 FR-008/FR-009 for `github-copilot` (T068-15).
//
// The GitHub Copilot CLI is the whole integration: it holds the login, and
// Omnipus never performs or stores it. These tests drive the two sign-in routes
// over a FAKE `copilot` placed on PATH, replaying each state the shipped CLI can
// produce — including the verified no-credential stderr of @github/copilot
// 1.0.80.

// putFakeCopilotOnPath writes a stand-in `copilot` into a fresh directory and
// makes that directory the whole PATH, so exec.LookPath finds it and nothing
// else. An empty stderr with exit 0 is the "signed in" case.
func putFakeCopilotOnPath(t *testing.T, stdout, stderr string, exitCode int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI uses a #!/bin/bash shebang with no Windows equivalent (see #113)")
	}
	dir := t.TempDir()
	body := "#!/bin/bash\n"
	if stdout != "" {
		body += "cat <<'OMNIPUS_EOF'\n" + stdout + "\nOMNIPUS_EOF\n"
	}
	if stderr != "" {
		body += "cat >&2 <<'OMNIPUS_EOF'\n" + stderr + "\nOMNIPUS_EOF\n"
	}
	body += "exit " + strconv.Itoa(exitCode) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "copilot"), []byte(body), 0o755))
	t.Setenv("PATH", dir)
}

// clearCopilotFromPath points PATH at an empty directory: the CLI is not
// installed on this machine.
func clearCopilotFromPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func adminRequest(t *testing.T, api *restAPI, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	ctx := context.WithValue(req.Context(), UserContextKey{}, &config.UserConfig{Username: "admin"})
	ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
	return w
}

// TestSignInStatus_Copilot pins the FR-009 state mapping for github-copilot.
func TestSignInStatus_Copilot(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"

	// The exact stderr @github/copilot 1.0.80 writes with no credential,
	// captured by running the published binary.
	const realNotSignedInStderr = `Error: No authentication information found.

Copilot can be authenticated with GitHub using an OAuth Token or a Fine-Grained Personal Access Token.

To authenticate, you can use any of the following methods:
  * Start 'copilot' and run the '/login' command
  * Set the COPILOT_GITHUB_TOKEN, GH_TOKEN, or GITHUB_TOKEN environment variable
  * Run 'gh auth login' to authenticate with the GitHub CLI`

	cases := []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int
		want     gen.SignInStatusState
	}{
		{"signed in", "ok", "", 0, gen.SignInStatusStateSignedIn},
		{"not signed in", "", realNotSignedInStderr, 1, gen.SignInStatusStateNotSignedIn},
		{"expired session", "", "Error: your Copilot session has expired. Run `copilot login` again.", 1, gen.SignInStatusStateExpired},
		{"unreadable failure degrades to not_signed_in", "", "Error: something unexpected", 1, gen.SignInStatusStateNotSignedIn},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newAuthMethodOnboardingAPI(t)
			putFakeCopilotOnPath(t, tc.stdout, tc.stderr, tc.exitCode)

			w := adminRequest(t, api, http.MethodGet, path)
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

			var got gen.SignInStatus
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			assert.Equal(t, tc.want, got.State)
			assert.True(t, got.State.Valid(), "state %q is off the contract enum", got.State)
			// Omnipus never holds or decodes the Copilot token, so no expiry is
			// ever reported for this cli_login provider (FR-009).
			assert.Nil(t, got.ExpiresAt)
		})
	}

	t.Run("cli missing on this machine", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		clearCopilotFromPath(t)

		w := adminRequest(t, api, http.MethodGet, path)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var got gen.SignInStatus
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		// `cli_missing` has no wire state: the enum is
		// not_signed_in|pending|signed_in|expired and the machine fact is
		// reported on the provider ROW instead.
		assert.Equal(t, gen.SignInStatusStateNotSignedIn, got.State)
		assert.Nil(t, got.AccountLabel)
	})

	// ADR-068 FR-050 (T068-14, decided here for T068-15's Copilot route):
	// github-copilot's GET .../sign-in/status is textually one of FR-050's
	// five sign-in routes (POST /providers/{id}/sign-in, GET
	// .../sign-in/status, POST .../sign-in/poll, POST
	// openai-chatgpt/sign-in/import, DELETE .../sign-in) — the FR-050 gate
	// in HandleProviders' dispatch (rest.go) matches by METHOD + PATH
	// SUFFIX only, never by provider id, so it was never possible for this
	// route to be excluded from that set. It belongs in it. The prior
	// "unauthenticated is 401" expectation predates FR-050 (T068-15 was
	// written before T068-14) and additionally never injected a config
	// snapshot into the request context at all, so it was not even
	// exercising the intended codepath either before or after this change —
	// with no snapshot, RequireNotBypass fails closed to 503 regardless of
	// onboarding state (same defect TestProviderSignInRoutes_AuthGating
	// fixed for the generic routes). Replaced with the FR-050-correct
	// pre/post-onboarding transition, using the same pattern.
	t.Run("onboarding incomplete, unauthenticated, no bypass -> reachable (FR-050)", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		putFakeCopilotOnPath(t, "ok", "", 0)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
		w := httptest.NewRecorder()
		api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
		assert.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	})

	t.Run("onboarding complete, unauthenticated -> 401 (FR-050)", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		require.NoError(t, api.onboardingMgr.CompleteOnboarding())
		req := httptest.NewRequest(http.MethodGet, path, nil)
		ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
		w := httptest.NewRecorder()
		api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("dev-mode bypass is 503", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		bypassCfg := *api.agentLoop.GetConfig()
		bypassCfg.Gateway.DevModeBypass = true
		req := httptest.NewRequest(http.MethodGet, path, nil)
		ctx := context.WithValue(req.Context(), UserContextKey{}, &config.UserConfig{Username: "admin"})
		ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, &bypassCfg)
		w := httptest.NewRecorder()
		api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})
}

// TestSignInStart_Copilot pins FR-008: Omnipus hands back the vendor CLI's own
// login command and never runs it.
func TestSignInStart_Copilot(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)

	w := adminRequest(t, api, http.MethodPost, "/api/v1/providers/github-copilot/sign-in")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got gen.SignInStartResponseCliLogin
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, gen.CliLogin, got.Method)
	assert.True(t, got.Method.Valid())
	assert.Equal(t, "copilot login", got.Command)
	assert.Contains(t, got.Instructions, "Check sign-in")

	// No device-code fields may leak onto the cli_login variant.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	for _, forbidden := range []string{"device_auth_id", "user_code", "verification_url", "interval_seconds"} {
		assert.NotContains(t, raw, forbidden)
	}
}

// TestSignIn_CopilotDispatchDoesNotLeakToOtherProviders replaces
// TestSignInStatus_CopilotOtherProvidersStillStubbed: T068-14 implemented
// real handlers for codex-cli and openai-chatgpt (previously the honest 501
// stub this test pinned), so "other ids stay stubbed" is obsolete by
// design. The still-valid coverage — the github-copilot special case in
// HandleProviders' dispatch (rest.go) does not leak its response onto other
// provider ids — is kept, now proven by each id getting ITS OWN correctly
// shaped response rather than Copilot's.
func TestSignIn_CopilotDispatchDoesNotLeakToOtherProviders(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)

	w := adminRequest(t, api, http.MethodPost, "/api/v1/providers/codex-cli/sign-in")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var codexStart gen.SignInStartResponseCliLogin
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &codexStart))
	assert.Equal(t, gen.CliLogin, codexStart.Method)
	assert.Equal(t, "codex login", codexStart.Command)
	assert.NotEqual(t, "copilot login", codexStart.Command,
		"codex-cli must never get github-copilot's command")

	w = adminRequest(t, api, http.MethodGet, "/api/v1/providers/codex-cli/sign-in/status")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var codexStatus gen.SignInStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &codexStatus))
	assert.True(t, codexStatus.State.Valid())

	state := "pending"
	server := httptest.NewServer(deviceCodeVendorMux(t, &state))
	defer server.Close()
	withDeviceCodeVendor(t, server)

	w = adminRequest(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var chatgptResp gen.SignInStartResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &chatgptResp))
	disc, err := chatgptResp.Discriminator()
	require.NoError(t, err)
	assert.Equal(t, "device_code", disc,
		"openai-chatgpt must get its own device_code response, never github-copilot's cli_login shape")
}

// TestSignInStatus_CopilotRowHint covers the FR-009 provider-row half: with the
// CLI absent the row reports disconnected with the operator hint, and it never
// claims anything when the CLI is present.
func TestSignInStatus_CopilotRowHint(t *testing.T) {
	t.Run("missing cli yields the hint", func(t *testing.T) {
		clearCopilotFromPath(t)
		assert.Equal(t, providers_pkg.CopilotCLIMissingHint, copilotRowHint("github-copilot"))
		assert.Contains(t, copilotRowHint("github-copilot"), "not found on this machine")
	})

	t.Run("installed cli yields no hint", func(t *testing.T) {
		putFakeCopilotOnPath(t, "ok", "", 0)
		assert.Empty(t, copilotRowHint("github-copilot"))
	})

	t.Run("other providers never get the hint", func(t *testing.T) {
		clearCopilotFromPath(t)
		for _, id := range []string{"codex-cli", "openai", "openai-chatgpt", ""} {
			assert.Empty(t, copilotRowHint(id), "id=%q", id)
		}
	})
}

// TestSignInStatus_CopilotRowReportsDisconnected drives the hint through
// GET /api/v1/providers so the operator actually sees it on the row.
func TestSignInStatus_CopilotRowReportsDisconnected(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)

	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Name:       "copilot",
		Model:      "claude-sonnet-4.6",
		Provider:   "github-copilot",
		AuthMethod: config.AuthMethodSignIn,
	})

	clearCopilotFromPath(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	ctx := context.WithValue(req.Context(), UserContextKey{}, &config.UserConfig{Username: "admin"})
	ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var rows []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))

	var row *gen.Provider
	for i := range rows {
		if rows[i].Id == "github-copilot" {
			row = &rows[i]
		}
	}
	require.NotNil(t, row, "github-copilot row missing from %s", w.Body.String())
	assert.Equal(t, gen.ProviderStatusDisconnected, row.Status)
	require.NotNil(t, row.Error, "the row must carry the operator hint")
	assert.True(t, strings.Contains(*row.Error, "not found on this machine"),
		"error = %q, want the missing-CLI hint", *row.Error)
	assert.Equal(t, gen.ProviderAuthMethodSignIn, row.AuthMethod)
}
