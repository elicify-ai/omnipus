package webrtc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestMediaStatsReceivedAndFailedWritesStayDistinct(t *testing.T) {
	for _, kind := range []string{"video", "audio"} {
		t.Run(kind, func(t *testing.T) {
			s := NewSession(Config{}, nil, nil)
			t.Cleanup(func() { _ = s.Close() })
			f := &s.videoForward
			read := func() (int64, int64) { v := s.Stats(); return v.VideoReceivedPackets, v.VideoForwardFailures }
			if kind == "audio" {
				f = &s.audioForward
				read = func() (int64, int64) { v := s.Stats(); return v.AudioReceivedPackets, v.AudioForwardFailures }
			}
			f.begin(1, 90000)
			delivered := 0
			success := func(*rtp.Packet) error { delivered++; return nil }
			partial := func(*rtp.Packet) error { delivered++; return errors.New("second viewer failed after first received") }
			total := func(*rtp.Packet) error { return errors.New("all viewers failed") }
			for _, writer := range []func(*rtp.Packet) error{success, partial, total} {
				f.write(1, &rtp.Packet{}, time.Now(), writer)
			}
			if got, failed := read(); got != 3 || failed != 2 {
				t.Fatalf("received=%d failed=%d want3,2 despite partial delivery", got, failed)
			}
			f.begin(2, 90000)
			if accepted, _ := f.write(1, &rtp.Packet{}, time.Now(), success); accepted {
				t.Fatal("retired source accepted")
			}
			f.write(2, &rtp.Packet{}, time.Now(), success)
			f.retire()
			f.write(2, &rtp.Packet{}, time.Now(), success)
			if got, failed := read(); got != 4 || failed != 2 {
				t.Fatalf("replacement cumulative received=%d failed=%d want4,2", got, failed)
			}
			if delivered != 3 {
				t.Fatalf("actual successful viewer deliveries=%d want3", delivered)
			}
			other := s.Stats()
			if kind == "video" && (other.AudioReceivedPackets != 0 || other.AudioForwardFailures != 0) {
				t.Fatal("video altered audio counters")
			}
			if kind == "audio" && (other.VideoReceivedPackets != 0 || other.VideoForwardFailures != 0) {
				t.Fatal("audio altered video counters")
			}
		})
	}
}

func TestMediaStatsReceivedVisibleWhileViewerWriteBlocks(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	s.videoForward.begin(1, 90000)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	go func() {
		defer close(done)
		s.videoForward.write(1, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { close(entered); <-release; return errors.New("egress failed") })
	}()
	<-entered
	stats := make(chan Stats, 1)
	go func() { stats <- s.Stats() }()
	select {
	case got := <-stats:
		if got.VideoReceivedPackets != 1 || got.VideoForwardFailures != 0 {
			t.Errorf("blocked-write stats=%+v want received1/failure0", got)
		}
	case <-time.After(time.Second):
		t.Error("stats waited for viewer writer")
	}
	once.Do(func() { close(release) })
	<-done
	got := s.Stats()
	if got.VideoReceivedPackets != 1 || got.VideoForwardFailures != 1 {
		t.Fatalf("completed-write received=%d failed=%d want1,1", got.VideoReceivedPackets, got.VideoForwardFailures)
	}
}

func TestMediaStatsBindingTokenDescribesInstalledPeer(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	_, sdp := admissionOffer(t, s)
	if got := s.Stats().IngestBindingToken; got != 0 {
		t.Fatalf("uninstalled token=%d want0", got)
	}
	first, err := s.BeginIngestBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().IngestBindingToken; got != 0 {
		t.Fatalf("unnegotiated token=%d want0", got)
	}
	if _, err := s.HandleIngestOfferForBinding(context.Background(), first, 1, sdp, 7, "tab"); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().IngestBindingToken; got != first {
		t.Fatalf("installed token=%d want first%d", got, first)
	}
	second, err := s.BeginIngestBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().IngestBindingToken; got != first {
		t.Fatalf("pending binding relabeled installed peer token=%d want%d", got, first)
	}
	if _, err := s.HandleIngestOfferForBinding(context.Background(), second, 1, sdp, 7, "tab"); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().IngestBindingToken; got != second {
		t.Fatalf("replacement installed token=%d want%d", got, second)
	}
}
