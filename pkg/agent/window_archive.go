package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// windowArchive is what projection, turn numbering and recall marks need from the
// retained model window (session-core U2 Decision C): the archived message of a
// retained slot by its stable ordinal, and the FR-018 turn number at an ordinal.
// It replaces the dense lifetime archive slice — a slot outside the window is an
// explicit miss, never an empty message.
type windowArchive interface {
	// message returns the archived message at ordinal line, ok=false when the
	// slot is not loaded in this window.
	message(line int) (memory.ArchivedMessage, bool)
	// turnBefore returns 1 + the number of user messages strictly before line
	// (FR-018). A negative or unknown line yields 1.
	turnBefore(line int) int
}

// viewArchive serves a bounded session.WindowView.
type viewArchive struct{ view session.WindowView }

func (a viewArchive) message(line int) (memory.ArchivedMessage, bool) {
	s, ok := a.view.Slot(line)
	if !ok {
		return memory.ArchivedMessage{}, false
	}
	return memory.ArchivedMessage{Message: s.Message, TS: s.TS}, true
}

func (a viewArchive) turnBefore(line int) int {
	if n, ok := a.view.TurnBefore(line); ok {
		return n
	}
	return 1
}

// denseArchive serves a fully loaded archive (a store with no ordinal index, or
// a test fixture): every ordinal is present.
type denseArchive []memory.ArchivedMessage

func (d denseArchive) message(line int) (memory.ArchivedMessage, bool) {
	if line < 0 || line >= len(d) {
		return memory.ArchivedMessage{}, false
	}
	return d[line], true
}

func (d denseArchive) turnBefore(line int) int {
	n := 1
	for i, m := range d {
		if i >= line {
			break
		}
		if m.Role == "user" {
			n++
		}
	}
	return n
}

// slotsBefore reads evicted slots for the breadcrumb. It is the only consumer of
// the evicted prefix and reads it through the ordinal index, newest first, only
// as far as the breadcrumb budget needs.
type slotsBefore interface {
	// readSlots calls fn for every ordinal in [from, to] ascending with its
	// message and the running user-turn count at that ordinal.
	readSlots(ctx context.Context, from, to int, fn func(idx, turn int, msg memory.ArchivedMessage) error) error
}

// storeSlots reads through ReadModelSlots.
type storeSlots struct {
	store session.ContextWindowStore
	key   string
}

func (s storeSlots) readSlots(ctx context.Context, from, to int, fn func(int, int, memory.ArchivedMessage) error) error {
	return s.store.ReadModelSlots(ctx, s.key, from, to, func(slot session.ModelSlot, _ []byte) error {
		return fn(slot.Ordinal, slot.UserTurn, memory.ArchivedMessage{Message: slot.Message, TS: slot.TS})
	})
}

// readSlots serves a dense archive (tests, fully loaded stores).
func (d denseArchive) readSlots(_ context.Context, from, to int, fn func(int, int, memory.ArchivedMessage) error) error {
	if from < 0 || to >= len(d) || to < from {
		return fmt.Errorf("window archive: range [%d,%d] outside %d slots", from, to, len(d))
	}
	turn := 0
	for i := 0; i < from; i++ {
		if d[i].Role == "user" {
			turn++
		}
	}
	for i := from; i <= to; i++ {
		if d[i].Role == "user" {
			turn++
		}
		if err := fn(i, turn, d[i]); err != nil {
			return err
		}
	}
	return nil
}
