// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_catchup_test.go — ADR-082 D3/FR-007/S-06: catch-up ordering under
// concurrency. When a connection binds to a session with an in-flight turn,
// it must receive, after replay history and before any live frame, exactly
// one catch-up token whose content is the turn's accumulated-so-far text —
// with every later live token continuing from that point, no duplicate, no
// gap — even when many connections bind at random moments while tokens are
// still streaming.

package gateway

import (
	"context"
	"encoding/json"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestAttach_CatchUpSnapshotOrdering is the T-06 regression test: tokens
// arrive at 1ms intervals; 20 connections bind at random moments during the
// stream; each connection's own catch-up-token + live-token stream must
// reassemble to EXACTLY the full text — no duplicate, no gap. Run with
// -race: this is the load-bearing proof that the bind (handleAttachSession)
// and the append+resolve (wsStreamer.Update) sharing one h.mu critical
// section actually produces the ordering guarantee ADR-082 D3 promises.
func TestAttach_CatchUpSnapshotOrdering(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	// Register the in-flight streamer exactly as the agent loop does at the
	// start of a streaming LLM round.
	streamerAny, ok := handler.GetStreamer(context.Background(), "webchat", "chat-origin", meta.ID)
	require.True(t, ok)
	streamer, ok := streamerAny.(*wsStreamer)
	require.True(t, ok, "GetStreamer for webchat must return a *wsStreamer")

	const numTokens = 50
	tokens := make([]string, numTokens)
	var fullTextBuilder strings.Builder
	for i := 0; i < numTokens; i++ {
		tok := "t" + strconv.Itoa(i) + "-"
		tokens[i] = tok
		fullTextBuilder.WriteString(tok)
	}
	fullText := fullTextBuilder.String()

	const numConns = 20
	results := make([]string, numConns)
	var attachWg sync.WaitGroup  // signals "this connection has bound"
	var collectWg sync.WaitGroup // signals "this connection has collected everything through done"
	attachWg.Add(numConns)
	collectWg.Add(numConns)

	for i := 0; i < numConns; i++ {
		go func() {
			defer collectWg.Done()

			// Bind at a random moment strictly within the streaming window
			// (0..(numTokens-2)ms) so every connection is guaranteed bound
			// before Finalize runs (joined via attachWg below). Test-only
			// jitter, not a security-sensitive random — math/rand is fine.
			delay := time.Duration(rand.Intn(numTokens-1)) * time.Millisecond
			time.Sleep(delay)

			wc := &wsConn{
				sendCh: make(chan []byte, replayLiveBufferCap+numTokens+8),
				doneCh: make(chan struct{}),
			}
			chatID := "chat-conn-" + strconv.Itoa(i)
			// handleAttachSession only binds h.sessionIDs (chatID→sessionID);
			// h.sessions (chatID→connection) is normally populated once at
			// connection-open time (ServeHTTP), which this test bypasses —
			// register it directly so resolveSessionConnsLocked can find wc.
			handler.mu.Lock()
			handler.sessions[chatID] = wc
			handler.mu.Unlock()
			handler.handleAttachSession(context.Background(), chatID, meta.ID, nil, wc)
			// Signal "bound" only AFTER handleAttachSession returns — the
			// caller (main goroutine) waits on this before calling Finalize,
			// so the done frame Finalize sends is guaranteed to reach every
			// connection's sendCh; the collect loop below then picks it up.
			attachWg.Done()

			// Collect every token frame's content until the TURN's own done
			// (not a deadline). Note: streamReplay itself always emits its
			// own "done" frame marking replay completion (present even for
			// a 0-entry transcript, and unconditionally the FIRST frame in
			// wc.sendCh here since it is written directly by streamReplay
			// before this connection's catch-up token or any live token can
			// reach it) — that is a DIFFERENT signal from the streaming
			// turn's own completion and must not be mistaken for it. The
			// turn's own done (wsStreamer.Finalize) always carries a
			// non-nil stats.tokens; the replay's own done never does — used
			// here as the distinguishing marker.
			var got strings.Builder
			deadline := time.After(5 * time.Second)
		collect:
			for {
				select {
				case raw := <-wc.sendCh:
					var f struct {
						Type    string `json:"type"`
						Content string `json:"content"`
						Stats   *struct {
							Tokens *float64 `json:"tokens"`
						} `json:"stats"`
					}
					if json.Unmarshal(raw, &f) != nil {
						continue
					}
					switch f.Type {
					case "token":
						got.WriteString(f.Content)
					case "done":
						if f.Stats != nil && f.Stats.Tokens != nil {
							break collect
						}
						// else: the replay's own done — keep collecting.
					}
				case <-deadline:
					break collect
				}
			}
			results[i] = got.String()
		}()
	}

	// Stream all tokens at 1ms intervals, concurrently with the 20 binders above.
	for _, tok := range tokens {
		require.NoError(t, streamer.Update(context.Background(), tok))
		time.Sleep(time.Millisecond)
	}

	// Wait for every connection to have bound (and completed its own
	// replay+catch-up+divert-drain) before finalizing — guarantees every
	// connection is a target of the done frame and has had the chance to
	// receive every live token that postdates its own bind.
	attachWg.Wait()

	require.NoError(t, streamer.Finalize(context.Background(), fullText))

	// Wait for every connection's collect loop to observe its done frame (or
	// time out) before inspecting results.
	collectWg.Wait()

	for i, got := range results {
		assert.Equal(t, fullText, got,
			"connection %d must reassemble catch-up+live tokens to exactly the full text, "+
				"no duplicate, no gap", i)
	}
}

// TestFix_CR5_F1_NoCatchUpForAlreadyPersistedRoundText proves the ADR-082
// review CR5/F1 fix: liveStreamers is per LLM ROUND, not per turn — a
// round whose narration was already written to the transcript via
// appendIntermediateAssistantTranscript (Bug #416) BEFORE its tool calls run
// stays registered as the session's live streamer for the entire tool-call
// window that follows (GetStreamer only overwrites the entry on the NEXT
// round). Before this fix, a connection binding during that window received
// the round's text TWICE: once from replay (already on disk) and again as a
// "catch-up" token — a duplicate bubble.
func TestFix_CR5_F1_NoCatchUpForAlreadyPersistedRoundText(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	// Round 1: stream narration, then simulate the agent loop's Bug #416
	// persistence path — appendIntermediateAssistantTranscript writes the
	// entry BEFORE this round's tool calls run, and
	// markLastStreamerTranscriptPersisted marks the streamer via
	// SuppressTranscriptWrite. This is the exact state a connection sees
	// while round 1's tool calls are executing: the streamer is still
	// registered (GetStreamer for round 2 has not run yet).
	round1Any, ok := handler.GetStreamer(context.Background(), "webchat", "chat-cr5", meta.ID)
	require.True(t, ok)
	round1, ok := round1Any.(*wsStreamer)
	require.True(t, ok)
	round1.SetTurnID("turn-cr5")
	require.NoError(t, round1.Update(context.Background(), "round one narration"))

	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID:        "entry-round1-cr5",
		Role:      "assistant",
		AgentID:   "mia",
		TurnID:    "turn-cr5",
		Content:   "round one narration",
		Timestamp: time.Now().UTC(),
	}))
	round1.SuppressTranscriptWrite()

	// Bind DURING the tool-call window: NO catch-up token; replay alone
	// carries round 1's text.
	wcDuringTools := &wsConn{sendCh: make(chan []byte, 16), doneCh: make(chan struct{})}
	handler.mu.Lock()
	handler.sessions["chat-cr5-attach1"] = wcDuringTools
	handler.mu.Unlock()
	handler.handleAttachSession(context.Background(), "chat-cr5-attach1", meta.ID, nil, wcDuringTools)

	var sawReplayText bool
	deadline := time.After(2 * time.Second)
