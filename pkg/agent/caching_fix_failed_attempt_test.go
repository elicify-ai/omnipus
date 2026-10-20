package agent

// Supplemental failure-dispatch tests. CHECK must audit this file.
// Oracle: every billed attempt (not just a successful chain) contributes once,
// attributed to the provider/model that actually handled that attempt.

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type cachingFixBilledProvider struct {
	calls   int
	failure error
	usage   *providers.UsageInfo
}

func (p *cachingFixBilledProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.failure != nil {
		return nil, p.failure
	}
	return &providers.LLMResponse{Content: "ACCOUNTED", FinishReason: "stop", Usage: p.usage}, nil
}
func (*cachingFixBilledProvider) GetDefaultModel() string { return "cache-model" }

func TestCachingFixBilledFailureThenFallback(t *testing.T) {
	cat, err := catalog.NewCatalog([]byte(backendCallCatalog))
	if err != nil {
		t.Fatal(err)
	}
	previous := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(previous) })
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	// Spec prices: A =100*3+800*.3+40*3.75+60*6+10*15 per million=.0012.
	// B =200*3+20*15 per million=.0009. Total tokens=1010+220=1230.
	failedUsage := &providers.UsageInfo{PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 800, CacheWriteTokens: 100, CacheWrite5mTokens: 40, CacheWrite1hTokens: 60, TotalTokens: 1010}
	a := &cachingFixBilledProvider{failure: &common.StreamProviderError{Code: "server_error", Message: "provider disconnected", Usage: failedUsage}}
	b := &cachingFixBilledProvider{usage: &providers.UsageInfo{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220}}
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "anthropic", Model: "cache-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 3
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, a)
	t.Cleanup(al.Close)
	ag := registerAgent(t, al, home, "mia", a)
	ag.mu.Lock()
	ag.Candidates = []providers.FallbackCandidate{{Provider: "anthropic", Model: "cache-model"}, {Provider: "cohere", Model: "cache-model"}}
	ag.mu.Unlock()
	ag.StoreProviderPool(map[string]providers.LLMProvider{"anthropic": a, "cohere": b})
	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := al.ProcessScheduled(t.Context(), "mia", meta.ID, "Return ACCOUNTED", "scheduled", meta.ID)
	if err != nil || reply != "ACCOUNTED" {
		t.Fatalf("fallback healthy control: reply=%q error=%v", reply, err)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Fatalf("provider calls A/B=%d/%d, want1/1", a.calls, b.calls)
	}
	store := al.ResolveSessionStore(meta.ID)
	entries, err := store.ReadTranscript(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	var billed []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" && e.BillingClass != "" {
			billed = append(billed, e)
		}
	}
	if len(billed) != 2 {
		t.Fatalf("billed-A-then-B has %d accounting entries, want exactly2; entries=%+v", len(billed), billed)
	}
	for i, want := range []struct {
		provider string
		total    int
		cost     float64
	}{{"anthropic", 1010, .0012}, {"cohere", 220, .0009}} {
		got := billed[i]
		if got.ProviderID != want.provider || got.ModelID != "cache-model" || got.Tokens != want.total || math.Abs(got.Cost-want.cost) > 1e-12 {
			t.Errorf("attempt%d=%+v, want provider=%s tokens=%d cost=%g", i, got, want.provider, want.total, want.cost)
		}
	}
	if flushErr := store.FlushSessionStats(meta.ID); flushErr != nil {
		t.Fatal(flushErr)
	}
	reopened, err := session.NewUnifiedStore(store.BaseDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	saved, err := reopened.GetMeta(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Stats.TokensTotal != 1230 || saved.Stats.ByProvider["anthropic"].Total != 1010 || saved.Stats.ByProvider["cohere"].Total != 220 || math.Abs(saved.Stats.Cost-.0021) > 1e-12 {
		t.Fatalf("reloaded per-provider accounting lost/duplicated a billed attempt: %+v", saved.Stats)
	}
}
