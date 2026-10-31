// websocket_streamer_cancel_parity_test.go: #1534 RED — the saved stopped
// answer must equal the body the browser showed at Stop.
//
// Founder decision 2026-10-31 (Discord #team-b, "it should work the same way
// as Claude code" = option A): when the user presses Stop, the saved answer
// stops where the screen stopped. After any reload the stopped answer body
// equals the body the browser showed at Stop, with the same "interrupted"
// label as today. Accepted residual: tokens published between the browser
// freeze and the server claiming the Stop (frame transit) may still be saved;
// nothing delivered AFTER the server claimed the Stop may be saved.
//
// The production cut the fix installs (diagnosis Q6 option 1):
// pkg/agent/cancel_stop.go::claimCancel(stopOnly=true) type-asserts an
// optional capability on the turn's lastStreamer —
// `interface{ CutoffAtStop() }`, the file's established optional-interface
// pattern — and calls it once; wsStreamer records the cutoff length and
// persists only the pre-cut prefix. pkg/gateway cannot call the unexported
// claimCancel, so these tests perform the cut through the SAME type assertion
// the production path uses: the file compiles before the capability exists
// and fails with a named message until wsStreamer implements it.
package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claimStopCutoffForTest performs the Stop cutoff exactly the way the #1534
// fix's production path will: the same optional-capability type assertion
// pkg/agent/cancel_stop.go::claimCancel(stopOnly=true) uses on the turn's
// lastStreamer. Reports whether the capability existed; a missing capability
// is this file's named RED, never a skip.
func claimStopCutoffForTest(t *testing.T, streamer interface {
	Update(ctx context.Context, content string) error
	Finalize(ctx context.Context, content string) error
}) bool {
	t.Helper()
	cut, ok := streamer.(interface{ CutoffAtStop() })
	if !ok {
		t.Errorf("streamer does not implement CutoffAtStop — required by #1534: " +
			"pkg/agent/cancel_stop.go::claimCancel(stopOnly=true) must cut the turn's lastStreamer " +
			"off at the claimed Stop (diagnosis Q6 option 1); until then every post-Stop delta is saved")
		return false
	}
	cut.CutoffAtStop()
	return true
}

// newStopParityStreamer builds the #1534 fixture: a real handler-backed
// wsStreamer for a real, store-backed session (so Finalize persists to a
// transcript a reload would read), stamped with the turn/message ids the
// agent loop stamps in production (#823: the persisted entry must carry the
// SAME id its live frames carried, so a reload replaces the same bubble).
func newStopParityStreamer(t *testing.T, chatID, turnID, messageID string) (*wsStreamer, *session.UnifiedStore, string) {
	t.Helper()
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "create session")

	st, ok := handler.GetStreamer(context.Background(), "webchat", chatID, meta.ID)
	require.True(t, ok, "GetStreamer must return a streamer for a webchat session")
	streamer, ok := st.(*wsStreamer)
	require.True(t, ok, "GetStreamer for webchat must return a *wsStreamer")
	streamer.SetTurnID(turnID)
	streamer.SetMessageID(messageID)
	return streamer, store, meta.ID
}

// stopParityAssistantEntries reads the transcript a reload would read and
// returns its assistant entries.
func stopParityAssistantEntries(t *testing.T, store *session.UnifiedStore, sid string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err, "read transcript")
	var assistant []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistant = append(assistant, e)
		}
	}
	return assistant
}

// TestWsStreamer_StopCutoff_SavedBodyEqualsBodyShownAtStop pins the founder's
// #1534 decision at the persistence boundary: text the browser discarded
// because it arrived after the Stop was claimed must not be saved. RED today:
// wsStreamer.Update appends every delta unconditionally and persistTranscript
// writes the full accumulated buffer, so the entry content is
// "BEFOREAFTER-STOP" where the browser showed "BEFORE".
//
// BDD:
//
//	Given a streaming assistant answer whose live bubble shows "BEFORE",
//	When the server claims the Stop (the production cutoff seam) and further
//	  deltas ("AFTER-STOP") are delivered anyway,
//	Then the persisted assistant entry's content is exactly "BEFORE" — what
//	  the browser showed at Stop — and the post-Stop token was still published
//	  through the session hub (frames unchanged; the browser discards it).
func TestWsStreamer_StopCutoff_SavedBodyEqualsBodyShownAtStop(t *testing.T) {
	streamer, store, sid := newStopParityStreamer(t, "chat-1534-stop", "turn-1534-stop", "msg-1534-stop")
	ctx := context.Background()

	// The tokens the browser accepted and showed before the user pressed Stop.
	require.NoError(t, streamer.Update(ctx, "BEFORE"))

	// The Stop is claimed through the production cancel path's cutoff seam.
	hasCut := claimStopCutoffForTest(t, streamer)

	// Tokens still in flight when the server claimed the Stop: the browser
	// discards them (its interrupted-status guards), and the frames are
	// unchanged — Update must keep publishing them.
	require.NoError(t, streamer.Update(ctx, "AFTER-STOP"))

	// Frames are unchanged: the post-Stop token was still published through
	// the session hub the bound browser reads (and discards). Read the
	// active-turn projection BEFORE Finalize — the done frame legitimately
	// clears it (BE-DESIGN.md §4.4).
	hub := streamer.wsHandler().hubs.lookup(sid)
	require.NotNil(t, hub, "the first token must create the session's hub")
	hub.mu.Lock()
	items := hub.proj.snapshot()
	hub.mu.Unlock()
	require.Len(t, items, 1)
	assert.Equal(t, "BEFOREAFTER-STOP", items[0].text,
		"frames are unchanged by the fix: post-Stop tokens are still published live; the browser discards them")
	assert.Equal(t, "msg-1534-stop", items[0].messageID)

	require.NoError(t, streamer.Finalize(ctx, ""))

	assistant := stopParityAssistantEntries(t, store, sid)
	require.Len(t, assistant, 1, "Finalize must write exactly one assistant entry")
	// The load-bearing #1534 assertion: the saved body is what the screen
	// showed at Stop, not what the server additionally received.
	assert.Equal(t, "BEFORE", assistant[0].Content,
		"the saved stopped answer must equal the body the browser showed at Stop (#1534); got %q", assistant[0].Content)
	// Label parity preconditions (unchanged by the fix): the entry is the same
	// bubble its live frames numbered (#823) and carries the turn id the
	// turn_canceled record correlates with to render "interrupted".
	assert.Equal(t, "msg-1534-stop", assistant[0].ID,
		"the persisted entry must keep the live frames' message id so a reload replaces the same bubble")
	assert.Equal(t, "turn-1534-stop", assistant[0].TurnID,
		"the persisted entry must keep the turn id so the turn_canceled record still labels it interrupted")
	assert.True(t, hasCut, "the cutoff seam must exist for the saved-body parity above to hold")
}

