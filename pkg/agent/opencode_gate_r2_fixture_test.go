package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: architect ruling R1-c/R1-d/R2 and the generated W17 contract
// catalogue (team-lead ruling 2026-11-23). Do not call runtime translators
// or cap helpers to manufacture expected values.
var (
	opencodeR2ChatWarning    = generated.LLMErrorUserMessages["transcript_save_failed"]
	opencodeR2NeutralWarning = generated.LLMErrorNeutralMessages["transcript_save_failed"]
)

const opencodeR2OwnershipLog = "external-cli: execution ownership unknown"
const opencodeR2SaveLog = "external-cli: a helper message could not be saved to the transcript"

// The unit inputs are turn states, real lifecycle journals and a real archive.
// Only outgoing stream transport is doubled. These are caller-boundary tests,
// not claims of observing a Stop/revival race or fabricating an admission.
// Keep the ordinary unwired audience (User) so a failed lifecycle lookup cannot
// independently hide a missing ownership gate. proveNoticeRoute checks it.
type opencodeR2Fixture struct {
	al        *AgentLoop
	lifecycle *session.LifecycleStore
	archive   *session.UnifiedStore
	record    *session.LifecycleRecord
	turn      *turnState
	stream    *mockStreamer
	events    <-chan Event
	logPath   string
}

func newOpencodeR2Fixture(t *testing.T) *opencodeR2Fixture {
	t.Helper()
	al := newSteerBoundaryTestLoop(t)
	lifecycle, archive := newTestClassifierStores(t)
	sid := newMetaSession(t, archive, session.SessionTypeChat, "")
	rec := &session.LifecycleRecord{
		SessionID: sid, State: session.LifecycleRunning, Generation: 1,
		AgentID: "agent-1", OwnerScopeKind: session.OwnerScopeHuman,
		ExecutionID: &session.ExecutionIdentity{RunID: "r2-producing-run", BootSeq: 7},
	}
	require.NoError(t, lifecycle.Persist(rec))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	f := &opencodeR2Fixture{al: al, lifecycle: lifecycle, archive: archive, record: rec, stream: &mockStreamer{}}
	f.turn = f.newTurn(t, rec.ExecutionID.RunID, rec.ExecutionID.BootSeq)
	f.turn.setLastStreamer(f.stream)
	require.True(t, al.tsExecutionClaim(f.turn, "").matches(rec), "instrument: real readable record matches the producing unit input")
	sub := al.SubscribeEvents(32)
	f.events = sub.C
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	f.logPath = filepath.Join(t.TempDir(), "ownership.jsonl")
	level := logger.GetLevel()
	logger.SetLevel(logger.ERROR)
	require.NoError(t, logger.EnableFileLogging(f.logPath))
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(level) })
	logger.ErrorCF("agent", "r2-log-instrument", map[string]any{"session_id": sid})
	return f
}

func (f *opencodeR2Fixture) newTurn(t *testing.T, runID string, bootSeq uint64) *turnState {
	t.Helper()
	ts := newTurnState(&AgentInstance{ID: "agent-1"}, processOptions{
		SessionKey: f.record.SessionID, Channel: "webchat", ChatID: f.record.SessionID,
		TranscriptSessionID: f.record.SessionID, TranscriptStore: f.archive, NoHistory: true,
	}, f.al.newTurnEventScope("agent-1", f.record.SessionID))
	ts.generation = f.record.Generation
	require.NoError(t, ts.setExecutionIdentity(runID, bootSeq))
	ts.externalLiveOutput = true
	return ts
}

func (f *opencodeR2Fixture) replaceRecord(t *testing.T) {
	t.Helper()
	require.NoError(t, f.lifecycle.Mutate(f.record.SessionID, func(rec *session.LifecycleRecord) error {
		rec.ExecutionID = &session.ExecutionIdentity{RunID: "r2-replacement-run", BootSeq: 8}
		return nil
	}))
	rec, err := f.lifecycle.Load(f.record.SessionID)
	require.NoError(t, err)
	require.False(t, f.al.tsExecutionClaim(f.turn, "").matches(rec), "instrument: durable replacement really differs")
}

