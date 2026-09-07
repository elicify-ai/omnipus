package browser

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type contextOfferProbe struct {
	fakeOfferRelay
	calls       atomic.Int64
	legacyCalls atomic.Int64
	offer       func(context.Context, string, string) (string, any, error)
}

func (f *contextOfferProbe) HandleViewerOfferHandleContext(ctx context.Context, id, sdp string) (string, any, error) {
	f.calls.Add(1)
	return f.offer(ctx, id, sdp)
}
func (f *contextOfferProbe) HandleViewerOfferHandle(id, sdp string) (string, any, error) {
	f.legacyCalls.Add(1)
	return f.fakeOfferRelay.HandleViewerOfferHandle(id, sdp)
}

type legacyOfferProbe struct {
	fakeOfferRelay
	offer func(string, string) (string, any, error)
}

func (f *legacyOfferProbe) HandleViewerOfferHandle(id, sdp string) (string, any, error) {
	return f.offer(id, sdp)
}

func captureContextFixture(t *testing.T, r RelaySession) *CaptureSession {
	t.Helper()
	cs, err := NewCaptureSessionWithDeps(nil, "context-test", r, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cs.Stop)
	// Model the already-issued attachment generation. Encoder startup is
	// outside this adapter's relay-signaling boundary.
	cs.viewers["viewer"] = viewerRegistration{gen: 7}
	return cs
}

func TestCaptureViewerContextRejectsBeforeRelay(t *testing.T) {
	for _, which := range []string{"nil", "canceled"} {
		t.Run(which, func(t *testing.T) {
			r := &contextOfferProbe{}
			r.offer = func(context.Context, string, string) (string, any, error) {
				t.Error("context relay called for rejected attachment")
				return "", nil, nil
			}
			cs := captureContextFixture(t, r)
			sibling := r.registerHandleOK("viewer")
			var ctx context.Context
			if which == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			answer, handle, err := cs.HandleViewerOfferContext(ctx, "viewer", "offer", 7)
			if err == nil || (which == "canceled" && !errors.Is(err, context.Canceled)) {
				t.Errorf("rejected context answer=%q error=%v", answer, err)
			}
			if answer != "" || handle != nil {
				t.Errorf("unattempted offer answer=%q handle=%v want empty/nil", answer, handle)
			}
			if _, exists := cs.viewers["viewer"]; exists {
				t.Error("rejected attachment left its capture registration behind")
			}
			if !r.isCurrentlyRegistered("viewer", sibling) {
				t.Error("pre-delegation rejection closed or replaced a sibling relay peer")
			}
			if r.calls.Load() != 0 || r.legacyCalls.Load() != 0 {
				t.Fatalf("rejected attachment called relay context=%d legacy=%d", r.calls.Load(), r.legacyCalls.Load())
			}
		})
	}
}

func TestCaptureViewerContextPreservesPersistentIdentity(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &contextOfferProbe{}
	var got context.Context
	var relayHandle any
	r.offer = func(ctx context.Context, id, sdp string) (string, any, error) {
		got = ctx
		if id != "viewer" || sdp != "exact-offer" {
			t.Errorf("relay arguments=(%q,%q)", id, sdp)
		}
		relayHandle = r.registerHandleOK(id)
		return "context-answer", relayHandle, nil
	}
	cs := captureContextFixture(t, r)
	answer, handle, err := cs.HandleViewerOfferContext(parent, "viewer", "exact-offer", 7)
	if err != nil || answer != "context-answer" || handle == nil {
		t.Fatalf("context answer=%q handle=%v err=%v", answer, handle, err)
	}
	if got != parent || got.Err() != nil {
		t.Fatalf("relay context=%v want exact still-live persistent parent", got)
	}
	if handle.relay != relayHandle || cs.viewers["viewer"].relayHandle != relayHandle {
		t.Fatal("exact relay identity was not recorded")
	}
	if r.calls.Load() != 1 || r.legacyCalls.Load() != 0 {
		t.Fatalf("routing context=%d legacy=%d want1,0", r.calls.Load(), r.legacyCalls.Load())
	}
	cancel()
	if !errors.Is(got.Err(), context.Canceled) {
		t.Fatal("original lifetime did not reach relay context")
	}
}

