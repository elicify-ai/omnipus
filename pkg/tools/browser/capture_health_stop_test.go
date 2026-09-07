package browser

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The watchdog can be paused after sampling liveness. A heartbeat or a new
// binding in that interval must win over its obsolete shutdown decision.
func TestCaptureHealthGuardedStopRechecksSample(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change string
		age    time.Duration
		stop   bool
	}{
		{"unchanged stale socket", "", 31 * time.Second, true},
		{"heartbeat after sample", "heartbeat", 31 * time.Second, false},
		{"binding after sample", "binding", 31 * time.Second, false},
		{"exact stale boundary", "", 30 * time.Second, false},
		{"zero timestamp", "zero", 31 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, relay := adapterFixture(t)
			epoch := adapterBind(t, cs, context.Background())
			sampled := time.Unix(100, 0)
			if tc.change == "zero" {
				sampled = time.Time{}
			}
			cs.mu.Lock()
			cs.lastPingAt = sampled
			cs.mu.Unlock()
			// This channel barrier reproduces the precise sample-to-claim race,
			// without depending on timer scheduling or sleeping.
			resume := make(chan struct{})
			result := make(chan bool, 1)
			go func() {
				<-resume
				result <- cs.StopIfIngestHeartbeatStale(epoch, sampled, sampled.Add(tc.age), 30*time.Second)
			}()
			switch tc.change {
			case "heartbeat":
				require.True(t, cs.RecordIngestHeartbeat(epoch, nil))
			case "binding":
				require.NotEqual(t, epoch, adapterBind(t, cs, context.Background()))
				// Equal timestamps cannot disguise changed connection ownership.
				cs.mu.Lock()
				cs.lastPingAt = sampled
				cs.mu.Unlock()
			}
			close(resume)
			select {
			case got := <-result:
				require.Equal(t, tc.stop, got)
			case <-time.After(time.Second):
				t.Fatal("guarded stop blocked")
			}
			wantCloses := 0
			if tc.stop {
				wantCloses = 1
			}
			require.Equal(t, wantCloses, relay.closeCount())
			if tc.stop {
				require.False(t, cs.StopIfIngestHeartbeatStale(epoch, sampled, sampled.Add(tc.age), 30*time.Second))
				require.Equal(t, 1, relay.closeCount(), "shutdown cleanup is exactly once")
			} else {
				select {
				case <-cs.Done():
					t.Fatal("obsolete sample stopped a healthy session")
				default:
				}
			}
		})
	}
}