func (f *opencodeR2Fixture) registerReplacement(t *testing.T) *turnState {
	t.Helper()
	other := f.newTurn(t, "r2-replacement-run", 8)
	f.al.registerActiveTurn(other)
	t.Cleanup(func() { f.al.clearActiveTurn(other) })
	require.Same(t, other, f.al.getActiveTurnState(f.record.SessionID))
	require.True(t, other.IsAlive())
	return other
}

func (f *opencodeR2Fixture) faultRecord(t *testing.T) error {
	t.Helper()
	path := filepath.Join(f.lifecycle.Dir(), f.record.SessionID+".jsonl")
	backup := filepath.Join(t.TempDir(), "readable-lifecycle.saved")
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700)) // Root-safe read fault, not chmod.
	t.Cleanup(func() {
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Rename(backup, path))
	})
	_, err := f.lifecycle.Load(f.record.SessionID)
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "instrument: fault the real store's read, not a preflight surrogate")
	require.Equal(t, path, pathErr.Path)
	return err
}

func (f *opencodeR2Fixture) takeNotices(t *testing.T) []ErrorPayload {
	t.Helper()
	var notices []ErrorPayload
	for {
		select {
		case event := <-f.events:
			if event.Kind == EventKindError {
				payload, ok := event.Payload.(ErrorPayload)
				require.True(t, ok, "typed error payload must survive the real event bus")
				require.Equal(t, f.record.SessionID, payload.SessionID)
				notices = append(notices, payload)
			}
		default:
			return notices // Emit is synchronous; no background turn runs in this fixture.
		}
	}
}

func (f *opencodeR2Fixture) proveNoticeRoute(t *testing.T) {
	t.Helper()
	require.Equal(t, steer.AudienceUser, f.al.audienceFor(context.Background(), steer.BoundaryTypedErrorFrame, f.record.SessionID))
	f.al.emitExternalErrorFrame(f.turn, "r2-route-instrument", LLMError{Code: CodeTranscriptSaveFailed, Message: opencodeR2ChatWarning}, "")
	rows := f.takeNotices(t)
	require.Len(t, rows, 1, "instrument: even during a lifecycle read fault a live notice could reach this subscriber")
	require.Equal(t, "r2-route-instrument", rows[0].Stage)
	require.Equal(t, "transcript_save_failed", rows[0].Code)
	require.Equal(t, opencodeR2ChatWarning, rows[0].Message)
	require.False(t, f.turn.externalSaveFailureAnnounced.Load(), "route control must not consume the unit's once-only flag")
}

func (f *opencodeR2Fixture) logs(t *testing.T, message string) []map[string]any {
	t.Helper()
	file, err := os.Open(f.logPath)
	require.NoError(t, err)
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var rows []map[string]any
	controls := 0
	for scanner.Scan() {
		var row map[string]any // not-wire-format: observation of the real structured logger.
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row))
		if row["session_id"] != f.record.SessionID {
			continue
		}
		if row["message"] == "r2-log-instrument" {
			controls++
		}
		if row["message"] == message {
			rows = append(rows, row)
		}
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, 1, controls, "instrument: an empty diagnostic search is meaningful only with a working logger")
	return rows
}

func (f *opencodeR2Fixture) assertOwnershipLog(t *testing.T, decision string, cause error) {
	t.Helper()
	rows := f.logs(t, opencodeR2OwnershipLog)
	if !assert.Len(t, rows, 1, "R2-e: exactly one error-level ownership diagnostic per consumed decision") {
		return
	}
	row := rows[0]
	assert.Equal(t, "error", row["level"])
	assert.Equal(t, f.turn.turnID, row["turn_id"])
	assert.Equal(t, "r2-producing-run", row["run_id"])
	assert.Equal(t, float64(1), row["generation"])
	assert.Equal(t, float64(7), row["boot_seq"])
	assert.Equal(t, decision, row["decision"])
	assert.Contains(t, row["error"], cause.Error(), "the lifecycle Load cause must not be silently dropped")
	assert.NotContains(t, row["error"], opencodeR2ChatWarning, "no message content in ownership diagnostics")
}
