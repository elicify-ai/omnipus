package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Calls to handleAttachSession below are synchronous. Nonblocking drain observes
// exactly the completed invocation, never a guessed sleep/quiet-period fence.
func pagingDrain(t *testing.T, wc *wsConn) []wireFrame {
	t.Helper()
	out := make([]wireFrame, 0)
	for {
		select {
		case b := <-wc.sendCh:
			out = append(out, decodeWire(t, b))
		default:
			return out
		}
	}
}

func pagingAPIForAttach(f *attachFixture) *restAPI {
	return &restAPI{agentLoop: f.h.agentLoop, homePath: f.h.agentLoop.GetConfig().Agents.Defaults.Home}
}

func pagingDetail(t *testing.T, api *restAPI, sid string) generated.SessionDetail {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var d generated.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &d))
	require.Equal(t, sid, d.Session.Id)
	return d
}

func TestSessionHistory_RESTDetailSnapshotSelectSamePage(t *testing.T) {
	f := newAttachFixture(t, "history-parity", "live-answer")
	pagingSeed(t, f.store, f.sid, "a", 61)
	api := pagingAPIForAttach(f)
	p := pagingRead(t, api, f.sid, "")
	require.Equal(t, pagingWantIDs("a", 22, 61), pagingIDs(p), "default40 from shared request factory")
	d := pagingDetail(t, api, f.sid)
	require.Equal(t, "available", d.HistoryState.Status)
	require.Nil(t, d.HistoryState.Reason)
	require.Nil(t, d.HistoryState.Message)
	require.NotNil(t, d.Messages)
	require.Equal(t, p.Entries, *d.Messages, "detail has same bounded public projection")
	require.NotNil(t, d.PageInfo)
	require.Equal(t, p.PageInfo, *d.PageInfo, "same factory/meter at unchanged cut")
	require.Equal(t, &p.HasEarlier, d.HasEarlier)
	require.Equal(t, &p.HasLater, d.HasLater)
	require.Equal(t, p.NextBefore, d.NextBefore)
	require.Equal(t, p.NextAfter, d.NextAfter)
	wc := f.newConn("history-parity-view")
	f.h.handleAttachSession(context.Background(), "history-parity-view", f.sid, nil, wc)
	frames := pagingDrain(t, wc)
	si := indexOfType(frames, "session_snapshot")
	require.Equal(t, 0, si, "read first, then snapshot opens successful history replacement")
	var snap generated.SessionSnapshotFrame
	require.NoError(t, json.Unmarshal(frames[si].raw, &snap))
	require.Equal(t, p.PageInfo, snap.PageInfo, "REST/WS same selected range/max-carrier meter")
	require.Equal(t, p.HasEarlier, snap.HasEarlier)
	require.Equal(t, p.HasLater, snap.HasLater)
	require.Equal(t, p.NextBefore, snap.NextBefore)
	require.Equal(t, p.NextAfter, snap.NextAfter)
	var ids []string
	for _, fr := range frames {
		if fr.Type == "replay_message" {
			var r generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(fr.raw, &r))
			require.NotNil(t, r.Id)
			ids = append(ids, *r.Id)
			require.NotNil(t, r.HistoryOrigin, "every entry-derived frame has local actual source origin")
			require.NotNil(t, r.HistoryOrigin.EntryId)
			require.Equal(t, *r.Id, *r.HistoryOrigin.EntryId)
			require.Equal(t, *p.Entries[len(ids)-1].HistoryPosition, r.HistoryOrigin.Position, "not transport seq or neighboring prose owner")
		}
	}
	require.Equal(t, pagingIDs(p), ids, "carrier parity is source identities, not equal frame counts")
	done := indexOfType(frames, "done")
	complete := indexOfType(frames, "catch_up_complete")
	require.Greater(t, done, 0)
	require.Greater(t, complete, done, "interim buffer releases on complete, never replay done")
	require.Equal(t, "snapshot", frames[complete].Mode)
}

