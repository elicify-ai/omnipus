package gateway

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Only the paid-model boundary is controlled. Cancellation is observed on the
// actual context the real running turn passes into its provider.
type fix1CancelProvider struct {
	providers.LLMProvider
	entered, cancelled chan struct{}
	session            string
}

func (p *fix1CancelProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.session = tools.ToolTranscriptSessionID(ctx)
	close(p.entered)
	<-ctx.Done()
	close(p.cancelled)
	return nil, ctx.Err()
}

// fix1StopActualBoundary observes one of two positive runtime acknowledgements:
// the real failed channel TryLock, or this test's actual Stop goroutine blocked
// in GetSessionLifecycleStore's RWMutex.RLock. Stack inspection is passive;
// Gosched only yields while observing it. A deadline is HARNESS, never RED.
func fix1StopActualBoundary(t *testing.T, contended <-chan string, done <-chan struct{}) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case id := <-contended:
			require.Equal(t, "whatsapp", id)
			return "deferred channel guard"
		case <-done:
			t.Fatal("HARNESS: Stop completed without an acknowledged held boundary")
		case <-deadline:
			t.Fatal("HARNESS: Stop reached neither instrumented boundary")
		default:
		}
		stack := make([]byte, 1<<20)
		n := runtime.Stack(stack, true)
		for _, goroutine := range strings.Split(string(stack[:n]), "\n\n") {
			if strings.Contains(goroutine, "TestPairingStop_HeldActualConfigTransactionDoesNotDelayCancelOrFence.func") &&
				strings.Contains(goroutine, "(*AgentLoop).StopSession") &&
				strings.Contains(goroutine, "(*AgentLoop).GetSessionLifecycleStore") &&
				strings.Contains(goroutine, "[sync.RWMutex.RLock]") {
				t.Logf("actual Stop blocked before durable acceptance/cancel:\n%s", goroutine)
				return "config read lock before durable acceptance/cancel"
			}
		}
		runtime.Gosched()
	}
}

// pr-test R7-T1 / DESIGN §3: the real writer owns config-write + the channel
// order, has admitted A, prepared its exact config intent, and is held in the
// real persistence callback. Stop must stamp durable intent and cancel the live
// turn before its deferred BeginStop can acquire the writer's channel guard.
func TestPairingStop_HeldActualConfigTransactionDoesNotDelayCancelOrFence(t *testing.T) {
	f, store, d := orderingWriterFixture(t)
	chat := orderingChat(t, f)
	require.NoError(t, os.MkdirAll(filepath.Join(f.f1277.home, "workspaces"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(f.f1277.home, "workspaces", "ordering.json"), []byte(`{"id":"ordering","name":"Ordering","status":"active","core_team":["mia"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`), 0o600))
	instance, exists := f.al.GetRegistry().GetAgent("mia")
	require.True(t, exists)
	provider := &fix1CancelProvider{LLMProvider: f.provider, entered: make(chan struct{}), cancelled: make(chan struct{})}
	instance.StoreProviderPool(map[string]providers.LLMProvider{"qa1277": provider})
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	turnDone := make(chan struct{})
	go func() {
		defer close(turnDone)
		_, _ = f.al.ProcessScheduled(turnCtx, "mia", chat, "hold at the external model boundary", "webchat", chat)
	}()
	t.Cleanup(func() { cancelTurn(); orderingJoin(t, turnDone) })
	orderingWaitStage(t, provider.entered)
	require.Equal(t, chat, provider.session)
	lifecycle := f.al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle)
	_, err := lifecycle.Load(chat)
	require.NoError(t, err, "instrument: the real running turn has a durable lifecycle")
	held, resume, writerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	d.SaveConfigLocked = func(cfg *config.Config) error {
		close(held)
		<-resume
		return config.SaveConfig(f.f1277.configPath, cfg)
	}
	revision := orderingToolRevision(t, d)
	var writeResult *tools.ToolResult
	writerCtx := tools.WithToolCallID(tools.WithTranscriptSessionID(context.Background(), chat), "held-config-connect")
	go func() {
		defer close(writerDone)
		writeResult = systools.NewChannelConnectTool(d).Execute(writerCtx, map[string]any{"id": "whatsapp", "workspace_id": "ordering", "agent_id": "mia", "expected_revision": revision})
	}()
	t.Cleanup(func() { release(); orderingJoin(t, writerDone) })
	orderingWaitStage(t, held)
	a, exists := store.Latest("whatsapp")
	require.True(t, exists)
	require.Equal(t, channels.PairingPhasePending, a.Phase, "admitted inside the actual still-held config transaction")
	require.Equal(t, chat, a.Owner.SessionID)
	contended := make(chan string, 1)
	store.SetOrderContendedHookForTest(func(id string) { contended <- id })
	stopDone := make(chan struct{})
	var stop agent.StopResult
	var stopErr error
	go func() {
		defer close(stopDone)
		stop, stopErr = f.al.StopSession(context.Background(), agent.StopRequest{SessionID: chat, Channel: "webchat", By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: wu6Owner}})
	}()
	t.Cleanup(func() {
		release()
		orderingJoin(t, writerDone)
		orderingJoin(t, stopDone)
		store.SetOrderContendedHookForTest(nil)
	})
	stage := fix1StopActualBoundary(t, contended, stopDone)
	accepted, err := lifecycle.Load(chat)
	require.NoError(t, err)
	durableBeforeRelease := accepted.StopEffect != nil
	cancelledBeforeRelease := false
	if stage == "deferred channel guard" {
		select {
		case <-provider.cancelled: // actual current provider context, not HooksFor/caller-start
			cancelledBeforeRelease = true
		case <-time.After(10 * time.Second):
			t.Fatal("HARNESS: real model context did not acknowledge cancellation while writer held")
		}
	} else {
		select {
		case <-provider.cancelled:
			cancelledBeforeRelease = true
		default:
		}
	}
	// A wrong positive lock-wait acknowledgement is sufficient to release the
	// writer safely; no timeout is converted into a behavioural failure.
	release()
	orderingJoin(t, writerDone)
	orderingJoin(t, stopDone)
	select {
	case <-provider.cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("HARNESS: real cancellation did not settle after releasing the writer")
	}
	orderingJoin(t, turnDone)
	assert.Equal(t, "deferred channel guard", stage, "Stop must get past durable acceptance/cancel without waiting on the config mutation's al.mu")
	assert.True(t, durableBeforeRelease, "durable Stop acceptance must precede release of the actual config transaction")
	assert.True(t, cancelledBeforeRelease, "real running model context cancellation must precede release of that transaction")
	require.NoError(t, stopErr)
	assert.True(t, stop.Durable)
	require.NotNil(t, writeResult)
	finished, exists := store.Attempt("whatsapp", a.AttemptID)
	require.True(t, exists)
	assert.Equal(t, channels.PairingPhaseTerminal, finished.Phase)
	assert.Equal(t, channels.PairingOutcomeStopped, finished.Outcome)
	assert.False(t, store.IsReady("whatsapp", a.AttemptID), "early exact-operation fence survived the held writer's later commit")
	_, err = store.Activate("whatsapp", a.AttemptID, func(string) func(context.Context) error {
		return func(context.Context) error { t.Error("cancelled operation reached a native effect"); return nil }
	})
	assert.ErrorIs(t, err, channels.ErrPairingConflict)
}
