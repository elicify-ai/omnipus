// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_onboarding_authority_test.go — who may run
// POST /api/v1/onboarding/complete, and what it is allowed to write.
//
// ADR-0008 rulings 1 and 2 inverted the order of the product: onboarding now
// runs AFTER sign-in. The route no longer mints an authentication authority —
// it creates no account, hashes no password, issues no bearer token and sets
// no cookie — so the pre-auth window that used to guard it is gone from this
// route, and the thing that replaced it is an ordinary authenticated session.
//
// The invariant these tests pin, in one sentence: an unauthenticated caller
// gets 401 and changes nothing, a signed-in caller gets 200 exactly once, and
// gateway.users is byte-for-byte the same before and after either.
//
// Every test drives the PRODUCTION route table (registerAdditionalEndpoints
// through a real *http.ServeMux — the exact call gateway.go makes at startup)
// rather than a hand-built handler, so a regression in EITHER the registration
// line (withAuth) or the in-handler check fails them.
//
// Each test uses its own source IP: onboardingCompleteLimiter is a
// package-level 3-per-minute-per-IP limiter shared with every other test in
// this package, and a shared IP would make these tests order-dependent.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// platformAccountEmail is the signed-in account these tests speak as. It is an
// EMAIL, because under ADR-0008 the omnipus.ai account is the login and its
// address is the gateway.users username.
const platformAccountEmail = "operator@example.com"

// platformSessionToken is the plaintext value of the omnipus-session cookie
// the fixture's account holds. Only its bcrypt hash is on disk, exactly as a
// real sign-in leaves it.
const platformSessionToken = "test-platform-session-token-value"

// onboardAuthEnv is one fully wired instance under test: a real restAPI, the
// temp $OMNIPUS_HOME its config.json lives in, and the loopback provider
// stand-in the api_key probe talks to.
type onboardAuthEnv struct {
	api      *restAPI
	home     string
	upstream string
	cfg      *config.Config
	auditDir string
}

// newSignedInOnboardEnv builds a fresh instance whose config.json already
// holds the SIGNED-IN account — username = the account email, password_hash
// EMPTY, session_token_hash matching platformSessionToken — and nothing else.
//
// That row is the state a platform sign-in leaves behind: an account with no
// local password, and a session issued against it. It is what every request in
// this file authenticates as, and what every assertion here checks is still
// untouched afterwards.
func newSignedInOnboardEnv(t *testing.T) *onboardAuthEnv {
	t.Helper()
	return newOnboardEnvWithUsers(t, []onboardEnvUser{{
		username:    platformAccountEmail,
		sessionHash: mustBcryptHash(t, platformSessionToken),
	}})
}

// newOnboardAuthEnv builds an instance whose config.json holds the named
// LOCAL-PASSWORD accounts — the shape that still matters to the routes which
// keep a pre-auth window (rest_provider_preauth_test.go), where the only
// question the gate asks is "does an authentication authority exist".
// Passing no users produces an instance with none.
func newOnboardAuthEnv(t *testing.T, existingUsers ...string) *onboardAuthEnv {
	t.Helper()
	users := make([]onboardEnvUser, 0, len(existingUsers))
	for _, name := range existingUsers {
		users = append(users, onboardEnvUser{
			username:     name,
			passwordHash: mustBcryptHash(t, name+"-original-password"),
		})
	}
	return newOnboardEnvWithUsers(t, users)
}

// onboardEnvUser is one gateway.users row the fixture seeds. Either hash may
// be empty: a platform account has no password, and an account nobody has
// signed in as yet has no session.
type onboardEnvUser struct {
	username     string
	passwordHash config.BcryptHash
	sessionHash  config.BcryptHash
}

