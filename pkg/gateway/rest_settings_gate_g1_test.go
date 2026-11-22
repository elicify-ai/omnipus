package gateway

// Gate G1, Settings #1519: B1/B2/B3/B5. Oracles: settings-redesign-spec
// C-6, FR-020/050/062/102 and gate/fix-round-g1.md. The real registered
// handlers, config file, agent and pending reader run. Only the credential
// failure point and reload completion ordering are injected.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func newGateG1API(t *testing.T) *retentionSectionAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", retentionTestToken)
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	doc := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"storage":{"retention":{"session_days":30,"memory_retros_days":180,"memory_retros_forever":false}},"sandbox":{"audit_log":true,"prompt_injection_level":"medium","rate_limits":{"max_agent_llm_calls_per_hour":7,"max_agent_tool_calls_per_minute":9}}}`
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	cfg, err := config.LoadConfig(path)
	require.NoError(t, err)
	cfg.Agents.Defaults.Home = home
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	a := &restAPI{agentLoop: al, homePath: home, allowedOrigin: "http://localhost:3000"}
	mux := http.NewServeMux()
	a.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return &retentionSectionAPI{api: a, mux: mux, cfg: cfg}
}

func gateG1FailedEntries(t *testing.T, h *retentionSectionAPI) map[string]pendingRestartEntry {
	t.Helper()
	w := h.do(http.MethodGet, "/api/v1/config/pending-restart", "")
	require.Equal(t, http.StatusOK, w.Code, "plain pending GET must still work after a credential refresh rejection: %s", w.Body.String())
	entries := decodeDiffs(t, w.Body.Bytes())
	out := map[string]pendingRestartEntry{}
	for _, e := range entries {
		if e.Reason == gen.PendingRestartEntryReasonApplyFailed {
			out[e.Key] = e
		}
	}
	return out
}

func gateG1AssertPending(t *testing.T, h *retentionSectionAPI, want map[string]any) {
	t.Helper()
	got := gateG1FailedEntries(t, h)
	keys := make([]string, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	wantKeys := make([]string, 0, len(want))
	for key := range want {
		wantKeys = append(wantKeys, key)
	}
	sort.Strings(keys)
	sort.Strings(wantKeys)
	assert.Equal(t, wantKeys, keys, "B1/B3 C-6: every persisted but unapplied field must already be listed, with no untouched siblings")
	for key, value := range want {
		assert.Equal(t, value, got[key].PersistedValue, "B1/B3: pending %s must read its value from disk", key)
	}
}

func TestGateG1_B1_RetentionRefreshFailureIsSavedNotInUse(t *testing.T) {
	h := newGateG1API(t)
	logger := h.api.agentLoop.AuditLogger()
	require.NotNil(t, logger, "fixture must use the real enabled audit logger")
	oldLive := h.api.agentLoop.GetConfig().Storage.Retention
	h.api.credStore = credentials.NewStore(filepath.Join(t.TempDir(), "locked.json"))
	w := h.do(http.MethodPut, "/api/v1/settings/retention", `{"memory_retros_days":730,"memory_retros_forever":true}`)

	stored, err := h.api.storedRetention()
	require.NoError(t, err)
	require.Equal(t, 730, stored.MemoryRetrosDays, "fixture: the failed refresh must be after the durable write")
	require.True(t, stored.MemoryRetrosForever, "fixture: Keep forever is on disk")
	require.Equal(t, oldLive, h.api.agentLoop.GetConfig().Storage.Retention, "fixture: the locked credential store must prevent live publication")
	assert.Equal(t, http.StatusInternalServerError, w.Code, "B1/C-6: post-write refresh failure is a 500 saved_not_refreshed, not success")
	var er gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &er))
	assert.Equal(t, "saved_not_refreshed", pointerValueG1(er.Code), "B1/C-6: a generic 500 hides a saved but unapplied retention change")
	var keys any
	if er.Details != nil {
		keys = (*er.Details)["keys"]
	}
	assert.Equal(t, []any{"storage.retention.memory_retros_days", "storage.retention.memory_retros_forever"}, keys,
		"B1/FR-102: details.keys must identify exactly the two persisted retention changes")
	gateG1AssertPending(t, h, map[string]any{
		"storage.retention.memory_retros_days":    float64(730),
		"storage.retention.memory_retros_forever": true,
	})
	log, err := os.ReadFile(logger.Path())
	require.NoError(t, err)
	assert.Contains(t, string(log), `"resource":"storage.retention"`, "B1: the durable change must be audited even when live refresh fails")
	assert.Contains(t, string(log), `"memory_retros_forever":true`, "B1: the audit must describe the persisted Keep forever value")
}

func pointerValueG1(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func TestGateG1_B3_FirstRefreshFailureRecordsPromptAndRateWrites(t *testing.T) {
	cases := []struct {
		name, path, body string
		want             map[string]any
	}{
		{"prompt_guard", "/api/v1/security/prompt-guard", `{"level":"high"}`, map[string]any{"sandbox.prompt_injection_level": "high"}},
		{"rate_limits", "/api/v1/security/rate-limits", `{"max_agent_llm_calls_per_hour":42}`, map[string]any{"sandbox.rate_limits.max_agent_llm_calls_per_hour": float64(42)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newGateG1API(t)
			before := h.api.agentLoop.GetConfig()
			h.api.credStore = credentials.NewStore(filepath.Join(t.TempDir(), "locked.json"))
			w := h.do(http.MethodPut, tc.path, tc.body)
			require.Same(t, before, h.api.agentLoop.GetConfig(), "fixture: first-stage refresh must reject the candidate")
			raw, err := os.ReadFile(h.api.configPath())
			require.NoError(t, err)
			var disk map[string]any
			require.NoError(t, json.Unmarshal(raw, &disk))
			for key, value := range tc.want {
				assert.Equal(t, value, getAtSegments(disk, canonicalConfigKeys(key)[0]), "fixture: B3 write must have reached disk")
			}
			// C-6 permits the endpoint's existing saved/requires_restart body;
			// SFH-03 also explicitly permits the recognized 500 C-6 signal.
			var body struct {
				gen.ErrorResponse
				Saved           bool `json:"saved"`
				RequiresRestart bool `json:"requires_restart"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			savedSignal := w.Code == http.StatusOK && body.Saved && body.RequiresRestart ||
				w.Code == http.StatusInternalServerError && pointerValueG1(body.Code) == "saved_not_refreshed"
			assert.True(t, savedSignal, "B3/C-6: persisted %s must say saved, not in use, even when the FIRST refresh fails; status=%d body=%s", tc.name, w.Code, w.Body.String())
			gateG1AssertPending(t, h, tc.want)
		})
	}
}

