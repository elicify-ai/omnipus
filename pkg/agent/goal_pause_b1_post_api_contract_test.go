package agent

// Four-case approval: POST-API boundary baselines, never original-source RED.
// The DIFFERENT combined CHECK auditor owns causal BAD -> exact restore proof.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

func TestGoalPauseB1PostAPIBoundaries(t *testing.T) {
	t.Run("MetadataReadErrorNeverGuessedShared", b1PostAPIMetadataReadError)
	t.Run("PreviouslySetClaimDoesNotReFire", func(t *testing.T) {
		for _, status := range []string{"blocked", "waiting_on_user"} {
			t.Run(status, func(t *testing.T) { b1PostAPIRepeatedClaim(t, status) })
		}
	})
}

func b1PostAPIEvents(sub EventSubscription) []Event {
	var events []Event
	for {
		select {
		case event, ok := <-sub.C:
			if !ok {
				return events
			}
			events = append(events, event)
		default:
			return events
		}
	}
}

func b1PostAPIMetadataReadError(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	dir := filepath.Join(home, "sessions")
	writer, err := session.NewUnifiedStore(dir)
	require.NoError(t, err)
	writerClosed := false
	t.Cleanup(func() {
		if !writerClosed {
			require.NoError(t, writer.Close())
		}
	})
	valid, err := writer.NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	fault, err := writer.NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	owner, title := "b1-postapi-known-owner", "B1 post-API fixture chat"
	for _, sid := range []string{valid.ID, fault.ID} {
		require.NoError(t, writer.SetMeta(sid, session.MetaPatch{Owner: &owner, Title: &title}))
	}
	// Close the actual writer BEFORE faulting disk; never falsify its cache.
	identity := filepath.Join(dir, fault.ID, "meta.json")
	original, err := os.ReadFile(identity)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	writerClosed = true
	require.NoError(t, os.WriteFile(identity, []byte("{"), 0600))
	t.Cleanup(func() { require.NoError(t, os.WriteFile(identity, original, 0600)) })
	// Real fresh-reader construction retries the real malformed identity, records
	// its load failure and leaves it absent from its otherwise genuine cache.
	reader, err := session.NewUnifiedStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	require.Equal(t, 1, reader.CacheLoadFailureCount(), "SETUP: actual fresh constructor must positively observe exactly the faulted identity")
	_, readErr := reader.GetMeta(fault.ID)
	var syntax *json.SyntaxError
	require.ErrorAs(t, readErr, &syntax, "SETUP: actual reader must fail JSON decoding, not just lose a session")
	require.False(t, os.IsNotExist(readErr), "SETUP: missing-session fallback is not this metadata-read error category")
	al := &AgentLoop{sharedSessionStore: reader, eventBus: NewEventBus()}
	t.Cleanup(al.eventBus.Close)
	notices := []GoalPausedEvent{}
	al.SetGoalPausedHook(func(event GoalPausedEvent) { notices = append(notices, event) })
	t.Cleanup(func() { al.SetGoalPausedHook(nil) })
	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	goalID, reason := "b1-postapi-metadata-goal", "B1 post-API safe reason"
	al.notifyGoalPaused(valid.ID, goalID, "blocked", reason)
	require.Equal(t, []GoalPausedEvent{{SessionID: valid.ID, GoalID: goalID, AgentID: testDefaultAgentID, ChatTitle: title, Owner: owner, Reason: reason}}, notices, "positive known-owner hook delivery must work before error suppression means anything")
	require.Empty(t, b1PostAPIEvents(sub), "SETUP: valid owner read must not fabricate a read-error event")
	require.Same(t, reader, al.ResolveSessionStore(fault.ID), "SETUP: real non-ENOENT read error must retain the owning store")
	al.notifyGoalPaused(fault.ID, goalID, "blocked", reason)
	require.Len(t, notices, 1, "real unreadable metadata must not invoke the hook as an empty/shared owner")
	// Existing fixed public error surface required by the brief. Its stable copy
	// is frozen BEFORE running; this does not substitute source text for behavior.
	expected := "Goal state could not be read; deliver goal-pause bell warning (notification not sent) deferred: chat ownership metadata could not be read"
	errorsSeen := []ErrorPayload{}
	for _, event := range b1PostAPIEvents(sub) {
		if event.Kind != EventKindError {
			continue
		}
		payload, ok := event.Payload.(ErrorPayload)
		require.True(t, ok)
		if payload.Stage == "goal_record_read" {
			require.Equal(t, fault.ID, event.Meta.SessionKey)
			errorsSeen = append(errorsSeen, payload)
		}
	}
	require.Equal(t, []ErrorPayload{{Stage: "goal_record_read", Code: string(CodeUnknown), Message: expected, SessionID: fault.ID}}, errorsSeen, "actual fixed same-session visible error is required, never just empty capture or a log")
}