collectTools:
	for {
		select {
		case raw := <-wcDuringTools.sendCh:
			var f struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			}
			require.NoError(t, json.Unmarshal(raw, &f))
			if f.Type == "token" {
				t.Fatalf("BUG REGRESSION: catch-up token delivered for text already persisted "+
					"to the transcript: %q", f.Content)
			}
			if f.Type == "replay_message" && f.Content == "round one narration" {
				sawReplayText = true
			}
			if f.Type == "done" {
				break collectTools
			}
		case <-deadline:
			t.Fatal("timed out waiting for replay to finish during the tool-call window")
		}
	}
	assert.True(t, sawReplayText, "replay must carry round 1's already-persisted text")

	// Round 2: GetStreamer overwrites liveStreamers[sid] with a fresh
	// streamer (transcriptPersisted=false). Binding DURING round 2's own
	// streaming must carry ONLY round 2's partial text as catch-up.
	round2Any, ok := handler.GetStreamer(context.Background(), "webchat", "chat-cr5", meta.ID)
	require.True(t, ok)
	round2, ok := round2Any.(*wsStreamer)
	require.True(t, ok)
	round2.SetTurnID("turn-cr5-round2")
	require.NoError(t, round2.Update(context.Background(), "round two partial"))

	wcDuringRound2 := &wsConn{sendCh: make(chan []byte, 16), doneCh: make(chan struct{})}
	handler.mu.Lock()
	handler.sessions["chat-cr5-attach2"] = wcDuringRound2
	handler.mu.Unlock()
	handler.handleAttachSession(context.Background(), "chat-cr5-attach2", meta.ID, nil, wcDuringRound2)

	// round2 is never Finalize()d in this test (it is still "streaming" at
	// the moment attach2 binds), so the ONLY "done" frame in this stream is
	// the REPLAY's own terminating done — which, per the CR1 wire order,
	// arrives BEFORE the catch-up token (replay* → done{frames_emitted} →
	// token{catch-up}). Breaking on the first "done" would therefore stop
	// collection before the catch-up token even arrives; drain for the full
	// window instead.
	var catchUpTexts []string
	deadline2 := time.After(1 * time.Second)
