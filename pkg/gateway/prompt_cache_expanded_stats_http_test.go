package gateway

// T4 — assert the NEW fields on the actual HandleTokenStats HTTP response.
// Traces to: prompt-caching-spec.md FR-002 (per-agent and per-provider hit
// percent, provider totals from stored per-call ids, reload survival,
// unpriced/subscription visible), SC-002 (0.8 fixture; null at denominator
// 0), and pr-test-analyzer.md::T4.
//
// What the existing pack lacked: the aggregator is tested pure
// (session), the handler test asserts only the OLD token/model fields
// (TestHandleTokenStats_WireShapeMatchesContract), and the screen test mocks
// fetchTokenStats with an already-correct response. If HandleTokenStats forgot
// by_provider or the ratio mapping, all three stayed green while the real
// Usage page lost the breakdown. Here the real handler is invoked over a real
// seeded store, the body is decoded into the generated contract type with
// DisallowUnknownFields, and the raw JSON is inspected for the literal-null
// ratio case.
//
// Expected values are hand-derived from the contract formula
// cache_read / (in + cache_read + cache_write) — null at denominator 0 — and
// from the spec's 100/800/100 fixture; none is read off a run.
//
// Baseline expectations and the mutation plan are in handoff.md.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// expandedStatsNewHome mints the shared home directory with the minimal
// config.json the restAPI constructor expects (mirrors
// newTestRestAPIWithHomeDevModeBypass's fixture, externalized so a SECOND api
// can be built over the SAME home for the reload case).
func expandedStatsNewHome(t *testing.T) string {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	dir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), minimalCfg, 0o600))
	return dir
}

// expandedStatsAPIAtHome builds a real restAPI (real AgentLoop, real session
// store) over the given home directory. Called twice on one home it models a
// process restart: the second loop's store reads sessions back from disk.
func expandedStatsAPIAtHome(t *testing.T, dir string) *restAPI {
	t.Helper()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         dir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(dir),
		homePath:      dir,
		taskStore:     task.New(dir + "/tasks"),
		taskLock:      task.TaskFileLock,
	}
}

// expandedStatsCall is one stored per-call accounting row. tokens is the
// authoritative total and must equal prompt+completion+cacheRead+cacheWrite —
// the same additive convention the store and aggregator document.
type expandedStatsCall struct {
	prompt     int
	completion int
	cacheRead  int
	cacheWrite int
	tokens     int
	providerID string
	billing    string
	unpriced   int
}

// expandedStatsSeed writes assistant calls through the REAL production
// accumulation path (UnifiedStore.AppendTranscriptStrict), so the per-model
// and per-provider stats groups are derived exactly as a live call derives
// them — never hand-written into a stats file.
func expandedStatsSeed(t *testing.T, api *restAPI, agentID, model string, calls ...expandedStatsCall) {
	t.Helper()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be initialized")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	require.NoError(t, err, "store.NewSession must succeed")
	for _, c := range calls {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
			Role:             "assistant",
			AgentID:          agentID,
			Timestamp:        time.Now().UTC(),
			Model:            model,
			ModelID:          model,
			ProviderID:       c.providerID,
			PromptTokens:     c.prompt,
			CompletionTokens: c.completion,
			CacheReadTokens:  c.cacheRead,
			CacheWriteTokens: c.cacheWrite,
			Tokens:           c.tokens,
			BillingClass:     c.billing,
			UnpricedTokens:   c.unpriced,
		}), "AppendTranscriptStrict must succeed for agent %q provider %q", agentID, c.providerID)
	}
}

func expandedStatsHandle(t *testing.T, api *restAPI) []byte {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	r.URL.RawQuery = "period=all"
	api.HandleTokenStats(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET /stats/tokens?period=all must return 200; body=%s", w.Body.String())
	return w.Body.Bytes()
}

func expandedStatsDecodeContract(t *testing.T, raw []byte) gen.TokenUsageSummary {
	t.Helper()
	var decoded gen.TokenUsageSummary
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&decoded),
		"handler output must decode into gen.TokenUsageSummary with DisallowUnknownFields — "+
			"a decode error means a new field is missing from, or misspelled in, the contract type; body=%s", string(raw))
	return decoded
}

func expandedStatsAgentIndex(t *testing.T, decoded *gen.TokenUsageSummary, agentID string) int {
	t.Helper()
	for i := range decoded.Agents {
		if decoded.Agents[i].AgentId == agentID {
			return i
		}
	}
	t.Fatalf("agent %q missing from the usage response (%d agents)", agentID, len(decoded.Agents))
	return -1
}

