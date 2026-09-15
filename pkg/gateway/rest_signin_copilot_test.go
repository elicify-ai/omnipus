// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	writeFakeCopilotBinary(t, dir, "", stdout, stderr, exitCode)
	t.Setenv("PATH", dir)
}

// writeFakeCopilotBinary writes a `copilot` stand-in into dir that prints
// stdout and then stderr (each followed by one newline, when non-empty) and
// exits with exitCode. preamble is bash run before that, e.g. an invocation
// tally.
//
// The stand-in uses bash builtins only, because dir is the WHOLE PATH its
// callers give the handler, and an external command such as `cat` is "command
// not found" there. An earlier version printed with `cat`: it printed nothing
// it was given, and bash's own complaint — which quotes the script's path,
// random temp directory name included — became the CLI's stderr. The sign-in
// classifier then read that path, so a directory name containing "401" (an
// expired-session marker) turned "not signed in" into "expired" (CI
// ubuntu-latest, 2026-09-15), while the "expired session" case only ever
// passed because its directory is named after the subtest.
//
// The text lives in files outside dir, and every stand-in is run once under
// PATH=dir before it is handed over, so one that does not print exactly what
// it was given fails here instead of as a wrong state further down.
func writeFakeCopilotBinary(t *testing.T, dir, preamble, stdout, stderr string, exitCode int) {
	t.Helper()
	data := t.TempDir()
	outputs := ""
	for _, out := range []struct{ name, text, redirect string }{
		{name: "stdout", text: stdout},
		{name: "stderr", text: stderr, redirect: " >&2"},
	} {
		if out.text == "" {
			continue
		}
		require.Falsef(t, strings.HasSuffix(out.text, "\n"),
			"fake copilot %s must not end in a newline: the stand-in adds exactly one", out.name)
		file := filepath.Join(data, out.name)
		require.NotContainsf(t, file, "'", "fake copilot data path %q cannot be single-quoted", file)
		require.NoError(t, os.WriteFile(file, []byte(out.text), 0o600))
		// "$(<file)" is bash reading the file itself — no `cat`, no PATH lookup.
		outputs += "printf '%s\\n' \"$(<'" + file + "')\"" + out.redirect + "\n"
	}
	outputs += "exit " + strconv.Itoa(exitCode) + "\n"

	script := filepath.Join(dir, "copilot")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/bash\n"+outputs), 0o755))
	verifyFakeCopilotBinary(t, dir, stdout, stderr, exitCode)
	if preamble != "" {
		// Checked above WITHOUT the preamble, so a tally preamble never counts
		// the check itself as an invocation.
		require.NoError(t, os.WriteFile(script, []byte("#!/bin/bash\n"+preamble+outputs), 0o755))
	}
}

// verifyFakeCopilotBinary runs dir's stand-in with PATH=dir — the PATH the
// handler under test sees — and requires the exact output and exit code.
func verifyFakeCopilotBinary(t *testing.T, dir, stdout, stderr string, exitCode int) {
	t.Helper()
	withNewline := func(s string) string {
		if s == "" {
			return ""
		}
		return s + "\n"
	}
	cmd := exec.Command(filepath.Join(dir, "copilot"))
	cmd.Env = []string{"PATH=" + dir}
	var gotOut, gotErr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &gotOut, &gotErr
	gotExit := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		require.Truef(t, errors.As(err, &exitErr), "run the fake copilot under PATH=%s: %v", dir, err)
		gotExit = exitErr.ExitCode()
	}
	require.Equalf(t, withNewline(stdout), gotOut.String(), "fake copilot stdout under PATH=%s", dir)
	require.Equalf(t, withNewline(stderr), gotErr.String(), "fake copilot stderr under PATH=%s", dir)
	require.Equalf(t, exitCode, gotExit, "fake copilot exit code under PATH=%s", dir)
}

// installFakeCopilot writes a stand-in `copilot` into dir, then runs it once
// with PATH narrowed to dir — the harshest environment any caller creates —
// and fails the test unless it writes EXACTLY its scripted stdout and stderr
// and exits with the scripted code. A fixture that cannot print its own
// message must fail here, loudly, rather than hand the classifier something
// else and let the fallback turn it green.
//
// With a tally file, the same run is the positive control for the counter: it
// must record exactly one invocation, and the tally is then removed so the
// test's own count starts from zero. Any tally left by an earlier install is
// cleared first, so re-installing mid-test resets the count.
func installFakeCopilot(t *testing.T, dir, stdout, stderr string, exitCode int, tally string) {
	t.Helper()
	if !hasBash() {
		t.Skip("fake CLI uses a #!/bin/bash shebang with no Windows equivalent (see #113)")
	}
	if tally != "" {
		if err := os.Remove(tally); err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
	}
	script := filepath.Join(dir, "copilot")
	require.NoError(t, os.WriteFile(script, []byte(fakeCopilotScript(stdout, stderr, exitCode, tally)), 0o755))

	cmd := exec.Command(script)
	cmd.Env = append(os.Environ(), "PATH="+dir)
	var gotOut, gotErr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &gotOut, &gotErr
	gotCode := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		require.True(t, errors.As(err, &exitErr), "the fake copilot could not be run at all: %v", err)
		gotCode = exitErr.ExitCode()
	}
	require.Equal(t, strings.TrimSpace(stderr), strings.TrimSpace(gotErr.String()),
		"the fake copilot must write exactly its scripted stderr with PATH=%s", dir)
	require.Equal(t, strings.TrimSpace(stdout), strings.TrimSpace(gotOut.String()),
		"the fake copilot must write exactly its scripted stdout with PATH=%s", dir)
	require.Equal(t, exitCode, gotCode, "the fake copilot must exit with its scripted code")

	if tally != "" {
		require.Equal(t, 1, countInvocations(t, tally),
			"positive control: one run of the counting fake must record exactly one invocation")
		require.NoError(t, os.Remove(tally))
	}
}

