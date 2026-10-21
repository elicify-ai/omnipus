// archive_day_store.go: the addressed day-partitioned archive — the
// append-and-seek half of session-core C-ARCHIVE / U2 Decisions B and C
// (spec FR-004/FR-005) and the ONE append path of a session directory (effects
// design D1/D2/D7): chat records, model payloads, model_refs and tool-call
// effects are all appended here, under the same file set and the same lock.
//
// Layout, inside the session directory `<baseDir>/<sessionID>/`:
//
//	transcript.jsonl   the live partition (the day mark holds its key)
//	transcript.day     the day mark: the UTC day transcript.jsonl currently holds
//	<key>.jsonl        rolled partitions; a pre-marker rename still belongs to
//	                  the retained marker and can accept a same-key recovery append
//
// Rollover is by the SERVER's append UTC day (never an entry's own timestamp)
// and is crash-safe by ordering: the rename happens BEFORE the day mark
// advances, and the partition key a record is addressed by is the DAY it was
// written under, which survives the rename unchanged. A rolled name that
// already exists is refused visibly: it would re-point every address already
// issued for the older file. Append captures the pre-write end offset, so the
// returned address seeks back to exactly that record; a torn final line gets a
// newline first, and a failed write is truncated back, so a returned address is
// always a complete, newline-framed record and unpublished bytes never linger.
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/memory"
)

const (
	// archiveCurrentFile is the live partition file name.
	archiveCurrentFile = transcriptFileName
	// archiveDayMarkFile is the disk-only day mark (never on the wire).
	archiveDayMarkFile = transcriptDayMarkFile
	// archiveRecordBound caps one encoded envelope, reusing the archive's
	// existing 8 MiB encoded-line admission bound rather than enlarging it.
	archiveRecordBound = memory.EncodedLineBound
)

// ArchiveDayStore appends archive envelopes to, and seeks addressed records
// from, one session's new archive. It is not safe for concurrent use across
// processes; a single in-process mutex serializes its writes. Clock is
// injectable so "the server's current append UTC day" is deterministic in tests.
type ArchiveDayStore struct {
	mu        sync.Mutex
	baseDir   string
	sessionID string
	now       func() time.Time
	// onRead, when set (tests only), observes every archive read: kind "record"
	// for one addressed read, "scan" for a whole-archive walk. It is how a test
	// proves a hot path stays proportional to the window (FR-005).
	onRead func(kind string, addr ArchiveAddress)
	// onWriteStep is nil outside tests; observation only, never an I/O substitute.
	onWriteStep func(step string)
}

// NewArchiveDayStore binds a store to one session directory. The session id must
// be a single path element (it names the directory), so a value carrying a
// separator or a ".." element is refused loudly rather than escaping baseDir.
func NewArchiveDayStore(baseDir, sessionID string) (*ArchiveDayStore, error) {
	if sessionID == "" {
		return nil, errors.New("archive: empty session id")
	}
	if sessionID == "." || sessionID == ".." || strings.ContainsRune(sessionID, os.PathSeparator) ||
		strings.ContainsRune(sessionID, '/') || strings.ContainsRune(sessionID, '\\') {
		return nil, fmt.Errorf("archive: invalid session id %q", sessionID)
	}
	return &ArchiveDayStore{
		baseDir:   baseDir,
		sessionID: sessionID,
		now:       func() time.Time { return time.Now() },
	}, nil
}

func (s *ArchiveDayStore) dir() string {
	return filepath.Join(s.baseDir, s.sessionID)
}

