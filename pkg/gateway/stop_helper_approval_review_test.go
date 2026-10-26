package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Oracle: Stop/helper approval fix-round-1 requirements 1–5. Every interleaving
// is controlled at a REAL durable Stop callback; no sleeps decide who wins.
func TestStopHelperApprovalReviewR1(t *testing.T) {
	t.Run("original_controls", TestStopOwningChat_CancelsHelperApprovals)
	for _, tree := range []bool{false, true} {
		name := "stale_plain_stop"
		if tree {
			name = "stale_tree_stop"
		}
		t.Run(name, func(t *testing.T) { reviewStaleApprovalStop(t, tree) })
	}
	t.Run("partial_discovery", reviewPartialApprovalDiscovery)
	t.Run("stop_wins", func(t *testing.T) { reviewApprovalRace(t, false) })
	t.Run("approve_wins", func(t *testing.T) { reviewApprovalRace(t, true) })
	t.Run("parent_and_helpers_ask", reviewParentOwnApproval)
	for _, stopped := range []bool{false, true} {
		name := "completed_parent"
		if stopped {
			name = "stopped_parent"
		}
		t.Run(name, func(t *testing.T) { reviewIdleParentApproval(t, stopped) })
	}
}

type reviewApprovalStopGate struct {
	*agent.SteerCanceller
	root    string
	entered chan struct{}
	release chan struct{}
	pause   sync.Once
	unblock sync.Once
}

func (g *reviewApprovalStopGate) StopTurnsWithCause(ctx context.Context, id string, by steer.Principal, tree bool, cause session.StopCause, stop agent.GenerationCancelFunc) (steer.CancelReport, error) {
	return g.SteerCanceller.StopTurnsWithCause(ctx, id, by, tree, cause,
		func(ctx context.Context, selected string, generation int) (agent.GenerationCancelResult, error) {
			if selected == g.root {
				g.pause.Do(func() { close(g.entered); <-g.release })
			}
			return stop(ctx, selected, generation)
		})
}

func reviewPauseStop(t *testing.T, f *stopHelperApprovalFixture, parent string, tree bool) <-chan struct{} {
	t.Helper()
	base, ok := gatewaySteerCanceller(f.al).(*agent.SteerCanceller)
	require.True(t, ok)
	gate := &reviewApprovalStopGate{SteerCanceller: base, root: parent, entered: make(chan struct{}), release: make(chan struct{})}
	setGatewaySteerCanceller(f.al, gate)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.h.handleCancelWithScope(f.wc, parent, tree)
	}()
	t.Cleanup(func() {
		gate.unblock.Do(func() { close(gate.release) })
		select {
		case <-done:
		case <-time.After(cancelTestTurnStartDeadline):
			t.Error("gated Stop did not join during cleanup")
		}
	})
	select {
	case <-gate.entered:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("real Stop never reached its accepted root callback")
	}
	return done
}

func reviewReleaseStop(t *testing.T, f *stopHelperApprovalFixture, done <-chan struct{}) {
	t.Helper()
	gate, ok := gatewaySteerCanceller(f.al).(*reviewApprovalStopGate)
	require.True(t, ok, "Stop canceller must be the installed *reviewApprovalStopGate")
	gate.unblock.Do(func() { close(gate.release) })
	select {
	case <-done:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("accepted Stop did not finish after releasing its callback")
	}
}

