package webrtc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIngestLossNotifiesBeforeBlockedWriterDrains(t *testing.T) {
	for _, typed := range []bool{false, true} {
		name := "legacy"
		if typed {
			name = "identity-qualified"
		}
		t.Run(name, func(t *testing.T) {
			s, old, unblock := blockedIngestWriter(t, false)
			notified := make(chan struct{}, 1)
			if typed {
				s.SetOnIngestLostForOffer(func(uint64, uint64, uint64, string) { notified <- struct{}{} })
			} else {
				s.SetOnIngestLost(func() { notified <- struct{}{} })
			}
			done := make(chan bool, 1)
			go func() { done <- s.clearIngestIfCurrent("test", old, "failed") }()
			prompt := false
			select {
			case <-notified:
				prompt = true
			case <-time.After(200 * time.Millisecond):
			}
			unblock()
			require.True(t, <-done)
			require.True(t, prompt, "recovery notification must precede the occupied writer drain")
		})
	}
}

func TestIngestLossRetainsInstalledIdentityAndReceipt(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	binding, err := s.BeginIngestBinding(context.Background())
	require.NoError(t, err)
	s.mu.Lock()
	s.ingestOfferID = 3
	s.mu.Unlock()
	pc := replacementPeer(t, s)
	admission := &ingestAdmission{ctx: context.Background(), negotiation: context.Background(), bindingToken: binding, offerID: 3}
	_, err = s.installIngestCandidate(context.Background(), pc, admission, 17, "selected-tab")
	require.NoError(t, err)
	// A newer reservation has not replaced the installed source yet.
	s.mu.Lock()
	s.ingestOfferID = 4
	s.mu.Unlock()
	// Receipt history is deliberately independent of installed media lifetime.
	s.videoForward.receiptMu.Lock()
	s.videoForward.receipt = VideoReceipt{Serial: 41}
	s.videoForward.receiptMu.Unlock()
	type identity struct {
		binding, offer, generation uint64
		target                     string
	}
	notified := make(chan identity, 1)
	legacy := make(chan struct{}, 1)
	s.SetOnIngestLost(func() { legacy <- struct{}{} })
	s.SetOnIngestLostForOffer(func(b, o, g uint64, target string) { notified <- identity{b, o, g, target} })
	require.True(t, s.clearIngestIfCurrent("test", pc, "failed"))
	select {
	case got := <-notified:
		require.Equal(t, identity{binding, 3, 17, "selected-tab"}, got)
	case <-time.After(200 * time.Millisecond):
		t.Error("missing identity-qualified loss callback")
	}
	b, o, g, target, serial := s.InstalledIngestOffer()
	require.Equal(t, identity{binding, 3, 17, "selected-tab"}, identity{b, o, g, target})
	require.Equal(t, uint64(41), serial)
	select {
	case <-legacy:
		t.Error("typed callback must replace legacy delivery")
	default:
	}
}

func TestIngestInstalledSnapshotDoesNotWaitForWriter(t *testing.T) {
	s, _, unblock := blockedIngestWriter(t, false)
	result := make(chan uint64, 1)
	go func() { _, _, _, _, serial := s.InstalledIngestOffer(); result <- serial }()
	prompt := false
	var serial uint64
	select {
	case serial = <-result:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
	unblock()
	if !prompt {
		serial = <-result
	}
	require.True(t, prompt, "installed identity snapshot must not acquire the writer lock")
	require.Equal(t, uint64(1), serial, "accepted ingress receipt exists before the external writer completes")
}
