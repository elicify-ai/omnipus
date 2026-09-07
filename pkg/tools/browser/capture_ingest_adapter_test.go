package browser

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

type adapterOffer struct {
	token, id, generation uint64
	sdp, target           string
}
type adapterRelay struct {
	fakeRelay
	bindingMu sync.Mutex
	nextToken uint64
	requests  []adapterOffer
	offer     func(context.Context, adapterOffer) (string, error)
}

func (r *adapterRelay) BeginIngestBinding(ctx context.Context) (uint64, error) {
	if ctx == nil {
		return 0, errors.New("nil binding context")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.bindingMu.Lock()
	defer r.bindingMu.Unlock()
	r.nextToken += 7
	return r.nextToken, nil
}
func (r *adapterRelay) HandleIngestOfferForBinding(ctx context.Context, token, id uint64, sdp string, generation uint64, target string) (string, error) {
	call := adapterOffer{token, id, generation, sdp, target}
	r.bindingMu.Lock()
	r.requests = append(r.requests, call)
	fn := r.offer
	r.bindingMu.Unlock()
	if fn != nil {
		return fn(ctx, call)
	}
	return "accepted-answer", nil
}
func (r *adapterRelay) calls() []adapterOffer {
	r.bindingMu.Lock()
	defer r.bindingMu.Unlock()
	return append([]adapterOffer(nil), r.requests...)
}
func adapterFixture(t *testing.T) (*CaptureSession, *adapterRelay) {
	t.Helper()
	r := &adapterRelay{nextToken: 40}
	var starts int32
	cs, err := NewCaptureSessionWithDeps(nil, "adapter", r, fakeEncoderStarter(&starts, nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.BeginFrameTransition("page-a", 800, 600, 1)
	require.NoError(t, err)
	return cs, r
}
func adapterBind(t *testing.T, cs *CaptureSession, ctx context.Context) uint64 {
	t.Helper()
	_, epoch, err := cs.BindIngestContext(ctx, func(string, *string, int, int, int) error { return nil }, func() {})
	require.NoError(t, err)
	return epoch
}
func awaitAdapterError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("pending ingest did not cancel")
		return nil
	}
}

func TestCaptureIngestAdapterUsesRelayTokenNotSocketEpoch(t *testing.T) {
	cs, r := adapterFixture(t)
	epoch := adapterBind(t, cs, context.Background())
	answer, err := cs.HandleIngestOfferForBinding(context.Background(), epoch, 9, "offer-sdp", 1, "page-a")
	require.NoError(t, err)
	require.Equal(t, "accepted-answer", answer)
	require.Equal(t, []adapterOffer{{47, 9, 1, "offer-sdp", "page-a"}}, r.calls())
	require.Equal(t, uint64(1), epoch)
}

func TestCaptureIngestAdapterRejectsUnqualifiedOffers(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		epoch, id, generation uint64
		target                string
	}{
		{"missing epoch", 0, 1, 1, "page-a"}, {"wrong epoch", 2, 1, 1, "page-a"},
		{"missing ID", 1, 0, 1, "page-a"}, {"unsafe ID", 1, 9007199254740992, 1, "page-a"},
		{"missing generation", 1, 1, 0, "page-a"}, {"wrong generation", 1, 1, 2, "page-a"},
		{"wrong target", 1, 1, 1, "page-b"}, {"missing target", 1, 1, 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, r := adapterFixture(t)
			adapterBind(t, cs, context.Background())
			answer, err := cs.HandleIngestOfferForBinding(context.Background(), tc.epoch, tc.id, "sdp", tc.generation, tc.target)
			require.ErrorIs(t, err, webrtc.ErrStaleIngestOffer)
			require.Empty(t, answer)
			require.Empty(t, r.calls())
		})
	}
}

func TestCaptureIngestAdapterRejectsRepeatedOfferID(t *testing.T) {
	cs, r := adapterFixture(t)
	epoch := adapterBind(t, cs, context.Background())
	_, err := cs.HandleIngestOfferForBinding(context.Background(), epoch, 3, "sdp", 1, "page-a")
	require.NoError(t, err)
	_, err = cs.HandleIngestOfferForBinding(context.Background(), epoch, 3, "sdp", 1, "page-a")
	require.ErrorIs(t, err, webrtc.ErrStaleIngestOffer)
	require.Len(t, r.calls(), 1)
}

