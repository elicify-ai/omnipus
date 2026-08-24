// UAT-fix regression tests (operator decisions):
//   - Task 3: workspace/agent create+update reject whitespace-only names (400).
//   - Task 4: provider model catalog — has_models_endpoint signal, user-supplied
//     model slugs round-trip on PUT, and the /refresh-models endpoint.
//   - Task 2: the structured-delegation-failure WS forwarder helper.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
)

// TestFetchUpstreamModels covers the OpenAI-compatible /models fetcher against a
// stub server: success (sorted IDs), non-200, wrong content-type, and bad JSON.
// Re-pointed to providers_pkg.FetchModels after the gateway copy was deleted (FR-002 / SC-002).
func TestFetchUpstreamModels(t *testing.T) {
	t.Run("success returns sorted ids", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/models", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"z-model"},{"id":"a-model"},{"id":""}]}`))
		}))
		defer srv.Close()

		models, err := providers_pkg.FetchModels(context.Background(), srv.URL, "key", nil)
		require.NoError(t, err)
		// Sorted, empty id dropped.
		assert.Equal(t, []string{"a-model", "z-model"}, models)
	})

	t.Run("non-200 returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		_, err := providers_pkg.FetchModels(context.Background(), srv.URL, "key", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "401")
	})

	t.Run("wrong content-type returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>not json</html>"))
		}))
		defer srv.Close()

		_, err := providers_pkg.FetchModels(context.Background(), srv.URL, "key", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Content-Type")
	})

	t.Run("bad JSON returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":`)) // truncated
		}))
		defer srv.Close()

		_, err := providers_pkg.FetchModels(context.Background(), srv.URL, "key", nil)
		require.Error(t, err)
	})
}

// --- Task 3: whitespace-only name validation ---

// TestWorkspaceCreate_WhitespaceName_Rejected proves a whitespace-only workspace
// name is rejected with 400 (previously accepted as 201).
func TestWorkspaceCreate_WhitespaceName_Rejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"   \t  "}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"whitespace-only workspace name must be 400; body=%s", w.Body.String())
}

// TestWorkspaceCreate_NameTrimmed proves a name with surrounding whitespace is
// trimmed before persistence.
func TestWorkspaceCreate_NameTrimmed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"  Padded Name  "}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ws))
	assert.Equal(t, "Padded Name", ws.Name, "leading/trailing whitespace must be trimmed")
}

// TestWorkspaceUpdate_WhitespaceName_Rejected proves a whitespace-only name on
// update is rejected with 400.
func TestWorkspaceUpdate_WhitespaceName_Rejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "Original", "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id,
		strings.NewReader(`{"name":"   "}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"whitespace-only workspace name on update must be 400; body=%s", w.Body.String())
}

// TestAgentCreate_WhitespaceName_Rejected proves a whitespace-only agent name is
// rejected (422, the agent handler's empty-name status).
func TestAgentCreate_WhitespaceName_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"   ","type":"Main","soul":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"whitespace-only agent name must be rejected; body=%s", w.Body.String())
}

// TestAgentUpdate_WhitespaceName_Rejected proves a whitespace-only agent name on
// update is rejected.
func TestAgentUpdate_WhitespaceName_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Create a valid agent first.
	cw := httptest.NewRecorder()
	cr := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Valid Agent","type":"Main","soul":"x"}`))
	cr.Header.Set("Content-Type", "application/json")
	api.HandleAgents(cw, cr)
	require.Equal(t, http.StatusCreated, cw.Code, "create body=%s", cw.Body.String())
	created := decodeAgentResp(t, cw.Body.Bytes())

	uw := httptest.NewRecorder()
	ur := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
		strings.NewReader(`{"name":"   "}`))
	ur.Header.Set("Content-Type", "application/json")
	api.HandleAgents(uw, ur)

	require.Equal(t, http.StatusUnprocessableEntity, uw.Code,
		"whitespace-only agent name on update must be rejected; body=%s", uw.Body.String())
}

// --- Task 4: provider model catalog ---

