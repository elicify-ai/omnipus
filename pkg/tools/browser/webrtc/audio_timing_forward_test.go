package webrtc

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

func TestAudioTimingForwarderMeasuresBoundariesOutsideLock(t *testing.T) {
	t.Setenv(audioTimingEnv, "1")
	synctest.Test(t, func(t *testing.T) {
		var f mediaForwarder
		f.begin(1, 48000)
		var got []audioTimingSummary
		f.audioTiming = newAudioTiming(func(s audioTimingSummary) {
			done := make(chan struct{})
			go func() { f.mu.Lock(); f.mu.Unlock(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("diagnostic emitter ran under media writer lock")
			}
			got = append(got, s)
		})
		start := time.Now()
		f.mu.Lock()
		go func() { time.Sleep(10 * time.Millisecond); f.mu.Unlock() }()
		sentinel := errors.New("authored writer failure")
		accepted, err := f.write(1, &rtp.Packet{Header: rtp.Header{Timestamp: 1000, SequenceNumber: 1}}, start, func(*rtp.Packet) error { time.Sleep(20 * time.Millisecond); return sentinel })
		if !accepted || err != sentinel {
			t.Fatalf("diagnostic changed write outcome: %v %v", accepted, err)
		}
		f.mu.Lock()
		go func() { time.Sleep(30 * time.Millisecond); f.mu.Unlock() }()
		if !f.senderReport(1, &rtcp.SenderReport{RTPTime: 1000}, func(*rtcp.SenderReport) { time.Sleep(40 * time.Millisecond) }) {
			t.Fatal("report rejected")
		}
		time.Sleep(5*time.Second - time.Since(start))
		f.write(1, &rtp.Packet{Header: rtp.Header{Timestamp: 1960, SequenceNumber: 2}}, time.Now(), func(*rtp.Packet) error { return nil })
		if len(got) != 1 {
			t.Fatalf("real forwarder emitted %d timing windows, want1", len(got))
		}
		want := audioTimingSummary{Packets: 2, Intervals: 1, Reports: 1, ReadCompletionGapTotal: 5 * time.Second, ReadCompletionGapMax: 5 * time.Second, SourceTotal: 20 * time.Millisecond, SourceMax: 20 * time.Millisecond, RTPLockTotal: 10 * time.Millisecond, RTPLockMax: 10 * time.Millisecond, RTPWriteTotal: 20 * time.Millisecond, RTPWriteMax: 20 * time.Millisecond, RTCPLockTotal: 30 * time.Millisecond, RTCPLockMax: 30 * time.Millisecond, RTCPWriteTotal: 40 * time.Millisecond, RTCPWriteMax: 40 * time.Millisecond}
		if got[0] != want {
			t.Fatalf("boundary attribution=%+v want=%+v", got[0], want)
		}
	})
}

func TestAudioTimingRetiredFeedDoesNotRecord(t *testing.T) {
	t.Setenv(audioTimingEnv, "1")
	var f mediaForwarder
	f.begin(2, 48000)
	var got []audioTimingSummary
	f.audioTiming = newAudioTiming(func(s audioTimingSummary) { got = append(got, s) })
	at := time.Unix(100, 0)
	for _, n := range []int{0, 5, 10} {
		if accepted, _ := f.write(1, &rtp.Packet{}, at.Add(time.Duration(n)*time.Second), func(*rtp.Packet) error { t.Error("retired RTP written"); return nil }); accepted {
			t.Error("retired feed accepted")
		}
		if f.senderReport(1, &rtcp.SenderReport{}, func(*rtcp.SenderReport) { t.Error("retired report written") }) {
			t.Error("retired report accepted")
		}
	}
	if len(got) != 0 {
		t.Fatalf("retired feed contaminated diagnostics: %+v", got)
	}
}

// A retired feed is rejected before inspecting its packet, as before diagnostics.
func TestAudioTimingRetiredNilPacketRejectedBeforeInspection(t *testing.T) {
	var f mediaForwarder
	f.begin(2, 48000)
	accepted, err := f.write(1, nil, time.Now(), func(*rtp.Packet) error {
		t.Error("retired writer called")
		return nil
	})
	if accepted || err != nil {
		t.Fatalf("retired nil packet: accepted=%v err=%v", accepted, err)
	}
}
