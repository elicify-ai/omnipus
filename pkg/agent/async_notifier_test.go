// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package-internal (white-box) tests for AsyncNotifier: registerObserver is
// unexported, so tests exercising it must live in `package agent`, not
// `package agent_test` (async-notifier-spec.md TDD Plan, Test Hierarchy note).

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newAsyncNotifierTestLoop builds a minimal, real AgentLoop (no full gateway)
// suitable for exercising AsyncNotifier directly, mirroring the pattern used
// by TestAgentLoop_EmitsFollowUpQueuedEvent (eventbus_test.go) and
// TestRunTurn_ScriptedToolCall_PolicyDeniesAndAudits (scenario_runturn_test.go).
func newAsyncNotifierTestLoop(t *testing.T) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &toolCallProvider{finalResp: "ok"})
	t.Cleanup(al.Close)
	return al, msgBus
}

// TestAsyncNotifier_Notify_PublishesInboundMessage covers Order #1 / FR-N1:
// the basic shape — sender ID composition, channel, chatID, and content are
// all preserved exactly as today's inline asyncCallback produced them.
func TestAsyncNotifier_Notify_PublishesInboundMessage(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)

	err := al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
		Channel:    "slack",
		ChatID:     "C123",
		AgentID:    "test-agent",
		SourceKind: "bash",
		Content:    "build succeeded",
	})
	require.NoError(t, err)

	select {
	case msg := <-msgBus.InboundChan():
		assert.Equal(t, "system", msg.Channel)
		assert.Equal(t, "async:bash", msg.Sender.CanonicalID)
		assert.Equal(t, "slack:C123", msg.ChatID)
		assert.Equal(t, "build succeeded", msg.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Notify to publish an inbound message")
	}
}

// TestAsyncNotifier_Notify_RejectsEmptyChannel covers Order #2 / FR-N7: an
// empty Channel or ChatID is rejected before any publish is attempted (Test
// Dataset "Notify destination validation", rows 1-2).
func TestAsyncNotifier_Notify_RejectsEmptyChannel(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)

	cases := []struct {
		name    string
		channel string
		chatID  string
	}{
		{"empty channel", "", "chat1"},
		{"empty chat id", "slack", ""},
		{"both empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
				Channel:    tc.channel,
				ChatID:     tc.chatID,
				SourceKind: "bash",
				Content:    "should never publish",
			})
			require.Error(t, err, "Notify must reject an ambiguous destination")

			select {
			case msg := <-msgBus.InboundChan():
				t.Fatalf("expected no publish for an ambiguous destination, got %+v", msg)
			case <-time.After(100 * time.Millisecond):
				// Expected: nothing published.
			}
		})
	}
}

// TestAsyncNotifier_Notify_TruncatesLargeContent covers Order #3 / FR-N8,
// using the exact Test Dataset "Content truncation" (async-notifier-spec.md
// lines 319-327) boundary values: rows 1-2 (0 bytes, 1 byte — added as part
// of the ADR-036 fix-wave test-coverage review; the smallest case previously
// tested was an arbitrary 500 bytes, leaving the dataset's own boundary rows
// 1/2 untested), row 3 (exactly the 1,048,576-byte cap, untouched), row 4
// (cap+1, truncated), and row 5 (10x cap, truncated) — matching
// pkg/tools/session.go's maxOutputBufferSize/outputTruncateMarker convention
// exactly (NOT the unrelated 50,000-byte pkg/tools/web.go defaultMaxChars).
func TestAsyncNotifier_Notify_TruncatesLargeContent(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)

	cases := []struct {
		name          string
		size          int
		wantTruncated bool
	}{
		{"0 bytes (empty, dataset row 1)", 0, false},
		{"1 byte (min, dataset row 2)", 1, false},
		{"500 bytes untouched", 500, false},
		{"exactly cap untouched", asyncNotifyMaxContentBytes, false},
		{"cap+1 truncated", asyncNotifyMaxContentBytes + 1, true},
		{"10x cap truncated", asyncNotifyMaxContentBytes * 10, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Repeat("a", tc.size)
			err := al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
				Channel:    "slack",
				ChatID:     "C1",
				SourceKind: "bash",
				Content:    content,
			})
			require.NoError(t, err)

			select {
			case msg := <-msgBus.InboundChan():
				if tc.wantTruncated {
					wantLen := asyncNotifyMaxContentBytes + len(asyncNotifyTruncateMarker)
					assert.Equal(t, wantLen, len(msg.Content), "truncated content must equal cap+marker length")
					assert.True(t, strings.HasSuffix(msg.Content, asyncNotifyTruncateMarker),
						"truncated content must end with the truncation marker")
					assert.Equal(t, content[:asyncNotifyMaxContentBytes], msg.Content[:asyncNotifyMaxContentBytes],
						"content up to the cap must be preserved untouched")
				} else {
					assert.Equal(t, tc.size, len(msg.Content), "under-cap content must be published untouched")
					assert.Equal(t, content, msg.Content)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for publish")
			}
		})
	}
}

