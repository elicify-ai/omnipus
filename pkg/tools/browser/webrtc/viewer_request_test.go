package webrtc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

func viewerRequestOffer(t *testing.T, s *Session) (*pion.PeerConnection, string) {
	t.Helper()
	pc, err := s.buildPeerConnection(s.api, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	for _, kind := range []pion.RTPCodecType{pion.RTPCodecTypeVideo, pion.RTPCodecTypeAudio} {
		if _, err := pc.AddTransceiverFromKind(kind, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := pion.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
		t.Fatal("viewer fixture gathering timed out")
	}
	return pc, pc.LocalDescription().SDP
}

func TestViewerRequestLateOldAdmissionPreservesConnectedWinner(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s := NewSession(Config{}, nil, func(format string, _ ...any) {
		if strings.Contains(format, "offer received") && armed.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
	})
	t.Cleanup(func() { _ = s.Close() })
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")
	_, oldOffer := viewerRequestOffer(t, s)
	newClient, newOffer := viewerRequestOffer(t, s)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		answer string
		handle any
		err    error
	}
	done := make(chan result, 1)
	armed.Store(true)
	go func() {
		a, h, e := s.HandleViewerOfferHandleRequest(context.Background(), parent, 1, "viewer", oldOffer)
		done <- result{a, h, e}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old request never reached pre-install boundary")
	}
	answer, winnerHandle, err := s.HandleViewerOfferHandleRequest(context.Background(), parent, 2, "viewer", newOffer)
	if err != nil || winnerHandle == nil {
		t.Fatalf("new request: %v", err)
	}
	if err := newClient.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatal(err)
	}
	s.viewersMu.Lock()
	winner := s.viewers["viewer"]
	s.viewersMu.Unlock()
	until := time.Now().Add(3 * time.Second)
	for winner.pc.ConnectionState() != pion.PeerConnectionStateConnected && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if winner.pc.ConnectionState() != pion.PeerConnectionStateConnected {
		t.Fatal("new viewer did not connect before old request resumed")
	}
	if !s.IsViewerCurrent(winnerHandle) {
		t.Error("exact connected winner handle rejected")
	}
	releaseOnce.Do(func() { close(release) })
	var old result
	select {
	case old = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("old request did not finish after release")
	}
	if old.err == nil || old.answer != "" || old.handle != nil {
		t.Errorf("delayed old request installed: answer bytes=%d handle=%v err=%v", len(old.answer), old.handle, old.err)
	}
	s.CloseViewerIfCurrent(old.handle)
	s.viewersMu.Lock()
	got, count := s.viewers["viewer"], len(s.viewers)
	s.viewersMu.Unlock()
	if got != winner || count != 1 || winner.inputCtx.Err() != nil || winner.pc.ConnectionState() != pion.PeerConnectionStateConnected {
		t.Fatal("old admission/cleanup displaced connected winning peer")
	}
}

func TestViewerRequestNegotiationCancellationInterruptsTrackWait(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	s := NewSession(Config{}, nil, func(format string, _ ...any) {
		if strings.Contains(format, "offer received") {
			once.Do(func() { close(entered) })
		}
	})
	t.Cleanup(func() { _ = s.Close() })
	parent, stopParent := context.WithCancel(context.Background())
	defer stopParent()
	negotiation, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, h, e := s.HandleViewerOfferHandleRequest(negotiation, parent, 1, "viewer", "waiting-offer")
		s.CloseViewerIfCurrent(h)
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not enter wait")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("negotiation canceled wait error=%v", err)
		}
	case <-time.After(time.Second):
		t.Error("temporary negotiation cancellation left request waiting for tracks")
		setLiveTrack(t, s, "video")
		setLiveTrack(t, s, "audio")
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("released request did not drain")
		}
	}
	if parent.Err() != nil {
		t.Error("temporary cancellation ended original attachment")
	}
}

type viewerRequestMarker struct{}

func TestViewerRequestTemporaryCompletionPreservesOriginalPeerLifetime(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")
	_, offer := viewerRequestOffer(t, s)
	parent, stopParent := context.WithCancel(context.WithValue(context.Background(), viewerRequestMarker{}, "original"))
	defer stopParent()
	negotiation, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, handle, err := s.HandleViewerOfferHandleRequest(negotiation, parent, 1, "viewer", offer)
	if err != nil || handle == nil {
		t.Fatal(err)
	}
	s.viewersMu.Lock()
	vc := s.viewers["viewer"]
	s.viewersMu.Unlock()
	cancel()
	if vc.inputCtx.Err() != nil || vc.inputCtx.Value(viewerRequestMarker{}) != "original" {
		t.Fatal("negotiation completion lost persistent original source")
	}
	stopParent()
	select {
	case <-vc.inputCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("original attachment did not cancel peer input")
	}
}

