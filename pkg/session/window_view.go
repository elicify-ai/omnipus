// window_view.go: the bounded model window a turn works with — session-core
// C-ARCHIVE / U2 Decision C, caller-migration steps 1-3.
//
// A WindowView replaces the dense lifetime snapshot (memory.WindowSnapshot with
// every archived message from ordinal 0). It carries ONLY what the window needs:
// the live slots [Skip, Count) without the retained-but-excluded ones, the
// optional original-user anchor, and the integers needed to number turns. Work
// to build it is the active window plus one anchor read, never the evicted
// prefix, and a slot outside it is an explicit "not in window" (Slot returns
// ok=false), never an empty message.
package session

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ModelSlot is one model placement with its message loaded.
type ModelSlot struct {
	// Ordinal is the stable `archive_line` model-slot ordinal.
	Ordinal int
	// Addr is the address of the record that occupies the slot (the payload, or
	// the model_ref placement). It is the exact identity a producer keeps, e.g. an
	// assistant's address passed to its tool results.
	Addr    ArchiveAddress
	Message providers.Message
	// TS is the payload's timestamp, unix seconds (0 when unknown).
	TS int64
	// UserTurn counts the user messages at ordinals <= Ordinal.
	UserTurn int
	// Origin is the record's CONV provenance marker (ModelOriginConvArchive /
	// ModelOriginConvRebuilt); empty for a record written live. A rebuilt record
	// has no provable original tool-result bytes.
	Origin string
}

// WindowView is the bounded snapshot of one session's model window.
type WindowView struct {
	State memory.WindowState
	// Live holds the slots in [State.Skip, State.Count) that are not excluded,
	// in model order.
	Live []ModelSlot
	// Anchor is the original user message pinned before Skip, when present.
	Anchor *ModelSlot
	// Excluded lists the retained-but-excluded spans (FR-006 rollback effects).
	Excluded []memory.ArchiveSpan
	// Lead is the evicted tail that an open tool group still owns: the slots
	// directly before State.Skip back to and including the nearest user or
	// assistant slot. Restart-cancellation binding needs them (a marker inside
	// the window can belong to a declaration before Skip); they are never part of
	// the model view. Bounded by leadLimit.
	Lead []ModelSlot
	// PriorUserTurns counts the user messages at ordinals < State.Skip.
	PriorUserTurns int

	// turnsAfter[i] is the UserTurn of ordinal State.Skip+i, for every ordinal in
	// the window including excluded ones (rows are content-free).
	turnsAfter []int
}

// leadLimit bounds how many evicted slots a view reads for an open group.
const leadLimit = 256

// Slot returns the loaded slot with the given ordinal: a live slot or the
// anchor. ok is false for anything else (evicted, excluded or out of range).
func (v WindowView) Slot(ordinal int) (ModelSlot, bool) {
	if v.Anchor != nil && v.Anchor.Ordinal == ordinal {
		return *v.Anchor, true
	}
	// Live is ordered by ordinal; an excluded span leaves gaps, so search.
	lo, hi := 0, len(v.Live)
	for lo < hi {
		mid := (lo + hi) / 2
		if v.Live[mid].Ordinal < ordinal {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(v.Live) && v.Live[lo].Ordinal == ordinal {
		return v.Live[lo], true
	}
	return ModelSlot{}, false
}

// TurnBefore returns 1 + the number of user messages strictly before ordinal
// (FR-018's turn number). It answers for ordinals in [State.Skip, State.Count];
// ok is false below Skip, where the answer needs an indexed range read.
func (v WindowView) TurnBefore(ordinal int) (int, bool) {
	if ordinal < v.State.Skip || ordinal > v.State.Count {
		return 0, false
	}
	if ordinal == v.State.Skip {
		return v.PriorUserTurns + 1, true
	}
	return v.turnsAfter[ordinal-v.State.Skip-1] + 1, true
}

// History returns the provider messages the window represents (anchor first,
// then the live slots) and the ordinal each came from.
func (v WindowView) History() ([]providers.Message, []int) {
	n := len(v.Live)
	if v.Anchor != nil {
		n++
	}
	msgs := make([]providers.Message, 0, n)
	lines := make([]int, 0, n)
	if v.Anchor != nil {
		msgs = append(msgs, v.Anchor.Message)
		lines = append(lines, v.Anchor.Ordinal)
	}
	for _, s := range v.Live {
		msgs = append(msgs, s.Message)
		lines = append(lines, s.Ordinal)
	}
	return msgs, lines
}

// ModelAppend is one checked model-message append. Every member is chosen by
// the producer: there is no role-derived default (Decision A).
type ModelAppend struct {
	Message providers.Message
	// ViewMembership is required: ViewMembershipModel for a model-only control
	// or result, ViewMembershipBoth for a synchronous chat+model record.
	ViewMembership string
	// Source is the trusted, server-minted provenance. Kind is required.
	Source EntrySource
	// ToolResultFor is required for a role "tool" message: the exact address of
	// the assistant record that issued the call. The backend reads that record
	// and refuses an append whose issuer does not declare the call.
	ToolResultFor *ArchiveAddress
	// AgentID attributes the record when known.
	AgentID string
}

func (a ModelAppend) validate() error {
	switch a.ViewMembership {
	case ViewMembershipModel, ViewMembershipBoth:
	case "":
		return errors.New("model append: view_membership is required and producer-chosen")
	default:
		return fmt.Errorf("model append: a model slot cannot be %q membership", a.ViewMembership)
	}
	if a.Source.Kind == "" {
		return errors.New("model append: a trusted source kind is required")
	}
	if a.Message.Role == "tool" {
		if a.Message.ToolCallID == "" {
			return errors.New("model append: a tool result has no tool_call_id")
		}
		if a.ToolResultFor == nil {
			return errors.New("model append: a tool result must name the assistant record that issued its call")
		}
	} else if a.ToolResultFor != nil {
		return errors.New("model append: tool_result_for is only valid on a tool result")
	}
	return nil
}
