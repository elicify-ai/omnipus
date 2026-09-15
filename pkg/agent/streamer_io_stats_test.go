// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
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