func b1PostAPIRepeatedClaim(t *testing.T, status string) {
	h := newGRHarness(t)
	// Existing actual parent admission, not a hand-written Running record or a
	// new UserInitiated bit. No human input occurs between the two unit edges.
	parentRun := h.human(t, "B1 post-API parent holds a real launch")
	parentCall := grAwaitCall(t, h.root, parentRun)
	t.Cleanup(func() {
		h.root.openAll()
		result := grJoin(t, parentRun)
		require.NoError(t, result.err)
		grAwaitOwner(t, parentCall)
	})
	launcher := NewSteerLauncher(h.al)
	launched, err := launcher.Launch(h.ctx, steer.LaunchRequest{SteeringSessionID: h.id, TargetAgentID: grReviewerID, Task: "B1 post-API child fixture", Label: "B1 post-API child", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "b1-postapi-launch"}})
	require.NoError(t, err)
	child, err := h.al.GetSessionLifecycleStore().Load(launched.SessionID)
	require.NoError(t, err)
	require.NotNil(t, child.SteeredBy)
	require.Equal(t, h.id, child.SteeredBy.SteeringSessionID)
	require.Equal(t, session.LifecycleQueued, child.State, "SETUP: launcher-created child stays queued; unit does not invent a child execution")
	// The approved private handler is the unit. A real retained goal/session and
	// actual upward inbox remain its side-effect boundaries, not callback mocks.
	intent := "B1 post-API retained child goal"
	gid := armGoalRecord(t, launched.SessionID, intent, recordedGoalCriteria(intent), 0, time.Now().UTC())
	record, err := resolveGoalRecordStore().Get(gid)
	require.NoError(t, err)
	notices := []GoalPausedEvent{}
	h.al.SetGoalPausedHook(func(event GoalPausedEvent) { notices = append(notices, event) })
	t.Cleanup(func() { h.al.SetGoalPausedHook(nil) })
	sub := h.al.SubscribeEvents(64)
	t.Cleanup(func() { h.al.UnsubscribeEvents(sub.ID) })
	first, second := "B1 post-API first edge reason", "B1 post-API prior-true edge reason"
	gl := agentLoopCheckGoalLoopAfterTurn{al: h.al, ctx: context.Background(), store: h.al.GetSessionStore(), sessionID: launched.SessionID, rec: record, result: &turnResult{}, toolEvidence: first, marker: goalStatusMarker{Present: true, Status: status}}
	if status == "blocked" {
		require.False(t, h.al.goalIsBlocked(gid))
	} else {
		require.False(t, h.al.goalIsWaitingOnUser(gid))
	}
	gl.handleOutcome()
	require.Len(t, notices, 1, "prior-false first edge must positively fire the real approved product hook")
	require.Equal(t, launched.SessionID, notices[0].SessionID)
	require.Equal(t, gid, notices[0].GoalID)
	require.Equal(t, first, notices[0].Reason)
	b1PostAPIClaimAndUpward(t, h, launched.SessionID, gid, status, []string{first})
	if status == "blocked" {
		require.True(t, h.al.goalIsBlocked(gid))
	} else {
		require.True(t, h.al.goalIsWaitingOnUser(gid))
	}
	gl.toolEvidence = second
	gl.handleOutcome()
	require.Len(t, notices, 1, "previously true claim flag must not re-fire the pause hook")
	require.Equal(t, first, notices[0].Reason, "repeat cannot silently rewrite the first hook event")
	b1PostAPIClaimAndUpward(t, h, launched.SessionID, gid, status, []string{first, second})
	states := []GoalStatusChangedPayload{}
	for _, event := range b1PostAPIEvents(sub) {
		if event.Kind == EventKindGoalStatusChanged {
			p, ok := event.Payload.(GoalStatusChangedPayload)
			require.True(t, ok)
			if p.SessionID == launched.SessionID && p.GoalID == gid {
				states = append(states, p)
			}
		}
	}
	require.Len(t, states, 2, "repeat must preserve the actual status publication instead of an early return that merely hides the hook")
	for _, state := range states {
		require.Equal(t, status, state.State)
		require.Equal(t, 0, state.Round)
		require.Equal(t, record.MaxRounds, state.MaxRounds)
	}
}

func b1PostAPIClaimAndUpward(t *testing.T, h *grHarness, childID, gid, status string, reasons []string) {
	t.Helper()
	current, err := goal.NewStore(config.OmnipusHomeDir()).Get(gid)
	require.NoError(t, err)
	require.Equal(t, generated.GoalStateActive, current.State)
	require.Equal(t, childID, current.ActiveSessionID)
	require.Equal(t, 0, current.Round)
	require.Nil(t, current.LatestVerdict)
	require.NotNil(t, current.LatestClaim)
	require.Equal(t, status, string(current.LatestClaim.Status))
	require.Equal(t, reasons[len(reasons)-1], current.LatestClaim.Evidence, "repeat must still record its real claim, not suppress everything to keep hook count low")
	entries, err := h.al.GetMessageInboxStore().Entries(h.id)
	require.NoError(t, err)
	actual := []string{}
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		class, err := session.ClassifySessionMessage(*entry.Message)
		require.NoError(t, err)
		if status == "blocked" && class.Kind == "blocker" {
			msg, err := entry.Message.AsSessionMessageBlocker()
			require.NoError(t, err)
			if msg.SessionId != childID {
				continue
			}
			require.NotNil(t, msg.ParentSessionId)
			require.Equal(t, h.id, *msg.ParentSessionId)
			actual = append(actual, msg.Text)
		} else if status == "waiting_on_user" && class.Kind == "question" {
			msg, err := entry.Message.AsSessionMessageQuestion()
			require.NoError(t, err)
			if msg.SessionId != childID {
				continue
			}
			require.NotNil(t, msg.ParentSessionId)
			require.Equal(t, h.id, *msg.ParentSessionId)
			actual = append(actual, msg.Text)
		}
	}
	require.Equal(t, reasons, actual, "actual upward messages for first and repeated edge must both persist; no mock deliverer or suppressed side effects")
}
