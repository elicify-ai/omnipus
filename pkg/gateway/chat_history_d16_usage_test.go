package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D-U2's exact provider receipts, including cache accounting. Reconstructing
// totals from uncached prompt+completion would be wrong for every call here.
func historyD16UsageProvider() *historyD16Provider {
	return &historyD16Provider{replies: []historyD16Reply{
		{content: "D16 earlier accounted answer", usage: providers.UsageInfo{PromptTokens: 100, CompletionTokens: 100, CacheReadTokens: 1000, TotalTokens: 1200}},
		{content: "D16 completed accounted answer", usage: providers.UsageInfo{PromptTokens: 123, CompletionTokens: 1000, CacheReadTokens: 44000, TotalTokens: 45123}},
		{content: "D16 next accounted answer", usage: providers.UsageInfo{PromptTokens: 200, CompletionTokens: 100, CacheReadTokens: 400, TotalTokens: 700}},
	}}
}

func historyD16AssertTotal(t *testing.T, total *int, want int, carrier string) {
	t.Helper()
	if assert.NotNil(t, total, "FR-027: %s must publish the KNOWN authoritative absolute total, not omit/reset it", carrier) {
		assert.Equal(t, want, *total, "FR-027: %s absolute total, never delta/page-sum/frames_emitted", carrier)
	}
}

func historyD16StoredTotal(t *testing.T, store *session.UnifiedStore, sid string, want int) {
	t.Helper()
	require.NoError(t, store.FlushSessionStats(sid))
	meta, err := store.GetMeta(sid)
	require.NoError(t, err)
	require.Equal(t, want, meta.Stats.TokensTotal, "prerequisite: real accounting aggregate, never a manually seeded counter")
	data, err := os.ReadFile(filepath.Join(store.BaseDir(), sid, "stats.json"))
	require.NoError(t, err)
	var disk session.SessionStats
	require.NoError(t, json.Unmarshal(data, &disk))
	require.Equal(t, want, disk.TokensTotal, "prerequisite: authoritative aggregate is actually saved")
}

func historyD16CompleteTurn(t *testing.T, h *historyD16Harness, peer *websocket.Conn, sid, client string, wantDelta, wantTotal int) generated.DoneFrame {
	t.Helper()
	h.send(peer, sid, client, "D16 consume the next frozen provider receipt")
	done, _ := h.turnDone(peer)
	require.Equal(t, sid, done.SessionId)
	require.NotNil(t, done.Stats)
	require.NotNil(t, done.Stats.Tokens)
	require.EqualValues(t, wantDelta, *done.Stats.Tokens, "positive control: real live per-turn delta")
	h.event(func(event agent.Event) bool {
		if event.Kind != agent.EventKindTurnEnd {
			return false
		}
		payload, ok := event.Payload.(agent.TurnEndPayload)
		return ok && payload.SessionID == sid && payload.Status == agent.TurnEndStatusCompleted
	})
	historyD16StoredTotal(t, h.store, sid, wantTotal)
	return done
}

func historyD16TwoUsageTurns(t *testing.T) (*historyD16Harness, *historyD16Provider, string, *websocket.Conn, []generated.DoneFrame) {
	t.Helper()
	home, cfg := historyD16Config(t)
	provider := historyD16UsageProvider()
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	first := historyD16CompleteTurn(t, h, peer, sid, "d16-usage-first", 1200, 1200)
	// Real display-only canonical groups omit the earlier billed source from
	// latest40. They carry no usage and are not injected model messages.
	pagingSeed(t, h.store, sid, "d16-display-only", 45)
	second := historyD16CompleteTurn(t, h, peer, sid, "d16-usage-second", 45123, 46323)
	require.Equal(t, 2, provider.count(), "two genuine external model calls")
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	var billed []int
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Tokens > 0 {
			billed = append(billed, entry.Tokens)
		}
	}
	require.Equal(t, []int{1200, 45123}, billed, "actual immutable call accounting, exactly once per call")
	return h, provider, sid, peer, []generated.DoneFrame{first, second}
}

