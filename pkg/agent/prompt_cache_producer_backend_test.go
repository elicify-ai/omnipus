package agent

// Backend-authored supplemental tests, explicitly requested by the squad lead.
// These do not replace or alter the QA RED pack. CHECK must audit this file.

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Prices and expectations come from the approved spec's arithmetic and D18,
// not from Price. Only the external model transport is fake; the real turn,
// transcript producer, catalog lookup and session aggregation all run.
const backendCallCatalog = `{
 "schema_version":"2.0.0","version":"v2026.10.16","updated_at":"2026-10-16T00:00:00Z","source":"fixture",
 "default_resize_limits":{"long_edge_px":7680,"max_bytes":10485760},
 "providers":[
  {"id":"anthropic","name":"Native fixture","api":"https://native.example/v1","protocol":"anthropic","tier":"standard","auth_methods":["api_key"],"models":[{"id":"cache-model","name":"Fixture","tool_call":true,"context_window":200000,"max_output_tokens":4096,"input_modalities":["text"],"status":"active","cost":{"input":3,"output":15,"cache_read":0.3,"cache_write":3.75}}]},
  {"id":"cohere","name":"Mixed fixture","api":"https://mixed.example/v1","protocol":"openai-compatible","tier":"standard","auth_methods":["api_key"],"models":[{"id":"cache-model","name":"Fixture","tool_call":true,"context_window":200000,"max_output_tokens":4096,"input_modalities":["text"],"status":"active","cost":{"input":3,"output":15,"cache_read":0.3,"cache_write":3.75}}]},
  {"id":"unpriced-fixture","name":"Unpriced fixture","api":"https://unpriced.example/v1","protocol":"openai-compatible","tier":"standard","auth_methods":["api_key"],"models":[{"id":"cache-model","name":"Fixture","tool_call":true,"context_window":200000,"max_output_tokens":4096,"input_modalities":["text"],"status":"active"}]},
  {"id":"openai-chatgpt","name":"Subscription fixture","api":"https://subscription.example/v1","protocol":"openai-compatible","tier":"standard","auth_methods":["sign_in"],"models":[{"id":"cache-model","name":"Fixture","tool_call":true,"context_window":200000,"max_output_tokens":4096,"input_modalities":["text"],"status":"active","cost":{"input":3,"output":15,"cache_read":0.3,"cache_write":3.75}}]}
 ]
}`

type backendAccountingProvider struct {
	usage *providers.UsageInfo
	calls int
}

func (p *backendAccountingProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	return &providers.LLMResponse{Content: "ACCOUNTED", Usage: p.usage, FinishReason: "stop"}, nil
}
func (*backendAccountingProvider) GetDefaultModel() string { return "cache-model" }

