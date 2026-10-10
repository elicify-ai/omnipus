package session

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ContextWindowStore is the error-returning checkpoint seam. It is separate
// from the legacy fire-and-forget session interface, not a second archive.
type ContextWindowStore interface {
	// WindowView is the bounded snapshot (live slots, anchor, turn counters).
	WindowView(ctx context.Context, key string) (WindowView, error)
	// AppendModelMessage is the checked append: producer-chosen membership,
	// trusted source and, for a tool result, the issuing assistant's address.
	AppendModelMessage(ctx context.Context, key string, in ModelAppend) (ModelSlot, WindowView, error)
	// PlaceSavedInput places a saved chat-only input into model order once.
	PlaceSavedInput(ctx context.Context, key string, source ArchiveAddress) (ModelSlot, WindowView, error)
	// ReadModelSlots streams the slots with ordinals in [from, to] through the
	// ordinal index, with each slot's literal stored model_message JSON.
	ReadModelSlots(ctx context.Context, key string, from, to int, fn func(ModelSlot, []byte) error) error

	AppendWindowMessage(ctx context.Context, key string, msg providers.Message) (memory.WindowSnapshot, error)
	SnapshotWindow(ctx context.Context, key string) (memory.WindowSnapshot, error)
	CommitWindow(ctx context.Context, key string, before, after memory.WindowState) error
	RestoreWindow(ctx context.Context, key string, before, after memory.WindowState) error
	RollbackWindow(ctx context.Context, key string, start memory.WindowState) error
}

// jsonlWindowStore is the dense checkpoint surface memory.JSONLStore provides.
type jsonlWindowStore interface {
	AppendWindowMessage(ctx context.Context, key string, msg providers.Message) (memory.WindowSnapshot, error)
	SnapshotWindow(ctx context.Context, key string) (memory.WindowSnapshot, error)
	CommitWindow(ctx context.Context, key string, before, after memory.WindowState) error
	RestoreWindow(ctx context.Context, key string, before, after memory.WindowState) error
	RollbackWindow(ctx context.Context, key string, start memory.WindowState) error
}

func (b *JSONLBackend) windowStore() (jsonlWindowStore, error) {
	s, ok := b.store.(jsonlWindowStore)
	if !ok {
		return nil, fmt.Errorf("session: store does not support atomic context checkpoints")
	}
	return s, nil
}

func (b *JSONLBackend) AppendWindowMessage(ctx context.Context, key string, msg providers.Message) (memory.WindowSnapshot, error) {
	s, err := b.windowStore()
	if err != nil {
		return memory.WindowSnapshot{}, err
	}
	return s.AppendWindowMessage(ctx, key, msg)
}

func (b *JSONLBackend) SnapshotWindow(ctx context.Context, key string) (memory.WindowSnapshot, error) {
	s, err := b.windowStore()
	if err != nil {
		return memory.WindowSnapshot{}, err
	}
	return s.SnapshotWindow(ctx, key)
}
func (b *JSONLBackend) CommitWindow(ctx context.Context, key string, before, after memory.WindowState) error {
	s, err := b.windowStore()
	if err != nil {
		return err
	}
	return s.CommitWindow(ctx, key, before, after)
}
func (b *JSONLBackend) RestoreWindow(ctx context.Context, key string, before, after memory.WindowState) error {
	s, err := b.windowStore()
	if err != nil {
		return err
	}
	return s.RestoreWindow(ctx, key, before, after)
}

func (b *JSONLBackend) RollbackWindow(ctx context.Context, key string, start memory.WindowState) error {
	s, err := b.windowStore()
	if err != nil {
		return err
	}
	return s.RollbackWindow(ctx, key, start)
}

func (us *UnifiedStore) AppendWindowMessage(ctx context.Context, key string, msg providers.Message) (memory.WindowSnapshot, error) {
	return us.backend.AppendWindowMessage(ctx, key, msg)
}

func (us *UnifiedStore) SnapshotWindow(ctx context.Context, key string) (memory.WindowSnapshot, error) {
	return us.backend.SnapshotWindow(ctx, key)
}
func (us *UnifiedStore) CommitWindow(ctx context.Context, key string, before, after memory.WindowState) error {
	return us.backend.CommitWindow(ctx, key, before, after)
}
func (us *UnifiedStore) RestoreWindow(ctx context.Context, key string, before, after memory.WindowState) error {
	return us.backend.RestoreWindow(ctx, key, before, after)
}