func historyD16AssertAttachUsage(t *testing.T, sid string, frames [][]byte, want int) {
	t.Helper()
	states, replayEnds := 0, 0
	for _, raw := range frames {
		switch historyD16Type(t, raw) {
		case "session_state":
			var state generated.SessionStateFrame
			require.NoError(t, json.Unmarshal(raw, &state))
			if state.SessionId != nil && *state.SessionId == sid {
				states++
				historyD16AssertTotal(t, state.SessionTokensTotal, want, "session-scoped attach state")
			}
		case "done":
			var done generated.DoneFrame
			require.NoError(t, json.Unmarshal(raw, &done))
			if done.Stats != nil && done.Stats.FramesEmitted != nil {
				replayEnds++
				assert.Nil(t, done.Stats.SessionTokensTotal, "D16: replay diagnostics are never a fresh absolute usage observation")
				assert.Nil(t, done.Stats.Tokens, "D16: replay terminator is not a per-turn debit")
			}
		}
	}
	require.Equal(t, 1, states, "actual addressed current state exists once")
	require.Equal(t, 1, replayEnds, "actual historical replay completed once")
}

func historyD16UsageReloadCarriers(t *testing.T) {
	h, provider, sid, _, live := historyD16TwoUsageTurns(t)
	// Only after genuine live/accounting positives do the new-field oracles run.
	historyD16AssertTotal(t, live[0].Stats.SessionTokensTotal, 1200, "first live done")
	historyD16AssertTotal(t, live[1].Stats.SessionTokensTotal, 46323, "second live done")
	h.shutdown()
	readerProvider := &historyD16Provider{}
	reader := newHistoryD16Harness(t, h.home, h.cfg, readerProvider, false)
	historyD16StoredTotal(t, reader.store, sid, 46323)
	readLogs := pagingLogCapture(t)
	page := reader.page(sid, "")
	require.Len(t, page.Entries, 40, "default bounded suffix, not lifetime history")
	var pageSum int
	for _, entry := range page.Entries {
		if entry.Tokens != nil {
			pageSum += *entry.Tokens
		}
	}
	require.Equal(t, 45123, pageSum, "positive omitted-prefix control")
	require.NotEqual(t, 46323, pageSum)
	historyD16AssertTotal(t, page.SessionTokensTotal, 46323, "fresh bounded REST page")
	rows := pagingSummaryRows(readLogs())
	require.Len(t, rows, 1)
	assert.EqualValues(t, 40, rows[0]["selected_archive_records"], "usage metadata must not force omitted-prefix decoding")
	assert.LessOrEqual(t, pagingCounter(t, rows[0], "lookahead_archive_records"), float64(1))
	detail := reader.detail(sid)
	assert.Equal(t, 46323, detail.Session.Stats.TokensTotal, "existing detail authority is the same saved total")
	peer := reader.connect()
	historyD16AssertAttachUsage(t, sid, reader.attach(peer, sid), 46323)
	historyD16AssertAttachUsage(t, sid, reader.attach(peer, sid), 46323)
	historyD16StoredTotal(t, reader.store, sid, 46323)
	require.Equal(t, 0, readerProvider.count(), "reopen/Retry/snapshot execute no model")
	require.Equal(t, 2, provider.count(), "no resend/re-execution through reload")
}

func TestSessionTokens_ReloadAbsoluteTotalParity(t *testing.T) {
	t.Run("recorded_provider_usage_survives_fresh_REST_detail_and_snapshot", historyD16UsageReloadCarriers)
	t.Run("held_done_and_snapshot_share_absolute_authority", historyD16UsageHeldDone)
	t.Run("stale_page_does_not_change_newer_accounting", historyD16UsageStalePage)
	t.Run("real_zero_unknown_and_other_session_are_distinct", historyD16UsageZeroUnknown)
	t.Run("known_usage_is_preserved_in_live_only_state", historyD16UsageLiveOnly)
}
