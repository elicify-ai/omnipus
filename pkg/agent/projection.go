// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// projection.go — the ONE pure projection function (ADR-066 FR-019, §6.1):
// given a slice of window messages and the persisted projection state
// keyed (tool_call_id, archive_line) → capped | emptied, it returns the
// slice the provider sees. assembleMessages applies it on every assembly
// (turn start, post-trim, reload) and the D6 mid-turn path (T066-13)
// applies it to the in-memory slice — same function, same bytes, so the
// live window and a reload agree byte for byte (B-12, B-22).
//
// It is pure: no I/O, no LLM call, no mutation of its input; it reads only
// what it is handed (the messages, the state, the cap policy and the
// archive — the last for the mark's turn number).
package agent

import (
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// projectionContext carries the two things the projection needs beyond the
// messages and the state: the per-call cap policy (the same snapshot the
// choke point used) and the archive, consulted only for the mark's turn
// number (1 + the role:user lines before the result's line — including
// evicted ones, which the window slice cannot see).
type projectionContext struct {
	policy  resultCapPolicy
	archive []memory.ArchivedMessage
}

// projectMessages applies set to msgs. lineOf maps a window index to its
// archive line (−1 for a message that is not on the archive — an injected
// system note, a spliced recall span); only tool messages with a line in
// the set are touched. Returns a new slice; msgs is never modified.
//
// capped: the result is re-cut head-and-tail at the cap the surface and the
// owning call's parallel count yield under pc.policy. The surface is taken
// from the owning assistant call's tool name; success is assumed because the
// window does not record IsError — a FAILURE-surface result that was capped
// live therefore re-projects at its success cap on reload (it stays bounded
// and correctly marked, but may show more of its content than the model saw
// live). Recording the effective cap with the state would close that gap;
// it is a pkg/memory shape change deliberately not made here.
//
// emptied: the content becomes the emptied recall mark and nothing else.
func projectMessages(
	msgs []providers.Message,
	lineOf func(i int) int,
	set memory.ProjectionSet,
	pc projectionContext,
) []providers.Message {
	out := append([]providers.Message(nil), msgs...)
	if len(set) == 0 {
		return out
	}
	for i, m := range msgs {
		if m.Role != "tool" || m.ToolCallID == "" {
			continue
		}
		line := lineOf(i)
		if line < 0 {
			continue
		}
		state, ok := set[memory.ProjectionKey{ToolCallID: m.ToolCallID, ArchiveLine: line}]
		if !ok {
			continue
		}
		tool, parallelN := owningToolCall(msgs, i, m.ToolCallID)
		switch state {
		case memory.ProjectionCapped:
			capChars := pc.policy.effectiveCap(toolResultSurfaceFor(tool, false), parallelN)
			out[i].Content, _ = projectToolResult(m.Content, capChars, func(full string) string {
				return capMarkOrEmpty(tool, m.ToolCallID, line, full, pc.archive)
			})
		case memory.ProjectionEmptied:
			// Size from the archive line (the full result), not from the
			// handed-in copy — same source the live pass uses
			// (empty_in_place.go's markSourceContent), same bytes.
			mark, err := buildRecallMark("emptied", tool, m.ToolCallID, line, markSourceContent(m, line, pc.archive), pc.archive)
			if err != nil {
				// buildRecallMark already reported the marshal failure. An
				// empty content is still "emptied" — the window must not
				// keep the bytes the state says are gone.
				mark = ""
			}
			out[i].Content = mark
		}
	}
	return out
}

// owningToolCall finds the assistant message before index i that issued
// toolCallID and returns its tool name and its number of tool calls (the
// parallel N of FR-011). Unknown → ("", 1): the mark then says "(unknown
// tool)" and the cap is the single-call cap.
func owningToolCall(msgs []providers.Message, i int, toolCallID string) (string, int) {
	for j := i - 1; j >= 0; j-- {
		if msgs[j].Role != "assistant" || len(msgs[j].ToolCalls) == 0 {
			continue
		}
		for _, tc := range msgs[j].ToolCalls {
			if tc.ID != toolCallID {
				continue
			}
			name := tc.Name
			if name == "" && tc.Function != nil {
				name = tc.Function.Name
			}
			return name, len(msgs[j].ToolCalls)
		}
	}
	return "", 1
}

// archiveLineResolver returns the lineOf function assembleMessages hands
// projectMessages: when history is the archive's tail (the normal case —
// GetHistory returns the lines from Skip), index i is line skip+i. When it
// is not (orphan recovery rewrote the slice), every tool message is located
// by its most recent (tool_call_id, content) match in the archive so the
// state still lands on the right line.
func archiveLineResolver(archive []memory.ArchivedMessage, history []providers.Message) func(i int) int {
	skip := len(archive) - len(history)
	aligned := skip >= 0
	if aligned {
		for i := range history {
			a := archive[skip+i].Message
			if a.Role != history[i].Role || a.ToolCallID != history[i].ToolCallID {
				aligned = false
				break
			}
		}
	}
	if aligned {
		return func(i int) int { return skip + i }
	}
	return func(i int) int {
		if i < 0 || i >= len(history) || history[i].Role != "tool" {
			return -1
		}
		for j := len(archive) - 1; j >= 0; j-- {
			if archive[j].Role == "tool" && archive[j].ToolCallID == history[i].ToolCallID && archive[j].Content == history[i].Content {
				return j
			}
		}
		return -1
	}
}
