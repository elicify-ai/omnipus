package webrtc

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestVideoReceiptRetainsActualPacketIdentityAcrossFeeds(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	f := &s.videoForward
	first := VideoReceipt{BindingToken: 11, Generation: 7, TargetID: "tab-A"}
	second := VideoReceipt{BindingToken: 11, Generation: 8, TargetID: "tab-B"}
	success := func(*rtp.Packet) error { return nil }
	failure := func(*rtp.Packet) error { return errors.New("viewer delivery failed") }
	f.beginWithReceipt(1, 90000, first)
	if got := s.Stats().VideoReceipt; got != (VideoReceipt{}) {
		t.Fatalf("receipt before any packet=%+v", got)
	}
	f.write(1, &rtp.Packet{}, time.Now(), success)
	first.Serial = 1
	if got := s.Stats().VideoReceipt; got != first {
		t.Fatalf("first receipt=%+v want%+v", got, first)
	}
	f.beginWithReceipt(2, 90000, second)
	if got := s.Stats().VideoReceipt; got != first {
		t.Fatalf("new feed relabeled old receipt=%+v want%+v", got, first)
	}
	if accepted, _ := f.write(1, &rtp.Packet{}, time.Now(), success); accepted {
		t.Fatal("retired feed accepted")
	}
	if got := s.Stats().VideoReceipt; got != first {
		t.Fatalf("retired packet changed receipt=%+v", got)
	}
	f.write(2, &rtp.Packet{}, time.Now(), failure)
	second.Serial = 2
	if got := s.Stats().VideoReceipt; got != second {
		t.Fatalf("failed-write receipt=%+v want%+v", got, second)
	}
	f.retire()
	f.write(2, &rtp.Packet{}, time.Now(), success)
	if got := s.Stats().VideoReceipt; got != second {
		t.Fatalf("retirement changed receipt=%+v want%+v", got, second)
	}
	third := VideoReceipt{BindingToken: 12, Generation: 8, TargetID: "tab-B"}
	f.beginWithReceipt(3, 90000, third)
	f.write(3, &rtp.Packet{}, time.Now(), success)
	third.Serial = 3
	if got := s.Stats().VideoReceipt; got != third {
		t.Fatalf("new binding receipt=%+v want%+v", got, third)
	}
}

func TestVideoReceiptVisibleBeforeBlockedWriterCompletes(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	expected := VideoReceipt{BindingToken: 9, Generation: 27, TargetID: "single-repaint", Serial: 1}
	s.videoForward.beginWithReceipt(1, 90000, VideoReceipt{BindingToken: 9, Generation: 27, TargetID: "single-repaint"})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	go func() {
		defer close(done)
		s.videoForward.write(1, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { close(entered); <-release; return errors.New("failed after receipt") })
	}()
	<-entered
	snapshots := make(chan Stats, 1)
	go func() { snapshots <- s.Stats() }()
	select {
	case got := <-snapshots:
		if got.VideoReceipt != expected {
			t.Errorf("blocked-writer receipt=%+v want%+v", got.VideoReceipt, expected)
		}
		if got.VideoPackets != 0 || got.VideoForwardFailures != 0 {
			t.Errorf("blocked writer falsely completed: %+v", got)
		}
	case <-time.After(time.Second):
		t.Error("receipt snapshot waited for external writer")
	}
	once.Do(func() { close(release) })
	<-done
	if got := s.Stats(); got.VideoReceipt != expected || got.VideoForwardFailures != 1 {
		t.Fatalf("final failed writer stats=%+v", got)
	}
}

func TestVideoReceiptConcurrentSnapshotsKeepIdentityCoherent(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := uint64(1); i <= 1000; i++ {
			target := "even"
			if i%2 != 0 {
				target = "odd"
			}
			s.videoForward.beginWithReceipt(int64(i), 90000, VideoReceipt{BindingToken: i + 100, Generation: i, TargetID: target})
			s.videoForward.write(int64(i), &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { return nil })
		}
	}()
	check := func(got VideoReceipt) {
		t.Helper()
		if got.Serial == 0 {
			return
		}
		target := "even"
		if got.Serial%2 != 0 {
			target = "odd"
		}
		if got.BindingToken != got.Serial+100 || got.Generation != got.Serial || got.TargetID != target {
			t.Errorf("torn receipt identity=%+v", got)
		}
	}
	for {
		check(s.Stats().VideoReceipt)
		select {
		case <-done:
			got := s.Stats().VideoReceipt
			check(got)
			if got.Serial != 1000 {
				t.Fatalf("final receipt serial=%d want1000", got.Serial)
			}
			return
		default:
		}
	}
}

func TestVideoReceiptSerialExhaustionDoesNotReuseAnAncientSerial(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	last := VideoReceipt{BindingToken: 9, Generation: 7, TargetID: "old", Serial: ^uint64(0)}
	s.videoForward.receipt = last
	s.videoForward.beginWithReceipt(1, 90000, VideoReceipt{BindingToken: 10, Generation: 8, TargetID: "new"})
	s.videoForward.write(1, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { return nil })
	if got := s.Stats().VideoReceipt; got != last {
		t.Fatalf("exhaustion reused receipt serial: got%+v want retained%+v", got, last)
	}
}
