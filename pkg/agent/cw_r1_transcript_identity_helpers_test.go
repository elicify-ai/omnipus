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
//
// stream-C R1 migration (architect-ruled, 2026-10-01): project() no longer
// drives pkg/agent/empty_in_place.go::recordEmptiedOnTranscript — that path
// has zero production callers (emptyInPlace, its only caller, is itself
// uncalled) and is scheduled for deletion on a separate branch. project()
// now drives the real, live mechanism — window_trim_checked.go::
// trimWindowChecked -> window_projection_effects.go::commitWindowProjections
// — under genuine budget pressure; see project's own doc comment below for
// the full account, including why trimWindowChecked (not checkpointWindow)
// and the one load-bearing constraint it forced on the fixtures (every
// result the pack projects must be long enough that the real recall mark
// shrinks it, not grows it).
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

// identityProjectionTool is the one tool name every scenario's fixture uses
// (cwIdentityCall sets session.ToolCall.Tool, archived as
// providers.ToolCall.Function.Name) — hardcoding it here rather than
// re-deriving it via owningToolCall on every call is safe for exactly that
// reason, confirmed directly against owningToolCall's own resolution
// (projection.go::owningToolCall reads tc.Name, falling back to
// tc.Function.Name, which cwIdentityCall sets to "identity_tool").
const identityProjectionTool = "identity_tool"

