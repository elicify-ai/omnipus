// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md,
// the 2026-10-01 SF-2 server ruling, Decision #1105 and Privacy/SF-4;
// contracts/openapi.yaml's POST connection-check 409 contract;
// docs/settings.md's credential-store recovery text; and the F1/F2/F3 dispatch.
// These close the independently frozen proof-be2 audit's three survivors.
// F1 drives handleConfigReload, not wireRolesReload. F2 completes a real save
// BEFORE admission. F3 observes real store failures and actual slog output.
// Only the external search HTTP response is held. Independent CHECK, not this
// author, supplies mutation proof and the audit verdict.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

const (
	sf2GapConflictMessage = "The key changed while checking. Check again."
	sf2GapRecoveryMessage = "Could not read the saved key from the credential store. Unlock or repair it, then try again."
	// Synthetic test-only keys; never print their values in failure messages.
	sf2GapPriorKey       = "sf2-prior-completed-save-fixture"
	sf2GapReplacementKey = "sf2-held-replacement-save-fixture"
)

func TestSearchConnectionCheck_RealConfigReloadInvalidatesHeldOutcomeWithNoReloadControl(t *testing.T) {
	for _, reload := range []bool{false, true} {
		name := "no_reload_returns_200"
		if reload {
			name = "successful_real_reload_returns_409"
		}
		t.Run(name, func(t *testing.T) {
			api, cfg, mux := newSearchCheckMux(t)
			t.Cleanup(api.credStore.Close)
			edge := newSF2GapHeldProvider(t)
			wireSearchCheckEdge(t, api, cfg, "tavily", edge.server.URL)
			running, msgBus := prepareSF2GapRealReload(t, api, cfg)
			before := api.agentLoop.GetConfig()
			finish := startSF2GapHeldCheck(t, mux, edge, searchSettingsSecret)

			if reload {
				loaded, err := config.LoadConfigWithStore(api.configPath(), api.credStore)
				require.NoError(t, err)
				provider := providers.LLMProvider(&restMockProvider{})
				// This returns only after the REAL registry/config swap and service
				// restart. The fixture's SetReloadFunc callback is not invoked.
				require.NoError(t, handleConfigReload(t.Context(), api.agentLoop, loaded, &provider, running, msgBus, true),
					"F1 requires a successful production reload before releasing the old provider response")
				after := api.agentLoop.GetConfig()
				require.NotSame(t, before, after, "real reload must actually swap the running configuration")
				require.Equal(t, edge.server.URL, after.Tools.Web.Tavily.BaseURL, "reload must apply the real persisted search configuration")
				require.True(t, after.Tools.Web.UsableSearchProvider("tavily"), "unchanged key remains usable after reload")
				t.Log("successful handleConfigReload completed while the original provider response was held")
			}

			response := finish()
			if reload {
				requireSF2GapError(t, response, http.StatusConflict, sf2GapConflictMessage)
			} else {
				requireSearchCheckResult(t, response, "tavily", "success")
			}
		})
	}
}

func TestSearchConnectionCheck_NonzeroAdmissionAfterRealSaveAcceptsUnchangedAndRejectsReplacement(t *testing.T) {
	// Separate fixtures each admit one check after a completed save. This tests
	// nonzero admission without changing cooldown state or sleeping 30 seconds.
	for _, replace := range []bool{false, true} {
		name := "prior_save_then_unchanged_returns_200"
		if replace {
			name = "prior_save_then_held_replacement_returns_409"
		}
		t.Run(name, func(t *testing.T) {
			api, cfg, mux := newSearchCheckMux(t)
			t.Cleanup(api.credStore.Close)
			edge := newSF2GapHeldProvider(t)
			wireSearchCheckEdge(t, api, cfg, "tavily", edge.server.URL)
			require.Len(t, cfg.Gateway.Users, 1)
			user := cfg.Gateway.Users[0]
			// newSearchCheckMux seeds bearer authentication only in memory.
			// Persist it before the real save refreshes config from disk, so
			// the subsequently admitted check remains a real authenticated POST.
			require.NoError(t, api.safeUpdateConfigJSON(func(raw map[string]any) error {
				ensureMap(raw, "gateway")["users"] = cfg.Gateway.Users
				return nil
			}))
			// F2 uses the existing save fixture, not F1's production reload.
			// Its injection hook must refresh the process-edge environment from
			// the real encrypted store, just as its documented contract allows.
			wireRolesReload(t, cfg, api, func() {
				saved, err := api.credStore.Get("TAVILY_API_KEY")
				require.NoError(t, err, "save-fixture reload must read the real saved credential")
				t.Setenv("TAVILY_API_KEY", saved)
			}, nil)

			before := api.searchChecks.generation("tavily")
			saveSF2GapKey(t, api, mux, &user, sf2GapPriorKey)
			afterSave := api.searchChecks.generation("tavily")
			require.Greater(t, afterSave, before, "a completed real save must advance the generation before check admission")
			require.Greater(t, afterSave, uint64(0), "F2 must not silently exercise another generation-zero fixture")
			finish := startSF2GapHeldCheck(t, mux, edge, sf2GapPriorKey)

			if replace {
				saveSF2GapKey(t, api, mux, &user, sf2GapReplacementKey)
				t.Log("real replacement save completed while the nonzero-generation provider response was held")
			}
			response := finish()
			if replace {
				requireSF2GapError(t, response, http.StatusConflict, sf2GapConflictMessage)
			} else {
				requireSearchCheckResult(t, response, "tavily", "success")
			}
		})
	}
}

