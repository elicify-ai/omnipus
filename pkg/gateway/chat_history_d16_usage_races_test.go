package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func historyD16UsageHeldDone(t *testing.T) {
	h, provider, sid, peer, _ := historyD16TwoUsageTurns(t)
	var live generated.DoneFrame
	var once sync.Once
	setAttachHook(t, func(attached string) {
		if attached == sid {
			once.Do(func() { live = historyD16CompleteTurn(t, h, peer, sid, "d16-usage-held-third", 700, 47023) })
		}
	})
	wc := &wsConn{sendCh: make(chan []byte, 1024), doneCh: make(chan struct{})}
	h.handler.mu.Lock()
	h.handler.sessions["d16-held-current"] = wc
	h.handler.mu.Unlock()
	h.handler.handleAttachSession(context.Background(), "d16-held-current", sid, nil, wc)
	wire := pagingDrain(t, wc)
	complete := indexOfType(wire, "catch_up_complete")
	require.GreaterOrEqual(t, complete, 0, "real held/current catch-up completes")
	frames := make([][]byte, len(wire))
	for i := range wire {
		frames[i] = wire[i].raw
	}
	historyD16AssertAttachUsage(t, sid, frames, 47023)
	historyD16AssertTotal(t, live.Stats.SessionTokensTotal, 47023, "actual third-turn live done")
	held := 0
	for i, raw := range frames {
		if historyD16Type(t, raw) != "done" {
			continue
		}
		var done generated.DoneFrame
		require.NoError(t, json.Unmarshal(raw, &done))
		if done.Stats != nil && done.Stats.Tokens != nil {
			held++
			assert.Greater(t, i, complete, "live done held at bind may not overtake catch-up completion")
			require.NotNil(t, done.Seq)
			require.NotNil(t, wire[complete].Seq)
			assert.Greater(t, *done.Seq, *wire[complete].Seq)
			assert.EqualValues(t, 700, *done.Stats.Tokens, "one new turn delta, not the session baseline")
			historyD16AssertTotal(t, done.Stats.SessionTokensTotal, 47023, "held live done absolute")
		}
	}
	require.Equal(t, 1, held)
	historyD16StoredTotal(t, h.store, sid, 47023)
	historyD16AssertTotal(t, h.page(sid, "").SessionTokensTotal, 47023, "post-held-done REST")
	require.Equal(t, 3, provider.count(), "snapshot/held delivery does not repeat model work")
}

type historyD16HeldResponse struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	free    sync.Once
}

func (w *historyD16HeldResponse) Write(body []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.ResponseRecorder.Write(body)
}
func (w *historyD16HeldResponse) unblock() { w.free.Do(func() { close(w.release) }) }

func historyD16UsageStalePage(t *testing.T) {
	h, provider, sid, peer, _ := historyD16TwoUsageTurns(t)
	writer := &historyD16HeldResponse{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(writer.unblock)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		h.api.HandleSessions(writer, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages", nil))
	}()
	select {
	case <-writer.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("D16 real prepared stale page never reached the outward delivery boundary")
	}
	third := historyD16CompleteTurn(t, h, peer, sid, "d16-usage-stale-third", 700, 47023)
	writer.unblock()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("D16 held outward page did not join")
	}
	require.Equal(t, http.StatusOK, writer.Code, writer.Body.String())
	var stale generated.SessionHistoryResponse
	require.NoError(t, json.Unmarshal(writer.Body.Bytes(), &stale))
	historyD16AssertTotal(t, stale.SessionTokensTotal, 46323, "real earlier prepared page cut")
	historyD16AssertTotal(t, third.Stats.SessionTokensTotal, 47023, "newer accounted live done")
	historyD16StoredTotal(t, h.store, sid, 47023)
	historyD16AssertTotal(t, h.page(sid, "").SessionTokensTotal, 47023, "fresh page after stale delivery")
	historyD16AssertAttachUsage(t, sid, h.attach(peer, sid), 47023)
	historyD16AssertAttachUsage(t, sid, h.attach(peer, sid), 47023)
	require.Equal(t, 3, provider.count(), "stale page/Retry/replay cannot add a turn or debit again")
}

func historyD16UsageZeroUnknown(t *testing.T) {
	h, provider, sid, _, _ := historyD16TwoUsageTurns(t)
	zero := h.session()
	zeroMeta, err := h.store.GetMeta(zero)
	require.NoError(t, err)
	require.Zero(t, zeroMeta.Stats.TokensTotal, "real newly created, never-accounted session")
	zeroHistory, err := h.store.ReadTranscript(zero)
	require.NoError(t, err)
	require.Empty(t, zeroHistory, "genuine unused zero, not a historical session with lost evidence")
	unknown := h.session()
	require.NoError(t, h.store.AppendTranscriptStrict(unknown, session.TranscriptEntry{
		ID: "d16-historical-no-usage", Role: "user", Content: "historical display without an authoritative usage aggregate",
		AgentID: "mia", ViewMembership: session.ViewMembershipChat, Timestamp: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
	}))
	h.shutdown()
	statsPath := filepath.Join(h.store.BaseDir(), unknown, "stats.json")
	_, err = os.ReadFile(statsPath)
	require.NoError(t, err, "instrument: product-writer aggregate file existed before evidence loss")
	require.NoError(t, os.Remove(statsPath), "controlled absent-aggregate historical fixture, no invented counter")
	readerProvider := &historyD16Provider{}
	reader := newHistoryD16Harness(t, h.home, h.cfg, readerProvider, false)
	historyD16AssertTotal(t, reader.page(sid, "").SessionTokensTotal, 46323, "unrelated known session after another's evidence loss")
	historyD16AssertTotal(t, reader.page(zero, "").SessionTokensTotal, 0, "genuinely unused authoritative zero")
	assert.Nil(t, reader.page(unknown, "").SessionTokensTotal, "D16 missing usage authority is unknown, never manufactured zero")
	peer := reader.connect()
	frames := reader.attach(peer, unknown)
	states := 0
	for _, raw := range frames {
		if historyD16Type(t, raw) == "session_state" {
			var state generated.SessionStateFrame
			require.NoError(t, json.Unmarshal(raw, &state))
			if state.SessionId != nil && *state.SessionId == unknown {
				states++
				assert.Nil(t, state.SessionTokensTotal, "unknown usage does not reset a previously known client total")
				historyD16RequireOmitted(t, raw, "session_tokens_total", "unknown-usage scoped state")
			}
		}
	}
	require.Equal(t, 1, states)
	historyD16AssertAttachUsage(t, sid, reader.attach(peer, sid), 46323)
	require.Equal(t, 0, readerProvider.count())
	require.Equal(t, 2, provider.count())
}
