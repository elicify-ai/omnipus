package browser

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// Timing must describe the real admission and CDP boundaries, including early
// rejection. Only CDP is replaced at the process boundary; the gates and input
// state remain real. These server-local markers do not align remote clocks.
func TestLiveInputTimingStages(t *testing.T) {
	for _, failCDP := range []bool{false, true} {
		name := "success"
		if failCDP {
			name = "cdp failure"
		}
		t.Run(name, func(t *testing.T) {
			expectedError := errors.New("controlled CDP failure")
			commands := 0
			lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
				commands++
				if failCDP {
					return expectedError
				}
				return nil
			})
			picture := installInputTestPicture(t, lv)
			stages := []string{}
			in := inputWithTestPicture(picture, LiveInput{Kind: "text", Text: "private text", Timing: &LiveInputTimingObserver{Observe: func(stage string) { stages = append(stages, stage) }}})
			err := lv.dispatchInputContext(context.Background(), "viewer", in)
			if failCDP {
				require.ErrorIs(t, err, expectedError)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, commands, "measurement must not add browser round trips")
			require.Equal(t, []string{"live_entry", "tab_gate_wait", "tab_gate_acquired", "input_gate_wait", "input_gate_acquired", "admission_done", "cdp_start", "cdp_done"}, stages)
		})
	}
}

func TestLiveInputTimingStopsAtCanceledGate(t *testing.T) {
	commands := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { commands++; return nil })
	release, err := lv.mgr.acquireLiveTabCommand(context.Background(), lv.sessionID)
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stages := []string{}
	in := LiveInput{Kind: "text", Timing: &LiveInputTimingObserver{Observe: func(stage string) {
		stages = append(stages, stage)
		if stage == "tab_gate_wait" {
			cancel()
		}
	}}}
	err = lv.dispatchInputContext(ctx, "viewer", in)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, commands)
	require.Equal(t, []string{"live_entry", "tab_gate_wait"}, stages)
}

func TestLiveInputTimingIncludesCachedPointerMapping(t *testing.T) {
	var commands []*input.DispatchMouseEventParams
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			mouse, ok := action.(*input.DispatchMouseEventParams)
			require.True(t, ok, "cached mapping adds no metrics command")
			commands = append(commands, mouse)
		}
		return nil
	})
	picture := installInputTestPicture(t, lv)
	lv.mu.Lock()
	lv.cssViewportW, lv.cssViewportH = 800, 600
	lv.mu.Unlock()
	var stages []string
	in := inputWithTestPicture(picture, LiveInput{Kind: "mouse_down", Button: "left", HasXY: true, X: 100, Y: 75, CaptureWidth: 400, CaptureHeight: 300, Timing: &LiveInputTimingObserver{Observe: func(stage string) { stages = append(stages, stage) }}})
	require.NoError(t, lv.dispatchInputContext(context.Background(), "viewer", in))
	require.Len(t, commands, 1)
	require.Equal(t, float64(200), commands[0].X)
	require.Equal(t, float64(150), commands[0].Y)
	require.Equal(t, []string{"live_entry", "tab_gate_wait", "tab_gate_acquired", "input_gate_wait", "input_gate_acquired", "admission_done", "mapping_start", "mapping_done", "cdp_start", "cdp_done"}, stages)
}
