package browser

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

type captureRequestRouteKey struct{}

type requestOfferProbe struct {
	fakeOfferRelay
	calls       atomic.Int64
	legacyCalls atomic.Int64
	offer       func(context.Context, context.Context, uint64, string, string) (string, any, error)
}

func (r *requestOfferProbe) HandleViewerOfferHandleRequest(negotiation, parent context.Context, epoch uint64, id, sdp string) (string, any, error) {
	r.calls.Add(1)
	return r.offer(negotiation, parent, epoch, id, sdp)
}
func (r *requestOfferProbe) HandleViewerOfferHandleContext(parent context.Context, id, sdp string) (string, any, error) {
	r.legacyCalls.Add(1)
	return r.offer(parent, parent, 0, id, sdp)
}
func (r *requestOfferProbe) IsViewerCurrent(handle any) bool {
	h, ok := handle.(*fakeOfferHandle)
	return ok && h != nil && r.isCurrentlyRegistered(h.viewerID, h)
}
func captureRequestFixture(t *testing.T, r RelaySession) *CaptureSession {
	t.Helper()
	cs, err := NewCaptureSessionWithDeps(nil, "request-test", r, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cs.Stop)
	return cs
}
func captureRequestRegistration(cs *CaptureSession) (viewerRegistration, int, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.viewers["viewer"], len(cs.viewers), cs.stopTimer != nil
}
func captureRequestSuccess(r *requestOfferProbe) {
	r.offer = func(_ context.Context, _ context.Context, _ uint64, id, sdp string) (string, any, error) {
		return "answer-" + sdp, r.registerHandleOK(id), nil
	}
}
func TestCaptureViewerRequestRejectsDelayedOldAdmission(t *testing.T) {
	r := &requestOfferProbe{}
	captureRequestSuccess(r)
	cs := captureRequestFixture(t, r)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, newest, err := cs.HandleViewerOfferRequest(context.Background(), context.WithValue(parent, captureRequestRouteKey{}, "new"), 2, "viewer", "new")
	if err != nil || a != "answer-new" || newest == nil {
		t.Fatalf("new result %q %v %v", a, newest, err)
	}
	winner, count, _ := captureRequestRegistration(cs)
	if count != 1 || winner.relayHandle != newest.relay {
		t.Fatal("new viewer was not registered exactly once")
	}
	a, old, err := cs.HandleViewerOfferRequest(context.Background(), context.WithValue(parent, captureRequestRouteKey{}, "old"), 1, "viewer", "old")
	if err == nil || a != "" || old != nil {
		t.Errorf("delayed old admitted: answer=%q handle=%v err=%v", a, old, err)
	}
	cs.CleanupViewerOffer(old)
	got, count, _ := captureRequestRegistration(cs)
	if got != winner || count != 1 || !r.isCurrentlyRegistered("viewer", fixtureValue[*fakeOfferHandle](winner.relayHandle)) {
		t.Fatal("delayed old request/cleanup displaced winning registration or peer")
	}
	if r.calls.Load() != 1 || r.legacyCalls.Load() != 0 {
		t.Fatalf("request=%d legacy=%d want1,0", r.calls.Load(), r.legacyCalls.Load())
	}
}
func TestCaptureViewerRequestRetainsActiveDuringCanceledReplacement(t *testing.T) {
	r := &requestOfferProbe{}
	captureRequestSuccess(r)
	cs := captureRequestFixture(t, r)
	parent, stopParent := context.WithCancel(context.Background())
	defer stopParent()
	_, first, err := cs.HandleViewerOfferRequest(context.Background(), parent, 1, "viewer", "first")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	winner, _, _ := captureRequestRegistration(cs)
	negotiation, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	r.offer = func(ctx, original context.Context, epoch uint64, id, sdp string) (string, any, error) {
		if ctx != negotiation || original != parent || epoch != 2 {
			t.Errorf("replacement changed context pair or epoch")
		}
		close(entered)
		<-negotiation.Done()
		return "", nil, negotiation.Err()
	}
	type result struct {
		handle *ViewerAttachHandle
		err    error
	}
	done := make(chan result, 1)
	go func() {
		_, h, e := cs.HandleViewerOfferRequest(negotiation, parent, 2, "viewer", "replacement")
		done <- result{h, e}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("replacement never delegated")
	}
	got, count, _ := captureRequestRegistration(cs)
	if got != winner || count != 1 {
		t.Error("pending replacement erased active registration")
	}
	cancel()
	var out result
	select {
	case out = <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled replacement did not return")
	}
	if !errors.Is(out.err, context.Canceled) {
		t.Errorf("replacement cancellation=%v", out.err)
	}
	cs.CleanupViewerOffer(out.handle)
	got, count, _ = captureRequestRegistration(cs)
	if got != winner || count != 1 || !r.isCurrentlyRegistered("viewer", fixtureValue[*fakeOfferHandle](winner.relayHandle)) {
		t.Fatal("canceled pre-install replacement lost the active viewer")
	}
	if parent.Err() != nil {
		t.Fatal("negotiation cancellation ended persistent source")
	}
}
func TestCaptureViewerRequestRejectsLateSuccessAndKeepsExactCleanup(t *testing.T) {
	r := &requestOfferProbe{}
	cs := captureRequestFixture(t, r)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var oldRelay *fakeOfferHandle
	r.offer = func(_ context.Context, _ context.Context, _ uint64, id, sdp string) (string, any, error) {
		h := r.registerHandleOK(id)
		if sdp == "old" {
			oldRelay = h
			close(entered)
			<-release
		}
		return "answer-" + sdp, h, nil
	}
	type result struct {
		answer string
		handle *ViewerAttachHandle
		err    error
	}
	done := make(chan result, 1)
	go func() {
		a, h, e := cs.HandleViewerOfferRequest(context.Background(), parent, 1, "viewer", "old")
		done <- result{a, h, e}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old request did not enter relay")
	}
	_, newest, err := cs.HandleViewerOfferRequest(context.Background(), parent, 2, "viewer", "new")
	if err != nil || newest == nil {
		t.Fatal(err)
	}
	winner, _, _ := captureRequestRegistration(cs)
	close(release)
	var old result
	select {
	case old = <-done:
	case <-time.After(time.Second):
		t.Fatal("old completion stuck")
	}
	if old.err == nil || old.answer != "" || old.handle == nil || old.handle.relay != oldRelay {
		t.Errorf("late completion answer=%q handle=%v err=%v", old.answer, old.handle, old.err)
	}
	cs.CleanupViewerOffer(old.handle)
	got, count, _ := captureRequestRegistration(cs)
	if got != winner || count != 1 || !r.isCurrentlyRegistered("viewer", fixtureValue[*fakeOfferHandle](winner.relayHandle)) {
		t.Fatal("late completion cleanup removed winning registration or peer")
	}
}
func TestCaptureViewerRequestRequiresFencedCapability(t *testing.T) {
	r := &fakeOfferRelay{}
	cs := captureRequestFixture(t, r)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, h, err := cs.HandleViewerOfferRequest(context.Background(), parent, 1, "viewer", "offer")
	if err == nil || a != "" || h != nil {
		t.Fatalf("unfenced relay silently accepted request: %q %v %v", a, h, err)
	}
	if len(cs.ViewerIDs()) != 0 {
		t.Fatal("unsupported request registered viewer")
	}
}
func TestCaptureViewerRequestProductionCapability(t *testing.T) {
	cs, err := NewCaptureSessionWithContextInput(nil, "agent", "panel", relay.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Stop()
	if _, ok := cs.Relay().(requestViewerOfferHandler); !ok {
		t.Fatal("production relay lacks fenced viewer request capability")
	}
}
func TestCaptureViewerRequestFirstFailureArmsGrace(t *testing.T) {
	r := &requestOfferProbe{}
	failure := errors.New("negotiation failed before install")
	r.offer = func(context.Context, context.Context, uint64, string, string) (string, any, error) {
		return "", nil, failure
	}
	cs := captureRequestFixture(t, r)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, h, err := cs.HandleViewerOfferRequest(context.Background(), parent, 1, "viewer", "offer")
	if !errors.Is(err, failure) || a != "" {
		t.Fatalf("failure result=%q %v", a, err)
	}
	_, count, armed := captureRequestRegistration(cs)
	if count != 0 || !armed {
		t.Errorf("first failed request leaves viewers=%d grace=%v", count, armed)
	}
	cs.CleanupViewerOffer(h)
}
func TestCaptureViewerRequestCohortReplacement(t *testing.T) {
	r := &requestOfferProbe{}
	captureRequestSuccess(r)
	cs := captureRequestFixture(t, r)
	old, cancelOld := context.WithCancel(context.Background())
	defer cancelOld()
	fresh, cancelFresh := context.WithCancel(context.Background())
	defer cancelFresh()
	_, first, err := cs.HandleViewerOfferRequest(context.Background(), old, 9, "viewer", "old")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	if a, h, e := cs.HandleViewerOfferRequest(context.Background(), fresh, 1, "viewer", "premature"); e == nil || a != "" || h != nil {
		t.Error("different live attachment took over existing cohort")
	}
	cancelOld()
	_, newest, err := cs.HandleViewerOfferRequest(context.Background(), fresh, 1, "viewer", "new")
	if err != nil || newest == nil {
		t.Fatalf("canceled cohort blocked new attachment: %v", err)
	}
	winner, _, _ := captureRequestRegistration(cs)
	if a, h, e := cs.HandleViewerOfferRequest(context.Background(), old, 10, "viewer", "late"); !errors.Is(e, context.Canceled) || a != "" || h != nil {
		t.Error("canceled old cohort was admitted")
	}
	cs.CleanupViewerOffer(first)
	got, count, _ := captureRequestRegistration(cs)
	if got != winner || count != 1 || !r.isCurrentlyRegistered("viewer", fixtureValue[*fakeOfferHandle](winner.relayHandle)) {
		t.Fatal("old cohort cleanup removed replacement")
	}
}
func TestCaptureViewerRequestRejectsInvalidOrigins(t *testing.T) {
	for _, which := range []string{"nil-negotiation", "nil-parent", "unbounded-parent", "canceled-negotiation", "canceled-parent", "zero-epoch", "empty-viewer", "empty-sdp", "exhausted-generation"} {
		t.Run(which, func(t *testing.T) {
			r := &requestOfferProbe{}
			captureRequestSuccess(r)
			cs := captureRequestFixture(t, r)
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			negotiation, cancelNegotiation := context.WithCancel(context.Background())
			defer cancelNegotiation()
			epoch, id, sdp := uint64(1), "viewer", "offer"
			switch which {
			case "nil-negotiation":
				negotiation = nil
			case "nil-parent":
				parent = nil
			case "unbounded-parent":
				parent = context.Background()
			case "canceled-negotiation":
				cancelNegotiation()
			case "canceled-parent":
				cancelParent()
			case "zero-epoch":
				epoch = 0
			case "empty-viewer":
				id = ""
			case "empty-sdp":
				sdp = ""
			case "exhausted-generation":
				cs.viewerGenSeq = math.MaxUint64
			}
			a, h, err := cs.HandleViewerOfferRequest(negotiation, parent, epoch, id, sdp)
			if err == nil || a != "" || h != nil {
				t.Errorf("invalid request accepted: %q %v %v", a, h, err)
			}
			if r.calls.Load() != 0 || r.legacyCalls.Load() != 0 || len(cs.ViewerIDs()) != 0 {
				t.Fatal("invalid request touched relay or viewer registry")
			}
		})
	}
}

func TestCaptureViewerRequestOldFailureCannotEndNewPendingAttempt(t *testing.T) {
	r := &requestOfferProbe{}
	cs := captureRequestFixture(t, r)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldEntered, newEntered := make(chan struct{}), make(chan struct{})
	oldRelease, newRelease := make(chan struct{}), make(chan struct{})
	defer func() {
		for _, ch := range []chan struct{}{oldRelease, newRelease} {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}()
	r.offer = func(_ context.Context, _ context.Context, _ uint64, _, sdp string) (string, any, error) {
		if sdp == "old" {
			close(oldEntered)
			<-oldRelease
		} else {
			close(newEntered)
			<-newRelease
		}
		return "", nil, errors.New("pre-install failure")
	}
	oldDone, newDone := make(chan *ViewerAttachHandle, 1), make(chan *ViewerAttachHandle, 1)
	go func() {
		_, h, _ := cs.HandleViewerOfferRequest(context.Background(), parent, 1, "viewer", "old")
		oldDone <- h
	}()
	select {
	case <-oldEntered:
	case <-time.After(time.Second):
		t.Fatal("old never entered")
	}
	go func() {
		_, h, _ := cs.HandleViewerOfferRequest(context.Background(), parent, 2, "viewer", "new")
		newDone <- h
	}()
	select {
	case <-newEntered:
	case <-time.After(time.Second):
		t.Fatal("new never entered")
	}
	close(oldRelease)
	select {
	case h := <-oldDone:
		cs.CleanupViewerOffer(h)
	case <-time.After(time.Second):
		t.Fatal("old never returned")
	}
	_, count, armed := captureRequestRegistration(cs)
	if count != 0 || armed {
		t.Errorf("old cleanup disturbed newer pending attempt: active=%d grace=%v", count, armed)
	}
	close(newRelease)
	select {
	case h := <-newDone:
		cs.CleanupViewerOffer(h)
	case <-time.After(time.Second):
		t.Fatal("new never returned")
	}
	_, count, armed = captureRequestRegistration(cs)
	if count != 0 || !armed {
		t.Errorf("last pending failure leaked capture: active=%d grace=%v", count, armed)
	}
}

func TestCaptureViewerRequestCanceledSuccessRetainsExactCleanup(t *testing.T) {
	r := &requestOfferProbe{}
	cs := captureRequestFixture(t, r)
	parent, stopParent := context.WithCancel(context.Background())
	defer stopParent()
	negotiation, cancel := context.WithCancel(context.Background())
	defer cancel()
	var installed *fakeOfferHandle
	r.offer = func(context.Context, context.Context, uint64, string, string) (string, any, error) {
		installed = r.registerHandleOK("viewer")
		cancel()
		return "late-success", installed, nil
	}
	a, h, err := cs.HandleViewerOfferRequest(negotiation, parent, 1, "viewer", "offer")
	if !errors.Is(err, context.Canceled) || a != "" || h == nil || h.relay != installed {
		t.Fatalf("canceled success answer=%q handle=%v err=%v", a, h, err)
	}
	if len(cs.ViewerIDs()) != 0 {
		t.Error("canceled success published active registration")
	}
	cs.CleanupViewerOffer(h)
	if r.isCurrentlyRegistered("viewer", installed) {
		t.Error("canceled installed candidate was not exactly cleaned")
	}
	if parent.Err() != nil {
		t.Error("temporary cancellation ended attachment lifetime")
	}
}

type captureRequestRouteMarker struct{}

func TestCaptureViewerRequestDecoratedCohortKeepsEpochOrdering(t *testing.T) {
	r := &requestOfferProbe{}
	captureRequestSuccess(r)
	cs := captureRequestFixture(t, r)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstParent := context.WithValue(parent, captureRequestRouteMarker{}, "first-route")
	secondParent := context.WithValue(parent, captureRequestRouteMarker{}, "second-route")
	_, first, err := cs.HandleViewerOfferRequest(context.Background(), firstParent, 1, "viewer", "first")
	if err != nil || first == nil {
		t.Fatalf("first decorated request: %v", err)
	}
	_, second, err := cs.HandleViewerOfferRequest(context.Background(), secondParent, 2, "viewer", "second")
	if err != nil || second == nil {
		t.Fatalf("new wrapper for same attachment rejected: %v", err)
	}
	winner, _, _ := captureRequestRegistration(cs)
	a, duplicate, err := cs.HandleViewerOfferRequest(context.Background(), firstParent, 2, "viewer", "replay")
	if err == nil || a != "" || duplicate != nil {
		t.Errorf("duplicate epoch accepted through different wrapper: %q %v %v", a, duplicate, err)
	}
	cs.CleanupViewerOffer(duplicate)
	cs.CleanupViewerOffer(first)
	got, count, _ := captureRequestRegistration(cs)
	if got != winner || count != 1 || !r.isCurrentlyRegistered("viewer", fixtureValue[*fakeOfferHandle](winner.relayHandle)) {
		t.Error("wrapper/replay cleanup replaced current viewer")
	}
	if r.calls.Load() != 2 || r.legacyCalls.Load() != 0 {
		t.Errorf("cohort routing request=%d legacy=%d want2,0", r.calls.Load(), r.legacyCalls.Load())
	}
}

func TestCaptureViewerRequestRejectsRemovedBeforePublication(t *testing.T) {
	r := &requestOfferProbe{}
	cs := captureRequestFixture(t, r)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var removed *fakeOfferHandle
	r.offer = func(context.Context, context.Context, uint64, string, string) (string, any, error) {
		removed = r.registerHandleOK("viewer")
		r.CloseViewerIfCurrent(removed)
		cs.removeViewerByRelayHandle("viewer", removed)
		return "already-removed-answer", removed, nil
	}
	a, h, err := cs.HandleViewerOfferRequest(context.Background(), parent, 1, "viewer", "offer")
	if err == nil || a != "" || h == nil || h.relay != removed {
		t.Errorf("removed peer published success: %q %v %v", a, h, err)
	}
	_, count, armed := captureRequestRegistration(cs)
	if count != 0 || !armed {
		t.Errorf("removed peer orphaned capture accounting: viewers=%d grace=%v", count, armed)
	}
	cs.CleanupViewerOffer(h)
}

func TestCaptureViewerRequestStopReleasesListenersAndPreservesActiveSnapshot(t *testing.T) {
	r := &requestOfferProbe{}
	cs := captureRequestFixture(t, r)
	active := r.registerHandleOK("viewer")
	cs.viewers["viewer"] = viewerRegistration{gen: 8, relayHandle: active}
	var stops [2]int
	cs.viewerRequests = map[string]*captureViewerRequest{
		"viewer":  {gen: 9, pending: true, stop: func() bool { stops[0]++; return true }},
		"sibling": {gen: 10, stop: func() bool { stops[1]++; return true }},
	}
	cs.mu.Lock()
	cs.stopViewerRequestsLocked()
	remaining := len(cs.viewerRequests)
	cs.mu.Unlock()
	if remaining != 0 || stops != [2]int{1, 1} {
		t.Errorf("request shutdown retained %d records, stopped listeners %v", remaining, stops)
	}
	reg, count, _ := captureRequestRegistration(cs)
	if count != 1 || reg.gen != 8 || reg.relayHandle != active {
		t.Error("request shutdown destroyed active viewer snapshot needed by stopped notification")
	}
}
