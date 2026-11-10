package agent

// effort_red_usage_test.go — RED tests for the reasoning-token usage pipeline
// (T-U1): provider usage JSON → the openai_compat parse → accountProviderCall
// → per-call TranscriptEntry (`reasoning_tokens`), and the failed-attempt
// usage path (StreamProviderError).
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-43, S-44, S-88, D7 rows 1-6/9, FR-024, FR-036, SC-011,
// SC-018, T-U1.
//
// RED reason: UsageInfo has no reasoning-token field today, so the provider
// parse, the per-call accounting entry and the error-path usage all carry no
// reasoning count. Assertions on those JSON names fail; the "absent stays
// absent" pins (S-44) pass at baseline by design and must keep passing at
// GREEN. The answering-entry stored snapshot (`turn_reasoning_tokens`) and
// the by-model cell sums are written by the production turn-completion and
// stats writers (SetEntryCallAccounting / addEntryTokens); their wire-level
// RED assertions live in the gateway transport tests (S-95/T-R2).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// usageEndpoint answers each request with the next canned body (the last one
// repeats). A body prefixed "sse:" is written as an SSE stream (used to
// trigger the StreamProviderError mid-stream error path with its usage).
type usageEndpoint struct {
	mu     sync.Mutex
	bodies []map[string]any
	script []string
	served int
}

func (e *usageEndpoint) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		e.mu.Lock()
		e.bodies = append(e.bodies, body)
		idx := e.served
		if idx >= len(e.script) {
			idx = len(e.script) - 1
		}
		e.served++
		resp := e.script[idx]
		e.mu.Unlock()
		if len(resp) > 4 && resp[:4] == "sse:" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(resp[4:]))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(resp))
	}
}

func newUsageEndpoint(t *testing.T, script []string) (*usageEndpoint, string) {
	t.Helper()
	e := &usageEndpoint{script: script}
	srv := httptest.NewServer(e.handler(t))
	t.Cleanup(srv.Close)
	return e, srv.URL
}

// newUsageFixture wires a primary provider pool exactly like the routing-1340
// rejection fixture, with canned usage responses.
func newUsageFixture(t *testing.T, primaryScript []string) (*usageEndpoint, *routing1340ReviewFixture) {
	t.Helper()
	primary, primaryURL := newUsageEndpoint(t, primaryScript)
	cfg := &config.Config{}
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: "primary-m", APIBase: primaryURL})
	candidates := []providers.FallbackCandidate{{Provider: "openrouter", Model: "primary-m"}}
	a := &AgentInstance{ID: "effort-usage-agent", Model: "primary-m", Candidates: candidates}
	a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	return primary, f
}

