package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: ADR-066 §12, [2026-10-01 correction — R1 GREEN blocker,
// FR-019/FR-022], read in the architect-owned cw-r1-green ADR, not its code.
// Dispatcher clarification on 2026-10-01: TranscriptLine is *int; nil is
// unknown, while &0 addresses the first transcript row. No wire field is added.
// Real admission, transcript rewrites, JSONL persistence and rollback stay real.
// GREEN and implementation mutations are deferred to an independent CHECK.
type cwIdentityHarness struct {
	al              *AgentLoop
	agent           *AgentInstance
	store           *session.UnifiedStore
	home            string
	baseDir         string
	sessionID       string
	key             string
	archiveLines    int
	transcriptLines int
}

type cwIdentityRecorded struct {
	key      memory.ProjectionKey
	line     int
	admitted admittedToolResult
}

func newCWIdentityHarness(t *testing.T) *cwIdentityHarness {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, "sessions")
	store, err := session.NewUnifiedStoreWithHome(base, home)
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "identity")
	require.NoError(t, err)
	cs := config.DefaultContextSettings()
	// A fixture cap, not a product-limit oracle: cap+1 results exercise the
	// already-capped class; short literal results exercise the full class.
	cs.BuiltinSuccessCap = 512
	cs.BuiltinFailureCap = 512
	h := &cwIdentityHarness{
		al:        &AgentLoop{cfg: &config.Config{Context: cs}},
		agent:     &AgentInstance{ID: "identity", Sessions: store},
		store:     store,
		home:      home,
		baseDir:   base,
		sessionID: meta.ID,
		key:       "agent:identity:session:" + meta.ID,
	}
	t.Cleanup(func() { require.NoError(t, h.store.Close()) })
	return h
}

func (h *cwIdentityHarness) turn(id string) *turnState {
	return newTurnState(h.agent, processOptions{
		SessionKey: h.key, TranscriptSessionID: h.sessionID, TranscriptStore: h.store,
	}, turnEventScope{turnID: id})
}

func (h *cwIdentityHarness) transcript(t *testing.T) []session.TranscriptEntry {
	t.Helper()
	entries, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)
	require.Len(t, entries, h.transcriptLines, "physical transcript rows must not be duplicated or lost")
	return entries
}

func (h *cwIdentityHarness) archive(t *testing.T) []memory.ArchivedMessage {
	t.Helper()
	entries, err := h.store.ReadArchive(context.Background(), h.key)
	require.NoError(t, err)
	require.Len(t, entries, h.archiveLines, "archive count must equal the fixture's explicit writes")
	return entries
}

func (h *cwIdentityHarness) addArchive(t *testing.T, msg providers.Message) {
	t.Helper()
	h.store.AddFullMessage(h.key, msg)
	h.archiveLines++
	got := h.archive(t)
	require.Equal(t, msg, got[h.archiveLines-1].Message, "the full input must be archived at the next physical line")
}

// Only the real method's calling shape is bridged so this RED pack compiles
// before and after the ADR's internal append-return change. Neither branch
// supplies a fake result or replaces any production method.
func (h *cwIdentityHarness) addTranscript(t *testing.T, entry session.TranscriptEntry) {
	t.Helper()
	switch appendEntry := any(h.store.AppendTranscriptStrict).(type) {
	case func(string, session.TranscriptEntry) error:
		require.NoError(t, appendEntry(h.sessionID, entry))
	case func(string, session.TranscriptEntry) (int, error):
		line, err := appendEntry(h.sessionID, entry)
		require.NoError(t, err)
		require.Equal(t, h.transcriptLines, line, "append must return the actual zero-based transcript index")
	default:
		t.Fatalf("BLOCKED: transcript append index return has an unsupported signature — required by ADR-066 §12 correction 2026-10-01")
	}
	h.transcriptLines++
	h.transcript(t)
}

func cwIdentityAppendRecordedCall(t *testing.T, ts *turnState, tc session.ToolCall, archiveLine int) {
	t.Helper()
	// The one-argument branch executes the actual legacy writer. It is not
	// an address fallback: the row-identity assertions below must still fail
	// when that legacy writer/rewriter loses the archive identity.
	switch appendCall := any(ts.appendToolCallTranscript).(type) {
	case func(session.ToolCall):
		appendCall(tc)
	case func(session.ToolCall, int):
		appendCall(tc, archiveLine)
	case func(session.ToolCall, ...int):
		appendCall(tc, archiveLine)
	case func(session.ToolCall, ...int) error:
		require.NoError(t, appendCall(tc, archiveLine))
	default:
		t.Fatalf("BLOCKED: appendToolCallTranscript cannot receive the archive line — required by ADR-066 §12 correction 2026-10-01")
	}
}

