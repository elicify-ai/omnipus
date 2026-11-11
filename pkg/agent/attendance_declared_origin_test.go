package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// #1765: plan owner/supervision sessions (PlanEngine.mintPlanSession) and the
// external-CLI Judge session never get a lifecycle record, and the direct
// dispatch (processTaskDirect) declares the turn's origin
// (processOptions.OriginKind); wakes through other entries are covered by the
// plan-record witness (attendance_plan_session_test.go). A turn that DECLARES an unattended origin and
// whose own session has no record is classified by that declared origin: no
// "approval ancestry unresolved" ERROR, same refusal. A genuinely damaged
// chain (no declared origin, or a missing ANCESTOR) still logs and refuses.

const ancestryUnresolvedLog = "approval ancestry unresolved"

// attendanceLog runs applyTurnAttendance on opts with file logging on and
// returns the log text.
func attendanceLog(t *testing.T, al *AgentLoop, opts *processOptions) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "agent.log")
	require.NoError(t, logger.EnableFileLogging(logPath))
	defer logger.DisableFileLogging()
	al.applyTurnAttendance(opts)
	logger.DisableFileLogging()
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	return string(data)
}

// mintedPlanLikeSession mirrors PlanEngine.mintPlanSession: a task-typed
// session, no TaskID, no lifecycle record.
func mintedPlanLikeSession(t *testing.T, al *AgentLoop) string {
	t.Helper()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeTask, "system", testDefaultAgentID)
	require.NoError(t, err)
	_, loadErr := al.GetSessionLifecycleStore().Load(meta.ID)
	require.ErrorIs(t, loadErr, session.ErrLifecycleNotFound, "fixture: the minted session has no lifecycle record")
	return meta.ID
}

func TestAttendance_DeclaredTaskOrigin_PlanSessionWithoutRecord_NoUnresolvedError(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	id := mintedPlanLikeSession(t, al)

	opts := processOptions{TranscriptSessionID: id, OriginKind: session.OriginKindTask}
	text := attendanceLog(t, al, &opts)

	assert.NotContains(t, text, ancestryUnresolvedLog, "a declared task origin resolves the chain; no ERROR")
	assert.True(t, opts.AutoDenyAsk, "the turn stays unattended")
	assert.Equal(t, autoDenyTaskPlanReason, opts.askRefusalReason, "refusal wording for a plan wake is unchanged")
	assert.Empty(t, opts.askRefusalCause, "nothing is damaged, so no damage cause is carried into the audit")
}

func TestAttendance_DeclaredVerifierOrigin_SessionWithoutRecord_HeadlessWording(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	meta, err := al.GetSessionStore().NewVerifierSession("judge")
	require.NoError(t, err)

	opts := processOptions{TranscriptSessionID: meta.ID, OriginKind: session.OriginKindVerifier}
	text := attendanceLog(t, al, &opts)

	assert.NotContains(t, text, ancestryUnresolvedLog)
	assert.True(t, opts.AutoDenyAsk)
	assert.Equal(t, autoDenyHeadlessReason, opts.askRefusalReason, "a Judge turn keeps the headless wording")
}

func TestAttendance_NoDeclaredOrigin_MissingRecord_StillRefusesAndLogs(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	chat, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	require.NoError(t, err)

	opts := processOptions{TranscriptSessionID: chat.ID}
	text := attendanceLog(t, al, &opts)

	assert.Contains(t, text, ancestryUnresolvedLog, "a chat session with a missing record is real damage")
	assert.Contains(t, text, chat.ID)
	assert.True(t, opts.AutoDenyAsk, "fail toward the visible refusal")
	assert.Equal(t, autoDenyTaskPlanReason, opts.askRefusalReason)
	assert.Contains(t, opts.askRefusalCause, "lifecycle record unreadable or missing")
}

func TestAttendance_DeclaredOriginThatIsNotUnattended_MissingRecord_StillRefuses(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	id := mintedPlanLikeSession(t, al)

	opts := processOptions{TranscriptSessionID: id, OriginKind: session.OriginKindChat}
	text := attendanceLog(t, al, &opts)

	assert.Contains(t, text, ancestryUnresolvedLog, "only an UNATTENDED declared origin excuses a missing record")
	assert.True(t, opts.AutoDenyAsk)
	assert.Contains(t, opts.askRefusalCause, "lifecycle record unreadable or missing")
}

func TestAttendance_DeclaredTaskOrigin_MissingAncestorRecord_StillRefusesAndLogs(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	sessions := al.GetSessionStore()
	parent := mintedPlanLikeSession(t, al)
	child, err := sessions.NewSession(session.SessionTypeDelegate, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	require.NoError(t, sessions.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parent}))
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: child.ID, Generation: 1, State: session.LifecycleRunning,
		AgentID: testDefaultAgentID, OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: session.OriginKindDelegate},
	}))

	opts := processOptions{TranscriptSessionID: child.ID, OriginKind: session.OriginKindTask}
	text := attendanceLog(t, al, &opts)

	assert.Contains(t, text, ancestryUnresolvedLog, "the declared origin covers the turn's own session only, never an ancestor")
	assert.Contains(t, text, parent)
	assert.True(t, opts.AutoDenyAsk)
	assert.Contains(t, opts.askRefusalCause, parent)
}