func TestAttach_PagedSnapshotPreservesLiveAndControls(t *testing.T) {
	f := newAttachFixture(t, "history-live", "live-answer")
	pagingSeed(t, f.store, f.sid, "saved", 1)
	require.NoError(t, f.streamer.Update(context.Background(), "unpersisted-before-bind "))
	setAttachHook(t, func(sid string) {
		require.Equal(t, f.sid, sid)
		require.NoError(t, f.streamer.Update(context.Background(), "held-after-bind"))
	})
	wc := f.newConn("history-live-view")
	f.h.handleAttachSession(context.Background(), "history-live-view", f.sid, nil, wc)
	frames := pagingDrain(t, wc)
	complete := indexOfType(frames, "catch_up_complete")
	done := indexOfType(frames, "done")
	require.Greater(t, done, 0)
	require.Greater(t, complete, done, "current projection can still follow replay done")
	state := indexOfType(frames, "session_state")
	require.Greater(t, state, 0)
	var sf generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(frames[state].raw, &sf))
	require.NotNil(t, sf.HistoryState, "D7a state explicitly distinguishes ready history from live-only")
	require.Equal(t, "available", sf.HistoryState.Status)
	var projection, held string
	for i, fr := range frames {
		if fr.Type != "token" {
			continue
		}
		if fr.Seq != nil && *fr.Seq > *frames[complete].Seq {
			require.Greater(t, i, complete, "seq>W cannot overtake completed catch-up")
			held += fr.Content
		} else {
			require.Greater(t, i, done, "uncovered current projection after historical replay")
			require.Less(t, i, complete)
			projection += fr.Content
		}
	}
	require.Equal(t, "unpersisted-before-bind ", projection, "still-unpersisted answer is not suppressed by saved history")
	require.Equal(t, "held-after-bind", held, "held live tail delivered exactly once after complete")
	require.True(t, f.h.connBoundToSession(wc, f.sid), "history reads do not detach authorized active work")
}

func TestAttach_PagedIncrementalKeepsWholeJournal(t *testing.T) {
	f := newAttachFixture(t, "history-incremental", "live-answer")
	start := f.head()
	for i := 0; i < 41; i++ {
		require.NoError(t, f.streamer.Update(context.Background(), fmt.Sprintf("%02d ", i)))
	}
	wc := f.newConn("history-incremental-view")
	f.h.handleAttachSession(context.Background(), "history-incremental-view", f.sid, f.cursorAt(start), wc)
	frames := pagingDrain(t, wc)
	require.Equal(t, -1, indexOfType(frames, "session_snapshot"), "servable live cursor never invokes latest-page wipe")
	count := 0
	for _, fr := range frames {
		if fr.Type == "token" {
			count++
		}
	}
	require.Equal(t, 41, count, "default40 history target must not truncate41 live journal frames")
	complete := indexOfType(frames, "catch_up_complete")
	require.Greater(t, complete, 0)
	require.Equal(t, "incremental", frames[complete].Mode)
	requireContiguousSeqs(t, frames, int64(start))
	var sf generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(frames[0].raw, &sf))
	require.NotNil(t, sf.HistoryState, "even incremental current-state readiness is explicit")
}

// Make a real old-data session with canonical bytes/identity but NO derived
// index. Copy only these already-read canonical format files from a closed donor;
// never guess/delete a new implementation's index artifact name. A product
// creator is not used on the target, since fresh creation must initialize indexes.
func pagingUnindexedSession(t *testing.T, target *session.UnifiedStore) string {
	t.Helper()
	donor, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := donor.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	pagingSeed(t, donor, meta.ID, "old", 2)
	require.NoError(t, donor.Close(), "complete all canonical stats writes before copying")
	from := filepath.Join(donor.BaseDir(), meta.ID)
	to := filepath.Join(target.BaseDir(), meta.ID)
	require.NoError(t, os.MkdirAll(to, 0o700))
	for _, name := range []string{"meta.json", "stats.json", "transcript.jsonl", "transcript.day"} {
		b, err := os.ReadFile(filepath.Join(from, name))
		require.NoError(t, err, "donor canonical fixture %s", name)
		require.NoError(t, os.WriteFile(filepath.Join(to, name), b, 0o600))
	}
	_, err = target.GetMeta(meta.ID)
	require.NoError(t, err, "existing identity is addressable despite absent derived index")
	return meta.ID
}

const pagingUnavailable = "Session history is unavailable. Retry after the operator restores its index."

