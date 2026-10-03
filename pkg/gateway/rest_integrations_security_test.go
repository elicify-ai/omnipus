// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// PR #1116 security RED pack. Oracles: design.md, "Security review of PR
// #1116 — rulings 2026-10-01", SEC-1/2/3, and Decisions #1104/#1105.
// Every request crosses registerAdditionalEndpoints, real authentication,
// configSnapshotMiddleware and the production handler. Search HTTP responses
// and a filesystem fault are process-edge controls, not mocks of admission or
// reload. GREEN and mutation probes are deferred to an independent CHECK.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Independent existing guard oracle: RequireNotBypass and its
// TestRequireNotBypass_BypassOn_Returns503; SEC-1 expressly requires reuse.
const integrationSecurityBypassError = "this action is disabled while dev_mode_bypass is active"

func newIntegrationSecurityAPI(t *testing.T) (*restAPI, *config.Config, http.Handler) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	api, cfg, mux := newSearchCheckMux(t)

	// Keep the real identity and isolated agent home on disk too. Removal's
	// config refresh swaps pointers; an in-memory-only token would make the
	// required post-change catalogue fetch fail for an unrelated reason.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	agents, ok := doc["agents"].(map[string]any)
	require.True(t, ok)
	defaults, ok := agents["defaults"].(map[string]any)
	require.True(t, ok)
	defaults["workspace"] = cfg.Agents.Defaults.Home
	doc["gateway"] = cfg.Gateway
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))

	handler := api.configSnapshotMiddleware(mux)
	w := integrationSecurityRequest(t.Context(), handler, http.MethodGet,
		"/api/v1/integrations/providers", "", diagnosticBearer, "")
	require.Equal(t, http.StatusOK, w.Code, "instrument: registered authenticated route; body=%s", w.Body.String())
	return api, cfg, handler
}

func integrationSecurityRequest(ctx context.Context, handler http.Handler, method, path, body, bearer, consent string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if consent != "" {
		r.Header.Set(reAuthHeader, consent)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func integrationSecurityConsent(t *testing.T, handler http.Handler) string {
	t.Helper()
	if config.EditionAuthMode() == config.AuthModePlatform {
		return ""
	}
	body, err := json.Marshal(gen.ReAuthRequest{Password: rolesTestPassword})
	require.NoError(t, err)
	w := integrationSecurityRequest(t.Context(), handler, http.MethodPost,
		"/api/v1/auth/reauth", string(body), diagnosticBearer, "")
	require.Equal(t, http.StatusOK, w.Code, "instrument: real registered password consent; body=%s", w.Body.String())
	var consent gen.ReAuthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &consent))
	require.Equal(t, true, consent.Verified)
	require.NotEqual(t, "", consent.Token)
	return consent.Token
}

func assertIntegrationSecurityBypassRefusal(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"SEC-1: anonymous dev bypass must get the existing guard refusal; body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{"error": integrationSecurityBypassError}, body,
		"SEC-1: reuse the existing refusal, not a password-consent error or success envelope")
}

func integrationSecurityAnonymousBypass(t *testing.T, cfg *config.Config, handler http.Handler) {
	t.Helper()
	cfg.Gateway.DevModeBypass = true
	// No Authorization header or cookie is sent. Configured users remain so
	// this also catches the real bypass path on an already-onboarded install.
	w := integrationSecurityRequest(t.Context(), handler, http.MethodGet,
		"/api/v1/config/pending-restart", "", "", "")
	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"instrument: existing registered guard must see the bypass snapshot; body=%s", w.Body.String())
	require.Equal(t, integrationSecurityBypassError, searchSettingError(t, w.Body.Bytes()))
}

