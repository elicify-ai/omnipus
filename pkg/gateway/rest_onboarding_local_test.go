// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_onboarding_local_test.go — LOCAL-MODE onboarding (ADR-0010 WP5):
// admin-account creation and the pre-auth authority window gate, restored
// from upstream (merge base 184d724773789513a4a7fd404596115ea4ec55cf) for
// config.EditionAuthMode() == config.AuthModeLocal — the open-source
// edition's behaviour, unchanged from before ADR-0008. Every test here pins
// config.Edition = config.EditionCore in its own setup (the withEdition
// save/restore helper from rest_backup_test.go), so the suite is explicit
// about which mode it means rather than relying on the source default.
//
// rest_onboarding_authority_test.go pins the SAME route's PLATFORM-mode
// behaviour (today's, ADR-0008); the mode-agnostic provider/credential
// logic (rest_onboarding_test.go) applies to both. This file is
// deliberately the third leg, not a merge of the other two: local and
// platform mode disagree on what authorises the request and what the route
// creates on disk.
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// localOnboardEnv is one fully wired LOCAL-MODE instance under test.
type localOnboardEnv struct {
	api      *restAPI
	home     string
	upstream string
	auditDir string
}

// newLocalOnboardEnv builds a fresh local-mode instance: config.Edition
// pinned to core for the test's duration, a temp $OMNIPUS_HOME with a
// minimal config.json carrying the named existing users (none by default —
// a genuine fresh install), onboarding deliberately INCOMPLETE, and an
// audit logger attached so SEC-15 records are inspectable.
func newLocalOnboardEnv(t *testing.T, existingUsers ...string) *localOnboardEnv {
	t.Helper()
	withEdition(t, config.EditionCore)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()

	diskUsers := make([]any, 0, len(existingUsers))
	cfgUsers := make([]config.UserConfig, 0, len(existingUsers))
	for _, name := range existingUsers {
		hash := mustBcryptHash(t, name+"-original-password")
		diskUsers = append(diskUsers, map[string]any{
			"username": name, "password_hash": string(hash),
		})
		cfgUsers = append(cfgUsers, config.UserConfig{Username: name, PasswordHash: string(hash)})
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
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, Users: cfgUsers},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
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

	return &localOnboardEnv{
		api:      api,
		home:     home,
		upstream: startFakeProviderUpstream(t),
		auditDir: auditDir,
	}
}

// completeBody is upstream's wire shape: a provider AND an admin block.
func (e *localOnboardEnv) completeBody(username, password string) string {
	return withProviderEndpoint(
		fmt.Sprintf(
			`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},`+
				`"admin":{"username":%q,"password":%q},`+
				`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`,
			username, password,
		),
		e.upstream,
	)
}

// postComplete drives HandleCompleteOnboarding directly (this lane owns the
// handler, not route registration — see rest.go, WP2). No auth context is
// injected: local mode has no session to inject before this route runs.
func (e *localOnboardEnv) postComplete(t *testing.T, sourceIP, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = sourceIP + ":54321"
	w := httptest.NewRecorder()
	e.api.HandleCompleteOnboarding(w, req)
	return w
}

// diskUsers reads gateway.users straight off config.json.
func (e *localOnboardEnv) diskUsers(t *testing.T) []map[string]any {
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

// auditEvents returns every audit entry written so far, flushed to disk.
func (e *localOnboardEnv) auditEvents(t *testing.T) []map[string]any {
	t.Helper()
	require.NoError(t, e.api.auditor.Close())
	return readAuditLog(t, e.auditDir)
}

// ── Success: mints the account, issues cookies, returns the bootstrap token ─

// TestLocalOnboardingComplete_Success_CreatesAdminAndIssuesSessionAndToken is
// the local-mode positive case: an anonymous POST carrying a valid admin
// block and provider succeeds, the account lands in gateway.users with a
// bcrypt password hash and a token entry, the session and CSRF cookies are
// issued, and the JSON response carries BOTH username and token (upstream's
// shape, restored).
func TestLocalOnboardingComplete_Success_CreatesAdminAndIssuesSessionAndToken(t *testing.T) {
	env := newLocalOnboardEnv(t)

	w := env.postComplete(t, "203.0.113.21", env.completeBody("admin", "s3cr3tpassword"))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin", resp["username"])
	token, _ := resp["token"].(string)
	assert.NotEmpty(t, token, "local mode must bootstrap a bearer token — there was no session before this call")

	var sawSession, sawCSRF bool
	for _, c := range w.Result().Cookies() {
		if c.Name == middleware.SessionCookieName {
			sawSession = true
		}
		if c.Name == middleware.CSRFCookieName || c.Name == middleware.CSRFCookieNameHTTP {
			sawCSRF = true
		}
	}
	assert.True(t, sawSession, "local mode must issue the session cookie — this route bootstraps the first one")
	assert.True(t, sawCSRF, "local mode must issue the CSRF cookie alongside the session")

	users := env.diskUsers(t)
	require.Len(t, users, 1, "onboarding must create exactly one account")
	assert.Equal(t, "admin", users[0]["username"])
	assert.NotEmpty(t, users[0]["password_hash"], "the admin password must be persisted, bcrypt-hashed")
	assert.NotEmpty(t, users[0]["tokens"], "the bootstrap token must be persisted alongside the account")

	assert.True(t, env.api.onboardingMgr.IsComplete())
}

// ── admin required in local mode ────────────────────────────────────────────

// TestLocalOnboardingComplete_MissingAdminBlock_Is400 pins the OTHER mode's
// pin: platform mode refuses a body carrying `admin`
// (TestOnboardingComplete_BodyCarryingAnAdminBlock_Is400); local mode
// refuses a body that omits it, because this route is the one that mints
// the instance's first account and there is nothing else to mint it from.
func TestLocalOnboardingComplete_MissingAdminBlock_Is400(t *testing.T) {
	env := newLocalOnboardEnv(t)

	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"}}`,
		env.upstream,
	)
	w := env.postComplete(t, "203.0.113.22", body)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "admin")
	assert.Empty(t, env.diskUsers(t), "a refused request must create no account")
	assert.False(t, env.api.onboardingMgr.IsComplete())
}

