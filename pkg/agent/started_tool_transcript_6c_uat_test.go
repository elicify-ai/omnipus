package agent

// 6c UAT RED: S11/AC-D1-6 and B93 in chat-history-paging-spec, plus
// chief's 2026-11-02 08:23Z requirements (a)–(d). All transcript stores,
// dispatch/settlement helpers and boot recovery are real. No model is called.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The UAT receipt contains these ID/text prefixes, not complete UUIDs/text.
// Use the captured prefixes as fixture labels without inventing their suffixes.
const (
	uat6cA     = "b7745508-d"
	uat6cSteer = "23b864e5-8"
	uat6cCall  = session.ToolCallID("call_ffb6d")
	uat6cB     = "6746c6aa-0"
	uat6cTurn  = "mia-turn-16"
	uat6cCmd   = "sleep 30 && echo \"slept 30s; exit code 0\""
)

type uat6cTranscriptFixture struct {
	home  string
	store *session.UnifiedStore
	sid   string
	ts    *turnState
}

func newUAT6cTranscriptFixture(t *testing.T) *uat6cTranscriptFixture {
	t.Helper()
	f := &uat6cTranscriptFixture{home: t.TempDir()}
	var err error
	f.store, err = session.NewUnifiedStore(filepath.Join(f.home, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.store.Close()) })
	meta, err := f.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	f.sid = meta.ID
	f.newTurn()
	return f
}

func (f *uat6cTranscriptFixture) newTurn() {
	// Same minimal real-store boundary as newApprovalTranscriptTS. Display
	// receipt persistence does not require a provider or a model window.
	f.ts = &turnState{turnID: uat6cTurn, agentID: "mia", transcriptSessionID: f.sid, transcriptStore: f.store}
}

func (f *uat6cTranscriptFixture) reopen(t *testing.T) {
	t.Helper()
	require.NoError(t, f.store.Close())
	store, err := session.NewUnifiedStore(filepath.Join(f.home, "sessions"))
	require.NoError(t, err, "a reload opens the real saved transcript, not the old turn's memory")
	f.store = store
	f.newTurn()
}

func (f *uat6cTranscriptFixture) entries(t *testing.T) []session.TranscriptEntry {
	t.Helper()
	entries, err := f.store.ReadTranscript(f.sid)
	require.NoError(t, err, "saved transcript must remain readable")
	return entries
}

func (f *uat6cTranscriptFixture) append(t *testing.T, entry session.TranscriptEntry) {
	t.Helper()
	require.NoError(t, f.store.AppendTranscriptStrict(f.sid, entry))
}

func uat6cEntry(t *testing.T, id, role, text, at string) session.TranscriptEntry {
	t.Helper()
	stamp, err := time.Parse(time.RFC3339Nano, at)
	require.NoError(t, err)
	turn := uat6cTurn
	if role == "user" {
		turn = "" // the captured steer has no recorded turn of its own
	}
	return session.TranscriptEntry{ID: id, Role: role, AgentID: "mia", TurnID: turn,
		Content: text, Timestamp: stamp, ViewMembership: session.ViewMembershipChat}
}

func uat6cAEntry(t *testing.T) session.TranscriptEntry {
	return uat6cEntry(t, uat6cA, "assistant", "First sentence:\n\nA river begins as a rumour of water high in", "2026-11-02T04:36:48.369498154Z")
}

func uat6cSteerEntry(t *testing.T) session.TranscriptEntry {
	return uat6cEntry(t, uat6cSteer, "user", "also add a third sentence", "2026-11-02T04:36:52.508030335Z")
}

func uat6cBEntry(t *testing.T) session.TranscriptEntry {
	return uat6cEntry(t, uat6cB, "assistant", "The bash step is verified: `sleep 30 && echo \"slept 30s; exi", "2026-11-02T04:37:22.429815648Z")
}

