package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestCaptureInputPressureThresholdAndAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		duration   time.Duration
		want       bool
	}{
		{"below threshold", "key_down", 100*time.Millisecond - time.Nanosecond, false},
		{"threshold", "key_down", 100 * time.Millisecond, true},
		{"above threshold", "mouse_move", 100*time.Millisecond + time.Nanosecond, true},
		{"scroll pressure", "wheel", 100 * time.Millisecond, true},
		{"navigation is page work", "navigate", time.Second, false},
		{"reload is page work", "reload", time.Second, false},
		{"unknown kind", "unknown", time.Second, false},
		{"negative duration", "key_up", -time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := &CaptureSession{}
			sent := make(chan string, 1)
			cs.ingestSend = func(action string, _ *string, _, _, _ int) error { sent <- action; return nil }
			accepted := cs.noteInputPressure(tc.kind, tc.duration, time.Unix(100, 0))
			require.Equal(t, tc.want, accepted)
			if tc.want {
				select {
				case action := <-sent:
					require.Equal(t, "input_pressure", action)
				case <-time.After(time.Second):
					t.Fatal("pressure signal missing")
				}
			}
		})
	}
}

func TestCaptureInputPressureWiredToChromeDispatch(t *testing.T) {
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		time.Sleep(110 * time.Millisecond)
		return nil
	})
	picture := installInputTestPicture(t, lv)
	cs := lv.mgr.CaptureSessionForPanel(lv.sessionID)
	sent := make(chan string, 1)
	cs.ingestSend = func(action string, _ *string, _, _, _ int) error { sent <- action; return nil }
	err := lv.dispatchInputContext(context.Background(), "viewer", inputWithTestPicture(picture, LiveInput{Kind: "text", Text: "a"}))
	require.NoError(t, err)
	select {
	case action := <-sent:
		require.Equal(t, "input_pressure", action)
	case <-time.After(time.Second):
		t.Fatal("slow real input dispatch did not reduce video workload")
	}
}

func TestCaptureInputPressureNeverQueuesWhileSendBlocked(t *testing.T) {
	cs := &CaptureSession{}
	entered, release := make(chan struct{}, 2), make(chan struct{})
	defer close(release)
	cs.ingestSend = func(string, *string, int, int, int) error { entered <- struct{}{}; <-release; return nil }
	now := time.Unix(100, 0)
	require.True(t, cs.noteInputPressure("text", time.Second, now))
	<-entered
	require.False(t, cs.noteInputPressure("text", time.Second, now.Add(time.Second-time.Nanosecond)))
	require.False(t, cs.noteInputPressure("text", time.Second, now.Add(time.Hour)), "blocked transport must not accumulate goroutines")
}

func TestCaptureInputPressureRateLimitAfterCompletedSend(t *testing.T) {
	cs := &CaptureSession{}
	sent := make(chan struct{}, 2)
	cs.ingestSend = func(string, *string, int, int, int) error { sent <- struct{}{}; return nil }
	now := time.Unix(100, 0)
	require.True(t, cs.noteInputPressure("wheel", time.Second, now))
	<-sent
	require.Eventually(t, func() bool {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		return !cs.inputPressureSending
	}, time.Second, time.Millisecond)
	require.False(t, cs.noteInputPressure("wheel", time.Second, now.Add(time.Second-time.Nanosecond)))
	require.True(t, cs.noteInputPressure("wheel", time.Second, now.Add(time.Second)))
	<-sent
}

func TestCaptureInputPressureRejectsStoppedOrUnboundCapture(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		cs := &CaptureSession{stopped: stopped}
		if stopped {
			cs.ingestSend = func(string, *string, int, int, int) error { t.Error("stopped capture sent pressure"); return nil }
		}
		require.False(t, cs.noteInputPressure("key_down", time.Second, time.Now()))
	}
}
