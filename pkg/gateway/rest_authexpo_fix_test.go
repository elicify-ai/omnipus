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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ---------------------------------------------------------------------------
// Shared scaffolding
// ---------------------------------------------------------------------------

// completeOnboarding marks the instance onboarded through the real manager, so
// the test exercises the same state.json the production gate reads rather than
// a stubbed boolean.
func completeOnboarding(t *testing.T, api *restAPI) {
	t.Helper()
	require.NotNil(t, api.onboardingMgr, "fixture must carry a real onboarding manager")
	require.NoError(t, api.onboardingMgr.CompleteOnboarding())
	require.True(t, api.onboardingMgr.IsComplete(), "onboarding must read back as complete")
}

// signInProviderRow appends a configured sign_in provider row carrying a live
// OAuth credential, so the list branch has something whose account_label it
// could leak.
func signInProviderRow(t *testing.T, api *restAPI, providerID, accountID string) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Name:       providerID,
		Model:      "gpt-5",
		Provider:   providerID,
		AuthMethod: config.AuthMethodSignIn,
	})
	// Written through the same entry-name function the production reader uses
	// (credentials.OAuthEntryName + providers.OAuthVendorID), so the fixture
	// cannot drift from cheapSignInRowStatus's lookup.
	raw, err := json.Marshal(map[string]any{
		"access_token": "at-secret",
		"account_id":   accountID,
		"expires_at":   time.Now().Add(1 * time.Hour).Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	require.NoError(t, api.credStore.Set(
		credentials.OAuthEntryName(providers.OAuthVendorID(providerID)), string(raw)))
}

// getProviders issues GET /api/v1/providers. When user is non-empty the caller
// is authenticated as that username; when it is empty the caller is anonymous.
func listProvidersAs(t *testing.T, api *restAPI, user, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	if user != "" {
		ctx = context.WithValue(ctx, UserContextKey{}, &config.UserConfig{Username: user})
	}
	w := httptest.NewRecorder()
	api.HandleProviders(w, req.WithContext(ctx))
	return w
}

// ---------------------------------------------------------------------------
// C1 — GET /api/v1/providers was unauthenticated forever
// ---------------------------------------------------------------------------

// TestProviderList_RequiresAuthOnceOnboarded is the direct regression test for
// C1. Before the fix the `GET sub == ""` branch of HandleProviders carried no
// authorization gate of any kind: the route is registered withOptionalAuth
// (anonymous callers pass straight through) and, unlike PUT / /test / the five
// FR-050 sign-in routes / DELETE, this branch never checked anything. On a
// fully onboarded production gateway `curl http://host:5000/api/v1/providers`
// with no credentials returned 200 and the whole provider inventory.
//
// contracts/openapi.yaml has always declared `security: [BearerAuth: []]` and a
// 401 response for listProviders, so this is also the code/contract agreement.
func TestProviderList_RequiresAuthOnceOnboarded(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	completeOnboarding(t, api)

	w := listProvidersAs(t, api, "", "")

	require.Equal(t, http.StatusUnauthorized, w.Code,
		"an anonymous list on an onboarded gateway must be 401; body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "authentication required", body["error"])
}

// TestProviderList_AuthenticatedCallerStillListsAfterOnboarding pins the other
// half of the gate: the fix must not lock the Settings screen out of the list
// it re-reads on every provider edit.
func TestProviderList_AuthenticatedCallerStillListsAfterOnboarding(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	completeOnboarding(t, api)

	w := listProvidersAs(t, api, "admin", "")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var rows []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
}

// TestProviderList_AnonymousNeverSeesAccountLabel is the second C1 proof: the
// operator's live vendor account identifier must never appear in a response to
// an unauthenticated caller. Commit 9a7c5ae8 wired cheapSignInRowStatus into
// the list branch and populated AccountLabel from it — Provider.yaml documents
// that field as "account identifier of the signed-in session", i.e. the
// operator's own ChatGPT/xAI identity.
//
// The assertion is made three ways on purpose: the typed field, the raw JSON
// key, and a substring scan of the whole body — so a future change that moves
// the value onto a different field still fails this test.
func TestProviderList_AnonymousNeverSeesAccountLabel(t *testing.T) {
	const secretAccount = "operator@example.com"

	api, _ := newAuthMethodOnboardingAPI(t)
	signInProviderRow(t, api, "openai-chatgpt", secretAccount)

	// Onboarding deliberately left INCOMPLETE and no users configured: this is
	// the FR-050 pre-auth window, the one state in which an anonymous caller
	// still reaches the list at all. Even here the label must not appear.
	w := listProvidersAs(t, api, "", "")
	require.Equal(t, http.StatusOK, w.Code,
		"the pre-auth window must still answer the wizard; body=%s", w.Body.String())

	body := w.Body.String()
	assert.NotContains(t, body, secretAccount,
		"the operator's vendor account id must not appear anywhere in an unauthenticated response")
	assert.NotContains(t, body, "account_label",
		"an unauthenticated response must not carry the account_label key at all")

	var rows []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.NotEmpty(t, rows, "the fixture configured one provider row")
	for _, row := range rows {
		assert.Nil(t, row.AccountLabel, "row %q leaked an account_label", row.Id)
	}

	// Control: the SAME instance and the SAME row does surface the label to an
	// authenticated caller, so the test above is proving redaction rather than
	// an unpopulated fixture.
	authedRows := []gen.Provider{}
	wAuth := listProvidersAs(t, api, "admin", "")
	require.Equal(t, http.StatusOK, wAuth.Code, "body=%s", wAuth.Body.String())
	require.NoError(t, json.Unmarshal(wAuth.Body.Bytes(), &authedRows))
	var sawLabel bool
	for _, row := range authedRows {
		if row.AccountLabel != nil && *row.AccountLabel == secretAccount {
			sawLabel = true
		}
	}
	assert.True(t, sawLabel,
		"an authenticated caller must still get account_label — otherwise the redaction test proves nothing")
}

// TestProviderList_AnonymousNeverSeesDependents pins the other reduction: the
// dependents array names every agent bound to a provider, i.e. the operator's
// roster. Provider.yaml requires the field, so the anonymous answer is the
// empty array (contract-valid), never the real list.
func TestProviderList_AnonymousNeverSeesDependents(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Name: "openai", Model: "gpt-5", Provider: "openai", APIKeyRef: "openai_API_KEY",
	})
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID: "secret-recruiter", Name: "Secret Recruiter",
		Model: &config.AgentModelConfig{Primary: "gpt-5", Provider: "openai"},
	})

	// Authenticated control first: the dependent IS computed for this fixture.
	wAuth := listProvidersAs(t, api, "admin", "")
	require.Equal(t, http.StatusOK, wAuth.Code, "body=%s", wAuth.Body.String())
	assert.Contains(t, wAuth.Body.String(), "secret-recruiter",
		"an authenticated caller must see dependents — otherwise the redaction test proves nothing")

	w := listProvidersAs(t, api, "", "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "secret-recruiter",
		"an unauthenticated response must not enumerate the operator's agents")

	var rows []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.NotNil(t, row.Dependents,
			"Provider.yaml requires dependents: the anonymous answer is [], never a missing key")
		assert.Empty(t, row.Dependents, "row %q leaked dependents to an anonymous caller", row.Id)
	}
}

