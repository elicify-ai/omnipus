package session

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/memory"
)

// ScanArchiveRange exposes the same literal bounded scan on the live store used
// by per-agent recall registration, not just the lower-level JSONL backend.
func (us *UnifiedStore) ScanArchiveRange(ctx context.Context, key string, from, to int, fn func(int, []byte, memory.ArchivedMessage) error) error {
	return us.backend.ScanArchiveRange(ctx, key, from, to, fn)
}

// ScanEvictedArchive keeps breadcrumb identity independent of assembled history.
func (us *UnifiedStore) ScanEvictedArchive(ctx context.Context, key string, fn func(int, []byte, memory.ArchivedMessage) error) (int, error) {
	return us.backend.ScanEvictedArchive(ctx, key, fn)
}
