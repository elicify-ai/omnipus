package agent

// T2 closes the last composition seam: actual successful Anthropic SDK SSE →
// real turn accounting → persisted assistant entry, not an already-normalized fake.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestExpandedStreamAccounting_SuccessSDKWriteSplitReachesTranscript(t *testing.T) {
	const model = "claude-sonnet-4-5"
	const answer = "expanded-stream-accounted"
	const events = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":0,\"cache_read_input_tokens\":800,\"cache_creation_input_tokens\":100,\"cache_creation\":{\"ephemeral_5m_input_tokens\":40,\"ephemeral_1h_input_tokens\":60}}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"expanded-stream-accounted\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":10}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, events)
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("EXPANDED_STREAM_ACCOUNTING_KEY", "fixture")
	p, selected, err := providers.CreateProviderFromConfig(&config.ModelConfig{Provider: "anthropic", Model: model, APIBase: srv.URL, APIKeyRef: "EXPANDED_STREAM_ACCOUNTING_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "anthropic", Model: selected}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 2
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	msgBus.SetStreamDelegate(&expandedThinkingStreamDelegate{streamer: &expandedThinkingStreamer{}})
	al := mustNewAgentLoop(t, cfg, msgBus, p)
	t.Cleanup(al.Close)
	ag := registerAgent(t, al, home, "mia", p)
	ag.mu.Lock()
	ag.Candidates = []providers.FallbackCandidate{{Provider: "anthropic", Model: selected}}
	ag.mu.Unlock()
	ag.StoreProviderPool(map[string]providers.LLMProvider{"anthropic": p})
	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := al.ProcessScheduled(t.Context(), "mia", meta.ID, "return the fixture", "scheduled", meta.ID)
	if err != nil || reply != answer {
		t.Fatalf("real streamed turn reply=%q err=%v, want%s", reply, err, answer)
	}
	entries, err := al.GetSessionStore().ReadTranscript(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	billed := 0
	for _, entry := range entries {
		if entry.Role != "assistant" || entry.BillingClass == "" {
			continue
		}
		billed++
		if entry.ProviderID != "anthropic" || entry.ModelID != selected || entry.PromptTokens != 100 || entry.CompletionTokens != 10 || entry.CacheReadTokens != 800 || entry.CacheWriteTokens != 100 || entry.CacheWrite5mTokens != 40 || entry.CacheWrite1hTokens != 60 || entry.Tokens != 1010 {
			t.Errorf("stored SDK stream accounting=%+v, want100/10/800/100 writes40+60 total1010", entry)
		}
	}
	if billed != 1 {
		t.Fatalf("successful SDKstream stored%d accountingentries, want1", billed)
	}
}