func TestBackendProducer_PersistsRealCallClassification(t *testing.T) {
	ordinary := providers.UsageInfo{PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 800, CacheWriteTokens: 100, CacheWrite5mTokens: 40, CacheWrite1hTokens: 60, TotalTokens: 1010}
	cacheOnly := providers.UsageInfo{CacheReadTokens: 800, CacheWriteTokens: 200, CacheWrite5mTokens: 50, CacheWrite1hTokens: 150, TotalTokens: 1000}
	cases := []struct {
		name, provider, billing string
		usage                   providers.UsageInfo
		cost                    float64
		unpriced, subscription  int
	}{
		{"native priced", "anthropic", "priced", ordinary, 0.0012, 0, 0},
		{"mixed one-hour unpriced", "cohere", "priced", ordinary, 0.00084, 60, 0},
		{"missing input price", "unpriced-fixture", "unpriced", ordinary, 0, 1010, 0},
		{"subscription", "openai-chatgpt", "subscription", ordinary, 0, 0, 1010},
		{"cache-only known split", "anthropic", "priced", cacheOnly, 0.0013275, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, parseErr := catalog.NewCatalog([]byte(backendCallCatalog))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			previous := providers.ProviderCatalog()
			providers.SetCatalog(cat)
			t.Cleanup(func() { providers.SetCatalog(previous) })
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			fake := &backendAccountingProvider{usage: &tc.usage}
			cfg := &config.Config{}
			cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: tc.provider, Model: "cache-model"}
			cfg.Agents.Defaults.MaxTokens = 4096
			cfg.Agents.Defaults.MaxToolIterations = 3
			msgBus := bus.NewMessageBus()
			t.Cleanup(func() { msgBus.Close() })
			al := mustNewAgentLoop(t, cfg, msgBus, fake)
			t.Cleanup(al.Close)
			ag := registerAgent(t, al, home, "mia", fake)
			ag.mu.Lock()
			ag.Candidates = []providers.FallbackCandidate{{Provider: tc.provider, Model: "cache-model"}}
			ag.mu.Unlock()
			ag.StoreProviderPool(map[string]providers.LLMProvider{tc.provider: fake})
			meta, createErr := al.GetSessionStore().NewScheduledSession("mia")
			if createErr != nil {
				t.Fatal(createErr)
			}
			reply, turnErr := al.ProcessScheduled(context.Background(), "mia", meta.ID, "Return ACCOUNTED", "scheduled", meta.ID)
			if turnErr != nil {
				t.Fatalf("real turn: %v", turnErr)
			}
			if reply != "ACCOUNTED" || fake.calls != 1 {
				t.Fatalf("reply=%q provider calls=%d, want ACCOUNTED / 1", reply, fake.calls)
			}
			store := al.ResolveSessionStore(meta.ID)
			entries, readErr := store.ReadTranscript(meta.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			accounting := []session.TranscriptEntry{}
			for _, entry := range entries {
				if entry.Role == "assistant" && entry.BillingClass != "" {
					accounting = append(accounting, entry)
				}
			}
			if len(accounting) != 1 {
				t.Fatalf("stored assistant accounting entries=%d, want exactly 1 real-call entry; transcript=%+v", len(accounting), entries)
			}
			entry := accounting[0]
			if entry.ProviderID != tc.provider || entry.ModelID != "cache-model" || entry.BillingClass != tc.billing || entry.UnpricedTokens != tc.unpriced {
				t.Errorf("stored identity/classification=%q/%q/%q unpriced=%d, want %q/cache-model/%q/%d", entry.ProviderID, entry.ModelID, entry.BillingClass, entry.UnpricedTokens, tc.provider, tc.billing, tc.unpriced)
			}
			if entry.CacheWrite5mTokens != tc.usage.CacheWrite5mTokens || entry.CacheWrite1hTokens != tc.usage.CacheWrite1hTokens {
				t.Errorf("write split=%d/%d, want %d/%d", entry.CacheWrite5mTokens, entry.CacheWrite1hTokens, tc.usage.CacheWrite5mTokens, tc.usage.CacheWrite1hTokens)
			}
			if entry.PromptTokens != tc.usage.PromptTokens || entry.CompletionTokens != tc.usage.CompletionTokens || entry.CacheReadTokens != tc.usage.CacheReadTokens || entry.CacheWriteTokens != tc.usage.CacheWriteTokens || entry.Tokens != tc.usage.TotalTokens {
				t.Errorf("entry counters=%+v, want normalized split %+v", entry, tc.usage)
			}
			if math.Abs(entry.Cost-tc.cost) > 1e-12 {
				t.Errorf("entry cost=%g, want spec arithmetic %g", entry.Cost, tc.cost)
			}
			if flushErr := store.FlushSessionStats(meta.ID); flushErr != nil {
				t.Fatal(flushErr)
			}
			reopened, openErr := session.NewUnifiedStore(store.BaseDir())
			if openErr != nil {
				t.Fatal(openErr)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			saved, metaErr := reopened.GetMeta(meta.ID)
			if metaErr != nil {
				t.Fatal(metaErr)
			}
			if saved.Stats.TokensTotal != tc.usage.TotalTokens || saved.Stats.ByProvider[tc.provider].Total != tc.usage.TotalTokens || saved.Stats.UnpricedTokens != tc.unpriced || saved.Stats.SubscriptionTokens != tc.subscription {
				t.Errorf("reloaded stats=%+v, want total/provider=%d unpriced=%d subscription=%d; no double count", saved.Stats, tc.usage.TotalTokens, tc.unpriced, tc.subscription)
			}
		})
	}
}
