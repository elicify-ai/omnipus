package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// #1765 (validator finding): the external-CLI dispatch builds its own
// processOptions (runTaskExternalCLIOnce) and used to declare no origin, so a
// plan wake or an external-CLI Judge turn on a session minted without a
// lifecycle record still logged "approval ancestry unresolved". Real:
// processTaskDirectExternalCLI -> runTaskExternalCLIOnce -> applyTurnAttendance
// on a recording driver; the instrument is the gateway log.
func externalCLIAttendanceLog(t *testing.T, sessionID string) (string, *AgentLoop) {
	t.Helper()
	al, agent, _ := u10bTaskFixture(t)
	_, restore := withRecordingDriver(t)
	defer restore()
	logPath := filepath.Join(t.TempDir(), "agent.log")
	require.NoError(t, logger.EnableFileLogging(logPath))
	defer logger.DisableFileLogging()
	if sessionID == "task-run" {
		// A minted task-run session: persisted task evidence (meta TaskID).
		meta, err := al.GetSessionStore().NewSession(session.SessionTypeTask, "system", agent.ID)
		require.NoError(t, err)
		taskID := "task-1"
		require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{TaskID: &taskID}))
		sessionID = meta.ID
	}
	if sessionID == "" {
		meta, err := al.GetSessionStore().NewSession(session.SessionTypeTask, "system", agent.ID)
		require.NoError(t, err)
		sessionID = meta.ID
		_, loadErr := al.GetSessionLifecycleStore().Load(sessionID)
		require.ErrorIs(t, loadErr, session.ErrLifecycleNotFound, "fixture: no lifecycle record, like a plan owner session")
	}
	_, err := al.processTaskDirectExternalCLI(context.Background(), agent, u10bTaskInput, "task-key", sessionID, 0)
	require.NoError(t, err)
	logger.DisableFileLogging()
	data, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	return string(data), al
}

func TestExternalCLIDispatch_PlanLikeSessionWithoutRecord_NoUnresolvedError(t *testing.T) {
	text, _ := externalCLIAttendanceLog(t, "")
	require.NotContains(t, text, ancestryUnresolvedLog,
		"the external-CLI turn declares an unattended origin, so a session minted without a record is not damage")
}

func TestExternalCLIDispatch_RealTaskRunSession_NoUnresolvedError(t *testing.T) {
	text, _ := externalCLIAttendanceLog(t, "task-run")
	require.NotContains(t, text, ancestryUnresolvedLog, "a real task run resolves through its lifecycle record")
}

func TestExternalCLIDispatch_MissingAncestor_StillLogsUnresolved(t *testing.T) {
	al, agent, _ := u10bTaskFixture(t)
	_, restore := withRecordingDriver(t)
	defer restore()
	sessions := al.GetSessionStore()
	parent, err := sessions.NewSession(session.SessionTypeTask, "system", agent.ID)
	require.NoError(t, err)
	child, err := sessions.NewSession(session.SessionTypeDelegate, "webchat", agent.ID)
	require.NoError(t, err)
	parentID := parent.ID
	require.NoError(t, sessions.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parentID}))
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: child.ID, Generation: 1, State: session.LifecycleRunning,
		AgentID: agent.ID, OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: session.OriginKindDelegate},
	}))
	logPath := filepath.Join(t.TempDir(), "agent.log")
	require.NoError(t, logger.EnableFileLogging(logPath))
	defer logger.DisableFileLogging()

	_, runErr := al.processTaskDirectExternalCLI(context.Background(), agent, u10bTaskInput, "task-key", child.ID, 0)
	require.NoError(t, runErr)
	logger.DisableFileLogging()
	data, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	require.Contains(t, string(data), ancestryUnresolvedLog, "the declared origin never covers a damaged ancestor")
	require.Contains(t, string(data), parentID)
}
