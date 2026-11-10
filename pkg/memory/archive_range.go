package memory

import "context"

// ArchiveRangeScanner preserves original JSONL bytes, unlike a decoded archive
// read. Record addresses count nonempty physical lines, including corrupt ones;
// corrupt selected records fail the scan rather than silently shifting addresses.
// The raw slice is borrowed until fn returns and includes its original newline.
// No callback may reenter the store for the same session.
type ArchiveRangeScanner interface {
	ScanArchiveRange(ctx context.Context, key string, from, to int, fn func(int, []byte, ArchivedMessage) error) error
}

// EvictedArchiveScanner reads Skip and the corresponding archive under the same
// session lock. The returned Skip, not an assembled history's length, identifies
// the evicted prefix. It is returned even when the prefix scan fails.
type EvictedArchiveScanner interface {
	ScanEvictedArchive(ctx context.Context, key string, fn func(int, []byte, ArchivedMessage) error) (skip int, err error)
}
