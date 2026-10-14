package agent

// Regression tests for issue #1306: agent work has no wall-clock time limit.
// A delegated/steered session, a task run and an external-CLI worker end only
// when they finish or are explicitly stopped — never on a timer. Each test
// below fails on the pre-#1306 code (which recorded a 30-minute lifetime on
// every steered edge, derived the run context's deadline from it, and armed
// the external CLI driver with a kill timer).

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

// TestNoTimeLimit_LaunchRecordsNoLifetimeLimit: a delegate launch persists an
// edge with no time limit at all — no "limits" object, no timeout_seconds.
func TestNoTimeLimit_LaunchRecordsNoLifetimeLimit(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	steerer := newTestSteeringSession(t, al, "ws-1")

	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "long work",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-no-limit"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.SteeredBy == nil {
		t.Fatal("SteeredBy = nil, want the steering edge")
	}
	raw, err := json.Marshal(rec.SteeredBy)
	if err != nil {
		t.Fatalf("marshal SteeredBy: %v", err)
	}
	if strings.Contains(string(raw), "limits") || strings.Contains(string(raw), "timeout") {
		t.Fatalf("persisted steering edge carries a time limit: %s (issue #1306: delegated work has none)", raw)
	}
}

// gatedFinalProvider blocks every Chat until release is closed (or its own
// context ends), then answers with a final reply. entered is closed on the
// first call so a test knows the turn is genuinely in flight.
type gatedFinalProvider struct {
	enterOnce sync.Once
	entered   chan struct{}
	release   chan struct{}
	// hadDeadline records whether the first call's context carried a
	// deadline: a stopwatch cannot tell "no limit" from "a longer limit",
	// the context can.
	hadDeadline bool
}

func newGatedFinalProvider() *gatedFinalProvider {
	return &gatedFinalProvider{entered: make(chan struct{}), release: make(chan struct{})}
}

func (p *gatedFinalProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.enterOnce.Do(func() {
		_, p.hadDeadline = ctx.Deadline()
		close(p.entered)
	})
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "finished after a long time"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *gatedFinalProvider) GetDefaultModel() string { return "no-time-limit-test" }

// TestNoTimeLimit_WakeWithExpiredOldBudgetRunsToCompletion is the founder's
// scenario from issue #1306: a delegated child whose old lifetime budget
// would long have expired (created two hours ago; the retired default was 30
// minutes) is woken and keeps working until its provider answers. It is never
// stopped by a timer, and it completes normally with its answer.
func TestNoTimeLimit_WakeWithExpiredOldBudgetRunsToCompletion(t *testing.T) {
	provider := newGatedFinalProvider()
	al, cleanup := newSteerALWithProvider(t, provider)
	defer cleanup()
	mintGenuineBootEpochForLoop(t, al)
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "call-expired-budget")
	if err := al.GetSessionLifecycleStore().Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
		r.CreatedAt = time.Now().Add(-2 * time.Hour) // far past any old lifetime budget
		return nil
	}); err != nil {
		t.Fatalf("Mutate(created_at): %v", err)
	}

	wake := bus.InboundMessage{
		Channel:                  "system",
		ChatID:                   "system:steer:" + child.SessionID,
		Sender:                   bus.SenderInfo{CanonicalID: "test"},
		Content:                  "keep going",
		AsyncTranscriptSessionID: child.SessionID,
		AsyncOriginAgentID:       child.AgentID,
		Metadata:                 map[string]string{"steer_message_id": "wake-no-limit", "steer_generation": "1"},
	}
	done := make(chan error, 1)
	go func() {
		_, err := al.processSystemMessage(context.Background(), wake)
		done <- err
	}()

	select {
	case <-provider.entered:
	case err := <-done:
		t.Fatalf("woken child ended before its provider was reached: err=%v (a stale lifetime budget stopped it)", err)
	case <-time.After(5 * time.Second):
		t.Fatal("woken child never reached its provider")
	}
	if provider.hadDeadline {
		t.Fatal("the woken child's model call carries a deadline — delegated work has no time limit (issue #1306)")
	}
	// The turn is in flight. Nothing time-based may end it: give any timer
	// a chance to fire, then confirm the turn is still running.
	select {
	case err := <-done:
		t.Fatalf("woken child's turn ended while its provider was still working: err=%v (issue #1306: no time limit)", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(provider.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("woken child finished with error %v, want a normal completion", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("woken child did not finish after its provider answered")
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.StopNote != nil {
		t.Fatalf("child carries a stop note %+v after finishing on its own, want none", rec.StopNote)
	}
	if rec.State == session.LifecycleStopped || rec.State == session.LifecycleFailed {
		t.Fatalf("child state = %q after finishing on its own, want neither stopped nor failed", rec.State)
	}
}

// TestNoTimeLimit_TaskExternalCLIRunHasNoDeadline: a task assigned to an
// external-CLI worker is driven with no wall-clock bound — neither a run
// context deadline nor a driver kill timer.
func TestNoTimeLimit_TaskExternalCLIRunHasNoDeadline(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)
	fr, restore := withFakeDriver(t)
	defer restore()
	go func() {
		require.NoError(t, fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "ok"}}))
		require.NoError(t, fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd}))
		fr.Cancel()
	}()

	if _, err := al.processTaskDirect(context.Background(), "ext-agent", "do the task", "task-sess-nolimit", "task:nolimit"); err != nil {
		t.Fatalf("processTaskDirect: %v", err)
	}
	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	if opts[0].TimeoutSeconds != 0 {
		t.Errorf("task's CLI run TimeoutSeconds = %d, want 0 (no kill timer, issue #1306)", opts[0].TimeoutSeconds)
	}
	if dl := fr.RecordedRunDeadlines(); len(dl) != 1 || dl[0] {
		t.Errorf("task's CLI run context deadlines = %v, want [false] (no deadline, issue #1306)", dl)
	}
}
