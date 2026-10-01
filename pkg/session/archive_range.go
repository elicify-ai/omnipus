package session

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/memory"
)

// ScanArchiveRange forwards literal JSONL access without a materializing or
// re-encoding fallback: a decoded-only backend cannot promise original bytes.
func (b *JSONLBackend) ScanArchiveRange(ctx context.Context, key string, from, to int, fn func(int, []byte, memory.ArchivedMessage) error) error {
	reader, ok := b.store.(memory.ArchiveRangeScanner)
	if !ok {
		return fmt.Errorf("session: archive_range requires a literal JSONL archive reader")
	}
	return reader.ScanArchiveRange(ctx, key, from, to, fn)
}

// ScanEvictedArchive forwards the persisted Skip and addressed evicted records.
func (b *JSONLBackend) ScanEvictedArchive(ctx context.Context, key string, fn func(int, []byte, memory.ArchivedMessage) error) (int, error) {
	reader, ok := b.store.(memory.EvictedArchiveScanner)
	if !ok {
		return 0, fmt.Errorf("session: persisted evicted archive reader unavailable")
	}
	return reader.ScanEvictedArchive(ctx, key, fn)
}

// ScanArchiveRange exposes the same literal bounded scan on the live store used
// by per-agent recall registration, not just the lower-level JSONL backend.
func (us *UnifiedStore) ScanArchiveRange(ctx context.Context, key string, from, to int, fn func(int, []byte, memory.ArchivedMessage) error) error {
	return us.backend.ScanArchiveRange(ctx, key, from, to, fn)
}

// ScanEvictedArchive keeps breadcrumb identity independent of assembled history.
func (us *UnifiedStore) ScanEvictedArchive(ctx context.Context, key string, fn func(int, []byte, memory.ArchivedMessage) error) (int, error) {
	return us.backend.ScanEvictedArchive(ctx, key, fn)
}