// TestProviderList_AnonymousIsRateLimited proves the C1 impact statement's
// last clause ("No rate limit applies either") is closed. The list fans out to
// one upstream /models fetch per configured provider, so an unauthenticated
// caller inside the pre-auth window must not be able to drive it without a
// ceiling. A distinct RemoteAddr keeps this test's bucket out of every other
// test's.
func TestProviderList_AnonymousIsRateLimited(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	const attacker = "198.51.100.31:40000"

	var lastCode int
	for i := 0; i < 61; i++ {
		lastCode = listProvidersAs(t, api, "", attacker).Code
		if lastCode == http.StatusTooManyRequests {
			break
		}
	}
	require.Equal(t, http.StatusTooManyRequests, lastCode,
		"an anonymous caller must be rate limited within 61 requests")

	// An AUTHENTICATED caller from the same address is unaffected — the
	// limiter is scoped to the anonymous path only.
	assert.Equal(t, http.StatusOK, listProvidersAs(t, api, "admin", attacker).Code,
		"the anonymous ceiling must not lock out an authenticated caller")
}

// TestProviderList_EnvBearerTokenCallerIsNotLockedOut guards the C1 gate
// against over-reach. withOptionalAuth's legacy OMNIPUS_BEARER_TOKEN branch
// calls the handler with NO UserContextKey on a successful match, so a naive
// "no user in context => 401" gate would refuse the documented headless/CI
// deployment mode (an env token with no Gateway.Users rows) on a route that
// every ordinary withAuth endpoint serves it happily.
//
// The wrong token must still be refused, so this pins both directions.
func TestProviderList_EnvBearerTokenCallerIsNotLockedOut(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	completeOnboarding(t, api)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "headless-ci-token")

	listWithAuthHeader := func(header string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		req = req.WithContext(context.WithValue(req.Context(),
			ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))
		w := httptest.NewRecorder()
		api.HandleProviders(w, req)
		return w
	}

	assert.Equal(t, http.StatusOK, listWithAuthHeader("Bearer headless-ci-token").Code,
		"an env-token principal is authenticated and must be served")
	assert.Equal(t, http.StatusUnauthorized, listWithAuthHeader("Bearer wrong-token").Code,
		"a non-matching bearer token must not pass the gate")
	assert.Equal(t, http.StatusUnauthorized, listWithAuthHeader("").Code,
		"no credential at all must still be 401")
}

