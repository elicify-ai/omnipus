// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_onboarding_authority_test.go — the pre-auth authority gate on
// POST /api/v1/onboarding/complete.
//
// The defect these tests pin, reproduced twice against a live binary (once by
// a UAT agent, once independently by the orchestrator): the endpoint gated on
// the onboarding flag ALONE and never asked whether an authentication
// authority already existed, even though it is the one route that MINTS
// authority. In the divergent state — users present in config.json,
// system/state.json's onboarding.completed back to false because the file was
// lost, truncated or unparseable — an anonymous POST returned 200, appended a
// second admin to gateway.users and handed the caller a bearer token, while
// every ordinary route on the same instance correctly 401'd.
//
// Every test here drives the PRODUCTION route table
// (registerAdditionalEndpoints through a real *http.ServeMux — the exact call
// gateway.go makes at startup) rather than a hand-built handler, so a
// regression in EITHER the registration line or the in-handler gate fails
// them.
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
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

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

// newOnboardAuthEnv builds an instance whose config.json ALREADY contains the
// users named in `existingUsers`, with onboarding deliberately left INCOMPLETE
// (no state.json is written). Passing no users produces a genuine fresh
// install; passing one reproduces the divergent state exactly.
//
// The users are written to BOTH config.json (what the gate's authority check
// would read after a restart) and the in-memory *config.Config the agent loop
// serves, because that is what a restarted gateway actually looks like: boot
// loads config.json into memory, and configSnapshotMiddleware injects that
// same snapshot into every request.
func newOnboardAuthEnv(t *testing.T, existingUsers ...string) *onboardAuthEnv {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()

	users := make([]any, 0, len(existingUsers))
	cfgUsers := make([]config.UserConfig, 0, len(existingUsers))
	for _, name := range existingUsers {
		hash, err := bcrypt.GenerateFromPassword([]byte(name+"-original-password"), bcrypt.MinCost)
		require.NoError(t, err)
		users = append(users, map[string]any{
			"username":      name,
			"password_hash": string(hash),
		})
		cfgUsers = append(cfgUsers, config.UserConfig{Username: name, PasswordHash: string(hash)})
	}

	onDisk := map[string]any{
		"version":   config.CurrentVersion,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{},
		"gateway":   map[string]any{"users": users},
	}
	raw, err := json.MarshalIndent(onDisk, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(home+"/config.json", raw, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, Users: cfgUsers},
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
		"the fixture must leave onboarding INCOMPLETE — that is the whole point of the divergent state")

	return &onboardAuthEnv{
		api:      api,
		home:     home,
		upstream: startFakeProviderUpstream(t),
		cfg:      cfg,
		auditDir: auditDir,
	}
}

// postCompleteViaRealMux issues one anonymous POST /api/v1/onboarding/complete
// through the production route table.
func (e *onboardAuthEnv) postCompleteViaRealMux(t *testing.T, sourceIP, username string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	e.api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-attacker"},`+
			`"admin":{"username":"`+username+`","password":"attackerpw123"}}`,
		e.upstream,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = sourceIP + ":54321"

	w := httptest.NewRecorder()
	// configSnapshotMiddleware is not in this mux, so inject the snapshot the
	// production chain would have carried.
	mux.ServeHTTP(w, req.WithContext(withConfigSnapshot(req.Context(), e.cfg)))
	return w
}

// withConfigSnapshot injects a config snapshot the way
// configSnapshotMiddleware does in production.
func withConfigSnapshot(ctx context.Context, cfg *config.Config) context.Context {
	return context.WithValue(ctx, ctxkey.ConfigContextKey{}, cfg)
}

// diskUsers reads gateway.users straight off config.json, as an operator or a
// restarted gateway would.
func (e *onboardAuthEnv) diskUsers(t *testing.T) []string {
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
	names := make([]string, 0, len(list))
	for _, u := range list {
		um, isMap := u.(map[string]any)
		if !isMap {
			continue
		}
		if name, isStr := um["username"].(string); isStr {
			names = append(names, name)
		}
	}
	return names
}