// TestProviders_HasModelsEndpoint_Signal proves the GET response exposes
// has_models_endpoint=true for a provider with a known upstream base (openrouter)
// and false for a custom provider with no known base URL. Providers are seeded
// WITHOUT an API key so the GET does not make a live upstream fetch (hermetic).
func TestProviders_HasModelsEndpoint_Signal(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	seedProviderConfig(
		t, api,
		map[string]any{
			// updated_at marks this row as operator-configured rather than a
			// fresh-install template (isSeedTemplateRow, ADR-067 FR-029) — a
			// bare Provider+Model pair with no credential, endpoint, models
			// list or PUT stamp is indistinguishable from a template, and a
			// real PUT always stamps updated_at (ADR-068 MAJ-015).
			"model_name": "openrouter", "provider": "openrouter", "model": "~anthropic/claude-sonnet-latest",
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
		map[string]any{
			"model_name": "mygw", "provider": "mygw", "model": "mygw/llama",
			"models": []any{"mygw/llama", "mygw/mixtral"},
		},
	)

	provs := getProviders(t, api)
	byID := map[string]gen.Provider{}
	for _, p := range provs {
		byID[p.Id] = p
	}

	or, ok := byID["openrouter"]
	require.True(t, ok, "openrouter must be present; got %+v", provs)
	require.NotNil(t, or.HasModelsEndpoint)
	assert.True(t, *or.HasModelsEndpoint, "openrouter has a live /models endpoint")

	gw, ok := byID["mygw"]
	require.True(t, ok, "custom provider must be present; got %+v", provs)
	require.NotNil(t, gw.HasModelsEndpoint)
	assert.False(t, *gw.HasModelsEndpoint, "custom provider has no live /models endpoint")
	// The custom provider's catalog is the user-supplied slug list.
	assert.ElementsMatch(t, []string{"mygw/llama", "mygw/mixtral"}, gw.Models,
		"endpoint-less provider catalog must be the user-supplied slugs")
}

// TestProviders_UserModels_RoundTrip proves a user-supplied models list set via
// PUT persists and is returned by GET for an endpoint-less provider. The provider
// is seeded first (no api_key) so the PUT only updates the models slug list and
// never touches the credential store.
func TestProviders_UserModels_RoundTrip(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	seedProviderConfig(
		t, api,
		map[string]any{"model_name": "mygw", "provider": "mygw", "model": "mygw/a"},
	)

	putProvider(t, api, "mygw", `{"models":["mygw/a","mygw/b","mygw/a"]}`)

	provs := getProviders(t, api)
	var gw *gen.Provider
	for i := range provs {
		if provs[i].Id == "mygw" {
			gw = &provs[i]
		}
	}
	require.NotNil(t, gw, "mygw must be present; got %+v", provs)
	// Dedup: the duplicate "mygw/a" collapses to a single entry.
	assert.ElementsMatch(t, []string{"mygw/a", "mygw/b"}, gw.Models)

	// Clearing the list (empty array) removes the user catalog; the provider
	// then reports an empty models list (ADR-068 T068-04 removed the
	// model_name-alias fallback fill).
	putProvider(t, api, "mygw", `{"models":[]}`)
	provs = getProviders(t, api)
	// Reset gw so a "mygw" that no longer appears in the list fails the
	// NotNil check below loudly, instead of silently re-asserting on the
	// stale pointer from the block above.
	gw = nil
	for i := range provs {
		if provs[i].Id == "mygw" {
			gw = &provs[i]
		}
	}
	require.NotNil(t, gw, "mygw must still be present after clearing its models; got %+v", provs)
	assert.NotContains(t, gw.Models, "mygw/b", "cleared user models must not be returned")
}

// TestProviders_ModelBounds_Rejected proves the inline bounds caps (M-slug):
// a models list with >500 entries OR any slug >256 chars is rejected with 400,
// independent of gateway.validate_inbound (which defaults false in tests). The
// rejection short-circuits before any config write or reload.
func TestProviders_ModelBounds_Rejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	seedProviderConfig(
		t, api,
		map[string]any{"model_name": "mygw", "provider": "mygw", "model": "mygw/a"},
	)

	t.Run("too many entries", func(t *testing.T) {
		slugs := make([]string, 0, 501)
		for i := 0; i < 501; i++ {
			slugs = append(slugs, fmt.Sprintf("mygw/m%d", i))
		}
		payload, err := json.Marshal(map[string]any{"models": slugs})
		require.NoError(t, err)
		w := putProviderRaw(t, api, "mygw", string(payload))
		require.Equal(t, http.StatusBadRequest, w.Code,
			">500 models must be rejected with 400; body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "500")
	})

	t.Run("slug too long", func(t *testing.T) {
		longSlug := "mygw/" + strings.Repeat("x", 260) // > 256 chars total
		payload, err := json.Marshal(map[string]any{"models": []string{"mygw/ok", longSlug}})
		require.NoError(t, err)
		w := putProviderRaw(t, api, "mygw", string(payload))
		require.Equal(t, http.StatusBadRequest, w.Code,
			"a slug >256 chars must be rejected with 400; body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "256")
	})

	t.Run("at the bound is accepted", func(t *testing.T) {
		// Exactly 500 entries, each within the length cap, is allowed.
		slugs := make([]string, 0, 500)
		for i := 0; i < 500; i++ {
			slugs = append(slugs, fmt.Sprintf("mygw/m%d", i))
		}
		payload, err := json.Marshal(map[string]any{"models": slugs})
		require.NoError(t, err)
		w := putProviderRaw(t, api, "mygw", string(payload))
		require.Equal(t, http.StatusOK, w.Code,
			"exactly 500 models must be accepted; body=%s", w.Body.String())
	})
}

// TestProviders_RefreshModels_EndpointlessReturnsUserCatalogue proves the
// /refresh-models endpoint returns the stored slugs for an endpoint-less provider.
func TestProviders_RefreshModels_EndpointlessReturnsUserCatalogue(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	seedProviderConfig(
		t, api,
		map[string]any{
			"model_name": "mygw", "provider": "mygw", "model": "mygw/a",
			"models": []any{"mygw/a", "mygw/b"},
		},
	)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/providers/mygw/refresh-models", nil)
	r.URL.Path = "/api/v1/providers/mygw/refresh-models"
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var p gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	require.NotNil(t, p.HasModelsEndpoint)
	assert.False(t, *p.HasModelsEndpoint)
	assert.ElementsMatch(t, []string{"mygw/a", "mygw/b"}, p.Models)
}

// TestProviders_RefreshModels_NotConfigured_404 proves refresh on an unknown
// provider returns 404.
func TestProviders_RefreshModels_NotConfigured_404(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/providers/ghost/refresh-models", nil)
	r.URL.Path = "/api/v1/providers/ghost/refresh-models"
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

// --- Task 1 (M4): workspace→turn binding ---

// TestHandleChatMessage_StampsWorkspaceOnSession proves that a chat frame
// carrying metadata.workspace_id binds the minted session to that workspace, so
// a task the agent creates during the turn resolves to the ACTIVE workspace
// (resolveWorkspaceID reads ToolWorkspaceID(ctx), which the loop seeds from the
// session's WorkspaceID).
func TestHandleChatMessage_StampsWorkspaceOnSession(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	const wantWS = "01JXWORKSPACE0000000000001"
	handler.handleChatMessage(
		context.Background(),
		"chat-m4-1", // chatID
		"",          // frameSessionID (empty → mint a new session)
		"do it",     // content
		"",          // agentID
		nil,         // mediaRefs
		"",          // modelName
		wantWS,      // workspaceID (active workspace)
		false,       // setupKickoff
		wc,
	)

	// Drain the inbound publish, then assert the minted session carries the
	// workspace binding on its meta.
	var sessionID string
	select {
	case msg := <-msgBus.InboundChan():
		sessionID = msg.SessionID
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
	require.NotEmpty(t, sessionID, "handleChatMessage must mint a session")

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store, "session store must resolve the minted session")
	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, wantWS, meta.WorkspaceID,
		"minted session must be bound to the active workspace (M4)")
}

// --- Task 2: structured delegation-failure forwarder helper ---

// TestParseDelegationFailure proves the WS forwarder helper recognizes a
// structured delegation_denied result and ignores everything else.
func TestParseDelegationFailure(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		obj, reason, ok := parseStructuredToolFailure(
			`{"error":"delegation_denied","reason":"target untrusted","policy":"trust_set","tool":"spawn"}`,
		)
		require.True(t, ok)
		assert.Equal(t, "target untrusted", reason)
		assert.Equal(t, "delegation_denied", obj["error"])
		assert.Equal(t, "trust_set", obj["policy"])
	})
	t.Run("ordinary error string is not a denial", func(t *testing.T) {
		_, _, ok := parseStructuredToolFailure("some plain tool error")
		assert.False(t, ok)
	})
	t.Run("unrelated json object is not a denial", func(t *testing.T) {
		_, _, ok := parseStructuredToolFailure(`{"error":"timeout"}`)
		assert.False(t, ok)
	})
	t.Run("empty string", func(t *testing.T) {
		_, _, ok := parseStructuredToolFailure("")
		assert.False(t, ok)
	})
}

