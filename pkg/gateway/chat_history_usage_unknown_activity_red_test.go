package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type historyUnknownUsageProvider struct { // external paid-provider transport boundary only
	*historyD16Provider
	entered, release chan struct{}
	once, free       sync.Once
}

func (p *historyUnknownUsageProvider) unblock() { p.free.Do(func() { close(p.release) }) }
func (p *historyUnknownUsageProvider) ChatStream(ctx context.Context, input []providers.Message, definitions []providers.ToolDefinition, model string,
	options map[string]any, onChunk func(string), onTool providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return p.historyD16Provider.ChatStream(ctx, input, definitions, model, options, onChunk, onTool)
}

func historyUnknownUsageState(t *testing.T, sid string, frames [][]byte, stage string) {
	t.Helper()
	states := 0
	for _, raw := range frames {
		if historyD16Type(t, raw) != "session_state" {
			continue
		}
		var state generated.SessionStateFrame
		require.NoError(t, json.Unmarshal(raw, &state))
		if state.SessionId != nil && *state.SessionId == sid {
			states++
			assert.Nil(t, state.SessionTokensTotal, "F5/D16: %s cannot make an absent lifetime baseline authoritative0 or a partial total", stage)
		}
	}
	require.Equal(t, 1, states, "actual addressed attach state, not an absence-only false green")
}

// Gate0 F5 extends D16's unknown control through REAL new ingress/accounting.
// Exact1200/45123/700 provider receipts are frozen in D-U2; losing the aggregate
// does not authorize a page sum, lifetime scan or new-message zero baseline.
func TestSessionTokens_UnknownBaselineSurvivesRealNewActivity(t *testing.T) {
	for _, fault := range []string{"stats_file_missing", "tokens_total_member_missing"} {
		t.Run(fault, func(t *testing.T) { historyUnknownUsageActivity(t, fault) })
	}
}

func historyUnknownUsageActivity(t *testing.T, fault string) {
	t.Helper()
	original, originalProvider, sid, _, _ := historyD16TwoUsageTurns(t)
	original.shutdown()
	stats := filepath.Join(original.store.BaseDir(), sid, "stats.json")
	body, err := os.ReadFile(stats)
	require.NoError(t, err, "real46323 aggregate existed before controlled evidence loss")
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	require.Contains(t, fields, "tokens_total")
	if fault == "stats_file_missing" {
		require.NoError(t, os.Remove(stats))
	} else {
		require.Equal(t, "tokens_total_member_missing", fault)
		delete(fields, "tokens_total")
		body, err = json.Marshal(fields)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(stats, body, 0o600))
	}
	provider := &historyUnknownUsageProvider{historyD16Provider: &historyD16Provider{replies: []historyD16Reply{{
		content: "F5 one genuinely accounted new answer", usage: providers.UsageInfo{PromptTokens: 200, CompletionTokens: 100, CacheReadTokens: 400, TotalTokens: 700},
	}}}, entered: make(chan struct{}), release: make(chan struct{})}
	h := newHistoryD16Harness(t, original.home, original.cfg, provider, true)
	t.Cleanup(provider.unblock)
	assert.Nil(t, h.store.ReadSessionTokensTotal(sid), "F5: absent/missing-member aggregate is UNKNOWN before new activity")
	assert.Nil(t, h.page(sid, "").SessionTokensTotal)
	peer := h.connect()
	historyUnknownUsageState(t, sid, h.attach(peer, sid), "fresh historical attach")
	const clientID = "gate0-f5-actual-new-input"
	h.send(peer, sid, clientID, "F5 new accepted input cannot establish lost lifetime usage")
	historyD16Await(t, provider.entered, "real model request reached controlled paid-provider boundary")
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	accepted := 0
	for _, entry := range entries {
		if entry.ClientMessageID == clientID && entry.Role == "user" {
			accepted++
		}
	}
	require.Equal(t, 1, accepted, "actual new user input is durably accepted before the zero-authority assertion")
	assert.Nil(t, h.store.ReadSessionTokensTotal(sid), "F5: new zero-token user message does NOT recover a missing historical lifetime baseline")
	assert.Nil(t, h.page(sid, "").SessionTokensTotal, "F5 REST must not publish0 after new user acceptance")
	historyUnknownUsageState(t, sid, h.attach(peer, sid), "accepted user input before provider usage")
	provider.unblock()
	done, _ := h.turnDone(peer)
	require.Equal(t, sid, done.SessionId)
	require.NotNil(t, done.Stats)
	require.NotNil(t, done.Stats.Tokens)
	require.EqualValues(t, 700, *done.Stats.Tokens, "real new per-turn delta remains known even when lifetime usage is not")
	assert.Nil(t, done.Stats.SessionTokensTotal, "F5 live done must not present700 partial usage as the historical lifetime total")
	h.event(func(event agent.Event) bool {
		payload, ok := event.Payload.(agent.TurnEndPayload)
		return event.Kind == agent.EventKindTurnEnd && ok && payload.SessionID == sid && payload.Status == agent.TurnEndStatusCompleted
	})
	require.NoError(t, h.store.FlushSessionStats(sid))
	assert.Nil(t, h.store.ReadSessionTokensTotal(sid), "F5: newly persisted partial accounting does NOT establish the missing baseline")
	assert.Nil(t, h.page(sid, "").SessionTokensTotal)
	historyUnknownUsageState(t, sid, h.attach(peer, sid), "actual700-token provider completion")
	entries, err = h.store.ReadTranscript(sid)
	require.NoError(t, err)
	var billed []int
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Tokens > 0 {
			billed = append(billed, entry.Tokens)
		}
	}
	require.Equal(t, []int{1200, 45123, 700}, billed, "all canonical per-call facts remain truthful; no repeated billing or fabricated lifetime total")
	require.Equal(t, 1, provider.count())
	require.Equal(t, 2, originalProvider.count())
	h.shutdown()
	readerProvider := &historyD16Provider{}
	reader := newHistoryD16Harness(t, h.home, h.cfg, readerProvider, false)
	assert.Nil(t, reader.store.ReadSessionTokensTotal(sid), "F5 unknown lifetime authority survives another actual close/reopen")
	assert.Nil(t, reader.page(sid, "").SessionTokensTotal)
	historyUnknownUsageState(t, sid, reader.attach(reader.connect(), sid), "fresh reload after partial accounting flush")
	require.Zero(t, readerProvider.count())
}
