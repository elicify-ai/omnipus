package browser

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCaptureRecaptureKeepsOriginalAdmission(t *testing.T) {
	for _, retirement := range []string{"frame", "binding", "request", "capture"} {
		t.Run(retirement, func(t *testing.T) {
			cs, _ := adapterFixture(t)
			frame := cs.FrameState()
			request, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var accepted atomic.Int32
			_, _, err := cs.BindIngestRecaptureContext(context.Background(), func(string, *string, int, int, int) error { return nil },
				func(ctx context.Context, got CaptureFrameState, current func() bool) error {
					close(entered)
					<-release
					if ctx.Err() != nil || !current() {
						return context.Canceled
					}
					accepted.Add(1)
					return nil
				}, func() {})
			require.NoError(t, err)
			result := make(chan bool, 1)
			go func() { result <- cs.RecaptureFrameContext(request, frame) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				close(release)
				t.Fatal("recapture never reached transport admission")
			}
			switch retirement {
			case "frame":
				_, err = cs.BeginFrameTransition("page-b", 640, 480, 1)
			case "binding":
				adapterBind(t, cs, context.Background())
			case "request":
				cancel()
			case "capture":
				cs.Stop()
			}
			close(release)
			require.NoError(t, err)
			select {
			case admitted := <-result:
				require.False(t, admitted, "retired recapture reported acceptance")
			case <-time.After(time.Second):
				t.Fatal("retired recapture did not finish")
			}
			require.Equal(t, int32(0), accepted.Load(), "retired command reached the transport")
		})
	}
}

func TestCaptureRecapturePreservesMeasuredTuple(t *testing.T) {
	cs, _ := adapterFixture(t)
	want := cs.FrameState()
	var got CaptureFrameState
	_, _, err := cs.BindIngestRecaptureContext(context.Background(), func(string, *string, int, int, int) error { return nil },
		func(ctx context.Context, frame CaptureFrameState, current func() bool) error {
			require.NoError(t, ctx.Err())
			require.True(t, current())
			got = frame
			return nil
		}, func() {})
	require.NoError(t, err)
	require.True(t, cs.RecaptureFrameContext(context.Background(), want))
	require.Equal(t, want, got)
}

func TestCaptureRecaptureRejectsPendingGeometry(t *testing.T) {
	cs, _ := adapterFixture(t)
	frame, err := cs.BeginFrameTransition("page-b", 0, 0, 1)
	require.NoError(t, err)
	var calls int
	_, _, err = cs.BindIngestRecaptureContext(context.Background(), func(string, *string, int, int, int) error { return nil },
		func(context.Context, CaptureFrameState, func() bool) error {
			calls++
			return nil
		}, func() {})
	require.NoError(t, err)
	require.False(t, cs.RecaptureFrameContext(context.Background(), frame))
	require.Zero(t, calls, "unknown geometry authorized a transport command")
}
