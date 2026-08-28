// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// empty_in_place.go — ADR-066 D5 (spec FR-017, FR-019, FR-022, FR-023,
// T066-12): when the window is over budget, the oldest ELIGIBLE tool results
// are emptied in place — their content becomes the recall mark, their slot,
// role and tool_call_id stay, so the call/answer structure the providers
// validate is untouched and nothing is cut mid-turn. The archive is never
// modified (ADR-028 append-only): the emptied state is persisted as
// (tool_call_id, archive_line) → emptied beside Skip, and the ONE projection
// function (projection.go) re-applies the same mark on every later
// assembly, so the bytes the provider sees live and after a reload are
// identical (B-22).
//
// Eligible (FR-017) = every role:"tool" message whose assistant call is in
// the slice — including results of an earlier, completed turn the pre-turn
// floor kept (register #3, B-21b) — EXCLUDING the floor set: every result of
// the most recent assistant message that issued tool calls. Emptying runs
// oldest-first (slice order) until the caller's fit predicate holds or no
// eligible result remains (FR-021: one pass to target per trigger).
//
// Two call sites share this file: windowTrim's pre-turn floor path (this
// task) and the D6 mid-turn check after every admitted result (T066-13).
// Both hand the pass the slice the NEXT request is built from and a lineOf
// resolver; the pass mutates that slice in place and persists the state.
package agent

import (
	"sync/atomic"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// contextEmptiesTotal backs omnipus_context_empties_total (FR-023,
// US-6.AC10): tool results emptied in place, summed over every pass.
var contextEmptiesTotal atomic.Int64

// ContextEmptiesTotal returns the context_empties_total counter. Rendered
// by the gateway's /metrics handler (pkg/gateway/metrics.go).
func ContextEmptiesTotal() int64 { return contextEmptiesTotal.Load() }

// Emptying sites, named in the INFO line so an operator can tell a pre-turn
// floor empty (register #3) from a mid-turn one (D6).
const (
	emptyingSitePreTurn = "pre_turn"
	emptyingSiteMidTurn = "mid_turn"
)

// emptiedToolResult describes one result the pass emptied.
type emptiedToolResult struct {
	// Index is the message's position in the slice handed to the pass.
	Index       int
	ToolCallID  string
	ArchiveLine int
	Tool        string
	// SizeChars is the rune count of the content the mark replaced.
	SizeChars int
	Mark      string
}

// eligibleToolResults returns, oldest-first, the indices of msgs that D5 may
// empty (FR-017): role:"tool" with a tool_call_id, resolvable to an archive
// line (lineOf ≥ 0 — an injected note or a spliced recall span is not on
// the archive), whose owning assistant call is in msgs, excluding the floor
// set (every result of the LAST assistant message that issued tool calls)
// and anything the persisted set already marks emptied (re-emptying a mark
// would be a no-op that still counted as work).
func eligibleToolResults(msgs []providers.Message, lineOf func(int) int, set memory.ProjectionSet) []int {
	floor := floorResultIndices(msgs)
	var out []int
	for i, m := range msgs {
		if m.Role != "tool" || m.ToolCallID == "" {
			continue
		}
		if _, isFloor := floor[i]; isFloor {
			continue
		}
		line := lineOf(i)
		if line < 0 {
			continue
		}
		if !callPresent(msgs, i, m.ToolCallID) {
			// The call is not in the slice (orphan): emptying it would not
			// free a result the model can still pair; the recovery path
			// (session_recovery.go) owns orphans, not D5.
			continue
		}
		if set[memory.ProjectionKey{ToolCallID: m.ToolCallID, ArchiveLine: line}] == memory.ProjectionEmptied {
			continue
		}
		out = append(out, i)
	}
	return out
}

// floorResultIndices returns the SLICE INDICES of the FR-031 floor set: the
// results of the most recent assistant message that issued tool calls.
//
// It is keyed on the index, never on the bare tool_call_id. Ids are NOT
// unique within a session — memory.ProjectionKey is composite precisely
// because "providers reuse ids such as call_0 on every turn" (B-29b) — so an
// id-keyed floor swallowed every OLDER result that happened to share an id
// with the current step. With a provider that numbers calls per message, that
// made the entire window ineligible, emptied nothing, and turned D6's thrash
// guard into a turn-killer while large evictable results sat in the window.
//
// The floor's results are exactly the tool messages that FOLLOW that
// assistant message and answer one of its calls; anything before it belongs
// to an earlier, completed step and is eligible (register #3, B-21b).
func floorResultIndices(msgs []providers.Message) map[int]struct{} {
	floor := make(map[int]struct{})
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		ids := make(map[string]struct{}, len(msgs[i].ToolCalls))
		for _, tc := range msgs[i].ToolCalls {
			ids[tc.ID] = struct{}{}
		}
		for k := i + 1; k < len(msgs); k++ {
			if msgs[k].Role != "tool" || msgs[k].ToolCallID == "" {
				continue
			}
			if _, ok := ids[msgs[k].ToolCallID]; ok {
				floor[k] = struct{}{}
			}
		}
		break
	}
	return floor
}

