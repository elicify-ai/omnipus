// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for the replay gate (spec C3 "ReplayMessageFrame carries a
// thinking entry through replay", gate Boundary 2, section 8.1 row for
// pkg/gateway/websocket_replay.go: "Gated - thinking rows replay only to a
// toggle-on principal", section 16 items 9/22, FR-002).
// The pinned production symbol is
// pkg/gateway/replay.go::streamReplay (gains the requesting login's gate
// result as a trailing showThinking bool - dispatch-pinned signature).
//
// Oracles:
//   - gate OFF: a stored thinking entry emits NO ReplayThinkingFrame and
//     leaves zero thinking bytes in any emitted frame (the entry's absence
//     is the gate at replay); every other frame is unchanged.
//   - gate ON: the thinking entry replays as a ReplayThinkingFrame carrying
//     entry_id/thinking_text/turn_id from the stored row - the REDACTED copy.
//
// RED status: compile-fail on the trailing showThinking parameter and on
// the new TranscriptEntry thinking fields.

func wpcReplayEntries() []session.TranscriptEntry {
	now := time.Now().UTC()
	return []session.TranscriptEntry{
		{Role: "user", Content: "probe the replay gate", Timestamp: now},
		{
			ID: "wpc-think-1", Type: session.EntryTypeThinking,
			ThinkingText:    "replay probe " + thinkingSentinelGateway + " tail",
			TurnID:          "turn-1",
			ElapsedMS:       1500,
			ThinkingTokens:  42,
			ProviderSummary: true,
			Timestamp:       now,
		},
		{ID: "wpc-ans-1", Role: "assistant", Content: "the answer", TurnID: "turn-1", Timestamp: now},
	}
}

func TestStreamReplay_GateOff_ThinkingEntryEmitsNothing(t *testing.T) {
	sink := &sliceSink{}
	entries := wpcReplayEntries()
	_, err := streamReplay(context.Background(), "wpc-replay-sess", entries,
		computeReplayStats(entries), sink.emit, nil, nil, nil, nil, false)
	require.NoError(t, err)

	frames := sink.all()
	types := frameTypes(frames)
	assert.NotContains(t, types, "replay_thinking",
		"gate off: no ReplayThinkingFrame is emitted (the entry's absence is the gate at replay)")
	for i, raw := range sink.frames {
		assert.NotContains(t, string(raw), thinkingSentinelGateway,
			"frame %d: zero thinking bytes reach a gate-off replay (FR-002)", i)
	}
	// The other frames are unchanged: the answer replays, replay ends with
	// exactly one done frame.
	assert.Contains(t, types, "replay_message", "the answer still replays with the gate off")
	assert.Equal(t, "done", types[len(types)-1], "replay still ends with exactly one done frame (FR-I-004)")
}

func TestStreamReplay_GateOn_ThinkingEntryReplays(t *testing.T) {
	sink := &sliceSink{}
	entries := wpcReplayEntries()
	_, err := streamReplay(context.Background(), "wpc-replay-sess", entries,
		computeReplayStats(entries), sink.emit, nil, nil, nil, nil, true)
	require.NoError(t, err)

	frames := sink.all()
	think := findFrame(frames, "replay_thinking")
	require.NotNil(t, think, "gate on: the stored thinking row replays as a ReplayThinkingFrame; got %v", frameTypes(frames))
	assert.Equal(t, "replay_thinking", think.Type)

	var raw string
	for _, b := range sink.frames {
		if strings.Contains(string(b), `"entry_id":"wpc-think-1"`) {
			raw = string(b)
		}
	}
	require.NotEmpty(t, raw, "the replay_thinking frame carries the stored row's entry id")
	assert.Contains(t, raw, "replay probe", "the redacted display text replays")
	assert.NotContains(t, raw, thinkingSentinelGateway,
		"the replayed text is the REDACTED copy - never raw reasoning")
}
