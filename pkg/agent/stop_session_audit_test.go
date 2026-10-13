package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Every Stop request is audited exactly once as turn.cancel.attempt, whatever
// the session's state. A repeat Stop on a session whose stop already landed (or
// whose run already completed) used to be a durable no-op that never reached
// RequestCancel, so it left no audit row: whether a second Stop on a just-
// finished session was audited depended on whether the first Stop's landing had
// already been written (TestCancel_AuditEventEmitted, 2 of 20 under -race).
func TestStopSession_EveryAttemptIsAuditedOnceWhateverTheSessionState(t *testing.T) {
	states := map[string]*session.LifecycleRecord{
		"landed-stopped": {Generation: 1, State: session.LifecycleStopped, StopNote: &session.StopNote{Cause: session.StopCauseStop}},
		"completed":      {Generation: 1, State: session.LifecycleCompleted},
		"idle-running":   {Generation: 1, State: session.LifecycleRunning},
	}
	for name, rec := range states {
		t.Run(name, func(t *testing.T) {
			al := newCancelTestAgentLoop(t)
			auditDir := t.TempDir()
			al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)
			lc := session.NewLifecycleStore(t.TempDir())
			al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lc)
			r := *rec
			r.SessionID, r.OwnerScopeKind, r.WorkspaceID, r.AgentID = "sess-audit-"+name, session.OwnerScopeHuman, "ws-1", "mia"
			require.NoError(t, lc.Persist(&r))

			for attempt := 1; attempt <= 2; attempt++ {
				_, err := al.StopSession(context.Background(), StopRequest{
					SessionID: r.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "alice"}, Channel: "web",
				})
				require.NoError(t, err)
				n := 0
				for _, ev := range readCancelAuditEvents(t, auditDir) {
					if ev == audit.EventTurnCancelAttempt {
						n++
					}
				}
				require.Equal(t, attempt, n, "Stop #%d on a %s session must add exactly one turn.cancel.attempt", attempt, name)
			}
		})
	}
}

// R2-S1-09: a Stop attempt whose generation was already superseded, or whose
// RequestCancel fails early, still happened: it writes its audit row, exactly
// once.
func TestRequestCancel_EarlyReturnsStillAuditTheAttemptExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name       string
		recGen     int
		scopeGen   int
		wantErr    bool
		wantNewer  bool
		persistRec bool
	}{
		{"superseded generation", 3, 1, false, true, true},
		{"no carried selection", 1, 1, true, false, true},
		{"lifecycle record unreadable", 0, 1, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al := newCancelTestAgentLoop(t)
			auditDir := t.TempDir()
			al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)
			lc := session.NewLifecycleStore(t.TempDir())
			al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lc)
			if tc.persistRec {
				require.NoError(t, lc.Persist(&session.LifecycleRecord{
					SessionID: "sess-early", Generation: tc.recGen, State: session.LifecycleRunning,
					OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "mia",
				}))
			}
			out, err := al.RequestCancel(context.Background(),
				CancelScope{SessionID: "sess-early", TurnOnly: true, Generation: tc.scopeGen},
				CancelCanceller{UserID: "alice", Channel: "web"}, CancelHooks{})
			require.Equal(t, tc.wantErr, err != nil, "err=%v", err)
			require.Equal(t, tc.wantNewer, out.SkippedNewerGeneration)
			n := 0
			for _, ev := range readCancelAuditEvents(t, auditDir) {
				if ev == audit.EventTurnCancelAttempt {
					n++
				}
			}
			require.Equal(t, 1, n, "the attempt must be audited exactly once")
		})
	}
}