func uat6cIDs(entries []session.TranscriptEntry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func (f *uat6cTranscriptFixture) execution() *agentLoopRunTurnToolsExecute {
	rt := &agentLoopRunTurn{al: &AgentLoop{}, ts: f.ts}
	rf := &agentLoopRunTurnFallbacks{rt: rt}
	ri := &agentLoopRunTurnIteration{rf: rf}
	rq := &agentLoopRunTurnRequest{ri: ri}
	rr := &agentLoopRunTurnResponse{rq: rq}
	return &agentLoopRunTurnToolsExecute{
		rx: &agentLoopRunTurnTools{rr: rr}, toolName: "bash", toolCallID: string(uat6cCall),
		toolArgs: map[string]any{"command": uat6cCmd},
		// No model result is being admitted: exercise the genuine display
		// writer, as the approval-placeholder tests do, not a fabricated slot.
		admitted: admittedToolResult{ArchiveLine: -1},
	}
}

func (f *uat6cTranscriptFixture) begin(t *testing.T) *agentLoopRunTurnToolsExecute {
	t.Helper()
	ex := f.execution()
	require.True(t, ex.beginStartedTool(providers.ToolCall{ID: string(uat6cCall), Name: "bash"}, time.Now()),
		"instrument: real tool dispatch was accepted")
	return ex
}

func uat6cCompletedCall(status string) session.ToolCall {
	call := session.ToolCall{ID: uat6cCall, Tool: "bash", Status: status,
		Parameters: map[string]any{"command": uat6cCmd}, DurationMS: 30_000}
	if status == "error" {
		call.Error = "fixture: bash failed"
	} else {
		call.Result = map[string]any{"text": "slept 30s; exit code 0"}
	}
	return call
}

func (f *uat6cTranscriptFixture) archiveBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.store.BaseDir(), f.sid, "transcript.jsonl"))
	require.NoError(t, err)
	return data
}

func (f *uat6cTranscriptFixture) rawCallCount(t *testing.T) int {
	t.Helper()
	count := 0
	for _, line := range bytes.Split(bytes.TrimSpace(f.archiveBytes(t)), []byte{'\n'}) {
		var rec session.ArchiveRecord
		require.NoError(t, json.Unmarshal(line, &rec), "instrument: examine actual archive records, not duplicate-suppressed UI output")
		if rec.Type != session.EntryTypeToolCall {
			continue
		}
		for _, call := range rec.ToolCalls {
			if call.ID == uat6cCall {
				count++
			}
		}
	}
	return count
}

func (f *uat6cTranscriptFixture) boot(t *testing.T) *SteerBootRecovery {
	t.Helper()
	lifecycle := session.NewLifecycleStore(filepath.Join(f.home, "lifecycle"))
	boot := session.NewBootEpochStore(f.home)
	_, err := boot.Mint()
	require.NoError(t, err)
	var notices []string
	recovery := &SteerBootRecovery{
		Lifecycle: lifecycle, Sessions: f.store,
		Inbox:      session.NewMessageInboxStore(filepath.Join(f.home, "inbox")),
		Classifier: NewSteerRecordClassifier(lifecycle, f.store), BootEpoch: boot,
		OperatorNotice: func(message string) { notices = append(notices, message) },
	}
	require.NoError(t, recovery.Run(context.Background()), "instrument: real boot recovery completed")
	require.Empty(t, notices, "instrument: no unreadable or refused session was silently ignored")
	return recovery
}

func Test6cUAT_UD16_a_CrashLeftRunningRecordReloadsInterrupted(t *testing.T) {
	f := newUAT6cTranscriptFixture(t)
	f.append(t, uat6cAEntry(t))
	// Independent crash image: dispatch's required running record exists, but
	// neither complete() nor cancel() ever executes. Case (b) tests the real
	// dispatch write separately, so its absence cannot mask this crash oracle.
	running := session.ToolCall{ID: uat6cCall, Tool: "bash", Status: "running", Parameters: map[string]any{"command": uat6cCmd}}
	require.NoError(t, f.ts.appendToolCallTranscript(running))
	require.Equal(t, []session.ToolCall{running}, toolCallsFor(t, f.store, f.sid, uat6cCall),
		"instrument: a genuine running call is persisted before the simulated crash")
	oldBoot := session.NewBootEpochStore(f.home)
	epoch, err := oldBoot.Mint()
	require.NoError(t, err)
	lifecycle := session.NewLifecycleStore(filepath.Join(f.home, "lifecycle"))
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: f.sid, Generation: 1, State: session.LifecycleRunning,
		AgentID: "mia", WorkspaceID: "ws", OwnerScopeKind: session.OwnerScopeHuman,
		Origin:      &session.Origin{Kind: session.OriginKindChat},
		ExecutionID: &session.ExecutionIdentity{RunID: uat6cTurn, BootSeq: epoch},
	}))

	f.reopen(t) // discard the old turn/started-call memory; no terminal callback
	recovery := f.boot(t)
	calls := toolCallsFor(t, f.store, f.sid, uat6cCall)
	require.Len(t, calls, 1, "(a) crash recovery preserves exactly one card for the dispatched call")
	assert.Equal(t, "interrupted", calls[0].Status, "(a) crash-left tool must reload as interrupted, never running forever")
	assert.Equal(t, running.ID, calls[0].ID)
	assert.Equal(t, running.Tool, calls[0].Tool)
	assert.Equal(t, running.Parameters, calls[0].Parameters)
	assert.Equal(t, []string{uat6cA, string(uat6cCall)}, uat6cIDs(f.entries(t)), "(a) interruption does not move the card")
	assert.Equal(t, 1, f.rawCallCount(t), "(a) recovery settles the existing record, not a second tool_call")
	firstReload := f.entries(t)
	require.NoError(t, recovery.Run(context.Background()))
	assert.Equal(t, firstReload, f.entries(t), "(a) repeated recovery/read is idempotent")
}

