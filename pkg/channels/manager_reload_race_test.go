package channels

// Moved verbatim from manager_test.go (file-size budget, #1676).

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// TestRunWorker_NoConfigRaceAcrossReload is a deterministic -race regression
// test for a separate, real, pre-existing data race: runWorker (manager.go
// ~:1179, pre-fix) read m.config directly with NO lock while Reload
// (manager.go ~:1661) writes m.config under m.mu.Lock(). `go test -race`
// reproduced this reliably in isolation ("WARNING: DATA RACE ... Read ...
// (*Manager).runWorker() ... manager.go:1179" vs "Previous write ... Reload"),
// independent of the removed-channel/dispatch-close race the sibling test
// above covers — it fires even when no channel is ever removed.
//
// This test drives ONLY production entry points (Reload, PublishOutbound) —
// never runWorker directly — so it stays meaningful against the actual fix
// (runWorker now takes a config snapshot captured by its caller while holding
// m.mu.Lock(), instead of reading m.config itself) rather than merely
// asserting a call signature. A test that called runWorker directly with a
// pre-captured snapshot would trivially pass regardless of whether the
// production fix is present or reverted, and would fail to compile against
// the reverted (3-arg) signature — neither of which proves anything about the
// real bug.
//
// It registers a real channel instance ("mock-race") via a factory and keeps
// its config hash IDENTICAL across many repeated Reload calls (only the
// unrelated SplitOnMarker flag toggles, which does not affect the per-channel
// hash toChannelHashes computes). An unchanged hash routes every Reload
// through the "restart workers for existing (unchanged) channels" path
// (manager.go, in Reload), which tears down and relaunches runWorker's
// goroutine on every single call — exactly the repeated write/restart
// interleaving the race needed, with messages continuously flowing into the
// worker's queue (and being read as m.config, pre-fix) throughout.
func TestRunWorker_NoConfigRaceAcrossReload(t *testing.T) {
	withFactory(t, "mock-race", func(*config.Config, string, credentials.SecretBundle, *bus.MessageBus) (Channel, error) {
		return &mockChannel{sendFn: func(context.Context, bus.OutboundMessage) error { return nil }}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           bus.NewMessageBus(),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}
	// #1676: join the last dispatch generation before the test ends. A
	// cancel alone left a straggling dispatcher that logged its full-queue drop
	// into a later test's log file. StopAll cancels AND waits for the
	// dispatchers and workers (Reload already joins every older generation).
	t.Cleanup(func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := m.StopAll(stopCtx); err != nil {
			t.Errorf("cleanup StopAll: %v", err)
		}
	})

	newCfg := func(splitOnMarker bool) *config.Config {
		cfg := &config.Config{
			Channels: map[string]config.ChannelInstanceConfig{
				"mock-race": {Type: "mock-race", Enabled: true},
			},
		}
		cfg.Agents.Defaults.SplitOnMarker = splitOnMarker
		return cfg
	}

	// Bootstrap: this Reload adds "mock-race" (nothing existed before), starting
	// its channel and spinning up the first runWorker generation.
	if err := m.Reload(ctx, newCfg(false), credentials.SecretBundle{}); err != nil {
		t.Fatalf("bootstrap Reload: %v", err)
	}

	// Flood the bus with outbound messages for "mock-race" so dispatchOutbound
	// keeps pushing onto the worker's queue — and runWorker keeps dequeuing and
	// (pre-fix) reading m.config — for the entire duration of the repeated
	// Reload calls below.
	publishStop := make(chan struct{})
	var publishers sync.WaitGroup
	for i := 0; i < 16; i++ {
		publishers.Add(1)
		go func() {
			defer publishers.Done()
			for {
				select {
				case <-publishStop:
					return
				default:
					_ = m.bus.PublishOutbound(ctx, bus.OutboundMessage{
						Channel: "mock-race", ChatID: "1", Content: "x",
					})
				}
			}
		}()
	}

	// Repeated Reloads with an unchanged channel hash: every call restarts
	// "mock-race"'s runWorker generation on a fresh dispatchCtx while writing a
	// brand new m.config, racing the OLD (still-draining) generation's read.
	for i := 0; i < 60; i++ {
		if err := m.Reload(ctx, newCfg(i%2 == 0), credentials.SecretBundle{}); err != nil {
			t.Fatalf("Reload iteration %d: %v", i, err)
		}
	}

	close(publishStop)
	publishers.Wait()

	if _, ok := m.GetChannel("mock-race"); !ok {
		t.Fatalf("expected mock-race channel to still be registered after repeated Reloads")
	}
}