// Append validates one envelope, appends it to the current partition with
// newline framing and fsync, and returns its exact address. A record that fails
// validation, exceeds the encoded bound, or cannot be written returns an error
// and writes nothing — there is no partial-success append.
func (s *ArchiveDayStore) Append(rec ArchiveRecord) (ArchiveAddress, error) {
	line, err := encodeArchiveLine(rec)
	if err != nil {
		return ArchiveAddress{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLineLocked(rec.ID, line)
}

// encodeArchiveLine validates and encodes one envelope; the bound is the whole
// new envelope, model_message and source included.
func encodeArchiveLine(rec ArchiveRecord) ([]byte, error) {
	if err := rec.Validate(); err != nil {
		return nil, err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("archive: encode record %s: %w", rec.ID, err)
	}
	if len(line) > archiveRecordBound {
		return nil, fmt.Errorf("archive: record %s is %d bytes, over the %d-byte envelope bound",
			rec.ID, len(line), archiveRecordBound)
	}
	return line, nil
}

// appendLineLocked writes one encoded envelope to the current partition. s.mu
// must be held. A non-empty partition that does not end in a newline (a torn
// final line) gets one first, so the record starts on its own line and the
// returned address is the record's first byte; a write or fsync failure
// truncates the partition back to its pre-append size, so bytes that were never
// published are not left behind (they are not retained bytes: no address was
// returned for them).
func (s *ArchiveDayStore) appendLineLocked(id string, line []byte) (ArchiveAddress, error) {
	s.observeWriteStep("append.prepared")
	if err := s.resumeInputDeletionsLocked(); err != nil {
		return ArchiveAddress{}, err
	}
	key, path, err := s.partitionForAppendLocked(s.now().UTC().Format(transcriptDayLayout))
	if err != nil {
		return ArchiveAddress{}, err
	}
	s.observeWriteStep("append.partition_selected")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("archive: open %s: %w", path, err)
	}
	defer func() {
		_ = f.Close()
		s.observeWriteStep("append.current_closed")
	}()
	s.observeWriteStep("append.current_opened")
	pre, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("archive: seek end %s: %w", path, err)
	}
	s.observeWriteStep("append.end_located")
	offset := pre
	payload := make([]byte, 0, len(line)+2)
	if pre > 0 {
		last := make([]byte, 1)
		if _, rerr := f.ReadAt(last, pre-1); rerr != nil {
			return ArchiveAddress{}, fmt.Errorf("archive: read last byte of %s: %w", path, rerr)
		} else if last[0] != '\n' {
			slog.Warn("archive: partition did not end in a newline; starting the new record on its own line",
				"session_id", s.sessionID, "partition", key)
			payload = append(payload, '\n')
			offset++
		}
		s.observeWriteStep("append.tail_read")
	}
	payload = append(payload, line...)
	payload = append(payload, '\n')
	if _, werr := f.Write(payload); werr != nil {
		return ArchiveAddress{}, errors.Join(fmt.Errorf("archive: write %s: %w", path, werr), truncateBack(f, pre))
	}
	s.observeWriteStep("append.payload_written")
	if serr := f.Sync(); serr != nil {
		return ArchiveAddress{}, errors.Join(fmt.Errorf("archive: fsync %s: %w", path, serr), truncateBack(f, pre))
	}
	s.observeWriteStep("append.payload_synced")
	return ArchiveAddress{PartitionKey: key, ByteOffset: offset, EntryID: id}, nil
}

// ReadAt seeks straight to the addressed record and decodes exactly that one
// line. It never reads, decodes or counts the bytes before the offset — the
// bounded-read guarantee of Decision C. A missing partition, a short read, a
// non-JSON line or an id that does not match the mark is an error, never a
// silent wrong-record or empty-history result.
func (s *ArchiveDayStore) ReadAt(addr ArchiveAddress) (ArchiveRecord, error) {
	_, rec, err := s.ReadAtRaw(addr)
	return rec, err
}

// ReadAtRaw is ReadAt plus the record's exact stored line (without the framing
// newline), so a caller can quote a stored value literally without re-encoding.
func (s *ArchiveDayStore) ReadAtRaw(addr ArchiveAddress) ([]byte, ArchiveRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAtRawLocked(addr)
}

