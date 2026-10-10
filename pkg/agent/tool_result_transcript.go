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
func (ts *turnState) resultTranscriptSnapshot(tc session.ToolCall, line int) (session.ContextWindowStore, session.WindowView, error) {
	store, ok := ts.agent.Sessions.(session.ContextWindowStore)
	if !ok {
		return nil, session.WindowView{}, fmt.Errorf("context transcript: session store does not support atomic context checkpoints")
	}
	snap, err := store.WindowView(context.Background(), ts.sessionKey)
	if err != nil {
		return nil, snap, err
	}
	m, ok := snap.Slot(line)
	if line < snap.State.Skip || !ok {
		return nil, snap, fmt.Errorf("context transcript: archive line %d is outside the retained window", line)
	}
	if m.Message.Role != "tool" || m.Message.ToolCallID != string(tc.ID) {
		return nil, snap, fmt.Errorf("context transcript: archive line %d does not address tool call %q", line, tc.ID)
	}
	return store, snap, nil
}

func (ts *turnState) persistResultTranscriptAddr(store session.ContextWindowStore, snap session.WindowView, tc session.ToolCall, archiveLine int, transcriptAddr session.ArchiveAddress) error {
	after := snap.State.Clone()
	key := memory.ProjectionKey{ToolCallID: string(tc.ID), ArchiveLine: archiveLine}
	after.Projection.TranscriptAddr[key] = transcriptAddr
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
	source, _ := snap.Slot(key.ArchiveLine) // resultTranscriptSnapshot proved the slot is loaded
	mark, err := buildRecallMark(string(state), ex.toolName, key.ToolCallID, key.ArchiveLine,
		source.Message.Content, turnNumberForArchiveLine(viewArchive{view: snap}, key.ArchiveLine))
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
		result := make(map[string]any)
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