// ---------------------------------------------------------------------------
// M3 — the FR-050 gate failed OPEN
// ---------------------------------------------------------------------------

// TestPreAuthWindow_ClosedWhenOnboardingStateUnknown is the M3 regression
// test. onboarding.NewManager keeps OnboardingComplete=false on ANY load
// failure and renames an unparseable state.json aside, so a corrupt file on a
// long-onboarded instance silently reopened all five FR-050 sign-in routes —
// DELETE (destroys the OAuth grant) and import (writes the credential store)
// included — unauthenticated, for the whole process lifetime, after one WARN.
//
// The fix samples the file's readability BEFORE the manager consumes it and
// treats "unknown" as closed. The manager here honestly reports incomplete
// (that is the failure mode being modelled); only onboardingStateUnknown
// distinguishes it from a real fresh install.
func TestPreAuthWindow_ClosedWhenOnboardingStateUnknown(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	require.False(t, api.onboardingMgr.IsComplete(),
		"precondition: the manager reports the corrupt-state instance as a fresh install")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	req = req.WithContext(context.WithValue(req.Context(),
		ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))

	assert.True(t, api.preAuthOnboardingWindowOpen(req),
		"precondition: with a readable state the window is open (this is the FR-050 case)")

	api.onboardingStateUnknown = true
	assert.False(t, api.preAuthOnboardingWindowOpen(req),
		"an unknown onboarding state must NOT be treated as a fresh install")

	w := httptest.NewRecorder()
	api.HandleProviders(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"with the state unknown, an anonymous provider read must 401; body=%s", w.Body.String())
}

// TestOnboardingStateUnreadable_ClassifiesEachCase pins the three inputs of the
// boot-time sample that feeds onboardingStateUnknown. Getting the MISSING case
// wrong would break every genuine first launch, so it is asserted explicitly
// rather than left implied.
func TestOnboardingStateUnreadable_ClassifiesEachCase(t *testing.T) {
	t.Run("missing file is a genuine fresh install", func(t *testing.T) {
		assert.False(t, onboardingStateUnreadable(t.TempDir()))
	})

	t.Run("valid JSON is known", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, "system", "state.json"),
			[]byte(`{"version":1,"onboarding_complete":true}`), 0o600))
		assert.False(t, onboardingStateUnreadable(home))
	})

	t.Run("unparseable JSON is unknown", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, "system", "state.json"),
			[]byte(`{"version":1,`), 0o600))
		assert.True(t, onboardingStateUnreadable(home),
			"a truncated state.json must be unknown, not a fresh install")
	})

	t.Run("unreadable file is unknown", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		// A DIRECTORY where the file belongs: os.ReadFile fails with a
		// non-IsNotExist error on every platform, unlike a chmod 000 file,
		// which root can still read.
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system", "state.json"), 0o700))
		assert.True(t, onboardingStateUnreadable(home),
			"a state path that cannot be read must be unknown, not a fresh install")
	})
}