// unrecognisedCopilotMessageLog is the fragment of the warning
// providers.CopilotSignIn logs when a failed check matched no marker and fell
// back to not_signed_in. Its ABSENCE is how a test proves the classifier
// recognised the scripted message on purpose.
const unrecognisedCopilotMessageLog = "unrecognised message"

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
		// recognised: a failed check whose scripted stderr the classifier
		// must match ON PURPOSE. false means the unrecognised-message
		// fallback is itself the behaviour under test. Ignored for exit 0,
		// which never reaches the classifier.
		recognised bool
	}{
		{"signed in", "ok", "", 0, gen.SignInStatusStateSignedIn, false},
		{"not signed in", "", realNotSignedInStderr, 1, gen.SignInStatusStateNotSignedIn, true},
		// The verified error line on its own. The full message above also
		// carries the '/login' guidance, which a second marker matches, so only
		// this row proves the "No authentication information found" marker
		// itself is exercised: remove that marker and this row reaches the
		// fallback.
		{"not signed in, error line only", "", "Error: No authentication information found.", 1, gen.SignInStatusStateNotSignedIn, true},
		// Named without any marker word: the neutral fake directory keeps the
		// name out of the path, and this row must pass on its message alone.
		{"session rejected by the vendor", "", "Error: your Copilot session has expired. Run `copilot login` again.", 1, gen.SignInStatusStateExpired, true},
		{"unreadable failure degrades to not_signed_in", "", "Error: something unexpected", 1, gen.SignInStatusStateNotSignedIn, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newAuthMethodOnboardingAPI(t)
			putFakeCopilotOnPath(t, tc.stdout, tc.stderr, tc.exitCode)
			logs := captureSlog(t)

			w := adminRequest(t, api, http.MethodGet, path)
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

			var got gen.SignInStatus
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			assert.Equal(t, tc.want, got.State)
			assert.True(t, got.State.Valid(), "state %q is off the contract enum", got.State)
			// Omnipus never holds or decodes the Copilot token, so no expiry is
			// ever reported for this cli_login provider (FR-009).
			assert.Nil(t, got.ExpiresAt)

			// The state alone cannot tell "recognised" from "fell back to
			// not_signed_in", so pin which path the classifier took.
			if tc.exitCode != 0 {
				assert.NotContains(t, logs.String(), "command not found",
					"the classifier was handed a fixture failure, not the scripted message")
				if tc.recognised {
					assert.NotContains(t, logs.String(), unrecognisedCopilotMessageLog,
						"the scripted stderr must be recognised on purpose, not reach the fallback; logs=%s", logs.String())
				} else {
					assert.Contains(t, logs.String(), unrecognisedCopilotMessageLog,
						"an unrecognised message must take the logged fallback; logs=%s", logs.String())
					assert.Contains(t, logs.String(), fmt.Sprintf("detail=%q", tc.stderr),
						"the fallback must have been handed exactly the scripted stderr; logs=%s", logs.String())
				}
			}
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
	assert.Equal(t, gen.SignInStartResponseCliLoginMethodCliLogin, got.Method)
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
	assert.Equal(t, gen.SignInStartResponseCliLoginMethodCliLogin, codexStart.Method)
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

// neutralFakeCLIDir creates a directory for a fake vendor CLI whose own name
// carries neither the test's name nor a decimal number. t.TempDir() embeds
// both, and both once decided a Copilot sign-in result: the subtest named
// "expired session" put "expired" into the path, and a random suffix
// containing "401" did the same for not_signed_in. The suffix here is base32
// (A–Z, 2–7), which can never spell 401, and is re-drawn in the
// vanishingly rare case it spells "expired".
//
// The directory sits under os.TempDir(), so a run whose TMPDIR itself contains
// such text still exercises the case that matters: the classifier must be
// handed the fake's scripted message, never its path.
func neutralFakeCLIDir(t *testing.T) string {
	t.Helper()
	name := "omnipus-fakecli-" + rand.Text()
	for strings.Contains(strings.ToLower(name), "expired") {
		name = "omnipus-fakecli-" + rand.Text()
	}
	dir := filepath.Join(os.TempDir(), name)
	require.NoError(t, os.Mkdir(dir, 0o700))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// fakeCopilotScript is the body of a stand-in `copilot`. It uses bash
// BUILT-INS ONLY (printf, echo, redirection), because every caller makes the
// fake's own directory the whole PATH. An external command such as `cat` or
// `touch` is then "command not found": the scripted text is never written, and
// bash's own error line — which carries the fake's temp path — reaches the
// sign-in classifier instead. That is how a random "401" in a temp folder name
// turned not_signed_in into expired on CI (commit 829e26253, go-race gate).
//
// tally, when non-empty, is a file the fake appends one line to per run.
func fakeCopilotScript(stdout, stderr string, exitCode int, tally string) string {
	body := "#!/bin/bash\n"
	if tally != "" {
		body += "echo x >> " + fakeCopilotShellQuote(tally) + "\n"
	}
	if stdout != "" {
		body += "printf '%s\\n' " + fakeCopilotShellQuote(stdout) + "\n"
	}
	if stderr != "" {
		body += "printf '%s\\n' " + fakeCopilotShellQuote(stderr) + " >&2\n"
	}
	body += "exit " + strconv.Itoa(exitCode) + "\n"
	return body
}

// fakeCopilotShellQuote single-quotes s for bash, so scripted CLI text with
// quotes, backticks or dollar signs is written verbatim.
func fakeCopilotShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
