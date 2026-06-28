//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// See websocket_test.go for the same rationale.

package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// buildWsStreamer constructs a wsStreamer whose conn's sendCh is captured so
// we can inspect the done frame emitted by Finalize.
func buildWsStreamer(t *testing.T) (*wsStreamer, chan []byte) {
	t.Helper()
	ch := make(chan []byte, 8)

	// We need a real *wsConn because wsStreamer holds *wsConn by pointer and
	// sendConnGenFrame accesses conn.sendCh. Build the smallest valid *wsConn.
	conn := &wsConn{
		sendCh: ch,
		doneCh: make(chan struct{}),
	}

	s := &wsStreamer{
		conn:      conn,
		sessionID: "test-session",
		chatID:    "test-chat",
	}
	return s, ch
}

// readDoneFrame drains bytes from ch and unmarshals them as a DoneFrame.
func readDoneFrame(t *testing.T, ch chan []byte) generated.DoneFrame {
	t.Helper()
	select {
	case raw := <-ch:
		var frame generated.DoneFrame
		require.NoError(t, json.Unmarshal(raw, &frame), "must unmarshal DoneFrame")
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for done frame")
		return generated.DoneFrame{}
	}
}

// TestWsStreamer_TurnFailed_SetInDoneFrame verifies that when SetTurnFailed(true)
// is called before Finalize, the emitted done frame carries DoneStats.TurnFailed=true.
//
// BDD: Given a wsStreamer whose turn ended via the engine's synthetic fallback,
// When SetTurnFailed(true) and Finalize are called,
// Then the done frame contains stats.turn_failed = true.
//
// Traces to: pkg/gateway/websocket.go — wsStreamer.SetTurnFailed / Finalize
func TestWsStreamer_TurnFailed_SetInDoneFrame(t *testing.T) {
	s, ch := buildWsStreamer(t)

	s.SetTurnFailed(true)
	err := s.Finalize(context.Background(), "The model returned an empty response.")
	require.NoError(t, err)

	frame := readDoneFrame(t, ch)
	require.NotNil(t, frame.Stats, "done frame must carry Stats")
	require.NotNil(t, frame.Stats.TurnFailed, "TurnFailed must be set when the turn failed")
	assert.True(t, *frame.Stats.TurnFailed, "TurnFailed must be true")
}

// TestWsStreamer_TurnFailed_AbsentOnNormalTurn verifies that when SetTurnFailed is
// NOT called (or called with false), the emitted done frame omits TurnFailed.
//
// BDD: Given a wsStreamer whose turn ended normally (real model response),
// When Finalize is called WITHOUT calling SetTurnFailed,
// Then the done frame has stats.turn_failed absent (nil).
//
// Traces to: pkg/gateway/websocket.go — wsStreamer.Finalize (TurnFailed omitempty)
func TestWsStreamer_TurnFailed_AbsentOnNormalTurn(t *testing.T) {
	s, ch := buildWsStreamer(t)

	// No SetTurnFailed call — normal successful turn.
	err := s.Finalize(context.Background(), "Here is the answer.")
	require.NoError(t, err)

	frame := readDoneFrame(t, ch)
	require.NotNil(t, frame.Stats, "done frame must carry Stats")
	assert.Nil(t, frame.Stats.TurnFailed, "TurnFailed must be absent (nil) on a normal turn")
}

// TestWsStreamer_TurnFailed_ExplicitFalse verifies that SetTurnFailed(false) still
// omits TurnFailed from the done frame (omitempty: false/nil both absent).
//
// BDD: Given a wsStreamer where SetTurnFailed(false) is called explicitly,
// When Finalize is called,
// Then TurnFailed is nil in the emitted done frame (omitempty suppresses false).
//
// Traces to: pkg/gateway/websocket.go — Finalize: `if turnFailed { … }` guard
func TestWsStreamer_TurnFailed_ExplicitFalse(t *testing.T) {
	s, ch := buildWsStreamer(t)

	s.SetTurnFailed(false)
	err := s.Finalize(context.Background(), "Here is the answer.")
	require.NoError(t, err)

	frame := readDoneFrame(t, ch)
	require.NotNil(t, frame.Stats, "done frame must carry Stats")
	assert.Nil(t, frame.Stats.TurnFailed, "TurnFailed must be absent (nil) when explicitly false")
}