func TestLocalOnboardingComplete_MissingUsername_Is400(t *testing.T) {
	env := newLocalOnboardEnv(t)
	w := env.postComplete(t, "203.0.113.23", env.completeBody("", "s3cr3tpassword"))
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin.username is required", resp["error"])
}

func TestLocalOnboardingComplete_MissingPassword_Is400(t *testing.T) {
	env := newLocalOnboardEnv(t)
	w := env.postComplete(t, "203.0.113.24", env.completeBody("admin", ""))
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin.password is required", resp["error"])
}

// TestLocalOnboardingComplete_InvalidUsername_Is400 pins usernameRE: a
// username starting with a non-alphanumeric is rejected regardless of
// ValidateInbound.
func TestLocalOnboardingComplete_InvalidUsername_Is400(t *testing.T) {
	env := newLocalOnboardEnv(t)
	w := env.postComplete(t, "203.0.113.25", env.completeBody("-admin", "s3cr3tpassword"))
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, usernameInvalidMsg, resp["error"])
}

// TestLocalOnboardingComplete_ReservedUsername_Is400 pins reservedUsernames:
// "cli" collides with the synthetic CLI-token principal.
func TestLocalOnboardingComplete_ReservedUsername_Is400(t *testing.T) {
	env := newLocalOnboardEnv(t)
	w := env.postComplete(t, "203.0.113.26", env.completeBody("cli", "s3cr3tpassword"))
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, reservedUsernameMsg, resp["error"])
}

func TestLocalOnboardingComplete_WeakPassword_Is400(t *testing.T) {
	env := newLocalOnboardEnv(t)
	w := env.postComplete(t, "203.0.113.27", env.completeBody("admin", "short"))
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin.password must be at least 8 characters", resp["error"])
}

// ── duplicate username: refuse, never overwrite ─────────────────────────────

// TestLocalOnboardingComplete_SecondPost_Is409ByTheWindowGate is the
// sequential case: onboarding's single-flight reservation (ReserveComplete)
// already refuses a second completion before it can reach persistConfig at
// all, so two SEQUENTIAL requests never reach writeLocalAdminUser's
// collision check — the window gate is the primary control.
func TestLocalOnboardingComplete_SecondPost_Is409ByTheWindowGate(t *testing.T) {
	env := newLocalOnboardEnv(t)

	first := env.postComplete(t, "203.0.113.28", env.completeBody("admin", "s3cr3tpassword"))
	require.Equal(t, http.StatusOK, first.Code, "body=%s", first.Body.String())

	second := env.postComplete(t, "203.0.113.29", env.completeBody("admin", "s3cr3tpassword"))
	require.Equal(t, http.StatusConflict, second.Code, "body=%s", second.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &resp))
	assert.Equal(t, onboardingClosedMsg, resp["error"])

	users := env.diskUsers(t)
	require.Len(t, users, 1, "the second attempt must not add or mutate any account")
	assert.Equal(t, "admin", users[0]["username"])
}

