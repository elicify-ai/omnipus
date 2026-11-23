package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const opencodeR5HealthyAnswer = "The external helper's healthy answer must remain intact."

// Real launch/stores/routing, deterministic finishing-window holder; only the
// external process edge is doubled. No clock or race is the test oracle.
type opencodeR5FinishingFixture struct {
	al        *AgentLoop
	driver    *opencodeR4Driver
	processes *atomic.Int64
	cancels   atomic.Int64
	record    *session.LifecycleRecord
	message   bus.InboundMessage
	events    *opencodeR4Events
}

func newOpencodeR5FinishingFixture(t *testing.T) *opencodeR5FinishingFixture {
	t.Helper()
	al, _ := opencodeR4Loop(t, "opencode")
	driver, processes := opencodeR4ProcessEdge(t, al)
	child := launchDelegateTo(t, al, delegateExtCLIAgentID, "R5 finishing-window unit input")
	require.NoError(t, al.GetSessionLifecycleStore().Mutate(child.SessionID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleRunning
		rec.ExternalRunStarted = true
		return nil
	}))
	f := &opencodeR5FinishingFixture{al: al, driver: driver, processes: processes}
	holder := al.externalRunSession(child.SessionID)
	holder.mu.Lock()
	holder.started, holder.running, holder.driver = true, false, driver
	holder.cancelRun = func() { f.cancels.Add(1) }
	holder.mu.Unlock()
	var err error
	f.record, err = al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.NoError(t, al.ResolveSessionStore(child.SessionID).AppendTranscriptStrict(child.SessionID, session.TranscriptEntry{
		ID: "r5-original-answer", Role: "assistant", AgentID: delegateExtCLIAgentID,
		Content: opencodeR5HealthyAnswer, Timestamp: time.Now().UTC(),
	}))
	f.message = opencodeR4SaveHuman(t, al, child.SessionID, "R5 finishing-window follow-up")
	f.events = opencodeR4Observe(t, al, child.SessionID)
	return f
}

func (f *opencodeR5FinishingFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	after, err := f.al.GetSessionLifecycleStore().Load(f.record.SessionID)
	require.NoError(t, err)
	assert.Equal(t, f.record, after, "SFH-R5: refusing input changes no lifecycle state, execution or generation")
	assert.Zero(t, f.processes.Load(), "SFH-R5: no external process may be constructed")
	assert.Zero(t, f.cancels.Load(), "SFH-R5: no cancellation of the finishing execution")
	assert.Zero(t, f.driver.cancels.Load())
	assert.Zero(t, f.al.pendingSteeringCountForScope(f.record.SessionID), "SFH-R5: refused input is never queued")
}

func opencodeR5BlockRefusalArchive(t *testing.T, al *AgentLoop, sid string) (func(), error) {
	t.Helper()
	store := al.ResolveSessionStore(sid)
	path := filepath.Join(store.BaseDir(), sid, "transcript.jsonl")
	backup := filepath.Join(t.TempDir(), "answer-and-human-input.saved")
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	_, err := al.AppendSessionRefusal(sid, delegateExtCLIAgentID, "", "refusal-writer instrument")
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "instrument: the actual refusal writer must attempt the blocked archive")
	require.Equal(t, path, pathErr.Path)
	return restore, err
}

func opencodeR5AgentLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "refusal-save.jsonl")
	level := logger.GetLevel()
	logger.SetLevel(logger.ERROR)
	require.NoError(t, logger.EnableFileLogging(path))
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(level) })
	logger.ErrorCF("agent", "r5-log-control", map[string]any{"control": "r5-logger-observed"})
	return path
}

func opencodeR5AssertAgentCause(t *testing.T, path string, cause error) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "r5-logger-observed", "instrument: structured logger reached the saved file")
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row map[string]any // not-wire-format: structured test log observation.
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		text, _ := row["error"].(string)
		if row["level"] == "error" && strings.Contains(text, cause.Error()) {
			found = true
		}
	}
	assert.True(t, found, "SFH-R5-1: log the actual strict-append cause at error level: %v", cause)
}

// SFH-R5-2 allows either an existing explicit notice or a new refused-input
// discriminator. The latter's internal field names are in team-lead's
// follow-up brief. Reflection keeps this RED pack buildable before those
// fields exist; a missing implementation is a loud failure, never a skip.
func opencodeR5AssertNonTerminalPayload(t *testing.T, p ErrorPayload) {
	t.Helper()
	if p.ChatNotice != nil {
		assert.Equal(t, "replay_message", p.ChatNotice.Type)
		assert.Equal(t, "system", p.ChatNotice.Role)
		assert.Equal(t, p.SessionID, p.ChatNotice.SessionId)
		require.NotNil(t, p.ChatNotice.Id)
		assert.NotEmpty(t, *p.ChatNotice.Id)
		return
	}
	marker := reflect.ValueOf(p).FieldByName("RefusedInput")
	if !marker.IsValid() {
		t.Fatal("BLOCKED: explicit refused-input discriminator not implemented — required by SFH-R5-2 and team-lead round-5 follow-up")
	}
	require.Equal(t, reflect.Bool, marker.Kind())
	assert.True(t, marker.Bool(), "SFH-R5-2: refusing an input must be distinguishable from a terminal run error")
}

func opencodeR5AssertRefusedInputEntry(t *testing.T, p ErrorPayload, want string) {
	t.Helper()
	entry := reflect.ValueOf(p).FieldByName("RefusedInputEntryID")
	if !entry.IsValid() {
		t.Fatal("BLOCKED: refused-input entry correlation not implemented — required by C2 row reconciliation and team-lead round-5 follow-up")
	}
	require.Equal(t, reflect.String, entry.Kind())
	assert.Equal(t, want, entry.String(), "C2: reconcile the real refused human row, never the healthy assistant")
}
