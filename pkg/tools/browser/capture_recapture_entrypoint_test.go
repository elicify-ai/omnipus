package browser

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCaptureRecaptureEntrypointsKeepMeasuredFrame(t *testing.T) {
	for _, mode := range []string{"viewport", "tab", "retired tab"} {
		t.Run(mode, func(t *testing.T) {
			cs, _ := adapterFixture(t)
			original := cs.FrameState()
			type command struct {
				legacy bool
				frame  CaptureFrameState
			}
			commands := make(chan command, 4)
			_, _, err := cs.BindIngestRecaptureContext(context.Background(), func(action string, _ *string, _, _, _ int) error {
				if action == "recapture" {
					commands <- command{legacy: true}
				}
				return nil
			}, func(ctx context.Context, frame CaptureFrameState, current func() bool) error {
				if ctx.Err() != nil || !current() {
					return context.Canceled
				}
				commands <- command{frame: frame}
				return nil
			}, func() {})
			require.NoError(t, err)
			if mode == "viewport" {
				cs.RecaptureAt(800, 600)
			} else {
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				cs.mu.Lock()
				cs.foregroundAssertFn = func(context.Context) bool {
					close(entered)
					<-release
					return true
				}
				cs.mu.Unlock()
				cs.RecaptureForTabChangeAt(800, 600)
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("tab recapture never entered foreground preparation")
				}
				if mode == "retired tab" {
					_, err = cs.BeginFrameTransition("page-b", 640, 480, 1)
					require.NoError(t, err)
				}
				unblock()
				require.Eventually(t, func() bool {
					cs.mu.Lock()
					defer cs.mu.Unlock()
					return !cs.tabChangeRecaptureRunning
				}, time.Second, time.Millisecond, "tab recapture worker did not finish")
			}
			var got []command
			for len(commands) > 0 {
				got = append(got, <-commands)
			}
			if mode == "retired tab" {
				require.Empty(t, got, "old tab preparation issued a command after replacement")
			} else {
				require.Equal(t, []command{{frame: original}}, got, "entry point bypassed measured-frame admission")
			}
		})
	}
}