// TestExpandedStats_HandlerWireCarriesNewFields asserts the new fields on the
// real handler response: the 0.8 fixture ratio, the literal-null ratio at a
// zero denominator, distinct provider cells under one model id, and the
// unpriced/subscription counts.
func TestExpandedStats_HandlerWireCarriesNewFields(t *testing.T) {
	api := expandedStatsAPIAtHome(t, expandedStatsNewHome(t))

	// Hand-derived: ratio 800/(100+800+100) = 0.8 (SC-002 fixture).
	expandedStatsSeed(t, api, "expanded-stats-hit", "shared-model",
		expandedStatsCall{prompt: 100, completion: 10, cacheRead: 800, cacheWrite: 100, tokens: 1010, providerID: "openrouter"})
	// Two providers sharing ONE model id must stay distinct cells.
	expandedStatsSeed(t, api, "expanded-stats-shared", "shared-model",
		expandedStatsCall{prompt: 100, cacheRead: 300, cacheWrite: 100, tokens: 500, providerID: "openrouter"},
		expandedStatsCall{prompt: 100, cacheRead: 500, cacheWrite: 100, tokens: 700, providerID: "zai-coding-plan"})
	// Completion-only call: denominator 0 → literal null, not 0 and not absent.
	expandedStatsSeed(t, api, "expanded-stats-null", "null-model",
		expandedStatsCall{completion: 50, tokens: 50, providerID: "openrouter"})
	// Billing-visibility counts: one FULLY-unpriced 500-token call —
	// unpriced_tokens is a PORTION of the call's own tokens, so the portion
	// must never exceed the call total (490 in + 10 out = 500, all 500
	// unpriced) — plus a subscription call (100+0+200+0 = 300 tokens).
	expandedStatsSeed(t, api, "expanded-stats-billing", "bill-model",
		expandedStatsCall{prompt: 490, completion: 10, tokens: 500, providerID: "openrouter", billing: "unpriced", unpriced: 500},
		expandedStatsCall{prompt: 100, cacheRead: 200, tokens: 300, providerID: "openai-chatgpt", billing: "subscription"})

	raw := expandedStatsHandle(t, api)
	decoded := expandedStatsDecodeContract(t, raw)

	// Per-agent ratio on the 0.8 fixture (SC-002).
	hit := expandedStatsAgentIndex(t, &decoded, "expanded-stats-hit")
	require.NotNil(t, decoded.Agents[hit].CacheHitRatio,
		"the 0.8-fixture agent must carry a numeric cache_hit_ratio")
	assert.InDelta(t, 0.8, *decoded.Agents[hit].CacheHitRatio, 1e-9,
		"agent cache_hit_ratio must be 0.8 on the 100/800/100 fixture (SC-002)")

	// Literal null at denominator 0 — checked on the RAW body so an omitted
	// field (optional-field drift) cannot masquerade as a null.
	var top map[string]any
	require.NoError(t, json.Unmarshal(raw, &top), "body=%s", string(raw))
	agentsRaw, _ := top["agents"].([]any)
	require.NotEmpty(t, agentsRaw, "agents must be present in the raw body")
	nullRatioSeen := false
	for _, a := range agentsRaw {
		agentMap, ok := a.(map[string]any)
		if !ok || agentMap["agent_id"] != "expanded-stats-null" {
			continue
		}
		val, present := agentMap["cache_hit_ratio"]
		require.True(t, present,
			"cache_hit_ratio must be PRESENT for the zero-denominator agent — omission is the T4 regression; body=%s", string(raw))
		require.Nil(t, val,
			"cache_hit_ratio must serialize as literal null at denominator 0, got %v; body=%s", val, string(raw))
		nullRatioSeen = true
	}
	require.True(t, nullRatioSeen, "the zero-denominator agent must appear in the response")

	// Distinct provider cells under one model id. Global by_provider sums:
	//   openrouter = hit(800) + shared(300) + null(0) + unpriced(0) reads;
	//                in 100+100+0+490 = 690;
	//   zai-coding-plan = shared(500) reads;
	//   openai-chatgpt = subscription(200) reads.
	require.NotNil(t, decoded.ByProvider, "by_provider must be populated when provider data exists (FR-002)")
	byProvider := *decoded.ByProvider
	require.Len(t, byProvider, 3,
		"exactly three provider cells must exist for the seeded calls — an extra or merged cell is derivation drift")
	require.Contains(t, byProvider, "openrouter")
	require.Contains(t, byProvider, "zai-coding-plan")
	openrouterCell := byProvider["openrouter"]
	require.NotNil(t, openrouterCell.CacheRead)
	assert.Equal(t, 1100, *openrouterCell.CacheRead, "openrouter cell must sum its own stored calls' reads")
	require.NotNil(t, openrouterCell.In)
	assert.Equal(t, 690, *openrouterCell.In)
	zaiCell := byProvider["zai-coding-plan"]
	require.NotNil(t, zaiCell.CacheRead)
	assert.Equal(t, 500, *zaiCell.CacheRead, "zai-coding-plan must stay a distinct cell sharing model shared-model")
	// A positive-denominator cell MUST carry its ratio — omitting it is
	// exactly the T4 regression class, so presence is required, never
	// conditionally checked.
	require.NotNil(t, zaiCell.CacheHitRatio,
		"the zai-coding-plan cell (denominator 700) must carry cache_hit_ratio — omission is the T4 regression")
	assert.InDelta(t, 500.0/700.0, *zaiCell.CacheHitRatio, 1e-9,
		"zai cell ratio = 500/(100+500+100)")
	require.NotNil(t, openrouterCell.CacheHitRatio,
		"the openrouter cell (denominator 1990) must carry cache_hit_ratio — omission is the T4 regression")
	assert.InDelta(t, 1100.0/1990.0, *openrouterCell.CacheHitRatio, 1e-9,
		"openrouter cell ratio = 1100/(690+1100+200)")

	// The one shared model id merges into a single by_model cell:
	// reads 800 + 300 + 500 = 1600, in 100+100+100 = 300, writes 100+100+100 = 300.
	require.NotNil(t, decoded.ByModel, "top-level by_model must be populated")
	sharedCell, has := (*decoded.ByModel)["shared-model"]
	require.True(t, has, "shared-model must appear in by_model; body=%s", string(raw))
	require.NotNil(t, sharedCell.CacheRead)
	assert.Equal(t, 1600, *sharedCell.CacheRead)
	require.NotNil(t, sharedCell.In)
	assert.Equal(t, 300, *sharedCell.In)
	require.NotNil(t, sharedCell.CacheWrite)
	assert.Equal(t, 300, *sharedCell.CacheWrite)

	// Billing visibility (FR-002/FR-026): unpriced tokens and subscription
	// tokens survive to the HTTP surface as their own counters.
	require.NotNil(t, decoded.UnpricedTokens, "unpriced_tokens must be present")
	assert.Equal(t, 500, *decoded.UnpricedTokens)
	require.NotNil(t, decoded.SubscriptionTokens, "subscription_tokens must be present")
	assert.Equal(t, 300, *decoded.SubscriptionTokens)

	// Top-level cache totals reconcile with the seeded cells.
	require.NotNil(t, decoded.TokensCacheRead)
	assert.Equal(t, 1100+500+200, *decoded.TokensCacheRead)
	require.NotNil(t, decoded.TokensCacheWrite)
	assert.Equal(t, 300, *decoded.TokensCacheWrite)
}

