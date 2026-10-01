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
	AppendWindowMessage(ctx context.Context, key string, msg providers.Message) (memory.WindowSnapshot, error)
	SnapshotWindow(ctx context.Context, key string) (memory.WindowSnapshot, error)
	CommitWindow(ctx context.Context, key string, before, after memory.WindowState) error
	RestoreWindow(ctx context.Context, key string, before, after memory.WindowState) error
	RollbackWindow(ctx context.Context, key string, start memory.WindowState) error
}

func (b *JSONLBackend) windowStore() (ContextWindowStore, error) {
	s, ok := b.store.(ContextWindowStore)
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