func TestViewerRequestCurrentHandleRequiresExactLiveIdentity(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	pc, err := s.buildPeerConnection(s.apiViewer, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &ViewerHandle{viewerID: "viewer", pc: pc}
	vc := &viewerConn{pc: pc, handle: h, inputCtx: ctx, inputCancel: cancel}
	s.viewersMu.Lock()
	s.viewers["viewer"] = vc
	s.viewersMu.Unlock()
	if !s.IsViewerCurrent(h) {
		t.Error("current exact handle rejected")
	}
	for _, wrong := range []any{nil, "viewer", &ViewerHandle{viewerID: "viewer", pc: pc}, &ViewerHandle{viewerID: "other", pc: pc}} {
		if s.IsViewerCurrent(wrong) {
			t.Errorf("foreign handle accepted: %v", wrong)
		}
	}
	cancel() // No removal callback is registered in this fixture: canceled-but-present is intentional.
	if s.IsViewerCurrent(h) {
		t.Error("canceled-but-not-yet-removed peer treated as current")
	}
}

func TestViewerRequestSessionCloseCancelsPendingReservation(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	s := NewSession(Config{}, nil, func(format string, _ ...any) {
		if strings.Contains(format, "offer received") {
			once.Do(func() { close(entered) })
		}
	})
	t.Cleanup(func() { _ = s.Close() })
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := s.HandleViewerOfferHandleRequest(context.Background(), parent, 1, "viewer", "waiting-offer")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not reserve before close")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s.viewersMu.Lock()
	pending := len(s.viewerRequests)
	s.viewersMu.Unlock()
	if pending != 0 {
		t.Errorf("closed session retained %d request reservations", pending)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("closed session accepted pending request")
		}
	case <-time.After(time.Second):
		t.Error("session close left viewer request waiting for tracks")
		setLiveTrack(t, s, "video")
		setLiveTrack(t, s, "audio")
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("closed request did not drain after fixture release")
		}
	}
}

// The real current viewer must survive native preparation of a replacement.
// The SettingEngine callback controls only external interface enumeration.
func TestViewerRequestCanceledNativeReplacementPreservesConnectedPeer(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")
	client, offer := viewerRequestOffer(t, s)
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	answer, originalHandle, err := s.HandleViewerOfferHandleRequest(context.Background(), parent, 1, "viewer", offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatal(err)
	}
	s.viewersMu.Lock()
	original := s.viewers["viewer"]
	s.viewersMu.Unlock()
	until := time.Now().Add(3 * time.Second)
	for original.pc.ConnectionState() != pion.PeerConnectionStateConnected && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if original.pc.ConnectionState() != pion.PeerConnectionStateConnected {
		t.Fatal("original viewer did not connect")
	}
	_, replacementOffer := viewerRequestOffer(t, s)
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var settings pion.SettingEngine
	settings.SetInterfaceFilter(func(string) bool { enterOnce.Do(func() { close(entered); <-release }); return true })
	s.apiViewer = pion.NewAPI(pion.WithSettingEngine(settings))
	negotiation, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		handle any
		err    error
	}
	done := make(chan result, 1)
	go func() {
		_, h, e := s.HandleViewerOfferHandleRequest(negotiation, parent, 2, "viewer", replacementOffer)
		done <- result{h, e}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement never reached native preparation")
	}
	if !s.IsViewerCurrent(originalHandle) || original.inputCtx.Err() != nil || original.pc.ConnectionState() != pion.PeerConnectionStateConnected {
		t.Error("unfinished replacement displaced the working viewer")
	}
	cancel()
	var out result
	select {
	case out = <-done:
	case <-time.After(time.Second):
		t.Error("canceled native replacement did not return promptly")
		releaseOnce.Do(func() { close(release) })
		select {
		case out = <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("released replacement did not drain")
		}
	}
	if !errors.Is(out.err, context.Canceled) || out.handle != nil {
		t.Errorf("uninstalled canceled replacement: handle=%v error=%v", out.handle, out.err)
	}
	releaseOnce.Do(func() { close(release) })
	s.CloseViewerIfCurrent(out.handle)
	s.viewersMu.Lock()
	current, count := s.viewers["viewer"], len(s.viewers)
	s.viewersMu.Unlock()
	if current != original || count != 1 || original.inputCtx.Err() != nil || original.pc.ConnectionState() != pion.PeerConnectionStateConnected {
		t.Error("canceled replacement or its cleanup removed the working viewer")
	}
}