// usageJSON marshals a response's parsed usage into a raw map for JSON-name
// assertions (FR-024's reasoning_tokens member).
func usageJSON(t *testing.T, u *providers.UsageInfo) map[string]any {
	t.Helper()
	require.NotNil(t, u, "the provider's usage object was parsed")
	raw, err := json.Marshal(u)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// pendingCallEntries copies the call-accounting entries the production
// booking path (accountProviderCall) produced for this turn's provider calls.
func pendingCallEntries(t *testing.T, rt *agentLoopRunTurn) []map[string]any {
	t.Helper()
	rt.ts.mu.Lock()
	defer rt.ts.mu.Unlock()
	out := make([]map[string]any, 0, len(rt.ts.callAccounting.pending))
	for _, e := range rt.ts.callAccounting.pending {
		out = append(out, entryJSON(t, e))
	}
	return out
}

// TestT_U1_S43_CallEntryCarriesReasoningTokens pins D7 row 1 (FR-024): a
// provider response reporting 180 output / 120 reasoning tokens is parsed
// with its reasoning count intact, and the per-call accounting entry booked
// by the production writer stores reasoning_tokens 120 — never folded into
// the output or total counts.
// Traces to: S-43, D7 row 1, FR-024, SC-011, T-U1.
func TestT_U1_S43_CallEntryCarriesReasoningTokens(t *testing.T) {
	_, f := newUsageFixture(t, []string{
		`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190,"completion_tokens_details":{"reasoning_tokens":120}}}`,
	})

	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// The production parse keeps the count as its own member.
	u := usageJSON(t, resp.Usage)
	assert.Equal(t, float64(120), u["reasoning_tokens"],
		"the parsed usage carries reasoning_tokens 120 (S-43/FR-024) — RED: UsageInfo has no reasoning field")
	assert.Equal(t, float64(180), u["completion_tokens"], "output tokens unchanged (SC-011)")
	assert.Equal(t, float64(190), u["total_tokens"], "totals unchanged — never 300 (SC-011)")

	// The production booking (accountProviderCall) carries the count onto the
	// call's own accounting entry.
	calls := pendingCallEntries(t, f.rt)
	require.NotEmpty(t, calls, "the call's accounting entry is booked")
	var booked map[string]any
	for _, c := range calls {
		if c["completion_tokens"] == float64(180) {
			booked = c
		}
	}
	require.NotNil(t, booked, "the 180-output call entry is booked")
	assert.Equal(t, float64(120), booked["reasoning_tokens"],
		"the per-call entry stores reasoning_tokens 120 (S-43/FR-024) — RED")
}

// TestT_U1_S44_UnreportedReasoningStaysAbsent pins D7 row 5: a provider
// response with no reasoning count leaves reasoning_tokens absent from the
// parsed usage and from the booked per-call entry — never 0, never null.
// PIN: passes at baseline by design; must keep passing at GREEN.
// Traces to: S-44, D7 row 5, FR-024.
func TestT_U1_S44_UnreportedReasoningStaysAbsent(t *testing.T) {
	_, f := newUsageFixture(t, []string{
		`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190}}`,
	})

	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	u := usageJSON(t, resp.Usage)
	assert.NotContains(t, u, "reasoning_tokens",
		"an unreported count is absent from the parsed usage, never 0 (S-44) — PIN")
	for _, c := range pendingCallEntries(t, f.rt) {
		assert.NotContains(t, c, "reasoning_tokens",
			"an unreported count is absent from the call entry (S-44) — PIN")
	}
}

// TestT_U1_S88_FailedAttemptKeepsItsCount pins D7 row 7 at the actual HTTP
// adapter -> attempt-booking -> durable stats boundary (CHECK-08 / INFO-2).
// The adapter, NOT this test, constructs StreamProviderError from a real SSE
// failure carrying 300 output / 120 reasoning tokens. A real model-B response
// reports 80 output without reasoning. Production writers must keep A=120,
// B absent and sum=120. The TEST drives these two adapter calls: this is NOT
// automatic multi-candidate streamed fallback, live done or answering-entry
// snapshot proof. The gateway S-88/S-95 tests keep that workflow loud BLOCKED.
// Traces to: S-88, D7 row 7, FR-024, FR-036, SC-018, T-U1.
func TestT_U1_S88_FailedAttemptKeepsItsCount(t *testing.T) {
	endpoint, f := newUsageFixture(t, []string{
		"sse:data: {\"error\":{\"code\":\"402\",\"message\":\"billed partial attempt\"},\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"error\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":300,\"total_tokens\":310,\"completion_tokens_details\":{\"reasoning_tokens\":120}}}\n\ndata: [DONE]\n\n",
		`{"choices":[{"message":{"role":"assistant","content":"fallback answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":80,"total_tokens":85}}`,
	})
	streamer, ok := f.rt.activeProvider.(providers.StreamingProvider)
	require.True(t, ok, "the actual OpenRouter adapter exposes the streaming boundary")
	var visible []string
	responseA, failure := streamer.ChatStream(context.Background(), f.rf.callMessages, nil, "model-a", nil,
		func(content string) { visible = append(visible, content) }, nil)
	require.Error(t, failure, "actual SSE provider failure must surface as an error")
	require.Nil(t, responseA, "the failed stream is not a successful answer")
	var providerError *common.StreamProviderError
	require.ErrorAs(t, failure, &providerError, "the adapter creates the typed billed-stream error")
	assert.Equal(t, "402", providerError.Code)
	assert.Equal(t, "billed partial attempt", providerError.Message)
	assert.Empty(t, visible, "no visible bytes: this fixture does not violate the no-fallback-after-visible-bytes guard")
	aUsage := usageJSON(t, providerError.Usage)
	assert.Equal(t, float64(300), aUsage["completion_tokens"])
	assert.Equal(t, float64(120), aUsage["reasoning_tokens"], "S-88: actual failed-stream usage must retain the reported reasoning count")
	f.rt.accountProviderAttempt("openrouter", "model-a", responseA, failure)

	// A real second HTTP response supplies B's parsed usage. Selection/order
	// are intentionally driven at this boundary, not by a fake fallback chain.
	responseB, err := f.rt.activeProvider.Chat(context.Background(), f.rf.callMessages, nil, "model-b", nil)
	require.NoError(t, err)
	require.NotNil(t, responseB)
	require.Equal(t, "fallback answer", responseB.Content, "positive control: B really answered")
	bUsage := usageJSON(t, responseB.Usage)
	assert.Equal(t, float64(80), bUsage["completion_tokens"])
	assert.NotContains(t, bUsage, "reasoning_tokens", "B did not report a reasoning count")
	f.rt.accountProviderAttempt("openrouter", "model-b", responseB, nil)
	endpoint.mu.Lock()
	bodies := append([]map[string]any(nil), endpoint.bodies...)
	endpoint.mu.Unlock()
	require.Len(t, bodies, 2, "both attempts reached the actual HTTP edge")
	assert.Equal(t, "model-a", bodies[0]["model"])
	assert.Equal(t, true, bodies[0]["stream"], "not a manually constructed stream error")
	assert.Equal(t, "model-b", bodies[1]["model"])

	// The production writer produced both entries.
	f.rt.ts.mu.Lock()
	pending := append([]session.TranscriptEntry(nil), f.rt.ts.callAccounting.pending...)
	f.rt.ts.mu.Unlock()
	require.Len(t, pending, 2, "the failed attempt and the answer each book their own entry")
	aEntry, bEntry := pending[0], pending[1]
	require.Equal(t, "model-a", aEntry.Model)
	require.Equal(t, "model-b", bEntry.Model)

	aJSON := entryJSON(t, aEntry)
	assert.Equal(t, float64(300), aJSON["completion_tokens"], "A's billed output is booked — PIN")
	assert.Equal(t, float64(120), aJSON["reasoning_tokens"],
		"A's failed call entry keeps reasoning_tokens 120 (S-88) — RED: UsageInfo has no reasoning field")
	bJSON := entryJSON(t, bEntry)
	assert.NotContains(t, bJSON, "reasoning_tokens",
		"B is never credited with A's count (S-88) — PIN at baseline, must hold at GREEN")

	// Persist through the production transcript writer; read the by-model
	// cells the production stats writer produced.
	store, err := session.NewUnifiedStore(filepath.Join(t.TempDir(), "sessions"))
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", testDefaultAgentID)
	require.NoError(t, err)
	for _, e := range pending {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, e))
	}
	updated, err := store.GetMeta(meta.ID)
	require.NoError(t, err)
	cellsRaw, err := json.Marshal(updated.Stats.ByModel)
	require.NoError(t, err)
	var cells map[string]map[string]any
	require.NoError(t, json.Unmarshal(cellsRaw, &cells))
	require.Contains(t, cells, "model-a")
	require.Contains(t, cells, "model-b")
	assert.Equal(t, float64(300), cells["model-a"]["out"], "A's cell shows its own output")
	assert.Equal(t, float64(120), cells["model-a"]["reasoning"],
		"A's cell shows reasoning 120 (S-88/FR-036) — RED: ModelTokens has no reasoning cell")
	_, bHasReasoning := cells["model-b"]["reasoning"]
	assert.False(t, bHasReasoning,
		"B's cell carries no reasoning figure — B is never credited (S-88/SC-018) — PIN")
	var totalReasoning float64
	for _, cell := range cells {
		if r, ok := cell["reasoning"]; ok {
			totalReasoning += effortRequireType[float64](t, r)
		}
	}
	assert.Equal(t, float64(120), totalReasoning, "the cells show exactly A's 120, never 240 (SC-018)")
}