// diskPasswordHash returns the stored bcrypt hash for `username`, or "" when
// the user is absent.
func (e *onboardAuthEnv) diskPasswordHash(t *testing.T, username string) string {
	t.Helper()
	raw, err := os.ReadFile(e.home + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	gw, ok := m["gateway"].(map[string]any)
	if !ok {
		return ""
	}
	list, ok := gw["users"].([]any)
	if !ok {
		return ""
	}
	for _, u := range list {
		um, isMap := u.(map[string]any)
		if !isMap {
			continue
		}
		if um["username"] == username {
			hash, _ := um["password_hash"].(string)
			return hash
		}
	}
	return ""
}

// auditEvents returns every audit entry written so far, flushed to disk.
func (e *onboardAuthEnv) auditEvents(t *testing.T) []map[string]any {
	t.Helper()
	require.NoError(t, e.api.auditor.Close())
	return readAuditLog(t, e.auditDir)
}

// TestOnboardingComplete_RealMux_RefusesWhenAuthenticationAuthorityExists is
// the direct reproduction of the reported bypass: users present,
// onboarding.completed=false, anonymous POST.
//
// Before the fix this returned 200 with a bearer token and gateway.users
// became ["realoperator","attacker"].
func TestOnboardingComplete_RealMux_RefusesWhenAuthenticationAuthorityExists(t *testing.T) {
	env := newOnboardAuthEnv(t, "realoperator")

	w := env.postCompleteViaRealMux(t, "203.0.113.11", "attacker")

	require.Equal(t, http.StatusConflict, w.Code,
		"an instance that already has an authentication authority must refuse to mint another admin, "+
			"whatever the onboarding flag says; body=%s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "token",
		"a refused request must not hand back a bearer token")

	assert.Equal(t, []string{"realoperator"}, env.diskUsers(t),
		"gateway.users must be UNCHANGED — no account may be created by a refused request")
	assert.False(t, env.api.onboardingMgr.IsComplete(),
		"a refused request must not mark onboarding complete either")
}

// TestOnboardingComplete_RealMux_FreshInstallStillSucceeds guards the other
// direction. Locking the gate down must not re-break first-run onboarding,
// which was itself a release blocker fixed only hours before this change:
// a genuine fresh install has NO users and NO state.json, and must complete.
func TestOnboardingComplete_RealMux_FreshInstallStillSucceeds(t *testing.T) {
	env := newOnboardAuthEnv(t) // no existing users

	w := env.postCompleteViaRealMux(t, "203.0.113.12", "firstoperator")

	require.Equal(t, http.StatusOK, w.Code,
		"a genuine fresh install must still be able to onboard; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"], "a successful onboarding issues a bearer token")
	assert.Equal(t, "firstoperator", resp["username"])

	assert.Equal(t, []string{"firstoperator"}, env.diskUsers(t),
		"the admin account must actually be persisted")
	assert.True(t, env.api.onboardingMgr.IsComplete(),
		"a successful onboarding marks state.json complete")
}

// TestOnboardingComplete_RealMux_NeverOverwritesAnExistingPassword pins the
// UAT-reported takeover variant: naming an EXISTING username silently
// replaced that account's password_hash and tokens, after which the original
// password 401'd and the attacker's worked.
//
// The authority gate already refuses this request before the body is read.
// This test asserts the OUTCOME that matters regardless of which layer stops
// it — the victim's stored credential is byte-for-byte unchanged — so it
// still fails if the gate is ever relaxed while the overwrite branch is back.
func TestOnboardingComplete_RealMux_NeverOverwritesAnExistingPassword(t *testing.T) {
	env := newOnboardAuthEnv(t, "realoperator")
	before := env.diskPasswordHash(t, "realoperator")
	require.NotEmpty(t, before, "fixture must have stored a password hash to protect")

	w := env.postCompleteViaRealMux(t, "203.0.113.13", "realoperator")

	require.Equal(t, http.StatusConflict, w.Code,
		"re-using an existing username must be refused, not treated as idempotent success; body=%s",
		w.Body.String())
	assert.Equal(t, before, env.diskPasswordHash(t, "realoperator"),
		"creating an admin must NEVER overwrite an existing account's password as a side effect")
	assert.Equal(t, []string{"realoperator"}, env.diskUsers(t),
		"and must not duplicate the row either")
}

// TestOnboardingComplete_RealMux_UsernameCollisionRefusedInsideTheMutation is
// the defence-in-depth half of the test above. It bypasses the phase-0
// authority gate the only way production never can — by handing the handler a
// request context whose config carries NO users, while config.json on disk
// does — and asserts the config mutation itself still refuses rather than
// overwriting. Without the mutation-level check this returns 200 and rewrites
// realoperator's password.
func TestOnboardingComplete_RealMux_UsernameCollisionRefusedInsideTheMutation(t *testing.T) {
	env := newOnboardAuthEnv(t, "realoperator")
	before := env.diskPasswordHash(t, "realoperator")
	require.NotEmpty(t, before)

	// A config snapshot with no users: hasAuthenticationAuthority consults the
	// request context FIRST, so the gate sees no authority and opens the
	// window, and the request reaches the config mutation — which reads
	// config.json off disk, where realoperator still is.
	// Built fresh rather than copied: config.Config embeds a sync.RWMutex.
	blindCfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         env.home,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}

	mux := http.NewServeMux()
	env.api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-attacker"},`+
			`"admin":{"username":"realoperator","password":"attackerpw123"}}`,
		env.upstream,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.14:54321"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req.WithContext(withConfigSnapshot(req.Context(), blindCfg)))

	require.Equal(t, http.StatusConflict, w.Code,
		"the config mutation must refuse a username collision on its own; body=%s", w.Body.String())
	assert.Equal(t, before, env.diskPasswordHash(t, "realoperator"),
		"the victim's password hash must survive a request that reached the mutation")
}

