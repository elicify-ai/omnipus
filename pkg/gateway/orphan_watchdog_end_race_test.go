// Regression coverage for a race between a delegation's REAL end and the WS
// event forwarder's orphan watchdog (pkg/gateway/websocket.go,
// startOrphanWatchdog). Seen as
// TestOrphanWatchdog_GenuinelyActiveDelegate_NeverSynthesizesInterrupted
// failing on a slow CI runner: the delegation's first subagent_end, after its
// gate was released and it completed normally, said "interrupted".
//
// The watchdog asked agent.AgentLoop.IsSubTurnActiveForSpawnCall and, on "not
// active", sent subagent_end{status:"interrupted"} itself. That answer turned
// false BEFORE the real end reached the forwarder — during runTurn's unwind
// (the child is briefly absent from activeTurnStates) and between
// subTurnRecordPersisted and the EventKindSubTurnEnd emission — and even once
// the end event was queued, the forwarder goroutine had not necessarily
// consumed it. A watchdog tick in that gap reported a normally-completing
// delegation as interrupted, ahead of or just after its real success frame.
//
// The fix keeps the span active until its end event has been emitted
// (markSubTurnSpanOpen, pkg/agent/steering.go) and lets only the forwarder
// goroutine synthesize, after it has handled every event already queued
// (orphanFires, websocket.go). This test samples that gap about a thousand
// times a second instead of about seven: the watchdog period is 1ms, the
// reschedule ceiling is lifted so the parked delegate is never force-fired,
// and debug logging — which also proves the watchdog was armed — slows the
// forwarder the way a loaded runner does.

package gateway

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// orphanEndRaceLogBuffer is a goroutine-safe log sink: the watchdog, the
// forwarder and the delegate all log concurrently while the test reads it.
type orphanEndRaceLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *orphanEndRaceLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *orphanEndRaceLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Given a real async Critical delegate that outlives its parent's turn, with
// the orphan watchdog armed and polling every millisecond
// When the delegate is released and completes normally
// Then its only subagent_end frame says "success", and no interrupted end is
// ever synthesized for it — not before its real end, and not after.
func TestOrphanWatchdog_RealEndRacingTheWatchdog_NeverSynthesizesInterrupted(t *testing.T) {
	restoreTimeout := SetOrphanWatchdogTimeoutForTest(time.Millisecond)
	defer restoreTimeout()
	restoreCeiling := SetOrphanWatchdogMaxRechecksForTest(1 << 30)
	defer restoreCeiling()

	logBuf := &orphanEndRaceLogBuffer{}
	oldHandler := slog.Default().Handler()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(slog.New(oldHandler))

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "content-routed-orphan-mock"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// An explicitly registered agent — same reason as
			// TestOrphanWatchdog_GenuinelyActiveDelegate_NeverSynthesizesInterrupted.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	provider := &contentRoutedOrphanProvider{}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)

	unblock := make(chan struct{})
	gate := &orphanGateTool{unblock: unblock, entered: make(chan struct{})}
	al.RegisterTool(gate)

	delegateTool := tools.NewDelegateTool(cfg.Agents.Defaults.DefaultModel.Model, cfg.Agents.Defaults.MaxTokens, 0)
	delegateTool.SetSpawner(agent.NewSubTurnSpawner(al))
	// Same fixture wiring, and the same reasons, as the liveness test above.
	delegateTool.SetLifecycleStore(session.NewLifecycleStore(filepath.Join(tmpDir, "session_lifecycle")))
	delegateTool.SetDelegationDenyCheckerBackground(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)
	al.RegisterTool(delegateTool)
	// Bounded, and the gate is always released first on a failure path (LIFO
	// cleanup) — see the identical note in orphan_watchdog_liveness_test.go.
	releaseGate := sync.OnceFunc(func() { close(unblock) })
	t.Cleanup(func() {
		waitBounded(t, 15*time.Second, "async delegate drain (WaitForAsyncTasks)", delegateTool.WaitForAsyncTasks)
	})
	t.Cleanup(releaseGate)

	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				"delegate":         "allow",
				"orphan_gate_tool": "allow",
			},
		})
	}

	const chatID = "chat-orphan-end-race"
	const sessionKey = "test-session-orphan-watchdog-end-race"

	h := makeMinimalHandlerWithAgentLoop(al)
	wc, ch := makeForwarderTestConn(256)
	attachHubTestConn(t, h, al, chatID, wc)

	parentDone := make(chan struct{})
	var parentErr error
	go func() {
		defer close(parentDone)
		_, parentErr = al.ProcessDirectWithChannel(context.Background(), orphanParentRootTask, sessionKey, "telegram", chatID)
	}()
	select {
	case <-parentDone:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: parent turn never finished")
	}
	require.NoError(t, parentErr)

	select {
	case <-gate.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: delegate never reached its gated tool call")
	}

	// The parent has ended, so the watchdog is armed for the open span. Prove
	// it is really polling before releasing the delegate — otherwise nothing
	// below would be tested.
	require.Eventually(t, func() bool {
		return strings.Contains(logBuf.String(), "span_orphan_recheck_still_alive")
	}, 5*time.Second, 5*time.Millisecond,
		"the orphan watchdog never re-checked the parked delegate — it was not armed, so this test proves nothing")

	releaseGate()

	var frames [][]byte
	deadline := time.After(15 * time.Second)
	for sawEnd := false; !sawEnd; {
		select {
		case raw := <-ch:
			frames = append(frames, raw)
			sawEnd = isSubagentEndFrame(t, raw)
		case <-deadline:
			t.Fatal("BLOCKED: timed out waiting for the delegate's subagent_end after releasing the gate")
		}
	}
	// Let the delegate finish its own cleanup, then collect anything sent
	// after the first end frame too: a synthetic interrupted end arriving
	// just AFTER the real one is the same defect. The hub publishes span
	// frames synchronously on the emitting goroutine and a resolved span can
	// never be synthesized again (synthesizeOrphanEnd re-checks it under
	// spanMu), so nothing can still be in flight once the delegate is done.
	waitBounded(t, 15*time.Second, "async delegate drain (WaitForAsyncTasks)", delegateTool.WaitForAsyncTasks)
	frames = append(frames, drainAllFrames(ch)...)

	var endStatuses []string
	for _, raw := range frames {
		if isSubagentEndFrame(t, raw) {
			endStatuses = append(endStatuses, subagentEndStatus(t, raw))
		}
	}
	require.Equal(t, []string{"success"}, endStatuses,
		"BUG REGRESSION: a delegation that completed normally must produce exactly one subagent_end, with its real "+
			"status — never a synthesized interrupted end racing its real one")
	require.NotContains(t, logBuf.String(), "span_orphan_interrupted",
		"BUG REGRESSION: the orphan watchdog synthesized an interrupted end for a delegation that completed normally")
}
