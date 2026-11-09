package preview

import (
	"context"
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

func TestOfficeRetrySecondStartSharesStopAndDeadline(t *testing.T) {
	for _, mode := range []string{"stop", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			conv, c := fakeOfficeCall(t, t.TempDir(), "retry_81_block")
			defer c.finish()
			base, cancel := context.WithCancel(context.Background())
			defer cancel() // idempotent safety on a failing path: never leave a sleeping start behind
			ctx := base
			want := "stopped"
			if mode == "deadline" {
				ctx = officeRetryExpiredContext{base}
				want = "timed out after 120 s"
			}
			var second officeRetryReceipt
			_, _, conversionErr := officeRetryRunWithOwnerBarriers(t, conv, ctx, c, conv.self, []byte("same authorized input"), ".docx",
				func(t *testing.T, start int, receipt officeRetryReceipt) {
					if start != 2 {
						return
					}
					second = receipt
					assertOfficeRetryRecord(t, receipt)
					require.True(t, processAlive(receipt.PID))
					require.Positive(t, receipt.Child)
					require.True(t, processAlive(receipt.Child), "instrument: real grandchild is alive before cancellation")
					cancel() // Stop / deadline land during the second start
				})
			require.EqualError(t, conversionErr, want)
			require.Eventually(t, func() bool { return !processAlive(second.PID) && !processAlive(second.Child) }, 5*time.Second, 10*time.Millisecond, "T-78: both the second converter and its descendant must terminate")
			require.Len(t, officeRetryReceipts(t, c), 2, "canceling the retry must not start a third converter")
		})
	}
}