func integrationSecuritySearchEdge(t *testing.T, api *restAPI, cfg *config.Config) *atomic.Int32 {
	t.Helper()
	calls := new(atomic.Int32)
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		payload, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		var body map[string]any
		assert.NoError(t, json.Unmarshal(payload, &body))
		assert.Equal(t, "Omnipus", body["query"], "Decision #1105: no conversation data")
		assert.Equal(t, float64(1), body["max_results"], "Decision #1105: one result")
		assert.Equal(t, "fast", body["search_depth"], "Decision #1105 and ADR-096 D12: cheapest supported diagnostic depth")
		assert.Equal(t, searchSettingsSecret, body["api_key"], "the real client must use the saved key")
		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"results":[]}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(edge.Close)
	wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
	return calls
}

func TestIntegrationSecurity_CheckRejectsAnonymousBypass(t *testing.T) {
	for _, edition := range []string{config.EditionCore, config.EditionHosted} {
		t.Run(edition, func(t *testing.T) {
			withEdition(t, edition)
			api, cfg, handler := newIntegrationSecurityAPI(t)
			calls := integrationSecuritySearchEdge(t, api, cfg)
			integrationSecurityAnonymousBypass(t, cfg, handler)
			before := readRolesWebConfig(t, api)

			w := integrationSecurityRequest(t.Context(), handler, http.MethodPost,
				"/api/v1/integrations/providers/tavily/check", `{}`, "", "")
			assertIntegrationSecurityBypassRefusal(t, w)
			assert.Equal(t, int32(0), calls.Load(), "SEC-1: refused checks must not spend the saved key")
			assert.Equal(t, before, readRolesWebConfig(t, api))
			requireSavedSearchKey(t, api)
		})
	}
}

func TestIntegrationSecurity_RemovalRejectsAnonymousBypassBothEditions(t *testing.T) {
	for _, edition := range []string{config.EditionCore, config.EditionHosted} {
		t.Run(edition, func(t *testing.T) {
			withEdition(t, edition)
			api, cfg, handler := newIntegrationSecurityAPI(t)
			wireIntegrationSecurityReload(t, api)
			integrationSecurityAnonymousBypass(t, cfg, handler)
			beforeConfig, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			beforeSecrets, err := os.ReadFile(api.credStore.Path())
			require.NoError(t, err)

			w := integrationSecurityRequest(t.Context(), handler, http.MethodPut,
				"/api/v1/integrations/providers/tavily", removeSearchKeyJSON, "", "")
			assertIntegrationSecurityBypassRefusal(t, w)
			afterConfig, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			afterSecrets, err := os.ReadFile(api.credStore.Path())
			require.NoError(t, err)
			assert.Equal(t, beforeConfig, afterConfig, "SEC-1: refusal must not rewrite configuration")
			assert.Equal(t, beforeSecrets, afterSecrets, "SEC-1: refusal must not delete any encrypted entry")
			assert.Equal(t, searchSettingsSecret, os.Getenv("TAVILY_API_KEY"), "refusal must keep the injected key")
			requireSavedSearchKey(t, api)
		})
	}
}

// Characterization test: SEC-1 says ordinary saves stay as they are.
// Existing save consent is 403 without a password token in Core; Hosted
// permits the authenticated bypass identity. This does not endorse bypass
// saves as a new security policy and must not blanket-guard all provider PUTs.
func TestIntegrationSecurity_SaveUnderBypassPreservesConsentBehavior_Control(t *testing.T) {
	for _, edition := range []string{config.EditionCore, config.EditionHosted} {
		t.Run(edition, func(t *testing.T) {
			withEdition(t, edition)
			api, cfg, handler := newIntegrationSecurityAPI(t)
			integrationSecurityAnonymousBypass(t, cfg, handler)
			before := readRolesWebConfig(t, api)
			const replacement = "replacement-key-for-ordinary-save-control"
			var cycles <-chan integrationSecurityReloadResult
			if edition == config.EditionHosted {
				_, cycles = wireIntegrationSecurityReload(t, api)
			}

			w := integrationSecurityRequest(t.Context(), handler, http.MethodPut,
				"/api/v1/integrations/providers/tavily",
				`{"kind":"search","api_key":"`+replacement+`"}`, "", "")
			if edition == config.EditionCore {
				require.Equal(t, http.StatusForbidden, w.Code, "ordinary local save still requires password consent")
				assert.Equal(t, "this change requires re-typing your password — call POST /api/v1/auth/reauth first",
					searchSettingError(t, w.Body.Bytes()))
				assert.Equal(t, before, readRolesWebConfig(t, api))
				requireSavedSearchKey(t, api)
				return
			}
			require.Equal(t, http.StatusOK, w.Code, "ordinary platform save must not acquire the removal-only bypass guard; body=%s", w.Body.String())
			require.NoError(t, receiveIntegrationSecurityReload(t, cycles).err)
			key, err := api.credStore.Get("TAVILY_API_KEY")
			require.NoError(t, err)
			assert.Equal(t, replacement, key)
			assert.Equal(t, replacement, os.Getenv("TAVILY_API_KEY"))
			assertIntegrationSecurityRoles(t, before, readRolesWebConfig(t, api))
			assert.NotContains(t, w.Body.String(), replacement)
		})
	}
}

