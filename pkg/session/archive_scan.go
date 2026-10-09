// archive_scan.go: the ordered, address-returning scan of a session's addressed
// archive — the primitive the archive backend (archive_backend.go) and the raw
// recall path need to enumerate records without knowing their addresses ahead of
// time. Session-core C-ARCHIVE / U2 (FR-004/FR-005).
//
// Order is APPEND ORDER: rolled partitions (sorted by their date-prefixed key)
// then the live current partition, exactly the order the records were written.
// Each yielded address is the record's exact (partition_key, byte_offset,
// entry_id) mark, so a later read is a bounded seek. A corrupt line is skipped
// WITHOUT consuming a payload ordinal, mirroring the existing JSONL reader's
// log-and-continue recovery, so ordinals never shift under damage.
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScanAll walks every record in this session's archive in append order, calling
// fn with the record's exact address and decoded envelope. fn returning false
// stops the scan. A missing archive directory scans zero records.
func (s *ArchiveDayStore) ScanAll(fn func(ArchiveAddress, ArchiveRecord) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanAllLocked(fn)
}

func (s *ArchiveDayStore) scanAllLocked(fn func(ArchiveAddress, ArchiveRecord) bool) error {
	dir := s.dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("archive: scan: read dir: %w", err)
	}
	var rolled []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") || name == archiveCurrentFile {
			continue
		}
		rolled = append(rolled, name)
	}
	sort.Strings(rolled)
	for _, name := range rolled {
		key := strings.TrimSuffix(name, ".jsonl")
		cont, err := s.scanPartitionLocked(filepath.Join(dir, name), key, fn)
		if err != nil {
			return err
		}
		if !cont {
			return nil
		}
	}
	mark, err := s.readDayMarkLocked()
	if err != nil {
		return err
	}
	if mark == "" {
		return nil
	}
	cur := filepath.Join(dir, archiveCurrentFile)
	if _, statErr := os.Stat(cur); statErr == nil {
		_, err = s.scanPartitionLocked(cur, mark, fn)
		return err
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("archive: scan: stat current: %w", statErr)
	}
	return nil
}

// scanPartitionLocked walks one partition file in byte order, computing each
// record's byte offset. It returns false when the caller asked to stop. Empty
// lines are skipped; a corrupt line is skipped without yielding an address.
func (s *ArchiveDayStore) scanPartitionLocked(path, key string, fn func(ArchiveAddress, ArchiveRecord) bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("archive: scan: read %s: %w", path, err)
	}
	off := int64(0)
	for off < int64(len(data)) {
		rest := data[off:]
		lineEnd := bytes.IndexByte(rest, '\n')
		var line []byte
		var advance int64
		if lineEnd < 0 {
			line = rest
			advance = int64(len(rest))
		} else {
			line = rest[:lineEnd]
			advance = int64(lineEnd) + 1
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			off += advance
			continue
		}
		var rec ArchiveRecord
		if err := json.Unmarshal(trimmed, &rec); err != nil {
			// Corrupt record: skip it WITHOUT consuming an ordinal, matching the
			// existing JSONL reader's recovery so addresses never shift.
			off += advance
			continue
		}
		if !fn(ArchiveAddress{PartitionKey: key, ByteOffset: off, EntryID: rec.ID}, rec) {
			return false, nil
		}
		off += advance
	}
	return true, nil
}

// scanPartitionLinesLocked walks one partition file and yields each
// non-empty physical line's EXACT raw bytes plus its decoded envelope. It is
// the literal-bytes primitive the raw recall path needs; a corrupt line is
// skipped without consuming an ordinal.
func (s *ArchiveDayStore) scanPartitionLinesLocked(path, key string, fn func(ArchiveAddress, []byte, ArchiveRecord) bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("archive: scan: read %s: %w", path, err)
	}
	off := int64(0)
	for off < int64(len(data)) {
		rest := data[off:]
		lineEnd := bytes.IndexByte(rest, '\n')
		var line []byte
		var advance int64
		if lineEnd < 0 {
			line = rest
			advance = int64(len(rest))
		} else {
			line = rest[:lineEnd]
			advance = int64(lineEnd) + 1
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			off += advance
			continue
		}
		var rec ArchiveRecord
		if err := json.Unmarshal(trimmed, &rec); err != nil {
			off += advance
			continue
		}
		if !fn(ArchiveAddress{PartitionKey: key, ByteOffset: off, EntryID: rec.ID}, trimmed, rec) {
			return false, nil
		}
		off += advance
	}
	return true, nil
}