// TestExpandedStats_CacheOnlyTotalsSurviveReload seeds the known cache-only
// split (0 input, 0 output, 800 read, 200 write — FR-002's reconciliation
// case), then invokes the handler on a SECOND real API/loop over the SAME
// home directory, so the response is served from the store read back on disk.
func TestExpandedStats_CacheOnlyTotalsSurviveReload(t *testing.T) {
	dir := expandedStatsNewHome(t)
	api1 := expandedStatsAPIAtHome(t, dir)
	expandedStatsSeed(t, api1, "expanded-stats-cacheonly", "cache-only-model",
		expandedStatsCall{cacheRead: 800, cacheWrite: 200, tokens: 1000, providerID: "deepseek"})

	api2 := expandedStatsAPIAtHome(t, dir) // reload: fresh loop, same home
	raw := expandedStatsHandle(t, api2)
	decoded := expandedStatsDecodeContract(t, raw)

	i := expandedStatsAgentIndex(t, &decoded, "expanded-stats-cacheonly")
	agent := decoded.Agents[i]
	require.NotNil(t, agent.TokensCacheRead, "cache-only read total must survive reload")
	assert.Equal(t, 800, *agent.TokensCacheRead)
	require.NotNil(t, agent.TokensCacheWrite, "cache-only write total must survive reload")
	assert.Equal(t, 200, *agent.TokensCacheWrite)
	require.NotNil(t, agent.CacheHitRatio, "cache-only ratio = 800/(0+800+200)")
	assert.InDelta(t, 0.8, *agent.CacheHitRatio, 1e-9)

	// The stored per-call provider id survives reload into the by_provider
	// dimension (FR-002; the HTTP-layer twin of TestProviderId_SurvivesReload).
	require.NotNil(t, decoded.ByProvider, "by_provider must survive reload")
	byProvider := *decoded.ByProvider
	require.Contains(t, byProvider, "deepseek")
	cell := byProvider["deepseek"]
	require.NotNil(t, cell.CacheRead)
	assert.Equal(t, 800, *cell.CacheRead)
	require.NotNil(t, cell.CacheWrite)
	assert.Equal(t, 200, *cell.CacheWrite)
	require.NotNil(t, cell.CacheHitRatio,
		"the deepseek cell (denominator 1000) must carry cache_hit_ratio after reload — omission is the T4 regression")
	assert.InDelta(t, 0.8, *cell.CacheHitRatio, 1e-9, "deepseek cell ratio = 800/(0+800+200)")
}