func TestCaptureViewerContextCanceledCompletionRetainsCleanup(t *testing.T) {
	for _, capability := range []string{"context", "legacy"} {
		t.Run(capability, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			var r RelaySession
			var fake *fakeOfferRelay
			var relayHandle *fakeOfferHandle
			if capability == "context" {
				probe := &contextOfferProbe{}
				fake = &probe.fakeOfferRelay
				r = probe
				probe.offer = func(context.Context, string, string) (string, any, error) {
					relayHandle = probe.registerHandleOK("viewer")
					cancel()
					return "late-success", relayHandle, nil
				}
			} else {
				probe := &legacyOfferProbe{}
				fake = &probe.fakeOfferRelay
				r = probe
				probe.offer = func(string, string) (string, any, error) {
					relayHandle = probe.registerHandleOK("viewer")
					cancel()
					return "late-success", relayHandle, nil
				}
			}
			cs := captureContextFixture(t, r)
			answer, handle, err := cs.HandleViewerOfferContext(parent, "viewer", "offer", 7)
			if !errors.Is(err, context.Canceled) || answer != "" {
				t.Errorf("canceled completion answer=%q err=%v want empty/context.Canceled", answer, err)
			}
			if handle == nil || handle.relay != relayHandle || relayHandle == nil {
				t.Fatalf("cleanup handle=%v want exact attempted relay handle%v", handle, relayHandle)
			}
			if got := cs.viewers["viewer"].relayHandle; got != nil {
				t.Errorf("canceled result was recorded as current relay identity: %v", got)
			}
			cs.CleanupViewerOffer(handle)
			if fake.isCurrentlyRegistered("viewer", relayHandle) {
				t.Fatal("canceled attempt's exact relay handle was not cleaned")
			}
			if _, ok := cs.viewers["viewer"]; ok {
				t.Fatal("canceled capture registration was not cleaned")
			}
		})
	}
}

func TestCaptureViewerContextPreservesNewerRegistration(t *testing.T) {
	r := &contextOfferProbe{}
	cs := captureContextFixture(t, r)
	var newer *fakeOfferHandle
	r.offer = func(context.Context, string, string) (string, any, error) {
		old := r.registerHandleOK("viewer")
		newer = r.registerHandleOK("viewer")
		cs.viewers["viewer"] = viewerRegistration{gen: 8, relayHandle: newer}
		return "old-answer", old, nil
	}
	_, handle, err := cs.HandleViewerOfferContext(context.Background(), "viewer", "old-offer", 7)
	if err != nil {
		t.Fatal(err)
	}
	if reg := cs.viewers["viewer"]; reg.gen != 8 || reg.relayHandle != newer {
		t.Fatalf("old completion replaced newer registration: %+v", reg)
	}
	cs.CleanupViewerOffer(handle)
	if !r.isCurrentlyRegistered("viewer", newer) || cs.viewers["viewer"].relayHandle != newer {
		t.Fatal("old cleanup removed newer registration")
	}
}

func TestCaptureViewerContextLegacyCompatibility(t *testing.T) {
	for _, method := range []string{"legacy", "context"} {
		t.Run(method, func(t *testing.T) {
			r := &fakeRelay{}
			cs := captureContextFixture(t, r)
			var answer string
			var handle *ViewerAttachHandle
			var err error
			if method == "legacy" {
				answer, handle, err = cs.HandleViewerOffer("viewer", "offer", 7)
			} else {
				answer, handle, err = cs.HandleViewerOfferContext(context.Background(), "viewer", "offer", 7)
			}
			if err != nil || answer != "viewer-answer-viewer" || handle == nil {
				t.Fatalf("legacy answer=%q handle=%v err=%v", answer, handle, err)
			}
			cs.CleanupViewerOffer(handle)
			if len(r.closedViewers) != 1 || r.closedViewers[0] != "viewer" {
				t.Fatalf("legacy cleanup=%v", r.closedViewers)
			}
		})
	}
}

func TestCaptureViewerContextPreRejectionPreservesNewerGeneration(t *testing.T) {
	r := &contextOfferProbe{}
	cs := captureContextFixture(t, r)
	sibling := r.registerHandleOK("viewer")
	cs.viewers["viewer"] = viewerRegistration{gen: 8, relayHandle: sibling}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, handle, err := cs.HandleViewerOfferContext(ctx, "viewer", "old-offer", 7)
	if !errors.Is(err, context.Canceled) || handle != nil {
		t.Fatalf("old canceled attempt handle=%v err=%v", handle, err)
	}
	if reg := cs.viewers["viewer"]; reg.gen != 8 || reg.relayHandle != sibling || !r.isCurrentlyRegistered("viewer", sibling) {
		t.Fatal("old pre-rejection removed or replaced the newer registration/peer")
	}
	if r.calls.Load() != 0 || r.legacyCalls.Load() != 0 {
		t.Fatal("pre-rejection delegated to relay")
	}
}