func TestGateG1_B2_RetentionPutUpdatesExistingAgentRecallWindow(t *testing.T) {
	for _, body := range []string{`{"memory_retros_days":730}`, `{"memory_retros_forever":true}`} {
		t.Run(body, func(t *testing.T) {
			f := newRetentionSweepFixture(t, retentionDocPlain)
			inst, ok := f.al.GetRegistry().GetAgent(sweepTestAgentID)
			require.True(t, ok)
			mem := inst.ContextBuilder.Memory()
			adapter := agent.NewMemoryStoreAdapter(mem)
			day := fixedSweepNow.AddDate(0, 0, -400) // FR-050's kept 400-day reflection.
			dir := filepath.Join(inst.Home, ".omnipus", "retros", day.Format("2006-01-02"))
			require.NoError(t, os.MkdirAll(dir, 0o700))
			reflection := "<!-- ts=" + day.Format("2006-01-02T15:04:05.000Z") + " trigger=session_end -->\n## Session recap\nzeppelin retention observation\n<!-- next -->\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "zeppelin_retro.md"), []byte(reflection), 0o600))
			initial, err := adapter.SearchRetros("zeppelin", 20)
			require.NoError(t, err)
			require.Empty(t, initial, "instrument: the initial 180-day window must exclude this 400-day reflection")
			t.Setenv("OMNIPUS_BEARER_TOKEN", retentionTestToken)
			a := &restAPI{agentLoop: f.al, homePath: filepath.Dir(f.configPath)}
			mux := http.NewServeMux()
			a.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
			h := &retentionSectionAPI{api: a, mux: mux, cfg: f.cfg}
			w := h.do(http.MethodPut, "/api/v1/settings/retention", body)
			require.Equal(t, http.StatusOK, w.Code, "real retention save: %s", w.Body.String())
			still, ok := f.al.GetRegistry().GetAgent(sweepTestAgentID)
			require.True(t, ok)
			require.Same(t, inst, still, "B2 must update the ALREADY registered agent, not reconstruct it")
			require.Same(t, mem, still.ContextBuilder.Memory())
			hits, err := adapter.SearchRetros("zeppelin", 20)
			require.NoError(t, err)
			require.Len(t, hits, 1, "B2/FR-050: the same existing adapter must recall the kept 400-day zeppelin reflection after PUT, without restart")
			assert.Contains(t, hits[0].Content, "zeppelin retention observation")
		})
	}
}

func TestGateG1_B5_LateFailedReloadCannotResurrectNewerSuccessfulApply(t *testing.T) {
	h := newGateG1API(t)
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)
	var calls atomic.Int32
	h.api.agentLoop.SetReloadFunc(func() error {
		if calls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
			return errors.New("B5 injected older reload failure")
		}
		return nil
	})
	firstDone := make(chan int, 1)
	go func() {
		firstDone <- h.do(http.MethodPut, "/api/v1/security/prompt-guard", `{"level":"high"}`).Code
	}()
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("B5 fixture: first real handler never reached its reload completion seam")
	}
	secondDone := make(chan int, 1)
	go func() {
		secondDone <- h.do(http.MethodPut, "/api/v1/security/prompt-guard", `{"level":"low"}`).Code
	}()
	select {
	case code := <-secondDone:
		require.Equal(t, http.StatusOK, code, "B5 fixture: newer save must successfully apply before the older completion is released")
		release()
		require.Equal(t, http.StatusOK, <-firstDone, "older save retains its existing saved-not-applied response")
	case <-time.After(5 * time.Second):
		// Serialization is also a valid repair. Release A, then check B's
		// completion and the SAME final-state assertions, never a silent pass.
		release()
		require.Equal(t, http.StatusOK, <-firstDone)
		require.Equal(t, http.StatusOK, <-secondDone)
	}
	require.Equal(t, "low", string(h.api.agentLoop.GetConfig().Sandbox.PromptInjectionLevel))
	stored, err := os.ReadFile(h.api.configPath())
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(stored, &disk))
	assert.Equal(t, "low", getAtSegments(disk, canonicalConfigKeys("sandbox.prompt_injection_level")[0]))
	assert.Empty(t, gateG1FailedEntries(t, h), "B5/M-8: an older failed completion must not resurrect apply_failed after a newer successful apply of the same key")
}
