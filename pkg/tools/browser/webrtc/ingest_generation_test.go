package webrtc

import (
	"errors"
	"github.com/pion/rtp"
	"testing"
	"time"
)

func TestMediaReplacementTimestampStaysSeriallyAheadAfterLongIdle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		ticks   uint32
	}{
		{"backward_clock", -time.Second, 1}, {"same_instant", 0, 1}, {"short", time.Second / 10, 9000},
		{"one_second", time.Second, 90000}, {"one_day", 24 * time.Hour, 90000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f mediaForwarder
			now := time.Unix(100, 0)
			sink := func(*rtp.Packet) error { return nil }
			f.begin(1, 90000)
			f.write(1, &rtp.Packet{Header: rtp.Header{SequenceNumber: 1, Timestamp: 105000}}, now, sink)
			f.write(1, &rtp.Packet{Header: rtp.Header{SequenceNumber: 0, Timestamp: 102000}}, now, sink)
			f.begin(2, 90000)
			p := &rtp.Packet{Header: rtp.Header{SequenceNumber: 500, Timestamp: 700000}}
			accepted, err := f.write(2, p, now.Add(tc.elapsed), sink)
			if !accepted || err != nil {
				t.Fatalf("replacement rejected: accepted=%v err=%v", accepted, err)
			}
			if want := uint32(105000) + tc.ticks; p.Timestamp != want {
				t.Fatalf("new boundary=%d want%d (strictly after high-water105000)", p.Timestamp, want)
			}
		})
	}
}

func TestCurrentVideoBoundaryUsesCurrentSuccessfulHighWaterAndRetires(t *testing.T) {
	s := &Session{videoFeedID: 1, videoGeneration: 7, videoTargetID: "tab-A"}
	s.videoForward.begin(1, 90000)
	if _, _, _, ok := s.CurrentVideoBoundary(); ok {
		t.Fatal("unwritten feed has a displayed boundary")
	}
	now := time.Unix(100, 0)
	sink := func(*rtp.Packet) error { return nil }
	s.videoForward.write(1, &rtp.Packet{Header: rtp.Header{Timestamp: 105000}}, now, sink)
	s.videoForward.write(1, &rtp.Packet{Header: rtp.Header{Timestamp: 102000}}, now, sink)
	if g, target, ts, ok := s.CurrentVideoBoundary(); !ok || g != 7 || target != "tab-A" || ts != 105000 {
		t.Fatalf("boundary=(%d,%s,%d,%v) want(7,tab-A,105000,true)", g, target, ts, ok)
	}
	s.videoForward.retire()
	if _, _, _, ok := s.CurrentVideoBoundary(); ok {
		t.Fatal("retired feed still proves display identity")
	}
}

func TestVideoBoundaryFloorSurvivesFailedFirstWriteAndReordering(t *testing.T) {
	s := &Session{videoFeedID: 1, videoGeneration: 7, videoTargetID: "tab-A"}
	now := time.Unix(100, 0)
	sink := func(*rtp.Packet) error { return nil }
	s.videoForward.begin(1, 90000)
	s.videoForward.write(1, &rtp.Packet{Header: rtp.Header{Timestamp: 105000}}, now, sink)
	s.videoFeedID = 2
	s.videoGeneration = 8
	s.videoTargetID = "tab-B"
	s.videoForward.begin(2, 90000)
	failed := func(*rtp.Packet) error { return errors.New("transport write failed") }
	s.videoForward.write(2, &rtp.Packet{Header: rtp.Header{Timestamp: 700000}}, now, failed)
	if _, _, _, ok := s.CurrentVideoBoundary(); ok {
		t.Fatal("failed write claimed successful forwarding")
	}
	// This packet precedes the failed packet in the new source clock. The
	// first new mapped timestamp105001 must remain the proof floor, otherwise
	// an old cached frame at105000 could satisfy the new boundary.
	s.videoForward.write(2, &rtp.Packet{Header: rtp.Header{Timestamp: 697000}}, now, sink)
	if g, target, ts, ok := s.CurrentVideoBoundary(); !ok || g != 8 || target != "tab-B" || ts != 105001 {
		t.Fatalf("floor=(%d,%s,%d,%v) want(8,tab-B,105001,true)", g, target, ts, ok)
	}
	s.videoForward.write(2, &rtp.Packet{Header: rtp.Header{Timestamp: 703000}}, now, sink)
	if _, _, ts, ok := s.CurrentVideoBoundary(); !ok || ts != 108001 {
		t.Fatalf("fresh marker=%d ok%v want108001", ts, ok)
	}
	first, latest, ok := s.videoForward.boundary(2)
	if !ok || first != 105001 || latest != 108001 {
		t.Fatalf("first=%d latest=%d ok%v", first, latest, ok)
	}
}

func TestVideoBoundaryZeroAndTimestampWrap(t *testing.T) {
	s := &Session{videoFeedID: 1, videoGeneration: 1, videoTargetID: "tab"}
	now := time.Unix(100, 0)
	sink := func(*rtp.Packet) error { return nil }
	s.videoForward.begin(1, 90000)
	for _, ts := range []uint32{0xfffffff0, 0xfffffff8, 0, 8} {
		s.videoForward.write(1, &rtp.Packet{Header: rtp.Header{Timestamp: ts}}, now, sink)
		if _, _, got, ok := s.CurrentVideoBoundary(); !ok || got != ts {
			t.Fatalf("marker=%d ok%v want%d including zero and wrap", got, ok, ts)
		}
	}
	s.videoForward.write(1, &rtp.Packet{Header: rtp.Header{Timestamp: 0xfffffff9}}, now, sink)
	if _, _, got, ok := s.CurrentVideoBoundary(); !ok || got != 8 {
		t.Fatalf("reordered pre-wrap packet moved marker to%d ok%v", got, ok)
	}
}