collectRound2:
	for {
		select {
		case raw := <-wcDuringRound2.sendCh:
			var f struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			}
			require.NoError(t, json.Unmarshal(raw, &f))
			if f.Type == "token" {
				catchUpTexts = append(catchUpTexts, f.Content)
			}
		case <-deadline2:
			break collectRound2
		}
	}
	require.Len(t, catchUpTexts, 1, "exactly one catch-up token, for round 2's own partial text only")
	assert.Equal(t, "round two partial", catchUpTexts[0])
}

// TestFix_CR6_DoneDivertedWhileConnectionIsReplaying proves the ADR-082
// review CR6 fix: sendRawFrameBytes must not let a TURN's "done" frame
// bypass the replay divert — a turn finalizing while a re-attaching
// connection is still mid-replay must not put "done" ahead of that
// connection's still-pending replay/catch-up frames (which would orphan the
// bubble on the client and leave Stop looking stuck).
func TestFix_CR6_DoneDivertedWhileConnectionIsReplaying(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	// Seed one transcript entry so streamReplay actually emits at least one
	// replay_message frame — the controllable blocking point below.
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID: "seed-entry-cr6", Role: "user", Content: "hello", Timestamp: time.Now().UTC(),
	}))

	// Register an in-flight streamer for a turn on the SAME session that
	// will race its own Finalize against this attach's replay.
	streamerAny, ok := handler.GetStreamer(context.Background(), "webchat", "chat-cr6-origin", meta.ID)
	require.True(t, ok)
	streamer, ok := streamerAny.(*wsStreamer)
	require.True(t, ok)
	streamer.SetTurnID("turn-cr6")

	wc := &wsConn{
		// Capacity 1: the FIRST write (session_state, non-blocking, sent
		// while still holding h.mu) fills it; streamReplay's own first
		// content frame (the seeded entry) then BLOCKS until drained,
		// giving Finalize below a guaranteed window to race in while
		// wc.isReplayingLive is still true.
		sendCh: make(chan []byte, 1),
		doneCh: make(chan struct{}),
	}
	chatID := "chat-cr6-attach"
	handler.mu.Lock()
	handler.sessions[chatID] = wc
	handler.mu.Unlock()

	attachDone := make(chan struct{})
	go func() {
		defer close(attachDone)
		handler.handleAttachSession(context.Background(), chatID, meta.ID, nil, wc)
	}()

	// Let handleAttachSession bind + arm the divert + block on the seeded
	// entry's replay_message frame.
	time.Sleep(50 * time.Millisecond)

	finalizeDone := make(chan struct{})
	go func() {
		defer close(finalizeDone)
		require.NoError(t, streamer.Finalize(context.Background(), ""))
	}()
	// Give Finalize a moment to actually reach sendRawFrameBytes before we
	// start draining — otherwise the race isn't guaranteed to be exercised.
	time.Sleep(50 * time.Millisecond)

	var types []string
	turnDoneSeen := false
	deadline := time.After(5 * time.Second)
	for !turnDoneSeen {
		select {
		case raw := <-wc.sendCh:
			var f struct {
				Type  string `json:"type"`
				Stats *struct {
					Tokens *float64 `json:"tokens"`
				} `json:"stats"`
			}
			require.NoError(t, json.Unmarshal(raw, &f))
			types = append(types, f.Type)
			if f.Type == "done" && f.Stats != nil && f.Stats.Tokens != nil {
				turnDoneSeen = true
			}
		case <-deadline:
			t.Fatalf("timed out; frames so far: %v", types)
		}
	}

	<-attachDone
	<-finalizeDone

	require.NotEmpty(t, types)
	assert.Equal(t, "done", types[len(types)-1],
		"BUG REGRESSION: the turn's own done must never jump ahead of a still-replaying "+
			"connection's queued frames — it must arrive last")
	doneCount := 0
	for _, ty := range types {
		if ty == "done" {
			doneCount++
		}
	}
	assert.GreaterOrEqual(t, doneCount, 2,
		"expect both the replay's own terminating done and the turn's own final done")
}

