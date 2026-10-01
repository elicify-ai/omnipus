package agent

import (
	"fmt"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// retainedSourceRunes resolves a projection against its full archive source.
// An exact recorded limit takes precedence over any subsequent settings change.
func retainedSourceRunes(msgs []providers.Message, i, line int, state memory.ProjectionState, pc projectionContext) (int, error) {
	m := msgs[i]
	if line < 0 || line >= len(pc.archive) || pc.archive[line].Role != "tool" || pc.archive[line].ToolCallID != m.ToolCallID {
		return 0, fmt.Errorf("context projection: invalid archive identity for %q", m.ToolCallID)
	}
	n := utf8.RuneCountInString(pc.archive[line].Content)
	key := memory.ProjectionKey{ToolCallID: m.ToolCallID, ArchiveLine: line}
	if k, exact := pc.sourceRunes[key]; exact {
		if k < 0 || k > n {
			return 0, fmt.Errorf("context projection: source limit %d outside 0..%d", k, n)
		}
		return k, nil
	}
	if state == memory.ProjectionEmptied {
		return 0, nil
	}
	if state == "" {
		return n, nil
	}
	if state != memory.ProjectionCapped && state != memory.ProjectionCappedFailure {
		return 0, fmt.Errorf("context projection: unknown state %q", state)
	}
	tool, parallelN := owningToolCall(msgs, i, m.ToolCallID)
	capChars := pc.policy.effectiveCap(toolResultSurfaceFor(tool, state == memory.ProjectionCappedFailure), parallelN)
	if n <= capChars {
		return n, nil
	}
	mark, err := buildRecallMark("capped", tool, m.ToolCallID, line, pc.archive[line].Content, turnNumberForArchiveLine(pc.archive, line))
	if err != nil {
		return 0, err
	}
	return max(0, capChars-utf8.RuneCountInString(mark)-2), nil
}

func projectMessagesChecked(msgs []providers.Message, lineOf func(int) int, set memory.ProjectionSet, pc projectionContext) ([]providers.Message, error) {
	out := append([]providers.Message(nil), msgs...)
	for i, m := range msgs {
		if m.Role != "tool" || m.ToolCallID == "" {
			continue
		}
		line := lineOf(i)
		key := memory.ProjectionKey{ToolCallID: m.ToolCallID, ArchiveLine: line}
		state, present := set[key]
		if !present {
			continue
		}
		k, err := retainedSourceRunes(msgs, i, line, state, pc)
		if err != nil {
			return nil, err
		}
		tool, _ := owningToolCall(msgs, i, m.ToolCallID)
		out[i].Content, err = projectSource(pc.archive, line, k, tool, m.ToolCallID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
