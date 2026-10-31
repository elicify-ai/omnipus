package preview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The clock is a process-edge input. Expiry is triggered only after the
// actual second child and grandchild are ready, not after a guessed sleep.
// This checks deadline propagation, not the elapsed 120-second constant
// (covered by TestPreviewAdmission_QueuedDeadlineReleasesSlots).
type officeRetryExpiredContext struct{ context.Context }

func (c officeRetryExpiredContext) Err() error {
	if c.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func waitForOfficeRetrySecond(t *testing.T, c *call, done <-chan struct{}, failure *error) officeRetryReceipt {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second) // instrument failure bound only
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	path := filepath.Join(c.dir, "retry-start-2.json")
	for {
		raw, readErr := os.ReadFile(path)
		var receipt officeRetryReceipt
		if readErr == nil && json.Unmarshal(raw, &receipt) == nil {
			return receipt
		}
		select {
		case <-done:
			t.Fatalf("T-78: second converter never started; first attempt returned %v", *failure)
		case <-deadline.C:
			t.Fatal("T-78: second converter readiness was not reached")
		case <-tick.C:
		}
	}
}

func TestOfficeRetrySecondStartSharesStopAndDeadline(t *testing.T) {
	for _, mode := range []string{"stop", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			conv, c := fakeOfficeCall(t, t.TempDir(), "retry_81_block")
			defer c.finish()
			base, cancel := context.WithCancel(context.Background())
			ctx := base
			want := "stopped"
			if mode == "deadline" {
				ctx = officeRetryExpiredContext{base}
				want = "timed out after 120 s"
			}
			finished := make(chan struct{})
			var conversionErr error
			go func() {
				_, _, conversionErr = conv.run(ctx, []byte("same authorized input"), ".docx")
				close(finished)
			}()
			defer func() { cancel(); <-finished }()
			receipt := waitForOfficeRetrySecond(t, c, finished, &conversionErr)
			assertOfficeRetryRecord(t, receipt, conv.self)
			require.True(t, processAlive(receipt.PID))
			require.Positive(t, receipt.Child)
			require.True(t, processAlive(receipt.Child), "instrument: real grandchild is alive before cancellation")
			cancel()
			select {
			case <-finished:
				require.EqualError(t, conversionErr, want)
			case <-time.After(5 * time.Second):
				t.Fatal("T-78: cancellation did not end the second start")
			}
			require.Eventually(t, func() bool { return !processAlive(receipt.PID) && !processAlive(receipt.Child) }, 5*time.Second, 10*time.Millisecond, "T-78: both the second converter and its descendant must terminate")
			require.Len(t, officeRetryReceipts(t, c), 2, "canceling the retry must not start a third converter")
		})
	}
}
