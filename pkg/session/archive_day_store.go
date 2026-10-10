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
//	<key>.jsonl        every rolled-over partition, frozen once rolled
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
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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
	key, path, err := s.partitionForAppendLocked(s.now().UTC().Format(transcriptDayLayout))
	if err != nil {
		return ArchiveAddress{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("archive: open %s: %w", path, err)
	}
	defer f.Close()
	pre, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("archive: seek end %s: %w", path, err)
	}
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
	}
	payload = append(payload, line...)
	payload = append(payload, '\n')
	if _, werr := f.Write(payload); werr != nil {
		return ArchiveAddress{}, errors.Join(fmt.Errorf("archive: write %s: %w", path, werr), truncateBack(f, pre))
	}
	if serr := f.Sync(); serr != nil {
		return ArchiveAddress{}, errors.Join(fmt.Errorf("archive: fsync %s: %w", path, serr), truncateBack(f, pre))
	}
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
	if addr.EntryID == "" || addr.PartitionKey == "" || addr.ByteOffset < 0 {
		return nil, ArchiveRecord{}, fmt.Errorf("archive: incomplete address %+v", addr)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	current := filepath.Join(dir, archiveCurrentFile)
	mark, err := s.readDayMarkLocked()
	if err != nil {
		return "", "", err
	}
	if mark == "" {
		if err = s.writeDayMarkLocked(day); err != nil {
			return "", "", err
		}
		return day, current, nil
	}
	if day <= mark {
		return mark, current, nil
	}
	if _, statErr := os.Stat(current); statErr == nil {
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
		if err = os.Rename(current, rolled); err != nil {
			return "", "", fmt.Errorf("archive: roll day %s: %w", mark, err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", fmt.Errorf("archive: stat current before rollover: %w", statErr)
	}
	if err = s.writeDayMarkLocked(day); err != nil {
		return "", "", err
	}
	return day, current, nil
}

// partitionPathForKeyLocked maps a partition key to the file that holds it. The
// current partition's key equals the day mark; a rolled partition's key is its
// date-prefixed file name.
func (s *ArchiveDayStore) partitionPathForKeyLocked(key string) (string, error) {
	dir := s.dir()
	mark, err := s.readDayMarkLocked()
	if err != nil {
		return "", err
	}
	if key == mark {
		return filepath.Join(dir, archiveCurrentFile), nil
	}
	cand := filepath.Join(dir, key+".jsonl")
	if _, err := os.Stat(cand); err == nil {
		return cand, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("archive: stat partition %s: %w", key, err)
	}
	return "", fmt.Errorf("archive: no partition for key %q", key)
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
	tmp := filepath.Join(s.dir(), archiveDayMarkFile+".tmp")
	if err := os.WriteFile(tmp, []byte(day+"\n"), 0o600); err != nil {
		return fmt.Errorf("archive: write day mark: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir(), archiveDayMarkFile)); err != nil {
		return fmt.Errorf("archive: rename day mark: %w", err)
	}
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
