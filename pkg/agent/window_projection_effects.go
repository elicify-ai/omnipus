package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type windowProjectionChange struct {
	key          memory.ProjectionKey
	state        memory.ProjectionState
	text, mark   string
	transcriptAt *session.ArchiveAddress
}

// Build every mark before any persistence or publication. Updates are ordered by
// retained archive position, not by a session-wide id map or map iteration.
func (p *windowCheckpoint) projectionChanges() ([]windowProjectionChange, error) {
	var changes []windowProjectionChange
	for i, m := range p.messages {
		if m.Role != "tool" || p.lines[i] < 0 {
			continue
		}
		key := memory.ProjectionKey{ToolCallID: m.ToolCallID, ArchiveLine: p.lines[i]}
		state := p.state.Projection.Entries[key]
		if state == "" {
			continue
		}
		old := p.snapshot.State.Projection
		oldLimit, oldKnown := old.SourceRunes[key]
		limit, known := p.state.Projection.SourceRunes[key]
		if old.Entries[key] == state && oldKnown == known && oldLimit == limit {
			continue
		}
		tool, _ := owningToolCall(p.messages, i, key.ToolCallID)
		public := publicProjectionState(state)
		source, ok := p.archive().message(key.ArchiveLine)
		if !ok {
			return nil, fmt.Errorf("context checkpoint: tool result %q at archive line %d is not in the window", key.ToolCallID, key.ArchiveLine)
		}
		mark, err := buildRecallMark(string(public), tool, key.ToolCallID, key.ArchiveLine,
			source.Content, turnNumberForArchiveLine(p.archive(), key.ArchiveLine))
		if err != nil {
			return nil, err
		}
		change := windowProjectionChange{key: key, state: public, text: m.Content, mark: mark}
		if addr, ok := p.state.Projection.TranscriptAddr[key]; ok {
			change.transcriptAt = &addr
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func publicProjectionState(state memory.ProjectionState) memory.ProjectionState {
	if state == memory.ProjectionCappedFailure {
		return memory.ProjectionCapped
	}
	return state
}

func recordWindowProjections(ts *turnState, changes []windowProjectionChange) error {
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return nil
	}
	edits := make([]session.ToolCallProjectionEdit, 0, len(changes))
	for _, c := range changes {
		// A just-admitted result is recorded by finishCall after this checkpoint.
		// No bare-id guess can substitute for a missing archive mapping.
		if c.transcriptAt == nil {
			continue
		}
		edits = append(edits, session.ToolCallProjectionEdit{
			Target: *c.transcriptAt, ToolCallID: session.ToolCallID(c.key.ToolCallID),
			ContentState: string(c.state), Text: c.text,
		})
	}
	if len(edits) == 0 {
		return nil
	}
	effect, err := ts.transcriptStore.ProjectToolCalls(ts.transcriptSessionID, edits)
	if err != nil {
		return fmt.Errorf("context checkpoint: update transcript projections: %w", err)
	}
	ts.mu.Lock()
	ts.projectionEffects = append(ts.projectionEffects, effect)
	ts.mu.Unlock()
	return nil
}

func (al *AgentLoop) commitWindowProjections(ctx context.Context, p *windowCheckpoint, store session.ContextWindowStore) ([]windowProjectionChange, error) {
	changes, err := p.projectionChanges()
	if err != nil {
		return nil, err
	}
	if err := store.CommitWindow(ctx, p.ts.sessionKey, p.snapshot.State, p.state); err != nil {
		return nil, err
	}
	if err := recordWindowProjections(p.ts, changes); err != nil {
		// No slice, notice or frame has been installed. Undo this staged metadata
		// commit; a failed transcript rewrite did not modify its on-disk bytes.
		restoreErr := store.RestoreWindow(context.Background(), p.ts.sessionKey, p.state, p.snapshot.State)
		return nil, errors.Join(err, restoreErr)
	}
	return changes, nil
}

func (al *AgentLoop) emitWindowProjectionEvents(ts *turnState, changes []windowProjectionChange) {
	for _, c := range changes {
		if c.transcriptAt == nil {
			continue // finishCall publishes newly recorded results after tool.end.
		}
		al.emitWindowProjection(ts, c)
	}
}

func (al *AgentLoop) emitWindowProjection(ts *turnState, c windowProjectionChange) {
	al.emitEvent(EventKindToolResultProjection, ts.eventMeta("contextCheckpoint", "turn.context.projection"),
		ToolResultProjectionPayload{
			ChatID: ts.chatID, SessionID: u9ToolExecSessionIDs(ts),
			ToolCallID: session.ToolCallID(c.key.ToolCallID), ArchiveLine: c.key.ArchiveLine,
			ContentState: string(c.state), Mark: c.mark, AgentID: ts.resolveActiveAgentID(),
		})
}