// newOnboardEnvWithUsers is the shared body of both fixtures above.
// Onboarding is deliberately left INCOMPLETE (no state.json is written).
//
// The accounts are written to BOTH config.json (what a restarted gateway
// would read) and the in-memory *config.Config the agent loop serves, because
// that is what a running gateway actually looks like: boot loads config.json
// into memory, and configSnapshotMiddleware injects that same snapshot into
// every request.
func newOnboardEnvWithUsers(t *testing.T, seedUsers []onboardEnvUser) *onboardAuthEnv {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()

	diskUsers := make([]any, 0, len(seedUsers))
	cfgUsers := make([]config.UserConfig, 0, len(seedUsers))
	for _, u := range seedUsers {
		diskUsers = append(diskUsers, map[string]any{
			"username":           u.username,
			"password_hash":      string(u.passwordHash),
			"session_token_hash": string(u.sessionHash),
		})
		cfgUsers = append(cfgUsers, config.UserConfig{
			Username:         u.username,
			PasswordHash:     string(u.passwordHash),
			SessionTokenHash: u.sessionHash,
		})
	}

	onDisk := map[string]any{
		"version":   config.CurrentVersion,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"gateway":   map[string]any{"users": diskUsers},
	}
	raw, err := json.MarshalIndent(onDisk, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(home+"/config.json", raw, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 8080,
			Users: cfgUsers,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := newOnboardingTestAPI(t, home, al)

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir: auditDir, MaxSizeBytes: 1 << 20, RetentionDays: 1,
	})
	require.NoError(t, err, "audit logger must initialize")
	t.Cleanup(func() { _ = auditLogger.Close() })
	api.auditor = auditLogger

	require.False(t, api.onboardingMgr.IsComplete(),
		"the fixture must leave onboarding INCOMPLETE — every test here starts from a fresh instance")

	return &onboardAuthEnv{
		api:      api,
		home:     home,
		upstream: startFakeProviderUpstream(t),
		cfg:      cfg,
		auditDir: auditDir,
	}
}