// TestPreAuthWindow_ClosedWhenAnAuthenticationAuthorityExists pins the second
// M3 signal, the one a corrupt state.json cannot erase. FR-050 exists because
// there is no admin account to authenticate as yet; if somebody CAN
// authenticate here, that premise is false whatever state.json says.
func TestPreAuthWindow_ClosedWhenAnAuthenticationAuthorityExists(t *testing.T) {
	t.Run("a configured user closes the window", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		cfg := api.agentLoop.GetConfig()
		cfg.Gateway.Users = []config.UserConfig{{Username: "admin", PasswordHash: "x"}}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))

		assert.False(t, api.preAuthOnboardingWindowOpen(req))
		w := httptest.NewRecorder()
		api.HandleProviders(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})

	t.Run("OMNIPUS_BEARER_TOKEN closes the window", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "env-token")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		req = req.WithContext(context.WithValue(req.Context(),
			ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))

		assert.False(t, api.preAuthOnboardingWindowOpen(req))
	})
}

// ---------------------------------------------------------------------------
// C2 — the Copilot probe was pollable and billed to the operator
// ---------------------------------------------------------------------------

// TestCopilotProbe_SignedInResultIsCachedNotReprobed is the C2 billing proof.
// The probe execs `copilot -p ... --allow-all-tools --no-ask-user`, which
// CopilotSignIn's own doc comment says "costs one premium request when the
// operator is signed in ... and MUST NOT be put on a poll or a page-load
// path"; it nonetheless sat on an FR-050 pre-auth route behind a 60/min
// per-IP ceiling, i.e. 3,600 premium requests/hour billed to the operator.
//
// The fake CLI counts its own invocations, so this asserts the number of
// VENDOR EXECS, not merely the number of 200s.
func TestCopilotProbe_SignedInResultIsCachedNotReprobed(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"
	api, _ := newAuthMethodOnboardingAPI(t)
	counter := putCountingCopilotOnPath(t, "ok", "", 0)

	for i := 0; i < 20; i++ {
		w := adminRequest(t, api, http.MethodGet, path)
		require.Equal(t, http.StatusOK, w.Code, "call %d body=%s", i, w.Body.String())
		var got gen.SignInStatus
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.Equal(t, gen.SignInStatusStateSignedIn, got.State,
			"call %d must still report the real state", i)
	}

	assert.Equal(t, 1, countInvocations(t, counter),
		"20 status calls against a signed-in operator must spend exactly ONE premium request")
}

// TestCopilotProbe_NotSignedInIsNeverCached pins the correctness half of the
// cache: the transition an operator actually waits on (run `copilot login`,
// click Check sign-in) must never be answered from a stale negative. That is
// also why the cache is safe — a not_signed_in probe spends no premium
// request, so re-running it costs nothing.
func TestCopilotProbe_NotSignedInIsNeverCached(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"
	api, _ := newAuthMethodOnboardingAPI(t)

	dir := t.TempDir()
	writeFakeCopilot(t, dir, "", "Error: No authentication information found.", 1)
	t.Setenv("PATH", dir)

	w := adminRequest(t, api, http.MethodGet, path)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var first gen.SignInStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))
	require.Equal(t, gen.SignInStatusStateNotSignedIn, first.State)

	// The operator now signs in: the SAME binary path starts reporting success.
	writeFakeCopilot(t, dir, "ok", "", 0)

	w2 := adminRequest(t, api, http.MethodGet, path)
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	var second gen.SignInStatus
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &second))
	assert.Equal(t, gen.SignInStatusStateSignedIn, second.State,
		"the sign-in transition must be visible immediately, never masked by a cached negative")
}

// TestCopilotProbe_ConcurrentCallsAreRefusedNotSpawned is the C2 process-spawn
// proof: up to ~60 concurrent `copilot` children could be alive at once if the
// CLI hung to its 60s timeout. Only one probe may be in flight; the rest are
// refused with 429 rather than queued behind a held request.
func TestCopilotProbe_ConcurrentCallsAreRefusedNotSpawned(t *testing.T) {
	api := &restAPI{}

	require.True(t, api.copilotProbe.acquire(), "the first probe must claim the slot")
	assert.False(t, api.copilotProbe.acquire(),
		"a second concurrent probe must be refused, never spawned alongside the first")

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/providers/github-copilot/sign-in/status", nil)
	w := httptest.NewRecorder()
	api.handleCopilotSignInStatus(w, req)
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"a call arriving while a probe runs must be 429; body=%s", w.Body.String())
	assert.Equal(t, "5", w.Header().Get("Retry-After"),
		"a 429 must tell the caller when to come back")

	api.copilotProbe.release()
	assert.True(t, api.copilotProbe.acquire(), "the slot must be reusable after release")
}