// TestOnboardingComplete_RealMux_AuditsAdminCreation pins SEC-15 for the one
// moment this product mints an authentication authority. UAT found NO record
// of either the account creation or the password change anywhere in the audit
// log — the exact forensic gap SEC-15 exists to prevent.
func TestOnboardingComplete_RealMux_AuditsAdminCreation(t *testing.T) {
	env := newOnboardAuthEnv(t)

	w := env.postCompleteViaRealMux(t, "203.0.113.15", "firstoperator")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entry map[string]any
	for _, line := range env.auditEvents(t) {
		if line["event"] == audit.EventOnboardingAdminCreated {
			entry = line
			break
		}
	}
	require.NotNil(t, entry,
		"creating the administrator account must write an %q audit entry",
		audit.EventOnboardingAdminCreated)

	assert.Equal(t, audit.DecisionAllow, entry["decision"])
	assert.Equal(t, "firstoperator", entry["user"])
	assert.NotEmpty(t, entry["timestamp"], "SEC-15 requires a real timestamp")
	assert.NotEmpty(t, entry["policy_rule"], "SEC-17 requires an explanation on every decision")

	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "the entry must carry details")
	assert.Equal(t, "firstoperator", details["username"])
	assert.Equal(t, "203.0.113.15", details["source_ip"],
		"the source IP is the forensic field that makes an unattributed mint traceable")
	assert.Equal(t, "openai", details["provider"])
	assert.Equal(t, "/api/v1/onboarding/complete", details["route"])

	assert.NotContains(t, w.Body.String(), "attackerpw123")
	rendered, err := json.Marshal(entry)
	require.NoError(t, err)
	assert.NotContains(t, string(rendered), "secret",
		"the audit entry must never carry the chosen password")
	assert.NotContains(t, string(rendered), "sk-attacker",
		"nor the provider API key")
}

// TestOnboardingComplete_RealMux_AuditsRefusal pins the other half of the
// forensic record: the attempt that was refused must be visible too, with the
// reason and the source IP.
func TestOnboardingComplete_RealMux_AuditsRefusal(t *testing.T) {
	env := newOnboardAuthEnv(t, "realoperator")

	w := env.postCompleteViaRealMux(t, "203.0.113.16", "attacker")
	require.Equal(t, http.StatusConflict, w.Code)

	var entry map[string]any
	for _, line := range env.auditEvents(t) {
		if line["event"] == audit.EventOnboardingRefused {
			entry = line
			break
		}
	}
	require.NotNil(t, entry, "a refused admin-minting attempt must be audited")
	assert.Equal(t, audit.DecisionDeny, entry["decision"])
	assert.NotEmpty(t, entry["policy_rule"], "SEC-17 requires an explanation on every deny")

	details, ok := entry["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "authentication_authority_exists", details["reason"])
	assert.Equal(t, "203.0.113.16", details["source_ip"])
}

// TestOnboardingComplete_RefusalBodyRevealsNothing checks the anti-oracle
// property the shared refusal message exists for. In the divergent state
// GET /api/v1/state still reports onboarding_complete=false, so a
// reason-specific refusal body here would be the one signal telling an
// anonymous caller that this instance is in the interesting state. The
// already-complete refusal and the authority refusal must be byte-identical.
func TestOnboardingComplete_RefusalBodyRevealsNothing(t *testing.T) {
	divergent := newOnboardAuthEnv(t, "realoperator")
	divergentBody := divergent.postCompleteViaRealMux(t, "203.0.113.17", "attacker").Body.String()

	onboarded := newOnboardAuthEnv(t)
	require.NoError(t, onboarded.api.onboardingMgr.CompleteOnboarding())
	onboardedBody := onboarded.postCompleteViaRealMux(t, "203.0.113.18", "attacker").Body.String()

	assert.Equal(t, onboardedBody, divergentBody,
		"the divergent state must be indistinguishable from an ordinary onboarded instance "+
			"in the response body; the reason belongs in the audit log")
}