// completeBody is the new wire shape: a provider and nothing else. No account
// block exists on it at all.
func (e *onboardAuthEnv) completeBody() string {
	return withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},`+
			`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`,
		e.upstream,
	)
}

// postComplete issues one POST /api/v1/onboarding/complete through the
// production route table. When authenticated is true it carries the
// omnipus-session cookie the fixture's account holds; otherwise it carries no
// credential at all.
func (e *onboardAuthEnv) postComplete(
	t *testing.T, sourceIP, body string, authenticated bool,
) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	e.api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = sourceIP + ":54321"
	if authenticated {
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: platformSessionToken})
	}

	w := httptest.NewRecorder()
	// configSnapshotMiddleware is not in this mux, so inject the snapshot the
	// production chain would have carried. Reading the LIVE config matters:
	// safeUpdateConfigJSON refreshes it, so a second request in the same test
	// sees what the first one wrote.
	mux.ServeHTTP(w, req.WithContext(withConfigSnapshot(req.Context(), e.api.agentLoop.GetConfig())))
	return w
}

// withConfigSnapshot injects a config snapshot the way
// configSnapshotMiddleware does in production.
func withConfigSnapshot(ctx context.Context, cfg *config.Config) context.Context {
	return context.WithValue(ctx, ctxkey.ConfigContextKey{}, cfg)
}

// diskUsers reads gateway.users straight off config.json, as an operator or a
// restarted gateway would.
func (e *onboardAuthEnv) diskUsers(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(e.home + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	gw, ok := m["gateway"].(map[string]any)
	if !ok {
		return nil
	}
	list, ok := gw["users"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, u := range list {
		if um, isMap := u.(map[string]any); isMap {
			out = append(out, um)
		}
	}
	return out
}

// diskProviders reads the providers array straight off config.json.
func (e *onboardAuthEnv) diskProviders(t *testing.T) []any {
	t.Helper()
	raw, err := os.ReadFile(e.home + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	list, _ := m["providers"].([]any)
	return list
}

// requireAccountUntouched asserts the ONE thing every test in this file cares
// about besides its status code: the signed-in account is exactly as the
// platform left it — one row, no password, and no bearer token minted against
// it by this route.
func (e *onboardAuthEnv) requireAccountUntouched(t *testing.T) {
	t.Helper()
	users := e.diskUsers(t)
	require.Len(t, users, 1, "onboarding must neither add nor remove an account")
	assert.Equal(t, platformAccountEmail, users[0]["username"],
		"the account is the platform account, keyed by its email")
	assert.Empty(t, users[0]["password_hash"],
		"a platform account has NO local password — completion must never write one")
	assert.Empty(t, users[0]["tokens"],
		"completion must mint no bearer token: the caller already had a session")
	assert.Empty(t, users[0]["token_hash"],
		"nor the legacy single-token field")
}

// auditEvents returns every audit entry written so far, flushed to disk.
func (e *onboardAuthEnv) auditEvents(t *testing.T) []map[string]any {
	t.Helper()
	require.NoError(t, e.api.auditor.Close())
	return readAuditLog(t, e.auditDir)
}

// TestOnboardingComplete_Unauthenticated_Is401_AndWritesNothing is the
// security invariant, stated as bluntly as it can be: with no session, POST
// /api/v1/onboarding/complete is 401. Never 200, and never the 409 the old
// pre-auth window returned — a 409 here would mean the route had decided the
// request on instance STATE rather than on the caller's identity, which is
// exactly the shape ADR-0008 §0.4 FR-OB-062 warns is the dangerous one to get
// wrong.
func TestOnboardingComplete_Unauthenticated_Is401_AndWritesNothing(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)

	w := env.postComplete(t, "203.0.113.11", env.completeBody(), false)

	require.Equal(t, http.StatusUnauthorized, w.Code,
		"an anonymous caller must be refused 401 — never 200, never 409; body=%s", w.Body.String())
	for _, c := range w.Result().Cookies() {
		assert.NotEqual(t, middleware.SessionCookieName, c.Name,
			"a refused request must not hand back a session")
	}

	env.requireAccountUntouched(t)
	assert.Empty(t, env.diskProviders(t),
		"a refused request must persist no provider")
	assert.False(t, env.api.onboardingMgr.IsComplete(),
		"a refused request must not mark onboarding complete either")
}

// TestOnboardingComplete_Unauthenticated_Is401_EvenOnAFreshInstanceWithNoAccounts
// is the FR-OB-062 case, and the one most likely to rot back open. The old
// gate opened the window whenever the instance had no authentication
// authority. If any part of that logic survived on this route, an instance
// with an empty gateway.users — a half-provisioned install, a restored
// config, a deployment whose account row has not landed yet — would be
// anonymously onboardable. It must be 401 here for the same reason it is 401
// above: the check is on the CALLER, not on the instance.
func TestOnboardingComplete_Unauthenticated_Is401_EvenOnAFreshInstanceWithNoAccounts(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)
	// Strip every account from the live snapshot AND from disk: no users, no
	// bearer token, nothing that could count as an authority.
	emptyCfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         env.home,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	onDisk := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"gateway":{"users":[]}}`)
	require.NoError(t, os.WriteFile(env.home+"/config.json", onDisk, 0o600))

	mux := http.NewServeMux()
	env.api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete",
		strings.NewReader(env.completeBody()))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.12:54321"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req.WithContext(withConfigSnapshot(req.Context(), emptyCfg)))

	require.Equal(t, http.StatusUnauthorized, w.Code,
		"an instance with no accounts at all is still not anonymously onboardable; body=%s",
		w.Body.String())
	assert.Empty(t, env.diskProviders(t), "and nothing may be persisted")
	assert.False(t, env.api.onboardingMgr.IsComplete())
}

// TestOnboardingComplete_Authenticated_FreshInstance_Completes is the other
// direction: a signed-in owner on a fresh instance completes setup, the
// provider and the default model land, and the account is untouched.
func TestOnboardingComplete_Authenticated_FreshInstance_Completes(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)

	w := env.postComplete(t, "203.0.113.13", env.completeBody(), true)

	require.Equal(t, http.StatusOK, w.Code,
		"a signed-in owner must be able to finish setup; body=%s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, platformAccountEmail, resp["username"],
		"the response echoes the AUTHENTICATED account, not anything from the body")
	assert.NotContains(t, resp, "token",
		"the response must carry no bearer token — the caller already holds a session")
	for _, c := range w.Result().Cookies() {
		assert.NotEqual(t, middleware.SessionCookieName, c.Name,
			"completion must not re-issue a session cookie")
	}

	assert.True(t, env.api.onboardingMgr.IsComplete(),
		"a successful completion marks state.json complete")
	env.requireAccountUntouched(t)

	providers := env.diskProviders(t)
	require.Len(t, providers, 1, "the chosen provider must be persisted")
	entry, ok := providers[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "openai", entry["provider"])
}