func (us *UnifiedStore) RollbackWindow(ctx context.Context, key string, start memory.WindowState) error {
	return us.backend.RollbackWindow(ctx, key, start)
}

func (us *UnifiedStore) WindowView(ctx context.Context, key string) (WindowView, error) {
	return us.backend.WindowView(ctx, key)
}

func (us *UnifiedStore) AppendModelMessage(ctx context.Context, key string, in ModelAppend) (ModelSlot, WindowView, error) {
	return us.backend.AppendModelMessage(ctx, key, in)
}

func (us *UnifiedStore) PlaceSavedInput(ctx context.Context, key string, source ArchiveAddress) (ModelSlot, WindowView, error) {
	return us.backend.PlaceSavedInput(ctx, key, source)
}

func (us *UnifiedStore) ReadModelSlots(ctx context.Context, key string, from, to int, fn func(ModelSlot, []byte) error) error {
	return us.backend.ReadModelSlots(ctx, key, from, to, fn)
}

// --- legacy JSONL backend adapter ---
//
// The JSONL backend has no ordinal index, no addresses and no chat-only records:
// it adapts its dense snapshot to the bounded view (every slot loaded, zero
// addresses) so a store built on it keeps serving the checkpoint seam. It is the
// legacy fallback (DEL-10) and is removed with it.

func (b *JSONLBackend) WindowView(ctx context.Context, key string) (WindowView, error) {
	snap, err := b.SnapshotWindow(ctx, key)
	if err != nil {
		return WindowView{}, err
	}
	return WindowViewFromSnapshot(snap), nil
}

func (b *JSONLBackend) AppendModelMessage(ctx context.Context, key string, in ModelAppend) (ModelSlot, WindowView, error) {
	snap, err := b.AppendWindowMessage(ctx, key, in.Message)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	view := WindowViewFromSnapshot(snap)
	slot, ok := view.Slot(len(snap.Archive) - 1)
	if !ok {
		return ModelSlot{}, WindowView{}, fmt.Errorf("session: appended message is not in the window")
	}
	return slot, view, nil
}

func (b *JSONLBackend) PlaceSavedInput(context.Context, string, ArchiveAddress) (ModelSlot, WindowView, error) {
	return ModelSlot{}, WindowView{}, fmt.Errorf("session: the legacy JSONL backend has no saved-input placement")
}

func (b *JSONLBackend) ReadModelSlots(ctx context.Context, key string, from, to int, fn func(ModelSlot, []byte) error) error {
	return b.ScanArchiveRange(ctx, key, from, to, func(i int, raw []byte, m memory.ArchivedMessage) error {
		return fn(ModelSlot{Ordinal: i, Message: m.Message, TS: m.TS}, raw)
	})
}

// WindowViewFromSnapshot converts a dense snapshot into a bounded view with
// every slot loaded (the legacy JSONL backend, and fixtures that hold one).
func WindowViewFromSnapshot(snap memory.WindowSnapshot) WindowView {
	v := WindowView{State: snap.State.Clone(), Excluded: append([]memory.ArchiveSpan(nil), snap.Retracted...)}
	if skip := min(snap.State.Skip, len(snap.Archive)); skip > 0 {
		start := 0
		for i := skip - 1; i >= 0 && skip-i <= leadLimit; i-- {
			if r := snap.Archive[i].Role; r == "user" || r == "assistant" {
				start = i
				break
			}
		}
		for i := start; i < skip; i++ {
			v.Lead = append(v.Lead, ModelSlot{Ordinal: i, Message: snap.Archive[i].Message, TS: snap.Archive[i].TS})
		}
	}
	turns := 0
	for i, m := range snap.Archive {
		if m.Role == "user" {
			turns++
		}
		if i < snap.State.Skip {
			v.PriorUserTurns = turns
			if snap.State.AnchorLine != nil && *snap.State.AnchorLine == i && !retractedOrdinal(snap.Retracted, i) {
				v.Anchor = &ModelSlot{Ordinal: i, Message: m.Message, TS: m.TS, UserTurn: turns}
			}
			continue
		}
		v.turnsAfter = append(v.turnsAfter, turns)
		if retractedOrdinal(snap.Retracted, i) {
			continue
		}
		v.Live = append(v.Live, ModelSlot{Ordinal: i, Message: m.Message, TS: m.TS, UserTurn: turns})
	}
	return v
}