// TestWriteLocalAdminUser_DuplicateUsername_ReturnsSentinel is the
// defence-in-depth control itself, exercised directly: writeLocalAdminUser
// must refuse to append (or overwrite) a colliding username rather than
// silently accept it, restored from upstream's comment on the identical
// pre-ADR-0008 code (the branch used to overwrite the existing row's
// password_hash — a silent account takeover). Direct rather than via HTTP
// because the phase-0 window gate and the single-flight reservation both
// make a same-process collision practically unreachable over the wire
// (see TestLocalOnboardingComplete_SecondPost_Is409ByTheWindowGate above);
// this pins the belt half of belt-and-braces.
func TestWriteLocalAdminUser_DuplicateUsername_ReturnsSentinel(t *testing.T) {
	var body gen.OnboardingCompleteRequest
	require.NoError(t, json.Unmarshal([]byte(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"x"},`+
			`"admin":{"username":"admin","password":"s3cr3tpassword"}}`,
	), &body))
	ro := &restAPIHandleCompleteOnboarding{body: body}

	m := map[string]any{
		"gateway": map[string]any{
			"users": []any{
				map[string]any{"username": "admin", "password_hash": "existing-hash"},
			},
		},
	}
	err := ro.writeLocalAdminUser(m)

	require.ErrorIs(t, err, errOnboardingUsernameTaken)
	gw := m["gateway"].(map[string]any)
	users := gw["users"].([]any)
	require.Len(t, users, 1, "the existing row must not be duplicated or replaced")
	existing := users[0].(map[string]any)
	assert.Equal(t, "existing-hash", existing["password_hash"],
		"the existing account's password hash must be untouched")
}

// ── the pre-auth window gate ─────────────────────────────────────────────────

// TestLocalOnboardingComplete_AuthorityAlreadyExists_Is409AndWritesNothing is
// the defect the window gate closes: an instance that already has an
// account (an authentication authority) must refuse a second anonymous
// completion, even though state.json still says onboarding is not complete
// — the exact divergent state a corrupt/lost state.json produces.
func TestLocalOnboardingComplete_AuthorityAlreadyExists_Is409AndWritesNothing(t *testing.T) {
	env := newLocalOnboardEnv(t, "realoperator")

	w := env.postComplete(t, "203.0.113.30", env.completeBody("admin", "s3cr3tpassword"))

	require.Equal(t, http.StatusConflict, w.Code,
		"an instance with an existing account must refuse anonymous completion; body=%s", w.Body.String())
	users := env.diskUsers(t)
	require.Len(t, users, 1, "no second account may be appended")
	assert.Equal(t, "realoperator", users[0]["username"])
	assert.False(t, env.api.onboardingMgr.IsComplete())
}

// TestLocalOnboardingComplete_StateUnknown_Is409 pins the fail-CLOSED signal:
// an unreadable/unparseable state.json is "unknown", never "fresh install".
func TestLocalOnboardingComplete_StateUnknown_Is409(t *testing.T) {
	env := newLocalOnboardEnv(t)
	env.api.onboardingStateUnknown = true

	w := env.postComplete(t, "203.0.113.31", env.completeBody("admin", "s3cr3tpassword"))

	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	assert.Empty(t, env.diskUsers(t))
}

// TestLocalOnboardingComplete_AlreadyComplete_Is409 pins the ordinary
// already-onboarded case, and that the refusal is audited with a
// source IP.
func TestLocalOnboardingComplete_AlreadyComplete_Is409(t *testing.T) {
	env := newLocalOnboardEnv(t)
	require.NoError(t, env.api.onboardingMgr.CompleteOnboarding())

	w := env.postComplete(t, "203.0.113.32", env.completeBody("admin", "s3cr3tpassword"))

	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())

	var refusal map[string]any
	for _, line := range env.auditEvents(t) {
		if line["event"] == audit.EventOnboardingRefused {
			refusal = line
			break
		}
	}
	require.NotNil(t, refusal, "a refused request must be audited (SEC-15/SEC-17)")
	details, ok := refusal["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "onboarding_already_complete", details["reason"])
	assert.Equal(t, "203.0.113.32", details["source_ip"])
}

// ── the probe route's local-mode gate ───────────────────────────────────────

// TestLocalOnboardingProbe_WindowOpenOnFreshInstall_NotRefused pins that the
// probe route (reached BEFORE any account exists in local mode, unlike
// platform mode) is usable on a genuine fresh install — the same
// pre-auth window gate that guards /complete, not the platform's
// signed-in-caller gate.
func TestLocalOnboardingProbe_WindowOpenOnFreshInstall_NotRefused(t *testing.T) {
	env := newLocalOnboardEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider",
		strings.NewReader(`{"id":"openai","auth":"api_key","api_key":"sk-test","model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.api.HandleOnboardingProbeProvider(w, req)

	assert.NotEqual(t, http.StatusConflict, w.Code,
		"a fresh install's probe must not be refused by the window gate; body=%s", w.Body.String())
}

// TestLocalOnboardingProbe_AuthorityExists_Refused is the local-mode probe's
// counterpart to TestLocalOnboardingComplete_AuthorityAlreadyExists_Is409AndWritesNothing:
// once an account exists, the probe — an outbound-request oracle — must
// close too.
func TestLocalOnboardingProbe_AuthorityExists_Refused(t *testing.T) {
	env := newLocalOnboardEnv(t, "realoperator")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider",
		strings.NewReader(`{"id":"openai","auth":"api_key","api_key":"sk-test","model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.api.HandleOnboardingProbeProvider(w, req)

	assert.Equal(t, http.StatusConflict, w.Code,
		"an instance with an existing account must refuse the probe too; body=%s", w.Body.String())
}
