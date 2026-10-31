package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// r6GateJoinWorker is used only after this worker was positively observed
// processing the test input (at a checkpoint or at the provider). Its inTurn
// falling edge is not itself completion: processTurn still owes disposition
// settlement. Cancel only the now-idle worker lifetime, then join runLoop's
// done channel, which closes after that settlement and all processTurn defers.
// Unexpected provider calls are recorded and released, never left hanging.
func r6GateJoinWorker(t *testing.T, h *grHarness, worker *sessionWorker) []*grCall {
	t.Helper()
	require.NotNil(t, worker, "SETUP: a real worker must have started this input")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	watchdog := time.NewTimer(grWatchdog)
	defer watchdog.Stop()
	var calls []*grCall
	for {
		select {
		case call := <-h.root.requests:
			calls = append(calls, call)
			h.root.release(call)
		case <-ticker.C:
			if !worker.inTurn.Load() {
				worker.cancel()
			}
		case <-worker.done:
			for {
				select {
				case call := <-h.root.requests:
					calls = append(calls, call)
					h.root.release(call)
				default:
					return calls
				}
			}
		case <-watchdog.C:
			t.Fatal("SETUP: the started worker did not join after its input was released")
			return nil
		}
	}
}

// r6GateWorkerIdle excludes a producer whose body ended but whose ordinary
// execution owner has not retired. It is not used as a start/completion signal.
func r6GateWorkerIdle(h *grHarness, worker *sessionWorker) bool {
	if worker == nil || worker.inTurn.Load() || h.al.ownLiveTurn(h.id) != nil {
		return false
	}
	h.al.admission.mu.Lock()
	defer h.al.admission.mu.Unlock()
	for _, owner := range h.al.admission.activeScopes {
		if owner.execution != nil && owner.execution.claim.SessionID == h.id {
			return false
		}
	}
	return true
}
