// schedules_latch_expired_notice_test.go — the non-chat sibling of
// cancel_latch_expired_notice_test.go. A scheduled (cron) run has no browser
// watching it, so watchDeadline's force-abort hooks (schedules.go) cannot
// surface an expired-unfired pre-registration cancel latch as a WebSocket
// frame — the ONLY possible surface is an operator-visible log line. This
// proves watchDeadline's OnLatchExpired wiring produces exactly that, with
// enough context (session id, owner) to be findable in gateway.log.
//
// Deterministic by construction: a fake scheduledCanceller stands in for the
// real *agent.AgentLoop and invokes hooks.OnLatchExpired synchronously,
// simulating "this force-abort found no active turn and the latch it would
// have armed in its place later expired unconsumed" — the real TTL/timing
// semantics of that scenario are pkg/agent's own contract, already covered by
// cancel_prearm_expiry_hook_test.go. This test's only job is to prove
// watchDeadline's wiring of the hook — the field literal in schedules.go —
// actually logs something honest and identifiable when the hook fires.
package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// latchExpiredFakeCanceller is a scheduledCanceller whose RequestCancel
// simulates a force-abort that found no active turn (Fired:false, Armed:true
// — the real outcome shape logged just above the OnLatchExpired wiring in
// watchDeadline) and whose armed latch later expired with nothing ever
// consuming it. It invokes hooks.OnLatchExpired synchronously with the SAME
// scope/canceller RequestCancel received, exactly as pkg/agent's real
// notifyLatchExpired does asynchronously in production (cancel_prearm.go) —
// watchDeadline's own code does not (and must not) care which goroutine the
// callback runs on, only that its hooks wire it.
type latchExpiredFakeCanceller struct {
	mu       sync.Mutex
	sessions []string
}

func (c *latchExpiredFakeCanceller) RequestCancel(
	_ context.Context,
	scope agent.CancelScope,
	canceller agent.CancelCanceller,
	hooks agent.CancelHooks,
) (agent.CancelOutcome, error) {
	c.mu.Lock()
	c.sessions = append(c.sessions, scope.SessionID)
	c.mu.Unlock()
	if hooks.OnLatchExpired != nil {
		hooks.OnLatchExpired(scope, canceller)
	}
	return agent.CancelOutcome{Fired: false, Armed: true}, nil
}

// TestScheduledRunner_WatchDeadline_OnLatchExpired_LogsHonestWarn is this
// fix's MUTATION TARGET for the cron path: reverting the OnLatchExpired
// field in watchDeadline's agent.CancelHooks literal (schedules.go) makes
// hooks.OnLatchExpired nil, so latchExpiredFakeCanceller's guard skips the
// call entirely — the log file then never contains the expected text, and
// the require.Eventually below times out — a clean, real RED (not a panic).
func TestScheduledRunner_WatchDeadline_OnLatchExpired_LogsHonestWarn(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "watchdeadline-latch-expired.log")

	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cfg := baseConfig()
	cfg.Schedules.RunTimeoutSeconds = 0 // force the per-schedule override below
	r, exec, mb, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	exec.hangUntilCtxDone = true
	canceller := &latchExpiredFakeCanceller{}
	r.canceller = canceller
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	job := &cron.CronJob{
		ID: "jt-latch-expired", Name: "slow", AgentID: "mia", CreatedBy: "alice",
		SessionMode: cron.SessionModeIsolated, TimeoutSeconds: 1, // 1s deadline
		Payload: cron.CronPayload{Message: "work"},
	}

	sid, err := r.RunScheduled(context.Background(), job)
	require.Error(t, err)
	require.NotEmpty(t, sid)

	// watchDeadline's force-abort call runs on its own goroutine, racing
	// RunScheduled's own return (both wake on the same ctx2.Done()) — poll
	// the log file rather than assuming it has already been written.
	require.Eventually(t, func() bool {
		data, readErr := os.ReadFile(logFile)
		return readErr == nil && strings.Contains(string(data), "never actually force-aborted")
	}, 2*time.Second, 20*time.Millisecond,
		"watchDeadline's OnLatchExpired wiring must log an operator-visible Warn "+
			"when a scheduled run's force-abort latch expires unconsumed — there is "+
			"no browser to notify on this path, so the log IS the signal, and "+
			"'deferred' silently becoming 'never enforced' must not be invisible")

	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	logged := string(data)
	assert.Contains(t, logged, sid, "the log must identify which run's session this was")
	assert.Contains(t, logged, "mia", "the log must identify the run's owner")

	// Sanity: the fake canceller really was invoked for this run's session.
	calls := canceller.calls()
	require.NotEmpty(t, calls, "RequestCancel must be called on deadline")
	assert.Equal(t, sid, calls[0])
}

func (c *latchExpiredFakeCanceller) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.sessions))
	copy(out, c.sessions)
	return out
}