func reviewStaleApprovalStop(t *testing.T, tree bool) {
	f := newStopHelperApprovalFixture(t)
	parent := f.newChat(t)
	f.startParent(t, parent)
	child := f.launch(t, parent, "ASK:child.txt")
	old := f.awaitApproval(t, child)
	done := reviewPauseStop(t, f, parent, tree)
	// A separate legitimate Stop and Resume happens after the first Stop
	// accepted its selection, but before its delayed ancestor cleanup.
	f.h.handleCancel(f.wc, child)
	f.awaitState(t, child, session.LifecycleStopped)
	before, err := f.al.GetSessionLifecycleStore().Load(child)
	require.NoError(t, err)
	revived, err := f.al.ReviveStoppedSession(context.Background(), child,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: f.wc.userID}, "ASK:sibling.txt")
	require.NoError(t, err)
	require.True(t, revived)
	fresh := f.awaitApproval(t, child)
	require.NotEqual(t, old.ApprovalID, fresh.ApprovalID)
	require.NotEqual(t, old.TurnID, fresh.TurnID)
	after, err := f.al.GetSessionLifecycleStore().Load(child)
	require.NoError(t, err)
	require.NotEqual(t, before.ExecutionID, after.ExecutionID, "Resume must actually admit a replacement execution")
	reviewReleaseStop(t, f, done)
	require.Equal(t, ApprovalStatePending, f.state(fresh.ApprovalID), "an old Stop must not cancel a resumed helper's NEW Ask")
	require.NotNil(t, f.al.GetActiveTurnBySession(child), "replacement execution must survive the old callback")
	for _, frame := range f.drainFrames(t) {
		assert.NotEqual(t, fresh.ApprovalID, frame.ApprovalId, "new approval must not receive an old Stop resolution")
	}
	ok, gone := f.reg.resolve(fresh.ApprovalID, ApprovalActionApprove, false)
	require.True(t, ok)
	require.False(t, gone)
	f.awaitState(t, child, session.LifecycleCompleted)
	data, err := os.ReadFile(filepath.Join(f.workDir, "sibling.txt"))
	require.NoError(t, err)
	require.Equal(t, "approved helper content\n", string(data))
}

func reviewPartialApprovalDiscovery(t *testing.T) {
	f := newStopHelperApprovalFixture(t)
	parent := f.newChat(t)
	f.startParent(t, parent)
	child := f.launch(t, parent, "ASK:child.txt")
	good := f.awaitApproval(t, child)
	grandchild := f.launch(t, child, "ASK:grandchild.txt")
	bad := f.awaitApproval(t, grandchild)
	store := f.al.GetSessionLifecycleStore()
	// A directory at the journal path is unreadable as a journal on every
	// supported platform, including root CI; no chmod/root false green.
	journal := filepath.Join(store.Dir(), grandchild+".jsonl")
	backup := journal + ".review-backup"
	require.NoError(t, os.Rename(journal, backup))
	require.NoError(t, os.Mkdir(journal, 0o700))
	t.Cleanup(func() {
		require.NoError(t, os.Remove(journal))
		require.NoError(t, os.Rename(backup, journal))
	})
	_, fault := store.Load(grandchild)
	var pathError *os.PathError
	require.ErrorAs(t, fault, &pathError, "positive control: child journal really is unreadable")
	f.h.handleCancel(f.wc, parent)
	require.Equal(t, ApprovalStateDeniedCancel, f.state(good.ApprovalID), "reachable branch must still be cancelled")
	require.Equal(t, ApprovalStatePending, f.state(bad.ApprovalID), "unreadable branch must not be falsely reported cancelled")
	var visible bool
	for _, frame := range reviewDrainErrors(t, f) {
		visible = visible || strings.Contains(frame.Message, "helper sessions could not be listed")
		assert.NotContains(t, frame.Message, journal, "public failure must not leak raw storage paths")
	}
	assert.True(t, visible, "Stop must visibly report incomplete helper approval discovery")
	report, _, outcome, _, stopErr := f.h.requestScopedStop(f.wc, parent, false)
	require.Error(t, stopErr, "original discovery error must be returned, not only logged")
	assert.ErrorIs(t, stopErr, pathError.Err, "retain the real platform-specific storage error")
	assert.True(t, outcome.DescendantWalkIncomplete)
	require.NotEmpty(t, report.Unreachable)
}

