package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// #1765 (validator round 2): a plan created from a chat (a source channel)
// wakes its owner through the async notifier -> processSystemMessage, which
// declares no origin; any other event into a plan-minted session (an async tool
// result, a child's completion) takes the same route. The plan owner and
// supervision sessions are minted without a lifecycle record, so the classifier
// must recognise them as the engine's own, from the plan record, not from the
// entry that happened to wake them.

// withLifecycleStore installs a lifecycle store on the harness loop: without
// one the classifier has nothing to read and returns early (instrument check).
func withLifecycleStore(t *testing.T, h *planWakeHarness) {
	t.Helper()
	h.al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(t.TempDir()))
	require.NotNil(t, h.al.GetSessionLifecycleStore())
	// Production wires the engine onto the loop; the harness does not.
	h.al.SetPlanEngine(h.pe)
}

func planSessionAttendanceLog(t *testing.T, run func()) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "agent.log")
	require.NoError(t, logger.EnableFileLogging(logPath))
	defer logger.DisableFileLogging()
	run()
	logger.DisableFileLogging()
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	return string(data)
}

// Real route: StopPlan -> wakeOwner -> asyncNotifier.Notify -> bus ->
// processSystemMessage -> runAgentLoop -> newTurnStateForAdmission. The
// instrument checks the owner turn really ran and was delivered to the origin
// chat, so a zero count cannot come from a wake that never happened.
func TestOwnerWake_NotifierRoute_NoUnresolvedAncestryError(t *testing.T) {
	h := newPlanWakeHarness(t)
	withLifecycleStore(t, h)
	p := runningPlanWithDoD("p1", "telegram", "chat-1")
	require.NoError(t, h.plans.Create(p))

	text := planSessionAttendanceLog(t, func() {
		_, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web")
		require.NoError(t, err)
		waitForTurn(t, h.owner, 1, "owner wake over the notifier route")
		waitForOutbound(t, h.msgBus, "telegram", "chat-1")
		h.pe.wakeWG.Wait()
	})

	got, err := h.plans.Get("p1")
	require.NoError(t, err)
	require.NotEmpty(t, got.OwnerSessionID, "instrument check: the owner session was minted")
	_, loadErr := h.al.GetSessionLifecycleStore().Load(got.OwnerSessionID)
	require.ErrorIs(t, loadErr, session.ErrLifecycleNotFound, "instrument check: the owner session has no lifecycle record")
	require.NotContains(t, text, ancestryUnresolvedLog,
		"the plan engine's own owner session is not damage, whichever route wakes it")
}

// Any entry into the engine's own sessions, with no declared origin at all.
func TestClassify_EnginePlanSessions_NoDeclaredOrigin_NotDamage(t *testing.T) {
	h := newPlanWakeHarness(t)
	withLifecycleStore(t, h)
	ownerID, err := h.pe.mintPlanSession(testPlanOwnerAgentID, "Plan: p2")
	require.NoError(t, err)
	supID, err := h.pe.mintPlanSession(testPlanOwnerAgentID, "Plan supervision: p2")
	require.NoError(t, err)
	p := runningPlanWithDoD("p2", "telegram", "chat-2")
	p.OwnerSessionID = ownerID
	p.Supervision = &plan.Supervision{SessionID: supID}
	require.NoError(t, h.plans.Create(p))

	for name, id := range map[string]string{"owner": ownerID, "supervision": supID} {
		opts := processOptions{TranscriptSessionID: id}
		text := planSessionAttendanceLog(t, func() { h.al.applyTurnAttendance(&opts) })
		require.NotContains(t, text, ancestryUnresolvedLog, name)
		require.True(t, opts.AutoDenyAsk, name+": stays unattended")
		require.Equal(t, autoDenyTaskPlanReason, opts.askRefusalReason, name+": wording unchanged")
		require.Empty(t, opts.askRefusalCause, name)
	}
}

// Real damage next to a plan engine: a session the engine does not own, an
// engine-session that lost its record AND is not on any plan, and a damaged
// ancestor still log and refuse.
func TestClassify_NotAnEngineSession_StillLogsUnresolved(t *testing.T) {
	h := newPlanWakeHarness(t)
	withLifecycleStore(t, h)
	p := runningPlanWithDoD("p3", "telegram", "chat-3")
	require.NoError(t, h.plans.Create(p))

	chat, err := h.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testPlanOwnerAgentID)
	require.NoError(t, err)
	stray, err := h.pe.mintPlanSession(testPlanOwnerAgentID, "Plan: stray")
	require.NoError(t, err)

	for name, id := range map[string]string{"chat": chat.ID, "stray plan-like session on no plan": stray} {
		opts := processOptions{TranscriptSessionID: id}
		text := planSessionAttendanceLog(t, func() { h.al.applyTurnAttendance(&opts) })
		require.Contains(t, text, ancestryUnresolvedLog, name)
		require.True(t, opts.AutoDenyAsk, name)
		require.Contains(t, opts.askRefusalCause, "lifecycle record unreadable or missing", name)
	}
}