// TestCopilotProbe_ConcurrentHTTPCallsSpawnOneVendorProcess drives the same
// guarantee through the real handler from many goroutines at once, counting
// actual vendor execs.
func TestCopilotProbe_ConcurrentHTTPCallsSpawnOneVendorProcess(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"
	api, _ := newAuthMethodOnboardingAPI(t)
	counter := putCountingCopilotOnPath(t, "ok", "", 0)
	cfg := api.agentLoop.GetConfig()

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			ctx := context.WithValue(req.Context(), UserContextKey{},
				&config.UserConfig{Username: "admin"})
			ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, cfg)
			api.HandleProviders(httptest.NewRecorder(), req.WithContext(ctx))
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, countInvocations(t, counter), 1,
		"25 concurrent status calls must never spawn more than one vendor process")
}

// writeFakeCopilot writes a `copilot` stand-in into dir. Split out of the
// existing putFakeCopilotOnPath so a test can REPLACE the binary mid-test
// (the sign-in transition) without swapping PATH.
func writeFakeCopilot(t *testing.T, dir, stdout, stderr string, exitCode int) {
	t.Helper()
	body := "#!/bin/bash\n"
	if stdout != "" {
		body += "cat <<'OMNIPUS_EOF'\n" + stdout + "\nOMNIPUS_EOF\n"
	}
	if stderr != "" {
		body += "cat >&2 <<'OMNIPUS_EOF'\n" + stderr + "\nOMNIPUS_EOF\n"
	}
	body += "exit " + strconv.Itoa(exitCode) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "copilot"), []byte(body), 0o755))
}

// putCountingCopilotOnPath installs a `copilot` stand-in that appends one line
// to a tally file per invocation, and returns that file's path. Counting execs
// rather than HTTP 200s is the point: the defect is about how many PREMIUM
// REQUESTS the operator is billed for, not how many responses are returned.
func putCountingCopilotOnPath(t *testing.T, stdout, stderr string, exitCode int) string {
	t.Helper()
	if !hasBash() {
		t.Skip("fake CLI uses a #!/bin/bash shebang with no Windows equivalent (see #113)")
	}
	dir := t.TempDir()
	tally := filepath.Join(dir, "invocations")
	body := "#!/bin/bash\necho x >> " + tally + "\n"
	if stdout != "" {
		body += "cat <<'OMNIPUS_EOF'\n" + stdout + "\nOMNIPUS_EOF\n"
	}
	if stderr != "" {
		body += "cat >&2 <<'OMNIPUS_EOF'\n" + stderr + "\nOMNIPUS_EOF\n"
	}
	body += "exit " + strconv.Itoa(exitCode) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "copilot"), []byte(body), 0o755))
	t.Setenv("PATH", dir)
	return tally
}

// countInvocations reports how many times the counting stand-in ran. A missing
// tally file means zero runs, not a failure.
func countInvocations(t *testing.T, tally string) int {
	t.Helper()
	data, err := os.ReadFile(tally)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(strings.Fields(string(data)))
}

func hasBash() bool {
	_, err := os.Stat("/bin/bash")
	return err == nil
}

// ---------------------------------------------------------------------------
// M5 — pre-auth sign-in probes produced no audit trail
// ---------------------------------------------------------------------------

