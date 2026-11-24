package gateway

// G2-B1, Settings #1519. Oracles: spec C-2/C-6, FR-020/062 and
// docs/settings.md (saved but not in use). Registered handlers, durable JSON,
// live config, existing agent registry and pending GET are real. Only the
// registry-rebuild completion seam is injected; it never publishes a rebuild.
// Existing G1 B5 (newer successful SAME-key apply clears pending) is unchanged.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// The model is configured AND selected by the registered agent. Its initial
// override (32768) and submitted override (65536) are distinct valid windows,
// not an unconfigured row that pruneDeadOverrides can silently discard.
const gateG2OverrideBody = `{"model_overrides":[{"provider":"ollama","model":"llama3.1:8b","context_window":65536}]}`

func newGateG2ContextAPI(t *testing.T) *retentionSectionAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", retentionTestToken)
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	doc := `{"version":1,"agents":{"defaults":{"default_model":{"provider":"ollama","model":"llama3.1:8b"},"max_tokens":4096,"max_tool_iterations":40}},"providers":[{"provider":"ollama","model":"llama3.1:8b","api_base":"http://127.0.0.1:11434"}],"context":{"model_overrides":[{"provider":"ollama","model":"llama3.1:8b","context_window":32768}]},"storage":{"retention":{"session_days":30,"memory_retros_days":180,"memory_retros_forever":false}}}`
	path := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	cfg, err := config.LoadConfig(path)
	require.NoError(t, err)
	cfg.Agents.Defaults.Home = home
	cfg.Agents.List = []config.AgentConfig{{ID: sweepTestAgentID, Name: "G2 Context Agent", Type: config.AgentTypeCustom}}
	seedAgentEntities(t, home, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	inst, ok := al.GetRegistry().GetAgent(sweepTestAgentID)
	require.True(t, ok, "instrument: an actual configured agent must be registered")
	require.Equal(t, "llama3.1:8b", inst.Model, "instrument: the changed override must target the existing agent's model")
	require.Equal(t, 32768, inst.ContextWindow, "instrument: the initial override must really govern the registered agent")
	a := &restAPI{agentLoop: al, homePath: home, allowedOrigin: "http://localhost:3000"}
	mux := http.NewServeMux()
	a.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	// /config is registered by gateway_boot.go, not the additional-endpoints
	// catalog. Keep its real handler and auth wrapper in this bounded mux.
	mux.HandleFunc("/api/v1/config", a.withAuth(a.HandleConfig))
	return &retentionSectionAPI{api: a, mux: mux, cfg: cfg}
}

type gateG2Put struct {
	done     chan struct{}
	response *httptest.ResponseRecorder
}

type gateG2Reloads struct {
	h       *retentionSectionAPI
	entered [2]chan struct{}
	release [2]func()
	puts    []*gateG2Put
}

func newGateG2Reloads(t *testing.T, h *retentionSectionAPI) *gateG2Reloads {
	t.Helper()
	b := &gateG2Reloads{h: h}
	var blocked [2]chan struct{}
	for i := range blocked {
		b.entered[i] = make(chan struct{})
		blocked[i] = make(chan struct{})
		var once sync.Once
		b.release[i] = func() { once.Do(func() { close(blocked[i]) }) }
	}
	var calls atomic.Int32
	h.api.agentLoop.SetReloadFunc(func() error {
		i := int(calls.Add(1)) - 1
		if i < len(blocked) {
			close(b.entered[i])
			<-blocked[i]
		}
		return errors.New("G2 injected registry rebuild failure")
	})
	// Release AND join before the fixture closes the loop or removes its files.
	t.Cleanup(func() {
		for _, release := range b.release {
			release()
		}
		for _, put := range b.puts {
			select {
			case <-put.done:
			case <-time.After(5 * time.Second):
				t.Error("G2 fixture: handler did not finish after releasing its rebuild")
			}
		}
	})
	return b
}

func (b *gateG2Reloads) startPut(path, body string) *gateG2Put {
	put := &gateG2Put{done: make(chan struct{})}
	b.puts = append(b.puts, put)
	go func() {
		defer close(put.done)
		put.response = b.h.do(http.MethodPut, path, body)
	}()
	return put
}

func gateG2Await(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("G2 fixture: did not reach %s", what)
	}
}