func Test6cUAT_UD16_b_UpdatedInPlaceKeepsPositionAndOneCallID(t *testing.T) {
	for _, status := range []string{"success", "error"} {
		t.Run(status, func(t *testing.T) {
			f := newUAT6cTranscriptFixture(t)
			f.append(t, uat6cAEntry(t))
			ex := f.begin(t)
			assert.Equal(t, []string{uat6cA, string(uat6cCall)}, uat6cIDs(f.entries(t)),
				"(b) dispatch reserves the tool's transcript position before any steer")
			assert.Equal(t, []session.ToolCall{{ID: uat6cCall, Tool: "bash", Status: "running", Parameters: map[string]any{"command": uat6cCmd}}},
				toolCallsFor(t, f.store, f.sid, uat6cCall), "(b) dispatched call is durably running before completion")
			f.append(t, uat6cSteerEntry(t)) // accepted while the tool is still running
			ex.tcRecord = uat6cCompletedCall(status)
			require.NoError(t, ex.recordAdmittedTranscript(), "real completion settles the dispatched call")
			// A repeated completion must not write a second tool_call receipt.
			require.NoError(t, ex.recordAdmittedTranscript())
			f.append(t, uat6cBEntry(t))
			f.reopen(t)
			assert.Equal(t, []string{uat6cA, string(uat6cCall), uat6cSteer, uat6cB}, uat6cIDs(f.entries(t)),
				"(b) in-place update preserves the original A,tool,steer,B position")
			assert.Equal(t, []session.ToolCall{uat6cCompletedCall(status)}, toolCallsFor(t, f.store, f.sid, uat6cCall),
				"(b) reload retains the exact final status, arguments, result/error and duration once")
			assert.Equal(t, 1, f.rawCallCount(t), "(b) exactly one physical tool_call record per call ID; settlements are effects")
		})
	}
}

func Test6cUAT_UD16_c_LegacyHistoryWithoutRunningRecordsLoadsUnchanged(t *testing.T) {
	f := newUAT6cTranscriptFixture(t)
	// Captured pre-change mia-turn-16 order. Do not retroactively repair it:
	// chief (c) explicitly requires identical entries, order and no error.
	toolEntry := session.TranscriptEntry{
		ID: string(uat6cCall), Type: session.EntryTypeToolCall, TurnID: uat6cTurn,
		AgentID: "mia", ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{ID: uat6cCall, Tool: "bash", Status: "success", Parameters: map[string]any{"command": uat6cCmd}}},
	}
	var err error
	toolEntry.Timestamp, err = time.Parse(time.RFC3339Nano, "2026-11-02T04:37:19.773894247Z")
	require.NoError(t, err)
	want := []session.TranscriptEntry{uat6cAEntry(t), uat6cSteerEntry(t), toolEntry, uat6cBEntry(t)}
	for _, entry := range want {
		f.append(t, entry)
	}
	before := f.archiveBytes(t)
	f.reopen(t)
	f.boot(t)
	assert.Equal(t, want, f.entries(t), "(c) legacy A,steer,tool,B entries load identically without reordering or error")
	assert.Equal(t, before, f.archiveBytes(t), "(c) reload/boot does not rewrite or append corrections to old completed history")
}

func Test6cUAT_UD16_d_ReloadKeepsSteerAfterToolAndBeforeB(t *testing.T) {
	f := newUAT6cTranscriptFixture(t)
	f.append(t, uat6cAEntry(t))
	ex := f.begin(t)
	f.append(t, uat6cSteerEntry(t)) // genuine acceptance order: dispatch, then steer
	ex.tcRecord = uat6cCompletedCall("success")
	require.NoError(t, ex.recordAdmittedTranscript())
	f.append(t, uat6cBEntry(t))
	f.reopen(t)
	assert.Equal(t, []string{uat6cA, string(uat6cCall), uat6cSteer, uat6cB}, uat6cIDs(f.entries(t)),
		"(d) B93 reload source order must be A,tool,steer,B so rendered rows are [A+bash],steer,B")
	assert.Equal(t, []session.ToolCall{uat6cCompletedCall("success")}, toolCallsFor(t, f.store, f.sid, uat6cCall))
	assert.Equal(t, 1, f.rawCallCount(t), "(d) one call, not a moved/duplicate card")
}
