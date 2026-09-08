package webrtc_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

func TestIngestGenerationBoundaryWaitsForMediaAndKeepsOfferIdentity(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })
	type boundary struct {
		generation uint64
		target     string
		timestamp  uint32
	}
	events := make(chan boundary, 16)
	sess.SetOnVideoBoundary(func(g uint64, target string, ts uint32) { events <- boundary{g, target, ts} })
	var first boundary
	for index, target := range []string{"duplicate-url-tab-A", "duplicate-url-tab-B"} {
		enc := newFakeEncoder(t, false)
		generation := uint64(17 + index)
		answer, err := sess.HandleIngestOfferForGeneration(nonTrickleOffer(t, enc.pc), generation, target)
		if err != nil {
			t.Fatal(err)
		}
		setAnswer(t, enc.pc, answer)
		select {
		case got := <-events:
			t.Fatalf("boundary before video forwarded: %+v", got)
		case <-time.After(50 * time.Millisecond):
		}
		enc.startPumping(t)
		select {
		case got := <-events:
			if got.generation != generation || got.target != target {
				t.Fatalf("boundary=%+v want generation%d target%s", got, generation, target)
			}
			if index == 0 {
				first = got
			} else if delta := got.timestamp - first.timestamp; delta == 0 || delta >= 0x80000000 {
				t.Fatalf("replacement timestamp%d not serially after%d", got.timestamp, first.timestamp)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("forwarded video never published its capture generation boundary")
		}
		select {
		case got := <-events:
			t.Fatalf("generation emitted more than one boundary: %+v", got)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestIngestGenerationValidationRejectsInvalidIdentityBeforeNegotiation(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })
	for _, generation := range []uint64{0, 9007199254740992, ^uint64(0)} {
		_, err := sess.HandleIngestOfferForGeneration("v=0", generation, "tab")
		if err == nil || err.Error() != "webrtc: capture generation must be a positive safe integer" {
			t.Fatalf("generation%d: %v", generation, err)
		}
	}
	for _, target := range []string{"", strings.Repeat("t", 129)} {
		_, err := sess.HandleIngestOfferForGeneration("v=0", 1, target)
		if err == nil || err.Error() != "webrtc: capture target must contain 1 to 128 bytes" {
			t.Fatalf("target length%d: %v", len(target), err)
		}
	}
	for _, generation := range []uint64{1, 2, 9007199254740990, 9007199254740991} {
		for _, length := range []int{1, 2, 127, 128} {
			_, err := sess.HandleIngestOfferForGeneration("v=0", generation, strings.Repeat("t", length))
			if !errors.Is(err, relay.ErrOfferHasNoUsableCandidates) {
				t.Fatalf("valid identity generation%d length%d must reach SDP candidate validation: %v", generation, length, err)
			}
		}
	}
}
