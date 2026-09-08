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
