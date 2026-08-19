// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"
	"time"
)

// fakeIOStreamer records what finalizeStreamer pushes into a streamer.
// It implements Streamer plus both optional stats interfaces.
type fakeIOStreamer struct {
	gotTokens     int64
	gotPrompt     int
	gotCompletion int
	gotCacheRead  int
	gotCacheWrite int
	finalized     bool
}

func (f *fakeIOStreamer) Update(context.Context, string) error { return nil }

func (f *fakeIOStreamer) Finalize(context.Context, string) error {
	f.finalized = true
	return nil
}

func (f *fakeIOStreamer) Cancel(context.Context) {}

func (f *fakeIOStreamer) SetTurnStats(t int64, _ float64, _ time.Duration) {
	f.gotTokens = t
}

func (f *fakeIOStreamer) SetTurnIOStats(prompt, completion, cacheRead, cacheWrite int) {
	f.gotPrompt = prompt
	f.gotCompletion = completion
	f.gotCacheRead = cacheRead
	f.gotCacheWrite = cacheWrite
}

// TestFinalizeStreamer_PushesTokenSplitToStreamer closes the seam that made the
// token-accounting fix inert for the surface that matters most.
//
// The split was threaded through turnState -> TranscriptEntry -> session stats
// and covered by tests at each end. But a STREAMED turn — every ordinary
// webchat turn — does not go through appendAssistantTranscript at all; the
// streamer builds its own TranscriptEntry in Finalize. finalizeStreamer pushed
// only the collapsed total, so the streamer had no split to stamp and every
// webchat session kept reporting tokens_in: 0.
//
// Every other test in this change stayed green throughout that. This one fails
// if the streamerIOStatsSetter call is removed from finalizeStreamer.
func TestFinalizeStreamer_PushesTokenSplitToStreamer(t *testing.T) {
	ts := &turnState{startedAt: time.Now()}
	streamer := &fakeIOStreamer{}
	ts.lastStreamer = streamer

	ts.AddTurnStats(1000, 0.5)
	ts.AddTurnIOStats(800, 200)
	ts.AddTurnCacheStats(150, 50)

	ts.finalizeStreamer(context.Background())

	if !streamer.finalized {
		t.Fatal("streamer was never finalized")
	}
	if streamer.gotTokens != 1000 {
		t.Errorf("total tokens = %d, want 1000", streamer.gotTokens)
	}
	if streamer.gotPrompt != 800 {
		t.Errorf("prompt tokens = %d, want 800 — the streamer got no input/output split, "+
			"so its TranscriptEntry will carry none and tokens_in stays 0", streamer.gotPrompt)
	}
	if streamer.gotCompletion != 200 {
		t.Errorf("completion tokens = %d, want 200", streamer.gotCompletion)
	}
	if streamer.gotCacheRead != 150 || streamer.gotCacheWrite != 50 {
		t.Errorf("cache split = %d/%d, want 150/50", streamer.gotCacheRead, streamer.gotCacheWrite)
	}
}