// readAtRawLocked is the shared bounded address reader. The caller holds mu.
func (s *ArchiveDayStore) readAtRawLocked(addr ArchiveAddress) ([]byte, ArchiveRecord, error) {
	if addr.EntryID == "" || addr.PartitionKey == "" || addr.ByteOffset < 0 {
		return nil, ArchiveRecord{}, fmt.Errorf("archive: incomplete address %+v", addr)
	}
	if s.onRead != nil {
		s.onRead("record", addr)
	}
	path, err := s.partitionPathForKeyLocked(addr.PartitionKey)
	if err != nil {
		return nil, ArchiveRecord{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ArchiveRecord{}, fmt.Errorf("archive: open partition %s: %w", addr.PartitionKey, err)
	}
	defer f.Close()
	if _, err = f.Seek(addr.ByteOffset, io.SeekStart); err != nil {
		return nil, ArchiveRecord{}, fmt.Errorf("archive: seek %d in %s: %w", addr.ByteOffset, addr.PartitionKey, err)
	}
	line, rec, err := readArchiveLineBounded(f)
	if err != nil {
		return nil, ArchiveRecord{}, fmt.Errorf("archive: read %s@%d: %w", addr.PartitionKey, addr.ByteOffset, err)
	}
	if rec.ID != addr.EntryID {
		return nil, ArchiveRecord{}, fmt.Errorf("archive: address %s@%d resolves to record %q, not %q (corrupt or stale mark)",
			addr.PartitionKey, addr.ByteOffset, rec.ID, addr.EntryID)
	}
	return line, rec, nil
}

// ReadAddrs resolves several addresses in order. Each is one bounded seek; the
// caller may pass a contiguous suffix, which is read with one open per address
// rather than a single whole-partition scan.
func (s *ArchiveDayStore) ReadAddrs(addrs []ArchiveAddress) ([]ArchiveRecord, error) {
	out := make([]ArchiveRecord, 0, len(addrs))
	for _, a := range addrs {
		rec, err := s.ReadAt(a)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// partitionForAppendLocked returns the partition key and path an append under
// UTC day `day` must use, performing a day rollover when `day` is newer than
// the persisted mark. A delayed/older `day` never moves the archive backward.
func (s *ArchiveDayStore) partitionForAppendLocked(day string) (key, path string, err error) {
	dir := s.dir()
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("archive: create dir %s: %w", dir, err)
	}
	s.observeWriteStep("rollover.directory_ready")
	current := filepath.Join(dir, archiveCurrentFile)
	mark, err := s.readDayMarkLocked()
	if err != nil {
		return "", "", err
	}
	s.observeWriteStep("rollover.marker_read")
	if mark == "" {
		if err = s.writeDayMarkLocked(day); err != nil {
			return "", "", err
		}
		return day, current, nil
	}
	if day <= mark {
		// An interrupted rename may already have moved the marked partition.
		// Continue at its actual location instead of creating a second file
		// for the same key when the clock has not advanced beyond the mark.
		path, resolveErr := s.partitionPathForKeyLocked(mark)
		if resolveErr != nil {
			return "", "", resolveErr
		}
		if _, statErr := os.Stat(path); statErr != nil {
			if !errors.Is(statErr, fs.ErrNotExist) {
				return "", "", fmt.Errorf("archive: stat append partition %s: %w", mark, statErr)
			}
			if err := s.checkMissingCurrentPublicationLocked(nil); err != nil {
				return "", "", err
			}
		}
		return mark, path, nil
	}
	if _, statErr := os.Stat(current); statErr == nil {
		s.observeWriteStep("rollover.current_stated")
		rolled := filepath.Join(dir, mark+".jsonl")
		// A record is addressed by its partition key, and the rolled file is
		// named by that same key. A colliding name would silently re-point every
		// address already issued for the older file, so it is refused visibly
		// (an out-of-band file, or a rollover that did not complete).
		if _, collideErr := os.Stat(rolled); collideErr == nil {
			return "", "", fmt.Errorf("archive: roll day %s: %s already exists; refusing to re-point issued record addresses", mark, filepath.Base(rolled))
		} else if !errors.Is(collideErr, os.ErrNotExist) {
			return "", "", fmt.Errorf("archive: stat rolled partition name: %w", collideErr)
		}
		s.observeWriteStep("rollover.destination_checked")
		if err = os.Rename(current, rolled); err != nil {
			return "", "", fmt.Errorf("archive: roll day %s: %w", mark, err)
		}
		s.observeWriteStep("rollover.renamed")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", fmt.Errorf("archive: stat current before rollover: %w", statErr)
	}
	if err = s.writeDayMarkLocked(day); err != nil {
		return "", "", err
	}
	return day, current, nil
}

// partitionPathForKeyLocked resolves a stable key against both physical names.
// The marked key normally lives in current, but a rename may already have moved
// it to its dated file before marker publication. Two claimants are ambiguous.
func (s *ArchiveDayStore) partitionPathForKeyLocked(key string) (string, error) {
	dir := s.dir()
	mark, err := s.readDayMarkLocked()
	if err != nil {
		return "", err
	}
	cand := filepath.Join(dir, key+".jsonl")
	_, datedErr := os.Stat(cand)
	if datedErr != nil && !errors.Is(datedErr, fs.ErrNotExist) {
		return "", fmt.Errorf("archive: stat partition %s: %w", key, datedErr)
	}
	if key == mark {
		current := filepath.Join(dir, archiveCurrentFile)
		_, currentErr := os.Stat(current)
		if currentErr != nil && !errors.Is(currentErr, fs.ErrNotExist) {
			return "", fmt.Errorf("archive: stat current partition %s: %w", key, currentErr)
		}
		if datedErr == nil {
			if currentErr == nil {
				return "", fmt.Errorf("archive: partition %q is ambiguous: current and dated files both exist", key)
			}
			// Rename completed but marker publication did not. The same stable
			// key still resolves to its intact dated file; reads do not repair
			// the marker or manufacture an empty replacement current file.
			return cand, nil
		}
		// Preserve the addressed open's missing-file cause for an actually
		// absent current partition (including the existing SFH-3 path).
		return current, nil
	}
	if datedErr == nil {
		return cand, nil
	}
	return "", fmt.Errorf("archive: no partition for key %q: %w", key, errNoPartition)
}

// errNoPartition marks a partition key whose file is provably gone (swept by
// retention). It is the only partition failure that means "expired"; any other
// failure to resolve or read a partition is an error, never expiry.
var errNoPartition = errors.New("partition is gone")

// partitionGone reports whether err proves the addressed partition no longer
// exists, as opposed to an I/O or corruption error that must surface.
func partitionGone(err error) bool {
	return errors.Is(err, errNoPartition) || errors.Is(err, fs.ErrNotExist)
}

func (s *ArchiveDayStore) readDayMarkLocked() (string, error) {
	b, err := os.ReadFile(filepath.Join(s.dir(), archiveDayMarkFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("archive: read day mark: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func (s *ArchiveDayStore) writeDayMarkLocked(day string) error {
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return fmt.Errorf("archive: create dir: %w", err)
	}
	s.observeWriteStep("marker.directory_ready")
	if err := fileutil.WriteFileAtomic(filepath.Join(s.dir(), archiveDayMarkFile), []byte(day+"\n"), 0o600); err != nil {
		return fmt.Errorf("archive: write day mark: %w", err)
	}
	s.observeWriteStep("marker.write_complete")
	return nil
}

// rolledPartitionNames returns the frozen day partitions among entries, in
// chronological order. Only date-named files are partitions: the session
// directory holds other files that are not archive content.
func rolledPartitionNames(entries []os.DirEntry) []string {
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == archiveCurrentFile || !transcriptDayPattern.MatchString(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// readArchiveRecordBounded reads exactly one newline-framed record from r,
// bounded by archiveRecordBound. A missing newline inside the bound means the
// record is longer than the admission bound (or the offset is corrupt); both
// are errors.
func readArchiveRecordBounded(r io.Reader) (ArchiveRecord, error) {
	_, rec, err := readArchiveLineBounded(r)
	return rec, err
}

// readArchiveLineBounded is readArchiveRecordBounded that also returns the
// record's exact line bytes (framing newline trimmed).
func readArchiveLineBounded(r io.Reader) ([]byte, ArchiveRecord, error) {
	br := bufio.NewReaderSize(io.LimitReader(r, int64(archiveRecordBound)+1), 64*1024)
	line, err := br.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, ArchiveRecord{}, err
	}
	truncated := !bytes.HasSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\n'})
	if len(bytes.TrimSpace(line)) == 0 {
		return nil, ArchiveRecord{}, errors.New("no record at address")
	}
	if truncated {
		return nil, ArchiveRecord{}, fmt.Errorf("record exceeds the %d-byte bound or is not newline-framed", archiveRecordBound)
	}
	var rec ArchiveRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil, ArchiveRecord{}, fmt.Errorf("decode record: %w", err)
	}
	return line, rec, nil
}
