package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/require"
)

// T-29/S-24: actual REST revocation + Manager.Reload/worker join, not a pure
// predicate, limiter-cancellation proxy or read-lock-only probe.
func TestT29_ReloadAndDequeue_RealWriteLockInterleavingCannotDeadlockOrSendRevokedM2(t *testing.T) {
	f, adapter, manager, lifecycle := t29Fixture(t)
	t32FaultRefresh(t, f) // real REST save still narrowly publishes the revocation
	revision := t14Read(t, f, "discord").Revision
	paused, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	manager.SetBeforeDequeueStartCheckHookForTest(func(msg bus.OutboundMessage) {
		if msg.Content == "M2" {
			close(paused)
			<-release
		}
	})
	locks, joins := make(chan string, 2), make(chan string, 2)
	manager.SetReloadWriteLockHookForTest(func(stage string) { locks <- stage }) // only signal under m.mu
	manager.SetWorkerJoinHookForTest(func(instance, phase string) {
		if instance == "discord" {
			joins <- phase // actual old worker, while Reload holds m.mu
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		manager.SetBeforeDequeueStartCheckHookForTest(nil)
		manager.SetReloadWriteLockHookForTest(nil)
		manager.SetWorkerJoinHookForTest(nil)
	})
	delivered := make(chan string, 4)
	adapter.send = func(_ context.Context, msg bus.OutboundMessage) error { delivered <- msg.Content; return nil }
	require.NoError(t, f.bus.PublishOutbound(ctx, t15Start(t, "M1")))
	select {
	case text := <-delivered:
		require.Equal(t, "M1", text, "instrument: an allowed start traverses this actual pipeline")
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: allowed M1 never reached Send")
	}
	require.NoError(t, f.bus.PublishOutbound(ctx, t15Start(t, "M2")))
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: real worker never reached the pre-dequeue M2 barrier")
	}
	// One normative five-second bound for the forced interleaving, not a sleep.
	bound, stopBound := context.WithTimeout(ctx, 5*time.Second)
	defer stopBound()
	reloaded := make(chan error, 1)
	go func() { reloaded <- manager.Reload(ctx, lifecycle, nil) }()
	for _, want := range []string{"acquired", "reacquired"} {
		select {
		case stage := <-locks:
			require.Equal(t, want, stage)
		case <-bound.Done():
			t.Fatal("S-24: actual Reload did not reach its write-lock stages")
		}
	}
	select {
	case phase := <-joins:
		require.Equal(t, "joining", phase, "instrument: Reload now holds m.mu waiting for this exact paused worker")
	case <-bound.Done():
		t.Fatal("instrument: actual Reload never began the paused worker's join")
	}
	body, err := json.Marshal(gen.ChannelOutboundUpdateRequest{Policy: "reply_only", Targets: []string{"222"}, ExpectedRevision: revision})
	require.NoError(t, err) // all fatal assertions stay on the test goroutine
	saved := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		saved <- t14Request(t, f, http.MethodPut, "/api/v1/channels/discord/outbound", string(body), true)
	}()
	select {
	case rec := <-saved:
		require.Equal(t, http.StatusOK, rec.Code, "S-24: real REST revocation completes before M2 is released: %s", rec.Body.String())
	case <-bound.Done():
		t.Fatal("S-24: real REST revocation stalled while Reload held the lifecycle lock")
	}
	require.Equal(t, "reply_only", f.al.GetConfig().Channels["discord"].Outbound.Policy,
		"instrument: actual live policy is revoked while Reload is still joining M2")
	releaseOnce.Do(func() { close(release) })
	select {
	case phase := <-joins:
		require.Equal(t, "joined", phase, "S-24: the worker exited without acquiring Reload's write lock")
	case <-bound.Done():
		t.Fatal("S-24: M2 dequeue check deadlocked against Reload's write lock")
	}
	select {
	case err := <-reloaded:
		require.NoError(t, err)
	case <-bound.Done():
		t.Fatal("S-24: actual Reload did not finish within five seconds")
	}
	// The new dispatcher/worker's real reply is a completion/control fence. Any
	// wrongly derived notice is observable before it; M2 must never reach Send.
	require.NoError(t, f.bus.PublishOutbound(ctx, bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "QA T29 reply fence"}))
	select {
	case text := <-delivered:
		require.Equal(t, "QA T29 reply fence", text)
	case <-bound.Done():
		t.Fatal("S-24: manager failed to serve the post-reload ordinary reply")
	}
	attempts := adapter.attemptSnapshot()
	contents := make([]string, 0, len(attempts))
	for _, msg := range attempts {
		contents = append(contents, msg.Content)
	}
	require.Equal(t, []string{"M1", "QA T29 reply fence"}, contents, "zero revoked M2 Send and zero derived chat notices")
	t13AssertOutbound(t, f, "discord", "reply_only", []string{"discord:222"})
}