func TestIntegrationSecurity_CheckWithRealIdentity_Control(t *testing.T) {
	for _, edition := range []string{config.EditionCore, config.EditionHosted} {
		t.Run(edition, func(t *testing.T) {
			withEdition(t, edition)
			api, cfg, handler := newIntegrationSecurityAPI(t)
			calls := integrationSecuritySearchEdge(t, api, cfg)
			before := readRolesWebConfig(t, api)

			w := integrationSecurityRequest(t.Context(), handler, http.MethodPost,
				"/api/v1/integrations/providers/tavily/check", `{}`, diagnosticBearer, "")
			requireSearchCheckResult(t, w, "tavily", "success")
			assert.Equal(t, int32(1), calls.Load(), "SEC-1/3: authenticated, unobstructed check remains callable")
			assert.Equal(t, before, readRolesWebConfig(t, api))
			requireSavedSearchKey(t, api)

			// Positive instrument: a genuinely admitted check DOES spend the
			// shared 30s cooldown, unlike an expired/cancelled admission.
			repeat := integrationSecurityRequest(t.Context(), handler, http.MethodPost,
				"/api/v1/integrations/providers/tavily/check", `{}`, diagnosticBearer, "")
			assert.Equal(t, http.StatusTooManyRequests, repeat.Code)
			retry, err := strconv.Atoi(repeat.Header().Get("Retry-After"))
			require.NoError(t, err)
			assert.GreaterOrEqual(t, retry, 1)
			assert.LessOrEqual(t, retry, 30, "Decision #1105: 30-second cooldown")
			assert.Equal(t, int32(1), calls.Load(), "local throttle must not reach the provider")
		})
	}
}

// not-wire-format: in-process observation of a real reload cycle, not an API type.
type integrationSecurityReloadResult struct {
	err                    error
	keyRemovalPublished    bool
	pendingDuringExecution bool
}

func wireIntegrationSecurityReload(t *testing.T, api *restAPI) (*services, <-chan integrationSecurityReloadResult) {
	t.Helper()
	msgBus := bus.NewMessageBus()
	cm, err := channels.NewManager(api.agentLoop.GetConfig(), credentials.SecretBundle{}, msgBus, nil)
	require.NoError(t, err)
	rs := &services{
		ChannelManager:   cm,
		homePath:         api.homePath,
		credStore:        api.credStore,
		restAPIRef:       api,
		manualReloadChan: make(chan struct{}, 1),
		reloadOutcome:    &reloadOutcomeTracker{},
	}
	loader := newReloadConfigLoader(api.configPath(), api.homePath, api.credStore, rs)
	rs.loadConfigForSwap = loader
	api.reloadOutcome = rs.reloadOutcome
	rs.reloadOutcome.onSuccess = api.pendingApply.clearAfterReload
	api.agentLoop.SetReloadFunc(newReloadTrigger(rs, api.agentLoop))

	results := make(chan integrationSecurityReloadResult, 1)
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		provider := providers.LLMProvider(&restMockProvider{})
		for {
			select {
			case <-stop:
				return
			case <-rs.manualReloadChan:
				var result integrationSecurityReloadResult
				exec := func(next *config.Config) error {
					published := api.agentLoop.GetConfig()
					result.keyRemovalPublished = !published.Tools.Web.Tavily.Enabled && published.Tools.Web.Tavily.APIKeyRef == ""
					result.pendingDuringExecution = api.agentLoop.IsReloadPending()
					// No synthetic error: rebuild, swap, and restart the real
					// services. Empty-model startup avoids paid LLM traffic.
					result.err = executeReload(t.Context(), api.agentLoop, next, &provider, rs, msgBus, true)
					return result.err
				}
				runReloadCycle(api.agentLoop, rs, nil, 0, exec, loader)
				results <- result
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("instrument: real reload consumer did not stop before fixture cleanup")
		}
		stopAndCleanupServices(rs, 5*time.Second, true)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, cm.StopAll(ctx))
	})
	return rs, results
}