func gateG2AssertSavedFailure(t *testing.T, put *gateG2Put) {
	t.Helper()
	gateG2Await(t, put.done, "Context handler completion")
	require.Equal(t, http.StatusInternalServerError, put.response.Code, "instrument: persisted Context write must report the injected failed rebuild: %s", put.response.Body.String())
	var body gen.ErrorResponse
	require.NoError(t, json.Unmarshal(put.response.Body.Bytes(), &body))
	require.NotNil(t, body.Code)
	require.Equal(t, "saved_not_refreshed", *body.Code, "instrument: this must be a post-persistence failure, not validation/auth")
}

func gateG2AssertContextPending(t *testing.T, h *retentionSectionAPI, withCap bool) {
	t.Helper()
	raw, err := os.ReadFile(h.api.configPath())
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	var submitted map[string]any
	require.NoError(t, json.Unmarshal([]byte(gateG2OverrideBody), &submitted))
	wantOverride := submitted["model_overrides"]
	require.Equal(t, wantOverride, getAtSegments(disk, canonicalConfigKeys("context.model_overrides")[0]), "instrument: the meaningful configured-model override must really persist")
	want := map[string]any{"context.model_overrides": wantOverride}
	if withCap {
		require.Equal(t, float64(1234), getAtSegments(disk, canonicalConfigKeys("context.builtin_success_cap")[0]), "instrument: different-field sparse write must persist as well")
		want["context.builtin_success_cap"] = float64(1234)
	}
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent(sweepTestAgentID)
	require.True(t, ok)
	require.Equal(t, 32768, inst.ContextWindow, "instrument: refresh-only saves did not rebuild the existing registry agent")
	got := gateG1FailedEntries(t, h)
	values := make(map[string]any, len(got))
	for key, entry := range got {
		values[key] = entry.PersistedValue
	}
	assert.Equal(t, want, values, "G2-B1/C-6/FR-062: every saved but unapplied Context key must remain pending; newer failed writes or unrelated successful refreshes do not apply it")
}

func TestGateG2_B1_DifferentContextFailuresKeepBothPendingInEitherCompletionOrder(t *testing.T) {
	for _, order := range []string{"older-first", "newer-first"} {
		t.Run(order, func(t *testing.T) {
			h := newGateG2ContextAPI(t)
			b := newGateG2Reloads(t, h)
			first := b.startPut("/api/v1/settings/context", gateG2OverrideBody)
			gateG2Await(t, b.entered[0], "first registry rebuild")
			second := b.startPut("/api/v1/settings/context", `{"builtin_success_cap":1234}`)
			select {
			case <-b.entered[1]:
			case <-time.After(5 * time.Second):
				// Serializing the apply path is a valid repair; it must still
				// satisfy the identical pending-union assertion, never skip it.
				b.release[0]()
				gateG2Await(t, b.entered[1], "serialized second registry rebuild")
			}
			if order == "older-first" {
				b.release[0]()
				gateG2AssertSavedFailure(t, first)
				b.release[1]()
			} else {
				b.release[1]()
				gateG2AssertSavedFailure(t, second)
				b.release[0]()
			}
			gateG2AssertSavedFailure(t, first)
			gateG2AssertSavedFailure(t, second)
			gateG2AssertContextPending(t, h, true)
		})
	}
}

func TestGateG2_B1_UnrelatedSuccessfulRefreshCannotHideFailedContextApply(t *testing.T) {
	for _, tc := range []struct{ name, path, body string }{
		{"config", "/api/v1/config", `{"gateway":{"log_level":"debug"}}`},
		{"retention", "/api/v1/settings/retention", `{"session_days":180}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newGateG2ContextAPI(t)
			b := newGateG2Reloads(t, h)
			context := b.startPut("/api/v1/settings/context", gateG2OverrideBody)
			gateG2Await(t, b.entered[0], "Context registry rebuild")
			unrelated := b.startPut(tc.path, tc.body)
			select {
			case <-unrelated.done:
			case <-time.After(5 * time.Second):
				// A serialized repair may wait for Context; the final pending
				// requirement is the same after both completions.
				b.release[0]()
				gateG2Await(t, unrelated.done, "serialized refresh-only save")
			}
			require.Equal(t, http.StatusOK, unrelated.response.Code, "instrument: unrelated write must actually succeed: %s", unrelated.response.Body.String())
			b.release[0]()
			gateG2AssertSavedFailure(t, context)
			gateG2AssertContextPending(t, h, false)
		})
	}
}
