package session

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/memory"
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

	CommitWindow(ctx context.Context, key string, before, after memory.WindowState) error
	RestoreWindow(ctx context.Context, key string, before, after memory.WindowState) error
	RollbackWindow(ctx context.Context, key string, start memory.WindowState) error
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
	// Effects design D2: a model append is a write to the session archive, so it
	// takes the session shard first (shard, then backend, then store lock).
	h := us.lockSession(owningSessionID(key))
	defer h.Unlock()
	return us.backend.AppendModelMessage(ctx, key, in)
}

func (us *UnifiedStore) PlaceSavedInput(ctx context.Context, key string, source ArchiveAddress) (ModelSlot, WindowView, error) {
	// Effects design D2: a model append is a write to the session archive, so it
	// takes the session shard first (shard, then backend, then store lock).
	h := us.lockSession(owningSessionID(key))
	defer h.Unlock()
	return us.backend.PlaceSavedInput(ctx, key, source)
}

func (us *UnifiedStore) ReadModelSlots(ctx context.Context, key string, from, to int, fn func(ModelSlot, []byte) error) error {
	return us.backend.ReadModelSlots(ctx, key, from, to, fn)
}