// callPresent reports whether an assistant message before index i issued
// toolCallID (owningToolCall's "" answer is ambiguous between "not found"
// and "a call with no tool name", so the presence check is separate).
func callPresent(msgs []providers.Message, i int, toolCallID string) bool {
	for j := i - 1; j >= 0; j-- {
		if msgs[j].Role != "assistant" {
			continue
		}
		for _, tc := range msgs[j].ToolCalls {
			if tc.ID == toolCallID {
				return true
			}
		}
	}
	return false
}

// emptyOldestFirst replaces msgs[i].Content with the emptied recall mark for
// the candidates in order, stopping as soon as fits(msgs) reports the slice
// fits (checked after every empty) or the candidates are exhausted. It
// mutates msgs in place — it IS the in-memory view the next request is built
// from (ADR-066 §6.1) — and returns what it emptied. The mark is the same
// byte string projection.go produces for the persisted state, by
// construction: both call buildRecallMark with the same inputs.
func emptyOldestFirst(
	msgs []providers.Message,
	candidates []int,
	lineOf func(int) int,
	archive []memory.ArchivedMessage,
	fits func([]providers.Message) bool,
) []emptiedToolResult {
	if fits(msgs) {
		return nil
	}
	var emptied []emptiedToolResult
	for _, i := range candidates {
		m := msgs[i]
		line := lineOf(i)
		tool, _ := owningToolCall(msgs, i, m.ToolCallID)
		full := markSourceContent(m, line, archive)
		mark, err := buildRecallMark("emptied", tool, m.ToolCallID, line, full, turnNumberForArchiveLine(archive, line))
		if err != nil {
			// buildRecallMark reported the marshal failure. The content is
			// still gone — the window must not keep bytes the persisted
			// state says are emptied (projection.go applies the same rule).
			mark = ""
		}
		emptied = append(emptied, emptiedToolResult{
			Index:       i,
			ToolCallID:  m.ToolCallID,
			ArchiveLine: line,
			Tool:        tool,
			SizeChars:   utf8.RuneCountInString(full),
			Mark:        mark,
		})
		msgs[i].Content = mark
		if fits(msgs) {
			break
		}
	}
	return emptied
}

// markSourceContent returns the content the emptied mark describes: the
// ARCHIVE line's full content when line addresses one (the in-memory copy
// may already be the capped form, and the mark's size_chars must name the
// full result whichever copy the pass was handed — that is what keeps the
// live bytes and projection.go's reload bytes identical, B-22), else the
// message's own content.
func markSourceContent(m providers.Message, line int, archive []memory.ArchivedMessage) string {
	if line >= 0 && line < len(archive) && archive[line].Role == "tool" && archive[line].ToolCallID == m.ToolCallID {
		return archive[line].Content
	}
	return m.Content
}

// toolResultShareTokens is D6's `share`: the estimator tokens of every
// role:"tool" message in msgs (the second trigger condition, FR-024).
func toolResultShareTokens(msgs []providers.Message) int {
	share := 0
	for _, m := range msgs {
		if m.Role == "tool" {
			share += estimateMessageTokens(m)
		}
	}
	return share
}

// midTurnLineResolver returns the lineOf function for the in-memory slice
// the tool loop builds (pinned core + breadcrumb + window + this turn's
// appended messages).
//
// It is POSITION-AWARE, not id-keyed. The archive is append-only and the
// slice's tool messages appear in the same relative order as their archive
// lines (the window is an archive suffix; the choke point appends this
// turn's results before the slice does), so a single reverse walk that
// CONSUMES each matched line assigns every message its own line — including
// when a provider reuses an id such as call_0 on every turn (B-29b, the very
// reason memory.ProjectionKey is composite).
//
// Resolving by id alone took the most recent line for every message carrying
// that id, so two results sharing an id both resolved to the newer line: the
// mark described the wrong content, and the persisted (id, line) → emptied
// entry made the reload projection blank a DIFFERENT, newer result than the
// live pass emptied — the live/reload byte-identity B-22 requires.
//
// A message that is not on the archive (a system note, a spliced recall
// span) matches nothing, resolves to −1 and consumes no line.
func midTurnLineResolver(archive []memory.ArchivedMessage, msgs []providers.Message) func(int) int {
	lines := make(map[int]int, len(msgs))
	next := len(archive) - 1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "tool" || msgs[i].ToolCallID == "" {
			continue
		}
		found := -1
		for j := next; j >= 0; j-- {
			if archive[j].Role == "tool" && archive[j].ToolCallID == msgs[i].ToolCallID {
				found = j
				break
			}
		}
		lines[i] = found
		if found >= 0 {
			// Consume: an EARLIER message can only own an earlier line.
			next = found - 1
		}
	}
	return func(i int) int {
		if line, ok := lines[i]; ok {
			return line
		}
		return -1
	}
}

