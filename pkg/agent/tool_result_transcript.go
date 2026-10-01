package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ErrNoWindowProjection signals the deliberate no-op: this result was not
// admitted into a capped projection (not capped, NoHistory, or no archive
// line), so there is nothing to record — not a failure. Callers check for
// this sentinel via errors.Is to distinguish it from a real persistence error.
var ErrNoWindowProjection = errors.New("window projection: nothing to record for this result")

// resultTranscriptSnapshot validates the admitted archive address before writing
// its transcript record. This also gives the metadata compare-and-commit base.
func (ts *turnState) resultTranscriptSnapshot(tc session.ToolCall, line int) (session.ContextWindowStore, memory.WindowSnapshot, error) {
	store, ok := ts.agent.Sessions.(session.ContextWindowStore)
	if !ok {
		return nil, memory.WindowSnapshot{}, fmt.Errorf("context transcript: session store does not support atomic context checkpoints")
	}
	snap, err := store.SnapshotWindow(context.Background(), ts.sessionKey)
	if err != nil {
		return nil, snap, err
	}
	if line < snap.State.Skip || line >= len(snap.Archive) {
		return nil, snap, fmt.Errorf("context transcript: archive line %d is outside the retained window", line)
	}
	m := snap.Archive[line]
	if m.Role != "tool" || m.ToolCallID != string(tc.ID) {
		return nil, snap, fmt.Errorf("context transcript: archive line %d does not address tool call %q", line, tc.ID)
	}
	return store, snap, nil
}

func (ts *turnState) persistResultTranscriptLine(store session.ContextWindowStore, snap memory.WindowSnapshot, tc session.ToolCall, archiveLine, transcriptLine int) error {
	after := snap.State.Clone()
	key := memory.ProjectionKey{ToolCallID: string(tc.ID), ArchiveLine: archiveLine}
	after.Projection.TranscriptLine[key] = transcriptLine
	if err := store.CommitWindow(context.Background(), ts.sessionKey, snap.State, after); err != nil {
		return fmt.Errorf("context transcript: persist exact result identity: %w", err)
	}
	return nil
}

func (ex *agentLoopRunTurnToolsExecute) recordAdmittedTranscript() error {
	ts := ex.rx.rr.rq.ri.rf.rt.ts
	if ts.opts.NoHistory || ex.admitted.ArchiveLine < 0 {
		return ts.appendToolCallTranscript(ex.tcRecord)
	}
	return ts.appendToolCallTranscript(ex.tcRecord, ex.admitted.ArchiveLine)
}

func (ex *agentLoopRunTurnToolsExecute) recordedProjection() (*windowProjectionChange, error) {
	ts := ex.rx.rr.rq.ri.rf.rt.ts
	if !ex.admitted.Capped || ts.opts.NoHistory || ex.admitted.ArchiveLine < 0 {
		return nil, ErrNoWindowProjection
	}
	_, snap, err := ts.resultTranscriptSnapshot(ex.tcRecord, ex.admitted.ArchiveLine)
	if err != nil {
		return nil, err
	}
	key := memory.ProjectionKey{ToolCallID: ex.toolCallID, ArchiveLine: ex.admitted.ArchiveLine}
	state := publicProjectionState(ex.admitted.Projection)
	mark, err := buildRecallMark(string(state), ex.toolName, key.ToolCallID, key.ArchiveLine,
		snap.Archive[key.ArchiveLine].Content, turnNumberForArchiveLine(snap.Archive, key.ArchiveLine))
	if err != nil {
		return nil, err
	}
	return &windowProjectionChange{key: key, state: state, text: ex.toolResultMsg.Content, mark: mark}, nil
}

// projectRecordedResult replaces text only, retaining descriptors, error flags,
// status, arguments, parent identity and duration supplied by normal recording.
func (ex *agentLoopRunTurnToolsExecute) projectRecordedResult() {
	text := ex.toolResultMsg.Content
	if ex.toolResult.IsError && ex.tcRecord.Result == nil {
		ex.tcRecord.Error = text
	} else {
		result := make(map[string]any, len(ex.tcRecord.Result)+1)
		for k, v := range ex.tcRecord.Result {
			result[k] = v
		}
		result["text"] = text
		ex.tcRecord.Result = result
	}
	if ex.admitted.Capped {
		ex.tcRecord.ContentState = string(memory.ProjectionCapped)
		if ex.admitted.Projection == memory.ProjectionEmptied {
			ex.tcRecord.ContentState = string(memory.ProjectionEmptied)
		}
	}
}