func TestUnavailableHistory_CanonicalAppendAndLiveOnlyControls(t *testing.T) {
	f := newAttachFixture(t, "history-unavailable", "live-answer")
	sid := pagingUnindexedSession(t, f.store)
	api := pagingAPIForAttach(f)
	before := pagingCanonicalBytes(t, f.store, sid)
	pagingRefusal(t, pagingRequest(api, sid, ""), 500, pagingUnavailable)
	d := pagingDetail(t, api, sid)
	require.Equal(t, "unavailable", d.HistoryState.Status)
	require.NotNil(t, d.HistoryState.Reason)
	require.Equal(t, "index_missing", *d.HistoryState.Reason)
	require.NotNil(t, d.HistoryState.Message)
	require.Equal(t, pagingUnavailable, *d.HistoryState.Message)
	require.Nil(t, d.Messages, "metadata-only unavailable detail is not messages:[]")
	require.Nil(t, d.PageInfo)
	require.Nil(t, d.HasEarlier)
	require.Nil(t, d.HasLater)
	require.Nil(t, d.NextBefore)
	require.Nil(t, d.NextAfter)
	require.NoError(t, f.store.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "new-send", Role: "user", Content: "saved once", ClientMessageID: "client-new", AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 1, 0, time.UTC)}), "D1/D7a derived failure must not misreport a durable canonical save")
	after := pagingCanonicalBytes(t, f.store, sid)
	for name, old := range before {
		require.GreaterOrEqual(t, len(after[name]), len(old))
		require.Equal(t, old, after[name][:len(old)], "old accepted bytes never rewritten")
	}
	pagingRefusal(t, pagingRequest(api, sid, ""), 500, pagingUnavailable)
	wc := f.newConn("history-unavailable-view")
	f.h.handleAttachSession(context.Background(), "history-unavailable-view", sid, nil, wc)
	frames := pagingDrain(t, wc)
	require.Equal(t, -1, indexOfType(frames, "session_snapshot"), "unavailable history cannot wipe cached rows")
	require.Equal(t, -1, indexOfType(frames, "done"), "history failure is not turn/replay completion")
	require.Equal(t, -1, indexOfType(frames, "error"), "typed history_state is not a turn-ending error")
	si := indexOfType(frames, "session_state")
	require.Equal(t, 0, si)
	var state generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(frames[si].raw, &state))
	require.NotNil(t, state.HistoryState)
	require.Equal(t, d.HistoryState, *state.HistoryState)
	ci := indexOfType(frames, "catch_up_complete")
	require.Greater(t, ci, si)
	require.Equal(t, "live_only", frames[ci].Mode, "only completed current/live controls, never history proof")
	require.True(t, f.h.connBoundToSession(wc, sid), "keep authorized live binding")
	// Explicit Retry attempts same missing baseline once, no seed or new send.
	f.h.handleAttachSession(context.Background(), "history-unavailable-view", sid, nil, wc)
	retry := pagingDrain(t, wc)
	ri := indexOfType(retry, "catch_up_complete")
	require.GreaterOrEqual(t, ri, 0)
	require.Equal(t, "live_only", retry[ri].Mode)
	require.Equal(t, after, pagingCanonicalBytes(t, f.store, sid), "history Retry cannot resend the accepted input")
}

func TestUnavailableHistory_IncompleteControlsNeverComplete(t *testing.T) {
	f := newAttachFixture(t, "history-control-abort", "live-answer")
	sid := pagingUnindexedSession(t, f.store)
	wc := f.newConn("history-control-abort-view")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.h.handleAttachSession(ctx, "history-control-abort-view", sid, nil, wc)
	frames := pagingDrain(t, wc)
	require.Equal(t, -1, indexOfType(frames, "catch_up_complete"), "neither snapshot nor live_only may acknowledge undelivered controls")
	require.False(t, f.h.connBoundToSession(wc, sid), "failed-control attach stays truthful, unlike successful live_only")
}

func TestUnavailableHistory_ActualCurrentInteractionsAndEngineStop(t *testing.T) {
	t.Fatal("BLOCKED: unavailable-history running-engine fixture/current approval-question-secret/Stop observation seam not implemented — required by D7a/T31/B42/B43/B49/D-N2; real engine must observe Stop after live_only, saved-send receipt once, no attention seen/restart sweep/history success; REST/hub assertions alone are not engine/UAT reachability proof")
}