func receiveIntegrationSecurityReload(t *testing.T, results <-chan integrationSecurityReloadResult) integrationSecurityReloadResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: no completion receipt from the real asynchronous reload cycle")
		return integrationSecurityReloadResult{}
	}
}

func assertIntegrationSecurityRoles(t *testing.T, before, after map[string]any) {
	t.Helper()
	for _, field := range []string{"default_provider", "fallback_provider", "roles_migrated_at"} {
		assert.Equal(t, before[field], after[field], "Decision #1104: removal/save must preserve raw %s", field)
	}
}

func assertIntegrationSecurityRemovedState(t *testing.T, api *restAPI, handler http.Handler, before map[string]any) {
	t.Helper()
	_, err := api.credStore.Get("TAVILY_API_KEY")
	var missing *credentials.NotFoundError
	require.ErrorAs(t, err, &missing, "Decision #1104: delete the entry, not replace it with an empty secret")
	assert.Equal(t, "TAVILY_API_KEY", missing.Name)
	reopened := credentials.NewStore(api.credStore.Path())
	require.NoError(t, credentials.Unlock(reopened))
	t.Cleanup(reopened.Close)
	_, err = reopened.Get("TAVILY_API_KEY")
	require.ErrorAs(t, err, &missing, "deletion must survive reopening the encrypted store")

	after := readRolesWebConfig(t, api)
	assertIntegrationSecurityRoles(t, before, after)
	section := roleSection(t, after, "tavily")
	assert.Equal(t, false, section["enabled"])
	ref, present := section["api_key_ref"]
	assert.Equal(t, true, present, "explicit empty reference must survive persistence")
	assert.Equal(t, "", ref)
	assert.Equal(t, "", os.Getenv("TAVILY_API_KEY"))
	fresh, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	assert.Equal(t, false, fresh.Tools.Web.Tavily.Enabled)
	assert.Equal(t, "", fresh.Tools.Web.Tavily.APIKeyRef)

	w := integrationSecurityRequest(t.Context(), handler, http.MethodGet,
		"/api/v1/integrations/providers", "", diagnosticBearer, "")
	require.Equal(t, http.StatusOK, w.Code, "Decision #1104: refetch persisted state after success or partial failure; body=%s", w.Body.String())
	var catalogue gen.IntegrationProvidersResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &catalogue))
	row := searchRow(t, catalogue, "tavily")
	assert.Equal(t, false, row.Configured)
	require.NotNil(t, row.Usable)
	assert.Equal(t, false, *row.Usable)
	assert.NotContains(t, w.Body.String(), searchSettingsSecret)
}