// TestOnboardingComplete_Authenticated_SecondPost_Is409 pins the
// already-complete conflict, which survives the rewrite unchanged: the
// reservation is the single-shot gate, and a signed-in caller who asks twice
// is told the instance is already set up.
func TestOnboardingComplete_Authenticated_SecondPost_Is409(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)

	first := env.postComplete(t, "203.0.113.14", env.completeBody(), true)
	require.Equal(t, http.StatusOK, first.Code, "body=%s", first.Body.String())

	second := env.postComplete(t, "203.0.113.15", env.completeBody(), true)

	require.Equal(t, http.StatusConflict, second.Code,
		"a second completion on an already-onboarded instance must be refused 409; body=%s",
		second.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &resp))
	assert.Equal(t, onboardingClosedMsg, resp["error"])

	env.requireAccountUntouched(t)
}

// TestOnboardingComplete_BodyCarryingAnAdminBlock_Is400 pins the wire break.
// `admin` was REQUIRED on this endpoint until ADR-0008 ruling 2 deleted the
// local account; a client that still sends it must be told loudly, because
// the alternative — accepting the body and ignoring the credentials in it —
// is a client that believes it created a password that does not exist.
//
// The strict decode is what enforces this, so it holds whether or not
// ValidateInbound is on (the fixture leaves it off, which is the weaker of the
// two configurations and therefore the one worth testing).
func TestOnboardingComplete_BodyCarryingAnAdminBlock_Is400(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)
	require.False(t, env.api.agentLoop.GetConfig().Gateway.ValidateInbound,
		"this test is only meaningful with schema validation OFF — the strict decode must catch it alone")

	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},`+
			`"admin":{"username":"admin","password":"secret123"}}`,
		env.upstream,
	)
	w := env.postComplete(t, "203.0.113.16", body, true)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a body still carrying the deleted admin block must be rejected, not silently accepted; body=%s",
		w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errMsg, _ := resp["error"].(string)
	assert.Contains(t, errMsg, "admin",
		"the refusal must name the field that is no longer allowed")

	env.requireAccountUntouched(t)
	assert.Empty(t, env.diskProviders(t), "a rejected body persists nothing")
	assert.False(t, env.api.onboardingMgr.IsComplete(),
		"and releases the reservation rather than bricking onboarding")
}

// TestOnboardingComplete_AuditsTheAuthenticatedActor pins SEC-15 for the new
// shape. The record must attribute the completion to the SIGNED-IN account —
// the only identity the route now has, since the body carries none — and must
// still carry the source IP and the provider.
func TestOnboardingComplete_AuditsTheAuthenticatedActor(t *testing.T) {
	withEdition(t, config.EditionHosted)
	env := newSignedInOnboardEnv(t)

	w := env.postComplete(t, "203.0.113.17", env.completeBody(), true)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entry map[string]any
	for _, line := range env.auditEvents(t) {
		if line["event"] == audit.EventOnboardingAdminCreated {
			entry = line
			break
		}
	}
	require.NotNil(t, entry,
		"completing onboarding must write an %q audit entry", audit.EventOnboardingAdminCreated)

	assert.Equal(t, audit.DecisionAllow, entry["decision"])
	assert.Equal(t, platformAccountEmail, entry["user"],
		"the actor is the authenticated account")
	assert.NotEmpty(t, entry["timestamp"], "SEC-15 requires a real timestamp")
	assert.NotEmpty(t, entry["policy_rule"], "SEC-17 requires an explanation on every decision")

	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "the entry must carry details")
	assert.Equal(t, platformAccountEmail, details["username"])
	assert.Equal(t, "203.0.113.17", details["source_ip"],
		"the source IP is the forensic field that makes a completion traceable")
	assert.Equal(t, "openai", details["provider"])
	assert.Equal(t, "/api/v1/onboarding/complete", details["route"])

	rendered, err := json.Marshal(entry)
	require.NoError(t, err)
	assert.NotContains(t, string(rendered), "sk-test",
		"the audit entry must never carry the provider API key")
}
