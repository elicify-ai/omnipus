package webrtc

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

func TestDedicatedWheelContinuationSurvivesSlowAcknowledgement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		var delivered []generated.BrowserInputFrame
		var failures []string
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			if *f.ReliableSeq == 1 {
				close(entered)
				select {
				case <-time.After(1200 * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
			delivered = append(delivered, f)
		}, func(reason string) { failures = append(failures, reason) })
		defer q.close()
		q.setActiveDispatchBudget(2 * time.Second)
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		for seq := 2; seq <= 21; seq++ {
			q.submit(false, pressureWheel(seq, 0, 10))
		}
		time.Sleep(1300 * time.Millisecond)
		synctest.Wait()
		require.Empty(t, failures)
		require.Len(t, delivered, 2)
		require.Equal(t, 10.0, *delivered[0].DeltaY)
		require.Equal(t, 200.0, *delivered[1].DeltaY, "pending meaningful deltas must not be lost")
		require.Equal(t, 1, *delivered[0].ReliableSeq, "active wheel must not be replayed")
		q.submit(false, pressureWheel(22, 0, 10))
		synctest.Wait()
		require.Len(t, delivered, 3, "merged sequence must preserve next input admission")
	})
}

func TestDedicatedWheelContinuationRetainsActiveDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		failures := make(chan string, 1)
		calls := 0
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) { calls++; close(entered); <-ctx.Done() }, func(reason string) { failures <- reason })
		defer q.close()
		q.setActiveDispatchBudget(2 * time.Second)
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		q.submit(false, pressureWheel(2, 0, 10))
		time.Sleep(2*time.Second - time.Nanosecond)
		synctest.Wait()
		select {
		case failure := <-failures:
			t.Fatalf("expiry before active deadline: %s", failure)
		default:
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		require.Equal(t, "reliable input queue expired", <-failures)
		// The pre-channel version asserted the failure COUNT (require.Len
		// == 1); a bare receive only asserts "at least one". Settle the
		// bubble once more and drain: a second, spurious expiry would
		// otherwise pass unnoticed. (A sender blocked on the size-1
		// buffer is durably blocked, so Wait returns and the receive
		// above frees it.)
		synctest.Wait()
		select {
		case extra := <-failures:
			t.Fatalf("expected exactly one expiry, got a second: %s", extra)
		default:
		}
		require.Equal(t, 1, calls)
	})
}

func TestDedicatedWheelContinuationBoundariesRemainStrict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*generated.BrowserInputFrame)
	}{
		{"direction", func(f *generated.BrowserInputFrame) { v := -10.0; f.DeltaY = &v }},
		{"position", func(f *generated.BrowserInputFrame) { v := 121.0; f.X = &v }},
		{"capture", func(f *generated.BrowserInputFrame) {
			v := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			f.CaptureId = &v
		}},
		{"generation", func(f *generated.BrowserInputFrame) { v := 8; f.CaptureGeneration = &v }},
		{"modifiers", func(f *generated.BrowserInputFrame) { v := 2; f.Modifiers = &v }},
		{"click", func(f *generated.BrowserInputFrame) { f.Kind = "mouse_down"; v := 1; f.GestureBarrier = &v }},
		{"key", func(f *generated.BrowserInputFrame) { f.Kind = "key_down"; v := 1; f.GestureBarrier = &v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered := make(chan struct{})
				failures := make(chan string, 1)
				calls := 0
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) { calls++; close(entered); <-ctx.Done() }, func(reason string) { failures <- reason })
				defer q.close()
				q.setActiveDispatchBudget(2 * time.Second)
				q.submit(false, pressureWheel(1, 0, 10))
				<-entered
				next := pressureWheel(2, 0, 10)
				tc.change(&next)
				q.submit(false, next)
				time.Sleep(time.Second - time.Nanosecond)
				synctest.Wait()
				select {
				case failure := <-failures:
					t.Fatalf("expiry before strict deadline: %s", failure)
				default:
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				require.Equal(t, "reliable input queue expired", <-failures)
				// The pre-channel version asserted the failure COUNT (require.Len
				// == 1); a bare receive only asserts "at least one". Settle the
				// bubble once more and drain: a second, spurious expiry would
				// otherwise pass unnoticed. (A sender blocked on the size-1
				// buffer is durably blocked, so Wait returns and the receive
				// above frees it.)
				synctest.Wait()
				select {
				case extra := <-failures:
					t.Fatalf("expected exactly one expiry, got a second: %s", extra)
				default:
				}
				require.Equal(t, 1, calls)
			})
		})
	}
}

func TestDedicatedWheelContinuationLateMixedActionRestoresOldDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		var failures []string
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, _ generated.BrowserInputFrame) { close(entered); <-ctx.Done() }, func(reason string) { failures = append(failures, reason) })
		defer q.close()
		q.setActiveDispatchBudget(2 * time.Second)
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		q.submit(false, pressureWheel(2, 0, 10))
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		require.Empty(t, failures)
		next := pressureWheel(3, 0, 10)
		next.Kind = "key_down"
		barrier := 1
		next.GestureBarrier = &barrier
		q.submit(false, next)
		synctest.Wait()
		require.Equal(t, []string{"reliable input queue expired"}, failures, "mixed input must not inherit wheel-only admission")
	})
}

func TestDedicatedWheelContinuationHeldInputRemainsStrict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		var failures []string
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			if f.Kind == "key_down" {
				return
			}
			close(entered)
			<-ctx.Done()
		}, func(reason string) { failures = append(failures, reason) })
		defer q.close()
		q.setActiveDispatchBudget(2 * time.Second)
		key := pressureWheel(1, 0, 10)
		key.Kind = "key_down"
		barrier := 1
		key.GestureBarrier = &barrier
		q.submit(false, key)
		synctest.Wait()
		first := pressureWheel(2, 0, 10)
		first.GestureBarrier = &barrier
		q.submit(false, first)
		<-entered
		next := pressureWheel(3, 0, 10)
		next.GestureBarrier = &barrier
		q.submit(false, next)
		time.Sleep(time.Second)
		synctest.Wait()
		require.Equal(t, []string{"reliable input queue expired"}, failures)
	})
}