// TestAsyncNotifier_RegisterObserver_ReceivesEvent covers Order #4 / FR-N4 /
// FR-N5: a registered observer receives a copy of the full structured event.
func TestAsyncNotifier_RegisterObserver_ReceivesEvent(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)

	var mu sync.Mutex
	var received []AsyncNotifyEvent
	al.asyncNotifier.registerObserver(func(evt AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, evt)
	})

	err := al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
		Channel:    "slack",
		ChatID:     "C123",
		SourceKind: "bash",
		Content:    "build succeeded",
	})
	require.NoError(t, err)

	// Drain the corresponding bus publish so the observer assertion below is
	// checked only once Notify has fully run (Notify calls observers
	// synchronously before returning, so this drain is just good hygiene).
	select {
	case <-msgBus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for publish")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, received, 1, "observer must receive exactly one event for one Notify call")
	assert.Equal(t, "bash", received[0].SourceKind)
	assert.Equal(t, "slack", received[0].Channel)
	assert.Equal(t, "C123", received[0].ChatID)
	assert.Equal(t, "build succeeded", received[0].Content)
}

// TestAsyncNotifier_NoObserver_BehaviorUnchanged covers Order #5 / FR-N5: with
// no observer ever registered, Notify's bus-publish behavior is identical to
// TestAsyncNotifier_Notify_PublishesInboundMessage — proving the mechanism is
// zero-cost/behavior-neutral when unused.
func TestAsyncNotifier_NoObserver_BehaviorUnchanged(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	// Deliberately: no registerObserver call anywhere in this test.

	err := al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
		Channel:    "slack",
		ChatID:     "C999",
		SourceKind: "bash",
		Content:    "no observers here",
	})
	require.NoError(t, err)

	select {
	case msg := <-msgBus.InboundChan():
		assert.Equal(t, "system", msg.Channel)
		assert.Equal(t, "async:bash", msg.Sender.CanonicalID)
		assert.Equal(t, "slack:C999", msg.ChatID)
		assert.Equal(t, "no observers here", msg.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for publish")
	}
}

// TestAsyncNotifier_ObserverPanic_Recovered covers Order #6 / FR-N6: a
// panicking observer is recovered, does not crash the caller, does not block
// the core bus-publish step, and does not prevent OTHER observers from
// running.
func TestAsyncNotifier_ObserverPanic_Recovered(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)

	var secondCalled atomic.Bool
	al.asyncNotifier.registerObserver(func(AsyncNotifyEvent) {
		panic("boom: simulated observer failure")
	})
	al.asyncNotifier.registerObserver(func(AsyncNotifyEvent) {
		secondCalled.Store(true)
	})

	var notifyErr error
	require.NotPanics(t, func() {
		notifyErr = al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
			Channel:    "slack",
			ChatID:     "C1",
			SourceKind: "bash",
			Content:    "panic test",
		})
	}, "an observer panic must never propagate out of Notify")

	require.NoError(t, notifyErr, "bus publish must still succeed despite an observer panic")
	assert.True(t, secondCalled.Load(), "a later observer must still run after an earlier one panics")

	select {
	case msg := <-msgBus.InboundChan():
		assert.Equal(t, "panic test", msg.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for publish despite observer panic")
	}
}

// dangerousStubToolForNotifier is a test-only tool that records whether
// Execute was called. Named distinctly from scenario_runturn_test.go's
// dangerousStubTool to avoid a package-level redeclaration collision while
// keeping this test file self-contained and independently readable.
type dangerousStubToolForNotifier struct {
	tools.BaseTool
	wasCalled atomic.Bool
}

func (d *dangerousStubToolForNotifier) Name() string { return "dangerous_tool_notifier" }
func (d *dangerousStubToolForNotifier) Description() string {
	return "Dangerous test stub — must never execute (AsyncNotifier capability test)"
}

func (d *dangerousStubToolForNotifier) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (d *dangerousStubToolForNotifier) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (d *dangerousStubToolForNotifier) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	d.wasCalled.Store(true)
	return &tools.ToolResult{ForLLM: "executed — this should never happen", IsError: false}
}