// TestT_U1_S88_ReportedZeroBeatsAbsent pins D7 row 9: a later call reporting
// a reasoning count of 0 carries it — reported zero beats absent, and the
// count stays on the call that reported it.
// Traces to: S-88, D7 row 9, FR-024.
func TestT_U1_S88_ReportedZeroBeatsAbsent(t *testing.T) {
	_, f := newUsageFixture(t, []string{
		`{"choices":[{"message":{"content":"first answer"}}],"usage":{"prompt_tokens":5,"completion_tokens":50,"total_tokens":55}}`,
		`{"choices":[{"message":{"content":"second answer"}}],"usage":{"prompt_tokens":5,"completion_tokens":50,"total_tokens":55,"completion_tokens_details":{"reasoning_tokens":0}}}`,
	})

	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)

	u := usageJSON(t, resp.Usage)
	assert.Equal(t, float64(0), u["reasoning_tokens"],
		"the reported zero is carried as 0 (D7 row 9) — RED")
	calls := pendingCallEntries(t, f.rt)
	require.Len(t, calls, 2, "each call books its own accounting entry")
	var zeroEntry map[string]any
	for _, c := range calls {
		if r, ok := c["reasoning_tokens"]; ok && r == float64(0) {
			zeroEntry = c
		}
	}
	require.NotNil(t, zeroEntry,
		"the reported zero lands on the call entry that reported it (D7 row 9) — RED")
}
