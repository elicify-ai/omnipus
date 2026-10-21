package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

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
	sessionID := owningSessionID(key)
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if err := ctx.Err(); err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	if err := us.recoverSavedInputLifecycleLocked(sessionID); err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	state, err := loadSavedInputLifecycle(filepath.Join(us.baseDir, sessionID), sessionID)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	if expired, ok := state.ExpiredReports[source.EntryID]; ok {
		if expired.Source != source {
			return ModelSlot{}, WindowView{}, fmt.Errorf("saved input source conflicts with its exact expiry receipt")
		}
		return ModelSlot{}, WindowView{}, state.expiry(source.EntryID)
	}
	if poll, ok := state.PollConsumed[source.EntryID]; ok {
		if poll.Source != nil && *poll.Source != source {
			return ModelSlot{}, WindowView{}, fmt.Errorf("saved input source conflicts with its poll receipt")
		}
		return ModelSlot{}, WindowView{}, ErrAlreadyConsumed
	}
	for receiptID, notice := range state.Notices {
		if notice.Source == nil || *notice.Source != source {
			continue
		}
		if notice.ModelAdmitted {
			return ModelSlot{}, WindowView{}, ErrAlreadyConsumed
		}
		if state.retiredNoticeSource(source) {
			e, _ := state.ExpiredReportsByReceipt(receiptID)
			slot, view, err := us.backend.placeRetentionNoticeModelOnly(ctx, key, e)
			if err == nil || errors.Is(err, ErrAlreadyConsumed) {
				err = errors.Join(err, us.acknowledgeRetentionNoticeLocked(sessionID, source))
			}
			return slot, view, err
		}
	}
	fromOrdinal, indexed, err := us.savedInputFromOrdinalLocked(sessionID, source)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	var slot ModelSlot
	var view WindowView
	if indexed {
		slot, view, err = us.backend.placeSavedInput(ctx, key, source, fromOrdinal)
	} else {
		slot, view, err = us.backend.PlaceSavedInput(ctx, key, source)
	}
	if err == nil || errors.Is(err, ErrAlreadyConsumed) {
		// Placed now or earlier: retire the pending hint, then acknowledge only
		// the notice whose positive ordinal admission was actually published.
		if indexed {
			if retireErr := us.retireSavedInputByAddressLocked(sessionID, source); retireErr != nil {
				return slot, view, errors.Join(err, retireErr)
			}
		}
		err = errors.Join(err, us.acknowledgeRetentionNoticeLocked(sessionID, source))
	}
	return slot, view, err
}

func (us *UnifiedStore) ReadModelSlots(ctx context.Context, key string, from, to int, fn func(ModelSlot, []byte) error) error {
	return us.backend.ReadModelSlots(ctx, key, from, to, fn)
}