func reviewApprovalRace(t *testing.T, approveWins bool) {
	f := newStopHelperApprovalFixture(t)
	parent := f.newChat(t)
	f.startParent(t, parent)
	child := f.launch(t, parent, "ASK:child.txt")
	entry := f.awaitApproval(t, child)
	var executions atomic.Int32
	f.al.SetEventSyncTap(func(event agent.Event) {
		forwardStopTestEvent(f.h, f.wc, event)
		if payload, ok := event.Payload.(agent.ToolExecStartPayload); ok && payload.SessionID == child && payload.Tool == "write_file" {
			executions.Add(1)
		}
	})
	f.drainFrames(t)
	done := reviewPauseStop(t, f, parent, false)
	if approveWins {
		ok, gone := f.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
		require.True(t, ok)
		require.False(t, gone)
		f.awaitState(t, child, session.LifecycleCompleted)
		reviewReleaseStop(t, f, done)
		require.Equal(t, ApprovalStateApproved, f.state(entry.ApprovalID))
		require.EqualValues(t, 1, executions.Load(), "approved tool must execute exactly once")
		data, err := os.ReadFile(filepath.Join(f.workDir, "child.txt"))
		require.NoError(t, err)
		require.Equal(t, "approved helper content\n", string(data))
	} else {
		reviewReleaseStop(t, f, done)
		ok, gone := f.reg.resolve(entry.ApprovalID, ApprovalActionApprove, true)
		require.False(t, ok, "late Approve must be rejected after Stop wins")
		require.True(t, gone, "the terminal request must yield Gone, not accept a new decision")
		f.awaitState(t, child, session.LifecycleStopped)
		require.Equal(t, ApprovalStateDeniedCancel, f.state(entry.ApprovalID))
		require.Zero(t, executions.Load(), "Stop-winning request must never execute")
		_, err := os.Stat(filepath.Join(f.workDir, "child.txt"))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.False(t, f.al.ApprovalGrants().IsAllowed(child, "mia", "write_file", entry.Args))
	}
	notices, allResolutions := reviewDrainStopFrames(t, f)
	var resolutions []generated.ToolApprovalResolvedFrame
	for _, frame := range allResolutions {
		if frame.ApprovalId == entry.ApprovalID {
			resolutions = append(resolutions, frame)
		}
	}
	require.Len(t, resolutions, 1, "one consistent terminal card resolution in either ordering")
	wantState := "denied_cancel"
	if approveWins {
		wantState = "approved"
		var approvedNotice bool
		for _, frame := range notices {
			approvedNotice = approvedNotice || frame.Message == "Stop did not withdraw 1 already-approved tool request(s). Those actions may already have run."
		}
		assert.True(t, approvedNotice, "Stop outcome must disclose that Approve already won")
	}
	require.Equal(t, wantState, resolutions[0].State)
}

func reviewParentOwnApproval(t *testing.T) {
	f := newStopHelperApprovalFixture(t)
	parent := f.newChat(t)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	f.h.msgBus.SetStreamDelegate(f.h)
	go func() { runDone <- f.al.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runDone:
			require.True(t, err == nil || errors.Is(err, context.Canceled), "real human loop shutdown: %v", err)
		case <-time.After(cancelTestTurnStartDeadline):
			t.Error("human loop did not join")
		}
	})
	f.h.handleChatMessage(ctx, "review-parent", parent, "ASK:parent.txt", "mia", nil, "", testHarnessWorkspaceMembershipID, f.wc)
	own := f.awaitApproval(t, parent)
	child := f.launch(t, parent, "ASK:child.txt")
	helper := f.awaitApproval(t, child)
	grandchild := f.launch(t, child, "ASK:grandchild.txt")
	nested := f.awaitApproval(t, grandchild)
	f.drainFrames(t)
	f.h.handleCancel(f.wc, parent)
	for _, entry := range []*approvalEntry{own, helper, nested} {
		require.Equal(t, ApprovalStateDeniedCancel, f.state(entry.ApprovalID), "Stop must cover the parent's own Ask as well as helpers")
		path, ok := entry.Args["path"].(string)
		require.True(t, ok, "approval %s path must be a string, got %T", entry.ApprovalID, entry.Args["path"])
		_, err := os.Stat(filepath.Join(f.workDir, path))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	f.awaitState(t, child, session.LifecycleStopped)
	f.awaitState(t, grandchild, session.LifecycleStopped)
	frames := f.drainFrames(t)
	for _, entry := range []*approvalEntry{own, helper, nested} {
		var count int
		for _, frame := range frames {
			if frame.ApprovalId == entry.ApprovalID && frame.State == "denied_cancel" {
				count++
			}
		}
		require.Equal(t, 1, count, "each owning-chat approval must resolve once")
	}
}