// TestCopilotProbe_IsAudited proves M5 for the one sign-in emission that lives
// in this team's files. The C2 vendor traffic was previously invisible: the
// probe emitted nothing at all, so an operator billed for thousands of premium
// requests had no record of who drove them or from where.
func TestCopilotProbe_IsAudited(t *testing.T) {
	const path = "/api/v1/providers/github-copilot/sign-in/status"
	api, _ := newAuthMethodOnboardingAPI(t)
	putCountingCopilotOnPath(t, "ok", "", 0)
	auditDir := attachTestAuditor(t, api)

	// Anonymous, inside the FR-050 pre-auth window — the exact shape of the
	// C2 abuse traffic.
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "203.0.113.55:5555"
	req = req.WithContext(context.WithValue(req.Context(),
		ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))
	w := httptest.NewRecorder()
	api.HandleProviders(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	entries := readAuditEntries(t, auditDir, EventProviderSignInStatusChecked)
	require.Len(t, entries, 1, "exactly one probe must produce exactly one audit entry")
	details, _ := entries[0]["details"].(map[string]any)
	require.NotNil(t, details, "the entry must carry a details map")
	assert.Equal(t, "github-copilot", details["provider"])
	assert.Equal(t, "203.0.113.55", details["source_ip"],
		"the entry must record where the call came from")
	assert.Equal(t, "signed_in", details["state"])
	assert.Equal(t, false, details["cached"],
		"the first probe actually ran the vendor CLI, so it must not be marked cached")

	// An authenticated probe records the actor. Rebuilt fresh so the cache
	// does not absorb it.
	api2, _ := newAuthMethodOnboardingAPI(t)
	putCountingCopilotOnPath(t, "ok", "", 0)
	auditDir2 := attachTestAuditor(t, api2)
	req2 := httptest.NewRequest(http.MethodGet, path, nil)
	req2.RemoteAddr = "203.0.113.56:5556"
	ctx2 := context.WithValue(req2.Context(), UserContextKey{},
		&config.UserConfig{Username: "admin"})
	ctx2 = context.WithValue(ctx2, ctxkey.ConfigContextKey{}, api2.agentLoop.GetConfig())
	w2 := httptest.NewRecorder()
	api2.HandleProviders(w2, req2.WithContext(ctx2))
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())

	entries2 := readAuditEntries(t, auditDir2, EventProviderSignInStatusChecked)
	require.Len(t, entries2, 1)
	assert.Equal(t, "admin", entries2[0]["user"],
		"an authenticated probe must name the actor that drove it")
}

// ---------------------------------------------------------------------------
// M1 — the startup orphan sweep could never sweep openai_OAUTH
// ---------------------------------------------------------------------------

// TestSweepOrphanedProviderCredentials_SweepsOAuthBehindASeedTemplateRow is the
// M1 regression test. sweepOrphanedProviderCredentials built configuredVendors
// from EVERY cfg.Providers row without applying isSeedTemplateRow, and
// pkg/config/defaults.go seeds `{Provider: "openai"}` as a permanent keyless
// template row — so configuredVendors["openai"] was populated on every install
// and `openai_OAUTH`, the only OAuth grant the product currently issues, was
// structurally unsweepable. If the process died between the config write and
// the credential delete during provider removal, the live access AND refresh
// token survived with nothing in the UI referencing them.
func TestSweepOrphanedProviderCredentials_SweepsOAuthBehindASeedTemplateRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"orphan"}`))
	require.NoError(t, store.Set("openai_API_KEY", "sk-orphan"))

	// Exactly the shipped seed: a keyless template row with a provider
	// identity and nothing else. No operator ever created it.
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "openai", Model: "gpt-5", APIBase: ""},
	}}
	require.True(t, isSeedTemplateRow(cfg.Providers[0]),
		"precondition: the fixture row must be the seeded template shape")

	sweepOrphanedProviderCredentials(cfg, store, nil)

	_, err := store.Get(oauthName)
	assert.Error(t, err,
		"a seeded template row must not protect %s from the orphan sweep", oauthName)
	_, err = store.Get("openai_API_KEY")
	assert.Error(t, err,
		"a seeded template row must not protect openai_API_KEY from the orphan sweep")
}

// TestSweepOrphanedProviderCredentials_SeedShapedSignInRowStillProtectsItsGrant
// is the guard on the M1 fix itself, for a mistake the fix made on its first
// attempt and the existing suite caught: filtering the vendor keep-set on
// isSeedTemplateRow ALONE deletes live OAuth grants.
//
// A sign_in row legitimately carries no api_key_ref, no api_base and no
// models — it authenticates with a vendor session, not a key — so it can be
// seed-SHAPED while being a real, operator-configured row whose grant is
// live. Sweeping that is unrecoverable, and strictly worse than the orphan
// M1 set out to reclaim. The row's id mapping to a DIFFERENT vendor is what
// distinguishes it from the shipped api-key seed.
func TestSweepOrphanedProviderCredentials_SeedShapedSignInRowStillProtectsItsGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"live","refresh_token":"live-refresh"}`))

	// Deliberately the MINIMAL sign-in row: no auth_method, no api_key_ref,
	// no api_base, no models. isSeedTemplateRow says "template"; it is not.
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Name: "openai-chatgpt", Provider: "openai-chatgpt", Model: "gpt-5.2"},
	}}
	require.True(t, isSeedTemplateRow(cfg.Providers[0]),
		"precondition: this real sign-in row is seed-SHAPED — that is the whole trap")

	sweepOrphanedProviderCredentials(cfg, store, nil)

	_, err := store.Get(oauthName)
	assert.NoError(t, err,
		"a configured sign-in row must protect its vendor's live OAuth grant even when seed-shaped")
}

