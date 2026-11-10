package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type b3NotifyRun struct {
	done      chan struct{}
	err       error
	returned  time.Time
	callerErr error
}

func b3StartNotify(f *b3Fixture, ctx context.Context) *b3NotifyRun {
	r := &b3NotifyRun{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		r.err = f.al.asyncNotifier.Notify(ctx, f.event)
		r.callerErr = ctx.Err()
		r.returned = time.Now()
	}()
	return r
}
func b3JoinNotify(t *testing.T, r *b3NotifyRun) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(b3JoinWatchdog):
		t.Fatal("INVALID fixture: Notify producer did not join; watchdog is not behavioral RED")
	}
}

// Reads complete native records only. The final joined reader b3Logs separately
// rejects malformed output; this polling reader never manufactures a marker.
func b3ObserveFailure(t *testing.T, f *b3Fixture, r *b3NotifyRun) (string, time.Time) {
	t.Helper()
	watch := time.NewTimer(b3JoinWatchdog)
	defer watch.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		data, err := os.ReadFile(f.logPath)
		require.NoError(t, err)
		lines := strings.Split(string(data), "\n")
		for _, line := range lines[:len(lines)-1] {
			var row map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &row), "INVALID fixture: malformed complete native record")
			if row["message"] == b3AttemptMessage && row["attempt"] == float64(1) && row["attempt_budget"] == float64(3) {
				return "actual-attempt-1", time.Now()
			}
			if row["message"] == "Failed to publish async notification; result permanently lost" && row["source"] == f.event.SourceKind && row["channel"] == f.event.Channel && row["chat_id"] == f.event.ChatID {
				return "legacy-post-sole-failure", time.Now()
			}
		}
		select {
		case <-tick.C:
		case <-watch.C:
			t.Fatal("INVALID fixture: no positive matching failed-publish record; absent signal is not retry RED")
			return "", time.Time{}
		}
	}
}

func TestAsyncNotifierB3RetryContract(t *testing.T) {
	t.Run("ImmediateSuccessExactNormalizedEnvelope", b3ImmediateSuccess)
	t.Run("RealFirstFailureThenSameOpenBusRepair", b3CausalRepair)
	t.Run("ThreeActualClosedBusFailures", b3ThreeFailures)
	t.Run("CallerDeadlineAfterActualAttemptStillWarns", b3CallerDeadline)
	t.Run("IntentionalPreCanceledValidObserverNoAttempt", b3PreCanceled)
	t.Run("CancelAfterFailureOnlyIfStillInFlight", b3CancelAfterFailure)
	t.Run("EarlyInvalidDestinationKeepsObserverZero", b3InvalidDestination)
}

func b3ImmediateSuccess(t *testing.T) {
	f := b3New(t)
	f.event.Content = strings.Repeat("x", 1024*1024+1)
	f.event.SenderCanonicalID = "system:goal_loop"
	f.event.Metadata = map[string]any{"goal_id": "b3-goal-fixture", "goal_reminder": true}
	require.NoError(t, f.al.asyncNotifier.Notify(context.Background(), f.event))
	messages := b3DrainInbound(f.bus)
	require.Len(t, messages, 1)
	want := f.event.Content[:1024*1024] + "\n... [output truncated, exceeded 1MB]\n"
	assert.Equal(t, bus.InboundMessage{Channel: "system", Sender: bus.SenderInfo{CanonicalID: "system:goal_loop"}, ChatID: f.event.Channel + ":" + f.event.ChatID, Content: want,
		AsyncOriginAgentID: f.event.AgentID, AsyncTranscriptSessionID: f.sid, Metadata: map[string]string{"goal_id": "b3-goal-fixture", "goal_reminder": "true"}}, messages[0])
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, q)
	f.observedMu.Lock()
	observed := append([]AsyncNotifyEvent(nil), f.observed...)
	f.observedMu.Unlock()
	require.Len(t, observed, 1)
	assert.Equal(t, want, observed[0].Content)
	assert.Empty(t, b3Rows(t, f.al, f.sid))
	assert.Empty(t, b3DrainOutbound(f.bus))
	assert.Empty(t, b3Logs(t, f.logPath, b3AttemptMessage))
}

func b3CausalRepair(t *testing.T) {
	f := b3New(t)
	f.event.Metadata = map[string]any{"b3_correlation": "repair-input"}
	b3FillInbound(t, f.bus)
	started := time.Now()
	run := b3StartNotify(f, context.Background())
	var once sync.Once
	repair := func() {
		once.Do(func() {
			select {
			case <-f.bus.InboundChan():
			default:
			}
		})
	}
	t.Cleanup(func() { repair(); b3DrainInbound(f.bus); b3JoinNotify(t, run) })
	signal, observed := b3ObserveFailure(t, f, run)
	// This bound classifies a timely fixture; it is not the handoff oracle.
	// With a full untouched OPEN queue, the second timeout cannot precede 5+1+5s.
	if observed.Sub(started) >= 11*time.Second {
		t.Fatal("INVALID fixture: first-failure observation was too late for the retained causal window")
	}
	require.Equal(t, cap(f.bus.InboundChan()), len(f.bus.InboundChan()), "SETUP: no consumer repaired the queue before the proven failed call")
	repairAt := time.Now()
	repair()
	b3JoinNotify(t, run)
	require.NoError(t, run.callerErr, "SETUP: caller must remain live; own publish timeout is retryable")
	t.Logf("causal classification: signal=%s failed_before_repair=%t repair_before_second_timeout=%t producer_joined=true", signal, !observed.After(repairAt), repairAt.Sub(started) < 11*time.Second)
	assert.NoError(t, run.err, "B3-1: a retryable first failure must not abandon this SAME Notify before its remaining attempts")
	target := []bus.InboundMessage{}
	for _, m := range b3DrainInbound(f.bus) {
		if m.AsyncTranscriptSessionID == f.sid {
			target = append(target, m)
		}
	}
	assert.Len(t, target, 1, "same original Notify must enqueue exactly one target after actual post-failure repair")
	if len(target) == 1 {
		assert.Equal(t, f.event.Channel+":"+f.event.ChatID, target[0].ChatID)
		assert.Equal(t, f.event.Content, target[0].Content)
		assert.Equal(t, f.event.AgentID, target[0].AsyncOriginAgentID)
		assert.Equal(t, map[string]string{"b3_correlation": "repair-input"}, target[0].Metadata)
	}
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, q)
	assert.Empty(t, b3Rows(t, f.al, f.sid))
	assert.Empty(t, b3DrainOutbound(f.bus), "successful retry must not warn about its earlier failed attempt")
}

