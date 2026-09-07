package webrtc

import (
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplacementSequenceHasNoArtificialLoss(t *testing.T) {
	for _, last := range []uint16{0, 1, 32767, 65534, 65535} {
		var mark atomic.Uint32
		mark.Store(uint32(last))
		rewrite := seqRewriter{lastOut: &mark}
		for i, in := range []uint16{510, 511, 514, 512, 515} {
			// Next feed begins at last+1; missing513 and reordered512 retain
			// their original positions rather than being assigned arrival order.
			want := last + 1 + []uint16{0, 1, 4, 2, 5}[i]
			if got := rewrite.rewrite(in); got != want {
				t.Fatalf("last=%d input=%d got=%d want=%d", last, in, got, want)
			}
		}
	}
}

func TestMediaReplacementRejectsRetiredPacketsAndTranslatesClock(t *testing.T) {
	var f mediaForwarder
	now := time.Unix(100, 0)
	var packets []rtp.Packet
	write := func(p *rtp.Packet) error { packets = append(packets, *p); return nil }
	f.begin(1, 90000)
	f.write(1, &rtp.Packet{Header: rtp.Header{SequenceNumber: 60000, Timestamp: 9000}}, now, write)
	f.begin(2, 90000)
	if accepted, err := f.write(1, &rtp.Packet{Header: rtp.Header{SequenceNumber: 60001, Timestamp: 12000}}, now, write); accepted || err != nil {
		t.Fatalf("retired write accepted=%v error=%v", accepted, err)
	}
	f.write(2, &rtp.Packet{Header: rtp.Header{SequenceNumber: 50, Timestamp: 700000}}, now.Add(time.Second), write)
	f.write(2, &rtp.Packet{Header: rtp.Header{SequenceNumber: 52, Timestamp: 706000}}, now.Add(time.Second+time.Second/15), write)
	f.write(2, &rtp.Packet{Header: rtp.Header{SequenceNumber: 51, Timestamp: 703000}}, now.Add(2*time.Second), write)
	want := []rtp.Header{{SequenceNumber: 1, Timestamp: 9000}, {SequenceNumber: 2, Timestamp: 99000}, {SequenceNumber: 4, Timestamp: 105000}, {SequenceNumber: 3, Timestamp: 102000}}
	if len(packets) != len(want) {
		t.Fatalf("packets=%d want%d", len(packets), len(want))
	}
	for i, p := range packets {
		if !reflect.DeepEqual(p.Header, want[i]) {
			t.Fatalf("packet%d header=%+v want=%+v", i, p.Header, want[i])
		}
	}
	sr := &rtcp.SenderReport{RTPTime: 709000, NTPTime: 123, SSRC: 17}
	var report *rtcp.SenderReport
	if f.senderReport(1, sr, func(r *rtcp.SenderReport) { report = r }) || report != nil {
		t.Fatal("retired sender report reached viewer")
	}
	if !f.senderReport(2, sr, func(r *rtcp.SenderReport) { report = r }) {
		t.Fatal("current report rejected")
	}
	if report.RTPTime != 108000 || report.NTPTime != 123 || report.SSRC != 17 {
		t.Fatalf("translated report=%+v", report)
	}
	if sr.RTPTime != 709000 {
		t.Fatal("source report mutated")
	}
	f.retire()
	if accepted, _ := f.write(2, &rtp.Packet{}, now, write); accepted {
		t.Fatal("retired feed accepts packet")
	}
}

func TestMediaReplacementWaitsForOldWriteBoundary(t *testing.T) {
	var f mediaForwarder
	f.begin(1, 48000)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		f.write(1, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { close(entered); <-release; return nil })
		close(done)
	}()
	<-entered
	switched := make(chan struct{})
	go func() { f.begin(2, 48000); close(switched) }()
	select {
	case <-switched:
		t.Fatal("replacement committed while old writer was inside transport")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-done
	<-switched
	called := false
	if accepted, _ := f.write(1, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { called = true; return nil }); accepted || called {
		t.Fatal("old source writes after replacement commits")
	}
}

func TestEndingFeedRetiresPacketAndReportOwner(t *testing.T) {
	s := &Session{videoFeedID: 9}
	s.videoForward.begin(9, 90000)
	s.videoForward.write(9, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { return nil })
	s.endFeed("test", webrtc.RTPCodecTypeVideo, 9)
	if accepted, _ := s.videoForward.write(9, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { t.Fatal("ended feed wrote packet"); return nil }); accepted {
		t.Fatal("ended feed accepted")
	}
	if s.videoForward.senderReport(9, &rtcp.SenderReport{}, func(*rtcp.SenderReport) { t.Fatal("ended feed wrote report") }) {
		t.Fatal("ended feed report accepted")
	}
}