// TestSweepOrphanedProviderCredentials_KeepsConfiguredAndReferenced pins the
// two keep-sets the M1 filter must NOT weaken. Wrongly deleting a live secret
// is unrecoverable; failing to sweep is merely untidy.
func TestSweepOrphanedProviderCredentials_KeepsConfiguredAndReferenced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"live"}`))
	require.NoError(t, store.Set("anthropic_API_KEY", "sk-live"))
	require.NoError(t, store.Set("weird_API_KEY", "sk-hand-named"))

	cfg := &config.Config{Providers: []*config.ModelConfig{
		// A real, operator-configured openai-chatgpt row: its vendor entry is
		// openai_OAUTH and must survive.
		{Provider: "openai-chatgpt", Model: "gpt-5", AuthMethod: config.AuthMethodSignIn},
		// A real anthropic row.
		{Provider: "anthropic", Model: "claude", APIKeyRef: "anthropic_API_KEY"},
		// A row whose ref was renamed by hand: the belt-and-braces keep-set.
		{Provider: "custom-thing", Model: "m", APIKeyRef: "weird_API_KEY"},
	}}

	sweepOrphanedProviderCredentials(cfg, store, nil)

	for _, name := range []string{oauthName, "anthropic_API_KEY", "weird_API_KEY"} {
		_, err := store.Get(name)
		assert.NoError(t, err, "%s is live and must never be swept", name)
	}
}

// ---------------------------------------------------------------------------
// M2 — two FR-050 pre-auth routes had no rate limit
// ---------------------------------------------------------------------------

// TestSignInImportAndSignOut_AreRateLimited is the M2 regression test. Both
// routes called their handlers BARE while start/poll/status were wrapped. Each
// import rewrites the whole encrypted credentials.json and re-registers every
// OAuth value; each sign-out nils the process-wide sensitive-data replacer
// cache, forcing a full reflection walk of Config under a write lock on the
// next scrub.
//
// Distinct RemoteAddrs keep these buckets away from every other test's.
func TestSignInImportAndSignOut_AreRateLimited(t *testing.T) {
	t.Run("import", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		code := hammerProviderRoute(t, api, http.MethodPost,
			"/api/v1/providers/openai-chatgpt/sign-in/import", "198.51.100.41:41000", 12)
		assert.Equal(t, http.StatusTooManyRequests, code,
			"the import route must be rate limited like its FR-050 siblings")
	})

	t.Run("sign-out", func(t *testing.T) {
		api, _ := newAuthMethodOnboardingAPI(t)
		code := hammerProviderRoute(t, api, http.MethodDelete,
			"/api/v1/providers/openai-chatgpt/sign-in", "198.51.100.42:42000", 12)
		assert.Equal(t, http.StatusTooManyRequests, code,
			"the sign-out route must be rate limited like its FR-050 siblings")
	})
}

// hammerProviderRoute issues up to n anonymous calls from one address and
// returns the first 429's code (or the last code seen).
func hammerProviderRoute(t *testing.T, api *restAPI, method, path, remoteAddr string, n int) int {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	code := 0
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = remoteAddr
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))
		w := httptest.NewRecorder()
		api.HandleProviders(w, req)
		code = w.Code
		if code == http.StatusTooManyRequests {
			return code
		}
	}
	return code
}

// ---------------------------------------------------------------------------
// M4 — every sign-in rate limiter was bypassable on trust_xff deployments
// ---------------------------------------------------------------------------

// TestCanonicalRemoteIP_UsesTheTrustedRightmostHop is the M4 regression test.
// canonicalRemoteIP took the LEFTMOST X-Forwarded-For entry when trust_xff was
// on. The nginx idiom this project documents
// (proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for) APPENDS the
// peer to whatever the client sent, so the leftmost entry is a string the
// ATTACKER chose: a fresh value per request yielded a fresh rate-limit bucket,
// defeating signInStatus/Start/Poll and globalLoginLimiter's brute-force
// protection, and poisoning the audit source_ip.
func TestCanonicalRemoteIP_UsesTheTrustedRightmostHop(t *testing.T) {
	cases := []struct {
		name string
		xff  string
		ra   string
		want string
	}{
		{
			// The attack, exactly as nginx delivers it.
			name: "attacker-supplied entry is ignored in favour of the proxy-appended one",
			xff:  "6.6.6.6, 198.51.100.42",
			ra:   "10.0.0.1:443",
			want: "198.51.100.42",
		},
		{
			name: "a whole forged chain still resolves to the appended hop",
			xff:  "1.1.1.1, 2.2.2.2, 3.3.3.3, 198.51.100.42",
			ra:   "10.0.0.1:443",
			want: "198.51.100.42",
		},
		{
			// Caddy's documented header_up X-Forwarded-For {remote_host}
			// REPLACES the header, so its single entry is the real client.
			name: "single entry (Caddy replace idiom) is honoured unchanged",
			xff:  "203.0.113.1",
			ra:   "10.0.0.1:1234",
			want: "203.0.113.1",
		},
		{
			name: "whitespace around the trusted hop is trimmed",
			xff:  "6.6.6.6 ,  203.0.113.1 ",
			ra:   "10.0.0.1:1234",
			want: "203.0.113.1",
		},
		{
			// Fail-closed: a trailing comma must not yield an empty key that
			// every caller would then share.
			name: "an empty trailing entry falls back to RemoteAddr",
			xff:  "6.6.6.6,",
			ra:   "10.0.0.9:1234",
			want: "10.0.0.9",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
			req.RemoteAddr = tc.ra
			req.Header.Set("X-Forwarded-For", tc.xff)
			assert.Equal(t, tc.want, canonicalRemoteIP(req, true))
		})
	}
}

// TestSpoofedXFF_CannotResetASignInLimiterOnTrustXFFDeployments is the
// end-to-end M4 proof: on a trust_xff deployment, a fresh forged
// X-Forwarded-For per request must NOT hand the caller a fresh bucket.
// canonicalRemoteIP is exercised through the real limiter path, so a
// regression that reintroduced leftmost parsing anywhere in the chain fails
// here even if the unit test above were adjusted.
func TestSpoofedXFF_CannotResetASignInLimiterOnTrustXFFDeployments(t *testing.T) {
	limiter := newAPIRateLimiter(2, time.Minute)
	calls := 0
	wrapped := withRateLimit(limiter, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	cfg := &config.Config{Gateway: config.GatewayConfig{TrustXFF: true}}

	var lastCode int
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/x/sign-in", nil)
		req.RemoteAddr = "10.0.0.1:443" // the trusted proxy
		// A DIFFERENT forged left-hand entry every time; nginx appends the
		// real (constant) client address.
		req.Header.Set("X-Forwarded-For",
			"9.9.9."+strconv.Itoa(i)+", 198.51.100.77")
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))
		w := httptest.NewRecorder()
		wrapped(w, req)
		lastCode = w.Code
	}

	assert.Equal(t, http.StatusTooManyRequests, lastCode,
		"a fresh spoofed leftmost X-Forwarded-For must not reset the bucket")
	assert.Equal(t, 2, calls, "only the limiter's allowance may reach the handler")
}

// ---------------------------------------------------------------------------
// audit scaffolding
// ---------------------------------------------------------------------------

// attachTestAuditor wires a real audit logger writing into home/audit so the
// M5 test reads the ACTUAL JSONL the gateway would write, not a stub.
func attachTestAuditor(t *testing.T, api *restAPI) string {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	api.auditor = logger
	return dir
}

// readAuditEntries returns every audit entry with the given event name.
func readAuditEntries(t *testing.T, auditDir, event string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "line=%s", line)
		if entry["event"] == event {
			out = append(out, entry)
		}
	}
	return out
}