func TestViewerRequestRejectsClosedPreparedCandidate(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	pc, err := s.buildPeerConnection(s.apiViewer, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := pc.Close(); err != nil {
		t.Fatal(err)
	}
	source, cancel := context.WithCancel(context.Background())
	defer cancel()
	vc := &viewerConn{pc: pc, inputCtx: source, inputCancel: cancel, handle: &ViewerHandle{viewerID: "closed", pc: pc}}
	err = s.installViewerCandidate(context.Background(), nil, "closed-fixture", vc, nil)
	if err == nil {
		t.Error("closed prepared candidate was installed")
	}
	s.viewersMu.Lock()
	count := len(s.viewers)
	s.viewersMu.Unlock()
	if count != 0 {
		t.Errorf("closed candidate left %d published viewers", count)
	}
}

func TestViewerRequestNativeSaturationPreservesHealthyPeerAndReusesReleasedSlot(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	client, offer := viewerRequestOffer(t, s)
	answer, originalHandle, err := s.HandleViewerOfferHandleRequest(context.Background(), parent, 1, "viewer", offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatal(err)
	}
	s.viewersMu.Lock()
	original := s.viewers["viewer"]
	s.viewersMu.Unlock()
	until := time.Now().Add(3 * time.Second)
	for original.pc.ConnectionState() != pion.PeerConnectionStateConnected && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if original.pc.ConnectionState() != pion.PeerConnectionStateConnected {
		t.Fatal("original viewer did not connect")
	}
	_, replacementOffer := viewerRequestOffer(t, s)
	originalAPI := s.apiViewer
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	type result struct {
		answer string
		handle any
		err    error
	}
	for i := 0; i < 4; i++ {
		entered := make(chan struct{})
		var once sync.Once
		var settings pion.SettingEngine
		settings.SetInterfaceFilter(func(string) bool { once.Do(func() { close(entered); <-release }); return true })
		s.apiViewer = pion.NewAPI(pion.WithSettingEngine(settings))
		negotiation, cancel := context.WithCancel(context.Background())
		done := make(chan result, 1)
		go func(epoch uint64) {
			a, h, e := s.HandleViewerOfferHandleRequest(negotiation, parent, epoch, "viewer", replacementOffer)
			done <- result{a, h, e}
		}(uint64(i + 2))
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("native worker did not enter enumeration")
		}
		cancel()
		select {
		case got := <-done:
			if !errors.Is(got.err, context.Canceled) || got.answer != "" || got.handle != nil {
				t.Errorf("canceled uninstalled request=%+v", got)
			}
		case <-time.After(time.Second):
			t.Error("canceled native caller did not return")
			releaseOnce.Do(func() { close(release) })
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("released native caller did not drain")
			}
			return
		}
	}
	s.viewerPreparations.mu.Lock()
	occupied := s.viewerPreparations.active
	s.viewerPreparations.mu.Unlock()
	if occupied != 4 {
		t.Errorf("blocked native preparations occupy %d slots, want four", occupied)
	}
	s.apiViewer = originalAPI
	a, h, err := s.HandleViewerOfferHandleRequest(context.Background(), parent, 6, "viewer", replacementOffer)
	if !errors.Is(err, errViewerPreparationBusy) || a != "" || h != nil {
		t.Errorf("fifth native request: answer=%d handle=%v error=%v", len(a), h, err)
	}
	if !s.IsViewerCurrent(originalHandle) || original.inputCtx.Err() != nil || original.pc.ConnectionState() != pion.PeerConnectionStateConnected {
		t.Error("saturated replacement displaced healthy peer")
	}
	releaseOnce.Do(func() { close(release) })
	until = time.Now().Add(3 * time.Second)
	for {
		s.viewerPreparations.mu.Lock()
		occupied = s.viewerPreparations.active
		s.viewerPreparations.mu.Unlock()
		if occupied == 0 || time.Now().After(until) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if occupied != 0 {
		t.Fatalf("released native workers retained %d slots", occupied)
	}
	newClient, newOffer := viewerRequestOffer(t, s)
	answer, newHandle, err := s.HandleViewerOfferHandleRequest(context.Background(), parent, 7, "viewer", newOffer)
	if err != nil || newHandle == nil || !s.IsViewerCurrent(newHandle) {
		t.Fatalf("released preparation capacity not reusable: %v", err)
	}
	if err := newClient.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatal(err)
	}
	s.viewersMu.Lock()
	replacement := s.viewers["viewer"]
	s.viewersMu.Unlock()
	until = time.Now().Add(3 * time.Second)
	for replacement.pc.ConnectionState() != pion.PeerConnectionStateConnected && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if replacement.pc.ConnectionState() != pion.PeerConnectionStateConnected || original.inputCtx.Err() == nil {
		t.Fatal("released-slot replacement did not connect and retire the original source")
	}
}

func TestViewerRequestRepeatedAndOlderEpochCannotReplaceWinner(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")
	_, offer := viewerRequestOffer(t, s)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, winner, err := s.HandleViewerOfferHandleRequest(context.Background(), context.WithValue(parent, viewerRequestMarker{}, "first wrapper"), 2, "viewer", offer)
	if err != nil {
		t.Fatal(err)
	}
	for _, epoch := range []uint64{1, 2} {
		answer, handle, err := s.HandleViewerOfferHandleRequest(context.Background(), context.WithValue(parent, viewerRequestMarker{}, "another wrapper"), epoch, "viewer", offer)
		if !errors.Is(err, errStaleViewerRequest) || answer != "" || handle != nil {
			t.Errorf("epoch %d replaced admitted epoch two: answer=%d handle=%v error=%v", epoch, len(answer), handle, err)
		}
		if !s.IsViewerCurrent(winner) {
			t.Error("repeated or older admission removed the winning peer")
		}
	}
}