// emptyInPlace is the D5 pass with its side effects: it empties the oldest
// eligible results of msgs (in place) until fits holds, persists each
// (tool_call_id, archive_line) → emptied in the session's projection state,
// rewrites the matching transcript tool_call records (content_state +
// projected result, FR-022) remembering their previous state on ts for an
// abort revert, emits one tool_result_projection event per result, logs ONE
// INFO line (session key, count, share before/after — FR-023) and bumps
// context_empties_total. ts may be nil (a model-switch trim outside any
// turn): state is still persisted and logged; the transcript and the frame
// need a turn and are skipped.
//
// archive is the session archive as read by ReadArchive (for the mark's
// turn number and the caller's lineOf). Returns what was emptied; an empty
// result means either the slice already fit or nothing was eligible — the
// caller distinguishes by re-checking its own condition (the D6 guard).
func (al *AgentLoop) emptyInPlace(
	ts *turnState,
	agent *AgentInstance,
	sessionKey string,
	msgs []providers.Message,
	lineOf func(int) int,
	archive []memory.ArchivedMessage,
	fits func([]providers.Message) bool,
	site string,
) []emptiedToolResult {
	if agent == nil || agent.Sessions == nil {
		return nil
	}
	shareBefore := toolResultShareTokens(msgs)
	set := agent.Sessions.Projection(sessionKey).Entries
	candidates := eligibleToolResults(msgs, lineOf, set)
	emptied := emptyOldestFirst(msgs, candidates, lineOf, archive, fits)
	if len(emptied) == 0 {
		return nil
	}

	for _, e := range emptied {
		agent.Sessions.SetProjectionState(sessionKey,
			memory.ProjectionKey{ToolCallID: e.ToolCallID, ArchiveLine: e.ArchiveLine}, memory.ProjectionEmptied)
	}
	contextEmptiesTotal.Add(int64(len(emptied)))

	if ts != nil {
		al.recordEmptiedOnTranscript(ts, emptied)
		al.emitProjectionEvents(ts, emptied)
	}

	logger.InfoCF("agent", "emptied tool results in place (ADR-066 D5)",
		map[string]any{
			"session_key":           sessionKey,
			"agent_id":              agent.ID,
			"site":                  site,
			"count":                 len(emptied),
			"share_before":          shareBefore,
			"share_after":           toolResultShareTokens(msgs),
			"context_empties_total": contextEmptiesTotal.Load(),
		})
	return emptied
}

// recordEmptiedOnTranscript rewrites the transcript tool_call records of the
// emptied results — content_state "emptied", result = the mark the model now
// sees (FR-022: "the transcript result is the PROJECTED content") — and keeps
// the previous rows on ts so restoreSession can put them back if the turn
// aborts (the window's projection set rolls back to turn start; the
// transcript must follow). Best-effort, like every transcript write.
func (al *AgentLoop) recordEmptiedOnTranscript(ts *turnState, emptied []emptiedToolResult) {
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return
	}
	updates := make([]session.ToolCallProjectionUpdate, 0, len(emptied))
	for _, e := range emptied {
		updates = append(updates, session.ToolCallProjectionUpdate{
			ToolCallID:   session.ToolCallID(e.ToolCallID),
			ContentState: string(memory.ProjectionEmptied),
			Result:       map[string]any{"text": e.Mark},
		})
	}
	prev, err := ts.transcriptStore.UpdateToolCallProjections(ts.transcriptSessionID, updates)
	if err != nil {
		logger.WarnCF("agent", "emptying: transcript content_state update failed",
			map[string]any{"session_id": ts.transcriptSessionID, "count": len(updates), "error": err.Error()})
		return
	}
	ts.mu.Lock()
	ts.emptiedTranscriptPrev = append(ts.emptiedTranscriptPrev, prev...)
	ts.mu.Unlock()
}

// emitProjectionEvents emits one EventKindToolResultProjection per emptied
// result, with the ADR-057 routing / producing session ids the tool_call
// frames use (u9ToolExecSessionIDs).
func (al *AgentLoop) emitProjectionEvents(ts *turnState, emptied []emptiedToolResult) {
	sid, producingSID := u9ToolExecSessionIDs(ts)
	for _, e := range emptied {
		al.emitEvent(
			EventKindToolResultProjection,
			ts.eventMeta("emptyInPlace", "turn.context.projection"),
			ToolResultProjectionPayload{
				ChatID:             ts.chatID,
				SessionID:          sid,
				ProducingSessionID: producingSID,
				ToolCallID:         session.ToolCallID(e.ToolCallID),
				ArchiveLine:        e.ArchiveLine,
				ContentState:       string(memory.ProjectionEmptied),
				Mark:               e.Mark,
				AgentID:            ts.resolveActiveAgentID(),
			},
		)
	}
}