func reviewIdleParentApproval(t *testing.T, stopped bool) {
	f := newStopHelperApprovalFixture(t)
	parent := f.newChat(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	// WAIT:helper's release returns a real ordinary final answer, not a
	// cancellation. The stopped variant uses the real StopSession primitive.
	go func() {
		defer close(done)
		_, runErr = f.al.ProcessScheduled(ctx, "mia", parent, "WAIT:helper", "webchat", parent)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			require.True(t, runErr == nil || errors.Is(runErr, context.Canceled), "parent cleanup exit: %v", runErr)
		case <-time.After(cancelTestTurnStartDeadline):
			t.Error("parent turn did not join cleanup")
		}
	})
	f.awaitProvider(t)
	child := f.launch(t, parent, "ASK:child.txt")
	entry := f.awaitApproval(t, child)
	want := session.LifecycleCompleted
	if stopped {
		res, err := f.al.StopSession(context.Background(), agent.StopRequest{
			SessionID: parent, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: f.wc.userID}, Channel: "web",
			HooksFor: func(string) agent.CancelHooks { return agent.CancelHooks{} },
		})
		require.NoError(t, err)
		require.NoError(t, res.RootErr)
		want = session.LifecycleStopped
	} else {
		close(f.provider.finishHelper)
	}
	select {
	case <-done:
		require.True(t, runErr == nil || errors.Is(runErr, context.Canceled), "parent turn exit: %v", runErr)
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("parent turn did not finish")
	}
	require.Eventually(t, func() bool {
		rec, err := f.al.GetSessionLifecycleStore().Load(parent)
		return err == nil && rec.State == want
	}, cancelTestTurnStartDeadline, 10*time.Millisecond, "SETUP: parent must really be completed/stopped")
	require.Equal(t, ApprovalStatePending, f.state(entry.ApprovalID), "SETUP: helper still genuinely waits")
	f.h.handleCancel(f.wc, parent)
	require.Equal(t, ApprovalStateDeniedCancel, f.state(entry.ApprovalID), "explicit server cancel must withdraw helper Ask even when parent is skipped")
	f.awaitState(t, child, session.LifecycleStopped)
	_, err := os.Stat(filepath.Join(f.workDir, "child.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// not-wire-format: reader output for operational notice text, not an app frame.
type reviewStopNotice struct{ Message string }

func forwardStopTestEvent(h *WSHandler, wc *wsConn, event agent.Event) {
	h.hubSyncTap(event)
	if event.Kind == agent.EventKindNotification {
		(&eventForwardState{h: h, wc: wc}).onNotification(event)
	}
}

func reviewDrainErrors(t *testing.T, f *stopHelperApprovalFixture) []reviewStopNotice {
	t.Helper()
	notices, _ := reviewDrainStopFrames(t, f)
	return notices
}

func reviewDrainStopFrames(t *testing.T, f *stopHelperApprovalFixture) ([]reviewStopNotice, []generated.ToolApprovalResolvedFrame) {
	t.Helper()
	var notices []reviewStopNotice
	var resolutions []generated.ToolApprovalResolvedFrame
	for {
		select {
		case raw := <-f.wc.sendCh:
			var frame generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			switch frame.Type {
			case "error":
				notices = append(notices, reviewStopNotice{Message: frame.Message})
			case "notification":
				var notice generated.NotificationFrame
				require.NoError(t, json.Unmarshal(raw, &notice))
				require.Equal(t, "stop_notice", notice.NotificationType)
				require.NotNil(t, notice.Body)
				notices = append(notices, reviewStopNotice{Message: *notice.Body})
			case "tool_approval_resolved":
				var resolved generated.ToolApprovalResolvedFrame
				require.NoError(t, json.Unmarshal(raw, &resolved))
				resolutions = append(resolutions, resolved)
			}
		default:
			return notices, resolutions
		}
	}
}
