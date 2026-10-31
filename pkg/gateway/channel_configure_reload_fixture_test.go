package gateway

// T-02/T-02b/T-03/T-07 fixture repair: exercise the real production reload
// consumer. No shared fixture is changed and no outcome is manufactured.
// External model and LINE HTTP are the existing issue1277 process-edge fakes.

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

func t02Fixture(t *testing.T) *wu6Fixture {
	t.Helper()
	// Unlike orderingWriterFixture, this needs no enabled WhatsApp instance:
	// it owns Configure/reload, not native pairing admission.
	f := newWU6Fixture(t)
	t02WireReload(t, f, f.provider)
	return f
}

func t02WireReload(t *testing.T, f *wu6Fixture, provider providers.LLMProvider) {
	t.Helper()
	f.svc.reloadTrigger = newReloadTrigger(f.svc, f.al)
	f.al.SetReloadFunc(f.svc.reloadTrigger)
	stop, drained := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(stop); <-drained })
	go func() {
		defer close(drained)
		for {
			select {
			case <-stop:
				return
			case <-f.svc.manualReloadChan:
				runReloadCycle(f.al, f.svc, nil, 0, func(cfg *config.Config) error {
					return executeReload(context.Background(), f.al, cfg, &provider, f.svc, f.bus, false)
				}, f.svc.loadConfigForSwap)
			}
		}
	}()
}

func t02BootFixture(t *testing.T, stage func(*issue1277Fixture)) *wu6Fixture {
	t.Helper()
	base := newIssue1277Fixture(t)
	base.cfg.Gateway.DevModeBypass = true
	stage(base)
	require.NoError(t, config.SaveConfig(base.configPath, base.cfg))
	cfg, bundle, _ := base.boot(t)
	api, svc, msgBus, provider := base.startRuntime(t, cfg, bundle)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	f := &wu6Fixture{t: t, f1277: base, api: api, svc: svc, al: api.agentLoop,
		bus: msgBus, mux: mux, store: base.store}
	t02WireReload(t, f, provider)
	return f
}

// t02StopFailure faults only the adapter boundary. Stop really closes the
// existing adapter, then reports unfinished disposal until the cause is removed.
// Other channel behavior is the original real LINE adapter's behavior.
type t02StopFailure struct {
	channels.Channel
	fail  atomic.Bool
	calls atomic.Int32
}

func (c *t02StopFailure) Stop(ctx context.Context) error {
	c.calls.Add(1)
	err := c.Channel.Stop(ctx)
	if c.fail.Load() {
		return errors.Join(err, errors.New("t02 scripted unfinished adapter disposal"))
	}
	return err
}

func t02FaultStop(t *testing.T, f *wu6Fixture, id string) *t02StopFailure {
	t.Helper()
	original, exists := f.svc.ChannelManager.GetChannel(id)
	require.True(t, exists, "instrument: the adapter exists before faulting its Stop")
	fault := &t02StopFailure{Channel: original}
	fault.fail.Store(true)
	t.Cleanup(func() { fault.fail.Store(false) })
	f.svc.ChannelManager.RegisterChannel(id, fault)
	return fault
}

// t02PauseStop is an adapter-boundary barrier, not a replacement reload.
// Manager disposal runs after bundle capture and before rebuilding the adapter.
// It deliberately does NOT claim the during-capture barrier of R8-MIN-001.
type t02PauseStop struct {
	channels.Channel
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *t02PauseStop) Stop(ctx context.Context) error {
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return c.Channel.Stop(ctx)
}

type t02PendingReporter interface {
	PendingApplication(string) (uint64, bool)
}

// Observe an actual marker while the old rebuild is held at its barrier.
// The deadline is only a hang guard; no elapsed duration is an ordering oracle.
func t02WaitPending(t *testing.T, reporter t02PendingReporter, id string) uint64 {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if version, pending := reporter.PendingApplication(id); pending {
			return version
		}
		select {
		case <-deadline.C:
			t.Fatal("instrument: Configure never published its accepted credential's pending marker")
		default:
			runtime.Gosched()
		}
	}
}