// project drives the REAL D5 "empty tool result in place" mechanism —
// pkg/agent/window_trim_checked.go::trimWindowChecked, the live pre-turn /
// forced-recovery entry point the architect's R1 ruling named, confirmed to
// call commitWindowProjections directly (window_trim_checked.go:105) —
// under genuine budget pressure computed from rec's own ALREADY-ARCHIVED
// content and the real recall-mark builder (buildRecallMark), never a
// hand-built emptiedToolResult.
//
// trimWindowChecked is used for all five scenarios rather than
// checkpointWindow (the other live entry point commitWindowProjections has,
// window_checkpoint.go:192): checkpointWindow's slideOldest step
// (window_relief.go::slideOldest) EVICTS an entire older turn wholesale —
// advances Skip past it, deletes its projection/transcript-line entries —
// the instant a second complete tool-call step exists in the window, which
// is the wrong semantic for a composite-identity test: it removes the row
// instead of emptying it in place, and it never reaches
// commitWindowProjections for what it evicted, so the transcript is never
// rewritten. trimWindowChecked never calls slideOldest. Its whole-turn cut
// (trimWholeTurns) is a guaranteed no-op here because every fixture has
// exactly one leading role:user archive line (h.prefix) and no later one:
// parseTurnBoundaries' only candidate (index 0) fails its own `end>0` guard.
//
// ts is registered as the session's active turn state (al.activeTurnStates)
// for the call's duration so commitWindowProjections' recordWindowProjections
// — which no-ops when ts.transcriptStore/ts.transcriptSessionID are unset —
// actually rewrites the transcript; trimWindowChecked looks the active ts up
// by session key exactly as the real pre-turn and timeout-recovery call
// sites do (al.getActiveTurnState, window_trim_checked.go:29).
//
// Returns the real recall mark buildRecallMark produces for rec — the exact
// mark commitWindowProjections writes, since both call the same producer
// with the same inputs (recall_mark.go's documented FR-019 byte-identity
// contract) — computed from rec's own known archived content BEFORE the
// real call runs, so the assertion's oracle is the mark's documented
// contract, never this call's own observed output. ok reports whether a
// real projection change actually committed: false means the window
// already fit (used to prove the mechanism's per-key idempotency for real,
// rather than asserting it synthetically — see
// TestCWIdentity_RepeatedUpdatesAbortToOriginalAndKeepAddress).
func (h *cwIdentityHarness) project(t *testing.T, ts *turnState, rec cwIdentityRecorded) (mark string, ok bool) {
	t.Helper()
	h.al.activeTurnStates.Store(h.key, ts)
	defer h.al.activeTurnStates.Delete(h.key)

	snap, err := h.store.SnapshotWindow(context.Background(), h.key)
	require.NoError(t, err)
	msgs, lines := memory.WindowHistory(snap)
	idx := -1
	for i, line := range lines {
		if line == rec.key.ArchiveLine {
			idx = i
			break
		}
	}
	require.GreaterOrEqual(t, idx, 0, "project target must be mapped into the current window")

	full := snap.Archive[rec.key.ArchiveLine].Content
	turn := turnNumberForArchiveLine(denseArchive(snap.Archive), rec.key.ArchiveLine)
	computedMark, err := buildRecallMark("emptied", identityProjectionTool, rec.key.ToolCallID, rec.key.ArchiveLine, full, turn)
	require.NoError(t, err)

	// projected is what trimWindowChecked's own p.messages equals BEFORE any
	// shortening this call might do — the window's CURRENT persisted view,
	// which already differs from the raw archive content whenever an earlier
	// real admission (tool_result_window.go::admitResultWindow) capped this
	// same result at the door. policy is left zero-value: retainedSourceRunes
	// only consults it when a capped/capped_failure key has no persisted
	// exact SourceRunes entry, which never happens here — every capped
	// admission in this pack persists its exact kept amount at admission
	// time (admitResultWindow sets after.Projection.SourceRunes[key]).
	pc := projectionContext{archive: denseArchive(snap.Archive), sourceRunes: snap.State.Projection.SourceRunes}
	projected, err := projectMessagesChecked(msgs, func(i int) int { return lines[i] }, snap.State.Projection.Entries, pc)
	require.NoError(t, err)

	before := sumMessageTokens(projected)
	curTok := estimateMessageTokens(providers.Message{Role: "tool", ToolCallID: rec.key.ToolCallID, Content: projected[idx].Content})
	markTok := estimateMessageTokens(providers.Message{Role: "tool", ToolCallID: rec.key.ToolCallID, Content: computedMark})
	after := before - curTok + markTok
	// If the window already carries the mark (a repeat call against an
	// already-Emptied key — see the idempotency check in
	// TestCWIdentity_RepeatedUpdatesAbortToOriginalAndKeepAddress), curTok
	// and markTok are identical and after==before by construction: that is
	// the correct, expected outcome of this call, not a fixture problem.
	// Only a genuinely NOT-yet-minimal result that still fails to shrink is
	// a fixture-sizing defect.
	alreadyMark := projected[idx].Content == computedMark
	if !alreadyMark {
		require.Less(t, after, before,
			"fixture content must be long enough that the real mark genuinely shrinks the window "+
				"(buildRecallMark has ~285 chars of fixed overhead, measured directly against the "+
				"real function) — otherwise no budget could ever make trimWindowChecked settle "+
				"after exactly one real empty of this result; this is the live mechanism's own "+
				"constraint, not a test artifact")
	}

	// Fix a large window and solve MaxTokens so agentContextBudget() == after
	// exactly (B = W - MaxTokens - ceil(0.05W) - pinnedCoreOverheadTokens —
	// context_budget.go::contextBudget): budget==after means fits() is
	// satisfied the instant — and only once — rec's own result is fully
	// emptied. Before that, before>after forces the real trim loop to run;
	// a second call against an already-projected window starts with
	// before<=after and short-circuits with NothingToTrim (ok=false).
	h.agent.ContextWindow = 200000
	headroom := (h.agent.ContextWindow + 19) / 20
	h.agent.MaxTokens = h.agent.ContextWindow - headroom - pinnedCoreOverheadTokens(h.agent) - after

	_, ok = h.al.trimWindowChecked(context.Background(), h.agent, "", h.key, false)
	return computedMark, ok
}

// projectOK is project for the common case: the caller requires the real
// pressure to actually commit (ok=true) and only needs the mark back.
func (h *cwIdentityHarness) projectOK(t *testing.T, ts *turnState, rec cwIdentityRecorded) string {
	t.Helper()
	mark, ok := h.project(t, ts, rec)
	require.True(t, ok, "real D5 budget pressure must actually empty the addressed result")
	return mark
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
