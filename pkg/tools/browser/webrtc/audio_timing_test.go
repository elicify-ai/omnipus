package webrtc

import (
	"reflect"
	"testing"
	"time"
)

// Oracle: three authored arrivals at 0ms, 20ms and 5000ms; source
// timestamps advance by 960 ticks per 20ms at Opus's 48000Hz clock.
func TestAudioTimingExactWindowAndReset(t *testing.T) {
	t.Setenv(audioTimingEnv, "1")
	var got []audioTimingSummary
	d := newAudioTiming(func(s audioTimingSummary) { got = append(got, s) })
	if d == nil {
		t.Fatal("explicit diagnostic opt-in did not enable aggregation")
	}
	start := time.Unix(100, 0)
	d.recordRTP(start, 1000, 48000, time.Millisecond, 2*time.Millisecond)
	d.recordRTP(start.Add(20*time.Millisecond), 1960, 48000, 3*time.Millisecond, 4*time.Millisecond)
	d.recordRTCP(start.Add(5*time.Second-time.Nanosecond), 5*time.Millisecond, 6*time.Millisecond)
	if len(got) != 0 {
		t.Fatalf("logged before five-second boundary: %+v", got)
	}
	d.recordRTP(start.Add(5*time.Second), 2920, 48000, 7*time.Millisecond, 8*time.Millisecond)
	want := audioTimingSummary{Packets: 3, Intervals: 2, Reports: 1,
		ReadCompletionGapTotal: 5 * time.Second, ReadCompletionGapMax: 4980 * time.Millisecond,
		SourceTotal: 40 * time.Millisecond, SourceMax: 20 * time.Millisecond,
		RTPLockTotal: 11 * time.Millisecond, RTPLockMax: 7 * time.Millisecond,
		RTPWriteTotal: 14 * time.Millisecond, RTPWriteMax: 8 * time.Millisecond,
		RTCPLockTotal: 5 * time.Millisecond, RTCPLockMax: 5 * time.Millisecond,
		RTCPWriteTotal: 6 * time.Millisecond, RTCPWriteMax: 6 * time.Millisecond}
	if !reflect.DeepEqual(got, []audioTimingSummary{want}) {
		t.Fatalf("aggregate=%+v want=%+v", got, want)
	}
	d.recordRTP(start.Add(5*time.Second+20*time.Millisecond), 3880, 48000, 0, 0)
	if len(got) != 1 {
		t.Fatalf("per-packet logging after flush: %d", len(got))
	}
	d.recordRTCP(start.Add(10*time.Second), 0, 0)
	want = audioTimingSummary{Packets: 1, Intervals: 1, Reports: 1, ReadCompletionGapTotal: 20 * time.Millisecond, ReadCompletionGapMax: 20 * time.Millisecond, SourceTotal: 20 * time.Millisecond, SourceMax: 20 * time.Millisecond}
	if len(got) != 2 || got[1] != want {
		t.Fatalf("window did not reset while preserving source continuity: %+v", got)
	}
}

func TestAudioTimingDisabledHasNoObserver(t *testing.T) {
	for _, value := range []string{"", "0", "true", "garbage"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(audioTimingEnv, value)
			d := newAudioTiming(func(audioTimingSummary) { t.Error("disabled diagnostic emitted") })
			if d != nil {
				t.Fatal("only explicit 1 may enable diagnostic")
			}
			d.recordRTP(time.Unix(100, 0), 0, 48000, 0, 0)
			d.recordRTCP(time.Unix(110, 0), 0, 0)
		})
	}
}

func TestAudioTimingWrapAndReorderDoNotInventSourceGap(t *testing.T) {
	t.Setenv(audioTimingEnv, "1")
	var got []audioTimingSummary
	d := newAudioTiming(func(s audioTimingSummary) { got = append(got, s) })
	if d == nil {
		t.Fatal("diagnostic unavailable")
	}
	start := time.Unix(100, 0)
	d.recordRTP(start, ^uint32(0)-479, 48000, 0, 0)
	d.recordRTP(start.Add(20*time.Millisecond), 480, 48000, 0, 0)
	d.recordRTP(start.Add(21*time.Millisecond), 0, 48000, 0, 0)
	d.recordRTP(start.Add(40*time.Millisecond), 1440, 48000, 0, 0)
	d.recordRTCP(start.Add(5*time.Second), 0, 0)
	if len(got) != 1 {
		t.Fatalf("summaries=%d want1", len(got))
	}
	if got[0].Reordered != 1 || got[0].SourceTotal != 40*time.Millisecond || got[0].SourceMax != 20*time.Millisecond {
		t.Fatalf("wrap/reorder corrupted source progression: %+v", got[0])
	}
}