func TestIntegrationSecurity_RemovalWithAppliedReload_Control(t *testing.T) {
	for _, edition := range []string{config.EditionCore, config.EditionHosted} {
		t.Run(edition, func(t *testing.T) {
			withEdition(t, edition)
			api, _, handler := newIntegrationSecurityAPI(t)
			before := readRolesWebConfig(t, api)
			rs, cycles := wireIntegrationSecurityReload(t, api)
			consent := integrationSecurityConsent(t, handler)

			w := integrationSecurityRequest(t.Context(), handler, http.MethodPut,
				"/api/v1/integrations/providers/tavily", removeSearchKeyJSON, diagnosticBearer, consent)
			cycle := receiveIntegrationSecurityReload(t, cycles)
			require.NoError(t, cycle.err, "instrument: unmodified real reload must apply")
			assert.Equal(t, true, cycle.pendingDuringExecution, "instrument: this was asynchronous execution, not an immediate trigger return")
			assert.Equal(t, true, cycle.keyRemovalPublished)
			assert.Equal(t, false, rs.reloadOutcome.lastFailed())
			require.Equal(t, http.StatusOK, w.Code, "SEC-1/2: real identity and applied reload retain removal success; body=%s", w.Body.String())
			var response gen.IntegrationProvidersResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			row := searchRow(t, response, "tavily")
			assert.Equal(t, false, row.Configured)
			require.NotNil(t, row.Usable)
			assert.Equal(t, false, *row.Usable)
			assertIntegrationSecurityRemovedState(t, api, handler, before)
			assert.Equal(t, map[string]any{
				"provider": "tavily", "kind": "search", "action": "remove_key",
				"key_removed": true, "roles_changed": false, "enabled": false, "outcome": "removed",
			}, removalAuditValues(t, api))
		})
	}
}

func TestIntegrationSecurity_RemovalWithFailedAsyncReloadReportsPartialFailure(t *testing.T) {
	withEdition(t, config.EditionCore)
	api, cfg, handler := newIntegrationSecurityAPI(t)
	persisted, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	require.Equal(t, cfg.AgentHomeBasePath(), persisted.AgentHomeBasePath(),
		"instrument: reload must read the same isolated workspace where the filesystem fault is installed")
	before := readRolesWebConfig(t, api)
	rs, cycles := wireIntegrationSecurityReload(t, api)
	consent := integrationSecurityConsent(t, handler)

	// Real filesystem fault: CronService.Start must read a file here after
	// config publication AND the registry swap. A directory is unreadable as
	// JSON even for root, unlike a permission-bit fault. No reload function
	// or returned error is stubbed.
	jobsPath := filepath.Join(cfg.AgentHomeBasePath(), "cron", "jobs.json")
	require.NoError(t, os.MkdirAll(jobsPath, 0o700))
	w := integrationSecurityRequest(t.Context(), handler, http.MethodPut,
		"/api/v1/integrations/providers/tavily", removeSearchKeyJSON, diagnosticBearer, consent)
	cycle := receiveIntegrationSecurityReload(t, cycles)
	require.ErrorContains(t, cycle.err, "error restarting cron service: failed to load store:",
		"instrument: CronService.Start must fail reading the jobs store in the later service-restart stage")
	var filesystemErr *os.PathError
	require.ErrorAs(t, cycle.err, &filesystemErr, "instrument: reload must expose the real filesystem error")
	require.Equal(t, jobsPath, filesystemErr.Path, "instrument: the failing read must target the installed filesystem fault")
	require.Equal(t, true, cycle.keyRemovalPublished, "instrument: disabled config was published before the failing cycle")
	require.Equal(t, true, cycle.pendingDuringExecution, "instrument: the real trigger accepted and queued this cycle")
	require.Equal(t, true, rs.reloadOutcome.lastFailed(), "instrument: the completed cycle recorded execution failure")
	require.Equal(t, false, api.agentLoop.IsReloadPending(), "instrument: pending cleared despite execution failure")

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"SEC-2: a failed asynchronous restart is partial failure, never removal-success HTTP 200; reload_error=%v; disabled_config_published=%t; pending_during_execution=%t; body=%s",
		cycle.err, cycle.keyRemovalPublished, cycle.pendingDuringExecution, w.Body.String())
	message := searchSettingError(t, w.Body.Bytes())
	assert.Contains(t, message, "saved key was removed", "Decision #1104: disclose the persisted deletion")
	assert.Contains(t, message, "disabled configuration was saved", "Decision #1104: disclose saved disabled state")
	assert.Contains(t, message, "reload", "Decision #1104: name the failed stage")
	assert.Contains(t, message, "not confirmed", "Decision #1104: do not falsely confirm runtime switching-off")
	assert.NotContains(t, message, "The service has been switched off.")
	assert.NotContains(t, w.Body.String(), searchSettingsSecret)
	assertIntegrationSecurityRemovedState(t, api, handler, before)
	assert.Equal(t, map[string]any{
		"provider": "tavily", "kind": "search", "action": "remove_key",
		"key_removed": true, "roles_changed": false, "enabled": false,
		"outcome": "partial_failure", "failed_stage": "reload",
	}, removalAuditValues(t, api), "SEC-2: a failed cycle must not produce a successful removal audit")
}