// TestAsyncNotifier_NotificationGrantsNoCapability covers Order #10 / FR-N10:
// a Notify-triggered turn goes through the exact same tool-policy evaluation
// as any other inbound message — AsyncNotifier performs no authorization of
// its own and grants no capability by virtue of how the turn was triggered.
//
// `bash` does not exist as a tool yet (per this wave's scope), so this test
// proves the GENERAL property using the real dispatch path a Notify-produced
// "system" message actually takes: Notify's bus.PublishInbound(...) is drained
// and fed into al.processSystemMessage — the exact function AgentLoop.Run's
// select loop calls for any Channel=="system" inbound message (see
// pkg/agent/loop.go's Run method). A scripted provider tries to call a
// policy-denied tool once the Notify-triggered turn starts; if AsyncNotifier
// (or the turn it triggers) special-cased "this came from Notify" to widen
// policy, the stub tool would execute. It must not.
func TestAsyncNotifier_NotificationGrantsNoCapability(t *testing.T) {
	tmpHome := t.TempDir()
	provider := testutil.NewScenario().
		WithToolCall("dangerous_tool_notifier", `{}`).
		WithText("I cannot proceed without that tool.")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpHome,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpHome}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	stub := &dangerousStubToolForNotifier{}
	al.RegisterTool(stub)

	// Deny dangerous_tool_notifier on every agent — same mechanism the TOCTOU
	// re-check (resolveToolPolicyAtExec) enforces for a normal user message.
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				"dangerous_tool_notifier": "deny",
			},
		})
	}

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "default agent must exist after boot")

	// This is the exact call a real bash/delegate producer would make (this
	// wave's scope: AsyncNotifier itself, not those producers). It goes
	// through the identical Notify -> bus.PublishInbound path tested above.
	err := al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
		Channel:    "cli",
		ChatID:     "direct",
		AgentID:    defaultAgent.ID,
		SourceKind: "bash",
		Content:    "please run dangerous_tool_notifier for me",
	})
	require.NoError(t, err)

	var msg bus.InboundMessage
	select {
	case msg = <-msgBus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Notify to publish the inbound message")
	}
	require.Equal(t, "system", msg.Channel, "Notify must publish on the system channel, same as today")

	// Drive the message through the EXACT function AgentLoop.Run's dispatch
	// select loop calls for Channel=="system" — no bypass path exists.
	_, err = al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	assert.False(t, stub.wasCalled.Load(),
		"CRITICAL: a Notify-triggered turn executed a policy-denied tool — "+
			"AsyncNotifier must never grant tool capability by virtue of how the turn was triggered")
}

// TestAsyncNotifier_Notify_PublishFailureDoesNotEmitFollowUpQueued covers the
// event-ordering fix: EventKindFollowUpQueued must be emitted only after
// PublishInbound actually succeeds. Before the fix, Notify emitted the event
// unconditionally BEFORE attempting the publish, so a failed publish still
// left the event bus showing "queued" with no compensating correction — a
// false positive any future event-bus consumer (e.g. a Goals feature) would
// see, even though the failure was already correctly logged and returned to
// the caller.
//
// The publish is forced to fail deterministically by closing the message bus
// before calling Notify: MessageBus.Close is guarded by sync.Once (safe to
// call independent of AgentLoop.Close, which never touches an externally
// owned bus — see newAsyncNotifierTestLoop), and publish() checks
// mb.closed.Load() before touching the channel, so PublishInbound returns
// bus.ErrBusClosed immediately with no timing race to manage.
func TestAsyncNotifier_Notify_PublishFailureDoesNotEmitFollowUpQueued(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)

	sub := al.SubscribeEvents(8)
	defer al.UnsubscribeEvents(sub.ID)

	msgBus.Close()

	err := al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
		Channel:    "slack",
		ChatID:     "C-fail",
		SourceKind: "bash",
		Content:    "this must never show as queued",
	})
	require.Error(t, err, "Notify must still surface the publish failure to the caller")

	// Drain the event stream for a bounded window and confirm
	// EventKindFollowUpQueued never arrives for this failed call. A fixed
	// wait (rather than waitForEvent, which asserts presence) is correct
	// here because we are asserting an ABSENCE.
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case evt := <-sub.C:
			if evt.Kind == EventKindFollowUpQueued {
				t.Fatalf(
					"EventKindFollowUpQueued must not be emitted when the publish failed; got payload %+v",
					evt.Payload,
				)
			}
		case <-deadline:
			return
		}
	}
}

// TestAsyncNotifier_ConcurrentNotifyAndRegisterObserver_RaceFree covers Order
// #12: Notify and registerObserver must be safe to call concurrently. Run
// with `go test -race`.
func TestAsyncNotifier_ConcurrentNotifyAndRegisterObserver_RaceFree(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)

	// Drain inbound messages concurrently so PublishInbound never blocks on a
	// full buffer while many goroutines are notifying at once.
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			select {
			case <-msgBus.InboundChan():
			case <-drainCtx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancelDrain()
		<-drainDone
	})

	const numNotifiers = 25
	const numRegistrars = 25
	var wg sync.WaitGroup
	wg.Add(numNotifiers + numRegistrars)

	for i := 0; i < numNotifiers; i++ {
		go func(i int) {
			defer wg.Done()
			_ = al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
				Channel:    "slack",
				ChatID:     fmt.Sprintf("C%d", i),
				SourceKind: "bash",
				Content:    "concurrent notify",
			})
		}(i)
	}
	for i := 0; i < numRegistrars; i++ {
		go func() {
			defer wg.Done()
			al.asyncNotifier.registerObserver(func(AsyncNotifyEvent) {})
		}()
	}

	wg.Wait()
}