// TestFix_CR10_ReplayErrorStillEmitsSessionStateAndDrainsDivert proves the
// ADR-082 review CR10 fix: on a replay error, handleAttachSession must not
// leave the connection bound with an undrained wc.replayDivertCh (which is
// allocated once and reused across every later attach on the same
// connection — an undrained buffer here would leak into the NEXT
// attach_session as stale duplicates), and session_state must already have
// been delivered (CR1's early emit, ahead of replay).
func TestFix_CR10_ReplayErrorStillEmitsSessionStateAndDrainsDivert(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	wc := &wsConn{sendCh: make(chan []byte, 32), doneCh: make(chan struct{})}
	chatID := "chat-cr10"
	handler.mu.Lock()
	handler.sessions[chatID] = wc
	handler.mu.Unlock()

	// A pre-canceled context forces streamReplay's own ctx.Err() check
	// (reached even for a zero-entry transcript, right before its final
	// done emit) to fail — deterministic, no timing dependency.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handler.handleAttachSession(ctx, chatID, meta.ID, nil, wc)

	var sawSessionState, sawError, sawReplayErrorDone bool
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case raw := <-wc.sendCh:
			var f struct {
				Type      string  `json:"type"`
				SessionID *string `json:"session_id"`
				Stats     *struct {
					ReplayError *bool `json:"replay_error"`
				} `json:"stats"`
			}
			require.NoError(t, json.Unmarshal(raw, &f))
			switch f.Type {
			case "session_state":
				sawSessionState = true
				require.NotNil(t, f.SessionID)
				assert.Equal(t, meta.ID, *f.SessionID)
			case "error":
				sawError = true
			case "done":
				if f.Stats != nil && f.Stats.ReplayError != nil && *f.Stats.ReplayError {
					sawReplayErrorDone = true
					break collect
				}
			}
		case <-deadline:
			t.Fatalf("timed out; session_state=%v error=%v replayErrorDone=%v",
				sawSessionState, sawError, sawReplayErrorDone)
		}
	}

	assert.True(t, sawSessionState, "session_state must still be delivered even though replay aborted")
	assert.True(t, sawError, "an error frame must be delivered on replay failure")
	assert.True(t, sawReplayErrorDone, "a done{replay_error:true} frame must follow")

	// The divert must have been disarmed AND drained — nothing left over to
	// leak into a later attach on this same connection.
	assert.False(t, wc.isReplayingLive.Load(), "isReplayingLive must be disarmed after a replay error")
	select {
	case leftover := <-wc.replayDivertCh:
		t.Fatalf("BUG REGRESSION: replayDivertCh must be drained after a replay error, found: %s", string(leftover))
	default:
		// expected — empty
	}
}