func TestSearchConnectionCheck_CredentialReadFailuresReturnRecoveryAndSafeDiagnostic(t *testing.T) {
	// F3 explicitly adopts logIntegrationChangeFailure's fixed category labels,
	// not its free-form underlying error text. Both failures use the real store.
	for _, tc := range []struct {
		name, cause string
		locked      bool
	}{
		{"locked_store", "credential_store_locked", true},
		{"authentication_failed", "credential_authentication_failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, cfg, mux := newSearchCheckMux(t)
			t.Cleanup(api.credStore.Close)
			var calls atomic.Int32
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, err := io.WriteString(w, `{"results":[]}`)
				assert.NoError(t, err)
			}))
			t.Cleanup(edge.Close)
			wireSearchCheckEdge(t, api, cfg, "tavily", edge.URL)
			key, err := api.credStore.Get("TAVILY_API_KEY")
			require.NoError(t, err, "readable saved-key positive control")
			if key != searchSettingsSecret {
				t.Fatal("positive control: saved key differs from the fixture (values redacted)")
			}
			api.credStore.Close()
			wantErr := credentials.ErrStoreLocked
			if !tc.locked {
				// A different fixed 32-byte AES test key makes the existing real
				// ciphertext fail authentication; no error-returning stub is used.
				require.NoError(t, api.credStore.UnlockWithKey(bytes.Repeat([]byte{0x5a}, 32)))
				wantErr = credentials.ErrWrongKey
			}
			_, readErr := api.credStore.Get("TAVILY_API_KEY")
			require.ErrorIs(t, readErr, wantErr, "fixture must reach the requested non-missing credential failure")

			capture := &searchDiagnosticLog{}
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(previous) })
			slog.Error("sf2-gap-log-positive-control")
			require.Contains(t, capture.String(), `"msg":"sf2-gap-log-positive-control"`, "instrument must capture ERROR-level slog output")
			capture.mu.Lock()
			capture.buf.Reset() // The calibration record cannot satisfy F3.
			capture.mu.Unlock()

			response := postSearchCheck(t.Context(), mux, "tavily", `{}`, diagnosticBearer)
			requireSF2GapError(t, response, http.StatusServiceUnavailable, sf2GapRecoveryMessage)
			assert.Equal(t, int32(0), calls.Load(), "a failed saved-key read must not contact the provider")
			captured := capture.String()
			requireSF2GapDiagnostic(t, captured, tc.cause)
			for _, forbidden := range []string{searchSettingsSecret, diagnosticBearer, "TAVILY_API_KEY", api.credStore.Path(), readErr.Error()} {
				if strings.Contains(captured, forbidden) || strings.Contains(response.Body.String(), forbidden) {
					t.Error("credential-read response or diagnostic leaked a key, credential name, path, or free-form error (value redacted)")
				}
			}
		})
	}
}

// The service manager is real, with no external channels enabled. It lets F1
// exercise all of handleConfigReload, including successful restart and cleanup.
func prepareSF2GapRealReload(t *testing.T, api *restAPI, cfg *config.Config) (*services, *bus.MessageBus) {
	t.Helper()
	require.NoError(t, api.safeUpdateConfigJSON(func(raw map[string]any) error {
		defaults := ensureMap(raw, "agents", "defaults")
		defaults["home"] = cfg.Agents.Defaults.Home
		defaults["default_model"] = config.DefaultModel{}      // Production limited-mode startup; no paid model call.
		ensureMap(raw, "gateway")["users"] = cfg.Gateway.Users // Preserve the authenticated control across reload.
		return nil
	}))
	msgBus := bus.NewMessageBus()
	bundle := credentials.SecretBundle{"TAVILY_API_KEY": searchSettingsSecret}
	manager, err := channels.NewManager(api.agentLoop.GetConfig(), bundle, msgBus, nil)
	require.NoError(t, err)
	running := &services{
		homePath: api.homePath, restAPIRef: api, credStore: api.credStore,
		ChannelManager: manager, bundle: bundle,
	}
	running.loadConfigForSwap = newReloadConfigLoader(api.configPath(), api.homePath, api.credStore, running)
	// Five seconds is the existing gateway fixture's shutdown backstop, not an
	// elapsed-time assertion. Stop joins the actual restarted services.
	t.Cleanup(func() { stopAndCleanupServices(running, 5*time.Second, false) })
	return running, msgBus
}