func TestCaptureIngestAdapterFrameChangeCancelsPendingOffer(t *testing.T) {
	cs, r := adapterFixture(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	epoch := adapterBind(t, cs, caller)
	entered := make(chan struct{})
	r.offer = func(ctx context.Context, _ adapterOffer) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	result := make(chan error, 1)
	go func() { _, err := cs.HandleIngestOfferForBinding(caller, epoch, 1, "sdp", 1, "page-a"); result <- err }()
	<-entered
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	require.Error(t, awaitAdapterError(t, result))
}

func TestCaptureIngestAdapterSameFrameDoesNotCancelOffer(t *testing.T) {
	cs, r := adapterFixture(t)
	epoch := adapterBind(t, cs, context.Background())
	r.offer = func(ctx context.Context, _ adapterOffer) (string, error) {
		_, err := cs.BeginFrameTransition("page-a", 800, 600, 1)
		require.NoError(t, err)
		require.NoError(t, ctx.Err())
		return "same-frame-answer", nil
	}
	answer, err := cs.HandleIngestOfferForBinding(context.Background(), epoch, 1, "sdp", 1, "page-a")
	require.NoError(t, err)
	require.Equal(t, "same-frame-answer", answer)
}

func TestCaptureIngestAdapterRechecksFrameAfterNegotiation(t *testing.T) {
	cs, r := adapterFixture(t)
	epoch := adapterBind(t, cs, context.Background())
	r.offer = func(context.Context, adapterOffer) (string, error) {
		_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
		require.NoError(t, err)
		return "obsolete-answer", nil
	}
	answer, err := cs.HandleIngestOfferForBinding(context.Background(), epoch, 1, "sdp", 1, "page-a")
	require.ErrorIs(t, err, webrtc.ErrStaleIngestOffer)
	require.Empty(t, answer)
}

func TestCaptureIngestAdapterRebindingCancelsOldOfferAndStaleUnbindIsHarmless(t *testing.T) {
	cs, r := adapterFixture(t)
	old, closeOld := context.WithCancel(context.Background())
	defer closeOld()
	first := adapterBind(t, cs, old)
	entered := make(chan struct{})
	r.offer = func(ctx context.Context, _ adapterOffer) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	result := make(chan error, 1)
	go func() { _, err := cs.HandleIngestOfferForBinding(old, first, 1, "sdp", 1, "page-a"); result <- err }()
	<-entered
	second := adapterBind(t, cs, context.Background())
	require.Error(t, awaitAdapterError(t, result))
	cs.UnbindIngest(first)
	r.bindingMu.Lock()
	r.offer = nil
	r.bindingMu.Unlock()
	answer, err := cs.HandleIngestOfferForBinding(context.Background(), second, 1, "new-sdp", 1, "page-a")
	require.NoError(t, err)
	require.Equal(t, "accepted-answer", answer)
	require.Equal(t, adapterOffer{54, 1, 1, "new-sdp", "page-a"}, r.calls()[1])
}

func TestCaptureIngestAdapterOriginalSocketCancellationRejectsOffer(t *testing.T) {
	cs, r := adapterFixture(t)
	socket, cancel := context.WithCancel(context.Background())
	epoch := adapterBind(t, cs, socket)
	cancel()
	answer, err := cs.HandleIngestOfferForBinding(context.Background(), epoch, 1, "sdp", 1, "page-a")
	require.Error(t, err)
	require.Empty(t, answer)
	require.Empty(t, r.calls())
}

func TestCaptureIngestAdapterStopCancelsBeforeShutdownWrite(t *testing.T) {
	cs, r := adapterFixture(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdown := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	_, epoch, err := cs.BindIngestContext(caller, func(action string, _ *string, _, _, _ int) error {
		if action == "shutdown" {
			close(shutdown)
			<-release
		}
		return nil
	}, func() {})
	require.NoError(t, err)
	entered := make(chan struct{})
	r.offer = func(ctx context.Context, _ adapterOffer) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	result := make(chan error, 1)
	go func() { _, err := cs.HandleIngestOfferForBinding(caller, epoch, 1, "sdp", 1, "page-a"); result <- err }()
	<-entered
	go cs.Stop()
	<-shutdown
	require.Error(t, awaitAdapterError(t, result))
}

func TestCaptureIngestBindingRejectsMissingCapabilityAndCanceledSocket(t *testing.T) {
	cs := &CaptureSession{relay: &fakeRelay{}}
	_, _, err := cs.BindIngestContext(context.Background(), nil, nil)
	require.EqualError(t, err, "capture session: context-bound ingest is unsupported")
	cs, r := adapterFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, epoch, err := cs.BindIngestContext(ctx, nil, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, epoch)
	require.Equal(t, uint64(40), r.nextToken)
}

func TestCaptureIngestBindingIdentityClearsOnSocketCancellation(t *testing.T) {
	cs, _ := adapterFixture(t)
	socket, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapterBind(t, cs, socket)
	epoch, token := cs.CurrentIngestBinding()
	require.Equal(t, uint64(1), epoch)
	require.Equal(t, uint64(47), token)
	cancel()
	epoch, token = cs.CurrentIngestBinding()
	require.Zero(t, epoch)
	require.Zero(t, token)
}