func b3ThreeFailures(t *testing.T) {
	f := b3New(t)
	f.bus.Close()
	err := f.al.asyncNotifier.Notify(context.Background(), f.event)
	assert.ErrorIs(t, err, bus.ErrBusClosed)
	rows := b3Logs(t, f.logPath, b3AttemptMessage)
	assert.Len(t, rows, 3, "three TOTAL actual failed calls require exactly three post-return diagnostics")
	if len(rows) > 0 {
		b3AssertDiagnostic(t, rows, false, "goal follow-up", f.sid, "", "")
	}
	if len(rows) == 3 {
		firstID := rows[0]["notice_id"]
		for i, row := range rows {
			assert.Equal(t, float64(i+1), row["attempt"])
			assert.Equal(t, firstID, row["notice_id"])
			status := "retry"
			if i == 2 {
				status = "final"
			}
			assert.Equal(t, status, row["status"])
		}
	}
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
	saved := b3AssertSaved(t, f, "This goal follow-up notification did not wake the agent.")
	if len(rows) > 0 {
		assert.Equal(t, saved.ID, rows[0]["notice_id"])
	}
}

func b3CallerDeadline(t *testing.T) {
	f := b3New(t)
	b3FillInbound(t, f.bus)
	// Supplied delivery-context input, not an overall runtime/worker cap.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := f.al.asyncNotifier.Notify(ctx, f.event)
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	legacy := b3Logs(t, f.logPath, "Failed to publish async notification; result permanently lost")
	actual := b3Logs(t, f.logPath, b3AttemptMessage)
	require.Equal(t, 1, len(legacy)+len(actual), "SETUP: a real failed publish must be proven before deadline-warning assertions")
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
	assert.Equal(t, 1, len(f.bus.OutboundChan()), "caller deadline must not erase the permitted origin warning")
	row := b3AssertSaved(t, f, "This goal follow-up notification did not wake the agent.")
	b3AssertOrigin(t, f, row)
	b3DrainInbound(f.bus)
}

func b3PreCanceled(t *testing.T) {
	f := b3New(t)
	b3FillInbound(t, f.bus)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := f.al.asyncNotifier.Notify(ctx, f.event)
	assert.ErrorIs(t, err, context.Canceled, "intentional cancellation must win before any attempted publish")
	assert.Empty(t, b3Logs(t, f.logPath, b3AttemptMessage))
	assert.Empty(t, b3Logs(t, f.logPath, "Failed to publish async notification; result permanently lost"), "pre-cancel must make ZERO actual failed calls, not silently spend a child timeout")
	assert.Empty(t, b3Rows(t, f.al, f.sid))
	assert.Empty(t, b3DrainOutbound(f.bus))
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
	b3DrainInbound(f.bus)
}

func b3CancelAfterFailure(t *testing.T) {
	f := b3New(t)
	b3FillInbound(t, f.bus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := b3StartNotify(f, ctx)
	t.Cleanup(func() { cancel(); b3DrainInbound(f.bus); b3JoinNotify(t, run) })
	signal, _ := b3ObserveFailure(t, f, run)
	select {
	case <-run.done:
		t.Logf("CONTROL_UNEXERCISED: post-failure cancellation phase unavailable; signal=%s producer_already_returned=true", signal)
		assert.NoError(t, run.err, "B3-1 premature return prevented the cancellation cut; this is NOT a claim of in-flight cancellation coverage")
		return
	default:
	}
	cancel()
	b3JoinNotify(t, run)
	assert.ErrorIs(t, run.err, context.Canceled)
	assert.Empty(t, b3Rows(t, f.al, f.sid))
	assert.Empty(t, b3DrainOutbound(f.bus))
	records := b3Logs(t, f.logPath, b3AttemptMessage)
	assert.Len(t, records, 1, "cancellation during the retry wait must prevent a second call")
	n, q := f.counts()
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, q)
	t.Log("post-failure cancellation control: actual_failure_observed=true cancellation_before_return=true producer_joined=true")
}

func b3InvalidDestination(t *testing.T) {
	f := b3New(t)
	f.event.Channel = ""
	err := f.al.asyncNotifier.Notify(context.Background(), f.event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to publish with empty destination")
	n, q := f.counts()
	assert.Equal(t, 0, n)
	assert.Equal(t, 0, q)
	assert.Empty(t, b3DrainInbound(f.bus))
	assert.Empty(t, b3Rows(t, f.al, f.sid))
	assert.Empty(t, b3Logs(t, f.logPath, b3AttemptMessage))
}
