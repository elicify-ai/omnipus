package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ChatHistoryReadMetrics is request-owned actual work, shared with the gateway.
// It contains no content, identity, address or error cause. One request uses it
// serially; it is not a process-wide allocation or I/O estimate.
type ChatHistoryReadMetrics struct {
	SelectedArchiveRecords, SelectedArchiveBytes                                                     int64
	LookaheadArchiveRecords, LookaheadArchiveBytes                                                   int64
	DependencyArchiveRecords, DependencyArchiveBytes                                                 int64
	RecoveryArchiveRecords, RecoveryArchiveBytes                                                     int64
	DiscardedArchiveRecords, DiscardedArchiveBytes                                                   int64
	DispositionRecordsRead, DispositionBytesRead, IndexBytesRead                                     int64
	DependencyCount, RecoveryItems, RecoveryAnchorRecords, RecoveryOverflowRecords, RetentionRetries int64
}
type historyMetricsKey struct{}
type historyDependencyKey struct {
	stream string
	item   uint64
}

// One resolved fact counts once at this immutable cut, even when cumulative
// candidates reuse it. Physical rereads remain separate actual I/O counters.
func (t *chatHistoryIndexTree) resolvedDependency(stream string, item uint64) {
	m := historyMetrics(t.ctx)
	if m == nil {
		return
	}
	key := historyDependencyKey{stream: stream, item: item}
	if _, exists := t.dependencies[key]; exists {
		return
	}
	if t.dependencies == nil {
		t.dependencies = make(map[historyDependencyKey]struct{})
	}
	t.dependencies[key] = struct{}{}
	m.DependencyCount++
}

func WithChatHistoryReadMetrics(ctx context.Context, m *ChatHistoryReadMetrics) context.Context {
	return context.WithValue(ctx, historyMetricsKey{}, m)
}
func historyMetrics(ctx context.Context) *ChatHistoryReadMetrics {
	m, _ := ctx.Value(historyMetricsKey{}).(*ChatHistoryReadMetrics)
	return m
}
func historyIndexBytes(ctx context.Context, n int64) {
	if m := historyMetrics(ctx); m != nil {
		m.IndexBytesRead += n
	}
}
func historyReadEvent(ctx context.Context, e chatHistoryReadEvent) error {
	if chatHistoryReadHook != nil {
		if err := chatHistoryReadHook(ctx, e); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func historyPublication(e chatHistoryPublicationEvent) error {
	if chatHistoryPublicationHook != nil {
		return chatHistoryPublicationHook(e)
	}
	return nil
}
func historyUnavailable(reason string, err error) error {
	return &ChatHistoryUnavailableError{Reason: reason, cause: err}
}
func historyDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Open and close for each bounded content read. Resolving a current key after
// rollover never binds that old key to the new current file. No append mutex is
// held while the actual opened handle is read or a test barrier runs.
func historyArchivePath(dir, key string) (string, error) {
	if key == "" || strings.ContainsAny(key, "/\\") || key == "." || key == ".." {
		return "", errors.New("invalid archive partition key")
	}
	mark, err := os.ReadFile(filepath.Join(dir, archiveDayMarkFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if strings.TrimSpace(string(mark)) == key {
		return filepath.Join(dir, archiveCurrentFile), nil
	}
	return filepath.Join(dir, key+".jsonl"), nil
}
func historyReadBytes(ctx context.Context, path string, off, length int64, e chatHistoryReadEvent) (b []byte, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if off < 0 || length < 0 || length > historyFrameBound {
		return nil, errors.New("invalid history read bounds")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	e.Phase = "record_opened"
	e.Offset = off
	defer func() {
		closeErr := f.Close()
		err = errors.Join(err, closeErr)
		e.Phase = "record_closed"
		e.Bytes = 0
		// A cancellation does not prevent the closed-handle observation.
		if chatHistoryReadHook != nil {
			err = errors.Join(err, chatHistoryReadHook(ctx, e))
		}
	}()
	if err = historyReadEvent(ctx, e); err != nil {
		return nil, err
	}
	b = make([]byte, length)
	n, readErr := f.ReadAt(b, off)
	b = b[:n]
	e.Phase = "record_read"
	e.Bytes = int64(n)
	if m := historyMetrics(ctx); m != nil {
		if e.Stream == "archive" {
			switch {
			case e.Scope == "recovery":
				m.RecoveryArchiveRecords++
				m.RecoveryArchiveBytes += int64(n)
			case e.Purpose == "dependency":
				m.DependencyArchiveRecords++
				m.DependencyArchiveBytes += int64(n)
			default:
				m.SelectedArchiveRecords++
				m.SelectedArchiveBytes += int64(n)
			}
		}
		if e.Stream == "disposition" {
			m.DispositionRecordsRead++
			m.DispositionBytesRead += int64(n)
		}
		if e.Scope == "recovery" && e.Purpose == "anchor" {
			m.RecoveryAnchorRecords++
		}
		if e.Scope == "recovery" && e.Purpose == "overflow" {
			m.RecoveryOverflowRecords++
		}
	}
	err = errors.Join(readErr, historyReadEvent(ctx, e))
	if err != nil {
		return b, err
	}
	if int64(n) != length {
		return b, io.ErrUnexpectedEOF
	}
	return b, nil
}

func historyReadArchive(ctx context.Context, dir string, r historyRecord, cut ChatHistoryReadCut, scope, purpose string) (ArchiveRecord, error) {
	var rec ArchiveRecord
	path, err := historyArchivePath(dir, r.Address.PartitionKey)
	if err != nil {
		return rec, err
	}
	b, err := historyReadBytes(ctx, path, r.Address.ByteOffset, r.Length, chatHistoryReadEvent{SessionID: cut.sessionID, Scope: scope, Purpose: purpose, Cut: cut, Stream: "archive", Address: &r.Address})
	if err != nil {
		return rec, err
	}
	if historyDigest(b) != r.Digest {
		return rec, errors.New("history canonical record changed")
	}
	rec, err = historyDecodeArchive(b)
	if err == nil && rec.ID != r.Address.EntryID {
		err = errors.New("history checked address mismatch")
	}
	return rec, err
}