// not-wire-format: request-input synchronization, not a handler/admission mock.
type integrationSecurityGatedBody struct {
	reader  *strings.Reader
	reached chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (body *integrationSecurityGatedBody) Read(p []byte) (int, error) {
	body.once.Do(func() {
		close(body.reached)
		<-body.resume
	})
	return body.reader.Read(p)
}

func (*integrationSecurityGatedBody) Close() error { return nil }

func startIntegrationSecurityWaitingCheck(t *testing.T, ctx context.Context, handler http.Handler) (<-chan *httptest.ResponseRecorder, func()) {
	t.Helper()
	body := &integrationSecurityGatedBody{
		reader: strings.NewReader(`{}`), reached: make(chan struct{}), resume: make(chan struct{}),
	}
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(body.resume) }) }
	r := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/providers/tavily/check", nil).WithContext(ctx)
	r.Body = body
	r.ContentLength = 2
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+diagnosticBearer)
	done := make(chan *httptest.ResponseRecorder, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		done <- w
	}()
	t.Cleanup(func() {
		resume()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("instrument: request handler remained alive during fixture cleanup")
		}
	})
	select {
	case <-body.reached:
		return done, resume
	case <-time.After(3 * time.Second):
		t.Fatal("instrument: registered/authenticated handler never reached request-body decoding")
		return done, resume
	}
}

func holdIntegrationSecurityWriter(t *testing.T, api *restAPI, which string) func() {
	t.Helper()
	var release func()
	if which == "gateway-config" {
		api.configMu.Lock()
		var once sync.Once
		release = func() { once.Do(api.configMu.Unlock) }
	} else {
		acquired := make(chan struct{})
		resume := make(chan struct{})
		finished := make(chan error, 1)
		go func() {
			finished <- api.agentLoop.MutateConfig(func(*config.Config) error {
				close(acquired)
				<-resume
				return nil
			})
		}()
		var once sync.Once
		release = func() {
			once.Do(func() {
				close(resume)
				select {
				case err := <-finished:
					assert.NoError(t, err, "instrument: real loop writer must finish")
				case <-time.After(3 * time.Second):
					t.Error("instrument: real loop writer did not release")
				}
			})
		}
		t.Cleanup(release)
		select {
		case <-acquired:
		case <-time.After(3 * time.Second):
			t.Fatal("instrument: real loop configuration writer was not acquired")
		}
		return release
	}
	t.Cleanup(release)
	return release
}

func waitIntegrationSecurityAdmission(t *testing.T) {
	t.Helper()
	// A scheduling instrument only: prove the authenticated request has
	// reached real admission while our writer is held BEFORE cancelling.
	// No output expectation comes from these frames and no function is mocked.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		if strings.Contains(string(buf[:n]), "(*restAPI).admitSearchConnectionCheck(") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("instrument: request was not observed inside real admission while the writer was held")
}

func receiveIntegrationSecurityCheck(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-time.After(3 * time.Second):
		t.Fatal("instrument: handler did not finish after releasing the writer")
		return nil
	}
}

