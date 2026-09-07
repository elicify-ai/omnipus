package browser

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func injectionFrame() CaptureFrameState {
	return CaptureFrameState{CaptureID: "capture-a", Generation: 7, TargetID: "page-a", Width: 756, Height: 413, Scale: 1.25}
}

func TestCaptureInjectionUsesImmutableConfirmedFrame(t *testing.T) {
	frame := injectionFrame()
	script, err := captureInjectionScript("token", "ws://localhost/ingest", "", frame)
	require.NoError(t, err)
	frame.Generation = 8
	frame.TargetID = "page-b"
	frame.Width = 900
	var got map[string]any
	require.True(t, strings.HasPrefix(script, "window.__omnipusCapture = "))
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(script, "window.__omnipusCapture = "), ";")), &got))
	require.Equal(t, map[string]any{"token": "token", "ingestUrl": "ws://localhost/ingest", "stunServer": "", "capture_generation": float64(7), "target_id": "page-a", "expected_width": float64(756), "expected_height": float64(413), "capture_scale": 1.25}, got)
}

func TestCaptureInjectionRejectsInvalidFrame(t *testing.T) {
	cases := []struct {
		name   string
		change func(*CaptureFrameState)
	}{
		{"generation zero", func(f *CaptureFrameState) { f.Generation = 0 }},
		{"generation unsafe", func(f *CaptureFrameState) { f.Generation = 9007199254740992 }},
		{"target blank", func(f *CaptureFrameState) { f.TargetID = " " }},
		{"target oversized", func(f *CaptureFrameState) { f.TargetID = strings.Repeat("x", 129) }},
		{"width zero", func(f *CaptureFrameState) { f.Width = 0 }},
		{"height negative", func(f *CaptureFrameState) { f.Height = -1 }},
		{"width oversized", func(f *CaptureFrameState) { f.Width = 16385 }},
		{"height oversized", func(f *CaptureFrameState) { f.Height = 16385 }},
		{"scale low", func(f *CaptureFrameState) { f.Scale = .99 }},
		{"scale high", func(f *CaptureFrameState) { f.Scale = 4.01 }},
		{"scale NaN", func(f *CaptureFrameState) { f.Scale = math.NaN() }},
		{"scale infinite", func(f *CaptureFrameState) { f.Scale = math.Inf(1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := injectionFrame()
			tc.change(&f)
			s, err := captureInjectionScript("token", "ws://localhost/ingest", "", f)
			require.EqualError(t, err, "capture session: invalid confirmed frame")
			require.Equal(t, "", s)
		})
	}
}

func TestCaptureInjectionAcceptsBoundaryFrames(t *testing.T) {
	for _, f := range []CaptureFrameState{
		{Generation: 1, TargetID: "x", Width: 1, Height: 1, Scale: 1},
		{Generation: 2, TargetID: "xx", Width: 2, Height: 2, Scale: 1.01},
		{Generation: 9007199254740990, TargetID: strings.Repeat("x", 127), Width: 16383, Height: 16383, Scale: 3.99},
		{Generation: 9007199254740991, TargetID: strings.Repeat("x", 128), Width: 16384, Height: 16384, Scale: 4},
	} {
		_, err := captureInjectionScript("token", "ws://localhost/ingest", "", f)
		require.NoError(t, err)
	}
}

func TestEncoderStartupSuccessfulTargetOutlivesCaller(t *testing.T) {
	caller, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	var lifetime context.Context
	ctx, closeTarget, err := runEncoderStartup(caller, context.Background(), func(parent context.Context) (*tabEntry, error) {
		lifetime = parent
		target, cancel := context.WithCancel(parent)
		return &tabEntry{ctx: target, cancel: cancel}, nil
	}, func(ctx context.Context) error { return ctx.Err() })
	require.NoError(t, err)
	require.NotNil(t, closeTarget)
	defer closeTarget()
	cancelCaller()
	require.NoError(t, ctx.Err())
	require.NoError(t, lifetime.Err())
	closeTarget()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.ErrorIs(t, lifetime.Err(), context.Canceled)
}

func TestEncoderStartupCancellationClosesTarget(t *testing.T) {
	for _, stage := range []string{"create", "navigate"} {
		t.Run(stage, func(t *testing.T) {
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{})
			finished := make(chan error, 1)
			var target context.Context
			go func() {
				_, _, err := runEncoderStartup(caller, context.Background(), func(parent context.Context) (*tabEntry, error) {
					var closeTarget context.CancelFunc
					target, closeTarget = context.WithCancel(parent)
					if stage == "create" {
						close(entered)
						<-parent.Done()
					}
					return &tabEntry{ctx: target, cancel: closeTarget}, nil
				}, func(ctx context.Context) error {
					if stage == "navigate" {
						close(entered)
					}
					<-ctx.Done()
					return ctx.Err()
				})
				finished <- err
			}()
			<-entered
			cancel()
			select {
			case err := <-finished:
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, target.Err(), context.Canceled)
			case <-time.After(time.Second):
				t.Fatal("caller cancellation did not stop encoder startup")
			}
		})
	}
}

func TestEncoderStartupNavigationFailureClosesTarget(t *testing.T) {
	failure := errors.New("navigation failed")
	var target context.Context
	_, closeTarget, err := runEncoderStartup(context.Background(), context.Background(), func(parent context.Context) (*tabEntry, error) {
		var cancel context.CancelFunc
		target, cancel = context.WithCancel(parent)
		return &tabEntry{ctx: target, cancel: cancel}, nil
	}, func(context.Context) error { return failure })
	require.ErrorIs(t, err, failure)
	require.Nil(t, closeTarget)
	require.ErrorIs(t, target.Err(), context.Canceled)
}