// TestWsStreamer_NoStop_SavesFullStreamedBody is the control that proves the
// fixture: with no Stop and no cut, Finalize persists the whole streamed
// body. It must pass today, unchanged by the fix.
func TestWsStreamer_NoStop_SavesFullStreamedBody(t *testing.T) {
	streamer, store, sid := newStopParityStreamer(t, "chat-1534-control", "turn-1534-control", "msg-1534-control")
	ctx := context.Background()

	require.NoError(t, streamer.Update(ctx, "A"))
	require.NoError(t, streamer.Update(ctx, "B"))
	require.NoError(t, streamer.Finalize(ctx, ""))

	assistant := stopParityAssistantEntries(t, store, sid)
	require.Len(t, assistant, 1, "Finalize must write exactly one assistant entry")
	assert.Equal(t, "AB", assistant[0].Content,
		"with no Stop, the saved body must be the full streamed text — the fixture this RED pair stands on")
	assert.Equal(t, "msg-1534-control", assistant[0].ID)
}

// TestWsStreamer_CutoffAtStop_ConcurrentUpdate_SavesOnlyPreCutBody is the
// write-during-race case: Updates arriving concurrently with the cut (the
// real Stop window — the provider keeps delivering while claimCancel runs)
// and one delivered strictly after the cut has fired. Run with -race. The
// founder-accepted residual allows pre-claim tokens to be saved; nothing
// delivered after the claimed Stop may be.
func TestWsStreamer_CutoffAtStop_ConcurrentUpdate_SavesOnlyPreCutBody(t *testing.T) {
	streamer, store, sid := newStopParityStreamer(t, "chat-1534-race", "turn-1534-race", "msg-1534-race")
	ctx := context.Background()

	require.NoError(t, streamer.Update(ctx, "HEAD"))

	// The same optional-capability assertion the production claim performs on
	// the turn's bus.Streamer; recorded (not skipped) when missing — that
	// Errorf is this test's RED.
	var streamerBus bus.Streamer = streamer
	cut, hasCut := streamerBus.(interface{ CutoffAtStop() })
	if !hasCut {
		t.Errorf("streamer does not implement CutoffAtStop — required by #1534: " +
			"pkg/agent/cancel_stop.go::claimCancel(stopOnly=true) must cut the turn's lastStreamer " +
			"off at the claimed Stop (diagnosis Q6 option 1); until then every post-Stop delta is saved")
	}

	// The cut fires concurrently with in-flight deliveries.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if hasCut {
			cut.CutoffAtStop()
		}
	}()
	deltas := []string{"r1", "r2", "r3", "r4", "r5"}
	for _, d := range deltas {
		require.NoError(t, streamer.Update(ctx, d))
	}
	wg.Wait()

	// The cut has now definitely fired. "TAIL" is delivered strictly after
	// the claimed Stop, so it must never be saved — whatever the race above
	// let through is the accepted residual.
	require.NoError(t, streamer.Update(ctx, "TAIL"))

	full := "HEAD" + strings.Join(deltas, "") + "TAIL"

	// Frames are unchanged: every delivery — "TAIL" included — was still
	// published live through the session hub. Read BEFORE Finalize, whose
	// done frame legitimately clears the projection.
	hub := streamer.wsHandler().hubs.lookup(sid)
	require.NotNil(t, hub, "the first token must create the session's hub")
	hub.mu.Lock()
	items := hub.proj.snapshot()
	hub.mu.Unlock()
	require.Len(t, items, 1)
	assert.Equal(t, full, items[0].text,
		"frames are unchanged by the fix: every delivery — post-Stop included — is still published live")

	require.NoError(t, streamer.Finalize(ctx, ""))

	assistant := stopParityAssistantEntries(t, store, sid)
	require.Len(t, assistant, 1, "Finalize must write exactly one assistant entry")
	saved := assistant[0].Content
	if hasCut {
		assert.True(t, strings.HasPrefix(full, saved),
			"the saved body must be a prefix of the delivered stream (the cut freezes, never reorders): saved=%q full=%q", saved, full)
		assert.NotContains(t, saved, "TAIL",
			"nothing delivered after the server claimed the Stop may be saved (#1534)")
	} else {
		// Characterization (no cutoff seam exists yet): today the full stream
		// is saved — exactly the #1534 defect. This branch disappears once the
		// capability exists; the Errorf above is this test's RED.
		assert.Equal(t, full, saved,
			"characterization test — with no CutoffAtStop seam the whole stream is saved; this equality is the defect, not the expectation")
	}
}
