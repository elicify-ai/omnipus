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
	transcriptAt *int
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
		if line, ok := p.state.Projection.TranscriptLine[key]; ok {
			change.transcriptAt = &line
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
	updates := make([]session.ToolCallProjectionUpdate, 0, len(changes))
	for _, c := range changes {
		// A just-admitted result is recorded by finishCall after this checkpoint.
		// No bare-id guess can substitute for a missing archive mapping.
		if c.transcriptAt == nil {
			continue
		}
		text := c.text
		updates = append(updates, session.ToolCallProjectionUpdate{
			ToolCallID: session.ToolCallID(c.key.ToolCallID), TranscriptLine: c.transcriptAt,
			ContentState: string(c.state), Text: &text,
		})
	}
	previous, err := ts.transcriptStore.UpdateToolCallProjections(ts.transcriptSessionID, updates)
	if err != nil {
		return fmt.Errorf("context checkpoint: update transcript projections: %w", err)
	}
	ts.mu.Lock()
	ts.emptiedTranscriptPrev = append(ts.emptiedTranscriptPrev, previous...)
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
