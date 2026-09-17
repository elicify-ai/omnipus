// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// recall_mark.go — the ONE producer of the recall mark that stands in for a
// tool result's content in the model's window (ADR-066 D5 and D4, spec
// FR-018, B-21, T066-04).
//
// The mark is a typed ToolResultRecallMark payload (contracts/asyncapi.yaml,
// ADR-060 family) encoded by pkg/tools' marshalWithinBudget — never a
// string-assembled sentence. It states the tool name and tool_call_id
// (both sanitised to at most 64 printable runes — a hostile MCP tool name,
// edge case E6, cannot smuggle control sequences into the window or the
// SPA), the zero-based archive line, the full result's size in characters,
// the turn number recall_conversation's turn_range mode addresses, and the
// literal recall_conversation call that returns the content in pages.
//
// Two states, one shape: `emptied` when D5 replaces the content in place
// because the window is over budget, `capped` when the D4 choke point admits
// a head-and-tail cut (the mark's own length counts toward the cap, FR-011).
// The emptying pass (T066-12) and the choke point (T066-05) are the two
// callers; the projection function (T066-05/12) re-applies the same mark
// on reload so live and reload bytes are identical (FR-019).
package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// buildRecallMark returns the JSON recall mark for the tool result at
// archiveLine whose full (filtered, as archived) content is content.
// state is "emptied" or "capped"; any other value is refused because
// `full` is never a mark (it is ToolCall.content_state's default and only
// ever read from the transcript).
//
// turn is the 1-based turn number the mark cites (FR-018) — callers compute
// it themselves (typically via turnNumberForArchiveLine against whatever
// archive they have in scope, or a value they already tracked) rather than
// handing this function an archive to re-derive it from.
func buildRecallMark(state, tool, toolCallID string, archiveLine int, content string, turn int) (string, error) {
	params := tools.RecallMarkParams{
		Tool:        tool,
		ToolCallID:  toolCallID,
		ArchiveLine: archiveLine,
		SizeChars:   len([]rune(content)),
		Turn:        turn,
	}
	var (
		encoded []byte
		err     error
	)
	switch state {
	case "emptied":
		encoded, err = tools.EmptiedMarkPayload(params)
	case "capped":
		encoded, err = tools.CappedMarkPayload(params)
	default:
		return "", fmt.Errorf("recall mark: unknown content state %q (want emptied or capped)", state)
	}
	if err != nil {
		// Observable, never silent: the mark replaces content the model can
		// no longer see, so losing the mark loses the only pointer back to
		// it. Every family producer reports its marshal fallback this way.
		tools.ReportStructuredFailureMarshalError("pkg/agent.buildRecallMark", tool, tools.ToolResultRecallMarkCode, err)
		return "", err
	}
	return string(encoded), nil
}

// turnNumberForArchiveLine returns 1 + the count of role:user archive lines
// strictly preceding line (FR-018). A system line, an assistant line or a
// tool line never starts a turn; the user line AT line is not "preceding".
// A line past the end counts every user line; a negative line counts none.
func turnNumberForArchiveLine(archive []memory.ArchivedMessage, line int) int {
	n := 1
	for i, m := range archive {
		if i >= line {
			break
		}
		if m.Role == "user" {
			n++
		}
	}
	return n
}