// --- helpers ---

// seedProviderConfig writes the given provider entries into config.json (bypassing
// the credential store) AND into the in-memory config so the GET/PUT handlers
// observe them without depending on TriggerReload (which is not wired in the test
// agent loop). Used to set up endpoint-less providers without an API key.
func seedProviderConfig(t *testing.T, api *restAPI, entries ...map[string]any) {
	t.Helper()
	// Wire a reload func that re-reads the providers list from config.json into
	// the live config — the PUT provider handler calls triggerReloadAndWait,
	// which tolerates ErrReloadNotConfigured but still POLLS IsReloadPending()
	// (up to 5s) for a genuinely-wired reload func to finish. This func runs
	// synchronously (no goroutine, unlike production's async pipeline) so it
	// MUST call ClearReloadPending itself when done — production's
	// executeReload does this via defer after the real registry swap
	// completes, and TriggerReload sets reloadPending=true before invoking
	// this func in the first place. Without the explicit clear here,
	// triggerReloadAndWait would busy-poll for the full 5s timeout on every
	// PUT that reaches this point (it never fails, so IsReloadPending would
	// never observe a clear) — turning fast unit tests into multi-second ones.
	api.agentLoop.SetReloadFunc(func() error {
		defer api.agentLoop.ClearReloadPending()
		raw, rErr := os.ReadFile(api.configPath())
		if rErr != nil {
			return rErr
		}
		var m map[string]any
		if uErr := json.Unmarshal(raw, &m); uErr != nil {
			return uErr
		}
		list, _ := m["providers"].([]any)
		cfg := api.agentLoop.GetConfig()
		cfg.Providers = cfg.Providers[:0]
		for _, item := range list {
			e, ok := item.(map[string]any)
			if !ok {
				continue
			}
			// Round-trip through encoding/json rather than hand-picking
			// fields: a partial reconstruction (as this once was — Provider/
			// Model/Models only) silently drops UpdatedAt/APIKeyRef/APIBase/
			// AuthMethod, which makes isSeedTemplateRow misclassify a real,
			// previously-PUT row as a fresh-install template the moment its
			// Models list goes empty (its `updated_at` stamp never survived
			// this reload, even though the real PUT handler wrote it to
			// config.json) — exactly what a real config reload does NOT do,
			// since the production loader unmarshals every ModelConfig field.
			raw, mErr := json.Marshal(e)
			if mErr != nil {
				return mErr
			}
			mc := &config.ModelConfig{}
			if uErr := json.Unmarshal(raw, mc); uErr != nil {
				return uErr
			}
			cfg.Providers = append(cfg.Providers, mc)
		}
		return nil
	})

	err := api.safeUpdateConfigJSON(func(m map[string]any) error {
		list := make([]any, 0, len(entries))
		for _, e := range entries {
			list = append(list, e)
		}
		m["providers"] = list
		return nil
	})
	require.NoError(t, err, "seed provider config")

	// Mirror into the in-memory config (the GET handler reads cfg.Providers).
	// Same round-trip-through-JSON rationale as the reload closure above.
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = cfg.Providers[:0]
	for _, e := range entries {
		raw, mErr := json.Marshal(e)
		require.NoError(t, mErr, "seed provider config: marshal entry")
		mc := &config.ModelConfig{}
		require.NoError(t, json.Unmarshal(raw, mc), "seed provider config: unmarshal entry")
		cfg.Providers = append(cfg.Providers, mc)
	}
}

func putProvider(t *testing.T, api *restAPI, id, body string) {
	t.Helper()
	w := putProviderRaw(t, api, id, body)
	require.Equal(t, http.StatusOK, w.Code, "PUT provider %s must be 200; body=%s", id, w.Body.String())
}

// putProviderRaw PUTs a provider and returns the recorder without asserting the
// status — for tests that expect a non-200 (e.g. bounds rejection).
func putProviderRaw(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/providers/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/providers/" + id
	api.HandleProviders(w, r)
	return w
}

func getProviders(t *testing.T, api *restAPI) []gen.Provider {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET providers must be 200; body=%s", w.Body.String())
	var provs []gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &provs))
	return provs
}
