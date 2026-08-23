package memory

import (
	"errors"
	"fmt"
	"sort"
)

// ProjectionState is the per-result projection state recorded in session
// meta (ADR-066 D4/D5, FR-019). The archive line itself is never modified
// (ADR-028 append-only); the state tells the projection function how the
// in-memory / provider view of that line differs from the archived bytes.
//
// This type is internal persistence state — it is NOT a gateway/SPA wire
// type. The SPA-facing `content_state` on the transcript is a separate,
// contract-defined enum (contracts/components/schemas/ToolCall.yaml).
type ProjectionState string

const (
	// ProjectionCapped — the archive holds the full result; the window
	// carries the D4 head-and-tail capped form plus the cap mark.
	ProjectionCapped ProjectionState = "capped"
	// ProjectionEmptied — the archive holds the full result; the window
	// carries only the D5 recall mark.
	ProjectionEmptied ProjectionState = "emptied"
)

// ErrArchiveNotEmpty is returned by SetHistory when the session archive
// already holds at least one line (FR-047, US-15.AC5). Hydration may only
// fill an empty archive; rewriting an existing one would reset Skip and
// destroy evicted turns (SC-001).
var ErrArchiveNotEmpty = errors.New("memory: set history refused: archive is not empty")

// ProjectionKey addresses one archived tool result. The key is composite
// because tool_call_ids are not unique across a session (B-29b: providers
// reuse ids such as call_0 on every turn) — the archive line index is what
// makes the address exact.
type ProjectionKey struct {
	ToolCallID  string
	ArchiveLine int
}

// ProjectionSet is the in-memory form of the persisted projection state:
// one entry per (tool_call_id, archive_line) that is capped or emptied.
// Absent key == full content.
type ProjectionSet map[ProjectionKey]ProjectionState

// Clone returns an independent copy (nil in → empty, non-nil out).
func (p ProjectionSet) Clone() ProjectionSet {
	out := make(ProjectionSet, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// ProjectionMeta is what a reader gets back: the set plus the D5.5
// hydrated flag (FR-048 — an archive rebuilt from the UI transcript cannot
// answer recall-by-id with the original bytes).
type ProjectionMeta struct {
	Entries  ProjectionSet
	Hydrated bool
}

// validProjectionState reports whether s is one of the two known states.
func validProjectionState(s ProjectionState) bool {
	return s == ProjectionCapped || s == ProjectionEmptied
}

// validateProjectionWrite checks a SetProjectionState request.
func validateProjectionWrite(pk ProjectionKey, state ProjectionState) error {
	if pk.ToolCallID == "" {
		return errors.New("memory: projection: empty tool_call_id")
	}
	if pk.ArchiveLine < 0 {
		return fmt.Errorf("memory: projection: negative archive_line %d", pk.ArchiveLine)
	}
	if !validProjectionState(state) {
		return fmt.Errorf("memory: projection: unknown state %q", state)
	}
	return nil
}

// projectionEntry is the on-disk form of one ProjectionSet entry. A JSON
// object cannot carry a struct map key, so the set is persisted as a
// slice sorted by (archive_line, tool_call_id) — deterministic bytes so a
// meta file written twice from the same state is identical.
type projectionEntry struct {
	ToolCallID  string          `json:"tool_call_id"`
	ArchiveLine int             `json:"archive_line"`
	State       ProjectionState `json:"state"`
}

// projectionToEntries flattens a set for persistence.
func projectionToEntries(p ProjectionSet) []projectionEntry {
	if len(p) == 0 {
		return nil
	}
	out := make([]projectionEntry, 0, len(p))
	for k, v := range p {
		out = append(out, projectionEntry{ToolCallID: k.ToolCallID, ArchiveLine: k.ArchiveLine, State: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ArchiveLine != out[j].ArchiveLine {
			return out[i].ArchiveLine < out[j].ArchiveLine
		}
		return out[i].ToolCallID < out[j].ToolCallID
	})
	return out
}

// projectionFromEntries rebuilds a set from the persisted slice. Entries
// that fail validation (hand-edited meta, future state values) are dropped
// rather than poisoning the session: a dropped entry degrades to "full
// content", which is the safe direction.
func projectionFromEntries(entries []projectionEntry) ProjectionSet {
	out := make(ProjectionSet, len(entries))
	for _, e := range entries {
		pk := ProjectionKey{ToolCallID: e.ToolCallID, ArchiveLine: e.ArchiveLine}
		if validateProjectionWrite(pk, e.State) != nil {
			continue
		}
		out[pk] = e.State
	}
	return out
}

// pruneProjectionBelow drops every entry whose archive_line < skip
// (US-6.AC9 — evicted lines have no window view to project).
func pruneProjectionBelow(p ProjectionSet, skip int) ProjectionSet {
	out := make(ProjectionSet, len(p))
	for k, v := range p {
		if k.ArchiveLine >= skip {
			out[k] = v
		}
	}
	return out
}

// rollbackProjection computes the post-abort set (FR-020, US-6.AC5):
//
//   - every entry (current or turn-start) with archive_line ≥ targetLines
//     is dropped — those lines no longer exist;
//   - current `capped` entries below targetLines are kept — capping
//     happens at append time, so a capped pre-turn line was capped at turn
//     start too;
//   - turn-start entries below targetLines are restored verbatim and win
//     over the current state, undoing every mid-turn empty of a pre-turn
//     line;
//   - current `emptied` entries below targetLines that are absent from the
//     turn-start set were emptied during the aborted turn and go.
//
// Callers pass the whole turn-start set (both states) so a capped→emptied
// transition during the turn rolls back to capped, not to full.
func rollbackProjection(current, turnStart ProjectionSet, targetLines int) ProjectionSet {
	out := make(ProjectionSet, len(turnStart))
	for k, v := range current {
		if k.ArchiveLine < targetLines && v == ProjectionCapped {
			out[k] = v
		}
	}
	for k, v := range turnStart {
		if k.ArchiveLine < targetLines {
			out[k] = v
		}
	}
	return out
}