func saveSF2GapKey(t *testing.T, api *restAPI, mux http.Handler, user *config.UserConfig, key string) {
	t.Helper()
	body, err := json.Marshal(gen.IntegrationProviderUpdateRequest{Kind: "search", ApiKey: &key})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/integrations/providers/tavily", bytes.NewReader(body)).WithContext(t.Context())
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+diagnosticBearer)
	r.Header.Set("X-Reauth-Token", reauthFor(t, api, user))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, r)
	require.Equal(t, http.StatusOK, response.Code, "real authenticated key save must complete before proceeding")
	saved, err := api.credStore.Get("TAVILY_API_KEY")
	require.NoError(t, err)
	if saved != key {
		t.Fatal("real save did not persist the exact replacement fixture key (values redacted)")
	}
	if api.agentLoop.GetConfig().Tools.Web.Tavily.APIKey() != key {
		t.Fatal("real completed save did not load the exact replacement fixture key (values redacted)")
	}
}

type sf2GapHeldProvider struct {
	server      *httptest.Server
	started     chan []byte
	release     chan struct{}
	releaseOnce sync.Once
	calls       atomic.Int32
}

func newSF2GapHeldProvider(t *testing.T) *sf2GapHeldProvider {
	t.Helper()
	edge := &sf2GapHeldProvider{started: make(chan []byte, 1), release: make(chan struct{})}
	edge.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		edge.calls.Add(1)
		data, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err, "real provider request body must be consumed") {
			return
		}
		select {
		case edge.started <- data:
		case <-r.Context().Done():
			return
		}
		select {
		case <-edge.release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Decision #1105: a valid empty provider result is success.
		_, err = io.WriteString(w, `{"results":[]}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(edge.server.Close)
	t.Cleanup(func() { edge.releaseOnce.Do(func() { close(edge.release) }) })
	return edge
}

func startSF2GapHeldCheck(t *testing.T, mux http.Handler, edge *sf2GapHeldProvider, key string) func() *httptest.ResponseRecorder {
	t.Helper()
	// The design's 15-second total budget is a harness backstop. Channels, not
	// elapsed time, prove admission -> mutation/reload -> provider completion.
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	finished := make(chan *httptest.ResponseRecorder, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		finished <- postSearchCheck(ctx, mux, "tavily", `{}`, diagnosticBearer)
	}()
	t.Cleanup(func() {
		edge.releaseOnce.Do(func() { close(edge.release) })
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("check goroutine did not finish after release and cancellation")
		}
	})
	var data []byte
	select {
	case data = <-edge.started:
	case early := <-finished:
		t.Fatalf("expected admission at the real provider, but check returned HTTP %d before the held call", early.Code)
	case <-ctx.Done():
		t.Fatalf("held check never reached the real provider: %v", ctx.Err())
	}
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	if payload["api_key"] != key {
		t.Fatal("held real request did not use the exact pre-change fixture key (values redacted)")
	}
	require.Equal(t, "Omnipus", payload["query"], "Decision #1105's fixed non-personal query")
	require.Equal(t, int32(1), edge.calls.Load(), "admission must send one addressed request")
	return func() *httptest.ResponseRecorder {
		t.Helper()
		select {
		case early := <-finished:
			t.Fatalf("check returned HTTP %d before the provider response was released", early.Code)
		default:
		}
		edge.releaseOnce.Do(func() { close(edge.release) })
		select {
		case response := <-finished:
			require.Equal(t, int32(1), edge.calls.Load(), "no retry, fallback or key rotation after the held response")
			return response
		case <-ctx.Done():
			t.Fatalf("released provider response did not complete the check: %v", ctx.Err())
			return nil
		}
	}
}

func requireSF2GapError(t *testing.T, response *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	require.Equal(t, status, response.Code, "expected the specified gateway error, not a completed provider outcome")
	var failure gen.ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
	require.Equal(t, gen.ErrorResponse{Error: message}, failure, "fixed non-secret recovery message")
	var fields map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
	require.Equal(t, map[string]any{"error": message}, fields, "error-only shape: no provider outcome, key identity or extra data")
	for _, secret := range []string{searchSettingsSecret, sf2GapPriorKey, sf2GapReplacementKey, discardedSearchContent} {
		if strings.Contains(response.Body.String(), secret) {
			t.Error("gateway error leaked a key or provider payload (value redacted)")
		}
	}
}

func requireSF2GapDiagnostic(t *testing.T, captured, cause string) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(captured))
	var diagnostics []map[string]any
	for {
		var entry map[string]any
		err := decoder.Decode(&entry)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err, "captured diagnostic output must be valid JSON")
		if entry["msg"] == "integration change failed" {
			delete(entry, "time") // Timestamp is nondeterministic, not diagnostic content.
			diagnostics = append(diagnostics, entry)
		}
	}
	require.Len(t, diagnostics, 1, "expected one safe check_credential_read diagnostic, even though the HTTP 503 is unchanged")
	require.Equal(t, map[string]any{
		"level": "ERROR", "msg": "integration change failed", "service": "tavily",
		"stage": "check_credential_read", "cause": cause,
	}, diagnostics[0], "complete safe diagnostic: fixed labels only, never raw errors, paths or secret fields")
}