func (h *cwIdentityHarness) record(
	t *testing.T, ts *turnState, tc session.ToolCall, content string, media []string, placeholderLine *int,
) cwIdentityRecorded {
	t.Helper()
	args, err := json.Marshal(tc.Parameters)
	require.NoError(t, err)
	// Name/Arguments are non-persisted convenience fields. Use the canonical
	// function envelope, retaining the exact archive round-trip assertion.
	h.addArchive(t, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
		{ID: string(tc.ID), Function: &providers.FunctionCall{Name: tc.Tool, Arguments: string(args)}},
	}})
	// The index is derived from the fixture's explicit writes, not an ID scan.
	key := memory.ProjectionKey{ToolCallID: string(tc.ID), ArchiveLine: h.archiveLines}
	admitted := h.al.admitToolResult(ts, toolResultAdmission{
		Tool: tc.Tool, ToolCallID: string(tc.ID), Content: content, Media: media,
		IsError: tc.Status == "error", ParallelN: 1,
	})
	h.archiveLines++
	archived := h.archive(t)
	require.Equal(t, content, archived[key.ArchiveLine].Content, "admission must retain full source text")
	require.Equal(t, media, archived[key.ArchiveLine].Media, "admission must retain source media")
	if admitted.Capped {
		tc.ContentState = "capped"
	}
	if tc.Status != "error" || tc.Result != nil {
		if tc.Result == nil {
			tc.Result = make(map[string]any)
		}
		tc.Result["text"] = admitted.Message.Content
	}
	line := h.transcriptLines
	if placeholderLine != nil {
		line = *placeholderLine
	} else {
		h.transcriptLines++
	}
	cwIdentityAppendRecordedCall(t, ts, tc, admitted.ArchiveLine)
	entries := h.transcript(t)
	require.Equal(t, tc, entries[line].ToolCalls[0], "recording must append or replace the expected physical row")
	return cwIdentityRecorded{key: key, line: line, admitted: admitted}
}

func (h *cwIdentityHarness) prefix(t *testing.T) {
	t.Helper()
	h.addArchive(t, providers.Message{Role: "user", Content: "identity fixture anchor"})
	h.addTranscript(t, session.TranscriptEntry{
		ID: "identity-anchor", Role: "user", Content: "identity fixture anchor", TurnID: "anchor",
	})
}

func cwIdentityCall(id, label string) session.ToolCall {
	return session.ToolCall{
		ID: session.ToolCallID(id), Tool: "identity_tool", Status: "success",
		Parameters: map[string]any{"occurrence": label}, DurationMS: 7,
	}
}

// Independent architect/dispatcher clarification, 2026-10-01: project the
// existing text field, preserving media/sibling keys. Error-only records keep
// Result nil and carry the projected text in Error; never invent Result.text.
func cwIdentityProjectedTranscript(
	before []session.TranscriptEntry, line int, state, text string,
) []session.TranscriptEntry {
	want := append([]session.TranscriptEntry(nil), before...)
	want[line].ToolCalls = append([]session.ToolCall(nil), before[line].ToolCalls...)
	call := &want[line].ToolCalls[0]
	call.ContentState = state
	if call.Result == nil && call.Error != "" {
		call.Error = text
		return want
	}
	result := make(map[string]any, len(call.Result)+1)
	for key, value := range call.Result {
		result[key] = value
	}
	result["text"] = text
	call.Result = result
	return want
}

func (h *cwIdentityHarness) project(t *testing.T, ts *turnState, rec cwIdentityRecorded, mark string) {
	t.Helper()
	h.al.recordEmptiedOnTranscript(ts, []emptiedToolResult{{
		ToolCallID: rec.key.ToolCallID, ArchiveLine: rec.key.ArchiveLine, Mark: mark,
	}})
}

func cwIdentityTranscriptLines(t *testing.T, pm memory.ProjectionMeta) map[memory.ProjectionKey]int {
	t.Helper()
	field := reflect.ValueOf(pm).FieldByName("TranscriptLine")
	if !field.IsValid() {
		t.Fatal("BLOCKED: ProjectionMeta.TranscriptLine map[ProjectionKey]int not implemented — required by ADR-066 §12 correction 2026-10-01")
	}
	lines, ok := field.Interface().(map[memory.ProjectionKey]int)
	require.True(t, ok, "the independent ADR requires map[ProjectionKey]int, not an ID-only index")
	return lines
}

func (h *cwIdentityHarness) assertLines(t *testing.T, records ...cwIdentityRecorded) {
	t.Helper()
	want := make(map[memory.ProjectionKey]int, len(records))
	for _, rec := range records {
		want[rec.key] = rec.line
	}
	require.Equal(t, want, cwIdentityTranscriptLines(t, h.store.Projection(h.key)),
		"every recorded result, full or projected, must retain its actual transcript line")
}

func (h *cwIdentityHarness) reopen(t *testing.T) {
	t.Helper()
	require.NoError(t, h.store.Close())
	store, err := session.NewUnifiedStoreWithHome(h.baseDir, h.home)
	require.NoError(t, err)
	h.store = store
	h.agent.Sessions = store
}

func cwIdentityAddressedUpdate(
	t *testing.T, id session.ToolCallID, line int, state string, result map[string]any,
) session.ToolCallProjectionUpdate {
	t.Helper()
	u := session.ToolCallProjectionUpdate{ToolCallID: id, ContentState: state, Result: result}
	field := reflect.ValueOf(&u).Elem().FieldByName("TranscriptLine")
	if !field.IsValid() {
		t.Fatal("BLOCKED: ToolCallProjectionUpdate.TranscriptLine not implemented — required by ADR-066 §12 correction 2026-10-01")
	}
	require.Equal(t, reflect.TypeOf((*int)(nil)), field.Type(), "nil distinguishes unknown from known index zero")
	field.Set(reflect.ValueOf(&line))
	return u
}

func cwIdentityAssertUndoAddress(t *testing.T, previous []session.ToolCallProjectionUpdate, line int) {
	t.Helper()
	require.NotEmpty(t, previous, "a successful projection must return the addressed row's undo record")
	field := reflect.ValueOf(previous[len(previous)-1]).FieldByName("TranscriptLine")
	if !field.IsValid() {
		t.Fatal("BLOCKED: projection undo does not carry TranscriptLine — required by ADR-066 §12 correction 2026-10-01")
	}
	address, ok := field.Interface().(*int)
	require.True(t, ok, "undo must use the same *int address contract")
	require.NotNil(t, address, "an addressed update must not become a bare-ID undo")
	assert.Equal(t, line, *address, "undo must target the original row, not the newer duplicate")
}
