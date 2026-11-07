package channels

// #1676: T-16b counts full-queue drops by scanning the process-wide log file
// for "Outbound queue full". The logger is a package global, so a drop logged
// by any other Manager in the same test binary while T-16b's file sink is
// active is counted as T-16b's own. Observed: a straggling dispatcher of
// TestRunWorker_NoConfigRaceAcrossReload (channel "mock-race", chat "1")
// wrote its drop into T-16b's file (log:
// /workspace/logs/fix-1675-1676/diag/1676-pair-repro.log).
//
// This test forces that schedule deterministically: one foreign drop on a
// different channel and chat, through the real enqueueOutbound, while the
// T-16b instrument is recording. The T-16b oracle must count only the drops
// of the channel and chat under test, so the expected count is zero.

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/require"
)

func TestT16bInstrument_ForeignManagerDropIsNotCountedAsOwnDrop(t *testing.T) {
	path := t16LogFile(t)

	foreignBus := bus.NewMessageBus()
	t.Cleanup(foreignBus.Close)
	foreign := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      foreignBus,
	}
	w := &channelWorker{queue: make(chan bus.OutboundMessage, 1)}
	w.queue <- bus.OutboundMessage{Channel: "mock-race", ChatID: "1", Content: "filler"}

	cont := foreign.enqueueOutbound(context.Background(), w,
		bus.OutboundMessage{Channel: "mock-race", ChatID: "1", Content: "x"})
	require.True(t, cont, "instrument: a full-queue drop keeps the foreign dispatch loop running")

	// The T-16b subject is channel "discord", chat "222"; nothing was dropped there.
	require.Equal(t, 0, t16QueueDrops(t, path),
		"T-16b drop oracle counted a drop of another manager (channel mock-race, chat 1) as its own")
}
