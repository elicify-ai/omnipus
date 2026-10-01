package memory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

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

const archiveReadChunk = 64 * 1024

// ScanArchiveRange reads an inclusive, zero-based range from the existing log.
// It retains one bounded record at a time, never the selected range.
func (s *JSONLStore) ScanArchiveRange(ctx context.Context, key string, from, to int, fn func(int, []byte, ArchivedMessage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l := s.sessionLock(key)
	l.Lock()
	defer l.Unlock()
	return s.scanArchiveRangeLocked(ctx, key, from, to, fn)
}

// ScanEvictedArchive streams only the prefix identified by actual persisted Skip.
func (s *JSONLStore) ScanEvictedArchive(ctx context.Context, key string, fn func(int, []byte, ArchivedMessage) error) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	l := s.sessionLock(key)
	l.Lock()
	defer l.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	meta, err := s.readMeta(key)
	if err != nil {
		return 0, err
	}
	if meta.Skip == 0 {
		return 0, nil
	}
	return meta.Skip, s.scanArchiveRangeLocked(ctx, key, 0, meta.Skip-1, fn)
}

func (s *JSONLStore) scanArchiveRangeLocked(ctx context.Context, key string, from, to int, fn func(int, []byte, ArchivedMessage) error) (err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	f, err := os.Open(s.jsonlPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("archive_range [%d,%d] outside available archive records (0 records)", from, to)
	}
	if err != nil {
		return fmt.Errorf("memory: open archive: %w", err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("memory: stat archive: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("memory: archive is not a regular file: %s", info.Mode())
	}
	return ScanJSONLRange(ctx, f, from, to, fn)
}

// archiveContextReader is an ordinary cancellable input boundary shared by the
// disk scan and JSON decoder. Each underlying read is bounded, including while
// decoding a large record. Callers can compose any io.Reader with ScanJSONLRange.
type archiveContextReader struct {
	ctx     context.Context
	r       io.Reader
	readErr error
}

func (r *archiveContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p[:min(len(p), archiveReadChunk)])
	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}
	return n, err
}

// ScanJSONLRange is the raw read/decode boundary for archive-range recall. It
// validates every selected record even after the caller has collected its page.
// A callback error, read/decode fault or cancellation invalidates the whole scan.
func ScanJSONLRange(ctx context.Context, r io.Reader, from, to int, fn func(int, []byte, ArchivedMessage) error) (scanErr error) {
	if from < 0 || to < from {
		return fmt.Errorf("archive_range must have 0 <= from <= to, got [%d,%d]", from, to)
	}
	input := &archiveContextReader{ctx: ctx, r: r}
	defer func() {
		// ReadSlice can return a complete last selected line before exposing an
		// underlying (n > 0, err != nil) fault. Never lose that error at to.
		if input.readErr != nil && !errors.Is(scanErr, input.readErr) {
			scanErr = errors.Join(scanErr, fmt.Errorf("memory: read archive: %w", input.readErr))
		}
	}()
	reader := bufio.NewReaderSize(input, archiveReadChunk)
	var scratch []byte
	for idx := 0; ; {
		raw, err := readArchiveRecord(ctx, reader, scratch[:0])
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("archive_range [%d,%d] outside available archive records (%d records)", from, to, idx)
		}
		if err != nil {
			return fmt.Errorf("memory: read archive record %d: %w", idx, err)
		}
		scratch = raw
		// Empty physical lines are not records, matching the store's Count/Skip.
		if len(bytes.TrimSuffix(bytes.TrimSuffix(raw, []byte{'\n'}), []byte{'\r'})) == 0 {
			continue
		}
		if idx >= from {
			msg, err := decodeArchiveRecord(ctx, raw)
			if err != nil {
				return fmt.Errorf("memory: decode archive record %d: %w", idx, err)
			}
			if err := fn(idx, raw, msg); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if idx == to {
			return nil
		}
		idx++
	}
}

// readArchiveRecord preserves LF/CRLF and an unterminated final record. Buffer
// growth never exceeds the existing D10 admitted-record bound plus a delimiter.
func readArchiveRecord(ctx context.Context, r *bufio.Reader, record []byte) ([]byte, error) {
	const limit = EncodedLineBound + 2 // the original CRLF, if present
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > limit-len(record) {
			return nil, fmt.Errorf("archive record exceeds size bound %d bytes", EncodedLineBound)
		}
		needed := len(record) + len(chunk)
		if needed > cap(record) {
			next := min(limit, max(archiveReadChunk, max(needed, 2*cap(record))))
			grown := make([]byte, len(record), next)
			copy(grown, record)
			record = grown
		}
		record = append(record, chunk...)
		switch {
		case err == nil:
			return record, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(record) > 0:
			return record, nil
		default:
			return nil, err
		}
	}
}

func decodeArchiveRecord(ctx context.Context, raw []byte) (ArchivedMessage, error) {
	if err := ctx.Err(); err != nil {
		return ArchivedMessage{}, err
	}
	if !utf8.Valid(raw) {
		return ArchivedMessage{}, fmt.Errorf("archive record is not valid UTF-8")
	}
	decoder := json.NewDecoder(&archiveContextReader{ctx: ctx, r: bytes.NewReader(raw)})
	var msg ArchivedMessage
	if err := decoder.Decode(&msg); err != nil {
		return ArchivedMessage{}, err
	}
	// Decode must consume exactly one JSON value, not accept a valid prefix of
	// malformed data or a concatenated second record on the same physical line.
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return ArchivedMessage{}, err
		}
		return ArchivedMessage{}, fmt.Errorf("archive record contains multiple JSON values")
	}
	return msg, ctx.Err()
}