func assertIntegrationSecurityNextCheckAdmitted(t *testing.T, handler http.Handler, calls *atomic.Int32) {
	t.Helper()
	assert.Equal(t, int32(0), calls.Load(), "SEC-3: expired/cancelled admission must not contact the provider")
	started := time.Now()
	w := integrationSecurityRequest(t.Context(), handler, http.MethodPost,
		"/api/v1/integrations/providers/tavily/check", `{}`, diagnosticBearer, "")
	assert.Less(t, time.Since(started), time.Second, "SEC-3: the next live request must be admitted immediately")
	requireSearchCheckResult(t, w, "tavily", "success")
	assert.Equal(t, int32(1), calls.Load(), "SEC-3: only the next live check may use the saved key")
}

func TestIntegrationSecurity_CheckAdmissionHonorsFifteenSecondBudgetAndKeepsCooldown(t *testing.T) {
	withEdition(t, config.EditionCore)
	api, cfg, handler := newIntegrationSecurityAPI(t)
	calls := integrationSecuritySearchEdge(t, api, cfg)
	done, resume := startIntegrationSecurityWaitingCheck(t, t.Context(), handler)
	release := holdIntegrationSecurityWriter(t, api, "gateway-config")
	started := time.Now()
	resume()
	waitIntegrationSecurityAdmission(t)

	// Exercise the real 15-second budget ONCE, without a shorter parent
	// deadline or changing production constants. The writer stays held until
	// the response or the dispatch's strict 16-second upper bound.
	timer := time.NewTimer(time.Until(started.Add(16 * time.Second)))
	defer timer.Stop()
	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
		elapsed := time.Since(started)
		assert.GreaterOrEqual(t, elapsed, 14*time.Second, "real 15s budget, allowing up to 1s of scheduling variation")
		assert.LessOrEqual(t, elapsed, 16*time.Second, "SEC-3: 15s total budget includes admission")
		t.Logf("SEC-3 admission deadline elapsed=%s; writer still held", elapsed)
	case <-timer.C:
		t.Errorf("SEC-3: check still blocked on configuration writer after %s; must return timeout within 16s WITHOUT releasing the writer", time.Since(started))
		release()
		w = receiveIntegrationSecurityCheck(t, done)
	}
	release()
	requireSearchCheckResult(t, w, "tavily", "timeout")
	assertIntegrationSecurityNextCheckAdmitted(t, handler, calls)
}

func TestIntegrationSecurity_CancelledAdmissionReturnsPromptlyAndKeepsCooldown(t *testing.T) {
	for _, writer := range []string{"gateway-config", "agent-loop-config"} {
		t.Run(writer, func(t *testing.T) {
			withEdition(t, config.EditionCore)
			api, cfg, handler := newIntegrationSecurityAPI(t)
			calls := integrationSecuritySearchEdge(t, api, cfg)
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			done, resume := startIntegrationSecurityWaitingCheck(t, ctx, handler)
			release := holdIntegrationSecurityWriter(t, api, writer)
			resume()
			waitIntegrationSecurityAdmission(t)

			started := time.Now()
			cancel() // the actual request context, as for a disconnected client
			select {
			case <-done:
				elapsed := time.Since(started)
				assert.Less(t, elapsed, time.Second, "SEC-3: cancelled admission must return while writer remains held")
				t.Logf("SEC-3 cancelled admission elapsed=%s; %s writer still held", elapsed, writer)
			case <-time.After(time.Second):
				t.Errorf("SEC-3: cancelled check did not finish within 1s while the %s writer remained held", writer)
				release()
				receiveIntegrationSecurityCheck(t, done)
			}
			release()
			assert.ErrorIs(t, ctx.Err(), context.Canceled)
			// No invented 'cancelled' wire category: the contract has none.
			// The ruling specifies prompt termination/no call/no cooldown.
			assertIntegrationSecurityNextCheckAdmitted(t, handler, calls)
		})
	}
}
